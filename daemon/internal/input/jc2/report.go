package jc2

import "fmt"

// ReportLen is the length of a native input report.
const ReportLen = 63

// Button is a physical button of a Joy-Con 2.
type Button uint8

// The physical buttons. A left Joy-Con has the first group, a right one the
// second; both have SL and SR.
const (
	ButtonUp Button = iota
	ButtonDown
	ButtonLeft
	ButtonRight
	ButtonL
	ButtonZL
	ButtonMinus
	ButtonCapture
	ButtonLStick // click of the left stick

	ButtonA
	ButtonB
	ButtonX
	ButtonY
	ButtonR
	ButtonZR
	ButtonPlus
	ButtonHome
	ButtonC
	ButtonRStick // click of the right stick

	ButtonSL
	ButtonSR

	numButtons
)

var buttonNames = [numButtons]string{
	ButtonUp: "Up", ButtonDown: "Down", ButtonLeft: "Left", ButtonRight: "Right",
	ButtonL: "L", ButtonZL: "ZL", ButtonMinus: "Minus", ButtonCapture: "Capture", ButtonLStick: "LStick",
	ButtonA: "A", ButtonB: "B", ButtonX: "X", ButtonY: "Y",
	ButtonR: "R", ButtonZR: "ZR", ButtonPlus: "Plus", ButtonHome: "Home", ButtonC: "C", ButtonRStick: "RStick",
	ButtonSL: "SL", ButtonSR: "SR",
}

func (b Button) String() string {
	if b < numButtons {
		return buttonNames[b]
	}
	return fmt.Sprintf("Button(%d)", b)
}

// Buttons is a set of Buttons, one bit each.
type Buttons uint32

// Has reports whether b is in the set.
func (s Buttons) Has(b Button) bool { return b < numButtons && s&(1<<b) != 0 }

// Power is the power byte of a report.
type Power struct {
	Level    uint8 // 0..9
	External bool  // external power is connected
	Charging bool
}

// Report is a decoded native input report.
type Report struct {
	Counter uint8 // +1 per report, wraps
	Power   Power
	Buttons Buttons
	// StickX and StickY are the raw 12-bit stick position; Y is larger up.
	StickX, StickY uint16
}

// buttonBit is one bit of the two button bytes (byte 2 is the low byte,
// byte 3 the high one) and the button it means. Bits that are in no table,
// such as the grip button, are ignored.
type buttonBit struct {
	bit uint16
	btn Button
}

var (
	leftBits = []buttonBit{
		{1 << 0, ButtonDown}, {1 << 1, ButtonRight}, {1 << 2, ButtonLeft}, {1 << 3, ButtonUp},
		{1 << 4, ButtonL}, {1 << 5, ButtonZL}, {1 << 6, ButtonMinus}, {1 << 7, ButtonLStick},
		{1 << 8, ButtonCapture}, {1 << 14, ButtonSR}, {1 << 15, ButtonSL},
	}
	rightBits = []buttonBit{
		{1 << 0, ButtonB}, {1 << 1, ButtonA}, {1 << 2, ButtonY}, {1 << 3, ButtonX},
		{1 << 4, ButtonR}, {1 << 5, ButtonZR}, {1 << 6, ButtonPlus}, {1 << 7, ButtonRStick},
		{1 << 8, ButtonHome}, {1 << 12, ButtonC}, {1 << 14, ButtonSR}, {1 << 15, ButtonSL},
	}
)

// ParseReport decodes a native input report of the controller on side. It
// requires exactly ReportLen bytes. Bits it does not know are ignored.
func ParseReport(side Side, b []byte) (Report, error) {
	if len(b) != ReportLen {
		return Report{}, fmt.Errorf("jc2: report of %d bytes, want %d", len(b), ReportLen)
	}
	var bits []buttonBit
	switch side {
	case SideL:
		bits = leftBits
	case SideR:
		bits = rightBits
	default:
		return Report{}, fmt.Errorf("jc2: report of side %v", side)
	}
	r := Report{
		Counter: b[0],
		Power: Power{
			Level:    min(b[1]>>2&0x0f, 9),
			External: b[1]&0x01 != 0,
			Charging: b[1]&0x02 != 0,
		},
		StickX: uint16(b[5]) | uint16(b[6]&0x0f)<<8,
		StickY: uint16(b[6]>>4) | uint16(b[7])<<4,
	}
	raw := uint16(b[2]) | uint16(b[3])<<8
	for _, e := range bits {
		if raw&e.bit != 0 {
			r.Buttons |= 1 << e.btn
		}
	}
	return r, nil
}
