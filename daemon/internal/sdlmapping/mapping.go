// Package sdlmapping parses, checks and formats SDL gamepad mapping strings
// (IN-18, ADR-0072): "GUID,name,key:value,key:value,...".
//
// It is pure: no SDL, no Windows, no clock. The format is SDL 3.4.16's
// (src/joystick/SDL_gamepad.c): the name cannot contain a comma, the fields
// are "key:value" pairs separated by commas, and an element that is
// malformed is silently ignored by SDL. This package is stricter: it rejects
// what SDL would ignore, so that a mapping the user saved does what the
// wizard showed.
package sdlmapping

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// PlatformWindows is the platform field padmate writes: SDL's file loader
// (SDL_AddGamepadMappingsFromFile) only takes lines that contain
// "platform:Windows,".
const PlatformWindows = "platform:Windows"

// Limits that SDL itself applies (SDL_gamepad.c): a longer key or value
// aborts the parse of the whole string.
const (
	maxKey   = 19
	maxValue = 127
	// MaxLen bounds a mapping string that padmate accepts.
	MaxLen = 2048
)

// Field is one "key:value" pair of a mapping.
type Field struct{ Key, Value string }

// Mapping is a parsed mapping string.
type Mapping struct {
	// GUID is 32 lowercase hex digits.
	GUID string
	// Name is the gamepad name field; "*" keeps the device's own name.
	Name string
	// Fields are the pairs after the name, in order.
	Fields []Field
}

// outputButtons and outputAxes are the SDL gamepad element names a mapping
// binds, in the order of SDL's documentation. Axis outputs may carry a "+"
// or "-" prefix.
var outputButtons = []string{
	"a", "b", "x", "y", "back", "guide", "start", "leftstick", "rightstick",
	"leftshoulder", "rightshoulder", "dpup", "dpdown", "dpleft", "dpright",
	"misc1", "paddle1", "paddle2", "paddle3", "paddle4", "touchpad",
	"misc2", "misc3", "misc4", "misc5", "misc6",
}

var outputAxes = []string{"leftx", "lefty", "rightx", "righty", "lefttrigger", "righttrigger"}

// metaKeys are the fields that are no binding.
var metaKeys = []string{"crc", "platform", "type", "face", "hint"}

// buttonNames maps an output key to the control name of input.ButtonName.
var buttonNames = map[string]string{
	"a": "south", "b": "east", "x": "west", "y": "north",
	"back": "back", "guide": "guide", "start": "start",
	"leftstick": "leftstick", "rightstick": "rightstick",
	"leftshoulder": "leftshoulder", "rightshoulder": "rightshoulder",
	"dpup": "dpad.up", "dpdown": "dpad.down", "dpleft": "dpad.left", "dpright": "dpad.right",
	"misc1": "misc1", "paddle1": "paddle1", "paddle2": "paddle2", "paddle3": "paddle3",
	"paddle4": "paddle4", "touchpad": "touchpad",
	"misc2": "misc2", "misc3": "misc3", "misc4": "misc4", "misc5": "misc5", "misc6": "misc6",
}

// ButtonKey returns the mapping key of the gamepad button named name in
// input.ButtonName terms ("south" is "a"), and false for a name it does not
// know.
func ButtonKey(name string) (string, bool) {
	for key, n := range buttonNames {
		if n == name {
			return key, true
		}
	}
	return "", false
}

// IsOutputKey reports whether key (with an optional "+" or "-" prefix for an
// axis) names a gamepad element.
func IsOutputKey(key string) bool {
	_, ok := outputBase(key)
	return ok
}

// outputBase returns key without the "+"/"-" prefix of an axis output.
func outputBase(key string) (string, bool) {
	if slices.Contains(outputButtons, key) {
		return key, true
	}
	base := key
	if len(key) > 0 && (key[0] == '+' || key[0] == '-') {
		base = key[1:]
	}
	return base, slices.Contains(outputAxes, base)
}

// Parse parses and checks a mapping string. The GUID must be 32 hex digits
// (the special names "xinput" and "default" are refused: padmate maps one
// device kind at a time). Every field is "key:value" with a known key or one
// of crc, platform, type, face and hint, and every binding value is one of
// bN, aN, +aN, -aN (each with an optional "~" for an inverted axis) or hN.M
// with M in 1..15. A key may appear once. Parse does not require a
// binding: Validate does.
func Parse(s string) (Mapping, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Mapping{}, errors.New("empty mapping")
	}
	if len(s) > MaxLen {
		return Mapping{}, fmt.Errorf("mapping is longer than %d bytes", MaxLen)
	}
	if strings.ContainsAny(s, "\x00\r\n") {
		return Mapping{}, errors.New("mapping contains a control character")
	}
	guid, rest, ok := strings.Cut(s, ",")
	if !ok {
		return Mapping{}, errors.New("mapping needs GUID,name,fields")
	}
	name, rest, ok := strings.Cut(rest, ",")
	if !ok {
		return Mapping{}, errors.New("mapping needs GUID,name,fields")
	}
	m := Mapping{GUID: strings.ToLower(strings.TrimSpace(guid)), Name: strings.TrimSpace(name)}
	if !IsGUID(m.GUID) {
		return Mapping{}, fmt.Errorf("the GUID %q is not 32 hex digits", guid)
	}
	if m.Name == "" {
		return Mapping{}, errors.New("the name field is empty; use * to keep the device's name")
	}
	seen := map[string]bool{}
	for _, f := range strings.Split(rest, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		key, value, ok := strings.Cut(f, ":")
		if !ok || key == "" || value == "" {
			return Mapping{}, fmt.Errorf("the field %q is not key:value", f)
		}
		if len(key) > maxKey || len(value) > maxValue {
			return Mapping{}, fmt.Errorf("the field %q is too long", f)
		}
		if seen[key] {
			return Mapping{}, fmt.Errorf("the key %q appears twice", key)
		}
		seen[key] = true
		if err := checkField(key, value); err != nil {
			return Mapping{}, err
		}
		m.Fields = append(m.Fields, Field{key, value})
	}
	return m, nil
}

func checkField(key, value string) error {
	if slices.Contains(metaKeys, key) {
		return nil
	}
	if !IsOutputKey(key) {
		return fmt.Errorf("%q is not a gamepad element", key)
	}
	if _, err := ParseInput(value); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}

// IsGUID reports whether s is 32 lowercase hex digits.
func IsGUID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// InputKind is the kind of the joystick element a binding reads.
type InputKind uint8

// The kinds of binding input.
const (
	InputButton InputKind = iota + 1 // bN
	InputAxis                        // aN, +aN, -aN, with an optional ~
	InputHat                         // hN.M
)

// Half selects the part of an axis that a binding reads.
type Half uint8

// The parts of an axis.
const (
	Full     Half = iota // aN: -32768..32767
	Positive             // +aN: 0..32767
	Negative             // -aN: 0..-32768
)

// Input is the parsed value of a binding.
type Input struct {
	Kind  InputKind
	Index int  // button, axis or hat index
	Half  Half // axis only
	// Inverted is the "~" of an axis.
	Inverted bool
	// Mask is the hat direction mask for a hat: up 1, right 2, down 4, left 8.
	Mask int
}

// Limits of joystick indices that padmate accepts: SDL's DirectInput driver
// has at most 128 buttons, 8 axes and 4 hats; other drivers may have more,
// but nothing realistic exceeds these.
const (
	maxButton = 255
	maxAxis   = 63
	maxHat    = 15
)

// ParseInput parses the value of a binding: bN, aN, +aN, -aN (each with an
// optional trailing "~") or hN.M.
func ParseInput(v string) (Input, error) {
	bad := func() (Input, error) { return Input{}, fmt.Errorf("%q is not bN, aN, +aN, -aN or hN.M", v) }
	if v == "" {
		return bad()
	}
	in := Input{}
	rest := v
	switch rest[0] {
	case '+':
		in.Half, rest = Positive, rest[1:]
	case '-':
		in.Half, rest = Negative, rest[1:]
	}
	if rest == "" {
		return bad()
	}
	switch rest[0] {
	case 'b':
		if in.Half != Full {
			return bad()
		}
		n, ok := number(rest[1:], maxButton)
		if !ok {
			return bad()
		}
		in.Kind, in.Index = InputButton, n
	case 'a':
		body := rest[1:]
		if strings.HasSuffix(body, "~") {
			in.Inverted, body = true, body[:len(body)-1]
		}
		n, ok := number(body, maxAxis)
		if !ok {
			return bad()
		}
		in.Kind, in.Index = InputAxis, n
	case 'h':
		if in.Half != Full {
			return bad()
		}
		h, m, ok := strings.Cut(rest[1:], ".")
		if !ok {
			return bad()
		}
		hat, ok1 := number(h, maxHat)
		mask, ok2 := number(m, 15)
		if !ok1 || !ok2 || mask < 1 {
			return bad()
		}
		in.Kind, in.Index, in.Mask = InputHat, hat, mask
	default:
		return bad()
	}
	return in, nil
}

// number parses a canonical decimal number up to max: no sign, no leading
// zeros, no space.
func number(s string, limit int) (int, bool) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil || n > limit {
		return 0, false
	}
	return n, true
}

// String returns the text of the input as a binding value.
func (in Input) String() string {
	switch in.Kind {
	case InputButton:
		return "b" + strconv.Itoa(in.Index)
	case InputHat:
		return "h" + strconv.Itoa(in.Index) + "." + strconv.Itoa(in.Mask)
	case InputAxis:
		s := "a" + strconv.Itoa(in.Index)
		switch in.Half {
		case Positive:
			s = "+" + s
		case Negative:
			s = "-" + s
		}
		if in.Inverted {
			s += "~"
		}
		return s
	}
	return ""
}

// Validate checks that m has a GUID and a name that SDL accepts and binds at
// least one element. It returns the first problem.
func (m Mapping) Validate() error {
	if !IsGUID(m.GUID) {
		return fmt.Errorf("the GUID %q is not 32 hex digits", m.GUID)
	}
	if m.Name == "" || strings.Contains(m.Name, ",") {
		return errors.New("the name must be non-empty and have no comma")
	}
	n := 0
	for _, f := range m.Fields {
		if IsOutputKey(f.Key) {
			n++
			if _, err := ParseInput(f.Value); err != nil {
				return fmt.Errorf("%s: %w", f.Key, err)
			}
		} else if !slices.Contains(metaKeys, f.Key) {
			return fmt.Errorf("%q is not a gamepad element", f.Key)
		}
	}
	if n == 0 {
		return errors.New("the mapping binds no element")
	}
	return nil
}

// Bindings returns the element bindings of m (the fields that are no meta
// field), keyed by output key including any "+"/"-" prefix.
func (m Mapping) Bindings() map[string]string {
	out := map[string]string{}
	for _, f := range m.Fields {
		if IsOutputKey(f.Key) {
			out[f.Key] = f.Value
		}
	}
	return out
}

// Get returns the value of the field key and whether it is there.
func (m Mapping) Get(key string) (string, bool) {
	for _, f := range m.Fields {
		if f.Key == key {
			return f.Value, true
		}
	}
	return "", false
}

// Binds reports whether m binds the output element key, such as "back".
// For an axis output it is true when the axis or either of its halves is
// bound.
func (m Mapping) Binds(key string) bool {
	for _, f := range m.Fields {
		if f.Key == key || f.Key == "+"+key || f.Key == "-"+key {
			return true
		}
	}
	return false
}

// BindsButton reports whether m binds the gamepad button named name as
// input.ButtonName names it ("south", "back", ...).
func (m Mapping) BindsButton(name string) bool {
	key, ok := ButtonKey(name)
	return ok && m.Binds(key)
}

// Equivalent reports whether a and b bind the same elements to the same
// inputs, ignoring the name, the GUID, the order and the meta fields: how a
// mapping that SDL stores and reads back (SDL_GetGamepadMappingForID) is
// compared with the one that was added.
func Equivalent(a, b Mapping) bool {
	x, y := a.Bindings(), b.Bindings()
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		if y[k] != v {
			return false
		}
	}
	return true
}

// String returns the canonical text of m: GUID, name and the fields in
// order, without a trailing comma.
func (m Mapping) String() string {
	var b strings.Builder
	b.WriteString(m.GUID)
	b.WriteByte(',')
	b.WriteString(m.Name)
	b.WriteByte(',') // SDL needs two commas, also for a mapping without fields
	for i, f := range m.Fields {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(f.Key)
		b.WriteByte(':')
		b.WriteString(f.Value)
	}
	return b.String()
}

// DBLine returns m as a line of an SDL mapping database file, which ends
// with a comma after the platform field: SDL_AddGamepadMappingsFromFile
// only takes lines that contain "platform:Windows,".
func (m Mapping) DBLine() string {
	c := m
	c.Fields = slices.DeleteFunc(slices.Clone(m.Fields), func(f Field) bool { return f.Key == "platform" })
	c.Fields = append(c.Fields, Field{"platform", "Windows"})
	return c.String() + ","
}

// Normalize returns the mapping that padmate saves and applies for the
// device with GUID guid: the name is "*" (the device keeps its own name,
// which slot matching uses), "type" is dropped (it would change the device
// family that the emergency combo is chosen by), "face" and "hint" are
// dropped, and the platform is Windows. The notes say what was changed.
// The GUID of m must be guid.
func Normalize(m Mapping, guid string) (Mapping, []string, error) {
	var notes []string
	if m.GUID != strings.ToLower(guid) {
		return Mapping{}, nil, fmt.Errorf("the mapping is for GUID %s, not for the device %s", m.GUID, guid)
	}
	out := Mapping{GUID: m.GUID, Name: "*"}
	if m.Name != "*" {
		notes = append(notes, "the name is replaced by *, so the device keeps its own name")
	}
	for _, f := range m.Fields {
		switch f.Key {
		case "type", "face", "hint":
			notes = append(notes, "the "+f.Key+" field is dropped")
			continue
		case "platform":
			if f.Value != "Windows" {
				return Mapping{}, nil, fmt.Errorf("platform:%s: only platform:Windows can be saved", f.Value)
			}
			continue
		}
		out.Fields = append(out.Fields, f)
	}
	out.Fields = append(out.Fields, Field{"platform", "Windows"})
	if err := out.Validate(); err != nil {
		return Mapping{}, nil, err
	}
	return out, notes, nil
}

// LenientFields returns the element bindings of text, a mapping string as SDL
// reads it back from a device (SDL_GetGamepadMappingForID), without checking
// the values: it reads what is after the second comma and keeps the fields
// whose key names a gamepad element. It is for comparing with Equivalent,
// not for anything that SDL is to read again.
func LenientFields(text string) []Field {
	_, rest, ok := strings.Cut(text, ",")
	if !ok {
		return nil
	}
	_, rest, ok = strings.Cut(rest, ",")
	if !ok {
		return nil
	}
	var out []Field
	for _, f := range strings.Split(rest, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(f), ":")
		if ok && value != "" && IsOutputKey(key) {
			out = append(out, Field{key, value})
		}
	}
	return out
}
