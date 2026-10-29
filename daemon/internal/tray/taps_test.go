package tray

import (
	"testing"
	"time"
)

// fakeTimers are the timers of a Taps under test: they fire only when the
// test says so.
type fakeTimers struct {
	timers []*fakeTimer
}

type fakeTimer struct {
	d       time.Duration
	f       func()
	stopped bool
	fired   bool
}

func (ft *fakeTimers) after(d time.Duration, f func()) func() bool {
	timer := &fakeTimer{d: d, f: f}
	ft.timers = append(ft.timers, timer)
	return func() bool {
		if timer.stopped || timer.fired {
			return false
		}
		timer.stopped = true
		return true
	}
}

// fire fires timer i, as the runtime would once its time has passed.
func (ft *fakeTimers) fire(t *testing.T, i int) {
	t.Helper()
	timer := ft.timers[i]
	if timer.stopped || timer.fired {
		t.Fatalf("timer %d cannot fire: stopped %v, fired %v", i, timer.stopped, timer.fired)
	}
	timer.fired = true
	timer.f()
}

func newTaps() (*Taps, *fakeTimers, *int, *int) {
	ft := &fakeTimers{}
	var opens, menus int
	taps := &Taps{
		Interval:  500 * time.Millisecond,
		Open:      func() { opens++ },
		Menu:      func() { menus++ },
		AfterFunc: ft.after,
	}
	return taps, ft, &opens, &menus
}

func TestTapsSingleClickShowsTheMenuAfterTheInterval(t *testing.T) {
	taps, ft, opens, menus := newTaps()
	taps.Tap()
	if *menus != 0 || *opens != 0 {
		t.Fatalf("a tap acted at once: %d menus, %d opens", *menus, *opens)
	}
	if len(ft.timers) != 1 || ft.timers[0].d != 500*time.Millisecond {
		t.Fatalf("timers = %+v, want one of the interval", ft.timers)
	}
	ft.fire(t, 0)
	if *menus != 1 || *opens != 0 {
		t.Errorf("after the interval: %d menus, %d opens; want 1, 0", *menus, *opens)
	}
}

func TestTapsDoubleClickOpensAndShowsNoMenu(t *testing.T) {
	taps, ft, opens, menus := newTaps()
	taps.Tap()
	taps.Tap()
	if *opens != 1 || *menus != 0 {
		t.Fatalf("double click: %d opens, %d menus; want 1, 0", *opens, *menus)
	}
	if !ft.timers[0].stopped {
		t.Error("the timer of the first tap still runs")
	}
	// The next tap is a first one again.
	taps.Tap()
	if len(ft.timers) != 2 {
		t.Fatalf("timers = %d, want 2", len(ft.timers))
	}
	ft.fire(t, 1)
	if *opens != 1 || *menus != 1 {
		t.Errorf("then a single click: %d opens, %d menus; want 1, 1", *opens, *menus)
	}
}

func TestTapsTwoSlowClicksAreTwoSingleClicks(t *testing.T) {
	taps, ft, opens, menus := newTaps()
	taps.Tap()
	ft.fire(t, 0)
	taps.Tap()
	ft.fire(t, 1)
	if *opens != 0 || *menus != 2 {
		t.Errorf("two slow clicks: %d opens, %d menus; want 0, 2", *opens, *menus)
	}
}

// A second tap that comes while the timer of the first is already firing
// starts a new wait; the firing timer then shows no menu, so one menu comes,
// for the second tap.
func TestTapsTapWhileTheTimerFires(t *testing.T) {
	taps, ft, opens, menus := newTaps()
	taps.Tap()
	first := ft.timers[0]
	first.fired = true // fired, its callback not yet run
	taps.Tap()
	first.f()
	if *opens != 0 || *menus != 0 {
		t.Fatalf("stale timer acted: %d opens, %d menus", *opens, *menus)
	}
	ft.fire(t, 1)
	if *opens != 0 || *menus != 1 {
		t.Errorf("%d opens, %d menus; want 0, 1", *opens, *menus)
	}
}

// With no AfterFunc, Taps uses real timers.
func TestTapsRealTimer(t *testing.T) {
	menu := make(chan struct{}, 1)
	taps := &Taps{Interval: time.Millisecond, Open: func() { t.Error("opened") }, Menu: func() { menu <- struct{}{} }}
	taps.Tap()
	select {
	case <-menu:
	case <-time.After(5 * time.Second):
		t.Fatal("no menu")
	}
}
