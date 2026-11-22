package e22

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestCommandsMatchManual(t *testing.T) {
	tests := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"set channel", WriteCmd(0x05, []byte{0x09}, false), []byte{0xC0, 0x05, 0x01, 0x09}},
		{"set channel temp", WriteCmd(0x05, []byte{0x09}, true), []byte{0xC2, 0x05, 0x01, 0x09}},
		{"read channel", ReadCmd(0x05, 1), []byte{0xC1, 0x05, 0x01}},
		{"read pid", ReadCmd(RegPID, pidLen), []byte{0xC1, 0x80, 0x07}},
		{"mode config", ModeCmd(ModeConfig), []byte{0xC0, 0xC1, 0xC2, 0xC3, 0x02, 0x01}},
		{"mode transparent", ModeCmd(ModeTransparent), []byte{0xC0, 0xC1, 0xC2, 0xC3, 0x02, 0x00}},
		{"noise", NoiseCmd(), []byte{0xC0, 0xC1, 0xC2, 0xC3, 0x00, 0x02}},
	}
	for _, tt := range tests {
		if !bytes.Equal(tt.got, tt.want) {
			t.Errorf("%s: got % X, want % X", tt.name, tt.got, tt.want)
		}
	}
}

// The factory default line for the E22-900T22U in manual section 6.3 is
// C0 00 09 00 00 00 62 00 12 03 00 00.
func TestFactoryDefaultRoundTrip(t *testing.T) {
	regs := []byte{0x00, 0x00, 0x00, 0x62, 0x00, 0x12, 0x03}
	c, err := DecodeConfig(regs)
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != 0 || c.NetID != 0 {
		t.Errorf("addr/netid = %04X/%02X, want 0/0", c.Addr, c.NetID)
	}
	if BaudRates[c.BaudIdx] != 9600 || Parities[c.ParityIdx] != "8N1" {
		t.Errorf("uart = %d %s, want 9600 8N1", BaudRates[c.BaudIdx], Parities[c.ParityIdx])
	}
	if AirRates[c.AirRateIdx] != "2.4k" || Powers[c.PowerIdx] != 22 {
		t.Errorf("air/power = %s/%d, want 2.4k/22", AirRates[c.AirRateIdx], Powers[c.PowerIdx])
	}
	if got := FrequencyMHz(c.Channel); got != 868.125 {
		t.Errorf("frequency = %v, want 868.125", got)
	}
	if got := c.Encode(); !bytes.Equal(got, regs) {
		t.Errorf("encode = % X, want % X (reserved bits must survive)", got, regs)
	}
}

func TestEncodeFlags(t *testing.T) {
	c, _ := DecodeConfig([]byte{0x12, 0x34, 0x00, 0x62, 0x00, 0x12, 0x00})
	c.NoiseRSSI, c.SoftMode, c.PacketRSSI, c.LBT = true, true, true, true
	c.PowerIdx, c.Channel = 1, 65
	want := []byte{0x12, 0x34, 0x00, 0x62, 0x25, 65, 0x90}
	if got := c.Encode(); !bytes.Equal(got, want) {
		t.Errorf("encode = % X, want % X", got, want)
	}
}

func TestParseRegReply(t *testing.T) {
	if _, err := ParseRegReply([]byte{0xFF, 0xFF, 0xFF}, 0, 7); !errors.Is(err, ErrFormat) {
		t.Errorf("format error: got %v", err)
	}
	if _, err := ParseRegReply([]byte{0xC1, 0x05, 0x01}, 0x05, 1); err == nil {
		t.Error("short reply accepted")
	}
	if _, err := ParseRegReply([]byte{0xC1, 0x04, 0x01, 0x09}, 0x05, 1); err == nil {
		t.Error("wrong address accepted")
	}
	got, err := ParseRegReply([]byte{0xC1, 0x05, 0x01, 0x09}, 0x05, 1)
	if err != nil || !bytes.Equal(got, []byte{0x09}) {
		t.Errorf("got % X, %v", got, err)
	}
}

func TestCleanATReply(t *testing.T) {
	if got := CleanATReply([]byte("DEVTYPE=E22-900T22U\r\n\x00")); got != "E22-900T22U" {
		t.Errorf("got %q", got)
	}
	if got := CleanATReply([]byte("FWCODE=\x01\x02\xfa\r\n")); got != "01 02 FA" {
		t.Errorf("binary reply: got %q", got)
	}
}

func TestRSSI(t *testing.T) {
	if got := RSSIdBm(0xA0); got != -80 {
		t.Errorf("got %v", got)
	}
}

// fakePort answers each write with a canned reply.
type fakePort struct {
	replies map[string][]byte
	respond func([]byte) []byte // overrides replies when set
	out     bytes.Buffer
	written [][]byte
}

func (f *fakePort) Write(p []byte) (int, error) {
	f.written = append(f.written, append([]byte{}, p...))
	if f.respond != nil {
		f.out.Write(f.respond(p))
		return len(p), nil
	}
	f.out.Write(f.replies[string(p)])
	return len(p), nil
}

func (f *fakePort) Read(p []byte) (int, error) {
	n, _ := f.out.Read(p)
	return n, nil // n == 0 models a read timeout
}
func (f *fakePort) SetReadTimeout(time.Duration) error { return nil }
func (f *fakePort) ResetInputBuffer() error            { f.out.Reset(); return nil }

func TestDeviceReadWriteConfig(t *testing.T) {
	regs := []byte{0x00, 0x00, 0x00, 0x62, 0x00, 0x12, 0x03}
	newRegs := []byte{0x00, 0x00, 0x00, 0x62, 0x00, 65, 0x03}
	p := &fakePort{replies: map[string][]byte{
		string(ReadCmd(0, 7)):              append([]byte{0xC1, 0x00, 0x07}, regs...),
		string(WriteCmd(0, newRegs, true)): append([]byte{0xC1, 0x00, 0x07}, newRegs...),
	}}
	d := New(p)
	d.Timeout = 10 * time.Millisecond
	c, err := d.ReadConfig()
	if err != nil {
		t.Fatal(err)
	}
	c.Channel = 65
	if err := d.WriteConfig(c, true); err != nil {
		t.Fatal(err)
	}
	if len(p.written) != 2 || p.written[1][0] != 0xC2 {
		t.Errorf("writes = % X", p.written)
	}
}

func TestDeviceNoReply(t *testing.T) {
	d := New(&fakePort{})
	d.Timeout = 10 * time.Millisecond
	if _, err := d.ReadConfig(); !errors.Is(err, ErrNoReply) {
		t.Errorf("got %v, want ErrNoReply", err)
	}
}

func TestSetModeRejectsBadAck(t *testing.T) {
	p := &fakePort{replies: map[string][]byte{
		string(ModeCmd(ModeConfig)): {0xC1, 0xC2, 0xC3, 0x02, 0x00},
	}}
	d := New(p)
	d.Timeout = 10 * time.Millisecond
	if err := d.SetMode(ModeConfig); err == nil {
		t.Error("wrong mode ack accepted")
	}
}

func TestListenSplitsPackets(t *testing.T) {
	p := &fakePort{}
	p.out.WriteString("hello")
	d := New(p)
	stop := make(chan struct{})
	var got []string
	err := d.Listen(time.Millisecond, stop, func(b []byte) {
		got = append(got, string(b))
		close(stop)
	})
	if err != nil || len(got) != 1 || got[0] != "hello" {
		t.Errorf("got %q, %v", got, err)
	}
	if len(p.written) != 0 {
		t.Errorf("Listen wrote % X", p.written)
	}
}

func TestSplitRSSI(t *testing.T) {
	data, rssi, ok := SplitRSSI([]byte{'h', 'i', 0xA0})
	if !ok || string(data) != "hi" || rssi != 0xA0 {
		t.Errorf("got %q %X %v", data, rssi, ok)
	}
}

func scanPort(regs []byte) *fakePort {
	replies := map[string][]byte{
		string(ReadCmd(0, 7)):            append([]byte{0xC1, 0x00, 0x07}, regs...),
		string(ModeCmd(ModeTransparent)): ModeReply(ModeTransparent),
		string(ModeCmd(ModeConfig)):      ModeReply(ModeConfig),
		string(NoiseCmd()):               {0xC1, 0x00, 0x02, 0xC8, 0x00},
	}
	for ch := byte(0); ch <= MaxChannel900; ch++ {
		r := append([]byte{}, regs...)
		r[5] = ch
		replies[string(WriteCmd(0, r, true))] = append([]byte{0xC1, 0x00, 0x07}, r...)
	}
	return &fakePort{replies: replies}
}

func TestScan(t *testing.T) {
	// Soft mode and noise rssi on (REG1 = 0x24), channel 65.
	p := scanPort([]byte{0, 0, 0, 0x62, 0x24, 65, 0x03})
	d := New(p)
	d.Timeout = 10 * time.Millisecond
	var rows []ScanRow
	if err := d.Scan(70, 71, 2, nil, func(r ScanRow) { rows = append(rows, r) }); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Channel != 70 || rows[1].Avg != -100 {
		t.Errorf("rows = %+v", rows)
	}
	last := p.written[len(p.written)-1]
	if last[0] != cmdWriteTemp || last[3+5] != 65 {
		t.Errorf("did not restore channel 65 with a temp write: % X", last)
	}
	for _, w := range p.written {
		if w[0] == cmdWrite && !bytes.HasPrefix(w, magic) {
			t.Errorf("scan made a persistent write: % X", w)
		}
	}
}

// Without the in-band bits, Scan's commands would be transmitted, so it must
// send nothing after the config read.
func TestScanRefusesWithoutInBandBits(t *testing.T) {
	p := scanPort([]byte{0, 0, 0, 0x62, 0x00, 65, 0x03})
	d := New(p)
	d.Timeout = 10 * time.Millisecond
	if err := d.Scan(70, 71, 2, nil, func(ScanRow) {}); err == nil {
		t.Fatal("scan ran without soft mode / noise rssi")
	}
	if len(p.written) != 1 {
		t.Errorf("wrote after refusing: % X", p.written)
	}
}

// modalPort is scanPort with a mode: noise reads only work in transmission
// mode and are rejected in configuration mode, as on the real module.
func modalPort(start Mode) *fakePort {
	p := scanPort([]byte{0, 0, 0, 0x62, 0x24, 65, 0x03})
	mode, replies := start, p.replies
	p.respond = func(cmd []byte) []byte {
		switch string(cmd) {
		case string(ModeCmd(ModeConfig)):
			mode = ModeConfig
		case string(ModeCmd(ModeTransparent)):
			mode = ModeTransparent
		case string(NoiseCmd()):
			if mode == ModeConfig {
				return []byte{0xFF, 0xFF, 0xFF}
			}
		default:
			if mode == ModeTransparent {
				return nil // would be transmitted; no reply
			}
		}
		return replies[string(cmd)]
	}
	return p
}

func TestScanAnyMode(t *testing.T) {
	for _, start := range []Mode{ModeTransparent, ModeConfig} {
		p := modalPort(start)
		d := New(p)
		d.Timeout = 10 * time.Millisecond
		n := 0
		if err := d.ScanAnyMode(70, 71, 1, nil, func(ScanRow) { n++ }); err != nil {
			t.Fatalf("start %d: %v", start, err)
		}
		if n != 2 {
			t.Errorf("start %d: %d rows", start, n)
		}
		last := p.written[len(p.written)-1]
		if start == ModeTransparent && !bytes.Equal(last, ModeCmd(ModeTransparent)) {
			t.Errorf("did not return to transmission mode, last write % X", last)
		}
		if start == ModeConfig && last[0] != cmdWriteTemp {
			t.Errorf("config start should end on the channel restore, last write % X", last)
		}
	}
}

// The module drops commands that arrive too soon after its last reply
// (seen on hardware: a scan's first channel write after the config read got
// no reply). Device must pace its commands.
func TestScanPacesCommands(t *testing.T) {
	p := modalPort(ModeConfig)
	inner := p.respond
	var lastReply time.Time
	p.respond = func(cmd []byte) []byte {
		if !lastReply.IsZero() && time.Since(lastReply) < 40*time.Millisecond {
			return nil // dropped
		}
		lastReply = time.Now()
		return inner(cmd)
	}
	d := New(p)
	d.Timeout = 20 * time.Millisecond
	d.Pace = 50 * time.Millisecond
	if err := d.ScanAnyMode(70, 70, 1, nil, func(ScanRow) {}); err != nil {
		t.Fatalf("paced scan: %v", err)
	}
	d.Pace = 0
	if err := d.ScanAnyMode(70, 70, 1, nil, func(ScanRow) {}); err == nil {
		t.Error("unpaced scan passed; the fake is not modelling the drop")
	}
}

func TestRegions(t *testing.T) {
	au, ok := RegionByName("AU915")
	if !ok || !au.Contains(65) || !au.Contains(77) || au.Contains(78) || au.Contains(18) {
		t.Errorf("AU915 = %+v", au)
	}
	if FrequencyMHz(au.First) < 915 || FrequencyMHz(au.Last) > 928 {
		t.Errorf("AU915 channels outside 915-928 MHz")
	}
	if _, ok := RegionByName("None"); ok {
		t.Error("None is not a region")
	}
}
