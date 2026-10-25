package spike

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"golang.org/x/sys/windows"
)

// dispatchCPUs lists the processors of group 0 that MeasureDispatch may
// pin its thread to, from the highest index down: fast are the fastest
// efficiency class, slow the slowest. slow is empty when every processor
// is in one class.
func dispatchCPUs() (fast, slow []CPU, err error) {
	sets, err := cpuSets()
	if err != nil {
		return nil, nil, err
	}
	if len(sets) == 0 {
		return nil, nil, nil
	}
	slices.SortFunc(sets, func(a, b cpuSet) int { return cmp.Compare(b.cpu, a.cpu) })
	hi, lo := sets[0].class, sets[0].class
	for _, s := range sets {
		hi, lo = max(hi, s.class), min(lo, s.class)
	}
	for _, s := range sets {
		c := CPU{Index: s.cpu, EfficiencyClass: s.class}
		switch s.class {
		case hi:
			fast = append(fast, c)
		case lo:
			slow = append(slow, c)
		}
	}
	return fast, slow, nil
}

// pinThread restricts the calling thread, which must be locked to its
// goroutine, to c.
func pinThread(c CPU) error {
	if _, err := setThreadAffinityMask(windows.CurrentThread(), 1<<c.Index); err != nil {
		return fmt.Errorf("SetThreadAffinityMask(CPU %d): %w", c.Index, err)
	}
	return nil
}

// outsideAffinity reports whether pinThread failed because the process
// may not run on that processor (a job object, say).
func outsideAffinity(err error) bool { return errors.Is(err, windows.ERROR_INVALID_PARAMETER) }
