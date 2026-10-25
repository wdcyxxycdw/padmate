package output

// Every event padmate injects carries dwExtraInfo = Tag | nonce: the high
// 32 bits hold Tag and the low 32 bits a nonce chosen by the injector (the
// process id for padmatectl). A low-level hook can then tell padmate's own
// events apart from the physical keyboard and from other injectors, and
// tell padmate processes apart. Experiment (M0b, Windows 11 x64): a keyboard
// hook receives all 64 bits of dwExtraInfo unchanged. A mouse hook does not
// (measured 2026-10-19, Windows 11 x64): MSLLHOOKSTRUCT.dwExtraInfo of an
// injected mouse event keeps only the low 32 bits, the nonce, so IsPadmate is
// false for every injected mouse event in a hook; see MayBePadmateMouse.

// Tag marks padmate events: ASCII "padm" in the high 32 bits.
const Tag uint64 = 0x7061646D << 32

// tagMask selects the bits of dwExtraInfo that hold Tag.
const tagMask uint64 = 0xFFFFFFFF << 32

// ExtraInfo returns the dwExtraInfo value of an event injected with nonce.
func ExtraInfo(nonce uint32) uintptr {
	return uintptr(Tag | uint64(nonce))
}

// IsPadmate reports whether extra, a dwExtraInfo value, was made by
// ExtraInfo.
func IsPadmate(extra uintptr) bool {
	return uint64(extra)&tagMask == Tag
}

// Nonce returns the nonce of a dwExtraInfo value made by ExtraInfo.
func Nonce(extra uintptr) uint32 {
	return uint32(extra)
}

// MayBePadmateMouse reports whether extra, the dwExtraInfo of an injected mouse
// event as a low-level mouse hook delivers it, can have been made by
// ExtraInfo: its high 32 bits are 0, which is what Windows delivers (it drops
// them), or Tag, in case some Windows version keeps them. It is not proof: a
// zero high half is also any other injector's 32-bit value, so a mouse hook
// takes an event for padmate's only when the nonce (Nonce) is one it owns as
// well, and the nonces a test owns must stay clear of the values other
// injectors use (injecttest.NonceBase). padmated itself never hooks mouse
// events and still sends ExtraInfo(nonce).
func MayBePadmateMouse(extra uintptr) bool {
	high := uint64(extra) & tagMask
	return high == 0 || high == Tag
}
