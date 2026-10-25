package input

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/Zyko0/go-sdl3/sdl"

	"github.com/wdcyxxycdw/padmate/daemon/internal/sdlpin"
)

// EnvLibrary names the environment variable that overrides the SDL
// library path, for development.
const EnvLibrary = "PADMATE_SDL3"

// ErrNoLibrary is wrapped by the errors of ResolveLibrary and Open when no
// SDL library can be found.
var ErrNoLibrary = errors.New("SDL3 library not found")

// libraryFile is the file name of the SDL library on this OS.
func libraryFile() string {
	if runtime.GOOS == "windows" {
		return sdlpin.DLLName
	}
	return sdl.Path()
}

// ResolveLibrary returns the SDL library to load. It tries, in order:
//
//  1. the file named by $PADMATE_SDL3, which must exist when set;
//  2. the library next to the executable (SDL3.dll on Windows);
//  3. on other systems only, sdl.Path(), such as "libSDL3.so.0", for the
//     dynamic loader to find.
//
// The first two are absolute paths. On Windows it never falls back to the
// DLL search path: padmate loads the pinned DLL it ships (ADR-0007).
func ResolveLibrary() (string, error) {
	if p := os.Getenv(EnvLibrary); p != "" {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", fmt.Errorf("$%s: %w", EnvLibrary, err)
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("%w: $%s: %w; correct it, or unset it to %s",
				ErrNoLibrary, EnvLibrary, err, defaultLibraryAdvice())
		}
		return abs, nil
	}
	exe, err := os.Executable()
	if err == nil {
		p := filepath.Join(filepath.Dir(exe), libraryFile())
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if runtime.GOOS != "windows" {
		return sdl.Path(), nil
	}
	if err != nil {
		return "", fmt.Errorf("%w: cannot look for %s next to the executable: %w; set $%s",
			ErrNoLibrary, libraryFile(), err, EnvLibrary)
	}
	return "", fmt.Errorf("%w: no %s next to %s; run `task sdl` to fetch it into bin/, or set $%s",
		ErrNoLibrary, libraryFile(), exe, EnvLibrary)
}

// defaultLibraryAdvice says what ResolveLibrary loads without
// $PADMATE_SDL3.
func defaultLibraryAdvice() string {
	if runtime.GOOS == "windows" {
		return "load the " + sdlpin.DLLName + " next to the executable, which `task sdl` fetches into bin/"
	}
	return "load the library next to the executable or else the system's " + sdl.Path()
}

// fetchAdvice says how to get an SDL library that loadLibrary accepts.
func fetchAdvice() string {
	if runtime.GOOS == "windows" {
		return "run `task sdl` to fetch the pinned SDL " + sdlpin.Version
	}
	return "install SDL 3.4 or a later 3.x"
}

var (
	// loadMu guards loaded. go-sdl3 binds its functions to one library
	// per process, so the first successful load is final.
	loadMu sync.Mutex
	loaded string
)

// loadLibrary loads the SDL library at path, once per process, binds
// padmate's own SDL functions and checks the version. It returns the
// library's version.
func loadLibrary(path string) (sdl.Version, error) {
	loadMu.Lock()
	defer loadMu.Unlock()
	if loaded != "" {
		if path != loaded {
			return 0, fmt.Errorf("SDL is already loaded from %s; cannot load %s", loaded, path)
		}
		return sdl.GetVersion(), nil
	}
	if err := sdlLoad(path); err != nil {
		return 0, fmt.Errorf("load %s: %w; %s", path, err, fetchAdvice())
	}
	v := sdl.GetVersion()
	if err := checkVersion(v, runtime.GOOS); err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	if err := bindExt(path); err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	loaded = path
	return v, nil
}

// sdlLoad is sdl.LoadLibrary, which panics when the library lacks a
// function go-sdl3 binds, as an SDL older than 3.4 does.
func sdlLoad(path string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	return sdl.LoadLibrary(path)
}

// checkVersion checks the version of the loaded SDL. On Windows it must be
// the pinned release that padmate ships. Elsewhere padmate uses the
// system's SDL, which only has to provide the SDL 3.4 API that go-sdl3
// binds.
func checkVersion(v sdl.Version, goos string) error {
	if goos == "windows" {
		if v != sdlpin.VersionNum {
			return fmt.Errorf("SDL version is %s, want the pinned %s; run `task sdl`", v, sdlpin.Version)
		}
		return nil
	}
	if v.Major() != sdlpin.Major || v.Minor() < 4 {
		return fmt.Errorf("SDL version is %s, want 3.4 or a later 3.x", v)
	}
	return nil
}
