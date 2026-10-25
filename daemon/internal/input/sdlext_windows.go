package input

import (
	"runtime"
	"syscall"
	"unsafe"

	"github.com/Zyko0/go-sdl3/sdl"
	"github.com/ebitengine/purego"
	"golang.org/x/sys/windows"
)

// bindExt binds the functions of sdlext.go in the SDL library at path.
func bindExt(path string) error {
	if runtime.GOARCH != "amd64" {
		// Only x64 is in scope (sdlpin). Other ABIs return a 16-byte
		// struct differently; without a binding GUIDs are left empty, and
		// the receiver watchdog watches no real device.
		return nil
	}
	// Loading the path go-sdl3 loaded returns the same module and adds a
	// reference that, like go-sdl3's own, is never released.
	h, err := windows.LoadLibrary(path)
	if err != nil {
		return err
	}
	var addrs [4]uintptr
	for i, name := range [...]string{"SDL_GetJoystickGUIDForID", "SDL_hid_open_path", "SDL_hid_read_timeout", "SDL_hid_close"} {
		if addrs[i], err = windows.GetProcAddress(h, name); err != nil {
			return err
		}
	}
	guidAddr, openAddr, readAddr, closeAddr := addrs[0], addrs[1], addrs[2], addrs[3]
	getGUID = func(id sdl.JoystickID) GUID {
		// The x64 calling convention returns a result larger than 8 bytes
		// in memory the caller provides: its address is passed as a hidden
		// first argument, in RCX, and the real arguments move one register
		// to the right. SDL_GUID is 16 bytes.
		var g GUID
		purego.SyscallN(guidAddr, uintptr(unsafe.Pointer(&g)), uintptr(id))
		return g
	}
	// The C int results are in EAX; the upper half of RAX is undefined,
	// so they are converted to int32 first. The int argument of
	// SDL_hid_read_timeout is read from the lower half of its register.
	//
	// T-input reads every watched device at least once every 5 ms, so
	// SDL_hid_read_timeout and SDL_hid_close go through syscall.SyscallN,
	// whose arguments do not escape: those of purego.SyscallN do, one
	// allocation a call.
	hidOpenPath = func(path string) uintptr {
		p := append([]byte(path), 0)
		r, _, _ := purego.SyscallN(openAddr, uintptr(unsafe.Pointer(&p[0])))
		runtime.KeepAlive(p)
		return r
	}
	hidReadTimeout = func(dev uintptr, buf []byte, milliseconds int32) int32 {
		r, _, _ := syscall.SyscallN(readAddr, dev, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(milliseconds))
		runtime.KeepAlive(buf)
		return int32(r)
	}
	hidClose = func(dev uintptr) int32 {
		r, _, _ := syscall.SyscallN(closeAddr, dev)
		return int32(r)
	}
	return nil
}
