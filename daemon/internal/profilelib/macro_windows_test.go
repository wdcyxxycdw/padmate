package profilelib

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// A junction as the library's macros folder, as its .padmate folder, or
// as a profile's macros folder from before the library, is never listed,
// written, migrated or followed.
func TestMacroWritesNeverFollowJunctions(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	fixture(t, dir, "paint", validProfile)
	if err := os.WriteFile(filepath.Join(outside, "a.ts"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	mklinkJunction(t, filepath.Join(dir, profile.MacroDir), outside)
	l := New(dir)
	var unsupported *UnsupportedError
	if _, err := l.Macros(); !errors.As(err, &unsupported) {
		t.Fatal(err)
	}
	if _, _, err := l.ImportMacro("a.ts", []byte("x"), true, nil); !errors.As(err, &unsupported) {
		t.Fatal(err)
	}
	// A profile's macros wait while the library's folder is a junction.
	putProfileMacros(t, dir, "paint", map[string]string{"b.ts": "b"})
	if m, err := l.MigrateMacros(); err != nil || len(m.Errs) != 1 || !errors.As(m.Errs[0], &unsupported) || len(m.Moved) != 0 {
		t.Fatalf("%+v, %v", m, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles", "paint", profile.MacroDir, "b.ts")); err != nil {
		t.Fatal("moved through a junction:", err)
	}
	if err := os.Remove(filepath.Join(dir, profile.MacroDir)); err != nil {
		t.Fatal(err)
	}
	putMacroFiles(t, dir, map[string]string{"a.ts": "a"})
	mklinkJunction(t, filepath.Join(dir, profile.MacroDir, ".padmate"), outside)
	types := []MacroFile{{Name: ".padmate/a.ts", Data: []byte("x")}}
	if _, err := l.MacroPath("a.ts", types); !errors.As(err, &unsupported) {
		t.Fatal(err)
	}
	// A profile's macros folder that is a junction stays as it is.
	fixture(t, dir, "photo", validProfile)
	mklinkJunction(t, filepath.Join(dir, "profiles", "photo", profile.MacroDir), outside)
	m, err := l.MigrateMacros()
	if err != nil || !slices.Contains(m.Left, "photo/macros") {
		t.Fatalf("%+v, %v", m, err)
	}
	if data, err := os.ReadFile(filepath.Join(outside, "a.ts")); err != nil || string(data) != "secret" {
		t.Fatal("wrote through a junction", err)
	}
	if got := macroNames(t, l); !slices.Equal(got, []string{"a.ts", "b.ts"}) {
		t.Fatal(got)
	}
}

// An 8.3 short name names another file: an edit takes only a name the
// folder lists exactly, so the short name of a macro reaches nothing, and
// a migrated or imported macro never takes a name that is another file's
// short name.
func TestMacroShortNames(t *testing.T) {
	dir := t.TempDir()
	fixture(t, dir, "paint", validProfile)
	putMacroFiles(t, dir, map[string]string{"abcdefghijk.ts": "long"})
	short := "ABCDEF~1.TS"
	if _, err := os.Stat(filepath.Join(dir, profile.MacroDir, short)); err != nil {
		t.Skip("no 8.3 names on this volume:", err)
	}
	l := New(dir)
	var notFound *MacroNotFoundError
	if _, err := l.RenameMacro(short, "x.ts"); !errors.As(err, &notFound) {
		t.Fatal(err)
	}
	if _, err := l.MacroPath(short, nil); !errors.As(err, &notFound) {
		t.Fatal(err)
	}
	if _, err := l.DeleteMacro(short); err != nil {
		t.Fatal(err)
	}
	if got := readMacro(t, dir, "abcdefghijk.ts"); got != "long" {
		t.Fatal(got)
	}
	// Another case is another name too.
	if _, err := l.DeleteMacro(strings.ToUpper("abcdefghijk") + ".ts"); err != nil {
		t.Fatal(err)
	}
	if names := macroNames(t, l); len(names) != 1 {
		t.Fatal(names)
	}
	// A profile's file named like the short name moves under a number.
	putProfileMacros(t, dir, "paint", map[string]string{"abcdef~1.ts": "other"})
	m, err := l.MigrateMacros()
	if err != nil || len(m.Errs) != 0 || len(m.Moved) != 1 || m.Moved[0].To != "abcdef~1 2.ts" {
		t.Fatalf("%+v, %v", m, err)
	}
	if got := readMacro(t, dir, "abcdefghijk.ts"); got != "long" {
		t.Fatal("replaced through a short name:", got)
	}
}
