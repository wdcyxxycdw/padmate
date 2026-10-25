package script_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// macroSlotRuntime is a slot's runtime that only runs macros, as padmated's
// is for a profile without a legacy entry (design §5.2), on a real
// Lifecycle: a safety release stops it, and a resume or a reload builds
// a new runtime. It builds synchronously, so a test has no cold start.
type macroSlotRuntime struct {
	t        *testing.T
	clk      clock.Clock
	arb      slot.Arbiter
	programs script.Programs
	gen      uint32
	life     *script.Lifecycle
	paused   bool
	cycles   atomic.Uint64
	disabled script.DisabledMacros
}

func (r *macroSlotRuntime) Needed() bool     { return true }
func (r *macroSlotRuntime) Configured() bool { return false }

func (r *macroSlotRuntime) Dispatch(e slot.ScriptEvent) (bool, error) {
	r.t.Errorf("Dispatch(%+v) on a runtime that only runs macros", e)
	return false, nil
}

func (r *macroSlotRuntime) RunMacro(file string, e slot.ScriptEvent) error {
	if r.life == nil {
		return errors.New("RunMacro while stopped")
	}
	return r.life.RunMacro(file, script.Event{Control: e.Control, Gesture: e.Gesture, Time: e.Time})
}

func (r *macroSlotRuntime) RunDue() error {
	if r.life == nil {
		return nil
	}
	return r.life.RunDue()
}

func (r *macroSlotRuntime) NextDeadline() (clock.Time, bool) {
	if r.life == nil {
		return 0, false
	}
	return r.life.NextDeadline()
}

func (r *macroSlotRuntime) ReleaseAll() error { return r.SetPaused(true) }

func (r *macroSlotRuntime) SetPaused(on bool) error {
	if r.paused == on {
		return nil
	}
	r.paused = on
	if on {
		return r.stop()
	}
	return r.start()
}

// start builds and installs a runtime, then stops the old one.
func (r *macroSlotRuntime) start() error {
	life := script.NewLifecycle(r.clk, script.HostOptions{Output: r.arb.(script.OutputSink),
		Owner: output.Owner{Slot: 0, Gen: r.gen}, OwnerCycles: &r.cycles, DisabledMacros: &r.disabled})
	loaded := <-life.LoadPrograms(context.Background(), func(context.Context) (script.Programs, error) { return r.programs, nil })
	if loaded.Err != nil {
		return loaded.Err
	}
	if err := life.Swap(loaded.Host); err != nil {
		return err
	}
	old := r.life
	r.life = life
	if old != nil {
		return old.Dispose()
	}
	return nil
}

func (r *macroSlotRuntime) stop() error {
	old := r.life
	r.life = nil
	if old != nil {
		return old.Dispose()
	}
	return nil
}

func (r *macroSlotRuntime) Reload(gen uint32) error {
	r.gen = gen
	if r.paused {
		return nil
	}
	return r.start()
}

func (r *macroSlotRuntime) Dispose() error { return r.stop() }

const holdMacro = `import { key, sleep } from "padmate";
export default async function () { key.down("Shift"); await sleep(100); key.down("X"); }`

// macroOnlySlot returns a slot whose south tap runs hold.ts, and its
// runtime.
func macroOnlySlot(t *testing.T, sim *simtest.Sim) (*slot.Slot, *macroSlotRuntime) {
	t.Helper()
	rt := &macroSlotRuntime{t: t, clk: sim.Clock, arb: sim.Arb, gen: 1,
		programs: script.ProgramsForTest(t, map[string]string{"macros/hold.ts": holdMacro, "macros/other.ts": holdMacro}, "", "hold.ts", "other.ts")}
	if err := rt.start(); err != nil {
		t.Fatal(err)
	}
	s, err := slot.New(slot.Config{Gen: 1, Program: bindings(t, `"south": {"tap": {"type": "macro", "file": "hold.ts"}}`), Arbiter: sim.Arb, Clock: sim.Clock, Script: rt})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = s.ReleaseAll()
		_ = s.Dispose()
		if errs := s.Errors(); len(errs) > 0 {
			t.Errorf("slot errors:\n%v", errors.Join(errs...))
		}
	})
	return s, rt
}

func bindings(t *testing.T, b ...string) *engine.Program {
	t.Helper()
	p, ds := profile.Parse([]byte(`{"schemaVersion": 0, "name": "t", "layers": {"base": {` + strings.Join(b, ", ") + `}}}`))
	if p == nil {
		t.Fatalf("profile.Parse: %v", ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	return prog
}

// advanceSlot moves the clock to ms, running the slot's deadlines on the
// way.
func advanceSlot(sim *simtest.Sim, s *slot.Slot, ms int) {
	target := clock.Time(time.Duration(ms) * time.Millisecond)
	for {
		next, ok := s.NextDeadline()
		if !ok || next > target {
			break
		}
		if next > sim.Clock.Now() {
			sim.Clock.Set(next)
		}
		s.Advance()
	}
	sim.Clock.Set(target)
	s.Advance()
}

func tapSouth(sim *simtest.Sim, s *slot.Slot, ms int) {
	advanceSlot(sim, s, ms)
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	advanceSlot(sim, s, ms+10)
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
}

// A run of a macro ends at each of the slot's boundaries: its keys go up
// and the rest of it never runs, and the macro runs afresh afterwards.
func TestMacroRunEndsAtSlotBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		boundary func(*slot.Slot, *macroSlotRuntime) error
		again    bool // the next tap runs the macro
	}{
		{"emergency release", func(s *slot.Slot, _ *macroSlotRuntime) error { return s.ReleaseAll() }, true},
		{"pause and resume", func(s *slot.Slot, _ *macroSlotRuntime) error {
			return errors.Join(s.SetPaused(true), s.SetPaused(false))
		}, true},
		{"disconnect", func(s *slot.Slot, _ *macroSlotRuntime) error {
			s.Handle(input.Event{Kind: input.DeviceRemoved})
			return nil
		}, false},
		{"reload", func(s *slot.Slot, _ *macroSlotRuntime) error {
			return s.Reload(bindings(t, `"south": {"tap": {"type": "macro", "file": "hold.ts"}}`), 2)
		}, true},
		{"switch profile", func(s *slot.Slot, rt *macroSlotRuntime) error {
			rt.programs = script.ProgramsForTest(t, map[string]string{"macros/hold.ts": holdMacro}, "", "hold.ts")
			return s.Reload(bindings(t, `"south": {"tap": {"type": "macro", "file": "hold.ts"}}`, `"east": {"tap": {"type": "keys", "keys": "F13"}}`), 2)
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sim := simtest.New(t)
			s, rt := macroOnlySlot(t, sim)
			tapSouth(sim, s, 0)
			advanceSlot(sim, s, 30)
			sim.ExpectOutput("+LShift@10")
			if err := tc.boundary(s, rt); err != nil {
				t.Fatal(err)
			}
			advanceSlot(sim, s, 500)
			sim.ExpectOutput("-LShift@30")
			if !tc.again {
				return
			}
			tapSouth(sim, s, 1000)
			advanceSlot(sim, s, 1200)
			sim.ExpectOutput("+LShift@1010 +X@1110 -X@1110 -LShift@1110")
		})
	}
}
