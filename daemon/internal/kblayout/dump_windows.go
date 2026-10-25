package kblayout

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	mapvkVSCToVKEx = 3 // MAPVK_VSC_TO_VK_EX
	mapvkVKToVSCEx = 4 // MAPVK_VK_TO_VSC_EX

	// toUnicodeNoStateChange is ToUnicodeEx wFlags bit 2: leave the
	// keyboard state, including any pending dead key, unchanged
	// (Windows 10 1607 and later).
	toUnicodeNoStateChange = 0x4

	vkShift    = 0x10 // VK_SHIFT
	vkControl  = 0x11 // VK_CONTROL
	vkMenu     = 0x12 // VK_MENU
	vkLShift   = 0xA0 // VK_LSHIFT
	vkLControl = 0xA2 // VK_LCONTROL
	vkRMenu    = 0xA5 // VK_RMENU
)

const layoutsKey = `SYSTEM\CurrentControlSet\Control\Keyboard Layouts\`

// layerDriverValues are the i8042prt values that pick the DLL a Japanese
// or Korean layout delegates to, by primary language.
var layerDriverValues = map[uint16]string{
	0x11: "LayerDriver JPN",
	0x12: "LayerDriver KOR",
}

// LayoutList returns the keyboard layouts loaded for the user, as
// GetKeyboardLayoutList reports them.
func LayoutList() ([]windows.Handle, error) {
	n, err := getKeyboardLayoutList(0, nil)
	if n == 0 {
		return nil, fmt.Errorf("kblayout: GetKeyboardLayoutList: %w", err)
	}
	list := make([]windows.Handle, n)
	got, err := getKeyboardLayoutList(n, &list[0])
	if got == 0 {
		return nil, fmt.Errorf("kblayout: GetKeyboardLayoutList: %w", err)
	}
	return list[:got], nil
}

// FormatHKL formats a keyboard layout handle as in Fixture.HKL.
func FormatHKL(hkl windows.Handle) string {
	return fmt.Sprintf("0x%08X", uint64(hkl))
}

// Dump loads the layout klid, reads its tables and returns them as a
// Fixture with Generator and Version left empty for the caller.
//
// It loads the layout through WithLoaded, so the user's layout list ends
// as it started. The tables are read through MapVirtualKeyExW, ToUnicodeEx
// (with the flag that leaves the keyboard state alone) and VkKeyScanExW;
// the "Layout File", "Layout Text" and layer driver names are read from
// the registry without write access.
func Dump(klid string) (*Fixture, error) {
	if !ValidKLID(klid) {
		return nil, badKLID(klid)
	}
	klid = strings.ToUpper(klid)
	f := &Fixture{KLID: klid, Windows: windowsVersion()}
	var err error
	if f.LayoutFile, f.LayoutText, err = layoutNames(klid); err != nil {
		return nil, err
	}
	f.LayerDriver = LayerDriver(klid)
	err = WithLoaded(klid, func(hkl windows.Handle) error {
		f.HKL = FormatHKL(hkl)
		f.Scancodes = dumpScancodes(hkl)
		var err error
		if f.VKToScan, err = VKToScan(hkl); err != nil {
			return err
		}
		f.Chars, err = Chars(hkl)
		return err
	})
	if err != nil {
		return nil, err
	}
	return f, nil
}

func badKLID(klid string) error {
	return fmt.Errorf("kblayout: bad KLID %q: want eight hex digits such as 00000409", klid)
}

// LayerDriver returns this machine's i8042prt "LayerDriver JPN" or
// "LayerDriver KOR" value for a Japanese or Korean klid (see
// Fixture.LayerDriver), or "" for other languages and bad KLIDs.
func LayerDriver(klid string) string {
	if !ValidKLID(klid) {
		return ""
	}
	id, _ := strconv.ParseUint(klid, 16, 32)
	name, ok := layerDriverValues[uint16(id)&0x3FF]
	if !ok {
		return ""
	}
	return registryString(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\i8042prt\Parameters`, name)
}

// WithLoaded loads the layout klid and calls f with its handle, on one
// locked OS thread.
//
// It loads the layout with KLF_NOTELLSHELL and never activates it, and
// unloads it afterwards unless it was loaded before, so the user's layout
// list ends as it started; WithLoaded checks that and fails otherwise.
// The list is shared by the session, so no other process may load layouts
// meanwhile. It also fails, without calling f, when Windows hands out a
// layout of another language, as it may for a KLID it does not know.
func WithLoaded(klid string, f func(hkl windows.Handle) error) (err error) {
	if !ValidKLID(klid) {
		return badKLID(klid)
	}
	klid = strings.ToUpper(klid)
	id, _ := strconv.ParseUint(klid, 16, 32)
	lang := uint16(id)

	// The layout list and the load are per thread as much as per process;
	// keep them on one OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	before, err := LayoutList()
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(klid)
	if err != nil {
		return err
	}
	hkl, err := windows.LoadKeyboardLayout(name, windows.KLF_NOTELLSHELL)
	if err != nil {
		return fmt.Errorf("kblayout: LoadKeyboardLayoutW(%s): %w", klid, err)
	}
	defer func() {
		if !slices.Contains(before, hkl) {
			if uerr := windows.UnloadKeyboardLayout(hkl); uerr != nil {
				err = errors.Join(err, fmt.Errorf("kblayout: UnloadKeyboardLayout(%s): %w", FormatHKL(hkl), uerr))
			}
		}
		after, lerr := LayoutList()
		switch {
		case lerr != nil:
			err = errors.Join(err, lerr)
		case !slices.Equal(before, after):
			err = errors.Join(err, fmt.Errorf("kblayout: the keyboard layout list changed from %s to %s", FormatList(before), FormatList(after)))
		}
	}()
	// Without KLF_SUBSTITUTE_OK Windows may still hand out another
	// layout, such as US for a KLID it does not know.
	if got := uint16(hkl); got != lang {
		return fmt.Errorf("kblayout: LoadKeyboardLayoutW(%s) gave %s, whose language 0x%04X is not the KLID's 0x%04X", klid, FormatHKL(hkl), got, lang)
	}
	return f(hkl)
}

func dumpScancodes(hkl windows.Handle) []ScanEntry {
	var out []ScanEntry
	for _, ext := range []bool{false, true} {
		for scan := uint32(0x01); scan <= 0x7F; scan++ {
			code := scan
			if ext {
				code |= 0xE000
			}
			vk := mapVirtualKey(code, mapvkVSCToVKEx, hkl)
			if vk == 0 || vk > 0xFE {
				continue
			}
			out = append(out, ScanEntry{
				Scan:       Code(scan),
				Ext:        ext,
				VK:         Code(vk),
				Base:       toUnicode(vk, scan, hkl, false, false),
				Shift:      toUnicode(vk, scan, hkl, true, false),
				AltGr:      toUnicode(vk, scan, hkl, false, true),
				ShiftAltGr: toUnicode(vk, scan, hkl, true, true),
			})
		}
	}
	return out
}

// VKToScan reads the scan code of every VK of hkl, as MapVirtualKeyExW
// (MAPVK_VK_TO_VSC_EX) gives it: the vkToScan table of a Fixture.
func VKToScan(hkl windows.Handle) ([]VKEntry, error) {
	var out []VKEntry
	for vk := uint32(0x01); vk <= 0xFE; vk++ {
		code := mapVirtualKey(vk, mapvkVKToVSCEx, hkl)
		if code == 0 {
			continue
		}
		e := VKEntry{VK: Code(vk), Scan: Code(code & 0xFF)}
		switch code >> 8 {
		case 0:
		case 0xE0:
			e.Ext = true
		case 0xE1:
			e.E1 = true
		default:
			return nil, fmt.Errorf("kblayout: MapVirtualKeyExW(vk=0x%02X) gave the unexpected scan code 0x%X", vk, code)
		}
		out = append(out, e)
	}
	return out, nil
}

// VkKeyScanExW shift state bits.
const (
	vkScanShift   = 0x01
	vkScanCtrl    = 0x02
	vkScanAlt     = 0x04
	vkScanHankaku = 0x08
)

// Chars reads the key that types each printable ASCII character on hkl,
// as VkKeyScanExW gives it, with its scan code: the chars table of a
// Fixture.
func Chars(hkl windows.Handle) ([]CharEntry, error) {
	out := make([]CharEntry, 0, 0x7F-0x20)
	for c := uint32(0x20); c <= 0x7E; c++ {
		e := CharEntry{Char: string(rune(c)), Code: Code(c)}
		res := vkKeyScanEx(uint16(c), hkl)
		if res == 0xFFFF {
			e.Absent = true
			out = append(out, e)
			continue
		}
		vk, state := uint32(res&0xFF), res>>8
		if state&^(vkScanShift|vkScanCtrl|vkScanAlt|vkScanHankaku) != 0 {
			return nil, fmt.Errorf("kblayout: VkKeyScanExW(%q) gave the unexpected shift state 0x%02X", rune(c), state)
		}
		e.VK = Code(vk)
		e.Shift = state&vkScanShift != 0
		e.Ctrl = state&vkScanCtrl != 0
		e.Alt = state&vkScanAlt != 0
		e.Hankaku = state&vkScanHankaku != 0
		code := mapVirtualKey(vk, mapvkVKToVSCEx, hkl)
		e.Scan = Code(code & 0xFF)
		switch code >> 8 {
		case 0:
		case 0xE0:
			e.Ext = true
		default:
			return nil, fmt.Errorf("kblayout: VkKeyScanExW(%q) gave vk=0x%02X with the scan code 0x%X", rune(c), vk, code)
		}
		out = append(out, e)
	}
	return out, nil
}

func mapVirtualKey(code, mapType uint32, hkl windows.Handle) uint32 {
	return mapVirtualKeyEx(code, mapType, hkl)
}

// toUnicode returns what vk types with Shift and AltGr (Ctrl+Alt) held or
// not, or nil when it types nothing.
func toUnicode(vk, scan uint32, hkl windows.Handle, shift, altGr bool) *Output {
	var state [256]byte
	if shift {
		state[vkShift], state[vkLShift] = 0x80, 0x80
	}
	if altGr {
		state[vkControl], state[vkLControl] = 0x80, 0x80
		state[vkMenu], state[vkRMenu] = 0x80, 0x80
	}
	var buf [16]uint16
	n := windows.ToUnicodeEx(vk, scan, &state[0], &buf[0], int32(len(buf)), toUnicodeNoStateChange, hkl)
	switch {
	case n < 0:
		// A dead key: buf holds its spacing form.
		return &Output{Text: windows.UTF16ToString(buf[:]), Dead: true}
	case n == 0:
		return nil
	}
	return &Output{Text: string(utf16.Decode(buf[:min(int(n), len(buf))]))}
}

// layoutNames reads the "Layout File" and "Layout Text" of klid.
func layoutNames(klid string) (file, text string, err error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, layoutsKey+klid, registry.QUERY_VALUE)
	if err != nil {
		return "", "", fmt.Errorf("kblayout: keyboard layout %s is not installed: %w", klid, err)
	}
	defer k.Close()
	file, _, err = k.GetStringValue("Layout File")
	if err != nil {
		return "", "", fmt.Errorf("kblayout: %s: reading Layout File: %w", klid, err)
	}
	text, _, _ = k.GetStringValue("Layout Text")
	return file, text, nil
}

// registryString reads a string value, or returns "" when it is missing.
func registryString(root registry.Key, path, name string) string {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if err != nil {
		return ""
	}
	return v
}

func windowsVersion() string {
	v := windows.RtlGetVersion()
	return fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}

// FormatList formats a keyboard layout list, such as GetKeyboardLayoutList
// returns it, as "[0x04090409 0x04110411]".
func FormatList(list []windows.Handle) string {
	s := make([]string, len(list))
	for i, h := range list {
		s[i] = FormatHKL(h)
	}
	return "[" + strings.Join(s, " ") + "]"
}
