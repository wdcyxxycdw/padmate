package jc2

import (
	"bytes"
	"testing"
)

// flashReply builds a successful reply to a flash read: the header, then
// length, 3 zeros, the address and the bytes.
func flashReply(addr uint32, n byte, fill byte) []byte {
	b := []byte{0x02, 0x01, 0x01, 0x04, 0x10, 0x78, 0x00, 0x00}
	b = append(b, n, 0, 0, 0, byte(addr), byte(addr>>8), byte(addr>>16), byte(addr>>24))
	return append(b, bytes.Repeat([]byte{fill}, int(n))...)
}

// fwReply is a firmware info reply: version 2.1.4 of a left Joy-Con.
var fwReply = []byte{0x10, 0x01, 0x01, 0x01, 0x10, 0x78, 0x00, 0x00, 0x02, 0x01, 0x04, 0x00}

// featReply is the reply to the feature mask command.
var featReply = []byte{0x0c, 0x01, 0x01, 0x02, 0x10, 0x78, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

// mustReply parses b or fails the test.
func mustReply(t testing.TB, b []byte) Reply {
	t.Helper()
	r, err := ParseReply(b)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestParseReply(t *testing.T) {
	r := mustReply(t, fwReply)
	if r.ID != 0x10 || r.Sub != 0x01 || r.Status != [2]byte{0x10, 0x78} || !r.OK() || !bytes.Equal(r.Data, fwReply[8:]) {
		t.Errorf("ParseReply = %+v", r)
	}

	bad := []struct {
		name string
		b    []byte
	}{
		{"nil", nil},
		{"7 bytes", fwReply[:7]},
		{"direction of a request", []byte{0x10, 0x91, 0x01, 0x01, 0x10, 0x78, 0x00, 0x00}},
		{"USB transport", []byte{0x10, 0x01, 0x00, 0x01, 0x10, 0x78, 0x00, 0x00}},
		{"byte 6", []byte{0x10, 0x01, 0x01, 0x01, 0x10, 0x78, 0x01, 0x00}},
		{"byte 7", []byte{0x10, 0x01, 0x01, 0x01, 0x10, 0x78, 0x00, 0x01}},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			if r, err := ParseReply(tt.b); err == nil {
				t.Errorf("ParseReply(% x) = %+v, want an error", tt.b, r)
			}
		})
	}

	// A header alone is a reply without data; a bad status is still decoded.
	r, err := ParseReply([]byte{0x0c, 0x01, 0x01, 0x02, 0x00, 0x00, 0x00, 0x00})
	if err != nil || r.OK() || len(r.Data) != 0 {
		t.Errorf("failed status: %+v, %v", r, err)
	}
}

func TestReplyAnswers(t *testing.T) {
	fw := mustReply(t, fwReply)
	feat := mustReply(t, featReply)
	info := mustReply(t, flashReply(FlashInfo.Addr, FlashInfo.Len, 0))
	stick := mustReply(t, flashReply(FlashFactoryStick.Addr, FlashFactoryStick.Len, 0))
	tests := []struct {
		name string
		r    Reply
		q    Request
		want bool
	}{
		{"firmware", fw, FirmwareInfo(), true},
		{"firmware for a mask", fw, SetFeatureMask(), false},
		{"mask", feat, SetFeatureMask(), true},
		{"mask for enable", feat, EnableFeatures(), false},
		{"info", info, ReadFlash(FlashInfo), true},
		{"info for another range", info, ReadFlash(FlashFactoryStick), false},
		{"stick", stick, ReadFlash(FlashFactoryStick), true},
		{"stick for info", stick, ReadFlash(FlashInfo), false},
		{"flash reply without data", Reply{ID: 0x02, Sub: 0x04}, ReadFlash(FlashInfo), false},
		{"flash reply for a request without data", info, Request{ID: 0x02, Sub: 0x04}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.r.Answers(tt.q); got != tt.want {
				t.Errorf("Answers = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFlashData(t *testing.T) {
	good := flashReply(FlashInfo.Addr, FlashInfo.Len, 0xa5)
	with := func(f func([]byte)) []byte {
		b := append([]byte(nil), good...)
		f(b)
		return b
	}
	tests := []struct {
		name string
		r    Reply
		ok   bool
	}{
		{"good", mustReply(t, good), true},
		{"other address", mustReply(t, flashReply(FlashFactoryStick.Addr, FlashInfo.Len, 0xa5)), false},
		{"other length", mustReply(t, flashReply(FlashInfo.Addr, 0x20, 0xa5)), false},
		{"one byte short", mustReply(t, good[:len(good)-1]), false},
		{"one byte long", mustReply(t, append(append([]byte(nil), good...), 0)), false},
		{"length byte lies", mustReply(t, with(func(b []byte) { b[8] = 0x3f })), false},
		{"failed status", mustReply(t, with(func(b []byte) { b[4], b[5] = 0, 0 })), false},
		{"not a flash reply", mustReply(t, fwReply), false},
		{"no data", Reply{ID: 0x02, Sub: 0x04, Status: statusOK}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FlashData(tt.r, FlashInfo)
			if (err == nil) != tt.ok {
				t.Fatalf("FlashData err = %v, want ok %v", err, tt.ok)
			}
			if tt.ok && (len(got) != 0x40 || got[0] != 0xa5 || got[0x3f] != 0xa5) {
				t.Errorf("FlashData = % x", got)
			}
		})
	}
}

func TestParseFirmware(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want Firmware
		ok   bool
	}{
		{"left 2.1.4", []byte{2, 1, 4, 0, 9, 9}, Firmware{2, 1, 4, SideL}, true},
		{"right 2.1.4", []byte{2, 1, 4, 1}, Firmware{2, 1, 4, SideR}, true},
		{"another type", []byte{2, 1, 4, 2}, Firmware{}, false},
		{"short", []byte{2, 1, 4}, Firmware{}, false},
		{"empty", nil, Firmware{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseFirmware(tt.data)
			if (err == nil) != tt.ok || got != tt.want {
				t.Errorf("ParseFirmware = %+v, %v; want %+v, ok %v", got, err, tt.want, tt.ok)
			}
		})
	}
	if s := (Firmware{2, 1, 4, SideL}).String(); s != "2.1.4" {
		t.Errorf("String = %q", s)
	}
	// The firmware reply of the test data parses end to end.
	if fw, err := ParseFirmware(mustReply(t, fwReply).Data); err != nil || fw != (Firmware{2, 1, 4, SideL}) {
		t.Errorf("firmware reply: %+v, %v", fw, err)
	}
}

func FuzzParseReply(f *testing.F) {
	f.Add(fwReply)
	f.Add(featReply)
	f.Add(flashReply(FlashInfo.Addr, FlashInfo.Len, 0x41))
	f.Add(flashReply(FlashFactoryStick.Addr, FlashFactoryStick.Len, 0xff))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		r, err := ParseReply(b)
		if err != nil {
			return
		}
		if len(r.Data) != len(b)-headerLen {
			t.Fatalf("%d data bytes of %d", len(r.Data), len(b))
		}
		for _, q := range allRequests() {
			r.Answers(q)
		}
		for _, fr := range flashRanges {
			if d, err := FlashData(r, fr); err == nil && len(d) != int(fr.Len) {
				t.Fatalf("FlashData returned %d bytes for %+v", len(d), fr)
			}
		}
		// The parsers of data take any bytes.
		_, _ = ParseFirmware(r.Data)
		_, _ = ParseInfo(r.Data)
	})
}
