package input_test

import (
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
)

// These tests need the SDL library, as those of source_test.go do. They
// hold the pause combo (DMN-07) on virtual gamepads.

// comboHold is the hold time of the combos in these tests.
const comboHold = 50 * time.Millisecond

// openCombo opens a Source with combo detection and hints, nil for the
// default preset, and closes it at the end of the test. The channel gets a
// value for each OnCombo call.
func openCombo(t *testing.T, hints []input.Hint) (*input.Source, <-chan struct{}) {
	t.Helper()
	fired := make(chan struct{}, 16)
	s, err := input.Open(input.Options{
		Library: inputtest.Library(t),
		Hints:   hints,
		OnCombo: func() {
			select {
			case fired <- struct{}{}:
			default: // more calls than any test makes; calledOnce reports it
			}
		},
		ComboHold: comboHold,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	return s, fired
}

// set presses the buttons of v, or releases them, in order.
func set(t *testing.T, v *input.Virtual, down bool, buttons ...string) {
	t.Helper()
	for _, b := range buttons {
		if err := v.SetButton(b, down); err != nil {
			t.Fatalf("SetButton(%q, %v): %v", b, down, err)
		}
	}
}

// expect returns the next events of device id, which must be want as
// format renders them.
func expect(t *testing.T, s *input.Source, id input.DeviceID, want ...string) []input.Event {
	t.Helper()
	evs := collect(t, s, id, len(want))
	got := make([]string, len(evs))
	for i, e := range evs {
		got[i] = format(e)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("events:\n got %q\nwant %q", got, want)
	}
	return evs
}

// calledOnce checks, without waiting, that OnCombo has been called once
// since the last check: T-input calls it before it sends the ComboHeld.
func calledOnce(t *testing.T, fired <-chan struct{}) {
	t.Helper()
	select {
	case <-fired:
	default:
		t.Error("OnCombo was not called before the ComboHeld arrived")
	}
	select {
	case <-fired:
		t.Error("OnCombo was called again")
	default:
	}
}

// quiet checks that no event of device id arrives and OnCombo is not
// called for d.
func quiet(t *testing.T, s *input.Source, id input.DeviceID, fired <-chan struct{}, d time.Duration) {
	t.Helper()
	timeout := time.After(d)
	for {
		select {
		case e, ok := <-s.Events():
			if !ok {
				t.Fatal("Events closed")
			}
			if e.Device == id {
				t.Errorf("unexpected event %+v", e)
			}
		case <-fired:
			t.Error("OnCombo was called")
		case <-timeout:
			return
		}
	}
}

// TestComboVirtual holds back+start on a virtual gamepad: T-input calls
// OnCombo and sends a ComboHeld at the hold time after the down of the
// last member, once while they stay held, and again after a member is
// pressed anew. A member released before the hold time disarms the combo.
func TestComboVirtual(t *testing.T) {
	s, fired := openCombo(t, nil)
	v, _ := attach(t, s, "padmate combo pad")
	id := v.ID()
	set(t, v, true, "back", "start")
	evs := expect(t, s, id, "back down", "start down", "combo")
	calledOnce(t, fired)
	if due := evs[1].Time + uint64(comboHold); evs[2].Time != due {
		t.Errorf("ComboHeld at %d ns, want %d: the start down's time plus the hold", evs[2].Time, due)
	}
	quiet(t, s, id, fired, 3*comboHold)

	set(t, v, false, "start")
	set(t, v, true, "start")
	evs = expect(t, s, id, "start up", "start down", "combo")
	calledOnce(t, fired)
	if due := evs[1].Time + uint64(comboHold); evs[2].Time != due {
		t.Errorf("second ComboHeld at %d ns, want %d", evs[2].Time, due)
	}

	// back down arms the combo; start up disarms it, unless it came after
	// the due time, which a slow machine may make it: the combo then fires
	// before it. The event times, which SDL takes in SetButton, decide.
	set(t, v, false, "back")
	expect(t, s, id, "back up")
	set(t, v, true, "back")
	set(t, v, false, "start")
	evs = collect(t, s, id, 2)
	if format(evs[0]) != "back down" {
		t.Fatalf("event %q, want back down", format(evs[0]))
	}
	due := evs[0].Time + uint64(comboHold)
	switch format(evs[1]) {
	case "start up":
		if evs[1].Time >= due {
			t.Errorf("start up at %d ns, after the due time %d, and no ComboHeld before it", evs[1].Time, due)
		}
		quiet(t, s, id, fired, 3*comboHold)
	case "combo":
		calledOnce(t, fired)
		up := expect(t, s, id, "start up")[0]
		if evs[1].Time != due || up.Time < due {
			t.Errorf("ComboHeld at %d ns and start up at %d, want the due time %d and not before it", evs[1].Time, up.Time, due)
		}
	default:
		t.Fatalf("event %q, want start up or combo", format(evs[1]))
	}
}

// withButton returns d with button b too.
func withButton(d input.VirtualDesc, b string) input.VirtualDesc {
	d.Buttons = append(slices.Clone(d.Buttons), b)
	return d
}

// TestComboVirtualJoyCon holds combos on virtual Joy-Cons, whose USB ids
// make SDL report them as single Joy-Cons: SL+SR fires, as leftshoulder
// and rightshoulder when they are held sideways, and as their paddles when
// SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS is in effect, whether Options or
// the environment sets it. back+start, which a Joy-Con pair has, does not.
func TestComboVirtualJoyCon(t *testing.T) {
	inputtest.Unsetenv(t, input.HintVerticalJoyCons)
	vertical := []input.Hint{{Name: input.HintVerticalJoyCons, Value: "1"}}
	for _, tc := range []struct {
		name  string
		desc  input.VirtualDesc
		hints []input.Hint
		env   string // the hint in the environment; "" leaves it unset
		typ   string
		combo []string
		not   []string
	}{
		{"left sideways", withButton(inputtest.JoyConLeft("padmate combo Joy-Con (L)"), "back"), nil, "", "joyconleft",
			[]string{"leftshoulder", "rightshoulder"}, []string{"back", "start"}},
		{"right sideways", inputtest.JoyConRight("padmate combo Joy-Con (R)"), nil, "", "joyconright",
			[]string{"rightshoulder", "leftshoulder"}, []string{"paddle1", "paddle3"}},
		{"left vertical", inputtest.JoyConLeft("padmate combo Joy-Con (L)"), vertical, "", "joyconleft",
			[]string{"paddle4", "paddle2"}, []string{"leftshoulder", "rightshoulder"}},
		{"right vertical", inputtest.JoyConRight("padmate combo Joy-Con (R)"), vertical, "", "joyconright",
			[]string{"paddle3", "paddle1"}, []string{"leftshoulder", "rightshoulder"}},
		{"left vertical from the environment", inputtest.JoyConLeft("padmate combo Joy-Con (L)"), nil, "true", "joyconleft",
			[]string{"paddle2", "paddle4"}, []string{"leftshoulder", "rightshoulder"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv(input.HintVerticalJoyCons, tc.env)
			}
			s, fired := openCombo(t, tc.hints)
			v, err := s.AttachVirtualDesc(tc.desc)
			if err != nil {
				t.Fatalf("AttachVirtualDesc: %v", err)
			}
			id := v.ID()
			added := collect(t, s, id, 1)[0]
			if added.Kind != input.DeviceAdded || added.Info == nil || added.Info.Type != tc.typ {
				t.Fatalf("first event %+v, want DeviceAdded of a %s", added, tc.typ)
			}

			set(t, v, true, tc.not...)
			expect(t, s, id, tc.not[0]+" down", tc.not[1]+" down")
			quiet(t, s, id, fired, 3*comboHold)
			set(t, v, false, tc.not...)
			expect(t, s, id, tc.not[0]+" up", tc.not[1]+" up")

			set(t, v, true, tc.combo...)
			expect(t, s, id, tc.combo[0]+" down", tc.combo[1]+" down", "combo")
			calledOnce(t, fired)
		})
	}
}

// TestComboQueueFull holds back+start while Events is full, as when the
// consumer stops reading: T-input drops the button events, but its combo
// watch saw them, so it still calls OnCombo, and the ComboHeld waits in
// line and arrives once the consumer reads again. Dropped counts only the
// button events.
func TestComboQueueFull(t *testing.T) {
	// Dropped is global to the Source. Physical axes may report jitter even
	// when the owner is not touching a connected controller; keep this exact
	// queue accounting test on SDL's virtual backend only.
	var hints []input.Hint
	for _, name := range input.BackendHints {
		inputtest.Unsetenv(t, name)
		hints = append(hints, input.Hint{Name: name, Value: "0"})
	}
	s, fired := openCombo(t, hints)
	v, _ := attach(t, s, "padmate combo pad")
	id := v.ID()
	if n, err := s.FillEvents(); err != nil || n == 0 {
		t.Fatalf("FillEvents = %d, %v; want some fillers", n, err)
	}
	before := s.Dropped()
	set(t, v, true, "back", "start")
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("OnCombo was not called while Events was full")
	}
	// Only the virtual device contributes to this count.
	if n := s.Dropped() - before; n != 2 {
		t.Errorf("dropped %d events, want 2: the downs of back and start", n)
	}
	expect(t, s, id, "combo") // after the fillers, which collect skips
	set(t, v, false, "back", "start")
	expect(t, s, id, "back up", "start up")
	if n := s.Dropped() - before; n != 2 {
		t.Errorf("dropped %d events in all, want 2", n)
	}
	select {
	case <-fired:
		t.Error("OnCombo was called again")
	default:
	}
}

// TestComboRepickVirtual changes the combo that ComboFor gives while a
// virtual gamepad holds the old one, and calls RepickCombos (UI-19): the
// old combo no longer fires, and the new one fires once its members are
// pressed after the change, without reopening the Source.
func TestComboRepickVirtual(t *testing.T) {
	var current atomic.Pointer[input.Combo]
	current.Store(&input.Combo{Buttons: []string{"back", "start"}, Hold: 10 * comboHold})
	fired := make(chan struct{}, 16)
	s, err := input.Open(input.Options{
		Library: inputtest.Library(t),
		OnCombo: func() {
			select {
			case fired <- struct{}{}:
			default:
			}
		},
		ComboFor: func(input.DeviceInfo, bool) input.Combo { return *current.Load() },
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	if s.VerticalJoyCons() {
		t.Skip(input.HintVerticalJoyCons + " is set in the environment")
	}
	v, _ := attach(t, s, "padmate combo pad")
	id := v.ID()
	set(t, v, true, "back", "start")
	expect(t, s, id, "back down", "start down")
	current.Store(&input.Combo{Buttons: []string{"west", "east"}, Hold: comboHold})
	if err := s.RepickCombos(); err != nil {
		t.Fatalf("RepickCombos: %v", err)
	}
	// The old combo, armed for 10 holds, never fires.
	quiet(t, s, id, fired, 12*comboHold)
	set(t, v, true, "west", "east")
	expect(t, s, id, "west down", "east down", "combo")
	calledOnce(t, fired)
	s.Close()
	if err := s.RepickCombos(); !errors.Is(err, input.ErrClosed) {
		t.Errorf("RepickCombos after Close = %v, want ErrClosed", err)
	}
}
