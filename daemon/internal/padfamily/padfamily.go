// Package padfamily sorts controllers into the families that decide a
// controller's own settings (ADR-0012, ADR-0017): which button can be its
// function key, and which emergency combo it has by default. The family
// follows from the layout of the controller's buttons, not from the brand:
// a pad with the standard layout (Xbox, PlayStation, Switch Pro, a pad SDL
// does not know) and the shapes of a Joy-Con.
//
// The package is pure data with no platform dependencies. It does not read
// a DeviceInfo (input.FamilyOf does) and does not read config.json
// (profile.Config.PadSettings does); it only holds the rules they share.
package padfamily

import (
	"slices"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
)

// Family is the shape of a controller's buttons.
type Family uint8

// The families. Unknown is the zero value: the family of a controller that
// is not known well enough to tell, such as a DeviceKey without a GUID
// from a hand-written trace. It is treated as Standard wherever a value is
// needed.
const (
	Unknown Family = iota
	// Standard is every controller with the standard layout: Xbox,
	// PlayStation, Switch Pro, GameCube, a pad SDL reports as unknown, and
	// anything that is not a Joy-Con.
	Standard
	// JoyConLeft and JoyConRight are a single Joy-Con held sideways, which is
	// how padmate holds it unless the vertical hint is set (ARCHITECTURE
	// §7.1). The Switch 1 Joy-Con and the Joy-Con 2 map the same way.
	JoyConLeft
	JoyConRight
	// JoyConLeftVertical and JoyConRightVertical are a single Joy-Con held
	// upright.
	JoyConLeftVertical
	JoyConRightVertical
	// JoyConPair is a left and a right Joy-Con connected together: one
	// controller with the standard buttons.
	JoyConPair
)

var names = [...]string{
	Unknown: "unknown", Standard: "standard",
	JoyConLeft: "joyconLeft", JoyConRight: "joyconRight",
	JoyConLeftVertical: "joyconLeftVertical", JoyConRightVertical: "joyconRightVertical",
	JoyConPair: "joyconPair",
}

// String returns the name of f, which is also its name in JSON. Apart from
// unknown and joyconPair the names are those of the families of
// config.json's safety.panic.
func (f Family) String() string {
	if int(f) < len(names) {
		return names[f]
	}
	return names[Unknown]
}

// Parse reads a name String returned.
func Parse(name string) (Family, bool) {
	i := slices.Index(names[:], name)
	if i < 0 {
		return Unknown, false
	}
	return Family(i), true
}

// IsJoyCon reports whether f is a Joy-Con, single or paired.
func (f Family) IsJoyCon() bool { return f >= JoyConLeft && f <= JoyConPair }

// IsSingleJoyCon reports whether f is a Joy-Con alone.
func (f Family) IsSingleJoyCon() bool { return f >= JoyConLeft && f <= JoyConRightVertical }

// Sideways reports whether f is a single Joy-Con held sideways.
func (f Family) Sideways() bool { return f == JoyConLeft || f == JoyConRight }

// FunctionKeys are the buttons that can be a controller's function key.
type FunctionKeys struct {
	// Default is the function key when nothing is chosen. It is Options[0].
	Default control.Control
	// Options are the possible function keys, the default first.
	Options []control.Control
}

// Allows reports whether c is one of the options.
func (k FunctionKeys) Allows(c control.Control) bool { return slices.Contains(k.Options, c) }

// FunctionKeys returns the function keys of f (ADR-0012). Guide is never
// one.
//
//   - Standard layout (Standard, Unknown): Start (Menu, Options, +), the
//     default, or Back (View, Create, Share, −).
//   - A single Joy-Con held sideways: the big trigger on its side, ZR on the
//     right one (paddle3) or ZL on the left one (paddle4), the default, or
//     the shoulder button above it, R (paddle1) or L (paddle2). SDL maps a
//     sideways Joy-Con's L and ZL to paddle2 and paddle4 and R and ZR to
//     paddle1 and paddle3, and the Joy-Con 2 driver the same
//     (ARCHITECTURE §7.1; inputtest.JoyConLeft; jc2.horizontalL).
//   - A single Joy-Con held upright: the one of Back and Start it has, − on
//     the left one and + on the right one. There is no second choice: what
//     it has besides are its shoulder buttons, which are the ones held
//     most.
//   - A pair of Joy-Cons: Back (−), the default, or Start (+).
func (f Family) FunctionKeys() FunctionKeys {
	var options []control.Control
	switch f {
	case JoyConLeft:
		options = []control.Control{control.Paddle4, control.Paddle2}
	case JoyConRight:
		options = []control.Control{control.Paddle3, control.Paddle1}
	case JoyConLeftVertical:
		options = []control.Control{control.Back}
	case JoyConRightVertical:
		options = []control.Control{control.Start}
	case JoyConPair:
		options = []control.Control{control.Back, control.Start}
	default:
		options = []control.Control{control.Start, control.Back}
	}
	return FunctionKeys{Default: options[0], Options: options}
}

// DefaultPanicHoldMs is how long the buttons of the built-in emergency combo
// are held, in milliseconds (input.ComboHold).
const DefaultPanicHoldMs = 2000

// DefaultPanic returns the buttons of f's built-in emergency combo
// (input.DefaultCombo, ARCHITECTURE §10.4), which are held for
// DefaultPanicHoldMs.
func (f Family) DefaultPanic() []string {
	switch f {
	case JoyConLeft, JoyConRight:
		return []string{"leftshoulder", "rightshoulder"}
	case JoyConLeftVertical:
		return []string{"paddle2", "paddle4"}
	case JoyConRightVertical:
		return []string{"paddle3", "paddle1"}
	}
	return []string{"back", "start"}
}

// PanicFamily returns the name of the family of config.json's retired
// safety.panic that covers f: a single Joy-Con by its side and grip, every
// other controller, a pair of Joy-Cons too, standard.
func (f Family) PanicFamily() string {
	if f.IsSingleJoyCon() {
		return f.String()
	}
	return Standard.String()
}

// Nintendo's vendor id, and the product ids of the Joy-Cons: Switch 1
// (SDL's usb_ids.h) and Joy-Con 2 (ARCHITECTURE §7.1.1).
const (
	vendorNintendo = 0x057e

	productJoyConLeft  = 0x2006
	productJoyConRight = 0x2007
	productJoyConPair  = 0x2008

	productJoyCon2Left  = 0x2067
	productJoyCon2Right = 0x2066
	productJoyCon2Pair  = 0x2068
)

// FromGUID returns the family the GUID of a DeviceKey tells, without opening
// the controller: the vendor and product ids in an SDL GUID (input.GUID)
// name a Joy-Con, and any other GUID is a controller with the standard
// layout. It is Unknown for text that is no GUID or has no ids. A single
// Joy-Con is taken as held upright when vertical is set (the hint
// SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS), else sideways: the GUID cannot
// tell, and input.FamilyOf, which sees the buttons, does.
func FromGUID(guid string, vertical bool) Family {
	vendor, product, ok := ids(guid)
	if !ok {
		return Unknown
	}
	if vendor != vendorNintendo {
		return Standard
	}
	switch product {
	case productJoyConLeft, productJoyCon2Left:
		if vertical {
			return JoyConLeftVertical
		}
		return JoyConLeft
	case productJoyConRight, productJoyCon2Right:
		if vertical {
			return JoyConRightVertical
		}
		return JoyConRight
	case productJoyConPair, productJoyCon2Pair:
		return JoyConPair
	}
	return Standard
}

// PossibleFunctionKeys returns every function key a controller with the
// GUID of a DeviceKey may have, whatever its grip: what config.json can
// hold for it. For a GUID that tells nothing it is every button that is a
// function key of some family.
func PossibleFunctionKeys(guid string) []control.Control {
	switch f := FromGUID(guid, false); f {
	case JoyConLeft:
		return slices.Concat(JoyConLeft.FunctionKeys().Options, JoyConLeftVertical.FunctionKeys().Options)
	case JoyConRight:
		return slices.Concat(JoyConRight.FunctionKeys().Options, JoyConRightVertical.FunctionKeys().Options)
	case Unknown:
		return []control.Control{control.Start, control.Back, control.Paddle1, control.Paddle2, control.Paddle3, control.Paddle4}
	default:
		return slices.Clone(f.FunctionKeys().Options)
	}
}

// ids reads the vendor and product ids of the SDL GUID guid (32 hex
// digits). SDL lays a GUID out as little-endian uint16s: bus, CRC of the
// name, vendor, 0, product, 0, version (input.GUID). With no vendor id, the
// bytes hold the name instead and there are no ids.
func ids(guid string) (vendor, product uint16, ok bool) {
	if len(guid) != 32 {
		return 0, 0, false
	}
	var b [16]byte
	for i := range b {
		hi, lo := hexDigit(guid[2*i]), hexDigit(guid[2*i+1])
		if hi < 0 || lo < 0 {
			return 0, 0, false
		}
		b[i] = byte(hi<<4 | lo)
	}
	vendor = uint16(b[4]) | uint16(b[5])<<8
	product = uint16(b[8]) | uint16(b[9])<<8
	if vendor == 0 || b[6] != 0 || b[7] != 0 || b[10] != 0 || b[11] != 0 {
		return 0, 0, false
	}
	return vendor, product, true
}

func hexDigit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}
