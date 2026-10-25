package keycapture

import (
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

func TestSidedChordsAndRelease(t *testing.T) {
	var state State
	state.Step(winhook.Event{VK: 0xA3, Scan: 0x1d, Flags: winhook.FlagExtended})
	e, ok := state.Step(winhook.Event{VK: 'Z', Scan: 0x2c})
	if !ok || e.Chord != "RCtrl+Z" || e.PhysicalChord != "RCtrl+sc:0x2C" {
		t.Fatal(e, ok)
	}
	if _, ok := state.Step(winhook.Event{VK: 'Z', Scan: 0x2c}); ok {
		t.Fatal("autorepeat captured twice")
	}
	state.Step(winhook.Event{VK: 0xA3, Scan: 0x1d, Flags: winhook.FlagExtended | winhook.FlagUp})
	state.Step(winhook.Event{VK: 'Z', Scan: 0x2c, Flags: winhook.FlagUp})
	e, ok = state.Step(winhook.Event{VK: 'Z', Scan: 0x2c})
	if !ok || e.Chord != "Z" {
		t.Fatal(e, ok)
	}
}

func TestCaptureSpecialKeysAndInjectedIgnored(t *testing.T) {
	for _, tc := range []struct {
		vk, scan, flags uint32
		want            string
	}{
		{0xA1, 0x36, 0, "RShift"}, {0xA5, 0x38, 1, "RAlt"}, {0x5c, 0x5c, 1, "RWin"},
		{0x0d, 0x1c, 1, "NumEnter"}, {0x13, 0x45, 0, "Pause"}, {0x7c, 0x64, 0, "F13"},
	} {
		var state State
		e, ok := state.Step(winhook.Event{VK: tc.vk, Scan: tc.scan, Flags: tc.flags})
		if !ok || e.Chord != tc.want {
			t.Fatal(tc, e, ok)
		}
	}
	var state State
	if _, ok := state.Step(winhook.Event{VK: 'A', Scan: 0x1e, Flags: winhook.FlagInjected}); ok {
		t.Fatal("captured synthetic output")
	}
}

// A key event with no known VK is no mouse key: the mouse keys have the zero
// mapping, so their VK 0 must not match (ADR-0062). Binding capture records
// the keyboard only.
func TestCaptureNeverYieldsMouseKeys(t *testing.T) {
	for _, vk := range []uint32{0, 0xE8, 0xFF} {
		if k, ok := keyFor(winhook.Event{VK: vk}); ok && k.Kind() == keys.KindMouse {
			t.Errorf("VK %#x resolved to the mouse key %s", vk, k)
		}
	}
}

// The media and volume keys (OUT-20) are recorded under their names, as the
// keyboard sends them: an E0 scan code and the VK of the key. The physical form
// is the raw E0 scan code, which presses the same key.
func TestCaptureMediaKeys(t *testing.T) {
	for _, tc := range []struct {
		vk, scan uint32
		want     string
		physical string
	}{
		{0xAF, 0x30, "VolumeUp", "sc:0xE030"},
		{0xAE, 0x2E, "VolumeDown", "sc:0xE02E"},
		{0xAD, 0x20, "VolumeMute", "sc:0xE020"},
		{0xB3, 0x22, "MediaPlayPause", "sc:0xE022"},
		{0xB0, 0x19, "MediaNext", "sc:0xE019"},
		{0xB1, 0x10, "MediaPrevious", "sc:0xE010"},
		{0xB2, 0x24, "MediaStop", "sc:0xE024"},
	} {
		var state State
		e, ok := state.Step(winhook.Event{VK: tc.vk, Scan: tc.scan, Flags: winhook.FlagExtended})
		if !ok || e.Chord != tc.want || e.PhysicalChord != tc.physical || e.VK != tc.vk || !e.Extended {
			t.Errorf("VK %#x: %+v, %v, want %s (%s)", tc.vk, e, ok, tc.want, tc.physical)
		}
		// With a modifier held, and not recorded again while it repeats.
		state = State{}
		state.Step(winhook.Event{VK: 0xA3, Scan: 0x1d, Flags: winhook.FlagExtended})
		e, ok = state.Step(winhook.Event{VK: tc.vk, Scan: tc.scan, Flags: winhook.FlagExtended})
		if !ok || e.Chord != "RCtrl+"+tc.want || e.PhysicalChord != "RCtrl+"+tc.physical {
			t.Errorf("RCtrl and VK %#x: %+v, %v, want RCtrl+%s", tc.vk, e, ok, tc.want)
		}
		if _, ok := state.Step(winhook.Event{VK: tc.vk, Scan: tc.scan, Flags: winhook.FlagExtended}); ok {
			t.Errorf("VK %#x: autorepeat captured twice", tc.vk)
		}
	}
}
