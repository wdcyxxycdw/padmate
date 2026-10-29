package slot_test

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// Operations of FuzzReplay. Each takes four bytes: the opcode, arguments a
// and b, and dt, the milliseconds the trace time moves before the
// operation.
const (
	fzDown       = iota // button fuzzButtons[a] goes down
	fzUp                // button fuzzButtons[a] goes up
	fzLT                // lefttrigger moves to int16(a<<8|b)
	fzRT                // righttrigger moves to int16(a<<8|b)
	fzStick             // stick axis fuzzSticks[b] moves to int8(a)<<8
	fzReleaseAll        // ReleaseAll: after Replay if a is even, else after driving the events by hand, without settling
	fzRemove            // the device is removed
	fzFail              // a%3+1 Send calls from now on inject at most b%4 events
	fzUnknown           // an event the engine does not know, by a%3
	fzWait              // Replay, then wait a<<8|b ms
	fzPause             // SetPaused, pausing or resuming by turns: after Replay if a is even, else after driving the events by hand
	fzCombo             // a ComboHeld, which toggles the pause in Replay: replayed at once if a is even, else left pending
	fzOps
)

// fuzzBindings has hold and tap bindings, keys shared between holds
// (LShift) and between a hold and a tap (Space), a label-only binding and
// both triggers.
var fuzzBindings = []string{
	`"south": {"press": {"type": "keys", "keys": "Ctrl+Z", "mode": "tap"}}`,
	`"east": {"press": {"type": "keys", "keys": "Space", "mode": "hold"}}`,
	`"west": {"press": {"type": "keys", "keys": "E", "mode": "tap"}}`,
	`"leftshoulder": {"press": {"type": "keys", "keys": "LCtrl", "mode": "hold"}}`,
	`"rightshoulder": {"press": {"type": "keys", "keys": "LShift", "mode": "hold"}}`,
	`"dpad.up": {"press": {"type": "keys", "keys": "Shift", "mode": "hold"}}`,
	`"dpad.down": {"press": {"type": "keys", "keys": "Space", "mode": "tap"}}`,
	`"lt": {"press": {"type": "keys", "keys": "Alt", "mode": "hold"}}`,
	`"rt": {"press": {"type": "keys", "keys": "Ctrl+Shift+Z", "mode": "tap"}}`,
	`"start": {"label": "later"}`,
}

// fuzzHolds are the keys the hold bindings of fuzzBindings hold.
var fuzzHolds = map[control.Control]string{
	control.East:          "Space",
	control.LeftShoulder:  "LCtrl",
	control.RightShoulder: "LShift",
	control.DpadUp:        "LShift",
	control.LT:            "LAlt",
}

// fuzzButtons are the button names of fzDown and fzUp: bound, label-only
// and unbound controls, and an alias.
var fuzzButtons = [...]string{
	"south", "east", "west", "north", "leftshoulder", "rightshoulder",
	"dpad.up", "dpad.down", "dpad.left", "start", "lb",
}

var fuzzSticks = [...]control.Axis{control.LeftX, control.LeftY, control.RightX, control.RightY}

// fuzzRetry is the Arbiter's RetryInterval: short, so that failed
// releases settle within a few ops.
const fuzzRetry = 7 * ms

// fuzzSeeds are named regression and coverage inputs, as op, a, b, dt
// quadruples. The golden inputs are seeds too.
var fuzzSeeds = []struct {
	name string
	ops  []byte
}{
	{"hold", []byte{fzDown, 1, 0, 0, fzUp, 1, 0, 50}},
	{"tap on held modifier", []byte{fzDown, 4, 0, 0, fzDown, 0, 0, 10, fzUp, 0, 0, 5, fzUp, 4, 0, 30}},
	{"tap on held key", []byte{fzDown, 1, 0, 0, fzDown, 7, 0, 5, fzUp, 1, 0, 5, fzUp, 7, 0, 30}},
	{"shared key", []byte{fzDown, 5, 0, 0, fzDown, 6, 0, 10, fzUp, 5, 0, 10, fzUp, 6, 0, 10}},
	{"alias", []byte{fzDown, 10, 0, 0, fzDown, 4, 0, 10, fzUp, 4, 0, 10, fzUp, 10, 0, 10}},
	{"trigger hysteresis", []byte{fzLT, 0x26, 0x66, 0, fzLT, 0x20, 0, 10, fzLT, 0x19, 0x99, 10, fzLT, 0x80, 0, 10, fzLT, 0x7f, 0xff, 10, fzLT, 0, 0, 10}},
	{"both triggers", []byte{fzLT, 0x7f, 0xff, 0, fzRT, 0x7f, 0xff, 5, fzRT, 0, 0, 5, fzLT, 0, 0, 30}},
	{"release all settled", []byte{fzDown, 1, 0, 0, fzDown, 0, 0, 5, fzLT, 0x7f, 0, 5, fzReleaseAll, 0, 0, 10, fzUp, 1, 0, 10, fzLT, 0x50, 0, 5, fzLT, 0, 0, 5}},
	{"release all mid tap", []byte{fzDown, 4, 0, 0, fzDown, 0, 0, 5, fzReleaseAll, 1, 0, 5, fzUp, 0, 0, 5, fzDown, 0, 0, 5}},
	{"remove while held", []byte{fzDown, 1, 0, 0, fzRT, 0x7f, 0, 5, fzDown, 7, 0, 5, fzRemove, 0, 0, 5, fzUp, 1, 0, 5, fzDown, 1, 0, 5}},
	{"press after remove", []byte{fzDown, 1, 0, 0, fzLT, 0x7f, 0, 0, fzRemove, 0, 0, 5, fzDown, 1, 0, 5, fzLT, 0x7f, 0, 0}},
	{"failed release", []byte{fzDown, 1, 0, 0, fzFail, 1, 0, 10, fzUp, 1, 0, 0, fzWait, 0, 30, 0}},
	{"failed press", []byte{fzFail, 0, 0, 0, fzDown, 4, 0, 0, fzUp, 4, 0, 10}},
	{"failed tap end", []byte{fzDown, 0, 0, 0, fzFail, 2, 1, 15, fzWait, 0, 20, 10}},
	{"failed release all", []byte{fzDown, 1, 0, 0, fzDown, 4, 0, 0, fzFail, 2, 1, 10, fzReleaseAll, 1, 0, 0, fzDown, 1, 0, 3}},
	{"unknown", []byte{fzUnknown, 0, 0, 0, fzUnknown, 1, 0, 0, fzUnknown, 2, 0, 0, fzUnknown, 0, 0, 0, fzDown, 1, 0, 1}},
	{"sticks", []byte{fzStick, 0x80, 0, 0, fzStick, 0x7f, 1, 0, fzStick, 0, 2, 5}},
	{"pause while held", []byte{fzDown, 1, 0, 0, fzLT, 0x7f, 0, 5, fzDown, 0, 0, 5, fzPause, 1, 0, 5, fzDown, 4, 0, 5, fzUp, 1, 0, 5,
		fzPause, 0, 0, 5, fzUp, 4, 0, 5, fzLT, 0x10, 0, 5, fzLT, 0x7f, 0, 5, fzDown, 1, 0, 5}},
	{"press while paused", []byte{fzPause, 0, 0, 0, fzDown, 1, 0, 5, fzLT, 0x7f, 0xff, 5, fzRemove, 0, 0, 5, fzDown, 1, 0, 5,
		fzPause, 0, 0, 5, fzReleaseAll, 0, 0, 5, fzUp, 1, 0, 5, fzDown, 1, 0, 5}},
	{"failed pause", []byte{fzDown, 1, 0, 0, fzDown, 4, 0, 0, fzFail, 2, 1, 10, fzPause, 1, 0, 0, fzDown, 5, 0, 3, fzWait, 0, 30, 0}},
	{"combo while held", []byte{fzDown, 1, 0, 0, fzLT, 0x7f, 0, 5, fzDown, 0, 0, 5, fzCombo, 0, 0, 5, fzDown, 4, 0, 5, fzUp, 1, 0, 5,
		fzCombo, 1, 0, 5, fzUp, 4, 0, 5, fzDown, 1, 0, 5, fzPause, 1, 0, 5, fzCombo, 0, 0, 5, fzDown, 5, 0, 5}},
	{"combo pending", []byte{fzDown, 1, 0, 0, fzCombo, 1, 0, 5, fzDown, 4, 0, 5, fzCombo, 1, 0, 5, fzUp, 1, 0, 5, fzDown, 1, 0, 5,
		fzReleaseAll, 1, 0, 5, fzCombo, 1, 0, 5, fzDown, 5, 0, 5}},
	{"failed combo", []byte{fzDown, 1, 0, 0, fzDown, 4, 0, 0, fzFail, 2, 1, 10, fzCombo, 0, 0, 0, fzDown, 5, 0, 3, fzCombo, 1, 0, 5, fzDown, 6, 0, 5}},
	// The function key (MAP-27, start) has no engine cycle: the model must not
	// count one, or the owner of the next hold is off by one.
	{"function key takes no cycle", []byte{fzPause, 0, 0, 0, fzDown, 9, 0, 5, fzPause, 0, 0, 5, fzDown, 4, 0, 5, fzUp, 9, 0, 5}},
}

// FuzzReplay drives a Slot over fuzzBindings with random events at random
// times, ReleaseAll by Replay or by hand, pauses and resumes by SetPaused
// or by ComboHeld events in Replay, device removals and Send failures.
// Right after a pause, as after a ReleaseAll, no owner holds a key. At
// every Replay it checks the Arbiter against a model of the controls: the
// keys held are exactly those of the hold bindings whose control is down
// and whose cycle was not canceled (a press while paused starts canceled),
// the Recorder has no key down the Arbiter does not know, and the engine
// never sends an unmatched Up or a second Down. At the end, with failures
// off, it releases every control: nothing may be held; then ReleaseAll,
// and the Sim checks that nothing is held.
func FuzzReplay(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add(seed.ops)
	}
	traces, err := filepath.Glob(filepath.Join("testdata", "golden", "*", "input.trace"))
	if err != nil {
		f.Fatal(err)
	}
	for _, path := range traces {
		data, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		evs, err := parseInput(string(data))
		if err != nil {
			f.Fatalf("%s: %v", path, err)
		}
		f.Add(encodeOps(evs))
	}
	prog := compile(f, fuzzBindings...)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4*256 {
			data = data[:4*256]
		}
		fz := newFuzzer(t, prog)
		for i := 0; i < len(data); i += 4 {
			var arg [3]byte
			copy(arg[:], data[i+1:min(i+4, len(data))])
			fz.step(i/4, data[i]%fzOps, arg[0], arg[1], arg[2])
		}
		fz.finish()
	})
}

// encodeOps turns events into FuzzReplay operations, for a seed. The seed
// is an approximation: buttons not in fuzzButtons are left out, a stick
// keeps only the high byte of its value, and a gap of more than 255 ms
// becomes fzWait operations, each of which replays first.
func encodeOps(evs []input.Event) []byte {
	var ops []byte
	var prev uint64
	for _, ev := range evs {
		var op, a, b byte
		u := uint16(ev.Value)
		switch axis, _ := control.ParseAxis(ev.Control); {
		case ev.Kind == input.ButtonDown || ev.Kind == input.ButtonUp:
			i := slices.Index(fuzzButtons[:], ev.Control)
			if i < 0 {
				continue
			}
			op, a = fzDown, byte(i)
			if ev.Kind == input.ButtonUp {
				op = fzUp
			}
		case axis == control.LeftTrigger:
			op, a, b = fzLT, byte(u>>8), byte(u)
		case axis == control.RightTrigger:
			op, a, b = fzRT, byte(u>>8), byte(u)
		default:
			op, a, b = fzStick, byte(u>>8), byte(slices.Index(fuzzSticks[:], axis))
		}
		d := (ev.Time - prev) / uint64(ms)
		prev = ev.Time
		for d > math.MaxUint8 {
			w := min(d, math.MaxUint16)
			ops = append(ops, fzWait, byte(w>>8), byte(w), 0)
			d -= w
		}
		ops = append(ops, op, a, b, byte(d))
	}
	return ops
}

// TestEncodeOps checks that a golden input outside what FuzzReplay can
// express still makes a seed: a button not in fuzzButtons is left out, its
// gap going to the next event, and a gap of more than 65535 ms is split.
func TestEncodeOps(t *testing.T) {
	evs, err := parseInput("+east@0 +back@10 -back@20 -east@30 +east@70030 lefttrigger=1@70100")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		fzDown, 1, 0, 0,
		fzUp, 1, 0, 30,
		fzWait, 0xff, 0xff, 0, fzWait, 0x11, 0x71, 0, fzDown, 1, 0, 0, // 70000 ms = 65535 + 4465
		fzLT, 0x7f, 0xff, 70,
	}
	if got := encodeOps(evs); !slices.Equal(got, want) {
		t.Errorf("encodeOps = %v, want %v", got, want)
	}
}

// fzControl is the model of one control: what the engine should think.
type fzControl struct {
	down, canceled bool
	cycle          uint64
}

// failPlan makes the first Send call at or after at inject at most n
// events.
type failPlan struct {
	at clock.Time
	n  int
}

// fuzzer is one FuzzReplay run: a Slot on a Sim, the events not replayed
// yet and the model of the controls.
type fuzzer struct {
	t   *testing.T
	sim *simtest.Sim
	s   *slot.Slot

	now     clock.Time    // the trace time
	pending []input.Event // events not handled yet

	ctl        [control.Count]fzControl
	cycle      uint64 // the last cycle the model handed out
	paused     bool
	pressRaw   int16
	releaseRaw int16

	plans   []failPlan
	off     bool // failures disabled
	used    bool // a plan was used since the last check
	failed  bool // a plan was ever used
	unknown bool // an unknown event was sent since the last check
	desc    string
}

func newFuzzer(t *testing.T, prog *engine.Program) *fuzzer {
	fz := &fuzzer{
		t:          t,
		sim:        simtest.New(t, simtest.WithArbiterOptions(output.ArbiterOptions{RetryInterval: fuzzRetry})),
		pressRaw:   int16(math.Round(engine.TriggerPress * math.MaxInt16)),
		releaseRaw: int16(math.Round(engine.TriggerRelease * math.MaxInt16)),
	}
	fz.sim.Out.Inject = fz.inject
	s, err := slot.New(slot.Config{Gen: 1, Program: prog, Arbiter: fz.sim.Arb, Clock: fz.sim.Clock})
	if err != nil {
		t.Fatal(err)
	}
	fz.s = s
	return fz
}

// inject is the Recorder's hook: it applies the failure plans that are
// due.
func (fz *fuzzer) inject(events []output.KeyEvent) (int, error) {
	if fz.off || len(fz.plans) == 0 || fz.plans[0].at > fz.sim.Clock.Now() {
		return len(events), nil
	}
	p := fz.plans[0]
	fz.plans = fz.plans[1:]
	fz.used, fz.failed = true, true
	return p.n, simtest.ErrBlocked
}

func (fz *fuzzer) step(i int, op, a, b, dt byte) {
	fz.now = fz.now.Add(time.Duration(dt) * ms)
	fz.desc = fmt.Sprintf("op %d (%d %d %d) at %v", i, op, a, b, fz.now)
	switch op {
	case fzDown, fzUp:
		name := fuzzButtons[int(a)%len(fuzzButtons)]
		kind := input.ButtonUp
		if op == fzDown {
			kind = input.ButtonDown
		}
		fz.add(input.Event{Kind: kind, Control: name})
		c, _ := control.Parse(name)
		switch st := &fz.ctl[c]; {
		case st.down == (op == fzDown):
		case op == fzDown:
			fz.press(c)
		default:
			*st = fzControl{}
		}
	case fzLT, fzRT:
		v := int16(uint16(a)<<8 | uint16(b))
		c, axis := control.LT, control.LeftTrigger
		if op == fzRT {
			c, axis = control.RT, control.RightTrigger
		}
		fz.add(input.Event{Kind: input.AxisMotion, Control: axis.String(), Value: v})
		switch st := &fz.ctl[c]; {
		case !st.down && max(v, 0) >= fz.pressRaw:
			fz.press(c)
		case st.down && max(v, 0) <= fz.releaseRaw:
			*st = fzControl{}
		}
	case fzStick:
		axis := fuzzSticks[int(b)%len(fuzzSticks)]
		fz.add(input.Event{Kind: input.AxisMotion, Control: axis.String(), Value: int16(int8(a)) << 8})
	case fzReleaseAll:
		fz.catchUp(a)
		fz.released("ReleaseAll", fz.s.ReleaseAll())
	case fzPause:
		fz.catchUp(a)
		fz.paused = !fz.paused
		err := fz.s.SetPaused(fz.paused)
		switch {
		case fz.paused:
			fz.released("SetPaused(true)", err)
		case err != nil:
			fz.t.Fatalf("%s: SetPaused(false) = %v", fz.desc, err)
		}
	case fzCombo:
		fz.add(input.Event{Kind: input.ComboHeld})
		fz.paused = !fz.paused
		if fz.paused {
			fz.cancelDown()
		}
		if a%2 == 0 {
			fz.replay()
			if fz.paused {
				fz.released("ComboHeld", nil) // Replay keeps the error
			}
		}
	case fzRemove:
		fz.add(input.Event{Kind: input.DeviceRemoved})
		fz.ctl = [control.Count]fzControl{}
	case fzFail:
		for range a%3 + 1 {
			fz.plans = append(fz.plans, failPlan{at: fz.now, n: int(b % 4)})
		}
	case fzUnknown:
		fz.unknown = true
		switch a % 3 {
		case 0:
			fz.add(input.Event{Kind: input.ButtonDown, Control: "button30"})
		case 1:
			fz.add(input.Event{Kind: input.AxisMotion, Control: "axis9", Value: 1})
		default:
			fz.add(input.Event{Kind: 99, Control: "east"})
		}
	case fzWait:
		fz.replay()
		fz.now = fz.now.Add(time.Duration(uint16(a)<<8|uint16(b)) * ms)
	}
}

// catchUp handles the pending events before a ReleaseAll or a pause: by
// Replay if a is even or a ComboHeld is pending, which only Replay
// handles, else by hand, without settling.
func (fz *fuzzer) catchUp(a byte) {
	combo := slices.ContainsFunc(fz.pending, func(e input.Event) bool { return e.Kind == input.ComboHeld })
	if a%2 == 0 || combo {
		fz.replay()
	} else {
		fz.drive()
	}
}

// cancelDown cancels every control down in the model, as a release of
// every key does.
func (fz *fuzzer) cancelDown() {
	for c := range fz.ctl {
		if fz.ctl[c].down {
			fz.ctl[c].canceled = true
		}
	}
}

// released checks the slot after what, which lets go of every key and
// returned err: every control down is canceled in the model, and no owner
// holds a key; releases that failed may be pending.
func (fz *fuzzer) released(what string, err error) {
	if err != nil && !fz.used {
		fz.t.Fatalf("%s: %s = %v without a failure", fz.desc, what, err)
	}
	fz.cancelDown()
	for _, h := range fz.sim.Arb.Held() {
		if len(h.Owners) > 0 || !fz.failed {
			fz.t.Fatalf("%s: %s held after %s: %+v", fz.desc, keyName(h.Press), what, h)
		}
	}
}

// press starts a cycle of control c in the model: canceled while paused. The
// function key (MAP-27, start unless the program says otherwise) has no cycle:
// the engine does not count one, so the model does not either, and nothing it
// binds is a hold of fuzzHolds.
func (fz *fuzzer) press(c control.Control) {
	if c != control.Start {
		fz.cycle++
	}
	fz.ctl[c] = fzControl{down: true, cycle: fz.cycle, canceled: fz.paused}
}

func (fz *fuzzer) add(ev input.Event) {
	ev.Time = uint64(fz.now)
	fz.pending = append(fz.pending, ev)
}

// replay replays the pending events, then checks.
func (fz *fuzzer) replay() {
	fz.t.Helper()
	if err := fz.s.Replay(fz.sim.Clock, fz.pending); err != nil {
		fz.t.Fatalf("%s: Replay: %v", fz.desc, err)
	}
	fz.pending = nil
	fz.now = max(fz.now, fz.sim.Clock.Now())
	fz.check()
}

// drive handles the pending events like a slot loop: Advance at each
// deadline, Handle at each event's time, and no settling at the end.
func (fz *fuzzer) drive() {
	clk := fz.sim.Clock
	runTo := func(t clock.Time) {
		for {
			d, ok := fz.s.NextDeadline()
			if !ok || d > t {
				break
			}
			clk.Set(max(d, clk.Now()))
			fz.s.Advance()
			if d, ok := fz.s.NextDeadline(); ok && d <= clk.Now() {
				fz.t.Fatalf("%s: deadline %v is still due after Advance at %v", fz.desc, d, clk.Now())
			}
		}
		clk.Set(t)
	}
	for _, ev := range fz.pending {
		runTo(clock.Time(ev.Time))
		fz.s.Handle(ev)
	}
	fz.pending = nil
	runTo(fz.now)
}

// check compares the Arbiter with the model after a Replay, which leaves
// no taps and no pending releases, and takes the slot's errors.
func (fz *fuzzer) check() {
	fz.t.Helper()
	for _, err := range fz.s.Errors() {
		switch {
		case errors.Is(err, slot.ErrUnknownInput) && fz.unknown:
		case errors.Is(err, simtest.ErrBlocked) && fz.used:
		case strings.Contains(err.Error(), "more errors dropped") && fz.used:
		default:
			fz.t.Fatalf("%s: unexpected slot error %v", fz.desc, err)
		}
	}
	fz.used, fz.unknown = false, false
	if got := fz.s.Paused(); got != fz.paused {
		fz.t.Fatalf("%s: Paused() = %v, want %v", fz.desc, got, fz.paused)
	}
	if st := fz.sim.Arb.Stats(); st.DuplicateDowns != 0 || st.UnmatchedUps != 0 {
		fz.t.Fatalf("%s: %d duplicate Downs, %d unmatched Ups", fz.desc, st.DuplicateDowns, st.UnmatchedUps)
	}
	want := map[string]string{} // owner: key
	for c, st := range fz.ctl {
		if key, ok := fuzzHolds[control.Control(c)]; ok && st.down && !st.canceled {
			want[output.Owner{Gen: 1, Source: output.SourceDecl, Cycle: st.cycle}.String()] = key
		}
	}
	got := map[string]string{}
	var held []string
	for _, h := range fz.sim.Arb.Held() {
		key := keyName(h.Press)
		held = append(held, key)
		if h.Pending || len(h.Owners) == 0 {
			fz.t.Fatalf("%s: %s pending after Replay: %+v", fz.desc, key, h)
		}
		for _, o := range h.Owners {
			if _, dup := got[o.String()]; dup {
				fz.t.Fatalf("%s: %v holds two keys", fz.desc, o)
			}
			got[o.String()] = key
		}
	}
	if !maps.Equal(got, want) {
		fz.t.Fatalf("%s: holds %v, want %v", fz.desc, got, want)
	}
	var down []string
	for _, e := range fz.sim.Out.Down() {
		down = append(down, keyName(e))
	}
	for _, key := range down {
		if !slices.Contains(held, key) {
			fz.t.Fatalf("%s: %s is physically down, the Arbiter holds %v", fz.desc, key, held)
		}
	}
	// A press that failed leaves a key held but physically up.
	if !fz.failed && len(down) != len(held) {
		fz.t.Fatalf("%s: %v physically down, the Arbiter holds %v", fz.desc, down, held)
	}
}

// finish releases every control, with failures off: nothing may be left
// held. Then it runs the exit path, ReleaseAll.
func (fz *fuzzer) finish() {
	fz.off = true
	fz.desc = "finish"
	fz.replay()
	for c, st := range fz.ctl {
		switch c := control.Control(c); {
		case !st.down:
		case c.IsTrigger():
			fz.add(input.Event{Kind: input.AxisMotion, Control: c.Axis().String()})
		default:
			fz.add(input.Event{Kind: input.ButtonUp, Control: c.String()})
		}
	}
	fz.ctl = [control.Count]fzControl{}
	fz.replay()
	simtest.AssertNoneHeld(fz.t, fz.sim.Arb, fz.sim.Out)
	if err := fz.s.ReleaseAll(); err != nil {
		fz.t.Fatalf("final ReleaseAll: %v", err)
	}
	fz.replay()
}

func keyName(e output.KeyEvent) string { return simtest.Record{Event: e}.Edge().Name }

// Operations of FuzzResync, four bytes each like FuzzReplay's: the
// opcode, arguments a and b, and the milliseconds dt before it.
const (
	rsDown   = iota // button resyncButtons[a] goes down
	rsUp            // button resyncButtons[a] goes up
	rsLT            // lefttrigger moves to int16(a<<8|b)
	rsRT            // righttrigger moves to int16(a<<8|b)
	rsStick         // stick axis fuzzSticks[b] moves to int8(a)<<8
	rsRemove        // the device is removed
	rsGap           // slot B starts missing events; in a gap, the gap ends and B resyncs to A
	rsReset         // slot B resets, as when events were lost
	rsOps
)

// resyncButtons are the button names of rsDown and rsUp: bound,
// label-only and unbound controls, an alias and a trigger as a button.
var resyncButtons = [...]string{
	"south", "east", "west", "north", "lb", "rightshoulder", "dpad.up", "dpad.down", "start", "lt",
}

// resyncSeeds are FuzzResync's seed inputs, as op, a, b, dt quadruples.
var resyncSeeds = []struct {
	name string
	ops  []byte
}{
	{"hold across a gap", []byte{rsDown, 1, 0, 0, rsGap, 0, 0, 5, rsUp, 1, 0, 10, rsDown, 1, 0, 10, rsGap, 0, 0, 10, rsUp, 1, 0, 10}},
	{"press in a gap", []byte{rsGap, 0, 0, 0, rsDown, 1, 0, 5, rsDown, 0, 0, 5, rsGap, 0, 0, 5, rsUp, 1, 0, 10, rsUp, 0, 0, 10}},
	{"release in a gap", []byte{rsDown, 1, 0, 0, rsDown, 5, 0, 0, rsGap, 0, 0, 5, rsUp, 1, 0, 5, rsUp, 5, 0, 5, rsGap, 0, 0, 5}},
	{"shared key", []byte{rsDown, 5, 0, 0, rsGap, 0, 0, 5, rsDown, 6, 0, 5, rsUp, 5, 0, 5, rsGap, 0, 0, 5, rsUp, 6, 0, 5}},
	{"axes in a gap", []byte{rsLT, 0x7f, 0xff, 0, rsGap, 0, 0, 5, rsLT, 0, 0, 5, rsStick, 0x80, 1, 5, rsRT, 0x40, 0, 5, rsGap, 0, 0, 5, rsRT, 0, 0, 5}},
	{"reset", []byte{rsDown, 1, 0, 0, rsDown, 4, 0, 0, rsReset, 0, 0, 5, rsUp, 4, 0, 5, rsGap, 0, 0, 5, rsGap, 0, 0, 5, rsUp, 1, 0, 5}},
	{"remove in a gap", []byte{rsDown, 1, 0, 0, rsLT, 0x7f, 0, 0, rsGap, 0, 0, 5, rsRemove, 0, 0, 5, rsDown, 0, 0, 5, rsGap, 0, 0, 5}},
	{"remove outside a gap", []byte{rsDown, 1, 0, 0, rsRemove, 0, 0, 5, rsGap, 0, 0, 5, rsDown, 1, 0, 5, rsGap, 0, 0, 5}},
	{"no gap end", []byte{rsDown, 6, 0, 0, rsGap, 0, 0, 5, rsDown, 5, 0, 5, rsUp, 6, 0, 5}},
	{"alias and trigger button", []byte{rsDown, 4, 0, 0, rsGap, 0, 0, 5, rsDown, 9, 0, 0, rsUp, 4, 0, 5, rsGap, 0, 0, 5}},
	{"tap in a gap", []byte{rsGap, 0, 0, 0, rsDown, 0, 0, 5, rsGap, 0, 0, 5, rsUp, 0, 0, 30}},
	{"trigger hysteresis", []byte{rsGap, 0, 0, 0, rsLT, 0x7f, 0xff, 5, rsLT, 0x20, 0, 5, rsGap, 0, 0, 5, rsLT, 0, 0, 5}},
}

// FuzzResync drives two slots with the same random events: A handles
// every event, B misses those in gaps, resyncing to A's State at the end
// of each, and may be reset. A's State is always State.Apply over its
// events. After every Resync and at the end, B's State is A's, and a
// second Resync handles nothing. With a profile that binds only buttons,
// B's Arbiter then holds the same keys as A's. That holds only while the
// engine has no gestures, whose timing a Resync does not replay; nor does
// it replay a trigger crossing its press threshold and coming back into
// the hysteresis band, so with fuzzBindings, which bind the triggers,
// FuzzResync checks only the State. At the end both slots ReleaseAll:
// nothing may be held.
func FuzzResync(f *testing.F) {
	for _, seed := range resyncSeeds {
		f.Add(seed.ops)
	}
	buttonsOnly := slices.DeleteFunc(slices.Clone(fuzzBindings), func(b string) bool {
		return strings.HasPrefix(b, `"lt"`) || strings.HasPrefix(b, `"rt"`)
	})
	progs := []struct {
		prog  *engine.Program
		holds bool // compare the keys held
	}{{compile(f, buttonsOnly...), true}, {compile(f, fuzzBindings...), false}}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4*256 {
			data = data[:4*256]
		}
		for _, p := range progs {
			r := &resyncer{t: t, a: newResyncSlot(t, p.prog), b: newResyncSlot(t, p.prog), holds: p.holds}
			for i := 0; i < len(data); i += 4 {
				var arg [3]byte
				copy(arg[:], data[i+1:min(i+4, len(data))])
				r.step(i/4, data[i]%rsOps, arg[0], arg[1], arg[2])
			}
			r.finish()
		}
	})
}

// resyncSlot is a slot of FuzzResync on a Sim of its own.
type resyncSlot struct {
	sim     *simtest.Sim
	s       *slot.Slot
	pending []input.Event // not handled yet
}

func newResyncSlot(t *testing.T, prog *engine.Program) *resyncSlot {
	rs := &resyncSlot{sim: simtest.New(t)}
	s, err := slot.New(slot.Config{Gen: 1, Program: prog, Arbiter: rs.sim.Arb, Clock: rs.sim.Clock})
	if err != nil {
		t.Fatal(err)
	}
	rs.s = s
	return rs
}

// resyncer is one run of FuzzResync.
type resyncer struct {
	t     *testing.T
	a, b  *resyncSlot
	holds bool       // compare the keys held
	now   clock.Time // the trace time, not before either clock
	gap   bool       // B misses the events
	// applied is State.Apply over every event of A: A's State.
	applied slot.State
	desc    string
}

func (r *resyncer) step(i int, op, a, b, dt byte) {
	r.now = r.now.Add(time.Duration(dt) * ms)
	r.desc = fmt.Sprintf("op %d (%d %d %d) at %v", i, op, a, b, r.now)
	switch op {
	case rsDown, rsUp:
		r.add(button(resyncButtons[int(a)%len(resyncButtons)], op == rsDown))
	case rsLT, rsRT:
		axis := control.LeftTrigger
		if op == rsRT {
			axis = control.RightTrigger
		}
		r.add(input.Event{Kind: input.AxisMotion, Control: axis.String(), Value: int16(uint16(a)<<8 | uint16(b))})
	case rsStick:
		axis := fuzzSticks[int(b)%len(fuzzSticks)]
		r.add(input.Event{Kind: input.AxisMotion, Control: axis.String(), Value: int16(int8(a)) << 8})
	case rsRemove:
		r.add(input.Event{Kind: input.DeviceRemoved})
	case rsGap:
		if r.gap {
			r.sync()
		} else {
			r.gap = true
		}
	case rsReset:
		r.replay(r.b)
		r.b.sim.Clock.Set(r.now)
		r.b.s.Reset()
	}
}

// add adds ev at the trace time to A's events, and to B's outside a gap.
func (r *resyncer) add(ev input.Event) {
	ev.Time = uint64(r.now)
	r.a.pending = append(r.a.pending, ev)
	r.applied.Apply(ev)
	if !r.gap {
		r.b.pending = append(r.b.pending, ev)
	}
}

// replay replays the events of rs and runs its deadlines until none is
// left, which may move its clock past the trace time.
func (r *resyncer) replay(rs *resyncSlot) {
	r.t.Helper()
	if err := rs.s.Replay(rs.sim.Clock, rs.pending); err != nil {
		r.t.Fatalf("%s: Replay: %v", r.desc, err)
	}
	rs.pending = nil
	r.now = max(r.now, rs.sim.Clock.Now())
}

// sync ends the gap: both slots catch up to the trace time, B resyncs to
// A's State, and the two are compared.
func (r *resyncer) sync() {
	r.t.Helper()
	r.gap = false
	r.replay(r.a)
	r.replay(r.b)
	r.b.sim.Clock.Set(r.now)
	want := r.a.s.State()
	if want != r.applied {
		r.t.Fatalf("%s: A's State() = %+v\nState.Apply over its events = %+v", r.desc, want, r.applied)
	}
	diff := stateDiff(r.b.s.State(), want)
	if n := r.b.s.Resync(want); n != diff {
		r.t.Fatalf("%s: Resync = %d, the states differ in %d entries", r.desc, n, diff)
	}
	r.replay(r.b) // the ends of the taps Resync started
	r.a.sim.Clock.Set(r.now)
	if got := r.b.s.State(); got != want {
		r.t.Fatalf("%s: State() after Resync = %+v\nwant %+v", r.desc, got, want)
	}
	if n := r.b.s.Resync(want); n != 0 {
		r.t.Fatalf("%s: second Resync = %d, want 0", r.desc, n)
	}
	for _, rs := range []*resyncSlot{r.a, r.b} {
		for _, err := range rs.s.Errors() {
			if !errors.Is(err, slot.ErrUnknownInput) {
				r.t.Fatalf("%s: unexpected slot error %v", r.desc, err)
			}
		}
		if st := rs.sim.Arb.Stats(); st.DuplicateDowns != 0 || st.UnmatchedUps != 0 {
			r.t.Fatalf("%s: %d duplicate Downs, %d unmatched Ups", r.desc, st.DuplicateDowns, st.UnmatchedUps)
		}
	}
	if a, b := heldIDs(r.a.sim.Arb), heldIDs(r.b.sim.Arb); r.holds && !maps.Equal(a, b) {
		r.t.Fatalf("%s: B holds %v after Resync, A %v", r.desc, b, a)
	}
}

// finish ends the gap, if any, then lets go of everything.
func (r *resyncer) finish() {
	r.t.Helper()
	r.desc = "finish"
	r.sync()
	for _, rs := range []*resyncSlot{r.a, r.b} {
		if err := rs.s.ReleaseAll(); err != nil {
			r.t.Fatalf("ReleaseAll: %v", err)
		}
		r.replay(rs)
		simtest.AssertNoneHeld(r.t, rs.sim.Arb, rs.sim.Out)
	}
}

// stateDiff counts the entries in which x and y differ.
func stateDiff(x, y slot.State) int {
	n := 0
	for i := range x.Buttons {
		if x.Buttons[i] != y.Buttons[i] {
			n++
		}
	}
	for i := range x.Axes {
		if x.Axes[i] != y.Axes[i] {
			n++
		}
	}
	return n
}

// heldIDs returns the keys a holds.
func heldIDs(a *output.Arbiter) map[output.KeyID]bool {
	ids := map[output.KeyID]bool{}
	for _, h := range a.Held() {
		ids[h.Press.ID()] = true
	}
	return ids
}
