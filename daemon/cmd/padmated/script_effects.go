package main

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
)

// The rumble and LED calls of scripts (SCR-23, ADR-0066) reach a controller
// through the effect queue, as every other effect does, in the Script class of
// the Mixer: a script's buzz never cuts the pulses of a layer, and it ends
// with the runtime that asked for it.

// feedbackOf is the feedback settings of p, the defaults for a profile that
// has none.
func feedbackOf(p *profile.Profile) profile.Feedback {
	if p != nil && p.Feedback != nil {
		return *p.Feedback
	}
	return profile.DefaultFeedback()
}

// feedbackSettings returns the feedback settings of the profile that runs.
func (d *scriptDefinition) feedbackSettings() profile.Feedback {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.feedback
}

// scriptEffects is the script.Effects of a live padmated: the switches are the
// global ones of config.json (ADR-0067), and the requests go to the effect
// queue, which never blocks.
type scriptEffects struct{ run *liveRun }

var _ script.Effects = scriptEffects{}

// Switches reports the global rumble switch, which needs a strength above 0 in
// the profile that runs (0 is off for everything padmate makes by itself), and
// the global led switch. It reads both at each call, so host.has and the
// effect calls follow a change of config.json at once.
func (e scriptEffects) Switches(uint8) (rumble, led bool) {
	fb := e.run.scriptDef.feedbackSettings()
	sw := e.run.feedbackSwitches()
	return sw.Rumble && fb.Strength > 0, sw.LED
}

func (e scriptEffects) Rumble(_ uint8, dev input.DeviceID, steps []haptic.Step) {
	e.run.effects.postEffect(input.Effect{Device: dev, Class: haptic.Script, Steps: steps})
}

func (e scriptEffects) Haptic(_ uint8, dev input.DeviceID, preset haptic.Preset) {
	fb := e.run.scriptDef.feedbackSettings()
	e.run.effects.postEffect(input.Effect{
		Device: dev, Class: haptic.Script, Preset: preset,
		Strength: uint16(fb.Strength), ShortMs: fb.ShortMs, GapMs: fb.GapMs,
	})
}

func (e scriptEffects) StopRumble(_ uint8, dev input.DeviceID) {
	e.run.effects.postStop(input.Stop{Device: dev, Class: haptic.Script})
}

func (e scriptEffects) Light(_ uint8, dev input.DeviceID, color haptic.RGB) {
	e.run.effects.postLight(input.Light{Device: dev, Class: haptic.Script, Color: color})
}

func (e scriptEffects) ReleaseLight(_ uint8, dev input.DeviceID) {
	e.run.effects.postLight(input.Light{Device: dev, Class: haptic.Script, Release: true})
}

func (e scriptEffects) Cancel(_ uint8, dev input.DeviceID) {
	e.run.effects.postStop(input.Stop{Device: dev, Class: haptic.Script, Everything: true})
}

// scriptErrorCue is the error buzz of a script that starts failing
// (SCRIPTING 4.2): the "error" preset in the Alert class, which is a warning
// the daemon raises and so outranks the layer's pulses, and which a stop of the
// script's own class, when its runtime is released for the failure, does not
// end. The global rumble switch silences it, and so does a strength of 0 in
// the profile.
func (r *liveRun) scriptErrorCue(slot uint8, dev input.DeviceID) {
	fb := r.scriptDef.feedbackSettings()
	if dev == 0 || !r.feedbackSwitches().Rumble || fb.Strength <= 0 {
		return
	}
	r.log.Debug("script failed; error cue", "slot", slot, "id", dev)
	r.effects.postEffect(input.Effect{Device: dev, Class: haptic.Alert, Preset: haptic.Error, Strength: uint16(fb.Strength)})
}
