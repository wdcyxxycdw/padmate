package main

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

// eventSink is where a decisionFeed publishes: the IPC Hub.
type eventSink interface {
	Listening(topic string) bool
	Publish(topic string, data any)
}

const (
	// decisionFeedSize is how many decisions and unrouted events wait for the
	// publisher: about two seconds of the busiest realistic play. When it is
	// full the oldest is dropped.
	decisionFeedSize = 512
	// unroutedBurst and unroutedPerSecond limit the unrouted events (ADR-0071):
	// a gamepad that no slot maps is pressed by a person, and a faulty one must
	// not fill the feed.
	unroutedBurst     = 16
	unroutedPerSecond = 16
	// feedDropLogEvery bounds how often drops are logged.
	feedDropLogEvery = 10 * time.Second
	// feedPressDepth is how many presses of one control the feed keeps the
	// arrival of while their decisions wait behind a pending gesture. A person
	// does not press a button more often than this within one hold threshold;
	// the presses past it are timed from their own decision.
	feedPressDepth = 8
	// feedPressTTL is how long an arrival waits for its decision. A press that
	// a pause, a reload or a d-pad mode never decides would shift every pair
	// after it; it is forgotten after this. A press decided later than this
	// (a hold threshold of more than five seconds) is timed from its own
	// decision.
	feedPressTTL = 5 * time.Second
)

// A feedItem is one event waiting for the publisher.
type feedItem struct {
	kind feedKind
	at   int64 // Unix ms of the wall clock, when the slot or router handed it over
	// A decision: the slot, the decision, the gestures its binding still waits
	// for (a bit per profile.Gesture) and the wait for a tap, hold or
	// multi-tap in ms, -1 when it has none.
	slot    uint8
	d       engine.Decision
	waiting uint8
	waited  int64
	// An unrouted event: the gamepad, the control, its edge and the reason.
	device  input.DeviceID
	control string
	down    bool
	why     string
}

type feedKind uint8

const (
	feedDecision feedKind = iota + 1
	feedUnrouted
)

// decisionFeed gets the engine's decisions (ENG-20) from the slots, and the
// button edges of gamepads that no slot maps from the router, and publishes
// them as engine.activity events for the diagnostics page (ADR-0071).
//
// The slots and the router only copy a value into a bounded ring: a slot
// goroutine must not encode JSON before its output goes out. A publisher
// goroutine encodes and publishes them in order. When the ring is full the
// oldest is dropped and counted (Dropped). Nothing is queued while no client
// listens to engine.activity.
//
// It also adds what the engine does not know: the wall time of the decision,
// the gestures an unbound press waits for, and the time a tap, hold or
// multi-tap waited since the press (waitedMs).
type decisionFeed struct {
	sink eventSink
	log  *slog.Logger
	clk  clock.Clock
	// wall tells the wall clock; a test sets it.
	wall func() time.Time

	mu   sync.Mutex
	ring []feedItem
	head int
	n    int
	// tokens and last are the unrouted rate limit.
	tokens float64
	last   time.Time

	wake    chan struct{}
	dropped atomic.Uint64

	// rows hold, for each slot, when its button events arrived and when its
	// presses began.
	rows [supervisor.MaxSlots]pressTimes
}

// pressTimes is what the feed knows of the presses of one slot. Only the
// slot's goroutine uses it; the lock keeps the race detector, and a slot that
// is replaced while the old goroutine winds down, honest. It is a leaf: nothing
// is called with it held.
type pressTimes struct {
	mu sync.Mutex
	// arrived is, for each control, when the button events that press it
	// arrived on the clock and no decision has taken yet, oldest first: the
	// physical presses. Presses that wait behind a pending gesture are
	// decided at its commit, in the order they came, so the oldest arrival
	// belongs to the next press decision.
	arrived [control.Count]pressQueue
	// start is the time, plus one (0 is none), that the latest press of each
	// control began, as the decision of its press found it: the arrival of
	// the event when there was one, the press decision's own time otherwise
	// (a stick, a trigger or the d-pad's diagonals have no button event).
	start [control.Count]int64
}

// reset forgets everything: the gamepad was added or removed, so the engine
// starts over, and the presses it had buffered are gone.
func (p *pressTimes) reset() {
	p.arrived = [control.Count]pressQueue{}
	p.start = [control.Count]int64{}
}

// A pressQueue is a short queue of arrival times, oldest first.
type pressQueue struct {
	n int
	t [feedPressDepth]clock.Time
}

// push adds the arrival t. A full queue leaves the newest out, so the presses
// in it keep their pairs. A queue that keeps only the latest arrival (latest)
// is for the d-pad directions: in the modes that merge them, a press of the
// engine is not one press of one event (a diagonal takes two of them), so an
// arrival that has no decision must not stand for a later press.
func (q *pressQueue) push(t clock.Time, latest bool) {
	switch {
	case latest:
		q.t[0], q.n = t, 1
	case q.n < len(q.t):
		q.t[q.n] = t
		q.n++
	}
}

// expire forgets the arrivals before since.
func (q *pressQueue) expire(since clock.Time) {
	i := 0
	for i < q.n && q.t[i] < since {
		i++
	}
	if i > 0 {
		q.n = copy(q.t[:], q.t[i:q.n])
	}
}

// take removes and returns the oldest arrival that is not before since.
func (q *pressQueue) take(since clock.Time) (clock.Time, bool) {
	q.expire(since)
	if q.n == 0 {
		return 0, false
	}
	t := q.t[0]
	q.n = copy(q.t[:], q.t[1:q.n])
	return t, true
}

// isDpadDirection reports whether c is one of the four physical d-pad buttons.
func isDpadDirection(c control.Control) bool {
	return c == control.DpadUp || c == control.DpadDown || c == control.DpadLeft || c == control.DpadRight
}

func newDecisionFeed(sink eventSink, clk clock.Clock, log *slog.Logger, size int) *decisionFeed {
	if size <= 0 {
		size = decisionFeedSize
	}
	return &decisionFeed{
		sink: sink, clk: clk, log: log, wall: time.Now,
		ring: make([]feedItem, size), wake: make(chan struct{}, 1),
		tokens: unroutedBurst,
	}
}

// Dropped is how many events the feed dropped for want of room.
func (f *decisionFeed) Dropped() uint64 { return f.dropped.Load() }

// pendingLen is how many events wait for the publisher.
func (f *decisionFeed) pendingLen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

// event notes the arrival of ev on slot, the supervisor's OnEvent: the time
// of a physical press, for waitedMs. It runs on the slot's goroutine.
func (f *decisionFeed) event(slot uint8, ev input.Event) {
	if int(slot) >= len(f.rows) {
		return
	}
	row := &f.rows[slot]
	switch ev.Kind {
	case input.DeviceAdded, input.DeviceRemoved:
		row.mu.Lock()
		row.reset()
		row.mu.Unlock()
	case input.ButtonDown:
		c, ok := control.Parse(ev.Control)
		if !ok {
			return
		}
		now := f.clk.Now()
		row.mu.Lock()
		q := &row.arrived[c]
		q.expire(now - clock.Time(feedPressTTL))
		q.push(now, isDpadDirection(c))
		row.mu.Unlock()
	}
}

// decision takes d, the Trace of slot, on the slot's goroutine.
func (f *decisionFeed) decision(slot uint8, d engine.Decision) {
	waited := int64(-1)
	if int(slot) < len(f.rows) && d.Control.IsValid() {
		row := &f.rows[slot]
		switch {
		case d.Edge == engine.EdgeDown && d.Gesture == engine.GesturePress:
			// The oldest arrival of the control is this press, however late
			// a pending gesture made the engine decide it.
			t := d.T
			row.mu.Lock()
			if a, ok := row.arrived[d.Control].take(d.T - clock.Time(feedPressTTL)); ok {
				t = a
			}
			row.start[d.Control] = int64(t) + 1
			row.mu.Unlock()
		case d.Edge == engine.EdgeDown && waitsForPress(d.Gesture):
			row.mu.Lock()
			s := row.start[d.Control]
			row.mu.Unlock()
			if s != 0 {
				waited = max(0, (int64(d.T)-(s-1)+int64(time.Millisecond)/2)/int64(time.Millisecond))
			}
		}
	}
	if !f.sink.Listening("engine.activity") {
		return
	}
	it := feedItem{kind: feedDecision, slot: slot, d: d, waited: waited, at: f.wall().UnixMilli()}
	if d.Reason == engine.ReasonUnbound && d.Gesture == engine.GesturePress {
		it.waiting = waitingGestures(d.Binding)
	}
	f.push(it)
}

// waitsForPress reports whether the decision of g counts from a press: a
// tap, a multi-tap or a hold is known only some time after the press.
func waitsForPress(g engine.Gesture) bool {
	switch g {
	case engine.GestureTap, engine.GestureDoubleTap, engine.GestureTripleTap, engine.GestureQuadTap, engine.GestureHold:
		return true
	}
	return false
}

// waitingGestures is a bit per gesture, by profile.Gesture, that b has an
// action for and a press alone does not run: the press only starts the wait
// for it.
func waitingGestures(b *profile.Binding) uint8 {
	var mask uint8
	if b == nil {
		return 0
	}
	for g := engine.GestureRelease; g <= engine.GestureHold; g++ {
		if b.Action(g) != nil {
			mask |= 1 << g
		}
	}
	return mask
}

// waitingNames names the gestures of a waitingGestures mask, in the order of
// profile.Gesture; nil for none.
func waitingNames(mask uint8) []string {
	var names []string
	for g := engine.GestureRelease; g <= engine.GestureHold; g++ {
		if mask&(1<<g) != 0 {
			names = append(names, g.String())
		}
	}
	return names
}

// unrouted takes a button edge ev of a gamepad that no slot maps, with why
// (supervisor's Unrouted reasons), on the router's goroutine. At most
// unroutedPerSecond of them are let through, with a burst of unroutedBurst.
func (f *decisionFeed) unrouted(ev input.Event, why string) {
	if !f.sink.Listening("engine.activity") {
		return
	}
	now := f.wall()
	f.mu.Lock()
	if !f.last.IsZero() {
		f.tokens = min(unroutedBurst, f.tokens+now.Sub(f.last).Seconds()*unroutedPerSecond)
	}
	f.last = now
	if f.tokens < 1 {
		f.mu.Unlock()
		return
	}
	f.tokens--
	f.mu.Unlock()
	f.push(feedItem{kind: feedUnrouted, at: now.UnixMilli(), device: ev.Device, control: ev.Control, down: ev.Kind == input.ButtonDown, why: why})
}

// push queues it, dropping the oldest item when the ring is full.
func (f *decisionFeed) push(it feedItem) {
	f.mu.Lock()
	if f.n == len(f.ring) {
		f.head = (f.head + 1) % len(f.ring)
		f.n--
		f.dropped.Add(1)
	}
	f.ring[(f.head+f.n)%len(f.ring)] = it
	f.n++
	f.mu.Unlock()
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// pop moves the queued items into batch, oldest first.
func (f *decisionFeed) pop(batch []feedItem) []feedItem {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ; f.n > 0; f.n-- {
		batch = append(batch, f.ring[f.head])
		f.ring[f.head] = feedItem{} // let go of the binding
		f.head = (f.head + 1) % len(f.ring)
	}
	return batch
}

// run publishes the items as they come, until ctx is done.
func (f *decisionFeed) run(ctx context.Context) {
	var batch []feedItem
	var logged uint64
	var loggedAt time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.wake:
		}
		batch = f.pop(batch[:0])
		for i := range batch {
			f.publish(&batch[i])
		}
		if n := f.dropped.Load(); n != logged && time.Since(loggedAt) >= feedDropLogEvery {
			f.log.Warn("decision events dropped: the publisher was too slow", "dropped", n-logged, "total", n)
			logged, loggedAt = n, time.Now()
		}
	}
}

// rpcDecisionEvent is the engine.activity event of a decision. at, waiting and
// waitedMs are the daemon's, not the engine's (ADR-0071): at is the Unix
// time in ms, waiting the gestures an unbound press waits for, waitedMs the
// ms from the latest press to the decision of a tap, hold or multi-tap.
type rpcDecisionEvent struct {
	Kind         string          `json:"kind"`
	Slot         uint8           `json:"slot"`
	Decision     engine.Decision `json:"decision"`
	LatchBinding bool            `json:"latchBinding"`
	At           int64           `json:"at"`
	Waiting      []string        `json:"waiting,omitempty"`
	WaitedMs     *int64          `json:"waitedMs,omitempty"`
}

// rpcUnroutedEvent is the engine.activity event of a button edge of a
// gamepad that no slot maps. why is queued (it waits for a slot), ignored
// (the device filter or the ignore setting leaves it out), notGamepad (no
// standard mapping) or disabled (its slot was disabled after repeated
// errors).
type rpcUnroutedEvent struct {
	Kind    string         `json:"kind"`
	At      int64          `json:"at"`
	Device  input.DeviceID `json:"device"`
	Control string         `json:"control"`
	Down    bool           `json:"down"`
	Why     string         `json:"why"`
}

func (f *decisionFeed) publish(it *feedItem) {
	switch it.kind {
	case feedDecision:
		d := it.d
		ev := rpcDecisionEvent{Kind: "decision", Slot: it.slot, Decision: d, LatchBinding: d.Binding != nil && d.Binding.Latch, At: it.at, Waiting: waitingNames(it.waiting)}
		if it.waited >= 0 {
			ev.WaitedMs = &it.waited
		}
		if b := d.Binding; b != nil && b.ID == engine.ScriptResolutionID {
			// The engine's binding for what a script's handlers and the
			// declarations answer to a control is no entry of the profile.
			c := *b
			c.ID = ""
			ev.Decision.Binding = &c
		}
		f.sink.Publish("engine.activity", ev)
	case feedUnrouted:
		f.sink.Publish("engine.activity", rpcUnroutedEvent{Kind: "unrouted", At: it.at, Device: it.device, Control: it.control, Down: it.down, Why: it.why})
	}
}

// startDecisionFeed starts the publisher goroutine of r.decisions. It stops
// with r.end, or with stopDecisionFeed.
func (r *liveRun) startDecisionFeed() {
	ctx, cancel := context.WithCancel(r.end)
	r.decisionsStop = cancel
	r.decisionsDone = make(chan struct{})
	go func() {
		defer close(r.decisionsDone)
		defer func() {
			if v := recover(); v != nil {
				r.log.Error("decision feed stopped", "panic", v)
			}
		}()
		r.decisions.run(ctx)
	}()
}

// stopDecisionFeed stops the publisher and waits for it, watchStopWait at
// most. Events still queued are not published: the clients are going away.
func (r *liveRun) stopDecisionFeed() {
	if r.decisionsStop == nil {
		return
	}
	r.decisionsStop()
	select {
	case <-r.decisionsDone:
	case <-time.After(watchStopWait):
		r.log.Debug("decision feed did not stop before shutdown")
	}
}
