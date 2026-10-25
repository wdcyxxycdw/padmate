package script

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

const apiVersion = "0.21"

// ErrOutputBudget reports a script output request rejected by the
// per-runtime output limiter. Releases are never rejected by this budget.
var ErrOutputBudget = errors.New("script: output budget exceeded")

// Feature names accepted by host.has. A feature is a runtime capability (for
// example an attached overlay or a device sensor), so its availability is
// deliberately independent from the script API version.
type Feature string

const (
	FeatureHUD       Feature = "hud"
	FeatureRumble    Feature = "rumble"
	FeatureLED       Feature = "led"
	FeatureGyro      Feature = "gyro"
	FeatureTouchpad  Feature = "touchpad"
	FeatureMouse     Feature = "mouse"
	FeatureClipboard Feature = "clipboard"
	FeatureExec      Feature = "exec"
)

// State is the lifecycle state of a script runtime.
type State string

const (
	StateBuilding State = "Building"
	StateStarting State = "Starting"
	StateRunning  State = "Running"
	StateDisposed State = "Disposed"
)

// Registration is an input handler installed by a script.
type Registration struct {
	Control, Gesture string
	Handler          goja.Callable
	Observe          bool
	Scope            RegistrationScope
	Layer            string
	Priority         int
	Label            string
	Timing           profile.Timing
	Claim            bool
	disposed         bool
}

// RegistrationScope identifies where a script registration participates in
// the level lookup. M5 exposes only the base and global scopes; layer scopes
// are added with the M6 layer API.
type RegistrationScope uint8

const (
	ScopeBase RegistrationScope = iota
	ScopeGlobal
	ScopeLayer
)

// OutputSink is the slot's output-arbiter seam. The slot owns the arbiter;
// script code supplies only its runtime owner and parsed chords.
type OutputSink interface {
	Down(output.Owner, keys.Chord) error
	Up(output.Owner, keys.Chord) error
	Tap(output.Owner, keys.Chord, time.Duration) error
	ReleaseOwner(output.Owner) error
}

// HostOptions configures a Host's clock, output sink and runtime owner. The
// owner source is always normalized to output.SourceScript; Slot, Gen and
// Cycle are preserved.
type HostOptions struct {
	Clock  clock.Clock
	Output OutputSink
	Owner  output.Owner
	// OwnerCycles, when set, assigns cycles across multiple Lifecycle values
	// owned by one slot. It prevents a canceled old candidate releasing a
	// newly resumed host with the same slot and generation.
	OwnerCycles *atomic.Uint64
	Features    map[Feature]bool
	Pad         PadState
	Logs        *LogBuffer
	Mode        string // run (default), describe, or test
	Profile     string // stable profile identity supplied by the owner
	Params      map[string]any
	// Layers is used only by activated handlers on the owner slot goroutine.
	Layers     engine.LayerAccess
	LayerState engine.LayerState
	LayerNames []string
	// Wake notifies an idle owner loop that an output completion is ready.
	Wake func()
	// TickHz is the continuous-input frequency; zero uses 125 Hz.
	TickHz int
	// Session is shared by runtimes of one supervisor-owned profile key.
	Session *Session
	// Environment supplies in-memory foreground and library snapshots.
	Environment *Environment
	// SwitchProfile must only enqueue a request; it must not wait for slots.
	SwitchProfile func(string) error
	// DisabledMacros remembers the macros stopped after an interruption
	// (SCR-50); the runtimes of one slot share it, so a rebuilt runtime
	// does not load them again until their file changes. A Lifecycle
	// creates one when it is nil.
	DisabledMacros *DisabledMacros
}

// PadState is an immutable copy of the slot's most recent input state.
type PadState struct {
	Connected      bool
	Info           input.DeviceInfo
	Key            string
	Down           [control.Count]bool
	Since          [control.Count]clock.Time
	Axes           [control.AxisCount]int16
	HasControl     [control.Count]bool
	HasAxis        [control.AxisCount]bool
	Analog         profile.Analog
	AnalogResolved bool
	// Power is the battery state the gamepad last reported, HasPower false until
	// it reported one (pad.info.battery, IN-12).
	Power    input.Power
	HasPower bool
}

// OutputEvent records a key transition requested by the host. The slot's
// output arbiter will consume these events in the production integration.
type OutputEvent struct {
	Spec  string
	Up    bool
	Time  clock.Time
	Owner output.Owner
	Chord keys.Chord
	// Text is the character of a key.type or a text step that was typed (SCR-26);
	// Spec and Chord are empty then.
	Text string
}

// Event is the v0 input event delivered to a handler.
type Event struct {
	Control, Gesture string
	Time             clock.Time
	Duration         time.Duration
	Count            int
	Scope            string
	Cycle            int64
	Held             bool
}

// Host is the v0 goja host. It is owned by one slot loop goroutine at a time.
// Host does not start goroutines and all timers are driven by RunDue.
type Host struct {
	VM   *goja.Runtime
	Loop *EventLoop

	clock clock.Clock
	// budget is the runtime's output budget, the base scope's; each run of
	// a macro forks it and so shares its token bucket.
	budget *OutputBudget
	// base is the output scope of the legacy script's key API (design §6.2).
	base           *outputScope
	tapCompletions *tapCompletionQueue
	// macros are the runtime's macros by file name, macroOrder the same in
	// file name order, and runs the macros running now, in start order.
	macros     map[string]*macroFile
	macroOrder []*macroFile
	runs       []*outputScope
	// macroErrs are errors already attributed to a macro, reported by the
	// next call that returns errors.
	macroErrs []error
	// macroCall is the macro file whose top level or run the JavaScript
	// call in progress is, "" for the legacy script: console's lines name
	// it, as the macro's own log does.
	macroCall string
	// disabled remembers the macros interrupted for their CPU use, shared
	// with the Lifecycle and the runtimes it builds after this one.
	disabled *DisabledMacros
	// watchdogCalls counts the calls made under a watchdog; tests use it.
	watchdogCalls int
	// panels are the runtime's hud panels in the order first set; toasts counts
	// the toasts of the current second (SCR-22).
	panels []HUDPanel
	toasts rateWindow
	// The effects of the script on its gamepad (SCR-23): effectRate counts the
	// calls of the current second, effectDev is the gamepad they went to, rumbling
	// and lit say that a rumble or the LED may be on it, and rumbleBody and
	// rumbleTrig are the two channels of the rumble that run. cancelEffects ends
	// them when the runtime stops being the active one.
	effectRate             rateWindow
	effectDev              input.DeviceID
	rumbling, lit          bool
	rumbleBody, rumbleTrig rumbleLevel
	// macroLoad is set while the macros' top levels run, where effects throw.
	macroLoad       bool
	setup           bool
	active          bool
	state           State
	disposed        bool
	watchdogEnabled bool
	output          OutputSink
	candidateOutput OutputSink
	candidate       bool
	features        map[Feature]bool
	padState        PadState
	ignoreRelease   [control.Count]bool
	gestureIgnored  [control.Count]bool
	runtimeID       uint64
	mode            string
	passRequested   bool

	onActivate goja.Callable
	teardown   goja.Callable

	registrations     []Registration
	outputs           []OutputEvent
	logs              *LogBuffer
	rejections        map[*goja.Promise]error
	trackingRejection bool
	promisePhase      Phase
	names             map[string]goja.Value
	eventProto        *goja.Object
	profile           string
	rawParams         map[string]any
	paramsError       error
	paramSpecs        map[*goja.Object]ParamInfo
	paramSchema       map[string]ParamInfo
	paramsValue       *goja.Object
	contextValue      *goja.Object
	actions           map[string]namedAction
	continuous        []*continuousRegistration
	tickPeriod        time.Duration
	session           *sessionView
	gestureStates     map[gestureKey]*gestureState
	gestureSequence   uint64
	ownerCycles       *atomic.Uint64
	layerAccess       engine.LayerAccess
	layerState        engine.LayerState
	layerNames        []string
	layerListeners    []*layerListener
	layerHolds        []func()
	pendingLayerOps   []pendingLayerOp
	environment       *Environment
	environmentWake   func()
	appVersion        *AppInfo
	appListeners      []*layerListener
	switchProfile     func(string) error
}

// NewHost creates a host driven by c. Omitting c uses the process real clock.
func NewHost(cs ...clock.Clock) *Host {
	var c clock.Clock
	if len(cs) > 0 {
		c = cs[0]
	}
	return NewHostWithOptions(HostOptions{Clock: c})
}

// NewHostWithOptions creates a host with the output sink and runtime owner
// used by key.down/up/tap and releaseAll. The caller's owner source is
// normalized to script, including when a declarative owner is supplied; a
// zero owner therefore becomes a valid source owner for direct Host tests and
// dry runs.
func NewHostWithOptions(opts HostOptions) *Host {
	c := opts.Clock
	if c == nil {
		c = clock.Real()
	}
	// Script output never borrows a declarative owner. Preserve slot/gen and
	// any explicit runtime token, but always use the script source.
	opts.Owner.Source = output.SourceScript
	vm := goja.New()
	loop, budget := NewEventLoop(c), NewOutputBudget(c)
	h := &Host{
		VM: vm, clock: c, Loop: loop, budget: budget, base: newOutputScope(opts.Owner, budget, loop), names: make(map[string]goja.Value), state: StateBuilding,
		environment: opts.Environment, environmentWake: opts.Wake, switchProfile: opts.SwitchProfile,
		output: opts.Output, features: cloneFeatures(opts.Features), promisePhase: PhaseHandler,
		padState:    opts.Pad,
		ownerCycles: opts.OwnerCycles, layerAccess: opts.Layers, layerState: opts.LayerState, layerNames: append([]string(nil), opts.LayerNames...),
		gestureStates: make(map[gestureKey]*gestureState), gestureSequence: opts.Owner.Cycle, runtimeID: gestureRuntimeIDs.Add(1),
		tapCompletions: &tapCompletionQueue{wake: opts.Wake}, macros: make(map[string]*macroFile), disabled: opts.DisabledMacros,
	}
	h.mode = opts.Mode
	hz := opts.TickHz
	if hz <= 0 {
		hz = defaultTickHz
	}
	h.tickPeriod = time.Second / time.Duration(hz)
	if h.tickPeriod < time.Millisecond {
		h.tickPeriod = time.Millisecond
	}
	if opts.Session == nil {
		opts.Session = NewSession()
	}
	h.session = opts.Session.fork()
	if h.mode == "" {
		h.mode = "run"
	}
	h.logs = opts.Logs
	if h.logs == nil {
		h.logs = NewLogBuffer(nil)
	}
	vm.SetMaxCallStackSize(2000)
	vm.SetPromiseRejectionTracker(func(p *goja.Promise, operation goja.PromiseRejectionOperation) {
		switch operation {
		case goja.PromiseRejectionReject:
			if h.rejections == nil {
				h.rejections = make(map[*goja.Promise]error)
			}
			if h.trackingRejection {
				h.rejections[p] = Diagnose(h.promisePhase, errors.New("unhandled Promise rejection while formatting another rejection"))
				return
			}
			h.trackingRejection = true
			defer func() { h.trackingRejection = false }()
			h.rejections[p] = diagnoseRejection(h.promisePhase, p.Result())
		case goja.PromiseRejectionHandle:
			delete(h.rejections, p)
		}
	})
	vm.SetTimeSource(func() time.Time { return time.Unix(0, int64(c.Now())).UTC() })
	h.eventProto = vm.NewObject()
	h.initActions(opts)
	h.installEventMethods()
	h.installGlobals()
	h.UpdatePad(opts.Pad)
	return h
}

func (h *Host) installGlobals() {
	vm := h.VM
	host := vm.NewObject()
	set(host, "host", h.hostInfo())
	if err := host.DefineDataProperty("tickMs", vm.ToValue(float64(h.tickPeriod)/float64(time.Millisecond)), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE); err != nil {
		panic(err)
	}
	setFunction(vm, host, "defineScript", 1, func(call goja.FunctionCall) goja.Value { return call.Argument(0) })
	h.installActions(host)
	setFunction(vm, host, "on", 3, h.on)
	setFunction(vm, host, "global", 0, h.global)
	setFunction(vm, host, "observe", 2, h.observe)
	set(host, "pad", h.pad())
	set(host, "key", h.key())
	setFunction(vm, host, "now", 0, h.now)
	setFunction(vm, host, "sleep", 1, h.sleep)
	setFunction(vm, host, "every", 2, h.every)
	h.installGestureAPI(host)
	set(host, "log", h.log())
	h.installContinuousAPI(host)
	h.installSessionAPI(host)
	h.installEnvironmentAPI(host)
	set(host, "hud", h.hud())
	set(vm.GlobalObject(), compile.HostGlobal, host)
	// Keep the scaffold's global console declaration executable at runtime.
	// It shares the host logger so console calls follow the same diagnostics
	// path as padmate.log and do not require a Node or browser global.
	set(vm.GlobalObject(), "console", h.console())
}

func (h *Host) hostInfo() *goja.Object {
	o := h.VM.NewObject()
	set(o, "version", apiVersion)
	set(o, "api", apiVersion)
	set(o, "mode", h.mode)
	setFunction(h.VM, o, "has", 1, func(call goja.FunctionCall) goja.Value {
		return h.VM.ToValue(h.hasFeature(Feature(call.Argument(0).String())))
	})
	return o
}

// hasFeature is host.has: a static feature the host was given, or one that depends
// on the moment: an overlay that listens (hud), a gamepad that can rumble or
// light up with the profile's switch on (rumble, led).
func (h *Host) hasFeature(feature Feature) bool {
	if h.features[feature] {
		return true
	}
	switch feature {
	case FeatureHUD:
		return h.hudConnected()
	case FeatureRumble, FeatureLED:
		return h.effectsAvailable(feature)
	}
	return false
}

func cloneFeatures(in map[Feature]bool) map[Feature]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[Feature]bool, len(in))
	for feature, enabled := range in {
		if enabled && knownFeature(feature) {
			out[feature] = true
		}
	}
	return out
}

func knownFeature(feature Feature) bool {
	switch feature {
	case FeatureHUD, FeatureRumble, FeatureLED, FeatureGyro, FeatureTouchpad, FeatureMouse, FeatureClipboard, FeatureExec:
		return true
	default:
		return false
	}
}

func (h *Host) installEventMethods() {
	setFunction(h.VM, h.eventProto, "pass", 0, func(goja.FunctionCall) goja.Value {
		h.passRequested = true
		return goja.Undefined()
	})
}

// Activate runs a compiled program and invokes the default defineScript
// setup. Setup must be synchronous; a returned thenable is rejected.
func (h *Host) Activate(p *goja.Program) error {
	if p == nil {
		h.state = StateDisposed
		return Diagnose(PhaseSetup, errors.New("script: nil program"))
	}
	return h.ActivatePrograms(Programs{Entry: p})
}

// ActivatePrograms installs the macros' bridges, runs the legacy entry and
// its setup when there is one, then loads each macro (design §5.3). A
// failure of the entry fails the whole runtime and no macro loads; a macro
// that fails to load keeps its error and does not run, and the others load.
// Only a wall-clock wedge while a macro loads fails the runtime.
func (h *Host) ActivatePrograms(ps Programs) error {
	previous := h.promisePhase
	h.promisePhase = PhaseSetup
	defer func() { h.promisePhase = previous }()
	h.installMacros(ps.Macros)
	if ps.Entry != nil {
		_, err := h.withWatchdog(WatchdogCallSetup, func() (goja.Value, error) {
			return nil, h.runEntry(ps.Entry)
		})
		if err := errors.Join(Diagnose(PhaseSetup, err), h.takeErrors()); err != nil {
			return err
		}
	}
	if err := h.loadMacros(); err != nil {
		h.state = StateDisposed
		return err
	}
	_, err := h.withWatchdog(WatchdogCallSetup, func() (goja.Value, error) {
		h.finishActivate()
		return nil, nil
	})
	return errors.Join(Diagnose(PhaseSetup, err), h.takeErrors())
}

// runEntry runs the legacy entry and its setup.
func (h *Host) runEntry(p *goja.Program) error {
	h.setup = true
	if _, err := h.VM.RunProgram(p); err != nil {
		h.setup = false
		h.state = StateDisposed
		return err
	}
	bundle := h.VM.Get(compile.BundleGlobal)
	if obj, ok := bundle.(*goja.Object); ok {
		def := obj.Get("default")
		if fn, ok := goja.AssertFunction(def); ok {
			// A default function is accepted as shorthand for setup.
			def = h.VM.ToValue(fn)
		} else if defObj, ok := def.(*goja.Object); ok {
			if err := h.loadActionDefinition(defObj); err != nil {
				h.setup = false
				h.state = StateDisposed
				return err
			}
			// defineScript returns a ScriptDef object. The host invokes its
			// setup member after the bundle's top-level registrations run.
			if setup, ok := goja.AssertFunction(defObj.Get("setup")); ok && h.mode != "describe" {
				// Keep lifecycle callbacks for the slot loop. setup itself is
				// invoked here on the worker before the candidate is swapped.
				v, err := setup(goja.Undefined(), h.contextValue)
				if err != nil {
					h.setup = false
					h.state = StateDisposed
					return err
				}
				if isThenable(v) {
					h.setup = false
					h.state = StateDisposed
					return errors.New("setup must be synchronous; start async work in onActivate")
				}
			}
			if f, ok := goja.AssertFunction(defObj.Get("onActivate")); ok {
				h.onActivate = f
			}
			if f, ok := goja.AssertFunction(defObj.Get("teardown")); ok {
				h.teardown = f
			}
			def = nil
		}
		if setup, ok := goja.AssertFunction(def); ok && h.mode != "describe" {
			v, err := setup(goja.Undefined(), h.contextValue)
			if err != nil {
				h.setup = false
				h.state = StateDisposed
				return err
			}
			if isThenable(v) {
				h.setup = false
				h.state = StateDisposed
				return errors.New("setup must be synchronous; start async work in onActivate")
			}
		}
	}
	h.setup = false
	return nil
}

// finishActivate makes a loaded runtime active, after its entry and macros.
func (h *Host) finishActivate() {
	h.active = true
	h.state = StateRunning
	if !h.candidate {
		h.session.activate()
		h.resetContinuous()
		h.startEnvironment()
	}
	h.Loop.DrainMicrotasks()
}

// EnableWatchdog enables the standard CPU and wall budgets around future
// setup, handler, activation and teardown calls. It is normally enabled by
// Lifecycle before loading a candidate.
func (h *Host) EnableWatchdog() { h.watchdogEnabled = true }

func (h *Host) withWatchdog(kind WatchdogCall, fn func() (goja.Value, error)) (v goja.Value, callErr error) {
	if !h.watchdogEnabled {
		return fn()
	}
	h.watchdogCalls++
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	opts := HandlerWatchdog
	switch kind {
	case WatchdogCallSetup:
		opts = SetupWatchdog
	case WatchdogCallTeardown:
		opts = TeardownWatchdog
	}
	w, err := NewWatchdog(h.VM, opts)
	if err != nil {
		return nil, err
	}
	if err := w.Begin(kind); err != nil {
		_ = w.Stop()
		return nil, err
	}
	defer func() {
		firing := w.End()
		stopErr := w.Stop()
		if callErr == nil && firing != nil {
			callErr = firing.Reason
		}
		if callErr == nil {
			callErr = stopErr
		}
	}()
	return fn()
}

// OnActivate enables output and invokes the optional lifecycle callback. A
// returned Promise is allowed to remain pending; its continuation is resumed
// by the host event loop when its timers complete.
func (h *Host) OnActivate() error {
	if h.mode == "describe" {
		return nil
	}
	previous := h.promisePhase
	h.promisePhase = PhaseActivate
	defer func() { h.promisePhase = previous }()
	_, err := h.withWatchdog(WatchdogCallHandler, func() (goja.Value, error) {
		for _, c := range control.All() {
			h.ignoreRelease[c] = h.padState.Down[c]
			h.gestureIgnored[c] = h.padState.Down[c]
		}
		h.active = true
		h.state = StateRunning
		h.startEnvironment()
		h.resetContinuous()
		h.applyPendingLayers()
		if h.onActivate == nil {
			return nil, nil
		}
		v, err := h.onActivate(goja.Undefined(), h.contextValue)
		h.Loop.DrainMicrotasks()
		return v, err
	})
	return errors.Join(Diagnose(PhaseActivate, err), h.takeErrors())
}

// Dispose runs teardown and releases every key owned by this runtime, even
// when teardown returns an error.
func (h *Host) Dispose() (err error) {
	if h.disposed {
		return nil
	}
	h.disposed = true
	h.stopEnvironment()
	h.cancelEffects()
	// The runs of macros end before teardown, which cannot reach them.
	h.stopRuns()
	// Teardown may call script key.releaseAll(), which settles live tap
	// Promises. Abandon the previous runtime's continuations first.
	h.cancelTaps(h.base, false)
	// Teardown may inspect state and release keys, but must not create a new
	// down or tap after the slot has begun its exit path.
	h.active = false
	h.session.detach()
	h.cancelContinuous()
	h.Loop.Clear()
	previous := h.promisePhase
	h.promisePhase = PhaseTeardown
	defer func() { h.promisePhase = previous }()
	defer func() {
		h.releaseAll(false)
		h.active = false
		h.state = StateDisposed
		err = errors.Join(err, h.takeErrors())
		h.Loop.Clear()
	}()
	_, err = h.withWatchdog(WatchdogCallTeardown, func() (goja.Value, error) {
		if h.teardown == nil {
			return nil, nil
		}
		return h.teardown(goja.Undefined())
	})
	return Diagnose(PhaseTeardown, err)
}

// State reports the runtime lifecycle state.
func (h *Host) State() State { return h.state }

func isThenable(v goja.Value) bool {
	o, ok := v.(*goja.Object)
	if !ok || o == nil {
		return false
	}
	_, ok = goja.AssertFunction(o.Get("then"))
	return ok
}

// Dispatch delivers an event to matching registrations and discards the
// consumed result for callers that do not have a declaration layer.
func (h *Host) Dispatch(e Event) error {
	_, err := h.DispatchEvent(e)
	return err
}

// DispatchResult reports whether a script handled an event.
type DispatchResult struct {
	Consumed bool
}

// DispatchEvent runs observers and the first matching consumer. Global
// registrations are tried before base registrations; e.pass() continues to
// the next level so a declaration can handle the event.
func (h *Host) DispatchEvent(e Event) (DispatchResult, error) {
	if c, ok := control.Parse(e.Control); ok && e.Gesture == "release" && h.ignoreRelease[c] {
		h.ignoreRelease[c] = false
		return DispatchResult{}, nil
	}
	if e.Scope == "" {
		e.Scope = "base"
	}
	result := DispatchResult{}
	var observerErrs []error
	// Observers never participate in consumption, so run every matching
	// observer before selecting the first consumer. This remains true when a
	// consumer appears earlier in registration order.
	for _, scope := range []RegistrationScope{ScopeGlobal, ScopeBase} {
		if scope == ScopeBase && e.Scope != "base" && e.Scope != "common" {
			continue
		}
		for _, r := range h.registrations {
			if r.disposed || r.Scope != scope || r.Control != e.Control {
				continue
			}
			if r.Observe {
				callEvent := e
				callEvent.Scope = "global"
				if err := h.callHandler(r.Handler, callEvent); err != nil {
					observerErrs = append(observerErrs, err)
				}
			}
		}
	}
	if len(observerErrs) > 0 {
		return h.finishDispatch(result, errors.Join(observerErrs...))
	}
	for _, scope := range []RegistrationScope{ScopeGlobal, ScopeBase} {
		if scope == ScopeBase && e.Scope != "base" && e.Scope != "common" {
			continue
		}
		for _, r := range h.registrations {
			if r.disposed || r.Scope != scope || r.Observe || r.Control != e.Control {
				continue
			}
			if r.Gesture != e.Gesture {
				continue
			}
			callEvent := e
			if scope == ScopeGlobal {
				callEvent.Scope = "global"
			} else {
				callEvent.Scope = "base"
			}
			if err := h.callHandler(r.Handler, callEvent); err != nil {
				return h.finishDispatch(result, err)
			}
			if h.passRequested {
				h.passRequested = false
				break
			}
			result.Consumed = true
			return h.finishDispatch(result, nil)
		}
	}
	return h.finishDispatch(result, nil)
}

func (h *Host) finishDispatch(result DispatchResult, callErr error) (DispatchResult, error) {
	return result, errors.Join(callErr, h.drainMicrotasks(), h.takeErrors())
}

// drainMicrotasks runs the runtime's queued Go microtasks under a watchdog,
// and enters none when the queue is empty.
func (h *Host) drainMicrotasks() error {
	if len(h.Loop.micro) == 0 {
		return nil
	}
	_, err := h.withWatchdog(WatchdogCallHandler, func() (goja.Value, error) {
		h.Loop.DrainMicrotasks()
		return nil, nil
	})
	return Diagnose(PhaseHandler, err)
}

// takeErrors returns the errors of the runtime's scope, the Promise
// rejections of the call that just ended, and the macros' errors already
// attributed, and forgets them.
func (h *Host) takeErrors() error {
	errs := h.base.takeErrors()
	errs = append(errs, h.takeRejections()...)
	errs = append(errs, h.macroErrs...)
	h.macroErrs = nil
	return errors.Join(errs...)
}

func (h *Host) takeRejections() []error {
	var errs []error
	for p, err := range h.rejections {
		errs = append(errs, err)
		delete(h.rejections, p)
	}
	return errs
}

func (h *Host) callHandler(fn goja.Callable, e Event) error {
	h.passRequested = false
	_, err := h.withWatchdog(WatchdogCallHandler, func() (goja.Value, error) {
		return fn(goja.Undefined(), h.event(e))
	})
	return Diagnose(PhaseHandler, err)
}

// RunDue advances host timers at the clock's current time: the tap
// completions T-output has returned, each scope's in a call of its own,
// the runtime's timers, then the timers and the time limit of each run of a
// macro. It makes a JavaScript call, under a watchdog, only for work that
// is there: an idle runtime costs no watchdog.
func (h *Host) RunDue() error {
	if h.disposed {
		return nil
	}
	h.pollEnvironment()
	stop, completionErr := h.runCompletions()
	if stop {
		return completionErr
	}
	_, err := h.Loop.RunDueWith(h.clock.Now(), func(fn func()) error {
		_, callErr := h.withWatchdog(WatchdogCallHandler, func() (goja.Value, error) {
			fn()
			return nil, h.takeErrors()
		})
		return callErr
	})
	if err != nil {
		return errors.Join(completionErr, Diagnose(PhaseHandler, err), h.takeErrors())
	}
	stop, runErr := h.runMacrosDue()
	if stop {
		return errors.Join(completionErr, runErr, h.takeErrors())
	}
	stop, lateErr := h.runCompletions()
	if stop {
		return errors.Join(completionErr, runErr, lateErr)
	}
	return errors.Join(completionErr, runErr, lateErr, h.drainMicrotasks(), h.takeErrors())
}

func (h *Host) event(e Event) *goja.Object {
	o := h.VM.CreateObject(h.eventProto)
	set(o, "control", h.name(e.Control))
	set(o, "gesture", h.name(e.Gesture))
	set(o, "time", float64(e.Time)/float64(time.Millisecond))
	set(o, "duration", float64(e.Duration)/float64(time.Millisecond))
	set(o, "count", e.Count)
	set(o, "scope", h.name(e.Scope))
	set(o, "cycle", e.Cycle)
	h.installGestureEvent(o, e)
	return o
}

func (h *Host) on(call goja.FunctionCall) goja.Value {
	return h.onScoped(ScopeBase, call)
}

func (h *Host) onScoped(scope RegistrationScope, call goja.FunctionCall) goja.Value {
	return h.onGesture(scope, "", call)
}

func (h *Host) onGesture(scope RegistrationScope, layer string, call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 3 {
		panic(h.VM.NewTypeError("on: expected control, gesture and handler"))
	}
	controlName, gestureName := call.Argument(0).String(), call.Argument(1).String()
	c, ok := control.Parse(controlName)
	if !ok {
		panic(h.VM.NewTypeError(fmt.Sprintf("on: unknown control %q", controlName)))
	}
	controlName = c.String()
	if !validGesture(gestureName) {
		panic(h.VM.NewTypeError(fmt.Sprintf("on: unknown gesture %q", gestureName)))
	}
	fn, ok := goja.AssertFunction(call.Argument(2))
	if !ok {
		panic(h.VM.NewTypeError("on: handler is not a function"))
	}
	idx := len(h.registrations)
	r := Registration{Control: controlName, Gesture: gestureName, Handler: fn, Scope: scope, Layer: layer}
	h.readGestureOptions(&r, call.Argument(3))
	h.registrations = append(h.registrations, r)
	return h.VM.ToValue(h.disposable(func() { h.registrations[idx].disposed = true }))
}

func validGesture(s string) bool {
	switch s {
	case "press", "release", "tap", "doubleTap", "tripleTap", "quadTap", "hold":
		return true
	default:
		return false
	}
}

func (h *Host) global(goja.FunctionCall) goja.Value {
	o := h.VM.NewObject()
	setFunction(h.VM, o, "on", 3, func(call goja.FunctionCall) goja.Value { return h.onScoped(ScopeGlobal, call) })
	setFunction(h.VM, o, "claim", 1, func(call goja.FunctionCall) goja.Value { return h.claimGesture(ScopeGlobal, "", call) })
	return o
}

func (h *Host) observe(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 2 {
		panic(h.VM.NewTypeError("observe: expected control and handler"))
	}
	c, ok := control.Parse(call.Argument(0).String())
	if !ok {
		panic(h.VM.NewTypeError(fmt.Sprintf("observe: unknown control %q", call.Argument(0).String())))
	}
	fn, ok := goja.AssertFunction(call.Argument(1))
	if !ok {
		panic(h.VM.NewTypeError("observe: handler is not a function"))
	}
	idx := len(h.registrations)
	h.registrations = append(h.registrations, Registration{Control: c.String(), Gesture: "*", Handler: fn, Observe: true, Scope: ScopeGlobal})
	return h.VM.ToValue(h.disposable(func() { h.registrations[idx].disposed = true }))
}

func (h *Host) disposable(fn func()) *goja.Object {
	o := h.VM.NewObject()
	set(o, "dispose", func(goja.FunctionCall) goja.Value { fn(); return goja.Undefined() })
	return o
}

// key is the legacy script's key API: its output goes to the runtime's
// scope, and its isHeld and releaseAll include the gestures' holds.
func (h *Host) key() *goja.Object {
	base := func() *outputScope { return h.base }
	return h.keyAPI(base, h.isChordHeld, func(*outputScope) { h.releaseAll(true) })
}

// keyAPI returns a key object whose output goes to the scope that scope
// returns; scope may panic with a TypeError (a macro that is not running).
func (h *Host) keyAPI(scope func() *outputScope, isHeld func(keys.Chord) bool, releaseAll func(*outputScope)) *goja.Object {
	o := h.VM.NewObject()
	setFunction(h.VM, o, "tap", 1, func(call goja.FunctionCall) goja.Value { return h.startTapCall(scope(), call) })
	setFunction(h.VM, o, "sequence", 1, func(call goja.FunctionCall) goja.Value { return h.startSequenceCall(scope(), call) })
	setFunction(h.VM, o, "type", 1, func(call goja.FunctionCall) goja.Value { return h.startTypeCall(scope(), call) })
	setFunction(h.VM, o, "down", 1, func(c goja.FunctionCall) goja.Value {
		h.keyDown(scope(), c.Argument(0).String())
		return goja.Undefined()
	})
	setFunction(h.VM, o, "up", 1, func(c goja.FunctionCall) goja.Value {
		h.keyUp(scope(), c.Argument(0).String())
		return goja.Undefined()
	})
	setFunction(h.VM, o, "isHeld", 1, func(c goja.FunctionCall) goja.Value {
		chord, ok := h.parseChord(scope(), c.Argument(0).String())
		return h.VM.ToValue(ok && isHeld(chord))
	})
	setFunction(h.VM, o, "releaseAll", 0, func(goja.FunctionCall) goja.Value { releaseAll(scope()); return goja.Undefined() })
	setFunction(h.VM, o, "parse", 1, func(c goja.FunctionCall) goja.Value {
		s := strings.TrimSpace(c.Argument(0).String())
		r := h.VM.NewObject()
		chord, err := keys.Parse(s)
		if err != nil {
			set(r, "ok", false)
			set(r, "error", err.Error())
		} else {
			set(r, "ok", true)
			set(r, "canonical", chord.String())
		}
		return r
	})
	return o
}

// releaseAll releases the legacy script's output: its gestures' holds and
// layers, and its scope. The runs of macros are not the legacy script's:
// safety boundaries stop them first (stopRuns). Script-initiated releaseAll
// settles tap Promises under the caller's watchdog; safety boundaries
// abandon them so no old JS can execute while the slot is releasing keys.
func (h *Host) releaseAll(settlePromises bool) {
	h.releaseGestureStates(false)
	h.releaseLayerHolds()
	h.releaseScope(h.base, settlePromises)
}

func (h *Host) sleep(call goja.FunctionCall) goja.Value {
	return h.sleepIn(h.base, call.Argument(0))
}

// sleepIn returns a Promise resolved by a timer of the scope's loop: the
// end of a run of a macro clears its loop, and the Promise never settles.
func (h *Host) sleepIn(s *outputScope, msValue goja.Value) goja.Value {
	ms := maxInt64(msValue.ToInteger(), 0)
	p, resolve, _ := h.VM.NewPromise()
	s.loop.After(time.Duration(ms)*time.Millisecond, func() {
		if err := resolve(goja.Undefined()); err != nil {
			s.addError(Diagnose(PhaseHandler, err))
		}
	})
	return h.VM.ToValue(p)
}

func (h *Host) every(call goja.FunctionCall) goja.Value {
	ms := maxInt64(call.Argument(0).ToInteger(), 1)
	fn, ok := goja.AssertFunction(call.Argument(1))
	if !ok {
		panic(h.VM.NewTypeError("every: callback is not a function"))
	}
	timer := new(Handle)
	*timer = h.Loop.Every(time.Duration(ms)*time.Millisecond, func() {
		if _, err := fn(goja.Undefined()); err != nil {
			h.base.addError(Diagnose(PhaseHandler, err))
		}
	})
	return h.VM.ToValue(h.disposable(func() { timer.Cancel() }))
}

func (h *Host) now(goja.FunctionCall) goja.Value {
	return h.VM.ToValue(float64(h.clock.Now()) / float64(time.Millisecond))
}

func (h *Host) pad() *goja.Object {
	o := h.VM.NewObject()
	setFunction(h.VM, o, "isDown", 1, func(call goja.FunctionCall) goja.Value {
		c, ok := control.Parse(call.Argument(0).String())
		return h.VM.ToValue(ok && h.padState.Connected && h.padState.Down[c])
	})
	setFunction(h.VM, o, "heldFor", 1, func(call goja.FunctionCall) goja.Value {
		c, ok := control.Parse(call.Argument(0).String())
		if !ok || !h.padState.Connected || !h.padState.Down[c] {
			return h.VM.ToValue(float64(0))
		}
		elapsed := h.clock.Now() - h.padState.Since[c]
		if elapsed < 0 {
			elapsed = 0
		}
		return h.VM.ToValue(float64(elapsed) / float64(time.Millisecond))
	})
	setFunction(h.VM, o, "axis", 1, func(call goja.FunctionCall) goja.Value {
		a, ok := parseScriptAxis(call.Argument(0).String())
		if !ok || !h.padState.Connected {
			return h.VM.ToValue(float64(0))
		}
		v := float64(h.padState.Axes[a])
		if a == control.LeftTrigger || a == control.RightTrigger {
			return h.VM.ToValue(v / 32767)
		}
		if v < 0 {
			v /= 32768
		} else {
			v /= 32767
		}
		if a == control.LeftY || a == control.RightY {
			v = -v
		}
		return h.VM.ToValue(v)
	})
	setFunction(h.VM, o, "has", 1, func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		if a, ok := parseScriptAxis(name); ok {
			return h.VM.ToValue(h.padState.Connected && h.padState.HasAxis[a])
		}
		if c, ok := control.Parse(name); ok {
			return h.VM.ToValue(h.padState.Connected && h.padState.HasControl[c])
		}
		return h.VM.ToValue(false)
	})
	setFunction(h.VM, o, "label", 1, h.padLabel)
	setFunction(h.VM, o, "rumble", 3, h.padRumble)
	setFunction(h.VM, o, "rumbleTriggers", 3, h.padRumbleTriggers)
	setFunction(h.VM, o, "haptic", 1, h.padHaptic)
	setFunction(h.VM, o, "setLed", 1, h.padSetLed)
	getter := h.VM.ToValue(func(goja.FunctionCall) goja.Value { return h.padInfo() })
	if err := o.DefineAccessorProperty("info", getter, nil, goja.FLAG_FALSE, goja.FLAG_TRUE); err != nil {
		panic(fmt.Sprintf("script: define pad.info: %v", err))
	}
	h.installAnalogAPI(o)
	return o
}

// UpdatePad replaces the slot-owned snapshot. It must run on the Host owner
// goroutine, or before a worker candidate is made visible to the owner.
func (h *Host) UpdatePad(state PadState) {
	h.padState = state
	for c := range h.gestureIgnored {
		if !state.Down[c] {
			h.gestureIgnored[c] = false
		}
	}
	// A script can install setters on reachable JS objects. Do not access
	// goja here: slot calls UpdatePad outside the handler watchdog.
}

// padInfo runs only on a script read, within the caller's watchdog budget.
func (h *Host) padInfo() *goja.Object {
	state := h.padState
	info := h.VM.CreateObject(nil)
	if state.Connected {
		key := state.Key
		if key == "" {
			key = input.KeyOf(&state.Info).String()
		}
		set(info, "key", key)
		set(info, "name", state.Info.Name)
		set(info, "type", scriptDeviceType(state.Info.Type))
		if state.HasPower {
			battery := h.VM.NewObject()
			set(battery, "state", state.Power.State.String())
			if state.Power.Percent >= 0 {
				set(battery, "percent", state.Power.Percent)
			}
			set(info, "battery", battery)
		}
	} else {
		set(info, "key", "")
		set(info, "name", "")
		set(info, "type", "other")
	}
	return info
}

func scriptDeviceType(kind string) string {
	switch kind {
	case "xbox360", "xboxone":
		return "xbox"
	case "ps4", "ps5":
		return kind
	case "switchpro":
		return "switchPro"
	case "joyconleft":
		return "joyconLeft"
	case "joyconright":
		return "joyconRight"
	case "joyconpair":
		return "joyconPair"
	default:
		return "other"
	}
}

func (h *Host) log() *goja.Object { return h.logAPI("") }

// logAPI returns a logger whose entries name macro, a macro's file, or
// none for the legacy script.
func (h *Host) logAPI(macro string) *goja.Object {
	o := h.VM.NewObject()
	for _, level := range []string{"debug", "info", "warn", "error"} {
		level := level
		set(o, level, func(c goja.FunctionCall) goja.Value {
			h.logs.push(h.logEntry(level, macro, formatLogArgs(c.Arguments)))
			return goja.Undefined()
		})
	}
	return o
}

// console exposes the standard log method alongside the host's structured
// logger methods. It intentionally shares the same in-memory sink so both
// declaration surfaces produce identical diagnostics. A line names the
// macro whose call writes it (macroCall).
func (h *Host) console() *goja.Object {
	o := h.VM.NewObject()
	for _, method := range []string{"log", "debug", "info", "warn", "error"} {
		level := method
		if level == "log" {
			level = "info"
		}
		set(o, method, func(c goja.FunctionCall) goja.Value {
			h.logs.push(h.logEntry(level, h.macroCall, formatLogArgs(c.Arguments)))
			return goja.Undefined()
		})
	}
	return o
}

// DrainLogs returns formatted messages without reading the JavaScript VM.
func (h *Host) DrainLogs() ([]LogEntry, uint64) { return h.logs.Drain() }

func (h *Host) logEntry(level, macro, message string) LogEntry {
	return LogEntry{Level: level, Message: message, Time: h.clock.Now(), Slot: h.base.owner.Slot, Generation: h.base.owner.Gen,
		Device: h.padState.Info.ID, DeviceKey: h.padState.Key, Profile: h.profile, Macro: macro}
}

// Registrations returns a copy of registered handlers.
func (h *Host) Registrations() []Registration { return append([]Registration(nil), h.registrations...) }

// Output returns a copy of output transitions since activation.
func (h *Host) Output() []OutputEvent { return append([]OutputEvent(nil), h.outputs...) }

// ReleaseAll releases keys owned by this host and deactivates it. Every run
// of a macro ends.
func (h *Host) ReleaseAll() {
	h.active = false
	h.stopEnvironment()
	h.cancelEffects()
	h.cancelContinuous()
	h.session.detach()
	h.stopRuns()
	h.releaseAll(false)
}

// Suspend closes output before teardown and discards all pending callbacks.
// A resumed slot uses a new Host: an old Promise continuation must never
// become runnable again after the safety boundary. Every run of a macro
// ends.
func (h *Host) Suspend() {
	h.active = false
	h.stopEnvironment()
	h.cancelEffects()
	h.session.detach()
	h.cancelContinuous()
	h.Loop.Clear()
	h.stopRuns()
	h.releaseAll(false)
}

// Discard tears down a worker candidate without ever calling its Arbiter.
// It has never been active in a slot, so there is no owner to release.
func (h *Host) Discard() error {
	h.output = nil
	h.candidateOutput = nil
	return h.Dispose()
}

// ReleaseKeys releases keys and abandons pending tap Promises while keeping
// other callbacks active. This is for direct Host users; slot safety
// boundaries use Suspend and a fresh worker-built runtime so old timers
// cannot emit afterward. Every run of a macro ends.
func (h *Host) ReleaseKeys() {
	h.stopRuns()
	h.releaseAll(false)
}

func (h *Host) name(s string) goja.Value {
	if v, ok := h.names[s]; ok {
		return v
	}
	v := h.VM.ToValue(s)
	h.names[s] = v
	return v
}

func maxInt64(v, lo int64) int64 {
	if v < lo {
		return lo
	}
	if v > math.MaxInt64/int64(time.Millisecond) {
		return math.MaxInt64 / int64(time.Millisecond)
	}
	return v
}

func set(o *goja.Object, name string, value any) {
	if err := o.Set(name, value); err != nil {
		panic(fmt.Sprintf("script: set %s: %v", name, err))
	}
}

// Native Go callbacks have length 0 in goja. Preserve the required argument
// count promised by the filtered TypeScript declaration for introspection.
func setFunction(vm *goja.Runtime, o *goja.Object, name string, arity int, value any) {
	set(o, name, value)
	fn, ok := o.Get(name).(*goja.Object)
	if !ok {
		panic("script: callback is not a function: " + name)
	}
	if err := fn.DefineDataProperty("length", vm.ToValue(arity), goja.FLAG_FALSE, goja.FLAG_TRUE, goja.FLAG_FALSE); err != nil {
		panic(fmt.Sprintf("script: set %s.length: %v", name, err))
	}
}
