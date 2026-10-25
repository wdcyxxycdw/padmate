//go:build !windows

package spike

import (
	"errors"
	"fmt"
)

// CycleCalibration is only supported on Windows: QueryThreadCycleTime is
// a Windows API. The error wraps errors.ErrUnsupported.
func CycleCalibration(CycleOptions) (*CycleReport, error) {
	return nil, fmt.Errorf("spike: QueryThreadCycleTime calibration is only supported on Windows: %w", errors.ErrUnsupported)
}
