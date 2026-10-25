// Command padmate-probe is a development tool (ENG-06): it records the
// keyboard events a low-level hook sees, injected or physical, dumps
// keyboard layouts into test fixtures, and reads window state (the
// foreground window, the extended style of a window; SPK-09). All three
// subcommands need Windows.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/wdcyxxycdw/padmate/daemon/internal/version"
)

// command is one padmate-probe subcommand. run receives the arguments
// after the subcommand name and returns the process exit code.
type command struct {
	summary string
	run     func(args []string, stdout, stderr io.Writer) int
}

var commands = map[string]command{
	"record":      {summary: "print the keyboard events a low-level hook sees", run: runRecord},
	"dump-layout": {summary: "write keyboard layout fixtures", run: runDumpLayout},
	"window":      {summary: "print the foreground window and the extended style of a window", run: runWindow},
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage(stdout)
		return 0
	case "-v", "-version", "--version", "version":
		fmt.Fprintln(stdout, "padmate-probe", version.String())
		return 0
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "padmate-probe: unknown command %q\n\n", args[0])
		usage(stderr)
		return 2
	}
	return cmd.run(args[1:], stdout, stderr)
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: padmate-probe <command> [arguments]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "commands:")
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(w, "  %-12s %s\n", name, commands[name].summary)
	}
	fmt.Fprintf(w, "  %-12s %s\n", "version", "print the version")
}

// parseFlags parses the flags of a subcommand. When it returns false the
// subcommand is done and should exit with the returned code: 0 after
// printing help, 2 after a flag error.
func parseFlags(fs *flag.FlagSet, args []string, usage string, stdout, stderr io.Writer) (int, bool) {
	fs.SetOutput(stderr)
	fs.Usage = func() {} // usage is printed below, to the right stream
	err := fs.Parse(args)
	switch {
	case err == nil:
		return 0, true
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(stdout, usage)
		return 0, false
	default:
		fmt.Fprint(stderr, usage)
		return 2, false
	}
}
