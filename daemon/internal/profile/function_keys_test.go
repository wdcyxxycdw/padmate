package profile_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// The function keys (MAP-27, ADR-0069): back and start are function keys in
// every profile. A profile's functionKeys object holds one set of bindings
// for each, written like a layer.
func TestFunctionKeysParse(t *testing.T) {
	const src = `{
	"schemaVersion": 1, "name": "fn",
	"layers": {"base": {"south": {"press": {"type": "keys", "keys": "A"}}}, "paint": {}},
	"functionKeys": {
		"back": {
			"south": {"label": "Redo", "press": {"type": "keys", "keys": "Ctrl+Shift+Z", "mode": "tap"}},
			"x": {"press": {"type": "keys", "keys": "B"}},
			"ls.cw": {"press": {"type": "keys", "keys": "Ctrl+]", "mode": "tap"}},
			"lt": {"block": true}
		},
		"start": {
			"dpad.up": {"press": {"type": "layer", "layer": "paint", "op": "toggle"}}
		}
	}}`
	p, ds := profile.Parse([]byte(src))
	if p == nil || ds.HasErrors() {
		t.Fatalf("errors: %v", ds)
	}
	if p.FunctionKeys == nil {
		t.Fatal("no function keys")
	}
	back, start := p.FunctionKeys.Set(control.Back), p.FunctionKeys.Set(control.Start)
	if back == nil || start == nil || back != p.FunctionKeys.Back || start != p.FunctionKeys.Start {
		t.Fatalf("sets %v and %v", back, start)
	}
	if back.Name != "function:back" || start.Name != "function:start" {
		t.Errorf("set names %q and %q", back.Name, start.Name)
	}
	if profile.FunctionLevel(control.Back) != "function:back" || profile.FunctionLevel(control.Start) != "function:start" || profile.FunctionLevel(control.South) != "" {
		t.Errorf("FunctionLevel: %q %q %q", profile.FunctionLevel(control.Back), profile.FunctionLevel(control.Start), profile.FunctionLevel(control.South))
	}
	// The bindings are in control order; the ID keeps the name as written.
	var got []string
	for _, b := range back.Bindings {
		got = append(got, b.Control.String()+" "+b.ID)
	}
	want := []string{
		"south /functionKeys/back/south",
		"west /functionKeys/back/x",
		"lt /functionKeys/back/lt",
		"ls.cw /functionKeys/back/ls.cw",
	}
	if !slices.Equal(got, want) {
		t.Errorf("bindings %q, want %q", got, want)
	}
	if b := back.Binding(control.LT); b == nil || !b.Block {
		t.Errorf("lt: %+v, want a block binding", b)
	}
	if b := back.Binding(control.LSCW); b == nil || b.Press == nil || b.Press.ID != "/functionKeys/back/ls.cw/press" {
		t.Errorf("ls.cw: %+v", b)
	}
	if b := start.Binding(control.DpadUp); b == nil || b.Press == nil || b.Press.Type != profile.ActionLayer {
		t.Errorf("dpad.up: %+v", b)
	}
	if p.Layer("base").Binding(control.Back) != nil || p.Layer("function:back") != nil {
		t.Error("a function key set is not a layer")
	}
	if sets := p.FunctionSets(); len(sets) != 2 || sets[0] != back || sets[1] != start {
		t.Errorf("FunctionSets() = %v", sets)
	}
	var none *profile.FunctionKeys
	if none.Set(control.Back) != nil || (&profile.FunctionKeys{}).Set(control.Start) != nil || p.FunctionKeys.Set(control.South) != nil {
		t.Error("Set of a missing set, or of a control that is not a function key, is not nil")
	}
}

// Both aliases of one control in one set is the error a layer reports.
func TestFunctionKeysDuplicateAlias(t *testing.T) {
	const src = `{"schemaVersion": 1, "name": "fn", "layers": {"base": {}},
	"functionKeys": {"back": {"south": {}, "a": {}}}}`
	p, ds := profile.Parse([]byte(src))
	if p != nil || !strings.Contains(diagText(ds), `"a" is already bound as "south"`) {
		t.Errorf("profile %v, diagnostics %v", p != nil, ds)
	}
}

// back and start are not bound in any layer or in common: a binding there
// parses as nothing, with one warning each, and the profile loads (ADR-0069).
func TestFunctionKeysIgnoreBindingsOnThem(t *testing.T) {
	const src = `{
	"schemaVersion": 1, "name": "old",
	"layers": {
		"base": {
			"south": {"press": {"type": "keys", "keys": "A"}},
			"back": {"press": {"type": "keys", "keys": "Ctrl+V", "mode": "tap"}},
			"start": {"label": "Save"}
		},
		"paint": {"select": {"press": {"type": "keys", "keys": "Q"}}}
	},
	"common": {"start": {"block": true}}}`
	p, ds := profile.Parse([]byte(src))
	if p == nil || ds.HasErrors() {
		t.Fatalf("errors: %v", ds)
	}
	for _, l := range append(slices.Clone(p.Layers), p.Common) {
		for _, b := range l.Bindings {
			if b.Control.IsFunctionKey() {
				t.Errorf("layer %s keeps a binding of %s", l.Name, b.Control)
			}
		}
	}
	if n := len(p.Layer("base").Bindings); n != 1 {
		t.Errorf("base has %d bindings, want south only", n)
	}
	want := []string{
		`6:4: warning: back is a function key (ADR-0069): this binding has no effect (/layers/base/back)`,
		`7:4: warning: start is a function key (ADR-0069): this binding has no effect (/layers/base/start)`,
		`9:13: warning: select is an alias of back, a function key (ADR-0069): this binding has no effect (/layers/paint/select)`,
		`11:13: warning: start is a function key (ADR-0069): this binding has no effect (/common/start)`,
	}
	var got []string
	for _, d := range ds {
		if d.Severity != profile.SeverityWarning {
			t.Errorf("diagnostic %v is not a warning", d)
		}
		got = append(got, d.String())
	}
	if !slices.Equal(got, want) {
		t.Errorf("diagnostics\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// An ignored binding is not checked either: a layer action to a layer that
// does not exist is no error on a binding that does nothing.
func TestFunctionKeysIgnoredBindingIsNotChecked(t *testing.T) {
	const src = `{"schemaVersion": 1, "name": "old", "layers": {"base": {
		"back": {"press": {"type": "layer", "layer": "nonesuch", "op": "hold"}},
		"start": {"press": {"type": "script", "name": "x"}}}}}`
	p, ds := profile.Parse([]byte(src))
	if p == nil || ds.HasErrors() || len(ds) != 2 {
		t.Fatalf("profile %v, diagnostics %v", p != nil, ds)
	}
}

// A function key's set is checked like a layer: its layer actions name
// layers that exist, a block has no action, a script action needs a script.
func TestFunctionKeysCheckBindings(t *testing.T) {
	for _, tc := range []struct{ name, set, want string }{
		{"unknown layer", `"south": {"press": {"type": "layer", "layer": "nonesuch", "op": "hold"}}`, `unknown layer "nonesuch" (/functionKeys/back/south/press/layer)`},
		{"known layer", `"south": {"press": {"type": "layer", "layer": "paint", "op": "hold"}}`, ""},
		{"block with an action", `"south": {"block": true, "press": {"type": "keys", "keys": "A"}}`, "block cannot have an action (/functionKeys/back/south)"},
		{"script action", `"south": {"press": {"type": "script", "name": "x"}}`, "script action requires the profile's script configuration (/functionKeys/back/south/press)"},
		{"momentary hold", `"south": {"tap": {"type": "keys", "keys": "A", "mode": "hold"}}`, "(/functionKeys/back/south/tap"},
		{"no action", `"south": {"label": "x"}`, "warning: binding has no action (/functionKeys/back/south)"},
		{"unknown macro file", `"south": {"press": {"type": "macro", "file": "a/b.ts"}}`, "invalid macro file name"},
	} {
		src := `{"schemaVersion": 1, "name": "fn", "layers": {"base": {}, "paint": {}}, "functionKeys": {"back": {` + tc.set + `}}}`
		p, ds := profile.Parse([]byte(src))
		if tc.want == "" {
			if p == nil || len(ds) > 0 {
				t.Errorf("%s: profile %v, diagnostics %v", tc.name, p != nil, ds)
			}
			continue
		}
		if !strings.Contains(diagText(ds), tc.want) {
			t.Errorf("%s: diagnostics %v; want one with %q", tc.name, ds, tc.want)
		}
	}
}

// The schema keeps the function keys out of their own sets, and a set has
// no color: it is not a layer.
func TestFunctionKeysSchemaMessages(t *testing.T) {
	for _, tc := range []struct{ name, set, want string }{
		{"back", `"back": {}`, `"back" is a function key (ADR-0069); a function key set cannot bind it (/functionKeys/back/back)`},
		{"start", `"start": {}`, `"start" is a function key (ADR-0069); a function key set cannot bind it (/functionKeys/back/start)`},
		{"alias", `"select": {}`, `"select" is a function key (ADR-0069); a function key set cannot bind it (/functionKeys/back/select)`},
		{"color", `"color": "#FF0000"`, `a function key set has no color: it is not a layer (/functionKeys/back/color)`},
		{"typo", `"southh": {}`, `unknown control "southh"; did you mean "south"? (/functionKeys/back/southh)`},
	} {
		src := `{"schemaVersion": 1, "name": "fn", "layers": {"base": {}}, "functionKeys": {"back": {` + tc.set + `}}}`
		p, ds := profile.Parse([]byte(src))
		if p != nil || !strings.Contains(diagText(ds), tc.want) {
			t.Errorf("%s: profile %v, diagnostics %v; want one with %q", tc.name, p != nil, ds, tc.want)
		}
	}
	for _, tc := range []struct{ name, fk, want string }{
		{"other key", `{"guide": {}}`, `unknown property "guide"; only "back" and "start" are function keys (/functionKeys/guide)`},
		{"other control", `{"south": {}}`, `unknown property "south"; only "back" and "start" are function keys (/functionKeys/south)`},
		{"name printed on the pad", `{"menu": {}}`, `unknown property "menu"; did you mean "start"? (/functionKeys/menu)`},
		{"name printed on the pad, other case", `{"View": {}}`, `unknown property "View"; did you mean "back"? (/functionKeys/View)`},
		{"alias", `{"select": {}}`, `unknown property "select"; did you mean "back"? (/functionKeys/select)`},
		{"typo", `{"bak": {}}`, `unknown property "bak"; did you mean "back"? (/functionKeys/bak)`},
		{"other case", `{"Start": {}}`, `unknown property "Start"; did you mean "start"? (/functionKeys/Start)`},
		{"not an object", `[]`, `expected an object`},
		{"set not an object", `{"back": []}`, `expected an object`},
	} {
		src := `{"schemaVersion": 1, "name": "fn", "layers": {"base": {}}, "functionKeys": ` + tc.fk + `}`
		p, ds := profile.Parse([]byte(src))
		if p != nil || !strings.Contains(diagText(ds), tc.want) {
			t.Errorf("%s: profile %v, diagnostics %v; want one with %q", tc.name, p != nil, ds, tc.want)
		}
	}
}

// Every name printed on a pad for the two function keys, in any case, gets the
// function key it means, not the nearest spelling or none.
func TestFunctionKeysNicknames(t *testing.T) {
	for nick, want := range map[string]string{
		"view": "back", "share": "back", "create": "back", "minus": "back", "select": "back",
		"menu": "start", "options": "start", "plus": "start",
	} {
		for _, name := range []string{nick, strings.ToUpper(nick)} {
			src := `{"schemaVersion": 1, "name": "fn", "layers": {"base": {}}, "functionKeys": {"` + name + `": {}}}`
			p, ds := profile.Parse([]byte(src))
			if w := `unknown property "` + name + `"; did you mean "` + want + `"? (/functionKeys/` + name + `)`; p != nil || !strings.Contains(diagText(ds), w) {
				t.Errorf("%s: profile %v, diagnostics %v; want one with %q", name, p != nil, ds, w)
			}
		}
	}
}

// A function key's set is the same schema as a layer: every control but the
// function keys (and select, an alias of back) and no color, in the order of
// the layer's.
func TestSchemaFunctionSetControls(t *testing.T) {
	var s struct {
		Defs map[string]struct {
			Properties json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(profile.Schema(), &s); err != nil {
		t.Fatal(err)
	}
	layer := schemaKeys(t, s.Defs["layer"].Properties)
	set := schemaKeys(t, s.Defs["functionSet"].Properties)
	var want []string
	for _, name := range layer {
		if name == "color" || name == "back" || name == "start" || name == "select" {
			continue
		}
		want = append(want, name)
	}
	if !slices.Equal(set, want) {
		t.Errorf("function set properties\n%q\nwant the layer's without color, back, start and select\n%q", set, want)
	}
}

func schemaKeys(t *testing.T, obj json.RawMessage) []string {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(obj))
	if tok, err := d.Token(); err != nil || tok != json.Delim('{') {
		t.Fatal("not a JSON object")
	}
	var keys []string
	for d.More() {
		tok, err := d.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var v json.RawMessage
		if err := d.Decode(&v); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

// The macros that the function keys' sets run are found, renamed and removed
// like the macros of a layer, and a missing one warns with its own pointer.
func TestFunctionKeysMacros(t *testing.T) {
	const src = `{"schemaVersion": 1, "name": "fn", "layers": {"base": {}},
	"functionKeys": {"start": {"south": {"press": {"type": "macro", "file": "x.ts"}}}}}`
	refs, err := profile.MacroRefs([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if uses := refs["x.ts"]; len(uses) != 1 || uses[0].Control != control.South || uses[0].Gesture != profile.GesturePress {
		t.Errorf("MacroRefs: %v", refs)
	}
	p, ds := profile.Parse([]byte(src))
	if p == nil || ds.HasErrors() {
		t.Fatal(ds)
	}
	if files := p.MacroFiles(); !slices.Equal(files, []string{"x.ts"}) {
		t.Errorf("MacroFiles() = %v", files)
	}
	out, n, err := profile.RenameMacroRefs([]byte(src), "x.ts", "y.ts")
	if err != nil || n != 1 || !strings.Contains(string(out), `"file": "y.ts"`) {
		t.Errorf("RenameMacroRefs: %d, %v, %s", n, err, out)
	}
	out, n, err = profile.RemoveMacroRefs([]byte(src), "x.ts")
	if err != nil || n != 1 || strings.Contains(string(out), "x.ts") {
		t.Errorf("RemoveMacroRefs: %d, %v, %s", n, err, out)
	}
	if q, ds := profile.Parse(out); q == nil || ds.HasErrors() {
		t.Errorf("the profile without the macro: %v", ds)
	}
	missing := profile.MissingMacros([]byte(src), func(string) bool { return false })
	if len(missing) != 1 || missing[0].Pointer != "/functionKeys/start/south/press/file" {
		t.Errorf("MissingMacros: %v", missing)
	}
}
