//go:build !windows

package spike

import "errors"

// dispatchCPUs returns no processors: the spike pins threads only on
// Windows.
func dispatchCPUs() (fast, slow []CPU, err error) { return nil, nil, nil }

func pinThread(CPU) error { return errors.ErrUnsupported }

func outsideAffinity(error) bool { return false }
