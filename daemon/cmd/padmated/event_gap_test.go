package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// startGapLive runs a dry-run library session with config and one profile,
// "gap", whose east holds Ctrl+Shift+F13.
func startGapLive(t *testing.T, config string) (*testLive, *fakeSource) {
	t.Helper()
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "gap", `{"schemaVersion":0,"name":"gap","layers":{"base":{"east":{"press":{"type":"keys","keys":"Ctrl+Shift+F13"}}}}}`)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"schemaVersion":0,"lastProfile":"gap","lastProfileSet":true}`), 0o600); err != nil {
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
	cfg.ownForeground = func(uint32, string) bool { return false }
	cfg.ownAlive = func(uint32, string, uint64) bool { return false }
	tl.start(t, cfg)
	src.events <- input.Event{Kind: input.DeviceAdded, Device: 1, Info: &input.DeviceInfo{ID: 1, GUID: strings.Repeat("d3", 16), Name: liveTestPad, IsGamepad: true}}
	tl.sync()
	return tl, src
}

// sendLines returns the events of out's send lines, one string per line.
func sendLines(out string) []string {
	var lines []string
	for line := range strings.Lines(out) {
		if f := strings.Fields(line); len(f) > 2 && f[1] == "send" {
			lines = append(lines, strings.Join(f[2:], " "))
		}
	}
	return lines
}

// TestConfigPutAppliesEventGapLive: output.eventGapMs (OUT-17) is read at
// startup and applied at once by config.put, without a restart: with a gap
// each key event is its own send line, with 0 a chord is one.
func TestConfigPutAppliesEventGapLive(t *testing.T) {
	tl, src := startGapLive(t, `{"schemaVersion":0,"output":{"eventGapMs":5}}`)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	put := func(text string) {
		t.Helper()
		var doc rpcDocument
		if err := client.CallResult(ctx, "config.get", nil, &doc); err != nil {
			t.Fatal(err)
		}
		var saved rpcSaved
		if err := client.CallResult(ctx, "config.put", map[string]string{"revision": doc.Revision, "text": text}, &saved); err != nil {
			t.Fatal(err)
		}
		if !saved.Applied || saved.RestartRequired || saved.Problem != "" {
			t.Fatalf("eventGapMs not applied live: %+v", saved)
		}
	}
	pressed := 0
	press := func(wantLines int) {
		t.Helper()
		before := len(sendLines(tl.stdout.String()))
		pressed++
		button(src, "east", true)
		tl.waitForCount(t, tl.stdout, "+F13", pressed)
		button(src, "east", false)
		tl.waitForCount(t, tl.stdout, "-LCtrl", pressed)
		lines := sendLines(tl.stdout.String())[before:]
		if len(lines) != wantLines {
			t.Fatalf("send lines %q, want %d", lines, wantLines)
		}
	}

	press(6) // the startup gap: +LCtrl, +LShift, +F13, -F13, -LShift, -LCtrl
	put(`{"schemaVersion":0,"output":{"eventGapMs":0}}`)
	press(2) // +LCtrl +LShift +F13, -F13 -LShift -LCtrl
	put(`{"schemaVersion":0,"output":{"eventGapMs":10,"sendToOwnWindow":false}}`)
	press(6)
	if got := tl.selectionConfig().Output.EventGapMs; got != 10 {
		t.Errorf("live eventGapMs %d, want 10", got)
	}
	tl.cancel()
	if tl.waitCode(t) != 0 {
		t.Fatal(tl.stderr.String())
	}
}

// TestReplayEventGap: the replay paces the keys with config.json's
// output.eventGapMs too, on its virtual clock.
func TestReplayEventGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gap.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":0,"name":"gap","layers":{"base":{"east":{"press":{"type":"keys","keys":"Ctrl+Shift+F13"}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	evs := []input.Event{
		{Device: 1, Kind: input.DeviceAdded},
		{Time: 1_000_000, Device: 1, Kind: input.ButtonDown, Control: "east"},
		{Time: 100_000_000, Device: 1, Kind: input.ButtonUp, Control: "east"},
	}
	for _, tc := range []struct {
		config string
		want   []string
	}{
		{`{"schemaVersion":0}`, []string{"1.000 +LCtrl +LShift +F13", "100.000 -F13 -LShift -LCtrl"}},
		{`{"schemaVersion":0,"output":{"eventGapMs":10}}`, []string{
			"1.000 +LCtrl", "11.000 +LShift", "21.000 +F13", "100.000 -F13", "110.000 -LShift", "120.000 -LCtrl",
		}},
	} {
		t.Run(tc.config, func(t *testing.T) {
			var stdout, stderr strings.Builder
			p, prog, _, ok := loadProfile(path, &stderr)
			if !ok {
				t.Fatal(stderr.String())
			}
			opts := options{profile: path, settings: mustConfig(t, tc.config), dryRun: true}
			if code := replayDryRun(context.Background(), opts, p, prog, device{id: 1}, evs, &stdout, &stderr); code != 0 {
				t.Fatal(code, stderr.String())
			}
			var got []string
			for line := range strings.Lines(stdout.String()) {
				if f := strings.Fields(line); len(f) > 2 && f[1] == "send" {
					got = append(got, f[0]+" "+strings.Join(f[2:], " "))
				}
			}
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("send lines:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

// TestReplayEventGapRepeat: a typematic repeat faster than the gap does
// not pile up behind it (OUT-17): no press goes out after the button, and
// the chord goes up within a few gaps, however long it was held.
func TestReplayEventGapRepeat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repeat.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":0,"name":"repeat","layers":{"base":{"east":{"press":{"type":"keys","keys":"Ctrl+Z","mode":"hold","repeat":{"style":"typematic","rateHz":25}}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	const upAt = 10_001 // ms
	evs := []input.Event{
		{Device: 1, Kind: input.DeviceAdded},
		{Time: 1_000_000, Device: 1, Kind: input.ButtonDown, Control: "east"},
		{Time: upAt * 1_000_000, Device: 1, Kind: input.ButtonUp, Control: "east"},
	}
	var stdout, stderr strings.Builder
	p, prog, _, ok := loadProfile(path, &stderr)
	if !ok {
		t.Fatal(stderr.String())
	}
	opts := options{profile: path, settings: mustConfig(t, `{"schemaVersion":0,"output":{"eventGapMs":50}}`), dryRun: true}
	if code := replayDryRun(context.Background(), opts, p, prog, device{id: 1}, evs, &stdout, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	var last float64
	presses := 0
	for line := range strings.Lines(stdout.String()) {
		f := strings.Fields(line)
		if len(f) < 3 || f[1] != "send" {
			continue
		}
		at, err := strconv.ParseFloat(f[0], 64)
		if err != nil {
			t.Fatal(err)
		}
		last = at
		if f[2] == "+Z" {
			presses++
			if at > upAt {
				t.Errorf("+Z at %v ms, after the button went up at %v", at, upAt)
			}
		}
	}
	if presses < 100 {
		t.Errorf("%d presses of Z in 10 s, want the repeat", presses)
	}
	if last > upAt+150 {
		t.Errorf("last key event at %v ms, %v ms after the button", last, last-upAt)
	}
}
