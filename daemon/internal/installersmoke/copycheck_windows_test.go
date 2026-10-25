package installersmoke

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/installerpkg"
)

// The checks of the installer's copy of itself assert that something is not
// there, so they would pass if they could never fire: these cases include the
// ones that must fire. They need no installer.

func TestDropsCopy(t *testing.T) {
	for label, want := range map[string]bool{
		"v0.16.0": false, "v0.17.0-rc.1": false, "v0.17.0-rc.2": false,
		"v0.17.0-rc.3": true, "v0.17.0-rc.10": true, "v0.17.0": true, "v0.18.0-rc.1": true,
	} {
		tag, err := installerpkg.ParseTag(label)
		if err != nil {
			t.Fatal(err)
		}
		if got := dropsCopy(t, tag); got != want {
			t.Errorf("dropsCopy(%s) = %v, want %v", label, got, want)
		}
	}
}

func TestCopyComparisons(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	older := write("older.exe", "installer A")
	sameAsOlder := write("same.exe", "installer A")
	newer := write("newer.exe", "installer B") // same size, other bytes
	longer := write("longer.exe", "installer AB")
	missing := filepath.Join(dir, "missing.exe")

	for _, c := range []struct {
		a, b string
		want bool
	}{
		{older, sameAsOlder, true},
		{older, newer, false},
		{older, longer, false},
		{older, missing, false},
		{missing, missing, false},
		{older, dir, false},
	} {
		if got := sameBytes(c.a, c.b); got != c.want {
			t.Errorf("sameBytes(%s, %s) = %v, want %v", filepath.Base(c.a), filepath.Base(c.b), got, c.want)
		}
	}

	// leftCopyOf compares with the installer of the tag only: the user's copy
	// of an older installer the test also ran is not the newer one's.
	tagOlder, _ := installerpkg.ParseTag("v0.17.0-rc.2")
	tagNewer, _ := installerpkg.ParseTag("v0.17.0-rc.3")
	s := &smoke{t: t, updaterCopy: sameAsOlder, installers: map[string]string{tagOlder.Label: older, tagNewer.Label: newer}}
	if s.leftCopyOf(tagNewer) {
		t.Error("a copy of the older installer counts as the newer one's")
	}
	if !s.leftCopyOf(tagOlder) {
		t.Error("a copy of the older installer is not found as its own")
	}
	s.updaterCopy = write("copy.exe", "installer B")
	if !s.leftCopyOf(tagNewer) {
		t.Error("a copy of the newer installer is not found")
	}
	s.updaterCopy = missing
	if s.leftCopyOf(tagNewer) {
		t.Error("a missing copy counts as left")
	}

	// ran records installers by tag, and leaves out a program that is not one
	// (TestWizardPage starts its own small wizard), even on a bare smoke.
	bare := &smoke{t: t}
	bare.ran(filepath.Join(dir, "page.exe"))
	bare.ran(filepath.Join(dir, "padmate-v0.17.0-rc.3-windows-amd64-setup.exe"))
	if len(bare.installers) != 1 || bare.installers[tagNewer.Label] == "" {
		t.Errorf("ran recorded %v, want only the rc.3 installer", bare.installers)
	}

	// ownCopy: the test's file, unless it was there before the test.
	beforeOlder, err := fileHash(older)
	if err != nil {
		t.Fatal(err)
	}
	beforeOther, err := fileHash(longer)
	if err != nil {
		t.Fatal(err)
	}
	own := []string{older, newer}
	for _, c := range []struct {
		name   string
		path   string
		before *[32]byte
		want   bool
	}{
		{"a copy of an installer of the test, nothing before", sameAsOlder, nil, true},
		{"a copy of an installer of the test, something else before", sameAsOlder, &beforeOther, true},
		{"the same file as before the test", sameAsOlder, &beforeOlder, false},
		{"a file of no installer of the test", longer, nil, false},
		{"no file", missing, nil, false},
		{"a folder", dir, nil, false},
	} {
		if got := ownCopy(c.path, c.before, own); got != c.want {
			t.Errorf("%s: ownCopy = %v, want %v", c.name, got, c.want)
		}
	}
}
