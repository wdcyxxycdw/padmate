package script

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync/atomic"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

type gestureKey struct {
	control string
	cycle   int64
}

var gestureRuntimeIDs atomic.Uint64

type gestureState struct {
	key    gestureKey
	owner  output.Owner
	ended  bool
	held   map[string]keys.Chord
	order  []keys.Chord
	layers []func()
	ends   []goja.Callable
}

func parseGesture(name string) engine.Gesture {
	for g := engine.GesturePress; g <= engine.GestureHold; g++ {
		if g.String() == name {
			return g
		}
	}
	return engine.GestureNone
}

// GestureEntries copies metadata without exposing goja to the pure engine.
func (h *Host) GestureEntries() []engine.GestureEntry {
	if !h.active || h.disposed {
		return nil
	}
	var entries []engine.GestureEntry
	for i, r := range h.registrations {
		if r.disposed {
			continue
		}
		c, ok := control.Parse(r.Control)
		if !ok || h.gestureIgnored[c] {
			continue
		}
		scope := "base"
		switch r.Scope {
		case ScopeGlobal:
			scope = "global"
		case ScopeLayer:
			scope = "layer:" + r.Layer
		}
		entries = append(entries, engine.GestureEntry{ID: uint64(i + 1), Runtime: h.runtimeID, Control: c, Gesture: parseGesture(r.Gesture), Scope: scope, Priority: r.Priority, Label: r.Label, Timing: r.Timing, Claim: r.Claim, Observe: r.Observe})
	}
	return entries
}

func scriptGestureEvent(e engine.GestureEvent) Event {
	return Event{Control: e.Control.String(), Gesture: e.Gesture.String(), Time: e.Time, Duration: e.Duration, Count: e.Count, Scope: e.Scope, Cycle: int64(e.Cycle), Held: e.Held}
}

// ObserveGesture delivers resolved events without participating in ownership
// or recognition timing. Observers are global and run before consumers.
func (h *Host) ObserveGesture(e engine.GestureEvent) error {
	if !h.active || h.disposed || e.Runtime != h.runtimeID || h.gestureIgnored[e.Control] {
		return nil
	}
	ev := scriptGestureEvent(e)
	ev.Scope = "global"
	var errs []error
	for _, r := range h.registrations {
		if r.Observe && !r.disposed && r.Control == ev.Control {
			errs = appendError(errs, h.callHandler(r.Handler, ev))
		}
	}
	_, err := h.finishDispatch(DispatchResult{}, errors.Join(errs...))
	h.passRequested = false
	return err
}

// DispatchGesture invokes a registration chosen by the engine's snapshot.
func (h *Host) DispatchGesture(id uint64, e engine.GestureEvent) (bool, error) {
	if !h.active || h.disposed || e.Runtime != h.runtimeID || id == 0 || id > uint64(len(h.registrations)) {
		return false, nil
	}
	r := h.registrations[id-1]
	if r.disposed || r.Claim {
		return false, nil
	}
	err := h.callHandler(r.Handler, scriptGestureEvent(e))
	consumed := !h.passRequested
	h.passRequested = false
	_, err = h.finishDispatch(DispatchResult{Consumed: consumed}, err)
	return consumed, err
}

func (h *Host) readGestureOptions(r *Registration, v goja.Value) {
	if goja.IsUndefined(v) || goja.IsNull(v) {
		return
	}
	o, ok := v.(*goja.Object)
	if !ok {
		panic(h.VM.NewTypeError("on: options must be an object"))
	}
	for _, name := range o.Keys() {
		switch name {
		case "label":
			r.Label = o.Get(name).String()
		case "priority":
			r.Priority = h.gestureInteger(o.Get(name), name, -1000000, 1000000)
		case "holdMs":
			r.Timing.HoldMs = h.gestureInteger(o.Get(name), name, 0, 60000)
			r.Timing.HoldSet = true
		case "multiTapMs":
			r.Timing.MultiTapMs = h.gestureInteger(o.Get(name), name, 0, 60000)
			r.Timing.MultiTapSet = true
		default:
			panic(h.VM.NewTypeError("on: unknown option " + name))
		}
	}
}

func (h *Host) gestureInteger(v goja.Value, name string, minimum, maximum int) int {
	f := v.ToFloat()
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || f < float64(minimum) || f > float64(maximum) {
		panic(h.VM.NewTypeError(fmt.Sprintf("on: %s must be an integer between %d and %d", name, minimum, maximum)))
	}
	return int(f)
}

func (h *Host) claimGesture(scope RegistrationScope, layer string, call goja.FunctionCall) goja.Value {
	target := call.Argument(0).String()
	var controls []control.Control
	if c, ok := control.Parse(target); ok {
		controls = append(controls, c)
	} else {
		for _, c := range control.All() {
			name := c.String()
			if target == "ls" && strings.HasPrefix(name, "ls.") || target == "rs" && strings.HasPrefix(name, "rs.") || target == "dpad" && strings.HasPrefix(name, "dpad.") {
				controls = append(controls, c)
			}
		}
	}
	if len(controls) == 0 {
		panic(h.VM.NewTypeError("claim: unknown target " + target))
	}
	first := len(h.registrations)
	for _, c := range controls {
		h.registrations = append(h.registrations, Registration{Control: c.String(), Scope: scope, Layer: layer, Claim: true})
	}
	last := len(h.registrations)
	return h.VM.ToValue(h.disposable(func() {
		for i := first; i < last; i++ {
			h.registrations[i].disposed = true
		}
	}))
}

func (h *Host) gestureState(e Event) *gestureState {
	k := gestureKey{e.Control, e.Cycle}
	if st := h.gestureStates[k]; st != nil {
		return st
	}
	st := &gestureState{key: k, owner: h.newOwner(), held: make(map[string]keys.Chord), ended: !e.Held}
	if !st.ended {
		h.gestureStates[k] = st
	}
	return st
}

func (h *Host) installGestureEvent(o *goja.Object, e Event) {
	st := h.gestureState(e)
	held := func() bool {
		c, ok := control.Parse(e.Control)
		return !st.ended && h.active && ok && h.padState.Down[c] && (e.Gesture == "press" || e.Gesture == "hold")
	}
	_ = o.DefineAccessorProperty("held", h.VM.ToValue(func(goja.FunctionCall) goja.Value { return h.VM.ToValue(held()) }), nil, goja.FLAG_FALSE, goja.FLAG_TRUE)
	setFunction(h.VM, o, "holdKeys", 1, func(call goja.FunctionCall) goja.Value {
		if !held() {
			panic(h.VM.NewTypeError("holdKeys is only valid while the control is held; use key.tap"))
		}
		chord, ok := h.parseChord(h.base, call.Argument(0).String())
		if !ok {
			return goja.Undefined()
		}
		chord = chord.Sided()
		if _, ok := st.held[chord.String()]; ok {
			return goja.Undefined()
		}
		if !h.budget.Allow(2 * len(chord.Keys())) {
			h.base.addError(ErrOutputBudget)
			return goja.Undefined()
		}
		st.held[chord.String()] = chord
		st.order = append(st.order, chord)
		h.recordGestureOutput(st.owner, chord, false)
		if h.output != nil {
			h.base.addError(h.output.Down(st.owner, chord))
		}
		return goja.Undefined()
	})
	setFunction(h.VM, o, "holdLayer", 1, func(call goja.FunctionCall) goja.Value {
		if !held() {
			panic(h.VM.NewTypeError("holdLayer is only valid while the control is held"))
		}
		dispose := h.holdLayer(call.Argument(0).String())
		st.layers = append(st.layers, dispose)
		return goja.Undefined()
	})
	setFunction(h.VM, o, "onEnd", 1, func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			panic(h.VM.NewTypeError("onEnd: callback must be a function"))
		}
		if st.ended {
			h.queueGestureEnd(fn)
		} else {
			st.ends = append(st.ends, fn)
		}
		return goja.Undefined()
	})
}

func appendError(errs []error, err error) []error {
	if err != nil {
		return append(errs, err)
	}
	return errs
}

// newOwner returns an owner of its own for output that outlives the call
// that starts it: a gesture's holds, or a run of a macro. Its cycle is new
// across every runtime of the slot when the slot shares OwnerCycles.
func (h *Host) newOwner() output.Owner {
	o := h.base.owner
	if h.ownerCycles != nil {
		o.Cycle = h.ownerCycles.Add(1)
	} else {
		h.gestureSequence++
		o.Cycle = h.gestureSequence
	}
	return o
}

// isChordHeld reports whether the legacy script holds chord, with key.down
// or a gesture's holdKeys.
func (h *Host) isChordHeld(chord keys.Chord) bool {
	spec := chord.Sided().String()
	if h.base.isChordHeld(chord) {
		return true
	}
	for _, st := range h.gestureStates {
		if _, ok := st.held[spec]; ok {
			return true
		}
	}
	return false
}

func (h *Host) recordGestureOutput(owner output.Owner, chord keys.Chord, up bool) {
	h.outputs = append(h.outputs, OutputEvent{Spec: chord.String(), Up: up, Time: h.clock.Now(), Owner: owner, Chord: chord})
}

func (h *Host) queueGestureEnd(fn goja.Callable) {
	h.Loop.QueueMicrotask(func() {
		if h.active && !h.disposed {
			_, err := fn(goja.Undefined())
			h.base.addError(Diagnose(PhaseHandler, err))
		}
	})
}

func (h *Host) releaseGesture(st *gestureState, callbacks bool) {
	st.ended = true
	for i := len(st.order) - 1; i >= 0; i-- {
		chord := st.order[i]
		h.recordGestureOutput(st.owner, chord, true)
		if h.output != nil {
			h.base.addError(h.output.Up(st.owner, chord))
		}
	}
	if h.output != nil && !callbacks {
		h.base.addError(h.output.ReleaseOwner(st.owner))
	}
	for _, dispose := range st.layers {
		dispose()
	}
	st.held = nil
	st.order = nil
	st.layers = nil
	if callbacks {
		for _, fn := range st.ends {
			h.queueGestureEnd(fn)
		}
	}
	st.ends = nil
	delete(h.gestureStates, st.key)
}

// EndGesture releases holds before scheduling normal end callbacks. Safety
// boundaries abandon user continuations, matching Suspend's timer policy.
func (h *Host) EndGesture(e engine.GestureEvent) error {
	if st := h.gestureStates[gestureKey{e.Control.String(), int64(e.Cycle)}]; st != nil {
		h.releaseGesture(st, !e.Canceled)
	}
	return h.takeErrors()
}

func (h *Host) releaseGestureStates(callbacks bool) {
	for _, st := range h.gestureStates {
		h.releaseGesture(st, callbacks)
	}
}
