package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/wdcyxxycdw/padmate/daemon/internal/script/spike"
)

const usage = `usage: spk03 [-dispatch N] [-warmup N] [-allocs N] [-idle N] [-runs N] [-cycles=false]

The SPK-03 spike: goja dispatch and activation, esbuild build time, the
watchdog checks and the QueryThreadCycleTime calibration, printed to
stdout as one Markdown report. It exits 0 once the report is written,
pass or fail; 1 when a measurement failed; 2 on bad flags.

flags:
  -dispatch N   timed dispatches per scenario (default 100000)
  -warmup N     untimed dispatches before them (default 10000)
  -allocs N     dispatches counted for allocations (default 2000)
  -idle N       dispatches timed after each idle gap, 0 to skip (default 100)
  -runs N       cold and warm activations each (default 200)
  -cycles       calibrate QueryThreadCycleTime, Windows only (default true)
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// parseFlags parses and checks the flags. When ok is false, spk03 exits
// with code.
func parseFlags(args []string, stdout, stderr io.Writer) (opts spike.Options, code int, ok bool) {
	opts = spike.DefaultOptions()
	fs := flag.NewFlagSet("spk03", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {} // usage is printed below, to the right stream
	fs.IntVar(&opts.Dispatch.N, "dispatch", opts.Dispatch.N, "timed dispatches per scenario")
	fs.IntVar(&opts.Dispatch.Warmup, "warmup", opts.Dispatch.Warmup, "untimed dispatches first")
	fs.IntVar(&opts.Dispatch.AllocN, "allocs", opts.Dispatch.AllocN, "dispatches counted for allocations")
	fs.IntVar(&opts.Dispatch.IdleN, "idle", opts.Dispatch.IdleN, "dispatches timed after each idle gap")
	fs.IntVar(&opts.Runs, "runs", opts.Runs, "cold and warm activations each")
	cycles := fs.Bool("cycles", true, "calibrate QueryThreadCycleTime")
	switch err := fs.Parse(args); {
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(stdout, usage)
		return opts, 0, false
	case err != nil:
		fmt.Fprint(stderr, usage)
		return opts, 2, false
	}
	var problem string
	switch {
	case fs.NArg() > 0:
		problem = fmt.Sprintf("unexpected argument %q", fs.Arg(0))
	case opts.Dispatch.N < 1 || opts.Runs < 1:
		problem = "-dispatch and -runs must be at least 1"
	case opts.Dispatch.Warmup < 0 || opts.Dispatch.AllocN < 0 || opts.Dispatch.IdleN < 0:
		problem = "-warmup, -allocs and -idle must not be negative"
	}
	if problem != "" {
		fmt.Fprintf(stderr, "spk03: %s\n", problem)
		fmt.Fprint(stderr, usage)
		return opts, 2, false
	}
	if !*cycles {
		opts.Cycles = nil
	}
	return opts, 0, true
}

// run is spk03. Nothing else in the process may use esbuild or goja
// before it, since the first build and activation are reported apart.
func run(args []string, stdout, stderr io.Writer) int {
	opts, code, ok := parseFlags(args, stdout, stderr)
	if !ok {
		return code
	}
	rep, err := spike.Run(opts)
	if err != nil {
		fmt.Fprintf(stderr, "spk03: %v\n", err)
		return 1
	}
	w := bufio.NewWriter(stdout)
	err = rep.WriteMarkdown(w)
	if ferr := w.Flush(); err == nil {
		err = ferr
	}
	if err != nil {
		fmt.Fprintf(stderr, "spk03: %v\n", err)
		return 1
	}
	return 0
}
