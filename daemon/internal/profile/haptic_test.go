package profile_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// bindingHapticSchema returns the enum of binding.haptic in the schema, in
// file order.
func bindingHapticSchema(t *testing.T) []string {
	t.Helper()
	var doc struct {
		Defs struct {
			Binding struct {
				Properties struct {
					Haptic struct {
						Enum []string `json:"enum"`
					} `json:"haptic"`
				} `json:"properties"`
			} `json:"binding"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(profile.Schema(), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Defs.Binding.Properties.Haptic.Enum
}

// The names of binding.haptic are the names of haptic.Preset, in the same
// order: the schema, the model and the script API share one list (MAP-25,
// ADR-0065).
func TestBindingHapticEnumIsThePresetList(t *testing.T) {
	if got, want := bindingHapticSchema(t), haptic.PresetNames(); !slices.Equal(got, want) {
		t.Errorf("schema enum = %v; haptic.PresetNames() = %v", got, want)
	}
}

// Each preset name resolves to its Preset on the binding that wrote it, and a
// binding that does not write one has none. The haptic is the binding's, not
// the action's: it applies to every gesture.
func TestBindingHapticParses(t *testing.T) {
	for _, preset := range haptic.Presets() {
		src := `{"schemaVersion":1,"name":"h","layers":{"base":{
			"south":{"haptic":"` + preset.String() + `","press":{"type":"keys","keys":"A"},"hold":{"type":"keys","keys":"B"}},
			"east":{"press":{"type":"keys","keys":"C"}}}}}`
		p, ds := profile.Parse([]byte(src))
		if p == nil || len(ds) != 0 {
			t.Fatalf("%s: Parse: %v", preset, ds)
		}
		base := p.Layer("base")
		south, east := base.Bindings[0], base.Bindings[1]
		if south.Control.String() != "south" {
			south, east = east, south
		}
		if south.Haptic != preset {
			t.Errorf("south haptic = %v; want %v", south.Haptic, preset)
		}
		if east.Haptic != 0 {
			t.Errorf("east haptic = %v; want none", east.Haptic)
		}
	}
}

// A haptic on a block binding or on a binding with no action does nothing at
// run time; the file is still valid, and the editor may keep it while the
// user changes the action.
func TestBindingHapticOnBlockIsAccepted(t *testing.T) {
	p, ds := profile.Parse([]byte(`{"schemaVersion":1,"name":"h","layers":{"base":{"south":{"block":true,"haptic":"tick"}}}}`))
	if p == nil || ds.HasErrors() {
		t.Fatalf("Parse: %v", ds)
	}
	if got := p.Layer("base").Bindings[0].Haptic; got != haptic.Tick {
		t.Errorf("haptic = %v; want tick", got)
	}
}

// knobTick is meaningful with mode "knob" only, but outside it the file is
// still valid with no diagnostic, and the value is kept: the editor leaves a
// stick's other settings in place when its mode changes (knobStepDeg is the
// same), so a warning would nag about a setting the user cannot see. The
// engine plays no tick outside mode knob (engine.TestKnobTickOnlyInKnobMode).
func TestKnobTickIsInertOutsideKnobMode(t *testing.T) {
	if profile.DefaultAnalog().LS.KnobTick || profile.DefaultAnalog().RS.KnobTick {
		t.Fatal("knobTick is on by default")
	}
	for _, tc := range []struct {
		name, analog string
		ls, rs       bool
	}{
		{"default", `{}`, false, false},
		{"knob on", `{"ls":{"mode":"knob","knobTick":true}}`, true, false},
		{"knob off", `{"rs":{"mode":"knob","knobTick":false}}`, false, false},
		{"standard", `{"ls":{"mode":"standard","knobTick":true},"rs":{"knobTick":true}}`, true, true},
		{"mouse", `{"rs":{"mode":"mouse","knobTick":true}}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, ds := profile.Parse([]byte(`{"schemaVersion":1,"name":"k","analog":` + tc.analog + `,"layers":{"base":{}}}`))
			if p == nil || len(ds) != 0 {
				t.Fatalf("Parse: %v", ds)
			}
			if p.Analog.LS.KnobTick != tc.ls || p.Analog.RS.KnobTick != tc.rs {
				t.Errorf("knobTick = %v / %v; want %v / %v", p.Analog.LS.KnobTick, p.Analog.RS.KnobTick, tc.ls, tc.rs)
			}
		})
	}
}

// The schema rejects what is not a preset name or a boolean, each at its
// member.
func TestHapticSchemaRejectsBadValues(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"unknown preset", `{"schemaVersion":1,"name":"h","layers":{"base":{"south":{"haptic":"bogus","press":{"type":"keys","keys":"A"}}}}}`},
		{"case", `{"schemaVersion":1,"name":"h","layers":{"base":{"south":{"haptic":"Tick","press":{"type":"keys","keys":"A"}}}}}`},
		{"empty", `{"schemaVersion":1,"name":"h","layers":{"base":{"south":{"haptic":"","press":{"type":"keys","keys":"A"}}}}}`},
		{"number", `{"schemaVersion":1,"name":"h","layers":{"base":{"south":{"haptic":1,"press":{"type":"keys","keys":"A"}}}}}`},
		{"bool", `{"schemaVersion":1,"name":"h","layers":{"base":{"south":{"haptic":true,"press":{"type":"keys","keys":"A"}}}}}`},
		{"array", `{"schemaVersion":1,"name":"h","layers":{"base":{"south":{"haptic":["tick"],"press":{"type":"keys","keys":"A"}}}}}`},
		{"null", `{"schemaVersion":1,"name":"h","layers":{"base":{"south":{"haptic":null,"press":{"type":"keys","keys":"A"}}}}}`},
		{"knobTick string", `{"schemaVersion":1,"name":"h","analog":{"ls":{"mode":"knob","knobTick":"yes"}},"layers":{"base":{}}}`},
		{"knobTick number", `{"schemaVersion":1,"name":"h","analog":{"rs":{"mode":"knob","knobTick":1}},"layers":{"base":{}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, ds := profile.Parse([]byte(tc.src))
			if p != nil || !ds.HasErrors() {
				t.Errorf("profile=%v diagnostics=%v; want errors", p, ds)
			}
		})
	}
}
