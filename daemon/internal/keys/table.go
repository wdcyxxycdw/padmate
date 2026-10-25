package keys

import "fmt"

// The key table. Scan codes are set-1 make codes as listed in the Microsoft
// keyboard scan code specification; VK codes are the WinUser.h constants.
// Both describe the US English layout (00000409). The table is pinned
// entry by entry in table_test.go: change both together, deliberately.

// row is one table entry as written in the literals below. scan is 0xNN
// for a plain key, 0xE0NN for an E0-extended key, and 0xE11D45 for Pause.
type row struct {
	name    string
	scan    uint32
	vk      uint16
	aliases []string
}

// modifierRows are the modifiers in canonical order: row i has Mods bit
// 1<<i. A generic modifier maps to its left key.
var modifierRows = [numMods]row{
	{"Ctrl", 0x1D, 0xA2, []string{"Control"}},             // VK_LCONTROL
	{"LCtrl", 0x1D, 0xA2, []string{"ControlLeft"}},        // VK_LCONTROL
	{"RCtrl", 0xE01D, 0xA3, []string{"ControlRight"}},     // VK_RCONTROL
	{"Shift", 0x2A, 0xA0, nil},                            // VK_LSHIFT
	{"LShift", 0x2A, 0xA0, []string{"ShiftLeft"}},         // VK_LSHIFT
	{"RShift", 0x36, 0xA1, []string{"ShiftRight"}},        // VK_RSHIFT
	{"Alt", 0x38, 0xA4, nil},                              // VK_LMENU
	{"LAlt", 0x38, 0xA4, []string{"AltLeft"}},             // VK_LMENU
	{"RAlt", 0xE038, 0xA5, []string{"AltRight", "AltGr"}}, // VK_RMENU
	{"Win", 0xE05B, 0x5B, []string{"Meta"}},               // VK_LWIN
	{"LWin", 0xE05B, 0x5B, []string{"MetaLeft"}},          // VK_LWIN
	{"RWin", 0xE05C, 0x5C, []string{"MetaRight"}},         // VK_RWIN
}

// namedRows are the keys with a fixed function.
var namedRows = [...]row{
	{"F1", 0x3B, 0x70, nil},
	{"F2", 0x3C, 0x71, nil},
	{"F3", 0x3D, 0x72, nil},
	{"F4", 0x3E, 0x73, nil},
	{"F5", 0x3F, 0x74, nil},
	{"F6", 0x40, 0x75, nil},
	{"F7", 0x41, 0x76, nil},
	{"F8", 0x42, 0x77, nil},
	{"F9", 0x43, 0x78, nil},
	{"F10", 0x44, 0x79, nil},
	{"F11", 0x57, 0x7A, nil},
	{"F12", 0x58, 0x7B, nil},
	// F13-F21 are 0x64-0x6C, F22 is 0x6D, F23 is 0x6E and F24 is 0x76.
	{"F13", 0x64, 0x7C, nil},
	{"F14", 0x65, 0x7D, nil},
	{"F15", 0x66, 0x7E, nil},
	{"F16", 0x67, 0x7F, nil},
	{"F17", 0x68, 0x80, nil},
	{"F18", 0x69, 0x81, nil},
	{"F19", 0x6A, 0x82, nil},
	{"F20", 0x6B, 0x83, nil},
	{"F21", 0x6C, 0x84, nil},
	{"F22", 0x6D, 0x85, nil},
	{"F23", 0x6E, 0x86, nil},
	{"F24", 0x76, 0x87, nil},

	{"Esc", 0x01, 0x1B, []string{"Escape"}},       // VK_ESCAPE
	{"Tab", 0x0F, 0x09, nil},                      // VK_TAB
	{"Space", 0x39, 0x20, nil},                    // VK_SPACE
	{"Enter", 0x1C, 0x0D, []string{"Return"}},     // VK_RETURN
	{"Backspace", 0x0E, 0x08, nil},                // VK_BACK
	{"Insert", 0xE052, 0x2D, []string{"Ins"}},     // VK_INSERT
	{"Delete", 0xE053, 0x2E, []string{"Del"}},     // VK_DELETE
	{"Home", 0xE047, 0x24, nil},                   // VK_HOME
	{"End", 0xE04F, 0x23, nil},                    // VK_END
	{"PageUp", 0xE049, 0x21, []string{"PgUp"}},    // VK_PRIOR
	{"PageDown", 0xE051, 0x22, []string{"PgDn"}},  // VK_NEXT
	{"Up", 0xE048, 0x26, []string{"ArrowUp"}},     // VK_UP
	{"Down", 0xE050, 0x28, []string{"ArrowDown"}}, // VK_DOWN
	{"Left", 0xE04B, 0x25, []string{"ArrowLeft"}}, // VK_LEFT
	{"Right", 0xE04D, 0x27, []string{"ArrowRight"}},

	{"CapsLock", 0x3A, 0x14, nil},                           // VK_CAPITAL
	{"NumLock", 0x45, 0x90, nil},                            // VK_NUMLOCK; per the Win32 docs the physical key carries the extended flag, an injected 0x45 does not (measured)
	{"ScrollLock", 0x46, 0x91, nil},                         // VK_SCROLL
	{"PrintScreen", 0xE037, 0x2C, []string{"PrtSc"}},        // VK_SNAPSHOT
	{"Pause", 0xE11D45, 0x13, nil},                          // VK_PAUSE; E1 1D 45, injected by VK
	{"Menu", 0xE05D, 0x5D, []string{"Apps", "ContextMenu"}}, // VK_APPS

	{"Num0", 0x52, 0x60, []string{"Numpad0"}}, // VK_NUMPAD0
	{"Num1", 0x4F, 0x61, []string{"Numpad1"}},
	{"Num2", 0x50, 0x62, []string{"Numpad2"}},
	{"Num3", 0x51, 0x63, []string{"Numpad3"}},
	{"Num4", 0x4B, 0x64, []string{"Numpad4"}},
	{"Num5", 0x4C, 0x65, []string{"Numpad5"}},
	{"Num6", 0x4D, 0x66, []string{"Numpad6"}},
	{"Num7", 0x47, 0x67, []string{"Numpad7"}},
	{"Num8", 0x48, 0x68, []string{"Numpad8"}},
	{"Num9", 0x49, 0x69, []string{"Numpad9"}},
	{"NumAdd", 0x4E, 0x6B, []string{"NumpadAdd"}},           // VK_ADD
	{"NumSubtract", 0x4A, 0x6D, []string{"NumpadSubtract"}}, // VK_SUBTRACT
	{"NumMultiply", 0x37, 0x6A, []string{"NumpadMultiply"}}, // VK_MULTIPLY
	{"NumDivide", 0xE035, 0x6F, []string{"NumpadDivide"}},   // VK_DIVIDE
	{"NumDecimal", 0x53, 0x6E, []string{"NumpadDecimal"}},   // VK_DECIMAL
	{"NumEnter", 0xE01C, 0x0D, []string{"NumpadEnter"}},     // VK_RETURN, extended

	// ISO and JIS keys. They are not on the US 101-key keyboard. Their VK is
	// the scan-code-to-VK entry of the layout that has them (an ISO layout,
	// or kbd106.dll for Japanese 106/109) and is informational only, as they
	// are sent by scan code. Keys flagged KBDSPECIAL in that table may be
	// translated again by the IME at run time.
	{"IntlBackslash", 0x56, 0xE2, nil}, // VK_OEM_102
	{"IntlRo", 0x73, 0xE2, nil},        // VK_OEM_102 on kbd106
	{"IntlYen", 0x7D, 0xDC, nil},       // VK_OEM_5 on kbd106
	{"Convert", 0x79, 0x1C, nil},       // VK_CONVERT on kbd106, KBDSPECIAL
	{"NonConvert", 0x7B, 0x1D, nil},    // VK_NONCONVERT on kbd106, KBDSPECIAL
	{"KanaMode", 0x70, 0xF2, nil},      // VK_DBE_HIRAGANA (VK_OEM_COPY) on kbd106, KBDSPECIAL
}

// mediaRows are the volume and media keys (ADR-0070, OUT-20). Their scan codes
// are the E0 codes of the Microsoft set-1 table (Volume Up E0 30, Volume Down
// E0 2E, Mute E0 20, Play/Pause E0 22, Next Track E0 19, Previous Track E0 10,
// Stop E0 24) and their VKs the VK_VOLUME_* and VK_MEDIA_* constants of
// WinUser.h. The aliases are the W3C code names; the VK constant names are not
// accepted. They come after the physical keys and before the mouse rows, so
// that no keyboard key moves and the mouse keys, which are no keyboard keys,
// stay last.
var mediaRows = [...]row{
	{"VolumeUp", 0xE030, 0xAF, []string{"AudioVolumeUp"}},           // VK_VOLUME_UP
	{"VolumeDown", 0xE02E, 0xAE, []string{"AudioVolumeDown"}},       // VK_VOLUME_DOWN
	{"VolumeMute", 0xE020, 0xAD, []string{"AudioVolumeMute"}},       // VK_VOLUME_MUTE
	{"MediaPlayPause", 0xE022, 0xB3, nil},                           // VK_MEDIA_PLAY_PAUSE
	{"MediaNext", 0xE019, 0xB0, []string{"MediaTrackNext"}},         // VK_MEDIA_NEXT_TRACK
	{"MediaPrevious", 0xE010, 0xB1, []string{"MediaTrackPrevious"}}, // VK_MEDIA_PREV_TRACK
	{"MediaStop", 0xE024, 0xB2, nil},                                // VK_MEDIA_STOP
}

// mouseRow is one mouse input that stands in a key's place (ADR-0062). It has
// no scan code and no VK. The rows come last in the table, so that the
// indices of the keyboard keys never change.
type mouseRow struct {
	name    string
	mouse   Mouse
	aliases []string
}

// mouseRows are the mouse keys, in listing order: the wheel notches, then the
// buttons, whose aliases are the Windows virtual-key names (VK_LBUTTON, ...).
var mouseRows = [...]mouseRow{
	{"WheelUp", WheelUp, nil},
	{"WheelDown", WheelDown, nil},
	{"WheelLeft", WheelLeft, nil},
	{"WheelRight", WheelRight, nil},
	{"MouseLeft", MouseLeft, []string{"LButton"}},
	{"MouseRight", MouseRight, []string{"RButton"}},
	{"MouseMiddle", MouseMiddle, []string{"MButton"}},
	{"MouseX1", MouseX1, []string{"XButton1"}},
	{"MouseX2", MouseX2, []string{"XButton2"}},
}

// charRow is one position of the US main block. It yields a character key
// named char and a physical key named "Key"+char; code is the W3C code
// name, accepted as an alias of the physical key when it differs.
type charRow struct {
	char string
	code string
	scan uint32
	vk   uint16
}

var charRows = [...]charRow{
	{"A", "KeyA", 0x1E, 0x41},
	{"B", "KeyB", 0x30, 0x42},
	{"C", "KeyC", 0x2E, 0x43},
	{"D", "KeyD", 0x20, 0x44},
	{"E", "KeyE", 0x12, 0x45},
	{"F", "KeyF", 0x21, 0x46},
	{"G", "KeyG", 0x22, 0x47},
	{"H", "KeyH", 0x23, 0x48},
	{"I", "KeyI", 0x17, 0x49},
	{"J", "KeyJ", 0x24, 0x4A},
	{"K", "KeyK", 0x25, 0x4B},
	{"L", "KeyL", 0x26, 0x4C},
	{"M", "KeyM", 0x32, 0x4D},
	{"N", "KeyN", 0x31, 0x4E},
	{"O", "KeyO", 0x18, 0x4F},
	{"P", "KeyP", 0x19, 0x50},
	{"Q", "KeyQ", 0x10, 0x51},
	{"R", "KeyR", 0x13, 0x52},
	{"S", "KeyS", 0x1F, 0x53},
	{"T", "KeyT", 0x14, 0x54},
	{"U", "KeyU", 0x16, 0x55},
	{"V", "KeyV", 0x2F, 0x56},
	{"W", "KeyW", 0x11, 0x57},
	{"X", "KeyX", 0x2D, 0x58},
	{"Y", "KeyY", 0x15, 0x59},
	{"Z", "KeyZ", 0x2C, 0x5A},
	{"0", "Digit0", 0x0B, 0x30},
	{"1", "Digit1", 0x02, 0x31},
	{"2", "Digit2", 0x03, 0x32},
	{"3", "Digit3", 0x04, 0x33},
	{"4", "Digit4", 0x05, 0x34},
	{"5", "Digit5", 0x06, 0x35},
	{"6", "Digit6", 0x07, 0x36},
	{"7", "Digit7", 0x08, 0x37},
	{"8", "Digit8", 0x09, 0x38},
	{"9", "Digit9", 0x0A, 0x39},
	{"-", "Minus", 0x0C, 0xBD},        // VK_OEM_MINUS
	{"=", "Equal", 0x0D, 0xBB},        // VK_OEM_PLUS
	{"[", "BracketLeft", 0x1A, 0xDB},  // VK_OEM_4
	{"]", "BracketRight", 0x1B, 0xDD}, // VK_OEM_6
	{";", "Semicolon", 0x27, 0xBA},    // VK_OEM_1
	{"'", "Quote", 0x28, 0xDE},        // VK_OEM_7
	{"`", "Backquote", 0x29, 0xC0},    // VK_OEM_3
	{"\\", "Backslash", 0x2B, 0xDC},   // VK_OEM_5
	{",", "Comma", 0x33, 0xBC},        // VK_OEM_COMMA
	{".", "Period", 0x34, 0xBE},       // VK_OEM_PERIOD
	{"/", "Slash", 0x35, 0xBF},        // VK_OEM_2
}

// plusRow is the "+" character. "+" separates chord tokens, so it is
// spelled Plus. On the US layout it is Shift+=; the layout resolver (M1b)
// replaces that with whatever types "+" on the foreground layout.
var plusRow = row{"Plus", 0x0D, 0xBB, nil}

// entry is one key of the table.
type entry struct {
	name    string
	kind    Kind
	us      USMapping
	mod     Mods
	aliases []string
	// char is the character a KindChar key types, lower-cased for letters;
	// 0 for every other kind.
	char rune
	// mouse is the input of a KindMouse key; MouseNone for every other kind.
	mouse Mouse
}

// keyTable is the compiled key table.
type keyTable struct {
	entries []entry
	// index maps every lower-cased name and alias to its entry.
	index map[string]uint16
	// mods maps a modifier bit index to its key.
	mods [numMods]Key
}

// maxNameLen bounds the length of names and aliases so lookups can fold
// case into a stack buffer.
const maxNameLen = 32

var tbl = newKeyTable()

// newKeyTable compiles the table literals. It panics on a malformed table,
// which the package tests catch.
func newKeyTable() *keyTable {
	t := &keyTable{index: make(map[string]uint16)}
	add := func(kind Kind, r row, mod Mods) {
		e := entry{name: r.name, kind: kind, us: usMapping(r.scan, r.vk), mod: mod, aliases: r.aliases}
		idx := uint16(len(t.entries))
		t.entries = append(t.entries, e)
		for _, name := range append([]string{r.name}, r.aliases...) {
			if len(name) == 0 || len(name) > maxNameLen {
				panic(fmt.Sprintf("keys: bad name length %q", name))
			}
			folded := foldASCII(name)
			if prev, dup := t.index[folded]; dup {
				panic(fmt.Sprintf("keys: %q of %s collides with %s", name, r.name, t.entries[prev].name))
			}
			t.index[folded] = idx
		}
		if kind == KindModifier {
			t.mods[bitIndex(mod)] = Key{kind: KindModifier, code: idx}
		}
	}
	for i, r := range modifierRows {
		add(KindModifier, r, Mods(1)<<i)
	}
	for _, r := range namedRows {
		add(KindNamed, r, 0)
	}
	for _, r := range charRows {
		add(KindChar, row{name: r.char, scan: r.scan, vk: r.vk}, 0)
		t.entries[len(t.entries)-1].char = rowChar(r.char)
	}
	add(KindChar, plusRow, 0)
	t.entries[len(t.entries)-1].us.Shift = true
	t.entries[len(t.entries)-1].char = '+'
	for _, r := range charRows {
		var aliases []string
		if name := "Key" + r.char; r.code != name {
			aliases = []string{r.code}
		}
		add(KindPhysical, row{name: "Key" + r.char, scan: r.scan, vk: r.vk, aliases: aliases}, 0)
	}
	for _, r := range mediaRows {
		add(KindMedia, r, 0)
	}
	for _, r := range mouseRows {
		// No scan code and no VK: usMapping(0, 0) is the zero mapping.
		add(KindMouse, row{name: r.name, aliases: r.aliases}, 0)
		t.entries[len(t.entries)-1].mouse = r.mouse
	}
	return t
}

// rowChar returns the character a charRow name types: the name itself, a
// single ASCII character, with letters lower-cased because a character key
// means the unshifted key ("Z" is the key that types "z").
func rowChar(name string) rune {
	if len(name) != 1 || name[0] < '!' || name[0] > '~' {
		panic(fmt.Sprintf("keys: bad character key name %q", name))
	}
	return rune(foldASCII(name)[0])
}

// usMapping decodes a row scan code.
func usMapping(scan uint32, vk uint16) USMapping {
	switch {
	case scan>>8 == 0xE11D:
		return USMapping{Scan: uint8(scan), E1: true, VK: vk}
	case scan>>8 == 0xE0:
		return USMapping{Scan: uint8(scan), Ext: true, VK: vk}
	case scan <= 0x7F:
		return USMapping{Scan: uint8(scan), VK: vk}
	}
	panic(fmt.Sprintf("keys: bad table scan code %#x", scan))
}

// bitIndex returns the index of the single bit set in m.
func bitIndex(m Mods) int {
	for i := range numMods {
		if m == 1<<i {
			return i
		}
	}
	panic(fmt.Sprintf("keys: bad modifier bit %#x", uint16(m)))
}

// foldASCII lower-cases the ASCII letters of s. Names are ASCII, so
// non-ASCII input never matches.
func foldASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// Lookup returns the key a single token names: a canonical name or alias,
// matched case-insensitively, or a raw scan code such as "sc:0x2C". It
// accepts neither chords nor surrounding spaces; use Parse for those.
func Lookup(name string) (Key, bool) {
	if i, ok := lookupIndex(name); ok {
		return Key{kind: tbl.entries[i].kind, code: i}, true
	}
	if hasPrefixFold(name, scanPrefix) {
		k, msg := parseScan(name)
		return k, msg == ""
	}
	return Key{}, false
}

// lookupIndex finds the table entry of a name or alias without allocating.
func lookupIndex(name string) (uint16, bool) {
	if len(name) == 0 || len(name) > maxNameLen {
		return 0, false
	}
	var buf [maxNameLen]byte
	for i := range len(name) {
		c := name[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		buf[i] = c
	}
	i, ok := tbl.index[string(buf[:len(name)])]
	return i, ok
}

// Table returns every key of the table in listing order: modifiers, named
// keys, character keys, physical keys, media keys, then mouse keys. Raw scan codes are
// not listed.
func Table() []Key {
	out := make([]Key, len(tbl.entries))
	for i, e := range tbl.entries {
		out[i] = Key{kind: e.kind, code: uint16(i)}
	}
	return out
}
