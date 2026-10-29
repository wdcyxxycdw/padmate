package engine

import (
	"math"
	"strconv"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Timing and analog defaults (ARCHITECTURE §7.6). A profile's timing and
// analog settings override them.
const (
	// DefaultTapPress is how long a tap-mode action holds its keys
	// (tapPressMs): the hold the engine passes to Output.Tap.
	DefaultTapPress    = 20 * time.Millisecond
	DefaultHold        = 250 * time.Millisecond
	DefaultMultiTap    = 220 * time.Millisecond
	DefaultTapGap      = 20 * time.Millisecond
	DefaultRepeatDelay = 350 * time.Millisecond
	DefaultRepeatRate  = profile.DefaultRepeatRateHz
	// TriggerPress is the fraction of its travel at or above which a
	// trigger presses LT or RT.
	TriggerPress = 0.30
	// TriggerRelease is the fraction at or below which a pressed trigger
	// releases.
	TriggerRelease            = 0.20
	DefaultDirectionThreshold = 0.30
	DefaultDeadzone           = 0.15
	DefaultSaturation         = 0.95
	// DefaultOuterThreshold is the normalized stick depth at which the outer
	// ring starts (ANA-10). The outer ring ends RingHysteresis below it.
	DefaultOuterThreshold = 0.90
	// RingHysteresis is how far below its threshold a stick must fall before
	// it leaves the outer ring.
	RingHysteresis = 0.05
	// DefaultKnobStepDeg is the rotation of one virtual knob tick (ANA-11).
	DefaultKnobStepDeg = 30
	// TriggerFullPress is the fraction of its travel at or above which a
	// trigger also presses the second stage, LTFull or RTFull (ANA-14).
	TriggerFullPress = 0.90
	// TriggerFullRelease is the fraction at or below which the second stage
	// releases.
	TriggerFullRelease = 0.80
	// KnobSettle is how long the knob waits, after an axis input of its
	// stick, before it reads the angle (ANA-11). The two axes of one report
	// reach the slot microseconds apart, and a fast release from a diagonal
	// would otherwise show an intermediate reading (one axis already at rest,
	// the other not yet) whose angle is 45 degrees off, and queue a stray
	// tick. 4 ms also covers a slot goroutine that is briefly descheduled.
	// A knob tick is rate-limited to 25 Hz anyway, so 4 ms is not felt.
	KnobSettle = 4 * time.Millisecond
)

// Gesture is a way of using a control that a binding gives an action. It
// is profile's type: see the package doc.
type Gesture = profile.Gesture

// The gestures, with profile's names.
const (
	GestureNone      = profile.GestureNone
	GesturePress     = profile.GesturePress
	GestureRelease   = profile.GestureRelease
	GestureTap       = profile.GestureTap
	GestureDoubleTap = profile.GestureDoubleTap
	GestureTripleTap = profile.GestureTripleTap
	GestureQuadTap   = profile.GestureQuadTap
	GestureHold      = profile.GestureHold
)

// Source is what issued a command. The zero value is invalid. The slot
// maps it to output.Source.
type Source uint8

const (
	// SourceDecl is a declarative binding.
	SourceDecl Source = iota + 1
	// SourceScript is a script (M5).
	SourceScript
)

// String returns "decl", "script", or "source(N)" for any other value.
func (s Source) String() string {
	switch s {
	case SourceDecl:
		return "decl"
	case SourceScript:
		return "script"
	}
	return "source(" + strconv.Itoa(int(s)) + ")"
}

// OwnerID is the engine's part of an output.Owner: what issued a command
// and its press cycle. The slot adds its index and profile generation.
type OwnerID struct {
	Source Source
	Cycle  uint64
}

// String formats o as source/cycle, such as "decl/17".
func (o OwnerID) String() string {
	return o.Source.String() + "/" + strconv.FormatUint(o.Cycle, 10)
}

// InputKind is the kind of an Input. The zero value is invalid.
type InputKind uint8

const (
	// InputButton is a button edge.
	InputButton InputKind = iota + 1
	// InputAxis is a new axis value.
	InputAxis
)

// String returns "button", "axis", or "inputkind(N)" for any other value.
func (k InputKind) String() string {
	switch k {
	case InputButton:
		return "button"
	case InputAxis:
		return "axis"
	}
	return "inputkind(" + strconv.Itoa(int(k)) + ")"
}

// Input is one input from the slot. The engine never sees input.Event.
type Input struct {
	T       clock.Time
	Kind    InputKind
	Control control.Control // InputButton: a physical button, never LT or RT
	Down    bool            // InputButton
	Axis    control.Axis    // InputAxis
	Value   int16           // InputAxis: raw, -32768..32767 for sticks, 0..32767 for triggers
	// SuppressActions ends an already started button cycle without its tap or
	// release action when a higher priority script consumes the release.
	SuppressActions bool
	// fn is the function key that was active when the edge arrived (MAP-27),
	// control.Invalid when none was. stepButton sets it on the edges it
	// buffers, so that a press replayed later looks its binding up in the set
	// of the key that was held when it came, not of the one held when it is
	// replayed.
	fn control.Control
}

// Output receives the engine's key commands. The slot forwards them to the
// Output Arbiter, whose errors are the slot's business. The engine calls
// it on its own goroutine, from Step, Apply, CancelAll, Pause, Reload and
// Reset; it must not call back into the Engine.
type Output interface {
	Down(o OwnerID, c keys.Chord, minPress time.Duration)
	Up(o OwnerID, c keys.Chord)
	Tap(o OwnerID, c keys.Chord, hold time.Duration)
	Repeat(o OwnerID, c keys.Chord)
	TapSequence(o OwnerID, items []keys.Chord, hold, gap time.Duration)
	// Sequence runs the steps of a sequence action (MAP-22, ADR-0070): taps a
	// shortcut, waits, types a text, one after another, hold being the press time
	// of a tap and gap the least time between two steps and between the characters
	// of a text. steps is the action's own slice: the output copies what it keeps.
	// Like TapSequence it holds nothing, and a second one waits for the first.
	Sequence(o OwnerID, steps []keys.Step, hold, gap time.Duration)
	// TypeText types the text of a text action (OUT-21), a character at a time,
	// gap apart; it is a sequence of one text step.
	TypeText(o OwnerID, text string, gap time.Duration)
	RepeatTap(o OwnerID, c keys.Chord, hold time.Duration)
	StopRepeat(o OwnerID, c keys.Chord)
	// Move moves the pointer by dx pixels right and dy down, relative to where
	// it is, for a stick in mode "mouse" (ADR-0062, section 5). It holds
	// nothing, so there is nothing to release.
	Move(o OwnerID, dx, dy int32)
	// Jump puts the pointer at x, y, each 0 to 65535 across the virtual
	// desktop, for a mouse jump action.
	Jump(o OwnerID, x, y int32)
}

// Options configures an Engine.
type Options struct {
	// Trace receives every Decision (ENG-20), on the engine's goroutine,
	// before the output the decision causes. Like the Output, it must not
	// call back into the Engine. When it is nil the engine builds no
	// decisions at all.
	Trace       func(Decision)
	LayerChange func(LayerState)
	Feedback    func(Feedback)
	// Haptic receives the one-shot haptics of the profile (MAP-25, HUD-10;
	// haptic.go): the preset of a binding each time a gesture of it is
	// dispatched, and a tick for each knob step with knobTick. It is called on
	// the engine's goroutine, like Feedback, and must not block or call back
	// into the Engine. It is never called while the global rumble switch is
	// off (Switches) or the profile's feedback has a strength of 0, while the
	// engine is paused, or by an input preview.
	Haptic func(Haptic)
	// Switches returns the global feedback switches (UI-27, ADR-0067: rumble,
	// led and pause shape what the engine publishes and plays). The engine asks
	// each time it publishes a feedback state or plays a haptic, so a change of
	// config.json applies at once; RefreshFeedback publishes the state again
	// after one. It is called on the engine's goroutine and must not block, so
	// it reads an immutable value. Nil means every switch on, which is what a
	// profile with the default settings gets. The switches that a profile's
	// feedback once held are not read (profile.Feedback).
	Switches func() profile.FeedbackSwitches
	// Registry, when set, replaces the program's layers in the lookup of a
	// binding. The engine does not check the bindings it returns: they must
	// pass CheckBinding (see Level). The set of a held function key (MAP-27)
	// is not one of its levels: it is above them, from the Program.
	Registry Registry
	// Button runs synchronously for each recognized physical or virtual
	// button transition, before the declaration for that transition. The
	// callback returns true when a higher level consumes it.
	Button func(ButtonEvent) bool
	// ButtonObserver runs for the same transitions as Button, after it,
	// without the Scope, and cannot consume them. Unlike Button it does not make the virtual
	// controls that no binding uses recognized (a stick direction, a d-pad
	// diagonal): the slot keeps its macro bookkeeping with it without
	// changing which controls exist (SCR-50).
	ButtonObserver func(ButtonEvent)
	// Gestures supplies script registrations to the existing recognizer.
	Gestures GestureResolver
	// Script invokes a named script action without importing its runtime.
	Script func(ScriptActionEvent)
	// Profile requests an asynchronous library switch; it must not reenter
	// the engine or synchronously wait for the supervisor.
	Profile func(string)
}

// ButtonEvent is the pure input shape of a button transition handed to a
// resolver outside the engine. Scope is the current declaration level at a
// press; callers retain it for the matching release.
type ButtonEvent struct {
	Time    clock.Time
	Control control.Control
	Down    bool
	Scope   string
}

type LayerState struct {
	Base     string
	Overlays []string
	Color    string
}

// Feedback is the indication a slot's engine asks of its controller: the
// state of the layer and of the pause, with the pulses to give when it
// changed. Color is "" when there is none to show (the layer has none, or
// the global led switch is off), which releases the color the controller has.
type Feedback struct {
	Layer    LayerState
	Color    string
	Pulse    bool
	Pulses   int
	PulseMs  int
	GapMs    int
	Strength uint16
	// Paused says that the mapping is paused, whether or not the pause is
	// shown (the global pause switch): the state is the same.
	Paused bool
	// Mode is the mode number of the layer, the number of its pulses (1 for
	// base and for the first layer of a cycle). Pausing does not change it,
	// and it is not limited to 1 to 4: whoever shows it on four player lights
	// takes that into account (HUD-08).
	Mode int
	// LED is the global led switch (ADR-0067): padmate manages the LED and the
	// player lights of the controller. When it is false the lights are released.
	LED bool
}

// Engine is the mapping state of one slot. It is not safe for concurrent
// use: it belongs to the slot's goroutine (ADR-0005).
type Engine struct {
	prog             *Program
	out              Output
	trace            func(Decision)
	cycle            uint64
	paused           bool
	inputOnly        bool
	ctl              [control.Count]ctlState
	physical         [control.Count]bool
	physicalReleased [control.Count]clock.Time
	multi            [control.Count]multiState
	latch            [control.Count]latchState
	buffer           [256]Input
	buffered         int
	actions          [control.Count][3]actionState
	baseLayer        string
	layerEpoch       uint64
	overlays         []overlay
	layerChange      func(LayerState)
	feedback         func(Feedback)
	haptic           func(Haptic)
	switches         func() profile.FeedbackSwitches
	registry         Registry
	button           func(ButtonEvent) bool
	buttonObserver   func(ButtonEvent)
	gestures         GestureResolver
	script           func(ScriptActionEvent)
	profileSwitch    func(string)
	axes             [control.AxisCount]int16
	sticks           [2]stickState
	dpadPhysical     [4]bool
	dpadOrder        [4]control.Control
	dpadOrderN       int
	dpadVirtual      [8]bool
	halfAxis         [control.AxisCount * 2]bool
	triggerFull      [2]bool    // the second stage of LT and RT, as recognized
	modulated        int        // rate-modulated repeats running (actionState.mod)
	precision        float64    // the smallest factor of the precision actions held; 0 when none (updatePrecision)
	hints            hintsState // the key hints (HUD-03): on while a hints action is held, with the list to show (hints.go)
	// fnActive is the function key (prog.fnKey) while it is held, whose set
	// the next press looks up first (function_keys.go), else control.Invalid.
	// It follows the physical state of the key, so CancelAll, Pause and a
	// Reload with the same function key keep it, and Reset and a Reload that
	// changes the key clear it.
	fnActive control.Control
	// The stick calibration of the controller (IN-14): calSrc as set, cal what
	// the sticks recognize with, calIgnored the sticks whose calibration does
	// not hold with the program. They belong to the controller, so Reload and
	// Reset keep them.
	calSrc     *profile.Calibration
	cal        [2]stickCal
	calIgnored [2]bool
}

type stickState struct {
	current    [8]bool
	active     bool
	inner      bool // recognized ring state (ANA-10)
	outer      bool
	pending    [8]bool
	pendingSet bool
	deadline   clock.Time
	// The virtual knob (ANA-11): knobOn when the angle has a reference,
	// knobRef is that angle, ticks the queued ticks (clockwise positive) and
	// knobNext the earliest time of the next one. An axis input of a knob
	// stick sets knobEvalSet and knobEvalAt, the time the angle is read at
	// (KnobSettle later); later inputs do not move it.
	knobOn      bool
	knobRef     float64
	ticks       int
	knobNext    clock.Time
	knobEvalSet bool
	knobEvalAt  clock.Time
	// The pointer in mode "mouse" (stick_mouse.go): mouseOn while it ticks, at
	// mouseAt. mouseTx and mouseTy are the velocity the stick asks for, mouseVx
	// and mouseVy the smoothed one the pointer has, both in pixels per second;
	// mouseAx and mouseAy the distance in pixels not sent yet, and mouseLast the
	// time they are settled up to. mouseLock: the stick was pushed across a
	// safety boundary (CancelAll) and has not been back inside the deadzone since.
	mouseOn          bool
	mouseLock        bool
	mouseAt          clock.Time
	mouseLast        clock.Time
	mouseTx, mouseTy float64
	mouseVx, mouseVy float64
	mouseAx, mouseAy float64
}

type ctlState struct {
	plan       *gesturePlan
	started    clock.Time
	eventScope string
	latching   bool
	down       bool
	cycle      uint64
	canceled   bool
	gesture    Gesture
	level      string
	binding    *profile.Binding
	// hapticCycle and hapticAt are the cycle and the instant this control
	// last played its binding's haptic (hapticSet once it has), so that two
	// gestures dispatched at one instant play one.
	hapticCycle uint64
	hapticAt    clock.Time
	hapticSet   bool
	held        *profile.Action
	holds       [2]*profile.Action
	holdsN      int
	pending     bool
	deadline    clock.Time
	tapCount    int
	holding     bool
	tapped      bool // decideTap ran the tap action; the release only ends the cycle
	// answered is whether a decision of this cycle other than unbound has been
	// made (decide, decideUnresolved): an action that ran, a script that took
	// the gesture, a block, a cancel. An unbound press that only waits for
	// a tap, a multi-tap, a hold or a release is not answered until one of them
	// is, or until a multi-tap window ends with no action for its taps
	// (decideNoTapAction), which only an unanswered cycle needs. The copy of the
	// cycle that a multi-tap window keeps carries it.
	answered    bool
	layerHolds  [2]string
	layerHoldsN int
	// precision is the speed factor of the mouse precision action this cycle
	// holds, 0 when it holds none (stick_mouse.go).
	precision float64
	// hints is whether this cycle holds a HUD hints action (hints.go).
	hints bool
}

type multiState struct {
	count    int
	deadline clock.Time
	state    ctlState
}

type latchState struct {
	layerHolds  [2]string
	layerHoldsN int
	holds       [2]*profile.Action
	holdsN      int
	owner       OwnerID
	precision   float64 // a latched precision action's factor, 0 when none
	hints       bool    // a latched HUD hints action
}

type actionState struct {
	action  *profile.Action
	next    clock.Time
	repeat  bool
	owner   OwnerID
	binding *profile.Binding
	style   profile.RepeatStyle
	// A rate-modulated repeat (ANA-12) is mod. last is the time of its latest
	// repeat, or of its start; once armed (the initial delay is over: the
	// first repeat went, or there was no delay), next follows last and the
	// depth of its control.
	last  clock.Time
	mod   bool
	armed bool
}

// New returns an Engine for prog that sends its commands to out, with every
// control up. It panics if prog or out is nil.
func New(prog *Program, out Output, opts Options) *Engine {
	if prog == nil || out == nil {
		panic("engine: New needs a Program and an Output")
	}
	return &Engine{prog: prog, out: out, trace: opts.Trace, baseLayer: prog.defaultLayer, overlays: make([]overlay, 0, len(prog.layers)), layerChange: opts.LayerChange, feedback: opts.Feedback, haptic: opts.Haptic, switches: opts.Switches, registry: opts.Registry, button: opts.Button, buttonObserver: opts.ButtonObserver, gestures: opts.Gestures, script: opts.Script, profileSwitch: opts.Profile}
}

// ControlDown reports the engine's current recognized button state. It is
// read only on the owning slot goroutine for script pad snapshots.
func (e *Engine) ControlDown(c control.Control) bool {
	return c.IsValid() && e.physical[c]
}

// SetButton updates the optional script resolver at a profile reload. A
// virtual control already down remains tracked until its release edge.
func (e *Engine) SetButton(button func(ButtonEvent) bool) { e.button = button }

// SetButtonObserver updates Options.ButtonObserver at a profile reload.
func (e *Engine) SetButtonObserver(observe func(ButtonEvent)) { e.buttonObserver = observe }

// Step handles one input at in.T. Times need not increase strictly. Axis
// values feed trigger hysteresis, sticks, d-pad virtual controls and
// half-axis buttons. Invalid inputs are ignored.
func (e *Engine) Step(in Input) {
	switch in.Kind {
	case InputButton:
		if !in.Control.IsValid() || in.Control.IsTrigger() {
			e.spurious(in.T, in.Control, edgeOf(in.Down), 0)
			return
		}
		if isDpad(in.Control) && e.prog.analog.Dpad.Mode != "buttons" {
			e.stepDpad(in.T, in.Control, in.Down)
			return
		}
		if isDpad(in.Control) {
			e.trackDpadButton(in.Control, in.Down)
		}
		e.stepButton(in.T, in.Control, in.Down, in.SuppressActions)
	case InputAxis:
		if !in.Axis.IsValid() {
			e.spurious(in.T, control.Invalid, EdgeNone, 0)
			return
		}
		e.axes[in.Axis] = in.Value
		e.stepHalfAxis(in.T, in.Axis, in.Value)
		if c := trigger(in.Axis); c != control.Invalid {
			e.stepTrigger(in.T, c, in.Value)
		}
		if in.Axis == control.LeftX || in.Axis == control.LeftY {
			e.updateStick(in.T, 0)
		}
		if in.Axis == control.RightX || in.Axis == control.RightY {
			e.updateStick(in.T, 1)
		}
		e.retimeRepeats(in.Axis)
	default:
		e.spurious(in.T, in.Control, EdgeNone, 0)
		return
	}
}

// stepTrigger moves the virtual buttons of trigger c, LT or RT, to the raw
// value v: the first stage with its press and release thresholds, and the
// second stage (LTFull, RTFull; ANA-14) with its own hysteresis (fullPress,
// fullRelease). LT stays down while the second stage is, so the second stage
// presses after LT and releases before it, in one Step too. A second stage
// that nothing binds is tracked but not pressed, like the half axes.
func (e *Engine) stepTrigger(t clock.Time, c control.Control, v int16) {
	i, fc := 0, control.LTFull
	if c == control.RT {
		i, fc = 1, control.RTFull
	}
	down := e.physical[c]
	if !down && v >= e.prog.triggerPressRaw[i] {
		down = true
	}
	if down && v <= e.prog.triggerReleaseRaw[i] {
		down = false
	}
	was := e.triggerFull[i]
	full := was
	if !full && v >= e.prog.triggerFullPressRaw[i] {
		full = true
	}
	if full && v <= e.prog.triggerFullReleaseRaw[i] {
		full = false
	}
	e.triggerFull[i] = full
	pressFull := full && !was && e.virtualBound(fc)
	releaseFull := !full && e.physical[fc]
	if full && (pressFull || e.physical[fc]) {
		down = true
	}
	if releaseFull {
		e.stepButton(t, fc, false, false)
	}
	if down != e.physical[c] {
		e.stepButton(t, c, down, false)
	}
	if pressFull {
		e.stepButton(t, fc, true, false)
	}
}

func (e *Engine) stepButton(t clock.Time, c control.Control, down, suppressActions bool) {
	if !c.IsValid() {
		e.spurious(t, c, edgeOf(down), 0)
		return
	}
	if e.physical[c] == down {
		e.spurious(t, c, edgeOf(down), e.ctl[c].cycle)
		return
	}
	in := Input{T: t, Kind: InputButton, Control: c, Down: down, SuppressActions: suppressActions}
	e.physical[in.Control] = in.Down
	if !down {
		e.physicalReleased[c] = t
	}
	if c == e.prog.fnKey {
		// The function key (MAP-27, ADR-0069, ADR-0075) never enters gesture
		// recognition: no cycle, no buffering, no script, and it does not
		// interrupt a pending gesture.
		e.stepFunctionKey(t, down)
		return
	}
	in.fn = e.fnActive
	if e.inputOnly {
		if e.button != nil {
			e.button(ButtonEvent{Time: t, Control: c, Down: down})
		}
		return
	}
	consumed := false
	if e.button != nil {
		consumed = e.button(ButtonEvent{Time: t, Control: c, Down: down, Scope: e.InputScope(c)})
		in.SuppressActions = in.SuppressActions || consumed
	}
	if e.buttonObserver != nil {
		e.buttonObserver(ButtonEvent{Time: t, Control: c, Down: down})
	}
	if in.Down && in.SuppressActions {
		// A press the script's button handler consumed starts no cycle: it
		// has no cycle id and no binding, and the script has it (ADR-0071).
		if consumed && e.trace != nil {
			e.trace(Decision{T: t, Control: c, Edge: EdgeDown, Gesture: GesturePress, Reason: ReasonScript})
		}
		return
	}
	if !in.Down {
		// Repeats stop at the physical release (OUT-11), even when the
		// release waits in the buffer: only its Up keeps the input order.
		e.stopRepeats(c)
		// The release of a dispatched control (down, not pending) goes at
		// once when the buffer is empty: no pending gesture captured its
		// press, so it does not wait for one. Otherwise it joins the buffer
		// and replays in order, as every edge does (ARCHITECTURE §7.7,
		// MAP-09): the earlier edges run with the keys and layers this
		// control holds, as they came, and an edge of this control still in
		// the buffer never runs after its release. The pending gestures
		// ahead of it resolve by their deadlines, so it waits at most their
		// holdMs.
		if e.buffered == 0 && e.ctl[c].down && !e.ctl[c].pending {
			e.release(in.T, in)
			return
		}
	}
	if e.buffered == len(e.buffer) {
		// Bounded overload: resolve the blocker, then drain before accepting
		// another edge. No physical release is dropped, and no heap is used.
		if c := e.blocker(); c != control.Invalid {
			e.commitHold(in.T, c)
		}
		e.drain(in.T)
	}
	e.buffer[e.buffered] = in
	e.buffered++
	e.drain(in.T)
}

func isDpad(c control.Control) bool {
	return c == control.DpadUp || c == control.DpadDown || c == control.DpadLeft || c == control.DpadRight
}

func dpadIndex(c control.Control) int {
	switch c {
	case control.DpadUp:
		return 0
	case control.DpadDown:
		return 1
	case control.DpadLeft:
		return 2
	case control.DpadRight:
		return 3
	}
	return -1
}

func stickControls(which int) [8]control.Control {
	if which == 0 {
		return [8]control.Control{control.LSUp, control.LSDown, control.LSLeft, control.LSRight, control.LSUpLeft, control.LSUpRight, control.LSDownLeft, control.LSDownRight}
	}
	return [8]control.Control{control.RSUp, control.RSDown, control.RSLeft, control.RSRight, control.RSUpLeft, control.RSUpRight, control.RSDownLeft, control.RSDownRight}
}

func dpadControls() [8]control.Control {
	return [8]control.Control{control.DpadUp, control.DpadDown, control.DpadLeft, control.DpadRight, control.DpadUpLeft, control.DpadUpRight, control.DpadDownLeft, control.DpadDownRight}
}

func (e *Engine) stepDpad(t clock.Time, c control.Control, down bool) {
	i := dpadIndex(c)
	if i < 0 {
		return
	}
	if e.dpadPhysical[i] == down {
		e.spurious(t, c, edgeOf(down), e.ctl[c].cycle)
		return
	}
	e.dpadPhysical[i] = down
	if down {
		if e.dpadOrderN < len(e.dpadOrder) {
			e.dpadOrder[e.dpadOrderN] = c
			e.dpadOrderN++
		}
	} else {
		for n := 0; n < e.dpadOrderN; n++ {
			if e.dpadOrder[n] == c {
				copy(e.dpadOrder[n:], e.dpadOrder[n+1:e.dpadOrderN])
				e.dpadOrderN--
				break
			}
		}
	}
	var want [8]bool
	switch e.prog.analog.Dpad.Mode {
	case "eightWay":
		want = e.dpadWantEight()
	case "fourWay":
		want = e.dpadWantFour()
	default:
		return
	}
	e.applyVirtual(t, dpadControls(), &e.dpadVirtual, &want)
}

func (e *Engine) dpadWantEight() (want [8]bool) {
	for i := 0; i < 4; i++ {
		if e.dpadPhysical[i] {
			want[i] = true
		}
	}
	for _, pair := range [][3]int{{0, 3, 5}, {1, 3, 7}, {1, 2, 6}, {0, 2, 4}} {
		if e.dpadPhysical[pair[0]] && e.dpadPhysical[pair[1]] {
			want[pair[0]] = false
			want[pair[1]] = false
			want[pair[2]] = true
		}
	}
	return want
}

func (e *Engine) dpadWantFour() (want [8]bool) {
	for i := 0; i < 4; i++ {
		if e.dpadPhysical[i] {
			want[i] = true
		}
	}
	for _, pair := range [][3]int{{0, 3, 3}, {1, 3, 3}, {1, 2, 2}, {0, 2, 0}} {
		if e.dpadPhysical[pair[0]] && e.dpadPhysical[pair[1]] {
			chosen := pair[2]
			if e.prog.analog.Dpad.Fold == "keepFirst" {
				chosen = pair[0]
				if e.dpadOrderN > 0 && e.dpadOrder[0] == dpadControls()[pair[1]] {
					chosen = pair[1]
				}
			}
			for i := 0; i < 4; i++ {
				want[i] = false
			}
			want[chosen] = true
		}
	}
	return want
}

func (e *Engine) halfAxisState(a control.Axis, raw int16) (bool, bool) {
	threshold := e.axisThreshold()
	v := float64(raw) / 32767
	if a == control.LeftTrigger || a == control.RightTrigger {
		if v < 0 {
			v = 0
		}
	}
	return v >= threshold, -v >= threshold
}

func (e *Engine) stepHalfAxis(t clock.Time, a control.Axis, raw int16) {
	pos, neg := e.halfAxisState(a, raw)
	idx := int(a) * 2
	if e.halfAxis[idx] != pos {
		e.halfAxis[idx] = pos
		if c := halfAxisControl(a, true); pos && e.virtualBound(c) || !pos && e.physical[c] {
			e.stepButton(t, c, pos, false)
		}
	}
	if e.halfAxis[idx+1] != neg {
		e.halfAxis[idx+1] = neg
		if c := halfAxisControl(a, false); neg && e.virtualBound(c) || !neg && e.physical[c] {
			e.stepButton(t, c, neg, false)
		}
	}
}

func halfAxisControl(a control.Axis, positive bool) control.Control {
	base := []control.Control{control.LeftXPositive, control.LeftYPositive, control.RightXPositive, control.RightYPositive, control.LeftTriggerPositive, control.RightTriggerPositive}
	if int(a) < 1 || int(a) > len(base) {
		return control.Invalid
	}
	c := base[int(a)-1]
	if !positive {
		c++
	}
	return c
}

func (e *Engine) updateStick(t clock.Time, which int) {
	rd := e.readStick(which)
	want := e.directionsFor(which, &rd)
	s := &e.sticks[which]
	activeCtl := control.LSActive
	if which == 1 {
		activeCtl = control.RSActive
	}
	if rd.active != s.active {
		s.active = rd.active
		if rd.active && e.virtualBound(activeCtl) || !rd.active && e.physical[activeCtl] {
			e.stepButton(t, activeCtl, rd.active, false)
		}
	}
	// Edge order within one update (ARCHITECTURE §7.5): active, then the
	// rings, then the directions; the knob ticks follow the rings.
	e.updateRings(t, which, &rd)
	e.updateKnob(t, which, &rd)
	e.updateMouse(t, which, &rd)
	if sameDirections(s.current, want) {
		s.pendingSet = false
		s.deadline = 0
		return
	}
	if d := e.prog.analog.LS.DirectionDelayMs; which == 1 {
		d = e.prog.analog.RS.DirectionDelayMs
		if d > 0 {
			s.pending = want
			s.pendingSet = true
			s.deadline = t.Add(time.Duration(d) * time.Millisecond)
			return
		}
	} else if d > 0 {
		s.pending = want
		s.pendingSet = true
		s.deadline = t.Add(time.Duration(d) * time.Millisecond)
		return
	}
	e.commitStick(t, which, want)
}

func sameDirections(a, b [8]bool) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (e *Engine) commitStick(t clock.Time, which int, want [8]bool) {
	s := &e.sticks[which]
	e.applyVirtual(t, stickControls(which), &s.current, &want)
	s.pendingSet = false
	s.deadline = 0
}

func (e *Engine) applyVirtual(t clock.Time, controls [8]control.Control, current, want *[8]bool) {
	for i := 0; i < 8; i++ {
		if current[i] && !want[i] {
			if e.physical[controls[i]] {
				e.stepButton(t, controls[i], false, false)
			}
		}
	}
	for i := 0; i < 8; i++ {
		if !current[i] && want[i] {
			if e.virtualBound(controls[i]) {
				e.stepButton(t, controls[i], true, false)
			}
		}
	}
	*current = *want
}

func (e *Engine) virtualBound(c control.Control) bool {
	_, b, _, _ := e.lookup(c, GesturePress)
	return b != nil || e.button != nil || e.physical[c]
}

// stickReading is what the axes of one stick say: its settings with the
// defaults applied, how deep it is pushed (norm, 0 inside the deadzone, 1 at
// the saturation radius) and, while it is active, the angle in degrees
// (0 is up, clockwise, in [0, 360)). A stick in mode "mouse" has its angle
// whenever it is outside the deadzone, since the pointer does not wait for
// directionThreshold.
type stickReading struct {
	cfg    profile.Stick
	norm   float64
	angle  float64
	active bool
}

// readStick reads stick which (0 left, 1 right) from the axes. active is
// norm >= directionThreshold.
func (e *Engine) readStick(which int) (rd stickReading) {
	a := e.prog.analog.LS
	if which == 1 {
		a = e.prog.analog.RS
	}
	if a.Mode == "" {
		a.Mode = "standard"
	}
	if !e.prog.analogResolved && a.Deadzone < 0 {
		a.Deadzone = DefaultDeadzone
	}
	if !e.prog.analogResolved && a.Saturation <= 0 {
		a.Saturation = DefaultSaturation
	}
	if !e.prog.analogResolved && a.DirectionThreshold <= 0 {
		a.DirectionThreshold = DefaultDirectionThreshold
	}
	if !e.prog.analogResolved && a.OuterThreshold <= 0 {
		a.OuterThreshold = DefaultOuterThreshold
	}
	if a.KnobStepDeg <= 0 {
		a.KnobStepDeg = DefaultKnobStepDeg
	}
	if c := &e.cal[which]; c.on {
		a.Deadzone, a.Saturation = c.deadzone, c.saturation
	}
	rd.cfg = a
	x := float64(e.axes[axisForStick(which, true)]) / 32767
	y := -float64(e.axes[axisForStick(which, false)]) / 32767
	mag := math.Hypot(x, y)
	if mag <= a.Deadzone {
		return rd
	}
	if mag > a.Saturation {
		mag = a.Saturation
	}
	rd.norm = (mag - a.Deadzone) / (a.Saturation - a.Deadzone)
	rd.active = rd.norm >= a.DirectionThreshold
	if !rd.active && a.Mode != "mouse" {
		return rd
	}
	rd.angle = math.Atan2(x, y) * 180 / math.Pi
	if rd.angle < 0 {
		rd.angle += 360
	}
	return rd
}

// stickWant returns the direction controls stick which holds down and
// whether it is active.
func (e *Engine) stickWant(which int) (want [8]bool, active bool) {
	rd := e.readStick(which)
	return e.directionsFor(which, &rd), rd.active
}

// directionsFor returns the direction controls the reading rd of stick
// which holds down, by its mode. A knob has none (ANA-11), nor has a stick that
// moves the pointer.
func (e *Engine) directionsFor(which int, rd *stickReading) (want [8]bool) {
	a, angle := rd.cfg, rd.angle
	if !rd.active || a.Mode == "knob" || a.Mode == "mouse" {
		return want
	}
	rawCard := int(math.Floor((angle+45)/90)) % 4
	card := [4]int{0, 3, 1, 2}[rawCard]
	diag := int(math.Floor(angle/90)) % 4
	// Diagonal sectors are centered at 45, 135, 225 and 315 degrees and
	// wrap around at 360 degrees.
	nearDiag := angleDistance(angle, float64(diag*90+45)) <= a.DiagonalWidth/2
	if nearDiag {
		diagCtl := [4]int{5, 7, 6, 4}[diag]
		pair := [4][2]int{{0, 3}, {1, 3}, {1, 2}, {0, 2}}[diag]
		switch a.Mode {
		case "fourWayDiagonal":
			want[diagCtl] = true
		case "eightWay":
			want[diagCtl] = true
		case "standard":
			want[pair[0]] = true
			want[pair[1]] = true
		default:
			want[card] = true
		}
	} else {
		switch a.Mode {
		case "fourWayDiagonal":
			want[[4]int{5, 7, 6, 4}[diag]] = true
		case "eightWay":
			want[card] = true
		default:
			want[card] = true
		}
	}
	if a.Mode == "eightWay" && want[[4]int{5, 7, 6, 4}[diag]] && a.DiagonalFallback == "cardinals" {
		cs := stickControls(which)
		diagCtl := [4]int{5, 7, 6, 4}[diag]
		_, b, _, _ := e.lookup(cs[diagCtl], GesturePress)
		if b == nil {
			want[diagCtl] = false
			pair := [4][2]int{{0, 3}, {1, 3}, {1, 2}, {0, 2}}[diag]
			want[pair[0]] = true
			want[pair[1]] = true
		}
	}
	// Keep the previous sector until the stick has moved past its boundary
	// plus hysteresis. This prevents chatter when the axis sits on a cardinal
	// or diagonal boundary.
	if a.HysteresisDeg > 0 {
		if center, ok := directionCenter(e.sticks[which].current); ok && !sameDirections(e.sticks[which].current, want) {
			sector := 45.0
			switch a.Mode {
			case "standard":
				if standardDiagonal(e.sticks[which].current) {
					sector = a.DiagonalWidth / 2
				} else {
					sector = (90 - a.DiagonalWidth) / 2
				}
			case "eightWay", "fourWayDiagonal":
				sector = 22.5
			}
			if angleDistance(angle, center) <= sector+a.HysteresisDeg {
				want = e.sticks[which].current
			}
		}
	}
	return want
}

func directionCenter(d [8]bool) (float64, bool) {
	// standard mode represents a diagonal as its two cardinals. Check this
	// before the individual cardinals so the hysteresis center is 45/135/…,
	// rather than the first cardinal's 0/90/… center.
	for i, center := range [...]float64{45, 135, 225, 315} {
		pair := [4][2]int{{0, 3}, {1, 3}, {1, 2}, {0, 2}}[i]
		if d[pair[0]] && d[pair[1]] {
			return center, true
		}
	}
	for i, center := range [...]float64{0, 180, 270, 90} {
		if d[i] {
			return center, true
		}
	}
	for i, center := range [...]float64{315, 45, 225, 135} {
		if d[i+4] {
			return center, true
		}
	}
	return 0, false
}

func standardDiagonal(d [8]bool) bool {
	return (d[0] && d[3]) || (d[1] && d[3]) || (d[1] && d[2]) || (d[0] && d[2])
}

func axisForStick(which int, x bool) control.Axis {
	if which == 0 {
		if x {
			return control.LeftX
		}
		return control.LeftY
	}
	if x {
		return control.RightX
	}
	return control.RightY
}

func angleDistance(a, b float64) float64 {
	d := math.Abs(a - b)
	if d > 180 {
		d = 360 - d
	}
	return d
}

func (e *Engine) blocker() control.Control {
	for c := range e.ctl {
		if e.ctl[c].pending {
			return control.Control(c)
		}
	}
	return control.Invalid
}

// bufferedRelease returns the original time of the first edge of control c
// in the buffer, when that edge is a release.
func (e *Engine) bufferedRelease(c control.Control) (clock.Time, bool) {
	for _, in := range e.buffer[:e.buffered] {
		if in.Control == c {
			return in.T, !in.Down
		}
	}
	return 0, false
}

// releasedBefore reports whether the first edge of control c in the buffer
// is a release whose original time is before t.
func (e *Engine) releasedBefore(c control.Control, t clock.Time) bool {
	raw, ok := e.bufferedRelease(c)
	return ok && raw < t
}

// popFront removes the first edge of the buffer and returns it. The buffer
// is a queue: edges leave it only in the order they came.
func (e *Engine) popFront() Input {
	in := e.buffer[0]
	copy(e.buffer[:], e.buffer[1:e.buffered])
	e.buffered--
	return in
}

// drain retains input order and original timestamps for recognizers,
// while all decisions and output are stamped with the dispatch time. Only
// the decision of a pending gesture, its hold or tap action, comes before
// the edges it captured; every edge, the pending control's release too,
// replays in the order it came (ARCHITECTURE §7.7).
func (e *Engine) drain(now clock.Time) {
replay:
	for e.buffered > 0 {
		if c := e.blocker(); c != control.Invalid {
			st := &e.ctl[c]
			policy := e.policy(st)
			var seen [control.Count]bool
			resolved := false
			for _, in := range e.buffer[:e.buffered] {
				if in.Control == c && !in.Down {
					e.decideTap(now, c, in.SuppressActions)
					resolved = true
					break
				}
				if in.Down {
					seen[in.Control] = true
					if policy == "holdPreferred" {
						e.commitHold(now, c)
						resolved = true
						break
					}
				} else if policy == "balanced" && seen[in.Control] {
					e.commitHold(now, c)
					resolved = true
					break
				}
			}
			if !resolved {
				break replay
			}
			continue
		}
		in := e.popFront()
		// A multi-tap window whose deadline is at or before this edge's
		// original time was over before the edge came.
		e.commitMultiDue(now, in.T)
		if in.Down {
			e.flushMultiOnPress(now, in.Control)
			e.press(now, in.T, in.Control, in.fn)
		} else if e.ctl[in.Control].down {
			e.release(now, in)
		}
	}
	// A replayed release opens its window at its original time, so the
	// window may be over already.
	e.commitMultiDue(now, now)
}

// multiHeld reports whether the multi-tap window of control c waits for a
// press in the buffer: the first buffered press is c's and came before the
// window's deadline, so its replay continues the count. The window is not
// due at its deadline then, and NextDeadline leaves it out: the pending
// gesture that holds that press up has a deadline of its own. A window
// opens only when its release is replayed, in order, so every buffered
// edge came after that release.
func (e *Engine) multiHeld(c control.Control) bool {
	for _, in := range e.buffer[:e.buffered] {
		if in.Down {
			return in.Control == c && in.T < e.multi[c].deadline
		}
	}
	return false
}

// multiDue reports whether the multi-tap window of control c is over at t:
// it is open, its deadline is at or before t, and it does not wait for a
// buffered press (multiHeld).
func (e *Engine) multiDue(c control.Control, t clock.Time) bool {
	m := &e.multi[c]
	return m.count > 0 && m.deadline <= t && !e.multiHeld(c)
}

// commitMultiDue commits, at now, every multi-tap window that is over at t.
func (e *Engine) commitMultiDue(now, t clock.Time) {
	for c := range e.multi {
		if e.multiDue(control.Control(c), t) {
			e.commitMulti(now, control.Control(c))
		}
	}
}

func (e *Engine) policy(st *ctlState) string {
	b := st.binding
	if b.Policy != "" {
		return b.Policy
	}
	if b.Hold != nil && (b.Hold.Type == profile.ActionLayer || b.Hold.Type == profile.ActionMouse || b.Hold.Type == profile.ActionHUD || b.Hold.Keys.Key().IsModifier()) {
		return "holdPreferred"
	}
	return "balanced"
}

func (e *Engine) commitHold(now clock.Time, c control.Control) {
	st := &e.ctl[c]
	st.pending = false
	st.holding = true
	st.tapCount = 0
	st.deadline = 0
	e.runAction(now, c, st, GestureHold, st.binding.Hold)
}

func (e *Engine) runAction(now clock.Time, c control.Control, st *ctlState, g Gesture, a *profile.Action) {
	if st.plan != nil {
		e.gestures.ObserveGesture(e.gestureEvent(now, c, st, g))
		// The plan has an action for every gesture something answers, so a
		// gesture it has none for (the release of most controls) is only
		// offered to the script, and is not worth a decision; the press is
		// always one, whatever the plan has, and so is the end of a multi-tap
		// window (commitMulti) of a cycle that nothing has answered: the press
		// that opened it waits for it.
		expected := a != nil || g == GesturePress || isTapCount(g) && !st.answered
		var binding *profile.Binding
		var level string
		var outcome gestureOutcome
		a, binding, level, outcome = e.resolveGesture(now, c, st, g)
		if outcome != gestureFound {
			if expected {
				e.decideUnresolved(now, c, st, g, binding, level, outcome)
			}
			return
		}
		original := st.binding
		st.binding = binding
		st.latching = st.latching || binding.Latch
		defer func() { st.binding = original }()
	}
	if a == nil {
		if isTapCount(g) && !st.answered {
			e.decideNoTapAction(now, c, st, g)
		}
		return
	}
	owner := OwnerID{Source: SourceDecl, Cycle: st.cycle}
	st.gesture = g
	e.decide(now, c, EdgeDown, st, a, owner, ReasonDispatch)
	// The haptic of the binding plays once for the dispatch (MAP-25), and a
	// knob step's tick (HUD-10) when the binding has none of its own: the
	// repeats of a held key start at advanceAction and do not come here. A
	// second gesture of the same cycle at the same instant (the hold that a
	// press commits at once when the binding has no tap) is the same moment
	// and plays nothing more.
	if st.binding != nil && !st.hapticPlayedAt(now) {
		p := st.binding.Haptic
		if p == 0 && e.knobTick(c) {
			p = haptic.Tick
		}
		if p != 0 {
			st.hapticSet, st.hapticCycle, st.hapticAt = true, st.cycle, now
			e.playHaptic(p)
		}
	}
	if a.Type == profile.ActionLayer {
		e.applyLayerAction(now, a, st)
		return
	}
	if a.Type == profile.ActionScript {
		if e.script != nil {
			e.script(ScriptActionEvent{Name: a.Name, Args: a.Args, GestureEvent: e.gestureEvent(now, c, st, g)})
		}
		return
	}
	if a.Type == profile.ActionMacro {
		// A macro runs once and holds nothing of the engine's: its keys
		// are its run's, which the runtime releases (SCR-50).
		if e.script != nil {
			e.script(ScriptActionEvent{Macro: a.File, GestureEvent: e.gestureEvent(now, c, st, g)})
		}
		return
	}
	if a.Type == profile.ActionProfile {
		if e.profileSwitch != nil {
			e.profileSwitch(a.Profile)
		}
		return
	}
	if a.Type == profile.ActionSequence || a.Type == profile.ActionText {
		// A sequence and a text hold nothing: the Output runs them to the end or
		// until its owner is released, and there is no repeat to start.
		if a.Type == profile.ActionSequence {
			e.out.Sequence(owner, a.Steps, e.tapPressDuration(st.binding), e.tapGapDuration(st.binding))
		} else {
			e.out.TypeText(owner, a.Text, e.tapGapDuration(st.binding))
		}
		return
	}
	if a.Type == profile.ActionMouse {
		e.runMouseAction(now, st, a, owner)
		return
	}
	if a.Type == profile.ActionHUD {
		e.runHUDAction(st, a)
		return
	}
	if len(a.Sequence) == 0 {
		e.runChordAction(st, a, owner, a.Keys)
	} else {
		e.out.TapSequence(owner, a.Sequence, e.tapPressDuration(st.binding), e.tapGapDuration(st.binding))
	}
	// A replayed press or hold whose release waits in the buffer does not
	// repeat: the control is up already (OUT-11).
	if _, released := e.bufferedRelease(c); a.Repeat != nil && !released {
		e.startRepeat(now, c, st, a, owner)
	}
}

func (e *Engine) runChordAction(st *ctlState, a *profile.Action, owner OwnerID, chord keys.Chord) {
	switch a.Mode {
	case profile.ModeHold:
		if st.holdsN < len(st.holds) {
			st.holds[st.holdsN] = a
			st.holdsN++
		}
		st.held = a
		var minPress time.Duration
		if b := st.binding; b != nil && b.Timing != nil && (b.Timing.TapPressSet || b.Timing.TapPressMs > 0) {
			minPress = time.Duration(b.Timing.TapPressMs) * time.Millisecond
		}
		e.out.Down(owner, chord, minPress)
	case profile.ModeTap:
		e.out.Tap(owner, chord, e.tapPressDuration(st.binding))
	}
}

func (e *Engine) advanceAction(now clock.Time, c control.Control, a *actionState) {
	if a.action == nil || a.next > now {
		return
	}
	if a.style == profile.RepeatTap {
		e.out.RepeatTap(a.owner, a.action.Keys, e.tapPressDuration(a.binding))
	} else {
		e.out.Repeat(a.owner, firstChord(a.action))
	}
	a.last, a.armed = now, true
	a.next = now.Add(e.repeatInterval(c, a.action, a.binding, a.mod))
}

func (e *Engine) repeatMinimum(binding *profile.Binding) time.Duration {
	timing := e.prog.Timing()
	return max(e.tapPressDuration(binding), time.Duration(timing.TapPressMs)*time.Millisecond) + max(e.tapGapDuration(binding), time.Duration(timing.TapGapMs)*time.Millisecond)
}

func firstChord(a *profile.Action) keys.Chord {
	if len(a.Sequence) > 0 {
		return a.Sequence[0]
	}
	return a.Keys
}

// lookup finds the binding that a press of c finds now (ARCHITECTURE §7.3).
func (e *Engine) lookup(c control.Control, g Gesture) (string, *profile.Binding, *profile.Action, bool) {
	return e.lookupWith(c, g, e.fnActive)
}

// lookupWith is lookup with the function key fn, control.Invalid for none, as
// the one held: the set of that key is the first level (below the script's
// global(), which the caller looks at before), above the registry's levels
// or the overlays.
func (e *Engine) lookupWith(c control.Control, g Gesture, fn control.Control) (string, *profile.Binding, *profile.Action, bool) {
	if fn != control.Invalid {
		if l, name := e.prog.functionSet(fn); l != nil {
			if b := l.Binding(c); b != nil {
				return name, b, b.Action(g), b.Block
			}
		}
	}
	if e.registry != nil {
		levels := e.registry.Levels()
		for i := len(levels) - 1; i >= 0; i-- {
			if b := levels[i].Binding(c); b != nil {
				return levels[i].Name(), b, b.Action(g), b.Block
			}
		}
		return "", nil, nil, false
	}
	for i := len(e.overlays) - 1; i >= 0; i-- {
		if l := e.prog.layer(e.overlays[i].name); l != nil {
			if b := l.Binding(c); b != nil {
				return l.Name, b, b.Action(g), b.Block
			}
		}
	}
	if l := e.prog.layer(e.baseLayer); l != nil {
		if b := l.Binding(c); b != nil {
			return l.Name, b, b.Action(g), b.Block
		}
	}
	if l := e.prog.common; l != nil {
		if b := l.Binding(c); b != nil {
			return l.Name, b, b.Action(g), b.Block
		}
	}
	return "", nil, nil, false
}

// NextDeadline returns the earliest gesture or action timer. It leaves out
// a multi-tap window that waits for a buffered press (multiHeld).
func (e *Engine) NextDeadline() (clock.Time, bool) { return e.nextDeadline(false) }

// NextSettleDeadline is NextDeadline for a caller that runs the deadlines in
// virtual time until none is left (slot.Slot.Replay after the last event of a
// trace): it also leaves out the ticks of the pointer of a stick that is still
// pushed in mode "mouse". That pointer keeps going for as long as the stick
// is held, like a key held at the end of the trace, and only the caller's
// ReleaseAll ends it, so waiting for it would never be done. The ticks of a
// pointer that is gliding to a stop (the stick let go, smoothing on) are
// included: they end by themselves.
func (e *Engine) NextSettleDeadline() (clock.Time, bool) { return e.nextDeadline(true) }

// nextDeadline is NextDeadline; with settle it leaves out the ticks of the
// pointers that a stick still pushed keeps going (NextSettleDeadline).
func (e *Engine) nextDeadline(settle bool) (clock.Time, bool) {
	var d clock.Time
	ok := false
	take := func(t clock.Time) {
		if !ok || t < d {
			d, ok = t, true
		}
	}
	for c := range e.ctl {
		if st := &e.ctl[c]; st.pending {
			take(st.deadline)
		}
		if m := &e.multi[c]; m.count > 0 && !e.multiHeld(control.Control(c)) {
			take(m.deadline)
		}
		for i := range e.actions[c] {
			if a := &e.actions[c][i]; a.action != nil {
				take(a.next)
			}
		}
	}
	for i := range e.sticks {
		if e.sticks[i].pendingSet {
			take(e.sticks[i].deadline)
		}
		if e.sticks[i].ticks != 0 && !e.inputOnly {
			take(e.sticks[i].knobNext)
		}
		if e.sticks[i].knobEvalSet && !e.inputOnly {
			take(e.sticks[i].knobEvalAt)
		}
		if s := &e.sticks[i]; s.mouseOn && !e.inputOnly && !(settle && (s.mouseTx != 0 || s.mouseTy != 0)) {
			take(s.mouseAt)
		}
	}
	return d, ok
}

// Apply dispatches due work once; late repeats do not produce catch-up bursts.
func (e *Engine) Apply(now clock.Time) {
	for i := range e.sticks {
		s := &e.sticks[i]
		if s.pendingSet && s.deadline <= now {
			want, _ := e.stickWant(i)
			if sameDirections(want, s.pending) {
				e.commitStick(now, i, want)
			} else {
				s.pendingSet = false
				s.deadline = 0
				e.updateStick(now, i)
			}
		}
		if s.knobEvalSet && s.knobEvalAt <= now && !e.inputOnly {
			e.settleKnob(now, i)
		}
		e.emitKnob(now, i)
		if s.mouseOn && s.mouseAt <= now && !e.inputOnly {
			e.tickMouse(now, i)
		}
	}
	for c := range e.ctl {
		if st := &e.ctl[c]; st.pending && st.deadline <= now {
			e.commitHold(now, control.Control(c))
		}
		if e.multiDue(control.Control(c), now) {
			e.commitMulti(now, control.Control(c))
		}
		for i := range e.actions[c] {
			a := &e.actions[c][i]
			if a.action != nil && a.next <= now {
				e.advanceAction(now, control.Control(c), a)
			}
		}
	}
	e.drain(now)
}

// commitMulti ends the multi-tap window of control c at now: it runs the
// action for the number of taps in the window, or, when the binding has none
// for that number, decides that (decideNoTapAction), so the press that began
// the count does not stay unanswered; a cycle that something answered already
// has nothing more to be told. A window that CancelAll, Reset or Reload clears
// is dropped, not committed: cancelAll answers the cycle of a window that
// waits.
func (e *Engine) commitMulti(now clock.Time, c control.Control) {
	m := e.multi[c]
	e.multi[c] = multiState{}
	g, a := tapActionForCount(m.state.binding, m.count)
	e.runAction(now, c, &m.state, g, a)
}

// flushMultiOnPress commits, at now, the multi-tap windows of the controls
// other than c: a press of another control ends them. Every open window was
// opened by a release that came before this press, since releases open
// windows only in input order.
func (e *Engine) flushMultiOnPress(now clock.Time, c control.Control) {
	for i := range e.multi {
		if control.Control(i) != c && e.multi[i].count > 0 {
			e.commitMulti(now, control.Control(i))
		}
	}
}

// CancelAll ends every active press cycle at now (OUT-07 engine level,
// ARCHITECTURE §7.4). It sends nothing but the Up of the latched chords
// (MAP-10): the caller has the Arbiter release the cycles' other keys right
// after. The controls stay down in the engine's view, so their physical
// release does nothing, and a trigger presses again only after it falls to
// TriggerRelease. CancelAll clears everything that would send output
// later: the held chords, latches, buffered edges, pending gestures and
// multi-tap windows, repeats, stick direction delays and the overlays. The
// pointer of a stick in mode "mouse" stops too, and the stick has to come back
// inside the deadzone before it moves it again.
func (e *Engine) CancelAll(now clock.Time) { e.cancelAll(now, true) }

// cancelAll is CancelAll. With lockPointer false the pointers of the sticks in
// mode "mouse" are left as they are: Reload, which takes the new settings into
// them next, does not stop a stick that is still pushed.
func (e *Engine) cancelAll(now clock.Time, lockPointer bool) {
	e.layerEpoch++
	e.buffered = 0
	for c := range e.ctl {
		st := &e.ctl[c]
		if st.down {
			e.endScriptGesture(now, control.Control(c), st, true)
		}
		if st.down && !st.canceled {
			e.decide(now, control.Control(c), EdgeNone, st, st.held, OwnerID{}, ReasonCancel)
		}
		// The function key has no cycle to cancel: its edges never reach ctl.
		st.down = e.physical[c] && control.Control(c) != e.prog.fnKey
		st.canceled = true
		st.held = nil
		st.holds = [2]*profile.Action{}
		st.holdsN = 0
		st.layerHolds = [2]string{}
		st.layerHoldsN = 0
		st.precision = 0
		st.hints = false
		st.pending = false
		st.deadline = 0
	}
	// A multi-tap window is dropped, not committed. The press that opened it
	// waited for its end, so a cycle that nothing has answered is canceled here
	// as a held one is above (ReasonCancel); it holds nothing.
	for c := range e.multi {
		if m := &e.multi[c]; m.count > 0 && !m.state.answered {
			e.decide(now, control.Control(c), EdgeNone, &m.state, nil, OwnerID{}, ReasonCancel)
		}
	}
	clear(e.multi[:])
	for c := range e.latch {
		l := &e.latch[c]
		for i := l.holdsN - 1; i >= 0; i-- {
			e.out.Up(l.owner, firstChord(l.holds[i]))
		}
	}
	clear(e.latch[:])
	// The precision actions end with the cycles that held them; the pointer
	// itself stops in settleVirtual below (unless it is kept).
	e.precision = 0
	// Likewise the key hints (hints.go): every cycle that held them is over, and
	// so is every latch, so there is nothing to show.
	e.hints = hintsState{}
	clear(e.actions[:])
	e.modulated = 0
	for i := range e.sticks {
		e.sticks[i].pendingSet = false
		e.sticks[i].deadline = 0
	}
	e.clearKnob()
	if len(e.overlays) > 0 {
		e.overlays = e.overlays[:0]
		e.notifyLayer(now, false)
	}
	e.settleVirtual(now, false, lockPointer)
}

// Pause puts the engine in paused mode at now (DMN-06): it runs CancelAll,
// so the caller has the Arbiter release the keys right after, and until
// Resume every press ends its cycle at once and sends nothing. Its
// decision has ReasonPaused and what the lookup found, and its release is
// ReasonCanceled, as after CancelAll. Pause does nothing, and decides
// nothing, when the engine is already paused. Reload, Reset and CancelAll
// keep the engine paused.
func (e *Engine) Pause(now clock.Time) {
	if e.paused {
		return
	}
	e.CancelAll(now)
	e.paused = true
	e.publishFeedback(true, true)
}

// Resume leaves paused mode. It sends and decides nothing, and presses
// nothing again: a control held across it stays canceled, so its release
// does nothing, and a trigger presses again only after it falls to the
// release threshold. Resume does nothing when the engine is not paused.
func (e *Engine) Resume() {
	if !e.paused {
		return
	}
	e.paused = false
	e.publishFeedback(true, true)
}

// Paused reports whether the engine is in paused mode: between Pause and
// Resume.
func (e *Engine) Paused() bool { return e.paused }

// Reload switches the engine to prog at now (PRO-03): it runs CancelAll,
// then every later press looks its binding up in prog, and the trigger
// thresholds are prog's. The controls down stay down and canceled, as
// after CancelAll: the release of one reports the binding its press found
// in the old program, with ReasonCanceled, and sends nothing, and a
// trigger presses again only after it falls to the release threshold.
// Cycle ids keep counting, and a paused engine stays paused. prog may be
// the Program the engine has. Reload panics if prog is nil.
//
// The one thing a Reload does not stop is the pointer of a stick that is still
// pushed in mode "mouse": a reload comes from an autosave, a hot reload or an
// automatic profile switch, which can fall while the user holds the stick, so
// the pointer goes on with the new settings, unless the stick is locked, as
// after a pause.
//
// A prog with another function key (config.json's functionKey changed,
// ADR-0075) makes the old key an ordinary control and the new one the
// function key. Nothing is held then: the engine forgets that the function key
// is held, whichever of the two it was, and the new key, even if it is down
// now, selects the function set only from its next press. The old key, if it
// is down, has no cycle, as it had none as the function key: its release sends
// and decides nothing.
func (e *Engine) Reload(now clock.Time, prog *Program) {
	if prog == nil {
		panic("engine: Reload needs a Program")
	}
	e.cancelAll(now, false)
	if e.prog.fnKey != prog.fnKey {
		e.fnActive = control.Invalid
		e.ctl[prog.fnKey] = ctlState{} // it may have been down, as an ordinary control
	}
	e.prog = prog
	e.resolveCalibration() // settled with everything else just below
	e.baseLayer = prog.defaultLayer
	e.overlays = make([]overlay, 0, len(prog.layers))
	e.settleVirtual(now, true, false)
	e.notifyLayer(now, false)
}

// Reset runs CancelAll, then forgets which controls are down, as if every
// control were up: for a device that went away or came back. Cycle ids
// keep counting, and a paused engine stays paused.
func (e *Engine) Reset(now clock.Time) {
	e.CancelAll(now)
	clear(e.ctl[:])
	clear(e.physical[:])
	clear(e.multi[:])
	clear(e.latch[:])
	clear(e.axes[:])
	clear(e.sticks[:])
	clear(e.dpadPhysical[:])
	clear(e.dpadOrder[:])
	e.dpadOrderN = 0
	clear(e.dpadVirtual[:])
	clear(e.halfAxis[:])
	clear(e.triggerFull[:])
	e.baseLayer = e.prog.defaultLayer
	e.overlays = e.overlays[:0]
	// Every control is up again, the function keys too.
	e.fnActive = control.Invalid
	e.notifyLayer(now, false)
}

// press starts a cycle for control c, which is up, and runs its press
// action. While paused the cycle ends as it starts, as if CancelAll had
// ended it.
func (e *Engine) press(now, raw clock.Time, c, fn control.Control) {
	st := &e.ctl[c]
	level, b, a, blocked := e.lookupWith(c, GesturePress, fn)
	var plan *gesturePlan
	if e.gestures != nil {
		plan, b = e.scriptPlan(c, fn)
		a = b.Press
		blocked = false
	}
	if l := &e.latch[c]; l.holdsN > 0 || l.layerHoldsN > 0 || l.precision > 0 || l.hints {
		for i := l.holdsN - 1; i >= 0; i-- {
			e.out.Up(l.owner, firstChord(l.holds[i]))
		}
		e.releaseLayers(now, l.layerHolds[:l.layerHoldsN])
		hints := l.hints
		*l = latchState{}
		e.updatePrecision(now)
		if hints {
			e.updateHints()
		}
		e.cycle++
		*st = ctlState{down: true, cycle: e.cycle, canceled: true, gesture: GesturePress, level: level, binding: b}
		e.decide(now, c, EdgeDown, st, nil, OwnerID{}, ReasonCanceled)
		return
	}
	// A multi-tap window belongs to the binding that opened it. If a function
	// key set took the control over, or gave it back, since the window opened
	// (the lookup above is made on arrival), this press is not the next tap of
	// that window: it ends the window like the press of another control, and
	// starts a new count. A script's plan is the snapshot of the first press
	// and goes on (ARCHITECTURE §7.9).
	if m := &e.multi[c]; m.count > 0 && m.state.plan == nil && level != m.state.level && (e.prog.isFunctionLevel(level) || e.prog.isFunctionLevel(m.state.level)) {
		e.commitMulti(now, c)
	}
	cycle := uint64(0)
	count := 1
	if m := e.multi[c]; m.count > 0 {
		cycle = m.state.cycle
		count = m.count + 1
		if m.state.plan != nil {
			plan, b, level = m.state.plan, m.state.binding, m.state.level
			a = b.Press
		}
		e.multi[c] = multiState{}
	} else {
		e.cycle++
		cycle = e.cycle
	}
	*st = ctlState{down: true, cycle: cycle, canceled: e.paused, gesture: GesturePress, level: level, binding: b, tapCount: count, plan: plan, started: raw}
	if e.paused {
		e.decide(now, c, EdgeDown, st, nil, OwnerID{}, ReasonPaused)
		return
	}
	if blocked {
		e.decide(now, c, EdgeDown, st, nil, OwnerID{}, ReasonBlocked)
		return
	}
	if a != nil || plan != nil {
		e.runAction(now, c, st, GesturePress, a)
	} else {
		e.decide(now, c, EdgeDown, st, nil, OwnerID{}, ReasonUnbound)
	}
	if b == nil || b.Hold == nil {
		return
	}
	if b.Tap != nil || hasMulti(b) {
		st.pending = true
		st.deadline = raw.Add(time.Duration(e.prog.timingFor(b).HoldMs) * time.Millisecond)
		// A replayed press can be past its deadline, which counts from its
		// original time. It is a hold then, unless its own release, also
		// buffered, came before the deadline: drain then judges it from
		// the buffer, in the original order, as it would have been judged
		// when the edges came (a tap, or a hold by an interrupt before the
		// release).
		if st.deadline <= now && !e.releasedBefore(c, st.deadline) {
			e.commitHold(now, c)
		}
	} else {
		e.commitHold(now, c)
	}
}

// decideTap resolves the pending gesture of control c as a tap: drain found
// c's release in the buffer before an interrupt that makes it a hold. Like
// commitHold, it runs only the decision ahead of the edges the gesture
// captured: the tap action, unless the release opens a multi-tap window.
// The release stays in its place in the buffer and replays in order, so
// the edges before it still find the keys and layers c's press action
// holds, the release action comes after them, and a window opens only
// after them, so that their presses do not commit it (ARCHITECTURE §7.7).
// suppressActions is the release's: a consumed release runs no tap.
func (e *Engine) decideTap(now clock.Time, c control.Control, suppressActions bool) {
	st := &e.ctl[c]
	st.pending = false
	st.deadline = 0
	b := st.binding
	if suppressActions || hasMulti(b) && st.tapCount < maxTapCount(b) {
		return
	}
	g, a := tapActionForCount(b, st.tapCount)
	e.runAction(now, c, st, g, a)
	st.tapped = true
}

// release ends the cycle of the control of in, a release: the multi-tap
// window it opens counts from in.T, however late the release is replayed.
// It does not run the tap action again when decideTap ran it (st.tapped).
func (e *Engine) release(now clock.Time, in Input) {
	c, raw, suppressActions := in.Control, in.T, in.SuppressActions
	st := &e.ctl[c]
	st.pending = false
	st.deadline = 0
	if st.canceled {
		e.decide(now, c, EdgeUp, st, nil, OwnerID{}, ReasonCanceled)
		*st = ctlState{}
		return
	}
	// Stop repeating before a release action can start another tap. An active
	// tap finishes its minimum duration; unsent taps of this owner are dropped.
	owner := OwnerID{Source: SourceDecl, Cycle: st.cycle}
	e.stopRepeats(c)
	b := st.binding
	if !suppressActions && b != nil && !st.holding && !st.tapped && (b.Tap != nil || hasMulti(b)) {
		if hasMulti(b) && st.tapCount < maxTapCount(b) {
			e.multi[c] = multiState{count: st.tapCount, deadline: raw.Add(time.Duration(e.prog.timingFor(b).MultiTapMs) * time.Millisecond), state: *st}
		} else {
			g, a := tapActionForCount(b, st.tapCount)
			e.runAction(now, c, st, g, a)
		}
	} else if suppressActions && !st.answered && waitsAfterPress(b) {
		e.decideConsumedRelease(now, c, st)
	}
	switch {
	case st.holdsN > 0:
		e.decide(now, c, EdgeUp, st, st.held, owner, ReasonRelease)
		if b != nil && b.Latch || st.latching {
			e.latch[c] = latchState{holds: st.holds, holdsN: st.holdsN, owner: owner}
		} else {
			for i := st.holdsN - 1; i >= 0; i-- {
				e.out.Up(owner, firstChord(st.holds[i]))
			}
		}
	case b == nil || b.Tap == nil && !hasMulti(b):
		e.decide(now, c, EdgeUp, st, nil, OwnerID{}, ReasonEnd)
	}
	if st.layerHoldsN > 0 {
		if b != nil && b.Latch || st.latching {
			e.latch[c].layerHolds = st.layerHolds
			e.latch[c].layerHoldsN = st.layerHoldsN
			e.latch[c].owner = owner
		} else {
			e.releaseLayers(now, st.layerHolds[:st.layerHoldsN])
		}
	}
	if st.precision > 0 && (b != nil && b.Latch || st.latching) {
		// A latched precision stays until the control is pressed again, like
		// the keys a latch keeps down.
		e.latch[c].precision = st.precision
	}
	if st.hints && (b != nil && b.Latch || st.latching) {
		// ... and so does a latched hints action.
		e.latch[c].hints = true
	}
	if b != nil && !suppressActions {
		e.runAction(now, c, st, GestureRelease, b.Release)
		// The window this release opened (above) kept a copy of the cycle from
		// before its release action ran: if that action answered the press, the
		// window's end has nothing to answer. A window only this release can
		// have opened is open here, since the press that began the cycle took
		// over any earlier one.
		if m := &e.multi[c]; st.answered && m.count > 0 {
			m.state.answered = true
		}
	}
	e.endScriptGesture(now, c, st, false)
	hints := st.hints
	*st = ctlState{}
	e.updatePrecision(now)
	if hints {
		e.updateHints()
	}
}

// stopRepeats ends the typematic repeats of control c.
func (e *Engine) stopRepeats(c control.Control) {
	for i := range e.actions[c] {
		if a := &e.actions[c][i]; a.action != nil && a.repeat {
			if a.style == profile.RepeatTap {
				e.out.StopRepeat(a.owner, a.action.Keys)
			}
			if a.mod {
				e.modulated--
			}
			*a = actionState{}
		}
	}
}

func hasMulti(b *profile.Binding) bool {
	return b.DoubleTap != nil || b.TripleTap != nil || b.QuadTap != nil
}
func maxTapCount(b *profile.Binding) int {
	switch {
	case b.QuadTap != nil:
		return 4
	case b.TripleTap != nil:
		return 3
	case b.DoubleTap != nil:
		return 2
	default:
		return 1
	}
}
func tapActionForCount(b *profile.Binding, n int) (Gesture, *profile.Action) {
	g := GestureTap
	switch n {
	case 2:
		g = GestureDoubleTap
	case 3:
		g = GestureTripleTap
	case 4:
		g = GestureQuadTap
	}
	return g, b.Action(g)
}

// isTapCount reports whether g is the gesture of a number of taps, from the
// tap to the quad tap: what tapActionForCount returns.
func isTapCount(g Gesture) bool {
	switch g {
	case GestureTap, GestureDoubleTap, GestureTripleTap, GestureQuadTap:
		return true
	}
	return false
}

func (e *Engine) tapPressDuration(b *profile.Binding) time.Duration {
	d := e.prog.timing.TapPressMs
	if b != nil && b.Timing != nil && (b.Timing.TapPressSet || b.Timing.TapPressMs > 0) {
		d = b.Timing.TapPressMs
	}
	return time.Duration(d) * time.Millisecond
}
func (e *Engine) tapGapDuration(b *profile.Binding) time.Duration {
	d := e.prog.timing.TapGapMs
	if b != nil && b.Timing != nil && (b.Timing.TapGapSet || b.Timing.TapGapMs > 0) {
		d = b.Timing.TapGapMs
	}
	return time.Duration(d) * time.Millisecond
}

// decide reports a decision about the cycle of control c, whose state is
// st.
func (e *Engine) decide(t clock.Time, c control.Control, edge Edge, st *ctlState, a *profile.Action, owner OwnerID, r Reason) {
	if r != ReasonUnbound {
		st.answered = true
	}
	if e.trace == nil {
		return
	}
	e.trace(Decision{
		T: t, Control: c, Edge: edge, Cycle: st.cycle,
		Gesture: st.gesture, Level: st.level, Binding: st.binding, Action: a,
		Owner: owner, Reason: r,
	})
}

// decideNoTapAction records the end of a multi-tap window whose number of
// taps (g, from tapActionForCount) the binding has no action for, such as a
// single tap of a binding that has only a double tap (ADR-0071). The press
// that began the count was unbound and waiting; this is the answer, and says
// the wait ended with nothing to run. Like the press, it has the binding and
// level the press resolved, and no action or owner. The edge is EdgeDown, as
// runAction records every gesture. It changes nothing in st.
//
// Its callers record it only for a cycle nothing has answered (st.answered):
// a press or release action that ran, or a script that took the press, is the
// answer, and "no action for one tap" would contradict the key that was sent.
func (e *Engine) decideNoTapAction(t clock.Time, c control.Control, st *ctlState, g Gesture) {
	if e.trace == nil {
		return
	}
	e.trace(Decision{T: t, Control: c, Edge: EdgeDown, Cycle: st.cycle, Gesture: g, Level: st.level, Binding: st.binding, Reason: ReasonUnbound})
}

// decideConsumedRelease records the release of a cycle that a script took
// (Options.Button returned true for it, or the caller set
// Input.SuppressActions) when nothing has answered the cycle: the release runs
// no tap and opens no multi-tap window, so the press that only started a wait
// for one of them would stay unanswered. It is the script's, like the press it
// consumed in stepButton: ReasonScript with no level, binding, action or owner,
// the release gesture, EdgeDown as for every gesture, and the cycle of the
// press.
func (e *Engine) decideConsumedRelease(t clock.Time, c control.Control, st *ctlState) {
	st.answered = true
	if e.trace == nil {
		return
	}
	e.trace(Decision{T: t, Control: c, Edge: EdgeDown, Cycle: st.cycle, Gesture: GestureRelease, Reason: ReasonScript})
}

// waitsAfterPress reports whether a press of b only starts a wait for a gesture
// that comes after it: a release, a tap, a multi-tap or a hold. These are the
// gestures the daemon's decision feed lists as waiting for an unbound press.
func waitsAfterPress(b *profile.Binding) bool {
	return b != nil && (b.Release != nil || b.Tap != nil || b.Hold != nil || hasMulti(b))
}

// decideUnresolved records a gesture g that resolveGesture found no action
// for, under a script's gesture registry (ADR-0071). It says what happened
// to it, as the declarations alone would have: a handler or a claim took it
// (ReasonScript, with no level, binding or action), a binding blocks the
// control (ReasonBlocked, with that binding and its level), or nothing answers
// it (ReasonUnbound, with the resolution binding of the cycle and no level:
// st.level is the level of a gesture found earlier). The edge is EdgeDown,
// as runAction records every gesture.
func (e *Engine) decideUnresolved(t clock.Time, c control.Control, st *ctlState, g Gesture, blocking *profile.Binding, level string, outcome gestureOutcome) {
	if outcome == gestureScript || outcome == gestureBlocked {
		st.answered = true
	}
	if e.trace == nil {
		return
	}
	d := Decision{T: t, Control: c, Edge: EdgeDown, Cycle: st.cycle, Gesture: g}
	switch outcome {
	case gestureScript:
		d.Reason = ReasonScript
	case gestureBlocked:
		d.Reason, d.Level, d.Binding = ReasonBlocked, level, blocking
	default:
		d.Reason, d.Binding = ReasonUnbound, st.binding
	}
	e.trace(d)
}

// spurious reports an ignored input.
func (e *Engine) spurious(t clock.Time, c control.Control, edge Edge, cycle uint64) {
	if e.trace != nil {
		e.trace(Decision{T: t, Control: c, Edge: edge, Cycle: cycle, Reason: ReasonSpurious})
	}
}

func edgeOf(down bool) Edge {
	if down {
		return EdgeDown
	}
	return EdgeUp
}

// trigger returns the virtual control that axis a drives, or
// control.Invalid for a stick axis.
func trigger(a control.Axis) control.Control {
	switch a {
	case control.LeftTrigger:
		return control.LT
	case control.RightTrigger:
		return control.RT
	}
	return control.Invalid
}
