package latency

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"
)

func TestSummarize(t *testing.T) {
	// 1..n µs, shuffled: element k (0-based) of the sorted copy is k+1 µs.
	ramp := func(n int) []time.Duration {
		ds := make([]time.Duration, n)
		for i := range ds {
			ds[i] = time.Duration(i+1) * time.Microsecond
		}
		r := rand.New(rand.NewPCG(uint64(n), 5))
		r.Shuffle(n, func(i, j int) { ds[i], ds[j] = ds[j], ds[i] })
		return ds
	}
	us := func(k int) time.Duration { return time.Duration(k) * time.Microsecond }
	cases := []struct {
		name string
		ds   []time.Duration
		want Summary
	}{
		{"none", nil, Summary{}},
		{"1", ramp(1), Summary{N: 1, P50: us(1), P90: us(1), P99: us(1), P999: us(1), Max: us(1)}},
		{"2", ramp(2), Summary{N: 2, P50: us(1), P90: us(2), P99: us(2), P999: us(2), Max: us(2)}},
		{"10", ramp(10), Summary{N: 10, P50: us(5), P90: us(9), P99: us(10), P999: us(10), Max: us(10)}},
		{"100", ramp(100), Summary{N: 100, P50: us(50), P90: us(90), P99: us(99), P999: us(100), Max: us(100)}},
		{"1000", ramp(1000), Summary{N: 1000, P50: us(500), P90: us(900), P99: us(990), P999: us(999), Max: us(1000)}},
		{"1001", ramp(1001), Summary{N: 1001, P50: us(501), P90: us(901), P99: us(991), P999: us(1000), Max: us(1001)}},
		{"negative", []time.Duration{-3, 5, -10, 0}, Summary{N: 4, P50: -3, P90: 5, P99: 5, P999: 5, Max: 5}},
		{"all negative", []time.Duration{-1, -2}, Summary{N: 2, P50: -2, P90: -1, P99: -1, P999: -1, Max: -1}},
		{"ties", []time.Duration{7, 7, 7}, Summary{N: 3, P50: 7, P90: 7, P99: 7, P999: 7, Max: 7}},
	}
	for _, tc := range cases {
		in := slices.Clone(tc.ds)
		if got := Summarize(tc.ds); got != tc.want {
			t.Errorf("%s: Summarize = %+v, want %+v", tc.name, got, tc.want)
		}
		if !slices.Equal(in, tc.ds) {
			t.Errorf("%s: Summarize changed its input", tc.name)
		}
	}
}
