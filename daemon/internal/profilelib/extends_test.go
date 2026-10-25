package profilelib

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Profile inheritance in the library (PRO-19, ADR-0070).

const (
	// extParent has a layer with two bindings, one with a macro, and a timing.
	extParent = `{
  "schemaVersion": 1,
  "name": "绘画",
  "timing": {"holdMs": 280},
  "layers": {
    "base": {
      "south": {"label": "撤销", "press": {"type": "keys", "keys": "Ctrl+Z"}},
      "east": {"label": "复制", "press": {"type": "macro", "file": "copy.ts"}}
    },
    "paint": {"north": {"press": {"type": "keys", "keys": "B"}}}
  }
}
`
	// extChild changes east and has its own layer.
	extChild = `{
  "schemaVersion": 1,
  "name": "绘画左手",
  "extends": "paint",
  "layers": {
    "base": {"east": {"label": "粘贴", "press": {"type": "keys", "keys": "Ctrl+V"}}},
    "extra": {"west": {"press": {"type": "keys", "keys": "E"}}}
  }
}
`
	// extGrandchild builds on the child.
	extGrandchild = `{
  "schemaVersion": 1,
  "name": "绘画左手小",
  "extends": "left",
  "timing": {"holdMs": 150},
  "layers": {"base": {}}
}
`
)

func extLibrary(t *testing.T) (*Library, string) {
	t.Helper()
	dir := t.TempDir()
	fixture(t, dir, "paint", extParent)
	fixture(t, dir, "left", extChild)
	fixture(t, dir, "small", extGrandchild)
	putMacroFiles(t, dir, map[string]string{"copy.ts": "export default () => {};\n"})
	return New(dir), dir
}

func TestLoadMergesTheChain(t *testing.T) {
	l, _ := extLibrary(t)
	doc, err := l.Load("small")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Extends != "left" || doc.Name != "绘画左手小" || string(doc.Data) != extGrandchild || doc.Revision != Revision([]byte(extGrandchild)) {
		t.Errorf("document %+v", doc.Entry)
	}
	p := doc.Profile
	if p.Extends != "left" || len(p.Parents) != 2 || p.Parents[0].ID != "left" || p.Parents[1].ID != "paint" {
		t.Errorf("extends %q parents %v", p.Extends, p.Parents)
	}
	if p.Timing.HoldMs != 150 {
		t.Errorf("timing %+v", p.Timing)
	}
	base := p.Layer("base")
	if got := base.Binding(control.East); got == nil || got.Label != "粘贴" {
		t.Errorf("east %+v", got)
	}
	if got := base.Binding(control.South); got == nil || got.Label != "撤销" {
		t.Errorf("south %+v", got)
	}
	if p.Layer("paint") == nil || p.Layer("extra") == nil {
		t.Errorf("layers %v", p.Layers)
	}
	if files := p.MacroFiles(); len(files) != 0 {
		t.Errorf("macros %v: the child replaced the macro", files)
	}
	if doc.Resolved == nil || len(doc.Resolved.Inherited()) == 0 {
		t.Error("no origins")
	}

	// The list shows the parent a profile names, with the profile.
	entries, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range entries {
		if e.Problem != "" {
			t.Errorf("%s: %s", e.ID, e.Problem)
		}
		got[e.ID] = e.Extends
	}
	if want := map[string]string{"paint": "", "left": "paint", "small": "left"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("extends %v, want %v", got, want)
	}
}

func TestLoadReportsAParentThatIsMissingOrBroken(t *testing.T) {
	l, dir := extLibrary(t)
	// The parent is gone: the child stays in the list, with why and what it names.
	if err := os.RemoveAll(filepath.Join(dir, "profiles", "paint")); err != nil {
		t.Fatal(err)
	}
	doc, err := l.Load("left")
	if err == nil || doc.Profile != nil {
		t.Fatalf("loaded %v %v", doc, err)
	}
	if doc.Extends != "paint" || len(doc.Diagnostics) != 1 || doc.Diagnostics[0].Message != `parent "paint" is not in the profile library` ||
		!strings.Contains(err.Error(), `parent "paint" is not in the profile library`) {
		t.Errorf("%+v, %v", doc.Entry, err)
	}
	entries, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		switch e.ID {
		case "left", "small":
			if e.Problem == "" || e.Extends == "" {
				t.Errorf("%s: %+v", e.ID, e)
			}
		}
	}

	// A parent with an error of its own: the child says which, the parent's
	// diagnostics are in ParentDiagnostics, not in the child's.
	fixture(t, dir, "paint", strings.Replace(extParent, `"keys": "Ctrl+Z"`, `"keys": "Hyper+"`, 1))
	doc, err = l.Load("left")
	if err == nil {
		t.Fatal("loaded")
	}
	if len(doc.Diagnostics) != 1 || doc.Diagnostics[0].Pointer != "/extends" || !strings.Contains(doc.Diagnostics[0].Message, `parent "paint" has errors`) {
		t.Errorf("diagnostics %v", doc.Diagnostics)
	}
	if len(doc.ParentDiagnostics) != 1 || doc.ParentDiagnostics[0].Profile != "paint" {
		t.Errorf("parent diagnostics %v", doc.ParentDiagnostics)
	}
	if !strings.Contains(err.Error(), `parent "paint" has errors`) {
		t.Errorf("error %v", err)
	}
}

func TestPutAChildChecksItsParents(t *testing.T) {
	l, _ := extLibrary(t)
	saved, err := l.Put("mine", "", []byte(`{"schemaVersion":1,"name":"Mine","extends":"paint","layers":{"base":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := l.Read("mine"); !strings.Contains(string(got), `"extends"`) || saved.Revision != Revision(got) {
		t.Errorf("saved %q", got)
	}
	for name, text := range map[string]string{
		"missing parent": `{"schemaVersion":1,"name":"Mine","extends":"gone","layers":{"base":{}}}`,
		"itself":         `{"schemaVersion":1,"name":"Mine","extends":"mine","layers":{"base":{}}}`,
		"bad id":         `{"schemaVersion":1,"name":"Mine","extends":"../x","layers":{"base":{}}}`,
	} {
		_, err := l.Put("mine", saved.Revision, []byte(text))
		var invalid *profile.Error
		if !errors.As(err, &invalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got, _ := l.Read("mine"); Revision(got) != saved.Revision {
		t.Error("a refused put changed the file")
	}
	// A parent cannot extend its own child.
	loop := strings.Replace(extParent, `"name": "绘画",`, `"name": "绘画", "extends": "mine",`, 1)
	_, err = l.Put("paint", Revision([]byte(extParent)), []byte(loop))
	var invalid *profile.Error
	if !errors.As(err, &invalid) || !strings.Contains(err.Error(), "extends loops") {
		t.Errorf("a loop was saved: %v", err)
	}
}

func TestChildrenAndParents(t *testing.T) {
	l, dir := extLibrary(t)
	fixture(t, dir, "plain", validProfile)
	fixture(t, dir, "broken", "{")
	fixture(t, dir, "another", `{"schemaVersion":1,"name":"x","extends":"paint","layers":{"base":{}}}`)
	for id, want := range map[string][]string{
		"paint": {"another", "left"},
		"left":  {"small"},
		"small": nil,
		"plain": nil,
		"gone":  nil,
	} {
		got, err := l.Children(id)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("Children(%q) = %v, %v; want %v", id, got, err, want)
		}
	}
	for id, want := range map[string][]string{
		"small":  {"left", "paint"},
		"left":   {"paint"},
		"paint":  nil,
		"broken": nil,
		"gone":   nil,
	} {
		if got := l.Parents(id); !slices.Equal(got, want) {
			t.Errorf("Parents(%q) = %v; want %v", id, got, want)
		}
	}
	// A loop or a chain that is too long ends the walk.
	fixture(t, dir, "a", `{"schemaVersion":1,"name":"a","extends":"b","layers":{"base":{}}}`)
	fixture(t, dir, "b", `{"schemaVersion":1,"name":"b","extends":"a","layers":{"base":{}}}`)
	if got := l.Parents("a"); !slices.Equal(got, []string{"b"}) {
		t.Errorf("loop: %v", got)
	}
	fixture(t, dir, "self", `{"schemaVersion":1,"name":"s","extends":"self","layers":{"base":{}}}`)
	if got := l.Parents("self"); got != nil {
		t.Errorf("itself: %v", got)
	}
	for i := range 8 {
		fixture(t, dir, fmt.Sprintf("n%d", i), fmt.Sprintf(`{"schemaVersion":1,"name":"n","extends":"n%d","layers":{"base":{}}}`, i+1))
	}
	if got := l.Parents("n0"); len(got) != profile.MaxExtendsDepth {
		t.Errorf("long chain: %v", got)
	}
}

// Deleting a parent writes its children whole first.
func TestRemoveWritesChildrenWhole(t *testing.T) {
	l, dir := extLibrary(t)
	fixture(t, dir, "plain", validProfile)
	before := map[string]*profile.Profile{}
	for _, id := range []string{"left", "small"} {
		doc, err := l.Load(id)
		if err != nil {
			t.Fatal(err)
		}
		before[id] = doc.Profile
	}
	leftBefore, _ := l.Read("left")

	edits, err := l.RemoveFlattening("paint", Revision([]byte(extParent)))
	if err != nil {
		t.Fatal(err)
	}
	// Only the child that named it, not the grandchild, which names the child.
	if len(edits) != 1 || edits[0].ID != "left" || string(edits[0].Old) != string(leftBefore) || edits[0].Saved.Revision != Revision(edits[0].Data) {
		t.Fatalf("edits %+v", edits)
	}
	if _, err := l.Read("paint"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the parent is still there", err)
	}
	flat, err := l.Read("left")
	if err != nil || string(flat) != string(edits[0].Data) || strings.Contains(string(flat), "extends") {
		t.Fatalf("left:\n%s\n%v", flat, err)
	}
	// Nothing changed in what the profiles do.
	for _, id := range []string{"left", "small"} {
		doc, err := l.Load(id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		want, got := *before[id], *doc.Profile
		want.Extends, want.Parents, got.Extends, got.Parents = "", nil, "", nil
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s changed:\n got %+v\nwant %+v", id, got, want)
		}
	}
	// The grandchild now has one parent.
	if doc, _ := l.Load("small"); len(doc.Profile.Parents) != 1 || doc.Profile.Parents[0].ID != "left" {
		t.Errorf("small: %+v", doc.Profile.Parents)
	}
	// The old text of the child is a snapshot.
	snapshots, err := l.History("left")
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("history %v %v", snapshots, err)
	}
	// A profile that extends nothing needs no flattening.
	edits, err = l.RemoveFlattening("plain", Revision([]byte(validProfile)))
	if err != nil || len(edits) != 0 {
		t.Errorf("plain: %v %v", edits, err)
	}
	assertNoCopyLeftovers(t, dir, "left", "small")
}

// A write that fails puts back the ones before it, and nothing is deleted.
func TestRemoveFlatteningIsAllOrNothing(t *testing.T) {
	l, dir := extLibrary(t)
	fixture(t, dir, "right", `{"schemaVersion":1,"name":"右手","extends":"paint","layers":{"base":{"north":{"press":{"type":"keys","keys":"N"}}}}}`)
	fixture(t, dir, "zzz", `{"schemaVersion":1,"name":"最后","extends":"paint","layers":{"base":{}}}`)
	snapshot := func() map[string]string {
		got := map[string]string{}
		ids, _ := l.IDs()
		for _, id := range ids {
			data, err := l.Read(id)
			if err != nil {
				t.Fatal(err)
			}
			got[id] = string(data)
		}
		return got
	}
	before := snapshot()
	// The third write fails: left and right are written, zzz fails.
	failing := errors.New("disk full")
	l.setPutHook(func(id string) error {
		if id == "zzz" {
			return failing
		}
		return nil
	})
	_, err := l.RemoveFlattening("paint", Revision([]byte(extParent)))
	if !errors.Is(err, failing) {
		t.Fatalf("error %v", err)
	}
	l.setPutHook(nil)
	if after := snapshot(); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Errorf("the library changed:\n got %v\nwant %v", after, before)
	}
	// And the undo itself failing is reported with the cause.
	l.setPutHook(func(id string) error {
		if id == "zzz" {
			return failing
		}
		if id == "left" {
			if data, _ := l.Read("left"); !strings.Contains(string(data), `"extends"`) {
				return errors.New("cannot put back")
			}
		}
		return nil
	})
	_, err = l.RemoveFlattening("paint", Revision([]byte(extParent)))
	l.setPutHook(nil)
	if !errors.Is(err, failing) || !strings.Contains(err.Error(), "cannot put back") {
		t.Errorf("error %v", err)
	}
	// With nothing in the way it works.
	if _, err := l.Load("zzz"); err != nil {
		t.Fatal(err)
	}
	if err := l.Remove("paint", Revision([]byte(extParent))); err != nil {
		t.Fatalf("%v", err)
	}
	for _, id := range []string{"left", "right", "zzz"} {
		if data, err := l.Read(id); err != nil || strings.Contains(string(data), "extends") {
			t.Errorf("%s: %s %v", id, data, err)
		}
	}
}

// A delete that fails leaves the history of the children as it was: the
// writes and the undo of the flattening keep no snapshot that the user never
// saved, however often it is tried.
func TestFailedRemoveLeavesTheHistoryAsItWas(t *testing.T) {
	l, dir := extLibrary(t)
	fixture(t, dir, "right", `{"schemaVersion":1,"name":"右手","extends":"paint","layers":{"base":{"north":{"press":{"type":"keys","keys":"N"}}}}}`)
	fixture(t, dir, "zzz", `{"schemaVersion":1,"name":"最后","extends":"paint","layers":{"base":{}}}`)
	// left has an earlier version of its own, and right none.
	older := strings.Replace(extChild, `"绘画左手"`, `"绘画左手旧"`, 1)
	saved, err := l.Put("left", Revision([]byte(extChild)), []byte(older))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put("left", saved.Revision, []byte(extChild)); err != nil {
		t.Fatal(err)
	}
	versions := func(id string) []string {
		var got []string
		for _, s := range history(t, l, id) {
			got = append(got, s.Version)
		}
		return got
	}
	before := map[string][]string{"left": versions("left"), "right": versions("right")}
	if len(before["left"]) != 2 || len(before["right"]) != 0 {
		t.Fatalf("history before: %v", before)
	}
	failing := errors.New("disk full")
	l.setPutHook(func(id string) error {
		if id == "zzz" {
			return failing
		}
		return nil
	})
	defer l.setPutHook(nil)
	for attempt := range 4 {
		if _, err := l.RemoveFlattening("paint", Revision([]byte(extParent))); !errors.Is(err, failing) {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		for _, id := range []string{"left", "right"} {
			if got := versions(id); !slices.Equal(got, before[id]) {
				t.Fatalf("attempt %d: history of %s is %v, was %v", attempt, id, got, before[id])
			}
			if got, err := l.Read(id); err != nil || !strings.Contains(string(got), `"extends"`) {
				t.Fatalf("attempt %d: %s was left written whole: %s %v", attempt, id, got, err)
			}
		}
	}
	// What a delete that works keeps is still the old text of each child.
	l.setPutHook(nil)
	if err := l.Remove("paint", Revision([]byte(extParent))); err != nil {
		t.Fatal(err)
	}
	if got := versions("right"); len(got) != 1 {
		t.Errorf("history of right after the delete: %v", got)
	}
	if got := versions("left"); len(got) != 3 {
		t.Errorf("history of left after the delete: %v", got)
	}
}

// A profile that cannot be read cannot be shown to extend the one that is
// deleted, so it does not stop the delete, as Children does not list it.
func TestRemoveIgnoresAnUnreadableProfile(t *testing.T) {
	l, dir := extLibrary(t)
	fixture(t, dir, "plain", validProfile)
	// Over the size limit, and a folder where the file should be.
	fixture(t, dir, "big", strings.Repeat(" ", MaxDocumentBytes+1))
	fixture(t, dir, "isdir", "")
	file := filepath.Join(dir, "profiles", "isdir", "profile.json")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := l.Remove("plain", Revision([]byte(validProfile))); err != nil {
		t.Fatalf("a profile with no children was not deleted: %v", err)
	}
	// The ones that extend the parent are still written whole.
	edits, err := l.RemoveFlattening("paint", Revision([]byte(extParent)))
	if err != nil || len(edits) != 1 || edits[0].ID != "left" {
		t.Fatalf("%v %v", edits, err)
	}
	if got, err := l.Read("left"); err != nil || strings.Contains(string(got), "extends") {
		t.Errorf("left: %s %v", got, err)
	}
	for _, id := range []string{"big", "isdir"} {
		if _, err := os.Stat(filepath.Join(dir, "profiles", id)); err != nil {
			t.Errorf("%s: %v", id, err)
		}
	}
}

func TestRemoveRefusedForAChildThatCannotBeWrittenWhole(t *testing.T) {
	l, dir := extLibrary(t)
	// A child with an error of its own.
	fixture(t, dir, "bad", `{"schemaVersion":1,"name":"坏","extends":"paint","layers":{"base":{"south":{"press":{"type":"layer","layer":"nowhere","op":"switch"}}}}}`)
	before, _ := l.Read("left")
	err := l.Remove("paint", Revision([]byte(extParent)))
	var child *ChildError
	if !errors.As(err, &child) || child.ID != "bad" {
		t.Fatalf("error %v", err)
	}
	var invalid *profile.Error
	if !errors.As(err, &invalid) {
		t.Errorf("no diagnostics in %v", err)
	}
	if _, err := l.Read("paint"); err != nil {
		t.Error("the parent was deleted")
	}
	if after, _ := l.Read("left"); string(after) != string(before) {
		t.Error("another child was written")
	}
	if err := os.RemoveAll(filepath.Join(dir, "profiles", "bad")); err != nil {
		t.Fatal(err)
	}

	// A child that inherits the script of the parent: the files of a script
	// stay in the parent's folder.
	script := `{"schemaVersion":1,"name":"S","script":{"entry":"main.ts","api":"0.17"},"layers":{"base":{"south":{"press":{"type":"script","name":"go"}}}}}`
	fixture(t, dir, "scripted", script)
	fixture(t, dir, "user", `{"schemaVersion":1,"name":"U","extends":"scripted","layers":{"base":{}}}`)
	err = l.Remove("scripted", Revision([]byte(script)))
	if !errors.As(err, &child) || child.ID != "user" || !errors.Is(err, ErrInheritedScript) {
		t.Fatalf("error %v", err)
	}
	// A child with a script of its own is fine.
	fixture(t, dir, "user", `{"schemaVersion":1,"name":"U","extends":"scripted","script":{"entry":"mine.ts","api":"0.17"},"layers":{"base":{"south":{"press":{"type":"script","name":"go"}}}}}`)
	if err := l.Remove("scripted", Revision([]byte(script))); err != nil {
		t.Fatal(err)
	}
}

func TestExportWritesAChildWhole(t *testing.T) {
	l, dir := extLibrary(t)
	putMacroFiles(t, dir, map[string]string{"paste.ts": "export default () => {};\n"})
	// The parent's macro is in the child's export too: it is part of the child.
	fixture(t, dir, "left", strings.Replace(extChild, `"press": {"type": "keys", "keys": "Ctrl+V"}`, `"press": {"type": "keys", "keys": "Ctrl+V"}},
      "north": {"press": {"type": "macro", "file": "copy.ts"}`, 1))
	exported, err := l.Export("left", "test")
	if err != nil {
		t.Fatal(err)
	}
	a, err := ReadArchive(exported.Data)
	if err != nil {
		t.Fatal(err)
	}
	if a.Manifest.Name != "绘画左手" || a.Profile.Extends != "" || len(a.Profile.Parents) != 0 {
		t.Errorf("archive %+v %+v", a.Manifest, a.Profile)
	}
	if got := a.Profile.Layer("base").Binding(control.South); got == nil || got.Label != "撤销" {
		t.Errorf("inherited south: %+v", got)
	}
	if a.Profile.Layer("paint") == nil || a.Profile.Layer("extra") == nil || a.Profile.Timing.HoldMs != 280 {
		t.Errorf("profile %+v", a.Profile)
	}
	paths := strings.Join(a.Paths(), " ")
	if !strings.Contains(paths, "macros/copy.ts") {
		t.Errorf("paths %s: the inherited macro is missing", paths)
	}
	// The child in the library keeps its parent.
	if data, _ := l.Read("left"); !strings.Contains(string(data), `"extends": "paint"`) {
		t.Errorf("the library's child changed:\n%s", data)
	}

	// Imports are flat: an archive whose profile.json extends is not one.
	bad := makeZip(t,
		zipEntry{name: ArchiveManifest, data: testManifest},
		zipEntry{name: "profile/profile.json", data: extChild})
	_, err = ReadArchive(bad)
	var archive *ArchiveError
	if !errors.As(err, &archive) || archive.Reason != ArchiveProfileReason || len(archive.Diagnostics) != 1 ||
		!strings.Contains(archive.Diagnostics[0].Message, "only in a profile of the library") {
		t.Errorf("%v", err)
	}
}

func TestExportRefusedForAnInheritedScript(t *testing.T) {
	l, dir := extLibrary(t)
	fixture(t, dir, "scripted", `{"schemaVersion":1,"name":"S","script":{"entry":"main.ts","api":"0.17"},"layers":{"base":{"south":{"press":{"type":"script","name":"go"}}}}}`)
	fixture(t, dir, "user", `{"schemaVersion":1,"name":"U","extends":"scripted","layers":{"base":{}}}`)
	_, err := l.Export("user", "test")
	var unsupported *UnsupportedError
	if !errors.As(err, &unsupported) || !strings.Contains(unsupported.Reason, "inherits the script") {
		t.Errorf("%v", err)
	}
	// A child with errors is reported as such.
	fixture(t, dir, "broken", `{"schemaVersion":1,"name":"B","extends":"gone","layers":{"base":{}}}`)
	_, err = l.Export("broken", "test")
	var invalid *profile.Error
	if !errors.As(err, &invalid) || !strings.Contains(err.Error(), `parent "gone" is not in the profile library`) {
		t.Errorf("%v", err)
	}
}

func TestRestoreAndMacroEditsOfAChild(t *testing.T) {
	l, dir := extLibrary(t)
	// A macro rename edits the file that has the action, the parent's.
	edit, err := l.RenameMacro("copy.ts", "copy 2.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(editedIDs(edit), []string{"paint"}) {
		t.Errorf("edited %v", editedIDs(edit))
	}
	doc, err := l.Load("left")
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Profile.Layer("base").Binding(control.East); got == nil || got.Press.Type != profile.ActionKeys {
		t.Errorf("east %+v", got)
	}
	// A child that runs the macro itself is edited as well, and stays a child.
	fixture(t, dir, "mac", `{"schemaVersion":1,"name":"M","extends":"paint","layers":{"base":{"west":{"press":{"type":"macro","file":"copy 2.ts"}}}}}`)
	putMacroFiles(t, dir, map[string]string{"copy 2.ts": "export default () => {};\n"})
	edit, err = l.RenameMacro("copy 2.ts", "copy 3.ts")
	if err != nil || !slices.Equal(editedIDs(edit), []string{"mac", "paint"}) {
		t.Fatalf("%v %v", editedIDs(edit), err)
	}
	if data, _ := l.Read("mac"); !strings.Contains(string(data), `"extends":"paint"`) || !strings.Contains(string(data), `"copy 3.ts"`) {
		t.Errorf("mac:\n%s", data)
	}
	// A snapshot of a child is validated with the library.
	history, err := l.History("mac")
	if err != nil || len(history) != 1 {
		t.Fatalf("%v %v", history, err)
	}
	cur, _ := l.Read("mac")
	if _, _, err := l.Restore("mac", history[0].Version, Revision(cur)); err != nil {
		t.Fatal(err)
	}
	if data, _ := l.Read("mac"); !strings.Contains(string(data), `"copy 2.ts"`) {
		t.Errorf("restored:\n%s", data)
	}
}

func TestScriptPathOfAnInheritedScript(t *testing.T) {
	l, dir := extLibrary(t)
	fixture(t, dir, "scripted", `{"schemaVersion":1,"name":"S","script":{"entry":"main.ts","api":"0.17"},"layers":{"base":{"south":{"press":{"type":"script","name":"go"}}}}}`)
	writeFile(t, filepath.Join(dir, "profiles", "scripted", "main.ts"), "export {};\n")
	fixture(t, dir, "user", `{"schemaVersion":1,"name":"U","extends":"scripted","layers":{"base":{}}}`)
	got, err := l.ScriptPath("user")
	want := filepath.Join(dir, "profiles", "scripted", "main.ts")
	if err != nil || got != want {
		t.Errorf("ScriptPath = %q, %v; want %q", got, err, want)
	}
}
