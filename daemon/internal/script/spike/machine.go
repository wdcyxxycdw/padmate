package spike

import (
	"os"
	"runtime"
	"runtime/debug"
	"time"
)

// Machine describes where the numbers come from.
type Machine struct {
	CPU                     string
	LogicalCPUs, GOMAXPROCS int
	OS                      string
	// Goja and Esbuild are "path version", as tools/gojafeatures names them.
	Go, Goja, Esbuild string
	// GOGC is the environment variable, "" when unset (100).
	GOGC string
	// Stopwatch is the clock the timings use; StampCost is the cost of
	// one reading, which the timings include.
	Stopwatch string
	StampCost time.Duration
}

// Module paths whose versions MachineInfo reports.
const (
	gojaModule    = "github.com/dop251/goja"
	esbuildModule = "github.com/evanw/esbuild"
)

// MachineInfo describes this machine and binary.
func MachineInfo() Machine {
	m := Machine{
		CPU:         cpuName(),
		LogicalCPUs: runtime.NumCPU(),
		GOMAXPROCS:  runtime.GOMAXPROCS(0),
		OS:          osName() + " " + runtime.GOARCH,
		Go:          runtime.Version(),
		Goja:        moduleVersion(gojaModule),
		Esbuild:     moduleVersion(esbuildModule),
		GOGC:        os.Getenv("GOGC"),
		Stopwatch:   stopwatchName(),
	}
	const n = 100000
	t0 := stamp()
	for range n {
		stamp()
	}
	m.StampCost = stampDuration(stamp()-t0) / n
	return m
}

// moduleVersion names module path as "path version" from the binary's
// build info, the replacement under a replace directive, as
// tools/gojafeatures does; "path unknown" when it is not there.
func moduleVersion(path string) string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, d := range info.Deps {
			if d.Path != path {
				continue
			}
			if d.Replace != nil {
				d = d.Replace
			}
			return d.Path + " " + d.Version
		}
	}
	return path + " unknown"
}
