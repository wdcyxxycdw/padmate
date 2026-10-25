package main

import (
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// settingsOff is the default config.json with the named global feedback
// switches (UI-27, ADR-0067) off: "rumble", "led", "pause" and "lowBattery".
func settingsOff(off ...string) *profile.Config {
	c := profile.DefaultConfig()
	for _, name := range off {
		switch name {
		case "rumble":
			c.Feedback.Rumble = false
		case "led":
			c.Feedback.LED = false
		case "pause":
			c.Feedback.Pause = false
		case "lowBattery":
			c.Feedback.LowBattery = false
		default:
			panic("unknown switch " + name)
		}
	}
	return c
}

// applySwitches commits a config.json whose global feedback switches named in
// off are off, the way config.put applies one (commitConfigLocked without the
// file): the effective config is stored and applyLiveSettings makes the running
// controllers follow.
func (tl *testLive) applySwitches(t *testing.T, off ...string) {
	t.Helper()
	tl.configApplyMu.Lock()
	defer tl.configApplyMu.Unlock()
	prev := tl.selectionConfig()
	tl.selection.Store(liveSettings(tl.cfg.opts.settings, settingsOff(off...)))
	if err := tl.applyLiveSettings(prev, tl.selectionConfig()); err != nil {
		t.Fatalf("applyLiveSettings: %v", err)
	}
}

// A liveRun reads the switches of its effective config at each use: a run that
// has none yet has every switch on, and a stored config replaces them.
func TestFeedbackSwitchesOfTheEffectiveConfig(t *testing.T) {
	r := &liveRun{}
	if got := r.feedbackSwitches(); got != profile.DefaultFeedbackSwitches() {
		t.Fatalf("without a config: %+v, want every switch on", got)
	}
	r.selection.Store(settingsOff("rumble", "pause"))
	if got := r.feedbackSwitches(); got.Rumble || !got.LED || got.Pause || !got.LowBattery {
		t.Errorf("rumble and pause off: %+v", got)
	}
	r.selection.Store(settingsOff())
	if got := r.feedbackSwitches(); got != profile.DefaultFeedbackSwitches() {
		t.Errorf("back on: %+v", got)
	}
}

// The feedback switches apply at once and never ask for a restart (ADR-0067):
// liveSettings takes them from the committed config, and startupFields leaves
// them out of what a restart applies.
func TestFeedbackSwitchesApplyAtOnce(t *testing.T) {
	startup := profile.DefaultConfig()
	next := settingsOff("rumble", "lowBattery")
	live := liveSettings(startup, next)
	if live.Feedback != next.Feedback {
		t.Errorf("liveSettings feedback = %+v, want the committed %+v", live.Feedback, next.Feedback)
	}
	if startup.Feedback != profile.DefaultFeedbackSwitches() {
		t.Errorf("liveSettings changed the startup config: %+v", startup.Feedback)
	}
	a, b := startupFields(startup), startupFields(next)
	if a.Feedback != b.Feedback {
		t.Errorf("startupFields keeps the switches: %+v and %+v differ, so config.get would ask for a restart", a.Feedback, b.Feedback)
	}
	r := &liveRun{cfg: liveConfig{opts: options{settings: startup}}}
	if r.configNeedsRestart(next) {
		t.Error("changing only the feedback switches requires a restart")
	}
}
