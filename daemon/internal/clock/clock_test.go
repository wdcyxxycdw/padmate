package clock_test

import (
	"math"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

const ms = time.Millisecond

func TestTimeArithmetic(t *testing.T) {
	const maxT, minT = clock.Time(math.MaxInt64), clock.Time(math.MinInt64)
	add := []struct {
		t    clock.Time
		d    time.Duration
		want clock.Time
	}{
		{0, 0, 0},
		{0, 120 * ms, clock.Time(120 * ms)},
		{clock.Time(120 * ms), -20 * ms, clock.Time(100 * ms)},
		{0, -1, -1},
		// Saturates instead of wrapping, like time.Time.Sub.
		{1, math.MaxInt64, maxT},
		{maxT, 1, maxT},
		{-1, math.MinInt64, minT},
		{minT, -1, minT},
	}
	for _, tt := range add {
		if got := tt.t.Add(tt.d); got != tt.want {
			t.Errorf("Time(%d).Add(%d) = %d, want %d", tt.t, tt.d, got, tt.want)
		}
	}

	sub := []struct {
		t, u clock.Time
		want time.Duration
	}{
		{0, 0, 0},
		{clock.Time(120 * ms), clock.Time(20 * ms), 100 * ms},
		{clock.Time(20 * ms), clock.Time(120 * ms), -100 * ms},
		{maxT, -1, math.MaxInt64},
		{maxT, minT, math.MaxInt64},
		{minT, 1, math.MinInt64},
		{minT, maxT, math.MinInt64},
		{maxT, maxT, 0},
	}
	for _, tt := range sub {
		if got := tt.t.Sub(tt.u); got != tt.want {
			t.Errorf("Time(%d).Sub(%d) = %d, want %d", tt.t, tt.u, got, tt.want)
		}
	}

	a, b := clock.Time(10), clock.Time(20)
	if !a.Before(b) || a.After(b) || !b.After(a) || b.Before(a) || a.Before(a) || a.After(a) {
		t.Errorf("Before/After wrong for %d, %d", a, b)
	}

	for _, tt := range []struct {
		t    clock.Time
		want string
	}{
		{0, "0s"},
		{clock.Time(120 * ms), "120ms"},
		{clock.Time(1500 * time.Microsecond), "1.5ms"},
		{clock.Time(2 * time.Second), "2s"},
	} {
		if got := tt.t.String(); got != tt.want {
			t.Errorf("Time(%d).String() = %q, want %q", tt.t, got, tt.want)
		}
	}
}

func TestAppendMs(t *testing.T) {
	for _, tt := range []struct {
		t    clock.Time
		want string
	}{
		{0, "0"},
		{clock.Time(20 * ms), "20"},
		{clock.Time(1500 * time.Microsecond), "1.5"},
		{1, "0.000001"},
		{clock.Time(2104*ms + 556300), "2104.5563"},
		{-1, "-0.000001"},
		{math.MaxInt64, "9223372036854.775807"},
		{math.MinInt64, "-9223372036854.775808"},
	} {
		if got := string(tt.t.AppendMs([]byte("t="))); got != "t="+tt.want {
			t.Errorf("Time(%d).AppendMs(t=) = %q, want %q", tt.t, got, "t="+tt.want)
		}
	}
}

func TestRealNow(t *testing.T) {
	c := clock.Real()
	if c != clock.Real() {
		t.Fatal("Real() returns different clocks")
	}
	prev := c.Now()
	if prev < 0 {
		t.Fatalf("Now() = %v, before the epoch", prev)
	}
	for range 10000 {
		now := c.Now()
		if now < prev {
			t.Fatalf("Now() went backwards: %v after %v", now, prev)
		}
		prev = now
	}

	// The clock tracks the monotonic clock: it moves by at least the sleep
	// and by no more than the wall time measured around it.
	const sleep = 5 * ms
	w0 := time.Now()
	t0 := c.Now()
	time.Sleep(sleep)
	t1 := c.Now()
	wall := time.Since(w0)
	if d := t1.Sub(t0); d < sleep || d > wall {
		t.Errorf("Now() moved %v across a %v sleep, want between %v and %v", d, sleep, sleep, wall)
	}
}

// recv waits up to a second for a value on c.
func recv(t *testing.T, c <-chan clock.Time) clock.Time {
	t.Helper()
	select {
	case v := <-c:
		return v
	case <-time.After(time.Second):
		t.Fatal("timer did not fire within 1s")
		return 0
	}
}

// expectNone fails if c holds a value.
func expectNone(t *testing.T, c <-chan clock.Time, what string) {
	t.Helper()
	select {
	case v := <-c:
		t.Errorf("%s: received %v, want nothing", what, v)
	default:
	}
}

func TestRealTimer(t *testing.T) {
	c := clock.Real()

	// A timer sends its deadline once d has elapsed.
	start := c.Now()
	tm := c.NewTimer(2 * ms)
	v := recv(t, tm.C())
	if now := c.Now(); v < start.Add(2*ms) || v > now {
		t.Errorf("timer sent %v, want a deadline in [%v, %v]", v, start.Add(2*ms), now)
	}
	if tm.Stop() {
		t.Error("Stop after the value was received = true, want false")
	}

	// d <= 0 fires at once with the current time as deadline.
	for _, d := range []time.Duration{0, -time.Hour} {
		before := c.Now()
		v := recv(t, c.NewTimer(d).C())
		if v < before || v > c.Now() {
			t.Errorf("NewTimer(%v) sent %v, want about %v", d, v, before)
		}
	}

	// Stop before it fires.
	tm = c.NewTimer(time.Hour)
	if !tm.Stop() {
		t.Error("Stop of an active timer = false, want true")
	}
	if tm.Stop() {
		t.Error("second Stop = true, want false")
	}

	// A value that fired but was not received counts as pending: Reset
	// reports it and drops it. Whether or not the timer has fired after the
	// sleep, Reset returns true and no stale value is received.
	tm = c.NewTimer(ms)
	time.Sleep(10 * ms)
	if !tm.Reset(time.Hour) {
		t.Error("Reset of a timer whose value was not received = false, want true")
	}
	expectNone(t, tm.C(), "after Reset")
	time.Sleep(5 * ms)
	expectNone(t, tm.C(), "after Reset(1h)")

	// Reset re-arms with a new deadline.
	start = c.Now()
	if !tm.Reset(2 * ms) {
		t.Error("Reset of an active timer = false, want true")
	}
	if v := recv(t, tm.C()); v < start.Add(2*ms) {
		t.Errorf("reset timer sent %v, want at least %v", v, start.Add(2*ms))
	}
	if tm.Reset(time.Hour) {
		t.Error("Reset after the value was received = true, want false")
	}
	if !tm.Stop() {
		t.Error("Stop after Reset = false, want true")
	}
}

func TestRealTimerStopRace(t *testing.T) {
	// Stop and Reset right around the deadline never leave a stale value.
	c := clock.Real()
	tm := c.NewTimer(0)
	for i := range 50 {
		tm.Reset(time.Duration(i%3) * time.Microsecond)
		if i%2 == 0 {
			time.Sleep(time.Duration(i%4) * 10 * time.Microsecond)
		}
		tm.Stop()
		time.Sleep(20 * time.Microsecond)
		expectNone(t, tm.C(), "after Stop")
	}
}
