// Package publicmirror recognizes the public code mirror of padmate.
//
// The public repository holds the code only: the export tool
// (daemon/tools/mirror) leaves out the documentation and writes the file
// Marker at the root of the tree. Everything that reads a file the mirror
// leaves out (a test that checks the documentation, a task that packs it)
// asks this package whether it runs in the mirror and then skips, or, for a
// package, refuses. The development repository has no marker, so there the
// same tests and tasks stay strict: a missing document is a failure.
//
// The package imports no testing code, so the commands that refuse to
// package can use it.
package publicmirror

import (
	"errors"
	"os"
	"path/filepath"
)

// Marker is the name of the file at the root of the mirror that says so.
const Marker = ".padmate-public"

// MarkerText is the content of the marker file.
const MarkerText = "This is the public code mirror of padmate: the documentation lives in the development repository, " +
	"so the tests and tasks that read it are skipped here.\n"

// PackagingRefusal is what the package tasks say in the mirror.
const PackagingRefusal = "this is the public code mirror: packages are built from the development repository, " +
	"which has the documentation that ships with them"

// ErrNoRoot is returned by Root when no repository root encloses the start
// directory.
var ErrNoRoot = errors.New("publicmirror: no repository root (Taskfile.yml next to daemon/go.mod) above the directory")

// Root returns the repository root that encloses start (or the working
// directory when start is empty): the nearest directory that has both
// Taskfile.yml and daemon/go.mod.
func Root(start string) (string, error) {
	dir := start
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = wd
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if isFile(filepath.Join(dir, "Taskfile.yml")) && isFile(filepath.Join(dir, "daemon", "go.mod")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNoRoot
		}
		dir = parent
	}
}

// Is reports whether root, the repository root, is the public code mirror.
func Is(root string) bool {
	return isFile(filepath.Join(root, Marker))
}

// InMirror reports whether the repository that encloses the working
// directory is the public code mirror. Outside any repository root it is
// false, so a test run from elsewhere stays strict.
func InMirror() bool {
	root, err := Root("")
	return err == nil && Is(root)
}

// Skipper is the part of *testing.T and *testing.B that SkipIfMirror uses.
type Skipper interface {
	Helper()
	Skipf(format string, args ...any)
}

// SkipIfMirror skips the test when it runs in the public code mirror. what
// names what the test reads that the mirror leaves out. In the development
// repository it does nothing.
func SkipIfMirror(t Skipper, what string) {
	t.Helper()
	if InMirror() {
		t.Skipf("public code mirror (%s at the repository root): %s is not part of the mirror", Marker, what)
	}
}

// RefusePackaging returns an error that says why a package cannot be built
// when root, the repository root, is the public code mirror, and nil
// otherwise.
func RefusePackaging(root string) error {
	if Is(root) {
		return errors.New(PackagingRefusal)
	}
	return nil
}

// RefusePackagingAt is RefusePackaging for the repository that encloses dir
// (dir may be the root itself). A folder that is in no repository, such as the
// temporary root of a test, is not the mirror. The code that packs files from
// a root calls it with that root.
func RefusePackagingAt(dir string) error {
	if root, err := Root(dir); err == nil {
		return RefusePackaging(root)
	}
	return nil
}

// RefusePackagingHere is RefusePackagingAt for the working directory. The
// package commands call it first thing in main, so that nothing is built or
// read in the mirror; their tests call run, not main.
func RefusePackagingHere() error {
	return RefusePackagingAt("")
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
