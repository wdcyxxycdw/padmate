package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
)

func TestDecodeIPCInputIncomplete(t *testing.T) {
	for _, row := range []ipc.Notification{
		{Topic: "dropped", Data: json.RawMessage(`{"n":2}`)},
		{Topic: "log", Data: json.RawMessage(`{}`)},
		{Topic: "input", Data: json.RawMessage("")},
		{Topic: "input", Data: json.RawMessage("{\"timeNs\":1,\"kind\":\"down\",\"device\":1,\"control\":\"south\"}\n{\"timeNs\":2,\"kind\":\"up\",\"device\":1,\"control\":\"south\"}")},
	} {
		if _, err := decodeIPCInput(row); err == nil {
			t.Fatalf("accepted incomplete or invalid notification: %+v", row)
		}
	}
}

func TestInputDefaultDoesNotFallBackToSDL(t *testing.T) {
	t.Setenv(input.EnvLibrary, "must-not-load-sdl")
	for _, run := range []func(context.Context, []string, *bytes.Buffer, *bytes.Buffer) int{
		func(ctx context.Context, args []string, out, errOut *bytes.Buffer) int {
			return monitorMain(ctx, args, out, errOut)
		},
		func(ctx context.Context, args []string, out, errOut *bytes.Buffer) int {
			return recordMain(ctx, args, out, errOut)
		},
	} {
		var out, errOut bytes.Buffer
		code := run(context.Background(), []string{"--ipc-dir", t.TempDir()}, &out, &errOut)
		if code != 1 || out.Len() != 0 || strings.Contains(errOut.String(), input.ErrNoLibrary.Error()) {
			t.Fatalf("code=%d out=%q error=%q", code, &out, &errOut)
		}
	}
}

// TestDecodeIPCInputRemapped: the input topic is the trace format plus a
// "remapped" row for a gamepad whose mapping changed (IN-18); traces do not
// carry it.
func TestDecodeIPCInputRemapped(t *testing.T) {
	row := func(data string) ipc.Notification {
		return ipc.Notification{Topic: "input", Data: json.RawMessage(data)}
	}
	e, err := decodeIPCInput(row(`{"timeNs":7,"kind":"remapped","device":3,"info":{"id":3,"name":"Pad","gamepad":true,"buttonMask":21,"axisMask":3}}`))
	if err != nil || e.Kind != input.DeviceRemapped || e.Device != 3 || e.Time != 7 || e.Info == nil || e.Info.ButtonMask != 21 || e.Info.AxisMask != 3 {
		t.Fatalf("decoded %+v, %v", e, err)
	}
	if e, err = decodeIPCInput(row(`{"timeNs":8,"kind":"remapped","device":3}`)); err != nil || e.Kind != input.DeviceRemapped || e.Info != nil {
		t.Fatalf("a remap without info: %+v, %v", e, err)
	}
	for _, bad := range []string{
		`{"timeNs":8,"kind":"remapped"}`,
		`{"timeNs":8,"kind":"remapped","device":0}`,
		`{"timeNs":8,"kind":"remapped","device":3,"control":"south"}`,
		`{"timeNs":8,"kind":"remapped","device":3,"info":{"id":4}}`,
		`{"timeNs":8,"kind":"remapped","device":3}{"timeNs":9,"kind":"remapped","device":3}`,
	} {
		if e, err := decodeIPCInput(row(bad)); err == nil {
			t.Errorf("decodeIPCInput(%s) = %+v, want an error", bad, e)
		}
	}
}

// TestAddedSet: the daemon's list and the rows queued before it overlap, so a
// DeviceAdded that repeats a device is dropped; but a joystick that got a
// mapping is reported again as a gamepad with the same id, and one that lost it
// is removed and reported again, and those are shown.
func TestAddedSet(t *testing.T) {
	added := func(id input.DeviceID, gamepad bool) input.Event {
		return input.Event{Kind: input.DeviceAdded, Device: id, Info: &input.DeviceInfo{ID: id, IsGamepad: gamepad}}
	}
	seen := addedSet{}
	for i, step := range []struct {
		e    input.Event
		want bool
	}{
		{added(1, false), true},
		{added(1, false), false}, // the copy that was queued before the list
		{added(1, true), true},   // upgrade
		{added(1, true), false},
		{input.Event{Kind: input.ButtonDown, Device: 1, Control: "south"}, true},
		{input.Event{Kind: input.DeviceRemapped, Device: 1}, true},
		{input.Event{Kind: input.DeviceRemoved, Device: 1}, true}, // downgrade
		{added(1, false), true},
		{added(2, true), true},
		{added(2, true), false},
	} {
		if got := seen.fresh(step.e); got != step.want {
			t.Errorf("step %d (%s of %d): fresh = %v, want %v", i, step.e.Kind, step.e.Device, got, step.want)
		}
	}
}
