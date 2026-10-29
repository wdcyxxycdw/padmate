package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ownwindow"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winfocus"
)

// liveProfile is immutable after publication. A new pointer also invalidates
// callbacks from a watcher of the old file, even when returning to the same ID.
type liveProfile struct {
	id, path string
	sum      [32]byte
	gen      uint32
	// parents are the profiles this one extends (PRO-19), nearest first, with
	// the sums of the files that were merged: the files the watcher watches
	// beside its own. publishProfile takes them from the running profile.
	parents []profile.ParentRef
}

// dependsOn reports whether the profile p extends id, directly or not.
func (p *liveProfile) dependsOn(id string) bool {
	return slices.ContainsFunc(p.parents, func(parent profile.ParentRef) bool { return parent.ID == id })
}

type profileRequest struct {
	gen uint32
	id  string
}

func (r *liveRun) requestProfile(gen uint32, id string) error {
	if !profile.ValidLibraryID(id) {
		return errors.New("invalid profile library id")
	}
	select {
	case r.profileRequests <- profileRequest{gen: gen, id: id}:
		return nil
	default:
		return errors.New("profile selection queue unavailable or full")
	}
}

func (r *liveRun) applyProfileRequest(ctx context.Context, req profileRequest) error {
	r.profileMu.Lock()
	defer r.profileMu.Unlock()
	if r.targetLocked().gen != req.gen {
		return nil
	}
	return r.activateProfileLocked(ctx, req.id, nil)
}

// targetLocked supports small test harnesses that do not use startLive.
func (r *liveRun) targetLocked() *liveProfile {
	if p := r.profileTarget.Load(); p != nil {
		return p
	}
	p := &liveProfile{id: r.cfg.opts.profileID, path: r.cfg.opts.profile, sum: r.sum, gen: 1}
	r.profileTarget.Store(p)
	return p
}

func (r *liveRun) publishProfile(p *liveProfile) {
	if p.parents == nil && r.cfg.profile != nil {
		p.parents = r.cfg.profile.Parents
	}
	r.profileTarget.Store(p)
	r.events.Publish("profile.changed", map[string]any{"kind": "active", "id": p.id, "revision": fmt.Sprintf("%x", p.sum), "generation": p.gen})
	select {
	case r.profileWatchWake <- struct{}{}:
	default:
	}
	select {
	case r.macroWatchWake <- struct{}{}:
	default:
	}
}

func (r *liveRun) startFocus() {
	r.focusWake = make(chan struct{}, 1)
	r.selectionWake = make(chan struct{}, 1)
	r.focusDone = make(chan struct{})
	ctx, cancel := context.WithCancel(r.end)
	r.focusStop = cancel
	go func() {
		defer close(r.focusDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		defer func() {
			if v := recover(); v != nil {
				r.log.Error("foreground selection stopped", "panic", v)
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.refreshScriptProfiles()
			case req := <-r.profileRequests:
				if err := r.applyProfileRequest(ctx, req); err != nil && ctx.Err() == nil {
					r.log.Warn("profile action failed", "profile", req.id, "err", err)
				}
			case <-r.focusWake:
				if !r.evaluateForeground(ctx, true) {
					return
				}
			case <-r.selectionWake:
				if !r.evaluateForeground(ctx, false) {
					return
				}
			}
		}
	}()
}

// evaluateForeground runs the selection for the current foreground on the
// worker; switched tells a foreground change from a config change. It
// returns false once the worker is to stop.
func (r *liveRun) evaluateForeground(ctx context.Context, switched bool) bool {
	app := r.foreground.Load()
	if app == nil || ctx.Err() != nil {
		return ctx.Err() == nil
	}
	err := r.selectApp(ctx, app, switched)
	if ctx.Err() != nil {
		return false
	}
	if err != nil {
		r.log.Warn("foreground selection failed", "exe", app.Exe, "err", err)
	}
	r.log.Debug("foreground evaluated", "exe", app.Exe, "switched", switched)
	return true
}

// wakeSelection asks the worker to re-evaluate the current foreground with
// the settings just committed. It never blocks; before startFocus the nil
// channel is never ready.
func (r *liveRun) wakeSelection() {
	select {
	case r.selectionWake <- struct{}{}:
	default:
	}
}

// daemonPID identifies padmated's own windows: opening the tray menu makes
// its hidden notification window the foreground.
var daemonPID = uint32(os.Getpid())

func (r *liveRun) onForeground(app winfocus.App) {
	// The daemon's own windows are no program, like the shell surfaces:
	// selection, scripts and the tray keep the program before them.
	if app.Path == "" || winfocus.IgnoredClass(app.Class) || app.PID == daemonPID || r.end.Err() != nil {
		return
	}
	r.foreground.Store(&app)
	// The capture helper (PRO-16) sees the same filtered foreground.
	r.noteForeground(app)
	if app.Elevated {
		r.notice(elevatedNotice(app.Exe))
	}
	if r.scriptDef.environment != nil {
		r.scriptDef.environment.SetApp(&script.AppInfo{Exe: app.Exe, Path: app.Path, PID: app.PID, Elevated: app.Elevated})
	}
	select {
	case r.focusWake <- struct{}{}:
	default:
	}
}

// isOwnUI reports whether app is a padmate UI process that registered over
// IPC (ui.register) and is still the same live process instance. Its
// windows never trigger profile selection, as if ignored, so editing a
// profile in padmate cannot switch the mapping away (UI-18).
func (r *liveRun) isOwnUI(app *winfocus.App) bool {
	if app == nil || app.PID == 0 {
		return false
	}
	windows := r.ownWindows.Load()
	if windows == nil {
		return false
	}
	alive := r.cfg.ownAlive
	if alive == nil {
		alive = ownwindow.Alive
	}
	for _, ui := range *windows {
		if ui.pid == app.PID && alive(ui.pid, ui.path, ui.created) {
			return true
		}
	}
	return false
}

func (r *liveRun) refreshScriptProfiles() {
	if r.cfg.opts.library == nil || r.scriptDef.environment == nil {
		return
	}
	ids, err := r.cfg.opts.library.IDs()
	if err != nil {
		r.log.Debug("profile directory refresh failed", "err", err)
		return
	}
	r.scriptDef.environment.SetProfiles(ids)
}

// selectForeground evaluates a foreground change to app.
func (r *liveRun) selectForeground(ctx context.Context, app *winfocus.App) error {
	return r.selectApp(ctx, app, true)
}

// selectApp applies the selection settings to app, the current foreground:
// its rule's profile, or the fixed default when no rule binds it
// (ADR-0053). A padmate UI keeps the current profile without a release
// (UI-18). switched is false when a config change, not a foreground
// change, asks for the evaluation: releaseOnSwitch "always" then does not
// release a profile that stays; a profile change still releases, through
// the same activation as a foreground change.
func (r *liveRun) selectApp(ctx context.Context, app *winfocus.App, switched bool) error {
	if r.isOwnUI(app) {
		return nil
	}
	if !r.cfg.opts.librarySelection() {
		return nil
	}
	r.profileMu.Lock()
	target := r.targetLocked()
	selection := r.selectionConfig().Select(app.Path, target.id)
	if selection.Profile == target.id {
		defer r.profileMu.Unlock()
		if switched && selection.Release && r.foreground.Load() == app && ctx.Err() == nil {
			return r.sup.ReleaseAll()
		}
		return nil
	}
	r.profileMu.Unlock()
	return r.activateProfile(ctx, selection.Profile, app)
}

// activateProfile serializes explicit switches, foreground selection and
// hot reload. It must never be called synchronously from a slot owner.
// expected rejects an obsolete automatic choice after slow disk reads.
func (r *liveRun) activateProfile(ctx context.Context, id string, expected *winfocus.App) error {
	r.profileMu.Lock()
	defer r.profileMu.Unlock()
	return r.activateProfileLocked(ctx, id, expected)
}

func (r *liveRun) activateProfileLocked(ctx context.Context, id string, expected *winfocus.App) error {
	if r.cfg.opts.library == nil {
		return errors.New("profile selection requires a profile library")
	}
	old := r.targetLocked()
	if old.id == id && (id != "" || old.path == "") {
		return nil
	}
	var path string
	data := []byte(disabledProfile)
	if id != "" {
		var err error
		path, err = r.cfg.opts.library.Path(id)
		if err != nil {
			return err
		}
		var problem error
		data, problem, err = readLibraryProfile(r.cfg.opts.library, id, true)
		if err != nil {
			return err
		}
		if problem != nil {
			r.log.Warn("default profile unusable; it runs without mapping", "profile", id, "err", problem)
		}
	}
	return r.installSelectionLocked(ctx, id, path, data, expected)
}

func (r *liveRun) installSelectionLocked(ctx context.Context, id, path string, data []byte, expected *winfocus.App) error {
	old := r.targetLocked()
	p, prog, ds, err := parseProfileIn(r.cfg.opts.library, id, path, data, r.cfg.stderr, r.selectionConfig())
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if r.end != nil && r.end.Err() != nil {
		return r.end.Err()
	}
	if expected != nil && r.foreground.Load() != expected {
		return nil // the mailbox contains a newer foreground
	}
	previous := r.cfg.profile
	r.scriptDef.setProfile(path, p)
	// Once accepted, finish the switch even if the requesting client goes
	// away. Supervisor shutdown still terminates the barriers.
	key := id
	if id == "" && path != "" {
		key = canonicalProfileKey(path)
	}
	gen, err := r.sup.ReloadProfile(context.Background(), prog, key)
	if err != nil {
		r.scriptDef.setProfile(old.path, previous)
		return err
	}
	r.cfg.profile, r.cfg.prog, r.running = p, prog, data
	r.sum = sha256.Sum256(data)
	timing := arbiterTiming(prog, nil) // the tap timing; the event gap is config.json's
	if err := r.loop.Do(func(a *output.Arbiter) error { return a.SetTiming(timing.MinPress, timing.MinGap) }); err != nil {
		r.log.Debug("profile timing update stopped", "err", err)
	}
	r.publishProfile(&liveProfile{id: id, path: path, sum: r.sum, gen: gen})
	if r.cfg.opts.memory != nil && r.cfg.opts.librarySelection() {
		r.cfg.opts.memory.SetLastProfile(id)
	}
	r.log.Info("profile selected", "profile", id, "gen", gen, "warnings", len(ds))
	r.logIgnoredFunctionKeyBindings(prog, id)
	return nil
}

func (r *liveRun) stopFocus() {
	if r.focusStop == nil {
		return
	}
	r.focusStop()
	select {
	case <-r.focusDone:
	case <-time.After(watchStopWait):
		fmt.Fprintln(r.cfg.stderr, "padmated: foreground selection did not stop; continuing key release")
	}
}
