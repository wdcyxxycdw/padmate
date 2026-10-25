package injecttest

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

// The tests in this file inject nothing and install no hook: they feed a
// Recorder without a hook fabricated events, and check the batches that
// InjectFencedMixed would send.

const mouseTestBase = 0x4D4F_0000 // "MO"

// delivered is the dwExtraInfo of a mouse event as a mouse hook sees it: the
// low 32 bits of what was injected, the nonce (measured 2026-10-19).
func delivered(nonce uint32) uintptr { return uintptr(nonce) }

// wheel is an injected wheel notch of delta (+-120) with nonce, seen by a hook
// that swallowed it as swallowed says.
func wheel(seq uint64, nonce uint32, delta int16, swallowed bool) winhook.MouseEvent {
	return winhook.MouseEvent{
		Msg: winhook.WMMouseWheel, MouseData: uint32(uint16(delta)) << 16, Flags: winhook.MouseFlagInjected,
		Extra: delivered(nonce), Swallowed: swallowed, Seq: seq,
	}
}

// move is an injected relative move with nonce: one of the two events of the
// mouse canary, as the hook sees it.
func move(seq uint64, nonce uint32, swallowed bool) winhook.MouseEvent {
	return winhook.MouseEvent{
		Msg: winhook.WMMouseMove, X: 500, Y: 300, Flags: winhook.MouseFlagInjected,
		Extra: delivered(nonce), Swallowed: swallowed, Seq: seq,
	}
}

// keyEvent is an injected padmate key event with nonce.
func keyEvent(seq uint64, nonce uint32, vk uint32, up, swallowed bool) winhook.Event {
	flags := uint32(winhook.FlagInjected)
	if up {
		flags |= winhook.FlagUp
	}
	return winhook.Event{VK: vk, Flags: flags, Extra: output.ExtraInfo(nonce), Swallowed: swallowed, Seq: seq}
}

// The mouse canary is a move of +1 and one of -1 along x, so a leak leaves the
// cursor where it was; it is not a wheel event, nor a move of no distance, which
// never reach a hook (measured 2026-10-19).
func TestMouseCanaryEvents(t *testing.T) {
	events := MouseCanaryEvents()
	if len(events) != MouseCanaryLen {
		t.Fatalf("MouseCanaryEvents() has %d events, want MouseCanaryLen = %d", len(events), MouseCanaryLen)
	}
	sum := 0
	for i, e := range events {
		if e.Mouse != keys.MouseMove || e.Dx == 0 || e.Dy != 0 || e.Up {
			t.Errorf("event %d = %+v, want a relative move along x", i, e)
		}
		if e.Silent() {
			t.Errorf("event %d is silent: it would inject nothing, and no hook would ever see it", i)
		}
		sum += int(e.Dx)
	}
	if events[0].Dx != 1 || events[1].Dx != -1 || sum != 0 {
		t.Errorf("moves %+d then %+d (sum %d), want +1, -1", events[0].Dx, events[1].Dx, sum)
	}
}

func TestIsMouseCanary(t *testing.T) {
	const nonce = mouseTestBase | 5
	canary := move(1, nonce, true)
	with := func(f func(e *winhook.MouseEvent)) winhook.MouseEvent { e := canary; f(&e); return e }
	for _, tt := range []struct {
		name string
		e    winhook.MouseEvent
		want bool
	}{
		{"canary, as Windows delivers it", canary, true},
		{"canary, with the whole tag kept", with(func(e *winhook.MouseEvent) { e.Extra = output.ExtraInfo(nonce) }), true},
		{"not swallowed is still the canary", move(1, nonce, false), true},
		{"a notch", wheel(1, nonce, 120, true), false},
		{"a notch down", wheel(1, nonce, -120, true), false},
		{"another nonce", move(1, nonce+1, true), false},
		{"another injector's high half", with(func(e *winhook.MouseEvent) { e.Extra = 0x1_0000_0000 | uintptr(nonce) }), false},
		{"not injected", with(func(e *winhook.MouseEvent) { e.Flags = 0 }), false},
		{"a horizontal wheel", with(func(e *winhook.MouseEvent) { e.Msg = winhook.WMMouseHWheel }), false},
		{"a button", with(func(e *winhook.MouseEvent) { e.Msg = winhook.WMLButtonDown }), false},
	} {
		if got := IsMouseCanary(tt.e, nonce); got != tt.want {
			t.Errorf("%s: IsMouseCanary = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// The mouse hook reports, and swallows, the injected events whose nonce the
// recorder owns, with the high half of their extra 0 (what Windows delivers) or
// the whole tag: the physical mouse, whatever it sends, and the other injectors
// are neither.
func TestRecorderMousePredicates(t *testing.T) {
	r := newRecorder(func(nonce uint32) bool { return Owns(mouseTestBase, nonce) })
	owned := wheel(0, mouseTestBase|1, 120, false)
	tagged := owned
	tagged.Extra = output.ExtraInfo(mouseTestBase | 1)     // in case some Windows keeps the tag
	other := wheel(0, mouseTestBase+0x10000|1, 120, false) // a nonce of another test
	foreign := owned
	foreign.Extra = 0x1234 // injected by someone else
	pen := owned
	pen.Extra = 0xFF515700 | 0x80 // what Windows sends for a pen or touch input
	highHalf := owned
	highHalf.Extra |= 0x1_0000_0000 // not a half padmate sends
	otherTag := owned
	otherTag.Extra = 0x7061646C_00000000 | owned.Extra // "padl"
	physical := owned
	physical.Flags = 0
	physical.Extra = output.ExtraInfo(mouseTestBase | 1) // not injected, tagged anyway
	physicalNonce := owned
	physicalNonce.Flags = 0 // not injected, the nonce is right
	moved := winhook.MouseEvent{Msg: winhook.WMMouseMove, X: 100, Y: 200}
	for _, tt := range []struct {
		name  string
		e     winhook.MouseEvent
		owned bool
	}{
		{"owned", owned, true},
		{"owned, tag kept", tagged, true},
		{"the canary", move(0, mouseTestBase|1, false), true},
		{"another test's nonce", other, false},
		{"injected by another injector", foreign, false},
		{"a pen or touch signature", pen, false},
		{"an unknown high half", highHalf, false},
		{"another tag", otherTag, false},
		{"physical, tagged", physical, false},
		{"physical, right nonce", physicalNonce, false},
		{"physical move", moved, false},
		{"physical wheel", winhook.MouseEvent{Msg: winhook.WMMouseWheel, MouseData: 120 << 16}, false},
		{"injected, extra 0", winhook.MouseEvent{Msg: winhook.WMMouseMove, Flags: winhook.MouseFlagInjected}, false},
	} {
		// The hook reports what it may swallow, and nothing else.
		if got := r.mouseWatched(tt.e); got != tt.owned {
			t.Errorf("%s: mouseWatched = %v, want %v", tt.name, got, tt.owned)
		}
		if got := r.mouseOwned(tt.e); got != tt.owned {
			t.Errorf("%s: mouseOwned = %v, want %v", tt.name, got, tt.owned)
		}
	}
}

// NonceBase stays clear of the dwExtraInfo values other injectors send, since
// a mouse event is told from theirs by its nonce alone: 0 and the small ids,
// the pen and touch signature 0xFF5157xx, and -1. Callers that set the top bit
// get the same.
func TestNonceBaseClash(t *testing.T) {
	for _, tt := range []struct {
		base uint32
		want bool
	}{
		{0, true}, {0x80000000, true}, // 0 through 0xFFFF, with and without the top bit
		{0xFF510000, true}, {0x7F510000, true}, // 0xFF5157xx
		{0xFFFF0000, true}, {0x7FFF0000, true}, // 0xFFFFFFFF
		{0x00010000, false}, {0xFF500000, false}, {0xFF520000, false}, {0x4D4F0000, false}, {0xCD4F0000, false},
	} {
		if got := nonceBaseClash(tt.base); got != tt.want {
			t.Errorf("nonceBaseClash(%#x) = %v, want %v", tt.base, got, tt.want)
		}
	}
	// What the pen and touch signature spans is in a clashing base.
	for _, extra := range []uint32{0xFF515700, 0xFF515780, 0xFF5157FF, 0, 1, 0xFFFF, 0xFFFFFFFF} {
		if base := extra &^ 0xFFFF; !nonceBaseClash(base) {
			t.Errorf("nonceBaseClash(%#x) = false, but its nonces include %#x", base, extra)
		}
	}
	for range 5000 {
		if base := NonceBase(); base&0xFFFF != 0 || nonceBaseClash(base) || nonceBaseClash(base|0x80000000) {
			t.Fatalf("NonceBase() = %#x", base)
		}
	}
}

// Mouse events are counted with the keyboard's: WaitOwn waits for the total,
// fails at an owned mouse event that was not swallowed, and OwnAll returns
// both kinds in the order the hook thread saw them, which the keyboard
// and mouse lists alone do not tell.
func TestRecorderNoteMouse(t *testing.T) {
	const base = mouseTestBase
	r := newRecorder(func(nonce uint32) bool { return Owns(base, nonce) })

	r.note(keyEvent(1, base|1, 0xA2, false, true))    // Ctrl down
	r.noteMouse(wheel(2, base|1, 120, true))          // the notch
	r.note(keyEvent(3, base|1, 0xA2, true, true))     // Ctrl up
	r.noteMouse(wheel(4, base+0x10000|1, 120, false)) // another test's: recorded, not owned
	if n := r.OwnCount(); n != 3 {
		t.Fatalf("OwnCount() = %d, want 3 (two keys and a notch)", n)
	}
	if err := r.WaitOwn(3, 0); err != nil {
		t.Fatalf("WaitOwn(3, 0) = %v, want nil", err)
	}
	if got := len(r.Own()); got != 2 {
		t.Errorf("len(Own()) = %d, want the 2 keyboard events", got)
	}
	if got := r.OwnMouse(); len(got) != 1 || got[0].Seq != 2 {
		t.Errorf("OwnMouse() = %+v, want the one notch", got)
	}
	all := r.OwnAll()
	if len(all) != 3 || all[0].Keyboard == nil || all[1].Mouse == nil || all[2].Keyboard == nil || all[0].Seq() != 1 || all[1].Seq() != 2 || all[2].Seq() != 3 {
		t.Errorf("OwnAll() = %v, want key, notch, key by Seq", all)
	}
	if k, m := Keyboard(all), Mouse(all); len(k) != 2 || len(m) != 1 {
		t.Errorf("Keyboard, Mouse of OwnAll = %d, %d events, want 2, 1", len(k), len(m))
	}
	if err := r.WaitOwn(4, 20*time.Millisecond); err == nil || !strings.Contains(err.Error(), "3 of 4 owned events") {
		t.Errorf("WaitOwn(4, 20ms) = %v, want a timeout with 3 of 4", err)
	}

	// WaitMouseFence returns the notches of its nonce before the mouse canary,
	// and waits for both of its moves.
	const fenceNonce = base | 3
	r.noteMouse(wheel(6, fenceNonce, 120, true))
	r.noteMouse(move(7, fenceNonce, true))
	if _, _, ok := r.WaitMouseFence(fenceNonce, 20*time.Millisecond); ok {
		t.Error("WaitMouseFence succeeded with one of the two moves of the canary")
	}
	r.noteMouse(move(8, fenceNonce, true))
	if before, fence, ok := r.WaitMouseFence(fenceNonce, time.Second); !ok || len(before) != 1 || fence.Seq != 8 || !fence.Swallowed {
		t.Errorf("WaitMouseFence = %+v, %+v, %v, want the notch, the last move of the canary, true", before, fence, ok)
	}
	if _, _, ok := r.WaitMouseFence(base|4, 20*time.Millisecond); ok {
		t.Error("WaitMouseFence of a nonce with no canary succeeded")
	}

	// An owned mouse event the hook let through fails the wait at once and
	// for good, as a key does.
	r.noteMouse(wheel(5, base|2, 120, false))
	if err := r.WaitOwn(1, 0); !errors.Is(err, ErrUnswallowed) {
		t.Errorf("WaitOwn(1, 0) after an owned notch got past the hook = %v, want ErrUnswallowed", err)
	}
}

func TestSeenString(t *testing.T) {
	k := keyEvent(1, mouseTestBase|7, 0xA2, true, true)
	m := wheel(2, mouseTestBase|7, -120, true)
	for _, tt := range []struct {
		s    Seen
		want string
	}{
		{Seen{Keyboard: &k}, "{key vk=0xA2 sc=0x00 up=true flags=0x90 extra=0x7061646d4d4f0007 swallowed=true seq=1}"},
		{Seen{Mouse: &m}, "{mouse WM_MOUSEWHEEL delta=-120 data=0xff880000 flags=0x01 extra=0x4d4f0007 swallowed=true seq=2}"},
	} {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

// A fence is complete when the canaries its batch carried are there; what came
// before them is the batch.
func TestFenceOf(t *testing.T) {
	const nonce = mouseTestBase | 9
	seen := func(es ...any) []Seen {
		var out []Seen
		for _, e := range es {
			switch e := e.(type) {
			case winhook.Event:
				out = append(out, Seen{Keyboard: &e})
			case winhook.MouseEvent:
				out = append(out, Seen{Mouse: &e})
			}
		}
		return out
	}
	ctrl := keyEvent(1, nonce, 0xA2, false, true)
	notch := wheel(2, nonce, 120, true)
	keyCanary := keyEvent(3, nonce, CanaryVK, true, true)
	moveUp, moveBack := move(4, nonce, true), move(5, nonce, true) // the two moves of the mouse canary
	otherNotch := wheel(6, nonce+1, 120, true)
	otherMove := move(7, nonce+1, true)

	for _, tt := range []struct {
		name               string
		seen               []Seen
		wantKey, wantMouse bool
		before, canaries   int
		ok                 bool
	}{
		{"all there", seen(ctrl, notch, keyCanary, moveUp, moveBack), true, true, 2, 3, true},
		{"one move of the mouse canary", seen(ctrl, notch, keyCanary, moveUp), true, true, 2, 2, false},
		{"mouse canary missing", seen(ctrl, notch, keyCanary), true, true, 2, 1, false},
		{"keyboard canary missing", seen(ctrl, notch, moveUp, moveBack), true, true, 2, 2, false},
		{"a mouse batch needs the mouse canary only", seen(notch, moveUp, moveBack), false, true, 1, 2, true},
		{"a mouse batch, one move of the canary", seen(notch, moveUp), false, true, 1, 1, false},
		{"the keyboard canary does not stand for the mouse one", seen(notch, keyCanary), false, true, 1, 1, false},
		{"a keyboard batch needs the keyboard canary only", seen(ctrl, keyCanary), true, false, 1, 1, true},
		{"the events of another nonce are not the batch", seen(ctrl, otherNotch, keyCanary), true, false, 1, 1, true},
		{"the moves of another nonce are not the canary", seen(notch, moveUp, otherMove), false, true, 1, 1, false},
		{"nothing yet", nil, true, false, 0, 0, false},
	} {
		before, canaries, ok := fenceOf(tt.seen, nonce, tt.wantKey, tt.wantMouse)
		if len(before) != tt.before || len(canaries) != tt.canaries || ok != tt.ok {
			t.Errorf("%s: fenceOf = %d events, %d canaries, ok %v; want %d, %d, %v",
				tt.name, len(before), len(canaries), ok, tt.before, tt.canaries, tt.ok)
		}
	}
}

func chordEvent(t *testing.T, spec string, up bool) output.KeyEvent {
	t.Helper()
	e, err := output.KeyEventFor(keys.MustParse(spec).Key(), up)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestPlanBatch(t *testing.T) {
	ev := func(spec string, up bool) output.KeyEvent { return chordEvent(t, spec, up) }
	for _, tt := range []struct {
		name               string
		events             []output.KeyEvent
		wantKey, wantMouse bool
		injected           int
		tail               []output.KeyEvent // the events behind the batch's own
	}{
		{"keyboard", []output.KeyEvent{ev("Z", false), ev("Z", true)}, true, false, 2,
			[]output.KeyEvent{CanaryEvent()}},
		{"a wheel key", []output.KeyEvent{ev("WheelUp", false), ev("WheelUp", true)}, false, true, 1,
			MouseCanaryEvents()},
		{"a wheel chord", []output.KeyEvent{ev("LCtrl", false), ev("WheelUp", false), ev("WheelUp", true), ev("LCtrl", true)}, true, true, 3,
			slices.Concat([]output.KeyEvent{CanaryEvent()}, MouseCanaryEvents())},
		{"two notches", []output.KeyEvent{ev("WheelDown", false), ev("WheelDown", false), ev("WheelDown", true)}, false, true, 2,
			MouseCanaryEvents()},
		{"nothing", nil, true, false, 0, []output.KeyEvent{CanaryEvent()}},
	} {
		p := tt.events
		plan := planBatch(p)
		if plan.wantKey != tt.wantKey || plan.wantMouse != tt.wantMouse || plan.injected != tt.injected {
			t.Errorf("%s: plan = key %v, mouse %v, injected %d; want %v, %v, %d",
				tt.name, plan.wantKey, plan.wantMouse, plan.injected, tt.wantKey, tt.wantMouse, tt.injected)
		}
		if len(plan.batch) != len(p)+len(tt.tail) {
			t.Fatalf("%s: batch has %d events, want %d", tt.name, len(plan.batch), len(p)+len(tt.tail))
		}
		for i, e := range p {
			if plan.batch[i] != e {
				t.Errorf("%s: batch event %d = %v, want %v", tt.name, i, plan.batch[i], e)
			}
		}
		for i, e := range tt.tail {
			if got := plan.batch[len(p)+i]; got != e {
				t.Errorf("%s: canary %d = %v, want %v", tt.name, i, got, e)
			}
		}
	}
}

// A mouse event must never be injected without the mouse hook proven live:
// InjectFenced refuses a batch with one, and InjectFencedMixed refuses it until
// MouseCanary has run. Both stop before SendInput. (The events are the moves
// of the mouse canary, +1 and -1, so that a failure of this very check could
// not scroll or click anything and would leave the cursor where it was.)
func TestInjectFencedRefusesMouseEvents(t *testing.T) {
	const base = mouseTestBase
	owns := func(nonce uint32) bool { return Owns(base, nonce) }
	canary := MouseCanaryEvents()

	for _, tt := range []struct {
		name string
		run  func(f *fakeTB, r *Recorder)
		want string
	}{
		{"InjectFenced", func(f *fakeTB, r *Recorder) { r.InjectFenced(f, base|1, canary) },
			"InjectFenced is for keyboard events"},
		{"InjectFencedMixed", func(f *fakeTB, r *Recorder) { r.InjectFencedMixed(f, base|1, canary) },
			"call MouseCanary first"},
		{"InjectFencedMixed, a chord", func(f *fakeTB, r *Recorder) {
			// Keyboard canaries, not real keys: the release of a key that is not
			// down does nothing even if a regressed guard let it through.
			r.InjectFencedMixed(f, base|1, slices.Concat([]output.KeyEvent{CanaryEvent()}, canary, []output.KeyEvent{CanaryEvent()}))
		}, "call MouseCanary first"},
		{"InjectFencedMixed, a nonce it does not own", func(f *fakeTB, r *Recorder) {
			r.InjectFencedMixed(f, base+0x10000|1, canary)
		}, "is not owned by the recorder"},
		{"MouseCanary, a nonce it does not own", func(f *fakeTB, r *Recorder) { r.MouseCanary(f, base+0x10000|1) },
			"is not owned by the recorder"},
	} {
		f := &fakeTB{TB: t}
		r := newRecorder(owns)
		failures := f.run(func() { tt.run(f, r) })
		if len(failures) != 1 || !strings.Contains(failures[0], tt.want) {
			t.Errorf("%s: failures %q, want one containing %q", tt.name, failures, tt.want)
		}
		if n := r.OwnCount(); n != 0 {
			t.Errorf("%s: the recorder saw %d events: something was injected", tt.name, n)
		}
	}
}

// button is an injected button event of msg with nonce, whose mouseData has
// xbutton in the high word, as a mouse hook sees it.
func button(seq uint64, nonce uint32, msg uint32, xbutton uint32, swallowed bool) winhook.MouseEvent {
	return winhook.MouseEvent{
		Msg: msg, MouseData: xbutton << 16, Flags: winhook.MouseFlagInjected,
		Extra: delivered(nonce), Swallowed: swallowed, Seq: seq,
	}
}

// The hook reports a button by the WM_*BUTTON* message of WinUser.h, and a side
// button by WM_XBUTTONDOWN or WM_XBUTTONUP with its number in the high word of
// mouseData (1 or 2): the expectation of every button test, spelled out here
// with the numbers.
func TestButtonMessage(t *testing.T) {
	for _, tt := range []struct {
		m        keys.Mouse
		down, up uint32
		xbutton  int
	}{
		{keys.MouseLeft, 0x201, 0x202, 0},
		{keys.MouseRight, 0x204, 0x205, 0},
		{keys.MouseMiddle, 0x207, 0x208, 0},
		{keys.MouseX1, 0x20B, 0x20C, 1},
		{keys.MouseX2, 0x20B, 0x20C, 2},
	} {
		if msg, x := ButtonMessage(tt.m, false); msg != tt.down || x != tt.xbutton {
			t.Errorf("ButtonMessage(%v, down) = %#x, %d, want %#x, %d", tt.m, msg, x, tt.down, tt.xbutton)
		}
		if msg, x := ButtonMessage(tt.m, true); msg != tt.up || x != tt.xbutton {
			t.Errorf("ButtonMessage(%v, up) = %#x, %d, want %#x, %d", tt.m, msg, x, tt.up, tt.xbutton)
		}
	}
	for _, m := range []keys.Mouse{keys.MouseNone, keys.WheelUp, keys.WheelRight} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("ButtonMessage(%v) did not panic: a wheel key has no button message", m)
				}
			}()
			ButtonMessage(m, false)
		}()
	}
}

func TestButtonProblems(t *testing.T) {
	const nonce = mouseTestBase | 11
	leftDown := button(1, nonce, winhook.WMLButtonDown, 0, true)
	with := func(e winhook.MouseEvent, f func(e *winhook.MouseEvent)) winhook.MouseEvent { f(&e); return e }
	for _, tt := range []struct {
		name    string
		e       winhook.MouseEvent
		m       keys.Mouse
		up      bool
		swapped bool
		want    string // a part of the one problem, or "" for none
	}{
		{"left down", leftDown, keys.MouseLeft, false, false, ""},
		{"left up", button(1, nonce, winhook.WMLButtonUp, 0, true), keys.MouseLeft, true, false, ""},
		{"right down", button(1, nonce, winhook.WMRButtonDown, 0, true), keys.MouseRight, false, false, ""},
		{"middle up", button(1, nonce, winhook.WMMButtonUp, 0, true), keys.MouseMiddle, true, false, ""},
		{"x1 down", button(1, nonce, winhook.WMXButtonDown, 1, true), keys.MouseX1, false, false, ""},
		{"x2 up", button(1, nonce, winhook.WMXButtonUp, 2, true), keys.MouseX2, true, false, ""},
		{"the tag kept", with(leftDown, func(e *winhook.MouseEvent) { e.Extra = output.ExtraInfo(nonce) }), keys.MouseLeft, false, false, ""},

		{"a press for a release", leftDown, keys.MouseLeft, true, false, "message WM_LBUTTONDOWN"},
		{"another button", button(1, nonce, winhook.WMRButtonDown, 0, true), keys.MouseLeft, false, false, "message WM_RBUTTONDOWN"},
		{"the wrong side button", button(1, nonce, winhook.WMXButtonDown, 2, true), keys.MouseX1, false, false, "side button 2"},
		{"no side button number", button(1, nonce, winhook.WMXButtonDown, 0, true), keys.MouseX2, false, false, "side button 0"},
		{"a wheel", wheel(1, nonce, 120, true), keys.MouseLeft, false, false, "message WM_MOUSEWHEEL"},
		{"not injected", with(leftDown, func(e *winhook.MouseEvent) { e.Flags = 0 }), keys.MouseLeft, false, false, "flags 0x00"},
		{"from a lower integrity level", with(leftDown, func(e *winhook.MouseEvent) { e.Flags |= winhook.MouseFlagLowerILInjected }), keys.MouseLeft, false, false, "flags 0x03"},
		{"another nonce", button(1, nonce+1, winhook.WMLButtonDown, 0, true), keys.MouseLeft, false, false, "want the nonce"},
		{"an unknown high half", with(leftDown, func(e *winhook.MouseEvent) { e.Extra |= 0x1_0000_0000 }), keys.MouseLeft, false, false, "want the nonce"},
		{"not swallowed", button(1, nonce, winhook.WMLButtonDown, 0, false), keys.MouseLeft, false, false, "not swallowed"},

		// With the buttons swapped, what a hook reports for an injected left or
		// right button is not measured: the message of the other one counts.
		{"swapped, left as right", button(1, nonce, winhook.WMRButtonDown, 0, true), keys.MouseLeft, false, true, ""},
		{"swapped, right up as left", button(1, nonce, winhook.WMLButtonUp, 0, true), keys.MouseRight, true, true, ""},
		{"swapped, the message as is", leftDown, keys.MouseLeft, false, true, ""},
		{"swapped, but not a middle", button(1, nonce, winhook.WMLButtonDown, 0, true), keys.MouseMiddle, false, true, "message WM_LBUTTONDOWN"},
		{"swapped, but not a side button", button(1, nonce, winhook.WMLButtonDown, 0, true), keys.MouseX1, false, true, "message WM_LBUTTONDOWN"},
		{"swapped, but still a press for a release", leftDown, keys.MouseRight, true, true, "message WM_LBUTTONDOWN"},
		{"swapped, the rest is as strict", with(button(1, nonce, winhook.WMRButtonDown, 0, false), func(e *winhook.MouseEvent) { e.Flags = 0 }), keys.MouseLeft, false, true, "flags 0x00"},
	} {
		got := buttonProblems(tt.e, tt.m, tt.up, nonce, tt.swapped)
		switch {
		case tt.want == "" && len(got) != 0:
			t.Errorf("%s: problems %q, want none", tt.name, got)
		case tt.want != "" && !strings.Contains(strings.Join(got, "; "), tt.want):
			t.Errorf("%s: problems %q, want one with %q", tt.name, got, tt.want)
		}
	}
}

// A batch must press and release each button it has, in that order and
// alternately, and end with every button up. Anything else could leave a button
// held on the machine the test runs on, or click twice.
func TestUnpairedButton(t *testing.T) {
	ev := func(spec string, up bool) output.KeyEvent { return chordEvent(t, spec, up) }
	for _, tt := range []struct {
		name   string
		events []output.KeyEvent
		want   string // a part of the problem, or "" for none
	}{
		{"nothing", nil, ""},
		{"keyboard only", []output.KeyEvent{ev("Z", false)}, ""},
		{"wheel keys are not held", []output.KeyEvent{ev("WheelUp", false), ev("WheelUp", false), ev("WheelUp", true)}, ""},
		{"a wheel release alone", []output.KeyEvent{ev("WheelDown", true)}, ""},
		{"a click", []output.KeyEvent{ev("MouseLeft", false), ev("MouseLeft", true)}, ""},
		{"two clicks", []output.KeyEvent{ev("MouseRight", false), ev("MouseRight", true), ev("MouseRight", false), ev("MouseRight", true)}, ""},
		{"a chord", []output.KeyEvent{ev("LCtrl", false), ev("MouseX1", false), ev("MouseX1", true), ev("LCtrl", true)}, ""},
		{"two buttons at once", []output.KeyEvent{ev("MouseLeft", false), ev("MouseRight", false), ev("MouseRight", true), ev("MouseLeft", true)}, ""},
		{"two side buttons", []output.KeyEvent{ev("MouseX1", false), ev("MouseX2", false), ev("MouseX1", true), ev("MouseX2", true)}, ""},

		{"a press alone", []output.KeyEvent{ev("MouseLeft", false)}, "MouseLeft is still down"},
		{"a press, then a click of another", []output.KeyEvent{ev("MouseLeft", false), ev("MouseRight", false), ev("MouseRight", true)}, "MouseLeft is still down"},
		{"a side button left down", []output.KeyEvent{ev("MouseX2", false)}, "MouseX2 is still down"},
		{"a chord without its release", []output.KeyEvent{ev("LCtrl", false), ev("MouseMiddle", false), ev("LCtrl", true)}, "MouseMiddle is still down"},
		{"a release alone", []output.KeyEvent{ev("MouseLeft", true)}, "MouseLeft is released with no press"},
		{"a release first", []output.KeyEvent{ev("MouseRight", true), ev("MouseRight", false)}, "MouseRight is released with no press"},
		{"the release of another button", []output.KeyEvent{ev("MouseLeft", false), ev("MouseRight", true), ev("MouseLeft", true)}, "MouseRight is released with no press"},
		{"pressed twice", []output.KeyEvent{ev("MouseLeft", false), ev("MouseLeft", false), ev("MouseLeft", true)}, "MouseLeft is pressed again"},
		{"released twice", []output.KeyEvent{ev("MouseLeft", false), ev("MouseLeft", true), ev("MouseLeft", true)}, "MouseLeft is released with no press"},
	} {
		got := unpairedButton(tt.events)
		switch {
		case tt.want == "" && got != "":
			t.Errorf("%s: unpairedButton = %q, want none", tt.name, got)
		case tt.want != "" && !strings.Contains(got, tt.want):
			t.Errorf("%s: unpairedButton = %q, want one with %q", tt.name, got, tt.want)
		}
		// The plan carries it to InjectFencedMixed.
		if plan := planBatch(tt.events); plan.unpaired != got {
			t.Errorf("%s: planBatch(...).unpaired = %q, want %q", tt.name, plan.unpaired, got)
		}
	}
}

// A button click is a batch like a wheel notch: the events, then the
// canaries, the mouse one last; both events of a click are injected, so the
// hooks must see two.
func TestPlanBatchButtons(t *testing.T) {
	ev := func(spec string, up bool) output.KeyEvent { return chordEvent(t, spec, up) }
	for _, tt := range []struct {
		name               string
		events             []output.KeyEvent
		wantKey, wantMouse bool
		injected           int
		tail               []output.KeyEvent
	}{
		{"a click", []output.KeyEvent{ev("MouseLeft", false), ev("MouseLeft", true)}, false, true, 2,
			MouseCanaryEvents()},
		{"a side button click", []output.KeyEvent{ev("MouseX2", false), ev("MouseX2", true)}, false, true, 2,
			MouseCanaryEvents()},
		{"a chord", []output.KeyEvent{ev("LShift", false), ev("MouseRight", false), ev("MouseRight", true), ev("LShift", true)}, true, true, 4,
			slices.Concat([]output.KeyEvent{CanaryEvent()}, MouseCanaryEvents())},
	} {
		plan := planBatch(tt.events)
		if plan.unpaired != "" || plan.wantKey != tt.wantKey || plan.wantMouse != tt.wantMouse || !plan.hasMouse || plan.injected != tt.injected {
			t.Errorf("%s: plan = unpaired %q, key %v, mouse %v, hasMouse %v, injected %d; want none, %v, %v, true, %d",
				tt.name, plan.unpaired, plan.wantKey, plan.wantMouse, plan.hasMouse, plan.injected, tt.wantKey, tt.wantMouse, tt.injected)
		}
		if want := slices.Concat(tt.events, tt.tail); !slices.Equal(plan.batch, want) {
			t.Errorf("%s: batch = %v, want %v", tt.name, plan.batch, want)
		}
	}
}

// A mouse button is never injected on its own, whatever the mouse hook has
// proven: InjectFencedMixed refuses a batch that leaves a button down, releases
// one that is not down, or presses one twice, before it sends anything. The
// event here is the release of the middle button, which would do little in the
// application it reached if the check failed; the pure check of a press without
// its release is TestUnpairedButton, so that no test here ever hands the
// injection a lone button press. (The mouse canary has not run either, and
// MouseLive stops the batch as well.)
func TestInjectFencedRefusesUnpairedButton(t *testing.T) {
	const base = mouseTestBase
	release := chordEvent(t, "MouseMiddle", true)
	f := &fakeTB{TB: t}
	r := newRecorder(func(nonce uint32) bool { return Owns(base, nonce) })
	failures := f.run(func() { r.InjectFencedMixed(f, base|1, []output.KeyEvent{release}) })
	if len(failures) != 1 || !strings.Contains(failures[0], "pressed and released in the same batch") ||
		!strings.Contains(failures[0], "MouseMiddle is released with no press") {
		t.Errorf("failures %q, want one about the unpaired MouseMiddle release", failures)
	}
	if n := r.OwnCount(); n != 0 {
		t.Errorf("the recorder saw %d events: something was injected", n)
	}
}
