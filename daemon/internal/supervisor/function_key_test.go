package supervisor_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Tests of the function key of each controller (ADR-0012, ADR-0017): the
// slots share one profile, and each runs the Program that has the function key
// of its own controller.

// fnProfileSrc has a function set that rebinds south and east; rt, back and
// start have an ordinary binding in the base layer, which is ignored where the
// key is the function key.
const fnProfileSrc = `{"schemaVersion":1,"name":"fn","layers":{"base":{
  "south":{"press":{"type":"keys","keys":"A"}},
  "east":{"press":{"type":"keys","keys":"B"}},
  "start":{"press":{"type":"keys","keys":"S"}},
  "back":{"press":{"type":"keys","keys":"V"}},
  "rt":{"press":{"type":"keys","keys":"E"}}}},
 "functionSet":{"south":{"press":{"type":"keys","keys":"F1"}},"east":{"press":{"type":"keys","keys":"F2"}},
  "north":{"press":{"type":"keys","keys":"F3"}},"west":{"press":{"type":"keys","keys":"F4"}}}}`

func fnProgram(t testing.TB) *engine.Program {
	t.Helper()
	p, ds := profile.Parse([]byte(fnProfileSrc))
	if p == nil || ds.HasErrors() {
		t.Fatal(ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	return prog
}

// GUIDs of a Joy-Con 2 (R), a Joy-Con 2 pair and an Xbox pad, as padfamily
// reads them.
const (
	guidJoyConLeft  = "030000007e0500006720000000000000"
	guidJoyConRight = "030000007e0500006620000000000000"
	guidJoyConPair  = "030000007e0500006820000000000000"
	guidXbox        = "030000005e040000ff02000000000000"
)

func joyConRight(id input.DeviceID, serial string) *input.DeviceInfo {
	return &input.DeviceInfo{ID: id, Name: "Joy-Con 2 (R)", GUID: guidJoyConRight, Serial: serial, Type: "joyconright", IsGamepad: true}
}

func joyConPair(id input.DeviceID, serial string) *input.DeviceInfo {
	return &input.DeviceInfo{ID: id, Name: "Joy-Con 2 pair", GUID: guidJoyConPair, Serial: serial, Type: "joyconpair", IsGamepad: true}
}

func xbox(id input.DeviceID, serial string) *input.DeviceInfo {
	return &input.DeviceInfo{ID: id, Name: "Xbox", GUID: guidXbox, Serial: serial, Type: "xboxone", IsGamepad: true}
}

// padConfig is a config.json that tests replace as a committed config is.
type padConfig struct {
	cfg atomic.Pointer[profile.Config]
}

func newPadConfig() *padConfig {
	c := &padConfig{}
	c.cfg.Store(profile.DefaultConfig())
	return c
}

func (c *padConfig) functionKey(key string, info input.DeviceInfo) control.Control {
	return c.cfg.Load().PadSettings(key, input.FamilyOf(info)).FunctionKey
}

// setFunctionKey commits devices.<key>.functionKey = fk ("" takes it away).
func (c *padConfig) setFunctionKey(key, fk string) {
	next := *c.cfg.Load()
	next.Devices = map[string]profile.DeviceSettings{}
	for k, d := range c.cfg.Load().Devices {
		next.Devices[k] = d
	}
	d := next.Devices[key]
	d.FunctionKey = fk
	next.Devices[key] = d
	c.cfg.Store(&next)
}

func (h *harness) refreshFunctionKeys() {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	if err := h.sup.RefreshFunctionKeys(ctx); err != nil {
		h.t.Fatalf("RefreshFunctionKeys: %v", err)
	}
}

// fresh returns the output since the last call, as a trace, for tests that
// check one step at a time against the cumulative recorder.
func (h *harness) fresh(seen *string) string {
	h.t.Helper()
	all := h.rec.Trace()
	if len(all) < len(*seen) {
		h.t.Fatalf("output shrank: %q after %q", all, *seen)
	}
	out := strings.TrimSpace(all[len(*seen):])
	*seen = all
	return out
}

// slotKey returns the function key of the Program of the slot's view.
func (h *harness) slotFunctionKey(i int) control.Control {
	h.t.Helper()
	views, err := h.sup.Inspect(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	for _, v := range views {
		if int(v.Index) == i {
			return v.Mapping.ProgramFunctionKey
		}
	}
	h.t.Fatalf("no view of slot %d", i)
	return control.Invalid
}

// Two controllers at the same time with different function keys: a single Joy-Con
// has rt, an Xbox pad start. Each takes the function set under its own key,
// and the other's key is an ordinary control with the profile's binding.
func TestFunctionKeysOfTwoControllers(t *testing.T) {
	cfg := newPadConfig()
	var mu sync.Mutex
	var switched []string
	h := start(t, opts{slots: 2, program: fnProgram(t), functionKey: cfg.functionKey,
		onFunctionKey: func(slot uint8, key string, prog *engine.Program) {
			mu.Lock()
			defer mu.Unlock()
			switched = append(switched, string(rune('0'+slot))+" "+prog.FunctionKey().String())
		}})
	h.send(added(joyConRight(1, "j1")))
	h.send(added(xbox(2, "x1")))
	h.sync()
	if got := h.slotFunctionKey(0); got != control.RT {
		t.Fatalf("the Joy-Con's slot has %s; want rt", got)
	}
	if got := h.slotFunctionKey(1); got != control.Start {
		t.Fatalf("the Xbox pad's slot has %s; want start", got)
	}
	// Joy-Con: rt then south: F1. Then its start (ordinary) is S.
	h.send(axis(1, "righttrigger", 20000))
	h.send(button(1, "south", true))
	h.send(button(1, "south", false))
	h.send(axis(1, "righttrigger", 0))
	h.send(button(1, "start", true))
	h.send(button(1, "start", false))
	h.sync()
	h.expect("+F1@0 -F1@0 +S@0 -S@0")
	// Xbox: the same buttons the other way round.
	h.send(button(2, "start", true))
	h.send(button(2, "south", true))
	h.send(button(2, "south", false))
	h.send(button(2, "start", false))
	h.send(axis(2, "righttrigger", 20000))
	h.send(axis(2, "righttrigger", 0))
	h.sync()
	h.expect("+F1@0 -F1@0 +S@0 -S@0 +F1@0 -F1@0 +E@0 -E@0")
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"0 rt"}; !slices.Equal(switched, want) {
		t.Errorf("OnFunctionKey calls %q; want %q", switched, want)
	}
}

// A third controller that is connected later gets its key at attach, and a
// controller waiting in the queue takes the function key of its own, not of the
// slot it takes over.
func TestFunctionKeyAtAttachAndTakeOver(t *testing.T) {
	cfg := newPadConfig()
	h := start(t, opts{slots: 1, program: fnProgram(t), functionKey: cfg.functionKey})
	h.send(added(xbox(1, "x1")))
	h.send(added(joyConRight(2, "j1"))) // queued: the only slot is taken
	h.sync()
	if got := h.slotFunctionKey(0); got != control.Start {
		t.Fatalf("slot 0 has %s; want start", got)
	}
	// The Xbox pad leaves with start held: its keys end, and the Joy-Con takes
	// over the slot with its own function key.
	h.send(button(1, "start", true))
	h.send(button(1, "south", true))
	h.sync()
	h.expect("+F1@0")
	h.send(removed(1))
	h.sync()
	h.expect("+F1@0 -F1@0")
	if got := h.slotFunctionKey(0); got != control.RT {
		t.Fatalf("slot 0 has %s after the Joy-Con took it; want rt", got)
	}
	h.send(axis(2, "righttrigger", 20000))
	h.send(button(2, "east", true))
	h.send(button(2, "east", false))
	h.send(axis(2, "righttrigger", 0))
	h.sync()
	h.expect("+F1@0 -F1@0 +F2@0 -F2@0")
}

// Changing one controller's function key while its function key is held and
// another key of it is held: everything of that controller ends and nothing
// stays held; the other controller keeps its keys, its function key stays held.
func TestFunctionKeyChangeWhileHeld(t *testing.T) {
	cfg := newPadConfig()
	h := start(t, opts{slots: 2, program: fnProgram(t), functionKey: cfg.functionKey})
	h.send(added(xbox(1, "x1")))
	h.send(added(xbox(2, "x2")))
	// Controller 1 holds start, with south (F1) and east (F2) under it;
	// controller 2 holds start, with north (F3) and west (F4).
	h.send(button(1, "start", true))
	h.send(button(1, "south", true))
	h.send(button(1, "east", true))
	h.send(button(2, "start", true))
	h.send(button(2, "north", true))
	h.send(button(2, "west", true))
	h.sync()
	if got := len(h.owners()); got != 4 {
		t.Fatalf("%d owners before the change; want 4", got)
	}
	var seen string
	h.fresh(&seen)
	// The Xbox pad 1 gets back as its function key.
	cfg.setFunctionKey(guidXbox+"/s:x1", "back")
	h.refreshFunctionKeys()
	if got, want := h.fresh(&seen), "-F2@0 -F1@0"; got != want {
		t.Fatalf("the switch let go of %q; want only controller 1's keys, %q", got, want)
	}
	var holding []uint8
	for _, o := range h.owners() {
		holding = append(holding, o.Slot)
	}
	if want := []uint8{1, 1}; !slices.Equal(holding, want) {
		t.Fatalf("owners hold keys of slots %v; want only the other controller's (slot 1) %v", holding, want)
	}
	if h.slotFunctionKey(0) != control.Back || h.slotFunctionKey(1) != control.Start {
		t.Fatalf("function keys %s and %s; want back and start", h.slotFunctionKey(0), h.slotFunctionKey(1))
	}
	// Controller 2 is untouched: its start is still held, so south is still F1.
	h.send(button(2, "south", true))
	h.send(button(2, "south", false))
	// Controller 1: what was down does nothing until pressed again; start is now
	// ordinary.
	h.send(button(1, "south", false))
	h.send(button(1, "east", false))
	h.send(button(1, "start", false))
	h.send(button(1, "start", true))
	h.send(button(1, "start", false))
	h.send(button(1, "back", true))
	h.send(button(1, "south", true))
	h.send(button(1, "south", false))
	h.send(button(1, "back", false))
	h.sync()
	// Controller 1 sends S for start and F1 under back; the F1 of controller 2
	// (still under start) is the same key.
	if err := interleaved(h.fresh(&seen), "+F1@0 -F1@0", "+S@0 -S@0 +F1@0 -F1@0"); err != nil {
		t.Error(err)
	}
	for _, o := range h.owners() {
		if o.Slot == 0 {
			t.Errorf("slot 0 still holds %s", o)
		}
	}
	// Setting it to what it already is releases nothing.
	cfg.setFunctionKey(guidXbox+"/s:x1", "back")
	h.refreshFunctionKeys()
	if got := h.fresh(&seen); got != "" {
		t.Errorf("a refresh that changed nothing sent %q", got)
	}
	// The setting away brings start back.
	cfg.setFunctionKey(guidXbox+"/s:x1", "")
	h.refreshFunctionKeys()
	if got := h.slotFunctionKey(0); got != control.Start {
		t.Errorf("slot 0 has %s after the setting went; want start", got)
	}
	h.send(button(2, "start", false))
	h.send(button(2, "north", false))
	h.send(button(2, "west", false))
	h.sync()
}

// A single Joy-Con turns into a pair: the slot gets a controller of another
// family, its function key goes from rt to back, and what the Joy-Con held,
// the function key included, ends with it. The pair turning back is the same.
func TestFunctionKeyJoyConBecomesAPairAndBack(t *testing.T) {
	cfg := newPadConfig()
	h := start(t, opts{slots: 1, program: fnProgram(t), functionKey: cfg.functionKey})
	h.send(added(joyConRight(1, "j1")))
	h.send(axis(1, "righttrigger", 20000))
	h.send(button(1, "south", true))
	h.send(button(1, "east", true))
	h.sync()
	h.expect("+F1@0 +F2@0")
	// The two Joy-Cons are joined: the single one leaves, the pair comes.
	h.send(removed(1))
	h.send(added(joyConPair(2, "j1")))
	h.sync()
	h.expect("+F1@0 +F2@0 -F2@0 -F1@0")
	if got := h.slotFunctionKey(0); got != control.Back {
		t.Fatalf("the pair has %s; want back", got)
	}
	h.send(button(2, "back", true))
	h.send(button(2, "south", true))
	h.send(button(2, "south", false))
	h.send(button(2, "back", false))
	h.send(axis(2, "righttrigger", 20000)) // an ordinary control on the pair
	h.send(axis(2, "righttrigger", 0))
	h.send(button(2, "start", true)) // and start
	h.send(button(2, "start", false))
	h.sync()
	h.expect("+F1@0 +F2@0 -F2@0 -F1@0 +F1@0 -F1@0 +E@0 -E@0 +S@0 -S@0")
	// Held across the swap back: the pair splits, the Joy-Con is back.
	h.send(button(2, "back", true))
	h.send(button(2, "south", true))
	h.sync()
	h.send(removed(2))
	h.send(added(joyConRight(3, "j1")))
	h.sync()
	if got := h.slotFunctionKey(0); got != control.RT {
		t.Fatalf("the Joy-Con has %s; want rt", got)
	}
	var seen string
	h.fresh(&seen)
	h.send(axis(3, "righttrigger", 20000))
	h.send(button(3, "south", true))
	h.send(button(3, "south", false))
	h.send(axis(3, "righttrigger", 0))
	h.sync()
	if got := h.fresh(&seen); got != "+F1@0 -F1@0" {
		t.Errorf("the Joy-Con sent %q; want +F1@0 -F1@0", got)
	}
}

// The function key held when the controller is unplugged: the keys held under it
// end at the removal, and the slot starts clean for the next.
func TestFunctionKeyUnplugWhileHeld(t *testing.T) {
	cfg := newPadConfig()
	h := start(t, opts{slots: 1, program: fnProgram(t), functionKey: cfg.functionKey})
	h.send(added(joyConRight(1, "j1")))
	h.send(axis(1, "righttrigger", 20000))
	h.send(button(1, "south", true))
	h.sync()
	h.expect("+F1@0")
	h.send(removed(1))
	h.sync()
	h.expect("+F1@0 -F1@0")
	if got := len(h.owners()); got != 0 {
		t.Errorf("%d owners after the unplug", got)
	}
	views, err := h.sup.Inspect(context.Background())
	if err != nil || len(views) != 1 {
		t.Fatalf("Inspect = %v, %v", views, err)
	}
	if views[0].Mapping.FunctionKey != control.Invalid {
		t.Errorf("function key %s still held after the unplug", views[0].Mapping.FunctionKey)
	}
}

func joyConLeft(id input.DeviceID, serial string) *input.DeviceInfo {
	return &input.DeviceInfo{ID: id, Name: "Joy-Con 2 (L)", GUID: guidJoyConLeft, Serial: serial, Type: "joyconleft", IsGamepad: true}
}

// Two single Joy-Cons, the left one with its shoulder button (L) as the function
// key by its setting and the right one with its trigger (ZR) by default, each
// held with a key under it: the pause, a Reload to another profile and the
// removal of one let go of all their keys, and nothing is held afterwards (the
// invariant of ARCHITECTURE section 3, no stuck keys).
func TestFunctionKeyTriggerAndShoulderExitPaths(t *testing.T) {
	cfg := newPadConfig()
	cfg.setFunctionKey(guidJoyConLeft+"/s:l1", "leftshoulder")
	h := start(t, opts{slots: 2, program: fnProgram(t), functionKey: cfg.functionKey})
	h.send(added(joyConLeft(1, "l1")))
	h.send(added(joyConRight(2, "r1")))
	h.sync()
	if h.slotFunctionKey(0) != control.LeftShoulder || h.slotFunctionKey(1) != control.RT {
		t.Fatalf("function keys %s and %s; want leftshoulder and rt", h.slotFunctionKey(0), h.slotFunctionKey(1))
	}
	holdBoth := func() {
		h.send(button(1, "leftshoulder", true))
		h.send(button(1, "south", true))       // F1
		h.send(axis(2, "righttrigger", 32767)) // all the way: no second stage
		h.send(button(2, "east", true))        // F2
		h.sync()
	}
	releaseBoth := func() {
		h.send(button(1, "south", false))
		h.send(button(1, "leftshoulder", false))
		h.send(button(2, "east", false))
		h.send(axis(2, "righttrigger", 0))
		h.sync()
	}
	holdBoth()
	h.expectInterleaved("+F1@0", "+F2@0")
	// Pause: everything is let go; the function keys stay what the controllers
	// hold (a pause sends nothing and forgets nothing), and the releases send nothing.
	h.pause(true)
	if got := len(h.owners()); got != 0 {
		t.Errorf("%d owners after the pause", got)
	}
	releaseBoth()
	h.pause(false)
	h.expectInterleaved("+F1@0 -F1@0", "+F2@0 -F2@0")
	if got := len(h.owners()); got != 0 {
		t.Errorf("%d owners after the releases", got)
	}
	// Held again, then a saved profile (Reload): the keys end, the controllers
	// keep their function keys.
	var seen string
	h.fresh(&seen)
	holdBoth()
	if got := len(h.owners()); got != 2 {
		t.Fatalf("%d owners before the Reload; want 2", got)
	}
	h.reload(fnProgram(t), 2)
	if got := len(h.owners()); got != 0 {
		t.Errorf("%d owners after the Reload", got)
	}
	if h.slotFunctionKey(0) != control.LeftShoulder || h.slotFunctionKey(1) != control.RT {
		t.Errorf("after the Reload the keys are %s and %s; want leftshoulder and rt", h.slotFunctionKey(0), h.slotFunctionKey(1))
	}
	releaseBoth()
	// The left Joy-Con goes away with its function key held and a key under it.
	h.send(button(1, "leftshoulder", true))
	h.send(button(1, "south", true))
	h.sync()
	h.send(removed(1))
	h.sync()
	if got := len(h.owners()); got != 0 {
		t.Errorf("%d owners after the unplug", got)
	}
	views, err := h.sup.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.Mapping.FunctionKey != control.Invalid {
			t.Errorf("slot %d: function key %s still held at the end", v.Index, v.Mapping.FunctionKey)
		}
	}
}

// A Reload (a saved profile) keeps the function key of each controller.
func TestFunctionKeyReloadKeepsEachControllersKey(t *testing.T) {
	cfg := newPadConfig()
	h := start(t, opts{slots: 2, program: fnProgram(t), functionKey: cfg.functionKey})
	h.send(added(joyConRight(1, "j1")))
	h.send(added(xbox(2, "x1")))
	h.sync()
	h.reload(fnProgram(t), 2)
	if h.slotFunctionKey(0) != control.RT || h.slotFunctionKey(1) != control.Start {
		t.Fatalf("after the Reload the keys are %s and %s; want rt and start", h.slotFunctionKey(0), h.slotFunctionKey(1))
	}
	h.send(axis(1, "righttrigger", 20000))
	h.send(button(1, "south", true))
	h.send(button(1, "south", false))
	h.send(axis(1, "righttrigger", 0))
	h.sync()
	h.expect("+F1@0 -F1@0")
}

// Without Config.FunctionKey, RefreshFunctionKeys does nothing and every slot
// has the Program's key.
func TestRefreshFunctionKeysWithoutConfig(t *testing.T) {
	h := start(t, opts{slots: 1, program: fnProgram(t)})
	h.send(added(joyConRight(1, "j1")))
	h.sync()
	h.refreshFunctionKeys()
	if got := h.slotFunctionKey(0); got != control.Start {
		t.Errorf("function key %s; want the Program's start", got)
	}
}
