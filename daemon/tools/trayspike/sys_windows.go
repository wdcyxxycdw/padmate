package main

import (
	"fmt"
	"runtime"
	"time"

	"golang.org/x/sys/windows"
)

// cpuTimes is the CPU time of this process so far. Windows accounts it
// in clock ticks (15.625ms by default), so a difference over a window of
// a few seconds is only accurate to a tick or two.
type cpuTimes struct {
	user, kernel time.Duration
	// created is when the process started.
	created time.Time
}

// processTimes reads the CPU time of this process (GetProcessTimes).
func processTimes() (cpuTimes, error) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &created, &exited, &kernel, &user); err != nil {
		return cpuTimes{}, fmt.Errorf("GetProcessTimes: %w", err)
	}
	return cpuTimes{
		user:    filetimeDuration(user),
		kernel:  filetimeDuration(kernel),
		created: time.Unix(0, created.Nanoseconds()),
	}, nil
}

// filetimeDuration converts a FILETIME that holds a duration, in 100ns
// units, rather than a date.
func filetimeDuration(ft windows.Filetime) time.Duration {
	return time.Duration(int64(ft.HighDateTime)<<32|int64(ft.LowDateTime)) * 100
}

// logicalCPUs returns the number of logical processors in all processor
// groups.
func logicalCPUs() int {
	if n := windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS); n > 0 {
		return int(n)
	}
	return runtime.NumCPU()
}

// currentThreadID returns the id of the calling OS thread.
func currentThreadID() uint32 { return windows.GetCurrentThreadId() }

// foregroundPID returns the id of the process that owns the foreground
// window, or 0 when there is none (on the secure desktop, for example).
func foregroundPID() uint32 {
	h := windows.GetForegroundWindow()
	if h == 0 {
		return 0
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(h, &pid); err != nil {
		return 0
	}
	return pid
}
