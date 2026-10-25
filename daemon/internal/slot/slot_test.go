package slot_test

import (
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

const ms = time.Millisecond

// Bindings of the base layer, as JSON members, for the test profiles.
const (
	eastHold = `"east": {"label": "抓手", "press": {"type": "keys", "keys": "Space", "mode": "hold"}}`
	southTap = `"south": {"label": "撤销", "press": {"type": "keys", "keys": "Ctrl+Z", "mode": "tap"}}`
	lbCtrl   = `"lb": {"press": {"type": "keys", "keys": "LCtrl"}}`
	ltAlt    = `"lt": {"label": "吸管", "press": {"type": "keys", "keys": "Alt"}}`
)

// compile parses a profile whose base layer has the given bindings and
// compiles it. Warnings are fine; errors fail the test.
func compile(t testing.TB, bindings ...string) *engine.Program {
	t.Helper()
	src := `{"schemaVersion": 0, "name": "t", "layers": {"base": {` + strings.Join(bindings, ", ") + `}}}`
	p, ds := profile.Parse([]byte(src))
	if p == nil {
		t.Fatalf("profile.Parse: %v", ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return prog
}

// harness is a Slot on a simtest.Sim that records its decisions.
type harness struct {
	*simtest.Sim
	s    *slot.Slot
	decs []engine.Decision
}

// newHarness returns a slot with the given index and generation (1 when
// 0) over a profile with bindings. When the test ends, it fails the test
// if the slot has errors the test did not take; the Sim checks that
// nothing is held.
func newHarness(t *testing.T, index uint8, gen uint32, bindings ...string) *harness {
	t.Helper()
	h := &harness{Sim: simtest.New(t)}
	h.s = h.newSlot(t, index, gen, compile(t, bindings...))
	return h
}

// newSlot adds a slot on h's Arbiter and clock.
func (h *harness) newSlot(t *testing.T, index uint8, gen uint32, prog *engine.Program) *slot.Slot {
	t.Helper()
	if gen == 0 {
		gen = 1
	}
	s, err := slot.New(slot.Config{
		Index: index, Gen: gen, Program: prog, Arbiter: h.Arb, Clock: h.Clock,
		Trace: func(d engine.Decision) { h.decs = append(h.decs, d) },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if errs := s.Errors(); len(errs) > 0 {
			t.Errorf("slot errors the test did not take:\n%v", errors.Join(errs...))
		}
	})
	return s
}

// replay converts trace, in input.trace notation, and replays it.
func (h *harness) replay(t *testing.T, trace string) {
	t.Helper()
	evs, err := parseInput(trace)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.s.Replay(h.Clock, evs); err != nil {
		t.Fatalf("Replay: %v", err)
	}
}

// at moves the clock to t ms without running deadlines: the test knows
// there are none on the way.
func (h *harness) at(t *testing.T, msec int) {
	t.Helper()
	target := clock.Time(time.Duration(msec) * ms)
	if d, ok := h.s.NextDeadline(); ok && d < target {
		t.Fatalf("at(%d): deadline %v on the way", msec, d)
	}
	h.Clock.Set(target)
}

// expectDecisions compares the decisions since the last call, as
// "@t control edge reason" each, with want.
func (h *harness) expectDecisions(t *testing.T, want ...string) {
	t.Helper()
	got := make([]string, len(h.decs))
	for i, d := range h.decs {
		msec := strconv.FormatFloat(float64(d.T)/float64(ms), 'f', -1, 64)
		got[i] = "@" + msec + " " + d.Control.String() + " " + d.Edge.String() + " " + d.Reason.String()
	}
	h.decs = nil
	if !slices.Equal(got, want) {
		t.Errorf("decisions:\n got %q\nwant %q", got, want)
	}
}

func button(c string, down bool) input.Event {
	k := input.ButtonUp
	if down {
		k = input.ButtonDown
	}
	return input.Event{Kind: k, Control: c}
}

func TestScriptRuntimeReceivesButtonsAndReleasesOnReset(t *testing.T) {
	sim := simtest.New(t)
	fake := new(fakeScriptRuntime)
	s, err := slot.New(slot.Config{
		Index: 0, Gen: 1, Program: compile(t), Arbiter: sim.Arb, Clock: sim.Clock, Script: fake,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Handle(button("south", true))
	s.Handle(button("south", false))
	if len(fake.events) != 2 || fake.events[0].Gesture != "press" || fake.events[1].Gesture != "release" {
		t.Fatalf("script events = %+v", fake.events)
	}
	s.Reset()
	if fake.releases != 1 {
		t.Fatalf("script releases = %d, want 1", fake.releases)
	}
	if err := s.Dispose(); err != nil {
		t.Fatal(err)
	}
	if fake.disposals != 1 {
		t.Fatalf("script disposals = %d, want 1", fake.disposals)
	}
}

func TestScriptRuntimeConsumptionSkipsDeclarativeEngine(t *testing.T) {
	sim := simtest.New(t)
	fake := &fakeScriptRuntime{consumed: true}
	s, err := slot.New(slot.Config{
		Index: 0, Gen: 1, Program: compile(t, southTap), Arbiter: sim.Arb, Clock: sim.Clock, Script: fake,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A script handler that consumes both edges owns the event entirely. The
	// declaration with the same control must not produce a second action.
	s.Handle(button("south", true))
	s.Handle(button("south", false))
	if got := simtest.FormatTrace(sim.Out.Edges()); got != "" {
		t.Fatalf("consumed script event produced declarative output %q", got)
	}

	// Passing the event through restores the declaration path.
	fake.consumed = false
	s.Handle(button("south", true))
	if got := simtest.FormatTrace(sim.Out.Edges()); got != "+LCtrl@0 +Z@0" {
		t.Fatalf("passed script event output = %q, want declarative tap", got)
	}
	if err := s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
}

func TestScriptAsymmetricConsumptionEndsPressCycle(t *testing.T) {
	for _, tc := range []struct {
		name, consumedGesture, want string
	}{
		{"release only", "release", "+Space@0 -Space@0"},
		{"press only", "press", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sim := simtest.New(t)
			fake := &fakeScriptRuntime{consumeGesture: tc.consumedGesture}
			s, err := slot.New(slot.Config{Index: 0, Gen: 1, Program: compile(t, eastHold), Arbiter: sim.Arb, Clock: sim.Clock, Script: fake})
			if err != nil {
				t.Fatal(err)
			}
			s.Handle(button("east", true))
			s.Handle(button("east", false))
			if got := simtest.FormatTrace(sim.Out.Edges()); got != tc.want {
				t.Fatalf("edges = %q, want %q", got, tc.want)
			}
			if err := s.ReleaseAll(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestScriptReloadKeepsNewRuntimeOutput(t *testing.T) {
	sim := simtest.New(t)
	fake := &fakeScriptRuntime{arb: sim.Arb}
	s, err := slot.New(slot.Config{
		Index: 0, Gen: 1, Program: compile(t), Arbiter: sim.Arb, Clock: sim.Clock, Script: fake,
	})
	if err != nil {
		t.Fatal(err)
	}
	fake.reload = func(gen uint32) {
		if err := fake.arb.Down(output.Owner{Slot: 0, Gen: gen, Source: output.SourceScript, Cycle: 1}, keys.MustParse("Z")); err != nil {
			t.Errorf("new runtime output: %v", err)
		}
	}
	if err := s.Reload(compile(t), 2); err != nil {
		t.Fatal(err)
	}
	if got := simtest.FormatTrace(sim.Out.Edges()); got != "+Z@0" {
		t.Fatalf("reload output = %q, want new runtime key held", got)
	}
	if held := sim.Arb.Held(); len(held) != 1 || len(held[0].Owners) != 1 || held[0].Owners[0].Gen != 2 {
		t.Fatalf("held after reload = %+v, want generation 2", held)
	}
	if err := s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
}

type fakeScriptRuntime struct {
	events         []slot.ScriptEvent
	consumed       bool
	consumeGesture string
	runs           int
	releases       int
	disposals      int
	arb            slot.Arbiter
	reload         func(uint32)
}

func (f *fakeScriptRuntime) Dispatch(e slot.ScriptEvent) (bool, error) {
	f.events = append(f.events, e)
	return f.consumed || f.consumeGesture == e.Gesture, nil
}
func (f *fakeScriptRuntime) RunDue() error                    { f.runs++; return nil }
func (f *fakeScriptRuntime) NextDeadline() (clock.Time, bool) { return 0, false }
func (f *fakeScriptRuntime) ReleaseAll() error                { f.releases++; return nil }
func (f *fakeScriptRuntime) Reload(gen uint32) error {
	if f.reload != nil {
		f.reload(gen)
	}
	return nil
}
func (f *fakeScriptRuntime) Dispose() error { f.disposals++; return nil }

func TestNewErrors(t *testing.T) {
	sim := simtest.New(t)
	prog := compile(t, eastHold)
	ok := slot.Config{Gen: 1, Program: prog, Arbiter: sim.Arb, Clock: sim.Clock}
	if _, err := slot.New(ok); err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, tc := range []struct {
		name string
		edit func(*slot.Config)
		want string
	}{
		{"no program", func(c *slot.Config) { c.Program = nil }, "program"},
		{"no arbiter", func(c *slot.Config) { c.Arbiter = nil }, "Arbiter"},
		{"nil *output.Arbiter", func(c *slot.Config) { c.Arbiter = (*output.Arbiter)(nil) }, "Arbiter"},
		{"nil *output.RemoteArbiter", func(c *slot.Config) { c.Arbiter = (*output.RemoteArbiter)(nil) }, "Arbiter"},
		{"no clock", func(c *slot.Config) { c.Clock = nil }, "clock"},
		{"gen 0", func(c *slot.Config) { c.Gen = 0 }, "generation"},
	} {
		cfg := ok
		tc.edit(&cfg)
		if s, err := slot.New(cfg); s != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: New = %v, %v; want nil and an error about %s", tc.name, s, err, tc.want)
		}
	}
}

// TestHoldReleasedByControl is the normal path: the release of the
// control lets go of the key.
func TestHoldReleasedByControl(t *testing.T) {
	h := newHarness(t, 0, 0, eastHold)
	h.replay(t, "+east@0 -east@300")
	h.ExpectOutput("+Space@0 -Space@300")
	h.expectDecisions(t, "@0 east down dispatch", "@300 east up release")
}

// TestReleaseAllWhileHeld: ReleaseAll lets go of the held keys at once,
// and the later physical release does nothing.
func TestReleaseAllWhileHeld(t *testing.T) {
	h := newHarness(t, 0, 0, eastHold, lbCtrl, ltAlt)
	h.replay(t, "+east@0 +lb@10 lefttrigger=0.5@20")
	h.ExpectOutput("+Space@0 +LCtrl@10 +LAlt@20")
	h.at(t, 100)
	if err := h.s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	// Non-modifiers first, then modifiers in reverse press order.
	h.ExpectOutput("-Space@100 -LAlt@100 -LCtrl@100")
	simtest.AssertNoneHeld(t, h.Arb, h.Out)
	// The trigger is still pulled: it must drop to TriggerRelease before
	// it presses again.
	h.replay(t, "-east@200 -lb@210 lefttrigger=1@220 lefttrigger=0.2@230 lefttrigger=0.3@240")
	h.ExpectOutput("+LAlt@240")
	h.replay(t, "+east@300 lefttrigger=0@310 -east@320")
	h.ExpectOutput("+Space@300 -LAlt@310 -Space@320")
	h.expectDecisions(t,
		"@0 east down dispatch", "@10 leftshoulder down dispatch", "@20 lt down dispatch",
		"@100 east none cancel", "@100 leftshoulder none cancel", "@100 lt none cancel",
		"@200 east up canceled", "@210 leftshoulder up canceled", "@230 lt up canceled", "@240 lt down dispatch",
		"@300 east down dispatch", "@310 lt up release", "@320 east up release")
}

// TestDeviceRemovedWhileHeld: a removed device releases its keys and its
// controls count as up, so the next press of the new device works.
func TestDeviceRemovedWhileHeld(t *testing.T) {
	h := newHarness(t, 0, 0, eastHold, lbCtrl, southTap)
	h.replay(t, "+east@0 +lb@10")
	h.at(t, 20)
	h.s.Handle(button("south", true))
	h.at(t, 30)
	h.s.Handle(input.Event{Kind: input.DeviceRemoved, Device: 7})
	// The tap was still holding Ctrl+Z: it ends at once too.
	h.ExpectOutput("+Space@0 +LCtrl@10 +Z@20 -Z@30 -Space@30 -LCtrl@30")
	simtest.AssertNoneHeld(t, h.Arb, h.Out)
	if d, ok := h.s.NextDeadline(); ok {
		t.Errorf("NextDeadline = %v after the release, want none", d)
	}
	// The reconnected device starts with every control up: a stale up is
	// spurious, a press works.
	h.at(t, 40)
	h.s.Handle(input.Event{Kind: input.DeviceAdded, Device: 8, Info: &input.DeviceInfo{ID: 8}})
	h.replay(t, "-east@50 +east@60 -east@70")
	h.ExpectOutput("+Space@60 -Space@70")
	h.expectDecisions(t,
		"@0 east down dispatch", "@10 leftshoulder down dispatch", "@20 south down dispatch",
		"@30 south none cancel", "@30 east none cancel", "@30 leftshoulder none cancel",
		"@50 east up spurious", "@60 east down dispatch", "@70 east up release")
}

// TestOnEvent: OnEvent gets every event at its time, before the decisions
// and the key commands the event causes.
func TestOnEvent(t *testing.T) {
	sim := simtest.New(t)
	var log []string
	s, err := slot.New(slot.Config{
		Gen: 1, Program: compile(t, eastHold, ltAlt), Arbiter: sim.Arb, Clock: sim.Clock,
		Trace: func(d engine.Decision) { log = append(log, "decision "+d.Control.String()+" "+d.Reason.String()) },
		OnEvent: func(ev input.Event) {
			log = append(log, "event "+strings.TrimSpace(ev.Kind.String()+" "+ev.Control)+" @"+sim.Clock.Now().String())
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sim.Out.Inject = func(evs []output.KeyEvent) (int, error) {
		log = append(log, "send "+strconv.Itoa(len(evs)))
		return len(evs), nil
	}
	evs := []input.Event{
		{Time: 0, Device: 1, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: 1}},
		{Time: uint64(10 * ms), Device: 1, Kind: input.ButtonDown, Control: "east"},
		{Time: uint64(20 * ms), Device: 1, Kind: input.AxisMotion, Control: "lefttrigger", Value: 5000},
		{Time: uint64(30 * ms), Device: 1, Kind: input.AxisMotion, Control: "lefttrigger", Value: 20000},
		{Time: uint64(40 * ms), Device: 1, Kind: input.DeviceRemoved},
	}
	if err := s.Replay(sim.Clock, evs); err != nil {
		t.Fatal(err)
	}
	sim.ExpectOutput("+Space@10 +LAlt@30 -Space@40 -LAlt@40")
	want := []string{
		"event added @0s",
		"event down east @10ms", "decision east dispatch", "send 1",
		"event axis lefttrigger @20ms",
		"event axis lefttrigger @30ms", "decision lt dispatch", "send 1",
		"event removed @40ms", "decision east cancel", "decision lt cancel", "send 2",
	}
	if !slices.Equal(log, want) {
		t.Errorf("log:\n got %q\nwant %q", log, want)
	}
}

// TestReleaseAllTapInProgress: ReleaseAll ends a tap at once, not at the
// end of its hold (ARCHITECTURE §8.2).
func TestReleaseAllTapInProgress(t *testing.T) {
	h := newHarness(t, 0, 0, southTap)
	h.s.Handle(button("south", true))
	if d, ok := h.s.NextDeadline(); !ok || d != clock.Time(engine.DefaultTapPress) {
		t.Fatalf("NextDeadline = %v, %v; want the end of the tap, %v", d, ok, engine.DefaultTapPress)
	}
	h.at(t, 5)
	if err := h.s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	h.ExpectOutput("+LCtrl@0 +Z@0 -Z@5 -LCtrl@5")
	if d, ok := h.s.NextDeadline(); ok {
		t.Errorf("NextDeadline = %v after ReleaseAll, want none", d)
	}
	h.replay(t, "-south@50")
	h.ExpectOutput("")
	h.expectDecisions(t, "@0 south down dispatch", "@5 south none cancel", "@50 south up canceled")
}

func TestReleaseAllTwice(t *testing.T) {
	h := newHarness(t, 0, 0, eastHold)
	h.replay(t, "+east@0")
	for range 2 {
		if err := h.s.ReleaseAll(); err != nil {
			t.Fatal(err)
		}
	}
	h.ExpectOutput("+Space@0 -Space@0")
	if n := h.Out.Calls(); n != 2 {
		t.Errorf("%d Send calls, want 2: the second ReleaseAll has nothing to send", n)
	}
	h.expectDecisions(t, "@0 east down dispatch", "@0 east none cancel")
}

// TestReleaseAllSendFailures: releases that fail are retried until they
// go out; ReleaseAll reports the failure.
func TestReleaseAllSendFailures(t *testing.T) {
	h := newHarness(t, 0, 0, eastHold, lbCtrl)
	h.replay(t, "+east@0 +lb@10")
	h.at(t, 50)
	// The batch and its immediate resend fail, and so does the first retry.
	h.Out.FailNext(1, nil)
	h.Out.FailNext(0, nil)
	h.Out.FailNext(0, nil)
	err := h.s.ReleaseAll()
	if !errors.Is(err, simtest.ErrBlocked) {
		t.Fatalf("ReleaseAll = %v, want ErrBlocked", err)
	}
	h.ExpectOutput("+Space@0 +LCtrl@10 -Space@50")
	if held := h.Arb.Held(); len(held) != 1 || !held[0].Pending {
		t.Fatalf("Held = %+v, want LCtrl pending release", held)
	}
	if d, ok := h.s.NextDeadline(); !ok || d != clock.Time(50*ms+output.DefaultRetryInterval) {
		t.Fatalf("NextDeadline = %v, %v; want the retry at %v", d, ok, 50*ms+output.DefaultRetryInterval)
	}
	// Replay settles: the first retry at 150 ms fails, the second goes out.
	if err := h.s.Replay(h.Clock, nil); err != nil {
		t.Fatal(err)
	}
	h.ExpectOutput("-LCtrl@250")
	if errs := h.s.Errors(); len(errs) != 1 || !errors.Is(errs[0], simtest.ErrBlocked) {
		t.Errorf("Errors = %v, want the failed retry", errs)
	}
	simtest.AssertNoneHeld(t, h.Arb, h.Out)
}

// TestReleaseAllOwnSlot: a slot releases only its own keys.
func TestReleaseAllOwnSlot(t *testing.T) {
	h := newHarness(t, 0, 0, eastHold)
	other := h.newSlot(t, 1, 1, compile(t, eastHold, lbCtrl))
	h.replay(t, "+east@0")
	other.Handle(button("east", true))
	other.Handle(button("lb", true))
	if err := other.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	h.ExpectOutput("+Space@0 +LCtrl@0 -LCtrl@0")
	if held := h.Arb.Held(); len(held) != 1 || len(held[0].Owners) != 1 || held[0].Owners[0].Slot != 0 {
		t.Errorf("Held = %+v, want Space held by slot 0 only", held)
	}
	h.replay(t, "-east@10")
	h.ExpectOutput("-Space@10")
}

// TestErrors: Arbiter errors of forwarded commands and of Advance are
// collected, and Errors takes them.
func TestErrors(t *testing.T) {
	h := newHarness(t, 0, 0, eastHold, southTap)
	h.Out.FailNext(0, nil) // the press of Space
	h.replay(t, "+east@0")
	errs := h.s.Errors()
	if len(errs) != 1 || !errors.Is(errs[0], simtest.ErrBlocked) {
		t.Fatalf("Errors = %v, want the failed press", errs)
	}
	if errs := h.s.Errors(); errs != nil {
		t.Fatalf("second Errors = %v, want nil", errs)
	}
	// The press counts as down: its release still goes out.
	h.replay(t, "-east@10")
	h.ExpectOutput("-Space@10")

	// The end of a tap fails in Advance, twice, then goes out on retry.
	h.at(t, 100)
	h.s.Handle(button("south", true))
	h.Out.FailNext(0, nil)
	h.Out.FailNext(0, nil)
	h.replay(t, "-south@200")
	h.ExpectOutput("+LCtrl@100 +Z@100 -Z@220 -LCtrl@220")
	errs = h.s.Errors()
	if len(errs) != 1 || !errors.Is(errs[0], simtest.ErrBlocked) || !strings.Contains(errs[0].Error(), "failed twice") {
		t.Fatalf("Errors = %v, want the failed tap end", errs)
	}
}

// TestOwner: the Arbiter sees slot/gen/decl/cycle owners.
func TestOwner(t *testing.T) {
	h := newHarness(t, 3, 7, eastHold, lbCtrl)
	h.replay(t, "+east@0 -east@10 +east@20 +lb@30")
	var got []string
	for _, k := range h.Arb.Held() {
		for _, o := range k.Owners {
			got = append(got, o.String())
		}
	}
	if want := []string{"3/7/decl/2", "3/7/decl/3"}; !slices.Equal(got, want) {
		t.Errorf("owners = %q, want %q", got, want)
	}
	for _, tc := range []struct {
		o    engine.OwnerID
		want output.Owner
	}{
		{engine.OwnerID{Source: engine.SourceDecl, Cycle: 17}, output.Owner{Slot: 3, Gen: 7, Source: output.SourceDecl, Cycle: 17}},
		{engine.OwnerID{Source: engine.SourceScript}, output.Owner{Slot: 3, Gen: 7, Source: output.SourceScript}},
		{engine.OwnerID{Cycle: 1}, output.Owner{Slot: 3, Gen: 7, Source: output.SourceInvalid, Cycle: 1}},
		{engine.OwnerID{Source: 9}, output.Owner{Slot: 3, Gen: 7, Source: output.SourceInvalid}},
	} {
		if got := h.s.Owner(tc.o); got != tc.want {
			t.Errorf("Owner(%v) = %v, want %v", tc.o, got, tc.want)
		}
	}
	if err := h.s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
}

// TestUnknownInput: events of controls the engine does not know are
// dropped, each name reported once.
func TestUnknownInput(t *testing.T) {
	h := newHarness(t, 0, 0, eastHold, ltAlt)
	for range 2 {
		h.s.Handle(button("button30", true))
		h.s.Handle(button("button30", false))
		h.s.Handle(button("lt", true)) // the trigger is an axis
		h.s.Handle(input.Event{Kind: input.AxisMotion, Control: "axis9", Value: 32767})
		h.s.Handle(input.Event{Kind: 99, Control: "east"})
		h.s.Handle(input.Event{Kind: input.ComboHeld}) // only Replay toggles the pause
	}
	var got []string
	for _, err := range h.s.Errors() {
		if !errors.Is(err, slot.ErrUnknownInput) {
			t.Errorf("error %v is not ErrUnknownInput", err)
		}
		got = append(got, err.Error())
	}
	want := []string{
		`slot: unknown input: button "button30"; its events are dropped`,
		`slot: unknown input: button "lt"; its events are dropped`,
		`slot: unknown input: axis "axis9"; its events are dropped`,
		`slot: unknown input: event kind99; such events are dropped`,
		`slot: unknown input: event combo; such events are dropped`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("errors:\n got %q\nwant %q", got, want)
	}
	h.ExpectOutput("")
	h.expectDecisions(t)
	// Known controls still work.
	h.replay(t, "+east@0 -east@10")
	h.ExpectOutput("+Space@0 -Space@10")
}

// TestReplay checks that Replay runs deadlines at their exact time, before
// an event at the same time, and settles after the last event.
func TestReplay(t *testing.T) {
	h := newHarness(t, 0, 0, southTap, eastHold,
		`"west": {"press": {"type": "keys", "keys": "E", "mode": "tap"}}`)
	h.replay(t, "+south@0 +west@10 -south@15 +east@20 -west@25 -east@30 +west@40")
	h.ExpectOutput("+LCtrl@0 +Z@0 +E@10 -Z@20 -LCtrl@20 +Space@20 -E@30 -Space@30 +E@40 -E@60")
	if now := h.Clock.Now(); now != clock.Time(60*ms) {
		t.Errorf("clock at %v after Replay, want the end of the last tap, 60ms", now)
	}
	// Times are absolute: an event before now is an error, and so is one
	// that does not fit a clock.Time. Nothing is handled.
	for _, evs := range [][]input.Event{
		{{Time: uint64(59 * ms), Kind: input.ButtonDown, Control: "south"}},
		{{Time: uint64(70 * ms), Kind: input.ButtonUp, Control: "west"}, {Time: uint64(69 * ms), Kind: input.ButtonDown, Control: "west"}},
		{{Time: math.MaxInt64 + 1, Kind: input.ButtonDown, Control: "south"}},
	} {
		if err := h.s.Replay(h.Clock, evs); err == nil {
			t.Errorf("Replay(%+v) = nil, want an error", evs)
		}
	}
	h.ExpectOutput("")
	if err := h.s.Replay(clock.NewFake(), nil); err == nil {
		t.Error("Replay on another clock = nil, want an error")
	}
	h.expectDecisions(t,
		"@0 south down dispatch", "@10 west down dispatch", "@15 south up end", "@20 east down dispatch",
		"@25 west up end", "@30 east up release", "@40 west down dispatch")
	h.replay(t, "-west@80")
	h.expectDecisions(t, "@80 west up end")
}

// TestReplaySettleLimit: Replay gives up when the slot is still busy a
// minute after the last event.
func TestReplaySettleLimit(t *testing.T) {
	h := newHarness(t, 0, 0, eastHold)
	h.replay(t, "+east@0")
	h.Out.Inject = func([]output.KeyEvent) (int, error) { return 0, simtest.ErrBlocked }
	h.s.Handle(button("east", false))
	err := h.s.Replay(h.Clock, nil)
	if err == nil || !strings.Contains(err.Error(), "still busy") {
		t.Fatalf("Replay = %v, want still busy", err)
	}
	if now := h.Clock.Now(); now > clock.Time(time.Minute) {
		t.Errorf("clock at %v, want at most a minute", now)
	}
	h.Out.Inject = nil
	if err := h.s.Replay(h.Clock, nil); err != nil {
		t.Fatal(err)
	}
	h.ExpectOutput("+Space@0 -Space@60100")
	// A failed release, then 600 failed retries: the Slot keeps the first
	// hundred errors.
	errs := h.s.Errors()
	if len(errs) != 101 || !strings.Contains(errs[100].Error(), "501 more errors dropped") {
		t.Errorf("%d errors, the last %v; want 100 and 501 dropped", len(errs), errs[len(errs)-1])
	}
}
