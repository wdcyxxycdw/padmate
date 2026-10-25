package script

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// jsNumber is f as a JavaScript literal.
func jsNumber(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// What the effects API hands the gamepad always keeps its limits, whatever a
// script passes: a call either throws a TypeError or sends steps of 1 to 5000
// ms or a preset that exists; and once the runtime has ended, nothing that the
// script started is left going (SCR-23).
func FuzzEffects(f *testing.F) {
	f.Add(0.5, 1.0, 200.0, "tick", "#FFAA00", uint8(0))
	f.Add(-3.0, 7.0, 0.0, "pulse5", "red", uint8(1))
	f.Add(math.NaN(), math.Inf(1), math.Inf(-1), "", "#GGGGGG", uint8(2))
	f.Add(0.0, 0.0, 99999.0, "confirm", "#00ff00", uint8(3))
	f.Add(1.0, 1.0, 0.4, "error\x00", "#12345", uint8(4))
	f.Add(1e300, -1e300, 1234.5, "pulse3", "", uint8(5))
	f.Fuzz(func(t *testing.T, a, b, c float64, preset, color string, mode uint8) {
		sink := &fakeEffects{rumble: true, led: true}
		h, _ := effectHost(t, sink, effectPad(allEffects), "")
		calls := []string{
			"pad.rumble(" + jsNumber(a) + ", " + jsNumber(b) + ", " + jsNumber(c) + ")",
			"pad.rumbleTriggers(" + jsNumber(b) + ", " + jsNumber(a) + ", " + jsNumber(c) + ")",
			"pad.haptic(" + strconv.Quote(preset) + ")",
			"pad.setLed(" + strconv.Quote(color) + ")",
			"pad.setLed(null)",
			"pad.rumble(0, 0, 0)",
		}
		for i := range 12 {
			src := calls[(int(mode)+i*5)%len(calls)]
			if !utf8.ValidString(src) {
				continue
			}
			if _, err := h.VM.RunString(src); err != nil && !strings.Contains(err.Error(), "TypeError") {
				t.Fatalf("%s: %v", src, err)
			}
		}
		_ = h.Dispose()

		rumbling, lit := false, false
		for _, call := range sink.calls {
			if call.Dev != effectPadID || call.Slot != 2 {
				t.Fatalf("call for the wrong gamepad: %+v", call)
			}
			switch call.Kind {
			case "rumble":
				if n := len(call.Steps); n < 1 || n > 3 {
					t.Fatalf("%d steps", n)
				}
				for _, s := range call.Steps {
					if s.Ms < 1 || s.Ms > rumbleMaxMs {
						t.Fatalf("step %+v", s)
					}
				}
				rumbling = true
			case "haptic":
				if !call.Preset.Valid() {
					t.Fatalf("preset %v", call.Preset)
				}
				rumbling = true
			case "stop":
				rumbling = false
			case "light":
				lit = true
			case "release":
				lit = false
			case "cancel":
				rumbling, lit = false, false
			}
		}
		if rumbling || lit {
			t.Fatalf("after the runtime ended: rumbling %v, lit %v; calls %+v", rumbling, lit, sink.calls)
		}
	})
}
