package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

// TestIPCProfileHistoryAndRestore: profiles.put keeps the replaced
// profile.json (PRO-17); profiles.restore puts a kept version back with
// put's revision check, applies it to the running mapping at once and
// publishes profile.changed like a save.
func TestIPCProfileHistoryAndRestore(t *testing.T) {
	dir := t.TempDir()
	const original = `{"schemaVersion":0,"name":"paint","layers":{"base":{"east":{"press":{"type":"keys","keys":"F13"}}}}}`
	writeLibraryProfile(t, dir, "paint", original)
	tl, src := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, events := libraryEvents(ctx, t, tl)
	historyOf := func(id string) rpcHistory {
		t.Helper()
		var h rpcHistory
		if err := client.CallResult(ctx, "profiles.history", map[string]string{"id": id}, &h); err != nil {
			t.Fatal(err)
		}
		return h
	}
	if h := historyOf("paint"); h.Snapshots == nil || len(h.Snapshots) != 0 {
		t.Fatalf("history of an unsaved profile: %+v", h)
	}

	changed := strings.ReplaceAll(strings.ReplaceAll(original, "F13", "F14"), `"paint"`, `"renamed"`)
	var saved rpcSaved
	put := map[string]string{"id": "paint", "revision": profilelib.Revision([]byte(original)), "text": changed}
	if err := client.CallResult(ctx, "profiles.put", put, &saved); err != nil || !saved.Applied {
		t.Fatal(saved, err)
	}
	h := historyOf("paint")
	if len(h.Snapshots) != 1 || h.Snapshots[0].Name != "paint" || h.Snapshots[0].Bytes != int64(len(original)) || !profilelib.ValidVersion(h.Snapshots[0].Version) {
		t.Fatalf("history after a save: %+v", h)
	}
	if at, err := time.Parse(time.RFC3339, h.Snapshots[0].SavedAt); err != nil || time.Since(at) > time.Minute || !strings.HasSuffix(h.Snapshots[0].SavedAt, "Z") {
		t.Fatal("savedAt", h.Snapshots[0].SavedAt, err)
	}
	version := h.Snapshots[0].Version
	button(src, "east", true)
	button(src, "east", false)
	tl.waitFor(t, tl.stdout, "send -F14\n")

	restore := func(id, version string, revision any) (rpcSaved, error) {
		var result rpcSaved
		err := client.CallResult(ctx, "profiles.restore", map[string]any{"id": id, "version": version, "revision": revision}, &result)
		return result, err
	}
	if conflict := rpcErrorData(t, func() error { _, err := restore("paint", version, "stale"); return err }(), -32009); !strings.Contains(conflict, saved.Revision) {
		t.Fatal(conflict)
	}
	for _, bad := range []string{"../paint/profile", version + ".json", "", strings.ToUpper(version)} {
		_, err := restore("paint", bad, saved.Revision)
		rpcErrorData(t, err, jrpc2.InvalidParams)
	}
	rpcErrorData(t, client.CallResult(ctx, "profiles.restore", map[string]string{"id": "paint", "version": version}, new(any)), jrpc2.InvalidParams)
	_, err := restore("paint", "20261007-153012-0123abcd", saved.Revision)
	rpcErrorData(t, err, -32004)
	_, err = restore("../paint", version, saved.Revision)
	rpcErrorData(t, err, jrpc2.InvalidParams)

	restored, err := restore("paint", version, saved.Revision)
	if err != nil || !restored.Applied || restored.Revision != profilelib.Revision([]byte(original)) {
		t.Fatal(restored, err)
	}
	// The put's event comes first.
	if e := waitLibraryEvent(ctx, t, events, "saved", "paint"); e["revision"] != saved.Revision {
		t.Fatal(e)
	}
	if e := waitLibraryEvent(ctx, t, events, "saved", "paint"); e["revision"] != restored.Revision || e["applied"] != true {
		t.Fatal(e)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "profiles", "paint", "profile.json")); err != nil || string(data) != original {
		t.Fatal("restored file", string(data), err)
	}
	button(src, "east", true)
	button(src, "east", false)
	tl.waitFor(t, tl.stdout, "send -F13\n")
	checkBalanced(t, tl.stdout.String())
	// The replaced version is kept, newest first; the restored one stays.
	h = historyOf("paint")
	if len(h.Snapshots) != 2 || h.Snapshots[0].Name != "renamed" || h.Snapshots[1].Version != version {
		t.Fatalf("history after restore: %+v", h)
	}

	// A kept version that no longer validates is refused with diagnostics.
	bad := "20261007-153012-0123abcd"
	if err := os.WriteFile(filepath.Join(dir, "profiles", "paint", profilelib.HistoryDir, bad+".json"), []byte(`{"schemaVersion":0,"layers":null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = restore("paint", bad, restored.Revision)
	if data := rpcErrorData(t, err, jrpc2.InvalidParams); !strings.Contains(data, `"pointer"`) {
		t.Fatal("no diagnostics", data)
	}

	rpcErrorData(t, client.CallResult(ctx, "profiles.history", map[string]string{"id": "missing"}, new(any)), -32004)
	rpcErrorData(t, client.CallResult(ctx, "profiles.history", map[string]string{"id": "../paint"}, new(any)), jrpc2.InvalidParams)
	rpcErrorData(t, client.CallResult(ctx, "profiles.history", map[string]any{"id": "paint", "extra": 1}, new(any)), jrpc2.InvalidParams)
}

// TestIPCPutWithoutHistory: a version that cannot be kept never fails the
// save; padmated logs why.
func TestIPCPutWithoutHistory(t *testing.T) {
	dir := t.TempDir()
	const original = `{"schemaVersion":0,"name":"other","layers":{"base":{}}}`
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{}}}`)
	writeLibraryProfile(t, dir, "other", original)
	if err := os.WriteFile(filepath.Join(dir, "profiles", "other", profilelib.HistoryDir), []byte("a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	tl, _ := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	changed := strings.ReplaceAll(original, `"other"`, `"other 2"`)
	var saved rpcSaved
	put := map[string]string{"id": "other", "revision": profilelib.Revision([]byte(original)), "text": changed}
	if err := client.CallResult(ctx, "profiles.put", put, &saved); err != nil || saved.Applied || saved.Revision != profilelib.Revision([]byte(changed)) {
		t.Fatal(saved, err)
	}
	tl.waitFor(t, tl.log, "profile saved without keeping the previous version")
	rpcErrorData(t, client.CallResult(ctx, "profiles.history", map[string]string{"id": "other"}, new(any)), rpcCodeProfileUnsupported)
}
