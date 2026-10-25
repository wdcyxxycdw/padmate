package input_test

import (
	"context"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
)

// TestEventStamp (DMN-15, ADR-0071): T-input reads latency.Now right after SDL
// returns the event of a button or an axis, and the Event carries it as Stamp.
// The reading lies between the counter before the virtual gamepad was driven
// and the counter after the event came out of Events. The events of devices
// (added, removed) carry none: they are not input edges.
func TestEventStamp(t *testing.T) {
	s := open(t, nil)
	v, added := attach(t, s, "padmate stamp pad")
	if added.Stamp != 0 {
		t.Errorf("DeviceAdded carries Stamp %d, want none", added.Stamp)
	}
	for _, step := range []struct {
		name string
		do   func() error
		want string
	}{
		{"press", func() error { return v.SetButton("south", true) }, "south down"},
		{"release", func() error { return v.SetButton("south", false) }, "south up"},
		{"axis", func() error { return v.SetAxis("lefttrigger", 20000) }, "lefttrigger 20000"},
	} {
		before := latency.Now()
		if err := step.do(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		e := collect(t, s, v.ID(), 1)[0]
		after := latency.Now()
		if format(e) != step.want {
			t.Fatalf("%s: event %q, want %q", step.name, format(e), step.want)
		}
		if e.Stamp < before || e.Stamp > after {
			t.Errorf("%s: Stamp %d is outside [%d, %d]", step.name, e.Stamp, before, after)
		}
		t.Logf("%s: the event was stamped %v into the call, %v before it was out of Events", step.name, e.Stamp.Sub(before), after.Sub(e.Stamp))
	}
	if err := v.Detach(); err != nil {
		t.Fatal(err)
	}
	// SDL lets the controls go first (an axis back to rest), which are edges
	// with a Stamp; the removal is the one without.
	for {
		e := collect(t, s, v.ID(), 1)[0]
		if e.Kind == input.DeviceRemoved {
			if e.Stamp != 0 {
				t.Errorf("DeviceRemoved carries Stamp %d, want none", e.Stamp)
			}
			return
		}
		if e.Stamp == 0 {
			t.Errorf("event %q before the removal has no Stamp", format(e))
		}
	}
}

// TestReenumerateEventsHaveNoStamp (DMN-15, ADR-0071): the events that a
// reenumeration drains from SDL after a sleep were queued at an unknown time,
// not when T-input last woke up, so they carry no Stamp. They must not take
// the reading of the last event the loop handled, which can be hours old: a
// sample from it would be hours long and spoil the maximum and the p99 of the
// device until it is reset.
func TestReenumerateEventsHaveNoStamp(t *testing.T) {
	s := open(t, nil)
	v, _ := attach(t, s, "padmate stale stamp pad")
	// A reading from long ago, as T-input left it before the sleep.
	const stale = latency.Stamp(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := v.ReenumerateWithQueuedPress(ctx, "south", stale); err != nil {
		t.Fatal(err)
	}
	pressed := false
	for {
		var e input.Event
		select {
		case e = <-s.Events():
		case <-ctx.Done():
			t.Fatal("no DeviceState of the reenumeration")
		}
		if e.Device != v.ID() {
			continue
		}
		if e.Kind == input.ButtonDown {
			pressed = true
			if e.Stamp != 0 {
				t.Errorf("the press drained by the reenumeration has Stamp %d, want none", e.Stamp)
			}
		}
		if e.Kind == input.DeviceState {
			break
		}
	}
	if !pressed {
		t.Error("the reenumeration drained no press: the test shows nothing")
	}
	// The loop stamps the next live edge afresh.
	before := latency.Now()
	if err := v.SetButton("south", true); err != nil {
		t.Fatal(err)
	}
	e := collect(t, s, v.ID(), 1)[0]
	if e.Stamp < before || e.Stamp > latency.Now() {
		t.Errorf("the next live edge has Stamp %d, want one since %d", e.Stamp, before)
	}
}
