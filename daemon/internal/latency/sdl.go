package latency

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"time"
)

// TicksPair is one reading of SDL_GetTicksNS between two Stamps.
type TicksPair struct {
	Before, After Stamp
	NS            uint64
}

// SDLClock maps SDL_GetTicksNS values (input.Event.Time) onto Stamps. SDL's
// tick zero, as a Stamp, lies in [lo, hi].
//
// SDL 3.4.16 (src/timer/SDL_timer.c) computes SDL_GetTicksNS from the
// performance counter q it reads, the counter z it read in SDL_InitTicks
// and the counter frequency f as (q−z)·num/den, where num/den is 1e9/f
// reduced by their gcd. It does so in two uint64 steps,
// (d/den)·num + (d%den)·num/den with d = q−z, which together are exactly
//
//	NS = ⌊(q−z)·1e9/f⌋.
//
// So SDL returns n exactly when q−z ∈ [a(n), b(n)], with
//
//	a(n) = ⌈n·f/1e9⌉       the first count whose NS is ≥ n,
//	b(n) = ⌈(n+1)·f/1e9⌉−1 the last count whose NS is ≤ n.
//
// When f ≤ 1e9, every count has an NS of its own, so the range is one
// count, a(n) = b(n), or empty, b(n) = a(n)−1, for the n that SDL never
// returns: at 10 MHz, a = b = n/100 for the multiples of 100 and no
// others. A pair read with Before ≤ q ≤ After puts z in
// [Before − b(NS), After − a(NS)], and CalibrateSDL intersects the pairs'
// ranges into [lo, hi]; when f ≤ 1e9 its width is at most the narrowest
// After − Before. An event stamped ns was stamped at a q in
// Bounds(ns) = [lo + a(ns), hi + b(ns)].
type SDLClock struct {
	freq   int64
	lo, hi Stamp
}

var errOverflow = errors.New("latency: SDL tick value overflows")

// CalibrateSDL finds SDL's tick zero from pairs, for a counter of freq
// counts per second that SDL also reads (SDL_GetPerformanceCounter is
// QueryPerformanceCounter on Windows). It fails when freq ≤ 0, pairs is
// empty, a pair has After < Before, a value overflows, a pair has an NS
// that SDL cannot return at freq, or the pairs disagree (their ranges do
// not intersect: SDL does not read this counter).
func CalibrateSDL(freq int64, pairs []TicksPair) (SDLClock, error) {
	if freq <= 0 {
		return SDLClock{}, fmt.Errorf("latency: invalid counter frequency %d", freq)
	}
	if len(pairs) == 0 {
		return SDLClock{}, errors.New("latency: no SDL tick readings")
	}
	c := SDLClock{freq: freq, lo: math.MinInt64, hi: math.MaxInt64}
	for i, p := range pairs {
		if p.After < p.Before {
			return SDLClock{}, fmt.Errorf("latency: reading %d has After %d < Before %d", i, p.After, p.Before)
		}
		a, b, err := ticksRange(p.NS, freq)
		if err != nil {
			return SDLClock{}, fmt.Errorf("latency: reading %d (%d ns): %w", i, p.NS, err)
		}
		if b < a {
			return SDLClock{}, fmt.Errorf("latency: reading %d: SDL cannot return %d ns at %d Hz", i, p.NS, freq)
		}
		lo, ok1 := subStamp(p.Before, b)
		hi, ok2 := subStamp(p.After, a)
		if !ok1 || !ok2 {
			return SDLClock{}, fmt.Errorf("latency: reading %d (%d ns): %w", i, p.NS, errOverflow)
		}
		c.lo, c.hi = max(c.lo, lo), min(c.hi, hi)
		if c.lo > c.hi {
			return SDLClock{}, fmt.Errorf("latency: reading %d disagrees with the ones before it: SDL does not read this counter", i)
		}
	}
	return c, nil
}

// Merge intersects two calibrations of the same SDL clock (the start and
// the end of a run). It fails when the freqs differ, either is not a
// calibration, or the brackets are disjoint.
func (c SDLClock) Merge(d SDLClock) (SDLClock, error) {
	if c.freq <= 0 || d.freq <= 0 {
		return SDLClock{}, errors.New("latency: merging an SDLClock that CalibrateSDL did not return")
	}
	if c.freq != d.freq {
		return SDLClock{}, fmt.Errorf("latency: merging SDL clocks of %d Hz and %d Hz", c.freq, d.freq)
	}
	m := SDLClock{freq: c.freq, lo: max(c.lo, d.lo), hi: min(c.hi, d.hi)}
	if m.lo > m.hi {
		return SDLClock{}, fmt.Errorf("latency: SDL tick zero moved: [%d, %d] and [%d, %d] are disjoint", c.lo, c.hi, d.lo, d.hi)
	}
	return m, nil
}

// Bounds returns where the SDL counter reading that gave ns lies. For an
// ns that SDL cannot return, it spans the counts of its two neighbors,
// so lo ≤ hi always. The sums saturate at math.MaxInt64. An ns whose
// a(ns) or b(ns) does not fit an int64 gives MaxInt64, MaxInt64 whatever
// the tick zero; SDL returns no such ns, since it lies over 2^63 counts
// after the zero. The zero SDLClock gives 0, 0.
func (c SDLClock) Bounds(ns uint64) (lo, hi Stamp) {
	if c.freq <= 0 {
		return 0, 0
	}
	a, b, err := ticksRange(ns, c.freq)
	if err != nil {
		return math.MaxInt64, math.MaxInt64
	}
	if b < a {
		a, b = b, a
	}
	return addStamp(c.lo, a), addStamp(c.hi, b)
}

// Mid is the middle of Bounds(ns), rounded down.
func (c SDLClock) Mid(ns uint64) Stamp {
	lo, hi := c.Bounds(ns)
	return lo>>1 + hi>>1 + lo&hi&1 // ⌊(lo+hi)/2⌋ without overflow
}

// Width is hi−lo, the uncertainty of the tick zero. Every Bounds is at
// least this wide, and exactly this wide when the counter runs at ≤ 1 GHz
// and ns is one that SDL returns.
func (c SDLClock) Width() time.Duration {
	if c.freq <= 0 {
		return 0
	}
	return sub(c.hi, c.lo, c.freq)
}

// ticksRange returns a(n) and b(n) of the type comment, for f > 0.
func ticksRange(n uint64, f int64) (a, b int64, err error) {
	const ns = uint64(time.Second)
	if a, err = mulDivSec(n, uint64(f), ns-1); err != nil {
		return 0, 0, err
	}
	if b, err = mulDivSec(n, uint64(f), uint64(f)-1); err != nil {
		return 0, 0, err
	}
	return a, b, nil
}

// mulDivSec returns ⌊(n·f + c)/1e9⌋, or errOverflow when it is above
// math.MaxInt64. c < 2^64, so the sum needs no third word.
func mulDivSec(n, f, c uint64) (int64, error) {
	hi, lo := bits.Mul64(n, f)
	lo, carry := bits.Add64(lo, c, 0)
	hi += carry // hi ≤ 2^64−2 before, so this cannot wrap
	if hi >= uint64(time.Second) {
		return 0, errOverflow
	}
	q, _ := bits.Div64(hi, lo, uint64(time.Second))
	if q > math.MaxInt64 {
		return 0, errOverflow
	}
	return int64(q), nil
}

// subStamp returns s−d for d ≥ 0, and false when it underflows.
func subStamp(s Stamp, d int64) (Stamp, bool) {
	r := s - Stamp(d)
	return r, r <= s
}

// addStamp returns s+d for d ≥ 0, saturating at math.MaxInt64.
func addStamp(s Stamp, d int64) Stamp {
	r := s + Stamp(d)
	if r < s {
		return math.MaxInt64
	}
	return r
}
