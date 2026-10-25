package output

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// KeyEvent is one keyboard or mouse event to inject: a key press or release,
// described independently of the injection backend. An event with Mouse set
// is a mouse event (a wheel notch, a button press or release, or a pointer
// move); every other field but Key and Up is then unused, except Dx and Dy of a
// pointer move. An event with Unicode set types one UTF-16 code unit of a text
// (OUT-21); every other field but Unit and Up is then unused.
type KeyEvent struct {
	// Key is the key the event was made from, for display and
	// bookkeeping. It does not affect injection of a keyboard event and may
	// be the zero Key.
	Key keys.Key
	// Mouse is the mouse input of a mouse event, and keys.MouseNone for a
	// keyboard event. KeyEventFor sets it for a mouse key (ADR-0062). It
	// decides what is injected: the press of a wheel key turns the wheel one
	// notch, and its release injects nothing (Silent); a button's press and
	// release are its down and up.
	Mouse keys.Mouse
	// Scan is the set-1 make code without prefix bytes.
	Scan uint8
	// Ext reports an E0 prefix: SendInput adds KEYEVENTF_EXTENDEDKEY.
	Ext bool
	// VK is the virtual-key code. It is only used when ByVK is set; for
	// scan code events the system derives the VK from the scan code and
	// the keyboard layout.
	VK uint16
	// ByVK injects the event by virtual key instead of by scan code, with
	// Scan still passed along in wScan. KeyEventFor sets it for Pause,
	// whose E1 1D 45 sequence is not a single scan code, and for the
	// numpad digits and NumDecimal, whose VK the system would otherwise
	// derive from the current NumLock state, and for the media and volume
	// keys (OUT-20), which carry an E0 scan code and Ext along.
	// MenuMaskEvent sets it for the
	// menu mask key, vk 0xE8, with Scan 0, since no physical key has it
	// (OUT-08).
	ByVK bool
	// Up reports a release rather than a press.
	Up bool
	// Dx and Dy are the distance of a keys.MouseMove event in pixels, right and
	// down (a screen's y grows downward); of a keys.MouseJump event they are the
	// absolute position, 0 to 65535 across the virtual desktop. Both are 0 for
	// every other event. The injection tests' mouse canary is a pair of moves,
	// Dx +1 then Dx -1, that a mouse hook can see and that leaves the cursor
	// where it was even if it leaks (ADR-0062).
	Dx, Dy int32
	// Unicode marks an event that types a character instead of pressing a key
	// (ADR-0070, OUT-21): the injection sets KEYEVENTF_UNICODE, with wVk 0 and the
	// UTF-16 code unit Unit in wScan. The system hands the foreground thread a
	// VK_PACKET key, which the application turns into the character. Nothing of
	// the layout, the IME or the modifier state of padmate comes into it. A
	// character outside the Basic Multilingual Plane is two such units, the
	// high surrogate and then the low one, each with its own press and release
	// (UnicodeEvent, unicodeBatch).
	//
	// It is a press and a release like a key's, but not "held": TypeText
	// always sends a unit's release in the batch of its press, so nothing refers
	// to the unit afterwards, and neither the Arbiter's key table, the ledger's
	// held-key log nor the startup sweep keeps one. The one record is the
	// Arbiter's list of releases that failed twice (Arbiter.stuck), which it
	// resends like a pending key release. The Menu mask does not take it for a
	// key (mask.go).
	Unicode bool
	// Unit is the UTF-16 code unit of a Unicode event: a character of the Basic
	// Multilingual Plane, or half of a surrogate pair. Unit 0 is valid only so
	// that the zero value of the field is harmless; TypeText never types it.
	Unit uint16
}

// UnicodeEvent returns the event that types, or with up releases, the UTF-16
// code unit unit (KEYEVENTF_UNICODE, OUT-21). It has no Key: nothing in the key
// table names it.
func UnicodeEvent(unit uint16, up bool) KeyEvent {
	return KeyEvent{Unicode: true, Unit: unit, Up: up}
}

// unicodeBatch appends to dst the events that type r: for a character of the
// Basic Multilingual Plane its press and its release; for any other, which is
// two UTF-16 code units, the press and release of the high surrogate and then
// those of the low one, so that the pair arrives back to back. r must be a
// valid code point (keys.ValidateText has seen to it).
func unicodeBatch(dst []KeyEvent, r rune) []KeyEvent {
	if r >= 0x10000 {
		hi, lo := utf16.EncodeRune(r)
		return append(dst, UnicodeEvent(uint16(hi), false), UnicodeEvent(uint16(hi), true),
			UnicodeEvent(uint16(lo), false), UnicodeEvent(uint16(lo), true))
	}
	return append(dst, UnicodeEvent(uint16(r), false), UnicodeEvent(uint16(r), true))
}

// String formats e for dry-run output, such as "down LCtrl sc=0x1D",
// "up   RCtrl sc=E0 1D", "down Pause vk=0x13 sc=0x45" or, for a mouse event,
// "down WheelUp", and for a pointer event "move +3,-2" or "jump 32768,32768".
// A Unicode event shows its unit, and the character when the unit is one that
// can be shown: "down text U+0041 'A'", "up   text U+D83D".
func (e KeyEvent) String() string {
	switch e.Mouse {
	case keys.MouseMove:
		return fmt.Sprintf("move %+d,%+d", e.Dx, e.Dy)
	case keys.MouseJump:
		return fmt.Sprintf("jump %d,%d", e.Dx, e.Dy)
	}
	var b strings.Builder
	if e.Up {
		b.WriteString("up  ")
	} else {
		b.WriteString("down")
	}
	if e.Unicode {
		fmt.Fprintf(&b, " text U+%04X", e.Unit)
		if r := rune(e.Unit); !utf16.IsSurrogate(r) && unicode.IsPrint(r) {
			b.WriteByte(' ')
			b.WriteString(strconv.QuoteRune(r))
		}
		return b.String()
	}
	if e.Key.IsValid() {
		b.WriteByte(' ')
		b.WriteString(e.Key.String())
	}
	if e.Mouse != keys.MouseNone {
		if !e.Key.IsValid() {
			b.WriteByte(' ')
			b.WriteString(e.Mouse.String())
		}
		return b.String()
	}
	if e.ByVK {
		fmt.Fprintf(&b, " vk=0x%02X", e.VK)
	}
	if e.Ext {
		fmt.Fprintf(&b, " sc=E0 %02X", e.Scan)
	} else {
		fmt.Fprintf(&b, " sc=0x%02X", e.Scan)
	}
	return b.String()
}

// release returns the event that releases the key e presses.
func (e KeyEvent) release() KeyEvent {
	e.Up = true
	return e
}

// sameKey reports whether e and o are the same physical key.
func (e KeyEvent) sameKey(o KeyEvent) bool { return e.ID() == o.ID() }

// momentary reports whether e is an event of a key with no held state: a
// wheel notch, whose press is the whole action (ADR-0062). A mouse button is
// not: it is held like a key.
func (e KeyEvent) momentary() bool { return e.Mouse.IsWheel() }

// isMouse reports whether e is a mouse event, of a wheel notch, a button or a
// pointer move. No mouse event counts as another key pressed for the menu mask
// (mask.go).
func (e KeyEvent) isMouse() bool { return e.Mouse != keys.MouseNone }

// Pointer reports whether e moves the pointer (a relative move or an absolute
// jump). It is not a key: it has no press and no release and holds nothing, so
// the ledger, the held-key log and the Arbiter's key bookkeeping never see it,
// and it is never paced (gapless).
func (e KeyEvent) Pointer() bool { return e.Mouse.IsPointer() }

// gapless reports whether e takes no part in the event gap (OUT-17): it neither
// waits for the gap nor starts one. A wheel release injects nothing, and a
// pointer move is sent at the rate of the stick, which the gap would throttle.
func (e KeyEvent) gapless() bool { return e.Silent() || e.Pointer() }

// Silent reports whether injecting e does nothing: the release of a wheel
// key, never a button's. A Sender must still count a silent event as injected
// when every event before it was (SendInput), and a dry-run printer leaves it
// out, since nothing is sent. The Arbiter still sends it, so that every press
// has its release in its ledger, in the held-key bookkeeping of Repeat and in
// the tests' recorders.
func (e KeyEvent) Silent() bool { return e.Up && e.momentary() }

// KeyID identifies a physical key: its VK for an event injected by VK,
// else its scan code and E0 flag; a mouse event is identified by its Mouse
// alone. Two events with the same KeyID press and release the same key,
// whatever their Key field says: on the US layout "Z", "KeyZ" and "sc:0x2C"
// are one KeyID. No mouse KeyID equals a keyboard one or another mouse
// input's, nor the zero KeyID, which is scan code 0. A Unicode event is
// identified by its Unit alone, and no such KeyID equals any other kind's.
type KeyID struct {
	ByVK    bool
	VK      uint16     // when ByVK
	Scan    uint8      // unless ByVK, Mouse or Unicode
	Ext     bool       // unless ByVK, Mouse or Unicode
	Mouse   keys.Mouse // the mouse input of a mouse event; MouseNone for a keyboard key
	Unicode bool       // the event types a UTF-16 code unit
	Unit    uint16     // when Unicode
}

// momentary reports whether id is a wheel key.
func (id KeyID) momentary() bool { return id.Mouse.IsWheel() }

// button reports whether id is a mouse button, a key that is held and must
// never be pressed again while it is down.
func (id KeyID) button() bool { return id.Mouse.IsButton() }

// ID returns the physical key of e.
func (e KeyEvent) ID() KeyID {
	if e.Mouse != keys.MouseNone {
		return KeyID{Mouse: e.Mouse}
	}
	if e.Unicode {
		return KeyID{Unicode: true, Unit: e.Unit}
	}
	if e.ByVK {
		return KeyID{ByVK: true, VK: e.VK}
	}
	return KeyID{Scan: e.Scan, Ext: e.Ext}
}

// String formats id like a raw KeySpec: "sc:0x2C", "sc:0xE01D", or
// "vk:0x13" for a key injected by VK; a mouse input has the name of its key,
// such as "WheelUp", and a UTF-16 code unit that is typed "text:U+0041".
func (id KeyID) String() string {
	switch {
	case id.Mouse != keys.MouseNone:
		return id.Mouse.String()
	case id.Unicode:
		return fmt.Sprintf("text:U+%04X", id.Unit)
	case id.ByVK:
		return fmt.Sprintf("vk:0x%02X", id.VK)
	case id.Ext:
		return fmt.Sprintf("sc:0xE0%02X", id.Scan)
	default:
		return fmt.Sprintf("sc:0x%02X", id.Scan)
	}
}

// KeyEventFor returns the event that presses (or, when up is set,
// releases) k on the US layout, as given by k.US(). Raw scan code keys map
// to themselves, and generic modifiers to their left key. It returns an
// error for the zero Key.
//
// Most keys are injected by scan code. Three groups go by VK, with their
// scan code in wScan (the first two measured in M0b, Windows 11 x64):
//   - Pause (E1 1D 45): VK_PAUSE with wScan 0x45, which hooks see as
//     vk=0x13 sc=0x45, like the physical key.
//   - Num0–Num9 and NumDecimal: an injected scan code 0x47–0x53 is
//     translated like the physical key, so it gives VK_NUMPAD7 for 0x47
//     with NumLock on and VK_HOME with NumLock off. By VK they always
//     arrive as VK_NUMPAD0–9 and VK_DECIMAL, whatever the NumLock state.
//   - The media and volume keys (VolumeUp and its kin, ADR-0070, OUT-20): the
//     VK of VK_VOLUME_UP, VK_MEDIA_PLAY_PAUSE and the like, with the E0 scan
//     code in wScan and KEYEVENTF_EXTENDEDKEY, as the physical key sends them.
//     Whatever the layout, the key is the key; what the hook sees of it has not
//     been measured yet (TestInjectKeys covers them).
//
// The menu mask key is the only other event sent by VK; it is not a Key
// (MenuMaskEvent).
//
// Character keys use their US scan code; resolving them against the
// foreground layout is the Resolver's job (OUT-04).
//
// A mouse key (a wheel notch or a button) makes a mouse event: no scan code,
// no VK, no layout and no NumLock. A wheel notch is momentary: its release is Silent. A
// button is held like a key: its press is a button down, its release a button
// up.
func KeyEventFor(k keys.Key, up bool) (KeyEvent, error) {
	if !k.IsValid() {
		return KeyEvent{}, fmt.Errorf("output: invalid key")
	}
	if m, ok := k.Mouse(); ok {
		return KeyEvent{Key: k, Mouse: m, Up: up}, nil
	}
	us := k.US()
	e := KeyEvent{Key: k, Scan: us.Scan, Ext: us.Ext, Up: up}
	if us.E1 || numLockVK(us.VK) || k.Kind() == keys.KindMedia {
		e.ByVK, e.VK = true, us.VK
	}
	return e, nil
}

// numLockVK reports whether vk is one of the numpad VKs that the system
// produces from a numpad scan code only while NumLock is on:
// VK_NUMPAD0–VK_NUMPAD9 and VK_DECIMAL.
func numLockVK(vk uint16) bool {
	return vk >= 0x60 && vk <= 0x69 || vk == 0x6E
}

// TypedText returns the text the Unicode presses of events type, in order, for
// a dry-run printer: the units of a surrogate pair become the one character they
// make, and a half that has no partner (which TypeText never sends) becomes
// U+FFFD. Events that are not Unicode presses, and every release, add nothing.
func TypedText(events []KeyEvent) string {
	var units []uint16
	for _, e := range events {
		if e.Unicode && !e.Up {
			units = append(units, e.Unit)
		}
	}
	return string(utf16.Decode(units))
}
