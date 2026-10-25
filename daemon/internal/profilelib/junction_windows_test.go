package profilelib

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// mklinkJunction creates a directory junction, which needs no privilege,
// unlike symlinks without Developer Mode.
func mklinkJunction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("mklink /J unavailable: %v %s", err, out)
	}
}

func TestJunctionsAreNeverFollowed(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	fixture(t, outside, "secret", validProfile)
	fixture(t, dir, "paint", validProfile)
	mklinkJunction(t, filepath.Join(dir, "profiles", "escape"), filepath.Join(outside, "profiles", "secret"))
	mklinkJunction(t, filepath.Join(dir, "profiles", "paint", "shared"), filepath.Join(outside, "profiles", "secret"))
	l := New(dir)
	revision := Revision([]byte(validProfile))
	var unsupported *UnsupportedError
	if err := l.Remove("escape", revision); !errors.As(err, &unsupported) {
		t.Fatal("removed junction:", err)
	}
	for _, from := range []string{"escape", "paint"} {
		if _, err := l.Copy(from, "copy"); !errors.As(err, &unsupported) {
			t.Fatal("copied through junction:", from, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(dir, "profiles", "escape")); err != nil {
		t.Fatal(err)
	}
	if data, err := New(outside).Read("secret"); err != nil || string(data) != validProfile {
		t.Fatal("changed outside the library", err)
	}
	assertNoCopyLeftovers(t, dir, "escape", "paint")
	// Deleting paint deletes its junction, not the folder it points to.
	if err := l.Remove("paint", revision); err != nil {
		t.Fatal(err)
	}
	assertNoCopyLeftovers(t, dir, "escape")
	if data, err := New(outside).Read("secret"); err != nil || string(data) != validProfile {
		t.Fatal("deleted through junction", err)
	}
}

// Removing interrupted copies never follows a junction named like one.
func TestCopyLeftoverJunctionKept(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	fixture(t, outside, "secret", validProfile)
	fixture(t, dir, "paint", validProfile)
	link := filepath.Join(dir, "profiles", ".tmp-link-0123456789ab")
	mklinkJunction(t, link, filepath.Join(outside, "profiles", "secret"))
	if _, err := New(dir).Copy("paint", "copy"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatal("junction removed", err)
	}
	if data, err := New(outside).Read("secret"); err != nil || string(data) != validProfile {
		t.Fatal("changed outside the library", err)
	}
}

// A junction named like the history folder is never written or read.
func TestHistoryJunctionNeverFollowed(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	l := New(dir)
	saved, err := l.Put("paint", "", []byte(validProfile))
	if err != nil {
		t.Fatal(err)
	}
	mklinkJunction(t, filepath.Join(dir, "profiles", "paint", HistoryDir), outside)
	saved, err = l.Put("paint", saved.Revision, []byte(validProfile+"\n"))
	var unsupported *UnsupportedError
	if err != nil || !errors.As(saved.HistoryErr, &unsupported) {
		t.Fatal("snapshot through a junction", saved, err)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatal("wrote through the junction", entries, err)
	}
	if _, err := l.History("paint"); !errors.As(err, &unsupported) {
		t.Fatal(err)
	}
	// Copying leaves the junction out instead of refusing the copy.
	if _, err := l.Copy("paint", "copy"); err != nil {
		t.Fatal(err)
	}
}

// Export refuses a junction inside the profile instead of following it;
// an import never replaces a profile folder that is a junction, and a
// junction named like a replaced folder is never renamed back.
func TestArchiveJunctionsNeverFollowed(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	fixture(t, outside, "secret", validProfile)
	fixture(t, dir, "paint", validProfile)
	mklinkJunction(t, filepath.Join(dir, "profiles", "paint", "shared"), filepath.Join(outside, "profiles", "secret"))
	mklinkJunction(t, filepath.Join(dir, "profiles", "escape"), filepath.Join(outside, "profiles", "secret"))
	mklinkJunction(t, filepath.Join(dir, "profiles", ".old-gone-0123456789ab"), filepath.Join(outside, "profiles", "secret"))
	l := New(dir)
	var unsupported *UnsupportedError
	if _, err := l.Export("paint", "v"); !errors.As(err, &unsupported) || unsupported.Path != "paint/shared" {
		t.Fatal("exported through junction:", err)
	}
	if _, err := l.Export("escape", "v"); !errors.As(err, &unsupported) {
		t.Fatal("exported a junction:", err)
	}
	a := testArchive(t, profileNamed("new"), nil)
	if _, err := l.Import(a, ImportOptions{Replace: "escape"}); !errors.As(err, &unsupported) {
		t.Fatal("replaced a junction:", err)
	}
	if _, err := l.EnsureDefault(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "profiles", "gone")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("junction renamed back as a profile", err)
	}
	if data, err := New(outside).Read("secret"); err != nil || string(data) != validProfile {
		t.Fatal("changed outside the library", err)
	}
}
