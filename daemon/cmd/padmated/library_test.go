package main

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

func TestLibraryReplay(t *testing.T) {
	dir := t.TempDir()
	profileDir := filepath.Join(dir, "profiles", "paint")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(basicProfile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "profile.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := runCmd(t, "--data-dir", dir, "--profile-id", "paint", "--fake-input", basicTrace, "--dry-run")
	if code != 0 || stderr != "" || !strings.Contains(out, "send +Space") || !strings.Contains(out, "send -Space") {
		t.Fatalf("exit %d\n%s\n%s", code, out, stderr)
	}
	for _, id := range []string{"../paint", "missing"} {
		code, _, _ := runCmd(t, "--data-dir", dir, "--profile-id", id, "--fake-input", basicTrace, "--dry-run")
		if code == 0 {
			t.Fatalf("loaded %q", id)
		}
	}
	code, _, _ = runCmd(t, "--data-dir", dir, "--profile-id", "paint", "--profile", basicProfile, "--dry-run")
	if code != 2 {
		t.Fatal("accepted ambiguous selection", code)
	}
	// A replay only reads the library: it never creates the fixed default,
	// which it runs as the built-in empty profile when it is missing.
	code, out, stderr = runCmd(t, "--data-dir", dir, "--profile-id", profilelib.DefaultID, "--fake-input", basicTrace, "--dry-run")
	if code != 0 || stderr != "" || strings.Contains(out, "send +") {
		t.Fatalf("exit %d\n%s\n%s", code, out, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles", profilelib.DefaultID)); !os.IsNotExist(err) {
		t.Fatal("a replay created the default", err)
	}
}

// TestBrokenDefaultStarts: a fixed default that does not parse (a hand
// edit, an older default folder) runs as the built-in empty profile, so an
// automatic start still starts; the file stays as it is for repair.
func TestBrokenDefaultStarts(t *testing.T) {
	const broken = `{"schemaVersion":0,"name":"默认","layers":`
	dir := t.TempDir()
	writeLibraryProfile(t, dir, profilelib.DefaultID, broken)
	opts := options{dataDir: dir, dryRun: true}
	if err := resolveLibrary(&opts); err != nil || opts.profileID != profilelib.DefaultID || opts.defaultCreated || opts.defaultErr != nil {
		t.Fatal(opts, err)
	}
	var stderr strings.Builder
	p, _, _, ok := loadSelectedProfile(opts, &stderr)
	if !ok || p.Name != "默认" || len(p.Layers) != 1 || len(p.Layers[0].Bindings) != 0 {
		t.Fatal("a broken default stopped the start", p, stderr.String())
	}
	if !strings.Contains(stderr.String(), "default profile") {
		t.Fatalf("no warning: %q", stderr.String())
	}
	if data, err := profilelib.New(dir).Read(profilelib.DefaultID); err != nil || string(data) != broken {
		t.Fatalf("broken default rewritten: %q %v", data, err)
	}
}

func TestLibraryStartupSelection(t *testing.T) {
	dir := t.TempDir()
	write := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The retired defaultProfile no longer chooses the start (ADR-0053).
	write("config.json", `{"schemaVersion":0,"defaultProfile":"paint","onUnmatched":"default","timing":{"holdMs":500}}`)
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{}}}`)
	lib := profilelib.New(dir)
	opts := options{dataDir: dir, dryRun: true}
	if err := resolveLibrary(&opts); err != nil || opts.profileID != profilelib.DefaultID || !opts.autoProfile || !opts.defaultCreated || opts.defaultErr != nil {
		t.Fatal(opts, err)
	}
	if data, err := lib.Read(profilelib.DefaultID); err != nil || string(data) != profilelib.DefaultProfileText {
		t.Fatal("default not created", err)
	}
	p, _, _, ok := loadSelectedProfile(opts, io.Discard)
	if !ok || p.Name != "默认" || p.Timing.HoldMs != 500 {
		t.Fatal("startup did not load the default", p)
	}
	args, err := startupArgs(opts)
	if err != nil || slices.Contains(args, "--profile-id") || slices.Contains(args, "--profile") || !slices.Contains(args, "--data-dir") {
		t.Fatal(args, err)
	}
	if err := lib.WriteState(profile.State{LastProfile: "other"}); err != nil {
		t.Fatal(err)
	}
	// A remembered profile that is gone falls back to the default.
	opts = options{dataDir: dir, dryRun: true}
	if err := resolveLibrary(&opts); err != nil || opts.profileID != profilelib.DefaultID || opts.missingLastProfile != "other" || !opts.autoProfile || opts.defaultCreated {
		t.Fatal(opts, err)
	}
	writeLibraryProfile(t, dir, "other", `{"schemaVersion":0,"name":"other","layers":{"base":{}}}`)
	opts = options{dataDir: dir, dryRun: true}
	if err := resolveLibrary(&opts); err != nil || opts.profileID != "other" || opts.missingLastProfile != "" {
		t.Fatal(opts, err)
	}
	opts = options{dataDir: dir, profileID: "explicit", dryRun: true}
	if err := resolveLibrary(&opts); err != nil || opts.profileID != "explicit" {
		t.Fatal(opts, err)
	}
	// The mapping disabled last is no profile to resume.
	if err := lib.WriteState(profile.State{LastProfileSet: true}); err != nil {
		t.Fatal(err)
	}
	opts = options{dataDir: dir, dryRun: true}
	if err := resolveLibrary(&opts); err != nil || opts.profileID != profilelib.DefaultID {
		t.Fatal(opts, err)
	}
	// A default that cannot be read from disk still starts, as the built-in
	// empty default.
	if err := os.RemoveAll(filepath.Join(dir, "profiles", profilelib.DefaultID)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profiles", profilelib.DefaultID), []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts = options{dataDir: dir, dryRun: true}
	if err := resolveLibrary(&opts); err != nil || opts.profileID != profilelib.DefaultID || opts.defaultErr == nil {
		t.Fatal(opts, err)
	}
	if p, _, _, ok := loadSelectedProfile(opts, io.Discard); !ok || p.Name != "默认" {
		t.Fatal("startup without a default file failed", p)
	}
}

func TestLibraryStartupArgs(t *testing.T) {
	dir := t.TempDir()
	args, err := startupArgs(options{profileID: "paint", dataDir: dir, dryRun: true, tray: true, watch: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(args, []string{"--profile-id", "paint", "--data-dir", dir, "--dry-run", "--log-level", "info"}) {
		t.Fatal(args)
	}
}
