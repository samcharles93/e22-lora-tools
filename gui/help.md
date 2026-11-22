# E22 Control

A desktop app for the EBYTE E22-900T22U USB LoRa dongle.

## Modes

The dongle has two modes, shown by its LED:

| LED | Mode | What it does |
|---|---|---|
| Green | Transmission | Sends whatever it is given over the air, and passes received packets back |
| Red | Configuration | Reads and writes settings; nothing is transmitted |

Hold the dongle's button for 1.5 s to switch. With **Soft mode** on, the
Settings page also gets buttons that switch from the app.

In transmission mode the dongle transmits any bytes it receives, including
configuration commands. Read, Write and Scan only belong in configuration mode.

## Settings

Press **Read** in configuration mode to load the dongle's settings, change
them, then press **Write**. With **Temporary** on, the write is lost when the
dongle loses power.

| Setting | Meaning |
|---|---|
| Channel | Frequency = 850.125 + channel MHz, from 0 to 80 |
| Air rate | Over-the-air data rate; lower reaches further. Both ends must match |
| Power | Transmit power |
| Address | Module address. 65535 receives everything on the channel |
| Net ID | Network ID; both ends must match |
| Packet RSSI | Appends a signal-strength byte to every received packet |
| Noise RSSI | Allows ambient noise reads, which Scan needs |
| Soft mode | Allows switching modes in software, which Scan needs |
| Listen before talk | Waits for a quiet channel (up to 2 s) before transmitting |
| Fixed-point | The first 3 bytes of each message are the target address and channel |

**Model**, **Firmware** and **Product ID** come from the dongle.

### Region

Pick your region and the channel field is marked in red outside its
licence-free band, and Scan defaults to that band. The app remembers the
choice. The bands are approximate: check your regulator's rules, which also
limit power and duty cycle.

| Region | Band | Channels |
|---|---|---|
| EU868 | 863-870 MHz | 13-19 |
| IN865 | 865-867 MHz | 15-16 |
| US915 | 902-928 MHz | 52-77 |
| AU915 | 915-928 MHz (also NZ) | 65-77 |

## Scan

Measures ambient noise on each channel in a range and charts the average.
A channel well above the rest has a transmitter on it; hover a bar for its
minimum and maximum.

Scan needs **Soft mode** and **Noise RSSI** on. Once the app has read or
written them as on, Scan works from either mode: it finds the current one,
switches to configuration mode, and switches back when it is done. Before
that, start it in configuration mode.

It only receives. It moves between channels with temporary writes and ends
on the channel it started on.

## Listen

Shows packets received in transmission mode, newest first, as text and hex.
With **RSSI byte** on, the last byte of each packet is shown as signal
strength; turn **Packet RSSI** on in Settings to match.

The dongle only passes on packets from EBYTE modules on the same channel, air
rate and net ID, and address unless it is 65535. LoRaWAN and Meshtastic
traffic does not appear here; Scan shows it as noise.

**Send** transmits the text in the box.

## Signal strength

Values are dBm = -raw / 2, as given in the dongle's manual.

## Troubleshooting

- **No reply:** the dongle is probably in the wrong mode for the command.
- **Serial port busy:** another program, such as the `e22ctl` command line
  tool, has the port open.
- **No ports:** check that the dongle is plugged in, and that your user is in
  the `dialout` group.
