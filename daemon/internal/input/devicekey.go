package input

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

// DeviceKey identifies a gamepad across reconnections, unlike DeviceID.
// It is comparable, so it can be a map key.
//
// A gamepad with a serial is known by its GUID and serial; one without, by
// its GUID and device path, so the same gamepad on another USB port is
// another key. The path of an XInput gamepad is its XInput slot, not a
// port, and does not count. Identical gamepads connected at the same time,
// with the same GUID and serial or path (or neither, like SDL virtual and
// XInput gamepads), are told apart by N. The canonical text is also the
// persisted identity in state.json (PRO-11).
type DeviceKey struct {
	GUID   string // DeviceInfo.GUID
	Serial string // DeviceInfo.Serial, when the device has one
	Path   string // DeviceInfo.Path, only when Serial is "" and it is not an XInput slot
	// N is 0 for the first device with this GUID, Serial and Path, and 1,
	// 2, ... for identical ones connected at the same time.
	N int
}

// xinputPath starts the path that SDL's XInput backend gives a device,
// followed by its XInput user index (SDL_xinputjoystick.c).
const xinputPath = "XInput#"

// KeyOf returns the key of info with N = 0, or the zero key for nil. The
// zero key is also that of a gamepad without a GUID, serial or path, as
// in a hand-written trace.
func KeyOf(info *DeviceInfo) DeviceKey {
	switch {
	case info == nil:
		return DeviceKey{}
	case info.Serial != "":
		return DeviceKey{GUID: info.GUID, Serial: info.Serial}
	case strings.HasPrefix(info.Path, xinputPath):
		// XInput gives a gamepad the lowest free slot, so the slot
		// changes when another XInput device took it meanwhile. A gamepad
		// that SDL names after its slot, such as "XInput Controller #1",
		// still changes key with it: the GUID has the name's CRC.
		return DeviceKey{GUID: info.GUID}
	default:
		return DeviceKey{GUID: info.GUID, Path: info.Path}
	}
}

// String returns the GUID, then "#N" when N > 0, then "/s:SERIAL" or
// "/p:PATH", such as "0300…6800#1/p:\\?\HID#VID_045E…". It needs no
// escaping, since the GUID of a DeviceInfo is hex or empty (ReadEvents
// checks it too). ParseDeviceKey reads the canonical form.
func (k DeviceKey) String() string {
	s := k.GUID
	if k.N > 0 {
		s += "#" + strconv.Itoa(k.N)
	}
	switch {
	case k.Serial != "":
		s += "/s:" + k.Serial
	case k.Path != "":
		s += "/p:" + k.Path
	}
	return s
}

// MarshalText returns String, so that JSON and slog show a key as a
// string.
func (k DeviceKey) MarshalText() ([]byte, error) {
	return []byte(k.String()), nil
}

// ParseDeviceKey reads the canonical persisted form without weakening device
// identity: serial/path suffixes are opaque and keep their case and separators.
func ParseDeviceKey(text string) (DeviceKey, error) {
	bad := errors.New("input: invalid or noncanonical device key")
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return DeviceKey{}, bad
	}
	head, suffix, hasSuffix := strings.Cut(text, "/")
	guid, count, hasCount := strings.Cut(head, "#")
	if len(guid) != 0 && len(guid) != 32 {
		return DeviceKey{}, bad
	}
	for _, c := range guid {
		if c < '0' || c > '9' {
			if c < 'a' || c > 'f' {
				return DeviceKey{}, bad
			}
		}
	}
	k := DeviceKey{GUID: guid}
	if hasCount {
		n, err := strconv.Atoi(count)
		if err != nil || n <= 0 || strconv.Itoa(n) != count {
			return DeviceKey{}, bad
		}
		k.N = n
	}
	if hasSuffix {
		if len(suffix) <= 2 {
			return DeviceKey{}, bad
		}
		switch suffix[:2] {
		case "s:":
			k.Serial = suffix[2:]
		case "p:":
			k.Path = suffix[2:]
		default:
			return DeviceKey{}, bad
		}
	}
	return k, nil
}

// UnmarshalText allows canonical DeviceKey strings as JSON object keys.
func (k *DeviceKey) UnmarshalText(text []byte) error {
	parsed, err := ParseDeviceKey(string(text))
	if err != nil {
		return err
	}
	*k = parsed
	return nil
}

// maxDisconnected is how many disconnected gamepads a Registry remembers.
const maxDisconnected = 32

// A Registry gives connected gamepads their DeviceKey and remembers the
// keys of disconnected ones, so that a gamepad that comes back gets its
// key again. Its methods must be called from one goroutine.
type Registry struct {
	connected    []registered // in connection order
	disconnected []DeviceKey  // the earliest disconnected first
}

type registered struct {
	id  DeviceID
	key DeviceKey
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Connect registers a connected gamepad. It returns its key and whether
// the key belongs to a gamepad that was connected before and is now back.
//
// Of the disconnected gamepads with the base key of info, the one with
// the lowest N comes back. Without one, the gamepad gets the lowest N
// that no gamepad with that base key has, connected or disconnected. So
// two identical gamepads may swap their numbers when they reconnect.
//
// Connect ignores nil and a device that is not a gamepad, returning the
// zero key and false. For an id that is already connected it returns its
// key and false. The zero key is a valid key too (see KeyOf), so whether
// Connect registered info is info.IsGamepad, or Key(info.ID), not k.
func (r *Registry) Connect(info *DeviceInfo) (k DeviceKey, reconnected bool) {
	if info == nil || !info.IsGamepad {
		return DeviceKey{}, false
	}
	if k, ok := r.Key(info.ID); ok {
		return k, false
	}
	base := KeyOf(info)
	back := -1
	for i, d := range r.disconnected {
		if d.base() == base && (back < 0 || d.N < r.disconnected[back].N) {
			back = i
		}
	}
	if back >= 0 {
		k = r.disconnected[back]
		r.disconnected = append(r.disconnected[:back], r.disconnected[back+1:]...)
	} else {
		// No disconnected gamepad has this base key, so only the
		// connected ones can have k.
		k = base
		for r.isConnected(k) {
			k.N++
		}
	}
	r.connected = append(r.connected, registered{info.ID, k})
	return k, back >= 0
}

// Disconnect unregisters the connected gamepad id and returns its key,
// which Connect gives it again when it comes back. It returns the zero key
// and false when id is not connected. When more than 32 gamepads are
// disconnected, the one disconnected earliest is forgotten.
func (r *Registry) Disconnect(id DeviceID) (DeviceKey, bool) {
	for i, c := range r.connected {
		if c.id == id {
			r.connected = append(r.connected[:i], r.connected[i+1:]...)
			if len(r.disconnected) == maxDisconnected {
				r.disconnected = append(r.disconnected[:0], r.disconnected[1:]...)
			}
			r.disconnected = append(r.disconnected, c.key)
			return c.key, true
		}
	}
	return DeviceKey{}, false
}

// Key returns the key of the connected gamepad id.
func (r *Registry) Key(id DeviceID) (DeviceKey, bool) {
	for _, c := range r.connected {
		if c.id == id {
			return c.key, true
		}
	}
	return DeviceKey{}, false
}

// Connected returns the keys of the connected gamepads in the order they
// connected.
func (r *Registry) Connected() []DeviceKey {
	keys := make([]DeviceKey, len(r.connected))
	for i, c := range r.connected {
		keys[i] = c.key
	}
	return keys
}

// isConnected reports whether a connected gamepad has key k.
func (r *Registry) isConnected(k DeviceKey) bool {
	for _, c := range r.connected {
		if c.key == k {
			return true
		}
	}
	return false
}

// base returns k with N = 0.
func (k DeviceKey) base() DeviceKey {
	k.N = 0
	return k
}
