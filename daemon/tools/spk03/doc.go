// Command spk03 is the SPK-03 spike: goja and esbuild measured with the
// benchmark script and the stand-in host of internal/script/spike.
//
// `task spk03` runs it with `go run`; flags go after --, as in
// `task spk03 -- -dispatch 10000`.
//
//	spk03 [-dispatch N] [-warmup N] [-allocs N] [-idle N] [-runs N] [-cycles=false]
//
// It builds the benchmark script with esbuild, activates it cold and warm
// -runs times each (default 200), times -dispatch handler calls per
// scenario (default 100000) after -warmup untimed ones, counts the
// allocations of -allocs more, times -idle more after each idle gap
// (default 100; 0 skips those rows), runs the SCRIPTING §4.1 watchdog checks,
// records the esbuild message for a top-level await and, on Windows,
// calibrates QueryThreadCycleTime against QueryPerformanceCounter
// (-cycles=false skips that). It then prints one Markdown report to
// stdout: the machine (CPU, logical CPUs, OS, Go, goja and esbuild
// versions), the results table with the SCRIPTING §7 targets, the
// watchdog findings and the calibration. The full run takes about 40 s
// and keeps one or two cores busy; run it on an otherwise idle machine.
//
// It exits 0 once the report is written, whether or not the targets were
// met; 1 when a measurement failed; 2 on bad flags.
package main
