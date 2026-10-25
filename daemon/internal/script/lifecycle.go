package script

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// workerPollInterval bounds how long an owner loop can stay asleep while a
// build/setup worker is completing. Worker results are delivered on channels,
// so the loop needs a timer wake even when there are no script timers.
const workerPollInterval = time.Millisecond

// BuildFunc is executed by the lifecycle worker. It must compile and return
// a goja program; setup runs on that same worker before Load returns a host.
type BuildFunc func(context.Context) (*goja.Program, error)

// LoadResult is the worker's result. A successful host is in StateStarting;
// only Swap, called by the slot loop, makes it Running.
type LoadResult struct {
	Host *Host
	Err  error
}

// Lifecycle owns the active runtime and performs swaps on its caller's slot
// loop. Build and setup are deliberately isolated in Load's worker goroutine.
type Lifecycle struct {
	clock          clock.Clock
	hostOptions    HostOptions
	current        *Host
	paramsSchema   map[string]ParamInfo // last successful schema, including restart backoff
	state          State
	restart        RestartTracker
	restartCount   int
	restartLoop    *EventLoop
	build          ProgramsFunc
	restartHandle  Handle
	pending        []pendingLoad
	pollDeadline   clock.Time
	teardowns      []<-chan error
	teardownPoll   clock.Time
	runtimeSeq     uint64
	runtimeCycles  atomic.Uint64
	beforeSetup    func(*Host) // deterministic worker setup failure seam for tests
	beforeTeardown func()      // deterministic worker teardown hold seam for tests
	// queued are the macros triggered while the runtime builds, each file
	// once; they run once it is installed (design §5.2).
	queued []queuedMacro
}

type pendingLoad struct {
	result <-chan LoadResult
	cancel context.CancelFunc
}

type queuedMacro struct {
	file  string
	event Event
}

// NewLifecycle creates a lifecycle manager driven by c. Optional host options
// are copied into every worker-built runtime so output ownership survives a
// swap and teardown can release the same arbiter owner.
func NewLifecycle(c clock.Clock, options ...HostOptions) *Lifecycle {
	if c == nil {
		c = clock.Real()
	}
	var hostOptions HostOptions
	if len(options) > 0 {
		hostOptions = options[0]
	}
	hostOptions.Clock = c
	l := &Lifecycle{clock: c, hostOptions: hostOptions, state: StateDisposed, restartLoop: NewEventLoop(c)}
	if l.hostOptions.OwnerCycles == nil {
		l.hostOptions.OwnerCycles = &l.runtimeCycles
	}
	if l.hostOptions.DisabledMacros == nil {
		l.hostOptions.DisabledMacros = new(DisabledMacros)
	}
	return l
}

// DisabledMacros returns the macros this lifecycle's runtimes skip after an
// interruption (HostOptions.DisabledMacros).
func (l *Lifecycle) DisabledMacros() *DisabledMacros { return l.hostOptions.DisabledMacros }

// UpdatePad copies the slot's current input state on the owner loop. Worker
// loads receive a value copy at start; Swap refreshes candidates that spent
// time building while new input arrived.
func (l *Lifecycle) UpdatePad(state PadState) {
	l.hostOptions.Pad = state
	if l.current != nil {
		l.current.UpdatePad(state)
	}
}

// Load starts a worker that builds a candidate runtime. The returned channel
// is buffered so the worker can finish even if the slot is handling input.
func (l *Lifecycle) Load(ctx context.Context, build BuildFunc) <-chan LoadResult {
	return l.LoadPrograms(ctx, programsOf(build))
}

// LoadPrograms is Load for a runtime with macros: build returns the legacy
// entry, if any, and the macros (ActivatePrograms).
func (l *Lifecycle) LoadPrograms(ctx context.Context, build ProgramsFunc) <-chan LoadResult {
	l.build = build
	l.state = StateBuilding
	return l.startLoad(ctx, build, true, false)
}

// startLoad starts a worker for a candidate runtime without changing the
// active lifecycle state. Startup and restart use Load, while a debounced
// reload uses this form so a failed candidate leaves the current runtime
// running.
func (l *Lifecycle) startLoad(ctx context.Context, build ProgramsFunc, buffered, dropOnCancel bool) <-chan LoadResult {
	if ctx == nil {
		ctx = context.Background()
	}
	hostOptions := l.hostOptions
	hostOptions.Owner.Source = output.SourceScript
	if hostOptions.OwnerCycles != nil {
		hostOptions.Owner.Cycle = hostOptions.OwnerCycles.Add(1)
	} else {
		l.runtimeSeq++
		hostOptions.Owner.Cycle = l.runtimeSeq
	}
	capacity := 0
	if buffered {
		capacity = 1
	}
	out := make(chan LoadResult, capacity)
	send := func(result LoadResult) {
		if buffered && !dropOnCancel {
			out <- result
			return
		}
		if buffered {
			if ctx.Err() != nil {
				if result.Host != nil {
					_ = result.Host.Discard()
				}
				return
			}
			select {
			case out <- result:
			case <-ctx.Done():
				if result.Host != nil {
					_ = result.Host.Discard()
				}
			}
			return
		}
		select {
		case out <- result:
		case <-ctx.Done():
			if result.Host != nil {
				_ = result.Host.Discard()
			}
		}
	}
	go func() {
		select {
		case <-ctx.Done():
			send(LoadResult{Err: ctx.Err()})
			return
		default:
		}
		if build == nil {
			send(LoadResult{Err: errors.New("script: nil build function")})
			return
		}
		ps, err := build(ctx)
		if err != nil {
			send(LoadResult{Err: Diagnose(PhaseCompile, err)})
			return
		}
		sink := hostOptions.Output
		hostOptions.Output = nil
		h := NewHostWithOptions(hostOptions)
		h.candidateOutput = sink
		h.candidate = true
		if l.beforeSetup != nil {
			l.beforeSetup(h)
		}
		h.EnableWatchdog()
		if err := h.ActivatePrograms(ps); err != nil {
			_ = h.Discard()
			send(LoadResult{Err: err})
			return
		}
		// Activate keeps direct Host users convenient; a worker candidate is
		// output-disabled until the slot loop swaps it in.
		h.active = false
		h.state = StateStarting
		if err := ctx.Err(); err != nil {
			_ = h.Discard()
			send(LoadResult{Err: err})
			return
		}
		send(LoadResult{Host: h})
	}()
	return out
}

// Swap atomically installs a starting candidate, activates it, then releases
// the previous runtime's keys before handing its teardown to a worker.
// Teardown errors are reported by RunDue on the owner loop.
func (l *Lifecycle) Swap(next *Host) error {
	if next == nil || next.State() != StateStarting {
		return errors.New("script: swap requires a starting runtime")
	}
	// Workers own their VM until this transfer. Params may have changed
	// since their snapshot; validate against the candidate's own schema
	// before it can run onActivate or replace the active runtime.
	if err := next.UpdateParams(l.hostOptions.Params); err != nil {
		l.retire(next)
		if l.current == nil {
			l.state = StateDisposed
		}
		return err
	}
	old := l.current
	next.UpdatePad(l.hostOptions.Pad)
	next.BindLayers(l.hostOptions.Layers)
	l.current = next
	next.output = next.candidateOutput
	next.candidateOutput = nil
	next.candidate = false
	next.session.refresh()
	// What the old runtime has going on the gamepad ends before the new one can
	// start anything in its onActivate: both are the Script class of one gamepad,
	// and the old one's cancel, coming after, would end the new one's effects too.
	if old != nil {
		old.CancelEffects()
	}
	if err := next.OnActivate(); err != nil {
		l.retire(next)
		l.current = old
		if old == nil {
			l.state = StateDisposed
		} else {
			l.state = StateRunning
		}
		return err
	}
	l.state = StateRunning
	// A runtime installed while the automatic restart still waits out its
	// backoff (a saved script after an interruption) is the recovery that the
	// restart was for: the timer would only replace it with a second one.
	if l.restartHandle.Cancel() {
		l.restartHandle = Handle{}
	}
	l.paramsSchema = next.ParamsSchema()
	next.session.activate()
	if old != nil {
		l.retire(old)
	}
	return nil
}

// Current returns the runtime installed on the slot loop.
func (l *Lifecycle) Current() *Host { return l.current }

// State reports the lifecycle state.
func (l *Lifecycle) State() State { return l.state }

// RestartStatus reports automatic attempts begun since the last manual reload.
func (l *Lifecycle) RestartStatus() (int, bool) { return l.restartCount, l.restart.Permanent() }

// NextDeadline reports the earliest timer the lifecycle needs its owner loop
// to run. It includes the active runtime and automatic restart backoff.
func (l *Lifecycle) NextDeadline() (clock.Time, bool) {
	var d clock.Time
	ok := false
	if l.current != nil {
		if len(l.queued) > 0 {
			return l.clock.Now(), true
		}
		if t, ready := l.current.NextDeadline(); ready {
			d, ok = t, true
		}
	}
	if t, ready := l.restartLoop.NextDeadline(); ready && (!ok || t < d) {
		d, ok = t, true
	}
	if len(l.pending) > 0 {
		t := l.pollDeadline
		if !ok || t < d {
			d, ok = t, true
		}
	}
	if len(l.teardowns) > 0 {
		t := l.teardownPoll
		if !ok || t < d {
			d, ok = t, true
		}
	}
	return d, ok
}

// TeardownsPending reports whether retired runtimes still have worker work.
// Owners that replace a Lifecycle retain the old one until this becomes false
// so its asynchronous diagnostics continue to reach the slot loop.
func (l *Lifecycle) TeardownsPending() bool { return len(l.teardowns) > 0 }

// Dispatch forwards an input event to the active runtime and reports whether
// a script registration consumed it. A consumed event must not continue to a
// declaration layer owned by the slot.
func (l *Lifecycle) Dispatch(e Event) (DispatchResult, error) {
	if l.current == nil {
		return DispatchResult{}, nil
	}
	result, err := l.current.DispatchEvent(e)
	l.handleRuntimeFailure(err)
	return result, err
}

// RunMacro starts a run of the macro file on the active runtime (design
// §6.1), and applies the error policy: an interruption of the macro
// disables it and restarts the runtime. While a runtime builds, at startup
// or after an interruption, each triggered file is remembered once and runs
// when the runtime is installed; a load failure or Dispose forgets them.
func (l *Lifecycle) RunMacro(file string, e Event) error {
	if l.current != nil {
		err := l.current.RunMacro(file, e)
		l.handleRuntimeFailure(err)
		return err
	}
	if l.state == StateBuilding && !slices.ContainsFunc(l.queued, func(q queuedMacro) bool { return q.file == file }) {
		l.queued = append(l.queued, queuedMacro{file: file, event: e})
	}
	return nil
}

// EndMacroRuns ends every run of a macro on the active runtime at once,
// releasing its keys, and forgets the macros triggered while it builds. A
// profile reload calls it when it starts building the replacement: the
// runs end then (design §6.3), while the legacy script keeps running until
// the replacement is installed. It returns the errors of the runs.
func (l *Lifecycle) EndMacroRuns() error {
	l.queued = nil
	if l.current == nil {
		return nil
	}
	l.current.stopRuns()
	return l.current.takeMacroErrs()
}

// runQueued runs the macros triggered while the runtime was building, once
// it is installed, and forgets them when the build failed.
func (l *Lifecycle) runQueued() error {
	if l.current == nil {
		if l.state != StateBuilding {
			l.queued = nil
		}
		return nil
	}
	queued := l.queued
	l.queued = nil
	var errs []error
	for _, q := range queued {
		if l.current == nil {
			break
		}
		errs = append(errs, l.RunMacro(q.file, q.event))
	}
	return errors.Join(errs...)
}

func isBudgetFailure(err error) bool {
	return budgetFailure(err) != nil
}

// IsWallBudget reports the wall-clock wedge reason through diagnostic and
// goja interrupt wrappers. A caller owning an older runtime must stop it
// when a replacement candidate fails this way.
func IsWallBudget(err error) bool {
	b := budgetFailure(err)
	return b != nil && b.Clock == "wall"
}

func budgetFailure(err error) *BudgetError {
	if err == nil {
		return nil
	}
	var budget *BudgetError
	if errors.As(err, &budget) {
		return budget
	}
	var interrupted *goja.InterruptedError
	if !errors.As(err, &interrupted) {
		return nil
	}
	budget, _ = interrupted.Value().(*BudgetError)
	return budget
}

// handleRuntimeFailure applies the two budget policies and treats uncatchable
// stack overflow as an interruption. A wall wedge is terminal until reload.
// A macro interrupted for its CPU use or its stack is disabled first: the
// restarted runtime does not load it (design §7).
func (l *Lifecycle) handleRuntimeFailure(err error) {
	if err == nil {
		return
	}
	if l.current != nil {
		for _, me := range MacroErrorsOf(err) {
			if cpuInterruption(me) {
				l.current.disableMacro(me.File)
			}
		}
	}
	if budget := budgetFailure(err); budget != nil {
		if budget.Clock == "wall" {
			l.stopPermanently()
		} else {
			l.RecordInterruption(l.clock.Now())
		}
		return
	}
	var overflow *goja.StackOverflowError
	if errors.As(err, &overflow) {
		l.RecordInterruption(l.clock.Now())
	}
}

func (l *Lifecycle) stopPermanently() {
	l.restart.Stop()
	l.restartHandle.Cancel()
	l.restartHandle = Handle{}
	l.discardPending()
	l.pending = nil
	l.pollDeadline = 0
	l.queued = nil
	if l.current != nil {
		l.retire(l.current)
		l.current = nil
	}
	l.state = StateDisposed
}

// RunDue runs active timers and microtasks and reports asynchronous load and
// runtime failures to the owner loop.
func (l *Lifecycle) RunDue() error {
	l.restartLoop.RunDue(l.clock.Now())
	loadErr := l.pollLoads()
	teardownErr := l.pollTeardowns()
	if len(l.pending) > 0 && l.pollDeadline <= l.clock.Now() {
		l.pollDeadline = l.clock.Now().Add(workerPollInterval)
	}
	if len(l.teardowns) > 0 && l.teardownPoll <= l.clock.Now() {
		l.teardownPoll = l.clock.Now().Add(workerPollInterval)
	}
	queuedErr := l.runQueued()
	if l.current != nil {
		err := l.current.RunDue()
		l.handleRuntimeFailure(err)
		return errors.Join(loadErr, teardownErr, queuedErr, err)
	}
	return errors.Join(loadErr, teardownErr, queuedErr)
}

// retire cuts the output seam on the slot loop before a worker can execute
// teardown. The worker owns only the detached old goja runtime; even a late
// key.up or releaseAll cannot call the arbiter or touch the new owner.
func (l *Lifecycle) retire(old *Host) {
	if old == nil {
		return
	}
	old.Suspend()
	old.output = nil
	result := make(chan error, 1)
	l.teardowns = append(l.teardowns, result)
	l.teardownPoll = l.clock.Now().Add(workerPollInterval)
	go func() {
		if l.beforeTeardown != nil {
			l.beforeTeardown()
		}
		result <- old.Dispose()
	}()
}

func (l *Lifecycle) pollTeardowns() error {
	var errs []error
	for i := 0; i < len(l.teardowns); {
		select {
		case err := <-l.teardowns[i]:
			l.teardowns = append(l.teardowns[:i], l.teardowns[i+1:]...)
			errs = append(errs, err)
		default:
			i++
		}
	}
	return errors.Join(errs...)
}

// RecordInterruption applies the M5 restart policy. A permanent stop releases
// keys immediately while leaving the lifecycle available for manual reload.
func (l *Lifecycle) RecordInterruption(now clock.Time) RestartDecision {
	d := l.restart.Record(now)
	if l.current != nil {
		l.retire(l.current)
		l.current = nil
		l.state = StateDisposed
	}
	if d.Permanent || l.build == nil {
		return d
	}
	if l.restartHandle.Cancel() {
		l.restartHandle = Handle{}
	}
	l.restartHandle = l.restartLoop.After(d.Delay, func() {
		l.restartCount++
		// A restart candidate is built off the slot loop. Mark the lifecycle
		// as building so RunDue can adopt its result, while Dispose can make
		// any already queued result inert by restoring StateDisposed.
		l.state = StateBuilding
		ctx, cancel := context.WithCancel(context.Background())
		// Keep restart workers unbuffered and cancellable. If Dispose wins the
		// race with a worker finishing setup, the worker observes ctx.Done and
		// disposes its candidate instead of leaving it in an unread channel.
		l.pending = append(l.pending, pendingLoad{result: l.startLoad(ctx, l.build, false, true), cancel: cancel})
		l.pollDeadline = l.clock.Now().Add(workerPollInterval)
	})
	return d
}

func (l *Lifecycle) pollLoads() error {
	// Dispose drops pending candidates. A worker may still finish and write to
	// its buffered result channel, but it must never be swapped in afterwards.
	if l.state != StateBuilding {
		l.discardPending()
		return nil
	}
	var errs []error
	for i := 0; i < len(l.pending); {
		select {
		case result := <-l.pending[i].result:
			l.pending[i].cancel()
			l.pending = append(l.pending[:i], l.pending[i+1:]...)
			if result.Err == nil {
				if err := l.Swap(result.Host); err != nil {
					errs = append(errs, err)
					if l.current == nil {
						l.state = StateDisposed
					}
				}
			} else {
				l.state = StateDisposed
				errs = append(errs, result.Err)
			}
		default:
			i++
		}
	}
	return errors.Join(errs...)
}

// discardPending drains completed worker results and discards any candidate
// off the slot loop. Restart workers use cancellable unbuffered channels, so this
// prevents a candidate that finished just as the lifecycle was disposed from
// retaining timers, registrations or output ownership.
func (l *Lifecycle) discardPending() {
	for i := 0; i < len(l.pending); {
		l.pending[i].cancel()
		select {
		case result := <-l.pending[i].result:
			if result.Host != nil {
				go func(h *Host) { _ = h.Discard() }(result.Host)
			}
			l.pending = append(l.pending[:i], l.pending[i+1:]...)
		default:
			i++
		}
	}
}

// ResetRestartPolicy allows a manual reload to re-enable automatic restarts.
func (l *Lifecycle) ResetRestartPolicy() { l.restart.Reset(); l.restartCount = 0 }

// Dispose immediately releases the active runtime and schedules its teardown
// on a worker. Its completion and error are visible through RunDue.
func (l *Lifecycle) Dispose() error {
	if l.restartHandle.Cancel() {
		l.restartHandle = Handle{}
	}
	l.discardPending()
	l.pending = nil
	l.pollDeadline = 0
	l.queued = nil
	if l.current == nil {
		l.state = StateDisposed
		return nil
	}
	l.retire(l.current)
	l.current = nil
	l.state = StateDisposed
	return nil
}
