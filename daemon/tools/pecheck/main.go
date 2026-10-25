// Command pecheck checks the built Windows executables of the daemon and the
// CLI against the hygiene policy of internal/pecheck (PKG-11): layout, sections,
// entropy, the import table and the Windows functions padmate binds at run time,
// the last two against goldens in internal/pecheck/testdata.
//
//	pecheck [-bin ../bin] [-golden internal/pecheck/testdata]
//	pecheck -update [-bin ../bin] [-golden internal/pecheck/testdata]
//
// `task pe:check` runs it in `task ci`, after `task build`. -update rewrites
// the goldens from the files in -bin (after the same checks, minus the golden
// comparison, have passed); the diff is what a reviewer reads.
//
// It reads files and runs nothing.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/wdcyxxycdw/padmate/daemon/internal/pecheck"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pecheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bin := fs.String("bin", "../bin", "folder with the built padmated.exe and padmatectl.exe")
	golden := fs.String("golden", filepath.FromSlash("internal/pecheck/testdata"), "folder with the golden lists")
	update := fs.Bool("update", false, "rewrite the golden lists from the files")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "pecheck: unexpected positional argument")
		return 2
	}
	failed := false
	fail := func(format string, args ...any) {
		failed = true
		fmt.Fprintf(stderr, "pecheck: "+format+"\n", args...)
	}
	for _, name := range pecheck.Executables {
		file := filepath.Join(*bin, name+".exe")
		rep, err := pecheck.InspectFile(file, pecheck.Full)
		if err != nil {
			fail("%s: %v", file, err)
			continue
		}
		if *update {
			// The golden is the file's own lists, so only the problems that
			// no golden can excuse remain: the denied functions, a missing
			// symbol table, the layout.
			own := pecheck.Golden{Imports: rep.ImportLines(), Procs: rep.ProcLines()}
			if problems := pecheck.Check(rep, pecheck.GoPolicy(own)); len(problems) > 0 {
				for _, p := range problems {
					fail("%s: %s", name+".exe", p)
				}
				continue
			}
			old, err := pecheck.ReadGolden(os.DirFS(*golden), name)
			if err != nil {
				old = pecheck.Golden{}
			}
			if err := pecheck.WriteGolden(*golden, name, rep); err != nil {
				fail("%s: %v", name, err)
				continue
			}
			fmt.Fprintf(stdout, "%s: %d imports (%s), %d Windows functions bound (%s)\n", name+".exe",
				len(own.Imports), change(old.Imports, own.Imports), len(own.Procs), change(old.Procs, own.Procs))
			continue
		}
		g, err := pecheck.ReadGolden(os.DirFS(*golden), name)
		if err != nil {
			fail("%v", err)
			continue
		}
		problems := pecheck.Check(rep, pecheck.GoPolicy(g))
		for _, p := range problems {
			fail("%s: %s", name+".exe", p)
		}
		if len(problems) == 0 {
			fmt.Fprintf(stdout, "%s: %d imports, %d Windows functions bound, as the goldens list them\n", name+".exe", len(rep.ImportLines()), len(rep.ProcLines()))
		}
	}
	if failed {
		return 1
	}
	return 0
}

// change says how many lines were added and removed.
func change(old, now []string) string {
	added, removed := 0, 0
	for _, l := range now {
		if !slices.Contains(old, l) {
			added++
		}
	}
	for _, l := range old {
		if !slices.Contains(now, l) {
			removed++
		}
	}
	return fmt.Sprintf("+%d -%d", added, removed)
}
