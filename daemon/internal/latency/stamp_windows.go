package latency

import "sync"

// Now reads QueryPerformanceCounter, which cannot fail since Windows XP,
// so its error is ignored.
func Now() Stamp {
	var c int64
	_ = queryPerformanceCounter(&c)
	return Stamp(c)
}

// Freq returns QueryPerformanceFrequency, which is fixed at boot.
func Freq() int64 { return frequency() }

var frequency = sync.OnceValue(func() int64 {
	var f int64
	_ = queryPerformanceFrequency(&f) // cannot fail since Windows XP
	return f
})
