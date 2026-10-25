package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

// Exercise the actual Slot -> scriptRuntime -> Arbiter path. No SendInput is used.
func TestScriptPauseCancelsAsyncAndRebuildsOnResume(t *testing.T) {
	dir := t.TempDir()
	source := `import { defineScript, on, key, sleep, every } from "padmate";
export default defineScript({
  setup() {
    on("south", "press", async () => { key.down("X"); await sleep(10); key.down("Y"); });
    on("south", "release", () => { key.tap("Z"); });
    every(20, () => key.tap("F13"));
  },
  onActivate() { key.down("A"); }
});`
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake()
	recorder := simtest.NewRecorder(clk)
	arb := output.NewArbiter(clk, recorder, output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	r, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	s := newScriptTestSlot(t, clk, arb, r, `{}`)
	t.Cleanup(func() {
		_ = s.ReleaseAll()
		_ = s.Dispose()
		simtest.AssertNoneHeld(t, arb, recorder)
	})
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	if len(recorder.Down()) != 2 {
		t.Fatalf("before pause held = %v, want A and X", recorder.Down())
	}
	if err := s.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	if len(recorder.Down()) != 0 {
		t.Fatalf("pause left keys held: %v", recorder.Down())
	}
	before := len(recorder.Records())
	clk.Advance(30 * time.Millisecond)
	s.Advance()
	if len(recorder.Records()) != before {
		t.Fatalf("old timer or await emitted during pause: %v", recorder.Edges()[before:])
	}
	if err := s.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	// Resume schedules setup on a worker. Slot advancement adopts it.
	deadline := time.Now().Add(time.Second)
	for r.pending != nil && time.Now().Before(deadline) {
		clk.Advance(time.Millisecond)
		s.Advance()
		time.Sleep(time.Millisecond)
	}
	if r.pending != nil || r.lifecycle == nil || r.lifecycle.Current() == nil {
		t.Fatal("resume did not adopt a fresh runtime")
	}
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
	for _, rec := range recorder.Records()[before:] {
		if rec.Event.Key.String() == "Z" || rec.Event.Key.String() == "Y" {
			t.Fatalf("cross-pause release or await emitted: %v", recorder.Edges())
		}
	}
	before = len(recorder.Records())
	clk.Advance(20 * time.Millisecond)
	s.Advance()
	if len(recorder.Records()) == before {
		t.Fatal("setup every timer was not restored after resume")
	}
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	if len(recorder.Down()) < 2 {
		t.Fatalf("fresh press did not reach rebuilt script: %v", recorder.Down())
	}
}

func TestScriptInitiallyPausedNeverActivatesUntilResume(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(`import { defineScript, key } from "padmate";
export default defineScript({ onActivate() { key.down("A"); } });`), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake()
	recorder := simtest.NewRecorder(clk)
	arb := output.NewArbiter(clk, recorder, output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	runtime, err := def.Factory(0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	r := runtime.(*scriptRuntime)
	s, err := slot.New(slot.Config{Index: 0, Gen: 1, Program: newScriptTestProgram(t, `{}`), Arbiter: arb, Script: r, Clock: clk, Paused: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.ReleaseAll(); _ = s.Dispose(); simtest.AssertNoneHeld(t, arb, recorder) })
	s.Advance()
	if r.lifecycle != nil || len(recorder.Records()) != 0 {
		t.Fatal("initially paused slot activated its script")
	}
	s.Handle(input.Event{Kind: input.DeviceAdded})
	s.Resync(slot.State{})
	if err := s.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for r.pending != nil && time.Now().Before(deadline) {
		clk.Advance(time.Millisecond)
		s.Advance()
		time.Sleep(time.Millisecond)
	}
	if len(recorder.Down()) != 1 || recorder.Down()[0].Key.String() != "A" {
		t.Fatalf("resume did not activate: %v", recorder.Edges())
	}
}

func TestLiveScriptFactoryWaitsForInitialResync(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(`import { defineScript, key } from "padmate";
export default defineScript({ onActivate() { key.down("A"); } });`), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake()
	recorder := simtest.NewRecorder(clk)
	arb := output.NewArbiter(clk, recorder, output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	runtime, err := def.Factory(0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	r := runtime.(*scriptRuntime)
	s, err := slot.New(slot.Config{Index: 0, Gen: 1, Program: newScriptTestProgram(t, `{}`), Arbiter: arb, Script: r, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.ReleaseAll(); _ = s.Dispose(); simtest.AssertNoneHeld(t, arb, recorder) })
	s.Handle(input.Event{Kind: input.DeviceAdded})
	s.Advance()
	if r.pending != nil || r.lifecycle != nil || len(recorder.Records()) != 0 {
		t.Fatal("DeviceAdded activated script before initial Resync")
	}
	s.Resync(slot.State{})
	deadline := time.Now().Add(time.Second)
	for r.pending != nil && time.Now().Before(deadline) {
		clk.Advance(time.Millisecond)
		s.Advance()
		time.Sleep(time.Millisecond)
	}
	if len(recorder.Down()) != 1 || recorder.Down()[0].Key.String() != "A" {
		t.Fatalf("initial Resync did not activate script: %v", recorder.Edges())
	}
}

func TestScriptPauseDiscardsPendingWorkerCandidate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(`import { defineScript } from "padmate";
export default defineScript({ setup() {} });`), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake()
	recorder := simtest.NewRecorder(clk)
	arb := output.NewArbiter(clk, recorder, output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	r, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	s := newScriptTestSlot(t, clk, arb, r, `{}`)
	t.Cleanup(func() { _ = s.ReleaseAll(); _ = s.Dispose(); simtest.AssertNoneHeld(t, arb, recorder) })
	started := make(chan struct{})
	release := make(chan struct{})
	var builds atomic.Int32
	r.buildOverride = func(context.Context, string, *profile.ScriptConfig) (*goja.Program, error) {
		if builds.Add(1) == 1 {
			close(started)
			<-release
		}
		return goja.Compile("candidate.js", `globalThis.__padmate_bundle = { default: { onActivate() { __padmate.key.down("Y"); } } };`, false)
	}
	if err := s.Reload(newScriptTestProgram(t, `{}`), 2); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("candidate worker did not start")
	}
	if err := s.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	drained := r.cancelDone
	clk.Advance(time.Second)
	s.Advance()
	if len(recorder.Records()) != 0 || r.lifecycle != nil {
		t.Fatalf("worker crossed pause boundary: %v", recorder.Edges())
	}
	if err := s.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for r.pending != nil && time.Now().Before(deadline) {
		clk.Advance(time.Millisecond)
		s.Advance()
		time.Sleep(time.Millisecond)
	}
	if len(recorder.Down()) != 1 || recorder.Down()[0].Key.String() != "Y" {
		t.Fatalf("resume did not activate fresh worker candidate: %v", recorder.Edges())
	}
	newOwner := r.lifecycle.Current().Output()[0].Owner
	close(release)
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("canceled worker did not finish")
	}
	if len(recorder.Down()) != 1 || recorder.Down()[0].Key.String() != "Y" || r.lifecycle.Current().Output()[0].Owner != newOwner {
		t.Fatalf("late canceled candidate released resumed owner: %v", recorder.Edges())
	}
}

func TestScriptDeviceRemovalAndSlotReleaseCancelLateOutput(t *testing.T) {
	for _, boundary := range []string{"device removed", "slot release"} {
		t.Run(boundary, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(`import { defineScript, key, every } from "padmate";
export default defineScript({
  setup() { every(10, () => key.down("Y")); },
  onActivate() { key.down("X"); }
});`), 0o600); err != nil {
				t.Fatal(err)
			}
			clk := clock.NewFake()
			recorder := simtest.NewRecorder(clk)
			arb := output.NewArbiter(clk, recorder, output.ArbiterOptions{})
			def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
			r, err := newScriptRuntime(def, 0, 1, clk, arb)
			if err != nil {
				t.Fatal(err)
			}
			s := newScriptTestSlot(t, clk, arb, r, `{}`)
			t.Cleanup(func() { _ = s.ReleaseAll(); _ = s.Dispose(); simtest.AssertNoneHeld(t, arb, recorder) })
			if len(recorder.Down()) != 1 {
				t.Fatal("onActivate did not hold X")
			}
			if boundary == "device removed" {
				s.Handle(input.Event{Kind: input.DeviceRemoved})
			} else if err := s.ReleaseAll(); err != nil {
				t.Fatal(err)
			}
			if len(recorder.Down()) != 0 {
				t.Fatalf("%s left keys held: %v", boundary, recorder.Down())
			}
			before := len(recorder.Records())
			clk.Advance(30 * time.Millisecond)
			s.Advance()
			if len(recorder.Records()) != before {
				t.Fatalf("%s emitted late output: %v", boundary, recorder.Edges()[before:])
			}
			if boundary == "device removed" {
				s.Handle(input.Event{Kind: input.DeviceAdded})
				deadline := time.Now().Add(time.Second)
				for r.pending != nil && time.Now().Before(deadline) {
					clk.Advance(time.Millisecond)
					s.Advance()
					time.Sleep(time.Millisecond)
				}
				if len(recorder.Down()) != 1 || recorder.Down()[0].Key.String() != "X" {
					t.Fatalf("reconnected device did not rebuild script: %v", recorder.Edges())
				}
			} else {
				// A new physical press restarts setup asynchronously after an
				// emergency slot release; the release itself must stay quiet.
				s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
				deadline := time.Now().Add(time.Second)
				for r.pending != nil && time.Now().Before(deadline) {
					clk.Advance(time.Millisecond)
					s.Advance()
					time.Sleep(time.Millisecond)
				}
				if len(recorder.Down()) != 1 || recorder.Down()[0].Key.String() != "X" {
					t.Fatalf("fresh press did not rebuild script: %v", recorder.Edges())
				}
			}
		})
	}
}

func TestScriptResetResyncDoesNotDispatchOrphanRelease(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(`import { defineScript, on, key } from "padmate";
export default defineScript({ setup() { on("south", "release", () => key.tap("Z")); } });`), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake()
	recorder := simtest.NewRecorder(clk)
	arb := output.NewArbiter(clk, recorder, output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	r, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	s := newScriptTestSlot(t, clk, arb, r, `{}`)
	t.Cleanup(func() { _ = s.ReleaseAll(); _ = s.Dispose(); simtest.AssertNoneHeld(t, arb, recorder) })
	s.Reset()
	st := slot.State{}
	st.Apply(input.Event{Kind: input.ButtonDown, Control: "south"})
	s.Resync(st)
	deadline := time.Now().Add(time.Second)
	for r.pending != nil && time.Now().Before(deadline) {
		clk.Advance(time.Millisecond)
		s.Advance()
		time.Sleep(time.Millisecond)
	}
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
	if len(recorder.Records()) != 0 {
		t.Fatalf("orphan release after resync emitted: %v", recorder.Edges())
	}
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
	if len(recorder.Records()) == 0 {
		t.Fatal("fresh cycle did not reach script after resync")
	}
}

func TestScriptSlotDisposeSuppressesTeardownOutput(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(`import { defineScript, key, every } from "padmate";
export default defineScript({
  setup() { every(10, () => key.down("Y")); },
  onActivate() { key.down("X"); },
  teardown() { key.down("Z"); }
});`), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake()
	recorder := simtest.NewRecorder(clk)
	arb := output.NewArbiter(clk, recorder, output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	r, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	s := newScriptTestSlot(t, clk, arb, r, `{}`)
	if err := s.Dispose(); err != nil {
		t.Fatal(err)
	}
	clk.Advance(20 * time.Millisecond)
	s.Advance()
	if len(recorder.Down()) != 0 {
		t.Fatalf("disposed slot left key held: %v", recorder.Down())
	}
	got := recorder.Edges()
	if len(got) != 2 || got[0].Name != "X" || got[0].Kind != simtest.Down || got[1].Name != "X" || got[1].Kind != simtest.Up {
		t.Fatalf("teardown or old timer emitted: %v", got)
	}
	simtest.AssertNoneHeld(t, arb, recorder)
}

func TestScriptCandidateWallStopsOldRuntimeUntilReload(t *testing.T) {
	for _, phase := range []string{"build", "onActivate"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(`import { defineScript, key } from "padmate";
export default defineScript({ onActivate() { key.down("X"); } });`), 0o600); err != nil {
				t.Fatal(err)
			}
			clk := clock.NewFake()
			recorder := simtest.NewRecorder(clk)
			arb := output.NewArbiter(clk, recorder, output.ArbiterOptions{})
			def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
			r, err := newScriptRuntime(def, 0, 1, clk, arb)
			if err != nil {
				t.Fatal(err)
			}
			s := newScriptTestSlot(t, clk, arb, r, `{}`)
			t.Cleanup(func() { _ = s.ReleaseAll(); _ = s.Dispose(); simtest.AssertNoneHeld(t, arb, recorder) })
			if len(recorder.Down()) != 1 {
				t.Fatal("old runtime did not hold X")
			}
			r.buildOverride = func(context.Context, string, *profile.ScriptConfig) (*goja.Program, error) {
				if phase == "build" {
					return nil, &script.BudgetError{Clock: "wall", Budget: 2 * time.Second}
				}
				return goja.Compile("activate.js", `globalThis.__padmate_bundle = { default: { onActivate() { failWall(); __padmate.key.down("Y"); } } };`, false)
			}
			if err := r.Reload(2); err != nil {
				t.Fatal(err)
			}
			if phase == "onActivate" {
				var result script.LoadResult
				select {
				case result = <-r.pending.result:
				case <-time.After(time.Second):
					t.Fatal("candidate setup did not finish")
				}
				if result.Err != nil {
					t.Fatal(result.Err)
				}
				if err := result.Host.VM.Set("failWall", func() {
					result.Host.VM.Interrupt(&script.BudgetError{Clock: "wall", Budget: 2 * time.Second})
				}); err != nil {
					t.Fatal(err)
				}
				ready := make(chan script.LoadResult, 1)
				ready <- result
				r.pending.result = ready
			}
			deadline := time.Now().Add(time.Second)
			for r.pending != nil && time.Now().Before(deadline) {
				clk.Advance(time.Millisecond)
				s.Advance()
				time.Sleep(time.Millisecond)
			}
			if !r.wallStopped || r.lifecycle != nil || len(recorder.Down()) != 0 {
				t.Fatalf("wall candidate left old runtime active: stopped=%v held=%v", r.wallStopped, recorder.Down())
			}
			before := len(recorder.Records())
			clk.Advance(time.Minute)
			s.Advance()
			if len(recorder.Records()) != before || r.pending != nil {
				t.Fatalf("wall candidate restarted automatically: %v", recorder.Edges()[before:])
			}
			r.buildOverride = func(context.Context, string, *profile.ScriptConfig) (*goja.Program, error) {
				return goja.Compile("recovered.js", `globalThis.__padmate_bundle = { default: { onActivate() { __padmate.key.down("Y"); } } };`, false)
			}
			if err := r.Reload(3); err != nil {
				t.Fatal(err)
			}
			deadline = time.Now().Add(time.Second)
			for r.pending != nil && time.Now().Before(deadline) {
				clk.Advance(time.Millisecond)
				s.Advance()
				time.Sleep(time.Millisecond)
			}
			if r.wallStopped || len(recorder.Down()) != 1 || recorder.Down()[0].Key.String() != "Y" {
				t.Fatalf("manual reload did not recover: %v", recorder.Edges())
			}
		})
	}
}

// This is the padmatectl release-all entry path: Supervisor.ReleaseAll must
// reach each slot, not merely clear T-output's current Arbiter references.
func TestSupervisorEmergencyReleaseCancelsScriptTimers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(`import { defineScript, key, every } from "padmate";
export default defineScript({
  setup() { every(10, () => key.down("Y")); },
  onActivate() { key.down("X"); }
});`), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake()
	recorder := simtest.NewSyncRecorder(clk)
	out := output.StartLoop(clk, recorder, output.LoopOptions{})
	events := make(chan input.Event)
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	sup, err := supervisor.New(supervisor.Config{
		Clock: clk, Events: events, Output: out, Program: newScriptTestProgram(t, `{}`), Slots: 1,
		ScriptFactory: func(index uint8, gen uint32, clk clock.Clock, arb slot.Arbiter) (slot.ScriptRuntime, error) {
			return newScriptRuntime(def, index, gen, clk, arb)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Supervisor.Run: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("Supervisor.Run did not stop")
		}
		if err := out.Stop(); err != nil {
			t.Errorf("output.Stop: %v", err)
		}
		recorder.With(func(r *simtest.Recorder) { simtest.AssertNoneHeld(t, nil, r) })
	})
	if err := sup.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	recorder.With(func(r *simtest.Recorder) {
		if len(r.Down()) != 1 || r.Down()[0].Key.String() != "X" {
			t.Fatalf("initial script output = %v, want X down", r.Edges())
		}
	})
	if err := sup.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	before := 0
	recorder.With(func(r *simtest.Recorder) {
		if len(r.Down()) != 0 {
			t.Fatalf("emergency release left held keys: %v", r.Down())
		}
		before = len(r.Records())
	})
	clk.Advance(30 * time.Millisecond)
	if err := sup.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	recorder.With(func(r *simtest.Recorder) {
		if len(r.Records()) != before {
			t.Fatalf("script timer emitted after supervisor emergency release: %v", r.Edges()[before:])
		}
	})
}

type failEmergencyUps struct {
	recorder *simtest.SyncRecorder
	left     atomic.Int32
	attempts atomic.Int32
}

func (s *failEmergencyUps) Send(events []output.KeyEvent) (int, error) {
	for _, event := range events {
		if event.Up && s.left.Load() > 0 {
			s.left.Add(-1)
			s.attempts.Add(1)
			return 0, errors.New("test: emergency release send failed")
		}
	}
	return s.recorder.Send(events)
}

func TestSupervisorEmergencyReleaseFailureStillCancelsScripts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(`import { defineScript, key, every } from "padmate";
export default defineScript({ setup() { every(10, () => key.down("Y")); }, onActivate() { key.down("X"); } });`), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake()
	recorder := simtest.NewSyncRecorder(clk)
	sender := &failEmergencyUps{recorder: recorder}
	out := output.StartLoop(clk, sender, output.LoopOptions{Arbiter: output.ArbiterOptions{RetryInterval: time.Millisecond}})
	events := make(chan input.Event)
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	sup, err := supervisor.New(supervisor.Config{
		Clock: clk, Events: events, Output: out, Program: newScriptTestProgram(t, `{}`), Slots: 1,
		ScriptFactory: func(index uint8, gen uint32, clk clock.Clock, arb slot.Arbiter) (slot.ScriptRuntime, error) {
			return newScriptRuntime(def, index, gen, clk, arb)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Supervisor.Run: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("Supervisor.Run did not stop")
		}
		if err := out.Stop(); err != nil {
			t.Errorf("output.Stop: %v", err)
		}
		recorder.With(func(r *simtest.Recorder) { simtest.AssertNoneHeld(t, nil, r) })
	})
	if err := sup.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	sender.left.Store(4)
	if err := sup.ReleaseAll(); err == nil {
		t.Fatal("failed initial output release was not reported")
	}
	if sender.attempts.Load() < 4 {
		t.Fatalf("failed sends = %d, want at least 4", sender.attempts.Load())
	}
	clk.Advance(30 * time.Millisecond)
	if err := sup.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	var records []simtest.Record
	recorder.With(func(r *simtest.Recorder) { records = r.Records() })
	for _, record := range records {
		if record.Event.Key.String() == "Y" && !record.Event.Up && record.Injected {
			t.Fatalf("old timer emitted after failed release: %v", recorder.Trace())
		}
	}
	recorder.With(func(r *simtest.Recorder) {
		if len(r.Down()) != 0 {
			t.Fatalf("release retry left held keys: %v", r.Down())
		}
	})
}
