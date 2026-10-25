package input_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

func TestVirtualCapabilitiesAreKnown(t *testing.T) {
	s := open(t, nil)
	_, added := attach(t, s, "capabilities")
	info := added.Info
	if info.Capabilities != (input.Capabilities{Known: true}) {
		t.Fatalf("plain virtual gamepad capabilities=%+v", info.Capabilities)
	}
	if info.ButtonMask == 0 || info.AxisMask == 0 {
		t.Fatal("buttons/axes missing")
	}
}

func TestCapabilitiesTraceRoundTripAndValidation(t *testing.T) {
	info := &input.DeviceInfo{ID: 1, IsGamepad: true, ButtonMask: 3, AxisMask: 5, Capabilities: input.Capabilities{Known: true, Gyro: true, Accelerometer: true, Touchpads: 2, Rumble: true, TriggerRumble: true, LED: true, RGBLED: true}}
	var out bytes.Buffer
	if err := input.WriteEvent(&out, input.Event{Device: 1, Kind: input.DeviceAdded, Info: info}); err != nil {
		t.Fatal(err)
	}
	events, err := input.ReadEvents(bytes.NewReader(out.Bytes()))
	if err != nil || len(events) != 1 || !reflect.DeepEqual(events[0].Info, info) {
		t.Fatalf("roundtrip=%+v, %v", events, err)
	}
	for _, bad := range []string{
		strings.Replace(out.String(), `"touchpads":2`, `"touchpads":-1`, 1),
		strings.Replace(out.String(), `"known":true`, `"known":null`, 1),
		strings.Replace(out.String(), `"gyro":true`, `"gyro":true,"gyro":false`, 1),
		strings.Replace(out.String(), `"gyro":true`, `"unknown":true`, 1),
	} {
		if _, err := input.ReadEvents(strings.NewReader(bad)); err == nil {
			t.Fatalf("accepted invalid capabilities: %s", bad)
		}
	}
}
