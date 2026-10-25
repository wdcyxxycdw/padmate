package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// fakeReadouts stands in for an *input.Source: it answers Readout from a
// table, as T-input would from SDL's getters, so no SDL is needed.
type fakeReadouts struct {
	by    map[input.DeviceID]input.Readout
	asked []input.DeviceID
}

func (f *fakeReadouts) Readout(id input.DeviceID) (input.Readout, error) {
	f.asked = append(f.asked, id)
	r, ok := f.by[id]
	if !ok {
		return input.Readout{}, errors.New("input: device is not an open gamepad")
	}
	return r, nil
}

var (
	xinputPad = input.DeviceInfo{
		ID: 3, Name: "XInput Controller #1", Type: "xboxone", IsGamepad: true, Driver: "xinput",
		Capabilities: input.Capabilities{Known: true, Rumble: true},
	}
	switchPad = input.DeviceInfo{
		ID: 4, Name: "Nintendo Switch Pro Controller", Type: "switchpro", IsGamepad: true, Driver: "hidapi",
		Capabilities: input.Capabilities{Known: true, Gyro: true, Accelerometer: true, Rumble: true, LED: true, PlayerLED: true},
	}
	ps4Pad = input.DeviceInfo{
		ID: 5, Name: "PS4 Controller", Type: "ps4", IsGamepad: true, Driver: "hidapi",
		Capabilities: input.Capabilities{Known: true, Touchpads: 1, Rumble: true, TriggerRumble: true, LED: true, RGBLED: true},
	}
	oldPad    = input.DeviceInfo{ID: 6, Name: "From an old trace", Type: "unknown", IsGamepad: true}
	wheelInfo = input.DeviceInfo{ID: 7, Name: "Wheel", Type: "unknown"}
)

// TestMonitorListPrintsPower (IN-12): "monitor --list --direct" prints, under
// the device lines, a block per gamepad with what SDL says: its
// capabilities, power, connection state and player index. The text is
// the one the hardware matrix takes.
func TestMonitorListPrintsPower(t *testing.T) {
	src := &fakeReadouts{by: map[input.DeviceID]input.Readout{
		3: {Power: input.Power{State: input.PowerCharging, Percent: 100}, Connection: "wired", PlayerIndex: 0},
		4: {Power: input.Power{State: input.PowerDischarging, Percent: 38}, Connection: "wireless", PlayerIndex: 2},
		5: {Power: input.Power{State: input.PowerWired, Percent: -1}, Connection: "unknown", PlayerIndex: -1},
		6: {Power: input.Power{State: input.PowerUnknown, Percent: -1}, Connection: "unknown", PlayerIndex: -1},
	}}
	pads := padDetailsFor(src, []input.DeviceInfo{xinputPad, switchPad, ps4Pad, oldPad, wheelInfo})
	var b bytes.Buffer
	if err := printPadDetails(&b, pads); err != nil {
		t.Fatal(err)
	}
	want := `pad 3 "XInput Controller #1"
  capabilities  rumble
  power         charging 100%
  connection    wired
  player index  0
pad 4 "Nintendo Switch Pro Controller"
  capabilities  rumble, led, playerLed, gyro, accelerometer
  power         discharging 38%
  connection    wireless
  player index  2
pad 5 "PS4 Controller"
  capabilities  rumble, triggerRumble, rgbLed, touchpads=1
  power         wired
  connection    unknown
  player index  none
pad 6 "From an old trace"
  capabilities  unknown
  power         unknown
  connection    unknown
  player index  none
`
	if b.String() != want {
		t.Errorf("printed:\n%s\nwant:\n%s", b.String(), want)
	}
	// A joystick without a gamepad mapping has no gamepad to read: it gets
	// no block, and SDL is not asked.
	for _, id := range src.asked {
		if id == wheelInfo.ID {
			t.Error("Readout asked for a joystick without a gamepad mapping")
		}
	}
	if len(src.asked) != 4 {
		t.Errorf("Readout asked for %v, want the four gamepads", src.asked)
	}
}

// TestMonitorListReadoutFailure: a gamepad that is gone before it could be
// read says so, in its block, and the other blocks are still printed.
func TestMonitorListReadoutFailure(t *testing.T) {
	src := &fakeReadouts{by: map[input.DeviceID]input.Readout{
		4: {Power: input.Power{State: input.PowerDischarging, Percent: 0}, Connection: "wireless", PlayerIndex: -1},
	}}
	var b bytes.Buffer
	if err := printPadDetails(&b, padDetailsFor(src, []input.DeviceInfo{xinputPad, switchPad})); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.HasPrefix(got, "pad 3 \"XInput Controller #1\"\n  capabilities  rumble\n  readout       failed: input: device is not an open gamepad\n") {
		t.Errorf("block of the failed readout:\n%s", got)
	}
	if !strings.Contains(got, "pad 4 \"Nintendo Switch Pro Controller\"\n  capabilities  rumble, led, playerLed, gyro, accelerometer\n  power         discharging 0%\n") {
		t.Errorf("the block after it:\n%s", got)
	}
}

// TestMonitorListFromTheDaemon: the daemon reports the battery of devices.list
// and no connection state or player index, which only SDL's getters give: the
// block says so and points at --direct.
func TestMonitorListFromTheDaemon(t *testing.T) {
	bat := input.Power{State: input.PowerDischarging, Percent: 70}
	devices := []ipcDevice{
		{Info: switchPad, Battery: &bat},
		{Info: xinputPad},
		{Info: wheelInfo},
	}
	var b bytes.Buffer
	if err := printPadDetails(&b, padDetailsFromIPC(devices)); err != nil {
		t.Fatal(err)
	}
	want := `pad 4 "Nintendo Switch Pro Controller"
  capabilities  rumble, led, playerLed, gyro, accelerometer
  power         discharging 70%
  connection    not reported by the daemon; use --direct
  player index  not reported by the daemon; use --direct
pad 3 "XInput Controller #1"
  capabilities  rumble
  power         not read
  connection    not reported by the daemon; use --direct
  player index  not reported by the daemon; use --direct
`
	if b.String() != want {
		t.Errorf("printed:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestFormatCapabilities(t *testing.T) {
	tests := []struct {
		c    input.Capabilities
		want string
	}{
		{input.Capabilities{}, "unknown"},
		{input.Capabilities{Known: true}, "none"},
		{input.Capabilities{Known: true, Rumble: true, TriggerRumble: true}, "rumble, triggerRumble"},
		{input.Capabilities{Known: true, LED: true}, "led"},
		{input.Capabilities{Known: true, LED: true, RGBLED: true}, "rgbLed"},
		{input.Capabilities{Known: true, RGBLED: true}, "rgbLed"},
		{input.Capabilities{Known: true, PlayerLED: true, Touchpads: 2, Gyro: true}, "playerLed, gyro, touchpads=2"},
	}
	for _, tt := range tests {
		if got := formatCapabilities(tt.c); got != tt.want {
			t.Errorf("formatCapabilities(%+v) = %q, want %q", tt.c, got, tt.want)
		}
	}
}

func TestFormatPower(t *testing.T) {
	tests := []struct {
		p    *input.Power
		want string
	}{
		{nil, "not read"},
		{&input.Power{State: input.PowerUnknown, Percent: -1}, "unknown"},
		{&input.Power{State: input.PowerUnknown, Percent: 40}, "unknown"},
		{&input.Power{State: input.PowerWired, Percent: -1}, "wired"},
		{&input.Power{State: input.PowerWired, Percent: 100}, "wired 100%"},
		{&input.Power{State: input.PowerCharging, Percent: 0}, "charging 0%"},
		{&input.Power{State: input.PowerDischarging, Percent: -1}, "discharging"},
	}
	for _, tt := range tests {
		if got := formatPower(tt.p); got != tt.want {
			t.Errorf("formatPower(%+v) = %q, want %q", tt.p, got, tt.want)
		}
	}
}

// TestMonitorListTracksDevices: the pads that --list prints are those still
// connected when the wait ends, in the order they were added; one that came
// back has the info of its latest connection.
func TestMonitorListTracksDevices(t *testing.T) {
	var l padList
	a, b, c := xinputPad, switchPad, ps4Pad
	for _, e := range []input.Event{
		{Device: 3, Kind: input.DeviceAdded, Info: &a},
		{Device: 4, Kind: input.DeviceAdded, Info: &b},
		{Device: 3, Kind: input.ButtonDown, Control: "south"},
		{Device: 3, Kind: input.DeviceRemoved},
		{Device: 5, Kind: input.DeviceAdded, Info: &c},
		{Device: 9, Kind: input.DeviceAdded},           // no info
		{Device: 4, Kind: input.DevicePower},           // routing metadata: nothing to track
		{Device: 8, Kind: input.DeviceRemoved},         // never seen
		{Device: 4, Kind: input.DeviceAdded, Info: &b}, // reported twice
	} {
		l.observe(e)
	}
	got := l.infos()
	if len(got) != 2 || got[0].ID != 4 || got[1].ID != 5 {
		t.Errorf("infos = %+v, want devices 4 and 5", got)
	}
}

// TestMonitorAndRecordIgnorePower: a DevicePower is no input event. The
// printer shows nothing of it in any mode, and the recorder neither writes it
// (WriteEvent would refuse it) nor starts a recording with it. A Source sends
// it only to a consumer that asked, so this is the belt to that brace.
func TestMonitorAndRecordIgnorePower(t *testing.T) {
	p := input.Power{State: input.PowerCharging, Percent: 50}
	e := input.Event{Time: 5, Device: 3, Kind: input.DevicePower, Power: &p}
	for _, opts := range []monitorOptions{{}, {json: true}, {list: true}} {
		var b bytes.Buffer
		shown, err := newEventPrinter(&b, opts).print(e)
		if shown || err != nil || b.Len() != 0 {
			t.Errorf("print(%+v) = %t, %v, wrote %q", opts, shown, err, b.String())
		}
	}
	var out, errOut bytes.Buffer
	r := &recorder{w: &out, stderr: &errOut, keys: input.NewRegistry()}
	more, err := r.handle(e)
	if !more || err != nil || r.n != 0 || out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("recorder.handle = %t, %v; wrote %q and %q", more, err, out.String(), errOut.String())
	}
}
