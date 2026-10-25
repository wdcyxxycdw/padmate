package output

import (
	"math"
	"strconv"
	"testing"
	"unsafe"
)

func TestExtraInfo(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) < 8 {
		t.Skip("the tag needs a 64-bit dwExtraInfo")
	}
	for _, nonce := range []uint32{0, 1, 0x1234, 0xEEEE0000, math.MaxUint32} {
		extra := ExtraInfo(nonce)
		if want := uintptr(uint64(0x7061646D)<<32 | uint64(nonce)); extra != want {
			t.Errorf("ExtraInfo(%#x) = %#x, want %#x", nonce, extra, want)
		}
		if !IsPadmate(extra) {
			t.Errorf("IsPadmate(ExtraInfo(%#x)) = false", nonce)
		}
		if got := Nonce(extra); got != nonce {
			t.Errorf("Nonce(ExtraInfo(%#x)) = %#x", nonce, got)
		}
	}
	if s := string([]byte{byte(Tag >> 56 & 0xFF), byte(Tag >> 48 & 0xFF), byte(Tag >> 40 & 0xFF), byte(Tag >> 32 & 0xFF)}); s != "padm" {
		t.Errorf("Tag spells %q, want %q", s, "padm")
	}
}

func TestIsPadmate(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) < 8 {
		t.Skip("the tag needs a 64-bit dwExtraInfo")
	}
	for _, extra := range []uint64{0, 1, 0x7061646D, 0xFF515151, 0x7061646C_00000000, 0x7161646D_00000001, 0xFFFFFFFF_FFFFFFFF} {
		if IsPadmate(uintptr(extra)) {
			t.Errorf("IsPadmate(%s) = true", strconv.FormatUint(extra, 16))
		}
	}
}

// A mouse hook delivers the low 32 bits of dwExtraInfo only (measured
// 2026-10-19): the high half is 0, or the whole tag if some Windows keeps it.
func TestMayBePadmateMouse(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) < 8 {
		t.Skip("the tag needs a 64-bit dwExtraInfo")
	}
	for _, tt := range []struct {
		extra uint64
		want  bool
	}{
		{0x75190007, true},           // what Windows delivers for ExtraInfo(0x75190007)
		{0, true},                    // the nonce is checked by the caller
		{uint64(ExtraInfo(1)), true}, // the full tag
		{uint64(ExtraInfo(0)), true}, // the full tag, nonce 0
		{0x7061646C_00000001, false}, // another tag
		{0x7161646D_00000001, false}, // another tag
		{0x00000001_00000001, false}, // some other high half
		{0xFFFFFFFF_FFFFFFFF, false}, // all bits
		{0xFF515700, true},           // a pen or touch signature: the nonce check is what rejects it
	} {
		if got := MayBePadmateMouse(uintptr(tt.extra)); got != tt.want {
			t.Errorf("MayBePadmateMouse(%#x) = %v, want %v", tt.extra, got, tt.want)
		}
	}
	// The nonce of what a hook delivers is the nonce that was sent, with the
	// tag or without.
	for _, nonce := range []uint32{0, 7, 0x75190007, math.MaxUint32} {
		sent := ExtraInfo(nonce)
		for _, delivered := range []uintptr{sent, uintptr(uint32(sent))} {
			if !MayBePadmateMouse(delivered) || Nonce(delivered) != nonce {
				t.Errorf("delivered %#x of ExtraInfo(%#x): MayBePadmateMouse %v, Nonce %#x",
					delivered, nonce, MayBePadmateMouse(delivered), Nonce(delivered))
			}
		}
	}
}
