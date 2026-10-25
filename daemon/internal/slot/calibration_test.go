package slot_test

import (
	"errors"
	"math"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// Tests of the stick calibration of a controller (IN-14) in a slot: the slot
// asks for the calibration of the controller it has, by its DeviceKey.

var unset = math.NaN()

func calStick(deadzone, saturation float64) *profile.StickCalibration {
	s := &profile.StickCalibration{}
	if !math.IsNaN(deadzone) {
		s.Deadzone = &deadzone
	}
	if !math.IsNaN(saturation) {
		s.Saturation = &saturation
	}
	return s
}

// calProfile has a deadzone of 0.4 on the left stick and binds ls.right.
const calProfile = `{"schemaVersion":1,"name":"cal","analog":{"ls":{"deadzone":0.4,"saturation":0.95}},"layers":{"base":{"ls.right":{"press":{"type":"keys","keys":"F1","mode":"hold"}}}}}`

// calSource is the calibration source of a slot: a DeviceKey to its
// calibration, replaced as a committed config.json is.
type calSource struct {
	devices atomic.Pointer[map[string]*profile.Calibration]
}

func (c *calSource) set(m map[string]*profile.Calibration) { c.devices.Store(&m) }
func (c *calSource) get(key string) *profile.Calibration {
	if m := c.devices.Load(); m != nil {
		return (*m)[key]
	}
	return nil
}

func newCalSlot(t *testing.T, sim *simtest.Sim, index uint8, src *calSource, prog *engine.Program) *slot.Slot {
	t.Helper()
	s, err := slot.New(slot.Config{Index: index, Gen: 1, Program: prog, Arbiter: sim.Arb, Clock: sim.Clock, ProfileKey: "cal", Calibration: src.get})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.ReleaseAll(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func addDevice(s *slot.Slot, id input.DeviceID, key string) {
	s.Handle(input.Event{Kind: input.DeviceAdded, Device: id, AssignedKey: key, Info: &input.DeviceInfo{ID: id, Name: "pad " + key, IsGamepad: true, ButtonMask: ^uint32(0), AxisMask: 63}})
}

// pushLeft puts the left stick to the right at magnitude mag.
func pushLeft(s *slot.Slot, mag float64) {
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "lefty", Value: 0})
	s.Handle(input.Event{Kind: input.AxisMotion, Control: "leftx", Value: int16(math.Round(mag * 32767))})
}

// f1Down reports whether a key is down: F1, the only one the profile binds.
func f1Down(sim *simtest.Sim) bool { return len(sim.Out.Down()) > 0 }

// TestCalibrationPerDevice: two controllers on one profile: the one with a
// calibration recognizes with it, the other with the profile's values.
func TestCalibrationPerDevice(t *testing.T) {
	sim := simtest.New(t)
	prog := scriptProgram(t, calProfile)
	src := &calSource{}
	src.set(map[string]*profile.Calibration{"pad-a": {LS: calStick(0.1, unset)}})
	a, b := newCalSlot(t, sim, 0, src, prog), newCalSlot(t, sim, 1, src, prog)
	addDevice(a, 1, "pad-a")
	addDevice(b, 2, "pad-b")
	// The profile's deadzone is 0.4: (0.45-0.4)/0.55 = 0.09 is not a
	// direction, the calibrated 0.1 makes it (0.45-0.1)/0.85 = 0.41.
	pushLeft(b, 0.45)
	if f1Down(sim) {
		t.Fatalf("the controller without a calibration maps at 0.45: %s", sim.Output())
	}
	pushLeft(a, 0.45)
	if !f1Down(sim) {
		t.Fatalf("the calibrated controller does not map at 0.45: %s", sim.Output())
	}
	pushLeft(a, 0)
	pushLeft(b, 0)
	if f1Down(sim) {
		t.Fatalf("held: %s", sim.Output())
	}
}

// TestCalibrationChangeWithoutRestart: RefreshCalibration (the slot's side of
// a committed config.json) applies the new values to the pad the slot has,
// releases what no longer applies and presses nothing.
func TestCalibrationChangeWithoutRestart(t *testing.T) {
	sim := simtest.New(t)
	prog := scriptProgram(t, calProfile)
	src := &calSource{}
	s := newCalSlot(t, sim, 0, src, prog)
	addDevice(s, 1, "pad-a")
	pushLeft(s, 0.45)
	if f1Down(sim) {
		t.Fatalf("mapped before the calibration: %s", sim.Output())
	}
	src.set(map[string]*profile.Calibration{"pad-a": {LS: calStick(0.1, unset)}})
	s.RefreshCalibration()
	if f1Down(sim) {
		t.Fatalf("a calibration change pressed: %s", sim.Output())
	}
	pushLeft(s, 0)
	pushLeft(s, 0.45)
	if !f1Down(sim) {
		t.Fatalf("the new calibration does not map after the stick came back: %s", sim.Output())
	}
	// A deadzone above the stick: what was pressed is released at once.
	src.set(map[string]*profile.Calibration{"pad-a": {LS: calStick(0.45, unset)}})
	s.RefreshCalibration()
	if f1Down(sim) {
		t.Fatalf("a key stays held after a calibration that no longer maps: %s", sim.Output())
	}
	// Taken away: back to the profile's values.
	src.set(nil)
	s.RefreshCalibration()
	pushLeft(s, 0)
	pushLeft(s, 0.45)
	if f1Down(sim) {
		t.Fatalf("the profile's deadzone applies again: %s", sim.Output())
	}
	pushLeft(s, 0)
}

// TestCalibrationRefreshWithoutChange: asking again for the same values does
// nothing the stick could notice.
func TestCalibrationRefreshWithoutChange(t *testing.T) {
	sim := simtest.New(t)
	src := &calSource{}
	src.set(map[string]*profile.Calibration{"pad-a": {LS: calStick(0.1, unset)}})
	s := newCalSlot(t, sim, 0, src, scriptProgram(t, calProfile))
	addDevice(s, 1, "pad-a")
	pushLeft(s, 0.45)
	before := sim.Output()
	for range 3 {
		s.RefreshCalibration()
	}
	if after := sim.Output(); after != before || !f1Down(sim) {
		t.Fatalf("a refresh changed the output:\n%s\n%s", before, after)
	}
	pushLeft(s, 0)
}

// TestCalibrationFollowsTheDevice: a slot's controller is whichever it has:
// removed, the calibration goes; another one takes its own.
func TestCalibrationFollowsTheDevice(t *testing.T) {
	sim := simtest.New(t)
	src := &calSource{}
	src.set(map[string]*profile.Calibration{"pad-a": {LS: calStick(0.1, unset)}})
	s := newCalSlot(t, sim, 0, src, scriptProgram(t, calProfile))
	addDevice(s, 1, "pad-a")
	pushLeft(s, 0.45)
	if !f1Down(sim) {
		t.Fatalf("pad-a: %s", sim.Output())
	}
	s.Handle(input.Event{Kind: input.DeviceRemoved, Device: 1})
	if f1Down(sim) {
		t.Fatalf("held after the device went: %s", sim.Output())
	}
	addDevice(s, 2, "pad-b")
	pushLeft(s, 0.45)
	if f1Down(sim) {
		t.Fatalf("pad-b maps with pad-a's calibration: %s", sim.Output())
	}
	pushLeft(s, 0)
	// A slot without a device has no calibration to refresh.
	s.Handle(input.Event{Kind: input.DeviceRemoved, Device: 2})
	s.RefreshCalibration()
	if got := s.View().Key; got != "" {
		t.Fatalf("key %q", got)
	}
}

// TestCalibrationSurvivesReload: a profile reload keeps the calibration of
// the controller and uses it with the new profile.
func TestCalibrationSurvivesReload(t *testing.T) {
	sim := simtest.New(t)
	src := &calSource{}
	src.set(map[string]*profile.Calibration{"pad-a": {LS: calStick(0.1, unset)}})
	s := newCalSlot(t, sim, 0, src, scriptProgram(t, calProfile))
	addDevice(s, 1, "pad-a")
	if err := s.Reload(scriptProgram(t, strings.Replace(calProfile, `"deadzone":0.4`, `"deadzone":0.6`, 1)), 2); err != nil {
		t.Fatal(err)
	}
	pushLeft(s, 0.45) // inside 0.6, outside the device's 0.1
	if !f1Down(sim) {
		t.Fatalf("the calibration was lost at the reload: %s", sim.Output())
	}
	pushLeft(s, 0)
}

// TestCalibrationIgnoredIsReportedOnce: a calibration that does not hold with
// the profile in use is ignored for that stick, with one warning per
// controller and profile.
func TestCalibrationIgnoredIsReportedOnce(t *testing.T) {
	sim := simtest.New(t)
	src := &calSource{}
	// The deadzone 0.97 is above the profile's saturation, 0.95.
	src.set(map[string]*profile.Calibration{"pad-a": {LS: calStick(0.97, unset)}})
	s := newCalSlot(t, sim, 0, src, scriptProgram(t, calProfile))
	addDevice(s, 1, "pad-a")
	s.RefreshCalibration()
	s.RefreshCalibration()
	errs := s.Errors()
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "pad-a") || !strings.Contains(errs[0].Error(), "left stick") {
		t.Fatalf("errors: %v", errors.Join(errs...))
	}
	// The same profile again: still once.
	if err := s.Reload(scriptProgram(t, calProfile), 2); err != nil {
		t.Fatal(err)
	}
	if errs := s.Errors(); len(errs) != 0 {
		t.Fatalf("reported again: %v", errors.Join(errs...))
	}
	// The profile's values are in force.
	pushLeft(s, 0.45)
	if f1Down(sim) {
		t.Fatalf("the ignored calibration maps: %s", sim.Output())
	}
	// Another profile is another report.
	s.SetProfileKey("other")
	if err := s.Reload(scriptProgram(t, calProfile), 3); err != nil {
		t.Fatal(err)
	}
	if errs := s.Errors(); len(errs) != 1 {
		t.Fatalf("errors for another profile: %v", errors.Join(errs...))
	}
	pushLeft(s, 0)
}

// calBridge is a script runtime that hands the slot's analog settings to the
// host as padmated's does.
type calBridge struct {
	*scriptSlotBridge
}

func (b *calBridge) UpdatePad(p slot.PadSnapshot) {
	b.host.UpdatePad(script.PadState{Connected: p.Connected, Info: p.Info, Key: p.Key, Down: p.Down, Since: p.Since, Axes: p.Axes, HasControl: p.HasControl, HasAxis: p.HasAxis, Analog: p.Analog, AnalogResolved: true})
}

// TestCalibrationReachesTheScript: pad.stick() of a script defaults to the
// calibrated deadzone and outer edge.
func TestCalibrationReachesTheScript(t *testing.T) {
	sim := simtest.New(t)
	host := script.NewHostWithOptions(script.HostOptions{Clock: sim.Clock, Output: hostSlotOutput{sim.Arb}})
	prog, err := goja.Compile("cal.js", `globalThis.read = () => globalThis.__padmate.pad.stick("ls").magnitude;`, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Activate(prog); err != nil {
		t.Fatal(err)
	}
	src := &calSource{}
	s, err := slot.New(slot.Config{Index: 0, Gen: 1, Clock: sim.Clock, Arbiter: sim.Arb, Program: scriptProgram(t, calProfile), ProfileKey: "cal", Calibration: src.get, Script: &calBridge{&scriptSlotBridge{host}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = s.ReleaseAll()
		_ = s.Dispose()
	})
	addDevice(s, 1, "pad-a")
	pushLeft(s, 0.5)
	magnitude := func() float64 {
		v, err := host.VM.RunString(`read()`)
		if err != nil {
			t.Fatal(err)
		}
		return v.ToFloat()
	}
	// The profile's 0.4 to 0.95: (0.5-0.4)/0.55.
	if got := magnitude(); math.Abs(got-0.1/0.55) > 0.001 {
		t.Fatalf("without a calibration: %v", got)
	}
	src.set(map[string]*profile.Calibration{"pad-a": {LS: calStick(0.2, 0.7)}})
	s.RefreshCalibration()
	if got := magnitude(); math.Abs(got-0.3/0.5) > 0.001 {
		t.Fatalf("with a calibration: %v, want %v", got, 0.3/0.5)
	}
	src.set(nil)
	s.RefreshCalibration()
	if got := magnitude(); math.Abs(got-0.1/0.55) > 0.001 {
		t.Fatalf("calibration taken away: %v", got)
	}
}
