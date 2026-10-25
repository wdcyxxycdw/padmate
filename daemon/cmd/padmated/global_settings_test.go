package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// putConfig writes text as config.json through config.put, with the
// revision config.get gives, and returns the result.
func putConfig(t *testing.T, client *jrpc2.Client, text string) rpcSaved {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), liveWait)
	defer cancel()
	var doc rpcDocument
	if err := client.CallResult(ctx, "config.get", nil, &doc); err != nil {
		t.Fatal(err)
	}
	var saved rpcSaved
	if err := client.CallResult(ctx, "config.put", map[string]string{"revision": doc.Revision, "text": text}, &saved); err != nil {
		t.Fatal(err)
	}
	return saved
}

// timing returns the resolved timing of the profile that runs.
func (tl *testLive) timing() profile.Timing {
	tl.profileMu.Lock()
	defer tl.profileMu.Unlock()
	return tl.cfg.prog.Timing()
}

// TestConfigPutAppliesTimingLive: config.json's timing applies on
// config.put without a restart (UI-19, ADR-0051). The running profile is
// compiled again and reloaded, which releases the keys it holds
// (invariant 1); a profile whose timing does not change keeps running, and
// the next profile activated uses the new defaults.
func TestConfigPutAppliesTimingLive(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "first", `{"schemaVersion":0,"name":"first","layers":{"base":{
		"west":{"tap":{"type":"keys","keys":"F13"},"hold":{"type":"keys","keys":"F14"}},
		"east":{"press":{"type":"keys","keys":"Space"}}}}}`)
	writeLibraryProfile(t, dir, "second", `{"schemaVersion":0,"name":"second","timing":{"holdMs":40},"layers":{"base":{}}}`)
	tl, src := startFocusLive(t, dir, "first", `{"schemaVersion":0,"timing":{"holdMs":60000}}`, false)
	client := rpcTestClient(t, tl.liveRun)
	if got := tl.timing().HoldMs; got != 60000 {
		t.Fatalf("startup holdMs %d", got)
	}
	// A long press is a tap while the hold takes a minute.
	button(src, "west", true)
	tl.sync()
	time.Sleep(100 * time.Millisecond)
	button(src, "west", false)
	tl.waitFor(t, tl.stdout, "send -F13")

	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space")
	gen := tl.profileTarget.Load().gen
	saved := putConfig(t, client, `{"schemaVersion":0,"timing":{"holdMs":30}}`)
	if !saved.Applied || saved.RestartRequired || saved.Problem != "" {
		t.Fatalf("timing not applied live: %+v", saved)
	}
	// The reload released the key east held, before config.put returned.
	if !strings.Contains(tl.stdout.String(), "send -Space") {
		t.Fatalf("held key not released by the timing change:\n%s", tl.stdout)
	}
	if got := tl.profileTarget.Load(); got.gen == gen || got.id != "first" {
		t.Fatalf("profile not reloaded: %+v, was generation %d", got, gen)
	}
	if got := tl.timing().HoldMs; got != 30 {
		t.Fatalf("holdMs %d after config.put", got)
	}
	button(src, "east", false)
	// The same long press is a hold now.
	button(src, "west", true)
	tl.waitFor(t, tl.stdout, "send +F14")
	button(src, "west", false)
	tl.waitFor(t, tl.stdout, "send -F14")

	// Another field: the timing, and the profile, stay.
	gen = tl.profileTarget.Load().gen
	if saved := putConfig(t, client, `{"schemaVersion":0,"timing":{"holdMs":30},"output":{"sendToOwnWindow":false}}`); !saved.Applied {
		t.Fatalf("%+v", saved)
	}
	if got := tl.profileTarget.Load().gen; got != gen {
		t.Fatalf("an unchanged timing reloaded the profile: generation %d, was %d", got, gen)
	}

	// second sets holdMs itself: a new global holdMs changes nothing.
	ctx, cancel := context.WithTimeout(context.Background(), liveWait)
	defer cancel()
	if err := client.CallResult(ctx, "profiles.activate", map[string]string{"id": "second"}, new(any)); err != nil {
		t.Fatal(err)
	}
	gen = tl.profileTarget.Load().gen
	if saved := putConfig(t, client, `{"schemaVersion":0,"timing":{"holdMs":500}}`); !saved.Applied {
		t.Fatalf("%+v", saved)
	}
	if got := tl.profileTarget.Load().gen; got != gen {
		t.Fatalf("a profile that keeps its timing was reloaded: generation %d, was %d", got, gen)
	}
	// multiTapMs comes from the config, and changed: second reloads.
	if saved := putConfig(t, client, `{"schemaVersion":0,"timing":{"holdMs":500,"multiTapMs":310}}`); !saved.Applied {
		t.Fatalf("%+v", saved)
	}
	if got := tl.timing(); got.HoldMs != 40 || got.MultiTapMs != 310 || tl.profileTarget.Load().gen == gen {
		t.Fatalf("second's timing %+v, generation %d", got, tl.profileTarget.Load().gen)
	}
	// The next profile activated gets the new defaults.
	if err := client.CallResult(ctx, "profiles.activate", map[string]string{"id": "first"}, new(any)); err != nil {
		t.Fatal(err)
	}
	if got := tl.timing(); got.HoldMs != 500 || got.MultiTapMs != 310 || got.TapPressMs != 20 {
		t.Fatalf("first activated with %+v", got)
	}
	tl.cancel()
	if tl.waitCode(t) != 0 {
		t.Fatal(tl.stderr.String())
	}
	checkBalanced(t, tl.stdout.String())
}

// TestReapplyTimingRereadsStartupProfile: the profile that startLive
// started with is read again, and must still have the sum that runs.
func TestReapplyTimingRereadsStartupProfile(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "first", `{"schemaVersion":0,"name":"first","layers":{"base":{}}}`)
	tl, _ := startFocusLive(t, dir, "first", `{"schemaVersion":0}`, false)
	tl.profileMu.Lock()
	if tl.running != nil {
		tl.profileMu.Unlock()
		t.Fatal("startLive kept running bytes")
	}
	data, err := tl.runningDataLocked(tl.targetLocked())
	tl.profileMu.Unlock()
	if err != nil || !strings.Contains(string(data), `"first"`) {
		t.Fatalf("%q %v", data, err)
	}
	// Changed on disk without a reload (--watch=false): not applied.
	writeLibraryProfile(t, dir, "first", `{"schemaVersion":0,"name":"changed","layers":{"base":{}}}`)
	client := rpcTestClient(t, tl.liveRun)
	saved := putConfig(t, client, `{"schemaVersion":0,"timing":{"holdMs":400}}`)
	if saved.Applied || !strings.Contains(saved.Problem, "global timing not applied") || saved.RestartRequired {
		t.Fatalf("%+v", saved)
	}
	if got := tl.timing().HoldMs; got != 250 {
		t.Fatalf("holdMs %d", got)
	}
}

// comboFake is a fake source with the comboSource methods.
type comboFake struct {
	*fakeSource
	vertical bool
	mu       sync.Mutex
	repicks  int
}

func (f *comboFake) RepickCombos() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repicks++
	return nil
}

func (f *comboFake) VerticalJoyCons() bool { return f.vertical }

func (f *comboFake) repicked() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.repicks
}

// TestStatusCombosAndRepick: status.get gives each device the combo
// T-input detects, by family and grip, and a safety change re-picks the
// combos on T-input at once; another change does not.
func TestStatusCombosAndRepick(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "first", `{"schemaVersion":0,"name":"first","layers":{"base":{}}}`)
	src := &comboFake{fakeSource: newFakeSource(), vertical: true}
	tl := startLibraryLiveOpen(t, `{"schemaVersion":0,"safety":{"panic":{"joyconLeftVertical":{"buttons":["paddle2","south"],"holdMs":1500}}}}`,
		options{dataDir: dir, dryRun: true}, func(input.Options) (eventSource, error) { return src, nil })
	for i, typ := range []string{"xboxone", "joyconleft", "joyconright"} {
		id := input.DeviceID(10 + i)
		src.events <- input.Event{Kind: input.DeviceAdded, Device: id, Info: &input.DeviceInfo{ID: id, GUID: strings.Repeat("e"+string(rune('0'+i)), 16), Name: typ, Type: typ, IsGamepad: true}}
	}
	tl.sync()
	client := rpcTestClient(t, tl.liveRun)
	status := func() map[input.DeviceID]rpcCombo {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), liveWait)
		defer cancel()
		var st rpcStatus
		if err := client.CallResult(ctx, "status.get", nil, &st); err != nil {
			t.Fatal(err)
		}
		if len(st.Combos) != len(st.Devices) {
			t.Fatalf("%d combos for %d devices", len(st.Combos), len(st.Devices))
		}
		m := map[input.DeviceID]rpcCombo{}
		for _, c := range st.Combos {
			m[c.Device] = c
		}
		return m
	}
	check := func(got rpcCombo, family string, holdMs int64, buttons ...string) {
		t.Helper()
		if got.Family != family || got.HoldMs != holdMs || !slices.Equal(got.Buttons, buttons) {
			t.Errorf("combo %+v, want %s %v %d", got, family, buttons, holdMs)
		}
	}
	combos := status()
	check(combos[10], "standard", 2000, "back", "start")
	check(combos[11], "joyconLeftVertical", 1500, "paddle2", "south")
	check(combos[12], "joyconRightVertical", 2000, "paddle3", "paddle1")
	if n := src.repicked(); n != 0 {
		t.Fatalf("%d repicks at startup", n)
	}

	if saved := putConfig(t, client, `{"schemaVersion":0,"timing":{"holdMs":300},"safety":{"panic":{"joyconLeftVertical":{"buttons":["paddle2","south"],"holdMs":1500}}}}`); !saved.Applied {
		t.Fatalf("%+v", saved)
	}
	if n := src.repicked(); n != 0 {
		t.Fatalf("a timing change re-picked the combos %d times", n)
	}
	saved := putConfig(t, client, `{"schemaVersion":0,"timing":{"holdMs":300},"safety":{"panic":{"standard":{"buttons":["west","east","north"],"holdMs":1000}}}}`)
	if !saved.Applied || saved.RestartRequired || saved.Problem != "" {
		t.Fatalf("combo not applied live: %+v", saved)
	}
	if n := src.repicked(); n != 1 {
		t.Fatalf("%d repicks after a safety change", n)
	}
	combos = status()
	check(combos[10], "standard", 1000, "west", "east", "north")
	check(combos[11], "joyconLeftVertical", 2000, "paddle2", "paddle4")
}

// TestConfigPutAppliesComboLive: with fake IPC input, whose source picks
// the combos as T-input does, a new safety.panic combo pauses the mapping
// without a restart, and the old combo no longer does.
func TestConfigPutAppliesComboLive(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "first", `{"schemaVersion":0,"name":"first","layers":{"base":{}}}`)
	tl := startLibraryLiveOpen(t, `{"schemaVersion":0,"safety":{"panic":{"standard":{"buttons":["back","start"],"holdMs":500}}}}`,
		options{dataDir: dir, dryRun: true, fakeInput: "ipc"}, nil)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), liveWait)
	defer cancel()
	call := func(method, control string) {
		t.Helper()
		if err := client.CallResult(ctx, method, map[string]string{"control": control}, new(any)); err != nil {
			t.Fatal(method, control, err)
		}
	}
	paused := func() bool { return tl.sup.Status().Paused }
	// The old combo pauses, and resumes.
	call("fake.press", "back")
	call("fake.press", "start")
	tl.waitUntil(t, "the old combo's pause", paused)
	call("fake.release", "start")
	call("fake.press", "start")
	tl.waitUntil(t, "the old combo's resume", func() bool { return !paused() })
	// Held through the change, it fires no more.
	saved := putConfig(t, client, `{"schemaVersion":0,"safety":{"panic":{"standard":{"buttons":["west","east"],"holdMs":500}}}}`)
	if !saved.Applied || saved.RestartRequired {
		t.Fatalf("%+v", saved)
	}
	call("fake.release", "start")
	call("fake.press", "start")
	time.Sleep(800 * time.Millisecond)
	if paused() {
		t.Fatal("the old combo paused after the change")
	}
	call("fake.press", "west")
	call("fake.press", "east")
	tl.waitUntil(t, "the new combo's pause", paused)
	var st rpcStatus
	if err := client.CallResult(ctx, "status.get", nil, &st); err != nil || len(st.Combos) != 1 {
		t.Fatal(st.Combos, err)
	}
	if c := st.Combos[0]; c.Family != "standard" || !slices.Equal(c.Buttons, []string{"west", "east"}) || c.HoldMs != 500 {
		t.Fatalf("%+v", c)
	}
}

// startLibraryLiveOpen is startLibraryLive with open as the input (nil
// keeps the one opts selects) and no device added.
func startLibraryLiveOpen(t *testing.T, config string, opts options, open func(input.Options) (eventSource, error)) *testLive {
	t.Helper()
	if err := os.WriteFile(filepath.Join(opts.dataDir, "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	tl := &testLive{}
	cfg := testConfig(t, tl, "", nil, "--dry-run", "--watch=false")
	if err := resolveLibrary(&opts); err != nil {
		t.Fatal(err)
	}
	cfg.opts = opts
	var ok bool
	cfg.profile, cfg.prog, cfg.sum, ok = loadSelectedProfile(opts, tl.stderr)
	if !ok {
		t.Fatal(tl.stderr.String())
	}
	cfg.open = open
	tl.start(t, cfg)
	return tl
}

// TestConfigPutAppliesFeedbackSwitchesLive (UI-27, ADR-0067): config.json's
// feedback switches apply on config.put without a restart and without
// reloading the profile. The lights switch off gives the lights back to the
// controller at once; the same file read by config.get does not ask for a
// restart; and a profile that still holds the retired members does nothing.
func TestConfigPutAppliesFeedbackSwitchesLive(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "first", profileModes(`{"led": false, "rumble": false}`))
	tl, src := startFocusLive(t, dir, "first", `{"schemaVersion":0}`, false)
	client := rpcTestClient(t, tl.liveRun)
	const dev = input.DeviceID(1)
	button(src, "east", true)
	button(src, "east", false)
	tl.waitUntil(t, "alt, with its light and pulses", func() bool {
		calls := feedbackCalls(src, dev)
		return len(calls) > 0 && calls[len(calls)-1].PlayerMode == 2
	})
	if p := pulsesOf(feedbackCalls(src, dev)); len(p) != 1 {
		t.Fatalf("pulses = %+v; the retired members of the profile must not silence them", p)
	}
	gen := tl.profileTarget.Load().gen
	before := len(feedbackCalls(src, dev))
	saved := putConfig(t, client, `{"schemaVersion":0,"feedback":{"led":false}}`)
	if !saved.Applied || saved.RestartRequired || saved.Problem != "" {
		t.Fatalf("the switch was not applied live: %+v", saved)
	}
	tl.waitUntil(t, "the feedback of the change", func() bool { return len(feedbackCalls(src, dev)) > before })
	calls := feedbackCalls(src, dev)
	if f := calls[len(calls)-1]; f.PlayerMode != 0 || f.Color != "" || f.Pulse {
		t.Errorf("feedback after led went off = %+v, want the lights released and no pulse", f)
	}
	if got := tl.profileTarget.Load().gen; got != gen {
		t.Errorf("the switch reloaded the profile: generation %d, was %d", got, gen)
	}
	ctx, cancel := context.WithTimeout(context.Background(), liveWait)
	defer cancel()
	var doc rpcDocument
	if err := client.CallResult(ctx, "config.get", nil, &doc); err != nil || doc.RestartRequired || doc.Problem != "" {
		t.Errorf("config.get = %+v, %v; want no restart", doc, err)
	}
	// Back on, and nothing else in the file changes what runs.
	before = len(calls)
	if saved := putConfig(t, client, `{"schemaVersion":0}`); !saved.Applied || saved.RestartRequired {
		t.Fatalf("%+v", saved)
	}
	tl.waitUntil(t, "the feedback of the change back", func() bool { return len(feedbackCalls(src, dev)) > before })
	calls = feedbackCalls(src, dev)
	if f := calls[len(calls)-1]; f.PlayerMode != 2 || f.Color != "#112233" {
		t.Errorf("feedback after led came back = %+v, want the mode and color of alt", f)
	}
}
