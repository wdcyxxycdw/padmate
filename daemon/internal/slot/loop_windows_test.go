package slot_test

import (
	"runtime"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// TestRunLoopLockOneThread: with Lock, the jobs of goroutines on other
// threads, the edges and the engine's decisions all run on one OS thread.
func TestRunLoopLockOneThread(t *testing.T) {
	const goroutines, jobs = 4, 25
	var ids []uint32 // only the loop goroutine touches it until RunLoop returns
	record := func() { ids = append(ids, windows.GetCurrentThreadId()) }
	h := startSlotLoop(t, loopOpts{lock: true, trace: func(engine.Decision) { record() }})

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			runtime.LockOSThread() // a thread of its own
			defer runtime.UnlockOSThread()
			for range jobs {
				select {
				case h.jobs <- func(*slot.Slot) { record() }:
				case <-time.After(waitLimit):
					t.Error("the loop takes no jobs")
					return
				}
			}
		})
	}
	wg.Wait()
	h.button("east", true)
	h.button("east", false)
	close(h.edges)
	if err := h.wait(); err != nil {
		t.Fatalf("RunLoop = %v, want nil", err)
	}
	expect(t, h.rec, "+Space@0 -Space@0")

	// The jobs and the decisions of east's press and release.
	if want := goroutines*jobs + 2; len(ids) != want {
		t.Errorf("%d calls recorded, want %d", len(ids), want)
	}
	threads := map[uint32]int{}
	for _, id := range ids {
		threads[id]++
	}
	if len(threads) != 1 {
		t.Errorf("the loop ran on %d OS threads (calls by thread id: %v), want 1", len(threads), threads)
	}
}
