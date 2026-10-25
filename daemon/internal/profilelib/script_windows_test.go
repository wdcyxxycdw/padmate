package profilelib

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A junction as profile folder, or inside it, never takes a script write
// out of the library.
func TestScriptWritesNeverFollowJunctions(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	fixture(t, outside, "secret", validProfile)
	fixture(t, dir, "paint", validProfile)
	mklinkJunction(t, filepath.Join(dir, "profiles", "escape"), filepath.Join(outside, "profiles", "secret"))
	mklinkJunction(t, filepath.Join(dir, "profiles", "paint", "lib.ts"), filepath.Join(outside, "profiles", "secret"))
	l := New(dir)
	var unsupported *UnsupportedError
	if _, err := writeScript(t, l, "escape", "main.ts", "x", false); !errors.As(err, &unsupported) {
		t.Fatal("wrote into a junction folder:", err)
	}
	if _, err := l.Folder("escape"); !errors.As(err, &unsupported) {
		t.Fatal("named a junction folder:", err)
	}
	for _, replace := range []bool{false, true} {
		if _, err := writeScript(t, l, "paint", "lib.ts", "x", replace); !errors.As(err, &unsupported) {
			t.Fatal("wrote over a junction:", replace, err)
		}
	}
	assertFolder(t, outside, "secret", "profile.json")
	assertFolder(t, dir, "paint", "profile.json", "lib.ts")
}

// Windows refuses to rename over a file that a reader holds open without
// FILE_SHARE_DELETE, as os.Open does. A brief reader is waited out; one
// that stays keeps the old script whole and leaves no temporary file.
func TestReplaceScriptWhileOpen(t *testing.T) {
	dir := t.TempDir()
	fixture(t, dir, "paint", validProfile)
	l := New(dir)
	if _, err := writeScript(t, l, "paint", "main.ts", "// v1\n", false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "profiles", "paint", "main.ts")
	held, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })
	if _, err := writeScript(t, l, "paint", "main.ts", "// v2\n", true); err == nil {
		t.Fatal("replaced a file held open")
	}
	if got := readScript(t, dir, "paint", "main.ts"); got != "// v1\n" {
		t.Fatal("old script damaged:", got)
	}
	assertFolder(t, dir, "paint", "profile.json", "main.ts")
	time.AfterFunc(2*replaceRetry, func() { _ = held.Close() })
	if replaced, err := writeScript(t, l, "paint", "main.ts", "// v2\n", true); err != nil || !replaced {
		t.Fatal("brief reader not waited out:", err)
	}
	if got := readScript(t, dir, "paint", "main.ts"); got != "// v2\n" {
		t.Fatal(got)
	}
	assertFolder(t, dir, "paint", "profile.json", "main.ts")
}
