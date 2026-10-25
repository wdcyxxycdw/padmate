package output_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/injecttest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

// The tests in this file inject the pointer moves of a stick (ADR-0062, section
// 5). A move is not held, but a move that got past the hook would take the
// cursor of the machine the test runs on somewhere else, so these tests are
// stricter than the wheel ones in inject_windows_test.go:
//
//   - The moves are only ever injected in batches that add up to no distance
//     along either axis, the moves of a stick out and the moves back, in one
//     SendInput call, and that never take the cursor more than 20 pixels from
//     where it started (injecttest.InjectFencedMoves, which refuses any other
//     batch before it sends anything). Pairing limits the damage if a batch got
//     past the hook; it does not promise that the cursor would come back, since
//     the system scales a relative move (pointer speed, "enhance pointer
//     precision") and clamps it at the edge of the screen, so the size of the
//     batch is what bounds it. An unpaired move, and an absolute jump, which has
//     nothing to pair with, are never injected.
//   - The mouse hook is proven live by a mouse canary before the first batch,
//     and every batch has a canary behind it. The hook reports a move of the
//     batch and a move of the canary alike (WM_MOUSEMOVE, injected, a nonce), so
//     the moves and the canary behind them carry nonces of their own, and each
//     nonce serves one batch.
//   - The keyboard hook sees nothing of it.
//
// What the hook reports of a move is injecttest.PointerProblems's: WM_MOUSEMOVE,
// LLMHF_INJECTED, the nonce (a mouse hook gets no tag), swallowed. Where it says
// the pointer is (the point of the event) is not checked, since how the system
// reports a relative move that a hook swallows, and how it scales one with the
// pointer speed and "enhance pointer precision", has not been measured; the test
// logs the points so that `task test:inject` shows them.

// pointerBatch is a batch of relative moves that adds up to no distance.
type pointerBatch struct {
	name  string
	moves []output.KeyEvent
}

// steps returns n moves of (dx, dy).
func steps(n int, dx, dy int32) []output.KeyEvent {
	out := make([]output.KeyEvent, n)
	for i := range out {
		out[i] = injecttest.PointerMove(dx, dy)
	}
	return out
}

// cycles returns n times the moves evs, one after the other.
func cycles(n int, evs ...output.KeyEvent) []output.KeyEvent {
	var out []output.KeyEvent
	for range n {
		out = append(out, evs...)
	}
	return out
}

// pointerBatches returns the batches the injection test sends, each the moves
// out and the moves back, and none of them takes the cursor more than 20 pixels
// from where it started (injecttest.MaxPointerExcursion): the moves of the mouse
// canary, one along each axis, a diagonal, the farthest the limit allows, a
// path back by another way, ten small steps of a stick out and back, and a burst
// of a hundred and twenty varied moves that wanders a few pixels and comes back,
// to count and order many events.
func pointerBatches() []pointerBatch {
	mv := injecttest.PointerMove
	return []pointerBatch{
		{"a pair along x", []output.KeyEvent{mv(1, 0), mv(-1, 0)}},
		{"a pair along y", []output.KeyEvent{mv(0, 1), mv(0, -1)}},
		{"a diagonal pair", []output.KeyEvent{mv(5, 3), mv(-5, -3)}},
		{"the farthest the limit allows", []output.KeyEvent{mv(20, -20), mv(-20, 20)}},
		{"a path back by another way", []output.KeyEvent{mv(10, 0), mv(0, 10), mv(-10, -10)}},
		{"ten steps of a stick out and back", slices.Concat(steps(10, 2, -1), steps(10, -2, 1))},
		{"a burst", cycles(20, mv(3, 1), mv(-1, 2), mv(2, -2), mv(-3, -1), mv(1, -2), mv(-2, 2))},
	}
}

// pointerSeenProblems returns what is wrong with what the mouse hook saw of
// the batch of moves want injected with nonce: exactly one event for each, as
// injecttest.PointerProblems has it (WM_MOUSEMOVE, LLMHF_INJECTED, the nonce,
// swallowed), in the order the hook thread saw them (Seq increases), with a
// stamp that is set and does not go back.
func pointerSeenProblems(nonce uint32, want []output.KeyEvent, got []winhook.MouseEvent) []string {
	if len(got) != len(want) {
		return []string{fmt.Sprintf("the mouse hook saw %d moves, want %d", len(got), len(want))}
	}
	var problems []string
	var prevSeq, prevStamp uint64
	for i, e := range got {
		for _, p := range injecttest.PointerProblems(e, nonce) {
			problems = append(problems, fmt.Sprintf("move %d (%v): %s", i, want[i], p))
		}
		if e.Seq <= prevSeq {
			problems = append(problems, fmt.Sprintf("move %d has Seq %d, not after %d", i, e.Seq, prevSeq))
		}
		prevSeq = e.Seq
		if stamp := uint64(e.Stamp); stamp == 0 || stamp < prevStamp {
			problems = append(problems, fmt.Sprintf("move %d has stamp %d after %d", i, stamp, prevStamp))
		} else {
			prevStamp = stamp
		}
	}
	return problems
}

// pointsOf describes the points the hook gave for the first and last moves of a
// batch, next to the moves, for the log.
func pointsOf(want []output.KeyEvent, got []winhook.MouseEvent) string {
	var parts []string
	for i, e := range got {
		if i >= len(want) || i >= 3 && i < len(got)-2 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%v -> (%d, %d)", want[i], e.X, e.Y))
	}
	return strings.Join(parts, "; ")
}

// TestInjectPointerMoves (ADR-0062, section 5) injects batches of relative
// pointer moves that add up to no distance and checks what the mouse hook sees:
// one WM_MOUSEMOVE for each move, LLMHF_INJECTED, the nonce of the batch, that
// the hook swallowed it, and in the order of the batch. The mouse hook is proven
// live by a mouse canary before the first batch and every batch has one behind
// it, with a nonce of its own (injecttest.InjectFencedMoves). The keyboard hook
// sees nothing of it. The points of the events are logged, since how the system
// reports them is unmeasured until this test has run.
func TestInjectPointerMoves(t *testing.T) {
	injecttest.Require(t)
	base := injecttest.NonceBase()
	rec := injecttest.Start(t, base)
	rec.Canary(t, base)
	rec.MouseCanary(t, base+1)
	keyboardSeen := len(rec.Own())

	nonce := base + 2
	for _, b := range pointerBatches() {
		moveNonce, fenceNonce := nonce, nonce+1
		nonce += 2
		got := rec.InjectFencedMoves(t, moveNonce, fenceNonce, b.moves)
		t.Logf("%s: %d moves: %s", b.name, len(got), pointsOf(b.moves, got))
		if problems := pointerSeenProblems(moveNonce, b.moves, got); len(problems) > 0 {
			t.Errorf("%s: %s", b.name, strings.Join(problems, "; "))
		}
	}
	// None of the batches had a keyboard event, so the keyboard hook saw nothing.
	if n := len(rec.Own()); n != keyboardSeen {
		t.Errorf("the keyboard hook saw %d owned events during the pointer batches, want none", n-keyboardSeen)
	}
}

// The tests below inject nothing and install no hook: they run the checks above
// on events made up as a hook would report them.

// TestPointerBatches: every batch the injection test sends is one
// injecttest.InjectFencedMoves accepts, and they are different from each other.
func TestPointerBatches(t *testing.T) {
	seen := map[string]bool{}
	for _, b := range pointerBatches() {
		if why := injecttest.UnpairedMoves(b.moves); why != "" {
			t.Errorf("%s: %s", b.name, why)
		}
		if seen[b.name] {
			t.Errorf("%s is twice in the batches", b.name)
		}
		seen[b.name] = true
		for i, e := range b.moves {
			if e.Mouse != keys.MouseMove || e.Silent() || e.Up {
				t.Errorf("%s: move %d = %+v, want a pointer move that injects something", b.name, i, e)
			}
		}
	}
	if len(seen) < 5 {
		t.Errorf("%d batches, want at least the pairs along x, y and the diagonal, a long way and a burst", len(seen))
	}
}

// seenMove is what the mouse hook sees of a move injected with nonce, if it
// works as WinUser.h says; later events have later stamps.
func seenMove(seq uint64, nonce uint32) winhook.MouseEvent {
	return winhook.MouseEvent{
		X: 640 + int32(seq), Y: 360, Msg: winhook.WMMouseMove, Flags: winhook.MouseFlagInjected,
		Extra: uintptr(nonce), Swallowed: true, Stamp: latency.Stamp(1000 + 10*seq), Seq: seq,
	}
}

func TestPointerSeenProblems(t *testing.T) {
	const nonce = 0x4D4F0003
	want := []output.KeyEvent{injecttest.PointerMove(5, 3), injecttest.PointerMove(-5, -3)}
	good := func() []winhook.MouseEvent { return []winhook.MouseEvent{seenMove(1, nonce), seenMove(2, nonce)} }
	with := func(i int, f func(e *winhook.MouseEvent)) []winhook.MouseEvent {
		got := good()
		f(&got[i])
		return got
	}
	for _, tt := range []struct {
		name string
		got  []winhook.MouseEvent
		want string // a part of the problems, or "" for none
	}{
		{"all as it should be", good(), ""},
		{"nothing", nil, "saw 0 moves, want 2"},
		{"a move missing", good()[:1], "saw 1 moves, want 2"},
		{"a move too many", append(good(), seenMove(3, nonce)), "saw 3 moves, want 2"},
		{"not swallowed", with(1, func(e *winhook.MouseEvent) { e.Swallowed = false }), "move 1 (move -5,-3): not swallowed"},
		{"another nonce", with(0, func(e *winhook.MouseEvent) { e.Extra++ }), "move 0 (move +5,+3): extra"},
		{"a notch for a move", with(0, func(e *winhook.MouseEvent) { e.Msg = winhook.WMMouseWheel }), "message WM_MOUSEWHEEL"},
		{"not injected", with(0, func(e *winhook.MouseEvent) { e.Flags = 0 }), "want LLMHF_INJECTED only"},
		{"Seq goes back", with(1, func(e *winhook.MouseEvent) { e.Seq = 1 }), "move 1 has Seq 1, not after 1"},
		{"no stamp", with(0, func(e *winhook.MouseEvent) { e.Stamp = 0 }), "move 0 has stamp 0"},
		{"the stamp goes back", with(1, func(e *winhook.MouseEvent) { e.Stamp = 1 }), "move 1 has stamp 1 after"},
	} {
		problems := strings.Join(pointerSeenProblems(nonce, want, tt.got), "; ")
		switch {
		case tt.want == "" && problems != "":
			t.Errorf("%s: problems %q, want none", tt.name, problems)
		case tt.want != "" && !strings.Contains(problems, tt.want):
			t.Errorf("%s: problems %q, want one with %q", tt.name, problems, tt.want)
		}
	}
}

func TestPointsOf(t *testing.T) {
	want := steps(8, 1, 0)
	var got []winhook.MouseEvent
	for i := range want {
		got = append(got, seenMove(uint64(i+1), 1))
	}
	// The first three and the last two, whatever the length of the batch.
	if s := pointsOf(want, got); strings.Count(s, "->") != 5 {
		t.Errorf("pointsOf = %q, want 5 points", s)
	}
	if s := pointsOf(want[:2], got[:2]); strings.Count(s, "->") != 2 {
		t.Errorf("pointsOf of two moves = %q, want 2 points", s)
	}
	if s := pointsOf(nil, nil); s != "" {
		t.Errorf("pointsOf of nothing = %q", s)
	}
}
