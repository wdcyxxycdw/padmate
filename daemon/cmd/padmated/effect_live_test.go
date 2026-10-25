package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// A profile whose east button toggles the layer alt, which has a color, with
// the feedback times and strength of its own.
const profileRoutes = `{"schemaVersion": 0, "name": "routes", "feedback": {"shortMs": 50, "strength": 20000},` +
	` "layers": {"base": {"east": {"press": {"type": "layer", "layer": "alt", "op": "toggle"}}},` +
	` "alt": {"color": "#112233"}}}` + "\n"

// feedbackCalls returns the "feedback" calls the fake source received for the
// gamepad dev.
func feedbackCalls(src *fakeSource, dev input.DeviceID) []input.Feedback {
	var got []input.Feedback
	for _, c := range src.effects.all() {
		if c.Kind == "feedback" && c.Device == dev {
			got = append(got, c.Feedback)
		}
	}
	return got
}

// pulsesOf returns the calls among calls that start a pulse train.
func pulsesOf(calls []input.Feedback) []input.Feedback {
	var got []input.Feedback
	for _, f := range calls {
		if f.Pulse {
			got = append(got, f)
		}
	}
	return got
}

// TestLiveFeedbackRoutesToTheSlotsDevice: the feedback of a slot reaches the
// source for the DeviceID the slot has, through the supervisor and padmated's
// effect queue, and for no other: with two gamepads in two slots, a layer
// change on the second reaches the second only. A fake source stands in for
// the Source, so no controller is reached and it needs no SDL.
func TestLiveFeedbackRoutesToTheSlotsDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(profileRoutes), 0o644); err != nil {
		t.Fatal(err)
	}
	src := newFakeSource()
	tl := &testLive{}
	cfg := testConfig(t, tl, "", nil, "--profile", path, "--dry-run")
	cfg.open = func(input.Options) (eventSource, error) { return src, nil }
	tl.start(t, cfg)

	const first, second = input.DeviceID(7), input.DeviceID(8)
	src.events <- input.Event{Device: first, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: first, Name: liveTestPad, Path: "fake7", IsGamepad: true}}
	tl.waitFor(t, tl.stdout, "added to slot 0")
	src.events <- input.Event{Device: second, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: second, Name: "2nd " + liveTestPad, Path: "fake8", IsGamepad: true}}
	tl.waitFor(t, tl.stdout, "added to slot 1")
	// A slot that gets its gamepad publishes its state to it (RefreshFeedback):
	// no pulse, no color for the base layer.
	for _, dev := range []input.DeviceID{first, second} {
		tl.waitUntil(t, "the first feedback of a gamepad", func() bool { return len(feedbackCalls(src, dev)) >= 1 })
		if f := feedbackCalls(src, dev)[0]; f.Pulse || f.Color != "" || f.Device != dev {
			t.Errorf("first feedback of %d = %+v, want no pulse and no color", dev, f)
		}
	}

	// East on the second gamepad switches its slot to alt: a pulse train and
	// the layer's color, for the second gamepad.
	src.events <- input.Event{Device: second, Kind: input.ButtonDown, Control: "east"}
	src.events <- input.Event{Device: second, Kind: input.ButtonUp, Control: "east"}
	tl.waitUntil(t, "the feedback of the layer change", func() bool { return len(pulsesOf(feedbackCalls(src, second))) >= 1 })
	f := pulsesOf(feedbackCalls(src, second))[0]
	if f.Device != second || f.Color != "#112233" || f.PulseMs != 50 || f.Strength != 20000 || f.Pulses < 1 {
		t.Errorf("feedback of the layer change = %+v, want the color of alt, pulses of 50 ms at 20000, for device %d", f, second)
	}
	if n := len(feedbackCalls(src, first)); n != 1 {
		t.Errorf("the first gamepad got %d feedback calls %+v, want only its first", n, feedbackCalls(src, first))
	}

	// And the first, after the second: each reaches its own.
	src.events <- input.Event{Device: first, Kind: input.ButtonDown, Control: "east"}
	src.events <- input.Event{Device: first, Kind: input.ButtonUp, Control: "east"}
	tl.waitUntil(t, "the feedback of the first gamepad's layer change", func() bool { return len(pulsesOf(feedbackCalls(src, first))) >= 1 })
	if f := pulsesOf(feedbackCalls(src, first))[0]; f.Device != first || f.Color != "#112233" {
		t.Errorf("feedback of the first gamepad = %+v", f)
	}
	// Nothing but the ambient state of the two gamepads was asked of the source.
	for _, c := range src.effects.all() {
		if c.Kind != "feedback" || (c.Device != first && c.Device != second) {
			t.Errorf("unexpected effect call %+v", c)
		}
	}
}

// A profile whose east button taps F13 and plays pulse2, with the feedback
// times and strength of its own, and whose south button has no haptic.
func profileHaptics(feedback string) string {
	return `{"schemaVersion": 0, "name": "haptics", "feedback": ` + feedback + `,` +
		` "layers": {"base": {` +
		`"east": {"haptic": "pulse2", "press": {"type": "keys", "keys": "F13", "mode": "tap"}},` +
		`"south": {"press": {"type": "keys", "keys": "F14", "mode": "tap"}}}}}` + "\n"
}

// playCalls returns the one-shot effects the fake source received for the
// gamepad dev.
func playCalls(src *fakeSource, dev input.DeviceID) []input.Effect {
	var got []input.Effect
	for _, c := range src.effects.all() {
		if c.Kind == "play" && c.Device == dev {
			got = append(got, c.Effect)
		}
	}
	return got
}

// TestLiveHapticRoutesToTheSlotsDevice (MAP-25): the haptic of a binding
// reaches the source for the DeviceID of the slot that dispatched it, as an
// Event-class effect with the profile's strength and pulse times, through the
// supervisor and the effect queue, and for no other gamepad; a binding without
// a haptic plays none, and the dry-run printer says what played. A fake
// source stands in for the Source, so no controller is reached.
func TestLiveHapticRoutesToTheSlotsDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(profileHaptics(`{"shortMs": 50, "gapMs": 60, "strength": 20000}`)), 0o644); err != nil {
		t.Fatal(err)
	}
	src := newFakeSource()
	tl := &testLive{}
	cfg := testConfig(t, tl, "", nil, "--profile", path, "--dry-run")
	cfg.open = func(input.Options) (eventSource, error) { return src, nil }
	tl.start(t, cfg)

	const first, second = input.DeviceID(7), input.DeviceID(8)
	src.events <- input.Event{Device: first, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: first, Name: liveTestPad, Path: "fake7", IsGamepad: true}}
	tl.waitFor(t, tl.stdout, "added to slot 0")
	src.events <- input.Event{Device: second, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: second, Name: "2nd " + liveTestPad, Path: "fake8", IsGamepad: true}}
	tl.waitFor(t, tl.stdout, "added to slot 1")

	press := func(dev input.DeviceID, control string) {
		src.events <- input.Event{Device: dev, Kind: input.ButtonDown, Control: control}
		src.events <- input.Event{Device: dev, Kind: input.ButtonUp, Control: control}
	}
	// South has no haptic; east on the second gamepad has.
	press(second, "south")
	tl.waitFor(t, tl.stdout, "send +F14")
	press(second, "east")
	tl.waitUntil(t, "the haptic of east", func() bool { return len(playCalls(src, second)) >= 1 })
	want := input.Effect{Device: second, Class: haptic.Event, Preset: haptic.Pulse2, Strength: 20000, ShortMs: 50, GapMs: 60}
	if got := playCalls(src, second); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("the second gamepad was asked for %+v; want exactly %+v", got, want)
	}
	tl.waitFor(t, tl.stdout, "haptic slot=1 pulse2")
	if got := playCalls(src, first); len(got) != 0 {
		t.Errorf("the first gamepad was asked for %+v; want nothing", got)
	}
	// And the first, after the second.
	press(first, "east")
	tl.waitUntil(t, "the haptic of the first gamepad", func() bool { return len(playCalls(src, first)) >= 1 })
	if got := playCalls(src, first); len(got) != 1 || got[0].Device != first || got[0].Preset != haptic.Pulse2 {
		t.Errorf("the first gamepad was asked for %+v", got)
	}
	tl.waitFor(t, tl.stdout, "haptic slot=0 pulse2")
	if n := len(playCalls(src, second)); n != 1 {
		t.Errorf("the second gamepad has %d haptics, want 1", n)
	}
}

// TestLiveHapticSilencedByRumbleOff: with the global rumble switch off the
// binding still sends its key but nothing is asked of the controller, and
// nothing is printed (MAP-25, ADR-0067).
func TestLiveHapticSilencedByRumbleOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(profileHaptics(`{}`)), 0o644); err != nil {
		t.Fatal(err)
	}
	src := newFakeSource()
	tl := &testLive{}
	cfg := testConfig(t, tl, "", nil, "--profile", path, "--dry-run")
	cfg.open = func(input.Options) (eventSource, error) { return src, nil }
	cfg.opts.settings = settingsOff("rumble")
	tl.start(t, cfg)

	const dev = input.DeviceID(7)
	src.events <- input.Event{Device: dev, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: dev, Name: liveTestPad, Path: "fake7", IsGamepad: true}}
	tl.waitFor(t, tl.stdout, "added to slot 0")
	src.events <- input.Event{Device: dev, Kind: input.ButtonDown, Control: "east"}
	src.events <- input.Event{Device: dev, Kind: input.ButtonUp, Control: "east"}
	tl.waitFor(t, tl.stdout, "send +F13")
	// Whatever would have been posted has been by now: a later press of a
	// binding without a haptic went through the same slot after it.
	src.events <- input.Event{Device: dev, Kind: input.ButtonDown, Control: "south"}
	src.events <- input.Event{Device: dev, Kind: input.ButtonUp, Control: "south"}
	tl.waitFor(t, tl.stdout, "send +F14")
	if got := playCalls(src, dev); len(got) != 0 {
		t.Errorf("a profile with rumble off asked for %+v", got)
	}
	if strings.Contains(tl.stdout.String(), "haptic slot=") {
		t.Errorf("the printer showed a haptic:\n%s", tl.stdout.String())
	}
}

// TestLiveHapticBurstNeverBlocksTheSlot (MAP-25): a controller that takes its
// time with a haptic does not slow the mapping. With the source holding every
// Play, more presses than the effect queue keeps are all dispatched; once the
// source goes on, it gets the haptic it was in the middle of and the newest of
// the rest, at most the line of the queue (maxPendingEvents).
func TestLiveHapticBurstNeverBlocksTheSlot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(profileHaptics(`{}`)), 0o644); err != nil {
		t.Fatal(err)
	}
	src := newFakeSource()
	src.playGate = make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(src.playGate) }) }
	tl := &testLive{}
	cfg := testConfig(t, tl, "", nil, "--profile", path, "--dry-run")
	cfg.open = func(input.Options) (eventSource, error) { return src, nil }
	tl.start(t, cfg)
	// Cleanups run last in, first out: let go before padmated is stopped, which
	// waits for the worker of the effect queue, even if the test fails.
	t.Cleanup(release)

	const dev = input.DeviceID(7)
	src.events <- input.Event{Device: dev, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: dev, Name: liveTestPad, Path: "fake7", IsGamepad: true}}
	tl.waitFor(t, tl.stdout, "added to slot 0")
	const presses = maxPendingEvents + 8
	for range presses {
		src.events <- input.Event{Device: dev, Kind: input.ButtonDown, Control: "east"}
		src.events <- input.Event{Device: dev, Kind: input.ButtonUp, Control: "east"}
	}
	// The printer says it at the dispatch, before the post, so each of the
	// presses came through the callback that posts to the queue.
	tl.waitForCount(t, tl.stdout, "haptic slot=0 pulse2", presses)
	if got := playCalls(src, dev); len(got) != 0 {
		t.Fatalf("the source got %d haptics through its gate", len(got))
	}
	release()
	tl.waitUntil(t, "the line of haptics", func() bool { return len(playCalls(src, dev)) >= 1+maxPendingEvents })
	time.Sleep(50 * time.Millisecond)
	if n := len(playCalls(src, dev)); n != 1+maxPendingEvents {
		t.Errorf("the source got %d haptics for %d presses, want the one in flight and %d queued", n, presses, maxPendingEvents)
	}
}
