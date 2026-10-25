package engine_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Tests of the haptics a binding gives (MAP-25) and of the knob tick
// (HUD-10), ADR-0065: Options.Haptic is called once per declarative dispatch
// of a binding with "haptic", and once per knob step with knobTick.

// compileHaptic compiles a profile with the given feedback, analog and base
// layer bindings; an empty feedback or analog is "{}".
func compileHaptic(t testing.TB, feedback, analog string, bindings ...string) *engine.Program {
	t.Helper()
	if feedback == "" {
		feedback = "{}"
	}
	if analog == "" {
		analog = "{}"
	}
	src := `{"schemaVersion":0,"name":"haptic","feedback":` + feedback + `,"analog":` + analog +
		`,"layers":{"base":{` + strings.Join(bindings, ",") + `},"fn":{"south":{"press":{"type":"keys","keys":"F20"}}}}}`
	return compileProfile(t, src)
}

// hapticLog collects what Options.Haptic was called with.
type hapticLog struct{ calls []engine.Haptic }

func (l *hapticLog) options(off ...string) engine.Options {
	return engine.Options{Haptic: func(h engine.Haptic) { l.calls = append(l.calls, h) }, Switches: switchesOff(off...)}
}

func (l *hapticLog) presets() []string {
	var out []string
	for _, h := range l.calls {
		out = append(out, h.Preset.String())
	}
	return out
}

func newHapticDriver(prog *engine.Program) (*gestureDriver, *hapticLog, *counter) {
	l, out := &hapticLog{}, &counter{}
	return &gestureDriver{e: engine.New(prog, out, l.options())}, l, out
}

// TestBindingHapticFires: each gesture of a binding with a haptic plays it
// once per dispatch, with the strength and pulse timing of the profile's
// feedback, and a binding without one plays nothing.
func TestBindingHapticFires(t *testing.T) {
	prog := compileHaptic(t, `{"strength":20000,"shortMs":50,"gapMs":80}`, "",
		`"south":{"haptic":"tick","press":{"type":"keys","keys":"A","mode":"tap"}}`,
		`"east":{"haptic":"confirm","release":{"type":"keys","keys":"B"}}`,
		`"west":{"haptic":"pulse2","tap":{"type":"keys","keys":"C"},"doubleTap":{"type":"keys","keys":"D"},"tripleTap":{"type":"keys","keys":"E"},"quadTap":{"type":"keys","keys":"F"}}`,
		`"north":{"haptic":"error","tap":{"type":"keys","keys":"G"},"hold":{"type":"keys","keys":"H"}}`,
		`"lb":{"haptic":"pulse4","press":{"type":"layer","layer":"fn","op":"toggle"}}`,
		`"rb":{"press":{"type":"keys","keys":"I","mode":"tap"}}`,
		`"dpad.up":{"block":true,"haptic":"tick"}`,
		`"leftstick":{"haptic":"tick","press":{"type":"keys","keys":"J","mode":"tap"},"hold":{"type":"keys","keys":"K","mode":"tap"}}`,
	)
	type tc struct {
		name string
		run  func(d *gestureDriver)
		want []engine.Haptic
	}
	h := func(p haptic.Preset) engine.Haptic {
		return engine.Haptic{Preset: p, Strength: 20000, ShortMs: 50, GapMs: 80}
	}
	for _, c := range []tc{
		{"press", func(d *gestureDriver) { d.press(control.South); d.wait(10); d.release(control.South); d.settle() }, []engine.Haptic{h(haptic.Tick)}},
		{"release", func(d *gestureDriver) { d.press(control.East); d.wait(10); d.release(control.East); d.settle() }, []engine.Haptic{h(haptic.Confirm)}},
		{"tap", func(d *gestureDriver) { d.press(control.West); d.wait(10); d.release(control.West); d.settle() }, []engine.Haptic{h(haptic.Pulse2)}},
		{"double tap", func(d *gestureDriver) {
			d.press(control.West)
			d.wait(10)
			d.release(control.West)
			d.wait(50)
			d.press(control.West)
			d.wait(10)
			d.release(control.West)
			d.settle()
		}, []engine.Haptic{h(haptic.Pulse2)}},
		{"triple tap", func(d *gestureDriver) {
			for range 3 {
				d.press(control.West)
				d.wait(10)
				d.release(control.West)
				d.wait(50)
			}
			d.settle()
		}, []engine.Haptic{h(haptic.Pulse2)}},
		{"quad tap", func(d *gestureDriver) {
			for range 4 {
				d.press(control.West)
				d.wait(10)
				d.release(control.West)
				d.wait(50)
			}
			d.settle()
		}, []engine.Haptic{h(haptic.Pulse2)}},
		{"tap of a binding with a hold", func(d *gestureDriver) { d.press(control.North); d.wait(10); d.release(control.North); d.settle() }, []engine.Haptic{h(haptic.Error)}},
		{"hold", func(d *gestureDriver) { d.press(control.North); d.wait(300); d.release(control.North); d.settle() }, []engine.Haptic{h(haptic.Error)}},
		{"layer action", func(d *gestureDriver) { d.press(control.LeftShoulder); d.release(control.LeftShoulder); d.settle() }, []engine.Haptic{h(haptic.Pulse4)}},
		{"no haptic", func(d *gestureDriver) { d.press(control.RightShoulder); d.release(control.RightShoulder); d.settle() }, nil},
		{"block binding", func(d *gestureDriver) { d.press(control.DpadUp); d.wait(10); d.release(control.DpadUp); d.settle() }, nil},
		{"unbound", func(d *gestureDriver) { d.press(control.RightStick); d.release(control.RightStick); d.settle() }, nil},
		// The press runs and commits the hold at the same instant: one moment,
		// one haptic. A later press is a new cycle and plays again.
		{"press and hold at one instant", func(d *gestureDriver) {
			d.press(control.LeftStick)
			d.wait(10)
			d.release(control.LeftStick)
			d.wait(10)
			d.press(control.LeftStick)
			d.wait(10)
			d.release(control.LeftStick)
			d.settle()
		}, []engine.Haptic{h(haptic.Tick), h(haptic.Tick)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, l, _ := newHapticDriver(prog)
			c.run(d)
			if !slices.Equal(l.calls, c.want) {
				t.Errorf("haptics = %+v; want %+v", l.calls, c.want)
			}
		})
	}
}

// A press whose gesture a script claims is not a dispatch of the binding, so
// it plays nothing (the script has pad.haptic).
func TestHapticNotWhenAScriptConsumes(t *testing.T) {
	prog := compileHaptic(t, "", "", `"south":{"haptic":"tick","press":{"type":"keys","keys":"A","mode":"tap"}}`)
	l := &hapticLog{}
	opts := l.options()
	opts.Button = func(ev engine.ButtonEvent) bool { return ev.Control == control.South }
	e := engine.New(prog, &counter{}, opts)
	d := &gestureDriver{e: e}
	d.press(control.South)
	d.release(control.South)
	d.settle()
	if len(l.calls) != 0 {
		t.Errorf("a consumed press played %+v", l.calls)
	}
}

// The typematic repeat of a held key is not a dispatch: the press plays the
// haptic once, however long the key repeats.
func TestHapticNotOnRepeat(t *testing.T) {
	prog := compileHaptic(t, "", "",
		`"east":{"haptic":"tick","press":{"type":"keys","keys":"A","repeat":{"delayMs":30,"rateHz":50}}}`,
		`"dpad.right":{"haptic":"tick","press":{"type":"keys","keys":"F21","repeat":{"style":"tap","delayMs":0,"rateHz":50}}}`,
	)
	for _, c := range []control.Control{control.East, control.DpadRight} {
		d, l, out := newHapticDriver(prog)
		d.press(c)
		d.wait(400)
		d.release(c)
		d.settle()
		if out.repeats < 5 {
			t.Fatalf("%s: %d repeats; the test needs the key to repeat", c, out.repeats)
		}
		if got := l.presets(); !slices.Equal(got, []string{"tick"}) {
			t.Errorf("%s: haptics %v after %d repeats; want one tick", c, got, out.repeats)
		}
	}
}

// Nothing plays while the engine is paused, neither for a press nor for a
// gesture that was waiting when the pause came; the first press after the
// resume plays again.
func TestHapticNotWhilePaused(t *testing.T) {
	prog := compileHaptic(t, "", "",
		`"south":{"haptic":"tick","press":{"type":"keys","keys":"A","mode":"tap"}}`,
		`"west":{"haptic":"confirm","tap":{"type":"keys","keys":"C"},"doubleTap":{"type":"keys","keys":"D"}}`,
	)
	d, l, _ := newHapticDriver(prog)
	// A tap waits for the double-tap window; the pause comes first.
	d.press(control.West)
	d.wait(10)
	d.release(control.West)
	d.wait(20)
	d.e.Pause(d.now)
	d.press(control.South)
	d.release(control.South)
	d.settle()
	if len(l.calls) != 0 {
		t.Fatalf("paused: haptics %v", l.presets())
	}
	d.e.Resume()
	d.press(control.South)
	d.release(control.South)
	d.settle()
	if got := l.presets(); !slices.Equal(got, []string{"tick"}) {
		t.Errorf("after the resume: haptics %v; want one tick", got)
	}
}

// The global rumble switch off, or a strength of 0 in the profile, silences
// every haptic of the profile: the bindings and the knob. The LED switch does
// not, and neither do the switches a profile's feedback once held (ADR-0067).
func TestHapticSilencedByRumbleOff(t *testing.T) {
	bindings := []string{
		`"south":{"haptic":"tick","press":{"type":"keys","keys":"A","mode":"tap"}}`,
		`"rs.cw":{"press":{"type":"keys","keys":"]","mode":"tap"}}`,
		`"rs.ccw":{"press":{"type":"keys","keys":"[","mode":"tap"}}`,
	}
	analog := `{"rs":{"mode":"knob","knobTick":true}}`
	for _, tc := range []struct {
		name, feedback string
		off            []string
		sound          bool
	}{
		{"default", `{}`, nil, true},
		{"rumble off", `{}`, []string{"rumble"}, false},
		{"strength 0", `{"strength":0}`, nil, false},
		{"led off", `{}`, []string{"led"}, true},
		{"pause off", `{}`, []string{"pause"}, true},
		{"rumble off, strength set", `{"strength":65535}`, []string{"rumble"}, false},
		{"profile rumble false is ignored", `{"rumble":false}`, nil, true},
		{"profile rumble true does not turn the global off", `{"rumble":true}`, []string{"rumble"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog := compileHaptic(t, tc.feedback, analog, bindings...)
			l, r := &hapticLog{}, &recorder{}
			e := engine.New(prog, r, l.options(tc.off...))
			(&gestureDriver{e: e}).press(control.South)
			(&gestureDriver{e: e}).release(control.South)
			stickAt(e, r, true, 0, 0.5, 100)
			stickAt(e, r, true, 40, 0.5, 110)
			advance(e, r, 200)
			if got := len(l.calls) > 0; got != tc.sound {
				t.Errorf("haptics %v; want sound=%v", l.presets(), tc.sound)
			}
			if tc.sound && len(l.calls) != 2 {
				t.Errorf("haptics %v; want the key's and one knob tick", l.presets())
			}
			// Silenced or not, the keys are the same.
			if got := len(downs(r, "down decl/")) + len(downs(r, "tap decl/")); got != 2 {
				t.Errorf("%d key commands in %s; want 2", got, r)
			}
		})
	}
}

// The rumble switch is asked at each haptic: a change of config.json applies to
// the next binding, with no new Program.
func TestHapticFollowsTheSwitchLive(t *testing.T) {
	prog := compileHaptic(t, "", "", `"south":{"haptic":"tick","press":{"type":"keys","keys":"A","mode":"tap"}}`)
	sw := profile.DefaultFeedbackSwitches()
	l, r := &hapticLog{}, &recorder{}
	opts := l.options()
	opts.Switches = func() profile.FeedbackSwitches { return sw }
	d := &gestureDriver{e: engine.New(prog, r, opts)}
	tap := func() {
		d.press(control.South)
		d.release(control.South)
	}
	tap()
	sw.Rumble = false
	tap()
	sw.Rumble = true
	tap()
	if len(l.calls) != 2 {
		t.Errorf("haptics %v; want the first and the third tap only", l.presets())
	}
}

// An input preview (the engine of a cold script build) never plays.
func TestHapticInputOnlyNeverCalls(t *testing.T) {
	prog := compileHaptic(t, "", `{"rs":{"mode":"knob","knobTick":true}}`,
		`"south":{"haptic":"tick","press":{"type":"keys","keys":"A","mode":"tap"}}`,
		`"rs.cw":{"press":{"type":"keys","keys":"]","mode":"tap"}}`,
	)
	l, r := &hapticLog{}, &recorder{}
	e := engine.New(prog, r, l.options())
	preview := e.PreviewInput(nil)
	preview.Step(engine.Input{Kind: engine.InputButton, Control: control.South, Down: true})
	preview.Step(engine.Input{T: clock.Time(5 * time.Millisecond), Kind: engine.InputButton, Control: control.South})
	preview.Step(engine.Input{T: clock.Time(10 * time.Millisecond), Kind: engine.InputAxis, Axis: control.RightX, Value: 0})
	preview.Step(engine.Input{T: clock.Time(10 * time.Millisecond), Kind: engine.InputAxis, Axis: control.RightY, Value: -20000})
	preview.Step(engine.Input{T: clock.Time(60 * time.Millisecond), Kind: engine.InputAxis, Axis: control.RightX, Value: 20000})
	preview.Apply(clock.Time(200 * time.Millisecond))
	if len(l.calls) != 0 || len(r.out) != 0 {
		t.Errorf("an input preview played %v and sent %s", l.presets(), r)
	}
	// The engine it was taken from still plays.
	d := &gestureDriver{e: e}
	d.press(control.South)
	d.release(control.South)
	d.settle()
	if got := l.presets(); !slices.Equal(got, []string{"tick"}) {
		t.Errorf("the engine played %v; want one tick", got)
	}
}

// Reload switches the program the haptics come from: its binding and its
// strength (a strength of 0 is the quiet profile).
func TestHapticFollowsReload(t *testing.T) {
	loud := compileHaptic(t, `{"strength":30000}`, "", `"south":{"haptic":"tick","press":{"type":"keys","keys":"A","mode":"tap"}}`)
	quiet := compileHaptic(t, `{"strength":0}`, "", `"south":{"haptic":"tick","press":{"type":"keys","keys":"A","mode":"tap"}}`)
	d, l, _ := newHapticDriver(loud)
	tap := func() {
		d.press(control.South)
		d.release(control.South)
		d.settle()
	}
	tap()
	d.e.Reload(d.now, quiet)
	tap()
	d.e.Reload(d.now, loud)
	tap()
	if len(l.calls) != 2 || l.calls[0].Strength != 30000 {
		t.Errorf("haptics %+v; want two with strength 30000 around the quiet profile", l.calls)
	}
}

// TestKnobTickHaptic: with knobTick, each knob step that sends a key plays a
// tick just before the key; a step of a direction nothing binds plays none
// (it makes no key), and neither does the knob without knobTick.
func TestKnobTickHaptic(t *testing.T) {
	log := func(r *recorder) func(engine.Haptic) {
		return func(h engine.Haptic) { r.add("haptic " + h.Preset.String()) }
	}
	t.Run("one per step", func(t *testing.T) {
		r := &recorder{}
		e := engine.New(compileHaptic(t, "", `{"rs":{"mode":"knob","knobTick":true}}`, knobBind), r, engine.Options{Haptic: log(r)})
		stickAt(e, r, true, 0, 0.5, 0)
		stickAt(e, r, true, 35, 0.5, 100) // a step clockwise
		stickAt(e, r, true, -5, 0.5, 300) // 35 degrees back: counter-clockwise
		stickAt(e, r, true, 100, 0.5, 400)
		advance(e, r, 600)
		want := "@104 haptic tick, @104 down decl/1 ], @104 up decl/1 ], " +
			"@304 haptic tick, @304 down decl/2 [, @304 up decl/2 [, " +
			"@404 haptic tick, @404 down decl/3 ], @404 up decl/3 ], " +
			"@444 haptic tick, @444 down decl/4 ], @444 up decl/4 ], " +
			"@484 haptic tick, @484 down decl/5 ], @484 up decl/5 ]"
		if got := r.String(); got != want {
			t.Fatalf("got %s\nwant %s", got, want)
		}
	})
	t.Run("unbound steps", func(t *testing.T) {
		r := &recorder{}
		e := engine.New(compileHaptic(t, "", `{"rs":{"mode":"knob","knobTick":true}}`, holdKey("rs.ccw", "[")), r, engine.Options{Haptic: log(r)})
		stickAt(e, r, true, 0, 0.5, 0)
		stickAt(e, r, true, 100, 0.5, 5) // clockwise: rs.cw has no binding
		advance(e, r, 20)
		if r.String() != "" {
			t.Fatalf("an unbound tick sent %s", r)
		}
		stickAt(e, r, true, 50, 0.5, 30) // counter-clockwise: bound
		advance(e, r, 40)
		if got, want := r.String(), "@34 haptic tick, @34 down decl/1 [, @34 up decl/1 ["; got != want {
			t.Fatalf("got %s; want %s", got, want)
		}
	})
	// A step that no declarative binding runs plays no tick: one a script's
	// hook may take (Options.Button makes every tick control count as bound
	// for its key) or takes, a blocked tick control, a binding without an
	// action.
	t.Run("steps no binding runs", func(t *testing.T) {
		for _, c := range []struct {
			name, cw string
			hook     func(engine.ButtonEvent) bool
		}{
			{"script hook", "", func(engine.ButtonEvent) bool { return false }},
			{"script consumes", holdKey("rs.cw", "]"), func(ev engine.ButtonEvent) bool { return ev.Control == control.RSCW }},
			{"blocked", `"rs.cw":{"block":true}`, nil},
			{"no action", `"rs.cw":{"label":"cw"}`, nil},
		} {
			t.Run(c.name, func(t *testing.T) {
				r := &recorder{}
				opts := engine.Options{Haptic: log(r), Button: c.hook}
				bindings := []string{holdKey("rs.ccw", "[")}
				if c.cw != "" {
					bindings = append(bindings, c.cw)
				}
				e := engine.New(compileHaptic(t, "", `{"rs":{"mode":"knob","knobTick":true}}`, bindings...), r, opts)
				stickAt(e, r, true, 0, 0.5, 0)
				stickAt(e, r, true, 100, 0.5, 5) // clockwise
				advance(e, r, 200)
				if strings.Contains(r.String(), "haptic") {
					t.Fatalf("a step no binding runs played %s", r)
				}
			})
		}
	})
	// A binding that runs once for two steps (doubleTap only) ticks once,
	// with its key: the tick follows the dispatch, not the step.
	t.Run("one tick per dispatch", func(t *testing.T) {
		r := &recorder{}
		e := engine.New(compileHaptic(t, "", `{"rs":{"mode":"knob","knobTick":true}}`,
			`"rs.cw":{"doubleTap":{"type":"keys","keys":"]"}}`), r, engine.Options{Haptic: log(r)})
		stickAt(e, r, true, 0, 0.5, 0)
		stickAt(e, r, true, 80, 0.5, 5) // two steps clockwise
		advance(e, r, 1000)
		if got := strings.Count(r.String(), "haptic tick"); got != 1 || !strings.Contains(r.String(), "]") {
			t.Fatalf("got %s; want one tick with the double tap's key", r)
		}
	})
	t.Run("off", func(t *testing.T) {
		for _, analog := range []string{`{"rs":{"mode":"knob"}}`, `{"rs":{"mode":"knob","knobTick":false}}`} {
			r := &recorder{}
			e := engine.New(compileHaptic(t, "", analog, knobBind), r, engine.Options{Haptic: log(r)})
			stickAt(e, r, true, 0, 0.5, 0)
			stickAt(e, r, true, 35, 0.5, 100)
			advance(e, r, 200)
			if got, want := r.String(), "@104 down decl/1 ], @104 up decl/1 ]"; got != want {
				t.Errorf("%s: got %s; want %s", analog, got, want)
			}
		}
	})
	t.Run("each stick", func(t *testing.T) {
		r := &recorder{}
		e := engine.New(compileHaptic(t, "", `{"ls":{"mode":"knob"},"rs":{"mode":"knob","knobTick":true}}`,
			knobBind, holdKey("ls.cw", "F5")), r, engine.Options{Haptic: log(r)})
		stickAt(e, r, false, 0, 0.5, 0)
		stickAt(e, r, false, 35, 0.5, 100) // the left stick has no knobTick
		advance(e, r, 150)
		stickAt(e, r, true, 0, 0.5, 200)
		stickAt(e, r, true, 35, 0.5, 300)
		advance(e, r, 400)
		want := "@104 down decl/1 F5, @104 up decl/1 F5, @304 haptic tick, @304 down decl/2 ], @304 up decl/2 ]"
		if got := r.String(); got != want {
			t.Fatalf("got %s\nwant %s", got, want)
		}
	})
	t.Run("a binding's own haptic wins", func(t *testing.T) {
		r := &recorder{}
		e := engine.New(compileHaptic(t, "", `{"rs":{"mode":"knob","knobTick":true}}`,
			`"rs.cw":{"haptic":"confirm","press":{"type":"keys","keys":"]","mode":"hold"}}`,
			`"rs.ccw":{"press":{"type":"keys","keys":"[","mode":"hold"}}`), r, engine.Options{Haptic: log(r)})
		stickAt(e, r, true, 0, 0.5, 0)
		stickAt(e, r, true, 35, 0.5, 100)
		stickAt(e, r, true, -5, 0.5, 300)
		advance(e, r, 400)
		want := "@104 haptic confirm, @104 down decl/1 ], @104 up decl/1 ], @304 haptic tick, @304 down decl/2 [, @304 up decl/2 ["
		if got := r.String(); got != want {
			t.Fatalf("got %s\nwant %s", got, want)
		}
	})
	t.Run("rate", func(t *testing.T) {
		// Three revolutions in 100 ms, as TestKnobRateLimit: a tick for each
		// key, never closer than the key can follow (40 ms), which also keeps
		// the controller's motors up with them.
		r := &recorder{}
		e := engine.New(compileHaptic(t, "", `{"rs":{"mode":"knob","knobTick":true}}`, knobBind), r, engine.Options{Haptic: log(r)})
		stickAt(e, r, true, 0, 0.5, 0)
		for ms := 1; ms <= 100; ms++ {
			advance(e, r, ms)
			stickAt(e, r, true, 10.8*float64(ms), 0.5, ms)
		}
		advance(e, r, 2000)
		keys, ticks := downs(r, "down decl/"), downs(r, "haptic tick")
		if len(keys) < 10 || !slices.Equal(keys, ticks) {
			t.Fatalf("keys at %v, ticks at %v; want a tick with each key", keys, ticks)
		}
		for i := 1; i < len(ticks); i++ {
			if gap := ticks[i] - ticks[i-1]; gap < 40 {
				t.Errorf("ticks %d and %d are %d ms apart; want at least 40", i-1, i, gap)
			}
		}
	})
}

// TestKnobTickOnlyInKnobMode: knobTick set on a stick that is not a knob does
// nothing, however the stick moves.
func TestKnobTickOnlyInKnobMode(t *testing.T) {
	for _, mode := range []string{"standard", "eightWay", "mouse"} {
		r := &recorder{}
		l := &hapticLog{}
		e := engine.New(compileHaptic(t, "", fmt.Sprintf(`{"rs":{"mode":%q,"knobTick":true}}`, mode), knobBind), r, l.options())
		for i := 0; i < 24; i++ {
			stickAt(e, r, true, float64(i*30), 0.8, i*20)
		}
		stickCenter(e, r, true, 600)
		advance(e, r, 1000)
		if len(l.calls) != 0 {
			t.Errorf("mode %s: the stick played %v", mode, l.presets())
		}
	}
}

// A knob that is turned while the engine is paused plays nothing, before or
// after the resume (the keys do not come either).
func TestKnobTickNotWhilePaused(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileHaptic(t, "", `{"rs":{"mode":"knob","knobTick":true}}`, knobBind), r, engine.Options{
		Haptic: func(h engine.Haptic) { r.add("haptic " + h.Preset.String()) },
	})
	stickAt(e, r, true, 0, 0.5, 0)
	e.Pause(clock.Time(time.Millisecond))
	stickAt(e, r, true, 100, 0.5, 5)
	advance(e, r, 200)
	e.Resume()
	advance(e, r, 300)
	if r.String() != "" {
		t.Fatalf("a paused knob played %s", r)
	}
}

// CheckBinding and Compile reject a binding whose haptic is not a preset, as
// Parse does an unknown name (TestParseAndCompileAgreeOnHaptic is the two
// together).
func TestCheckBindingHaptic(t *testing.T) {
	tap := &profile.Action{ID: "/b/press", Type: profile.ActionKeys, Mode: profile.ModeTap, Keys: keys.MustParse("A")}
	for _, tc := range []struct {
		name   string
		preset haptic.Preset
		ok     bool
	}{
		{"none", 0, true},
		{"tick", haptic.Tick, true},
		{"pulse4", haptic.Pulse4, true},
		{"one past the last", haptic.Pulse4 + 1, false},
		{"far", haptic.Preset(200), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &profile.Binding{ID: "/b", Control: control.West, Press: tap, Haptic: tc.preset}
			err := engine.CheckBinding(b)
			_, cerr := engine.Compile(&profile.Profile{Layers: []*profile.Layer{{Name: "base", Bindings: []*profile.Binding{b}}}})
			if (err == nil) != tc.ok || (cerr == nil) != tc.ok {
				t.Errorf("CheckBinding: %v; Compile: %v; want ok=%v", err, cerr, tc.ok)
			}
			if err != nil && !strings.Contains(err.Error(), "haptic") {
				t.Errorf("CheckBinding: %v does not name the haptic", err)
			}
		})
	}
}

// TestParseAndCompileAgreeOnHaptic: Parse refuses a haptic that is not a
// preset name exactly when Compile, and CheckBinding, refuse the same
// binding built by hand, so every profile Parse returns compiles
// (TestParseAndCompileAgree).
func TestParseAndCompileAgreeOnHaptic(t *testing.T) {
	var accepted, rejected int
	for _, name := range append([]string{"", "bogus", "Tick", "tick ", "pulse5", "pulse0", "none"}, haptic.PresetNames()...) {
		member := ""
		if name != "" {
			member = `"haptic": "` + name + `", `
		}
		src := `{"schemaVersion": 0, "name": "t", "layers": {"base": {"west": {` + member + `"press": {"type": "keys", "keys": "A"}}}}}`
		p, ds := profile.Parse([]byte(src))
		b := &profile.Binding{ID: "/layers/base/west", Control: control.West, Press: &profile.Action{
			ID: "/layers/base/west/press", Type: profile.ActionKeys, Mode: profile.ModeHold, Keys: keys.MustParse("A"),
		}}
		if name != "" {
			preset, ok := haptic.ParsePreset(name)
			if !ok {
				preset = haptic.Preset(99)
			}
			b.Haptic = preset
		}
		_, err := engine.Compile(&profile.Profile{Layers: []*profile.Layer{{Name: "base", Bindings: []*profile.Binding{b}}}})
		if ds.HasErrors() == (err == nil) {
			t.Errorf("haptic %q: Parse %v; Compile of the model: %v", name, ds, err)
			continue
		}
		if cerr := engine.CheckBinding(b); (cerr == nil) != (err == nil) {
			t.Errorf("haptic %q: CheckBinding %v; Compile %v", name, cerr, err)
		}
		if ds.HasErrors() {
			rejected++
			continue
		}
		accepted++
		if got := p.Layer("base").Binding(control.West).Haptic; got != b.Haptic {
			t.Errorf("haptic %q: Parse gives %v; the test built %v", name, got, b.Haptic)
		}
		if _, err := engine.Compile(p); err != nil {
			t.Errorf("haptic %q: Compile of the parsed profile: %v", name, err)
		}
	}
	if want := len(haptic.Presets()) + 1; accepted != want || rejected != 6 {
		t.Errorf("%d accepted and %d rejected; want %d and 6", accepted, rejected, want)
	}
}
