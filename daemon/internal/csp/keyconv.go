package csp

import (
	"strconv"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// CSP modifier bits of the khc "modifier" column.
const (
	ModAlt   = 1
	ModShift = 2
	ModCtrl  = 4
	// modMask is every modifier bit this package understands.
	modMask = ModAlt | ModShift | ModCtrl
)

// KeyStatus is the outcome of converting a CSP key name to a chord or back.
type KeyStatus uint8

// Conversion outcomes.
const (
	// KeyUnknown: the name is not a CSP key name at all (or the chord has no CSP
	// spelling that is known).
	KeyUnknown KeyStatus = iota
	// KeyOK: converted.
	KeyOK
	// KeyUnconfirmed: a real CSP key whose physical key on US and Chinese
	// keyboards is not confirmed yet (the symbol keys). Not converted.
	KeyUnconfirmed
	// KeyUnsupported: a real CSP key (or modifier combination) with no padmate
	// counterpart: HELP, NUMCLEAR, devices, modifier bits beyond Alt/Shift/Ctrl,
	// a chord with Win, ...
	KeyUnsupported
	// KeyNone: the literal "NULL", which means no shortcut.
	KeyNone
)

// String returns a short name of s.
func (s KeyStatus) String() string {
	switch s {
	case KeyOK:
		return "ok"
	case KeyUnconfirmed:
		return "unconfirmed"
	case KeyUnsupported:
		return "unsupported"
	case KeyNone:
		return "none"
	}
	return "unknown"
}

// symbolKeys lists the symbol key names of CSP's table (JIS keyboard names).
// The value is the padmate KeySpec of the physical key; "" means the mapping
// is not confirmed yet. Fill the values in here, and only here, after the
// manual test on a real keyboard (ADR-0019).
var symbolKeys = map[string]string{
	"-":  "",
	"^":  "",
	"@":  "",
	"[":  "",
	"]":  "",
	";":  "",
	":":  "",
	",":  "",
	".":  "",
	"/":  "",
	"\\": "",
}

// unsupportedKeys are the CSP key names that have no padmate counterpart.
var unsupportedKeys = buildUnsupported()

func buildUnsupported() map[string]bool {
	m := map[string]bool{
		"HELP": true, "CLEAR": true, "NUMCLEAR": true, "NUM=": true, "NUM,": true,
		"YEN": true, "S1": true, "S2": true, "E1": true, "E2": true,
	}
	for i := 1; i <= 30; i++ {
		m["T"+strconv.Itoa(i)] = true
	}
	for i := 1; i <= 8; i++ {
		m["GAMEPADBUTTON"+strconv.Itoa(i)] = true
	}
	return m
}

// supportedKeys maps a CSP key name to the padmate key spec of the same key.
var supportedKeys = buildSupported()

// reverseKeys maps a padmate key back to its CSP name.
var reverseKeys = buildReverse()

func buildSupported() map[string]keys.Key {
	m := map[string]keys.Key{}
	add := func(name, spec string) {
		m[name] = keys.MustParse(spec).Key()
	}
	for c := 'A'; c <= 'Z'; c++ {
		add(string(c), string(c))
	}
	for c := '0'; c <= '9'; c++ {
		add(string(c), string(c))
		add("NUM"+string(c), "Num"+string(c))
	}
	for i := 1; i <= 24; i++ {
		n := strconv.Itoa(i)
		add("F"+n, "F"+n)
	}
	for name, spec := range map[string]string{
		"TAB": "Tab", "SPACE": "Space", "BACKSPACE": "Backspace", "DELETE": "Delete",
		"ESC": "Esc", "HOME": "Home", "END": "End", "PAGEUP": "PageUp",
		"PAGEDOWN": "PageDown", "LEFT": "Left", "RIGHT": "Right", "UP": "Up",
		"DOWN": "Down", "INSERT": "Insert",
		"NUM+": "NumAdd", "NUM-": "NumSubtract", "NUM*": "NumMultiply",
		"NUM/": "NumDivide", "NUM.": "NumDecimal",
	} {
		add(name, spec)
	}
	for name, spec := range symbolKeys {
		if spec != "" {
			add(name, spec)
		}
	}
	return m
}

func buildReverse() map[keys.Key]string {
	m := make(map[keys.Key]string, len(supportedKeys))
	for name, k := range supportedKeys {
		m[k] = name
	}
	return m
}

// KeyNameStatus classifies a CSP key name without converting it. The name is
// matched case-insensitively. "NULL" is KeyNone.
func KeyNameStatus(name string) KeyStatus {
	name = strings.ToUpper(name)
	switch {
	case name == "NULL":
		return KeyNone
	case supportedKeys[name].IsValid():
		return KeyOK
	}
	if spec, ok := symbolKeys[name]; ok && spec == "" {
		return KeyUnconfirmed
	}
	if unsupportedKeys[name] {
		return KeyUnsupported
	}
	return KeyUnknown
}

// ParseCSPKey converts a CSP key name and modifier bitmask (1 Alt, 2 Shift,
// 4 Ctrl) to a padmate chord with generic (not sided) modifiers. The chord is
// valid only when the status is KeyOK. Modifier bits outside 1|2|4 make the
// result KeyUnsupported when the name is otherwise a known key.
func ParseCSPKey(name string, modifier int) (keys.Chord, KeyStatus) {
	st := KeyNameStatus(name)
	if st != KeyOK {
		return keys.Chord{}, st
	}
	if modifier < 0 || modifier&^modMask != 0 {
		return keys.Chord{}, KeyUnsupported
	}
	k := supportedKeys[strings.ToUpper(name)]
	spec := k.String()
	if modifier&ModCtrl != 0 {
		spec = "Ctrl+" + spec
	}
	if modifier&ModShift != 0 {
		spec = "Shift+" + spec
	}
	if modifier&ModAlt != 0 {
		spec = "Alt+" + spec
	}
	c, err := keys.Parse(spec)
	if err != nil {
		return keys.Chord{}, KeyUnknown
	}
	return c, KeyOK
}

// FormatCSPKey converts a padmate chord to a CSP key name and modifier bitmask.
// Sided and generic Ctrl, Shift and Alt are all accepted. The result is
// KeyUnsupported for a chord with Win, a chord made only of modifiers, or a key
// CSP does not have.
func FormatCSPKey(c keys.Chord) (name string, modifier int, status KeyStatus) {
	if !c.IsValid() {
		return "", 0, KeyUnknown
	}
	m := c.Mods()
	if m&(keys.ModWin|keys.ModLWin|keys.ModRWin) != 0 {
		return "", 0, KeyUnsupported
	}
	if m&(keys.ModCtrl|keys.ModLCtrl|keys.ModRCtrl) != 0 {
		modifier |= ModCtrl
	}
	if m&(keys.ModShift|keys.ModLShift|keys.ModRShift) != 0 {
		modifier |= ModShift
	}
	if m&(keys.ModAlt|keys.ModLAlt|keys.ModRAlt) != 0 {
		modifier |= ModAlt
	}
	n, ok := reverseKeys[c.Key()]
	if !ok {
		return "", 0, KeyUnsupported
	}
	return n, modifier, KeyOK
}

// ToolKeyChord converts a tool shortcut code (1-26 for A-Z) to a chord. It
// returns false for 0 (no shortcut) and for every code that is not known.
func ToolKeyChord(code int) (keys.Chord, bool) {
	if code < 1 || code > 26 {
		return keys.Chord{}, false
	}
	c, st := ParseCSPKey(string(rune('A'+code-1)), 0)
	return c, st == KeyOK
}

// ToolKeyCode converts a chord to a tool shortcut code. Tool shortcuts have no
// modifiers and only the letters A-Z, so it returns false for anything else.
func ToolKeyCode(c keys.Chord) (int, bool) {
	name, mod, st := FormatCSPKey(c)
	if st != KeyOK || mod != 0 || len(name) != 1 || name[0] < 'A' || name[0] > 'Z' {
		return 0, false
	}
	return int(name[0]-'A') + 1, true
}
