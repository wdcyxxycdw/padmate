package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

// profileFolderNames lists profiles/ in the data directory dir.
func profileFolderNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "profiles"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// A file another program holds open (os.Open shares no delete) cannot be
// deleted. Once the folder has left the library, profiles.delete still
// succeeds: clients drop the profile, and the next copy deletes the hidden
// remains.
func TestIPCDeleteProfileIncomplete(t *testing.T) {
	dir := t.TempDir()
	const first = `{"schemaVersion":0,"name":"first","layers":{"base":{}}}`
	const paint = `{"schemaVersion":0,"name":"paint","layers":{"base":{}}}`
	writeLibraryProfile(t, dir, "first", first)
	writeLibraryProfile(t, dir, "paint", paint)
	if err := os.WriteFile(filepath.Join(dir, "profiles", "paint", "main.ts"), []byte("export {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tl, _ := startFocusLive(t, dir, "first", `{"schemaVersion":0}`, false)
	var mu sync.Mutex
	var held *os.File
	release := func() {
		mu.Lock()
		defer mu.Unlock()
		if held != nil {
			_ = held.Close()
			held = nil
		}
	}
	t.Cleanup(release) // before TempDir's cleanup, which the open file would fail
	tl.cfg.opts.library.SetRemoveHook(func(tmp string) {
		f, err := os.Open(filepath.Join(dir, "profiles", tmp, "main.ts"))
		if err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		held = f
		mu.Unlock()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, events := libraryEvents(ctx, t, tl)
	var result map[string]any
	if err := client.CallResult(ctx, "profiles.delete", map[string]string{"id": "paint", "revision": profilelib.Revision([]byte(paint))}, &result); err != nil || len(result) != 0 {
		t.Fatal(result, err)
	}
	mu.Lock()
	holding := held != nil
	mu.Unlock()
	if !holding {
		t.Fatal("no file held open during the delete")
	}
	waitLibraryEvent(ctx, t, events, "deleted", "paint")
	var items []profilelib.Entry
	if err := client.CallResult(ctx, "profiles.list", nil, &items); err != nil || len(items) != 2 || items[0].ID != profilelib.DefaultID || items[1].ID != "first" {
		t.Fatal(items, err)
	}
	names := profileFolderNames(t, dir)
	if len(names) != 3 || !strings.HasPrefix(names[0], ".tmp-paint-") || names[1] != profilelib.DefaultID || names[2] != "first" {
		t.Fatal("profiles/ after an incomplete delete:", names)
	}
	release()
	tl.cfg.opts.library.SetRemoveHook(nil)
	if err := client.CallResult(ctx, "profiles.copy", map[string]string{"from": "first", "to": "copy"}, new(any)); err != nil {
		t.Fatal(err)
	}
	if names := profileFolderNames(t, dir); !slices.Equal(names, []string{"copy", profilelib.DefaultID, "first"}) {
		t.Fatal("profiles/ after the next copy:", names)
	}
}
