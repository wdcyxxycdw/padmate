package engine

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// This file is the key hints (HUD-03, ADR-0063): a HUD hints action
// ({"type":"hud","op":"hints"}) turns the hints on for as long as its cycle
// holds, the way a precision action holds its factor (stick_mouse.go). It sends
// no key, and nothing outside the engine's own state changes.
//
// While the hints are on, the engine keeps the list of what the controls do in
// the layer stack that is current: the label of the binding each control
// resolves to, or else a short text of its first action. The list is built when
// the hints turn on, when another hints action turns on (the control that holds
// it is left out of the list), and when the stack changes while they are on
// (notifyLayer); never while the stack stays as it is. View carries it to the
// slot's status, where the UI's overlay reads it.
//
// Every path that ends a held action ends the hints too, because they are held
// like one: the release of the control, CancelAll (so a pause, a reload, a
// reset, a removed device and ReleaseAll), and a latch's next press. The
// tests of hints_test.go run each.

// Hint is one entry of the key hints: what a control does in the current layer
// stack, as the overlay shows it next to the control's name.
type Hint struct {
	Control control.Control
	Label   string
}

// maxHintLabelRunes caps the label of one hint, so that a long label of a
// profile does not make the status of the slot large; the overlay cuts it
// shorter to fit.
const maxHintLabelRunes = 64

// hintsState is the key hints of an Engine: on while a hints action is held,
// with the list of hints to show.
type hintsState struct {
	on   bool
	list []Hint
}

// hintGestures is the order in which the gestures of a binding without a label
// are looked at for the text of its hint: what the control does when pressed or
// held first, then the taps, and the release last.
var hintGestures = [...]Gesture{GesturePress, GestureTap, GestureHold, GestureDoubleTap, GestureTripleTap, GestureQuadTap, GestureRelease}

// runHUDAction runs a HUD action of the cycle st: hints turn on for as long as
// the cycle holds (the control's release, CancelAll or a latch replaced by the
// next press end it).
func (e *Engine) runHUDAction(st *ctlState, a *profile.Action) {
	if a.Op != profile.HUDOpHints {
		return // checkAction refuses any other
	}
	st.hints = true
	e.updateHints()
}

// updateHints makes the hints state what the cycles and latches say: on, with
// a new list, when one of them holds a hints action, else off. It runs where
// that set changes, so it is not on the path of an input that holds none.
func (e *Engine) updateHints() {
	for c := range e.ctl {
		if e.ctl[c].hints || e.latch[c].hints {
			e.hints = hintsState{on: true, list: e.hintList()}
			return
		}
	}
	e.hints = hintsState{}
}

// hintList returns the hints of the current layer stack, in control order.
// Each control is resolved as a press does (lookup): the top level that has a
// binding for it wins, and a binding that blocks hides it. A control is left
// out when the binding it resolves to has nothing to show (no label, and no
// action with a text), and the controls that hold a hints action are left out:
// the one the user is holding is not an answer to what the buttons do. The
// function key is left out too: a binding on it is ignored.
//
// The hints are of the declared bindings; what a script registers for a
// control is not in them.
func (e *Engine) hintList() []Hint {
	list := make([]Hint, 0, 16)
	for i := 1; i < control.Count; i++ {
		c := control.Control(i)
		if c == e.prog.fnKey || e.ctl[c].hints || e.latch[c].hints {
			continue // the function key does nothing of its own
		}
		_, b, _, blocked := e.lookup(c, GesturePress)
		if b == nil || blocked {
			continue
		}
		if label := hintLabel(b); label != "" {
			list = append(list, Hint{Control: c, Label: label})
		}
	}
	return list
}

// hintLabel returns the text of the hint of binding b: its label, or else the
// text of its first action (hintGestures), or "" when it has neither.
func hintLabel(b *profile.Binding) string {
	if label := cleanHint(b.Label); label != "" {
		return label
	}
	for _, g := range hintGestures {
		if a := b.Action(g); a != nil {
			if text := cleanHint(actionHint(a)); text != "" {
				return text
			}
		}
	}
	return ""
}

// actionHint is the short text of action a for a hint: the shortcut of a keys
// action (the chords of a sequence apart by a comma), the name of a script
// action or macro, and a few words for the others. The layer, profile and
// mouse words are Chinese, whatever the language of the UI: the UI shows the
// text as it gets it.
func actionHint(a *profile.Action) string {
	switch a.Type {
	case profile.ActionKeys:
		if len(a.Sequence) > 1 {
			parts := make([]string, len(a.Sequence))
			for i, c := range a.Sequence {
				parts[i] = c.String()
			}
			return strings.Join(parts, ", ")
		}
		return a.Keys.String()
	case profile.ActionLayer:
		if a.Op == "cycle" {
			return "层：" + strings.Join(a.Layers, "/")
		}
		return "层：" + a.Layer
	case profile.ActionScript:
		return a.Name
	case profile.ActionMacro:
		return strings.TrimSuffix(strings.TrimSuffix(a.File, ".ts"), ".js")
	case profile.ActionProfile:
		return "配置：" + a.Profile
	case profile.ActionSequence:
		return "序列：" + keys.FormatSteps(a.Steps)
	case profile.ActionText:
		return "文字：" + keys.ShortText(a.Text, keys.ShortTextRunes)
	case profile.ActionMouse:
		if a.Op == profile.MouseOpJump {
			return "鼠标跳转"
		}
		return "精确模式"
	case profile.ActionHUD:
		return "按键提示"
	}
	return ""
}

// cleanHint makes s a one-line text: control characters are dropped, runs of
// white space become one space, the ends are trimmed, and a text of more than
// maxHintLabelRunes runes is cut there with an ellipsis.
func cleanHint(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= maxHintLabelRunes {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:maxHintLabelRunes-1])) + "…"
}
