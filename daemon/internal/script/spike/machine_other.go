//go:build !windows

package spike

import (
	"bufio"
	"os"
	"runtime"
	"strings"
)

func cpuName() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return runtime.GOARCH
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), ":"); ok && strings.TrimSpace(k) == "model name" {
			return strings.TrimSpace(v)
		}
	}
	return runtime.GOARCH
}

func osName() string { return runtime.GOOS }

func stopwatchName() string { return "time.Now (monotonic)" }
