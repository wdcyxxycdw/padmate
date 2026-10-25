//go:build !windows

package inputtest

import (
	"fmt"

	"github.com/Zyko0/go-sdl3/sdl"
	"github.com/ebitengine/purego"

	"github.com/wdcyxxycdw/padmate/daemon/internal/sdlpin"
)

// systemLibrary returns the name of the system's libSDL3 if the dynamic
// loader can load it and it is SDL 3.4 or a later 3.x, as input requires.
// An older SDL is reported like a missing one, so the tests skip.
func systemLibrary() (string, error) {
	name := sdl.Path()
	h, err := purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return "", err
	}
	// Dlclose only fails for an invalid handle.
	defer func() { _ = purego.Dlclose(h) }()
	fn, err := purego.Dlsym(h, "SDL_GetVersion")
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	r, _, _ := purego.SyscallN(fn)
	// SDL_GetVersion returns a C int. The check is input's: go-sdl3 binds
	// the SDL 3.4 API.
	if v := sdl.Version(int32(r)); v.Major() != sdlpin.Major || v.Minor() < 4 {
		return "", fmt.Errorf("%s is SDL %s, want 3.4 or a later 3.x", name, v)
	}
	return name, nil
}
