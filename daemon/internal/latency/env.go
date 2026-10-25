package latency

import (
	"runtime"
	"time"
)

// Environment is the machine and runtime a report was measured on.
type Environment struct {
	Date       string // RFC 3339, UTC
	GoVersion  string
	OS         string // "windows 10.0.26200" (RtlGetVersion); runtime.GOOS elsewhere
	NumCPU     int
	GOMAXPROCS int
	Freq       int64    // Freq()
	Extra      []string // set by the caller: SDL version, SDL_TIMER_RESOLUTION, mode, scale, devices, ...
}

// ReadEnvironment reads the Environment, all but Extra. It only reads
// documented, read-only values: no registry, no timer resolution, no
// power state and no CPU name.
func ReadEnvironment() Environment {
	return Environment{
		Date:       time.Now().UTC().Format(time.RFC3339),
		GoVersion:  runtime.Version(),
		OS:         osVersion(),
		NumCPU:     runtime.NumCPU(),
		GOMAXPROCS: runtime.GOMAXPROCS(0),
		Freq:       Freq(),
	}
}
