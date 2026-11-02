package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/tray"
)

// The runtime half of the settings of each controller (ADR-0017): the function
// key, the feedback switches and the emergency combo that pads.set and
// config.json give one controller reach that controller's slot, effects and
// detection, and no other's. The two controllers are an Xbox pad (slot 0) and
// a Joy-Con 2 (R) (slot 1), as in the pads.list tests.

// padLiveProfile has a function set (south F1, east F2), a plain binding on
// every key that can be the function key, and a haptic on west.
const padLiveProfile = `{"schemaVersion":1,"name":"first","layers":{"base":{
  "south":{"press":{"type":"keys","keys":"A"}},
  "east":{"press":{"type":"keys","keys":"B"}},
  "west":{"haptic":"pulse2","press":{"type":"keys","keys":"F13","mode":"tap"}},
  "back":{"press":{"type":"keys","keys":"V"}},
  "start":{"press":{"type":"keys","keys":"S"}},
  "rt":{"press":{"type":"keys","keys":"E"}}}},
 "functionSet":{"south":{"press":{"type":"keys","keys":"F1"}},"east":{"press":{"type":"keys","keys":"F2"}}}}`

// startPadLive starts live mode with config and padLiveProfile, and connects
// the Xbox pad (device 1, slot 0) and the Joy-Con (device 2, slot 1).
func startPadLive(t *testing.T, config string) (*testLive, *mappingFake, *rpcTestConn) {
	t.Helper()
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "first", padLiveProfile)
	writeLastProfile(t, dir, "first")
	src := newMappingFake()
	tl := startLibraryLiveOpen(t, config, options{dataDir: dir, dryRun: true, tray: true}, func(input.Options) (eventSource, error) { return src, nil })
	src.events <- padAdded(1, padXboxGUID, "XB1", "Xbox Wireless Controller", "xboxone", []string{"south", "east", "west", "north", "back", "start", "leftshoulder", "rightshoulder", "leftstick", "rightstick"})
	src.events <- padAdded(2, padJoyConGUID, "XKW2", "Joy-Con 2 (R)", "joyconright", inputtest.JoyConRight("r").Buttons)
	tl.sync()
	return tl, src, &rpcTestConn{tl: tl, client: rpcTestClient(t, tl.liveRun), ctx: mappingCtx(t), t: t}
}

func edge(src *mappingFake, dev input.DeviceID, name string, down bool) {
	if name == "lt" || name == "rt" {
		// A trigger is an axis: pressed past its threshold, or let go.
		value := int16(0)
		if down {
			value = 20000
		}
		src.events <- input.Event{Device: dev, Kind: input.AxisMotion, Control: map[string]string{"lt": "lefttrigger", "rt": "righttrigger"}[name], Value: value}
		return
	}
	kind := input.ButtonUp
	if down {
		kind = input.ButtonDown
	}
	src.events <- input.Event{Device: dev, Kind: kind, Control: name}
}

// slotFunctionKey is the function key that slot i runs.
func (tl *testLive) slotFunctionKey(t *testing.T, i uint8) control.Control {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), liveWait)
	defer cancel()
	views, err := tl.sup.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.Index == i {
			return v.Mapping.ProgramFunctionKey
		}
	}
	t.Fatalf("no slot %d", i)
	return control.Invalid
}

// sent returns what the dry-run printer sent since the text before.
func (tl *testLive) sentSince(before string) string {
	return strings.TrimPrefix(tl.stdout.String(), before)
}

// TestPadsSetFunctionKeyAppliesToThatControllerOnly: the function key of a
// controller is its own. At attach each slot runs the key of its controller;
// pads.set changes one controller's key while its function key and another key
// are held, which lets go of that controller's keys at once and of no other's;
// the other controller keeps its function key and its held keys.
func TestPadsSetFunctionKeyAppliesToThatControllerOnly(t *testing.T) {
	tl, src, c := startPadLive(t, `{"schemaVersion":0}`)
	if x, j := tl.slotFunctionKey(t, 0), tl.slotFunctionKey(t, 1); x != control.Start || j != control.RT {
		t.Fatalf("function keys of the Xbox pad and the Joy-Con: %s, %s; want start and rt", x, j)
	}
	// The Xbox pad: start held, south under it (F1). The Joy-Con: rt held,
	// east under it (F2).
	edge(src, 1, "start", true)
	edge(src, 1, "south", true)
	edge(src, 2, "rt", true)
	edge(src, 2, "east", true)
	tl.waitFor(t, tl.stdout, "send +F1\n")
	tl.waitFor(t, tl.stdout, "send +F2\n")
	before := tl.stdout.String()

	var res rpcPadSet
	if err := c.call("pads.set", map[string]any{"key": padXboxKey, "functionKey": "back"}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Pad.FunctionKey.Effective != "back" {
		t.Fatalf("pads.set = %+v", res.Pad.FunctionKey)
	}
	after := tl.sentSince(before)
	if !strings.Contains(after, "-F1") {
		t.Fatalf("the Xbox pad's key was not let go of when its function key changed:\n%s", after)
	}
	if strings.Contains(after, "F2") {
		t.Fatalf("the Joy-Con's key was touched by the Xbox pad's setting:\n%s", after)
	}
	if x, j := tl.slotFunctionKey(t, 0), tl.slotFunctionKey(t, 1); x != control.Back || j != control.RT {
		t.Fatalf("function keys after pads.set: %s, %s; want back and rt", x, j)
	}
	// The Joy-Con still holds rt and east: its release lets go of F2.
	edge(src, 2, "east", false)
	tl.waitFor(t, tl.stdout, "send -F2\n")
	edge(src, 2, "rt", false)
	// The Xbox pad: start is an ordinary control now (S), back the function key.
	edge(src, 1, "south", false)
	edge(src, 1, "start", false)
	edge(src, 1, "start", true)
	edge(src, 1, "start", false)
	tl.waitFor(t, tl.stdout, "send +S\n")
	edge(src, 1, "back", true)
	edge(src, 1, "south", true)
	edge(src, 1, "south", false)
	edge(src, 1, "back", false)
	tl.waitForCount(t, tl.stdout, "send +F1\n", 2)

	// The same setting again lets go of nothing.
	edge(src, 2, "rt", true)
	edge(src, 2, "east", true) // F2 under the Joy-Con's function key
	tl.waitForCount(t, tl.stdout, "send +F2\n", 2)
	before = tl.stdout.String()
	if err := c.call("pads.set", map[string]any{"key": padXboxKey, "functionKey": "back"}, &res); err != nil {
		t.Fatal(err)
	}
	tl.sync()
	if got := tl.sentSince(before); strings.Contains(got, "send -") {
		t.Errorf("a setting that changed nothing let go of keys:\n%s", got)
	}
	// And the key the Joy-Con is given: R in place of ZR.
	if err := c.call("pads.set", map[string]any{"key": padJoyConKey, "functionKey": "rightshoulder"}, &res); err != nil {
		t.Fatal(err)
	}
	if got := tl.sentSince(before); !strings.Contains(got, "-F2") {
		t.Errorf("the Joy-Con's change did not let go of its F2:\n%s", got)
	}
	if got := tl.slotFunctionKey(t, 1); got != control.RightShoulder {
		t.Errorf("the Joy-Con runs %s; want rightshoulder", got)
	}
	edge(src, 2, "east", false)
	edge(src, 2, "rt", false)
	tl.cancel()
	if tl.waitCode(t) != 0 {
		t.Fatal(tl.stderr.String())
	}
	checkBalanced(t, tl.stdout.String())
}

// TestPadsFeedbackIsPerController: with rumble off for the Xbox pad and on
// for the Joy-Con, the same binding's haptic reaches only the Joy-Con; a
// switch that pads.set changes applies to that controller at once, and the
// other keeps its own.
func TestPadsFeedbackIsPerController(t *testing.T) {
	tl, src, c := startPadLive(t, `{"schemaVersion":0,"devices":{"`+padXboxKey+`":{"feedback":{"rumble":false}}}}`)
	press := func(dev input.DeviceID) {
		edge(src, dev, "west", true)
		edge(src, dev, "west", false)
	}
	press(1)
	press(2)
	tl.waitUntil(t, "the Joy-Con's haptic", func() bool { return len(playCalls(src.fakeSource, 2)) == 1 })
	tl.sync()
	if got := playCalls(src.fakeSource, 1); len(got) != 0 {
		t.Fatalf("the Xbox pad has rumble off and played %+v", got)
	}
	if got := playCalls(src.fakeSource, 2)[0]; got.Class != haptic.Event || got.Preset != haptic.Pulse2 {
		t.Errorf("the Joy-Con's haptic = %+v", got)
	}
	// Turn it on for the Xbox pad and off for the Joy-Con.
	var res rpcPadSet
	if err := c.call("pads.set", map[string]any{"key": padXboxKey, "feedback": map[string]any{"rumble": true}}, &res); err != nil {
		t.Fatal(err)
	}
	if err := c.call("pads.set", map[string]any{"key": padJoyConKey, "feedback": map[string]any{"rumble": false}}, &res); err != nil {
		t.Fatal(err)
	}
	press(1)
	press(2)
	tl.waitUntil(t, "the Xbox pad's haptic", func() bool { return len(playCalls(src.fakeSource, 1)) == 1 })
	tl.sync()
	if got := playCalls(src.fakeSource, 2); len(got) != 1 {
		t.Errorf("the Joy-Con has rumble off and played %d times in all", len(got))
	}
}

// batteryNotices returns the low-battery notices queued so far: the profile of
// these tests binds buttons that one controller or the other lacks, which the
// tray tells as well.
func batteryNotices(tl *testLive) []tray.Notification {
	var got []tray.Notification
	for _, n := range drainNotices(tl) {
		if strings.HasPrefix(n.Key, "battery:") {
			got = append(got, n)
		}
	}
	return got
}

// The low-battery alert of a controller follows its own lowBattery switch: the
// one with it off gets no alert and no notice, the other gets both.
func TestPadsLowBatteryIsPerController(t *testing.T) {
	tl, src, c := startPadLive(t, `{"schemaVersion":0,"devices":{"`+padXboxKey+`":{"feedback":{"lowBattery":false}}}}`)
	src.events <- powerEvent(1, input.PowerDischarging, 10)
	src.events <- powerEvent(2, input.PowerDischarging, 10)
	tl.waitUntil(t, "the Joy-Con's alert", func() bool { return len(alerts(src.fakeSource, 2)) == 1 })
	flushEffects(t, tl, src.fakeSource)
	if got := alerts(src.fakeSource, 1); len(got) != 0 {
		t.Fatalf("the Xbox pad has lowBattery off and was alerted: %+v", got)
	}
	notices := batteryNotices(tl)
	if len(notices) != 1 || !strings.Contains(notices[0].Body, "Joy-Con 2 (R)") {
		t.Fatalf("notices = %+v; want only the Joy-Con's", notices)
	}
	// The Xbox pad's episode was silent: turning the switch on does not
	// announce it, and the next one is announced.
	var res rpcPadSet
	if err := c.call("pads.set", map[string]any{"key": padXboxKey, "feedback": map[string]any{"lowBattery": true}}, &res); err != nil {
		t.Fatal(err)
	}
	src.events <- powerEvent(1, input.PowerCharging, 50)
	src.events <- powerEvent(1, input.PowerDischarging, 12)
	tl.waitUntil(t, "the Xbox pad's next alert", func() bool { return len(alerts(src.fakeSource, 1)) == 1 })
	if got := alerts(src.fakeSource, 2); len(got) != 1 {
		t.Errorf("the Joy-Con was alerted again: %+v", got)
	}
}

// The emergency combo of a controller is its own: the pad that has one set
// detects it, the other keeps its family's (status.get shows what fires), and
// pads.set changes one without repicking the other.
func TestPadsComboIsPerController(t *testing.T) {
	tl, _, c := startPadLive(t, `{"schemaVersion":0,"devices":{"`+padXboxKey+`":{"panic":{"buttons":["start","north"],"holdMs":700}}}}`)
	settings := tl.selectionConfig()
	xbox := input.DeviceInfo{ID: 1, GUID: padXboxGUID, Serial: "XB1", Type: "xboxone", IsGamepad: true}
	joy := input.DeviceInfo{ID: 2, GUID: padJoyConGUID, Serial: "XKW2", Type: "joyconright", IsGamepad: true, ButtonMask: buttonMaskOf(inputtest.JoyConRight("r").Buttons)}
	family, got := effectiveCombo(settings, xbox)
	if family != "standard" || strings.Join(got.Buttons, "+") != "start+north" || got.Hold != 700*time.Millisecond {
		t.Errorf("Xbox combo = %s %+v", family, got)
	}
	family, got = effectiveCombo(settings, joy)
	if family != "joyconRight" || strings.Join(got.Buttons, "+") != "paddle3+paddle1" || got.Hold != input.ComboHold {
		t.Errorf("Joy-Con combo = %s %+v", family, got)
	}
	var res rpcPadSet
	if err := c.call("pads.set", map[string]any{"key": padJoyConKey, "panic": map[string]any{"buttons": []string{"rightshoulder", "south"}, "holdMs": 900}}, &res); err != nil {
		t.Fatal(err)
	}
	settings = tl.selectionConfig()
	if _, got = effectiveCombo(settings, joy); strings.Join(got.Buttons, "+") != "rightshoulder+south" || got.Hold != 900*time.Millisecond {
		t.Errorf("Joy-Con combo after pads.set = %+v", got)
	}
	if _, got = effectiveCombo(settings, xbox); strings.Join(got.Buttons, "+") != "start+north" {
		t.Errorf("Xbox combo after the Joy-Con's = %+v", got)
	}
	// A controller that lacks a button of its combo keeps the family's.
	slim := xbox
	slim.ButtonMask = buttonMaskOf([]string{"south", "start"})
	if _, got = effectiveCombo(settings, slim); strings.Join(got.Buttons, "+") != "back+start" {
		t.Errorf("a pad without north = %+v; want back+start", got)
	}
}

// applyLiveSettings looks at each controller's settings: a change that is no
// function key, combo or switch of any controller makes none of the three
// compare as different, so it restarts nothing and lets go of nothing.
func TestLiveSettingsCompareThePerControllerSettings(t *testing.T) {
	base, _ := profile.ParseConfig([]byte(`{"schemaVersion":0,"devices":{"` + padXboxKey + `":{"functionKey":"back","feedback":{"rumble":false},"panic":{"buttons":["start","north"],"holdMs":700}}}}`))
	same := func(a, b *profile.Config, what string) {
		t.Helper()
		if !reflect.DeepEqual(functionKeySettings(a), functionKeySettings(b)) || !reflect.DeepEqual(comboSettings(a), comboSettings(b)) || !reflect.DeepEqual(feedbackSettings(a), feedbackSettings(b)) {
			t.Errorf("%s compares as a change of a controller's settings", what)
		}
	}
	other, _ := profile.ParseConfig([]byte(`{"schemaVersion":0,"output":{"eventGapMs":5},"devices":{"` + padXboxKey + `":{"functionKey":"back","feedback":{"rumble":false},"panic":{"buttons":["start","north"],"holdMs":700},"calibration":{"ls":{"deadzone":0.2}}},"` + padJoyConKey + `":{"calibration":{"rs":{"deadzone":0.3}}}}}`))
	same(base, other, "a calibration, another controller's entry and the output")
	// Each setting, alone, is seen.
	for name, text := range map[string]string{
		"function key": `{"schemaVersion":0,"devices":{"` + padXboxKey + `":{"functionKey":"start","feedback":{"rumble":false},"panic":{"buttons":["start","north"],"holdMs":700}}}}`,
		"feedback":     `{"schemaVersion":0,"devices":{"` + padXboxKey + `":{"functionKey":"back","feedback":{"rumble":true},"panic":{"buttons":["start","north"],"holdMs":700}}}}`,
		"combo":        `{"schemaVersion":0,"devices":{"` + padXboxKey + `":{"functionKey":"back","feedback":{"rumble":false},"panic":{"buttons":["start","north"],"holdMs":800}}}}`,
		"another controller": `{"schemaVersion":0,"devices":{"` + padXboxKey + `":{"functionKey":"back","feedback":{"rumble":false},"panic":{"buttons":["start","north"],"holdMs":700}},"` +
			padJoyConKey + `":{"functionKey":"rightshoulder"}}}`,
		"the retired function key": `{"schemaVersion":0,"functionKey":"back","devices":{"` + padXboxKey + `":{"functionKey":"back","feedback":{"rumble":false},"panic":{"buttons":["start","north"],"holdMs":700}}}}`,
	} {
		next, ds := profile.ParseConfig([]byte(text))
		if next == nil {
			t.Fatalf("%s: %v", name, ds)
		}
		changed := !reflect.DeepEqual(functionKeySettings(base), functionKeySettings(next)) ||
			!reflect.DeepEqual(comboSettings(base), comboSettings(next)) || !reflect.DeepEqual(feedbackSettings(base), feedbackSettings(next))
		if !changed {
			t.Errorf("a change of the %s is not seen", name)
		}
	}
}

// profiles.explain follows the function key of each controller: the entry of a
// slot has the key its Program has, whether the control is that key, and the
// set it belongs to; a library profile has one entry for each function key the
// known controllers have.
func TestExplainShowsEachControllersFunctionKey(t *testing.T) {
	_, _, c := startPadLive(t, `{"schemaVersion":0}`)
	explain := func(params map[string]any) rpcExplanation {
		t.Helper()
		var res rpcExplanation
		if err := c.call("profiles.explain", params, &res); err != nil {
			t.Fatal(err)
		}
		return res
	}
	x := explain(map[string]any{"device": "1", "control": "start"})
	if len(x.Slots) != 1 || x.Slots[0].FunctionKey != "start" || !x.Slots[0].IsFunctionKey || x.FunctionKey != "start" || !x.IsFunctionKey || x.Slots[0].FunctionSet.Layer != "function:start" {
		t.Errorf("start on the Xbox pad: %+v", x)
	}
	j := explain(map[string]any{"device": "2", "control": "rt"})
	if len(j.Slots) != 1 || j.Slots[0].FunctionKey != "rt" || !j.Slots[0].IsFunctionKey || j.Slots[0].FunctionSet.Layer != "function:rt" {
		t.Errorf("rt on the Joy-Con: %+v", j)
	}
	j = explain(map[string]any{"device": "2", "control": "start"})
	if len(j.Slots) != 1 || j.Slots[0].FunctionKey != "rt" || j.Slots[0].IsFunctionKey {
		t.Errorf("start on the Joy-Con is an ordinary control: %+v", j)
	}
	lib := explain(map[string]any{"id": "first", "control": "south"})
	if len(lib.Slots) != 2 || lib.Slots[0].FunctionKey != "start" || lib.Slots[1].FunctionKey != "rt" ||
		len(lib.Slots[0].Controllers) != 1 || lib.Slots[0].Controllers[0] != padXboxKey || lib.Slots[1].Controllers[0] != padJoyConKey || lib.FunctionKey != "start" {
		t.Errorf("library profile: %+v", lib)
	}
}
