// Command padmatectl is the padmate command-line client: validation,
// monitoring, debugging and emergency release.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/wdcyxxycdw/padmate/daemon/internal/version"
)

// command is one padmatectl subcommand. run receives the arguments after
// the subcommand name and returns the process exit code.
type command struct {
	summary string
	run     func(args []string, stdout, stderr io.Writer) int
}

var commands = map[string]command{
	"keyboard-capture": {summary: "temporary native shortcut capture for Electron", run: runKeyboardCapture},
	"ui-bridge":        {summary: "private Electron main-process IPC bridge", run: runUIBridge},
	"status":           {summary: "query daemon state", run: rpcCommand("status")},
	"devices":          {summary: "query devices and capabilities", run: rpcCommand("devices")},
	"identify":         {summary: "make a controller buzz and flash to tell which it is", run: rpcCommand("identify")},
	"latency":          {summary: "show how long padmated takes from a gamepad input to the key", run: rpcCommand("latency")},
	"pause":            {summary: "pause daemon mapping", run: rpcCommand("pause")},
	"resume":           {summary: "resume daemon mapping", run: rpcCommand("resume")},
	"layer":            {summary: "change a daemon mapping layer", run: rpcCommand("layer")},
	"profile":          {summary: "list, edit, copy, delete, activate and explain profiles", run: rpcCommand("profile")},
	"config":           {summary: "read or save global daemon settings", run: rpcCommand("config")},
	"rpc":              {summary: "call a daemon JSON-RPC method", run: rpcCommand("rpc")},
	"subscribe":        {summary: "stream daemon events", run: eventCommand("subscribe")},
	"capture":          {summary: "capture input with a temporary mapping pause", run: eventCommand("capture")},
	"fake":             {summary: "drive a daemon started with --fake-input", run: rpcCommand("fake")},
	"keys":             {summary: "parse, format and list key specs", run: runKeys},
	"monitor":          {summary: "print daemon input (or standalone SDL with --direct)", run: runMonitor},
	"record":           {summary: "record a gamepad's events as JSON lines", run: runRecord},
	"trace":            {summary: "show the input-to-output gesture timeline", run: runTrace},
	"release-all":      {summary: "release keys held by a running padmated", run: runReleaseAll},
	"quit":             {summary: "ask the running padmated to exit and wait for it (installer)", run: runQuit},
	"send":             {summary: "tap key chords with SendInput (development tool)", run: runSend},
	"validate":         {summary: "validate profile files", run: runValidate},
	"script":           {summary: "create and inspect script projects", run: runScript},
	"macro":            {summary: "list, create, import, rename and delete the script library's macros", run: rpcCommand("macro")},
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
	case "-v", "--version", "version":
		fmt.Fprintln(stdout, "padmatectl", version.String())
		return 0
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "padmatectl: unknown command %q\n\n", args[0])
		usage(stderr)
		return 2
	}
	return cmd.run(args[1:], stdout, stderr)
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: padmatectl <command> [arguments]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "commands:")
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(w, "  %-10s %s\n", name, commands[name].summary)
	}
	fmt.Fprintf(w, "  %-10s %s\n", "version", "print the version")
}
