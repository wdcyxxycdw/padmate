package integration

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

func chord(t *testing.T, s string) keys.Chord {
	t.Helper()
	c, err := keys.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func bindingOf(g profile.Gesture, a *profile.Action) *profile.Binding {
	b := &profile.Binding{}
	switch g {
	case profile.GesturePress:
		b.Press = a
	case profile.GestureTap:
		b.Tap = a
	case profile.GestureHold:
		b.Hold = a
	case profile.GestureQuadTap:
		b.QuadTap = a
	}
	return b
}

func TestUsedKeys(t *testing.T) {
	keysAction := func(s string) *profile.Action {
		return &profile.Action{Type: profile.ActionKeys, Keys: chord(t, s), Mode: profile.ModeTap}
	}
	tests := []struct {
		name string
		p    func() *profile.Profile
		want []string
	}{
		{"nil profile", func() *profile.Profile { return nil }, nil},
		{"empty", func() *profile.Profile { return &profile.Profile{} }, nil},
		{"common keys action, any spelling is canonical", func() *profile.Profile {
			return &profile.Profile{Common: &profile.Layer{Bindings: []*profile.Binding{bindingOf(profile.GesturePress, keysAction("shift+ctrl+z"))}}}
		}, []string{"Ctrl+Shift+Z"}},
		{"every gesture of every layer", func() *profile.Profile {
			return &profile.Profile{Layers: []*profile.Layer{
				{Name: "base", Bindings: []*profile.Binding{bindingOf(profile.GestureHold, keysAction("B")), bindingOf(profile.GestureQuadTap, keysAction("Q"))}},
				{Name: "alt", Bindings: []*profile.Binding{bindingOf(profile.GestureTap, keysAction("Ctrl+E"))}},
			}}
		}, []string{"B", "Ctrl+E", "Q"}},
		{"function sets, old and new format", func() *profile.Profile {
			return &profile.Profile{
				FunctionSet: &profile.Layer{Bindings: []*profile.Binding{bindingOf(profile.GesturePress, keysAction("F"))}},
				FunctionKeys: &profile.FunctionKeys{
					Back:  &profile.Layer{Bindings: []*profile.Binding{bindingOf(profile.GesturePress, keysAction("G"))}},
					Start: &profile.Layer{Bindings: []*profile.Binding{bindingOf(profile.GesturePress, keysAction("H"))}},
				},
			}
		}, []string{"F", "G", "H"}},
		{"keys action with a sequence of chords", func() *profile.Profile {
			a := &profile.Action{Type: profile.ActionKeys, Keys: chord(t, "A"), Sequence: []keys.Chord{chord(t, "A"), chord(t, "Ctrl+B")}, Mode: profile.ModeTap}
			return &profile.Profile{Common: &profile.Layer{Bindings: []*profile.Binding{bindingOf(profile.GestureTap, a)}}}
		}, []string{"A", "Ctrl+B"}},
		{"sequence action: shortcut steps only", func() *profile.Profile {
			a := &profile.Action{Type: profile.ActionSequence, Steps: []keys.Step{
				keys.KeysStep(chord(t, "Ctrl+K")), keys.WaitStep(50 * time.Millisecond), keys.TextStep("hello"), keys.KeysStep(chord(t, "X"))}}
			return &profile.Profile{Common: &profile.Layer{Bindings: []*profile.Binding{bindingOf(profile.GesturePress, a)}}}
		}, []string{"Ctrl+K", "X"}},
		{"actions that send no chord", func() *profile.Profile {
			acts := []*profile.Action{
				{Type: profile.ActionLayer, Layer: "x"}, {Type: profile.ActionScript, Name: "s"}, {Type: profile.ActionProfile, Profile: "p"},
				{Type: profile.ActionMacro, File: "m.macro"}, {Type: profile.ActionMouse}, {Type: profile.ActionHUD}, {Type: profile.ActionText, Text: "P"},
			}
			l := &profile.Layer{}
			for _, a := range acts {
				l.Bindings = append(l.Bindings, bindingOf(profile.GesturePress, a))
			}
			return &profile.Profile{Common: l}
		}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := UsedKeys(tc.p())
			var gotList []string
			for k := range got {
				gotList = append(gotList, k)
			}
			slices.Sort(gotList)
			if !reflect.DeepEqual(gotList, tc.want) {
				t.Errorf("UsedKeys = %v, want %v", gotList, tc.want)
			}
		})
	}
}

func TestUsedItems(t *testing.T) {
	items := []Item{{Keys: "Ctrl+Z", Function: "a"}, {Keys: "P", Function: "b"}, {Keys: "shift+ctrl+Y", Function: "c"}, {Keys: "X", Function: "d"}}
	tests := []struct {
		name string
		used map[string]bool
		want []string
	}{
		{"nil keeps all", nil, []string{"a", "b", "c", "d"}},
		{"empty drops all", map[string]bool{}, []string{}},
		{"some", map[string]bool{"Ctrl+Z": true, "Ctrl+Shift+Y": true}, []string{"a", "c"}},
		{"unrelated", map[string]bool{"Q": true}, []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := []string{}
			for _, it := range UsedItems(items, tc.used) {
				got = append(got, it.Function)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("UsedItems = %v, want %v", got, tc.want)
			}
		})
	}
}
