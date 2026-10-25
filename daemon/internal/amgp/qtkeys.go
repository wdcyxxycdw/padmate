package amgp

import "fmt"

// qtKey is what a keyboard slot's code means in padmate. AntiMicroX stores
// the Qt::Key of the key from configversion 6 on, with 0x10000000 added for
// the right-hand variant of a modifier and for a keypad key that a Qt key
// also has, and 0x20000000 added to an X11 keysym for a keypad key.
type qtKey struct {
	// mods are modifiers the key stands for besides its own key: "!" is
	// Shift and 1.
	mods []string
	// name is the padmate key.
	name string
	// modifier is true when the key is a modifier.
	modifier bool
	// approx says how the translation differs, when it does.
	approx string
	// drop says why the code has no padmate key; the other fields are
	// then empty.
	drop string
}

const (
	qtCustomPrefix = 0x10000000
	qtKeysymPrefix = 0x20000000
	qtNativePrefix = 0x60000000
)

// qtSpecial maps the Qt keys at 0x01000000 and above, and the keypad codes,
// to padmate keys, as AntiMicroX's Windows key mapper does.
var qtSpecial = map[int]qtKey{
	0x01000000: {name: "Esc"},
	0x01000001: {name: "Tab"},
	0x01000002: {mods: []string{"Shift"}, name: "Tab"}, // Backtab
	0x01000003: {name: "Backspace"},
	0x01000004: {name: "Enter"}, // Return
	0x01000005: {name: "Enter"}, // Enter, which AntiMicroX sends as Return too
	0x01000006: {name: "Insert"},
	0x01000007: {name: "Delete"},
	0x01000008: {name: "Pause"},
	0x01000009: {name: "PrintScreen"},
	0x01000010: {name: "Home"},
	0x01000011: {name: "End"},
	0x01000012: {name: "Left"},
	0x01000013: {name: "Up"},
	0x01000014: {name: "Right"},
	0x01000015: {name: "Down"},
	0x01000016: {name: "PageUp"},
	0x01000017: {name: "PageDown"},
	0x01000020: {name: "LShift", modifier: true},
	0x01000021: {name: "LCtrl", modifier: true},
	0x01000022: {name: "LWin", modifier: true}, // Meta
	0x01000023: {name: "LAlt", modifier: true},
	0x01000024: {name: "CapsLock"},
	0x01000025: {name: "NumLock"},
	0x01000026: {name: "ScrollLock"},
	0x01000053: {name: "LWin", modifier: true}, // Super_L
	0x01000054: {name: "RWin", modifier: true}, // Super_R
	0x01000055: {name: "Menu"},
	0x01000070: {name: "VolumeDown"},
	0x01000071: {name: "VolumeMute"},
	0x01000072: {name: "VolumeUp"},
	0x01000080: {name: "MediaPlayPause"}, // Key_MediaPlay, which AntiMicroX sends as play/pause
	0x01000081: {name: "MediaStop"},
	0x01000082: {name: "MediaPrevious"},
	0x01000083: {name: "MediaNext"},
	0x01000086: {name: "MediaPlayPause"}, // Key_MediaTogglePlayPause

	// The right-hand modifiers.
	qtCustomPrefix | 0x01000020: {name: "RShift", modifier: true},
	qtCustomPrefix | 0x01000021: {name: "RCtrl", modifier: true},
	qtCustomPrefix | 0x01000022: {name: "RWin", modifier: true},
	qtCustomPrefix | 0x01000023: {name: "RAlt", modifier: true},

	// The keypad, as AntiMicroX writes it on Windows: operators as the
	// character's Qt key with the custom prefix, digits as X11 keypad
	// keysyms.
	qtCustomPrefix | 0x2f:       {name: "NumDivide"},
	qtCustomPrefix | 0x2a:       {name: "NumMultiply"},
	qtCustomPrefix | 0x2d:       {name: "NumSubtract"},
	qtCustomPrefix | 0x2b:       {name: "NumAdd"},
	qtCustomPrefix | 0x2e:       {name: "NumDecimal"},
	qtKeysymPrefix | 0xff8d:     {name: "NumEnter"},
	qtKeysymPrefix | 0xff9f:     {name: "NumDecimal"},
	qtCustomPrefix | 0x01000006: {name: "Num0", approx: "小键盘的 Insert（NumLock 关时的 0）按 Num0 导入，要 NumLock 开着才对应"},
	qtCustomPrefix | 0x01000007: {name: "NumDecimal", approx: "小键盘的 Delete（NumLock 关时的小数点）按 NumDecimal 导入，要 NumLock 开着才对应"},
	qtCustomPrefix | 0x01000010: {name: "Num7", approx: "小键盘的 Home（NumLock 关时的 7）按 Num7 导入，要 NumLock 开着才对应"},
	qtCustomPrefix | 0x01000011: {name: "Num1", approx: "小键盘的 End（NumLock 关时的 1）按 Num1 导入，要 NumLock 开着才对应"},
	qtCustomPrefix | 0x01000012: {name: "Num4", approx: "小键盘的 Left（NumLock 关时的 4）按 Num4 导入，要 NumLock 开着才对应"},
	qtCustomPrefix | 0x01000013: {name: "Num8", approx: "小键盘的 Up（NumLock 关时的 8）按 Num8 导入，要 NumLock 开着才对应"},
	qtCustomPrefix | 0x01000014: {name: "Num6", approx: "小键盘的 Right（NumLock 关时的 6）按 Num6 导入，要 NumLock 开着才对应"},
	qtCustomPrefix | 0x01000015: {name: "Num2", approx: "小键盘的 Down（NumLock 关时的 2）按 Num2 导入，要 NumLock 开着才对应"},
	qtCustomPrefix | 0x01000016: {name: "Num9", approx: "小键盘的 PageUp（NumLock 关时的 9）按 Num9 导入，要 NumLock 开着才对应"},
	qtCustomPrefix | 0x01000017: {name: "Num3", approx: "小键盘的 PageDown（NumLock 关时的 3）按 Num3 导入，要 NumLock 开着才对应"},
	qtCustomPrefix | 0x0100000b: {name: "Num5", approx: "小键盘的 5（NumLock 关）按 Num5 导入，要 NumLock 开着才对应"},
}

// qtDropped names the keys that have a Qt code but no padmate key.
var qtDropped = map[int]string{
	0x0100000a: "SysReq",
	0x0100000b: "Clear",
	0x01000058: "Help",
	0x01001103: "AltGr",
	0x01000061: "浏览器后退",
	0x01000062: "浏览器前进",
	0x01000063: "浏览器停止",
	0x01000064: "浏览器刷新",
	0x01000090: "浏览器主页",
	0x01000091: "浏览器收藏夹",
	0x01000092: "浏览器搜索",
	0x010000a0: "启动邮件",
	0x010000a1: "启动媒体播放器",
	0x010000a2: "启动应用 1",
	0x010000a3: "启动应用 2",
	0x01000085: "MediaPause",
	0x01020002: "Printer",
	0x01020005: "Play",
}

// shifted maps the ASCII characters that need Shift on a US keyboard to the
// key that carries them.
var shifted = map[int]string{
	'!': "1", '"': "'", '#': "3", '$': "4", '%': "5", '&': "7", '(': "9", ')': "0", '*': "8",
	':': ";", '<': ",", '>': ".", '?': "/", '@': "2", '^': "6", '_': "-",
	'{': "[", '|': "\\", '}': "]", '~': "`",
}

// qtKeyFor translates the code of a keyboard slot.
func qtKeyFor(code int) qtKey {
	switch {
	case code >= 'A' && code <= 'Z', code >= '0' && code <= '9':
		return qtKey{name: string(rune(code))}
	case code == ' ':
		return qtKey{name: "Space"}
	case code == '+':
		return qtKey{name: "Plus"}
	}
	switch code {
	case ',', '-', '.', '/', ';', '=', '[', '\\', ']', '\'', '`':
		return qtKey{name: string(rune(code))}
	}
	if base, ok := shifted[code]; ok {
		return qtKey{mods: []string{"Shift"}, name: base,
			approx: fmt.Sprintf("“%c”按美式键盘写成 Shift+%s", rune(code), base)}
	}
	if code >= 0x01000030 && code <= 0x01000047 {
		return qtKey{name: fmt.Sprintf("F%d", code-0x01000030+1)}
	}
	if code >= 0x01000048 && code <= 0x01000052 {
		return qtKey{drop: fmt.Sprintf("F%d：padmate 只有 F1 至 F24", code-0x01000030+1)}
	}
	if code >= qtKeysymPrefix|0xffb0 && code <= qtKeysymPrefix|0xffb9 {
		return qtKey{name: fmt.Sprintf("Num%d", code-(qtKeysymPrefix|0xffb0))}
	}
	if k, ok := qtSpecial[code]; ok {
		return k
	}
	if why, ok := qtDropped[code]; ok {
		return qtKey{drop: why + "：padmate 没有这个键"}
	}
	if code >= qtNativePrefix {
		return qtKey{drop: fmt.Sprintf("原生键码 0x%x：不知道它在哪个系统上写的", code)}
	}
	return qtKey{drop: fmt.Sprintf("键码 0x%x 不是 padmate 认识的键", code)}
}

// mouseButtonNames maps the codes of a mousebutton slot to KeySpec mouse
// keys. The order is AntiMicroX's, not Windows': 2 is the middle button and 3
// the right one.
var mouseButtonNames = map[int]string{
	1: "MouseLeft", 2: "MouseMiddle", 3: "MouseRight",
	4: "WheelUp", 5: "WheelDown", 6: "WheelLeft", 7: "WheelRight",
	8: "MouseX1", 9: "MouseX2",
}

// isWheel reports whether the mousebutton code is a wheel notch, and
// whether the notch is horizontal.
func isWheel(code int) (wheel, horizontal bool) {
	switch code {
	case 4, 5:
		return true, false
	case 6, 7:
		return true, true
	}
	return false, false
}
