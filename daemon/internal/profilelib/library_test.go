package profilelib

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// validProfile is a version 0 file, as the previews wrote it; Load migrates it
// in memory to migratedProfile and never rewrites the file.
const validProfile = `{"schemaVersion":0,"name":"画画","layers":{"base":{"south":{"press":{"type":"keys","keys":"F13"}}}}}`

// migratedProfile is validProfile at the current version.
const migratedProfile = `{"schemaVersion":1,"name":"画画","layers":{"base":{"south":{"press":{"type":"keys","keys":"F13"}}}}}`

func fixture(t *testing.T, root, id, data string) {
	t.Helper()
	dir := filepath.Join(root, "profiles", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profile.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLibraryRead(t *testing.T) {
	dir := t.TempDir()
	lib := New(dir)
	entries, err := lib.List()
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty list: %v, %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created directory")
	}
	fixture(t, dir, "zebra", validProfile)
	fixture(t, dir, "paint", validProfile)
	fixture(t, dir, "broken", `{"schemaVersion":0,"layers":null}`)
	fixture(t, dir, "future", `{"schemaVersion":2}`)
	fixture(t, dir, ".hidden", validProfile)
	entries, err = lib.List()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.ID)
		if e.ID == "broken" || e.ID == "future" {
			if e.Problem == "" {
				t.Fatalf("bad entry looks valid: %+v", e)
			}
		} else if e.Name != "画画" || e.Problem != "" || e.Revision == "" {
			t.Fatalf("bad entry: %+v", e)
		}
	}
	if !slices.Equal(ids, []string{"broken", "future", "paint", "zebra"}) {
		t.Fatal(ids)
	}
	doc, err := lib.Load("paint")
	if err != nil || doc.Profile == nil || doc.Profile.Name != "画画" {
		t.Fatalf("load: %+v, %v", doc, err)
	}
	if doc.Path != filepath.Join(dir, "profiles", "paint", "profile.json") || !bytes.Equal(doc.Data, []byte(migratedProfile)) {
		t.Fatalf("document: %+v", doc)
	}
	first := doc.Revision
	fixture(t, dir, "paint", validProfile+"\n")
	doc, err = lib.Load("paint")
	if err != nil || doc.Revision == first {
		t.Fatal("revision did not track external edit", err)
	}
	for _, id := range []string{"missing", "broken", "future"} {
		if _, err := lib.Load(id); err == nil {
			t.Fatalf("loaded %q", id)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "profiles", "future", "profile.json"))
	if err != nil || string(data) != `{"schemaVersion":2}` {
		t.Fatal("read overwrote original", err)
	}
}

func TestLibraryIDs(t *testing.T) {
	for _, id := range []string{"", ".", "..", "../paint", `..\paint`, "a/b", `a\b`, "C:paint", "a.json", "Paint", "nul", "com1", "lpt9", "a ", "a:", "a*"} {
		if ValidID(id) {
			t.Errorf("accepted unsafe/noncanonical id %q", id)
		}
		if _, err := New(t.TempDir()).Load(id); err == nil {
			t.Errorf("loaded %q", id)
		}
	}
	for _, id := range []string{"paint", "paint-2", "joycon_left", "0", "console"} {
		if !ValidID(id) {
			t.Errorf("rejected %q", id)
		}
	}
}

func TestLibraryConfinesLinks(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	fixture(t, outside, "secret", validProfile)
	if err := os.Mkdir(filepath.Join(dir, "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "profiles", "secret"), filepath.Join(dir, "profiles", "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := New(dir).Load("escape"); err == nil {
		t.Fatal("followed link outside library")
	}
}
