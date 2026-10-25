package main

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/watch"
)

// watchLibrary keeps one file watcher alive at a time. Selection only posts
// to the mailbox; a stuck old read never delays switching or key release.
// Beside it the macros folder of the profile has a watcher of its own
// (watchMacros); watched closes once both have stopped.
func (r *liveRun) watchLibrary() {
	ctx, cancel := context.WithCancel(context.Background())
	r.stopWatching, r.watched = cancel, make(chan struct{})
	profileDone, macrosDone := make(chan struct{}), make(chan struct{})
	watched := r.watched
	go func() {
		<-profileDone
		<-macrosDone
		close(watched)
	}()
	go func() {
		defer close(macrosDone)
		r.watchMacros(ctx)
	}()
	go func() {
		defer close(profileDone)
		// The end of the profile watcher, a failure too, ends the watching:
		// the macros watcher stops with it.
		defer cancel()
		for ctx.Err() == nil {
			target := r.profileTarget.Load()
			if target.path == "" {
				select {
				case <-ctx.Done():
					return
				case <-r.profileWatchWake:
					continue
				}
			}
			o := r.cfg.watch
			o.Logger = r.log
			if target.id != "" && r.cfg.opts.library != nil {
				o.ReadFile = func(string) ([]byte, error) { return r.cfg.opts.library.Read(target.id) }
			}
			w, err := watch.New(clock.Real(), target.path, target.sum, o)
			if err != nil {
				r.log.Error("profile watcher unavailable", "err", err)
				return
			}
			child, stop := context.WithCancel(ctx)
			done := make(chan struct{})
			var running sync.WaitGroup
			running.Go(func() {
				defer close(done)
				r.runProfileWatch(child, w, func(c watch.Change) { r.reloadProfileFor(child, target, c) })
			})
			// The parents of the profile (PRO-19, ADR-0070) are watched too:
			// when one of them is saved, the profile reloads with the new
			// merge. They stop with the profile's watcher.
			for _, parent := range target.parents {
				pw, err := r.parentWatcher(parent)
				if err != nil {
					r.log.Warn("parent profile not watched", "profile", target.id, "parent", parent.ID, "err", err)
					continue
				}
				running.Go(func() {
					r.runProfileWatch(child, pw, func(watch.Change) { r.reloadDependentFor(child, target) })
				})
			}
			stopped := func() {
				stop()
				running.Wait()
			}
			select {
			case <-ctx.Done():
				stopped()
				return
			case <-done:
				stopped()
				return // failure was logged by runProfileWatch
			case <-r.profileWatchWake:
				stopped()
			}
		}
	}()
}

// parentWatcher returns the watcher of the file of library profile parent,
// which starts from the bytes that the running profile was merged from.
func (r *liveRun) parentWatcher(parent profile.ParentRef) (*watch.Watcher, error) {
	lib := r.cfg.opts.library
	if lib == nil {
		return nil, errors.New("no profile library")
	}
	path, err := lib.Path(parent.ID)
	if err != nil {
		return nil, err
	}
	o := r.cfg.watch
	o.Logger = r.log
	o.ReadFile = func(string) ([]byte, error) { return lib.Read(parent.ID) }
	return watch.New(clock.Real(), path, parent.Sum, o)
}

func (r *liveRun) runProfileWatch(ctx context.Context, w *watch.Watcher, apply func(watch.Change)) {
	finished := false
	defer func() {
		if finished {
			return
		}
		err := errors.New("the profile watcher exited")
		if v := recover(); v != nil {
			err = fmt.Errorf("the profile watcher panicked: %v", v)
		}
		fmt.Fprintf(r.cfg.stderr, "padmated: %v; saving the profile no longer reloads it\n", err)
		r.log.Error("profile watcher panicked", "err", err, "stack", string(debug.Stack()))
	}()
	_ = w.Run(ctx, apply)
	finished = true
}
