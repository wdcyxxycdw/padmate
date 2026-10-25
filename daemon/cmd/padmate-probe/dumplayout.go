package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/kblayout"
	"github.com/wdcyxxycdw/padmate/daemon/internal/version"
)

const dumpLayoutUsage = `usage: padmate-probe dump-layout [--out DIR] [KLID...]

Writes a JSON fixture for each keyboard layout, named <KLID>.json: the VK
of every scan code and what it types without and with Shift and AltGr, the
scan code of every VK, and the key of every printable ASCII character.
The default layouts are 00000409 (US), 0000040C (French) and 00000411
(Japanese).

A layout the user has not loaded is loaded without being activated
(KLF_NOTELLSHELL) and unloaded afterwards; the command fails if the
user's layout list ends up different. The registry is only read.

flags:
  --out DIR  write the fixtures into DIR (default testdata/layouts)
`

// defaultKLIDs are the layouts dump-layout writes when given none.
var defaultKLIDs = []string{"00000409", "0000040C", "00000411"}

// openLayoutDumper returns the layoutDumper of the platform. Tests replace
// it.
var openLayoutDumper = newLayoutDumper

// layoutDumper reads keyboard layouts: the Windows API on Windows, a fake
// in tests.
type layoutDumper interface {
	// LayoutList describes the user's loaded layouts, for the before and
	// after check.
	LayoutList() (string, error)
	// Dump returns the fixture of klid with Generator and Version empty.
	Dump(klid string) (*kblayout.Fixture, error)
}

func runDumpLayout(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dump-layout", flag.ContinueOnError)
	out := fs.String("out", filepath.Join("testdata", "layouts"), "output directory")
	if code, ok := parseFlags(fs, args, dumpLayoutUsage, stdout, stderr); !ok {
		return code
	}
	args = fs.Args()
	if len(args) == 0 {
		args = defaultKLIDs
	}
	klids := make([]string, 0, len(args))
	for _, klid := range args {
		if !kblayout.ValidKLID(klid) {
			fmt.Fprintf(stderr, "padmate-probe dump-layout: bad KLID %q: want eight hex digits such as 00000409\n", klid)
			fmt.Fprint(stderr, dumpLayoutUsage)
			return 2
		}
		klids = append(klids, strings.ToUpper(klid))
	}
	d, err := openLayoutDumper()
	switch {
	case errors.Is(err, errUnsupported):
		fmt.Fprintln(stderr, "padmate-probe: dump-layout is only supported on Windows")
		return 1
	case err != nil:
		fmt.Fprintln(stderr, "padmate-probe dump-layout:", err)
		return 1
	}
	return dumpLayouts(d, klids, *out, stdout, stderr)
}

// dumpLayouts writes the fixture of each KLID into dir and prints one line
// per file. It checks that the user's layout list is the same before and
// after.
func dumpLayouts(d layoutDumper, klids []string, dir string, stdout, stderr io.Writer) int {
	before, err := d.LayoutList()
	if err != nil {
		fmt.Fprintln(stderr, "padmate-probe dump-layout:", err)
		return 1
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(stderr, "padmate-probe dump-layout:", err)
		return 1
	}
	status := 0
	for _, klid := range klids {
		f, err := d.Dump(klid)
		if err == nil {
			f.Generator, f.Version = kblayout.Generator, version.String()
			err = writeFixture(dir, f)
		}
		if err != nil {
			fmt.Fprintf(stderr, "padmate-probe dump-layout: %s: %v\n", klid, err)
			status = 1
			continue
		}
		fmt.Fprintf(stdout, "%s %s (%s, %s): %d scan codes, %d VKs\n",
			filepath.Join(dir, kblayout.FileName(f.KLID)), f.HKL, f.LayoutFile, f.LayoutText, len(f.Scancodes), len(f.VKToScan))
	}
	after, err := d.LayoutList()
	switch {
	case err != nil:
		fmt.Fprintln(stderr, "padmate-probe dump-layout:", err)
		return 1
	case after != before:
		fmt.Fprintf(stderr, "padmate-probe dump-layout: the keyboard layout list changed from %s to %s\n", before, after)
		return 1
	}
	fmt.Fprintf(stderr, "padmate-probe dump-layout: keyboard layout list unchanged: %s\n", after)
	return status
}

// writeFixture validates f and writes it into dir.
func writeFixture(dir string, f *kblayout.Fixture) error {
	if err := f.Validate(); err != nil {
		return err
	}
	data, err := f.Marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, kblayout.FileName(f.KLID)), data, 0o644)
}
