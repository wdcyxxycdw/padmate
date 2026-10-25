package supervisor_test

import (
	"context"
	"math"
	"sync/atomic"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Tests of the stick calibration of a controller (IN-14) across slots.

// calProgram has a left stick deadzone of 0.4 and binds ls.right to a held
// Space.
func calProgram(t testing.TB) *engine.Program {
	t.Helper()
	p, ds := profile.Parse([]byte(`{"schemaVersion":1,"name":"cal","analog":{"ls":{"deadzone":0.4,"saturation":0.95}},"layers":{"base":{"ls.right":{"press":{"type":"keys","keys":"Space","mode":"hold"}}}}}`))
	if p == nil {
		t.Fatal(ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	return prog
}

// calConfig is a config.json of the calibrations of two controllers, replaced
// as a committed config is.
type calConfig struct {
	cfg atomic.Pointer[map[string]*profile.Calibration]
}

func (c *calConfig) set(m map[string]*profile.Calibration) { c.cfg.Store(&m) }
func (c *calConfig) get(key string) *profile.Calibration {
	if m := c.cfg.Load(); m != nil {
		return (*m)[key]
	}
	return nil
}

func deadzone(v float64) *profile.Calibration {
	return &profile.Calibration{LS: &profile.StickCalibration{Deadzone: &v}}
}

func (h *harness) refreshCalibration() {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	if err := h.sup.RefreshCalibration(ctx); err != nil {
		h.t.Fatalf("RefreshCalibration: %v", err)
	}
}

// push puts the left stick of gamepad id to the right at magnitude mag.
func push(h *harness, id input.DeviceID, mag float64) {
	h.t.Helper()
	h.send(axis(id, "lefty", 0))
	h.send(axis(id, "leftx", int16(math.Round(mag*32767))))
}

// TestCalibrationPerDevice: two controllers share one profile; the one whose
// key has a calibration recognizes with it, the other with the profile's.
func TestCalibrationPerDevice(t *testing.T) {
	cfg := &calConfig{}
	cfg.set(map[string]*profile.Calibration{"03ab": deadzone(0.1)})
	h := start(t, opts{slots: 2, program: calProgram(t), calibration: cfg.get})
	h.send(added(pad(1, "Pad A", "03ab")))
	h.send(added(pad(2, "Pad B", "03cd")))
	// With the profile's deadzone, 0.4, (0.45-0.4)/0.55 = 0.09 is below the
	// direction threshold; with the calibrated 0.1 it is 0.41.
	push(h, 2, 0.45)
	h.sync()
	h.expect("")
	push(h, 1, 0.45)
	h.sync()
	if got := h.rec.Trace(); got != "+Space@0" {
		t.Fatalf("the calibrated controller: %q", got)
	}
	push(h, 1, 0)
	h.sync()
	h.expect("+Space@0 -Space@0")
}

// TestCalibrationAppliesWithoutRestart: a committed change reaches the slots
// that already have their controllers, with nothing held across it.
func TestCalibrationAppliesWithoutRestart(t *testing.T) {
	cfg := &calConfig{}
	h := start(t, opts{slots: 2, program: calProgram(t), calibration: cfg.get})
	h.send(added(pad(1, "Pad A", "03ab")))
	h.send(added(pad(2, "Pad B", "03cd")))
	push(h, 1, 0.45)
	h.sync()
	h.expect("")
	cfg.set(map[string]*profile.Calibration{"03ab": deadzone(0.1)})
	h.refreshCalibration()
	h.expect("") // a change presses nothing
	push(h, 1, 0)
	push(h, 1, 0.45)
	h.sync()
	h.expect("+Space@0")
	// A larger deadzone takes the direction away at once.
	cfg.set(map[string]*profile.Calibration{"03ab": deadzone(0.45)})
	h.refreshCalibration()
	h.expect("+Space@0 -Space@0")
	// The other controller never had it.
	push(h, 2, 0.45)
	h.sync()
	h.expect("+Space@0 -Space@0")
	cfg.set(nil)
	h.refreshCalibration()
	push(h, 1, 0)
}

// TestCalibrationRefreshAfterStop: a refresh after Run has ended fails like
// the other requests.
func TestCalibrationRefreshAfterStop(t *testing.T) {
	h := start(t, opts{program: calProgram(t), calibration: (&calConfig{}).get})
	h.cancel()
	if err := h.wait(); err != nil {
		t.Fatal(err)
	}
	if err := h.sup.RefreshCalibration(context.Background()); err == nil {
		t.Fatal("RefreshCalibration after Run succeeded")
	}
}
