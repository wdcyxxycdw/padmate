package main

import (
	"context"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
)

// functionKeyLiveProfile has a base layer with a binding on each of the two
// controls that can be the function key, so that whichever is not it is an
// ordinary control, and a function set that rebinds south and binds the two
// too. Both also bind guide, which padmate never maps (ADR-0012). Everything
// holds its key (no mode), so that the printer shows each press and release.
const functionKeyLiveProfile = `{"schemaVersion":1,"name":"first","layers":{"base":{
	"south":{"press":{"type":"keys","keys":"F13"}},
	"east":{"press":{"type":"keys","keys":"Space"}},
	"back":{"press":{"type":"keys","keys":"F15"}},
	"start":{"press":{"type":"keys","keys":"F16"}},
	"guide":{"press":{"type":"keys","keys":"F18"}}}},
 "functionSet":{
	"south":{"press":{"type":"keys","keys":"F1"}},
	"back":{"press":{"type":"keys","keys":"F5"}},
	"start":{"press":{"type":"keys","keys":"F6"}},
	"guide":{"press":{"type":"keys","keys":"F7"}}}}`

// functionKey returns the function key that slot 0 runs: the one of its
// controller (ADR-0017).
func (tl *testLive) functionKey() control.Control {
	ctx, cancel := context.WithTimeout(context.Background(), liveWait)
	defer cancel()
	views, err := tl.sup.Inspect(ctx)
	if err != nil {
		panic(err)
	}
	for _, v := range views {
		if v.Index == 0 {
			return v.Mapping.ProgramFunctionKey
		}
	}
	panic("no slot 0")
}

// TestConfigPutAppliesFunctionKeyLive: config.json's functionKey, the retired
// top-level one that a controller with the standard layout follows, applies on
// config.put without a restart (ADR-0012, ADR-0017). The slot of the controller
// switches to the Program with the new function key, as it does for any
// setting of a controller: the keys held through the old function set are
// released before config.put returns (invariant 1), a function key that is held
// is forgotten, and the presses after it use the new function key. The profile
// is not reloaded for it, and a config that does not change the key leaves the
// slot alone.
// guide, which an old config.json may still choose, reads as start, and sends
// nothing whichever key is the function key.
func TestConfigPutAppliesFunctionKeyLive(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "first", functionKeyLiveProfile)
	tl, src := startFocusLive(t, dir, "first", `{"schemaVersion":0}`, false)
	client := rpcTestClient(t, tl.liveRun)
	if got := tl.functionKey(); got != control.Start {
		t.Fatalf("the function key at startup is %s, want start", got)
	}
	// The binding on the function key, in the layer and in the set, is
	// ignored, and that is said once.
	if lines := logLines(tl.log.String(), "bindings on the function key are ignored"); len(lines) != 1 ||
		!strings.Contains(lines[0], `"level":"INFO"`) || !strings.Contains(lines[0], `"functionKey":"start"`) ||
		!strings.Contains(lines[0], `"count":2`) || !strings.Contains(lines[0], "/layers/base/start") || !strings.Contains(lines[0], "/functionSet/start") {
		t.Fatalf("log lines %q", lines)
	}
	press := func(control string, want string) {
		t.Helper()
		n := strings.Count(tl.stdout.String(), "send +"+want+"\n")
		button(src, control, true)
		tl.waitForCount(t, tl.stdout, "send +"+want+"\n", n+1)
	}
	release := func(control string, want string) {
		t.Helper()
		n := strings.Count(tl.stdout.String(), "send -"+want+"\n")
		button(src, control, false)
		tl.waitForCount(t, tl.stdout, "send -"+want+"\n", n+1)
	}
	quiet := func(what string) {
		t.Helper()
		tl.sync()
		before := tl.stdout.String()
		tl.sync()
		if after := tl.stdout.String(); after != before {
			t.Fatalf("%s sent something:\n%s", what, strings.TrimPrefix(after, before))
		}
	}

	// start is the function key: it sends nothing and south uses the set.
	button(src, "start", true)
	quiet("the function key")
	press("south", "F1")
	press("east", "Space") // held through the change, with its normal binding
	gen := tl.profileTarget.Load().gen
	saved := putConfig(t, client, `{"schemaVersion":0,"functionKey":"back"}`)
	if !saved.Applied || saved.RestartRequired || saved.Problem != "" {
		t.Fatalf("function key not applied live: %+v", saved)
	}
	// The slot let go of both keys, before config.put returned.
	out := tl.stdout.String()
	at := strings.LastIndex(out, " cancel ")
	if at < 0 || !strings.Contains(out[at:], " -F1") || !strings.Contains(out[at:], " -Space") {
		t.Fatalf("held keys not released by the function key change:\n%s", out)
	}
	if got := tl.profileTarget.Load(); got.gen != gen || got.id != "first" {
		t.Fatalf("the profile was reloaded for a function key: %+v, was generation %d", got, gen)
	}
	if got := tl.functionKey(); got != control.Back {
		t.Fatalf("the function key after config.put is %s, want back", got)
	}
	// The releases of what was held, and of start, which is an ordinary control
	// now and was the function key when it went down, send nothing.
	button(src, "south", false)
	button(src, "east", false)
	button(src, "start", false)
	quiet("the releases after the change")
	// start is ordinary: its layer binding works now.
	press("start", "F16")
	release("start", "F16")
	// back is the function key: nothing for it, the set for south, and the
	// set's binding for the ordinary center key.
	button(src, "back", true)
	quiet("the new function key")
	press("south", "F1")
	release("south", "F1")
	press("start", "F6")
	release("start", "F6")
	button(src, "guide", true)
	button(src, "guide", false)
	quiet("guide under the function key")
	button(src, "back", false)
	press("south", "F13")
	release("south", "F13")
	// The ignored bindings are said again, for the new key.
	if lines := logLines(tl.log.String(), "bindings on the function key are ignored"); len(lines) != 2 || !strings.Contains(lines[1], `"functionKey":"back"`) || !strings.Contains(lines[1], "/layers/base/back") {
		t.Fatalf("log lines %q", lines)
	}

	// Another field: the function key, and the profile, stay.
	gen = tl.profileTarget.Load().gen
	if saved := putConfig(t, client, `{"schemaVersion":0,"functionKey":"back","output":{"sendToOwnWindow":false}}`); !saved.Applied {
		t.Fatalf("%+v", saved)
	}
	if got := tl.profileTarget.Load().gen; got != gen {
		t.Fatalf("an unchanged function key reloaded the profile: generation %d, was %d", got, gen)
	}

	// The function key held at the change is forgotten, and the key it was is
	// an ordinary control: south, pressed while back is still down, uses its
	// normal binding. guide, which a config.json written before padmate
	// stopped mapping it may hold, is taken as start.
	button(src, "back", true)
	tl.sync()
	if saved := putConfig(t, client, `{"schemaVersion":0,"functionKey":"guide"}`); !saved.Applied || saved.Problem != "" {
		t.Fatalf("%+v", saved)
	}
	if got := tl.functionKey(); got != control.Start {
		t.Fatalf("the function key after config.put of guide is %s, want start", got)
	}
	press("south", "F13")
	release("south", "F13")
	button(src, "back", false)
	quiet("the release of the old function key")
	press("back", "F15")
	release("back", "F15")
	button(src, "guide", true)
	button(src, "guide", false)
	quiet("guide")
	button(src, "start", true)
	press("south", "F1")
	release("south", "F1")
	button(src, "start", false)
	// The default is start again when the member is left out.
	if saved := putConfig(t, client, `{"schemaVersion":0}`); !saved.Applied {
		t.Fatalf("%+v", saved)
	}
	if got := tl.functionKey(); got != control.Start {
		t.Fatalf("the function key after the member was left out is %s, want start", got)
	}
	button(src, "start", true)
	press("south", "F1")
	release("south", "F1")
	button(src, "start", false)
	tl.cancel()
	if tl.waitCode(t) != 0 {
		t.Fatal(tl.stderr.String())
	}
	checkBalanced(t, tl.stdout.String())
}

// A function key that config.json holds at startup is the one profiles are
// compiled with, and so are the ones activated later; a config that is not
// valid leaves the default.
func TestStartupFunctionKeyFromConfig(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "first", functionKeyLiveProfile)
	writeLibraryProfile(t, dir, "second", `{"schemaVersion":1,"name":"second","layers":{"base":{"south":{"press":{"type":"keys","keys":"F13"}}}},"functionSet":{"south":{"press":{"type":"keys","keys":"F2"}}}}`)
	tl, src := startFocusLive(t, dir, "first", `{"schemaVersion":0,"functionKey":"back"}`, false)
	client := rpcTestClient(t, tl.liveRun)
	if got := tl.functionKey(); got != control.Back {
		t.Fatalf("the function key at startup is %s, want back", got)
	}
	button(src, "back", true)
	button(src, "south", true)
	tl.waitFor(t, tl.stdout, "send +F1\n")
	button(src, "south", false)
	tl.waitFor(t, tl.stdout, "send -F1\n")
	button(src, "back", false)
	ctx, cancel := context.WithTimeout(context.Background(), liveWait)
	defer cancel()
	if err := client.CallResult(ctx, "profiles.activate", map[string]string{"id": "second"}, new(any)); err != nil {
		t.Fatal(err)
	}
	if got := tl.functionKey(); got != control.Back {
		t.Fatalf("the function key of a profile activated later is %s, want back", got)
	}
	button(src, "back", true)
	button(src, "south", true)
	tl.waitFor(t, tl.stdout, "send +F2\n")
	button(src, "south", false)
	button(src, "back", false)
	tl.cancel()
	if tl.waitCode(t) != 0 {
		t.Fatal(tl.stderr.String())
	}
	checkBalanced(t, tl.stdout.String())
}
