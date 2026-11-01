package jc2

import "bytes"

// StickCal is the calibration of one stick: the raw 12-bit position of the
// center and how far the stick travels from it in each direction (a
// distance, not a position).
type StickCal struct {
	NeutralX, NeutralY uint16
	PosX, PosY         uint16 // travel above the neutral (right, up)
	NegX, NegY         uint16 // travel below the neutral (left, down)
	// Factory is true for the calibration from the factory area, false for
	// one the user made or for DefaultStick.
	Factory bool
}

// DefaultStick is the calibration for a controller that has neither a
// factory nor a user one: center 2048 and a travel of 1200 each way, a
// little under what the owner's controllers measure.
var DefaultStick = StickCal{
	NeutralX: 2048, NeutralY: 2048,
	PosX: 1200, PosY: 1200, NegX: 1200, NegY: 1200,
}

// Offsets of the calibration in the flash blocks, and its size.
const (
	factoryStickAt = 0x28 // in the FlashFactoryStick block
	userStickAt    = 2    // in the FlashUserStick block, after the magic
	stickCalLen    = 9    // three packed 12-bit pairs
)

// userStickMagic is what the user calibration starts with (0xa1b2, LE).
var userStickMagic = []byte{0xb2, 0xa1}

// Valid reports whether every travel of c is positive, which is what Norm
// needs.
func (c StickCal) Valid() bool {
	return c.PosX != 0 && c.PosY != 0 && c.NegX != 0 && c.NegY != 0
}

// ParseFactoryStick decodes the factory calibration from the bytes of
// FlashFactoryStick. ok is false when the block is too short, the
// calibration is absent (all 0xFF) or a travel is zero.
func ParseFactoryStick(block []byte) (StickCal, bool) {
	if len(block) < factoryStickAt+stickCalLen {
		return StickCal{}, false
	}
	c, ok := parseStickCal(block[factoryStickAt : factoryStickAt+stickCalLen])
	c.Factory = ok
	return c, ok
}

// ParseUserStick decodes the user calibration from the bytes of
// FlashUserStick: the magic b2 a1, then the same 9 bytes as the factory one.
// ok is false when the magic is missing (all 0xFF when there is no user
// calibration), the block is too short or a travel is zero.
func ParseUserStick(block []byte) (StickCal, bool) {
	if len(block) < userStickAt+stickCalLen || !bytes.HasPrefix(block, userStickMagic) {
		return StickCal{}, false
	}
	return parseStickCal(block[userStickAt : userStickAt+stickCalLen])
}

// parseStickCal decodes 9 bytes: the neutral, the travel above it and the
// travel below it, each a pair packed like the stick in a report.
func parseStickCal(b []byte) (StickCal, bool) {
	if bytes.Equal(b, bytes.Repeat([]byte{0xff}, stickCalLen)) {
		return StickCal{}, false
	}
	var c StickCal
	c.NeutralX, c.NeutralY = unpack12(b[0:3])
	c.PosX, c.PosY = unpack12(b[3:6])
	c.NegX, c.NegY = unpack12(b[6:9])
	if !c.Valid() {
		return StickCal{}, false
	}
	return c, true
}

// unpack12 splits 3 bytes into two 12-bit values.
func unpack12(b []byte) (x, y uint16) {
	x = uint16(b[0]) | uint16(b[1]&0x0f)<<8
	y = uint16(b[1]>>4) | uint16(b[2])<<4
	return x, y
}

// ChooseStick returns the calibration to use: the user's if it is Valid,
// else the factory one if it is, else DefaultStick. Pass the zero StickCal
// for one that is absent.
func ChooseStick(user, factory StickCal) StickCal {
	switch {
	case user.Valid():
		return user
	case factory.Valid():
		return factory
	}
	return DefaultStick
}

// Norm converts a raw position to -1..1 per axis, positive when the raw
// value is greater than the neutral (right, up). Each half is scaled by its
// own travel, and the result is clamped. A calibration that is not Valid
// gives 0, 0.
//
// There is no dead zone: a stick rests a little off its neutral (the left
// one of the owner's ~45 counts, 0.04), which the mapping engine's dead zone
// absorbs.
func (c StickCal) Norm(x, y uint16) (fx, fy float64) {
	if !c.Valid() {
		return 0, 0
	}
	return normAxis(x, c.NeutralX, c.PosX, c.NegX), normAxis(y, c.NeutralY, c.PosY, c.NegY)
}

// normAxis scales v around neutral by the travel on its side.
func normAxis(v, neutral, pos, neg uint16) float64 {
	d := float64(v) - float64(neutral)
	if d >= 0 {
		d /= float64(pos)
	} else {
		d /= float64(neg)
	}
	return max(-1, min(1, d))
}
