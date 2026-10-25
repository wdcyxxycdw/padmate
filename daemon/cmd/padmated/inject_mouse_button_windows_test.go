package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/injecttest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// profileButtons taps MouseX1 on south and holds MouseRight on east: a mouse
// button is down from the press of the pad's button to its release, however
// long that is (ADR-0062, second batch).
const profileButtons = `{"schemaVersion": 1, "name": "buttons", "layers": {"base": {` +
	`"south": {"press": {"type": "keys", "keys": "MouseX1", "mode": "tap"}}, ` +
	`"east": {"press": {"type": "keys", "keys": "MouseRight", "mode": "hold"}}}}}` + "\n"

// TestLiveInjectMouseButtons (ADR-0062) is TestLiveInjectWheel for mouse buttons:
// the presses of a virtual gamepad go through live mode with --inject, and the
// mouse hook sees the button events SendInput injected, with padmated's nonce,
// the process id (the mouse hook gets no more of dwExtraInfo, measured
// 2026-10-19), and swallows them. The hooks, proven live by a canary each
// before padmated starts, swallow everything, so no click reaches the window
// under the cursor (CLAUDE.md invariant 6). Both are removed only after live
// mode has stopped and a canary of each has fenced its events, also when the
// test fails.
//
// Unlike the tests that inject the buttons themselves (the output package's
// TestInjectMouseButtons, which always press and release in one batch), here
// padmated holds a button across SendInput calls, which is what a hold binding
// is. That is safe because the hook that swallows the press is in place until
// padmated has released it: waitMouse stops the test at the first event that
// got past the hook, and the cleanup stops padmated, which releases the button,
// before the hooks go (stopFencedMouse).
//
// South taps MouseX1: the hook must see a WM_XBUTTONDOWN and a WM_XBUTTONUP,
// both with the side button number 1 in the high word of mouseData. East holds
// MouseRight: a WM_RBUTTONDOWN when the pad's button goes down and a
// WM_RBUTTONUP when it goes up. Then east is pressed again and padmated stops
// while the button is held, and it must release it: a last WM_RBUTTONUP, which
// the hook swallows too (invariant 1: no exit path leaves a button down). The
// keyboard hook sees nothing from padmated.
func TestLiveInjectMouseButtons(t *testing.T) {
	injecttest.Require(t)
	pid := uint32(os.Getpid())
	rec := injecttest.StartFunc(t, func(n uint32) bool { return n == pid })
	rec.Canary(t, pid)
	rec.MouseCanary(t, pid)
	t.Logf("mouse buttons swapped (SM_SWAPBUTTON): %v", injecttest.ButtonsSwapped())
	// Cleanups run last in first: this one after that of startTestLive and
	// before the one that removes the hooks.
	var tl *testLive
	fenced := false
	t.Cleanup(func() {
		if !fenced {
			stopFencedMouse(t, rec, pid, tl)
		}
	})

	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(profileButtons), 0o644); err != nil {
		t.Fatal(err)
	}
	tl = startTestLive(t, nil, "--profile", path, "--inject")
	v := tl.attach(t)
	tl.waitFor(t, tl.log, `"msg":"slot assigned"`)

	// The tap of MouseX1: its press and its release.
	press(t, v, "south", true)
	press(t, v, "south", false)
	waitMouse(t, rec, pid, 2)
	// The hold of MouseRight: down with the pad's button, up with its release.
	press(t, v, "east", true)
	waitMouse(t, rec, pid, 3)
	press(t, v, "east", false)
	waitMouse(t, rec, pid, 4)
	// And held when padmated stops.
	press(t, v, "east", true)
	waitMouse(t, rec, pid, 5)

	code := stopFencedMouse(t, rec, pid, tl)
	fenced = true
	if code != 0 {
		t.Fatalf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
	}

	events, canaries := ownMouse(rec, pid)
	if want := 2 * injecttest.MouseCanaryLen; canaries < want { // one before padmated started, one after it stopped
		t.Errorf("the mouse hook saw %d moves of canaries, want %d", canaries, want)
	}
	want := []struct {
		button keys.Mouse
		up     bool
	}{
		{keys.MouseX1, false}, {keys.MouseX1, true},
		{keys.MouseRight, false}, {keys.MouseRight, true},
		{keys.MouseRight, false}, {keys.MouseRight, true}, // the release by the stop
	}
	if len(events) != len(want) {
		t.Fatalf("the mouse hook saw %d button events, want %d: %+v", len(events), len(want), events)
	}
	for i, e := range events {
		for _, p := range injecttest.ButtonProblems(e, want[i].button, want[i].up, pid) {
			t.Errorf("button event %d (%v, up %v): %s; saw %+v", i, want[i].button, want[i].up, p, e)
		}
	}
	if keyEvents, _ := ownKeys(rec, pid); len(keyEvents) != 0 {
		t.Errorf("the keyboard hook saw %d keys of padmated for mouse buttons, want none: %v", len(keyEvents), keyEvents)
	}
}
