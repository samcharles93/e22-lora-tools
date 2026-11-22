package main

import (
	"flag"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/samcharles93/e22-lora-tools/e22"
)

var screenshots = flag.Bool("screenshots", false, "write the README screenshots to ../docs/screenshots")

// TestScreenshots renders the README screenshots from sample data:
//
//	CGO_ENABLED=0 go test -run TestScreenshots -screenshots .
func TestScreenshots(t *testing.T) {
	if !*screenshots {
		t.Skip("run with -screenshots")
	}
	a := testApp(&fakePort{})
	a.dev = e22.New(&fakePort{})
	a.port, a.ports = "/dev/ttyUSB0", []string{"/dev/ttyUSB0"}
	cfg, _ := e22.DecodeConfig([]byte{0x00, 0x00, 0x00, 0x62, 0x24, 65, 0x83})
	a.load(cfg)
	a.model, a.firmware, a.pid = "E22-900T22U", "7434-2-11", "00 22 20 16 0B 00 00"

	noise := []float64{-104, -105, -103, -104, -106, -71, -104, -103, -105, -104, -102, -104, -103}
	for i, avg := range noise {
		ch := byte(65 + i)
		a.scanRows = append(a.scanRows, e22.ScanRow{Channel: ch, MHz: e22.FrequencyMHz(ch), Min: avg - 3, Avg: avg, Max: avg + 4})
	}
	start := time.Date(2026, 11, 22, 19, 4, 11, 0, time.UTC)
	for i, msg := range []string{"temp=21.4", "temp=21.5", "hello from the c3", "temp=21.5", "ping 42"} {
		a.packets = append(a.packets, packet{
			at:   start.Add(time.Duration(i) * 7300 * time.Millisecond),
			data: []byte(msg),
			rssi: []string{"-61.5 dBm", "-62.0 dBm", "-58.5 dBm", "-61.0 dBm", "-73.5 dBm"}[i],
		})
	}

	dir := filepath.Join("..", "docs", "screenshots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, s := range []struct {
		name   string
		tab    int
		status string
		h      int
	}{
		{"settings", 0, "Read settings: done", 770},
		{"scan", 1, "Scan: done", 390},
		{"listen", 2, "Listen…", 400},
	} {
		a.tab, a.status = s.tab, s.status
		a.busy, a.listening = s.tab == 2, s.tab == 2
		tt := ui.NewTester(a.view, 760, s.h)
		tt.SetScale(2)
		tt.Frame()
		f, err := os.Create(filepath.Join(dir, s.name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
