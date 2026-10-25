package main

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

func mustConfig(t *testing.T, text string) *profile.Config {
	t.Helper()
	c, err := profilelib.ValidateConfig([]byte(text))
	if err != nil {
		t.Fatalf("config %s: %v", text, err)
	}
	return c
}

func TestStandaloneProfileIdentityWithDataDir(t *testing.T) {
	opts := options{profile: basicProfile, library: profilelib.New(t.TempDir())}
	if got := opts.profileKey(); got != canonicalProfileKey(basicProfile) {
		t.Fatalf("standalone identity = %q", got)
	}
	opts.profileID = "paint"
	if got := opts.profileKey(); got != "paint" {
		t.Fatalf("library identity = %q", got)
	}
}

func TestReplayConfiguredCombo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	p, prog, _, ok := loadProfile(basicProfile, &stderr)
	if !ok {
		t.Fatal(stderr.String())
	}
	cfg, ds := profile.ParseConfig([]byte(`{"schemaVersion":0,"safety":{"panic":{"standard":{"buttons":["west","east"],"holdMs":500}}}}`))
	if ds.HasErrors() {
		t.Fatal(ds)
	}
	opts := options{profile: basicProfile, settings: cfg, dryRun: true}
	evs := []input.Event{
		{Device: 1, Kind: input.DeviceAdded},
		{Time: 1_000_000, Device: 1, Kind: input.ButtonDown, Control: "west"},
		{Time: 2_000_000, Device: 1, Kind: input.ButtonDown, Control: "east"},
		{Time: 600_000_000, Device: 1, Kind: input.ButtonUp, Control: "east"},
		{Time: 601_000_000, Device: 1, Kind: input.ButtonUp, Control: "west"},
	}
	if code := replayDryRun(context.Background(), opts, p, prog, device{id: 1}, evs, &stdout, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "pause") {
		t.Fatal("configured emergency combo did not pause replay:", stdout.String())
	}
	checkBalanced(t, stdout.String())
}

// TestEffectiveComboLackingButton: a family's combo applies to a gamepad
// that may have all its buttons. One known to lack a button of it keeps
// the built-in combo, at T-input (ComboFor) and in status.get alike.
func TestEffectiveComboLackingButton(t *testing.T) {
	settings, ds := profile.ParseConfig([]byte(`{"schemaVersion":0,"safety":{"panic":{"standard":{"buttons":["paddle1","paddle2"],"holdMs":1000}}}}`))
	if ds.HasErrors() {
		t.Fatal(ds)
	}
	const backStart, paddles = 1<<4 | 1<<6, 1<<16 | 1<<17 // SDL_GamepadButton bits
	known := input.Capabilities{Known: true}
	recorded := input.Combo{Buttons: []string{"paddle1", "paddle2"}, Hold: time.Second}
	builtIn := input.Combo{Buttons: []string{"back", "start"}, Hold: input.ComboHold}
	for _, tc := range []struct {
		name string
		info input.DeviceInfo
		want input.Combo
	}{
		{"has the paddles", input.DeviceInfo{Type: "xboxone", ButtonMask: backStart | paddles, Capabilities: known}, recorded},
		{"no paddles", input.DeviceInfo{Type: "xbox360", ButtonMask: backStart, Capabilities: known}, builtIn},
		{"controls unknown", input.DeviceInfo{Type: "xbox360"}, recorded},
	} {
		family, got := effectiveCombo(settings, tc.info, false)
		picked := configuredCombo(settings)(tc.info, false)
		for _, c := range []input.Combo{got, picked} {
			if family != "standard" || !slices.Equal(c.Buttons, tc.want.Buttons) || c.Hold != tc.want.Hold {
				t.Errorf("%s: %s %+v, want standard %+v", tc.name, family, c, tc.want)
			}
		}
	}
}

func TestLiveConfigDeviceFilterAndCombo(t *testing.T) {
	src, tl := newFakeSource(), &testLive{}
	cfg := testConfig(t, tl, "", nil, "--dry-run", "--watch=false")
	key := strings.Repeat("ab", 16)
	settings, ds := profile.ParseConfig([]byte(`{"schemaVersion":0,"devices":{"` + key + `":{"ignored":true}},"safety":{"panic":{"standard":{"buttons":["west","east"],"holdMs":3000},"joyconLeftVertical":{"buttons":["paddle2","paddle4"],"holdMs":1500}}}}`))
	if ds.HasErrors() {
		t.Fatal(ds)
	}
	cfg.opts.settings = settings
	cfg.open = func(o input.Options) (eventSource, error) {
		if o.ComboFor == nil {
			t.Fatal("configuration not passed to T-input")
		}
		got := o.ComboFor(input.DeviceInfo{Type: "xboxone"}, false)
		if !slices.Equal(got.Buttons, []string{"west", "east"}) || got.Hold != 3*time.Second {
			t.Fatal(got)
		}
		got = o.ComboFor(input.DeviceInfo{Type: "joyconleft"}, true)
		if !slices.Equal(got.Buttons, []string{"paddle2", "paddle4"}) || got.Hold != 1500*time.Millisecond {
			t.Fatal(got)
		}
		got = o.ComboFor(input.DeviceInfo{Type: "joyconright"}, false)
		if !slices.Equal(got.Buttons, []string{"leftshoulder", "rightshoulder"}) || got.Hold != input.ComboHold {
			t.Fatal(got)
		}
		return src, nil
	}
	tl.start(t, cfg)
	src.events <- input.Event{Kind: input.DeviceAdded, Device: 1, Info: &input.DeviceInfo{ID: 1, GUID: key, Name: liveTestPad, IsGamepad: true}}
	src.events <- input.Event{Kind: input.ButtonDown, Device: 1, Control: "east"}
	src.events <- input.Event{Kind: input.ButtonUp, Device: 1, Control: "east"}
	tl.sync()
	if strings.Contains(tl.stdout.String(), "send +Space") {
		t.Fatal("ignored pad produced output")
	}
	if !strings.Contains(tl.log.String(), "device connected") {
		t.Fatal("ignored pad disappeared from discovery")
	}
	src.events <- input.Event{Kind: input.DeviceAdded, Device: 2, Info: &input.DeviceInfo{ID: 2, GUID: strings.Repeat("ac", 16), Name: liveTestPad, IsGamepad: true}}
	src.events <- input.Event{Kind: input.ButtonDown, Device: 2, Control: "east"}
	tl.waitFor(t, tl.stdout, "send +Space\n")
	tl.cancel()
	if tl.waitCode(t) != 0 {
		t.Fatal(tl.stderr.String())
	}
	checkBalanced(t, tl.stdout.String())
}
