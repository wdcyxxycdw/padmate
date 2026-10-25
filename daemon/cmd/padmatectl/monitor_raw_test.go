package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
)

// TestMonitorRawFlags checks the flags of "monitor --raw" that are refused
// before SDL is loaded.
func TestMonitorRawFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--device", "3"}, "--device needs --raw"},
		{[]string{"--raw", "--list"}, "--raw cannot be combined"},
		{[]string{"--raw", "--virtual"}, "--raw cannot be combined"},
		{[]string{"--raw", "--device", "4294967296"}, "--device must be at most"},
		{[]string{"--raw", "--device", "x"}, "invalid value"},
	} {
		code, stdout, stderr := runCmd(t, append([]string{"monitor"}, tc.args...)...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, tc.want) || !strings.Contains(stderr, "usage: padmatectl monitor") {
			t.Errorf("monitor %q: exit %d, stdout %q, stderr %q; want 2, nothing, %q and the usage", tc.args, code, stdout, stderr, tc.want)
		}
	}
	// --raw alone is accepted, and through the daemon: with no daemon it fails
	// to connect, not to parse.
	code, _, stderr := runCmd(t, "monitor", "--raw", "--ipc-dir", t.TempDir(), "--timeout", "100ms")
	if code != 1 || strings.Contains(stderr, "usage:") {
		t.Errorf("monitor --raw without a daemon: exit %d, stderr %q", code, stderr)
	}
}

func TestMonitorRawFormat(t *testing.T) {
	for _, tc := range []struct {
		e    rawEvent
		text string
		json string
	}{
		{rawEvent{TimeNs: 1_234_567_890, Device: 3, Kind: "button", Index: 4, Value: 1}, `1.234567 3 raw button 4 down`, `{"timeNs":1234567890,"kind":"raw","device":3,"rawKind":"button","index":4,"value":1}`},
		{rawEvent{TimeNs: 5, Device: 3, Kind: "button", Index: 4, Value: 0}, `0.000000 3 raw button 4 up`, `{"timeNs":5,"kind":"raw","device":3,"rawKind":"button","index":4,"value":0}`},
		{rawEvent{TimeNs: 2_000_001_000, Device: 7, Kind: "axis", Index: 2, Value: -32768}, `2.000001 7 raw axis 2 -32768`, `{"timeNs":2000001000,"kind":"raw","device":7,"rawKind":"axis","index":2,"value":-32768}`},
		{rawEvent{TimeNs: 0, Device: 7, Kind: "hat", Index: 0, Value: 9}, `0.000000 7 raw hat 0 9`, `{"timeNs":0,"kind":"raw","device":7,"rawKind":"hat","index":0,"value":9}`},
	} {
		if got := formatRawEvent(tc.e); got != tc.text {
			t.Errorf("formatRawEvent(%+v) = %q, want %q", tc.e, got, tc.text)
		}
		var text, js bytes.Buffer
		if shown, err := newEventPrinter(&text, monitorOptions{}).printRaw(tc.e); err != nil || !shown || text.String() != tc.text+"\n" {
			t.Errorf("printRaw text = %q, %v, %v", text.String(), shown, err)
		}
		if shown, err := newEventPrinter(&js, monitorOptions{json: true}).printRaw(tc.e); err != nil || !shown || js.String() != tc.json+"\n" {
			t.Errorf("printRaw json = %q, %v, %v; want %q", js.String(), shown, err, tc.json)
		}
	}
	// The deadband hides small moves of an axis, not buttons.
	var out bytes.Buffer
	p := newEventPrinter(&out, monitorOptions{deadband: 100})
	for _, v := range []int16{0, 50, 120, 130, 400} {
		if _, err := p.printRaw(rawEvent{Kind: "axis", Index: 1, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.printRaw(rawEvent{Kind: "button", Index: 1, Value: 1}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(out.String(), "\n"); got != 4 {
		t.Errorf("deadband printed %d lines, want 4 (0, 120, 400 and the button):\n%s", got, &out)
	}
}

func TestMonitorRemappedLine(t *testing.T) {
	e := input.Event{Time: 3_000_000_000, Device: 5, Kind: input.DeviceRemapped, Info: &input.DeviceInfo{ID: 5, ButtonMask: 0x853, AxisMask: 3}}
	if got, want := formatMonitorEvent(e, ""), "3.000000 ~device 5 remapped buttons=0x853 axes=0x3"; got != want {
		t.Errorf("remapped line = %q, want %q", got, want)
	}
	var text, js, list bytes.Buffer
	if shown, err := newEventPrinter(&text, monitorOptions{}).print(e); err != nil || !shown {
		t.Errorf("text: shown %v, %v", shown, err)
	}
	// A JSON line would not read back as a trace record, and --list shows added only.
	if shown, err := newEventPrinter(&js, monitorOptions{json: true}).print(e); err != nil || shown || js.Len() != 0 {
		t.Errorf("json: shown %v, %v, %q", shown, err, js.String())
	}
	if shown, err := newEventPrinter(&list, monitorOptions{list: true}).print(e); err != nil || shown || list.Len() != 0 {
		t.Errorf("list: shown %v, %v, %q", shown, err, list.String())
	}
}

// TestMonitorRawDirect watches a virtual joystick that SDL has no gamepad
// mapping for: its elements are printed by index, and a device that was not
// asked for (--device) is not.
func TestMonitorRawDirect(t *testing.T) {
	s, err := input.Open(input.Options{Library: inputtest.Library(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	a, err := s.AttachRawVirtual(input.RawJoystickDesc{Name: "1st monitor raw", Axes: 2, Buttons: 4, Hats: 1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AttachRawVirtual(input.RawJoystickDesc{Name: "2nd monitor raw", Axes: 2, Buttons: 4, Hats: 1})
	if err != nil {
		t.Fatal(err)
	}
	opts := monitorOptions{raw: true, rawDevice: a.ID(), duration: 1500 * time.Millisecond}
	var out, errOut bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- watch(context.Background(), s, opts, &out, &errOut) }()
	// Once the monitor has asked for device a, so has this call, which is the same.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := s.RawStart(a.ID()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("device a never appeared")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // the monitor's own RawStart is idempotent
	for _, f := range []func() error{
		func() error { return a.SetRawButton(2, true) },
		func() error { return a.SetRawButton(2, false) },
		func() error { return a.SetRawAxis(1, 20000) }, // SDL sends the rest value first
		func() error { return a.SetRawHat(0, 4) },
		func() error { return b.SetRawButton(1, true) },
	} {
		if err := f(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(15 * time.Millisecond)
	}
	if code := <-done; code != 0 {
		t.Fatalf("watch = %d, stderr %q", code, &errOut)
	}
	lines := strings.Split(out.String(), "\n")
	var raw []string
	for _, l := range lines {
		if strings.Contains(l, " raw ") {
			f := strings.Fields(l)
			raw = append(raw, strings.Join(f[1:], " "))
		}
	}
	id := fmt.Sprint(a.ID())
	want := []string{
		id + " raw button 2 down", id + " raw button 2 up", id + " raw axis 1 0", id + " raw axis 1 20000", id + " raw hat 0 4",
	}
	if strings.Join(raw, "|") != strings.Join(want, "|") {
		t.Errorf("raw lines %q, want %q (device b's must not be there)\n%s", raw, want, &out)
	}
	if !strings.Contains(out.String(), "+device "+id+" ") || !strings.Contains(out.String(), "joystick-only") {
		t.Errorf("the device line is missing:\n%s", &out)
	}
}

// TestPrinterRawAndGamepadAxesRunTogether: in direct mode the raw events are
// printed from another goroutine than the gamepad events, and --deadband
// makes both remember the last value of their axes. A gamepad with raw events
// on sends both for every move, so the printer must be safe for the two.
func TestPrinterRawAndGamepadAxesRunTogether(t *testing.T) {
	p := newEventPrinter(&lockedWriter{w: &bytes.Buffer{}}, monitorOptions{deadband: 100})
	var wg sync.WaitGroup
	end := time.Now().Add(300 * time.Millisecond)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; time.Now().Before(end); i++ {
			v := int16(20000)
			if i%2 == 0 {
				v = -20000
			}
			_, _ = p.print(input.Event{Kind: input.AxisMotion, Device: 1, Control: "leftx", Value: v})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; time.Now().Before(end); i++ {
			v := int16(20000)
			if i%2 == 0 {
				v = -20000
			}
			_, _ = p.printRaw(rawEvent{Device: 1, Kind: "axis", Index: uint8(i % 4), Value: v})
		}
	}()
	wg.Wait()
}

// TestRawSkipped: a device the raw events of which cannot be started is left
// out with a line on stderr when all devices are watched, and is an error when
// it was named, or when the failure is not the device's.
func TestRawSkipped(t *testing.T) {
	gone := fmt.Errorf("raw events of device 3: %w", input.ErrNoDevice)
	closed := fmt.Errorf("raw events of device 3: %w", input.ErrClosed)
	deviceFailure := func(err error) bool { return !errors.Is(err, input.ErrClosed) }
	for _, tc := range []struct {
		name    string
		opts    monitorOptions
		err     error
		skipped bool
	}{
		{"none", monitorOptions{}, nil, true},
		{"one of all", monitorOptions{}, gone, true},
		{"named", monitorOptions{rawDevice: 3}, gone, false},
		{"source closed", monitorOptions{}, closed, false},
	} {
		var stderr bytes.Buffer
		err := tc.opts.rawSkipped(&stderr, 3, tc.err, deviceFailure)
		switch {
		case tc.skipped && err != nil:
			t.Errorf("%s: err = %v, want nil", tc.name, err)
		case !tc.skipped && !errors.Is(err, tc.err):
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.err)
		}
		if said := stderr.Len() > 0; said != (tc.skipped && tc.err != nil) {
			t.Errorf("%s: stderr %q", tc.name, &stderr)
		}
		if tc.skipped && tc.err != nil && !strings.Contains(stderr.String(), "device 3") {
			t.Errorf("%s: stderr %q does not name the device", tc.name, &stderr)
		}
	}
}
