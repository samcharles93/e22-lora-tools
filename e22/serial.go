package e22

import (
	"path/filepath"

	"go.bug.st/serial"
)

// Open opens a dongle at 9600 8N1. Close the returned port when done.
func Open(name string) (*Device, serial.Port, error) {
	p, err := serial.Open(name, &serial.Mode{BaudRate: 9600, DataBits: 8, Parity: serial.NoParity, StopBits: serial.OneStopBit})
	if err != nil {
		return nil, nil, err
	}
	return New(p), p, nil
}

// Ports lists likely USB serial devices; the T22U's CH340 shows as ttyUSB.
func Ports() []string {
	usb, _ := filepath.Glob("/dev/ttyUSB*")
	acm, _ := filepath.Glob("/dev/ttyACM*")
	return append(usb, acm...)
}
