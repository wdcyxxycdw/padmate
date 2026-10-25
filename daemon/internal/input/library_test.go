package input

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Zyko0/go-sdl3/sdl"

	"github.com/wdcyxxycdw/padmate/daemon/internal/sdlpin"
)

func TestResolveLibraryEnv(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "libSDL3-test")
	if err := os.WriteFile(lib, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvLibrary, lib)
	got, err := ResolveLibrary()
	if err != nil || got != lib {
		t.Errorf("ResolveLibrary() = %q, %v; want %q", got, err, lib)
	}

	// A set variable that names no file is an error, not a fallback.
	t.Setenv(EnvLibrary, filepath.Join(dir, "missing"))
	got, err = ResolveLibrary()
	if !errors.Is(err, ErrNoLibrary) {
		t.Fatalf("ResolveLibrary() with a missing file = %q, %v; want ErrNoLibrary", got, err)
	}
	want := []string{"$" + EnvLibrary, "unset it"}
	if runtime.GOOS == "windows" {
		want = append(want, "task sdl")
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not mention %q", err, w)
		}
	}
}

// TestResolveLibraryFallback runs without $PADMATE_SDL3, and with no SDL
// library next to the test binary.
func TestResolveLibraryFallback(t *testing.T) {
	t.Setenv(EnvLibrary, "")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(exe), libraryFile())); err == nil {
		t.Skip("an SDL library lies next to the test binary")
	}
	got, err := ResolveLibrary()
	if runtime.GOOS == "windows" {
		// Never the DLL search path (ADR-0007).
		if !errors.Is(err, ErrNoLibrary) {
			t.Fatalf("ResolveLibrary() = %q, %v; want ErrNoLibrary", got, err)
		}
		for _, w := range []string{"SDL3.dll", exe, "task sdl", "$" + EnvLibrary} {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("error %q does not mention %q", err, w)
			}
		}
		return
	}
	if err != nil || got != sdl.Path() {
		t.Errorf("ResolveLibrary() = %q, %v; want %q", got, err, sdl.Path())
	}
}

func TestCheckVersion(t *testing.T) {
	v := func(major, minor, patch int) sdl.Version {
		return sdl.Version(major*1000000 + minor*1000 + patch)
	}
	for _, tc := range []struct {
		v    sdl.Version
		goos string
		ok   bool
	}{
		{v(3, 4, 16), "windows", true},
		{v(3, 4, 14), "windows", false},
		{v(3, 5, 0), "windows", false},
		{v(3, 4, 16), "linux", true},
		{v(3, 4, 0), "linux", true},
		{v(3, 6, 2), "darwin", true},
		{v(3, 2, 20), "linux", false},
		{v(2, 30, 0), "linux", false},
		{v(4, 0, 0), "linux", false},
	} {
		err := checkVersion(tc.v, tc.goos)
		if (err == nil) != tc.ok {
			t.Errorf("checkVersion(%s, %s) = %v, want ok=%t", tc.v, tc.goos, err, tc.ok)
		}
		if err == nil {
			continue
		}
		// The error names the version found and the one wanted.
		want := []string{tc.v.String(), "3.4"}
		if tc.goos == "windows" {
			want = []string{tc.v.String(), sdlpin.Version, "task sdl"}
		}
		for _, w := range want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("checkVersion(%s, %s) = %q, want it to mention %q", tc.v, tc.goos, err, w)
			}
		}
	}
}
