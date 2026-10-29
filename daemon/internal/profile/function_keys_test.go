package profile_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// The function key (MAP-27, ADR-0075): one control of back, start and guide,
// chosen in config.json. A profile's functionSet holds the bindings used while
// it is held, written like a layer and bindable on any control.
func TestFunctionSetParse(t *testing.T) {
	const src = `{
	"schemaVersion": 1, "name": "fn",
	"layers": {"base": {"south": {"press": {"type": "keys", "keys": "A"}}}, "paint": {}},
	"functionSet": {
		"south": {"label": "Redo", "press": {"type": "keys", "keys": "Ctrl+Shift+Z", "mode": "tap"}},
		"x": {"press": {"type": "keys", "keys": "B"}},
		"ls.cw": {"press": {"type": "keys", "keys": "Ctrl+]", "mode": "tap"}},
		"lt": {"block": true},
		"back": {"press": {"type": "keys", "keys": "Ctrl+V", "mode": "tap"}},
		"start": {"label": "Save"},
		"guide": {"press": {"type": "layer", "layer": "paint", "op": "toggle"}}
	}}`
	p, ds := profile.Parse([]byte(src))
	if p == nil || ds.HasErrors() {
		t.Fatalf("errors: %v", ds)
	}
	set := p.FunctionSet
	if set == nil || p.FunctionKeys != nil {
		t.Fatalf("function set %v, function keys %v", set, p.FunctionKeys)
	}
	if set.Name != profile.FunctionSetName || profile.FunctionSetName != "function" {
		t.Errorf("set name %q", set.Name)
	}
	for _, c := range []control.Control{control.Back, control.Start, control.Guide} {
		if want := "function:" + c.String(); profile.FunctionLevel(c) != want {
			t.Errorf("FunctionLevel(%s) = %q, want %q", c, profile.FunctionLevel(c), want)
		}
	}
	if profile.FunctionLevel(control.South) != "" || profile.FunctionLevel(control.Invalid) != "" {
		t.Errorf("FunctionLevel of a control that cannot be the function key: %q %q", profile.FunctionLevel(control.South), profile.FunctionLevel(control.Invalid))
	}
	// The bindings are in control order; the ID keeps the name as written.
	var got []string
	for _, b := range set.Bindings {
		got = append(got, b.Control.String()+" "+b.ID)
	}
	want := []string{
		"south /functionSet/south",
		"west /functionSet/x",
		"back /functionSet/back",
		"guide /functionSet/guide",
		"start /functionSet/start",
		"lt /functionSet/lt",
		"ls.cw /functionSet/ls.cw",
	}
	if !slices.Equal(got, want) {
		t.Errorf("bindings %q, want %q", got, want)
	}
	if b := set.Binding(control.LT); b == nil || !b.Block {
		t.Errorf("lt: %+v, want a block binding", b)
	}
	if b := set.Binding(control.LSCW); b == nil || b.Press == nil || b.Press.ID != "/functionSet/ls.cw/press" {
		t.Errorf("ls.cw: %+v", b)
	}
	if b := set.Binding(control.Guide); b == nil || b.Press == nil || b.Press.Type != profile.ActionLayer {
		t.Errorf("guide: %+v", b)
	}
	if p.Layer("base").Binding(control.Back) != nil || p.Layer("function") != nil || p.Layer("function:start") != nil {
		t.Error("a function set is not a layer")
	}
	if sets := p.FunctionSets(); len(sets) != 1 || sets[0] != set {
		t.Errorf("FunctionSets() = %v", sets)
	}
	if p.EffectiveFunctionSet() != set {
		t.Error("the effective function set is not the functionSet")
	}
	var none profile.Profile
	if none.EffectiveFunctionSet() != nil || len(none.FunctionSets()) != 0 {
		t.Error("a profile with no function set has one")
	}
}

// The old format stays valid: its two sets are merged into the one set, start's
// first, then back's for the controls start's does not bind (ADR-0075).
func TestFunctionKeysParseAndMerge(t *testing.T) {
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
			"a": {"label": "Fill", "press": {"type": "keys", "keys": "F"}},
			"dpad.up": {"press": {"type": "layer", "layer": "paint", "op": "toggle"}}
		}
	}}`
	p, ds := profile.Parse([]byte(src))
	if p == nil || ds.HasErrors() || len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	if p.FunctionKeys == nil || p.FunctionSet != nil {
		t.Fatalf("function keys %v, function set %v", p.FunctionKeys, p.FunctionSet)
	}
	back, start := p.FunctionKeys.Back, p.FunctionKeys.Start
	if back == nil || start == nil || back.Name != "function:back" || start.Name != "function:start" {
		t.Fatalf("sets %+v and %+v", back, start)
	}
	if sets := p.FunctionSets(); len(sets) != 2 || sets[0] != back || sets[1] != start {
		t.Errorf("FunctionSets() = %v", sets)
	}
	set := p.EffectiveFunctionSet()
	if set == nil || set.Name != profile.FunctionSetName {
		t.Fatalf("effective set %+v", set)
	}
	var got []string
	for _, b := range set.Bindings {
		got = append(got, b.Control.String()+" "+b.ID)
	}
	// south: start's (a, the alias) wins over back's; back's x, lt and ls.cw fill the gaps.
	want := []string{
		"south /functionKeys/start/a",
		"west /functionKeys/back/x",
		"dpad.up /functionKeys/start/dpad.up",
		"lt /functionKeys/back/lt",
		"ls.cw /functionKeys/back/ls.cw",
	}
	if !slices.Equal(got, want) {
		t.Errorf("effective bindings %q, want %q", got, want)
	}
	if b := set.Binding(control.South); b == nil || b.Label != "Fill" {
		t.Errorf("south: %+v", b)
	}
	// Each of the two alone is the set.
	for _, only := range []string{"back", "start"} {
		q, ds := profile.Parse([]byte(`{"schemaVersion":1,"name":"x","layers":{"base":{}},"functionKeys":{"` + only + `":{"south":{"press":{"type":"keys","keys":"A"}}}}}`))
		if q == nil || ds.HasErrors() {
			t.Fatal(ds)
		}
		if s := q.EffectiveFunctionSet(); s == nil || len(s.Bindings) != 1 || s.Bindings[0].ID != "/functionKeys/"+only+"/south" {
			t.Errorf("only %s: effective set %+v", only, s)
		}
	}
	// An empty functionKeys has no set.
	q, ds := profile.Parse([]byte(`{"schemaVersion":1,"name":"x","layers":{"base":{}},"functionKeys":{}}`))
	if q == nil || ds.HasErrors() || q.EffectiveFunctionSet() != nil {
		t.Errorf("empty functionKeys: %v, %+v", ds, q)
	}
}

// With both formats the new one is used and the old one is ignored, with one
// warning; it is still read, so a mistake in it is still reported.
func TestFunctionSetOverFunctionKeys(t *testing.T) {
	const src = `{"schemaVersion": 1, "name": "fn", "layers": {"base": {}},
	"functionKeys": {"start": {"south": {"press": {"type": "keys", "keys": "A"}}}},
	"functionSet": {"east": {"press": {"type": "keys", "keys": "B"}}}}`
	p, ds := profile.Parse([]byte(src))
	if p == nil || ds.HasErrors() {
		t.Fatalf("diagnostics %v", ds)
	}
	if want := []string{`2:2: warning: functionKeys is ignored because functionSet is also written (ADR-0075) (/functionKeys)`}; len(ds) != 1 || ds[0].String() != want[0] {
		t.Errorf("diagnostics %v, want %q", ds, want)
	}
	set := p.EffectiveFunctionSet()
	if set != p.FunctionSet || set.Binding(control.East) == nil || set.Binding(control.South) != nil {
		t.Errorf("effective set %+v", set)
	}
	// The ignored one is checked like any set.
	q, ds := profile.Parse([]byte(strings.Replace(src, `"keys": "A"`, `"keys": "NoSuchKey"`, 1)))
	if q != nil || !ds.HasErrors() {
		t.Errorf("a bad key in the ignored functionKeys was not an error: %v", ds)
	}
}

// Both aliases of one control in one set is the error a layer reports.
func TestFunctionSetDuplicateAlias(t *testing.T) {
	for _, set := range []string{`"functionSet": {"south": {}, "a": {}}`, `"functionKeys": {"back": {"south": {}, "a": {}}}`} {
		src := `{"schemaVersion": 1, "name": "fn", "layers": {"base": {}}, ` + set + `}`
		p, ds := profile.Parse([]byte(src))
		if p != nil || !strings.Contains(diagText(ds), `"a" is already bound as "south"`) {
			t.Errorf("%s: profile %v, diagnostics %v", set, p != nil, ds)
		}
	}
}

// back, start and guide are bindable in any layer or in common: which of them
// is the function key is config.json's choice, not known when a profile is
// read, so a binding on one is kept and raises no warning (ADR-0075; ADR-0069
// dropped the ones on back and start).
func TestCenterKeysAreBindableInLayers(t *testing.T) {
	const src = `{
	"schemaVersion": 1, "name": "old",
	"layers": {
		"base": {
			"south": {"press": {"type": "keys", "keys": "A"}},
			"back": {"press": {"type": "keys", "keys": "Ctrl+V", "mode": "tap"}},
			"start": {"label": "Save", "press": {"type": "keys", "keys": "Ctrl+S", "mode": "tap"}},
			"guide": {"press": {"type": "keys", "keys": "G"}}
		},
		"paint": {"select": {"press": {"type": "keys", "keys": "Q"}}}
	},
	"common": {"start": {"block": true}}}`
	p, ds := profile.Parse([]byte(src))
	if p == nil || ds.HasErrors() || len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	if n := len(p.Layer("base").Bindings); n != 4 {
		t.Errorf("base has %d bindings, want 4", n)
	}
	for _, c := range []control.Control{control.Back, control.Start, control.Guide} {
		if p.Layer("base").Binding(c) == nil {
			t.Errorf("base lost its binding of %s", c)
		}
	}
	if b := p.Layer("paint").Binding(control.Back); b == nil || b.ID != "/layers/paint/select" {
		t.Errorf("paint: select is back: %+v", b)
	}
	if b := p.Common.Binding(control.Start); b == nil || !b.Block {
		t.Errorf("common: %+v", b)
	}
	// guide was never dropped, so a binding on it that is not valid is an error.
	_, ds = profile.Parse([]byte(`{"schemaVersion": 1, "name": "old", "layers": {"base": {
		"guide": {"press": {"type": "layer", "layer": "nonesuch", "op": "hold"}}}}}`))
	if !strings.Contains(diagText(ds), `unknown layer "nonesuch"`) || !ds.HasErrors() {
		t.Errorf("diagnostics %v", ds)
	}
}

// ADR-0069 dropped a binding on back or start in a layer or in common unread,
// so a file that was valid may hold one that is not, and the format is frozen:
// such a file stays valid. Parse leaves the binding out of the model with a
// warning (so Compile never sees it, whichever control is the function key),
// and keeps the ones that are valid. A binding on guide, which was never
// dropped, and one in the function set, which is new, are still errors.
func TestInvalidBindingsOnBackAndStartAreLeftOut(t *testing.T) {
	for _, tc := range []struct{ name, in, binding, id, want string }{
		{"unknown layer", `"layers": {"base": {%s}}`, `"start": {"press": {"type": "layer", "layer": "no such layer", "op": "hold"}}`, "/layers/base/start/press/layer", `unknown layer "no such layer"`},
		{"key that does not parse", `"layers": {"base": {%s}}`, `"back": {"press": {"type": "keys", "keys": "Ctrl+Nonsense"}}`, "/layers/base/back/press/keys", "Nonsense"},
		{"alias", `"layers": {"base": {%s}}`, `"select": {"press": {"type": "keys", "keys": "Ctrl+Nonsense"}}`, "/layers/base/select/press/keys", "Nonsense"},
		{"block with an action", `"layers": {"base": {%s}}`, `"start": {"block": true, "press": {"type": "keys", "keys": "A"}}`, "/layers/base/start", "block cannot have an action"},
		{"repeated layer", `"layers": {"base": {%s}, "paint": {}}`, `"start": {"press": {"type": "layer", "layers": ["paint", "paint"], "op": "cycle"}}`, "/layers/base/start/press/layers", `repeated layer "paint"`},
		{"hold on a tap", `"layers": {"base": {%s}}`, `"back": {"tap": {"type": "keys", "keys": "A", "mode": "hold"}}`, "/layers/base/back/tap/mode", "runs after the control is released"},
		{"script without a script", `"layers": {"base": {%s}}`, `"back": {"press": {"type": "script", "name": "x"}}`, "/layers/base/back/press", "script action requires"},
		{"in common", `"layers": {"base": {}}, "common": {%s}`, `"back": {"press": {"type": "layer", "layer": "no such layer", "op": "hold"}}`, "/common/back/press/layer", `unknown layer "no such layer"`},
	} {
		src := `{"schemaVersion": 1, "name": "old", ` + strings.Replace(tc.in, "%s", tc.binding+`, "south": {"press": {"type": "keys", "keys": "A"}}`, 1) + `}`
		p, ds := profile.Parse([]byte(src))
		if p == nil || ds.HasErrors() {
			t.Errorf("%s: profile %v, diagnostics %v", tc.name, p != nil, ds)
			continue
		}
		var warned bool
		for _, d := range ds {
			if d.Pointer == tc.id && d.Severity == profile.SeverityWarning && strings.Contains(d.Message, tc.want) && strings.Contains(d.Message, "left out") {
				warned = true
			}
		}
		if !warned {
			t.Errorf("%s: diagnostics %v; want a warning at %s with %q", tc.name, ds, tc.id, tc.want)
		}
		south := 0
		for _, l := range append(slices.Clone(p.Layers), p.Common) {
			if l == nil {
				continue
			}
			for _, c := range []control.Control{control.Back, control.Start} {
				if l.Binding(c) != nil {
					t.Errorf("%s: %s keeps a binding of %s", tc.name, l.Name, c)
				}
			}
			if l.Binding(control.South) != nil {
				south++
			}
		}
		if south != 1 {
			t.Errorf("%s: the valid binding of south is in %d layers, want 1", tc.name, south)
		}
		for _, fn := range []control.Control{control.Back, control.Start, control.Guide} {
			if _, err := engine.Compile(p, engine.WithFunctionKey(fn)); err != nil {
				t.Errorf("%s: Compile with function key %s: %v", tc.name, fn, err)
			}
		}
	}
	// Valid bindings next to the invalid one stay, and a valid one is no
	// warning at all.
	p, ds := profile.Parse([]byte(`{"schemaVersion": 1, "name": "old", "layers": {"base": {
		"back": {"press": {"type": "keys", "keys": "Ctrl+V", "mode": "tap"}},
		"start": {"press": {"type": "layer", "layer": "no such layer", "op": "hold"}}}}}`))
	if p == nil || ds.HasErrors() || len(ds) != 1 {
		t.Fatalf("diagnostics %v", ds)
	}
	if l := p.Layer("base"); l.Binding(control.Back) == nil || l.Binding(control.Start) != nil || len(l.Bindings) != 1 {
		t.Errorf("base: %+v", l.Bindings)
	}
	// New things are strict: the function set, and guide.
	for _, in := range []string{
		`"layers": {"base": {}}, "functionSet": {"start": {"press": {"type": "layer", "layer": "no such layer", "op": "hold"}}}`,
		`"layers": {"base": {}}, "functionSet": {"back": {"press": {"type": "keys", "keys": "Ctrl+Nonsense"}}}`,
		`"layers": {"base": {"guide": {"press": {"type": "keys", "keys": "Ctrl+Nonsense"}}}}`,
	} {
		if q, ds := profile.Parse([]byte(`{"schemaVersion": 1, "name": "x", ` + in + `}`)); q != nil && !ds.HasErrors() {
			t.Errorf("%s: no error, diagnostics %v", in, ds)
		}
	}
}

// A function set is checked like a layer: its layer actions name layers that
// exist, a block has no action, a script action needs a script. The same for the
// sets of the old format.
func TestFunctionSetCheckBindings(t *testing.T) {
	for _, format := range []struct{ name, open, close, where string }{
		{"functionSet", `"functionSet": {`, `}`, "/functionSet"},
		{"functionKeys", `"functionKeys": {"back": {`, `}}`, "/functionKeys/back"},
	} {
		for _, tc := range []struct{ name, set, want string }{
			{"unknown layer", `"south": {"press": {"type": "layer", "layer": "nonesuch", "op": "hold"}}`, `unknown layer "nonesuch" (@/south/press/layer)`},
			{"known layer", `"south": {"press": {"type": "layer", "layer": "paint", "op": "hold"}}`, ""},
			{"block with an action", `"south": {"block": true, "press": {"type": "keys", "keys": "A"}}`, "block cannot have an action (@/south)"},
			{"script action", `"south": {"press": {"type": "script", "name": "x"}}`, "script action requires the profile's script configuration (@/south/press)"},
			{"momentary hold", `"south": {"tap": {"type": "keys", "keys": "A", "mode": "hold"}}`, "(@/south/tap"},
			{"no action", `"south": {"label": "x"}`, "warning: binding has no action (@/south)"},
			{"unknown macro file", `"south": {"press": {"type": "macro", "file": "a/b.ts"}}`, "invalid macro file name"},
		} {
			src := `{"schemaVersion": 1, "name": "fn", "layers": {"base": {}, "paint": {}}, ` + format.open + tc.set + format.close + `}`
			p, ds := profile.Parse([]byte(src))
			want := strings.ReplaceAll(tc.want, "@", format.where)
			if tc.want == "" {
				if p == nil || len(ds) > 0 {
					t.Errorf("%s %s: profile %v, diagnostics %v", format.name, tc.name, p != nil, ds)
				}
				continue
			}
			if !strings.Contains(diagText(ds), want) {
				t.Errorf("%s %s: diagnostics %v; want one with %q", format.name, tc.name, ds, want)
			}
		}
	}
}

// The schema keeps back and start out of the sets of the old functionKeys
// (functionSet has them), and a set has no color: it is not a layer.
func TestFunctionKeysSchemaMessages(t *testing.T) {
	for _, tc := range []struct{ name, set, want string }{
		{"back", `"back": {}`, `"back" cannot be bound in a functionKeys set (ADR-0069); functionSet (ADR-0075) can (/functionKeys/back/back)`},
		{"start", `"start": {}`, `"start" cannot be bound in a functionKeys set (ADR-0069); functionSet (ADR-0075) can (/functionKeys/back/start)`},
		{"alias", `"select": {}`, `"select" cannot be bound in a functionKeys set (ADR-0069); functionSet (ADR-0075) can (/functionKeys/back/select)`},
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

// The messages of functionSet: no color, a typo gets its correction, and the
// names printed on the pad get the control they mean.
func TestFunctionSetSchemaMessages(t *testing.T) {
	for _, tc := range []struct{ name, set, want string }{
		{"color", `"color": "#FF0000"`, `a function set has no color: it is not a layer (/functionSet/color)`},
		{"typo", `"southh": {}`, `unknown control "southh"; did you mean "south"? (/functionSet/southh)`},
		{"typo of a center key", `"bak": {}`, `unknown control "bak"; did you mean "back"? (/functionSet/bak)`},
		{"name printed on a pad", `"menu": {}`, `unknown control "menu"; did you mean "start"? (/functionSet/menu)`},
		{"binding not an object", `"south": []`, `expected a binding object`},
	} {
		src := `{"schemaVersion": 1, "name": "fn", "layers": {"base": {}}, "functionSet": {` + tc.set + `}}`
		p, ds := profile.Parse([]byte(src))
		if p != nil || !strings.Contains(diagText(ds), tc.want) {
			t.Errorf("%s: profile %v, diagnostics %v; want one with %q", tc.name, p != nil, ds, tc.want)
		}
	}
	p, ds := profile.Parse([]byte(`{"schemaVersion": 1, "name": "fn", "layers": {"base": {}}, "functionSet": []}`))
	if p != nil || !strings.Contains(diagText(ds), `expected an object`) {
		t.Errorf("a functionSet that is no object: %v", ds)
	}
}

// Every name printed on a pad for the two function keys of the old format, in
// any case, gets the function key it means, not the nearest spelling or none.
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

// The sets are the same schema as a layer: every control (and no color) in the
// order of the layer's; a set of the old format also leaves out back, start and
// select, which functionSet has.
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
	var withoutColor, withoutCenter []string
	for _, name := range layer {
		if name == "color" {
			continue
		}
		withoutColor = append(withoutColor, name)
		if name == "back" || name == "start" || name == "select" {
			continue
		}
		withoutCenter = append(withoutCenter, name)
	}
	if set := schemaKeys(t, s.Defs["functionSet"].Properties); !slices.Equal(set, withoutCenter) {
		t.Errorf("function key set properties\n%q\nwant the layer's without color, back, start and select\n%q", set, withoutCenter)
	}
	if set := schemaKeys(t, s.Defs["functionBindings"].Properties); !slices.Equal(set, withoutColor) {
		t.Errorf("functionSet properties\n%q\nwant the layer's without color\n%q", set, withoutColor)
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

// The macros that the function sets run are found, renamed and removed like the
// macros of a layer, and a missing one warns with its own pointer.
func TestFunctionSetMacros(t *testing.T) {
	for _, format := range []struct{ name, set, pointer string }{
		{"functionSet", `"functionSet": {"south": {"press": {"type": "macro", "file": "x.ts"}}}`, "/functionSet/south/press/file"},
		{"functionKeys", `"functionKeys": {"start": {"south": {"press": {"type": "macro", "file": "x.ts"}}}}`, "/functionKeys/start/south/press/file"},
	} {
		t.Run(format.name, func(t *testing.T) {
			src := `{"schemaVersion": 1, "name": "fn", "layers": {"base": {}}, ` + format.set + `}`
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
			if len(missing) != 1 || missing[0].Pointer != format.pointer {
				t.Errorf("MissingMacros: %v", missing)
			}
		})
	}
}
