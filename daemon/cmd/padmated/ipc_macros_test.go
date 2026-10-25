package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/dtsgen"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// libraryProfile reads a library profile's profile.json.
func libraryProfile(t *testing.T, dir, id string) string {
	t.Helper()
	return readProfileFile(t, dir, id, "profile.json")
}

// readLibraryMacro reads a file of the data folder dir's script library.
func readLibraryMacro(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, profile.MacroDir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// macros.list lists the script library (SCR-51): each macro with the
// profiles and controls that run it in their profile.json, aliases and
// layers merged, its build or load problem, names CheckMacroFile refuses,
// the macros that bindings run but the library lacks, and every profile's
// legacy entry. It takes no profile, needs no running one, and accepts
// and ignores the id a client from before the library sends.
func TestIPCListMacros(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{}}}`)
	tl, _ := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := rpcTestClient(t, tl.liveRun)
	// A library without macros or legacy scripts lists empty arrays, not
	// null.
	var raw json.RawMessage
	if err := client.CallResult(ctx, "macros.list", nil, &raw); err != nil || string(raw) != `{"macros":[],"missing":[],"legacy":[]}` {
		t.Fatal(string(raw), err)
	}

	const photo = `{"schemaVersion":0,"name":"照片","script":{"entry":"main.ts","api":"0.21"},
"layers":{"base":{"a":{"tap":{"type":"macro","file":"复制全部.ts"}},"east":{"hold":{"type":"macro","file":"不存在.ts"}}},
"alt":{"south":{"press":{"type":"macro","file":"复制全部.ts"},"tap":{"type":"macro","file":"复制全部.ts"}}}},
"common":{"leftstick":{"doubleTap":{"type":"macro","file":"出错.ts"}}}}`
	writeLibraryProfile(t, dir, "photo", photo)
	writeLibraryProfile(t, dir, "sketch", `{"schemaVersion":0,"layers":{"base":{"north":{"tap":{"type":"macro","file":"复制全部.ts"}},"west":{"tap":{"type":"macro","file":"不存在.ts"}}}}}`)
	// Notepad starts profile.json with a BOM.
	writeLibraryProfile(t, dir, "bom", "\ufeff"+`{"schemaVersion":0,"name":"bom","script":{"entry":"main.ts","api":"0.21"},"layers":{"base":{}}}`)
	// A profile.json that is not valid JSON runs no macro.
	writeLibraryProfile(t, dir, "torn", `{"layers":{"base":{"south":{"tap":{"type":"macro","file":"复制全部.ts"}}}`)
	writeMacros(t, dir, map[string]string{
		"复制全部.ts": `import {key} from "padmate"; export default async function () { await key.tap("Ctrl+A"); }`,
		"出错.ts":   "export default function (",
		"顶层.ts":   `throw new Error("top level");`,
		"v1.2.ts": `export default function () {}`,
		"x.d.ts":  `export {};`,
	})
	for _, params := range []any{nil, map[string]string{}, map[string]string{"id": "photo"}, map[string]string{"id": "../photo"}} {
		var list rpcMacroList
		if err := client.CallResult(ctx, "macros.list", params, &list); err != nil {
			t.Fatal(params, err)
		}
		files := make([]string, len(list.Macros))
		byFile := map[string]rpcMacro{}
		for i, m := range list.Macros {
			files[i], byFile[m.File] = m.File, m
		}
		if want := []string{"v1.2.ts", "出错.ts", "复制全部.ts", "顶层.ts"}; !slices.Equal(files, want) {
			t.Fatalf("macros %q, want %q", files, want)
		}
		copyAll := byFile["复制全部.ts"]
		if want := []rpcMacroUse{{"photo", "照片", "south", "press"}, {"photo", "照片", "south", "tap"}, {"sketch", "sketch", "north", "tap"}}; copyAll.Name != "复制全部" ||
			copyAll.Problem != "" || copyAll.InvalidName || copyAll.Actions != 4 || !slices.Equal(copyAll.UsedBy, want) {
			t.Errorf("复制全部.ts: %+v", copyAll)
		}
		if m := byFile["出错.ts"]; !strings.Contains(m.Problem, "出错.ts:1") || !slices.Equal(m.UsedBy, []rpcMacroUse{{"photo", "照片", "leftstick", "doubleTap"}}) {
			t.Errorf("出错.ts: %+v", m)
		}
		if m := byFile["顶层.ts"]; !strings.Contains(m.Problem, "top level") || len(m.UsedBy) != 0 || m.UsedBy == nil {
			t.Errorf("顶层.ts: %+v", m)
		}
		if m := byFile["v1.2.ts"]; !m.InvalidName || m.Problem != "" || m.Name != "v1.2" {
			t.Errorf("v1.2.ts: %+v", m)
		}
		if want := []rpcMacroUse{{"photo", "照片", "east", "hold"}, {"sketch", "sketch", "west", "tap"}}; len(list.Missing) != 1 || list.Missing[0].File != "不存在.ts" ||
			list.Missing[0].Actions != 2 || !slices.Equal(list.Missing[0].UsedBy, want) {
			t.Errorf("missing %+v", list.Missing)
		}
		if want := []rpcLegacyScript{{"bom", "bom", "main.ts"}, {"photo", "照片", "main.ts"}}; !slices.Equal(list.Legacy, want) {
			t.Errorf("legacy %+v", list.Legacy)
		}
	}
	rpcErrorData(t, client.CallResult(ctx, "macros.list", map[string]any{"extra": 1}, new(any)), jrpc2.InvalidParams)
	rpcErrorData(t, client.CallResult(ctx, "macros.list", map[string]any{"id": 1}, new(any)), jrpc2.InvalidParams)
}

// macros.create makes NAME.ts from the template in the script library
// with its editor files, exclusively; macros.import writes a file the
// client read, replacing one of the same name and extension under its
// exact name only with replace. Neither edits a profile.json.
func TestIPCCreateAndImportMacros(t *testing.T) {
	dir := t.TempDir()
	const paint = `{"schemaVersion":0,"name":"paint","layers":{"base":{}}}`
	writeLibraryProfile(t, dir, "paint", paint)
	tl, _ := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := rpcTestClient(t, tl.liveRun)
	create := func(name string) (rpcMacroFile, error) {
		var result rpcMacroFile
		err := client.CallResult(ctx, "macros.create", map[string]string{"name": name}, &result)
		return result, err
	}
	if result, err := create("复制全部"); err != nil || result.File != "复制全部.ts" || result.Replaced != nil {
		t.Fatal(result, err)
	}
	if got := readLibraryMacro(t, dir, "复制全部.ts"); got != dtsgen.MacroTemplate {
		t.Fatal(got)
	}
	project, err := dtsgen.MacroProject(dtsgen.HostAPI)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range project {
		if got := readLibraryMacro(t, dir, f.Name); got != string(f.Data) {
			t.Errorf("%s differs", f.Name)
		}
	}
	if _, err := os.Lstat(filepath.Join(dir, "profiles", "paint", profile.MacroDir)); !os.IsNotExist(err) {
		t.Fatal("the profile got a macros folder:", err)
	}
	data := rpcErrorData(t, func() error { _, err := create("复制全部"); return err }(), rpcCodeScriptExists)
	if data != `{"exists":true,"name":"复制全部.ts","replaceable":false}` {
		t.Fatal(data)
	}
	for name, kind := range map[string]string{"": "empty", "a.b": "char", "con": "invalid", strings.Repeat("长", 41): "length", "a/b": "char"} {
		data := rpcErrorData(t, func() error { _, err := create(name); return err }(), jrpc2.InvalidParams)
		if data != `{"kind":"`+kind+`"}` {
			t.Errorf("%q: %s", name, data)
		}
	}
	// A client from before the library sends a profile; it is ignored.
	if err := client.CallResult(ctx, "macros.create", map[string]string{"id": "missing", "name": "旧客户端"}, new(any)); err != nil {
		t.Fatal(err)
	}
	if got := readLibraryMacro(t, dir, "旧客户端.ts"); got != dtsgen.MacroTemplate {
		t.Fatal(got)
	}

	importMacro := func(name, text string, replace bool) (rpcMacroFile, error) {
		var result rpcMacroFile
		err := client.CallResult(ctx, "macros.import", map[string]any{"name": name, "text": text, "replace": replace}, &result)
		return result, err
	}
	if result, err := importMacro("下一个笔刷.js", "export default function () {}\n", false); err != nil || result.File != "下一个笔刷.js" || result.Replaced == nil || *result.Replaced {
		t.Fatal(result, err)
	}
	data = rpcErrorData(t, func() error { _, err := importMacro("复制全部.TS", "x", false); return err }(), jrpc2.InvalidParams)
	if data != `{"kind":"extension"}` {
		t.Fatal(data)
	}
	data = rpcErrorData(t, func() error { _, err := importMacro("复制全部.ts", "x", false); return err }(), rpcCodeScriptExists)
	if data != `{"exists":true,"name":"复制全部.ts","replaceable":true}` {
		t.Fatal(data)
	}
	if result, err := importMacro("复制全部.ts", "// 新的\n", true); err != nil || result.File != "复制全部.ts" || !*result.Replaced {
		t.Fatal(result, err)
	}
	if got := readLibraryMacro(t, dir, "复制全部.ts"); got != "// 新的\n" {
		t.Fatal(got)
	}
	data = rpcErrorData(t, func() error { _, err := importMacro("下一个笔刷.ts", "x", true); return err }(), rpcCodeScriptExists)
	if data != `{"exists":true,"name":"下一个笔刷.js","replaceable":false}` {
		t.Fatal(data)
	}
	rpcErrorData(t, client.CallResult(ctx, "macros.import", json.RawMessage(`{"name":"c.ts","replace":false,"text":"\ud800"}`), new(any)), jrpc2.InvalidParams)
	if err := os.WriteFile(filepath.Join(dir, profile.MacroDir, "tsconfig.json"), []byte("// mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := create("第三个"); err != nil {
		t.Fatal(err)
	}
	if got := readLibraryMacro(t, dir, "tsconfig.json"); got != "// mine\n" {
		t.Fatal("tsconfig.json rewritten:", got)
	}
	for i := 4; i < profile.MaxMacros; i++ {
		if _, err := create(fmt.Sprintf("m%d", i)); err != nil {
			t.Fatal(i, err)
		}
	}
	if data := rpcErrorData(t, func() error { _, err := create("one more"); return err }(), jrpc2.InvalidParams); data != `{"limit":64}` {
		t.Fatal(data)
	}
	if got := libraryProfile(t, dir, "paint"); got != paint {
		t.Fatal("profile.json changed:", got)
	}
}

// macros.rename and macros.delete edit every profile that runs the macro
// in place and apply the active one: the renamed macro still runs on its
// key, and a deleted one's binding is gone with its file. Each profile
// written is in the result and tells subscribers it was saved. The file
// must be one the library lists exactly.
func TestIPCRenameAndDeleteMacros(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{
"south":{"press":{"type":"macro","file":"a.ts"}},
"east":{"label":"删","press":{"type":"macro","file":"b.ts"}}}}}`)
	const photo = `{"schemaVersion":0,"name":"photo","layers":{"base":{"north":{"tap":{"type":"macro","file":"a.ts"}},"west":{"tap":{"type":"macro","file":"b.ts"}},"south":{"tap":{"type":"macro","file":"b.ts"}}}}}`
	writeLibraryProfile(t, dir, "photo", photo)
	writeLibraryProfile(t, dir, "plain", `{"schemaVersion":0,"name":"plain","layers":{"base":{}}}`)
	writeMacros(t, dir, map[string]string{
		"a.ts": `import {key} from "padmate"; export default function () { key.tap("F21"); }`,
		"b.ts": `import {key} from "padmate"; export default function () { key.tap("F22"); }`,
	})
	tl, src := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, events := libraryEvents(ctx, t, tl)
	running := func() bool {
		var views []slot.ScriptView
		return client.CallResult(ctx, "script.status", map[string]string{"device": "1"}, &views) == nil &&
			len(views) == 1 && views[0].Script.State == "Running" && !views[0].Script.Pending
	}
	tl.waitUntil(t, "the macros' runtime", running)
	var edit rpcMacroEdit
	if err := client.CallResult(ctx, "macros.rename", map[string]string{"file": "a.ts", "to": "复制 (全部).ts"}, &edit); err != nil ||
		edit.File != "复制 (全部).ts" || edit.Bindings != 2 || len(edit.Profiles) != 2 ||
		edit.Profiles[0].ID != "paint" || edit.Profiles[0].Bindings != 1 || edit.Profiles[1].ID != "photo" || edit.Profiles[1].Bindings != 1 {
		t.Fatal(edit, err)
	}
	e := waitLibraryEvent(ctx, t, events, "saved", "paint")
	if e["revision"] != edit.Profiles[0].Revision || e["applied"] != true {
		t.Fatal(e)
	}
	if e := waitLibraryEvent(ctx, t, events, "saved", "photo"); e["revision"] != edit.Profiles[1].Revision || e["applied"] != false {
		t.Fatal(e)
	}
	for _, id := range []string{"paint", "photo"} {
		if got := libraryProfile(t, dir, id); !strings.Contains(got, `"file":"复制 (全部).ts"`) || strings.Contains(got, "a.ts") {
			t.Fatal(id, got)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, profile.MacroDir, "复制 (全部).ts")); err != nil {
		t.Fatal(err)
	}
	tl.waitUntil(t, "the rebuilt runtime", running)
	button(src, "south", true)
	button(src, "south", false)
	tl.waitFor(t, tl.stdout, "send -F21\n")

	var deleted rpcMacroEdit
	if err := client.CallResult(ctx, "macros.delete", map[string]string{"file": "b.ts"}, &deleted); err != nil || deleted.Bindings != 3 || deleted.File != "" || len(deleted.Profiles) != 2 {
		t.Fatal(deleted, err)
	}
	waitLibraryEvent(ctx, t, events, "saved", "paint")
	waitLibraryEvent(ctx, t, events, "saved", "photo")
	if got := libraryProfile(t, dir, "paint"); strings.Contains(got, "east") || strings.Contains(got, "b.ts") {
		t.Fatal(got)
	}
	if got := libraryProfile(t, dir, "photo"); strings.Contains(got, "west") || strings.Contains(got, "b.ts") || !strings.Contains(got, "north") {
		t.Fatal(got)
	}
	if _, err := os.Stat(filepath.Join(dir, profile.MacroDir, "b.ts")); !os.IsNotExist(err) {
		t.Fatal("b.ts not deleted", err)
	}
	// A file already gone only loses its references; one nothing runs
	// writes no profile.json.
	var raw json.RawMessage
	if err := client.CallResult(ctx, "macros.delete", map[string]string{"id": "paint", "file": "gone.ts"}, &raw); err != nil || string(raw) != `{"bindings":0,"profiles":[]}` {
		t.Fatal(string(raw), err)
	}
	for _, tc := range []struct {
		method string
		params map[string]string
		code   jrpc2.Code
		data   string
	}{
		{"macros.rename", map[string]string{"file": "A.ts", "to": "c.ts"}, rpcCodeMacroNotFound, `{"file":"A.ts"}`},
		{"macros.rename", map[string]string{"file": "复制 (全部).ts", "to": "c.js"}, jrpc2.InvalidParams, ""},
		{"macros.rename", map[string]string{"file": "复制 (全部).ts", "to": "c.d.ts"}, jrpc2.InvalidParams, `{"kind":"char"}`},
		{"macros.delete", map[string]string{"file": ""}, rpcCodeMacroNotFound, `{"file":""}`},
	} {
		err := client.CallResult(ctx, tc.method, tc.params, new(any))
		if data := rpcErrorData(t, err, tc.code); tc.data != "" && data != tc.data {
			t.Errorf("%s %v: %s", tc.method, tc.params, data)
		}
	}
	writeMacros(t, dir, map[string]string{"c.ts": ""})
	if data := rpcErrorData(t, client.CallResult(ctx, "macros.rename", map[string]string{"file": "复制 (全部).ts", "to": "C.ts"}, new(any)), rpcCodeScriptExists); data != `{"exists":true,"name":"c.ts","replaceable":false}` {
		t.Fatal(data)
	}
}

// macros.path names a regular file of the script library for the UI's
// main process and writes the editor files first; script.path names a
// profile's legacy entry. script.detach removes the legacy script from
// profile.json and keeps its files.
func TestIPCScriptPathsAndDetach(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","script":{"entry":"main.ts","api":"0.21"},
"layers":{"base":{"north":{"press":{"type":"script","name":"brush"}},"south":{"press":{"type":"macro","file":"a.ts"}}}}}`)
	writeLibraryProfile(t, dir, "photo", `{"schemaVersion":0,"name":"photo","layers":{"base":{}}}`)
	folder := filepath.Join(dir, "profiles", "paint")
	if err := os.WriteFile(filepath.Join(folder, "main.ts"), []byte(`import {defineScript} from "padmate"; export default defineScript({actions:{brush:{run(){}}},setup(){}});`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeMacros(t, dir, map[string]string{"a.ts": `export default function () {}`})
	tl, _ := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, events := libraryEvents(ctx, t, tl)
	var path map[string]string
	if err := client.CallResult(ctx, "macros.path", map[string]string{"file": "a.ts"}, &path); err != nil ||
		path["path"] != filepath.Join(dir, profile.MacroDir, "a.ts") {
		t.Fatal(path, err)
	}
	if _, err := os.Stat(filepath.Join(dir, profile.MacroDir, dtsgen.MacroTypesDir, "padmate.d.ts")); err != nil {
		t.Fatal(err)
	}
	if data := rpcErrorData(t, client.CallResult(ctx, "macros.path", map[string]string{"file": "A.ts"}, new(any)), rpcCodeMacroNotFound); data != `{"file":"A.ts"}` {
		t.Fatal(data)
	}
	if err := client.CallResult(ctx, "script.path", map[string]string{"id": "paint"}, &path); err != nil || path["path"] != filepath.Join(folder, "main.ts") {
		t.Fatal(path, err)
	}
	rpcErrorData(t, client.CallResult(ctx, "script.path", map[string]string{"id": "photo"}, new(any)), jrpc2.InvalidParams)
	rpcErrorData(t, client.CallResult(ctx, "script.path", map[string]string{}, new(any)), jrpc2.InvalidParams)
	writeLibraryProfile(t, dir, "photo", `{"schemaVersion":0,"name":"photo","script":{"entry":"none.ts","api":"0.21"},"layers":{"base":{}}}`)
	if data := rpcErrorData(t, client.CallResult(ctx, "script.path", map[string]string{"id": "photo"}, new(any)), rpcCodeMacroNotFound); data != `{"file":"none.ts"}` {
		t.Fatal(data)
	}
	writeLibraryProfile(t, dir, "photo", `{"schemaVersion":0,"name":"photo","script":{"entry":".history/x.ts","api":"0.21"},"layers":{"base":{}}}`)
	rpcErrorData(t, client.CallResult(ctx, "script.path", map[string]string{"id": "photo"}, new(any)), rpcCodeProfileUnsupported)

	var raw json.RawMessage
	if err := client.CallResult(ctx, "script.detach", map[string]string{"id": "paint"}, &raw); err != nil {
		t.Fatal(err)
	}
	var edit rpcScriptEdit
	if err := json.Unmarshal(raw, &edit); err != nil || edit.Bindings != 1 || edit.Revision == "" || strings.Contains(string(raw), "profiles") {
		t.Fatal(string(raw), err)
	}
	waitLibraryEvent(ctx, t, events, "saved", "paint")
	got := libraryProfile(t, dir, "paint")
	if strings.Contains(got, "script") || !strings.Contains(got, "a.ts") {
		t.Fatal(got)
	}
	if _, err := os.Stat(filepath.Join(folder, "main.ts")); err != nil {
		t.Fatal(err)
	}
	// The macro still runs: the runtime is only needed for it now.
	tl.waitUntil(t, "the macros' runtime", func() bool {
		var views []slot.ScriptView
		return client.CallResult(ctx, "script.status", map[string]string{"device": "1"}, &views) == nil &&
			len(views) == 1 && views[0].Script.State == "Running" && views[0].Script.Entry == ""
	})
	if err := client.CallResult(ctx, "script.detach", map[string]string{"id": "paint"}, &edit); err != nil || edit.Bindings != 0 {
		t.Fatal(edit, err)
	}
}

// A library daemon moves the profiles' macros folders into the script
// library before it starts (SCR-51): a macro the library has with other
// bytes gets a number and the profile's binding follows it, so each
// profile runs its own macro; the start logs what moved.
func TestLiveMigratesMacros(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{"south":{"press":{"type":"macro","file":"a.ts"}}}}}`)
	writeMacros(t, filepath.Join(dir, "profiles", "paint"), map[string]string{"a.ts": `import {key} from "padmate"; export default function () { key.tap("F21"); }`})
	writeLibraryProfile(t, dir, "photo", `{"schemaVersion":0,"name":"photo","layers":{"base":{"south":{"press":{"type":"macro","file":"a.ts"}}}}}`)
	writeMacros(t, filepath.Join(dir, "profiles", "photo"), map[string]string{"a.ts": `import {key} from "padmate"; export default function () { key.tap("F22"); }`})
	tl, src := startFocusLive(t, dir, "photo", `{"schemaVersion":0}`, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, id := range []string{"paint", "photo"} {
		if _, err := os.Lstat(filepath.Join(dir, "profiles", id, profile.MacroDir)); !os.IsNotExist(err) {
			t.Fatal(id, "keeps its macros folder:", err)
		}
	}
	if got := readLibraryMacro(t, dir, "a 2.ts"); !strings.Contains(got, "F22") {
		t.Fatal(got)
	}
	if got := libraryProfile(t, dir, "photo"); !strings.Contains(got, `"file":"a 2.ts"`) {
		t.Fatal(got)
	}
	client := rpcTestClient(t, tl.liveRun)
	tl.waitUntil(t, "the macros' runtime", func() bool {
		var views []slot.ScriptView
		return client.CallResult(ctx, "script.status", map[string]string{"device": "1"}, &views) == nil &&
			len(views) == 1 && views[0].Script.State == "Running" && !views[0].Script.Pending
	})
	button(src, "south", true)
	button(src, "south", false)
	tl.waitFor(t, tl.stdout, "send -F22\n")
	tl.waitFor(t, tl.log, `"msg":"macro moved into the script library","profile":"photo","file":"a.ts","to":"a 2.ts"`)
	tl.waitFor(t, tl.log, `"msg":"macro references renamed","profile":"photo","bindings":1`)
}

// TestLogMacroMigrationShadowed: a module the library has with other bytes
// is an error in the log, not only a file that stayed.
func TestLogMacroMigrationShadowed(t *testing.T) {
	var out strings.Builder
	log := slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logMacroMigration(log, profilelib.MacroMigration{
		Left:     []string{"photo/macros/lib/util.ts", "photo/macros/tsconfig.json"},
		Shadowed: []string{"photo/macros/lib/util.ts"},
	}, nil)
	var errorLines []string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.Contains(line, `"level":"ERROR"`) {
			errorLines = append(errorLines, line)
		}
	}
	if len(errorLines) != 1 || !strings.Contains(errorLines[0], "photo/macros/lib/util.ts") || !strings.Contains(errorLines[0], "module not moved") {
		t.Fatalf("logged %q", out.String())
	}
	if strings.Count(out.String(), `"level":"WARN"`) != 2 {
		t.Fatalf("logged %q", out.String())
	}
}
