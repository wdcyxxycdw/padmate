package supervisor_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

// pauses records the calls of Config.OnPause, as "<paused>/<by>".
type pauses struct {
	mu    sync.Mutex
	calls []string
}

func (p *pauses) onPause(paused bool, by string) {
	p.mu.Lock()
	p.calls = append(p.calls, fmt.Sprintf("%v/%s", paused, by))
	p.mu.Unlock()
}

func (p *pauses) String() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.calls, " ")
}

// checkPaused checks Status.Paused.
func (h *harness) checkPaused(want bool) {
	h.t.Helper()
	if got := h.sup.Status().Paused; got != want {
		h.t.Errorf("Status.Paused %v, want %v", got, want)
	}
}

// checkBlocked checks how many presses the Arbiter's gate dropped so far.
func (h *harness) checkBlocked(want uint64) {
	h.t.Helper()
	if got := h.blocked(); got != want {
		h.t.Errorf("the gate dropped %d presses, want %d", got, want)
	}
}

// checkNoWarnings checks that nothing was logged at warn level or above.
func (h *harness) checkNoWarnings() {
	h.t.Helper()
	for _, r := range h.logs.with("") {
		if r.Level >= slog.LevelWarn {
			h.t.Errorf("logged %q at %v with %v, want nothing at warn level", r.Msg, r.Level, r.Attrs)
		}
	}
}

// TestPauseAPI: Pause returns once the keys are up, and presses then do
// nothing: the paused slot sends none, so the gate drops none. Pause(false)
// presses nothing again: east, held across it, does nothing until pressed
// again. A Pause to the state the mapping is in does nothing.
func TestPauseAPI(t *testing.T) {
	p := &pauses{}
	h := start(t, opts{onPause: p.onPause})
	h.send(added(padA))
	h.send(button(1, "east", true))
	h.set(10)
	h.expect("+Space@0")
	h.checkPaused(false)
	h.pause(true)
	// Pause returned: the release is out.
	h.expect("+Space@0 -Space@10")
	h.checkPaused(true)
	h.send(button(1, "south", true))
	h.send(button(1, "south", false))
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.set(20)
	h.pause(true)
	h.expect("+Space@0 -Space@10")
	h.checkBlocked(0)
	h.pause(false)
	h.checkPaused(false)
	h.sync()
	h.expect("+Space@0 -Space@10")
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+Space@0 -Space@10 +Space@20")
	h.pause(false)
	h.send(button(1, "east", false))
	h.sync()
	h.expect("+Space@0 -Space@10 +Space@20 -Space@20")
	if got := p.String(); got != "true/api false/api" {
		t.Errorf("OnPause calls %q, want true/api false/api", got)
	}
	for _, msg := range []string{"mapping paused", "mapping resumed"} {
		lines := h.logs.with(msg)
		if len(lines) != 1 || lines[0].Level != slog.LevelInfo {
			t.Errorf("lines %q: %v, want one at info level", msg, lines)
			continue
		}
		checkAttrs(t, lines[0], map[string]string{"by": "api", "id": ""})
	}
	h.checkNoWarnings()
}

// TestPauseCombo: a ComboHeld toggles the pause (DMN-07), whichever
// gamepad it comes from: the slot's, one that waits for a slot, one the
// device filter leaves out, or an id the router does not know. No slot
// gets it, and it takes no slot over.
func TestPauseCombo(t *testing.T) {
	combo := func(id input.DeviceID) input.Event { return input.Event{Device: id, Kind: input.ComboHeld} }
	for _, tc := range []struct {
		name   string
		device string
		from   input.DeviceID
	}{
		{"the slot's gamepad", "", 1},
		{"a waiting gamepad", "", 2},
		{"a filtered gamepad", "Pad A", 2},
		{"an unknown id", "", 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &pauses{}
			var got atomic.Int32
			h := start(t, opts{device: tc.device, onPause: p.onPause, onEvent: func(_ uint8, e input.Event) {
				if e.Kind == input.ComboHeld {
					got.Add(1)
				}
			}})
			h.send(added(padA))
			h.send(added(padB))
			h.send(button(1, "east", true))
			h.sync()
			h.expect("+Space@0")
			h.send(combo(tc.from))
			h.sync()
			h.expect("+Space@0 -Space@0")
			h.checkPaused(true)
			h.send(button(1, "east", false))
			h.send(button(1, "east", true))
			h.send(combo(tc.from))
			h.sync()
			h.checkPaused(false)
			// East, held across the resume, does nothing until pressed again.
			h.expect("+Space@0 -Space@0")
			h.set(10)
			h.send(button(1, "east", false))
			h.send(button(1, "east", true))
			h.sync()
			h.expect("+Space@0 -Space@0 +Space@10")
			h.send(button(1, "east", false))
			h.sync()
			h.expect("+Space@0 -Space@0 +Space@10 -Space@10")
			if n := got.Load(); n != 0 {
				t.Errorf("slots got %d ComboHeld events, want none", n)
			}
			if st := h.slot0(); st.ID != 1 || st.Provisional {
				t.Errorf("slot 0 %+v, want Pad A's, confirmed", st)
			}
			if got := p.String(); got != "true/combo false/combo" {
				t.Errorf("OnPause calls %q, want true/combo false/combo", got)
			}
			for _, msg := range []string{"mapping paused", "mapping resumed"} {
				lines := h.logs.with(msg)
				if len(lines) != 1 || lines[0].Level != slog.LevelInfo {
					t.Errorf("lines %q: %v, want one at info level", msg, lines)
					continue
				}
				checkAttrs(t, lines[0], map[string]string{"by": "combo", "id": fmt.Sprint(tc.from)})
			}
			h.checkNoWarnings()
		})
	}
}

// TestPauseErrors: Pause before Run, which waits, and after Run.
func TestPauseErrors(t *testing.T) {
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
		if err := sup.Pause(ctx, true); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Pause = %v, want DeadlineExceeded", err)
		}
		if sup.Status().Paused {
			t.Error("Status.Paused set by a Pause the router never took")
		}
	})
	t.Run("after Run", func(t *testing.T) {
		h := start(t, opts{})
		h.cancel()
		if err := h.wait(); err != nil {
			t.Fatalf("Run = %v", err)
		}
		if err := h.sup.Pause(context.Background(), true); !errors.Is(err, supervisor.ErrStopped) {
			t.Errorf("Pause = %v, want ErrStopped", err)
		}
	})
}

// TestPauseBehind: Pause does not wait for a slot that is behind, but the
// gate is closed when it returns: the press queued for the slot is
// dropped. The slot pauses with its Resync, so nothing is held, the
// control held across does nothing, and the pause is not a skipped edge.
func TestPauseBehind(t *testing.T) {
	k := &slotHooks{}
	h := start(t, opts{hooks: k, buffer: 1})
	h.send(added(padA))
	h.sync()
	k.stall(0)
	h.send(button(1, "west", true)) // unbound; the slot stalls on it
	k.waitStalled(t, 0)
	h.send(button(1, "east", true)) // queued: the channel holds one
	h.pause(true)                   // the slot is behind
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
	h.pause(false)
	h.send(button(1, "east", false))
	h.send(button(1, "east", true))
	h.ticking("Sync", h.sup.Sync)
	if got := untimed(h.rec.Trace()); got != "+Space" {
		t.Errorf("output %s, want +Space", got)
	}
	h.send(button(1, "east", false))
	h.ticking("Sync", h.sup.Sync)
	if got := untimed(h.rec.Trace()); got != "+Space -Space" {
		t.Errorf("output %s, want +Space -Space", got)
	}
}

// TestResumeBehind: a slot that is behind learns of Pause(false) after its
// Resync, so the presses the Resync brings in stay canceled, whether the
// slot was paused before it fell behind or the pause came and went while it
// was behind and it never saw it.
func TestResumeBehind(t *testing.T) {
	for _, before := range []bool{true, false} {
		t.Run(map[bool]string{true: "paused before behind", false: "paused while behind"}[before], func(t *testing.T) {
			k := &slotHooks{}
			h := start(t, opts{hooks: k, buffer: 1})
			h.send(added(padA))
			h.sync() // the channel holds one: the pause must not find it full
			if before {
				h.pause(true)
				h.sync()
			}
			k.stall(0)
			h.send(button(1, "west", true)) // unbound; the slot stalls on it
			k.waitStalled(t, 0)
			h.send(button(1, "west", false)) // queued: the channel holds one
			if !before {
				h.pause(true)
			}
			h.send(button(1, "east", true)) // skipped: the Resync covers it
			h.pause(false)
			k.unstall(0)
			h.statesTicking()
			h.expect("")
			checkAttrs(t, h.waitLog("slot behind; resyncing", 1)[0], map[string]string{"slot": "0", "skipped": "1"})
			// East, held across the resume, does nothing until pressed again.
			// The slot can fall behind again, so the Syncs tick.
			h.send(button(1, "east", false))
			h.send(button(1, "east", true))
			h.ticking("Sync", h.sup.Sync)
			if got := untimed(h.rec.Trace()); got != "+Space" {
				t.Errorf("output %s, want +Space", got)
			}
			h.send(button(1, "east", false))
			h.ticking("Sync", h.sup.Sync)
		})
	}
}

// TestPauseRestart: a slot restarted after a panic starts paused while the
// mapping is paused, whether the pause came before the panic or during the
// backoff, and runs as usual when a resume came during the backoff. A press
// queued while paused stays canceled, also when the resume came before the
// restart: the fresh slot handles each edge queued for it paused as the
// mapping was when the router delivered it.
func TestPauseRestart(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before bool   // paused before the panic
		during string // during the backoff: pause, resume, press (east)
		paused bool   // after the restart
	}{
		{"paused before the panic", true, "", true},
		{"paused during the backoff", false, "pause", true},
		{"resumed during the backoff", true, "resume", false},
		{"pressed while paused", true, "press", true},
		{"pressed while paused, then resumed", true, "press resume", false},
		{"paused, pressed and resumed during the backoff", false, "pause press resume", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := &slotHooks{}
			h := start(t, opts{hooks: k})
			k.panicOn(0, "south")
			h.send(added(padA))
			if tc.before {
				h.pause(true)
			}
			h.send(button(1, "south", true)) // the Trace sees it paused or not
			h.waitLog("slot panicked", 1)
			k.disarm()
			h.send(button(1, "south", false))
			pressed := false
			for _, step := range strings.Fields(tc.during) {
				switch step {
				case "pause", "resume":
					h.pause(step == "pause")
				case "press":
					h.send(button(1, "east", true))
					pressed = true
				default:
					t.Fatalf("step %q", step)
				}
			}
			h.clk.Advance(100 * ms)
			h.sync()
			if got := h.slot0().Restarts; got != 1 {
				t.Errorf("Restarts %d, want 1", got)
			}
			h.checkPaused(tc.paused)
			if pressed {
				// East, pressed while paused, does nothing until pressed again:
				// the fresh slot sent nothing.
				h.expect("")
				h.checkBlocked(0)
				h.send(button(1, "east", false))
			}
			h.send(button(1, "east", true))
			h.sync()
			if tc.paused {
				// The fresh slot sent nothing: the gate dropped nothing.
				h.expect("")
				h.checkBlocked(0)
				h.pause(false)
				h.send(button(1, "east", false))
				h.send(button(1, "east", true))
				h.sync()
			}
			h.expect("+Space@100")
			h.send(button(1, "east", false))
			h.sync()
			h.expect("+Space@100 -Space@100")
		})
	}
}

// TestPauseDisabled: a slot enabled again while the mapping is paused
// starts paused. A press while paused stays canceled also when the resume
// comes before the runner creates the enabled slot, after the router
// delivered both.
func TestPauseDisabled(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "paused", true: "pressed and resumed before the start"}[late], func(t *testing.T) {
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
			h.pause(true) // the disabled slot gets nothing
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
				h.pause(false)
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
				h.pause(false)
			}
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

// TestPauseDisabledSwap: the gamepad that takes the slot while its third
// panic disables it presses east, and the router enables the slot again at
// once at the gamepad's state (see TestSlotPanicDisablesAfterSwap). When the
// mapping was paused since the gamepad connected, east stays canceled,
// although the resume came before the router learned of the panic; a pause
// and resume before the gamepad connected cancel nothing.
func TestPauseDisabledSwap(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps string // after the removal: pause, resume, add (Pad B), press (east)
		want  string // the output once enabled
	}{
		{"pressed while paused", "pause add press resume", ""},
		{"paused after the press", "add press pause resume", ""}, // a live slot would press and cancel
		{"paused before the gamepad connected", "pause resume add press", "+Space@300"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &panicky{}
			p.armed.Store(true)
			var st *stall
			h := start(t, opts{trace: p.trace, onEvent: func(i uint8, e input.Event) { st.onEvent(i, e) }})
			st = newStall(t, "south")
			st.armed.Store(false)
			h.send(added(padA))
			for i, backoff := range []time.Duration{100 * ms, 200 * ms} {
				h.send(button(1, "south", true))
				h.waitLog("slot panicked", i+1)
				h.send(button(1, "south", false))
				h.clk.Advance(backoff)
				h.sync()
			}
			// The slot stalls on the press that panics the third time; the
			// router routes the rest meanwhile.
			st.armed.Store(true)
			h.send(button(1, "south", true))
			st.wait(t, st.stalled)
			h.send(removed(1))
			for _, step := range strings.Fields(tc.steps) {
				switch step {
				case "pause", "resume":
					h.pause(step == "pause")
				case "add":
					h.send(added(padB))
				case "press":
					h.send(button(2, "east", true))
				default:
					t.Fatalf("step %q", step)
				}
			}
			st.release()
			h.waitLog("slot disabled", 1)
			h.waitLog("slot enabled", 1)
			h.sync()
			if got := h.slot0(); got.Disabled || got.ID != 2 {
				t.Errorf("Status %+v, want enabled, Pad B", got)
			}
			h.checkPaused(false)
			h.expect(tc.want)
			h.checkBlocked(0)
			if tc.want == "" {
				h.send(button(2, "east", false))
				h.send(button(2, "east", true))
				h.sync()
				h.expect("+Space@300")
			}
			h.send(button(2, "east", false))
			h.sync()
			h.expect("+Space@300 -Space@300")
		})
	}
}
