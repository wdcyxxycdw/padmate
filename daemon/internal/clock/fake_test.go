package clock_test

import (
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

var _ clock.Clock = (*clock.Fake)(nil)

// at is a Time in milliseconds, to keep the tables short.
func at(msec int) clock.Time { return clock.Time(time.Duration(msec) * ms) }

// fired reports the value waiting on c, if any, without blocking.
func fired(c <-chan clock.Time) (clock.Time, bool) {
	select {
	case v := <-c:
		return v, true
	default:
		return 0, false
	}
}

func TestFakeAdvance(t *testing.T) {
	f := clock.NewFake()
	if f.Now() != 0 {
		t.Fatalf("NewFake().Now() = %v, want 0", f.Now())
	}
	if _, ok := f.Next(); ok {
		t.Error("Next() with no timers reports one")
	}

	// Deadlines 30, 10, 20, 10 and 50 ms, in creation order.
	durs := []int{30, 10, 20, 10, 50}
	timers := make([]clock.Timer, len(durs))
	for i, d := range durs {
		timers[i] = f.NewTimer(time.Duration(d) * ms)
	}
	if next, ok := f.Next(); !ok || next != at(10) {
		t.Errorf("Next() = %v, %v, want 10ms", next, ok)
	}

	steps := []struct {
		advance int
		fire    []int // indexes into timers that fire in this step
		next    int   // expected Next, or -1 for none
	}{
		{5, nil, 10},
		{5, []int{1, 3}, 20}, // exactly at the deadline
		{15, []int{2}, 30},
		{5, []int{0}, 50},
		{20, []int{4}, -1},
		{10, nil, -1},
	}
	now := 0
	for _, st := range steps {
		now += st.advance
		f.Advance(time.Duration(st.advance) * ms)
		if f.Now() != at(now) {
			t.Fatalf("Now() = %v after advancing to %dms", f.Now(), now)
		}
		for i, tm := range timers {
			want := slices.Contains(st.fire, i)
			v, ok := fired(tm.C())
			if ok != want || ok && v != at(durs[i]) {
				t.Errorf("at %dms: timer %d sent %v, %v; want %v with its deadline %dms", now, i, v, ok, want, durs[i])
			}
		}
		next, ok := f.Next()
		if ok != (st.next >= 0) || ok && next != at(st.next) {
			t.Errorf("at %dms: Next() = %v, %v, want %dms", now, next, ok, st.next)
		}
	}
}

func TestFakeAdvanceFiresEveryDueTimer(t *testing.T) {
	// One Advance past several deadlines fires each timer once, with its own
	// deadline, and leaves Now at the target.
	f := clock.NewFake()
	a := f.NewTimer(10 * ms)
	b := f.NewTimer(10 * ms)
	c := f.NewTimer(40 * ms)
	d := f.NewTimer(100 * ms)
	f.Advance(50 * ms)
	for name, tc := range map[string]struct {
		tm   clock.Timer
		want clock.Time
		ok   bool
	}{
		"a": {a, at(10), true},
		"b": {b, at(10), true},
		"c": {c, at(40), true},
		"d": {d, 0, false},
	} {
		if v, ok := fired(tc.tm.C()); v != tc.want || ok != tc.ok {
			t.Errorf("timer %s: %v, %v, want %v, %v", name, v, ok, tc.want, tc.ok)
		}
	}
	if f.Now() != at(50) {
		t.Errorf("Now() = %v, want 50ms", f.Now())
	}
	if next, ok := f.Next(); !ok || next != at(100) {
		t.Errorf("Next() = %v, %v, want 100ms", next, ok)
	}
	f.Set(at(100))
	if v, ok := fired(d.C()); !ok || v != at(100) {
		t.Errorf("timer d: %v, %v after Set(100ms)", v, ok)
	}
	if _, ok := f.Next(); ok {
		t.Error("Next() reports a timer after all fired")
	}
}

func TestFakeStopReset(t *testing.T) {
	f := clock.NewFake()

	// Stop before the deadline.
	tm := f.NewTimer(10 * ms)
	if !tm.Stop() {
		t.Error("Stop of an active timer = false")
	}
	if tm.Stop() {
		t.Error("second Stop = true")
	}
	if _, ok := f.Next(); ok {
		t.Error("Next() reports a stopped timer")
	}
	f.Advance(20 * ms)
	if v, ok := fired(tm.C()); ok {
		t.Errorf("stopped timer sent %v", v)
	}

	// Fired but not received: Stop reports it and drops the value.
	tm = f.NewTimer(10 * ms) // deadline 30ms
	f.Advance(10 * ms)
	if !tm.Stop() {
		t.Error("Stop of a timer whose value was not received = false")
	}
	if v, ok := fired(tm.C()); ok {
		t.Errorf("stale value %v after Stop", v)
	}

	// Fired but not received: Reset drops the value and re-arms.
	tm = f.NewTimer(10 * ms) // deadline 40ms
	f.Advance(10 * ms)
	if !tm.Reset(5 * ms) {
		t.Error("Reset of a timer whose value was not received = false")
	}
	if v, ok := fired(tm.C()); ok {
		t.Errorf("stale value %v after Reset", v)
	}
	f.Advance(5 * ms)
	if v, ok := fired(tm.C()); !ok || v != at(45) {
		t.Errorf("reset timer sent %v, %v, want 45ms", v, ok)
	}

	// Received: Stop and Reset report false; Reset re-arms.
	if tm.Stop() {
		t.Error("Stop after the value was received = true")
	}
	if tm.Reset(10 * ms) {
		t.Error("Reset after the value was received = true")
	}
	// Reset of an active timer moves its deadline.
	f.Advance(5 * ms)
	if !tm.Reset(10 * ms) {
		t.Error("Reset of an active timer = false")
	}
	f.Advance(5 * ms) // the old deadline, 55ms
	if v, ok := fired(tm.C()); ok {
		t.Errorf("timer fired at its old deadline with %v", v)
	}
	f.Advance(5 * ms)
	if v, ok := fired(tm.C()); !ok || v != at(60) {
		t.Errorf("moved timer sent %v, %v, want 60ms", v, ok)
	}
}

func TestFakeDueAtOnce(t *testing.T) {
	// A deadline that is not after Now fires in NewTimer or Reset, with Now
	// as its value.
	f := clock.NewFake()
	f.Advance(7 * ms)
	for _, d := range []time.Duration{0, -time.Second} {
		tm := f.NewTimer(d)
		if v, ok := fired(tm.C()); !ok || v != at(7) {
			t.Errorf("NewTimer(%v) sent %v, %v, want 7ms", d, v, ok)
		}
		if tm.Reset(d) {
			t.Errorf("Reset(%v) after the value was received = true", d)
		}
		if v, ok := fired(tm.C()); !ok || v != at(7) {
			t.Errorf("Reset(%v) sent %v, %v, want 7ms", d, v, ok)
		}
	}
	if _, ok := f.Next(); ok {
		t.Error("Next() reports a timer that already fired")
	}
}

func TestFakeSetBackwardsPanics(t *testing.T) {
	f := clock.NewFake()
	f.Set(at(20))
	f.Set(at(20)) // not backwards
	for name, fn := range map[string]func(){
		"Set":     func() { f.Set(at(19)) },
		"Advance": func() { f.Advance(-1) },
	} {
		func() {
			defer func() {
				r := recover()
				if s, _ := r.(string); !strings.Contains(s, "before Now") {
					t.Errorf("%s backwards: panic %v, want one naming Now", name, r)
				}
			}()
			fn()
		}()
	}
	if f.Now() != at(20) {
		t.Errorf("Now() = %v after the panics, want 20ms", f.Now())
	}
}

func TestFakeConcurrent(t *testing.T) {
	// Goroutines wait on timers while the test advances the clock; run with
	// -race in CI.
	f := clock.NewFake()
	const workers, rounds = 4, 50
	var wg sync.WaitGroup
	errs := make(chan string, workers*rounds)
	for w := range workers {
		wg.Go(func() {
			tm := f.NewTimer(time.Duration(w+1) * ms)
			for range rounds {
				v := <-tm.C()
				if now := f.Now(); v > now {
					errs <- "value " + v.String() + " after Now " + now.String()
				}
				tm.Reset(time.Duration(w+1) * ms)
			}
			tm.Stop()
		})
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	for {
		select {
		case <-done:
			close(errs)
			for e := range errs {
				t.Error(e)
			}
			return
		default:
			f.Advance(ms)
			f.Next()
			runtime.Gosched()
		}
	}
}
