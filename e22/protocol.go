// Package e22 encodes and decodes the configuration protocol of EBYTE
// E22-xxxT22U USB LoRa dongles.
//
// Sources: the E22-xxxT22U user manual v1.1 (section 6) and the byte
// constants in EBYTE's RF_Setting(E22-E9X(SL)) V3.5 Windows tool.
package e22

import (
	"bytes"
	"errors"
	"fmt"
)

// Command bytes.
const (
	cmdWrite     = 0xC0 // persistent register write
	cmdRead      = 0xC1 // register read; also the header of every reply
	cmdWriteTemp = 0xC2 // register write that is lost on power cycle
)

// Register addresses.
const (
	RegAddrH = 0x00
	RegPID   = 0x80
	pidLen   = 7
	// cfgLen covers ADDH..REG3. CRYPT_H/CRYPT_L (0x07, 0x08) are write-only
	// and read back as zero, so they are only written when asked for.
	cfgLen = 7
)

// ErrFormat is the module's reply to a malformed command.
var ErrFormat = errors.New("module replied FF FF FF (rejected command)")

// magic prefixes the in-band commands accepted in transmission mode, when the
// matching REG1 bit is enabled. With the bit disabled they are transmitted
// over the air like any other data.
var magic = []byte{0xC0, 0xC1, 0xC2, 0xC3}

// Mode is the module's operating mode.
type Mode byte

const (
	ModeTransparent Mode = 0x00
	ModeConfig      Mode = 0x01
)

// AirRates is the air data rate for each REG0[2:0] value on the 900 MHz model.
var AirRates = [8]string{"2.4k", "2.4k", "2.4k", "4.8k", "9.6k", "19.2k", "38.4k", "62.5k"}

// BaudRates is the UART rate for each REG0[7:5] value.
var BaudRates = [8]int{1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200}

// Parities is the UART framing for each REG0[4:3] value.
var Parities = [4]string{"8N1", "8O1", "8E1", "8N1"}

// PacketSizes is the sub-packet length in bytes for each REG1[7:6] value.
var PacketSizes = [4]int{240, 128, 64, 32}

// Powers is the transmit power in dBm for each REG1[1:0] value.
var Powers = [4]int{22, 17, 13, 10}

// MaxChannel900 is the highest channel on the E22-900T22U.
const MaxChannel900 = 80

// Region is a licence-free band, as the whole 900 MHz-model channels inside
// it. The edges are approximate: check your own regulator's rules, which also
// limit power and duty cycle.
type Region struct {
	Name        string
	First, Last byte
}

// Regions lists common bands the E22-900T22U can tune.
var Regions = []Region{
	{"EU868", 13, 19}, // 863-870 MHz
	{"IN865", 15, 16}, // 865-867 MHz
	{"US915", 52, 77}, // 902-928 MHz
	{"AU915", 65, 77}, // 915-928 MHz, also NZ
}

// RegionByName finds a region; ok is false for none.
func RegionByName(name string) (Region, bool) {
	for _, r := range Regions {
		if r.Name == name {
			return r, true
		}
	}
	return Region{}, false
}

// Contains reports whether ch lies in the region.
func (r Region) Contains(ch byte) bool { return ch >= r.First && ch <= r.Last }

// Config is the decoded contents of registers 0x00-0x06.
type Config struct {
	Addr         uint16
	NetID        byte
	BaudIdx      byte // REG0[7:5]
	ParityIdx    byte // REG0[4:3]
	AirRateIdx   byte // REG0[2:0]
	PacketIdx    byte // REG1[7:6]
	NoiseRSSI    bool // REG1[5]: in-band ambient noise reads
	SoftMode     bool // REG1[2]: in-band mode switching
	PowerIdx     byte // REG1[1:0]
	Channel      byte // REG2
	PacketRSSI   bool // REG3[7]: append an RSSI byte to received data
	FixedPoint   bool // REG3[6]: first 3 bytes of TX data are addr+channel
	Relay        bool // REG3[5]
	LBT          bool // REG3[4]: listen before talk
	reg3Reserved byte // REG3[3:0], preserved on write
	reg1Reserved byte // REG1[4:3], preserved on write
}

// FrequencyMHz returns the 900 MHz model's centre frequency for a channel.
func FrequencyMHz(ch byte) float64 { return 850.125 + float64(ch) }

func bit(b byte, n uint) bool { return b&(1<<n) != 0 }

func setBit(on bool, n uint) byte {
	if on {
		return 1 << n
	}
	return 0
}

// DecodeConfig parses registers 0x00-0x06.
func DecodeConfig(r []byte) (Config, error) {
	if len(r) < cfgLen {
		return Config{}, fmt.Errorf("config needs %d bytes, got %d", cfgLen, len(r))
	}
	return Config{
		Addr:         uint16(r[0])<<8 | uint16(r[1]),
		NetID:        r[2],
		BaudIdx:      r[3] >> 5 & 7,
		ParityIdx:    r[3] >> 3 & 3,
		AirRateIdx:   r[3] & 7,
		PacketIdx:    r[4] >> 6 & 3,
		NoiseRSSI:    bit(r[4], 5),
		reg1Reserved: r[4] & 0x18,
		SoftMode:     bit(r[4], 2),
		PowerIdx:     r[4] & 3,
		Channel:      r[5],
		PacketRSSI:   bit(r[6], 7),
		FixedPoint:   bit(r[6], 6),
		Relay:        bit(r[6], 5),
		LBT:          bit(r[6], 4),
		reg3Reserved: r[6] & 0x0F,
	}, nil
}

// Encode returns registers 0x00-0x06.
func (c Config) Encode() []byte {
	return []byte{
		byte(c.Addr >> 8),
		byte(c.Addr),
		c.NetID,
		c.BaudIdx&7<<5 | c.ParityIdx&3<<3 | c.AirRateIdx&7,
		c.PacketIdx&3<<6 | setBit(c.NoiseRSSI, 5) | c.reg1Reserved | setBit(c.SoftMode, 2) | c.PowerIdx&3,
		c.Channel,
		setBit(c.PacketRSSI, 7) | setBit(c.FixedPoint, 6) | setBit(c.Relay, 5) | setBit(c.LBT, 4) | c.reg3Reserved,
	}
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (c Config) String() string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "address      0x%04X\n", c.Addr)
	fmt.Fprintf(&b, "net id       0x%02X\n", c.NetID)
	fmt.Fprintf(&b, "channel      %d (%.3f MHz)\n", c.Channel, FrequencyMHz(c.Channel))
	fmt.Fprintf(&b, "air rate     %s bps\n", AirRates[c.AirRateIdx])
	fmt.Fprintf(&b, "power        %d dBm\n", Powers[c.PowerIdx])
	fmt.Fprintf(&b, "uart         %d %s\n", BaudRates[c.BaudIdx], Parities[c.ParityIdx])
	fmt.Fprintf(&b, "packet size  %d bytes\n", PacketSizes[c.PacketIdx])
	fmt.Fprintf(&b, "fixed-point  %s\n", onOff(c.FixedPoint))
	fmt.Fprintf(&b, "packet rssi  %s\n", onOff(c.PacketRSSI))
	fmt.Fprintf(&b, "noise rssi   %s\n", onOff(c.NoiseRSSI))
	fmt.Fprintf(&b, "soft mode    %s\n", onOff(c.SoftMode))
	fmt.Fprintf(&b, "lbt          %s\n", onOff(c.LBT))
	fmt.Fprintf(&b, "relay        %s", onOff(c.Relay))
	return b.String()
}

// ReadCmd builds a register read: C1 addr len.
func ReadCmd(addr, n byte) []byte { return []byte{cmdRead, addr, n} }

// WriteCmd builds a register write: C0|C2 addr len data...
func WriteCmd(addr byte, data []byte, temporary bool) []byte {
	op := byte(cmdWrite)
	if temporary {
		op = cmdWriteTemp
	}
	return append([]byte{op, addr, byte(len(data))}, data...)
}

// ParseRegReply checks a C1 addr len data... reply and returns data.
func ParseRegReply(reply []byte, addr, n byte) ([]byte, error) {
	if len(reply) >= 3 && bytes.Equal(reply[:3], []byte{0xFF, 0xFF, 0xFF}) {
		return nil, ErrFormat
	}
	want := 3 + int(n)
	if len(reply) < want {
		return nil, fmt.Errorf("short reply: want %d bytes, got % X", want, reply)
	}
	if reply[0] != cmdRead || reply[1] != addr || reply[2] != n {
		return nil, fmt.Errorf("unexpected reply header: % X", reply[:3])
	}
	return reply[3:want], nil
}

// ModeCmd builds the in-band mode switch: C0 C1 C2 C3 02 mode.
// It needs REG1 bit 2 (soft mode switching) enabled.
func ModeCmd(m Mode) []byte { return append(append([]byte{}, magic...), 0x02, byte(m)) }

// ModeReply is the module's acknowledgement of ModeCmd: C1 C2 C3 02 mode.
func ModeReply(m Mode) []byte { return []byte{0xC1, 0xC2, 0xC3, 0x02, byte(m)} }

// NoiseCmd builds the in-band RSSI read: C0 C1 C2 C3 00 02, returning ambient
// noise (register 0) and the last packet's RSSI (register 1). It needs REG1
// bit 5 enabled.
func NoiseCmd() []byte { return append(append([]byte{}, magic...), 0x00, 0x02) }

// RSSIdBm converts a raw RSSI register value to dBm, per manual section 6.2.
func RSSIdBm(raw byte) float64 { return -float64(raw) / 2 }

// AT queries found in RF_Setting V3.5. Replies are ASCII.
var (
	ATDevType = []byte("AT+DEVTYPE=?") // reply "DEVTYPE=<model>"
	ATFWCode  = []byte("AT+FWCODE=?")  // reply "FWCODE=<code>", e.g. 7434-2-11
)

// CleanATReply drops anything up to the first '=' and trailing NUL/CR/LF.
// A text value is returned as is, a binary one as hex.
func CleanATReply(b []byte) string {
	if i := bytes.IndexByte(b, '='); i >= 0 {
		b = b[i+1:]
	}
	b = bytes.TrimRight(b, "\x00\r\n ")
	for _, c := range b {
		if c < 0x20 || c > 0x7E {
			return fmt.Sprintf("% X", b)
		}
	}
	return string(b)
}
