package jc2

import (
	"math/rand/v2"
	"testing"
)

// reportBytes builds a native report: counter, power, two button bytes, the
// 07 marker, the stick, and the 0x30 / zero tail of feature mask 0x03.
func reportBytes(counter, power, b2, b3 byte, x, y uint16) []byte {
	b := make([]byte, ReportLen)
	b[0], b[1], b[2], b[3], b[4] = counter, power, b2, b3, 0x07
	b[5], b[6], b[7] = pack12(x, y)[0], pack12(x, y)[1], pack12(x, y)[2]
	b[8] = 0x30
	return b
}

func TestParseReportButtons(t *testing.T) {
	type bit struct {
		byteNo int // 2 or 3
		bit    uint
		want   Button
	}
	tests := []struct {
		side Side
		bits []bit
		// unknown bits of the two bytes, which must press nothing
		unknown []bit
	}{
		{SideL, []bit{
			{2, 0, ButtonDown}, {2, 1, ButtonRight}, {2, 2, ButtonLeft}, {2, 3, ButtonUp},
			{2, 4, ButtonL}, {2, 5, ButtonZL}, {2, 6, ButtonMinus}, {2, 7, ButtonLStick},
			{3, 0, ButtonCapture}, {3, 6, ButtonSR}, {3, 7, ButtonSL},
		}, []bit{{3, 1, 0}, {3, 2, 0}, {3, 3, 0}, {3, 4, 0}, {3, 5, 0}}},
		{SideR, []bit{
			{2, 0, ButtonB}, {2, 1, ButtonA}, {2, 2, ButtonY}, {2, 3, ButtonX},
			{2, 4, ButtonR}, {2, 5, ButtonZR}, {2, 6, ButtonPlus}, {2, 7, ButtonRStick},
			{3, 0, ButtonHome}, {3, 4, ButtonC}, {3, 6, ButtonSR}, {3, 7, ButtonSL},
		}, []bit{{3, 1, 0}, {3, 2, 0}, {3, 3, 0}, {3, 5, 0}}},
	}
	for _, tt := range tests {
		t.Run(tt.side.String(), func(t *testing.T) {
			seen := map[Button]bool{}
			for _, e := range tt.bits {
				b2, b3 := byte(0), byte(0)
				if e.byteNo == 2 {
					b2 = 1 << e.bit
				} else {
					b3 = 1 << e.bit
				}
				r, err := ParseReport(tt.side, reportBytes(0, 0, b2, b3, 2048, 2048))
				if err != nil {
					t.Fatal(err)
				}
				if r.Buttons != 1<<e.want || !r.Buttons.Has(e.want) {
					t.Errorf("byte %d bit %d: buttons %#x, want only %v", e.byteNo, e.bit, uint32(r.Buttons), e.want)
				}
				seen[e.want] = true
			}
			for _, e := range tt.unknown {
				r, _ := ParseReport(tt.side, reportBytes(0, 0, 0, 1<<e.bit, 2048, 2048))
				if r.Buttons != 0 {
					t.Errorf("unknown byte 3 bit %d pressed %#x", e.bit, uint32(r.Buttons))
				}
			}
			// All bits at once: exactly the known buttons.
			r, _ := ParseReport(tt.side, reportBytes(0, 0, 0xff, 0xff, 2048, 2048))
			if len(seen) != 11 && len(seen) != 12 {
				t.Errorf("table has %d buttons", len(seen))
			}
			for b := Button(0); b < numButtons; b++ {
				if got := r.Buttons.Has(b); got != seen[b] {
					t.Errorf("all bits: %v pressed = %v, want %v", b, got, seen[b])
				}
			}
		})
	}
}

func TestParseReportFields(t *testing.T) {
	b := reportBytes(0xfe, 0x20|0x03, 0, 0, 2053, 1999)
	r, err := ParseReport(SideR, b)
	if err != nil {
		t.Fatal(err)
	}
	want := Report{Counter: 0xfe, Power: Power{Level: 8, External: true, Charging: true}, StickX: 2053, StickY: 1999}
	if r != want {
		t.Errorf("ParseReport = %+v, want %+v", r, want)
	}

	// The first report of the right capture: power 0x20 (level 8), stick
	// at rest.
	first := []byte{0x00, 0x20, 0x00, 0x00, 0x07, 0x0a, 0xd8, 0x7b, 0x30}
	first = append(first, make([]byte, ReportLen-len(first))...)
	r, err = ParseReport(SideR, first)
	if err != nil || r.Power != (Power{Level: 8}) || r.StickX != 0x80a || r.StickY != 0x7bd {
		t.Errorf("first report: %+v, %v", r, err)
	}

	// The levels 0..9 and the clamp of the values above.
	for lvl, want := range map[byte]uint8{0: 0, 1: 1, 9: 9, 10: 9, 15: 9} {
		r, _ := ParseReport(SideL, reportBytes(0, lvl<<2, 0, 0, 0, 0))
		if r.Power.Level != want || r.Power.External || r.Power.Charging {
			t.Errorf("level %d: %+v, want %d", lvl, r.Power, want)
		}
	}

	// The stick extremes.
	for _, c := range []struct{ x, y uint16 }{{0, 0}, {4095, 4095}, {0, 4095}, {4095, 0}, {0x123, 0xabc}} {
		r, _ := ParseReport(SideL, reportBytes(0, 0, 0, 0, c.x, c.y))
		if r.StickX != c.x || r.StickY != c.y {
			t.Errorf("stick %d,%d decoded as %d,%d", c.x, c.y, r.StickX, r.StickY)
		}
	}
}

func TestParseReportErrors(t *testing.T) {
	good := reportBytes(0, 0, 0, 0, 2048, 2048)
	tests := []struct {
		name string
		side Side
		b    []byte
	}{
		{"nil", SideL, nil},
		{"62 bytes", SideL, good[:62]},
		{"64 bytes", SideR, append(append([]byte(nil), good...), 0)},
		{"no side", 0, good},
		{"side 3", 3, good},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if r, err := ParseReport(tt.side, tt.b); err == nil {
				t.Errorf("ParseReport = %+v, want an error", r)
			}
		})
	}
}

func TestButtonNames(t *testing.T) {
	seen := map[string]bool{}
	for b := Button(0); b < numButtons; b++ {
		n := b.String()
		if n == "" || seen[n] {
			t.Errorf("button %d has name %q", b, n)
		}
		seen[n] = true
	}
	if Button(200).String() == "" || Buttons(1).Has(200) {
		t.Error("a button that does not exist")
	}
}

func FuzzParseReport(f *testing.F) {
	f.Add(uint8(SideL), reportBytes(1, 0x20, 0xff, 0xff, 4095, 0))
	f.Add(uint8(SideR), reportBytes(255, 0x27, 0x55, 0xaa, 0, 4095))
	f.Add(uint8(0), []byte{})
	r := rand.New(rand.NewPCG(1, 2))
	for range 4 {
		b := make([]byte, ReportLen)
		for i := range b {
			b[i] = byte(r.Uint32())
		}
		f.Add(uint8(SideR), b)
	}
	f.Fuzz(func(t *testing.T, side uint8, b []byte) {
		rep, err := ParseReport(Side(side), b)
		if err != nil {
			return
		}
		if len(b) != ReportLen || (Side(side) != SideL && Side(side) != SideR) {
			t.Fatalf("accepted side %d with %d bytes", side, len(b))
		}
		if rep.Power.Level > 9 || rep.StickX > 4095 || rep.StickY > 4095 {
			t.Fatalf("out of range: %+v", rep)
		}
		if rep.Buttons >= 1<<numButtons {
			t.Fatalf("unknown button bits: %#x", uint32(rep.Buttons))
		}
		// A button of the other side's cluster never shows up.
		for btn := ButtonUp; btn <= ButtonLStick; btn++ {
			if Side(side) == SideR && rep.Buttons.Has(btn) {
				t.Fatalf("right Joy-Con with %v", btn)
			}
		}
		for btn := ButtonA; btn <= ButtonRStick; btn++ {
			if Side(side) == SideL && rep.Buttons.Has(btn) {
				t.Fatalf("left Joy-Con with %v", btn)
			}
		}
		// Whatever it decodes to maps to a state without panicking, and
		// releasing it is the way back to rest.
		for _, m := range []Mode{Horizontal, Vertical, Pair} {
			s := Map(m, Side(side), rep, DefaultStick)
			if got := apply(State{}, Diff(State{}, s)); got != s {
				t.Fatalf("diff does not reproduce %+v: %+v", s, got)
			}
			if got := apply(s, Released(s)); got != (State{}) {
				t.Fatalf("Released leaves %+v", got)
			}
		}
	})
}
