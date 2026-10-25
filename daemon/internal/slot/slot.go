package slot

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// ErrUnknownInput is wrapped by the errors of events the engine does not
// know: a button or axis name control does not parse, a trigger sent as a
// button, or an unknown event kind.
var ErrUnknownInput = errors.New("slot: unknown input")

const (
	// settleLimit bounds how long Replay keeps running deadlines after
	// the last event.
	settleLimit = time.Minute
	// maxErrors bounds the errors a Slot keeps until Errors takes them.
	maxErrors = 100
)

// Arbiter is where a Slot sends its key commands: an *output.Arbiter
// owned by the slot's goroutine, or an *output.RemoteArbiter.
type Arbiter interface {
	Down(output.Owner, keys.Chord) error
	DownFor(output.Owner, keys.Chord, time.Duration) error
	Up(output.Owner, keys.Chord) error
	Tap(output.Owner, keys.Chord, time.Duration) error
	Repeat(output.Owner, keys.Chord) error
	TapSequence(output.Owner, []keys.Chord, time.Duration, time.Duration) error
	// Sequence runs the steps of a sequence or text action (MAP-22, OUT-21): the
	// Arbiter queues it behind the sequences before it and cancels it with the
	// owner.
	Sequence(o output.Owner, steps []keys.Step, hold, gap time.Duration) error
	RepeatTap(output.Owner, keys.Chord, time.Duration) error
	StopRepeat(output.Owner, keys.Chord) error
	Move(o output.Owner, dx, dy int32) error
	Jump(o output.Owner, x, y int32) error
	ReleaseOwners(match func(output.Owner) bool) error
	Advance() error
	NextDeadline() (clock.Time, bool)
}

// ScriptEvent is the input shape exposed to a per-slot TypeScript runtime.
// It deliberately mirrors the script host event without making this package
// depend on goja or the script implementation.
type ScriptEvent struct {
	Control, Gesture string
	Time             clock.Time
	Duration         time.Duration
	Count            int
	Scope            string
	Cycle            int64
	Held             bool
}

// ScriptRuntime is the slot-owned seam for the optional M5 runtime. The
// implementation may build and reload asynchronously, but all methods are
// called by the slot loop goroutine.
type ScriptRuntime interface {
	// Dispatch returns whether the script consumed the event. A consumed
	// press skips the declaration; a consumed release still ends any prior
	// declaration cycle, without running its release actions.
	Dispatch(ScriptEvent) (consumed bool, err error)
	RunDue() error
	NextDeadline() (clock.Time, bool)
	ReleaseAll() error
	Reload(uint32) error
	Dispose() error
}

// wakeSource is optional: a script file watcher can notify a sleeping slot
// without touching the slot or its script runtime from the watcher goroutine.
type wakeSource interface{ Wake() <-chan struct{} }

// scriptPauser is optional for runtimes without asynchronous work. It closes
// the script output gate and invalidates old callbacks at a safety boundary.
type scriptPauser interface{ SetPaused(bool) error }
type scriptResyncWaiter interface{ WaitForInitialResync() bool }

// scriptConfigurator says whether the profile has a legacy script entry,
// which takes over input: Dispatch and the gesture hooks see every button
// edge, script-only virtual controls exist, and a cold runtime queues the
// input until it is ready (ADR-0044). A runtime without it is configured.
type scriptConfigurator interface{ Configured() bool }

// scriptNeeder says whether the profile needs the runtime at all: it has a
// legacy entry, or one of its actions runs a macro (SCR-50). A runtime only
// needed for macros does not take over input; the slot keeps its Resync
// gate and release bookkeeping. A runtime without it is needed exactly when
// it is configured.
type scriptNeeder interface{ Needed() bool }

// inputMarker is optional: an Arbiter that measures the latency of the keys an
// input edge causes (DMN-15, ADR-0071). The slot marks the edge it is handling
// with MarkInput(stamp, deviceKey), and clears the mark (a zero stamp) when it
// has handled it. *output.RemoteArbiter is one, and *output.Arbiter is not: a
// replay on a local Arbiter measures nothing.
type inputMarker interface {
	MarkInput(stamp latency.Stamp, key string)
}

var (
	_ Arbiter = (*output.Arbiter)(nil)
	_ Arbiter = (*output.RemoteArbiter)(nil)
)

var _ inputMarker = (*output.RemoteArbiter)(nil)

// Config configures a Slot.
type Config struct {
	Index         uint8           // the slot number, output.Owner.Slot
	Gen           uint32          // the profile generation, output.Owner.Gen; at least 1
	Program       *engine.Program // the compiled profile
	Arbiter       Arbiter         // where the key commands go
	Script        ScriptRuntime   // optional per-slot script runtime
	ProfileKey    string
	LayerMemory   LayerMemory
	ProfileSwitch func(gen uint32, id string) error
	// Clock is the Arbiter's clock, for a RemoteArbiter its Loop's. The
	// engine's input times are its Now.
	Clock clock.Clock
	// Trace, when set, receives every engine.Decision (ENG-20), before the
	// key commands it causes. It must not call back into the Slot.
	Trace          func(engine.Decision)
	OnLayerChange  func(engine.LayerState)
	Feedback       func(engine.Feedback)
	FeedbackDevice func(input.DeviceID, engine.Feedback)
	// OnHaptic, when set, receives the one-shot haptics of the engine (MAP-25,
	// HUD-10) with the controller the slot has, 0 when it has none yet. It is
	// called on the slot's goroutine and must not block or call back into the
	// Slot.
	OnHaptic func(input.DeviceID, engine.Haptic)
	// Switches, when set, returns the global feedback switches (UI-27,
	// ADR-0067) the engine reads at each feedback state and haptic
	// (engine.Options.Switches). It is called on the slot's goroutine, must not
	// block, and returns what the last committed config.json says; nil means
	// every switch on.
	Switches func() profile.FeedbackSwitches
	// OnEvent, when set, receives every event Handle gets, and each
	// ComboHeld of Replay, at the clock's Now, before the decisions and
	// key commands the event causes. It must not call back into the Slot.
	OnEvent func(input.Event)
	// Paused starts the slot paused, as SetPaused(true) would, without
	// sending anything (DMN-06).
	Paused bool
	// Calibration, when set, returns the stick calibration (IN-14) of the
	// controller with the canonical DeviceKey key, nil when it has none. The
	// slot calls it on its own goroutine when it gets a controller and at
	// RefreshCalibration; it must not block, and returns what the last
	// committed config.json says.
	Calibration func(key string) *profile.Calibration
	// Power, when set, returns the battery state that the gamepad with this
	// connection id last reported (IN-12), false when it has reported none. The
	// slot calls it on its own goroutine each time it copies the gamepad's state
	// for a script (pad.info.battery), so it must not block: padmated's is a
	// read of an immutable table.
	Power func(input.DeviceID) (input.Power, bool)
}

// Slot is one device slot: an Engine whose commands go to the Arbiter. It
// is not safe for concurrent use: one goroutine owns it, and the Arbiter
// unless that is a RemoteArbiter.
type Slot struct {
	index              uint8
	gen                uint32
	clk                clock.Clock
	arb                Arbiter
	marker             inputMarker // arb, if it can mark
	eng                *engine.Engine
	state              State
	info               *input.DeviceInfo
	pad                PadSnapshot
	analog             profile.Analog // what the engine recognizes with: the profile's with the calibration
	calibration        func(key string) *profile.Calibration
	power              func(input.DeviceID) (input.Power, bool)
	scopes             [control.Count]string
	scriptSequence     int64
	scriptCycles       [control.Count]int64
	scriptStarted      [control.Count]clock.Time
	onEvent            func(input.Event)
	device             input.DeviceID
	deviceKey          string
	profileKey         string
	nextProfileKey     *string
	layerMemory        LayerMemory
	restoringLayers    bool
	errs               []error
	dropped            int             // errors past maxErrors since the last Errors
	reported           map[string]bool // unknown inputs already reported
	script             ScriptRuntime
	scriptEnabled      bool // the runtime is Configured: a legacy script takes over input
	scriptNeeded       bool // the runtime is Needed: scriptEnabled, or the profile runs macros
	scriptDisconnected bool
	scriptReleased     bool
	scriptCanceled     [control.Count]bool
	// macroCanceled is set at each press of a control, when the slot is
	// paused or waits for its Resync: that press cycle runs no macro, its
	// release, tap or hold included (SCR-50). Unlike scriptCanceled it is
	// not cleared at the release, so it holds with or without a legacy
	// script.
	macroCanceled      [control.Count]bool
	scriptInputQueue   []scriptInput
	scriptInputBase    State
	scriptInputBasePad PadSnapshot
	scriptInputPad     PadSnapshot
	scriptInputPreview *engine.InputPreview
	scriptInputGen     uint32
}

// New returns a Slot with every control up, paused with Config.Paused.
func New(cfg Config) (*Slot, error) {
	switch {
	case cfg.Program == nil:
		return nil, errors.New("slot: no program")
	case isNil(cfg.Arbiter):
		return nil, errors.New("slot: no Arbiter")
	case cfg.Clock == nil:
		return nil, errors.New("slot: no clock")
	case cfg.Gen == 0:
		return nil, errors.New("slot: profile generation 0 is invalid")
	}
	s := &Slot{
		index: cfg.Index, gen: cfg.Gen, clk: cfg.Clock, arb: cfg.Arbiter, onEvent: cfg.OnEvent,
		reported: map[string]bool{}, script: cfg.Script,
		analog: cfg.Program.Analog(), calibration: cfg.Calibration, power: cfg.Power,
		profileKey: cfg.ProfileKey, layerMemory: cfg.LayerMemory,
	}
	s.marker, _ = cfg.Arbiter.(inputMarker)
	s.scriptEnabled = s.scriptConfigured()
	s.scriptNeeded = s.scriptNeededNow()
	if w, ok := s.script.(scriptResyncWaiter); ok && w.WaitForInitialResync() {
		s.scriptDisconnected = true
	}
	opts := engine.Options{Trace: cfg.Trace, Feedback: cfg.Feedback, Script: s.invokeAction, Switches: cfg.Switches}
	if cfg.ProfileSwitch != nil {
		opts.Profile = func(id string) { s.keep(cfg.ProfileSwitch(s.gen, id)) }
	}
	opts.LayerChange = cfg.OnLayerChange
	if _, ok := s.script.(gestureRuntime); ok {
		opts.LayerChange = func(state engine.LayerState) {
			if runtime, ok := s.script.(gestureRuntime); ok {
				runtime.BindLayers(s.eng)
			}
			if cfg.OnLayerChange != nil {
				cfg.OnLayerChange(state)
			}
		}
	}
	if s.layerMemory != nil {
		onChange := opts.LayerChange
		opts.LayerChange = func(state engine.LayerState) {
			s.saveLayers()
			if onChange != nil {
				onChange(state)
			}
		}
	}
	if s.scriptEnabled {
		opts.Button = s.dispatchButton
	}
	if cfg.FeedbackDevice != nil {
		opts.Feedback = func(f engine.Feedback) { cfg.FeedbackDevice(s.device, f) }
	}
	if cfg.OnHaptic != nil {
		opts.Haptic = func(h engine.Haptic) { cfg.OnHaptic(s.device, h) }
	}
	s.eng = engine.New(cfg.Program, (*engineOutput)(s), opts)
	s.connectGestures()
	if cfg.Paused {
		// Every control is up: the pause ends no cycle and decides nothing.
		s.eng.Pause(s.clk.Now())
		if p, ok := s.script.(scriptPauser); ok {
			s.keep(p.SetPaused(true))
		}
	}
	s.updatePad(s.clk.Now())
	return s, nil
}

// isNil reports whether a is nil or a nil pointer.
func isNil(a Arbiter) bool {
	switch a := a.(type) {
	case nil:
		return true
	case *output.Arbiter:
		return a == nil
	case *output.RemoteArbiter:
		return a == nil
	}
	return false
}

func (s *Slot) scriptConfigured() bool {
	if s.script == nil {
		return false
	}
	if configured, ok := s.script.(scriptConfigurator); ok {
		return configured.Configured()
	}
	return true
}

func (s *Slot) scriptNeededNow() bool {
	if s.scriptConfigured() {
		return true
	}
	n, ok := s.script.(scriptNeeder)
	return ok && n.Needed()
}

// Handle handles ev at the clock's Now. Button and axis events go to the
// engine. DeviceRemoved releases everything, like ReleaseAll, and the
// engine then counts every control as up; DeviceAdded does nothing.
// State follows by State.Apply. Config.OnEvent sees ev first. Handle does
// not look at ev.Time or ev.Device. Other kinds, ComboHeld among them,
// are unknown inputs: the pause is not one slot's to toggle, so Replay
// and padmated's router handle ComboHeld themselves.
func (s *Slot) Handle(ev input.Event) {
	now := s.clk.Now()
	if s.onEvent != nil {
		raw := ev
		raw.AssignedKey = ""
		s.onEvent(raw)
	}
	s.state.Apply(ev)
	if s.queueScriptInput(ev, now) {
		return
	}
	if s.marker == nil || ev.Stamp == 0 || ev.Kind != input.ButtonDown && ev.Kind != input.ButtonUp && ev.Kind != input.AxisMotion {
		s.handleInput(ev, now)
		return
	}
	// The keys this edge causes are measured from the Stamp that T-input read
	// when it received it (DMN-15). Only here: the edges that the slot queued for
	// a starting script are handled later, by handleInput alone, and what the
	// timers of Advance send is not an edge's.
	s.marker.MarkInput(ev.Stamp, s.deviceKey)
	s.handleInput(ev, now)
	s.marker.MarkInput(0, "")
}

func (s *Slot) handleInput(ev input.Event, now clock.Time) {
	switch ev.Kind {
	case input.DeviceAdded:
		s.device = ev.Device
		s.deviceKey = ev.AssignedKey
		if s.deviceKey == "" && ev.Info != nil {
			s.deviceKey = input.KeyOf(ev.Info).String()
		}
		if ev.Info != nil {
			copied := *ev.Info
			s.info = &copied
		}
		s.restoreLayers()
		s.applyCalibration(now)
		s.updatePad(now)
		waitResync, _ := s.script.(scriptResyncWaiter)
		s.scriptDisconnected = waitResync != nil && waitResync.WaitForInitialResync()
		if !s.Paused() && !s.scriptDisconnected {
			if p, ok := s.script.(scriptPauser); ok {
				s.keep(p.SetPaused(false))
			}
			s.scriptReleased = false
		}
		s.eng.RefreshFeedback()
	case input.DeviceRemoved:
		s.saveLayers()
		s.restoringLayers = true
		s.reset(now)
		s.restoringLayers = false
		s.device = 0
		s.deviceKey = ""
		s.info = nil
		s.applyCalibration(now)
		s.updatePad(now)
	case input.ButtonDown, input.ButtonUp:
		c, ok := parseButton(ev.Control)
		if !ok {
			s.unknown("button", ev.Control)
			return
		}
		s.eng.Step(engine.Input{T: now, Kind: engine.InputButton, Control: c, Down: ev.Kind == input.ButtonDown})
		s.updatePad(now)
	case input.AxisMotion:
		a, ok := control.ParseAxis(ev.Control)
		if !ok {
			s.unknown("axis", ev.Control)
			return
		}
		s.eng.Step(engine.Input{T: now, Kind: engine.InputAxis, Axis: a, Value: ev.Value})
		s.updatePad(now)
	default:
		if key := "kind " + ev.Kind.String(); !s.reported[key] {
			s.reported[key] = true
			s.keep(fmt.Errorf("%w: event %v; such events are dropped", ErrUnknownInput, ev.Kind))
		}
	}
}

// dispatchButton is called by the pure engine at its common physical and
// virtual button transition point. The callback stays on the slot goroutine.
func (s *Slot) dispatchButton(ev engine.ButtonEvent) bool {
	if ev.Down {
		s.scriptSequence++
		if s.scriptSequence <= 0 {
			s.scriptSequence = 1
		}
		s.scriptCycles[ev.Control] = s.scriptSequence
		s.scriptStarted[ev.Control] = ev.Time
		s.scopes[ev.Control] = ev.Scope
	}
	cycle := s.scriptCycles[ev.Control]
	var duration time.Duration
	if !ev.Down && cycle != 0 && ev.Time >= s.scriptStarted[ev.Control] {
		duration = time.Duration(ev.Time - s.scriptStarted[ev.Control])
	}
	s.updatePad(ev.Time)
	consumed := false
	if ev.Down {
		s.macroCanceled[ev.Control] = s.Paused() || s.scriptDisconnected
	}
	if ev.Down && (s.Paused() || s.scriptDisconnected) {
		s.scriptCanceled[ev.Control] = true
	}
	canceled := s.scriptCanceled[ev.Control]
	if !ev.Down {
		s.scriptCanceled[ev.Control] = false
	}
	if ev.Down && !canceled && !s.Paused() && !s.scriptDisconnected && s.scriptReleased {
		if p, ok := s.script.(scriptPauser); ok {
			s.keep(p.SetPaused(false))
		}
		s.scriptReleased = false
	}
	if s.script != nil && !s.Paused() && !s.scriptDisconnected && !canceled {
		gesture := "release"
		if ev.Down {
			gesture = "press"
		}
		var err error
		consumed, err = s.script.Dispatch(ScriptEvent{
			Control: ev.Control.String(), Gesture: gesture, Time: ev.Time,
			Duration: duration, Count: 1, Cycle: cycle, Held: ev.Down, Scope: s.scopes[ev.Control],
		})
		s.keep(err)
	}
	if !ev.Down {
		s.scopes[ev.Control] = ""
		s.scriptCycles[ev.Control] = 0
		s.scriptStarted[ev.Control] = 0
	}
	return consumed
}

// unknown reports the first event of an unknown button or axis name.
func (s *Slot) unknown(what, name string) {
	if key := what + " " + name; !s.reported[key] {
		s.reported[key] = true
		s.keep(fmt.Errorf("%w: %s %q; its events are dropped", ErrUnknownInput, what, name))
	}
}

// NextDeadline returns when Advance next has work: the earlier of the
// engine's and the Arbiter's deadlines, only the engine's with a
// RemoteArbiter, whose Loop runs its own. It returns false when there is
// none.
func (s *Slot) NextDeadline() (clock.Time, bool) { return s.nextDeadline(false) }

// nextDeadline is NextDeadline; with settle it is the deadline Replay waits for
// once its events are done, where the engine leaves out the pointer of a stick
// that is still pushed (engine.Engine.NextSettleDeadline).
func (s *Slot) nextDeadline(settle bool) (clock.Time, bool) {
	var d clock.Time
	var ok bool
	if settle {
		d, ok = s.eng.NextSettleDeadline()
	} else {
		d, ok = s.eng.NextDeadline()
	}
	if len(s.scriptInputQueue) != 0 {
		ok = false
	}
	if s.script != nil {
		if sd, sok := s.script.NextDeadline(); sok && (!ok || sd < d) {
			d, ok = sd, true
		}
	}
	if a, aok := s.arb.NextDeadline(); aok && (!ok || a < d) {
		d, ok = a, true
	}
	return d, ok
}

func (s *Slot) wake() <-chan struct{} {
	if w, ok := s.script.(wakeSource); ok {
		return w.Wake()
	}
	return nil
}

// Advance runs the work due at the clock's Now: the Arbiter's retries and
// tap ends, then the engine's timers.
func (s *Slot) Advance() {
	s.updatePad(s.clk.Now())
	s.keep(s.arb.Advance())
	if s.script != nil {
		s.keep(s.script.RunDue())
	}
	s.flushScriptInput()
	if len(s.scriptInputQueue) != 0 {
		return
	}
	s.eng.Apply(s.clk.Now())
	// A local Arbiter can complete a zero-duration output synchronously.
	// Deliver its notification before reporting the next replay deadline.
	if s.script != nil {
		s.keep(s.script.RunDue())
	}
	s.updatePad(s.clk.Now())
}

// ReleaseAll lets go of every key of the slot at once (OUT-07): the engine
// ends every press cycle, then the Arbiter releases the keys of every
// owner with the slot's index, of any generation, and ends their pending
// taps. The controls that are down produce nothing until they are pressed
// again. ReleaseAll returns the Arbiter's error; releases that failed are
// retried by Advance.
//
// A runtime that only runs macros is rebuilt at once, when the slot is not
// paused and not waiting for its Resync: no press restarts it, as one does
// a legacy script's (SCR-50).
func (s *Slot) ReleaseAll() error {
	err := s.releaseAll()
	s.restartMacros()
	return err
}

// ReleaseAllForExit is ReleaseAll for an exit path that goes on to
// Dispose: it does not start a runtime that only runs macros again, which
// Dispose would tear down at once.
func (s *Slot) ReleaseAllForExit() error { return s.releaseAll() }

// restartMacros resumes a runtime that only runs macros after ReleaseAll
// stopped it, unless the slot is paused or waits for its Resync, whose end
// resumes it.
func (s *Slot) restartMacros() {
	if !s.scriptNeeded || s.scriptEnabled || !s.scriptReleased || s.Paused() || s.scriptDisconnected {
		return
	}
	if p, ok := s.script.(scriptPauser); ok {
		s.keep(p.SetPaused(false))
	}
	s.scriptReleased = false
}

// releaseAll is ReleaseAll without restarting a macro runtime, for the
// paths that go on to stop it for longer: a removed device and the end of
// RunLoop.
func (s *Slot) releaseAll() error {
	s.discardScriptInput()
	s.cancelScriptHeld()
	var scriptErr error
	if s.script != nil {
		s.scriptReleased = true
		scriptErr = s.script.ReleaseAll()
	}
	s.eng.CancelAll(s.clk.Now())
	return errors.Join(scriptErr, s.releaseOwn())
}

// Dispose releases and tears down the optional script runtime. RunLoop calls
// this after releasing the declarative engine on every exit path.
func (s *Slot) Dispose() error {
	s.discardScriptInput()
	if s.script == nil {
		return nil
	}
	err := s.script.Dispose()
	s.script = nil
	return err
}

// releaseOwn has the Arbiter release the keys of every owner with the
// slot's index, of any generation, and end their pending taps.
func (s *Slot) releaseOwn() error {
	return s.arb.ReleaseOwners(func(o output.Owner) bool { return o.Slot == s.index })
}

// SetPaused pauses the slot, or resumes it when on is false (DMN-06).
// SetPaused(true) puts the engine in paused mode, which ends every press
// cycle as ReleaseAll does, then releases like ReleaseAll and returns the
// Arbiter's error; releases that failed are retried by Advance. Until
// SetPaused(false) every press sends nothing (engine.ReasonPaused).
// SetPaused(false) sends nothing and presses nothing again: the controls
// down across it stay canceled until they are released. Pausing a paused
// slot decides nothing, and its release finds only the releases still
// pending; resuming a slot not paused does nothing. ReleaseAll, Reset,
// Reload and a removed device leave the pause as it is.
func (s *Slot) SetPaused(on bool) error {
	if !on {
		s.eng.Resume()
		if !s.scriptDisconnected {
			if p, ok := s.script.(scriptPauser); ok {
				s.scriptReleased = false
				return p.SetPaused(false)
			}
		}
		return nil
	}
	s.discardScriptInput()
	s.cancelScriptHeld()
	s.eng.Pause(s.clk.Now())
	var scriptErr error
	if s.script != nil {
		if p, ok := s.script.(scriptPauser); ok {
			scriptErr = p.SetPaused(true)
		} else {
			scriptErr = s.script.ReleaseAll()
		}
	}
	return errors.Join(scriptErr, s.releaseOwn())
}

// Paused reports whether the slot is paused: since SetPaused(true), or
// Config.Paused, and until SetPaused(false).
func (s *Slot) Paused() bool { return s.eng.Paused() }

// Reload switches the slot to prog, a new profile generation gen (PRO-03):
// the engine's Reload, then the release of ReleaseAll, whose error goes to
// Errors; later commands carry gen. The controls down stay down, each
// doing nothing until it is pressed again, and State does not change.
// Reload fails, and changes nothing, when prog is nil or gen is not after
// the slot's generation.
func (s *Slot) Reload(prog *engine.Program, gen uint32) error {
	switch {
	case prog == nil:
		return errors.New("slot: Reload needs a program")
	case gen <= s.gen:
		return fmt.Errorf("slot: profile generation %d is not after %d", gen, s.gen)
	}
	s.saveLayers()
	s.discardScriptInput()
	s.restoringLayers = true
	defer func() { s.restoringLayers = false; s.saveLayers() }()
	if s.nextProfileKey != nil {
		s.profileKey = *s.nextProfileKey
		s.nextProfileKey = nil
		if r, ok := s.script.(interface{ SetProfileKey(string) error }); ok {
			s.keep(r.SetProfileKey(s.profileKey))
		}
	}
	s.cancelScriptHeld()
	wasNeeded := s.scriptNeeded
	s.scriptEnabled = s.scriptConfigured()
	s.scriptNeeded = s.scriptNeededNow()
	s.connectGestures()
	now := s.clk.Now()
	s.eng.Reload(now, prog)
	s.syncCalibration(now)
	s.updatePad(now)
	s.connectGestures()
	// Release every owner from the previous generation before activating the
	// replacement script. Script Reload may run onActivate synchronously and
	// emit keys under the new owner; a broad slot release after that point
	// would immediately undo those keys.
	s.keep(s.releaseOwn())
	s.restoreLayers()
	if s.script != nil {
		s.keep(s.script.Reload(gen))
		if s.scriptReleased && !s.Paused() && !s.scriptDisconnected {
			if p, ok := s.script.(scriptPauser); ok {
				s.keep(p.SetPaused(false))
			}
			s.scriptReleased = false
		}
	}
	s.gen = gen
	if !wasNeeded && s.scriptNeeded && s.info != nil {
		// This pad was assigned while the profile needed no runtime (no
		// script, no macro), so its initial assignment had no Resync
		// barrier. The current snapshot is already complete; open the
		// script gate on this reload.
		s.scriptDisconnected = false
		s.updatePad(now)
		if !s.Paused() {
			if p, ok := s.script.(scriptPauser); ok {
				s.keep(p.SetPaused(false))
			}
			s.scriptReleased = false
		}
	}
	return nil
}

// Gen returns the slot's profile generation, output.Owner.Gen.
func (s *Slot) Gen() uint32 { return s.gen }

// Errors returns the errors since the last call and forgets them: Arbiter
// errors of the engine's commands, of Advance and of Reload, and unknown
// inputs. When more than a hundred piled up, the last one says how many
// were dropped.
func (s *Slot) Errors() []error {
	errs := s.errs
	if s.dropped > 0 {
		errs = append(errs, fmt.Errorf("slot: %d more errors dropped", s.dropped))
	}
	s.errs, s.dropped = nil, 0
	return errs
}

// keep keeps err, when it is not nil, for Errors.
func (s *Slot) keep(err error) {
	switch {
	case err == nil:
	case len(s.errs) < maxErrors:
		s.errs = append(s.errs, err)
	default:
		s.dropped++
	}
}

// Owner returns the Arbiter owner of the engine's owner o: the slot's
// index and generation, o's source and cycle. A source the Arbiter does
// not know maps to output.SourceInvalid, which the Arbiter rejects.
func (s *Slot) Owner(o engine.OwnerID) output.Owner {
	src := output.SourceInvalid
	switch o.Source {
	case engine.SourceDecl:
		src = output.SourceDecl
	case engine.SourceScript:
		src = output.SourceScript
	}
	return output.Owner{Slot: s.index, Gen: s.gen, Source: src, Cycle: o.Cycle}
}

// Replay handles events in virtual time on clk, which must be the slot's
// clock. Event times are absolute: event i is handled at
// clock.Time(events[i].Time). Before each event Replay moves clk forward,
// stopping at every deadline of the slot on the way to run Advance at its
// exact time, a deadline before an event at the same time. After the last
// event it keeps running deadlines until none is left, and fails if that
// takes more than a minute of virtual time. The pointer of a stick that is
// still pushed in mode "mouse" is not such a deadline: like a key held at the
// end, it goes on until the caller's ReleaseAll, so a trace may end with the
// stick deflected. In M1a only tap ends and
// retries of failed Sends make deadlines, so that fails only while Send
// keeps failing; OUT-11's repeat while a control is held will need an end
// time for Replay. It does not ReleaseAll: the caller does. With no
// events, Replay only runs the deadlines left, such as those after a
// ReleaseAll. With a RemoteArbiter it does not step the Arbiter's
// deadlines: the Loop runs them on its own timer.
//
// A ComboHeld event, which input.ExpandCombos inserts, toggles the pause
// (DMN-07) as padmated does for all slots, instead of going to Handle:
// Config.OnEvent sees it, then SetPaused(!Paused()) runs, whose error goes
// to Errors. State does not change.
//
// Replay checks the times first and handles nothing when one is before
// clk's Now or the event before it, or does not fit a clock.Time.
func (s *Slot) Replay(clk *clock.Fake, events []input.Event) error {
	if clk == nil || clock.Clock(clk) != s.clk {
		return errors.New("slot: Replay needs the slot's clock")
	}
	prev := clk.Now()
	for i, ev := range events {
		if ev.Time > math.MaxInt64 {
			return fmt.Errorf("slot: event %d: time %dns is out of range", i, ev.Time)
		}
		t := clock.Time(ev.Time)
		if t < prev {
			if i == 0 {
				return fmt.Errorf("slot: event 0 at %v is before the clock, %v", t, prev)
			}
			return fmt.Errorf("slot: event %d at %v is before event %d, at %v", i, t, i-1, prev)
		}
		prev = t
	}
	for _, ev := range events {
		if err := s.advanceTo(clk, clock.Time(ev.Time)); err != nil {
			return err
		}
		if ev.Kind == input.ComboHeld {
			s.combo(ev)
			continue
		}
		s.Handle(ev)
	}
	limit := clk.Now().Add(settleLimit)
	for {
		d, ok := s.nextDeadline(true)
		if !ok {
			return nil
		}
		if d > limit {
			return fmt.Errorf("slot: still busy %v after the last event, next deadline %v", settleLimit, d)
		}
		if err := s.advanceTo(clk, max(d, clk.Now())); err != nil {
			return err
		}
	}
}

// combo handles the ComboHeld ev of a replay at the clock's Now: it
// toggles the pause.
func (s *Slot) combo(ev input.Event) {
	if s.onEvent != nil {
		s.onEvent(ev)
	}
	s.keep(s.SetPaused(!s.Paused()))
}

// advanceTo moves clk to t, which is not before its Now, running Advance
// at every deadline on the way. A deadline already past runs at once.
func (s *Slot) advanceTo(clk *clock.Fake, t clock.Time) error {
	for {
		d, ok := s.NextDeadline()
		if !ok || d > t {
			break
		}
		if d > clk.Now() {
			clk.Set(d)
		}
		s.Advance()
		if d, ok := s.NextDeadline(); ok && d <= clk.Now() {
			return fmt.Errorf("slot: deadline %v is still due after Advance at %v", d, clk.Now())
		}
	}
	clk.Set(t)
	return nil
}

// engineOutput is the engine.Output of a Slot: it forwards the commands to
// the Arbiter with the slot's owner and keeps their errors.
type engineOutput Slot

func (o *engineOutput) Down(id engine.OwnerID, c keys.Chord, minPress time.Duration) {
	s := (*Slot)(o)
	s.keep(s.arb.DownFor(s.Owner(id), c, minPress))
}

func (o *engineOutput) Up(id engine.OwnerID, c keys.Chord) {
	s := (*Slot)(o)
	s.keep(s.arb.Up(s.Owner(id), c))
}

func (o *engineOutput) Tap(id engine.OwnerID, c keys.Chord, hold time.Duration) {
	s := (*Slot)(o)
	s.keep(s.arb.Tap(s.Owner(id), c, hold))
}

func (o *engineOutput) Repeat(id engine.OwnerID, c keys.Chord) {
	s := (*Slot)(o)
	s.keep(s.arb.Repeat(s.Owner(id), c))
}

func (o *engineOutput) TapSequence(id engine.OwnerID, items []keys.Chord, hold, gap time.Duration) {
	s := (*Slot)(o)
	s.keep(s.arb.TapSequence(s.Owner(id), items, hold, gap))
}

func (o *engineOutput) Sequence(id engine.OwnerID, steps []keys.Step, hold, gap time.Duration) {
	s := (*Slot)(o)
	s.keep(s.arb.Sequence(s.Owner(id), steps, hold, gap))
}

// TypeText is a sequence of one text step, so that a second trigger of a text
// action types after the first and not among its characters.
func (o *engineOutput) TypeText(id engine.OwnerID, text string, gap time.Duration) {
	s := (*Slot)(o)
	s.keep(s.arb.Sequence(s.Owner(id), []keys.Step{keys.TextStep(text)}, 0, gap))
}

func (o *engineOutput) RepeatTap(id engine.OwnerID, c keys.Chord, hold time.Duration) {
	s := (*Slot)(o)
	s.keep(s.arb.RepeatTap(s.Owner(id), c, hold))
}

func (o *engineOutput) StopRepeat(id engine.OwnerID, c keys.Chord) {
	s := (*Slot)(o)
	s.keep(s.arb.StopRepeat(s.Owner(id), c))
}

func (o *engineOutput) Move(id engine.OwnerID, dx, dy int32) {
	s := (*Slot)(o)
	s.keep(s.arb.Move(s.Owner(id), dx, dy))
}

func (o *engineOutput) Jump(id engine.OwnerID, x, y int32) {
	s := (*Slot)(o)
	s.keep(s.arb.Jump(s.Owner(id), x, y))
}
