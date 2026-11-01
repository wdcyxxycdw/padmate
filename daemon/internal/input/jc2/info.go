package jc2

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// Info is what the controller's identity block holds.
type Info struct {
	Serial  string
	Vendor  uint16
	Product uint16
}

// Offsets in the FlashInfo block, and the length of the serial number.
const (
	infoSerialAt  = 2
	infoSerialLen = 14
	infoVendorAt  = 0x12
	infoProductAt = 0x14
	infoLen       = infoProductAt + 2
)

// ParseInfo decodes the bytes of FlashInfo: the serial number (14 ASCII
// bytes at +2, NUL padded) and the vendor and product ids (LE, at +0x12 and
// +0x14). It rejects a serial that is empty or not printable ASCII.
func ParseInfo(block []byte) (Info, error) {
	if len(block) < infoLen {
		return Info{}, fmt.Errorf("jc2: info block of %d bytes is shorter than %d", len(block), infoLen)
	}
	serial := bytes.TrimRight(block[infoSerialAt:infoSerialAt+infoSerialLen], "\x00")
	if len(serial) == 0 {
		return Info{}, errors.New("jc2: info block has no serial number")
	}
	for _, c := range serial {
		if c < 0x20 || c > 0x7e {
			return Info{}, errors.New("jc2: serial number is not printable ASCII")
		}
	}
	return Info{
		Serial:  string(serial),
		Vendor:  binary.LittleEndian.Uint16(block[infoVendorAt:]),
		Product: binary.LittleEndian.Uint16(block[infoProductAt:]),
	}, nil
}
