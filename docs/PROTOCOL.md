# E22-900T22U configuration protocol

These notes come from three sources: the *E22-xxxT22U User Manual* v1.1
(section 6), static analysis of EBYTE's Windows tool *RF_Setting(E22-E9X(SL))
V3.5* (an unobfuscated .NET 2.0 program), and testing on an E22-900T22U running
firmware 7434-2-11. They are marked **manual**, **RF_Setting** and
**observed**.

## Serial

- 9600 baud, 8N1. Configuration commands are accepted only at 9600 8N1
  (**manual**).
- The CH340 bridge's DTR and RTS lines play no part. RF_Setting never touches
  them; you switch modes with the button or the in-band command below
  (**RF_Setting**).

## Pacing

The module **drops or rejects a command that arrives too soon after its
previous reply** (**observed**). A command sent at once after a reply may get
no answer, or `FF FF FF`. RF_Setting sleeps 50-100 ms between commands, and
e22ctl keeps 80 ms between exchanges, after which every command works.

## Register commands (configuration mode)

| Command | Reply | |
|---|---|---|
| `C0 addr len data...` | `C1 addr len data...` | Write, saved to flash |
| `C1 addr len` | `C1 addr len data...` | Read |
| `C2 addr len data...` | `C1 addr len data...` | Write, lost at power off |
| `CF CF` + any of the above | `CF CF` + reply | Same, over the air to a remote module |
| malformed | `FF FF FF` | Format error |

(**manual**)

## Registers

| Addr | Name | Bits |
|---|---|---|
| 00, 01 | ADDH, ADDL | Module address. `FFFF` = broadcast, and receive without address filtering |
| 02 | NETID | Network ID |
| 03 | REG0 | `7:5` UART baud (1200 ... 115200, `011` = 9600) · `4:3` parity (8N1, 8O1, 8E1) · `2:0` air rate (900 MHz model: `000`-`010` 2.4k, then 4.8k, 9.6k, 19.2k, 38.4k, 62.5k) |
| 04 | REG1 | `7:6` sub-packet (240, 128, 64, 32 bytes) · `5` noise RSSI enable · `4:3` reserved · `2` soft mode switching · `1:0` power (22, 17, 13, 10 dBm) |
| 05 | REG2 | Channel. 900 MHz model: frequency = 850.125 + ch MHz, ch 0-80 |
| 06 | REG3 | `7` append RSSI byte to received data · `6` fixed-point transmission · `5` relay · `4` listen before talk · `3:0` reserved |
| 07, 08 | CRYPT_H, CRYPT_L | Key, write-only (read back as 0) |
| 80-86 | PID | Product information, 7 bytes, read-only |

Factory default for the E22-900T22U: `00 00 00 62 00 12 03`, which is
868.125 MHz, 2.4k air rate, 9600 8N1, 22 dBm. Its REG3 has reserved bits
`03` set (**manual**); e22ctl preserves reserved bits when writing.

Because CRYPT is write-only, writing all nine registers back after a read
sets the key to 0. Write `00`-`06` only unless you mean to set the key.

PID on firmware 7434-2-11: `00 22 20 16 0B 00 00` (**observed**). RF_Setting
maps it to a model through internal tables; not decoded here.

## In-band commands (transmission mode)

These work only when their REG1 bit is on. When it is off, the module
transmits the bytes like any other data (**manual**).

| Needs | Command | Reply |
|---|---|---|
| REG1 bit 2 | `C0 C1 C2 C3 02 00` (to transmission mode) or `... 02 01` (to configuration mode) | `C1 C2 C3 02 mode` |
| REG1 bit 5 | `C0 C1 C2 C3 00 02` | `C1 00 02 noise last` |

`noise` is the current ambient RSSI and `last` the RSSI of the last received
packet: dBm = -raw / 2 (**manual**).

### Detecting the mode

With soft mode and noise RSSI both on, the noise read works as a mode probe
that never transmits. Transmission mode answers it; configuration mode reads
it as a malformed register write and replies `FF FF FF` (**observed**).
e22ctl's `scan -any-mode` uses this to switch to configuration mode and back.

## AT queries (configuration mode)

Not in the manual (**RF_Setting**, replies **observed**):

| Query | Reply |
|---|---|
| `AT+DEVTYPE=?` | `DEVTYPE=E22-900T22U` |
| `AT+FWCODE=?` | `FWCODE=7434-2-11` |

RF_Setting also sends:

- `AT+DEVTYPE=<name>` to write the model name (a hidden tab), and
- `AT+IAP` to enter the bootloader, then uploads firmware by YMODEM at
  115200 baud.

e22ctl uses neither.

## Received data

In transmission mode the module passes on only packets from EBYTE modules
with the same channel, air rate and net ID, and with a matching address
unless its own address is `FFFF`. With REG3 bit 7 on, each packet is followed
by one RSSI byte. Packets carry no length or delimiter on the serial side;
e22ctl splits them at 30 ms of silence.
