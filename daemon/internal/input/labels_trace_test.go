package input_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

func TestTraceButtonLabels(t *testing.T) {
	labels := input.FaceButtonLabels{South: "Cross", East: "Circle", West: "Square", North: "Triangle"}
	info := &input.DeviceInfo{ID: 7, Type: "ps5", ButtonLabels: labels}
	var out bytes.Buffer
	if err := input.WriteEvent(&out, input.Event{Device: 7, Kind: input.DeviceAdded, Info: info}); err != nil {
		t.Fatal(err)
	}
	events, err := input.ReadEvents(&out)
	if err != nil || len(events) != 1 || events[0].Info.ButtonLabels != labels {
		t.Fatalf("label round trip: %+v, %v", events, err)
	}
	for _, tc := range []struct{ name, object, want string }{
		{"wrong type", `{"south":true}`, "info.buttonLabels.south: want a string"},
		{"unknown control", `{"a":"A"}`, `unknown field "info.buttonLabels.a"`},
		{"duplicate", `{"south":"A","south":"B"}`, `duplicate field "info.buttonLabels.south"`},
		{"not object", `[]`, "want a JSON object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := `{"timeNs":0,"kind":"added","device":7,"info":{"buttonLabels":` + tc.object + `}}`
			if _, err := input.ReadEvents(strings.NewReader(line)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ReadEvents = %v, want %q", err, tc.want)
			}
		})
	}
}
