package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

// The profile format is frozen at version 1 (PRO-01, PRO-06). Over IPC a
// file of version 0 reads, validates and saves as it is (profiles.get shows
// its own bytes, never the migrated ones), and a file of a newer version is
// refused with a message that says what to do.
func TestIPCProfileVersions(t *testing.T) {
	dir := t.TempDir()
	const (
		old     = `{"schemaVersion":0,"name":"old","layers":{"base":{"east":{"press":{"type":"keys","keys":"F13"}}}}}`
		current = `{"schemaVersion":1,"name":"current","layers":{"base":{"east":{"press":{"type":"keys","keys":"F13"}}}}}`
		future  = `{"schemaVersion":2,"name":"future","layers":{"base":{}}}`
	)
	writeLibraryProfile(t, dir, "old", old)
	writeLibraryProfile(t, dir, "current", current)
	writeLibraryProfile(t, dir, "future", future)
	tl, _ := startFocusLive(t, dir, "old", `{"schemaVersion":0}`, false)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for id, text := range map[string]string{"old": old, "current": current} {
		var doc rpcDocument
		if err := client.CallResult(ctx, "profiles.get", map[string]string{"id": id}, &doc); err != nil ||
			doc.Text != text || doc.Problem != "" || doc.Revision != profilelib.Revision([]byte(text)) {
			t.Fatalf("profiles.get %s: %+v, %v", id, doc, err)
		}
	}
	var doc rpcDocument
	if err := client.CallResult(ctx, "profiles.get", map[string]string{"id": "future"}, &doc); err != nil ||
		doc.Text != future || !strings.Contains(doc.Problem, "upgrade padmate") {
		t.Fatalf("profiles.get future: %+v, %v", doc, err)
	}

	for text, valid := range map[string]bool{old: true, current: true, future: false} {
		var result rpcValidation
		if err := client.CallResult(ctx, "profiles.validate", map[string]string{"text": text}, &result); err != nil || result.Valid != valid {
			t.Fatalf("profiles.validate %s: %+v, %v", text, result, err)
		}
		if !valid && (len(result.Diagnostics) != 1 || !strings.Contains(result.Diagnostics[0].Message, "newer than supported version 1; upgrade padmate")) {
			t.Fatalf("profiles.validate future: %+v", result)
		}
	}

	// A save writes the bytes it is sent.
	for id, text := range map[string]string{"saved-old": old, "saved-current": current} {
		var saved rpcSaved
		put := map[string]string{"id": id, "revision": "", "text": text}
		if err := client.CallResult(ctx, "profiles.put", put, &saved); err != nil || saved.Revision != profilelib.Revision([]byte(text)) {
			t.Fatalf("profiles.put %s: %+v, %v", id, saved, err)
		}
		var got rpcDocument
		if err := client.CallResult(ctx, "profiles.get", map[string]string{"id": id}, &got); err != nil || got.Text != text {
			t.Fatalf("profiles.get %s after put: %+v, %v", id, got, err)
		}
	}
	put := map[string]string{"id": "saved-future", "revision": "", "text": future}
	var rpcErr *jrpc2.Error
	if err := client.CallResult(ctx, "profiles.put", put, new(any)); !errors.As(err, &rpcErr) || rpcErr.Code != jrpc2.InvalidParams ||
		!strings.Contains(string(rpcErr.Data), "upgrade padmate") {
		t.Fatalf("profiles.put of version 2: %v", err)
	}
	if err := client.CallResult(ctx, "profiles.get", map[string]string{"id": "saved-future"}, new(any)); err == nil {
		t.Fatal("a refused save created the profile")
	}
}
