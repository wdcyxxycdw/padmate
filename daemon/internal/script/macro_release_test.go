package script_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// The tests of this file are the release points of design §6.4: each
// path that ends a run of a macro releases the keys it holds, and the
// Sim's cleanup checks that nothing is left held.

// macroHost activates the macros of files on the Sim's Arbiter and lets
// the Sim drive it.
func macroHost(t *testing.T, sim *simtest.Sim, files map[string]string, entry string, macros ...string) *script.Host {
	t.Helper()
	h := script.NewHostWithOptions(script.HostOptions{Clock: sim.Clock, Output: sim.Arb, Owner: output.Owner{Slot: 0, Gen: 1}})
	if err := h.ActivatePrograms(script.ProgramsForTest(t, files, entry, macros...)); err != nil {
		t.Fatal(err)
	}
	if err := h.OnActivate(); err != nil {
		t.Fatal(err)
	}
	sim.Drive(h)
	return h
}

func run(t *testing.T, h *script.Host, file string) {
	t.Helper()
	if err := h.RunMacro(file, script.Event{}); err != nil {
		t.Fatalf("RunMacro(%s): %v", file, err)
	}
}

// macroErrorOf fails the test unless errs are one error of the macro file
// that contains text.
func macroErrorOf(t *testing.T, errs []error, file, text string) {
	t.Helper()
	err := errors.Join(errs...)
	mes := script.MacroErrorsOf(err)
	if len(errs) != 1 || len(mes) != 1 || mes[0].File != file || !strings.Contains(err.Error(), text) {
		t.Fatalf("errors %v, want one of %s with %q", err, file, text)
	}
}

// A run that ends releases what it holds: a key.down without key.up,
// after a return, at the end of an async function, after a throw or a
// rejection.
func TestMacroEndReleasesHeldKeys(t *testing.T) {
	for _, tc := range []struct {
		name, source, out, err string
		syncErr                bool
	}{
		{"return", `export default function () { key.down("Shift"); }`, "+LShift@0 -LShift@0", "", false},
		{"async", `export default async function () { key.down("Shift"); await sleep(100); }`, "+LShift@0 -LShift@100", "", false},
		{"throw", `export default function () { key.down("Shift"); throw new Error("boom"); }`, "+LShift@0 -LShift@0", "Error: boom", true},
		{"reject", `export default async function () { key.down("Shift"); await sleep(10); throw new Error("boom"); }`, "+LShift@0 -LShift@10", "Error: boom", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sim := simtest.New(t)
			h := macroHost(t, sim, map[string]string{"macros/m.ts": `import { key, sleep } from "padmate";` + "\n" + tc.source}, "", "m.ts")
			err := h.RunMacro("m.ts", script.Event{})
			if tc.syncErr {
				macroErrorOf(t, []error{err}, "m.ts", tc.err)
			} else if err != nil {
				t.Fatal(err)
			}
			sim.Settle()
			sim.ExpectOutput(tc.out)
			if errs := sim.Errors(); tc.err != "" && !tc.syncErr {
				macroErrorOf(t, errs, "m.ts", tc.err)
			} else if len(errs) != 0 {
				t.Fatalf("errors %v", errs)
			}
		})
	}
}

// pressDurations returns how long each press of the recorded output
// lasted.
func pressDurations(t *testing.T, sim *simtest.Sim) map[string]time.Duration {
	t.Helper()
	down := map[string]time.Duration{}
	held := map[string]time.Duration{}
	for _, e := range sim.Out.Edges() {
		switch e.Kind {
		case simtest.Down:
			down[e.Name] = time.Duration(e.T)
		case simtest.Up:
			held[e.Name] = time.Duration(e.T) - down[e.Name]
		}
	}
	return held
}

// A tap the run did not await is not cut short by the end of the run: the
// Arbiter has it and presses for the minimum press time.
func TestMacroUnawaitedTapFinishes(t *testing.T) {
	for _, source := range []string{
		`export default function () { key.tap("Ctrl+S"); }`,
		`export default async function () { key.tap("Ctrl+S"); }`,
	} {
		t.Run(source, func(t *testing.T) {
			sim := simtest.New(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: 20 * time.Millisecond, EventGap: 5 * time.Millisecond}))
			h := macroHost(t, sim, map[string]string{"macros/m.ts": `import { key } from "padmate";` + "\n" + source}, "", "m.ts")
			run(t, h, "m.ts")
			sim.Settle()
			got := pressDurations(t, sim)
			if len(got) != 2 || got["S"] < 20*time.Millisecond || got["LCtrl"] < 20*time.Millisecond {
				t.Fatalf("presses %v in %s, want Ctrl and S down 20 ms each", got, sim.Output())
			}
		})
	}
}

// A normal end releases with Up: another owner's press of the same key
// keeps its minimum press time, where releasing the owner would cut it.
func TestMacroEndKeepsOtherOwnersPress(t *testing.T) {
	sim := simtest.New(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: 20 * time.Millisecond}))
	h := macroHost(t, sim, map[string]string{
		"macros/m.ts": `import { key, sleep } from "padmate"; export default async function () { key.down("Shift"); await sleep(10); }`,
	}, "", "m.ts")
	other := output.Owner{Slot: 0, Gen: 1, Source: output.SourceDecl, Cycle: 999}
	shift := keys.MustParse("Shift")
	if err := sim.Arb.Down(other, shift); err != nil {
		t.Fatal(err)
	}
	run(t, h, "m.ts")
	sim.Advance(5 * time.Millisecond)
	if err := sim.Arb.Up(other, shift); err != nil {
		t.Fatal(err)
	}
	sim.Settle()
	sim.ExpectOutput("+LShift@0 -LShift@20")
}

// A run that lasts MacroRunLimit is canceled: its keys go up and it
// reports the limit; the macro runs again afterwards.
func TestMacroRunLimit(t *testing.T) {
	sim := simtest.New(t)
	h := macroHost(t, sim, map[string]string{
		"macros/m.ts": `import { key } from "padmate"; export default async function () { key.down("Shift"); await new Promise(() => {}); }`,
	}, "", "m.ts")
	run(t, h, "m.ts")
	sim.Advance(script.MacroRunLimit - time.Millisecond)
	sim.ExpectOutput("+LShift@0")
	sim.Advance(time.Millisecond)
	sim.ExpectOutput("-LShift@60000")
	macroErrorOf(t, sim.Errors(), "m.ts", "longer than 60 s")
	run(t, h, "m.ts")
	sim.ExpectOutput("+LShift@60000")
}

// A safety boundary of the runtime ends every run: its keys go up, and a
// run in a sleep never continues.
func TestMacroSafetyBoundariesEndRuns(t *testing.T) {
	for _, boundary := range []string{"ReleaseAll", "Suspend", "ReleaseKeys", "Dispose"} {
		t.Run(boundary, func(t *testing.T) {
			sim := simtest.New(t)
			h := macroHost(t, sim, map[string]string{
				"macros/m.ts": `import { key, sleep } from "padmate"; export default async function () { key.down("Shift"); await sleep(100); key.down("X"); }`,
			}, "", "m.ts")
			run(t, h, "m.ts")
			sim.Advance(30 * time.Millisecond)
			switch boundary {
			case "ReleaseAll":
				h.ReleaseAll()
			case "Suspend":
				h.Suspend()
			case "ReleaseKeys":
				h.ReleaseKeys()
			case "Dispose":
				if err := h.Dispose(); err != nil {
					t.Fatal(err)
				}
			}
			sim.Advance(time.Second)
			sim.ExpectOutput("+LShift@0 -LShift@30")
		})
	}
}

// Two runs that hold one key each hold a reference: the key goes up when
// the last of them ends.
func TestMacrosShareAKey(t *testing.T) {
	sim := simtest.New(t)
	h := macroHost(t, sim, map[string]string{
		"macros/a.ts": `import { key, sleep } from "padmate"; export default async function () { key.down("Shift"); await sleep(10); }`,
		"macros/b.ts": `import { key, sleep } from "padmate"; export default async function () { key.down("Shift"); await sleep(30); }`,
	}, "", "a.ts", "b.ts")
	run(t, h, "a.ts")
	run(t, h, "b.ts")
	sim.Settle()
	sim.ExpectOutput("+LShift@0 -LShift@30")
}

// Two runs tapping one key at once each have their pending tap: the budget
// rejects neither.
func TestMacrosTapOneKey(t *testing.T) {
	sim := simtest.New(t)
	source := `import { key, session } from "padmate"; export default async function () { const r = await key.tap("X", { holdMs: 30 }); session.set(%q, r.dropped); }`
	h := macroHost(t, sim, map[string]string{
		"macros/a.ts": strings.Replace(source, "%q", `"a"`, 1),
		"macros/b.ts": strings.Replace(source, "%q", `"b"`, 1),
	}, "", "a.ts", "b.ts")
	run(t, h, "a.ts")
	run(t, h, "b.ts")
	sim.Settle()
	if errs := sim.Errors(); len(errs) != 0 {
		t.Fatalf("errors %v", errs)
	}
	v, err := h.VM.RunString(`[__padmate.session.get("a"), __padmate.session.get("b")].join()`)
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != "false,false" {
		t.Fatalf("dropped = %s, want neither", v)
	}
}

// The end of one run keeps the tap completions of another that T-output has
// already returned: the other run goes on.
func TestMacroEndKeepsOtherRunsCompletions(t *testing.T) {
	sim := simtest.New(t)
	h := macroHost(t, sim, map[string]string{
		"macros/a.ts": `import { key } from "padmate"; export default function () { key.tap("X"); }`,
		"macros/b.ts": `import { key } from "padmate"; export default async function () { await key.tap("Y"); key.down("Z"); }`,
	}, "", "a.ts", "b.ts")
	run(t, h, "b.ts") // Y's completion is queued at once
	run(t, h, "a.ts") // ends at once, with X's completion queued
	sim.Settle()
	sim.ExpectOutput("+Y@0 -Y@0 +X@0 -X@0 +Z@0 -Z@0")
}

// key.releaseAll in a macro releases its own run only; the other run keeps
// its key and its queued completion.
func TestMacroReleaseAllIsTheRunsOwn(t *testing.T) {
	sim := simtest.New(t)
	h := macroHost(t, sim, map[string]string{
		"macros/a.ts": `import { key, sleep } from "padmate"; export default async function () { key.down("Shift"); await key.tap("Y"); key.down("Z"); await sleep(100); }`,
		"macros/b.ts": `import { key, sleep } from "padmate"; export default async function () { key.down("Ctrl"); key.releaseAll(); await sleep(50); key.down("Alt"); }`,
	}, "", "a.ts", "b.ts")
	run(t, h, "a.ts")
	run(t, h, "b.ts")
	sim.Settle()
	sim.ExpectOutput("+LShift@0 +Y@0 -Y@0 +LCtrl@0 -LCtrl@0 +Z@0 +LAlt@50 -LAlt@50 -Z@100 -LShift@100")
}

// The legacy script's key.releaseAll is its own: a macro's tap in progress
// finishes normally.
func TestLegacyReleaseAllLeavesMacroTaps(t *testing.T) {
	sim := simtest.New(t)
	h := macroHost(t, sim, map[string]string{
		"main.ts": `import { defineScript, on, key } from "padmate";
export default defineScript({ setup() { on("south", "press", () => key.releaseAll()); } });`,
		"macros/m.ts": `import { key } from "padmate"; export default async function () { const r = await key.tap("X", { holdMs: 50 }); if (!r.dropped) key.down("Y"); }`,
	}, "main.ts", "m.ts")
	run(t, h, "m.ts")
	sim.Advance(10 * time.Millisecond)
	if err := h.Dispatch(script.Event{Control: "south", Gesture: "press"}); err != nil {
		t.Fatal(err)
	}
	sim.Settle()
	sim.ExpectOutput("+X@0 -X@50 +Y@50 -Y@50")
}

// A trigger while the macro runs is ignored: no second press.
func TestMacroRetriggerAddsNoPress(t *testing.T) {
	sim := simtest.New(t)
	h := macroHost(t, sim, map[string]string{
		"macros/m.ts": `import { key, sleep } from "padmate"; export default async function () { key.tap("A"); await sleep(50); }`,
	}, "", "m.ts")
	run(t, h, "m.ts")
	sim.Advance(10 * time.Millisecond)
	run(t, h, "m.ts")
	sim.Settle()
	sim.ExpectOutput("+A@0 -A@0")
}

// macroLifecycle loads programs in a Lifecycle on the Sim's Arbiter, as
// padmated's runtime does, and lets the Sim drive it.
func macroLifecycle(t *testing.T, sim *simtest.Sim, build script.ProgramsFunc) *script.Lifecycle {
	t.Helper()
	l := script.NewLifecycle(sim.Clock, script.HostOptions{Output: sim.Arb, Owner: output.Owner{Slot: 0, Gen: 1}})
	loaded := <-l.LoadPrograms(context.Background(), build)
	if loaded.Err != nil {
		t.Fatal(loaded.Err)
	}
	if err := l.Swap(loaded.Host); err != nil {
		t.Fatal(err)
	}
	sim.Drive(l)
	return l
}

// A reload (a saved macro, a new profile) swaps in a new runtime and the
// old one's runs end; Dispose (a switch to a profile without a runtime)
// too. The new runtime runs the macro afresh.
func TestMacroRunEndsAtReloadAndDispose(t *testing.T) {
	files := map[string]string{
		"macros/m.ts": `import { key, sleep } from "padmate"; export default async function () { key.down("Shift"); await sleep(100); key.down("X"); }`,
	}
	for _, how := range []string{"reload", "dispose"} {
		t.Run(how, func(t *testing.T) {
			sim := simtest.New(t)
			ps := script.ProgramsForTest(t, files, "", "m.ts")
			build := func(context.Context) (script.Programs, error) { return ps, nil }
			l := macroLifecycle(t, sim, build)
			if err := l.RunMacro("m.ts", script.Event{}); err != nil {
				t.Fatal(err)
			}
			sim.Advance(30 * time.Millisecond)
			switch how {
			case "reload":
				next := <-l.LoadPrograms(context.Background(), build)
				if next.Err != nil {
					t.Fatal(next.Err)
				}
				if err := l.Swap(next.Host); err != nil {
					t.Fatal(err)
				}
			case "dispose":
				if err := l.Dispose(); err != nil {
					t.Fatal(err)
				}
			}
			sim.Advance(200 * time.Millisecond)
			sim.ExpectOutput("+LShift@0 -LShift@30")
			if how == "reload" {
				if err := l.RunMacro("m.ts", script.Event{}); err != nil {
					t.Fatal(err)
				}
				sim.ExpectOutput("+LShift@230")
			}
		})
	}
}

// A macro that loops or overflows its stack is interrupted: the runtime
// releases every run, its own and another macro's waiting in a sleep, and
// restarts without it; the macro loads again once its file changes.
func TestMacroInterruptionReleasesAndDisables(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		wait         time.Duration // before the interruption, in a sleep
		out          string
	}{
		{"loop", `export default function () { key.down("Ctrl"); while (true) {} }`, 0,
			"+LShift@0 +LCtrl@0 -LCtrl@0 -LShift@0"},
		{"loop after a sleep", `export default async function () { key.down("Ctrl"); await sleep(10); while (true) {} }`, 10 * time.Millisecond,
			"+LShift@0 +LCtrl@0 -LShift@10 -LCtrl@10"},
		{"stack overflow", `function f(n: number): number { return f(n + 1) + 1; }
export default function () { key.down("Ctrl"); f(0); }`, 0,
			"+LShift@0 +LCtrl@0 -LCtrl@0 -LShift@0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sim := simtest.New(t)
			built := script.ProgramsForTest(t, map[string]string{
				"macros/hold.ts": `import { key, sleep } from "padmate"; export default async function () { key.down("Shift"); await sleep(1000); key.down("X"); }`,
				"macros/spin.ts": `import { key, sleep } from "padmate";` + "\n" + tc.source,
			}, "", "hold.ts", "spin.ts")
			var version atomic.Value
			version.Store("1")
			// The restart builds on a worker goroutine.
			build := func(context.Context) (script.Programs, error) {
				ps := script.Programs{Macros: slices.Clone(built.Macros)}
				for i := range ps.Macros {
					ps.Macros[i].Version = version.Load().(string)
				}
				return ps, nil
			}
			l := macroLifecycle(t, sim, build)
			if err := l.RunMacro("hold.ts", script.Event{}); err != nil {
				t.Fatal(err)
			}
			vm := l.Current().VM
			escape := time.AfterFunc(5*time.Second, func() { vm.Interrupt(errors.New("test escape")) })
			err := l.RunMacro("spin.ts", script.Event{})
			if tc.wait > 0 {
				if err != nil {
					t.Fatal(err)
				}
				sim.Advance(tc.wait)
				err = errors.Join(sim.Errors()...)
			}
			escape.Stop()
			if mes := script.MacroErrorsOf(err); len(mes) == 0 || mes[0].File != "spin.ts" {
				t.Fatalf("interruption %v, want an error of spin.ts", err)
			}
			if l.Current() != nil {
				t.Fatal("the interrupted runtime is still current")
			}
			if got := l.DisabledMacros().Files(); len(got) != 1 || got[0] != "spin.ts" {
				t.Fatalf("disabled %v, want spin.ts", got)
			}
			sim.ExpectOutput(tc.out)

			// The restarted runtime runs hold.ts and reports spin.ts disabled.
			restarted := waitRestart(t, sim, l)
			if err := l.RunMacro("spin.ts", script.Event{}); !errors.Is(err, script.ErrMacroDisabled) {
				t.Fatalf("RunMacro(spin.ts) = %v, want disabled", err)
			}
			if err := l.RunMacro("hold.ts", script.Event{}); err != nil {
				t.Fatal(err)
			}
			sim.ExpectOutput("+LShift@" + restarted)
			if err := l.Dispose(); err != nil {
				t.Fatal(err)
			}
			sim.ClearOutput()

			// A new version of the file loads again.
			version.Store("2")
			l2 := macroLifecycle(t, sim, build)
			if got := l2.Current().MacroErrors(); len(got) != 0 {
				t.Fatalf("macro errors after the file changed: %v", got)
			}
		})
	}
}

// A wall-clock wedge of a macro, in its run or a timer callback, releases
// every run, its own and another macro's waiting in a sleep, and stops the
// lifecycle for good: no restart, and the macro is not disabled, as the
// wedge is not its CPU use. A reload brings it back.
func TestMacroWallWedgeReleasesAndStops(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		wait         time.Duration // before the wedge, in a sleep
		out          string
	}{
		{"in the run", `export default function () { key.down("Ctrl"); log.info("spin"); while (true) {} }`, 0,
			"+LShift@0 +LCtrl@0 -LCtrl@0 -LShift@0"},
		{"in a timer", `export default async function () { key.down("Ctrl"); await sleep(10); log.info("spin"); while (true) {} }`, 10 * time.Millisecond,
			"+LShift@0 +LCtrl@0 -LShift@10 -LCtrl@10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sim := simtest.New(t)
			ps := script.ProgramsForTest(t, map[string]string{
				"macros/hold.ts":  `import { key, sleep } from "padmate"; export default async function () { key.down("Shift"); await sleep(1000); key.down("X"); }`,
				"macros/wedge.ts": `import { key, log, sleep } from "padmate";` + "\n" + tc.source,
			}, "", "hold.ts", "wedge.ts")
			var builds atomic.Int32
			build := func(context.Context) (script.Programs, error) { builds.Add(1); return ps, nil }
			spinning := make(chan struct{}, 1)
			l := script.NewLifecycle(sim.Clock, script.HostOptions{Output: sim.Arb, Owner: output.Owner{Slot: 0, Gen: 1}, Logs: script.NewLogBuffer(spinning)})
			loaded := <-l.LoadPrograms(context.Background(), build)
			if loaded.Err != nil {
				t.Fatal(loaded.Err)
			}
			if err := l.Swap(loaded.Host); err != nil {
				t.Fatal(err)
			}
			sim.Drive(l)
			if err := l.RunMacro("hold.ts", script.Event{}); err != nil {
				t.Fatal(err)
			}
			// The wall clock's watchdog fires after seconds; the test wedges
			// the loop as soon as it spins, long before the CPU budget can.
			vm := l.Current().VM
			done := make(chan struct{})
			defer close(done)
			go func() {
				select {
				case <-spinning:
					vm.Interrupt(&script.BudgetError{Clock: "wall", Budget: 2 * time.Second})
				case <-done:
				}
			}()
			err := l.RunMacro("wedge.ts", script.Event{})
			if tc.wait > 0 {
				if err != nil {
					t.Fatal(err)
				}
				sim.Advance(tc.wait)
				err = errors.Join(sim.Errors()...)
			}
			if !script.IsWallBudget(err) {
				t.Fatalf("error %v, want the wall budget's", err)
			}
			if mes := script.MacroErrorsOf(err); len(mes) == 0 || mes[0].File != "wedge.ts" {
				t.Fatalf("wedge %v, want an error of wedge.ts", err)
			}
			if l.Current() != nil {
				t.Fatal("the wedged runtime is still current")
			}
			if got := l.DisabledMacros().Files(); len(got) != 0 {
				t.Fatalf("disabled %v after a wall wedge", got)
			}
			sim.ExpectOutput(tc.out)
			if _, permanent := l.RestartStatus(); !permanent {
				t.Fatal("the lifecycle may restart after a wall wedge")
			}
			sim.Advance(time.Minute)
			if l.Current() != nil || builds.Load() != 1 {
				t.Fatalf("restarted after a wall wedge: %d builds", builds.Load())
			}
			if err := l.RunMacro("wedge.ts", script.Event{}); err != nil {
				t.Fatalf("RunMacro on the stopped lifecycle: %v", err)
			}
			sim.ExpectOutput("")

			// A reload loads the wedged macro again.
			next := <-l.LoadPrograms(context.Background(), build)
			if next.Err != nil {
				t.Fatal(next.Err)
			}
			if err := l.Swap(next.Host); err != nil {
				t.Fatal(err)
			}
			if got := l.Current().MacroErrors(); len(got) != 0 {
				t.Fatalf("macro errors after the reload: %v", got)
			}
			if err := l.RunMacro("hold.ts", script.Event{}); err != nil {
				t.Fatal(err)
			}
			sim.ExpectOutput("+LShift@" + strconv.FormatInt(int64(time.Duration(sim.Clock.Now())/time.Millisecond), 10))
			if err := l.Dispose(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// waitRestart advances past the restart backoff until the lifecycle has a
// runtime again, and returns the time in ms.
func waitRestart(t *testing.T, sim *simtest.Sim, l *script.Lifecycle) string {
	t.Helper()
	sim.Advance(time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for l.Current() == nil && time.Now().Before(deadline) {
		sim.Advance(time.Millisecond)
	}
	if l.Current() == nil {
		t.Fatalf("no restart: %v", sim.Errors())
	}
	sim.Errors() // the restart's poll reports nothing more
	return strconv.FormatInt(int64(time.Duration(sim.Clock.Now())/time.Millisecond), 10)
}

// An error of the legacy script's completions stops that turn, as before
// macros; a macro's completions in the same turn wait for the next one.
func TestMacroCompletionsSurviveLegacyError(t *testing.T) {
	sim := simtest.New(t)
	h := macroHost(t, sim, map[string]string{
		"main.ts": `import { defineScript, on, key } from "padmate";
export default defineScript({ setup() { on("south", "press", () => { key.tap("A").then(() => { throw new Error("legacy"); }); }); } });`,
		"macros/m.ts": `import { key } from "padmate"; export default async function () { await key.tap("B"); key.down("C"); }`,
	}, "main.ts", "m.ts")
	if err := h.Dispatch(script.Event{Control: "south", Gesture: "press"}); err != nil {
		t.Fatal(err)
	}
	run(t, h, "m.ts")
	sim.Settle()
	sim.ExpectOutput("+A@0 -A@0 +B@0 -B@0 +C@0 -C@0")
	if errs := sim.Errors(); len(errs) != 1 || !strings.Contains(errs[0].Error(), "legacy") || len(script.MacroErrorsOf(errs[0])) != 0 {
		t.Fatalf("errors %v, want the legacy script's", errs)
	}
}
