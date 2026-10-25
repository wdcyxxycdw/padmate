package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winfocus"
)

// startSelectionLive is startFocusLive with first and second in the
// library, first selected by state.json, and change applied to the config.
func startSelectionLive(t *testing.T, config string, change func(*liveConfig)) (*testLive, *fakeSource, string) {
	t.Helper()
	return startSelectionLiveWith(t, config, func(id string) string {
		return `{"schemaVersion":0,"name":"` + id + `","layers":{"base":{"east":{"press":{"type":"keys","keys":"Space"}}}}}`
	}, change)
}

// startSelectionLiveWith is startSelectionLive with the text of each
// profile, first and second, chosen by the test.
func startSelectionLiveWith(t *testing.T, config string, profileText func(id string) string, change func(*liveConfig)) (*testLive, *fakeSource, string) {
	t.Helper()
	dir := t.TempDir()
	for _, id := range []string{"first", "second"} {
		writeLibraryProfile(t, dir, id, profileText(id))
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"schemaVersion":0,"lastProfile":"first","lastProfileSet":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	src, tl := newFakeSource(), &testLive{}
	cfg := testConfig(t, tl, "", nil, "--dry-run", "--watch=false")
	opts := options{dataDir: dir, dryRun: true}
	if err := resolveLibrary(&opts); err != nil {
		t.Fatal(err)
	}
	cfg.opts = opts
	var ok bool
	cfg.profile, cfg.prog, cfg.sum, ok = loadSelectedProfile(opts, tl.stderr)
	if !ok {
		t.Fatal(tl.stderr.String())
	}
	cfg.open = func(input.Options) (eventSource, error) { return src, nil }
	// The UI seams never consult the real foreground or processes.
	cfg.ownForeground = func(uint32, string) bool { return false }
	cfg.ownAlive = func(uint32, string, uint64) bool { return false }
	if change != nil {
		change(&cfg)
	}
	tl.start(t, cfg)
	src.events <- input.Event{Kind: input.DeviceAdded, Device: 1, Info: &input.DeviceInfo{ID: 1, GUID: strings.Repeat("d2", 16), Name: liveTestPad, IsGamepad: true}}
	tl.sync()
	return tl, src, dir
}

// ownUISeams make PID 4242 a verified padmate UI while alive holds.
func ownUISeams(alive *atomic.Bool) func(*liveConfig) {
	return func(cfg *liveConfig) {
		cfg.verifyUI = func(uint32, uint32) (string, error) { return `C:\padmate\padmate.exe`, nil }
		cfg.ownInstance = func(uint32) (uint64, error) { return 7, nil }
		cfg.ownAlive = func(uint32, string, uint64) bool { return alive.Load() }
	}
}

var ownUIApp = winfocus.App{Window: 2, PID: 4242, Exe: "padmate.exe", Path: `C:\padmate\padmate.exe`}

func registerOwnUI(t *testing.T, tl *testLive) {
	t.Helper()
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.CallResult(ctx, "ui.register", map[string]uint32{"pid": ownUIApp.PID}, new(any)); err != nil {
		t.Fatal(err)
	}
}

// foreground reports app as the foreground and waits for its evaluation.
func (tl *testLive) foregroundTo(t *testing.T, app winfocus.App) {
	t.Helper()
	before := strings.Count(tl.log.String(), `"foreground evaluated","exe":"`+app.Exe+`","switched":true`)
	tl.onForeground(app)
	tl.waitForCount(t, tl.log, `"foreground evaluated","exe":"`+app.Exe+`","switched":true`, before+1)
}

func (tl *testLive) waitProfile(t *testing.T, id string) {
	t.Helper()
	tl.waitUntil(t, "profile "+id, func() bool { return tl.profileTarget.Load().id == id })
}

func TestConfigPutAppliesSelectionLive(t *testing.T) {
	tl, src, _ := startSelectionLive(t, `{"schemaVersion":0,"rules":[{"exe":"game.exe","profile":"first"}]}`, nil)
	events := make(chan map[string]any, 256)
	client := rpcTestClient(t, tl.liveRun, &jrpc2.ClientOptions{OnNotify: func(r *jrpc2.Request) {
		var data map[string]any
		if r.Method() == "profile.changed" && r.UnmarshalParams(&data) == nil {
			select {
			case events <- data:
			default:
			}
		}
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.CallResult(ctx, "subscribe", map[string]any{"topics": []string{"profile.changed"}}, new(any)); err != nil {
		t.Fatal(err)
	}
	tl.foregroundTo(t, focusApp("game.exe"))
	tl.waitProfile(t, "first")
	reevaluations := func() int {
		return strings.Count(tl.log.String(), `"foreground evaluated","exe":"game.exe","switched":false`)
	}
	put := func(text string) {
		t.Helper()
		var doc rpcDocument
		if err := client.CallResult(ctx, "config.get", nil, &doc); err != nil {
			t.Fatal(err)
		}
		before := reevaluations()
		var saved rpcSaved
		if err := client.CallResult(ctx, "config.put", map[string]string{"revision": doc.Revision, "text": text}, &saved); err != nil {
			t.Fatal(err)
		}
		if !saved.Applied || saved.RestartRequired || saved.Problem != "" {
			t.Fatalf("selection settings not applied live: %+v", saved)
		}
		if err := client.CallResult(ctx, "config.get", nil, &doc); err != nil || doc.RestartRequired || doc.Revision != saved.Revision {
			t.Fatal("config.get disagrees", doc, err)
		}
		// The current foreground is evaluated again without a new focus event.
		tl.waitUntil(t, "re-evaluation", func() bool { return reevaluations() > before })
		for {
			select {
			case e := <-events:
				if e["kind"] == "config" && e["revision"] == saved.Revision {
					return
				}
			case <-ctx.Done():
				t.Fatal("no profile.changed config event")
			}
		}
	}
	stays := func(id string) {
		t.Helper()
		tl.sync()
		if got := tl.profileTarget.Load().id; got != id {
			t.Fatalf("selected %q, want %q", got, id)
		}
	}

	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space")
	put(`{"schemaVersion":0,"rules":[{"exe":"game.exe","profile":"second"}]}`)
	tl.waitProfile(t, "second")
	tl.waitFor(t, tl.stdout, "send -Space")
	button(src, "east", false)

	// No rule binds game.exe any more: the fixed default (ADR-0053).
	put(`{"schemaVersion":0,"rules":[{"exe":"paint.exe","profile":"second"}]}`)
	tl.waitProfile(t, profilelib.DefaultID)
	// A null rule an older file kept binds nothing.
	put(`{"schemaVersion":0,"rules":[{"exe":"game.exe","profile":null}]}`)
	stays(profilelib.DefaultID)
	put(`{"schemaVersion":0,"rules":[{"exe":"game.exe","profile":"first"}]}`)
	tl.waitProfile(t, "first")

	// Automatic selection off keeps the profile.
	put(`{"schemaVersion":0,"enabled":false,"rules":[{"exe":"game.exe","profile":"second"}]}`)
	stays("first")
	// The retired ignore list no longer keeps the profile.
	put(`{"schemaVersion":0,"ignore":["game.exe"],"onUnmatched":"keep","defaultProfile":"first","rules":[{"exe":"game.exe","profile":"second"}]}`)
	tl.waitProfile(t, "second")
	if c := tl.selectionConfig(); !c.Enabled || len(c.Rules) != 1 || *c.Rules[0].Profile != "second" || *tl.cfg.opts.settings.Rules[0].Profile != "first" {
		t.Fatalf("live %+v startup %+v", c, tl.cfg.opts.settings)
	}

	// The next foreground change uses the same live settings.
	tl.foregroundTo(t, focusApp("other.exe"))
	tl.waitProfile(t, profilelib.DefaultID)
	tl.cancel()
	if tl.waitCode(t) != 0 {
		t.Fatal(tl.stderr.String())
	}
	checkBalanced(t, tl.stdout.String())
}

func TestConfigReevaluationIsNotAForegroundSwitch(t *testing.T) {
	tl, src, dir := startSelectionLive(t, `{"schemaVersion":0,"releaseOnSwitch":"always","rules":[{"exe":"game.exe","profile":"first"}]}`, nil)
	tl.foregroundTo(t, focusApp("game.exe"))
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space")
	l := profilelib.New(dir)
	_, revision, err := l.ConfigText()
	if err != nil {
		t.Fatal(err)
	}
	text := []byte(`{"schemaVersion":0,"releaseOnSwitch":"always","rules":[{"exe":"game.exe","profile":"first"},{"exe":"other.exe","profile":"second"}]}`)
	c := mustConfig(t, string(text))
	tl.configApplyMu.Lock()
	_, err = tl.commitConfigLocked(tl.cfg.opts.library, revision, text, c)
	tl.configApplyMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	tl.waitFor(t, tl.log, `"foreground evaluated","exe":"game.exe","switched":false`)
	tl.sync()
	if strings.Contains(tl.stdout.String(), "-Space") {
		t.Fatal("a config change released a profile that stays")
	}
	// A real foreground change still releases with "always".
	tl.foregroundTo(t, focusApp("game.exe"))
	tl.waitFor(t, tl.stdout, "send -Space")
	button(src, "east", false)
}

func TestConfigRestartRequiredOnlyForStartupFields(t *testing.T) {
	base := `"schemaVersion":0,"timing":{"holdMs":300},"devices":{"` + strings.Repeat("ab", 16) + `":{"ignored":false}}`
	r := &liveRun{cfg: liveConfig{opts: options{settings: mustConfig(t, "{"+base+"}")}}}
	for _, tc := range []struct {
		extra   string
		restart bool
	}{
		{``, false},
		{`,"enabled":false`, false},
		{`,"releaseOnSwitch":"always"`, false},
		{`,"rules":[{"exe":"game.exe","profile":"first"},{"exe":"paint.exe","profile":"second","enabled":false}]`, false},
		// Members ADR-0053 retired change nothing.
		{`,"onUnmatched":"none","defaultProfile":"first","ignore":["explorer.exe"]`, false},
		{`,"rules":[{"exe":"game.exe","profile":null}]`, false},
		{`,"output":{"sendToOwnWindow":true}`, false},
		{`,"output":{"eventGapMs":10}`, false},
		{`,"restoreLayerState":false`, true},
		{`,"safety":{"panic":{"standard":{"buttons":["back","start"],"holdMs":3000}}}`, false},
	} {
		c := mustConfig(t, "{"+base+tc.extra+"}")
		if got := r.configNeedsRestart(c); got != tc.restart {
			t.Errorf("%s: restart %v, want %v", tc.extra, got, tc.restart)
		}
	}
	// Changes to startup-captured fields outside the shared base: which
	// devices are ignored.
	key := strings.Repeat("ab", 16)
	for _, text := range []string{
		`{"schemaVersion":0,"timing":{"holdMs":300},"devices":{"` + key + `":{"ignored":true}}}`,
		`{"schemaVersion":0,"timing":{"holdMs":300},"devices":{"` + key + `":{"ignored":false},"` + strings.Repeat("cd", 16) + `":{"ignored":true}}}`,
	} {
		if !r.configNeedsRestart(mustConfig(t, text)) {
			t.Errorf("%s: no restart", text)
		}
	}
	// A device entry that does not ignore its device is the same as none, and
	// the stick calibration of a device applies at once (IN-14).
	for _, text := range []string{
		`{"schemaVersion":0,"timing":{"holdMs":300}}`,
		`{"schemaVersion":0,"timing":{"holdMs":300},"devices":{"` + key + `":{"calibration":{"ls":{"deadzone":0.2}}}}}`,
		`{"schemaVersion":0,"timing":{"holdMs":300},"devices":{"` + key + `":{"ignored":false,"calibration":{"rs":{"saturation":0.8}}},"` + strings.Repeat("cd", 16) + `":{"calibration":{"ls":{"deadzone":0.1}}}}}`,
	} {
		if r.configNeedsRestart(mustConfig(t, text)) {
			t.Errorf("%s: restart", text)
		}
	}
	ignoredStartup := &liveRun{cfg: liveConfig{opts: options{settings: mustConfig(t, `{"schemaVersion":0,"devices":{"`+key+`":{"ignored":true,"calibration":{"ls":{"deadzone":0.2}}}}}`)}}}
	if ignoredStartup.configNeedsRestart(mustConfig(t, `{"schemaVersion":0,"devices":{"`+key+`":{"ignored":true,"calibration":{"ls":{"deadzone":0.3}}}}}`)) {
		t.Error("a changed calibration of an ignored device needs a restart")
	}
	if !ignoredStartup.configNeedsRestart(mustConfig(t, `{"schemaVersion":0,"devices":{"`+key+`":{"calibration":{"ls":{"deadzone":0.2}}}}}`)) {
		t.Error("un-ignoring a device needs no restart")
	}
	// Timing applies at once (UI-19).
	if text := `{"schemaVersion":0,"timing":{"holdMs":400},"devices":{"` + strings.Repeat("ab", 16) + `":{"ignored":false}}}`; r.configNeedsRestart(mustConfig(t, text)) {
		t.Errorf("%s: restart", text)
	}
	// liveSettings applies exactly the live fields over startup.
	next := mustConfig(t, `{"schemaVersion":0,"enabled":false,"releaseOnSwitch":"never","rules":[{"exe":"b.exe","profile":"second"}],"restoreLayerState":false,"timing":{"holdMs":900},"output":{"sendToOwnWindow":true,"eventGapMs":10}}`)
	got := liveSettings(r.cfg.opts.settings, next)
	if got.Enabled || got.ReleaseOnSwitch != "never" || len(got.Rules) != 1 || !got.Output.SendToOwnWindow || got.Output.EventGapMs != 10 {
		t.Fatalf("live fields not applied: %+v", got)
	}
	if !got.RestoreLayerState || got.Timing.HoldMs != 900 || len(got.Devices) != 1 {
		t.Fatalf("startup fields replaced: %+v", got)
	}
}

// TestLiveSettingsCalibration: the calibration of a device is next's, its
// ignored flag startup's (IN-14).
func TestLiveSettingsCalibration(t *testing.T) {
	a, b, c := strings.Repeat("aa", 16), strings.Repeat("bb", 16), strings.Repeat("cc", 16)
	startup := mustConfig(t, `{"schemaVersion":0,"devices":{"`+a+`":{"ignored":true,"calibration":{"ls":{"deadzone":0.1}}},"`+b+`":{"ignored":false}}}`)
	next := mustConfig(t, `{"schemaVersion":0,"devices":{"`+a+`":{"ignored":false,"calibration":{"rs":{"saturation":0.8}}},"`+c+`":{"ignored":true,"calibration":{"ls":{"deadzone":0.3}}},"`+b+`":{"calibration":{}}}}`)
	got := liveSettings(startup, next)
	if d := got.Devices[a]; !d.Ignored || d.Calibration == nil || d.Calibration.LS != nil || d.Calibration.RS == nil || *d.Calibration.RS.Saturation != 0.8 {
		t.Errorf("a: %+v", d)
	}
	if d := got.Devices[b]; d.Ignored || d.Calibration != nil {
		t.Errorf("b: %+v", d)
	}
	if d, ok := got.Devices[c]; !ok || d.Ignored || d.Calibration == nil || *d.Calibration.LS.Deadzone != 0.3 {
		t.Errorf("c: %+v", d)
	}
	if startup.Devices[a].Calibration.LS == nil || startup.Devices[a].Calibration.RS != nil {
		t.Error("startup changed")
	}
	r := &liveRun{}
	r.selection.Store(got)
	if cal := r.deviceCalibration(c); cal == nil || *cal.LS.Deadzone != 0.3 {
		t.Errorf("deviceCalibration(c) = %+v", cal)
	}
	if r.deviceCalibration(b) != nil || r.deviceCalibration("none") != nil {
		t.Error("a device without a calibration got one")
	}
}

// calibratedProfile has a left stick deadzone of 0.4 and ls.right held as
// Space.
func calibratedProfile(id string) string {
	return `{"schemaVersion":0,"name":"` + id + `","analog":{"ls":{"deadzone":0.4,"saturation":0.95}},"layers":{"base":{"ls.right":{"press":{"type":"keys","keys":"Space","mode":"hold"}}}}}`
}

func stick(src *fakeSource, x float64) {
	src.events <- input.Event{Device: 1, Kind: input.AxisMotion, Control: "lefty", Value: 0}
	src.events <- input.Event{Device: 1, Kind: input.AxisMotion, Control: "leftx", Value: int16(math.Round(x * 32767))}
}

// TestConfigPutAppliesCalibrationLive: a config.put with the calibration of
// the connected controller applies at once, without a restart, to the stick
// that is held, and presses nothing by itself (IN-14).
func TestConfigPutAppliesCalibrationLive(t *testing.T) {
	tl, src, _ := startSelectionLiveWith(t, `{"schemaVersion":0}`, calibratedProfile, nil)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	key := strings.Repeat("d2", 16) // the pad startSelectionLive connects
	put := func(text string) rpcSaved {
		t.Helper()
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
	// The profile's deadzone, 0.4: 0.45 is no direction.
	stick(src, 0.45)
	tl.sync()
	if strings.Contains(tl.stdout.String(), "send +Space") {
		t.Fatal("mapped inside the profile's deadzone")
	}
	saved := put(`{"schemaVersion":0,"devices":{"` + key + `":{"calibration":{"ls":{"deadzone":0.1}}}}}`)
	if !saved.Applied || saved.RestartRequired || saved.Problem != "" {
		t.Fatalf("calibration not applied live: %+v", saved)
	}
	tl.waitFor(t, tl.log, `"stick calibration applied"`)
	tl.sync()
	if strings.Contains(tl.stdout.String(), "send +Space") {
		t.Fatal("a calibration change pressed a key")
	}
	stick(src, 0)
	stick(src, 0.45)
	tl.waitFor(t, tl.stdout, "send +Space")
	// A deadzone above the stick lets go of the key at once.
	saved = put(`{"schemaVersion":0,"devices":{"` + key + `":{"calibration":{"ls":{"deadzone":0.5}}}}}`)
	if !saved.Applied || saved.RestartRequired {
		t.Fatalf("second calibration: %+v", saved)
	}
	tl.waitFor(t, tl.stdout, "send -Space")
	// Ignoring the device still needs the restart.
	saved = put(`{"schemaVersion":0,"devices":{"` + key + `":{"ignored":true,"calibration":{"ls":{"deadzone":0.5}}}}}`)
	if !saved.RestartRequired {
		t.Fatalf("ignored: %+v", saved)
	}
	stick(src, 0)
	tl.cancel()
	if tl.waitCode(t) != 0 {
		t.Fatal(tl.stderr.String())
	}
	checkBalanced(t, tl.stdout.String())
}

func TestOwnUIForegroundKeepsProfile(t *testing.T) {
	var alive atomic.Bool
	alive.Store(true)
	tl, src, _ := startSelectionLive(t, `{"schemaVersion":0,"releaseOnSwitch":"always","rules":[{"exe":"paint.exe","profile":"first"}]}`, ownUISeams(&alive))
	registerOwnUI(t, tl)
	tl.foregroundTo(t, focusApp("paint.exe"))
	tl.waitProfile(t, "first")
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space")
	if !tl.isOwnUI(&ownUIApp) || tl.isOwnUI(&winfocus.App{PID: 4243, Exe: "padmate.exe"}) {
		t.Fatal("own UI identity")
	}
	// The padmate window in front, which no rule binds, neither switches
	// to the default nor releases (always).
	tl.foregroundTo(t, ownUIApp)
	tl.sync()
	if id := tl.profileTarget.Load().id; id != "first" || strings.Contains(tl.stdout.String(), "-Space") {
		t.Fatalf("own UI changed selection: %q\n%s", id, tl.stdout.String())
	}
	// A UI process that has exited is an ordinary program again.
	alive.Store(false)
	tl.foregroundTo(t, ownUIApp)
	tl.waitProfile(t, profilelib.DefaultID)
	tl.waitFor(t, tl.stdout, "send -Space")
	button(src, "east", false)
}

// TestDaemonWindowIsNoForeground: opening the tray menu makes padmated's
// hidden window the foreground. It is no program, so the program before
// it stays the foreground and keeps its profile.
func TestDaemonWindowIsNoForeground(t *testing.T) {
	tl, _, _ := startSelectionLive(t, `{"schemaVersion":0,"rules":[{"exe":"game.exe","profile":"second"}]}`, nil)
	tl.foregroundTo(t, focusApp("game.exe"))
	tl.waitProfile(t, "second")
	tl.onForeground(winfocus.App{Window: 5, Class: "SystrayClass", PID: daemonPID, Exe: "padmated.exe", Path: `C:\padmate\padmated.exe`})
	tl.sync()
	if app := tl.foreground.Load(); app == nil || app.Exe != "game.exe" {
		t.Fatalf("daemon window became the foreground: %+v", app)
	}
	if id := tl.profileTarget.Load().id; id != "second" {
		t.Fatalf("daemon window selected %q", id)
	}
}

// TestStartupSelectsDefault: an automatic start without a remembered
// profile, or with the mapping disabled last, runs the fixed default,
// which resolveLibrary created; selection then follows the rules.
func TestStartupSelectsDefault(t *testing.T) {
	for name, state := range map[string]string{
		"no_state":      ``,
		"disabled_last": `{"schemaVersion":0,"lastProfileSet":true}`,
		"last_gone":     `{"schemaVersion":0,"lastProfile":"gone","lastProfileSet":true}`,
		"default_last":  `{"schemaVersion":0,"lastProfile":"default","lastProfileSet":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{"east":{"press":{"type":"keys","keys":"Space"}}}}}`)
			if state != "" {
				if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(state), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// The retired defaultProfile no longer chooses the start.
			tl, src := startFocusLive(t, dir, "", `{"schemaVersion":0,"defaultProfile":"paint","onUnmatched":"default","rules":[{"exe":"paint.exe","profile":"paint"}]}`, false)
			if id := tl.profileTarget.Load().id; id != profilelib.DefaultID {
				t.Fatalf("started with %q", id)
			}
			if data, err := profilelib.New(dir).Read(profilelib.DefaultID); err != nil || string(data) != profilelib.DefaultProfileText {
				t.Fatalf("default not created: %q %v", data, err)
			}
			button(src, "east", true)
			button(src, "east", false)
			tl.foregroundTo(t, focusApp("paint.exe"))
			tl.waitProfile(t, "paint")
			button(src, "east", true)
			tl.waitFor(t, tl.stdout, "send +Space")
			button(src, "east", false)
			if strings.Count(tl.stdout.String(), "send +Space") != 1 {
				t.Fatalf("the default mapped east:\n%s", tl.stdout.String())
			}
		})
	}
}

// TestDefaultRecreatedWhenDeletedWhileRunning: a default folder deleted by
// hand comes back at the next switch to it, so an unbound application
// never keeps the mapping of the profile before.
func TestDefaultRecreatedWhenDeletedWhileRunning(t *testing.T) {
	tl, src, dir := startSelectionLive(t, `{"schemaVersion":0,"rules":[{"exe":"game.exe","profile":"first"}]}`, nil)
	tl.foregroundTo(t, focusApp("game.exe"))
	tl.waitProfile(t, "first")
	if err := os.RemoveAll(filepath.Join(dir, "profiles", profilelib.DefaultID)); err != nil {
		t.Fatal(err)
	}
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space")
	tl.foregroundTo(t, focusApp("other.exe"))
	tl.waitProfile(t, profilelib.DefaultID)
	tl.waitFor(t, tl.stdout, "send -Space")
	button(src, "east", false)
	if data, err := profilelib.New(dir).Read(profilelib.DefaultID); err != nil || string(data) != profilelib.DefaultProfileText {
		t.Fatalf("default not recreated: %q %v", data, err)
	}
}

// TestBrokenDefaultMapsNothing: a default that stops parsing while
// padmated runs still takes an unbound application: the keys of the
// profile before are released and nothing is mapped; the file stays for
// repair.
func TestBrokenDefaultMapsNothing(t *testing.T) {
	const broken = `{"schemaVersion":0,"name":"默认","layers":{"base":{"east":`
	tl, src, dir := startSelectionLive(t, `{"schemaVersion":0,"rules":[{"exe":"game.exe","profile":"first"}]}`, nil)
	tl.foregroundTo(t, focusApp("game.exe"))
	tl.waitProfile(t, "first")
	writeLibraryProfile(t, dir, profilelib.DefaultID, broken)
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space")
	tl.foregroundTo(t, focusApp("other.exe"))
	tl.waitProfile(t, profilelib.DefaultID)
	tl.waitFor(t, tl.stdout, "send -Space")
	button(src, "east", false)
	tl.waitFor(t, tl.log, `"msg":"default profile unusable; it runs without mapping"`)
	button(src, "east", true)
	button(src, "east", false)
	tl.sync()
	if n := strings.Count(tl.stdout.String(), "send +Space"); n != 1 {
		t.Fatalf("the broken default mapped east:\n%s", tl.stdout.String())
	}
	if data, err := profilelib.New(dir).Read(profilelib.DefaultID); err != nil || string(data) != broken {
		t.Fatalf("broken default rewritten: %q %v", data, err)
	}
}
