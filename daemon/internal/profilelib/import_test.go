package profilelib

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportNames(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	if _, err := l.EnsureDefault(); err != nil {
		t.Fatal(err)
	}
	fixture(t, dir, "paint", validProfile)
	fixture(t, dir, "nameless", "{")
	fixture(t, dir, "accent", profileNamed("\xc3\xa9"))
	// Names compare trimmed, in NFC (here e and U+0301) and ignoring case;
	// the default goes by both of its names, a profile without a readable
	// name by its ID.
	for _, tc := range []struct{ name, want string }{{"画画", "paint"}, {" 画画 ", "paint"}, {"默认", DefaultID}, {"DEFAULT", DefaultID}, {"nameless", "nameless"}, {"E\xcc\x81", "accent"}} {
		_, err := l.Import(testArchive(t, profileNamed(tc.name), nil), ImportOptions{})
		var taken *NameTakenError
		if !errors.As(err, &taken) || taken.ID != tc.want || taken.Name != strings.TrimSpace(tc.name) {
			t.Fatalf("%q: %v", tc.name, err)
		}
	}
	a := testArchive(t, validProfile, map[string]string{"main.ts": "export {};\n"})
	imported, err := l.Import(a, ImportOptions{Name: " 画画 2 "})
	if err != nil || imported.ID != "2" || imported.Name != "画画 2" {
		t.Fatalf("%+v %v", imported, err)
	}
	doc, err := l.Load("2")
	if err != nil || doc.Name != "画画 2" || doc.Revision != imported.Revision || string(imported.Data) != strings.Replace(validProfile, `"画画"`, `"画画 2"`, 1) {
		t.Fatal(doc, err, string(imported.Data))
	}
	// The same name again, and an ID that Windows would see as taken.
	if _, err := l.Import(a, ImportOptions{Name: "画画 2"}); !errors.As(err, new(*NameTakenError)) {
		t.Fatal(err)
	}
	imported, err = l.Import(testArchive(t, profileNamed("Paint"), nil), ImportOptions{})
	if err != nil || imported.ID != "paint-2" {
		t.Fatalf("%+v %v", imported, err)
	}
	for _, opts := range []ImportOptions{{Name: "  "}, {Name: strings.Repeat("名", MaxNameRunes+1)}, {Name: "x", Replace: "paint"}, {Replace: "../paint"}} {
		if _, err := l.Import(a, opts); err == nil {
			t.Fatalf("accepted %+v", opts)
		}
	}
	assertNoCopyLeftovers(t, dir, "2", "accent", DefaultID, "nameless", "paint", "paint-2")
}

func TestImportReplace(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	fixture(t, dir, "paint", validProfile)
	fixture(t, dir, "other", profileNamed("other"))
	writeFile(t, filepath.Join(dir, "profiles", "paint", "old.ts"), "export {};\n")
	edited := strings.Replace(validProfile, "F13", "F14", 1)
	if _, err := l.Put("paint", Revision([]byte(validProfile)), []byte(edited)); err != nil {
		t.Fatal(err)
	}
	before, err := l.History("paint")
	if err != nil || len(before) != 1 {
		t.Fatal(before, err)
	}
	incoming := strings.Replace(validProfile, "F13", "F15", 1)
	a := testArchive(t, incoming, map[string]string{"main.ts": "export const v = 2;\n"})
	imported, err := l.Import(a, ImportOptions{Replace: "paint"})
	if err != nil || imported.ID != "paint" || imported.Revision != Revision([]byte(incoming)) || imported.HistoryErr != nil || imported.CleanupErr != nil {
		t.Fatalf("%+v %v", imported, err)
	}
	if data, err := l.Read("paint"); err != nil || string(data) != incoming {
		t.Fatal(string(data), err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "profiles", "paint", "main.ts")); err != nil || string(data) != "export const v = 2;\n" {
		t.Fatal(string(data), err)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles", "paint", "old.ts")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("old script kept", err)
	}
	// The history stays and keeps the replaced profile.json.
	after, err := l.History("paint")
	if err != nil || len(after) != 2 || after[1].Version != before[0].Version {
		t.Fatal(after, err)
	}
	if saved, data, err := l.Restore("paint", after[0].Version, imported.Revision); err != nil || string(data) != edited {
		t.Fatal(saved, string(data), err)
	}
	assertNoCopyLeftovers(t, dir, "other", "paint")

	// A name another profile goes by, a missing profile.
	if _, err := l.Import(testArchive(t, profileNamed("other"), nil), ImportOptions{Replace: "paint"}); !errors.As(err, new(*NameTakenError)) {
		t.Fatal(err)
	}
	if data, err := l.Read("paint"); err != nil || string(data) != edited {
		t.Fatal("refused import changed the profile", string(data), err)
	}
	if _, err := l.Import(a, ImportOptions{Replace: "missing"}); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	// Its own name is no clash.
	if _, err := l.Import(testArchive(t, profileNamed("other"), nil), ImportOptions{Replace: "other"}); err != nil {
		t.Fatal(err)
	}
	assertNoCopyLeftovers(t, dir, "other", "paint")
}

// Replacing the fixed default keeps its stored name, whatever the
// archive's name is.
func TestImportReplaceDefault(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	if _, err := l.EnsureDefault(); err != nil {
		t.Fatal(err)
	}
	fixture(t, dir, "paint", validProfile)
	a := testArchive(t, validProfile, map[string]string{"main.ts": "export {};\n"})
	imported, err := l.Import(a, ImportOptions{Replace: DefaultID})
	if err != nil || imported.ID != DefaultID || imported.Name != "默认" {
		t.Fatalf("%+v %v", imported, err)
	}
	doc, err := l.Load(DefaultID)
	if err != nil || doc.Name != "默认" || doc.Revision != imported.Revision || !strings.Contains(string(imported.Data), "F13") {
		t.Fatal(doc, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles", DefaultID, "main.ts")); err != nil {
		t.Fatal(err)
	}
	// A default that does not parse has no name to keep.
	if err := os.WriteFile(filepath.Join(dir, "profiles", DefaultID, "profile.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if imported, err := l.Import(a, ImportOptions{Replace: DefaultID}); err != nil || imported.Name != "画画" {
		t.Fatalf("%+v %v", imported, err)
	}
	assertNoCopyLeftovers(t, dir, DefaultID, "paint")
}

// An import cut off between its two renames leaves the replaced folder
// under a hidden name; the next library change, or the next start, renames
// it back. A replaced folder left beside its profile is deleted.
func TestImportCutOffRestored(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	fixture(t, dir, ".old-paint-0123456789ab", validProfile)
	fixture(t, dir, ".tmp-import-0123456789ab", profileNamed("half"))
	fixture(t, dir, "other", profileNamed("other"))
	fixture(t, dir, ".old-other-0123456789ab", validProfile)
	fixture(t, dir, ".old-x", validProfile) // not a name Import gives; kept
	if _, err := l.Copy("other", "copy"); err != nil {
		t.Fatal(err)
	}
	if data, err := l.Read("paint"); err != nil || string(data) != validProfile {
		t.Fatal(string(data), err)
	}
	if data, err := l.Read("other"); err != nil || string(data) != profileNamed("other") {
		t.Fatal(string(data), err)
	}
	assertNoCopyLeftovers(t, dir, ".old-x", "copy", "other", "paint")

	// At start, before the default is written again.
	fixture(t, dir, ".old-default-0123456789ab", profileNamed("mine"))
	if created, err := l.EnsureDefault(); err != nil || created {
		t.Fatal(created, err)
	}
	if doc, err := l.Load(DefaultID); err != nil || doc.Name != "mine" {
		t.Fatal(doc, err)
	}

	// A replaced folder that its import could not delete never comes back
	// as a profile deleted since.
	fixture(t, dir, ".old-paint-ba9876543210", profileNamed("stale"))
	if err := l.Remove("paint", Revision([]byte(validProfile))); err != nil {
		t.Fatal(err)
	}
	if _, err := l.EnsureDefault(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Read("paint"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("deleted profile restored", err)
	}
	assertNoCopyLeftovers(t, dir, ".old-x", "copy", DefaultID, "other")
}

func TestNewID(t *testing.T) {
	taken := func(ids ...string) func(string) bool {
		return func(id string) bool {
			for _, t := range ids {
				if t == id {
					return true
				}
			}
			return false
		}
	}
	long := strings.Repeat("a", 70)
	for _, tc := range []struct {
		name  string
		taken []string
		want  string
	}{
		{"Painting", nil, "painting"},
		{"画画", nil, "profile"},
		{"Café Noir!", nil, "cafe-noir"},
		{"--A  b__c--", nil, "a-b-c"},
		{"Painting", []string{"painting", "painting-2"}, "painting-3"},
		{"Default", nil, "default-2"},
		{"CON", nil, "con-2"},
		{"ｐａｉｎｔ", nil, "paint"},
		{long, nil, long[:64]},
		{long, []string{long[:64]}, long[:62] + "-2"},
		{"a" + strings.Repeat("-", 70) + "b", []string{"a"}, "a-b"},
		{"profile", []string{"profile"}, "profile-2"},
	} {
		if got := NewID(tc.name, taken(tc.taken...)); got != tc.want || !ValidID(got) {
			t.Errorf("NewID(%q, %v) = %q, want %q", tc.name, tc.taken, got, tc.want)
		}
	}
}

func TestSetName(t *testing.T) {
	const bom = "\xef\xbb\xbf"
	for _, tc := range []struct{ in, name, want string }{
		{`{"schemaVersion":0,"name":"a","layers":{"base":{}}}`, "b", `{"schemaVersion":0,"name":"b","layers":{"base":{}}}`},
		{bom + "{\n  \"schemaVersion\": 0,\n  \"name\" :\t\"a\\\"\",\n  \"layers\": {\"base\": {}}\n}\n", `<x> "y"`, bom + "{\n  \"schemaVersion\": 0,\n  \"name\" :\t\"<x> \\\"y\\\"\",\n  \"layers\": {\"base\": {}}\n}\n"},
		{`{"layers":{"base":{}},"name":"a","schemaVersion":0}`, "画画", `{"layers":{"base":{}},"name":"画画","schemaVersion":0}`},
	} {
		got, err := setName([]byte(tc.in), tc.name)
		if err != nil || string(got) != tc.want {
			t.Errorf("setName(%q, %q) = %q, %v", tc.in, tc.name, got, err)
		}
	}
}

// An archive's own name must be one a client could choose; it is stored
// trimmed.
func TestImportArchiveName(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	read := func(name string) *Archive {
		t.Helper()
		a, err := ReadArchive(makeZip(t, zipEntry{name: ArchiveManifest, data: testManifest}, zipEntry{name: "profile/profile.json", data: profileNamed(name)}))
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	for _, name := range []string{" ", strings.Repeat("名", MaxNameRunes+1)} {
		_, err := l.Import(read(name), ImportOptions{})
		var archiveErr *ArchiveError
		if !errors.As(err, &archiveErr) || archiveErr.Reason != ArchiveProfileReason || len(archiveErr.Diagnostics) != 1 || archiveErr.Diagnostics[0].Pointer != "/name" {
			t.Fatalf("%q: %v", name, err)
		}
	}
	// Twice the same blank name would otherwise give two profiles of it.
	if entries, err := l.List(); err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
	imported, err := l.Import(read(" "), ImportOptions{Name: "Given"})
	if err != nil || imported.Name != "Given" {
		t.Fatalf("%+v %v", imported, err)
	}
	imported, err = l.Import(read("  画画  "), ImportOptions{})
	if err != nil || imported.Name != "画画" || !strings.Contains(string(imported.Data), `"name":"画画"`) {
		t.Fatalf("%+v %v %s", imported, err, imported.Data)
	}
	if _, err := l.Import(read("画画"), ImportOptions{}); !errors.As(err, new(*NameTakenError)) {
		t.Fatal(err)
	}
}

// A name the file system already has for another item is an ArchiveError
// naming the item, not an unclassified failure.
func TestImportClashError(t *testing.T) {
	err := clashError(fmt.Errorf("write: %w", &fs.PathError{Op: "open", Path: filepath.Join("lib", "abcdef~1.ts"), Err: fs.ErrExist}))
	var archiveErr *ArchiveError
	if !errors.As(err, &archiveErr) || archiveErr.Reason != ArchiveEntryReason || archiveErr.Path != "profile/lib/abcdef~1.ts" {
		t.Fatal(err)
	}
	other := errors.New("disk full")
	if !errors.Is(clashError(other), other) || clashError(nil) != nil {
		t.Fatal("changed another error")
	}
}
