package profile

import (
	"strconv"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// Profile is a validated profile with every name resolved and every
// default applied: what the engine compiler consumes.
type Profile struct {
	SchemaVersion int
	Name          string
	DefaultLayer  string
	ControlNames  map[string]string
	Timing        Timing
	Analog        Analog
	// AnalogResolved is true for profiles produced by Parse. It lets the
	// compiler distinguish an explicit numeric zero from an omitted field.
	AnalogResolved bool
	Feedback       *Feedback
	Script         *ScriptConfig
	Common         *Layer
	Layers         []*Layer
	// FunctionKeys holds the bindings used while a function key is held
	// (MAP-27, ADR-0069); nil when the file has no functionKeys.
	FunctionKeys *FunctionKeys
	// Extends is the library ID of the profile this one builds on (PRO-19,
	// ADR-0070), "" for a profile that does not. The profile is already the
	// merge of the whole chain: every member is the child's own or inherited.
	Extends string
	// Parents are the profiles of the chain, nearest first, with the sum of
	// the bytes that were merged; the daemon watches them.
	Parents []ParentRef
}

// FunctionKeys holds the sets of bindings of the two function keys, back
// (View, Create, Share, Minus) and start (Menu, Options, Plus). In every
// profile these two controls are function keys (control.Control.IsFunctionKey):
// they send nothing and cannot be bound in a layer. While one is held, the
// other controls look their binding up in that key's set first (ARCHITECTURE
// §7.3). A set is written like a layer, without a color, and cannot contain
// the function keys themselves. Back and Start are nil for a key without a
// set; their Name is FunctionLevel of their control.
type FunctionKeys struct {
	Back, Start *Layer
}

// Set returns the set of function key c, control.Back or control.Start, or
// nil when c has none, is not a function key, or f is nil.
func (f *FunctionKeys) Set(c control.Control) *Layer {
	if f == nil {
		return nil
	}
	switch c {
	case control.Back:
		return f.Back
	case control.Start:
		return f.Start
	}
	return nil
}

// FunctionLevel returns the name that the set of function key c has as a
// level of the lookup and in decisions (ENG-20): "function:back" or
// "function:start", and "" when c is not a function key. The colon keeps it
// apart from the name of a layer, which is free text, the way a script's
// "layer:x" scope is.
func FunctionLevel(c control.Control) string {
	switch c {
	case control.Back:
		return "function:back"
	case control.Start:
		return "function:start"
	}
	return ""
}

// FunctionSets returns the sets that p has, back's first, without the nil
// ones.
func (p *Profile) FunctionSets() []*Layer {
	var sets []*Layer
	if p.FunctionKeys != nil {
		for _, l := range []*Layer{p.FunctionKeys.Back, p.FunctionKeys.Start} {
			if l != nil {
				sets = append(sets, l)
			}
		}
	}
	return sets
}

// CurrentScriptAPI is the script API of this release, dtsgen.HostAPI (its
// tests check the two agree). script.api also takes the previous version,
// with a warning (SCRIPTING §6).
const CurrentScriptAPI = "0.21"

// ScriptConfig selects the TypeScript runtime for a profile. Entry is
// resolved relative to the profile file's directory by the daemon; profile
// parsing only validates its shape and supported host API.
type ScriptConfig struct {
	Entry  string
	API    string
	Params map[string]any
	TickHz int
	// Origin is the library ID of the profile whose folder holds Entry, when
	// the script is inherited (PRO-19): the entry is relative to that
	// profile's folder, not to the child's. "" is the profile's own.
	Origin string
}

// Analog contains the profile-wide analog recognizer settings (ANA-02..08).
// Values are resolved to defaults by Parse, so the engine can use them
// without consulting JSON or platform state.
type Analog struct {
	LS   Stick
	RS   Stick
	Dpad Dpad
	LT   Trigger
	RT   Trigger
	// AxisThreshold is the positive magnitude required by half-axis buttons.
	// Zero means use DirectionThreshold.
	AxisThreshold float64
}

// Stick describes one analog stick's direction recognizer.
type Stick struct {
	Mode               string
	Deadzone           float64
	Saturation         float64
	DirectionThreshold float64
	DiagonalWidth      float64
	HysteresisDeg      float64
	DirectionDelayMs   int
	DiagonalFallback   string
	// OuterThreshold is the normalized depth, measured like
	// DirectionThreshold, at which the stick enters its outer ring (ANA-10).
	OuterThreshold float64
	// KnobStepDeg is the rotation that gives one knob tick in mode "knob"
	// (ANA-11).
	KnobStepDeg int
	// KnobTick gives a haptic tick for each knob step that sends a key (HUD-10,
	// ADR-0065). It does nothing without mode "knob", and the file may still
	// set it: the editor keeps a stick's other settings when its mode changes.
	KnobTick bool
	// Curve shapes the push depth that a repeat with MinRateHz follows
	// (ANA-13): CurveLinear (the default), CurveQuadratic or CurveCubic. Empty
	// in a model built by hand means linear. In mode "mouse" it also shapes the
	// pointer speed (MOU-04).
	Curve string
	// MouseSpeed is the pointer speed of the stick in mode "mouse" at full
	// travel, in mouse movement units per second (MOU-04); 0 in a model built by
	// hand means DefaultMouseSpeed.
	MouseSpeed int
	// MouseSmoothingMs is the time constant of the exponential moving average of
	// the pointer's velocity in mode "mouse" (MOU-06); 0 follows the stick at once.
	MouseSmoothingMs int
}

// DefaultMouseSpeed is the MouseSpeed of a stick that sets none: units per
// second at full travel.
const DefaultMouseSpeed = 1200

// Dpad describes conversion of the four physical d-pad buttons.
type Dpad struct {
	Mode string
	Fold string
}

// Trigger keeps the existing trigger-to-button hysteresis configurable while
// retaining the M1 defaults.
type Trigger struct {
	Press   float64
	Release float64
	// FullPress and FullRelease are the hysteresis of the second stage,
	// lt.full and rt.full (ANA-14).
	FullPress   float64
	FullRelease float64
	// Curve is the Stick.Curve of a trigger (ANA-13, ANA-15).
	Curve string
}

// The response curves of a stick or trigger (ANA-13).
const (
	CurveLinear    = "linear"
	CurveQuadratic = "quadratic"
	CurveCubic     = "cubic"
)

// ValidCurve reports whether name is a response curve; the empty string is
// not (a resolved profile always names one).
func ValidCurve(name string) bool {
	return name == CurveLinear || name == CurveQuadratic || name == CurveCubic
}

// DefaultAnalog returns the M4 defaults from ARCHITECTURE §7.6.
func DefaultAnalog() Analog {
	stick := Stick{Mode: "standard", Deadzone: 0.15, Saturation: 0.95,
		DirectionThreshold: 0.30, DiagonalWidth: 45, HysteresisDeg: 8,
		DiagonalFallback: "none", OuterThreshold: 0.90, KnobStepDeg: 30, Curve: CurveLinear,
		MouseSpeed: DefaultMouseSpeed}
	return Analog{
		LS: stick, RS: stick,
		Dpad:          Dpad{Mode: "buttons", Fold: "keepFirst"},
		LT:            Trigger{Press: 0.30, Release: 0.20, FullPress: 0.90, FullRelease: 0.80, Curve: CurveLinear},
		RT:            Trigger{Press: 0.30, Release: 0.20, FullPress: 0.90, FullRelease: 0.80, Curve: CurveLinear},
		AxisThreshold: 0.30,
	}
}

// Feedback configures the controller indications: the pulses and colors of
// layer changes and pause (M3), the haptics of bindings (MAP-25), the knob
// ticks (HUD-10), the player lights (HUD-08) and the low-battery alert (IN-12).
// It holds their times, strength and pause color only. The switches that
// turn them off (rumble, led, pause and lowBattery) are global since
// ADR-0067: they are Config.Feedback, and a profile's feedback object that
// still has them is parsed and ignored.
type Feedback struct {
	ShortMs     int
	LongMs      int
	GapMs       int
	Strength    int
	PausedColor string
}

// DefaultFeedback returns the default M3 feedback settings.
func DefaultFeedback() Feedback {
	return Feedback{
		ShortMs: 70, LongMs: 210, GapMs: 100,
		Strength: 0x7000, PausedColor: "#FFAA00",
	}
}

// Timing is the profile-wide default for M2 gesture and output timing.
type Timing struct {
	HoldMs, MultiTapMs, TapPressMs, TapGapMs     int
	HoldSet, MultiTapSet, TapPressSet, TapGapSet bool
}

// Layer returns the layer called name, or nil.
func (p *Profile) Layer(name string) *Layer {
	for _, l := range p.Layers {
		if l.Name == name {
			return l
		}
	}
	return nil
}

// Layer is the bindings of one layer.
type Layer struct {
	Name     string
	Color    string
	Bindings []*Binding // in control.All() order, at most one per control
}

// Binding returns the binding of control c, or nil.
func (l *Layer) Binding(c control.Control) *Binding {
	for _, b := range l.Bindings {
		if b.Control == c {
			return b
		}
	}
	return nil
}

// Binding is what one control does in a layer.
type Binding struct {
	// ID is the JSON Pointer of the binding object as written, such as
	// "/layers/base/south" or "/layers/base/a" for the alias: a stable name
	// for decisions (ENG-20) and the UI.
	ID        string
	Control   control.Control // resolved from the key, alias or canonical
	Label     string          // MAP-03; "" when absent
	Press     *Action         // nil when absent
	Release   *Action
	Tap       *Action
	DoubleTap *Action
	TripleTap *Action
	QuadTap   *Action
	Hold      *Action
	Timing    *Timing
	Policy    string
	Latch     bool
	Block     bool
	// Haptic is the haptic the controller gives each time a gesture of the
	// binding runs (MAP-25, ADR-0065); 0 for none. It is the binding's, not
	// an action's, so it plays for every gesture, once per dispatch. A block
	// binding runs nothing and plays none.
	Haptic haptic.Preset
}

// Action returns b's action for gesture g, or nil when it has none.
func (b *Binding) Action(g Gesture) *Action {
	switch g {
	case GesturePress:
		return b.Press
	case GestureRelease:
		return b.Release
	case GestureTap:
		return b.Tap
	case GestureDoubleTap:
		return b.DoubleTap
	case GestureTripleTap:
		return b.TripleTap
	case GestureQuadTap:
		return b.QuadTap
	case GestureHold:
		return b.Hold
	}
	return nil
}

// Gesture is a way of using a control that a binding gives an action, its
// property name in the file (ARCHITECTURE §7.2, §10.3). The zero value is
// GestureNone. M1a has only press; M2 adds release, tap, hold and the
// multi-taps.
//
// The engine names gestures with this type too (engine.Gesture is an
// alias): profile owns the names, so both packages share them and profile
// never imports engine.
type Gesture uint8

const (
	// GestureNone is no gesture.
	GestureNone Gesture = iota
	// GesturePress fires when the control goes down.
	GesturePress
	GestureRelease
	GestureTap
	GestureDoubleTap
	GestureTripleTap
	GestureQuadTap
	GestureHold
)

// GestureCount is the number of declared Gesture values, GestureNone
// included: a [GestureCount]T array indexed by Gesture covers them all.
const GestureCount = int(GestureHold) + 1

// String returns the name used in the file, such as "press", "none" for
// GestureNone, or "gesture(N)".
func (g Gesture) String() string {
	switch g {
	case GestureNone:
		return "none"
	case GesturePress:
		return "press"
	case GestureRelease:
		return "release"
	case GestureTap:
		return "tap"
	case GestureDoubleTap:
		return "doubleTap"
	case GestureTripleTap:
		return "tripleTap"
	case GestureQuadTap:
		return "quadTap"
	case GestureHold:
		return "hold"
	}
	return "gesture(" + strconv.Itoa(int(g)) + ")"
}

// Momentary reports whether g runs after the control is released, so that
// there is nothing to hold: release, tap and the multi-taps. Their keys
// actions must use tap mode without repeat, and their layer actions cannot
// hold a layer (ARCHITECTURE §7.7). The engine runs a multi-tap committed
// by its window, or by the press of another control, on a copy of the
// control's state, so a hold it started would never end (invariant 1).
func (g Gesture) Momentary() bool {
	switch g {
	case GestureRelease, GestureTap, GestureDoubleTap, GestureTripleTap, GestureQuadTap:
		return true
	}
	return false
}

// Action is what a gesture does.
type Action struct {
	ID       string // JSON Pointer of the action object, such as "/layers/base/south/press"
	Type     ActionType
	Keys     keys.Chord // for ActionKeys
	Sequence []keys.Chord
	// Steps are the steps of an ActionSequence (MAP-22, ADR-0070): shortcuts to
	// tap, waits and texts to type, in order. ActionText has none: its Text is the
	// one text.
	Steps   []keys.Step
	Text    string // ActionText: the text to type
	Mode    Mode   // for ActionKeys, with the gesture's default applied
	Repeat  *Repeat
	Layer   string
	Layers  []string
	Op      string         // ActionLayer: the layer operation; ActionMouse: MouseOpPrecision or MouseOpJump; ActionHUD: HUDOpHints
	Factor  float64        // ActionMouse precision: the speed factor kept while held
	X, Y    float64        // ActionMouse jump: the position as a fraction of the virtual desktop
	Name    string         // ActionScript: registered action name
	Args    map[string]any // ActionScript: JSON argument values
	Profile string         // ActionProfile: target library ID
	File    string         // ActionMacro: the macro's file name in MacroDir
}

// RepeatStyle selects physical taps or typematic main-key presses.
type RepeatStyle uint8

const (
	RepeatDefault RepeatStyle = iota // follows Mode in directly constructed models
	RepeatTypematic
	RepeatTap
)

func (s RepeatStyle) String() string {
	switch s {
	case RepeatDefault:
		return "default"
	case RepeatTypematic:
		return "typematic"
	case RepeatTap:
		return "tap"
	}
	return "repeat(" + strconv.Itoa(int(s)) + ")"
}

// DefaultRepeatRateHz is the repeat rate of an action that sets none.
const DefaultRepeatRateHz = 12

type Repeat struct {
	Style    RepeatStyle
	DelayMs  int
	DelaySet bool // distinguishes an explicit zero from the default delay
	RateHz   int  // 0 means DefaultRepeatRateHz
	// MinRateHz, when above 0, makes the rate follow how far the control is
	// pushed: MinRateHz at its activation threshold, the rate at full travel
	// (ANA-12). Only controls derived from a stick or trigger may use it
	// (Control.HasDepth), and it must not exceed the rate.
	MinRateHz int
}

// EffectiveRateHz is RateHz, or DefaultRepeatRateHz when it is not set.
func (r Repeat) EffectiveRateHz() int {
	if r.RateHz > 0 {
		return r.RateHz
	}
	return DefaultRepeatRateHz
}

// StyleFor resolves the default style without mutating the model.
func (r Repeat) StyleFor(mode Mode) RepeatStyle {
	if r.Style != RepeatDefault {
		return r.Style
	}
	if mode == ModeTap {
		return RepeatTap
	}
	return RepeatTypematic
}

// String names a as decisions and the dry-run output show it: the mode and
// the shortcut of a keys action, such as "tap Ctrl+Z", or else its type.
func (a Action) String() string {
	if a.Type == ActionKeys {
		if len(a.Sequence) > 1 {
			parts := make([]string, len(a.Sequence))
			for i, c := range a.Sequence {
				parts[i] = c.String()
			}
			return a.Mode.String() + " [" + strings.Join(parts, ",") + "]"
		}
		return a.Mode.String() + " " + a.Keys.String()
	}
	if a.Type == ActionLayer {
		if len(a.Layers) > 0 {
			return "layer " + a.Op + " [" + strings.Join(a.Layers, ",") + "]"
		}
		return "layer " + a.Op + " " + a.Layer
	}
	if a.Type == ActionScript {
		return "script " + a.Name
	}
	if a.Type == ActionProfile {
		return "profile " + a.Profile
	}
	if a.Type == ActionMacro {
		return "macro " + a.File
	}
	if a.Type == ActionMouse {
		if a.Op == MouseOpJump {
			return "mouse jump " + strconv.FormatFloat(a.X, 'g', -1, 64) + "," + strconv.FormatFloat(a.Y, 'g', -1, 64)
		}
		return "mouse " + a.Op + " " + strconv.FormatFloat(a.Factor, 'g', -1, 64)
	}
	if a.Type == ActionHUD {
		return "hud " + a.Op
	}
	if a.Type == ActionSequence {
		return "sequence: " + keys.FormatSteps(a.Steps)
	}
	if a.Type == ActionText {
		return keys.TextStep(a.Text).String()
	}
	return a.Type.String()
}

// ActionType is the kind of an action, its "type" in the file. The zero
// value is invalid.
type ActionType uint8

const (
	// ActionKeys presses a keyboard shortcut.
	ActionKeys ActionType = iota + 1
	// ActionLayer changes the active layer stack.
	ActionLayer
	// ActionScript invokes a named action registered by the profile's script.
	ActionScript
	// ActionProfile requests a switch to a library profile.
	ActionProfile
	// ActionMacro runs a macro, a file in the profile's MacroDir, once
	// (SCR-50, ADR-0055).
	ActionMacro
	// ActionMouse changes how the pointer moves: precision (MOU-05) slows the
	// sticks in mode "mouse" while the gesture is held, jump (MOU-08) puts the
	// pointer somewhere (ADR-0062, section 5).
	ActionMouse
	// ActionHUD controls the overlay: hints (HUD-03) shows the labels of the
	// bindings of the current layer stack while the gesture is held (ADR-0063).
	// It sends no key.
	ActionHUD
	// ActionSequence runs steps one after the other when its gesture fires: taps
	// a shortcut, waits, types a text (MAP-22, ADR-0070). It holds nothing, so it
	// fits every gesture, like a macro.
	ActionSequence
	// ActionText types a text when its gesture fires (OUT-21, ADR-0070). It holds
	// nothing, so it fits every gesture.
	ActionText
)

// HUDOpHints is the operation of the HUD action that shows the key hints.
const HUDOpHints = "hints"

// The operations of a mouse action (Action.Op).
const (
	MouseOpPrecision = "precision"
	MouseOpJump      = "jump"
)

// DefaultPrecisionFactor is the Factor of a precision action that sets none.
const DefaultPrecisionFactor = 0.3

// The range of the Factor of a precision action.
const (
	MinPrecisionFactor = 0.05
	MaxPrecisionFactor = 1.0
)

// Holds reports whether g keeps its action for as long as the control is
// held: the press gesture, which lasts until the release, and the hold gesture,
// which lasts from the moment it is recognized until the release. The other
// gestures run after the control is released (Momentary) and there is nothing
// to hold.
func (g Gesture) Holds() bool { return g == GesturePress || g == GestureHold }

// PrecisionGestureMessage is the error of a mouse precision action on a gesture
// that does not last while the control is held. engine.Compile says the same.
func PrecisionGestureMessage(g Gesture) string {
	return "a precision action slows the pointer while the control is held: use it on press or hold, not on " + g.String()
}

// HintsGestureMessage is the error of a HUD hints action on a gesture that does
// not last while the control is held. engine.Compile says the same.
func HintsGestureMessage(g Gesture) string {
	return "a hints action shows the key hints while the control is held: use it on press or hold, not on " + g.String()
}

// String returns the name used in the file, or "actiontype(N)".
func (t ActionType) String() string {
	switch t {
	case ActionKeys:
		return "keys"
	case ActionLayer:
		return "layer"
	case ActionScript:
		return "script"
	case ActionProfile:
		return "profile"
	case ActionMacro:
		return "macro"
	case ActionMouse:
		return "mouse"
	case ActionHUD:
		return "hud"
	case ActionSequence:
		return "sequence"
	case ActionText:
		return "text"
	}
	return "actiontype(" + strconv.Itoa(int(t)) + ")"
}

// Mode says how a keys action uses its shortcut. The zero value is
// invalid.
type Mode uint8

const (
	// ModeHold keeps the shortcut down while the control is held (MAP-02).
	ModeHold Mode = iota + 1
	// ModeTap taps the shortcut once when the gesture fires.
	ModeTap
)

// String returns the name used in the file, or "mode(N)".
func (m Mode) String() string {
	switch m {
	case ModeHold:
		return "hold"
	case ModeTap:
		return "tap"
	}
	return "mode(" + strconv.Itoa(int(m)) + ")"
}

// parseMode is the inverse of Mode.String for valid modes.
func parseMode(s string) (Mode, bool) {
	switch s {
	case "hold":
		return ModeHold, true
	case "tap":
		return ModeTap, true
	}
	return 0, false
}
