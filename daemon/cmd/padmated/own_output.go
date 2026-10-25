package main

import (
	"context"
	"slices"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ownwindow"
)

type ownUI struct {
	owner   uint64
	pid     uint32
	path    string
	created uint64
}

func (r *liveRun) wantsOwnDryRun() bool {
	if r.ownSend.Load() {
		return false
	}
	windows := r.ownWindows.Load()
	if windows == nil || len(*windows) == 0 {
		return false
	}
	matches := r.cfg.ownForeground
	var foreground uint32
	if matches == nil {
		foreground = ownwindow.ForegroundPID()
	}
	alive := r.cfg.ownAlive
	if alive == nil {
		alive = ownwindow.Alive
	}
	for _, ui := range *windows {
		match := ui.pid == foreground
		if matches != nil {
			match = matches(ui.pid, ui.path)
		}
		if match && alive(ui.pid, ui.path, ui.created) {
			return true
		}
	}
	return false
}

func (r *liveRun) previewOutput(c keys.Chord, local bool) {
	if !r.events.Listening("output.preview") {
		return
	}
	r.events.Publish("output.preview", struct {
		Chord       string `json:"chord"`
		LocalDryRun bool   `json:"localDryRun"`
		DryRun      bool   `json:"dryRun"`
	}{c.String(), local, r.cfg.opts.dryRun})
}

// This guard precedes both mapping commands and timer output on T-output.
// It checks the OS foreground PID instead of waiting for profile debounce.
func (r *liveRun) guardOwnOutput(a *output.Arbiter) {
	desired := r.wantsOwnDryRun()
	if desired == a.LocalDryRun() {
		return
	}
	if r.loop == nil {
		a.SetBlocked(true)
		return
	}
	if r.loop.Blocking()&output.BlockOwnWindow == 0 {
		r.loop.BlockFor(output.BlockOwnWindow, true)
	}
	a.SetBlocked(true)
	if err := a.SetLocalDryRun(desired); err != nil {
		return
	}
	r.ownMode.Store(desired)
	r.ownEpoch.Add(1)
	select {
	case r.ownWake <- struct{}{}:
	default:
	}
}

func (r *liveRun) startOwnOutput() {
	r.ownDone = make(chan struct{})
	go func() {
		defer close(r.ownDone)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.end.Done():
				return
			case <-r.loop.Done():
				return
			case <-ticker.C:
				windows := r.ownWindows.Load()
				if (windows == nil || len(*windows) == 0) && r.loop.Blocking()&output.BlockOwnWindow == 0 {
					continue
				}
				if err := r.loop.Do(func(*output.Arbiter) error { return nil }); err != nil {
					return
				}
				if r.loop.Blocking()&output.BlockOwnWindow != 0 {
					select {
					case r.ownWake <- struct{}{}:
					default:
					}
				}
			case <-r.ownWake:
				for {
					epoch := r.ownEpoch.Load()
					// ReleaseAll cancels held engine cycles, pending gestures, repeat
					// queues and every old script runtime before reopening output.
					if r.sup == nil {
						break
					}
					release := r.sup.ReleaseAll
					if r.cfg.ownBarrier != nil {
						release = func() error { return r.cfg.ownBarrier(r.sup.ReleaseAll) }
					}
					if err := release(); err != nil {
						if r.end.Err() == nil {
							r.log.Debug("own-window release failed", "err", err)
						}
						// A failed release or owner barrier keeps this independent gate
						// closed; the bounded ticker retries without blocking T-output.
						break
					}
					settled := false
					if err := r.loop.Do(func(*output.Arbiter) error {
						// Guard precedes this callback on T-output. Checking and opening
						// here is indivisible relative to a new transition's epoch.
						if epoch == r.ownEpoch.Load() {
							r.loop.BlockFor(output.BlockOwnWindow, false)
							settled = true
						}
						return nil
					}); err != nil {
						return
					}
					if settled {
						break
					}
				}
			}
		}
	}()
}

func (r *liveRun) setOwnOutput(enabled bool) error {
	r.configApplyMu.Lock()
	defer r.configApplyMu.Unlock()
	return r.applyOwnOutput(enabled)
}

// Caller holds configApplyMu, including across config persistence when needed.
func (r *liveRun) applyOwnOutput(enabled bool) error {
	r.ownSend.Store(enabled)
	return r.loop.Do(func(*output.Arbiter) error { return nil })
}

// applyEventGap sets output.eventGapMs on T-output (OUT-17); the caller
// holds configApplyMu. The gap is set whatever the Sends it makes return:
// their errors are logged as output errors, and only a closed loop fails.
func (r *liveRun) applyEventGap(ms int) error {
	var sendErr error
	err := r.loop.Do(func(a *output.Arbiter) error {
		sendErr = a.SetEventGap(time.Duration(ms) * time.Millisecond)
		return nil
	})
	if sendErr != nil {
		r.log.Warn("output error", "err", sendErr)
	}
	return err
}

func (r *liveRun) uiMethods(methods handler.Map, session *ipc.Session, owner uint64) {
	registered := false
	session.OnClose(func() {
		if !registered {
			return
		}
		r.uiExit.remove(session)
		// A broken bridge cannot make a still-foreground UI receive real keys.
		// Its verified process-instance lease lasts until the UI actually exits.
		_ = r.loop.Do(func(*output.Arbiter) error { return nil })
	})
	methods["ui.register"] = func(_ context.Context, req *jrpc2.Request) (any, error) {
		var p struct {
			PID uint32 `json:"pid"`
		}
		if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
			return nil, err
		}
		verify := r.cfg.verifyUI
		if verify == nil {
			verify = ownwindow.Verify
		}
		path, err := verify(p.PID, session.PeerPID)
		if err != nil {
			return nil, &jrpc2.Error{Code: -32001, Message: "UI process identity verification failed"}
		}
		instance := r.cfg.ownInstance
		if instance == nil {
			instance = ownwindow.Instance
		}
		created, err := instance(p.PID)
		if err != nil {
			return nil, &jrpc2.Error{Code: -32001, Message: "UI process identity unavailable"}
		}
		r.ownMu.Lock()
		next := []ownUI{}
		if old := r.ownWindows.Load(); old != nil {
			next = slices.Clone(*old)
		}
		alive := r.cfg.ownAlive
		if alive == nil {
			alive = ownwindow.Alive
		}
		next = slices.DeleteFunc(next, func(ui ownUI) bool {
			return ui.owner == owner || (ui.pid == p.PID && ui.created == created) || !alive(ui.pid, ui.path, ui.created)
		})
		if len(next) >= 16 {
			r.ownMu.Unlock()
			return nil, &jrpc2.Error{Code: -32001, Message: "too many UI process leases"}
		}
		next = append(next, ownUI{owner: owner, pid: p.PID, path: path, created: created})
		r.ownWindows.Store(&next)
		registered = true
		r.ownMu.Unlock()
		r.uiExit.add(session)
		if err := r.loop.Do(func(*output.Arbiter) error { return nil }); err != nil {
			return nil, err
		}
		return map[string]bool{"registered": true, "localDryRun": r.ownMode.Load()}, nil
	}
	// ui.exitReady is the UI's answer to daemon.exiting (reason "tray" or
	// "installer", DMN-22): it has saved its last edit and quits. exiting is
	// false when no exit is under way, and the answer then counts for
	// nothing.
	methods["ui.exitReady"] = func(_ context.Context, req *jrpc2.Request) (any, error) {
		if !registered {
			return nil, &jrpc2.Error{Code: -32001, Message: "UI registration required"}
		}
		if err := req.UnmarshalParams(jrpc2.StrictFields(new(struct{}))); err != nil {
			return nil, err
		}
		return map[string]bool{"exiting": r.uiExit.ready(session)}, nil
	}
	methods["ui.setOutput"] = func(_ context.Context, req *jrpc2.Request) (any, error) {
		if !registered {
			return nil, &jrpc2.Error{Code: -32001, Message: "UI registration required"}
		}
		var p struct {
			Enabled *bool `json:"enabled"`
		}
		if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
			return nil, err
		}
		if p.Enabled == nil {
			return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "enabled is required"}
		}
		if err := r.setOwnOutput(*p.Enabled); err != nil {
			return nil, err
		}
		return map[string]bool{"sendToOwnWindow": r.ownSend.Load(), "localDryRun": r.ownMode.Load()}, nil
	}
}
