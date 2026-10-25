package profilelib

import (
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// The profile format is frozen as version 1 (PRO-01, PRO-06). Version 0 is
// the format of the previews: those files load, save, copy, restore and
// export as they are, and the library never rewrites a file because it is
// old. Whatever the library writes from nothing is version 1.

func TestDefaultProfileIsCurrentVersion(t *testing.T) {
	if !strings.Contains(DefaultProfileText, `"schemaVersion":1`) {
		t.Fatalf("the default profile is not version 1: %s", DefaultProfileText)
	}
	l := New(t.TempDir())
	if created, err := l.EnsureDefault(); err != nil || !created {
		t.Fatal(created, err)
	}
	data, err := l.Read(DefaultID)
	if err != nil || string(data) != DefaultProfileText {
		t.Fatalf("%q %v", data, err)
	}
	if migrated, ds := profile.Migrate(data); ds.HasErrors() || string(migrated) != string(data) {
		t.Fatalf("the default profile needs a migration: %q %v", migrated, ds)
	}
}

func TestVersionZeroProfilesStayAsTheyAre(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	fixture(t, dir, "old", validProfile)
	doc, err := l.Load("old")
	if err != nil || doc.Profile == nil || doc.Profile.SchemaVersion != profile.CurrentSchema || doc.Name != "画画" {
		t.Fatalf("load: %+v, %v", doc, err)
	}
	entries, err := l.List()
	if err != nil || len(entries) != 1 || entries[0].Problem != "" || entries[0].Name != "画画" {
		t.Fatalf("list: %+v, %v", entries, err)
	}
	if data, err := l.Read("old"); err != nil || string(data) != validProfile || doc.Revision != Revision([]byte(validProfile)) {
		t.Fatalf("Load rewrote the file: %q %v", data, err)
	}

	// A save writes the bytes it is given, whatever their version.
	for _, tc := range []struct{ id, text string }{
		{"saved-v0", validProfile},
		{"saved-v1", migratedProfile},
		{"saved-bom", "\xef\xbb\xbf{\r\n  \"schemaVersion\": 0.0,\r\n  \"name\": \"n\",\r\n  \"layers\": {\"base\": {}}\r\n}\r\n"},
	} {
		saved, err := l.Put(tc.id, "", []byte(tc.text))
		if err != nil || saved.Revision != Revision([]byte(tc.text)) {
			t.Fatalf("put %s: %+v, %v", tc.id, saved, err)
		}
		if got, err := l.Read(tc.id); err != nil || string(got) != tc.text {
			t.Fatalf("put %s changed the bytes: %q %v", tc.id, got, err)
		}
	}

	// A copy is a copy: version 0 stays version 0, version 1 stays version 1.
	for _, id := range []string{"saved-v0", "saved-v1"} {
		revision, err := l.Copy(id, id+"-copy")
		if err != nil {
			t.Fatal(err)
		}
		want, _ := l.Read(id)
		if got, err := l.Read(id + "-copy"); err != nil || string(got) != string(want) || revision != Revision(want) {
			t.Fatalf("copy of %s: %q %v", id, got, err)
		}
	}

	// A version from the future is refused with a message that says what to do.
	_, err = l.Put("future", "", []byte(`{"schemaVersion":2,"name":"n","layers":{"base":{}}}`))
	if err == nil || !strings.Contains(err.Error(), "upgrade padmate") {
		t.Fatalf("version 2: %v", err)
	}
	if _, err := l.Read("future"); err == nil {
		t.Fatal("a refused save wrote a file")
	}
}

// A version 0 snapshot of the history is restored whole.
func TestRestoreVersionZero(t *testing.T) {
	l := New(t.TempDir())
	saved, err := l.Put("paint", "", []byte(validProfile))
	if err != nil {
		t.Fatal(err)
	}
	if saved, err = l.Put("paint", saved.Revision, []byte(migratedProfile+"\n")); err != nil {
		t.Fatal(err)
	}
	snapshots, err := l.History("paint")
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("history: %+v, %v", snapshots, err)
	}
	if _, data, err := l.Restore("paint", snapshots[0].Version, saved.Revision); err != nil || string(data) != validProfile {
		t.Fatalf("restore: %q, %v", data, err)
	}
}
