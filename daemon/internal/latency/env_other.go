//go:build !windows

package latency

import "runtime"

func osVersion() string { return runtime.GOOS }
