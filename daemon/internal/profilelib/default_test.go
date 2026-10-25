package profilelib

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// TestEnsureDefaultCreatesOnlyWhenMissing: the fixed default profile is
// written, with no mapping, only where profile.json is missing; an
// existing default folder stays as it is (PRO-28, ADR-0053).
func TestEnsureDefaultCreatesOnlyWhenMissing(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	created, err := l.EnsureDefault()
	if err != nil || !created {
		t.Fatal(created, err)
	}
	data, err := l.Read(DefaultID)
	if err != nil || string(data) != DefaultProfileText {
		t.Fatalf("%q %v", data, err)
	}
	p, ds := profile.Parse(data)
	if ds.Err() != nil || p.Name != "默认" || len(p.Layers) != 1 || len(p.Layers[0].Bindings) != 0 || p.Common != nil || p.Script != nil {
		t.Fatalf("default maps something: %+v %v", p, ds)
	}
	if created, err := l.EnsureDefault(); err != nil || created {
		t.Fatal("created twice", created, err)
	}
	// An existing default, even a broken one, is kept as it is.
	for _, existing := range []string{validProfile, "{"} {
		fixture(t, dir, DefaultID, existing)
		if created, err := l.EnsureDefault(); err != nil || created {
			t.Fatal(created, err)
		}
		if data, err := l.Read(DefaultID); err != nil || string(data) != existing {
			t.Fatalf("existing default replaced: %q %v", data, err)
		}
	}
	// A default folder that holds only scripts gets its profile.json.
	if err := os.Remove(filepath.Join(dir, "profiles", DefaultID, "profile.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profiles", DefaultID, "main.ts"), []byte("//"), 0o600); err != nil {
		t.Fatal(err)
	}
	if created, err := l.EnsureDefault(); err != nil || !created {
		t.Fatal(created, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles", DefaultID, "main.ts")); err != nil {
		t.Fatal("script lost", err)
	}
	entries, err := l.List()
	if err != nil || len(entries) != 1 || !entries[0].Default || entries[0].Name != "默认" {
		t.Fatalf("%+v %v", entries, err)
	}
}

// TestDefaultCannotBeDeletedOrRenamed: Remove refuses the default whatever
// its revision; Put refuses a new name but saves any other edit, and
// repairs a broken default with any name. Other profiles rename freely.
func TestDefaultCannotBeDeletedOrRenamed(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	if _, err := l.EnsureDefault(); err != nil {
		t.Fatal(err)
	}
	revision := Revision([]byte(DefaultProfileText))
	for _, expected := range []string{revision, "", "stale"} {
		var fixed *FixedError
		if err := l.Remove(DefaultID, expected); !errors.As(err, &fixed) || fixed.Op != "delete" || fixed.ID != DefaultID {
			t.Fatalf("revision %q: %v", expected, err)
		}
	}
	if data, err := l.Read(DefaultID); err != nil || string(data) != DefaultProfileText {
		t.Fatal("refused delete changed the default", err)
	}

	renamed := `{"schemaVersion":0,"name":"Mine","layers":{"base":{}}}`
	var fixed *FixedError
	if _, err := l.Put(DefaultID, revision, []byte(renamed)); !errors.As(err, &fixed) || fixed.Op != "rename" {
		t.Fatal(err)
	}
	// A stale revision is a conflict first, as for every profile.
	var conflict *ConflictError
	if _, err := l.Put(DefaultID, "stale", []byte(renamed)); !errors.As(err, &conflict) {
		t.Fatal(err)
	}
	edited := `{"schemaVersion":0,"name":"默认","layers":{"base":{"south":{"press":{"type":"keys","keys":"F13"}}}}}`
	if _, err := l.Put(DefaultID, revision, []byte(edited)); err != nil {
		t.Fatal(err)
	}
	if data, err := l.Read(DefaultID); err != nil || string(data) != edited {
		t.Fatal("edit not saved", err)
	}

	// A default that does not parse has no name to keep.
	fixture(t, dir, DefaultID, "{")
	if _, err := l.Put(DefaultID, Revision([]byte("{")), []byte(renamed)); err != nil {
		t.Fatal("broken default not repairable", err)
	}
	if _, err := l.Put("paint", "", []byte(validProfile)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put("paint", Revision([]byte(validProfile)), []byte(renamed)); err != nil {
		t.Fatal("ordinary profile not renamed", err)
	}
	// A copy of the default is an ordinary profile.
	if _, err := l.Copy(DefaultID, "mine"); err != nil {
		t.Fatal(err)
	}
	if err := l.Remove("mine", Revision([]byte(renamed))); err != nil {
		t.Fatal(err)
	}
}
