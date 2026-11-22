package e22

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"time"
)

// Port is the subset of a serial port the device needs.
type Port interface {
	io.ReadWriter
	SetReadTimeout(time.Duration) error
	ResetInputBuffer() error
}

// Device talks to one dongle.
type Device struct {
	p Port
	// Timeout bounds each reply.
	Timeout time.Duration
	// Pace is the quiet time kept between the end of one exchange and the
	// next command. The module drops a command that arrives too soon after
	// its last reply; RF_Setting sleeps 50-100 ms between commands.
	Pace time.Duration
	last time.Time
}

// New wraps an open port. The port must be at 9600 8N1, the only framing the
// module accepts configuration commands in.
func New(p Port) *Device { return &Device{p: p, Timeout: time.Second, Pace: 80 * time.Millisecond} }

// send writes cmd once Pace has passed since the last exchange.
func (d *Device) send(cmd []byte) error {
	if wait := time.Until(d.last.Add(d.Pace)); wait > 0 {
		time.Sleep(wait)
	}
	if err := d.p.ResetInputBuffer(); err != nil {
		return err
	}
	_, err := d.p.Write(cmd)
	return err
}

// ErrNoReply means the module sent nothing back.
var ErrNoReply = errors.New("no reply (is the dongle in configuration mode, red LED?)")

// readN reads exactly n bytes or fails after d.Timeout.
func (d *Device) readN(n int) ([]byte, error) {
	buf := make([]byte, 0, n)
	deadline := time.Now().Add(d.Timeout)
	tmp := make([]byte, n)
	for len(buf) < n {
		left := time.Until(deadline)
		if left <= 0 {
			break
		}
		if err := d.p.SetReadTimeout(left); err != nil {
			return nil, err
		}
		k, err := d.p.Read(tmp[:n-len(buf)])
		if err != nil {
			return nil, err
		}
		if k == 0 {
			break // timed out
		}
		buf = append(buf, tmp[:k]...)
		// A format error reply is shorter than most expected replies.
		if len(buf) == 3 && bytes.Equal(buf, []byte{0xFF, 0xFF, 0xFF}) {
			return buf, nil
		}
	}
	if len(buf) == 0 {
		return nil, ErrNoReply
	}
	return buf, nil
}

// readIdle reads until the line has been quiet for gap or max bytes arrive.
func (d *Device) readIdle(gap time.Duration, max int) ([]byte, error) {
	var buf []byte
	tmp := make([]byte, 256)
	deadline := time.Now().Add(d.Timeout)
	if err := d.p.SetReadTimeout(d.Timeout); err != nil {
		return nil, err
	}
	for len(buf) < max && time.Now().Before(deadline) {
		k, err := d.p.Read(tmp)
		if err != nil {
			return nil, err
		}
		if k == 0 {
			break
		}
		buf = append(buf, tmp[:k]...)
		if err := d.p.SetReadTimeout(gap); err != nil {
			return nil, err
		}
	}
	if len(buf) == 0 {
		return nil, ErrNoReply
	}
	return buf, nil
}

func (d *Device) xfer(cmd []byte, replyLen int) ([]byte, error) {
	if err := d.send(cmd); err != nil {
		return nil, err
	}
	defer func() { d.last = time.Now() }()
	return d.readN(replyLen)
}

// ReadRegs reads n registers starting at addr. Configuration mode only.
func (d *Device) ReadRegs(addr, n byte) ([]byte, error) {
	reply, err := d.xfer(ReadCmd(addr, n), 3+int(n))
	if err != nil {
		return nil, err
	}
	return ParseRegReply(reply, addr, n)
}

// WriteRegs writes registers and checks the echoed values. Configuration
// mode only.
func (d *Device) WriteRegs(addr byte, data []byte, temporary bool) error {
	reply, err := d.xfer(WriteCmd(addr, data, temporary), 3+len(data))
	if err != nil {
		return err
	}
	got, err := ParseRegReply(reply, addr, byte(len(data)))
	if err != nil {
		return err
	}
	if !bytes.Equal(got, data) {
		return fmt.Errorf("module echoed % X, wrote % X", got, data)
	}
	return nil
}

// ReadConfig reads registers 0x00-0x06.
func (d *Device) ReadConfig() (Config, error) {
	r, err := d.ReadRegs(RegAddrH, cfgLen)
	if err != nil {
		return Config{}, err
	}
	return DecodeConfig(r)
}

// WriteConfig writes registers 0x00-0x06, leaving the encryption key alone.
func (d *Device) WriteConfig(c Config, temporary bool) error {
	return d.WriteRegs(RegAddrH, c.Encode(), temporary)
}

// SetKey writes the write-only CRYPT_H/CRYPT_L registers.
func (d *Device) SetKey(key uint16, temporary bool) error {
	return d.WriteRegs(0x07, []byte{byte(key >> 8), byte(key)}, temporary)
}

// ReadPID reads the 7 product-information bytes (0x80-0x86).
func (d *Device) ReadPID() ([]byte, error) { return d.ReadRegs(RegPID, pidLen) }

// AT sends an AT query and returns the cleaned text reply.
func (d *Device) AT(q []byte) (string, error) {
	if err := d.send(q); err != nil {
		return "", err
	}
	reply, err := d.readIdle(50*time.Millisecond, 64)
	d.last = time.Now()
	if err != nil {
		return "", err
	}
	if bytes.Equal(reply, []byte{0xFF, 0xFF, 0xFF}) {
		return "", ErrFormat
	}
	return CleanATReply(reply), nil
}

// SetMode switches mode in-band. REG1 bit 2 must already be enabled, which
// the caller should confirm from ReadConfig while in configuration mode:
// otherwise, from transparent mode, these bytes go out over the air.
func (d *Device) SetMode(m Mode) error {
	want := ModeReply(m)
	reply, err := d.xfer(ModeCmd(m), len(want))
	if err != nil {
		return fmt.Errorf("mode switch: %w", err)
	}
	if !bytes.Equal(reply, want) {
		return fmt.Errorf("mode switch: got % X, want % X", reply, want)
	}
	// The module applies the new mode once idle; give it a moment.
	time.Sleep(50 * time.Millisecond)
	return nil
}

// Noise reads ambient noise and last-packet RSSI in transparent mode. REG1
// bit 5 must be enabled (see SetMode for why the caller must check first).
func (d *Device) Noise() (ambient, lastPacket byte, err error) {
	reply, err := d.xfer(NoiseCmd(), 5)
	if err != nil {
		return 0, 0, err
	}
	data, err := ParseRegReply(reply, 0x00, 2)
	if err != nil {
		return 0, 0, err
	}
	return data[0], data[1], nil
}

// Send transmits data. Transparent mode only.
func (d *Device) Send(data []byte) error {
	_, err := d.p.Write(data)
	return err
}

// pollInterval bounds how long Listen and Scan take to notice stop.
const pollInterval = 100 * time.Millisecond

// Listen calls pkt for each received packet, a burst of bytes followed by
// gap of silence, until stop is closed. It never writes. Transparent mode.
func (d *Device) Listen(gap time.Duration, stop <-chan struct{}, pkt func([]byte)) error {
	buf := make([]byte, 512)
	var cur []byte
	for {
		select {
		case <-stop:
			if len(cur) > 0 {
				pkt(cur)
			}
			return nil
		default:
		}
		timeout := pollInterval
		if len(cur) > 0 {
			timeout = gap
		}
		if err := d.p.SetReadTimeout(timeout); err != nil {
			return err
		}
		n, err := d.p.Read(buf)
		if err != nil {
			return err
		}
		if n == 0 {
			if len(cur) > 0 {
				pkt(cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, buf[:n]...)
	}
}

// SplitRSSI separates the RSSI byte the module appends to each packet when
// REG3 bit 7 is on.
func SplitRSSI(pkt []byte) (data []byte, rssi byte, ok bool) {
	if len(pkt) < 2 {
		return pkt, 0, false
	}
	return pkt[:len(pkt)-1], pkt[len(pkt)-1], true
}

// ErrStopped reports a Scan ended by its stop channel.
var ErrStopped = errors.New("stopped")

// ScanRow is the noise measured on one channel.
type ScanRow struct {
	Channel       byte
	MHz           float64
	Min, Avg, Max float64 // dBm
}

// Scan measures ambient noise on channels from..to, calling row after each.
// It must start in configuration mode and needs soft mode switching and
// noise RSSI enabled, which it checks first: without them its in-band
// commands would be transmitted. It receives only, writes channels with
// temporary (C2) writes, and ends in configuration mode on the original
// channel.
func (d *Device) Scan(from, to byte, samples int, stop <-chan struct{}, row func(ScanRow)) (err error) {
	if from > to || to > MaxChannel900 || samples < 1 {
		return errors.New("bad scan range")
	}
	c, err := d.ReadConfig()
	if err != nil {
		return err
	}
	if !c.SoftMode || !c.NoiseRSSI {
		return errors.New("scan needs soft mode and noise rssi switched on")
	}
	orig := c.Channel
	defer func() {
		c.Channel = orig
		if rerr := d.WriteConfig(c, true); rerr != nil && err == nil {
			err = fmt.Errorf("restore channel: %w", rerr)
		}
	}()

	for ch := int(from); ch <= int(to); ch++ {
		select {
		case <-stop:
			return ErrStopped
		default:
		}
		c.Channel = byte(ch)
		if err := d.WriteConfig(c, true); err != nil {
			return fmt.Errorf("ch %d: %w", ch, err)
		}
		if err := d.SetMode(ModeTransparent); err != nil {
			return err
		}
		time.Sleep(pollInterval)
		r := ScanRow{Channel: byte(ch), MHz: FrequencyMHz(byte(ch)), Min: 1000, Max: -1000}
		for range samples {
			amb, _, nerr := d.Noise()
			if nerr != nil {
				_ = d.SetMode(ModeConfig)
				return fmt.Errorf("ch %d noise: %w", ch, nerr)
			}
			v := RSSIdBm(amb)
			r.Min, r.Max, r.Avg = min(r.Min, v), max(r.Max, v), r.Avg+v
			time.Sleep(20 * time.Millisecond)
		}
		r.Avg /= float64(samples)
		if err := d.SetMode(ModeConfig); err != nil {
			return err
		}
		row(r)
	}
	return nil
}

// Raw sends cmd and returns whatever comes back until the line goes quiet.
// For probing; it does no mode checks.
func (d *Device) Raw(cmd []byte) ([]byte, error) {
	if err := d.send(cmd); err != nil {
		return nil, err
	}
	defer func() { d.last = time.Now() }()
	return d.readIdle(100*time.Millisecond, 256)
}

// ProbeMode reports the current mode by sending a noise read: transmission
// mode answers it, configuration mode rejects it as a malformed register
// write (FF FF FF). Only use it with soft mode and noise RSSI known to be
// on; otherwise, in transmission mode, the probe is transmitted.
func (d *Device) ProbeMode() (Mode, error) {
	reply, err := d.xfer(NoiseCmd(), 5)
	if err != nil {
		return 0, fmt.Errorf("mode probe: %w", err)
	}
	if bytes.Equal(reply, []byte{0xFF, 0xFF, 0xFF}) {
		return ModeConfig, nil
	}
	if _, err := ParseRegReply(reply, 0x00, 2); err != nil {
		return 0, fmt.Errorf("mode probe: %w", err)
	}
	return ModeTransparent, nil
}

// ScanAnyMode is Scan from either mode, returning to the mode it found. It
// has ProbeMode's precondition: soft mode and noise RSSI already on.
func (d *Device) ScanAnyMode(from, to byte, samples int, stop <-chan struct{}, row func(ScanRow)) (err error) {
	m, err := d.ProbeMode()
	if err != nil {
		return err
	}
	if m == ModeTransparent {
		if err := d.SetMode(ModeConfig); err != nil {
			return err
		}
		defer func() {
			if merr := d.SetMode(ModeTransparent); merr != nil && err == nil {
				err = merr
			}
		}()
	}
	return d.Scan(from, to, samples, stop, row)
}
