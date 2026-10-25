package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// TestFakeInputCapabilities: the fake gamepad can do what E2E needs to see an
// identify (rumble, a color LED, player lights) unless PADMATE_FAKE_CAPS says
// otherwise; the list is validated.
func TestFakeInputCapabilities(t *testing.T) {
	all := input.Capabilities{Known: true, Rumble: true, LED: true, RGBLED: true, PlayerLED: true}
	for _, tc := range []struct {
		list string
		want input.Capabilities
	}{
		{"", all},
		{"  ", all},
		{"none", input.Capabilities{Known: true}},
		{"rumble", input.Capabilities{Known: true, Rumble: true}},
		{"rumble, trigger", input.Capabilities{Known: true, Rumble: true, TriggerRumble: true}},
		{"led", input.Capabilities{Known: true, LED: true}},
		{"rgb", input.Capabilities{Known: true, LED: true, RGBLED: true}},
		{"player,rumble", input.Capabilities{Known: true, Rumble: true, PlayerLED: true}},
		{"rumble,led,rgb,player", all},
	} {
		got, err := parseFakeCaps(tc.list)
		if err != nil || got != tc.want {
			t.Errorf("parseFakeCaps(%q) = %+v, %v; want %+v", tc.list, got, err, tc.want)
		}
	}
	for _, list := range []string{"rumble,", "Rumble", "vibration", "none,bogus", ","} {
		if _, err := parseFakeCaps(list); err == nil {
			t.Errorf("parseFakeCaps(%q) accepted", list)
		}
	}

	src := newRPCInputSource(input.Options{})
	added := <-src.Events()
	src.Close()
	if added.Info.Capabilities != all {
		t.Errorf("the fake gamepad reports %+v, want %+v", added.Info.Capabilities, all)
	}
	t.Setenv(fakeCapsEnv, "none")
	caps, err := fakeCapsFromEnv()
	if err != nil || caps != (input.Capabilities{Known: true}) {
		t.Errorf("%s=none gives %+v, %v", fakeCapsEnv, caps, err)
	}
	t.Setenv(fakeCapsEnv, "bogus")
	if _, err := fakeCapsFromEnv(); err == nil {
		t.Errorf("%s=bogus accepted", fakeCapsEnv)
	}
}

// fakeEffects is the answer of fake.effects.
type fakeEffects struct {
	Effects []fakeEffect `json:"effects"`
}

type fakeEffect struct {
	Kind       string `json:"kind"`
	Device     int    `json:"device"`
	Color      string `json:"color"`
	Pulse      bool   `json:"pulse"`
	Paused     bool   `json:"paused"`
	PlayerMode int    `json:"playerMode"`
	Cues       *struct {
		Rumble bool `json:"rumble"`
		LED    bool `json:"led"`
		Player bool `json:"player"`
	} `json:"cues"`
	Rumble []struct {
		Low  int `json:"low"`
		High int `json:"high"`
		Ms   int `json:"ms"`
	} `json:"rumble"`
	LED []struct {
		Color string `json:"color"`
		Ms    int    `json:"ms"`
	} `json:"led"`
	Player []struct {
		Index int `json:"index"`
		Ms    int `json:"ms"`
	} `json:"player"`
}

// startFakeEffects starts the fake IPC daemon, whose gamepad has the
// capabilities caps (PADMATE_FAKE_CAPS), and returns a client of it.
func startFakeEffects(t *testing.T, caps string) (*testLive, *jrpc2.Client) {
	t.Helper()
	return startFakeEffectsProfile(t, caps, `{"schemaVersion":0,"name":"fake","layers":{"base":{}}}`)
}

// startFakeEffectsProfile is startFakeEffects with the profile text profile.
func startFakeEffectsProfile(t *testing.T, caps, profile string) (*testLive, *jrpc2.Client) {
	t.Helper()
	t.Setenv(fakeCapsEnv, caps)
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	tl := &testLive{}
	cfg := testConfig(t, tl, "", nil, "--profile", path, "--fake-input=ipc", "--dry-run", "--watch=false")
	cfg.opts.device = ""
	cfg.open = func(input.Options) (eventSource, error) { panic("fake IPC opened SDL") }
	tl.start(t, cfg)
	return tl, rpcTestClient(t, tl.liveRun)
}

// identifyEffect waits for the identify call in fake.effects and returns it.
func identifyEffect(ctx context.Context, t *testing.T, tl *testLive, client *jrpc2.Client) fakeEffect {
	t.Helper()
	var found fakeEffect
	tl.waitUntil(t, "the identify call", func() bool {
		var got fakeEffects
		if err := client.CallResult(ctx, "fake.effects", nil, &got); err != nil {
			t.Fatal(err)
		}
		for _, e := range got.Effects {
			if e.Kind == "identify" {
				found = e
				return true
			}
		}
		return false
	})
	return found
}

// TestFakeIPCEffects: the fake gamepad answers devices.identify with the cues
// its capabilities allow and fake.effects shows what it was asked, the
// programs included, so that an E2E can see the buzz and the flash that no
// controller shows. clear takes the calls and forgets them.
func TestFakeIPCEffects(t *testing.T) {
	tl, client := startFakeEffects(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var cues haptic.Cues
	if err := client.CallResult(ctx, "devices.identify", map[string]string{"device": "1"}, &cues); err != nil {
		t.Fatal(err)
	}
	// A color LED is flashed; the player lights chase only without one.
	if want := (haptic.Cues{Rumble: true, LED: true}); cues != want {
		t.Fatalf("identify = %+v, want %+v", cues, want)
	}
	call := identifyEffect(ctx, t, tl, client)
	if call.Device != 1 || call.Cues == nil || !call.Cues.Rumble || !call.Cues.LED || call.Cues.Player {
		t.Fatalf("identify call %+v, want the fake gamepad with its cues", call)
	}
	// Three buzzes on both motors, 150 ms each, with 150 ms of rest between.
	if len(call.Rumble) != 5 {
		t.Fatalf("rumble program %+v, want 3 buzzes and 2 rests", call.Rumble)
	}
	for i, step := range call.Rumble {
		buzz := i%2 == 0
		if step.Ms != 150 || buzz != (step.Low > 0) || step.High != step.Low {
			t.Errorf("rumble step %d = %+v", i, step)
		}
	}
	// Three white flashes, 250 ms on and 250 ms off.
	if len(call.LED) != 5 {
		t.Fatalf("led program %+v, want 3 flashes and 2 pauses", call.LED)
	}
	for i, phase := range call.LED {
		want := ""
		if i%2 == 0 {
			want = "#ffffff"
		}
		if phase.Ms != 250 || phase.Color != want {
			t.Errorf("led phase %d = %+v, want color %q for 250 ms", i, phase, want)
		}
	}
	if len(call.Player) != 0 {
		t.Errorf("player program %+v for a gamepad with a color LED", call.Player)
	}

	// clear returns the calls and forgets them.
	var all fakeEffects
	if err := client.CallResult(ctx, "fake.effects", nil, &all); err != nil || len(all.Effects) == 0 {
		t.Fatalf("fake.effects = %+v, %v", all, err)
	}
	var cleared fakeEffects
	if err := client.CallResult(ctx, "fake.effects", map[string]bool{"clear": true}, &cleared); err != nil || len(cleared.Effects) < len(all.Effects) {
		t.Errorf("clear returned %d calls, want at least the %d read before", len(cleared.Effects), len(all.Effects))
	}
	var raw map[string]json.RawMessage
	if err := client.CallResult(ctx, "fake.effects", nil, &raw); err != nil || string(raw["effects"]) != "[]" {
		t.Errorf("after clear: %s, %v; want {\"effects\":[]}", raw, err)
	}
	// Only a bool clear.
	for _, params := range []any{map[string]string{"clear": "yes"}, map[string]any{"all": true}} {
		if err := client.CallResult(ctx, "fake.effects", params, new(any)); err == nil {
			t.Errorf("fake.effects accepted %v", params)
		}
	}
}

// TestFakeIPCEffectsWithoutCues: a fake gamepad that can only chase its
// player lights shows that program; one that can do nothing, or has a mono
// LED, answers all false and has no program.
func TestFakeIPCEffectsWithoutCues(t *testing.T) {
	for _, tc := range []struct {
		caps   string
		want   haptic.Cues
		player bool
	}{
		{"player", haptic.Cues{Player: true}, true},
		{"none", haptic.Cues{}, false},
		{"led", haptic.Cues{}, false}, // a mono LED gives no cue
	} {
		tl, client := startFakeEffects(t, tc.caps)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		var cues haptic.Cues
		if err := client.CallResult(ctx, "devices.identify", map[string]string{"device": "1"}, &cues); err != nil || cues != tc.want {
			t.Errorf("caps %q: identify = %+v, %v; want %+v", tc.caps, cues, err, tc.want)
		}
		call := identifyEffect(ctx, t, tl, client)
		if len(call.Rumble) != 0 || len(call.LED) != 0 {
			t.Errorf("caps %q: programs %+v %+v for cues %+v", tc.caps, call.Rumble, call.LED, tc.want)
		}
		// The chase visits the four lights in turn.
		if got := len(call.Player); tc.player != (got == 4) || !tc.player && got != 0 {
			t.Errorf("caps %q: player program %+v", tc.caps, call.Player)
		}
		cancel()
	}
}

// fake.effects needs the fake input like the other fake methods.
func TestFakeEffectsRejectedInHardwareMode(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{}}}`)
	tl, _ := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var rpcErr *jrpc2.Error
	if err := client.CallResult(ctx, "fake.effects", nil, new(any)); !errors.As(err, &rpcErr) || rpcErr.Code != -32010 {
		t.Fatalf("hardware mode answered fake.effects with %v, want -32010", err)
	}
}

// TestFakeIPCEffectsShowTheFeedbackSwitches (UI-27, HUD-08): fake.effects shows
// the ambient state the fake gamepad was given, with the mode of the layer the
// player lights show and the color; an E2E reads it to see what the switches of
// the settings dialog do, which no controller shows.
func TestFakeIPCEffectsShowTheFeedbackSwitches(t *testing.T) {
	tl, client := startFakeEffectsProfile(t, "", profileModes(""))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	feedbacks := func() []fakeEffect {
		var got fakeEffects
		if err := client.CallResult(ctx, "fake.effects", nil, &got); err != nil {
			t.Fatal(err)
		}
		var out []fakeEffect
		for _, e := range got.Effects {
			if e.Kind == "feedback" {
				out = append(out, e)
			}
		}
		return out
	}
	tl.waitUntil(t, "the first feedback", func() bool { return len(feedbacks()) >= 1 })
	if f := feedbacks()[0]; f.PlayerMode != 1 || f.Color != "" || f.Pulse {
		t.Errorf("first feedback = %+v, want the mode 1 of base", f)
	}
	for _, method := range []string{"fake.press", "fake.release"} {
		if err := client.CallResult(ctx, method, map[string]any{"control": "east"}, new(any)); err != nil {
			t.Fatal(err)
		}
	}
	tl.waitUntil(t, "the feedback of alt", func() bool { return len(feedbacks()) >= 2 })
	if f := feedbacks()[1]; f.PlayerMode != 2 || f.Color != "#112233" || !f.Pulse {
		t.Errorf("feedback of alt = %+v, want mode 2, its color and a pulse", f)
	}
}
