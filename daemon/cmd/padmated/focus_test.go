package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winfocus"
)

func writeLibraryProfile(t *testing.T, dir, id, data string) {
	t.Helper()
	path := filepath.Join(dir, "profiles", id, "profile.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeLastProfile makes id the profile state.json remembers, which an
// automatic start resumes instead of the fixed default.
func writeLastProfile(t *testing.T, dir, id string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"schemaVersion":0,"lastProfile":"`+id+`","lastProfileSet":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

// startFocusLive starts an automatic dry-run library daemon on dir with
// config as config.json; start, unless empty, is the profile it starts
// with (writeLastProfile), otherwise the fixed default.
func startFocusLive(t *testing.T, dir, start, config string, watch bool) (*testLive, *fakeSource) {
	t.Helper()
	if start != "" {
		writeLastProfile(t, dir, start)
	}
	return startLibraryLive(t, config, options{dataDir: dir, dryRun: true, watch: watch})
}

// startLibraryLive starts a dry-run library daemon with the startup
// options opts (dataDir, and profileID or profile for an explicit start)
// after writing config to the data directory's config.json; changes
// change its liveConfig before it starts.
func startLibraryLive(t *testing.T, config string, opts options, changes ...func(*liveConfig)) (*testLive, *fakeSource) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(opts.dataDir, "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	src, tl := newFakeSource(), &testLive{}
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
	cfg.open = func(input.Options) (eventSource, error) { return src, nil }
	cfg.watch.Interval, cfg.watch.Debounce = 5*time.Millisecond, 10*time.Millisecond
	for _, change := range changes {
		change(&cfg)
	}
	tl.start(t, cfg)
	src.events <- input.Event{Kind: input.DeviceAdded, Device: 1, Info: &input.DeviceInfo{ID: 1, GUID: strings.Repeat("d2", 16), Name: liveTestPad, IsGamepad: true}}
	tl.sync()
	return tl, src
}

func focusApp(exe string) winfocus.App {
	return winfocus.App{Window: 1, PID: 100, Exe: exe, Path: `C:\apps\` + exe}
}

func TestScriptEnvironmentWakesIdleSlotAndSwitchesProfile(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "first", `{"schemaVersion":0,"name":"first","script":{"entry":"main.ts","api":"0.21"},"layers":{"base":{}}}`)
	writeLibraryProfile(t, dir, "second", `{"schemaVersion":0,"name":"second","layers":{"base":{"east":{"press":{"type":"keys","keys":"F14"}}}}}`)
	srcText := `import {app,profiles,log,on,defineScript} from "padmate";
export default defineScript({setup(){
  log.info("library at setup", profiles.list().join(","), profiles.current);
  app.onChange(a => log.info("foreground from idle", a?.exe, app.is("paint.exe")));
  on("south","press",()=>profiles.switchTo("second"));
},onActivate(){log.info("environment ready");}});`
	if err := os.WriteFile(filepath.Join(dir, "profiles", "first", "main.ts"), []byte(srcText), 0o600); err != nil {
		t.Fatal(err)
	}
	tl, src := startFocusLive(t, dir, "first", `{"schemaVersion":0,"rules":[{"exe":"paint.exe","profile":"first"},{"exe":"later.exe","profile":"second"}]}`, false)
	tl.waitFor(t, tl.log, "environment ready")
	// The library always holds the fixed default (ADR-0053).
	tl.waitFor(t, tl.log, "library at setup default,first,second first")
	tl.onForeground(focusApp("paint.exe"))
	tl.waitFor(t, tl.log, "foreground from idle paint.exe true")
	src.events <- input.Event{Kind: input.ButtonDown, Device: 1, Control: "south"}
	src.events <- input.Event{Kind: input.ButtonUp, Device: 1, Control: "south"}
	tl.waitFor(t, tl.log, `"msg":"profile selected","profile":"second"`)
	src.events <- input.Event{Kind: input.ButtonDown, Device: 1, Control: "east"}
	src.events <- input.Event{Kind: input.ButtonUp, Device: 1, Control: "east"}
	tl.waitFor(t, tl.stdout, "send +F14")
	before := strings.Count(tl.log.String(), "foreground from idle")
	tl.onForeground(focusApp("later.exe"))
	tl.waitFor(t, tl.log, `"foreground evaluated","exe":"later.exe"`)
	tl.sync()
	if strings.Count(tl.log.String(), "foreground from idle") != before {
		t.Fatal("old app listener survived profile switch")
	}
}

func TestForegroundSwitchReleaseAndRestore(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","common":{"east":{"press":{"type":"keys","keys":"Alt"}},"north":{"press":{"type":"layer","op":"switch","layer":"mode2"}}},"layers":{"base":{},"mode2":{}}}`)
	writeLibraryProfile(t, dir, "other", `{"schemaVersion":0,"name":"other","layers":{"base":{"east":{"press":{"type":"keys","keys":"F14"}}}}}`)
	tl, src := startFocusLive(t, dir, "paint", `{"schemaVersion":0,"rules":[{"exe":"game.exe","profile":null},{"exe":"krita.exe","profile":"paint"},{"exe":"paint.exe","profile":"paint"},{"exe":"other.exe","profile":"other"}]}`, false)
	press := func(kind input.EventKind, control string) {
		src.events <- input.Event{Kind: kind, Device: 1, Control: control}
		tl.sync()
	}
	// The real session callback must enqueue without waiting for a slot.
	if tl.session.cfg.Focus == nil {
		t.Fatal("foreground observer was not attached to the session")
	}
	change := func(exe string) {
		t.Helper()
		tl.session.cfg.Focus.OnChange(focusApp(exe))
		tl.waitFor(t, tl.log, `"foreground evaluated","exe":"`+exe+`"`)
	}
	press(input.ButtonDown, "north")
	press(input.ButtonUp, "north")
	press(input.ButtonDown, "east")
	tl.waitFor(t, tl.stdout, "send +LAlt")
	change("krita.exe")
	if strings.Contains(tl.stdout.String(), " -LAlt") {
		t.Fatal("profileChange released Alt in the same profile")
	}
	// An application no rule binds gets the fixed default, which maps
	// nothing (ADR-0053); an old null rule binds nothing.
	change("game.exe")
	tl.waitFor(t, tl.stdout, " -LAlt")
	if got := tl.profileTarget.Load().id; got != profilelib.DefaultID {
		t.Fatal("unbound application did not select the default", got)
	}
	press(input.ButtonUp, "east")
	press(input.ButtonDown, "east")
	press(input.ButtonUp, "east")
	change("unmatched.exe")
	press(input.ButtonDown, "east")
	press(input.ButtonUp, "east")
	if n := strings.Count(tl.stdout.String(), "send +LAlt"); n != 1 || tl.profileTarget.Load().id != profilelib.DefaultID {
		t.Fatal("the default produced output", tl.stdout.String())
	}
	change("other.exe")
	press(input.ButtonDown, "east")
	tl.waitFor(t, tl.stdout, "send +F14")
	change("paint.exe")
	tl.waitFor(t, tl.stdout, "send -F14")
	if n := strings.Count(tl.stdout.String(), "send +LAlt"); n != 1 {
		t.Fatal("switch replayed held control")
	}
	views, err := tl.sup.Inspect(context.Background())
	if err != nil || len(views) == 0 || views[0].Mapping.Layers.Base != "mode2" {
		t.Fatal("layer not restored", views, err)
	}
	press(input.ButtonUp, "east")
	press(input.ButtonDown, "east")
	press(input.ButtonUp, "east")
	tl.waitForCount(t, tl.stdout, "send +LAlt", 2)
	tl.cancel()
	if tl.waitCode(t) != 0 {
		t.Fatal(tl.stderr.String())
	}
	checkBalanced(t, tl.stdout.String())
	var edges strings.Builder
	for line := range strings.Lines(tl.stdout.String()) {
		if _, sent, ok := strings.Cut(line, "send "); ok {
			edges.WriteString(strings.TrimSpace(sent) + "\n")
		}
	}
	checkGolden(t, "testdata/focus-default.golden", edges.String())
	state, err := profilelib.New(dir).ReadState()
	if err != nil || state.LastProfile != "paint" {
		t.Fatal(state, err)
	}
}

func TestLibraryWatcherFollowsSelection(t *testing.T) {
	dir := t.TempDir()
	profileText := func(name, key string) string {
		return `{"schemaVersion":0,"name":"` + name + `","layers":{"base":{"east":{"press":{"type":"keys","keys":"` + key + `"}}}}}`
	}
	writeLibraryProfile(t, dir, "first", profileText("first", "F13"))
	writeLibraryProfile(t, dir, "second", profileText("second", "F14"))
	tl, src := startFocusLive(t, dir, "first", `{"schemaVersion":0}`, true)
	if err := tl.activateProfile(context.Background(), "second", nil); err != nil {
		t.Fatal(err)
	}
	writeLibraryProfile(t, dir, "first", profileText("first changed", "F16"))
	writeLibraryProfile(t, dir, "second", profileText("second changed", "F15"))
	tl.waitFor(t, tl.log, `"msg":"profile reloaded"`)
	src.events <- input.Event{Kind: input.ButtonDown, Device: 1, Control: "east"}
	src.events <- input.Event{Kind: input.ButtonUp, Device: 1, Control: "east"}
	tl.waitFor(t, tl.stdout, "send +F15")
	if strings.Contains(tl.stdout.String(), "send +F16") {
		t.Fatal("old profile watcher replaced selection")
	}
	if err := tl.activateProfile(context.Background(), "missing", nil); err == nil {
		t.Fatal("missing profile succeeded")
	}
	if tl.profileTarget.Load().id != "second" {
		t.Fatal("failed switch replaced selection")
	}
}

func TestProfileSwitchDisposesOldScriptWhenNewSetupFails(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"first", "second"} {
		writeLibraryProfile(t, dir, id, `{"schemaVersion":0,"name":"`+id+`","script":{"entry":"main.ts","api":"0.21"},"layers":{"base":{}}}`)
	}
	first := `import {defineScript,every,key} from "padmate";
export default defineScript({setup(){every(20,()=>{key.tap("F13",{holdMs:0});});}});`
	second := `import {defineScript} from "padmate";
export default defineScript({setup(){throw new Error("blocked candidate");}});`
	for id, data := range map[string]string{"first": first, "second": second} {
		if err := os.WriteFile(filepath.Join(dir, "profiles", id, "main.ts"), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tl, _ := startFocusLive(t, dir, "first", `{"schemaVersion":0}`, false)
	tl.waitFor(t, tl.stdout, "send +F13")
	if err := tl.activateProfile(context.Background(), "second", nil); err != nil {
		t.Fatal(err)
	}
	tl.waitFor(t, tl.log, "blocked candidate")
	before := strings.Count(tl.stdout.String(), "send +F13")
	time.Sleep(100 * time.Millisecond)
	tl.sync()
	if got := strings.Count(tl.stdout.String(), "send +F13"); got != before {
		t.Fatal("old script still outputs in the new profile", before, got)
	}
}

func TestForegroundPoliciesInLive(t *testing.T) {
	for _, tc := range []struct {
		name, extra, wantID string
		release             bool
	}{
		{"always_same", `"releaseOnSwitch":"always","rules":[{"exe":"target.exe","profile":"first"}]`, "first", true},
		{"profile_change_same", `"rules":[{"exe":"target.exe","profile":"first"}]`, "first", false},
		{"never_same", `"releaseOnSwitch":"never","rules":[{"exe":"target.exe","profile":"first"}]`, "first", false},
		{"disabled", `"enabled":false,"releaseOnSwitch":"always"`, "first", false},
		{"unbound_default", `"rules":[{"exe":"other.exe","profile":"second"}]`, profilelib.DefaultID, true},
		{"disabled_rule_default", `"rules":[{"exe":"target.exe","profile":"second","enabled":false}]`, profilelib.DefaultID, true},
		// Members ADR-0053 retired select nothing: target.exe still gets
		// the fixed default.
		{"legacy_ignored", `"ignore":["target.exe"],"onUnmatched":"keep"`, profilelib.DefaultID, true},
		{"legacy_default", `"onUnmatched":"default","defaultProfile":"second"`, profilelib.DefaultID, true},
		{"legacy_none", `"onUnmatched":"none","rules":[{"exe":"target.exe","profile":null}]`, profilelib.DefaultID, true},
		{"never_still_releases_new_profile", `"releaseOnSwitch":"never","rules":[{"exe":"target.exe","profile":"second"}]`, "second", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, id := range []string{"first", "second"} {
				writeLibraryProfile(t, dir, id, `{"schemaVersion":0,"name":"`+id+`","layers":{"base":{"east":{"press":{"type":"keys","keys":"Space"}}}}}`)
			}
			tl, src := startFocusLive(t, dir, "first", `{"schemaVersion":0,`+tc.extra+`}`, false)
			src.events <- input.Event{Kind: input.ButtonDown, Device: 1, Control: "east"}
			tl.waitFor(t, tl.stdout, "send +Space")
			app := focusApp("target.exe")
			tl.foreground.Store(&app)
			if err := tl.selectForeground(context.Background(), &app); err != nil {
				t.Fatal(err)
			}
			tl.sync()
			if got := tl.profileTarget.Load().id; got != tc.wantID {
				t.Fatal(got, tc.wantID)
			}
			if got := strings.Contains(tl.stdout.String(), "send -Space"); got != tc.release {
				t.Fatal("release decision", got, tc.release, tl.stdout.String())
			}
			src.events <- input.Event{Kind: input.ButtonUp, Device: 1, Control: "east"}
			tl.sync()
		})
	}
}

func TestTriggerAltTabSurvivesTaskSwitcher(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{"lt":{"press":{"type":"keys","keys":"Alt"}},"south":{"press":{"type":"keys","keys":"Tab","mode":"tap"}}}}}`)
	tl, src := startFocusLive(t, dir, "paint", `{"schemaVersion":0,"rules":[{"exe":"target.exe","profile":"paint"}]}`, false)
	src.events <- input.Event{Kind: input.AxisMotion, Device: 1, Control: "lefttrigger", Value: 32767}
	tl.waitFor(t, tl.stdout, "send +LAlt")
	src.events <- input.Event{Kind: input.ButtonDown, Device: 1, Control: "south"}
	src.events <- input.Event{Kind: input.ButtonUp, Device: 1, Control: "south"}
	tl.waitFor(t, tl.stdout, "send -Tab")
	taskSwitcher := focusApp("explorer.exe")
	taskSwitcher.Class = "MultitaskingViewFrame"
	tl.session.cfg.Focus.OnChange(taskSwitcher)
	app := focusApp("target.exe")
	tl.foreground.Store(&app)
	if err := tl.selectForeground(context.Background(), &app); err != nil {
		t.Fatal(err)
	}
	tl.sync()
	if strings.Contains(tl.stdout.String(), "-LAlt") {
		t.Fatal("Alt released while LT remained held", tl.stdout.String())
	}
	src.events <- input.Event{Kind: input.AxisMotion, Device: 1, Control: "lefttrigger", Value: 0}
	tl.waitFor(t, tl.stdout, "-LAlt")
	checkBalanced(t, tl.stdout.String())
}

func TestProfileActionSwitchesWithoutBlockingSlot(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "first", `{"schemaVersion":0,"name":"first","layers":{"base":{"east":{"press":{"type":"keys","keys":"Space"}},"south":{"press":{"type":"profile","profile":"second"}}}}}`)
	writeLibraryProfile(t, dir, "second", `{"schemaVersion":0,"name":"second","layers":{"base":{"east":{"press":{"type":"keys","keys":"F14"}}}}}`)
	tl, src := startFocusLive(t, dir, "first", `{"schemaVersion":0}`, false)
	src.events <- input.Event{Kind: input.ButtonDown, Device: 1, Control: "east"}
	tl.waitFor(t, tl.stdout, "send +Space")
	src.events <- input.Event{Kind: input.ButtonDown, Device: 1, Control: "south"}
	src.events <- input.Event{Kind: input.ButtonUp, Device: 1, Control: "south"}
	tl.waitFor(t, tl.log, `"msg":"profile selected","profile":"second"`)
	tl.waitFor(t, tl.stdout, "send -Space")
	src.events <- input.Event{Kind: input.ButtonUp, Device: 1, Control: "east"}
	src.events <- input.Event{Kind: input.ButtonDown, Device: 1, Control: "east"}
	src.events <- input.Event{Kind: input.ButtonUp, Device: 1, Control: "east"}
	tl.waitFor(t, tl.stdout, "send +F14")
	// A delayed request from the previous program generation cannot switch
	// the new configuration back after its input has already been canceled.
	if err := tl.applyProfileRequest(context.Background(), profileRequest{gen: 1, id: "first"}); err != nil {
		t.Fatal(err)
	}
	if tl.profileTarget.Load().id != "second" {
		t.Fatal("stale profile action was accepted")
	}
}
