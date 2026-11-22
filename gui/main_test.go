package main

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/samcharles93/e22-lora-tools/e22"

	"github.com/egoist/mygo/ui"
)

// fakePort answers each command with a canned reply.
type fakePort struct {
	replies map[string][]byte
	out     bytes.Buffer
	written [][]byte
}

func (f *fakePort) Write(p []byte) (int, error) {
	f.written = append(f.written, append([]byte{}, p...))
	f.out.Write(f.replies[string(p)])
	return len(p), nil
}

func (f *fakePort) Read(p []byte) (int, error) {
	n, _ := f.out.Read(p)
	return n, nil
}
func (f *fakePort) SetReadTimeout(time.Duration) error { return nil }
func (f *fakePort) ResetInputBuffer() error            { f.out.Reset(); return nil }
func (f *fakePort) Close() error                       { return nil }

// factory is the E22-900T22U's default configuration (manual section 6.3).
var factory = []byte{0x00, 0x00, 0x00, 0x62, 0x00, 0x12, 0x03}

func testApp(p *fakePort) *app {
	a := newApp()
	a.ports, a.port = []string{"/dev/ttyUSB9"}, "/dev/ttyUSB9"
	a.open = func(string) (*e22.Device, io.Closer, error) {
		d := e22.New(p)
		d.Timeout = 5 * time.Millisecond
		return d, p, nil
	}
	a.spawn = func(f func()) { f() }
	a.saveRegion = func(string) {}
	a.region = "AU915"
	a.update = func(f func()) { f() }
	return a
}

func TestConnectReadWrite(t *testing.T) {
	ch65 := append([]byte{}, factory...)
	ch65[5] = 65
	p := &fakePort{replies: map[string][]byte{
		string(e22.ReadCmd(0, 7)):            append([]byte{0xC1, 0x00, 0x07}, factory...),
		string(e22.ReadCmd(0x80, 7)):         {0xC1, 0x80, 0x07, 1, 2, 3, 4, 5, 6, 7},
		string(e22.ATDevType):                []byte("DEVTYPE=E22-900T22U\r\n"),
		string(e22.WriteCmd(0, ch65, false)): append([]byte{0xC1, 0x00, 0x07}, ch65...),
	}}
	a := testApp(p)
	tt := ui.NewTester(a.view, 760, 640)
	if err := tt.Click("Connect"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Read"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.err != "" || a.cfg == nil {
		t.Fatalf("read failed: %q", a.err)
	}
	if !tt.HasText("868.125 MHz") || !tt.HasText("E22-900T22U") {
		t.Errorf("texts after read: %q", tt.Texts())
	}

	if !tt.HasText("Outside AU915") || !tt.HasText("no reply") {
		t.Errorf("channel 18 / missing PID should warn: %q", tt.Texts())
	}
	a.channel = 65
	tt.Frame()
	if tt.HasText("Outside AU915") {
		t.Error("channel 65 warned")
	}
	if !tt.HasText("915.125 MHz") {
		t.Errorf("frequency not updated: %q", tt.Texts())
	}
	if err := tt.Click("Write"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.err != "" || a.cfg.Channel != 65 {
		t.Errorf("write: err %q, channel %d", a.err, a.cfg.Channel)
	}
}

func TestReadErrorShown(t *testing.T) {
	a := testApp(&fakePort{})
	tt := ui.NewTester(a.view, 760, 640)
	_ = tt.Click("Connect")
	tt.Frame()
	if err := tt.Click("Read"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.err == "" || a.busy {
		t.Errorf("err %q busy %v", a.err, a.busy)
	}
}

func TestScanAndListenViews(t *testing.T) {
	a := testApp(&fakePort{})
	tt := ui.NewTester(a.view, 760, 640)
	_ = tt.Click("Connect")
	tt.Frame()
	if err := tt.Click("Scan"); err != nil { // the tab
		t.Fatal(err)
	}
	a.scanRows = []e22.ScanRow{
		{Channel: 65, MHz: 915.125, Min: -110, Avg: -105, Max: -100},
		{Channel: 66, MHz: 916.125, Min: -90, Avg: -70, Max: -60},
	}
	tt.Frame()
	if !tt.HasText("66") || !tt.HasText("Average noise by channel, dBm") {
		t.Errorf("scan texts: %q", tt.Texts())
	}
	if err := tt.Click("Listen"); err != nil { // the tab
		t.Fatal(err)
	}
	a.packets = []packet{{at: time.Unix(0, 0), data: []byte("hi"), rssi: "-80.0 dBm"}}
	tt.Frame()
	if !tt.HasText(`"hi"`) || !tt.HasText("-80.0 dBm") {
		t.Errorf("listen texts: %q", tt.Texts())
	}
}

func TestHelpWithoutConnection(t *testing.T) {
	a := testApp(&fakePort{})
	tt := ui.NewTester(a.view, 760, 640)
	if !tt.HasText("Not connected") {
		t.Errorf("texts: %q", tt.Texts())
	}
	if err := tt.Click("Help"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Modes") {
		t.Errorf("help not shown: %q", tt.Texts())
	}
}
