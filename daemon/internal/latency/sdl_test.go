package latency

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"
)

// maxSDLFreq is the highest counter frequency SDL accepts (SDL_InitTicks
// asserts it fits a Uint32); above 1e9 one NS spans several counts.
const maxSDLFreq = math.MaxUint32

// sdlTicksNS is SDL_GetTicksNS of SDL 3.4.16 (src/timer/SDL_timer.c) for a
// counter d counts past tick_start, at freq counts per second. SDL never
// defines USE_128BIT_MATH, so this is its uint64 path, with num/den reduced
// by SDL_CalculateGCD.
func sdlTicksNS(d uint64, freq uint32) uint64 {
	g := uint64(gcd(1_000_000_000, freq))
	num, den := 1_000_000_000/g, uint64(freq)/g
	return (d/den)*num + (d%den)*num/den
}

func gcd(a, b uint32) uint32 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// consistent reports whether SDL, with its tick zero at z, can have
// returned p.NS from a counter read in [p.Before, p.After], and not
// before z. p.NS must be one that sdlTicksNS returns. sdlTicksNS is
// non-decreasing and, when it skips values (f ≤ 1e9), strictly
// increasing, so the check is exact.
func consistent(z Stamp, p TicksPair, f uint32) bool {
	lo, hi := max(p.Before, z), p.After
	return lo <= hi && sdlTicksNS(uint64(lo-z), f) <= p.NS && p.NS <= sdlTicksNS(uint64(hi-z), f)
}

// checkExact checks c against sdlTicksNS, not ticksRange: [c.lo, c.hi] is
// exactly the set of tick zeros that fit every pair, and Bounds(p.NS) is
// exactly the counts that give p.NS from a zero in it.
func checkExact(t *testing.T, name string, c SDLClock, pairs []TicksPair) {
	t.Helper()
	f := uint32(c.freq)
	fits := func(z Stamp) bool {
		for _, p := range pairs {
			if !consistent(z, p, f) {
				return false
			}
		}
		return true
	}
	// The zeros that fit one pair form an interval, so those that fit all do.
	if !fits(c.lo) || !fits(c.hi) || fits(c.lo-1) || fits(c.hi+1) {
		t.Errorf("%s: [%d, %d] is not the set of tick zeros that fit the readings", name, c.lo, c.hi)
	}
	for i, p := range pairs {
		lo, hi := c.Bounds(p.NS)
		a, b := uint64(lo-c.lo), uint64(hi-c.hi) // a(NS) and b(NS)
		if sdlTicksNS(a, f) != p.NS || (a > 0 && sdlTicksNS(a-1, f) >= p.NS) ||
			sdlTicksNS(b, f) != p.NS || sdlTicksNS(b+1, f) <= p.NS {
			t.Errorf("%s, reading %d: Bounds(%d) = [%d, %d] gives counts %d..%d past the zero, not those of %d ns",
				name, i, p.NS, lo, hi, a, b, p.NS)
		}
	}
}

// TestSDLFormula checks the premise of SDLClock: SDL's two uint64 steps
// are exactly ⌊d·1e9/f⌋.
func TestSDLFormula(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	fs := append(slices.Clone(freqs), maxSDLFreq)
	for range 20000 {
		f := fs[r.IntN(len(fs))]
		d := r.Uint64N(1 << 50)
		want := new(big.Int).Mul(new(big.Int).SetUint64(d), big.NewInt(int64(time.Second)))
		want.Quo(want, big.NewInt(f))
		if got := sdlTicksNS(d, uint32(f)); got != want.Uint64() {
			t.Fatalf("sdlTicksNS(%d, %d) = %d, want %d", d, f, got, want)
		}
	}
}

// calCase is a set of synthetic readings of an SDL clock whose tick zero
// is z.
type calCase struct {
	name  string
	freq  int64
	z     Stamp
	width int64 // After − Before of every pair
	pairs []TicksPair
	qs    []Stamp // the counter SDL read for each pair
}

func calCases() []calCase {
	var cs []calCase
	for _, f := range append(slices.Clone(freqs), maxSDLFreq) {
		for _, w := range []int64{0, 1, 50, 5000} {
			for _, z := range []Stamp{123_456_789, 0, -5_000_000_000_000} {
				c := calCase{name: fmt.Sprintf("%dHz/width%d/z%d", f, w, z), freq: f, z: z, width: w}
				for i, d := range []int64{0, 1, 7, f - 1, f, 1000*f + 7, 86400 * f} {
					q := z + Stamp(d)
					before := q - Stamp(int64(i)*w/6) // q moves from Before to After
					c.pairs = append(c.pairs, TicksPair{Before: before, After: before + Stamp(w), NS: sdlTicksNS(uint64(d), uint32(f))})
					c.qs = append(c.qs, q)
				}
				cs = append(cs, c)
			}
		}
	}
	return cs
}

func TestCalibrateSDL(t *testing.T) {
	for _, tc := range calCases() {
		c, err := CalibrateSDL(tc.freq, tc.pairs)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if c.lo > tc.z || tc.z > c.hi {
			t.Errorf("%s: tick zero %d not in [%d, %d]", tc.name, tc.z, c.lo, c.hi)
		}
		checkExact(t, tc.name, c, tc.pairs)
		if tc.freq <= int64(time.Second) {
			// Every count has an NS of its own: the bracket is exact.
			if int64(c.hi-c.lo) > tc.width {
				t.Errorf("%s: [%d, %d] is wider than the brackets", tc.name, c.lo, c.hi)
			}
			if tc.width == 0 && (c.lo != tc.z || c.hi != tc.z) {
				t.Errorf("%s: [%d, %d], want [%d, %d]", tc.name, c.lo, c.hi, tc.z, tc.z)
			}
		}
		if got, want := c.Width(), sub(c.hi, c.lo, tc.freq); got != want {
			t.Errorf("%s: Width() = %v, want %v", tc.name, got, want)
		}
	}

	good := calCases()[0].pairs // 10 MHz, width 0
	shifted := slices.Clone(good)
	shifted[3].Before += 10_000_000
	shifted[3].After += 10_000_000
	errs := []struct {
		name     string
		freq     int64
		pairs    []TicksPair
		want     string
		overflow bool
	}{
		{name: "freq 0", freq: 0, pairs: good, want: "invalid counter frequency"},
		{name: "freq negative", freq: -10_000_000, pairs: good, want: "invalid counter frequency"},
		{name: "no pairs", freq: 10_000_000, want: "no SDL tick readings"},
		{name: "After < Before", freq: 10_000_000, pairs: []TicksPair{{Before: 10, After: 9}}, want: "After 9 < Before 10"},
		{name: "one pair 1 s late", freq: 10_000_000, pairs: shifted, want: "reading 3 disagrees"},
		{name: "a(n) above MaxInt64", freq: 1_000_000_000, pairs: []TicksPair{{NS: math.MaxUint64}}, overflow: true},
		{name: "n·f needs a third word", freq: 4_000_000_000, pairs: []TicksPair{{NS: 1 << 63}}, overflow: true},
		{name: "Before − b(n) underflows", freq: 10_000_000,
			pairs: []TicksPair{{Before: math.MinInt64 + 5, After: math.MinInt64 + 5, NS: 1000}}, overflow: true},
		{name: "NS that SDL cannot return", freq: 10_000_000, pairs: []TicksPair{{NS: 150}}, want: "SDL cannot return 150 ns"},
	}
	for _, tc := range errs {
		c, err := CalibrateSDL(tc.freq, tc.pairs)
		switch {
		case err == nil:
			t.Errorf("%s: no error, got %+v", tc.name, c)
		case tc.overflow && !errors.Is(err, errOverflow):
			t.Errorf("%s: %v, want an overflow", tc.name, err)
		case !strings.Contains(err.Error(), tc.want):
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		case c != SDLClock{}:
			t.Errorf("%s: %+v with the error", tc.name, c)
		}
	}
}

// TestCalibrateSDLProperty replaces a fuzz target: the input is
// arithmetic, not parsed bytes.
func TestCalibrateSDLProperty(t *testing.T) {
	r := rand.New(rand.NewPCG(0x5eed, 0xca1))
	fs := append(slices.Clone(freqs), maxSDLFreq)
	for i := range 2000 {
		f := fs[r.IntN(len(fs))]
		z := Stamp(r.Int64N(1<<51) - 1<<50)
		d := r.Uint64N(1 << 40) // SDL's products stay below 2^64
		var pairs []TicksPair
		var qs []Stamp
		narrowest := int64(math.MaxInt64)
		for range 1 + r.IntN(64) {
			d += 1 + r.Uint64N(1<<30)
			q := z + Stamp(d)
			p := TicksPair{Before: q - Stamp(r.IntN(5001)), After: q + Stamp(r.IntN(5001)), NS: sdlTicksNS(d, uint32(f))}
			pairs = append(pairs, p)
			qs = append(qs, q)
			narrowest = min(narrowest, int64(p.After-p.Before))
		}
		c, err := CalibrateSDL(f, pairs)
		if err != nil {
			t.Fatalf("case %d (%d Hz, z %d): %v", i, f, z, err)
		}
		if c.lo > z || z > c.hi {
			t.Fatalf("case %d (%d Hz): tick zero %d not in [%d, %d]", i, f, z, c.lo, c.hi)
		}
		if f <= int64(time.Second) && int64(c.hi-c.lo) > narrowest {
			t.Fatalf("case %d (%d Hz): [%d, %d] is wider than the narrowest bracket, %d counts", i, f, c.lo, c.hi, narrowest)
		}
		checkExact(t, fmt.Sprintf("case %d (%d Hz)", i, f), c, pairs)
		for j, p := range pairs {
			if lo, hi := c.Bounds(p.NS); qs[j] < lo || qs[j] > hi {
				t.Fatalf("case %d (%d Hz), reading %d: q %d not in Bounds(%d) = [%d, %d]", i, f, j, qs[j], p.NS, lo, hi)
			}
		}
	}
}

func TestBounds(t *testing.T) {
	for _, tc := range calCases() {
		c, err := CalibrateSDL(tc.freq, tc.pairs)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		for i, p := range tc.pairs {
			lo, hi := c.Bounds(p.NS)
			if tc.qs[i] < lo || tc.qs[i] > hi {
				t.Errorf("%s, reading %d: q %d not in Bounds(%d) = [%d, %d]", tc.name, i, tc.qs[i], p.NS, lo, hi)
			}
			if m := c.Mid(p.NS); m < lo || m > hi {
				t.Errorf("%s, reading %d: Mid %d not in [%d, %d]", tc.name, i, m, lo, hi)
			}
			if tc.freq <= int64(time.Second) && hi-lo != c.hi-c.lo {
				t.Errorf("%s, reading %d: Bounds is %d counts wide, the tick zero %d", tc.name, i, hi-lo, c.hi-c.lo)
			}
		}
	}

	const z = 1000
	exact := SDLClock{freq: 10_000_000, lo: z, hi: z}
	edges := []struct {
		name   string
		c      SDLClock
		ns     uint64
		lo, hi Stamp
	}{
		{"10 MHz, 100 ns", exact, 100, z + 1, z + 1},
		{"10 MHz, 200 ns", exact, 200, z + 2, z + 2},
		{"10 MHz, 150 ns, which SDL cannot return", exact, 150, z + 1, z + 2},
		{"zero SDLClock", SDLClock{}, 100, 0, 0},
		{"overflow", SDLClock{freq: 1_000_000_000}, math.MaxUint64, math.MaxInt64, math.MaxInt64},
		{"saturation", SDLClock{freq: 10_000_000, lo: math.MaxInt64 - 5, hi: math.MaxInt64 - 5}, 1000, math.MaxInt64, math.MaxInt64},
		{"whole range", SDLClock{freq: 1_000_000_000, lo: math.MinInt64, hi: math.MaxInt64}, 0, math.MinInt64, math.MaxInt64},
	}
	for _, e := range edges {
		if lo, hi := e.c.Bounds(e.ns); lo != e.lo || hi != e.hi {
			t.Errorf("%s: Bounds(%d) = [%d, %d], want [%d, %d]", e.name, e.ns, lo, hi, e.lo, e.hi)
		}
	}
	mids := []struct {
		c    SDLClock
		want Stamp
	}{
		{SDLClock{freq: 1_000_000_000, lo: 3, hi: 6}, 4},
		{SDLClock{freq: 1_000_000_000, lo: -6, hi: -3}, -5},
		{SDLClock{freq: 1_000_000_000, lo: math.MinInt64, hi: math.MaxInt64}, -1},
		{SDLClock{freq: 1_000_000_000, lo: math.MaxInt64, hi: math.MaxInt64}, math.MaxInt64},
		{SDLClock{freq: 1_000_000_000, lo: math.MinInt64, hi: math.MinInt64}, math.MinInt64},
	}
	for _, m := range mids {
		if got := m.c.Mid(0); got != m.want {
			t.Errorf("Mid of [%d, %d] = %d, want %d", m.c.lo, m.c.hi, got, m.want)
		}
	}
}

func TestMerge(t *testing.T) {
	c := SDLClock{freq: 10_000_000, lo: 10, hi: 20}
	ok := []struct {
		d, want SDLClock
	}{
		{SDLClock{freq: 10_000_000, lo: 15, hi: 30}, SDLClock{freq: 10_000_000, lo: 15, hi: 20}},
		{SDLClock{freq: 10_000_000, lo: 0, hi: 12}, SDLClock{freq: 10_000_000, lo: 10, hi: 12}},
		{SDLClock{freq: 10_000_000, lo: 12, hi: 14}, SDLClock{freq: 10_000_000, lo: 12, hi: 14}},
		{SDLClock{freq: 10_000_000, lo: 20, hi: 30}, SDLClock{freq: 10_000_000, lo: 20, hi: 20}},
	}
	for _, tc := range ok {
		for _, m := range [][2]SDLClock{{c, tc.d}, {tc.d, c}} {
			got, err := m[0].Merge(m[1])
			if err != nil || got != tc.want {
				t.Errorf("%+v.Merge(%+v) = %+v, %v; want %+v", m[0], m[1], got, err, tc.want)
			}
		}
	}
	bad := []struct {
		d    SDLClock
		want string
	}{
		{SDLClock{freq: 10_000_000, lo: 21, hi: 30}, "disjoint"},
		{SDLClock{freq: 10_000_000, lo: 0, hi: 9}, "disjoint"},
		{SDLClock{freq: 24_000_000, lo: 10, hi: 20}, "merging SDL clocks of"},
		{SDLClock{}, "CalibrateSDL did not return"},
	}
	for _, tc := range bad {
		for _, m := range [][2]SDLClock{{c, tc.d}, {tc.d, c}} {
			if got, err := m[0].Merge(m[1]); err == nil || !strings.Contains(err.Error(), tc.want) || got != (SDLClock{}) {
				t.Errorf("%+v.Merge(%+v) = %+v, %v; want an error with %q", m[0], m[1], got, err, tc.want)
			}
		}
	}

	// The start and the end of a run.
	for _, tc := range calCases() {
		start, err1 := CalibrateSDL(tc.freq, tc.pairs[:3])
		end, err2 := CalibrateSDL(tc.freq, tc.pairs[3:])
		m, err3 := start.Merge(end)
		if err := errors.Join(err1, err2, err3); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if m.lo > tc.z || tc.z > m.hi || m.Width() > min(start.Width(), end.Width()) {
			t.Errorf("%s: merged [%d, %d] of [%d, %d] and [%d, %d]", tc.name, m.lo, m.hi, start.lo, start.hi, end.lo, end.hi)
		}
		checkExact(t, tc.name+", merged", m, tc.pairs)
	}

	if w := c.Width(); w != time.Microsecond {
		t.Errorf("Width of 10 counts at 10 MHz = %v, want 1µs", w)
	}
	if w := (SDLClock{}).Width(); w != 0 {
		t.Errorf("Width of the zero SDLClock = %v", w)
	}
}
