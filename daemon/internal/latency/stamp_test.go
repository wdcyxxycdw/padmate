package latency

import (
	"math"
	"math/big"
	"math/rand/v2"
	"slices"
	"testing"
	"time"
)

// The counter frequencies the tests use: QPC on current Windows, the ACPI
// PM timer, the HPET, a 24 MHz crystal and nanoseconds.
var freqs = []int64{10_000_000, 3_579_545, 14_318_180, 24_000_000, 1_000_000_000}

func TestSub(t *testing.T) {
	const p62 = 1 << 62
	sat := time.Duration(math.MaxInt64)
	type tc struct {
		d    int64 // s−t
		want time.Duration
	}
	cases := map[int64][]tc{
		10_000_000: {{0, 0}, {1, 100}, {-1, -100}, {10_000_000, time.Second}, {-10_000_000, -time.Second},
			{p62, sat}, {-p62, -sat},
			{92_233_720_368_547_758, 9_223_372_036_854_775_800}, {-92_233_720_368_547_758, -9_223_372_036_854_775_800},
			{92_233_720_368_547_759, sat}, {-92_233_720_368_547_759, -sat}},
		3_579_545: {{0, 0}, {1, 279}, {-1, -279}, {2, 558}, {-2, -558}, {3_579_545, time.Second}, {-3_579_545, -time.Second},
			{3_579_545_000_000 + 1, 1_000_000_000_000_279}, {p62, sat}, {-p62, -sat}},
		14_318_180: {{0, 0}, {1, 69}, {-1, -69}, {14_318_180, time.Second}, {-14_318_180, -time.Second}, {p62, sat}, {-p62, -sat}},
		24_000_000: {{0, 0}, {1, 41}, {-1, -41}, {24_000_000, time.Second}, {-24_000_000, -time.Second}, {p62, sat}, {-p62, -sat}},
		1_000_000_000: {{0, 0}, {1, 1}, {-1, -1}, {1_000_000_000, time.Second}, {-1_000_000_000, -time.Second},
			{p62, p62}, {-p62, -p62}},
	}
	for _, f := range freqs {
		for _, c := range cases[f] {
			// The same difference at several places on the counter.
			for _, base := range []Stamp{0, 1 << 40, -1 << 40} {
				s, u := base+Stamp(c.d), base
				if got := sub(s, u, f); got != c.want {
					t.Errorf("sub(%d, %d, %d Hz) = %d, want %d", s, u, f, got, c.want)
				}
			}
		}
	}
	// Differences that need 65 bits.
	extremes := []struct {
		s, u Stamp
		freq int64
		want time.Duration
	}{
		{math.MaxInt64, math.MinInt64, 10_000_000, sat},
		{math.MinInt64, math.MaxInt64, 10_000_000, -sat},
		{math.MaxInt64, math.MinInt64, 2_000_000_000, sat}, // (2^64−1)/2 rounds down to MaxInt64 exactly
		{math.MinInt64, math.MaxInt64, 2_000_000_000, -sat},
		{math.MaxInt64, -1, 2_000_000_000, 1 << 62},
		{math.MinInt64, 0, 1_000_000_000, -sat},
	}
	for _, c := range extremes {
		if got := sub(c.s, c.u, c.freq); got != c.want {
			t.Errorf("sub(%d, %d, %d Hz) = %d, want %d", c.s, c.u, c.freq, got, c.want)
		}
	}
	if got := Stamp(Freq()).Sub(0); got != time.Second {
		t.Errorf("Stamp(Freq()).Sub(0) = %v, want 1s", got)
	}
}

// TestSubOracle checks sub against big-integer arithmetic.
func TestSubOracle(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	fs := append(slices.Clone(freqs), 1, 2_000_000_000, math.MaxInt64)
	for range 20000 {
		f := fs[r.IntN(len(fs))]
		s, u := Stamp(r.Int64()), Stamp(r.Int64())
		if r.IntN(2) == 0 { // half of them within ±2^39 counts
			u = s - Stamp(r.Int64N(1<<40)-1<<39)
		}
		want := oracleSub(s, u, f)
		if got := sub(s, u, f); got != want {
			t.Fatalf("sub(%d, %d, %d Hz) = %d, want %d", s, u, f, got, want)
		}
	}
}

func oracleSub(s, u Stamp, f int64) time.Duration {
	d := new(big.Int).Sub(big.NewInt(int64(s)), big.NewInt(int64(u)))
	d.Mul(d, big.NewInt(int64(time.Second)))
	d.Quo(d, big.NewInt(f)) // truncates toward zero
	switch {
	case d.Cmp(big.NewInt(math.MaxInt64)) > 0:
		return math.MaxInt64
	case d.Cmp(big.NewInt(-math.MaxInt64)) < 0:
		return -math.MaxInt64
	}
	return time.Duration(d.Int64())
}

func TestNow(t *testing.T) {
	if Freq() <= 0 {
		t.Fatalf("Freq() = %d", Freq())
	}
	prev := Now()
	for i := range 1000 {
		s := Now()
		if s < prev {
			t.Fatalf("read %d: Now() = %d after %d", i, s, prev)
		}
		prev = s
	}
	if n := testing.AllocsPerRun(100, func() { _ = Now() }); n != 0 {
		t.Errorf("Now allocates %v times", n)
	}
}
