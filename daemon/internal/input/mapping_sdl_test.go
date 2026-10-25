package input_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/mapwizard"
)

// These tests are the SDL side of the mapping wizard and custom mappings
// (IN-17, IN-18, IN-19, ADR-0072). They use SDL virtual joysticks, which SDL
// maps to no gamepad until a mapping for their GUID is added.

// rawMapping returns the mapping string of the device with GUID guid that
// binds the given fields.
func rawMapping(guid string, fields ...string) string {
	return guid + ",*," + strings.Join(fields, ",") + ",platform:Windows"
}

// until returns the first event of device id (any device when id is 0) that
// match accepts, within five seconds.
func until(t *testing.T, s *input.Source, id input.DeviceID, what string, match func(input.Event) bool) input.Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-s.Events():
			if !ok {
				t.Fatalf("Events closed while waiting for %s", what)
			}
			if (id == 0 || e.Device == id) && match(e) {
				return e
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func isKind(kind input.EventKind) func(input.Event) bool {
	return func(e input.Event) bool { return e.Kind == kind }
}

// asGamepad accepts the DeviceAdded of a gamepad.
func asGamepad(e input.Event) bool {
	return e.Kind == input.DeviceAdded && e.Info != nil && e.Info.IsGamepad
}

// attachRaw attaches a raw virtual joystick and returns it with its
// DeviceAdded, which is a joystick without a mapping.
func attachRaw(t *testing.T, s *input.Source, name string, axes, buttons, hats int, rest ...int16) (*input.Virtual, input.Event) {
	t.Helper()
	v, err := s.AttachRawJoystick(name, axes, buttons, hats, rest...)
	if err != nil {
		t.Fatalf("AttachRawJoystick: %v", err)
	}
	added := until(t, s, v.ID(), "the DeviceAdded of the joystick", isKind(input.DeviceAdded))
	if added.Info == nil || added.Info.IsGamepad {
		t.Fatalf("DeviceAdded = %+v, want a joystick without a mapping", added.Info)
	}
	return v, added
}

func hasButton(info *input.DeviceInfo, names ...string) bool {
	for _, n := range names {
		if info.ButtonMask&(1<<buttonBit(n)) == 0 {
			return false
		}
	}
	return true
}

// untilAll waits for an event of each device in wants that its predicate
// accepts, in any order, and drops the events of the other devices.
func untilAll(t *testing.T, s *input.Source, what string, wants map[input.DeviceID]func(input.Event) bool) {
	t.Helper()
	left := len(wants)
	timeout := time.After(5 * time.Second)
	for left > 0 {
		select {
		case e, ok := <-s.Events():
			if !ok {
				t.Fatalf("Events closed while waiting for %s", what)
			}
			if match, ok := wants[e.Device]; ok && match != nil && match(e) {
				wants[e.Device] = nil
				left--
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func buttonBit(name string) int {
	for i := range 32 {
		if input.ButtonName(uint8(i)) == name {
			return i
		}
	}
	return 31
}

// TestMappingMakesAJoystickAGamepad: adding a mapping for the GUID of a
// joystick that SDL does not know turns it into a gamepad, reported again with
// the same id, and its buttons are read by the names of the mapping.
func TestMappingMakesAJoystickAGamepad(t *testing.T) {
	s := open(t, nil)
	v, added := attachRaw(t, s, "padmate raw up", 4, 8, 1)
	m := rawMapping(added.Info.GUID, "a:b0", "b:b1", "back:b6", "start:b7", "leftx:a0", "lefty:a1", "dpup:h0.1")
	rep, err := s.SetMappings([]string{m})
	if err != nil {
		t.Fatalf("SetMappings: %v", err)
	}
	if len(rep.Devices) != 1 || rep.Devices[0].Device != v.ID() || !rep.Devices[0].Gamepad || !rep.Devices[0].Applied {
		t.Fatalf("report = %+v, want the device applied as a gamepad", rep)
	}
	up := until(t, s, v.ID(), "the DeviceAdded of the gamepad", asGamepad)
	if up.Info.ID != v.ID() || up.Info.Name != "padmate raw up" {
		t.Errorf("Info = %+v; the device keeps its id and, with the name *, its name", *up.Info)
	}
	if !hasButton(up.Info, "south", "east", "back", "start", "dpad.up") || hasButton(up.Info, "west") {
		t.Errorf("ButtonMask = %#x, want south east back start dpad.up only", up.Info.ButtonMask)
	}
	if up.Info.AxisMask&(1<<0) == 0 || up.Info.AxisMask&(1<<1) == 0 || up.Info.AxisMask&(1<<2) != 0 {
		t.Errorf("AxisMask = %#x, want leftx and lefty only", up.Info.AxisMask)
	}
	if err := v.SetRawButton(0, true); err != nil {
		t.Fatal(err)
	}
	if e := until(t, s, v.ID(), "the press", isKind(input.ButtonDown)); e.Control != "south" {
		t.Errorf("press = %+v, want south", e)
	}
	if err := v.SetRawHat(0, 1); err != nil {
		t.Fatal(err)
	}
	if e := until(t, s, v.ID(), "the d-pad", isKind(input.ButtonDown)); e.Control != "dpad.up" {
		t.Errorf("hat = %+v, want dpup", e)
	}
	if err := v.SetRawAxis(0, 20000); err != nil {
		t.Fatal(err)
	}
	if e := until(t, s, v.ID(), "the stick", isKind(input.AxisMotion)); e.Control != "leftx" || e.Value != 20000 {
		t.Errorf("axis = %+v, want leftx 20000", e)
	}
}

// TestResettingAMappingReturnsAGamepadToAJoystick: the gamepad is removed
// from the consumer, then reported as a joystick without a mapping, with the
// same id; its buttons give no events any more. A mapping added again makes
// it a gamepad once more.
func TestResettingAMappingReturnsAGamepadToAJoystick(t *testing.T) {
	s := open(t, nil)
	v, added := attachRaw(t, s, "padmate raw down", 2, 4, 0)
	m := rawMapping(added.Info.GUID, "a:b0", "b:b1")
	if _, err := s.SetMappings([]string{m}); err != nil {
		t.Fatal(err)
	}
	until(t, s, v.ID(), "the gamepad", asGamepad)
	if err := v.SetRawButton(0, true); err != nil {
		t.Fatal(err)
	}
	until(t, s, v.ID(), "the press", isKind(input.ButtonDown))
	if _, err := s.SetMappings(nil); err != nil {
		t.Fatalf("SetMappings(nil): %v", err)
	}
	until(t, s, v.ID(), "the removal", isKind(input.DeviceRemoved))
	back := until(t, s, v.ID(), "the joystick again", isKind(input.DeviceAdded))
	if back.Info == nil || back.Info.IsGamepad || back.Info.ID != v.ID() {
		t.Fatalf("after the removal: %+v, want the joystick without a mapping, same id", back.Info)
	}
	if err := v.SetRawButton(0, false); err != nil {
		t.Fatal(err)
	}
	noEvent(t, s, v.ID())
	// And it can be mapped again.
	if _, err := s.SetMappings([]string{m}); err != nil {
		t.Fatal(err)
	}
	until(t, s, v.ID(), "the gamepad again", asGamepad)
	if err := v.SetRawButton(1, true); err != nil {
		t.Fatal(err)
	}
	if e := until(t, s, v.ID(), "the press", isKind(input.ButtonDown)); e.Control != "east" {
		t.Errorf("press = %+v, want east", e)
	}
}

// TestRemapIsReportedWhileAButtonIsHeld: a mapping changed under a gamepad
// that holds a button sends DeviceRemapped, with the controls of the new
// mapping, before any later event of the device. SDL releases nothing itself:
// the button that is held now reads as another element, so the consumer
// that tracked south as down would never see south go up (invariant 1); it
// resets the device on DeviceRemapped.
func TestRemapIsReportedWhileAButtonIsHeld(t *testing.T) {
	s := open(t, nil)
	v, added := attach(t, s, "padmate remap held")
	if err := v.SetButton("south", true); err != nil {
		t.Fatal(err)
	}
	until(t, s, v.ID(), "the press", func(e input.Event) bool { return e.Kind == input.ButtonDown && e.Control == "south" })
	// The virtual gamepad's buttons are joystick buttons 0, 1, … in the order
	// of SDL's enum; south is b0. The new mapping reads the south button as east
	// and binds only a and b.
	m := rawMapping(added.Info.GUID, "a:b1", "b:b0")
	rep, err := s.SetMappings([]string{m})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Devices) != 1 || !rep.Devices[0].Applied {
		t.Fatalf("report = %+v", rep)
	}
	re := until(t, s, v.ID(), "DeviceRemapped", isKind(input.DeviceRemapped))
	if re.Info == nil || re.Info.ID != v.ID() || !re.Info.IsGamepad {
		t.Fatalf("DeviceRemapped.Info = %+v", re.Info)
	}
	if !hasButton(re.Info, "south", "east") || hasButton(re.Info, "west") || hasButton(re.Info, "start") {
		t.Errorf("ButtonMask = %#x after the remap, want south and east only", re.Info.ButtonMask)
	}
	if err := v.SetButton("south", false); err != nil {
		t.Fatal(err)
	}
	// Joystick button 0 is east now: its release is an up of east, never of south.
	e := until(t, s, v.ID(), "the release", func(e input.Event) bool { return e.Kind == input.ButtonUp })
	if e.Control != "east" {
		t.Errorf("release = %+v, want east: south has no button any more", e)
	}
}

// TestMappingsOfOptionsApplyBeforeTheDeviceIsSeen: a mapping given to Open is
// loaded by SDL itself, before it enumerates the devices, so a joystick that
// is attached later is reported once, as a gamepad, with no joystick report
// to flip from.
func TestMappingsOfOptionsApplyBeforeTheDeviceIsSeen(t *testing.T) {
	name := "padmate startup raw"
	m := rawMapping(input.VirtualJoystickGUID(name), "a:b0", "b:b1")
	s, err := input.Open(input.Options{Library: inputtest.Library(t), Mappings: []string{m}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	file, err := s.MappingsFile()
	if err != nil || file == "" {
		t.Fatalf("MappingsFile = %q, %v", file, err)
	}
	data, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(data), "platform:Windows,") {
		t.Fatalf("the mapping file: %q, %v; SDL needs 'platform:Windows,' on each line", data, err)
	}
	v, err := s.AttachRawJoystick(name, 2, 4, 0)
	if err != nil {
		t.Fatal(err)
	}
	first := until(t, s, v.ID(), "the first report", isKind(input.DeviceAdded))
	if !first.Info.IsGamepad {
		t.Fatalf("the first report is a joystick without a mapping: %+v", *first.Info)
	}
	s.Close()
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("the mapping file stays after Close: %v", err)
	}
}

// TestResettingOneMappingKeepsTheOthers: with two mapped joysticks, SetMappings
// without one returns that one to a joystick and keeps the other mapped.
func TestResettingOneMappingKeepsTheOthers(t *testing.T) {
	// Without a mapping file the remaining mappings are added back after the
	// reload; with one SDL reads them from the file itself.
	for _, withFile := range []bool{false, true} {
		t.Run(fmt.Sprintf("file=%t", withFile), func(t *testing.T) {
			var mappings []string
			if withFile {
				mappings = []string{}
			}
			s, err := input.Open(input.Options{Library: inputtest.Library(t), Mappings: mappings})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.Close)
			resettingOneKeepsTheOthers(t, s, withFile)
		})
	}
}

func resettingOneKeepsTheOthers(t *testing.T, s *input.Source, withFile bool) {
	a, addedA := attachRaw(t, s, "1st raw pad", 2, 4, 0)
	b, addedB := attachRaw(t, s, "2nd raw pad", 2, 4, 0)
	mA := rawMapping(addedA.Info.GUID, "a:b0")
	mB := rawMapping(addedB.Info.GUID, "a:b2", "b:b3")
	if _, err := s.SetMappings([]string{mA, mB}); err != nil {
		t.Fatal(err)
	}
	untilAll(t, s, "both pads as gamepads", map[input.DeviceID]func(input.Event) bool{a.ID(): asGamepad, b.ID(): asGamepad})
	if withFile {
		file, _ := s.MappingsFile()
		if data, err := os.ReadFile(file); err != nil || !strings.Contains(string(data), addedA.Info.GUID) || !strings.Contains(string(data), addedB.Info.GUID) {
			t.Errorf("the mapping file lacks a mapping: %q, %v", data, err)
		}
	}
	rep, err := s.SetMappings([]string{mB})
	if err != nil {
		t.Fatal(err)
	}
	if withFile {
		file, _ := s.MappingsFile()
		if data, err := os.ReadFile(file); err != nil || strings.Contains(string(data), addedA.Info.GUID) || !strings.Contains(string(data), addedB.Info.GUID) {
			t.Errorf("the mapping file after the reset: %q, %v; want pad B's mapping only", data, err)
		}
	}
	until(t, s, a.ID(), "pad A removed", isKind(input.DeviceRemoved))
	if back := until(t, s, a.ID(), "pad A as a joystick", isKind(input.DeviceAdded)); back.Info.IsGamepad {
		t.Errorf("pad A is still a gamepad: %+v", *back.Info)
	}
	var sawB bool
	for _, d := range rep.Devices {
		if d.Device == b.ID() {
			sawB = true
			if !d.Gamepad || !d.Applied {
				t.Errorf("pad B in the report: %+v, want its mapping applied", d)
			}
		}
	}
	if !sawB {
		t.Errorf("report = %+v lacks pad B", rep)
	}
	if err := b.SetRawButton(2, true); err != nil {
		t.Fatal(err)
	}
	if e := until(t, s, b.ID(), "pad B's press", isKind(input.ButtonDown)); e.Control != "south" {
		t.Errorf("pad B press = %+v, want south: its mapping must have survived the reset", e)
	}
}

// TestMappingShadowedByTheEnvironmentIsReported: SDL_GAMECONTROLLERCONFIG
// has a priority above the mappings that a program adds, so the user's own
// mapping wins; the read-back says the mapping was not applied.
func TestMappingShadowedByTheEnvironmentIsReported(t *testing.T) {
	name := "padmate shadowed raw"
	guid := input.VirtualJoystickGUID(name)
	t.Setenv("SDL_GAMECONTROLLERCONFIG", rawMapping(guid, "a:b5"))
	s := open(t, nil)
	v, err := s.AttachRawJoystick(name, 6, 8, 0)
	if err != nil {
		t.Fatal(err)
	}
	until(t, s, v.ID(), "the gamepad of the environment's mapping", asGamepad)
	rep, err := s.SetMappings([]string{rawMapping(guid, "a:b0", "b:b1")})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Devices) != 1 || rep.Devices[0].Applied {
		t.Fatalf("report = %+v, want the mapping not applied", rep)
	}
	if got := rep.Devices[0].Got; !strings.Contains(got, "a:b5") {
		t.Errorf("Got = %q, want the environment's mapping", got)
	}
}

func TestSetMappingsRejects(t *testing.T) {
	s := open(t, nil)
	guid := input.VirtualJoystickGUID("padmate bad mappings")
	for name, ms := range map[string][]string{
		"not a mapping":  {"nonsense"},
		"binds nothing":  {guid + ",*,platform:Windows"},
		"two for a GUID": {rawMapping(guid, "a:b0"), rawMapping(guid, "a:b1")},
	} {
		if _, err := s.SetMappings(ms); err == nil {
			t.Errorf("%s: SetMappings(%q) succeeded", name, ms)
		}
	}
	// A failure applies nothing: the same call with one good mapping works.
	if _, err := s.SetMappings([]string{rawMapping(guid, "a:b0")}); err != nil {
		t.Errorf("SetMappings after a rejected call: %v", err)
	}
	if _, err := s.SetMappings([]string{rawMapping(guid, "a:b0"), rawMapping(guid, "a:b0")}); err != nil {
		t.Errorf("the same mapping twice is not a conflict: %v", err)
	}
}

// TestMappingsOfOptionsWithAnEnvironmentFile: the user's own
// SDL_GAMECONTROLLERCONFIG_FILE has the file hint, which SDL reads from the
// environment instead of the one Open sets; the mappings are then added through
// the API, and still hold after another mapping is taken away.
func TestMappingsOfOptionsWithAnEnvironmentFile(t *testing.T) {
	theirs := filepath.Join(t.TempDir(), "theirs.txt")
	if err := os.WriteFile(theirs, []byte("# the user's own database\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SDL_GAMECONTROLLERCONFIG_FILE", theirs)
	name := "padmate env file raw"
	m := rawMapping(input.VirtualJoystickGUID(name), "a:b0", "b:b1")
	other := rawMapping(input.VirtualJoystickGUID("padmate other file raw"), "a:b0")
	s, err := input.Open(input.Options{Library: inputtest.Library(t), Mappings: []string{m, other}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	v, err := s.AttachRawJoystick(name, 2, 4, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first := until(t, s, v.ID(), "the first report", isKind(input.DeviceAdded)); !first.Info.IsGamepad {
		t.Fatalf("the first report is a joystick without a mapping: %+v", *first.Info)
	}
	// Taking the other mapping away reloads SDL's database; ours stays.
	if _, err := s.SetMappings([]string{m}); err != nil {
		t.Fatal(err)
	}
	if err := v.SetRawButton(1, true); err != nil {
		t.Fatal(err)
	}
	if e := until(t, s, v.ID(), "the press", isKind(input.ButtonDown)); e.Control != "east" {
		t.Errorf("press = %+v, want east", e)
	}
}

// TestEmergencyComboAfterAMapping: the emergency combo is picked again when a
// mapping changes the controller (IN-18). A joystick that a mapping turns
// into a gamepad with back and start has the combo, so holding both pauses;
// a remap that leaves back out takes it away, and one that puts it back gives
// it again.
func TestEmergencyComboAfterAMapping(t *testing.T) {
	s, fired := openCombo(t, nil)
	v, added := attachRaw(t, s, "padmate combo raw", 2, 10, 0)
	full := rawMapping(added.Info.GUID, "a:b0", "back:b6", "start:b7")
	hold := func(down bool) {
		t.Helper()
		for _, b := range []int{6, 7} {
			if err := v.SetRawButton(b, down); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := s.SetMappings([]string{full}); err != nil {
		t.Fatal(err)
	}
	until(t, s, v.ID(), "the gamepad", asGamepad)
	hold(true)
	until(t, s, v.ID(), "the combo", isKind(input.ComboHeld))
	calledOnce(t, fired)
	hold(false)

	// Back is gone from the mapping: the same buttons are no combo.
	if _, err := s.SetMappings([]string{rawMapping(added.Info.GUID, "a:b0", "start:b7", "dpup:b6")}); err != nil {
		t.Fatal(err)
	}
	until(t, s, v.ID(), "the remap", isKind(input.DeviceRemapped))
	hold(true)
	noCombo(t, s, v.ID(), fired, 4*comboHold)
	hold(false)

	// And back again.
	if _, err := s.SetMappings([]string{full}); err != nil {
		t.Fatal(err)
	}
	until(t, s, v.ID(), "the remap", isKind(input.DeviceRemapped))
	hold(true)
	until(t, s, v.ID(), "the combo again", isKind(input.ComboHeld))
}

// noCombo checks that no ComboHeld of device id arrives and OnCombo is not
// called for d; its other events are no matter.
func noCombo(t *testing.T, s *input.Source, id input.DeviceID, fired <-chan struct{}, d time.Duration) {
	t.Helper()
	timeout := time.After(d)
	for {
		select {
		case e, ok := <-s.Events():
			if !ok {
				t.Fatal("Events closed")
			}
			if e.Device == id && e.Kind == input.ComboHeld {
				t.Errorf("unexpected combo %+v", e)
			}
		case <-fired:
			t.Error("OnCombo was called")
		case <-timeout:
			return
		}
	}
}

// TestWizardTriggerMappingReadsThePartialPull: a trigger that rests at -32768
// and that the user pulled only part of the way is bound by the wizard
// (mapwizard.AxisSpec) so that SDL reads the whole pull: a half range would
// read 0 once the trigger is past the middle of it.
func TestWizardTriggerMappingReadsThePartialPull(t *testing.T) {
	s := open(t, nil)
	v, added := attachRaw(t, s, "padmate raw trigger", 2, 2, 0, 0, -32768)
	spec := mapwizard.AxisSpec(1, -32768, 10000)
	m := rawMapping(added.Info.GUID, "a:b0", "lefttrigger:"+spec)
	if _, err := s.SetMappings([]string{m}); err != nil {
		t.Fatal(err)
	}
	until(t, s, v.ID(), "the DeviceAdded of the gamepad", asGamepad)
	for _, step := range []struct {
		raw  int16
		want func(int16) bool
		what string
	}{
		{10000, func(x int16) bool { return x > 18000 && x < 24000 }, "about 65 percent of the trigger"},
		{32767, func(x int16) bool { return x == 32767 }, "a full trigger past the middle of the pull"},
		{-32768, func(x int16) bool { return x == 0 }, "a released trigger"},
	} {
		if err := v.SetRawAxis(1, step.raw); err != nil {
			t.Fatal(err)
		}
		e := until(t, s, v.ID(), step.what, func(e input.Event) bool { return e.Kind == input.AxisMotion })
		if e.Control != "lefttrigger" || !step.want(e.Value) {
			t.Errorf("axis at %d with %q = %+v, want %s", step.raw, spec, e, step.what)
		}
	}
}

// TestAnotherPadsResetLeavesAPadAlone: taking one pad's mapping away makes SDL
// load its database again and remap every open gamepad, so every one is told
// (SDL_EVENT_GAMEPAD_REMAPPED). A gamepad whose mapping is the same after it
// is not reported as remapped: the consumer would reset it, releasing what it
// holds, for nothing.
func TestAnotherPadsResetLeavesAPadAlone(t *testing.T) {
	s := open(t, nil)
	a, addedA := attachRaw(t, s, "1st untouched pad", 2, 4, 0)
	b, addedB := attachRaw(t, s, "2nd reset pad", 2, 4, 0)
	mA := rawMapping(addedA.Info.GUID, "a:b0", "b:b1")
	mB := rawMapping(addedB.Info.GUID, "a:b2")
	if _, err := s.SetMappings([]string{mA, mB}); err != nil {
		t.Fatal(err)
	}
	untilAll(t, s, "both pads as gamepads", map[input.DeviceID]func(input.Event) bool{a.ID(): asGamepad, b.ID(): asGamepad})
	if err := a.SetRawButton(0, true); err != nil {
		t.Fatal(err)
	}
	until(t, s, a.ID(), "the press", isKind(input.ButtonDown))
	if _, err := s.SetMappings([]string{mA}); err != nil {
		t.Fatal(err)
	}
	until(t, s, b.ID(), "pad B removed", isKind(input.DeviceRemoved))
	until(t, s, b.ID(), "pad B as a joystick", isKind(input.DeviceAdded))
	// Pad A's release follows everything that the reload sent.
	if err := a.SetRawButton(0, false); err != nil {
		t.Fatal(err)
	}
	e := until(t, s, a.ID(), "the release", func(e input.Event) bool {
		if e.Kind == input.DeviceRemapped || e.Kind == input.DeviceRemoved {
			t.Errorf("pad A got %v although its mapping did not change", e.Kind)
		}
		return e.Kind == input.ButtonUp
	})
	if e.Control != "south" {
		t.Errorf("release = %+v, want south", e)
	}
}

// TestChangedMappingOfAPadIsStillReported: the same pad, with its mapping
// really changed, is still reported as remapped (the other half of the test
// above).
func TestChangedMappingOfAPadIsStillReported(t *testing.T) {
	s := open(t, nil)
	a, addedA := attachRaw(t, s, "changing pad", 2, 4, 0)
	if _, err := s.SetMappings([]string{rawMapping(addedA.Info.GUID, "a:b0", "b:b1")}); err != nil {
		t.Fatal(err)
	}
	until(t, s, a.ID(), "the gamepad", asGamepad)
	if _, err := s.SetMappings([]string{rawMapping(addedA.Info.GUID, "a:b1", "b:b0")}); err != nil {
		t.Fatal(err)
	}
	until(t, s, a.ID(), "DeviceRemapped", isKind(input.DeviceRemapped))
	// Taking another mapping away reloads SDL's database, and pad A's mapping
	// is added again: it is the same as before, so nothing is reported.
	other := input.VirtualJoystickGUID("some other pad")
	if _, err := s.SetMappings([]string{rawMapping(addedA.Info.GUID, "a:b1", "b:b0"), rawMapping(other, "a:b0")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMappings([]string{rawMapping(addedA.Info.GUID, "a:b1", "b:b0")}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetRawButton(1, true); err != nil {
		t.Fatal(err)
	}
	until(t, s, a.ID(), "the press", func(e input.Event) bool {
		if e.Kind == input.DeviceRemapped {
			t.Errorf("pad A is reported remapped by a reload that left its mapping as it was")
		}
		return e.Kind == input.ButtonDown
	})
}
