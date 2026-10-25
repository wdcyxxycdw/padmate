package kblayout

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// fixtureDir holds the fixtures "padmate-probe dump-layout" wrote
// ("task layouts" regenerates them).
const fixtureDir = "../../testdata/layouts"

const (
	klidUS = "00000409"
	klidFR = "0000040C"
	klidJP = "00000411"
)

func loadFixture(t *testing.T, klid string) *Fixture {
	t.Helper()
	f, err := Load(filepath.Join(fixtureDir, FileName(klid)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return f
}

func TestFixturesWellFormed(t *testing.T) {
	for _, tc := range []struct{ klid, layoutFile string }{
		{klidUS, "KBDUS.DLL"},
		{klidFR, "KBDFR.DLL"},
		{klidJP, "KBDJPN.DLL"},
	} {
		t.Run(tc.klid, func(t *testing.T) {
			path := filepath.Join(fixtureDir, FileName(tc.klid))
			f := loadFixture(t, tc.klid)
			if f.KLID != tc.klid {
				t.Errorf("klid = %q, want %q", f.KLID, tc.klid)
			}
			if !strings.EqualFold(f.LayoutFile, tc.layoutFile) {
				t.Errorf("layoutFile = %q, want %q", f.LayoutFile, tc.layoutFile)
			}
			if f.Generator != Generator {
				t.Errorf("generator = %q, want %q", f.Generator, Generator)
			}
			if f.Version == "" {
				t.Error("version is empty")
			}
			// The low word of the HKL is the language of the KLID.
			hkl, err := strconv.ParseUint(strings.TrimPrefix(f.HKL, "0x"), 16, 64)
			if err != nil || !strings.HasPrefix(f.HKL, "0x") {
				t.Fatalf("hkl %q is not a hex handle", f.HKL)
			}
			klid, _ := strconv.ParseUint(tc.klid, 16, 32)
			if uint16(hkl) != uint16(klid) {
				t.Errorf("hkl %s: language 0x%04X, want 0x%04X", f.HKL, uint16(hkl), uint16(klid))
			}
			// Every layout has the letters, digits and Esc on their usual
			// scan codes, whatever VK it gives them.
			for _, sc := range []uint8{0x01, 0x02, 0x10, 0x1E, 0x2C, 0x39} {
				if _, ok := f.Scan(sc, false); !ok {
					t.Errorf("scancodes has no entry for sc=0x%02X", sc)
				}
			}

			// The file is exactly what Marshal writes: stable key order,
			// two-space indentation and a trailing newline.
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got, err := f.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("Marshal(Load(%s)) differs from the file; regenerate it with task layouts", path)
			}
			if !bytes.HasSuffix(want, []byte("}\n")) || bytes.Contains(want, []byte("\r")) {
				t.Errorf("%s does not end in a single newline or has CR characters", path)
			}
		})
	}
}

// TestKeyTableVKToScan checks the scan code of every key of the table
// against what the US layout maps its VK to.
func TestKeyTableVKToScan(t *testing.T) {
	us := loadFixture(t, klidUS)
	// navigation keys: MAPVK_VK_TO_VSC_EX maps VK_HOME and the other
	// navigation VKs to their numpad scan code without the E0 prefix. The
	// table sends the dedicated key (E0 47 and so on), which gives the same
	// VK (the scancodes check below), and must: the numpad codes follow
	// NumLock.
	navigation := map[string]bool{
		"Insert": true, "Delete": true, "Home": true, "End": true, "PageUp": true,
		"PageDown": true, "Up": true, "Down": true, "Left": true, "Right": true,
	}
	for _, k := range keys.Table() {
		m := k.US()
		if m.VK == 0 {
			continue
		}
		name := k.String()
		got, ok := us.VK(uint8(m.VK))
		switch {
		case isJISKey(name):
			// JIS keys: their VKs are those of kbd106.dll, and the US layout
			// either lacks them (Convert, NonConvert, KanaMode) or gives them
			// to another key (IntlRo's VK_OEM_102 is IntlBackslash, IntlYen's
			// VK_OEM_5 is Backslash). TestJISKeys checks them.
			continue
		case navigation[name]:
			if !ok || got.Scan != Code(m.Scan) || got.Ext || got.E1 || !m.Ext {
				t.Errorf("%s: vkToScan[%s] = %+v, want the numpad code 0x%02X without E0", name, Code(m.VK), got, m.Scan)
			}
			continue
		case name == "PrintScreen":
			// VK_SNAPSHOT maps to 0x54, the code the keyboard sends for
			// Alt+PrintScreen (SysRq). The table sends the make code E0 37,
			// which gives VK_SNAPSHOT too (checked below).
			if !ok || got.Scan != 0x54 || got.Ext {
				t.Errorf("%s: vkToScan[%s] = %+v, want 0x54", name, Code(m.VK), got)
			}
			continue
		case name == "Pause":
			// MAPVK_VK_TO_VSC_EX gives 0xE11D, the first two bytes of
			// E1 1D 45; the table keeps the last byte. Pause is sent by VK.
			if !ok || !got.E1 || got.Scan != 0x1D || !m.E1 || m.Scan != 0x45 {
				t.Errorf("%s: vkToScan[%s] = %+v, want E1 1D", name, Code(m.VK), got)
			}
			continue
		case name == "NumEnter":
			// VK_RETURN is shared with Enter, and maps to Enter's 0x1C.
			// The scancodes check below finds VK_RETURN on E0 1C.
			if !ok || got.Scan != 0x1C || got.Ext {
				t.Errorf("%s: vkToScan[%s] = %+v, want Enter's 0x1C", name, Code(m.VK), got)
			}
			continue
		}
		if !ok {
			t.Errorf("%s: the US layout has no scan code for VK %s", name, Code(m.VK))
			continue
		}
		if got.Scan != Code(m.Scan) || got.Ext != m.Ext || got.E1 != m.E1 {
			t.Errorf("%s: vkToScan[%s] = %v ext=%v e1=%v, want %v ext=%v e1=%v",
				name, Code(m.VK), got.Scan, got.Ext, got.E1, Code(m.Scan), m.Ext, m.E1)
		}
	}
}

// TestKeyTableScanToVK checks the VK of every key of the table against the
// VK the US layout gives its scan code.
func TestKeyTableScanToVK(t *testing.T) {
	us := loadFixture(t, klidUS)
	// numpad digits and NumDecimal: MAPVK_VSC_TO_VK_EX ignores NumLock and
	// gives their scan codes the navigation VKs. That is why output sends
	// them by VK: by scan code they would follow the NumLock state.
	numpadNav := map[string]Code{
		"Num0": 0x2D, "Num1": 0x23, "Num2": 0x28, "Num3": 0x22, "Num4": 0x25, "Num5": 0x0C,
		"Num6": 0x27, "Num7": 0x24, "Num8": 0x26, "Num9": 0x21, "NumDecimal": 0x2E,
	}
	for _, k := range keys.Table() {
		if k.Kind() == keys.KindMouse {
			continue // no scan code, no VK: a mouse key is no keyboard key
		}
		m := k.US()
		name := k.String()
		switch {
		case isJISKey(name):
			continue // TestJISKeys
		case m.E1:
			continue // Pause: MapVirtualKeyExW has no E1 input; checked above
		}
		got, ok := us.Scan(m.Scan, m.Ext)
		if nav, isNum := numpadNav[name]; isNum {
			if !ok || got.VK != nav {
				t.Errorf("%s: scancodes[%s] = %+v, want the navigation VK %v", name, m.ScanString(), got, nav)
			}
			continue
		}
		if !ok {
			t.Errorf("%s: the US layout has no VK for %s", name, m.ScanString())
			continue
		}
		if got.VK != Code(m.VK) {
			t.Errorf("%s: scancodes[%s].vk = %v, want %v", name, m.ScanString(), got.VK, Code(m.VK))
		}
	}
}

// TestCharKeys checks that each character key is the key the US layout
// types its character with, Shift included.
func TestCharKeys(t *testing.T) {
	us := loadFixture(t, klidUS)
	n := 0
	for _, k := range keys.Table() {
		c, ok := k.Char()
		if !ok {
			continue
		}
		n++
		m := k.US()
		e, ok := us.Char(c)
		if !ok || e.Absent {
			t.Errorf("%s: the US layout does not type %q", k, c)
			continue
		}
		if e.Scan != Code(m.Scan) || e.Ext != m.Ext || e.VK != Code(m.VK) || e.Shift != m.Shift || e.Ctrl || e.Alt || e.Hankaku {
			t.Errorf("%s: US types %q with %+v, want sc=0x%02X vk=%v shift=%v", k, c, e, m.Scan, Code(m.VK), m.Shift)
		}
	}
	if want := 26 + 10 + 11 + 1; n != want { // letters, digits, punctuation, Plus
		t.Errorf("%d character keys, want %d", n, want)
	}
	// Upper-case letters are Shift plus the letter key.
	for c := 'A'; c <= 'Z'; c++ {
		e, _ := us.Char(c)
		lower, _ := us.Char(c + 'a' - 'A')
		if e.Absent || !e.Shift || e.Scan != lower.Scan || e.VK != lower.VK {
			t.Errorf("US %q = %+v, want Shift and the key of %q (%+v)", c, e, c+'a'-'A', lower)
		}
	}
}

// jisKeys are the keys of Japanese 106/109-key keyboards with the scan
// code the table gives them.
var jisKeys = []struct {
	name string
	scan uint8
}{
	{"IntlYen", 0x7D},
	{"IntlRo", 0x73},
	{"Convert", 0x79},
	{"NonConvert", 0x7B},
	{"KanaMode", 0x70},
}

func isJISKey(name string) bool {
	for _, k := range jisKeys {
		if k.name == name {
			return true
		}
	}
	return false
}

// TestJISKeys checks the JIS keys against the Japanese layout. KBDJPN.DLL
// delegates to the layer driver the i8042prt "LayerDriver JPN" value names:
// kbd106.dll has the JIS keys, kbd101.dll (a 101-key keyboard) does not.
// The fixture holds the tables of one of them, so the subtest of the other
// is skipped.
func TestJISKeys(t *testing.T) {
	jp := loadFixture(t, klidJP)
	t.Run("table", func(t *testing.T) {
		for _, jk := range jisKeys {
			k, ok := keys.Lookup(jk.name)
			if !ok {
				t.Fatalf("keys.Lookup(%q) failed", jk.name)
			}
			if m := k.US(); m.Scan != jk.scan || m.Ext || m.E1 {
				t.Errorf("%s: table scan code %s, want 0x%02X", jk.name, m.ScanString(), jk.scan)
			}
		}
	})
	t.Run("kbd106", func(t *testing.T) {
		if !strings.EqualFold(jp.LayerDriver, "kbd106.dll") {
			// Regenerate the fixture on a machine whose LayerDriver JPN is
			// kbd106.dll to check the JIS VKs of the key table.
			t.Skipf("00000411 was recorded with layer driver %q, not kbd106.dll: the JIS VKs of the key table are not checked", jp.LayerDriver)
		}
		for _, jk := range jisKeys {
			k, ok := keys.Lookup(jk.name)
			if !ok {
				t.Fatalf("keys.Lookup(%q) failed", jk.name)
			}
			got, ok := jp.Scan(jk.scan, false)
			if !ok || got.VK != Code(k.US().VK) {
				t.Errorf("%s: kbd106 scancodes[0x%02X] = %+v, want vk=%v", jk.name, jk.scan, got, Code(k.US().VK))
			}
		}
	})
	t.Run("kbd101", func(t *testing.T) {
		if !strings.EqualFold(jp.LayerDriver, "kbd101.dll") {
			t.Skipf("00000411 was recorded with layer driver %q, not kbd101.dll", jp.LayerDriver)
		}
		// kbd101.dll has the US tables: the JIS scan codes exist or not
		// as on US.
		kbd101 := map[uint8]Code{
			0x73: 0xC1, // VK_ABNT_C1
			0x7B: 0xEB, // VK_OEM_PA1
			0x70: 0,    // no VK
			0x79: 0,
			0x7D: 0,
		}
		for sc, want := range kbd101 {
			got, ok := jp.Scan(sc, false)
			if want == 0 && ok || want != 0 && (!ok || got.VK != want) {
				t.Errorf("kbd101 scancodes[0x%02X] = %+v (present %v), want vk=%v", sc, got, ok, want)
			}
		}
	})
}

// TestFrenchLayout spot-checks the AZERTY fixture, which the layout
// resolver tests (M1b) depend on.
func TestFrenchLayout(t *testing.T) {
	fr := loadFixture(t, klidFR)
	for _, tc := range []struct {
		c     rune
		scan  Code
		shift bool
		altGr bool
	}{
		{'a', 0x10, false, false},
		{'q', 0x1E, false, false},
		{'z', 0x11, false, false},
		{'w', 0x2C, false, false},
		{'m', 0x27, false, false},
		{'1', 0x02, true, false},
		{'+', 0x0D, true, false},
		{'@', 0x0B, false, true},
	} {
		e, _ := fr.Char(tc.c)
		if e.Absent || e.Scan != tc.scan || e.Shift != tc.shift || e.Ctrl != tc.altGr || e.Alt != tc.altGr {
			t.Errorf("French %q = %+v, want sc=%v shift=%v altgr=%v", tc.c, e, tc.scan, tc.shift, tc.altGr)
		}
	}
	// The key right of P is the circumflex dead key.
	e, ok := fr.Scan(0x1A, false)
	if !ok || e.Base == nil || !e.Base.Dead || e.Base.Text != "^" {
		t.Errorf("French sc=0x1A = %+v, want the dead key ^", e)
	}
}

func TestCodeText(t *testing.T) {
	for _, tc := range []struct {
		c    Code
		text string
	}{
		{0x00, "0x00"},
		{0x2C, "0x2C"},
		{0xFE, "0xFE"},
		{0xE01D, "0xE01D"},
	} {
		b, err := tc.c.MarshalText()
		if err != nil || string(b) != tc.text {
			t.Errorf("MarshalText(%d) = %q, %v; want %q", uint16(tc.c), b, err, tc.text)
		}
		var got Code
		if err := got.UnmarshalText([]byte(tc.text)); err != nil || got != tc.c {
			t.Errorf("UnmarshalText(%q) = %v, %v; want %v", tc.text, got, err, tc.c)
		}
	}
	for _, bad := range []string{"", "2C", "0x", "0x2", "0x2c", "0X2C", "0xZZ", "0x10000", " 0x2C", "44"} {
		var c Code
		if err := c.UnmarshalText([]byte(bad)); err == nil {
			t.Errorf("UnmarshalText(%q) = %v, want an error", bad, c)
		}
	}
}

// validFixture returns a small well-formed fixture.
func validFixture() *Fixture {
	f := &Fixture{
		KLID: "00000409", HKL: "0x04090409", LayoutFile: "KBDUS.DLL", LayoutText: "US",
		Generator: Generator, Version: "dev", Windows: "10.0.26200",
		Scancodes: []ScanEntry{
			{Scan: 0x1E, VK: 0x41, Base: &Output{Text: "a"}, Shift: &Output{Text: "A"}},
			{Scan: 0x2C, VK: 0x5A},
			{Scan: 0x1D, Ext: true, VK: 0xA3},
		},
		VKToScan: []VKEntry{{VK: 0x13, Scan: 0x1D, E1: true}, {VK: 0x41, Scan: 0x1E}, {VK: 0xA3, Scan: 0x1D, Ext: true}},
	}
	for c := 0x20; c <= 0x7E; c++ {
		e := CharEntry{Char: string(rune(c)), Code: Code(c), Absent: true}
		if c == 'a' {
			e = CharEntry{Char: "a", Code: 'a', VK: 0x41, Scan: 0x1E}
		}
		f.Chars = append(f.Chars, e)
	}
	return f
}

func TestValidate(t *testing.T) {
	if err := validFixture().Validate(); err != nil {
		t.Fatalf("Validate(valid) = %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Fixture)
		want   string
	}{
		{"bad klid", func(f *Fixture) { f.KLID = "409" }, "bad KLID"},
		{"no hkl", func(f *Fixture) { f.HKL = "" }, "hkl is empty"},
		{"no layout file", func(f *Fixture) { f.LayoutFile = "" }, "layoutFile is empty"},
		{"no generator", func(f *Fixture) { f.Generator = "" }, "generator is empty"},
		{"no windows", func(f *Fixture) { f.Windows = "" }, "windows is empty"},
		{"no scancodes", func(f *Fixture) { f.Scancodes = nil }, "empty code lists"},
		{"scan out of range", func(f *Fixture) { f.Scancodes[0].Scan = 0x80 }, "out of range"},
		{"vk out of range", func(f *Fixture) { f.Scancodes[1].VK = 0xFF }, "out of range"},
		{"scancodes out of order", func(f *Fixture) { f.Scancodes[0], f.Scancodes[1] = f.Scancodes[1], f.Scancodes[0] }, "out of order"},
		{"E0 before plain", func(f *Fixture) { f.Scancodes[2].Ext = false }, "out of order"},
		{"duplicate scan", func(f *Fixture) { f.Scancodes[1].Scan = 0x1E }, "out of order"},
		{"empty output", func(f *Fixture) { f.Scancodes[0].Shift = &Output{} }, "empty output"},
		{"vkToScan out of order", func(f *Fixture) { f.VKToScan[1].VK = 0x13 }, "out of order"},
		{"ext and e1", func(f *Fixture) { f.VKToScan[0].Ext = true }, "invalid"},
		{"chars missing", func(f *Fixture) { f.Chars = f.Chars[:94] }, "94 entries"},
		{"chars out of order", func(f *Fixture) { f.Chars[1], f.Chars[2] = f.Chars[2], f.Chars[1] }, "want"},
		{"absent with a key", func(f *Fixture) { f.Chars[0].VK = 0x20 }, "absent but has a key"},
		{"present without a vk", func(f *Fixture) { f.Chars[0].Absent = false }, "vk=0x00 out of range"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := validFixture()
			tc.mutate(f)
			err := f.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate() = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestUnmarshal(t *testing.T) {
	data, err := validFixture().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	f, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal(Marshal(valid)) = %v", err)
	}
	again, err := f.Marshal()
	if err != nil || !bytes.Equal(again, data) {
		t.Errorf("round trip differs: %v\n%s\n%s", err, again, data)
	}
	if !bytes.Contains(data, []byte(`"scan": "0x1E"`)) || !bytes.HasSuffix(data, []byte("}\n")) {
		t.Errorf("Marshal output is not in the fixture format:\n%s", data)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m["extra"] = true
	withExtra, _ := json.Marshal(m)
	if _, err := Unmarshal(withExtra); err == nil || !strings.Contains(err.Error(), "extra") {
		t.Errorf("Unmarshal with an unknown field = %v, want an error", err)
	}
	if _, err := Unmarshal(append(data, "{}"...)); err == nil {
		t.Error("Unmarshal with trailing data succeeded")
	}
	if _, err := Unmarshal([]byte(`{"klid": "00000409"}`)); err == nil {
		t.Error("Unmarshal of an incomplete fixture succeeded")
	}
}

func TestValidKLID(t *testing.T) {
	for s, want := range map[string]bool{
		"00000409": true, "0000040C": true, "0000040c": true, "E0010411": true,
		"": false, "409": false, "000004090": false, "0000040G": false, "0x000409": false,
	} {
		if got := ValidKLID(s); got != want {
			t.Errorf("ValidKLID(%q) = %v, want %v", s, got, want)
		}
	}
}
