package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func startupFixture(t *testing.T, args []string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"version": 1, "executable": exe, "args": args})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	id := hex.EncodeToString(sum[:])
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(dir, "padmate", "startup")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, id+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return "--startup-id=" + id
}

func TestStartupEntryUsesNormalValidation(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, args := range [][]string{{"--profile", filepath.Join(t.TempDir(), "missing.json"), "--dry-run"}, {"--profile", basicProfile}, {"--profile", basicProfile, "--inject", "--dry-run"}} {
		ref := startupFixture(t, args)
		var out, errout bytes.Buffer
		if code := runContext(context.Background(), []string{ref}, &out, &errout); code == 0 || out.Len() != 0 {
			t.Fatalf("invalid startup accepted: %q code %d %s", args, code, errout.String())
		}
	}
	var out, errout bytes.Buffer
	if code := runContext(context.Background(), []string{"--startup-id=" + strings.Repeat("a", 64)}, &out, &errout); code != 2 {
		t.Fatal(code)
	}
}

func TestDesktopOutputClosedStream(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "closed")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = desktopOutput(f).Write([]byte("dry-run")); err != nil {
		t.Fatal(err)
	}
}

func TestStartupArgsAllLiveFlags(t *testing.T) {
	var out bytes.Buffer
	original := []string{"--profile", basicProfile, "--dry-run", "--device", "Pad 画画", "--preset", "rawinput", "--hint", "SDL_JOYSTICK_HIDAPI_SWITCH=1", "--hint", "SDL_JOYSTICK_HIDAPI_PS4=0", "--watch=false", "--tray=false", "--decisions", "--log-level", "debug", "--log-file", "-"}
	opts, _, ok := parseFlags(original, &out, &out)
	if !ok {
		t.Fatal(out.String())
	}
	args, err := startupArgs(opts)
	if err != nil {
		t.Fatal(err)
	}
	restored, _, ok := parseFlags(args, &out, &out)
	if !ok {
		t.Fatal(out.String())
	}
	opts.profile, err = filepath.Abs(opts.profile)
	if err != nil {
		t.Fatal(err)
	}
	// Login startup has no inherited stderr: use the default persistent log.
	opts.logFile = ""
	// Startup intentionally canonicalizes paths and omits default flags.
	opts.forwardArgs, opts.explicitFlags = nil, nil
	restored.forwardArgs, restored.explicitFlags = nil, nil
	if !reflect.DeepEqual(opts, restored) {
		t.Fatalf("options changed:\n%+v\n%+v", opts, restored)
	}
}
