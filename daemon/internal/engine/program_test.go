package engine_test

import (
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

func TestCompileErrors(t *testing.T) {
	hold := func(id string) *profile.Action {
		return &profile.Action{ID: id + "/press", Type: profile.ActionKeys, Keys: keys.MustParse("Space"), Mode: profile.ModeHold}
	}
	bind := func(c control.Control, a *profile.Action) *profile.Binding {
		return &profile.Binding{ID: "/layers/base/" + c.String(), Control: c, Press: a}
	}
	base := func(bs ...*profile.Binding) *profile.Profile {
		return &profile.Profile{Name: "t", Layers: []*profile.Layer{{Name: "base", Bindings: bs}}}
	}
	for _, tc := range []struct {
		name string
		p    *profile.Profile
		want string
	}{
		{"nil profile", nil, "engine: no profile"},
		{"no layers", &profile.Profile{Name: "t"}, "engine: the profile has no base layer"},
		{"nil layer", &profile.Profile{Layers: []*profile.Layer{nil}}, "engine: layer 0 is nil"},
		{
			"two base layers", &profile.Profile{Layers: []*profile.Layer{{Name: "base"}, {Name: "base"}}},
			`engine: two layers are called "base"`,
		},
		{"nil binding", base(nil), `engine: layer "base": binding 0 is nil`},
		{
			"invalid control", base(&profile.Binding{ID: "/layers/base/x"}),
			"engine: binding /layers/base/x: invalid control control(0)",
		},
		{
			"control out of range", base(&profile.Binding{ID: "/layers/base/x", Control: control.Control(control.Count)}),
			"engine: binding /layers/base/x: invalid control control(" + strconv.Itoa(control.Count) + ")",
		},
		{
			"control bound twice",
			base(bind(control.South, nil), &profile.Binding{ID: "/layers/base/a", Control: control.South}),
			"engine: binding /layers/base/a: south is already bound by /layers/base/south",
		},
		{
			"unknown action type",
			base(bind(control.South, &profile.Action{ID: "/layers/base/south/press", Keys: keys.MustParse("Z"), Mode: profile.ModeTap})),
			"engine: action /layers/base/south/press: unsupported type actiontype(0)",
		},
		{
			"no keys",
			base(bind(control.South, &profile.Action{ID: "/layers/base/south/press", Type: profile.ActionKeys, Mode: profile.ModeTap})),
			"engine: action /layers/base/south/press: no keys",
		},
		{
			"invalid mode",
			base(bind(control.South, &profile.Action{ID: "/layers/base/south/press", Type: profile.ActionKeys, Keys: keys.MustParse("Z")})),
			"engine: action /layers/base/south/press: invalid mode mode(0)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := engine.Compile(tc.p)
			if err == nil || err.Error() != tc.want || prog != nil {
				t.Errorf("Compile = %v, %v; want nil, %s", prog, err, tc.want)
			}
		})
	}

	// A hand-built profile that is consistent compiles, whatever order its
	// bindings are in.
	p := base(bind(control.RT, hold("/layers/base/rt")), bind(control.South, nil))
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if prog.Profile() != p {
		t.Error("Profile() is not the compiled profile")
	}
	var ids []string
	for _, bi := range prog.Bindings() {
		ids = append(ids, bi.Binding.ID)
	}
	if want := []string{"/layers/base/south", "/layers/base/rt"}; !slices.Equal(ids, want) {
		t.Errorf("Bindings() = %v, want %v", ids, want)
	}
}

func TestBindings(t *testing.T) {
	// Written out of control order, with an alias and a binding without an
	// action.
	prog := compile(t, rtShift, xLabelOnly, `"a": {"press": {"type": "keys", "keys": "Z", "mode": "tap"}}`, ltAlt, eastHold)
	var got []string
	for _, bi := range prog.Bindings() {
		got = append(got, bi.Layer+" "+bi.Binding.ID+" "+bi.Binding.Control.String()+" "+bi.Gesture.String()+" +"+bi.AddedLatency.String())
	}
	want := []string{
		"base /layers/base/a south press +0s",
		"base /layers/base/east east press +0s",
		"base /layers/base/x west none +0s", // a label alone has no gesture
		"base /layers/base/lt lt press +0s",
		"base /layers/base/rt rt press +0s",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Bindings():\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// The caller owns the slice.
	bs := prog.Bindings()
	bs[0].Binding = nil
	if prog.Bindings()[0].Binding == nil {
		t.Error("changing the result of Bindings() changed the Program")
	}

	if n := len(compile(t).Bindings()); n != 0 {
		t.Errorf("an empty base layer has %d bindings", n)
	}
}

// TestParseAndCompileAgree checks that profile.Parse and Compile apply the
// same action rules (ARCHITECTURE §7.7): for every gesture, every keys
// action with mode absent, hold or tap, a KeySpec or an array, and with or
// without repeat, and every layer operation, Parse has no error exactly
// when Compile accepts the same action built by hand, and CheckBinding,
// which a Registry's levels use, agrees with Compile. So every profile
// Parse returns compiles.
func TestParseAndCompileAgree(t *testing.T) {
	// The same rules hold for the bindings of a layer and for those of the
	// function set (MAP-27), which Parse and Compile read the same way.
	for _, onSet := range []bool{false, true} {
		t.Run(map[bool]string{false: "layer", true: "function key set"}[onSet], func(t *testing.T) {
			testParseAndCompileAgree(t, onSet)
		})
	}
}

func testParseAndCompileAgree(t *testing.T, onSet bool) {
	gestures := []engine.Gesture{
		engine.GesturePress, engine.GestureRelease, engine.GestureTap, engine.GestureDoubleTap,
		engine.GestureTripleTap, engine.GestureQuadTap, engine.GestureHold,
	}
	// defaultMode is the mode of a keys action that sets none (ARCHITECTURE
	// §10.3).
	defaultMode := func(g engine.Gesture) profile.Mode {
		if g == engine.GesturePress || g == engine.GestureHold {
			return profile.ModeHold
		}
		return profile.ModeTap
	}
	var accepted, rejected int
	checkOn := func(c control.Control, g engine.Gesture, member string, want *profile.Action) {
		t.Helper()
		id := "/layers/base/" + c.String()
		binding := `{"` + g.String() + `": ` + member + `}`
		src := `{"schemaVersion": 0, "name": "t", "layers": {"base": {"` + c.String() + `": ` + binding + `}, "paint": {}}}`
		model := &profile.Profile{Layers: []*profile.Layer{{Name: "base"}, {Name: "paint"}}}
		b := &profile.Binding{ID: id, Control: c}
		if onSet {
			id = "/functionSet/" + c.String()
			b.ID = id
			src = `{"schemaVersion": 0, "name": "t", "layers": {"base": {}, "paint": {}}, "functionSet": {"` + c.String() + `": ` + binding + `}}`
			model.FunctionSet = &profile.Layer{Name: profile.FunctionSetName, Bindings: []*profile.Binding{b}}
		} else {
			model.Layers[0].Bindings = []*profile.Binding{b}
		}
		want.ID = id + "/" + g.String()
		p, ds := profile.Parse([]byte(src))
		setAction(b, g, want)
		_, err := engine.Compile(model)
		if ds.HasErrors() == (err == nil) {
			t.Errorf("%s %s: %s: Parse %v; Compile of the model: %v", c, g, member, ds, err)
			return
		}
		if cerr := engine.CheckBinding(b); (cerr == nil) != (err == nil) {
			t.Errorf("%s %s: %s: CheckBinding %v; Compile %v", c, g, member, cerr, err)
		}
		if ds.HasErrors() {
			rejected++
			return
		}
		accepted++
		// The model built by hand is the one Parse returns, which compiles
		// too.
		parsed := p.Layer("base")
		if onSet {
			parsed = p.FunctionSet
		}
		if got := parsed.Binding(c).Action(g); !reflect.DeepEqual(got, want) {
			t.Errorf("%s %s: %s: Parse gives %+v; the test built %+v", c, g, member, got, want)
		}
		if _, err := engine.Compile(p); err != nil {
			t.Errorf("%s %s: %s: Compile of the parsed profile: %v", c, g, member, err)
		}
	}
	check := func(g engine.Gesture, member string, want *profile.Action) {
		t.Helper()
		checkOn(control.West, g, member, want)
	}
	for _, g := range gestures {
		// A mouse button is held like a key but never repeats typematically:
		// both layers reject it (profile.MouseButtonRepeatMessage), so the
		// rule is checked for the buttons, a chord with one and a wheel key,
		// which repeats.
		for _, key := range []string{"A", "MouseLeft", "Ctrl+RButton", "XButton2", "WheelUp"} {
			for _, mode := range []string{"", "hold", "tap"} {
				for _, array := range []bool{false, true} {
					if array && key != "A" {
						continue
					}
					for _, repeat := range []bool{false, true} {
						member := `{"type": "keys"`
						want := &profile.Action{Type: profile.ActionKeys, Mode: defaultMode(g), Keys: keys.MustParse(key)}
						if array {
							member += `, "keys": ["Ctrl+K", "S"]`
							want.Keys = keys.MustParse("Ctrl+K")
							want.Sequence = []keys.Chord{want.Keys, keys.MustParse("S")}
						} else {
							member += `, "keys": "` + key + `"`
						}
						if mode != "" {
							member += `, "mode": "` + mode + `"`
							want.Mode = map[string]profile.Mode{"hold": profile.ModeHold, "tap": profile.ModeTap}[mode]
						}
						if repeat {
							member += `, "repeat": {"delayMs": 10, "rateHz": 5}`
							style := profile.RepeatTypematic
							if want.Mode == profile.ModeTap {
								style = profile.RepeatTap
							}
							want.Repeat = &profile.Repeat{Style: style, DelayMs: 10, DelaySet: true, RateHz: 5}
						}
						check(g, member+"}", want)
					}
				}
			}
		}
		for _, op := range []string{"hold", "toggle", "switch"} {
			check(g, `{"type": "layer", "layer": "paint", "op": "`+op+`"}`,
				&profile.Action{Type: profile.ActionLayer, Layer: "paint", Op: op})
		}
		check(g, `{"type": "layer", "layers": ["base", "paint"], "op": "cycle"}`,
			&profile.Action{Type: profile.ActionLayer, Layers: []string{"base", "paint"}, Op: "cycle"})
		check(g, `{"type": "macro", "file": "复制 (1).ts"}`, &profile.Action{Type: profile.ActionMacro, File: "复制 (1).ts"})
		check(g, `{"type": "macro", "file": "v1.2.ts"}`, &profile.Action{Type: profile.ActionMacro, File: "v1.2.ts"})
		// A sequence and a text hold nothing, so they fit every gesture; what is
		// wrong with one is wrong on all of them (MAP-22, OUT-21).
		check(g, `{"type": "sequence", "steps": [{"keys": "Ctrl+A"}, {"wait": 100}, {"text": "你好"}]}`, &profile.Action{Type: profile.ActionSequence, Steps: []keys.Step{
			keys.KeysStep(keys.MustParse("Ctrl+A")), keys.WaitStep(100 * time.Millisecond), keys.TextStep("你好")}})
		check(g, `{"type": "sequence", "steps": [{"wait": 1}, {"wait": 10000}]}`, &profile.Action{Type: profile.ActionSequence, Steps: []keys.Step{
			keys.WaitStep(time.Millisecond), keys.WaitStep(10 * time.Second)}})
		check(g, `{"type": "sequence", "steps": [{"keys": "VolumeUp"}]}`, &profile.Action{Type: profile.ActionSequence, Steps: []keys.Step{keys.KeysStep(keys.MustParse("VolumeUp"))}})
		check(g, `{"type": "text", "text": "a\tb\n😀"}`, &profile.Action{Type: profile.ActionText, Text: "a\tb\n😀"})
		check(g, `{"type": "text", "text": "`+strings.Repeat("字", 256)+`"}`, &profile.Action{Type: profile.ActionText, Text: strings.Repeat("字", 256)})
		for _, bad := range []string{
			`{"type": "sequence", "steps": []}`,
			`{"type": "sequence", "steps": [{"wait": 0}]}`,
			`{"type": "sequence", "steps": [{"wait": 10001}]}`,
			`{"type": "sequence", "steps": [{"keys": "Z+Ctrl"}]}`,
			`{"type": "sequence", "steps": [{"text": ""}]}`,
			`{"type": "sequence", "steps": [{"text": "a\rb"}]}`,
			`{"type": "sequence", "steps": [{"keys": "A", "wait": 5}]}`,
			`{"type": "sequence", "steps": [{"keys": "A"}], "mode": "tap"}`,
			`{"type": "sequence", "steps": [{"keys": "A"}], "repeat": {"rateHz": 5}}`,
			`{"type": "text", "text": ""}`,
			`{"type": "text", "text": "a\rb"}`,
			`{"type": "text", "text": "\u0007"}`,
			`{"type": "text", "text": "` + strings.Repeat("a", 257) + `"}`,
			`{"type": "text", "text": "` + strings.Repeat("😀", 129) + `"}`,
			`{"type": "text", "text": "a", "mode": "tap"}`,
		} {
			check(g, bad, &profile.Action{Type: profile.ActionText})
		}
		tooLong := make([]string, keys.MaxSteps+1)
		for i := range tooLong {
			tooLong[i] = `{"wait": 1}`
		}
		check(g, `{"type": "sequence", "steps": [`+strings.Join(tooLong, ",")+`]}`, &profile.Action{Type: profile.ActionSequence})
		// A mouse precision action lasts while the control is held, so it needs
		// a press or hold gesture; a jump is once and fits any.
		check(g, `{"type": "mouse", "op": "precision", "factor": 0.5}`, &profile.Action{Type: profile.ActionMouse, Op: "precision", Factor: 0.5})
		check(g, `{"type": "mouse", "op": "precision"}`, &profile.Action{Type: profile.ActionMouse, Op: "precision", Factor: 0.3})
		check(g, `{"type": "mouse", "op": "jump", "x": 0.5, "y": 0.25}`, &profile.Action{Type: profile.ActionMouse, Op: "jump", X: 0.5, Y: 0.25})
		// A HUD hints action lasts while the control is held too (HUD-03).
		check(g, `{"type": "hud", "op": "hints"}`, &profile.Action{Type: profile.ActionHUD, Op: "hints"})
	}
	// A repeat with minRateHz (ANA-12): not above the rate (12 when rateHz is
	// not written), and only on a control that has a depth.
	for _, c := range []control.Control{
		control.West, control.LT, control.LTFull, control.LSUp, control.RSActive, control.LSOuter, control.RightXNegative,
		control.LeftTriggerPositive, control.RSCW, control.LSCCW, control.DpadUpLeft, control.DpadDownRight,
	} {
		for _, g := range []engine.Gesture{engine.GesturePress, engine.GestureHold, engine.GestureTap} {
			for _, rep := range []struct {
				member string
				want   profile.Repeat
			}{
				{`{"rateHz": 20, "minRateHz": 3}`, profile.Repeat{RateHz: 20, MinRateHz: 3}},
				{`{"rateHz": 20, "minRateHz": 20}`, profile.Repeat{RateHz: 20, MinRateHz: 20}},
				{`{"rateHz": 20, "minRateHz": 21}`, profile.Repeat{RateHz: 20, MinRateHz: 21}},
				{`{"minRateHz": 12}`, profile.Repeat{MinRateHz: 12}},
				{`{"minRateHz": 13}`, profile.Repeat{MinRateHz: 13}},
				{`{"delayMs": 0, "rateHz": 2, "minRateHz": 1}`, profile.Repeat{DelaySet: true, RateHz: 2, MinRateHz: 1}},
			} {
				want := &profile.Action{Type: profile.ActionKeys, Mode: profile.ModeHold, Keys: keys.MustParse("A"), Repeat: &rep.want}
				want.Repeat.Style = profile.RepeatTypematic
				if g == engine.GestureTap {
					want.Mode, want.Repeat.Style = profile.ModeTap, profile.RepeatTap
				}
				checkOn(c, g, `{"type": "keys", "keys": "A", "mode": "`+want.Mode.String()+`", "repeat": `+rep.member+`}`, want)
			}
		}
	}
	if accepted == 0 || rejected == 0 {
		t.Errorf("%d actions accepted and %d rejected; want some of each", accepted, rejected)
	}
}

// TestCheckBinding checks what CheckBinding rejects besides the action
// rules, which TestParseAndCompileAgree covers.
func TestCheckBinding(t *testing.T) {
	tap := &profile.Action{Type: profile.ActionKeys, Mode: profile.ModeTap, Keys: keys.MustParse("A")}
	for _, tc := range []struct {
		name string
		b    *profile.Binding
		want string
	}{
		{"valid", &profile.Binding{ID: "/b", Control: control.West, Tap: tap}, ""},
		{"block only", &profile.Binding{ID: "/b", Control: control.West, Block: true}, ""},
		{"nil", nil, "engine: binding is nil"},
		{"invalid control", &profile.Binding{ID: "/b", Tap: tap}, "engine: binding /b: invalid control"},
		{"layer hold on tap", &profile.Binding{ID: "/b", Control: control.West, Tap: &profile.Action{ID: "/b/tap", Type: profile.ActionLayer, Layer: "fn", Op: "hold"}},
			"engine: action /b/tap: layer hold action is only valid for press/hold"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := engine.CheckBinding(tc.b)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("CheckBinding: %v", err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Fatalf("CheckBinding: %v; want %q", err, tc.want)
			}
		})
	}
}

// TestExamples checks that every example profile compiles and reports a
// binding for each binding in the file.
func TestExamples(t *testing.T) {
	files, err := filepath.Glob("../../../examples/profiles/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("examples/profiles not found; this test needs the whole repository")
	}
	for _, file := range files {
		p, ds, err := profile.Load(file)
		if err != nil || p == nil {
			t.Fatalf("%s: %v %v", file, err, ds)
		}
		prog, err := engine.Compile(p)
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		want := 0
		for _, l := range p.Layers {
			want += len(l.Bindings)
		}
		if p.Common != nil {
			want += len(p.Common.Bindings)
		}
		for _, set := range p.FunctionSets() {
			want += len(set.Bindings)
		}
		if got := len(prog.Bindings()); got != want {
			t.Errorf("%s: %d bindings, want %d", file, got, want)
		}
	}
}

// TestProgramShared checks that engines that share a Program do not
// change it.
func TestProgramShared(t *testing.T) {
	prog := compile(t, eastHold, southTap)
	before := prog.Bindings()
	for range 3 {
		r := &recorder{}
		run(t, engine.New(prog, r, engine.Options{}), r, "+east@0 +south@1 cancel@2 -east@3 reset@4")
	}
	if !slices.Equal(prog.Bindings(), before) {
		t.Error("running engines changed the Program")
	}
}
