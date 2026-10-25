package profilelib

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// macroProfile runs a.ts on south's press and tap, b.ts on east and has a
// legacy script whose action runs on north.
const macroProfile = `{"schemaVersion":0,"name":"paint",
  "script": {"entry": "main.ts", "api": "0.21"},
  "layers": {"base": {
    "south": {"label": "复制", "press": {"type": "macro", "file": "a.ts"}, "tap": {"type": "macro", "file": "a.ts"}},
    "east": {"press": {"type": "macro", "file": "b.ts"}, "release": {"type": "keys", "keys": "F13"}},
    "north": {"press": {"type": "script", "name": "brush"}}
  }}
}
`

// photoProfile runs a.ts on its common layer's back.
const photoProfile = `{"schemaVersion":0,"name":"photo","layers":{"base":{}},"common":{"back":{"hold":{"type":"macro","file":"a.ts"}}}}`

var macroTypes = []MacroFile{
	{Name: "tsconfig.json", Data: []byte("{}\n"), Keep: true},
	{Name: ".padmate/padmate.d.ts", Data: []byte("declare module x;\n")},
}

// putMacroFiles writes files into the library's macros folder.
func putMacroFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		writeFile(t, filepath.Join(dir, profile.MacroDir, filepath.FromSlash(name)), text)
	}
}

// putProfileMacros writes files into profile id's own macros folder, from
// before the library.
func putProfileMacros(t *testing.T, dir, id string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		writeFile(t, filepath.Join(dir, "profiles", id, profile.MacroDir, filepath.FromSlash(name)), text)
	}
}

func readMacro(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, profile.MacroDir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func macroNames(t *testing.T, l *Library) []string {
	t.Helper()
	names, err := l.Macros()
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// The list is the top .ts and .js files of <data>/macros, names
// CheckMacroFile refuses included, sorted; declarations, other files and
// folders are not macros, nor are a profile's own.
func TestMacros(t *testing.T) {
	dir := t.TempDir()
	fixture(t, dir, "paint", validProfile)
	l := New(dir)
	if names := macroNames(t, l); len(names) != 0 {
		t.Fatal(names)
	}
	if got := l.MacroDir(); got != filepath.Join(dir, "macros") {
		t.Fatal(got)
	}
	putMacroFiles(t, dir, map[string]string{
		"复制全部.ts": "", "a.js": "", "v1.2.ts": "", "Upper.TS": "", "x.d.ts": "", "notes.txt": "", "tsconfig.json": "",
	})
	putProfileMacros(t, dir, "paint", map[string]string{"own.ts": ""})
	if err := os.MkdirAll(filepath.Join(dir, profile.MacroDir, "lib.ts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, want := macroNames(t, l), []string{"Upper.TS", "a.js", "v1.2.ts", "复制全部.ts"}; !slices.Equal(got, want) {
		t.Fatalf("macros %q, want %q", got, want)
	}
	if names, err := New(filepath.Join(dir, "missing")).Macros(); err != nil || names != nil {
		t.Fatal(names, err)
	}
}

// A new macro gets the folder and its editor files; tsconfig.json is never
// rewritten, the declarations are when they differ. The same name in
// another case or extension, and a 65th macro, are refused.
func TestCreateMacro(t *testing.T) {
	dir := t.TempDir()
	fixture(t, dir, "paint", macroProfile)
	l := New(dir)
	if err := l.CreateMacro("复制全部.ts", []byte("template"), macroTypes); err != nil {
		t.Fatal(err)
	}
	if readMacro(t, dir, "复制全部.ts") != "template" || readMacro(t, dir, "tsconfig.json") != "{}\n" ||
		readMacro(t, dir, ".padmate/padmate.d.ts") != "declare module x;\n" {
		t.Fatal("created files")
	}
	putMacroFiles(t, dir, map[string]string{"tsconfig.json": "// mine\n", ".padmate/padmate.d.ts": "old"})
	if err := l.CreateMacro("next.js", nil, macroTypes); err != nil {
		t.Fatal(err)
	}
	if readMacro(t, dir, "tsconfig.json") != "// mine\n" || readMacro(t, dir, ".padmate/padmate.d.ts") != "declare module x;\n" {
		t.Fatal("editor files after the second macro")
	}
	var exists *MacroExistsError
	for _, name := range []string{"复制全部.ts", "复制全部.js", "NEXT.ts"} {
		if err := l.CreateMacro(name, nil, macroTypes); !errors.As(err, &exists) || exists.Replaceable || !SameMacro(exists.Name, name) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	var nameErr *profile.MacroNameError
	for _, name := range []string{"v1.2.ts", "a.TS", "con.ts", ""} {
		if err := l.CreateMacro(name, nil, macroTypes); !errors.As(err, &nameErr) {
			t.Fatalf("%q: %v", name, err)
		}
	}
	for i := 2; i < profile.MaxMacros; i++ {
		if err := l.CreateMacro(fmt.Sprintf("m%d.ts", i), nil, nil); err != nil {
			t.Fatal(i, err)
		}
	}
	var limit *MacroLimitError
	if err := l.CreateMacro("one more.ts", nil, nil); !errors.As(err, &limit) {
		t.Fatal(err)
	}
	if _, _, err := l.ImportMacro("one more.ts", nil, false, nil); !errors.As(err, &limit) {
		t.Fatal(err)
	}
	// Replacing one at the limit is no new macro.
	if written, replaced, err := l.ImportMacro("m2.ts", []byte("x"), true, nil); err != nil || written != "m2.ts" || !replaced {
		t.Fatal(written, replaced, err)
	}
	if data, err := l.Read("paint"); err != nil || string(data) != macroProfile {
		t.Fatal("profile.json changed", err)
	}
	// A library without a data folder yet gets one.
	fresh := New(filepath.Join(t.TempDir(), "padmate"))
	if err := fresh.CreateMacro("a.ts", nil, nil); err != nil || !slices.Equal(macroNames(t, fresh), []string{"a.ts"}) {
		t.Fatal(err)
	}
}

// setPutHook makes the writes of profile.json fail where hook says.
func (l *Library) setPutHook(hook func(id string) error) {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	l.putHook = hook
}

// SameMacro is profile.SameMacroName, short.
func SameMacro(a, b string) bool { return profile.SameMacroName(a, b) }

// An import with the name of a macro of the same extension, in any case,
// is replaceable: with replace it goes into the existing exact name. One
// with another extension is refused either way. The text is bounded UTF-8.
func TestImportMacro(t *testing.T) {
	dir := t.TempDir()
	fixture(t, dir, "paint", macroProfile)
	putMacroFiles(t, dir, map[string]string{"a.ts": "old a", "b.js": "old b"})
	l := New(dir)
	if written, replaced, err := l.ImportMacro("新的 (1).ts", []byte("new"), false, macroTypes); err != nil || written != "新的 (1).ts" || replaced {
		t.Fatal(written, replaced, err)
	}
	if readMacro(t, dir, "新的 (1).ts") != "new" || readMacro(t, dir, "tsconfig.json") != "{}\n" {
		t.Fatal("imported files")
	}
	var exists *MacroExistsError
	if _, _, err := l.ImportMacro("A.ts", []byte("new a"), false, nil); !errors.As(err, &exists) || exists.Name != "a.ts" || !exists.Replaceable {
		t.Fatal(err)
	}
	if written, replaced, err := l.ImportMacro("A.ts", []byte("new a"), true, nil); err != nil || written != "a.ts" || !replaced {
		t.Fatal(written, replaced, err)
	}
	if readMacro(t, dir, "a.ts") != "new a" {
		t.Fatal("replaced text")
	}
	for _, replace := range []bool{false, true} {
		if _, _, err := l.ImportMacro("b.ts", []byte("x"), replace, nil); !errors.As(err, &exists) || exists.Name != "b.js" || exists.Replaceable {
			t.Fatal(replace, err)
		}
	}
	if readMacro(t, dir, "b.js") != "old b" {
		t.Fatal("replaced another extension")
	}
	for _, data := range [][]byte{{0xff}, []byte(strings.Repeat(" ", MaxScriptBytes+1))} {
		if _, _, err := l.ImportMacro("c.ts", data, false, nil); err == nil {
			t.Fatal("bad text accepted")
		}
	}
	if got, want := macroNames(t, l), []string{"a.ts", "b.js", "新的 (1).ts"}; !slices.Equal(got, want) {
		t.Fatalf("macros %q, want %q", got, want)
	}
}

// editedIDs returns the IDs of an edit's profiles.
func editedIDs(edit MacroEdit) []string {
	var ids []string
	for _, p := range edit.Profiles {
		ids = append(ids, p.ID)
	}
	return ids
}

// A rename changes the file and, in place, every action of every profile
// that runs it, keeping each old profile.json in its history; profiles
// that do not run it stay as they are.
func TestRenameMacro(t *testing.T) {
	dir := t.TempDir()
	fixture(t, dir, "paint", macroProfile)
	fixture(t, dir, "photo", photoProfile)
	fixture(t, dir, "plain", validProfile)
	putMacroFiles(t, dir, map[string]string{"a.ts": "a", "b.ts": "b", "v1.2.ts": "bad name"})
	l := New(dir)
	edit, err := l.RenameMacro("a.ts", "复制 (全部).ts")
	if err != nil || edit.File != "复制 (全部).ts" || edit.Bindings != 3 || !slices.Equal(editedIDs(edit), []string{"paint", "photo"}) {
		t.Fatal(edit, err)
	}
	want := strings.ReplaceAll(macroProfile, `"a.ts"`, `"复制 (全部).ts"`)
	if data, err := l.Read("paint"); err != nil || string(data) != want || string(edit.Profiles[0].Data) != want ||
		edit.Profiles[0].Saved.Revision != Revision(data) || edit.Profiles[0].Bindings != 2 {
		t.Fatalf("profile.json %s, %v", data, err)
	}
	if data, err := l.Read("photo"); err != nil || string(data) != strings.ReplaceAll(photoProfile, `"a.ts"`, `"复制 (全部).ts"`) {
		t.Fatalf("photo %s, %v", data, err)
	}
	for id, n := range map[string]int{"paint": 1, "photo": 1, "plain": 0} {
		if history, err := l.History(id); err != nil || len(history) != n {
			t.Fatal(id, history, err)
		}
	}
	if got := macroNames(t, l); !slices.Equal(got, []string{"b.ts", "v1.2.ts", "复制 (全部).ts"}) {
		t.Fatal(got)
	}
	// A case-only rename, and one of a name CheckMacroFile refuses.
	if edit, err := l.RenameMacro("b.ts", "B.ts"); err != nil || edit.Bindings != 1 {
		t.Fatal(edit, err)
	}
	if edit, err := l.RenameMacro("v1.2.ts", "v1-2.ts"); err != nil || edit.Bindings != 0 || edit.Profiles != nil {
		t.Fatal(edit, err)
	}
	if got := macroNames(t, l); !slices.Equal(got, []string{"B.ts", "v1-2.ts", "复制 (全部).ts"}) {
		t.Fatal(got)
	}
	var notFound *MacroNotFoundError
	var exists *MacroExistsError
	var nameErr *profile.MacroNameError
	for _, tc := range []struct {
		from, to string
		check    func(error) bool
	}{
		{"none.ts", "x.ts", func(err error) bool { return errors.As(err, &notFound) && notFound.File == "none.ts" }},
		{"b.ts", "x.ts", func(err error) bool { return errors.As(err, &notFound) }}, // only B.ts is listed
		{"B.ts", "复制 (全部).ts", func(err error) bool { return errors.As(err, &exists) && exists.Name == "复制 (全部).ts" }},
		{"B.ts", "V1-2.ts", func(err error) bool { return errors.As(err, &exists) && exists.Name == "v1-2.ts" }},
		{"B.ts", "B.js", func(err error) bool { return errors.Is(err, ErrMacroExtension) }},
		{"B.ts", "a.b.ts", func(err error) bool { return errors.As(err, &nameErr) }},
	} {
		if _, err := l.RenameMacro(tc.from, tc.to); !tc.check(err) {
			t.Errorf("%s → %s: %v", tc.from, tc.to, err)
		}
	}
}

// A profile that does not validate gets the renamed references all the
// same, with only them changed; one whose syntax is broken is left as it
// is. When a profile cannot be written, those written before get their
// old bytes back and the file its old name.
func TestRenameMacroBrokenProfiles(t *testing.T) {
	dir := t.TempDir()
	semantic := strings.Replace(macroProfile, `"name":"paint"`, `"name":"paint","unknown":1`, 1)
	fixture(t, dir, "broken", semantic)
	fixture(t, dir, "syntax", `{"layers":{"base":{"south":{"tap":{"type":"macro","file":"a.ts"}}}`)
	fixture(t, dir, "photo", photoProfile)
	putMacroFiles(t, dir, map[string]string{"a.ts": "a"})
	l := New(dir)
	edit, err := l.RenameMacro("a.ts", "c.ts")
	if err != nil || !slices.Equal(editedIDs(edit), []string{"broken", "photo"}) {
		t.Fatal(edit, err)
	}
	if data, _ := l.Read("broken"); string(data) != strings.ReplaceAll(semantic, `"a.ts"`, `"c.ts"`) {
		t.Fatal(string(data))
	}
	if data, _ := l.Read("syntax"); !strings.Contains(string(data), `"a.ts"`) {
		t.Fatal(string(data))
	}
	// A failed save of photo after broken's: broken gets its bytes back.
	l.setPutHook(func(id string) error {
		if id == "photo" {
			return errors.New("disk full")
		}
		return nil
	})
	defer l.setPutHook(nil)
	before, _ := l.Read("broken")
	snapshots := len(history(t, l, "broken"))
	if _, err := l.RenameMacro("c.ts", "e.ts"); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatal(err)
	}
	if after, _ := l.Read("broken"); string(after) != string(before) {
		t.Fatalf("not restored: %s", after)
	}
	// The undo keeps no version the user never saved.
	if got := len(history(t, l, "broken")); got != snapshots {
		t.Fatalf("history of broken has %d snapshots, had %d", got, snapshots)
	}
	if got := macroNames(t, l); !slices.Equal(got, []string{"c.ts"}) {
		t.Fatal("not renamed back:", got)
	}
	// An unreadable profile.json stops the rename too.
	l.setPutHook(nil)
	photo := filepath.Join(dir, "profiles", "photo", "profile.json")
	if err := os.Remove(photo); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(photo, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := l.RenameMacro("c.ts", "e.ts"); err == nil {
		t.Fatal("renamed past an unreadable profile")
	}
	if got := macroNames(t, l); !slices.Equal(got, []string{"c.ts"}) {
		t.Fatal("not renamed back:", got)
	}
}

// A rename never replaces another entry of the folder: a listed file whose
// extension is in upper case has the same name, and a folder or other
// entry that is not listed is in the way too. A case-only rename of an
// upper-case extension is fine.
func TestRenameMacroKeepsOtherFiles(t *testing.T) {
	dir := t.TempDir()
	fixture(t, dir, "paint", macroProfile)
	putMacroFiles(t, dir, map[string]string{"a.ts": "a", "b.ts": "b", "X.TS": "upper"})
	if err := os.Mkdir(filepath.Join(dir, profile.MacroDir, "dir.ts"), 0o700); err != nil {
		t.Fatal(err)
	}
	l := New(dir)
	var exists *MacroExistsError
	if _, err := l.RenameMacro("a.ts", "x.ts"); !errors.As(err, &exists) || exists.Name != "X.TS" {
		t.Fatalf("rename onto X.TS: %v", err)
	}
	if _, err := l.RenameMacro("a.ts", "dir.ts"); !errors.As(err, &exists) || exists.Name != "dir.ts" {
		t.Fatalf("rename onto a folder: %v", err)
	}
	if got := readMacro(t, dir, "X.TS"); got != "upper" {
		t.Fatalf("X.TS is %q", got)
	}
	if _, err := l.RenameMacro("X.TS", "x.ts"); err != nil {
		t.Fatal(err)
	}
	if got := macroNames(t, l); !slices.Equal(got, []string{"a.ts", "b.ts", "x.ts"}) || readMacro(t, dir, "x.ts") != "upper" {
		t.Fatal(got)
	}
}

// A deletion removes the actions of every profile first, with a binding
// left with no action, then the file; a file that is not there only loses
// its references, and one nothing runs leaves every profile.json as it is.
func TestDeleteMacro(t *testing.T) {
	dir := t.TempDir()
	fixture(t, dir, "paint", macroProfile)
	fixture(t, dir, "photo", photoProfile)
	putMacroFiles(t, dir, map[string]string{"a.ts": "a", "unused.ts": "u"})
	l := New(dir)
	edit, err := l.DeleteMacro("a.ts")
	if err != nil || edit.Bindings != 3 || !slices.Equal(editedIDs(edit), []string{"paint", "photo"}) {
		t.Fatal(edit, err)
	}
	data, err := l.Read("paint")
	if err != nil || strings.Contains(string(data), "a.ts") || strings.Contains(string(data), `"south"`) || !strings.Contains(string(data), `"b.ts"`) {
		t.Fatalf("profile.json %s, %v", data, err)
	}
	if _, ds := profile.Parse(data); ds.Err() != nil {
		t.Fatal(ds)
	}
	if data, _ := l.Read("photo"); strings.Contains(string(data), "a.ts") {
		t.Fatal(string(data))
	}
	edit, err = l.DeleteMacro("b.ts")
	if err != nil || edit.Bindings != 1 || len(edit.Profiles) != 1 || strings.Contains(string(edit.Profiles[0].Data), "b.ts") ||
		!strings.Contains(string(edit.Profiles[0].Data), "F13") {
		t.Fatal(edit, err)
	}
	edit, err = l.DeleteMacro("unused.ts")
	if err != nil || edit.Bindings != 0 || edit.Profiles != nil {
		t.Fatal(edit, err)
	}
	if got := macroNames(t, l); len(got) != 0 {
		t.Fatal(got)
	}
	var notFound *MacroNotFoundError
	for _, file := range []string{"", "../profile.json", `a\b.ts`} {
		if _, err := l.DeleteMacro(file); !errors.As(err, &notFound) {
			t.Fatalf("%q: %v", file, err)
		}
	}
	// A failed save keeps the file and every reference.
	putMacroFiles(t, dir, map[string]string{"c.ts": "c"})
	fixture(t, dir, "paint", strings.ReplaceAll(macroProfile, `"a.ts"`, `"c.ts"`))
	fixture(t, dir, "photo", strings.ReplaceAll(photoProfile, `"a.ts"`, `"c.ts"`))
	l.setPutHook(func(id string) error {
		if id == "photo" {
			return errors.New("disk full")
		}
		return nil
	})
	defer l.setPutHook(nil)
	if _, err := l.DeleteMacro("c.ts"); err == nil {
		t.Fatal("deleted after a failed save")
	}
	if got := macroNames(t, l); !slices.Equal(got, []string{"c.ts"}) {
		t.Fatal(got)
	}
	if data, _ := l.Read("paint"); !strings.Contains(string(data), `"c.ts"`) {
		t.Fatal("paint not restored:", string(data))
	}
	// Without a library folder, only references go.
	l.setPutHook(nil)
	if err := os.RemoveAll(filepath.Join(dir, profile.MacroDir)); err != nil {
		t.Fatal(err)
	}
	if edit, err := l.DeleteMacro("c.ts"); err != nil || edit.Bindings != 3 {
		t.Fatal(edit, err)
	}
}

// Detaching removes the script member and its actions; the files stay.
func TestDetachScript(t *testing.T) {
	dir := t.TempDir()
	fixture(t, dir, "paint", macroProfile)
	if err := os.WriteFile(filepath.Join(dir, "profiles", "paint", "main.ts"), []byte("main"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := New(dir)
	edit, err := l.DetachScript("paint")
	if err != nil || edit.Bindings != 1 || edit.Data == nil || edit.ID != "paint" {
		t.Fatal(edit, err)
	}
	doc, err := l.Load("paint")
	if err != nil || doc.Profile.Script != nil || strings.Contains(string(doc.Data), `"north"`) || !strings.Contains(string(doc.Data), "b.ts") {
		t.Fatalf("%s, %v", doc.Data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles", "paint", "main.ts")); err != nil {
		t.Fatal(err)
	}
	if edit, err := l.DetachScript("paint"); err != nil || edit.Data != nil || edit.Bindings != 0 {
		t.Fatal(edit, err)
	}
	if _, err := l.DetachScript("missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}

// The path of a macro is absolute, of a file listed exactly, and its
// editor files are written first.
func TestMacroPath(t *testing.T) {
	dir := t.TempDir()
	putMacroFiles(t, dir, map[string]string{"a.ts": "a"})
	l := New(dir)
	got, err := l.MacroPath("a.ts", macroTypes)
	if err != nil || got != filepath.Join(dir, profile.MacroDir, "a.ts") || !filepath.IsAbs(got) {
		t.Fatal(got, err)
	}
	if readMacro(t, dir, ".padmate/padmate.d.ts") != "declare module x;\n" {
		t.Fatal("declarations")
	}
	var notFound *MacroNotFoundError
	for _, file := range []string{"A.ts", "b.ts", "../profile.json", "tsconfig.json"} {
		if _, err := l.MacroPath(file, nil); !errors.As(err, &notFound) {
			t.Fatalf("%q: %v", file, err)
		}
	}
}

// The legacy entry's path names a regular file of the folder, reached
// without a link, outside the history.
func TestScriptPath(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	withEntry := func(entry string) {
		t.Helper()
		data, err := encodeEntry(entry)
		if err != nil {
			t.Fatal(err)
		}
		fixture(t, dir, "paint", data)
	}
	withEntry("src/main.ts")
	if err := os.MkdirAll(filepath.Join(dir, "profiles", "paint", "src", "dir.ts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profiles", "paint", "src", "main.ts"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := l.ScriptPath("paint"); err != nil || got != filepath.Join(dir, "profiles", "paint", "src", "main.ts") {
		t.Fatal(got, err)
	}
	withEntry("./src/../src/missing.ts")
	var missing *EntryNotFoundError
	if _, err := l.ScriptPath("paint"); !errors.Is(err, fs.ErrNotExist) || !errors.As(err, &missing) || missing.Entry != "./src/../src/missing.ts" {
		t.Fatal(err)
	}
	var unsupported *UnsupportedError
	for _, entry := range []string{"src/dir.ts", "src", "../other/main.ts", ".history/x.ts", ".History/x.ts", "/main.ts", `\main.ts`, "C:/main.ts", "."} {
		withEntry(entry)
		if _, err := l.ScriptPath("paint"); !errors.As(err, &unsupported) {
			t.Errorf("%q: %v", entry, err)
		}
	}
	fixture(t, dir, "paint", validProfile)
	if _, err := l.ScriptPath("paint"); !errors.Is(err, ErrNoScript) {
		t.Fatal("no script:", err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "main.ts"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "profiles", "paint", "linked")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	withEntry("linked/main.ts")
	if _, err := l.ScriptPath("paint"); !errors.As(err, &unsupported) {
		t.Fatal("through a link:", err)
	}
}

func encodeEntry(entry string) (string, error) {
	quoted := strings.ReplaceAll(entry, `\`, `\\`)
	return `{"schemaVersion":0,"name":"paint","script":{"entry":"` + quoted + `","api":"0.21"},"layers":{"base":{}}}`, nil
}

// A macros folder that is a link is never listed, written or followed.
func TestMacrosFolderLink(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	fixture(t, dir, "paint", validProfile)
	if err := os.WriteFile(filepath.Join(outside, "a.ts"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, profile.MacroDir)); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	l := New(dir)
	var unsupported *UnsupportedError
	if _, err := l.Macros(); !errors.As(err, &unsupported) || unsupported.Path != "macros" {
		t.Fatal(err)
	}
	if err := l.CreateMacro("b.ts", nil, nil); !errors.As(err, &unsupported) {
		t.Fatal(err)
	}
	if _, _, err := l.ImportMacro("a.ts", []byte("x"), true, nil); !errors.As(err, &unsupported) {
		t.Fatal(err)
	}
	if _, err := l.DeleteMacro("a.ts"); !errors.As(err, &unsupported) {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(outside, "a.ts")); err != nil || string(data) != "secret" {
		t.Fatal("wrote through a link", err)
	}
}
