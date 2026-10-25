package script

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	filewatch "github.com/wdcyxxycdw/padmate/daemon/internal/watch"
)

const defaultReloadDebounce = 200 * time.Millisecond

// ReloadOptions configures a debounced script reload. Build is run on a
// worker goroutine after the debounce; the candidate is installed only by
// RunDue, which callers invoke on the slot goroutine.
type ReloadOptions struct {
	Clock    clock.Clock
	Debounce time.Duration
	Build    BuildFunc
	// Programs builds a runtime with macros; it replaces Build when set.
	Programs ProgramsFunc
	OnError  func(error)
	// OnInstall is called from RunDue, on the owner loop with no lock held,
	// after a candidate was swapped in. A candidate that failed to build or to
	// activate is not an install. Neither is the runtime that the lifecycle
	// restarts by itself after an interruption: it comes through Swap too, but
	// it is not a reload, and the owner may keep its failure state across it.
	OnInstall func()
}

// Reloader turns save notifications into lifecycle swaps. It is deliberately
// independent from filesystem watching: internal/watch supplies the change
// notification, while this type guarantees debounce, candidate isolation and
// owner-safe swaps.
type Reloader struct {
	lifecycle *Lifecycle
	clock     clock.Clock
	debounce  time.Duration
	build     ProgramsFunc
	onError   func(error)
	onInstall func()
	loop      *EventLoop

	mu           sync.Mutex
	seq          uint64
	timer        Handle
	cancel       context.CancelFunc
	pending      <-chan LoadResult
	loading      bool
	pollDeadline clock.Time
	lastErr      error
	queued       error
}

// ScriptWatchOptions combines the cross-platform file watcher with a
// Reloader. Watcher debounce is applied by internal/watch; the resulting
// settled change is handed to the slot-owned Reloader immediately.
type ScriptWatchOptions struct {
	Clock   clock.Clock
	Path    string
	Initial [32]byte
	Watch   filewatch.Options
	Build   BuildFunc
	// Programs builds a runtime with macros; it replaces Build when set.
	Programs ProgramsFunc
	OnError  func(error)
	// OnInstall is ReloadOptions.OnInstall of the watcher's reloader: a saved
	// script was installed.
	OnInstall func()
	OnChange  func() // called on the watcher goroutine after a settled change
}

// ScriptWatcher is the small seam between file notifications and the slot
// loop. Run is intended for the watcher goroutine; RunDue is called by the
// slot loop alongside script timers.
type ScriptWatcher struct {
	watcher  *filewatch.Watcher
	reloader *Reloader
	onChange func()
}

// NewScriptWatcher creates a script watcher. Build reads or compiles the
// settled source when RunDue starts a candidate.
func NewScriptWatcher(l *Lifecycle, o ScriptWatchOptions) (*ScriptWatcher, error) {
	if o.Clock == nil && l != nil {
		o.Clock = l.clock
	}
	w, err := filewatch.New(o.Clock, o.Path, o.Initial, o.Watch)
	if err != nil {
		return nil, err
	}
	if l == nil {
		return nil, errors.New("script: nil lifecycle")
	}
	return &ScriptWatcher{watcher: w, reloader: NewReloader(l, ReloadOptions{Clock: o.Clock, Debounce: time.Nanosecond, Build: o.Build, Programs: o.Programs, OnError: o.OnError, OnInstall: o.OnInstall}), onChange: o.OnChange}, nil
}

// Run watches until ctx is canceled. It does not run goja or swap runtimes;
// callers continue calling RunDue on the slot loop.
func (w *ScriptWatcher) Run(ctx context.Context) error {
	return w.watcher.Run(ctx, func(filewatch.Change) {
		w.reloader.RequestImmediate(ctx)
		if w.onChange != nil {
			w.onChange()
		}
	})
}

// RunDue advances the reloader on the slot loop.
func (w *ScriptWatcher) RunDue() { w.reloader.RunDue() }

// NextDeadline reports the debounce deadline for the slot loop.
func (w *ScriptWatcher) NextDeadline() (clock.Time, bool) {
	if w == nil || w.reloader == nil {
		return 0, false
	}
	return w.reloader.NextDeadline()
}

// Cancel stops pending candidate work. The caller should also cancel the
// context passed to Run to stop file polling.
func (w *ScriptWatcher) Cancel() { w.reloader.Cancel() }

// Ignore marks a content hash as the daemon's own write.
func (w *ScriptWatcher) Ignore(sum [32]byte) { w.watcher.Ignore(sum) }

// NewReloader returns a reloader associated with l. The lifecycle's clock is
// used when options.Clock is omitted. Build is required; a nil build is
// reported through OnError after the debounce rather than changing runtime.
func NewReloader(l *Lifecycle, o ReloadOptions) *Reloader {
	if l == nil {
		panic("script: nil lifecycle")
	}
	c := o.Clock
	if c == nil {
		c = l.clock
	}
	d := o.Debounce
	if d == 0 {
		d = defaultReloadDebounce
	}
	if d < 0 {
		panic("script: negative reload debounce")
	}
	build := o.Programs
	if build == nil {
		build = programsOf(o.Build)
	}
	return &Reloader{lifecycle: l, clock: c, debounce: d, build: build, onError: o.OnError, onInstall: o.OnInstall, loop: NewEventLoop(c)}
}

// Request schedules a reload. Requests arriving during the debounce replace
// the previous one. A request already building is canceled and its result
// is ignored, so a late worker can never swap an obsolete runtime.
func (r *Reloader) Request(ctx context.Context) {
	r.request(ctx, r.debounce)
}

// RequestImmediate schedules a candidate without an additional delay. It is
// used after internal/watch has already applied its content debounce.
func (r *Reloader) RequestImmediate(ctx context.Context) { r.request(ctx, 0) }

func (r *Reloader) request(ctx context.Context, delay time.Duration) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	r.seq++
	seq := r.seq
	if r.timer.Cancel() {
		r.timer = Handle{}
	}
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.pending = nil
	r.loading = false
	r.pollDeadline = 0
	r.lastErr = nil
	r.queued = nil
	requestCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.timer = r.loop.After(delay, func() {
		if seq != r.seq || requestCtx.Err() != nil {
			return
		}
		if r.build == nil {
			r.finishErrorLocked(Diagnose(PhaseCompile, errors.New("script: reload has no build function")))
			return
		}
		r.pending = r.lifecycle.startLoad(requestCtx, r.build, false, true)
		r.loading = true
		r.pollDeadline = r.clock.Now().Add(workerPollInterval)
	})
	r.mu.Unlock()
}

// RunDue advances the debounce timer and adopts a completed candidate. It
// must be called from the same slot goroutine that dispatches script input.
func (r *Reloader) RunDue() {
	r.mu.Lock()
	r.loop.RunDue(r.clock.Now())
	ch := r.pending
	seq := r.seq
	if ch == nil {
		onError, queued := r.takeErrorLocked()
		r.mu.Unlock()
		r.deliverError(queued, onError)
		return
	}
	select {
	case result := <-ch:
		// A new request can have replaced this result while the worker was
		// running. Do not let it install an obsolete runtime.
		if seq != r.seq {
			r.mu.Unlock()
			if result.Host != nil {
				go func(h *Host) { _ = h.Discard() }(result.Host)
			}
			return
		}
		r.pending = nil
		r.loading = false
		r.pollDeadline = 0
		r.cancel = nil
		if result.Err != nil {
			r.finishErrorLocked(result.Err)
			onError, queued := r.takeErrorLocked()
			r.mu.Unlock()
			r.deliverError(queued, onError)
			return
		}
		// Keep the request lock through Swap. Requests arrive on the watcher
		// goroutine; without this critical section one could cancel and replace
		// the sequence after the check above but before the old candidate was
		// installed.
		if err := r.lifecycle.Swap(result.Host); err != nil {
			r.finishErrorLocked(err)
			onError, queued := r.takeErrorLocked()
			r.mu.Unlock()
			r.deliverError(queued, onError)
			return
		}
		onError, queued := r.takeErrorLocked()
		r.mu.Unlock()
		if r.onInstall != nil {
			r.onInstall()
		}
		r.deliverError(queued, onError)
	default:
		if r.pollDeadline <= r.clock.Now() {
			r.pollDeadline = r.clock.Now().Add(workerPollInterval)
		}
		onError, queued := r.takeErrorLocked()
		r.mu.Unlock()
		r.deliverError(queued, onError)
	}
}

// Loading reports whether a candidate worker is currently running. A request
// that is still in its debounce window is intentionally not loading yet.
func (r *Reloader) Loading() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loading
}

// NextDeadline reports the debounce timer that needs RunDue on the owner
// slot loop. A worker result has no timer and is polled on the same wake.
func (r *Reloader) NextDeadline() (clock.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.loop.NextDeadline()
	if r.loading || r.pending != nil {
		t := r.pollDeadline
		if !ok || t < d {
			d, ok = t, true
		}
	}
	return d, ok
}

// LastError returns the most recent candidate or swap error.
func (r *Reloader) LastError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastErr
}

// Cancel drops a debounced request or an in-flight candidate. The active
// runtime remains installed and any worker result is ignored.
func (r *Reloader) Cancel() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	if r.timer.Cancel() {
		r.timer = Handle{}
	}
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.pending = nil
	r.loading = false
	r.pollDeadline = 0
}

func (r *Reloader) finishErrorLocked(err error) {
	r.lastErr = err
	r.queued = err
}

func (r *Reloader) takeErrorLocked() (func(error), error) {
	err := r.queued
	r.queued = nil
	return r.onError, err
}

func (r *Reloader) deliverError(err error, onError func(error)) {
	if err != nil && onError != nil {
		onError(err)
	}
}
