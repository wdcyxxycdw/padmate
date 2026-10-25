package output_test

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// The pointer of ADR-0062, section 5: Move and Jump are not keys. They hold
// nothing, are never released, and go through the Arbiter in order with the key
// events.

func (s tsim) move(o output.Owner, dx, dy int32) {
	s.t.Helper()
	if err := s.Arb.Move(o, dx, dy); err != nil {
		s.t.Fatalf("Move(%v, %d, %d): %v", o, dx, dy, err)
	}
}

func TestMoveAndJump(t *testing.T) {
	s := newSim(t)
	s.move(ownerA, 5, -3)
	s.ExpectOutput("move=+5,-3@0")
	s.Advance(8 * ms)
	s.move(ownerA, -100, 0)
	s.move(ownerA, 0, 7)
	s.ExpectOutput("move=-100,+0@8 move=+0,+7@8")
	if err := s.Arb.Jump(ownerA, 32768, 0); err != nil {
		t.Fatal(err)
	}
	// A jump outside the desktop is clamped to its edges.
	if err := s.Arb.Jump(ownerA, -5, 70000); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("jump=32768,0@8 jump=0,65535@8")
	if got := s.Out.Calls(); got != 5 {
		t.Errorf("%d Send calls, want one for each of the 5 commands", got)
	}
	// Nothing is held: no ownership, no ledger, no release.
	if held := s.Arb.Held(); len(held) != 0 {
		t.Errorf("Held() = %v after moves, want none", held)
	}
	if st := s.Arb.Stats(); st.PendingReleases != 0 || st.Blocked != 0 {
		t.Errorf("stats %+v, want no pending release", st)
	}
	// ReleaseAll has nothing to let go of and sends nothing.
	s.ClearOutput()
	if err := s.Arb.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("")
}

func TestMoveZeroAndInvalidOwner(t *testing.T) {
	s := newSim(t)
	s.move(ownerA, 0, 0)
	s.ExpectOutput("")
	if s.Out.Calls() != 0 {
		t.Errorf("a move of 0, 0 made %d Send calls, want none", s.Out.Calls())
	}
	if err := s.Arb.Move(output.Owner{}, 1, 1); err == nil || !strings.Contains(err.Error(), "invalid owner") {
		t.Errorf("Move with the zero Owner = %v, want an invalid owner error", err)
	}
	if err := s.Arb.Move(output.Owner{}, 0, 0); err == nil {
		t.Error("Move(zero Owner, 0, 0) = nil, want an invalid owner error")
	}
	if err := s.Arb.Jump(output.Owner{}, 1, 1); err == nil {
		t.Error("Jump with the zero Owner = nil, want an invalid owner error")
	}
	s.ExpectOutput("")
}

// While the gate is closed (pause, lock, sleep) a move is dropped like a
// press, and counts in Blocked.
func TestMoveBlocked(t *testing.T) {
	s := newSim(t)
	s.Arb.SetBlocked(true)
	s.move(ownerA, 4, 4)
	if err := s.Arb.Jump(ownerA, 1, 1); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("")
	if st := s.Arb.Stats(); st.Blocked != 2 {
		t.Errorf("Blocked = %d, want 2", st.Blocked)
	}
	s.Arb.SetBlocked(false)
	s.move(ownerA, 4, 4)
	s.ExpectOutput("move=+4,+4@0")
}

// Moves come between the events of the keys in the order they were commanded:
// a drag is the button down, the moves, the button up.
func TestMoveOrderedWithKeys(t *testing.T) {
	s := newSim(t)
	s.down(ownerA, "MouseLeft")
	s.move(ownerA, 3, 0)
	s.move(ownerA, 3, 1)
	s.up(ownerA, "MouseLeft")
	s.ExpectOutput("+MouseLeft@0 move=+3,+0@0 move=+3,+1@0 -MouseLeft@0")
}

// A move is not a key: a lone Alt released after moves still gets the mask key,
// as after another mouse event (a move is no "other key pressed").
func TestMoveNotAnotherKeyForMask(t *testing.T) {
	s := newSim(t, simtest.WithMenuMask())
	s.down(ownerA, "LAlt")
	s.move(ownerA, 10, 10)
	s.up(ownerA, "LAlt")
	got := s.Output()
	if !strings.Contains(got, "vk:0xE8") {
		t.Errorf("a lone LAlt released after a move was not masked: %s", got)
	}
	// With a key between, the Alt is not alone, and the mask stays out.
	s = newSim(t, simtest.WithMenuMask())
	s.down(ownerA, "LAlt")
	s.tap(ownerA, "Z", 0)
	s.move(ownerA, 1, 1)
	s.up(ownerA, "LAlt")
	if got := s.Output(); strings.Contains(got, "vk:0xE8") {
		t.Errorf("LAlt+Z was masked: %s", got)
	}
}

// With an event gap a move waits behind the events already queued, in order,
// but it never waits for the gap itself and does not start one.
func TestMoveWithEventGap(t *testing.T) {
	const gap = 20 * ms
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{EventGap: gap}))
	// The queue is empty: a move goes out at once, whatever the gap, and a key
	// event right after it is not delayed by it.
	s.move(ownerA, 5, 0)
	s.Advance(ms)
	s.tap(ownerA, "X", 0)
	s.ExpectOutput("move=+5,+0@0 +X@1")
	s.Advance(gap) // -X goes out
	s.ExpectOutput("-X@21")

	// With events queued, the move waits its turn behind them.
	s.tap(ownerA, "Y", 0) // +Y now (gap since -X is not over: at 41), -Y after it
	s.move(ownerA, 1, 0)
	s.move(ownerA, 2, 0) // joins the move before it
	s.move(ownerB, 0, 9) // another owner's: its own entry
	s.move(ownerB, 0, 1) // joins that
	s.ExpectOutput("")
	s.Advance(gap) // +Y at 41; -Y and the moves behind it wait for the gap
	s.ExpectOutput("+Y@41")
	s.Advance(gap) // -Y at 61, and the moves right after it, not a gap later
	s.ExpectOutput("-Y@61 move=+3,+0@61 move=+0,+10@61")
	// The moves did not start a gap of their own: a key tapped now goes at
	// once, the gap since -Y being over at 81.
	s.Advance(gap)
	s.tap(ownerA, "W", 0)
	s.ExpectOutput("+W@81")
	s.Settle()
	s.ExpectOutput("-W@101")
}

// The sum of two moves that cancel is not sent.
func TestMoveQueueCancels(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{EventGap: 20 * ms}))
	s.tap(ownerA, "X", 0) // +X now, -X queued
	s.move(ownerA, 4, -2)
	s.move(ownerA, -4, 2)
	s.Settle()
	s.ExpectOutput("+X@0 -X@20")
}

// A forced release of an owner drops the moves of that owner still waiting in
// the queue, and sends the other owners'. A dropped move needs no mask key.
func TestMoveDroppedByReleaseOwners(t *testing.T) {
	s := newSim(t, simtest.WithMenuMask(), simtest.WithArbiterOptions(output.ArbiterOptions{EventGap: 50 * ms}))
	s.tap(ownerB, "Z", 0) // +Z now, -Z queued
	s.move(ownerA, 7, 7)
	s.move(ownerB, 2, 2)
	s.ExpectOutput("+Z@0")
	err := s.Arb.ReleaseOwners(func(o output.Owner) bool { return o == ownerA })
	if err != nil {
		t.Fatal(err)
	}
	// B's -Z and move go out at once, in their order; A's move is gone.
	s.ExpectOutput("-Z@0 move=+2,+2@0")
	s.Settle()
	s.ExpectOutput("")
}

// ReleaseAll and the other forced releases take no key down for a move, and a
// Down chord's modifiers are not kept by a move.
func TestMoveDroppedBesideDroppedModifier(t *testing.T) {
	s := newSim(t, simtest.WithMenuMask(), simtest.WithArbiterOptions(output.ArbiterOptions{EventGap: 50 * ms}))
	s.down(ownerB, "Shift")  // +LShift now
	s.down(ownerA, "Ctrl+X") // queued behind the gap: +LCtrl +X
	s.move(ownerA, 1, 1)
	if err := s.Arb.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	s.Settle()
	// Nothing is stuck: the Recorder has no key down, the Sim checks at the end.
	if st := s.Arb.Stats(); st.PendingReleases != 0 {
		t.Errorf("stats %+v", st)
	}
}

// The Send errors of pointer events alone are reported once, then once a
// second: a desktop that refuses input would otherwise make 125 errors a
// second. Stats counts every failure.
func TestMoveErrorsAreRateLimited(t *testing.T) {
	s := newSim(t)
	for range 20 {
		s.Out.FailNext(0, errBlocked)
	}
	first := s.Arb.Move(ownerA, 1, 0)
	if first == nil || !errors.Is(first, errBlocked) {
		t.Fatalf("the first failing move = %v, want its error", first)
	}
	for i := range 10 {
		s.Advance(8 * ms)
		if err := s.Arb.Move(ownerA, 1, 0); err != nil {
			t.Fatalf("failing move %d within a second: %v, want nil", i+2, err)
		}
	}
	s.Advance(time.Second)
	if err := s.Arb.Move(ownerA, 1, 0); err == nil {
		t.Error("a failing move a second later returned nil, want its error")
	}
	if st := s.Arb.Stats(); st.SendErrors != 12 {
		t.Errorf("SendErrors = %d, want all 12 failures counted", st.SendErrors)
	}
	// A key that fails is always reported.
	s.Out.FailNext(0, errBlocked)
	if err := s.Arb.Tap(ownerA, keys.MustParse("Z"), 0); err == nil {
		t.Error("a failing key tap returned nil")
	}
	s.Out.Inject = nil
	// Nothing was injected for the failed moves and the tap, and no move is
	// retried: a failed tap press needs its release, which Settle sends.
	s.Settle()
	_ = s.Errors()
}

// A local dry run (the own window) sends nothing to the Sender, moves included.
func TestMoveLocalDryRun(t *testing.T) {
	s := newSim(t)
	if err := s.Arb.SetLocalDryRun(true); err != nil {
		t.Fatal(err)
	}
	s.move(ownerA, 5, 5)
	s.ExpectOutput("")
	if err := s.Arb.SetLocalDryRun(false); err != nil {
		t.Fatal(err)
	}
	s.move(ownerA, 5, 5)
	s.ExpectOutput("move=+5,+5@0")
}

// A move does not take part in the ledger of a Loop (nothing to release after a
// panic or at Stop), reaches the Sender, and the held-key log never has it.
func TestLoopMoves(t *testing.T) {
	path := heldPath(t)
	lt := startLoopWith(t, output.LoopOptions{HeldFile: path, KeyDown: func(output.KeyEvent) bool { return false }}, nil, nil)
	if res := lt.l.Swept(); res.Err != nil || res.Off {
		t.Fatalf("Swept() = %+v", res)
	}
	r := lt.l.Remote()
	lt.do(down(ownerA, "Ctrl"))
	if err := r.Move(ownerA, 3, -4); err != nil {
		t.Fatal(err)
	}
	if err := r.Jump(ownerA, 100, 200); err != nil {
		t.Fatal(err)
	}
	wantHeld(t, path, "LCtrl")
	if err := lt.stop(); err != nil {
		t.Fatal(err)
	}
	wantNoHeld(t, path)
	if got, want := lt.rec.Trace(), "+LCtrl@0 move=+3,-4@0 jump=100,200@0 -LCtrl@0"; got != want {
		t.Errorf("output:\n got %s\nwant %s", got, want)
	}
}

// After Stop the Loop refuses moves like any command.
func TestRemoteMoveAfterStop(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	r := lt.l.Remote()
	if err := lt.stop(); err != nil {
		t.Fatal(err)
	}
	if err := r.Move(ownerA, 1, 1); !errors.Is(err, output.ErrLoopClosed) {
		t.Errorf("Move after Stop = %v, want ErrLoopClosed", err)
	}
}

// The gate of a Loop drops moves like presses.
func TestLoopBlockDropsMoves(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	r := lt.l.Remote()
	lt.l.Block(true)
	if err := r.Move(ownerA, 1, 1); err != nil {
		t.Fatal(err)
	}
	lt.sync()
	lt.l.Block(false)
	if err := r.Move(ownerA, 2, 2); err != nil {
		t.Fatal(err)
	}
	lt.sync()
	if got := lt.rec.Trace(); got != "move=+2,+2@0" {
		t.Errorf("output: %s, want only the move after the gate opened", got)
	}
}

// A pointer event has no bit in the held-key log, and the log never names one.
func TestHeldLogNeverHasPointerEvents(t *testing.T) {
	for _, e := range []output.KeyEvent{
		{Mouse: keys.MouseMove, Dx: 5, Dy: 5},
		{Mouse: keys.MouseJump, Dx: 5, Dy: 5},
	} {
		b := output.EncodeHeld(e, press(t, "LCtrl"))
		ids, err := output.DecodeHeld(b)
		if err != nil || len(ids) != 1 || ids[0] != press(t, "LCtrl").ID() {
			t.Errorf("a log with %v and LCtrl has %v, %v; want only LCtrl", e, ids, err)
		}
	}
}

var _ = clock.Time(0)

// Random commands of keys and moves, with and without an event gap: every move
// commanded comes out (the sum is what was commanded) when nothing releases or
// blocks, never more when owners are released, and no key is left held. The
// moves between two key commands never pass them.
func TestMoveRandomCommands(t *testing.T) {
	specs := []string{"Z", "Ctrl+Z", "MouseLeft", "Shift+MouseRight", "WheelUp", "Alt"}
	owners := []output.Owner{ownerA, ownerB, ownerS}
	for seed := range 80 {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(uint64(seed), 11))
			paced := seed >= 30
			releases := seed%2 == 1
			var s tsim
			if paced {
				s = newSim(t, simtest.WithMenuMask(), simtest.WithArbiterOptions(output.ArbiterOptions{
					MinPress: time.Duration(rng.IntN(30)) * ms, MinGap: time.Duration(rng.IntN(60)) * ms,
					EventGap: time.Duration(1+rng.IntN(40)) * ms,
				}))
			} else {
				s = newSim(t, simtest.WithMenuMask(), withTiming(time.Duration(rng.IntN(30))*ms, time.Duration(rng.IntN(60))*ms))
			}
			wantX, wantY := 0, 0
			for range 250 {
				o, spec := owners[rng.IntN(len(owners))], specs[rng.IntN(len(specs))]
				switch rng.IntN(10) {
				case 0:
					s.down(o, spec)
				case 1:
					s.up(o, spec)
				case 2:
					s.tap(o, spec, time.Duration(rng.IntN(3))*15*ms)
				case 3:
					s.Advance(time.Duration(rng.IntN(25)) * ms)
				case 4:
					if releases && rng.IntN(4) == 0 {
						if err := s.Arb.ReleaseOwner(o); err != nil {
							t.Fatal(err)
						}
					}
				default:
					dx, dy := int32(rng.IntN(21)-10), int32(rng.IntN(21)-10)
					s.move(o, dx, dy)
					wantX += int(dx)
					wantY += int(dy)
				}
			}
			// What waits in the queue goes out with time; the forced release of
			// ReleaseAll would drop the moves of the owners it releases.
			s.Settle()
			if err := s.Arb.ReleaseAll(); err != nil {
				t.Fatal(err)
			}
			s.Settle()
			gotX, gotY := 0, 0
			for _, r := range s.Out.Records() {
				if r.Injected && r.Event.Mouse == keys.MouseMove {
					gotX += int(r.Event.Dx)
					gotY += int(r.Event.Dy)
				}
			}
			if !releases && (gotX != wantX || gotY != wantY) {
				t.Errorf("the moves add up to %d,%d, commanded %d,%d", gotX, gotY, wantX, wantY)
			}
			if releases && (gotX == 0 && gotY == 0 && (wantX != 0 || wantY != 0) && !paced) {
				t.Errorf("no move came out of %d,%d commanded without an event gap", wantX, wantY)
			}
		})
	}
}

// A command resends the releases that failed, but a stick commands 125 times a
// second: while a release waits for its RetryInterval the moves do not try it
// again or report it (Advance does, at the interval), and the key is released.
func TestMoveDoesNotRetryReleasesFasterThanTheInterval(t *testing.T) {
	s := newSim(t, withRetry(100*ms))
	s.down(ownerA, "Z")
	s.Out.FailNext(0, errBlocked) // the release
	s.Out.FailNext(0, errBlocked) // its immediate retry
	if err := s.Arb.Up(ownerA, keys.MustParse("Z")); err == nil {
		t.Fatal("the failing release returned no error")
	}
	if st := s.Arb.Stats(); st.PendingReleases != 1 {
		t.Fatalf("stats %+v, want one pending release", st)
	}
	calls := s.Out.Calls()
	for i := range 11 {
		s.Advance(8 * ms)
		if err := s.Arb.Move(ownerA, 1, 0); err != nil {
			t.Fatalf("move %d: %v, want no error: the release waits for its interval", i, err)
		}
	}
	if got := s.Out.Calls() - calls; got != 11 {
		t.Errorf("%d Send calls for 11 moves, want 11: none for the release", got)
	}
	// The interval is over: Advance resends the release.
	s.Advance(20 * ms)
	if err := s.Errors(); len(err) != 0 {
		t.Errorf("errors %v", err)
	}
	if st := s.Arb.Stats(); st.PendingReleases != 0 {
		t.Errorf("stats %+v, want the release resent", st)
	}
	// With the gate closed a move is dropped and does not retry either.
	s.Arb.SetBlocked(true)
	s.move(ownerA, 1, 0)
	s.Arb.SetBlocked(false)
}
