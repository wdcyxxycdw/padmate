// Package keys defines the keyboard keys padmate can output, the KeySpec
// syntax that names them ("Ctrl+Shift+Z"), and the US-layout baseline that
// maps each key to a set-1 scan code and a Windows virtual-key code. The
// mouse wheel notches (WheelUp, ...) and buttons (MouseLeft, ...) are keys too
// (KindMouse, ADR-0062): they have no scan code and no VK. The volume and media
// keys (VolumeUp, MediaNext, ..., KindMedia, ADR-0070) are keyboard keys with an
// E0 scan code and a VK, which the output layer sends by VK. The rules for the
// text a binding types (ValidateText, ADR-0070) are here too, so that profile
// parsing, scripts and the output share them.
//
// The package is pure data and string processing with no platform
// dependencies, so it builds and tests on every OS. Character keys are
// resolved against the foreground keyboard layout at send time (M1b); the
// US mapping here is what that resolver falls back to.
package keys

import (
	"math/bits"
	"slices"
	"strings"
)

// Kind classifies a Key.
type Kind uint8

// Key kinds. The zero Kind marks an invalid Key.
const (
	KindInvalid Kind = iota
	// KindNamed is a key with a fixed function, such as F13, Enter or Up.
	KindNamed
	// KindChar is "the key that types this character": "Z", "1", "[" or
	// "Plus". It is resolved per keyboard layout at send time.
	KindChar
	// KindPhysical is a fixed position of the US layout, such as "KeyZ",
	// that stays put whatever layout is active.
	KindPhysical
	// KindScan is a raw set-1 scan code, such as "sc:0x2C" or "sc:0xE01D".
	KindScan
	// KindModifier is Ctrl, Shift, Alt or Win, generic or sided.
	KindModifier
	// KindMouse is a mouse input standing in a key's place (ADR-0062): the
	// four wheel notches WheelUp, WheelDown, WheelLeft and WheelRight, and the
	// five buttons MouseLeft, MouseRight, MouseMiddle, MouseX1 and MouseX2. It
	// has no scan code, no VK and no layout; Key.Mouse says which input it is.
	// It can be the main key of a chord, never a modifier.
	KindMouse
	// KindMedia is a volume or media key (ADR-0070, OUT-20): VolumeUp,
	// VolumeDown, VolumeMute, MediaPlayPause, MediaNext, MediaPrevious and
	// MediaStop. It is a keyboard key with an E0 scan code and the VK of its
	// VK_VOLUME_* or VK_MEDIA_* constant, and the output layer sends it by VK
	// (with the scan code and the extended-key flag along), whatever the
	// layout: it never goes through the layout lookup of a character key. It
	// can be the main key of a chord, never a modifier.
	KindMedia
)

var kindNames = [...]string{
	KindInvalid:  "invalid",
	KindNamed:    "named",
	KindChar:     "char",
	KindPhysical: "physical",
	KindScan:     "scan",
	KindModifier: "modifier",
	KindMouse:    "mouse",
	KindMedia:    "media",
}

// Mouse names a mouse input a KindMouse key stands for. The zero value,
// MouseNone, is not a mouse input.
type Mouse uint8

// Mouse inputs. New ones are appended, so the values stay stable.
const (
	MouseNone Mouse = iota
	// WheelUp, WheelDown, WheelLeft and WheelRight each turn the wheel one
	// notch (120 units) when pressed, and do nothing when released.
	WheelUp
	WheelDown
	WheelLeft
	WheelRight
	// MouseLeft, MouseRight, MouseMiddle, MouseX1 and MouseX2 are the buttons.
	// A button has a held state like a keyboard key: it goes down when pressed
	// and up when released. Nothing moves the cursor.
	MouseLeft
	MouseRight
	MouseMiddle
	MouseX1
	MouseX2
	// MouseMove and MouseJump are not keys and have no KeySpec name: they tell
	// the output layer that an event moves the pointer, relatively by a number of
	// pixels (MouseMove) or to an absolute position (MouseJump), which the stick
	// in mode "mouse" and the "mouse" action produce (ADR-0062, M15b). No Key has
	// them, so no profile can spell them as a key.
	MouseMove
	MouseJump
)

var mouseNames = [...]string{
	MouseNone:   "",
	WheelUp:     "WheelUp",
	WheelDown:   "WheelDown",
	WheelLeft:   "WheelLeft",
	WheelRight:  "WheelRight",
	MouseLeft:   "MouseLeft",
	MouseRight:  "MouseRight",
	MouseMiddle: "MouseMiddle",
	MouseX1:     "MouseX1",
	MouseX2:     "MouseX2",
	// Not key names (no table row has them, Parse refuses them): only for
	// messages and dry-run text.
	MouseMove: "MouseMove",
	MouseJump: "MouseJump",
}

// String returns the canonical key name of m, such as "WheelUp", or "" for
// MouseNone and a value that names no input.
func (m Mouse) String() string {
	if int(m) < len(mouseNames) {
		return mouseNames[m]
	}
	return ""
}

// IsWheel reports whether m is a wheel notch. A notch is momentary: pressing
// it is the whole action, and releasing it injects nothing.
func (m Mouse) IsWheel() bool { return m >= WheelUp && m <= WheelRight }

// IsButton reports whether m is a mouse button. A button is held like a key:
// it stays down until it is released, and must never be pressed again while
// it is down (no typematic repeat).
func (m Mouse) IsButton() bool { return m >= MouseLeft && m <= MouseX2 }

// IsPointer reports whether m moves the pointer instead of standing in a key's
// place: MouseMove or MouseJump. Such an event has no press and no release, and
// holds nothing.
func (m Mouse) IsPointer() bool { return m == MouseMove || m == MouseJump }

// String returns the lower-case name of k, such as "named" or "char".
func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "invalid"
}

// Key is one keyboard key. It is a small comparable value, usable as a map
// key. Different spellings of the same position are different Keys: the
// character key Z, the physical key KeyZ and the raw scan code sc:0x2C all
// compare unequal and each formats back to its own spelling. The zero Key is
// invalid.
type Key struct {
	kind Kind
	// code is the table index for table keys, and the scan code (0xNN, or
	// 0xE0NN when E0-extended) for KindScan.
	code uint16
}

// Kind returns the kind of k.
func (k Key) Kind() Kind { return k.kind }

// IsValid reports whether k names a key. The zero Key is invalid.
func (k Key) IsValid() bool { return k.kind != KindInvalid }

// IsModifier reports whether k is one of the twelve modifiers. A raw scan
// code is never a modifier, even when it is the scan code of one.
func (k Key) IsModifier() bool { return k.kind == KindModifier }

// Mod returns the modifier bit of k, or 0 when k is not a modifier.
func (k Key) Mod() Mods {
	if k.kind != KindModifier {
		return 0
	}
	return tbl.entries[k.code].mod
}

// Mouse returns the mouse input of a KindMouse key. It returns false for
// every other key, which are keyboard keys.
func (k Key) Mouse() (Mouse, bool) {
	if k.kind != KindMouse {
		return MouseNone, false
	}
	return tbl.entries[k.code].mouse, true
}

// IsMouseButton reports whether k is a mouse button (MouseLeft and its kin),
// a key that is held like a keyboard key and must never be pressed again
// while it is down.
func (k Key) IsMouseButton() bool {
	m, ok := k.Mouse()
	return ok && m.IsButton()
}

// String returns the canonical spelling of k, such as "LShift", "Z",
// "KeyZ" or "sc:0xE01D". It returns "" for the zero Key.
func (k Key) String() string {
	switch k.kind {
	case KindInvalid:
		return ""
	case KindScan:
		return formatScan(k.code)
	default:
		return tbl.entries[k.code].name
	}
}

// Char returns the character a character key types: lower case for letters
// ('z' for "Z"), the character itself for digits and punctuation, and '+'
// for "Plus". The layout resolver (M1b) must look keys up by this character,
// not by String: the name of a letter key is upper case, and on Windows an
// upper-case letter resolves to Shift plus the key, so "Ctrl+Z" would send
// Ctrl+Shift+Z. It returns false for every key that is not KindChar.
func (k Key) Char() (rune, bool) {
	if k.kind != KindChar {
		return 0, false
	}
	return tbl.entries[k.code].char, true
}

// Aliases returns the alternative spellings Parse accepts for k, in
// addition to its canonical name and case variations. It returns nil for
// raw scan codes and for keys without aliases.
func (k Key) Aliases() []string {
	if k.kind == KindInvalid || k.kind == KindScan {
		return nil
	}
	return slices.Clone(tbl.entries[k.code].aliases)
}

// US returns how k is produced on the US English layout (00000409).
//
// For character keys this is only the baseline: the layout resolver (M1b)
// replaces it with the mapping of the foreground layout. A generic modifier
// maps to its left key. A raw scan code maps to itself, with VK 0 because
// its virtual key depends on the layout. A mouse key is no keyboard key and
// maps to the zero USMapping.
func (k Key) US() USMapping {
	switch k.kind {
	case KindInvalid:
		return USMapping{}
	case KindScan:
		return USMapping{Scan: uint8(k.code), Ext: k.code>>8 == 0xE0}
	default:
		return tbl.entries[k.code].us
	}
}

// USMapping describes how a key is produced on the US English layout.
type USMapping struct {
	// Scan is the set-1 make code without prefix bytes.
	Scan uint8
	// Ext reports an E0 prefix. SendInput needs KEYEVENTF_EXTENDEDKEY.
	Ext bool
	// E1 reports the E1 1D prefix of Pause (E1 1D 45). Such a key cannot be
	// injected as a scan code; it is injected by VK instead.
	E1 bool
	// VK is the Windows virtual-key code, or 0 when unknown.
	VK uint16
	// Shift reports that the key needs Shift held to type its character on
	// the US layout. Only Plus sets it: "+" is Shift+= on the US layout.
	Shift bool
}

// ScanString formats the scan code of m for display: "0x2C" for a plain
// key, "E0 1D" for an E0-extended key and "E1 1D 45" for Pause.
func (m USMapping) ScanString() string {
	switch {
	case m.E1:
		return "E1 1D " + hexByte(m.Scan)
	case m.Ext:
		return "E0 " + hexByte(m.Scan)
	default:
		return "0x" + hexByte(m.Scan)
	}
}

// Mods is a set of modifiers. The bit order is the canonical order in
// which chords list their modifiers.
type Mods uint16

// Modifier bits, in canonical order.
const (
	ModCtrl Mods = 1 << iota
	ModLCtrl
	ModRCtrl
	ModShift
	ModLShift
	ModRShift
	ModAlt
	ModLAlt
	ModRAlt
	ModWin
	ModLWin
	ModRWin
)

// numMods is the number of modifier bits.
const numMods = 12

// allMods is the set of every modifier bit.
const allMods = Mods(1)<<numMods - 1

// Has reports whether m contains every modifier in o.
func (m Mods) Has(o Mods) bool { return m&o == o }

// Keys returns the modifier keys in m in canonical order.
func (m Mods) Keys() []Key {
	m &= allMods
	out := make([]Key, 0, bits.OnesCount16(uint16(m)))
	for m != 0 {
		i := bits.TrailingZeros16(uint16(m))
		out = append(out, tbl.mods[i])
		m &^= 1 << i
	}
	return out
}

// String returns the canonical names of the modifiers in m joined by "+",
// such as "Ctrl+Shift", or "" for the empty set.
func (m Mods) String() string {
	var b strings.Builder
	m.appendTo(&b)
	s := b.String()
	return strings.TrimSuffix(s, "+")
}

// appendTo writes every modifier of m followed by "+" to b.
func (m Mods) appendTo(b *strings.Builder) {
	m &= allMods
	for m != 0 {
		i := bits.TrailingZeros16(uint16(m))
		b.WriteString(tbl.entries[tbl.mods[i].code].name)
		b.WriteByte('+')
		m &^= 1 << i
	}
}

// Chord is a set of modifiers plus one key: the parsed form of a KeySpec.
// It is a small comparable value. The zero Chord is invalid.
//
// A Chord is always canonical: its key is never also in its modifier set,
// and a chord made only of modifiers ("Shift+Ctrl") keeps the modifier that
// sorts last as its key ("Ctrl+Shift" has modifiers Ctrl and key Shift).
type Chord struct {
	mods Mods
	key  Key
}

// Mods returns the modifiers of c, not including its key.
func (c Chord) Mods() Mods { return c.mods }

// Key returns the last key of c: the non-modifier key, or for a chord made
// only of modifiers, the modifier that sorts last.
func (c Chord) Key() Key { return c.key }

// IsValid reports whether c holds a key. The zero Chord is invalid.
func (c Chord) IsValid() bool { return c.key.IsValid() }

// Keys returns every key of c in press order: the modifiers in canonical
// order, then the key.
func (c Chord) Keys() []Key {
	if !c.IsValid() {
		return nil
	}
	return append(c.mods.Keys(), c.key)
}

// genericMods is the set of modifiers without a side. Each one's left key
// is the next bit up: ModCtrl<<1 == ModLCtrl.
const genericMods = ModCtrl | ModShift | ModAlt | ModWin

// Sided returns c with every generic modifier (Ctrl, Shift, Alt, Win)
// replaced by its left key, in the modifier set and as the key of a chord
// made only of modifiers: "Ctrl+Z" becomes "LCtrl+Z" and "Ctrl+Shift"
// becomes "LCtrl+LShift". The result is canonical and presses the same US
// keys in the same order, since a generic modifier maps to its left key.
// The Output Arbiter counts keys by this form (OUT-06). The zero Chord
// stays zero.
func (c Chord) Sided() Chord {
	if !c.IsValid() {
		return c
	}
	if !c.key.IsModifier() {
		return Chord{mods: sidedMods(c.mods), key: c.key}
	}
	// The key may become a different bit; canonicalize the set again.
	return modifierChord(sidedMods(c.mods | c.key.Mod()))
}

// sidedMods replaces every generic modifier in m by its left key. A
// canonical set never has both, so no bit is lost.
func sidedMods(m Mods) Mods {
	g := m & genericMods
	return m&^g | g<<1
}

// String returns the canonical KeySpec of c, such as "Ctrl+Shift+Z". It
// returns "" for the zero Chord.
func (c Chord) String() string {
	if !c.IsValid() {
		return ""
	}
	if c.mods == 0 {
		return c.key.String()
	}
	var b strings.Builder
	b.Grow(32)
	c.mods.appendTo(&b)
	b.WriteString(c.key.String())
	return b.String()
}

const hexDigits = "0123456789ABCDEF"

// hexByte formats v as two upper-case hex digits.
func hexByte(v uint8) string {
	return string([]byte{hexDigits[v>>4], hexDigits[v&0xF]})
}

// formatScan formats a raw scan code: "sc:0x2C", or "sc:0xE01D" when it is
// E0-extended.
func formatScan(code uint16) string {
	if code > 0xFF {
		return "sc:0x" + hexByte(uint8(code>>8)) + hexByte(uint8(code))
	}
	return "sc:0x" + hexByte(uint8(code))
}
