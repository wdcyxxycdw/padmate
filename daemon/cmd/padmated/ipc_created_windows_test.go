package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

// profiles.list and macros.list carry when each profile's folder and each
// macro's file was created, in Unix milliseconds (UI-41, SCR-51): the
// management pages list by it. macros.list is sorted by the name without
// the extension, so "复制全部 2.ts" follows "复制全部.ts".
func TestIPCListsCarryCreated(t *testing.T) {
	dir := t.TempDir()
	before := time.Now().Add(-time.Minute).UnixMilli()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{}}}`)
	writeMacros(t, dir, map[string]string{"复制全部 2.ts": "export default function () {}", "复制全部.ts": "export default function () {}"})
	tl, _ := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := rpcTestClient(t, tl.liveRun)
	after := time.Now().Add(time.Minute).UnixMilli()

	var items []profilelib.Entry
	if err := client.CallResult(ctx, "profiles.list", nil, &items); err != nil || len(items) != 2 {
		t.Fatal(items, err)
	}
	for _, e := range items {
		if e.Created < before || e.Created > after {
			t.Errorf("profile %s created %d, want within [%d, %d]", e.ID, e.Created, before, after)
		}
	}

	var list rpcMacroList
	if err := client.CallResult(ctx, "macros.list", nil, &list); err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, m := range list.Macros {
		files = append(files, m.File)
		if m.Created < before || m.Created > after {
			t.Errorf("macro %s created %d, want within [%d, %d]", m.File, m.Created, before, after)
		}
	}
	if want := []string{"复制全部.ts", "复制全部 2.ts"}; !slices.Equal(files, want) {
		t.Fatalf("macros %q, want %q", files, want)
	}
}
