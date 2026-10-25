package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"slices"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/watch"
)

// macroDir returns the macros folder the active profile runs its macros
// from: the script library of the data folder (SCR-51), or without a
// library the folder beside the --profile file; "" while no profile file
// is active.
func (r *liveRun) macroDir() string {
	if r.cfg.opts.library != nil {
		return r.cfg.opts.library.MacroDir()
	}
	target := r.profileTarget.Load()
	if target == nil || target.path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(target.path), profile.MacroDir)
}

// watchMacros keeps one watcher of the macros folder (SCR-50, design
// §5.3) until ctx is done. The library's stays while profiles switch;
// without a library, a switch to a profile in another folder replaces it.
func (r *liveRun) watchMacros(ctx context.Context) {
	for ctx.Err() == nil {
		dir := r.macroDir()
		if dir == "" {
			select {
			case <-ctx.Done():
				return
			case <-r.macroWatchWake:
				continue
			}
		}
		child, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			r.runMacroWatch(child, dir)
		}()
		for same := true; same; {
			select {
			case <-ctx.Done():
				stop()
				<-done
				return
			case <-done:
				stop()
				return // failure was logged by runMacroWatch
			case <-r.macroWatchWake:
				same = r.macroDir() == dir
			}
		}
		stop()
		<-done
	}
}

// runMacroWatch watches the macros folder dir until ctx is done: the top
// .ts and .js files, by name, size and modification time.
func (r *liveRun) runMacroWatch(ctx context.Context, dir string) {
	finished := false
	defer func() {
		if finished {
			return
		}
		err := errors.New("the macro watcher exited")
		if v := recover(); v != nil {
			err = fmt.Errorf("the macro watcher panicked: %v", v)
		}
		fmt.Fprintf(r.cfg.stderr, "padmated: %v; saving a macro no longer reloads it\n", err)
		r.log.Error("macro watcher panicked", "err", err, "stack", string(debug.Stack()))
	}()
	o := r.cfg.watch
	w, err := watch.NewDir(clock.Real(), dir, watch.DirOptions{
		Interval: o.Interval, Debounce: o.Debounce, Filter: profile.MacroCandidate, Logger: r.log,
	})
	if err != nil {
		finished = true
		r.log.Error("macro watcher unavailable", "err", err)
		return
	}
	_ = w.Run(ctx, func(c watch.DirChange) { r.macrosChanged(ctx, dir, c.Names) })
	finished = true
}

// macrosChanged handles a settled change of names in the macros folder
// dir: when the active profile, which still runs its macros from dir, runs
// one of them, every slot rebuilds its runtime (a save of a macro
// interrupts its runs, a known limit); a change of a file it does not
// run, a new or imported macro say, rebuilds nothing: the other profiles
// build theirs when they become active. Either way subscribers get
// profile.changed {kind:"macros"} and the UI lists the library again.
func (r *liveRun) macrosChanged(ctx context.Context, dir string, names []string) {
	r.profileMu.Lock()
	defer r.profileMu.Unlock()
	if ctx.Err() != nil || r.macroDir() != dir {
		return
	}
	target := r.targetLocked()
	used := r.scriptDef.macroFiles()
	var changed []string
	for _, name := range names {
		if slices.Contains(used, name) {
			changed = append(changed, name)
		}
	}
	if len(changed) > 0 {
		r.log.Info("macros changed; rebuilding the scripts", "profile", target.id, "files", changed)
		// Not ctx: a rebuild that began completes as a profile reload does.
		if _, err := r.sup.Scripts(context.Background(), "", false, true); err != nil {
			r.log.Debug("macro reload stopped: padmated is stopping", "err", err)
		}
	}
	r.events.Publish("profile.changed", map[string]any{"kind": "macros"})
}
