package supervisor

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/logging"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

const (
	// backoffBase is the wait before the restart after a panic; it doubles
	// with each earlier panic in panicWindow.
	backoffBase = 100 * time.Millisecond
	// maxPanics panics in panicWindow disable a slot.
	maxPanics   = 3
	panicWindow = 10 * time.Second
	// errorEvery is how often a slot error with one message is logged.
	errorEvery = time.Second
)

// errGoexit is the error of a slot loop that runtime.Goexit ended.
var errGoexit = errors.New("supervisor: runtime.Goexit in the slot loop")

// runner supervises one slot: it runs slot.RunLoop on a goroutine of its
// own, one incarnation of the slot after another, and sends it the
// barriers of Sync and Reload as jobs, so that the router never waits for
// a slot.
//
// The runner goroutine owns the queue of barriers, the panic history, the
// program and generation of the next incarnation, and log; each loop
// goroutine owns its Slot and the limiter while it runs, and the next
// starts only after it ended.
type runner struct {
	s     *Supervisor
	index uint8
	edges chan slot.Edge // the router sends; Run closes it
	jobs  chan func(*slot.Slot)
	base  *slog.Logger // with the slot's index
	// The program and generation a fresh slot starts with: Config's, then
	// those of the last Reload sent to a slot loop or settled (record);
	// log has the generation.
	prog       *engine.Program
	gen        uint32
	profileKey string
	log        *slog.Logger
	limiter    *logging.Limiter

	mu    sync.Mutex
	inbox []message     // from the router
	wake  chan struct{} // signaled after each post
}

// message is what the router posts to a runner: a barrier, or the
// enabling of a disabled slot.
type message struct {
	b      *barrier
	enable bool
}

// loopResult is how an incarnation of the slot loop ended.
type loopResult struct {
	err    error // RunLoop's
	goexit bool  // RunLoop did not return
}

func newRunner(s *Supervisor, index uint8) *runner {
	base := s.log.With("slot", index)
	return &runner{
		s: s, index: index,
		edges: make(chan slot.Edge, s.cfg.EdgeBuffer),
		jobs:  make(chan func(*slot.Slot)),
		base:  base,
		prog:  s.cfg.Program, gen: firstGen,
		profileKey: s.cfg.ProfileKey,
		log:        base.With("gen", firstGen),
		limiter:    logging.NewLimiter(s.cfg.Clock, errorEvery),
		wake:       make(chan struct{}, 1),
	}
}

// post hands m to the runner without waiting for it.
func (r *runner) post(m message) {
	r.mu.Lock()
	r.inbox = append(r.inbox, m)
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// take returns the posted messages.
func (r *runner) take() []message {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.inbox
	r.inbox = nil
	return m
}

// supervise runs the runner. A panic or runtime.Goexit of the runner
// itself, not of the slot loop, ends Run with a *PanicError: the slot
// loops then release their keys as at every end of Run (invariant 1). It
// logs nothing, since the logger may be what panicked.
func (r *runner) supervise() {
	returned := false
	defer func() {
		if returned {
			return
		}
		var v any = "runtime.Goexit"
		if p := recover(); p != nil {
			v = p
		}
		r.s.fatal <- &PanicError{Slot: r.index, Value: v, Stack: debug.Stack()}
	}()
	r.run()
	returned = true
}

// run supervises the slot until Run stops it.
func (r *runner) run() {
	clk := r.s.cfg.Clock
	var (
		queue  []*barrier   // unfinished barriers, in order
		sent   int          // of queue, how many the running loop took
		panics []clock.Time // within panicWindow
	)
	for {
		sl, err := r.newSlot()
		if err != nil {
			// New checked the Config, so this is a bug; the slot stays off.
			r.log.Error("slot not created", "err", err)
			var ok bool
			if queue, ok = r.disabled(queue); !ok {
				return
			}
			panics = nil
			continue
		}
		done := make(chan loopResult, 1)
		r.s.loops.Go(func() { r.loop(sl, done) })

		var res loopResult
	running:
		for {
			queue, sent = prune(queue, sent)
			var jobs chan<- func(*slot.Slot)
			var job func(*slot.Slot)
			if sent < len(queue) {
				jobs, job = r.jobs, r.job(queue[sent])
			}
			select {
			case res = <-done:
				break running
			case <-r.wake:
				queue = r.queue(queue, r.take())
			case jobs <- job:
				r.record(queue[sent])
				sent++
			}
		}
		// The jobs the loop took but did not run go to the next one, but for
		// the reloads, which finish once recorded (settle): it starts with
		// the program of the last.
		sent = 0
		if res.err == nil && !res.goexit {
			return // Run closed the edge channel
		}
		if res.goexit {
			res.err = errGoexit
		}
		var stack []byte
		if pe, ok := errors.AsType[*slot.PanicError](res.err); ok {
			stack = pe.Stack
		}
		now := clk.Now()
		panics = slices.DeleteFunc(panics, func(t clock.Time) bool { return now.Sub(t) >= panicWindow })
		panics = append(panics, now)
		if len(panics) >= maxPanics {
			r.log.Error("slot panicked", "err", res.err, "stack", string(stack))
			r.log.Error("slot disabled", "panics", len(panics), "window", panicWindow)
			var ok bool
			if queue, ok = r.disabled(queue); !ok {
				return
			}
			panics = nil
			continue
		}
		backoff := backoffBase << (len(panics) - 1)
		// The timer first: a test that sees the log line may move the clock.
		t := clk.NewTimer(backoff)
		r.log.Error("slot panicked", "err", res.err, "stack", string(stack), "backoff", backoff)
		queue = r.settle(queue)
		if !r.wait(t, &queue) {
			return
		}
		r.s.update(r.index, func(st *SlotStatus) { st.Restarts++ })
	}
}

// newSlot returns a fresh slot, every control up, with the program
// recorded, paused while the mapping is held, by the user's pause or the
// system's suspension: the router stores the hold before it delivers its
// edges, and Run stores it before the first slots, so the Edge.Pause edges
// queued for the slot leave it as the last change did. The edges queued
// before the mapping was last let go carry PauseOn when they came while it
// was held (router.deliver), so the slot still handles them paused.
func (r *runner) newSlot() (*slot.Slot, error) {
	cfg := r.s.cfg
	sc := slot.Config{
		Index: r.index, Gen: r.gen, Program: r.prog, Arbiter: cfg.Output.Remote(), Clock: cfg.Clock,
		Paused:     r.s.held.Load(),
		ProfileKey: r.profileKey, LayerMemory: cfg.LayerMemory,
		Calibration: cfg.Calibration, Power: r.s.powerOf, Switches: cfg.Switches,
	}
	if f := cfg.ScriptFactory; f != nil {
		runtime, err := f(r.index, r.gen, cfg.Clock, cfg.Output.Remote())
		if err != nil {
			return nil, err
		}
		sc.Script = runtime
		if binder, ok := runtime.(interface {
			BindProfileSession(*script.SessionStore, string)
		}); ok {
			binder.BindProfileSession(&r.s.sessions, r.profileKey)
		}
	}
	index := r.index
	if f := cfg.ProfileSwitch; f != nil {
		sc.ProfileSwitch = func(gen uint32, id string) error { return f(index, gen, id) }
	}
	if f := cfg.Trace; f != nil {
		sc.Trace = func(d engine.Decision) { f(index, d) }
	}
	if f := cfg.OnEvent; f != nil {
		sc.OnEvent = func(e input.Event) { f(index, e) }
	}
	if f := cfg.OnLayerChange; f != nil {
		sc.OnLayerChange = func(s engine.LayerState) { f(index, s) }
	}
	if f := cfg.OnFeedbackDevice; f != nil {
		sc.FeedbackDevice = func(id input.DeviceID, x engine.Feedback) { f(index, id, x) }
	} else if f := cfg.OnFeedback; f != nil {
		sc.Feedback = func(x engine.Feedback) { f(index, x) }
	}
	if f := cfg.OnHapticDevice; f != nil {
		sc.OnHaptic = func(id input.DeviceID, h engine.Haptic) { f(index, id, h) }
	}
	return slot.New(sc)
}

// loop runs one incarnation of the slot loop and reports how it ended,
// a runtime.Goexit included.
func (r *runner) loop(sl *slot.Slot, done chan<- loopResult) {
	res := loopResult{goexit: true}
	defer func() { done <- res }()
	res.err = slot.RunLoop(context.Background(), slot.LoopConfig{
		Slot: sl, Edges: r.edges, Jobs: r.jobs, Lock: r.s.cfg.Lock,
		Errors: func(errs []error) { r.report(sl, errs) },
	})
	res.goexit = false
}

// report logs the errors of sl, each message at most once per second. It
// runs on the loop goroutine.
func (r *runner) report(sl *slot.Slot, errs []error) {
	for _, err := range errs {
		if ok, n := r.limiter.Allow(err.Error()); ok {
			r.base.Warn("slot error", "gen", sl.Gen(), "err", err, "suppressed", n)
		}
	}
}

// job returns the job of b for the slot loop. The runner records the
// program of a Reload as it sends the job: a slot started after this one
// starts with it.
func (r *runner) job(b *barrier) func(*slot.Slot) {
	if !b.isReload() {
		return b.run
	}
	return func(sl *slot.Slot) { r.reload(sl, b) }
}

// reload switches sl to the program of the Reload barrier b, unless it
// started with that one or a later one, and finishes b once Status shows
// the slot's generation. It runs on the loop goroutine.
func (r *runner) reload(sl *slot.Slot, b *barrier) {
	if sl.Gen() < b.gen {
		sl.SetProfileKey(b.profileKey)
		if f := r.s.cfg.OnReload; f != nil {
			f(r.index, b.gen)
		}
		if err := sl.Reload(b.prog, b.gen); err != nil {
			r.base.Error("slot not reloaded", "gen", sl.Gen(), "err", err) // a bug: the router checks both
		} else {
			r.base.Debug("slot reloaded", "gen", b.gen)
		}
	}
	gen := sl.Gen()
	r.s.update(r.index, func(st *SlotStatus) { st.Gen = gen })
	b.finish()
}

// record makes the program of the Reload barrier b the one a fresh slot
// starts with. Barriers come in the order of their generations.
func (r *runner) record(b *barrier) {
	if b.isReload() && b.gen > r.gen {
		r.prog, r.gen = b.prog, b.gen
		r.profileKey = b.profileKey
		r.log = r.base.With("gen", b.gen)
	}
}

// settle records the Reload barriers of q, with no slot loop to run them:
// the next slot starts with the last program. They finish once Status
// shows its generation. It returns the other barriers, those of Sync.
func (r *runner) settle(q []*barrier) []*barrier {
	rest := q[:0]
	var reloads []*barrier
	for _, b := range q {
		if !b.isReload() {
			rest = append(rest, b)
			continue
		}
		r.record(b)
		reloads = append(reloads, b)
	}
	if len(reloads) > 0 {
		gen := r.gen
		r.s.update(r.index, func(st *SlotStatus) { st.Gen = gen })
		for _, b := range reloads {
			b.finish()
		}
	}
	return rest
}

// wait waits for the backoff timer t, queueing the barriers posted
// meanwhile; those of a Reload finish at once (settle). It returns false
// when Run stops the runner.
func (r *runner) wait(t clock.Timer, queue *[]*barrier) bool {
	for {
		select {
		case <-t.C():
			return true
		case <-r.s.stop:
			t.Stop()
			return false
		case <-r.wake:
			*queue = r.settle(r.queue(*queue, r.take()))
		}
	}
}

// disabled marks the slot disabled, tells the router, and waits until the
// router enables it again. A disabled slot has nothing for a Sync to wait
// for: the barriers queued, and those posted before the enable, finish at
// once, those of a Reload once recorded (settle). It returns the barriers
// posted after the enable, which the fresh slot runs, and false when Run
// stops the runner instead.
func (r *runner) disabled(queue []*barrier) ([]*barrier, bool) {
	r.s.update(r.index, func(st *SlotStatus) { st.Disabled = true })
	finishAll(r.settle(queue))
	select {
	case r.s.notes <- r.index:
	case <-r.s.stop:
		return nil, false
	}
	var after []*barrier
	enabled := false
	for !enabled {
		select {
		case <-r.s.stop:
			return nil, false
		case <-r.wake:
		}
		msgs := r.take()
		i := slices.IndexFunc(msgs, func(m message) bool { return m.enable })
		if i >= 0 {
			enabled = true
			after = r.queue(nil, msgs[i+1:])
			msgs = msgs[:i]
		}
		finishAll(r.settle(r.queue(nil, msgs)))
	}
	r.s.update(r.index, func(st *SlotStatus) { st.Restarts, st.Disabled = 0, false })
	r.log.Info("slot enabled")
	return after, true
}

// finishAll finishes the barriers of q.
func finishAll(q []*barrier) {
	for _, b := range q {
		b.finish()
	}
}

// queue appends the barriers of msgs to q. Enables come only to a
// disabled slot (see disabled).
func (r *runner) queue(q []*barrier, msgs []message) []*barrier {
	for _, m := range msgs {
		if m.b != nil {
			q = append(q, m.b)
		}
	}
	return q
}

// prune drops the finished barriers at the front of q: the loop runs the
// jobs it takes in order.
func prune(q []*barrier, sent int) ([]*barrier, int) {
	for len(q) > 0 && q[0].finished() {
		q = q[1:]
		if sent > 0 {
			sent--
		}
	}
	return q, sent
}
