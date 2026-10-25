package engine

import (
	"slices"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// ScriptResolutionID is the ID of the binding that stands for what a control
// can do under a script's gesture registry: the engine builds it for each
// press from the registrations and the declarations (scriptPlan), and it is
// the binding of the decisions of that cycle that no declaration answered.
// It is no entry of any profile.
const ScriptResolutionID = "script-resolution"

// GestureEntry is immutable registration metadata copied from a runtime.
// Runtime distinguishes registrations of successive script activations.
type GestureEntry struct {
	ID, Runtime uint64
	Control     control.Control
	Gesture     Gesture
	Scope       string // global, base, or layer:<name>
	Priority    int
	Label       string
	Timing      profile.Timing
	Claim       bool
	Observe     bool
}

// GestureEvent is a recognized event. Its layer and registration are from
// the press snapshot, while Held reports the current physical state.
type GestureEvent struct {
	Control        control.Control
	Gesture        Gesture
	Time           clock.Time
	Duration       time.Duration
	Count          int
	Scope          string
	Cycle, Runtime uint64
	Held, Canceled bool
}

// ScriptActionEvent asks the script runtime to run an action: a named
// action of the legacy script (profile.ActionScript), or a macro
// (profile.ActionMacro, SCR-50) when Macro is its file name. Name and Args
// are empty for a macro.
type ScriptActionEvent struct {
	Name  string
	Args  map[string]any
	Macro string
	GestureEvent
}

// GestureResolver participates in recognition without owning a recognizer.
// All calls occur on the engine's owner goroutine. A false DispatchGesture
// result passes the event to the next level of its press snapshot.
type GestureResolver interface {
	GestureEntries() []GestureEntry
	ObserveGesture(GestureEvent)
	DispatchGesture(uint64, GestureEvent) bool
	EndGesture(GestureEvent)
}

type gestureSource struct {
	scope      string
	eventScope string
	entries    []GestureEntry
	binding    *profile.Binding
}

type gesturePlan struct {
	sources []gestureSource
	runtime uint64
}

func setGesture(b *profile.Binding, g Gesture, a *profile.Action) {
	switch g {
	case GesturePress:
		b.Press = a
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

// scriptPlan captures accessible levels and the union of the gestures they
// can handle. A declaration is terminal for its gesture; a claim is terminal
// for the whole control. Script handlers remain reachable because they can pass.
// fn is the function key held when the press arrived (control.Invalid for
// none): its set is the level after the script's global() handlers and before
// every layer (MAP-27, ARCHITECTURE §7.3). A script event of its bindings has
// the scope "global", the nearest scope of the script API, which has no scope
// for the function keys.
func (e *Engine) scriptPlan(c, fn control.Control) (*gesturePlan, *profile.Binding) {
	entries := e.gestures.GestureEntries()
	p := &gesturePlan{}
	addScript := func(scope string, extra string) {
		var selected []GestureEntry
		for _, r := range entries {
			if r.Control == c && (r.Scope == scope || extra != "" && r.Scope == extra) {
				selected = append(selected, r)
				p.runtime = r.Runtime
			}
		}
		if len(selected) == 0 {
			return
		}
		slices.SortStableFunc(selected, func(a, b GestureEntry) int { return b.Priority - a.Priority })
		p.sources = append(p.sources, gestureSource{scope: scope, entries: selected})
	}
	addDecl := func(l *profile.Layer, eventScope string) {
		if l != nil {
			if b := l.Binding(c); b != nil {
				p.sources = append(p.sources, gestureSource{scope: l.Name, eventScope: eventScope, binding: b})
			}
		}
	}
	addScript("global", "")
	if set, name := e.prog.functionSet(fn); set != nil {
		if b := set.Binding(c); b != nil {
			p.sources = append(p.sources, gestureSource{scope: name, eventScope: "global", binding: b})
		}
	}
	for i := len(e.overlays) - 1; i >= 0; i-- {
		name := e.overlays[i].name
		addScript("layer:"+name, "")
		addDecl(e.prog.layer(name), "layer:"+name)
	}
	addScript("base", "layer:"+e.baseLayer)
	addDecl(e.prog.layer(e.baseLayer), "base")
	addDecl(e.prog.common, "common")
	b := &profile.Binding{Control: c, ID: ScriptResolutionID, Timing: &profile.Timing{}}
	for g := GesturePress; g <= GestureHold; g++ {
		var action *profile.Action
		for _, src := range p.sources {
			claimed := false
			for _, r := range src.entries {
				claimed = claimed || r.Claim
			}
			for _, r := range src.entries {
				if r.Gesture == g {
					if action == nil {
						action = &profile.Action{}
						b.Label = r.Label
					}
					mergeGestureTiming(b.Timing, e.prog.timingFor(&profile.Binding{Timing: &r.Timing}))
					break
				}
			}
			if claimed {
				break
			}
			if src.binding != nil {
				if src.binding.Block {
					break
				}
				if a := src.binding.Action(g); a != nil {
					if action == nil {
						action = a
						b.Label = src.binding.Label
					}
					mergeGestureTiming(b.Timing, e.prog.timingFor(src.binding))
					if src.binding.Policy != "" {
						b.Policy = src.binding.Policy
					}
					break
				}
			}
		}
		setGesture(b, g, action)
	}
	return p, b
}

func mergeGestureTiming(dst *profile.Timing, src profile.Timing) {
	dst.HoldMs = max(dst.HoldMs, src.HoldMs)
	dst.MultiTapMs = max(dst.MultiTapMs, src.MultiTapMs)
	dst.HoldSet, dst.MultiTapSet = true, true
}

func (e *Engine) gestureEvent(now clock.Time, c control.Control, st *ctlState, g Gesture) GestureEvent {
	end := now
	if !e.physical[c] {
		end = e.physicalReleased[c]
	}
	ev := GestureEvent{Control: c, Gesture: g, Time: now, Duration: time.Duration(max(end-st.started, 0)), Count: max(st.tapCount, 1), Scope: st.level, Cycle: st.cycle, Held: e.physical[c] && !g.Momentary()}
	if st.plan != nil {
		ev.Runtime = st.plan.runtime
	}
	if st.eventScope != "" {
		ev.Scope = st.eventScope
	}
	return ev
}

// gestureOutcome is how resolveGesture ended.
type gestureOutcome uint8

const (
	// gestureFound is a declared action, which the other results of
	// resolveGesture describe.
	gestureFound gestureOutcome = iota
	// gestureScript is a gesture that a handler took or a claim ended.
	gestureScript
	// gestureBlocked is a binding that blocks the control.
	gestureBlocked
	// gestureNone is a gesture that every level passed and no declaration
	// answered.
	gestureNone
)

// resolveGesture offers gesture g of the cycle in st to the levels of its
// plan in order. It returns the declared action and its binding when
// outcome is gestureFound; the blocking binding and the name of its level
// when it is gestureBlocked.
func (e *Engine) resolveGesture(now clock.Time, c control.Control, st *ctlState, g Gesture) (a *profile.Action, b *profile.Binding, level string, outcome gestureOutcome) {
	ev := e.gestureEvent(now, c, st, g)
	for _, src := range st.plan.sources {
		claimed := false
		for _, r := range src.entries {
			claimed = claimed || r.Claim
		}
		for _, r := range src.entries {
			if r.Gesture != g {
				continue
			}
			ev.Scope, ev.Runtime = r.Scope, r.Runtime
			if e.gestures.DispatchGesture(r.ID, ev) {
				return nil, nil, "", gestureScript
			}
			break // pass continues at the next level, not another same-level handler
		}
		if claimed {
			return nil, nil, "", gestureScript
		}
		if src.binding != nil {
			if src.binding.Block {
				return nil, src.binding, src.scope, gestureBlocked
			}
			if a := src.binding.Action(g); a != nil {
				st.level = src.scope
				st.eventScope = src.eventScope
				return a, src.binding, src.scope, gestureFound
			}
		}
	}
	return nil, nil, "", gestureNone
}

func (e *Engine) endScriptGesture(now clock.Time, c control.Control, st *ctlState, canceled bool) {
	if e.gestures == nil || st.plan == nil {
		return
	}
	ev := e.gestureEvent(now, c, st, GestureRelease)
	ev.Held, ev.Canceled = false, canceled
	e.gestures.EndGesture(ev)
}

// SetGestures installs a slot-owned runtime resolver after a reload.
func (e *Engine) SetGestures(r GestureResolver) { e.gestures = r }
