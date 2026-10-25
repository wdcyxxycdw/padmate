package input

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
)

// makeGUID builds a GUID as SDL_CreateJoystickGUID does
// (src/joystick/SDL_joystick.c in SDL 3.4.16).
func makeGUID(bus, vendor, product, version uint16, name string, sig, data byte) GUID {
	var g GUID
	binary.LittleEndian.PutUint16(g[0:], bus)
	binary.LittleEndian.PutUint16(g[2:], crc16(name))
	if vendor != 0 {
		binary.LittleEndian.PutUint16(g[4:], vendor)
		binary.LittleEndian.PutUint16(g[8:], product)
		binary.LittleEndian.PutUint16(g[12:], version)
		g[14], g[15] = sig, data
		return g
	}
	space := len(g) - 4 - 1 // SDL_strlcpy keeps a NUL
	if sig != 0 {
		space -= 2
		g[14], g[15] = sig, data
	}
	copy(g[4:4+min(space, len(name))], name)
	return g
}

// crc16 is SDL_crc16 (CRC-16/ARC), which SDL_CreateJoystickGUID computes
// over the name.
func crc16(s string) uint16 {
	var c uint16
	for _, b := range []byte(s) {
		c ^= uint16(b)
		for range 8 {
			if c&1 != 0 {
				c = c>>1 ^ 0xa001
			} else {
				c >>= 1
			}
		}
	}
	return c
}

// The bus types of SDL_sysjoystick.h.
const (
	busUSB       = 0x03
	busBluetooth = 0x05
	busVirtual   = 0xff
)

// TestDriverName decodes a GUID of each SDL backend, built with the
// arguments that backend passes to SDL_CreateJoystickGUID in SDL 3.4.16.
func TestDriverName(t *testing.T) {
	tests := []struct {
		name string
		g    GUID
		goos string
		want string
	}{
		// SDL_hidapijoystick.c: 'h', 0.
		{"hidapi", makeGUID(busBluetooth, 0x045e, 0x0b13, 0x0509, "", 'h', 0), "windows", "hidapi"},
		// SDL_xinputjoystick.c: 'x', the XInput subtype (1 is a gamepad).
		{"xinput", makeGUID(busUSB, 0x045e, 0x028e, 0x0114, "", 'x', 1), "windows", "xinput"},
		// SDL_rawinputjoystick.c: 'r', 0.
		{"rawinput", makeGUID(busUSB, 0x045e, 0x0b12, 0x0001, "", 'r', 0), "windows", "rawinput"},
		// SDL_windows_gaming_input.c: 'w', the SDL joystick type.
		{"wgi", makeGUID(busUSB, 0x045e, 0x0b12, 0x0001, "", 'w', 1), "windows", "wgi"},
		// SDL_gameinputjoystick.cpp: 'g', the subtype.
		{"gameinput", makeGUID(busUSB, 0x045e, 0x0b12, 0x0001, "", 'g', 1), "windows", "gameinput"},
		// SDL_virtualjoystick.c: bus 0xFF, 'v', the joystick type, and the
		// name in place of the ids.
		{"virtual", makeGUID(busVirtual, 0, 0, 0, "exp pad", 'v', 1), "windows", "virtual"},
		{"virtual with ids", makeGUID(busVirtual, 0x1234, 0x5678, 0, "", 'v', 1), "linux", "virtual"},
		{"mfi", makeGUID(busBluetooth, 0x054c, 0x0ce6, 0, "", 'm', 0), "darwin", "mfi"},
		// Backends that pass 0 as the signature.
		{"dinput", makeGUID(busUSB, 0x046d, 0xc216, 0x0100, "", 0, 0), "windows", "dinput"},
		{"evdev", makeGUID(busUSB, 0x046d, 0xc216, 0x0111, "", 0, 0), "linux", "evdev"},
		{"iokit", makeGUID(busUSB, 0x046d, 0xc216, 0x0100, "", 0, 0), "darwin", "iokit"},
		{"unsigned elsewhere", makeGUID(busUSB, 0x046d, 0xc216, 0x0100, "", 0, 0), "freebsd", "unknown"},
		// SDL_CreateJoystickGUIDForName: bus 0, no ids, no signature. A
		// short name leaves byte 14 zero.
		{"name only", makeGUID(0, 0, 0, 0, "pad", 0, 0), "windows", "dinput"},
		{"unknown signature", makeGUID(busUSB, 0x045e, 0x0b12, 0, "", 'z', 0), "windows", "sig7a"},
		{"zero", GUID{}, "windows", "dinput"},
	}
	for _, tt := range tests {
		if got := DriverName(tt.g, tt.goos); got != tt.want {
			t.Errorf("%s: DriverName(%s, %s) = %q, want %q", tt.name, tt.g, tt.goos, got, tt.want)
		}
	}
}

// TestGUIDString checks the hex form against a GUID read from SDL 3.4.16:
// a virtual gamepad named "exp pad" (bus ff00, name CRC d55c).
func TestGUIDString(t *testing.T) {
	const s = "ff005cd5657870207061640000007601"
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	g := GUID(b)
	if got := g.String(); got != s {
		t.Errorf("String() = %q, want %q", got, s)
	}
	if want := makeGUID(busVirtual, 0, 0, 0, "exp pad", 'v', 1); g != want {
		t.Errorf("SDL's GUID = %s, makeGUID = %s", g, want)
	}
	if d := DriverName(g, "windows"); d != "virtual" {
		t.Errorf("DriverName = %q, want virtual", d)
	}
	// The GameSir Nova 2 Lite of docs/hardware-matrix.md in Xbox mode, as
	// SDL 3.4.16 reported it: SDL names it after its XInput slot, and the
	// GUID has the name's CRC.
	const nova = "0300fa67373500009810000000017801"
	if g := makeGUID(busUSB, 0x3537, 0x1098, 0x100, "XInput Controller #1", 'x', 1).String(); g != nova {
		t.Errorf("GUID of the Nova 2 Lite = %s, want %s", g, nova)
	}
}
