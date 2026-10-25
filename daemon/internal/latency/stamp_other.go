//go:build !windows

package latency

import "time"

// epoch is the zero of Now.
var epoch = time.Now()

// Now reads the monotonic clock, in nanoseconds since the package was
// initialized.
func Now() Stamp { return Stamp(time.Since(epoch)) }

// Freq returns 1e9: Stamps are nanoseconds.
func Freq() int64 { return int64(time.Second) }
