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

// Tests of the function key (MAP-27, ADR-0069, ADR-0075): one of back, start
// and guide, start unless Compile is told otherwise, sends nothing, stays out
// of gesture recognition, and while it is held the other controls look their
// binding up in the profile's function set first.

// compileFn parses src and compiles it with function key fn; control.Invalid
// is the default, start.
func compileFn(t testing.TB, src string, fn control.Control) *engine.Program {
	t.Helper()
	p, ds := profile.Parse([]byte(src))
	if p == nil || ds.HasErrors() {
		t.Fatalf("profile.Parse: %v", ds)
	}
	prog, err := engine.Compile(p, engine.WithFunctionKey(fn))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return prog
}

// fnProfile has a base layer, a layer alt that rebinds south, and a function
// set. The set rebinds south and west (west is a press there, while the base
// binding is a tap and a hold), blocks dpad.up, owns dpad.left with a label
// only, and binds lt, north and the three controls that can be the function
// key; east is not in it. back, guide and start have a binding in the base
// layer too, so whichever of them is not the function key is an ordinary
// control, and the one that is has a binding that is ignored, in the layer and
// in the set. rightshoulder is a tap with a hold of Shift, a gesture that
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
  "lt":{"press":{"type":"keys","keys":"Alt"}},
  "back":{"press":{"type":"keys","keys":"V"}},
  "guide":{"press":{"type":"keys","keys":"G"}},
  "start":{"press":{"type":"keys","keys":"S"}}
 },
 "alt":{"south":{"press":{"type":"keys","keys":"X"}}}},
 "functionSet":{
  "south":{"label":"Redo","press":{"type":"keys","keys":"F1"}},
  "west":{"press":{"type":"keys","keys":"F2"}},
  "north":{"press":{"type":"keys","keys":"F5"}},
  "dpad.up":{"block":true},
  "dpad.left":{"label":"only a label"},
  "lt":{"press":{"type":"keys","keys":"F3"}},
  "back":{"press":{"type":"keys","keys":"F7"}},
  "guide":{"press":{"type":"keys","keys":"F8"}},
  "start":{"press":{"type":"keys","keys":"F9"}}
 }}`

func TestFunctionKeySets(t *testing.T) {
	for _, tc := range []struct{ name, steps, want string }{
		{"no function key", "+south@10 -south@20", "@10 down decl/1 A, @20 up decl/1 A"},
		{"the set is used", "+start@0 +south@10 -south@20 -start@30", "@10 down decl/1 F1, @20 up decl/1 F1"},
		{"a control the set lacks keeps its binding", "+start@0 +east@10 -east@20 -start@30", "@10 down decl/1 B, @20 up decl/1 B"},
		{"the set is dropped with the key", "+start@0 -start@10 +south@20 -south@30", "@20 down decl/1 A, @30 up decl/1 A"},
		{"a block binding consumes the control", "+start@0 +dpad.up@10 -dpad.up@20 -start@30", ""},
		{"block only while it is held", "+start@0 -start@10 +dpad.up@20 -dpad.up@30", "@20 down decl/1 U, @30 up decl/1 U"},
		{"a binding with only a label owns the control, as in a layer", "+start@0 +dpad.left@10 -dpad.left@20 -start@30", ""},
		{"a press where the base has a tap and a hold waits for nothing", "+start@0 +west@10 -west@20 -start@30", "@10 down decl/1 F2, @20 up decl/1 F2"},
		{"the base binding is untouched", "+west@10 -west@20 wait@300", "@20 tap decl/1 C 20ms"},
		{"above an overlay layer", "+leftshoulder@0 +south@5 -south@6 +start@10 +south@15 -south@20 -start@25 -leftshoulder@30", "@5 down decl/2 X, @6 up decl/2 X, @15 down decl/3 F1, @20 up decl/3 F1"},
		{"a function key does not use a cycle", "+start@0 -start@10 +start@20 -start@30 +south@40 -south@50", "@40 down decl/1 A, @50 up decl/1 A"},
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

// Each of back, start and guide can be the function key: it sends nothing,
// and the binding it has in a layer and in the set is ignored and listed
// (IgnoredFunctionKeyBindings); the other two are ordinary controls, with
// their own bindings in the layers, and under the function key with the set's.
func TestFunctionKeyChoice(t *testing.T) {
	for _, tc := range []struct {
		fn      control.Control
		ignored []string
		// The base binding and the set's of each key, as sent.
		plain, under map[string]string
	}{
		{control.Invalid, []string{"/layers/base/start", "/functionSet/start"}, map[string]string{"back": "V", "guide": "G"}, map[string]string{"back": "F7", "guide": "F8"}},
		{control.Start, []string{"/layers/base/start", "/functionSet/start"}, map[string]string{"back": "V", "guide": "G"}, map[string]string{"back": "F7", "guide": "F8"}},
		{control.Back, []string{"/layers/base/back", "/functionSet/back"}, map[string]string{"guide": "G", "start": "S"}, map[string]string{"guide": "F8", "start": "F9"}},
		{control.Guide, []string{"/layers/base/guide", "/functionSet/guide"}, map[string]string{"back": "V", "start": "S"}, map[string]string{"back": "F7", "start": "F9"}},
		{control.Control(250), []string{"/layers/base/start", "/functionSet/start"}, map[string]string{"back": "V", "guide": "G"}, map[string]string{"back": "F7", "guide": "F8"}},
	} {
		prog := compileFn(t, fnProfile, tc.fn)
		fn := prog.FunctionKey()
		t.Run(fn.String()+" from "+tc.fn.String(), func(t *testing.T) {
			if !fn.CanBeFunctionKey() || (tc.fn.CanBeFunctionKey() && fn != tc.fn) {
				t.Errorf("FunctionKey() = %s for %s", fn, tc.fn)
			}
			if got := prog.IgnoredFunctionKeyBindings(); !slices.Equal(got, tc.ignored) {
				t.Errorf("ignored bindings %q; want %q", got, tc.ignored)
			}
			for _, bi := range prog.Bindings() {
				if bi.Binding.Control == fn {
					t.Errorf("Bindings() lists %s of %s, the function key", bi.Binding.ID, bi.Layer)
				}
			}
			name := fn.String()
			// The function key sends nothing, held alone or pressed again.
			r := &recorder{}
			e := engine.New(prog, r, engine.Options{})
			runM2(t, e, r, "+"+name+"@0 -"+name+"@10 +"+name+"@20 -"+name+"@30 wait@500")
			if got := r.String(); got != "" {
				t.Errorf("the function key sent %q", got)
			}
			// The set is used under it.
			runM2(t, e, r, "+"+name+"@100 +south@110 -south@120 -"+name+"@130")
			if got, want := r.String(), "@110 down decl/1 F1, @120 up decl/1 F1"; got != want {
				t.Errorf("under the function key sent %q; want %q", got, want)
			}
			// The other two are ordinary controls, plain and under the key.
			for _, other := range []string{"back", "guide", "start"} {
				if other == name {
					continue
				}
				plain, under := tc.plain[other], tc.under[other]
				r := &recorder{}
				e := engine.New(prog, r, engine.Options{})
				runM2(t, e, r, "+"+other+"@0 -"+other+"@10 +"+name+"@20 +"+other+"@30 -"+other+"@40 -"+name+"@50")
				want := "@0 down decl/1 " + plain + ", @10 up decl/1 " + plain + ", @30 down decl/2 " + under + ", @40 up decl/2 " + under
				if got := r.String(); got != want {
					t.Errorf("%s: sent %q; want %q", other, got, want)
				}
			}
		})
	}
}

// The trigger is a virtual control, the one the set binds goes through the
// same lookup.
func TestFunctionKeySetOfATrigger(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileLayers(t, fnProfile), r, engine.Options{})
	run(t, e, r, "+start@0 lefttrigger=32767@10 lefttrigger=0@20 -start@30 lefttrigger=32767@40 lefttrigger=0@50")
	if got, want := r.String(), "@10 down decl/1 F3, @20 up decl/1 F3, @40 down decl/2 Alt, @50 up decl/2 Alt"; got != want {
		t.Errorf("sent %q; want %q", got, want)
	}
}

// A stick direction, a ring or a knob step that only the set binds is
// recognized while the function key is held, as a control that only an overlay
// binds is while the overlay is.
func TestFunctionKeySetOfVirtualControls(t *testing.T) {
	const src = `{"schemaVersion":1,"name":"fn","analog":{"ls":{"mode":"fourWay"},"rs":{"mode":"knob"}},"layers":{"base":{}},
 "functionSet":{"ls.up":{"press":{"type":"keys","keys":"U"}},"rs.cw":{"press":{"type":"keys","keys":"R","mode":"tap"}}}}`
	for _, tc := range []struct{ name, steps, want string }{
		{"direction", "+start@0 lefty=-30000@10 lefty=0@20 -start@30", "@10 down decl/1 U"},
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
		{"released after the function key", "+start@0 +south@10 -start@20 -south@30", "@10 down decl/1 F1, @30 up decl/1 F1"},
		{"down before the function key", "+south@0 +start@10 -south@20 -start@30", "@0 down decl/1 A, @20 up decl/1 A"},
		{"down before, function key first", "+south@0 +start@10 -start@15 -south@20", "@0 down decl/1 A, @20 up decl/1 A"},
		{"pressed again under the key", "+south@0 +start@10 -south@20 +south@25 -south@30 -start@35", "@0 down decl/1 A, @20 up decl/1 A, @25 down decl/2 F1, @30 up decl/2 F1"},
		{"each press its own", "+start@0 +south@10 -south@20 -start@30 +south@40 -south@50", "@10 down decl/1 F1, @20 up decl/1 F1, @40 down decl/2 A, @50 up decl/2 A"},
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

// There is one function key. The other two center keys are ordinary controls:
// pressing one under the function key does not switch anything, and neither
// of them is a function key of its own, so held with the function key they
// both have bindings of the set, and held alone they have no effect on which
// set is used.
func TestOtherCenterKeysAreOrdinary(t *testing.T) {
	for _, tc := range []struct {
		name, steps, want string
		active            control.Control // after the steps
	}{
		{"alone", "+back@0 -back@10", "@0 down decl/1 V, @10 up decl/1 V", control.Invalid},
		{"back held, then the function key", "+back@0 +start@5 +south@10 -south@20 -start@25 -back@30", "@0 down decl/1 V, @10 down decl/2 F1, @20 up decl/2 F1, @30 up decl/1 V", control.Invalid},
		{"back held under the function key: its set binding", "+start@0 +back@5 +south@10 -south@20 -back@25", "@5 down decl/1 F7, @10 down decl/2 F1, @20 up decl/2 F1, @25 up decl/1 F7", control.Start},
		{"guide does not take the set", "+guide@0 +south@10 -south@20 -guide@30", "@0 down decl/1 G, @10 down decl/2 A, @20 up decl/2 A, @30 up decl/1 G", control.Invalid},
		{"the function key let go under back", "+start@0 +back@5 -start@10 +south@15 -south@20 -back@25", "@5 down decl/1 F7, @15 down decl/2 A, @20 up decl/2 A, @25 up decl/1 F7", control.Invalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileLayers(t, fnProfile), r, engine.Options{})
			runM2(t, e, r, tc.steps)
			if got := r.String(); got != tc.want {
				t.Errorf("sent %q; want %q", got, tc.want)
			}
			if got := e.ActiveFunctionKey(); got != tc.active {
				t.Errorf("function key held %v; want %v", got, tc.active)
			}
		})
	}
}

// The profile can have no set, in either format: the function key then
// changes nothing, and the old functionKeys are merged into the one set, start's
// first and back's for the controls start's does not bind (ADR-0075).
func TestFunctionKeyWithoutASet(t *testing.T) {
	const plain = `{"schemaVersion":1,"name":"none","layers":{"base":{"south":{"press":{"type":"keys","keys":"A"}}}}}`
	r := &recorder{}
	e := engine.New(compileLayers(t, plain), r, engine.Options{})
	runM2(t, e, r, "+start@0 +south@10 -south@20 -start@30")
	if got, want := r.String(), "@10 down decl/1 A, @20 up decl/1 A"; got != want {
		t.Errorf("sent %q; want %q", got, want)
	}
	const old = `{"schemaVersion":1,"name":"old","layers":{"base":{"south":{"press":{"type":"keys","keys":"A"}},"north":{"press":{"type":"keys","keys":"N"}},"east":{"press":{"type":"keys","keys":"B"}}}},
 "functionKeys":{
  "back":{"south":{"press":{"type":"keys","keys":"F1"}},"north":{"press":{"type":"keys","keys":"F2"}}},
  "start":{"south":{"press":{"type":"keys","keys":"F4"}}}}}`
	for _, tc := range []struct {
		name string
		fn   control.Control
	}{{"start", control.Start}, {"back", control.Back}, {"guide", control.Guide}} {
		t.Run("old format, "+tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileFn(t, old, tc.fn), r, engine.Options{})
			runM2(t, e, r, "+"+tc.name+"@0 +south@10 -south@20 +north@30 -north@40 +east@50 -east@60 -"+tc.name+"@70")
			// south: start's set wins; north: only back's has it; east: neither.
			if got, want := r.String(), "@10 down decl/1 F4, @20 up decl/1 F4, @30 down decl/2 F2, @40 up decl/2 F2, @50 down decl/3 B, @60 up decl/3 B"; got != want {
				t.Errorf("sent %q; want %q", got, want)
			}
		})
	}
}

// The set is chosen when a press arrives, not when a press that waited in the
// buffer is replayed.
func TestFunctionKeyBufferedPress(t *testing.T) {
	for _, tc := range []struct{ name, steps, want string }{
		{
			// south comes before the function key, behind west's pending
			// gesture; west is released as a tap while the key is held, south
			// replays after it.
			"pressed before the function key",
			"+west@0 +south@10 +start@20 -west@30 -south@40 -start@50",
			"@30 tap decl/1 C 20ms, @30 down decl/2 A, @40 up decl/2 A",
		},
		{
			// south comes under the key, which is let go before west is decided.
			"pressed under the function key",
			"+west@0 +start@10 +south@20 -start@25 -west@30 -south@40",
			"@30 tap decl/1 C 20ms, @30 down decl/2 F1, @40 up decl/2 F1",
		},
		{
			// The same after the pending gesture times out as a hold.
			"decided by the deadline",
			"+west@0 +start@10 +south@20 -start@25 wait@300 -west@310 -south@320",
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
	run(t, e, r, "+west@0 +south@10 +start@20 lefttrigger=32767@25 lefttrigger=0@26 -south@27 -start@28 -west@30")
	want := "@26 down decl/1 D, @26 down decl/2 A, @26 down decl/3 F3, @26 up decl/3 F3, @27 up decl/2 A, @30 up decl/1 D"
	if got := r.String(); got != want {
		t.Errorf("sent %q;\nwant      %q", got, want)
	}
}

// The function key is not an interrupt: it does not decide a pending gesture
// before its time and does not wait behind one, so it adds no delay to
// anything.
func TestFunctionKeyAddsNoDelay(t *testing.T) {
	for _, tc := range []struct{ name, steps, want string }{
		{
			"holdPreferred waits for its release",
			"+rightshoulder@0 +start@50 -start@60 -rightshoulder@100",
			"@100 tap decl/1 F6 20ms",
		},
		{
			"holdPreferred is committed by another control's press",
			"+rightshoulder@0 +east@50 -east@60 -rightshoulder@100",
			"@50 down decl/1 Shift, @50 down decl/2 B, @60 up decl/2 B, @100 up decl/1 Shift",
		},
		{
			"holdPreferred is committed by an ordinary center key, which is a press like any",
			"+rightshoulder@0 +back@50 -back@60 -rightshoulder@100",
			"@50 down decl/1 Shift, @50 down decl/2 V, @60 up decl/2 V, @100 up decl/1 Shift",
		},
		{
			"balanced waits for its deadline",
			"+west@0 +start@50 -start@60 wait@300 -west@310",
			"@250 down decl/1 D, @310 up decl/1 D",
		},
		{
			"the press of a bound control under the key is at once",
			"+start@0 +south@1 -south@2 -start@3",
			"@1 down decl/1 F1, @2 up decl/1 F1",
		},
		{
			"a function key edge between the release and the window of a double tap",
			"+south@0 -south@10 +start@20 -start@30",
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
	// Pressing and letting go of the function key leaves no deadline and no
	// buffered edge behind.
	e := engine.New(compileLayers(t, fnProfile), &recorder{}, engine.Options{})
	e.Step(engine.Input{Kind: engine.InputButton, Control: control.Start, Down: true})
	if d, ok := e.NextDeadline(); ok {
		t.Errorf("a function key press has a deadline at %v", time.Duration(d))
	}
	e.Step(engine.Input{Kind: engine.InputButton, Control: control.Start})
	if d, ok := e.NextDeadline(); ok {
		t.Errorf("a function key release has a deadline at %v", time.Duration(d))
	}
}

// The decisions (ENG-20) show the function key edges, with reason function and
// no cycle, level or binding, and the level of a binding found in the set,
// function:<the key>.
func TestFunctionKeyDecisions(t *testing.T) {
	for _, fn := range []control.Control{control.Start, control.Back, control.Guide} {
		t.Run(fn.String(), func(t *testing.T) {
			var got []engine.Decision
			r := &recorder{}
			e := engine.New(compileFn(t, fnProfile, fn), r, engine.Options{Trace: func(d engine.Decision) { got = append(got, d) }})
			name := fn.String()
			run(t, e, r, "+"+name+"@0 +south@10 +"+name+"@12 -south@20 -"+name+"@30 -"+name+"@31")
			var briefs []string
			for _, d := range got {
				briefs = append(briefs, brief(d))
			}
			want := []string{
				"@0 " + name + " down function",
				"@10 south down dispatch F1 Redo",
				"@12 " + name + " down spurious",
				"@20 south up release F1 Redo",
				"@30 " + name + " up function",
				"@31 " + name + " up spurious",
			}
			if !slices.Equal(briefs, want) {
				t.Fatalf("decisions\n%s\nwant\n%s", strings.Join(briefs, "\n"), strings.Join(want, "\n"))
			}
			if f := got[0]; f.Cycle != 0 || f.Level != "" || f.Binding != nil || f.Action != nil || f.Owner != (engine.OwnerID{}) || f.Reason != engine.ReasonFunction {
				t.Errorf("function key decision %+v", f)
			}
			if d := got[1]; d.Level != "function:"+name || d.Binding.ID != "/functionSet/south" || d.Cycle != 1 {
				t.Errorf("dispatch decision %+v", d)
			}
			if j := got[0].String(); j != `{"t":0,"control":"`+name+`","edge":"down","cycle":0,"reason":"function"}` {
				t.Errorf("JSON %s", j)
			}
		})
	}
}

// Every path that ends the held keys leaves none behind, and the function key,
// which is physical state, is kept by all of them but Reset and a Reload that
// changes it (ARCHITECTURE §7.4).
func TestFunctionKeyExitPaths(t *testing.T) {
	prog := compileLayers(t, fnProfile)
	plain := compileLayers(t, `{"schemaVersion":1,"name":"plain","layers":{"base":{"south":{"press":{"type":"keys","keys":"A"}}}}}`)
	other := compileFn(t, fnProfile, control.Guide)
	for _, tc := range []struct {
		name   string
		exit   func(e *engine.Engine, l *ledger, now int)
		active control.Control // after the exit, with start still down
	}{
		{"CancelAll", func(e *engine.Engine, l *ledger, now int) { e.CancelAll(timeAt(now)); l.releaseAll() }, control.Start},
		{"Pause and Resume", func(e *engine.Engine, l *ledger, now int) {
			e.Pause(timeAt(now))
			l.releaseAll()
			e.Resume()
		}, control.Start},
		{"Reload", func(e *engine.Engine, l *ledger, now int) { e.Reload(timeAt(now), prog); l.releaseAll() }, control.Start},
		{"Reload of a program without sets", func(e *engine.Engine, l *ledger, now int) { e.Reload(timeAt(now), plain); l.releaseAll() }, control.Start},
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
			step(control.Start, true, 0)
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
			// The releases of what was held do nothing more.
			step(control.South, false, 20)
			step(control.East, false, 21)
			if len(l.held) != 0 {
				t.Fatalf("%v held after the releases", l.held)
			}
			step(control.South, true, 30)
			step(control.South, false, 35)
			step(control.Start, false, 40)
			if got := e.ActiveFunctionKey(); got != control.Invalid {
				t.Errorf("function key held %v after the release of start", got)
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
			runM2(t, e, r, "+start@0 +south@10")
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
		runM2(t, e, r, "+start@0 pause@5 +south@10 -south@15 -start@20 +south@25 -south@30 resume@35 +south@40 -south@45")
		if got, want := r.String(), "@40 down decl/3 A, @45 up decl/3 A"; got != want {
			t.Errorf("sent %q; want %q", got, want)
		}
	})
	t.Run("a function key is physical state", func(t *testing.T) {
		r := &recorder{}
		e := engine.New(prog, r, engine.Options{})
		runM2(t, e, r, "+start@0 cancel@5 -start@10 +south@15 -south@20")
		if got, want := r.String(), "@15 down decl/1 A, @20 up decl/1 A"; got != want {
			t.Errorf("sent %q; want %q", got, want)
		}
	})
}

// A set is part of the program: Reload takes the new one, and bindings the old
// set had are gone with it.
func TestFunctionKeyReloadChangesTheSet(t *testing.T) {
	const next = `{"schemaVersion":1,"name":"fn2","layers":{"base":{"south":{"press":{"type":"keys","keys":"A"}}}},
 "functionSet":{"south":{"press":{"type":"keys","keys":"F9"}}}}`
	r := &recorder{}
	e := engine.New(compileLayers(t, fnProfile), r, engine.Options{})
	runM2(t, e, r, "+start@0")
	e.Reload(timeAt(5), compileLayers(t, next))
	runM2(t, e, r, "+south@10 -south@20")
	if got, want := r.String(), "@10 down decl/1 F9, @20 up decl/1 F9"; got != want {
		t.Errorf("sent %q; want %q", got, want)
	}
}

// A Reload that changes the function key (config.json's functionKey, ADR-0075)
// ends what is held like any Reload, forgets that the old key is held, and from
// then on the new key is the function key and the old one an ordinary control.
// Nothing is left held: every Up the ledger sees has its Down (invariant 1).
func TestFunctionKeyReloadChangesTheKey(t *testing.T) {
	start := compileLayers(t, fnProfile)
	guide := compileFn(t, fnProfile, control.Guide)
	newLedger := func(t *testing.T, prog *engine.Program) (*engine.Engine, *ledger, func(c control.Control, down bool, at int)) {
		l := &ledger{t: t, held: map[ledgerKey]int{}, ctl: map[uint64]control.Control{}, record: true}
		e := engine.New(prog, l, engine.Options{})
		l.e = e
		return e, l, func(c control.Control, down bool, at int) {
			l.down[c] = down
			e.Step(engine.Input{T: timeAt(at), Kind: engine.InputButton, Control: c, Down: down})
		}
	}
	t.Run("held through the old set and the old key", func(t *testing.T) {
		e, l, step := newLedger(t, start)
		step(control.Start, true, 0)
		step(control.South, true, 10) // F1 of the set
		step(control.East, true, 11)  // B
		if len(l.held) != 2 {
			t.Fatalf("held %v; want F1 and B", l.held)
		}
		e.Reload(timeAt(15), guide)
		l.releaseAll() // what the slot's ReleaseOwners does after a reload
		if got := e.ActiveFunctionKey(); got != control.Invalid {
			t.Errorf("function key held %v after the reload; want none", got)
		}
		// The releases do nothing: of the two keys held and of the old
		// function key, which is an ordinary control now and had no cycle.
		step(control.South, false, 20)
		step(control.East, false, 21)
		step(control.Start, false, 22)
		if len(l.held) != 0 || l.cmds != 2 {
			t.Fatalf("after the releases: %d commands, %v held; want the two Downs only", l.cmds, l.held)
		}
		// start is an ordinary control now: its base binding works. guide is the
		// function key: its edges send nothing and it selects the set.
		step(control.Start, true, 30)
		step(control.Start, false, 31)
		step(control.Guide, true, 40)
		step(control.South, true, 41)
		step(control.South, false, 42)
		step(control.Guide, false, 43)
		step(control.South, true, 50)
		step(control.South, false, 51)
		if want := []string{"F1", "B", "S", "F1", "A"}; !slices.Equal(l.sent, want) {
			t.Errorf("Downs %q; want %q", l.sent, want)
		}
		if len(l.held) != 0 {
			t.Errorf("%v held at the end", l.held)
		}
	})
	t.Run("the new key was down as an ordinary control", func(t *testing.T) {
		e, l, step := newLedger(t, start)
		step(control.Guide, true, 0) // G, held
		step(control.South, true, 5) // A, held
		if len(l.held) != 2 {
			t.Fatalf("held %v; want G and A", l.held)
		}
		e.Reload(timeAt(10), guide)
		l.releaseAll()
		if got := e.ActiveFunctionKey(); got != control.Invalid {
			t.Errorf("function key held %v after the reload although guide is down; want none", got)
		}
		// A press while guide is still down does not use the set: the key
		// selects it from its next press.
		step(control.South, false, 12)
		step(control.South, true, 14)
		step(control.South, false, 15)
		step(control.Guide, false, 16)
		step(control.Guide, true, 20)
		step(control.South, true, 21)
		step(control.South, false, 22)
		step(control.Guide, false, 23)
		if len(l.held) != 0 {
			t.Fatalf("%v held at the end", l.held)
		}
		want := []string{"G", "A", "A", "F1"}
		if !slices.Equal(l.sent, want) {
			t.Errorf("Downs %q; want %q", l.sent, want)
		}
	})
	t.Run("a pending gesture is dropped", func(t *testing.T) {
		e, l, step := newLedger(t, start)
		step(control.West, true, 0) // pending: tap or hold
		step(control.Start, true, 5)
		step(control.South, true, 6) // buffered behind west
		e.Reload(timeAt(10), guide)
		l.releaseAll()
		if d, ok := e.NextDeadline(); ok {
			t.Errorf("a deadline at %v is left", time.Duration(d))
		}
		step(control.West, false, 20)
		step(control.South, false, 21)
		step(control.Start, false, 22)
		if l.cmds != 0 || len(l.held) != 0 {
			t.Errorf("%d commands, %v held; want none", l.cmds, l.held)
		}
	})
}

// The key hints (HUD-03) show the set while the function key is held, and the
// normal bindings for the controls the set lacks. A center key that is not the
// function key is an ordinary control and is listed; the function key is not,
// whatever its layers bind on it.
func TestFunctionKeyHints(t *testing.T) {
	const src = `{"schemaVersion":1,"name":"hints","layers":{"base":{
 "rightshoulder":{"label":"hints","press":{"type":"hud","op":"hints"}},
 "south":{"label":"Undo","press":{"type":"keys","keys":"Ctrl+Z"}},
 "east":{"press":{"type":"keys","keys":"Space"}},
 "west":{"press":{"type":"keys","keys":"W"}},
 "back":{"label":"Paste","press":{"type":"keys","keys":"Ctrl+V"}},
 "start":{"label":"Save","press":{"type":"keys","keys":"Ctrl+S"}},
 "dpad.up":{"label":"Up","press":{"type":"keys","keys":"U"}}}},
 "functionSet":{
   "south":{"label":"Redo","press":{"type":"keys","keys":"Ctrl+Y"}},
   "north":{"press":{"type":"keys","keys":"F5"}},
   "west":{"block":true},
   "east":{"label":"Fill","press":{"type":"keys","keys":"F"}},
   "back":{"label":"Copy","press":{"type":"keys","keys":"Ctrl+C"}},
   "start":{"label":"Never","press":{"type":"keys","keys":"Q"}},
   "dpad.up":{"press":{"type":"keys","keys":"F6"}}}}`
	const normal = "on:south=Undo | east=Space | west=W | back=Paste | dpad.up=Up"
	const underSet = "on:south=Redo | east=Fill | north=F5 | back=Copy | dpad.up=F6"
	t.Run("the key is held first", func(t *testing.T) {
		h := newHsim(t, src)
		h.press("start")
		h.wantShown("a function key, no hints", "off")
		h.press("rightshoulder")
		h.wantShown("hints under the key", underSet)
		h.release("start")
		h.wantShown("the key let go", normal)
		h.release("rightshoulder")
		h.wantShown("hints let go", "off")
	})
	t.Run("the hints are on first", func(t *testing.T) {
		h := newHsim(t, src)
		h.press("rightshoulder")
		h.wantShown("normal", normal)
		h.press("start")
		h.wantShown("the key held", underSet)
		h.release("start")
		h.wantShown("the key let go", normal)
		h.release("rightshoulder")
		h.wantShown("hints let go", "off")
	})
	t.Run("another function key", func(t *testing.T) {
		out := &recorder{}
		h := &hsim{t: t, e: engine.New(compileFn(t, src, control.Back), out, engine.Options{}), out: out}
		h.press("rightshoulder")
		// back is the function key now: not listed; start is an ordinary control.
		h.wantShown("normal", "on:south=Undo | east=Space | west=W | start=Save | dpad.up=Up")
		h.press("back")
		h.wantShown("the key held", "on:south=Redo | east=Fill | north=F5 | start=Never | dpad.up=F6")
	})
	t.Run("sends nothing", func(t *testing.T) {
		h := newHsim(t, src)
		h.press("rightshoulder")
		h.press("start")
		h.release("start")
		h.release("rightshoulder")
		if h.out.String() != "" {
			t.Errorf("sent %q", h.out.String())
		}
	})
	t.Run("a copy", func(t *testing.T) {
		h := newHsim(t, src)
		h.press("rightshoulder")
		h.press("start")
		for _, hint := range h.e.View().Hints {
			if hint.Control == control.Start {
				t.Errorf("the hints list the function key %s", hint.Control)
			}
		}
	})
}

// A script (SCR-05) sees no function key: a handler on it never runs, and
// Options.Button is not asked about it; pad.isDown still reads it
// (ControlDown). The other center keys are ordinary: their handlers run. A
// global() handler is above the set, a base handler is below it.
func TestFunctionKeysAndScripts(t *testing.T) {
	const src = `{"schemaVersion":1,"name":"fn","script":{"entry":"main.ts","api":"0.21"},"layers":{"base":{
 "south":{"press":{"type":"keys","keys":"A"}},
 "east":{"press":{"type":"keys","keys":"B"}}}},
 "functionSet":{"south":{"press":{"type":"keys","keys":"F1"}},"north":{"press":{"type":"keys","keys":"F2"}}}}`
	entry := func(c control.Control, scope string) engine.GestureEntry {
		return engine.GestureEntry{ID: 1, Control: c, Gesture: engine.GesturePress, Scope: scope}
	}
	t.Run("handlers on the function key never run", func(t *testing.T) {
		r := &recorder{}
		g := &gestureRegistry{entries: []engine.GestureEntry{entry(control.Start, "global"), entry(control.Start, "base")}}
		var buttons []engine.ButtonEvent
		e := engine.New(compileLayers(t, src), r, engine.Options{Gestures: g, Button: func(ev engine.ButtonEvent) bool { buttons = append(buttons, ev); return false }})
		run(t, e, r, "+start@0 -start@10")
		if len(g.events) != 0 || len(g.ends) != 0 || len(buttons) != 0 || r.String() != "" {
			t.Errorf("handler events %+v, ends %+v, buttons %+v, sent %q", g.events, g.ends, buttons, r.String())
		}
		if e.ControlDown(control.Start) {
			t.Error("the function key reads as down after its release")
		}
		run(t, e, r, "+start@20")
		if !e.ControlDown(control.Start) {
			t.Error("ControlDown does not report a held function key")
		}
	})
	t.Run("the other center keys are ordinary", func(t *testing.T) {
		r := &recorder{}
		g := &gestureRegistry{entries: []engine.GestureEntry{entry(control.Back, "base"), entry(control.Guide, "global")}}
		var buttons []engine.ButtonEvent
		e := engine.New(compileLayers(t, src), r, engine.Options{Gestures: g, Button: func(ev engine.ButtonEvent) bool { buttons = append(buttons, ev); return false }})
		run(t, e, r, "+back@0 -back@10 +guide@20 -guide@30")
		if len(g.events) != 2 || len(buttons) != 4 {
			t.Errorf("handler events %+v, buttons %+v; want a handler event and a button event for each of back and guide", g.events, buttons)
		}
	})
	t.Run("global() wins over the set", func(t *testing.T) {
		r := &recorder{}
		g := &gestureRegistry{entries: []engine.GestureEntry{entry(control.South, "global")}}
		e := engine.New(compileLayers(t, src), r, engine.Options{Gestures: g})
		run(t, e, r, "+start@0 +south@10 -south@20 -start@30")
		if len(g.events) != 1 || r.String() != "" {
			t.Errorf("handler events %+v, sent %q; want the handler to take it", g.events, r.String())
		}
		// ... and when it passes, the set is next.
		r, g.events, g.pass = &recorder{}, nil, true
		e = engine.New(compileLayers(t, src), r, engine.Options{Gestures: g})
		run(t, e, r, "+start@0 +south@10 -south@20 -start@30")
		if len(g.events) != 1 || r.String() != "@10 down decl/1 F1, @20 up decl/1 F1" {
			t.Errorf("handler events %+v, sent %q; want the set's F1 after the pass", g.events, r.String())
		}
	})
	t.Run("a base handler is below the set", func(t *testing.T) {
		r := &recorder{}
		g := &gestureRegistry{entries: []engine.GestureEntry{entry(control.South, "base"), entry(control.East, "base"), entry(control.North, "base")}}
		e := engine.New(compileLayers(t, src), r, engine.Options{Gestures: g})
		// south and north are in the set: no handler. east is not: it runs.
		run(t, e, r, "+start@0 +south@10 -south@20 +north@30 -north@40 +east@50 -east@60 -start@70")
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
 "functionSet":{"south":{"press":{"type":"script","name":"act"}}}}`
		r := &recorder{}
		var events []engine.ScriptActionEvent
		g := &gestureRegistry{}
		e := engine.New(compileLayers(t, withScript), r, engine.Options{Gestures: g, Script: func(ev engine.ScriptActionEvent) { events = append(events, ev) }})
		run(t, e, r, "+start@0 +south@10 -south@20 -start@30")
		if len(events) != 1 || events[0].Name != "act" || events[0].Scope != "global" {
			t.Errorf("script action events %+v; want act with the scope global", events)
		}
	})
	t.Run("InputScope of a control the set binds", func(t *testing.T) {
		e := engine.New(compileLayers(t, src), &recorder{}, engine.Options{})
		if got := e.InputScope(control.South); got != "base" {
			t.Errorf("InputScope(south) = %q before the key", got)
		}
		e.Step(engine.Input{Kind: engine.InputButton, Control: control.Start, Down: true})
		if got := e.InputScope(control.South); got != "global" {
			t.Errorf("InputScope(south) = %q under the key; want global", got)
		}
		if got := e.InputScope(control.East); got != "base" {
			t.Errorf("InputScope(east) = %q under the key; want base", got)
		}
	})
}

// The Button callback is not asked about the function key, and a press the
// callback consumes is not looked up either.
func TestFunctionKeyButtonCallback(t *testing.T) {
	var asked []control.Control
	r := &recorder{}
	e := engine.New(compileLayers(t, fnProfile), r, engine.Options{Button: func(ev engine.ButtonEvent) bool {
		asked = append(asked, ev.Control)
		return ev.Control == control.South
	}})
	run(t, e, r, "+start@0 +south@10 -south@20 +east@30 -east@40 -start@50")
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
	runM2(t, e, r, "+south@0 -south@5 +start@10 +south@15 -south@20 +east@25 -east@30 -start@35")
	want := "@0 down decl/1 R, @5 up decl/1 R, @15 down decl/2 F1, @20 up decl/2 F1, @25 down decl/3 S, @30 up decl/3 S"
	if got := r.String(); got != want {
		t.Errorf("sent %q; want %q", got, want)
	}
}

// A binding on the function key in a layer, in common or in the set is not
// compiled, whatever it holds, and is listed for the log; the other center keys
// are compiled like any control. The set is listed with its own level name.
func TestCompileFunctionKeys(t *testing.T) {
	keysAction := func(id, k string) *profile.Action {
		return &profile.Action{ID: id, Type: profile.ActionKeys, Mode: profile.ModeHold, Keys: keys.MustParse(k)}
	}
	bad := &profile.Binding{ID: "/layers/base/start", Control: control.Start, Press: &profile.Action{ID: "/layers/base/start/press", Type: profile.ActionType(250)}}
	back := &profile.Binding{ID: "/layers/base/back", Control: control.Back, Press: keysAction("/layers/base/back/press", "V")}
	other := &profile.Binding{ID: "/common/start", Control: control.Start, Press: keysAction("/common/start/press", "Q")}
	south := &profile.Binding{ID: "/layers/base/south", Control: control.South, Press: keysAction("/layers/base/south/press", "A")}
	inSet := &profile.Binding{ID: "/functionSet/south", Control: control.South, Press: keysAction("/functionSet/south/press", "F1")}
	startInSet := &profile.Binding{ID: "/functionSet/start", Control: control.Start, Press: &profile.Action{ID: "/functionSet/start/press", Type: profile.ActionType(250)}}
	prog, err := engine.Compile(&profile.Profile{
		Layers:      []*profile.Layer{{Name: "base", Bindings: []*profile.Binding{south, back, bad}}},
		Common:      &profile.Layer{Name: "common", Bindings: []*profile.Binding{other}},
		FunctionSet: &profile.Layer{Name: "function", Bindings: []*profile.Binding{inSet, startInSet}},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	var listed []string
	for _, bi := range prog.Bindings() {
		listed = append(listed, bi.Layer+" "+bi.Binding.ID)
	}
	want := []string{"base /layers/base/south", "base /layers/base/back", "function:start /functionSet/south"}
	if !slices.Equal(listed, want) {
		t.Errorf("Bindings() = %q; want %q", listed, want)
	}
	if got, want := prog.IgnoredFunctionKeyBindings(), []string{"/layers/base/start", "/common/start", "/functionSet/start"}; !slices.Equal(got, want) {
		t.Errorf("IgnoredFunctionKeyBindings() = %q; want %q", got, want)
	}
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{})
	runM2(t, e, r, "+start@0 -start@1 +back@2 -back@3 +south@4 -south@5")
	if got, want := r.String(), "@2 down decl/1 V, @3 up decl/1 V, @4 down decl/2 A, @5 up decl/2 A"; got != want {
		t.Errorf("sent %q; want %q", got, want)
	}
	// A binding that is not the function key's is checked like a layer's, in a
	// set too: a hold of a layer on a tap.
	_, err = engine.Compile(&profile.Profile{
		Layers: []*profile.Layer{{Name: "base"}},
		FunctionSet: &profile.Layer{Name: "function", Bindings: []*profile.Binding{
			{ID: "/a", Control: control.South, Tap: &profile.Action{ID: "/a/tap", Type: profile.ActionLayer, Layer: "fn", Op: "hold"}},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "layer hold action is only valid for press/hold") {
		t.Errorf("Compile of a set with a hold on a tap: %v", err)
	}
	// ... and so is a control bound twice.
	_, err = engine.Compile(&profile.Profile{
		Layers: []*profile.Layer{{Name: "base"}},
		FunctionSet: &profile.Layer{Name: "function", Bindings: []*profile.Binding{
			{ID: "/a", Control: control.South}, {ID: "/b", Control: control.South},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "already bound") {
		t.Errorf("Compile of a set that binds south twice: %v", err)
	}
}

// A profile in the old format has its sets merged for the engine, whichever the
// function key: that is what the model says (EffectiveFunctionSet), and the
// level of the decisions is the same function:<key>.
func TestCompileOldFunctionKeys(t *testing.T) {
	const old = `{"schemaVersion":1,"name":"old","layers":{"base":{}},
 "functionKeys":{"back":{"south":{"press":{"type":"keys","keys":"F1"}},"east":{"press":{"type":"keys","keys":"F2"}}},
  "start":{"south":{"press":{"type":"keys","keys":"F4"}}}}}`
	prog := compileLayers(t, old)
	var listed []string
	for _, bi := range prog.Bindings() {
		listed = append(listed, bi.Layer+" "+bi.Binding.ID)
	}
	want := []string{"function:start /functionKeys/start/south", "function:start /functionKeys/back/east"}
	if !slices.Equal(listed, want) {
		t.Errorf("Bindings() = %q; want %q", listed, want)
	}
}

// A multi-tap window belongs to the binding that opened it. When the function
// key goes down or up between two taps, the second press finds another binding
// (the press looks up when it arrives), so it is not the next tap of that
// window: the window is committed first, as another control's press does, and
// the press starts a new count. Nothing the first tap was going to send is lost
// and the taps of two bindings are never added up.
func TestFunctionKeyBetweenTaps(t *testing.T) {
	const (
		// The set rebinds south as a plain press.
		press = `{"schemaVersion":1,"name":"fn","layers":{"base":{
 "south":{"tap":{"type":"keys","keys":"F13","mode":"tap"},"doubleTap":{"type":"keys","keys":"F14","mode":"tap"}}}},
 "functionSet":{"south":{"press":{"type":"keys","keys":"F15"}}}}`
		// The set blocks south.
		block = `{"schemaVersion":1,"name":"fn","layers":{"base":{
 "south":{"tap":{"type":"keys","keys":"F13","mode":"tap"},"doubleTap":{"type":"keys","keys":"F14","mode":"tap"}}}},
 "functionSet":{"south":{"block":true}}}`
		// The set rebinds south as a tap and a double tap of its own.
		multi = `{"schemaVersion":1,"name":"fn","layers":{"base":{
 "south":{"tap":{"type":"keys","keys":"F13","mode":"tap"},"doubleTap":{"type":"keys","keys":"F14","mode":"tap"}}}},
 "functionSet":{"south":{"tap":{"type":"keys","keys":"F15","mode":"tap"},"doubleTap":{"type":"keys","keys":"F16","mode":"tap"}}}}`
		// The set does not bind south.
		other = `{"schemaVersion":1,"name":"fn","layers":{"base":{
 "south":{"tap":{"type":"keys","keys":"F13","mode":"tap"},"doubleTap":{"type":"keys","keys":"F14","mode":"tap"}}}},
 "functionSet":{"east":{"press":{"type":"keys","keys":"F15"}}}}`
	)
	for _, tc := range []struct{ name, src, steps, want string }{
		{
			"the tap before the key is committed",
			press, "+south@10 -south@20 +start@30 +south@40 -south@50 -start@60 wait@1000",
			"@40 tap decl/1 F13 20ms, @40 down decl/2 F15, @50 up decl/2 F15",
		},
		{
			"the tap under the key is committed when it goes",
			multi, "+start@0 +south@10 -south@20 -start@30 +south@40 -south@50 wait@1000",
			"@40 tap decl/1 F15 20ms, @270 tap decl/2 F13 20ms",
		},
		{
			"the tap before a block is committed",
			block, "+south@10 -south@20 +start@30 +south@40 -south@50 -start@60 wait@1000",
			"@40 tap decl/1 F13 20ms",
		},
		{
			// The control south is not bound by the set, so it goes on as the
			// same binding: a double tap of base's.
			"the key does not change the binding",
			other, "+start@0 +south@10 -south@20 +south@40 -south@50 -start@60 wait@1000",
			"@50 tap decl/1 F14 20ms",
		},
		{
			"the key held through both taps",
			multi, "+start@0 +south@10 -south@20 +south@40 -south@50 -start@60 wait@1000",
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

// View says whether the function key is held, for the explanation of a mapping
// (ADR-0071): control.Invalid when it is not, the key when it is, and none
// again after Reset. Another center key changes nothing.
func TestViewFunctionKey(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileLayers(t, fnProfile), r, engine.Options{})
	if got := e.View().FunctionKey; got != control.Invalid {
		t.Errorf("FunctionKey = %v before any press", got)
	}
	run(t, e, r, "+back@0")
	if got := e.View().FunctionKey; got != control.Invalid {
		t.Errorf("FunctionKey = %v with back held, an ordinary control", got)
	}
	run(t, e, r, "+start@5")
	if got := e.View().FunctionKey; got != control.Start {
		t.Errorf("FunctionKey = %v with start held, want start", got)
	}
	run(t, e, r, "-start@10")
	if got := e.View().FunctionKey; got != control.Invalid {
		t.Errorf("FunctionKey = %v after start is let go", got)
	}
	run(t, e, r, "+start@12")
	e.Reset(clock.Time(20 * time.Millisecond))
	if got := e.View().FunctionKey; got != control.Invalid {
		t.Errorf("FunctionKey = %v after Reset", got)
	}
}
