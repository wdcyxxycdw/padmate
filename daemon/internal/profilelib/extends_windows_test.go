package profilelib

import (
	"os"
	"path/filepath"
	"testing"
)

// Windows refuses to rename a folder while a program holds a file in it open.
// The children were written whole before, and are put back as they were.
func TestRemoveUndoesFlatteningWhenTheFolderStaysPut(t *testing.T) {
	l, dir := extLibrary(t)
	writeFile(t, filepath.Join(dir, "profiles", "paint", "main.ts"), "export {}\n")
	left, _ := l.Read("left")
	small, _ := l.Read("small")
	held, err := os.Open(filepath.Join(dir, "profiles", "paint", "main.ts"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	// Again and again: a delete that fails keeps nothing in the history.
	for attempt := range 3 {
		if err := l.Remove("paint", Revision([]byte(extParent))); err == nil {
			t.Fatal("the folder was renamed while a file in it was open")
		}
		if _, err := l.Read("paint"); err != nil {
			t.Error("the parent is gone", err)
		}
		for id, want := range map[string][]byte{"left": left, "small": small} {
			if got, err := l.Read(id); err != nil || string(got) != string(want) {
				t.Errorf("%s was left written whole:\n%s\n%v", id, got, err)
			}
			if snapshots := history(t, l, id); len(snapshots) != 0 {
				t.Errorf("attempt %d: %s has %d snapshots of a delete that failed", attempt, id, len(snapshots))
			}
		}
	}
	assertNoCopyLeftovers(t, dir, "paint", "left", "small")
}
