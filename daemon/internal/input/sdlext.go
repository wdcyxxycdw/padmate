package input

import "github.com/Zyko0/go-sdl3/sdl"

// This file and its per-OS parts bind the SDL functions that go-sdl3
// v0.1.1 lacks or binds wrongly (SPK-10). bindExt resolves them in the
// library that loadLibrary loaded, by the same path, so they always come
// from the same module as go-sdl3's own bindings.
//
// SDL_GetJoystickGUIDForID returns the 16-byte SDL_GUID by value. go-sdl3
// declares it as returning a pointer, which is wrong for every ABI, and
// its JoystickID.JoystickGUIDForID panics "not implemented". purego
// v0.10.0 handles struct results only on darwin and linux: on Windows
// RegisterFunc panics for them. So windows/amd64 calls the function with
// purego.SyscallN, passing the result pointer by hand
// (sdlext_windows.go), and darwin and linux let purego.RegisterFunc
// return the struct (sdlext_other.go).
//
// go-sdl3 v0.1.1 binds none of SDL's HID API (SDL_hid_*), which the
// receiver watchdog reads reports with (IN-30, hidbeat.go). Its C int
// results are 32 bits wide: on x64 only EAX is defined, so the Windows
// bindings convert the register to int32 before they compare it. On
// Windows/amd64 reading allocates nothing; elsewhere purego's
// RegisterLibFunc allocates on every call.

// getGUID is SDL_GetJoystickGUIDForID once bindExt has run. It stays nil
// where there is no binding (Windows on arm64 or 386).
var getGUID func(id sdl.JoystickID) GUID

// hidOpenPath, hidReadTimeout and hidClose are SDL_hid_open_path,
// SDL_hid_read_timeout and SDL_hid_close once bindExt has run, with the
// SDL_hid_device pointer as a uintptr: 0 is NULL, which the last two
// reject with -1. They stay nil where there is no binding, as getGUID
// does.
var (
	hidOpenPath    func(path string) uintptr
	hidReadTimeout func(dev uintptr, buf []byte, milliseconds int32) int32
	hidClose       func(dev uintptr) int32
)

// deviceGUID returns the GUID of the joystick id. ok is false when there
// is no binding, or when SDL returns the zero GUID for an unknown id.
func deviceGUID(id sdl.JoystickID) (g GUID, ok bool) {
	if getGUID == nil {
		return GUID{}, false
	}
	g = getGUID(id)
	return g, g != GUID{}
}
