package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Rewrite testdata/validate.golden from the current output with
//
//	go test ./cmd/padmatectl -run TestValidateGolden -update
//
// and review the diff.
var update = flag.Bool("update", false, "rewrite testdata/validate.golden from the current output")

// profilesDir holds the M1a acceptance fixtures: a drawing profile without
// mistakes and three with one mistake each.
const profilesDir = "../../testdata/profiles/"

var validateFixtures = []string{
	profilesDir + "ok.json",
	profilesDir + "syntax-error.json",
	profilesDir + "schema-error.json",
	profilesDir + "semantic-error.json",
}

// labelOnly is a profile with only a warning: a binding with a label and
// no action, next to one with an action.
const labelOnly = "testdata/label-only.json"

// functionKeys is a profile with the sets of the old functionKeys format
// (MAP-27, ADR-0069), and a binding on back, an ordinary control since
// ADR-0075. functionSet is the same in the new format, with a binding on the
// default function key, start, in the layer and in the set: validate compiles
// with the default, which ignores them, so they are not listed.
const (
	functionKeys = "testdata/function-keys.json"
	functionSet  = "testdata/function-set.json"
)

// TestValidateGolden compares a transcript of validate over the fixtures
// labelOnly, functionKeys and functionSet, as text and as JSON, with
// testdata/validate.golden.
func TestValidateGolden(t *testing.T) {
	var b strings.Builder
	for _, args := range [][]string{
		append([]string{"validate"}, validateFixtures...),
		append([]string{"validate", "--json"}, validateFixtures...),
		{"validate", labelOnly},
		{"validate", "--json", labelOnly},
		{"validate", functionKeys},
		{"validate", "--json", functionKeys},
		{"validate", functionSet},
		{"validate", "--json", functionSet},
	} {
		code, stdout, stderr := runCmd(t, args...)
		if stderr != "" {
			t.Errorf("%q: stderr = %q, want empty", args, stderr)
		}
		fmt.Fprintf(&b, "$ padmatectl %s\n%sexit %d\n", strings.Join(args, " "), stdout, code)
	}
	const golden = "testdata/validate.golden"
	if *update {
		if err := os.WriteFile(golden, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v; run go test ./cmd/padmatectl -run TestValidateGolden -update", err)
	}
	if got := b.String(); got != string(want) {
		t.Errorf("output differs from %s (rerun with -update and review the diff):\n--- got\n%s--- want\n%s", golden, got, want)
	}
}

// TestValidatePositions checks that each fixture's single diagnostic points
// at the mistake itself, by comparing the rest of the line from line:col.
func TestValidatePositions(t *testing.T) {
	for _, tc := range []struct{ file, rest string }{
		{"syntax-error.json", ","},                           // the trailing comma
		{"schema-error.json", `"toggle"}},`},                 // the invalid mode
		{"semantic-error.json", `Shfit+Z", "mode": "tap"}}`}, // the misspelled key inside the spec
	} {
		path := profilesDir + tc.file
		code, stdout, _ := runCmd(t, "validate", path)
		var line, col int
		if n, _ := fmt.Sscanf(strings.TrimPrefix(stdout, path+":"), "%d:%d:", &line, &col); n != 2 || code != 1 || strings.Count(stdout, "\n") != 1 {
			t.Errorf("validate %s = %d, %q; want exit 1 and one diagnostic", tc.file, code, stdout)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(data), "\n")
		if line > len(lines) || col > len([]rune(lines[line-1]))+1 {
			t.Errorf("%s: %d:%d is outside the file", tc.file, line, col)
			continue
		}
		if got := string([]rune(lines[line-1])[col-1:]); got != tc.rest {
			t.Errorf("%s: %d:%d points at %q, want %q", tc.file, line, col, got, tc.rest)
		}
	}
}

func TestValidateJSON(t *testing.T) {
	code, stdout, stderr := runCmd(t, append([]string{"validate", "--json"}, validateFixtures...)...)
	if code != 1 || stderr != "" {
		t.Errorf("exit code = %d, stderr %q; want 1, empty", code, stderr)
	}
	sc := bufio.NewScanner(strings.NewReader(stdout))
	var files []string
	for sc.Scan() {
		var obj struct {
			File        string              `json:"file"`
			OK          bool                `json:"ok"`
			Diagnostics profile.Diagnostics `json:"diagnostics"`
			Bindings    *[]jsonBinding      `json:"bindings"`
		}
		if err := json.Unmarshal(sc.Bytes(), &obj); err != nil {
			t.Fatalf("%q: %v", sc.Text(), err)
		}
		files = append(files, obj.File)
		p, ds, err := profile.Load(obj.File)
		if err != nil {
			t.Fatal(err)
		}
		if obj.OK != !ds.HasErrors() || !slices.Equal(obj.Diagnostics, ds) {
			t.Errorf("%s: ok %v, diagnostics %v; want %v, %v", obj.File, obj.OK, obj.Diagnostics, !ds.HasErrors(), ds)
		}
		if len(ds) == 0 && !strings.Contains(sc.Text(), `"diagnostics":[]`) {
			t.Errorf("%s: %s; want an empty diagnostics array", obj.File, sc.Text())
		}
		switch {
		case ds.HasErrors():
			if obj.Bindings != nil {
				t.Errorf("%s: bindings %v; want none for a file with errors", obj.File, *obj.Bindings)
			}
		case obj.Bindings == nil:
			t.Errorf("%s: %s; want bindings", obj.File, sc.Text())
		default:
			if want := wantBindings(t, p); !slices.Equal(*obj.Bindings, want) || len(want) == 0 {
				t.Errorf("%s: bindings %v; want %v", obj.File, *obj.Bindings, want)
			}
		}
	}
	if !slices.Equal(files, validateFixtures) {
		t.Errorf("files = %q, want %q", files, validateFixtures)
	}
}

// jsonBinding is one element of "bindings" in validate --json.
type jsonBinding struct {
	ID             string  `json:"id"`
	Control        string  `json:"control"`
	Label          string  `json:"label"`
	Gesture        string  `json:"gesture"`
	AddedLatencyMs float64 `json:"addedLatencyMs"`
}

// wantBindings returns the bindings of p as validate --json should print
// them.
func wantBindings(t *testing.T, p *profile.Profile) []jsonBinding {
	t.Helper()
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	var bs []jsonBinding
	for _, b := range prog.Bindings() {
		jb := jsonBinding{
			ID: b.Binding.ID, Control: b.Binding.Control.String(), Label: b.Binding.Label,
			Gesture: b.Gesture.String(), AddedLatencyMs: float64(b.AddedLatency) / float64(time.Millisecond),
		}
		if b.Gesture == engine.GestureNone {
			jb.Gesture = "" // left out
		}
		bs = append(bs, jb)
	}
	return bs
}

// TestValidateBindingLines checks the binding lines of a file without
// problems: its bindings in control order, labels quoted only when set.
func TestValidateBindingLines(t *testing.T) {
	code, stdout, stderr := runCmd(t, "validate", profilesDir+"ok.json")
	want := profilesDir + `ok.json: ok
  /layers/base/south "撤销": press +0 ms
  /layers/base/east "抓手": press +0 ms
  /layers/base/west "橡皮": press +0 ms
  /layers/base/lb: press +0 ms
  /layers/base/lt "吸管": press +0 ms
  /layers/base/rt "重做": press +0 ms
`
	if code != 0 || stdout != want || stderr != "" {
		t.Errorf("validate ok.json = %d, stdout\n%s, stderr %q; want 0, stdout\n%s, empty", code, stdout, stderr, want)
	}
}

func TestLatencyMs(t *testing.T) {
	for _, tt := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0"},
		{20 * time.Millisecond, "20"},
		{1500 * time.Microsecond, "1.5"},
		{250 * time.Microsecond, "0.25"},
		{time.Nanosecond, "0.000001"},
	} {
		if got := latencyMs(tt.d); got != tt.want {
			t.Errorf("latencyMs(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

// TestCompileError covers the diagnostic for a profile the engine cannot
// compile, which profile.Parse never returns.
func TestCompileError(t *testing.T) {
	bs, ds := compileBindings(nil, control.DefaultFunctionKey)
	if bs != nil || len(ds) != 1 || ds[0].String() != "1:1: error: cannot compile the profile: engine: no profile" {
		t.Errorf("compileBindings(nil) = %v, %v; want no bindings and one error", bs, ds)
	}
}

func TestValidateExitCodes(t *testing.T) {
	warn := filepath.Join(t.TempDir(), "warn.json")
	if err := os.WriteFile(warn, []byte(`{"schemaVersion": 0, "name": "n", "layers": {"base": {"south": {"label": "撤销"}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(empty, []byte(`{"schemaVersion": 0, "name": "n", "layers": {"base": {}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := profilesDir + "missing.json"
	dir := t.TempDir()
	tests := []struct {
		args       []string
		code       int
		wantStdout string // substring; "" means empty
		wantStderr string // substring; "" means empty
	}{
		{[]string{profilesDir + "ok.json"}, 0, profilesDir + "ok.json: ok\n  /layers/base/south \"撤销\": press +0 ms\n", ""},
		{[]string{warn}, 0, warn + ":1:64: warning: binding has no action (/layers/base/south)\n  /layers/base/south \"撤销\": no action\n", ""},
		{[]string{"--json", warn}, 0, `"bindings":[{"id":"/layers/base/south","control":"south","label":"撤销","addedLatencyMs":0}]}`, ""},
		{[]string{"--json", empty}, 0, `"diagnostics":[],"bindings":[]}`, ""},
		{[]string{profilesDir + "ok.json", profilesDir + "semantic-error.json"}, 1, "semantic-error.json:9:", ""},
		{[]string{missing}, 2, "", "padmatectl validate: " + missing + ": "},
		// An unreadable file does not stop the others, and outranks errors.
		{[]string{missing, profilesDir + "semantic-error.json"}, 2, "semantic-error.json:9:", missing},
		{[]string{"--json", missing}, 2, "", missing},
		{[]string{dir}, 2, "", "padmatectl validate: " + dir + ": is a directory\n"},
		{nil, 2, "", "no profile files given"},
		{[]string{"--bogus", profilesDir + "ok.json"}, 2, "", "usage: padmatectl validate"},
		{[]string{"-h"}, 0, "usage: padmatectl validate", ""},
	}
	for _, tt := range tests {
		code, stdout, stderr := runCmd(t, append([]string{"validate"}, tt.args...)...)
		if code != tt.code {
			t.Errorf("%q: exit code = %d, want %d", tt.args, code, tt.code)
		}
		if tt.wantStdout == "" && stdout != "" || !strings.Contains(stdout, tt.wantStdout) {
			t.Errorf("%q: stdout = %q, want %q", tt.args, stdout, tt.wantStdout)
		}
		if tt.wantStderr == "" && stderr != "" || !strings.Contains(stderr, tt.wantStderr) {
			t.Errorf("%q: stderr = %q, want %q", tt.args, stderr, tt.wantStderr)
		}
	}
}

// A macro that an action runs and the macros folder next to the file lacks
// is a warning: the profile still loads. One listed under another case is
// missing too, as the runtime finds files by their exact name.
func TestValidateMissingMacros(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")
	doc := `{"schemaVersion": 0, "name": "n", "layers": {"base": {
  "south": {"tap": {"type": "macro", "file": "有.ts"}},
  "east": {"tap": {"type": "macro", "file": "没有.ts"}},
  "west": {"tap": {"type": "macro", "file": "case.ts"}}}}}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCmd(t, "validate", path)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		path + `:3:45: warning: macro file "没有.ts" is not in the macros folder (/layers/base/east/tap/file)`,
		path + `:4:45: warning: macro file "case.ts" is not in the macros folder (/layers/base/west/tap/file)`,
		path + `:2:46: warning: macro file "有.ts" is not in the macros folder (/layers/base/south/tap/file)`,
	} {
		if !strings.Contains(stdout, want+"\n") {
			t.Errorf("stdout %q, want %q", stdout, want)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, profile.MacroDir), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"有.ts", "Case.ts"} {
		if err := os.WriteFile(filepath.Join(dir, profile.MacroDir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, stdout, _ = runCmd(t, "validate", "--json", path)
	var obj validateJSONObject
	if err := json.Unmarshal([]byte(stdout), &obj); err != nil {
		t.Fatal(err)
	}
	var pointers []string
	for _, d := range obj.Diagnostics {
		pointers = append(pointers, d.Pointer)
	}
	if !obj.OK || !slices.Equal(pointers, []string{"/layers/base/east/tap/file", "/layers/base/west/tap/file"}) {
		t.Fatalf("%+v", obj)
	}

	// A profile of a library, profiles/<id>/profile.json, runs its macros
	// from the script library of the data folder (SCR-51), not from a
	// macros folder of its own.
	data := t.TempDir()
	library := filepath.Join(data, "profiles", "paint", "profile.json")
	for _, p := range []string{library, filepath.Join(data, profile.MacroDir, "有.ts"), filepath.Join(data, "profiles", "paint", profile.MacroDir, "没有.ts")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, stdout, _ = runCmd(t, "validate", "--json", library)
	obj = validateJSONObject{}
	if err := json.Unmarshal([]byte(stdout), &obj); err != nil {
		t.Fatal(err)
	}
	pointers = nil
	for _, d := range obj.Diagnostics {
		pointers = append(pointers, d.Pointer)
	}
	if !obj.OK || !slices.Equal(pointers, []string{"/layers/base/east/tap/file", "/layers/base/west/tap/file"}) {
		t.Fatalf("%+v", obj)
	}
}

// The wheel keys are keys (ADR-0062): a profile that binds them validates,
// with their gestures listed, and a wheel key before a modifier is the same
// error as any non-modifier before one.
func TestValidateWheelKeys(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "wheel.json")
	const goodProfile = `{
  "schemaVersion": 1,
  "name": "wheel",
  "analog": {"rs": {"mode": "knob"}},
  "layers": {
    "base": {
      "rs.cw": {"label": "放大", "press": {"type": "keys", "keys": "WheelUp", "mode": "tap"}},
      "rs.ccw": {"label": "缩小", "press": {"type": "keys", "keys": "WheelDown", "mode": "tap"}},
      "north": {"label": "下滚", "press": {"type": "keys", "keys": "wheeldown", "mode": "hold"}},
      "east": {"label": "缩放", "press": {"type": "keys", "keys": "Ctrl+WheelLeft", "mode": "hold"}},
      "south": {"label": "横滚", "press": {"type": "keys", "keys": ["WheelLeft", "Shift+WheelRight"], "mode": "tap"}}
    }
  }
}
`
	bad := filepath.Join(dir, "bad.json")
	const badProfile = `{
  "schemaVersion": 1,
  "name": "bad",
  "layers": {
    "base": {
      "north": {"press": {"type": "keys", "keys": "WheelUp+Ctrl", "mode": "tap"}},
      "east": {"press": {"type": "keys", "keys": "Ctrl+WheelMiddle", "mode": "tap"}}
    }
  }
}
`
	for path, text := range map[string]string{good: goodProfile, bad: badProfile} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, stdout, stderr := runCmd(t, "validate", good)
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, good+": ok\n") {
		t.Fatalf("validate wheel profile = %d, stdout %q, stderr %q; want ok", code, stdout, stderr)
	}
	for _, binding := range []string{"/layers/base/rs.cw", "/layers/base/rs.ccw", "/layers/base/north", "/layers/base/east", "/layers/base/south"} {
		if !strings.Contains(stdout, binding) {
			t.Errorf("validate output lacks the binding %s:\n%s", binding, stdout)
		}
	}

	code, stdout, _ = runCmd(t, "validate", bad)
	if code != 1 {
		t.Fatalf("validate bad wheel profile = %d, want 1:\n%s", code, stdout)
	}
	for _, want := range []string{
		`"WheelUp" is not a modifier; only the last key of a chord can be a non-modifier`,
		`unknown key "WheelMiddle"`,
		"(/layers/base/north/press/keys)",
		"(/layers/base/east/press/keys)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("validate output lacks %q:\n%s", want, stdout)
		}
	}
}

// The mouse buttons are keys too (ADR-0062): a profile that binds them
// validates, a tap repeat is repeated clicks, and a hold with a typematic
// repeat is an error that suggests mode "tap", also with the JSON output.
func TestValidateMouseButtons(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "buttons.json")
	const goodProfile = `{
  "schemaVersion": 1,
  "name": "buttons",
  "layers": {
    "base": {
      "south": {"label": "点击", "press": {"type": "keys", "keys": "MouseLeft", "mode": "tap"}},
      "east": {"label": "右键", "press": {"type": "keys", "keys": "rbutton", "mode": "hold"}},
      "west": {"label": "连点", "press": {"type": "keys", "keys": "MouseMiddle", "mode": "tap", "repeat": {"rateHz": 8}}},
      "north": {"label": "拖动", "press": {"type": "keys", "keys": "Shift+XButton1", "mode": "hold"}},
      "lt": {"label": "连点二", "press": {"type": "keys", "keys": "MouseX2", "repeat": {"style": "tap"}, "mode": "tap"}}
    }
  }
}
`
	bad := filepath.Join(dir, "bad.json")
	const badProfile = `{
  "schemaVersion": 1,
  "name": "bad",
  "layers": {
    "base": {
      "south": {"press": {"type": "keys", "keys": "MouseLeft", "mode": "hold", "repeat": {"delayMs": 100}}},
      "east": {"press": {"type": "keys", "keys": "Ctrl+RButton", "repeat": {"style": "typematic"}}},
      "north": {"press": {"type": "keys", "keys": "WheelUp", "mode": "hold", "repeat": {"delayMs": 100}}},
      "west": {"press": {"type": "keys", "keys": "MouseLeft+Ctrl", "mode": "tap"}}
    }
  }
}
`
	for path, text := range map[string]string{good: goodProfile, bad: badProfile} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, stdout, stderr := runCmd(t, "validate", good)
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, good+": ok\n") {
		t.Fatalf("validate button profile = %d, stdout %q, stderr %q; want ok", code, stdout, stderr)
	}
	for _, binding := range []string{"/layers/base/south", "/layers/base/east", "/layers/base/west", "/layers/base/north", "/layers/base/lt"} {
		if !strings.Contains(stdout, binding) {
			t.Errorf("validate output lacks the binding %s:\n%s", binding, stdout)
		}
	}

	code, stdout, _ = runCmd(t, "validate", bad)
	if code != 1 {
		t.Fatalf("validate bad button profile = %d, want 1:\n%s", code, stdout)
	}
	const message = `a mouse button cannot use typematic repeat, which presses it again while it is down: use mode "tap" to repeat clicks`
	for _, want := range []string{
		message + " (/layers/base/south/press/repeat)",
		message + " (/layers/base/east/press/repeat/style)",
		`"MouseLeft" is not a modifier`,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("validate output lacks %q:\n%s", want, stdout)
		}
	}
	// The wheel key repeats like a key: its binding is fine.
	if strings.Contains(stdout, "/layers/base/north") {
		t.Errorf("validate output complains about the wheel key's repeat:\n%s", stdout)
	}

	code, stdout, _ = runCmd(t, "validate", "--json", bad)
	if code != 1 || strings.Count(stdout, "cannot use typematic repeat") != 2 || !strings.Contains(stdout, `use mode \"tap\" to repeat clicks`) {
		t.Errorf("validate --json = %d:\n%s\nwant the typematic error of two bindings", code, stdout)
	}
}

// A stick that moves the pointer and the mouse actions (ADR-0062, section 5)
// validate; a precision action on a gesture that does not last while the
// control is held, an out-of-range speed and an incomplete jump are errors, also
// with the JSON output.
func TestValidateMousePointer(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "pointer.json")
	const goodProfile = `{
  "schemaVersion": 1,
  "name": "pointer",
  "analog": {
    "ls": {"mode": "mouse", "mouseSpeed": 1500, "mouseSmoothingMs": 80, "curve": "quadratic"}
  },
  "layers": {
    "base": {
      "leftshoulder": {"label": "慢速", "press": {"type": "mouse", "op": "precision", "factor": 0.25}},
      "rightshoulder": {"label": "长按慢速", "tap": {"type": "keys", "keys": "Z", "mode": "tap"}, "hold": {"type": "mouse", "op": "precision"}},
      "south": {"label": "回中", "press": {"type": "mouse", "op": "jump", "x": 0.5, "y": 0.5}}
    }
  }
}
`
	bad := filepath.Join(dir, "bad.json")
	const badProfile = `{
  "schemaVersion": 1,
  "name": "bad",
  "analog": {"rs": {"mode": "mouse", "mouseSpeed": 20}},
  "layers": {
    "base": {
      "east": {"press": {"type": "mouse", "op": "jump", "x": 0.5}}
    }
  }
}
`
	// The rules of the gestures come after the schema, so they are in a file of
	// their own.
	gesture := filepath.Join(dir, "gesture.json")
	const gestureProfile = `{
  "schemaVersion": 1,
  "name": "gesture",
  "layers": {
    "base": {
      "south": {"tap": {"type": "mouse", "op": "precision"}},
      "east": {"release": {"type": "mouse", "op": "precision", "factor": 0.5}}
    }
  }
}
`
	for path, text := range map[string]string{good: goodProfile, bad: badProfile, gesture: gestureProfile} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, stdout, stderr := runCmd(t, "validate", good)
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, good+": ok\n") {
		t.Fatalf("validate pointer profile = %d, stdout %q, stderr %q; want ok", code, stdout, stderr)
	}
	code, stdout, _ = runCmd(t, "validate", bad)
	if code != 1 {
		t.Fatalf("validate bad pointer profile = %d, want 1:\n%s", code, stdout)
	}
	for _, want := range []string{
		"minimum: got 20, want 50 (/analog/rs/mouseSpeed)",
		`missing property "y" (/layers/base/east/press)`,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("validate output lacks %q:\n%s", want, stdout)
		}
	}
	code, stdout, _ = runCmd(t, "validate", gesture)
	if code != 1 {
		t.Fatalf("validate gesture profile = %d, want 1:\n%s", code, stdout)
	}
	for _, want := range []string{
		"use it on press or hold, not on tap (/layers/base/south/tap/op)",
		"use it on press or hold, not on release (/layers/base/east/release/op)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("validate output lacks %q:\n%s", want, stdout)
		}
	}
	code, stdout, _ = runCmd(t, "validate", "--json", gesture)
	if code != 1 || !strings.Contains(stdout, "/layers/base/south/tap/op") {
		t.Errorf("validate --json = %d:\n%s", code, stdout)
	}
}

// A HUD hints action (HUD-03, ADR-0063) validates on a press or a hold; on a
// gesture that does not last while the control is held, with another operation
// or with a property of another action it is an error, also with the JSON
// output.
func TestValidateHUDHints(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "hints.json")
	const goodProfile = `{
  "schemaVersion": 1,
  "name": "hints",
  "layers": {
    "base": {
      "rightshoulder": {"label": "提示", "press": {"type": "hud", "op": "hints"}},
      "leftshoulder": {"label": "长按提示", "tap": {"type": "keys", "keys": "Z", "mode": "tap"}, "hold": {"type": "hud", "op": "hints"}}
    }
  }
}
`
	bad := filepath.Join(dir, "bad.json")
	const badProfile = `{
  "schemaVersion": 1,
  "name": "bad",
  "layers": {
    "base": {
      "east": {"press": {"type": "hud", "op": "map"}},
      "north": {"press": {"type": "hud", "op": "hints", "factor": 0.5}}
    }
  }
}
`
	// The rules of the gestures come after the schema, so they are in a file of
	// their own.
	gesture := filepath.Join(dir, "gesture.json")
	const gestureProfile = `{
  "schemaVersion": 1,
  "name": "gesture",
  "layers": {
    "base": {
      "south": {"tap": {"type": "hud", "op": "hints"}},
      "east": {"release": {"type": "hud", "op": "hints"}}
    }
  }
}
`
	for path, text := range map[string]string{good: goodProfile, bad: badProfile, gesture: gestureProfile} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, stdout, stderr := runCmd(t, "validate", good)
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, good+": ok\n") {
		t.Fatalf("validate hints profile = %d, stdout %q, stderr %q; want ok", code, stdout, stderr)
	}
	code, stdout, _ = runCmd(t, "validate", bad)
	if code != 1 {
		t.Fatalf("validate bad hints profile = %d, want 1:\n%s", code, stdout)
	}
	for _, want := range []string{
		`"map" is not a valid value; use "hints" (/layers/base/east/press/op)`,
		`unknown property "factor" (/layers/base/north/press/factor)`,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("validate output lacks %q:\n%s", want, stdout)
		}
	}
	code, stdout, _ = runCmd(t, "validate", gesture)
	if code != 1 {
		t.Fatalf("validate gesture profile = %d, want 1:\n%s", code, stdout)
	}
	for _, want := range []string{
		"a hints action shows the key hints while the control is held: use it on press or hold, not on tap (/layers/base/south/tap/op)",
		"use it on press or hold, not on release (/layers/base/east/release/op)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("validate output lacks %q:\n%s", want, stdout)
		}
	}
	code, stdout, _ = runCmd(t, "validate", "--json", gesture)
	if code != 1 || !strings.Contains(stdout, "/layers/base/south/tap/op") {
		t.Errorf("validate --json = %d:\n%s", code, stdout)
	}
}
