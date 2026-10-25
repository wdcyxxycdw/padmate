package profilelib

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPutProfileRevisionsAndValidation(t *testing.T) {
	l := New(t.TempDir())
	saved, err := l.Put("paint", "", []byte(validProfile))
	first := saved.Revision
	if err != nil || first != Revision([]byte(validProfile)) {
		t.Fatal(first, err)
	}
	for _, revision := range []string{"", "stale"} {
		_, err := l.Put("paint", revision, []byte(validProfile+"\n"))
		var conflict *ConflictError
		if !errors.As(err, &conflict) || conflict.Current != first {
			t.Fatalf("revision %q: %v", revision, err)
		}
	}
	for _, data := range []string{`{`, `{"schemaVersion":1}`, `{"schemaVersion":0,"layers":null}`, strings.Repeat(" ", MaxDocumentBytes+1)} {
		if _, err := l.Put("paint", first, []byte(data)); err == nil {
			t.Fatal("invalid profile saved")
		}
	}
	got, err := l.Read("paint")
	if err != nil || string(got) != validProfile {
		t.Fatal("failed put changed profile", err)
	}
	saved, err = l.Put("paint", first, []byte(validProfile+"\n"))
	if err != nil || saved.Revision == first {
		t.Fatal(saved, err)
	}
	// Broken files remain repairable using their raw revision.
	fixture(t, l.Dir(), "broken", "{")
	if _, err := l.Put("broken", Revision([]byte("{")), []byte(validProfile)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../escape", "Paint", "nul"} {
		if _, err := l.Put(id, "", []byte(validProfile)); err == nil {
			t.Fatalf("accepted %s", id)
		}
	}
}

func TestPutConcurrentRevisionHasOneWinner(t *testing.T) {
	l := New(t.TempDir())
	saved, err := l.Put("paint", "", []byte(validProfile))
	if err != nil {
		t.Fatal(err)
	}
	rev := saved.Revision
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for n := range 8 {
		wg.Go(func() {
			_, err := l.Put("paint", rev, []byte(validProfile+strings.Repeat("\n", n+1)))
			results <- err
		})
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else {
			var conflict *ConflictError
			if !errors.As(err, &conflict) {
				t.Fatal(err)
			}
		}
	}
	if winners != 1 {
		t.Fatalf("successful writers = %d", winners)
	}
}

func TestPutConfigRepairAndStrictDeviceKeys(t *testing.T) {
	l := New(t.TempDir())
	data, revision, err := l.ConfigText()
	if err != nil || revision != "" || string(data) != `{"schemaVersion":0}` {
		t.Fatal(string(data), revision, err)
	}
	first, err := l.PutConfig("", []byte(`{"schemaVersion":0,"enabled":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.PutConfig(first, []byte(`{"schemaVersion":0,"devices":{"bad":{"ignored":true}}}`)); err == nil {
		t.Fatal("invalid DeviceKey accepted")
	}
	cfg, _, err := l.ReadConfig()
	if err != nil || cfg.Enabled {
		t.Fatal(cfg, err)
	}
	path := filepath.Join(l.Dir(), "config.json")
	if err := os.WriteFile(path, []byte("bad json"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, revision, err = l.ConfigText()
	if err != nil || string(data) != "bad json" || revision != Revision(data) {
		t.Fatal("could not read broken config", err)
	}
	if _, err := l.PutConfig(revision, []byte(`{"schemaVersion":0}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.PutConfig(revision, []byte(`{"schemaVersion":0,"enabled":false}`)); err == nil {
		t.Fatal("stale config overwritten")
	}
}

func TestPutConfinesLinksAndLeavesNoTemporaryFiles(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	fixture(t, outside, "secret", validProfile)
	if err := os.Mkdir(filepath.Join(dir, "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "profiles", "secret"), filepath.Join(dir, "profiles", "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	l := New(dir)
	if _, err := l.Put("escape", Revision([]byte(validProfile)), []byte(validProfile+"\n")); err == nil {
		t.Fatal("wrote outside profile library")
	}
	data, err := New(outside).Read("secret")
	if err != nil || string(data) != validProfile {
		t.Fatal("changed outside file", err)
	}
	// Atomic replacement failure must preserve the destination and clean up.
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := atomicReplace(root, "profiles", []byte("new")); err == nil {
		t.Fatal("replaced directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "profiles" {
		t.Fatal("temporary file leaked", entries, err)
	}
}

// A binding that holds a mouse button with typematic repeat is refused before
// anything is written, with the message that suggests repeated clicks; the same
// binding as a tap repeat, and a wheel key's repeat, are saved (ADR-0062).
func TestPutRefusesTypematicMouseButton(t *testing.T) {
	const template = `{"schemaVersion":1,"name":"clicks","layers":{"base":{"south":{"press":{"type":"keys","keys":%q,"mode":%q,"repeat":{"delayMs":100}}}}}}`
	for _, tc := range []struct {
		keys, mode string
		ok         bool
	}{
		{"MouseLeft", "hold", false},
		{"Ctrl+RButton", "hold", false},
		{"XButton1", "hold", false},
		{"MouseLeft", "tap", true},
		{"MouseMiddle", "tap", true},
		{"WheelUp", "hold", true},
		{"Z", "hold", true},
	} {
		l := New(t.TempDir())
		_, err := l.Put("clicks", "", []byte(fmt.Sprintf(template, tc.keys, tc.mode)))
		if tc.ok {
			if err != nil {
				t.Errorf("%s %s repeat refused: %v", tc.mode, tc.keys, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), `use mode "tap" to repeat clicks`) {
			t.Errorf("%s %s repeat: Put = %v, want the typematic error", tc.mode, tc.keys, err)
		}
		if _, rerr := l.Read("clicks"); rerr == nil {
			t.Errorf("%s %s repeat: the refused profile was written", tc.mode, tc.keys)
		}
	}
}
