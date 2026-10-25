package script

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

// WriteFilesForTest writes files, by slash-separated path relative to root.
func WriteFilesForTest(t testing.TB, root string, files map[string]string) {
	t.Helper()
	for name, data := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// MacroProgramsForTest builds the macros files of root's macros folder as
// padmated does: a bundle each, compiled under its bare file name, with the
// build's key as the version.
func MacroProgramsForTest(t testing.TB, root string, files ...string) []MacroProgram {
	t.Helper()
	var ps []MacroProgram
	for _, r := range compile.NewCache().BuildMacros(filepath.Join(root, "macros"), files, apiVersion) {
		mp := MacroProgram{File: r.File, Err: r.Err, Version: r.Key}
		if r.Err == nil {
			p, err := goja.Compile(r.File, string(r.Result.JS), false)
			if err != nil {
				t.Fatalf("compile macro %s: %v", r.File, err)
			}
			mp.Program = p
		}
		ps = append(ps, mp)
	}
	return ps
}

// ProgramsForTest writes files to a new root and builds its programs: the
// macros named in macros, and the legacy entry when entry is not empty.
func ProgramsForTest(t testing.TB, files map[string]string, entry string, macros ...string) Programs {
	t.Helper()
	root := t.TempDir()
	WriteFilesForTest(t, root, files)
	var ps Programs
	if entry != "" {
		r, err := compile.Build(compile.BuildOptions{Root: root, Entry: entry, API: apiVersion})
		if err != nil {
			t.Fatalf("build entry: %v", err)
		}
		if ps.Entry, err = goja.Compile(entry, string(r.JS), false); err != nil {
			t.Fatal(err)
		}
	}
	ps.Macros = MacroProgramsForTest(t, root, macros...)
	return ps
}
