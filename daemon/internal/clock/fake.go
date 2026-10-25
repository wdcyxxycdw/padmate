package clock

import (
	"fmt"
	"slices"
	"sync"
	"time"
)

// Fake is a Clock that moves only when told to. It starts at 0.
//
// Set and Advance fire every timer whose deadline they reach, in deadline
// order (ties in creation order); each timer sends its deadline, and Now
// is the target once they return. To run work at each deadline in turn,
// step the clock with Next and Set. A timer whose deadline is not after
// Now fires at once, in NewTimer or Reset. Firing never blocks: the
// channels have capacity 1 and hold at most one value.
//
// A Fake is safe for concurrent use.
type Fake struct {
	mu     sync.Mutex
	now    Time
	timers []*fakeTimer // active timers, unordered
	nextID uint64
}

// NewFake returns a Fake at Time 0 with no timers.
func NewFake() *Fake { return &Fake{} }

// Now returns the current fake time.
func (f *Fake) Now() Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// NewTimer returns a timer that fires once the clock reaches Now+d.
func (f *Fake) NewTimer(d time.Duration) Timer {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	t := &fakeTimer{f: f, c: make(chan Time, 1), id: f.nextID}
	f.arm(t, d)
	return t
}

// Set moves the clock to t, firing the timers it reaches. It panics if t
// is before Now.
func (f *Fake) Set(t Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.set(t)
}

// Advance moves the clock forward by d, like Set(Now().Add(d)). It panics
// if d is negative.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.set(f.now.Add(d))
}

// Next returns the earliest deadline of an active timer, or false when
// there is none.
func (f *Fake) Next() (Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.earliest()
	if i < 0 {
		return 0, false
	}
	return f.timers[i].deadline, true
}

// set fires the due timers and moves now to t. f.mu is held.
func (f *Fake) set(t Time) {
	if t < f.now {
		panic(fmt.Sprintf("clock: Fake set to %v, before Now %v", t, f.now))
	}
	for {
		i := f.earliest()
		if i < 0 || f.timers[i].deadline > t {
			break
		}
		ft := f.timers[i]
		f.remove(i)
		ft.fire()
	}
	f.now = t
}

// arm schedules t at now+d, or fires it at once when that is not after
// now. f.mu is held.
func (f *Fake) arm(t *fakeTimer, d time.Duration) {
	t.deadline = f.now.Add(max(d, 0))
	if t.deadline <= f.now {
		t.fire()
		return
	}
	t.active = true
	f.timers = append(f.timers, t)
}

// earliest returns the index of the active timer that fires first, or -1.
// f.mu is held.
func (f *Fake) earliest() int {
	best := -1
	for i, t := range f.timers {
		if best < 0 || t.deadline < f.timers[best].deadline ||
			t.deadline == f.timers[best].deadline && t.id < f.timers[best].id {
			best = i
		}
	}
	return best
}

// remove drops timer i from the active list. f.mu is held.
func (f *Fake) remove(i int) {
	f.timers[i].active = false
	f.timers = slices.Delete(f.timers, i, i+1)
}

type fakeTimer struct {
	f        *Fake
	c        chan Time
	id       uint64 // creation order, for ties
	deadline Time
	active   bool // in f.timers; guarded by f.mu
}

func (t *fakeTimer) C() <-chan Time { return t.c }

func (t *fakeTimer) Stop() bool {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	return t.stop()
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	was := t.stop()
	t.f.arm(t, d)
	return was
}

// stop disarms t and drops an unreceived value. f.mu is held.
func (t *fakeTimer) stop() bool {
	was := t.active
	if was {
		t.f.remove(slices.Index(t.f.timers, t))
	}
	select {
	case <-t.c:
		was = true
	default:
	}
	return was
}

// fire sends the deadline. The channel is empty here: a timer fires once
// per arming, and every re-arming drains it first.
func (t *fakeTimer) fire() {
	select {
	case t.c <- t.deadline:
	default:
	}
}
