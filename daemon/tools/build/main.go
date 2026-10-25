// Command build compiles padmate commands with fresh Windows resources.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/buildtool"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	label := fs.String("version", "dev", "application build version")
	out := fs.String("out", "../bin", "output directory")
	targets := fs.String("targets", "all", "all or comma-separated command names")
	gui := fs.Bool("gui", false, "build padmated for the Windows GUI subsystem")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "build: unexpected positional argument")
		return 2
	}
	selected := strings.Split(*targets, ",")
	if *targets == "all" {
		selected = []string{"padmated", "padmatectl", "padmate-probe"}
	}
	if err := buildtool.Build(ctx, buildtool.Options{Version: *label, OutDir: *out, Targets: selected, GUI: *gui, Log: stdout}); err != nil {
		fmt.Fprintln(stderr, "build:", err)
		return 1
	}
	return 0
}
