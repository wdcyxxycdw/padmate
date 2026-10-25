package engine_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Tests of the function keys (MAP-27, ADR-0069): back and start send nothing,
// stay out of gesture recognition, and while one is held the other controls
// look their binding up in that key's set first.

// fnProfile has a base layer, a layer alt that rebinds south, and a set for
// each function key. Back's set rebinds south and west (west is a press there,
// while the base binding is a tap and a hold), blocks dpad.up, owns dpad.left
// with a label only, and binds lt; east is not in it. Start's set rebinds south
// and north. rightshoulder is a tap with a hold of Shift, a gesture that
// another control's press commits at once (holdPreferred).
const fnProfile = `{"schemaVersion":1,"name":"fn","layers":{
 "base":{
  "south":{"press":{"type":"keys","keys":"A"}},
  "east":{"press":{"type":"keys","keys":"B"}},
  "west":{"tap":{"type":"keys","keys":"C","mode":"tap"},"hold":{"type":"keys","keys":"D"}},
  "north":{"press":{"type":"keys","keys":"N"}},
  "rightshoulder":{"tap":{"type":"keys","keys":"F6","mode":"tap"},"hold":{"type":"keys","keys":"Shift"}},
  "leftshoulder":{"press":{"type":"layer","layer":"alt","op":"hold"}},
  "dpad.up":{"press":{"type":"keys","keys":"U"}},
  "dpad.left":{"press":{"type":"keys","keys":"L"}},
  "lt":{"press":{"type":"keys","keys":"Alt"}}
 },
 "alt":{"south":{"press":{"type":"keys","keys":"X"}}}},
 "functionKeys":{
  "back":{
   "south":{"label":"Redo","press":{"type":"keys","keys":"F1"}},
   "west":{"press":{"type":"keys","keys":"F2"}},
   "dpad.up":{"block":true},
   "dpad.left":{"label":"only a label"},
   "lt":{"press":{"type":"keys","keys":"F3"}}
  },
  "start":{
   "south":{"press":{"type":"keys","keys":"F4"}},
   "north":{"press":{"type":"keys","keys":"F5"}}
  }}}`

func TestFunctionKeySets(t *testing.T) {
	for _, tc := range []struct{ name, steps, want string }{
		{"no function key", "+south@10 -south@20", "@10 down decl/1 A, @20 up decl/1 A"},
		{"back's set is used", "+back@0 +south@10 -south@20 -back@30", "@10 down decl/1 F1, @20 up decl/1 F1"},
		{"start's set is used", "+start@0 +south@10 -south@20 -start@30", "@10 down decl/1 F4, @20 up decl/1 F4"},
		{"a control the set lacks keeps its binding", "+back@0 +east@10 -east@20 -back@30", "@10 down decl/1 B, @20 up decl/1 B"},
		{"the set is dropped with the key", "+back@0 -back@10 +south@20 -south@30", "@20 down decl/1 A, @30 up decl/1 A"},
		{"a block binding consumes the control", "+back@0 +dpad.up@10 -dpad.up@20 -back@30", ""},
		{"block only while it is held", "+back@0 -back@10 +dpad.up@20 -dpad.up@30", "@20 down decl/1 U, @30 up decl/1 U"},
		{"a binding with only a label owns the control, as in a layer", "+back@0 +dpad.left@10 -dpad.left@20 -back@30", ""},
		{"a press where the base has a tap and a hold waits for nothing", "+back@0 +west@10 -west@20 -back@30", "@10 down decl/1 F2, @20 up decl/1 F2"},
		{"the base binding is untouched", "+west@10 -west@20 wait@300", "@20 tap decl/1 C 20ms"},
		{"above an overlay layer", "+leftshoulder@0 +south@5 -south@6 +back@10 +south@15 -south@20 -back@25 -leftshoulder@30", "@5 down decl/2 X, @6 up decl/2 X, @15 down decl/3 F1, @20 up decl/3 F1"},
		{"a function key does not use a cycle", "+back@0 -back@10 +start@20 -start@30 +south@40 -south@50", "@40 down decl/1 A, @50 up decl/1 A"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileLayers(t, fnProfile), r, engine.Options{})
			runM2(t, e, r, tc.steps)
			if got := r.String(); got != tc.want {
				t.Errorf("sent %q; want %q", got, tc.want)
			}
		})
	}
}

// The trigger is a virtual control, the one the set binds goes through the
// same lookup.
func TestFunctionKeySetOfATrigger(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileLayers(t, fnProfile), r, engine.Options{})
	run(t, e, r, "+back@0 lefttrigger=32767@10 lefttrigger=0@20 -back@30 lefttrigger=32767@40 lefttrigger=0@50")
	if got, want := r.String(), "@10 down decl/1 F3, @20 up decl/1 F3, @40 down decl/2 Alt, @50 up decl/2 Alt"; got != want {
		t.Errorf("sent %q; want %q", got, want)
	}
}

// A stick direction, a ring or a knob step that only a set binds is recognized
// while its function key is held, as a control that only an overlay binds is
// while the overlay is.
func TestFunctionKeySetOfVirtualControls(t *testing.T) {
	const src = `{"schemaVersion":1,"name":"fn","analog":{"ls":{"mode":"fourWay"},"rs":{"mode":"knob"}},"layers":{"base":{}},
 "functionKeys":{"back":{"ls.up":{"press":{"type":"keys","keys":"U"}},"rs.cw":{"press":{"type":"keys","keys":"R","mode":"tap"}}}}}`
	for _, tc := range []struct{ name, steps, want string }{
		{"direction", "+back@0 lefty=-30000@10 lefty=0@20 -back@30", "@10 down decl/1 U"},
		{"direction without the key", "lefty=-30000@10 lefty=0@20", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileLayers(t, src), r, engine.Options{})
			run(t, e, r, tc.steps)
			got := r.String()
			if tc.want == "" && got != "" || tc.want != "" && !strings.HasPrefix(got, tc.want) {
				t.Errorf("sent %q; want %q", got, tc.want)
			}
		})
	}
}

// The press chooses the binding, and the release goes to that one even when
// the function key goes first (press-owner, ARCHITECTURE §7.4); a key already
// down when the function key goes down keeps the binding it had.
func TestFunctionKeyPressOwner(t *testing.T) {
	for _, tc := range []struct{ name, steps, want string }{
		{"released after the function key", "+back@0 +south@10 -back@20 -south@30", "@10 down decl/1 F1, @30 up decl/1 F1"},
		{"down before the function key", "+south@0 +back@10 -south@20 -back@30", "@0 down decl/1 A, @20 up decl/1 A"},
		{"down before, function key first", "+south@0 +back@10 -back@15 -south@20", "@0 down decl/1 A, @20 up decl/1 A"},
		{"pressed again under the key", "+south@0 +back@10 -south@20 +south@25 -south@30 -back@35", "@0 down decl/1 A, @20 up decl/1 A, @25 down decl/2 F1, @30 up decl/2 F1"},
		{"each press its own", "+back@0 +south@10 -south@20 -back@30 +south@40 -south@50", "@10 down decl/1 F1, @20 up decl/1 F1, @40 down decl/2 A, @50 up decl/2 A"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileLayers(t, fnProfile), r, engine.Options{})
			runM2(t, e, r, tc.steps)
			if got := r.String(); got != tc.want {
				t.Errorf("sent %q; want %q", got, tc.want)
			}
		})
	}
}

// Of two function keys held, the one pressed first gives the set; the other,
// pressed under it, does not switch it, and when the first is let go the other
// gives it.
func TestTwoFunctionKeys(t *testing.T) {
	for _, tc := range []struct {
		name, steps, want string
		active            control.Control // after the steps
	}{
		{"back first", "+back@0 +start@5 +south@10 -south@20", "@10 down decl/1 F1, @20 up decl/1 F1", control.Back},
		{"start first", "+start@0 +back@5 +south@10 -south@20", "@10 down decl/1 F4, @20 up decl/1 F4", control.Start},
		{"letting the first go hands over", "+back@0 +start@5 -back@10 +south@15 -south@20", "@15 down decl/1 F4, @20 up decl/1 F4", control.Start},
		{"letting the second go changes nothing", "+back@0 +start@5 -start@10 +south@15 -south@20", "@15 down decl/1 F1, @20 up decl/1 F1", control.Back},
		{"the first pressed again under the other does not take over", "+back@0 +start@5 -back@10 +back@12 +south@15 -south@20", "@15 down decl/1 F4, @20 up decl/1 F4", control.Start},
		{"both let go", "+back@0 +start@5 -back@10 -start@15 +south@20 -south@25", "@20 down decl/1 A, @25 up decl/1 A", control.Invalid},
		{"the other one has no set", "+start@0 +back@5 -start@10 +north@15 -north@20", "@15 down decl/1 N, @20 up decl/1 N", control.Back},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileLayers(t, fnProfile), r, engine.Options{})
			runM2(t, e, r, tc.steps)
			if got := r.String(); got != tc.want {
				t.Errorf("sent %q; want %q", got, tc.want)
			}
			if got := e.ActiveFunctionKey(); got != tc.active {
				t.Errorf("active function key %v; want %v", got, tc.active)
			}
		})
	}
}

// A function key set can be missing: the key then has none, and a press falls
// back to the normal lookup (the set of the other key is not borrowed).
func TestFunctionKeyWithoutASet(t *testing.T) {
	const src = `{"schemaVersion":1,"name":"fn","layers":{"base":{"south":{"press":{"type":"keys","keys":"A"}}}},
 "functionKeys":{"start":{"south":{"press":{"type":"keys","keys":"F4"}}}}}`
	for _, tc := range []struct{ name, steps, want string }{
		{"back has no set", "+back@0 +south@10 -south@20", "@10 down decl/1 A, @20 up decl/1 A"},
		{"start has one", "+start@0 +south@10 -south@20", "@10 down decl/1 F4, @20 up decl/1 F4"},
		{"back first, start's set is not used", "+back@0 +start@5 +south@10 -south@20", "@10 down decl/1 A, @20 up decl/1 A"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileLayers(t, src), r, engine.Options{})
			runM2(t, e, r, tc.steps)
			if got := r.String(); got != tc.want {
				t.Errorf("sent %q; want %q", got, tc.want)
			}
		})
	}
	// And a profile with no functionKeys at all.
	r := &recorder{}
	e := engine.New(compileLayers(t, `{"schemaVersion":1,"name":"none","layers":{"base":{"south":{"press":{"type":"keys","keys":"A"}}}}}`), r, engine.Options{})
	runM2(t, e, r, "+back@0 +south@10 -south@20 -back@30")
	if got, want := r.String(), "@10 down decl/1 A, @20 up decl/1 A"; got != want {
		t.Errorf("sent %q; want %q", got, want)
	}
}

// The set is chosen when a press arrives, not when a press that waited in the
// buffer is replayed.
func TestFunctionKeyBufferedPress(t *testing.T) {
	for _, tc := range []struct{ name, steps, want string }{
		{
			// south comes before back, behind west's pending gesture; west is
			// released as a tap while back is held, south replays after it.
			"pressed before the function key",
			"+west@0 +south@10 +back@20 -west@30 -south@40 -back@50",
			"@30 tap decl/1 C 20ms, @30 down decl/2 A, @40 up decl/2 A",
		},
		{
			// south comes under back, which is let go before west is decided.
			"pressed under the function key",
			"+west@0 +back@10 +south@20 -back@25 -west@30 -south@40",
			"@30 tap decl/1 C 20ms, @30 down decl/2 F1, @40 up decl/2 F1",
		},
		{
			// The same after the pending gesture times out as a hold.
			"decided by the deadline",
			"+west@0 +back@10 +south@20 -back@25 wait@300 -west@310 -south@320",
			"@250 down decl/1 D, @250 down decl/2 F1, @310 up decl/1 D, @320 up decl/2 F1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileLayers(t, fnProfile), r, engine.Options{})
			runM2(t, e, r, tc.steps)
			if got := r.String(); got != tc.want {
				t.Errorf("sent %q;\nwant      %q", got, tc.want)
			}
		})
	}
}

// The edges that wait in the buffer behind a pending gesture keep the set of
// their arrival, a trigger's too: south before the function key, the trigger
// under it, the pending hold committed by the release of the trigger.
func TestFunctionKeyBufferedPressOfATrigger(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileLayers(t, fnProfile), r, engine.Options{})
	run(t, e, r, "+west@0 +south@10 +back@20 lefttrigger=32767@25 lefttrigger=0@26 -south@27 -back@28 -west@30")
	want := "@26 down decl/1 D, @26 down decl/2 A, @26 down decl/3 F3, @26 up decl/3 F3, @27 up decl/2 A, @30 up decl/1 D"
	if got := r.String(); got != want {
		t.Errorf("sent %q;\nwant      %q", got, want)
	}
}

// A function key is not an interrupt: it does not decide a pending gesture
// before its time and does not wait behind one, so it adds no delay to
// anything.
func TestFunctionKeyAddsNoDelay(t *testing.T) {
	for _, tc := range []struct{ name, steps, want string }{
		{
			"holdPreferred waits for its release",
			"+rightshoulder@0 +back@50 -back@60 -rightshoulder@100",
			"@100 tap decl/1 F6 20ms",
		},
		{
			"holdPreferred is committed by another control's press",
			"+rightshoulder@0 +east@50 -east@60 -rightshoulder@100",
			"@50 down decl/1 Shift, @50 down decl/2 B, @60 up decl/2 B, @100 up decl/1 Shift",
		},
		{
			"balanced waits for its deadline",
			"+west@0 +back@50 -back@60 wait@300 -west@310",
			"@250 down decl/1 D, @310 up decl/1 D",
		},
		{
			"the press of a bound control under the key is at once",
			"+back@0 +south@1 -south@2 -back@3",
			"@1 down decl/1 F1, @2 up decl/1 F1",
		},
		{
			"a function key edge between the release and the window of a double tap",
			"+south@0 -south@10 +back@20 -back@30",
			"@0 down decl/1 A, @10 up decl/1 A",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileLayers(t, fnProfile), r, engine.Options{})
			runM2(t, e, r, tc.steps)
			if got := r.String(); got != tc.want {
				t.Errorf("sent %q; want %q", got, tc.want)
			}
			if d, ok := e.NextDeadline(); ok {
				t.Errorf("a deadline at %v is left", time.Duration(d))
			}
		})
	}
	// Pressing and letting go of a function key leaves no deadline and no
	// buffered edge behind.
	e := engine.New(compileLayers(t, fnProfile), &recorder{}, engine.Options{})
	e.Step(engine.Input{Kind: engine.InputButton, Control: control.Back, Down: true})
	if d, ok := e.NextDeadline(); ok {
		t.Errorf("a function key press has a deadline at %v", time.Duration(d))
	}
	e.Step(engine.Input{Kind: engine.InputButton, Control: control.Back})
	if d, ok := e.NextDeadline(); ok {
		t.Errorf("a function key release has a deadline at %v", time.Duration(d))
	}
}

// The decisions (ENG-20) show the function key edges, with reason function and
// no cycle, level or binding, and the level of a binding found in a set.
func TestFunctionKeyDecisions(t *testing.T) {
	var got []engine.Decision
	r := &recorder{}
	e := engine.New(compileLayers(t, fnProfile), r, engine.Options{Trace: func(d engine.Decision) { got = append(got, d) }})
	run(t, e, r, "+back@0 +south@10 +back@12 -south@20 -back@30 -back@31")
	var briefs []string
	for _, d := range got {
		briefs = append(briefs, brief(d))
	}
	want := []string{
		"@0 back down function",
		"@10 south down dispatch F1 Redo",
		"@12 back down spurious",
		"@20 south up release F1 Redo",
		"@30 back up function",
		"@31 back up spurious",
	}
	if !slices.Equal(briefs, want) {
		t.Errorf("decisions\n%s\nwant\n%s", strings.Join(briefs, "\n"), strings.Join(want, "\n"))
	}
	if len(got) == 6 {
		if f := got[0]; f.Cycle != 0 || f.Level != "" || f.Binding != nil || f.Action != nil || f.Owner != (engine.OwnerID{}) || f.Reason != engine.ReasonFunction {
			t.Errorf("function key decision %+v", f)
		}
		if d := got[1]; d.Level != "function:back" || d.Binding.ID != "/functionKeys/back/south" || d.Cycle != 1 {
			t.Errorf("dispatch decision %+v", d)
		}
		if j := got[0].String(); j != `{"t":0,"control":"back","edge":"down","cycle":0,"reason":"function"}` {
			t.Errorf("JSON %s", j)
		}
	}
}

// Every path that ends the held keys leaves none behind, and the function key,
// which is physical state, is kept by all of them but Reset (ARCHITECTURE §7.4).
func TestFunctionKeyExitPaths(t *testing.T) {
	prog := compileLayers(t, fnProfile)
	plain := compileLayers(t, `{"schemaVersion":1,"name":"plain","layers":{"base":{"south":{"press":{"type":"keys","keys":"A"}}}}}`)
	for _, tc := range []struct {
		name   string
		exit   func(e *engine.Engine, l *ledger, now int)
		active control.Control // after the exit, with back still down
	}{
		{"CancelAll", func(e *engine.Engine, l *ledger, now int) { e.CancelAll(timeAt(now)); l.releaseAll() }, control.Back},
		{"Pause and Resume", func(e *engine.Engine, l *ledger, now int) {
			e.Pause(timeAt(now))
			l.releaseAll()
			e.Resume()
		}, control.Back},
		{"Reload", func(e *engine.Engine, l *ledger, now int) { e.Reload(timeAt(now), prog); l.releaseAll() }, control.Back},
		{"Reload of a program without sets", func(e *engine.Engine, l *ledger, now int) { e.Reload(timeAt(now), plain); l.releaseAll() }, control.Back},
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
			step(control.Back, true, 0)
			step(control.South, true, 10) // held under back: F1
			step(control.East, true, 11)  // held, with its normal binding
			if len(l.held) != 2 {
				t.Fatalf("held %v before the exit; want F1 and B", l.held)
			}
			tc.exit(e, l, 15)
			if len(l.held) != 0 {
				t.Fatalf("%v held after the exit", l.held)
			}
			if got := e.ActiveFunctionKey(); got != tc.active {
				t.Errorf("active function key %v after the exit; want %v", got, tc.active)
			}
			// The releases of what was held do nothing more.
			step(control.South, false, 20)
			step(control.East, false, 21)
			if len(l.held) != 0 {
				t.Fatalf("%v held after the releases", l.held)
			}
			step(control.South, true, 30)
			step(control.South, false, 35)
			step(control.Back, false, 40)
			if got := e.ActiveFunctionKey(); got != control.Invalid {
				t.Errorf("active function key %v after the release of back", got)
			}
			e.CancelAll(timeAt(50))
			l.releaseAll()
			if len(l.held) != 0 {
				t.Errorf("%v held at the end", l.held)
			}
		})
	}
}

// What the press after an exit path sends, exactly.
func TestFunctionKeyAfterExitPaths(t *testing.T) {
	prog := compileLayers(t, fnProfile)
	plain := compileLayers(t, `{"schemaVersion":1,"name":"plain","layers":{"base":{"south":{"press":{"type":"keys","keys":"A"}}}}}`)
	for _, tc := range []struct {
		name  string
		exit  func(e *engine.Engine)
		after string
	}{
		{"CancelAll", func(e *engine.Engine) { e.CancelAll(timeAt(15)) }, "@30 down decl/2 F1, @35 up decl/2 F1"},
		{"Pause and Resume", func(e *engine.Engine) { e.Pause(timeAt(15)); e.Resume() }, "@30 down decl/2 F1, @35 up decl/2 F1"},
		{"Reload", func(e *engine.Engine) { e.Reload(timeAt(15), prog) }, "@30 down decl/2 F1, @35 up decl/2 F1"},
		{"Reload of a program without sets", func(e *engine.Engine) { e.Reload(timeAt(15), plain) }, "@30 down decl/2 A, @35 up decl/2 A"},
		{"Reset", func(e *engine.Engine) { e.Reset(timeAt(15)) }, "@30 down decl/2 A, @35 up decl/2 A"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(prog, r, engine.Options{})
			runM2(t, e, r, "+back@0 +south@10")
			tc.exit(e)
			r.out = nil
			runM2(t, e, r, "-south@20 +south@30 -south@35")
			if got := r.String(); got != tc.after {
				t.Errorf("sent %q; want %q", got, tc.after)
			}
		})
	}
	t.Run("paused", func(t *testing.T) {
		r := &recorder{}
		e := engine.New(prog, r, engine.Options{})
		runM2(t, e, r, "+back@0 pause@5 +south@10 -south@15 -back@20 +south@25 -south@30 resume@35 +south@40 -south@45")
		if got, want := r.String(), "@40 down decl/3 A, @45 up decl/3 A"; got != want {
			t.Errorf("sent %q; want %q", got, want)
		}
	})
	t.Run("a function key is physical state", func(t *testing.T) {
		r := &recorder{}
		e := engine.New(prog, r, engine.Options{})
		runM2(t, e, r, "+back@0 cancel@5 -back@10 +south@15 -south@20")
		if got, want := r.String(), "@15 down decl/1 A, @20 up decl/1 A"; got != want {
			t.Errorf("sent %q; want %q", got, want)
		}
	})
}

// A set is part of the program: Reload takes the new one, and bindings the old
// set had are gone with it.
func TestFunctionKeyReloadChangesTheSet(t *testing.T) {
	const next = `{"schemaVersion":1,"name":"fn2","layers":{"base":{"south":{"press":{"type":"keys","keys":"A"}}}},
 "functionKeys":{"back":{"south":{"press":{"type":"keys","keys":"F9"}}}}}`
	r := &recorder{}
	e := engine.New(compileLayers(t, fnProfile), r, engine.Options{})
	runM2(t, e, r, "+back@0")
	e.Reload(timeAt(5), compileLayers(t, next))
	runM2(t, e, r, "+south@10 -south@20")
	if got, want := r.String(), "@10 down decl/1 F9, @20 up decl/1 F9"; got != want {
		t.Errorf("sent %q; want %q", got, want)
	}
}

// The key hints (HUD-03) show the set while a function key is held, and the
// normal bindings for the controls the set lacks.
func TestFunctionKeyHints(t *testing.T) {
	const src = `{"schemaVersion":1,"name":"hints","layers":{"base":{
 "rightshoulder":{"label":"hints","press":{"type":"hud","op":"hints"}},
 "south":{"label":"Undo","press":{"type":"keys","keys":"Ctrl+Z"}},
 "east":{"press":{"type":"keys","keys":"Space"}},
 "west":{"press":{"type":"keys","keys":"W"}},
 "dpad.up":{"label":"Up","press":{"type":"keys","keys":"U"}}}},
 "functionKeys":{
  "back":{
   "south":{"label":"Redo","press":{"type":"keys","keys":"Ctrl+Y"}},
   "north":{"press":{"type":"keys","keys":"F5"}},
   "west":{"block":true},
   "dpad.up":{"press":{"type":"keys","keys":"F6"}}},
  "start":{"east":{"label":"Fill","press":{"type":"keys","keys":"F"}}}}}`
	const normal = "on:south=Undo | east=Space | west=W | dpad.up=Up"
	const backSet = "on:south=Redo | east=Space | north=F5 | dpad.up=F6"
	const startSet = "on:south=Undo | east=Fill | west=W | dpad.up=Up"
	t.Run("the key is held first", func(t *testing.T) {
		h := newHsim(t, src)
		h.press("back")
		h.wantShown("a function key, no hints", "off")
		h.press("rightshoulder")
		h.wantShown("hints under back", backSet)
		h.release("back")
		h.wantShown("back let go", normal)
		h.release("rightshoulder")
		h.wantShown("hints let go", "off")
	})
	t.Run("the hints are on first", func(t *testing.T) {
		h := newHsim(t, src)
		h.press("rightshoulder")
		h.wantShown("normal", normal)
		h.press("back")
		h.wantShown("back held", backSet)
		h.press("start")
		h.wantShown("start pressed under back", backSet)
		h.release("back")
		h.wantShown("back let go, start is active", startSet)
		h.release("start")
		h.wantShown("both let go", normal)
		h.press("start")
		h.wantShown("start", startSet)
		h.release("rightshoulder")
		h.wantShown("hints let go", "off")
	})
	t.Run("sends nothing", func(t *testing.T) {
		h := newHsim(t, src)
		h.press("rightshoulder")
		h.press("back")
		h.release("back")
		h.release("rightshoulder")
		if h.out.String() != "" {
			t.Errorf("sent %q", h.out.String())
		}
	})
	t.Run("a copy", func(t *testing.T) {
		h := newHsim(t, src)
		h.press("rightshoulder")
		h.press("back")
		hs := h.e.View().Hints
		for _, hint := range hs {
			if hint.Control.IsFunctionKey() {
				t.Errorf("the hints list the function key %s", hint.Control)
			}
		}
	})
}

// A script (SCR-05) sees no function key: a handler on back or start never
// runs, and Options.Button is not asked about them; pad.isDown still reads
// them (ControlDown). A global() handler is above the set, a base handler is
// below it.
func TestFunctionKeysAndScripts(t *testing.T) {
	const src = `{"schemaVersion":1,"name":"fn","script":{"entry":"main.ts","api":"0.21"},"layers":{"base":{
 "south":{"press":{"type":"keys","keys":"A"}},
 "east":{"press":{"type":"keys","keys":"B"}}}},
 "functionKeys":{"back":{"south":{"press":{"type":"keys","keys":"F1"}},"north":{"press":{"type":"keys","keys":"F2"}}}}}`
	entry := func(c control.Control, scope string) engine.GestureEntry {
		return engine.GestureEntry{ID: 1, Control: c, Gesture: engine.GesturePress, Scope: scope}
	}
	t.Run("handlers on the function keys never run", func(t *testing.T) {
		r := &recorder{}
		g := &gestureRegistry{entries: []engine.GestureEntry{entry(control.Back, "global"), entry(control.Start, "base"), entry(control.Back, "base")}}
		var buttons []engine.ButtonEvent
		e := engine.New(compileLayers(t, src), r, engine.Options{Gestures: g, Button: func(ev engine.ButtonEvent) bool { buttons = append(buttons, ev); return false }})
		run(t, e, r, "+back@0 +start@5 -back@10 -start@15")
		if len(g.events) != 0 || len(g.ends) != 0 || len(buttons) != 0 || r.String() != "" {
			t.Errorf("handler events %+v, ends %+v, buttons %+v, sent %q", g.events, g.ends, buttons, r.String())
		}
		if e.ControlDown(control.Back) || e.ControlDown(control.Start) {
			t.Error("the function keys read as down after their release")
		}
		run(t, e, r, "+back@20")
		if !e.ControlDown(control.Back) {
			t.Error("ControlDown does not report a held function key")
		}
	})
	t.Run("global() wins over the set", func(t *testing.T) {
		r := &recorder{}
		g := &gestureRegistry{entries: []engine.GestureEntry{entry(control.South, "global")}}
		e := engine.New(compileLayers(t, src), r, engine.Options{Gestures: g})
		run(t, e, r, "+back@0 +south@10 -south@20 -back@30")
		if len(g.events) != 1 || r.String() != "" {
			t.Errorf("handler events %+v, sent %q; want the handler to take it", g.events, r.String())
		}
		// ... and when it passes, the set is next.
		r, g.events, g.pass = &recorder{}, nil, true
		e = engine.New(compileLayers(t, src), r, engine.Options{Gestures: g})
		run(t, e, r, "+back@0 +south@10 -south@20 -back@30")
		if len(g.events) != 1 || r.String() != "@10 down decl/1 F1, @20 up decl/1 F1" {
			t.Errorf("handler events %+v, sent %q; want the set's F1 after the pass", g.events, r.String())
		}
	})
	t.Run("a base handler is below the set", func(t *testing.T) {
		r := &recorder{}
		g := &gestureRegistry{entries: []engine.GestureEntry{entry(control.South, "base"), entry(control.East, "base"), entry(control.North, "base")}}
		e := engine.New(compileLayers(t, src), r, engine.Options{Gestures: g})
		// south and north are in the set: no handler. east is not: it runs.
		run(t, e, r, "+back@0 +south@10 -south@20 +north@30 -north@40 +east@50 -east@60 -back@70")
		if len(g.events) != 1 || g.events[0].Control != control.East {
			t.Errorf("handler events %+v; want east's only", g.events)
		}
		if got, want := r.String(), "@10 down decl/1 F1, @20 up decl/1 F1, @30 down decl/2 F2, @40 up decl/2 F2"; got != want {
			t.Errorf("sent %q; want %q", got, want)
		}
		// Without the function key, the handler of south runs.
		g.events = nil
		run(t, e, r, "+south@100 -south@110")
		if len(g.events) != 1 || g.events[0].Control != control.South {
			t.Errorf("handler events %+v; want south's", g.events)
		}
	})
	t.Run("the scope of an event of the set", func(t *testing.T) {
		const withScript = `{"schemaVersion":1,"name":"fn","script":{"entry":"main.ts","api":"0.21"},"layers":{"base":{}},
 "functionKeys":{"back":{"south":{"press":{"type":"script","name":"act"}}}}}`
		r := &recorder{}
		var events []engine.ScriptActionEvent
		g := &gestureRegistry{}
		e := engine.New(compileLayers(t, withScript), r, engine.Options{Gestures: g, Script: func(ev engine.ScriptActionEvent) { events = append(events, ev) }})
		run(t, e, r, "+back@0 +south@10 -south@20 -back@30")
		if len(events) != 1 || events[0].Name != "act" || events[0].Scope != "global" {
			t.Errorf("script action events %+v; want act with the scope global", events)
		}
	})
	t.Run("InputScope of a control the set binds", func(t *testing.T) {
		e := engine.New(compileLayers(t, src), &recorder{}, engine.Options{})
		if got := e.InputScope(control.South); got != "base" {
			t.Errorf("InputScope(south) = %q before the key", got)
		}
		e.Step(engine.Input{Kind: engine.InputButton, Control: control.Back, Down: true})
		if got := e.InputScope(control.South); got != "global" {
			t.Errorf("InputScope(south) = %q under back; want global", got)
		}
		if got := e.InputScope(control.East); got != "base" {
			t.Errorf("InputScope(east) = %q under back; want base", got)
		}
	})
}

// The Button callback is not asked about a function key, and a press the
// callback consumes is not looked up either.
func TestFunctionKeyButtonCallback(t *testing.T) {
	var asked []control.Control
	r := &recorder{}
	e := engine.New(compileLayers(t, fnProfile), r, engine.Options{Button: func(ev engine.ButtonEvent) bool {
		asked = append(asked, ev.Control)
		return ev.Control == control.South
	}})
	run(t, e, r, "+back@0 +south@10 -south@20 +east@30 -east@40 -back@50")
	if !slices.Equal(asked, []control.Control{control.South, control.South, control.East, control.East}) {
		t.Errorf("Button asked about %v", asked)
	}
	if got, want := r.String(), "@30 down decl/1 B, @40 up decl/1 B"; got != want {
		t.Errorf("sent %q; want %q", got, want)
	}
}

// The levels of a registry (ENG-21) are below the set of the held function key.
func TestFunctionKeyAboveARegistry(t *testing.T) {
	b := &profile.Binding{ID: "/reg/south", Control: control.South, Press: &profile.Action{ID: "/reg/south/press", Type: profile.ActionKeys, Mode: profile.ModeHold, Keys: keys.MustParse("R")}}
	level := fakeLevel{name: "reg", bindings: map[control.Control]*profile.Binding{control.South: b, control.East: {ID: "/reg/east", Control: control.East, Press: &profile.Action{ID: "/reg/east/press", Type: profile.ActionKeys, Mode: profile.ModeHold, Keys: keys.MustParse("S")}}}}
	r := &recorder{}
	e := engine.New(compileLayers(t, fnProfile), r, engine.Options{Registry: fakeRegistry{levels: []engine.Level{level}}})
	runM2(t, e, r, "+south@0 -south@5 +back@10 +south@15 -south@20 +east@25 -east@30 -back@35")
	want := "@0 down decl/1 R, @5 up decl/1 R, @15 down decl/2 F1, @20 up decl/2 F1, @25 down decl/3 S, @30 up decl/3 S"
	if got := r.String(); got != want {
		t.Errorf("sent %q; want %q", got, want)
	}
}

// Back and start in a layer or in common are not compiled, whatever they hold:
// profile.Parse drops them, and Compile ignores those of a model built by
// hand. The sets of the function keys are listed with their own level names.
func TestCompileFunctionKeys(t *testing.T) {
	keysAction := func(id, k string) *profile.Action {
		return &profile.Action{ID: id, Type: profile.ActionKeys, Mode: profile.ModeHold, Keys: keys.MustParse(k)}
	}
	bad := &profile.Binding{ID: "/layers/base/back", Control: control.Back, Press: &profile.Action{ID: "/layers/base/back/press", Type: profile.ActionType(250)}}
	other := &profile.Binding{ID: "/common/start", Control: control.Start, Press: keysAction("/common/start/press", "Q")}
	south := &profile.Binding{ID: "/layers/base/south", Control: control.South, Press: keysAction("/layers/base/south/press", "A")}
	inSet := &profile.Binding{ID: "/functionKeys/back/south", Control: control.South, Press: keysAction("/functionKeys/back/south/press", "F1")}
	prog, err := engine.Compile(&profile.Profile{
		Layers:       []*profile.Layer{{Name: "base", Bindings: []*profile.Binding{bad, south}}},
		Common:       &profile.Layer{Name: "common", Bindings: []*profile.Binding{other}},
		FunctionKeys: &profile.FunctionKeys{Back: &profile.Layer{Name: "function:back", Bindings: []*profile.Binding{inSet}}},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	var listed []string
	for _, bi := range prog.Bindings() {
		listed = append(listed, bi.Layer+" "+bi.Binding.ID)
	}
	want := []string{"base /layers/base/south", "function:back /functionKeys/back/south"}
	if !slices.Equal(listed, want) {
		t.Errorf("Bindings() = %q; want %q", listed, want)
	}
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{})
	runM2(t, e, r, "+back@0 -back@1 +start@2 -start@3 +south@4 -south@5")
	if got, want := r.String(), "@4 down decl/1 A, @5 up decl/1 A"; got != want {
		t.Errorf("sent %q; want %q", got, want)
	}
	// A function key cannot be bound in a set.
	_, err = engine.Compile(&profile.Profile{
		Layers:       []*profile.Layer{{Name: "base"}},
		FunctionKeys: &profile.FunctionKeys{Back: &profile.Layer{Name: "function:back", Bindings: []*profile.Binding{{ID: "/functionKeys/back/start", Control: control.Start}}}},
	})
	if err == nil || !strings.Contains(err.Error(), "function key") {
		t.Errorf("Compile of a set that binds start: %v", err)
	}
	// ... nor twice, and a binding in a set is checked like a layer's.
	_, err = engine.Compile(&profile.Profile{
		Layers: []*profile.Layer{{Name: "base"}},
		FunctionKeys: &profile.FunctionKeys{Start: &profile.Layer{Name: "function:start", Bindings: []*profile.Binding{
			{ID: "/a", Control: control.South, Tap: &profile.Action{ID: "/a/tap", Type: profile.ActionLayer, Layer: "fn", Op: "hold"}},
		}}},
	})
	if err == nil || !strings.Contains(err.Error(), "layer hold action is only valid for press/hold") {
		t.Errorf("Compile of a set with a hold on a tap: %v", err)
	}
}

// A multi-tap window belongs to the binding that opened it. When a function key
// goes down or up between two taps, the second press finds another binding
// (the press looks up when it arrives), so it is not the next tap of that
// window: the window is committed first, as another control's press does, and
// the press starts a new count. Nothing the first tap was going to send is lost
// and the taps of two bindings are never added up.
func TestFunctionKeyBetweenTaps(t *testing.T) {
	const (
		// back's set rebinds south as a plain press, start's as a tap and a
		// double tap, and the set of a profile that blocks south under back.
		press = `{"schemaVersion":1,"name":"fn","layers":{"base":{
 "south":{"tap":{"type":"keys","keys":"F13","mode":"tap"},"doubleTap":{"type":"keys","keys":"F14","mode":"tap"}}}},
 "functionKeys":{"back":{"south":{"press":{"type":"keys","keys":"F15"}}},
  "start":{"south":{"tap":{"type":"keys","keys":"F17","mode":"tap"},"doubleTap":{"type":"keys","keys":"F18","mode":"tap"}}}}}`
		block = `{"schemaVersion":1,"name":"fn","layers":{"base":{
 "south":{"tap":{"type":"keys","keys":"F13","mode":"tap"},"doubleTap":{"type":"keys","keys":"F14","mode":"tap"}}}},
 "functionKeys":{"back":{"south":{"block":true}}}}`
		// back's set rebinds south as a tap and a double tap of its own.
		multi = `{"schemaVersion":1,"name":"fn","layers":{"base":{
 "south":{"tap":{"type":"keys","keys":"F13","mode":"tap"},"doubleTap":{"type":"keys","keys":"F14","mode":"tap"}}}},
 "functionKeys":{"back":{"south":{"tap":{"type":"keys","keys":"F15","mode":"tap"},"doubleTap":{"type":"keys","keys":"F16","mode":"tap"}}}}}`
	)
	for _, tc := range []struct{ name, src, steps, want string }{
		{
			"the tap before the key is committed",
			press, "+south@10 -south@20 +back@30 +south@40 -south@50 -back@60 wait@1000",
			"@40 tap decl/1 F13 20ms, @40 down decl/2 F15, @50 up decl/2 F15",
		},
		{
			"the tap under the key is committed when it goes",
			multi, "+back@0 +south@10 -south@20 -back@30 +south@40 -south@50 wait@1000",
			"@40 tap decl/1 F15 20ms, @270 tap decl/2 F13 20ms",
		},
		{
			"the tap before a block is committed",
			block, "+south@10 -south@20 +back@30 +south@40 -south@50 -back@60 wait@1000",
			"@40 tap decl/1 F13 20ms",
		},
		{
			"from one key's set to the other's",
			press, "+start@0 +south@10 -south@20 -start@25 +back@30 +south@40 -south@50 -back@60 wait@1000",
			"@40 tap decl/1 F17 20ms, @40 down decl/2 F15, @50 up decl/2 F15",
		},
		{
			// The control south is not bound by the set of start's, so it goes
			// on as the same binding: a double tap of base's.
			"the key does not change the binding",
			multi, "+start@0 +south@10 -south@20 +south@40 -south@50 -start@60 wait@1000",
			"@50 tap decl/1 F14 20ms",
		},
		{
			"the key held through both taps",
			multi, "+back@0 +south@10 -south@20 +south@40 -south@50 -back@60 wait@1000",
			"@50 tap decl/1 F16 20ms",
		},
		{
			"no function key",
			press, "+south@10 -south@20 +south@40 -south@50 wait@1000",
			"@50 tap decl/1 F14 20ms",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileLayers(t, tc.src), r, engine.Options{})
			runM2(t, e, r, tc.steps)
			if got := r.String(); got != tc.want {
				t.Errorf("sent %q;\nwant      %q", got, tc.want)
			}
			if d, ok := e.NextDeadline(); ok {
				t.Errorf("a deadline at %v is left", time.Duration(d))
			}
		})
	}
}

// View says which function key is held, for the explanation of a mapping
// (ADR-0071): control.Invalid when none is, the first of two held, and none
// again after Reset.
func TestViewFunctionKey(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileLayers(t, fnProfile), r, engine.Options{})
	if got := e.View().FunctionKey; got != control.Invalid {
		t.Errorf("FunctionKey = %v before any press", got)
	}
	run(t, e, r, "+start@0 +back@5")
	if got := e.View().FunctionKey; got != control.Start {
		t.Errorf("FunctionKey = %v with start held first, want start", got)
	}
	run(t, e, r, "-start@10")
	if got := e.View().FunctionKey; got != control.Back {
		t.Errorf("FunctionKey = %v with only back held, want back", got)
	}
	e.Reset(clock.Time(20 * time.Millisecond))
	if got := e.View().FunctionKey; got != control.Invalid {
		t.Errorf("FunctionKey = %v after Reset", got)
	}
}
