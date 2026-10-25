package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// Parse checks a profile in three stages, syntax, schema and semantics,
// and returns the resolved profile with any warnings, or nil and the
// errors. A stage runs only if the previous ones found no errors; warnings
// do not stop it. Diagnostics are in file order. Parse never panics.
//
// A document of an older schemaVersion is migrated in memory first (Migrate,
// PRO-06), so every version this build reads parses the same way and the
// resolved Profile has SchemaVersion CurrentSchema. A newer version is
// refused with the message of Migrate. A schemaVersion that is not a
// non-negative integer is left to the schema stage to describe. The
// migrations keep the length of the document, so diagnostics point at the
// positions of data.
func Parse(data []byte) (*Profile, Diagnostics) {
	r, ds := Resolve(data, nil)
	if r == nil {
		return nil, ds
	}
	return r.Profile, ds
}

// open reads data as a profile document up to its schema: syntax, version
// migration and schema, the stages that need nothing but the document. The
// diagnostics of a failure are placed in data. The doc has no id yet.
func open(data []byte) (*doc, Diagnostics) {
	root, serr := scan(data)
	if serr != nil {
		return nil, place(data, Diagnostics{{Severity: SeverityError, Offset: serr.off, Message: serr.msg}})
	}
	current := data
	if v, ok := rootVersion(root); ok && v != CurrentSchema {
		var ds Diagnostics
		if current, ds = Migrate(data); ds.HasErrors() {
			return nil, ds
		}
		if root, serr = scan(current); serr != nil { // Migrate checked it
			return nil, Diagnostics{internalError(errors.New(serr.msg))}
		}
	}
	if ds := validate(root); ds.HasErrors() {
		return nil, place(data, ds)
	}
	return &doc{orig: data, data: current, root: root}, nil
}

// Load reads and parses the profile at path. The error is only for
// reading the file; problems in the profile are diagnostics.
func Load(path string) (*Profile, Diagnostics, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	p, ds := Parse(data)
	return p, ds, nil
}

// ReadError formats err, an error from reading the file at path, such as
// the error of Load, as "path: reason", without the operation that
// *os.PathError adds, such as "p.json: The system cannot find the file
// specified.". Commands print it for the profile and for other input
// files.
func ReadError(path string, err error) string {
	// Windows reports reading a directory as "Incorrect function."
	if fi, serr := os.Stat(path); serr == nil && fi.IsDir() {
		return path + ": is a directory"
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	return path + ": " + err.Error()
}

// resolver builds the Profile from a document that passed the schema and
// runs the semantic checks.
type resolver struct {
	data []byte
	root *node
	ds   Diagnostics
	// where formats the position of an offset for a message; nil counts in
	// data. A merged profile (Resolve) says where the text came from.
	where func(off int) string
}

func (r *resolver) errorAt(off int, ptr, msg string) {
	r.ds = append(r.ds, Diagnostic{Severity: SeverityError, Pointer: ptr, Offset: off, Message: msg})
}

func (r *resolver) warnAt(off int, ptr, msg string) {
	r.ds = append(r.ds, Diagnostic{Severity: SeverityWarning, Pointer: ptr, Offset: off, Message: msg})
}

// checks run on the resolved profile, after every name is resolved. Later
// milestones add theirs here: layer references (M3) and so on. The rules of
// one action, such as the mode of a key sequence, are in action.
var checks = []func(r *resolver, p *Profile){
	checkNoAction,
	checkLayers,
	checkAnalog,
	checkScriptActions,
}

func resolve(data []byte, root *node, where func(off int) string) (*Profile, Diagnostics) {
	var w wireProfile
	if ds := decodeWire(data, root, &w); len(ds) > 0 {
		return nil, ds
	}
	r := &resolver{data: data, root: root, where: where}
	// The schema's const accepts 1.0 and 1e0 too.
	if f, err := w.SchemaVersion.Float64(); err != nil || f != CurrentSchema {
		return nil, Diagnostics{internalError(fmt.Errorf("schemaVersion %s passed the schema", w.SchemaVersion))}
	}
	p := &Profile{SchemaVersion: CurrentSchema, Name: w.Name, Timing: resolveTiming(w.Timing), Analog: resolveAnalog(w.Analog), AnalogResolved: true}
	p.ControlNames = w.ControlNames
	for name, label := range w.ControlNames {
		c, ok := control.Parse(name)
		ptr := childPointer("/controlNames", name)
		if !ok || c.String() != name {
			r.errorAt(root.members["controlNames"].members[name].start, ptr, "control name must use a canonical control ID")
		} else if strings.TrimSpace(label) == "" {
			r.errorAt(root.members["controlNames"].members[name].start, ptr, "control display name must not be blank")
		}
	}
	f := DefaultFeedback()
	if w.Feedback != nil {
		if w.Feedback.ShortMs != nil {
			f.ShortMs = *w.Feedback.ShortMs
		}
		if w.Feedback.LongMs != nil {
			f.LongMs = *w.Feedback.LongMs
		}
		if w.Feedback.GapMs != nil {
			f.GapMs = *w.Feedback.GapMs
		}
		if w.Feedback.Strength != nil {
			f.Strength = *w.Feedback.Strength
		}
		if w.Feedback.PausedColor != nil {
			f.PausedColor = *w.Feedback.PausedColor
		}
	}
	p.Feedback = &f
	if w.Script != nil {
		p.Script = &ScriptConfig{Entry: w.Script.Entry, API: w.Script.API, Params: w.Script.Params}
		if w.Script.API != CurrentScriptAPI {
			// The schema takes only the current API and the previous one.
			off, ptr := memberAt("/script", root.members["script"], "api")
			r.warnAt(off, ptr, "script API "+w.Script.API+" is the previous version; this release's is "+CurrentScriptAPI+": change script.api to "+CurrentScriptAPI)
		}
		if InMacroDir(w.Script.Entry) {
			off, ptr := memberAt("/script", root.members["script"], "entry")
			r.errorAt(off, ptr, "script entry must not be in the "+MacroDir+" folder: each file there is a macro")
		}
		p.Script.TickHz = 125
		if w.Script.TickHz != nil {
			p.Script.TickHz = *w.Script.TickHz
		}
	}
	p.DefaultLayer = w.DefaultLayer
	if p.DefaultLayer == "" {
		p.DefaultLayer = "base"
	}
	names := make([]string, 0, len(w.Layers))
	for name := range w.Layers {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		p.Layers = append(p.Layers, r.layer(name, w.Layers[name]))
	}
	if w.Common != nil {
		p.Common = r.layerAt("common", []string{"common"}, *w.Common)
	}
	if w.FunctionKeys != nil {
		p.FunctionKeys = &FunctionKeys{}
		if w.FunctionKeys.Back != nil {
			p.FunctionKeys.Back = r.layerAt(FunctionLevel(control.Back), []string{"functionKeys", "back"}, *w.FunctionKeys.Back)
		}
		if w.FunctionKeys.Start != nil {
			p.FunctionKeys.Start = r.layerAt(FunctionLevel(control.Start), []string{"functionKeys", "start"}, *w.FunctionKeys.Start)
		}
	}
	for _, check := range checks {
		check(r, p)
	}
	return p, r.ds
}

func resolveAnalog(w *wireAnalog) Analog {
	a := DefaultAnalog()
	if w == nil {
		return a
	}
	applyStick := func(dst *Stick, src *wireStick) {
		if src == nil {
			return
		}
		if src.Mode != nil {
			dst.Mode = *src.Mode
		}
		if src.Deadzone != nil {
			dst.Deadzone = *src.Deadzone
		}
		if src.Saturation != nil {
			dst.Saturation = *src.Saturation
		}
		if src.DirectionThreshold != nil {
			dst.DirectionThreshold = *src.DirectionThreshold
		}
		if src.DiagonalWidth != nil {
			dst.DiagonalWidth = *src.DiagonalWidth
		}
		if src.HysteresisDeg != nil {
			dst.HysteresisDeg = *src.HysteresisDeg
		}
		if src.DirectionDelayMs != nil {
			dst.DirectionDelayMs = *src.DirectionDelayMs
		}
		if src.DiagonalFallback != nil {
			dst.DiagonalFallback = *src.DiagonalFallback
		}
		if src.OuterThreshold != nil {
			dst.OuterThreshold = *src.OuterThreshold
		}
		if src.KnobStepDeg != nil {
			dst.KnobStepDeg = *src.KnobStepDeg
		}
		if src.KnobTick != nil {
			dst.KnobTick = *src.KnobTick
		}
		if src.Curve != nil {
			dst.Curve = *src.Curve
		}
		if src.MouseSpeed != nil {
			dst.MouseSpeed = *src.MouseSpeed
		}
		if src.MouseSmoothingMs != nil {
			dst.MouseSmoothingMs = *src.MouseSmoothingMs
		}
	}
	applyStick(&a.LS, w.LS)
	applyStick(&a.RS, w.RS)
	if w.Dpad != nil {
		if w.Dpad.Mode != nil {
			a.Dpad.Mode = *w.Dpad.Mode
		}
		if w.Dpad.Fold != nil {
			a.Dpad.Fold = *w.Dpad.Fold
		}
	}
	applyTrigger := func(dst *Trigger, src *wireTrigger) {
		if src == nil {
			return
		}
		if src.Press != nil {
			dst.Press = *src.Press
		}
		if src.Release != nil {
			dst.Release = *src.Release
		}
		if src.FullPress != nil {
			dst.FullPress = *src.FullPress
		}
		if src.FullRelease != nil {
			dst.FullRelease = *src.FullRelease
		}
		if src.Curve != nil {
			dst.Curve = *src.Curve
		}
	}
	applyTrigger(&a.LT, w.LT)
	applyTrigger(&a.RT, w.RT)
	if w.AxisThreshold != nil {
		a.AxisThreshold = *w.AxisThreshold
	}
	return a
}

// layer resolves the bindings of layer name, reporting controls bound
// twice under different names.
func (r *resolver) layer(name string, w wireLayer) *Layer {
	return r.layerAt(name, []string{"layers", name}, w)
}

func (r *resolver) layerAt(name string, parts []string, w wireLayer) *Layer {
	ptr := pointer(parts)
	l := &Layer{Name: name}
	if w.Color != nil {
		l.Color = *w.Color
	}
	n := r.root.lookupPointer(ptr)
	if n == nil {
		return l // not reached: the schema requires the layer
	}
	first := map[control.Control]*node{}
	for _, m := range n.order {
		if m.name == "color" {
			continue
		}
		c, ok := control.Parse(m.name)
		if !ok {
			r.errorAt(m.key, childPointer(ptr, m.name), "unknown control "+quote(m.name)) // not reached: the schema lists the controls
			continue
		}
		if f := first[c]; f != nil {
			// The two names differ (equal keys are a syntax error), so one
			// of them is the alias.
			alias := m.name
			if alias == c.String() {
				alias = f.name
			}
			r.errorAt(m.key, childPointer(ptr, m.name),
				fmt.Sprintf("%s is already bound as %s at %s (%s is an alias of %s)",
					quote(m.name), quote(f.name), r.at(f.key), quote(alias), quote(c.String())))
			continue
		}
		first[c] = m
		if c.IsFunctionKey() {
			// Back and start are the function keys in every profile
			// (ADR-0069): a binding on them, such as an old profile's,
			// loads as nothing. The schema keeps them out of the function
			// keys' own sets, so this is a layer or common.
			msg := c.String() + " is a function key (ADR-0069): this binding has no effect"
			if m.name != c.String() {
				msg = m.name + " is an alias of " + c.String() + ", a function key (ADR-0069): this binding has no effect"
			}
			r.warnAt(m.key, childPointer(ptr, m.name), msg)
			continue
		}
		l.Bindings = append(l.Bindings, r.binding(childPointer(ptr, m.name), c, m, w.Bindings[m.name]))
	}
	slices.SortFunc(l.Bindings, func(a, b *Binding) int { return int(a.Control) - int(b.Control) })
	return l
}

func (r *resolver) binding(id string, c control.Control, n *node, w *wireBinding) *Binding {
	b := &Binding{ID: id, Control: c}
	if w == nil {
		return b // not reached: the schema requires an object
	}
	if w.Label != nil {
		b.Label = *w.Label
	}
	b.Block = w.Block
	if w.Press != nil {
		b.Press = r.action(childPointer(id, "press"), c, n.members["press"], w.Press, GesturePress)
	}
	for g, field := range map[Gesture]struct {
		name string
		a    *wireAction
	}{
		GestureRelease: {"release", w.Release}, GestureTap: {"tap", w.Tap},
		GestureDoubleTap: {"doubleTap", w.DoubleTap}, GestureTripleTap: {"tripleTap", w.TripleTap},
		GestureQuadTap: {"quadTap", w.QuadTap}, GestureHold: {"hold", w.Hold},
	} {
		if field.a != nil {
			a := r.action(childPointer(id, field.name), c, n.members[field.name], field.a, g)
			switch g {
			case GestureRelease:
				b.Release = a
			case GestureTap:
				b.Tap = a
			case GestureDoubleTap:
				b.DoubleTap = a
			case GestureTripleTap:
				b.TripleTap = a
			case GestureQuadTap:
				b.QuadTap = a
			case GestureHold:
				b.Hold = a
			}
		}
	}
	if w.Timing != nil {
		t := resolveTiming(w.Timing)
		b.Timing = &t
	}
	if w.Policy != nil {
		b.Policy = *w.Policy
	}
	b.Latch = w.Latch
	if w.Haptic != nil {
		p, ok := haptic.ParsePreset(*w.Haptic)
		if !ok {
			off, ptr := memberAt(id, n, "haptic")
			r.errorAt(off, ptr, "unknown haptic preset "+quote(*w.Haptic)) // not reached: the schema lists the presets
		}
		b.Haptic = p
	}
	return b
}

func resolveTiming(w *wireTiming) Timing {
	var t Timing
	if w == nil {
		return t
	}
	if w.HoldMs != nil {
		t.HoldMs = *w.HoldMs
		t.HoldSet = true
	}
	if w.MultiTapMs != nil {
		t.MultiTapMs = *w.MultiTapMs
		t.MultiTapSet = true
	}
	if w.TapPressMs != nil {
		t.TapPressMs = *w.TapPressMs
		t.TapPressSet = true
	}
	if w.TapGapMs != nil {
		t.TapGapMs = *w.TapGapMs
		t.TapGapSet = true
	}
	return t
}

// gestureDefaults is the mode of a keys action that does not set one, by
// gesture (ARCHITECTURE §10.3).
var gestureDefaults = map[Gesture]Mode{
	GesturePress:   ModeHold,
	GestureRelease: ModeTap, GestureTap: ModeTap, GestureDoubleTap: ModeTap,
	GestureTripleTap: ModeTap, GestureQuadTap: ModeTap, GestureHold: ModeHold,
}

// action resolves the action of gesture g at id, whose node is n, and
// checks the rules of one action of control c (ARCHITECTURE §7.7). engine.Compile
// checks the same rules, so every profile Parse returns compiles
// (TestParseAndCompileAgree).
func (r *resolver) action(id string, c control.Control, n *node, w *wireAction, g Gesture) *Action {
	a := &Action{ID: id}
	switch w.Type {
	case "keys":
		a.Type = ActionKeys
	case "profile":
		a.Type, a.Profile = ActionProfile, w.Profile
		if !ValidLibraryID(w.Profile) {
			off, ptr := memberAt(id, n, "profile")
			r.errorAt(off, ptr, "invalid profile library id")
		}
		return a
	case "macro":
		a.Type, a.File = ActionMacro, w.File
		if err := CheckMacroFile(w.File); err != nil {
			off, ptr := memberAt(id, n, "file")
			var ne *MacroNameError
			msg := err.Error()
			if errors.As(err, &ne) {
				msg = "invalid macro file name: " + ne.Detail
			}
			r.errorAt(off, ptr, msg)
		}
		return a
	case "mouse":
		a.Type, a.Op = ActionMouse, w.Op
		switch w.Op {
		case MouseOpPrecision:
			a.Factor = DefaultPrecisionFactor
			if w.Factor != nil {
				a.Factor = *w.Factor
			}
			if !g.Holds() {
				off, ptr := memberAt(id, n, "op")
				r.errorAt(off, ptr, PrecisionGestureMessage(g))
			}
		case MouseOpJump:
			if w.X != nil {
				a.X = *w.X
			}
			if w.Y != nil {
				a.Y = *w.Y
			}
		default:
			off, ptr := memberAt(id, n, "op")
			r.errorAt(off, ptr, "unknown mouse operation "+quote(w.Op)) // not reached: the schema lists the operations
		}
		return a
	case "hud":
		a.Type, a.Op = ActionHUD, w.Op
		switch w.Op {
		case HUDOpHints:
			if !g.Holds() {
				off, ptr := memberAt(id, n, "op")
				r.errorAt(off, ptr, HintsGestureMessage(g))
			}
		default:
			off, ptr := memberAt(id, n, "op")
			r.errorAt(off, ptr, "unknown hud operation "+quote(w.Op)) // not reached: the schema lists the operations
		}
		return a
	case "sequence":
		a.Type = ActionSequence
		a.Steps = r.sequenceSteps(id, n, w.Steps)
		return a
	case "text":
		a.Type, a.Text = ActionText, w.Text
		if err := keys.ValidateText(w.Text); err != nil {
			r.textError(n.members["text"], childPointer(id, "text"), err)
		}
		return a
	case "script":
		a.Type, a.Name, a.Args = ActionScript, w.Name, w.Args
		if strings.TrimSpace(a.Name) == "" {
			off, ptr := memberAt(id, n, "name")
			r.errorAt(off, ptr, "script action name must not be blank")
		}
		return a
	case "layer":
		a.Type = ActionLayer
		a.Layer, a.Layers, a.Op = w.Layer, slices.Clone(w.Layers), w.Op
		if a.Op == "" {
			r.errorAt(n.start, id+"/op", "missing layer action op")
		}
		if a.Op == "hold" && g.Momentary() {
			off, ptr := memberAt(id, n, "op")
			r.errorAt(off, ptr, "layer hold action is only valid for press/hold")
		}
		return a
	default:
		r.errorAt(n.start, id, "unknown action type "+quote(w.Type)) // not reached: the schema lists the types
		return a
	}
	var specs []string
	isSequence := len(w.Keys) > 0 && w.Keys[0] == '['
	if isSequence {
		if err := json.Unmarshal(w.Keys, &specs); err != nil {
			specs = nil
		}
	} else {
		var s string
		if err := json.Unmarshal(w.Keys, &s); err == nil {
			specs = []string{s}
		}
	}
	if len(specs) == 0 {
		r.errorAt(n.members["keys"].start, childPointer(id, "keys"), "keys must be a non-empty string or array")
		return a
	}
	seq := make([]keys.Chord, 0, len(specs))
	for _, spec := range specs {
		chord, err := keys.Parse(spec)
		if err != nil {
			r.keySpecError(n.members["keys"], childPointer(id, "keys"), err)
			continue
		}
		seq = append(seq, chord)
	}
	if len(seq) == 0 {
		return a
	}
	a.Keys = seq[0]
	if isSequence {
		a.Sequence = seq
	}
	a.Mode = gestureDefaults[g]
	if w.Mode != nil {
		m, ok := parseMode(*w.Mode)
		if !ok {
			r.errorAt(n.members["mode"].start, childPointer(id, "mode"), "unknown mode "+quote(*w.Mode)) // not reached: the schema lists the modes
		}
		a.Mode = m
	}
	if w.Repeat != nil {
		a.Repeat = &Repeat{}
		if w.Repeat.Style != nil {
			switch *w.Repeat.Style {
			case "tap":
				a.Repeat.Style = RepeatTap
			case "typematic":
				a.Repeat.Style = RepeatTypematic
			}
			if w.Mode == nil && a.Repeat.Style == RepeatTap {
				a.Mode = ModeTap
			}
		}
		a.Repeat.Style = a.Repeat.StyleFor(a.Mode)
		if w.Repeat.DelayMs != nil {
			a.Repeat.DelayMs = *w.Repeat.DelayMs
			a.Repeat.DelaySet = true
		}
		if w.Repeat.RateHz != nil {
			a.Repeat.RateHz = *w.Repeat.RateHz
		}
		if w.Repeat.MinRateHz != nil {
			a.Repeat.MinRateHz = *w.Repeat.MinRateHz
		}
	}
	r.checkKeys(id, c, n, g, a)
	return a
}

// keySpecError reports why the KeySpec in the string at node kn, at ptr, does
// not parse: the position is the offending token's when the string has no
// escapes, and the string's start otherwise.
func (r *resolver) keySpecError(kn *node, ptr string, err error) {
	off := kn.start
	var pe *keys.ParseError
	msg := err.Error()
	if errors.As(err, &pe) {
		msg = pe.Msg
		// A KeySpec in a string cannot be a sequence: the sequence is the
		// array around it.
		if m, ok := strings.CutSuffix(msg, keys.SequenceHint); ok {
			msg = m
		}
		if kn.raw {
			off += 1 + pe.Offset // the string holds the spec byte for byte
		}
	}
	r.errorAt(off, ptr, msg)
}

// textError reports why the text in the string at node tn, at ptr, cannot be
// typed (keys.ValidateText): the position is the offending character's when the
// string has no escapes, and the string's start otherwise.
func (r *resolver) textError(tn *node, ptr string, err error) {
	off := tn.start
	msg := err.Error()
	var te *keys.TextError
	if errors.As(err, &te) && tn.raw {
		off += 1 + te.Offset
	}
	r.errorAt(off, ptr, msg)
}

// sequenceSteps resolves the steps of the sequence action at id, whose node is
// n: the schema has seen that there are 1 to 64 of them and that each has just one
// of keys, wait and text; what is left is a KeySpec that parses and a text that
// can be typed. A step with an error is left out (the profile is refused).
func (r *resolver) sequenceSteps(id string, n *node, ws []wireStep) []keys.Step {
	steps := make([]keys.Step, 0, len(ws))
	list := n.members["steps"]
	base := childPointer(id, "steps")
	for i, w := range ws {
		sn := list.order[i]
		ptr := childPointer(base, strconv.Itoa(i))
		switch {
		case w.Keys != nil:
			chord, err := keys.Parse(*w.Keys)
			if err != nil {
				r.keySpecError(sn.members["keys"], childPointer(ptr, "keys"), err)
				continue
			}
			steps = append(steps, keys.KeysStep(chord))
		case w.Wait != nil:
			steps = append(steps, keys.WaitStep(time.Duration(*w.Wait)*time.Millisecond))
		case w.Text != nil:
			if err := keys.ValidateText(*w.Text); err != nil {
				r.textError(sn.members["text"], childPointer(ptr, "text"), err)
				continue
			}
			steps = append(steps, keys.TextStep(*w.Text))
		default:
			r.errorAt(sn.start, ptr, "a step needs keys, wait or text") // not reached: the schema wants one
		}
	}
	return steps
}

// MouseButtonRepeatMessage is the error of a keys action that holds a mouse
// button with typematic repeat (mode "hold" with a repeat). Typematic repeat
// presses the main key again while it is down, which a button must never get:
// the application would see double clicks. Repeated clicks are a repeat of mode
// "tap". engine.Compile says the same.
const MouseButtonRepeatMessage = `a mouse button cannot use typematic repeat, which presses it again while it is down: use mode "tap" to repeat clicks`

// checkKeys checks the rules of the keys action a of gesture g, whose node
// is n: a gesture that runs after the control is released uses tap mode and
// does not repeat, a key sequence uses tap mode and does not repeat, a mouse
// button does not repeat with typematic repeat, repeat's style follows its
// mode, and a minRateHz is on a control with a depth and not above the rate.
// Each member gets at most one error.
func (r *resolver) checkKeys(id string, c control.Control, n *node, g Gesture, a *Action) {
	switch {
	case g.Momentary() && a.Mode == ModeHold:
		off, ptr := memberAt(id, n, "mode")
		r.errorAt(off, ptr, g.String()+` runs after the control is released: use mode "tap"`)
	case a.Sequence != nil && a.Mode != ModeTap:
		// Without a mode, the gesture's default is hold: point at the
		// sequence.
		member := "mode"
		if n.members["mode"] == nil {
			member = "keys"
		}
		off, ptr := memberAt(id, n, member)
		r.errorAt(off, ptr, `a key sequence needs mode "tap"`)
	}
	if a.Repeat == nil {
		return
	}
	off, ptr := memberAt(id, n, "repeat")
	switch {
	case g.Momentary():
		r.errorAt(off, ptr, "repeat needs a press or hold gesture")
	case a.Sequence != nil:
		r.errorAt(off, ptr, "a key sequence cannot repeat")
	case a.Repeat.Style == RepeatTypematic && a.Mode != ModeHold:
		if w := n.members["repeat"].members["style"]; w != nil {
			off, ptr = w.start, id+"/repeat/style"
		}
		r.errorAt(off, ptr, `typematic repeat needs mode "hold"`)
	case a.Repeat.Style == RepeatTypematic && a.Keys.Key().IsMouseButton():
		if w := n.members["repeat"].members["style"]; w != nil {
			off, ptr = w.start, id+"/repeat/style"
		}
		r.errorAt(off, ptr, MouseButtonRepeatMessage)
	case a.Repeat.Style == RepeatTap && a.Mode != ModeTap:
		if w := n.members["repeat"].members["style"]; w != nil {
			off, ptr = w.start, id+"/repeat/style"
		}
		r.errorAt(off, ptr, `tap repeat needs mode "tap"`)
	case a.Repeat.MinRateHz > 0 && !c.HasDepth():
		off, ptr = minRateAt(id, n)
		r.errorAt(off, ptr, "minRateHz needs a stick, trigger or half-axis control")
	case a.Repeat.MinRateHz > a.Repeat.EffectiveRateHz():
		off, ptr = minRateAt(id, n)
		r.errorAt(off, ptr, "minRateHz must not exceed rateHz")
	}
}

// minRateAt returns the offset and pointer of the minRateHz member of the
// repeat of the action n at id.
func minRateAt(id string, n *node) (int, string) {
	rep := n.members["repeat"]
	if m := rep.members["minRateHz"]; m != nil {
		return m.start, id + "/repeat/minRateHz"
	}
	return rep.start, id + "/repeat"
}

// memberAt returns the offset and pointer of member name of the object n
// at id, or of n itself when it has no such member.
func memberAt(id string, n *node, name string) (int, string) {
	if m := n.members[name]; m != nil {
		return m.start, childPointer(id, name)
	}
	return n.start, id
}

// at formats the position of off for a message.
func (r *resolver) at(off int) string {
	if r.where != nil {
		return r.where(off)
	}
	line, col := position(r.data, off)
	return fmt.Sprintf("%d:%d", line, col)
}

// checkNoAction warns about bindings that do nothing: a label alone is
// valid but probably unfinished.
func checkNoAction(r *resolver, p *Profile) {
	for _, l := range append(slices.Clone(p.Layers), p.FunctionSets()...) {
		for _, b := range l.Bindings {
			if !b.Block && b.Press == nil && b.Release == nil && b.Tap == nil && b.DoubleTap == nil && b.TripleTap == nil && b.QuadTap == nil && b.Hold == nil {
				r.warnAt(r.root.lookupPointer(b.ID).start, b.ID, "binding has no action")
			}
		}
	}
}

func checkLayers(r *resolver, p *Profile) {
	names := map[string]bool{}
	for _, l := range p.Layers {
		if strings.TrimSpace(l.Name) == "" {
			r.errorAt(r.root.start, "/layers/"+l.Name, "layer name must not be blank")
		}
		if l.Name == "common" {
			r.errorAt(r.root.start, "/layers/common", "layer name common is reserved")
		}
		names[l.Name] = true
	}
	if !names[p.DefaultLayer] {
		r.errorAt(r.root.start, "/defaultLayer", "unknown default layer "+quote(p.DefaultLayer))
	}
	for _, l := range p.Layers {
		for _, b := range l.Bindings {
			checkLayerBinding(r, names, b)
		}
	}
	if p.Common != nil {
		for _, b := range p.Common.Bindings {
			checkLayerBinding(r, names, b)
		}
	}
	for _, l := range p.FunctionSets() {
		for _, b := range l.Bindings {
			checkLayerBinding(r, names, b)
		}
	}
}

func checkLayerBinding(r *resolver, names map[string]bool, b *Binding) {
	if b.Block && (b.Press != nil || b.Release != nil || b.Tap != nil || b.DoubleTap != nil || b.TripleTap != nil || b.QuadTap != nil || b.Hold != nil) {
		r.errorAt(r.root.start, b.ID, "block cannot have an action")
	}
	for _, a := range []*Action{b.Press, b.Release, b.Tap, b.DoubleTap, b.TripleTap, b.QuadTap, b.Hold} {
		if a == nil || a.Type != ActionLayer {
			continue
		}
		if a.Op == "cycle" {
			if len(a.Layers) == 0 {
				r.errorAt(r.root.start, a.ID+"/layers", "cycle needs layers")
			}
			for _, name := range a.Layers {
				if !names[name] {
					r.errorAt(r.root.start, a.ID+"/layers", "unknown layer "+quote(name))
				}
			}
			for i, name := range a.Layers {
				for _, other := range a.Layers[i+1:] {
					if name == other {
						r.errorAt(r.root.start, a.ID+"/layers", "repeated layer "+quote(name))
					}
				}
			}
		} else if !names[a.Layer] {
			r.errorAt(r.root.start, a.ID+"/layer", "unknown layer "+quote(a.Layer))
		}
	}
}

func checkAnalog(r *resolver, p *Profile) {
	checkStick := func(name string, s Stick) {
		if s.Saturation <= s.Deadzone {
			r.errorAt(r.root.start, "/analog/"+name+"/saturation", "saturation must be greater than deadzone")
		}
		if s.Mode == "" {
			r.errorAt(r.root.start, "/analog/"+name+"/mode", "missing analog mode")
		}
	}
	checkStick("ls", p.Analog.LS)
	checkStick("rs", p.Analog.RS)
	checkTrigger := func(name string, t Trigger) {
		if t.Release > t.Press {
			r.errorAt(r.root.start, "/analog/"+name+"/release", "release must not exceed press")
		}
		if t.FullRelease > t.FullPress {
			r.errorAt(r.root.start, "/analog/"+name+"/fullRelease", "fullRelease must not exceed fullPress")
		}
		// Only a fullPress that is written is held to this: the default,
		// 0.90, must not reject a profile whose press is 0.90 or more, such
		// as the two-stage workaround the editor wrote before lt.full (the
		// second stage then presses together with the first).
		if t.FullPress <= t.Press && r.root.lookupPointer("/analog/"+name+"/fullPress") != nil {
			r.errorAt(r.root.start, "/analog/"+name+"/fullPress", "fullPress must be greater than press")
		}
	}
	checkTrigger("lt", p.Analog.LT)
	checkTrigger("rt", p.Analog.RT)
}
