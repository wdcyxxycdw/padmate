package engine

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Decision records what the engine did with one input, or with one cycle
// that CancelAll ended, and why (ENG-20). It is a value whose strings and
// pointers point into the Program, so building one does not allocate.
type Decision struct {
	T       clock.Time
	Control control.Control
	Edge    Edge   // EdgeNone for CancelAll and inputs that are not edges
	Cycle   uint64 // the cycle decided about; for a spurious edge the control's current cycle, 0 while up
	Gesture Gesture
	Level   string           // the level where the binding was found, "" when none
	Binding *profile.Binding // nil when none
	Action  *profile.Action  // the action run or released, nil when none
	Owner   OwnerID          // the owner of the output sent; zero when none
	Reason  Reason
}

// MarshalJSON writes d as one compact object with its keys in a fixed
// order, such as
//
//	{"t":20,"control":"south","edge":"down","cycle":1,"gesture":"press","level":"base","binding":"/layers/base/south","label":"撤销","action":"tap Ctrl+Z","owner":"decl/1","reason":"dispatch"}
//
// t is in milliseconds, with the shortest exact fraction. Empty fields are
// left out: gesture, level, binding, the binding's label, action, owner,
// and edge for EdgeNone. action is the mode and the chord. Nothing is
// escaped beyond what JSON needs; json.Marshal escapes <, > and & again,
// an Encoder with SetEscapeHTML(false) does not.
func (d Decision) MarshalJSON() ([]byte, error) {
	w := wireDecision{
		T:       json.Number(d.T.AppendMs(nil)),
		Control: d.Control.String(),
		Cycle:   d.Cycle,
		Level:   d.Level,
		Reason:  d.Reason.String(),
	}
	if d.Edge != EdgeNone {
		w.Edge = d.Edge.String()
	}
	if d.Gesture != GestureNone {
		w.Gesture = d.Gesture.String()
	}
	if b := d.Binding; b != nil {
		w.Binding, w.Label = b.ID, b.Label
	}
	if a := d.Action; a != nil {
		w.Action = a.String()
	}
	if d.Owner != (OwnerID{}) {
		w.Owner = d.Owner.String()
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(w); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// String returns the JSON of d.
func (d Decision) String() string {
	b, err := d.MarshalJSON()
	if err != nil {
		return "engine.Decision: " + err.Error() // not reached: every field encodes
	}
	return string(b)
}

// wireDecision is the JSON form of a Decision, in key order.
type wireDecision struct {
	T       json.Number `json:"t"`
	Control string      `json:"control"`
	Edge    string      `json:"edge,omitempty"`
	Cycle   uint64      `json:"cycle"`
	Gesture string      `json:"gesture,omitempty"`
	Level   string      `json:"level,omitempty"`
	Binding string      `json:"binding,omitempty"`
	Label   string      `json:"label,omitempty"`
	Action  string      `json:"action,omitempty"`
	Owner   string      `json:"owner,omitempty"`
	Reason  string      `json:"reason"`
}

// Edge is the edge of a control that a decision is about.
type Edge uint8

const (
	// EdgeNone is no edge: CancelAll, or an input that is not an edge.
	EdgeNone Edge = iota
	// EdgeDown is a press.
	EdgeDown
	// EdgeUp is a release.
	EdgeUp
)

// String returns "none", "down", "up", or "edge(N)" for any other value.
func (e Edge) String() string {
	switch e {
	case EdgeNone:
		return "none"
	case EdgeDown:
		return "down"
	case EdgeUp:
		return "up"
	}
	return "edge(" + strconv.Itoa(int(e)) + ")"
}

// Reason says why the engine did what a decision records. The zero value
// is invalid.
type Reason uint8

const (
	// ReasonDispatch is a press that ran an action: Down in hold mode, Tap
	// in tap mode.
	ReasonDispatch Reason = iota + 1
	// ReasonUnbound is a press that found no binding, or no action for its
	// gesture: no output. It is not a failure when the binding has a tap,
	// hold, release or multi-tap action: the press only starts the wait for
	// it, and the action comes later as a dispatch. When that wait ends with a
	// number of taps the binding has no action for (a double tap only, tapped
	// once), it is the decision of that wait's end: the gesture is the tap,
	// double tap, triple tap or quad tap of the count, with the binding and
	// level the press found. Only a cycle that nothing has answered gets it:
	// a press or release action that ran, or a script that took the press,
	// has answered it, and the end of the window says nothing more. With a
	// script's gesture
	// registry (Options.Gestures) it is any gesture that every level passed
	// and no declaration answered, and the binding is the resolution the
	// engine made for the press (ScriptResolutionID).
	ReasonUnbound
	// ReasonRelease is a release that ended a cycle holding a chord: Up.
	ReasonRelease
	// ReasonEnd is a release that ended a cycle holding nothing (tap mode,
	// unbound): no output.
	ReasonEnd
	// ReasonCancel is CancelAll ending an active cycle: no output, the
	// Arbiter releases its keys. It also ends the cycle of a multi-tap window
	// that CancelAll drops, when nothing has answered that cycle: the press that
	// opened the window waited for its end, and the window holds nothing.
	ReasonCancel
	// ReasonCanceled is the release of a cycle that CancelAll ended, or of
	// a press while paused: no output.
	ReasonCanceled
	// ReasonSpurious is an ignored input: a down of a control already
	// down, an up of one already up, or an invalid input.
	ReasonSpurious
	// ReasonPaused is a press while the engine is paused (DMN-06): its
	// cycle ends at once, as if CancelAll had ended it, and there is no
	// output. Its release is ReasonCanceled.
	ReasonPaused
	// ReasonBlocked is a layer binding that consumes an input without an action.
	ReasonBlocked
	// ReasonFunction is an edge of a function key (MAP-27, ADR-0069): it
	// starts no cycle and sends nothing, and only changes which set the next
	// presses look up first. It has no cycle, level or binding.
	ReasonFunction
	// ReasonScript is a gesture that a script took (ADR-0071): a handler of
	// the script's gesture registry ran it, a claim of the script ended it,
	// or the script's button handler consumed the press, or the release of a
	// cycle that was waiting for a tap, a multi-tap, a hold or a release action
	// (gesture release, the cycle of the press). It sends nothing
	// and has no level, binding, action or owner: what the script did with
	// the keys is not attributed to a decision. The edge is EdgeDown, as for
	// every gesture, whatever physical edge made it.
	ReasonScript
)

var reasonNames = [...]string{
	ReasonDispatch: "dispatch",
	ReasonUnbound:  "unbound",
	ReasonRelease:  "release",
	ReasonEnd:      "end",
	ReasonCancel:   "cancel",
	ReasonCanceled: "canceled",
	ReasonSpurious: "spurious",
	ReasonPaused:   "paused",
	ReasonBlocked:  "blocked",
	ReasonFunction: "function",
	ReasonScript:   "script",
}

// String returns the name of r, such as "dispatch", or "reason(N)" when r
// is not valid.
func (r Reason) String() string {
	if r >= ReasonDispatch && int(r) < len(reasonNames) {
		return reasonNames[r]
	}
	return "reason(" + strconv.Itoa(int(r)) + ")"
}
