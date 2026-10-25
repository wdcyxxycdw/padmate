package script

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

// MacroRunLimit is how long a run of a macro may last: past it the run is
// canceled and its keys are released (SCR-50, design §6.3).
const MacroRunLimit = 60 * time.Second

var (
	// ErrMacroNotFound is the error of a macro a binding names that the
	// runtime does not have, or whose file does not exist.
	ErrMacroNotFound = errors.New("macro file not found")
	// ErrMacroDisabled is the error of a macro stopped after it used too
	// much CPU or overflowed its stack; it loads again once its file
	// changes (DisabledMacros).
	ErrMacroDisabled = errors.New("macro was stopped after it used too much CPU; save the file to retry")
	// errMacroTimeout is the error of a run canceled at MacroRunLimit.
	errMacroTimeout = errors.New("macro ran longer than 60 s and was stopped")
)

// Programs is what a runtime runs: the legacy entry, if the profile has
// one, and the macros its bindings use (design §5.3).
type Programs struct {
	Entry  *goja.Program
	Macros []MacroProgram
}

// MacroProgram is one macro of Programs: a top-level file of the profile's
// macros folder, named as on disk.
type MacroProgram struct {
	File string
	// Program is the macro's bundle (compile.BuildOptions.Macro), compiled
	// with the bare file name as its name, so goja finds the sources of its
	// source map.
	Program *goja.Program
	// Err is the error of the build, or ErrMacroNotFound; the macro does
	// not load and running it reports Err.
	Err error
	// Version identifies the file's contents. A macro disabled after an
	// interruption stays disabled while its Version is the same.
	Version string
}

// ProgramsFunc builds the programs of a runtime on a lifecycle worker.
type ProgramsFunc func(context.Context) (Programs, error)

// programsOf adapts a BuildFunc of a legacy entry. A nil build stays nil.
func programsOf(build BuildFunc) ProgramsFunc {
	if build == nil {
		return nil
	}
	return func(ctx context.Context) (Programs, error) {
		p, err := build(ctx)
		if err == nil && p == nil {
			err = errors.New("script: nil program")
		}
		return Programs{Entry: p}, err
	}
}

// MacroError is an error of one macro: of its build, its load or a run
// (design §7). Each JavaScript call knows the scope it runs for, so the
// file is that of the call, not guessed from a stack. Err is a diagnostic
// *Error: its diagnostics without a file name the macro, and file names in
// the macros folder are relative to it, as "复制全部.ts:3:9".
type MacroError struct {
	File string
	Err  error
}

func (e *MacroError) Error() string { return e.Err.Error() }

func (e *MacroError) Unwrap() error { return e.Err }

// MacroErrorsOf returns the macros' errors in err, which may join several.
func MacroErrorsOf(err error) []*MacroError {
	var out []*MacroError
	var walk func(error)
	walk = func(err error) {
		switch e := err.(type) { //nolint:errorlint // walks the error tree itself
		case nil:
		case *MacroError:
			out = append(out, e)
		case interface{ Unwrap() []error }:
			for _, inner := range e.Unwrap() {
				walk(inner)
			}
		case interface{ Unwrap() error }:
			walk(e.Unwrap())
		}
	}
	walk(err)
	return out
}

// attributeMacro diagnoses err as an error of the macro file.
func attributeMacro(file string, phase Phase, err error) error {
	if err == nil {
		return nil
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		var out []error
		for _, inner := range j.Unwrap() {
			out = append(out, attributeMacro(file, phase, inner))
		}
		return errors.Join(out...)
	}
	if me := (*MacroError)(nil); errors.As(err, &me) {
		return err
	}
	var de *Error
	if !errors.As(Diagnose(phase, err), &de) {
		return &MacroError{File: file, Err: err}
	}
	clone := &Error{Cause: de.Cause, Diagnostics: slices.Clone(de.Diagnostics)}
	for i := range clone.Diagnostics {
		d := &clone.Diagnostics[i]
		// A macro is built with its macros folder as the root (SCR-51), so
		// the other files are named relative to the folder already.
		if d.File == "" {
			d.File = file
		}
	}
	return &MacroError{File: file, Err: clone}
}

// isInterruption reports an error after which a runtime must not run more
// JavaScript in this turn: a watchdog budget or a stack overflow. The
// Lifecycle then releases and restarts the runtime.
func isInterruption(err error) bool {
	if budgetFailure(err) != nil {
		return true
	}
	var overflow *goja.StackOverflowError
	return errors.As(err, &overflow)
}

// cpuInterruption reports an interruption that disables a macro: the CPU
// budget or a stack overflow. A wall-clock wedge stops the whole runtime.
func cpuInterruption(err error) bool {
	if b := budgetFailure(err); b != nil {
		return b.Clock != "wall"
	}
	var overflow *goja.StackOverflowError
	return errors.As(err, &overflow)
}

// DisabledMacros remembers the macros a slot stopped after an interruption
// (design §7), by file and Version. It is safe for concurrent use: a
// lifecycle worker reads it while a runtime loads.
type DisabledMacros struct {
	mu       sync.Mutex
	versions map[string]string
}

// Disable stops the macro file at version.
func (d *DisabledMacros) Disable(file, version string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.versions == nil {
		d.versions = make(map[string]string)
	}
	d.versions[file] = version
}

// Disabled reports whether the macro file at version is stopped. Another
// version of the file enables it again.
func (d *DisabledMacros) Disabled(file, version string) bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	v, ok := d.versions[file]
	if ok && v != version {
		delete(d.versions, file)
		return false
	}
	return ok
}

// Files returns the stopped macros, sorted.
func (d *DisabledMacros) Files() []string {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	files := make([]string, 0, len(d.versions))
	for file := range d.versions {
		files = append(files, file)
	}
	slices.Sort(files)
	return files
}

// macroFile is one macro of a runtime.
type macroFile struct {
	file    string
	version string
	program *goja.Program
	// err is why the macro does not run: its build, its load, a missing
	// file or a disabled macro. A runtime reports it once.
	err      error
	reported bool
	fn       goja.Callable // the default export, once loaded
	run      *outputScope  // the run in progress
}

// installMacros records the macros and defines MacroAPIGlobal before any
// script code runs: a frozen object whose member for each file is that
// file's bridge, for the runtime's life (design §5.3).
func (h *Host) installMacros(programs []MacroProgram) {
	programs = slices.Clone(programs)
	slices.SortStableFunc(programs, func(a, b MacroProgram) int { return strings.Compare(a.File, b.File) })
	vm := h.VM
	freeze, _ := goja.AssertFunction(vm.Get("Object").ToObject(vm).Get("freeze"))
	api := vm.NewObject()
	for _, mp := range programs {
		if h.macros[mp.File] != nil {
			continue
		}
		f := &macroFile{file: mp.File, version: mp.Version, program: mp.Program}
		switch {
		case mp.Err != nil:
			f.err = attributeMacro(mp.File, PhaseCompile, mp.Err)
		case mp.Program == nil:
			f.err = attributeMacro(mp.File, PhaseCompile, ErrMacroNotFound)
		case h.disabled.Disabled(mp.File, mp.Version):
			f.err = attributeMacro(mp.File, PhaseSetup, ErrMacroDisabled)
		}
		h.macros[mp.File] = f
		h.macroOrder = append(h.macroOrder, f)
		if err := api.DefineDataProperty(mp.File, h.macroBridge(f, freeze), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE); err != nil {
			panic(fmt.Sprintf("script: define macro bridge %q: %v", mp.File, err))
		}
	}
	if _, err := freeze(goja.Undefined(), api); err != nil {
		panic(fmt.Sprintf("script: freeze %s: %v", compile.MacroAPIGlobal, err))
	}
	if err := vm.GlobalObject().DefineDataProperty(compile.MacroAPIGlobal, api, goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE); err != nil {
		panic(fmt.Sprintf("script: define %s: %v", compile.MacroAPIGlobal, err))
	}
}

// macroBridge is the padmate module of the macro f: compile.MacroExports,
// whose key and sleep use the scope of f's run, and throw a TypeError when
// f is not running (at its top level, say). Its objects are its own, so
// macros share no JavaScript objects, only the session.
func (h *Host) macroBridge(f *macroFile, freeze goja.Callable) *goja.Object {
	vm := h.VM
	running := func() *outputScope {
		if f.run == nil {
			panic(vm.NewTypeError(fmt.Sprintf("macro %q is not running", f.file)))
		}
		return f.run
	}
	o := vm.NewObject()
	set(o, "host", h.hostInfo())
	set(o, "key", h.keyAPI(running, func(c keys.Chord) bool { return running().isChordHeld(c) }, func(s *outputScope) { h.releaseScope(s, true) }))
	setFunction(vm, o, "sleep", 1, func(call goja.FunctionCall) goja.Value { return h.sleepIn(running(), call.Argument(0)) })
	setFunction(vm, o, "now", 0, h.now)
	set(o, "log", h.logAPI(f.file))
	set(o, "pad", h.pad())
	h.installSessionAPI(o)
	if _, err := freeze(goja.Undefined(), o); err != nil {
		panic(fmt.Sprintf("script: freeze macro bridge %q: %v", f.file, err))
	}
	return o
}

// loadMacros runs each macro's bundle, in file name order, in a handler
// watchdog call of its own, and keeps its default export. A macro that
// fails keeps its error and the next one loads; one interrupted for its
// CPU use is also disabled. Only a wall-clock wedge returns an error: it
// fails the runtime, as it does a legacy entry's setup.
func (h *Host) loadMacros() error {
	for _, f := range h.macroOrder {
		if f.err != nil {
			continue
		}
		h.macroCall = f.file
		h.macroLoad = true
		_, err := h.withWatchdog(WatchdogCallHandler, func() (goja.Value, error) {
			if _, err := h.VM.RunProgram(f.program); err != nil {
				return nil, err
			}
			exports, ok := h.VM.Get(compile.MacroGlobal).(*goja.Object)
			if !ok {
				return nil, errors.New("the macro has no exports")
			}
			fn, ok := goja.AssertFunction(exports.Get("default"))
			if !ok {
				return nil, errors.New("the default export of a macro must be a function")
			}
			f.fn = fn
			return nil, nil
		})
		h.macroLoad = false
		h.macroCall = ""
		if err = errors.Join(append([]error{err}, h.takeRejections()...)...); err == nil {
			continue
		}
		f.fn = nil
		f.err = attributeMacro(f.file, PhaseSetup, err)
		if IsWallBudget(err) {
			return f.err
		}
		if cpuInterruption(err) {
			h.disabled.Disable(f.file, f.version)
		}
	}
	return nil
}

// disableMacro stops the macro file after an interruption of one of its
// calls: the runtimes built later skip it until its file changes.
func (h *Host) disableMacro(file string) {
	if f := h.macros[file]; f != nil && f.program != nil {
		h.disabled.Disable(file, f.version)
	}
}

// MacroErrors returns the errors of the macros that did not load: their
// build, their load, or disabled; sorted by file. A describe-mode runtime
// reports with it which macros would run.
func (h *Host) MacroErrors() []*MacroError {
	var out []*MacroError
	for _, f := range h.macroOrder {
		out = append(out, MacroErrorsOf(f.err)...)
	}
	return out
}

// RunMacro starts a run of the macro file (design §6.1): it calls the
// default export under a handler watchdog, and a returned thenable keeps
// the run going until it settles. A macro that is not there, did not load
// or is disabled reports its error once per runtime. A macro already
// running ignores the trigger.
func (h *Host) RunMacro(file string, _ Event) error {
	if h.mode == "describe" {
		return attributeMacro(file, PhaseHandler, errors.New("macros cannot run in describe mode"))
	}
	if h.disposed || !h.active || h.setup || h.candidate {
		return nil
	}
	f := h.macros[file]
	if f == nil {
		f = &macroFile{file: file, err: attributeMacro(file, PhaseHandler, ErrMacroNotFound)}
		h.macros[file] = f
	}
	if f.err != nil || f.fn == nil {
		if f.reported {
			return nil
		}
		f.reported = true
		return errors.Join(f.err, h.takeMacroErrs())
	}
	if f.run != nil {
		h.logs.push(h.logEntry("debug", f.file, fmt.Sprintf("macro %s is still running; the press is ignored", f.file)))
		return nil
	}
	s := newOutputScope(h.newOwner(), h.budget.fork(), NewEventLoop(h.clock))
	s.macro = f
	s.deadline = h.clock.Now().Add(MacroRunLimit)
	f.run = s
	h.runs = append(h.runs, s)
	return h.callMacro(s, func() error {
		v, err := f.fn(goja.Undefined())
		if err != nil {
			h.endRun(s, nil)
			return err
		}
		if !isThenable(v) {
			h.endRun(s, nil)
			return nil
		}
		promise := v.(*goja.Object)
		then, _ := goja.AssertFunction(promise.Get("then"))
		done := h.VM.ToValue(func(goja.FunctionCall) goja.Value {
			if f.run == s {
				h.endRun(s, nil)
			}
			return goja.Undefined()
		})
		failed := h.VM.ToValue(func(call goja.FunctionCall) goja.Value {
			if f.run == s {
				h.endRun(s, diagnoseReason(PhaseHandler, "", call.Argument(0)))
			}
			return goja.Undefined()
		})
		if then == nil {
			h.endRun(s, nil)
			return nil
		}
		if _, err := then(promise, done, failed); err != nil {
			h.endRun(s, nil)
			return err
		}
		return nil
	})
}

// callMacro makes fn one handler watchdog call for the run s: its error,
// the Promise rejections and the scope's errors are the macro's. It also
// returns the errors of runs that ended meanwhile.
func (h *Host) callMacro(s *outputScope, fn func() error) error {
	previous, previousCall := h.promisePhase, h.macroCall
	h.promisePhase, h.macroCall = PhaseHandler, s.macro.file
	defer func() { h.promisePhase, h.macroCall = previous, previousCall }()
	_, err := h.withWatchdog(WatchdogCallHandler, func() (goja.Value, error) { return nil, fn() })
	errs := append([]error{err}, h.takeRejections()...)
	errs = append(errs, s.takeErrors()...)
	return errors.Join(attributeMacro(s.macro.file, PhaseHandler, errors.Join(errs...)), h.takeMacroErrs())
}

func (h *Host) takeMacroErrs() error {
	errs := h.macroErrs
	h.macroErrs = nil
	return errors.Join(errs...)
}

// endRun ends the run s normally (design §6.3): its timers are canceled,
// its taps are abandoned, with only the step the Arbiter already has
// finishing, and each key it holds goes up with Up, so the Arbiter keeps
// every minimum press time and the references of other owners. Only if an
// Up does not reach the Arbiter is the owner released.
func (h *Host) endRun(s *outputScope, err error) {
	if s.ended {
		return
	}
	s.loop.Clear()
	for op := range s.pendingTaps {
		delete(s.pendingTaps, op)
		if op.chord.IsValid() {
			h.recordOutput(s, op.chord, true)
		}
		op.dropped = true
	}
	h.tapCompletions.drop(s)
	if !h.upHeld(s) && h.output != nil {
		s.addError(h.output.ReleaseOwner(s.owner))
	}
	h.closeRun(s, err)
}

// cancelRun ends the run s at a time limit or a safety boundary: its timers
// and taps are canceled and its owner released at once.
func (h *Host) cancelRun(s *outputScope, err error) {
	if s.ended {
		return
	}
	s.loop.Clear()
	h.releaseScope(s, false)
	h.closeRun(s, err)
}

// closeRun closes the scope of the run s and keeps its errors for the next
// report.
func (h *Host) closeRun(s *outputScope, err error) {
	s.ended = true
	f := s.macro
	if f.run == s {
		f.run = nil
	}
	h.runs = slices.DeleteFunc(h.runs, func(r *outputScope) bool { return r == s })
	errs := append(s.takeErrors(), err)
	if err := attributeMacro(f.file, PhaseHandler, errors.Join(errs...)); err != nil {
		h.macroErrs = append(h.macroErrs, err)
	}
}

// stopRuns cancels every run: a safety boundary releases all keys.
func (h *Host) stopRuns() {
	for _, s := range slices.Clone(h.runs) {
		h.cancelRun(s, nil)
	}
}

// runMacrosDue cancels the runs past MacroRunLimit and runs each run's due
// timers, each in a watchdog call of its run. stop reports an
// interruption, after which RunDue runs nothing more.
func (h *Host) runMacrosDue() (stop bool, err error) {
	if len(h.runs) == 0 {
		return false, nil
	}
	now := h.clock.Now()
	var errs []error
	for _, s := range slices.Clone(h.runs) {
		if s.ended {
			continue
		}
		if !now.Before(s.deadline) {
			h.cancelRun(s, errMacroTimeout)
			continue
		}
		interrupted := false
		_, _ = s.loop.RunDueWith(now, func(fn func()) error {
			callErr := h.callMacro(s, func() error { fn(); return nil })
			if callErr == nil {
				return nil
			}
			errs = append(errs, callErr)
			if isInterruption(callErr) {
				interrupted = true
				return callErr
			}
			return nil
		})
		if interrupted {
			return true, errors.Join(errs...)
		}
	}
	errs = append(errs, h.takeMacroErrs())
	return false, errors.Join(errs...)
}
