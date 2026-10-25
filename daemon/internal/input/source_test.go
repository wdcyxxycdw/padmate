package input_test

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zyko0/go-sdl3/sdl"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
	"github.com/wdcyxxycdw/padmate/daemon/internal/sdlpin"
)

// These tests need the SDL library (inputtest.Library). They drive an SDL
// virtual gamepad, so they need no hardware; events of real devices are
// ignored.

// open opens a Source with hints, nil for the default preset, and closes
// it at the end of the test.
func open(t *testing.T, hints []input.Hint) *input.Source {
	t.Helper()
	s, err := input.Open(input.Options{Library: inputtest.Library(t), Hints: hints})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// attach adds a virtual gamepad and returns it with its DeviceAdded event,
// which AttachVirtual queues before it returns.
func attach(t *testing.T, s *input.Source, name string) (*input.Virtual, input.Event) {
	t.Helper()
	v, err := s.AttachVirtual(name)
	if err != nil {
		t.Fatalf("AttachVirtual: %v", err)
	}
	evs := collect(t, s, v.ID(), 1)
	if evs[0].Kind != input.DeviceAdded {
		t.Fatalf("first event %+v, want DeviceAdded", evs[0])
	}
	return v, evs[0]
}

// collect returns the next n events of device id.
func collect(t *testing.T, s *input.Source, id input.DeviceID, n int) []input.Event {
	t.Helper()
	var evs []input.Event
	timeout := time.After(5 * time.Second)
	for len(evs) < n {
		select {
		case e, ok := <-s.Events():
			if !ok {
				t.Fatalf("Events closed after %d of %d events: %v", len(evs), n, evs)
			}
			if e.Device == id {
				evs = append(evs, e)
			}
		case <-timeout:
			t.Fatalf("timed out after %d of %d events: %v", len(evs), n, evs)
		}
	}
	return evs
}

// waitClosed waits for the Events channel to be closed, discarding
// events.
func waitClosed(t *testing.T, s *input.Source) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-s.Events():
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("Events is not closed")
		}
	}
}

// format renders an event without its time, for comparison.
func format(e input.Event) string {
	switch e.Kind {
	case input.ButtonDown, input.ButtonUp:
		return e.Control + " " + e.Kind.String()
	case input.AxisMotion:
		return fmt.Sprintf("%s %d", e.Control, e.Value)
	default:
		return e.Kind.String()
	}
}

func TestOpenCloseTwice(t *testing.T) {
	lib := inputtest.Library(t)
	for i := range 2 {
		s, err := input.Open(input.Options{Library: lib})
		if err != nil {
			t.Fatalf("Open #%d: %v", i+1, err)
		}
		if runtime.GOOS == "windows" && s.Version() != sdlpin.Version {
			t.Errorf("Version() = %q, want %q", s.Version(), sdlpin.Version)
		}
		if s.Library() != lib {
			t.Errorf("Library() = %q, want %q", s.Library(), lib)
		}
		if (s.ThreadID() != 0) != (runtime.GOOS == "windows") {
			t.Errorf("ThreadID() = %d on %s", s.ThreadID(), runtime.GOOS)
		}
		if _, err := input.Open(input.Options{Library: lib}); !errors.Is(err, input.ErrActive) {
			t.Errorf("a second Open = %v, want ErrActive", err)
		}
		s.Close()
		s.Close() // idempotent
		waitClosed(t, s)
		if _, err := s.AttachVirtual("closed"); !errors.Is(err, input.ErrClosed) {
			t.Errorf("AttachVirtual after Close = %v, want ErrClosed", err)
		}
	}
}

// TestHints checks the hints read back after SDL_Init for each preset:
// the three hints Open always sets first, then the preset's, then the
// other backend hints and the Joy-Con hints, each once. Joy-Cons are not
// combined, and the grip is left to SDL (IN-07).
func TestHints(t *testing.T) {
	lib := inputtest.Library(t)
	inputtest.Unsetenv(t, input.HintCombineJoyCons, input.HintVerticalJoyCons)
	first := []input.Hint{
		{Name: input.HintBackgroundEvents, Value: "1"},
		{Name: input.HintNoSignalHandlers, Value: "1"},
		{Name: input.HintCombineJoyCons, Value: "0"},
	}
	for _, name := range input.PresetNames() {
		hints, _ := input.PresetHints(name)
		s, err := input.Open(input.Options{Library: lib, Hints: hints})
		if err != nil {
			t.Fatalf("%s: Open: %v", name, err)
		}
		got := s.Hints()
		s.Close()
		if len(got) < len(first) || !slices.Equal(got[:len(first)], first) {
			t.Errorf("%s: Hints() = %v, want it to start with %v", name, got, first)
		}
		want := make(map[string]string)
		for _, h := range hints {
			want[h.Name] = h.Value
		}
		want[input.HintCombineJoyCons] = "0"
		want[input.HintVerticalJoyCons] = ""
		seen := make(map[string]bool)
		for _, h := range got {
			if seen[h.Name] {
				t.Errorf("%s: %s read back twice", name, h.Name)
			}
			seen[h.Name] = true
			if v, ok := want[h.Name]; ok && h.Value != v {
				t.Errorf("%s: %s, want %q", name, h, v)
			}
		}
		for n := range want {
			if !seen[n] {
				t.Errorf("%s: %s is not read back", name, n)
			}
		}
		for _, n := range slices.Concat(input.BackendHints, input.JoyConHints) {
			if !seen[n] {
				t.Errorf("%s: %s is not read back", name, n)
			}
		}
	}
}

// TestHintOverride pins the only way to override a Joy-Con default: a
// hint of Options, which Open sets after openHints, wins because SDL keeps
// the value set last. A hint of openHints is read back once, where Open
// set it first.
func TestHintOverride(t *testing.T) {
	inputtest.Unsetenv(t, input.HintCombineJoyCons, input.HintVerticalJoyCons)
	for _, tc := range []struct {
		name  string
		hint  input.Hint
		index int // where Hints has it; -1 for anywhere
	}{
		{"combine", input.Hint{Name: input.HintCombineJoyCons, Value: "1"}, 2},
		{"vertical", input.Hint{Name: input.HintVerticalJoyCons, Value: "1"}, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hints, _ := input.PresetHints("default")
			got := open(t, append(hints, tc.hint)).Hints()
			var at []int
			for i, h := range got {
				if h.Name == tc.hint.Name {
					at = append(at, i)
				}
			}
			if len(at) != 1 || got[at[0]] != tc.hint || tc.index >= 0 && at[0] != tc.index {
				t.Errorf("Hints() = %v, want %s once (at index %d)", got, tc.hint, tc.index)
			}
		})
	}
}

// TestHintFromEnvironment sets a hint in the environment that padmate sets
// differently: a backend hint of the preset, or a hint that Open always
// sets. SDL gives the environment priority, and SDL_SetHint fails for such
// a hint: Open still succeeds, and Hints reports the environment's value.
func TestHintFromEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name   string
		preset string
		env    input.Hint // in the environment
		// set is the value that the preset sets, or "" when Open sets the
		// hint instead (TestHints checks that value).
		set string
	}{
		{"backend", "xinput", input.Hint{Name: "SDL_JOYSTICK_HIDAPI", Value: "1"}, "0"},
		{"joycon", "default", input.Hint{Name: input.HintCombineJoyCons, Value: "1"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.env.Name, tc.env.Value)
			hints, _ := input.PresetHints(tc.preset)
			if tc.set != "" && !slices.Contains(hints, input.Hint{Name: tc.env.Name, Value: tc.set}) {
				t.Fatalf("the %s preset %v does not set %s=%s", tc.preset, hints, tc.env.Name, tc.set)
			}
			if got := open(t, hints).Hints(); !slices.Contains(got, tc.env) {
				t.Errorf("Hints() = %v, want %s from the environment", got, tc.env)
			}
		})
	}
}

// TestVirtualEvents drives a virtual gamepad and checks its events, in
// order.
func TestVirtualEvents(t *testing.T) {
	s := open(t, nil)
	v, added := attach(t, s, "padmate test pad")

	info := added.Info
	if info == nil {
		t.Fatal("DeviceAdded without Info")
	}
	if info.ID != v.ID() || info.Name != "padmate test pad" || !info.IsGamepad || !info.Virtual {
		t.Errorf("Info = %+v, want the virtual gamepad %d", *info, v.ID())
	}
	if info.Type == "" || info.Type == "unknown" {
		t.Errorf("Info.Type = %q, want a gamepad type", info.Type)
	}
	// SDL_virtualjoystick.c: bus 0xFF, driver signature 'v', the joystick
	// type (1, gamepad) as driver data.
	if info.Driver != "virtual" || !strings.HasPrefix(info.GUID, "ff00") || !strings.HasSuffix(info.GUID, "7601") {
		t.Errorf("Info.Driver = %q, GUID = %q; want virtual, ff00…7601", info.Driver, info.GUID)
	}

	steps := []struct {
		set  func() error
		want string
	}{
		{func() error { return v.SetButton("south", true) }, "south down"},
		{func() error { return v.SetButton("south", false) }, "south up"},
		{func() error { return v.SetAxis("leftx", 32767) }, "leftx 32767"},
		{func() error { return v.SetAxis("leftx", -32768) }, "leftx -32768"},
		{func() error { return v.SetAxis("leftx", 0) }, "leftx 0"},
		{func() error { return v.SetAxis("lefttrigger", 32767) }, "lefttrigger 32767"},
		{func() error { return v.SetAxis("lefttrigger", 0) }, "lefttrigger 0"},
		{func() error { return v.SetButton("dpad.up", true) }, "dpad.up down"},
		{func() error { return v.SetButton("paddle1", true) }, "paddle1 down"},
		{func() error { return v.SetButton("paddle1", false) }, "paddle1 up"},
		{func() error { return v.SetButton("dpad.up", false) }, "dpad.up up"},
		{func() error { return v.SetAxis("righttrigger", 16384) }, "righttrigger 16384"},
		{func() error { return v.SetAxis("righttrigger", 0) }, "righttrigger 0"},
		{func() error { return v.SetAxis("righty", -1) }, "righty -1"},
		{func() error { return v.SetAxis("righty", 0) }, "righty 0"},
	}
	want := make([]string, 0, len(steps)+1)
	for _, st := range steps {
		if err := st.set(); err != nil {
			t.Fatalf("%s: %v", st.want, err)
		}
		want = append(want, st.want)
	}
	// Setting a value that does not change anything sends no event.
	if err := v.SetAxis("leftx", 0); err != nil {
		t.Fatal(err)
	}
	if err := v.Detach(); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	want = append(want, "removed")

	evs := collect(t, s, v.ID(), len(want))
	got := make([]string, len(evs))
	for i, e := range evs {
		got[i] = format(e)
		if i > 0 && e.Time < evs[i-1].Time {
			t.Errorf("event %d (%s) at %d is before the previous one at %d", i, got[i], e.Time, evs[i-1].Time)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("events:\n got %q\nwant %q", got, want)
	}
	if n := s.Dropped(); n != 0 {
		t.Errorf("Dropped() = %d", n)
	}

	// Nothing else arrives for the device.
	select {
	case e := <-s.Events():
		if e.Device == v.ID() {
			t.Errorf("event after the removal: %+v", e)
		}
	case <-time.After(50 * time.Millisecond):
	}
	if err := v.SetButton("south", true); err == nil {
		t.Error("SetButton after Detach succeeded")
	}
	if err := v.Detach(); err != nil {
		t.Errorf("a second Detach = %v", err)
	}
}

// TestDetachReleases detaches a virtual gamepad with a button held and a
// stick moved: SDL releases and recenters them before it reports the
// removal (SDL_PrivateJoystickForceRecentering).
func TestDetachReleases(t *testing.T) {
	s := open(t, nil)
	v, _ := attach(t, s, "padmate test pad")
	for _, err := range []error{
		v.SetButton("south", true),
		v.SetAxis("leftx", 16384),
		v.SetAxis("lefttrigger", 32767),
		v.Detach(),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"south down", "leftx 16384", "lefttrigger 32767", "leftx 0", "lefttrigger 0", "south up", "removed"}
	evs := collect(t, s, v.ID(), len(want))
	got := make([]string, len(evs))
	for i, e := range evs {
		got[i] = format(e)
	}
	if !slices.Equal(got, want) {
		t.Errorf("events:\n got %q\nwant %q", got, want)
	}
}

// TestVirtualAllButtons presses and releases every SDL gamepad button, the
// extended ones (paddle1–4, misc1–6, touchpad; IN-06) too, one after
// another and then all together. TestNamesMatchControl checks that these
// names are the control names that profiles bind.
func TestVirtualAllButtons(t *testing.T) {
	s := open(t, nil)
	v, _ := attach(t, s, "padmate test pad")
	var names []string // all but guide, which SDL delays; see below
	for b := range sdl.GAMEPAD_BUTTON_COUNT {
		if b != sdl.GAMEPAD_BUTTON_GUIDE {
			names = append(names, input.ButtonName(uint8(b)))
		}
	}
	var want []string
	set := func(name string, down bool) {
		t.Helper()
		if err := v.SetButton(name, down); err != nil {
			t.Fatalf("SetButton(%q, %t): %v", name, down, err)
		}
		if down {
			want = append(want, name+" down")
		} else {
			want = append(want, name+" up")
		}
	}
	check := func() []input.Event {
		t.Helper()
		evs := collect(t, s, v.ID(), len(want))
		got := make([]string, len(evs))
		for i, e := range evs {
			got[i] = format(e)
		}
		if !slices.Equal(got, want) {
			t.Errorf("events:\n got %q\nwant %q", got, want)
		}
		want = want[:0]
		return evs
	}
	for _, name := range names {
		set(name, true)
		set(name, false)
	}
	for _, name := range names {
		set(name, true)
	}
	for _, name := range slices.Backward(names) {
		set(name, false)
	}
	check()

	// SDL reports a guide release no earlier than 250 ms after the press
	// (SDL_MINIMUM_GUIDE_BUTTON_DELAY_MS, in whole milliseconds), and
	// drops a press while it holds a release back. So a quick double tap
	// is one press of about 250 ms.
	for range 2 {
		set("guide", true)
		set("guide", false)
	}
	want = want[:2] // the first tap
	evs := check()
	if d := time.Duration(evs[1].Time - evs[0].Time); d < 249*time.Millisecond {
		t.Errorf("guide released %v after the press, want 250 ms", d)
	}
	// Nothing of the second tap is left.
	set("south", true)
	set("south", false)
	check()
	if n := s.Dropped(); n != 0 {
		t.Errorf("Dropped() = %d", n)
	}
}

// TestVirtualReattachSameKey checks the DeviceKeys (IN-09) of virtual
// gamepads, which have no serial and no path: a gamepad that is detached
// and attached again gets its key back, two with the same name are #0 and
// #1, and one with another name has another GUID.
func TestVirtualReattachSameKey(t *testing.T) {
	// The names differ after the 9 characters that fit in the GUID, so
	// only the name's CRC tells their GUIDs apart.
	const name, other = "padmate key pad", "padmate key pad 2"
	s := open(t, nil)
	r := input.NewRegistry()
	// attachKey attaches a virtual gamepad named n and connects it in r.
	attachKey := func(n string) (*input.Virtual, input.DeviceKey, bool) {
		t.Helper()
		v, added := attach(t, s, n)
		info := added.Info
		if info == nil {
			t.Fatal("DeviceAdded without Info")
		}
		if info.Serial != "" || info.Path != "" {
			t.Errorf("%q: Serial = %q, Path = %q; want both empty", n, info.Serial, info.Path)
		}
		// The GUID has the name's CRC and as much of the name as fits.
		if guid := input.VirtualGUID(n); info.GUID != guid {
			t.Errorf("%q: GUID = %s, want %s", n, info.GUID, guid)
		}
		k, back := r.Connect(info)
		return v, k, back
	}
	detach := func(v *input.Virtual, want input.DeviceKey) {
		t.Helper()
		if err := v.Detach(); err != nil {
			t.Fatalf("Detach: %v", err)
		}
		if e := collect(t, s, v.ID(), 1)[0]; e.Kind != input.DeviceRemoved {
			t.Fatalf("event %+v, want DeviceRemoved", e)
		}
		if k, ok := r.Disconnect(v.ID()); !ok || k != want {
			t.Errorf("Disconnect = %v, %t; want %v", k, ok, want)
		}
	}

	v1, k1, back := attachKey(name)
	if k1 != (input.DeviceKey{GUID: k1.GUID}) || back {
		t.Errorf("Connect = %v, %t; want the GUID alone, not reconnected", k1, back)
	}
	detach(v1, k1)
	v2, k2, back := attachKey(name)
	if v2.ID() == v1.ID() || k2 != k1 || !back {
		t.Errorf("attached again: id %d, Connect = %v, %t; want a new id, %v, true", v2.ID(), k2, back, k1)
	}

	v3, k3, back := attachKey(name)
	if want := (input.DeviceKey{GUID: k1.GUID, N: 1}); k3 != want || back {
		t.Errorf("a second %q: Connect = %v, %t; want %v, false", name, k3, back, want)
	}
	_, k4, back := attachKey(other)
	if k4.GUID == k1.GUID || k4.N != 0 || back {
		t.Errorf("%q: Connect = %v, %t; want another GUID than %s, N 0", other, k4, back, k1.GUID)
	}
	if got, want := r.Connected(), []input.DeviceKey{k2, k3, k4}; !slices.Equal(got, want) {
		t.Errorf("Connected() = %v, want %v", got, want)
	}

	// Both identical gamepads go and come back; they cannot be told apart,
	// so the first one back is #0.
	detach(v3, k3)
	detach(v2, k2)
	if _, got, gotBack := attachKey(name); got != k2 || !gotBack {
		t.Errorf("first back: Connect = %v, %t; want %v, true", got, gotBack, k2)
	}
	if _, got, gotBack := attachKey(name); got != k3 || !gotBack {
		t.Errorf("second back: Connect = %v, %t; want %v, true", got, gotBack, k3)
	}
}

func TestVirtualBadControl(t *testing.T) {
	s := open(t, nil)
	v, _ := attach(t, s, "padmate test pad")
	for _, err := range []error{
		v.SetButton("leftx", true),
		v.SetButton("bogus", true),
		v.SetAxis("south", 1),
		v.SetAxis("", 1),
	} {
		if err == nil {
			t.Error("a bad control name was accepted")
		}
	}
}

// noEvent checks that no event of device id arrives within 50 ms.
func noEvent(t *testing.T, s *input.Source, id input.DeviceID) {
	t.Helper()
	timeout := time.After(50 * time.Millisecond)
	for {
		select {
		case e, ok := <-s.Events():
			if !ok {
				t.Fatal("Events closed")
			}
			if e.Device == id {
				t.Errorf("unexpected event %+v", e)
			}
		case <-timeout:
			return
		}
	}
}

// TestVirtualDescErrors attaches virtual gamepads with bad control lists:
// AttachVirtualDesc fails, naming the control, and attaches nothing.
func TestVirtualDescErrors(t *testing.T) {
	s := open(t, nil)
	for _, tc := range []struct {
		name string
		d    input.VirtualDesc
		err  string
	}{
		{"unknown button", input.VirtualDesc{Buttons: []string{"south", "sl"}}, `unknown button "sl"`},
		{"repeated button", input.VirtualDesc{Buttons: []string{"south", "east", "south"}}, `button "south" is listed twice`},
		{"unknown axis", input.VirtualDesc{Axes: []string{"leftx", "leftz"}}, `unknown axis "leftz"`},
		{"button among axes", input.VirtualDesc{Axes: []string{"leftx", "south"}}, `unknown axis "south"`},
	} {
		tc.d.Name = "padmate bad pad"
		v, err := s.AttachVirtualDesc(tc.d)
		if want := `input: the virtual gamepad "padmate bad pad": ` + tc.err; err == nil || err.Error() != want {
			t.Errorf("%s: AttachVirtualDesc = %v, %v; want error %q", tc.name, v, err, want)
		}
		if v != nil {
			_ = v.Detach()
		}
	}
	// No device of any id: a DeviceAdded would be the first event.
	timeout := time.After(50 * time.Millisecond)
	for {
		select {
		case e, ok := <-s.Events():
			if !ok {
				t.Fatal("Events closed")
			}
			if e.Kind == input.DeviceAdded && e.Info != nil && e.Info.Virtual {
				t.Errorf("DeviceAdded %+v", *e.Info)
			}
		case <-timeout:
			return
		}
	}
}

// TestSidewaysLCopy checks that the internal tests' copy of the sideways
// Joy-Con (L)'s buttons (TestControlMask, TestPackIndex) is still
// inputtest.JoyConLeft's.
func TestSidewaysLCopy(t *testing.T) {
	if got := inputtest.JoyConLeft("").Buttons; !slices.Equal(got, input.SidewaysL) {
		t.Errorf("inputtest.JoyConLeft buttons %q, the internal tests' copy %q", got, input.SidewaysL)
	}
}

// TestVirtualJoyCon attaches the virtual Joy-Cons of inputtest: padmate
// carries their USB ids into SDL, SDL derives the gamepad type from them,
// the packed joystick indices route every listed control, and the others
// are refused. It models SDL's mapping as read from the SDL source and
// proves nothing about real Joy-Cons.
func TestVirtualJoyCon(t *testing.T) {
	for _, tc := range []struct {
		desc      input.VirtualDesc
		typ       string
		noButtons []string
		noAxes    []string
	}{
		{inputtest.JoyConLeft("padmate virtual Joy-Con (L)"), "joyconleft", []string{"dpad.up", "paddle1", "back"}, []string{"rightx", "lefttrigger"}},
		{inputtest.JoyConRight("padmate virtual Joy-Con (R)"), "joyconright", []string{"dpad.up", "paddle2", "rightstick"}, []string{"rightx", "righttrigger"}},
		{inputtest.JoyConPair("padmate virtual Joy-Cons"), "joyconpair", []string{"touchpad", "misc2"}, nil},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			s := open(t, nil)
			d := tc.desc
			v, err := s.AttachVirtualDesc(d)
			if err != nil {
				t.Fatalf("AttachVirtualDesc: %v", err)
			}
			added := collect(t, s, v.ID(), 1)[0]
			if added.Kind != input.DeviceAdded || added.Info == nil {
				t.Fatalf("first event %+v, want DeviceAdded with Info", added)
			}
			info := *added.Info
			if info.ButtonMask == 0 || info.AxisMask == 0 {
				t.Fatalf("SDL capabilities missing: buttons=%#x axes=%#x", info.ButtonMask, info.AxisMask)
			}
			info.ButtonMask, info.AxisMask = 0, 0
			want := input.DeviceInfo{
				ID:           v.ID(),
				Name:         d.Name,
				Type:         tc.typ,
				ButtonLabels: input.FaceButtonLabels{South: "B", East: "A", West: "Y", North: "X"},
				Vendor:       inputtest.VendorNintendo,
				Product:      d.Product,
				GUID:         input.VirtualDescGUID(d.Name, d.Vendor, d.Product),
				Driver:       "virtual",
				IsGamepad:    true,
				Virtual:      true,
				Capabilities: input.Capabilities{Known: true},
			}
			if info != want {
				t.Errorf("Info:\n got %+v\nwant %+v", info, want)
			}

			// check compares the next events of the gamepad with want.
			check := func(want ...string) {
				t.Helper()
				evs := collect(t, s, v.ID(), len(want))
				got := make([]string, len(evs))
				for i, e := range evs {
					got[i] = format(e)
				}
				if !slices.Equal(got, want) {
					t.Errorf("events:\n got %q\nwant %q", got, want)
				}
			}
			// One control at a time, so that guide, whose release SDL
			// delays by 250 ms (TestVirtualAllButtons), stays in order.
			for _, b := range d.Buttons {
				if err := v.SetButton(b, true); err != nil {
					t.Fatalf("SetButton(%q): %v", b, err)
				}
				if err := v.SetButton(b, false); err != nil {
					t.Fatalf("SetButton(%q): %v", b, err)
				}
				check(b+" down", b+" up")
			}
			for _, a := range d.Axes {
				if err := v.SetAxis(a, 32767); err != nil {
					t.Fatalf("SetAxis(%q): %v", a, err)
				}
				if err := v.SetAxis(a, 0); err != nil {
					t.Fatalf("SetAxis(%q): %v", a, err)
				}
				check(a+" 32767", a+" 0")
			}

			for _, b := range tc.noButtons {
				wantErr := fmt.Sprintf("input: the virtual gamepad %q has no button %q", d.Name, b)
				if err := v.SetButton(b, true); err == nil || err.Error() != wantErr {
					t.Errorf("SetButton(%q) = %v, want %q", b, err, wantErr)
				}
			}
			for _, a := range tc.noAxes {
				wantErr := fmt.Sprintf("input: the virtual gamepad %q has no axis %q", d.Name, a)
				if err := v.SetAxis(a, 32767); err == nil || err.Error() != wantErr {
					t.Errorf("SetAxis(%q) = %v, want %q", a, err, wantErr)
				}
			}
			noEvent(t, s, v.ID())

			// SDL releases the held buttons in joystick index order before
			// it reports the removal.
			for _, err := range []error{
				v.SetButton("leftshoulder", true),
				v.SetButton("rightshoulder", true),
				v.Detach(),
			} {
				if err != nil {
					t.Fatal(err)
				}
			}
			check("leftshoulder down", "rightshoulder down", "leftshoulder up", "rightshoulder up", "removed")
		})
	}
}

// TestVirtualJoyConKeys checks the DeviceKeys (IN-09) of virtual Joy-Cons:
// an L and an R of the same name differ by product, and two L of the same
// name, connected at once, are #0 and #1. Gamepads with the same USB ids
// share one name, as VirtualDesc says.
func TestVirtualJoyConKeys(t *testing.T) {
	const name = "padmate virtual Joy-Con"
	s := open(t, nil)
	r := input.NewRegistry()
	attachKey := func(d input.VirtualDesc) input.DeviceKey {
		t.Helper()
		v, err := s.AttachVirtualDesc(d)
		if err != nil {
			t.Fatalf("AttachVirtualDesc: %v", err)
		}
		added := collect(t, s, v.ID(), 1)[0]
		if added.Kind != input.DeviceAdded || added.Info == nil {
			t.Fatalf("first event %+v, want DeviceAdded with Info", added)
		}
		if want := input.VirtualDescGUID(d.Name, d.Vendor, d.Product); added.Info.GUID != want {
			t.Errorf("GUID = %s, want %s", added.Info.GUID, want)
		}
		k, back := r.Connect(added.Info)
		if back {
			t.Errorf("Connect(%+v) reconnected %v", *added.Info, k)
		}
		return k
	}
	l0 := attachKey(inputtest.JoyConLeft(name))
	rk := attachKey(inputtest.JoyConRight(name))
	l1 := attachKey(inputtest.JoyConLeft(name))
	if rk.GUID == l0.GUID || rk.N != 0 {
		t.Errorf("R: key %v, want another GUID than L's %s, N 0", rk, l0.GUID)
	}
	if l0 != (input.DeviceKey{GUID: l0.GUID}) || l1 != (input.DeviceKey{GUID: l0.GUID, N: 1}) {
		t.Errorf("two L: keys %v and %v, want the GUID with N 0 and 1", l0, l1)
	}
}

// TestCloseWithVirtual closes a Source whose virtual gamepad is still
// attached, with a button held.
func TestCloseWithVirtual(t *testing.T) {
	lib := inputtest.Library(t)
	s, err := input.Open(input.Options{Library: lib})
	if err != nil {
		t.Fatal(err)
	}
	v, _ := attach(t, s, "padmate test pad")
	if err := v.SetButton("south", true); err != nil {
		t.Fatal(err)
	}
	s.Close()
	waitClosed(t, s)
	if err := v.SetButton("south", false); !errors.Is(err, input.ErrClosed) {
		t.Errorf("SetButton after Close = %v, want ErrClosed", err)
	}
	if err := v.Detach(); err != nil {
		t.Errorf("Detach after Close = %v", err)
	}

	// Close quit SDL entirely: every subsystem is down, so the virtual
	// gamepad is gone. No Source is open, so nothing else calls SDL.
	if n := sdl.WasInit(0); n != 0 {
		t.Errorf("SDL_WasInit(0) = %#x after Close, want 0", uint32(n))
	}

	// The next Source works.
	s2 := open(t, nil)
	v2, _ := attach(t, s2, "padmate test pad 2")
	if err := v2.SetButton("south", true); err != nil {
		t.Fatal(err)
	}
	if e := collect(t, s2, v2.ID(), 1)[0]; format(e) != "south down" {
		t.Errorf("event %q, want south down", format(e))
	}
}

// TestSampleTicks checks the readings that map Event.Time onto latency
// Stamps (ENG-11): they calibrate SDL's clock to within 10 µs, and the
// event of a virtual gamepad maps into the SetButton call that sent it,
// since SDL stamps it in the SDL_UpdateJoysticks that SetButton makes.
func TestSampleTicks(t *testing.T) {
	s := open(t, nil)
	freq, pairs, err := s.SampleTicks(64)
	if err != nil {
		t.Fatalf("SampleTicks(64): %v", err)
	}
	if freq <= 0 || len(pairs) != 64 {
		t.Fatalf("SampleTicks(64) = %d Hz, %d readings", freq, len(pairs))
	}
	narrowest := pairs[0].After - pairs[0].Before
	for i, p := range pairs {
		if p.After < p.Before {
			t.Errorf("reading %d: After %d < Before %d", i, p.After, p.Before)
		}
		if i > 0 && (p.Before < pairs[i-1].Before || p.NS < pairs[i-1].NS) {
			t.Errorf("reading %d %+v comes before reading %d %+v", i, p, i-1, pairs[i-1])
		}
		narrowest = min(narrowest, p.After-p.Before)
	}
	if runtime.GOOS == "windows" && freq != latency.Freq() {
		t.Fatalf("SDL's counter is %d Hz, QueryPerformanceCounter %d Hz", freq, latency.Freq())
	}
	if freq != latency.Freq() {
		t.Skipf("SDL's counter (%d Hz) is not latency's (%d Hz) on %s", freq, latency.Freq(), runtime.GOOS)
	}
	// On Windows both read QueryPerformanceCounter. On Linux SDL reads
	// CLOCK_MONOTONIC_RAW and Go CLOCK_MONOTONIC: they differ by the NTP
	// slew only, far less than a bracket over these readings.
	c, err := latency.CalibrateSDL(freq, pairs)
	if err != nil {
		t.Fatalf("CalibrateSDL: %v", err)
	}
	t.Logf("SDL counter %d Hz, latency counter %d Hz; tick zero within %v; narrowest bracket %d counts",
		freq, latency.Freq(), c.Width(), narrowest)
	if c.Width() > 10*time.Microsecond {
		t.Errorf("tick zero within %v, want ≤ 10µs", c.Width())
	}
	for i, p := range pairs {
		if lo, hi := c.Bounds(p.NS); hi < p.Before || lo > p.After {
			t.Errorf("reading %d: Bounds(%d) = [%d, %d] misses [%d, %d]", i, p.NS, lo, hi, p.Before, p.After)
		}
	}

	v, _ := attach(t, s, "padmate ticks pad")
	t0 := latency.Now()
	if err := v.SetButton("south", true); err != nil {
		t.Fatal(err)
	}
	t1 := latency.Now()
	e := collect(t, s, v.ID(), 1)[0]
	if format(e) != "south down" {
		t.Fatalf("event %q, want south down", format(e))
	}
	lo, hi := c.Bounds(e.Time)
	if hi < t0 || lo > t1 {
		t.Errorf("south down at %d ns maps to [%d, %d], outside SetButton's [%d, %d]", e.Time, lo, hi, t0, t1)
	}
	t.Logf("SetButton took %v; the event was stamped %v into it", t1.Sub(t0), c.Mid(e.Time).Sub(t0))

	if _, p, err := s.SampleTicks(1024); err != nil || len(p) != 1024 {
		t.Errorf("SampleTicks(1024) = %d readings, %v", len(p), err)
	}
	for _, n := range []int{0, -1, 1025} {
		if _, p, err := s.SampleTicks(n); err == nil || errors.Is(err, input.ErrClosed) || p != nil {
			t.Errorf("SampleTicks(%d) = %d readings, %v; want a range error", n, len(p), err)
		}
	}
	s.Close()
	if _, p, err := s.SampleTicks(1); !errors.Is(err, input.ErrClosed) || p != nil {
		t.Errorf("SampleTicks(1) after Close = %d readings, %v; want ErrClosed", len(p), err)
	}
	// The range is checked before T-input is asked.
	if _, _, err := s.SampleTicks(1025); err == nil || errors.Is(err, input.ErrClosed) {
		t.Errorf("SampleTicks(1025) after Close = %v, want a range error", err)
	}
}

// TestPanicOnTInput checks that a panic or runtime.Goexit on T-input ends
// the Source as Close does, instead of the process: the call returns
// ErrClosed with the panic, Err reports it, Events closes, SDL is quit,
// and the next Source works.
func TestPanicOnTInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		kill func(*input.Source) error
		want string
	}{
		{"panic", func(s *input.Source) error { return s.Panic("boom") }, "input: panic on T-input: boom"},
		{"goexit", (*input.Source).Goexit, "input: panic on T-input: runtime.Goexit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := open(t, nil)
			v, _ := attach(t, s, "padmate test pad")
			if err := v.SetButton("south", true); err != nil {
				t.Fatal(err)
			}
			err := tc.kill(s)
			var pe *input.PanicError
			if !errors.Is(err, input.ErrClosed) || !errors.As(err, &pe) || pe.Error() != tc.want || len(pe.Stack) == 0 {
				t.Fatalf("the call = %v, want ErrClosed and %q with a stack", err, tc.want)
			}
			waitClosed(t, s)
			if got, ok := errors.AsType[*input.PanicError](s.Err()); !ok || got != pe {
				t.Errorf("Err() = %v, want the error of the call", s.Err())
			}
			s.Close() // returns: T-input has ended
			if err := v.SetButton("south", false); !errors.Is(err, input.ErrClosed) {
				t.Errorf("SetButton after the panic = %v, want ErrClosed", err)
			}
			if n := sdl.WasInit(0); n != 0 {
				t.Errorf("SDL_WasInit(0) = %#x after the panic, want 0", uint32(n))
			}

			s2 := open(t, nil)
			if s2.Err() != nil {
				t.Errorf("Err() of the next Source = %v, want nil", s2.Err())
			}
			v2, _ := attach(t, s2, "padmate test pad 2")
			if err := v2.SetButton("south", true); err != nil {
				t.Fatal(err)
			}
			if e := collect(t, s2, v2.ID(), 1)[0]; format(e) != "south down" {
				t.Errorf("event %q, want south down", format(e))
			}
		})
	}
}

// TestJoystickOnly attaches a virtual joystick without a gamepad mapping.
// It is reported as a joystick-only device, and removed once, and its
// buttons are not read.
func TestJoystickOnly(t *testing.T) {
	s := open(t, nil)
	v, err := s.AttachVirtualJoystick("padmate test joystick")
	if err != nil {
		t.Fatalf("AttachVirtualJoystick: %v", err)
	}
	added := collect(t, s, v.ID(), 1)[0]
	if added.Kind != input.DeviceAdded || added.Info == nil {
		t.Fatalf("first event %+v, want DeviceAdded with Info", added)
	}
	info := added.Info
	if info.ID != v.ID() || info.Name != "padmate test joystick" || info.IsGamepad || !info.Virtual || info.Type != "unknown" {
		t.Errorf("Info = %+v, want the joystick-only virtual device %d", *info, v.ID())
	}
	// SDL_virtualjoystick.c: driver signature 'v', the joystick type (0,
	// unknown) as driver data.
	if info.Driver != "virtual" || !strings.HasSuffix(info.GUID, "7600") {
		t.Errorf("Info.Driver = %q, GUID = %q; want virtual, …7600", info.Driver, info.GUID)
	}
	for _, err := range []error{v.SetButton("south", true), v.SetAxis("leftx", 16384), v.Detach()} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if e := collect(t, s, v.ID(), 1)[0]; e.Kind != input.DeviceRemoved {
		t.Errorf("event %+v after the removal, want only DeviceRemoved", e)
	}
	select {
	case e := <-s.Events():
		if e.Device == v.ID() {
			t.Errorf("event after the removal: %+v", e)
		}
	case <-time.After(50 * time.Millisecond):
	}
}

// TestGoneNotReported attaches a virtual device and detaches it before
// T-input handles its ADDED events, as SDL does with a Joy-Con that it
// folds into a pair (IN-07). SDL still calls a removed gamepad a gamepad
// and keeps its name, but the Source reports nothing of it: no
// DeviceAdded with an empty GUID, and no DeviceRemoved.
func TestGoneNotReported(t *testing.T) {
	for _, gamepad := range []bool{true, false} {
		t.Run(fmt.Sprintf("gamepad=%t", gamepad), func(t *testing.T) {
			s := open(t, nil)
			id, err := s.AttachGone("padmate gone pad", gamepad)
			if err != nil {
				t.Fatalf("AttachGone: %v", err)
			}
			check := func(e input.Event, ok bool) {
				t.Helper()
				if !ok {
					t.Fatal("Events closed")
				}
				if e.Device == id {
					t.Errorf("event of the gone device: %+v (Info %+v)", e, e.Info)
				}
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				n, err := s.Gone()
				if err != nil {
					t.Fatalf("Gone: %v", err)
				}
				if n == 1 {
					break
				}
				if n > 1 || time.Now().After(deadline) {
					t.Fatalf("Gone() = %d, want 1", n)
				}
				select {
				case e, ok := <-s.Events():
					check(e, ok)
				case <-time.After(time.Millisecond):
				}
			}

			// The removal events of the gone device come before those of a
			// barrier gamepad that is attached and detached after it.
			v, err := s.AttachVirtual("padmate barrier pad")
			if err != nil {
				t.Fatalf("AttachVirtual: %v", err)
			}
			if err := v.Detach(); err != nil {
				t.Fatalf("Detach: %v", err)
			}
			timeout := time.After(5 * time.Second)
			for removed := false; !removed; {
				select {
				case e, ok := <-s.Events():
					check(e, ok)
					removed = e.Device == v.ID() && e.Kind == input.DeviceRemoved
				case <-timeout:
					t.Fatal("the barrier's DeviceRemoved did not arrive")
				}
			}
			if n, err := s.Gone(); n != 1 || err != nil {
				t.Errorf("Gone() = %d, %v after the barrier; want 1", n, err)
			}
		})
	}
}

// TestNoGoroutineLeak checks that Close leaves no goroutine behind.
func TestNoGoroutineLeak(t *testing.T) {
	lib := inputtest.Library(t)
	before := runtime.NumGoroutine()
	for range 3 {
		s, err := input.Open(input.Options{Library: lib})
		if err != nil {
			t.Fatal(err)
		}
		v, _ := attach(t, s, "padmate test pad")
		if err := v.SetButton("south", true); err != nil {
			t.Fatal(err)
		}
		s.Close()
		waitClosed(t, s)
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		buf := make([]byte, 1<<16)
		t.Errorf("%d goroutines before, %d after:\n%s", before, n, buf[:runtime.Stack(buf, true)])
	}
}
