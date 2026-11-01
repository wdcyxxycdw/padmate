package jc2

import (
	"bytes"
	"math"
	"testing"
)

// The calibrations the owner's controllers measured (firmware 2.1.4), typed
// in by hand.
var (
	calR = StickCal{NeutralX: 2053, NeutralY: 1999, PosX: 1225, PosY: 1205, NegX: 1242, NegY: 1209, Factory: true}
	calL = StickCal{NeutralX: 2049, NeutralY: 2112, PosX: 1241, PosY: 1211, NegX: 1180, NegY: 1249, Factory: true}
)

// pack12 packs two 12-bit values into 3 bytes, like the stick in a report.
func pack12(x, y uint16) []byte {
	return []byte{byte(x), byte(x>>8)&0x0f | byte(y&0x0f)<<4, byte(y >> 4)}
}

// calBytes encodes c as the 9 bytes of the flash.
func calBytes(c StickCal) []byte {
	var b []byte
	b = append(b, pack12(c.NeutralX, c.NeutralY)...)
	b = append(b, pack12(c.PosX, c.PosY)...)
	return append(b, pack12(c.NegX, c.NegY)...)
}

// factoryBlock is a FlashFactoryStick block that holds the 9 bytes b.
func factoryBlock(b []byte) []byte {
	blk := bytes.Repeat([]byte{0xff}, int(FlashFactoryStick.Len))
	copy(blk[0x28:], b)
	return blk
}

// userBlock is a FlashUserStick block with the magic and the 9 bytes b.
func userBlock(b []byte) []byte {
	blk := bytes.Repeat([]byte{0xff}, int(FlashUserStick.Len))
	copy(blk, []byte{0xb2, 0xa1})
	copy(blk[2:], b)
	return blk
}

func TestPack12(t *testing.T) {
	// The same packing as the stick of a report: X = b0 | (b1&0x0f)<<8,
	// Y = b1>>4 | b2<<4.
	x, y := unpack12(pack12(0xabc, 0x123))
	if x != 0xabc || y != 0x123 {
		t.Errorf("round trip = %#x, %#x", x, y)
	}
	if got := pack12(2053, 1999); !bytes.Equal(got, []byte{0x05, 0xf8, 0x7c}) {
		t.Errorf("pack12(2053, 1999) = % x", got)
	}
}

func TestParseFactoryStick(t *testing.T) {
	absent := bytes.Repeat([]byte{0xff}, 9)
	zeroRange := calR
	zeroRange.NegY = 0
	tests := []struct {
		name  string
		block []byte
		want  StickCal
		ok    bool
	}{
		{"right", factoryBlock(calBytes(calR)), calR, true},
		{"left", factoryBlock(calBytes(calL)), calL, true},
		{"absent", factoryBlock(absent), StickCal{}, false},
		{"zero range", factoryBlock(calBytes(zeroRange)), StickCal{}, false},
		{"all zero", make([]byte, 0x40), StickCal{}, false},
		{"minimal block", factoryBlock(calBytes(calR))[:0x28+9], calR, true},
		{"one byte short", factoryBlock(calBytes(calR))[:0x28+8], StickCal{}, false},
		{"empty", nil, StickCal{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseFactoryStick(tt.block)
			if ok != tt.ok || got != tt.want {
				t.Errorf("ParseFactoryStick = %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestParseUserStick(t *testing.T) {
	user := calL
	user.Factory = false
	zeroRange := user
	zeroRange.PosX = 0
	badMagic := userBlock(calBytes(user))
	badMagic[1] = 0xa0
	tests := []struct {
		name  string
		block []byte
		want  StickCal
		ok    bool
	}{
		{"present", userBlock(calBytes(user)), user, true},
		{"absent", bytes.Repeat([]byte{0xff}, 0x40), StickCal{}, false},
		{"magic only", userBlock(bytes.Repeat([]byte{0xff}, 9)), StickCal{}, false},
		{"wrong magic", badMagic, StickCal{}, false},
		{"zero range", userBlock(calBytes(zeroRange)), StickCal{}, false},
		{"minimal block", userBlock(calBytes(user))[:11], user, true},
		{"one byte short", userBlock(calBytes(user))[:10], StickCal{}, false},
		{"empty", nil, StickCal{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseUserStick(tt.block)
			if ok != tt.ok || got != tt.want {
				t.Errorf("ParseUserStick = %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestChooseStick(t *testing.T) {
	user := calL
	user.Factory = false
	tests := []struct {
		name          string
		user, factory StickCal
		want          StickCal
	}{
		{"user wins", user, calR, user},
		{"factory", StickCal{}, calR, calR},
		{"neither", StickCal{}, StickCal{}, DefaultStick},
		{"invalid user", StickCal{NeutralX: 2048, PosX: 5}, calR, calR},
		{"user only", user, StickCal{}, user},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ChooseStick(tt.user, tt.factory); got != tt.want {
				t.Errorf("ChooseStick = %+v, want %+v", got, tt.want)
			}
		})
	}
	if !DefaultStick.Valid() || DefaultStick.NeutralX != 2048 || DefaultStick.PosX != 1200 {
		t.Errorf("DefaultStick = %+v", DefaultStick)
	}
}

func TestNorm(t *testing.T) {
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	tests := []struct {
		name   string
		cal    StickCal
		x, y   uint16
		fx, fy float64
	}{
		{"neutral", calR, 2053, 1999, 0, 0},
		{"full right up", calR, 2053 + 1225, 1999 + 1205, 1, 1},
		{"full left down", calR, 2053 - 1242, 1999 - 1209, -1, -1},
		{"half right, half left (own ranges)", calR, 2053 + 1225/2, 1999 - 1209/2, float64(612) / 1225, -float64(604) / 1209},
		{"past the edge", calR, 4095, 4095, 1, 1},
		{"zero", calR, 0, 0, -1, -1},
		{"left asymmetry", calL, 2049 - 1180/2, 2112 + 1211/2, -float64(590) / 1180, float64(605) / 1211},
		{"left rests 45 above neutral Y", calL, 2049, 2112 + 45, 0, 45.0 / 1211},
		{"default", DefaultStick, 2048 + 600, 2048 - 600, 0.5, -0.5},
		{"not valid", StickCal{NeutralX: 2048}, 3000, 100, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx, fy := tt.cal.Norm(tt.x, tt.y)
			if !near(fx, tt.fx) || !near(fy, tt.fy) {
				t.Errorf("Norm(%d, %d) = %v, %v; want %v, %v", tt.x, tt.y, fx, fy, tt.fx, tt.fy)
			}
		})
	}
}

func FuzzParseStick(f *testing.F) {
	f.Add(factoryBlock(calBytes(calR)), uint16(2053), uint16(1999))
	f.Add(userBlock(calBytes(calL)), uint16(0), uint16(4095))
	f.Add(bytes.Repeat([]byte{0xff}, 0x40), uint16(1), uint16(2))
	f.Add([]byte{}, uint16(0), uint16(0))
	f.Fuzz(func(t *testing.T, block []byte, x, y uint16) {
		fc, fok := ParseFactoryStick(block)
		uc, uok := ParseUserStick(block)
		if fok && (!fc.Valid() || !fc.Factory) {
			t.Fatalf("factory %+v ok but not valid", fc)
		}
		if uok && (!uc.Valid() || uc.Factory) {
			t.Fatalf("user %+v ok but not valid or factory", uc)
		}
		if !fok && fc != (StickCal{}) || !uok && uc != (StickCal{}) {
			t.Fatalf("not ok but %+v / %+v", fc, uc)
		}
		for _, c := range []StickCal{ChooseStick(uc, fc), {NeutralX: x, NeutralY: y, PosX: x, PosY: y, NegX: y, NegY: x}} {
			fx, fy := c.Norm(x, y)
			if fx < -1 || fx > 1 || fy < -1 || fy > 1 || math.IsNaN(fx) || math.IsNaN(fy) {
				t.Fatalf("%+v.Norm(%d, %d) = %v, %v", c, x, y, fx, fy)
			}
		}
		if !ChooseStick(uc, fc).Valid() {
			t.Fatal("ChooseStick gave an invalid calibration")
		}
	})
}
