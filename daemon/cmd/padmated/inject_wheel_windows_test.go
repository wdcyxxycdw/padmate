package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/injecttest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

// profileWheels taps WheelDown on south and holds WheelUp on east: a wheel key
// is a notch when it is pressed and nothing when it is released, however long
// the button is held (ADR-0062).
const profileWheels = `{"schemaVersion": 1, "name": "wheels", "layers": {"base": {` +
	`"south": {"press": {"type": "keys", "keys": "WheelDown", "mode": "tap"}}, ` +
	`"east": {"press": {"type": "keys", "keys": "WheelUp", "mode": "hold"}}}}}` + "\n"

// TestLiveInjectWheel (ADR-0062) is TestLiveInjectVirtual for wheel keys: the
// presses of a virtual gamepad go through live mode with --inject, and the
// mouse hook sees the notches SendInput injected, with padmated's nonce, the
// process id (the mouse hook gets no more of dwExtraInfo, measured
// 2026-10-19), and swallows them. The hooks, proven live by a canary
// each before padmated starts, swallow everything, so no notch reaches the
// window under the cursor (CLAUDE.md invariant 6). Both are removed only after
// live mode has stopped and a canary of each has fenced its events, also when
// the test fails.
//
// South taps WheelDown twice and east holds WheelUp once, and the hook must see
// WheelDown, WheelUp, WheelDown in that order, a notch for each press and none
// for the release of east: padmated sends the release, and SendInput injects
// nothing for it. The keyboard hook sees nothing from padmated.
func TestLiveInjectWheel(t *testing.T) {
	injecttest.Require(t)
	pid := uint32(os.Getpid())
	rec := injecttest.StartFunc(t, func(n uint32) bool { return n == pid })
	rec.Canary(t, pid)
	rec.MouseCanary(t, pid)
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
	if err := os.WriteFile(path, []byte(profileWheels), 0o644); err != nil {
		t.Fatal(err)
	}
	tl = startTestLive(t, nil, "--profile", path, "--inject")
	v := tl.attach(t)
	tl.waitFor(t, tl.log, `"msg":"slot assigned"`)

	tap := func(n int) {
		t.Helper()
		press(t, v, "south", true)
		press(t, v, "south", false)
		waitMouse(t, rec, pid, n)
	}
	tap(1)
	press(t, v, "east", true)
	waitMouse(t, rec, pid, 2)
	press(t, v, "east", false)
	// The release of east comes before this tap on the slot, so a notch for it
	// would be the third event, not WheelDown.
	tap(3)

	code := stopFencedMouse(t, rec, pid, tl)
	fenced = true
	if code != 0 {
		t.Fatalf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
	}

	wheels, canaries := ownMouse(rec, pid)
	if want := 2 * injecttest.MouseCanaryLen; canaries < want { // one before padmated started, one after it stopped
		t.Errorf("the mouse hook saw %d moves of canaries, want %d", canaries, want)
	}
	want := []int{-120, 120, -120}
	if len(wheels) != len(want) {
		t.Fatalf("the mouse hook saw %d notches, want %d: %+v", len(wheels), len(want), wheels)
	}
	for i, e := range wheels {
		if e.Msg != winhook.WMMouseWheel || e.WheelDelta() != want[i] || !e.Injected() || e.LowerILInjected() ||
			!output.MayBePadmateMouse(e.Extra) || output.Nonce(e.Extra) != pid || !e.Swallowed {
			t.Errorf("notch %d = {%s delta %d (mouseData %#x) flags 0x%02X extra %#x swallowed %v}, want %s delta %d, LLMHF_INJECTED only, padmated's nonce, swallowed",
				i, winhook.MouseMsgName(e.Msg), e.WheelDelta(), e.MouseData, e.Flags, uint64(e.Extra), e.Swallowed,
				winhook.MouseMsgName(winhook.WMMouseWheel), want[i])
		}
	}
	if keyEvents, _ := ownKeys(rec, pid); len(keyEvents) != 0 {
		t.Errorf("the keyboard hook saw %d keys of padmated for wheel keys, want none: %v", len(keyEvents), keyEvents)
	}
}

// ownMouse returns the mouse events with nonce that the recorder saw, other
// than the canaries, and how many events of canaries it saw (each canary has
// injecttest.MouseCanaryLen).
func ownMouse(rec *injecttest.Recorder, nonce uint32) (evs []winhook.MouseEvent, canaries int) {
	for _, e := range rec.OwnMouse() {
		switch {
		case output.Nonce(e.Extra) != nonce:
		case injecttest.IsMouseCanary(e, nonce):
			canaries++
		default:
			evs = append(evs, e)
		}
	}
	return evs, canaries
}

// waitMouse waits, at most liveWait, until the recorder has seen n mouse events
// (notches or buttons) with nonce. An event the hook let through stops the
// test at once: its cleanup then stops padmated, which releases what it holds,
// and fences that with the canaries.
func waitMouse(t *testing.T, rec *injecttest.Recorder, nonce uint32, n int) {
	t.Helper()
	deadline := time.Now().Add(liveWait)
	for {
		got, _ := ownMouse(rec, nonce)
		for _, e := range got {
			if !e.Swallowed {
				t.Fatalf("a mouse event of padmated got past the hook: %+v; stopping padmated", e)
			}
		}
		if len(got) >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the mouse hook saw %d events after %v, want %d: %+v", len(got), liveWait, n, got)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// stopFencedMouse is stopFenced for a test with the mouse hook: it stops live
// mode, when tl is not nil, and fences what it injected with a canary of each
// hook, sent after it has stopped. Once both hooks have seen theirs, they have
// seen everything before, and they may go. It returns the exit code.
func stopFencedMouse(t *testing.T, rec *injecttest.Recorder, pid uint32, tl *testLive) (code int) {
	t.Helper()
	code = stopLive(t, tl)
	_, keyBefore := ownKeys(rec, pid)
	_, mouseBefore := ownMouse(rec, pid)
	injecttest.SendCanary(t, pid)
	injecttest.SendMouseCanary(t, pid)
	deadline := time.Now().Add(injecttest.CanaryTimeout)
	for {
		_, keyNow := ownKeys(rec, pid)
		_, mouseNow := ownMouse(rec, pid)
		if keyNow > keyBefore && mouseNow >= mouseBefore+injecttest.MouseCanaryLen {
			return code
		}
		if time.Now().After(deadline) {
			t.Fatalf("the hooks did not see the canaries sent after the stop within %v (keyboard %v, mouse %v)",
				injecttest.CanaryTimeout, keyNow > keyBefore, mouseNow >= mouseBefore+injecttest.MouseCanaryLen)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
