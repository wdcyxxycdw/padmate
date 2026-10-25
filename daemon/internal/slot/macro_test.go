package slot_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// fakeMacroHold is how long a run of fakeMacros holds its key.
const fakeMacroHold = 100 * time.Millisecond

// fakeMacros is a runtime that only runs macros (SCR-50), as padmated's
// is for a profile without a legacy entry: Needed, not Configured. A run
// of the macro "F13.ts" presses F13 for fakeMacroHold under an owner of
// its own, so the output shows when each macro ran. Like padmated's
// runtime, ReleaseAll stops it (SetPaused(true)) and releases its owners,
// SetPaused(false) starts it again, and a macro triggered while it is not
// ready yet runs once when it is.
type fakeMacros struct {
	t       *testing.T
	clk     clock.Clock
	arb     slot.Arbiter
	index   uint8
	gen     uint32
	needed  bool
	legacy  bool // Configured: the profile also has a legacy script
	wait    bool // WaitForInitialResync, as padmated's factory runtimes
	paused  bool
	ready   bool // false while a cold start or rebuild is pending
	restart bool // SetPaused(false) leaves the runtime pending, not ready
	cycle   uint64
	queued  []string
	ups     []fakeUp
	runs    []string // "file@ms" for each run
	calls   []string // ReleaseAll, SetPaused, Reload and Dispose, in order
}

type fakeUp struct {
	at    clock.Time
	owner output.Owner
	chord keys.Chord
}

func newFakeMacros(t *testing.T, clk clock.Clock, arb slot.Arbiter) *fakeMacros {
	return &fakeMacros{t: t, clk: clk, arb: arb, gen: 1, needed: true, ready: true}
}

func (f *fakeMacros) Needed() bool               { return f.needed }
func (f *fakeMacros) Configured() bool           { return f.legacy }
func (f *fakeMacros) WaitForInitialResync() bool { return f.wait }

func (f *fakeMacros) Dispatch(e slot.ScriptEvent) (bool, error) {
	if !f.legacy {
		f.t.Errorf("Dispatch(%+v) on a runtime that only runs macros", e)
	}
	return false, nil
}

func (f *fakeMacros) RunMacro(file string, _ slot.ScriptEvent) error {
	if f.paused {
		return fmt.Errorf("fake: RunMacro(%q) while stopped", file)
	}
	if !f.ready {
		if !slices.Contains(f.queued, file) {
			f.queued = append(f.queued, file)
		}
		return nil
	}
	f.run(file)
	return nil
}

func (f *fakeMacros) run(file string) {
	chord, err := keys.Parse(strings.TrimSuffix(strings.TrimSuffix(file, ".ts"), ".js"))
	if err != nil {
		f.t.Errorf("fake macro %q: %v", file, err)
		return
	}
	f.cycle++
	o := output.Owner{Slot: f.index, Gen: f.gen, Source: output.SourceScript, Cycle: f.cycle}
	f.runs = append(f.runs, fmt.Sprintf("%s@%d", file, time.Duration(f.clk.Now())/time.Millisecond))
	if err := f.arb.Down(o, chord); err != nil {
		f.t.Error(err)
	}
	f.ups = append(f.ups, fakeUp{f.clk.Now().Add(fakeMacroHold), o, chord})
}

func (f *fakeMacros) RunDue() error {
	if f.ready && !f.paused {
		queued := f.queued
		f.queued = nil
		for _, file := range queued {
			f.run(file)
		}
	}
	now := f.clk.Now()
	var err error
	f.ups = slices.DeleteFunc(f.ups, func(u fakeUp) bool {
		if u.at > now {
			return false
		}
		err = errors.Join(err, f.arb.Up(u.owner, u.chord))
		return true
	})
	return err
}

func (f *fakeMacros) NextDeadline() (clock.Time, bool) {
	var d clock.Time
	ok := false
	if f.ready && !f.paused && len(f.queued) > 0 {
		d, ok = f.clk.Now(), true
	}
	for _, u := range f.ups {
		if !ok || u.at < d {
			d, ok = u.at, true
		}
	}
	return d, ok
}

func (f *fakeMacros) ReleaseAll() error {
	f.calls = append(f.calls, "ReleaseAll")
	return f.stop()
}

func (f *fakeMacros) SetPaused(on bool) error {
	f.calls = append(f.calls, fmt.Sprintf("SetPaused(%v)", on))
	if on {
		return f.stop()
	}
	if f.paused {
		f.paused = false
		f.ready = !f.restart
	}
	return nil
}

// stop ends every run, as padmated's runtime releases its owners.
func (f *fakeMacros) stop() error {
	f.paused, f.queued, f.ups = true, nil, nil
	return f.arb.ReleaseOwners(func(o output.Owner) bool { return o.Slot == f.index && o.Source == output.SourceScript })
}

func (f *fakeMacros) Reload(gen uint32) error {
	f.calls = append(f.calls, fmt.Sprintf("Reload(%d)", gen))
	f.gen = gen
	return nil
}

func (f *fakeMacros) Dispose() error {
	f.calls = append(f.calls, "Dispose")
	return f.stop()
}

// macroSlot returns a slot over bindings whose runtime is a fakeMacros.
func macroSlot(t *testing.T, sim *simtest.Sim, configure func(*fakeMacros), bindings ...string) (*slot.Slot, *fakeMacros) {
	t.Helper()
	fake := newFakeMacros(t, sim.Clock, sim.Arb)
	if configure != nil {
		configure(fake)
	}
	s, err := slot.New(slot.Config{Gen: 1, Program: compile(t, bindings...), Arbiter: sim.Arb, Clock: sim.Clock, Script: fake})
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
	return s, fake
}

// settle moves the clock forward by d, running the slot's deadlines on
// the way.
func settle(t *testing.T, sim *simtest.Sim, s *slot.Slot, d time.Duration) {
	t.Helper()
	target := sim.Clock.Now().Add(d)
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
}

func handleAt(sim *simtest.Sim, s *slot.Slot, msec int, ev input.Event) {
	sim.Clock.Set(clock.Time(time.Duration(msec) * time.Millisecond))
	s.Advance()
	s.Handle(ev)
}

const (
	southMacro = `"south": {"tap": {"type": "macro", "file": "F13.ts"}}`
	eastMacro  = `"east": {"tap": {"type": "macro", "file": "F15.ts"}, "hold": {"type": "macro", "file": "F14.ts"}}`
)

// A macro runs when its gesture fires, through RunMacro and nothing else:
// the runtime sees no Dispatch, and the declarative bindings work as
// without a script.
func TestMacroOnlyRunsThroughEngine(t *testing.T) {
	sim := simtest.New(t)
	s, fake := macroSlot(t, sim, nil, southMacro, eastHold)
	if err := s.Replay(sim.Clock, mustInput(t, "+south@0 -south@50 +east@100 -east@300")); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fake.runs, []string{"F13.ts@50"}) {
		t.Errorf("runs %v", fake.runs)
	}
	sim.ExpectOutput("+F13@50 +Space@100 -F13@150 -Space@300")
}

// ReleaseAll stops the runtime and starts it again at once: the next
// press of a macro's control runs it, with no other press first.
func TestMacroOnlyReleaseAllRestarts(t *testing.T) {
	sim := simtest.New(t)
	s, fake := macroSlot(t, sim, nil, southMacro)
	if err := s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"ReleaseAll", "SetPaused(false)"}; !slices.Equal(fake.calls, want) {
		t.Errorf("calls %v, want %v", fake.calls, want)
	}
	handleAt(sim, s, 0, button("south", true))
	handleAt(sim, s, 50, button("south", false))
	if !slices.Equal(fake.runs, []string{"F13.ts@50"}) {
		t.Errorf("runs %v", fake.runs)
	}
	// A run in progress ends at the release, and the runtime is back.
	sim.Clock.Set(clock.Time(80 * time.Millisecond))
	if err := s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	sim.ExpectOutput("+F13@50 -F13@80")
	if err := s.Replay(sim.Clock, mustInput(t, "+south@100 -south@150")); err != nil {
		t.Fatal(err)
	}
	sim.ExpectOutput("+F13@150 -F13@250")
	// A removed device and the end of RunLoop release without a restart.
	fake.calls = nil
	s.Handle(input.Event{Kind: input.DeviceRemoved})
	if want := []string{"SetPaused(true)", "ReleaseAll"}; !slices.Equal(fake.calls, want) {
		t.Errorf("calls after DeviceRemoved %v, want %v", fake.calls, want)
	}
}

// ReleaseAllForExit, a replay's exit path, releases as ReleaseAll does but
// does not start the runtime again: Dispose follows.
func TestMacroOnlyReleaseAllForExit(t *testing.T) {
	sim := simtest.New(t)
	s, fake := macroSlot(t, sim, nil, southMacro)
	handleAt(sim, s, 0, button("south", true))
	handleAt(sim, s, 50, button("south", false))
	sim.Clock.Set(clock.Time(80 * time.Millisecond))
	if err := s.ReleaseAllForExit(); err != nil {
		t.Fatal(err)
	}
	sim.ExpectOutput("+F13@50 -F13@80")
	if want := []string{"ReleaseAll"}; !slices.Equal(fake.calls, want) {
		t.Errorf("calls %v, want %v", fake.calls, want)
	}
	if !fake.paused {
		t.Error("the runtime started again")
	}
}

// A paused slot does not restart the runtime at ReleaseAll; resuming does.
func TestMacroOnlyReleaseAllWhilePaused(t *testing.T) {
	sim := simtest.New(t)
	s, fake := macroSlot(t, sim, nil, southMacro)
	if err := s.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	if !fake.paused {
		t.Fatal("ReleaseAll restarted a paused runtime")
	}
	if err := s.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	if err := s.Replay(sim.Clock, mustInput(t, "+south@0 -south@50")); err != nil {
		t.Fatal(err)
	}
	sim.ExpectOutput("+F13@50 -F13@150")
}

// A control pressed during the pause runs no macro, not even at its
// release after the resume; its next press does.
func TestMacroOnlyPressedWhilePaused(t *testing.T) {
	sim := simtest.New(t)
	s, fake := macroSlot(t, sim, nil, southMacro, `"west": {"release": {"type": "macro", "file": "F15.ts"}}`)
	if err := s.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	handleAt(sim, s, 0, button("south", true))
	handleAt(sim, s, 0, button("west", true))
	if err := s.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	handleAt(sim, s, 50, button("south", false))
	handleAt(sim, s, 60, button("west", false))
	if len(fake.runs) != 0 {
		t.Fatalf("canceled presses ran %v", fake.runs)
	}
	if err := s.Replay(sim.Clock, mustInput(t, "+south@100 -south@150 +west@200 -west@250")); err != nil {
		t.Fatal(err)
	}
	sim.ExpectOutput("+F13@150 -F13@250 +F15@250 -F15@350")
}

// A control held when the pad is assigned, whose Resync brings in its
// press, runs no hold macro when its hold time is up, after the Resync;
// its next press does.
func TestMacroOnlyHeldAtResync(t *testing.T) {
	sim := simtest.New(t)
	s, fake := macroSlot(t, sim, func(f *fakeMacros) { f.wait = true }, eastMacro)
	s.Handle(input.Event{Kind: input.DeviceAdded, Device: 3, Info: &input.DeviceInfo{ID: 3, IsGamepad: true}})
	var st slot.State
	st.Buttons[control.East] = true
	s.Resync(st)
	settle(t, sim, s, time.Second)
	if len(fake.runs) != 0 {
		t.Fatalf("held control ran %v", fake.runs)
	}
	sim.Clock.Set(clock.Time(2 * time.Second))
	s.Handle(button("east", false))
	if err := s.Replay(sim.Clock, mustInput(t, "+east@3000 -east@4000")); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fake.runs, []string{"F14.ts@3250"}) {
		t.Errorf("runs %v", fake.runs)
	}
}

// A control held when the pad is assigned, whose Resync brings in its
// press, runs no tap or release macro when it is released after the
// Resync, with or without a legacy script; its next press does.
func TestMacroHeldAtResyncReleased(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%v", legacy), func(t *testing.T) {
			sim := simtest.New(t)
			s, fake := macroSlot(t, sim, func(f *fakeMacros) { f.wait, f.legacy = true, legacy },
				southMacro, `"west": {"release": {"type": "macro", "file": "F15.ts"}}`)
			s.Handle(input.Event{Kind: input.DeviceAdded, Device: 3, Info: &input.DeviceInfo{ID: 3, IsGamepad: true}})
			var st slot.State
			st.Buttons[control.South] = true
			st.Buttons[control.West] = true
			s.Resync(st)
			handleAt(sim, s, 50, button("south", false))
			handleAt(sim, s, 60, button("west", false))
			settle(t, sim, s, time.Second)
			if len(fake.runs) != 0 {
				t.Fatalf("held controls ran %v", fake.runs)
			}
			if err := s.Replay(sim.Clock, mustInput(t, "+south@2000 -south@2050 +west@2100 -west@2150")); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(fake.runs, []string{"F13.ts@2050", "F15.ts@2150"}) {
				t.Errorf("runs %v", fake.runs)
			}
		})
	}
}

// A pad assigned while the profile needed no runtime had no Resync
// barrier: the reload that adds the first macro opens the gate.
func TestMacroOnlyReloadAddsFirstMacro(t *testing.T) {
	sim := simtest.New(t)
	s, fake := macroSlot(t, sim, func(f *fakeMacros) { f.wait, f.needed = true, false }, eastHold)
	s.Handle(input.Event{Kind: input.DeviceAdded, Device: 3, Info: &input.DeviceInfo{ID: 3, IsGamepad: true}})
	fake.needed = true
	if err := s.Reload(compile(t, southMacro), 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Replay(sim.Clock, mustInput(t, "+south@0 -south@50")); err != nil {
		t.Fatal(err)
	}
	sim.ExpectOutput("+F13@50 -F13@150")
	// Without the reload's gate the press would have waited for a Resync
	// that never comes.
	if !slices.Contains(fake.calls, "SetPaused(false)") {
		t.Errorf("calls %v", fake.calls)
	}
}

// A macro triggered while the runtime starts runs once it is ready; the
// declarative bindings do not wait for it.
func TestMacroOnlyColdStart(t *testing.T) {
	sim := simtest.New(t)
	s, fake := macroSlot(t, sim, func(f *fakeMacros) { f.ready = false }, southMacro, eastHold)
	handleAt(sim, s, 0, button("south", true))
	handleAt(sim, s, 30, button("south", false))
	handleAt(sim, s, 40, button("south", true))
	handleAt(sim, s, 60, button("south", false))
	handleAt(sim, s, 70, button("east", true))
	handleAt(sim, s, 90, button("east", false))
	sim.ExpectOutput("+Space@70 -Space@90")
	if len(fake.runs) != 0 {
		t.Fatalf("ran before ready: %v", fake.runs)
	}
	fake.ready = true
	sim.Clock.Set(clock.Time(100 * time.Millisecond))
	settle(t, sim, s, time.Second)
	if !slices.Equal(fake.runs, []string{"F13.ts@100"}) {
		t.Errorf("runs %v, want one run once ready", fake.runs)
	}
	sim.ExpectOutput("+F13@100 -F13@200")
}

// The engine sees the profile as without a script: an unbound stick
// direction is no control, so it decides nothing.
func TestMacroOnlyAddsNoVirtualControls(t *testing.T) {
	sim := simtest.New(t)
	fake := newFakeMacros(t, sim.Clock, sim.Arb)
	var decs []string
	s, err := slot.New(slot.Config{Gen: 1, Program: compile(t, southMacro), Arbiter: sim.Arb, Clock: sim.Clock, Script: fake,
		Trace: func(d engine.Decision) { decs = append(decs, d.Control.String()) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.ReleaseAll(); _ = s.Dispose() })
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "leftx", Value: 30000})
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "leftx"})
	settle(t, sim, s, time.Second)
	if len(decs) != 0 {
		t.Errorf("decisions for %v", decs)
	}
}

func mustInput(t *testing.T, trace string) []input.Event {
	t.Helper()
	evs, err := parseInput(trace)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}
