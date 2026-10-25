package spike

import (
	"fmt"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// WatchdogOptions are the limits of a Watchdog.
type WatchdogOptions struct {
	// Poll is how often the watchdog checks the running call; SCRIPTING
	// §4.1 says every 2–5 ms.
	Poll time.Duration
	// Budget is the thread CPU time one call may use: 100 ms for a
	// handler, 1000 ms for setup, 200 ms for teardown.
	Budget time.Duration
	// Wall is the wall-clock backstop, 2 s.
	Wall time.Duration
}

// HandlerWatchdog are the SCRIPTING §4.1 limits for a handler call.
var HandlerWatchdog = WatchdogOptions{Poll: 2 * time.Millisecond, Budget: 100 * time.Millisecond, Wall: 2 * time.Second}

// BudgetError is the value a Watchdog interrupts a runtime with; the
// call's *goja.InterruptedError carries it.
type BudgetError struct {
	// Clock is "cpu" for the thread CPU time budget and "wall" for the
	// backstop.
	Clock        string
	Budget, Used time.Duration
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("script call exceeded its %s budget of %s (%s used)", e.Clock, e.Budget, fmtDuration(e.Used))
}

// Firing is one interrupt by a Watchdog.
type Firing struct {
	Reason *BudgetError
	// Latency is the time from the Interrupt call to End, that is, until
	// the call returned to Go.
	Latency time.Duration
	at      int64
}

// Watchdog is the SPK-03 stand-in for the watchdog of SCRIPTING §4.1, not
// the M5 one. A goroutine polls the thread CPU time of the call running
// on the slot thread and interrupts the runtime when it is over budget.
// Begin and End bracket every call; the mutex makes sure an Interrupt
// only reaches the call it was meant for, because End clears it under the
// same lock.
type Watchdog struct {
	vm    *goja.Runtime
	clock *threadClock
	opts  WatchdogOptions

	mu      sync.Mutex
	running bool
	cpu0    time.Duration
	wall0   int64
	fired   *Firing
	polls   int
	since   int64

	stop, done chan struct{}
}

// StartWatchdog watches calls into vm made on the calling thread, which
// must be locked to its goroutine (runtime.LockOSThread) until Stop.
func StartWatchdog(vm *goja.Runtime, opts WatchdogOptions) (*Watchdog, error) {
	c, err := openThreadClock()
	if err != nil {
		return nil, err
	}
	w := &Watchdog{vm: vm, clock: c, opts: opts, since: stamp(), stop: make(chan struct{}), done: make(chan struct{})}
	go w.loop()
	return w, nil
}

// Begin marks the start of a call.
func (w *Watchdog) Begin() error {
	cpu, err := w.clock.now()
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.running, w.cpu0, w.wall0, w.fired = true, cpu, stamp(), nil
	w.mu.Unlock()
	return nil
}

// End marks the end of the call and returns its Firing, or nil. It clears
// the runtime's interrupt flag: goja clears it itself when an interrupt
// ends a call, but not when the Interrupt came after the call had
// returned, which would then fail the next call.
func (w *Watchdog) End() *Firing {
	end := stamp()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.running = false
	w.vm.ClearInterrupt()
	f := w.fired
	w.fired = nil
	if f != nil {
		f.Latency = stampDuration(end - f.at)
	}
	return f
}

// PollInterval is the mean time between polls so far.
func (w *Watchdog) PollInterval() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.polls == 0 {
		return 0
	}
	return stampDuration(stamp()-w.since) / time.Duration(w.polls)
}

// Stop stops the polling goroutine and closes the thread clock.
func (w *Watchdog) Stop() error {
	close(w.stop)
	<-w.done
	return w.clock.close()
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
	w.polls++
	if !w.running || w.fired != nil {
		return
	}
	var reason *BudgetError
	if cpu, err := w.clock.now(); err == nil && cpu-w.cpu0 > w.opts.Budget {
		reason = &BudgetError{Clock: "cpu", Budget: w.opts.Budget, Used: cpu - w.cpu0}
	} else if wall := stampDuration(stamp() - w.wall0); wall > w.opts.Wall {
		reason = &BudgetError{Clock: "wall", Budget: w.opts.Wall, Used: wall}
	}
	if reason == nil {
		return
	}
	w.fired = &Firing{Reason: reason, at: stamp()}
	w.vm.Interrupt(reason)
}
