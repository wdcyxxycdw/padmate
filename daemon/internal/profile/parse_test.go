package profile

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// Rewrite the golden files from the current output with
//
//	go test ./internal/profile -update
//
// and review the diff: the goldens are the specification of the messages
// and of the positions they point at.
var update = flag.Bool("update", false, "rewrite testdata/*/*.golden from the current output")

// fixtures returns the .json files in testdata/dir.
func fixtures(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no fixtures in testdata/%s", dir)
	}
	return files
}

// checkGolden compares got with the golden file next to fixture, or
// rewrites it with -update.
func checkGolden(t *testing.T, fixture, got string) {
	t.Helper()
	golden := strings.TrimSuffix(fixture, ".json") + ".golden"
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v; run go test ./internal/profile -update", err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s (rerun with -update and review the diff):\n--- got\n%s--- want\n%s", golden, got, want)
	}
}

// formatDiagnostics writes each diagnostic with the rest of its line from
// Line:Col, so a reviewer sees what the column points at. The excerpt is
// found from Line and Col alone, which also checks them against Offset.
func formatDiagnostics(t *testing.T, data []byte, ds Diagnostics) string {
	t.Helper()
	var b strings.Builder
	for _, d := range ds {
		fmt.Fprintln(&b, d.String())
		fmt.Fprintf(&b, "\t> %s\n", excerpt(t, data, d))
	}
	return b.String()
}

// excerpt returns up to 40 characters of data from d.Line:d.Col.
func excerpt(t *testing.T, data []byte, d Diagnostic) string {
	t.Helper()
	off := bomLen(data)
	for line := 1; line < d.Line; line++ {
		i := bytes.IndexByte(data[off:], '\n')
		if i < 0 {
			t.Errorf("%v: the file has only %d lines", d, line)
			return ""
		}
		off += i + 1
	}
	for col := 1; col < d.Col; col++ {
		if off == len(data) || data[off] == '\n' {
			t.Errorf("%v: line %d has only %d columns", d, d.Line, col)
			return ""
		}
		_, size := utf8.DecodeRune(data[off:])
		off += size
	}
	if off != d.Offset {
		t.Errorf("%v: line:col is offset %d, want %d", d, off, d.Offset)
	}
	rest := data[off:]
	if i := bytes.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	rest = bytes.TrimSuffix(rest, []byte("\r"))
	if len(rest) == 0 {
		if off == len(data) {
			return "(end of file)"
		}
		return "(end of line)"
	}
	// Control characters would make the golden file binary to git.
	s := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return utf8.RuneError
		}
		return r
	}, strings.ToValidUTF8(string(rest), "\uFFFD"))
	if r := []rune(s); len(r) > 40 {
		s = string(r[:40]) + "…"
	}
	return s
}

// formatBindings writes the bindings of layer l, one per line.
func formatBindings(t *testing.T, b *strings.Builder, l *Layer) {
	t.Helper()
	for _, bd := range l.Bindings {
		fmt.Fprintf(b, "\t%-13s %-26s label %q", bd.Control, bd.ID, bd.Label)
		if a := bd.Press; a != nil {
			if a.ID != bd.ID+"/press" {
				t.Errorf("%s: action ID %q", bd.ID, a.ID)
			}
			switch a.Type {
			case ActionMacro:
				fmt.Fprintf(b, "  press: macro %q", a.File)
			case ActionSequence, ActionText:
				fmt.Fprintf(b, "  press: %s", a)
			default:
				fmt.Fprintf(b, "  press: %s %s %s", a.Type, a.Keys, a.Mode)
			}
		}
		if bd.Haptic != 0 {
			fmt.Fprintf(b, "  haptic: %s", bd.Haptic)
		}
		b.WriteByte('\n')
	}
}

// formatProfile writes the resolved profile, one binding per line.
func formatProfile(t *testing.T, p *Profile) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "name %q, schemaVersion %d\n", p.Name, p.SchemaVersion)
	for _, l := range p.Layers {
		fmt.Fprintf(&b, "layer %s\n", l.Name)
		formatBindings(t, &b, l)
	}
	for _, l := range p.FunctionSets() {
		fmt.Fprintf(&b, "function key set %s\n", l.Name)
		formatBindings(t, &b, l)
	}
	// The old format's two sets, merged into the one the engine reads.
	if e := p.EffectiveFunctionSet(); e != nil && p.FunctionSet == nil {
		fmt.Fprintf(&b, "effective function set %s\n", e.Name)
		formatBindings(t, &b, e)
	}
	// Only a feedback that differs from the default is shown, so that the
	// fixtures that do not set one keep their golden files.
	if f := p.Feedback; f != nil && *f != DefaultFeedback() {
		fmt.Fprintf(&b, "feedback shortMs=%d longMs=%d gapMs=%d strength=%d pausedColor=%s\n", f.ShortMs, f.LongMs, f.GapMs, f.Strength, f.PausedColor)
	}
	return b.String()
}

func TestValidProfiles(t *testing.T) {
	for _, file := range fixtures(t, "valid") {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			p, ds := Parse(data)
			if ds.HasErrors() || p == nil {
				t.Fatalf("errors:\n%s", formatDiagnostics(t, data, ds))
			}
			checkGolden(t, file, formatProfile(t, p)+formatDiagnostics(t, data, ds))
		})
	}
}

func TestInvalidProfiles(t *testing.T) {
	for _, file := range fixtures(t, "invalid") {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			p, ds := Parse(data)
			if !ds.HasErrors() || p != nil {
				t.Fatalf("no errors; profile %v, diagnostics %v", p, ds)
			}
			checkGolden(t, file, formatDiagnostics(t, data, ds))
		})
	}
}

// TestExamples checks that the example profiles in the repository have no
// diagnostics at all.
func TestExamples(t *testing.T) {
	files, err := filepath.Glob("../../../examples/profiles/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("examples/profiles not found; this test needs the whole repository")
	}
	for _, file := range files {
		p, ds, err := Load(file)
		if err != nil {
			t.Fatal(err)
		}
		if p == nil || len(ds) > 0 {
			data, _ := os.ReadFile(file)
			t.Errorf("%s:\n%s", file, formatDiagnostics(t, data, ds))
		}
	}
}

// TestParseModel checks the resolved model beyond what the goldens show:
// lookups, parsed chords and the default mode.
func TestParseModel(t *testing.T) {
	const src = `{
  "schemaVersion": 0,
  "name": "t",
  "layers": {"base": {
    "rt": {"press": {"type": "keys", "keys": "shift+f13"}},
    "a": {"label": "撤销", "press": {"type": "keys", "keys": "Ctrl+Z", "mode": "tap"}},
    "dpad.up": {"label": ""}
  }}
}`
	p, ds := Parse([]byte(src))
	if p == nil {
		t.Fatalf("Parse: %v", ds)
	}
	if len(ds) != 1 || ds[0].Severity != SeverityWarning || ds[0].Pointer != "/layers/base/dpad.up" {
		t.Errorf("diagnostics = %v, want one warning for dpad.up", ds)
	}
	if p.Layer("base") == nil || p.Layer("alt") != nil || len(p.Layers) != 1 {
		t.Fatalf("layers = %v", p.Layers)
	}
	base := p.Layer("base")
	var order []control.Control
	for _, b := range base.Bindings {
		order = append(order, b.Control)
	}
	if want := []control.Control{control.South, control.DpadUp, control.RT}; !slices.Equal(order, want) {
		t.Errorf("binding order = %v, want %v", order, want)
	}
	south := base.Binding(control.South)
	if south == nil || south.ID != "/layers/base/a" || south.Label != "撤销" {
		t.Fatalf("south = %+v", south)
	}
	if want := keys.MustParse("Ctrl+Z"); south.Press.Keys != want || south.Press.Mode != ModeTap || south.Press.Type != ActionKeys {
		t.Errorf("south press = %+v", south.Press)
	}
	rt := base.Binding(control.RT)
	if rt.Press.Keys != keys.MustParse("Shift+F13") || rt.Press.Mode != ModeHold {
		t.Errorf("rt press = %+v, want Shift+F13 hold", rt.Press)
	}
	if base.Binding(control.North) != nil {
		t.Error("north is bound")
	}
	if south.Action(GesturePress) != south.Press || south.Action(GestureNone) != nil || south.Action(Gesture(99)) != nil {
		t.Error("south.Action does not return the press action for press alone")
	}
	if base.Binding(control.DpadUp).Action(GesturePress) != nil {
		t.Error("dpad.up has a press action")
	}
}

func TestParseSchemaVersion(t *testing.T) {
	// Version 1 as written, and version 0 (any spelling of zero) migrated
	// in memory: both resolve to the current version.
	for _, v := range []string{"1", "1.0", "1e0", "10e-1", "0", "-0", "0.0", "0e3"} {
		src := `{"schemaVersion": ` + v + `, "name": "n", "layers": {"base": {}}}`
		p, ds := Parse([]byte(src))
		if p == nil || len(ds) > 0 || p.SchemaVersion != CurrentSchema {
			t.Errorf("schemaVersion %s: profile %v, diagnostics %v", v, p, ds)
		}
	}
	for _, v := range []string{"2", "-1", "0.5", `"1"`, "null", "true"} {
		src := `{"schemaVersion": ` + v + `, "name": "n", "layers": {"base": {}}}`
		if p, ds := Parse([]byte(src)); p != nil || !ds.HasErrors() {
			t.Errorf("schemaVersion %s accepted: profile %v, diagnostics %v", v, p, ds)
		}
	}
}

// swapVersion returns doc with its schemaVersion 0 spelled 1 or the other
// way round, or ok false when it has neither.
func swapVersion(doc []byte) (swapped []byte, ok bool) {
	re := regexp.MustCompile(`("schemaVersion"\s*:\s*)([01])\b`)
	m := re.FindSubmatchIndex(doc)
	if m == nil {
		return nil, false
	}
	swapped = bytes.Clone(doc)
	swapped[m[5]-1] = '1' + '0' - doc[m[5]-1]
	return swapped, true
}

// A version 0 file and the same file at version 1 give the same profile and
// the same diagnostics at the same positions, whatever else is in them.
func TestVersionZeroMatchesOne(t *testing.T) {
	for _, dir := range []string{"valid", "invalid"} {
		for _, fixture := range fixtures(t, dir) {
			t.Run(fixture, func(t *testing.T) {
				data, err := os.ReadFile(fixture)
				if err != nil {
					t.Fatal(err)
				}
				swapped, ok := swapVersion(data)
				if !ok {
					t.Skip("no schemaVersion 0 or 1")
				}
				p1, ds1 := Parse(data)
				p2, ds2 := Parse(swapped)
				if got, want := formatDiagnostics(t, swapped, ds2), formatDiagnostics(t, data, ds1); got != want {
					t.Errorf("diagnostics differ:\n--- swapped\n%s--- file\n%s", got, want)
				}
				if (p1 == nil) != (p2 == nil) || p1 != nil && formatProfile(t, p1) != formatProfile(t, p2) {
					t.Error("the profiles differ")
				}
			})
		}
	}
}

func TestParseScriptConfig(t *testing.T) {
	base := `{"schemaVersion":0,"name":"script","layers":{"base":{}},"script":{"entry":"main.ts","api":"0.21"}}`
	p, ds := Parse([]byte(base))
	if len(ds) != 0 || p == nil || p.Script == nil {
		t.Fatalf("valid script profile = %#v, diagnostics %v", p, ds)
	}
	if p.Script.Entry != "main.ts" || p.Script.API != "0.21" {
		t.Fatalf("script config = %#v", p.Script)
	}
	// The previous API still loads, with a warning to move on.
	previous := `{"schemaVersion":0,"name":"script","layers":{"base":{}},"script":{"entry":"main.ts","api":"0.17"}}`
	p, ds = Parse([]byte(previous))
	if p == nil || p.Script == nil || p.Script.API != "0.17" || ds.HasErrors() || len(ds) != 1 ||
		ds[0].Severity != SeverityWarning || ds[0].Pointer != "/script/api" ||
		ds[0].Message != "script API 0.17 is the previous version; this release's is 0.21: change script.api to 0.21" {
		t.Fatalf("API 0.17 profile = %#v, diagnostics %v", p, ds)
	}
	// 0.8 was supported until M17 and 0.16 until M21 and neither is now: the
	// schema's enum has only the current and the previous API.
	for _, doc := range []string{
		`{"schemaVersion":0,"name":"script","layers":{"base":{}},"script":{"entry":"main.ts","api":"0.8"}}`,
		`{"schemaVersion":0,"name":"script","layers":{"base":{}},"script":{"entry":"main.ts","api":"0.5"}}`,
		`{"schemaVersion":0,"name":"script","layers":{"base":{}},"script":{"entry":"main.ts","api":"0.4"}}`,
		`{"schemaVersion":0,"name":"script","layers":{"base":{}},"script":{"entry":"main.ts","api":"0.6"}}`,
		`{"schemaVersion":0,"name":"script","layers":{"base":{}},"script":{"entry":"main.ts","api":"0.7"}}`,
		`{"schemaVersion":0,"name":"script","layers":{"base":{}},"script":{"entry":"main.ts","api":"0.9"}}`,
		`{"schemaVersion":0,"name":"script","layers":{"base":{}},"script":{"entry":"main.ts","api":"0.16"}}`,
		`{"schemaVersion":0,"name":"script","layers":{"base":{}},"script":{"entry":"main.ts","api":"0.22"}}`,
		`{"schemaVersion":0,"name":"script","layers":{"base":{}},"script":{"entry":"main.ts","api":"0.15"}}`,
		`{"schemaVersion":0,"name":"script","layers":{"base":{}},"script":{"entry":"main.ts","api":"0.18"}}`,
		`{"schemaVersion":0,"name":"script","layers":{"base":{}},"script":{"entry":"main.ts","api":"0.21","extra":true}}`,
	} {
		p, ds = Parse([]byte(doc))
		if p != nil || !ds.HasErrors() {
			t.Errorf("invalid script profile accepted: p=%#v diagnostics=%v", p, ds)
		}
	}
}

func TestParseAnalogSettings(t *testing.T) {
	src := `{"schemaVersion":0,"name":"analog","analog":{"ls":{"mode":"eightWay","deadzone":0.2,"saturation":0.9,"directionThreshold":0.35,"diagonalWidth":30,"hysteresisDeg":6,"directionDelayMs":12,"diagonalFallback":"cardinals"},"dpad":{"mode":"fourWay","fold":"clockwise"},"axisThreshold":0.4,"lt":{"press":0.4,"release":0.25}},"layers":{"base":{"ls.up":{},"leftx+":{}}}}`
	p, ds := Parse([]byte(src))
	if p == nil || ds.HasErrors() {
		t.Fatalf("Parse analog: %v", ds)
	}
	if p.Analog.LS.Mode != "eightWay" || p.Analog.LS.Deadzone != 0.2 || p.Analog.LS.Saturation != 0.9 || p.Analog.LS.DirectionDelayMs != 12 || p.Analog.LS.DiagonalFallback != "cardinals" {
		t.Errorf("LS = %+v", p.Analog.LS)
	}
	if p.Analog.Dpad.Mode != "fourWay" || p.Analog.Dpad.Fold != "clockwise" || p.Analog.AxisThreshold != 0.4 {
		t.Errorf("Dpad/axis threshold = %+v/%v", p.Analog.Dpad, p.Analog.AxisThreshold)
	}
	if p.Analog.LT.Press != 0.4 || p.Analog.LT.Release != 0.25 {
		t.Errorf("LT = %+v", p.Analog.LT)
	}
}

func TestParseAnalogDefaults(t *testing.T) {
	p, ds := Parse([]byte(`{"schemaVersion":0,"name":"analog","layers":{"base":{}}}`))
	if p == nil || len(ds) != 0 {
		t.Fatalf("Parse defaults: %v", ds)
	}
	a := p.Analog
	if a.LS.Mode != "standard" || a.LS.Deadzone != 0.15 || a.LS.Saturation != 0.95 || a.LS.DirectionThreshold != 0.30 || a.LS.DiagonalWidth != 45 || a.LS.HysteresisDeg != 8 || a.Dpad.Mode != "buttons" || a.LT.Press != 0.30 || a.LT.Release != 0.20 {
		t.Errorf("defaults = %+v", a)
	}
}

func TestParseAnalogExplicitZero(t *testing.T) {
	src := `{"schemaVersion":0,"name":"analog","analog":{"ls":{"deadzone":0,"saturation":0.5,"directionThreshold":0,"hysteresisDeg":0},"axisThreshold":0,"lt":{"press":0,"release":0}},"layers":{"base":{}}}`
	p, ds := Parse([]byte(src))
	if p == nil || ds.HasErrors() {
		t.Fatalf("Parse explicit zero: %v", ds)
	}
	if p.Analog.LS.Deadzone != 0 || p.Analog.LS.DirectionThreshold != 0 || p.Analog.LS.HysteresisDeg != 0 || p.Analog.AxisThreshold != 0 || p.Analog.LT.Press != 0 || p.Analog.LT.Release != 0 {
		t.Errorf("explicit zero was replaced: %+v", p.Analog)
	}
}

func TestParseAnalogRejectsEqualDeadzoneAndSaturation(t *testing.T) {
	src := `{"schemaVersion":0,"name":"analog","analog":{"ls":{"deadzone":0.5,"saturation":0.5}},"layers":{"base":{}}}`
	p, ds := Parse([]byte(src))
	if p != nil || !ds.HasErrors() || !strings.Contains(ds[0].Message, "greater than deadzone") {
		t.Fatalf("equal deadzone/saturation: profile=%v diagnostics=%v", p, ds)
	}
}

func TestLoad(t *testing.T) {
	p, ds, err := Load("testdata/valid/minimal.json")
	if err != nil || p == nil || len(ds) > 0 {
		t.Errorf("Load(minimal.json) = %v, %v, %v", p, ds, err)
	}
	p, ds, err = Load("testdata/invalid/empty.json")
	if err != nil || p != nil || !ds.HasErrors() {
		t.Errorf("Load(empty.json) = %v, %v, %v", p, ds, err)
	}
	if _, _, err := Load("testdata/missing.json"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load(missing.json) error = %v, want ErrNotExist", err)
	}
}

func TestReadError(t *testing.T) {
	_, _, err := Load("testdata/missing.json")
	var pe *os.PathError
	if !errors.As(err, &pe) {
		t.Fatalf("Load(missing.json) error = %#v, want an *os.PathError", err)
	}
	if got, want := ReadError("testdata/missing.json", err), "testdata/missing.json: "+pe.Err.Error(); got != want {
		t.Errorf("ReadError(missing) = %q, want %q", got, want)
	}
	_, _, err = Load("testdata")
	if got := ReadError("testdata", err); got != "testdata: is a directory" {
		t.Errorf("ReadError(directory) = %q, want %q", got, "testdata: is a directory")
	}
	if got := ReadError("testdata/missing.json", errors.New("disk on fire")); got != "testdata/missing.json: disk on fire" {
		t.Errorf("ReadError(other) = %q", got)
	}
}

func TestModelStrings(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{ActionKeys.String(), "keys"},
		{ActionType(0).String(), "actiontype(0)"},
		{ModeHold.String(), "hold"},
		{ModeTap.String(), "tap"},
		{Mode(0).String(), "mode(0)"},
		{GestureNone.String(), "none"},
		{GesturePress.String(), "press"},
		{Gesture(GestureCount).String(), "gesture(8)"},
		{Action{Type: ActionKeys, Mode: ModeTap, Keys: keys.MustParse("Ctrl+Z")}.String(), "tap Ctrl+Z"},
		{Action{Type: ActionKeys, Mode: ModeHold, Keys: keys.MustParse("Space")}.String(), "hold Space"},
		{Action{}.String(), "actiontype(0)"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

// TestParseM15Analog checks the rings, knob and two-stage trigger settings
// (ANA-10, ANA-11, ANA-14): the defaults, the values written, and the
// controls they add.
func TestParseM15Analog(t *testing.T) {
	p, ds := Parse([]byte(`{"schemaVersion":1,"name":"m15","layers":{"base":{}}}`))
	if p == nil || len(ds) != 0 {
		t.Fatalf("Parse defaults: %v", ds)
	}
	for name, s := range map[string]Stick{"ls": p.Analog.LS, "rs": p.Analog.RS} {
		if s.OuterThreshold != 0.90 || s.KnobStepDeg != 30 {
			t.Errorf("%s defaults: outer %v, knob step %d; want 0.90, 30", name, s.OuterThreshold, s.KnobStepDeg)
		}
	}
	for name, tr := range map[string]Trigger{"lt": p.Analog.LT, "rt": p.Analog.RT} {
		if tr.FullPress != 0.90 || tr.FullRelease != 0.80 {
			t.Errorf("%s defaults: fullPress %v, fullRelease %v; want 0.90, 0.80", name, tr.FullPress, tr.FullRelease)
		}
	}
	if d := DefaultAnalog(); d.LS.OuterThreshold != 0.90 || d.RS.KnobStepDeg != 30 || d.LT.FullPress != 0.90 || d.RT.FullRelease != 0.80 {
		t.Errorf("DefaultAnalog() = %+v", d)
	}

	const src = `{"schemaVersion":1,"name":"m15","analog":{
		"ls":{"outerThreshold":0.7},
		"rs":{"mode":"knob","knobStepDeg":45,"outerThreshold":0.6},
		"lt":{"fullPress":0.95,"fullRelease":0.7},
		"rt":{"press":0.4,"release":0.3,"fullPress":0.6,"fullRelease":0.6}},
		"layers":{"base":{"ls.inner":{},"ls.outer":{},"rs.cw":{},"rs.ccw":{},"lt.full":{},"rt.full":{}}}}`
	p, ds = Parse([]byte(src))
	if p == nil || ds.HasErrors() {
		t.Fatalf("Parse m15 settings: %v", ds)
	}
	if a := p.Analog; a.LS.OuterThreshold != 0.7 || a.LS.Mode != "standard" || a.RS.Mode != "knob" || a.RS.KnobStepDeg != 45 || a.RS.OuterThreshold != 0.6 {
		t.Errorf("sticks = %+v / %+v", a.LS, a.RS)
	}
	if a := p.Analog; a.LT.FullPress != 0.95 || a.LT.FullRelease != 0.7 || a.RT.FullPress != 0.6 || a.RT.FullRelease != 0.6 {
		t.Errorf("triggers = %+v / %+v", a.LT, a.RT)
	}
	base := p.Layer("base")
	for _, c := range []control.Control{control.LSInner, control.LSOuter, control.RSCW, control.RSCCW, control.LTFull, control.RTFull} {
		if base.Binding(c) == nil {
			t.Errorf("binding of %s is missing", c)
		}
	}
}

// TestParseM15AnalogErrors checks the rules of the second trigger stage and
// the ranges of the new fields.
func TestParseM15AnalogErrors(t *testing.T) {
	for _, tc := range []struct{ name, analog, pointer, message string }{
		{"fullRelease above fullPress", `{"lt":{"fullPress":0.7,"fullRelease":0.75}}`, "/analog/lt/fullRelease", "fullRelease must not exceed fullPress"},
		{"fullRelease above the default fullPress", `{"rt":{"fullRelease":0.95}}`, "/analog/rt/fullRelease", "fullRelease must not exceed fullPress"},
		{"fullPress below press", `{"lt":{"press":0.6,"release":0.5,"fullPress":0.5,"fullRelease":0.4}}`, "/analog/lt/fullPress", "fullPress must be greater than press"},
		{"fullPress equal to press", `{"rt":{"press":0.5,"release":0.4,"fullPress":0.5,"fullRelease":0.45}}`, "/analog/rt/fullPress", "fullPress must be greater than press"},
		{"fullPress above 1", `{"lt":{"fullPress":1.1}}`, "/analog/lt/fullPress", ""},
		{"fullRelease below 0", `{"rt":{"fullRelease":-0.1}}`, "/analog/rt/fullRelease", ""},
		{"outerThreshold above 1", `{"ls":{"outerThreshold":1.5}}`, "/analog/ls/outerThreshold", ""},
		{"outerThreshold below 0", `{"rs":{"outerThreshold":-0.1}}`, "/analog/rs/outerThreshold", ""},
		{"knobStepDeg below 10", `{"rs":{"mode":"knob","knobStepDeg":9}}`, "/analog/rs/knobStepDeg", ""},
		{"knobStepDeg above 180", `{"ls":{"mode":"knob","knobStepDeg":181}}`, "/analog/ls/knobStepDeg", ""},
		{"knobStepDeg fractional", `{"ls":{"mode":"knob","knobStepDeg":22.5}}`, "/analog/ls/knobStepDeg", ""},
		{"unknown mode", `{"ls":{"mode":"dial"}}`, "/analog/ls/mode", ""},
		{"empty stick label", `{"ls":{"label":""}}`, "/analog/ls/label", ""},
		{"stick label over 80", `{"rs":{"label":"` + strings.Repeat("x", 81) + `"}}`, "/analog/rs/label", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, ds := Parse([]byte(`{"schemaVersion":1,"name":"bad","analog":` + tc.analog + `,"layers":{"base":{}}}`))
			if p != nil && !ds.HasErrors() {
				t.Fatalf("accepted: %+v", p.Analog)
			}
			for _, d := range ds {
				if d.Severity == SeverityError && d.Pointer == tc.pointer && strings.Contains(d.Message, tc.message) {
					return
				}
			}
			t.Errorf("no error at %s containing %q in %v", tc.pointer, tc.message, ds)
		})
	}
}

// TestParseStickLabel checks the name of a stick's movement (analog.ls.label,
// ADR-0079): an optional string the editor shows and the engine ignores.
func TestParseStickLabel(t *testing.T) {
	p, ds := Parse([]byte(`{"schemaVersion":1,"name":"named","analog":{"ls":{"label":"画笔大小","mode":"knob"},"rs":{"label":"缩放"}},"layers":{"base":{}}}`))
	if p == nil || len(ds) != 0 {
		t.Fatalf("Parse: %v", ds)
	}
	if p.Analog.LS.Mode != "knob" {
		t.Errorf("ls mode = %q, want knob", p.Analog.LS.Mode)
	}
}

// TestParseTwoStageWorkaround checks that the profiles the editor wrote
// before lt.full, a trigger with press 0.90 and release 0.80 (UI, M12), still
// parse: the default fullPress is not held to be above press.
func TestParseTwoStageWorkaround(t *testing.T) {
	p, ds := Parse([]byte(`{"schemaVersion":1,"name":"old","analog":{"lt":{"press":0.9,"release":0.8},"rt":{"press":0.95,"release":0.9}},"layers":{"base":{}}}`))
	if p == nil || ds.HasErrors() {
		t.Fatalf("Parse: %v", ds)
	}
	if a := p.Analog; a.LT.Press != 0.9 || a.LT.FullPress != 0.9 || a.RT.Press != 0.95 {
		t.Errorf("triggers = %+v / %+v", a.LT, a.RT)
	}
}

// TestParseCurvesAndMinRate checks the response curves and the rate-modulated
// repeat (ANA-12, ANA-13, ANA-15): the defaults, the values written, and the
// controls a minRateHz may be on.
func TestParseCurvesAndMinRate(t *testing.T) {
	p, ds := Parse([]byte(`{"schemaVersion":1,"name":"curves","layers":{"base":{}}}`))
	if p == nil || len(ds) != 0 {
		t.Fatalf("Parse defaults: %v", ds)
	}
	for name, curve := range map[string]string{"ls": p.Analog.LS.Curve, "rs": p.Analog.RS.Curve, "lt": p.Analog.LT.Curve, "rt": p.Analog.RT.Curve} {
		if curve != CurveLinear {
			t.Errorf("%s curve = %q, want linear", name, curve)
		}
	}
	const src = `{"schemaVersion":1,"name":"curves","analog":{
		"ls":{"curve":"quadratic"},"rs":{"curve":"cubic"},"lt":{"curve":"quadratic"},"rt":{"curve":"linear"}},
		"layers":{"base":{
		  "ls.up":{"press":{"type":"keys","keys":"A","repeat":{"rateHz":20,"minRateHz":3}}},
		  "rt":{"press":{"type":"keys","keys":"B","repeat":{"minRateHz":12}}}}}}`
	p, ds = Parse([]byte(src))
	if p == nil || ds.HasErrors() {
		t.Fatalf("Parse: %v", ds)
	}
	if a := p.Analog; a.LS.Curve != CurveQuadratic || a.RS.Curve != CurveCubic || a.LT.Curve != CurveQuadratic || a.RT.Curve != CurveLinear {
		t.Errorf("curves = %q %q %q %q", a.LS.Curve, a.RS.Curve, a.LT.Curve, a.RT.Curve)
	}
	base := p.Layer("base")
	if r := base.Binding(control.LSUp).Press.Repeat; r.RateHz != 20 || r.MinRateHz != 3 || r.EffectiveRateHz() != 20 {
		t.Errorf("ls.up repeat = %+v", r)
	}
	// Without rateHz, the default rate (12) is the top of the range.
	if r := base.Binding(control.RT).Press.Repeat; r.RateHz != 0 || r.MinRateHz != 12 || r.EffectiveRateHz() != DefaultRepeatRateHz {
		t.Errorf("rt repeat = %+v", r)
	}
}

// TestParseMinRateErrors checks the rules of minRateHz (ANA-12): not above
// the rate, and only on a control that has a depth. Each error is at the
// minRateHz member.
func TestParseMinRateErrors(t *testing.T) {
	const tooHigh = "minRateHz must not exceed rateHz"
	const needsAnalog = "minRateHz needs a stick, trigger or half-axis control"
	for _, tc := range []struct {
		name, control, repeat, message string // message "" is a schema error
	}{
		{"above rateHz", "ls.up", `{"rateHz":10,"minRateHz":11}`, tooHigh},
		{"above the default rate", "rt", `{"minRateHz":13}`, tooHigh},
		{"on a button", "south", `{"rateHz":20,"minRateHz":3}`, needsAnalog},
		{"on a d-pad diagonal", "dpad.upleft", `{"minRateHz":3}`, needsAnalog},
		{"on a knob tick", "rs.cw", `{"minRateHz":3}`, needsAnalog},
		{"on a button and above rateHz", "east", `{"rateHz":5,"minRateHz":6}`, needsAnalog},
		{"zero", "ls.up", `{"minRateHz":0}`, ""},
		{"fractional", "ls.up", `{"minRateHz":2.5}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pointer := "/layers/base/" + tc.control + "/press/repeat/minRateHz"
			p, ds := Parse([]byte(`{"schemaVersion":1,"name":"bad","layers":{"base":{"` + tc.control + `":{"press":{"type":"keys","keys":"A","repeat":` + tc.repeat + `}}}}}`))
			if p != nil && !ds.HasErrors() {
				t.Fatalf("accepted: %s", tc.repeat)
			}
			for _, d := range ds {
				if d.Severity == SeverityError && d.Pointer == pointer && strings.Contains(d.Message, tc.message) {
					if tc.message != "" && d.Line == 0 {
						t.Errorf("no position: %+v", d)
					}
					return
				}
			}
			t.Errorf("no error at %s containing %q in %v", pointer, tc.message, ds)
		})
	}
	// The same rules apply to the other gestures that repeat, and to the
	// common layer.
	p, ds := Parse([]byte(`{"schemaVersion":1,"name":"hold","layers":{"base":{}},"common":{"lt.full":{"hold":{"type":"keys","keys":"A","repeat":{"rateHz":8,"minRateHz":8}}}}}`))
	if p == nil || ds.HasErrors() {
		t.Fatalf("minRateHz equal to rateHz on a hold in common: %v", ds)
	}
}

// TestParseCurveErrors checks that only the three curves are accepted.
func TestParseCurveErrors(t *testing.T) {
	for _, tc := range []struct{ name, analog, pointer string }{
		{"stick", `{"ls":{"curve":"sine"}}`, "/analog/ls/curve"},
		{"trigger", `{"rt":{"curve":"Quadratic"}}`, "/analog/rt/curve"},
		{"empty", `{"rs":{"curve":""}}`, "/analog/rs/curve"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, ds := Parse([]byte(`{"schemaVersion":1,"name":"bad","analog":` + tc.analog + `,"layers":{"base":{}}}`))
			if p != nil && !ds.HasErrors() {
				t.Fatalf("accepted: %+v", p.Analog)
			}
			for _, d := range ds {
				if d.Severity == SeverityError && d.Pointer == tc.pointer {
					return
				}
			}
			t.Errorf("no error at %s in %v", tc.pointer, ds)
		})
	}
}
