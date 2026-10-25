package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

func TestIPCScriptInspectionReloadAndPausedDescribe(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","script":{"entry":"main.ts","api":"0.21"},"layers":{"base":{}}}`)
	const source = `import {defineScript,defineAction,param,on,every,log} from "padmate";
export default defineScript({params:{size:param.number({default:2})},
actions:{paint:defineAction({label:"Paint",run(){}})},
setup(){on("south","press",e=>e.holdKeys("F13"),{label:"Draw"});on("east","press",()=>{throw new Error("handler diagnostic");});every(20,()=>log.info("ipc script running"));}});`
	if err := os.WriteFile(filepath.Join(dir, "profiles", "paint", "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	tl, src := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	tl.waitFor(t, tl.log, "ipc script running")
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	query := func(method string) []slot.ScriptView {
		t.Helper()
		var views []slot.ScriptView
		if err := client.CallResult(ctx, method, map[string]string{"device": "1"}, &views); err != nil {
			t.Fatal(err)
		}
		if len(views) != 1 {
			t.Fatal(views)
		}
		return views
	}
	view := query("script.status")[0]
	if view.Script.State != "Running" || view.Script.Entry != "main.ts" {
		t.Fatal(view)
	}
	view = query("script.registrations")[0]
	if len(view.Script.Metadata.Registrations) != 2 || view.Script.Metadata.Registrations[0].Label != "Draw" {
		t.Fatal(view)
	}
	oldRuntime := view.Script.Metadata.Runtime
	button(src, "east", true)
	button(src, "east", false)
	tl.waitUntil(t, "handler diagnostic", func() bool { return strings.Contains(query("script.status")[0].Script.LastError, "handler diagnostic") })
	var logs struct {
		Entries []ipc.LogRecord `json:"entries"`
		Next    uint64          `json:"next"`
	}
	if err := client.CallResult(ctx, "script.logs", map[string]any{"device": "1", "limit": 1}, &logs); err != nil || len(logs.Entries) != 1 || logs.Next == 0 {
		t.Fatal(logs, err)
	}
	var logged map[string]any
	if err := json.Unmarshal(logs.Entries[0].Record, &logged); err != nil || logged["profile"] != "paint" || logged["device"] != float64(1) {
		t.Fatal(logged, err)
	}
	button(src, "south", true)
	tl.waitFor(t, tl.stdout, "send +F13\n")
	query("script.reload")
	tl.waitFor(t, tl.stdout, "send -F13\n")
	tl.waitUntil(t, "new script installed", func() bool {
		v := query("script.registrations")[0]
		return v.Script.State == "Running" && v.Script.Metadata.Runtime != oldRuntime
	})
	// A failed candidate keeps the active runtime and an inspectable diagnostic.
	active := query("script.status")[0].Script.Metadata.Runtime
	if err := os.WriteFile(filepath.Join(dir, "profiles", "paint", "main.ts"), []byte(`throw new Error("broken candidate");`), 0o600); err != nil {
		t.Fatal(err)
	}
	query("script.reload")
	tl.waitUntil(t, "failed reload diagnostic", func() bool {
		v := query("script.status")[0].Script
		return !v.Pending && strings.Contains(v.LastError, "broken candidate") && v.Metadata.Runtime == active
	})
	if err := os.WriteFile(filepath.Join(dir, "profiles", "paint", "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.CallResult(ctx, "engine.pause", nil, new(any)); err != nil {
		t.Fatal(err)
	}
	view = query("script.status")[0]
	if !view.Script.Paused {
		t.Fatal(view)
	}
	var description rpcScriptDescription
	if err := client.CallResult(ctx, "script.describe", nil, &description); err != nil || len(description.Metadata.Actions) != 1 || description.Metadata.Params["size"].Default != float64(2) {
		t.Fatal(description, err)
	}
	query("script.reload")
	view = query("script.status")[0]
	if !view.Script.Paused || view.Script.Pending {
		t.Fatal("reload started paused runtime", view)
	}
	if err := client.CallResult(ctx, "script.status", map[string]string{"device": "missing"}, new(any)); err == nil {
		t.Fatal("unknown device accepted")
	}
	checkBalanced(t, tl.stdout.String())
}

func TestIPCScriptDescribeCannotAffectLiveMapping(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{}}}`)
	writeLibraryProfile(t, dir, "describe", `{"schemaVersion":0,"name":"describe","script":{"entry":"main.ts","api":"0.21"},"layers":{"base":{}}}`)
	const source = `import {defineScript,defineAction,key,every} from "padmate";
export default defineScript({actions:{test:defineAction({label:"Describe",run(){}})},
setup(){key.down("F14");every(1,()=>key.down("F15"));return ()=>key.down("F16");},
onActivate(){throw new Error("describe must not activate");}});`
	if err := os.WriteFile(filepath.Join(dir, "profiles", "describe", "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	tl, _ := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	before := tl.stdout.String()
	var desc rpcScriptDescription
	if err := client.CallResult(ctx, "script.describe", map[string]string{"id": "describe"}, &desc); err != nil {
		t.Fatal(err)
	}
	if len(desc.Metadata.Actions) != 1 || desc.Metadata.Actions[0].Label != "Describe" || tl.stdout.String() != before {
		t.Fatal("describe affected output or lost metadata", desc, tl.stdout.String())
	}
}
