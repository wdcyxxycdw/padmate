package profile

import (
	"math"
	"sort"
)

// IsEmpty reports whether c sets no member of any stick: a nil Calibration
// and a Calibration of empty sticks both calibrate nothing (IN-14).
func (c *Calibration) IsEmpty() bool {
	if c == nil {
		return true
	}
	for _, s := range [2]*StickCalibration{c.LS, c.RS} {
		if s != nil && (s.Deadzone != nil || s.Saturation != nil) {
			return false
		}
	}
	return true
}

// Calibrated returns a with the stick calibration cal of a controller laid
// over its sticks: a deadzone or saturation cal sets replaces the profile's,
// one it leaves out keeps it (IN-14, ARCHITECTURE §7.5). Nothing else of a
// changes.
//
// A stick whose calibrated pair does not satisfy 0 <= deadzone < saturation
// <= 1 keeps the profile's pair whole, and ignored says so for it (index 0
// is the left stick, 1 the right one): a calibration is written for any
// profile, so it cannot be checked against the one that runs until it does,
// and mapping goes on with the profile's values rather than fail.
func (a Analog) Calibrated(cal *Calibration) (out Analog, ignored [2]bool) {
	out = a
	if cal == nil {
		return out, ignored
	}
	for i, c := range [2]*StickCalibration{cal.LS, cal.RS} {
		if c == nil || c.Deadzone == nil && c.Saturation == nil {
			continue
		}
		s := &out.LS
		if i == 1 {
			s = &out.RS
		}
		deadzone, saturation := s.Deadzone, s.Saturation
		if c.Deadzone != nil {
			deadzone = *c.Deadzone
		}
		if c.Saturation != nil {
			saturation = *c.Saturation
		}
		if !(deadzone >= 0 && saturation <= 1 && saturation > deadzone) || math.IsNaN(deadzone) || math.IsNaN(saturation) {
			ignored[i] = true
			continue
		}
		s.Deadzone, s.Saturation = deadzone, saturation
	}
	return out, ignored
}

// checkCalibration reports the calibrations of the devices of c whose sticks
// give both members and a saturation that is not greater than the deadzone,
// by the pointer of the stick (a left-out member is checked against the
// profile that runs, by Analog.Calibrated).
func checkCalibration(devices map[string]DeviceSettings, fail func(pointer, msg string)) {
	keys := make([]string, 0, len(devices))
	for key := range devices {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cal := devices[key].Calibration
		if cal == nil {
			continue
		}
		base := childPointer("/devices", key) + "/calibration"
		for _, s := range [2]struct {
			name string
			cal  *StickCalibration
		}{{"ls", cal.LS}, {"rs", cal.RS}} {
			if s.cal != nil && s.cal.Deadzone != nil && s.cal.Saturation != nil && *s.cal.Saturation <= *s.cal.Deadzone {
				fail(childPointer(base, s.name), "saturation must be greater than deadzone")
			}
		}
	}
}
