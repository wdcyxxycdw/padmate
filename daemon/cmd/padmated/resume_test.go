package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

type resumeTestGate struct {
	mu   sync.Mutex
	bits output.Blocker
}

func (g *resumeTestGate) BlockFor(b output.Blocker, on bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if on {
		g.bits |= b
	} else {
		g.bits &^= b
	}
}
func (g *resumeTestGate) blocked() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.bits&output.BlockResume != 0
}

type resumeTestSupervisor struct {
	mu      sync.Mutex
	reasons winsession.Reason
	syncs   atomic.Int32
	barrier func(context.Context) error
}

func (s *resumeTestSupervisor) Suspend(r winsession.Reason) {
	s.mu.Lock()
	s.reasons = r
	s.mu.Unlock()
}
func (s *resumeTestSupervisor) Sync(ctx context.Context) error {
	s.syncs.Add(1)
	if s.barrier != nil {
		return s.barrier(ctx)
	}
	return ctx.Err()
}
func resumeWait(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatal("resume condition timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestResumeWaitsForCheckedWakePublicationAndSync(t *testing.T) {
	scan := make(chan struct{}, 1)
	allowScan, allowSync := make(chan struct{}), make(chan struct{})
	gate, sup := &resumeTestGate{}, &resumeTestSupervisor{}
	sup.barrier = func(ctx context.Context) error {
		select {
		case <-allowSync:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r := newResumeRecovery(context.Background(), func(ctx context.Context) error {
		scan <- struct{}{}
		select {
		case <-allowScan:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, sup, gate, slog.New(slog.DiscardHandler))
	t.Cleanup(r.stop)
	r.signal(winsession.SigWake) // No Sleep bit: still fence immediately.
	if !gate.blocked() {
		t.Fatal("raw wake did not close the recovery gate")
	}
	select {
	case <-scan:
		t.Fatal("scan started before the wake recheck")
	default:
	}
	r.wake(winsession.Change{Signal: winsession.SigWake})
	<-scan
	if sup.syncs.Load() != 0 || !gate.blocked() {
		t.Fatal("Sync overtook source publication")
	}
	close(allowScan)
	resumeWait(t, func() bool { return sup.syncs.Load() == 1 })
	if !gate.blocked() {
		t.Fatal("gate opened before Sync")
	}
	close(allowSync)
	resumeWait(t, func() bool { return !gate.blocked() })
}

func TestResumeNewSleepLockAndStopRejectOldCompletion(t *testing.T) {
	for _, sig := range []winsession.Signal{winsession.SigSleep, winsession.SigLock, winsession.SigEnd} {
		t.Run(sig.String(), func(t *testing.T) {
			entered, finish := make(chan struct{}), make(chan struct{})
			gate, sup := &resumeTestGate{}, &resumeTestSupervisor{}
			r := newResumeRecovery(context.Background(), func(context.Context) error { close(entered); <-finish; return nil }, sup, gate, slog.New(slog.DiscardHandler))
			t.Cleanup(r.stop)
			r.signal(winsession.SigWake)
			r.wake(winsession.Change{Signal: winsession.SigWake})
			<-entered
			r.signal(sig)
			r.signal(sig) // Repeat with unchanged reasons invalidates too.
			close(finish)
			resumeWait(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.flight == nil })
			if !gate.blocked() || sup.syncs.Load() != 0 {
				t.Fatal("stale scan reopened or synchronized")
			}
		})
	}
	entered, finish := make(chan struct{}), make(chan struct{})
	gate, sup := &resumeTestGate{}, &resumeTestSupervisor{}
	r := newResumeRecovery(context.Background(), func(context.Context) error { close(entered); <-finish; return nil }, sup, gate, slog.New(slog.DiscardHandler))
	r.signal(winsession.SigWake)
	r.wake(winsession.Change{Signal: winsession.SigWake})
	<-entered
	stopped := make(chan struct{})
	go func() { r.stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop waited for SDL")
	}
	close(finish)
	if !gate.blocked() {
		t.Fatal("stop opened gate")
	}
}

func TestResumeRepeatedWakeSupersedesInFlightScan(t *testing.T) {
	first := make(chan struct{})
	var n atomic.Int32
	gate, sup := &resumeTestGate{}, &resumeTestSupervisor{}
	r := newResumeRecovery(context.Background(), func(ctx context.Context) error {
		if n.Add(1) == 1 {
			close(first)
			<-ctx.Done()
		}
		return nil
	}, sup, gate, slog.New(slog.DiscardHandler))
	t.Cleanup(r.stop)
	r.signal(winsession.SigWake)
	r.wake(winsession.Change{Signal: winsession.SigWake})
	<-first
	r.signal(winsession.SigWake)
	r.wake(winsession.Change{Signal: winsession.SigWake})
	resumeWait(t, func() bool { return n.Load() == 2 && !gate.blocked() })
	if sup.syncs.Load() != 1 {
		t.Fatalf("stale scan reached Sync: %d", sup.syncs.Load())
	}
}

func TestResumeLockDuringSyncKeepsGateUntilUnlocked(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	gate, sup := &resumeTestGate{}, &resumeTestSupervisor{}
	sup.barrier = func(context.Context) error {
		if sup.syncs.Load() == 1 {
			close(entered)
			<-finish
		}
		return nil
	}
	r := newResumeRecovery(context.Background(), func(context.Context) error { return nil }, sup, gate, slog.New(slog.DiscardHandler))
	t.Cleanup(r.stop)
	r.signal(winsession.SigWake)
	r.wake(winsession.Change{Signal: winsession.SigWake})
	<-entered
	r.signal(winsession.SigLock)
	close(finish)
	resumeWait(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.flight == nil })
	if !gate.blocked() {
		t.Fatal("new lock lost to old Sync completion")
	}
	r.change(winsession.Change{Signal: winsession.SigUnlock, Was: winsession.Lock})
	resumeWait(t, func() bool { return !gate.blocked() })
	if sup.syncs.Load() != 2 {
		t.Fatal("unlock reused stale synchronization")
	}
}

func TestResumeRepeatedWakeAndFailureRetry(t *testing.T) {
	gate, sup := &resumeTestGate{}, &resumeTestSupervisor{}
	var scans atomic.Int32
	r := newResumeRecovery(context.Background(), func(context.Context) error {
		if scans.Add(1) == 1 {
			return errors.New("enumeration failed")
		}
		return nil
	}, sup, gate, slog.New(slog.DiscardHandler))
	t.Cleanup(r.stop)
	r.signal(winsession.SigWake)
	r.wake(winsession.Change{Signal: winsession.SigWake})
	resumeWait(t, func() bool { return scans.Load() == 1 })
	if !gate.blocked() || sup.syncs.Load() != 0 {
		t.Fatal("failed enumeration opened gate")
	}
	resumeWait(t, func() bool { return !gate.blocked() })
	r.signal(winsession.SigWake)
	r.wake(winsession.Change{Signal: winsession.SigWake})
	resumeWait(t, func() bool { return scans.Load() == 3 && !gate.blocked() })
}

type resumeLiveSource struct {
	*fakeSource
	scans    atomic.Int32
	snapshot *input.GamepadState
}

func (s *resumeLiveSource) Reenumerate(ctx context.Context) error {
	s.scans.Add(1)
	select {
	case s.events <- input.Event{Device: 1, Kind: input.DeviceState, Snapshot: s.snapshot}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestLiveResumeCorrectsLostEdgesAndPreservesUserPause(t *testing.T) {
	for _, paused := range []bool{false, true} {
		t.Run(fmt.Sprint(paused), func(t *testing.T) {
			tl := &testLive{}
			src := &resumeLiveSource{fakeSource: newFakeSource(), snapshot: &input.GamepadState{Buttons: []string{"south"}, Axes: map[string]int16{"leftx": 20000}}}
			cfg := testConfig(t, tl, "", nil, "--dry-run")
			cfg.open = func(input.Options) (eventSource, error) { return src, nil }
			tl.start(t, cfg)
			tl.addPad(t, src.fakeSource)
			button(src.fakeSource, "east", true)
			tl.waitFor(t, tl.stdout, "send +Space\n")
			tl.session.cfg.OnSignal(winsession.SigSleep)
			tl.session.change(winsession.SigSleep, 0, winsession.Sleep)
			tl.waitFor(t, tl.stdout, "send -Space\n")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if paused {
				if err := tl.sup.Pause(ctx, true); err != nil {
					t.Fatal(err)
				}
			}
			tl.session.cfg.OnSignal(winsession.SigWake)
			c := winsession.Change{Signal: winsession.SigWake, Was: winsession.Sleep}
			tl.session.cfg.OnChange(c)
			tl.session.cfg.OnWake(c)
			tl.waitFor(t, tl.log, "gamepads reenumerated after wake")
			tl.sync()
			if src.scans.Load() != 1 || tl.sup.Status().Paused != paused || tl.loop.Blocking()&output.BlockResume != 0 {
				t.Fatal("recovery lost pause or kept the recovery gate")
			}
			if strings.Count(tl.stdout.String(), "send +Space\n") != 1 || strings.Contains(tl.stdout.String(), "send +LCtrl") {
				t.Fatal("held state replayed an output on wake")
			}
			button(src.fakeSource, "south", false) // The sampled held button must be canceled.
			if paused {
				button(src.fakeSource, "east", true)
				tl.sync()
				if strings.Count(tl.stdout.String(), "send +Space\n") != 1 {
					t.Fatal("resume cleared user pause")
				}
				button(src.fakeSource, "east", false)
				if err := tl.sup.Pause(ctx, false); err != nil {
					t.Fatal(err)
				}
			}
			// An empty wake sample does not prove the old held control
			// was physically released on the unavailable input desktop.
			button(src.fakeSource, "east", false)
			button(src.fakeSource, "east", true)
			tl.waitForCount(t, tl.stdout, "send +Space\n", 2)
			button(src.fakeSource, "east", false)
			tl.waitForCount(t, tl.stdout, "send -Space\n", 2)
		})
	}
}

func TestLiveResumeSuppressesLateHeldDownAfterSyntheticUp(t *testing.T) {
	tl := &testLive{}
	src := &resumeLiveSource{fakeSource: newFakeSource(), snapshot: &input.GamepadState{}}
	cfg := testConfig(t, tl, "", nil, "--dry-run")
	cfg.open = func(input.Options) (eventSource, error) { return src, nil }
	tl.start(t, cfg)
	tl.addPad(t, src.fakeSource)
	button(src.fakeSource, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space\n")
	tl.session.cfg.OnSignal(winsession.SigSleep)
	tl.session.change(winsession.SigSleep, 0, winsession.Sleep)
	tl.waitFor(t, tl.stdout, "send -Space\n")
	button(src.fakeSource, "east", false) // SDL desktop-loss release
	tl.sync()
	tl.session.cfg.OnSignal(winsession.SigWake)
	c := winsession.Change{Signal: winsession.SigWake, Was: winsession.Sleep}
	tl.session.cfg.OnChange(c)
	tl.session.cfg.OnWake(c)
	tl.waitFor(t, tl.log, "gamepads reenumerated after wake")
	tl.sync()
	button(src.fakeSource, "east", true) // old physical hold reappears late
	tl.sync()
	if strings.Count(tl.stdout.String(), "send +Space\n") != 1 {
		t.Fatal("late held state replayed output after empty resume scan")
	}
	button(src.fakeSource, "east", false)
	button(src.fakeSource, "east", true)
	tl.waitForCount(t, tl.stdout, "send +Space\n", 2)
	button(src.fakeSource, "east", false)
	tl.waitForCount(t, tl.stdout, "send -Space\n", 2)
}

type resumePanicLogger struct{ slog.Handler }

func (resumePanicLogger) Handle(context.Context, slog.Record) error { panic("bad resume logger") }

func TestResumeWorkerFaultKeepsMappingSafe(t *testing.T) {
	for _, kind := range []string{"goexit", "logger"} {
		t.Run(kind, func(t *testing.T) {
			gate := &resumeTestGate{}
			log := slog.New(slog.DiscardHandler)
			if kind == "logger" {
				log = slog.New(resumePanicLogger{Handler: slog.NewTextHandler(io.Discard, nil)})
			}
			r := newResumeRecovery(context.Background(), func(context.Context) error {
				if kind == "goexit" {
					runtime.Goexit()
				}
				return errors.New("scan failed")
			}, &resumeTestSupervisor{}, gate, log)
			t.Cleanup(r.stop)
			r.signal(winsession.SigWake)
			r.wake(winsession.Change{Signal: winsession.SigWake})
			resumeWait(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.stopped })
			if !gate.blocked() {
				t.Fatal("worker fault opened the gate")
			}
		})
	}
}

func TestResumeScanPanicRetriesSafely(t *testing.T) {
	var n atomic.Int32
	gate := &resumeTestGate{}
	r := newResumeRecovery(context.Background(), func(context.Context) error {
		if n.Add(1) == 1 {
			panic("bad scanner")
		}
		return nil
	}, &resumeTestSupervisor{}, gate, slog.New(slog.DiscardHandler))
	t.Cleanup(r.stop)
	r.signal(winsession.SigWake)
	r.wake(winsession.Change{Signal: winsession.SigWake})
	resumeWait(t, func() bool { return n.Load() == 1 })
	if !gate.blocked() {
		t.Fatal("panic opened gate")
	}
	resumeWait(t, func() bool { return n.Load() == 2 && !gate.blocked() })
}
