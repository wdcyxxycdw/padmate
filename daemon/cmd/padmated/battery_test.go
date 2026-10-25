package main

import (
	"fmt"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

func pw(state input.PowerState, percent int) input.Power {
	return input.Power{State: state, Percent: percent}
}

// step is one reading of the tracker table: the power read, whether the
// controller's profile announces it, and whether an alert is due.
type step struct {
	p       input.Power
	enabled bool
	due     bool
}

func on(p input.Power, due bool) step  { return step{p, true, due} }
func off(p input.Power, due bool) step { return step{p, false, due} }

// TestBatteryTracker (IN-12): the thresholds, the hysteresis and the one alert
// per episode, as a table of readings of one controller.
func TestBatteryTracker(t *testing.T) {
	d, c, w, u := input.PowerDischarging, input.PowerCharging, input.PowerWired, input.PowerUnknown
	for _, tc := range []struct {
		name  string
		steps []step
	}{
		{"above the threshold is quiet", []step{on(pw(d, 100), false), on(pw(d, 50), false), on(pw(d, 26), false)}},
		{"at the threshold", []step{on(pw(d, 26), false), on(pw(d, 25), true)}},
		{"below the threshold at the first reading", []step{on(pw(d, 15), true)}},
		{"zero is low", []step{on(pw(d, 0), true)}},
		{"one alert per episode", []step{on(pw(d, 25), true), on(pw(d, 15), false), on(pw(d, 10), false), on(pw(d, 0), false)}},
		{"between the thresholds does not re-arm", []step{on(pw(d, 10), true), on(pw(d, 30), false), on(pw(d, 59), false), on(pw(d, 15), false)}},
		{"a charge from 60 re-arms", []step{on(pw(d, 10), true), on(pw(d, 59), false), on(pw(d, 20), false), on(pw(d, 60), false), on(pw(d, 20), true)}},
		{"charging re-arms", []step{on(pw(d, 10), true), on(pw(c, 12), false), on(pw(d, 10), true)}},
		{"wired re-arms", []step{on(pw(d, 10), true), on(pw(w, -1), false), on(pw(d, 10), true)}},
		{"charging is never low", []step{on(pw(c, 5), false), on(pw(c, 0), false), on(pw(w, 3), false)}},
		{"unknown is silent", []step{on(pw(u, -1), false), on(pw(u, -1), false)}},
		{"a charge that is not reported is silent", []step{on(pw(d, -1), false), on(pw(d, -1), false)}},
		{"unknown does not re-arm or end an episode", []step{on(pw(d, 10), true), on(pw(u, -1), false), on(pw(d, -1), false), on(pw(d, 10), false)}},
		// XInput reports 10, 40, 70 and 100: the alert is the 10, and the 40 that
		// the voltage can rebound to is not a re-arm; only the 70 (or charging) is.
		{"the XInput levels", []step{on(pw(d, 100), false), on(pw(d, 70), false), on(pw(d, 40), false), on(pw(d, 10), true), on(pw(d, 40), false), on(pw(d, 10), false), on(pw(d, 70), false), on(pw(d, 10), true)}},
		// The Switch protocol reports 0, 25, 50, 75 and 100: the critical 25 is the
		// alert, the 0 after it is the same episode, the 50 is not a re-arm.
		{"the Switch levels", []step{on(pw(d, 50), false), on(pw(d, 25), true), on(pw(d, 0), false), on(pw(d, 50), false), on(pw(d, 25), false), on(pw(d, 75), false), on(pw(d, 25), true)}},
		// A silent episode counts: the controller is disarmed, so switching
		// the alert on does not announce it later.
		{"a silent episode is an episode", []step{off(pw(d, 15), false), on(pw(d, 10), false), on(pw(d, 5), false)}},
		{"the next episode after a silent one is announced", []step{off(pw(d, 15), false), on(pw(c, 15), false), on(pw(d, 15), true)}},
		{"switched off mid-episode: the alert stays raised", []step{on(pw(d, 15), true), off(pw(d, 10), false), on(pw(d, 5), false)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := newBatteryTracker()
			for i, s := range tc.steps {
				if _, due := tr.update("pad", s.p, s.enabled); due != s.due {
					t.Fatalf("reading %d (%+v, enabled %t): due = %t, want %t", i, s.p, s.enabled, due, s.due)
				}
			}
		})
	}
}

// Each controller has its own episodes, and the episode numbers of the alerts
// are unique in a run: they tell the tray's notices apart.
func TestBatteryTrackerPerController(t *testing.T) {
	tr := newBatteryTracker()
	low := pw(input.PowerDischarging, 10)
	e1, due1 := tr.update("a", low, true)
	e2, due2 := tr.update("b", low, true)
	if !due1 || !due2 || e1 == e2 || e1 == 0 || e2 == 0 {
		t.Fatalf("alerts of two controllers = (%d, %t), (%d, %t); want two due, numbered differently", e1, due1, e2, due2)
	}
	if _, due := tr.update("a", low, true); due {
		t.Error("a second alert in the same episode")
	}
	tr.update("a", pw(input.PowerCharging, 50), true)
	e3, due3 := tr.update("a", low, true)
	if !due3 || e3 == e1 || e3 == e2 {
		t.Errorf("the next episode of a = (%d, %t), want a new number", e3, due3)
	}
}

// low is what the state event calls "low": a raised alert that the controller
// has not been re-armed from. A silent episode is not low.
func TestBatteryTrackerLow(t *testing.T) {
	tr := newBatteryTracker()
	if tr.low("pad") {
		t.Fatal("an unknown controller is low")
	}
	tr.update("pad", pw(input.PowerDischarging, 80), true)
	if tr.low("pad") {
		t.Error("80% is low")
	}
	tr.update("pad", pw(input.PowerDischarging, 15), true)
	if !tr.low("pad") {
		t.Error("an announced episode is not low")
	}
	tr.update("pad", pw(input.PowerDischarging, 50), true)
	if !tr.low("pad") {
		t.Error("the episode ended at 50%, below the re-arm threshold")
	}
	tr.update("pad", pw(input.PowerCharging, 25), true)
	if tr.low("pad") {
		t.Error("a charging controller is low")
	}
	tr.update("pad", pw(input.PowerDischarging, 15), false)
	if tr.low("pad") {
		t.Error("a silent episode is low: the overlay would tell it")
	}
}

// The tracker remembers a bounded number of controllers; the oldest goes first.
func TestBatteryTrackerIsBounded(t *testing.T) {
	tr := newBatteryTracker()
	low := pw(input.PowerDischarging, 10)
	for i := range 3 * maxBatteryPads {
		tr.update(fmt.Sprintf("k%d", i), low, true)
	}
	if n := len(tr.pads); n != maxBatteryPads || len(tr.order) != maxBatteryPads {
		t.Fatalf("the tracker holds %d controllers (%d in order), want %d", n, len(tr.order), maxBatteryPads)
	}
}

func TestBatteryNotice(t *testing.T) {
	n := batteryNotice("pad-key", 3, "Nova 2 Lite", 15)
	if n.Key != "battery:pad-key:3" || n.Title != "手柄电量低" || n.Body != "Nova 2 Lite 电量约 15%，请尽快充电。" || n.OncePerRun {
		t.Errorf("notice = %+v", n)
	}
}

// batteryCue is the Alert-class effect on the controller: pulses of three for
// rumble, the blink for the lights, each gated by its global switch (ADR-0067).
func TestBatteryCue(t *testing.T) {
	update := func(mod func(*profile.Feedback)) supervisor.PowerUpdate {
		f := profile.DefaultFeedback()
		mod(&f)
		return supervisor.PowerUpdate{Device: 7, Feedback: f}
	}
	none := func(*profile.Feedback) {}
	on := profile.DefaultFeedbackSwitches()
	off := func(rumble, led bool) profile.FeedbackSwitches {
		sw := on
		sw.Rumble, sw.LED = !rumble, !led
		return sw
	}
	for _, tc := range []struct {
		name   string
		mod    func(*profile.Feedback)
		sw     profile.FeedbackSwitches
		preset haptic.Preset
		blink  bool
		ok     bool
	}{
		{"defaults", none, on, haptic.Pulse3, true, true},
		{"rumble off", none, off(true, false), 0, true, true},
		{"lights off", none, off(false, true), haptic.Pulse3, false, true},
		{"both off", none, off(true, true), 0, false, false},
		{"strength 0 is no rumble", func(f *profile.Feedback) { f.Strength = 0 }, off(false, true), 0, false, false},
		{"pause off does not touch the alert", none, profile.FeedbackSwitches{Rumble: true, LED: true, LowBattery: true}, haptic.Pulse3, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, ok := batteryCue(update(tc.mod), tc.sw)
			if ok != tc.ok || e.Preset != tc.preset || e.Blink != tc.blink {
				t.Fatalf("cue = %+v, %t; want preset %v, blink %t, ok %t", e, ok, tc.preset, tc.blink, tc.ok)
			}
			if ok && (e.Device != 7 || e.Class != haptic.Alert) {
				t.Errorf("cue = %+v, want the Alert class on device 7", e)
			}
		})
	}
	e, _ := batteryCue(update(func(f *profile.Feedback) { f.ShortMs, f.GapMs, f.Strength = 50, 60, 20000 }), on)
	if e.ShortMs != 50 || e.GapMs != 60 || e.Strength != 20000 {
		t.Errorf("cue = %+v, want the profile's pulse times and strength", e)
	}
}

// episodeOf is the number of the alert a controller is in (0 when it is in
// none): the state event carries it, so that the overlay can tell a second
// episode that started between two snapshots, which never showed the controller
// as not low.
func TestBatteryTrackerEpisodeOf(t *testing.T) {
	tr := newBatteryTracker()
	if got := tr.episodeOf("pad"); got != 0 {
		t.Fatalf("an unknown controller is in episode %d", got)
	}
	d, c := input.PowerDischarging, input.PowerCharging
	e1, _ := tr.update("pad", pw(d, 15), true)
	if got := tr.episodeOf("pad"); got != e1 || e1 == 0 {
		t.Errorf("episodeOf = %d, want the alert's number %d", got, e1)
	}
	tr.update("pad", pw(d, 10), true) // the same episode
	if got := tr.episodeOf("pad"); got != e1 {
		t.Errorf("episodeOf in the same episode = %d, want %d", got, e1)
	}
	// Charged and low again before anything looked: another number, though the
	// controller was low at both snapshots.
	tr.update("pad", pw(c, 40), true)
	if got := tr.episodeOf("pad"); got != 0 {
		t.Errorf("episodeOf while charging = %d, want 0", got)
	}
	e2, due := tr.update("pad", pw(d, 15), true)
	if !due || e2 == e1 || tr.episodeOf("pad") != e2 {
		t.Errorf("second episode = %d (due %t), episodeOf %d; want a new number", e2, due, tr.episodeOf("pad"))
	}
	// A silent episode is no episode of the overlay's.
	tr.update("other", pw(d, 15), false)
	if got := tr.episodeOf("other"); got != 0 {
		t.Errorf("episodeOf a silent episode = %d, want 0", got)
	}
}
