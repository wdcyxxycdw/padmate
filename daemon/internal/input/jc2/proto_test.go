package jc2

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

// hexBytes decodes text such as "0c 91 01".
func hexBytes(t testing.TB, s string) []byte {
	t.Helper()
	var out []byte
	for _, f := range bytes.Fields([]byte(s)) {
		var v byte
		if _, err := fmt.Sscanf(string(f), "%02x", &v); err != nil {
			t.Fatalf("bad hex %q in %q", f, s)
		}
		out = append(out, v)
	}
	return out
}

// raw builds a request with a correct header and length byte.
func raw(id, sub byte, data ...byte) []byte {
	return Request{ID: id, Sub: sub, Data: data}.Bytes()
}

// allRequests lists every request the constructors can make.
func allRequests() []Request {
	reqs := []Request{FirmwareInfo(), SetFeatureMask(), EnableFeatures()}
	for _, fr := range flashRanges {
		reqs = append(reqs, ReadFlash(fr))
	}
	for n := range 8 {
		reqs = append(reqs, PlayerLEDs(n))
	}
	return reqs
}

func TestConstructorsPassCheckRequest(t *testing.T) {
	for _, r := range allRequests() {
		t.Run(r.Note, func(t *testing.T) {
			if err := CheckRequest(r.Bytes()); err != nil {
				t.Errorf("CheckRequest(% x) = %v", r.Bytes(), err)
			}
			if r.Note == "" {
				t.Error("no Note")
			}
		})
	}
	// PlayerLEDs masks n, so no number gets out of the table.
	for _, n := range []int{-1, 8, 100, -100} {
		if err := CheckRequest(PlayerLEDs(n).Bytes()); err != nil {
			t.Errorf("PlayerLEDs(%d): %v", n, err)
		}
	}
}

func TestKnownBytes(t *testing.T) {
	tests := []struct {
		name string
		req  Request
		want string
	}{
		{"feature mask", SetFeatureMask(), "0c 91 01 02 00 04 00 00 03 00 00 00"},
		{"enable features", EnableFeatures(), "0c 91 01 04 00 04 00 00 03 00 00 00"},
		{"flash info", ReadFlash(FlashInfo), "02 91 01 04 00 08 00 00 40 7e 00 00 00 30 01 00"},
		{"flash factory stick", ReadFlash(FlashFactoryStick), "02 91 01 04 00 08 00 00 40 7e 00 00 80 30 01 00"},
		{"flash user stick", ReadFlash(FlashUserStick), "02 91 01 04 00 08 00 00 40 7e 00 00 40 c0 1f 00"},
		{"firmware", FirmwareInfo(), "10 91 01 01 00 00 00 00"},
		{"player 0", PlayerLEDs(0), "09 91 01 07 00 08 00 00 01 00 00 00 00 00 00 00"},
		{"player 1", PlayerLEDs(1), "09 91 01 07 00 08 00 00 03 00 00 00 00 00 00 00"},
		{"player 3", PlayerLEDs(3), "09 91 01 07 00 08 00 00 0f 00 00 00 00 00 00 00"},
		{"player 7", PlayerLEDs(7), "09 91 01 07 00 08 00 00 06 00 00 00 00 00 00 00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.req.Bytes(); !bytes.Equal(got, hexBytes(t, tt.want)) {
				t.Errorf("got % x\nwant %s", got, tt.want)
			}
		})
	}
}

func TestCheckRequestRefuses(t *testing.T) {
	flash := func(addr uint32, n byte) []byte {
		return raw(0x02, 0x04, n, 0x7e, 0, 0, byte(addr), byte(addr>>8), byte(addr>>16), byte(addr>>24))
	}
	good := PlayerLEDs(2).Bytes()
	mutate := func(b []byte, f func([]byte) []byte) []byte { return f(append([]byte(nil), b...)) }
	zeros := func(n int) []byte { return make([]byte, n) }

	tests := []struct {
		name string
		b    []byte
	}{
		// Nintendo pairing, every subcommand, with and without data.
		{"pair 0x15/1", raw(0x15, 0x01)},
		{"pair 0x15/2", raw(0x15, 0x02)},
		{"pair 0x15/3", raw(0x15, 0x03)},
		{"pair 0x15/4", raw(0x15, 0x04)},
		{"pair 0x15/1 data", raw(0x15, 0x01, 1, 0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)},
		{"pair 0x15/2 data", raw(0x15, 0x02, zeros(16)...)},
		{"pair 0x15/3 data", raw(0x15, 0x03, 0x01)},
		{"pair 0x15/4 data", raw(0x15, 0x04, 0x01)},
		// Pairing info.
		{"0x03/0x01", raw(0x03, 0x01, zeros(8)...)},
		{"0x03/0x07", raw(0x03, 0x07)},
		{"0x03/0x08", raw(0x03, 0x08)},
		{"0x03/0x09", raw(0x03, 0x09)},
		{"0x03/0x0a", raw(0x03, 0x0a)},
		{"0x03/0x0d", raw(0x03, 0x0d)},
		// Flash write and erase and the other flash subcommands.
		{"flash 0x02/0x01", raw(0x02, 0x01, zeros(8)...)},
		{"flash 0x02/0x02", raw(0x02, 0x02, zeros(8)...)},
		{"flash 0x02/0x03", raw(0x02, 0x03, zeros(8)...)},
		{"flash 0x02/0x05", raw(0x02, 0x05, zeros(8)...)},
		{"flash write 0x02/0x03 with data", raw(0x02, 0x03, 0x40, 0x7e, 0, 0, 0x00, 0x30, 0x01, 0x00)},
		// The pairing area, which holds the console's link keys.
		{"pairing 0x1fa000", flash(0x1FA000, 0x40)},
		{"pairing 0x1fa008", flash(0x1FA008, 0x40)},
		{"pairing 0x1fa01a", flash(0x1FA01A, 0x40)},
		{"pairing 0x1fa042", flash(0x1FA042, 0x40)},
		{"pairing 0x1fa000 short", flash(0x1FA000, 0x10)},
		// Allowed addresses with another length, shifted, or a wrong magic.
		{"info longer", flash(0x13000, 0x41)},
		{"info much longer", flash(0x13000, 0xff)},
		{"info shorter", flash(0x13000, 0x3f)},
		{"info zero length", flash(0x13000, 0)},
		{"factory longer", flash(0x13080, 0x80)},
		{"user longer", flash(0x1FC040, 0x41)},
		{"info shifted", flash(0x13001, 0x40)},
		{"info other range", flash(0x13040, 0x40)},
		{"read with wrong 0x7e", raw(0x02, 0x04, 0x40, 0x7f, 0, 0, 0x00, 0x30, 0x01, 0x00)},
		{"read with junk byte 2", raw(0x02, 0x04, 0x40, 0x7e, 1, 0, 0x00, 0x30, 0x01, 0x00)},
		{"read with junk byte 3", raw(0x02, 0x04, 0x40, 0x7e, 0, 1, 0x00, 0x30, 0x01, 0x00)},
		{"read with trailing junk", raw(0x02, 0x04, 0x40, 0x7e, 0, 0, 0x00, 0x30, 0x01, 0x00, 0)},
		{"read without address", raw(0x02, 0x04, 0x40, 0x7e, 0, 0)},
		{"read without data", raw(0x02, 0x04)},
		// Firmware update, shutdown, reboot, and other commands.
		{"update 0x0d/0", raw(0x0d, 0x00)},
		{"update 0x0d/1", raw(0x0d, 0x01)},
		{"update 0x0d/2", raw(0x0d, 0x02, zeros(16)...)},
		{"update 0x0d/0xff", raw(0x0d, 0xff)},
		{"power 0x06/0x02", raw(0x06, 0x02)},
		{"power 0x06/0x03", raw(0x06, 0x03)},
		{"power 0x06/0x01", raw(0x06, 0x01)},
		{"0x07/0x01", raw(0x07, 0x01)},
		{"0x11/0x01", raw(0x11, 0x01)},
		{"0x11/0x03", raw(0x11, 0x03)},
		{"0x16/0x01", raw(0x16, 0x01)},
		{"vibration 0x0a/0x02", raw(0x0a, 0x02, zeros(32)...)},
		{"vibration 0x0a/0x08", raw(0x0a, 0x08, zeros(32)...)},
		{"0x08/0x02", raw(0x08, 0x02, zeros(8)...)},
		// Feature masks other than 0x03, and the other 0x0c subcommands.
		{"mask 0x07", raw(0x0c, 0x02, 0x07, 0, 0, 0)},
		{"mask 0x37", raw(0x0c, 0x02, 0x37, 0, 0, 0)},
		{"mask 0xff", raw(0x0c, 0x02, 0xff, 0, 0, 0)},
		{"mask 0x01", raw(0x0c, 0x02, 0x01, 0, 0, 0)},
		{"mask 0", raw(0x0c, 0x02, 0, 0, 0, 0)},
		{"enable 0xff", raw(0x0c, 0x04, 0xff, 0, 0, 0)},
		{"enable 0x07", raw(0x0c, 0x04, 0x07, 0, 0, 0)},
		{"mask junk", raw(0x0c, 0x02, 0x03, 0, 0, 1)},
		{"mask short", raw(0x0c, 0x02, 0x03)},
		{"mask long", raw(0x0c, 0x02, 0x03, 0, 0, 0, 0)},
		{"mask no data", raw(0x0c, 0x02)},
		{"0x0c/0x05", raw(0x0c, 0x05, 0x03, 0, 0, 0)},
		{"0x0c/0x06", raw(0x0c, 0x06, 0x03, 0, 0, 0)},
		{"0x0c/0x01", raw(0x0c, 0x01, 0x03, 0, 0, 0)},
		{"0x0c/0x03", raw(0x0c, 0x03, 0x03, 0, 0, 0)},
		// Lights.
		{"led trailing junk", raw(0x09, 0x07, 0x01, 0, 0, 0, 0, 0, 0, 0, 0)},
		{"led junk inside", raw(0x09, 0x07, 0x01, 0, 0, 0, 0, 0, 0, 1)},
		{"led unknown pattern", raw(0x09, 0x07, 0x02, 0, 0, 0, 0, 0, 0, 0)},
		{"led all on", raw(0x09, 0x07, 0xff, 0, 0, 0, 0, 0, 0, 0)},
		{"led off", raw(0x09, 0x07, 0x00, 0, 0, 0, 0, 0, 0, 0)},
		{"led short", raw(0x09, 0x07, 0x01)},
		{"led no data", raw(0x09, 0x07)},
		{"led other sub", raw(0x09, 0x08, 0x01, 0, 0, 0, 0, 0, 0, 0)},
		// Firmware info with data.
		{"firmware with data", raw(0x10, 0x01, 0)},
		{"firmware other sub", raw(0x10, 0x02)},
		// Header faults on a request that is otherwise good.
		{"USB transport", mutate(good, func(b []byte) []byte { b[2] = 0x00; return b })},
		{"transport 2", mutate(good, func(b []byte) []byte { b[2] = 0x02; return b })},
		{"reply direction", mutate(good, func(b []byte) []byte { b[1] = 0x01; return b })},
		{"direction 0", mutate(good, func(b []byte) []byte { b[1] = 0x00; return b })},
		{"header byte 4", mutate(good, func(b []byte) []byte { b[4] = 0x01; return b })},
		{"header byte 6", mutate(good, func(b []byte) []byte { b[6] = 0x01; return b })},
		{"header byte 7", mutate(good, func(b []byte) []byte { b[7] = 0x01; return b })},
		{"length byte too big", mutate(good, func(b []byte) []byte { b[5] = 9; return b })},
		{"length byte too small", mutate(good, func(b []byte) []byte { b[5] = 7; return b })},
		{"length byte 0", mutate(good, func(b []byte) []byte { b[5] = 0; return b })},
		{"data cut short", good[:len(good)-1]},
		// Not requests at all.
		{"nil", nil},
		{"empty", []byte{}},
		{"1 byte", []byte{0x10}},
		{"7 bytes", hexBytes(t, "10 91 01 01 00 00 00")},
		{"raw payload", []byte{0x03, 0, 0, 0}},
		{"raw flash payload", hexBytes(t, "40 7e 00 00 00 30 01 00")},
		{"raw led payload", hexBytes(t, "01 00 00 00 00 00 00 00")},
		{"zeros", zeros(16)},
		{"ff", bytes.Repeat([]byte{0xff}, 16)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckRequest(tt.b)
			if err == nil {
				t.Fatalf("CheckRequest(% x) = nil, want a refusal", tt.b)
			}
			if !errors.Is(err, ErrNotAllowed) {
				t.Errorf("error %v does not wrap ErrNotAllowed", err)
			}
		})
	}
}

// TestCheckRequestEmptyData: of every command id and subcommand without
// data, only the firmware info passes.
func TestCheckRequestEmptyData(t *testing.T) {
	for id := range 256 {
		for sub := range 256 {
			err := CheckRequest(raw(byte(id), byte(sub)))
			if want := id == 0x10 && sub == 0x01; (err == nil) != want {
				t.Errorf("command %#02x/%#02x: err = %v, want pass %v", id, sub, err, want)
			}
		}
	}
}

// TestCheckRequestAddresses: of the flash reads around the three ranges and
// the pairing area, only the three exact ranges pass.
func TestCheckRequestAddresses(t *testing.T) {
	ok := map[FlashRange]bool{}
	for _, fr := range flashRanges {
		ok[fr] = true
	}
	for _, base := range []uint32{0, 0x13000, 0x13080, 0x1FA000, 0x1FC040} {
		for d := uint32(0); d < 0x120; d++ {
			for _, n := range []byte{0, 1, 8, 0x1a, 0x40, 0x41, 0x80, 0xff} {
				addr := base + d
				b := raw(0x02, 0x04, n, 0x7e, 0, 0, byte(addr), byte(addr>>8), byte(addr>>16), byte(addr>>24))
				err := CheckRequest(b)
				if want := ok[FlashRange{addr, n}]; (err == nil) != want {
					t.Fatalf("read %#x+%#x: err = %v, want pass %v", addr, n, err, want)
				}
			}
		}
	}
}

func TestSides(t *testing.T) {
	if SideOf(ProductL) != SideL || SideOf(ProductR) != SideR || SideOf(0x2069) != 0 || SideOf(0) != 0 {
		t.Error("SideOf")
	}
	if SideL.SideInputUUID() != UUIDSideInputL || SideR.SideInputUUID() != UUIDSideInputR {
		t.Error("SideInputUUID")
	}
	for _, s := range []Side{SideL, SideR} {
		for _, u := range []string{UUIDService, UUIDSideInputL, UUIDSideInputR, UUIDCommand, UUIDCommandResp} {
			want := u == UUIDCommandResp || u == s.SideInputUUID()
			if got := MayNotify(s, u); got != want {
				t.Errorf("MayNotify(%v, %s) = %v, want %v", s, u, got, want)
			}
		}
	}
	if MayNotify(0, UUIDCommand) {
		t.Error("MayNotify of the command characteristic")
	}
	if SideL.String() != "L" || SideR.String() != "R" || Side(0).String() != "?" {
		t.Error("Side.String")
	}
}

// allowedBytes is the set of every request the constructors make.
func allowedBytes() map[string]bool {
	set := map[string]bool{}
	for _, r := range allRequests() {
		set[string(r.Bytes())] = true
	}
	return set
}

func FuzzCheckRequest(f *testing.F) {
	for _, r := range allRequests() {
		f.Add(r.Bytes())
	}
	f.Add([]byte{})
	f.Add(raw(0x15, 0x01))
	f.Add(raw(0x02, 0x04, 0x40, 0x7e, 0, 0, 0x00, 0xa0, 0x1f, 0))
	allowed := allowedBytes()
	f.Fuzz(func(t *testing.T, b []byte) {
		err := CheckRequest(b)
		if err != nil {
			if !errors.Is(err, ErrNotAllowed) {
				t.Fatalf("error %v does not wrap ErrNotAllowed", err)
			}
			return
		}
		if !allowed[string(b)] {
			t.Fatalf("CheckRequest accepted % x, which no constructor makes", b)
		}
	})
}
