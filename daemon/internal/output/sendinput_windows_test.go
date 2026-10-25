package output

import (
	"runtime"
	"testing"
	"unsafe"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// The layout SendInput expects on amd64 (WinUser.h): INPUT is 40 bytes,
// its union starts at offset 8 and is as large as MOUSEINPUT.
func TestINPUTLayout(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skipf("layout pinned for amd64, not %s", runtime.GOARCH)
	}
	var in input
	sizes := []struct {
		name      string
		got, want uintptr
	}{
		{"Sizeof(INPUT)", unsafe.Sizeof(in), 40},
		{"Sizeof(KEYBDINPUT)", unsafe.Sizeof(in.Ki), 24},
		{"Sizeof(MOUSEINPUT)", unsafe.Sizeof(mouseInput{}), 32},
		{"Offsetof(INPUT.Type)", unsafe.Offsetof(in.Type), 0},
		{"Offsetof(INPUT.Ki)", unsafe.Offsetof(in.Ki), 8},
		{"Offsetof(KEYBDINPUT.Vk)", unsafe.Offsetof(in.Ki.Vk), 0},
		{"Offsetof(KEYBDINPUT.Scan)", unsafe.Offsetof(in.Ki.Scan), 2},
		{"Offsetof(KEYBDINPUT.Flags)", unsafe.Offsetof(in.Ki.Flags), 4},
		{"Offsetof(KEYBDINPUT.Time)", unsafe.Offsetof(in.Ki.Time), 8},
		{"Offsetof(KEYBDINPUT.ExtraInfo)", unsafe.Offsetof(in.Ki.ExtraInfo), 16},
		{"Offsetof(MOUSEINPUT.dx)", unsafe.Offsetof(mouseInput{}.Dx), 0},
		{"Offsetof(MOUSEINPUT.dy)", unsafe.Offsetof(mouseInput{}.Dy), 4},
		{"Offsetof(MOUSEINPUT.mouseData)", unsafe.Offsetof(mouseInput{}.MouseData), 8},
		{"Offsetof(MOUSEINPUT.dwFlags)", unsafe.Offsetof(mouseInput{}.Flags), 12},
		{"Offsetof(MOUSEINPUT.time)", unsafe.Offsetof(mouseInput{}.Time), 16},
		{"Offsetof(MOUSEINPUT.dwExtraInfo)", unsafe.Offsetof(mouseInput{}.ExtraInfo), 24},
		// The union holds a KEYBDINPUT or a MOUSEINPUT: the whole of either
		// fits in the INPUT after its type, and the mouse view of the union
		// starts where the keyboard one does.
		{"Offsetof(INPUT.Ki)+Sizeof(MOUSEINPUT)", unsafe.Offsetof(in.Ki) + unsafe.Sizeof(mouseInput{}), unsafe.Sizeof(in)},
		{"offset of the mouse union in INPUT", uintptr(unsafe.Pointer(in.mouse())) - uintptr(unsafe.Pointer(&in)), 8},
		{"offset of MOUSEINPUT.dwExtraInfo in INPUT", uintptr(unsafe.Pointer(&in.mouse().ExtraInfo)) - uintptr(unsafe.Pointer(&in)), 32},
	}
	for _, s := range sizes {
		if s.got != s.want {
			t.Errorf("%s = %d, want %d", s.name, s.got, s.want)
		}
	}
}

func TestMakeInput(t *testing.T) {
	const extra = uintptr(0x7061646D_00001234)
	tests := []struct {
		name  string
		e     KeyEvent
		vk    uint16
		scan  uint16
		flags uint32
	}{
		{"scan down", KeyEvent{Scan: 0x2C}, 0, 0x2C, keyeventfScanCode},
		{"scan up", KeyEvent{Scan: 0x2C, Up: true}, 0, 0x2C, keyeventfScanCode | keyeventfKeyUp},
		{"ext down", KeyEvent{Scan: 0x1D, Ext: true}, 0, 0x1D, keyeventfScanCode | keyeventfExtendedKey},
		{"ext up", KeyEvent{Scan: 0x1D, Ext: true, Up: true}, 0, 0x1D, keyeventfScanCode | keyeventfExtendedKey | keyeventfKeyUp},
		// VK is ignored for scan code events.
		{"scan with VK", KeyEvent{Scan: 0x2C, VK: 0x5A}, 0, 0x2C, keyeventfScanCode},
		{"vk down", KeyEvent{Scan: 0x45, VK: 0x13, ByVK: true}, 0x13, 0x45, 0},
		{"vk up", KeyEvent{Scan: 0x45, VK: 0x13, ByVK: true, Up: true}, 0x13, 0x45, keyeventfKeyUp},
		{"vk ext down", KeyEvent{Scan: 0x45, VK: 0x90, ByVK: true, Ext: true}, 0x90, 0x45, keyeventfExtendedKey},
		{"vk ext up", KeyEvent{Scan: 0x45, VK: 0x90, ByVK: true, Ext: true, Up: true}, 0x90, 0x45, keyeventfExtendedKey | keyeventfKeyUp},
		{"vk no scan", KeyEvent{VK: 0x88, ByVK: true, Up: true}, 0x88, 0, keyeventfKeyUp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := makeInput(tt.e, extra)
			want := input{Type: inputKeyboard, Ki: keybdInput{Vk: tt.vk, Scan: tt.scan, Flags: tt.flags, ExtraInfo: extra}}
			if in != want {
				t.Errorf("makeInput(%+v) = %+v, want %+v", tt.e, in, want)
			}
		})
	}
}

// Every event KeyEventFor makes for the key table converts: scan code
// events carry KEYEVENTF_SCANCODE and no VK, VK events a VK and no
// KEYEVENTF_SCANCODE, and mouse events an INPUT_MOUSE: a wheel notch has a
// delta, a left, right or middle button only its flag, a side button the
// XBUTTON number as well.
func TestMakeInputTable(t *testing.T) {
	for _, e := range tableEvents(t) {
		in := makeInput(e, 0)
		if e.Mouse != keys.MouseNone {
			mi := in.mouse()
			sideButton := e.Mouse == keys.MouseX1 || e.Mouse == keys.MouseX2
			switch {
			case in.Type != inputMouse || mi.Flags == 0:
				t.Errorf("%v: %+v, want an INPUT_MOUSE with a flag", e, in)
			case e.Mouse.IsWheel() && mi.MouseData == 0:
				t.Errorf("%v: %+v, want a wheel notch with a delta", e, in)
			case e.Mouse.IsButton() && (mi.MouseData == 0) == sideButton:
				t.Errorf("%v: %+v, want mouseData only for a side button", e, in)
			}
			continue
		}
		if in.Type != inputKeyboard {
			t.Errorf("%v: Type %d, want INPUT_KEYBOARD", e, in.Type)
		}
		if e.ByVK != (in.Ki.Flags&keyeventfScanCode == 0) {
			t.Errorf("%v: flags %#x", e, in.Ki.Flags)
		}
		if e.ByVK != (in.Ki.Vk != 0) {
			t.Errorf("%v: wVk %#x", e, in.Ki.Vk)
		}
		if in.Ki.Scan != uint16(e.Scan) || in.Ki.Scan == 0 {
			t.Errorf("%v: wScan %#x", e, in.Ki.Scan)
		}
	}
}

// tableEvents returns the press of every key of keys.Table.
func tableEvents(t *testing.T) []KeyEvent {
	t.Helper()
	var evs []KeyEvent
	for _, k := range keys.Table() {
		e, err := KeyEventFor(k, false)
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		evs = append(evs, e)
	}
	return evs
}

// A media key goes by VK, and the E0 scan code and KEYEVENTF_EXTENDEDKEY go
// along (OUT-20): VK_VOLUME_UP is wVk 0xAF with wScan 0x30, as the physical key
// sends it, and no KEYEVENTF_SCANCODE, so that the layout is not asked.
func TestMakeInputMedia(t *testing.T) {
	const extra = uintptr(0x7061646D_00001234)
	tests := []struct {
		name string
		vk   uint16
		scan uint16
	}{
		{"VolumeUp", 0xAF, 0x30}, {"VolumeDown", 0xAE, 0x2E}, {"VolumeMute", 0xAD, 0x20},
		{"MediaPlayPause", 0xB3, 0x22}, {"MediaNext", 0xB0, 0x19}, {"MediaPrevious", 0xB1, 0x10}, {"MediaStop", 0xB2, 0x24},
	}
	for _, tt := range tests {
		for _, up := range []bool{false, true} {
			e, err := KeyEventFor(mustKey(t, tt.name), up)
			if err != nil {
				t.Fatal(err)
			}
			flags := uint32(keyeventfExtendedKey)
			if up {
				flags |= keyeventfKeyUp
			}
			want := input{Type: inputKeyboard, Ki: keybdInput{Vk: tt.vk, Scan: tt.scan, Flags: flags, ExtraInfo: extra}}
			if got := makeInput(e, extra); got != want {
				t.Errorf("makeInput(%s, up %v) = %+v, want %+v", tt.name, up, got, want)
			}
		}
	}
}

// A Unicode event is KEYEVENTF_UNICODE with wVk 0 and the code unit in wScan
// (OUT-21), a release with KEYEVENTF_KEYUP as well: no KEYEVENTF_SCANCODE and
// no KEYEVENTF_EXTENDEDKEY, whatever the other fields of the event say. The
// tag in dwExtraInfo is the same as any event's.
func TestMakeInputUnicode(t *testing.T) {
	const extra = uintptr(0x7061646D_00001234)
	tests := []struct {
		name  string
		e     KeyEvent
		scan  uint16
		flags uint32
	}{
		{"A down", UnicodeEvent('A', false), 'A', keyeventfUnicode},
		{"A up", UnicodeEvent('A', true), 'A', keyeventfUnicode | keyeventfKeyUp},
		{"CJK down", UnicodeEvent(0x4F60, false), 0x4F60, keyeventfUnicode},
		{"high surrogate down", UnicodeEvent(0xD83D, false), 0xD83D, keyeventfUnicode},
		{"low surrogate up", UnicodeEvent(0xDE00, true), 0xDE00, keyeventfUnicode | keyeventfKeyUp},
		{"U+FFFF", UnicodeEvent(0xFFFF, false), 0xFFFF, keyeventfUnicode},
		// Fields of other kinds of event do not reach the INPUT.
		{"scan fields ignored", KeyEvent{Unicode: true, Unit: 'Z', Scan: 0x2C, Ext: true, ByVK: true, VK: 0x5A}, 'Z', keyeventfUnicode},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := makeInput(tt.e, extra)
			want := input{Type: inputKeyboard, Ki: keybdInput{Vk: 0, Scan: tt.scan, Flags: tt.flags, ExtraInfo: extra}}
			if got != want {
				t.Errorf("makeInput(%+v) = %+v, want %+v", tt.e, got, want)
			}
		})
	}
	if keyeventfUnicode != 0x0004 {
		t.Errorf("KEYEVENTF_UNICODE = %#x, want 0x0004 (WinUser.h)", keyeventfUnicode)
	}
}

// A batch of a text's characters turns into one INPUT per Unicode event, in
// order, none left out, and a Send of them counts every one.
func TestBatchInputsUnicode(t *testing.T) {
	events := unicodeBatch(nil, 'a')
	events = unicodeBatch(events, '😀')
	inputs, before := batchInputs(events, 0)
	if len(inputs) != 6 {
		t.Fatalf("%d inputs for %d events, want 6", len(inputs), len(events))
	}
	for i := range before {
		if before[i] != i {
			t.Errorf("before[%d] = %d, want %d", i, before[i], i)
		}
	}
	wantScans := []uint16{'a', 'a', 0xD83D, 0xD83D, 0xDE00, 0xDE00}
	for i, in := range inputs {
		wantFlags := uint32(keyeventfUnicode)
		if i%2 == 1 {
			wantFlags |= keyeventfKeyUp
		}
		if in.Type != inputKeyboard || in.Ki.Vk != 0 || in.Ki.Scan != wantScans[i] || in.Ki.Flags != wantFlags {
			t.Errorf("input %d = %+v, want wScan %#x flags %#x", i, in.Ki, wantScans[i], wantFlags)
		}
	}
	for n := 0; n <= len(inputs); n++ {
		if got := injectedEvents(events, before, n); got != n {
			t.Errorf("injectedEvents(%d inputs) = %d, want %d", n, got, n)
		}
	}
}
