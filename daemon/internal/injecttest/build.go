package injecttest

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// BuildTool builds the command in pkg, a directory relative to the test's
// or an import path, into a temporary directory of t, and returns the
// path of the executable, named after the package's directory. The
// directory goes when t ends, after the cleanups registered later, such
// as StartProbe's, have stopped the command.
//
// Unlike the rest of the package it is not only for Windows.
func BuildTool(t testing.TB, pkg string) string {
	t.Helper()
	abs, err := filepath.Abs(pkg)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(abs)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	out, err := exec.Command("go", "build", "-o", path, pkg).CombinedOutput()
	if err != nil {
		t.Fatalf("go build %s: %v\n%s", pkg, err, out)
	}
	return path
}
