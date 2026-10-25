package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
)

// The decision at the end of a multi-tap window with no action for its taps
// answers a press that waited: it exists so that the row of an unbound press
// that is only waiting (ADR-0071) does not stay unanswered. A cycle that
// something else has already answered, with a press or release action that
// ran or a script that took the gesture, has its decisions, and the end of its
// window says nothing more: "no action for one tap" would contradict the key
// that was just sent.

const (
	westPressDouble    = `"west":{"press":{"type":"keys","keys":"F13","mode":"tap"},"doubleTap":{"type":"keys","keys":"F14"}}`
	westPressTriple    = `"west":{"press":{"type":"keys","keys":"F13","mode":"tap"},"tripleTap":{"type":"keys","keys":"F15"}}`
	westReleaseDouble  = `"west":{"release":{"type":"keys","keys":"F13","mode":"tap"},"doubleTap":{"type":"keys","keys":"F14"}}`
	westReleaseTriple  = `"west":{"release":{"type":"keys","keys":"F13","mode":"tap"},"tripleTap":{"type":"keys","keys":"F15"}}`
	westLatchDouble    = `"west":{"latch":true,"press":{"type":"keys","keys":"E"},"doubleTap":{"type":"keys","keys":"F14"}}`
	westPressDoubleRel = `"west":{"press":{"type":"keys","keys":"F13","mode":"tap"},"release":{"type":"keys","keys":"F12","mode":"tap"},"doubleTap":{"type":"keys","keys":"F14"}}`
)

func TestUnmatchedMultiTapDecisionNotAfterAnAnswer(t *testing.T) {
	const west = "level=base binding=/layers/base/west"
	for _, tc := range []struct {
		name     string
		bindings []string
		steps    string
		want     []string
		sent     string
	}{
		{
			name:     "a press action next to the double tap, tapped once",
			bindings: []string{westPressDouble},
			steps:    "+west@0 -west@10 wait@1000",
			want:     []string{"@0s west down press dispatch " + west},
			sent:     "@0 tap decl/1 F13 20ms",
		},
		{
			name:     "a press action next to the triple tap, tapped twice",
			bindings: []string{westPressTriple},
			steps:    "+west@0 -west@10 +west@60 -west@70 wait@1000",
			want: []string{
				"@0s west down press dispatch " + west,
				"@60ms west down press dispatch " + west,
			},
			sent: "@0 tap decl/1 F13 20ms, @60 tap decl/1 F13 20ms",
		},
		{
			name:     "a release action next to the double tap, tapped once: the release answers the press",
			bindings: []string{westReleaseDouble},
			steps:    "+west@0 -west@10 wait@1000",
			want: []string{
				"@0s west down press unbound " + west,
				"@10ms west down release dispatch " + west,
			},
			sent: "@10 tap decl/1 F13 20ms",
		},
		{
			name:     "a release action next to the triple tap, tapped twice",
			bindings: []string{westReleaseTriple},
			steps:    "+west@0 -west@10 +west@60 -west@70 wait@1000",
			want: []string{
				"@0s west down press unbound " + west,
				"@10ms west down release dispatch " + west,
				"@60ms west down press unbound " + west,
				"@70ms west down release dispatch " + west,
			},
			sent: "@10 tap decl/1 F13 20ms, @70 tap decl/1 F13 20ms",
		},
		{
			name:     "another control's press flushes a window that has an answer",
			bindings: []string{westPressDouble, southTap},
			steps:    "+west@0 -west@10 +south@40 -south@50",
			want: []string{
				"@0s west down press dispatch " + west,
				"@40ms south down press dispatch level=base binding=/layers/base/south",
				"@50ms south up press end level=base binding=/layers/base/south",
			},
			sent: "@0 tap decl/1 F13 20ms, @40 tap decl/2 Ctrl+Z 20ms",
		},
		{
			name:     "a press and a release action: the press answered",
			bindings: []string{westPressDoubleRel},
			steps:    "+west@0 -west@10 wait@1000",
			want: []string{
				"@0s west down press dispatch " + west,
				"@10ms west down release dispatch " + west,
			},
			sent: "@0 tap decl/1 F13 20ms, @10 tap decl/1 F12 20ms",
		},
		{
			// The latch ends the first cycle's keys with the second press, and the
			// window it leaves open ends with nothing to add: the first press ran
			// its action.
			name:     "a window left open by an unlatching press",
			bindings: []string{westLatchDouble},
			steps:    "+west@0 -west@10 +west@60 -west@70 wait@1000",
			want: []string{
				"@0s west down press dispatch " + west,
				"@10ms west up press release " + west,
				"@60ms west down press canceled " + west,
				"@70ms west up press canceled " + west,
			},
			sent: "@0 down decl/1 E, @60 up decl/1 E",
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

// Under a script's gesture registry the same holds: a press the script took, or
// a block, has answered the cycle, and the end of its window adds nothing. A
// press that a handler passed and no declaration answered is still waiting,
// and the end of the window answers it.
func TestUnmatchedMultiTapDecisionNotAfterAScriptAnswer(t *testing.T) {
	entry := func(g engine.Gesture) engine.GestureEntry {
		return engine.GestureEntry{ID: 1, Control: control.West, Gesture: g, Scope: "base"}
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
			name:    "handlers of the press and the double tap, tapped once",
			entries: []engine.GestureEntry{entry(engine.GesturePress), entry(engine.GestureDoubleTap)},
			steps:   "+west@0 -west@10 wait@1000",
			want:    []string{"@0s west down press script"},
		},
		{
			name:     "a handler of the press over a double tap, tapped once",
			bindings: []string{westDoubleOnly},
			entries:  []engine.GestureEntry{entry(engine.GesturePress)},
			steps:    "+west@0 -west@10 wait@1000",
			want:     []string{"@0s west down press script"},
		},
		{
			name:     "a declared press under a handler of the double tap, tapped once",
			bindings: []string{westPressDouble},
			entries:  []engine.GestureEntry{entry(engine.GestureDoubleTap)},
			steps:    "+west@0 -west@10 wait@1000",
			want:     []string{"@0s west down press dispatch level=base binding=/layers/base/west"},
		},
		{
			name:     "a handler of the press that passes: the press waits, and the end of the window answers it",
			bindings: []string{westDoubleOnly},
			entries:  []engine.GestureEntry{entry(engine.GesturePress)},
			pass:     true,
			steps:    "+west@0 -west@10 wait@1000",
			want: []string{
				"@0s west down press unbound " + resolution,
				"@230ms west down tap unbound " + resolution,
			},
		},
		{
			name:     "a handler of the release over a double tap, tapped once",
			bindings: []string{westDoubleOnly},
			entries:  []engine.GestureEntry{entry(engine.GestureRelease)},
			steps:    "+west@0 -west@10 wait@1000",
			want: []string{
				"@0s west down press unbound " + resolution,
				"@10ms west down release script",
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
}

// A window that CancelAll, Pause, Reset or Reload drops is not committed, so it
// has no end to decide, but the press that waits for it is answered by the
// cancel (ReasonCancel, as for a cycle that is held): the row of a press that
// is only waiting would otherwise stay unanswered for ever. A window of a
// cycle that something answered has nothing waiting, and no cancel.
func TestUnmatchedMultiTapDroppedWindowIsCanceled(t *testing.T) {
	const (
		press  = "@0s west down press unbound level=base binding=/layers/base/west"
		cancel = "@20ms west press cancel level=base binding=/layers/base/west"
	)
	for _, tc := range []struct {
		name, steps string
	}{
		{"CancelAll", "+west@0 -west@10 cancel@20 wait@1000"},
		{"Pause", "+west@0 -west@10 pause@20 resume@30 wait@1000"},
		{"Reset", "+west@0 -west@10 reset@20 wait@1000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, sent := unmatchedTrace(t, compile(t, westDoubleOnly), engine.Options{}, tc.steps)
			if want := []string{press, cancel}; !slices.Equal(got, want) {
				t.Errorf("decisions\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			if sent != "" {
				t.Errorf("sent %q", sent)
			}
		})
	}
	t.Run("Reload", func(t *testing.T) {
		var got []string
		r := &recorder{}
		e := engine.New(compile(t, westDoubleOnly), r, engine.Options{Trace: func(d engine.Decision) { got = append(got, scriptBrief(d)) }})
		runM2(t, e, r, "+west@0 -west@10")
		e.Reload(timeAt(20), compile(t, westDoubleOnly))
		runM2(t, e, r, "wait@1000")
		if want := []string{press, cancel}; !slices.Equal(got, want) {
			t.Errorf("decisions\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})
	t.Run("a script's registry", func(t *testing.T) {
		r := &recorder{}
		var got []string
		e := engine.New(compile(t, westDoubleOnly), r, engine.Options{
			Gestures: &gestureRegistry{},
			Trace:    func(d engine.Decision) { got = append(got, scriptBrief(d)) },
		})
		runM2(t, e, r, "+west@0 -west@10 cancel@20 wait@1000")
		want := []string{"@0s west down press unbound binding=script-resolution", "@20ms west press cancel level=base binding=script-resolution"}
		if !slices.Equal(got, want) {
			t.Errorf("decisions\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})
	t.Run("a window that has an answer", func(t *testing.T) {
		got, _ := unmatchedTrace(t, compile(t, westPressDouble), engine.Options{}, "+west@0 -west@10 cancel@20 wait@1000")
		if want := []string{"@0s west down press dispatch level=base binding=/layers/base/west"}; !slices.Equal(got, want) {
			t.Errorf("decisions\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})
	t.Run("a tap that continues the count leaves no window to cancel", func(t *testing.T) {
		// The second press takes the window over; the cancel that follows ends the
		// cycle of the count, which is the decision of that cycle, and nothing
		// more.
		got, sent := unmatchedTrace(t, compile(t, westDoubleOnly), engine.Options{}, "+west@0 -west@10 +west@60 cancel@65 -west@70 wait@1000")
		want := []string{
			press,
			"@60ms west down press unbound level=base binding=/layers/base/west",
			"@65ms west press cancel level=base binding=/layers/base/west",
			"@70ms west up press canceled level=base binding=/layers/base/west",
		}
		if !slices.Equal(got, want) || sent != "" {
			t.Errorf("decisions\n%s\nwant\n%s\nsent %q", strings.Join(got, "\n"), strings.Join(want, "\n"), sent)
		}
	})
	t.Run("two windows, in control order", func(t *testing.T) {
		got, _ := unmatchedTrace(t, compile(t, westDoubleOnly, `"north":{"tripleTap":{"type":"keys","keys":"F17"}}`), engine.Options{}, "+west@0 +north@5 -west@10 -north@15 cancel@20")
		want := []string{
			press,
			"@5ms north down press unbound level=base binding=/layers/base/north",
			cancel,
			"@20ms north press cancel level=base binding=/layers/base/north",
		}
		if !slices.Equal(got, want) {
			t.Errorf("decisions\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})
}

// A release that a script's button handler consumed runs no tap and opens no
// window, so the press of a cycle that only waited for them is answered by a
// decision of the script (ReasonScript, the release gesture) instead of staying
// unanswered. A cycle that something answered already, or that waits for
// nothing, gets none.
func TestConsumedReleaseAnswersAWaitingPress(t *testing.T) {
	const (
		west     = "level=base binding=/layers/base/west"
		tapOnly  = `"west":{"tap":{"type":"keys","keys":"F13"}}`
		tapHold  = `"west":{"tap":{"type":"keys","keys":"F13"},"hold":{"type":"keys","keys":"E"}}`
		relOnly  = `"west":{"release":{"type":"keys","keys":"F13"}}`
		pressRel = `"west":{"press":{"type":"keys","keys":"F13","mode":"tap"},"tap":{"type":"keys","keys":"F12"}}`
	)
	consumeRelease := func(ev engine.ButtonEvent) bool { return !ev.Down }
	// secondRelease consumes only the second release it sees.
	secondRelease := func() func(engine.ButtonEvent) bool {
		releases := 0
		return func(ev engine.ButtonEvent) bool {
			if !ev.Down {
				releases++
			}
			return !ev.Down && releases == 2
		}
	}
	for _, tc := range []struct {
		name     string
		bindings []string
		button   func(engine.ButtonEvent) bool
		steps    string
		want     []string
		sent     string
	}{
		{
			name:     "a tap",
			bindings: []string{tapOnly},
			steps:    "+west@0 -west@10 wait@1000",
			want:     []string{"@0s west down press unbound " + west, "@10ms west down release script"},
		},
		{
			name:     "a double tap only",
			bindings: []string{westDoubleOnly},
			steps:    "+west@0 -west@10 wait@1000",
			want:     []string{"@0s west down press unbound " + west, "@10ms west down release script"},
		},
		{
			name:     "a tap with a hold, released before the hold",
			bindings: []string{tapHold},
			steps:    "+west@0 -west@10 wait@1000",
			want:     []string{"@0s west down press unbound " + west, "@10ms west down release script"},
		},
		{
			name:     "a release action: the end of the cycle is not a decision to show",
			bindings: []string{relOnly},
			steps:    "+west@0 -west@10 wait@1000",
			want:     []string{"@0s west down press unbound " + west, "@10ms west down release script", "@10ms west up press end " + west},
		},
		{
			name:     "the second tap of a count",
			bindings: []string{`"west":{"tripleTap":{"type":"keys","keys":"F15"}}`},
			button:   secondRelease(),
			steps:    "+west@0 -west@10 +west@60 -west@70 wait@1000",
			want:     []string{"@0s west down press unbound " + west, "@60ms west down press unbound " + west, "@70ms west down release script"},
		},
		{
			name:     "a press action has answered",
			bindings: []string{pressRel},
			steps:    "+west@0 -west@10 wait@1000",
			want:     []string{"@0s west down press dispatch " + west},
			sent:     "@0 tap decl/1 F13 20ms",
		},
		{
			name:     "a key without a binding waits for nothing",
			bindings: []string{southTap},
			steps:    "+west@0 -west@10 wait@1000",
			want:     []string{"@0s west down press unbound", "@10ms west up press end"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			var got []string
			button := tc.button
			if button == nil {
				button = consumeRelease
			}
			e := engine.New(compile(t, tc.bindings...), r, engine.Options{
				Button: button,
				Trace:  func(d engine.Decision) { got = append(got, scriptBrief(d)) },
			})
			runM2(t, e, r, tc.steps)
			if !slices.Equal(got, tc.want) {
				t.Errorf("decisions\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
			if r.String() != tc.sent {
				t.Errorf("sent %q, want %q", r.String(), tc.sent)
			}
		})
	}
	t.Run("the decision is a value of the cycle", func(t *testing.T) {
		r := &recorder{}
		var got []engine.Decision
		e := engine.New(compile(t, tapOnly), r, engine.Options{
			Button: consumeRelease,
			Trace:  func(d engine.Decision) { got = append(got, d) },
		})
		runM2(t, e, r, "+west@0 -west@10 wait@1000")
		if len(got) != 2 {
			t.Fatalf("%d decisions: %v", len(got), got)
		}
		press, consumed := got[0], got[1]
		if consumed.Reason != engine.ReasonScript || consumed.Edge != engine.EdgeDown || consumed.Gesture != engine.GestureRelease || consumed.Cycle != press.Cycle || consumed.Control != control.West {
			t.Errorf("decision %s does not answer the press %s", consumed, press)
		}
		if consumed.Action != nil || consumed.Owner != (engine.OwnerID{}) || consumed.Binding != nil || consumed.Level != "" {
			t.Errorf("decision %s carries a binding, an action or an owner", consumed)
		}
		if want := `{"t":10,"control":"west","edge":"down","cycle":1,"gesture":"release","reason":"script"}`; consumed.String() != want {
			t.Errorf("JSON %s, want %s", consumed, want)
		}
	})
}

// A window keeps the binding that opened it, but a press that arrives after a
// layer changed (not a function key's set, which ends the window) continues
// the count with the binding it finds now. This is not new, and it is a known
// limit (ARCHITECTURE §12): the first window is dropped for it, and the new
// binding is asked for an action for a count it never saw. The decision says
// so, at the release, instead of leaving the press unanswered.
func TestUnmatchedMultiTapDecisionAcrossALayerChange(t *testing.T) {
	const src = `{"schemaVersion":1,"name":"stale","layers":{
 "base":{"dpad.down":{"press":{"type":"layer","layer":"fn","op":"hold"}},"west":{"tap":{"type":"keys","keys":"F13"}}},
 "fn":{"west":{"doubleTap":{"type":"keys","keys":"F14"}}}}}`
	got, sent := unmatchedTrace(t, compileLayers(t, src), engine.Options{}, "+dpad.down@0 +west@10 -west@20 -dpad.down@30 +west@60 -west@70 wait@1000")
	want := []string{
		"@0s dpad.down down press dispatch level=base binding=/layers/base/dpad.down",
		"@10ms west down press unbound level=fn binding=/layers/fn/west",
		"@30ms dpad.down up press end level=base binding=/layers/base/dpad.down",
		"@60ms west down press unbound level=base binding=/layers/base/west",
		"@70ms west down doubleTap unbound level=base binding=/layers/base/west",
	}
	if !slices.Equal(got, want) {
		t.Errorf("decisions\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if sent != "" {
		t.Errorf("sent %q", sent)
	}
}
