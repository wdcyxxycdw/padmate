// Package script contains the goja host used by a profile slot. It owns no
// goroutines: the slot loop calls RunDue and DrainMicrotasks after each input
// edge, which keeps script execution on the same goroutine as the engine.
package script

import (
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

// EventLoop is a small deterministic macro/microtask loop. A loop is not
// safe for concurrent use; its owner is the slot loop goroutine.
type EventLoop struct {
	clock   clock.Clock
	nextID  uint64
	timers  []*scheduled
	micro   []func()
	lastNow clock.Time
}

type scheduled struct {
	id       uint64
	deadline clock.Time
	period   time.Duration
	fn       func()
	canceled bool
}

// Handle identifies a timer and can cancel it. Cancel reports whether the
// timer was active at the time of the call.
type Handle struct{ timer *scheduled }

// Cancel prevents a timer from running again.
func (h Handle) Cancel() bool {
	if h.timer == nil || h.timer.canceled {
		return false
	}
	h.timer.canceled = true
	return true
}

// NewEventLoop makes a loop driven by c. c is also the source used by Host's
// now() and sleep() functions.
func NewEventLoop(c clock.Clock) *EventLoop {
	if c == nil {
		c = clock.Real()
	}
	return &EventLoop{clock: c, lastNow: c.Now()}
}

// After schedules fn once after d. A non-positive duration is due on the
// next RunDue call.
func (l *EventLoop) After(d time.Duration, fn func()) Handle {
	return l.add(maxDuration(d), 0, fn)
}

// Every schedules fn repeatedly. The first call is after d and each later
// call is one period after the previous RunDue; missed periods are not
// replayed when the owner was asleep.
func (l *EventLoop) Every(d time.Duration, fn func()) Handle {
	d = maxDuration(d)
	if d == 0 {
		// A repeating zero-duration timer must remain repeating without
		// allowing RunDue to spin forever at one instant.
		d = time.Nanosecond
	}
	return l.add(d, d, fn)
}

func maxDuration(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

func (l *EventLoop) add(d, period time.Duration, fn func()) Handle {
	if fn == nil {
		return Handle{}
	}
	l.nextID++
	t := &scheduled{id: l.nextID, deadline: l.clock.Now().Add(d), period: period, fn: fn}
	l.timers = append(l.timers, t)
	return Handle{timer: t}
}

// QueueMicrotask queues fn for DrainMicrotasks. It is useful for Go-side
// completions such as tap results; goja's own promise queue is drained by the
// outermost call into the runtime.
func (l *EventLoop) QueueMicrotask(fn func()) {
	if fn != nil {
		l.micro = append(l.micro, fn)
	}
}

// NextDeadline returns the earliest active timer deadline.
func (l *EventLoop) NextDeadline() (clock.Time, bool) {
	l.compact()
	if len(l.timers) == 0 {
		return 0, false
	}
	best := l.timers[0]
	for _, t := range l.timers[1:] {
		if t.deadline.Before(best.deadline) || t.deadline == best.deadline && t.id < best.id {
			best = t
		}
	}
	return best.deadline, true
}

// RunDue runs all timers whose deadline is no later than now and returns the
// number of callbacks. Timers scheduled by callbacks at the same instant are
// eligible in this pass; FIFO order is preserved for equal deadlines.
func (l *EventLoop) RunDue(now clock.Time) int {
	n, _ := l.RunDueWith(now, func(fn func()) error { fn(); return nil })
	return n
}

// RunDueWith lets the owner put a budget around each callback. An error stops
// the pass before another timer can run on a failed runtime.
func (l *EventLoop) RunDueWith(now clock.Time, run func(func()) error) (int, error) {
	if now < l.lastNow {
		now = l.lastNow
	}
	l.lastNow = now
	n := 0
	for {
		l.compact()
		var idx = -1
		for i, t := range l.timers {
			if t.deadline <= now && (idx < 0 || t.deadline.Before(l.timers[idx].deadline) ||
				t.deadline == l.timers[idx].deadline && t.id < l.timers[idx].id) {
				idx = i
			}
		}
		if idx < 0 {
			break
		}
		t := l.timers[idx]
		l.timers = append(l.timers[:idx], l.timers[idx+1:]...)
		if t.canceled {
			continue
		}
		if t.period > 0 {
			// Re-arm before calling fn so fn can cancel its own handle.
			t.deadline = now.Add(t.period)
			l.timers = append(l.timers, t)
		}
		if err := run(t.fn); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// DrainMicrotasks runs queued Go microtasks FIFO, including tasks queued by
// a task currently running.
func (l *EventLoop) DrainMicrotasks() {
	for len(l.micro) > 0 {
		fn := l.micro[0]
		l.micro = l.micro[1:]
		fn()
	}
}

// Clear removes all scheduled work when a runtime is disposed.
func (l *EventLoop) Clear() {
	for _, t := range l.timers {
		t.canceled = true
	}
	l.timers = nil
	l.micro = nil
}

func (l *EventLoop) compact() {
	write := 0
	for _, t := range l.timers {
		if !t.canceled {
			l.timers[write] = t
			write++
		}
	}
	clear(l.timers[write:])
	l.timers = l.timers[:write]
}
