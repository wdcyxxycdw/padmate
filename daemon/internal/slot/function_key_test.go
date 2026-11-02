package slot_test

import (
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// The function key of a slot is that of its controller (ADR-0012, ADR-0017):
// the slot runs the variant of its profile's Program that has it, switches
// when the controller or the setting changes, and lets go of everything first.

// fnTestProfile has a function set that rebinds south, and an ordinary binding
// on east, start and rt in the base layer, which under their function key
// are ignored.
const fnTestProfile = `{"schemaVersion":1,"name":"fn","layers":{"base":{
  "south":{"press":{"type":"keys","keys":"A"}},
  "east":{"press":{"type":"keys","keys":"B"}},
  "start":{"press":{"type":"keys","keys":"S"}},
  "back":{"press":{"type":"keys","keys":"V"}},
  "rt":{"press":{"type":"keys","keys":"E"}}}},
 "functionSet":{"south":{"press":{"type":"keys","keys":"F1"}}}}`

// GUIDs of a Joy-Con 2 (R) and of a pair (vendor 057e, product 2066 and 2068),
// which input.FamilyOf and padfamily read as a single right Joy-Con and a pair.
const (
	guidJoyConLeft  = "030000007e0500006720000000000000"
	guidJoyConRight = "030000007e0500006620000000000000"
	guidJoyConPair  = "030000007e0500006820000000000000"
)

func compileFnProfile(t testing.TB) *engine.Program {
	t.Helper()
	p, ds := profile.Parse([]byte(fnTestProfile))
	if p == nil || ds.HasErrors() {
		t.Fatalf("profile.Parse: %v", ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	return prog
}

// fnHarness is a slot whose function key comes from a profile.Config, as
// padmated's does, with the Config swapped by the test like a committed
// config.json.
type fnHarness struct {
	*simtest.Sim
	s        *slot.Slot
	cfg      atomic.Pointer[profile.Config]
	switched []string // OnFunctionKey, "key:function key"
}

func newFnHarness(t *testing.T) *fnHarness {
	t.Helper()
	h := &fnHarness{Sim: simtest.New(t)}
	h.cfg.Store(profile.DefaultConfig())
	s, err := slot.New(slot.Config{
		Index: 0, Gen: 1, Program: compileFnProfile(t), Arbiter: h.Arb, Clock: h.Clock,
		FunctionKey: func(key string, info input.DeviceInfo) control.Control {
			return h.cfg.Load().PadSettings(key, input.FamilyOf(info)).FunctionKey
		},
		OnFunctionKey: func(key string, prog *engine.Program) {
			h.switched = append(h.switched, key+":"+prog.FunctionKey().String())
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.s = s
	t.Cleanup(func() {
		if errs := s.Errors(); len(errs) > 0 {
			t.Errorf("slot errors the test did not take: %v", errs)
		}
	})
	return h
}

// set commits a config.json whose devices.<key>.functionKey is fk.
func (h *fnHarness) set(key, fk string) {
	c := *h.cfg.Load()
	devices := map[string]profile.DeviceSettings{}
	for k, d := range c.Devices {
		devices[k] = d
	}
	d := devices[key]
	d.FunctionKey = fk
	devices[key] = d
	c.Devices = devices
	h.cfg.Store(&c)
}

func (h *fnHarness) add(t *testing.T, id input.DeviceID, key, guid, typ string) {
	t.Helper()
	h.s.Handle(input.Event{Kind: input.DeviceAdded, Device: id, AssignedKey: key,
		Info: &input.DeviceInfo{ID: id, Name: "pad", GUID: guid, Type: typ, IsGamepad: true}})
}

func (h *fnHarness) remove(id input.DeviceID) {
	h.s.Handle(input.Event{Kind: input.DeviceRemoved, Device: id})
}

func (h *fnHarness) button(t *testing.T, name string, down bool, at int) {
	t.Helper()
	h.at(t, at)
	kind := input.ButtonUp
	if down {
		kind = input.ButtonDown
	}
	h.s.Handle(input.Event{Kind: kind, Control: name})
}

// axis moves an axis at at ms: a trigger at 20000 is pressed (the virtual
// button lt or rt), at 0 released.
func (h *fnHarness) axis(t *testing.T, name string, value int16, at int) {
	t.Helper()
	h.at(t, at)
	h.s.Handle(input.Event{Kind: input.AxisMotion, Control: name, Value: value})
}

func (h *fnHarness) at(t *testing.T, msec int) {
	t.Helper()
	h.Clock.Set(clock.Time(time.Duration(msec) * ms))
}

func (h *fnHarness) programKey() control.Control { return h.s.View().Mapping.ProgramFunctionKey }

// A slot runs the function key of the controller it has: rt for a single
// right Joy-Con, start for a standard pad, the settings of the controller if it
// has any; a slot with no controller has the standard layout's.
func TestSlotFunctionKeyFollowsTheController(t *testing.T) {
	h := newFnHarness(t)
	if got := h.programKey(); got != control.Start {
		t.Fatalf("a slot without a controller has %s; want start", got)
	}
	const joy = guidJoyConRight + "/s:one"
	const xbox = "0300000000000000000000000000dead/p:x"
	h.add(t, 1, joy, guidJoyConRight, "joyconright")
	if got := h.programKey(); got != control.RT {
		t.Fatalf("a single right Joy-Con has %s; want rt", got)
	}
	// rt is the function key: it sends nothing, and under it south is F1;
	// start is an ordinary control.
	h.axis(t, "righttrigger", 20000, 10)
	h.button(t, "south", true, 20)
	h.button(t, "south", false, 30)
	h.axis(t, "righttrigger", 0, 40)
	h.button(t, "start", true, 50)
	h.button(t, "start", false, 60)
	h.ExpectOutput("+F1@20 -F1@30 +S@50 -S@60")
	h.remove(1)
	if got := h.programKey(); got != control.Start {
		t.Errorf("after the Joy-Con went, %s; want start", got)
	}
	// A standard pad on the same slot: start is the function key, rt is
	// ordinary.
	h.add(t, 2, xbox, "0300000000000000000000000000dead", "xboxone")
	h.axis(t, "righttrigger", 20000, 100)
	h.axis(t, "righttrigger", 0, 110)
	h.button(t, "start", true, 120)
	h.button(t, "south", true, 130)
	h.button(t, "south", false, 140)
	h.button(t, "start", false, 150)
	h.ExpectOutput("+E@100 -E@110 +F1@130 -F1@140")
	// The standard pad needs no switch: the slot already ran start.
	if want := []string{joy + ":rt", ":start"}; !slices.Equal(h.switched, want) {
		t.Errorf("OnFunctionKey calls %q; want %q", h.switched, want)
	}
}

// A function key the controller has set itself wins, and a Joy-Con that turns
// into a pair (a new controller on the slot, with the standard buttons) changes
// from rt to back with everything let go first.
func TestSlotFunctionKeyJoyConBecomesAPair(t *testing.T) {
	h := newFnHarness(t)
	const joy = guidJoyConRight + "/s:one"
	const pair = guidJoyConPair + "/s:one"
	h.add(t, 1, joy, guidJoyConRight, "joyconright")
	h.axis(t, "righttrigger", 20000, 10) // the function key
	h.button(t, "south", true, 20)       // F1, under it
	h.button(t, "east", true, 30)        // B
	h.ExpectOutput("+F1@20 +B@30")
	// The Joy-Con goes: the keys are let go, with the function key held.
	h.at(t, 40)
	h.remove(1)
	h.ExpectOutput("-B@40 -F1@40")
	if got := h.s.View().Mapping.FunctionKey; got != control.Invalid {
		t.Errorf("function key held %s after the removal", got)
	}
	// The pair has the standard layout: back is its default.
	h.add(t, 2, pair, guidJoyConPair, "joyconpair")
	if got := h.programKey(); got != control.Back {
		t.Fatalf("a pair has %s; want back", got)
	}
	h.button(t, "back", true, 100)
	h.button(t, "south", true, 110)
	h.button(t, "south", false, 120)
	h.button(t, "back", false, 130)
	h.button(t, "start", true, 140) // ordinary on a pair whose function key is back
	h.button(t, "start", false, 150)
	h.ExpectOutput("+F1@110 -F1@120 +S@140 -S@150")
	if want := []string{joy + ":rt", ":start", pair + ":back"}; !slices.Equal(h.switched, want) {
		t.Errorf("OnFunctionKey calls %q; want %q", h.switched, want)
	}
}

// RefreshFunctionKey with the key that applies already does nothing: the
// function key stays held and nothing is let go. With another key it lets go of
// everything first, the function key and a key held under it and a key held
// plain, and the keys it held send nothing more.
func TestSlotRefreshFunctionKey(t *testing.T) {
	h := newFnHarness(t)
	const key = "0300000000000000000000000000dead/p:x"
	h.add(t, 1, key, "0300000000000000000000000000dead", "xboxone")
	h.button(t, "south", true, 5)
	h.button(t, "start", true, 10) // the function key, held
	h.button(t, "east", true, 20)  // B
	h.ExpectOutput("+A@5 +B@20")
	h.at(t, 25)
	h.s.RefreshFunctionKey()
	h.ExpectOutput("")
	if got := h.s.View().Mapping.FunctionKey; got != control.Start {
		t.Fatalf("a refresh that changes nothing dropped the held function key: %s", got)
	}
	// The setting changes to back while start is held, south and east held.
	h.set(key, "back")
	h.at(t, 30)
	h.s.RefreshFunctionKey()
	h.ExpectOutput("-B@30 -A@30")
	simtest.AssertNoneHeld(t, h.Arb, h.Out)
	if got := h.programKey(); got != control.Back {
		t.Fatalf("function key %s after the refresh; want back", got)
	}
	if got := h.s.View().Mapping.FunctionKey; got != control.Invalid {
		t.Errorf("held function key %s after the switch; want none", got)
	}
	// What was down does nothing until it is pressed again; start is ordinary now.
	h.button(t, "east", false, 40)
	h.button(t, "south", false, 41)
	h.button(t, "start", false, 42)
	h.ExpectOutput("")
	h.button(t, "start", true, 50)
	h.button(t, "start", false, 51)
	h.button(t, "back", true, 60)
	h.button(t, "south", true, 61)
	h.button(t, "south", false, 62)
	h.button(t, "back", false, 63)
	h.ExpectOutput("+S@50 -S@51 +F1@61 -F1@62")
	// And back to the default: start again.
	h.set(key, "")
	h.at(t, 70)
	h.s.RefreshFunctionKey()
	if got := h.programKey(); got != control.Start {
		t.Errorf("function key %s after the setting went; want start", got)
	}
	if want := []string{key + ":back", key + ":start"}; !slices.Equal(h.switched, want) {
		t.Errorf("OnFunctionKey calls %q; want %q", h.switched, want)
	}
}

// The function key held while the controller is unplugged: everything held
// under it ends with the removal, the next controller starts clean.
func TestSlotFunctionKeyUnplugWhileHeld(t *testing.T) {
	h := newFnHarness(t)
	const joy = guidJoyConRight + "/s:one"
	h.add(t, 1, joy, guidJoyConRight, "joyconright")
	h.axis(t, "righttrigger", 20000, 10)
	h.button(t, "south", true, 20)
	h.ExpectOutput("+F1@20")
	h.at(t, 30)
	h.remove(1)
	h.ExpectOutput("-F1@30")
	simtest.AssertNoneHeld(t, h.Arb, h.Out)
	h.add(t, 1, joy, guidJoyConRight, "joyconright")
	// The same Joy-Con comes back with rt still held: it is the function
	// key from its next press only, as after any reset.
	if got := h.s.View().Mapping.FunctionKey; got != control.Invalid {
		t.Errorf("function key held %s after the Joy-Con came back", got)
	}
	h.axis(t, "righttrigger", 20000, 40)
	h.button(t, "south", true, 50)
	h.button(t, "south", false, 60)
	h.axis(t, "righttrigger", 0, 70)
	h.ExpectOutput("+F1@50 -F1@60")
}

// A single Joy-Con's function key held, as its trigger (ZL, by default) or its
// shoulder button (L, chosen), with a key held under it and another beside it,
// ends with every way out of the slot (invariant 1): the controller going away,
// the pause, a Reload to another profile, a change of the setting. After each,
// nothing is held, the function key is held or not as the engine's rules say,
// and its release sends nothing.
func TestSlotJoyConFunctionKeyExitPaths(t *testing.T) {
	const joyL = guidJoyConLeft + "/s:left"
	for _, tc := range []struct {
		name string
		fk   string // devices.<key>.functionKey, "" for the default
		hold string // the control that is the function key
		down func(h *fnHarness, t *testing.T, at int)
		up   func(h *fnHarness, t *testing.T, at int)
	}{
		{"ZL by default", "", "lt",
			func(h *fnHarness, t *testing.T, at int) { h.axis(t, "lefttrigger", 20000, at) },
			func(h *fnHarness, t *testing.T, at int) { h.axis(t, "lefttrigger", 0, at) }},
		{"ZL pulled all the way", "", "lt",
			func(h *fnHarness, t *testing.T, at int) { h.axis(t, "lefttrigger", 32767, at) },
			func(h *fnHarness, t *testing.T, at int) { h.axis(t, "lefttrigger", 0, at) }},
		{"L chosen", "leftshoulder", "leftshoulder",
			func(h *fnHarness, t *testing.T, at int) { h.button(t, "leftshoulder", true, at) },
			func(h *fnHarness, t *testing.T, at int) { h.button(t, "leftshoulder", false, at) }},
	} {
		// start returns a harness with the Joy-Con attached and everything held.
		start := func(t *testing.T) *fnHarness {
			h := newFnHarness(t)
			if tc.fk != "" {
				h.set(joyL, tc.fk)
			}
			h.add(t, 1, joyL, guidJoyConLeft, "joyconleft")
			if got := h.programKey().String(); got != tc.hold {
				t.Fatalf("function key of the program %s; want %s", got, tc.hold)
			}
			tc.down(h, t, 10)
			h.button(t, "south", true, 20) // F1, under the function key
			h.button(t, "east", true, 30)  // B, beside it
			h.ExpectOutput("+F1@20 +B@30")
			if got := h.s.View().Mapping.FunctionKey.String(); got != tc.hold {
				t.Fatalf("function key held %s; want %s", got, tc.hold)
			}
			return h
		}
		t.Run(tc.name+"/the controller goes away", func(t *testing.T) {
			h := start(t)
			h.at(t, 40)
			h.remove(1)
			h.ExpectOutput("-B@40 -F1@40")
			simtest.AssertNoneHeld(t, h.Arb, h.Out)
			if got := h.s.View().Mapping.FunctionKey; got != control.Invalid {
				t.Errorf("function key held %s after the removal", got)
			}
		})
		t.Run(tc.name+"/pause and resume", func(t *testing.T) {
			h := start(t)
			h.at(t, 40)
			if err := h.s.SetPaused(true); err != nil {
				t.Fatal(err)
			}
			h.ExpectOutput("-B@40 -F1@40")
			simtest.AssertNoneHeld(t, h.Arb, h.Out)
			if err := h.s.SetPaused(false); err != nil {
				t.Fatal(err)
			}
			// Resuming presses nothing again; the keys still down do nothing.
			h.button(t, "south", false, 50)
			h.button(t, "east", false, 51)
			tc.up(h, t, 52)
			h.ExpectOutput("")
			simtest.AssertNoneHeld(t, h.Arb, h.Out)
			// And the function key works again from its next press.
			tc.down(h, t, 60)
			h.button(t, "south", true, 70)
			h.button(t, "south", false, 80)
			tc.up(h, t, 90)
			h.ExpectOutput("+F1@70 -F1@80")
		})
		t.Run(tc.name+"/profile switch", func(t *testing.T) {
			h := start(t)
			h.at(t, 40)
			if err := h.s.Reload(compileFnProfile(t), 2); err != nil {
				t.Fatal(err)
			}
			h.ExpectOutput("-B@40 -F1@40")
			simtest.AssertNoneHeld(t, h.Arb, h.Out)
			if got := h.programKey().String(); got != tc.hold {
				t.Errorf("function key of the new program %s; want %s", got, tc.hold)
			}
			h.button(t, "south", false, 50)
			h.button(t, "east", false, 51)
			tc.up(h, t, 52)
			h.ExpectOutput("")
			simtest.AssertNoneHeld(t, h.Arb, h.Out)
		})
		t.Run(tc.name+"/the setting changes", func(t *testing.T) {
			h := start(t)
			next := "" // the default again, ZL
			if tc.fk == "" {
				next = "leftshoulder"
			}
			h.set(joyL, next)
			h.at(t, 40)
			h.s.RefreshFunctionKey()
			h.ExpectOutput("-B@40 -F1@40")
			simtest.AssertNoneHeld(t, h.Arb, h.Out)
			if got := h.s.View().Mapping.FunctionKey; got != control.Invalid {
				t.Errorf("function key held %s after the setting changed", got)
			}
			if got := h.programKey().String(); got == tc.hold {
				t.Errorf("function key of the program still %s", got)
			}
			// What was down does nothing, and the old key is an ordinary control now.
			h.button(t, "south", false, 50)
			h.button(t, "east", false, 51)
			tc.up(h, t, 52)
			h.ExpectOutput("")
			simtest.AssertNoneHeld(t, h.Arb, h.Out)
		})
	}
}

// A Reload to another profile keeps the function key of the controller, and a
// controller whose key is not the default still has it afterwards.
func TestSlotReloadKeepsTheControllerFunctionKey(t *testing.T) {
	h := newFnHarness(t)
	const joy = guidJoyConRight + "/s:one"
	h.add(t, 1, joy, guidJoyConRight, "joyconright")
	h.at(t, 10)
	if err := h.s.Reload(compileFnProfile(t), 2); err != nil {
		t.Fatal(err)
	}
	if got := h.programKey(); got != control.RT {
		t.Errorf("after the Reload the function key is %s; want rt", got)
	}
	h.axis(t, "righttrigger", 20000, 20)
	h.button(t, "south", true, 30)
	h.button(t, "south", false, 40)
	h.axis(t, "righttrigger", 0, 50)
	h.ExpectOutput("+F1@30 -F1@40")
	if strings.Join(h.switched, ",") != joy+":rt" {
		t.Errorf("OnFunctionKey calls %q; a Reload is not a switch", h.switched)
	}
}

// Without Config.FunctionKey the slot keeps the Program it was given, whatever
// the controller, and RefreshFunctionKey does nothing.
func TestSlotWithoutFunctionKeyConfig(t *testing.T) {
	sim := simtest.New(t)
	s, err := slot.New(slot.Config{Index: 0, Gen: 1, Program: compileFnProfile(t), Arbiter: sim.Arb, Clock: sim.Clock})
	if err != nil {
		t.Fatal(err)
	}
	s.Handle(input.Event{Kind: input.DeviceAdded, Device: 1, AssignedKey: "k", Info: &input.DeviceInfo{ID: 1, Type: "joyconright", IsGamepad: true}})
	s.RefreshFunctionKey()
	if got := s.View().Mapping.ProgramFunctionKey; got != control.Start {
		t.Errorf("function key %s without Config.FunctionKey; want the Program's, start", got)
	}
}

// The feedback switches of Config.Switches are those of the slot's controller:
// the same profile's haptic is played for one and not for the other.
func TestSlotSwitchesPerController(t *testing.T) {
	const bind = `"south": {"haptic": "tick", "press": {"type": "keys", "keys": "A", "mode": "tap"}}`
	sim := simtest.New(t)
	var asked []string
	var haptics []input.DeviceID
	s, err := slot.New(slot.Config{
		Index: 0, Gen: 1, Program: compile(t, bind), Arbiter: sim.Arb, Clock: sim.Clock,
		Switches: func(key string) profile.FeedbackSwitches {
			asked = append(asked, key)
			sw := profile.DefaultFeedbackSwitches()
			sw.Rumble = key != "quiet"
			return sw
		},
		OnHaptic: func(id input.DeviceID, _ engine.Haptic) { haptics = append(haptics, id) },
	})
	if err != nil {
		t.Fatal(err)
	}
	tap := func() {
		t.Helper()
		now := uint64(sim.Clock.Now())
		if err := s.Replay(sim.Clock, []input.Event{
			{Time: now, Kind: input.ButtonDown, Control: "south"},
			{Time: now + 50e6, Kind: input.ButtonUp, Control: "south"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	s.Handle(input.Event{Kind: input.DeviceAdded, Device: 1, AssignedKey: "quiet", Info: &input.DeviceInfo{ID: 1}})
	tap()
	s.Handle(input.Event{Kind: input.DeviceRemoved, Device: 1})
	s.Handle(input.Event{Kind: input.DeviceAdded, Device: 2, AssignedKey: "loud", Info: &input.DeviceInfo{ID: 2}})
	tap()
	if want := []input.DeviceID{2}; !slices.Equal(haptics, want) {
		t.Errorf("haptics on devices %v; want %v", haptics, want)
	}
	if !slices.Contains(asked, "quiet") || !slices.Contains(asked, "loud") {
		t.Errorf("Switches was asked for %q; want both controllers", asked)
	}
}
