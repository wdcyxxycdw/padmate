package engine_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// JSON members shared by the decisions below.
const (
	east  = `"gesture":"press","level":"base","binding":"/layers/base/east","label":"抓手"`
	south = `"gesture":"press","level":"base","binding":"/layers/base/south","label":"撤销"`
	lt    = `"gesture":"press","level":"base","binding":"/layers/base/lt"`
)

func TestDecisions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		bindings []string
		input    string
		want     []string
	}{
		{
			name:     "hold",
			bindings: []string{eastHold},
			input:    "+east@0 -east@300",
			want: []string{
				`{"t":0,"control":"east","edge":"down","cycle":1,` + east + `,"action":"hold Space","owner":"decl/1","reason":"dispatch"}`,
				`{"t":300,"control":"east","edge":"up","cycle":1,` + east + `,"action":"hold Space","owner":"decl/1","reason":"release"}`,
			},
		},
		{
			name:     "tap",
			bindings: []string{southTap},
			input:    "+south@20 -south@25",
			want: []string{
				`{"t":20,"control":"south","edge":"down","cycle":1,` + south + `,"action":"tap Ctrl+Z","owner":"decl/1","reason":"dispatch"}`,
				`{"t":25,"control":"south","edge":"up","cycle":1,` + south + `,"reason":"end"}`,
			},
		},
		{
			name:     "alias",
			bindings: []string{`"a": {"press": {"type": "keys", "keys": "shift+f13"}}`},
			input:    "+south@0 -south@10",
			want: []string{
				`{"t":0,"control":"south","edge":"down","cycle":1,"gesture":"press","level":"base","binding":"/layers/base/a","action":"hold Shift+F13","owner":"decl/1","reason":"dispatch"}`,
				`{"t":10,"control":"south","edge":"up","cycle":1,"gesture":"press","level":"base","binding":"/layers/base/a","action":"hold Shift+F13","owner":"decl/1","reason":"release"}`,
			},
		},
		{
			name:     "unbound",
			bindings: []string{eastHold, xLabelOnly},
			input:    "+north@0 -north@10 +west@20 -west@30",
			want: []string{
				`{"t":0,"control":"north","edge":"down","cycle":1,"gesture":"press","reason":"unbound"}`,
				`{"t":10,"control":"north","edge":"up","cycle":1,"gesture":"press","reason":"end"}`,
				`{"t":20,"control":"west","edge":"down","cycle":2,"gesture":"press","level":"base","binding":"/layers/base/x","label":"todo","reason":"unbound"}`,
				`{"t":30,"control":"west","edge":"up","cycle":2,"gesture":"press","level":"base","binding":"/layers/base/x","label":"todo","reason":"end"}`,
			},
		},
		{
			name:     "spurious",
			bindings: []string{eastHold, ltAlt},
			input:    "-east@0 +east@10 +east@20 -east@30 -east@40 +lt@50",
			want: []string{
				`{"t":0,"control":"east","edge":"up","cycle":0,"reason":"spurious"}`,
				`{"t":10,"control":"east","edge":"down","cycle":1,` + east + `,"action":"hold Space","owner":"decl/1","reason":"dispatch"}`,
				`{"t":20,"control":"east","edge":"down","cycle":1,"reason":"spurious"}`,
				`{"t":30,"control":"east","edge":"up","cycle":1,` + east + `,"action":"hold Space","owner":"decl/1","reason":"release"}`,
				`{"t":40,"control":"east","edge":"up","cycle":0,"reason":"spurious"}`,
				`{"t":50,"control":"lt","edge":"down","cycle":0,"reason":"spurious"}`,
			},
		},
		{
			name:     "trigger crossings only",
			bindings: []string{ltAlt},
			input:    "lefttrigger=5000@0 lefttrigger=10000@10 lefttrigger=8000@20 lefttrigger=6000@30 lefttrigger=100@40 leftx=32767@50",
			want: []string{
				`{"t":10,"control":"lt","edge":"down","cycle":1,` + lt + `,"action":"hold Alt","owner":"decl/1","reason":"dispatch"}`,
				`{"t":30,"control":"lt","edge":"up","cycle":1,` + lt + `,"action":"hold Alt","owner":"decl/1","reason":"release"}`,
			},
		},
		{
			name:     "CancelAll",
			bindings: []string{eastHold, southTap, ltAlt},
			input: "+east@0 +south@10 lefttrigger=20000@20 +north@25 cancel@30 +east@35 -east@40 -south@50 " +
				"lefttrigger=9000@55 lefttrigger=6553@60 -north@65 cancel@70",
			want: []string{
				`{"t":0,"control":"east","edge":"down","cycle":1,` + east + `,"action":"hold Space","owner":"decl/1","reason":"dispatch"}`,
				`{"t":10,"control":"south","edge":"down","cycle":2,` + south + `,"action":"tap Ctrl+Z","owner":"decl/2","reason":"dispatch"}`,
				`{"t":20,"control":"lt","edge":"down","cycle":3,` + lt + `,"action":"hold Alt","owner":"decl/3","reason":"dispatch"}`,
				`{"t":25,"control":"north","edge":"down","cycle":4,"gesture":"press","reason":"unbound"}`,
				// In control order. Only hold cycles have an action left.
				`{"t":30,"control":"south","cycle":2,` + south + `,"reason":"cancel"}`,
				`{"t":30,"control":"east","cycle":1,` + east + `,"action":"hold Space","reason":"cancel"}`,
				`{"t":30,"control":"north","cycle":4,"gesture":"press","reason":"cancel"}`,
				`{"t":30,"control":"lt","cycle":3,` + lt + `,"action":"hold Alt","reason":"cancel"}`,
				`{"t":35,"control":"east","edge":"down","cycle":1,"reason":"spurious"}`,
				`{"t":40,"control":"east","edge":"up","cycle":1,` + east + `,"reason":"canceled"}`,
				`{"t":50,"control":"south","edge":"up","cycle":2,` + south + `,"reason":"canceled"}`,
				`{"t":60,"control":"lt","edge":"up","cycle":3,` + lt + `,"reason":"canceled"}`,
				`{"t":65,"control":"north","edge":"up","cycle":4,"gesture":"press","reason":"canceled"}`,
			},
		},
		{
			name:     "Reset",
			bindings: []string{eastHold},
			input:    "+east@0 +west@5 reset@10 -east@20 -west@25 +east@30 reset@40 reset@50",
			want: []string{
				`{"t":0,"control":"east","edge":"down","cycle":1,` + east + `,"action":"hold Space","owner":"decl/1","reason":"dispatch"}`,
				`{"t":5,"control":"west","edge":"down","cycle":2,"gesture":"press","reason":"unbound"}`,
				`{"t":10,"control":"east","cycle":1,` + east + `,"action":"hold Space","reason":"cancel"}`,
				`{"t":10,"control":"west","cycle":2,"gesture":"press","reason":"cancel"}`,
				`{"t":20,"control":"east","edge":"up","cycle":0,"reason":"spurious"}`,
				`{"t":25,"control":"west","edge":"up","cycle":0,"reason":"spurious"}`,
				`{"t":30,"control":"east","edge":"down","cycle":3,` + east + `,"action":"hold Space","owner":"decl/3","reason":"dispatch"}`,
				`{"t":40,"control":"east","cycle":3,` + east + `,"action":"hold Space","reason":"cancel"}`,
			},
		},
		{
			name:     "Pause",
			bindings: []string{eastHold, ltAlt},
			input:    "+east@0 pause@10 -east@15 +east@20 +west@25 lefttrigger=20000@30 resume@40 -east@50 -west@60 lefttrigger=0@70",
			want: []string{
				`{"t":0,"control":"east","edge":"down","cycle":1,` + east + `,"action":"hold Space","owner":"decl/1","reason":"dispatch"}`,
				`{"t":10,"control":"east","cycle":1,` + east + `,"action":"hold Space","reason":"cancel"}`,
				`{"t":15,"control":"east","edge":"up","cycle":1,` + east + `,"reason":"canceled"}`,
				// A press while paused has what its lookup found, and no
				// action or owner.
				`{"t":20,"control":"east","edge":"down","cycle":2,` + east + `,"reason":"paused"}`,
				`{"t":25,"control":"west","edge":"down","cycle":3,"gesture":"press","reason":"paused"}`,
				`{"t":30,"control":"lt","edge":"down","cycle":4,` + lt + `,"reason":"paused"}`,
				`{"t":50,"control":"east","edge":"up","cycle":2,` + east + `,"reason":"canceled"}`,
				`{"t":60,"control":"west","edge":"up","cycle":3,"gesture":"press","reason":"canceled"}`,
				`{"t":70,"control":"lt","edge":"up","cycle":4,` + lt + `,"reason":"canceled"}`,
			},
		},
		{
			name:     "Apply decides nothing",
			bindings: []string{southTap},
			input:    "apply@0 +south@10 apply@10 apply@30",
			want: []string{
				`{"t":10,"control":"south","edge":"down","cycle":1,` + south + `,"action":"tap Ctrl+Z","owner":"decl/1","reason":"dispatch"}`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			r := &recorder{}
			e := engine.New(compile(t, tc.bindings...), r, engine.Options{Trace: func(d engine.Decision) {
				b, err := d.MarshalJSON()
				if err != nil {
					t.Errorf("MarshalJSON(%+v): %v", d, err)
				}
				got = append(got, string(b))
			}})
			run(t, e, r, tc.input)
			if g, w := strings.Join(got, "\n"), strings.Join(tc.want, "\n"); g != w {
				t.Errorf("decisions:\n%s\nwant\n%s", g, w)
			}
		})
	}
}

// TestDecisionsMatchOutput checks that a decision's owner is the owner of
// the output it produced, and that the trace does not change the output.
func TestDecisionsMatchOutput(t *testing.T) {
	const input = "+east@0 +south@10 lefttrigger=20000@20 -south@30 lefttrigger=0@40 -east@50 +east@60 cancel@70 -east@80 " +
		"+east@90 pause@100 +south@110 -south@120 resume@130 -east@140 +south@150"
	prog := compile(t, eastHold, southTap, ltAlt)
	plain := &recorder{}
	run(t, engine.New(prog, plain, engine.Options{}), plain, input)

	traced := &recorder{}
	var owners []string
	e := engine.New(prog, traced, engine.Options{Trace: func(d engine.Decision) {
		if d.Owner != (engine.OwnerID{}) {
			owners = append(owners, d.Owner.String())
		}
	}})
	run(t, e, traced, input)
	if plain.String() != traced.String() {
		t.Errorf("output with a trace:\n%s\nwithout:\n%s", traced, plain)
	}
	var fromOutput []string
	for _, s := range traced.out {
		fromOutput = append(fromOutput, strings.Fields(s)[2])
	}
	if g, w := strings.Join(owners, " "), strings.Join(fromOutput, " "); g != w {
		t.Errorf("decision owners %s, output owners %s", g, w)
	}
}

// TestDecisionOrder checks that every decision comes before the output it
// causes, in hold and tap mode and on a trigger.
func TestDecisionOrder(t *testing.T) {
	r := &recorder{}
	e := engine.New(compile(t, eastHold, southTap, ltAlt), r, engine.Options{Trace: func(d engine.Decision) {
		r.add(d.Reason.String() + " " + d.Control.String())
	}})
	run(t, e, r, "+east@0 +south@10 -south@20 lefttrigger=10000@25 lefttrigger=0@28 -east@30 +east@40 cancel@50")
	want := "@0 dispatch east, @0 down decl/1 Space, @10 dispatch south, @10 tap decl/2 Ctrl+Z 20ms, @20 end south, " +
		"@25 dispatch lt, @25 down decl/3 Alt, @28 release lt, @28 up decl/3 Alt, " +
		"@30 release east, @30 up decl/1 Space, @40 dispatch east, @40 down decl/4 Space, @50 cancel east"
	if got := r.String(); got != want {
		t.Errorf("decisions and output:\n%s\nwant\n%s", got, want)
	}
}

func TestDecisionMarshalJSON(t *testing.T) {
	b := &profile.Binding{ID: "/layers/base/south", Control: control.South, Label: `撤销 <b>&"\`}
	tap := &profile.Action{ID: b.ID + "/press", Type: profile.ActionKeys, Keys: keys.MustParse("ctrl+z"), Mode: profile.ModeTap}
	for _, tc := range []struct {
		d    engine.Decision
		want string
	}{
		{engine.Decision{}, `{"t":0,"control":"control(0)","cycle":0,"reason":"reason(0)"}`},
		{
			engine.Decision{
				T: clock.Time(20 * time.Millisecond), Control: control.South, Edge: engine.EdgeDown, Cycle: 17,
				Gesture: engine.GesturePress, Level: "base", Binding: b, Action: tap,
				Owner: engine.OwnerID{Source: engine.SourceDecl, Cycle: 17}, Reason: engine.ReasonDispatch,
			},
			`{"t":20,"control":"south","edge":"down","cycle":17,"gesture":"press","level":"base","binding":"/layers/base/south",` +
				`"label":"撤销 <b>&\"\\","action":"tap Ctrl+Z","owner":"decl/17","reason":"dispatch"}`,
		},
		{
			engine.Decision{T: clock.Time(1_500_000), Control: control.LT, Edge: engine.EdgeUp, Reason: engine.ReasonSpurious},
			`{"t":1.5,"control":"lt","edge":"up","cycle":0,"reason":"spurious"}`,
		},
		{
			engine.Decision{T: clock.Time(1_000_001), Control: control.RT, Cycle: 1, Gesture: engine.GesturePress, Reason: engine.ReasonCancel},
			`{"t":1.000001,"control":"rt","cycle":1,"gesture":"press","reason":"cancel"}`,
		},
		{
			engine.Decision{T: clock.Time(-2_500_000), Control: control.Touchpad, Edge: engine.EdgeNone, Reason: engine.ReasonEnd},
			`{"t":-2.5,"control":"touchpad","cycle":0,"reason":"end"}`,
		},
		{
			engine.Decision{T: clock.Time(time.Hour), Control: control.DpadUp, Edge: engine.Edge(9), Gesture: engine.Gesture(9), Reason: engine.Reason(99)},
			`{"t":3600000,"control":"dpad.up","edge":"edge(9)","cycle":0,"gesture":"gesture(9)","reason":"reason(99)"}`,
		},
		{
			// A binding without a label, and an owner with only a cycle.
			engine.Decision{
				Control: control.East, Binding: &profile.Binding{ID: "/layers/base/east"},
				Owner: engine.OwnerID{Cycle: 3}, Reason: engine.ReasonUnbound,
			},
			`{"t":0,"control":"east","cycle":0,"binding":"/layers/base/east","owner":"source(0)/3","reason":"unbound"}`,
		},
	} {
		got, err := tc.d.MarshalJSON()
		if err != nil {
			t.Errorf("MarshalJSON(%+v): %v", tc.d, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("MarshalJSON(%+v) =\n %s\nwant\n %s", tc.d, got, tc.want)
		}
		if !json.Valid(got) {
			t.Errorf("MarshalJSON(%+v) is not valid JSON: %s", tc.d, got)
		}
		if s := tc.d.String(); s != tc.want {
			t.Errorf("String() = %s, want %s", s, tc.want)
		}
		// An Encoder without HTML escaping writes the same bytes.
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(tc.d); err != nil || buf.String() != tc.want+"\n" {
			t.Errorf("Encode(%+v) = %q, %v; want %s", tc.d, buf.String(), err, tc.want)
		}
	}
}

func TestStrings(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{engine.ReasonDispatch.String(), "dispatch"},
		{engine.ReasonUnbound.String(), "unbound"},
		{engine.ReasonRelease.String(), "release"},
		{engine.ReasonEnd.String(), "end"},
		{engine.ReasonCancel.String(), "cancel"},
		{engine.ReasonCanceled.String(), "canceled"},
		{engine.ReasonSpurious.String(), "spurious"},
		{engine.ReasonPaused.String(), "paused"},
		{engine.ReasonBlocked.String(), "blocked"},
		{engine.ReasonFunction.String(), "function"},
		{engine.Reason(0).String(), "reason(0)"},
		{engine.ReasonScript.String(), "script"},
		{(engine.ReasonScript + 1).String(), "reason(12)"},
		{engine.EdgeNone.String(), "none"},
		{engine.EdgeDown.String(), "down"},
		{engine.EdgeUp.String(), "up"},
		{engine.Edge(3).String(), "edge(3)"},
		{engine.SourceDecl.String(), "decl"},
		{engine.SourceScript.String(), "script"},
		{engine.Source(0).String(), "source(0)"},
		{engine.Source(3).String(), "source(3)"},
		{engine.OwnerID{Source: engine.SourceDecl, Cycle: 17}.String(), "decl/17"},
		{engine.OwnerID{Source: engine.SourceScript}.String(), "script/0"},
		{engine.GestureNone.String(), "none"},
		{engine.GesturePress.String(), "press"},
		{engine.InputButton.String(), "button"},
		{engine.InputAxis.String(), "axis"},
		{engine.InputKind(0).String(), "inputkind(0)"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}
