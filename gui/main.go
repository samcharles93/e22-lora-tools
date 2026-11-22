// Command e22gui is a desktop app for the EBYTE E22-900T22U LoRa dongle.
package main

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/samcharles93/e22-lora-tools/e22"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"
	"github.com/egoist/mygo/ui/markdown"
)

const maxPackets = 500

//go:embed help.md
var helpText string

const helpTab = 3

type packet struct {
	at   time.Time
	data []byte
	rssi string
}

// app is the window's state. The view reads it on the main thread. Serial
// work runs in one job goroutine at a time (busy), which owns dev while it
// runs and publishes results through update.
type app struct {
	// Hooks, swapped in tests.
	open       func(name string) (*e22.Device, io.Closer, error)
	spawn      func(func())
	update     func(func())
	saveRegion func(string)

	region string // a name from e22.Regions, or noRegion

	ports  []string
	port   string
	dev    *e22.Device
	closer io.Closer
	busy   bool
	status string
	err    string
	tab    int
	md     markdown.Options

	// Settings, edited in the form and written on Apply.
	cfg                                  *e22.Config
	channel, addr, netid                 float64
	air, power                           string
	packetRSSI, noiseRSSI, softMode, lbt bool
	fixed, temp                          bool
	model, firmware, pid                 string

	// Scan.
	scanFrom, scanTo, scanSamples float64
	scanRows                      []e22.ScanRow
	stop                          chan struct{}

	// Listen.
	listening bool
	showRSSI  bool
	packets   []packet
	sendText  string
}

func newApp() *app {
	a := &app{
		open: func(name string) (*e22.Device, io.Closer, error) {
			d, p, err := e22.Open(name)
			return d, p, err
		},
		spawn:       func(f func()) { go f() },
		saveRegion:  saveRegion,
		region:      loadRegion(),
		update:      func(f func()) { f() },
		scanFrom:    65,
		scanTo:      77,
		scanSamples: 10,
		showRSSI:    true,
	}
	a.refreshPorts()
	a.applyRegion()
	return a
}

const noRegion = "None"

func regionNames() []string {
	names := []string{noRegion}
	for _, r := range e22.Regions {
		names = append(names, r.Name)
	}
	return names
}

// regionFile holds the chosen region between runs.
func regionFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "e22-lora-tools", "region")
}

func loadRegion() string {
	if f := regionFile(); f != "" {
		if b, err := os.ReadFile(f); err == nil {
			if _, ok := e22.RegionByName(strings.TrimSpace(string(b))); ok {
				return strings.TrimSpace(string(b))
			}
		}
	}
	return noRegion
}

func saveRegion(name string) {
	f := regionFile()
	if f == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err == nil {
		_ = os.WriteFile(f, []byte(name+"\n"), 0o644)
	}
}

// applyRegion points the scan range at the region's band.
func (a *app) applyRegion() {
	if r, ok := e22.RegionByName(a.region); ok {
		a.scanFrom, a.scanTo = float64(r.First), float64(r.Last)
	}
}

// bandWarning flags a channel outside the chosen region.
func (a *app) bandWarning() string {
	r, ok := e22.RegionByName(a.region)
	if !ok || r.Contains(byte(a.channel)) {
		return ""
	}
	return "Outside " + r.Name
}

func (a *app) refreshPorts() {
	a.ports = e22.Ports()
	if len(a.ports) > 0 && !contains(a.ports, a.port) {
		a.port = a.ports[0]
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// job runs work off the main thread and reports its outcome in the status
// line. done, if set, runs on the main thread after work succeeds.
func (a *app) job(label string, work func(d *e22.Device) error, done func()) {
	if a.busy {
		return
	}
	a.busy, a.status, a.err = true, label+"…", ""
	d := a.dev
	a.spawn(func() {
		err := work(d)
		a.update(func() {
			a.busy, a.listening, a.stop = false, false, nil
			if err != nil && !errors.Is(err, e22.ErrStopped) {
				a.status, a.err = "", err.Error()
				return
			}
			a.status = label + ": done"
			if done != nil {
				done()
			}
		})
	})
}

func (a *app) connect() {
	name := a.port
	a.busy, a.status, a.err = true, "Opening "+name+"…", ""
	a.spawn(func() {
		d, cl, err := a.open(name)
		a.update(func() {
			a.busy = false
			if err != nil {
				a.status, a.err = "", err.Error()
				return
			}
			a.dev, a.closer, a.status = d, cl, "Connected to "+name
		})
	})
}

func (a *app) disconnect() {
	if a.closer != nil {
		_ = a.closer.Close()
	}
	a.dev, a.closer, a.cfg = nil, nil, nil
	a.status = "Disconnected"
}

// load copies a configuration into the form.
func (a *app) load(c e22.Config) {
	a.cfg = &c
	a.channel, a.addr, a.netid = float64(c.Channel), float64(c.Addr), float64(c.NetID)
	a.air = e22.AirRates[c.AirRateIdx]
	a.power = powerLabel(e22.Powers[c.PowerIdx])
	a.packetRSSI, a.noiseRSSI, a.softMode = c.PacketRSSI, c.NoiseRSSI, c.SoftMode
	a.lbt, a.fixed = c.LBT, c.FixedPoint
}

func powerLabel(dBm int) string { return strconv.Itoa(dBm) + " dBm" }

func powerLabels() []string {
	var s []string
	for _, p := range e22.Powers {
		s = append(s, powerLabel(p))
	}
	return s
}

// edited returns the loaded configuration with the form's changes.
func (a *app) edited() e22.Config {
	c := *a.cfg
	c.Channel = byte(a.channel)
	c.Addr = uint16(a.addr)
	c.NetID = byte(a.netid)
	for i, r := range e22.AirRates {
		if r == a.air && i >= 2 {
			c.AirRateIdx = byte(i)
			break
		}
	}
	for i, p := range e22.Powers {
		if powerLabel(p) == a.power {
			c.PowerIdx = byte(i)
		}
	}
	c.PacketRSSI, c.NoiseRSSI, c.SoftMode = a.packetRSSI, a.noiseRSSI, a.softMode
	c.LBT, c.FixedPoint = a.lbt, a.fixed
	return c
}

func (a *app) read() {
	var c e22.Config
	var model, fw, pid string
	a.job("Read settings", func(d *e22.Device) error {
		var err error
		if c, err = d.ReadConfig(); err != nil {
			return err
		}
		if b, err := d.ReadPID(); err == nil {
			pid = fmt.Sprintf("% X", b)
		} else {
			pid = unsupported(err)
		}
		if model, err = d.AT(e22.ATDevType); err != nil {
			model = unsupported(err)
		}
		if fw, err = d.AT(e22.ATFWCode); err != nil {
			fw = unsupported(err)
		}
		return nil
	}, func() {
		a.load(c)
		a.model, a.firmware, a.pid = model, fw, pid
	})
}

// unsupported words a failed device query.
func unsupported(err error) string {
	switch {
	case errors.Is(err, e22.ErrNoReply):
		return "no reply"
	case errors.Is(err, e22.ErrFormat):
		return "rejected by the dongle"
	}
	return "error: " + err.Error()
}

func (a *app) write() {
	c, temp := a.edited(), a.temp
	a.job("Write settings", func(d *e22.Device) error {
		return d.WriteConfig(c, temp)
	}, func() { a.load(c) })
}

func (a *app) setMode(m e22.Mode, label string) {
	a.job(label, func(d *e22.Device) error { return d.SetMode(m) }, nil)
}

func (a *app) startScan() {
	from, to, n := byte(a.scanFrom), byte(a.scanTo), int(a.scanSamples)
	stop := make(chan struct{})
	a.stop, a.scanRows = stop, nil
	// With both in-band options known on, Scan can find the mode itself.
	scan := (*e22.Device).Scan
	if a.cfg != nil && a.cfg.SoftMode && a.cfg.NoiseRSSI {
		scan = (*e22.Device).ScanAnyMode
	}
	a.job("Scan", func(d *e22.Device) error {
		return scan(d, from, to, n, stop, func(r e22.ScanRow) {
			a.update(func() { a.scanRows = append(a.scanRows, r) })
		})
	}, nil)
}

func (a *app) startListen() {
	stop := make(chan struct{})
	a.stop, a.listening = stop, true
	split := a.showRSSI
	a.job("Listen", func(d *e22.Device) error {
		return d.Listen(30*time.Millisecond, stop, func(b []byte) {
			p := packet{at: time.Now(), data: b}
			if split {
				if data, raw, ok := e22.SplitRSSI(b); ok {
					p.data, p.rssi = data, fmt.Sprintf("%.1f dBm", e22.RSSIdBm(raw))
				}
			}
			a.update(func() {
				a.packets = append(a.packets, p)
				if len(a.packets) > maxPackets {
					a.packets = a.packets[len(a.packets)-maxPackets:]
				}
			})
		})
	}, nil)
}

func (a *app) stopJob() {
	if a.stop != nil {
		close(a.stop)
		a.stop = nil
	}
	a.listening = false
}

func (a *app) send() {
	data := []byte(a.sendText)
	if !a.listening {
		a.job("Send", func(d *e22.Device) error { return d.Send(data) }, func() { a.sendText = "" })
		return
	}
	// The listen job owns reads; a serial port takes a write alongside them.
	d := a.dev
	a.sendText = ""
	a.spawn(func() {
		err := d.Send(data)
		a.update(func() {
			if err != nil {
				a.err = err.Error()
			} else {
				a.status = fmt.Sprintf("Sent %d bytes", len(data))
			}
		})
	})
}

// ---- view ----

func (a *app) view(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Gap(12).Padding(16).Children(func() {
		a.header(c)
		if a.err != "" {
			ui.Text(c, a.err).TextColor(t.Danger).Selectable()
		}
		ui.Tabs(c, &a.tab, "Settings", "Scan", "Listen", "Help")
		if a.dev == nil && a.tab != helpTab {
			ui.Column(c).Grow(1).Center().Children(func() {
				ui.Text(c, "Not connected").TextColor(t.TextMuted)
			})
			return
		}
		switch a.tab {
		case helpTab:
			ui.Scroll(c).Grow(1).Children(func() { markdown.View(c, helpText, &a.md) })
		case 0:
			a.settings(c)
		case 1:
			a.scan(c)
		case 2:
			a.listen(c)
		}
	})
}

func (a *app) header(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		connected := a.dev != nil
		if len(a.ports) == 0 {
			ui.Text(c, "No ports").TextColor(t.TextMuted)
		} else {
			ui.Select(c, &a.port, a.ports).Label("Port").Width(180).Disabled(connected || a.busy)
		}
		if ui.Button(c, "Refresh").Disabled(connected).Clicked() {
			a.refreshPorts()
		}
		if connected {
			if ui.Button(c, "Disconnect").Disabled(a.busy).Clicked() {
				a.disconnect()
			}
		} else if ui.PrimaryButton(c, "Connect").Disabled(a.busy || a.port == "").Clicked() {
			a.connect()
		}
		ui.Spacer(c)
		if a.busy {
			ui.Spinner(c)
		}
		ui.Text(c, a.status).TextColor(t.TextMuted).NoWrap()
	})
}

func (a *app) settings(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if ui.Button(c, "Read").Disabled(a.busy).Clicked() {
			a.read()
		}
		if ui.PrimaryButton(c, "Write").Disabled(a.busy || a.cfg == nil).Clicked() {
			a.write()
		}
		ui.Switch(c, &a.temp).Label("Temporary")
		ui.Text(c, "Temporary")
		ui.Spacer(c)
		if a.cfg != nil && a.cfg.SoftMode {
			if ui.Button(c, "To transmission mode").Disabled(a.busy).Clicked() {
				a.setMode(e22.ModeTransparent, "Switch to transmission mode")
			}
			if ui.Button(c, "To config mode").Disabled(a.busy).Clicked() {
				a.setMode(e22.ModeConfig, "Switch to config mode")
			}
		}
	})
	if a.cfg == nil {
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Text(c, "No settings loaded").TextColor(t.TextMuted)
		})
		return
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Form(c, func() {
			ui.Field(c, "Region", func() {
				if ui.Select(c, &a.region, regionNames()).Width(160).Changed() {
					a.saveRegion(a.region)
					a.applyRegion()
				}
			})
			ui.Field(c, "Channel", func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.NumberInput(c, &a.channel, 0, e22.MaxChannel900, 1).Width(110)
					ui.Textf(c, "%.3f MHz", e22.FrequencyMHz(byte(a.channel)))
				})
			}).Error(a.bandWarning())
			ui.Field(c, "Air rate", func() { ui.Select(c, &a.air, e22.AirRates[2:]) })
			ui.Field(c, "Power", func() { ui.Select(c, &a.power, powerLabels()) })
			ui.Field(c, "Address", func() { ui.NumberInput(c, &a.addr, 0, 65535, 1).Width(110) })
			ui.Field(c, "Net ID", func() { ui.NumberInput(c, &a.netid, 0, 255, 1).Width(110) })
			ui.Fieldset(c, "Options", func() {
				ui.Field(c, "Packet RSSI", func() { ui.Switch(c, &a.packetRSSI) })
				ui.Field(c, "Noise RSSI", func() { ui.Switch(c, &a.noiseRSSI) })
				ui.Field(c, "Soft mode", func() { ui.Switch(c, &a.softMode) })
				ui.Field(c, "Listen before talk", func() { ui.Switch(c, &a.lbt) })
				ui.Field(c, "Fixed-point", func() { ui.Switch(c, &a.fixed) })
			})
			ui.Fieldset(c, "Device", func() {
				ui.Field(c, "Model", func() { ui.Text(c, a.model).Selectable() })
				ui.Field(c, "Firmware", func() { ui.Text(c, a.firmware).Selectable() })
				ui.Field(c, "Product ID", func() { ui.Text(c, a.pid).Font("monospace").Selectable() })
			})
		})
	})
}

func (a *app) scan(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Channels")
		ui.NumberInput(c, &a.scanFrom, 0, e22.MaxChannel900, 1).Label("From").Width(100)
		ui.Text(c, "to")
		ui.NumberInput(c, &a.scanTo, 0, e22.MaxChannel900, 1).Label("To").Width(100)
		ui.Text(c, "Samples")
		ui.NumberInput(c, &a.scanSamples, 1, 100, 1).Label("Samples").Width(100)
		if a.stop != nil && a.busy {
			if ui.Button(c, "Stop").Clicked() {
				a.stopJob()
			}
		} else if ui.PrimaryButton(c, "Scan").Disabled(a.busy || a.scanFrom > a.scanTo).Clicked() {
			a.startScan()
		}
	})
	if len(a.scanRows) == 0 {
		return
	}
	labels := make([]string, len(a.scanRows))
	var vals []float64
	for i, r := range a.scanRows {
		labels[i] = strconv.Itoa(int(r.Channel))
		vals = append(vals, r.Min, r.Max)
	}
	lo, hi := chart.Extent(vals...)
	floor := math.Floor(lo/10)*10 - 10
	y := chart.NewLinear(floor, math.Ceil(hi/10)*10).Nice(4)
	x := chart.NewBand(labels...).Padding(0.3, 0.15)
	ticks := y.Ticks(4)
	chart.Plot(c, chart.Axes{Left: ticks, Bottom: x.Ticks()}, func(area ui.Element) {
		area.Draw(func(p *ui.Painter, r ui.Rect) {
			for _, tick := range ticks {
				p.Line(r.X, chart.Y(r, tick.At), r.X+r.W, chart.Y(r, tick.At), 1, t.Border)
			}
		})
		for i, r := range a.scanRows {
			chart.Rect(c, x.Band(i), y.Span(floor, r.Avg)).Radius(3).Background(t.Accent).
				Tooltip(fmt.Sprintf("ch %d, %.3f MHz: avg %.1f, min %.1f, max %.1f dBm", r.Channel, r.MHz, r.Avg, r.Min, r.Max))
		}
	}).Label("Average noise by channel, dBm").Height(220).Gap(6)
}

func (a *app) listen(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if a.listening && a.busy {
			if ui.Button(c, "Stop").Clicked() {
				a.stopJob()
			}
		} else if ui.PrimaryButton(c, "Listen").Disabled(a.busy).Clicked() {
			a.startListen()
		}
		ui.Switch(c, &a.showRSSI).Label("RSSI byte").Disabled(a.listening)
		ui.Text(c, "RSSI byte")
		ui.Spacer(c)
		if ui.Button(c, "Clear").Clicked() {
			a.packets = nil
		}
	})
	ui.List(c, nil, len(a.packets), func(i int) {
		p := a.packets[len(a.packets)-1-i] // newest first
		ui.Row(c).Gap(12).Padding(4, 8).Children(func() {
			ui.Text(c, p.at.Format("15:04:05.000")).Font("monospace").TextColor(t.TextMuted).NoWrap()
			ui.Text(c, strconv.QuoteToASCII(string(p.data))).Font("monospace").Grow(1)
			ui.Text(c, strings.TrimSpace(fmt.Sprintf("% X", p.data))).Font("monospace").TextColor(t.TextMuted).Shrink(1)
			if p.rssi != "" {
				ui.Badge(c, p.rssi)
			}
		})
	}).Grow(1).Children(func() {
		if len(a.packets) == 0 {
			ui.Text(c, "No packets").TextColor(t.TextMuted).Padding(12)
		}
	})
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c, &a.sendText).Placeholder("Message").Label("Message").Grow(1)
		if ui.Button(c, "Send").Disabled((a.busy && !a.listening) || a.sendText == "").Clicked() {
			a.send()
		}
	})
}

func main() {
	a := newApp()
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "E22 Control",
			Width:     760,
			Height:    640,
			MinWidth:  560,
			MinHeight: 420,
			StateKey:  "main",
			Content:   ui.View(a.view),
		})
		a.update = win.Update
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
