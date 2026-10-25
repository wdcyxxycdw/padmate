package output

import (
	"fmt"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

func mustKey(t *testing.T, name string) keys.Key {
	t.Helper()
	if len(name) > 3 && name[:3] == "sc:" {
		c, err := keys.Parse(name)
		if err != nil {
			t.Fatalf("Parse(%q): %v", name, err)
		}
		return c.Key()
	}
	k, ok := keys.Lookup(name)
	if !ok {
		t.Fatalf("Lookup(%q) failed", name)
	}
	return k
}

func TestKeyEventFor(t *testing.T) {
	tests := []struct {
		name string
		want KeyEvent // Key is filled in by the test
	}{
		{"Z", KeyEvent{Scan: 0x2C}},
		{"KeyZ", KeyEvent{Scan: 0x2C}},
		{"sc:0x2C", KeyEvent{Scan: 0x2C}},
		{"sc:0xE01D", KeyEvent{Scan: 0x1D, Ext: true}},
		{"sc:0x47", KeyEvent{Scan: 0x47}}, // raw scan codes are never sent by VK
		{"Ctrl", KeyEvent{Scan: 0x1D}},
		{"LCtrl", KeyEvent{Scan: 0x1D}},
		{"RCtrl", KeyEvent{Scan: 0x1D, Ext: true}},
		{"Shift", KeyEvent{Scan: 0x2A}},
		{"RShift", KeyEvent{Scan: 0x36}},
		{"Alt", KeyEvent{Scan: 0x38}},
		{"RAlt", KeyEvent{Scan: 0x38, Ext: true}},
		{"Win", KeyEvent{Scan: 0x5B, Ext: true}},
		{"RWin", KeyEvent{Scan: 0x5C, Ext: true}},
		{"F13", KeyEvent{Scan: 0x64}},
		{"F22", KeyEvent{Scan: 0x6D}},
		{"F23", KeyEvent{Scan: 0x6E}},
		{"F24", KeyEvent{Scan: 0x76}},
		{"Plus", KeyEvent{Scan: 0x0D}},
		{"Home", KeyEvent{Scan: 0x47, Ext: true}},
		{"NumLock", KeyEvent{Scan: 0x45}},
		{"PrintScreen", KeyEvent{Scan: 0x37, Ext: true}},
		{"Pause", KeyEvent{Scan: 0x45, VK: 0x13, ByVK: true}},
		{"Num0", KeyEvent{Scan: 0x52, VK: 0x60, ByVK: true}},
		{"Num5", KeyEvent{Scan: 0x4C, VK: 0x65, ByVK: true}},
		{"Num7", KeyEvent{Scan: 0x47, VK: 0x67, ByVK: true}},
		{"Num9", KeyEvent{Scan: 0x49, VK: 0x69, ByVK: true}},
		{"NumDecimal", KeyEvent{Scan: 0x53, VK: 0x6E, ByVK: true}},
		{"NumAdd", KeyEvent{Scan: 0x4E}},
		{"NumSubtract", KeyEvent{Scan: 0x4A}},
		{"NumMultiply", KeyEvent{Scan: 0x37}},
		{"NumDivide", KeyEvent{Scan: 0x35, Ext: true}},
		{"NumEnter", KeyEvent{Scan: 0x1C, Ext: true}},
		{"KanaMode", KeyEvent{Scan: 0x70}},
		// Mouse keys: no scan code, no VK, whatever Up says.
		{"WheelUp", KeyEvent{Mouse: keys.WheelUp}},
		{"WheelDown", KeyEvent{Mouse: keys.WheelDown}},
		{"WheelLeft", KeyEvent{Mouse: keys.WheelLeft}},
		{"WheelRight", KeyEvent{Mouse: keys.WheelRight}},
		{"MouseLeft", KeyEvent{Mouse: keys.MouseLeft}},
		{"MouseRight", KeyEvent{Mouse: keys.MouseRight}},
		{"MouseMiddle", KeyEvent{Mouse: keys.MouseMiddle}},
		{"MouseX1", KeyEvent{Mouse: keys.MouseX1}},
		{"MouseX2", KeyEvent{Mouse: keys.MouseX2}},
		{"LButton", KeyEvent{Mouse: keys.MouseLeft}},
		{"XButton2", KeyEvent{Mouse: keys.MouseX2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := mustKey(t, tt.name)
			for _, up := range []bool{false, true} {
				want := tt.want
				want.Key, want.Up = k, up
				got, err := KeyEventFor(k, up)
				if err != nil {
					t.Fatalf("KeyEventFor(%s, %v): %v", tt.name, up, err)
				}
				if got != want {
					t.Errorf("KeyEventFor(%s, %v) = %+v, want %+v", tt.name, up, got, want)
				}
			}
		})
	}
}

func TestKeyEventForInvalid(t *testing.T) {
	if _, err := KeyEventFor(keys.Key{}, false); err == nil {
		t.Fatal("KeyEventFor(zero Key) succeeded")
	}
}

// Every key of the table converts, and only Pause, the numpad keys that
// depend on NumLock and the media keys go by VK.
func TestKeyEventForTable(t *testing.T) {
	byVK := map[string]bool{"Pause": true, "NumDecimal": true}
	for _, n := range []string{"Num0", "Num1", "Num2", "Num3", "Num4", "Num5", "Num6", "Num7", "Num8", "Num9"} {
		byVK[n] = true
	}
	for _, n := range []string{"VolumeUp", "VolumeDown", "VolumeMute", "MediaPlayPause", "MediaNext", "MediaPrevious", "MediaStop"} {
		byVK[n] = true
	}
	seen := 0
	for _, k := range keys.Table() {
		e, err := KeyEventFor(k, false)
		if err != nil {
			t.Errorf("%s: %v", k, err)
			continue
		}
		if m, ok := k.Mouse(); ok {
			// A mouse key makes a mouse event and nothing of a keyboard one.
			if e.Mouse != m || e.Scan != 0 || e.Ext || e.ByVK || e.VK != 0 || e.Key != k || e.Up {
				t.Errorf("%s: %+v, want a bare press of %v", k, e, m)
			}
			continue
		}
		if e.Mouse != keys.MouseNone {
			t.Errorf("%s: Mouse = %v on a keyboard event", k, e.Mouse)
		}
		us := k.US()
		if e.Scan != us.Scan || e.Ext != us.Ext {
			t.Errorf("%s: scan %#x ext %v, want %#x ext %v", k, e.Scan, e.Ext, us.Scan, us.Ext)
		}
		if e.ByVK != byVK[k.String()] {
			t.Errorf("%s: ByVK = %v", k, e.ByVK)
		}
		if e.ByVK {
			seen++
			if e.VK != us.VK || e.VK == 0 {
				t.Errorf("%s: VK %#x, want %#x", k, e.VK, us.VK)
			}
		} else if e.VK != 0 {
			t.Errorf("%s: VK %#x set on a scan code event", k, e.VK)
		}
	}
	if seen != len(byVK) {
		t.Errorf("%d keys go by VK, want %d", seen, len(byVK))
	}
}

func TestKeyEventString(t *testing.T) {
	tests := []struct {
		e    KeyEvent
		want string
	}{
		{KeyEvent{Key: mustKey(t, "LCtrl"), Scan: 0x1D}, "down LCtrl sc=0x1D"},
		{KeyEvent{Key: mustKey(t, "Z"), Scan: 0x2C, Up: true}, "up   Z sc=0x2C"},
		{KeyEvent{Key: mustKey(t, "RCtrl"), Scan: 0x1D, Ext: true}, "down RCtrl sc=E0 1D"},
		{KeyEvent{Key: mustKey(t, "Pause"), Scan: 0x45, VK: 0x13, ByVK: true, Up: true}, "up   Pause vk=0x13 sc=0x45"},
		{KeyEvent{Scan: 0x1D, Ext: true}, "down sc=E0 1D"},
		{KeyEvent{VK: 0x88, ByVK: true, Up: true}, "up   vk=0x88 sc=0x00"},
		// A mouse event has no scan code and no VK to show.
		{KeyEvent{Key: mustKey(t, "WheelUp"), Mouse: keys.WheelUp}, "down WheelUp"},
		{KeyEvent{Key: mustKey(t, "WheelDown"), Mouse: keys.WheelDown, Up: true}, "up   WheelDown"},
		{KeyEvent{Mouse: keys.WheelLeft}, "down WheelLeft"},
		// A button's press and release are both shown.
		{KeyEvent{Key: mustKey(t, "MouseLeft"), Mouse: keys.MouseLeft}, "down MouseLeft"},
		{KeyEvent{Key: mustKey(t, "RButton"), Mouse: keys.MouseRight, Up: true}, "up   MouseRight"},
		{KeyEvent{Mouse: keys.MouseX1, Up: true}, "up   MouseX1"},
		// A pointer move shows its distance, and a jump its position; the two
		// moves of the mouse canary of the injection tests are moves too.
		{KeyEvent{Mouse: keys.MouseMove, Dx: 1}, "move +1,+0"},
		{KeyEvent{Mouse: keys.MouseMove, Dx: -1}, "move -1,+0"},
		{KeyEvent{Mouse: keys.MouseMove, Dx: 12, Dy: -7}, "move +12,-7"},
		{KeyEvent{Mouse: keys.MouseJump, Dx: 32768, Dy: 0}, "jump 32768,0"},
	}
	for _, tt := range tests {
		if got := tt.e.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func TestKeyEventName(t *testing.T) {
	tests := []struct {
		e    KeyEvent
		want string
	}{
		{KeyEvent{Key: mustKey(t, "LShift"), Scan: 0x2A}, "LShift"},
		{KeyEvent{VK: 0x13, ByVK: true}, "vk=0x13"},
		{KeyEvent{Scan: 0x1D, Ext: true}, "sc=E0 1D"},
		{KeyEvent{Scan: 0x2C}, "sc=0x2C"},
		{KeyEvent{Key: mustKey(t, "WheelUp"), Mouse: keys.WheelUp}, "WheelUp"},
		{KeyEvent{Mouse: keys.WheelRight}, "WheelRight"},
		{KeyEvent{Key: mustKey(t, "MButton"), Mouse: keys.MouseMiddle}, "MouseMiddle"},
		{KeyEvent{Mouse: keys.MouseX2}, "MouseX2"},
	}
	for _, tt := range tests {
		if got := tt.e.name(); got != tt.want {
			t.Errorf("name() = %q, want %q", got, tt.want)
		}
	}
}

func TestSameKey(t *testing.T) {
	tests := []struct {
		a, b KeyEvent
		want bool
	}{
		{KeyEvent{Scan: 0x1D}, KeyEvent{Scan: 0x1D, Up: true}, true},
		{KeyEvent{Key: mustKey(t, "LCtrl"), Scan: 0x1D}, KeyEvent{Scan: 0x1D}, true},
		{KeyEvent{Scan: 0x1D}, KeyEvent{Scan: 0x1D, Ext: true}, false},
		{KeyEvent{Scan: 0x45, VK: 0x13, ByVK: true}, KeyEvent{Scan: 0x45}, false},
		{KeyEvent{Scan: 0x45, VK: 0x13, ByVK: true}, KeyEvent{VK: 0x13, ByVK: true}, true},
		{KeyEvent{Scan: 0x47, VK: 0x67, ByVK: true}, KeyEvent{Scan: 0x47, VK: 0x24, ByVK: true}, false},
		// A wheel key is its own key: a press and a release are one, two wheel
		// keys are two, and none is a keyboard key, nor the zero KeyID.
		{KeyEvent{Mouse: keys.WheelUp}, KeyEvent{Mouse: keys.WheelUp, Up: true}, true},
		{KeyEvent{Key: mustKey(t, "WheelUp"), Mouse: keys.WheelUp}, KeyEvent{Mouse: keys.WheelUp}, true},
		{KeyEvent{Mouse: keys.WheelUp}, KeyEvent{Mouse: keys.WheelDown}, false},
		{KeyEvent{Mouse: keys.WheelLeft}, KeyEvent{Mouse: keys.WheelRight}, false},
		{KeyEvent{Mouse: keys.WheelUp}, KeyEvent{}, false},
		{KeyEvent{Mouse: keys.WheelUp}, KeyEvent{Scan: 0x48, Ext: true}, false},
		{KeyEvent{Mouse: keys.WheelUp, Scan: 0x2C}, KeyEvent{Mouse: keys.WheelUp}, true},
		// A button is its own key too: press and release are one, two buttons
		// are two, and it is neither a wheel key nor a keyboard key.
		{KeyEvent{Mouse: keys.MouseLeft}, KeyEvent{Mouse: keys.MouseLeft, Up: true}, true},
		{KeyEvent{Key: mustKey(t, "LButton"), Mouse: keys.MouseLeft}, KeyEvent{Mouse: keys.MouseLeft}, true},
		{KeyEvent{Mouse: keys.MouseLeft}, KeyEvent{Mouse: keys.MouseRight}, false},
		{KeyEvent{Mouse: keys.MouseX1}, KeyEvent{Mouse: keys.MouseX2}, false},
		{KeyEvent{Mouse: keys.MouseLeft}, KeyEvent{Mouse: keys.WheelUp}, false},
		{KeyEvent{Mouse: keys.MouseMiddle}, KeyEvent{}, false},
		{KeyEvent{Mouse: keys.MouseMiddle}, KeyEvent{ByVK: true, VK: 0x04}, false},
		{KeyEvent{Mouse: keys.MouseLeft}, KeyEvent{ByVK: true, VK: 0x01}, false},
	}
	for _, tt := range tests {
		if got := tt.a.sameKey(tt.b); got != tt.want {
			t.Errorf("%v.sameKey(%v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
		if got := tt.b.sameKey(tt.a); got != tt.want {
			t.Errorf("%v.sameKey(%v) = %v, want %v", tt.b, tt.a, got, tt.want)
		}
		if got := tt.a.ID() == tt.b.ID(); got != tt.want {
			t.Errorf("%v.ID() == %v.ID() is %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestKeyIDString(t *testing.T) {
	tests := []struct {
		e    KeyEvent
		want string
	}{
		{KeyEvent{Key: mustKey(t, "Z"), Scan: 0x2C}, "sc:0x2C"},
		{KeyEvent{Scan: 0x1D, Ext: true, Up: true}, "sc:0xE01D"},
		{KeyEvent{Key: mustKey(t, "Pause"), Scan: 0x45, VK: 0x13, ByVK: true}, "vk:0x13"},
		{KeyEvent{Scan: 0x4F, VK: 0x61, ByVK: true, Ext: true}, "vk:0x61"},
		{KeyEvent{Key: mustKey(t, "WheelUp"), Mouse: keys.WheelUp}, "WheelUp"},
		{KeyEvent{Mouse: keys.WheelRight, Up: true}, "WheelRight"},
		{KeyEvent{Key: mustKey(t, "MouseLeft"), Mouse: keys.MouseLeft}, "MouseLeft"},
		{KeyEvent{Mouse: keys.MouseX2, Up: true}, "MouseX2"},
	}
	for _, tt := range tests {
		if got := tt.e.ID().String(); got != tt.want {
			t.Errorf("%v: ID().String() = %q, want %q", tt.e, got, tt.want)
		}
	}
	// The raw KeySpec of a scan code key names the same key.
	for _, name := range []string{"sc:0x2C", "sc:0xE01D"} {
		e, err := KeyEventFor(mustKey(t, name), false)
		if err != nil {
			t.Fatal(err)
		}
		if got := e.ID().String(); got != name {
			t.Errorf("KeyEventFor(%s).ID().String() = %q", name, got)
		}
	}
}

// Only the release of a wheel key injects nothing: a button's release is as
// real as its press, and neither is momentary.
func TestKeyEventSilentButtons(t *testing.T) {
	for _, name := range []string{"MouseLeft", "MouseRight", "MouseMiddle", "MouseX1", "MouseX2"} {
		for _, up := range []bool{false, true} {
			e, err := KeyEventFor(mustKey(t, name), up)
			if err != nil {
				t.Fatal(err)
			}
			if e.Silent() || e.momentary() || e.ID().momentary() || !e.isMouse() || !e.ID().button() || e.Up != up {
				t.Errorf("%s (up %v): %+v, silent %v, momentary %v, want an injected, held mouse event", name, up, e, e.Silent(), e.momentary())
			}
		}
	}
	for _, name := range []string{"WheelUp", "WheelDown", "WheelLeft", "WheelRight"} {
		e, err := KeyEventFor(mustKey(t, name), false)
		if err != nil || !e.isMouse() || e.ID().button() {
			t.Errorf("%s: %+v, %v; want a mouse event that is no button", name, e, err)
		}
	}
	if (KeyEvent{Scan: 0x2C}).isMouse() || (KeyID{Scan: 0x2C}).button() || (KeyID{}).button() {
		t.Error("a keyboard key is a mouse button")
	}
}

// Only the release of a wheel key injects nothing.
func TestKeyEventSilent(t *testing.T) {
	tests := []struct {
		e    KeyEvent
		want bool
	}{
		{KeyEvent{Mouse: keys.WheelUp}, false},
		{KeyEvent{Mouse: keys.WheelUp, Up: true}, true},
		{KeyEvent{Mouse: keys.WheelDown, Up: true}, true},
		{KeyEvent{Mouse: keys.WheelLeft, Up: true}, true},
		{KeyEvent{Mouse: keys.WheelRight, Up: true}, true},
		{KeyEvent{Mouse: keys.WheelRight}, false},
		{KeyEvent{Key: mustKey(t, "Z"), Scan: 0x2C, Up: true}, false},
		{KeyEvent{Key: mustKey(t, "Z"), Scan: 0x2C}, false},
		{MenuMaskEvent(true), false},
		{KeyEvent{}, false},
		{KeyEvent{Up: true}, false},
	}
	for _, tt := range tests {
		if got := tt.e.Silent(); got != tt.want {
			t.Errorf("%+v.Silent() = %v, want %v", tt.e, got, tt.want)
		}
	}
	// The release KeyEventFor makes for a wheel key is the silent one.
	for _, name := range []string{"WheelUp", "WheelDown", "WheelLeft", "WheelRight"} {
		up, err := KeyEventFor(mustKey(t, name), true)
		if err != nil || !up.Silent() || !up.momentary() || !up.ID().momentary() {
			t.Errorf("%s release: %+v, %v; want a silent wheel event", name, up, err)
		}
		down, err := KeyEventFor(mustKey(t, name), false)
		if err != nil || down.Silent() || !down.momentary() {
			t.Errorf("%s press: %+v, %v; want a wheel press that injects", name, down, err)
		}
	}
	if (KeyEvent{Scan: 0x2C}).momentary() || (KeyID{Scan: 0x2C}).momentary() || (KeyID{}).momentary() {
		t.Error("a keyboard key is momentary")
	}
}

// A pointer event is a mouse event that is no key: it has an ID of its own,
// neither the zero KeyID nor another mouse input's, is never silent (it is
// injected), takes no part in the event gap, and has no release to speak of.
func TestPointerEvent(t *testing.T) {
	move := KeyEvent{Mouse: keys.MouseMove, Dx: 3, Dy: -4}
	jump := KeyEvent{Mouse: keys.MouseJump, Dx: 1, Dy: 2}
	for _, e := range []KeyEvent{move, jump} {
		if !e.Pointer() || !e.isMouse() || e.Silent() || e.momentary() || !e.gapless() || e.ID().momentary() || e.ID().button() {
			t.Errorf("%v: Pointer %v, isMouse %v, Silent %v, momentary %v, gapless %v", e, e.Pointer(), e.isMouse(), e.Silent(), e.momentary(), e.gapless())
		}
		if e.ID() == (KeyID{}) {
			t.Errorf("%v has the zero KeyID", e)
		}
	}
	if move.ID() == jump.ID() {
		t.Error("a move and a jump have one KeyID")
	}
	// The distance is not part of the identity: two moves are one input.
	if move.ID() != (KeyEvent{Mouse: keys.MouseMove, Dx: -9}).ID() {
		t.Error("two moves have different KeyIDs")
	}
	for _, e := range []KeyEvent{{Mouse: keys.WheelUp}, {Mouse: keys.MouseLeft}, {Scan: 0x2C}} {
		if e.Pointer() {
			t.Errorf("%v is a pointer event", e)
		}
	}
	if (KeyEvent{Mouse: keys.WheelUp, Up: true}).gapless() != true || (KeyEvent{Mouse: keys.MouseLeft, Up: true}).gapless() {
		t.Error("only the wheel release and the pointer are gapless")
	}
}

// The media and volume keys (ADR-0070, OUT-20) are sent by VK, with the E0 scan
// code and the extended-key flag along, so the system never has to guess the key
// from a layout: the press and the release carry the same VK and scan code.
func TestKeyEventForMedia(t *testing.T) {
	tests := []struct {
		name string
		want KeyEvent
	}{
		{"VolumeUp", KeyEvent{Scan: 0x30, Ext: true, VK: 0xAF, ByVK: true}},
		{"VolumeDown", KeyEvent{Scan: 0x2E, Ext: true, VK: 0xAE, ByVK: true}},
		{"VolumeMute", KeyEvent{Scan: 0x20, Ext: true, VK: 0xAD, ByVK: true}},
		{"MediaPlayPause", KeyEvent{Scan: 0x22, Ext: true, VK: 0xB3, ByVK: true}},
		{"MediaNext", KeyEvent{Scan: 0x19, Ext: true, VK: 0xB0, ByVK: true}},
		{"MediaPrevious", KeyEvent{Scan: 0x10, Ext: true, VK: 0xB1, ByVK: true}},
		{"MediaStop", KeyEvent{Scan: 0x24, Ext: true, VK: 0xB2, ByVK: true}},
		{"AudioVolumeMute", KeyEvent{Scan: 0x20, Ext: true, VK: 0xAD, ByVK: true}},
		{"MediaTrackPrevious", KeyEvent{Scan: 0x10, Ext: true, VK: 0xB1, ByVK: true}},
	}
	ids := map[KeyID]string{}
	for _, tt := range tests {
		k := mustKey(t, tt.name)
		for _, up := range []bool{false, true} {
			want := tt.want
			want.Key, want.Up = k, up
			got, err := KeyEventFor(k, up)
			if err != nil || got != want {
				t.Errorf("KeyEventFor(%s, %v) = %+v, %v, want %+v", tt.name, up, got, err, want)
			}
			if got.Silent() || got.momentary() || got.isMouse() || got.Pointer() || got.Unicode {
				t.Errorf("%s: %+v is not an ordinary held key", tt.name, got)
			}
		}
		// A media key is held like any key: its press and release are one KeyID,
		// which is the key's VK, and no other key has it, not even the raw scan
		// code of the same physical key.
		press, _ := KeyEventFor(k, false)
		release, _ := KeyEventFor(k, true)
		id := press.ID()
		if !press.sameKey(release) || id != (KeyID{ByVK: true, VK: tt.want.VK}) {
			t.Errorf("%s: ID %+v, want the VK %#x", tt.name, id, tt.want.VK)
		}
		if prev, dup := ids[id]; dup && !strings.HasPrefix(tt.name, "Audio") && !strings.HasPrefix(tt.name, "MediaTrack") {
			t.Errorf("%s and %s share the KeyID %v", tt.name, prev, id)
		}
		ids[id] = tt.name
		raw, err := KeyEventFor(mustKey(t, fmt.Sprintf("sc:0xE0%02X", tt.want.Scan)), false)
		if err != nil || raw.sameKey(press) {
			t.Errorf("%s and its raw scan code %v are one KeyID (%v)", tt.name, raw, err)
		}
		if got, want := press.ID().String(), fmt.Sprintf("vk:0x%02X", tt.want.VK); got != want {
			t.Errorf("%s: ID().String() = %q, want %q", tt.name, got, want)
		}
		if got := press.String(); !strings.Contains(got, fmt.Sprintf("vk=0x%02X", tt.want.VK)) || !strings.Contains(got, fmt.Sprintf("sc=E0 %02X", tt.want.Scan)) {
			t.Errorf("%s: String() = %q, want the VK and the E0 scan code", tt.name, got)
		}
	}
}

// A Unicode event is one UTF-16 unit of a text, typed by KEYEVENTF_UNICODE. It
// is its own kind of key: it matches no keyboard key, no mouse input and not the
// zero KeyID, and a unit's press and release are one key. Unlike a wheel
// notch, the release is a real event.
func TestUnicodeEvent(t *testing.T) {
	down, up := UnicodeEvent('A', false), UnicodeEvent('A', true)
	if !down.Unicode || down.Unit != 'A' || down.Up || !up.Up || !up.Unicode || up.Unit != 'A' {
		t.Fatalf("UnicodeEvent: %+v, %+v", down, up)
	}
	if down.Key.IsValid() || down.Mouse != keys.MouseNone || down.ByVK || down.Ext || down.Scan != 0 || down.VK != 0 {
		t.Errorf("a Unicode event has fields of another kind: %+v", down)
	}
	if down.Silent() || up.Silent() || down.momentary() || down.Pointer() || down.isMouse() || down.gapless() {
		t.Errorf("a Unicode event is a real, ordered event: silent %v/%v", down.Silent(), up.Silent())
	}
	if !down.sameKey(up) || down.sameKey(UnicodeEvent('B', false)) {
		t.Error("a unit's press and release are one key, and two units are two")
	}
	for _, other := range []KeyEvent{{}, {Scan: 0x41}, {Scan: 0x1E}, {ByVK: true, VK: 'A'}, {ByVK: true, VK: 0xE7}, {Mouse: keys.WheelUp}, {Mouse: keys.MouseLeft}} {
		if down.sameKey(other) || down.ID() == other.ID() {
			t.Errorf("the Unicode event for A is the same key as %+v", other)
		}
	}
	if got := down.ID().String(); got != "text:U+0041" {
		t.Errorf("ID().String() = %q, want text:U+0041", got)
	}
	if UnicodeEvent(0, false).ID() == (KeyID{}) {
		t.Error("the Unicode event for U+0000 has the zero KeyID")
	}
}

func TestUnicodeEventString(t *testing.T) {
	tests := []struct {
		e    KeyEvent
		want string
		name string
	}{
		{UnicodeEvent('A', false), "down text U+0041 'A'", "text:U+0041"},
		{UnicodeEvent('A', true), "up   text U+0041 'A'", "text:U+0041"},
		{UnicodeEvent(0x4F60, false), "down text U+4F60 '你'", "text:U+4F60"},
		// Half of a surrogate pair is no character on its own.
		{UnicodeEvent(0xD83D, false), "down text U+D83D", "text:U+D83D"},
		{UnicodeEvent(0xDE00, true), "up   text U+DE00", "text:U+DE00"},
		// A unit that is not printable is shown by its number only.
		{UnicodeEvent(0x200B, false), "down text U+200B", "text:U+200B"},
	}
	for _, tt := range tests {
		if got := tt.e.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
		if got := tt.e.name(); got != tt.name {
			t.Errorf("name() = %q, want %q", got, tt.name)
		}
	}
}

// textUnits splits a text into the batches of UTF-16 units that TypeText types:
// one per character, a surrogate pair together.
func TestUnicodeBatch(t *testing.T) {
	tests := []struct {
		r    rune
		want string
	}{
		{'a', "down text U+0061 'a' | up   text U+0061 'a'"},
		{'你', "down text U+4F60 '你' | up   text U+4F60 '你'"},
		{'😀', "down text U+D83D | up   text U+D83D | down text U+DE00 | up   text U+DE00"},
	}
	for _, tt := range tests {
		var parts []string
		for _, e := range unicodeBatch(nil, tt.r) {
			parts = append(parts, e.String())
		}
		if got := strings.Join(parts, " | "); got != tt.want {
			t.Errorf("unicodeBatch(%q) = %s, want %s", tt.r, got, tt.want)
		}
	}
}

func TestTypedText(t *testing.T) {
	tests := []struct {
		name   string
		events []KeyEvent
		want   string
	}{
		{"none", nil, ""},
		{"keys only", []KeyEvent{{Scan: 0x2C}, {Scan: 0x2C, Up: true}}, ""},
		{"a character", unicodeBatch(nil, 'a'), "a"},
		{"CJK", unicodeBatch(nil, '你'), "你"},
		{"a pair", unicodeBatch(nil, '😀'), "😀"},
		{"several characters", unicodeBatch(unicodeBatch(unicodeBatch(nil, 'a'), '😀'), 'b'), "a😀b"},
		{"releases only", []KeyEvent{UnicodeEvent('a', true)}, ""},
		{"mixed with a key", append(append([]KeyEvent{{Scan: 0x1C}}, unicodeBatch(nil, 'x')...), KeyEvent{Scan: 0x1C, Up: true}), "x"},
		{"a lone high half", []KeyEvent{UnicodeEvent(0xD83D, false), UnicodeEvent(0xD83D, true)}, "�"},
		{"a lone low half", []KeyEvent{UnicodeEvent(0xDE00, false)}, "�"},
		{"a control character", unicodeBatch(nil, 0x7F), "\x7f"},
	}
	for _, tt := range tests {
		if got := TypedText(tt.events); got != tt.want {
			t.Errorf("%s: TypedText = %q, want %q", tt.name, got, tt.want)
		}
	}
}
