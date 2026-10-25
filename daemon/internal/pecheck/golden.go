package pecheck

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Executables are the daemon and the CLI, whose imports and bound functions
// the goldens list. padmate-probe is a development tool and is not shipped.
var Executables = []string{"padmated", "padmatectl"}

// goldenHeader is the first line of a golden file; the comment lines are
// ignored when it is read.
const goldenHeader = "# Written by go run ./tools/pecheck -update (task pe:check). A new line is a new Windows function the program can call: read the diff before you commit it.\n"

//go:embed testdata/padmated.imports.txt testdata/padmated.procs.txt testdata/padmatectl.imports.txt testdata/padmatectl.procs.txt
var embedded embed.FS

// Embedded returns the committed goldens of the executable called name
// ("padmated" or "padmatectl"), as they were when the program was built.
func Embedded(name string) (Golden, error) {
	sub, err := fs.Sub(embedded, "testdata")
	if err != nil {
		return Golden{}, err
	}
	return ReadGolden(sub, name)
}

// ReadGolden reads <name>.imports.txt and <name>.procs.txt from fsys.
func ReadGolden(fsys fs.FS, name string) (Golden, error) {
	var g Golden
	for _, f := range []struct {
		suffix string
		into   *[]string
	}{{".imports.txt", &g.Imports}, {".procs.txt", &g.Procs}} {
		data, err := fs.ReadFile(fsys, name+f.suffix)
		if err != nil {
			return Golden{}, fmt.Errorf("pecheck: golden: %w", err)
		}
		*f.into = ParseList(string(data))
	}
	return g, nil
}

// WriteGolden writes the goldens of the executable called name from what rep
// found, in dir.
func WriteGolden(dir, name string, rep Report) error {
	for _, f := range []struct {
		suffix string
		lines  []string
	}{{".imports.txt", rep.ImportLines()}, {".procs.txt", rep.ProcLines()}} {
		if err := os.WriteFile(filepath.Join(dir, name+f.suffix), []byte(FormatList(f.lines)), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// ParseList reads a golden file: one entry per line; blank lines and lines
// that begin with # are skipped.
func ParseList(text string) []string {
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	return lines
}

// FormatList is the text of a golden file for lines: the header, then the
// lines, with LF line ends (the repository's .gitattributes keeps them).
func FormatList(lines []string) string {
	var b strings.Builder
	b.WriteString(goldenHeader)
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

// InspectFile opens path and runs Inspect on it.
func InspectFile(path string, level Level) (Report, error) {
	f, err := os.Open(path)
	if err != nil {
		return Report{}, err
	}
	defer func() { _ = f.Close() }()
	return Inspect(f, level)
}
