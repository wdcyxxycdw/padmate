package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/tray"
)

// The low-battery alert (IN-12, ADR-0065) end to end on a fake source: the power
// of a gamepad goes through the router's OnPower to the tracker, which asks for
// the cue on the controller (the effect queue), the tray's notice and the
// overlay's batteries. No controller is reached and no SDL is needed.

const (
	padOne = input.DeviceID(1)
	padTwo = input.DeviceID(2)
)

// startBattery starts live mode on a fake source with the profile text (see
// profileModes) and slots slots, connects the gamepad 1 and returns them. The
// global feedback switches named in off start off.
func startBattery(t *testing.T, feedback string, slots int, off ...string) (*testLive, *fakeSource) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(profileModes(feedback)), 0o644); err != nil {
		t.Fatal(err)
	}
	src := newFakeSource()
	tl := &testLive{}
	cfg := testConfig(t, tl, "", nil, "--profile", path, "--dry-run", "--watch=false")
	cfg.open = func(input.Options) (eventSource, error) { return src, nil }
	cfg.slots = slots
	if len(off) > 0 {
		cfg.opts.settings = settingsOff(off...)
	}
	tl.start(t, cfg)
	tl.addPad(t, src)
	return tl, src
}

// addSecondPad connects the gamepad 2, which waits when there is no free slot.
func addSecondPad(src *fakeSource) {
	src.events <- input.Event{Device: padTwo, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: padTwo, Name: "2nd " + liveTestPad, Path: "fake2", IsGamepad: true}}
}

// alerts returns the Alert-class effects the fake source received for dev.
func alerts(src *fakeSource, dev input.DeviceID) []input.Effect {
	var got []input.Effect
	for _, e := range playCalls(src, dev) {
		if e.Class == haptic.Alert {
			got = append(got, e)
		}
	}
	return got
}

// flushEffects waits until the effect queue has handed everything posted
// before the call to the source: it posts the ambient state of a device that
// no test uses and waits for the source to get it. The queue is first in,
// first out.
func flushEffects(t *testing.T, tl *testLive, src *fakeSource) {
	t.Helper()
	tl.sync()
	const sentinel = input.DeviceID(999)
	before := len(feedbackCalls(src, sentinel))
	tl.effects.post(input.Feedback{Device: sentinel, Color: fmt.Sprintf("#%06x", before+1)})
	tl.waitUntil(t, "the effect queue to be drained", func() bool { return len(feedbackCalls(src, sentinel)) > before })
}

// power sends the power of gamepad dev.
func power(src *fakeSource, dev input.DeviceID, state input.PowerState, percent int) {
	src.events <- powerEvent(dev, state, percent)
}

// drainNotices returns the tray's notices queued so far.
func drainNotices(tl *testLive) []tray.Notification {
	var got []tray.Notification
	for {
		n, ok := tl.notices.Take()
		if !ok {
			return got
		}
		got = append(got, n)
	}
}

// TestLiveLowBatteryAlert (IN-12): a gamepad that discharges to 25% is announced
// once, on the controller (Alert class: three pulses and the flash) and in the
// tray; the next episode, after it was charged, is announced again, under
// another notice key, which the tray's never-forgetting queue lets through
// (TestBatteryNoticeKeysRepeatPerEpisode).
func TestLiveLowBatteryAlert(t *testing.T) {
	tl, src := startBattery(t, "", 1)
	power(src, padOne, input.PowerDischarging, 80)
	power(src, padOne, input.PowerDischarging, 26)
	flushEffects(t, tl, src)
	if got := alerts(src, padOne); len(got) != 0 {
		t.Fatalf("alerts above 25%%: %+v", got)
	}
	if n := drainNotices(tl); len(n) != 0 {
		t.Fatalf("notices above 25%%: %+v", n)
	}

	power(src, padOne, input.PowerDischarging, 15)
	tl.waitUntil(t, "the alert", func() bool { return len(alerts(src, padOne)) == 1 })
	a := alerts(src, padOne)[0]
	if a.Preset != haptic.Pulse3 || !a.Blink || a.Strength != 0x7000 || a.ShortMs != 70 || a.GapMs != 100 {
		t.Errorf("alert = %+v, want pulse3 with the flash, at the profile's strength and times", a)
	}
	notices := drainNotices(tl)
	if len(notices) != 1 || notices[0].Title != "手柄电量低" || notices[0].Body != liveTestPad+" 电量约 15%，请尽快充电。" ||
		!strings.HasPrefix(notices[0].Key, "battery:") || !strings.HasSuffix(notices[0].Key, ":1") {
		t.Fatalf("notices = %+v, want one balloon for the episode", notices)
	}
	tl.waitFor(t, tl.log, `"msg":"controller battery low"`)

	// The same episode, lower: nothing more.
	power(src, padOne, input.PowerDischarging, 10)
	power(src, padOne, input.PowerDischarging, 0)
	flushEffects(t, tl, src)
	if len(alerts(src, padOne)) != 1 || len(drainNotices(tl)) != 0 {
		t.Fatalf("the same episode alerted again: %+v", alerts(src, padOne))
	}

	// Charging re-arms at once, whatever the charge: the next drop is a new
	// episode, under a new key.
	power(src, padOne, input.PowerCharging, 40)
	power(src, padOne, input.PowerDischarging, 15)
	tl.waitUntil(t, "the second alert", func() bool { return len(alerts(src, padOne)) == 2 })
	again := drainNotices(tl)
	if len(again) != 1 || again[0].Key == notices[0].Key || !strings.HasSuffix(again[0].Key, ":2") {
		t.Fatalf("notices of the second episode = %+v, want one under a new key (first %q)", again, notices[0].Key)
	}
}

// TestBatteryNoticeKeysRepeatPerEpisode: tray.Notifications never forgets a key
// that is not OncePerRun, so the keys of two episodes of one controller differ
// and both balloons get through, while a notice with the key of one it has
// seen is dropped.
func TestBatteryNoticeKeysRepeatPerEpisode(t *testing.T) {
	q := tray.NewNotifications(16)
	if !q.Add(batteryNotice("pad", 1, "Nova", 15)) {
		t.Fatal("the first episode's notice was refused")
	}
	if q.Add(batteryNotice("pad", 1, "Nova", 10)) {
		t.Error("a second notice of the same episode was accepted")
	}
	if !q.Add(batteryNotice("pad", 2, "Nova", 12)) {
		t.Error("the next episode's notice was refused")
	}
	if !q.Add(batteryNotice("other", 1, "Other", 12)) {
		t.Error("another controller's notice was refused")
	}
	for range 3 {
		if _, ok := q.Take(); !ok {
			t.Fatal("a notice is missing")
		}
	}
}

// TestLiveLowBatterySwitches (UI-27, ADR-0067): the global lowBattery switch
// off is silent everywhere; rumble off keeps the flash and the notice but not
// the pulses; led off the pulses but not the flash. The profile's retired
// members do nothing.
func TestLiveLowBatterySwitches(t *testing.T) {
	for _, tc := range []struct {
		name     string
		feedback string
		off      []string
		silent   bool
		preset   haptic.Preset
		blink    bool
	}{
		{"default", ``, nil, false, haptic.Pulse3, true},
		{"lowBattery off", ``, []string{"lowBattery"}, true, 0, false},
		{"rumble off", ``, []string{"rumble"}, false, 0, true},
		{"led off", ``, []string{"led"}, false, haptic.Pulse3, false},
		{"pause off is not the alert's", ``, []string{"pause"}, false, haptic.Pulse3, true},
		{"strength 0", `{"strength": 0}`, nil, false, 0, true},
		{"the profile's lowBattery false is ignored", `{"lowBattery": false, "rumble": false}`, nil, false, haptic.Pulse3, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tl, src := startBattery(t, tc.feedback, 1, tc.off...)
			power(src, padOne, input.PowerDischarging, 10)
			if tc.silent {
				flushEffects(t, tl, src)
				if got := alerts(src, padOne); len(got) != 0 {
					t.Fatalf("alert with the switch off: %+v", got)
				}
				if n := drainNotices(tl); len(n) != 0 {
					t.Fatalf("notice with the switch off: %+v", n)
				}
				return
			}
			tl.waitUntil(t, "the alert", func() bool { return len(alerts(src, padOne)) == 1 })
			if a := alerts(src, padOne)[0]; a.Preset != tc.preset || a.Blink != tc.blink {
				t.Errorf("alert = %+v, want preset %v, blink %t", a, tc.preset, tc.blink)
			}
			// The notice is the controller's and the tray's: no switch but
			// lowBattery takes it away.
			if n := drainNotices(tl); len(n) != 1 {
				t.Errorf("notices = %+v, want one", n)
			}
		})
	}
	t.Run("rumble and lights off", func(t *testing.T) {
		tl, src := startBattery(t, "", 1, "rumble", "led")
		power(src, padOne, input.PowerDischarging, 10)
		flushEffects(t, tl, src)
		if got := alerts(src, padOne); len(got) != 0 {
			t.Errorf("the controller was asked to alert with rumble and lights off: %+v", got)
		}
		if n := drainNotices(tl); len(n) != 1 {
			t.Errorf("notices = %+v, want the tray's one", n)
		}
	})
}

// TestLiveLowBatteryUnknownIsSilent: a controller that does not tell its
// battery, or its charge, never alerts.
func TestLiveLowBatteryUnknownIsSilent(t *testing.T) {
	tl, src := startBattery(t, "", 1)
	power(src, padOne, input.PowerUnknown, -1)
	power(src, padOne, input.PowerDischarging, -1)
	power(src, padOne, input.PowerCharging, 5)
	power(src, padOne, input.PowerWired, -1)
	flushEffects(t, tl, src)
	if got := alerts(src, padOne); len(got) != 0 {
		t.Fatalf("alerts for a controller that tells nothing, or is plugged in: %+v", got)
	}
	if n := drainNotices(tl); len(n) != 0 {
		t.Fatalf("notices: %+v", n)
	}
}

// TestLiveLowBatteryRevivedPadDoesNotAlertAgain: the receiver's watchdog
// reports a sleeping controller removed and gives the one that wakes a new
// connection ID; its DeviceKey is the same, and the episode goes on.
func TestLiveLowBatteryRevivedPadDoesNotAlertAgain(t *testing.T) {
	tl, src := startBattery(t, "", 1)
	power(src, padOne, input.PowerDischarging, 15)
	tl.waitUntil(t, "the alert", func() bool { return len(alerts(src, padOne)) == 1 })
	drainNotices(tl)

	src.events <- input.Event{Device: padOne, Kind: input.DeviceRemoved}
	const alias = input.DeviceID(1<<32 - 1)
	src.events <- input.Event{Device: alias, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: alias, Name: liveTestPad, Path: "fake1", IsGamepad: true}}
	power(src, alias, input.PowerDischarging, 15)
	flushEffects(t, tl, src)
	if got := alerts(src, alias); len(got) != 0 {
		t.Fatalf("the revived controller was alerted again: %+v", got)
	}
	if n := drainNotices(tl); len(n) != 0 {
		t.Fatalf("notices for the revived controller: %+v", n)
	}
	// Still low: the overlay's list says so under the new connection ID.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tl.waitUntil(t, "the revived controller in the batteries", func() bool {
		b := tl.batteries(ctx)
		return len(b) == 1 && b[0].Device == alias && b[0].Low
	})
}

// TestLiveLowBatteryProfileSwitchDoesNotAlertAgain: a profile that is saved or
// switched to mid-episode neither alerts a controller that was announced nor
// announces one that was silent.
func TestLiveLowBatteryProfileSwitchDoesNotAlertAgain(t *testing.T) {
	rl, cfg := newReloadLive(t, profileModes(""))
	rl.begin(t, cfg)
	power(rl.src, padOne, input.PowerDischarging, 15)
	rl.waitUntil(t, "the alert", func() bool { return len(alerts(rl.src, padOne)) == 1 })

	rl.write(t, profileModes(`{"gapMs": 90}`))
	rl.waitForCount(t, rl.log, `"msg":"profile reloaded"`, 1)
	power(rl.src, padOne, input.PowerDischarging, 10)
	rl.write(t, profileModes(`{"shortMs": 80}`))
	rl.waitForCount(t, rl.log, `"msg":"profile reloaded"`, 2)
	power(rl.src, padOne, input.PowerDischarging, 5)
	flushEffects(t, rl.testLive, rl.src)
	if got := alerts(rl.src, padOne); len(got) != 1 {
		t.Fatalf("alerts after the profile switches: %+v, want the one of the episode", got)
	}

	// The alert is per episode, whatever the profile says at the time: the
	// next episode is announced under the profile that is current.
	power(rl.src, padOne, input.PowerCharging, 50)
	power(rl.src, padOne, input.PowerDischarging, 12)
	rl.waitUntil(t, "the alert of the next episode", func() bool { return len(alerts(rl.src, padOne)) == 2 })
	if a := alerts(rl.src, padOne)[1]; a.ShortMs != 80 {
		t.Errorf("alert = %+v, want the pulse time of the profile in force (80 ms)", a)
	}
}

// TestLiveLowBatterySilentEpisodeIsNotAnnouncedLater: an episode that started
// with the global alert switch off is not announced when the switch is turned
// on (a committed config.json); the next episode is.
func TestLiveLowBatterySilentEpisodeIsNotAnnouncedLater(t *testing.T) {
	tl, src := startBattery(t, "", 1, "lowBattery")
	power(src, padOne, input.PowerDischarging, 15)
	flushEffects(t, tl, src)
	tl.applySwitches(t)
	power(src, padOne, input.PowerDischarging, 10)
	flushEffects(t, tl, src)
	if got := alerts(src, padOne); len(got) != 0 {
		t.Fatalf("a silent episode was announced later: %+v", got)
	}
	power(src, padOne, input.PowerCharging, 50)
	power(src, padOne, input.PowerDischarging, 10)
	tl.waitUntil(t, "the next episode's alert", func() bool { return len(alerts(src, padOne)) == 1 })
}

// TestLiveLowBatteryAppliesToEveryPad (UI-27, ADR-0067): the switches are the
// same for a gamepad that holds a slot and one that waits for it: the alert is
// off for both or on for both, and the controller's part follows rumble and
// lights for both. Strength and pulse times are the profile's for the mapped
// pad and the defaults for the waiting one.
func TestLiveLowBatteryAppliesToEveryPad(t *testing.T) {
	for _, tc := range []struct {
		name   string
		off    []string
		alert  bool
		preset haptic.Preset
		blink  bool
	}{
		{"default", nil, true, haptic.Pulse3, true},
		{"alert off", []string{"lowBattery"}, false, 0, false},
		{"rumble off", []string{"rumble"}, true, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The profile's retired members say the opposite and do nothing.
			tl, src := startBattery(t, `{"lowBattery": `+fmt.Sprint(!tc.alert)+`, "rumble": false}`, 1, tc.off...)
			addSecondPad(src) // every slot is taken: it waits
			tl.waitUntil(t, "the waiting pad", func() bool {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				devices, err := tl.sup.Devices(ctx)
				return err == nil && len(devices) == 2
			})
			power(src, padOne, input.PowerDischarging, 10)
			power(src, padTwo, input.PowerDischarging, 10)
			if !tc.alert {
				flushEffects(t, tl, src)
				if a1, a2 := alerts(src, padOne), alerts(src, padTwo); len(a1)+len(a2) != 0 {
					t.Errorf("alerts with the switch off: %+v, %+v", a1, a2)
				}
				if n := drainNotices(tl); len(n) != 0 {
					t.Errorf("notices with the switch off: %+v", n)
				}
				return
			}
			tl.waitUntil(t, "both alerts", func() bool { return len(alerts(src, padOne)) == 1 && len(alerts(src, padTwo)) == 1 })
			for _, dev := range []input.DeviceID{padOne, padTwo} {
				if a := alerts(src, dev)[0]; a.Preset != tc.preset || a.Blink != tc.blink {
					t.Errorf("alert of pad %d = %+v, want preset %v, blink %t", dev, a, tc.preset, tc.blink)
				}
			}
			if n := drainNotices(tl); len(n) != 2 {
				t.Errorf("notices = %+v, want one for each pad", n)
			}
		})
	}
}

// TestLiveBatteriesCarryTheEpisode: the state event's batteries say which alert
// a low controller is in, so that a second episode that began between two
// snapshots (charged and low again within 250 ms, never seen as not low) is
// still a new alert for the overlay.
func TestLiveBatteriesCarryTheEpisode(t *testing.T) {
	tl, src := startBattery(t, "", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	power(src, padOne, input.PowerDischarging, 80)
	tl.waitUntil(t, "the battery", func() bool { return len(tl.batteries(ctx)) == 1 })
	if b := tl.batteries(ctx)[0]; b.Low || b.Episode != 0 {
		t.Fatalf("a controller that is not low = %+v, want episode 0", b)
	}
	power(src, padOne, input.PowerDischarging, 15)
	tl.waitUntil(t, "the first episode", func() bool { b := tl.batteries(ctx); return len(b) == 1 && b[0].Low })
	first := tl.batteries(ctx)[0]
	if first.Episode == 0 {
		t.Fatalf("a low controller = %+v, want its episode", first)
	}
	// Charged and low again at once: low at both snapshots, another episode.
	power(src, padOne, input.PowerCharging, 40)
	power(src, padOne, input.PowerDischarging, 15)
	tl.waitUntil(t, "the second episode", func() bool { b := tl.batteries(ctx); return len(b) == 1 && b[0].Low && b[0].Episode != first.Episode })
	if second := tl.batteries(ctx)[0]; second.Episode <= first.Episode {
		t.Errorf("episodes %d then %d, want a larger number", first.Episode, second.Episode)
	}
	raw, err := json.Marshal(tl.batteries(ctx)[0])
	if err != nil || !strings.Contains(string(raw), `"episode":`) {
		t.Errorf("battery JSON = %s (%v), want the episode member", raw, err)
	}
	power(src, padOne, input.PowerCharging, 60)
	tl.waitUntil(t, "the end of the episode", func() bool { b := tl.batteries(ctx); return len(b) == 1 && !b[0].Low })
	if strings.Contains(mustJSON(t, tl.batteries(ctx)[0]), `"episode"`) {
		t.Errorf("a controller that is not low has an episode member")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestLiveLowBatteryIgnoredPadIsLeftAlone: a gamepad the ignore setting leaves
// out is not padmate's to alert, though its battery is shown.
func TestLiveLowBatteryIgnoredPadIsLeftAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(profileModes("")), 0o644); err != nil {
		t.Fatal(err)
	}
	src := newFakeSource()
	tl := &testLive{}
	cfg := testConfig(t, tl, "", nil, "--profile", path, "--dry-run", "--watch=false", "--device", "nobody-has-this-name")
	cfg.open = func(input.Options) (eventSource, error) { return src, nil }
	tl.start(t, cfg)
	src.events <- input.Event{Device: padOne, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: padOne, Name: liveTestPad, Path: "fake1", IsGamepad: true}}
	power(src, padOne, input.PowerDischarging, 5)
	tl.waitUntil(t, "the ignored pad's battery", func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		devices, err := tl.sup.Devices(ctx)
		return err == nil && len(devices) == 1 && devices[0].Battery != nil && devices[0].Status == "ignored"
	})
	flushEffects(t, tl, src)
	if got := alerts(src, padOne); len(got) != 0 {
		t.Errorf("an ignored gamepad was alerted: %+v", got)
	}
	if n := drainNotices(tl); len(n) != 0 {
		t.Errorf("notices for an ignored gamepad: %+v", n)
	}
}

// TestStateEventCarriesBatteries (IN-12): the engine.activity state event lists
// the power of each gamepad that has read one, with its key and name and "low":
// true while the alert of its episode is raised (and only then), which is what
// the overlay turns into its toast on the false-to-true edge; the member is an
// empty array before any gamepad read a power.
func TestStateEventCarriesBatteries(t *testing.T) {
	tl, src := startBattery(t, "", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	observed := make(chan struct{})
	go func() { defer close(observed); tl.observeIPC(ctx) }()
	t.Cleanup(func() { cancel(); <-observed })
	events := make(chan string, 256)
	client := rpcTestClient(t, tl.liveRun, &jrpc2.ClientOptions{OnNotify: func(r *jrpc2.Request) {
		if r.Method() == "engine.activity" && strings.Contains(r.ParamString(), `"kind":"state"`) {
			events <- r.ParamString()
		}
	}})
	if err := client.CallResult(ctx, "subscribe", map[string]any{"topics": []string{"engine.activity"}}, new(any)); err != nil {
		t.Fatal(err)
	}
	type battery struct {
		Device  int    `json:"device"`
		Key     string `json:"key"`
		Name    string `json:"name"`
		State   string `json:"state"`
		Percent *int   `json:"percent"`
		Low     bool   `json:"low"`
	}
	next := func(what string, want func(b []battery, raw string) bool) []battery {
		t.Helper()
		for {
			select {
			case data := <-events:
				var s struct {
					Batteries *[]battery `json:"batteries"`
				}
				if err := json.Unmarshal([]byte(data), &s); err != nil {
					t.Fatalf("%s: %v in %s", what, err, data)
				}
				if s.Batteries == nil {
					t.Fatalf("%s: the state event has no batteries member: %s", what, data)
				}
				if want(*s.Batteries, data) {
					return *s.Batteries
				}
			case <-ctx.Done():
				t.Fatalf("%s: no such state event", what)
			}
		}
	}

	next("before any reading", func(b []battery, raw string) bool {
		return len(b) == 0 && strings.Contains(raw, `"batteries":[]`)
	})
	power(src, padOne, input.PowerDischarging, 60)
	got := next("a reading", func(b []battery, _ string) bool { return len(b) == 1 })
	if b := got[0]; b.Device != 1 || b.Name != liveTestPad || b.State != "discharging" || b.Percent == nil || *b.Percent != 60 || b.Low || b.Key == "" {
		t.Errorf("battery = %+v, want the reading, not low", b)
	}
	power(src, padOne, input.PowerDischarging, 15)
	got = next("low", func(b []battery, _ string) bool { return len(b) == 1 && b[0].Low })
	if got[0].Percent == nil || *got[0].Percent != 15 {
		t.Errorf("low battery = %+v", got[0])
	}
	// Between the thresholds the episode goes on.
	power(src, padOne, input.PowerDischarging, 40)
	next("40%", func(b []battery, _ string) bool {
		return len(b) == 1 && b[0].Percent != nil && *b[0].Percent == 40 && b[0].Low
	})
	power(src, padOne, input.PowerCharging, -1)
	got = next("charging", func(b []battery, raw string) bool {
		return len(b) == 1 && b[0].State == "charging" && !b[0].Low && strings.Contains(raw, `"state":"charging","low":false`)
	})
	if got[0].Percent != nil {
		t.Errorf("a charge that is not reported has a percent: %+v", got[0])
	}
}

// TestTrayShowsTheBattery (IN-12): the tray's menu line of a pad has its power,
// and the low-battery balloon reaches the tray's notification. A fake renderer
// stands in for the tray, as in the pause test.
func TestTrayShowsTheBattery(t *testing.T) {
	tl, src := startBattery(t, "", 1)
	var mu sync.Mutex
	var details []string
	var notified []tray.Notification
	closed := make(chan struct{})
	var once sync.Once
	ui := &trayUI{pause: make(chan struct{}), quit: make(chan struct{}),
		update: func(p tray.Presentation, _, _ bool) {
			mu.Lock()
			defer mu.Unlock()
			details = p.Details
		},
		notify: func(n tray.Notification) error {
			mu.Lock()
			defer mu.Unlock()
			notified = append(notified, n)
			return nil
		},
		close: func() { once.Do(func() { close(closed) }) },
		run:   func(ready, exit func(), _ func(error)) { ready(); <-closed; exit() },
	}
	finished := make(chan int, 1)
	go func() { finished <- tl.driveTray(ui) }()
	t.Cleanup(func() {
		tl.endStop(stopCause("tray"))
		select {
		case <-finished:
		case <-time.After(liveWait):
		}
	})
	menu := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), details...)
	}
	tl.waitUntil(t, "the first menu", func() bool { return len(menu()) == 1 })
	if line := menu()[0]; strings.Contains(line, "电量") {
		t.Fatalf("menu line before a reading = %q", line)
	}
	power(src, padOne, input.PowerDischarging, 80)
	tl.waitUntil(t, "the power in the menu", func() bool {
		m := menu()
		return len(m) == 1 && strings.HasSuffix(m[0], " · 电量 80%")
	})
	power(src, padOne, input.PowerCharging, 82)
	tl.waitUntil(t, "charging in the menu", func() bool {
		m := menu()
		return len(m) == 1 && strings.HasSuffix(m[0], " · 电量 82% · 充电中")
	})
	power(src, padOne, input.PowerDischarging, 12)
	tl.waitUntil(t, "the balloon", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(notified) == 1
	})
	mu.Lock()
	n := notified[0]
	mu.Unlock()
	if n.Title != "手柄电量低" || n.Body != liveTestPad+" 电量约 12%，请尽快充电。" {
		t.Errorf("balloon = %+v", n)
	}
}
