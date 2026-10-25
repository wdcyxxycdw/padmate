package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

// rpcTestClient authenticates against the real method table using a private
// in-memory connection. It never discovers or controls a user's daemon.
func rpcTestClient(t *testing.T, r *liveRun, options ...*jrpc2.ClientOptions) *jrpc2.Client {
	t.Helper()
	a, b := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	d := ipc.Discovery{Protocol: ipc.Protocol, Token: strings.Repeat("c", 64)}
	done := make(chan struct{})
	go func() { defer close(done); _ = ipc.ServeConn(ctx, b, d, r.events, r.rpcMethods) }()
	t.Cleanup(func() { cancel(); _ = a.Close(); <-done })
	var opts *jrpc2.ClientOptions
	if len(options) > 0 {
		opts = options[0]
	}
	client, err := ipc.NewClient(ctx, a, d, "profile-test", opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestIPCProfileRevisionAndImmediateReload(t *testing.T) {
	dir := t.TempDir()
	const original = `{"schemaVersion":0,"name":"paint","layers":{"base":{"east":{"press":{"type":"keys","keys":"F13"}}}}}`
	writeLibraryProfile(t, dir, "paint", original)
	writeLibraryProfile(t, dir, "broken", "{")
	tl, src := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	a, b := rpcTestClient(t, tl.liveRun), rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var items []profilelib.Entry
	if err := a.CallResult(ctx, "profiles.list", nil, &items); err != nil || len(items) != 3 || items[0].Problem == "" || !items[1].Default {
		t.Fatal(items, err)
	}
	var document rpcDocument
	if err := a.CallResult(ctx, "profiles.get", map[string]string{"id": "paint"}, &document); err != nil || document.Text != original {
		t.Fatal(document, err)
	}
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +F13\n")
	changed := strings.ReplaceAll(original, "F13", "F14")
	put := map[string]string{"id": "paint", "revision": document.Revision, "text": changed}
	var saved rpcSaved
	if err := a.CallResult(ctx, "profiles.put", put, &saved); err != nil || !saved.Applied || saved.Revision != profilelib.Revision([]byte(changed)) {
		t.Fatal(saved, err)
	}
	tl.waitFor(t, tl.stdout, "send -F13\n")
	if err := b.CallResult(ctx, "profiles.put", put, &saved); err == nil {
		t.Fatal("second client overwrote a changed profile")
	} else {
		var rpcErr *jrpc2.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != -32009 || !strings.Contains(string(rpcErr.Data), saved.Revision) {
			t.Fatal(err)
		}
	}
	button(src, "east", false)
	button(src, "east", true)
	button(src, "east", false)
	tl.waitFor(t, tl.stdout, "send -F14\n")
	checkBalanced(t, tl.stdout.String())
	for _, p := range []map[string]string{
		{"id": "paint", "revision": saved.Revision, "text": "{"},
		{"id": "paint", "text": original},
		{"id": "../escape", "revision": "", "text": original},
	} {
		if err := a.CallResult(ctx, "profiles.put", p, new(any)); err == nil {
			t.Fatal("accepted invalid put", p)
		}
	}
	if err := a.CallResult(ctx, "profiles.get", map[string]string{"id": "broken"}, &document); err != nil || document.Text != "{" || document.Problem == "" {
		t.Fatal("broken document unavailable for repair", document, err)
	}
}

func TestIPCValidateProfileDoesNotSave(t *testing.T) {
	dir := t.TempDir()
	const original = `{"schemaVersion":0,"name":"paint","layers":{"base":{}}}`
	writeLibraryProfile(t, dir, "paint", original)
	tl, _ := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, text := range []string{original, `{"schemaVersion":0,"name":"broken","layers":{"base":{"south":{"hold":{"type":"keys","keys":"NONSENSE"}}}}}`} {
		var result rpcValidation
		if err := client.CallResult(ctx, "profiles.validate", map[string]string{"text": text}, &result); err != nil {
			t.Fatal(err)
		}
		if result.Valid != (text == original) {
			t.Fatal(result)
		}
		if !result.Valid && (len(result.Diagnostics) == 0 || result.Diagnostics[0].Pointer == "") {
			t.Fatal(result)
		}
	}
	var doc rpcDocument
	if err := client.CallResult(ctx, "profiles.get", map[string]string{"id": "paint"}, &doc); err != nil || doc.Text != original {
		t.Fatal(doc, err)
	}
}

func TestIPCConfigRevisionAndRestartNotice(t *testing.T) {
	dir := t.TempDir()
	tl, _ := startFocusLive(t, dir, "", `{"schemaVersion":0}`, false)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var doc rpcDocument
	if err := client.CallResult(ctx, "config.get", nil, &doc); err != nil || doc.RestartRequired {
		t.Fatal(doc, err)
	}
	var saved rpcSaved
	// Automatic selection applies at once (UI-18).
	if err := client.CallResult(ctx, "config.put", map[string]string{"revision": doc.Revision, "text": `{"schemaVersion":0,"enabled":false}`}, &saved); err != nil || saved.RestartRequired || !saved.Applied {
		t.Fatal(saved, err)
	}
	if err := client.CallResult(ctx, "config.get", nil, &doc); err != nil || doc.RestartRequired || doc.Revision != saved.Revision {
		t.Fatal(doc, err)
	}
	if tl.selectionConfig().Enabled {
		t.Fatal("saved selection settings were not applied")
	}
	if err := client.CallResult(ctx, "config.put", map[string]string{"revision": doc.Revision, "text": `{"schemaVersion":0,"enabled":false,"restoreLayerState":false}`}, &saved); err != nil || !saved.RestartRequired || saved.Applied {
		t.Fatal(saved, err)
	}
	if err := client.CallResult(ctx, "config.get", nil, &doc); err != nil || !doc.RestartRequired || doc.Revision != saved.Revision {
		t.Fatal(doc, err)
	}
	if !tl.cfg.opts.settings.Enabled || !tl.cfg.opts.settings.RestoreLayerState || !tl.selectionConfig().RestoreLayerState {
		t.Fatal("saved config silently changed startup settings")
	}
}

func TestIPCProfileSavedButRuntimeRejectsParams(t *testing.T) {
	dir := t.TempDir()
	const original = `{"schemaVersion":0,"name":"paint","script":{"entry":"main.ts","api":"0.21","params":{"amount":1}},"layers":{"base":{}}}`
	writeLibraryProfile(t, dir, "paint", original)
	const source = `import {defineScript,param,every,log} from "padmate";
export default defineScript({params:{amount:param.number({default:1,max:2})},setup(){every(20,()=>log.info("ipc schema ready"));}});`
	if err := os.WriteFile(filepath.Join(dir, "profiles", "paint", "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	tl, _ := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	tl.waitFor(t, tl.log, "ipc schema ready")
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	changed := strings.Replace(original, `"amount":1`, `"amount":"invalid"`, 1)
	var saved rpcSaved
	if err := client.CallResult(ctx, "profiles.put", map[string]string{"id": "paint", "text": changed, "revision": profilelib.Revision([]byte(original))}, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Applied || saved.Problem == "" || saved.Revision != profilelib.Revision([]byte(changed)) {
		t.Fatalf("save/apply distinction lost: %+v", saved)
	}
	var doc rpcDocument
	if err := client.CallResult(ctx, "profiles.get", map[string]string{"id": "paint"}, &doc); err != nil || doc.Text != changed {
		t.Fatal(doc, err)
	}
	tl.profileMu.Lock()
	active := tl.cfg.profile.Script.Params["amount"]
	tl.profileMu.Unlock()
	if active != float64(1) {
		t.Fatalf("failed validation changed active params: %v", active)
	}
}

// TestIPCDefaultProfileEditableNotRenamable: the fixed default (PRO-28,
// ADR-0053) saves and applies an edit at once, refuses a new name with
// rpcCodeProfileFixed, and copies to an ordinary profile that renames.
func TestIPCDefaultProfileEditableNotRenamable(t *testing.T) {
	dir := t.TempDir()
	tl, src := startFocusLive(t, dir, "", `{"schemaVersion":0}`, false)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var doc rpcDocument
	if err := client.CallResult(ctx, "profiles.get", map[string]string{"id": profilelib.DefaultID}, &doc); err != nil || doc.Text != profilelib.DefaultProfileText {
		t.Fatal(doc, err)
	}
	const renamed = `{"schemaVersion":0,"name":"Mine","layers":{"base":{}}}`
	put := func(id, revision, text string) (rpcSaved, error) {
		var saved rpcSaved
		err := client.CallResult(ctx, "profiles.put", map[string]string{"id": id, "revision": revision, "text": text}, &saved)
		return saved, err
	}
	_, err := put(profilelib.DefaultID, doc.Revision, renamed)
	if data := rpcErrorData(t, err, rpcCodeProfileFixed); data != `{"op":"rename"}` {
		t.Fatal(data)
	}
	const edited = `{"schemaVersion":0,"name":"默认","layers":{"base":{"east":{"press":{"type":"keys","keys":"F13"}}}}}`
	saved, err := put(profilelib.DefaultID, doc.Revision, edited)
	if err != nil || !saved.Applied {
		t.Fatal(saved, err)
	}
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +F13\n")
	button(src, "east", false)
	tl.waitFor(t, tl.stdout, "send -F13\n")
	if err := client.CallResult(ctx, "profiles.copy", map[string]string{"from": profilelib.DefaultID, "to": "mine"}, new(any)); err != nil {
		t.Fatal(err)
	}
	if _, err := put("mine", profilelib.Revision([]byte(edited)), renamed); err != nil {
		t.Fatal("copy of the default not renamable", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles", profilelib.DefaultID, "profile.json")); err != nil {
		t.Fatal(err)
	}
}
