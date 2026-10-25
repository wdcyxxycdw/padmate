package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// effectRig is a liveRun with the two things that a script's effects use: the
// script definition, which holds the profile's feedback, and an effect queue
// whose worker records what it sends.
func effectRig(t *testing.T, fb profile.Feedback, off ...string) (scriptEffects, *liveRun, *effectRecorder) {
	t.Helper()
	def := newScriptDefinition(filepath.Join(t.TempDir(), "profile.json"), nil, false)
	def.feedback = fb
	rec := &effectRecorder{}
	send := effectSender{
		play: func(e input.Effect) error {
			return rec.record(fmt.Sprintf("play %d %v %v steps=%v strength=%d short=%d gap=%d", e.Device, e.Class, e.Preset, e.Steps, e.Strength, e.ShortMs, e.GapMs))
		},
		light: func(l input.Light) error {
			return rec.record(fmt.Sprintf("light %d %v %v release=%v", l.Device, l.Class, l.Color, l.Release))
		},
		stop: func(s input.Stop) error {
			return rec.record(fmt.Sprintf("stop %d %v everything=%v", s.Device, s.Class, s.Everything))
		},
	}
	q := startEffectQueue(send, nil)
	t.Cleanup(q.close)
	r := &liveRun{scriptDef: def, effects: q, log: slog.New(slog.DiscardHandler)}
	r.selection.Store(settingsOff(off...))
	return scriptEffects{run: r}, r, rec
}

// The switches of a script's effects are the global ones (ADR-0067): rumble
// needs a strength above 0 in the profile as well, and the LED has its own
// switch. The pause and low-battery switches are not the scripts'.
func TestScriptEffectsSwitches(t *testing.T) {
	for name, tc := range map[string]struct {
		change      func(*profile.Feedback)
		off         []string
		rumble, led bool
	}{
		"default":            {func(*profile.Feedback) {}, nil, true, true},
		"rumble off":         {func(*profile.Feedback) {}, []string{"rumble"}, false, true},
		"led off":            {func(*profile.Feedback) {}, []string{"led"}, true, false},
		"strength 0":         {func(f *profile.Feedback) { f.Strength = 0 }, nil, false, true},
		"both off":           {func(*profile.Feedback) {}, []string{"rumble", "led"}, false, false},
		"pause is not":       {func(*profile.Feedback) {}, []string{"pause"}, true, true},
		"low battery is not": {func(*profile.Feedback) {}, []string{"lowBattery"}, true, true},
	} {
		fb := profile.DefaultFeedback()
		tc.change(&fb)
		sink, _, _ := effectRig(t, fb, tc.off...)
		if rumble, led := sink.Switches(0); rumble != tc.rumble || led != tc.led {
			t.Errorf("%s: Switches = %v, %v; want %v, %v", name, rumble, led, tc.rumble, tc.led)
		}
	}
}

// host.has("rumble") and the effect calls read the switches at each call: a
// change of config.json applies to a running script at once.
func TestScriptEffectsSwitchesFollowTheConfig(t *testing.T) {
	sink, r, _ := effectRig(t, profile.DefaultFeedback())
	if rumble, led := sink.Switches(0); !rumble || !led {
		t.Fatalf("Switches = %v, %v; want both on", rumble, led)
	}
	r.selection.Store(settingsOff("rumble", "led"))
	if rumble, led := sink.Switches(0); rumble || led {
		t.Errorf("after both went off: Switches = %v, %v", rumble, led)
	}
	r.selection.Store(settingsOff("led"))
	if rumble, led := sink.Switches(0); !rumble || led {
		t.Errorf("rumble back on: Switches = %v, %v", rumble, led)
	}
}

// A script's requests go to the queue as Script-class requests for the
// gamepad, with the profile's strength and pulse times for a preset and the
// steps as the script gave them.
func TestScriptEffectsRequests(t *testing.T) {
	fb := profile.DefaultFeedback()
	fb.Strength, fb.ShortMs, fb.GapMs = 20000, 50, 60
	sink, _, rec := effectRig(t, fb)
	steps := []haptic.Step{{Low: 5, High: 6, Ms: 100}}
	// One at a time: a stop takes the script's requests that wait out of the
	// line, and these would not all be there to see.
	requests := []struct {
		send func()
		want string
	}{
		{func() { sink.Rumble(1, 7, steps) }, fmt.Sprintf("play 7 script preset(0) steps=%v strength=0 short=0 gap=0", steps)},
		{func() { sink.Haptic(1, 7, haptic.Pulse2) }, "play 7 script pulse2 steps=[] strength=20000 short=50 gap=60"},
		{func() { sink.StopRumble(1, 7) }, "stop 7 script everything=false"},
		{func() { sink.Light(1, 7, haptic.RGB{1, 2, 3}) }, "light 7 script [1 2 3] release=false"},
		{func() { sink.ReleaseLight(1, 7) }, "light 7 script [0 0 0] release=true"},
		{func() { sink.Cancel(1, 7) }, "stop 7 script everything=true"},
	}
	for i, req := range requests {
		req.send()
		if got := rec.waitCalls(t, i+1); got[i] != req.want {
			t.Errorf("request %d: %q, want %q", i, got[i], req.want)
		}
	}
}

// The buzz of a script that fails is the "error" preset in the Alert class, for
// the gamepad of the slot, when the global rumble switch is on.
func TestScriptErrorCue(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(*profile.Feedback)
		off    []string
		dev    input.DeviceID
		want   string
	}{
		"default":    {func(*profile.Feedback) {}, nil, 3, "play 3 alert error steps=[] strength=28672 short=0 gap=0"},
		"strength":   {func(f *profile.Feedback) { f.Strength = 1000 }, nil, 3, "play 3 alert error steps=[] strength=1000 short=0 gap=0"},
		"rumble off": {func(*profile.Feedback) {}, []string{"rumble"}, 3, ""},
		"strength 0": {func(f *profile.Feedback) { f.Strength = 0 }, nil, 3, ""},
		"led off":    {func(*profile.Feedback) {}, []string{"led"}, 3, "play 3 alert error steps=[] strength=28672 short=0 gap=0"},
		"no gamepad": {func(*profile.Feedback) {}, nil, 0, ""},
	} {
		fb := profile.DefaultFeedback()
		tc.change(&fb)
		_, r, rec := effectRig(t, fb, tc.off...)
		r.scriptErrorCue(0, tc.dev)
		// A marker, so that "nothing" can be told from "not yet".
		r.effects.postEffect(input.Effect{Device: 99, Class: haptic.Event, Preset: haptic.Tick})
		got := rec.waitCalls(t, 1)
		if tc.want == "" {
			got = rec.waitCalls(t, 1)
			if got[0] != "play 99 event tick steps=[] strength=0 short=0 gap=0" {
				t.Errorf("%s: calls %v, want only the marker", name, got)
			}
			continue
		}
		if !reflect.DeepEqual(got[0], tc.want) {
			t.Errorf("%s: call %q, want %q", name, got[0], tc.want)
		}
	}
}

// The feedback that the effects obey is that of the profile that runs: it is
// taken when the script definition is made and at each reload or switch.
func TestScriptDefinitionFollowsTheProfileFeedback(t *testing.T) {
	parse := func(src string) *profile.Profile {
		t.Helper()
		p, ds := profile.Parse([]byte(src))
		if p == nil || ds.HasErrors() {
			t.Fatalf("%s: %v", src, ds)
		}
		return p
	}
	plain := parse(`{"schemaVersion":1,"name":"a","layers":{"base":{}}}`)
	quiet := parse(`{"schemaVersion":1,"name":"b","feedback":{"strength":9000},"layers":{"base":{}}}`)
	def := newProfileScriptDefinition(filepath.Join(t.TempDir(), "profile.json"), quiet, false)
	if fb := def.feedbackSettings(); fb.Strength != 9000 {
		t.Errorf("new definition: %+v", fb)
	}
	def.useProfile(plain)
	if fb := def.feedbackSettings(); fb.Strength != profile.DefaultFeedback().Strength {
		t.Errorf("after a reload: %+v", fb)
	}
	def.setProfile(filepath.Join(t.TempDir(), "other.json"), quiet)
	if fb := def.feedbackSettings(); fb.Strength != 9000 {
		t.Errorf("after a switch: %+v", fb)
	}
	if fb := newScriptDefinition("profile.json", nil, false).feedbackSettings(); fb != profile.DefaultFeedback() {
		t.Errorf("a definition without a profile: %+v", fb)
	}
	if fb := feedbackOf(nil); fb != profile.DefaultFeedback() {
		t.Errorf("feedbackOf(nil): %+v", fb)
	}
}

// A failing script buzzes when the episode starts and not again until
// something ends it: its first error, of the script or of a macro, buzzes
// for the gamepad the slot has; the errors that follow, of either, do not;
// what clears the errors (a reload, a new runtime) ends the episode.
func TestScriptFailureEpisodeBuzzesOnce(t *testing.T) {
	var cues []string
	def := newScriptDefinition(filepath.Join(t.TempDir(), "profile.json"), nil, false)
	def.errorCue = func(slot uint8, dev input.DeviceID) { cues = append(cues, fmt.Sprintf("%d/%d", slot, dev)) }
	r := &scriptRuntime{def: def, index: 3}
	r.pad = script.PadState{Connected: true, Info: input.DeviceInfo{ID: 5}}
	macroFailure := &script.MacroError{File: "a.ts", Err: errors.New("macro failed")}

	r.rememberError(nil)
	if len(cues) != 0 {
		t.Fatalf("no error buzzed: %v", cues)
	}
	r.rememberError(errors.New("handler failed"))
	r.rememberError(errors.New("handler failed again"))
	r.rememberError(macroFailure) // the macro fails in the same episode
	if !reflect.DeepEqual(cues, []string{"3/5"}) {
		t.Fatalf("one episode buzzed %v, want once for slot 3 and gamepad 5", cues)
	}
	// A reload or a new runtime clears them.
	r.lastError, r.macroErrors = "", nil
	r.rememberError(macroFailure) // a macro alone starts an episode
	r.rememberError(errors.New("handler failed"))
	if !reflect.DeepEqual(cues, []string{"3/5", "3/5"}) {
		t.Fatalf("two episodes buzzed %v", cues)
	}
	// Clearing one kind of error does not end an episode the other keeps going.
	r.lastError = ""
	r.rememberError(errors.New("handler failed"))
	if len(cues) != 2 {
		t.Fatalf("an episode that a macro's error keeps going buzzed again: %v", cues)
	}

	// A slot without a gamepad has none to buzz; the error is kept all the same.
	cues = nil
	quiet := &scriptRuntime{def: def, index: 3}
	quiet.rememberError(errors.New("no gamepad"))
	if len(cues) != 0 || quiet.lastError != "no gamepad" {
		t.Fatalf("without a gamepad: cues %v, lastError %q", cues, quiet.lastError)
	}
	// A definition without the cue (a replay) only keeps the error.
	plain := &scriptRuntime{def: newScriptDefinition("profile.json", nil, false), pad: r.pad}
	plain.rememberError(errors.New("replay"))
	if plain.lastError != "replay" {
		t.Fatalf("lastError %q", plain.lastError)
	}
}

// Saving the script file installs a new runtime through the watcher: that ends
// the failure episode as an explicit reload does. The errors of the script and
// of a macro are cleared (script.status shows none), and the next failure
// buzzes again.
func TestScriptRuntimeSavedScriptEndsTheFailureEpisode(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.ts")
	write := func(source string) {
		t.Helper()
		if err := os.WriteFile(entry, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const source = `import { on } from "padmate";
on("south", "press", () => { throw new Error("boom"); });
`
	write(source)
	writeMacros(t, dir, map[string]string{"出错.ts": "export default function () { throw new Error(\"macro boom\"); }\n"})
	clk := clock.Real()
	arb := output.NewArbiter(clk, simtest.NewRecorder(clk), output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, true)
	def.macros = []string{"出错.ts"}
	var cues int
	def.errorCue = func(uint8, input.DeviceID) { cues++ }
	r, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Dispose() })
	r.UpdatePad(slot.PadSnapshot{Connected: true, Info: input.DeviceInfo{ID: 5}})
	fail := func() {
		t.Helper()
		if _, err := r.Dispatch(slot.ScriptEvent{Control: "south", Gesture: "press"}); err == nil {
			t.Fatal("the handler did not throw")
		}
		if err := r.RunMacro("出错.ts", slot.ScriptEvent{Control: "east", Gesture: "press"}); err == nil {
			t.Fatal("the macro did not throw")
		}
	}

	fail()
	fail()
	if v := r.InspectScript(false); cues != 1 || v.LastError == "" || len(v.Macros) != 1 || v.Macros[0].Error == "" {
		t.Fatalf("one failure episode: %d cues, status %+v", cues, v)
	}
	first := r.lifecycle.Current()
	write(source + "// saved\n")
	deadline := time.Now().Add(5 * time.Second)
	for r.lifecycle.Current() == first && time.Now().Before(deadline) {
		if err := r.RunDue(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if r.lifecycle.Current() == first {
		t.Fatal("the saved script was not installed")
	}
	if v := r.InspectScript(false); v.LastError != "" || len(v.Macros) != 0 || r.lastError != "" || r.macroErrors != nil {
		t.Fatalf("after the save: status %+v", v)
	}
	fail()
	if cues != 2 {
		t.Fatalf("the failure after a save buzzed %d times in all, want 2", cues)
	}
}

// A saved script that does not build installs nothing: the failure episode
// goes on, the errors of the macros stay, and the error of the build replaces
// the script's own lastError. The next good save ends the episode.
func TestScriptRuntimeBrokenSaveDoesNotEndTheFailureEpisode(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.ts")
	write := func(source string) {
		t.Helper()
		if err := os.WriteFile(entry, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const source = `import { on } from "padmate";
on("south", "press", () => { throw new Error("boom"); });
`
	write(source)
	writeMacros(t, dir, map[string]string{"出错.ts": "export default function () { throw new Error(\"macro boom\"); }\n"})
	clk := clock.Real()
	arb := output.NewArbiter(clk, simtest.NewRecorder(clk), output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, true)
	def.macros = []string{"出错.ts"}
	var cues int
	def.errorCue = func(uint8, input.DeviceID) { cues++ }
	r, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Dispose() })
	r.UpdatePad(slot.PadSnapshot{Connected: true, Info: input.DeviceInfo{ID: 5}})
	if _, err := r.Dispatch(slot.ScriptEvent{Control: "south", Gesture: "press"}); err == nil || cues != 1 {
		t.Fatalf("the first failure: %v, %d cues", err, cues)
	}
	if err := r.RunMacro("出错.ts", slot.ScriptEvent{Control: "east", Gesture: "press"}); err == nil {
		t.Fatal("the macro did not throw")
	}
	oldError, first := r.lastError, r.lifecycle.Current()
	write(`import {`)
	deadline := time.Now().Add(5 * time.Second)
	var reported error
	for reported == nil && time.Now().Before(deadline) {
		reported = r.RunDue()
		time.Sleep(time.Millisecond)
	}
	if reported == nil || r.lifecycle.Current() != first || cues != 1 {
		t.Fatalf("a broken save: error %v, same runtime %v, %d cues", reported, r.lifecycle.Current() == first, cues)
	}
	if r.lastError == "" || r.lastError == oldError || len(r.macroErrors) != 1 {
		t.Fatalf("a broken save: lastError %q (was %q), macro errors %v", r.lastError, oldError, r.macroErrors)
	}
	write(source + "// fixed\n")
	for r.lifecycle.Current() == first && time.Now().Before(deadline) {
		_ = r.RunDue()
		time.Sleep(time.Millisecond)
	}
	if r.lifecycle.Current() == first || r.lastError != "" || r.macroErrors != nil {
		t.Fatalf("the good save: same runtime %v, lastError %q, macro errors %v", r.lifecycle.Current() == first, r.lastError, r.macroErrors)
	}
}

// A script saved while the runtime waits out the backoff of its automatic
// restart is the recovery: it ends the failure episode like any other install,
// and the restart that was waiting does not install a second runtime over it.
func TestScriptRuntimeSavedScriptDuringBackoffEndsTheFailureEpisode(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.ts")
	const source = `import { on } from "padmate";
on("south", "press", () => { throw new Error("boom"); });
`
	if err := os.WriteFile(entry, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake()
	arb := output.NewArbiter(clk, simtest.NewRecorder(clk), output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, true)
	var cues int
	def.errorCue = func(uint8, input.DeviceID) { cues++ }
	r, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Dispose() })
	r.UpdatePad(slot.PadSnapshot{Connected: true, Info: input.DeviceInfo{ID: 5}})
	press := func() {
		t.Helper()
		if _, err := r.Dispatch(slot.ScriptEvent{Control: "south", Gesture: "press"}); err == nil {
			t.Fatal("the handler did not throw")
		}
	}
	press()
	r.lifecycle.RecordInterruption(clk.Now())
	if r.lifecycle.Current() != nil || r.lastError == "" {
		t.Fatalf("the interruption: current %v, lastError %q", r.lifecycle.Current(), r.lastError)
	}
	if err := os.WriteFile(entry, []byte(source+"// saved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The file is polled on the fake clock, and the backoff is a second long.
	deadline := time.Now().Add(10 * time.Second)
	for r.lifecycle.Current() == nil && time.Now().Before(deadline) {
		clk.Advance(time.Millisecond)
		if err := r.RunDue(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	saved := r.lifecycle.Current()
	if saved == nil || r.lastError != "" || r.macroErrors != nil {
		t.Fatalf("the saved script: current %v, lastError %q", saved, r.lastError)
	}
	for range 3000 {
		clk.Advance(time.Millisecond)
		if err := r.RunDue(); err != nil {
			t.Fatal(err)
		}
		if r.lifecycle.Current() != saved {
			break
		}
	}
	if v := r.InspectScript(false); r.lifecycle.Current() != saved || v.RestartCount != 0 || v.State != "Running" {
		t.Fatalf("the backoff restart replaced the saved runtime: status %+v", v)
	}
	press()
	if cues != 2 {
		t.Fatalf("the failure after the save buzzed %d times in all, want 2", cues)
	}
}

// The runtime that the lifecycle restarts by itself after an interruption is
// not a reload: the errors stay, and the same failing script does not buzz
// again for failing again after its restart (SCRIPTING 4.2).
func TestScriptRuntimeBackoffRestartKeepsTheFailureEpisode(t *testing.T) {
	dir := t.TempDir()
	const source = `import { on } from "padmate";
on("south", "press", () => { throw new Error("boom"); });
`
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake()
	arb := output.NewArbiter(clk, simtest.NewRecorder(clk), output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	var cues int
	def.errorCue = func(uint8, input.DeviceID) { cues++ }
	r, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Dispose() })
	r.UpdatePad(slot.PadSnapshot{Connected: true, Info: input.DeviceInfo{ID: 5}})
	press := func() {
		t.Helper()
		if _, err := r.Dispatch(slot.ScriptEvent{Control: "south", Gesture: "press"}); err == nil {
			t.Fatal("the handler did not throw")
		}
	}
	press()
	first := r.lifecycle.Current()

	r.lifecycle.RecordInterruption(clk.Now())
	deadline := time.Now().Add(5 * time.Second)
	for (r.lifecycle.Current() == nil || r.lifecycle.State() != script.StateRunning) && time.Now().Before(deadline) {
		clk.Advance(time.Second)
		if err := r.RunDue(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if r.lifecycle.Current() == nil || r.lifecycle.Current() == first {
		t.Fatal("the lifecycle did not restart the runtime")
	}
	if v := r.InspectScript(false); v.LastError == "" || v.RestartCount != 1 {
		t.Fatalf("after the restart: status %+v", v)
	}
	press()
	if cues != 1 {
		t.Fatalf("the failure after a restart buzzed again: %d cues", cues)
	}
}

// recordedEffects is a script.Effects that records its calls as text.
type recordedEffects struct {
	mu    sync.Mutex
	calls []string
}

func (e *recordedEffects) add(format string, a ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, fmt.Sprintf(format, a...))
}

func (e *recordedEffects) take() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	calls := e.calls
	e.calls = nil
	return calls
}

func (*recordedEffects) Switches(uint8) (rumble, led bool) { return true, true }
func (e *recordedEffects) Rumble(slot uint8, dev input.DeviceID, steps []haptic.Step) {
	e.add("rumble %d/%d %v", slot, dev, steps)
}

func (e *recordedEffects) Haptic(slot uint8, dev input.DeviceID, p haptic.Preset) {
	e.add("haptic %d/%d %v", slot, dev, p)
}

func (e *recordedEffects) StopRumble(slot uint8, dev input.DeviceID) {
	e.add("stop %d/%d", slot, dev)
}

func (e *recordedEffects) Light(slot uint8, dev input.DeviceID, c haptic.RGB) {
	e.add("light %d/%d %v", slot, dev, c)
}

func (e *recordedEffects) ReleaseLight(slot uint8, dev input.DeviceID) {
	e.add("release %d/%d", slot, dev)
}

func (e *recordedEffects) Cancel(slot uint8, dev input.DeviceID) {
	e.add("cancel %d/%d", slot, dev)
}

// A reload swaps in a new runtime, whose onActivate starts an effect: the old
// runtime's effects end before it, so the cancel of the old one does not follow
// and end the new one's; pausing, and the end, cancel what the runtime has.
func TestScriptRuntimeEffectsAcrossAReload(t *testing.T) {
	dir := t.TempDir()
	const source = `import { defineScript, pad } from "padmate";
export default defineScript({ onActivate() { pad.haptic("tick"); } });`
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake()
	arb := output.NewArbiter(clk, simtest.NewRecorder(clk), output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	sink := &recordedEffects{}
	def.environment = new(script.Environment)
	def.environment.SetEffects(sink)
	r, err := newScriptRuntime(def, 3, 1, clk, arb, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Dispose() })
	r.UpdatePad(slot.PadSnapshot{Connected: true, Info: input.DeviceInfo{ID: 7, Capabilities: input.Capabilities{Known: true, Rumble: true}}})
	settle := func(what string, done func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !done() && time.Now().Before(deadline) {
			clk.Advance(time.Millisecond)
			if err := r.RunDue(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Millisecond)
		}
		if !done() {
			t.Fatalf("%s did not happen", what)
		}
	}
	running := func() bool { return r.pending == nil && r.lifecycle != nil && r.lifecycle.Current() != nil }

	if err := r.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	settle("the first runtime", running)
	if got, want := sink.take(), []string{"haptic 3/7 tick"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first runtime: %v, want %v", got, want)
	}
	first := r.lifecycle.Current()

	if err := r.Reload(2); err != nil {
		t.Fatal(err)
	}
	settle("the swap", func() bool { return running() && r.lifecycle.Current() != first })
	if got, want := sink.take(), []string{"cancel 3/7", "haptic 3/7 tick"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after the reload: %v, want the old runtime's cancel and then the new one's effect", got)
	}

	// A pause ends the runtime's effects, once; the resume's runtime starts new ones.
	if err := r.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	if got, want := sink.take(), []string{"cancel 3/7"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("at the pause: %v, want %v", got, want)
	}
	if err := r.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	settle("the resumed runtime", running)
	if got, want := sink.take(), []string{"haptic 3/7 tick"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after the resume: %v, want %v", got, want)
	}
	if err := r.Dispose(); err != nil {
		t.Fatal(err)
	}
	if got, want := sink.take(), []string{"cancel 3/7"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("at the end: %v, want %v", got, want)
	}
}
