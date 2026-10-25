// Package latency measures the end-to-end latency of padmate: the benchmark
// of ENG-11, which only the tests run, and the statistics that padmated keeps
// of every real key (DMN-15, ADR-0071).
//
// Stamp reads the high-resolution counter: QueryPerformanceCounter on
// Windows, where time.Now moves in steps of 0.5–2 ms, and the monotonic
// clock elsewhere. SDLClock maps SDL_GetTicksNS values, the timestamps of
// input.Event, onto Stamps, from pairs that input.Source.SampleTicks
// reads. Summarize takes nearest-rank percentiles, Report writes the
// results as Markdown, and bench.go parses the PADMATE_BENCH_*
// environment of the latency tests.
//
// The statistics are in stats.go. A Recorder keeps the latest RingSize
// latencies of each of up to MaxDevices devices: T-output calls Record when
// the keys of an input edge have been sent (output.ArbiterOptions.Latency),
// without allocating, and the IPC goroutines call Summary, Reset and
// ResetAll. WarnP99 is the product threshold, 1.5 ms, which the benchmark
// takes as its target too, and Warn says whether a Summary is bad enough to
// show as a warning (WarnMinSamples samples at least, so that a p99 is not
// just the maximum). Unlike the benchmark, whose latency starts at SDL's own
// timestamp, the statistics start when T-input has the event
// (input.Event.Stamp); ARCHITECTURE §13 gives the difference.
//
// The package is a leaf that builds on every OS: it imports only the
// standard library, plus x/sys/windows in _windows.go files, imports no
// internal package and reads no registry. The pure packages must not
// import it (depguard): they take time only from clock.Clock (CLAUDE.md
// invariant 2).
package latency
