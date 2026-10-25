package spike

import (
	"fmt"
	"runtime"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// qpcFrequency is the QueryPerformanceCounter frequency, fixed at boot.
var qpcFrequency = sync.OnceValue(func() int64 {
	var f int64
	_ = queryPerformanceFrequency(&f) // cannot fail since Windows XP
	return f
})

// stamp reads the spike's stopwatch, QueryPerformanceCounter. time.Now
// is no use for microseconds on Windows: it reads the interrupt time,
// which moves in steps of 0.5–2 ms.
func stamp() int64 { return qpc{}.now() }

// stampFreq is the number of stamp ticks per second.
func stampFreq() int64 { return qpcFrequency() }

// ThreadClockKind says what the watchdog's thread clock measures.
const ThreadClockKind = "thread CPU time: QueryThreadCycleTime, calibrated against QPC at startup"

// cycleRate is the startup calibration of QueryThreadCycleTime, in cycles
// per microsecond, run once on a locked thread of its own.
var cycleRate = sync.OnceValues(func() (float64, error) {
	type result struct {
		rate float64
		err  error
	}
	done := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		r, err := startupCalibration(qpc{qpcFrequency()})
		done <- result{r, err}
	}()
	r := <-done
	return r.rate, r.err
})

// threadClock reads the CPU time of one thread, from any thread.
type threadClock struct {
	thread   windows.Handle
	perMicro float64
}

// openThreadClock opens a clock for the calling thread, which must be
// locked to its goroutine for as long as the clock is used.
func openThreadClock() (*threadClock, error) {
	rate, err := cycleRate()
	if err != nil {
		return nil, err
	}
	h, err := windows.OpenThread(windows.THREAD_QUERY_LIMITED_INFORMATION, false, windows.GetCurrentThreadId())
	if err != nil {
		return nil, fmt.Errorf("OpenThread: %w", err)
	}
	return &threadClock{thread: h, perMicro: rate}, nil
}

func (c *threadClock) now() (time.Duration, error) {
	cy, err := cycles(c.thread)
	if err != nil {
		return 0, err
	}
	return time.Duration(float64(cy) / c.perMicro * float64(time.Microsecond)), nil
}

func (c *threadClock) close() error {
	return windows.CloseHandle(c.thread)
}
