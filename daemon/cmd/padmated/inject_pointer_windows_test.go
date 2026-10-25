package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/injecttest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

// pointerQuiet is how long padmated must stay without a move for the real
// injection test to take it as stopped: a dozen ticks of the stick (8 ms).
const pointerQuiet = 100 * time.Millisecond

// TestLiveInjectPointer (ADR-0062, section 5) is TestLiveInjectVirtual for the
// stick that moves the pointer: the left stick of a virtual gamepad is pushed
// through live mode with --inject (profilePointer), and the mouse hook sees the
// moves SendInput injected, WM_MOUSEMOVE with padmated's nonce, the process id
// (the mouse hook gets no more of dwExtraInfo, measured 2026-10-19), and
// swallows them. The hooks, proven live by a canary each before padmated starts,
// swallow everything, so the pointer of the machine does not move (CLAUDE.md
// invariant 6). Both are removed only after live mode has stopped and a canary
// of each has fenced its events, also when the test fails.
//
// Unlike the tests that inject moves themselves (the output package's
// TestInjectPointerMoves, which always injects moves that add up to nothing),
// padmated's moves go one way for as long as the stick is pushed. That is safe
// because the hook that swallows them is in place until padmated has stopped:
// waitPointer stops the test at the first move that got past the hook, and the
// cleanup stops padmated before the hooks go (stopFencedPointer). Were the hook
// to fail at the first move, the cursor would go a few pixels, not a click or a
// key.
//
// The steps: the stick is pushed to the right until the hook has seen eight
// moves; centered, after which padmated must send no more, which two canaries a
// hundred milliseconds apart show (the moves between them would be counted); and
// pushed to the left until eight more; then padmated stops with the stick still
// pushed, after which it must send no more either. The canaries carry nonces of
// their own: the hook reports a move of padmated and a move of a canary alike
// (WM_MOUSEMOVE, injected, a nonce), and tells them apart by nonce only. The
// keyboard hook sees nothing from padmated. The points of the first moves are
// logged, since how the system reports a swallowed relative move is unmeasured.
func TestLiveInjectPointer(t *testing.T) {
	injecttest.Require(t)
	pid := uint32(os.Getpid())
	fenceBase := injecttest.NonceBase() | 0x80000000 // far from any process id
	rec := injecttest.StartFunc(t, func(n uint32) bool { return n == pid || injecttest.Owns(fenceBase, n) })
	rec.Canary(t, pid)
	rec.MouseCanary(t, fenceBase)
	f := &pointerFence{rec: rec, pid: pid, next: fenceBase + 1}
	// Cleanups run last in first: this one after that of startTestLive and
	// before the one that removes the hooks.
	var tl *testLive
	fenced := false
	t.Cleanup(func() {
		if !fenced {
			stopFencedPointer(t, rec, f, tl)
		}
	})

	tl = startTestLive(t, nil, "--profile", writePointerProfile(t), "--inject")
	v := tl.attach(t)
	tl.waitFor(t, tl.log, `"msg":"slot assigned"`)

	// Pushed to the right: moves.
	started := time.Now()
	if err := v.SetAxis("leftx", 32767); err != nil {
		t.Fatalf("SetAxis(leftx): %v", err)
	}
	waitPointer(t, rec, pid, 8)
	t.Logf("the first 8 moves came %v after the push", time.Since(started))

	// Centered: they stop, and stay stopped.
	if err := v.SetAxis("leftx", 0); err != nil {
		t.Fatalf("SetAxis(leftx): %v", err)
	}
	right := expectStill(t, rec, f, "the stick was centered")

	// Pushed to the left: moves again.
	if err := v.SetAxis("leftx", -32768); err != nil {
		t.Fatalf("SetAxis(leftx): %v", err)
	}
	waitPointer(t, rec, pid, right+8)

	// padmated stops with the stick pushed: no more moves.
	code := stopFencedPointer(t, rec, f, tl)
	fenced = true
	if code != 0 {
		t.Fatalf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
	}
	total := expectStill(t, rec, f, "padmated stopped")

	moves, other := padmatedPointer(rec, pid)
	if total != len(moves) {
		t.Errorf("the hook saw %d moves of padmated before the last canary and %d in all", total, len(moves))
	}
	if len(other) != 0 {
		t.Errorf("the mouse hook saw %d events of padmated that are not moves, want none: %+v", len(other), other)
	}
	if len(moves) < right+8 {
		t.Fatalf("the mouse hook saw %d moves, want at least %d", len(moves), right+8)
	}
	var points []string
	for i, e := range moves {
		for _, p := range injecttest.PointerProblems(e, pid) {
			t.Errorf("move %d: %s; saw %+v", i, p, e)
		}
		if i < 3 || i == right || i == right+1 {
			points = append(points, fmt.Sprintf("#%d (%d, %d)", i, e.X, e.Y))
		}
	}
	t.Logf("%d moves in all, %d to the right; the points of some: %s", len(moves), right, strings.Join(points, ", "))
	if keyEvents, _ := ownKeys(rec, pid); len(keyEvents) != 0 {
		t.Errorf("the keyboard hook saw %d keys of padmated for a stick that moves the pointer, want none: %v", len(keyEvents), keyEvents)
	}
}

// padmatedPointer returns the mouse events of padmated (nonce pid) that the
// recorder saw: its pointer moves and, apart, anything else. The canaries have
// nonces of their own.
func padmatedPointer(rec *injecttest.Recorder, pid uint32) (moves, other []winhook.MouseEvent) {
	for _, e := range rec.OwnMouse() {
		switch {
		case output.Nonce(e.Extra) != pid:
		case e.Msg == winhook.WMMouseMove:
			moves = append(moves, e)
		default:
			other = append(other, e)
		}
	}
	return moves, other
}

// waitPointer waits, at most liveWait, until the recorder has seen n moves of
// padmated. A move the hook let through stops the test at once: its cleanup then
// stops padmated and fences what it injected.
func waitPointer(t *testing.T, rec *injecttest.Recorder, pid uint32, n int) {
	t.Helper()
	deadline := time.Now().Add(liveWait)
	for {
		got, _ := padmatedPointer(rec, pid)
		for _, e := range got {
			if !e.Swallowed {
				t.Fatalf("a move of padmated got past the hook: %+v; stopping padmated", e)
			}
		}
		if len(got) >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the mouse hook saw %d moves of padmated after %v, want %d", len(got), liveWait, n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// pointerFence sends canaries to the mouse hook, each with a nonce of its own.
type pointerFence struct {
	rec  *injecttest.Recorder
	pid  uint32
	next uint32 // the nonce of the next canary
}

// fence sends a mouse canary, waits until the mouse hook has seen both its moves
// and swallowed everything so far, and returns how many moves of padmated the
// hook saw before it: all that were injected before the canary was.
func (f *pointerFence) fence(t *testing.T) int {
	t.Helper()
	nonce := f.next
	f.next++
	injecttest.SendMouseCanary(t, nonce)
	_, last, ok := f.rec.WaitMouseFence(nonce, injecttest.CanaryTimeout)
	if !ok {
		t.Fatalf("the mouse hook did not see the canary with the nonce %#x within %v", nonce, injecttest.CanaryTimeout)
	}
	if err := f.rec.WaitOwn(0, 0); err != nil {
		t.Fatalf("an injected event got past the hook: %v", err)
	}
	moves, _ := padmatedPointer(f.rec, f.pid)
	n := 0
	for _, e := range moves {
		if e.Seq < last.Seq {
			n++
		}
	}
	return n
}

// expectStill waits until padmated has sent no move for pointerQuiet, then
// fences twice with pointerQuiet between the canaries, and fails the test unless
// the hook saw the same number of moves before both: padmated sent none while the
// second canary was on its way, and why names the reason it should not have. It
// returns how many moves the hook has seen.
func expectStill(t *testing.T, rec *injecttest.Recorder, f *pointerFence, why string) int {
	t.Helper()
	// Until the moves of the last ticks are in.
	last, changed := -1, time.Now()
	deadline := time.Now().Add(liveWait)
	for {
		moves, _ := padmatedPointer(rec, f.pid)
		if n := len(moves); n != last {
			last, changed = n, time.Now()
		} else if time.Since(changed) >= pointerQuiet/2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("padmated still moved the pointer %v after %s (%d moves)", liveWait, why, len(moves))
		}
		time.Sleep(2 * time.Millisecond)
	}
	before := f.fence(t)
	time.Sleep(pointerQuiet)
	after := f.fence(t)
	if after != before {
		t.Errorf("padmated moved the pointer %d times in the %v after %s, want none", after-before, pointerQuiet, why)
	}
	return after
}

// stopFencedPointer is stopFenced for a test with the mouse hook: it stops live
// mode, when tl is not nil, and fences what it injected with a canary of each
// hook, sent after it has stopped. Once both hooks have seen theirs, they have
// seen everything before, and they may go. It returns the exit code. When live
// mode does not stop, the process exits with the hooks in place (stopLive).
func stopFencedPointer(t *testing.T, rec *injecttest.Recorder, f *pointerFence, tl *testLive) (code int) {
	t.Helper()
	code = stopLive(t, tl)
	_, keyBefore := ownKeys(rec, f.pid)
	injecttest.SendCanary(t, f.pid)
	f.fence(t)
	deadline := time.Now().Add(injecttest.CanaryTimeout)
	for {
		if _, keyNow := ownKeys(rec, f.pid); keyNow > keyBefore {
			return code
		}
		if time.Now().After(deadline) {
			t.Fatalf("the keyboard hook did not see the canary sent after the stop within %v", injecttest.CanaryTimeout)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
