//go:build !windows

package spike

import (
	"errors"
	"testing"
)

func TestCycleCalibrationUnsupported(t *testing.T) {
	r, err := CycleCalibration(DefaultCycleOptions())
	if r != nil || !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("CycleCalibration = %v, %v; want nil, an error wrapping ErrUnsupported", r, err)
	}
}
