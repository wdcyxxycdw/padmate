// Package clock is the time source of the engine, the Output Arbiter and
// the slot loops (ENG-07). The pure packages read time only through a
// Clock (CLAUDE.md invariant 2), so tests drive them with a Fake.
//
// The package uses only the standard library and builds on every OS. It
// must not import output, input, script or x/sys/windows: the engine
// imports it.
package clock

import (
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Time is an instant on a Clock: nanoseconds since the clock's epoch.
type Time int64

// Add returns t+d, saturating at the int64 limits instead of wrapping.
func (t Time) Add(d time.Duration) Time {
	s := t + Time(d)
	switch {
	case d > 0 && s < t:
		return math.MaxInt64
	case d < 0 && s > t:
		return math.MinInt64
	}
	return s
}

// Sub returns t-u, saturating like time.Time.Sub.
func (t Time) Sub(u Time) time.Duration {
	d := time.Duration(t - u)
	switch {
	case t > u && d <= 0:
		return math.MaxInt64
	case t < u && d >= 0:
		return math.MinInt64
	}
	return d
}

// Before reports whether t is before u.
func (t Time) Before(u Time) bool { return t < u }

// After reports whether t is after u.
func (t Time) After(u Time) bool { return t > u }

// String formats t as a duration since the epoch, such as "120ms".
func (t Time) String() string { return time.Duration(t).String() }

// AppendMs appends t in milliseconds with the shortest exact fraction,
// such as "20", "1.5" or "-0.000001": the time format of simtest's traces,
// of engine decisions and of padmated's --decisions.
func (t Time) AppendMs(b []byte) []byte {
	const nsPerMs = uint64(time.Millisecond)
	u := uint64(t)
	if t < 0 {
		b = append(b, '-')
		u = -u // also right for math.MinInt64
	}
	b = strconv.AppendUint(b, u/nsPerMs, 10)
	if frac := u % nsPerMs; frac != 0 {
		// Adding nsPerMs gives the fraction its leading zeros.
		s := strconv.FormatUint(nsPerMs+frac, 10)[1:]
		b = append(b, '.')
		b = append(b, strings.TrimRight(s, "0")...)
	}
	return b
}

// Clock tells the time and makes timers.
type Clock interface {
	Now() Time
	// NewTimer returns a timer that sends its deadline on C once d has
	// elapsed. A d <= 0 fires at once, with Now as the deadline.
	NewTimer(d time.Duration) Timer
}

// Timer is a one-shot timer of a Clock. Its channel has capacity 1.
type Timer interface {
	C() <-chan Time
	// Stop and Reset behave like time.Timer since Go 1.23: after they
	// return, no stale value is received from C. They report whether the
	// timer was active, which includes a value sent but not yet received.
	Stop() bool
	Reset(d time.Duration) bool
}

// epoch is the zero Time of the real clock, fixed at package init.
var epoch = time.Now()

// Real returns the process-wide real clock. Its Times count from a single
// epoch on the monotonic clock, so all of them in a process compare
// meaningfully. On Windows the monotonic clock moves in steps of 0.5–2 ms
// (docs/PITFALLS.md "Windows 计时"); latency measurements that need microseconds use QPC.
func Real() Clock { return realClock{} }

type realClock struct{}

func (realClock) Now() Time { return Time(time.Since(epoch)) }

func (realClock) NewTimer(d time.Duration) Timer {
	r := &realTimer{c: make(chan Time, 1)}
	r.arm(d)
	return r
}

// realTimer runs each arming as its own time.AfterFunc. The callback
// carries the arming's sequence number and sends only if no Stop or Reset
// came since, which gives the Go 1.23 no-stale-value guarantee.
type realTimer struct {
	c        chan Time
	mu       sync.Mutex
	t        *time.Timer
	seq      uint64 // bumped by every arming and Stop
	deadline Time
	active   bool
}

func (r *realTimer) C() <-chan Time { return r.c }

func (r *realTimer) Stop() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stop()
}

func (r *realTimer) Reset(d time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	was := r.stop()
	r.arm(d)
	return was
}

// arm starts a new arming. r.mu is held, or r is not shared yet.
func (r *realTimer) arm(d time.Duration) {
	d = max(d, 0)
	r.seq++
	seq := r.seq
	r.deadline = realClock{}.Now().Add(d)
	r.active = true
	r.t = time.AfterFunc(d, func() { r.fire(seq) })
}

// stop disarms r and drops an unreceived value. r.mu is held.
func (r *realTimer) stop() bool {
	was := r.active
	r.active = false
	r.seq++
	r.t.Stop()
	select {
	case <-r.c:
		was = true
	default:
	}
	return was
}

func (r *realTimer) fire(seq uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if seq != r.seq || !r.active {
		return
	}
	r.active = false
	select {
	case r.c <- r.deadline:
	default:
	}
}
