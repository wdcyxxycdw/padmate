package supervisor_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

// reloads records the calls of Config.OnReload, as "<slot>/<gen>".
type reloads struct {
	mu    sync.Mutex
	calls []string
}

func (r *reloads) onReload(slot uint8, gen uint32) {
	r.mu.Lock()
	r.calls = append(r.calls, fmt.Sprintf("%d/%d", slot, gen))
	r.mu.Unlock()
}

func (r *reloads) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.calls, " ")
}

// checkGens checks the generations of the owners of the keys held.
func (h *harness) checkGens(want ...uint32) {
	h.t.Helper()
	owners := h.owners()
	var got []uint32
	for _, o := range owners {
		got = append(got, o.Gen)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		h.t.Errorf("owners %v, want generations %v", owners, want)
	}
}

// TestReloadWhileHeld: a Reload releases what the slot holds, once; the
// control held across it does nothing until pressed again, and then maps
// with the new program and generation.
func TestReloadWhileHeld(t *testing.T) {
	rl := &reloads{}
	h := start(t, opts{onReload: rl.onReload})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.set(50)
	h.expect("+Space@0")
	h.checkGens(1)
	h.reload(compile2(t), 2)
	// Reload returned: the release is out, and Status shows the generation.
	h.expect("+Space@0 -Space@50")
	if got := h.slot0(); got.Gen != 2 || got.ID != 1 {
		t.Errorf("Status %+v, want Pad A at generation 2", got)
	}
	if got := rl.String(); got != "0/2" {
		t.Errorf("OnReload calls %q, want 0/2", got)
	}
	checkAttrs(t, h.waitLog("slot reloaded", 1)[0], map[string]string{"slot": "0", "gen": "2"})
	h.send(button(1, "east", false))
	h.set(60)
	h.expect("+Space@0 -Space@50")
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0 -Space@50 +LShift@60")
	h.checkGens(2)
	h.send(button(1, "east", false))
	h.sync()
	h.expect("+Space@0 -Space@50 +LShift@60 -LShift@60")
	// The router logs the generation too.
	h.send(removed(1))
	h.send(added(pad(3, "Pad A", "03ab")))
	checkAttrs(t, h.waitLog("slot assigned", 2)[1], map[string]string{"id": "3", "gen": "2"})
}

// TestReloadWhilePaused: a Reload keeps the pause; once resumed, a press
// maps with the new program.
func TestReloadWhilePaused(t *testing.T) {
	h := start(t, opts{})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.set(10)
	h.pause(true)
	h.expect("+Space@0 -Space@10")
	h.reload(compile2(t), 2)
	if st := h.sup.Status(); !st.Paused || st.Slots[0].Gen != 2 {
		t.Errorf("Status %+v, want paused at generation 2", st)
	}
	h.send(button(1, "south", true))
	h.send(button(1, "south", false))
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.set(20)
	h.expect("+Space@0 -Space@10")
	h.pause(false)
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0 -Space@10 +LShift@20")
	h.checkGens(2)
	h.send(button(1, "east", false))
	h.sync()
	h.expect("+Space@0 -Space@10 +LShift@20 -LShift@20")
}

// TestReloadWhileSuspended: a Reload keeps the suspension, as it keeps the
// pause; once it ends, a press maps with the new program.
func TestReloadWhileSuspended(t *testing.T) {
	h := start(t, opts{})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.set(10)
	h.sup.Suspend(lock)
	h.sync()
	h.expect("+Space@0 -Space@10")
	h.reload(compile2(t), 2)
	if st := h.sup.Status(); st.Suspended != lock || st.Paused || st.Slots[0].Gen != 2 {
		t.Errorf("Status %+v, want suspended for lock at generation 2", st)
	}
	h.checkGate(output.BlockSystem)
	h.send(button(1, "south", true))
	h.send(button(1, "south", false))
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.set(20)
	h.expect("+Space@0 -Space@10")
	h.sup.Suspend(0)
	h.sync()
	h.checkGate(0)
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0 -Space@10 +LShift@20")
	h.checkGens(2)
	h.send(button(1, "east", false))
	h.sync()
	h.expect("+Space@0 -Space@10 +LShift@20 -LShift@20")
}

// TestReloadOrder: the events sent before Reload are the old program's,
// those still queued in Events included; the router picks between the two
// at random, so the test repeats.
func TestReloadOrder(t *testing.T) {
	for i := range 20 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			h := start(t, opts{quiet: true, queued: []input.Event{
				added(padA), button(1, "east", true), button(1, "south", true),
			}})
			h.reload(compile2(t), 2)
			// Non-modifiers first, in reverse press order (Arbiter.ReleaseOwners).
			h.expect("+Space@0 +LCtrl@0 +Z@0 -Z@0 -Space@0 -LCtrl@0")
			h.send(button(1, "south", false))
			h.send(button(1, "south", true))
			h.set(20)
			h.rec.WaitFor(t, "+Space@0 +LCtrl@0 +Z@0 -Z@0 -Space@0 -LCtrl@0 +X@0 -X@20")
		})
	}
}

// TestReloadTapPending: a Reload ends the tap in progress at once, and the
// tap's deadline then does nothing.
func TestReloadTapPending(t *testing.T) {
	h := start(t, opts{})
	h.send(added(padA))
	h.send(button(1, "south", true))
	h.set(5)
	h.expect("+LCtrl@0 +Z@0")
	h.reload(compile2(t), 2)
	h.expect("+LCtrl@0 +Z@0 -Z@5 -LCtrl@5")
	h.set(20)
	h.send(button(1, "south", false))
	h.sync()
	h.expect("+LCtrl@0 +Z@0 -Z@5 -LCtrl@5")
	h.send(button(1, "south", true))
	h.set(40)
	h.rec.WaitFor(t, "+LCtrl@0 +Z@0 -Z@5 -LCtrl@5 +X@20 -X@40")
}

// TestReloadBehind: the Reload of a slot that is behind waits for it to
// catch up with the old program, the Resync included, then releases what
// that pressed.
func TestReloadBehind(t *testing.T) {
	var st *stall
	h := start(t, opts{buffer: 1, onEvent: func(i uint8, e input.Event) { st.onEvent(i, e) }})
	st = newStall(t, "west")
	h.send(added(padA))
	st.next(t)
	h.send(button(1, "east", true))
	st.next(t)
	h.send(button(1, "west", true))
	st.wait(t, st.stalled)
	h.send(button(1, "west", false)) // queued: the channel holds one
	h.send(button(1, "south", true)) // skipped: the Resync covers it
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	gen, wait, err := h.sup.StartReload(ctx, compile2(t))
	if err != nil || gen != 2 {
		t.Fatalf("StartReload = %d, %v; want 2, nil", gen, err)
	}
	st.release()
	h.ticking("Reload", wait)
	// The Resync tapped Ctrl+Z; the Reload released Space, and the tap unless
	// its deadline came first: the clock moves on meanwhile.
	first := untimed(h.rec.Trace())
	if first != "+Space +LCtrl +Z -Z -Space -LCtrl" && first != "+Space +LCtrl +Z -Z -LCtrl -Space" {
		t.Errorf("output %s, want +Space +LCtrl +Z, then each released once", first)
	}
	if got := h.slot0().Gen; got != 2 {
		t.Errorf("Status Gen %d, want 2", got)
	}
	h.send(button(1, "east", false))
	// The channel holds one: the press finds it full when the slot has not
	// taken the release yet, and then only the router's retries catch it up.
	h.send(button(1, "east", true))
	h.ticking("Sync", h.sup.Sync)
	if got := untimed(h.rec.Trace()); got != first+" +LShift" {
		t.Errorf("output %s, want +LShift last", got)
	}
	h.checkGens(2)
	h.send(button(1, "east", false))
	h.sync()
}

// TestReloadBackoff: a slot that waits to restart after a panic has
// nothing to release; Reload returns at once, and the slot restarts with
// the new program.
func TestReloadBackoff(t *testing.T) {
	p := &panicky{}
	p.armed.Store(true)
	rl := &reloads{}
	h := start(t, opts{trace: p.trace, onReload: rl.onReload})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.send(button(1, "south", true))
	checkAttrs(t, h.waitLog("slot panicked", 1)[0], map[string]string{"gen": "1", "backoff": "100ms"})
	h.rec.WaitFor(t, "+Space@0 -Space@0")
	h.reload(compile2(t), 2)
	if got := h.slot0(); got.Gen != 2 || got.Restarts != 0 {
		t.Errorf("Status %+v, want generation 2 before the restart", got)
	}
	h.clk.Advance(100 * ms)
	h.sync()
	if got := h.slot0(); got.Gen != 2 || got.Restarts != 1 {
		t.Errorf("Status %+v, want generation 2, 1 restart", got)
	}
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0 -Space@0 +LShift@100")
	h.checkGens(2)
	h.send(button(1, "east", false))
	h.sync()
	if got := rl.String(); got != "" {
		t.Errorf("OnReload calls %q, want none", got)
	}
	// The runner logs the generation of the slot it runs.
	h.send(button(1, "south", false))
	h.send(button(1, "south", true))
	checkAttrs(t, h.waitLog("slot panicked", 2)[1], map[string]string{"gen": "2", "backoff": "200ms"})
}

// TestReloadBackoffQueued: the edges queued for a slot that waits to
// restart, those sent before Reload included, map with the new program.
func TestReloadBackoffQueued(t *testing.T) {
	p := &panicky{}
	p.armed.Store(true)
	h := start(t, opts{trace: p.trace})
	h.send(added(padA))
	h.send(button(1, "south", true))
	h.waitLog("slot panicked", 1)
	h.send(button(1, "east", true)) // queued before the Reload
	h.reload(compile2(t), 2)
	h.clk.Advance(100 * ms)
	h.sync()
	h.expect("+LShift@100")
	h.send(button(1, "east", false))
	h.sync()
	h.expect("+LShift@100 -LShift@100")
}

// TestReloadDisabled: a disabled slot has nothing to release either; it
// starts with the new program when it is enabled again, and a Reload then
// switches it as any slot that runs.
func TestReloadDisabled(t *testing.T) {
	p := &panicky{}
	p.armed.Store(true)
	rl := &reloads{}
	h := start(t, opts{trace: p.trace, onReload: rl.onReload})
	h.send(added(padA))
	for i, backoff := range []time.Duration{100 * ms, 200 * ms, 0} {
		h.send(button(1, "south", true))
		h.waitLog("slot panicked", i+1)
		h.send(button(1, "south", false))
		h.clk.Advance(backoff)
		h.sync()
	}
	h.waitLog("slot disabled", 1)
	h.reload(compile2(t), 2)
	if got := h.slot0(); got.Gen != 2 || !got.Disabled {
		t.Errorf("Status %+v, want generation 2, disabled", got)
	}
	p.armed.Store(false)
	h.send(removed(1))
	h.send(added(padB))
	h.waitLog("slot enabled", 1)
	h.send(button(2, "east", true))
	h.sync()
	h.expect("+LShift@300")
	h.checkGens(2)
	if got := h.slot0(); got.Gen != 2 || got.Disabled {
		t.Errorf("Status %+v, want generation 2, enabled", got)
	}
	if got := rl.String(); got != "" {
		t.Errorf("OnReload calls %q before the slot ran, want none", got)
	}
	h.reload(compile(t), 3)
	h.expect("+LShift@300 -LShift@300")
	if got := rl.String(); got != "0/3" {
		t.Errorf("OnReload calls %q, want 0/3", got)
	}
}

// TestReloadErrors: Reload of no program, before Run, past its context and
// after Run.
func TestReloadErrors(t *testing.T) {
	t.Run("no program", func(t *testing.T) {
		h := start(t, opts{})
		if gen, err := h.sup.Reload(context.Background(), nil); err == nil || gen != 0 {
			t.Errorf("Reload(nil) = %d, %v; want an error", gen, err)
		}
		h.reload(compile2(t), 2) // the generation was not taken
	})
	t.Run("before Run", func(t *testing.T) {
		clk := clock.NewFake()
		out := output.StartLoop(clk, simtest.NewSyncRecorder(clk), output.LoopOptions{})
		defer func() { _ = out.Stop() }()
		sup, err := supervisor.New(supervisor.Config{Clock: clk, Events: make(chan input.Event), Output: out, Program: compile(t)})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*ms)
		defer cancel()
		if gen, err := sup.Reload(ctx, compile2(t)); !errors.Is(err, context.DeadlineExceeded) || gen != 0 {
			t.Errorf("Reload = %d, %v; want 0, DeadlineExceeded", gen, err)
		}
	})
	t.Run("context ends", func(t *testing.T) {
		var st *stall
		h := start(t, opts{onEvent: func(i uint8, e input.Event) { st.onEvent(i, e) }})
		st = newStall(t, "west")
		h.send(added(padA))
		h.send(button(1, "east", true))
		h.send(button(1, "west", true))
		st.wait(t, st.stalled)
		ctx, cancel := context.WithTimeout(context.Background(), 10*ms)
		defer cancel()
		// The router took it: the generation is spent, and the slot
		// switches once it goes on.
		if gen, err := h.sup.Reload(ctx, compile2(t)); !errors.Is(err, context.DeadlineExceeded) || gen != 2 {
			t.Errorf("Reload = %d, %v; want 2, DeadlineExceeded", gen, err)
		}
		st.release()
		h.sync()
		h.expect("+Space@0 -Space@0")
		if got := h.slot0().Gen; got != 2 {
			t.Errorf("Status Gen %d, want 2", got)
		}
		h.reload(compile(t), 3)
	})
	t.Run("after Run", func(t *testing.T) {
		h := start(t, opts{})
		h.cancel()
		if err := h.wait(); err != nil {
			t.Fatalf("Run = %v", err)
		}
		if gen, err := h.sup.Reload(context.Background(), compile2(t)); !errors.Is(err, supervisor.ErrStopped) || gen != 0 {
			t.Errorf("Reload = %d, %v; want 0, ErrStopped", gen, err)
		}
	})
}

// TestReloadTwice: each Reload takes the next generation, and releases
// what the one before pressed.
func TestReloadTwice(t *testing.T) {
	rl := &reloads{}
	h := start(t, opts{onReload: rl.onReload})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.sync()
	h.reload(compile2(t), 2)
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.sync()
	h.checkGens(2)
	h.reload(compile(t), 3)
	h.expect("+Space@0 -Space@0 +LShift@0 -LShift@0")
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0 -Space@0 +LShift@0 -LShift@0 +Space@0")
	h.checkGens(3)
	if got := h.slot0().Gen; got != 3 {
		t.Errorf("Status Gen %d, want 3", got)
	}
	if got := rl.String(); got != "0/2 0/3" {
		t.Errorf("OnReload calls %q, want 0/2 0/3", got)
	}
	h.send(button(1, "east", false))
	h.sync()
}

// TestReloadReclaim: a Reload leaves the slot's gamepad, home and
// provisional state as they are. It releases what the stand-in holds; the
// gamepad that comes back takes the slot back with the new program, and
// the stand-in, handed it again, too.
func TestReloadReclaim(t *testing.T) {
	h := start(t, opts{})
	keyA, keyB := input.DeviceKey{GUID: "03ab"}, input.DeviceKey{GUID: "057e"}
	h.send(added(padA))
	// Pad A presses a button, so Pad B's press takes nothing over.
	h.send(button(1, "west", true))
	h.send(button(1, "west", false))
	h.send(added(padB))
	h.send(button(2, "east", true))
	h.set(10)
	h.send(at(10, removed(1)))
	h.sync()
	h.expect("+Space@10")
	h.set(20)
	h.reload(compile2(t), 2)
	h.expect("+Space@10 -Space@20")
	want := supervisor.SlotStatus{Key: keyB, ID: 2, Name: "Pad B", Gen: 2, Home: keyA, Provisional: true}
	if got := h.slot0(); got != want {
		t.Errorf("Status %+v after the Reload, want %+v", got, want)
	}
	// The removal of the stand-in releases nothing more.
	h.send(at(20, added(pad(4, "Pad A", "03ab"))))
	h.sync()
	h.expect("+Space@10 -Space@20")
	h.waitLog("slot reclaimed", 1)
	want = supervisor.SlotStatus{Key: keyA, ID: 4, Name: "Pad A", Gen: 2, Home: keyA, Provisional: true}
	if got := h.slot0(); got != want {
		t.Errorf("Status %+v after the reclaim, want %+v", got, want)
	}
	h.send(button(4, "east", true))
	h.sync()
	h.expect("+Space@10 -Space@20 +LShift@20")
	h.checkGens(2)
	h.send(button(4, "east", false))
	// Pad A leaves: Pad B, first in line, still holds east.
	h.set(30)
	h.send(at(30, removed(4)))
	h.sync()
	h.expect("+Space@10 -Space@20 +LShift@20 -LShift@20 +LShift@30")
	h.checkGens(2)
	h.send(button(2, "east", false))
	h.sync()
	h.expect("+Space@10 -Space@20 +LShift@20 -LShift@20 +LShift@30 -LShift@30")
}

// TestReloadTakeOver: after a Reload, a gamepad that waits takes the slot
// over from the provisional stand-in, whose keys the Reload released, and
// its press maps with the new program.
func TestReloadTakeOver(t *testing.T) {
	h := start(t, opts{})
	keyC := input.DeviceKey{GUID: "0c0c"}
	h.send(added(padA))
	h.send(button(1, "north", true))
	h.send(button(1, "north", false))
	h.send(added(padB))
	h.send(added(pad(3, "Pad C", "0c0c")))
	h.send(button(2, "east", true))
	h.set(10)
	h.send(at(10, removed(1)))
	h.sync()
	h.expect("+Space@10")
	h.set(15)
	h.reload(compile2(t), 2)
	h.expect("+Space@10 -Space@15")
	h.set(20)
	h.send(at(20, button(3, "south", true)))
	h.send(button(3, "south", false))
	h.set(40)
	h.rec.WaitFor(t, "+Space@10 -Space@15 +X@20 -X@40")
	checkAttrs(t, h.waitLog("slot taken over", 1)[0], map[string]string{"id": "3", "holder": "2"})
	checkAttrs(t, h.waitLog("slot assigned", 3)[2], map[string]string{"id": "3", "gen": "2", "provisional": "false"})
	want := supervisor.SlotStatus{Key: keyC, ID: 3, Name: "Pad C", Gen: 2, Home: keyC}
	if got := h.slot0(); got != want {
		t.Errorf("Status %+v after the take-over, want %+v", got, want)
	}
}

// TestReloadLogs: Reload logs nothing at info level; the slot logs its
// switch at debug level.
func TestReloadLogs(t *testing.T) {
	h := start(t, opts{})
	h.send(added(padA))
	h.sync()
	before := len(h.logs.with(""))
	h.reload(compile2(t), 2)
	lines := h.waitLog("slot reloaded", 1)
	if lines[0].Level != slog.LevelDebug {
		t.Errorf("slot reloaded at %v, want debug", lines[0].Level)
	}
	checkAttrs(t, lines[0], map[string]string{"slot": "0", "gen": "2"})
	h.sync()
	for _, r := range h.logs.with("")[before:] {
		if r.Level > slog.LevelDebug {
			t.Errorf("%s at %v during the reload, want debug", r.Msg, r.Level)
		}
	}
}

// TestSlotsReload: a Reload switches every slot, free ones included, and
// each lets go of what it holds.
func TestSlotsReload(t *testing.T) {
	rl := &reloads{}
	h := start(t, opts{slots: 3, program: compileMulti(t), onReload: rl.onReload})
	h.send(added(padA))
	h.send(added(padB))
	h.send(button(1, "east", true))
	h.sync()
	h.send(button(2, "west", true))
	h.set(10)
	h.expect("+Space@0 +LShift@0")
	h.reload(compile2(t), 2)
	h.expectInterleaved("+Space@0 -Space@10", "+LShift@0 -LShift@10")
	for i := range 3 {
		if got := h.slot(i).Gen; got != 2 {
			t.Errorf("slot %d at generation %d, want 2", i, got)
		}
	}
	calls := strings.Fields(rl.String())
	slices.Sort(calls)
	if got := strings.Join(calls, " "); got != "0/2 1/2 2/2" {
		t.Errorf("OnReload calls %q, want 0/2 1/2 2/2 in any order", got)
	}
	h.checkGens()
}
