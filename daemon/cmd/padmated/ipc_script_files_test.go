package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/dtsgen"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

var scriptHelpers = []string{"tsconfig.json", "padmate.d.ts", "padmate-globals.d.ts", "lib.padmate-goja.d.ts"}

// projectConfig is the tsconfig.json that script.init and script.import
// create; dtsgen tests that it covers every script in the folder.
func projectConfig(t *testing.T) string {
	t.Helper()
	files, err := dtsgen.Project(dtsgen.HostAPI, dtsgen.MainEntry, nil, nil, nil)
	if err != nil || files[0].Name != "tsconfig.json" {
		t.Fatal(files, err)
	}
	return string(files[0].Data)
}

func readProfileFile(t *testing.T, dir, id, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "profiles", id, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// script.init scaffolds without overwriting, leaves profile.json to the
// client, and its main.ts takes over no control once the profile uses it.
func TestIPCScriptInit(t *testing.T) {
	dir := t.TempDir()
	const paint = `{"schemaVersion":0,"name":"paint","layers":{"base":{"south":{"press":{"type":"keys","keys":"F13"}}}}}`
	writeLibraryProfile(t, dir, "paint", paint)
	writeLibraryProfile(t, dir, "photo", `{"schemaVersion":0,"name":"photo","layers":{"base":{}}}`)
	const work = "// user work\n"
	if err := os.WriteFile(filepath.Join(dir, "profiles", "photo", "main.ts"), []byte(work), 0o600); err != nil {
		t.Fatal(err)
	}
	tl, src := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := rpcTestClient(t, tl.liveRun)
	initScript := func(id string) (rpcScriptInit, error) {
		var result rpcScriptInit
		err := client.CallResult(ctx, "script.init", map[string]string{"id": id}, &result)
		return result, err
	}
	all := append([]string{"main.ts"}, scriptHelpers...)
	if result, err := initScript("paint"); err != nil || result.Entry != "main.ts" || !slices.Equal(result.Created, all) || result.Existing == nil || len(result.Existing) != 0 {
		t.Fatal(result, err)
	}
	if got := readProfileFile(t, dir, "paint", "main.ts"); got != dtsgen.MainTemplate {
		t.Fatal(got)
	}
	if got := readProfileFile(t, dir, "paint", "profile.json"); got != paint {
		t.Fatal("script.init edited profile.json:", got)
	}
	if result, err := initScript("paint"); err != nil || len(result.Created) != 0 || !slices.Equal(result.Existing, all) {
		t.Fatal(result, err)
	}
	// A script imported after the new script is covered by the same
	// tsconfig, which stays as it is.
	var imported rpcScriptImport
	if err := client.CallResult(ctx, "script.import", map[string]any{"id": "paint", "name": "brush.ts", "text": "export {};\n"}, &imported); err != nil || !slices.Equal(imported.Created, []string{"brush.ts"}) {
		t.Fatal(imported, err)
	}
	if got := readProfileFile(t, dir, "paint", "tsconfig.json"); got != projectConfig(t) {
		t.Fatal("tsconfig.json:", got)
	}
	if result, err := initScript("photo"); err != nil || !slices.Equal(result.Created, scriptHelpers) || !slices.Equal(result.Existing, []string{"main.ts"}) {
		t.Fatal(result, err)
	}
	if got := readProfileFile(t, dir, "photo", "main.ts"); got != work {
		t.Fatal("main.ts overwritten:", got)
	}
	_, err := initScript("missing")
	rpcErrorData(t, err, -32004)
	if _, err := os.Stat(filepath.Join(dir, "profiles", "missing")); !os.IsNotExist(err) {
		t.Fatal("script.init created a profile", err)
	}
	for _, id := range []string{"", "../paint", "Paint", "con"} {
		_, err := initScript(id)
		rpcErrorData(t, err, jrpc2.InvalidParams)
	}
	rpcErrorData(t, client.CallResult(ctx, "script.init", map[string]any{"id": "paint", "entry": "x.ts"}, new(any)), jrpc2.InvalidParams)

	// The client references the new main.ts; south keeps its declarative
	// binding although the script is running.
	var document rpcDocument
	if err := client.CallResult(ctx, "profiles.get", map[string]string{"id": "paint"}, &document); err != nil {
		t.Fatal(err)
	}
	withScript := strings.Replace(paint, `"layers"`, `"script":{"entry":"main.ts","api":"0.21"},"layers"`, 1)
	var saved rpcSaved
	if err := client.CallResult(ctx, "profiles.put", map[string]string{"id": "paint", "revision": document.Revision, "text": withScript}, &saved); err != nil || !saved.Applied {
		t.Fatal(saved, err)
	}
	tl.waitUntil(t, "the new script running", func() bool {
		var views []slot.ScriptView
		if err := client.CallResult(ctx, "script.registrations", map[string]string{"device": "1"}, &views); err != nil || len(views) != 1 {
			return false
		}
		return views[0].Script.State == "Running" && views[0].Script.Entry == "main.ts" && len(views[0].Script.Metadata.Registrations) == 0
	})
	button(src, "south", true)
	tl.waitFor(t, tl.stdout, "send +F13\n")
	button(src, "south", false)
	tl.waitFor(t, tl.stdout, "send -F13\n")
}

func TestIPCScriptImport(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{}}}`)
	writeLibraryProfile(t, dir, "photo", `{"schemaVersion":0,"name":"photo","layers":{"base":{}}}`)
	if err := os.Mkdir(filepath.Join(dir, "profiles", "photo", "lib.ts"), 0o700); err != nil {
		t.Fatal(err)
	}
	tl, _ := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := rpcTestClient(t, tl.liveRun)
	importScript := func(id, name, text string, replace bool) (rpcScriptImport, error) {
		var result rpcScriptImport
		err := client.CallResult(ctx, "script.import", map[string]any{"id": id, "name": name, "text": text, "replace": replace}, &result)
		return result, err
	}
	const v1 = "import { key, on } from \"padmate\";\non(\"south\", \"press\", async () => { await key.tap(\"B\"); });\n"
	result, err := importScript("paint", "brush.ts", v1, false)
	if err != nil || result.Entry != "brush.ts" || result.Replaced || !slices.Equal(result.Created, append([]string{"brush.ts"}, scriptHelpers...)) {
		t.Fatal(result, err)
	}
	if got := readProfileFile(t, dir, "paint", "brush.ts"); got != v1 {
		t.Fatal(got)
	}
	if got := readProfileFile(t, dir, "paint", "tsconfig.json"); got != projectConfig(t) {
		t.Fatal("tsconfig.json:", got)
	}

	// An existing script needs replace; the refusal says so.
	_, err = importScript("paint", "brush.ts", "// v2\n", false)
	if data := rpcErrorData(t, err, rpcCodeScriptExists); data != `{"exists":true,"name":"brush.ts"}` {
		t.Fatal(data)
	}
	if got := readProfileFile(t, dir, "paint", "brush.ts"); got != v1 {
		t.Fatal("overwritten without replace:", got)
	}
	if result, err := importScript("paint", "brush.ts", "// v2\n", true); err != nil || !result.Replaced || len(result.Created) != 0 {
		t.Fatal(result, err)
	}
	if got := readProfileFile(t, dir, "paint", "brush.ts"); got != "// v2\n" {
		t.Fatal(got)
	}
	// The editor files exist now. The tsconfig covers every script in the
	// folder, so a second entry, even a JavaScript one, needs no change to
	// it, and it is not rewritten.
	if result, err := importScript("paint", "tools.js", "export {};\n", false); err != nil || !slices.Equal(result.Created, []string{"tools.js"}) {
		t.Fatal(result, err)
	}
	if got := readProfileFile(t, dir, "paint", "tsconfig.json"); got != projectConfig(t) {
		t.Fatal("tsconfig.json:", got)
	}

	for _, name := range []string{"", "../brush.ts", `sub\brush.ts`, "brush.TS", "brush.tsx", ".brush.ts", "padmate.d.ts", "profile.json", "con.ts", "画笔.ts"} {
		_, err := importScript("paint", name, "x", true)
		rpcErrorData(t, err, jrpc2.InvalidParams)
	}
	_, err = importScript("paint", "big.ts", strings.Repeat("a", profilelib.MaxScriptBytes+1), false)
	rpcErrorData(t, err, jrpc2.InvalidParams)
	rpcErrorData(t, client.CallResult(ctx, "script.import", map[string]any{"id": "paint", "name": "none.ts", "replace": false}, new(any)), jrpc2.InvalidParams)
	// Text that is not UTF-8 is refused, not written with U+FFFD in place
	// of what the client sent.
	for _, text := range []string{`"\ud800"`, `"a\udc00b"`, `"\ud83d\ud83d"`, "\"a\xffb\"", "7", "null"} {
		params := json.RawMessage(`{"id":"paint","name":"bad.ts","replace":false,"text":` + text + `}`)
		rpcErrorData(t, client.CallResult(ctx, "script.import", params, new(any)), jrpc2.InvalidParams)
	}
	if _, err := os.Lstat(filepath.Join(dir, "profiles", "paint", "bad.ts")); !os.IsNotExist(err) {
		t.Fatal("wrote invalid text:", err)
	}
	params := json.RawMessage(`{"id":"paint","name":"emoji.ts","replace":false,"text":"// \ud83d\ude00 \\ud800\n"}`)
	if err := client.CallResult(ctx, "script.import", params, new(any)); err != nil {
		t.Fatal(err)
	}
	if got := readProfileFile(t, dir, "paint", "emoji.ts"); got != "// \U0001F600 \\ud800\n" {
		t.Fatalf("emoji.ts = %q", got)
	}
	rpcErrorData(t, client.CallResult(ctx, "script.import", map[string]any{"id": "paint", "name": "x.ts", "text": "", "path": "C:/x.ts"}, new(any)), jrpc2.InvalidParams)
	_, err = importScript("missing", "brush.ts", "x", false)
	rpcErrorData(t, err, -32004)
	_, err = importScript("../paint", "brush.ts", "x", false)
	rpcErrorData(t, err, jrpc2.InvalidParams)
	for _, replace := range []bool{false, true} {
		_, err := importScript("photo", "lib.ts", "x", replace)
		if data := rpcErrorData(t, err, rpcCodeProfileUnsupported); data != `{"path":"photo/lib.ts"}` {
			t.Fatal(data)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "profiles", "paint"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := append([]string{"brush.ts", "emoji.ts", "profile.json", "tools.js"}, scriptHelpers...)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("profile folder holds %v, want %v", names, want)
	}
}

func TestIPCProfileFolder(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{}}}`)
	tl, _ := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := rpcTestClient(t, tl.liveRun)
	folder := func(id string) (string, error) {
		var result struct{ Dir string }
		err := client.CallResult(ctx, "profiles.folder", map[string]string{"id": id}, &result)
		return result.Dir, err
	}
	if got, err := folder("paint"); err != nil || got != filepath.Join(dir, "profiles", "paint") || !filepath.IsAbs(got) {
		t.Fatal(got, err)
	}
	_, err := folder("missing")
	rpcErrorData(t, err, -32004)
	_, err = folder("..")
	rpcErrorData(t, err, jrpc2.InvalidParams)

	// A linked profile folder is neither named nor written.
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "profiles", "escape")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	_, err = folder("escape")
	rpcErrorData(t, err, rpcCodeProfileUnsupported)
	err = client.CallResult(ctx, "script.init", map[string]string{"id": "escape"}, new(any))
	rpcErrorData(t, err, rpcCodeProfileUnsupported)
	err = client.CallResult(ctx, "script.import", map[string]any{"id": "escape", "name": "x.ts", "text": "x"}, new(any))
	rpcErrorData(t, err, rpcCodeProfileUnsupported)
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatal("wrote through a link:", entries, err)
	}
}
