//go:build !windows

package input

import (
	"encoding/binary"
	"fmt"

	"github.com/Zyko0/go-sdl3/sdl"
	"github.com/ebitengine/purego"
)

// sdlGUID has the size and the register classification of SDL_GUID
// (Uint8 data[16]): two integer eightbytes. purego v0.10.0 cannot return
// a struct whose only field is a 16-byte array; it looks for a field at
// offset 8.
type sdlGUID struct{ Lo, Hi uint64 }

// bindExt binds the functions of sdlext.go in the SDL library at path.
func bindExt(path string) (err error) {
	h, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return err
	}
	name := "SDL_GetJoystickGUIDForID"
	defer func() {
		// RegisterLibFunc panics for a missing symbol or an unsupported
		// signature.
		if r := recover(); r != nil {
			err = fmt.Errorf("bind %s: %v", name, r)
		}
	}()
	var fn func(id sdl.JoystickID) sdlGUID
	purego.RegisterLibFunc(&fn, h, name)
	// purego converts the Go string into a C string, and reads the C int
	// results as 32 bits. A function RegisterLibFunc makes is a
	// reflect.MakeFunc, which allocates on every call, so here each read
	// of a heartbeat source allocates; only the Windows/amd64 bindings,
	// where padmate reads real receivers, read without allocating
	// (sdlext_windows.go, TestHIDBindings).
	var open func(path string) uintptr
	var read func(dev uintptr, data *byte, length uintptr, milliseconds int32) int32
	var closeDev func(dev uintptr) int32
	name = "SDL_hid_open_path"
	purego.RegisterLibFunc(&open, h, name)
	name = "SDL_hid_read_timeout"
	purego.RegisterLibFunc(&read, h, name)
	name = "SDL_hid_close"
	purego.RegisterLibFunc(&closeDev, h, name)
	getGUID = func(id sdl.JoystickID) GUID {
		r := fn(id)
		var g GUID
		binary.LittleEndian.PutUint64(g[:8], r.Lo)
		binary.LittleEndian.PutUint64(g[8:], r.Hi)
		return g
	}
	hidOpenPath = open
	hidReadTimeout = func(dev uintptr, buf []byte, milliseconds int32) int32 {
		return read(dev, &buf[0], uintptr(len(buf)), milliseconds)
	}
	hidClose = closeDev
	return nil
}
