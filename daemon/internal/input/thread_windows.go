package input

import "golang.org/x/sys/windows"

// currentThreadID returns the id of the calling OS thread.
func currentThreadID() uint32 { return windows.GetCurrentThreadId() }
