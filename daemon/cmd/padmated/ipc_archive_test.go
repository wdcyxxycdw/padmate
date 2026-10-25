package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/dtsgen"
)

// exportedArchive exports profile text, with files beside it, from a
// library of its own and returns the archive as profiles.import takes it.
func exportedArchive(t *testing.T, text string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "src", text)
	for name, data := range files {
		path := filepath.Join(dir, "profiles", "src", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	exported, err := profilelib.New(dir).Export("src", "test")
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(exported.Data)
}

// unsafeArchive is a valid archive with an entry that leaves the profile
// folder, as profiles.import takes it.
func unsafeArchive(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, data := range map[string]string{
		profilelib.ArchiveManifest: `{"type":"padmate-profile","format":1}`,
		"profile/profile.json":     `{"schemaVersion":0,"name":"x","layers":{"base":{}}}`,
		"profile/../x.ts":          "",
	} {
		f, err := w.Create(name)
		if err == nil {
			_, err = f.Write([]byte(data))
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestIPCExportImportProfile(t *testing.T) {
	dir := t.TempDir()
	const original = `{"schemaVersion":0,"name":"paint","script":{"entry":"main.ts","api":"0.21"},"layers":{"base":{}}}`
	writeLibraryProfile(t, dir, "paint", original)
	script := `import {defineScript,log,profiles} from "padmate";
import {tag} from "./lib/util";
export default defineScript({setup(){log.info("imported script ready", tag, profiles.current);}});`
	for name, text := range map[string]string{"main.ts": script, "lib/util.ts": `export const tag = "with-util";` + "\n"} {
		path := filepath.Join(dir, "profiles", "paint", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tl, _ := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	tl.waitFor(t, tl.log, "imported script ready with-util paint")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, events := libraryEvents(ctx, t, tl)
	var exported rpcExported
	if err := client.CallResult(ctx, "profiles.export", map[string]string{"id": "paint"}, &exported); err != nil || exported.Name != "paint" {
		t.Fatal(exported.Name, err)
	}
	data, err := base64.StdEncoding.DecodeString(exported.Data)
	if err != nil {
		t.Fatal(err)
	}
	if a, err := profilelib.ReadArchive(data); err != nil || a.Manifest.Padmate == "" {
		t.Fatal(err)
	}
	importProfile := func(params map[string]any, result any) error {
		return client.CallResult(ctx, "profiles.import", params, result)
	}
	// The name is taken: the client asks to replace or to rename.
	if taken := rpcErrorData(t, importProfile(map[string]any{"data": exported.Data}, new(any)), rpcCodeProfileNameTaken); taken != `{"id":"paint","name":"paint"}` {
		t.Fatal(taken)
	}
	var imported rpcImported
	if err := importProfile(map[string]any{"data": exported.Data, "name": "paint 2"}, &imported); err != nil || imported.ID != "paint-2" || imported.Name != "paint 2" || imported.Applied {
		t.Fatalf("%+v %v", imported, err)
	}
	e := waitLibraryEvent(ctx, t, events, "saved", "paint-2")
	if e["revision"] != imported.Revision || e["applied"] != false {
		t.Fatal(e)
	}
	// The imported script runs from its own folder.
	if err := client.CallResult(ctx, "profiles.activate", map[string]string{"id": "paint-2"}, new(any)); err != nil {
		t.Fatal(err)
	}
	tl.waitFor(t, tl.log, "imported script ready with-util paint-2")

	for _, tc := range []struct {
		method string
		params map[string]any
		code   jrpc2.Code
		data   string
	}{
		{"profiles.export", map[string]any{"id": "missing"}, -32004, ""},
		{"profiles.export", map[string]any{"id": "../paint"}, jrpc2.InvalidParams, ""},
		{"profiles.export", map[string]any{"id": "paint", "extra": 1}, jrpc2.InvalidParams, ""},
		{"profiles.import", map[string]any{}, jrpc2.InvalidParams, ""},
		{"profiles.import", map[string]any{"data": "not base64!"}, jrpc2.InvalidParams, ""},
		{"profiles.import", map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("not a zip"))}, rpcCodeArchiveInvalid, `{"reason":"format"}`},
		{"profiles.import", map[string]any{"data": strings.Repeat("A", base64.StdEncoding.EncodedLen(profilelib.MaxArchiveBytes)+4)}, rpcCodeArchiveInvalid, `{"reason":"limit"}`},
		{"profiles.import", map[string]any{"data": exported.Data, "name": "x", "replace": "paint"}, jrpc2.InvalidParams, ""},
		{"profiles.import", map[string]any{"data": exported.Data, "name": " "}, jrpc2.InvalidParams, ""},
		{"profiles.import", map[string]any{"data": exported.Data, "replace": "../paint"}, jrpc2.InvalidParams, ""},
		{"profiles.import", map[string]any{"data": exported.Data, "replace": "missing"}, -32004, ""},
		{"profiles.import", map[string]any{"data": exported.Data, "name": "默认"}, rpcCodeProfileNameTaken, `{"id":"default","name":"默认"}`},
		{"profiles.import", map[string]any{"data": unsafeArchive(t)}, rpcCodeArchiveInvalid, `{"reason":"entry","path":"profile/../x.ts"}`},
	} {
		err := client.CallResult(ctx, tc.method, tc.params, new(any))
		if data := rpcErrorData(t, err, tc.code); tc.data != "" && data != tc.data {
			t.Fatalf("%s %v: %s", tc.method, tc.params, data)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "profiles"))
	if err != nil || len(entries) != 3 {
		t.Fatal("refused imports left files:", entries, err)
	}
}

// Replacing the active profile applies the archive at once and releases
// what the old profile held, like a save.
func TestIPCImportReplacesActiveProfile(t *testing.T) {
	dir := t.TempDir()
	const original = `{"schemaVersion":0,"name":"paint","layers":{"base":{"east":{"press":{"type":"keys","keys":"F13"}}}}}`
	writeLibraryProfile(t, dir, "paint", original)
	if err := os.WriteFile(filepath.Join(dir, "profiles", "paint", "old.ts"), []byte("export {};\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tl, src := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, events := libraryEvents(ctx, t, tl)
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +F13\n")
	changed := strings.ReplaceAll(original, "F13", "F14")
	var imported rpcImported
	if err := client.CallResult(ctx, "profiles.import", map[string]any{"data": exportedArchive(t, changed, nil), "replace": "paint"}, &imported); err != nil ||
		imported.ID != "paint" || !imported.Applied || imported.Revision != profilelib.Revision([]byte(changed)) {
		t.Fatalf("%+v %v", imported, err)
	}
	tl.waitFor(t, tl.stdout, "send -F13\n")
	if e := waitLibraryEvent(ctx, t, events, "saved", "paint"); e["applied"] != true {
		t.Fatal(e)
	}
	button(src, "east", false)
	button(src, "east", true)
	button(src, "east", false)
	tl.waitFor(t, tl.stdout, "send -F14\n")
	checkBalanced(t, tl.stdout.String())
	if _, err := os.Stat(filepath.Join(dir, "profiles", "paint", "old.ts")); !os.IsNotExist(err) {
		t.Fatal("replaced folder kept its old file", err)
	}
	// The replaced version is a snapshot.
	var history rpcHistory
	if err := client.CallResult(ctx, "profiles.history", map[string]string{"id": "paint"}, &history); err != nil || len(history.Snapshots) != 1 {
		t.Fatal(history, err)
	}
}

// A profile's macros travel with profiles.export and profiles.import
// (SCR-50, SCR-51, PRO-20): the archive carries the library's macros that
// the profile runs, not the declarations macros.create wrote, and an
// import puts them into the script library. The same bytes are used;
// other bytes under the same name are rpcCodeMacroConflict before
// anything is written, and the client repeats the import with macros
// "keep", which adds the archive's under a number that the imported
// profile runs, or "replace".
func TestIPCExportImportMacros(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{"south":{"tap":{"type":"macro","file":"复制全部.ts"}}}}}`)
	tl, _ := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := rpcTestClient(t, tl.liveRun)
	if err := client.CallResult(ctx, "macros.create", map[string]string{"name": "复制全部"}, new(any)); err != nil {
		t.Fatal(err)
	}
	if err := client.CallResult(ctx, "macros.create", map[string]string{"name": "没用"}, new(any)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(profilelib.GeneratedMacroDir), "padmate.d.ts")); err != nil {
		t.Fatal("macros.create wrote no declarations:", err)
	}
	var exported rpcExported
	if err := client.CallResult(ctx, "profiles.export", map[string]string{"id": "paint"}, &exported); err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(exported.Data)
	if err != nil {
		t.Fatal(err)
	}
	a, err := profilelib.ReadArchive(data)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := a.Paths(), []string{"macros/", "macros/复制全部.ts", "profile.json"}; !slices.Equal(got, want) {
		t.Fatal(got)
	}
	importAs := func(params map[string]any) (rpcImported, error) {
		params["data"] = exported.Data
		var imported rpcImported
		err := client.CallResult(ctx, "profiles.import", params, &imported)
		return imported, err
	}
	imported, err := importAs(map[string]any{"name": "paint 2"})
	if err != nil || !slices.Equal(imported.Macros, []rpcImportedMacro{{File: "复制全部.ts", Name: "复制全部.ts", Same: true}}) {
		t.Fatal(imported, err)
	}
	var list rpcMacroList
	if err := client.CallResult(ctx, "macros.list", nil, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Macros) != 2 || list.Macros[0].File != "复制全部.ts" || list.Macros[0].Problem != "" ||
		!slices.Equal(list.Macros[0].UsedBy, []rpcMacroUse{{"paint", "paint", "south", "tap"}, {imported.ID, "paint 2", "south", "tap"}}) || len(list.Missing) != 0 {
		t.Fatalf("%+v", list)
	}
	if _, err := os.Lstat(filepath.Join(dir, "profiles", imported.ID, "macros")); !os.IsNotExist(err) {
		t.Fatal("macros imported into the profile folder:", err)
	}

	// The library's macro changes: the archive's is another one now.
	change := func(text string) {
		t.Helper()
		if err := client.CallResult(ctx, "macros.import", map[string]any{"name": "复制全部.ts", "text": text, "replace": true}, new(any)); err != nil {
			t.Fatal(err)
		}
	}
	change("// 改过\n")
	_, err = importAs(map[string]any{"name": "paint 3"})
	if data := rpcErrorData(t, err, rpcCodeMacroConflict); data != `{"conflicts":[{"file":"复制全部.ts","existing":"复制全部.ts","replaceable":true}]}` {
		t.Fatal(data)
	}
	var profiles []profilelib.Entry
	if err := client.CallResult(ctx, "profiles.list", nil, &profiles); err != nil || len(profiles) != 3 {
		t.Fatal("imported before asking:", profiles, err)
	}
	rpcErrorData(t, func() error { _, err := importAs(map[string]any{"name": "paint 3", "macros": "both"}); return err }(), jrpc2.InvalidParams)
	// 替换: the library's macro gets the archive's bytes, for every profile.
	replaced, err := importAs(map[string]any{"name": "paint 3", "macros": "replace"})
	if err != nil || !slices.Equal(replaced.Macros, []rpcImportedMacro{{File: "复制全部.ts", Name: "复制全部.ts", Replaced: true}}) {
		t.Fatal(replaced, err)
	}
	if got := readLibraryMacro(t, dir, "复制全部.ts"); got != dtsgen.MacroTemplate {
		t.Fatal(got)
	}
	// 都保留: the archive's macro gets a number, which the imported
	// profile runs.
	change("// 又改过\n")
	kept, err := importAs(map[string]any{"name": "paint 4", "macros": "keep"})
	if err != nil || !slices.Equal(kept.Macros, []rpcImportedMacro{{File: "复制全部.ts", Name: "复制全部 2.ts"}}) {
		t.Fatal(kept, err)
	}
	if got := libraryProfile(t, dir, kept.ID); !strings.Contains(got, `"file":"复制全部 2.ts"`) {
		t.Fatal(got)
	}
	if readLibraryMacro(t, dir, "复制全部 2.ts") != dtsgen.MacroTemplate || readLibraryMacro(t, dir, "复制全部.ts") != "// 又改过\n" {
		t.Fatal("keep changed the library's macro")
	}
	// The archive's macro is the numbered one now: no question.
	again, err := importAs(map[string]any{"name": "paint 5"})
	if err != nil || !slices.Equal(again.Macros, []rpcImportedMacro{{File: "复制全部.ts", Name: "复制全部 2.ts", Same: true}}) {
		t.Fatal(again, err)
	}
}
