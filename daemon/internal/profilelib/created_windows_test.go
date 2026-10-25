package profilelib

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// setCreated sets the creation time of the file or folder path, which
// NTFS keeps apart from the modification time.
func setCreated(t *testing.T, path string, created time.Time) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.FILE_WRITE_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	ft := windows.NsecToFiletime(created.UnixNano())
	if err := windows.SetFileTime(h, &ft, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func macroCreated(t *testing.T, l *Library) map[string]int64 {
	t.Helper()
	entries, err := l.MacroEntries()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, e := range entries {
		out[e.Name] = e.Created.UnixMilli()
	}
	return out
}

func profileCreated(t *testing.T, l *Library) map[string]int64 {
	t.Helper()
	entries, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, e := range entries {
		out[e.ID] = e.Created
	}
	return out
}

// A profile's creation time is its folder's: profile.json is replaced by a
// rename on every save, and a copy or a rename of the profile keeps the
// folder.
func TestProfileCreatedIsTheFolders(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	before := time.Now().Add(-time.Second).UnixMilli()
	fixture(t, dir, "paint", validProfile)
	fixture(t, dir, "photo", validProfile)
	after := time.Now().Add(time.Second).UnixMilli()
	got := profileCreated(t, l)
	for _, id := range []string{"paint", "photo"} {
		if got[id] < before || got[id] > after {
			t.Fatalf("%s created %d, want within [%d, %d]", id, got[id], before, after)
		}
	}
	old := time.Date(2020, 3, 4, 5, 6, 7, 0, time.UTC)
	setCreated(t, filepath.Join(dir, "profiles", "paint"), old)
	if got := profileCreated(t, l)["paint"]; got != old.UnixMilli() {
		t.Fatalf("created %d, want %d", got, old.UnixMilli())
	}
	// Saves replace profile.json and write the history, never the folder.
	saved, err := l.Put("paint", Revision([]byte(validProfile)), []byte(validProfile+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put("paint", saved.Revision, []byte(validProfile+"\n\n")); err != nil {
		t.Fatal(err)
	}
	if got := profileCreated(t, l)["paint"]; got != old.UnixMilli() {
		t.Fatalf("created %d after saves, want %d", got, old.UnixMilli())
	}
	// A copy is a new folder.
	if _, err := l.Copy("paint", "paint-2"); err != nil {
		t.Fatal(err)
	}
	copied := profileCreated(t, l)["paint-2"]
	if copied < before || copied > time.Now().Add(time.Second).UnixMilli() || copied == old.UnixMilli() {
		t.Fatalf("copy created %d", copied)
	}
}

// An import that replaces a profile keeps its creation time: the new
// folder is renamed over the old one, and the page lists by creation, so
// the replaced profile would otherwise jump to the end.
func TestProfileCreatedSurvivesReplace(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	fixture(t, dir, "paint", validProfile)
	fixture(t, dir, "other", profileNamed("other"))
	old := time.Date(2020, 3, 4, 5, 6, 7, 0, time.UTC)
	setCreated(t, filepath.Join(dir, "profiles", "paint"), old)
	incoming := strings.Replace(validProfile, "F13", "F15", 1)
	a := testArchive(t, incoming, map[string]string{"main.ts": "export const v = 2;\n"})
	if imported, err := l.Import(a, ImportOptions{Replace: "paint"}); err != nil || imported.ID != "paint" {
		t.Fatalf("%+v %v", imported, err)
	}
	if got := profileCreated(t, l)["paint"]; got != old.UnixMilli() {
		t.Fatalf("created %d after the replace, want %d", got, old.UnixMilli())
	}
	// An import that is new starts now.
	before := time.Now().Add(-time.Second).UnixMilli()
	imported, err := l.Import(testArchive(t, profileNamed("third"), nil), ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := profileCreated(t, l)[imported.ID]; got < before || got > time.Now().Add(time.Second).UnixMilli() {
		t.Fatalf("new import created %d", got)
	}
}

// A macro's creation time is its file's, and survives a rename and an
// import that replaces its text; a new macro starts now.
func TestMacroCreatedSurvivesRenameAndReplace(t *testing.T) {
	dir := t.TempDir()
	fixture(t, dir, "paint", validProfile)
	putMacroFiles(t, dir, map[string]string{"a.ts": "a", "b.ts": "b"})
	l := New(dir)
	old := time.Date(2021, 5, 6, 7, 8, 9, 0, time.UTC)
	older := old.Add(-48 * time.Hour)
	setCreated(t, filepath.Join(l.MacroDir(), "a.ts"), old)
	setCreated(t, filepath.Join(l.MacroDir(), "b.ts"), older)
	got := macroCreated(t, l)
	if got["a.ts"] != old.UnixMilli() || got["b.ts"] != older.UnixMilli() {
		t.Fatalf("created %v", got)
	}

	if _, err := l.RenameMacro("a.ts", "c.ts"); err != nil {
		t.Fatal(err)
	}
	got = macroCreated(t, l)
	if _, ok := got["a.ts"]; ok || got["c.ts"] != old.UnixMilli() {
		t.Fatalf("after the rename %v, want c.ts %d", got, old.UnixMilli())
	}

	if _, replaced, err := l.ImportMacro("c.ts", []byte("new text"), true, nil); err != nil || !replaced {
		t.Fatal(replaced, err)
	}
	if got := macroCreated(t, l)["c.ts"]; got != old.UnixMilli() {
		t.Fatalf("after the replace %d, want %d", got, old.UnixMilli())
	}
	if readMacro(t, dir, "c.ts") != "new text" {
		t.Fatal("not replaced")
	}

	before := time.Now().Add(-time.Second).UnixMilli()
	if err := l.CreateMacro("d.ts", []byte("d"), nil); err != nil {
		t.Fatal(err)
	}
	if got := macroCreated(t, l)["d.ts"]; got < before || got > time.Now().Add(time.Second).UnixMilli() {
		t.Fatalf("new macro created %d", got)
	}
}

// The editor files are not macros and have no entry.
func TestMacroEntriesSkipEditorFiles(t *testing.T) {
	dir := t.TempDir()
	putMacroFiles(t, dir, map[string]string{"a.ts": "a", "x.d.ts": "x", "tsconfig.json": "{}"})
	entries, err := New(dir).MacroEntries()
	if err != nil || len(entries) != 1 || entries[0].Name != "a.ts" || entries[0].Created.IsZero() {
		t.Fatalf("%+v, %v", entries, err)
	}
}
