package engine_test

import (
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
)

// A single Joy-Con, held upright, has no back or start within reach: its
// function key is its trigger (ZL, ZR: LT, RT) or its shoulder button (L, R)
// (ADR-0012, ADR-0017). The shoulders are buttons like back and start. The
// triggers are the virtual buttons of the trigger axes, pressed and released
// at the profile's thresholds, and the engine compiles with any of the four.

// joyConProfile binds south, east, both triggers with their second stage and
// both shoulders and start in the base layer, and the same in the
// function set: all of them but the function key (and, for a trigger, its second
// stage and half axis, which are the same trigger) are ordinary controls, and
// the function key's own bindings are ignored.
const joyConProfile = `{"schemaVersion":1,"name":"joycon","layers":{
 "base":{
  "south":{"press":{"type":"keys","keys":"A"}},
  "east":{"press":{"type":"keys","keys":"B"}},
  "lt":{"press":{"type":"keys","keys":"Q"}},
  "lt.full":{"press":{"type":"keys","keys":"Z"}},
  "rt":{"press":{"type":"keys","keys":"W"}},
  "rt.full":{"press":{"type":"keys","keys":"X"}},
  "leftshoulder":{"press":{"type":"keys","keys":"E"}},
  "rightshoulder":{"press":{"type":"keys","keys":"R"}},
  "start":{"press":{"type":"keys","keys":"S"}}
 }},
 "functionSet":{
  "south":{"press":{"type":"keys","keys":"F1"}},
  "lt":{"press":{"type":"keys","keys":"F11"}},
  "lt.full":{"press":{"type":"keys","keys":"F18"}},
  "rt":{"press":{"type":"keys","keys":"F12"}},
  "rt.full":{"press":{"type":"keys","keys":"F19"}},
  "leftshoulder":{"press":{"type":"keys","keys":"F13"}},
  "rightshoulder":{"press":{"type":"keys","keys":"F14"}},
  "start":{"press":{"type":"keys","keys":"F9"}}
 }}`

var joyConKeys = []control.Control{control.LT, control.RT, control.LeftShoulder, control.RightShoulder}

// Trigger axis values: pressed (above the press threshold of 0.30, below the
// second stage of 0.90), the second stage, and rest.
const (
	trigPressed = 20000
	trigFull    = 32767
)

// stepControl presses or releases c at at ms: a button, or for a trigger its
// axis, moved to a value between the thresholds.
func stepControl(e *engine.Engine, c control.Control, down bool, at int) {
	if c.IsTrigger() {
		v := int16(0)
		if down {
			v = trigPressed
		}
		e.Step(engine.Input{T: timeAt(at), Kind: engine.InputAxis, Axis: c.Axis(), Value: v})
		return
	}
	e.Step(engine.Input{T: timeAt(at), Kind: engine.InputButton, Control: c, Down: down})
}

// runJoyCon is runDeadlines for a script in which "+lt@10" moves a trigger's
// axis, since the buttons of runDeadlines refuse a trigger.
func runJoyCon(t testing.TB, e *engine.Engine, r *recorder, script string) {
	t.Helper()
	for tok := range strings.FieldsSeq(script) {
		op, at, _ := strings.Cut(tok, "@")
		if len(op) > 1 && (op[0] == '+' || op[0] == '-') {
			if c, ok := control.Parse(op[1:]); ok && c.IsTrigger() {
				n, err := strconv.Atoi(at)
				if err != nil {
					t.Fatalf("bad step %q", tok)
				}
				runDeadlines(t, e, r, "wait@"+at)
				stepControl(e, c, op[0] == '+', n)
				continue
			}
		}
		runDeadlines(t, e, r, tok)
	}
}

// ignoredFor is what a profile with joyConProfile's bindings leaves out when
// fn is the function key: the bindings on fn, and for a trigger the ones on its
// second stage too, in the base layer and in the function set.
func ignoredFor(fn control.Control) []string {
	names := []string{fn.String()}
	switch fn {
	case control.LT:
		names = append(names, "lt.full")
	case control.RT:
		names = append(names, "rt.full")
	}
	var out []string
	for _, n := range names {
		out = append(out, "/layers/base/"+n, "/functionSet/"+n)
	}
	slices.Sort(out)
	return out
}

// A trigger that is the function key leaves out the bindings on its second
// stage and on its half axis as well (the same physical trigger), in the layers
// and in the function set, and the other trigger keeps them.
func TestFunctionKeyTriggerSiblingBindingsIgnored(t *testing.T) {
	const src = `{"schemaVersion":1,"name":"t","layers":{"base":{
  "lt":{"press":{"type":"keys","keys":"Q"}},
  "lt.full":{"press":{"type":"keys","keys":"Z"}},
  "lefttrigger+":{"press":{"type":"keys","keys":"F16"}},
  "rt":{"press":{"type":"keys","keys":"W"}},
  "rt.full":{"press":{"type":"keys","keys":"X"}},
  "righttrigger+":{"press":{"type":"keys","keys":"F17"}}}},
 "functionSet":{"lt.full":{"press":{"type":"keys","keys":"F18"}},"lefttrigger+":{"press":{"type":"keys","keys":"F19"}},"south":{"press":{"type":"keys","keys":"F1"}}}}`
	for _, tc := range []struct {
		fn   control.Control
		want []string
	}{
		{control.LT, []string{"/functionSet/lefttrigger+", "/functionSet/lt.full", "/layers/base/lefttrigger+", "/layers/base/lt", "/layers/base/lt.full"}},
		{control.RT, []string{"/layers/base/righttrigger+", "/layers/base/rt", "/layers/base/rt.full"}},
		{control.LeftShoulder, nil},
	} {
		prog := compileFn(t, src, tc.fn)
		if got := slices.Sorted(slices.Values(prog.IgnoredFunctionKeyBindings())); !slices.Equal(got, tc.want) {
			t.Errorf("%s: ignored %q; want %q", tc.fn, got, tc.want)
		}
		for _, bi := range prog.Bindings() {
			if prog.FunctionKey() == control.LT && slices.Contains([]control.Control{control.LT, control.LTFull, control.LeftTriggerPositive}, bi.Binding.Control) {
				t.Errorf("%s: Bindings() lists %s of %s", tc.fn, bi.Binding.ID, bi.Layer)
			}
		}
	}
}

// Each of the four compiles as the function key: its bindings are ignored and
// listed, the other three and start are ordinary controls, and a press of south
// under the key takes the set's binding. The key sends nothing, held alone or
// pressed again.
func TestFunctionKeyJoyCon(t *testing.T) {
	plainKeys := map[control.Control]string{control.LT: "Q", control.RT: "W", control.LeftShoulder: "E", control.RightShoulder: "R", control.Start: "S"}
	setKeys := map[control.Control]string{control.LT: "F11", control.RT: "F12", control.LeftShoulder: "F13", control.RightShoulder: "F14", control.Start: "F9"}
	for _, fn := range joyConKeys {
		t.Run(fn.String(), func(t *testing.T) {
			prog := compileFn(t, joyConProfile, fn)
			if got := prog.FunctionKey(); got != fn {
				t.Fatalf("FunctionKey() = %s; want %s", got, fn)
			}
			if got := slices.Sorted(slices.Values(prog.IgnoredFunctionKeyBindings())); !slices.Equal(got, ignoredFor(fn)) {
				t.Errorf("ignored bindings %q; want %q", got, ignoredFor(fn))
			}
			for _, bi := range prog.Bindings() {
				if c := bi.Binding.Control; c == fn || fn.IsTrigger() && (c == control.LTFull && fn == control.LT || c == control.RTFull && fn == control.RT) {
					t.Errorf("Bindings() lists %s of %s", bi.Binding.ID, bi.Layer)
				}
			}
			name := fn.String()
			r := &recorder{}
			e := engine.New(prog, r, engine.Options{})
			runJoyCon(t, e, r, "+"+name+"@0 -"+name+"@10 +"+name+"@20 -"+name+"@30 wait@500")
			if got := r.String(); got != "" {
				t.Errorf("the function key sent %q", got)
			}
			// The set is used while it is held, the layer when it is not.
			runJoyCon(t, e, r, "+"+name+"@100 +south@110 -south@120 -"+name+"@130 +south@140 -south@150")
			if got, want := r.String(), "@110 down decl/1 F1, @120 up decl/1 F1, @140 down decl/2 A, @150 up decl/2 A"; got != want {
				t.Errorf("sent %q; want %q", got, want)
			}
			// The others that can be one are ordinary controls, under the key too.
			for _, other := range []control.Control{control.LT, control.RT, control.LeftShoulder, control.RightShoulder, control.Start} {
				if other == fn {
					continue
				}
				plain, under := plainKeys[other], setKeys[other]
				r = &recorder{}
				e = engine.New(prog, r, engine.Options{})
				on := other.String()
				runJoyCon(t, e, r, "+"+on+"@0 -"+on+"@10 +"+name+"@20 +"+on+"@30 -"+on+"@40 -"+name+"@50")
				want := "@0 down decl/1 " + plain + ", @10 up decl/1 " + plain + ", @30 down decl/2 " + under + ", @40 up decl/2 " + under
				if got := r.String(); got != want {
					t.Errorf("%s: sent %q; want %q", on, got, want)
				}
			}
		})
	}
}

// A trigger that is the function key is pressed and released at the profile's
// thresholds, with their hysteresis (0.30 and 0.20 of the axis by default), and
// the second stage and the half axis of that trigger are never pressed, whether
// a binding names them or a script's button handler would take any control: the
// engine does not press them at all, so no decision, no script event and no
// output mentions them. The other trigger keeps them.
func TestFunctionKeyTriggerStages(t *testing.T) {
	for _, tc := range []struct {
		fn, other            control.Control
		axis, otherAxis      control.Axis
		full, halfAxis       control.Control
		otherFull, otherHalf control.Control
	}{
		{control.LT, control.RT, control.LeftTrigger, control.RightTrigger, control.LTFull, control.LeftTriggerPositive, control.RTFull, control.RightTriggerPositive},
		{control.RT, control.LT, control.RightTrigger, control.LeftTrigger, control.RTFull, control.RightTriggerPositive, control.LTFull, control.LeftTriggerPositive},
	} {
		t.Run(tc.fn.String(), func(t *testing.T) {
			prog := compileFn(t, joyConProfile, tc.fn)
			var seen []control.Control // what the script's button handler is told, which takes every control
			var decided []engine.Decision
			r := &recorder{}
			e := engine.New(prog, r, engine.Options{
				Trace:  func(d engine.Decision) { decided = append(decided, d) },
				Button: func(ev engine.ButtonEvent) bool { seen = append(seen, ev.Control); return false },
			})
			axis := func(a control.Axis, v int16, at int) {
				e.Step(engine.Input{T: timeAt(at), Kind: engine.InputAxis, Axis: a, Value: v})
			}
			// Below the press threshold, then past it: the key is held.
			axis(tc.axis, 5000, 0)
			if e.ActiveFunctionKey() != control.Invalid {
				t.Fatal("function key held below the press threshold")
			}
			axis(tc.axis, 12000, 10)
			if e.ActiveFunctionKey() != tc.fn {
				t.Fatalf("function key held %s past the press threshold; want %s", e.ActiveFunctionKey(), tc.fn)
			}
			// Hysteresis: between the release and the press threshold it stays.
			axis(tc.axis, 8000, 20)
			if e.ActiveFunctionKey() != tc.fn {
				t.Error("function key let go above the release threshold")
			}
			// All the way down: the second stage would press here.
			axis(tc.axis, trigFull, 30)
			axis(tc.axis, trigPressed, 40)
			axis(tc.axis, trigFull, 50)
			if e.ActiveFunctionKey() != tc.fn {
				t.Error("function key let go while the trigger is pulled further")
			}
			axis(tc.axis, 0, 60)
			if e.ActiveFunctionKey() != control.Invalid {
				t.Errorf("function key still held at rest: %s", e.ActiveFunctionKey())
			}
			runDeadlines(t, e, r, "wait@600")
			if got := r.String(); got != "" {
				t.Errorf("sent %q; the key and its stages send nothing", got)
			}
			for _, c := range seen {
				if c == tc.fn || c == tc.full || c == tc.halfAxis {
					t.Errorf("the script was told of %s, which is the function key or its second stage or half axis", c)
				}
			}
			for _, d := range decided {
				if d.Control == tc.full || d.Control == tc.halfAxis {
					t.Errorf("decision %v on %s, which never presses", d, d.Control)
				}
				if d.Control == tc.fn && d.Reason != engine.ReasonFunction {
					t.Errorf("decision %v on the function key is not ReasonFunction", d)
				}
			}
			// The other trigger is an ordinary one, second stage and all.
			seen, decided = nil, nil
			axis(tc.otherAxis, trigFull, 700)
			axis(tc.otherAxis, 0, 710)
			if !slices.Contains(seen, tc.otherFull) || !slices.Contains(seen, tc.otherHalf) || !slices.Contains(seen, tc.other) {
				t.Errorf("the other trigger told the script %v; want %s, %s and %s", seen, tc.other, tc.otherFull, tc.otherHalf)
			}
			runDeadlines(t, e, r, "wait@1400")
			if e.ActiveFunctionKey() != control.Invalid {
				t.Error("the other trigger held the function key")
			}
		})
	}
}

// The edges of a function key that is a trigger or a shoulder never reach
// gesture recognition: they are decisions with ReasonFunction, start no cycle,
// and do not interrupt a pending gesture of another control.
func TestFunctionKeyJoyConEdges(t *testing.T) {
	for _, fn := range joyConKeys {
		t.Run(fn.String(), func(t *testing.T) {
			src := `{"schemaVersion":1,"name":"p","layers":{"base":{
  "south":{"tap":{"type":"keys","keys":"A","mode":"tap"},"hold":{"type":"keys","keys":"Shift"}},
  "` + fn.String() + `":{"press":{"type":"keys","keys":"E"}}}}}`
			prog := compileFn(t, src, fn)
			var decisions []engine.Decision
			r := &recorder{}
			e := engine.New(prog, r, engine.Options{Trace: func(d engine.Decision) { decisions = append(decisions, d) }})
			// south is pending (a tap or a hold); the key's edges go by.
			name := fn.String()
			runJoyCon(t, e, r, "+south@0 +"+name+"@50 -"+name+"@80 -south@100 wait@600")
			var got []engine.Decision
			for _, d := range decisions {
				if d.Control == fn {
					got = append(got, d)
				}
			}
			if len(got) != 2 || got[0].Reason != engine.ReasonFunction || got[0].Edge != engine.EdgeDown || got[1].Reason != engine.ReasonFunction || got[1].Edge != engine.EdgeUp {
				t.Fatalf("decisions of the key: %v", got)
			}
			for _, d := range got {
				if d.Cycle != 0 || d.Action != nil || d.Owner != (engine.OwnerID{}) {
					t.Errorf("the function key's decision %v has a cycle, an action or an owner", d)
				}
			}
			// The tap of south was decided by its own release, not by the key's press.
			if got, want := r.String(), "@100 tap decl/1 A 20ms"; got != want {
				t.Errorf("sent %q; want %q", got, want)
			}
		})
	}
}

// What a trigger or a shoulder that is the function key does when the engine
// exits: the same as for start (see TestFunctionKeyExitPaths). Nothing stays
// held after CancelAll, Pause, a Reload to the same key or another, and Reset
// (the key and what was pressed under it and beside it); a Reload that changes
// the key forgets that the old one is held; the key's release afterwards sends
// nothing.
func TestFunctionKeyJoyConExitPaths(t *testing.T) {
	for _, fn := range []control.Control{control.LT, control.RightShoulder} {
		prog := compileFn(t, joyConProfile, fn)
		otherKey := control.RT
		if fn == control.RT {
			otherKey = control.LT
		}
		if fn == control.RightShoulder {
			otherKey = control.LeftShoulder
		}
		other, err := prog.ForFunctionKey(otherKey)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name   string
			exit   func(e *engine.Engine, l *ledger, now int)
			active control.Control // after the exit, with the key still down
		}{
			{"CancelAll", func(e *engine.Engine, l *ledger, now int) { e.CancelAll(timeAt(now)); l.releaseAll() }, fn},
			{"Pause and Resume", func(e *engine.Engine, l *ledger, now int) {
				e.Pause(timeAt(now))
				l.releaseAll()
				e.Resume()
			}, fn},
			{"Reload", func(e *engine.Engine, l *ledger, now int) { e.Reload(timeAt(now), prog); l.releaseAll() }, fn},
			{"Reload with another function key", func(e *engine.Engine, l *ledger, now int) { e.Reload(timeAt(now), other); l.releaseAll() }, control.Invalid},
			{"Reset", func(e *engine.Engine, l *ledger, now int) { e.Reset(timeAt(now)); l.releaseAll() }, control.Invalid},
		} {
			t.Run(fn.String()+"/"+tc.name, func(t *testing.T) {
				l := &ledger{t: t, held: map[ledgerKey]int{}, ctl: map[uint64]control.Control{}}
				e := engine.New(prog, l, engine.Options{})
				l.e = e
				step := func(c control.Control, down bool, at int) {
					l.down[c] = down
					stepControl(e, c, down, at)
				}
				step(fn, true, 0)
				if e.ActiveFunctionKey() != fn {
					t.Fatalf("function key held %v; want %v", e.ActiveFunctionKey(), fn)
				}
				step(control.South, true, 10) // held under the function key: F1
				step(control.East, true, 11)  // held, with its normal binding
				if len(l.held) != 2 {
					t.Fatalf("held %v before the exit; want F1 and B", l.held)
				}
				tc.exit(e, l, 15)
				if len(l.held) != 0 {
					t.Fatalf("%v held after the exit", l.held)
				}
				if got := e.ActiveFunctionKey(); got != tc.active {
					t.Errorf("function key held %v after the exit; want %v", got, tc.active)
				}
				step(control.South, false, 20)
				step(control.East, false, 21)
				step(control.South, true, 30)
				step(control.South, false, 35)
				step(fn, false, 40)
				if len(l.held) != 0 {
					t.Fatalf("%v held after the releases", l.held)
				}
				if got := e.ActiveFunctionKey(); got != control.Invalid {
					t.Errorf("function key held %v after the release of %s", got, fn)
				}
				e.CancelAll(timeAt(50))
				l.releaseAll()
			})
		}
	}
}

// A function key that is a trigger, held while a second stage of the same
// trigger would press and while another key is held under it, leaves nothing
// held when the controller goes away (Reset), and the trigger's own return to
// rest afterwards presses nothing.
func TestFunctionKeyTriggerResetWhileFull(t *testing.T) {
	prog := compileFn(t, joyConProfile, control.LT)
	l := &ledger{t: t, held: map[ledgerKey]int{}, ctl: map[uint64]control.Control{}}
	e := engine.New(prog, l, engine.Options{})
	l.e = e
	e.Step(engine.Input{T: timeAt(0), Kind: engine.InputAxis, Axis: control.LeftTrigger, Value: trigFull})
	e.Step(engine.Input{T: timeAt(10), Kind: engine.InputButton, Control: control.South, Down: true})
	if len(l.held) != 1 || e.ActiveFunctionKey() != control.LT {
		t.Fatalf("held %v, function key %v; want F1 under lt", l.held, e.ActiveFunctionKey())
	}
	e.Reset(timeAt(20))
	l.releaseAll()
	e.Step(engine.Input{T: timeAt(30), Kind: engine.InputAxis, Axis: control.LeftTrigger, Value: 0})
	e.Step(engine.Input{T: timeAt(31), Kind: engine.InputButton, Control: control.South, Down: false})
	if len(l.held) != 0 || e.ActiveFunctionKey() != control.Invalid {
		t.Errorf("held %v, function key %v after the reset", l.held, e.ActiveFunctionKey())
	}
	e.CancelAll(timeAt(40))
}

// ForFunctionKey gives the Program of the same profile for another function
// key: the same Program for the key it has, one Program per key that is made
// once and then shared, with the bindings of its own key ignored, whatever
// goroutine asks; a control that cannot be a function key means the default.
func TestProgramForFunctionKey(t *testing.T) {
	start := compileFn(t, joyConProfile, control.Invalid)
	if got, err := start.ForFunctionKey(control.Start); err != nil || got != start {
		t.Fatalf("ForFunctionKey(start) = %p, %v; want the Program itself", got, err)
	}
	for _, c := range []control.Control{control.Guide, control.South, control.Paddle3} {
		if got, err := start.ForFunctionKey(c); err != nil || got != start {
			t.Errorf("ForFunctionKey(%s) = %p, %v; want the default, the Program itself", c, got, err)
		}
	}
	var wg sync.WaitGroup
	got := make([]*engine.Program, 16)
	for i := range got {
		wg.Go(func() {
			var err error
			if got[i], err = start.ForFunctionKey(control.RT); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	for _, p := range got {
		if p != got[0] {
			t.Fatal("ForFunctionKey made more than one Program for one key")
		}
	}
	rt := got[0]
	if rt.FunctionKey() != control.RT || rt.Profile() != start.Profile() {
		t.Errorf("variant has key %s and profile %p; want rt and %p", rt.FunctionKey(), rt.Profile(), start.Profile())
	}
	if got := slices.Sorted(slices.Values(rt.IgnoredFunctionKeyBindings())); !slices.Equal(got, ignoredFor(control.RT)) {
		t.Errorf("ignored %q; want %q", got, ignoredFor(control.RT))
	}
	// From a variant, the way back and the siblings are the same Programs.
	if back, err := rt.ForFunctionKey(control.Start); err != nil || back != start {
		t.Errorf("ForFunctionKey(start) of a variant = %p, %v; want the first Program", back, err)
	}
	if again, err := rt.ForFunctionKey(control.RT); err != nil || again != rt {
		t.Errorf("ForFunctionKey of its own key = %p, %v", again, err)
	}
	lb, err := rt.ForFunctionKey(control.LeftShoulder)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := start.ForFunctionKey(control.LeftShoulder); again != lb {
		t.Error("a sibling variant is not shared")
	}
	// The variants differ in the key only: each leaves out its own bindings
	// (rt also leaves out its second stage, so it lists two fewer).
	if len(start.Bindings()) == 0 || len(lb.Bindings()) != len(start.Bindings()) || len(rt.Bindings()) != len(lb.Bindings())-2 {
		t.Errorf("bindings: %d for start, %d for rt, %d for leftshoulder", len(start.Bindings()), len(rt.Bindings()), len(lb.Bindings()))
	}
	if start.Timing() != rt.Timing() || start.Analog() != rt.Analog() {
		t.Error("variants differ in timing or analog settings")
	}
}

// View tells the function key of the Program the engine runs, held or not.
func TestViewProgramFunctionKey(t *testing.T) {
	start := compileFn(t, joyConProfile, control.Invalid)
	r := &recorder{}
	e := engine.New(start, r, engine.Options{})
	if got := e.View().ProgramFunctionKey; got != control.Start {
		t.Errorf("ProgramFunctionKey = %s; want start", got)
	}
	lt, err := start.ForFunctionKey(control.LT)
	if err != nil {
		t.Fatal(err)
	}
	e.Reload(timeAt(1), lt)
	if got := e.View().ProgramFunctionKey; got != control.LT {
		t.Errorf("ProgramFunctionKey = %s after Reload; want lt", got)
	}
	if got := e.View().FunctionKey; got != control.Invalid {
		t.Errorf("FunctionKey = %s with nothing held", got)
	}
	stepControl(e, control.LT, true, 10)
	if got := e.View().FunctionKey; got != control.LT {
		t.Errorf("FunctionKey = %s with lt held; want lt", got)
	}
}
