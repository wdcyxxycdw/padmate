package profilelib

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// assertNoCopyLeftovers checks that only the expected profile folders remain:
// no temporary folder, and nothing List would show beyond them.
func assertNoCopyLeftovers(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "profiles"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("profiles/ holds %v, want %v", names, want)
	}
}

func TestCopyDuplicatesProfileFolder(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	fixture(t, dir, "paint", validProfile)
	files := map[string]string{
		"main.ts":                   "import {x} from \"./lib/util\";\n",
		"lib/util.ts":               "export const x = 1;\n",
		"lib/deep/a/b/c/d/e/f/g.ts": "// seven folders deep\n",
	}
	for name, data := range files {
		writeFile(t, filepath.Join(dir, "profiles", "paint", filepath.FromSlash(name)), data)
	}
	if err := os.Mkdir(filepath.Join(dir, "profiles", "paint", "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	revision, err := l.Copy("paint", "paint-2")
	if err != nil || revision != Revision([]byte(validProfile)) {
		t.Fatal(revision, err)
	}
	files["profile.json"] = validProfile
	for name, want := range files {
		data, err := os.ReadFile(filepath.Join(dir, "profiles", "paint-2", filepath.FromSlash(name)))
		if err != nil || string(data) != want {
			t.Fatal(name, string(data), err)
		}
	}
	if info, err := os.Stat(filepath.Join(dir, "profiles", "paint-2", "empty")); err != nil || !info.IsDir() {
		t.Fatal("empty folder not copied", err)
	}
	if doc, err := l.Load("paint-2"); err != nil || doc.Revision != revision {
		t.Fatal(doc, err)
	}
	assertNoCopyLeftovers(t, dir, "paint", "paint-2")
}

func TestCopyRefusals(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	fixture(t, dir, "paint", validProfile)
	fixture(t, dir, "taken", validProfile)
	if _, err := l.Copy("paint", "taken"); !errors.As(err, new(*ExistsError)) {
		t.Fatal(err)
	}
	if _, err := l.Copy("paint", "paint"); !errors.As(err, new(*ExistsError)) {
		t.Fatal(err)
	}
	if _, err := l.Copy("missing", "new"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "profiles", "noprofile"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Copy("noprofile", "new"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("copied a folder without profile.json", err)
	}
	for _, ids := range [][2]string{{"paint", "../escape"}, {"paint", "Paint2"}, {"paint", ".tmp-x"}, {"../paint", "new"}, {"paint", "con"}} {
		if _, err := l.Copy(ids[0], ids[1]); err == nil {
			t.Fatalf("accepted %v", ids)
		}
	}
	assertNoCopyLeftovers(t, dir, "noprofile", "paint", "taken")
}

func TestCopyLimits(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(t *testing.T, root string)
	}{
		{"entries", func(t *testing.T, root string) {
			for i := range MaxCopyEntries {
				writeFile(t, filepath.Join(root, fmt.Sprintf("f%03d.ts", i)), "")
			}
		}},
		{"bytes", func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, "big.bin"), strings.Repeat("x", MaxCopyBytes-len(validProfile)+1))
		}},
		{"depth", func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, "1", "2", "3", "4", "5", "6", "7", "8", "9", "deep.ts"), "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			fixture(t, dir, "paint", validProfile)
			tc.build(t, filepath.Join(dir, "profiles", "paint"))
			_, err := New(dir).Copy("paint", "copy")
			var unsupported *UnsupportedError
			if !errors.As(err, &unsupported) {
				t.Fatal(err)
			}
			assertNoCopyLeftovers(t, dir, "paint")
		})
	}
	// Exactly at the limits is accepted: 256 entries including profile.json,
	// and folders eight levels deep.
	dir := t.TempDir()
	fixture(t, dir, "paint", validProfile)
	root := filepath.Join(dir, "profiles", "paint")
	writeFile(t, filepath.Join(root, "1", "2", "3", "4", "5", "6", "7", "8", "deep.ts"), "")
	for i := range MaxCopyEntries - 10 {
		writeFile(t, filepath.Join(root, fmt.Sprintf("f%03d.ts", i)), "")
	}
	if _, err := New(dir).Copy("paint", "copy"); err != nil {
		t.Fatal(err)
	}
}

func TestCopyRefusesLinks(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	fixture(t, dir, "paint", validProfile)
	writeFile(t, filepath.Join(outside, "secret.ts"), "secret")
	if err := os.Symlink(filepath.Join(outside, "secret.ts"), filepath.Join(dir, "profiles", "paint", "linked.ts")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "profiles", "paint"), filepath.Join(dir, "profiles", "alias")); err != nil {
		t.Fatal(err)
	}
	l := New(dir)
	for _, from := range []string{"paint", "alias"} {
		_, err := l.Copy(from, "copy")
		var unsupported *UnsupportedError
		if !errors.As(err, &unsupported) {
			t.Fatal(from, err)
		}
	}
	assertNoCopyLeftovers(t, dir, "alias", "paint")
}

// The writer lock covers reading the source and the final rename, not the
// writing: a save of the source while the copy writes neither waits nor
// changes the copy, which keeps the version it read.
func TestCopyWritesWithoutWriterLock(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	fixture(t, dir, "paint", validProfile)
	saved := validProfile + "\n"
	called := false
	copyWriteHook = func(hooked *Library) {
		called = true
		if !hooked.writeMu.TryLock() {
			t.Error("copy writes under the writer lock")
			return
		}
		hooked.writeMu.Unlock()
		if _, err := hooked.Put("paint", Revision([]byte(validProfile)), []byte(saved)); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { copyWriteHook = func(*Library) {} })
	revision, err := l.Copy("paint", "paint-2")
	if err != nil || !called || revision != Revision([]byte(validProfile)) {
		t.Fatal(revision, called, err)
	}
	for id, want := range map[string]string{"paint": saved, "paint-2": validProfile} {
		if data, err := l.Read(id); err != nil || string(data) != want {
			t.Fatal(id, string(data), err)
		}
	}
}

// A copy cut off by a crash leaves its hidden temporary folder behind; the
// next copy removes it.
func TestCopyRemovesLeftovers(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	fixture(t, dir, "paint", validProfile)
	writeFile(t, filepath.Join(dir, "profiles", ".tmp-old-0123456789ab", "profile.json"), validProfile)
	writeFile(t, filepath.Join(dir, "profiles", ".tmp-old-0123456789ab", "lib", "util.ts"), "")
	if ids, err := l.IDs(); err != nil || !slices.Equal(ids, []string{"paint"}) {
		t.Fatal("leftover visible in library", ids, err)
	}
	if _, err := l.Copy("paint", "paint-2"); err != nil {
		t.Fatal(err)
	}
	assertNoCopyLeftovers(t, dir, "paint", "paint-2")
}
