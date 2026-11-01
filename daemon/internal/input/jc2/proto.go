// Package jc2 is the wire protocol of Nintendo's Joy-Con 2 over Bluetooth
// LE, as far as padmate uses it (ADR-0002): what the controllers advertise,
// the GATT characteristics, the commands padmate may send and their replies,
// the flash data it reads, and the input reports. It is pure and
// cross-platform; the Windows transport is in package jc2win, and
// package input turns reports into Events.
//
// The protocol is not documented by Nintendo. The facts here come from
// public reverse engineering (ndeadly's switch2_controller_research and
// linux-switch2's PROTOCOL.md) and were checked against the owner's
// controllers (firmware 2.1.4).
package jc2

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// Nintendo's Bluetooth company id and USB vendor id, and the product ids of
// the Joy-Con 2.
const (
	CompanyID   = 0x0553
	VendorID    = 0x057e
	ProductL    = 0x2067
	ProductR    = 0x2066
	productBase = 0x2060 // Switch 2 controllers count up from here
)

// GATT UUIDs, in lowercase canonical text. The controller service holds
// every characteristic padmate uses.
const (
	UUIDService     = "ab7de9be-89fe-49ad-828f-118f09df7fd0"
	UUIDSideInputL  = "cc1bbbb5-7354-4d32-a716-a81cb241a32a" // notify: native report 0x07, 63 bytes
	UUIDSideInputR  = "d5a9e01e-2ffc-4cca-b20c-8b67142bf442" // notify: native report 0x08, 63 bytes
	UUIDCommand     = "649d4ac9-8eb7-4e6c-af44-1ea54fe5f005" // write without response: Request bytes
	UUIDCommandResp = "c765a961-d9d8-4d36-a20a-5315b111836a" // notify: replies to UUIDCommand
)

// Side is a Joy-Con's side.
type Side uint8

const (
	SideL Side = iota + 1
	SideR
)

// SideOf returns the side of a product id, or 0 for another product.
func SideOf(product uint16) Side {
	switch product {
	case ProductL:
		return SideL
	case ProductR:
		return SideR
	}
	return 0
}

// SideInputUUID returns the native input report characteristic of side s.
func (s Side) SideInputUUID() string {
	if s == SideL {
		return UUIDSideInputL
	}
	return UUIDSideInputR
}

func (s Side) String() string {
	switch s {
	case SideL:
		return "L"
	case SideR:
		return "R"
	}
	return "?"
}

// NotifyUUIDs returns the only characteristics whose notifications padmate
// turns on (by writing their CCCD) for side s: its input reports and the
// command replies.
func NotifyUUIDs(s Side) []string {
	return []string{UUIDCommandResp, s.SideInputUUID()}
}

// MayNotify reports whether padmate may turn on the notifications of the
// characteristic uuid for side s.
func MayNotify(s Side, uuid string) bool {
	for _, u := range NotifyUUIDs(s) {
		if u == uuid {
			return true
		}
	}
	return false
}

// Commands.
//
// A request is an 8-byte header, then data: command id, 0x91 (host to
// device), 0x01 (Bluetooth), subcommand, 0x00, data length, 0x00 0x00. A
// reply has the same layout with 0x01 as its second byte, and its fifth and
// sixth bytes carry a status.
//
// CheckRequest is the allowlist: it accepts the exact bytes that the
// constructors below make and nothing else. Every write to UUIDCommand must
// pass it (jc2win's gate). It refuses the commands that can change what the
// controller keeps: Nintendo pairing (0x15), the pairing info commands
// (0x03/0x07, 0x08, 0x09), flash write and erase (0x02 other than 0x04),
// firmware update (0x0D), shutdown and reboot (0x06), and everything else
// that padmate does not use, vibration included.

const (
	dirRequest   = 0x91
	dirReply     = 0x01
	transportBLE = 0x01
	headerLen    = 8
)

// Request is a command before encoding.
type Request struct {
	ID, Sub byte
	Data    []byte
	// Note says what it does, for logs.
	Note string
}

// Bytes encodes r.
func (r Request) Bytes() []byte {
	b := make([]byte, 0, headerLen+len(r.Data))
	b = append(b, r.ID, dirRequest, transportBLE, r.Sub, 0x00, byte(len(r.Data)), 0x00, 0x00)
	return append(b, r.Data...)
}

// FlashRange is a flash read that padmate may make: exactly this address
// and length.
type FlashRange struct {
	Addr uint32
	Len  byte
}

// The flash ranges padmate reads. The pairing area (0x1FA000), which holds
// the console's link keys, is not among them.
var (
	// FlashInfo holds the serial number (at +2, ASCII) and the vendor and
	// product ids (at +0x12).
	FlashInfo = FlashRange{0x13000, 0x40}
	// FlashFactoryStick holds the factory stick calibration at +0x28.
	FlashFactoryStick = FlashRange{0x13080, 0x40}
	// FlashUserStick holds the user stick calibration, all 0xFF when there
	// is none.
	FlashUserStick = FlashRange{0x1FC040, 0x40}
)

var flashRanges = []FlashRange{FlashInfo, FlashFactoryStick, FlashUserStick}

// FeatureMask is the only feature mask padmate sets: buttons (0x01) and
// sticks (0x02).
const FeatureMask = 0x03

// playerLEDs are the patterns of the console's eight player numbers.
var playerLEDs = [...]byte{0x01, 0x03, 0x07, 0x0f, 0x09, 0x05, 0x0d, 0x06}

// FirmwareInfo asks for the firmware version and controller type.
func FirmwareInfo() Request {
	return Request{ID: 0x10, Sub: 0x01, Note: "read firmware version"}
}

// ReadFlash reads the range fr, which must be one of the ranges above.
func ReadFlash(fr FlashRange) Request {
	d := make([]byte, 8)
	d[0] = fr.Len
	d[1] = 0x7e
	binary.LittleEndian.PutUint32(d[4:], fr.Addr)
	return Request{ID: 0x02, Sub: 0x04, Data: d, Note: fmt.Sprintf("read flash %#x+%#x", fr.Addr, fr.Len)}
}

// SetFeatureMask sets the mask of features the reports may carry.
func SetFeatureMask() Request {
	return Request{ID: 0x0c, Sub: 0x02, Data: []byte{FeatureMask, 0, 0, 0}, Note: "set feature mask"}
}

// EnableFeatures turns the features of the mask on.
func EnableFeatures() Request {
	return Request{ID: 0x0c, Sub: 0x04, Data: []byte{FeatureMask, 0, 0, 0}, Note: "enable features"}
}

// PlayerLEDs shows player number n (0..7) on the controller's lights.
func PlayerLEDs(n int) Request {
	d := make([]byte, 8)
	d[0] = playerLEDs[n&7]
	return Request{ID: 0x09, Sub: 0x07, Data: d, Note: fmt.Sprintf("player lights %d", n&7)}
}

// ErrNotAllowed wraps every refusal of CheckRequest.
var ErrNotAllowed = errors.New("jc2: request not on the allowlist")

// CheckRequest returns nil only if b is exactly a request that one of the
// constructors above makes.
func CheckRequest(b []byte) error {
	if len(b) < headerLen {
		return fmt.Errorf("%w: %d bytes is shorter than a header", ErrNotAllowed, len(b))
	}
	id, sub, n := b[0], b[3], b[5]
	if b[1] != dirRequest || b[2] != transportBLE || b[4] != 0 || b[6] != 0 || b[7] != 0 {
		return fmt.Errorf("%w: header % x", ErrNotAllowed, b[:headerLen])
	}
	data := b[headerLen:]
	if int(n) != len(data) {
		return fmt.Errorf("%w: length byte %d with %d data bytes", ErrNotAllowed, n, len(data))
	}
	switch {
	case id == 0x10 && sub == 0x01:
		if len(data) == 0 {
			return nil
		}
	case id == 0x02 && sub == 0x04:
		if len(data) == 8 && data[1] == 0x7e && data[2] == 0 && data[3] == 0 {
			addr := binary.LittleEndian.Uint32(data[4:])
			for _, fr := range flashRanges {
				if fr.Addr == addr && fr.Len == data[0] {
					return nil
				}
			}
		}
	case id == 0x0c && (sub == 0x02 || sub == 0x04):
		if bytes.Equal(data, []byte{FeatureMask, 0, 0, 0}) {
			return nil
		}
	case id == 0x09 && sub == 0x07:
		if len(data) == 8 && bytes.IndexByte(playerLEDs[:], data[0]) >= 0 && bytes.Equal(data[1:], make([]byte, 7)) {
			return nil
		}
	}
	return fmt.Errorf("%w: command %#02x/%#02x data % x", ErrNotAllowed, id, sub, data)
}
