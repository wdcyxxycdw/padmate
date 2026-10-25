package winhook_test

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/injecttest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

// The hook tests install a real system-wide hook, so they are gated like
// the injection tests. TestHookLifecycle and TestHookCloseFails inject
// nothing, and their hooks swallow no real event.

func TestHookLifecycle(t *testing.T) {
	injecttest.Require(t)
	for round := range 2 { // a second Start after Close reuses the callback
		h, err := winhook.Start(nil)
		if err != nil {
			t.Fatalf("round %d: Start: %v", round, err)
		}
		if h2, err := winhook.Start(nil); !errors.Is(err, winhook.ErrActive) || h2 != nil {
			t.Errorf("round %d: second Start = %v, %v, want nil, ErrActive", round, h2, err)
		}
		if err := h.Close(); err != nil {
			t.Fatalf("round %d: Close: %v", round, err)
		}
		if err := h.Close(); err != nil {
			t.Errorf("round %d: second Close: %v", round, err)
		}
		// Events is closed once the hook is gone; physical keys may have
		// arrived before that.
		timeout := time.After(2 * time.Second)
	drain:
		for {
			select {
			case _, ok := <-h.Events():
				if !ok {
					break drain
				}
			case <-timeout:
				t.Fatalf("round %d: Events not closed after Close", round)
			}
		}
	}
}

// TestHookCloseFails makes Close fail to post WM_QUIT and checks that the
// hook then swallows nothing, stays installed, and goes away on a later
// Close. It injects nothing: it feeds the hook procedure's handling an
// event with a tag no real event carries, and its predicate swallows only
// that event.
func TestHookCloseFails(t *testing.T) {
	injecttest.Require(t)
	const tag = 0x5445_5354 // "TEST": no real event carries it
	fake := winhook.Event{VK: injecttest.CanaryVK, Extra: tag}
	h, err := winhook.Start(func(e winhook.Event) bool { return e.Extra == tag })
	if err != nil {
		t.Fatal(err)
	}
	fail := errors.New("test: posting WM_QUIT failed")
	restore := winhook.SetPostQuit(func(uint32) error { return fail })
	t.Cleanup(func() {
		restore()
		if err := h.Close(); err != nil {
			t.Errorf("Close after restoring: %v", err)
		}
	})

	if !h.Handle(fake) {
		t.Fatal("the hook did not swallow the tagged event before Close")
	}
	if err := h.Close(); !errors.Is(err, fail) {
		t.Fatalf("Close = %v, want %v", err, fail)
	}
	if h.Handle(fake) {
		t.Error("the hook still swallows after a failed Close")
	}
	if h2, err := winhook.Start(nil); !errors.Is(err, winhook.ErrActive) {
		t.Errorf("Start after a failed Close = %v, %v, want ErrActive", h2, err)
		if err == nil {
			_ = h2.Close()
		}
	}

	restore()
	if err := h.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Errorf("third Close: %v", err)
	}
	h2, err := winhook.Start(nil)
	if err != nil {
		t.Fatalf("Start after Close: %v", err)
	}
	if err := h2.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestHookInjected injects F13 and RCtrl, which map to the same VK on
// every layout, and checks that the hook reports them as injected with
// their tag and swallows them, and that it stamps every event in the order
// it saw them, those of the batch while the injection ran.
// The injecttest Recorder is a Hook with a predicate for the padmate
// events of its nonces.
func TestHookInjected(t *testing.T) {
	injecttest.Require(t)
	base := injecttest.NonceBase()
	rec := injecttest.Start(t, base)
	rec.Canary(t, base)

	nonce := base + 1
	f13 := output.KeyEvent{Scan: 0x64}
	rctrl := output.KeyEvent{Scan: 0x1D, Ext: true}
	up := func(e output.KeyEvent) output.KeyEvent { e.Up = true; return e }
	before := latency.Now()
	got := rec.InjectFenced(t, nonce, []output.KeyEvent{f13, up(f13), rctrl, up(rctrl)})
	after := latency.Now()
	want := []struct {
		msg, vk, scan, flags uint32
	}{
		{winhook.WMKeyDown, 0x7C, 0x64, winhook.FlagInjected},
		{winhook.WMKeyUp, 0x7C, 0x64, winhook.FlagInjected | winhook.FlagUp},
		{winhook.WMKeyDown, 0xA3, 0x1D, winhook.FlagInjected | winhook.FlagExtended},
		{winhook.WMKeyUp, 0xA3, 0x1D, winhook.FlagInjected | winhook.FlagExtended | winhook.FlagUp},
	}
	for i, w := range want {
		e := got[i]
		if e.Msg != w.msg || e.VK != w.vk || e.Scan != w.scan || e.Flags != w.flags ||
			e.Extra != output.ExtraInfo(nonce) || !e.Swallowed {
			t.Errorf("event %d = {%s vk=0x%02X sc=0x%02X flags=0x%02X extra=%#x swallowed=%v}, want {%s vk=0x%02X sc=0x%02X flags=0x%02X extra=%#x swallowed=true}",
				i, winhook.MsgName(e.Msg), e.VK, e.Scan, e.Flags, e.Extra, e.Swallowed,
				winhook.MsgName(w.msg), w.vk, w.scan, w.flags, output.ExtraInfo(nonce))
		}
	}

	// Every owned event, in the order the hook saw them: the first canary
	// before the injection, the batch and the canary behind it within it.
	var prev latency.Stamp
	for i, e := range rec.Own() {
		inBatch := output.Nonce(e.Extra) == nonce
		switch {
		case e.Stamp == 0:
			t.Errorf("owned event %d (vk=0x%02X) has no stamp", i, e.VK)
		case e.Stamp < prev:
			t.Errorf("owned event %d (vk=0x%02X): stamp %d before the previous event's %d", i, e.VK, e.Stamp, prev)
		case inBatch && (e.Stamp < before || e.Stamp > after):
			t.Errorf("owned event %d (vk=0x%02X): stamp %d outside the injection [%d, %d]", i, e.VK, e.Stamp, before, after)
		case !inBatch && e.Stamp > before:
			t.Errorf("owned event %d (vk=0x%02X): stamp %d after the injection began at %d", i, e.VK, e.Stamp, before)
		}
		prev = e.Stamp
	}
}

// TestHookStackGrowth makes the hook procedure grow the hook thread's
// stack, which moves it, while GetMessageW waits, and checks that the loop
// sees the message GetMessageW returns next. GetMessageW writes it through
// the pointer it was given, so with the MSG on the goroutine stack it
// would land in the freed old stack, and the loop would see the message
// before. It injects only canaries.
func TestHookStackGrowth(t *testing.T) {
	injecttest.Require(t)
	const wmApp = 0x8000 // WM_APP
	type message struct {
		msg    uint32
		wParam uintptr
	}
	got := make(chan message, 16)
	restore := winhook.SetAfterGet(func(msg uint32, wParam uintptr) {
		select {
		case got <- message{msg, wParam}:
		default:
		}
	})
	t.Cleanup(restore) // runs after the Recorder's Close, which StartFunc registers
	base := injecttest.NonceBase()
	var depth atomic.Int32
	rec := injecttest.StartFunc(t, func(nonce uint32) bool {
		grow(int(depth.Load()))
		return injecttest.Owns(base, nonce)
	})
	rec.Canary(t, base)
	for i := range 6 {
		// Each round the hook procedure needs about twice the stack of the
		// round before.
		depth.Store(64 << i)
		rec.Canary(t, base+1+uint32(i))
		if err := winhook.PostActive(wmApp, uintptr(i)); err != nil {
			t.Fatalf("round %d: PostThreadMessageW: %v", i, err)
		}
		select {
		case m := <-got:
			if want := (message{wmApp, uintptr(i)}); m != want {
				t.Fatalf("round %d: the loop saw message %#x with wParam %d, want %#x with %d",
					i, m.msg, m.wParam, want.msg, want.wParam)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("round %d: the loop saw no message", i)
		}
	}
}

// grow uses about n KiB of stack.
//
//go:noinline
func grow(n int) byte {
	var frame [1024]byte
	frame[n%len(frame)] = byte(n)
	if n == 0 {
		return frame[0]
	}
	return grow(n-1) + frame[n%len(frame)]
}

// TestHookMouseLifecycle installs and removes the mouse hook beside the
// keyboard one. It injects nothing: the hook watches no event, so every
// physical mouse event passes to the next hook untouched. The mouse hook
// goes in at most once, comes out with Close, and a hook that has been closed
// takes no new one.
func TestHookMouseLifecycle(t *testing.T) {
	injecttest.Require(t)
	for round := range 2 { // a second hook reuses both callbacks
		h, err := winhook.Start(nil)
		if err != nil {
			t.Fatalf("round %d: Start: %v", round, err)
		}
		if n := h.MouseDropped(); n != 0 {
			t.Errorf("round %d: MouseDropped() = %d before EnableMouse, want 0", round, n)
		}
		events, err := h.EnableMouse(winhook.MouseOptions{Watch: func(winhook.MouseEvent) bool { return false }})
		if err != nil {
			_ = h.Close()
			t.Fatalf("round %d: EnableMouse: %v", round, err)
		}
		if again, err := h.EnableMouse(winhook.MouseOptions{}); !errors.Is(err, winhook.ErrMouseActive) || again != nil {
			t.Errorf("round %d: second EnableMouse = %v, %v, want nil, ErrMouseActive", round, again, err)
		}
		if err := h.Close(); err != nil {
			t.Fatalf("round %d: Close: %v", round, err)
		}
		timeout := time.After(2 * time.Second)
		select {
		case e, ok := <-events:
			if ok {
				t.Errorf("round %d: the hook that watches nothing reported %+v", round, e)
			}
		case <-timeout:
			t.Fatalf("round %d: the mouse channel is not closed after Close", round)
		}
		if again, err := h.EnableMouse(winhook.MouseOptions{}); !errors.Is(err, winhook.ErrStopped) || again != nil {
			t.Errorf("round %d: EnableMouse after Close = %v, %v, want nil, ErrStopped", round, again, err)
		}
	}
}
