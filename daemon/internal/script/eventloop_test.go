package script

import (
	"reflect"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

func TestEventLoopTimersAndMicrotasks(t *testing.T) {
	c := clock.NewFake()
	l := NewEventLoop(c)
	var got []string
	l.After(10*time.Millisecond, func() {
		got = append(got, "first")
		l.QueueMicrotask(func() { got = append(got, "micro") })
	})
	l.After(10*time.Millisecond, func() { got = append(got, "second") })
	cancel := l.After(20*time.Millisecond, func() { got = append(got, "canceled") })
	if !cancel.Cancel() {
		t.Fatal("Cancel reported inactive timer")
	}
	if d, ok := l.NextDeadline(); !ok || d != clock.Time(10*time.Millisecond) {
		t.Fatalf("NextDeadline = %v, %v; want 10ms, true", d, ok)
	}
	c.Set(clock.Time(10 * time.Millisecond))
	if n := l.RunDue(c.Now()); n != 2 {
		t.Fatalf("RunDue ran %d timers, want 2", n)
	}
	l.DrainMicrotasks()
	if want := []string{"first", "second", "micro"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
	if _, ok := l.NextDeadline(); ok {
		t.Fatal("canceled timer left a deadline")
	}
}

func TestEventLoopEvery(t *testing.T) {
	c := clock.NewFake()
	l := NewEventLoop(c)
	var n int
	timer := l.Every(5*time.Millisecond, func() { n++ })
	for range 3 {
		c.Advance(5 * time.Millisecond)
		l.RunDue(c.Now())
	}
	if n != 3 {
		t.Fatalf("Every callback count = %d, want 3", n)
	}
	if !timer.Cancel() {
		t.Fatal("Every Cancel reported inactive")
	}
	c.Advance(5 * time.Millisecond)
	l.RunDue(c.Now())
	if n != 3 {
		t.Fatalf("Every callback ran after cancel: %d", n)
	}
}

func TestEventLoopEveryZeroRepeats(t *testing.T) {
	c := clock.NewFake()
	l := NewEventLoop(c)
	var n int
	timer := l.Every(0, func() { n++ })
	for range 3 {
		c.Advance(time.Nanosecond)
		l.RunDue(c.Now())
	}
	if n != 3 {
		t.Fatalf("Every(0) callback count = %d, want 3", n)
	}
	if !timer.Cancel() {
		t.Fatal("Every(0) Cancel reported inactive")
	}
}
