package clock_test

import (
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

// armCounter is a Fake that counts how often its timers are armed.
type armCounter struct {
	*clock.Fake
	arms int
}

func (c *armCounter) NewTimer(d time.Duration) clock.Timer {
	c.arms++
	return &countedTimer{c.Fake.NewTimer(d), c}
}

type countedTimer struct {
	clock.Timer
	c *armCounter
}

func (t *countedTimer) Reset(d time.Duration) bool {
	t.c.arms++
	return t.Timer.Reset(d)
}

func TestAlarm(t *testing.T) {
	clk := &armCounter{Fake: clock.NewFake()}
	a := clock.NewAlarm(clk)
	if a.C() != nil {
		t.Fatal("C of a new Alarm is not nil")
	}
	a.Set(0, false)
	if clk.arms != 0 {
		t.Fatalf("Set(_, false) armed a timer")
	}

	a.Set(at(10), true)
	a.Set(at(10), true) // unchanged: left alone
	if clk.arms != 1 {
		t.Errorf("%d armings for one deadline, want 1", clk.arms)
	}
	clk.Set(at(5))
	if _, ok := fired(a.C()); ok {
		t.Fatal("fired before its deadline")
	}
	clk.Set(at(10))
	if v, ok := fired(a.C()); !ok || v != at(10) {
		t.Fatalf("at 10: fired %v, %v; want 10ms", v, ok)
	}
	a.Fired()
	if a.C() != nil {
		t.Error("C is not nil after Fired")
	}

	// After Fired the same deadline arms again, and fires at once when it
	// is not after Now.
	a.Set(at(10), true)
	if v, ok := fired(a.C()); !ok || v != at(10) {
		t.Fatalf("re-armed at 10: fired %v, %v; want 10ms", v, ok)
	}
	a.Fired()

	// A new deadline re-arms; a stale value does not come through.
	a.Set(at(30), true)
	a.Set(at(20), true)
	if d, ok := clk.Next(); !ok || d != at(20) {
		t.Errorf("Next() = %v, %v; want 20ms", d, ok)
	}
	clk.Set(at(25))
	a.Set(at(40), true)
	if _, ok := fired(a.C()); ok {
		t.Error("a stale value came through after Set")
	}
	clk.Set(at(40))
	if v, ok := fired(a.C()); !ok || v != at(40) {
		t.Fatalf("at 40: fired %v, %v; want 40ms", v, ok)
	}
	a.Fired()

	// Set(_, false) and Stop disarm.
	a.Set(at(50), true)
	a.Set(0, false)
	if _, ok := clk.Next(); ok || a.C() != nil {
		t.Error("Set(_, false) left the alarm armed")
	}
	a.Set(at(60), true)
	a.Stop()
	a.Stop()
	if _, ok := clk.Next(); ok || a.C() != nil {
		t.Error("Stop left the alarm armed")
	}
	clk.Set(at(60))
	if clk.arms != 7 {
		t.Errorf("%d armings, want 7", clk.arms)
	}
}
