package profile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// fuzzSeeds are small inputs that once broke an invariant, or that reach
// corners the fixtures do not.
var fuzzSeeds = map[string]string{
	"profile action":  `{"schemaVersion":0,"name":"switch","layers":{"base":{"south":{"press":{"type":"profile","profile":"paint"}}}}}`,
	"script action":   `{"schemaVersion":0,"name":"script","script":{"entry":"main.ts","api":"0.21"},"layers":{"base":{"south":{"press":{"type":"script","name":"tool"}}}}}`,
	"macro action":    `{"schemaVersion":0,"name":"macro","layers":{"base":{"south":{"press":{"type":"macro","file":"复制 (1).ts"}}}}}`,
	"mouse actions":   `{"schemaVersion":1,"name":"m","analog":{"ls":{"mode":"mouse","mouseSpeed":900,"mouseSmoothingMs":50}},"layers":{"base":{"south":{"press":{"type":"mouse","op":"precision","factor":0.5},"tap":{"type":"mouse","op":"jump","x":0,"y":1}},"east":{"hold":{"type":"mouse","op":"precision"}}}}}`,
	"mouse bad":       `{"schemaVersion":1,"name":"m","layers":{"base":{"south":{"tap":{"type":"mouse","op":"precision"},"press":{"type":"mouse","op":"jump","x":2}}}}}`,
	"hud actions":     `{"schemaVersion":1,"name":"h","layers":{"base":{"south":{"press":{"type":"hud","op":"hints"}},"east":{"hold":{"type":"hud","op":"hints"}}}}}`,
	"hud bad":         `{"schemaVersion":1,"name":"h","layers":{"base":{"south":{"tap":{"type":"hud","op":"hints"},"press":{"type":"hud","op":"hints","factor":1}}}}}`,
	"sequence action": `{"schemaVersion":1,"name":"s","layers":{"base":{"south":{"press":{"type":"sequence","steps":[{"keys":"Ctrl+A"},{"wait":100},{"text":"你好\n"}]},"tap":{"type":"sequence","steps":[{"wait":10000}]}}}}}`,
	"sequence bad":    `{"schemaVersion":1,"name":"s","layers":{"base":{"south":{"press":{"type":"sequence","steps":[{},{"keys":"A","wait":5},{"wait":0},{"wait":1.5},{"keys":"Z+Ctrl"},{"text":"a\rb"},"A"]}}}}}`,
	"sequence empty":  `{"schemaVersion":1,"name":"s","layers":{"base":{"south":{"press":{"type":"sequence","steps":[]},"tap":{"type":"sequence"}}}}}`,
	"text action":     `{"schemaVersion":1,"name":"t","layers":{"base":{"south":{"press":{"type":"text","text":"a\tb😀"},"hold":{"type":"text","text":"x"}}}}}`,
	"text bad":        `{"schemaVersion":1,"name":"t","layers":{"base":{"south":{"press":{"type":"text","text":"a\u0007"},"tap":{"type":"text","text":""},"hold":{"type":"text","text":5},"release":{"type":"text","text":"x","mode":"tap"}}}}}`,
	"text escaped":    `{"schemaVersion":1,"name":"t","layers":{"base":{"south":{"press":{"type":"text","text":"\ud83d\ude00 \ud800"}}}}}`,
	"function set":    `{"schemaVersion":1,"name":"n","layers":{"base":{"back":{"press":{"type":"keys","keys":"A"}}}},"functionSet":{"back":{"press":{"type":"keys","keys":"B"}},"select":{},"start":{"block":true},"guide":{"label":"g"}},"functionKeys":{"back":{"south":{}}}}`,
	"function bad":    `{"schemaVersion":1,"name":"n","layers":{"base":{}},"functionSet":{"color":"#000000","menu":{},"south":[]}}`,
	"deep":            strings.Repeat(`{"a":`, maxDepth) + "0" + strings.Repeat("}", maxDepth),
	"too deep":        strings.Repeat("[", maxDepth+1),
	"escaped keys":    `{"schemaVersion": 0, "name": "n", "layers": {"base": {"\u0073outh": {}, "a": {}}}}`,
	"lone surrogates": `{"\ud800": 1, "\udc00": 2}`,
	"escaped control": `{"schemaVersion": 0, "name": "n", "layers": {"base": {"south": {"press": {"type": "keys", "keys": "Ctrl+\u0000"}}}}}`,
	"haptic preset":   `{"schemaVersion": 1, "name": "n", "analog": {"rs": {"mode": "knob", "knobTick": true}}, "layers": {"base": {"south": {"haptic": "pulse3", "press": {"type": "keys", "keys": "A"}}}}}`,
	"haptic unknown":  `{"schemaVersion": 1, "name": "n", "layers": {"base": {"south": {"haptic": "buzz", "press": {"type": "keys", "keys": "A"}}}}}`,
	"haptic type":     `{"schemaVersion": 1, "name": "n", "analog": {"ls": {"knobTick": "yes"}}, "layers": {"base": {"south": {"haptic": ["tick"], "press": {"type": "keys", "keys": "A"}}}}}`,
	"feedback pause":  `{"schemaVersion": 1, "name": "n", "feedback": {"rumble": true, "led": false, "pause": false}, "layers": {"base": {}}}`,
	"feedback bad":    `{"schemaVersion": 1, "name": "n", "feedback": {"pause": "no"}, "layers": {"base": {}}}`,
	"feedback low":    `{"schemaVersion": 1, "name": "n", "feedback": {"lowBattery": false}, "layers": {"base": {}}}`,
	"feedback lowbad": `{"schemaVersion": 1, "name": "n", "feedback": {"lowBattery": 0}, "layers": {"base": {}}}`,
	"haptic block":    `{"schemaVersion": 1, "name": "n", "analog": {"ls": {"knobTick": true}}, "layers": {"base": {"south": {"block": true, "haptic": "error"}}}}`,
	"version float":   `{"schemaVersion": 0.0, "name": "n", "layers": {"base": {}}}`,
	"version one":     `{"schemaVersion": 1, "name": "n", "layers": {"base": {}}}`,
	"version zero":    `{"schemaVersion": -0e0, "name": "n", "layers": {"base": {"x": 1}}}`,
	"version newer":   `{"schemaVersion": 2, "name": "n", "layers": {"base": {}}}`,
	"version huge":    `{"schemaVersion": 1e999, "name": "n", "layers": {"base": {}}}`,
	"version tiny":    `{"schemaVersion": 1e-999, "name": "n", "layers": {"base": {}}}`,
	"number huge exp": `{"analog":{"ls":{"deadzone":1E1000001}}}`,
	"number tiny exp": `{"analog":{"ls":{"deadzone":1E-1000001}}}`,
	"number overflow": `{"schemaVersion":1,"name":"n","params":1E400}`,
	"number max":      `{"schemaVersion":1,"name":"n","analog":{"ls":{"deadzone":1E308}}}`,
	"number min":      `{"schemaVersion":1,"name":"n","analog":{"ls":{"deadzone":1E-400}}}`,
	"whole exp":       `{"schemaVersion":1,"name":"n","timing":{"holdMs":1e2},"layers":{"base":{}}}`,
	"whole point":     `{"schemaVersion":1,"name":"n","timing":{"holdMs":2.0},"layers":{"base":{}}}`,
	"whole large":     `{"schemaVersion":1,"name":"n","timing":{"holdMs":99999999999999999999},"layers":{"base":{}}}`,
	"whole in layer":  `{"schemaVersion":1,"name":"n","layers":{"base":{"a":{"press":{"type":"sequence","steps":[{"wait":1e1}]}},"b":{"press":{"type":"keys","keys":"A","repeat":{"rateHz":5.0}}}}}}`,
	"bom only":        "\xEF\xBB\xBF",
	"crlf":            "{\r\n\"a\"\r\n:\r\n}",
}

func FuzzParse(f *testing.F) {
	for _, pattern := range []string{
		"testdata/valid/*.json",
		"testdata/invalid/*.json",
		"../../testdata/profiles/*.json",
		"../../../examples/profiles/*.json",
	} {
		files, err := filepath.Glob(pattern)
		if err != nil {
			f.Fatal(err)
		}
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				f.Fatal(err)
			}
			f.Add(data)
		}
	}
	for _, s := range fuzzSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		p, ds := Parse(data)
		checkParse(t, data, p, ds)
		p2, ds2 := Parse(data)
		if !slices.Equal(ds, ds2) || (p == nil) != (p2 == nil) || p != nil && formatProfile(t, p) != formatProfile(t, p2) {
			t.Fatal("Parse is not deterministic")
		}
		checkSyntaxMatchesJSON(t, data)
	})
}

// TestFuzzSeeds runs the checks of FuzzParse on the named seeds in every
// go test run.
func TestFuzzSeeds(t *testing.T) {
	for name, s := range fuzzSeeds {
		t.Run(name, func(t *testing.T) {
			data := []byte(s)
			p, ds := Parse(data)
			checkParse(t, data, p, ds)
			checkSyntaxMatchesJSON(t, data)
		})
	}
}

// checkParse checks the invariants of a Parse result.
func checkParse(t *testing.T, data []byte, p *Profile, ds Diagnostics) {
	t.Helper()
	if (p == nil) != ds.HasErrors() {
		t.Fatalf("profile %v with diagnostics %v", p, ds)
	}
	for i, d := range ds {
		if d.Offset < 0 || d.Offset > len(data) || d.Line < 1 || d.Col < 1 {
			t.Fatalf("diagnostic out of range: %+v", d)
		}
		if line, col := position(data, d.Offset); line != d.Line || col != d.Col {
			t.Fatalf("diagnostic %+v: offset %d is at %d:%d", d, d.Offset, line, col)
		}
		if d.Message == "" || d.Severity > SeverityWarning {
			t.Fatalf("diagnostic %+v", d)
		}
		if _, ok := splitPointer(d.Pointer); !ok {
			t.Fatalf("diagnostic %+v: malformed pointer", d)
		}
		if i > 0 && ds[i-1].Offset > d.Offset {
			t.Fatalf("diagnostics out of order: %v", ds)
		}
	}
	if p == nil {
		return
	}
	root, serr := scan(data)
	if serr != nil {
		t.Fatalf("Parse succeeded, scan failed: %s", serr.msg)
	}
	if p.Layer("base") == nil {
		t.Fatalf("layers %v", p.Layers)
	}
	layers := append([]*Layer{}, p.Layers...)
	if p.Common != nil {
		layers = append(layers, p.Common)
	}
	for _, l := range layers {
		for i, b := range l.Bindings {
			if root.lookupPointer(b.ID) == nil {
				t.Fatalf("binding ID %q is not in the document", b.ID)
			}
			if !b.Control.IsValid() || i > 0 && l.Bindings[i-1].Control >= b.Control {
				t.Fatalf("bindings not sorted by control or not unique: %v", l.Bindings)
			}
			if a := b.Press; a != nil {
				if root.lookupPointer(a.ID) == nil {
					t.Fatalf("action ID %q is not in the document", a.ID)
				}
				if a.Type != ActionKeys && a.Type != ActionLayer && a.Type != ActionScript && a.Type != ActionProfile && a.Type != ActionMacro && a.Type != ActionMouse && a.Type != ActionHUD && a.Type != ActionSequence && a.Type != ActionText ||
					a.Type == ActionKeys && (!a.Keys.IsValid() || a.Mode != ModeHold && a.Mode != ModeTap) ||
					a.Type == ActionMacro && CheckMacroFile(a.File) != nil ||
					a.Type == ActionSequence && keys.ValidateSteps(a.Steps) != nil ||
					a.Type == ActionText && keys.ValidateText(a.Text) != nil ||
					a.Type == ActionMouse && !validMouseAction(a) ||
					a.Type == ActionHUD && !validHUDAction(a) {
					t.Fatalf("action %+v", a)
				}
			}
		}
	}
}

// validMouseAction reports whether a, a mouse action Parse returned, is one the
// schema allows: a precision with a factor of 0.05 to 1 on a press or hold
// (the id says which gesture), or a jump to a position inside the desktop.
func validMouseAction(a *Action) bool {
	switch a.Op {
	case MouseOpPrecision:
		return a.Factor >= MinPrecisionFactor && a.Factor <= MaxPrecisionFactor && (strings.HasSuffix(a.ID, "/press") || strings.HasSuffix(a.ID, "/hold"))
	case MouseOpJump:
		return a.X >= 0 && a.X <= 1 && a.Y >= 0 && a.Y <= 1
	}
	return false
}

// validHUDAction reports whether a, a HUD action Parse returned, is one the
// schema allows: the hints operation on a press or a hold (the id says which
// gesture).
func validHUDAction(a *Action) bool {
	return a.Op == HUDOpHints && (strings.HasSuffix(a.ID, "/press") || strings.HasSuffix(a.ID, "/hold"))
}

// checkSyntaxMatchesJSON checks that the scanner accepts exactly what
// encoding/json accepts, apart from what it rejects on purpose: invalid
// UTF-8, duplicate keys and deep nesting.
func checkSyntaxMatchesJSON(t *testing.T, data []byte) {
	t.Helper()
	if !utf8.Valid(data) {
		return
	}
	_, serr := scan(data)
	if serr != nil && (strings.HasPrefix(serr.msg, "duplicate key") || strings.HasPrefix(serr.msg, "nesting is deeper") || strings.HasPrefix(serr.msg, "number out of range")) {
		return
	}
	if valid := json.Valid(data[bomLen(data):]); valid != (serr == nil) {
		t.Fatalf("json.Valid = %v, scan error %v", valid, serr)
	}
}
