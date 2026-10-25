package input_test

import (
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
)

// Virtual USB identities exercise the actual per-device SDL label API. They
// verify the labels published by Source, not a renderer's type-name guesses.
func TestVirtualButtonLabels(t *testing.T) {
	for _, tc := range []struct {
		name, typ string
		desc      input.VirtualDesc
		labels    input.FaceButtonLabels
	}{
		{"Xbox", "xboxone", input.VirtualDesc{Name: "padmate Xbox labels", Vendor: 0x045e, Product: 0x0b13}, input.FaceButtonLabels{South: "A", East: "B", West: "X", North: "Y"}},
		{"PlayStation", "ps5", input.VirtualDesc{Name: "padmate PS labels", Vendor: 0x054c, Product: 0x0ce6}, input.FaceButtonLabels{South: "Cross", East: "Circle", West: "Square", North: "Triangle"}},
		{"Nintendo", "switchpro", inputtest.SwitchPro("padmate Switch labels"), input.FaceButtonLabels{South: "B", East: "A", West: "Y", North: "X"}},
		{"JoyConLeft", "joyconleft", inputtest.JoyConLeft("padmate L labels"), input.FaceButtonLabels{South: "B", East: "A", West: "Y", North: "X"}},
		{"JoyConRight", "joyconright", inputtest.JoyConRight("padmate R labels"), input.FaceButtonLabels{South: "B", East: "A", West: "Y", North: "X"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := open(t, nil)
			v, err := s.AttachVirtualDesc(tc.desc)
			if err != nil {
				t.Fatal(err)
			}
			added := collect(t, s, v.ID(), 1)[0]
			if added.Info == nil || added.Info.Type != tc.typ || added.Info.ButtonLabels != tc.labels {
				t.Fatalf("SDL labels: %+v, want type %s and %+v", added.Info, tc.typ, tc.labels)
			}
		})
	}
}
