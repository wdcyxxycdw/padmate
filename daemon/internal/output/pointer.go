package output

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// The pointer (ADR-0062, section 5): a stick in mode "mouse" moves the pointer
// through Move, and the "jump" action puts it somewhere through Jump. Neither is
// a key. They have no owner's hold, no release and no minimum press, are never
// in the ledger or the held-key log, and a ReleaseAll has nothing to let go of;
// they are only ordered with the key events of the Arbiter, and dropped by the
// same things that drop a command: the closed gate, and a forced release of
// their owner while they wait in the event gap's queue.

// MaxJump is the largest coordinate of Jump: MOUSEEVENTF_ABSOLUTE puts the
// pointer at 0 to 65535 across the virtual desktop.
const MaxJump = 65535

// pointerErrInterval is how often Send errors of pointer events alone are
// reported. A stick that moves the pointer sends 125 times a second, and a
// desktop that refuses input (the secure desktop, UIPI) would make each of them
// an error; the first is reported, then one a second, and Stats counts them all.
const pointerErrInterval = time.Second

// Move moves the pointer by dx pixels right and dy down, relative to where it
// is (MOUSEEVENTF_MOVE; the system scales it with the pointer speed and, when it
// is on, the acceleration). A move of 0, 0 does nothing. The move goes out at
// once, in its own Send, unless events wait in the event gap's queue (OUT-17):
// then it waits behind them, in order, and joins the move queued last when that
// is the same owner's, so that the queue does not grow with a stick. A move
// never waits for the event gap itself and does not start one, which would
// throttle the stick. While the gate is closed Move does nothing. A move that
// the Sender does not inject is not retried.
func (a *Arbiter) Move(o Owner, dx, dy int32) error {
	if dx == 0 && dy == 0 {
		return a.pointerCheck(o)
	}
	return a.pointer(o, KeyEvent{Mouse: keys.MouseMove, Dx: dx, Dy: dy})
}

// Jump puts the pointer at (x, y), each 0 to 65535 across the virtual desktop
// (MOUSEEVENTF_ABSOLUTE | MOUSEEVENTF_VIRTUALDESK); a value outside is clamped.
// Like Move it has no owner's state and is ordered with the key events.
func (a *Arbiter) Jump(o Owner, x, y int32) error {
	return a.pointer(o, KeyEvent{Mouse: keys.MouseJump, Dx: min(max(x, 0), MaxJump), Dy: min(max(y, 0), MaxJump)})
}

// pointerCheck validates o and does nothing else.
func (a *Arbiter) pointerCheck(o Owner) error {
	if !o.IsValid() {
		return fmt.Errorf("output: invalid owner %v", o)
	}
	return nil
}

func (a *Arbiter) pointer(o Owner, e KeyEvent) error {
	if err := a.pointerCheck(o); err != nil {
		return err
	}
	if a.blocked {
		a.stats.Blocked++
		return a.pointerRetry()
	}
	err := errors.Join(a.pointerRetry(), a.runReady())
	if len(a.outbox) > 0 {
		a.queuePointer(o, e)
		return errors.Join(err, a.flush())
	}
	return errors.Join(err, a.sendBatch([]KeyEvent{e}))
}

// pointerRetry is retryPending for a pointer command: a command resends the
// releases that failed (CLAUDE.md invariant 1), but a stick commands 125 times a
// second, so a release that fails again is not tried again before its
// RetryInterval is over, which Advance goes by too. Otherwise a key stuck for a
// moment would make every move try, and report, its release.
func (a *Arbiter) pointerRetry() error {
	if a.retryPendingCount() == 0 || a.retryAt != 0 && a.clk.Now() < a.retryAt {
		return nil
	}
	return a.retryPending()
}

// queuePointer puts e at the end of the event gap's queue. A move joins the one
// before it when that is the same owner's: the sum is what the two would have
// moved. A move that cancels the one before it removes it.
func (a *Arbiter) queuePointer(o Owner, e KeyEvent) {
	if n := len(a.outbox); n > 0 && e.Mouse == keys.MouseMove {
		if last := &a.outbox[n-1]; last.e.Mouse == keys.MouseMove && last.o == o {
			last.e.Dx = satAdd(last.e.Dx, e.Dx)
			last.e.Dy = satAdd(last.e.Dy, e.Dy)
			if last.e.Dx == 0 && last.e.Dy == 0 {
				a.outbox = a.outbox[:n-1]
			}
			return
		}
	}
	a.outbox = append(a.outbox, outEvent{e: e, o: o})
}

// satAdd returns a + b, saturating at the limits of int32.
func satAdd(a, b int32) int32 {
	return int32(min(max(int64(a)+int64(b), math.MinInt32), math.MaxInt32))
}

// onlyPointer reports whether every event of batch moves the pointer (and there
// is one).
func onlyPointer(batch []KeyEvent) bool {
	return len(batch) > 0 && !slices.ContainsFunc(batch, func(e KeyEvent) bool { return !e.Pointer() })
}

// pointerError limits the reports of the Send errors of a batch of pointer
// events (pointerErrInterval): the first, then one a second.
func (a *Arbiter) pointerError(err error) error {
	if err == nil {
		return nil
	}
	now := a.clk.Now()
	if a.pointerErrSet && now < a.pointerErrAt.Add(pointerErrInterval) {
		return nil
	}
	a.pointerErrSet, a.pointerErrAt = true, now
	return err
}
