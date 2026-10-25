package profilelib

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// macroArchive is an archive of a profile named name that runs a.ts and
// x.ts, with their bytes, as an export from before the library makes one:
// the profile's whole macros folder, b.ts that nothing runs, tsconfig.json
// and a module included.
func macroArchive(t *testing.T, name, a, x string) *Archive {
	t.Helper()
	entries := []zipEntry{
		{name: ArchiveManifest, data: `{"type":"padmate-profile","format":1,"name":"x","padmate":"test"}`},
		{name: "profile/profile.json", data: macrosProfile(name, "a.ts", "x.ts")},
		{name: "profile/macros/a.ts", data: a},
		{name: "profile/macros/x.ts", data: x},
		{name: "profile/macros/b.ts", data: "B"},
		{name: "profile/macros/tsconfig.json", data: "T2"},
		{name: "profile/macros/lib/util.ts", data: "U2"},
	}
	archive, err := ReadArchive(makeZip(t, entries...))
	if err != nil {
		t.Fatal(err)
	}
	return archive
}

// An import puts the archive's macros into the library: the same bytes are
// used, a name the library has with other bytes is asked about before
// anything is written, and then replaced or kept beside under a number,
// which the imported profile runs. Modules go where the library has
// nothing.
func TestImportMacroConflicts(t *testing.T) {
	dir := t.TempDir()
	putMacroFiles(t, dir, map[string]string{"a.ts": "A", "x.js": "X", "lib/util.ts": "U"})
	l := New(dir)
	snapshot := func() map[string]string {
		files := map[string]string{}
		_ = filepath.WalkDir(l.MacroDir(), func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(l.MacroDir(), p)
				files[filepath.ToSlash(rel)] = readFile(t, p)
			}
			return nil
		})
		return files
	}
	before := snapshot()

	// a.ts has other bytes, x.ts the name of x.js: both are asked about.
	var conflict *MacroConflictError
	_, err := l.Import(macroArchive(t, "one", "A1", "X1"), ImportOptions{})
	if !errors.As(err, &conflict) || !slices.Equal(conflict.Conflicts, []MacroConflict{
		{File: "a.ts", Existing: "a.ts", Replaceable: true}, {File: "x.ts", Existing: "x.js"},
	}) {
		t.Fatal(err)
	}
	if ids, _ := l.IDs(); len(ids) != 0 || !mapsEqual(snapshot(), before) {
		t.Fatal("wrote before asking:", ids, snapshot())
	}

	// Keep both: numbered names, which the profile runs.
	kept, err := l.Import(macroArchive(t, "one", "A1", "X1"), ImportOptions{Macros: MacrosKeep})
	if err != nil {
		t.Fatal(err)
	}
	if string(kept.Data) != macrosProfile("one", "a 2.ts", "x 2.ts") {
		t.Fatal(string(kept.Data))
	}
	if data, _ := l.Read(kept.ID); string(data) != string(kept.Data) || kept.Revision != Revision(data) {
		t.Fatal(string(data))
	}
	if !slices.Equal(kept.Macros, []ImportedMacro{{File: "a.ts", Name: "a 2.ts"}, {File: "b.ts", Name: "b.ts"}, {File: "x.ts", Name: "x 2.ts"}}) {
		t.Fatalf("%+v", kept.Macros)
	}
	want := map[string]string{"a.ts": "A", "a 2.ts": "A1", "b.ts": "B", "x.js": "X", "x 2.ts": "X1", "lib/util.ts": "U", "tsconfig.json": "T2"}
	if got := snapshot(); !mapsEqual(got, want) {
		t.Fatal(got)
	}
	if _, err := os.Lstat(filepath.Join(dir, "profiles", kept.ID, profile.MacroDir)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("macros in the profile folder", err)
	}

	// The same archive again needs no question: its macros are the
	// numbered ones now.
	again, err := l.Import(macroArchive(t, "two", "A1", "X1"), ImportOptions{})
	if err != nil || string(again.Data) != macrosProfile("two", "a 2.ts", "x 2.ts") ||
		!slices.Equal(again.Macros, []ImportedMacro{{File: "a.ts", Name: "a 2.ts", Same: true}, {File: "b.ts", Name: "b.ts", Same: true}, {File: "x.ts", Name: "x 2.ts", Same: true}}) {
		t.Fatalf("%+v, %v", again, err)
	}

	// Replace: a.ts gets the archive's bytes for every profile; x.ts
	// cannot replace x.js and is kept beside it.
	replaced, err := l.Import(macroArchive(t, "three", "A3", "X3"), ImportOptions{Macros: MacrosReplace})
	if err != nil || string(replaced.Data) != macrosProfile("three", "a.ts", "x 3.ts") {
		t.Fatalf("%+v, %v", replaced, err)
	}
	if !slices.Equal(replaced.Macros, []ImportedMacro{{File: "a.ts", Name: "a.ts", Replaced: true}, {File: "b.ts", Name: "b.ts", Same: true}, {File: "x.ts", Name: "x 3.ts"}}) {
		t.Fatalf("%+v", replaced.Macros)
	}
	if readMacro(t, dir, "a.ts") != "A3" || readMacro(t, dir, "x 3.ts") != "X3" || readMacro(t, dir, "x.js") != "X" {
		t.Fatal(snapshot())
	}
	if _, err := l.Import(macroArchive(t, "four", "A", "X"), ImportOptions{Macros: "both"}); err == nil {
		t.Fatal("unknown choice accepted")
	}
}

// A conflict with a name the library got only from the same archive is no
// question: the archive keeps both.
func TestImportMacroOwnNames(t *testing.T) {
	entries := []zipEntry{
		{name: ArchiveManifest, data: `{"type":"padmate-profile","format":1,"name":"x","padmate":"test"}`},
		{name: "profile/profile.json", data: macrosProfile("own", "a.ts")},
		{name: "profile/macros/a.js", data: "one"},
		{name: "profile/macros/a.ts", data: "two"},
	}
	a, err := ReadArchive(makeZip(t, entries...))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	l := New(dir)
	imported, err := l.Import(a, ImportOptions{})
	if err != nil || string(imported.Data) != macrosProfile("own", "a 2.ts") {
		t.Fatalf("%+v, %v", imported, err)
	}
	if got := macroNames(t, l); !slices.Equal(got, []string{"a 2.ts", "a.js"}) {
		t.Fatal(got)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
