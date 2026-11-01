package script

import (
	"fmt"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// guide, the Xbox, PS or Home button, is never mapped (ADR-0012), by a
// script either: a handler, an observer or a claim on it is a TypeError, as
// one on an unknown control is, and the pad reads it as up and absent.
func TestScriptsCannotUseGuide(t *testing.T) {
	for _, src := range []string{
		`__padmate.on("guide", "press", () => {})`,
		`__padmate.global().on("guide", "tap", () => {})`,
		`__padmate.observe("guide", () => {})`,
		`__padmate.global().claim("guide")`,
	} {
		h := NewHost()
		_, err := h.VM.RunString(src)
		if err == nil || !strings.Contains(err.Error(), "TypeError") || !strings.Contains(err.Error(), "guide") {
			t.Errorf("%s: error %v; want a TypeError that names guide", src, err)
		}
		for _, r := range h.registrations {
			if r.Control == "guide" {
				t.Errorf("%s: registered %+v", src, r)
			}
		}
	}
	h := NewHost()
	state := PadState{Connected: true, Info: input.DeviceInfo{Name: "pad", Type: "xboxone"}}
	state.HasControl[control.Guide], state.Down[control.Guide] = true, true
	state.HasControl[control.South], state.Down[control.South] = true, true
	h.UpdatePad(state)
	v, err := h.VM.RunString(`[__padmate.pad.isDown("guide"), __padmate.pad.heldFor("guide"), __padmate.pad.has("guide"), __padmate.pad.isDown("south")]`)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(v.Export()); got != "[false 0 false true]" {
		t.Errorf("isDown, heldFor, has of guide and isDown of south = %v; want false, 0, false, true", got)
	}
}
