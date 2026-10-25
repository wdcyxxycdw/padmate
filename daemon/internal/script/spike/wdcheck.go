package spike

import (
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/dop251/goja"
)

// WatchdogCheckOptions say how CheckWatchdog runs.
type WatchdogCheckOptions struct {
	Watchdog WatchdogOptions
	// Loops is the number of while(true){} calls to interrupt.
	Loops int
	// NativeSleep is how long the native function sleeps, and
	// NativeInterrupt when the direct Interrupt comes during it.
	NativeSleep, NativeInterrupt time.Duration
	// Blocked are the limits of the run in which the watchdog itself
	// meets a sleeping native: a CPU budget the sleep never uses and a
	// wall backstop shorter than BlockedSleep.
	Blocked      WatchdogOptions
	BlockedSleep time.Duration
}

// DefaultWatchdogCheck uses the SCRIPTING §4.1 handler limits.
func DefaultWatchdogCheck() WatchdogCheckOptions {
	return WatchdogCheckOptions{
		Watchdog:        HandlerWatchdog,
		Loops:           10,
		NativeSleep:     50 * time.Millisecond,
		NativeInterrupt: 5 * time.Millisecond,
		Blocked:         WatchdogOptions{Poll: 2 * time.Millisecond, Budget: 20 * time.Millisecond, Wall: 100 * time.Millisecond},
		BlockedSleep:    300 * time.Millisecond,
	}
}

// WatchdogReport is what CheckWatchdog found.
type WatchdogReport struct {
	Options WatchdogCheckOptions
	// Clock is ThreadClockKind.
	Clock string
	// Poll is the mean poll interval the watchdog achieved.
	Poll time.Duration

	// Loops are the interrupted while(true){} calls.
	Loops []LoopRun
	// NextCallOK: after every interrupt, the next call returned normally
	// without anything but End.
	NextCallOK bool

	// StaleFails: an Interrupt made while no call ran failed the next
	// call; StaleErr is that call's error.
	StaleFails bool
	StaleErr   string
	// StaleClears: goja cleared the flag when that interrupt ended the
	// call, so the call after it worked.
	StaleClears bool
	// ClearWorks: ClearInterrupt after an Interrupt while idle let the
	// next call run.
	ClearWorks bool

	// Native is the direct Interrupt during a sleeping native function.
	Native NativeRun
	// Blocked is the watchdog meeting a sleeping native.
	Blocked BlockedRun

	Stack StackRun
}

// LoopRun is one interrupted while(true){}.
type LoopRun struct {
	// Reason is what the watchdog interrupted with.
	Reason BudgetError
	// Overshoot is Reason.Used minus the budget: how late the poll saw
	// the call go over.
	Overshoot time.Duration
	// Latency is from Interrupt to the return of the call.
	Latency time.Duration
	// Call is the wall time of the whole call.
	Call time.Duration
}

// NativeRun is an Interrupt made while a Go native function sleeps.
type NativeRun struct {
	// Call is the wall time of the call; Delay the time from the
	// Interrupt to its return.
	Call, Delay time.Duration
	// Interrupted: the call ended with the *goja.InterruptedError.
	Interrupted bool
	// Continued: the statement after the native call ran.
	Continued bool
}

// BlockedRun is a Watchdog whose call sleeps in a native function.
type BlockedRun struct {
	// Reason is what the watchdog interrupted with, nil if it did not.
	Reason *BudgetError
	// Latency is from Interrupt to the return of the call, Call the wall
	// time of the whole call.
	Latency, Call time.Duration
}

// StackRun is deep recursion under SetMaxCallStackSize(MaxCallStackSize).
type StackRun struct {
	// Depth is the deepest call the recursion reached.
	Depth int
	// Overflow: the Go caller got a *goja.StackOverflowError; Err is its
	// Error().
	Overflow bool
	Err      string
	// Caught and Finally: the script's catch and finally blocks ran.
	Caught, Finally bool
	// Reusable: the runtime ran a call normally afterwards.
	Reusable bool
}

// watchdogJS is the script CheckWatchdog runs. block is a Go native that
// sleeps.
const watchdogJS = `
var continued = false, depth = 0, caught = false, fin = false;
function spin() { while (true) {} }
function ok() { return 42; }
function blocked(ms) { block(ms); continued = true; }
function recurse(n) { depth = n; return recurse(n + 1) + 1; }
function deep() {
  try { recurse(1); } catch (e) { caught = true; } finally { fin = true; }
}
`

// CheckWatchdog runs the SCRIPTING §4.1 checks on a new locked OS thread,
// as a slot thread would be.
func CheckWatchdog(opts WatchdogCheckOptions) (*WatchdogReport, error) {
	type result struct {
		r   *WatchdogReport
		err error
	}
	done := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		r, err := checkWatchdog(opts)
		done <- result{r, err}
	}()
	res := <-done
	return res.r, res.err
}

// wdRuntime is the runtime of the checks and its functions.
type wdRuntime struct {
	h                     *Host
	spin, ok, blocked, dp goja.Callable
}

func newWDRuntime() (*wdRuntime, error) {
	h := NewHost()
	if err := h.VM.Set("block", func(ms int64) { time.Sleep(time.Duration(ms) * time.Millisecond) }); err != nil {
		return nil, err
	}
	if _, err := h.VM.RunString(watchdogJS); err != nil {
		return nil, err
	}
	r := &wdRuntime{h: h}
	for name, dst := range map[string]*goja.Callable{"spin": &r.spin, "ok": &r.ok, "blocked": &r.blocked, "deep": &r.dp} {
		fn, ok := goja.AssertFunction(h.VM.Get(name))
		if !ok {
			return nil, fmt.Errorf("spike: %s is not a function", name)
		}
		*dst = fn
	}
	return r, nil
}

// callOK reports whether ok() returns 42.
func (r *wdRuntime) callOK() bool {
	v, err := r.ok(goja.Undefined())
	return err == nil && v.ToInteger() == 42
}

func (r *wdRuntime) global(name string) goja.Value { return r.h.VM.Get(name) }

func checkWatchdog(opts WatchdogCheckOptions) (*WatchdogReport, error) {
	rt, err := newWDRuntime()
	if err != nil {
		return nil, err
	}
	rep := &WatchdogReport{Options: opts, Clock: ThreadClockKind, NextCallOK: true}
	if err := rep.loops(rt, opts); err != nil {
		return nil, err
	}
	rep.stale(rt)
	if err := rep.native(rt, opts); err != nil {
		return nil, err
	}
	if err := rep.blocked(rt, opts); err != nil {
		return nil, err
	}
	rep.stack(rt)
	return rep, nil
}

func (rep *WatchdogReport) loops(rt *wdRuntime, opts WatchdogCheckOptions) error {
	w, err := StartWatchdog(rt.h.VM, opts.Watchdog)
	if err != nil {
		return err
	}
	defer func() { _ = w.Stop() }()
	for range opts.Loops {
		if err := w.Begin(); err != nil {
			return err
		}
		t0 := stamp()
		_, callErr := rt.spin(goja.Undefined())
		call := stampDuration(stamp() - t0)
		f := w.End()
		reason, err := budgetOf(callErr)
		if err != nil {
			return err
		}
		if f == nil || f.Reason != reason {
			return errors.New("spike: while(true){} returned without the watchdog's interrupt")
		}
		rep.Loops = append(rep.Loops, LoopRun{Reason: *reason, Overshoot: reason.Used - reason.Budget, Latency: f.Latency, Call: call})
		if err := w.Begin(); err != nil {
			return err
		}
		ok := rt.callOK()
		w.End()
		rep.NextCallOK = rep.NextCallOK && ok
	}
	rep.Poll = w.PollInterval()
	return nil
}

// budgetOf returns the *BudgetError err carries.
func budgetOf(err error) (*BudgetError, error) {
	var ie *goja.InterruptedError
	if !errors.As(err, &ie) {
		return nil, fmt.Errorf("spike: call ended with %w, not an interrupt", err)
	}
	b, ok := ie.Value().(*BudgetError)
	if !ok {
		return nil, fmt.Errorf("spike: interrupted with %v, not a budget", ie.Value())
	}
	return b, nil
}

func (rep *WatchdogReport) stale(rt *wdRuntime) {
	rt.h.VM.Interrupt(errors.New("stale interrupt"))
	_, err := rt.ok(goja.Undefined())
	var ie *goja.InterruptedError
	rep.StaleFails = errors.As(err, &ie)
	if err != nil {
		rep.StaleErr = err.Error()
	}
	rep.StaleClears = rt.callOK()

	rt.h.VM.Interrupt(errors.New("stale interrupt"))
	rt.h.VM.ClearInterrupt()
	rep.ClearWorks = rt.callOK()
}

func (rep *WatchdogReport) native(rt *wdRuntime, opts WatchdogCheckOptions) error {
	if err := rt.h.VM.Set("continued", false); err != nil {
		return err
	}
	fired := make(chan int64, 1)
	go func() {
		time.Sleep(opts.NativeInterrupt)
		rt.h.VM.Interrupt(errors.New("interrupt during a native call"))
		fired <- stamp()
	}()
	t0 := stamp()
	_, err := rt.blocked(goja.Undefined(), rt.h.VM.ToValue(opts.NativeSleep.Milliseconds()))
	t1 := stamp()
	at := <-fired
	rt.h.VM.ClearInterrupt()
	var ie *goja.InterruptedError
	rep.Native = NativeRun{
		Call:        stampDuration(t1 - t0),
		Delay:       stampDuration(t1 - at),
		Interrupted: errors.As(err, &ie),
		Continued:   rt.global("continued").ToBoolean(),
	}
	return nil
}

func (rep *WatchdogReport) blocked(rt *wdRuntime, opts WatchdogCheckOptions) error {
	w, err := StartWatchdog(rt.h.VM, opts.Blocked)
	if err != nil {
		return err
	}
	defer func() { _ = w.Stop() }()
	if err := w.Begin(); err != nil {
		return err
	}
	t0 := stamp()
	_, callErr := rt.blocked(goja.Undefined(), rt.h.VM.ToValue(opts.BlockedSleep.Milliseconds()))
	call := stampDuration(stamp() - t0)
	f := w.End()
	rep.Blocked = BlockedRun{Call: call}
	if f != nil {
		if _, err := budgetOf(callErr); err != nil {
			return err
		}
		rep.Blocked.Reason, rep.Blocked.Latency = f.Reason, f.Latency
	}
	return nil
}

func (rep *WatchdogReport) stack(rt *wdRuntime) {
	_, err := rt.dp(goja.Undefined())
	var so *goja.StackOverflowError
	rep.Stack = StackRun{
		Depth:    int(rt.global("depth").ToInteger()),
		Overflow: errors.As(err, &so),
		Caught:   rt.global("caught").ToBoolean(),
		Finally:  rt.global("fin").ToBoolean(),
		Reusable: rt.callOK(),
	}
	if err != nil {
		rep.Stack.Err = err.Error()
	}
}
