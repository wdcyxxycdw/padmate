package engine_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
)

// scriptTrace steps a program with the given base bindings and a script
// registry (entries, pass) and returns the decisions as text:
// "@t control edge gesture reason level binding label", the members that
// are empty left out.
func scriptTrace(t *testing.T, bindings []string, entries []engine.GestureEntry, pass bool, steps string) []string {
	t.Helper()
	r := &recorder{}
	g := &gestureRegistry{entries: entries, pass: pass}
	var got []string
	e := engine.New(compile(t, bindings...), r, engine.Options{Gestures: g, Trace: func(d engine.Decision) { got = append(got, scriptBrief(d)) }})
	run(t, e, r, steps)
	return got
}

func scriptBrief(d engine.Decision) string {
	parts := []string{"@" + time.Duration(d.T).String(), d.Control.String()}
	if d.Edge != engine.EdgeNone {
		parts = append(parts, d.Edge.String())
	}
	if d.Gesture != engine.GestureNone {
		parts = append(parts, d.Gesture.String())
	}
	parts = append(parts, d.Reason.String())
	if d.Level != "" {
		parts = append(parts, "level="+d.Level)
	}
	if d.Binding != nil {
		parts = append(parts, "binding="+d.Binding.ID)
	}
	return strings.Join(parts, " ")
}

func TestReasonScriptString(t *testing.T) {
	if got := engine.ReasonScript.String(); got != "script" {
		t.Errorf("ReasonScript.String() = %q", got)
	}
	if got := (engine.ReasonScript + 1).String(); got != "reason(12)" {
		t.Errorf("the reason after ReasonScript is %q; the table must end there", got)
	}
}

// A script that handles or swallows a gesture, or that leaves a scripted
// press with nothing to run, leaves a decision (ADR-0071, ENG-20). Before
// it a scripted press that resolved to nothing left none at all.
func TestScriptDecisions(t *testing.T) {
	entry := func(c control.Control, g engine.Gesture, scope string) engine.GestureEntry {
		return engine.GestureEntry{ID: 1, Control: c, Gesture: g, Scope: scope}
	}
	for _, tc := range []struct {
		name     string
		bindings []string
		entries  []engine.GestureEntry
		pass     bool
		steps    string
		want     []string
	}{
		{
			name:    "a handler takes the press",
			entries: []engine.GestureEntry{entry(control.South, engine.GesturePress, "base")},
			steps:   "+south@0 -south@10",
			want: []string{
				"@0s south down press script",
				"@10ms south up press end binding=script-resolution",
			},
		},
		{
			name:     "a handler that passes leaves the declaration its dispatch",
			bindings: []string{southTap},
			entries:  []engine.GestureEntry{entry(control.South, engine.GesturePress, "base")},
			pass:     true,
			steps:    "+south@0 -south@10",
			want: []string{
				"@0s south down press dispatch level=base binding=/layers/base/south",
				"@10ms south up press end level=base binding=script-resolution",
			},
		},
		{
			name:    "a handler of the tap: the press waits, the release is the script's",
			entries: []engine.GestureEntry{entry(control.South, engine.GestureTap, "base")},
			steps:   "+south@0 -south@10",
			want: []string{
				"@0s south down press unbound binding=script-resolution",
				"@10ms south down tap script",
			},
		},
		{
			name:    "a handler that passes with nothing below it",
			entries: []engine.GestureEntry{entry(control.South, engine.GesturePress, "base")},
			pass:    true,
			steps:   "+south@0 -south@10",
			want: []string{
				"@0s south down press unbound binding=script-resolution",
				"@10ms south up press end binding=script-resolution",
			},
		},
		{
			name:    "a control no script and no declaration knows",
			entries: []engine.GestureEntry{entry(control.South, engine.GesturePress, "base")},
			steps:   "+east@0 -east@10",
			want: []string{
				"@0s east down press unbound binding=script-resolution",
				"@10ms east up press end binding=script-resolution",
			},
		},
		{
			name:    "a claim swallows the gesture it has no handler for",
			entries: []engine.GestureEntry{{ID: 1, Control: control.South, Scope: "global", Claim: true}},
			steps:   "+south@0 -south@10",
			want: []string{
				"@0s south down press script",
				"@10ms south up press end binding=script-resolution",
			},
		},
		{
			name:     "a blocked control",
			bindings: []string{`"north": {"block": true}`},
			entries:  []engine.GestureEntry{entry(control.South, engine.GesturePress, "base")},
			steps:    "+north@0 -north@10",
			want: []string{
				"@0s north down press blocked level=base binding=/layers/base/north",
				"@10ms north up press end level=base binding=script-resolution",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := scriptTrace(t, tc.bindings, tc.entries, tc.pass, tc.steps)
			if !slices.Equal(got, tc.want) {
				t.Errorf("decisions\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

// The script decision is a value of the engine's own: no binding, level,
// action or owner, edge down and the gesture it took.
func TestScriptDecisionJSON(t *testing.T) {
	r := &recorder{}
	g := &gestureRegistry{entries: []engine.GestureEntry{{ID: 1, Control: control.South, Gesture: engine.GesturePress, Scope: "base"}}}
	var got []engine.Decision
	e := engine.New(compile(t, southTap), r, engine.Options{Gestures: g, Trace: func(d engine.Decision) { got = append(got, d) }})
	run(t, e, r, "+south@20")
	if len(got) != 1 {
		t.Fatalf("decisions %v", got)
	}
	want := `{"t":20,"control":"south","edge":"down","cycle":1,"gesture":"press","reason":"script"}`
	if s := got[0].String(); s != want {
		t.Errorf("JSON %s, want %s", s, want)
	}
	if d := got[0]; d.Binding != nil || d.Action != nil || d.Level != "" || d.Owner != (engine.OwnerID{}) {
		t.Errorf("script decision %+v carries more than the reason", d)
	}
	if r.String() != "" {
		t.Errorf("the engine sent %q for a gesture the script took", r.String())
	}
}

// A press that the script's button handler consumes (the callback of a
// legacy script, Options.Button) starts no cycle and used to leave nothing.
func TestConsumedButtonDecision(t *testing.T) {
	r := &recorder{}
	var got []engine.Decision
	consume := true
	e := engine.New(compile(t, southTap), r, engine.Options{
		Button: func(ev engine.ButtonEvent) bool { return consume && ev.Control == control.South },
		Trace:  func(d engine.Decision) { got = append(got, d) },
	})
	run(t, e, r, "+south@5 -south@9 +east@12 -east@14")
	want := []string{
		`{"t":5,"control":"south","edge":"down","cycle":0,"gesture":"press","reason":"script"}`,
		`{"t":12,"control":"east","edge":"down","cycle":1,"gesture":"press","reason":"unbound"}`,
		`{"t":14,"control":"east","edge":"up","cycle":1,"gesture":"press","reason":"end"}`,
	}
	var lines []string
	for _, d := range got {
		lines = append(lines, d.String())
	}
	if !slices.Equal(lines, want) {
		t.Errorf("decisions\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if r.String() != "" {
		t.Errorf("sent %q", r.String())
	}
}

// A callback that consumes nothing leaves the usual decisions.
func TestUnconsumedButtonDecision(t *testing.T) {
	r := &recorder{}
	var got []string
	e := engine.New(compile(t, southTap), r, engine.Options{
		Button: func(engine.ButtonEvent) bool { return false },
		Trace:  func(d engine.Decision) { got = append(got, d.Reason.String()) },
	})
	run(t, e, r, "+south@5 -south@9")
	if want := []string{"dispatch", "end"}; !slices.Equal(got, want) {
		t.Errorf("reasons %v, want %v", got, want)
	}
}
