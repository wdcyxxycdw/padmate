package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/publicmirror"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/dtsgen"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

func readM6Example(t *testing.T, name string) (string, *engine.Program) {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "..", "examples", "scripts", name, "profile.json"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, ds := profile.Parse(data)
	if ds.HasErrors() {
		t.Fatalf("%s: %v", name, ds)
	}
	if p.Script == nil || p.Script.Entry != "main.ts" || p.Script.API != profile.CurrentScriptAPI {
		t.Fatalf("%s must declare local main.ts with API %s", name, profile.CurrentScriptAPI)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	return path, prog
}

type m6ExampleSim struct {
	clk *clock.Fake
	s   *slot.Slot
	r   *simtest.Recorder
}

func newM6ExampleSim(t *testing.T, name string) *m6ExampleSim {
	t.Helper()
	return newM6ExampleSimTraced(t, name, nil)
}

// newM6ExampleSimTraced is newM6ExampleSim with a Trace for the slot's
// decisions.
func newM6ExampleSimTraced(t *testing.T, name string, trace func(engine.Decision)) *m6ExampleSim {
	t.Helper()
	path, prog := readM6Example(t, name)
	clk := clock.NewFake()
	recorder := simtest.NewRecorder(clk)
	arb := output.NewArbiter(clk, recorder, output.ArbiterOptions{MinPress: 20 * time.Millisecond, MinGap: 20 * time.Millisecond})
	def := newScriptDefinition(path, prog.Profile().Script, false)
	r, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	s, err := slot.New(slot.Config{Gen: 1, Program: prog, Arbiter: arb, Clock: clk, Script: r, Trace: trace})
	if err != nil {
		t.Fatal(err)
	}
	h := &m6ExampleSim{clk: clk, s: s, r: recorder}
	t.Cleanup(func() {
		_ = s.ReleaseAll()
		_ = s.Dispose()
		simtest.AssertNoneHeld(t, arb, recorder)
	})
	s.Handle(input.Event{Kind: input.DeviceAdded, Device: 1, Info: &input.DeviceInfo{ID: 1, IsGamepad: true}})
	return h
}

func (h *m6ExampleSim) advance(t *testing.T, d time.Duration) {
	t.Helper()
	for remaining := d; remaining > 0; {
		step := min(remaining, 8*time.Millisecond)
		h.clk.Advance(step)
		h.s.Advance()
		remaining -= step
	}
	if errs := h.s.Errors(); len(errs) != 0 {
		t.Fatalf("example runtime: %v", errs)
	}
}

func (h *m6ExampleSim) button(name string, down bool) {
	kind := input.ButtonUp
	if down {
		kind = input.ButtonDown
	}
	h.s.Handle(input.Event{Kind: kind, Device: 1, Control: name})
}

func (h *m6ExampleSim) downs() []string {
	var out []string
	for _, edge := range h.r.Edges() {
		if edge.Kind == simtest.Down {
			out = append(out, edge.Name)
		}
	}
	return out
}

// These checks load the shipped profile and script and drive the production
// engine, Slot, scriptRuntime and Arbiter, including physical press/gap limits.
func TestM6ExampleStickRateAndDeadzone(t *testing.T) {
	for _, tc := range []struct {
		name string
		y    int16
		key  string
		min  int
		max  int
	}{
		{"small upward tilt", -6600, "F17", 3, 4},
		{"full upward tilt", -32768, "F17", 20, 20},
		{"full downward tilt", 32767, "F18", 20, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newM6ExampleSim(t, "stick-rate")
			h.s.Handle(input.Event{Kind: input.AxisMotion, Device: 1, Control: "righty", Value: tc.y})
			h.advance(t, time.Second)
			got := h.downs()
			if len(got) < tc.min || len(got) > tc.max {
				t.Fatalf("%d taps in 1 s, want %d..%d", len(got), tc.min, tc.max)
			}
			for _, key := range got {
				if key != tc.key {
					t.Fatalf("taps = %v, want only %s", got, tc.key)
				}
			}
			h.s.Handle(input.Event{Kind: input.AxisMotion, Device: 1, Control: "righty", Value: 2000})
			h.advance(t, time.Second)
			if after := h.downs(); len(after) != len(got) {
				t.Fatalf("output continued in the deadzone: %v", after[len(got):])
			}
			if len(h.r.Down()) != 0 {
				t.Fatalf("pending tap did not release in the deadzone: %v", h.r.Down())
			}
		})
	}
}

// Only the initial axis events are sent. The shipped script must continue
// through the real slot/output deadlines, then stop after entering deadzone.
func TestM6ExampleStickIdleSlot(t *testing.T) {
	path, prog := readM6Example(t, "stick-rate")
	clk := clock.Real()
	recorder := simtest.NewSyncRecorder(clk)
	out := output.StartLoop(clk, recorder, output.LoopOptions{Arbiter: output.ArbiterOptions{MinPress: 20 * time.Millisecond, MinGap: 20 * time.Millisecond}})
	def := newScriptDefinition(path, prog.Profile().Script, false)
	r, err := newScriptRuntime(def, 0, 1, clk, out.Remote())
	if err != nil {
		t.Fatal(err)
	}
	s, err := slot.New(slot.Config{Gen: 1, Program: prog, Arbiter: out.Remote(), Clock: clk, Script: r})
	if err != nil {
		t.Fatal(err)
	}
	edges := make(chan slot.Edge)
	jobs := make(chan func(*slot.Slot))
	done := make(chan error, 1)
	go func() { done <- slot.RunLoop(context.Background(), slot.LoopConfig{Slot: s, Edges: edges, Jobs: jobs}) }()
	t.Cleanup(func() {
		close(edges)
		if err := <-done; err != nil {
			t.Error(err)
		}
		if err := out.Stop(); err != nil {
			t.Error(err)
		}
		recorder.With(func(r *simtest.Recorder) { simtest.AssertNoneHeld(t, nil, r) })
	})
	edges <- slot.Edge{Event: input.Event{Kind: input.DeviceAdded, Device: 1, Info: &input.DeviceInfo{ID: 1, IsGamepad: true}}}
	edges <- slot.Edge{Event: input.Event{Kind: input.AxisMotion, Device: 1, Control: "righty", Value: -32768}}
	<-time.After(time.Second)
	count := func() int {
		n := 0
		recorder.With(func(r *simtest.Recorder) {
			for _, e := range r.Edges() {
				if e.Kind == simtest.Down && e.Name == "F17" {
					n++
				}
			}
		})
		return n
	}
	if n := count(); n < 18 || n > 21 {
		t.Fatalf("idle slot produced %d taps in 1 s, want approximately 20", n)
	}
	edges <- slot.Edge{Event: input.Event{Kind: input.AxisMotion, Device: 1, Control: "righty", Value: 0}}
	ack := make(chan struct{})
	jobs <- func(*slot.Slot) { close(ack) }
	<-ack
	before := count()
	<-time.After(80 * time.Millisecond)
	if n := count(); n != before {
		t.Fatalf("idle slot emitted %d taps after deadzone", n-before)
	}
}

func TestM6ScriptPhysicalTapRateCeiling(t *testing.T) {
	for _, tc := range []struct {
		name     string
		gap      time.Duration
		min, max int
	}{
		{"production 20ms press and gap", 20 * time.Millisecond, 24, 25},
		{"control without gap", 0, 26, 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			const source = `import {key,onTick} from "padmate"; import {RateAccumulator} from "padmate/math";
const acc=new RateAccumulator();onTick(dt=>{if(acc.step(100,dt)>0)void key.tap("F17",{holdMs:20});});`
			if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			clk := clock.NewFake()
			recorder := simtest.NewRecorder(clk)
			arb := output.NewArbiter(clk, recorder, output.ArbiterOptions{MinPress: 20 * time.Millisecond, MinGap: tc.gap})
			def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI, TickHz: 125}, false)
			r, err := newScriptRuntime(def, 0, 1, clk, arb)
			if err != nil {
				t.Fatal(err)
			}
			s := newScriptTestSlot(t, clk, arb, r, `{}`)
			t.Cleanup(func() { _ = s.ReleaseAll(); _ = s.Dispose(); simtest.AssertNoneHeld(t, arb, recorder) })
			for range 1000 {
				clk.Advance(time.Millisecond)
				s.Advance()
			}
			// Same-key requests while a tap is pending are expected to drop.
			// The control case has the identical host guard and exceeds 25 Hz,
			// proving that the production ceiling comes from Arbiter timing.
			for _, err := range s.Errors() {
				if !errors.Is(err, script.ErrOutputBudget) {
					t.Fatal(err)
				}
			}
			var times []clock.Time
			for _, edge := range recorder.Edges() {
				if edge.Kind == simtest.Down && edge.Name == "F17" {
					times = append(times, edge.T)
				}
			}
			if len(times) < tc.min || len(times) > tc.max {
				t.Fatalf("100 Hz request produced %d physical downs in 1 s, want %d..%d", len(times), tc.min, tc.max)
			}
			t.Logf("100 Hz requested, %d physical downs in 1 s", len(times))
			for i := 1; i < len(times); i++ {
				if gap := times[i] - times[i-1]; gap < clock.Time(20*time.Millisecond+tc.gap) {
					t.Fatalf("physical down gap %v violates press/gap timing", time.Duration(gap))
				}
			}
			if err := s.ReleaseAll(); err != nil {
				t.Fatal(err)
			}
			before := len(recorder.Records())
			clk.Advance(time.Second)
			s.Advance()
			if len(recorder.Records()) != before {
				t.Fatal("queued high-rate taps continued after release-all")
			}
		})
	}
}

func TestM6ExampleTabmateGesturesAndLayers(t *testing.T) {
	h := newM6ExampleSim(t, "tabmate")
	h.button("west", true)
	h.advance(t, 40*time.Millisecond)
	h.button("west", false)
	h.advance(t, 40*time.Millisecond)
	if got := h.downs(); !slices.Equal(got, []string{"F13"}) {
		t.Fatalf("short west press = %v, want pen tap", got)
	}
	h.button("west", true)
	h.advance(t, 250*time.Millisecond)
	if got := h.r.Down(); len(got) != 1 || got[0].Key.String() != "F14" {
		t.Fatalf("held west = %v, want temporary eraser", got)
	}
	h.button("west", false)
	h.advance(t, 40*time.Millisecond)
	if len(h.r.Down()) != 0 {
		t.Fatal("temporary eraser remained held after west release")
	}
	h.button("rightshoulder", true)
	h.button("south", true)
	h.button("south", false)
	h.advance(t, 40*time.Millisecond)
	h.button("rightshoulder", false)
	h.button("south", true)
	h.button("south", false)
	h.advance(t, 40*time.Millisecond)
	got := h.downs()
	if !slices.Equal(got, []string{"F13", "F14", "F15", "LCtrl", "Z"}) {
		t.Fatalf("temporary alt layer did not restore base: %v", got)
	}
	for range 2 {
		h.button("north", true)
		h.advance(t, 20*time.Millisecond)
		h.button("north", false)
		h.advance(t, 40*time.Millisecond)
	}
	h.advance(t, 220*time.Millisecond)
	h.button("south", true)
	h.button("south", false)
	h.advance(t, 40*time.Millisecond)
	got = h.downs()
	if got[len(got)-1] != "F15" || slices.Contains(got, "F19") {
		t.Fatalf("north double tap did not toggle alt without panel tap: %v", got)
	}
}

func TestM6ExampleTabmateHoldSafety(t *testing.T) {
	for _, tc := range []struct {
		name string
		stop func(*slot.Slot) error
	}{
		{"pause", func(s *slot.Slot) error { return s.SetPaused(true) }},
		{"releaseAll", (*slot.Slot).ReleaseAll},
		{"dispose", (*slot.Slot).Dispose},
		{"disconnect", func(s *slot.Slot) error { s.Handle(input.Event{Kind: input.DeviceRemoved, Device: 1}); return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newM6ExampleSim(t, "tabmate")
			h.button("west", true)
			h.advance(t, 250*time.Millisecond)
			if got := h.r.Down(); len(got) != 1 || got[0].Key.String() != "F14" {
				t.Fatalf("temporary eraser did not start: %v", got)
			}
			if err := tc.stop(h.s); err != nil {
				t.Fatal(err)
			}
			h.advance(t, time.Second)
			if got := h.r.Down(); len(got) != 0 {
				t.Fatalf("temporary eraser survived %s: %v", tc.name, got)
			}
		})
	}
}

func TestM6ExampleTabmateSingleTapWindow(t *testing.T) {
	h := newM6ExampleSim(t, "tabmate")
	h.button("north", true)
	h.advance(t, 20*time.Millisecond)
	h.button("north", false)
	h.advance(t, 200*time.Millisecond)
	if got := h.downs(); len(got) != 0 {
		t.Fatalf("single tap committed before the multi-tap window: %v", got)
	}
	h.advance(t, 64*time.Millisecond)
	if got := h.downs(); !slices.Equal(got, []string{"F19"}) {
		t.Fatalf("single tap after the window = %v, want panel tap", got)
	}
}

type m6ExampleSender struct {
	recorder *simtest.SyncRecorder
	events   chan reloadEvent
}

func (s *m6ExampleSender) Send(events []output.KeyEvent) (int, error) {
	n, err := s.recorder.Send(events)
	for _, e := range events[:n] {
		s.events <- reloadEvent{key: e.Key.String(), up: e.Up}
	}
	return n, err
}

type m6ReadyHandler struct{ ready chan struct{} }

func (h m6ReadyHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h m6ReadyHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Message == "cycleBrush ready" {
		h.ready <- struct{}{}
	}
	return nil
}
func (h m6ReadyHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h m6ReadyHandler) WithGroup(string) slog.Handler      { return h }

// The actual profile's named action passes through the real supervisor,
// engine and slot; reactivation and profile identity exercise session storage.
func TestM6ExampleCycleBrushSessionAndAction(t *testing.T) {
	path, prog := readM6Example(t, "cycle-brush")
	clk := clock.Real()
	recorder := simtest.NewSyncRecorder(clk)
	sender := &m6ExampleSender{recorder: recorder, events: make(chan reloadEvent, 128)}
	out := output.StartLoop(clk, sender, output.LoopOptions{Arbiter: output.ArbiterOptions{MinPress: 20 * time.Millisecond, MinGap: 20 * time.Millisecond}})
	ready := make(chan struct{}, 16)
	def := newScriptDefinition(path, prog.Profile().Script, false, slog.New(m6ReadyHandler{ready: ready}))
	events := make(chan input.Event, 16)
	sup, err := supervisor.New(supervisor.Config{Clock: clk, Events: events, Output: out, Program: prog, ProfileKey: "example-a", ScriptFactory: def.Factory, ScriptConfigured: def.Configured, Slots: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		if err := out.Stop(); err != nil {
			t.Error(err)
		}
		recorder.With(func(r *simtest.Recorder) { simtest.AssertNoneHeld(t, nil, r) })
	})
	waitReady := func() {
		t.Helper()
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("cycle-brush activation did not finish")
		}
	}
	press := func(control, want string) {
		t.Helper()
		events <- input.Event{Kind: input.ButtonDown, Device: 1, Control: control}
		events <- input.Event{Kind: input.ButtonUp, Device: 1, Control: control}
		select {
		case e := <-sender.events:
			if e.up || e.key != want {
				t.Fatalf("%s action output = %+v, want %s down", control, e, want)
			}
		case <-ctx.Done():
			t.Fatalf("%s action produced no %s", control, want)
		}
		select {
		case e := <-sender.events:
			if !e.up || e.key != want {
				t.Fatalf("action tap did not release: %+v", e)
			}
		case <-ctx.Done():
			t.Fatal("action tap did not release")
		}
	}
	events <- input.Event{Kind: input.DeviceAdded, Device: 1, Info: &input.DeviceInfo{ID: 1, IsGamepad: true}}
	waitReady()
	press("south", "F14")
	press("north", "F13")
	press("north", "F16")
	press("south", "F13")
	press("south", "F14")
	if _, err := sup.Reload(ctx, prog); err != nil {
		t.Fatal(err)
	}
	waitReady()
	press("south", "F15")
	if err := sup.Pause(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := sup.Pause(ctx, false); err != nil {
		t.Fatal(err)
	}
	waitReady()
	press("south", "F16")
	if _, err := sup.ReloadProfile(ctx, prog, "example-b"); err != nil {
		t.Fatal(err)
	}
	waitReady()
	press("south", "F14")
	if _, err := sup.ReloadProfile(ctx, prog, "example-a"); err != nil {
		t.Fatal(err)
	}
	waitReady()
	press("south", "F13")
}

func TestM6ExamplesTypecheck(t *testing.T) {
	tsc := os.Getenv("PADMATE_TEST_TSC")
	if tsc == "" {
		t.Skip("set PADMATE_TEST_TSC to the TypeScript compiler entry point")
	}
	for _, name := range []string{"stick-rate", "tabmate", "cycle-brush"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := dtsgen.Scaffold(dir, dtsgen.HostAPI, nil, nil); err != nil {
				t.Fatal(err)
			}
			source, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "scripts", name, "main.ts"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "main.ts"), source, 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), "node", tsc, "--noEmit", "--project", dir)
			if result, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("tsc %s: %v\n%s", name, err, result)
			}
		})
	}
}

// docsFeedbackExample is the script of docs/SCRIPTING.md section 8.7 (SCR-23):
// the first ts code block after its heading.
func docsFeedbackExample(t *testing.T) string {
	t.Helper()
	publicmirror.SkipIfMirror(t, "docs/SCRIPTING.md")
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "SCRIPTING.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	_, after, ok := strings.Cut(text, "\n### 8.7 ")
	if !ok {
		t.Fatal("SCRIPTING.md has no section 8.7")
	}
	_, code, ok := strings.Cut(after, "```ts\n")
	if !ok {
		t.Fatal("section 8.7 has no ts code block")
	}
	code, _, ok = strings.Cut(code, "```")
	if !ok {
		t.Fatal("the ts code block of section 8.7 does not end")
	}
	return code
}

// The example of SCRIPTING 8.7 runs as written: each button shows its member on
// a gamepad that can, and a gamepad that cannot gets nothing, whether the
// script asked first (host.has) or not (the haptic preset).
func TestScriptingDocFeedbackExample(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(docsFeedbackExample(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake()
	arb := output.NewArbiter(clk, simtest.NewRecorder(clk), output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	sink := &recordedEffects{}
	def.environment = new(script.Environment)
	def.environment.SetEffects(sink)
	r, err := newScriptRuntime(def, 3, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Dispose() })
	pad := func(caps input.Capabilities, power *input.Power) {
		snapshot := slot.PadSnapshot{Connected: true, Info: input.DeviceInfo{ID: 7, Capabilities: caps}}
		if power != nil {
			snapshot.Power, snapshot.HasPower = *power, true
		}
		r.UpdatePad(snapshot)
	}
	press := func(control, gesture string) []string {
		t.Helper()
		if _, err := r.Dispatch(slot.ScriptEvent{Control: control, Gesture: gesture}); err != nil {
			t.Fatalf("%s %s: %v", control, gesture, err)
		}
		return sink.take()
	}
	has := func(prefix string, calls []string) bool {
		return len(calls) == 1 && strings.HasPrefix(calls[0], prefix)
	}

	pad(input.Capabilities{Known: true, Rumble: true, LED: true, RGBLED: true}, nil)
	if got := press("south", "press"); !reflect.DeepEqual(got, []string{"haptic 3/7 tick"}) {
		t.Fatalf("south: %v", got)
	}
	if got := press("east", "press"); !has("rumble 3/7", got) {
		t.Fatalf("east: %v", got)
	}
	if got := press("north", "press"); !has("light 3/7", got) {
		t.Fatalf("north press: %v", got)
	}
	if got := press("north", "release"); !reflect.DeepEqual(got, []string{"release 3/7"}) {
		t.Fatalf("north release: %v", got)
	}
	// No battery reading yet; a low one; a good one.
	if got := press("west", "press"); len(got) != 0 {
		t.Fatalf("west without a reading: %v", got)
	}
	pad(input.Capabilities{Known: true, Rumble: true, LED: true, RGBLED: true}, &input.Power{State: input.PowerDischarging, Percent: 15})
	if got := press("west", "press"); !reflect.DeepEqual(got, []string{"haptic 3/7 error"}) {
		t.Fatalf("west with 15 %%: %v", got)
	}
	pad(input.Capabilities{Known: true, Rumble: true, LED: true, RGBLED: true}, &input.Power{State: input.PowerDischarging, Percent: 70})
	if got := press("west", "press"); len(got) != 0 {
		t.Fatalf("west with 70 %%: %v", got)
	}

	// A gamepad with neither motors nor a light: nothing reaches it.
	pad(input.Capabilities{Known: true}, nil)
	for _, c := range []string{"south", "east", "north"} {
		if got := press(c, "press"); len(got) != 0 {
			t.Fatalf("%s on a gamepad that cannot: %v", c, got)
		}
	}
	if got := press("north", "release"); len(got) != 0 {
		t.Fatalf("north release on a gamepad that cannot: %v", got)
	}
}

// The example of SCRIPTING 8.7 type-checks against the declarations of the
// current API, with the real compiler (PADMATE_TEST_TSC).
func TestScriptingDocFeedbackExampleTypechecks(t *testing.T) {
	tsc := os.Getenv("PADMATE_TEST_TSC")
	if tsc == "" {
		t.Skip("set PADMATE_TEST_TSC to the TypeScript compiler entry point")
	}
	dir := t.TempDir()
	if err := dtsgen.Scaffold(dir, dtsgen.HostAPI, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(docsFeedbackExample(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", tsc, "--noEmit", "--project", dir)
	if result, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tsc: %v\n%s", err, result)
	}
}

// A gesture that a script's handler takes leaves a decision with reason
// script, and a press that waits for its tap or hold leaves an unbound one
// that says what it waits for (ADR-0071, ENG-20): the decisions of the
// production engine, Slot and script runtime over the shipped tabmate example.
func TestM6ExampleTabmateDecisions(t *testing.T) {
	var got []string
	h := newM6ExampleSimTraced(t, "tabmate", func(d engine.Decision) {
		if d.Reason != engine.ReasonEnd {
			got = append(got, d.Control.String()+" "+d.Gesture.String()+" "+d.Reason.String())
		}
	})
	h.button("south", true) // a handler of the press
	h.button("south", false)
	h.button("west", true) // tap or hold: the press waits
	h.advance(t, 40*time.Millisecond)
	h.button("west", false) // a tap
	h.advance(t, 40*time.Millisecond)
	h.button("west", true)
	h.advance(t, 300*time.Millisecond) // a hold
	h.button("west", false)
	h.advance(t, 40*time.Millisecond)
	h.button("east", true) // nothing at all
	h.button("east", false)
	want := []string{
		"south press script",
		"west press unbound",
		"west tap script",
		"west press unbound",
		"west hold script",
		"east press unbound",
	}
	if !slices.Equal(got, want) {
		t.Errorf("decisions\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
