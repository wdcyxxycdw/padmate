package injecttest

import (
	"fmt"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

// The pointer moves (ADR-0062, section 5). A move is not held, so a lost move
// cannot leave anything down; what a leak costs is a cursor that is somewhere
// else. So a test injects relative moves only in batches that add up to no
// distance at all, the way the mouse canary is a move of +1 and one of -1, and
// the hook swallows every one of them. Pairing is there to limit the damage if a
// move leaked, not to guarantee that the cursor ends where it began: the system
// scales a relative move with the pointer speed and, with "enhance pointer
// precision", with how fast the moves come, so the way back need not undo the
// way out, and at the edge of the screen part of a move is lost to the clamp. So
// a batch is also kept small, a few pixels from where the cursor started (the
// limits below), and a leaked batch would leave the cursor at worst that far
// from where it was. An absolute jump has nothing to be paired with and is never
// injected by a test here.
//
// A move arrives at the mouse hook as WM_MOUSEMOVE, injected, with the nonce
// (the same as the mouse canary does), and the canary is such a move too. The
// hook cannot tell a move of the batch from one of the canary, so the batch and
// its canary never share a nonce: InjectFencedMoves injects the moves with one
// nonce and then the canary, behind them, with another. SendInput inserts the
// events of one thread in order, so the hook sees the canary after the moves,
// and a canary that arrived says the moves did too.

const (
	// MaxPointerMoves is the most moves one InjectFencedMoves batch has. The
	// hook's channel holds 1024 mouse events.
	MaxPointerMoves = 256
	// MaxPointerStep is the farthest, along either axis, that one move of a
	// batch goes.
	MaxPointerStep = 20
	// MaxPointerExcursion is the farthest, along either axis, that the moves of
	// a batch have taken the cursor from where it started at any point of the
	// batch, before the system scales them. It is small on purpose: the pairing
	// of a batch does not promise that the cursor comes back (see above).
	MaxPointerExcursion = 20
)

// PointerMove returns the event of a relative pointer move of dx pixels right
// and dy down, as the Arbiter's Move sends it: no key, no scan code, only the
// distance.
func PointerMove(dx, dy int32) output.KeyEvent {
	return output.KeyEvent{Mouse: keys.MouseMove, Dx: dx, Dy: dy}
}

// UnpairedMoves returns what is wrong with moves as a batch of
// InjectFencedMoves, or "" when nothing is: it must be pointer moves of the
// plain form PointerMove makes, each of some distance (a move of no distance
// never reaches a hook, measured 2026-10-19, and would not be counted), none
// farther than MaxPointerStep, the cursor never farther than MaxPointerExcursion
// from where it began, and their sum zero along both axes. An absolute jump is
// never one of them.
func UnpairedMoves(moves []output.KeyEvent) string {
	if len(moves) == 0 {
		return "there are no moves"
	}
	if len(moves) > MaxPointerMoves {
		return fmt.Sprintf("%d moves, at most %d", len(moves), MaxPointerMoves)
	}
	var x, y int64
	for i, e := range moves {
		switch {
		case e.Mouse == keys.MouseJump:
			return fmt.Sprintf("move %d is an absolute jump, which has nothing to pair with", i)
		case e.Mouse != keys.MouseMove || e != PointerMove(e.Dx, e.Dy):
			return fmt.Sprintf("event %d (%v) is not a plain relative pointer move", i, e)
		case e.Dx == 0 && e.Dy == 0:
			return fmt.Sprintf("move %d is of no distance, which never reaches a hook", i)
		case abs64(int64(e.Dx)) > MaxPointerStep || abs64(int64(e.Dy)) > MaxPointerStep:
			return fmt.Sprintf("move %d (%+d, %+d) goes farther than %d along an axis", i, e.Dx, e.Dy, MaxPointerStep)
		}
		x += int64(e.Dx)
		y += int64(e.Dy)
		if abs64(x) > MaxPointerExcursion || abs64(y) > MaxPointerExcursion {
			return fmt.Sprintf("after move %d the cursor would be (%+d, %+d) from where it began, farther than %d along an axis", i, x, y, MaxPointerExcursion)
		}
	}
	if x != 0 || y != 0 {
		return fmt.Sprintf("the moves add up to (%+d, %+d), not (0, 0)", x, y)
	}
	return ""
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// PointerProblems returns what is wrong with e as the mouse hook's report of a
// pointer move that a test or padmated injected with nonce, or nil when nothing
// is: it must be WM_MOUSEMOVE, injected and not LLMHF_LOWER_IL_INJECTED, carry the
// nonce (a mouse hook gets no tag; MayBePadmateMouse), and have been swallowed.
// Where the hook says the pointer would go (X, Y) is not checked: it is
// unmeasured how the system reports a relative move that a hook swallows, and
// how it scales one (the pointer speed, "enhance pointer precision").
func PointerProblems(e winhook.MouseEvent, nonce uint32) []string {
	var problems []string
	if e.Msg != winhook.WMMouseMove {
		problems = append(problems, fmt.Sprintf("message %s (mouseData %#x), want %s", winhook.MouseMsgName(e.Msg), e.MouseData, winhook.MouseMsgName(winhook.WMMouseMove)))
	}
	if !e.Injected() || e.LowerILInjected() {
		problems = append(problems, fmt.Sprintf("flags 0x%02X, want LLMHF_INJECTED only", e.Flags))
	}
	if !output.MayBePadmateMouse(e.Extra) || output.Nonce(e.Extra) != nonce {
		problems = append(problems, fmt.Sprintf("extra %#x, want the nonce %#x", uint64(e.Extra), nonce))
	}
	if !e.Swallowed {
		problems = append(problems, "not swallowed")
	}
	return problems
}

// InjectFencedMoves injects the relative pointer moves with nonce, in one
// SendInput call, then the mouse canary with fenceNonce, and returns the mouse
// events with nonce that the hook saw before the canary: exactly the moves of
// the batch, in order. The moves must add up to no distance (UnpairedMoves),
// and the two nonces must differ and be the recorder's own: the hook reports a
// move of the batch and a move of the canary alike, and tells them apart by
// nonce, so each of them serves one batch. InjectFencedMixed refuses pointer
// moves for the same reason.
//
// Call MouseCanary, or an earlier InjectFencedMoves, first. The moves go out
// before their own canary proves the hook live, so the canary of the batch
// before is what proves it for this one. If SendInput injects part of the
// batch, the rest is injected so that the moves still add up to nothing.
//
// The test stops, and injects nothing else, if the batch is refused, if
// SendInput does not inject the whole batch, if the canary does not arrive
// within CanaryTimeout, if the hook let a move or a move of the canary
// through, or if it saw a different number of moves than were injected.
func (r *Recorder) InjectFencedMoves(t testing.TB, nonce, fenceNonce uint32, moves []output.KeyEvent) []winhook.MouseEvent {
	t.Helper()
	if !r.owns(nonce) || !r.owns(fenceNonce) {
		t.Fatalf("nonces %#x and %#x are not both owned by the recorder", nonce, fenceNonce)
	}
	if nonce == fenceNonce {
		t.Fatalf("the moves and their canary share the nonce %#x: the hook could not tell them apart; injecting nothing", nonce)
	}
	if why := UnpairedMoves(moves); why != "" {
		t.Fatalf("a batch of pointer moves must add up to no distance and be small: %s; injecting nothing", why)
	}
	if !r.MouseLive() {
		t.Fatalf("a batch with a mouse event, and no mouse canary has proven the mouse hook live; call MouseCanary first")
	}
	s := output.SendInput{Nonce: nonce}
	if n, err := s.Send(moves); n < len(moves) {
		if n > 0 {
			// Finish the batch, so that it still adds up to nothing.
			if m, err2 := s.Send(moves[n:]); m < len(moves)-n {
				t.Fatalf("SendInput injected %d of %d moves (%v), and %d of the other %d (%v): the cursor may be moved",
					n, len(moves), err, m, len(moves)-n, err2)
			}
		}
		t.Fatalf("SendInput injected %d of %d moves: %v", n, len(moves), err)
	}
	SendMouseCanary(t, fenceNonce)
	_, canaries, ok := r.waitFences(fenceNonce, false, true, CanaryTimeout)
	if !ok {
		t.Fatalf("the mouse hook did not see the canary behind the moves within %v; injecting nothing else", CanaryTimeout)
	}
	var got []winhook.MouseEvent
	for _, e := range r.OwnMouse() {
		if output.Nonce(e.Extra) == nonce {
			got = append(got, e)
		}
	}
	if why := movesProblem(got, canaries, len(moves)); why != "" {
		t.Fatalf("%s; injecting nothing else", why)
	}
	return got
}

// movesProblem returns what is wrong with what the mouse hook saw of a batch
// of want pointer moves, got, and of the canary behind them, or "" when nothing
// is: both moves of the canary and every move must have been swallowed, the
// moves must all have come before the canary, and there must be as many as were
// injected.
func movesProblem(got []winhook.MouseEvent, canaries []Seen, want int) string {
	if len(canaries) < MouseCanaryLen {
		return fmt.Sprintf("the mouse hook saw %d moves of the canary behind the moves, want %d", len(canaries), MouseCanaryLen)
	}
	for _, c := range canaries {
		if !c.Swallowed() {
			return fmt.Sprintf("a move of the canary got past the hook: %v", c)
		}
	}
	for _, e := range got {
		switch {
		case !e.Swallowed:
			return fmt.Sprintf("a move got past the hook: %v", Seen{Mouse: &e})
		case e.Seq > canaries[0].Seq():
			return fmt.Sprintf("a move was seen after the canary behind it: %v", Seen{Mouse: &e})
		}
	}
	if len(got) != want {
		return fmt.Sprintf("the mouse hook saw %d moves before the canary, want %d", len(got), want)
	}
	return ""
}
