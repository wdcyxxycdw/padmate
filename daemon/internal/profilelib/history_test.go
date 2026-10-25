package profilelib

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// namedProfile is a valid profile named name.
func namedProfile(name string) string {
	return fmt.Sprintf(`{"schemaVersion":0,"name":%q,"layers":{"base":{}}}`, name)
}

// history lists id's snapshots and fails the test on an error.
func history(t *testing.T, l *Library, id string) []Snapshot {
	t.Helper()
	snapshots, err := l.History(id)
	if err != nil {
		t.Fatal(err)
	}
	return snapshots
}

// putAll saves each text in turn over profiles/<id> and returns the last
// revision.
func putAll(t *testing.T, l *Library, id, revision string, texts ...string) string {
	t.Helper()
	for _, text := range texts {
		saved, err := l.Put(id, revision, []byte(text))
		if err != nil || saved.HistoryErr != nil {
			t.Fatal(text, err, saved.HistoryErr)
		}
		revision = saved.Revision
	}
	return revision
}

func historyFiles(t *testing.T, dir, id string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "profiles", id, HistoryDir))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestPutKeepsReplacedVersion(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	first := putAll(t, l, "paint", "", namedProfile("v1"))
	// Creating a profile has nothing to keep.
	if _, err := os.Stat(filepath.Join(dir, "profiles", "paint", HistoryDir)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("history created for a new profile", err)
	}
	if got := history(t, l, "paint"); len(got) != 0 {
		t.Fatal(got)
	}
	// SavedAt is when v1 was saved, not when v2 replaced it.
	savedAt := time.Date(2026, 10, 7, 15, 30, 12, 0, time.Local)
	if err := os.Chtimes(filepath.Join(dir, "profiles", "paint", "profile.json"), savedAt, savedAt); err != nil {
		t.Fatal(err)
	}
	second := putAll(t, l, "paint", first, namedProfile("v2"))
	got := history(t, l, "paint")
	if len(got) != 1 || got[0].Name != "v1" || got[0].Bytes != int64(len(namedProfile("v1"))) || !got[0].SavedAt.Equal(savedAt) || !ValidVersion(got[0].Version) {
		t.Fatalf("history after one save: %+v", got)
	}
	data, err := os.ReadFile(filepath.Join(dir, "profiles", "paint", HistoryDir, got[0].Version+".json"))
	if err != nil || string(data) != namedProfile("v1") {
		t.Fatal("snapshot bytes", string(data), err)
	}
	if !strings.HasSuffix(got[0].Version, Revision(data)[:8]) {
		t.Fatal("version does not end in the content hash", got[0].Version)
	}
	// Saving the same bytes again changes nothing to keep.
	if putAll(t, l, "paint", second, namedProfile("v2")) != second || len(history(t, l, "paint")) != 1 {
		t.Fatal("an unchanged save kept a snapshot")
	}
	// Newest first, and names never sort backwards within one second.
	putAll(t, l, "paint", second, namedProfile("v3"), namedProfile("v4"))
	got = history(t, l, "paint")
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"v3", "v2", "v1"}) {
		t.Fatal(names)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Version <= got[i].Version {
			t.Fatalf("versions out of order: %+v", got)
		}
	}
	// A broken file is kept too, without a name.
	fixture(t, dir, "paint", "{")
	putAll(t, l, "paint", Revision([]byte("{")), namedProfile("v5"))
	got = history(t, l, "paint")
	if got[0].Name != "" || got[0].Bytes != 1 {
		t.Fatalf("broken snapshot: %+v", got[0])
	}
}

func TestPutSkipsSnapshotEqualToNewest(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	putAll(t, l, "paint", "", namedProfile("a"), namedProfile("b"))
	// An external editor puts a back; the next save would keep a again.
	fixture(t, dir, "paint", namedProfile("a"))
	putAll(t, l, "paint", Revision([]byte(namedProfile("a"))), namedProfile("c"))
	got := history(t, l, "paint")
	if len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("history kept a twice: %+v", got)
	}
}

func TestPutPrunesOldestSnapshots(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	revision := putAll(t, l, "paint", "", namedProfile("v0"))
	// Files that are not snapshots are neither listed nor deleted.
	other := filepath.Join(dir, "profiles", "paint", HistoryDir)
	writeFile(t, filepath.Join(other, "notes.txt"), "mine")
	writeFile(t, filepath.Join(other, ".padmate-leftover.tmp"), "{")
	for n := 1; n <= MaxSnapshots+2; n++ {
		revision = putAll(t, l, "paint", revision, namedProfile(fmt.Sprintf("v%d", n)))
	}
	got := history(t, l, "paint")
	if len(got) != MaxSnapshots || got[0].Name != fmt.Sprintf("v%d", MaxSnapshots+1) || got[MaxSnapshots-1].Name != "v2" {
		t.Fatalf("kept %d snapshots, newest %q, oldest %q", len(got), got[0].Name, got[len(got)-1].Name)
	}
	files := historyFiles(t, dir, "paint")
	if len(files) != MaxSnapshots+2 || !slices.Contains(files, "notes.txt") || !slices.Contains(files, ".padmate-leftover.tmp") {
		t.Fatal(files)
	}
}

func TestSnapshotFailureKeepsTheSave(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	first := putAll(t, l, "paint", "", namedProfile("v1"))
	writeFile(t, filepath.Join(dir, "profiles", "paint", HistoryDir), "not a folder")
	saved, err := l.Put("paint", first, []byte(namedProfile("v2")))
	var unsupported *UnsupportedError
	if err != nil || !errors.As(saved.HistoryErr, &unsupported) || saved.Revision != Revision([]byte(namedProfile("v2"))) {
		t.Fatal(saved, err)
	}
	if data, err := l.Read("paint"); err != nil || string(data) != namedProfile("v2") {
		t.Fatal("save failed with its snapshot", string(data), err)
	}
	if _, err := l.History("paint"); !errors.As(err, &unsupported) {
		t.Fatal("listed a file as the history folder", err)
	}
}

func TestHistoryConfinesLinks(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	l := New(dir)
	first := putAll(t, l, "paint", "", namedProfile("v1"))
	if err := os.Symlink(outside, filepath.Join(dir, "profiles", "paint", HistoryDir)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	saved, err := l.Put("paint", first, []byte(namedProfile("v2")))
	if err != nil || saved.HistoryErr == nil {
		t.Fatal("snapshot through a link", saved, err)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatal("wrote outside the profile", entries, err)
	}
	var unsupported *UnsupportedError
	if _, err := l.History("paint"); !errors.As(err, &unsupported) {
		t.Fatal(err)
	}
	if _, _, err := l.Restore("paint", "20261007-153012-0123abcd", saved.Revision); !errors.As(err, &unsupported) {
		t.Fatal(err)
	}
}

func TestHistoryLeftOutOfListAndCopy(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	putAll(t, l, "paint", "", namedProfile("v1"), namedProfile("v2"))
	writeFile(t, filepath.Join(dir, "profiles", "paint", "main.ts"), "export {};\n")
	if ids, err := l.IDs(); err != nil || !slices.Equal(ids, []string{"paint"}) {
		t.Fatal(ids, err)
	}
	if entries, err := l.List(); err != nil || len(entries) != 1 || entries[0].Name != "v2" {
		t.Fatal(entries, err)
	}
	// A link in the history would refuse the copy if it were copied.
	if err := os.Symlink(dir, filepath.Join(dir, "profiles", "paint", HistoryDir, "link")); err != nil {
		t.Logf("symlinks unavailable: %v", err)
	}
	if _, err := l.Copy("paint", "paint-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "profiles", "paint-2", HistoryDir)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("history copied", err)
	}
	if got := history(t, l, "paint-2"); len(got) != 0 {
		t.Fatal(got)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "profiles", "paint-2", "main.ts")); err != nil || string(data) != "export {};\n" {
		t.Fatal("script not copied", err)
	}
	// Deleting a profile deletes its history with it.
	entries, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Remove("paint", entries[0].Revision); err != nil {
		t.Fatal(err)
	}
	assertNoCopyLeftovers(t, dir, "paint-2")
	if _, err := l.History("paint"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("history of a deleted profile", err)
	}
}

// TestHistoryFolderCase checks that a history folder whose name differs
// from HistoryDir only in case is history to Copy as well. Windows finds it
// as HistoryDir, so saves keep snapshots there and History lists them; a
// copy that took it would start with the source's history and count it
// against MaxCopyBytes.
func TestHistoryFolderCase(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	revision := putAll(t, l, "paint", "", namedProfile("v1"), namedProfile("v2"))
	folder := filepath.Join(dir, "profiles", "paint")
	if err := os.Rename(filepath.Join(folder, HistoryDir), filepath.Join(folder, ".History")); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		putAll(t, l, "paint", revision, namedProfile("v3"))
		if got := history(t, l, "paint"); len(got) != 2 || got[0].Name != "v2" || got[1].Name != "v1" {
			t.Fatalf("history = %+v, want v2 and v1", got)
		}
		if got := historyFiles(t, dir, "paint"); len(got) != 2 {
			t.Fatalf(".History holds %q, want both snapshots", got)
		}
	}
	if _, err := l.Copy("paint", "paint-2"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "profiles", "paint-2"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"profile.json"}) {
		t.Fatalf("copy holds %q, want only profile.json", names)
	}
}

func TestRestore(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	current := putAll(t, l, "paint", "", namedProfile("v1"), namedProfile("v2"))
	v1 := history(t, l, "paint")[0].Version
	// The current file must still have the revision the client read.
	var conflict *ConflictError
	if _, _, err := l.Restore("paint", v1, "stale"); !errors.As(err, &conflict) || conflict.Current != current {
		t.Fatal(err)
	}
	saved, data, err := l.Restore("paint", v1, current)
	if err != nil || saved.HistoryErr != nil || string(data) != namedProfile("v1") || saved.Revision != Revision(data) {
		t.Fatal(saved, string(data), err)
	}
	if got, err := l.Read("paint"); err != nil || string(got) != namedProfile("v1") {
		t.Fatal(string(got), err)
	}
	// The replaced version is kept first, and the restored one stays.
	got := history(t, l, "paint")
	if len(got) != 2 || got[0].Name != "v2" || got[1].Version != v1 {
		t.Fatalf("history after restore: %+v", got)
	}
	// A snapshot that fails validation is refused like an invalid put.
	bad := "20261007-153012-0123abcd"
	writeFile(t, filepath.Join(dir, "profiles", "paint", HistoryDir, bad+".json"), `{"schemaVersion":0,"layers":null}`)
	var invalid *profile.Error
	if _, _, err := l.Restore("paint", bad, saved.Revision); !errors.As(err, &invalid) || len(invalid.Diagnostics) == 0 {
		t.Fatal("restored an invalid snapshot", err)
	}
	if _, _, err := l.Restore("paint", "20261007-153013-0123abcd", saved.Revision); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("missing snapshot", err)
	}
	if _, _, err := l.Restore("missing", v1, ""); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("missing profile", err)
	}
	if got, err := l.Read("paint"); err != nil || string(got) != namedProfile("v1") {
		t.Fatal("refused restore changed the profile", string(got), err)
	}
	// A folder whose profile.json is gone can be restored with "".
	if err := os.Remove(filepath.Join(dir, "profiles", "paint", "profile.json")); err != nil {
		t.Fatal(err)
	}
	if _, data, err := l.Restore("paint", v1, ""); err != nil || string(data) != namedProfile("v1") {
		t.Fatal(err)
	}
}

func TestSnapshotVersions(t *testing.T) {
	for _, version := range []string{"20261007-153012-0123abcd", "19991231-235959-ffffffff"} {
		if !ValidVersion(version) {
			t.Errorf("rejected %q", version)
		}
	}
	l := New(t.TempDir())
	putAll(t, l, "paint", "", namedProfile("v1"), namedProfile("v2"))
	for _, version := range []string{
		"", "20261007-153012-0123abcd.json", "20261007-153012-0123ABCD", "20261007-153012-0123abc",
		"20261307-153012-0123abcd", "20261007-253012-0123abcd", "../paint/profile", `..\20261007-153012-0123abcd`,
		"x/20261007-153012-0123abcd", "20261007-153012-0123abcd/..", " 20261007-153012-0123abcd", "notes",
	} {
		if ValidVersion(version) {
			t.Errorf("accepted %q", version)
		}
		if _, _, err := l.Restore("paint", version, ""); err == nil || errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Restore(%q) = %v, want an invalid version", version, err)
		}
	}
	for _, id := range []string{"../paint", "Paint", "nul", ""} {
		if _, err := l.History(id); err == nil {
			t.Errorf("History(%q) accepted", id)
		}
	}
}
