package input

import (
	"encoding/hex"
	"fmt"
	"runtime"
)

// GUID is SDL's 16-byte joystick GUID. SDL_CreateJoystickGUID
// (src/joystick/SDL_joystick.c) lays it out as little-endian uint16s:
//
//	[0:2]   bus type (0x03 USB, 0x05 Bluetooth, 0xFF virtual)
//	[2:4]   CRC-16 of the name
//	[4:6]   vendor id, [6:8] zero
//	[8:10]  product id, [10:12] zero
//	[12:14] product version
//	[14]    driver signature, [15] driver data
//
// When the vendor id is 0, bytes 4 on hold the start of the name instead,
// and bytes 14 and 15 keep the driver signature only if there is one.
type GUID [16]byte

// String returns the GUID as 32 lowercase hex digits, as SDL_GUIDToString
// does.
func (g GUID) String() string { return hex.EncodeToString(g[:]) }

// isGUIDString reports whether s is the String of a GUID.
func isGUIDString(s string) bool {
	if len(s) != 2*len(GUID{}) {
		return false
	}
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// driverSignatures maps the driver signature byte of a GUID to the name of
// the SDL backend that set it, from SDL 3.4.16: the SDL_IsJoystick*
// functions in src/joystick/SDL_joystick.c and the SDL_CreateJoystickGUID
// calls of each backend.
var driverSignatures = map[byte]string{
	'g': "gameinput", // SDL_gameinputjoystick.cpp
	'h': "hidapi",    // SDL_hidapijoystick.c
	'm': "mfi",       // Apple GameController framework
	'r': "rawinput",  // SDL_rawinputjoystick.c
	'v': "virtual",   // SDL_virtualjoystick.c
	'w': "wgi",       // SDL_windows_gaming_input.c
	'x': "xinput",    // SDL_xinputjoystick.c
}

// unsignedDrivers names the backend of a GUID without a driver signature,
// per OS: the backends that pass 0 to SDL_CreateJoystickGUID. On Windows
// that is DirectInput, and also GameInput for a device it reports as a
// generic controller, to keep DirectInput-compatible mappings.
var unsignedDrivers = map[string]string{
	"windows": "dinput",
	"linux":   "evdev",
	"darwin":  "iokit",
}

// DriverName returns the SDL backend that created g on an OS goos, such as
// runtime.GOOS: a name from driverSignatures, the OS's unsigned backend,
// or "sig<hex>" for a signature this SDL version does not define.
func DriverName(g GUID, goos string) string {
	sig := g[14]
	if sig == 0 {
		if d, ok := unsignedDrivers[goos]; ok {
			return d
		}
		return "unknown"
	}
	if d, ok := driverSignatures[sig]; ok {
		return d
	}
	return fmt.Sprintf("sig%02x", sig)
}

// driverName is DriverName on this OS.
func driverName(g GUID) string { return DriverName(g, runtime.GOOS) }
