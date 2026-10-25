package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/dtsgen"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// scriptDefinition is shared by all slots of one live profile. The profile
// watcher updates it only after a successful profile reload; each slot then
// swaps its own lifecycle on its loop goroutine.
type scriptDefinition struct {
	mu   sync.RWMutex
	root string
	// library is the data folder whose macros folder, the script library,
	// every profile's macros come from (SCR-51); "" without a library, when
	// they come from the macros folder beside the --profile file.
	library string
	cfg     *profile.ScriptConfig
	// macros are the macro files the profile's actions run, sorted, without
	// repeats (SCR-50): a runtime builds only these.
	macros    []string
	watch     bool
	log       *slog.Logger
	debugOnly bool // fake replay keeps every log line at debug level
	// Connected before the factory starts; both endpoints are concurrency-safe.
	environment   *script.Environment
	profileSwitch func(uint32, string) error
	// feedback is the profile's feedback settings, which a script's effects obey
	// (SCR-23): the rumble and led switches, and the strength and the pulses of
	// its presets.
	feedback profile.Feedback
	// errorCue, when set, is called on the slot goroutine when the script of the
	// slot starts failing, with the gamepad the slot has: the error buzz of
	// SCRIPTING 4.2.
	errorCue func(slot uint8, dev input.DeviceID)
}

// scriptSpec is a snapshot of a scriptDefinition: what a runtime builds.
type scriptSpec struct {
	root string
	// macroRoot holds the macros folder the macros build from: the data
	// folder of the library, or root.
	macroRoot string
	cfg       *profile.ScriptConfig
	macros    []string
	watch     bool
}

// needed reports whether the profile needs a runtime: it has a legacy
// script, or an action runs a macro (design §5.2).
func (s scriptSpec) needed() bool { return s.cfg != nil || len(s.macros) > 0 }

// api is the script API the macros build with: the legacy script's, or
// the host's when the profile has none. A macro has no version of its own
// (design §12).
func (s scriptSpec) api() string {
	if s.cfg != nil && s.cfg.API != "" {
		return s.cfg.API
	}
	return dtsgen.HostAPI.String()
}

func newScriptDefinition(profilePath string, cfg *profile.ScriptConfig, watch bool, logger ...*slog.Logger) *scriptDefinition {
	var copyCfg *profile.ScriptConfig
	if cfg != nil {
		v := *cfg
		copyCfg = &v
	}
	d := &scriptDefinition{root: filepath.Dir(profilePath), cfg: copyCfg, watch: watch, feedback: profile.DefaultFeedback()}
	if len(logger) > 0 {
		d.log = logger[0]
	}
	return d
}

// newProfileScriptDefinition is newScriptDefinition for the profile p at
// profilePath: its legacy script and its macros.
func newProfileScriptDefinition(profilePath string, p *profile.Profile, watch bool, logger ...*slog.Logger) *scriptDefinition {
	d := newScriptDefinition(profilePath, p.Script, watch, logger...)
	d.macros = p.MacroFiles()
	d.feedback = feedbackOf(p)
	return d
}

// update replaces the legacy script only, as a change of its params does.
func (d *scriptDefinition) update(cfg *profile.ScriptConfig) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.updateLocked(cfg)
}

// useProfile takes the legacy script and the macros of p, a new version of
// the same profile.
func (d *scriptDefinition) useProfile(p *profile.Profile) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.updateLocked(p.Script)
	d.macros = p.MacroFiles()
	d.feedback = feedbackOf(p)
}

// useLibrary has the profiles' macros come from the script library of the
// data folder dir (SCR-51).
func (d *scriptDefinition) useLibrary(dir string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.library = dir
}

// macroRootLocked returns the folder that holds the macros folder; the
// caller holds mu.
func (d *scriptDefinition) macroRootLocked() string {
	if d.library != "" {
		return d.library
	}
	return d.root
}

// setProfile switches to the profile p at path.
func (d *scriptDefinition) setProfile(path string, p *profile.Profile) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.root = filepath.Dir(path)
	d.updateLocked(p.Script)
	d.macros = p.MacroFiles()
	d.feedback = feedbackOf(p)
}

func (d *scriptDefinition) updateLocked(cfg *profile.ScriptConfig) {
	if cfg == nil {
		d.cfg = nil
		return
	}
	v := *cfg
	d.cfg = &v
}

func (d *scriptDefinition) spec() scriptSpec {
	d.mu.RLock()
	defer d.mu.RUnlock()
	s := scriptSpec{root: d.scriptRootLocked(), macroRoot: d.macroRootLocked(), macros: d.macros, watch: d.watch}
	if d.cfg != nil {
		v := *d.cfg
		s.cfg = &v
	}
	return s
}

// rootDir returns the folder the legacy script's entry is relative to.
func (d *scriptDefinition) rootDir() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.scriptRootLocked()
}

// scriptRootLocked returns the folder of the legacy script's files: the
// profile's own, or, for a script it inherits (PRO-19, ADR-0070), the
// folder of the parent that has it; the caller holds mu.
func (d *scriptDefinition) scriptRootLocked() string {
	if d.cfg != nil && d.cfg.Origin != "" && d.library != "" {
		return filepath.Join(d.library, "profiles", d.cfg.Origin)
	}
	return d.root
}

// macroFiles returns the macros the profile runs. The slice is never
// changed, only replaced.
func (d *scriptDefinition) macroFiles() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.macros
}

// Configured reports whether the profile has a legacy script, which takes
// over input.
func (d *scriptDefinition) Configured() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.cfg != nil
}

// Needed reports whether the profile needs a runtime: a legacy script or a
// macro. The supervisor gives the slots of such a profile the Resync
// barrier their dormant runtimes wait for (supervisor.Config's
// ScriptConfigured).
func (d *scriptDefinition) Needed() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.cfg != nil || len(d.macros) > 0
}

// Factory keeps live slots dormant until the first assignment's complete
// Resync. It supplies the device snapshot before setup and onActivate run.
func (d *scriptDefinition) Factory(index uint8, gen uint32, clk clock.Clock, arb slot.Arbiter) (slot.ScriptRuntime, error) {
	r, err := newScriptRuntime(d, index, gen, clk, arb, true)
	if r != nil {
		r.waitResync = true
	}
	return r, err
}

type scriptRuntime struct {
	def       *scriptDefinition
	index     uint8
	gen       uint32
	clk       clock.Clock
	arb       slot.Arbiter
	lifecycle *script.Lifecycle
	// activeRoot is the profile folder of lifecycle's runtime: after a
	// failed switch it is the old profile's, which runs no macro.
	activeRoot string
	retired    []*script.Lifecycle // old runtimes awaiting worker teardown diagnostics
	watcher    *script.ScriptWatcher
	stop       context.CancelFunc
	done       chan struct{}
	wake       chan struct{}
	pad        script.PadState
	layers     engine.LayerAccess
	logs       *script.LogBuffer
	pending    *pendingScriptReload
	// cancelDone closes after the last canceled candidate was discarded.
	// The slot never waits for it; tests use it to verify late worker safety.
	cancelDone <-chan struct{}
	// buildOverride is a deterministic seam for tests that need to hold a
	// candidate worker in setup. Production runtimes leave it nil.
	buildOverride func(context.Context, string, *profile.ScriptConfig) (*goja.Program, error)
	// macroBuild builds the macros on a lifecycle worker; nil means
	// buildMacroPrograms. Tests hold or count it.
	macroBuild func(ctx context.Context, root string, files []string, api string) []script.MacroProgram
	// disabled are the macros this slot stopped after an interruption; every
	// lifecycle of the runtime shares it (design §7).
	disabled *script.DisabledMacros
	// macroErrors are the latest run error of each macro, by file, for
	// script.status; forgetErrors clears them, as lastError.
	macroErrors map[string]string
	// reloadErr is handed to Slot.Errors by RunDue. A failed candidate is
	// deliberately not allowed to replace the active runtime or its watcher.
	reloadErr    error
	lastError    string // detached diagnostic, retained after RunDue reports it
	paused       bool
	wallStopped  bool
	waitResync   bool
	cycles       atomic.Uint64
	sessions     *script.SessionStore
	profileKey   string
	paramsSchema map[string]script.ParamInfo // last successful schema, retained across pause
}

type pendingScriptReload struct {
	life         *script.Lifecycle
	result       <-chan script.LoadResult
	cancel       context.CancelFunc
	gen          uint32
	spec         scriptSpec
	pollDeadline clock.Time
}

const reloadPollInterval = time.Millisecond

// scriptArbiter narrows a slot's RemoteArbiter to the output calls exposed to
// scripts. Owner matching keeps a runtime's release from touching the engine.
type scriptArbiter struct{ arb slot.Arbiter }

func (a scriptArbiter) Down(o output.Owner, c keys.Chord) error {
	return a.arb.Down(o, c)
}
func (a scriptArbiter) Up(o output.Owner, c keys.Chord) error { return a.arb.Up(o, c) }
func (a scriptArbiter) Tap(o output.Owner, c keys.Chord, d time.Duration) error {
	return a.arb.Tap(o, c, d)
}
func (a scriptArbiter) TapWithCompletion(o output.Owner, c keys.Chord, d time.Duration, done func(output.TapCompletion)) error {
	if arb, ok := a.arb.(interface {
		TapWithCompletion(output.Owner, keys.Chord, time.Duration, func(output.TapCompletion)) error
	}); ok {
		return arb.TapWithCompletion(o, c, d, done)
	}
	if done != nil {
		done(output.TapCompletion{Dropped: true, Canceled: true})
	}
	return errors.New("script: slot arbiter lacks tap completion")
}

// TypeTextWithCompletion types text for key.type and the text steps of
// key.sequence (SCR-26). The slot's RemoteArbiter has it; any other arbiter
// ends the text at once as dropped and canceled.
func (a scriptArbiter) TypeTextWithCompletion(o output.Owner, text string, gap time.Duration, done func(output.TapCompletion)) error {
	if arb, ok := a.arb.(interface {
		TypeTextWithCompletion(output.Owner, string, time.Duration, func(output.TapCompletion)) error
	}); ok {
		return arb.TypeTextWithCompletion(o, text, gap, done)
	}
	if done != nil {
		done(output.TapCompletion{Dropped: true, Canceled: true})
	}
	return errors.New("script: slot arbiter cannot type text")
}
func (a scriptArbiter) ReleaseOwner(o output.Owner) error {
	return a.arb.ReleaseOwners(func(candidate output.Owner) bool { return candidate == o })
}

func newScriptRuntime(def *scriptDefinition, index uint8, gen uint32, clk clock.Clock, arb slot.Arbiter, initialPaused ...bool) (*scriptRuntime, error) {
	if def == nil {
		return nil, nil
	}
	r := &scriptRuntime{def: def, index: index, gen: gen, clk: clk, arb: arb, wake: make(chan struct{}, 1), disabled: new(script.DisabledMacros)}
	r.logs = script.NewLogBuffer(r.wake)
	if len(initialPaused) > 0 {
		r.paused = initialPaused[0]
	}
	if r.paused {
		return r, nil
	}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *scriptRuntime) build(ctx context.Context, root string, cfg *profile.ScriptConfig) (*goja.Program, error) {
	if r.buildOverride != nil {
		return r.buildOverride(ctx, root, cfg)
	}
	if cfg == nil {
		return nil, errors.New("script: no profile script configuration")
	}
	result, err := compile.BuildCached(compile.BuildOptions{Root: root, Entry: cfg.Entry, API: cfg.API})
	if err != nil {
		return nil, err
	}
	return goja.Compile(cfg.Entry, string(result.JS), false)
}

// programs builds what the runtime of spec runs, on a lifecycle worker:
// the legacy entry, if any, and the macros the profile's actions run. A
// macro that does not build only fails itself (design §5.1).
func (r *scriptRuntime) programs(ctx context.Context, spec scriptSpec) (script.Programs, error) {
	var ps script.Programs
	if spec.cfg != nil {
		p, err := r.build(ctx, spec.root, spec.cfg)
		if err != nil {
			return ps, err
		}
		ps.Entry = p
	}
	if len(spec.macros) == 0 {
		return ps, nil
	}
	if err := ctx.Err(); err != nil {
		return ps, err
	}
	build := r.macroBuild
	if build == nil {
		build = buildMacroPrograms
	}
	ps.Macros = build(ctx, spec.macroRoot, spec.macros, spec.api())
	return ps, nil
}

// hostOptions are the options of the runtimes of a lifecycle for spec at
// the profile generation gen.
func (r *scriptRuntime) hostOptions(gen uint32, spec scriptSpec) script.HostOptions {
	session := r.profileSession() // first: it may set profileKey
	o := script.HostOptions{
		Clock: r.clk, Output: scriptArbiter{arb: r.arb},
		Owner:       output.Owner{Slot: r.index, Gen: gen, Source: output.SourceScript},
		OwnerCycles: &r.cycles, Pad: r.pad, Logs: r.logs,
		Wake: r.notify, Session: session, Profile: r.profileKey,
		Environment: r.def.environment, SwitchProfile: r.profileRequester(gen),
		Layers: r.layers, LayerState: r.layerSnapshot(), LayerNames: r.layerNames(),
		DisabledMacros: r.disabled,
	}
	if spec.cfg != nil {
		o.TickHz, o.Params = spec.cfg.TickHz, spec.cfg.Params
	}
	return o
}

func (r *scriptRuntime) load() error {
	defer r.flushLogs()
	spec := r.def.spec()
	if !spec.needed() {
		return nil
	}
	life := script.NewLifecycle(r.clk, r.hostOptions(r.gen, spec))
	result := <-life.LoadPrograms(context.Background(), func(ctx context.Context) (script.Programs, error) {
		return r.programs(ctx, spec)
	})
	if result.Err != nil {
		return result.Err
	}
	if err := life.Swap(result.Host); err != nil {
		return err
	}
	r.lifecycle, r.activeRoot = life, spec.root
	if !spec.watch || spec.cfg == nil {
		return nil
	}
	w, err := r.newWatcher(life, spec.root, spec.cfg)
	if err != nil {
		_ = life.Dispose()
		r.lifecycle = nil
		return err
	}
	r.startWatcher(w)
	return nil
}

func (r *scriptRuntime) Dispatch(e slot.ScriptEvent) (consumed bool, dispatchErr error) {
	defer func() { r.rememberError(dispatchErr) }()
	defer r.flushLogs()
	if r.paused || r.wallStopped || r.lifecycle == nil {
		return false, nil
	}
	result, err := r.lifecycle.Dispatch(script.Event{
		Control: e.Control, Gesture: e.Gesture, Time: e.Time,
		Duration: e.Duration, Count: e.Count, Scope: e.Scope,
		Cycle: e.Cycle, Held: e.Held,
	})
	if script.IsWallBudget(err) {
		r.stopForWall()
	}
	return result.Consumed, err
}

// RunMacro starts a run of the macro file (SCR-50). The slot calls it
// whatever the runtime's state; the slot itself drops a trigger while it is
// paused or waits for its Resync. While a runtime builds, at the start,
// after a release or for a reload, the building lifecycle remembers each
// file once and runs it once installed (design §5.2): a reload has ended
// the installed runtime's runs, and its profile may not have the file, or
// have another file of the same name. A stopped runtime drops it, as does
// the old profile's runtime left installed by a failed switch.
func (r *scriptRuntime) RunMacro(file string, e slot.ScriptEvent) (runErr error) {
	defer func() { r.rememberError(runErr) }()
	defer r.flushLogs()
	if r.paused || r.wallStopped {
		return nil
	}
	life := r.lifecycle
	switch {
	case r.pending != nil:
		life = r.pending.life
	case life != nil && r.activeRoot != r.def.rootDir():
		return nil
	}
	if life == nil {
		return nil
	}
	err := life.RunMacro(file, script.Event(e))
	if script.IsWallBudget(err) {
		r.stopForWall()
	}
	return err
}

func (r *scriptRuntime) UpdatePad(snapshot slot.PadSnapshot) {
	r.pad = script.PadState{
		Connected: snapshot.Connected, Info: snapshot.Info, Key: snapshot.Key, Down: snapshot.Down,
		Since: snapshot.Since, Axes: snapshot.Axes,
		HasControl: snapshot.HasControl, HasAxis: snapshot.HasAxis,
		Analog: snapshot.Analog, AnalogResolved: true,
		Power: snapshot.Power, HasPower: snapshot.HasPower,
	}
	if r.lifecycle != nil {
		r.lifecycle.UpdatePad(r.pad)
	}
	if r.pending != nil {
		r.pending.life.UpdatePad(r.pad)
	}
}

func (r *scriptRuntime) RunDue() (runErr error) {
	defer func() { r.rememberError(runErr) }()
	defer r.flushLogs()
	if !r.paused {
		r.pollReload()
	}
	var lifecycleErr error
	if r.lifecycle != nil {
		lifecycleErr = r.lifecycle.RunDue()
		if script.IsWallBudget(lifecycleErr) {
			r.stopForWall()
		}
	}
	for i := 0; i < len(r.retired); {
		old := r.retired[i]
		lifecycleErr = errors.Join(lifecycleErr, old.RunDue())
		if old.TeardownsPending() {
			i++
		} else {
			r.retired = append(r.retired[:i], r.retired[i+1:]...)
		}
	}
	if r.watcher != nil {
		r.watcher.RunDue()
	}
	err := r.reloadErr
	r.reloadErr = nil
	if script.IsWallBudget(err) {
		r.stopForWall()
	}
	return errors.Join(lifecycleErr, err)
}

// Wake is a notification from the file watcher to an otherwise idle slot.
// It carries no runtime state; all reload work still runs on the slot loop.
func (r *scriptRuntime) Wake() <-chan struct{} { return r.wake }

func (r *scriptRuntime) WaitForInitialResync() bool { return r.waitResync }

// HUDPanels are the hud panels of the running script (SCR-22). There are
// none while the runtime is paused, stopped or not running, and none of the
// old profile's runtime that a failed switch leaves installed: they do not
// belong to the profile the slot now shows (RunMacro treats it the same way).
func (r *scriptRuntime) HUDPanels() []script.HUDPanel {
	if r.paused || r.wallStopped || r.lifecycle == nil || r.activeRoot != r.def.rootDir() {
		return nil
	}
	if h := r.lifecycle.Current(); h != nil {
		return h.HUDPanels()
	}
	return nil
}

// Configured lets the slot enable script-only virtual controls when the
// current profile has a script, including immediately after profile reload.
func (r *scriptRuntime) Configured() bool {
	return r.def.Configured()
}

// Needed lets the slot keep its Resync gate and release bookkeeping for a
// profile that only runs macros, without a legacy script's takeover.
func (r *scriptRuntime) Needed() bool {
	return r.def.Needed()
}

// InputStatus is read only on the slot loop. A cold runtime must publish its
// gesture registrations before input planning. A live host remains responsive
// while a normal hot-reload candidate builds, as it did before the input gate.
func (r *scriptRuntime) InputStatus() (pending, ready bool) {
	return r.pending != nil && r.lifecycle == nil, !r.paused && !r.wallStopped && r.lifecycle != nil
}

func (r *scriptRuntime) notify() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *scriptRuntime) NextDeadline() (clock.Time, bool) {
	var d clock.Time
	ok := false
	if r.lifecycle != nil {
		d, ok = r.lifecycle.NextDeadline()
	}
	for _, old := range r.retired {
		if t, ready := old.NextDeadline(); ready && (!ok || t < d) {
			d, ok = t, true
		}
	}
	if r.watcher != nil {
		if t, ready := r.watcher.NextDeadline(); ready && (!ok || t < d) {
			d, ok = t, true
		}
	}
	// A worker result is delivered on a buffered channel and has no timer of
	// its own. Poll at a bounded interval so an idle slot still adopts a
	// completed candidate without requiring a new input edge.
	if r.pending != nil {
		t := r.pending.pollDeadline
		if !ok || t < d {
			d, ok = t, true
		}
	}
	return d, ok
}

func (r *scriptRuntime) ReleaseAll() error {
	// A slot-wide safety release invalidates old timers as well as held keys.
	// A direct script key.releaseAll stays within Host and keeps registrations.
	return r.SetPaused(true)
}

// SetPaused is the slot's safety gate. Resume starts a fresh worker-built
// Host, preserving setup timers without replaying old Promise continuations.
func (r *scriptRuntime) SetPaused(on bool) error {
	if r.paused == on {
		return nil
	}
	r.paused = on
	if !on {
		if r.wallStopped {
			return nil
		}
		return r.Reload(r.gen)
	}
	return r.stopCurrent()
}

func (r *scriptRuntime) stopForWall() {
	r.wallStopped = true
	_ = r.stopCurrent()
}

func (r *scriptRuntime) stopCurrent() error {
	r.cancelPending()
	r.stopWatcher()
	if r.lifecycle == nil {
		return nil
	}
	r.paramsSchema = r.lifecycle.ParamsSchema()
	if h := r.lifecycle.Current(); h != nil {
		h.Suspend()
	}
	old := r.lifecycle
	err := old.Dispose()
	r.lifecycle = nil
	if old.TeardownsPending() {
		r.retired = append(r.retired, old)
	}
	return err
}

func (r *scriptRuntime) Reload(gen uint32) error {
	// A profile reload is the explicit recovery from a wall wedge. A pause
	// remains in force, so the worker starts only after the normal resume.
	r.wallStopped = false
	r.forgetErrors()
	if r.paused {
		r.cancelPending()
		r.gen = gen
		return nil
	}
	spec := r.def.spec()
	if !spec.needed() {
		r.cancelPending()
		r.reloadErr = nil
		r.stopWatcher()
		if r.lifecycle != nil {
			old := r.lifecycle
			if h := old.Current(); h != nil {
				h.Suspend()
			}
			err := old.Dispose()
			r.lifecycle = nil
			if old.TeardownsPending() {
				r.retired = append(r.retired, old)
			}
			return err
		}
		return nil
	}
	// A newer profile generation supersedes an in-flight candidate. The old
	// active lifecycle and watcher remain until the new candidate is ready
	// and has a watcher of its own, but the runs of its macros end now
	// (design §6.3): the slot has just released their keys, and they must
	// not press more for the new profile. Its legacy script keeps running.
	r.cancelPending()
	r.reloadErr = nil
	var endErr error
	if r.lifecycle != nil {
		endErr = r.lifecycle.EndMacroRuns()
	}
	life := script.NewLifecycle(r.clk, r.hostOptions(gen, spec))
	ctx, cancel := context.WithCancel(context.Background())
	r.pending = &pendingScriptReload{
		life: life, result: life.LoadPrograms(ctx, func(ctx context.Context) (script.Programs, error) {
			return r.programs(ctx, spec)
		}), cancel: cancel, gen: gen, spec: spec,
		pollDeadline: r.clk.Now().Add(reloadPollInterval),
	}
	return endErr
}

// pollReload adopts a completed candidate on the owning slot goroutine. The
// active runtime and watcher are left in place while setup or compilation is
// still running, so declaration input remains responsive.
func (r *scriptRuntime) pollReload() {
	p := r.pending
	if p == nil {
		return
	}
	select {
	case result := <-p.result:
		p.cancel()
		r.pending = nil
		if result.Err != nil {
			_ = p.life.Dispose()
			r.reloadErr = result.Err
			if script.IsWallBudget(result.Err) {
				r.stopForWall()
			}
			return
		}
		// Build and watcher construction happen before touching the active
		// runtime. A failed candidate therefore leaves both the old lifecycle
		// and its file watcher running, so a later save can recover.
		var nextWatcher *script.ScriptWatcher
		if p.spec.watch && p.spec.cfg != nil {
			var err error
			nextWatcher, err = r.newWatcher(p.life, p.spec.root, p.spec.cfg)
			if err != nil {
				go func() { _ = result.Host.Discard() }()
				_ = p.life.Dispose()
				r.reloadErr = err
				return
			}
		}
		// The old runtime's effects on the gamepad end before the new runtime
		// activates: its cancel, coming after, would end the new one's too.
		if r.lifecycle != nil {
			if h := r.lifecycle.Current(); h != nil {
				h.CancelEffects()
			}
		}
		if err := p.life.Swap(result.Host); err != nil {
			_ = p.life.Dispose()
			if p.life.TeardownsPending() {
				r.retired = append(r.retired, p.life)
			}
			r.reloadErr = err
			if script.IsWallBudget(err) {
				r.stopForWall()
			}
			return
		}
		old := r.lifecycle
		oldWatcher, oldStop, oldDone := r.watcher, r.stop, r.done
		r.lifecycle, r.activeRoot = p.life, p.spec.root
		r.gen = p.gen
		r.reloadErr = nil
		r.forgetErrors()
		// Stop the old watcher only after the candidate is committed. This
		// keeps a valid old watcher available throughout candidate setup.
		if oldWatcher != nil {
			oldWatcher.Cancel()
		}
		if oldStop != nil {
			oldStop()
		}
		if oldDone != nil {
			<-oldDone
		}
		r.stop, r.done, r.watcher = nil, nil, nil
		if old != nil {
			if h := old.Current(); h != nil {
				h.Suspend()
			}
			if err := old.Dispose(); err != nil {
				r.reloadErr = err
			}
			if old.TeardownsPending() {
				r.retired = append(r.retired, old)
			}
		}
		if nextWatcher != nil {
			r.startWatcher(nextWatcher)
		}
	default:
		if p.pollDeadline <= r.clk.Now() {
			p.pollDeadline = r.clk.Now().Add(reloadPollInterval)
		}
	}
}

// cancelPending abandons a candidate and drains its buffered result when the
// worker eventually finishes. This ensures a late successful host is always
// disposed rather than leaking registrations or output ownership.
func (r *scriptRuntime) cancelPending() {
	p := r.pending
	if p == nil {
		return
	}
	r.pending = nil
	p.cancel()
	done := make(chan struct{})
	r.cancelDone = done
	go func() {
		defer close(done)
		if result, ok := <-p.result; ok && result.Host != nil {
			_ = result.Host.Discard()
		}
	}()
}

func (r *scriptRuntime) newWatcher(life *script.Lifecycle, root string, cfg *profile.ScriptConfig) (*script.ScriptWatcher, error) {
	entry := cfg.Entry
	if !filepath.IsAbs(entry) {
		entry = filepath.Join(root, entry)
	}
	data, err := os.ReadFile(entry)
	if err != nil {
		return nil, fmt.Errorf("script: read entry for watcher: %w", err)
	}
	w, err := script.NewScriptWatcher(life, script.ScriptWatchOptions{
		Clock: r.clk, Path: entry, Initial: sha256.Sum256(data),
		OnError: func(err error) { r.reloadErr = errors.Join(r.reloadErr, err) },
		// A saved script that was installed ends a failure episode, as a reload
		// does. The runtime that the lifecycle restarts by itself after an
		// interruption does not come through here: its errors stay.
		OnInstall: r.forgetErrors,
		OnChange: func() {
			select {
			case r.wake <- struct{}{}:
			default:
			}
		},
		// The macros build with the entry: a saved entry rebuilds the
		// whole runtime, macros included.
		Programs: func(ctx context.Context) (script.Programs, error) {
			latest := r.def.spec()
			if latest.cfg == nil {
				return script.Programs{}, errors.New("script: no profile script configuration")
			}
			return r.programs(ctx, latest)
		},
	})
	if err != nil {
		return nil, err
	}
	return w, nil
}

func (r *scriptRuntime) startWatcher(w *script.ScriptWatcher) {
	r.watcher = w
	r.done = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	r.stop = cancel
	go func() {
		defer close(r.done)
		_ = w.Run(ctx)
	}()
}

func (r *scriptRuntime) stopWatcher() {
	if r.stop == nil && r.watcher == nil {
		return
	}
	if r.watcher != nil {
		r.watcher.Cancel()
	}
	if r.stop != nil {
		r.stop()
	}
	if r.done != nil {
		<-r.done
	}
	r.stop, r.done, r.watcher = nil, nil, nil
}

func (r *scriptRuntime) Dispose() error {
	defer r.flushLogs()
	r.cancelPending()
	r.stopWatcher()
	if r.lifecycle == nil {
		return nil
	}
	old := r.lifecycle
	if h := old.Current(); h != nil {
		h.Suspend()
	}
	err := old.Dispose()
	r.lifecycle = nil
	if old.TeardownsPending() {
		r.retired = append(r.retired, old)
	}
	return err
}

func (r *scriptRuntime) flushLogs() {
	if r.def.log == nil {
		return
	}
	entries, dropped := r.logs.Drain()
	for _, entry := range entries {
		level := slog.LevelInfo
		switch entry.Level {
		case "debug":
			level = slog.LevelDebug
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		}
		if r.def.debugOnly {
			level = slog.LevelDebug
		}
		attrs := []any{"source", "script", "slot", entry.Slot,
			"gen", entry.Generation, "device", entry.Device, "device_key", entry.DeviceKey, "profile", entry.Profile,
			"script_level", entry.Level, "script_ms", float64(entry.Time) / float64(time.Millisecond)}
		if entry.Macro != "" {
			attrs = append(attrs, "macro", entry.Macro)
		}
		r.def.log.Log(context.Background(), level, entry.Message, attrs...)
	}
	if dropped > 0 {
		level := slog.LevelWarn
		if r.def.debugOnly {
			level = slog.LevelDebug
		}
		r.def.log.Log(context.Background(), level, "script log messages dropped", "slot", r.index, "count", dropped)
	}
}

var _ slot.ScriptRuntime = (*scriptRuntime)(nil)
