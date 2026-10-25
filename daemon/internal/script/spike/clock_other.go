//go:build !windows

package spike

import "time"

// epoch is the zero of stamp.
var epoch = time.Now()

// stamp reads the spike's stopwatch, the monotonic clock in nanoseconds.
func stamp() int64 { return int64(time.Since(epoch)) }

// stampFreq is the number of stamp ticks per second.
func stampFreq() int64 { return int64(time.Second) }

// ThreadClockKind says what the watchdog's thread clock measures.
const ThreadClockKind = "wall time: the spike has no thread CPU clock off Windows"

// threadClock stands in for a thread CPU clock with the wall time, which
// is enough for the watchdog tests.
type threadClock struct{ t0 int64 }

func openThreadClock() (*threadClock, error) { return &threadClock{t0: stamp()}, nil }

func (c *threadClock) now() (time.Duration, error) { return stampDuration(stamp() - c.t0), nil }

func (c *threadClock) close() error { return nil }
