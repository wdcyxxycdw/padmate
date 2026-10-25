package latency

import (
	"math"
	"math/bits"
	"time"
)

// Stamp is a reading of the high-resolution counter: a
// QueryPerformanceCounter count on Windows, nanoseconds of the monotonic
// clock elsewhere. Freq gives its counts per second. Now does not
// allocate after its first call, which on Windows looks up
// QueryPerformanceCounter, so a hook callback can take a Stamp once Now
// has run outside it.
type Stamp int64

// Sub returns s−t as a Duration, rounded toward zero. It is exact for any
// difference of two Stamps (128-bit arithmetic) and saturates at
// ±math.MaxInt64.
func (s Stamp) Sub(t Stamp) time.Duration { return sub(s, t, Freq()) }

// sub is Sub for a counter of freq > 0 counts per second. It takes both
// Stamps, not their difference, because the difference of two int64s
// needs 65 bits.
func sub(s, t Stamp, freq int64) time.Duration {
	neg := s < t
	var mag uint64 // |s−t| < 2^64, exact in modular arithmetic
	if neg {
		mag = uint64(t) - uint64(s)
	} else {
		mag = uint64(s) - uint64(t)
	}
	hi, lo := bits.Mul64(mag, uint64(time.Second))
	f := uint64(freq)
	if hi >= f { // the quotient needs more than 64 bits
		return saturate(neg)
	}
	q, _ := bits.Div64(hi, lo, f)
	if q > math.MaxInt64 {
		return saturate(neg)
	}
	if neg {
		return -time.Duration(q)
	}
	return time.Duration(q)
}

func saturate(neg bool) time.Duration {
	if neg {
		return -math.MaxInt64
	}
	return math.MaxInt64
}
