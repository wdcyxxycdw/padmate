package dtsgen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The media and volume keys (ADR-0070, OUT-20) are KeySpec names the parser
// accepts at runtime in every API version; their TypeScript names are declared
// from API 0.21 (MediaKey, and Key as the union with it), where the sequence and
// text members come too. Before it, Key is as it was: no declaration of the
// current API changes.
func TestMediaKeyAPIBoundary(t *testing.T) {
	names := []string{
		"VolumeUp", "VolumeDown", "VolumeMute", "MediaPlayPause", "MediaNext", "MediaPrevious", "MediaStop",
		"AudioVolumeUp", "AudioVolumeDown", "AudioVolumeMute", "MediaTrackNext", "MediaTrackPrevious",
	}
	for _, version := range []Version{{Major: 0, Minor: 7}, {Major: 0, Minor: 8}, {Major: 0, Minor: 16}, HostAPI, {Major: 0, Minor: 20}, {Major: 0, Minor: 21}, {Major: 0, Minor: 22}} {
		data, err := Filter(defaultDeclarations, version)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		withMedia := version.Minor >= 21
		if got := strings.Contains(text, "export type MediaKey ="); got != withMedia {
			t.Errorf("%s: MediaKey declared = %v, want %v", version, got, withMedia)
		}
		if got := strings.Count(text, "export type Key ="); got != 1 {
			t.Errorf("%s: %d declarations of Key, want 1", version, got)
		}
		if got := strings.Contains(text, "| MouseKey | MediaKey;"); got != withMedia {
			t.Errorf("%s: Key has MediaKey = %v, want %v", version, got, withMedia)
		}
		if got := strings.Contains(text, "| Modifier | MouseKey;"); got != (version.Minor >= 8 && !withMedia) {
			t.Errorf("%s: Key ends in MouseKey = %v", version, got)
		}
		for _, name := range names {
			if got := strings.Contains(text, `"`+name+`"`); got != withMedia {
				t.Errorf("%s: %s named = %v, want %v", version, name, got, withMedia)
			}
		}
	}
}

// A bare media key type-checks wherever a KeySpec does, and a chord that ends in
// one; names it does not have do not (ADR-0070). With the previous API's
// declarations (0.17) they are not names.
func TestMediaKeyTypesWithTSC(t *testing.T) {
	tsc := os.Getenv("PADMATE_TEST_TSC")
	if tsc == "" {
		t.Skip("set PADMATE_TEST_TSC to the TypeScript compiler entry point")
	}
	dir := t.TempDir()
	if err := Scaffold(dir, HostAPI, nil, nil); err != nil {
		t.Fatal(err)
	}
	const probe = `import { key, type Key, type KeySpec, type MediaKey } from "padmate";
const a: Key = "VolumeUp";
const b: KeySpec = "MediaNext";
const c: KeySpec = "Ctrl+VolumeDown";
const d: MediaKey = "AudioVolumeMute";
const e: KeySpec = "Shift+Alt+MediaTrackPrevious";
key.tap("MediaPlayPause");
key.tap(["VolumeUp", "Ctrl+MediaStop"]);
key.down("VolumeMute");
key.up("VolumeMute");
// @ts-expect-error not a media key name
const f: MediaKey = "Mute";
// @ts-expect-error not a media key name
key.tap("PlayPause");
// @ts-expect-error not a media key name
const g: Key = "VK_VOLUME_UP";
`
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(probe), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", tsc, "--project", dir, "--noEmit")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("media key types: %v\n%s", err, output)
	}

	previous := t.TempDir()
	if err := Scaffold(previous, Version{Major: 0, Minor: 17}, nil, nil); err != nil {
		t.Fatal(err)
	}
	const old = `import { key } from "padmate";
// @ts-expect-error API 0.21
key.tap("VolumeUp");
key.tap("WheelUp");
`
	if err := os.WriteFile(filepath.Join(previous, "main.ts"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("node", tsc, "--project", previous, "--noEmit")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("media key types of API 0.17: %v\n%s", err, output)
	}
}
