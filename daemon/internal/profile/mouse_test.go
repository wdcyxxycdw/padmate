package profile_test

import (
	"reflect"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// The stick in mode "mouse" and the mouse actions (ADR-0062, section 5): the
// optional fields resolve to their defaults, and the values written survive.
func TestMouseStickAndActions(t *testing.T) {
	const src = `{
	"schemaVersion": 1, "name": "pointer",
	"analog": {
		"ls": {"mode": "mouse", "mouseSpeed": 800, "mouseSmoothingMs": 120, "curve": "quadratic"},
		"rs": {"mode": "mouse"}
	},
	"layers": {"base": {
		"lb": {"press": {"type": "mouse", "op": "precision", "factor": 0.2}},
		"rb": {"hold": {"type": "mouse", "op": "precision"}},
		"south": {"press": {"type": "mouse", "op": "jump", "x": 0.5, "y": 0.25}},
		"east": {"tap": {"type": "mouse", "op": "jump", "x": 0, "y": 1}}
	}}}`
	p, ds := profile.Parse([]byte(src))
	if p == nil || ds.HasErrors() {
		t.Fatalf("errors: %v", ds)
	}
	ls, rs := p.Analog.LS, p.Analog.RS
	if ls.Mode != "mouse" || ls.MouseSpeed != 800 || ls.MouseSmoothingMs != 120 || ls.Curve != profile.CurveQuadratic {
		t.Errorf("ls = %+v", ls)
	}
	if rs.Mode != "mouse" || rs.MouseSpeed != profile.DefaultMouseSpeed || rs.MouseSmoothingMs != 0 {
		t.Errorf("rs = %+v, want the default speed and no smoothing", rs)
	}
	if profile.DefaultMouseSpeed != 1200 {
		t.Errorf("DefaultMouseSpeed = %d, want 1200", profile.DefaultMouseSpeed)
	}
	base := p.Layer("base")
	for _, tc := range []struct {
		control string
		get     func(*profile.Binding) *profile.Action
		want    profile.Action
		text    string
	}{
		{"lb", func(b *profile.Binding) *profile.Action { return b.Press }, profile.Action{Type: profile.ActionMouse, Op: "precision", Factor: 0.2}, "mouse precision 0.2"},
		{"rb", func(b *profile.Binding) *profile.Action { return b.Hold }, profile.Action{Type: profile.ActionMouse, Op: "precision", Factor: profile.DefaultPrecisionFactor}, "mouse precision 0.3"},
		{"south", func(b *profile.Binding) *profile.Action { return b.Press }, profile.Action{Type: profile.ActionMouse, Op: "jump", X: 0.5, Y: 0.25}, "mouse jump 0.5,0.25"},
		{"east", func(b *profile.Binding) *profile.Action { return b.Tap }, profile.Action{Type: profile.ActionMouse, Op: "jump", X: 0, Y: 1}, "mouse jump 0,1"},
	} {
		var got *profile.Action
		for _, b := range base.Bindings {
			if b.Control.String() == tc.control || b.ID == "/layers/base/"+tc.control {
				got = tc.get(b)
			}
		}
		if got == nil {
			t.Errorf("%s: no action", tc.control)
			continue
		}
		want := tc.want
		want.ID = got.ID
		if !reflect.DeepEqual(*got, want) {
			t.Errorf("%s: action %+v, want %+v", tc.control, *got, want)
		}
		if got.String() != tc.text {
			t.Errorf("%s: String() = %q, want %q", tc.control, got.String(), tc.text)
		}
	}
	if profile.ActionMouse.String() != "mouse" {
		t.Errorf("ActionMouse.String() = %q", profile.ActionMouse.String())
	}
}

// A profile that never mentions the mouse keeps working: the defaults apply and
// a mode the file does not set stays as it was.
func TestMouseDefaultsOfAProfileWithoutThem(t *testing.T) {
	p, ds := profile.Parse([]byte(`{"schemaVersion":1,"name":"x","layers":{"base":{}}}`))
	if p == nil || ds.HasErrors() {
		t.Fatalf("errors: %v", ds)
	}
	if p.Analog.LS.Mode != "standard" || p.Analog.LS.MouseSpeed != profile.DefaultMouseSpeed {
		t.Errorf("ls = %+v", p.Analog.LS)
	}
}

// Only a press or a hold lasts as long as the control is held.
func TestGestureHolds(t *testing.T) {
	for g := profile.GestureNone; int(g) < profile.GestureCount; g++ {
		want := g == profile.GesturePress || g == profile.GestureHold
		if g.Holds() != want {
			t.Errorf("%v.Holds() = %v, want %v", g, g.Holds(), want)
		}
		if g != profile.GestureNone && g.Holds() == g.Momentary() {
			t.Errorf("%v is both or neither of Holds and Momentary", g)
		}
	}
}
