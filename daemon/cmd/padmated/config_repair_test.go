package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

// configBackups returns the backups of config.json in dir.
func configBackups(t *testing.T, dir string) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(dir, "config.broken-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// statusRepair returns status.get's configRepair, nil when it has none.
func statusRepair(ctx context.Context, t *testing.T, client *jrpc2.Client) *rpcConfigRepair {
	t.Helper()
	var status map[string]json.RawMessage
	if err := client.CallResult(ctx, "status.get", nil, &status); err != nil {
		t.Fatal(err)
	}
	raw, ok := status["configRepair"]
	if !ok {
		return nil
	}
	var repair rpcConfigRepair
	if err := json.Unmarshal(raw, &repair); err != nil {
		t.Fatal(err)
	}
	return &repair
}

// TestStartRepairsConfig: a config.json with errors no longer stops the
// start (PRO-29): it is backed up, then repaired, keeping what was fine,
// and status.get tells the UI until it says it has shown it. A later read
// of a file broken by hand (config.get) repairs it too, and tells the
// subscribers at once.
func TestStartRepairsConfig(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{}}}`)
	const broken = `{"schemaVersion":0,"rules":[{"exe":"paint.exe","profile":"paint"}],"safety":{"panic":{"standard":{"buttons":["back"],"holdMs":2000}}}}`
	tl, _ := startLibraryLive(t, broken, options{dataDir: dir, dryRun: true})
	backups := configBackups(t, dir)
	if len(backups) != 1 {
		t.Fatal(backups)
	}
	if data, err := os.ReadFile(backups[0]); err != nil || string(data) != broken {
		t.Fatalf("backup %q %v", data, err)
	}
	if c, _, err := profilelib.New(dir).ReadConfig(); err != nil || len(c.Rules) != 1 || len(c.Safety.Panic) != 0 {
		t.Fatal(c, err)
	}
	if c := tl.selectionConfig(); len(c.Rules) != 1 || len(c.Safety.Panic) != 0 {
		t.Fatalf("running settings %+v", c)
	}
	tl.waitFor(t, tl.log, `"msg":"config.json had errors; repaired`)
	if !strings.Contains(tl.log.String(), `"reset":["/safety/panic/standard"]`) {
		t.Fatalf("log:\n%s", tl.log)
	}

	events := make(chan string, 16)
	client := rpcTestClient(t, tl.liveRun, &jrpc2.ClientOptions{OnNotify: func(req *jrpc2.Request) {
		if req.Method() == "profile.changed" {
			events <- req.ParamString()
		}
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	want := rpcConfigRepair{Backup: filepath.Base(backups[0]), Path: backups[0]}
	if got := statusRepair(ctx, t, client); got == nil || *got != want {
		t.Fatalf("configRepair %+v, want %+v", got, want)
	}
	// Only the repair the UI has shown goes.
	var seen map[string]bool
	if err := client.CallResult(ctx, "config.repairSeen", map[string]string{"backup": "config.broken-20000101-000000.json"}, &seen); err != nil || seen["cleared"] {
		t.Fatal(seen, err)
	}
	if got := statusRepair(ctx, t, client); got == nil {
		t.Fatal("an older backup cleared the repair")
	}
	if err := client.CallResult(ctx, "config.repairSeen", map[string]string{"backup": want.Backup}, &seen); err != nil || !seen["cleared"] {
		t.Fatal(seen, err)
	}
	if got := statusRepair(ctx, t, client); got != nil {
		t.Fatalf("configRepair %+v after it was shown", got)
	}
	if err := client.CallResult(ctx, "config.repairSeen", map[string]any{}, new(any)); err == nil {
		t.Fatal("config.repairSeen without a backup")
	}

	if err := client.CallResult(ctx, "subscribe", map[string]any{"topics": []string{"profile.changed"}}, new(any)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		hand, text string
		reset      bool
	}{
		{`{"schemaVersion":0,"enabled":false,"later":true}`, "{\n  \"schemaVersion\": 0,\n  \"enabled\": false\n}\n", false},
		{`{"schemaVersion":0,`, profile.DefaultConfigText, true},
	} {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(tc.hand), 0o600); err != nil {
			t.Fatal(err)
		}
		var doc rpcDocument
		if err := client.CallResult(ctx, "config.get", nil, &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Text != tc.text || doc.Problem != "" || doc.Revision != profilelib.Revision([]byte(tc.text)) {
			t.Fatalf("config.get %+v", doc)
		}
		repair := statusRepair(ctx, t, client)
		if repair == nil || repair.Reset != tc.reset || filepath.Dir(repair.Path) != dir || filepath.Base(repair.Path) != repair.Backup {
			t.Fatalf("configRepair %+v", repair)
		}
		if data, err := os.ReadFile(repair.Path); err != nil || string(data) != tc.hand {
			t.Fatalf("backup %q %v", data, err)
		}
		select {
		case e := <-events:
			var got struct {
				rpcConfigRepair
				Kind     string `json:"kind"`
				Revision string `json:"revision"`
			}
			if err := json.Unmarshal([]byte(e), &got); err != nil || got.Kind != "configRepaired" || got.rpcConfigRepair != *repair || got.Revision != doc.Revision {
				t.Fatalf("profile.changed %s", e)
			}
		case <-ctx.Done():
			t.Fatal("no profile.changed after the repair")
		}
	}
	if n := len(configBackups(t, dir)); n != 3 {
		t.Fatalf("%d backups", n)
	}
}

// TestDeleteProfileRepairsConfig: deleting a profile reads config.json for
// its references, so a file broken by hand is repaired first, and the
// deletion goes on.
func TestDeleteProfileRepairsConfig(t *testing.T) {
	dir := t.TempDir()
	const paint = `{"schemaVersion":0,"name":"paint","layers":{"base":{}}}`
	writeLibraryProfile(t, dir, "paint", paint)
	tl, _ := startLibraryLive(t, `{"schemaVersion":0}`, options{dataDir: dir, dryRun: true})
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"schemaVersion":0,"rules":[{"exe":"x.exe","profile":"../paint"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.CallResult(ctx, "profiles.delete", map[string]string{"id": "paint", "revision": profilelib.Revision([]byte(paint))}, new(any)); err != nil {
		t.Fatal(err)
	}
	if len(configBackups(t, dir)) != 1 || statusRepair(ctx, t, client) == nil {
		t.Fatal("config.json not repaired before the deletion")
	}
}

// TestStartResetsBrokenState: a state.json with errors starts as if there
// were none and is reset, with a warning in the log; the UI is not told.
func TestStartResetsBrokenState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"schemaVersion":0,"lastProfile":"../escape"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tl, _ := startLibraryLive(t, `{"schemaVersion":0}`, options{dataDir: dir, dryRun: true})
	tl.waitFor(t, tl.log, `"msg":"state.json had errors; starting without the saved state"`)
	if tl.cfg.opts.profileID != profilelib.DefaultID {
		t.Fatal(tl.cfg.opts.profileID)
	}
	if _, err := profilelib.New(dir).ReadState(); err != nil {
		t.Fatal("state.json not reset:", err)
	}
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if statusRepair(ctx, t, client) != nil || len(configBackups(t, dir)) != 0 {
		t.Fatal("state.json reset as config.json is")
	}
}

// TestReplayRepairsInMemory: a replay never writes (PRO-29): it runs with
// the repaired config.json and without a state.json that has errors,
// leaving both files as they are.
func TestReplayRepairsInMemory(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile(basicProfile)
	if err != nil {
		t.Fatal(err)
	}
	writeLibraryProfile(t, dir, "paint", string(data))
	const config, state = `{"schemaVersion":0,"timing":{"holdMs":-1},"bogus":true}`, `{"schemaVersion":0,"bogus":1}`
	for name, text := range map[string]string{"config.json": config, "state.json": state} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, out, stderr := runCmd(t, "--data-dir", dir, "--profile-id", "paint", "--fake-input", basicTrace, "--dry-run")
	if code != 0 || !strings.Contains(out, "send +Space") {
		t.Fatalf("exit %d\n%s\n%s", code, out, stderr)
	}
	if !strings.Contains(stderr, "padmated: config.json has errors; this replay runs without them") || !strings.Contains(stderr, "padmated: state.json has errors; this replay runs without it") {
		t.Fatalf("stderr %q", stderr)
	}
	for name, text := range map[string]string{"config.json": config, "state.json": state} {
		if got, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(got) != text {
			t.Fatalf("%s %q %v", name, got, err)
		}
	}
	if len(configBackups(t, dir)) != 0 {
		t.Fatal("a replay wrote a backup")
	}
}

// TestLogConfigRepairKnownFailure: a repair that could not be written is
// an error once; the same file read again (a config.get) is only debug.
func TestLogConfigRepairKnownFailure(t *testing.T) {
	var out strings.Builder
	log := slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelInfo}))
	failed := errors.New("access denied")
	logConfigRepair(log, &profilelib.ConfigRepair{Err: errors.New("bad"), WriteErr: failed})
	logConfigRepair(log, &profilelib.ConfigRepair{Err: errors.New("bad"), WriteErr: failed, Known: true})
	logConfigRepair(log, nil)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"level":"ERROR"`) || !strings.Contains(lines[0], "access denied") {
		t.Fatalf("logged %q", out.String())
	}
}
