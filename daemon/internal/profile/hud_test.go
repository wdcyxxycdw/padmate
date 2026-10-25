package profile_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// The HUD hints action (HUD-03, ADR-0063): {"type":"hud","op":"hints"} parses on
// a press or a hold, with nothing else to set.
func TestHUDHintsAction(t *testing.T) {
	const src = `{
	"schemaVersion": 1, "name": "hints",
	"layers": {"base": {
		"rb": {"press": {"type": "hud", "op": "hints"}},
		"lb": {"tap": {"type": "keys", "keys": "T"}, "hold": {"type": "hud", "op": "hints"}}
	}}}`
	p, ds := profile.Parse([]byte(src))
	if p == nil || ds.HasErrors() {
		t.Fatalf("errors: %v", ds)
	}
	base := p.Layer("base")
	for _, tc := range []struct {
		id  string
		get func(*profile.Binding) *profile.Action
	}{
		{"/layers/base/rb", func(b *profile.Binding) *profile.Action { return b.Press }},
		{"/layers/base/lb", func(b *profile.Binding) *profile.Action { return b.Hold }},
	} {
		var got *profile.Action
		for _, b := range base.Bindings {
			if b.ID == tc.id {
				got = tc.get(b)
			}
		}
		if got == nil {
			t.Errorf("%s: no action", tc.id)
			continue
		}
		want := profile.Action{ID: got.ID, Type: profile.ActionHUD, Op: profile.HUDOpHints}
		if !reflect.DeepEqual(*got, want) {
			t.Errorf("%s: action %+v, want %+v", tc.id, *got, want)
		}
		if got.String() != "hud hints" {
			t.Errorf("%s: String() = %q, want %q", tc.id, got.String(), "hud hints")
		}
	}
	if profile.ActionHUD.String() != "hud" || profile.HUDOpHints != "hints" {
		t.Errorf("ActionHUD.String() = %q, HUDOpHints = %q", profile.ActionHUD.String(), profile.HUDOpHints)
	}
}

// A hints action lasts while the control is held, so it is refused on every
// gesture that runs after the release, each with the message that names it.
func TestHUDHintsGestures(t *testing.T) {
	for _, g := range []string{"press", "hold", "tap", "release", "doubleTap", "tripleTap", "quadTap"} {
		src := `{"schemaVersion":1,"name":"h","layers":{"base":{"south":{"` + g + `":{"type":"hud","op":"hints"}}}}}`
		p, ds := profile.Parse([]byte(src))
		want := g == "press" || g == "hold"
		if want == ds.HasErrors() || want != (p != nil) {
			t.Errorf("%s: errors %v, profile %v; accepted wanted %v", g, ds, p != nil, want)
			continue
		}
		if !want && !strings.Contains(diagText(ds), "a hints action shows the key hints while the control is held: use it on press or hold, not on "+g) {
			t.Errorf("%s: %v", g, ds)
		}
	}
}

// The action has no other property, and no other operation.
func TestHUDHintsProperties(t *testing.T) {
	for _, tc := range []struct{ name, action, want string }{
		{"unknown op", `{"type":"hud","op":"map"}`, `"map" is not a valid value; use "hints"`},
		{"no op", `{"type":"hud"}`, `missing property "op"`},
		{"keys", `{"type":"hud","op":"hints","keys":"A"}`, `unknown property "keys"`},
		{"factor", `{"type":"hud","op":"hints","factor":0.5}`, `unknown property "factor"`},
		{"position", `{"type":"hud","op":"hints","x":0.5}`, `unknown property "x"`},
		{"hints of the wrong type", `{"type":"mouse","op":"hints"}`, `"hints" is not a valid value; use "precision" or "jump"`},
	} {
		src := `{"schemaVersion":1,"name":"h","layers":{"base":{"south":{"press":` + tc.action + `}}}}`
		p, ds := profile.Parse([]byte(src))
		if p != nil || !ds.HasErrors() || !strings.Contains(diagText(ds), tc.want) {
			t.Errorf("%s: profile %v, diagnostics %v; want one with %q", tc.name, p != nil, ds, tc.want)
		}
	}
}

// diagText is the diagnostics as lines of text.
func diagText(ds profile.Diagnostics) string {
	lines := make([]string, len(ds))
	for i, d := range ds {
		lines[i] = d.String()
	}
	return strings.Join(lines, "\n")
}
