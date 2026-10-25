package supervisor_test

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

const (
	lock    = winsession.Lock
	desktop = winsession.Desktop
)

// suspends records the calls of Config.OnSuspend, as "<was>><now>".
type suspends struct {
	mu    sync.Mutex
	calls []string
}

func (s *suspends) onSuspend(was, now winsession.Reason) {
	s.mu.Lock()
	s.calls = append(s.calls, fmt.Sprintf("%v>%v", was, now))
	s.mu.Unlock()
}

func (s *suspends) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.calls, " ")
}

// checkSuspended checks Status.Suspended and Status.Mapping.
func (h *harness) checkSuspended(want winsession.Reason) {
	h.t.Helper()
	st := h.sup.Status()
	if st.Suspended != want {
		h.t.Errorf("Status.Suspended %v, want %v", st.Suspended, want)
	}
	if got, want := st.Mapping(), !st.Paused && want == 0; got != want {
		h.t.Errorf("Status.Mapping() %v, want %v (%+v)", got, want, st)
	}
}

// checkGate checks the owners that close Output's gate.
func (h *harness) checkGate(want output.Blocker) {
	h.t.Helper()
	if got := h.out.Blocking(); got != want {
		h.t.Errorf("Output.Blocking() %v, want %v", got, want)
	}
}

// checkLog checks that the lines of msg are n, at info level, and the
// attributes of the first.
func (h *harness) checkLog(msg string, n int, want map[string]string) {
	h.t.Helper()
	lines := h.logs.with(msg)
	if len(lines) != n {
		h.t.Errorf("lines %q: %v, want %d", msg, lines, n)
		return
	}
	for _, r := range lines {
		if r.Level != slog.LevelInfo {
			h.t.Errorf("%q at %v, want info level", r.Msg, r.Level)
		}
	}
	if n == 0 {
		return
	}
	for k, v := range want {
		if got := lines[0].Attrs[k]; got != v {
			h.t.Errorf("%q: %s = %q, want %q (all: %v)", msg, k, got, v, lines[0].Attrs)
		}
	}
}

// routerStall holds the router in its log line of the next "device
// connected" once armed: release lets it go.
type routerStall struct {
	armed   atomic.Bool
	held    chan struct{}
	release func()
	gate    chan struct{}
}

func newRouterStall(t *testing.T) *routerStall {
	s := &routerStall{held: make(chan struct{}), gate: make(chan struct{})}
	s.release = sync.OnceFunc(func() { close(s.gate) })
	t.Cleanup(s.release) // before the harness stops Run
	return s
}

func (s *routerStall) logHook(msg string) {
	if msg == "device connected" && s.armed.CompareAndSwap(true, false) {
		s.held <- struct{}{}
		<-s.gate
	}
}

// stall arms the stall, connects info and waits until the router holds.
func (s *routerStall) stall(t *testing.T, h *harness, info *input.DeviceInfo) {
	t.Helper()
	s.armed.Store(true)
	h.send(added(info))
	select {
	case <-s.held:
	case <-time.After(waitLimit):
		t.Fatal("the router did not log the connection")
	}
}

// TestSuspendAPI: a suspension lets go of every key at once, and presses
// then do nothing: the held slot sends none, so the gate drops none. When
// it ends, it presses nothing again: east, held across, does nothing until
// pressed again. Bits outside AllReasons are ignored.
func TestSuspendAPI(t *testing.T) {
	s := &suspends{}
	p := &pauses{}
	h := start(t, opts{onSuspend: s.onSuspend, onPause: p.onPause})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.set(10)
	h.expect("+Space@0")
	h.checkSuspended(0)
	h.sup.Suspend(lock | 0x80)
	h.sync()
	h.expect("+Space@0 -Space@10")
	h.checkSuspended(lock)
	h.checkPaused(false)
	h.checkGate(output.BlockSystem)
	h.send(button(1, "south", true))
	h.send(button(1, "south", false))
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.set(20)
	h.expect("+Space@0 -Space@10")
	h.checkBlocked(0)
	h.sup.Suspend(lock) // the same reasons: nothing to do
	h.sync()
	h.sup.Suspend(0)
	h.sync()
	h.checkSuspended(0)
	h.checkGate(0)
	h.expect("+Space@0 -Space@10")
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0 -Space@10 +Space@20")
	h.send(button(1, "east", false))
	h.sync()
	h.expect("+Space@0 -Space@10 +Space@20 -Space@20")
	if got := s.String(); got != "none>lock lock>none" {
		t.Errorf("OnSuspend calls %q, want none>lock lock>none", got)
	}
	if got := p.String(); got != "" {
		t.Errorf("OnPause calls %q, want none", got)
	}
	h.checkLog("mapping suspended", 1, map[string]string{"reasons": "lock", "paused": "false", "collapsed": ""})
	h.checkLog("mapping unsuspended", 1, map[string]string{"was": "lock", "paused": "false"})
	h.checkLog("mapping suspension changed", 0, nil)
	h.checkLog("mapping paused", 0, nil)
	h.checkNoWarnings()
}

// TestSuspendKeepsUserPause: a suspension that comes and goes during the
// user's pause leaves it on: the slots stay paused and the user's gate
// closed.
func TestSuspendKeepsUserPause(t *testing.T) {
	h := start(t, opts{})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.set(10)
	h.pause(true)
	h.expect("+Space@0 -Space@10")
	h.sup.Suspend(lock)
	h.sync()
	h.checkGate(output.BlockUser | output.BlockSystem)
	h.sup.Suspend(0)
	h.sync()
	h.checkPaused(true)
	h.checkSuspended(0)
	h.checkGate(output.BlockUser)
	// The slot is still paused: its press sends nothing.
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0 -Space@10")
	h.checkBlocked(0)
	h.checkLog("mapping suspended", 1, map[string]string{"reasons": "lock", "paused": "true"})
	h.checkLog("mapping unsuspended", 1, map[string]string{"was": "lock", "paused": "true"})
	h.pause(false)
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0 -Space@10 +Space@10")
	h.send(button(1, "east", false))
	h.sync()
	h.checkNoWarnings()
}

// TestResumeWhileSuspended: the user's resume during a suspension returns
// with Status.Paused false, but the slots stay paused and the system gate
// closed until the suspension ends.
func TestResumeWhileSuspended(t *testing.T) {
	p := &pauses{}
	h := start(t, opts{onPause: p.onPause})
	h.send(added(padA))
	h.sync()
	h.sup.Suspend(lock)
	h.pause(true)
	h.checkGate(output.BlockUser | output.BlockSystem)
	h.pause(false)
	h.checkPaused(false)
	h.checkSuspended(lock)
	h.checkGate(output.BlockSystem)
	h.send(button(1, "east", true))
	h.sync()
	h.expect("")
	h.checkBlocked(0) // the slot is paused: it sent nothing
	h.checkLog("mapping paused", 1, map[string]string{"by": "api", "suspended": "lock"})
	h.checkLog("mapping resumed", 1, map[string]string{"by": "api", "suspended": "lock"})
	if got := p.String(); got != "true/api false/api" {
		t.Errorf("OnPause calls %q, want true/api false/api", got)
	}
	h.sup.Suspend(0)
	h.sync()
	h.checkGate(0)
	h.expect("")
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0")
	h.send(button(1, "east", false))
	h.sync()
	h.checkNoWarnings()
}

// TestSuspendCoalesced: a suspension set and cleared before the router
// looked still cancels the press cycle held across it: the router holds
// every slot for that moment.
func TestSuspendCoalesced(t *testing.T) {
	s := &suspends{}
	h := start(t, opts{onSuspend: s.onSuspend})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.set(10)
	h.expect("+Space@0")
	h.sup.SuspendQuiet(lock)
	h.sup.SuspendQuiet(0)
	h.sync()
	h.expect("+Space@0 -Space@10")
	h.checkSuspended(0)
	h.checkGate(0)
	h.set(20)
	h.expect("+Space@0 -Space@10") // east does nothing until pressed again
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0 -Space@10 +Space@20")
	h.send(button(1, "east", false))
	h.sync()
	h.checkLog("mapping suspended", 1, map[string]string{"reasons": "lock", "paused": "false", "collapsed": "true"})
	h.checkLog("mapping unsuspended", 1, map[string]string{"was": "lock"})
	if got := s.String(); got != "none>lock lock>none" {
		t.Errorf("OnSuspend calls %q, want none>lock lock>none", got)
	}
	h.checkNoWarnings()
}

// TestSuspendGateRouterOpens: only the router opens the system gate, once
// the slots have taken the end of the suspension: a Suspend(0) leaves it
// closed until then.
func TestSuspendGateRouterOpens(t *testing.T) {
	h := start(t, opts{})
	h.send(added(padA))
	h.sync()
	h.sup.SuspendQuiet(lock)
	h.checkGate(output.BlockSystem)
	h.sup.SuspendQuiet(0)
	h.checkGate(output.BlockSystem)
	h.checkSuspended(0) // the router has not looked
	h.sync()
	h.checkGate(0)
	h.checkSuspended(0)
	h.checkLog("mapping suspended", 1, map[string]string{"collapsed": "true"})
}

// TestSuspendGateWaitsBehind: a slot that has not taken the end of the
// suspension keeps the system gate closed for every slot, and an open
// that comes after a newer suspension does nothing.
func TestSuspendGateWaitsBehind(t *testing.T) {
	for _, relock := range []bool{false, true} {
		t.Run(map[bool]string{false: "unlocked", true: "locked again"}[relock], func(t *testing.T) {
			k := &slotHooks{}
			h := start(t, opts{hooks: k, slots: 2})
			h.send(added(padA))
			h.send(added(padB))
			h.sync()
			k.stall(0)
			h.send(button(1, "west", true)) // unbound; the slot stalls on it
			k.waitStalled(t, 0)
			h.sup.Suspend(lock)
			h.waitLog("mapping suspended", 1)
			h.sup.Suspend(0)
			h.waitLog("mapping unsuspended", 1)
			// Slot 1 has taken it; slot 0 has not.
			h.checkGate(output.BlockSystem)
			h.checkSuspended(0)
			if relock {
				h.sup.SuspendQuiet(lock)
			}
			k.unstall(0)
			h.sync()
			if relock {
				// The open of the first suspension's end came too late.
				h.checkGate(output.BlockSystem)
				h.checkSuspended(lock)
				h.sup.Suspend(0)
				h.sync()
			}
			h.checkGate(0)
			h.checkSuspended(0)
			h.send(button(2, "east", true))
			h.sync()
			h.expect("+Space@0")
			h.send(button(2, "east", false))
			h.send(button(1, "west", false))
			h.sync()
			h.checkNoWarnings()
		})
	}
}

// TestSuspendQueuedEvents: the events T-input queued during a suspension
// that the router routes only after it ended do nothing (critique 1): the
// router routes them held before it lets the slots go, or before it
// learned of the suspension, while the gate was closed. Whichever the
// router's select takes first, the wake or the events, so the test goes
// round a few times.
func TestSuspendQueuedEvents(t *testing.T) {
	rs := newRouterStall(t)
	h := start(t, opts{capacity: 8, logHook: rs.logHook})
	h.send(added(padA))
	h.sync()
	for i := range 16 {
		// A gamepad that waits: the router stalls in its connection line.
		rs.stall(t, h, pad(input.DeviceID(10+i), fmt.Sprintf("Pad %d", 10+i), "0a0a"))
		h.sup.Suspend(lock)
		h.events <- button(1, "south", true)
		h.events <- button(1, "south", false)
		h.events <- button(1, "east", true)
		h.sup.Suspend(0)
		rs.gate <- struct{}{}
		h.sync()
		h.checkGate(0)
		h.checkSuspended(0)
		h.expect("")
		if o := h.owners(); len(o) != 0 {
			t.Fatalf("round %d: owners %v, want none", i, o)
		}
		h.send(button(1, "east", false))
		h.sync()
		h.expect("")
	}
	h.checkLog("mapping suspended", 16, nil)
	// The mapping goes on as before.
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0")
	h.send(button(1, "east", false))
	h.sync()
}

// TestSuspendSyncApplies: a Sync after Suspend returns with it applied,
// whichever the router's select takes first, the wake or the Sync.
func TestSuspendSyncApplies(t *testing.T) {
	h := start(t, opts{})
	h.send(added(padA))
	for range 50 {
		h.sup.Suspend(lock)
		h.sync()
		h.checkSuspended(lock)
		h.checkGate(output.BlockSystem)
		h.sup.Suspend(0)
		h.sync()
		h.checkSuspended(0)
		h.checkGate(0)
	}
	h.checkLog("mapping suspended", 50, nil)
	h.checkLog("mapping unsuspended", 50, nil)
}

// TestSuspendReasonsChange: reasons added and cleared while the mapping
// is suspended change the suspension, not the slots: they stay paused
// from the first reason to the end of the last.
func TestSuspendReasonsChange(t *testing.T) {
	s := &suspends{}
	h := start(t, opts{onSuspend: s.onSuspend})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0")
	for i, r := range []winsession.Reason{lock, lock | desktop, desktop} {
		h.sup.Suspend(r)
		h.sync()
		h.checkSuspended(r)
		h.checkGate(output.BlockSystem)
		// The slot is paused: its press sends nothing.
		h.send(button(1, "south", true))
		h.send(button(1, "south", false))
		h.sync()
		h.checkBlocked(0)
		if i == 0 {
			h.expect("+Space@0 -Space@0")
		}
	}
	h.sup.Suspend(0)
	h.sync()
	h.checkSuspended(0)
	h.checkGate(0)
	h.expect("+Space@0 -Space@0")
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0 -Space@0 +Space@0")
	h.send(button(1, "east", false))
	h.sync()
	h.expect("+Space@0 -Space@0 +Space@0 -Space@0")
	if got, want := s.String(), "none>lock lock>lock,desktop lock,desktop>desktop desktop>none"; got != want {
		t.Errorf("OnSuspend calls %q, want %q", got, want)
	}
	h.checkLog("mapping suspended", 1, map[string]string{"reasons": "lock"})
	lines := h.logs.with("mapping suspension changed")
	if len(lines) != 2 {
		t.Fatalf("lines %v, want two changes", lines)
	}
	checkAttrs(t, lines[0], map[string]string{"reasons": "lock,desktop", "was": "lock"})
	checkAttrs(t, lines[1], map[string]string{"reasons": "desktop", "was": "lock,desktop"})
	h.checkLog("mapping unsuspended", 1, map[string]string{"was": "desktop"})
	h.checkNoWarnings()
}

// TestSuspendBeforeRun: a Suspend before Run holds the slots from the
// start: a gamepad that connects then maps nothing until the suspension
// ends.
func TestSuspendBeforeRun(t *testing.T) {
	s := &suspends{}
	h := start(t, opts{onSuspend: s.onSuspend, beforeRun: func(sup *supervisor.Supervisor) {
		sup.Suspend(lock)
	}})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.sync()
	h.checkSuspended(lock)
	h.checkGate(output.BlockSystem)
	h.expect("")
	h.checkBlocked(0) // the slot is paused: it sent nothing
	if got := s.String(); got != "none>lock" {
		t.Errorf("OnSuspend calls %q, want none>lock", got)
	}
	h.sup.Suspend(0)
	h.sync()
	h.expect("")
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0")
	h.send(button(1, "east", false))
	h.sync()
	h.checkNoWarnings()
}

// TestSuspendHandoffAfter: a waiting gamepad's system-quarantined controls
// must not be replayed by a later hand-off or take-over. A resumed release
// followed by a new press enables them. The user's pause alone retains
// its existing hand-off semantics; system suspension is stricter.
func TestSuspendHandoffAfter(t *testing.T) {
	// Pad A holds the slot, confirmed, so the presses of the gamepads that
	// wait take nothing over from it; then each of waiting connects, and
	// Pad B presses east during a lock.
	begin := func(t *testing.T, waiting ...*input.DeviceInfo) *harness {
		h := start(t, opts{})
		h.send(added(padA))
		h.send(button(1, "west", true))
		h.send(button(1, "west", false))
		for _, info := range waiting {
			h.send(added(info))
		}
		h.sup.Suspend(lock)
		h.sync()
		h.send(button(2, "east", true))
		return h
	}
	t.Run("handoff after", func(t *testing.T) {
		h := begin(t, padB)
		h.sup.Suspend(0)
		h.set(10)
		h.expect("")
		h.send(at(10, removed(1)))
		h.sync()
		h.expect("")
		h.send(button(2, "east", false))
		h.send(button(2, "east", true))
		h.sync()
		h.expect("+Space@10")
		h.send(button(2, "east", false))
		h.sync()
		h.expect("+Space@10 -Space@10")
		h.checkNoWarnings()
	})
	t.Run("take-over after", func(t *testing.T) {
		// Pad C waits first: it gets the slot at the hand-off,
		// provisionally, and Pad B's press takes it over, with a Resync
		// to Pad B's state before the press.
		h := begin(t, padC, padB)
		h.sup.Suspend(0)
		h.set(10)
		h.send(at(10, removed(1)))
		h.set(20)
		h.expect("")
		h.send(at(20, button(2, "south", true)))
		h.send(button(2, "south", false))
		h.set(40)
		h.rec.WaitFor(t, "+LCtrl@20 +Z@20 -Z@40 -LCtrl@40")
		h.send(button(2, "east", false))
		h.send(button(2, "east", true))
		h.sync()
		h.expect("+LCtrl@20 +Z@20 -Z@40 -LCtrl@40 +Space@40")
		h.send(button(2, "east", false))
		h.sync()
		h.expect("+LCtrl@20 +Z@20 -Z@40 -LCtrl@40 +Space@40 -Space@40")
		h.checkNoWarnings()
	})
	t.Run("handoff during", func(t *testing.T) {
		h := begin(t, padB)
		h.set(10)
		h.send(at(10, removed(1)))
		h.sync()
		h.sup.Suspend(0)
		h.set(20)
		h.expect("")
		// East does nothing until pressed again.
		h.send(button(2, "east", false))
		h.send(button(2, "east", true))
		h.sync()
		h.expect("+Space@20")
		h.send(button(2, "east", false))
		h.sync()
		h.expect("+Space@20 -Space@20")
		h.checkNoWarnings()
	})
}

// TestSuspendBehind: a slot that is behind when the suspension comes
// learns of it with its Resync; the gate, closed at once, drops the press
// it handled meanwhile, so nothing is held, and the control held across
// does nothing.
func TestSuspendBehind(t *testing.T) {
	k := &slotHooks{}
	h := start(t, opts{hooks: k, buffer: 1})
	h.send(added(padA))
	h.sync()
	k.stall(0)
	h.send(button(1, "west", true)) // unbound; the slot stalls on it
	k.waitStalled(t, 0)
	h.send(button(1, "east", true)) // queued: the channel holds one
	h.sup.Suspend(lock)             // the slot is behind
	h.waitLog("mapping suspended", 1)
	h.expect("")
	k.unstall(0)
	h.statesTicking()
	h.expect("")
	h.checkBlocked(1)
	if o := h.owners(); len(o) != 0 {
		t.Errorf("owners %v, want none", o)
	}
	checkAttrs(t, h.waitLog("slot behind; resyncing", 1)[0], map[string]string{"slot": "0", "skipped": "0"})
	// The slot is paused itself: its next press sends nothing. The slot
	// can fall behind again (the channel holds one), so the Syncs tick and
	// the output is untimed.
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.ticking("Sync", h.sup.Sync)
	h.expect("")
	h.checkBlocked(1)
	h.sup.Suspend(0)
	h.ticking("Sync", h.sup.Sync)
	h.checkGate(0)
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.ticking("Sync", h.sup.Sync)
	if got := untimed(h.rec.Trace()); got != "+Space" {
		t.Errorf("output %s, want +Space", got)
	}
	h.send(button(1, "east", false))
	h.ticking("Sync", h.sup.Sync)
}

// TestUnsuspendBehind: a suspension that came and went while a slot was
// behind still pauses it before its Resync, so the presses the Resync
// brings in stay canceled; the system gate stays closed until the slot
// has caught up.
func TestUnsuspendBehind(t *testing.T) {
	k := &slotHooks{}
	h := start(t, opts{hooks: k, buffer: 1})
	h.send(added(padA))
	h.sync()
	k.stall(0)
	h.send(button(1, "west", true)) // unbound; the slot stalls on it
	k.waitStalled(t, 0)
	h.send(button(1, "west", false)) // queued: the channel holds one
	h.sup.Suspend(lock)
	h.waitLog("mapping suspended", 1)
	h.send(button(1, "east", true)) // skipped: the Resync covers it
	h.sup.Suspend(0)
	h.waitLog("mapping unsuspended", 1)
	h.checkGate(output.BlockSystem)
	k.unstall(0)
	h.statesTicking()
	h.expect("")
	h.checkGate(0)
	// The skipped down and the masked recovery baseline both require Resync.
	checkAttrs(t, h.waitLog("slot behind; resyncing", 1)[0], map[string]string{"slot": "0", "skipped": "2"})
	// East, held across, does nothing until pressed again. The slot can
	// fall behind again, so the Syncs tick.
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.ticking("Sync", h.sup.Sync)
	if got := untimed(h.rec.Trace()); got != "+Space" {
		t.Errorf("output %s, want +Space", got)
	}
	h.send(button(1, "east", false))
	h.ticking("Sync", h.sup.Sync)
}

// TestSuspendRestart: a slot restarted after a panic starts paused while
// the mapping is suspended, whether the suspension came before the panic
// or during the backoff, and maps once it ended during the backoff; the
// system gate waits for the restart.
func TestSuspendRestart(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before bool   // suspended before the panic
		during string // during the backoff: lock, unlock
		held   bool   // after the restart
	}{
		{"suspended before the panic", true, "", true},
		{"suspended during the backoff", false, "lock", true},
		{"unsuspended during the backoff", true, "unlock", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := &slotHooks{}
			h := start(t, opts{hooks: k})
			k.panicOn(0, "south")
			h.send(added(padA))
			if tc.before {
				h.sup.Suspend(lock)
				h.sync()
			}
			h.send(button(1, "south", true)) // the Trace sees it paused or not
			h.waitLog("slot panicked", 1)
			k.disarm()
			h.send(button(1, "south", false))
			for _, step := range strings.Fields(tc.during) {
				switch step {
				case "lock":
					h.sup.Suspend(lock)
					h.waitLog("mapping suspended", 1)
				case "unlock":
					h.sup.Suspend(0)
					h.waitLog("mapping unsuspended", 1)
					h.checkGate(output.BlockSystem) // the slot has not taken it
				default:
					t.Fatalf("step %q", step)
				}
			}
			h.clk.Advance(100 * ms)
			h.sync()
			if got := h.slot0().Restarts; got != 1 {
				t.Errorf("Restarts %d, want 1", got)
			}
			h.send(button(1, "east", true))
			h.sync()
			if tc.held {
				// The fresh slot sent nothing: the gate dropped nothing.
				h.expect("")
				h.checkBlocked(0)
				h.sup.Suspend(0)
				h.sync()
				h.send(button(1, "east", false))
				h.send(button(1, "east", true))
				h.sync()
			}
			h.checkGate(0)
			h.expect("+Space@100")
			h.send(button(1, "east", false))
			h.sync()
			h.expect("+Space@100 -Space@100")
		})
	}
}

// TestSuspendDisabled: a slot enabled again while the mapping is suspended
// starts paused. A press during the suspension stays canceled also when
// the suspension ends before the runner creates the enabled slot, after
// the router delivered both.
func TestSuspendDisabled(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "suspended", true: "pressed and unsuspended before the start"}[late], func(t *testing.T) {
			p := &panicky{}
			p.armed.Store(true)
			// The runner logs "slot enabled" on its own goroutine before it
			// creates the slot: hold it there.
			var hold atomic.Bool
			held, release := make(chan struct{}), make(chan struct{})
			h := start(t, opts{trace: p.trace, logHook: func(msg string) {
				if msg == "slot enabled" && hold.CompareAndSwap(true, false) {
					close(held)
					<-release
				}
			}})
			letGo := sync.OnceFunc(func() { close(release) })
			t.Cleanup(letGo) // before the harness stops Run
			h.send(added(padA))
			for i, backoff := range []time.Duration{100 * ms, 200 * ms, 0} {
				h.send(button(1, "south", true))
				h.waitLog("slot panicked", i+1)
				h.send(button(1, "south", false))
				h.clk.Advance(backoff)
				h.sync()
			}
			h.waitLog("slot disabled", 1)
			h.routerDisabled()
			h.sup.Suspend(lock) // the disabled slot gets nothing
			h.sync()
			p.armed.Store(false)
			hold.Store(late)
			h.send(removed(1))
			h.send(added(padB))
			if late {
				select {
				case <-held:
				case <-time.After(waitLimit):
					t.Fatal("the runner did not enable the slot")
				}
				h.send(button(2, "east", true))
				h.sup.Suspend(0)
				h.waitLog("mapping unsuspended", 1)
				letGo()
			}
			h.waitLog("slot enabled", 1)
			if !late {
				h.send(button(2, "east", true))
			}
			h.sync()
			if got := h.slot0(); got.Disabled || got.ID != 2 {
				t.Errorf("Status %+v, want enabled, Pad B", got)
			}
			h.expect("")
			h.checkBlocked(0)
			if !late {
				h.sup.Suspend(0)
				h.sync()
			}
			h.checkGate(0)
			h.send(button(2, "east", false))
			h.send(button(2, "east", true))
			h.sync()
			h.expect("+Space@300")
			h.send(button(2, "east", false))
			h.sync()
			h.expect("+Space@300 -Space@300")
		})
	}
}

// TestSuspendAfterStop: Suspend after Run and Output ended returns at once
// and changes nothing.
func TestSuspendAfterStop(t *testing.T) {
	h := start(t, opts{})
	h.cancel()
	if err := h.wait(); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if err := h.out.Stop(); err != nil {
		t.Fatalf("output.Loop.Stop: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 10 {
			h.sup.Suspend(lock)
			h.sup.Suspend(0)
		}
	}()
	select {
	case <-done:
	case <-time.After(waitLimit):
		t.Fatal("Suspend did not return")
	}
	h.checkSuspended(0)
}

// TestSuspendNonBlocking: Suspend never waits for the router, and the keys
// are up once it returns, while the router cannot look (critique 4): it
// is held in a log line with east held.
func TestSuspendNonBlocking(t *testing.T) {
	rs := newRouterStall(t)
	h := start(t, opts{logHook: rs.logHook})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0")
	rs.stall(t, h, padB)
	begin := time.Now()
	for i := range 1001 {
		h.sup.Suspend(lock * winsession.Reason(1-i%2)) // lock, none, lock, …
	}
	if d := time.Since(begin); d > 100*ms {
		t.Errorf("1001 Suspend calls took %v, want 100ms at most", d)
	}
	h.rec.WaitFor(t, "+Space@0 -Space@0")
	if o := h.owners(); len(o) != 0 {
		t.Errorf("owners %v, want none", o)
	}
	h.checkSuspended(0) // the router has not looked
	rs.release()
	h.sync()
	h.checkSuspended(lock)
	h.checkGate(output.BlockSystem)
	h.sup.Suspend(0)
	h.sync()
	h.checkGate(0)
}

// TestComboWhileSuspended: the combo toggles the user's pause during a
// suspension without an edge: the slots stay paused, and after the
// suspension ends they stay paused until the combo resumes.
func TestComboWhileSuspended(t *testing.T) {
	combo := input.Event{Device: 1, Kind: input.ComboHeld}
	p := &pauses{}
	h := start(t, opts{onPause: p.onPause})
	h.send(added(padA))
	h.sup.Suspend(lock)
	h.sync()
	h.send(combo)
	h.sync()
	h.checkPaused(true)
	h.checkSuspended(lock)
	h.checkGate(output.BlockUser | output.BlockSystem)
	h.checkLog("mapping paused", 1, map[string]string{"by": "combo", "id": "1", "suspended": "lock"})
	h.sup.Suspend(0)
	h.sync()
	h.checkPaused(true)
	h.checkGate(output.BlockUser)
	h.send(button(1, "east", true))
	h.sync()
	h.expect("")
	h.checkBlocked(0)
	h.checkLog("mapping unsuspended", 1, map[string]string{"paused": "true"})
	h.send(combo)
	h.sync()
	h.checkPaused(false)
	h.checkGate(0)
	h.checkLog("mapping resumed", 1, map[string]string{"by": "combo", "suspended": ""})
	h.expect("") // east, held across, does nothing until pressed again
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0")
	h.send(button(1, "east", false))
	h.sync()
	if got := p.String(); got != "true/combo false/combo" {
		t.Errorf("OnPause calls %q, want true/combo false/combo", got)
	}
	h.checkNoWarnings()
}
