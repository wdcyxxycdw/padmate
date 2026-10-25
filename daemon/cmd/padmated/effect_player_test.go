package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// The global feedback switches (UI-27, ADR-0067) and the player lights (HUD-08)
// reach the controller as the ambient state of input.Feedback: PlayerMode is
// the mode number while the lights switch is on, and 0 otherwise. A fake source
// stands in for the Source, so no controller is reached and no SDL is needed.

// profileModes cycles base, alt and third on east; alt has a color. feedback is
// the member of the profile ("" for none).
func profileModes(feedback string) string {
	member := ""
	if feedback != "" {
		member = `"feedback": ` + feedback + `, `
	}
	return `{"schemaVersion": 1, "name": "modes", ` + member +
		`"common": {"east": {"press": {"type": "layer", "op": "cycle", "layers": ["base", "alt", "third"]}}},` +
		` "layers": {"base": {}, "alt": {"color": "#112233"}, "third": {}}}` + "\n"
}

// modesOf returns the PlayerMode of each feedback call for dev, in order.
func modesOf(calls []input.Feedback) []int {
	var got []int
	for _, f := range calls {
		got = append(got, f.PlayerMode)
	}
	return got
}

// startModes starts live mode with the profile text on a fake source and
// connects the gamepad 1 to slot 0. The global feedback switches named in off
// ("rumble", "led", "pause", "lowBattery") start off.
func startModes(t *testing.T, text string, off ...string) (*testLive, *fakeSource) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	src := newFakeSource()
	tl := &testLive{}
	cfg := testConfig(t, tl, "", nil, "--profile", path, "--dry-run")
	cfg.open = func(input.Options) (eventSource, error) { return src, nil }
	if len(off) > 0 {
		cfg.opts.settings = settingsOff(off...)
	}
	tl.start(t, cfg)
	tl.addPad(t, src)
	return tl, src
}

// TestLivePlayerLightsFollowTheMode (HUD-08): the mode of the layer is the
// PlayerMode of the ambient state the source gets for the slot's gamepad: 1
// for base, 2 for alt, 3 for third, and again 1 (and, past 4, a mode that no
// light shows, which the Source takes as none).
func TestLivePlayerLightsFollowTheMode(t *testing.T) {
	tl, src := startModes(t, profileModes(""))
	const dev = input.DeviceID(1)
	tl.waitUntil(t, "the first feedback", func() bool { return len(feedbackCalls(src, dev)) >= 1 })
	for want := 2; want <= 3; want++ {
		button(src, "east", true)
		button(src, "east", false)
		tl.waitUntil(t, "the feedback of the layer change", func() bool { return len(pulsesOf(feedbackCalls(src, dev))) >= want-1 })
	}
	button(src, "east", true)
	button(src, "east", false)
	tl.waitUntil(t, "the return to base", func() bool { return len(pulsesOf(feedbackCalls(src, dev))) >= 3 })
	calls := feedbackCalls(src, dev)
	if got, want := modesOf(calls), []int{1, 2, 3, 1}; !slices.Equal(got, want) {
		t.Fatalf("PlayerMode of the feedback calls = %v, want %v", got, want)
	}
	if calls[1].Color != "#112233" || calls[0].Color != "" {
		t.Errorf("colors = %q, %q; the player lights are independent of the color", calls[0].Color, calls[1].Color)
	}
}

// TestLiveLightsOffMeansNoPlayerMode: with the lights switch off there is no
// color and no PlayerMode, whatever the layer, so the Source gives the index
// back and leaves the lights alone (UI-27). The pulses are another switch.
func TestLiveLightsOffMeansNoPlayerMode(t *testing.T) {
	tl, src := startModes(t, profileModes(""), "led")
	const dev = input.DeviceID(1)
	button(src, "east", true)
	button(src, "east", false)
	tl.waitUntil(t, "the feedback of the layer change", func() bool { return len(pulsesOf(feedbackCalls(src, dev))) >= 1 })
	for _, f := range feedbackCalls(src, dev) {
		if f.PlayerMode != 0 || f.Color != "" {
			t.Errorf("feedback with the lights off = %+v, want no mode and no color", f)
		}
	}
	if p := pulsesOf(feedbackCalls(src, dev)); len(p) != 1 || p[0].Pulses != 2 {
		t.Errorf("pulses = %+v, want the two pulses of alt: led is not rumble", p)
	}
}

// TestLiveRumbleOffKeepsTheLights: with the rumble switch off no pulse is asked
// for, and the color and the player lights go on.
func TestLiveRumbleOffKeepsTheLights(t *testing.T) {
	tl, src := startModes(t, profileModes(""), "rumble")
	const dev = input.DeviceID(1)
	button(src, "east", true)
	button(src, "east", false)
	tl.waitUntil(t, "the feedback of the layer change", func() bool {
		calls := feedbackCalls(src, dev)
		return len(calls) > 0 && calls[len(calls)-1].PlayerMode == 2
	})
	calls := feedbackCalls(src, dev)
	if last := calls[len(calls)-1]; last.Color != "#112233" || last.Pulse {
		t.Errorf("last feedback = %+v, want the color of alt and no pulse", last)
	}
	if p := pulsesOf(calls); len(p) != 0 {
		t.Errorf("rumble off asked for pulses: %+v", p)
	}
}

// TestLivePauseSwitch (UI-27): pausing and resuming the mapping asks for the
// pause pulse and color with the default profile, and for nothing but the
// state with the pause switch off: no pulse, and the color of the layer. The
// profile's retired pause member does nothing (ADR-0067).
func TestLivePauseSwitch(t *testing.T) {
	for _, tc := range []struct {
		name      string
		feedback  string
		off       []string
		wantPulse bool
		wantColor string
	}{
		{"default", ``, nil, true, "#FFAA00"},
		{"pause off", ``, []string{"pause"}, false, "#112233"},
		{"the profile's pause false is ignored", `{"pause": false}`, nil, true, "#FFAA00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tl, src := startModes(t, profileModes(tc.feedback), tc.off...)
			const dev = input.DeviceID(1)
			button(src, "east", true) // alt, whose color is #112233
			button(src, "east", false)
			tl.waitUntil(t, "alt", func() bool { return len(pulsesOf(feedbackCalls(src, dev))) >= 1 })
			before := len(feedbackCalls(src, dev))
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := tl.sup.Pause(ctx, true); err != nil {
				t.Fatal(err)
			}
			tl.waitUntil(t, "the feedback of the pause", func() bool { return len(feedbackCalls(src, dev)) > before })
			f := feedbackCalls(src, dev)[before]
			if f.Pulse != tc.wantPulse || f.Color != tc.wantColor || !f.Paused || f.PlayerMode != 2 {
				t.Errorf("the pause = %+v, want pulse %t, color %s, the mode 2 of alt", f, tc.wantPulse, tc.wantColor)
			}
			if err := tl.sup.Pause(ctx, false); err != nil {
				t.Fatal(err)
			}
			tl.waitUntil(t, "the feedback of the resume", func() bool { return len(feedbackCalls(src, dev)) > before+1 })
			f = feedbackCalls(src, dev)[before+1]
			if f.Pulse != tc.wantPulse || f.Color != "#112233" || f.Paused || f.PlayerMode != 2 {
				t.Errorf("the resume = %+v, want pulse %t, the color of alt, the mode 2 of alt", f, tc.wantPulse)
			}
		})
	}
}

// TestLiveLightsOffAppliesWithoutReload (UI-27, ADR-0067): turning the lights
// switch off in config.json re-publishes the state of the slot without a color
// and without a mode, so the Source releases the LED and the player lights;
// the profile is not reloaded (no key is let go, no script restarts), and the
// pulses stay (they are the rumble switch's). Turning it on gives the lights
// back, without a pulse.
func TestLiveLightsOffAppliesWithoutReload(t *testing.T) {
	tl, src := startModes(t, profileModes(""))
	const dev = input.DeviceID(1)
	button(src, "east", true)
	button(src, "east", false)
	tl.waitUntil(t, "alt, with its light", func() bool {
		calls := feedbackCalls(src, dev)
		return len(calls) > 0 && calls[len(calls)-1].PlayerMode == 2
	})
	gen := tl.profileTarget.Load().gen
	before := len(feedbackCalls(src, dev))
	tl.applySwitches(t, "led")
	tl.waitUntil(t, "the feedback of the change", func() bool { return len(feedbackCalls(src, dev)) > before })
	calls := feedbackCalls(src, dev)
	if f := calls[len(calls)-1]; f.PlayerMode != 0 || f.Color != "" || f.Pulse {
		t.Errorf("feedback after the lights went off = %+v, want the lights released and no pulse", f)
	}
	before = len(calls)
	tl.applySwitches(t)
	tl.waitUntil(t, "the feedback of the change back", func() bool { return len(feedbackCalls(src, dev)) > before })
	calls = feedbackCalls(src, dev)
	if f := calls[len(calls)-1]; f.PlayerMode != 2 || f.Color != "#112233" || f.Pulse {
		t.Errorf("feedback after the lights came back = %+v, want the mode and color of alt and no pulse", f)
	}
	if got := tl.profileTarget.Load().gen; got != gen {
		t.Errorf("the feedback switches reloaded the profile: generation %d, was %d", got, gen)
	}
}

// TestLiveRumbleOffStopsWhatItGates: with rumble turned off the running rumble
// of every class a switch gates is stopped on every controller, and turning the
// lights off gives up a script's color; unchanged switches stop nothing.
func TestLiveRumbleOffStopsWhatItGates(t *testing.T) {
	tl, src := startModes(t, profileModes(""))
	const dev = input.DeviceID(1)
	stops := func() (n int, classes []string) {
		for _, c := range src.effects.all() {
			if c.Kind == "stop" && c.Device == dev {
				n++
				classes = append(classes, c.Stop.Class.String())
			}
		}
		return n, classes
	}
	lights := func() (n int) {
		for _, c := range src.effects.all() {
			if c.Kind == "light" && c.Device == dev && c.Light.Class == haptic.Script && c.Light.Release {
				n++
			}
		}
		return n
	}
	tl.applySwitches(t, "pause", "lowBattery") // neither is rumble nor lights
	flushEffects(t, tl, src)
	if n, _ := stops(); n != 0 || lights() != 0 {
		t.Fatalf("pause and low battery stopped %d effects and released %d lights", n, lights())
	}
	tl.applySwitches(t, "pause", "lowBattery", "rumble")
	tl.waitUntil(t, "the stops", func() bool { n, _ := stops(); return n == 3 })
	if n, classes := stops(); n != 3 || !slices.Equal(classes, []string{"event", "script", "alert"}) {
		t.Errorf("rumble off stopped %v, want the event, script and alert classes", classes)
	}
	if lights() != 0 {
		t.Errorf("rumble off released a script's light")
	}
	tl.applySwitches(t, "pause", "lowBattery", "rumble", "led")
	tl.waitUntil(t, "the release of the script's light", func() bool { return lights() == 1 })
	if n, _ := stops(); n != 3 {
		t.Errorf("lights off stopped rumble again: %d stops", n)
	}
}
