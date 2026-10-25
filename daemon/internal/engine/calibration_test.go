package engine_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Tests of the stick calibration of a controller (IN-14): its deadzone and
// saturation replace the profile's for that controller.

func cal(ls, rs *profile.StickCalibration) *profile.Calibration {
	return &profile.Calibration{LS: ls, RS: rs}
}

func stick(deadzone, saturation float64) *profile.StickCalibration {
	s := &profile.StickCalibration{}
	if !math.IsNaN(deadzone) {
		s.Deadzone = &deadzone
	}
	if !math.IsNaN(saturation) {
		s.Saturation = &saturation
	}
	return s
}

// unset leaves a member of a StickCalibration out.
var unset = math.NaN()

// pushRight puts the left stick at magnitude mag (a fraction of travel) to
// the right at ms.
func pushRight(e *engine.Engine, r *recorder, mag float64, ms int) {
	axisStep(e, r, control.LeftY, 0, ms)
	axisStep(e, r, control.LeftX, int16(math.Round(mag*32767)), ms)
}

var calBind = holdKey("ls.right", "F1") + "," + holdKey("ls.active", "F2")

// TestCalibrationChangesRecognition: a magnitude inside the profile's
// deadzone but outside a smaller one of the device is a direction, and the
// other way round.
func TestCalibrationChangesRecognition(t *testing.T) {
	for _, tc := range []struct {
		name    string
		analog  string
		cal     *profile.Calibration
		mag     float64
		pressed bool
	}{
		// Profile deadzone 0.5: 0.45 is inside it. norm is (0.45-0.1)/0.85.
		{"smaller device deadzone", `{"ls":{"deadzone":0.5}}`, cal(stick(0.1, unset), nil), 0.45, true},
		{"no calibration", `{"ls":{"deadzone":0.5}}`, nil, 0.45, false},
		{"the other stick's calibration", `{"ls":{"deadzone":0.5}}`, cal(nil, stick(0.1, unset)), 0.45, false},
		{"empty calibration", `{"ls":{"deadzone":0.5}}`, cal(&profile.StickCalibration{}, nil), 0.45, false},
		// Profile deadzone 0.1: 0.45 is a direction; a larger device deadzone
		// takes it away.
		{"larger device deadzone", `{"ls":{"deadzone":0.1}}`, cal(stick(0.5, unset), nil), 0.45, false},
		{"profile alone", `{"ls":{"deadzone":0.1}}`, nil, 0.45, true},
		// The outer edge: a lower saturation reaches the direction threshold
		// (0.30 of the way from the deadzone to the edge) sooner. 0.3 is
		// 0.15/0.8 = 0.19 of the way with the profile's edge, 0.3125 with 0.63.
		{"lower outer edge", `{"ls":{"deadzone":0.15,"saturation":0.95}}`, cal(stick(unset, 0.63), nil), 0.3, true},
		{"profile's outer edge", `{"ls":{"deadzone":0.15,"saturation":0.95}}`, nil, 0.3, false},
		// Only the missing member comes from the profile.
		{"deadzone only keeps the profile's edge", `{"ls":{"deadzone":0.15,"saturation":0.95}}`, cal(stick(0.2, unset), nil), 0.3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compileAnalog(t, tc.analog, calBind), r, engine.Options{})
			e.SetCalibration(0, tc.cal)
			pushRight(e, r, tc.mag, 1)
			got := strings.Contains(r.String(), "down decl/2 F1")
			if got != tc.pressed {
				t.Fatalf("pressed %v, want %v: %s", got, tc.pressed, r)
			}
			pushRight(e, r, 0, 2)
			balanced(t, r)
		})
	}
}

// TestCalibrationEffectiveAnalog: Analog reports the settings the engine
// recognizes with, and Calibration with nothing restores the profile's.
func TestCalibrationEffectiveAnalog(t *testing.T) {
	prog := compileAnalog(t, `{"ls":{"deadzone":0.2,"saturation":0.9},"rs":{"deadzone":0.1,"saturation":0.8}}`, calBind)
	e := engine.New(prog, &recorder{}, engine.Options{})
	if got := e.Analog(); got != prog.Analog() {
		t.Fatalf("a fresh engine: %+v", got)
	}
	if ignored := e.SetCalibration(0, cal(stick(0.3, unset), stick(unset, 0.6))); ignored != [2]bool{} {
		t.Fatalf("ignored %v", ignored)
	}
	got := e.Analog()
	if got.LS.Deadzone != 0.3 || got.LS.Saturation != 0.9 || got.RS.Deadzone != 0.1 || got.RS.Saturation != 0.6 {
		t.Fatalf("ls %v/%v rs %v/%v", got.LS.Deadzone, got.LS.Saturation, got.RS.Deadzone, got.RS.Saturation)
	}
	if prog.Analog().LS.Deadzone != 0.2 {
		t.Fatal("the shared Program changed")
	}
	e.SetCalibration(0, nil)
	if got := e.Analog(); got != prog.Analog() {
		t.Fatalf("after clearing: %+v", got)
	}
}

// TestCalibrationIgnoredWhenThePairFails: a calibration whose pair, with the
// profile's other member, has saturation not above deadzone is ignored for
// that stick, and says so; the other stick's calibration still applies.
func TestCalibrationIgnoredWhenThePairFails(t *testing.T) {
	prog := compileAnalog(t, `{"ls":{"deadzone":0.2,"saturation":0.6},"rs":{"deadzone":0.1,"saturation":0.9}}`, calBind)
	e := engine.New(prog, &recorder{}, engine.Options{})
	ignored := e.SetCalibration(0, cal(stick(0.7, unset), stick(0.3, unset)))
	if ignored != [2]bool{true, false} {
		t.Fatalf("ignored %v", ignored)
	}
	if got := e.Analog(); got.LS.Deadzone != 0.2 || got.LS.Saturation != 0.6 || got.RS.Deadzone != 0.3 {
		t.Fatalf("analog %+v", got)
	}
	if e.CalibrationIgnored() != [2]bool{true, false} {
		t.Fatalf("CalibrationIgnored %v", e.CalibrationIgnored())
	}
	e.SetCalibration(0, cal(stick(0.3, unset), nil))
	if e.CalibrationIgnored() != [2]bool{} {
		t.Fatalf("CalibrationIgnored after a valid one: %v", e.CalibrationIgnored())
	}
}

// TestCalibrationChangeWhilePushed: a stick held across a change of
// calibration gives up the directions the new values no longer want, at
// once, and presses none the new values newly want (invariant 1).
func TestCalibrationChangeWhilePushed(t *testing.T) {
	t.Run("no longer applies", func(t *testing.T) {
		r := &recorder{}
		e := engine.New(compileAnalog(t, `{"ls":{"deadzone":0.1}}`, calBind), r, engine.Options{})
		pushRight(e, r, 0.45, 1)
		if got := r.String(); got != "@1 down decl/1 F2, @1 down decl/2 F1" && got != "@1 down decl/2 F1, @1 down decl/1 F2" {
			t.Fatalf("before: %s", got)
		}
		r.now = clock.Time(5 * time.Millisecond)
		e.SetCalibration(r.now, cal(stick(0.5, unset), nil))
		if got := r.String(); !strings.Contains(got, "@5 up decl/2 F1") || !strings.Contains(got, "@5 up decl/1 F2") {
			t.Fatalf("the directions and ls.active stay down: %s", got)
		}
		// Nothing is held now, and moving the stick on presses nothing again
		// until it has left and come back.
		n := len(r.out)
		pushRight(e, r, 0.47, 6)
		if len(r.out) != n {
			t.Fatalf("pressed again: %s", r)
		}
		pushRight(e, r, 0, 7)
		pushRight(e, r, 0.9, 8)
		if got := r.String(); !strings.Contains(got, "@8 down") {
			t.Fatalf("a push out of the new deadzone does not map: %s", r)
		}
		pushRight(e, r, 0, 9)
		balanced(t, r)
	})
	t.Run("newly applies", func(t *testing.T) {
		r := &recorder{}
		e := engine.New(compileAnalog(t, `{"ls":{"deadzone":0.5}}`, calBind), r, engine.Options{})
		pushRight(e, r, 0.45, 1)
		if len(r.out) != 0 {
			t.Fatalf("inside the deadzone: %s", r)
		}
		e.SetCalibration(clock.Time(5*time.Millisecond), cal(stick(0.1, unset), nil))
		if len(r.out) != 0 {
			t.Fatalf("a calibration change pressed: %s", r)
		}
		pushRight(e, r, 0.46, 6)
		if len(r.out) != 0 {
			t.Fatalf("the stick had not left: %s", r)
		}
		pushRight(e, r, 0, 7)
		pushRight(e, r, 0.45, 8)
		if got := r.String(); !strings.Contains(got, "@8 down decl") {
			t.Fatalf("after leaving, the new deadzone maps: %s", r)
		}
		pushRight(e, r, 0, 9)
		balanced(t, r)
	})
	t.Run("only a key held by a button stays", func(t *testing.T) {
		// A calibration change is not a CancelAll: a held button keeps its key.
		r := &recorder{}
		e := engine.New(compileAnalog(t, `{}`, calBind+","+holdKey("south", "F3")), r, engine.Options{})
		e.Step(engine.Input{T: 0, Kind: engine.InputButton, Control: control.South, Down: true})
		pushRight(e, r, 0.5, 1)
		e.SetCalibration(clock.Time(2*time.Millisecond), cal(stick(0.8, unset), nil))
		for _, s := range r.out {
			if strings.Contains(s, "up decl/1 F3") {
				t.Fatalf("the button's key was released: %s", r)
			}
		}
		e.Step(engine.Input{T: clock.Time(3 * time.Millisecond), Kind: engine.InputButton, Control: control.South})
		pushRight(e, r, 0, 4)
		balanced(t, r)
	})
}

// TestCalibrationSurvivesReloadAndIsChecked: a Reload keeps the calibration
// of the controller, and checks it against the new profile.
func TestCalibrationSurvivesReloadAndIsChecked(t *testing.T) {
	r := &recorder{}
	a := compileAnalog(t, `{"ls":{"deadzone":0.1,"saturation":0.9}}`, calBind)
	b := compileAnalog(t, `{"ls":{"deadzone":0.1,"saturation":0.4}}`, calBind)
	e := engine.New(a, r, engine.Options{})
	e.SetCalibration(0, cal(stick(0.3, unset), nil))
	e.Reload(clock.Time(time.Millisecond), a)
	if got := e.Analog(); got.LS.Deadzone != 0.3 || e.CalibrationIgnored() != [2]bool{} {
		t.Fatalf("after reloading the same: %+v %v", got, e.CalibrationIgnored())
	}
	// 0.3 is below b's saturation, 0.4: the pair holds.
	e.Reload(clock.Time(2*time.Millisecond), b)
	if got := e.Analog(); got.LS.Deadzone != 0.3 || got.LS.Saturation != 0.4 || e.CalibrationIgnored() != [2]bool{} {
		t.Fatalf("after reloading b: %+v %v", got, e.CalibrationIgnored())
	}
	e.SetCalibration(clock.Time(3*time.Millisecond), cal(stick(0.45, unset), nil))
	if e.CalibrationIgnored() != [2]bool{true, false} {
		t.Fatalf("0.45 is above b's saturation: %v", e.CalibrationIgnored())
	}
	e.Reload(clock.Time(4*time.Millisecond), a)
	if got := e.Analog(); got.LS.Deadzone != 0.45 || e.CalibrationIgnored() != [2]bool{} {
		t.Fatalf("a takes 0.45: %+v %v", got, e.CalibrationIgnored())
	}
	e.Reset(clock.Time(5 * time.Millisecond))
	if got := e.Analog(); got.LS.Deadzone != 0.45 {
		t.Fatalf("Reset forgot the calibration, which belongs to the controller: %+v", got)
	}
	balanced(t, r)
}

// TestCalibrationChangesTheRings: the rings read the calibrated depth too.
func TestCalibrationChangesTheRings(t *testing.T) {
	bind := holdKey("ls.inner", "F1") + "," + holdKey("ls.outer", "F2")
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"ls":{"deadzone":0.15,"saturation":0.95}}`, bind), r, engine.Options{})
	e.SetCalibration(0, cal(stick(unset, 0.5), nil))
	// 0.49 of travel is 0.4 of the way from 0.15 to 0.95 with the profile's
	// edge (inner), and 0.97 of the way to 0.5 (outer).
	pushRight(e, r, 0.49, 1)
	if got := r.String(); !strings.Contains(got, "down decl/1 F2") || strings.Contains(got, "F1") {
		t.Fatalf("the outer ring: %s", r)
	}
	pushRight(e, r, 0, 2)
	balanced(t, r)
}

// TestCalibrationPreviewSharesIt: the input preview of a cold script runtime
// recognizes with the calibration the engine has.
func TestCalibrationPreviewSharesIt(t *testing.T) {
	r := &recorder{}
	e := engine.New(compileAnalog(t, `{"ls":{"deadzone":0.5}}`, calBind), r, engine.Options{})
	e.SetCalibration(0, cal(stick(0.1, unset), nil))
	var seen []string
	preview := e.PreviewInput(func(ev engine.ButtonEvent) { seen = append(seen, ev.Control.String()) })
	preview.Step(engine.Input{T: 0, Kind: engine.InputAxis, Axis: control.LeftX, Value: 14746})
	if !slicesContain(seen, "ls.right") {
		t.Fatalf("the preview does not use the calibration: %v", seen)
	}
}

func slicesContain(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TestNoAllocsCalibrated: the calibration costs the hot path no allocation.
func TestNoAllocsCalibrated(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector may allocate; the other CI jobs run this")
	}
	prog := compileAnalog(t, `{}`, calBind+","+holdKey("rs.up", "F5"))
	out := &counter{}
	e := engine.New(prog, out, engine.Options{})
	e.SetCalibration(0, cal(stick(0.05, 0.7), stick(0.3, unset)))
	var now clock.Time
	step := func(a control.Axis, v int16) {
		now = now.Add(time.Millisecond)
		e.Step(engine.Input{T: now, Kind: engine.InputAxis, Axis: a, Value: v})
	}
	if n := testing.AllocsPerRun(100, func() {
		step(control.LeftX, 20000)
		step(control.RightY, -30000)
		step(control.LeftX, 0)
		step(control.RightY, 0)
	}); n != 0 {
		t.Errorf("Step with a calibration: %v allocations", n)
	}
}
