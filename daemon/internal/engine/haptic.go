package engine

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Haptic is a one-shot haptic the engine asks the controller for: the preset
// of a binding that has one (MAP-25), or the tick of a knob step (HUD-10). It
// carries what the profile's feedback says about the strength and the pulses,
// so whoever plays it needs nothing of the profile. It is passed by value and
// never allocates.
type Haptic struct {
	Preset haptic.Preset
	// Strength is feedback.strength, never 0 (a profile with strength 0 asks
	// for no haptics at all).
	Strength uint16
	// ShortMs and GapMs are feedback.shortMs and feedback.gapMs, the pulse
	// and the gap of the presets pulse1 to pulse4.
	ShortMs, GapMs int
}

// hapticSettings is the part of a profile's feedback that haptics follow,
// computed once per Program (Compile): on is a strength above 0 (the global
// rumble switch is asked at each haptic, Options.Switches). When it is off the
// engine does not call Options.Haptic at all.
type hapticSettings struct {
	on             bool
	strength       uint16
	shortMs, gapMs int
}

// hapticSettingsOf reads the haptic settings of p, which has the default
// feedback when it sets none (a model built by hand).
func hapticSettingsOf(p *profile.Profile) hapticSettings {
	fb := profile.DefaultFeedback()
	if p.Feedback != nil {
		fb = *p.Feedback
	}
	return hapticSettings{
		on:       fb.Strength > 0,
		strength: uint16(min(max(fb.Strength, 0), 0xFFFF)),
		shortMs:  fb.ShortMs,
		gapMs:    fb.GapMs,
	}
}

// knobTick reports whether a dispatch of control c plays the knob tick
// (HUD-10): c is a tick control (ls.cw, ls.ccw, rs.cw, rs.ccw) of a stick in
// mode "knob" with knobTick. Only emitKnob presses a tick control, so its
// dispatch is a knob step that a declarative binding ran.
func (e *Engine) knobTick(c control.Control) bool {
	var cfg *profile.Stick
	switch c {
	case control.LSCW, control.LSCCW:
		cfg = &e.prog.analog.LS
	case control.RSCW, control.RSCCW:
		cfg = &e.prog.analog.RS
	default:
		return false
	}
	return cfg.Mode == "knob" && cfg.KnobTick
}

// hapticPlayedAt reports whether st's control already played its binding's
// haptic in this cycle at now: a second gesture dispatched at that instant
// is the same moment.
func (st *ctlState) hapticPlayedAt(now clock.Time) bool {
	return st.hapticSet && st.hapticCycle == st.cycle && st.hapticAt == now
}

// playHaptic asks for the preset p through Options.Haptic. It does nothing
// without a callback or a preset, for a preview that only recognizes input
// (the engine of a cold script build owns no output of any kind), while the
// engine is paused (a paused mapping does nothing, and its press cycles are
// canceled), when the global rumble switch is off, and when the profile's
// feedback has a strength of 0.
//
// It is the only place the engine plays a haptic; the callers are runAction
// for a binding and emitKnob for a knob tick, so the typematic repeat of a
// held key, which does not dispatch, never plays one.
func (e *Engine) playHaptic(p haptic.Preset) {
	if p == 0 || e.haptic == nil || e.inputOnly || e.paused || !e.prog.haptics.on || !e.feedbackSwitches().Rumble {
		return
	}
	h := &e.prog.haptics
	e.haptic(Haptic{Preset: p, Strength: h.strength, ShortMs: h.shortMs, GapMs: h.gapMs})
}

// feedbackSwitches returns the global feedback switches (Options.Switches),
// all on without a source. It is a small value and never allocates.
func (e *Engine) feedbackSwitches() profile.FeedbackSwitches {
	if e.switches == nil {
		return profile.DefaultFeedbackSwitches()
	}
	return e.switches()
}
