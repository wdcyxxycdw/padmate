package profilelib

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A file another program holds open without FILE_SHARE_DELETE, as os.Open
// does, cannot be deleted. Remove still takes the profile out of the
// library and reports the remains; the next Remove deletes them.
func TestRemoveIncompleteHidesRemains(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	fixture(t, dir, "paint", validProfile)
	fixture(t, dir, "other", validProfile)
	writeFile(t, filepath.Join(dir, "profiles", "paint", "main.ts"), "export {}\n")
	var held *os.File
	l.SetRemoveHook(func(tmp string) {
		f, err := os.Open(filepath.Join(dir, "profiles", tmp, "main.ts"))
		if err != nil {
			t.Error(err)
			return
		}
		held = f
	})
	t.Cleanup(func() {
		if held != nil {
			_ = held.Close()
		}
	})
	err := l.Remove("paint", Revision([]byte(validProfile)))
	var incomplete *IncompleteRemoveError
	if !errors.As(err, &incomplete) || incomplete.ID != "paint" {
		t.Fatal(err)
	}
	if ids, err := l.IDs(); err != nil || !slices.Equal(ids, []string{"other"}) {
		t.Fatal("remains visible in library", ids, err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	held = nil
	l.SetRemoveHook(nil)
	if err := l.Remove("other", Revision([]byte(validProfile))); err != nil {
		t.Fatal(err)
	}
	assertNoCopyLeftovers(t, dir)
}
