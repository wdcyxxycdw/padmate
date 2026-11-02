package engine_test

import (
	"slices"
	"sync"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
)

// A single Joy-Con held sideways has no back or start: its function key is one
// of its paddles (ADR-0012, ADR-0017), so the engine compiles with any of
// paddle1 to paddle4, and the paddle behaves as back and start do.

// paddleProfile binds south, east, every paddle and start in the base layer,
// and south, every paddle and start in the function set: all of them but the
// function key are ordinary controls, and the function key's own two bindings
// are ignored.
const paddleProfile = `{"schemaVersion":1,"name":"paddles","layers":{
 "base":{
  "south":{"press":{"type":"keys","keys":"A"}},
  "east":{"press":{"type":"keys","keys":"B"}},
  "paddle1":{"press":{"type":"keys","keys":"Q"}},
  "paddle2":{"press":{"type":"keys","keys":"W"}},
  "paddle3":{"press":{"type":"keys","keys":"E"}},
  "paddle4":{"press":{"type":"keys","keys":"R"}},
  "start":{"press":{"type":"keys","keys":"S"}}
 }},
 "functionSet":{
  "south":{"press":{"type":"keys","keys":"F1"}},
  "paddle1":{"press":{"type":"keys","keys":"F11"}},
  "paddle2":{"press":{"type":"keys","keys":"F12"}},
  "paddle3":{"press":{"type":"keys","keys":"F13"}},
  "paddle4":{"press":{"type":"keys","keys":"F14"}},
  "start":{"press":{"type":"keys","keys":"F9"}}
 }}`

var paddles = []control.Control{control.Paddle1, control.Paddle2, control.Paddle3, control.Paddle4}

// Each paddle compiles as the function key: its own two bindings are ignored
// and listed, the other paddles and start are ordinary controls, and a press of
// south under the paddle takes the set's binding. The paddle sends nothing,
// held alone or pressed again.
func TestFunctionKeyPaddles(t *testing.T) {
	plainKeys := map[control.Control]string{control.Paddle1: "Q", control.Paddle2: "W", control.Paddle3: "E", control.Paddle4: "R", control.Start: "S"}
	setKeys := map[control.Control]string{control.Paddle1: "F11", control.Paddle2: "F12", control.Paddle3: "F13", control.Paddle4: "F14", control.Start: "F9"}
	for _, fn := range paddles {
		t.Run(fn.String(), func(t *testing.T) {
			prog := compileFn(t, paddleProfile, fn)
			if got := prog.FunctionKey(); got != fn {
				t.Fatalf("FunctionKey() = %s; want %s", got, fn)
			}
			wantIgnored := []string{"/layers/base/" + fn.String(), "/functionSet/" + fn.String()}
			if got := prog.IgnoredFunctionKeyBindings(); !slices.Equal(got, wantIgnored) {
				t.Errorf("ignored bindings %q; want %q", got, wantIgnored)
			}
			for _, bi := range prog.Bindings() {
				if bi.Binding.Control == fn {
					t.Errorf("Bindings() lists %s of %s", bi.Binding.ID, bi.Layer)
				}
			}
			name := fn.String()
			r := &recorder{}
			e := engine.New(prog, r, engine.Options{})
			runDeadlines(t, e, r, "+"+name+"@0 -"+name+"@10 +"+name+"@20 -"+name+"@30 wait@500")
			if got := r.String(); got != "" {
				t.Errorf("the function key sent %q", got)
			}
			// The set is used while it is held, the layer when it is not.
			runDeadlines(t, e, r, "+"+name+"@100 +south@110 -south@120 -"+name+"@130 +south@140 -south@150")
			if got, want := r.String(), "@110 down decl/1 F1, @120 up decl/1 F1, @140 down decl/2 A, @150 up decl/2 A"; got != want {
				t.Errorf("sent %q; want %q", got, want)
			}
			// The others that can be one are ordinary controls, under the key too.
			others := append(slices.DeleteFunc(slices.Clone(paddles), func(c control.Control) bool { return c == fn }), control.Start)
			for _, other := range others {
				plain, under := plainKeys[other], setKeys[other]
				r = &recorder{}
				e = engine.New(prog, r, engine.Options{})
				on := other.String()
				runDeadlines(t, e, r, "+"+on+"@0 -"+on+"@10 +"+name+"@20 +"+on+"@30 -"+on+"@40 -"+name+"@50")
				want := "@0 down decl/1 " + plain + ", @10 up decl/1 " + plain + ", @30 down decl/2 " + under + ", @40 up decl/2 " + under
				if got := r.String(); got != want {
					t.Errorf("%s: sent %q; want %q", on, got, want)
				}
			}
		})
	}
}

// The edges of a paddle that is the function key never reach gesture
// recognition: they are decisions with ReasonFunction, start no cycle, and do
// not interrupt a pending gesture of another control.
func TestFunctionKeyPaddleEdges(t *testing.T) {
	const src = `{"schemaVersion":1,"name":"p","layers":{"base":{
  "south":{"tap":{"type":"keys","keys":"A","mode":"tap"},"hold":{"type":"keys","keys":"Shift"}},
  "paddle3":{"press":{"type":"keys","keys":"E"}}}}}`
	prog := compileFn(t, src, control.Paddle3)
	var decisions []engine.Decision
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{Trace: func(d engine.Decision) { decisions = append(decisions, d) }})
	// south is pending (a tap or a hold); the paddle's edges go by.
	runDeadlines(t, e, r, "+south@0 +paddle3@50 -paddle3@80 -south@100 wait@600")
	var fn []engine.Decision
	for _, d := range decisions {
		if d.Control == control.Paddle3 {
			fn = append(fn, d)
		}
	}
	if len(fn) != 2 || fn[0].Reason != engine.ReasonFunction || fn[0].Edge != engine.EdgeDown || fn[1].Reason != engine.ReasonFunction || fn[1].Edge != engine.EdgeUp {
		t.Fatalf("decisions of the paddle: %v", fn)
	}
	for _, d := range fn {
		if d.Cycle != 0 || d.Action != nil || d.Owner != (engine.OwnerID{}) {
			t.Errorf("the function key's decision %v has a cycle, an action or an owner", d)
		}
	}
	// The tap of south was decided by its own release, not by the paddle's press.
	if got, want := r.String(), "@100 tap decl/1 A 20ms"; got != want {
		t.Errorf("sent %q; want %q", got, want)
	}
}

// What a paddle does when the engine exits: the same as for start (see
// TestFunctionKeyExitPaths). Nothing stays held after CancelAll, Pause, a
// Reload to the same key or another, and Reset; a Reload that changes the key
// forgets that the old one is held.
func TestFunctionKeyPaddleExitPaths(t *testing.T) {
	prog := compileFn(t, paddleProfile, control.Paddle3)
	other, err := prog.ForFunctionKey(control.Paddle1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		exit   func(e *engine.Engine, l *ledger, now int)
		active control.Control // after the exit, with paddle3 still down
	}{
		{"CancelAll", func(e *engine.Engine, l *ledger, now int) { e.CancelAll(timeAt(now)); l.releaseAll() }, control.Paddle3},
		{"Pause and Resume", func(e *engine.Engine, l *ledger, now int) {
			e.Pause(timeAt(now))
			l.releaseAll()
			e.Resume()
		}, control.Paddle3},
		{"Reload", func(e *engine.Engine, l *ledger, now int) { e.Reload(timeAt(now), prog); l.releaseAll() }, control.Paddle3},
		{"Reload with another function key", func(e *engine.Engine, l *ledger, now int) { e.Reload(timeAt(now), other); l.releaseAll() }, control.Invalid},
		{"Reset", func(e *engine.Engine, l *ledger, now int) { e.Reset(timeAt(now)); l.releaseAll() }, control.Invalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := &ledger{t: t, held: map[ledgerKey]int{}, ctl: map[uint64]control.Control{}}
			e := engine.New(prog, l, engine.Options{})
			l.e = e
			step := func(c control.Control, down bool, at int) {
				l.down[c] = down
				e.Step(engine.Input{T: timeAt(at), Kind: engine.InputButton, Control: c, Down: down})
			}
			step(control.Paddle3, true, 0)
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
			step(control.Paddle3, false, 40)
			if len(l.held) != 0 {
				t.Fatalf("%v held after the releases", l.held)
			}
			if got := e.ActiveFunctionKey(); got != control.Invalid {
				t.Errorf("function key held %v after the release of paddle3", got)
			}
			e.CancelAll(timeAt(50))
			l.releaseAll()
		})
	}
}

// ForFunctionKey gives the Program of the same profile for another function
// key: the same Program for the key it has, one Program per key that is made
// once and then shared, with the bindings of its own key ignored, whatever
// goroutine asks; a control that cannot be a function key means the default.
func TestProgramForFunctionKey(t *testing.T) {
	start := compileFn(t, paddleProfile, control.Invalid)
	if got, err := start.ForFunctionKey(control.Start); err != nil || got != start {
		t.Fatalf("ForFunctionKey(start) = %p, %v; want the Program itself", got, err)
	}
	if got, err := start.ForFunctionKey(control.Guide); err != nil || got != start {
		t.Errorf("ForFunctionKey(guide) = %p, %v; want the default, the Program itself", got, err)
	}
	if got, err := start.ForFunctionKey(control.South); err != nil || got != start {
		t.Errorf("ForFunctionKey(south) = %p, %v; want the default, the Program itself", got, err)
	}
	var wg sync.WaitGroup
	got := make([]*engine.Program, 16)
	for i := range got {
		wg.Go(func() {
			var err error
			if got[i], err = start.ForFunctionKey(control.Paddle3); err != nil {
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
	p3 := got[0]
	if p3.FunctionKey() != control.Paddle3 || p3.Profile() != start.Profile() {
		t.Errorf("variant has key %s and profile %p; want paddle3 and %p", p3.FunctionKey(), p3.Profile(), start.Profile())
	}
	if want := []string{"/layers/base/paddle3", "/functionSet/paddle3"}; !slices.Equal(p3.IgnoredFunctionKeyBindings(), want) {
		t.Errorf("ignored %q; want %q", p3.IgnoredFunctionKeyBindings(), want)
	}
	// From a variant, the way back and the siblings are the same Programs.
	if back, err := p3.ForFunctionKey(control.Start); err != nil || back != start {
		t.Errorf("ForFunctionKey(start) of a variant = %p, %v; want the first Program", back, err)
	}
	if again, err := p3.ForFunctionKey(control.Paddle3); err != nil || again != p3 {
		t.Errorf("ForFunctionKey of its own key = %p, %v", again, err)
	}
	p1, err := p3.ForFunctionKey(control.Paddle1)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := start.ForFunctionKey(control.Paddle1); again != p1 {
		t.Error("a sibling variant is not shared")
	}
	// The variants differ in the key only: each leaves out its own bindings.
	if len(start.Bindings()) != len(p3.Bindings()) || len(p3.Bindings()) != len(p1.Bindings()) || len(p3.Bindings()) == 0 {
		t.Errorf("bindings: %d for start, %d for paddle3, %d for paddle1", len(start.Bindings()), len(p3.Bindings()), len(p1.Bindings()))
	}
	if start.Timing() != p3.Timing() || start.Analog() != p3.Analog() {
		t.Error("variants differ in timing or analog settings")
	}
}

// View tells the function key of the Program the engine runs, held or not.
func TestViewProgramFunctionKey(t *testing.T) {
	start := compileFn(t, paddleProfile, control.Invalid)
	r := &recorder{}
	e := engine.New(start, r, engine.Options{})
	if got := e.View().ProgramFunctionKey; got != control.Start {
		t.Errorf("ProgramFunctionKey = %s; want start", got)
	}
	p3, err := start.ForFunctionKey(control.Paddle3)
	if err != nil {
		t.Fatal(err)
	}
	e.Reload(timeAt(1), p3)
	if got := e.View().ProgramFunctionKey; got != control.Paddle3 {
		t.Errorf("ProgramFunctionKey = %s after Reload; want paddle3", got)
	}
	if got := e.View().FunctionKey; got != control.Invalid {
		t.Errorf("FunctionKey = %s with nothing held", got)
	}
}
