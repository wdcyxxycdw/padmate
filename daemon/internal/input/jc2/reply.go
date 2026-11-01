package jc2

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Reply is a command reply, as notified on UUIDCommandResp: command id, 0x01,
// 0x01, subcommand, status (2 bytes), 0x00 0x00, then data.
type Reply struct {
	ID, Sub byte
	Status  [2]byte
	Data    []byte
}

// statusOK is the status of a command that worked.
var statusOK = [2]byte{0x10, 0x78}

// ParseReply decodes a notification of UUIDCommandResp. Data aliases b.
func ParseReply(b []byte) (Reply, error) {
	if len(b) < headerLen {
		return Reply{}, fmt.Errorf("jc2: reply of %d bytes is shorter than a header", len(b))
	}
	if b[1] != dirReply || b[2] != transportBLE || b[6] != 0 || b[7] != 0 {
		return Reply{}, fmt.Errorf("jc2: not a reply header: % x", b[:headerLen])
	}
	return Reply{ID: b[0], Sub: b[3], Status: [2]byte{b[4], b[5]}, Data: b[headerLen:]}, nil
}

// OK reports whether the controller says the command worked.
func (r Reply) OK() bool { return r.Status == statusOK }

// Answers reports whether r is the reply to q. A flash read is answered by
// the reply that echoes its address.
func (r Reply) Answers(q Request) bool {
	if r.ID != q.ID || r.Sub != q.Sub {
		return false
	}
	if q.ID == 0x02 && q.Sub == 0x04 {
		return len(r.Data) >= flashHeadLen && len(q.Data) == 8 &&
			binary.LittleEndian.Uint32(r.Data[4:]) == binary.LittleEndian.Uint32(q.Data[4:])
	}
	return true
}

// flashHeadLen is the part of a flash read reply before the bytes: length,
// 3 zeros, address (4 bytes, LE).
const flashHeadLen = 8

// FlashData returns the bytes that r holds, the answer to ReadFlash(fr). It
// checks that r is a successful flash read reply of exactly fr's length that
// echoes fr's address.
func FlashData(r Reply, fr FlashRange) ([]byte, error) {
	if r.ID != 0x02 || r.Sub != 0x04 {
		return nil, fmt.Errorf("jc2: reply %#02x/%#02x is not a flash read", r.ID, r.Sub)
	}
	if !r.OK() {
		return nil, fmt.Errorf("jc2: flash read %#x failed with status % x", fr.Addr, r.Status)
	}
	if len(r.Data) != flashHeadLen+int(fr.Len) {
		return nil, fmt.Errorf("jc2: flash read %#x: reply holds %d bytes, want %d", fr.Addr, len(r.Data), flashHeadLen+int(fr.Len))
	}
	if r.Data[0] != fr.Len {
		return nil, fmt.Errorf("jc2: flash read %#x: reply says %d bytes, want %d", fr.Addr, r.Data[0], fr.Len)
	}
	if addr := binary.LittleEndian.Uint32(r.Data[4:]); addr != fr.Addr {
		return nil, fmt.Errorf("jc2: flash read %#x answered with address %#x", fr.Addr, addr)
	}
	return r.Data[flashHeadLen:], nil
}

// Firmware is the version and controller type that the firmware info command
// reports.
type Firmware struct {
	Major, Minor, Patch byte
	Side                Side
}

// String returns the version as major.minor.patch.
func (f Firmware) String() string { return fmt.Sprintf("%d.%d.%d", f.Major, f.Minor, f.Patch) }

// ParseFirmware decodes the data of the reply to FirmwareInfo: major, minor,
// patch, type (0 is a left Joy-Con, 1 a right one), and more that padmate
// ignores.
func ParseFirmware(data []byte) (Firmware, error) {
	if len(data) < 4 {
		return Firmware{}, errors.New("jc2: firmware info is shorter than 4 bytes")
	}
	f := Firmware{Major: data[0], Minor: data[1], Patch: data[2]}
	switch data[3] {
	case 0:
		f.Side = SideL
	case 1:
		f.Side = SideR
	default:
		return Firmware{}, fmt.Errorf("jc2: controller type %d is not a Joy-Con 2", data[3])
	}
	return f, nil
}
