package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

// Profile inheritance in a running daemon (PRO-19, ADR-0070).

const (
	// The parent binds east, and has a layer for the child to use.
	extParentText = `{"schemaVersion":1,"name":"parent","layers":{"base":{"east":{"press":{"type":"keys","keys":"F13"}}},"paint":{"south":{"press":{"type":"keys","keys":"F15"}}}}}`
	// The child binds west, and a layer of its parent.
	extChildText = `{"schemaVersion":1,"name":"child","extends":"parent","layers":{"base":{"west":{"press":{"type":"keys","keys":"F20"}},"north":{"press":{"type":"layer","layer":"paint","op":"toggle"}}}}}`
	// The grandchild only changes the name.
	extGrandchildText = `{"schemaVersion":1,"name":"grandchild","extends":"child","layers":{"base":{}}}`
)

func writeExtendsLibrary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "parent", extParentText)
	writeLibraryProfile(t, dir, "child", extChildText)
	writeLibraryProfile(t, dir, "grandchild", extGrandchildText)
	return dir
}

// tap presses and releases control, and waits until the daemon has sent key
// down and up.
func tap(t *testing.T, tl *testLive, src *fakeSource, control, key string) {
	t.Helper()
	before := strings.Count(tl.stdout.String(), "send +"+key+"\n")
	button(src, control, true)
	tl.waitForCount(t, tl.stdout, "send +"+key+"\n", before+1)
	button(src, control, false)
	tl.waitForCount(t, tl.stdout, "send -"+key+"\n", before+1)
}

func TestExtendsStartsAndRuns(t *testing.T) {
	dir := writeExtendsLibrary(t)
	tl, src := startFocusLive(t, dir, "grandchild", `{"schemaVersion":0}`, false)
	// Its own binding, its parent's and its grandparent's all run.
	tap(t, tl, src, "west", "F20")
	tap(t, tl, src, "east", "F13")
	checkBalanced(t, tl.stdout.String())
	target := tl.profileTarget.Load()
	if target.id != "grandchild" || len(target.parents) != 2 || target.parents[0].ID != "child" || target.parents[1].ID != "parent" {
		t.Errorf("target %+v", target)
	}
	if tl.cfg.profile.Extends != "child" || tl.cfg.profile.Name != "grandchild" {
		t.Errorf("profile %+v", tl.cfg.profile)
	}
}

// A profile of a file by itself has no library to extend from.
func TestExtendsNeedsTheLibrary(t *testing.T) {
	dir := writeExtendsLibrary(t)
	var stderr strings.Builder
	p, _, _, err := loadStartupProfile(options{profile: filepath.Join(dir, "profiles", "child", "profile.json")}, &stderr)
	if err == nil || p != nil {
		t.Fatalf("a file by itself extends: %v", err)
	}
	if !strings.Contains(stderr.String(), "extends works only in a profile of the library") || !strings.Contains(stderr.String(), "(/extends)") {
		t.Errorf("stderr: %s", stderr.String())
	}
	// By its ID it starts.
	stderr.Reset()
	opts := options{dataDir: dir, profileID: "child"}
	opts.library = profilelib.New(dir)
	opts.profile, _ = opts.library.Path("child")
	p, _, _, err = loadStartupProfile(opts, &stderr)
	if err != nil || p == nil || p.Extends != "parent" || stderr.Len() != 0 {
		t.Errorf("by ID: %v %v %q", p, err, stderr.String())
	}
	// A parent that is gone stops the start with why.
	if err := os.RemoveAll(filepath.Join(dir, "profiles", "parent")); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if _, _, _, err := loadStartupProfile(opts, &stderr); err == nil || !strings.Contains(stderr.String(), `parent "parent" is not in the profile library`) {
		t.Errorf("without the parent: %v %q", err, stderr.String())
	}
}

func TestExtendsParentSavedOnDiskReloadsTheChild(t *testing.T) {
	for _, start := range []string{"child", "grandchild"} {
		t.Run(start, func(t *testing.T) {
			dir := writeExtendsLibrary(t)
			tl, src := startFocusLive(t, dir, start, `{"schemaVersion":0}`, true)
			button(src, "east", true)
			tl.waitFor(t, tl.stdout, "send +F13\n")
			// The parent is saved by an editor, with the key held: the child
			// reloads, which releases it, and the new binding runs.
			writeLibraryProfile(t, dir, "parent", strings.ReplaceAll(extParentText, "F13", "F14"))
			tl.waitFor(t, tl.log, `"msg":"profile reloaded"`)
			tl.waitFor(t, tl.stdout, "send -F13\n")
			button(src, "east", false)
			tap(t, tl, src, "east", "F14")
			checkBalanced(t, tl.stdout.String())
			if target := tl.profileTarget.Load(); target.id != start || !target.dependsOn("parent") {
				t.Errorf("target %+v", target)
			}
			// The same bytes again are no change.
			reloads := strings.Count(tl.log.String(), `"msg":"profile reloaded"`)
			writeLibraryProfile(t, dir, "parent", strings.ReplaceAll(extParentText, "F13", "F14"))
			time.Sleep(150 * time.Millisecond)
			tl.sync()
			if got := strings.Count(tl.log.String(), `"msg":"profile reloaded"`); got != reloads {
				t.Errorf("%d reloads after %d: a save of the same bytes reloaded", got, reloads)
			}
			// A change of the grandparent's file reaches the grandchild, and a
			// change of the child's own text the child itself, as before.
			writeLibraryProfile(t, dir, "child", strings.ReplaceAll(extChildText, "F20", "F21"))
			tl.waitForCount(t, tl.log, `"msg":"profile reloaded"`, reloads+1)
			tap(t, tl, src, "west", "F21")
		})
	}
}

// A parent that is saved so that the child does not make sense any more
// leaves the running profile as it is.
func TestExtendsParentChangeThatBreaksTheChild(t *testing.T) {
	dir := writeExtendsLibrary(t)
	tl, src := startFocusLive(t, dir, "child", `{"schemaVersion":0}`, true)
	// The layer paint is the parent's; the child toggles it.
	writeLibraryProfile(t, dir, "parent", `{"schemaVersion":1,"name":"parent","layers":{"base":{"east":{"press":{"type":"keys","keys":"F14"}}}}}`)
	tl.waitFor(t, tl.log, `"msg":"profile not reloaded"`)
	tap(t, tl, src, "east", "F13")
	if !strings.Contains(tl.stderr.String(), `unknown layer "paint"`) || !strings.Contains(tl.stderr.String(), filepath.Join("profiles", "child", "profile.json")) {
		t.Errorf("stderr: %s", tl.stderr.String())
	}
	// The parent is mended: the child reloads.
	writeLibraryProfile(t, dir, "parent", strings.ReplaceAll(extParentText, "F13", "F16"))
	tl.waitFor(t, tl.log, `"msg":"profile reloaded"`)
	tap(t, tl, src, "east", "F16")
	checkBalanced(t, tl.stdout.String())
}

// breakProfileFile makes the profile.json of library profile id unreadable, as
// a program that holds it open without sharing does, by putting a folder in
// its place; mendProfileFile puts text back.
func breakProfileFile(t *testing.T, dir, id string) {
	t.Helper()
	path := filepath.Join(dir, "profiles", id, "profile.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mendProfileFile(t *testing.T, dir, id, text string) {
	t.Helper()
	if err := os.Remove(filepath.Join(dir, "profiles", id, "profile.json")); err != nil {
		t.Fatal(err)
	}
	writeLibraryProfile(t, dir, id, text)
}

// The file of the running child cannot be read at the moment its parent is
// saved. The parent's watcher has seen the save and will not report it
// again, so the reload must not be given up: the child that has been
// reloaded before merges the text it runs from with the new parent at once.
func TestExtendsParentSavedWhileTheChildCannotBeRead(t *testing.T) {
	dir := writeExtendsLibrary(t)
	tl, src := startFocusLive(t, dir, "child", `{"schemaVersion":0}`, true)
	// A reload of the child itself, so that the daemon has its text.
	writeLibraryProfile(t, dir, "child", strings.ReplaceAll(extChildText, "F20", "F21"))
	tl.waitFor(t, tl.log, `"msg":"profile reloaded"`)
	breakProfileFile(t, dir, "child")
	writeLibraryProfile(t, dir, "parent", strings.ReplaceAll(extParentText, "F13", "F14"))
	tl.waitForCount(t, tl.log, `"msg":"profile reloaded"`, 2)
	tap(t, tl, src, "east", "F14")
	tap(t, tl, src, "west", "F21")
	checkBalanced(t, tl.stdout.String())
}

// The profile that the daemon started with has no text of its own to merge
// again, so the reload waits for its file to be readable, a few times.
func TestExtendsParentSavedWhileTheStartupChildCannotBeRead(t *testing.T) {
	dir := writeExtendsLibrary(t)
	tl, src := startFocusLive(t, dir, "child", `{"schemaVersion":0}`, true)
	breakProfileFile(t, dir, "child")
	writeLibraryProfile(t, dir, "parent", strings.ReplaceAll(extParentText, "F13", "F14"))
	tl.waitFor(t, tl.log, `"msg":"profile not reloaded: cannot read it"`)
	mendProfileFile(t, dir, "child", extChildText)
	tl.waitFor(t, tl.log, `"msg":"profile reloaded"`)
	tap(t, tl, src, "east", "F14")
	tap(t, tl, src, "west", "F20")
	checkBalanced(t, tl.stdout.String())
}

func TestExtendsIPCReadsAndWrites(t *testing.T) {
	dir := writeExtendsLibrary(t)
	tl, src := startFocusLive(t, dir, "child", `{"schemaVersion":0}`, false)
	client, events := libraryEvents(context.Background(), t, tl)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// The list names the parent of each profile.
	var items []profilelib.Entry
	if err := client.CallResult(ctx, "profiles.list", nil, &items); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range items {
		if e.Problem != "" {
			t.Errorf("%s: %s", e.ID, e.Problem)
		}
		got[e.ID] = e.Extends
	}
	if got["child"] != "parent" || got["grandchild"] != "child" || got["parent"] != "" {
		t.Errorf("extends %v", got)
	}

	// get: the profile's own text, and what it extends.
	var doc rpcDocument
	if err := client.CallResult(ctx, "profiles.get", map[string]string{"id": "grandchild"}, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Text != extGrandchildText || doc.Extends == nil || doc.Extends.Parent != "child" || !slices.Equal(doc.Extends.Chain, []string{"child", "parent"}) || doc.Problem != "" {
		t.Errorf("get: %+v", doc)
	}
	var plain rpcDocument
	if err := client.CallResult(ctx, "profiles.get", map[string]string{"id": "parent"}, &plain); err != nil || plain.Extends != nil {
		t.Errorf("get of a profile that extends nothing: %+v %v", plain, err)
	}

	// resolve: the merge, and what it took from whom.
	var res rpcResolved
	if err := client.CallResult(ctx, "profiles.resolve", map[string]string{"id": "child"}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Revision != profilelib.Revision([]byte(extChildText)) || res.Problem != "" || strings.Contains(res.Text, "extends") {
		t.Errorf("resolve: %+v", res)
	}
	p, ds := profile.Parse([]byte(res.Text))
	if p == nil || ds.HasErrors() || p.Name != "child" || p.Layer("paint") == nil || len(p.Layer("base").Bindings) != 3 {
		t.Errorf("the merged text: %v\n%s", ds, res.Text)
	}
	wantInherited := []profile.Origin{
		{Pointer: "/layers/base/east", Profile: "parent"},
		{Pointer: "/layers/paint", Profile: "parent"},
	}
	if !slices.Equal(res.Inherited, wantInherited) {
		t.Errorf("inherited %v, want %v", res.Inherited, wantInherited)
	}
	var plainRes rpcResolved
	if err := client.CallResult(ctx, "profiles.resolve", map[string]string{"id": "parent"}, &plainRes); err != nil || plainRes.Text != extParentText || plainRes.Extends != nil || plainRes.Inherited == nil || len(plainRes.Inherited) != 0 {
		t.Errorf("resolve of a profile that extends nothing: %+v %v", plainRes, err)
	}
	rpcErrorData(t, client.CallResult(ctx, "profiles.resolve", map[string]string{"id": "nowhere"}, &res), -32004)

	// validate: a draft that extends, with or without an ID.
	var v rpcValidation
	draft := `{"schemaVersion":1,"name":"draft","extends":"parent","layers":{"base":{"north":{"press":{"type":"layer","layer":"paint","op":"switch"}}}}}`
	if err := client.CallResult(ctx, "profiles.validate", map[string]string{"text": draft}, &v); err != nil || !v.Valid {
		t.Errorf("validate: %+v %v", v, err)
	}
	if err := client.CallResult(ctx, "profiles.validate", map[string]string{"text": strings.Replace(draft, "parent", "nowhere", 1), "id": "draft"}, &v); err != nil || v.Valid ||
		len(v.Diagnostics) != 1 || v.Diagnostics[0].Pointer != "/extends" {
		t.Errorf("validate with a missing parent: %+v %v", v, err)
	}
	// A loop is found by the ID: parent would extend its own child.
	loop := strings.Replace(extParentText, `"name":"parent",`, `"name":"parent","extends":"child",`, 1)
	if err := client.CallResult(ctx, "profiles.validate", map[string]string{"text": loop, "id": "parent"}, &v); err != nil || v.Valid || !strings.Contains(v.Diagnostics[0].Message, "extends loops") {
		t.Errorf("validate of a loop: %+v %v", v, err)
	}
	rpcErrorData(t, client.CallResult(ctx, "profiles.validate", map[string]string{"text": draft, "id": "../x"}, &v), -32602)

	// put: saving the parent changes what the running child does, and says so.
	tap(t, tl, src, "east", "F13")
	var parentDoc rpcDocument
	if err := client.CallResult(ctx, "profiles.get", map[string]string{"id": "parent"}, &parentDoc); err != nil {
		t.Fatal(err)
	}
	changed := strings.ReplaceAll(extParentText, "F13", "F17")
	var saved rpcSaved
	if err := client.CallResult(ctx, "profiles.put", map[string]string{"id": "parent", "revision": parentDoc.Revision, "text": changed}, &saved); err != nil || !saved.Applied {
		t.Fatalf("put: %+v %v", saved, err)
	}
	waitLibraryEvent(ctx, t, events, "saved", "parent")
	tap(t, tl, src, "east", "F17")
	// The same text again is saved and changes nothing.
	target := tl.profileTarget.Load()
	if err := client.CallResult(ctx, "profiles.put", map[string]string{"id": "parent", "revision": saved.Revision, "text": changed}, &saved); err != nil || !saved.Applied {
		t.Fatalf("put again: %+v %v", saved, err)
	}
	if tl.profileTarget.Load() != target {
		t.Error("a save of the same text reloaded the child")
	}
	// A parent put that makes the running child invalid is saved, and not applied.
	broken := `{"schemaVersion":1,"name":"parent","layers":{"base":{}}}`
	if err := client.CallResult(ctx, "profiles.put", map[string]string{"id": "parent", "revision": saved.Revision, "text": broken}, &saved); err != nil || saved.Applied || saved.Problem == "" {
		t.Fatalf("put of a parent that breaks the child: %+v %v", saved, err)
	}
	tap(t, tl, src, "east", "F17")
	// A child cannot be saved with a missing parent or in a loop.
	var childDoc rpcDocument
	if err := client.CallResult(ctx, "profiles.get", map[string]string{"id": "child"}, &childDoc); err != nil {
		t.Fatal(err)
	}
	if childDoc.Problem == "" || len(childDoc.Diagnostics) == 0 {
		t.Errorf("a child whose parent makes it invalid: %+v", childDoc)
	}
	bad := strings.Replace(extChildText, `"parent"`, `"gone"`, 1)
	rpcErrorData(t, client.CallResult(ctx, "profiles.put", map[string]string{"id": "child", "revision": childDoc.Revision, "text": bad}, &saved), -32602)
}

func TestExtendsDeletingTheParent(t *testing.T) {
	dir := writeExtendsLibrary(t)
	tl, src := startFocusLive(t, dir, "child", `{"schemaVersion":0}`, true)
	client, events := libraryEvents(context.Background(), t, tl)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var parentDoc rpcDocument
	if err := client.CallResult(ctx, "profiles.get", map[string]string{"id": "parent"}, &parentDoc); err != nil {
		t.Fatal(err)
	}
	del := func(id, revision string) (rpcDeleted, error) {
		var result rpcDeleted
		err := client.CallResult(ctx, "profiles.delete", map[string]any{"id": id, "revision": revision}, &result)
		return result, err
	}

	// A child that cannot be written whole stops the deletion and changes nothing.
	writeLibraryProfile(t, dir, "orphan", `{"schemaVersion":1,"name":"orphan","extends":"parent","layers":{"base":{"south":{"press":{"type":"layer","layer":"nowhere","op":"switch"}}}}}`)
	data := rpcErrorData(t, func() error { _, err := del("parent", parentDoc.Revision); return err }(), rpcCodeProfileChild)
	if !strings.Contains(data, `"id":"orphan"`) || !strings.Contains(data, `"reason":"invalid"`) || !strings.Contains(data, `unknown layer`) {
		t.Errorf("error data %s", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles", "parent", "profile.json")); err != nil {
		t.Fatal("the parent was deleted", err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "profiles", "orphan")); err != nil {
		t.Fatal(err)
	}

	// The running child reads from its parent while it is deleted.
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +F13\n")
	result, err := del("parent", parentDoc.Revision)
	if err != nil || !slices.Equal(result.Flattened, []string{"child"}) {
		t.Fatalf("delete: %+v %v", result, err)
	}
	waitLibraryEvent(ctx, t, events, "saved", "child")
	waitLibraryEvent(ctx, t, events, "deleted", "parent")
	if _, err := os.Stat(filepath.Join(dir, "profiles", "parent")); !os.IsNotExist(err) {
		t.Fatal("the parent is still there", err)
	}
	text, err := os.ReadFile(filepath.Join(dir, "profiles", "child", "profile.json"))
	if err != nil || strings.Contains(string(text), "extends") {
		t.Fatalf("child:\n%s\n%v", text, err)
	}
	p, ds := profile.Parse(text)
	if p == nil || ds.HasErrors() || p.Name != "child" || p.Layer("paint") == nil {
		t.Errorf("the child does not stand by itself: %v\n%s", ds, text)
	}
	// The key held through the reload is released, and the child goes on
	// doing what it did, with nothing left of its parent.
	tl.waitFor(t, tl.stdout, "send -F13\n")
	button(src, "east", false)
	tap(t, tl, src, "east", "F13")
	tap(t, tl, src, "west", "F20")
	checkBalanced(t, tl.stdout.String())
	target := tl.profileTarget.Load()
	if target.id != "child" || len(target.parents) != 0 {
		t.Errorf("target %+v", target)
	}
	// And the grandchild, which extended the child, extends it still.
	var grandchild rpcDocument
	if err := client.CallResult(ctx, "profiles.get", map[string]string{"id": "grandchild"}, &grandchild); err != nil || grandchild.Problem != "" || grandchild.Extends == nil || !slices.Equal(grandchild.Extends.Chain, []string{"child"}) {
		t.Errorf("grandchild: %+v %v", grandchild, err)
	}
}

func TestExtendsInheritedScriptStopsDeleteAndExport(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "scripted", `{"schemaVersion":1,"name":"S","script":{"entry":"main.ts","api":"0.17"},"layers":{"base":{"south":{"press":{"type":"script","name":"go"}}}}}`)
	writeLibraryProfile(t, dir, "user", `{"schemaVersion":1,"name":"U","extends":"scripted","layers":{"base":{}}}`)
	if err := os.WriteFile(filepath.Join(dir, "profiles", "scripted", "main.ts"), []byte(`import {defineScript} from "padmate";
export default defineScript({setup(){}});
`), 0o600); err != nil {
		t.Fatal(err)
	}
	tl, _ := startFocusLive(t, dir, "", `{"schemaVersion":0}`, false)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	revision := profilelib.Revision([]byte(`{"schemaVersion":1,"name":"S","script":{"entry":"main.ts","api":"0.17"},"layers":{"base":{"south":{"press":{"type":"script","name":"go"}}}}}`))
	data := rpcErrorData(t, client.CallResult(ctx, "profiles.delete", map[string]any{"id": "scripted", "revision": revision}, new(any)), rpcCodeProfileChild)
	if !strings.Contains(data, `"id":"user"`) || !strings.Contains(data, `"reason":"script"`) {
		t.Errorf("delete: %s", data)
	}
	rpcErrorData(t, client.CallResult(ctx, "profiles.export", map[string]any{"id": "user"}, new(any)), rpcCodeProfileUnsupported)
	// The script of the parent is found where its files are.
	var path struct {
		Path string `json:"path"`
	}
	if err := client.CallResult(ctx, "script.path", map[string]any{"id": "user"}, &path); err != nil || path.Path != filepath.Join(dir, "profiles", "scripted", "main.ts") {
		t.Errorf("script.path: %+v %v", path, err)
	}
}

// A script that a profile inherits runs from the folder of the parent that has
// it.
func TestExtendsInheritedScriptRuns(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "scripted", `{"schemaVersion":1,"name":"S","script":{"entry":"main.ts","api":"0.17"},"layers":{"base":{}}}`)
	writeLibraryProfile(t, dir, "user", `{"schemaVersion":1,"name":"U","extends":"scripted","layers":{"base":{"east":{"press":{"type":"keys","keys":"F13"}}}}}`)
	srcText := `import {defineScript,log} from "padmate";
export default defineScript({setup(){log.info("inherited script ready");}});`
	if err := os.WriteFile(filepath.Join(dir, "profiles", "scripted", "main.ts"), []byte(srcText), 0o600); err != nil {
		t.Fatal(err)
	}
	tl, src := startFocusLive(t, dir, "user", `{"schemaVersion":0}`, false)
	tl.waitFor(t, tl.log, "inherited script ready")
	if tl.cfg.profile.Script == nil || tl.cfg.profile.Script.Origin != "scripted" {
		t.Errorf("script %+v", tl.cfg.profile.Script)
	}
	// The profile's own bindings still run beside it.
	tap(t, tl, src, "east", "F13")
}

// Saving the running child itself applies at once, with its parents merged.
func TestExtendsPutTheRunningChild(t *testing.T) {
	dir := writeExtendsLibrary(t)
	tl, src := startFocusLive(t, dir, "child", `{"schemaVersion":0}`, false)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var doc rpcDocument
	if err := client.CallResult(ctx, "profiles.get", map[string]string{"id": "child"}, &doc); err != nil {
		t.Fatal(err)
	}
	changed := strings.ReplaceAll(doc.Text, "F20", "F21")
	var saved rpcSaved
	if err := client.CallResult(ctx, "profiles.put", map[string]string{"id": "child", "revision": doc.Revision, "text": changed}, &saved); err != nil || !saved.Applied || saved.Problem != "" {
		t.Fatalf("put: %+v %v", saved, err)
	}
	tap(t, tl, src, "west", "F21")
	// The parent's binding is still there.
	tap(t, tl, src, "east", "F13")
	// A child with another parent runs with that one.
	other := strings.Replace(changed, `"extends":"parent"`, `"extends":"grandchild"`, 1)
	if other == changed {
		t.Fatal("test text")
	}
	if err := client.CallResult(ctx, "profiles.put", map[string]string{"id": "child", "revision": saved.Revision, "text": other}, &saved); err == nil {
		t.Fatal("a profile that extends its own child was saved")
	}
	tl.sync()
	if tl.profileTarget.Load().parents[0].ID != "parent" {
		t.Errorf("parents %v", tl.profileTarget.Load().parents)
	}
}
