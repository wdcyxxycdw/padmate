package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/logging"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// feedSink is the Hub of a decisionFeed in a test: it keeps what is published
// as JSON, says whether anybody listens, and lets a test wait for a number
// of events.
type feedSink struct {
	mu     sync.Mutex
	listen bool
	topics []string
	events []string
	more   chan struct{}
}

func newFeedSink() *feedSink { return &feedSink{listen: true, more: make(chan struct{}, 1024)} }

func (s *feedSink) Listening(topic string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listen && topic == "engine.activity"
}

func (s *feedSink) Publish(topic string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	s.mu.Lock()
	s.topics = append(s.topics, topic)
	s.events = append(s.events, string(b))
	s.mu.Unlock()
	s.more <- struct{}{}
}

func (s *feedSink) setListening(on bool) {
	s.mu.Lock()
	s.listen = on
	s.mu.Unlock()
}

// take waits for n more events and returns every event so far.
func (s *feedSink) take(t *testing.T, n int) []string {
	t.Helper()
	for range n {
		select {
		case <-s.more:
		case <-time.After(5 * time.Second):
			t.Fatalf("waited for %d events; got %v", n, s.snapshot())
		}
	}
	return s.snapshot()
}

func (s *feedSink) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

// testFeed is a decisionFeed on a virtual clock with a wall clock that
// says Unix ms 1792843200250 until a test moves it.
type testFeed struct {
	*decisionFeed
	sink *feedSink
	clk  *clock.Fake
	wall time.Time
	mu   sync.Mutex
}

const feedWallMs = 1792843200250

func newTestFeed(t *testing.T, size int) *testFeed {
	t.Helper()
	tf := &testFeed{sink: newFeedSink(), clk: clock.NewFake(), wall: time.UnixMilli(feedWallMs)}
	tf.decisionFeed = newDecisionFeed(tf.sink, tf.clk, logging.New(&syncBuffer{}, slog.LevelDebug), size)
	tf.decisionFeed.wall = func() time.Time {
		tf.mu.Lock()
		defer tf.mu.Unlock()
		return tf.wall
	}
	return tf
}

func (tf *testFeed) wallAdd(d time.Duration) {
	tf.mu.Lock()
	tf.wall = tf.wall.Add(d)
	tf.mu.Unlock()
}

// start runs the publisher until the test ends.
func (tf *testFeed) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); tf.run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
}

func actionOf(typ profile.ActionType) *profile.Action { return &profile.Action{Type: typ} }

func decision(ms int, c control.Control, edge engine.Edge, g engine.Gesture, r engine.Reason, b *profile.Binding) engine.Decision {
	return engine.Decision{T: clock.Time(time.Duration(ms) * time.Millisecond), Control: c, Edge: edge, Cycle: 1, Gesture: g, Binding: b, Reason: r}
}

// The decision event carries the wall clock time of the decision, taken when
// the slot hands it over, and is encoded by the publisher, not by the caller.
func TestDecisionFeedPublishesOffTheCallersGoroutine(t *testing.T) {
	tf := newTestFeed(t, 8)
	b := &profile.Binding{ID: "/layers/base/south", Label: "撤销", Latch: true, Press: actionOf(profile.ActionKeys)}
	tf.decision(2, decision(20, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonDispatch, b))
	if got := tf.sink.snapshot(); len(got) != 0 {
		t.Fatalf("decision published on the caller's goroutine: %v", got)
	}
	tf.start(t)
	got := tf.sink.take(t, 1)
	var ev map[string]any
	if err := json.Unmarshal([]byte(got[0]), &ev); err != nil {
		t.Fatal(err)
	}
	if ev["kind"] != "decision" || ev["slot"] != float64(2) || ev["at"] != float64(feedWallMs) || ev["latchBinding"] != true {
		t.Errorf("event %s", got[0])
	}
	d, _ := ev["decision"].(map[string]any)
	if d["control"] != "south" || d["reason"] != "dispatch" || d["binding"] != "/layers/base/south" || d["label"] != "撤销" {
		t.Errorf("decision %v", d)
	}
	for _, k := range []string{"waiting", "waitedMs"} {
		if _, ok := ev[k]; ok {
			t.Errorf("event has %s: %s", k, got[0])
		}
	}
}

// Nothing is queued while nobody listens.
func TestDecisionFeedNotListening(t *testing.T) {
	tf := newTestFeed(t, 8)
	tf.sink.setListening(false)
	tf.decision(0, decision(1, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, nil))
	tf.unrouted(input.Event{Kind: input.ButtonDown, Device: 3, Control: "south"}, "queued")
	tf.start(t)
	tf.sink.setListening(true)
	tf.decision(0, decision(2, control.East, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, nil))
	got := tf.sink.take(t, 1)
	if len(got) != 1 || !strings.Contains(got[0], `"control":"east"`) {
		t.Errorf("events %v; want only the one made while listening", got)
	}
}

// An unbound press of a binding that has tap, hold, release or multi-tap
// actions lists them as what it waits for; it is no failure.
func TestDecisionFeedWaiting(t *testing.T) {
	tapHold := &profile.Binding{ID: "/b/tapHold", Tap: actionOf(profile.ActionKeys), Hold: actionOf(profile.ActionKeys)}
	multi := &profile.Binding{ID: "/b/multi", DoubleTap: actionOf(profile.ActionKeys), TripleTap: actionOf(profile.ActionKeys), QuadTap: actionOf(profile.ActionKeys), Release: actionOf(profile.ActionKeys)}
	labelOnly := &profile.Binding{ID: "/b/label", Label: "todo"}
	pressOnly := &profile.Binding{ID: "/b/press", Press: actionOf(profile.ActionKeys), Tap: actionOf(profile.ActionKeys)}
	for _, tc := range []struct {
		name string
		d    engine.Decision
		want string // the waiting member, "" when absent
	}{
		{"tap and hold", decision(1, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, tapHold), `"waiting":["tap","hold"]`},
		{"multi-taps and release", decision(1, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, multi), `"waiting":["release","doubleTap","tripleTap","quadTap"]`},
		{"label only", decision(1, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, labelOnly), ""},
		{"no binding", decision(1, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, nil), ""},
		{"a dispatch is not waiting", decision(1, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonDispatch, pressOnly), ""},
		{"only an unbound press waits", decision(1, control.South, engine.EdgeDown, engine.GestureTap, engine.ReasonUnbound, tapHold), ""},
		// The end of a multi-tap window with no action for its taps is the
		// answer to the wait, not a new one, even when the binding has more
		// multi-tap actions.
		{"the end of a window with no action for its taps is not waiting", decision(1, control.South, engine.EdgeDown, engine.GestureTripleTap, engine.ReasonUnbound, multi), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tf := newTestFeed(t, 8)
			tf.start(t)
			tf.decision(0, tc.d)
			got := tf.sink.take(t, 1)[0]
			if tc.want == "" {
				if strings.Contains(got, `"waiting"`) {
					t.Errorf("%s", got)
				}
			} else if !strings.Contains(got, tc.want) {
				t.Errorf("%s; want %s", got, tc.want)
			}
		})
	}
}

// waitedMs is the time from the latest press to the decision of a tap, a
// hold or a multi-tap: from the arrival of the button event when there was
// one, otherwise from the press decision.
func TestDecisionFeedWaitedMs(t *testing.T) {
	tapBinding := &profile.Binding{ID: "/b/tap", Tap: actionOf(profile.ActionKeys)}
	waited := func(s string) string {
		var ev struct {
			W *int64 `json:"waitedMs"`
		}
		if err := json.Unmarshal([]byte(s), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.W == nil {
			return "absent"
		}
		return time.Duration(*ev.W * int64(time.Millisecond)).String()
	}
	t.Run("a tap from the event of the press", func(t *testing.T) {
		tf := newTestFeed(t, 16)
		tf.start(t)
		tf.clk.Set(clock.Time(100 * time.Millisecond))
		tf.event(1, input.Event{Kind: input.ButtonDown, Control: "south"})
		tf.decision(1, decision(101, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, tapBinding))
		tf.decision(1, decision(180, control.South, engine.EdgeDown, engine.GestureTap, engine.ReasonDispatch, tapBinding))
		got := tf.sink.take(t, 2)
		if w := waited(got[0]); w != "absent" {
			t.Errorf("the press has waitedMs %s", w)
		}
		if w := waited(got[1]); w != "80ms" {
			t.Errorf("tap waited %s: %s", w, got[1])
		}
	})
	t.Run("the press decision is replayed late", func(t *testing.T) {
		// A press behind a pending gesture is decided when that one is: the
		// time to wait for is from the press the user made.
		tf := newTestFeed(t, 16)
		tf.start(t)
		tf.clk.Set(clock.Time(50 * time.Millisecond))
		tf.event(0, input.Event{Kind: input.ButtonDown, Control: "east"})
		tf.decision(0, decision(250, control.East, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, tapBinding))
		tf.decision(0, decision(260, control.East, engine.EdgeDown, engine.GestureTap, engine.ReasonDispatch, tapBinding))
		if w := waited(tf.sink.take(t, 2)[1]); w != "210ms" {
			t.Errorf("waited %s, want 210ms", w)
		}
	})
	t.Run("a virtual control from the press decision", func(t *testing.T) {
		tf := newTestFeed(t, 16)
		tf.start(t)
		tf.decision(0, decision(1000, control.LT, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, tapBinding))
		tf.decision(0, decision(1200, control.LT, engine.EdgeDown, engine.GestureHold, engine.ReasonDispatch, tapBinding))
		if w := waited(tf.sink.take(t, 2)[1]); w != "200ms" {
			t.Errorf("waited %s, want 200ms", w)
		}
	})
	t.Run("the latest press of a multi-tap", func(t *testing.T) {
		tf := newTestFeed(t, 16)
		tf.start(t)
		tf.clk.Set(clock.Time(0))
		tf.event(0, input.Event{Kind: input.ButtonDown, Control: "south"})
		tf.decision(0, decision(0, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, tapBinding))
		tf.clk.Set(clock.Time(120 * time.Millisecond))
		tf.event(0, input.Event{Kind: input.ButtonDown, Control: "south"})
		tf.decision(0, decision(120, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, tapBinding))
		tf.decision(0, decision(400, control.South, engine.EdgeDown, engine.GestureDoubleTap, engine.ReasonDispatch, tapBinding))
		if w := waited(tf.sink.take(t, 3)[2]); w != "280ms" {
			t.Errorf("waited %s, want 280ms", w)
		}
	})
	t.Run("a window that ends with no action for its taps", func(t *testing.T) {
		// A binding with only a double tap, tapped once: the wait for the second
		// tap ends at the end of the window, and the decision that says so counts
		// from the press, as the dispatch of a double tap does. It lists no
		// waiting: the press already did.
		onlyDouble := &profile.Binding{ID: "/b/double", DoubleTap: actionOf(profile.ActionKeys)}
		tf := newTestFeed(t, 16)
		tf.start(t)
		tf.clk.Set(clock.Time(100 * time.Millisecond))
		tf.event(0, input.Event{Kind: input.ButtonDown, Control: "south"})
		tf.decision(0, decision(101, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, onlyDouble))
		tf.decision(0, decision(350, control.South, engine.EdgeDown, engine.GestureTap, engine.ReasonUnbound, onlyDouble))
		got := tf.sink.take(t, 2)
		if !strings.Contains(got[0], `"waiting":["doubleTap"]`) {
			t.Errorf("the press does not list what it waits for: %s", got[0])
		}
		if w := waited(got[1]); w != "250ms" {
			t.Errorf("the end of the window waited %s, want 250ms: %s", w, got[1])
		}
		if strings.Contains(got[1], `"waiting"`) {
			t.Errorf("the end of the window is waiting: %s", got[1])
		}
	})
	t.Run("a window that ends after two taps counts from the latest press", func(t *testing.T) {
		tapTriple := &profile.Binding{ID: "/b/tapTriple", Tap: actionOf(profile.ActionKeys), TripleTap: actionOf(profile.ActionKeys)}
		tf := newTestFeed(t, 16)
		tf.start(t)
		tf.clk.Set(clock.Time(0))
		tf.event(0, input.Event{Kind: input.ButtonDown, Control: "south"})
		tf.decision(0, decision(0, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, tapTriple))
		tf.clk.Set(clock.Time(120 * time.Millisecond))
		tf.event(0, input.Event{Kind: input.ButtonDown, Control: "south"})
		tf.decision(0, decision(120, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, tapTriple))
		tf.decision(0, decision(370, control.South, engine.EdgeDown, engine.GestureDoubleTap, engine.ReasonUnbound, tapTriple))
		if w := waited(tf.sink.take(t, 3)[2]); w != "250ms" {
			t.Errorf("waited %s, want 250ms", w)
		}
	})
	// presses plays events of one control as "ms down|up" on slot 0.
	presses := func(tf *testFeed, c string, evs ...[2]int) {
		for _, e := range evs {
			tf.clk.Set(clock.Time(time.Duration(e[0]) * time.Millisecond))
			kind := input.ButtonUp
			if e[1] == 1 {
				kind = input.ButtonDown
			}
			tf.event(0, input.Event{Kind: kind, Control: c})
		}
	}
	t.Run("a control pressed twice before the first press is decided", func(t *testing.T) {
		// Both presses wait behind a pending gesture and are replayed at 300:
		// each one's time to wait for is from its own arrival.
		tf := newTestFeed(t, 16)
		tf.start(t)
		presses(tf, "east", [2]int{50, 1}, [2]int{80, 0}, [2]int{120, 1}, [2]int{150, 0})
		for range 2 {
			tf.decision(0, decision(300, control.East, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, tapBinding))
			tf.decision(0, decision(300, control.East, engine.EdgeDown, engine.GestureTap, engine.ReasonDispatch, tapBinding))
		}
		got := tf.sink.take(t, 4)
		if w0, w1 := waited(got[1]), waited(got[3]); w0 != "250ms" || w1 != "180ms" {
			t.Errorf("the first tap waited %s, the second %s; want 250ms and 180ms", w0, w1)
		}
	})
	t.Run("the queue of a control holds a few presses and then leaves the rest", func(t *testing.T) {
		tf := newTestFeed(t, 64)
		tf.start(t)
		n := feedPressDepth + 2
		for i := range n {
			presses(tf, "east", [2]int{10 * i, 1}, [2]int{10*i + 5, 0})
		}
		for range n {
			tf.decision(0, decision(1000, control.East, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, tapBinding))
			tf.decision(0, decision(1000, control.East, engine.EdgeDown, engine.GestureTap, engine.ReasonDispatch, tapBinding))
		}
		got := tf.sink.take(t, 2*n)
		for i := range feedPressDepth {
			if w, want := waited(got[2*i+1]), time.Duration(1000-10*i)*time.Millisecond; w != want.String() {
				t.Errorf("tap %d waited %s, want %s", i, w, want)
			}
		}
		// The presses past the queue are measured from their own decision.
		for i := feedPressDepth; i < n; i++ {
			if w := waited(got[2*i+1]); w != "0s" {
				t.Errorf("tap %d waited %s, want 0s", i, w)
			}
		}
	})
	t.Run("a press nobody decided is forgotten in time", func(t *testing.T) {
		// A buffered press that a pause or a reload dropped never gets its
		// decision: it must not shift the pairs of the presses after it.
		tf := newTestFeed(t, 16)
		tf.start(t)
		presses(tf, "east", [2]int{0, 1}, [2]int{20, 0}, [2]int{60000, 1}, [2]int{60040, 0})
		tf.decision(0, decision(60001, control.East, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, tapBinding))
		tf.decision(0, decision(60100, control.East, engine.EdgeDown, engine.GestureTap, engine.ReasonDispatch, tapBinding))
		if w := waited(tf.sink.take(t, 2)[1]); w != "100ms" {
			t.Errorf("waited %s, want 100ms", w)
		}
	})
	t.Run("a d-pad press is the latest of its events", func(t *testing.T) {
		// In the d-pad modes that merge directions, a press of the engine is not
		// one of one event: an old, unpaired event must not stand for it.
		tf := newTestFeed(t, 16)
		tf.start(t)
		presses(tf, "dpad.up", [2]int{50, 1}, [2]int{60, 0}, [2]int{120, 1})
		tf.decision(0, decision(130, control.DpadUp, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, tapBinding))
		tf.decision(0, decision(200, control.DpadUp, engine.EdgeDown, engine.GestureTap, engine.ReasonDispatch, tapBinding))
		if w := waited(tf.sink.take(t, 2)[1]); w != "80ms" {
			t.Errorf("waited %s, want 80ms", w)
		}
	})
	t.Run("a gamepad that goes away takes its presses along", func(t *testing.T) {
		tf := newTestFeed(t, 16)
		tf.start(t)
		presses(tf, "east", [2]int{50, 1})
		tf.event(0, input.Event{Kind: input.DeviceRemoved})
		tf.decision(0, decision(300, control.East, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, tapBinding))
		tf.decision(0, decision(380, control.East, engine.EdgeDown, engine.GestureTap, engine.ReasonDispatch, tapBinding))
		if w := waited(tf.sink.take(t, 2)[1]); w != "80ms" {
			t.Errorf("waited %s, want 80ms", w)
		}
	})
	t.Run("only the gestures that wait", func(t *testing.T) {
		tf := newTestFeed(t, 16)
		tf.start(t)
		tf.event(0, input.Event{Kind: input.ButtonDown, Control: "south"})
		tf.decision(0, decision(10, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonDispatch, tapBinding))
		tf.decision(0, decision(30, control.South, engine.EdgeUp, engine.GestureHold, engine.ReasonRelease, tapBinding))
		tf.decision(0, decision(31, control.South, engine.EdgeDown, engine.GestureRelease, engine.ReasonDispatch, tapBinding))
		for i, got := range tf.sink.take(t, 3) {
			if w := waited(got); w != "absent" {
				t.Errorf("decision %d has waitedMs %s: %s", i, w, got)
			}
		}
	})
	t.Run("a gesture with no press seen", func(t *testing.T) {
		tf := newTestFeed(t, 16)
		tf.start(t)
		tf.decision(0, decision(10, control.North, engine.EdgeDown, engine.GestureTap, engine.ReasonDispatch, tapBinding))
		if w := waited(tf.sink.take(t, 1)[0]); w != "absent" {
			t.Errorf("waited %s", w)
		}
	})
	t.Run("slots keep their own press times", func(t *testing.T) {
		tf := newTestFeed(t, 16)
		tf.start(t)
		tf.clk.Set(clock.Time(10 * time.Millisecond))
		tf.event(0, input.Event{Kind: input.ButtonDown, Control: "south"})
		tf.clk.Set(clock.Time(60 * time.Millisecond))
		tf.event(1, input.Event{Kind: input.ButtonDown, Control: "south"})
		tf.decision(0, decision(11, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, tapBinding))
		tf.decision(1, decision(61, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, tapBinding))
		tf.decision(1, decision(100, control.South, engine.EdgeDown, engine.GestureTap, engine.ReasonDispatch, tapBinding))
		tf.decision(0, decision(100, control.South, engine.EdgeDown, engine.GestureTap, engine.ReasonDispatch, tapBinding))
		got := tf.sink.take(t, 4)
		if w0, w1 := waited(got[2]), waited(got[3]); w0 != "40ms" || w1 != "90ms" {
			t.Errorf("slot 1 waited %s, slot 0 waited %s; want 40ms and 90ms", w0, w1)
		}
	})
	t.Run("a slot index past the slots is ignored", func(t *testing.T) {
		tf := newTestFeed(t, 16)
		tf.start(t)
		tf.event(200, input.Event{Kind: input.ButtonDown, Control: "south"})
		tf.decision(200, decision(1, control.South, engine.EdgeDown, engine.GestureTap, engine.ReasonDispatch, tapBinding))
		if w := waited(tf.sink.take(t, 1)[0]); w != "absent" {
			t.Errorf("waited %s", w)
		}
	})
}

// A script resolution binding is no entry of the profile: its ID is not sent,
// but the waiting gestures of it are, and its label is.
func TestDecisionFeedScriptResolution(t *testing.T) {
	tf := newTestFeed(t, 8)
	tf.start(t)
	b := &profile.Binding{ID: engine.ScriptResolutionID, Label: "撤销", Tap: actionOf(profile.ActionKeys)}
	tf.decision(0, decision(1, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, b))
	got := tf.sink.take(t, 1)[0]
	if strings.Contains(got, engine.ScriptResolutionID) || !strings.Contains(got, `"label":"撤销"`) || !strings.Contains(got, `"waiting":["tap"]`) {
		t.Errorf("%s", got)
	}
	if b.ID != engine.ScriptResolutionID {
		t.Errorf("the engine's binding was changed: %q", b.ID)
	}
}

// A full ring drops its oldest decision and counts it; the publisher goes on
// with the newest, in order.
func TestDecisionFeedDropsTheOldest(t *testing.T) {
	tf := newTestFeed(t, 4)
	for i := range 10 {
		tf.decision(0, decision(i, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, nil))
	}
	if n := tf.Dropped(); n != 6 {
		t.Errorf("dropped %d, want 6", n)
	}
	tf.start(t)
	got := tf.sink.take(t, 4)
	for i, want := range []string{`"t":6,`, `"t":7,`, `"t":8,`, `"t":9,`} {
		if !strings.Contains(got[i], want) {
			t.Errorf("event %d is %s; want %s", i, got[i], want)
		}
	}
	// The ring is free again.
	tf.decision(0, decision(10, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, nil))
	if got := tf.sink.take(t, 1); !strings.Contains(got[4], `"t":10,`) || tf.Dropped() != 6 {
		t.Errorf("after the drain: %v, dropped %d", got[4], tf.Dropped())
	}
}

// The unrouted event names the device, the control, the edge and why it maps
// to nothing, with the wall time.
func TestDecisionFeedUnrouted(t *testing.T) {
	tf := newTestFeed(t, 8)
	tf.start(t)
	tf.unrouted(input.Event{Kind: input.ButtonDown, Device: 7, Control: "east"}, "queued")
	tf.unrouted(input.Event{Kind: input.ButtonUp, Device: 7, Control: "east"}, "ignored")
	got := tf.sink.take(t, 2)
	want := []string{
		`{"kind":"unrouted","at":1792843200250,"device":7,"control":"east","down":true,"why":"queued"}`,
		`{"kind":"unrouted","at":1792843200250,"device":7,"control":"east","down":false,"why":"ignored"}`,
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d is %s, want %s", i, got[i], want[i])
		}
	}
}

// The unrouted events of a flood are limited, and the limit refills with time.
func TestDecisionFeedUnroutedRateLimit(t *testing.T) {
	tf := newTestFeed(t, 512)
	for range 500 {
		tf.unrouted(input.Event{Kind: input.ButtonDown, Device: 7, Control: "east"}, "queued")
	}
	if n := tf.pendingLen(); n < 1 || n > unroutedBurst {
		t.Fatalf("%d unrouted events let through a burst of 500; want 1 to %d", n, unroutedBurst)
	}
	before := tf.pendingLen()
	tf.wallAdd(time.Second)
	tf.unrouted(input.Event{Kind: input.ButtonDown, Device: 7, Control: "east"}, "queued")
	if tf.pendingLen() != before+1 {
		t.Errorf("after a second: %d pending, want %d", tf.pendingLen(), before+1)
	}
	// Decisions are not limited.
	for range 100 {
		tf.decision(0, decision(1, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, nil))
	}
	if tf.pendingLen() != before+101 {
		t.Errorf("%d pending after 100 decisions, want %d", tf.pendingLen(), before+101)
	}
}

// The slot's side of the feed allocates nothing: the decision is copied into
// the ring, and the JSON is the publisher's.
func TestDecisionFeedHandoverDoesNotAllocate(t *testing.T) {
	tf := newTestFeed(t, 8)
	b := &profile.Binding{ID: "/b", Tap: actionOf(profile.ActionKeys)}
	press := decision(1, control.South, engine.EdgeDown, engine.GesturePress, engine.ReasonUnbound, b)
	tap := decision(90, control.South, engine.EdgeDown, engine.GestureTap, engine.ReasonDispatch, b)
	ev := input.Event{Kind: input.ButtonDown, Control: "south"}
	if n := testing.AllocsPerRun(200, func() {
		tf.event(0, ev)
		tf.decision(0, press)
		tf.decision(0, tap)
	}); n != 0 {
		t.Errorf("%v allocations per press and tap", n)
	}
}

// waitedMs through the real slot: two presses of one control wait behind a
// pending hold of another and are decided together when it commits. Each tap
// is timed from its own press, not from the later one of the same control.
func TestDecisionFeedWaitedMsBehindAPendingGesture(t *testing.T) {
	p, ds := profile.Parse([]byte(`{"schemaVersion":0,"name":"t","layers":{"base":{
		"south":{"tap":{"type":"keys","keys":"F13"},"hold":{"type":"keys","keys":"F14"},"policy":"timeOnly","timing":{"holdMs":300}},
		"east":{"tap":{"type":"keys","keys":"F15"}}}}}`))
	if p == nil || ds.HasErrors() {
		t.Fatalf("profile: %v", ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	sim := simtest.New(t)
	tf := newTestFeed(t, 64)
	tf.decisionFeed.clk = sim.Clock
	s, err := slot.New(slot.Config{
		Index: 0, Gen: 1, Program: prog, Arbiter: sim.Arb, Clock: sim.Clock,
		Trace:   func(d engine.Decision) { tf.decision(0, d) },
		OnEvent: func(ev input.Event) { tf.event(0, ev) },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Dispose(); err != nil {
			t.Error(err)
		}
	})
	var evs []input.Event
	for _, e := range []struct {
		ms   int
		c    string
		down bool
	}{{0, "south", true}, {50, "east", true}, {80, "east", false}, {120, "east", true}, {150, "east", false}, {400, "south", false}} {
		kind := input.ButtonUp
		if e.down {
			kind = input.ButtonDown
		}
		evs = append(evs, input.Event{Kind: kind, Control: e.c, Time: uint64(e.ms) * uint64(time.Millisecond)})
	}
	if err := s.Replay(sim.Clock, evs); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	for _, it := range tf.pop(nil) {
		tf.publish(&it)
	}
	var taps []string
	for _, raw := range tf.sink.snapshot() {
		var ev struct {
			Decision struct {
				Control string `json:"control"`
				Gesture string `json:"gesture"`
				Reason  string `json:"reason"`
			} `json:"decision"`
			WaitedMs *int64 `json:"waitedMs"`
		}
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Decision.Control == "east" && ev.Decision.Gesture == "tap" {
			if ev.WaitedMs == nil {
				t.Fatalf("a tap without waitedMs: %s", raw)
			}
			taps = append(taps, time.Duration(*ev.WaitedMs*int64(time.Millisecond)).String())
		}
	}
	if want := []string{"250ms", "180ms"}; !slices.Equal(taps, want) {
		t.Errorf("east taps waited %v, want %v\n%s", taps, want, strings.Join(tf.sink.snapshot(), "\n"))
	}
}

// A binding with only a double tap, tapped once, through the real slot: the
// press is unbound and waiting for the double tap, and when the window runs out
// the slot decides the single tap, unbound again, with waitedMs from the press
// and nothing it waits for. The window of a second tap that another control's
// press ends is decided the same way.
func TestDecisionFeedUnmatchedMultiTapThroughTheSlot(t *testing.T) {
	p, ds := profile.Parse([]byte(`{"schemaVersion":0,"name":"t","layers":{"base":{
		"west":{"doubleTap":{"type":"keys","keys":"F14"}},
		"east":{"press":{"type":"keys","keys":"F15","mode":"tap"}}}}}`))
	if p == nil || ds.HasErrors() {
		t.Fatalf("profile: %v", ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	sim := simtest.New(t)
	tf := newTestFeed(t, 64)
	tf.decisionFeed.clk = sim.Clock
	s, err := slot.New(slot.Config{
		Index: 0, Gen: 1, Program: prog, Arbiter: sim.Arb, Clock: sim.Clock,
		Trace:   func(d engine.Decision) { tf.decision(0, d) },
		OnEvent: func(ev input.Event) { tf.event(0, ev) },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Dispose(); err != nil {
			t.Error(err)
		}
	})
	var evs []input.Event
	for _, e := range []struct {
		ms   int
		c    string
		down bool
	}{
		{0, "west", true}, {30, "west", false}, // the window runs out at 250
		{500, "west", true}, {530, "west", false}, {600, "east", true}, {630, "east", false}, // east's press ends it
	} {
		kind := input.ButtonUp
		if e.down {
			kind = input.ButtonDown
		}
		evs = append(evs, input.Event{Kind: kind, Control: e.c, Time: uint64(e.ms) * uint64(time.Millisecond)})
	}
	if err := s.Replay(sim.Clock, evs); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	for _, it := range tf.pop(nil) {
		tf.publish(&it)
	}
	type row struct {
		Gesture, Reason string
		Waiting         []string
		Waited          string
	}
	var rows []row
	for _, raw := range tf.sink.snapshot() {
		var ev struct {
			Decision struct {
				Control string `json:"control"`
				Gesture string `json:"gesture"`
				Reason  string `json:"reason"`
			} `json:"decision"`
			Waiting  []string `json:"waiting"`
			WaitedMs *int64   `json:"waitedMs"`
		}
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Decision.Control != "west" {
			continue
		}
		r := row{Gesture: ev.Decision.Gesture, Reason: ev.Decision.Reason, Waiting: ev.Waiting}
		if ev.WaitedMs != nil {
			r.Waited = time.Duration(*ev.WaitedMs * int64(time.Millisecond)).String()
		}
		rows = append(rows, r)
	}
	want := []row{
		{"press", "unbound", []string{"doubleTap"}, ""},
		{"tap", "unbound", nil, "250ms"},
		{"press", "unbound", []string{"doubleTap"}, ""},
		{"tap", "unbound", nil, "100ms"},
	}
	if !slices.EqualFunc(rows, want, func(a, b row) bool {
		return a.Gesture == b.Gesture && a.Reason == b.Reason && slices.Equal(a.Waiting, b.Waiting) && a.Waited == b.Waited
	}) {
		t.Errorf("west's decisions\n%+v\nwant\n%+v\n%s", rows, want, strings.Join(tf.sink.snapshot(), "\n"))
	}
}
