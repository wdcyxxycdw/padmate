package engine

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Program is a compiled profile. It is immutable, so engines on any
// number of goroutines can share it.
type Program struct {
	prof           *profile.Profile
	base           *profile.Layer
	common         *profile.Layer
	layers         map[string]*profile.Layer
	defaultLayer   string
	byCtl          [control.Count]*profile.Binding // base layer bindings, nil where unbound
	bindings       []BindingInfo
	timing         profile.Timing
	analog         profile.Analog
	analogResolved bool
	// haptics is what the profile's feedback says about haptics, computed once
	// (haptic.go).
	haptics hapticSettings

	// Trigger thresholds in raw axis units.
	pressRaw, releaseRaw               int16
	triggerPressRaw, triggerReleaseRaw [2]int16
	// Second stage of the triggers (ANA-14).
	triggerFullPressRaw, triggerFullReleaseRaw [2]int16
	// Response curves (ANA-13), indexed by curveLS, curveRS, curveLT, curveRT.
	curves [4]curveKind

	// fnSets are the sets of the function keys (MAP-27): back's, then start's;
	// nil for a key without one.
	fnSets [2]*profile.Layer
}

// BindingInfo describes one binding of a Program (PRO-04).
type BindingInfo struct {
	Layer   string
	Binding *profile.Binding
	// Gesture is the gesture AddedLatency is for: GesturePress in M1a, or
	// GestureNone for a binding without an action.
	Gesture Gesture
	// AddedLatency is how long the engine waits after the press of the
	// binding's control before it acts on it (MAP-08). It is 0 for a
	// control with only press gestures, which is every control in M1a.
	AddedLatency time.Duration
}

// Compile turns a validated profile into a Program. The Program keeps
// pointers into p, which must not change afterwards. The error is only for
// a nil or inconsistent profile: Compile accepts everything profile.Parse
// returns.
func Compile(p *profile.Profile) (*Program, error) {
	if p == nil {
		return nil, errors.New("engine: no profile")
	}
	analog := p.Analog
	if !p.AnalogResolved {
		defaults := profile.DefaultAnalog()
		fillStick := func(dst *profile.Stick, src profile.Stick) {
			if dst.Mode == "" {
				dst.Mode = src.Mode
			}
			if dst.Deadzone == 0 {
				dst.Deadzone = src.Deadzone
			}
			if dst.Saturation == 0 {
				dst.Saturation = src.Saturation
			}
			if dst.DirectionThreshold == 0 {
				dst.DirectionThreshold = src.DirectionThreshold
			}
			if dst.DiagonalWidth == 0 {
				dst.DiagonalWidth = src.DiagonalWidth
			}
			if dst.HysteresisDeg == 0 {
				dst.HysteresisDeg = src.HysteresisDeg
			}
			if dst.DiagonalFallback == "" {
				dst.DiagonalFallback = src.DiagonalFallback
			}
			if dst.OuterThreshold == 0 {
				dst.OuterThreshold = src.OuterThreshold
			}
		}
		fillStick(&analog.LS, defaults.LS)
		fillStick(&analog.RS, defaults.RS)
		if analog.Dpad.Mode == "" {
			analog.Dpad.Mode = defaults.Dpad.Mode
		}
		if analog.Dpad.Fold == "" {
			analog.Dpad.Fold = defaults.Dpad.Fold
		}
		if analog.LT.Press == 0 {
			analog.LT.Press = defaults.LT.Press
		}
		if analog.LT.Release == 0 {
			analog.LT.Release = defaults.LT.Release
		}
		if analog.RT.Press == 0 {
			analog.RT.Press = defaults.RT.Press
		}
		if analog.RT.Release == 0 {
			analog.RT.Release = defaults.RT.Release
		}
		if analog.LT.FullRelease == 0 {
			analog.LT.FullRelease = defaults.LT.FullRelease
		}
		if analog.RT.FullRelease == 0 {
			analog.RT.FullRelease = defaults.RT.FullRelease
		}
		if analog.AxisThreshold == 0 {
			analog.AxisThreshold = defaults.AxisThreshold
		}
	}
	// A setting written in a file is never zero where zero is not allowed:
	// knobStepDeg is at least 10 and fullPress is above press. Zero there
	// means a model built by hand that did not set it.
	if analog.LS.KnobStepDeg <= 0 {
		analog.LS.KnobStepDeg = DefaultKnobStepDeg
	}
	if analog.RS.KnobStepDeg <= 0 {
		analog.RS.KnobStepDeg = DefaultKnobStepDeg
	}
	// mouseSpeed is at least 50 in a file: zero is a model built by hand.
	if analog.LS.MouseSpeed <= 0 {
		analog.LS.MouseSpeed = profile.DefaultMouseSpeed
	}
	if analog.RS.MouseSpeed <= 0 {
		analog.RS.MouseSpeed = profile.DefaultMouseSpeed
	}
	if analog.LT.FullPress == 0 {
		analog.LT.FullPress, analog.LT.FullRelease = TriggerFullPress, TriggerFullRelease
	}
	if analog.RT.FullPress == 0 {
		analog.RT.FullPress, analog.RT.FullRelease = TriggerFullPress, TriggerFullRelease
	}
	// A curve that is not set is linear.
	for _, c := range []struct {
		name string
		set  *string
	}{{"ls", &analog.LS.Curve}, {"rs", &analog.RS.Curve}, {"lt", &analog.LT.Curve}, {"rt", &analog.RT.Curve}} {
		if *c.set == "" {
			*c.set = profile.CurveLinear
		}
		if _, ok := parseCurve(*c.set); !ok {
			return nil, fmt.Errorf("engine: analog %s curve %q is unknown", c.name, *c.set)
		}
	}
	if analog.LS.Saturation <= analog.LS.Deadzone {
		return nil, errors.New("engine: analog ls saturation must be greater than deadzone")
	}
	if analog.RS.Saturation <= analog.RS.Deadzone {
		return nil, errors.New("engine: analog rs saturation must be greater than deadzone")
	}
	prog := &Program{
		prof:           p,
		pressRaw:       rawThreshold(TriggerPress),
		releaseRaw:     rawThreshold(TriggerRelease),
		analog:         analog,
		analogResolved: p.AnalogResolved,
		haptics:        hapticSettingsOf(p),
	}
	prog.curves[curveLS], _ = parseCurve(analog.LS.Curve)
	prog.curves[curveRS], _ = parseCurve(analog.RS.Curve)
	prog.curves[curveLT], _ = parseCurve(analog.LT.Curve)
	prog.curves[curveRT], _ = parseCurve(analog.RT.Curve)
	prog.triggerPressRaw = [2]int16{rawThreshold(analog.LT.Press), rawThreshold(analog.RT.Press)}
	prog.triggerReleaseRaw = [2]int16{rawThreshold(analog.LT.Release), rawThreshold(analog.RT.Release)}
	prog.triggerFullPressRaw = [2]int16{rawThreshold(analog.LT.FullPress), rawThreshold(analog.RT.FullPress)}
	prog.triggerFullReleaseRaw = [2]int16{rawThreshold(analog.LT.FullRelease), rawThreshold(analog.RT.FullRelease)}
	prog.timing = p.Timing
	if prog.timing.HoldMs == 0 && !prog.timing.HoldSet {
		prog.timing.HoldMs = int(DefaultHold / time.Millisecond)
	}
	if prog.timing.MultiTapMs == 0 && !prog.timing.MultiTapSet {
		prog.timing.MultiTapMs = int(DefaultMultiTap / time.Millisecond)
	}
	if prog.timing.TapPressMs == 0 && !prog.timing.TapPressSet {
		prog.timing.TapPressMs = int(DefaultTapPress / time.Millisecond)
	}
	if prog.timing.TapGapMs == 0 && !prog.timing.TapGapSet {
		prog.timing.TapGapMs = int(DefaultTapGap / time.Millisecond)
	}
	prog.layers = make(map[string]*profile.Layer, len(p.Layers))
	prog.common = p.Common
	prog.defaultLayer = p.DefaultLayer
	if prog.defaultLayer == "" {
		prog.defaultLayer = "base"
	}
	for i, l := range p.Layers {
		switch {
		case l == nil:
			return nil, fmt.Errorf("engine: layer %d is nil", i)
		case prog.layers[l.Name] != nil:
			return nil, fmt.Errorf("engine: two layers are called %q", l.Name)
		}
		prog.layers[l.Name] = l
		if l.Name == "base" {
			prog.base = l
		}
	}
	if prog.base == nil {
		return nil, errors.New("engine: the profile has no base layer")
	}
	if prog.layers[prog.defaultLayer] == nil {
		return nil, fmt.Errorf("engine: default layer %q does not exist", prog.defaultLayer)
	}
	// back and start are the function keys in every profile: a binding on
	// them in a layer or in common is ignored (ADR-0069), and Compile does not
	// even check it. profile.Parse drops them and warns; a model built by hand
	// may still have them.
	for _, l := range p.Layers {
		for i, b := range l.Bindings {
			if b != nil && b.Control.IsFunctionKey() {
				continue
			}
			if err := prog.checkBinding(l, i, b); err != nil {
				return nil, err
			}
		}
	}
	if p.Common != nil {
		for i, b := range p.Common.Bindings {
			if b != nil && b.Control.IsFunctionKey() {
				continue
			}
			if err := prog.checkBinding(p.Common, i, b); err != nil {
				return nil, err
			}
		}
	}
	if p.FunctionKeys != nil {
		for i, set := range [2]*profile.Layer{p.FunctionKeys.Back, p.FunctionKeys.Start} {
			if set == nil {
				continue
			}
			fn := [2]control.Control{control.Back, control.Start}[i]
			for j, b := range set.Bindings {
				if b != nil && b.Control.IsFunctionKey() {
					return nil, fmt.Errorf("engine: binding %s: %s is a function key and cannot be bound in the set of %s", b.ID, b.Control, fn)
				}
				if err := prog.checkBinding(set, j, b); err != nil {
					return nil, err
				}
			}
			prog.fnSets[i] = set
		}
	}
	for _, l := range p.Layers {
		prog.addBindingInfo(l, l.Name)
	}
	if p.Common != nil {
		prog.addBindingInfo(p.Common, p.Common.Name)
	}
	for i, fn := range [2]control.Control{control.Back, control.Start} {
		if prog.fnSets[i] != nil {
			prog.addBindingInfo(prog.fnSets[i], profile.FunctionLevel(fn))
		}
	}
	return prog, nil
}

// addBindingInfo adds the bindings of layer l to the list of Bindings, as
// layer name: the name of a function key's level, not its Layer's.
func (p *Program) addBindingInfo(l *profile.Layer, name string) {
	bindings := slices.Clone(l.Bindings)
	slices.SortFunc(bindings, func(a, b *profile.Binding) int { return int(a.Control) - int(b.Control) })
	for _, b := range bindings {
		if b.Control.IsFunctionKey() {
			continue // ignored (Compile)
		}
		gs := gesturesOf(b)
		g := GestureNone
		for _, candidate := range []Gesture{GesturePress, GestureRelease, GestureTap, GestureDoubleTap, GestureTripleTap, GestureQuadTap, GestureHold} {
			if gs.has(candidate) {
				g = candidate
				break
			}
		}
		p.bindings = append(p.bindings, BindingInfo{Layer: name, Binding: b, Gesture: g, AddedLatency: p.addedLatency(gs, b.Timing)})
	}
}

// checkBinding checks binding i of layer l and, in the base layer, adds it
// to byCtl.
func (p *Program) checkBinding(l *profile.Layer, i int, b *profile.Binding) error {
	if b == nil {
		return fmt.Errorf("engine: layer %q: binding %d is nil", l.Name, i)
	}
	if !b.Control.IsValid() {
		return fmt.Errorf("engine: binding %s: invalid control %s", b.ID, b.Control)
	}
	seen := l.Binding(b.Control)
	if seen != nil && seen != b {
		return fmt.Errorf("engine: binding %s: %s is already bound by %s", b.ID, b.Control, seen.ID)
	}
	if err := checkActions(b); err != nil {
		return err
	}
	if l.Name == "base" {
		p.byCtl[b.Control] = b
	}
	return nil
}

// CheckBinding checks b by the rules Compile applies to each binding on its
// own: a valid control, and the action rules of checkAction for every
// gesture (ARCHITECTURE §7.7). A Level of Options.Registry hands the engine
// bindings that never pass through Compile; the engine trusts them, and a
// momentary gesture that holds keys or a layer would leave them held
// (invariant 1), so a Level checks each binding with CheckBinding before it
// returns it. CheckBinding does not check that the layers a layer action
// names exist: that depends on the Registry.
func CheckBinding(b *profile.Binding) error {
	if b == nil {
		return errors.New("engine: binding is nil")
	}
	if !b.Control.IsValid() {
		return fmt.Errorf("engine: binding %s: invalid control %s", b.ID, b.Control)
	}
	return checkActions(b)
}

// checkActions checks the action of each gesture of b with checkAction.
func checkActions(b *profile.Binding) error {
	if b.Haptic != 0 && !b.Haptic.Valid() {
		return fmt.Errorf("engine: binding %s: unknown haptic preset %s", b.ID, b.Haptic)
	}
	for g := GesturePress; g <= GestureHold; g++ {
		if err := checkAction(b.Control, g, b.Action(g)); err != nil {
			return err
		}
	}
	return nil
}

// checkAction checks a, the action of gesture g of control c, or nothing when
// a is nil.
// The rules are profile.Parse's (ARCHITECTURE §7.7,
// TestParseAndCompileAgree): a gesture that runs after the control is
// released (Gesture.Momentary) holds neither keys nor a layer and does not
// repeat, since commitMulti runs its action on a copy of the control's
// state and nothing would end the hold (invariant 1); a key sequence uses
// tap mode and does not repeat; a mouse button does not repeat with typematic
// repeat (profile.MouseButtonRepeatMessage); a mouse precision action and a
// HUD hints action last while the control is held, so they need a press or hold
// gesture (profile.PrecisionGestureMessage, profile.HintsGestureMessage);
// repeat's style follows its mode;
// a minRateHz (ANA-12) is on a control that has a depth
// (control.Control.HasDepth) and does not exceed the rate.
func checkAction(c control.Control, g Gesture, a *profile.Action) error {
	if a == nil {
		return nil
	}
	if a.Type == profile.ActionProfile {
		if !profile.ValidLibraryID(a.Profile) {
			return fmt.Errorf("engine: action %s: invalid profile library id", a.ID)
		}
		return nil
	}
	if a.Type == profile.ActionScript {
		if a.Name == "" {
			return fmt.Errorf("engine: action %s: script action needs a name", a.ID)
		}
		return nil
	}
	if a.Type == profile.ActionMacro {
		if err := profile.CheckMacroFile(a.File); err != nil {
			return fmt.Errorf("engine: action %s: %w", a.ID, err)
		}
		return nil
	}
	if a.Type == profile.ActionSequence {
		// A sequence taps, waits and types; it holds nothing, so it fits every
		// gesture (MAP-22). Its steps pass the rules Parse applies.
		if err := keys.ValidateSteps(a.Steps); err != nil {
			return fmt.Errorf("engine: action %s: %w", a.ID, err)
		}
		return nil
	}
	if a.Type == profile.ActionText {
		if err := keys.ValidateText(a.Text); err != nil {
			return fmt.Errorf("engine: action %s: %w", a.ID, err)
		}
		return nil
	}
	if a.Type == profile.ActionMouse {
		switch {
		case a.Op == profile.MouseOpPrecision && !g.Holds():
			return fmt.Errorf("engine: action %s: %s", a.ID, profile.PrecisionGestureMessage(g))
		case a.Op == profile.MouseOpPrecision && (a.Factor < profile.MinPrecisionFactor || a.Factor > profile.MaxPrecisionFactor):
			return fmt.Errorf("engine: action %s: precision factor %v is outside %v to %v", a.ID, a.Factor, profile.MinPrecisionFactor, profile.MaxPrecisionFactor)
		case a.Op == profile.MouseOpJump && (a.X < 0 || a.X > 1 || a.Y < 0 || a.Y > 1):
			return fmt.Errorf("engine: action %s: jump position %v,%v is outside the desktop (0 to 1)", a.ID, a.X, a.Y)
		case a.Op != profile.MouseOpPrecision && a.Op != profile.MouseOpJump:
			return fmt.Errorf("engine: action %s: unknown mouse operation %q", a.ID, a.Op)
		}
		return nil
	}
	if a.Type == profile.ActionHUD {
		switch {
		case a.Op != profile.HUDOpHints:
			return fmt.Errorf("engine: action %s: unknown hud operation %q", a.ID, a.Op)
		case !g.Holds():
			return fmt.Errorf("engine: action %s: %s", a.ID, profile.HintsGestureMessage(g))
		}
		return nil
	}
	if a.Type == profile.ActionLayer {
		switch {
		case a.Op == "cycle" && len(a.Layers) == 0:
			return fmt.Errorf("engine: action %s: cycle needs layers", a.ID)
		case a.Op != "cycle" && a.Layer == "":
			return fmt.Errorf("engine: action %s: layer action needs a layer", a.ID)
		case a.Op == "hold" && g.Momentary():
			return fmt.Errorf("engine: action %s: layer hold action is only valid for press/hold", a.ID)
		}
		return nil
	}
	switch {
	case a.Type != profile.ActionKeys:
		return fmt.Errorf("engine: action %s: unsupported type %s", a.ID, a.Type)
	case !a.Keys.IsValid():
		return fmt.Errorf("engine: action %s: no keys", a.ID)
	case a.Mode != profile.ModeHold && a.Mode != profile.ModeTap:
		return fmt.Errorf("engine: action %s: invalid mode %s", a.ID, a.Mode)
	case g.Momentary() && a.Mode == profile.ModeHold:
		return fmt.Errorf("engine: action %s: %s runs after the control is released and needs tap mode", a.ID, g)
	case g.Momentary() && a.Repeat != nil:
		return fmt.Errorf("engine: action %s: repeat needs a press or hold gesture", a.ID)
	case len(a.Sequence) > 0 && a.Mode != profile.ModeTap:
		return fmt.Errorf("engine: action %s: key sequences need tap mode", a.ID)
	case len(a.Sequence) > 0 && a.Repeat != nil:
		return fmt.Errorf("engine: action %s: key sequences cannot repeat", a.ID)
	case a.Repeat != nil && a.Repeat.Style > profile.RepeatTap:
		return fmt.Errorf("engine: action %s: invalid repeat style", a.ID)
	case a.Repeat != nil && a.Repeat.StyleFor(a.Mode) == profile.RepeatTypematic && a.Mode != profile.ModeHold:
		return fmt.Errorf("engine: action %s: typematic repeat requires hold mode", a.ID)
	case a.Repeat != nil && a.Repeat.StyleFor(a.Mode) == profile.RepeatTypematic && a.Keys.Key().IsMouseButton():
		return fmt.Errorf("engine: action %s: %s", a.ID, profile.MouseButtonRepeatMessage)
	case a.Repeat != nil && a.Repeat.StyleFor(a.Mode) == profile.RepeatTap && a.Mode != profile.ModeTap:
		return fmt.Errorf("engine: action %s: tap repeat requires tap mode", a.ID)
	case a.Repeat != nil && a.Repeat.MinRateHz > 0 && !c.HasDepth():
		return fmt.Errorf("engine: action %s: minRateHz needs a stick, trigger or half-axis control", a.ID)
	case a.Repeat != nil && a.Repeat.MinRateHz > a.Repeat.EffectiveRateHz():
		return fmt.Errorf("engine: action %s: minRateHz must not exceed rateHz", a.ID)
	}
	return nil
}

func (p *Program) Timing() profile.Timing { return p.timing }

// Analog returns the resolved analog recognizer settings.
func (p *Program) Analog() profile.Analog { return p.analog }

func (p *Program) timingFor(b *profile.Binding) profile.Timing {
	t := p.timing
	if b != nil && b.Timing != nil {
		if b.Timing.HoldSet || b.Timing.HoldMs > 0 {
			t.HoldMs = b.Timing.HoldMs
		}
		if b.Timing.MultiTapSet || b.Timing.MultiTapMs > 0 {
			t.MultiTapMs = b.Timing.MultiTapMs
		}
	}
	return t
}

// Profile returns the profile p was compiled from.
func (p *Program) Profile() *profile.Profile { return p.prof }

// Bindings returns every binding of p, in layer order and then in
// control.All() order, the sets of the function keys (Layer
// profile.FunctionLevel) after common. The caller owns the slice.
func (p *Program) Bindings() []BindingInfo { return slices.Clone(p.bindings) }

// layer returns the layer called name, or nil. Engine.lookup walks the
// active levels with it (ENG-21).
func (p *Program) layer(name string) *profile.Layer { return p.layers[name] }

// gestures returns the gestures of control c that have an action.
func (p *Program) gestures(c control.Control) gestureSet {
	if b := p.base.Binding(c); b != nil {
		return gesturesOf(b)
	}
	return 0
}

func gesturesOf(b *profile.Binding) gestureSet {
	var gs gestureSet
	for g := range Gesture(profile.GestureCount) {
		if b.Action(g) != nil {
			gs = gs.with(g)
		}
	}
	return gs
}

// gestureSet is a set of gestures, one bit per Gesture.
type gestureSet uint32

func (s gestureSet) with(g Gesture) gestureSet { return s | 1<<g }
func (s gestureSet) has(g Gesture) bool        { return s&(1<<g) != 0 }

// addedLatency is how long the engine waits after the press of a control
// whose gestures are gs before it acts (MAP-08, ARCHITECTURE §7.2). A
// control with only immediate gestures never waits, and every M1a gesture
// is immediate. M2 adds the ones that wait here: tap together with hold
// waits holdMs to tell them apart, and a multi-tap adds multiTapMs to wait
// for the next tap. Until then another gesture is a bug: it panics.
func addedLatency(gs gestureSet) time.Duration {
	known := gestureSet(1<<profile.GestureCount) - 1
	if gs&^known != 0 {
		panic("engine: unknown gesture set " + strconv.FormatUint(uint64(gs), 2))
	}
	if gs.has(GestureHold) && (gs.has(GestureTap) || gs.has(GestureDoubleTap) || gs.has(GestureTripleTap) || gs.has(GestureQuadTap)) {
		return DefaultHold
	}
	if gs.has(GestureDoubleTap) || gs.has(GestureTripleTap) || gs.has(GestureQuadTap) {
		return DefaultMultiTap
	}
	return 0
}

func (p *Program) addedLatency(gs gestureSet, override *profile.Timing) time.Duration {
	d := addedLatency(gs)
	if d == 0 {
		return 0
	}
	t := p.timing
	if override != nil {
		if override.HoldSet || override.HoldMs > 0 {
			t.HoldMs = override.HoldMs
		}
		if override.MultiTapSet || override.MultiTapMs > 0 {
			t.MultiTapMs = override.MultiTapMs
		}
	}
	if gs.has(GestureHold) && (gs.has(GestureTap) || gs.has(GestureDoubleTap) || gs.has(GestureTripleTap) || gs.has(GestureQuadTap)) {
		return time.Duration(t.HoldMs) * time.Millisecond
	}
	return time.Duration(t.MultiTapMs) * time.Millisecond
}

// rawThreshold converts a fraction of a trigger's travel to raw axis units.
func rawThreshold(f float64) int16 { return int16(math.Round(f * math.MaxInt16)) }
