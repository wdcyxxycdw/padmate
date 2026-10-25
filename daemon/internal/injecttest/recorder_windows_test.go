package injecttest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

// TestRecorderNote feeds a Recorder without a hook fabricated events: it
// counts the owned ones, and WaitOwn waits for a count without allocating
// once it is there, wakes as soon as an event comes or the hook stops,
// times out with the count reached, and fails at once and for good after
// an owned event that was not swallowed. It injects nothing and installs
// no hook.
func TestRecorderNote(t *testing.T) {
	const base = 0x5445_0000
	event := func(extra uintptr, swallowed bool) winhook.Event {
		return winhook.Event{VK: 0x7C, Scan: 0x64, Flags: winhook.FlagInjected, Extra: extra, Swallowed: swallowed}
	}
	owned := func(swallowed bool) winhook.Event { return event(output.ExtraInfo(base|1), swallowed) }
	// wakes runs WaitOwn(n, 10s) while another goroutine calls f after 10
	// ms, and fails unless WaitOwn returns want long before its timeout:
	// f woke it.
	wakes := func(r *Recorder, n int, f func(), want error) {
		t.Helper()
		var wg sync.WaitGroup
		wg.Go(func() {
			time.Sleep(10 * time.Millisecond)
			f()
		})
		start := time.Now()
		err := r.WaitOwn(n, 10*time.Second)
		d := time.Since(start)
		wg.Wait()
		if !errors.Is(err, want) {
			t.Errorf("WaitOwn(%d, 10s) = %v, want %v", n, err, want)
		}
		if d > 5*time.Second {
			t.Errorf("WaitOwn(%d, 10s) returned after %v, want as soon as it was woken", n, d)
		}
	}
	r := newRecorder(func(nonce uint32) bool { return Owns(base, nonce) })

	r.note(owned(true))
	r.note(owned(true))
	if n := r.OwnCount(); n != 2 {
		t.Fatalf("OwnCount() = %d after two owned events, want 2", n)
	}
	if err := r.WaitOwn(2, 0); err != nil {
		t.Fatalf("WaitOwn(2, 0) = %v, want nil", err)
	}

	r.note(event(0x1234, false))                           // not padmate's
	r.note(event(output.ExtraInfo(base+0x10000|1), true))  // a nonce of another base
	r.note(event(output.ExtraInfo(base+0x10000|2), false)) // the same, let through
	if n, own := r.OwnCount(), len(r.Own()); n != 2 || own != 2 {
		t.Fatalf("OwnCount() = %d, len(Own()) = %d after three events not owned, want 2 and 2", n, own)
	}
	if err := r.WaitOwn(2, 0); err != nil {
		t.Fatalf("WaitOwn(2, 0) = %v after events not owned were let through, want nil", err)
	}
	if n := testing.AllocsPerRun(100, func() { _ = r.WaitOwn(2, time.Second) }); n != 0 {
		t.Errorf("WaitOwn of a count already reached allocates %v times, want 0", n)
	}

	err := r.WaitOwn(3, 20*time.Millisecond)
	if err == nil || errors.Is(err, ErrUnswallowed) || errors.Is(err, ErrHookStopped) ||
		!strings.Contains(err.Error(), "2 of 3 owned events after 20ms (hook dropped 0)") {
		t.Fatalf("WaitOwn(3, 20ms) = %v, want a timeout with 2 of 3 and hook dropped 0", err)
	}

	wakes(r, 3, func() { r.note(owned(true)) }, nil)
	// The fourth owned event comes, but was let through: that comes first.
	wakes(r, 4, func() { r.note(owned(false)) }, ErrUnswallowed)
	if n := r.OwnCount(); n != 4 {
		t.Fatalf("OwnCount() = %d after an owned event not swallowed, want 4", n)
	}
	start := time.Now()
	if err := r.WaitOwn(5, 10*time.Second); !errors.Is(err, ErrUnswallowed) {
		t.Errorf("WaitOwn(5, 10s) = %v, want ErrUnswallowed", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("WaitOwn(5, 10s) returned ErrUnswallowed after %v, want at once", d)
	}
	if err := r.WaitOwn(1, 0); !errors.Is(err, ErrUnswallowed) {
		t.Errorf("WaitOwn(1, 0) = %v, want ErrUnswallowed for good", err)
	}

	// A hook that stops: what it recorded still counts.
	s := newRecorder(func(nonce uint32) bool { return Owns(base, nonce) })
	s.note(owned(true))
	wakes(s, 2, func() { close(s.done) }, ErrHookStopped)
	if err := s.WaitOwn(1, time.Second); err != nil {
		t.Errorf("WaitOwn(1, 1s) of a stopped hook with one event = %v, want nil", err)
	}
	fresh := newRecorder(func(uint32) bool { return true })
	close(fresh.done)
	if err := fresh.WaitOwn(1, time.Second); !errors.Is(err, ErrHookStopped) {
		t.Errorf("WaitOwn(1, 1s) of a fresh recorder whose hook stopped = %v, want ErrHookStopped", err)
	}
}

// TestWaitOwn fences a real injection with WaitOwn, as a test that injects
// many batches does: after a canary it sends F13's down and up and waits
// for their count, which the hook must reach at once. The hook swallowed
// and stamped both, OwnCount matches Own, and one more times out.
func TestWaitOwn(t *testing.T) {
	Require(t)
	base := NonceBase()
	rec := Start(t, base)
	rec.Canary(t, base)
	want := rec.OwnCount()

	f13, err := output.ChordStroke(keys.MustParse("F13"))
	if err != nil {
		t.Fatal(err)
	}
	ev := slices.Concat(f13.Down, f13.Up)
	s := output.SendInput{Nonce: base | 1}
	if n, err := s.Send(ev); n < len(ev) {
		if n > 0 {
			if m, err2 := s.Send(ev[n:]); m < len(ev)-n {
				t.Fatalf("SendInput injected %d of %d events (%v), and %d of the other %d (%v): F13 may be held",
					n, len(ev), err, m, len(ev)-n, err2)
			}
		}
		t.Fatalf("SendInput injected %d of %d events: %v", n, len(ev), err)
	}
	want += len(ev)
	start := time.Now()
	if err := rec.WaitOwn(want, 10*time.Second); err != nil {
		t.Fatalf("WaitOwn(%d, 10s) after F13's down and up = %v; injecting nothing else", want, err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("WaitOwn(%d, 10s) returned after %v, want as soon as the hook saw the events", want, d)
	}

	own := rec.Own()
	if n := rec.OwnCount(); n != want || len(own) != want {
		t.Fatalf("OwnCount() = %d, len(Own()) = %d, want %d for both: the canary, F13 down and up", n, len(own), want)
	}
	for i, e := range own[1:] {
		if e.VK != 0x7C || e.Up() != (i == 1) || !e.Swallowed {
			t.Errorf("owned event %d = {vk=0x%02X up=%v swallowed=%v}, want F13 (vk 0x7C) %s, swallowed",
				i+1, e.VK, e.Up(), e.Swallowed, []string{"down", "up"}[i])
		}
	}
	for i, e := range own {
		if e.Stamp == 0 {
			t.Errorf("owned event %d (vk=0x%02X) has no stamp", i, e.VK)
		}
	}
	if err := rec.WaitOwn(want, 0); err != nil {
		t.Errorf("WaitOwn(%d, 0) = %v, want nil", want, err)
	}
	if err := rec.WaitOwn(want+1, 50*time.Millisecond); err == nil ||
		errors.Is(err, ErrUnswallowed) || errors.Is(err, ErrHookStopped) ||
		!strings.Contains(err.Error(), fmt.Sprintf("%d of %d owned events", want, want+1)) {
		t.Errorf("WaitOwn(%d, 50ms) = %v, want a timeout with %d of %d", want+1, err, want, want+1)
	}
}
