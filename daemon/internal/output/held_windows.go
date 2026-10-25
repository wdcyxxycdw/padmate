//go:build windows

package output

import "github.com/wdcyxxycdw/padmate/daemon/internal/keys"

// keyDownBit is the bit of GetAsyncKeyState's result that reports the key
// down.
const keyDownBit uint16 = 0x8000

// mapvkVscToVkEx is MapVirtualKeyEx's MAPVK_VSC_TO_VK_EX: a scan code, with
// 0xE0 in the high byte for an E0 key, to a VK that tells left and right
// apart.
const mapvkVscToVkEx = 3

// AsyncKeyDown reports whether GetAsyncKeyState has e's key down. It is
// the default LoopOptions.KeyDown of the startup sweep of the held-key log
// (OUT-14). A key sent by VK is looked up by that VK, a modifier or a named
// key by its US VK, which no layout changes, and any other key by its scan
// code on the keyboard layout of the calling thread. A mouse button is looked
// up by its VK (VK_LBUTTON, ...); GetAsyncKeyState reports the physical
// buttons, not the logical ones the swap-buttons setting maps them to
// (according to Microsoft's documentation), so with swapped buttons it is the
// physical button of that name that is meant; how an injected button of that
// name and the swap interact is not measured. According to
// Microsoft's documentation it returns false for every key while the
// calling thread's desktop is not the input desktop, on the lock screen or
// the secure desktop (not measured).
func AsyncKeyDown(e KeyEvent) bool {
	vk, ok := virtualKey(e)
	return ok && uint16(getAsyncKeyState(int32(vk)))&keyDownBit != 0
}

// virtualKey returns the VK that GetAsyncKeyState knows e's key by.
func virtualKey(e KeyEvent) (uint32, bool) {
	if e.Mouse != keys.MouseNone {
		vk, ok := buttonVK(e.Mouse)
		return uint32(vk), ok // a wheel notch is no key that stays down
	}
	if e.ByVK {
		return uint32(e.VK), e.VK != 0
	}
	if k := e.Key; (k.Kind() == keys.KindModifier || k.Kind() == keys.KindNamed) && k.US().VK != 0 {
		return uint32(k.US().VK), true
	}
	scan := uint32(e.Scan)
	if e.Ext {
		scan |= 0xE000
	}
	vk := mapVirtualKeyEx(scan, mapvkVscToVkEx, 0)
	return vk, vk != 0
}
