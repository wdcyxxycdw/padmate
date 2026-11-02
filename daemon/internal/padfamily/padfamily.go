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
	// JoyConLeft and JoyConRight are a single Joy-Con, which padmate always
	// holds upright (ARCHITECTURE §7.1): SDL gets the hint
	// SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS, and the Joy-Con 2 driver maps
	// the same way. A sideways grip no longer exists.
	JoyConLeft
	JoyConRight
	// JoyConPair is a left and a right Joy-Con connected together: one
	// controller with the standard buttons.
	JoyConPair
)

var names = [...]string{
	Unknown: "unknown", Standard: "standard",
	JoyConLeft: "joyconLeft", JoyConRight: "joyconRight",
	JoyConPair: "joyconPair",
}

// RetiredPanicFamilies are the members of the retired config.json
// safety.panic that name a grip padmate no longer has: a Joy-Con held
// upright is joyconLeft or joyconRight now. A file may still hold them; the
// daemon reads and ignores them, with a warning (profile.ParseConfig).
var RetiredPanicFamilies = [...]string{"joyconLeftVertical", "joyconRightVertical"}

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
func (f Family) IsSingleJoyCon() bool { return f == JoyConLeft || f == JoyConRight }

// The buttons SDL gives a single Joy-Con held upright, as read from the
// source of SDL 3.4.16 (SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS=1;
// inputtest.JoyConLeft and JoyConRight, jc2.verticalL and verticalR). The
// triggers are axes, no buttons.
var (
	joyConLeftButtons = []string{
		"back", "dpad.up", "dpad.down", "dpad.left", "dpad.right",
		"leftshoulder", "leftstick", "misc1", "paddle2", "paddle4",
	}
	joyConRightButtons = []string{
		"south", "east", "west", "north", "guide", "start",
		"rightshoulder", "rightstick", "misc2", "paddle1", "paddle3",
	}
)

// HasButton reports whether a controller of family f has the SDL button
// named name (input.ButtonName). Only a single Joy-Con is known to lack
// buttons: every other family answers true, since a pad of the standard
// layout may have any of them, and it is never asked about a name that is no
// button.
func (f Family) HasButton(name string) bool {
	switch f {
	case JoyConLeft:
		return slices.Contains(joyConLeftButtons, name)
	case JoyConRight:
		return slices.Contains(joyConRightButtons, name)
	}
	return true
}

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
//   - A single Joy-Con, held upright: the big trigger under its index
//     finger, ZL (LT) on the left one and ZR (RT) on the right one, the
//     default, or the shoulder button above it, L (LeftShoulder) or R
//     (RightShoulder). The trigger is a virtual button of the trigger axis
//     (control.LT, with the thresholds of the profile). − and + are not
//     offered: they are out of reach while the Joy-Con is held.
//   - A pair of Joy-Cons: Back (−), the default, or Start (+).
func (f Family) FunctionKeys() FunctionKeys {
	var options []control.Control
	switch f {
	case JoyConLeft:
		options = []control.Control{control.LT, control.LeftShoulder}
	case JoyConRight:
		options = []control.Control{control.RT, control.RightShoulder}
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
// DefaultPanicHoldMs: SL and SR on a single Joy-Con, which SDL reports as
// paddles (paddle2 and paddle4 on the left one, paddle3 and paddle1 on the
// right one), and back and start on every other controller.
func (f Family) DefaultPanic() []string {
	switch f {
	case JoyConLeft:
		return []string{"paddle2", "paddle4"}
	case JoyConRight:
		return []string{"paddle3", "paddle1"}
	}
	return []string{"back", "start"}
}

// PanicFamily returns the name of the family of config.json's retired
// safety.panic that covers f: a single Joy-Con by its side, every other
// controller, a pair of Joy-Cons too, standard.
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
// layout. It is Unknown for text that is no GUID or has no ids.
func FromGUID(guid string) Family {
	vendor, product, ok := ids(guid)
	if !ok {
		return Unknown
	}
	if vendor != vendorNintendo {
		return Standard
	}
	switch product {
	case productJoyConLeft, productJoyCon2Left:
		return JoyConLeft
	case productJoyConRight, productJoyCon2Right:
		return JoyConRight
	case productJoyConPair, productJoyCon2Pair:
		return JoyConPair
	}
	return Standard
}

// PossibleFunctionKeys returns every function key a controller with the
// GUID of a DeviceKey may have: what config.json can hold for it. For a GUID
// that tells nothing it is every button that is a function key of some
// family.
func PossibleFunctionKeys(guid string) []control.Control {
	if f := FromGUID(guid); f != Unknown {
		return slices.Clone(f.FunctionKeys().Options)
	}
	return []control.Control{control.Start, control.Back, control.LT, control.RT, control.LeftShoulder, control.RightShoulder}
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
