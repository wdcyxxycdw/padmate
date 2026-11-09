package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/cspnames"
	"github.com/wdcyxxycdw/padmate/daemon/internal/cspnames/tablegen"
)

const cspNamesUsage = `usage: padmate-probe csp-names --install DIR --out FILE [flags]

Regenerates the bundled table of CLIP STUDIO PAINT command names
(internal/cspnames/table.json) from CLIPStudioPaint.exe and the main
resources under resource\chinese_sc and resource\english. The table holds the
tree of the shortcut dialog (groups and the order of every command) and the
names of the commands outside the menus, whose text CSP keeps in its code.
Everything in the install folder is only read.

The scan looks at two ranges of virtual addresses in the executable, which
belong to one CSP version; the defaults are those of CSP 5.1.5. The command
fails if it finds implausibly little, which means the ranges or the code
patterns do not fit the executable.

flags:
  --install DIR    folder that holds CLIPStudioPaint.exe and resource\
  --out FILE       where to write the table
  --version V      CSP version recorded in the table (default 5.1.5)
  --region-a R     VA range START-END (hex) of the shortcut dialog tree
                   (default 0x140f80000-0x140fb0000)
  --region-b R     VA range of the command table
                   (default 0x14111a000-0x141130000)
`

func runCSPNames(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("csp-names", flag.ContinueOnError)
	install := fs.String("install", "", "CSP install folder")
	out := fs.String("out", "", "output file")
	ver := fs.String("version", tablegen.DefaultVersion, "CSP version")
	ra := fs.String("region-a", "", "VA range of the dialog tree")
	rb := fs.String("region-b", "", "VA range of the command table")
	if code, ok := parseFlags(fs, args, cspNamesUsage, stdout, stderr); !ok {
		return code
	}
	if *install == "" || *out == "" || fs.NArg() != 0 {
		fmt.Fprint(stderr, cspNamesUsage)
		return 2
	}
	opts := tablegen.Options{InstallDir: *install, Version: *ver}
	var err error
	if opts.RegionA, err = parseRegion(*ra); err != nil {
		fmt.Fprintf(stderr, "padmate-probe csp-names: --region-a: %v\n", err)
		return 2
	}
	if opts.RegionB, err = parseRegion(*rb); err != nil {
		fmt.Fprintf(stderr, "padmate-probe csp-names: --region-b: %v\n", err)
		return 2
	}
	t, rep, err := tablegen.Generate(opts)
	if rep != nil {
		fmt.Fprintf(stdout, "stubs %d (main resource %d), registrations %d (region A %d), with text %d, ids %d\n",
			rep.Stubs, rep.MainStubs, rep.Entries, rep.RegionAEntries, rep.WithText, rep.IDs)
	}
	if err != nil {
		fmt.Fprintln(stderr, "padmate-probe csp-names:", err)
		return 1
	}
	data, err := cspnames.MarshalTable(t)
	if err == nil {
		if _, err = cspnames.ParseTable(data); err == nil {
			err = os.WriteFile(filepath.Clean(*out), data, 0o644)
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "padmate-probe csp-names:", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: %d commands (%d more are named by the menu resource), %d with several candidate names\n",
		*out, rep.Commands, rep.MenuCovered, rep.Ambiguous)
	fmt.Fprintf(stdout, "tree: %d groups, %d commands; %d entries of unknown depth; %d groups without a plain name dissolved: %s\n",
		rep.Groups, rep.Leaves, rep.UnknownDepth, len(rep.DissolvedGroups), strings.Join(rep.DissolvedGroups, " "))
	return 0
}

// parseRegion parses "START-END" in hex; the empty string selects the
// default (the zero Region).
func parseRegion(s string) (tablegen.Region, error) {
	if s == "" {
		return tablegen.Region{}, nil
	}
	var a, b uint64
	if _, err := fmt.Sscanf(s, "0x%x-0x%x", &a, &b); err != nil {
		if _, err := fmt.Sscanf(s, "%x-%x", &a, &b); err != nil {
			return tablegen.Region{}, fmt.Errorf("want START-END in hex, got %q", s)
		}
	}
	if a >= b {
		return tablegen.Region{}, fmt.Errorf("empty range %q", s)
	}
	return tablegen.Region{Start: a, End: b}, nil
}
