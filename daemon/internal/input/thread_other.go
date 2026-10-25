//go:build !windows

package input

// currentThreadID returns 0: Source.ThreadID is only reported on Windows.
func currentThreadID() uint32 { return 0 }
