package winsession

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/winfocus"
)

// closeWait bounds how long Close waits for the session thread and the
// fallback to end.
const closeWait = 2 * time.Second

// Watcher is the session thread T-session and what it learned: the
// reasons the mapping is suspended for. Start makes one; Close ends it.
//
// When the session thread ends by itself (GetMessageW fails, or it
// panics outside its window procedure), the reasons are not cleared: a
// fallback goroutine takes the Tracker over, clears Sleep and End, which
// only messages report, and queries the session and the desktop every
// Poll until Close, so that locking and the secure desktop still suspend
// the mapping. Degraded is closed and Err reports why from then on.
type Watcher struct {
	cfg     Config
	log     *slog.Logger
	q       Querier
	tracker *Tracker
	// wakePending retries an uncompleted wake recheck on a later tick.
	// wakeGen prevents that retry from clearing a newer Sleep signal.
	wakePending bool
	wakeGen     uint32
	wakeID      uint32        // distinguishes duplicate wakes at the same Sleep generation
	signalGen   uint64        // every raw signal invalidates an in-flight wake query
	reasons     atomic.Uint32 // a copy of tracker.Reasons() for Reasons

	hwnd  uintptr           // the session window; set before Start returns
	focus *winfocus.Watcher // owned exclusively by the session thread

	closing   chan struct{} // closed by Close
	closeOnce sync.Once
	closeErr  error

	threadDone chan struct{} // closed when the session thread has ended
	degraded   chan struct{} // closed when the fallback takes over
	threadErr  error         // why the thread ended; set before degraded is closed
	done       chan struct{} // closed when everything has ended after Close

	// The session thread's own state. The fallback, which starts after the
	// thread ended, reads only panics.
	registered bool // WTSRegisterSessionNotification succeeded
	attempts   int  // registration attempts so far
	panics     int  // panics recovered in handlers
}

func newWatcher(cfg Config) *Watcher {
	cfg = cfg.withDefaults()
	return &Watcher{
		cfg:        cfg,
		log:        cfg.Logger,
		q:          cfg.Query,
		tracker:    NewTracker(cfg.StaleAfter),
		closing:    make(chan struct{}),
		threadDone: make(chan struct{}),
		degraded:   make(chan struct{}),
		done:       make(chan struct{}),
	}
}

// Close ends the session thread, or the fallback, and waits for it up to
// 2 s. It returns an error when they did not end in time, and otherwise
// Err: nil unless the session thread had ended by itself. Later calls
// return the same. Close must not be called from Config's callbacks: they
// run on the session thread, which Close waits for.
func (w *Watcher) Close() error {
	w.closeOnce.Do(func() {
		close(w.closing)
		w.closeErr = w.stop()
	})
	return w.closeErr
}

// wait waits for Done up to closeWait after the stop request.
func (w *Watcher) wait(postErr error) error {
	t := time.NewTimer(closeWait)
	defer t.Stop()
	select {
	case <-w.done:
		return w.Err()
	case <-t.C:
		return errors.Join(errors.New("winsession: the session thread did not end"), postErr)
	}
}

// Done is closed once Close has ended everything the Watcher runs.
func (w *Watcher) Done() <-chan struct{} { return w.done }

// Degraded is closed when the session thread ended by itself and the
// fallback took over: from then on the reasons are corrected only by the
// queries of every Poll, and sleep and the end of the session go unseen.
func (w *Watcher) Degraded() <-chan struct{} { return w.degraded }

// Err returns why the session thread ended by itself, once Degraded is
// closed, and nil before, or when Close ended it.
func (w *Watcher) Err() error {
	select {
	case <-w.degraded:
		return w.threadErr
	default:
		return nil
	}
}

// Reasons returns the reasons the mapping is suspended for, as the last
// Change reported them.
func (w *Watcher) Reasons() Reason { return Reason(w.reasons.Load()) }

// Window returns the session window's handle (an HWND), for tests that
// send it messages (winsessiontest).
func (w *Watcher) Window() uintptr { return w.hwnd }

// isClosing reports whether Close has been called.
func (w *Watcher) isClosing() bool {
	select {
	case <-w.closing:
		return true
	default:
		return false
	}
}

// step applies sig to the Tracker at the current awake time, querying q
// when not nil, and reports the change.
func (w *Watcher) step(sig Signal, q Querier) {
	defer w.resync(sig)
	w.report(w.tracker.Step(sig, w.cfg.awake(), q))
	if sig == SigTick && w.wakePending && q != nil {
		w.recheckStep(SigWake, w.wakeGen, w.wakeID)
	}
}

// resync, deferred by the steps, reports the Tracker's reasons when the
// step panicked, and panics on. A Querier's panic leaves the step half
// done (Tracker); unreported, what it did would be told to nobody, and the
// next step would find nothing to change. What the step's stale guard
// cleared is reported too (Tracker.LastStale), so that it is warned of.
// Wake queries use a private candidate, so their failures instead report
// the owned Tracker's latest state, retaining any newer raw signal.
func (w *Watcher) resync(sig Signal) {
	if p := recover(); p != nil {
		w.report(Change{Signal: sig, Now: w.tracker.Reasons(), Stale: w.tracker.LastStale()})
		panic(p)
	}
}

// report logs c and, when it changed the reasons, stores them and calls
// OnChange. The change is taken against the reasons last reported rather
// than the Tracker's before the step, so that OnChange always hears the
// whole difference.
func (w *Watcher) report(c Change) {
	if c.Stale != 0 {
		w.log.Warn("session reasons stale; cleared", "reasons", c.Stale, "signal", c.Signal)
	}
	c.Was = Reason(w.reasons.Load())
	if !c.Changed() {
		return
	}
	w.reasons.Store(uint32(c.Now))
	w.log.Debug("session signal", "signal", c.Signal, "was", c.Was, "now", c.Now)
	w.notify(c)
}

func (w *Watcher) rawSignal(sig Signal) {
	w.signalGen++
	if sig == SigWake {
		w.wakePending = true
		w.wakeGen = w.tracker.Gen(SigWake)
		w.wakeID++
	}
	if w.cfg.OnSignal == nil {
		return
	}
	defer func() {
		if p := recover(); p != nil {
			w.panicked(p)
		}
	}()
	w.cfg.OnSignal(sig)
}

func (w *Watcher) finishWake(c Change) {
	w.wakePending = false
	w.notifyWake(c)
}

// recheckStep applies a posted clearing signal at gen. Wake queries run
// against a private Tracker: an RPC can reenter raw handlers, whose newer
// reasons must survive both the old query's results and its ready callback.
func (w *Watcher) recheckStep(sig Signal, gen, wakeID uint32) {
	defer w.resync(sig)
	if sig != SigWake {
		w.report(w.tracker.Recheck(sig, gen, w.cfg.awake(), w.q))
		return
	}
	if wakeID != w.wakeID {
		return
	}
	signalGen := w.signalGen
	candidate := *w.tracker
	checked := &wakeQuerier{Querier: w.q}
	c := candidate.Recheck(sig, gen, w.cfg.awake(), checked)
	if wakeID != w.wakeID || signalGen != w.signalGen {
		return
	}
	if checked.err != nil {
		w.log.Warn("session wake recheck failed; retrying", "err", checked.err)
		return
	}
	*w.tracker = candidate
	w.report(c)
	if wakeID == w.wakeID && signalGen == w.signalGen {
		w.finishWake(c)
	}
}

// wakeQuerier retains the Tracker's ordinary error handling but prevents
// a partially failed wake query from opening the independent recovery gate.
type wakeQuerier struct {
	Querier
	err error
}

func (q *wakeQuerier) Session() (SessionInfo, error) {
	info, err := q.Querier.Session()
	q.err = errors.Join(q.err, err)
	return info, err
}

func (q *wakeQuerier) Desktop() (bool, error) {
	other, err := q.Querier.Desktop()
	q.err = errors.Join(q.err, err)
	return other, err
}

func (w *Watcher) notifyWake(c Change) {
	if w.cfg.OnWake == nil {
		return
	}
	defer func() {
		if p := recover(); p != nil {
			w.panicked(p)
		}
	}()
	w.cfg.OnWake(c)
}

// notify calls OnChange, recovering a panic, so that a failing handler
// cannot skip what follows the change: the barrier and OnStop at session
// end.
func (w *Watcher) notify(c Change) {
	if w.cfg.OnChange == nil {
		return
	}
	defer func() {
		if p := recover(); p != nil {
			w.panicked(p)
		}
	}()
	w.cfg.OnChange(c)
}

// panicked logs a panic recovered from a handler: the first at Error with
// its stack, later ones at Debug, so that a handler that always panics
// does not flood the log.
func (w *Watcher) panicked(p any) {
	w.panics++
	if w.panics == 1 {
		w.log.Error("session handler panicked", "panic", p, "stack", string(debug.Stack()))
		return
	}
	w.log.Debug("session handler panicked", "panic", p, "count", w.panics)
}

// barrier waits for Config.Barrier, bounded by BarrierWait. The Barrier
// runs on its own goroutine, so that one that ignores its context cannot
// hold the system's message longer. A failure is logged, never returned:
// the handlers never veto sleep or the end of the session.
func (w *Watcher) barrier(sig Signal) {
	if w.cfg.Barrier == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), w.cfg.BarrierWait)
	defer cancel()
	res := make(chan error, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				res <- fmt.Errorf("winsession: the barrier panicked: %v", p)
			}
		}()
		res <- w.cfg.Barrier(ctx)
	}()
	var err error
	select {
	case err = <-res:
	case <-ctx.Done():
		err = ctx.Err()
	}
	switch {
	case err == nil:
	case errors.Is(err, context.DeadlineExceeded):
		w.log.Warn("session barrier timed out", "signal", sig, "wait", w.cfg.BarrierWait)
	default:
		w.log.Warn("session barrier failed", "signal", sig, "err", err)
	}
}

// ended runs on the session thread after its teardown. If Close ended the
// thread, everything has ended; otherwise the fallback takes over.
func (w *Watcher) ended(err error) {
	if w.isClosing() {
		close(w.threadDone)
		w.finish()
		return
	}
	if err == nil {
		err = errors.New("winsession: the session thread received WM_QUIT")
	}
	w.threadErr = err
	w.log.Error("session thread ended; polling", "err", err, "poll", w.cfg.Poll)
	close(w.degraded)
	close(w.threadDone)
	go w.fallback()
}

// fallback owns the Tracker once the session thread has ended by itself.
// It ends only after Close, even when it fails.
func (w *Watcher) fallback() {
	defer w.finish()
	if !w.poll() {
		<-w.closing
	}
}

// poll clears Sleep and End, which no query sees, and steps SigTick with
// the Querier every Poll until Close, querying the session on every tick.
// It returns false after a panic, having cleared the reasons: a watcher
// that cannot watch must not hold the mapping suspended.
func (w *Watcher) poll() (ok bool) {
	defer func() {
		if p := recover(); p != nil {
			w.log.Error("session polling panicked; reasons cleared", "panic", p, "stack", string(debug.Stack()))
			w.giveUp()
		}
	}()
	awake := w.cfg.awake()
	w.report(w.tracker.Step(SigWake, awake, nil))
	w.report(w.tracker.Step(SigEndCanceled, awake, nil))
	w.tracker.SetRegistered(false)
	t := time.NewTicker(w.cfg.Poll)
	defer t.Stop()
	for {
		select {
		case <-w.closing:
			return true
		case <-t.C:
			w.step(SigTick, w.q)
		}
	}
}

// giveUp reports the reasons cleared, once, after the fallback failed.
func (w *Watcher) giveUp() {
	if was := Reason(w.reasons.Swap(0)); was != 0 {
		w.notify(Change{Signal: SigTick, Was: was})
	}
}
