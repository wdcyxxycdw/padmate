package winhook

import (
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"unsafe"
)

// The layout the system hands the mouse hook procedure (WinUser.h) on amd64:
// MSLLHOOKSTRUCT is a POINT, then mouseData, flags, time, and dwExtraInfo
// after 4 bytes of padding.
func TestMSLLHookStructLayout(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skipf("layout pinned for amd64, not %s", runtime.GOARCH)
	}
	var s msllhookstruct
	for _, c := range []struct {
		name      string
		got, want uintptr
	}{
		{"Sizeof(MSLLHOOKSTRUCT)", unsafe.Sizeof(s), 32},
		{"Offsetof(pt.x)", unsafe.Offsetof(s.x), 0},
		{"Offsetof(pt.y)", unsafe.Offsetof(s.y), 4},
		{"Offsetof(mouseData)", unsafe.Offsetof(s.mouseData), 8},
		{"Offsetof(flags)", unsafe.Offsetof(s.flags), 12},
		{"Offsetof(time)", unsafe.Offsetof(s.time), 16},
		{"Offsetof(dwExtraInfo)", unsafe.Offsetof(s.dwExtraInfo), 24},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}

func TestMouseEventOf(t *testing.T) {
	ms := &msllhookstruct{x: -5, y: 700, mouseData: 0xFF880000, flags: MouseFlagInjected, time: 1234, dwExtraInfo: 0x7061646D_00000001}
	e := mouseEventOf(ms, WMMouseWheel, 99)
	want := MouseEvent{
		X: -5, Y: 700, MouseData: 0xFF880000, Flags: MouseFlagInjected, Time: 1234,
		Extra: 0x7061646D_00000001, Msg: WMMouseWheel, Stamp: 99,
	}
	if e != want {
		t.Errorf("mouseEventOf = %+v, want %+v", e, want)
	}
	if !e.Injected() || e.WheelDelta() != -120 {
		t.Errorf("Injected, WheelDelta = %v, %d, want true, -120", e.Injected(), e.WheelDelta())
	}
}

// bareMouseHook returns a Hook that has a mouse hook state but no thread and
// no system hook, so that handleMouse can be tried without installing
// anything.
func bareMouseHook(opts MouseOptions) (*Hook, chan MouseEvent) {
	h := &Hook{events: make(chan Event, 4)}
	ms := &mouseState{watch: opts.Watch, swallow: opts.Swallow, events: make(chan MouseEvent, 4)}
	h.mouse.Store(ms)
	return h, ms.events
}

// An event the Watch predicate rejects passes as if the hook were not there:
// it is not swallowed even if the Swallow predicate would, not delivered, not
// counted, and takes no Seq. The Swallow predicate is not asked.
func TestHandleMouseWatch(t *testing.T) {
	var asked atomic.Int32
	h, events := bareMouseHook(MouseOptions{
		Watch:   func(e MouseEvent) bool { return e.Msg == WMMouseWheel && e.Injected() },
		Swallow: func(MouseEvent) bool { asked.Add(1); return true },
	})
	physicalMove := MouseEvent{Msg: WMMouseMove, X: 10, Y: 20}
	physicalWheel := MouseEvent{Msg: WMMouseWheel, MouseData: 120 << 16}
	injectedMove := MouseEvent{Msg: WMMouseMove, Flags: MouseFlagInjected}
	for _, e := range []MouseEvent{physicalMove, physicalWheel, injectedMove} {
		if h.handleMouse(e) {
			t.Errorf("handleMouse(%+v) swallowed an event the hook is not watching", e)
		}
	}
	if len(events) != 0 || h.MouseDropped() != 0 || h.seq.Load() != 0 || asked.Load() != 0 {
		t.Errorf("unwatched events: %d queued, %d dropped, seq %d, swallow asked %d times; want none of them",
			len(events), h.MouseDropped(), h.seq.Load(), asked.Load())
	}

	notch := MouseEvent{Msg: WMMouseWheel, Flags: MouseFlagInjected, MouseData: 120 << 16, Extra: 7}
	if !h.handleMouse(notch) {
		t.Fatal("handleMouse did not swallow a watched event the predicate swallows")
	}
	got := <-events
	if !got.Swallowed || got.Seq != 1 || got.Extra != 7 || got.WheelDelta() != 120 {
		t.Errorf("queued %+v, want swallowed, seq 1, extra 7, delta 120", got)
	}
}

// A hook shared with the keyboard numbers the events of both in one sequence,
// and a watched event no one swallows is still delivered and passes on.
func TestHandleMouseSeqAndNoSwallow(t *testing.T) {
	h, events := bareMouseHook(MouseOptions{})
	notch := MouseEvent{Msg: WMMouseWheel, Flags: MouseFlagInjected}
	if h.handleMouse(notch) {
		t.Error("a hook without a Swallow predicate swallowed an event")
	}
	if h.handle(Event{VK: 0x41}) {
		t.Error("a hook without a swallow predicate swallowed a key")
	}
	h.handleMouse(notch)
	first, second := <-events, <-events
	key := <-h.events
	if first.Seq != 1 || key.Seq != 2 || second.Seq != 3 {
		t.Errorf("Seq: mouse %d, key %d, mouse %d; want 1, 2, 3", first.Seq, key.Seq, second.Seq)
	}
}

// Close takes the swallowing away, mouse events included, so that a hook
// that cannot be removed lets everything through.
func TestHandleMouseClosing(t *testing.T) {
	h, events := bareMouseHook(MouseOptions{Swallow: func(MouseEvent) bool { return true }, Watch: func(MouseEvent) bool { return true }})
	if !h.handleMouse(MouseEvent{Msg: WMMouseWheel}) {
		t.Fatal("not swallowed before Close")
	}
	h.closing.Store(true)
	if h.handleMouse(MouseEvent{Msg: WMMouseWheel}) {
		t.Error("swallowed after Close began")
	}
	if first, second := <-events, <-events; !first.Swallowed || second.Swallowed {
		t.Errorf("Swallowed = %v, %v; want true, false", first.Swallowed, second.Swallowed)
	}
}

// Without a Watch predicate the hook reports the injected events and leaves
// every physical one alone: the safe default.
func TestHandleMouseDefaultWatch(t *testing.T) {
	h, events := bareMouseHook(MouseOptions{Swallow: func(MouseEvent) bool { return true }})
	for _, e := range []MouseEvent{
		{Msg: WMMouseMove, X: 1, Y: 2},
		{Msg: WMMouseWheel, MouseData: 120 << 16},
		{Msg: WMLButtonDown},
	} {
		if h.handleMouse(e) {
			t.Errorf("handleMouse(%+v) swallowed a physical event", e)
		}
	}
	if len(events) != 0 || h.seq.Load() != 0 {
		t.Errorf("%d physical events queued, seq %d; want none", len(events), h.seq.Load())
	}
	if !h.handleMouse(MouseEvent{Msg: WMMouseMove, Flags: MouseFlagInjected}) || len(events) != 1 {
		t.Error("an injected event was not reported and swallowed")
	}
}

// A hook without a mouse hook state passes everything.
func TestHandleMouseDisabled(t *testing.T) {
	h := &Hook{}
	if h.handleMouse(MouseEvent{Msg: WMMouseWheel, Flags: MouseFlagInjected}) {
		t.Error("a hook without a mouse hook swallowed an event")
	}
	if h.MouseDropped() != 0 {
		t.Error("MouseDropped without a mouse hook is not 0")
	}
}

// The channel never blocks the hook thread: when it is full the event is
// dropped and counted, and still swallowed as the predicate decided.
func TestHandleMouseFull(t *testing.T) {
	h, events := bareMouseHook(MouseOptions{Swallow: func(MouseEvent) bool { return true }, Watch: func(MouseEvent) bool { return true }})
	for range cap(events) + 3 {
		if !h.handleMouse(MouseEvent{Msg: WMMouseWheel}) {
			t.Fatal("a dropped event was not swallowed")
		}
	}
	if n := h.MouseDropped(); n != 3 {
		t.Errorf("MouseDropped() = %d, want 3", n)
	}
}

// EnableMouse refuses before it posts anything to a thread: a stopped hook
// answers ErrStopped, whether or not it had a mouse hook, and a hook that has
// one answers ErrMouseActive.
func TestEnableMouseGuards(t *testing.T) {
	stopped := func() *Hook {
		h := &Hook{done: make(chan struct{})}
		close(h.done)
		return h
	}
	closing := func() *Hook {
		h := &Hook{done: make(chan struct{})}
		h.closing.Store(true)
		return h
	}
	withMouse := func() *Hook {
		h, _ := bareMouseHook(MouseOptions{})
		h.done = make(chan struct{})
		return h
	}
	stoppedWithMouse := func() *Hook {
		h := withMouse()
		close(h.done)
		return h
	}
	for _, tt := range []struct {
		name string
		h    *Hook
		want error
	}{
		{"stopped", stopped(), ErrStopped},
		{"closing", closing(), ErrStopped},
		{"stopped with a mouse hook", stoppedWithMouse(), ErrStopped},
		{"mouse hook installed", withMouse(), ErrMouseActive},
	} {
		events, err := tt.h.EnableMouse(MouseOptions{})
		if !errors.Is(err, tt.want) || events != nil {
			t.Errorf("%s: EnableMouse = %v, %v, want nil, %v", tt.name, events, err, tt.want)
		}
	}
}
