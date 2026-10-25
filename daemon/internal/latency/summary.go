package latency

import (
	"slices"
	"time"
)

// Summary is a distribution of durations, by nearest rank.
type Summary struct {
	N                        int
	P50, P90, P99, P999, Max time.Duration
}

// Summarize sorts a copy of ds and takes nearest-rank percentiles: the
// p-quantile is element ⌈p·N⌉−1, the rule SPK-03 used. No samples give
// the zero Summary. ds is not modified. Negative values are allowed.
//
// It duplicates the percentile code of internal/script/spike on purpose:
// only tools/spk03 may import the spike (depguard). The ranks are
// computed in integers, so that no p·N misrounds.
func Summarize(ds []time.Duration) Summary {
	n := len(ds)
	if n == 0 {
		return Summary{}
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	rank := func(perTenThousand int64) time.Duration {
		return s[max((perTenThousand*int64(n)+9999)/10000-1, 0)]
	}
	return Summary{N: n, P50: rank(5000), P90: rank(9000), P99: rank(9900), P999: rank(9990), Max: s[n-1]}
}
