// Command packagezip publishes a checked Windows x64 portable preview zip.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/wdcyxxycdw/padmate/daemon/internal/portablezip"
	"github.com/wdcyxxycdw/padmate/daemon/internal/publicmirror"
)

func main() {
	if err := publicmirror.RefusePackagingHere(); err != nil {
		fmt.Fprintln(os.Stderr, "packagezip:", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("packagezip", flag.ContinueOnError)
	fs.SetOutput(stderr)
	label := fs.String("version", "dev", "application build version")
	root := fs.String("root", "..", "repository root for examples, docs and licenses")
	bin := fs.String("bin", "../bin", "built Windows x64 executable and SDL directory")
	out := fs.String("out", "../dist", "archive output directory")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "packagezip: unexpected positional argument")
		return 2
	}
	if err := publicmirror.RefusePackagingAt(*root); err != nil {
		fmt.Fprintln(stderr, "packagezip:", err)
		return 1
	}
	if _, err := portablezip.Pack(ctx, portablezip.Options{Version: *label, Root: *root, BinDir: *bin, OutDir: *out, Log: stdout}); err != nil {
		fmt.Fprintln(stderr, "packagezip:", err)
		return 1
	}
	return 0
}
