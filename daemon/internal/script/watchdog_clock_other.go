//go:build !windows

package script

import "time"

// The script host is cross-platform for tests; non-Windows builds use wall
// time as the watchdog's substitute for QueryThreadCycleTime.
type threadCPUClock interface {
	now() (time.Duration, error)
	close() error
}

type wallThreadClock struct{ start time.Time }

func openThreadCPUClock() (threadCPUClock, error)      { return &wallThreadClock{start: time.Now()}, nil }
func (c *wallThreadClock) now() (time.Duration, error) { return time.Since(c.start), nil }
func (*wallThreadClock) close() error                  { return nil }
