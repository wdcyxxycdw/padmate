package injecttest

import (
	"cmp"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

// The mouse side of the Recorder (ADR-0062). A test that injects a mouse
// event installs the mouse hook on the Recorder's hook thread first, and
// proves it live with a mouse canary, as the keyboard hook is proven with
// the keyboard one. A mouse event that got past the hook would scroll or
// click in the window under the cursor of the machine the test runs on.
//
// Only events that padmate's tests injected are of interest to the hook. Every
// other mouse event, the physical mouse's moves above all, is passed on at
// once on the hook thread, neither queued nor swallowed.
//
// The tag of a mouse event. A mouse hook does not get the 64 bits of
// dwExtraInfo that the injector passed, only the low 32 (measured 2026-10-19,
// Windows 11 x64): of output.ExtraInfo(nonce) it sees the nonce, without the
// "padm" tag. So a mouse event belongs to a test when it is injected and the
// high half of its extra is 0 (what Windows delivers) or the whole tag (in
// case some Windows keeps it), and its low half, the nonce, is one the
// Recorder owns (output.MayBePadmateMouse). A zero high half is no proof of
// padmate: that is why NonceBase keeps the nonces clear of the values other
// injectors use.

// MouseCanaryLen is the number of events of the mouse canary: a move of +1 and
// one of -1.
const MouseCanaryLen = 2

// IsMouseCanary reports whether e is one of the two moves of the mouse canary
// with nonce: a WM_MOUSEMOVE, injected, with the nonce. The hook does not tell
// the +1 from the -1, nor a move of the canary from any other move, so the
// canary is complete when MouseCanaryLen of them were seen, and the nonce of a
// fence is used for nothing else: the pointer moves of a test (InjectFencedMoves)
// and of padmated carry nonces of their own.
func IsMouseCanary(e winhook.MouseEvent, nonce uint32) bool {
	return e.Msg == winhook.WMMouseMove && e.Injected() &&
		output.MayBePadmateMouse(e.Extra) && output.Nonce(e.Extra) == nonce
}

// MouseCanaryEvents returns the events a mouse canary injects: a relative
// move of +1 along x, then one of -1. A mouse hook sees them (a wheel event or
// a move of no distance never reaches one, measured 2026-10-19), and if they
// ever got past the hook the cursor would move one pixel and back at once.
// Both go out in one SendInput call, tagged like the events of the test, and
// the hook must swallow both.
func MouseCanaryEvents() []output.KeyEvent {
	return []output.KeyEvent{
		{Mouse: keys.MouseMove, Dx: 1},
		{Mouse: keys.MouseMove, Dx: -1},
	}
}

// SendMouseCanary injects the mouse canary with nonce. It does not wait for a
// hook to see it: use Recorder.MouseCanary for that.
func SendMouseCanary(t testing.TB, nonce uint32) {
	t.Helper()
	events := MouseCanaryEvents()
	s := output.SendInput{Nonce: nonce}
	if n, err := s.Send(events); n != len(events) {
		if n > 0 {
			// Only the +1 went out: take the cursor back.
			_, _ = s.Send(events[n:])
		}
		t.Fatalf("injecting the mouse canary (%d of %d moves): %v", n, len(events), err)
	}
}

// The mouse buttons (ADR-0062, second batch). A button, unlike a wheel notch,
// is held: a press that was not released stays down on the machine the test
// runs on. So a test never injects one on its own. Every press goes out with
// its release in the same batch (InjectFencedMixed refuses any other, see
// unpairedButton), the mouse canary behind them, and the hook swallows both;
// a click that got past the hook would at least be over, not a button left
// down.

// ButtonMessage returns the window message a mouse hook reports for the press
// of the mouse button m, or with up for its release, and the number of the
// side button that the high word of mouseData carries in it: 1 for MouseX1, 2
// for MouseX2 and 0 for the others, whose mouseData is not used. The values
// are WinUser.h's (WM_LBUTTONDOWN 0x201 and so on); they are not measured
// until `task test:inject` has run them. It panics for m that is not a
// button.
func ButtonMessage(m keys.Mouse, up bool) (msg uint32, xbutton int) {
	pick := func(down, release uint32) uint32 {
		if up {
			return release
		}
		return down
	}
	switch m {
	case keys.MouseLeft:
		return pick(winhook.WMLButtonDown, winhook.WMLButtonUp), 0
	case keys.MouseRight:
		return pick(winhook.WMRButtonDown, winhook.WMRButtonUp), 0
	case keys.MouseMiddle:
		return pick(winhook.WMMButtonDown, winhook.WMMButtonUp), 0
	case keys.MouseX1:
		return pick(winhook.WMXButtonDown, winhook.WMXButtonUp), 1
	case keys.MouseX2:
		return pick(winhook.WMXButtonDown, winhook.WMXButtonUp), 2
	}
	panic("injecttest: no mouse button " + m.String())
}

// smSwapButton is SM_SWAPBUTTON: GetSystemMetrics says whether the user has
// swapped the primary and the secondary mouse button.
const smSwapButton = 23

// ButtonsSwapped reports whether the left and right mouse buttons are swapped
// (SM_SWAPBUTTON). Whether the message a mouse hook reports for an injected
// MOUSEEVENTF_LEFTDOWN follows the swap is not measured, so ButtonProblems
// takes the swapped message for MouseLeft and MouseRight as well when it is
// set, and the tests log it.
func ButtonsSwapped() bool { return getSystemMetrics(smSwapButton) != 0 }

// ButtonProblems returns what is wrong with e as the mouse hook's report of
// the injection of the mouse button m (up for its release) by a test with
// nonce, or nil when nothing is. e must be injected, not LLMHF_LOWER_IL_INJECTED,
// carry the nonce (a mouse hook gets no tag; MayBePadmateMouse), have been
// swallowed, and be the message ButtonMessage gives with the side button
// number in the high word of mouseData. With the buttons swapped (SM_SWAPBUTTON)
// the message of the other of left and right is taken for MouseLeft and
// MouseRight too, since what a hook reports is not measured.
func ButtonProblems(e winhook.MouseEvent, m keys.Mouse, up bool, nonce uint32) []string {
	return buttonProblems(e, m, up, nonce, ButtonsSwapped())
}

func buttonProblems(e winhook.MouseEvent, m keys.Mouse, up bool, nonce uint32, swapped bool) []string {
	var problems []string
	wantMsg, wantX := ButtonMessage(m, up)
	msgOK := e.Msg == wantMsg
	if !msgOK && swapped && (m == keys.MouseLeft || m == keys.MouseRight) {
		other := keys.MouseRight
		if m == keys.MouseRight {
			other = keys.MouseLeft
		}
		swappedMsg, _ := ButtonMessage(other, up)
		msgOK = e.Msg == swappedMsg
	}
	if !msgOK {
		problems = append(problems, fmt.Sprintf("message %s (mouseData %#x), want %s", winhook.MouseMsgName(e.Msg), e.MouseData, winhook.MouseMsgName(wantMsg)))
	} else if e.XButton() != wantX {
		problems = append(problems, fmt.Sprintf("side button %d in mouseData %#x, want %d", e.XButton(), e.MouseData, wantX))
	}
	if !e.Injected() || e.LowerILInjected() {
		problems = append(problems, fmt.Sprintf("flags 0x%02X, want LLMHF_INJECTED only", e.Flags))
	}
	if !output.MayBePadmateMouse(e.Extra) || output.Nonce(e.Extra) != nonce {
		problems = append(problems, fmt.Sprintf("extra %#x, want the nonce %#x", uint64(e.Extra), nonce))
	}
	if !e.Swallowed {
		problems = append(problems, "not swallowed")
	}
	return problems
}

// unpairedButton returns what is wrong with the mouse buttons of events when
// they are not each pressed and released, alternately and in that order, ending
// with the release, or "" when nothing is. A batch the Recorder injects must
// leave every button up: a press without its release in the batch is a button
// held on the machine the test runs on if the hook ever lets it through, and a
// press while the button is down is a double click.
func unpairedButton(events []output.KeyEvent) string {
	var down [keys.MouseX2 + 1]bool
	for _, e := range events {
		if !e.Mouse.IsButton() {
			continue
		}
		switch {
		case !e.Up && down[e.Mouse]:
			return fmt.Sprintf("%v is pressed again with no release between", e.Mouse)
		case !e.Up:
			down[e.Mouse] = true
		case !down[e.Mouse]:
			return fmt.Sprintf("%v is released with no press before it in the batch", e.Mouse)
		default:
			down[e.Mouse] = false
		}
	}
	for m := keys.MouseLeft; m <= keys.MouseX2; m++ {
		if down[m] {
			return fmt.Sprintf("%v is still down at the end of the batch", m)
		}
	}
	return ""
}

// mouseOwned reports whether e is an injected mouse event with a nonce r owns
// and so one of its tests': the events the mouse hook reports, and swallows,
// and nothing else. It runs on the hook thread for every mouse event of the
// system, so the cheap test, the injected flag, comes first.
func (r *Recorder) mouseOwned(e winhook.MouseEvent) bool {
	return e.Injected() && output.MayBePadmateMouse(e.Extra) && r.owns(output.Nonce(e.Extra))
}

// mouseWatched is the Watch predicate of the mouse hook. It reports exactly
// what it may swallow: another injector's event has a zero high half too, so
// the nonce is all that tells a test's events from it, and the hook has no
// business seeing the others.
func (r *Recorder) mouseWatched(e winhook.MouseEvent) bool { return r.mouseOwned(e) }

func (r *Recorder) collectMouse(events <-chan winhook.MouseEvent, done chan<- struct{}) {
	defer close(done)
	for e := range events {
		r.noteMouse(e)
	}
}

// noteMouse records e, counts it when it is owned, and wakes a waiter.
func (r *Recorder) noteMouse(e winhook.MouseEvent) {
	r.mu.Lock()
	r.mouseEvents = append(r.mouseEvents, e)
	if r.mouseOwned(e) {
		r.own++
		if !e.Swallowed {
			r.unswallowed++
		}
	}
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// enableMouse installs the mouse hook on the recorder's hook thread, unless
// it is there already.
func (r *Recorder) enableMouse() error {
	r.mu.Lock()
	enabled := r.mouseDone != nil
	r.mu.Unlock()
	if enabled {
		return nil
	}
	events, err := r.hook.EnableMouse(winhook.MouseOptions{Watch: r.mouseWatched, Swallow: r.mouseOwned})
	if err != nil {
		return err
	}
	done := make(chan struct{})
	r.mu.Lock()
	r.mouseDone = done
	r.mu.Unlock()
	go r.collectMouse(events, done)
	return nil
}

// MouseLive reports whether the mouse hook is installed and a mouse canary
// has proven it live (MouseCanary).
func (r *Recorder) MouseLive() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.mouseLive
}

// MouseCanary installs the mouse hook, unless it is installed, injects the
// mouse canary with nonce, which the recorder must own, and stops the test
// unless the hook saw and swallowed both its moves within CanaryTimeout. Call
// it before injecting any real mouse event: InjectFencedMixed refuses a mouse
// event until it has.
func (r *Recorder) MouseCanary(t testing.TB, nonce uint32) {
	t.Helper()
	if !r.owns(nonce) {
		t.Fatalf("mouse canary nonce %#x is not owned by the recorder", nonce)
	}
	if err := r.enableMouse(); err != nil {
		t.Fatalf("installing the mouse hook: %v; injecting nothing", err)
	}
	SendMouseCanary(t, nonce)
	_, canaries, ok := r.waitFences(nonce, false, true, CanaryTimeout)
	if !ok {
		t.Fatalf("the mouse hook did not see the mouse canary within %v; injecting nothing else", CanaryTimeout)
	}
	for _, c := range canaries {
		if !c.Swallowed() {
			t.Fatalf("the mouse hook did not swallow the mouse canary: %v; injecting nothing else", c)
		}
	}
	r.mu.Lock()
	r.mouseLive = true
	r.mu.Unlock()
}

// OwnMouse returns the mouse events seen so far whose nonce the recorder
// owns. The mouse hook reports no others.
func (r *Recorder) OwnMouse() []winhook.MouseEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var own []winhook.MouseEvent
	for _, e := range r.mouseEvents {
		if r.mouseOwned(e) {
			own = append(own, e)
		}
	}
	return own
}

// Seen is an event one of a Recorder's hooks saw: a keyboard event or a mouse
// event, exactly one of Keyboard and Mouse set.
type Seen struct {
	Keyboard *winhook.Event
	Mouse    *winhook.MouseEvent
}

// Seq is the event's place in the order the hook thread saw the events of
// both hooks.
func (s Seen) Seq() uint64 {
	if s.Mouse != nil {
		return s.Mouse.Seq
	}
	return s.Keyboard.Seq
}

// Swallowed reports whether the hook swallowed the event.
func (s Seen) Swallowed() bool {
	if s.Mouse != nil {
		return s.Mouse.Swallowed
	}
	return s.Keyboard.Swallowed
}

// Extra is the event's dwExtraInfo.
func (s Seen) Extra() uintptr {
	if s.Mouse != nil {
		return s.Mouse.Extra
	}
	return s.Keyboard.Extra
}

// IsCanary reports whether the event is the keyboard canary or one of the moves
// of the mouse canary with nonce.
func (s Seen) IsCanary(nonce uint32) bool {
	if s.Mouse != nil {
		return IsMouseCanary(*s.Mouse, nonce)
	}
	return IsCanary(*s.Keyboard, nonce)
}

func (s Seen) String() string {
	if s.Mouse != nil {
		e := s.Mouse
		return fmt.Sprintf("{mouse %s delta=%d data=%#x flags=0x%02X extra=%#x swallowed=%v seq=%d}",
			winhook.MouseMsgName(e.Msg), e.WheelDelta(), e.MouseData, e.Flags, uint64(e.Extra), e.Swallowed, e.Seq)
	}
	e := s.Keyboard
	return fmt.Sprintf("{key vk=0x%02X sc=0x%02X up=%v flags=0x%02X extra=%#x swallowed=%v seq=%d}",
		e.VK, e.Scan, e.Up(), e.Flags, uint64(e.Extra), e.Swallowed, e.Seq)
}

// Keyboard returns the keyboard events of seen, in order.
func Keyboard(seen []Seen) []winhook.Event {
	var out []winhook.Event
	for _, s := range seen {
		if s.Keyboard != nil {
			out = append(out, *s.Keyboard)
		}
	}
	return out
}

// Mouse returns the mouse events of seen, in order.
func Mouse(seen []Seen) []winhook.MouseEvent {
	var out []winhook.MouseEvent
	for _, s := range seen {
		if s.Mouse != nil {
			out = append(out, *s.Mouse)
		}
	}
	return out
}

// OwnAll returns the keyboard and mouse events seen so far whose nonce the
// recorder owns, in the order the hook thread saw them.
func (r *Recorder) OwnAll() []Seen {
	r.mu.Lock()
	defer r.mu.Unlock()
	var all []Seen
	for _, e := range r.events {
		if r.owned(e) {
			all = append(all, Seen{Keyboard: &e})
		}
	}
	for _, e := range r.mouseEvents {
		if r.mouseOwned(e) {
			all = append(all, Seen{Mouse: &e})
		}
	}
	slices.SortStableFunc(all, func(a, b Seen) int { return cmp.Compare(a.Seq(), b.Seq()) })
	return all
}

// fenceOf splits the events with nonce among seen into the canaries and the
// other events, which came before them, and reports whether the canaries the
// fence needs are there: the keyboard canary when wantKey, both moves of the
// mouse canary when wantMouse. A nonce serves one fence: a second canary with
// it would complete the next one early.
func fenceOf(seen []Seen, nonce uint32, wantKey, wantMouse bool) (before, canaries []Seen, ok bool) {
	var haveKey bool
	moves := 0
	for _, s := range seen {
		if output.Nonce(s.Extra()) != nonce {
			continue
		}
		if !s.IsCanary(nonce) {
			before = append(before, s)
			continue
		}
		canaries = append(canaries, s)
		if s.Mouse != nil {
			moves++
		} else {
			haveKey = true
		}
	}
	return before, canaries, (!wantKey || haveKey) && (!wantMouse || moves >= MouseCanaryLen)
}

// waitFences waits up to timeout for the canaries with nonce that a fence
// needs (fenceOf), and returns what fenceOf does. The hook thread sees the
// events in the order they were injected, so once the canaries are there so
// is everything injected before them.
func (r *Recorder) waitFences(nonce uint32, wantKey, wantMouse bool, timeout time.Duration) (before, canaries []Seen, ok bool) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		before, canaries, ok = fenceOf(r.OwnAll(), nonce, wantKey, wantMouse)
		if ok {
			return before, canaries, true
		}
		select {
		case <-r.wake:
		case <-r.done:
			return before, canaries, false
		case <-deadline.C:
			return before, canaries, false
		}
	}
}

// WaitMouseFence is WaitFence for the mouse hook: it waits up to timeout for
// both moves of the mouse canary with nonce, and returns the mouse events with
// nonce the hook saw before them, the last of the two moves, and whether they
// came. The keyboard events of the batch are not among them:
// InjectFencedMixed returns those too.
func (r *Recorder) WaitMouseFence(nonce uint32, timeout time.Duration) (before []winhook.MouseEvent, fence winhook.MouseEvent, ok bool) {
	seen, canaries, ok := r.waitFences(nonce, false, true, timeout)
	if !ok {
		return Mouse(seen), winhook.MouseEvent{}, false
	}
	for _, c := range canaries {
		if c.Mouse != nil {
			fence = *c.Mouse // the last one wins
		}
	}
	return Mouse(seen), fence, true
}

// batchPlan is what InjectFencedMixed sends and expects.
type batchPlan struct {
	batch     []output.KeyEvent // the events and the canaries behind them
	wantKey   bool              // the keyboard canary is in the batch
	wantMouse bool              // the mouse canary is in the batch
	hasMouse  bool              // events has a mouse event
	pointer   bool              // events has a pointer move or jump: InjectFencedMixed refuses it, InjectFencedMoves is for those
	injected  int               // the events the hooks must see: all but the silent ones
	unpaired  string            // why the batch would leave a mouse button down or press it twice (unpairedButton), or ""
}

// planBatch returns the plan for events. The keyboard canary goes in unless
// the batch is mouse events only; the mouse canary, its two moves, goes in
// when it has any. The mouse canary is last: the hook thread sees both kinds
// in the order they were injected, and the last canary says the others went
// before it.
func planBatch(events []output.KeyEvent) batchPlan {
	p := batchPlan{batch: slices.Clone(events), unpaired: unpairedButton(events)}
	hasKey := false
	for _, e := range events {
		if e.Mouse != keys.MouseNone {
			p.hasMouse = true
			p.pointer = p.pointer || e.Pointer()
		} else {
			hasKey = true
		}
		if !e.Silent() {
			p.injected++
		}
	}
	p.wantKey = hasKey || !p.hasMouse
	p.wantMouse = p.hasMouse
	if p.wantKey {
		p.batch = append(p.batch, CanaryEvent())
	}
	if p.wantMouse {
		p.batch = append(p.batch, MouseCanaryEvents()...)
	}
	return p
}

// InjectFencedMixed is InjectFenced for a batch that may have mouse events,
// the press of a wheel key or the press and release of a button, as well as
// keyboard events; pointer moves it refuses, InjectFencedMoves injects those.
// The events go out in one SendInput call with the canaries behind them (a
// keyboard canary unless the batch is of mouse events only, and a mouse
// canary, two moves, when it has any), and it returns what the hooks saw
// before them, keyboard and mouse events together in the order the hook thread
// saw them: exactly the events of the batch. The release of a wheel key injects
// nothing and so is not among them.
//
// A batch with a mouse event needs MouseCanary first, and one with a Unicode
// press needs UnicodeCanary; the test stops without injecting anything otherwise. It stops as InjectFenced does, and also if a
// canary the batch needs does not come. A mouse button is held, unlike a wheel
// notch, so a batch must press each button it has and release it, in that
// order, and end with it up (unpairedButton): the test stops without injecting
// anything for a batch that does not.
func (r *Recorder) InjectFencedMixed(t testing.TB, nonce uint32, events []output.KeyEvent) []Seen {
	t.Helper()
	if !r.owns(nonce) {
		t.Fatalf("nonce %#x is not owned by the recorder", nonce)
	}
	p := planBatch(events)
	if p.unpaired != "" {
		t.Fatalf("a mouse button must be pressed and released in the same batch: %s; injecting nothing", p.unpaired)
	}
	if msg := unpairedUnicode(events); msg != "" {
		t.Fatalf("a typed Unicode unit must be pressed and released in the same batch: %s; injecting nothing", msg)
	}
	if msg := r.unicodeRefusal(events); msg != "" {
		t.Fatalf("InjectFencedMixed: %s; injecting nothing", msg)
	}
	if p.hasMouse && !r.MouseLive() {
		t.Fatalf("a batch with a mouse event, and no mouse canary has proven the mouse hook live; call MouseCanary first")
	}
	if p.pointer {
		t.Fatalf("a pointer move cannot go in a batch with the mouse canary: the hook reports both as WM_MOUSEMOVE with the same nonce; use InjectFencedMoves; injecting nothing")
	}
	s := output.SendInput{Nonce: nonce}
	if n, err := s.Send(p.batch); n < len(p.batch) {
		if n > 0 && n < len(events) {
			if m, err2 := s.Send(events[n:]); m < len(events)-n {
				t.Fatalf("SendInput injected %d of %d events (%v), and %d of the other %d (%v): keys or buttons may be held",
					n, len(p.batch), err, m, len(events)-n, err2)
			}
		} else if p.wantMouse && n == len(p.batch)-1 {
			// Only the -1 of the mouse canary is missing: take the cursor back.
			_, _ = s.Send(p.batch[n:])
		}
		t.Fatalf("SendInput injected %d of %d events: %v", n, len(p.batch), err)
	}
	got, canaries, ok := r.waitFences(nonce, p.wantKey, p.wantMouse, CanaryTimeout)
	if !ok {
		t.Fatalf("the hooks did not see the canaries after the events within %v (keyboard %v, mouse %v; events before them: %v); injecting nothing else",
			CanaryTimeout, p.wantKey, p.wantMouse, got)
	}
	for _, c := range canaries {
		if !c.Swallowed() {
			t.Fatalf("a canary got past the hook: %v; injecting nothing else", c)
		}
	}
	for _, e := range got {
		if !e.Swallowed() {
			t.Fatalf("an injected event got past the hook: %v; injecting nothing else", e)
		}
	}
	if len(got) != p.injected {
		t.Fatalf("the hooks saw %d events before the canaries, want %d: %v", len(got), p.injected, got)
	}
	return got
}
