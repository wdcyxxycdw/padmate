// Package spike holds throwaway measurement code for the M0d goja spike.
// Its conclusions are in docs/adr/0010-scripting-runtime.md;
// the code itself is a stand-in, not the M5 script host.
//
// Do not extend it, import it or copy it from M5 code: the real host is
// written from the script design, in its own package, and
// this package may be deleted once the M0d results are recorded.
//
// Host is the least host that runs the benchmark script (bench/, the
// stick and gesture examples reduced to the M0d padmate stub): a
// goja runtime with a fixed time and random source whose padmate global
// records on() registrations and key.tap specs, and resolves sleeps when
// told to. MeasureActivation times esbuild builds and activations, cold
// and from a cached program; MeasureDispatch times handler calls one by
// one, back to back and after idle gaps, on a P-core and an E-core, and
// counts their allocations; CheckWatchdog runs the
// watchdog checks with Watchdog, a stand-in for the M5 watchdog. Run does
// all of it, and Report.WriteMarkdown prints the results; tools/spk03 is
// the command. Timings use QueryPerformanceCounter on Windows, where
// time.Now moves in steps of 0.5–2 ms, and the monotonic clock elsewhere.
//
// CycleCalibration checks whether QueryThreadCycleTime, which the
// watchdog is meant to use for the CPU time of a locked slot thread,
// can be converted to time by calibrating it against
// QueryPerformanceCounter. It spins for fixed QPC times, sleeps in the
// middle of a spin, pins the thread to each logical processor, reads the
// cycle time of a spinning thread from another thread, as the watchdog
// would, and measures the granularity of GetThreadTimes, the
// alternative. It is Windows-only; elsewhere it returns an error that
// wraps errors.ErrUnsupported. The report types and their Markdown
// output build on every OS.
package spike
