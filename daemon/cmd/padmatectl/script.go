package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/script/dtsgen"
)

const scriptUsage = `usage: padmatectl script init [--api 0.21] [--dir DIR]
       padmatectl script init --id PROFILE
       padmatectl script import [--replace] PROFILE FILE
       padmatectl script status|registrations|reload [--device ID]
       padmatectl script describe [--id PROFILE]
       padmatectl script logs [--device ID] [--after N] [--limit 100]

script init creates a TypeScript script project with main.ts, tsconfig.json
and the version-filtered padmate declarations in DIR and refuses when any
of them exists. --api takes a script API version this release supports: the
current one, which is the default, or the previous 0.N. main.ts binds no
control; its example stays commented out.
With --id the running daemon writes the project into that library profile's
folder instead, creating only the missing files. script import has the
daemon copy FILE (.ts or .js, at most 1 MiB of UTF-8) into the profile's
folder under its base name; --replace replaces an existing script. Neither
edits profile.json: set script.entry with profile put.`

func runScript(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stdout, scriptUsage)
		return 0
	}
	if args[0] != "init" || hasFlag(args[1:], "id") {
		return rpcCommand("script")(args, stdout, stderr)
	}
	fs := flag.NewFlagSet("script init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apiText := fs.String("api", dtsgen.HostAPI.String(), "script API version (major.minor), one of "+strings.Join(dtsgen.SupportedNames(), ", "))
	dir := fs.String("dir", ".", "directory for the new script project")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, scriptUsage)
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "padmatectl script init: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	api, err := dtsgen.ParseVersion(*apiText)
	if err != nil {
		fmt.Fprintln(stderr, "padmatectl script init:", err)
		return 2
	}
	if err := dtsgen.Scaffold(*dir, api, nil, nil); err != nil {
		fmt.Fprintln(stderr, "padmatectl script init:", err)
		return 1
	}
	fmt.Fprintf(stdout, "created script project in %s (api %s)\n", filepath.Clean(*dir), api)
	return 0
}

// hasFlag reports whether args, before a "--" terminator, set the flag
// name in any of the forms the flag package accepts: -name, --name, and
// either with =value.
func hasFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		flagName, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		if flagName == name {
			return true
		}
	}
	return false
}
