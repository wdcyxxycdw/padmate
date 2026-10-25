package script

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// effectCall is one request that a script's pad.* call made of the Effects.
type effectCall struct {
	Kind   string // rumble, haptic, stop, light, release or cancel
	Slot   uint8
	Dev    input.DeviceID
	Steps  []haptic.Step
	Preset haptic.Preset
	Color  haptic.RGB
}

// fakeEffects records what a script asked of the gamepad. The switches are the
// profile's feedback.rumble and feedback.led.
type fakeEffects struct {
	rumble, led bool
	calls       []effectCall
}

func (f *fakeEffects) Switches(uint8) (rumble, led bool) { return f.rumble, f.led }

func (f *fakeEffects) Rumble(slot uint8, dev input.DeviceID, steps []haptic.Step) {
	f.calls = append(f.calls, effectCall{Kind: "rumble", Slot: slot, Dev: dev, Steps: steps})
}

func (f *fakeEffects) Haptic(slot uint8, dev input.DeviceID, p haptic.Preset) {
	f.calls = append(f.calls, effectCall{Kind: "haptic", Slot: slot, Dev: dev, Preset: p})
}

func (f *fakeEffects) StopRumble(slot uint8, dev input.DeviceID) {
	f.calls = append(f.calls, effectCall{Kind: "stop", Slot: slot, Dev: dev})
}

func (f *fakeEffects) Light(slot uint8, dev input.DeviceID, c haptic.RGB) {
	f.calls = append(f.calls, effectCall{Kind: "light", Slot: slot, Dev: dev, Color: c})
}

func (f *fakeEffects) ReleaseLight(slot uint8, dev input.DeviceID) {
	f.calls = append(f.calls, effectCall{Kind: "release", Slot: slot, Dev: dev})
}

func (f *fakeEffects) Cancel(slot uint8, dev input.DeviceID) {
	f.calls = append(f.calls, effectCall{Kind: "cancel", Slot: slot, Dev: dev})
}

// take returns the calls so far and forgets them.
func (f *fakeEffects) take() []effectCall {
	calls := f.calls
	f.calls = nil
	return calls
}

const effectPadID = input.DeviceID(7)

// allEffects is a gamepad with every effect there is.
var allEffects = input.Capabilities{Known: true, Rumble: true, TriggerRumble: true, LED: true, RGBLED: true, PlayerLED: true}

// effectPad is the snapshot of a connected gamepad that can do caps.
func effectPad(caps input.Capabilities) PadState {
	return PadState{Connected: true, Key: "pad", Info: input.DeviceInfo{ID: effectPadID, Name: "Pad", Type: "xboxone", Capabilities: caps}}
}

// effectHost is an active host on slot 2 whose environment has sink, with the
// gamepad pad in its slot.
func effectHost(t *testing.T, sink Effects, pad PadState, src string) (*Host, *clock.Fake) {
	t.Helper()
	env := new(Environment)
	if sink != nil {
		env.SetEffects(sink)
	}
	clk := clock.NewFake()
	h := NewHostWithOptions(HostOptions{Clock: clk, Environment: env, Pad: pad, Owner: output.Owner{Slot: 2, Gen: 1, Source: output.SourceScript}})
	t.Cleanup(func() { _ = h.Dispose() })
	if err := h.Activate(environmentProgram(t, `const pad = __padmate.pad, host = __padmate.host;`+src)); err != nil {
		t.Fatal(err)
	}
	return h, clk
}

// onPad is an active host with every effect, and its sink with both switches on.
func onPad(t *testing.T) (*Host, *fakeEffects, *clock.Fake) {
	t.Helper()
	sink := &fakeEffects{rumble: true, led: true}
	h, clk := effectHost(t, sink, effectPad(allEffects), "")
	return h, sink, clk
}

func run(t *testing.T, h *Host, src string) {
	t.Helper()
	if _, err := h.VM.RunString(src); err != nil {
		t.Fatalf("%s: %v", src, err)
	}
}

func throws(t *testing.T, h *Host, src, want string) {
	t.Helper()
	_, err := h.VM.RunString(src)
	if err == nil || !strings.Contains(err.Error(), "TypeError") || !strings.Contains(err.Error(), want) {
		t.Errorf("%s: err = %v, want a TypeError with %q", src, err, want)
	}
}

func rumbleOf(steps ...haptic.Step) effectCall {
	return effectCall{Kind: "rumble", Slot: 2, Dev: effectPadID, Steps: steps}
}

// pad.rumble takes strengths from 0 to 1, limits what is outside, rounds the
// time and limits it to 1..5000 ms: 0 is no time but a stop.
func TestEffectsRumbleArguments(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want haptic.Step
	}{
		{`pad.rumble(0.5, 1, 200)`, haptic.Step{Low: 32768, High: 65535, Ms: 200}},
		{`pad.rumble(0, 0.25, 1)`, haptic.Step{High: 16384, Ms: 1}},
		{`pad.rumble(1, 1, 99999)`, haptic.Step{Low: 65535, High: 65535, Ms: 5000}},
		{`pad.rumble(-1, 2, 1234.5)`, haptic.Step{High: 65535, Ms: 1235}},
		{`pad.rumble(1, 0, -7)`, haptic.Step{Low: 65535, Ms: 1}},
		{`pad.rumble(1, 0, 0.5)`, haptic.Step{Low: 65535, Ms: 1}},
		{`pad.rumble(1, 0, 4999.4)`, haptic.Step{Low: 65535, Ms: 4999}},
		{`pad.rumble(1, 0, 1e9)`, haptic.Step{Low: 65535, Ms: 5000}},
	} {
		h, sink, _ := onPad(t)
		run(t, h, tc.src)
		if got := sink.take(); !reflect.DeepEqual(got, []effectCall{rumbleOf(tc.want)}) {
			t.Errorf("%s: calls = %+v, want one rumble of %+v", tc.src, got, tc.want)
		}
	}

	h, sink, _ := onPad(t)
	for _, tc := range []struct{ src, want string }{
		{`pad.rumble()`, "pad.rumble: low must be a number from 0 to 1"},
		{`pad.rumble("1", 0, 100)`, "pad.rumble: low must be a number from 0 to 1"},
		{`pad.rumble(NaN, 0, 100)`, "pad.rumble: low must be a number from 0 to 1"},
		{`pad.rumble(Infinity, 0, 100)`, "pad.rumble: low must be a number from 0 to 1"},
		{`pad.rumble(1, null, 100)`, "pad.rumble: high must be a number from 0 to 1"},
		{`pad.rumble(1, true, 100)`, "pad.rumble: high must be a number from 0 to 1"},
		{`pad.rumble(1, {}, 100)`, "pad.rumble: high must be a number from 0 to 1"},
		{`pad.rumble(1, 1n, 100)`, "pad.rumble: high must be a number from 0 to 1"},
		{`pad.rumble(1, 1)`, "pad.rumble: ms must be a finite number"},
		{`pad.rumble(1, 1, "100")`, "pad.rumble: ms must be a finite number"},
		{`pad.rumble(1, 1, NaN)`, "pad.rumble: ms must be a finite number"},
		{`pad.rumble(1, 1, -Infinity)`, "pad.rumble: ms must be a finite number"},
		{`pad.rumbleTriggers(1, 1)`, "pad.rumbleTriggers: ms must be a finite number"},
		{`pad.rumbleTriggers("a", 1, 10)`, "pad.rumbleTriggers: left must be a number from 0 to 1"},
		{`pad.rumbleTriggers(1, [], 10)`, "pad.rumbleTriggers: right must be a number from 0 to 1"},
	} {
		throws(t, h, tc.src, tc.want)
	}
	if calls := sink.take(); len(calls) != 0 {
		t.Fatalf("a rejected call reached the gamepad: %+v", calls)
	}
}

// ms 0 stops the script's rumble; so does a rumble of no strength, and a stop
// with nothing running sends nothing.
func TestEffectsRumbleStop(t *testing.T) {
	h, sink, _ := onPad(t)
	run(t, h, `pad.rumble(0, 0, 0)`)
	if calls := sink.take(); len(calls) != 0 {
		t.Fatalf("a stop with nothing running sent %+v", calls)
	}
	run(t, h, `pad.rumble(0.5, 0, 1000)`)
	run(t, h, `pad.rumble(1, 1, 0)`)
	run(t, h, `pad.rumble(0.5, 0, 1000)`)
	run(t, h, `pad.rumble(0, 0, 500)`)
	run(t, h, `pad.rumble(0.5, 0, 1000)`)
	run(t, h, `pad.rumble(0.5, 0.5, 0.2)`) // rounds to 0
	want := []effectCall{
		rumbleOf(haptic.Step{Low: 32768, Ms: 1000}),
		{Kind: "stop", Slot: 2, Dev: effectPadID},
		rumbleOf(haptic.Step{Low: 32768, Ms: 1000}),
		{Kind: "stop", Slot: 2, Dev: effectPadID},
		rumbleOf(haptic.Step{Low: 32768, Ms: 1000}),
		{Kind: "stop", Slot: 2, Dev: effectPadID},
	}
	if got := sink.take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %+v\nwant %+v", got, want)
	}
}

// The motors and the triggers are two channels of one rumble: a call for one
// keeps what is still running on the other, as one program.
func TestEffectsTriggersMergeWithTheMotors(t *testing.T) {
	h, sink, clk := onPad(t)
	run(t, h, `pad.rumble(1, 0, 300)`)
	clk.Advance(100 * time.Millisecond)
	run(t, h, `pad.rumbleTriggers(0, 1, 400)`)
	clk.Advance(50 * time.Millisecond)
	run(t, h, `pad.rumbleTriggers(0.5, 0.5, 100)`) // replaces the triggers' channel
	clk.Advance(30 * time.Millisecond)
	run(t, h, `pad.rumble(0, 0, 0)`)         // the motors end, the triggers go on
	run(t, h, `pad.rumbleTriggers(0, 0, 0)`) // and then they end
	want := []effectCall{
		rumbleOf(haptic.Step{Low: 65535, Ms: 300}),
		// 200 ms of the motors are left: both for 200 ms, the triggers after.
		rumbleOf(haptic.Step{Low: 65535, RightTrig: 65535, Ms: 200}, haptic.Step{RightTrig: 65535, Ms: 200}),
		// At 150 ms: 150 ms of the motors, 100 ms of the triggers' new level.
		rumbleOf(haptic.Step{Low: 65535, LeftTrig: 32768, RightTrig: 32768, Ms: 100}, haptic.Step{Low: 65535, Ms: 50}),
		// At 180 ms the motors end, 70 ms of the triggers are left.
		rumbleOf(haptic.Step{LeftTrig: 32768, RightTrig: 32768, Ms: 70}),
		{Kind: "stop", Slot: 2, Dev: effectPadID},
	}
	if got := sink.take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %+v\nwant %+v", got, want)
	}

	// A channel that ran out is not merged in, and a preset replaces both.
	clk.Advance(time.Second)
	run(t, h, `pad.rumble(0.5, 0, 100)`)
	run(t, h, `pad.haptic("tick")`)
	clk.Advance(10 * time.Millisecond)
	run(t, h, `pad.rumbleTriggers(1, 1, 100)`)
	want = []effectCall{
		rumbleOf(haptic.Step{Low: 32768, Ms: 100}),
		{Kind: "haptic", Slot: 2, Dev: effectPadID, Preset: haptic.Tick},
		rumbleOf(haptic.Step{LeftTrig: 65535, RightTrig: 65535, Ms: 100}),
	}
	if got := sink.take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %+v\nwant %+v", got, want)
	}
}

// pad.haptic takes the preset names that bindings take; anything else is a
// TypeError that lists them.
func TestEffectsHaptic(t *testing.T) {
	h, sink, _ := onPad(t)
	for _, name := range haptic.PresetNames() {
		run(t, h, fmt.Sprintf(`pad.haptic(%q)`, name))
	}
	var want []effectCall
	for _, p := range haptic.Presets() {
		want = append(want, effectCall{Kind: "haptic", Slot: 2, Dev: effectPadID, Preset: p})
	}
	// The budget is 20 a second, and there are seven.
	if got := sink.take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %+v\nwant %+v", got, want)
	}
	const names = "tick, confirm, error, pulse1, pulse2, pulse3, pulse4"
	for _, src := range []string{`pad.haptic("buzz")`, `pad.haptic("Tick")`, `pad.haptic("")`} {
		throws(t, h, src, "pad.haptic: unknown preset")
		throws(t, h, src, names)
	}
	for _, src := range []string{`pad.haptic()`, `pad.haptic(1)`, `pad.haptic(null)`, `pad.haptic({})`, `pad.haptic(["tick"])`} {
		throws(t, h, src, "pad.haptic: preset must be one of "+names)
	}
	if calls := sink.take(); len(calls) != 0 {
		t.Fatalf("a rejected call reached the gamepad: %+v", calls)
	}
}

// pad.setLed takes "#RRGGBB" in either case, or null to give the claim up,
// which sends nothing when there is none.
func TestEffectsSetLed(t *testing.T) {
	h, sink, _ := onPad(t)
	run(t, h, `pad.setLed(null)`)
	run(t, h, `pad.setLed("#FFAA00")`)
	run(t, h, `pad.setLed("#0a0B0c")`)
	run(t, h, `pad.setLed(null)`)
	run(t, h, `pad.setLed(null)`)
	want := []effectCall{
		{Kind: "light", Slot: 2, Dev: effectPadID, Color: haptic.RGB{0xFF, 0xAA, 0x00}},
		{Kind: "light", Slot: 2, Dev: effectPadID, Color: haptic.RGB{0x0A, 0x0B, 0x0C}},
		{Kind: "release", Slot: 2, Dev: effectPadID},
	}
	if got := sink.take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %+v\nwant %+v", got, want)
	}
	const message = `pad.setLed: color must be "#RRGGBB" or null`
	for _, src := range []string{
		`pad.setLed()`, `pad.setLed(undefined)`, `pad.setLed("red")`, `pad.setLed("FFAA00")`, `pad.setLed("#FFF")`,
		`pad.setLed("#GGAA00")`, `pad.setLed("#FFAA00 ")`, `pad.setLed("#FFAA000")`, `pad.setLed(0xFFAA00)`, `pad.setLed([255, 170, 0])`,
	} {
		throws(t, h, src, message)
	}
	if calls := sink.take(); len(calls) != 0 {
		t.Fatalf("a rejected call reached the gamepad: %+v", calls)
	}
}

// Everything is a silent no-op unless the host has the effects, a connected
// gamepad that can do it, and the profile's switch on; each of rumble, triggers
// and haptic follows the rumble switch, and the LED the led switch.
func TestEffectsGating(t *testing.T) {
	const everything = `pad.rumble(1, 1, 100); pad.rumbleTriggers(1, 1, 100); pad.haptic("tick"); pad.setLed("#FF0000");`
	rumbleOnly := input.Capabilities{Known: true, Rumble: true}
	ledOnly := input.Capabilities{Known: true, LED: true}
	for _, tc := range []struct {
		name string
		sink *fakeEffects
		pad  PadState
		want []string // the kinds of the calls
	}{
		{"everything", &fakeEffects{rumble: true, led: true}, effectPad(allEffects), []string{"rumble", "rumble", "haptic", "light"}},
		{"switches off", &fakeEffects{}, effectPad(allEffects), nil},
		{"rumble off", &fakeEffects{led: true}, effectPad(allEffects), []string{"light"}},
		{"led off", &fakeEffects{rumble: true}, effectPad(allEffects), []string{"rumble", "rumble", "haptic"}},
		{"no triggers", &fakeEffects{rumble: true, led: true}, effectPad(input.Capabilities{Known: true, Rumble: true, LED: true}), []string{"rumble", "haptic", "light"}},
		{"rumble only", &fakeEffects{rumble: true, led: true}, effectPad(rumbleOnly), []string{"rumble", "haptic"}},
		{"led only", &fakeEffects{rumble: true, led: true}, effectPad(ledOnly), []string{"light"}},
		{"nothing", &fakeEffects{rumble: true, led: true}, effectPad(input.Capabilities{Known: true}), nil},
		{"unknown capabilities", &fakeEffects{rumble: true, led: true}, effectPad(input.Capabilities{}), []string{"rumble", "haptic", "light"}},
		{"no gamepad", &fakeEffects{rumble: true, led: true}, PadState{}, nil},
	} {
		h, _ := effectHost(t, tc.sink, tc.pad, "")
		run(t, h, everything)
		var kinds []string
		for _, c := range tc.sink.calls {
			kinds = append(kinds, c.Kind)
		}
		if !reflect.DeepEqual(kinds, tc.want) {
			t.Errorf("%s: calls = %v, want %v", tc.name, kinds, tc.want)
		}
	}

	// No effects connected at all.
	h, _ := effectHost(t, nil, effectPad(allEffects), "")
	run(t, h, everything)
	run(t, h, `pad.setLed(null); pad.rumble(0, 0, 0)`)

	// A script that ends its rumble after the switch went off still ends it.
	sink := &fakeEffects{rumble: true, led: true}
	h, _ = effectHost(t, sink, effectPad(allEffects), "")
	run(t, h, `pad.rumble(1, 1, 1000); pad.setLed("#00FF00")`)
	sink.rumble, sink.led = false, false
	run(t, h, `pad.rumble(1, 1, 1000); pad.setLed("#0000FF")`)
	run(t, h, `pad.rumble(0, 0, 0); pad.setLed(null)`)
	var kinds []string
	for _, c := range sink.calls {
		kinds = append(kinds, c.Kind)
	}
	if want := []string{"rumble", "light", "stop", "release"}; !reflect.DeepEqual(kinds, want) {
		t.Errorf("after the switches went off: %v, want %v", kinds, want)
	}
}

// host.has("rumble") and host.has("led") are true when the effects are
// connected, the gamepad can do it and the switch is on; each call reads them
// anew.
func TestEffectsHostHas(t *testing.T) {
	has := func(h *Host, feature string) bool {
		t.Helper()
		v, err := h.VM.RunString(`host.has("` + feature + `")`)
		if err != nil {
			t.Fatal(err)
		}
		return v.ToBoolean()
	}
	sink := &fakeEffects{rumble: true, led: true}
	h, _ := effectHost(t, sink, effectPad(allEffects), "")
	if !has(h, "rumble") || !has(h, "led") {
		t.Fatal("has is false with everything on")
	}
	sink.rumble = false
	if has(h, "rumble") || !has(h, "led") {
		t.Fatal("has(rumble) does not follow the rumble switch")
	}
	sink.rumble, sink.led = true, false
	if !has(h, "rumble") || has(h, "led") {
		t.Fatal("has(led) does not follow the led switch")
	}

	for name, caps := range map[string]input.Capabilities{
		"no motors": {Known: true, LED: true}, "no led": {Known: true, Rumble: true},
	} {
		sink := &fakeEffects{rumble: true, led: true}
		h, _ := effectHost(t, sink, effectPad(caps), "")
		if got, want := [2]bool{has(h, "rumble"), has(h, "led")}, [2]bool{caps.Rumble, caps.LED}; got != want {
			t.Errorf("%s: has(rumble), has(led) = %v, want %v", name, got, want)
		}
	}
	// No gamepad, or no effects: false, and the other features are unchanged.
	h, _ = effectHost(t, &fakeEffects{rumble: true, led: true}, PadState{}, "")
	if has(h, "rumble") || has(h, "led") {
		t.Error("has is true without a gamepad")
	}
	h, _ = effectHost(t, nil, effectPad(allEffects), "")
	if has(h, "rumble") || has(h, "led") || has(h, "gyro") || has(h, "hud") {
		t.Error("has is true without effects")
	}
	// A feature the host was given stays true.
	given := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), Features: map[Feature]bool{FeatureRumble: true}})
	t.Cleanup(func() { _ = given.Dispose() })
	if v, _ := given.VM.RunString(`__padmate.host.has("rumble")`); !v.ToBoolean() {
		t.Error("a feature given to the host is false")
	}
	// A describe runtime never reaches a gamepad.
	env := new(Environment)
	env.SetEffects(&fakeEffects{rumble: true, led: true})
	describe := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), Environment: env, Pad: effectPad(allEffects), Mode: "describe"})
	t.Cleanup(func() { _ = describe.Dispose() })
	if v, _ := describe.VM.RunString(`__padmate.host.has("rumble") || __padmate.host.has("led")`); v.ToBoolean() {
		t.Error("describe: has is true")
	}
}

// The calls throw in setup and while a macro loads, where the runtime is not
// the one on screen, and do nothing in a describe run, in teardown and once the
// runtime is not active; an argument that is wrong is a TypeError wherever it
// is.
func TestEffectsOutsideAnActiveRuntime(t *testing.T) {
	sink := &fakeEffects{rumble: true, led: true}
	for _, call := range []string{`pad.rumble(1, 1, 100)`, `pad.rumbleTriggers(1, 1, 100)`, `pad.haptic("tick")`, `pad.setLed("#FFFFFF")`, `pad.setLed(null)`} {
		env := new(Environment)
		env.SetEffects(sink)
		h := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), Environment: env, Pad: effectPad(allEffects)})
		err := h.Activate(environmentProgram(t, `__padmate.pad.`+strings.TrimPrefix(call, "pad.")+`;`))
		name := strings.SplitN(strings.TrimPrefix(call, "pad."), "(", 2)[0]
		if err == nil || !strings.Contains(err.Error(), "pad."+name+" is not available in setup; call it from onActivate or a handler") {
			t.Errorf("%s at the top level: %v", call, err)
		}
		_ = h.Dispose()
	}
	// In the setup function of defineScript.
	ps := ProgramsForTest(t, map[string]string{"main.ts": `import { defineScript, pad } from "padmate";
export default defineScript({ setup() { pad.haptic("tick"); } });`}, "main.ts")
	env := new(Environment)
	env.SetEffects(sink)
	h := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), Environment: env, Pad: effectPad(allEffects)})
	if err := h.ActivatePrograms(ps); err == nil || !strings.Contains(err.Error(), "pad.haptic is not available in setup") {
		t.Errorf("setup(): %v", err)
	}
	_ = h.Dispose()
	if calls := sink.take(); len(calls) != 0 {
		t.Fatalf("setup reached the gamepad: %+v", calls)
	}

	// A describe run does nothing, wherever it is called from.
	describe := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), Environment: env, Pad: effectPad(allEffects), Mode: "describe"})
	t.Cleanup(func() { _ = describe.Dispose() })
	if err := describe.Activate(environmentProgram(t, `__padmate.pad.rumble(1, 1, 100); __padmate.pad.haptic("tick"); __padmate.pad.setLed("#FF0000");`)); err != nil {
		t.Fatal(err)
	}
	if _, err := describe.VM.RunString(`__padmate.pad.haptic("nope")`); err == nil {
		t.Error("describe: a wrong argument was accepted")
	}

	// Teardown cannot reach the gamepad: the runtime is no longer active.
	ps = ProgramsForTest(t, map[string]string{"main.ts": `import { defineScript, pad } from "padmate";
export default defineScript({ teardown() { pad.haptic("tick"); pad.setLed("#FF0000"); } });`}, "main.ts")
	td := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), Environment: env, Pad: effectPad(allEffects)})
	if err := td.ActivatePrograms(ps); err != nil {
		t.Fatal(err)
	}
	if err := td.OnActivate(); err != nil {
		t.Fatal(err)
	}
	if err := td.Dispose(); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if calls := sink.take(); len(calls) != 0 {
		t.Fatalf("describe or teardown reached the gamepad: %+v", calls)
	}

	// An inactive runtime: after a release, a call does nothing.
	act, actSink, _ := onPad(t)
	act.ReleaseAll()
	run(t, act, `pad.rumble(1, 1, 100); pad.haptic("tick"); pad.setLed("#FF0000")`)
	throws(t, act, `pad.haptic("nope")`, "pad.haptic: unknown preset")
	if calls := actSink.take(); len(calls) != 0 {
		t.Fatalf("an inactive runtime reached the gamepad: %+v", calls)
	}
}

// A macro has pad, so it can buzz in its run; its top level, which runs when
// the macro loads, throws like setup.
func TestEffectsInMacros(t *testing.T) {
	sink := &fakeEffects{rumble: true, led: true}
	env := new(Environment)
	env.SetEffects(sink)
	h := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), Environment: env, Pad: effectPad(allEffects)})
	t.Cleanup(func() { _ = h.Dispose() })
	ps := ProgramsForTest(t, map[string]string{
		"macros/buzz.ts": `import { pad } from "padmate";
export default function () { pad.haptic("confirm"); pad.setLed("#112233"); }`,
		"macros/early.ts": `import { pad } from "padmate";
pad.haptic("tick");
export default function () {}`,
	}, "", "buzz.ts", "early.ts")
	if err := h.ActivatePrograms(ps); err != nil {
		t.Fatal(err)
	}
	if err := h.OnActivate(); err != nil {
		t.Fatal(err)
	}
	macros := h.MacroErrors()
	if len(macros) != 1 || macros[0].File != "early.ts" || !strings.Contains(macros[0].Error(), "pad.haptic is not available while the macro loads") {
		t.Fatalf("macro errors = %v", macros)
	}
	if err := h.RunMacro("buzz.ts", Event{}); err != nil {
		t.Fatal(err)
	}
	want := []effectCall{
		{Kind: "haptic", Slot: 0, Dev: effectPadID, Preset: haptic.Confirm},
		{Kind: "light", Slot: 0, Dev: effectPadID, Color: haptic.RGB{0x11, 0x22, 0x33}},
	}
	if got := sink.take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %+v\nwant %+v", got, want)
	}
}

// A runtime may start 20 effects a second of the slot clock; one warning a
// second says the rest were dropped. Stopping something costs nothing and is
// never dropped.
func TestEffectsBudget(t *testing.T) {
	h, sink, clk := onPad(t)
	run(t, h, `for (let i = 0; i < 25; i++) pad.rumble(0.5, 0.5, 10)`)
	if n := len(sink.take()); n != effectsPerSecond {
		t.Fatalf("%d effects in one second, want %d", n, effectsPerSecond)
	}
	logs, _ := h.DrainLogs()
	if len(logs) != 1 || logs[0].Level != "warn" || !strings.Contains(logs[0].Message, "pad.rumble: more than 20 effect calls in a second") {
		t.Fatalf("logs = %+v", logs)
	}
	// Every member shares the budget; the limit holds within the second.
	clk.Advance(999 * time.Millisecond)
	run(t, h, `pad.haptic("tick"); pad.setLed("#FF0000"); pad.rumbleTriggers(1, 1, 10)`)
	if calls := sink.take(); len(calls) != 0 {
		t.Fatalf("calls within the second: %+v", calls)
	}
	if logs, _ := h.DrainLogs(); len(logs) != 0 {
		t.Fatalf("second warning in one window: %+v", logs)
	}
	// A stop and a release pass although the budget is gone.
	run(t, h, `pad.rumble(0, 0, 0); pad.setLed(null)`)
	if got, want := sink.take(), []effectCall{{Kind: "stop", Slot: 2, Dev: effectPadID}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("a stop within the budget's second: %+v, want %+v", got, want)
	}
	clk.Advance(time.Millisecond)
	run(t, h, `pad.haptic("tick"); pad.setLed("#FF0000")`)
	if got := sink.take(); len(got) != 2 || got[0].Kind != "haptic" || got[1].Kind != "light" {
		t.Fatalf("calls in the next second: %+v", got)
	}
	run(t, h, `pad.setLed(null)`)
	if got, want := sink.take(), []effectCall{{Kind: "release", Slot: 2, Dev: effectPadID}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("release: %+v, want %+v", got, want)
	}

	// A call that does nothing for lack of a gamepad or a switch is not counted.
	sink.rumble = false
	run(t, h, `for (let i = 0; i < 60; i++) pad.rumble(1, 1, 10)`)
	sink.rumble = true
	clk.Advance(time.Second)
	run(t, h, `for (let i = 0; i < 5; i++) pad.rumble(1, 1, 10)`)
	if n := len(sink.take()); n != 5 {
		t.Fatalf("%d calls after a second of ignored calls, want 5", n)
	}
}

// Whenever a runtime stops being the active one, what the script has on its
// gamepad ends with it; once, and only if it has anything.
func TestEffectsCancelWhenTheRuntimeStops(t *testing.T) {
	canceled := []effectCall{{Kind: "cancel", Slot: 2, Dev: effectPadID}}
	for name, stop := range map[string]func(*Host){
		"release all": func(h *Host) { h.ReleaseAll() },
		"suspend":     func(h *Host) { h.Suspend() },
		"dispose":     func(h *Host) { _ = h.Dispose() },
		"discard":     func(h *Host) { _ = h.Discard() },
		"suspend and dispose": func(h *Host) {
			h.Suspend()
			_ = h.Dispose()
		},
		"release all twice": func(h *Host) { h.ReleaseAll(); h.ReleaseAll() },
	} {
		for what, src := range map[string]string{
			"a rumble":  `pad.rumble(1, 1, 3000)`,
			"a preset":  `pad.haptic("error")`,
			"a color":   `pad.setLed("#FF0000")`,
			"all three": `pad.rumble(1, 1, 3000); pad.haptic("tick"); pad.setLed("#FF0000")`,
		} {
			h, sink, _ := onPad(t)
			run(t, h, src)
			sink.take()
			stop(h)
			if got := sink.take(); !reflect.DeepEqual(got, canceled) {
				t.Errorf("%s after %s: calls = %+v, want one cancel", name, what, got)
			}
		}
		// Nothing started, or everything stopped: nothing to cancel.
		h, sink, _ := onPad(t)
		run(t, h, `pad.rumble(1, 1, 3000); pad.rumble(0, 0, 0); pad.setLed("#00FF00"); pad.setLed(null)`)
		sink.take()
		stop(h)
		if got := sink.take(); len(got) != 0 {
			t.Errorf("%s with nothing going: %+v", name, got)
		}
	}
	// key.releaseAll of the script is about keys.
	h, sink, _ := onPad(t)
	run(t, h, `pad.rumble(1, 1, 3000); __padmate.key.releaseAll()`)
	sink.take()
	h.ReleaseKeys()
	if got := sink.take(); len(got) != 0 {
		t.Errorf("releasing the keys canceled the effects: %+v", got)
	}
}

// A script whose slot got another gamepad leaves nothing on the first one.
func TestEffectsFollowTheGamepad(t *testing.T) {
	h, sink, _ := onPad(t)
	run(t, h, `pad.rumble(1, 1, 3000)`)
	sink.take()
	other := effectPad(allEffects)
	other.Info.ID = 9
	h.UpdatePad(other)
	run(t, h, `pad.setLed("#112233")`)
	want := []effectCall{
		{Kind: "cancel", Slot: 2, Dev: effectPadID},
		{Kind: "light", Slot: 2, Dev: 9, Color: haptic.RGB{0x11, 0x22, 0x33}},
	}
	if got := sink.take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %+v\nwant %+v", got, want)
	}
	h.Suspend()
	if got, want := sink.take(), []effectCall{{Kind: "cancel", Slot: 2, Dev: 9}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %+v\nwant %+v", got, want)
	}
	// A pad that left: its effects end through the release of the runtime.
	h2, sink2, _ := onPad(t)
	run(t, h2, `pad.haptic("tick")`)
	sink2.take()
	h2.UpdatePad(PadState{})
	run(t, h2, `pad.haptic("tick")`) // no gamepad: nothing
	h2.ReleaseAll()
	if got := sink2.take(); !reflect.DeepEqual(got, []effectCall{{Kind: "cancel", Slot: 2, Dev: effectPadID}}) {
		t.Fatalf("calls = %+v", got)
	}
}

// Through a Lifecycle: a swap, an interruption (the restart after an error,
// and a permanent stop) and the end of the lifecycle each end the effects of
// the runtime they replace, and the runtime that follows starts with none.
func TestEffectsCancelThroughTheLifecycle(t *testing.T) {
	c := clock.NewFake()
	sink := &fakeEffects{rumble: true, led: true}
	env := new(Environment)
	env.SetEffects(sink)
	l := NewLifecycle(c, HostOptions{Environment: env, Pad: effectPad(allEffects), Owner: output.Owner{Slot: 2, Gen: 1, Source: output.SourceScript}})
	t.Cleanup(func() { _ = l.Dispose() })
	build := func(context.Context) (*goja.Program, error) {
		return goja.Compile("main.js", `var fire = () => { globalThis.__padmate.pad.rumble(1, 1, 3000); globalThis.__padmate.pad.setLed("#FF0000"); };`, false)
	}
	load := func() *Host {
		t.Helper()
		loaded := <-l.Load(context.Background(), build)
		if loaded.Err != nil {
			t.Fatal(loaded.Err)
		}
		if err := l.Swap(loaded.Host); err != nil {
			t.Fatal(err)
		}
		return loaded.Host
	}
	fire := func(h *Host) {
		t.Helper()
		if _, err := h.VM.RunString("fire()"); err != nil {
			t.Fatal(err)
		}
		if n := len(sink.take()); n != 2 {
			t.Fatalf("fire made %d calls", n)
		}
	}
	canceled := []effectCall{{Kind: "cancel", Slot: 2, Dev: effectPadID}}
	waitCurrent := func() {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for l.Current() == nil && time.Now().Before(deadline) {
			if err := l.RunDue(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Millisecond)
		}
		if l.Current() == nil {
			t.Fatal("no runtime after the restart")
		}
	}

	first := load()
	fire(first)
	second := load() // the swap retires the first
	if got := sink.take(); !reflect.DeepEqual(got, canceled) {
		t.Fatalf("swap: calls = %+v, want a cancel", got)
	}
	fire(second)
	l.RecordInterruption(c.Now())
	if got := sink.take(); !reflect.DeepEqual(got, canceled) {
		t.Fatalf("interruption: calls = %+v, want a cancel", got)
	}
	c.Advance(time.Second)
	waitCurrent()
	restarted := l.Current()
	if got := sink.take(); len(got) != 0 {
		t.Fatalf("the restarted runtime made calls: %+v", got)
	}
	fire(restarted)
	l.RecordInterruption(c.Now())
	l.RecordInterruption(c.Now())
	if _, stopped := l.RestartStatus(); !stopped {
		t.Fatal("no permanent stop")
	}
	if got := sink.take(); !reflect.DeepEqual(got, canceled) {
		t.Fatalf("permanent stop: calls = %+v, want a cancel", got)
	}
}

// pad.info.battery is there once the gamepad read a power, in the four states
// of the script API, with the percent when it reports one.
func TestEffectsPadInfoBattery(t *testing.T) {
	read := func(pad PadState) string {
		t.Helper()
		h := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), Pad: pad})
		t.Cleanup(func() { _ = h.Dispose() })
		v, err := h.VM.RunString(`JSON.stringify(__padmate.pad.info.battery === undefined ? "none" : __padmate.pad.info.battery)`)
		if err != nil {
			t.Fatal(err)
		}
		return v.String()
	}
	withPower := func(p input.Power) PadState {
		s := effectPad(allEffects)
		s.Power, s.HasPower = p, true
		return s
	}
	for _, tc := range []struct {
		pad  PadState
		want string
	}{
		{effectPad(allEffects), `"none"`},
		{PadState{}, `"none"`},
		{withPower(input.Power{State: input.PowerDischarging, Percent: 80}), `{"state":"discharging","percent":80}`},
		{withPower(input.Power{State: input.PowerCharging, Percent: 40}), `{"state":"charging","percent":40}`},
		{withPower(input.Power{State: input.PowerWired, Percent: -1}), `{"state":"wired"}`},
		{withPower(input.Power{State: input.PowerUnknown, Percent: -1}), `{"state":"unknown"}`},
		{withPower(input.Power{State: input.PowerDischarging, Percent: 0}), `{"state":"discharging","percent":0}`},
		{withPower(input.Power{State: input.PowerWired, Percent: 100}), `{"state":"wired","percent":100}`},
	} {
		if got := read(tc.pad); got != tc.want {
			t.Errorf("pad %+v: pad.info.battery = %s, want %s", tc.pad, got, tc.want)
		}
	}
	// A disconnected gamepad has none even if one was read.
	gone := withPower(input.Power{State: input.PowerWired, Percent: -1})
	gone.Connected = false
	if got := read(gone); got != `"none"` {
		t.Errorf("a disconnected gamepad: pad.info.battery = %s", got)
	}
}

// A runtime that replaces another starts with its effects: what the old one has
// going on the gamepad ends before the new one's onActivate runs, so the old
// one's cancel does not come after, and end, what onActivate starts (they are
// the Script class of one gamepad).
func TestEffectsOfTheOldRuntimeEndBeforeTheNewOneActivates(t *testing.T) {
	ps := ProgramsForTest(t, map[string]string{"main.ts": `import { defineScript, pad } from "padmate";
export default defineScript({ onActivate() { pad.haptic("tick"); pad.setLed("#112233"); } });`}, "main.ts")
	build := func(context.Context) (*goja.Program, error) { return ps.Entry, nil }
	sink := &fakeEffects{rumble: true, led: true}
	env := new(Environment)
	env.SetEffects(sink)
	l := NewLifecycle(clock.NewFake(), HostOptions{Environment: env, Pad: effectPad(allEffects), Owner: output.Owner{Slot: 2, Gen: 1, Source: output.SourceScript}})
	load := func() {
		t.Helper()
		loaded := <-l.Load(context.Background(), build)
		if loaded.Err != nil {
			t.Fatal(loaded.Err)
		}
		if err := l.Swap(loaded.Host); err != nil {
			t.Fatal(err)
		}
	}
	started := []effectCall{
		{Kind: "haptic", Slot: 2, Dev: effectPadID, Preset: haptic.Tick},
		{Kind: "light", Slot: 2, Dev: effectPadID, Color: haptic.RGB{0x11, 0x22, 0x33}},
	}
	load()
	if got := sink.take(); !reflect.DeepEqual(got, started) {
		t.Fatalf("first runtime: %+v", got)
	}
	load()
	want := append([]effectCall{{Kind: "cancel", Slot: 2, Dev: effectPadID}}, started...)
	if got := sink.take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("second runtime: %+v\nwant the old one's cancel and then the new one's effects: %+v", got, want)
	}
	// The old runtime's Suspend and Dispose, which follow, add nothing.
	time.Sleep(20 * time.Millisecond)
	if err := l.RunDue(); err != nil {
		t.Fatal(err)
	}
	if got := sink.take(); len(got) != 0 {
		t.Fatalf("the old runtime's teardown made calls: %+v", got)
	}
	if err := l.Dispose(); err != nil {
		t.Fatal(err)
	}
	if got := sink.take(); !reflect.DeepEqual(got, []effectCall{{Kind: "cancel", Slot: 2, Dev: effectPadID}}) {
		t.Fatalf("at the end: %+v", got)
	}
}
