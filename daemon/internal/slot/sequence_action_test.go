package slot_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// Bindings of the sequence tests (MAP-22, OUT-21, ADR-0070): a shortcut, a
// wait and a text; a text alone.
const (
	southSequence = `"south": {"label": "序列", "press": {"type": "sequence", "steps": [{"keys": "Ctrl+A"}, {"wait": 100}, {"text": "ab"}, {"keys": "F13"}]}}`
	eastText      = `"east": {"label": "文字", "press": {"type": "text", "text": "xyz"}}`
)

// unit is how the trace names the Unicode event of a typed character.
func unit(r rune, up bool) string {
	sign := "+"
	if up {
		sign = "-"
	}
	return sign + output.UnicodeEvent(uint16(r), up).ID().String()
}

// typedAt is the trace of typing the characters of s at t ms.
func typedAt(t int, s string) string {
	var parts []string
	for _, r := range s {
		parts = append(parts, unit(r, false)+"@"+strconv.Itoa(t), unit(r, true)+"@"+strconv.Itoa(t))
	}
	return strings.Join(parts, " ")
}

// A sequence runs when its gesture fires: its steps go to the Arbiter, which
// times them (the slot's timing is the profile's, here the defaults of 20 ms for
// the press and the gap).
func TestSequenceActionRuns(t *testing.T) {
	h := newHarness(t, 0, 0, southSequence, eastText)
	h.replay(t, "+south@0 -south@30")
	want := "+LCtrl@0 +A@0 -A@20 -LCtrl@20 " + typedAt(120, "a") + " " + typedAt(140, "b") + " +F13@160 -F13@180"
	h.ExpectOutput(want)
	// The text action is a sequence of a text step: it types, a gap apart.
	h.replay(t, "+east@1000 -east@1020")
	h.ExpectOutput(typedAt(1000, "x") + " " + typedAt(1020, "y") + " " + typedAt(1040, "z"))
	h.expectDecisions(t, "@0 south down dispatch", "@30 south up end", "@1000 east down dispatch", "@1020 east up end")
	simtest.AssertNoneHeld(t, h.Arb, h.Out)
}

// A second trigger of a binding runs after the first is over, not among its
// steps, and a text action behind a sequence waits for it too.
func TestSequenceActionSecondTriggerWaits(t *testing.T) {
	h := newHarness(t, 0, 0, southSequence, eastText)
	h.replay(t, "+south@0 -south@10 +south@50 -south@60 +east@70 -east@80")
	got := h.Output()
	// The trace has the first run in full, then the second, then the text.
	run := func(at int) string {
		return "+LCtrl@" + strconv.Itoa(at) + " +A@" + strconv.Itoa(at) + " -A@" + strconv.Itoa(at+20) + " -LCtrl@" + strconv.Itoa(at+20) +
			" " + typedAt(at+120, "a") + " " + typedAt(at+140, "b") + " +F13@" + strconv.Itoa(at+160) + " -F13@" + strconv.Itoa(at+180)
	}
	want := run(0) + " " + run(180) + " " + typedAt(360, "x") + " " + typedAt(380, "y") + " " + typedAt(400, "z")
	if got != want {
		t.Errorf("output\n got %s\nwant %s", got, want)
	}
}

// What ends the slot's keys ends a sequence under way, in its wait and in its
// text, and nothing it had not sent is sent afterwards (CLAUDE.md invariant 1):
// a ReleaseAll (the exit and the emergency release), the pause (also by the
// emergency combo), a reload (a profile switch), a removed device. The late
// physical release does nothing.
func TestSequenceActionCanceledOnEveryPath(t *testing.T) {
	paths := []struct {
		name string
		path func(t *testing.T, h *harness)
	}{
		{"ReleaseAll", func(t *testing.T, h *harness) {
			if err := h.s.ReleaseAll(); err != nil {
				t.Fatal(err)
			}
		}},
		{"ReleaseAllForExit", func(t *testing.T, h *harness) {
			if err := h.s.ReleaseAllForExit(); err != nil {
				t.Fatal(err)
			}
		}},
		{"pause", func(t *testing.T, h *harness) {
			if err := h.s.SetPaused(true); err != nil {
				t.Fatal(err)
			}
		}},
		{"pause combo", func(t *testing.T, h *harness) {
			if err := h.s.Replay(h.Clock, comboTrace(t, "combo@50")); err != nil {
				t.Fatal(err)
			}
		}},
		{"reload", func(t *testing.T, h *harness) {
			if err := h.s.Reload(compile(t, eastText), 2); err != nil {
				t.Fatal(err)
			}
		}},
		{"device removed", func(t *testing.T, h *harness) {
			h.s.Handle(input.Event{Kind: input.DeviceRemoved, Device: 7})
		}},
	}
	states := []struct {
		name    string
		binding string
		sent    string // what has gone out at 50 ms
	}{
		{"in the wait", southSequence, "+LCtrl@0 +A@0 -A@20 -LCtrl@20"},
		{"in the text", `"south": {"press": {"type": "sequence", "steps": [{"text": "abcdef"}, {"keys": "F13"}]}}`, typedAt(0, "a") + " " + typedAt(20, "b") + " " + typedAt(40, "c")},
		{"in a queued sequence", southSequence + `, "west": {"press": {"type": "sequence", "steps": [{"keys": "F14"}]}}`, "+LCtrl@0 +A@0 -A@20 -LCtrl@20"},
	}
	for _, st := range states {
		for _, p := range paths {
			t.Run(st.name+"/"+p.name, func(t *testing.T) {
				h := newHarness(t, 0, 0, st.binding)
				h.s.Handle(button("south", true))
				if strings.Contains(st.name, "queued") {
					h.s.Handle(button("west", true))
				}
				h.Advance(50 * ms)
				h.ExpectOutput(st.sent)
				p.path(t, h)
				h.ExpectOutput("")
				h.Advance(time.Minute)
				h.ExpectOutput("")
				simtest.AssertNoneHeld(t, h.Arb, h.Out)
				if d, ok := h.s.NextDeadline(); ok {
					t.Errorf("NextDeadline = %v after the cancel, want none", d)
				}
				// The late physical release of the controls sends nothing more.
				h.s.Handle(button("south", false))
				h.s.Handle(button("west", false))
				h.Advance(time.Second)
				h.ExpectOutput("")
			})
		}
	}
}
