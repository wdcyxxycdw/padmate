// Package kblayout holds dumps of Windows keyboard layouts: which virtual
// key each scan code gives, which characters it types, and which key types
// each printable ASCII character.
//
// "padmate-probe dump-layout" (ENG-06) writes one Fixture per layout into
// daemon/testdata/layouts. Dump, WithLoaded and the table readers
// VKToScan and Chars, which read a layout through the Win32 keyboard
// functions, are Windows-only; the layout resolver (output.Resolver,
// OUT-04) reads the foreground layout with the same two readers. Fixture,
// Load and Marshal build on every OS, so that tests of the key table and
// of the resolver run on Linux against the recorded layouts.
package kblayout

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Generator is the Fixture.Generator of the fixtures that
// "padmate-probe dump-layout" writes.
const Generator = "padmate-probe dump-layout"

// Fixture is the dump of one keyboard layout. Codes are written as hex
// strings such as "0x2C" (see Code).
type Fixture struct {
	// KLID is the keyboard layout identifier, such as "00000409".
	KLID string `json:"klid"`
	// HKL is the handle LoadKeyboardLayoutW returned for KLID, such as
	// "0x04090409".
	HKL string `json:"hkl"`
	// LayoutFile and LayoutText are the "Layout File" and "Layout Text"
	// values of the KLID under
	// HKLM\SYSTEM\CurrentControlSet\Control\Keyboard Layouts.
	LayoutFile string `json:"layoutFile"`
	LayoutText string `json:"layoutText"`
	// LayerDriver is the i8042prt "LayerDriver JPN" or "LayerDriver KOR"
	// value for Japanese and Korean layouts: the DLL whose tables the
	// layout really uses. KBDJPN.DLL, for one, delegates to kbd106.dll
	// (106/109-key JIS keyboards) or kbd101.dll (101-key keyboards), and
	// only kbd106.dll has the JIS keys.
	LayerDriver string `json:"layerDriver,omitempty"`
	// Generator, Version and Windows record what wrote the fixture: the
	// tool, the padmate version and the Windows version (major.minor.build).
	Generator string `json:"generator"`
	Version   string `json:"version"`
	Windows   string `json:"windows"`
	// Scancodes lists, for every scan code 0x01–0x7F without and with the
	// E0 prefix, the VK that MapVirtualKeyExW(MAPVK_VSC_TO_VK_EX) gives it
	// and what the key types. Codes without a VK are left out. The mapping
	// ignores NumLock: the numpad codes give the navigation VKs.
	Scancodes []ScanEntry `json:"scancodes"`
	// VKToScan lists every VK 0x01–0xFE that
	// MapVirtualKeyExW(MAPVK_VK_TO_VSC_EX) maps to a scan code.
	VKToScan []VKEntry `json:"vkToScan"`
	// Chars lists every printable ASCII character 0x20–0x7E with the key
	// VkKeyScanExW finds for it.
	Chars []CharEntry `json:"chars"`
}

// ScanEntry is one scan code of a layout.
type ScanEntry struct {
	Scan Code `json:"scan"`
	Ext  bool `json:"ext"`
	VK   Code `json:"vk"`
	// Base, Shift, AltGr and ShiftAltGr are what ToUnicodeEx gives the VK
	// with no modifier, Shift, Ctrl+Alt and Shift+Ctrl+Alt held; nil when
	// it gives nothing.
	Base       *Output `json:"base,omitempty"`
	Shift      *Output `json:"shift,omitempty"`
	AltGr      *Output `json:"altgr,omitempty"`
	ShiftAltGr *Output `json:"shiftAltgr,omitempty"`
}

// Output is what a key types in one modifier state.
type Output struct {
	// Text is the UTF-16 output of ToUnicodeEx as a string. For a dead key
	// it is the spacing form of the accent.
	Text string `json:"text"`
	// Dead reports a dead key: it types nothing itself and changes the
	// next character.
	Dead bool `json:"dead,omitempty"`
}

// VKEntry is the scan code of one VK.
type VKEntry struct {
	VK   Code `json:"vk"`
	Scan Code `json:"scan"`
	Ext  bool `json:"ext"`
	// E1 reports the E1 prefix, which only VK_PAUSE has (E1 1D 45).
	E1 bool `json:"e1,omitempty"`
}

// CharEntry is the key that types one ASCII character.
type CharEntry struct {
	Char string `json:"char"`
	Code Code   `json:"code"`
	// Absent reports that no key of the layout types the character; the
	// other fields are then zero.
	Absent bool `json:"absent,omitempty"`
	// VK and the modifier bits are the result of VkKeyScanExW.
	VK      Code `json:"vk,omitempty"`
	Shift   bool `json:"shift,omitempty"`
	Ctrl    bool `json:"ctrl,omitempty"`
	Alt     bool `json:"alt,omitempty"`
	Hankaku bool `json:"hankaku,omitempty"`
	// Scan and Ext are the scan code of VK, from
	// MapVirtualKeyExW(MAPVK_VK_TO_VSC_EX).
	Scan Code `json:"scan,omitempty"`
	Ext  bool `json:"ext,omitempty"`
}

// Code is a scan code, VK or character code. It is written in JSON as a
// hex string with at least two upper-case digits, such as "0x2C".
type Code uint16

// String formats c as "0x2C".
func (c Code) String() string {
	return fmt.Sprintf("0x%02X", uint16(c))
}

// MarshalText implements encoding.TextMarshaler.
func (c Code) MarshalText() ([]byte, error) {
	return []byte(c.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler. It accepts only the
// form MarshalText writes.
func (c *Code) UnmarshalText(b []byte) error {
	s := string(b)
	digits, ok := strings.CutPrefix(s, "0x")
	if !ok || len(digits) < 2 || strings.ToUpper(digits) != digits {
		return fmt.Errorf("kblayout: code %q is not a hex string like \"0x2C\"", s)
	}
	v, err := strconv.ParseUint(digits, 16, 16)
	if err != nil {
		return fmt.Errorf("kblayout: code %q: %w", s, err)
	}
	*c = Code(v)
	return nil
}

// FileName returns the name of the fixture file of klid: "00000409.json".
func FileName(klid string) string {
	return klid + ".json"
}

// Load reads and validates the fixture at path.
func Load(path string) (*Fixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := Unmarshal(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return f, nil
}

// Unmarshal decodes and validates a fixture. Unknown fields are errors.
func Unmarshal(data []byte) (*Fixture, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f Fixture
	if err := dec.Decode(&f); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("kblayout: data after the fixture object")
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	return &f, nil
}

// Marshal encodes f the way fixture files are written: fields in struct
// order, two-space indentation and a trailing newline, so that a
// regenerated fixture only differs where the layout does.
func (f *Fixture) Marshal() ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Validate checks that f is well-formed: a KLID of eight hex digits, the
// recorded metadata, and code lists that are in range, sorted and free of
// duplicates.
func (f *Fixture) Validate() error {
	if !ValidKLID(f.KLID) {
		return fmt.Errorf("kblayout: bad KLID %q", f.KLID)
	}
	for _, field := range []struct{ name, value string }{
		{"hkl", f.HKL}, {"layoutFile", f.LayoutFile}, {"generator", f.Generator}, {"windows", f.Windows},
	} {
		if field.value == "" {
			return fmt.Errorf("kblayout: %s: %s is empty", f.KLID, field.name)
		}
	}
	if len(f.Scancodes) == 0 || len(f.VKToScan) == 0 || len(f.Chars) == 0 {
		return fmt.Errorf("kblayout: %s: empty code lists", f.KLID)
	}
	prev := -1
	for _, e := range f.Scancodes {
		if e.Scan < 0x01 || e.Scan > 0x7F || e.VK < 0x01 || e.VK > 0xFE {
			return fmt.Errorf("kblayout: %s: scancodes: sc=%v vk=%v out of range", f.KLID, e.Scan, e.VK)
		}
		key := scanKey(uint8(e.Scan), e.Ext)
		if key <= prev {
			return fmt.Errorf("kblayout: %s: scancodes: sc=%v ext=%v out of order", f.KLID, e.Scan, e.Ext)
		}
		prev = key
		for _, o := range []*Output{e.Base, e.Shift, e.AltGr, e.ShiftAltGr} {
			if o != nil && o.Text == "" {
				return fmt.Errorf("kblayout: %s: scancodes: sc=%v has an empty output", f.KLID, e.Scan)
			}
		}
	}
	prev = -1
	for _, e := range f.VKToScan {
		if e.VK < 0x01 || e.VK > 0xFE || e.Scan > 0xFF || e.Ext && e.E1 {
			return fmt.Errorf("kblayout: %s: vkToScan: vk=%v sc=%v invalid", f.KLID, e.VK, e.Scan)
		}
		if int(e.VK) <= prev {
			return fmt.Errorf("kblayout: %s: vkToScan: vk=%v out of order", f.KLID, e.VK)
		}
		prev = int(e.VK)
	}
	if len(f.Chars) != 0x7F-0x20 {
		return fmt.Errorf("kblayout: %s: chars has %d entries, want %d", f.KLID, len(f.Chars), 0x7F-0x20)
	}
	for i, e := range f.Chars {
		c := 0x20 + i
		switch {
		case int(e.Code) != c || e.Char != string(rune(c)):
			return fmt.Errorf("kblayout: %s: chars[%d] is %q (%v), want %q", f.KLID, i, e.Char, e.Code, string(rune(c)))
		case e.Absent && (e.VK != 0 || e.Scan != 0 || e.Shift || e.Ctrl || e.Alt || e.Hankaku || e.Ext):
			return fmt.Errorf("kblayout: %s: chars %q is absent but has a key", f.KLID, e.Char)
		case !e.Absent && (e.VK < 0x01 || e.VK > 0xFE):
			return fmt.Errorf("kblayout: %s: chars %q: vk=%v out of range", f.KLID, e.Char, e.VK)
		}
	}
	return nil
}

// scanKey orders scan codes: plain codes first, then E0 codes.
func scanKey(scan uint8, ext bool) int {
	if ext {
		return 0x100 | int(scan)
	}
	return int(scan)
}

// Scan returns the entry of a scan code, if the layout maps it to a VK.
func (f *Fixture) Scan(scan uint8, ext bool) (ScanEntry, bool) {
	for _, e := range f.Scancodes {
		if uint8(e.Scan) == scan && e.Ext == ext {
			return e, true
		}
	}
	return ScanEntry{}, false
}

// VK returns the scan code entry of vk, if the layout has one.
func (f *Fixture) VK(vk uint8) (VKEntry, bool) {
	for _, e := range f.VKToScan {
		if e.VK == Code(vk) {
			return e, true
		}
	}
	return VKEntry{}, false
}

// Char returns the entry of a printable ASCII character, and false for
// any other character. An entry can be Absent.
func (f *Fixture) Char(c rune) (CharEntry, bool) {
	i := int(c) - 0x20
	if i < 0 || i >= len(f.Chars) {
		return CharEntry{}, false
	}
	return f.Chars[i], true
}

// ValidKLID reports whether s is a KLID: eight hex digits, such as
// "00000409" or "0000040C".
func ValidKLID(s string) bool {
	if len(s) != 8 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'A' || c > 'F') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
