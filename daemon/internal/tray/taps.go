package tray

import (
	"sync"
	"time"
)

// Taps tells a double click on the tray icon from a single one. The systray
// library reports each release of the left button (a double click is two),
// and nothing else of it: a second release within Interval of the first is a
// double click, which calls Open; a first release that no second one follows
// within Interval calls Menu, which shows the menu the library would have
// shown at once. Open and Menu run on the goroutine that calls Tap (a second
// tap) or on the timer's, and must not block.
type Taps struct {
	Interval   time.Duration
	Open, Menu func()
	// AfterFunc starts a timer; nil means time.AfterFunc. It returns the
	// timer's Stop.
	AfterFunc func(time.Duration, func()) (stop func() bool)

	mu      sync.Mutex
	waiting func() bool // stops the timer that waits for a second tap
	gen     uint64      // tells the timer of this wait from an earlier one
}

// Tap is one release of the left button on the icon.
func (t *Taps) Tap() {
	t.mu.Lock()
	if t.waiting != nil {
		stop := t.waiting
		t.waiting = nil
		if stop() {
			// The second tap in time: a double click, and no menu.
			t.mu.Unlock()
			t.Open()
			return
		}
		// The timer has fired and is about to show the menu: this tap is a
		// first one again.
	}
	t.gen++
	gen := t.gen
	after := t.AfterFunc
	if after == nil {
		after = func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }
	}
	t.waiting = after(t.Interval, func() {
		t.mu.Lock()
		if t.gen != gen || t.waiting == nil {
			t.mu.Unlock()
			return
		}
		t.waiting = nil
		t.mu.Unlock()
		t.Menu()
	})
	t.mu.Unlock()
}
