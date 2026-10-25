package output_test

import (
	"runtime"
	"sync"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// threadSender forwards to a simtest.SyncRecorder and records the OS
// thread of every Send.
type threadSender struct {
	*simtest.SyncRecorder
	mu  sync.Mutex
	ids []uint32
}

func (s *threadSender) Send(events []output.KeyEvent) (int, error) {
	s.mu.Lock()
	s.ids = append(s.ids, windows.GetCurrentThreadId())
	s.mu.Unlock()
	return s.SyncRecorder.Send(events)
}

// TestLoopLockOneThread: with Lock, every Send runs on one OS thread,
// whether it comes from Do calls of goroutines on other threads, a timer
// wake or the release after a panic.
func TestLoopLockOneThread(t *testing.T) {
	const goroutines, commands = 4, 25
	fake := clock.NewFake()
	s := &threadSender{SyncRecorder: simtest.NewSyncRecorder(fake)}
	l := output.StartLoop(fake, s, output.LoopOptions{Lock: true})
	t.Cleanup(func() {
		_ = l.Stop()
		s.With(func(r *simtest.Recorder) { simtest.AssertNoneHeld(t, nil, r) })
	})

	if err := l.Do(tap(ownerA, "Y", 50*ms)); err != nil {
		t.Fatal(err)
	}
	fake.Advance(50 * ms)
	s.WaitFor(t, "+Y@0 -Y@50")
	var wg sync.WaitGroup
	for g := range goroutines {
		o := output.Owner{Slot: uint8(g), Gen: 1, Source: output.SourceDecl, Cycle: 1}
		key := string(rune('A' + g)) // a key per goroutine, so that every command sends
		wg.Go(func() {
			runtime.LockOSThread() // a thread of its own
			defer runtime.UnlockOSThread()
			for range commands {
				if err := l.Do(down(o, key)); err != nil {
					t.Error(err)
				}
				if err := l.Do(up(o, key)); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	err := l.Do(func(a *output.Arbiter) error {
		if err := a.Down(ownerA, keys.MustParse("Space")); err != nil {
			return err
		}
		panic("boom")
	})
	checkPanic(t, l, err, "boom", "-Space", nil)

	s.mu.Lock()
	defer s.mu.Unlock()
	// The tap's two batches, a press and a release per command, and the
	// press and release of Space.
	if want := 2 + goroutines*commands*2 + 2; len(s.ids) != want {
		t.Errorf("%d Send calls, want %d", len(s.ids), want)
	}
	threads := map[uint32]int{}
	for _, id := range s.ids {
		threads[id]++
	}
	if len(threads) != 1 {
		t.Errorf("Send ran on %d OS threads (calls by thread id: %v), want 1", len(threads), threads)
	}
}

// TestLoopSweepOnLoopThread: the sweep's Send runs on the Loop's OS
// thread, like every command's, not on the thread that started the Loop
// (CLAUDE.md invariant 3).
func TestLoopSweepOnLoopThread(t *testing.T) {
	path := heldPath(t)
	writeHeld(t, path, "LCtrl")
	fake := clock.NewFake()
	s := &threadSender{SyncRecorder: simtest.NewSyncRecorder(fake)}
	type started struct {
		l      *output.Loop
		caller uint32
	}
	ready := make(chan started)
	release := make(chan struct{})
	go func() {
		// A thread of its own, like padmated's startLive goroutine.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		l := output.StartLoop(fake, s, output.LoopOptions{Lock: true, HeldFile: path, KeyDown: func(output.KeyEvent) bool { return true }})
		if res := l.Swept(); res.Err != nil || eventsTrace(res.Released) != "-LCtrl -vk:0xE8" {
			t.Errorf("Swept() = %+v, want LCtrl and the mask released", res)
		}
		for range 5 {
			if err := l.Do(tap(ownerA, "Z", 0)); err != nil {
				t.Error(err)
			}
		}
		ready <- started{l, windows.GetCurrentThreadId()}
		<-release
	}()
	st := <-ready
	defer close(release)
	if err := st.l.Stop(); err != nil {
		t.Fatal(err)
	}
	s.With(func(r *simtest.Recorder) { simtest.AssertNoneHeld(t, nil, r) })
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.ids) != 6 {
		t.Fatalf("%d Send calls, want the sweep's and 5 taps", len(s.ids))
	}
	for i, id := range s.ids {
		if id != s.ids[0] || id == st.caller {
			t.Errorf("Send %d ran on thread %d; want every Send on the Loop's thread %d, not the caller's %d", i, id, s.ids[0], st.caller)
		}
	}
	wantNoHeld(t, path)
}
