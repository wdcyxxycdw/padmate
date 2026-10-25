package input_test

import (
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
)

// These tests are the raw joystick stream (IN-17, ADR-0072): the elements of
// a joystick as SDL reads them, for the wizard and "padmatectl monitor
// --raw". Only a device that was asked for sends them, they never reach
// Events, and they are not the events that slots or the combo watch see.

// rawUntil returns the first raw event that match accepts, within five
// seconds.
func rawUntil(t *testing.T, s *input.Source, what string, match func(input.RawEvent) bool) input.RawEvent {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-s.RawEvents():
			if !ok {
				t.Fatalf("RawEvents closed while waiting for %s", what)
			}
			if match(e) {
				return e
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// noRaw fails when a raw event comes within d.
func noRaw(t *testing.T, s *input.Source, d time.Duration) {
	t.Helper()
	select {
	case e := <-s.RawEvents():
		t.Fatalf("unexpected raw event %+v", e)
	case <-time.After(d):
	}
}

func TestRawStreamDeliversTheWatchedJoystick(t *testing.T) {
	s := open(t, nil)
	v, _ := attachRaw(t, s, "padmate raw stream", 3, 6, 1, -32768)
	// Nothing is sent before the device is asked for.
	if err := v.SetRawButton(2, true); err != nil {
		t.Fatal(err)
	}
	noRaw(t, s, 50*time.Millisecond)
	if err := v.SetRawButton(2, false); err != nil {
		t.Fatal(err)
	}

	info, err := s.RawStart(v.ID())
	if err != nil {
		t.Fatalf("RawStart: %v", err)
	}
	if info.Device != v.ID() || info.Axes != 3 || info.Buttons != 6 || info.Hats != 1 || info.Gamepad {
		t.Errorf("RawInfo = %+v, want 3 axes, 6 buttons and 1 hat of a joystick", info)
	}
	if len(info.AxisRest) != 3 || info.AxisRest[0] != -32768 || info.AxisRest[1] != 0 {
		t.Errorf("AxisRest = %v, want [-32768 0 0]", info.AxisRest)
	}
	if again, err := s.RawStart(v.ID()); err != nil || again.Axes != 3 {
		t.Errorf("RawStart again = %+v, %v; it is idempotent", again, err)
	}

	if err := v.SetRawButton(2, true); err != nil {
		t.Fatal(err)
	}
	if e := rawUntil(t, s, "the press", func(e input.RawEvent) bool { return e.Kind == input.RawButton }); e.Device != v.ID() || e.Index != 2 || e.Value != 1 || e.Time == 0 {
		t.Errorf("press = %+v", e)
	}
	if err := v.SetRawButton(2, false); err != nil {
		t.Fatal(err)
	}
	if e := rawUntil(t, s, "the release", func(e input.RawEvent) bool { return e.Kind == input.RawButton }); e.Index != 2 || e.Value != 0 {
		t.Errorf("release = %+v", e)
	}
	if err := v.SetRawHat(0, 4); err != nil {
		t.Fatal(err)
	}
	if e := rawUntil(t, s, "the hat", func(e input.RawEvent) bool { return e.Kind == input.RawHat }); e.Index != 0 || e.Value != 4 {
		t.Errorf("hat = %+v, want hat 0 down (4)", e)
	}
	if err := v.SetRawAxis(1, 12345); err != nil {
		t.Fatal(err)
	}
	if e := rawUntil(t, s, "the axis", func(e input.RawEvent) bool { return e.Kind == input.RawAxis && e.Index == 1 }); e.Value != 12345 {
		t.Errorf("axis = %+v", e)
	}
	// None of it is an event of a gamepad.
	noEvent(t, s, v.ID())

	if err := s.RawStop(v.ID()); err != nil {
		t.Fatal(err)
	}
	if err := v.SetRawButton(3, true); err != nil {
		t.Fatal(err)
	}
	noRaw(t, s, 50*time.Millisecond)
}

// TestRawStreamOfAGamepadDoesNotTakeAnythingFromEvents: a recognized gamepad
// can be asked for as well (the wizard remaps one); its Events stay as they
// are.
func TestRawStreamOfAGamepadDoesNotTakeAnythingFromEvents(t *testing.T) {
	s := open(t, nil)
	v, _ := attach(t, s, "padmate raw gamepad")
	info, err := s.RawStart(v.ID())
	if err != nil || !info.Gamepad || info.Buttons == 0 || info.Axes == 0 {
		t.Fatalf("RawStart = %+v, %v", info, err)
	}
	if err := v.SetButton("south", true); err != nil {
		t.Fatal(err)
	}
	if e := until(t, s, v.ID(), "the press", isKind(input.ButtonDown)); e.Control != "south" {
		t.Errorf("press = %+v", e)
	}
	if e := rawUntil(t, s, "the raw press", func(e input.RawEvent) bool { return e.Kind == input.RawButton }); e.Index != 0 || e.Value != 1 {
		t.Errorf("raw press = %+v, want joystick button 0", e)
	}
}

// TestRawStreamOnlyOfTheAskedDevice: a device that was not asked for sends
// nothing.
func TestRawStreamOnlyOfTheAskedDevice(t *testing.T) {
	s := open(t, nil)
	a, _ := attachRaw(t, s, "1st raw stream", 1, 4, 0)
	b, _ := attachRaw(t, s, "2nd raw stream", 1, 4, 0)
	if _, err := s.RawStart(a.ID()); err != nil {
		t.Fatal(err)
	}
	if err := b.SetRawButton(1, true); err != nil {
		t.Fatal(err)
	}
	noRaw(t, s, 50*time.Millisecond)
	if err := a.SetRawButton(1, true); err != nil {
		t.Fatal(err)
	}
	if e := rawUntil(t, s, "pad A's press", func(e input.RawEvent) bool { return true }); e.Device != a.ID() {
		t.Errorf("raw event of device %d, want %d", e.Device, a.ID())
	}
}

// TestRawAxisRateLimit: an axis that moves at once, over and over, sends a
// bounded number of events, and the last value always gets through: the
// wizard needs to see the axis come back.
func TestRawAxisFloodWithSDL(t *testing.T) {
	s := open(t, nil)
	v, _ := attachRaw(t, s, "padmate raw flood", 1, 1, 0)
	if _, err := s.RawStart(v.ID()); err != nil {
		t.Fatal(err)
	}
	const sets = 400
	start := time.Now()
	for i := range sets {
		if err := v.SetRawAxis(0, int16(1000+i*10)); err != nil {
			t.Fatal(err)
		}
	}
	took := time.Since(start)
	last := int16(1000 + (sets-1)*10)
	var got, final int
	deadline := time.After(2 * time.Second)
collect:
	for {
		select {
		case e := <-s.RawEvents():
			got++
			if e.Kind == input.RawAxis && e.Value == last {
				final++
				break collect
			}
		case <-deadline:
			t.Fatalf("the last value %d never came; %d events", last, got)
		}
	}
	// One event per 8 ms of the run at most, plus a few for the edges.
	if limit := int(took/(8*time.Millisecond)) + 5; got > limit {
		t.Errorf("%d raw events for %d sets in %v, want at most %d", got, sets, took, limit)
	}
	if final != 1 {
		t.Error("the last value did not arrive")
	}
}

func TestRawStartErrors(t *testing.T) {
	s := open(t, nil)
	if _, err := s.RawStart(input.DeviceID(987654)); err == nil {
		t.Error("RawStart of an unknown device succeeded")
	}
	if err := s.RawStop(input.DeviceID(987654)); err != nil {
		t.Errorf("RawStop of an unknown device = %v, want nil", err)
	}
	v, _ := attachRaw(t, s, "padmate raw gone", 1, 1, 0)
	if _, err := s.RawStart(v.ID()); err != nil {
		t.Fatal(err)
	}
	if err := v.Detach(); err != nil {
		t.Fatal(err)
	}
	until(t, s, v.ID(), "the removal", isKind(input.DeviceRemoved))
	if _, err := s.RawStart(v.ID()); err == nil {
		t.Error("RawStart of a removed device succeeded")
	}
}

func TestRawEventsCloseWithTheSource(t *testing.T) {
	s, err := input.Open(input.Options{Library: inputtest.Library(t)})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	select {
	case _, ok := <-s.RawEvents():
		if ok {
			t.Error("a raw event after Close")
		}
	case <-time.After(time.Second):
		t.Error("RawEvents is not closed after Close")
	}
	if _, err := s.RawStart(1); err == nil {
		t.Error("RawStart after Close succeeded")
	}
}
