package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// macroProfile binds south's tap to 复制全部.ts, east's press to a macro
// that throws, west's press to one that logs, and north's press to a file
// that does not exist.
const macroProfile = `{"schemaVersion":0,"name":"paint","layers":{"base":{
"south":{"tap":{"type":"macro","file":"复制全部.ts"}},
"east":{"press":{"type":"macro","file":"出错.ts"}},
"west":{"press":{"type":"macro","file":"日志.ts"}},
"north":{"press":{"type":"macro","file":"不存在.ts"}},
"leftstick":{"press":{"type":"keys","keys":"F13"}}}}}`

// A profile that only runs macros (SCR-50), from the script library
// (SCR-51): its runtime starts with the slot's first Resync, so a tap runs
// the macro; script.status reports each macro's error by file and the log
// names the macro. Saving a macro the profile runs rebuilds the runtimes,
// and every change of the library tells the subscribers; a new macro that
// nothing runs rebuilds nothing. Another profile that runs the same macro
// rebuilds when the macro changes while it is active.
func TestLiveMacros(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", macroProfile)
	writeLibraryProfile(t, dir, "photo", `{"schemaVersion":0,"name":"photo","layers":{"base":{"east":{"tap":{"type":"macro","file":"复制全部.ts"}}}}}`)
	writeMacros(t, dir, map[string]string{
		"复制全部.ts": `import {key, sleep} from "padmate";
export default async function () { await key.tap("Ctrl+A"); await sleep(50); await key.tap("Ctrl+C"); }`,
		"出错.ts": "export default function () {\n  throw new Error(\"boom\");\n}\n",
		"日志.ts": `import {log} from "padmate"; export default function () { log.info("macro says hi"); }`,
	})
	tl, src := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, events := libraryEvents(ctx, t, tl)
	status := func() slot.ScriptView {
		var views []slot.ScriptView
		if err := client.CallResult(ctx, "script.status", map[string]string{"device": "1"}, &views); err != nil || len(views) != 1 {
			t.Fatal(views, err)
		}
		return views[0]
	}
	tl.waitUntil(t, "the macros' runtime", func() bool { return status().Script.State == "Running" })
	if v := status(); v.Script.Entry != "" || len(v.Script.Macros) != 0 {
		t.Fatalf("status %+v", v.Script)
	}
	tapButton := func(control string) {
		button(src, control, true)
		button(src, control, false)
	}
	tapButton("south")
	tl.waitFor(t, tl.stdout, "send +LCtrl +C\n")
	tl.waitFor(t, tl.stdout, "send -C -LCtrl\n")
	if strings.Count(tl.stdout.String(), "send +LCtrl +A\n") != 1 {
		t.Fatalf("stdout:\n%s", tl.stdout)
	}
	tapButton("leftstick")
	tl.waitFor(t, tl.stdout, "send -F13\n")

	tapButton("east")
	tapButton("north")
	tl.waitUntil(t, "the macros' errors", func() bool { return len(status().Script.Macros) == 2 })
	v := status().Script
	if v.LastError != "" || v.Macros[0].File != "不存在.ts" || !strings.Contains(v.Macros[0].Error, "not found") ||
		v.Macros[1].File != "出错.ts" || !strings.Contains(v.Macros[1].Error, "出错.ts:2") {
		t.Fatalf("status %+v", v)
	}
	tapButton("west")
	tl.waitFor(t, tl.log, "macro says hi")
	if line := logLine(tl.log.String(), "macro says hi"); !strings.Contains(line, `"source":"script"`) || !strings.Contains(line, `"macro":"日志.ts"`) {
		t.Fatalf("log line %q", line)
	}

	// A save of a macro the profile runs rebuilds the runtime.
	writeMacros(t, dir, map[string]string{"复制全部.ts": `import {key} from "padmate"; export default function () { key.tap("F21"); }`})
	waitMacrosEvent(ctx, t, events)
	tl.waitFor(t, tl.log, `"msg":"macros changed; rebuilding the scripts"`)
	tl.waitUntil(t, "the rebuilt runtime", func() bool {
		v := status().Script
		return v.State == "Running" && !v.Pending && len(v.Macros) == 0
	})
	tapButton("south")
	tl.waitFor(t, tl.stdout, "send -F21\n")

	// A new macro that no binding runs rebuilds nothing.
	writeMacros(t, dir, map[string]string{"新的.ts": `export default function () {}`})
	waitMacrosEvent(ctx, t, events)
	if n := strings.Count(tl.log.String(), "macros changed; rebuilding the scripts"); n != 1 {
		t.Fatalf("%d rebuilds, want 1", n)
	}

	// The library stays watched while another profile that runs the same
	// macro is active.
	if err := client.CallResult(ctx, "profiles.activate", map[string]string{"id": "photo"}, new(any)); err != nil {
		t.Fatal(err)
	}
	tl.waitUntil(t, "photo's runtime", func() bool {
		v := status().Script
		return v.State == "Running" && !v.Pending
	})
	tapButton("east")
	tl.waitForCount(t, tl.stdout, "send -F21\n", 2)
	writeMacros(t, dir, map[string]string{"复制全部.ts": `import {key} from "padmate"; export default function () { key.tap("F22"); }`})
	waitMacrosEvent(ctx, t, events)
	tl.waitForCount(t, tl.log, `"msg":"macros changed; rebuilding the scripts","profile":"photo"`, 1)
	tl.waitUntil(t, "photo's rebuilt runtime", func() bool {
		v := status().Script
		return v.State == "Running" && !v.Pending
	})
	tapButton("east")
	tl.waitFor(t, tl.stdout, "send -F22\n")
}

// waitMacrosEvent waits for profile.changed {kind:"macros"}, which names no
// profile: the script library is every profile's.
func waitMacrosEvent(ctx context.Context, t *testing.T, events chan map[string]any) {
	t.Helper()
	for {
		select {
		case e := <-events:
			if e["kind"] == "macros" {
				if _, ok := e["id"]; ok {
					t.Fatalf("macros event names a profile: %v", e)
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("no profile.changed macros")
		}
	}
}

// A run that holds a key ends when the profile switches: the key goes up
// (design §6.3), and a profile without macros needs no runtime.
func TestLiveMacroProfileSwitchReleases(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "first", `{"schemaVersion":0,"name":"first","layers":{"base":{"south":{"press":{"type":"macro","file":"hold.ts"}}}}}`)
	writeLibraryProfile(t, dir, "second", `{"schemaVersion":0,"name":"second","layers":{"base":{"east":{"press":{"type":"keys","keys":"F14"}}}}}`)
	writeMacros(t, dir, map[string]string{
		"hold.ts": `import {key, sleep} from "padmate";
export default async function () { key.down("F21"); await sleep(60000); }`,
	})
	tl, src := startFocusLive(t, dir, "first", `{"schemaVersion":0}`, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := rpcTestClient(t, tl.liveRun)
	tl.waitUntil(t, "the macros' runtime", func() bool {
		var views []slot.ScriptView
		return client.CallResult(ctx, "script.status", map[string]string{"device": "1"}, &views) == nil &&
			len(views) == 1 && views[0].Script.State == "Running"
	})
	button(src, "south", true)
	tl.waitFor(t, tl.stdout, "send +F21\n")
	button(src, "south", false)
	if err := client.CallResult(ctx, "profiles.activate", map[string]string{"id": "second"}, new(any)); err != nil {
		t.Fatal(err)
	}
	tl.waitFor(t, tl.stdout, "send -F21\n")
	var views []slot.ScriptView
	if err := client.CallResult(ctx, "script.status", map[string]string{"device": "1"}, &views); err != nil || len(views) != 1 || views[0].Script.State != "Absent" {
		t.Fatal(views, err)
	}
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +F14\n")
	button(src, "east", false)
	tl.waitFor(t, tl.stdout, "send -F14\n")
}
