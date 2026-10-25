package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

// libraryEvents subscribes a private client to profile.changed and returns
// the notifications' params.
func libraryEvents(ctx context.Context, t *testing.T, tl *testLive) (*jrpc2.Client, chan map[string]any) {
	t.Helper()
	events := make(chan map[string]any, 32)
	client := rpcTestClient(t, tl.liveRun, &jrpc2.ClientOptions{OnNotify: func(r *jrpc2.Request) {
		var data map[string]any
		if r.Method() == "profile.changed" && r.UnmarshalParams(&data) == nil {
			events <- data
		}
	}})
	if err := client.CallResult(ctx, "subscribe", map[string]any{"topics": []string{"profile.changed"}}, new(any)); err != nil {
		t.Fatal(err)
	}
	return client, events
}

func waitLibraryEvent(ctx context.Context, t *testing.T, events chan map[string]any, kind, id string) map[string]any {
	t.Helper()
	for {
		select {
		case e := <-events:
			if e["kind"] == kind && e["id"] == id {
				return e
			}
		case <-ctx.Done():
			t.Fatalf("no profile.changed %s %s", kind, id)
		}
	}
}

// rpcErrorData fails unless err carries code and returns the error's data.
func rpcErrorData(t *testing.T, err error, code jrpc2.Code) string {
	t.Helper()
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != code {
		t.Fatalf("want error %d, got %v", code, err)
	}
	return string(rpcErr.Data)
}

func TestIPCDeleteProfile(t *testing.T) {
	dir := t.TempDir()
	profiles := map[string]string{}
	for _, id := range []string{"first", "second", "third"} {
		profiles[id] = fmt.Sprintf(`{"schemaVersion":0,"name":%q,"layers":{"base":{}}}`, id)
		writeLibraryProfile(t, dir, id, profiles[id])
	}
	revision := func(id string) string { return profilelib.Revision([]byte(profiles[id])) }
	tl, _ := startFocusLive(t, dir, "first", `{"schemaVersion":0,"defaultProfile":"first","rules":[{"exe":"a.exe","profile":"second"}]}`, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, events := libraryEvents(ctx, t, tl)
	del := func(id string, rev any) error {
		return client.CallResult(ctx, "profiles.delete", map[string]any{"id": id, "revision": rev}, new(any))
	}
	rpcErrorData(t, del("first", revision("first")), rpcCodeProfileActive)
	if refused := rpcErrorData(t, del("second", revision("second")), rpcCodeProfileReferenced); refused != `{"references":["rules/0"]}` {
		t.Fatal(refused)
	}
	if conflict := rpcErrorData(t, del("third", "stale"), -32009); !strings.Contains(conflict, revision("third")) {
		t.Fatal(conflict)
	}
	rpcErrorData(t, del("missing", ""), -32004)
	rpcErrorData(t, client.CallResult(ctx, "profiles.delete", map[string]string{"id": "third"}, new(any)), jrpc2.InvalidParams)
	rpcErrorData(t, del("../third", ""), jrpc2.InvalidParams)
	var result map[string]any
	if err := client.CallResult(ctx, "profiles.delete", map[string]string{"id": "third", "revision": revision("third")}, &result); err != nil || len(result) != 0 {
		t.Fatal(result, err)
	}
	waitLibraryEvent(ctx, t, events, "deleted", "third")
	// Deleted for good: nothing left in profiles/ and no trash folder.
	left, err := os.ReadDir(filepath.Join(dir, "profiles"))
	if err != nil || len(left) != 3 {
		t.Fatal("profiles/ after delete:", left, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "trash")); !os.IsNotExist(err) {
		t.Fatal("delete created a trash folder", err)
	}
	var items []profilelib.Entry
	if err := client.CallResult(ctx, "profiles.list", nil, &items); err != nil || len(items) != 3 || items[0].ID != profilelib.DefaultID || !items[0].Default || items[1].Default {
		t.Fatal(items, err)
	}
	// The fixed default is never deleted (PRO-28, ADR-0053).
	if data := rpcErrorData(t, del(profilelib.DefaultID, items[0].Revision), rpcCodeProfileFixed); data != `{"op":"delete"}` {
		t.Fatal(data)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles", profilelib.DefaultID, "profile.json")); err != nil {
		t.Fatal("refused delete removed the default", err)
	}

	// Switching away allows the delete: the retired defaultProfile is no
	// reference (ADR-0053). The last profile reaches state.json before
	// the folder goes.
	if err := client.CallResult(ctx, "profiles.activate", map[string]string{"id": "second"}, new(any)); err != nil {
		t.Fatal(err)
	}
	if err := del("first", revision("first")); err != nil {
		t.Fatal(err)
	}
	waitLibraryEvent(ctx, t, events, "deleted", "first")
	state, err := tl.cfg.opts.library.ReadState()
	if err != nil || state.LastProfile != "second" {
		t.Fatalf("state.json still selects a deleted profile: %+v %v", state, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles", "first")); !os.IsNotExist(err) {
		t.Fatal("first still in library", err)
	}
}

func TestIPCCopyProfileWithScript(t *testing.T) {
	dir := t.TempDir()
	const original = `{"schemaVersion":0,"name":"paint","script":{"entry":"main.ts","api":"0.21"},"layers":{"base":{}}}`
	writeLibraryProfile(t, dir, "paint", original)
	scripts := map[string]string{
		"main.ts": `import {defineScript,log,profiles} from "padmate";
import {tag} from "./lib/util";
export default defineScript({setup(){log.info("copied script ready", tag, profiles.current);}});`,
		"lib/util.ts": `export const tag = "with-util";` + "\n",
	}
	for name, text := range scripts {
		path := filepath.Join(dir, "profiles", "paint", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeLibraryProfile(t, dir, "big", original)
	for i := range profilelib.MaxCopyEntries {
		if err := os.WriteFile(filepath.Join(dir, "profiles", "big", fmt.Sprintf("f%03d.ts", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tl, _ := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	tl.waitFor(t, tl.log, "copied script ready with-util paint")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, events := libraryEvents(ctx, t, tl)
	copyProfile := func(from, to string, result any) error {
		return client.CallResult(ctx, "profiles.copy", map[string]string{"from": from, "to": to}, result)
	}
	var copied struct{ Revision string }
	if err := copyProfile("paint", "paint-copy", &copied); err != nil || copied.Revision != profilelib.Revision([]byte(original)) {
		t.Fatal(copied, err)
	}
	e := waitLibraryEvent(ctx, t, events, "saved", "paint-copy")
	if e["revision"] != copied.Revision || e["applied"] != false {
		t.Fatal(e)
	}
	for name, text := range scripts {
		data, err := os.ReadFile(filepath.Join(dir, "profiles", "paint-copy", filepath.FromSlash(name)))
		if err != nil || string(data) != text {
			t.Fatal(name, err)
		}
	}
	rpcErrorData(t, copyProfile("paint", "paint-copy", new(any)), rpcCodeProfileExists)
	rpcErrorData(t, copyProfile("missing", "other", new(any)), -32004)
	rpcErrorData(t, copyProfile("paint", "Paint", new(any)), jrpc2.InvalidParams)
	rpcErrorData(t, client.CallResult(ctx, "profiles.copy", map[string]string{"from": "paint"}, new(any)), jrpc2.InvalidParams)
	if limit := rpcErrorData(t, copyProfile("big", "big-copy", new(any)), rpcCodeProfileUnsupported); limit != `{"path":"big"}` {
		t.Fatal(limit)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles", "big-copy")); !os.IsNotExist(err) {
		t.Fatal("refused copy left a profile", err)
	}
	// The copy's script resolves its imports inside its own folder.
	if err := client.CallResult(ctx, "profiles.activate", map[string]string{"id": "paint-copy"}, new(any)); err != nil {
		t.Fatal(err)
	}
	tl.waitFor(t, tl.log, "copied script ready with-util paint-copy")
}

// The profile padmated was started with stays deletable only after a
// restart: the login entry may have saved its --profile-id or --profile.
// Started by file from inside the library, its folder is the active one.
func TestIPCDeleteStartupProfile(t *testing.T) {
	for _, byFile := range []bool{false, true} {
		t.Run(map[bool]string{false: "profile-id", true: "profile-file"}[byFile], func(t *testing.T) {
			dir := t.TempDir()
			const text = `{"schemaVersion":0,"name":"p","layers":{"base":{}}}`
			for _, id := range []string{"paint", "second"} {
				writeLibraryProfile(t, dir, id, text)
			}
			opts := options{dataDir: dir, dryRun: true, profileID: "paint"}
			if byFile {
				opts.profileID, opts.profile = "", filepath.Join(dir, "profiles", "paint", "profile.json")
			}
			tl, _ := startLibraryLive(t, `{"schemaVersion":0}`, opts)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client := rpcTestClient(t, tl.liveRun)
			del := func() error {
				return client.CallResult(ctx, "profiles.delete", map[string]string{"id": "paint", "revision": profilelib.Revision([]byte(text))}, new(any))
			}
			if byFile {
				rpcErrorData(t, del(), rpcCodeProfileActive)
			}
			if err := client.CallResult(ctx, "profiles.activate", map[string]string{"id": "second"}, new(any)); err != nil {
				t.Fatal(err)
			}
			if refused := rpcErrorData(t, del(), rpcCodeProfileReferenced); refused != `{"references":["startup"]}` {
				t.Fatal(refused)
			}
			if _, err := os.Stat(filepath.Join(dir, "profiles", "paint", "profile.json")); err != nil {
				t.Fatal("startup profile moved", err)
			}
		})
	}
}

// padmated applies config.json only through config.put; until then the
// running rules, not the edited file, decide what a delete would break.
func TestIPCDeleteProfileSelectedByRunningRules(t *testing.T) {
	dir := t.TempDir()
	const text = `{"schemaVersion":0,"name":"p","layers":{"base":{}}}`
	for _, id := range []string{"paint", "game"} {
		writeLibraryProfile(t, dir, id, text)
	}
	tl, _ := startFocusLive(t, dir, "paint", `{"schemaVersion":0,"rules":[{"exe":"game.exe","profile":"game"}]}`, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := rpcTestClient(t, tl.liveRun)
	edited := `{"schemaVersion":0}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	del := func() error {
		return client.CallResult(ctx, "profiles.delete", map[string]string{"id": "game", "revision": profilelib.Revision([]byte(text))}, new(any))
	}
	if refused := rpcErrorData(t, del(), rpcCodeProfileReferenced); refused != `{"references":["rules/0"]}` {
		t.Fatal(refused)
	}
	put := map[string]string{"revision": profilelib.Revision([]byte(edited)), "text": edited}
	if err := client.CallResult(ctx, "config.put", put, new(any)); err != nil {
		t.Fatal(err)
	}
	if err := del(); err != nil {
		t.Fatal(err)
	}
}
