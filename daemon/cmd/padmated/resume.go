package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

const resumeWaitTime = 3 * time.Second
const resumeRetryTime = 500 * time.Millisecond

type resumeSupervisor interface {
	Suspend(winsession.Reason)
	Sync(context.Context) error
}
type resumeOutput interface{ BlockFor(output.Blocker, bool) }
type resumeInput interface{ Reenumerate(context.Context) error }

// resumeRecovery holds the mapping across both the wake recheck and the
// input publication/slot barriers. Its mutex only covers state and prompt,
// nonblocking gate changes; scans, Sync and logging never run under it.
type resumeRecovery struct {
	mu                      sync.Mutex
	ctx                     context.Context
	cancel                  context.CancelFunc
	scan                    func(context.Context) error
	sup                     resumeSupervisor
	gate                    resumeOutput
	log                     *slog.Logger
	jobs                    chan struct{}
	gen                     uint64
	reasons                 winsession.Reason
	pending, ready, stopped bool
	flight                  context.CancelFunc
}

func newResumeRecovery(ctx context.Context, scan func(context.Context) error, sup resumeSupervisor, gate resumeOutput, log *slog.Logger) *resumeRecovery {
	ctx, cancel := context.WithCancel(ctx)
	r := &resumeRecovery{ctx: ctx, cancel: cancel, scan: scan, sup: sup, gate: gate, log: log, jobs: make(chan struct{}, 1)}
	go r.run()
	return r
}

func (r *resumeRecovery) post() {
	select {
	case r.jobs <- struct{}{}:
	default:
	}
}
func (r *resumeRecovery) invalidate() {
	r.gen++
	if r.flight != nil {
		r.flight()
	}
}
func (r *resumeRecovery) hold() {
	reasons := r.reasons
	if r.pending {
		reasons |= winsession.Sleep
	}
	r.sup.Suspend(reasons)
}

// signal runs in the raw Win32 notification, before any deferred RPC.
func (r *resumeRecovery) signal(sig winsession.Signal) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped || r.ctx.Err() != nil {
		return
	}
	switch sig {
	case winsession.SigWake:
		r.invalidate()
		r.pending, r.ready = true, false
		r.gate.BlockFor(output.BlockResume, true)
	case winsession.SigSleep:
		r.invalidate()
		r.reasons |= winsession.Sleep
	case winsession.SigLock:
		r.invalidate()
		r.reasons |= winsession.Lock
	case winsession.SigDisconnect:
		r.invalidate()
		r.reasons |= winsession.Disconnect
	case winsession.SigQueryEnd, winsession.SigEnd:
		r.invalidate()
		r.reasons |= winsession.End
	default:
		return
	}
	r.hold()
}

func (r *resumeRecovery) change(c winsession.Change) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped || r.ctx.Err() != nil {
		return
	}
	if c.Now&^r.reasons != 0 {
		r.invalidate()
	}
	r.reasons = c.Now
	r.hold()
	if r.pending && r.ready && r.reasons == 0 {
		r.post()
	}
}

// wake is called after every wake recheck, including an unchanged one.
func (r *resumeRecovery) wake(c winsession.Change) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped || r.ctx.Err() != nil || !r.pending {
		return
	}
	r.reasons, r.ready = c.Now, true
	r.hold()
	if r.reasons == 0 {
		r.post()
	}
}

// stop does not wait for a driver or a sender. A late completion sees the
// stopped flag and can never open a gate or emit shutdown-time diagnostics.
func (r *resumeRecovery) stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	r.invalidate()
	r.cancel()
}

func (r *resumeRecovery) run() {
	finished := false
	defer func() {
		if finished {
			return
		}
		fault := any("runtime.Goexit")
		if p := recover(); p != nil {
			fault = p
		}
		r.mu.Lock()
		r.stopped = true
		r.pending = true
		r.gate.BlockFor(output.BlockResume, true)
		r.hold()
		r.cancel()
		r.mu.Unlock()
		// A broken log handler must not terminate the process either.
		defer func() { _ = recover() }()
		r.log.Error("resume worker stopped; restart padmated", "err", fault)
	}()
	for {
		select {
		case <-r.ctx.Done():
			finished = true
			return
		case <-r.jobs:
		}
		r.mu.Lock()
		if r.stopped || !r.pending || !r.ready || r.reasons != 0 {
			r.mu.Unlock()
			continue
		}
		gen := r.gen
		ctx, cancel := context.WithTimeout(r.ctx, resumeWaitTime)
		r.flight = cancel
		r.mu.Unlock()
		err := r.attempt(ctx)
		cancel()
		r.mu.Lock()
		r.flight = nil
		current := !r.stopped && r.ctx.Err() == nil && gen == r.gen && r.reasons == 0
		if current && err == nil {
			r.pending = false
			r.hold() // System gate opens behind its own resume barriers.
			r.gate.BlockFor(output.BlockResume, false)
		}
		r.mu.Unlock()
		if !current {
			continue
		}
		if err == nil {
			r.log.Info("gamepads reenumerated after wake")
			continue
		}
		r.log.Warn("gamepads not reenumerated after wake; retrying", "err", err, "after", resumeRetryTime)
		select {
		case <-r.ctx.Done():
			finished = true
			return
		case <-time.After(resumeRetryTime):
			r.post()
		}
	}
}

func (r *resumeRecovery) attempt(ctx context.Context) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("resume: panic: %v", p)
		}
	}()
	err = r.scan(ctx)
	if err == nil && ctx.Err() == nil {
		err = r.sup.Sync(ctx)
	}
	if err == nil {
		err = ctx.Err()
	}
	return err
}

func (r *liveRun) startResume() {
	scan := func(context.Context) error { return errors.New("input source does not support resume reenumeration") }
	if src, ok := r.src.(resumeInput); ok {
		scan = src.Reenumerate
	}
	r.resume = newResumeRecovery(r.end, scan, r.sup, r.loop, r.log)
}
