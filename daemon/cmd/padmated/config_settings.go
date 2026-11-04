package main

import (
	"slices"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
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

// feedbackSwitchesFor returns the feedback switches (ADR-0015, ADR-0017) of
// the controller with DeviceKey string key in the effective config: its own
// settings, then the retired top-level feedback, then all on. The slots'
// engines, the effect queue's callers, the scripts' effects and the
// low-battery alert read it at each use for their own controller, so a switch of
// one controller never reaches another. The result is a copy. A key of ""
// (a slot without a controller) has the top-level switches.
func (r *liveRun) feedbackSwitchesFor(key string) profile.FeedbackSwitches {
	sw, _ := r.selectionConfig().PadFeedback(key)
	return sw
}

// liveSettings is the effective config after committing next: the fields
// applied at once (automatic selection, output.sendToOwnWindow,
// output.eventGapMs, timing, the function key, safety, the feedback switches,
// joycon2.autoConnect and the stick calibration of each device) come from next, everything captured at startup
// (restoreLayerState, which devices are ignored and the rest of output)
// stays as startup has it. Both inputs are immutable and may share slices and
// maps with the result.
func liveSettings(startup, next *profile.Config) *profile.Config {
	if startup == nil {
		startup = profile.DefaultConfig()
	}
	c := *startup
	c.Timing, c.Safety = next.Timing, next.Safety
	c.FunctionKey = next.FunctionKeyOrDefault()
	c.Enabled, c.ReleaseOnSwitch, c.Rules = next.Enabled, next.ReleaseOnSwitch, next.Rules
	c.Output, c.Feedback, c.JoyCon2 = next.Output, next.Feedback, next.JoyCon2
	c.Legacy = next.Legacy
	c.Integrations = next.Integrations
	c.Devices = liveDevices(startup.Devices, next.Devices)
	return &c
}

// liveDevices is the devices of startup with the calibration, the mapping and
// the settings of the controller of next (function key, feedback switches,
// emergency combo: ADR-0017): a device's ignored flag is startup's, the rest
// next's (all apply at once).
func liveDevices(startup, next map[string]profile.DeviceSettings) map[string]profile.DeviceSettings {
	var out map[string]profile.DeviceSettings
	set := func(key string, d profile.DeviceSettings) {
		if out == nil {
			out = make(map[string]profile.DeviceSettings, len(startup)+len(next))
		}
		out[key] = d
	}
	for key, d := range startup {
		n := next[key]
		d.Calibration, d.Mapping = nil, n.Mapping
		d.FunctionKey, d.Feedback, d.Panic = n.FunctionKey, n.Feedback, n.Panic
		if cal := next[key].Calibration; !cal.IsEmpty() {
			d.Calibration = cal
		}
		set(key, d)
	}
	for key, d := range next {
		if _, ok := startup[key]; !ok && (!d.Calibration.IsEmpty() || d.Mapping != "" || d.FunctionKey != "" || d.Feedback != nil || d.Panic != nil) {
			set(key, profile.DeviceSettings{Calibration: d.Calibration, Mapping: d.Mapping, FunctionKey: d.FunctionKey, Feedback: d.Feedback, Panic: d.Panic})
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
	s.FunctionKey = control.Invalid
	s.Legacy = profile.LegacyPad{}
	s.Enabled, s.ReleaseOnSwitch, s.Rules = false, "", nil
	s.Output = profile.OutputSettings{}
	s.Feedback = profile.FeedbackSwitches{}
	s.JoyCon2 = profile.JoyCon2Settings{}
	s.Integrations = nil
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
// one: what a change of config.json has to tell the slots about.
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
func configuredCombo(settings *profile.Config) func(input.DeviceInfo) input.Combo {
	if settings == nil {
		return nil
	}
	return func(info input.DeviceInfo) input.Combo {
		_, c := effectiveCombo(settings, info)
		return c
	}
}

// effectiveCombo returns the family of a gamepad (the name of the config.json
// safety.panic family that covers it) and the emergency combo settings give
// it (ADR-0017): that of the controller's own entry in devices, else the
// retired safety.panic entry of its family, else input.DefaultCombo. A
// gamepad known to lack a button of that combo, such as a standard pad
// without the paddles another pad recorded, keeps input.DefaultCombo, so it
// still has an emergency pause; so does an entry with guide, which padmate
// never uses (profile.PanicCombo.Usable, which Config.PadSettings applies).
// T-input (through ComboFor) and status.get both use it, so the UI shows the
// combo that fires. settings may be nil. The controller is told by the
// DeviceKey its info makes (input.KeyOf), whose number N for identical pads is
// 0: ComboFor is not told which of them it is.
func effectiveCombo(settings *profile.Config, info input.DeviceInfo) (string, input.Combo) {
	family := input.FamilyOf(info)
	if settings != nil {
		s := settings.PadSettings(input.KeyOf(&info).String(), family)
		if s.PanicSource != profile.SourceDefault && !slices.ContainsFunc(s.Panic.Buttons, func(b string) bool { return !info.MayHaveButton(b) }) {
			return family.PanicFamily(), input.Combo{Buttons: slices.Clone(s.Panic.Buttons), Hold: time.Duration(s.Panic.HoldMs) * time.Millisecond}
		}
	}
	return family.PanicFamily(), input.DefaultCombo(info)
}

// padFunctionKey is the FunctionKey of the Supervisor's Config: the function
// key the effective config gives the controller with DeviceKey key and
// DeviceInfo info (ADR-0017).
func (r *liveRun) padFunctionKey(key string, info input.DeviceInfo) control.Control {
	return padFunctionKey(r.selectionConfig(), key, info)
}

// padFunctionKey is the function key settings give the controller key.
func padFunctionKey(settings *profile.Config, key string, info input.DeviceInfo) control.Control {
	return settings.PadSettings(key, input.FamilyOf(info)).FunctionKey
}
