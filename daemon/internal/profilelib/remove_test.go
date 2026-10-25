package profilelib

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, dir, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveDeletesFolder(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	fixture(t, dir, "paint", validProfile)
	fixture(t, dir, "other", validProfile)
	writeFile(t, filepath.Join(dir, "profiles", "paint", "main.ts"), "export {}\n")
	writeFile(t, filepath.Join(dir, "profiles", "paint", "lib", "util.ts"), "export const x = 1;\n")
	if err := l.Remove("paint", Revision([]byte(validProfile))); err != nil {
		t.Fatal(err)
	}
	// Gone with its scripts, no hidden folder left, nothing kept elsewhere
	// in the data directory.
	assertNoCopyLeftovers(t, dir, "other")
	if _, err := os.Lstat(filepath.Join(dir, "trash")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("removal created a trash folder", err)
	}
	if ids, err := l.IDs(); err != nil || !slices.Equal(ids, []string{"other"}) {
		t.Fatal(ids, err)
	}
	// The ID is free again.
	fixture(t, dir, "paint", validProfile)
	if err := l.Remove("paint", Revision([]byte(validProfile))); err != nil {
		t.Fatal(err)
	}
	// A folder without profile.json (a broken item List still shows) is
	// removed with the empty revision, like Put's creation rule.
	if err := os.MkdirAll(filepath.Join(dir, "profiles", "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := l.Remove("empty", ""); err != nil {
		t.Fatal(err)
	}
	assertNoCopyLeftovers(t, dir, "other")
}

// Links inside a profile folder are deleted themselves; what they point to
// outside the library stays.
func TestRemoveDeletesLinksNotTargets(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	fixture(t, outside, "secret", validProfile)
	fixture(t, dir, "paint", validProfile)
	secret := filepath.Join(outside, "profiles", "secret")
	if err := os.Symlink(secret, filepath.Join(dir, "profiles", "paint", "shared")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(secret, "profile.json"), filepath.Join(dir, "profiles", "paint", "linked.json")); err != nil {
		t.Fatal(err)
	}
	if err := New(dir).Remove("paint", Revision([]byte(validProfile))); err != nil {
		t.Fatal(err)
	}
	assertNoCopyLeftovers(t, dir)
	if data, err := New(outside).Read("secret"); err != nil || string(data) != validProfile {
		t.Fatal("changed outside the library", err)
	}
}

// A profile.json List cannot read shows with an empty revision and a
// problem; Remove accepts that revision, so the item can be deleted.
func TestRemoveUnreadableProfile(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(t *testing.T, folder string)
	}{
		{"oversized", func(t *testing.T, folder string) {
			writeFile(t, filepath.Join(folder, "profile.json"), strings.Repeat(" ", MaxDocumentBytes+1))
		}},
		{"folder", func(t *testing.T, folder string) {
			if err := os.MkdirAll(filepath.Join(folder, "profile.json"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			l := New(dir)
			tc.build(t, filepath.Join(dir, "profiles", "broken"))
			entries, err := l.List()
			if err != nil || len(entries) != 1 || entries[0].Revision != "" || entries[0].Problem == "" {
				t.Fatalf("%+v %v", entries, err)
			}
			err = l.Remove("broken", "stale")
			var conflict *ConflictError
			if !errors.As(err, &conflict) || conflict.Current != "" {
				t.Fatal(err)
			}
			if err := l.Remove("broken", entries[0].Revision); err != nil {
				t.Fatal(err)
			}
			if ids, err := l.IDs(); err != nil || len(ids) != 0 {
				t.Fatal(ids, err)
			}
		})
	}
}

func TestRemoveRefusals(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	fixture(t, dir, "paint", validProfile)
	fixture(t, dir, "other", validProfile)
	revision := Revision([]byte(validProfile))
	for _, expected := range []string{"", "stale"} {
		err := l.Remove("paint", expected)
		var conflict *ConflictError
		if !errors.As(err, &conflict) || conflict.Current != revision {
			t.Fatalf("revision %q: %v", expected, err)
		}
	}
	if err := l.Remove("missing", ""); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	for _, id := range []string{"", "../paint", "Paint", "nul", ".hidden"} {
		if err := l.Remove(id, ""); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	// The retired defaultProfile selects nothing, so it is no reference
	// (ADR-0053); a null rule names no profile.
	writeConfig(t, dir, `{"schemaVersion":0,"defaultProfile":"paint","onUnmatched":"default","rules":[
		{"exe":"a.exe","profile":"other"},
		{"exe":"b.exe","profile":"paint","enabled":false},
		{"exe":"c.exe","profile":null},
		{"exe":"d.exe","profile":"paint"}]}`)
	err := l.Remove("paint", revision)
	var referenced *ReferencedError
	if !errors.As(err, &referenced) || referenced.ID != "paint" || !slices.Equal(referenced.References, []string{"rules/1", "rules/3"}) {
		t.Fatal(err)
	}
	if err := l.Remove("other", revision); !errors.As(err, &referenced) || !slices.Equal(referenced.References, []string{"rules/0"}) {
		t.Fatal(err)
	}
	// References cannot be checked in a broken config, so nothing is deleted.
	writeConfig(t, dir, `{"schemaVersion":0,`)
	if err := l.Remove("other", revision); err == nil {
		t.Fatal("removed with an unreadable config")
	}
	for _, id := range []string{"paint", "other"} {
		if data, err := l.Read(id); err != nil || string(data) != validProfile {
			t.Fatal("refused removal changed", id, err)
		}
	}
	assertNoCopyLeftovers(t, dir, "other", "paint")
}

func TestRemoveRefusesLinkedProfileFolder(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	fixture(t, outside, "secret", validProfile)
	if err := os.Mkdir(filepath.Join(dir, "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "profiles", "escape")
	if err := os.Symlink(filepath.Join(outside, "profiles", "secret"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := New(dir).Remove("escape", Revision([]byte(validProfile)))
	var unsupported *UnsupportedError
	if !errors.As(err, &unsupported) || unsupported.Path != "escape" {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatal("link removed", err)
	}
	if data, err := New(outside).Read("secret"); err != nil || string(data) != validProfile {
		t.Fatal("changed outside the library", err)
	}
}
