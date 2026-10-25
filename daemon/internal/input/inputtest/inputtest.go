// Package inputtest helps the tests that need SDL: it finds the SDL
// library, keeps the developer's SDL hints out of a test, and describes
// virtual Joy-Cons and a virtual Switch Pro gamepad with a heartbeat
// (SwitchPro, Beats) for the receiver watchdog (IN-30).
package inputtest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/sdlpin"
)

// The environment variables of the SDL-backed tests.
const (
	// EnvLibrary names the SDL library to use, as it does for input.
	EnvLibrary = input.EnvLibrary
	// EnvRequire=1 turns a missing library into a failure instead of a
	// skip, for CI and `task test:sdl`.
	EnvRequire = "PADMATE_REQUIRE_SDL"
)

// Library returns the SDL library for a test:
//
//  1. $PADMATE_SDL3 when it is set, and nothing else;
//  2. on Windows, bin/SDL3.dll of the repository, found by walking up from
//     the working directory to the directory with Taskfile.yml;
//  3. elsewhere, the system's libSDL3, when the dynamic loader finds it.
//
// It skips the test when there is none, unless $PADMATE_REQUIRE_SDL is 1.
// The result is an absolute path, except for a system library found by
// the loader.
func Library(t testing.TB) string {
	t.Helper()
	path, err := find()
	if err != nil {
		if os.Getenv(EnvRequire) == "1" {
			t.Fatalf("SDL library: %v (%s=1; run `task sdl`)", err, EnvRequire)
		}
		t.Skipf("no SDL library: %v; run `task sdl`, or set %s=1 to fail instead", err, EnvRequire)
	}
	return path
}

func find() (string, error) {
	if p := os.Getenv(EnvLibrary); p != "" {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("$%s: %w", EnvLibrary, err)
		}
		return abs, nil
	}
	if runtime.GOOS != "windows" {
		return systemLibrary()
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "Taskfile.yml")); err == nil {
			p := filepath.Join(dir, "bin", sdlpin.DLLName)
			if _, err := os.Stat(p); err != nil {
				return "", err
			}
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no Taskfile.yml above the working directory")
		}
		dir = parent
	}
}

// Unsetenv unsets the environment variables names for the rest of test
// t and restores them afterwards, as t.Setenv does for a value, so that
// a test of padmate's hint defaults does not depend on the developer's
// environment.
func Unsetenv(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		t.Setenv(name, "") // registers the restore
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
	}
}
