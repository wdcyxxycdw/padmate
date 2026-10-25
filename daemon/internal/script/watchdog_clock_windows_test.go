package script

import (
	"runtime"
	"testing"
	"time"
)

func TestConsistentCycleRate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		samples []float64
		want    float64
		ok      bool
	}{
		{"agrees with fastest", []float64{1, 2, 2.97, 3, 3.02}, 3.02, true},
		{"rejects isolated high outlier", []float64{1, 2, 2.95, 3, 5}, 3, true},
		{"rejects inconsistent top three", []float64{1, 2, 3, 4, 5}, 0, false},
		{"too few samples", []float64{3, 3}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := consistentCycleRate(tc.samples)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("consistentCycleRate = %g, %v; want %g, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestWindowsThreadClockDoesNotOvercountBusyThread(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	c, err := openThreadCPUClock()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.close() }()
	cpu0, err := c.now()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	var work uint64 = 1
	for time.Since(start) < 200*time.Millisecond {
		work = work*6364136223846793005 + 1442695040888963407
	}
	wall := time.Since(start)
	cpu1, err := c.now()
	if err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(work)
	used := cpu1 - cpu0
	if used <= 0 || used > wall*13/10 {
		t.Fatalf("thread CPU time %s over wall time %s: calibration likely overcounts", used, wall)
	}
}
