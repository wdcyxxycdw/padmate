package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

func compileLayers(t testing.TB, src string) *engine.Program {
	t.Helper()
	p, ds := profile.Parse([]byte(src))
	if p == nil || ds.HasErrors() {
		t.Fatalf("profile.Parse: %v", ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return prog
}

func runLayerScript(t testing.TB, e *engine.Engine, r *recorder, script string) {
	t.Helper()
	for _, tok := range strings.Fields(script) {
		op, at, ok := strings.Cut(tok, "@")
		if !ok {
			t.Fatalf("bad step %q", tok)
		}
		n, err := time.ParseDuration(at + "ms")
		if err != nil {
			t.Fatalf("bad time %q: %v", at, err)
		}
		now := clock.Time(n)
		r.now = now
		switch op {
		case "wait":
			for d, ok := e.NextDeadline(); ok && d <= now; d, ok = e.NextDeadline() {
				r.now = d
				e.Apply(d)
			}
		default:
			c, ok := control.Parse(op[1:])
			if !ok {
				t.Fatalf("bad control %q", op)
			}
			e.Step(engine.Input{T: now, Kind: engine.InputButton, Control: c, Down: op[0] == '+'})
		}
	}
}

func TestM3Layers(t *testing.T) {
	const src = `{"schemaVersion":0,"name":"layers","defaultLayer":"base","common":{"leftstick":{"press":{"type":"layer","layers":["base","alt","third"],"op":"cycle"}}},"layers":{"base":{"east":{"press":{"type":"layer","layer":"alt","op":"toggle"}},"south":{"press":{"type":"keys","keys":"A"}},"west":{"latch":true,"press":{"type":"keys","keys":"Shift"}}},"alt":{"south":{"press":{"type":"keys","keys":"B"}},"west":{"block":true}},"third":{"south":{"press":{"type":"keys","keys":"C"}}}}}`
	for _, tc := range []struct{ name, script, want string }{
		{"toggle layer", "+east@0 -east@1 +south@2 -south@3 +east@4 -east@5 +south@6 -south@7", "@2 down decl/2 B, @3 up decl/2 B, @6 down decl/4 A, @7 up decl/4 A"},
		{"cycle common", "+leftstick@0 -leftstick@1 +south@2 -south@3 +leftstick@4 -leftstick@5 +south@6 -south@7 +leftstick@8 -leftstick@9 +south@10 -south@11", "@2 down decl/2 B, @3 up decl/2 B, @6 down decl/4 C, @7 up decl/4 C, @10 down decl/6 A, @11 up decl/6 A"},
		{"block consumes", "+east@0 -east@1 +west@2 -west@3 +east@4 -east@5 +west@6 -west@7", "@6 down decl/4 Shift"},
		{"default layer", "+leftstick@0 -leftstick@1 +south@2 -south@3", "@2 down decl/2 B, @3 up decl/2 B"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog := compileLayers(t, src)
			r := &recorder{}
			e := engine.New(prog, r, engine.Options{})
			if tc.name == "default layer" {
				e.Reload(0, prog)
			}
			runLayerScript(t, e, r, tc.script)
			if got := r.String(); got != tc.want {
				t.Fatalf("got %s; want %s", got, tc.want)
			}
		})
	}
}

func TestM3LayerTapPressOwnerAndCarry(t *testing.T) {
	prog := compileLayers(t, `{"schemaVersion":0,"name":"tap","layers":{"base":{"leftshoulder":{"tap":{"type":"keys","keys":"F13"},"hold":{"type":"layer","layer":"alt","op":"hold"}},"south":{"press":{"type":"keys","keys":"A"}},"east":{"press":{"type":"layer","layer":"alt","op":"toggle"}}},"alt":{"south":{"press":{"type":"keys","keys":"B"}},"east":{"press":{"type":"keys","keys":"C"}}}}}`)
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{})
	runLayerScript(t, e, r, "+leftshoulder@0 +south@30 -leftshoulder@40 -south@50")
	if got, want := r.String(), "@30 down decl/2 B, @50 up decl/2 B"; got != want {
		t.Fatalf("layer-tap got %s; want %s", got, want)
	}
	r = &recorder{}
	e = engine.New(prog, r, engine.Options{})
	runLayerScript(t, e, r, "+south@0 +east@10 -east@11 -south@20")
	if got, want := r.String(), "@0 down decl/1 A, @20 up decl/1 A"; got != want {
		t.Fatalf("press-owner got %s; want %s", got, want)
	}
}

func TestM3DefaultLayer(t *testing.T) {
	prog := compileLayers(t, `{"schemaVersion":0,"name":"default","defaultLayer":"alt","layers":{"base":{"south":{"press":{"type":"keys","keys":"A"}}},"alt":{"south":{"press":{"type":"keys","keys":"B"}}}}}`)
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{})
	runLayerScript(t, e, r, "+south@0 -south@1")
	if got, want := r.String(), "@0 down decl/1 B, @1 up decl/1 B"; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
}

func TestM3LatchAcrossLayer(t *testing.T) {
	prog := compileLayers(t, `{"schemaVersion":0,"name":"latch-layer","layers":{"base":{"east":{"latch":true,"press":{"type":"keys","keys":"Shift"}},"leftstick":{"press":{"type":"layer","layer":"alt","op":"toggle"}}},"alt":{"east":{"press":{"type":"keys","keys":"B"}}}}}`)
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{})
	runLayerScript(t, e, r, "+east@0 -east@1 +leftstick@2 -leftstick@3 +east@4 -east@5")
	if got, want := r.String(), "@0 down decl/1 Shift, @4 up decl/1 Shift"; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
}

func TestM3LayerChangeNotification(t *testing.T) {
	prog := compileLayers(t, `{"schemaVersion":0,"name":"notify","layers":{"base":{"east":{"press":{"type":"layer","layer":"alt","op":"toggle"}}},"alt":{"color":"#112233"}}}`)
	r := &recorder{}
	var got []engine.LayerState
	e := engine.New(prog, r, engine.Options{LayerChange: func(s engine.LayerState) { got = append(got, s) }})
	runLayerScript(t, e, r, "+east@0 -east@1 +east@2 -east@3")
	if len(got) != 2 || got[0].Base != "base" || len(got[0].Overlays) != 1 || got[0].Overlays[0] != "alt" || got[0].Color != "#112233" || len(got[1].Overlays) != 0 {
		t.Fatalf("layer notifications = %+v", got)
	}
}

func TestM3FeedbackPulsesAndPause(t *testing.T) {
	prog := compileLayers(t, `{"schemaVersion":0,"name":"feedback","feedback":{"shortMs":80,"longMs":240,"gapMs":120,"strength":12345,"pausedColor":"#AA5500"},"common":{"leftstick":{"press":{"type":"layer","op":"cycle","layers":["base","alt","third"]}}},"layers":{"base":{"east":{"press":{"type":"layer","layer":"alt","op":"toggle"}}},"alt":{"color":"#112233"},"third":{"color":"#334455"}}}`)
	r := &recorder{}
	var got []engine.Feedback
	e := engine.New(prog, r, engine.Options{Feedback: func(f engine.Feedback) { got = append(got, f) }})
	runLayerScript(t, e, r, "+east@0 -east@1")
	runLayerScript(t, e, r, "+east@2 -east@3 +leftstick@4 -leftstick@5 +leftstick@6 -leftstick@7")
	e.Pause(2)
	e.Resume()
	if len(got) != 6 || !got[0].Pulse || got[0].Pulses != 2 || got[0].PulseMs != 80 || got[0].GapMs != 120 || got[0].Strength != 12345 || got[0].Color != "#112233" || !got[1].Pulse || got[1].Pulses != 1 || got[1].PulseMs != 240 || got[1].Color != "" || !got[2].Pulse || got[2].Pulses != 2 || got[3].Pulses != 3 || got[3].Color != "#334455" || !got[4].Paused || got[4].Color != "#AA5500" || !got[4].Pulse || got[5].Paused || got[5].Color != "#334455" || !got[5].Pulse {
		t.Fatalf("feedback = %+v", got)
	}
}

func TestM3FeedbackCanBeDisabled(t *testing.T) {
	prog := compileLayers(t, `{"schemaVersion":0,"name":"feedback-off","layers":{"base":{"east":{"press":{"type":"layer","layer":"alt","op":"toggle"}}},"alt":{"color":"#112233"}}}`)
	var got []engine.Feedback
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{Feedback: func(f engine.Feedback) { got = append(got, f) }, Switches: switchesOff("rumble", "led")})
	runLayerScript(t, e, r, "+east@0 -east@1")
	e.Pause(2)
	if len(got) == 0 {
		t.Fatal("no feedback state was published")
	}
	for _, f := range got {
		if f.Pulse || f.Color != "" {
			t.Fatalf("disabled feedback still emitted an effect: %+v", f)
		}
	}
}

func TestM3CommonCarry(t *testing.T) {
	prog := compileLayers(t, `{"schemaVersion":0,"name":"common","common":{"north":{"press":{"type":"keys","keys":"C"}}},"layers":{"base":{"south":{"press":{"type":"keys","keys":"A"}}},"alt":{}}}`)
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{})
	runLayerScript(t, e, r, "+north@0 -north@1 +south@2 -south@3")
	if got, want := r.String(), "@0 down decl/1 C, @1 up decl/1 C, @2 down decl/2 A, @3 up decl/2 A"; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
}

type fakeLevel struct {
	name     string
	bindings map[control.Control]*profile.Binding
}

func (l fakeLevel) Name() string                               { return l.name }
func (l fakeLevel) Binding(c control.Control) *profile.Binding { return l.bindings[c] }

func TestM3FakeRegistryConsumesSouthTap(t *testing.T) {
	prog := compileLayers(t, `{"schemaVersion":0,"name":"fake","layers":{"base":{}}}`)
	r := &recorder{}
	b := &profile.Binding{ID: "/fake/south", Control: control.South, Block: true}
	e := engine.New(prog, r, engine.Options{Registry: fakeRegistry{levels: []engine.Level{fakeLevel{name: "base", bindings: map[control.Control]*profile.Binding{control.South: b}}}}})
	runLayerScript(t, e, r, "+south@0 -south@1")
	if r.String() != "" {
		t.Fatalf("fake registry output %s; want none", r.String())
	}
}

type fakeRegistry struct{ levels []engine.Level }

func (r fakeRegistry) Levels() []engine.Level { return r.levels }

func TestLayerOwnershipBoundaries(t *testing.T) {
	const src = `{"schemaVersion":0,"name":"ownership","common":{
	 "leftshoulder":{"press":{"type":"layer","layer":"alt","op":"hold"}},
	 "rightshoulder":{"press":{"type":"layer","layer":"alt","op":"hold"}},
	 "east":{"press":{"type":"layer","layer":"alt","op":"toggle"}},
	 "leftstick":{"press":{"type":"layer","layer":"other","op":"switch"}}},
	 "layers":{"base":{"south":{"press":{"type":"keys","keys":"A"}}},
	 "alt":{"south":{"press":{"type":"keys","keys":"B"}}},
	 "other":{"south":{"press":{"type":"keys","keys":"C"}}}}}`
	for _, tc := range []struct{ name, steps, want string }{
		{"two holders", "+leftshoulder@0 +rightshoulder@1 -leftshoulder@2 +south@3 -south@4 -rightshoulder@5 +south@6 -south@7", "@3 down decl/3 B, @4 up decl/3 B, @6 down decl/4 A, @7 up decl/4 A"},
		{"toggle survives hold", "+leftshoulder@0 +east@1 -east@2 -leftshoulder@3 +south@4 -south@5", "@4 down decl/3 B, @5 up decl/3 B"},
		{"hold survives toggle off", "+east@0 -east@1 +leftshoulder@2 +east@3 -east@4 +south@5 -south@6 -leftshoulder@7 +south@8 -south@9", "@5 down decl/4 B, @6 up decl/4 B, @8 down decl/5 A, @9 up decl/5 A"},
		{"pause clears toggle", "+east@0 -east@1 pause@2 resume@3 +south@4 -south@5", "@4 down decl/2 A, @5 up decl/2 A"},
		{"switch retains hold owner", "+leftshoulder@0 +south@1 +leftstick@2 -leftstick@3 -leftshoulder@4 -south@5 +south@6 -south@7", "@1 down decl/2 B, @5 up decl/2 B, @6 down decl/4 C, @7 up decl/4 C"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileLayers(t, src), r, engine.Options{})
			runM2(t, e, r, tc.steps)
			if got := r.String(); got != tc.want {
				t.Fatalf("got %s; want %s", got, tc.want)
			}
			e.CancelAll(r.now)
		})
	}
}

func TestLayerPartialBindingAndBlock(t *testing.T) {
	for _, block := range []bool{false, true} {
		alt := `"south":{"release":{"type":"keys","keys":"F14"}}`
		want := "@2 tap decl/2 F14 20ms"
		if block {
			alt = `"south":{"block":true}`
			want = ""
		}
		p := compileLayers(t, `{"schemaVersion":0,"name":"partial","common":{"east":{"press":{"type":"layer","layer":"alt","op":"hold"}}},"layers":{"base":{"south":{"press":{"type":"keys","keys":"F13"}}},"alt":{`+alt+`}}}`)
		r := &recorder{}
		e := engine.New(p, r, engine.Options{})
		runM2(t, e, r, "+east@0 +south@1 -south@2 -east@3")
		if r.String() != want {
			t.Errorf("block=%v: got %s; want %s", block, r.String(), want)
		}
	}
}

func TestLayerAndKeyHoldReleaseTogether(t *testing.T) {
	p := compileLayers(t, `{"schemaVersion":0,"name":"mixed","layers":{"base":{"west":{"press":{"type":"keys","keys":"Shift"},"tap":{"type":"keys","keys":"F13"},"hold":{"type":"layer","layer":"alt","op":"hold"}},"south":{"press":{"type":"keys","keys":"A"}}},"alt":{"south":{"press":{"type":"keys","keys":"B"}}}}}`)
	r := &recorder{}
	e := engine.New(p, r, engine.Options{})
	runM2(t, e, r, "+west@0 wait@300 -west@310 +south@320 -south@330")
	if want := "@0 down decl/1 Shift, @310 up decl/1 Shift, @320 down decl/2 A, @330 up decl/2 A"; r.String() != want {
		t.Fatalf("got %s; want %s", r.String(), want)
	}
}
