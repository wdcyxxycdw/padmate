package engine_test

import (
	"math"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

func compileAnalog(t testing.TB, analog, bindings string) *engine.Program {
	t.Helper()
	src := `{"schemaVersion":0,"name":"analog","analog":` + analog + `,"layers":{"base":{` + bindings + `}}}`
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

func axisStep(e *engine.Engine, r *recorder, axis control.Axis, x int16, at int) {
	r.now = clock.Time(time.Duration(at) * time.Millisecond)
	e.Step(engine.Input{T: r.now, Kind: engine.InputAxis, Axis: axis, Value: x})
}

func TestStickStandardAndEightWay(t *testing.T) {
	bind := `"ls.up":{"press":{"type":"keys","keys":"U"}},"ls.right":{"press":{"type":"keys","keys":"R"}},"ls.upright":{"press":{"type":"keys","keys":"D"}}`
	for _, tc := range []struct {
		name, mode, want string
	}{
		{"standard diagonal", "standard", "@1 down decl/1 U, @1 down decl/2 R, @11 up decl/1 U, @11 up decl/2 R"},
		{"eight diagonal", "eightWay", "@1 down decl/1 D, @11 up decl/1 D"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileAnalog(t, `{"ls":{"mode":"`+tc.mode+`","directionDelayMs":1}}`, bind), r, engine.Options{})
			axisStep(e, r, control.LeftX, 24000, 0)
			axisStep(e, r, control.LeftY, -24000, 0)
			r.now = clock.Time(time.Millisecond)
			e.Apply(clock.Time(time.Millisecond))
			axisStep(e, r, control.LeftX, 0, 10)
			axisStep(e, r, control.LeftY, 0, 10)
			r.now = clock.Time(11 * time.Millisecond)
			e.Apply(clock.Time(11 * time.Millisecond))
			if got := r.String(); got != tc.want {
				t.Fatalf("got %s; want %s", got, tc.want)
			}
		})
	}
}

func TestStickModesAndDeadzone(t *testing.T) {
	bind := `"ls.up":{"press":{"type":"keys","keys":"U"}},"ls.right":{"press":{"type":"keys","keys":"R"}},"ls.upright":{"press":{"type":"keys","keys":"D"}}`
	for _, tc := range []struct{ mode, value, want string }{
		{"fourWay", "24000", "@1 down decl/1 R, @11 up decl/1 R"},
		{"fourWayDiagonal", "24000", "@1 down decl/1 D, @11 up decl/1 D"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileAnalog(t, `{"ls":{"mode":"`+tc.mode+`","directionDelayMs":1}}`, bind), r, engine.Options{})
			axisStep(e, r, control.LeftY, -24000, 0)
			axisStep(e, r, control.LeftX, 24000, 0)
			r.now = clock.Time(time.Millisecond)
			e.Apply(clock.Time(time.Millisecond))
			axisStep(e, r, control.LeftX, 0, 10)
			axisStep(e, r, control.LeftY, 0, 10)
			r.now = clock.Time(11 * time.Millisecond)
			e.Apply(clock.Time(11 * time.Millisecond))
			if got := r.String(); got != tc.want {
				t.Fatalf("got %s; want %s", got, tc.want)
			}
		})
	}
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"ls":{"deadzone":0.3,"saturation":0.8}}`, bind), r, engine.Options{})
	axisStep(e, r, control.LeftX, 8000, 0)
	if got := r.String(); got != "" {
		t.Fatalf("deadzone emitted %s", got)
	}
	axisStep(e, r, control.LeftX, 32767, 10)
	axisStep(e, r, control.LeftX, 0, 20)
	if got := r.String(); got != "@10 down decl/1 R, @20 up decl/1 R" {
		t.Fatalf("saturation/active got %s", got)
	}
}

func TestStickEightWayDiagonalFallback(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"ls":{"mode":"eightWay","diagonalFallback":"cardinals","directionDelayMs":1}}`, `"ls.up":{"press":{"type":"keys","keys":"U"}},"ls.right":{"press":{"type":"keys","keys":"R"}}`), r, engine.Options{})
	axisStep(e, r, control.LeftX, 24000, 0)
	axisStep(e, r, control.LeftY, -24000, 0)
	r.now = clock.Time(time.Millisecond)
	e.Apply(clock.Time(time.Millisecond))
	axisStep(e, r, control.LeftX, 0, 10)
	axisStep(e, r, control.LeftY, 0, 10)
	r.now = clock.Time(11 * time.Millisecond)
	e.Apply(clock.Time(11 * time.Millisecond))
	if got := r.String(); got != "@1 down decl/1 U, @1 down decl/2 R, @11 up decl/1 U, @11 up decl/2 R" {
		t.Fatalf("fallback got %s", got)
	}
}

func TestDpadModesAndFold(t *testing.T) {
	bind := `"dpad.up":{"press":{"type":"keys","keys":"U"}},"dpad.right":{"press":{"type":"keys","keys":"R"}},"dpad.upright":{"press":{"type":"keys","keys":"D"}}`
	for _, tc := range []struct{ name, mode, fold, want string }{
		{"buttons", "buttons", "keepFirst", "@0 down decl/1 U, @1 down decl/2 R, @2 up decl/1 U, @2 up decl/2 R"},
		{"eight", "eightWay", "keepFirst", "@0 down decl/1 U, @1 up decl/1 U, @1 down decl/2 D, @2 up decl/2 D, @2 down decl/3 R, @2 up decl/3 R"},
		{"four keep", "fourWay", "keepFirst", "@0 down decl/1 U, @2 up decl/1 U, @2 down decl/2 R, @2 up decl/2 R"},
		{"four clockwise", "fourWay", "clockwise", "@0 down decl/1 U, @1 up decl/1 U, @1 down decl/2 R, @2 up decl/2 R"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileAnalog(t, `{"dpad":{"mode":"`+tc.mode+`","fold":"`+tc.fold+`"}}`, bind), r, engine.Options{})
			r.now = 0
			e.Step(engine.Input{T: 0, Kind: engine.InputButton, Control: control.DpadUp, Down: true})
			r.now = clock.Time(time.Millisecond)
			e.Step(engine.Input{T: clock.Time(time.Millisecond), Kind: engine.InputButton, Control: control.DpadRight, Down: true})
			r.now = clock.Time(2 * time.Millisecond)
			e.Step(engine.Input{T: clock.Time(2 * time.Millisecond), Kind: engine.InputButton, Control: control.DpadUp, Down: false})
			e.Step(engine.Input{T: clock.Time(2 * time.Millisecond), Kind: engine.InputButton, Control: control.DpadRight, Down: false})
			if got := r.String(); got != tc.want {
				t.Fatalf("got %s; want %s", got, tc.want)
			}
		})
	}
}

func TestHalfAxisButtons(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"axisThreshold":0.3}`, `"leftx+":{"press":{"type":"keys","keys":"R"}},"leftx-":{"press":{"type":"keys","keys":"L"}},"lefttrigger+":{"press":{"type":"keys","keys":"T"}}`), r, engine.Options{})
	axisStep(e, r, control.LeftX, 10000, 0)
	axisStep(e, r, control.LeftX, 20000, 10)
	axisStep(e, r, control.LeftX, 0, 20)
	axisStep(e, r, control.LeftTrigger, 10000, 30)
	axisStep(e, r, control.LeftTrigger, 0, 40)
	if got, want := r.String(), "@0 down decl/1 R, @20 up decl/1 R, @30 down decl/2 T, @40 up decl/2 T"; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
}

func TestStickDirectionDelay(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"ls":{"directionDelayMs":10}}`, `"ls.up":{"press":{"type":"keys","keys":"U"}}`), r, engine.Options{})
	axisStep(e, r, control.LeftY, -32767, 0)
	if r.String() != "" {
		t.Fatalf("direction emitted before delay: %s", r.String())
	}
	r.now = clock.Time(10 * time.Millisecond)
	e.Apply(clock.Time(10 * time.Millisecond))
	if r.String() != "@10 down decl/1 U" {
		t.Fatalf("delayed direction = %s", r.String())
	}
	axisStep(e, r, control.LeftY, 0, 20)
	e.Apply(clock.Time(30 * time.Millisecond))
	if r.String() != "@10 down decl/1 U, @20 up decl/1 U" {
		t.Fatalf("release = %s", r.String())
	}
}

func TestStandardDiagonalHysteresisUsesDiagonalCenter(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"ls":{"mode":"standard","directionDelayMs":1,"hysteresisDeg":8}}`, `"ls.up":{"press":{"type":"keys","keys":"U"}},"ls.right":{"press":{"type":"keys","keys":"R"}}`), r, engine.Options{})
	axisStep(e, r, control.LeftX, 24000, 0)
	axisStep(e, r, control.LeftY, -24000, 0)
	r.now = clock.Time(time.Millisecond)
	e.Apply(clock.Time(time.Millisecond))
	if got, want := r.String(), "@1 down decl/1 U, @1 down decl/2 R"; got != want {
		t.Fatalf("initial diagonal = %s; want %s", got, want)
	}
	setAngle := func(deg float64, at int) {
		rad := deg * math.Pi / 180
		axisStep(e, r, control.LeftX, int16(math.Round(math.Sin(rad)*32767)), at)
		axisStep(e, r, control.LeftY, int16(math.Round(-math.Cos(rad)*32767)), at)
	}
	// 72° is in the right cardinal sector, but within 8° hysteresis of
	// the 45° diagonal center (75.5° boundary), so both directions remain.
	setAngle(72, 10)
	r.now = clock.Time(11 * time.Millisecond)
	e.Apply(clock.Time(11 * time.Millisecond))
	if got := r.String(); got != "@1 down decl/1 U, @1 down decl/2 R" {
		t.Fatalf("diagonal chatter at 72°: %s", got)
	}
	// At 90° the boundary is crossed and only right remains.
	setAngle(90, 20)
	r.now = clock.Time(21 * time.Millisecond)
	e.Apply(clock.Time(21 * time.Millisecond))
	if got, want := r.String(), "@1 down decl/1 U, @1 down decl/2 R, @21 up decl/1 U"; got != want {
		t.Fatalf("diagonal switch at 80° = %s; want %s", got, want)
	}
}

func TestCompilePreservesExplicitAnalogZero(t *testing.T) {
	p, ds := profile.Parse([]byte(`{"schemaVersion":0,"name":"zero","analog":{"ls":{"deadzone":0,"saturation":0.5,"directionThreshold":0,"hysteresisDeg":0},"axisThreshold":0,"lt":{"press":0,"release":0}},"layers":{"base":{}}}`))
	if p == nil || ds.HasErrors() {
		t.Fatalf("profile.Parse: %v", ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	a := prog.Analog()
	if a.LS.Deadzone != 0 || a.LS.DirectionThreshold != 0 || a.LS.HysteresisDeg != 0 || a.AxisThreshold != 0 || a.LT.Press != 0 || a.LT.Release != 0 {
		t.Errorf("Compile replaced explicit zeros: %+v", a)
	}
}

func TestCompileDirectProfileGetsAnalogDefaults(t *testing.T) {
	p := &profile.Profile{Layers: []*profile.Layer{{Name: "base"}}}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	a := prog.Analog()
	if a.LS.Mode != "standard" || a.LS.Deadzone != 0.15 || a.LS.Saturation != 0.95 || a.LS.DirectionThreshold != 0.30 || a.LT.Press != 0.30 || a.LT.Release != 0.20 {
		t.Errorf("direct profile defaults = %+v", a)
	}
}

func TestCompileRejectsEqualDeadzoneAndSaturation(t *testing.T) {
	p := &profile.Profile{
		AnalogResolved: true,
		Analog:         profile.Analog{LS: profile.Stick{Deadzone: 0.5, Saturation: 0.5}, RS: profile.DefaultAnalog().RS},
		Layers:         []*profile.Layer{{Name: "base"}},
	}
	if _, err := engine.Compile(p); err == nil {
		t.Fatal("Compile accepted equal deadzone and saturation")
	}
}
