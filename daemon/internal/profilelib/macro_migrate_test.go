package profilelib

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// macrosProfile runs each of files on a control of its own.
func macrosProfile(name string, files ...string) string {
	controls := []string{"south", "east", "west", "north"}
	var b strings.Builder
	b.WriteString(`{"schemaVersion":0,"name":"` + name + `","layers":{"base":{`)
	for i, f := range files {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"` + controls[i] + `":{"tap":{"type":"macro","file":"` + f + `"}}`)
	}
	b.WriteString(`}}}`)
	return b.String()
}

// migrationLibrary is a library from before the script library: four
// profiles with macros folders of their own.
//
//   - paint: a.ts, b.ts, a module lib/util.ts, tsconfig.json and generated
//     declarations;
//   - photo: a.ts as paint's, c.ts, the same tsconfig.json and another
//     lib/util.ts;
//   - rough: "a 2.ts" and an a.ts unlike paint's;
//   - sketch: an a.ts like rough's.
func migrationLibrary(t *testing.T) (string, *Library) {
	t.Helper()
	dir := t.TempDir()
	fixture(t, dir, "paint", macrosProfile("paint", "a.ts", "b.ts"))
	putProfileMacros(t, dir, "paint", map[string]string{
		"a.ts": "A", "b.ts": "B", "lib/util.ts": "U", "tsconfig.json": "T", ".padmate/padmate.d.ts": "D",
	})
	fixture(t, dir, "photo", macrosProfile("photo", "a.ts", "c.ts"))
	putProfileMacros(t, dir, "photo", map[string]string{"a.ts": "A", "c.ts": "C", "tsconfig.json": "T", "lib/util.ts": "U2"})
	fixture(t, dir, "rough", macrosProfile("rough", "a.ts", "a 2.ts"))
	putProfileMacros(t, dir, "rough", map[string]string{"a.ts": "A2", "a 2.ts": "X"})
	fixture(t, dir, "sketch", macrosProfile("sketch", "a.ts"))
	putProfileMacros(t, dir, "sketch", map[string]string{"a.ts": "A2"})
	fixture(t, dir, "plain", validProfile)
	return dir, New(dir)
}

// checkMigrated checks the library after migrationLibrary migrated.
func checkMigrated(t *testing.T, dir string, l *Library) {
	t.Helper()
	if got, want := macroNames(t, l), []string{"a 2.ts", "a 3.ts", "a.ts", "b.ts", "c.ts"}; !slices.Equal(got, want) {
		t.Fatalf("library %q, want %q", got, want)
	}
	for name, want := range map[string]string{"a.ts": "A", "a 2.ts": "X", "a 3.ts": "A2", "b.ts": "B", "c.ts": "C", "tsconfig.json": "T", "lib/util.ts": "U"} {
		if got := readMacro(t, dir, name); got != want {
			t.Errorf("%s is %q, want %q", name, got, want)
		}
	}
	if _, err := os.Lstat(filepath.Join(dir, profile.MacroDir, profile.MacroTypesDir)); !errors.Is(err, fs.ErrNotExist) {
		t.Error("generated declarations moved", err)
	}
	for id, want := range map[string]string{
		"paint":  macrosProfile("paint", "a.ts", "b.ts"),
		"photo":  macrosProfile("photo", "a.ts", "c.ts"),
		"rough":  macrosProfile("rough", "a 3.ts", "a 2.ts"),
		"sketch": macrosProfile("sketch", "a 3.ts"),
		"plain":  validProfile,
	} {
		if data, err := l.Read(id); err != nil || string(data) != want {
			t.Errorf("%s: %s, %v", id, data, err)
		}
		folder := filepath.Join(dir, "profiles", id, profile.MacroDir)
		_, err := os.Lstat(folder)
		if id == "photo" {
			if got := readFile(t, filepath.Join(folder, "lib", "util.ts")); got != "U2" {
				t.Errorf("photo's module is %q", got)
			}
			entries, _ := os.ReadDir(folder)
			if len(entries) != 1 {
				t.Errorf("photo's macros folder holds %v", entries)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s's macros folder stays: %v", id, err)
		}
	}
	for id, n := range map[string]int{"paint": 0, "photo": 0, "rough": 1, "sketch": 1} {
		if history, err := l.History(id); err != nil || len(history) != n {
			t.Errorf("%s history %v, %v", id, history, err)
		}
	}
	if got := journalFiles(t, dir); len(got) != 0 {
		t.Error("journals stay", got)
	}
}

// journalFiles lists the migration journals in the data folder.
func journalFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), MigrationJournalPrefix) {
			names = append(names, e.Name())
		}
	}
	return names
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Each profile's macros move into the library: the same bytes are kept
// once, another file with a taken name gets a number and the profile's
// references follow it, also when the number was the old name of another
// of its macros; modules move where the library has none, the generated
// declarations go and the emptied folders are removed. A second run
// changes nothing.
func TestMigrateMacros(t *testing.T) {
	dir, l := migrationLibrary(t)
	m, err := l.MigrateMacros()
	if err != nil || len(m.Errs) != 0 {
		t.Fatalf("%+v, %v", m, err)
	}
	checkMigrated(t, dir, l)
	moved := map[string]MigratedMacro{}
	for _, mm := range m.Moved {
		moved[mm.Profile+"/"+mm.From] = mm
	}
	for key, want := range map[string]MigratedMacro{
		"paint/a.ts":          {Profile: "paint", From: "a.ts", To: "a.ts"},
		"paint/lib/util.ts":   {Profile: "paint", From: "lib/util.ts", To: "lib/util.ts"},
		"photo/a.ts":          {Profile: "photo", From: "a.ts", To: "a.ts", Same: true},
		"photo/tsconfig.json": {Profile: "photo", From: "tsconfig.json", To: "tsconfig.json", Same: true},
		"rough/a.ts":          {Profile: "rough", From: "a.ts", To: "a 3.ts"},
		"rough/a 2.ts":        {Profile: "rough", From: "a 2.ts", To: "a 2.ts"},
		"sketch/a.ts":         {Profile: "sketch", From: "a.ts", To: "a 3.ts", Same: true},
	} {
		if moved[key] != want {
			t.Errorf("%s: %+v, want %+v", key, moved[key], want)
		}
	}
	if len(m.Moved) != 10 || !slices.Equal(m.Left, []string{"photo/macros/lib/util.ts"}) || m.Renamed["rough"] != 1 || m.Renamed["sketch"] != 1 || len(m.Renamed) != 2 {
		t.Fatalf("%+v", m)
	}
	// A module the library has with other bytes is reported apart: the
	// macros of its profile that import it run the library's file. Another
	// tsconfig.json is none.
	if !slices.Equal(m.Shadowed, []string{"photo/macros/lib/util.ts"}) {
		t.Fatalf("shadowed %q", m.Shadowed)
	}
	again, err := l.MigrateMacros()
	if err != nil || len(again.Moved) != 0 || len(again.Errs) != 0 || !slices.Equal(again.Left, m.Left) {
		t.Fatalf("again: %+v, %v", again, err)
	}
	checkMigrated(t, dir, l)
	// A library without per-profile folders is not touched at all.
	empty := New(t.TempDir())
	if m, err := empty.MigrateMacros(); err != nil || len(m.Moved)+len(m.Left)+len(m.Errs) != 0 {
		t.Fatal(m, err)
	}
	if _, err := os.Lstat(empty.MacroDir()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("created the macros folder", err)
	}
}

// A migration cut off after its journal finishes from the journal: at
// each step a crash can stop it, the next run ends where an uncut one
// does, without renaming a reference twice.
func TestMigrateMacrosResumes(t *testing.T) {
	for _, tc := range []struct {
		name string
		// steps is how far the cut-off run got in rough.
		steps func(t *testing.T, l *Library, j *journal)
	}{
		{"journal", func(*testing.T, *Library, *journal) {}},
		{"profile", func(t *testing.T, l *Library, j *journal) {
			if _, err := l.putLocked(j.Profile, j.Revision, j.Data, false); err != nil {
				t.Fatal(err)
			}
		}},
		{"one file", func(t *testing.T, l *Library, j *journal) {
			if _, err := l.putLocked(j.Profile, j.Revision, j.Data, false); err != nil {
				t.Fatal(err)
			}
			source, err := l.openProfileMacros(j.Profile)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			library, err := l.openMacroDir(true)
			if err != nil {
				t.Fatal(err)
			}
			defer library.Close()
			if _, _, err := moveFile(source, library, j.Files[0].From, j.Files[0].To); err != nil {
				t.Fatal(err)
			}
		}},
		{"copied, not deleted", func(t *testing.T, l *Library, j *journal) {
			if _, err := l.putLocked(j.Profile, j.Revision, j.Data, false); err != nil {
				t.Fatal(err)
			}
			for _, f := range j.Files {
				data := readFile(t, filepath.Join(l.dir, "profiles", j.Profile, profile.MacroDir, f.From))
				writeFile(t, filepath.Join(l.MacroDir(), f.To), data)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, l := migrationLibrary(t)
			// paint and photo migrate, and rough's plan is made, as in a
			// full run; sketch is planned after rough is done.
			for _, id := range []string{"paint", "photo"} {
				j, err := l.planMigration(id)
				if err != nil {
					t.Fatal(err)
				}
				var m MacroMigration
				if err := l.runJournal(&m, j); err != nil {
					t.Fatal(err)
				}
			}
			j, err := l.planMigration("rough")
			if err != nil || j.Data == nil || len(j.Files) != 2 {
				t.Fatalf("%+v, %v", j, err)
			}
			if err := l.writeJournal(j); err != nil {
				t.Fatal(err)
			}
			tc.steps(t, l, j)
			m, err := l.MigrateMacros()
			if err != nil || len(m.Errs) != 0 {
				t.Fatalf("%+v, %v", m, err)
			}
			checkMigrated(t, dir, l)
		})
	}
}

// A damaged journal is reported and deleted, and the migration is planned
// again; a profile changed since its journal is planned again too, with
// the references it has now.
func TestMigrateMacrosJournalProblems(t *testing.T) {
	dir, l := migrationLibrary(t)
	writeFile(t, filepath.Join(dir, journalName("rough")), "{")
	m, err := l.MigrateMacros()
	if err != nil || len(m.Errs) != 1 || !strings.Contains(m.Errs[0].Error(), "damaged") {
		t.Fatalf("%+v, %v", m, err)
	}
	checkMigrated(t, dir, l)

	dir, l = migrationLibrary(t)
	j, err := l.planMigration("paint")
	if err == nil {
		err = l.runJournal(&m, j)
	}
	if err != nil {
		t.Fatal(err)
	}
	if j, err = l.planMigration("rough"); err != nil || j.Data == nil {
		t.Fatal(j, err)
	}
	if err := l.writeJournal(j); err != nil {
		t.Fatal(err)
	}
	fixture(t, dir, "rough", macrosProfile("rough 2", "a.ts", "a 2.ts"))
	m, err = l.MigrateMacros()
	if err != nil || len(m.Errs) != 1 || !strings.Contains(m.Errs[0].Error(), "changed since") {
		t.Fatalf("%+v, %v", m, err)
	}
	if data, _ := l.Read("rough"); string(data) != macrosProfile("rough 2", "a 3.ts", "a 2.ts") || readMacro(t, dir, "a 3.ts") != "A2" {
		t.Fatal(string(data))
	}
	if got := journalFiles(t, dir); len(got) != 0 {
		t.Fatal("journals stay", got)
	}
}

// A profile whose references were renamed but whose macros could not all
// be moved keeps its own journal: the profiles after it neither overwrite
// it nor take the names it has reserved, so that its references run its
// macros when the next start finishes it.
func TestMigrateMacrosKeepsFailedJournal(t *testing.T) {
	dir := t.TempDir()
	putMacroFiles(t, dir, map[string]string{"a.ts": "L"})
	for _, id := range []string{"x", "y"} {
		fixture(t, dir, id, macrosProfile(id, "a.ts"))
		putProfileMacros(t, dir, id, map[string]string{"a.ts": strings.ToUpper(id)})
	}
	l := New(dir)
	l.moveHook = func(id, _ string) error {
		if id == "x" {
			return errors.New("disk error")
		}
		return nil
	}
	m, err := l.MigrateMacros()
	if err != nil || len(m.Errs) != 1 || !strings.Contains(m.Errs[0].Error(), "disk error") {
		t.Fatalf("%+v, %v", m, err)
	}
	// x's references follow its plan, its macro has not moved and its
	// journal stays; y was migrated, under the number after x's.
	if data, _ := l.Read("x"); string(data) != macrosProfile("x", "a 2.ts") {
		t.Fatal(string(data))
	}
	if got := journalFiles(t, dir); !slices.Equal(got, []string{journalName("x")}) {
		t.Fatal("journals", got)
	}
	if data, _ := l.Read("y"); string(data) != macrosProfile("y", "a 3.ts") || readMacro(t, dir, "a 3.ts") != "Y" {
		t.Fatal(string(data))
	}
	if _, err := os.Lstat(filepath.Join(dir, "profiles", "x", profile.MacroDir, "a.ts")); err != nil {
		t.Fatal("x's macro left its folder", err)
	}
	l.moveHook = nil
	again, err := l.MigrateMacros()
	if err != nil || len(again.Errs) != 0 {
		t.Fatalf("%+v, %v", again, err)
	}
	for name, want := range map[string]string{"a.ts": "L", "a 2.ts": "X", "a 3.ts": "Y"} {
		if got := readMacro(t, dir, name); got != want {
			t.Errorf("%s is %q, want %q", name, got, want)
		}
	}
	if got := journalFiles(t, dir); len(got) != 0 {
		t.Fatal("journals stay", got)
	}
	if _, err := os.Lstat(filepath.Join(dir, "profiles", "x", profile.MacroDir)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("x's macros folder stays", err)
	}
}

// A name the journal planned that another file has taken since is an
// error, not a success: the macro stays in its profile's folder, the
// journal stays, and the library's file is not touched.
func TestMigrateMacrosTargetTaken(t *testing.T) {
	dir := t.TempDir()
	putMacroFiles(t, dir, map[string]string{"a.ts": "L"})
	fixture(t, dir, "x", macrosProfile("x", "a.ts"))
	putProfileMacros(t, dir, "x", map[string]string{"a.ts": "X"})
	l := New(dir)
	j, err := l.planMigration("x")
	if err != nil || j.Data == nil || j.Files[0].To != "a 2.ts" {
		t.Fatalf("%+v, %v", j, err)
	}
	if err := l.writeJournal(j); err != nil {
		t.Fatal(err)
	}
	if _, err := l.putLocked("x", j.Revision, j.Data, false); err != nil {
		t.Fatal(err)
	}
	putMacroFiles(t, dir, map[string]string{"a 2.ts": "other"})
	m, err := l.MigrateMacros()
	if err != nil || len(m.Errs) != 1 || !strings.Contains(m.Errs[0].Error(), "a 2.ts") || len(m.Moved) != 0 {
		t.Fatalf("%+v, %v", m, err)
	}
	if readMacro(t, dir, "a 2.ts") != "other" || readFile(t, filepath.Join(dir, "profiles", "x", profile.MacroDir, "a.ts")) != "X" {
		t.Fatal("a file was lost")
	}
	if got := journalFiles(t, dir); !slices.Equal(got, []string{journalName("x")}) {
		t.Fatal("journals", got)
	}
}

// A macro that needs another name stays in its profile's macros folder
// while profile.json cannot be read: under its old name the profile would
// run the library's other macro. The others move, and each start tries
// again until profile.json can be read.
func TestMigrateMacrosBrokenProfile(t *testing.T) {
	dir := t.TempDir()
	fixture(t, dir, "paint", macrosProfile("paint", "a.ts"))
	putProfileMacros(t, dir, "paint", map[string]string{"a.ts": "A"})
	const broken = `{"layers":{"base":{"south":{"tap":{"type":"macro","file":"a.ts"}}}`
	fixture(t, dir, "torn", broken)
	putProfileMacros(t, dir, "torn", map[string]string{"a.ts": "other", "b.ts": "B"})
	l := New(dir)
	for range 2 {
		m, err := l.MigrateMacros()
		if err != nil || len(m.Errs) != 1 || !strings.Contains(m.Errs[0].Error(), "torn") || !slices.Equal(m.Left, []string{"torn/macros/a.ts"}) {
			t.Fatalf("%+v, %v", m, err)
		}
		if got := macroNames(t, l); !slices.Equal(got, []string{"a.ts", "b.ts"}) || readMacro(t, dir, "a.ts") != "A" {
			t.Fatal(got)
		}
		if data, _ := l.Read("torn"); string(data) != broken {
			t.Fatal(string(data))
		}
	}
	fixture(t, dir, "torn", macrosProfile("torn", "a.ts"))
	m, err := l.MigrateMacros()
	if err != nil || len(m.Errs) != 0 || len(m.Left) != 0 || m.Renamed["torn"] != 1 {
		t.Fatalf("%+v, %v", m, err)
	}
	if data, _ := l.Read("torn"); string(data) != macrosProfile("torn", "a 2.ts") || readMacro(t, dir, "a 2.ts") != "other" {
		t.Fatal(string(data))
	}
}

// When a profile.json cannot be written, its macros that need another
// name stay and the profile is migrated at the next start; the other
// profiles go on.
func TestMigrateMacrosFailedSave(t *testing.T) {
	dir, l := migrationLibrary(t)
	l.setPutHook(func(id string) error {
		if id == "rough" {
			return errors.New("disk full")
		}
		return nil
	})
	m, err := l.MigrateMacros()
	if err != nil || len(m.Errs) != 1 || !strings.Contains(m.Errs[0].Error(), "disk full") || !slices.Contains(m.Left, "rough/macros/a.ts") {
		t.Fatalf("%+v, %v", m, err)
	}
	if data, _ := l.Read("rough"); string(data) != macrosProfile("rough", "a.ts", "a 2.ts") {
		t.Fatal(string(data))
	}
	l.setPutHook(nil)
	if m, err = l.MigrateMacros(); err != nil || len(m.Errs) != 0 {
		t.Fatalf("%+v, %v", m, err)
	}
	checkMigrated(t, dir, l)
}

// numberedMacro adds the number after the name and keeps a long name
// within profile.MaxMacroName.
func TestNumberedMacro(t *testing.T) {
	long := strings.Repeat("长", profile.MaxMacroName)
	for _, tc := range []struct {
		file string
		n    int
		want string
	}{
		{"a.ts", 1, "a.ts"},
		{"a.ts", 2, "a 2.ts"},
		{"复制 全部.JS", 12, "复制 全部 12.JS"},
		{long + ".ts", 2, strings.Repeat("长", profile.MaxMacroName-2) + " 2.ts"},
		{strings.Repeat("长", profile.MaxMacroName-3) + "  x.ts", 10, strings.Repeat("长", profile.MaxMacroName-3) + " 10.ts"},
	} {
		got := numberedMacro(tc.file, tc.n)
		if got != tc.want {
			t.Errorf("numberedMacro(%q, %d) = %q, want %q", tc.file, tc.n, got, tc.want)
		}
		if profile.CheckMacroFile(tc.file) == nil && profile.CheckMacroFile(got) != nil {
			t.Errorf("%q: %v", got, profile.CheckMacroFile(got))
		}
	}
}
