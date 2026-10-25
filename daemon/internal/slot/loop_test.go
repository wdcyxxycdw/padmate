package slot_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// waitLimit bounds every real-time wait of the loop tests.
const waitLimit = 5 * time.Second

var (
	errBlocked = errors.New("test: blocked")
	errGoexit  = errors.New("test: RunLoop did not return")
)

// expect checks the injected events now.
func expect(t *testing.T, s *simtest.SyncRecorder, want string) {
	t.Helper()
	if got := s.Trace(); got != want {
		t.Errorf("output:\n got %s\nwant %s", got, want)
	}
}

// loopOpts configures a loopHarness.
type loopOpts struct {
	bindings []string                           // of the profile, in place of eastHold and southTap
	remote   bool                               // through an output.Loop
	wrap     func(*output.Arbiter) slot.Arbiter // wraps the plain Arbiter
	trace    func(engine.Decision)
	lock     bool
	buffer   int // of the edge channel
}

// loopHarness runs a slot with eastHold and southTap in RunLoop, on a
// fake clock, with a plain Arbiter or through an output.Loop, both
// sending to a simtest.SyncRecorder.
type loopHarness struct {
	t      *testing.T
	clk    *clock.Fake
	rec    *simtest.SyncRecorder
	arb    *output.Arbiter // the plain Arbiter; only the loop touches it while it runs
	out    *output.Loop    // with remote
	s      *slot.Slot
	edges  chan slot.Edge
	jobs   chan func(*slot.Slot)
	errs   chan []error
	cancel context.CancelFunc
	result chan error
	ended  bool
	err    error // RunLoop's, once ended
}

func startSlotLoop(t *testing.T, o loopOpts) *loopHarness {
	t.Helper()
	h := &loopHarness{
		t: t, clk: clock.NewFake(),
		edges: make(chan slot.Edge, o.buffer), jobs: make(chan func(*slot.Slot)),
		errs: make(chan []error, 16), result: make(chan error, 1),
	}
	h.rec = simtest.NewSyncRecorder(h.clk)
	var arb slot.Arbiter
	if o.remote {
		h.out = output.StartLoop(h.clk, h.rec, output.LoopOptions{})
		arb = h.out.Remote()
	} else {
		h.arb = output.NewArbiter(h.clk, h.rec, output.ArbiterOptions{})
		arb = h.arb
		if o.wrap != nil {
			arb = o.wrap(h.arb)
		}
	}
	bindings := o.bindings
	if bindings == nil {
		bindings = []string{eastHold, southTap}
	}
	s, err := slot.New(slot.Config{Gen: 1, Program: compile(t, bindings...), Arbiter: arb, Clock: h.clk, Trace: o.trace})
	if err != nil {
		t.Fatal(err)
	}
	h.s = s
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	cfg := slot.LoopConfig{
		Slot: s, Edges: h.edges, Jobs: h.jobs, Lock: o.lock,
		Errors: func(errs []error) {
			select {
			case h.errs <- errs:
			default:
				t.Errorf("Errors: channel full at %v", errs)
			}
		},
	}
	go func() {
		err := errGoexit // left when RunLoop exits the goroutine instead of returning
		defer func() { h.result <- err }()
		err = slot.RunLoop(ctx, cfg)
	}()
	t.Cleanup(func() {
		h.cancel()
		_ = h.wait() // the test checks the error it expects
		if h.out != nil {
			if err := h.out.Stop(); err != nil {
				t.Errorf("output.Loop.Stop: %v", err)
			}
		}
		h.rec.With(func(r *simtest.Recorder) { simtest.AssertNoneHeld(t, h.arb, r) })
	})
	return h
}

// wait waits for RunLoop to return and returns its error.
func (h *loopHarness) wait() error {
	h.t.Helper()
	if !h.ended {
		select {
		case h.err = <-h.result:
		case <-time.After(waitLimit):
			h.t.Fatal("RunLoop did not return")
		}
		h.ended = true
	}
	return h.err
}

// send hands e to the loop without waiting for it to be handled.
func (h *loopHarness) send(e slot.Edge) {
	h.t.Helper()
	select {
	case h.edges <- e:
	case <-time.After(waitLimit):
		h.t.Fatal("the loop takes no edges")
	}
}

// button sends a button edge and waits until the loop has handled it.
func (h *loopHarness) button(c string, down bool) {
	h.t.Helper()
	h.send(slot.Edge{Event: button(c, down)})
	h.sync()
}

// job runs f on the loop and waits for it; jobJust hands it over.
func (h *loopHarness) job(f func(*slot.Slot)) {
	h.t.Helper()
	ran := make(chan struct{})
	h.jobJust(func(s *slot.Slot) { f(s); close(ran) })
	select {
	case <-ran:
	case <-time.After(waitLimit):
		h.t.Fatal("the job did not run")
	}
}

func (h *loopHarness) jobJust(f func(*slot.Slot)) {
	h.t.Helper()
	select {
	case h.jobs <- f:
	case <-time.After(waitLimit):
		h.t.Fatal("the loop takes no jobs")
	}
}

// sync returns once the loop has finished its last wake, re-arming its
// timer included, and so has the output.Loop with remote, so that the
// test can move the clock.
func (h *loopHarness) sync() {
	h.t.Helper()
	h.job(func(*slot.Slot) {})
	if h.out != nil {
		if err := h.out.Do(func(*output.Arbiter) error { return nil }); err != nil {
			h.t.Fatal(err)
		}
	}
}

// set syncs, then moves the clock to msec ms.
func (h *loopHarness) set(msec int) {
	h.t.Helper()
	h.sync()
	h.clk.Set(clock.Time(time.Duration(msec) * ms))
}

// nextErrors waits for the errors of a wake.
func (h *loopHarness) nextErrors() []error {
	h.t.Helper()
	select {
	case errs := <-h.errs:
		return errs
	case <-time.After(waitLimit):
		h.t.Fatal("no errors")
		return nil
	}
}

func (h *loopHarness) noErrors() {
	h.t.Helper()
	if len(h.errs) > 0 {
		h.t.Errorf("unexpected errors: %v", <-h.errs)
	}
}

func wantErr(t *testing.T, errs []error, target error, subs ...string) {
	t.Helper()
	err := errors.Join(errs...)
	if !errors.Is(err, target) {
		t.Errorf("errors %v do not wrap %v", err, target)
	}
	for _, sub := range subs {
		if err == nil || !strings.Contains(err.Error(), sub) {
			t.Errorf("errors %v do not contain %q", err, sub)
		}
	}
}

func TestRunLoopConfigErrors(t *testing.T) {
	sim := simtest.New(t)
	s, err := slot.New(slot.Config{Gen: 1, Program: compile(t, eastHold), Arbiter: sim.Arb, Clock: sim.Clock})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		cfg  slot.LoopConfig
		want string
	}{
		{"no slot", slot.LoopConfig{Edges: make(chan slot.Edge)}, "Slot"},
		{"no edges", slot.LoopConfig{Slot: s}, "edge channel"},
	} {
		if err := slot.RunLoop(context.Background(), tc.cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: RunLoop = %v, want an error about %s", tc.name, err, tc.want)
		}
	}
	if sim.Out.Calls() != 0 {
		t.Errorf("RunLoop with a bad config sent %d batches", sim.Out.Calls())
	}
}

func TestRunLoopEdges(t *testing.T) {
	h := startSlotLoop(t, loopOpts{})
	h.button("east", true)
	expect(t, h.rec, "+Space@0")
	h.set(100)
	h.button("east", false)
	expect(t, h.rec, "+Space@0 -Space@100")

	st := stateWith(t, "east", "leftx=100")
	h.send(slot.Edge{Event: button("south", true), Resync: &st}) // Event is ignored
	h.sync()
	expect(t, h.rec, "+Space@0 -Space@100 +Space@100")
	h.job(func(s *slot.Slot) {
		if got := s.State(); got != st {
			t.Errorf("State() = %+v after Resync, want %+v", got, st)
		}
	})
	h.button("east", false)
	close(h.edges)
	if err := h.wait(); err != nil {
		t.Errorf("RunLoop = %v, want nil", err)
	}
	expect(t, h.rec, "+Space@0 -Space@100 +Space@100 -Space@100")
	h.noErrors()
}

// TestRunLoopReset: a Reset edge lets go of everything and zeroes the
// State; its Event and Resync are ignored.
func TestRunLoopReset(t *testing.T) {
	h := startSlotLoop(t, loopOpts{})
	h.button("east", true)
	h.set(10)
	st := stateWith(t, "west", "leftx=100")
	h.send(slot.Edge{Event: button("south", true), Resync: &st, Reset: true})
	h.sync()
	expect(t, h.rec, "+Space@0 -Space@10")
	h.job(func(s *slot.Slot) {
		if got := s.State(); got != (slot.State{}) {
			t.Errorf("State() = %+v after a Reset, want zero", got)
		}
	})
	h.button("east", false) // up already, for the engine too
	h.button("east", true)
	close(h.edges)
	if err := h.wait(); err != nil {
		t.Errorf("RunLoop = %v, want nil", err)
	}
	expect(t, h.rec, "+Space@0 -Space@10 +Space@10 -Space@10")
	h.noErrors()
}

// TestRunLoopJobAfterQueuedEdges: a job runs after the edges queued before
// the loop took it, so it sees them handled.
func TestRunLoopJobAfterQueuedEdges(t *testing.T) {
	h := startSlotLoop(t, loopOpts{buffer: 4})
	for i := range 20 {
		gate := make(chan struct{})
		h.jobJust(func(*slot.Slot) { <-gate }) // the loop waits in it
		down := i%2 == 0
		h.send(slot.Edge{Event: button("east", down)})
		h.send(slot.Edge{Event: axis("leftx", int16(i))})
		close(gate)
		h.job(func(s *slot.Slot) {
			var want slot.State
			want.Buttons[control.East] = down
			want.Axes[control.LeftX] = int16(i)
			if got := s.State(); got != want {
				t.Errorf("round %d: State() = %+v in the job, want %+v", i, got, want)
			}
		})
	}
	close(h.edges)
	if err := h.wait(); err != nil {
		t.Errorf("RunLoop = %v, want nil", err)
	}
	h.noErrors()
}

func TestRunLoopEdgesClosedReleases(t *testing.T) {
	h := startSlotLoop(t, loopOpts{})
	h.button("east", true)
	h.set(50)
	close(h.edges)
	if err := h.wait(); err != nil {
		t.Errorf("RunLoop = %v, want nil", err)
	}
	expect(t, h.rec, "+Space@0 -Space@50")
}

func TestRunLoopCancelReleases(t *testing.T) {
	h := startSlotLoop(t, loopOpts{})
	h.button("east", true)
	h.set(50)
	h.cancel()
	if err := h.wait(); !errors.Is(err, context.Canceled) {
		t.Errorf("RunLoop = %v, want context.Canceled", err)
	}
	expect(t, h.rec, "+Space@0 -Space@50")
}

// TestRunLoopEndReleaseFails: the errors of the release at the end reach
// Errors, and the Arbiter's retry of it is left to the Arbiter's owner.
func TestRunLoopEndReleaseFails(t *testing.T) {
	h := startSlotLoop(t, loopOpts{})
	h.button("east", true)
	h.rec.With(func(r *simtest.Recorder) { r.FailNext(0, errBlocked) })
	h.set(10)
	h.button("south", true) // the tap's press fails: its release is due at 30ms
	wantErr(t, h.nextErrors(), errBlocked)
	h.rec.With(func(r *simtest.Recorder) { r.FailNext(0, errBlocked); r.FailNext(0, errBlocked) })
	h.cancel() // the release fails twice: pending, retried at 110ms
	if err := h.wait(); !errors.Is(err, context.Canceled) {
		t.Errorf("RunLoop = %v, want context.Canceled", err)
	}
	wantErr(t, h.nextErrors(), errBlocked, "failed twice")
	expect(t, h.rec, "+Space@0")
	// The Arbiter is the test's again: its retry is left.
	h.clk.Set(clock.Time(110 * ms))
	if err := h.arb.Advance(); err != nil {
		t.Fatal(err)
	}
	expect(t, h.rec, "+Space@0 -Z@110 -Space@110 -LCtrl@110")
}

func TestRunLoopJobs(t *testing.T) {
	h := startSlotLoop(t, loopOpts{})
	h.button("east", true)
	h.job(func(s *slot.Slot) {
		if got := s.State(); !got.Buttons[control.East] {
			t.Errorf("State() = %+v in a job, want east down", got)
		}
	})
	h.jobJust(nil) // ignored
	h.sync()
	close(h.jobs) // jobs end, edges go on
	h.send(slot.Edge{Event: button("east", false)})
	h.rec.WaitFor(t, "+Space@0 -Space@0")
	close(h.edges)
	if err := h.wait(); err != nil {
		t.Errorf("RunLoop = %v, want nil", err)
	}
}

func TestRunLoopErrors(t *testing.T) {
	h := startSlotLoop(t, loopOpts{})
	h.rec.With(func(r *simtest.Recorder) { r.FailNext(0, errBlocked) })
	h.button("east", true) // the press fails; the Arbiter counts it down
	wantErr(t, h.nextErrors(), errBlocked)
	h.button("east", false)
	h.noErrors()
	expect(t, h.rec, "-Space@0")
}

// TestRunLoopTimer: moving the clock alone runs the Arbiter's deadlines:
// a retry of a failed release, then the end of a tap.
func TestRunLoopTimer(t *testing.T) {
	h := startSlotLoop(t, loopOpts{})
	h.button("east", true)
	h.rec.With(func(r *simtest.Recorder) { r.FailNext(0, errBlocked); r.FailNext(0, errBlocked) })
	h.set(10)
	h.button("east", false)
	wantErr(t, h.nextErrors(), errBlocked, "failed twice")
	h.set(110)
	h.rec.WaitFor(t, "+Space@0 -Space@110")

	h.set(200)
	h.button("south", true)
	expect(t, h.rec, "+Space@0 -Space@110 +LCtrl@200 +Z@200")
	h.set(200 + int(engine.DefaultTapPress/ms))
	h.rec.WaitFor(t, "+Space@0 -Space@110 +LCtrl@200 +Z@200 -Z@220 -LCtrl@220")
	h.sync()
	h.noErrors()
}

// panicArbiter is an Arbiter whose ReleaseOwners panics.
type panicArbiter struct{ *output.Arbiter }

func (panicArbiter) ReleaseOwners(func(output.Owner) bool) error { panic("release boom") }

func TestRunLoopPanicReleases(t *testing.T) {
	southDown := func(d engine.Decision) {
		if d.Control == control.South && d.Edge == engine.EdgeDown {
			panic("boom")
		}
	}
	for _, tc := range []struct {
		name       string
		o          loopOpts
		boom       func(h *loopHarness)
		out        string
		releaseErr string
	}{
		{"in Trace", loopOpts{trace: southDown}, func(h *loopHarness) { h.send(slot.Edge{Event: button("south", true)}) },
			"+Space@0 -Space@0", ""},
		{"in a job", loopOpts{}, func(h *loopHarness) { h.jobJust(func(*slot.Slot) { panic("boom") }) },
			"+Space@0 -Space@0", ""},
		{"release panics", loopOpts{wrap: func(a *output.Arbiter) slot.Arbiter { return panicArbiter{a} }},
			func(h *loopHarness) { h.jobJust(func(*slot.Slot) { panic("boom") }) },
			"+Space@0", "slot: panic releasing the keys of slot 0: release boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := startSlotLoop(t, tc.o)
			h.button("east", true)
			tc.boom(h)
			var pe *slot.PanicError
			if err := h.wait(); !errors.As(err, &pe) {
				t.Fatalf("RunLoop = %v, want a *PanicError", err)
			}
			if pe.Value != "boom" || len(pe.Stack) == 0 || !strings.Contains(pe.Error(), "boom") {
				t.Errorf("PanicError %q: Value %v, %d bytes of stack", pe, pe.Value, len(pe.Stack))
			}
			switch {
			case tc.releaseErr == "" && pe.ReleaseErr != nil:
				t.Errorf("ReleaseErr = %v, want nil", pe.ReleaseErr)
			case tc.releaseErr != "" && (pe.ReleaseErr == nil || pe.ReleaseErr.Error() != tc.releaseErr):
				t.Errorf("ReleaseErr = %v, want %s", pe.ReleaseErr, tc.releaseErr)
			case tc.releaseErr != "" && !strings.Contains(pe.Error(), tc.releaseErr):
				t.Errorf("error %q leaves out ReleaseErr", pe)
			}
			expect(t, h.rec, tc.out)
			if tc.releaseErr != "" {
				// The loop has ended: the Arbiter is the test's again.
				if err := h.arb.ReleaseAll(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// TestRunLoopGoexit: after a runtime.Goexit in a job, RunLoop releases
// the slot's keys as after a panic, hands an error of the release to
// Errors, and does not return.
func TestRunLoopGoexit(t *testing.T) {
	for _, tc := range []struct {
		name       string
		o          loopOpts
		out        string
		releaseErr string
	}{
		{"release", loopOpts{}, "+Space@0 -Space@0", ""},
		{"release panics", loopOpts{wrap: func(a *output.Arbiter) slot.Arbiter { return panicArbiter{a} }},
			"+Space@0", "slot: panic releasing the keys of slot 0: release boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := startSlotLoop(t, tc.o)
			h.button("east", true)
			h.jobJust(func(*slot.Slot) { runtime.Goexit() })
			// The loop's goroutine delivers the errors before it has exited.
			if err := h.wait(); !errors.Is(err, errGoexit) {
				t.Fatalf("RunLoop = %v, want it not to return", err)
			}
			expect(t, h.rec, tc.out)
			if tc.releaseErr == "" {
				h.noErrors()
				return
			}
			if errs := h.nextErrors(); len(errs) != 1 || errs[0].Error() != tc.releaseErr {
				t.Errorf("Errors got %v, want %s", errs, tc.releaseErr)
			}
			// The loop has ended: the Arbiter is the test's again.
			if err := h.arb.ReleaseAll(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestRunLoopWithRemoteArbiter: the same flow through an output.Loop,
// whose own timer ends the tap.
func TestRunLoopWithRemoteArbiter(t *testing.T) {
	h := startSlotLoop(t, loopOpts{remote: true})
	h.button("east", true)
	expect(t, h.rec, "+Space@0")
	h.set(10)
	h.button("south", true)
	expect(t, h.rec, "+Space@0 +LCtrl@10 +Z@10")
	h.job(func(s *slot.Slot) {
		if d, ok := s.NextDeadline(); ok {
			t.Errorf("NextDeadline = %v with a RemoteArbiter, want none: the output.Loop runs the tap's", d)
		}
	})
	h.set(30)
	h.rec.WaitFor(t, "+Space@0 +LCtrl@10 +Z@10 -Z@30 -LCtrl@30")
	h.set(40)
	h.button("east", false)
	h.set(50)
	h.button("east", true)
	h.cancel()
	if err := h.wait(); !errors.Is(err, context.Canceled) {
		t.Errorf("RunLoop = %v, want context.Canceled", err)
	}
	expect(t, h.rec, "+Space@0 +LCtrl@10 +Z@10 -Z@30 -LCtrl@30 -Space@40 +Space@50 -Space@50")
	h.noErrors()
}
