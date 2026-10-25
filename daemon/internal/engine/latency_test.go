package engine

import (
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

func TestAddedLatency(t *testing.T) {
	for _, tc := range []struct {
		gs   gestureSet
		want time.Duration
	}{
		{0, 0},
		{gestureSet(0).with(GesturePress), 0},
	} {
		if got := addedLatency(tc.gs); got != tc.want {
			t.Errorf("addedLatency(%b) = %v, want %v", tc.gs, got, tc.want)
		}
	}
	// A gesture without a rule is a bug in the engine, not in the profile.
	defer func() {
		if recover() == nil {
			t.Error("addedLatency of an unknown gesture did not panic")
		}
	}()
	addedLatency(gestureSet(0).with(GesturePress).with(Gesture(profile.GestureCount)))
}

func TestGestures(t *testing.T) {
	press := &profile.Action{ID: "/layers/base/south/press", Type: profile.ActionKeys, Keys: keys.MustParse("Z"), Mode: profile.ModeTap}
	prog, err := Compile(&profile.Profile{Layers: []*profile.Layer{{Name: "base", Bindings: []*profile.Binding{
		{ID: "/layers/base/south", Control: control.South, Press: press},
		{ID: "/layers/base/east", Control: control.East, Label: "todo"},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		c    control.Control
		want gestureSet
	}{
		{control.South, gestureSet(0).with(GesturePress)},
		{control.East, 0}, // a label alone reaches no gesture
		{control.West, 0}, // unbound
	} {
		gs := prog.gestures(tc.c)
		if gs != tc.want {
			t.Errorf("gestures(%s) = %b, want %b", tc.c, gs, tc.want)
		}
		if gs.has(GesturePress) != (tc.want != 0) || gs.has(GestureNone) {
			t.Errorf("gestures(%s).has is wrong for %b", tc.c, gs)
		}
	}
}

func TestRawThresholds(t *testing.T) {
	if p, r := rawThreshold(TriggerPress), rawThreshold(TriggerRelease); p != 9830 || r != 6553 {
		t.Errorf("raw thresholds = %d, %d; want 9830, 6553", p, r)
	}
	if got := rawThreshold(1); got != 32767 {
		t.Errorf("rawThreshold(1) = %d", got)
	}
}
