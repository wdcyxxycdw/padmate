package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
)

// A multi-tap window that ends with no action for the number of taps in it
// leaves one decision (ADR-0071, ENG-20): the press only started a wait, and
// without it the explanation of the press ("waiting for a tap, hold or
// multi-tap") would never be answered. The decision is unbound, with the
// gesture of the count, the binding and level the press resolved, and no
// action, output or owner.

const (
	westDoubleOnly = `"west":{"doubleTap":{"type":"keys","keys":"F14"}}`
	westTapTriple  = `"west":{"tap":{"type":"keys","keys":"F13"},"tripleTap":{"type":"keys","keys":"F15"}}`
	westQuadOnly   = `"west":{"quadTap":{"type":"keys","keys":"F16"}}`
	westTapDouble  = `"west":{"tap":{"type":"keys","keys":"F13"},"doubleTap":{"type":"keys","keys":"F14"}}`
	westDoubleHold = `"west":{"doubleTap":{"type":"keys","keys":"F14"},"hold":{"type":"keys","keys":"E"}}`
)

// unmatchedTrace steps prog through steps (runM2: every deadline on the way
// runs at its time) and returns the decisions, as scriptBrief writes them,
// and what the engine sent.
func unmatchedTrace(t *testing.T, prog *engine.Program, opts engine.Options, steps string) (decisions []string, sent string) {
	t.Helper()
	r := &recorder{}
	opts.Trace = func(d engine.Decision) { decisions = append(decisions, scriptBrief(d)) }
	runM2(t, engine.New(prog, r, opts), r, steps)
	return decisions, r.String()
}

func TestUnmatchedMultiTapDecision(t *testing.T) {
	const west = "level=base binding=/layers/base/west"
	for _, tc := range []struct {
		name     string
		bindings []string
		steps    string
		want     []string
		sent     string
	}{
		{
			name:     "a double tap only, tapped once: the window ends",
			bindings: []string{westDoubleOnly},
			steps:    "+west@0 -west@10 wait@1000",
			want: []string{
				"@0s west down press unbound " + west,
				"@230ms west down tap unbound " + west,
			},
		},
		{
			name:     "a double tap only, tapped once: another control's press flushes the window",
			bindings: []string{westDoubleOnly, southTap},
			steps:    "+west@0 -west@10 +south@40 -south@50",
			want: []string{
				"@0s west down press unbound " + west,
				"@40ms west down tap unbound " + west,
				"@40ms south down press dispatch level=base binding=/layers/base/south",
				"@50ms south up press end level=base binding=/layers/base/south",
			},
			sent: "@40 tap decl/2 Ctrl+Z 20ms",
		},
		{
			name:     "a tap and a triple tap, tapped twice",
			bindings: []string{westTapTriple},
			steps:    "+west@0 -west@10 +west@60 -west@70 wait@1000",
			want: []string{
				"@0s west down press unbound " + west,
				"@60ms west down press unbound " + west,
				"@290ms west down doubleTap unbound " + west,
			},
		},
		{
			name:     "a quad tap only, tapped three times",
			bindings: []string{westQuadOnly},
			steps:    "+west@0 -west@10 +west@60 -west@70 +west@120 -west@130 wait@1000",
			want: []string{
				"@0s west down press unbound " + west,
				"@60ms west down press unbound " + west,
				"@120ms west down press unbound " + west,
				"@350ms west down tripleTap unbound " + west,
			},
		},
		{
			name:     "a double tap and a hold, released before the hold",
			bindings: []string{westDoubleHold},
			steps:    "+west@0 -west@100 wait@1000",
			want: []string{
				"@0s west down press unbound " + west,
				"@320ms west down tap unbound " + west,
			},
		},
		{
			name:     "two windows end by the press of a third control",
			bindings: []string{westDoubleOnly, `"north":{"tripleTap":{"type":"keys","keys":"F17"}}`, southTap},
			steps:    "+west@0 +north@5 -west@10 -north@15 +south@40 -south@50",
			want: []string{
				"@0s west down press unbound " + west,
				"@5ms north down press unbound level=base binding=/layers/base/north",
				// Both windows are open at south's press; they end in control order.
				"@40ms west down tap unbound " + west,
				"@40ms north down tap unbound level=base binding=/layers/base/north",
				"@40ms south down press dispatch level=base binding=/layers/base/south",
				"@50ms south up press end level=base binding=/layers/base/south",
			},
			sent: "@40 tap decl/3 Ctrl+Z 20ms",
		},
		// The counts that have an action are decided as before: one decision
		// for the tap, none more.
		{
			name:     "a tap and a double tap, tapped once: the tap",
			bindings: []string{westTapDouble},
			steps:    "+west@0 -west@10 wait@1000",
			want: []string{
				"@0s west down press unbound " + west,
				"@230ms west down tap dispatch " + west,
			},
			sent: "@230 tap decl/1 F13 20ms",
		},
		{
			name:     "a double tap only, tapped twice: the double tap",
			bindings: []string{westDoubleOnly},
			steps:    "+west@0 -west@10 +west@60 -west@70 wait@1000",
			want: []string{
				"@0s west down press unbound " + west,
				"@60ms west down press unbound " + west,
				"@70ms west down doubleTap dispatch " + west,
			},
			sent: "@70 tap decl/1 F14 20ms",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, sent := unmatchedTrace(t, compile(t, tc.bindings...), engine.Options{}, tc.steps)
			if !slices.Equal(got, tc.want) {
				t.Errorf("decisions\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
			if sent != tc.sent {
				t.Errorf("sent %q, want %q", sent, tc.sent)
			}
		})
	}
}

// The decision is a value of the cycle of the press: its cycle and binding,
// edge down, the gesture of the count, and nothing it did. The cycle counter
// goes on as if nothing had happened.
func TestUnmatchedMultiTapDecisionFields(t *testing.T) {
	r := &recorder{}
	var got []engine.Decision
	e := engine.New(compile(t, westDoubleOnly, southTap), r, engine.Options{Trace: func(d engine.Decision) { got = append(got, d) }})
	runM2(t, e, r, "+west@0 -west@10 wait@1000 +south@1100 -south@1110")
	if len(got) != 4 {
		t.Fatalf("%d decisions: %v", len(got), got)
	}
	press, commit, next := got[0], got[1], got[2]
	if commit.Reason != engine.ReasonUnbound || commit.Edge != engine.EdgeDown || commit.Gesture != engine.GestureTap {
		t.Errorf("decision %s", commit)
	}
	if commit.Cycle != press.Cycle || commit.Control != control.West || commit.Binding != press.Binding || commit.Level != "base" {
		t.Errorf("decision %s does not describe the press %s", commit, press)
	}
	if commit.Action != nil || commit.Owner != (engine.OwnerID{}) {
		t.Errorf("decision %s carries an action or an owner", commit)
	}
	if want := `{"t":230,"control":"west","edge":"down","cycle":1,"gesture":"tap","level":"base","binding":"/layers/base/west","reason":"unbound"}`; commit.String() != want {
		t.Errorf("JSON %s, want %s", commit, want)
	}
	if next.Cycle != press.Cycle+1 {
		t.Errorf("the next cycle is %d after %d", next.Cycle, press.Cycle)
	}
	if r.String() != "@1100 tap decl/2 Ctrl+Z 20ms" {
		t.Errorf("sent %q", r.String())
	}
}

// A script's gesture registry decides the same: a multi-tap commit is a
// gesture the script is offered, and when nothing answers it, it is unbound
// (the binding of the resolution), or the script's when a handler or a claim
// took it.
func TestUnmatchedMultiTapDecisionUnderScript(t *testing.T) {
	entry := func(g engine.Gesture, scope string) engine.GestureEntry {
		return engine.GestureEntry{ID: 1, Control: control.West, Gesture: g, Scope: scope}
	}
	const resolution = "binding=script-resolution"
	for _, tc := range []struct {
		name     string
		bindings []string
		entries  []engine.GestureEntry
		pass     bool
		steps    string
		want     []string
	}{
		{
			name:     "nothing answers a single tap",
			bindings: []string{westDoubleOnly},
			steps:    "+west@0 -west@10 wait@1000",
			want: []string{
				"@0s west down press unbound " + resolution,
				"@230ms west down tap unbound " + resolution,
			},
		},
		{
			name:     "a handler of the double tap: a single tap is unbound",
			bindings: nil,
			entries:  []engine.GestureEntry{entry(engine.GestureDoubleTap, "base")},
			steps:    "+west@0 -west@10 wait@1000",
			want: []string{
				"@0s west down press unbound " + resolution,
				"@230ms west down tap unbound " + resolution,
			},
		},
		{
			name:     "a handler of the double tap takes the double tap",
			bindings: nil,
			entries:  []engine.GestureEntry{entry(engine.GestureDoubleTap, "base")},
			steps:    "+west@0 -west@10 +west@60 -west@70 wait@1000",
			want: []string{
				"@0s west down press unbound " + resolution,
				"@60ms west down press unbound " + resolution,
				"@70ms west down doubleTap script",
			},
		},
		{
			name:     "a handler of the triple tap, tapped twice",
			bindings: []string{westTapTriple},
			entries:  []engine.GestureEntry{entry(engine.GestureTripleTap, "base")},
			steps:    "+west@0 -west@10 +west@60 -west@70 wait@1000",
			want: []string{
				"@0s west down press unbound " + resolution,
				"@60ms west down press unbound " + resolution,
				"@290ms west down doubleTap unbound " + resolution,
			},
		},
		{
			name:     "the declared tap is not unbound",
			bindings: []string{westTapDouble},
			steps:    "+west@0 -west@10 wait@1000",
			want: []string{
				"@0s west down press unbound " + resolution,
				"@230ms west down tap dispatch level=base binding=/layers/base/west",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			var got []string
			e := engine.New(compile(t, tc.bindings...), r, engine.Options{
				Gestures: &gestureRegistry{entries: tc.entries, pass: tc.pass},
				Trace:    func(d engine.Decision) { got = append(got, scriptBrief(d)) },
			})
			runM2(t, e, r, tc.steps)
			if !slices.Equal(got, tc.want) {
				t.Errorf("decisions\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
	t.Run("a claim of the script below the declaration takes the press", func(t *testing.T) {
		// fn is a layer held by dpad.down. It declares the double tap of west; the
		// script claims west in the base scope, below it, so the press that nothing
		// in fn answers goes to the claim, and the claim answers the cycle: the single
		// tap that ends the window has nothing more to say.
		const src = `{"schemaVersion":1,"name":"claim","layers":{
 "base":{"dpad.down":{"press":{"type":"layer","layer":"fn","op":"hold"}}},
 "fn":{"west":{"doubleTap":{"type":"keys","keys":"F14"}}}}}`
		r := &recorder{}
		var got []string
		e := engine.New(compileLayers(t, src), r, engine.Options{
			Gestures: &gestureRegistry{entries: []engine.GestureEntry{{ID: 1, Control: control.West, Scope: "base", Claim: true}}},
			Trace:    func(d engine.Decision) { got = append(got, scriptBrief(d)) },
		})
		runM2(t, e, r, "+dpad.down@0 +west@10 -west@20 wait@1000")
		want := []string{
			"@0s dpad.down down press dispatch level=base binding=/layers/base/dpad.down",
			"@10ms west down press script",
		}
		if !slices.Equal(got, want) {
			t.Errorf("decisions\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		if r.String() != "" {
			t.Errorf("sent %q", r.String())
		}
	})
	t.Run("a block below the declaration blocks the press", func(t *testing.T) {
		// The same layer declares the double tap of west over a base that blocks
		// west: neither the press nor the single tap finds an action, and the
		// block answers the cycle at the press.
		const src = `{"schemaVersion":1,"name":"block","layers":{
 "base":{"dpad.down":{"press":{"type":"layer","layer":"fn","op":"hold"}},"west":{"block":true}},
 "fn":{"west":{"doubleTap":{"type":"keys","keys":"F14"}}}}}`
		r := &recorder{}
		var got []string
		e := engine.New(compileLayers(t, src), r, engine.Options{
			Gestures: &gestureRegistry{},
			Trace:    func(d engine.Decision) { got = append(got, scriptBrief(d)) },
		})
		runM2(t, e, r, "+dpad.down@0 +west@10 -west@20 wait@1000")
		want := []string{
			"@0s dpad.down down press dispatch level=base binding=/layers/base/dpad.down",
			"@10ms west down press blocked level=base binding=/layers/base/west",
		}
		if !slices.Equal(got, want) || r.String() != "" {
			t.Errorf("decisions\n%s\nwant\n%s\nsent %q", strings.Join(got, "\n"), strings.Join(want, "\n"), r.String())
		}
	})
}

// Under a function key the window belongs to the binding that opened it: the
// press of the key's set ends it as another control's press does, and the
// window with no action for its count is decided first.
func TestUnmatchedMultiTapDecisionBeforeAFunctionKeyPress(t *testing.T) {
	const src = `{"schemaVersion":1,"name":"fn","layers":{"base":{
 "south":{"doubleTap":{"type":"keys","keys":"F14","mode":"tap"}}}},
 "functionKeys":{"back":{"south":{"press":{"type":"keys","keys":"F15"}}}}}`
	got, sent := unmatchedTrace(t, compileLayers(t, src), engine.Options{}, "+south@10 -south@20 +back@30 +south@40 -south@50 -back@60 wait@1000")
	want := []string{
		"@10ms south down press unbound level=base binding=/layers/base/south",
		"@30ms back down function",
		"@40ms south down tap unbound level=base binding=/layers/base/south",
		"@40ms south down press dispatch level=function:back binding=/functionKeys/back/south",
		"@50ms south up press release level=function:back binding=/functionKeys/back/south",
		"@60ms back up function",
	}
	if !slices.Equal(got, want) {
		t.Errorf("decisions\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if sent != "@40 down decl/2 F15, @50 up decl/2 F15" {
		t.Errorf("sent %q", sent)
	}
}

// The decision sends nothing and plays no haptic: the binding's haptic is the
// feedback of a dispatch, and nothing was dispatched.
func TestUnmatchedMultiTapDecisionIsSilent(t *testing.T) {
	prog := compileHaptic(t, `{"strength":30000}`, "", `"west":{"haptic":"confirm","doubleTap":{"type":"keys","keys":"F14"}}`)
	for _, tc := range []struct {
		name, steps string
		decisions   int
		plays       int
		sent        string
	}{
		{"a single tap", "+west@0 -west@10 wait@1000", 2, 0, ""},
		// The double tap plays it, so the binding is armed.
		{"a double tap", "+west@0 -west@10 +west@60 -west@70 wait@1000", 3, 1, "@70 tap decl/1 F14 20ms"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := &hapticLog{}
			got, sent := unmatchedTrace(t, prog, l.options(), tc.steps)
			if len(got) != tc.decisions || len(l.calls) != tc.plays || sent != tc.sent {
				t.Errorf("%d decisions, %d haptics, sent %q; want %d, %d and %q\n%s", len(got), len(l.calls), sent, tc.decisions, tc.plays, tc.sent, strings.Join(got, "\n"))
			}
		})
	}
}

// What the engine sends is what it sent before the decision existed, with a
// trace or without one.
func TestUnmatchedMultiTapDecisionDoesNotChangeOutput(t *testing.T) {
	const (
		steps = "+west@0 -west@10 +south@40 -south@50 +west@300 -west@310 +west@350 -west@360 wait@1000"
		want  = "@40 tap decl/2 Ctrl+Z 20ms, @360 tap decl/3 F14 20ms"
	)
	prog := compile(t, westDoubleOnly, southTap)
	_, traced := unmatchedTrace(t, prog, engine.Options{}, steps)
	r := &recorder{}
	runM2(t, engine.New(prog, r, engine.Options{}), r, steps)
	if traced != want || r.String() != want {
		t.Errorf("output with a trace %q, without %q; want %q", traced, r.String(), want)
	}
}
