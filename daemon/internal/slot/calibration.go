package slot

import (
	"fmt"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// RefreshCalibration asks Config.Calibration again for the calibration of the
// slot's controller and applies it (IN-14): after a committed config.json
// changed, the supervisor runs it on every slot as a job. A slot without a
// controller, or without Config.Calibration, has nothing to calibrate. What
// the new values no longer recognize is released through the engine's normal
// path and nothing is pressed (engine.Engine.SetCalibration); a refresh that
// changes nothing does nothing.
func (s *Slot) RefreshCalibration() { s.applyCalibration(s.clk.Now()) }

// applyCalibration sets the engine's calibration to that of the controller the
// slot has, and shows it to the script runtime through the pad snapshot.
func (s *Slot) applyCalibration(now clock.Time) {
	if s.calibration == nil {
		return
	}
	var cal *profile.Calibration
	if s.deviceKey != "" {
		cal = s.calibration(s.deviceKey)
	}
	s.eng.SetCalibration(now, cal)
	s.syncCalibration(now)
}

// syncCalibration takes the analog settings the engine recognizes with
// (calibrated or not) for the pad snapshot, and reports a calibration that
// does not hold with the profile once per controller, profile and stick.
func (s *Slot) syncCalibration(now clock.Time) {
	for i, ignored := range s.eng.CalibrationIgnored() {
		stick := [2]string{"left", "right"}[i]
		key := "calibration " + s.deviceKey + " " + s.profileKey + " " + stick
		if !ignored || s.reported[key] {
			continue
		}
		s.reported[key] = true
		s.keep(fmt.Errorf("slot: the calibration of the %s stick of controller %s does not fit profile %q (saturation must be greater than deadzone): the profile's values are used", stick, s.deviceKey, s.profileKey))
	}
	if analog := s.eng.Analog(); analog != s.analog {
		s.analog = analog
		s.updatePad(now)
	}
}
