package input

import (
	"testing"
	"time"
)

func TestConfiguredComboPick(t *testing.T) {
	s := &Source{comboFor: func(info DeviceInfo, vertical bool) Combo {
		if info.Type != "xboxone" || vertical {
			t.Fatal(info, vertical)
		}
		return Combo{Buttons: []string{"west"}, Hold: 3 * time.Second}
	}}
	got := s.comboPick()(DeviceInfo{Type: "xboxone"})
	if len(got.Buttons) != 1 || got.Buttons[0] != "west" || got.Hold != 3*time.Second {
		t.Fatal(got)
	}
	// The test-only duration seam still overrides the configured hold.
	s.comboHold = time.Millisecond
	if got := s.comboPick()(DeviceInfo{Type: "xboxone"}); got.Hold != time.Millisecond {
		t.Fatal(got)
	}
}
