package main

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/wdcyxxycdw/padmate/daemon/internal/injecttest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

// TestLiveInjectVirtual is the automatic acceptance of --inject (ADR-0018;
// TestPadToProbe is the end-to-end test of M1b): the presses of a virtual
// gamepad go through live mode with --inject, and a low-level
// hook sees the keys injected by SendInput, tagged with padmated's nonce,
// the process id. The hook, proven live by a canary before padmated
// starts, swallows them all, so nothing reaches the foreground
// application (CLAUDE.md invariant 6); it is removed only after live mode
// has stopped and a canary has fenced its keys, also when the test fails.
//
// The Resolver reads the foreground layout, so the expected keys come
// from a Resolver of the test: Ctrl is the same on every layout, Z need
// not be.
func TestLiveInjectVirtual(t *testing.T) {
	injecttest.Require(t)
	pid := uint32(os.Getpid())
	rec := injecttest.StartFunc(t, func(n uint32) bool { return n == pid })
	rec.Canary(t, pid)
	// Cleanups run last in first: this one after that of startTestLive and
	// before the one that removes the hook.
	var tl *testLive
	fenced := false
	t.Cleanup(func() {
		if !fenced {
			stopFenced(t, rec, pid, tl)
		}
	})

	hkl := injecttest.ForegroundLayout()
	resolve := keyResolver(t)
	undo, space := resolve("Ctrl+Z"), resolve("Space")
	want := slices.Concat(undo, releases(undo), space, releases(space))
	tapped := 2 * len(undo)     // the keys of the tap of Ctrl+Z
	held := tapped + len(space) // and then the presses of Space

	tl = startTestLive(t, nil, "--inject")
	v := tl.attach(t)
	tl.waitFor(t, tl.log, `"msg":"slot assigned"`)
	press(t, v, "south", true)
	press(t, v, "south", false)
	waitKeys(t, rec, pid, tapped)
	press(t, v, "east", true)
	waitKeys(t, rec, pid, held)
	code := stopFenced(t, rec, pid, tl)
	fenced = true
	if code != 0 {
		t.Fatalf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
	}
	got, _ := ownKeys(rec, pid)
	seen := make([]seenKey, len(got))
	for i, e := range got {
		seen[i] = hookKey(e)
	}
	checkKeys(t, "the hook", seen, want, hkl, pid)
}

// keyResolver returns a function that resolves a key spec as padmated
// does, against the foreground layout, to the presses the Arbiter sends.
func keyResolver(t *testing.T) func(spec string) []output.KeyEvent {
	res := output.NewResolver(output.SystemLayouts(), output.ResolverOptions{})
	return func(spec string) []output.KeyEvent {
		t.Helper()
		s, err := res.Resolve(keys.MustParse(spec).Sided())
		if err != nil {
			t.Fatalf("Resolve(%s): %v", spec, err)
		}
		return s.Down
	}
}

// releases returns the releases of the presses down in the order the
// Arbiter sends them: the reverse.
func releases(down []output.KeyEvent) []output.KeyEvent {
	up := make([]output.KeyEvent, len(down))
	for i, e := range down {
		e.Up = true
		up[len(down)-1-i] = e
	}
	return up
}

// seenKey is a key event as a low-level hook saw it: the hook of a
// Recorder in the test process, or that of padmate-probe.
type seenKey struct {
	scan, vk          uint32
	ext, up           bool
	injected, lowerIL bool
	extra             uint64 // dwExtraInfo
	swallowed         bool
}

func (k seenKey) String() string {
	return fmt.Sprintf("sc=0x%02X ext=%v up=%v vk=0x%02X injected=%v lowerIL=%v extra=%#x swallowed=%v",
		k.scan, k.ext, k.up, k.vk, k.injected, k.lowerIL, k.extra, k.swallowed)
}

// hookKey is the seenKey of an event of a Recorder.
func hookKey(e winhook.Event) seenKey {
	return seenKey{
		scan: e.Scan, vk: e.VK, ext: e.Ext(), up: e.Up(),
		injected: e.Injected(), lowerIL: e.LowerILInjected(), extra: uint64(e.Extra), swallowed: e.Swallowed,
	}
}

// probeKey is the seenKey of an event of padmate-probe. An extra that is
// not 0x and up to 16 hex digits becomes 0, which no nonce's tag is.
func probeKey(e injecttest.ProbeEvent) seenKey {
	extra, err := strconv.ParseUint(strings.TrimPrefix(e.Extra, "0x"), 16, 64)
	if err != nil {
		extra = 0
	}
	return seenKey{
		scan: e.Scan, vk: e.VK, ext: e.Ext, up: e.Up,
		injected: e.Injected, lowerIL: e.LowerIL, extra: extra, swallowed: e.Swallowed,
	}
}

// checkKeys checks that who, a hook, saw the events of want as SendInput
// injects them with nonce, with hkl the foreground layout: each with its
// scan code, E0 flag and direction, the VK the layout gives the scan code
// (or the VK of an event sent by VK), LLKHF_INJECTED and not
// LLKHF_LOWER_IL_INJECTED, the tag of nonce, and swallowed; and every
// key released as often as pressed.
func checkKeys(t *testing.T, who string, got []seenKey, want []output.KeyEvent, hkl windows.Handle, nonce uint32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s saw %d keys, want %d: %v", who, len(got), len(want), got)
	}
	var problems []string
	down := map[string]int{}
	for i, ev := range got {
		w := want[i]
		wantVK := uint32(w.VK)
		if !w.ByVK {
			wantVK = injecttest.ScanVK(w.Scan, w.Ext, hkl)
		}
		if ev.scan != uint32(w.Scan) || ev.ext != w.Ext || ev.up != w.Up || ev.vk != wantVK {
			problems = append(problems, fmt.Sprintf("event %d: sc=0x%02X ext=%v up=%v vk=0x%02X, want %v with vk=0x%02X",
				i, ev.scan, ev.ext, ev.up, ev.vk, w, wantVK))
		}
		if !ev.injected || ev.lowerIL || ev.extra != uint64(output.ExtraInfo(nonce)) || !ev.swallowed {
			problems = append(problems, fmt.Sprintf("event %d: injected %v lowerIL %v extra %#x swallowed %v, want LLKHF_INJECTED only, padmated's tag, swallowed",
				i, ev.injected, ev.lowerIL, ev.extra, ev.swallowed))
		}
		key := fmt.Sprintf("sc=0x%02X ext=%v", ev.scan, ev.ext)
		if ev.up {
			down[key]--
		} else {
			down[key]++
		}
	}
	for key, n := range down {
		if n != 0 {
			problems = append(problems, fmt.Sprintf("%s: %d more presses than releases", key, n))
		}
	}
	if len(problems) > 0 {
		t.Errorf("foreground layout %#x:\n%s", hkl, strings.Join(problems, "\n"))
	}
}

// stopLive stops live mode, when tl is not nil, as Ctrl+C does, and
// returns its exit code. When live mode does not stop within liveWait,
// the process exits with the hooks in place rather than let a cleanup
// remove them while padmated may still inject.
func stopLive(t *testing.T, tl *testLive) int {
	t.Helper()
	if tl == nil {
		return 0
	}
	tl.cancel()
	codes := make(chan int, 1)
	go func() { codes <- tl.wait() }()
	select {
	case code := <-codes:
		return code
	case <-time.After(liveWait):
		fmt.Fprintf(os.Stderr, "%s: live mode did not stop within %v; exiting with the hooks in place\nstderr:\n%s", t.Name(), liveWait, tl.stderr)
		os.Exit(1)
		return 1
	}
}

// stopFenced stops live mode, when tl is not nil, and fences the keys it
// injected: a canary sent after it has stopped follows them to the hook,
// so once the canary is there the hook may go. It returns the exit code.
// When live mode does not stop, the process exits with the hook in place
// (stopLive).
func stopFenced(t *testing.T, rec *injecttest.Recorder, pid uint32, tl *testLive) (code int) {
	t.Helper()
	code = stopLive(t, tl)
	_, before := ownKeys(rec, pid)
	injecttest.SendCanary(t, pid)
	deadline := time.Now().Add(injecttest.CanaryTimeout)
	for _, n := ownKeys(rec, pid); n <= before; _, n = ownKeys(rec, pid) {
		if time.Now().After(deadline) {
			t.Fatalf("the hook did not see the canary sent after the stop within %v", injecttest.CanaryTimeout)
		}
		time.Sleep(2 * time.Millisecond)
	}
	return code
}

// ownKeys returns the events with nonce that the recorder saw, other than
// the canaries, and how many canaries it saw.
func ownKeys(rec *injecttest.Recorder, nonce uint32) (evs []winhook.Event, canaries int) {
	for _, e := range rec.Own() {
		switch {
		case output.Nonce(e.Extra) != nonce:
		case injecttest.IsCanary(e, nonce):
			canaries++
		default:
			evs = append(evs, e)
		}
	}
	return evs, canaries
}

// waitKeys waits, at most liveWait, until the recorder has seen n keys
// with nonce.
func waitKeys(t *testing.T, rec *injecttest.Recorder, nonce uint32, n int) {
	t.Helper()
	deadline := time.Now().Add(liveWait)
	for {
		got, _ := ownKeys(rec, nonce)
		if len(got) >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the hook saw %d keys after %v, want %d: %v", len(got), liveWait, n, got)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
