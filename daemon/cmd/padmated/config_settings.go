package main

import (
	"slices"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

func (o options) librarySelection() bool {
	return o.library != nil && (o.profileID != "" || o.autoProfile)
}

// replay reports whether o replays a trace file (--fake-input TRACE), which
// only reads the library.
func (o options) replay() bool {
	return o.fakeInput != "" && o.fakeInput != "ipc"
}

func (o options) profileKey() string {
	if o.librarySelection() {
		return o.profileID
	}
	return canonicalProfileKey(o.profile)
}

// selectionConfig returns the effective config: the fields of the last
// committed config.json that apply at once (liveSettings) over the
// startup-captured rest. Automatic selection, the global timing that
// profiles are compiled with, and the emergency combos read it. The result
// is immutable. Small test harnesses that do not use startLive fall back
// to the startup settings.
func (r *liveRun) selectionConfig() *profile.Config {
	if c := r.selection.Load(); c != nil {
		return c
	}
	if r.cfg.opts.settings != nil {
		return r.cfg.opts.settings
	}
	return profile.DefaultConfig()
}

// feedbackSwitches returns the global feedback switches (UI-27, ADR-0067) of
// the effective config, which the slots' engines, the effect queue's callers,
// the scripts' effects and the low-battery alert read at each use. The result
// is an immutable copy.
func (r *liveRun) feedbackSwitches() profile.FeedbackSwitches {
	return r.selectionConfig().Feedback
}

// liveSettings is the effective config after committing next: the fields
// applied at once (automatic selection, output.sendToOwnWindow,
// output.eventGapMs, timing, safety, the feedback switches and the stick
// calibration of each device, IN-14) come from next, everything captured at startup
// (restoreLayerState, which devices are ignored and the rest of output)
// stays as startup has it. Both inputs are immutable and may share slices and
// maps with the result.
func liveSettings(startup, next *profile.Config) *profile.Config {
	if startup == nil {
		startup = profile.DefaultConfig()
	}
	c := *startup
	c.Timing, c.Safety = next.Timing, next.Safety
	c.Enabled, c.ReleaseOnSwitch, c.Rules = next.Enabled, next.ReleaseOnSwitch, next.Rules
	c.Output, c.Feedback = next.Output, next.Feedback
	c.Devices = liveDevices(startup.Devices, next.Devices)
	return &c
}

// liveDevices is the devices of startup with the calibration and the mapping
// of next: a device's ignored flag is startup's, its calibration and its
// mapping next's (both apply at once).
func liveDevices(startup, next map[string]profile.DeviceSettings) map[string]profile.DeviceSettings {
	var out map[string]profile.DeviceSettings
	set := func(key string, d profile.DeviceSettings) {
		if out == nil {
			out = make(map[string]profile.DeviceSettings, len(startup)+len(next))
		}
		out[key] = d
	}
	for key, d := range startup {
		d.Calibration, d.Mapping = nil, next[key].Mapping
		if cal := next[key].Calibration; !cal.IsEmpty() {
			d.Calibration = cal
		}
		set(key, d)
	}
	for key, d := range next {
		if _, ok := startup[key]; !ok && (!d.Calibration.IsEmpty() || d.Mapping != "") {
			set(key, profile.DeviceSettings{Calibration: d.Calibration, Mapping: d.Mapping})
		}
	}
	return out
}

// startupFields clears the fields liveSettings applies at once, leaving
// those only a restart applies. Of the devices that is which ones are
// ignored: an entry that does not ignore its device, such as one that only
// calibrates it, is the same as none.
func startupFields(c *profile.Config) profile.Config {
	s := *c
	s.Timing, s.Safety = profile.Timing{}, profile.SafetySettings{}
	s.Enabled, s.ReleaseOnSwitch, s.Rules = false, "", nil
	s.Output = profile.OutputSettings{}
	s.Feedback = profile.FeedbackSwitches{}
	s.Devices = nil
	for key, d := range c.Devices {
		if d.Ignored {
			if s.Devices == nil {
				s.Devices = map[string]profile.DeviceSettings{}
			}
			s.Devices[key] = profile.DeviceSettings{Ignored: true}
		}
	}
	return s
}

// calibrations returns the stick calibration of each device of c that has
// one (IN-14): what a change of config.json has to tell the slots about.
func calibrations(c *profile.Config) map[string]*profile.Calibration {
	var out map[string]*profile.Calibration
	for key, d := range c.Devices {
		if d.Calibration.IsEmpty() {
			continue
		}
		if out == nil {
			out = map[string]*profile.Calibration{}
		}
		out[key] = d.Calibration
	}
	return out
}

// deviceCalibration is the Calibration of the Supervisor's Config: the
// calibration the effective config gives the gamepad with DeviceKey key.
func (r *liveRun) deviceCalibration(key string) *profile.Calibration {
	return r.selectionConfig().Devices[key].Calibration
}

// configuredCombo returns the ComboFor of settings, nil without settings.
func configuredCombo(settings *profile.Config) func(input.DeviceInfo, bool) input.Combo {
	if settings == nil {
		return nil
	}
	return func(info input.DeviceInfo, vertical bool) input.Combo {
		_, c := effectiveCombo(settings, info, vertical)
		return c
	}
}

// comboFamily names the config.json safety.panic family of a gamepad
// (ARCHITECTURE §10.4): a single Joy-Con by its side and grip, every other
// gamepad, a pair of Joy-Cons too, standard.
func comboFamily(info input.DeviceInfo, vertical bool) string {
	family := "standard"
	switch info.Type {
	case "joyconleft":
		family = "joyconLeft"
	case "joyconright":
		family = "joyconRight"
	}
	if family != "standard" && vertical {
		family += "Vertical"
	}
	return family
}

// effectiveCombo returns the family of a gamepad and the emergency combo
// settings give it: its family's entry, or input.DefaultCombo. A gamepad
// known to lack a button of its family's entry, such as a standard pad
// without the paddles another pad recorded, keeps input.DefaultCombo, so
// it still has an emergency pause. T-input (through ComboFor) and
// status.get both use it, so the UI shows the combo that fires. settings
// may be nil.
func effectiveCombo(settings *profile.Config, info input.DeviceInfo, vertical bool) (string, input.Combo) {
	family := comboFamily(info, vertical)
	if settings != nil {
		combo, ok := settings.Safety.Panic[family]
		if ok && !slices.ContainsFunc(combo.Buttons, func(b string) bool { return !info.MayHaveButton(b) }) {
			return family, input.Combo{Buttons: slices.Clone(combo.Buttons), Hold: time.Duration(combo.HoldMs) * time.Millisecond}
		}
	}
	return family, input.DefaultCombo(info, vertical)
}
