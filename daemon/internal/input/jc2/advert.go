package jc2

import "encoding/binary"

// advertLen is the shortest manufacturer data of a Joy-Con 2 advertisement:
// the 6-byte host address ends at byte 16.
const advertLen = 16

// Advert is the manufacturer data of a Joy-Con 2 advertisement (company
// CompanyID): `01 00 03 7e 05 <pid lo> <pid hi> 00 01 <wake> <host address,
// 6 bytes> 0f ...`.
type Advert struct {
	Product uint16
	Side    Side
	// Standard reports whether the host address is all zero. The controller
	// advertises so while SYNC is held, and then accepts a PC. With an
	// address set it is reconnecting to the console, and drops a PC's link
	// at once.
	Standard bool
	// Wake reports whether the controller was woken by a button (byte 9 is
	// 0x81).
	Wake bool
	// HostAddr is the 6 bytes as advertised (data[10:16]), not reordered.
	HostAddr [6]byte
}

// ParseAdvert decodes manufacturer data. ok is false unless company is
// Nintendo's and the data is the advertisement of a Joy-Con 2, left or right.
func ParseAdvert(company uint16, data []byte) (Advert, bool) {
	if company != CompanyID || len(data) < advertLen {
		return Advert{}, false
	}
	if binary.LittleEndian.Uint16(data[3:5]) != VendorID {
		return Advert{}, false
	}
	a := Advert{Product: binary.LittleEndian.Uint16(data[5:7])}
	a.Side = SideOf(a.Product)
	if a.Side == 0 {
		return Advert{}, false
	}
	a.Wake = data[9] == 0x81
	copy(a.HostAddr[:], data[10:advertLen])
	a.Standard = a.HostAddr == [6]byte{}
	return a, true
}
