package script

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

// WatchdogCall identifies the kind of JavaScript call under a budget.
type WatchdogCall uint8

const (
	WatchdogCallHandler WatchdogCall = iota
	WatchdogCallSetup
	WatchdogCallTeardown
)

// WatchdogOptions define the CPU and wall budgets of one JavaScript call.
type WatchdogOptions struct {
	Poll, Budget, Wall time.Duration
}

// Standard budgets from SCRIPTING §4.1.
var (
	HandlerWatchdog  = WatchdogOptions{Poll: 2 * time.Millisecond, Budget: 100 * time.Millisecond, Wall: 2 * time.Second}
	SetupWatchdog    = WatchdogOptions{Poll: 2 * time.Millisecond, Budget: time.Second, Wall: 2 * time.Second}
	TeardownWatchdog = WatchdogOptions{Poll: 2 * time.Millisecond, Budget: 200 * time.Millisecond, Wall: 2 * time.Second}
)

// BudgetError is the value carried by a goja InterruptedError.
type BudgetError struct {
	Clock        string
	Budget, Used time.Duration
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("script call exceeded its %s budget of %s (%s used)", e.Clock, e.Budget, e.Used)
}

// Firing records a watchdog interruption.
type Firing struct{ Reason *BudgetError }

// Watchdog polls the CPU time of a locked OS thread and interrupts its goja
// runtime. Begin/End bracket every call, and Interrupt and ClearInterrupt are
// protected by the same mutex so a late poll cannot poison the next call.
type Watchdog struct {
	vm        *goja.Runtime
	clock     threadCPUClock
	opts      WatchdogOptions
	mu        sync.Mutex
	start     time.Time
	cpu0      time.Duration
	kind      WatchdogCall
	run       bool
	fired     *Firing
	stop      chan struct{}
	done      chan struct{}
	once      sync.Once
	closeOnce sync.Once
	closeErr  error
}

// NewWatchdog must be called on the locked OS thread that will run vm. The
// returned watchdog may be used for many calls and must be stopped before
// that thread exits.
func NewWatchdog(vm *goja.Runtime, opts WatchdogOptions) (*Watchdog, error) {
	if vm == nil {
		return nil, errors.New("script: nil runtime")
	}
	if opts.Poll <= 0 || opts.Budget <= 0 || opts.Wall <= 0 {
		return nil, errors.New("script: watchdog poll, CPU budget and wall budget must be positive")
	}
	c, err := openThreadCPUClock()
	if err != nil {
		return nil, err
	}
	w := &Watchdog{vm: vm, clock: c, opts: opts, stop: make(chan struct{}), done: make(chan struct{})}
	go w.loop()
	return w, nil
}

// Begin starts a budgeted call. The default watchdog watches one type of
// call; callers can also choose the standard options by call kind.
func (w *Watchdog) Begin(kinds ...WatchdogCall) error {
	kind := WatchdogCallHandler
	if len(kinds) > 0 {
		kind = kinds[0]
	}
	cpu, err := w.clock.now()
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.run {
		return errors.New("script: watchdog call already running")
	}
	w.kind, w.cpu0, w.start, w.run, w.fired = kind, cpu, time.Now(), true, nil
	return nil
}

// End ends a call and clears any interrupt, including one that arrived as
// the JavaScript call returned.
func (w *Watchdog) End() *Firing {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.run = false
	w.vm.ClearInterrupt()
	f := w.fired
	w.fired = nil
	return f
}

func (w *Watchdog) loop() {
	defer close(w.done)
	t := time.NewTicker(w.opts.Poll)
	defer t.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
			w.check()
		}
	}
}

func (w *Watchdog) check() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.run || w.fired != nil {
		return
	}
	opts := w.opts
	switch w.kind {
	case WatchdogCallSetup:
		opts = SetupWatchdog
	case WatchdogCallTeardown:
		opts = TeardownWatchdog
	}
	var reason *BudgetError
	if cpu, err := w.clock.now(); err == nil && cpu-w.cpu0 > opts.Budget {
		reason = &BudgetError{Clock: "cpu", Budget: opts.Budget, Used: cpu - w.cpu0}
	} else if wall := time.Since(w.start); wall > opts.Wall {
		reason = &BudgetError{Clock: "wall", Budget: opts.Wall, Used: wall}
	}
	if reason != nil {
		w.fired = &Firing{Reason: reason}
		w.vm.Interrupt(reason)
	}
}

// Stop ends polling and releases the thread handle. It is idempotent.
func (w *Watchdog) Stop() error {
	w.once.Do(func() { close(w.stop) })
	<-w.done
	w.closeOnce.Do(func() { w.closeErr = w.clock.close() })
	return w.closeErr
}

// RestartDecision says when to retry after an interruption.
type RestartDecision struct {
	Delay     time.Duration
	Count     int
	Permanent bool
}

// RestartTracker counts interruptions in a sliding ten-minute window. The
// third interruption permanently disables automatic restarts until Reset.
type RestartTracker struct {
	times     []clock.Time
	permanent bool
}

// Record registers an interruption and returns the next retry policy.
func (r *RestartTracker) Record(now clock.Time) RestartDecision {
	if r.permanent {
		return RestartDecision{Count: len(r.times), Permanent: true}
	}
	keep := r.times[:0]
	for _, t := range r.times {
		if now.Sub(t) < 10*time.Minute {
			keep = append(keep, t)
		}
	}
	r.times = keep
	r.times = append(r.times, now)
	if len(r.times) >= 3 {
		r.permanent = true
	}
	delays := [...]time.Duration{time.Second, 10 * time.Second, 60 * time.Second}
	attempt := min(len(r.times), len(delays))
	return RestartDecision{Delay: delays[attempt-1], Count: len(r.times), Permanent: r.permanent}
}

// Permanent reports whether automatic restart is disabled.
func (r *RestartTracker) Permanent() bool { return r.permanent }

// Stop disables automatic restarts after an unrecoverable wall wedge.
func (r *RestartTracker) Stop() { r.permanent = true }

// Reset clears the breaker after a manual reload.
func (r *RestartTracker) Reset() { *r = RestartTracker{} }
