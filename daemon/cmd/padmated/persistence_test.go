package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

func TestLiveRestoresLayersAfterRestart(t *testing.T) {
	dir := t.TempDir()
	profDir := filepath.Join(dir, "profiles", "paint")
	if err := os.MkdirAll(profDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := `{"schemaVersion":0,"name":"paint","common":{"east":{"press":{"type":"keys","keys":"Space"}}},"layers":{"base":{"north":{"press":{"type":"layer","op":"switch","layer":"mode2"}}},"mode2":{}}}`
	if err := os.WriteFile(filepath.Join(profDir, "profile.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("c1", 16)
	for i := range 2 {
		src, tl := newFakeSource(), &testLive{}
		cfg := testConfig(t, tl, "", nil, "--dry-run", "--watch=false")
		opts := options{dataDir: dir, profileID: "paint", dryRun: true}
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
		tl.start(t, cfg)
		src.events <- input.Event{Kind: input.DeviceAdded, Device: 1, Info: &input.DeviceInfo{ID: 1, GUID: key, Name: liveTestPad, IsGamepad: true}}
		if i == 0 {
			src.events <- input.Event{Kind: input.ButtonDown, Device: 1, Control: "north"}
			src.events <- input.Event{Kind: input.ButtonUp, Device: 1, Control: "north"}
		}
		tl.sync()
		layers, ok := opts.memory.Load(key, "paint")
		if !ok || layers.Base != "mode2" {
			t.Fatalf("run %d: %+v, %v", i, layers, ok)
		}
		src.events <- input.Event{Kind: input.ButtonDown, Device: 1, Control: "east"}
		tl.waitFor(t, tl.stdout, "send +Space\n")
		tl.cancel()
		if code := tl.waitCode(t); code != 0 {
			t.Fatal(code, tl.stderr.String())
		}
		checkBalanced(t, tl.stdout.String())
		saved, err := profilelib.New(dir).ReadState()
		if err != nil || saved.LastProfile != "paint" || saved.Devices[key].Profiles["paint"].Base != "mode2" {
			t.Fatal(saved, err)
		}
	}
}
