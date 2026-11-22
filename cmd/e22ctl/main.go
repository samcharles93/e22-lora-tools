// Command e22ctl configures and uses an EBYTE E22-900T22U USB LoRa dongle.
package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/samcharles93/e22-lora-tools/e22"
)

const usage = `usage: e22ctl [-port /dev/ttyUSB0] <command> [flags]

Configuration mode (hold the dongle's button 1.5s until the LED is red):
  get                 show the configuration
  info                configuration plus model, firmware and product ID
  set [flags]         change settings (run "e22ctl set -h")
  scan [flags]        measure noise per channel; needs soft mode and noise rssi on
                      (-any-mode: start from either mode once they are on)

Transmission mode (green LED):
  listen [-rssi]      print received packets; never transmits
  noise               read ambient noise and last-packet RSSI; needs noise rssi on
  send TEXT           transmit TEXT

Probing:
  raw HEX... | raw -text STRING
                      send bytes and print the reply in hex; no mode checks

Either mode:
  mode config|transparent
                      switch mode in software; needs soft mode on, otherwise
                      the command bytes are transmitted over the air

A command sent in the wrong mode is either ignored or, in transmission mode,
transmitted as data.`

func main() {
	port := flag.String("port", "/dev/ttyUSB0", "serial device")
	flag.Usage = func() { fmt.Fprintln(os.Stderr, usage) }
	flag.Parse()
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*port, flag.Arg(0), flag.Args()[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "e22ctl:", err)
		os.Exit(1)
	}
}

func run(portName, cmd string, args []string) error {
	d, p, err := e22.Open(portName)
	if err != nil {
		return err
	}
	defer p.Close()

	switch cmd {
	case "get":
		c, err := d.ReadConfig()
		if err != nil {
			return err
		}
		fmt.Println(c)
	case "info":
		return info(d)
	case "set":
		return set(d, args)
	case "scan":
		return scan(d, args)
	case "listen":
		return listen(d, args)
	case "noise":
		amb, last, err := d.Noise()
		if err != nil {
			return err
		}
		fmt.Printf("ambient %.1f dBm, last packet %.1f dBm\n", e22.RSSIdBm(amb), e22.RSSIdBm(last))
	case "send":
		if len(args) != 1 {
			return errors.New("send takes one argument")
		}
		return d.Send([]byte(args[0]))
	case "raw":
		return raw(d, args)
	case "mode":
		if len(args) != 1 {
			return errors.New("mode takes config or transparent")
		}
		switch args[0] {
		case "config":
			return d.SetMode(e22.ModeConfig)
		case "transparent":
			return d.SetMode(e22.ModeTransparent)
		}
		return fmt.Errorf("unknown mode %q", args[0])
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
	return nil
}

func info(d *e22.Device) error {
	c, err := d.ReadConfig()
	if err != nil {
		return err
	}
	fmt.Println(c)
	// Only query further once the module has proved it is in config mode.
	if pid, err := d.ReadPID(); err == nil {
		fmt.Printf("product id   % X\n", pid)
	} else {
		fmt.Printf("product id   error: %v\n", err)
	}
	for _, q := range []struct {
		label string
		cmd   []byte
	}{{"model", e22.ATDevType}, {"firmware", e22.ATFWCode}} {
		s, err := d.AT(q.cmd)
		if err != nil {
			s = "error: " + err.Error()
		}
		fmt.Printf("%-12s %s\n", q.label, s)
	}
	return nil
}

// onOff is a flag that may be left unset.
type onOff struct {
	set, v bool
}

func (o *onOff) String() string { return "" }

func (o *onOff) Set(s string) error {
	switch s {
	case "on":
		o.v = true
	case "off":
		o.v = false
	default:
		return errors.New("want on or off")
	}
	o.set = true
	return nil
}

func index[T comparable](table []T, v T) (byte, error) {
	for i, x := range table {
		if x == v {
			return byte(i), nil
		}
	}
	return 0, fmt.Errorf("%v not one of %v", v, table)
}

func set(d *e22.Device, args []string) error {
	fs := flag.NewFlagSet("set", flag.ContinueOnError)
	channel := fs.Int("channel", -1, "channel 0-80 (frequency = 850.125 + channel MHz)")
	freq := fs.Float64("freq", 0, "frequency in MHz, rounded to a channel")
	air := fs.String("air", "", "air rate: 2.4k 4.8k 9.6k 19.2k 38.4k 62.5k")
	power := fs.Int("power", 0, "transmit power dBm: 22 17 13 10")
	addr := fs.Int("addr", -1, "module address 0-65535 (65535 = broadcast/monitor)")
	netid := fs.Int("netid", -1, "network id 0-255")
	key := fs.Int("key", -1, "encryption key 0-65535 (write-only register)")
	temp := fs.Bool("temp", false, "write temporarily (C2); lost at power off")
	var packetRSSI, noiseRSSI, softMode, lbt, fixed onOff
	fs.Var(&packetRSSI, "packet-rssi", "on|off: append an RSSI byte to received data")
	fs.Var(&noiseRSSI, "noise-rssi", "on|off: allow in-band noise reads")
	fs.Var(&softMode, "soft-mode", "on|off: allow in-band mode switching")
	fs.Var(&lbt, "lbt", "on|off: listen before talk")
	fs.Var(&fixed, "fixed", "on|off: fixed-point transmission")
	if err := fs.Parse(args); err != nil {
		return err
	}

	c, err := d.ReadConfig()
	if err != nil {
		return err
	}
	if *freq != 0 {
		*channel = int(math.Round(*freq - 850.125))
	}
	if *channel >= 0 {
		if *channel > e22.MaxChannel900 {
			return fmt.Errorf("channel %d out of range 0-%d", *channel, e22.MaxChannel900)
		}
		c.Channel = byte(*channel)
	}
	if *air != "" {
		// Index 2 is the documented 2.4k default; 0 and 1 alias it.
		i, err := index(e22.AirRates[2:], strings.ToLower(*air))
		if err != nil {
			return err
		}
		c.AirRateIdx = i + 2
	}
	if *power != 0 {
		if c.PowerIdx, err = index(e22.Powers[:], *power); err != nil {
			return err
		}
	}
	if *addr >= 0 {
		if *addr > 0xFFFF {
			return errors.New("addr out of range")
		}
		c.Addr = uint16(*addr)
	}
	if *netid >= 0 {
		if *netid > 0xFF {
			return errors.New("netid out of range")
		}
		c.NetID = byte(*netid)
	}
	for _, f := range []struct {
		o   onOff
		dst *bool
	}{{packetRSSI, &c.PacketRSSI}, {noiseRSSI, &c.NoiseRSSI}, {softMode, &c.SoftMode}, {lbt, &c.LBT}, {fixed, &c.FixedPoint}} {
		if f.o.set {
			*f.dst = f.o.v
		}
	}

	if err := d.WriteConfig(c, *temp); err != nil {
		return err
	}
	if *key >= 0 {
		if *key > 0xFFFF {
			return errors.New("key out of range")
		}
		if err := d.SetKey(uint16(*key), *temp); err != nil {
			return err
		}
	}
	fmt.Println(c)
	return nil
}

func scan(d *e22.Device, args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	from := fs.Uint("from", 65, "first channel (65 = 915.125 MHz)")
	to := fs.Uint("to", 77, "last channel (77 = 927.125 MHz)")
	samples := fs.Int("samples", 10, "noise readings per channel")
	anyMode := fs.Bool("any-mode", false, "start from either mode; only when soft mode and noise rssi are already on")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to > e22.MaxChannel900 {
		return errors.New("bad channel range")
	}
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	go func() { <-sig; close(stop) }()

	fmt.Println("ch   MHz       min    avg    max  (dBm)")
	scanFn := d.Scan
	if *anyMode {
		scanFn = d.ScanAnyMode
	}
	return scanFn(byte(*from), byte(*to), *samples, stop, func(r e22.ScanRow) {
		fmt.Printf("%-4d %-9.3f %6.1f %6.1f %6.1f\n", r.Channel, r.MHz, r.Min, r.Avg, r.Max)
	})
}

func listen(d *e22.Device, args []string) error {
	fs := flag.NewFlagSet("listen", flag.ContinueOnError)
	rssi := fs.Bool("rssi", false, "treat the last byte of each packet as RSSI (needs packet-rssi on)")
	gap := fs.Duration("gap", 30*time.Millisecond, "silence that ends a packet")
	if err := fs.Parse(args); err != nil {
		return err
	}
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	go func() { <-sig; close(stop) }()

	fmt.Fprintln(os.Stderr, "listening; Ctrl+C to stop")
	return d.Listen(*gap, stop, func(pkt []byte) {
		data, extra := pkt, ""
		if *rssi {
			var raw byte
			var ok bool
			if data, raw, ok = e22.SplitRSSI(pkt); ok {
				extra = fmt.Sprintf("  rssi=%d (%.1f dBm)", raw, e22.RSSIdBm(raw))
			}
		}
		fmt.Printf("%s  %3d bytes  % X  %s%s\n", time.Now().Format("15:04:05.000"), len(data), data, strconv.QuoteToASCII(string(data)), extra)
	})
}

func raw(d *e22.Device, args []string) error {
	fs := flag.NewFlagSet("raw", flag.ContinueOnError)
	text := fs.String("text", "", "send this string instead of hex bytes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cmd := []byte(*text)
	if *text == "" {
		for _, h := range fs.Args() {
			v, err := strconv.ParseUint(h, 16, 8)
			if err != nil {
				return fmt.Errorf("bad hex byte %q", h)
			}
			cmd = append(cmd, byte(v))
		}
	}
	if len(cmd) == 0 {
		return errors.New("nothing to send")
	}
	reply, err := d.Raw(cmd)
	fmt.Printf("sent  % X\n", cmd)
	if err != nil {
		return err
	}
	fmt.Printf("reply % X  %s\n", reply, strconv.QuoteToASCII(string(reply)))
	return nil
}
