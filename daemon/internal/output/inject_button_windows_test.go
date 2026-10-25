package output_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/injecttest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

// The tests in this file inject the mouse buttons (ADR-0062, second batch). A
// button is held until it is released, unlike a wheel notch, so these tests are
// stricter than the wheel ones in inject_windows_test.go:
//
//   - A button is never injected on its own. Every press goes out with its
//     release in the same batch (injecttest.Recorder.InjectFencedMixed, which
//     refuses any other), after the mouse canary has proven the mouse hook
//     live, and the mouse canary is the last of the batch.
//   - A click, a press and a release, is the most a batch has of one button: a
//     second pair would be a double click if the hook ever let it through.
//   - No test here holds a button across batches or across a sleep, and none
//     uses the Arbiter, whose hold is a press now and a release later.
//
// What the hook must report is injecttest.ButtonMessage's: WM_LBUTTONDOWN and
// WM_LBUTTONUP (0x201, 0x202), the right button's 0x204, 0x205, the middle
// button's 0x207, 0x208 and WM_XBUTTONDOWN, WM_XBUTTONUP (0x20B, 0x20C) with the
// number of the side button in the high word of mouseData. Those values and the
// mouseData come from WinUser.h and the documentation, and `task test:inject`
// measured them on 2026-10-19 (Windows 11 x64): the hook saw exactly these
// messages, the side button's number (1, 2) in the high word, flags 0x01
// (LLMHF_INJECTED) and the nonce in the low 32 bits of dwExtraInfo, and
// swallowed them all. SM_SWAPBUTTON was false there, so what the hook reports
// with the buttons swapped is still unmeasured.
//
// What the tests check of what the hooks saw is in buttonSeenProblems and
// strokeProblems, which TestButtonSeenProblems and TestStrokeProblems run on
// made-up events, so that the checks are themselves tested without injecting
// anything.

// buttonSeenProblems returns what is wrong with what the hooks saw of clicks
// clicks of the mouse button m: it must be exactly a press and a release for
// each, in that order, all mouse events, each as injecttest.ButtonProblems has
// it (the message, the side button number in mouseData, LLMHF_INJECTED, the
// nonce, swallowed), in the order the hook thread saw them, and none of them
// moving the cursor: a button event has no coordinates, so every event of one
// batch is at the same position.
func buttonSeenProblems(m keys.Mouse, nonce uint32, clicks int, got []injecttest.Seen) []string {
	if len(got) != 2*clicks {
		return []string{fmt.Sprintf("the hooks saw %d events, want %d (a press and a release for each of %d clicks): %v", len(got), 2*clicks, clicks, got)}
	}
	var problems []string
	var prevSeq uint64
	for i, s := range got {
		e := s.Mouse
		if e == nil {
			problems = append(problems, fmt.Sprintf("event %d is a keyboard event: %v", i, s))
			continue
		}
		up := i%2 == 1
		for _, p := range injecttest.ButtonProblems(*e, m, up, nonce) {
			problems = append(problems, fmt.Sprintf("event %d: %s", i, p))
		}
		if s.Seq() <= prevSeq {
			problems = append(problems, fmt.Sprintf("event %d has Seq %d, not after %d", i, s.Seq(), prevSeq))
		}
		prevSeq = s.Seq()
		if first := got[0].Mouse; first != nil && (e.X != first.X || e.Y != first.Y) {
			problems = append(problems, fmt.Sprintf("event %d is at (%d, %d), the first at (%d, %d): a button event moved the cursor", i, e.X, e.Y, first.X, first.Y))
		}
	}
	return problems
}

// checkInjectedButton fails the test with buttonSeenProblems. InjectFencedMixed
// has already counted the events swallowed.
func checkInjectedButton(t *testing.T, m keys.Mouse, nonce uint32, clicks int, got []injecttest.Seen) {
	t.Helper()
	if problems := buttonSeenProblems(m, nonce, clicks, got); len(problems) > 0 {
		t.Errorf("%v: %s", m, strings.Join(problems, "; "))
	}
}

// strokeProblems returns what is wrong with what the hooks saw of the events
// of a stroke with a mouse button, a chord's modifiers around the click: every
// event is injected (none of the stroke is silent), so the hooks must have seen
// all of them, in the order of the stroke, the modifier presses, the button
// press, the button release, the modifier releases. A keyboard event must be
// the modifier of the stroke (its scan code, E0 flag, direction and the US VK of
// the key table, since a modifier does not depend on the layout), a mouse event
// the button as ButtonProblems has it. One sequence orders the events of both
// hooks (winhook Seq), which must increase, and the stamps must not go back.
func strokeProblems(events []output.KeyEvent, nonce uint32, got []injecttest.Seen) []string {
	if len(got) != len(events) {
		return []string{fmt.Sprintf("the hooks saw %d events, want %d: %v", len(got), len(events), got)}
	}
	var problems []string
	var prevSeq, prevStamp uint64
	for j, w := range events {
		s := got[j]
		if s.Seq() <= prevSeq {
			problems = append(problems, fmt.Sprintf("event %d has Seq %d, not after %d", j, s.Seq(), prevSeq))
		}
		prevSeq = s.Seq()
		var stamp uint64
		switch {
		case w.Mouse != keys.MouseNone:
			if s.Mouse == nil {
				problems = append(problems, fmt.Sprintf("event %d = %v, want the %v button event %v", j, s, w.Mouse, w))
				continue
			}
			for _, p := range injecttest.ButtonProblems(*s.Mouse, w.Mouse, w.Up, nonce) {
				problems = append(problems, fmt.Sprintf("event %d (%v): %s", j, w, p))
			}
			stamp = uint64(s.Mouse.Stamp)
		default:
			if s.Keyboard == nil || s.Keyboard.Scan != uint32(w.Scan) || s.Keyboard.Ext() != w.Ext ||
				s.Keyboard.Up() != w.Up || s.Keyboard.VK != uint32(w.Key.US().VK) {
				problems = append(problems, fmt.Sprintf("event %d = %v, want %v (vk 0x%02X)", j, s, w, w.Key.US().VK))
				continue
			}
			stamp = uint64(s.Keyboard.Stamp)
		}
		if stamp == 0 || stamp < prevStamp {
			problems = append(problems, fmt.Sprintf("event %d has stamp %d after %d", j, stamp, prevStamp))
		}
		prevStamp = stamp
	}
	return problems
}

// TestInjectMouseButtons (ADR-0062) injects a click of each mouse button, its
// press and its release in one batch, and checks what the mouse hook sees: the
// message (WM_LBUTTONDOWN and so on), the number of the side button in the high
// word of mouseData for MouseX1 and MouseX2, LLMHF_INJECTED, the nonce, that the
// hook swallowed it, and that the release follows the press. The mouse hook is
// proven live by a mouse canary before the first click and every batch has one
// behind it (injecttest.InjectFencedMixed). The keyboard hook sees nothing of
// it. The events seen are logged, so that a run on a machine with the buttons
// swapped (unmeasured) shows what the hook reported.
func TestInjectMouseButtons(t *testing.T) {
	injecttest.Require(t)
	base := injecttest.NonceBase()
	rec := injecttest.Start(t, base)
	rec.Canary(t, base)
	rec.MouseCanary(t, base+1)
	t.Logf("mouse buttons swapped (SM_SWAPBUTTON): %v", injecttest.ButtonsSwapped())
	keyboardSeen := len(rec.Own())

	nonce := base + 1
	for _, name := range []string{"MouseLeft", "MouseRight", "MouseMiddle", "MouseX1", "MouseX2"} {
		key := keys.MustParse(name).Key()
		down, err := output.KeyEventFor(key, false)
		if err != nil {
			t.Fatal(err)
		}
		up := down
		up.Up = true
		m, _ := key.Mouse()

		nonce++
		got := rec.InjectFencedMixed(t, nonce, []output.KeyEvent{down, up})
		t.Logf("%s: %v", name, got)
		checkInjectedButton(t, m, nonce, 1, got)
	}
	// None of the batches had a keyboard event, so the keyboard hook saw nothing.
	if n := len(rec.Own()); n != keyboardSeen {
		t.Errorf("the keyboard hook saw %d owned events during the button batches, want none", n-keyboardSeen)
	}
}

// TestInjectMouseButtonChord (ADR-0062) injects Shift+MouseLeft and
// Ctrl+Shift+MouseX2 as the Arbiter sends them, modifiers around the click, in
// one SendInput call, and checks the order in which the two hooks saw the
// events (strokeProblems): the modifier presses, the button press, the button
// release, the modifier releases in reverse. As for the wheel chord, one
// sequence orders the events of both hooks and the stamps do not go back. The
// whole stroke is in one batch, so the click never starts without its release
// behind it. No Alt or Win: a modifier that got through would show.
func TestInjectMouseButtonChord(t *testing.T) {
	injecttest.Require(t)
	base := injecttest.NonceBase()
	rec := injecttest.Start(t, base)
	rec.Canary(t, base)
	rec.MouseCanary(t, base+1)

	for i, spec := range []string{"Shift+MouseLeft", "Ctrl+Shift+MouseX2"} {
		nonce := base + 2 + uint32(i)
		stroke, err := output.ChordStroke(keys.MustParse(spec))
		if err != nil {
			t.Fatal(err)
		}
		events := slices.Concat(stroke.Down, stroke.Up)
		got := rec.InjectFencedMixed(t, nonce, events)
		if problems := strokeProblems(events, nonce, got); len(problems) > 0 {
			t.Errorf("%s: %s", spec, strings.Join(problems, "; "))
		}
	}
}

// The tests below inject nothing and install no hook: they run the checks
// above on events made up as a hook would report them.

// stampOf is the Stamp of the made-up event with sequence number seq: later
// events have later stamps.
func stampOf(seq uint64) latency.Stamp { return latency.Stamp(1000 + 10*seq) }

// seenButton is what the mouse hook sees of the press (or with up the release)
// of the mouse button m injected with nonce, if it works as WinUser.h says.
func seenButton(seq uint64, nonce uint32, m keys.Mouse, up bool) injecttest.Seen {
	msg, x := injecttest.ButtonMessage(m, up)
	return injecttest.Seen{Mouse: &winhook.MouseEvent{
		X: 640, Y: 360, Msg: msg, MouseData: uint32(x) << 16, Flags: winhook.MouseFlagInjected,
		Extra: uintptr(nonce), Swallowed: true, Stamp: stampOf(seq), Seq: seq,
	}}
}

// seenKey is what the keyboard hook sees of w, a modifier event of a stroke.
func seenKey(seq uint64, nonce uint32, w output.KeyEvent) injecttest.Seen {
	flags := uint32(winhook.FlagInjected)
	msg := uint32(winhook.WMKeyDown)
	if w.Up {
		flags |= winhook.FlagUp
		msg = winhook.WMKeyUp
	}
	if w.Ext {
		flags |= winhook.FlagExtended
	}
	return injecttest.Seen{Keyboard: &winhook.Event{
		VK: uint32(w.Key.US().VK), Scan: uint32(w.Scan), Flags: flags, Extra: output.ExtraInfo(nonce),
		Msg: msg, Swallowed: true, Stamp: stampOf(seq), Seq: seq,
	}}
}

// mutated returns s with its mouse event changed by f.
func mutated(s injecttest.Seen, f func(e *winhook.MouseEvent)) injecttest.Seen {
	e := *s.Mouse
	f(&e)
	return injecttest.Seen{Mouse: &e}
}

func TestButtonSeenProblems(t *testing.T) {
	const nonce = 0x4D4F0001
	click := func(seq uint64, m keys.Mouse) []injecttest.Seen {
		return []injecttest.Seen{seenButton(seq, nonce, m, false), seenButton(seq+1, nonce, m, true)}
	}
	left := click(1, keys.MouseLeft)
	for _, tt := range []struct {
		name   string
		m      keys.Mouse
		clicks int
		got    []injecttest.Seen
		want   string // a part of the problems, or "" for none
	}{
		{"a click of the left button", keys.MouseLeft, 1, left, ""},
		{"a click of the right button", keys.MouseRight, 1, click(1, keys.MouseRight), ""},
		{"a click of the middle button", keys.MouseMiddle, 1, click(1, keys.MouseMiddle), ""},
		{"a click of the first side button", keys.MouseX1, 1, click(1, keys.MouseX1), ""},
		{"a click of the second side button", keys.MouseX2, 1, click(1, keys.MouseX2), ""},
		{"two clicks", keys.MouseRight, 2, slices.Concat(click(1, keys.MouseRight), click(3, keys.MouseRight)), ""},

		{"nothing", keys.MouseLeft, 1, nil, "saw 0 events, want 2"},
		{"the press only", keys.MouseLeft, 1, left[:1], "saw 1 events, want 2"},
		{"a click too many", keys.MouseLeft, 1, slices.Concat(left, click(3, keys.MouseLeft)), "saw 4 events, want 2"},
		{"the other button", keys.MouseLeft, 1, click(1, keys.MouseMiddle), "message WM_MBUTTONDOWN"},
		{"the other side button", keys.MouseX2, 1, click(1, keys.MouseX1), "side button 1"},
		{"the release first", keys.MouseLeft, 1, []injecttest.Seen{left[1], left[0]}, "message WM_LBUTTONUP"},
		{"a keyboard event for the release", keys.MouseLeft, 1,
			[]injecttest.Seen{left[0], {Keyboard: &winhook.Event{Seq: 2}}}, "event 1 is a keyboard event"},
		{"not swallowed", keys.MouseLeft, 1,
			[]injecttest.Seen{left[0], mutated(left[1], func(e *winhook.MouseEvent) { e.Swallowed = false })}, "event 1: not swallowed"},
		{"another nonce", keys.MouseLeft, 1,
			[]injecttest.Seen{mutated(left[0], func(e *winhook.MouseEvent) { e.Extra++ }), left[1]}, "event 0: extra"},
		{"the cursor moved", keys.MouseLeft, 1,
			[]injecttest.Seen{left[0], mutated(left[1], func(e *winhook.MouseEvent) { e.X++ })}, "moved the cursor"},
		{"Seq goes back", keys.MouseLeft, 1,
			[]injecttest.Seen{left[0], mutated(left[1], func(e *winhook.MouseEvent) { e.Seq = 1 })}, "event 1 has Seq 1, not after 1"},
	} {
		problems := strings.Join(buttonSeenProblems(tt.m, nonce, tt.clicks, tt.got), "; ")
		switch {
		case tt.want == "" && problems != "":
			t.Errorf("%s: problems %q, want none", tt.name, problems)
		case tt.want != "" && !strings.Contains(problems, tt.want):
			t.Errorf("%s: problems %q, want one with %q", tt.name, problems, tt.want)
		}
	}
}

func TestStrokeProblems(t *testing.T) {
	const nonce = 0x4D4F0002
	stroke, err := output.ChordStroke(keys.MustParse("Shift+MouseLeft"))
	if err != nil {
		t.Fatal(err)
	}
	events := slices.Concat(stroke.Down, stroke.Up)
	if len(events) != 4 || events[1].Mouse != keys.MouseLeft || events[1].Up || events[2].Mouse != keys.MouseLeft || !events[2].Up {
		t.Fatalf("the stroke of Shift+MouseLeft is %v, want Shift down, the button down, the button up, Shift up", events)
	}
	// What the hooks see of it, if all is well.
	seen := func() []injecttest.Seen {
		var got []injecttest.Seen
		for i, w := range events {
			if w.Mouse != keys.MouseNone {
				got = append(got, seenButton(uint64(i+1), nonce, w.Mouse, w.Up))
			} else {
				got = append(got, seenKey(uint64(i+1), nonce, w))
			}
		}
		return got
	}
	withKey := func(i int, f func(e *winhook.Event)) []injecttest.Seen {
		got := seen()
		e := *got[i].Keyboard
		f(&e)
		got[i] = injecttest.Seen{Keyboard: &e}
		return got
	}
	for _, tt := range []struct {
		name string
		got  []injecttest.Seen
		want string // a part of the problems, or "" for none
	}{
		{"all as it should be", seen(), ""},
		{"an event missing", seen()[:3], "saw 3 events, want 4"},
		{"the click before the modifier", slices.Concat(seen()[1:2], seen()[:1], seen()[2:]), "want the"},
		{"the release of the modifier first", slices.Concat(seen()[:2], seen()[3:], seen()[2:3]), "event 2 = "},
		{"the press for the release of the button", []injecttest.Seen{seen()[0], seen()[1], seenButton(3, nonce, keys.MouseLeft, false), seen()[3]}, "message WM_LBUTTONDOWN"},
		{"the button swallowed late", func() []injecttest.Seen {
			got := seen()
			got[2] = mutated(got[2], func(e *winhook.MouseEvent) { e.Swallowed = false })
			return got
		}(), "not swallowed"},
		{"Seq goes back", withKey(3, func(e *winhook.Event) { e.Seq = 2 }), "event 3 has Seq 2, not after 3"},
		{"the stamp goes back", withKey(3, func(e *winhook.Event) { e.Stamp = 1 }), "event 3 has stamp 1"},
		{"no stamp", withKey(0, func(e *winhook.Event) { e.Stamp = 0 }), "event 0 has stamp 0"},
		{"the wrong modifier", withKey(0, func(e *winhook.Event) { e.VK = 0x11 }), "event 0 = "},
		{"the modifier up for down", withKey(0, func(e *winhook.Event) { e.Flags |= winhook.FlagUp }), "event 0 = "},
		{"the modifier with an E0 prefix", withKey(0, func(e *winhook.Event) { e.Flags |= winhook.FlagExtended }), "event 0 = "},
	} {
		problems := strings.Join(strokeProblems(events, nonce, tt.got), "; ")
		switch {
		case tt.want == "" && problems != "":
			t.Errorf("%s: problems %q, want none", tt.name, problems)
		case tt.want != "" && !strings.Contains(problems, tt.want):
			t.Errorf("%s: problems %q, want one with %q", tt.name, problems, tt.want)
		}
	}
}
