package slot_test

import (
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// markRig is a Slot over a Loop's RemoteArbiter on a fake clock, with the
// latency sink of the Arbiter (DMN-15, ADR-0071): which input edges end in a
// measurement, and under which key.
type markRig struct {
	t    *testing.T
	clk  *clock.Fake
	loop *output.Loop
	rec  *simtest.SyncRecorder
	s    *slot.Slot

	mu      sync.Mutex
	samples []string // the keys of the samples, in order
}

const padKey = "pad-key-1"

func newMarkRig(t *testing.T, script slot.ScriptRuntime, bindings ...string) *markRig {
	t.Helper()
	var r *markRig
	r = newMarkRigSink(t, script, func(key string, d time.Duration) {
		if d < 0 || d > time.Minute {
			t.Errorf("sample of %s: %v", key, d)
		}
		r.mu.Lock()
		r.samples = append(r.samples, key)
		r.mu.Unlock()
	}, bindings...)
	return r
}

// newMarkRigSink is newMarkRig with the sink of the Arbiter given.
func newMarkRigSink(t *testing.T, script slot.ScriptRuntime, sink func(string, time.Duration), bindings ...string) *markRig {
	t.Helper()
	r := &markRig{t: t, clk: clock.NewFake()}
	r.rec = simtest.NewSyncRecorder(r.clk)
	r.loop = output.StartLoop(r.clk, r.rec, output.LoopOptions{Arbiter: output.ArbiterOptions{Latency: sink}})
	s, err := slot.New(slot.Config{Gen: 1, Program: compile(t, bindings...), Arbiter: r.loop.Remote(), Clock: r.clk, Script: script})
	if err != nil {
		t.Fatal(err)
	}
	r.s = s
	t.Cleanup(func() {
		_ = r.s.ReleaseAll()
		if err := r.loop.Stop(); err != nil {
			t.Errorf("Stop: %v", err)
		}
		r.rec.With(func(rec *simtest.Recorder) { simtest.AssertNoneHeld(t, nil, rec) })
	})
	s.Handle(input.Event{Kind: input.DeviceAdded, Device: 1, AssignedKey: padKey, Info: &input.DeviceInfo{ID: 1, Name: "pad", IsGamepad: true}})
	return r
}

// stamped is an edge as T-input delivers it.
func stamped(control string, down bool) input.Event {
	e := button(control, down)
	e.Stamp = latency.Now()
	return e
}

// count returns the keys of the samples that have come since the last call.
// A command is answered after T-output ran it, and the sink runs inside it, so
// the samples of the edges handled so far are all there.
func (r *markRig) count() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	got := r.samples
	r.samples = nil
	return got
}

func (r *markRig) want(what string, keys ...string) {
	r.t.Helper()
	got := r.count()
	if len(got) != len(keys) {
		r.t.Errorf("%s: samples %v, want %v", what, got, keys)
		return
	}
	for i := range keys {
		if got[i] != keys[i] {
			r.t.Errorf("%s: samples %v, want %v", what, got, keys)
			return
		}
	}
}

// TestSlotMarksHandledEdges (DMN-15): the slot marks the keys of an input edge it
// handles with the edge's Stamp and its device key: a press and a release of
// a hold binding are one sample each. An event without a Stamp (a trace, a
// replay) is not measured, and neither is a key that nothing sends: a
// paused slot presses nothing.
func TestSlotMarksHandledEdges(t *testing.T) {
	r := newMarkRig(t, nil, eastHold, southTap)
	r.s.Handle(stamped("east", true))
	r.want("the press of a hold", padKey)
	r.s.Handle(stamped("east", false))
	r.want("its release", padKey)

	r.s.Handle(button("east", true))
	r.s.Handle(button("east", false))
	r.want("edges without a Stamp")

	// A tap sends on the press edge: one sample for the edge, none for the end of the tap.
	r.s.Handle(stamped("south", true))
	r.want("the press of a tap", padKey)
	r.clk.Advance(100 * time.Millisecond)
	r.s.Advance()
	r.s.Handle(stamped("south", false))
	r.want("the release of a tap")

	if err := r.s.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	r.s.Handle(stamped("east", true))
	r.s.Handle(stamped("east", false))
	r.want("a paused slot")
}

// TestSlotUnmarkedOutput: the output that does not come out of an input edge's
// handling is not measured: a ReleaseAll, and the work of Advance (the end of
// a tap, the commit of a hold, the repeats of a key held), also right after an
// edge, since the mark does not outlive Handle.
func TestSlotUnmarkedOutput(t *testing.T) {
	const westHold = `"west": {"tap": {"type": "keys", "keys": "F13"}, "hold": {"type": "keys", "keys": "F14"}}`
	const repeating = `"north": {"press": {"type": "keys", "keys": "F15", "mode": "hold", "repeat": {"delayMs": 30, "rateHz": 50}}}`
	r := newMarkRig(t, nil, eastHold, westHold, repeating)

	// A hold of a tap-or-hold binding: the press waits, the commit at the deadline
	// sends, from Advance. Only the release edge's key is measured.
	r.s.Handle(stamped("west", true))
	r.want("the press of a pending west")
	r.clk.Advance(time.Second)
	r.s.Advance()
	r.want("the commit of the hold")
	r.s.Handle(stamped("west", false))
	r.want("the release of the hold", padKey)

	// A tap decided by the release: measured from the release edge.
	r.s.Handle(stamped("west", true))
	r.clk.Advance(50 * time.Millisecond)
	r.s.Advance()
	r.s.Handle(stamped("west", false))
	r.want("the release that decides a tap", padKey)
	r.clk.Advance(time.Second)
	r.s.Advance()
	r.want("the end of that tap")

	// A repeat: the press is measured, the repeats are not.
	r.s.Handle(stamped("north", true))
	r.want("the press of a repeating key", padKey)
	for range 5 {
		r.clk.Advance(20 * time.Millisecond)
		r.s.Advance()
	}
	r.want("the repeats")
	r.s.Handle(stamped("north", false))
	r.want("the release of a repeating key", padKey)

	// ReleaseAll with a key held.
	r.s.Handle(stamped("east", true))
	r.want("the press of east", padKey)
	if err := r.s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	r.want("ReleaseAll")
}

// TestSlotMarkEveryEdgeOnce: an edge that makes several commands is measured
// once, at the first Send that goes out: the release of a tap-or-hold key that
// is decided as a tap makes the tap, and the buffered edge of another key that
// the tap replays does not add a second sample.
func TestSlotMarkEveryEdgeOnce(t *testing.T) {
	const westTap = `"west": {"tap": {"type": "keys", "keys": "F13"}, "hold": {"type": "keys", "keys": "F14"}}`
	r := newMarkRig(t, nil, westTap, southTap)
	r.s.Handle(stamped("west", true))
	r.s.Handle(stamped("south", true)) // an interruption: buffered with west pending
	r.want("the buffered press")
	r.s.Handle(stamped("west", false))
	got := r.count()
	if len(got) != 1 {
		t.Errorf("the release of west that decides the tap and replays south: samples %v, want one", got)
	}
	r.s.Handle(stamped("south", false))
	r.clk.Advance(time.Second)
	r.s.Advance()
	r.count()
}

// coldRuntime is a script runtime that is not ready: the slot queues the
// input (ADR-0044) and replays it once the runtime is.
type coldRuntime struct {
	fakeScriptRuntime
	pending, ready bool
}

func (*coldRuntime) Configured() bool { return true }
func (c *coldRuntime) InputStatus() (bool, bool) {
	return c.pending, c.ready
}

// TestSlotReplayedScriptInputUnmarked (DMN-15, ADR-0071): an edge that the slot
// queued while the script starts, and replays later, is not measured: its
// Stamp is old, and the user waited for the script, not for padmate. The
// first edge after the replay is.
func TestSlotReplayedScriptInputUnmarked(t *testing.T) {
	rt := &coldRuntime{pending: true}
	r := newMarkRig(t, rt, eastHold)
	r.s.Handle(stamped("east", true))
	r.want("a queued press")
	rt.pending, rt.ready = false, true
	r.s.Advance()
	r.rec.With(func(rec *simtest.Recorder) {
		if len(rec.Down()) == 0 {
			t.Error("the queued press was not replayed")
		}
	})
	r.want("the replay of the queued press")
	r.s.Handle(stamped("east", false))
	r.want("the first edge after the replay", padKey)
}

// markCounter is an Arbiter wrapper that implements MarkInput and records the
// marks, for the slot's side of the contract alone.
type markCounter struct {
	slot.Arbiter
	marks []string
}

func (m *markCounter) MarkInput(stamp latency.Stamp, key string) {
	if stamp == 0 {
		m.marks = append(m.marks, "-")
		return
	}
	m.marks = append(m.marks, key)
}

// TestSlotMarkProtocol: Handle sets the mark before an edge's handling and
// clears it after, for buttons and axes; not for the events of devices, for
// events without a Stamp, nor in a Slot whose Arbiter cannot mark.
func TestSlotMarkProtocol(t *testing.T) {
	sim := simtest.New(t)
	mc := &markCounter{Arbiter: sim.Arb}
	s, err := slot.New(slot.Config{Gen: 1, Program: compile(t, eastHold), Arbiter: mc, Clock: sim.Clock})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.ReleaseAll() })
	s.Handle(input.Event{Kind: input.DeviceAdded, Device: 1, AssignedKey: "k", Info: &input.DeviceInfo{ID: 1}})
	s.Handle(button("east", true))
	if len(mc.marks) != 0 {
		t.Fatalf("marks %v for a device event and an edge without a Stamp, want none", mc.marks)
	}
	s.Handle(stamped("east", false))
	axis := input.Event{Kind: input.AxisMotion, Control: "lefttrigger", Value: 100, Stamp: latency.Now()}
	s.Handle(axis)
	if got, want := mc.marks, []string{"k", "-", "k", "-"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Errorf("marks %v, want %v", got, want)
	}
	// An Arbiter that cannot mark is no error.
	s2, err := slot.New(slot.Config{Gen: 1, Program: compile(t, eastHold), Arbiter: sim.Arb, Clock: sim.Clock})
	if err != nil {
		t.Fatal(err)
	}
	s2.Handle(stamped("east", true))
	s2.Handle(stamped("east", false))
}

// TestSlotMarkNoAllocs (DMN-15): marking an edge adds no allocation to the path
// from Handle through the Loop to the Send and the Recorder: a press and a
// release with a Stamp allocate exactly as many times as the same without one.
// (The Loop allocates a reply channel and a closure per command, and the test's
// recorder a little; that is not new.)
func TestSlotMarkNoAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector may allocate; the other CI jobs run this")
	}
	rec := latency.NewRecorder()
	r := newMarkRigSink(t, nil, rec.Record, eastHold)
	measure := func(stamp bool) float64 {
		edge := func(down bool) input.Event {
			e := button("east", down)
			if stamp {
				e.Stamp = latency.Now()
			}
			return e
		}
		r.s.Handle(edge(true)) // warm up
		r.s.Handle(edge(false))
		return testing.AllocsPerRun(200, func() {
			r.s.Handle(edge(true))
			r.s.Handle(edge(false))
		})
	}
	plain, marked := measure(false), measure(true)
	if marked != plain {
		t.Errorf("%v allocations per press and release with a Stamp, %v without", marked, plain)
	}
	if s := rec.Summary(padKey); s.N == 0 {
		t.Error("the recorder has no sample: the marked edges were not measured")
	}
}
