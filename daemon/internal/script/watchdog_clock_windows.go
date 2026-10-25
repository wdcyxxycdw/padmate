package script

import (
	"errors"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type threadCPUClock interface {
	now() (time.Duration, error)
	close() error
}

type windowsThreadClock struct {
	handle   windows.Handle
	perMicro float64
}

var (
	kernel32                      = windows.NewLazySystemDLL("kernel32.dll")
	procQueryThreadCycleTime      = kernel32.NewProc("QueryThreadCycleTime")
	procQueryPerformanceCounter   = kernel32.NewProc("QueryPerformanceCounter")
	procQueryPerformanceFrequency = kernel32.NewProc("QueryPerformanceFrequency")
)

var cycleCalibration = sync.OnceValues(func() (float64, error) {
	result := make(chan struct {
		rate float64
		err  error
	}, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		rate, err := calibrateCycles()
		result <- struct {
			rate float64
			err  error
		}{rate, err}
	}()
	r := <-result
	return r.rate, r.err
})

func openThreadCPUClock() (threadCPUClock, error) {
	rate, err := cycleCalibration()
	if err != nil {
		return nil, err
	}
	h, err := windows.OpenThread(windows.THREAD_QUERY_LIMITED_INFORMATION, false, windows.GetCurrentThreadId())
	if err != nil {
		return nil, fmt.Errorf("OpenThread: %w", err)
	}
	return &windowsThreadClock{handle: h, perMicro: rate}, nil
}

func (c *windowsThreadClock) now() (time.Duration, error) {
	var cycles uint64
	if r, _, err := procQueryThreadCycleTime.Call(uintptr(c.handle), uintptr(unsafe.Pointer(&cycles))); r == 0 {
		return 0, fmt.Errorf("QueryThreadCycleTime: %w", err)
	}
	return time.Duration(float64(cycles) / c.perMicro * float64(time.Microsecond)), nil
}

func (c *windowsThreadClock) close() error { return windows.CloseHandle(c.handle) }

// calibrateCycles measures ten one-millisecond QPC spins per attempt. It uses
// the fastest rate from an agreeing pair among the top three samples, so an
// isolated high outlier cannot make the CPU budget run late.
func calibrateCycles() (float64, error) {
	var freq, startQ, endQ int64
	if r, _, err := procQueryPerformanceFrequency.Call(uintptr(unsafe.Pointer(&freq))); r == 0 || freq <= 0 {
		return 0, fmt.Errorf("QueryPerformanceFrequency: %w", err)
	}
	thread := windows.CurrentThread()
	for range 3 {
		samples := make([]float64, 0, 10)
		for range 10 {
			var startC, endC uint64
			if r, _, err := procQueryThreadCycleTime.Call(uintptr(thread), uintptr(unsafe.Pointer(&startC))); r == 0 {
				return 0, fmt.Errorf("QueryThreadCycleTime: %w", err)
			}
			if r, _, err := procQueryPerformanceCounter.Call(uintptr(unsafe.Pointer(&startQ))); r == 0 {
				return 0, fmt.Errorf("QueryPerformanceCounter: %w", err)
			}
			end := startQ + freq/int64(time.Second/time.Millisecond)
			for {
				if r, _, err := procQueryPerformanceCounter.Call(uintptr(unsafe.Pointer(&endQ))); r == 0 {
					return 0, fmt.Errorf("QueryPerformanceCounter: %w", err)
				}
				if endQ >= end {
					break
				}
			}
			if r, _, err := procQueryThreadCycleTime.Call(uintptr(thread), uintptr(unsafe.Pointer(&endC))); r == 0 {
				return 0, fmt.Errorf("QueryThreadCycleTime: %w", err)
			}
			if d := endQ - startQ; d > 0 && endC > startC {
				rate := float64(endC-startC) / (float64(d) / float64(freq) * 1e6)
				samples = append(samples, rate)
			}
		}
		if rate, ok := consistentCycleRate(samples); ok {
			return rate, nil
		}
	}
	return 0, errors.New("QueryThreadCycleTime calibration had no agreeing samples in three attempts")
}

// consistentCycleRate chooses the faster rate of an agreeing top-three pair.
// A slower sample may have lost cycles to preemption; rates within 5% provide
// the consistency check required before trusting the selected conversion.
func consistentCycleRate(samples []float64) (float64, bool) {
	if len(samples) < 3 {
		return 0, false
	}
	sort.Float64s(samples)
	top := samples[len(samples)-3:]
	for i := 2; i > 0; i-- {
		for j := i - 1; j >= 0; j-- {
			if top[j] > 0 && (top[i]-top[j])/top[i] <= 0.05 {
				return top[i], true
			}
		}
	}
	return 0, false
}
