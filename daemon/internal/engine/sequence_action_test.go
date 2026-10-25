package engine_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// The sequence and text actions (MAP-22, OUT-21, ADR-0070) hold nothing, so they
// fit every gesture, and the engine only hands the request to its Output, once,
// when the gesture fires. The timing is the Output's.

const (
	sequenceSteps = `[{"keys": "Ctrl+A"}, {"wait": 100}, {"text": "你好"}]`
	// seqRequest is how the recorder shows the steps above.
	seqRequest = `[Ctrl+A, wait 100, text "你好"]`
)

func TestSequenceActionGestures(t *testing.T) {
	seq := `{"type": "sequence", "steps": ` + sequenceSteps + `}`
	txt := `{"type": "text", "text": "a\tb"}`
	for _, tc := range []struct {
		name     string
		bindings []string
		input    string
		want     string
	}{
		{"press sequence runs at the press and nothing at the release",
			[]string{`"south": {"press": ` + seq + `}`},
			"+south@0 -south@500",
			"@0 steps decl/1 " + seqRequest + " 20ms 20ms"},
		{"press text",
			[]string{`"south": {"press": ` + txt + `}`},
			"+south@0 -south@500",
			`@0 type decl/1 "a\tb" 20ms`},
		{"tap sequence runs at the release",
			[]string{`"south": {"tap": ` + seq + `}`},
			"+south@0 -south@30",
			"@30 steps decl/1 " + seqRequest + " 20ms 20ms"},
		{"release text",
			[]string{`"south": {"release": ` + txt + `}`},
			"+south@0 -south@30",
			`@30 type decl/1 "a\tb" 20ms`},
		{"hold sequence without a tap runs at the press",
			[]string{`"south": {"hold": ` + seq + `}`},
			"+south@0 -south@2000",
			"@0 steps decl/1 " + seqRequest + " 20ms 20ms"},
		{"hold sequence runs once when the hold is recognized, however long it lasts",
			[]string{`"south": {"tap": ` + txt + `, "hold": ` + seq + `}`},
			"+south@0 apply@250 -south@2000",
			"@250 steps decl/1 " + seqRequest + " 20ms 20ms"},
		{"double tap text",
			[]string{`"south": {"doubleTap": ` + txt + `}`},
			"+south@0 -south@10 +south@50 -south@60",
			`@60 type decl/1 "a\tb" 20ms`},
		{"each press is a request of its own, and a latch holds nothing",
			[]string{`"south": {"latch": true, "press": ` + seq + `}`},
			"+south@0 -south@10 +south@20 -south@30",
			"@0 steps decl/1 " + seqRequest + " 20ms 20ms, @20 steps decl/2 " + seqRequest + " 20ms 20ms"},
		{"the binding's timing is the request's",
			[]string{`"south": {"timing": {"tapPressMs": 35, "tapGapMs": 7}, "tap": ` + txt + `}`},
			"+south@0 -south@5",
			`@5 type decl/1 "a\tb" 7ms`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compile(t, tc.bindings...), r, engine.Options{})
			run(t, e, r, tc.input)
			if got := r.String(); got != tc.want {
				t.Errorf("output\n got %s\nwant %s", got, tc.want)
			}
		})
	}
}

// The press time and the gap come from the profile's timing and a binding's
// own, as a key sequence's do.
func TestSequenceActionTiming(t *testing.T) {
	src := `{"schemaVersion": 1, "name": "t", "timing": {"tapPressMs": 12, "tapGapMs": 3}, "layers": {"base": {
		"south": {"press": {"type": "sequence", "steps": [{"keys": "A"}, {"text": "x"}]}},
		"east": {"timing": {"tapGapMs": 0}, "press": {"type": "text", "text": "y"}}}}}`
	p, ds := profile.Parse([]byte(src))
	if p == nil {
		t.Fatal(ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{})
	run(t, e, r, "+south@0 -south@5 +east@10 -east@15")
	want := `@0 steps decl/1 [A, text "x"] 12ms 3ms, @10 type decl/2 "y" 0s`
	if got := r.String(); got != want {
		t.Errorf("output\n got %s\nwant %s", got, want)
	}
}

// The steps handed to the Output are the action's own: no copy per press.
func TestSequenceActionSharesTheSteps(t *testing.T) {
	p, ds := profile.Parse([]byte(`{"schemaVersion": 1, "name": "t", "layers": {"base": {
		"south": {"press": {"type": "sequence", "steps": [{"keys": "A"}, {"wait": 5}]}}}}}`))
	if p == nil {
		t.Fatal(ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	var got [][]keys.Step
	out := &stepOut{recorder: &recorder{}, steps: &got}
	e := engine.New(prog, out, engine.Options{})
	run(t, e, out.recorder, "+south@0 -south@5 +south@10 -south@15")
	if len(got) != 2 || &got[0][0] != &p.Layer("base").Binding(control.South).Press.Steps[0] {
		t.Errorf("the Output got %d requests that do not share the action's steps", len(got))
	}
}

type stepOut struct {
	*recorder
	steps *[][]keys.Step
}

func (o *stepOut) Sequence(owner engine.OwnerID, steps []keys.Step, hold, gap time.Duration) {
	*o.steps = append(*o.steps, steps)
	o.recorder.Sequence(owner, steps, hold, gap)
}

// The decision shows the action in one short line.
func TestSequenceActionDecisions(t *testing.T) {
	var got []string
	e := engine.New(compile(t,
		`"south": {"press": {"type": "sequence", "steps": `+sequenceSteps+`}}`,
		`"east": {"press": {"type": "text", "text": "`+strings.Repeat("字", 40)+`"}}`,
	), &recorder{}, engine.Options{Trace: func(d engine.Decision) {
		if d.Action != nil {
			got = append(got, d.Action.String())
		}
	}})
	e.Step(engine.Input{Kind: engine.InputButton, Control: control.South, Down: true})
	e.Step(engine.Input{Kind: engine.InputButton, Control: control.South})
	e.Step(engine.Input{Kind: engine.InputButton, Control: control.East, Down: true})
	e.Step(engine.Input{Kind: engine.InputButton, Control: control.East})
	want := []string{
		`sequence: Ctrl+A, wait 100, text "你好"`,
		`text "` + strings.Repeat("字", keys.ShortTextRunes) + `…"`,
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("actions\n got %q\nwant %q", got, want)
	}
}

// The JSON line of a decision carries the same text, quoted.
func TestSequenceActionDecisionJSON(t *testing.T) {
	var line []byte
	e := engine.New(compile(t, `"south": {"press": {"type": "text", "text": "a\nb"}}`), &recorder{}, engine.Options{Trace: func(d engine.Decision) {
		if d.Action != nil && line == nil {
			line, _ = d.MarshalJSON()
		}
	}})
	e.Step(engine.Input{Kind: engine.InputButton, Control: control.South, Down: true})
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("%s: %v", line, err)
	}
	if m["action"] != `text "a\nb"` {
		t.Errorf("action = %q in %s", m["action"], line)
	}
}

// The text of a sequence action is on the key hints, short; a label wins.
func TestSequenceActionHints(t *testing.T) {
	h := newHsim(t, `{"schemaVersion": 1, "name": "t", "layers": {"base": {
 "rightshoulder": {"press": {"type": "hud", "op": "hints"}},
 "south": {"press": {"type": "sequence", "steps": `+sequenceSteps+`}},
 "east": {"press": {"type": "text", "text": "hello"}},
 "west": {"label": "签名", "press": {"type": "text", "text": "hello"}}}}}`)
	h.press("rightshoulder")
	h.wantShown("hints on", `on:south=序列：Ctrl+A, wait 100, text "你好" | east=文字：hello | west=签名`)
}
