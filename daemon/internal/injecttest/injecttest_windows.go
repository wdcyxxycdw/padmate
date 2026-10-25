package injecttest

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

// EnvVar enables the injection tests when set to "1".
const EnvVar = "PADMATE_TEST_INJECT"

// CanaryVK is the key-up the canary injects: 0x97 is unassigned in
// WinUser.h, and a release of a key that is not down does nothing even if
// it gets through.
const CanaryVK = 0x97

// CanaryTimeout bounds the wait for a canary.
const CanaryTimeout = 3 * time.Second

// Require skips t unless EnvVar is "1".
func Require(t testing.TB) {
	t.Helper()
	if os.Getenv(EnvVar) != "1" {
		t.Skipf("injects real key events; set %s=1 to run (they are swallowed by a hook)", EnvVar)
	}
}

// NonceBase returns a random nonce base with the low 16 bits clear. A
// test owns the nonces base through base+0xFFFF.
//
// It is never a base whose nonces other injectors use as the whole of
// their dwExtraInfo (nonceBaseClash): a mouse hook sees nothing but the
// nonce of a mouse event, not the tag, so the nonce alone must tell a test's
// event from theirs. Callers that set the top bit, as those that have to stay
// clear of process ids do, get the same guarantee.
func NonceBase() uint32 {
	for {
		if base := rand.Uint32() &^ 0xFFFF; !nonceBaseClash(base) {
			return base
		}
	}
}

// nonceBaseClash reports whether the nonces of base, with or without the top
// bit, include values that other injectors send as dwExtraInfo, which a mouse
// hook sees whole. They are: 0, which nearly every injector sends, with the
// other small ids (0 through 0xFFFF); 0xFF5157xx, the signature Windows gives
// the mouse events it makes from a pen or touch input (MI_WP_SIGNATURE,
// 0xFF515700 under the mask 0xFFFFFF00); and 0xFFFFFFFF, which is -1.
func nonceBaseClash(base uint32) bool {
	switch base | 0x80000000 {
	case 0x80000000, 0xFF510000, 0xFFFF0000:
		return true
	}
	return false
}

// Owns reports whether nonce lies in the range of base.
func Owns(base, nonce uint32) bool {
	return nonce&^0xFFFF == base
}

// CanaryEvent is the event a canary injects.
func CanaryEvent() output.KeyEvent {
	return output.KeyEvent{VK: CanaryVK, ByVK: true, Up: true}
}

// IsCanary reports whether e is the canary with nonce.
func IsCanary(e winhook.Event, nonce uint32) bool {
	return e.VK == CanaryVK && e.Up() && output.IsPadmate(e.Extra) && output.Nonce(e.Extra) == nonce
}

// SendCanary injects the canary with nonce. It does not wait for a hook
// to see it: use Recorder.Canary for that, or watch the hook of another
// process.
func SendCanary(t testing.TB, nonce uint32) {
	t.Helper()
	if n, err := (output.SendInput{Nonce: nonce}).Send([]output.KeyEvent{CanaryEvent()}); n != 1 {
		t.Fatalf("injecting the canary: %v", err)
	}
}

// ErrUnswallowed means the recorder saw an owned event that the hook did
// not swallow: stop injecting.
var ErrUnswallowed = errors.New("injecttest: an injected event was not swallowed")

// ErrHookStopped means the hook ended before the events arrived.
var ErrHookStopped = errors.New("injecttest: the hook stopped")

// Recorder is a hook that swallows the padmate events whose nonce it owns
// and records every event it sees.
//
// It watches the keyboard from the start. It watches the mouse only once a
// test asks with MouseCanary, for the injected events with a nonce it owns
// (the mouse hook sees no tag, only the nonce), and records those alone: the
// physical mouse is left alone. Its
// events are in the same counts as the keyboard's, in the same order (Seq),
// but in lists of their own, so that Own is still the keyboard events.
//
// Its waits (WaitOwn, WaitFence, and so Canary and InjectFenced) share one
// wake-up: call them from one goroutine at a time.
type Recorder struct {
	owns func(nonce uint32) bool
	hook *winhook.Hook // nil only in TestRecorderNote

	mu          sync.Mutex
	events      []winhook.Event
	mouseEvents []winhook.MouseEvent
	own         int           // owned events in events and mouseEvents
	unswallowed int           // owned events in either that the hook let through
	wake        chan struct{} // signaled, without blocking, after each event
	done        chan struct{} // closed when the hook's Events channel is drained
	mouseDone   chan struct{} // closed when the mouse channel is drained; nil before the mouse hook is installed
	mouseLive   bool          // a mouse canary has proven the mouse hook live
	unicodeLive bool          // a Unicode canary has proven the hook is handed the tag of a Unicode event

	closeOnce sync.Once
}

// Start installs a Recorder that owns the nonces of base. It is closed
// when t ends; see Close.
func Start(t testing.TB, base uint32) *Recorder {
	t.Helper()
	return StartFunc(t, func(nonce uint32) bool { return Owns(base, nonce) })
}

// StartFunc installs a Recorder that owns the nonces for which owns
// returns true. owns runs on the hook thread, and on the goroutines that
// record and read events, and must be fast. It is closed when t ends; see
// Close.
func StartFunc(t testing.TB, owns func(nonce uint32) bool) *Recorder {
	t.Helper()
	r := newRecorder(owns)
	h, err := winhook.Start(r.owned)
	if err != nil {
		t.Fatalf("installing the swallowing hook: %v", err)
	}
	r.hook = h
	go r.collect()
	t.Cleanup(func() { r.Close(t) })
	return r
}

// newRecorder returns a Recorder without a hook. StartFunc then installs
// one; TestRecorderNote feeds it events with note instead.
func newRecorder(owns func(nonce uint32) bool) *Recorder {
	return &Recorder{owns: owns, wake: make(chan struct{}, 1), done: make(chan struct{})}
}

// owned reports whether e is a padmate event whose nonce r owns.
func (r *Recorder) owned(e winhook.Event) bool {
	return output.IsPadmate(e.Extra) && r.owns(output.Nonce(e.Extra))
}

func (r *Recorder) collect() {
	defer close(r.done)
	for e := range r.hook.Events() {
		r.note(e)
	}
}

// note records e, counts it when it is owned, and wakes a waiter.
func (r *Recorder) note(e winhook.Event) {
	r.mu.Lock()
	r.events = append(r.events, e)
	if r.owned(e) {
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

// Close removes the hook, the mouse hook with it, and waits until every
// event they saw is recorded, so that Own and OwnMouse are complete
// afterwards. It fails t if an owned event got past a hook or if a hook
// dropped events. Later calls do nothing.
func (r *Recorder) Close(t testing.TB) {
	t.Helper()
	r.closeOnce.Do(func() {
		if err := r.hook.Close(); err != nil {
			// The hook swallows nothing any more, and its events are not
			// drained.
			t.Errorf("closing the swallowing hook: %v", err)
			return
		}
		<-r.done
		r.mu.Lock()
		mouseDone := r.mouseDone
		r.mu.Unlock()
		if mouseDone != nil {
			<-mouseDone
		}
		for _, e := range r.Own() {
			if !e.Swallowed {
				t.Errorf("an injected event got past the hook: vk=0x%02X sc=0x%02X flags=0x%02X nonce=%#x",
					e.VK, e.Scan, e.Flags, output.Nonce(e.Extra))
			}
		}
		for _, e := range r.OwnMouse() {
			if !e.Swallowed {
				t.Errorf("an injected mouse event got past the hook: %s data=%#x flags=0x%02X nonce=%#x",
					winhook.MouseMsgName(e.Msg), e.MouseData, e.Flags, output.Nonce(e.Extra))
			}
		}
		if n := r.hook.Dropped(); n > 0 {
			t.Errorf("the hook dropped %d events", n)
		}
		if n := r.hook.MouseDropped(); n > 0 {
			t.Errorf("the mouse hook dropped %d events", n)
		}
	})
}

// Own returns the keyboard events seen so far whose nonce the recorder owns.
// The mouse events are OwnMouse's, and both are OwnAll's.
func (r *Recorder) Own() []winhook.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var own []winhook.Event
	for _, e := range r.events {
		if r.owned(e) {
			own = append(own, e)
		}
	}
	return own
}

// OwnCount returns how many owned events the recorder has seen, canaries
// included, keyboard and mouse events together. It is O(1).
func (r *Recorder) OwnCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.own
}

// WaitOwn waits up to timeout until the recorder has seen n owned events,
// keyboard and mouse events together. It returns ErrUnswallowed as soon as
// any owned event so far was not swallowed (also after n is reached),
// ErrHookStopped once the hook stopped, and on timeout an error with the count reached and the hook's
// Dropped(). A hook that Windows removed, as it does silently with a hook
// that times out, sees no more events: that shows as a timeout.
//
// Unlike WaitFence it is O(1), and it allocates only when it has to wait,
// so a test can fence every batch it injects. n is a running total: the
// OwnCount after the first Canary plus the events each Send injected
// since. An OwnCount read after a Send may already count some of its
// events.
func (r *Recorder) WaitOwn(n int, timeout time.Duration) error {
	// The timer allocates, so it is made only when WaitOwn has to wait: a
	// fence whose events the hook has already seen allocates nothing.
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	// Once the hook has stopped or the time is up, the counts are checked
	// once more: an event may have come with it.
	var stopped, expired bool
	for {
		r.mu.Lock()
		own, unswallowed := r.own, r.unswallowed
		r.mu.Unlock()
		switch {
		case unswallowed > 0:
			return ErrUnswallowed
		case own >= n:
			return nil
		case stopped:
			return ErrHookStopped
		case expired:
			return fmt.Errorf("injecttest: %d of %d owned events after %v (hook dropped %d)", own, n, timeout, r.dropped())
		}
		if timer == nil {
			timer = time.NewTimer(timeout)
		}
		select {
		case <-r.wake:
		case <-r.done:
			stopped = true
		case <-timer.C:
			expired = true
		}
	}
}

// dropped returns the hook's Dropped, or 0 without a hook.
func (r *Recorder) dropped() uint64 {
	if r.hook == nil {
		return 0
	}
	return r.hook.Dropped()
}

// WaitFence waits up to timeout for the canary with nonce. It returns the
// events with nonce the hook saw before the canary, the canary, and
// whether the canary came. The hook sees events in the order they were
// injected, so once the canary is there so is everything injected before
// it.
func (r *Recorder) WaitFence(nonce uint32, timeout time.Duration) (before []winhook.Event, fence winhook.Event, ok bool) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		before = before[:0]
		for _, e := range r.Own() {
			if output.Nonce(e.Extra) != nonce {
				continue
			}
			if IsCanary(e, nonce) {
				return before, e, true
			}
			before = append(before, e)
		}
		select {
		case <-r.wake:
		case <-r.done:
			return before, winhook.Event{}, false
		case <-deadline.C:
			return before, winhook.Event{}, false
		}
	}
}

// Canary injects the canary with nonce, which the recorder must own, and
// stops the test unless the hook saw and swallowed it within
// CanaryTimeout. Call it before injecting any real key.
func (r *Recorder) Canary(t testing.TB, nonce uint32) {
	t.Helper()
	if !r.owns(nonce) {
		t.Fatalf("canary nonce %#x is not owned by the recorder", nonce)
	}
	SendCanary(t, nonce)
	if _, fence, ok := r.WaitFence(nonce, CanaryTimeout); !ok || !fence.Swallowed {
		t.Fatalf("the hook did not swallow the canary within %v (seen: %v); injecting nothing else", CanaryTimeout, ok)
	}
}

// InjectFenced injects events with nonce, followed by the canary with
// nonce, in one SendInput batch that other input cannot interleave with,
// and returns the events the hook saw before that canary: exactly the
// events of the batch.
//
// Call Canary, or an earlier InjectFenced, first: the events go out before
// their own canary proves the hook live, so the canary of the previous
// batch is what proves it for this one. events must leave no key held,
// such as a press and a release of every key; if SendInput injects only
// part of them, InjectFenced sends the rest so that no key stays down.
//
// The test stops, and injects nothing else, if SendInput does not inject
// the whole batch, if the canary does not arrive within CanaryTimeout, if
// the hook let an event of the batch through, or if it saw a different
// number of events than were injected.
//
// It is for keyboard events: a batch with a mouse event stops the test before
// anything is injected. InjectFencedMixed injects those, once MouseCanary has
// proven the mouse hook live. A batch with a Unicode press (OUT-21) needs
// UnicodeCanary first, and one that leaves a unit pressed is refused too.
func (r *Recorder) InjectFenced(t testing.TB, nonce uint32, events []output.KeyEvent) []winhook.Event {
	t.Helper()
	if !r.owns(nonce) {
		t.Fatalf("nonce %#x is not owned by the recorder", nonce)
	}
	if slices.ContainsFunc(events, func(e output.KeyEvent) bool { return e.Mouse != keys.MouseNone }) {
		t.Fatalf("InjectFenced is for keyboard events and the batch has a mouse event: use InjectFencedMixed after MouseCanary; injecting nothing")
	}
	if msg := unpairedUnicode(events); msg != "" {
		t.Fatalf("InjectFenced: %s; injecting nothing", msg)
	}
	if msg := r.unicodeRefusal(events); msg != "" {
		t.Fatalf("InjectFenced: %s; injecting nothing", msg)
	}
	s := output.SendInput{Nonce: nonce}
	batch := append(slices.Clone(events), CanaryEvent())
	if n, err := s.Send(batch); n < len(batch) {
		if n > 0 && n < len(events) {
			if m, err2 := s.Send(events[n:]); m < len(events)-n {
				t.Fatalf("SendInput injected %d of %d events (%v), and %d of the other %d (%v): keys may be held",
					n, len(batch), err, m, len(events)-n, err2)
			}
		}
		t.Fatalf("SendInput injected %d of %d events: %v", n, len(batch), err)
	}
	got, fence, ok := r.WaitFence(nonce, CanaryTimeout)
	if !ok || !fence.Swallowed {
		t.Fatalf("the hook did not swallow the canary after the events within %v (seen: %v, events before it: %v); injecting nothing else",
			CanaryTimeout, ok, got)
	}
	for _, e := range got {
		if !e.Swallowed {
			t.Fatalf("an injected event got past the hook: %+v; injecting nothing else", e)
		}
	}
	if len(got) != len(events) {
		t.Fatalf("the hook saw %d events before the canary, want %d: %+v", len(got), len(events), got)
	}
	return got
}

const mapvkVSCToVKEx = 3 // MAPVK_VSC_TO_VK_EX

// ForegroundLayout returns the keyboard layout of the foreground thread.
// SendInput derives the VK of a scan code event from it, not from the
// layout of the injecting thread (measured in M0b: with the injecting
// thread switched to French, sc 0x10 still arrived as the foreground's VK
// 'Q').
func ForegroundLayout() windows.Handle {
	if fg := windows.GetForegroundWindow(); fg != 0 {
		var pid uint32
		if tid, err := windows.GetWindowThreadProcessId(fg, &pid); err == nil && tid != 0 {
			return windows.GetKeyboardLayout(tid)
		}
	}
	return windows.GetKeyboardLayout(0)
}

// ScanVK returns the VK that hkl gives the scan code scan (E0-prefixed
// when ext), as the system does for an injected scan code: the result of
// MapVirtualKeyEx, or 0xFF when the layout has no VK for the code.
func ScanVK(scan uint8, ext bool, hkl windows.Handle) uint32 {
	code := uint32(scan)
	if ext {
		code |= 0xE000
	}
	if r := mapVirtualKeyEx(code, mapvkVSCToVKEx, hkl); r != 0 {
		return r
	}
	return 0xFF
}
