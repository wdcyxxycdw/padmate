// Command dtsgen writes the TypeScript editor scaffold for one script API version.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/wdcyxxycdw/padmate/daemon/internal/script/dtsgen"
)

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("dtsgen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apiFlag := fs.String("api", dtsgen.HostAPI.String(), "script API version (major.minor)")
	srcFlag := fs.String("src", "", "canonical padmate.d.ts (default: embedded declarations)")
	outFlag := fs.String("out", ".", "directory for the editor scaffold")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "dtsgen: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	api, err := dtsgen.ParseVersion(*apiFlag)
	if err != nil {
		fmt.Fprintf(stderr, "dtsgen: %v\n", err)
		return 2
	}
	var source []byte
	if *srcFlag != "" {
		source, err = os.ReadFile(*srcFlag)
		if err != nil {
			fmt.Fprintf(stderr, "dtsgen: %v\n", err)
			return 1
		}
	}
	if err := dtsgen.Scaffold(*outFlag, api, source, nil); err != nil {
		fmt.Fprintf(stderr, "dtsgen: %v\n", err)
		return 1
	}
	return 0
}
