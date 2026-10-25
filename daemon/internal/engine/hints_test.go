package engine_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Tests of the key hints (HUD-03, ADR-0063): a HUD hints action is held like a
// precision action, ends on every path that ends a held action, and while it
// holds the engine keeps the list of what the controls do in the current layer
// stack.

// hsim is an Engine driven in virtual time, one millisecond per step.
type hsim struct {
	t   *testing.T
	e   *engine.Engine
	out *recorder
	now clock.Time
}

func newHsim(t *testing.T, src string) *hsim {
	t.Helper()
	out := &recorder{}
	return &hsim{t: t, e: engine.New(compileLayers(t, src), out, engine.Options{}), out: out}
}

// step moves the clock by d, running the deadlines on the way.
func (h *hsim) step(d time.Duration) {
	h.t.Helper()
	target := h.now.Add(d)
	for {
		dl, ok := h.e.NextDeadline()
		if !ok || dl > target {
			break
		}
		h.now = max(dl, h.now)
		h.out.now = h.now
		h.e.Apply(h.now)
	}
	h.now = target
	h.out.now = target
}

func (h *hsim) button(name string, down bool) {
	h.t.Helper()
	h.step(time.Millisecond)
	c, ok := control.Parse(name)
	if !ok {
		h.t.Fatalf("unknown control %q", name)
	}
	h.e.Step(engine.Input{T: h.now, Kind: engine.InputButton, Control: c, Down: down})
}

func (h *hsim) press(name string)   { h.t.Helper(); h.button(name, true) }
func (h *hsim) release(name string) { h.t.Helper(); h.button(name, false) }

// shown is the hints as text, "control=label" apart by " | ", or "off" when
// they are off.
func (h *hsim) shown() string {
	h.t.Helper()
	return hintsText(h.e.View().Hints)
}

func hintsText(hs []engine.Hint) string {
	if hs == nil {
		return "off"
	}
	parts := make([]string, len(hs))
	for i, h := range hs {
		parts[i] = h.Control.String() + "=" + h.Label
	}
	return "on:" + strings.Join(parts, " | ")
}

// wantShown fails the test unless the hints read want.
func (h *hsim) wantShown(when, want string) {
	h.t.Helper()
	if got := h.shown(); got != want {
		h.t.Errorf("%s: hints %s\nwant %s", when, got, want)
	}
}

// hintsProfile has the hints on rightshoulder, a stack of layers, and one of
// each kind of label: a label, a shortcut, a sequence, a script, a macro, a
// layer, a precision action, and a binding with nothing to show.
const hintsProfile = `{"schemaVersion":1,"name":"stack","defaultLayer":"base","script":{"entry":"main.ts","api":"0.21"},
"common":{"misc4":{"label":"通用","press":{"type":"keys","keys":"F1"}},"guide":{"press":{"type":"keys","keys":"F2"}}},
"layers":{
"base":{
 "rightshoulder":{"label":"提示","press":{"type":"hud","op":"hints"}},
 "south":{"label":"撤销","press":{"type":"keys","keys":"Ctrl+Z"}},
 "east":{"press":{"type":"keys","keys":"Space"}},
 "west":{"press":{"type":"script","name":"brush"}},
 "north":{"tap":{"type":"keys","keys":"Ctrl+S","mode":"tap"},"hold":{"type":"layer","layer":"fn","op":"hold"}},
 "misc5":{"press":{"type":"macro","file":"复制.ts"}},
 "leftshoulder":{"press":{"type":"mouse","op":"precision"}},
 "leftstick":{"press":{"type":"layer","layer":"fn","op":"hold"}},
 "rightstick":{"press":{"type":"layer","layer":"fn","op":"toggle"}},
 "misc1":{"press":{"type":"layer","layer":"fn","op":"switch"}},
 "misc2":{"press":{"type":"layer","layer":"base","op":"switch"}},
 "dpad.up":{"label":"  多\n行 "},
 "dpad.down":{},
 "dpad.left":{"press":{"type":"keys","keys":["Ctrl+K","S"],"mode":"tap"}},
 "dpad.right":{"press":{"type":"layer","layers":["base","fn"],"op":"cycle"}},
 "lt":{"press":{"type":"keys","keys":"Alt"}}
},
"fn":{
 "south":{"label":"重做","press":{"type":"keys","keys":"Ctrl+Y"}},
 "east":{"block":true},
 "misc4":{"label":"fn 返回","press":{"type":"keys","keys":"F5"}},
 "misc3":{"press":{"type":"layer","layer":"extra","op":"hold"}},
 "lt":{"press":{"type":"keys","keys":"Shift"}}
},
"extra":{
 "south":{"label":"额外"},
 "west":{"block":true}
}}}`

// The hints are on while the control is held, send nothing, and are gone at the
// release.
func TestHintsWhileHeld(t *testing.T) {
	h := newHsim(t, hintsProfile)
	h.wantShown("before", "off")
	h.press("rightshoulder")
	got := h.shown()
	if !strings.HasPrefix(got, "on:") {
		t.Fatalf("hints %s after the press", got)
	}
	h.step(time.Second)
	if h.shown() != got {
		t.Errorf("the hints changed while held: %s then %s", got, h.shown())
	}
	h.release("rightshoulder")
	h.wantShown("after the release", "off")
	if h.out.String() != "" {
		t.Errorf("the hints sent %q", h.out.String())
	}
	if dl, ok := h.e.NextDeadline(); ok {
		t.Errorf("a deadline at %v is left", time.Duration(dl))
	}
	h.press("rightshoulder")
	if !strings.HasPrefix(h.shown(), "on:") {
		t.Errorf("hints %s at the second press", h.shown())
	}
	h.release("rightshoulder")
	h.wantShown("after the second release", "off")
}

// The lists hintsProfile gives, in control order. The control that holds the
// hints (rightshoulder) is not in them.
const (
	hintsOfBase = "on:south=撤销 | east=Space | west=brush | north=Ctrl+S | guide=F2" +
		" | leftstick=层：fn | rightstick=层：fn | leftshoulder=精确模式 | dpad.up=多 行 | dpad.left=Ctrl+K, S" +
		" | dpad.right=层：base/fn | misc1=层：fn | misc2=层：base | misc4=通用 | misc5=复制 | lt=Alt"
	// fn over the base: fn's south and misc4 win, east is blocked, lt is fn's.
	hintsOfFnOverlay = "on:south=重做 | west=brush | north=Ctrl+S | guide=F2" +
		" | leftstick=层：fn | rightstick=层：fn | leftshoulder=精确模式 | dpad.up=多 行 | dpad.left=Ctrl+K, S" +
		" | dpad.right=层：base/fn | misc1=层：fn | misc2=层：base | misc3=层：extra | misc4=fn 返回 | misc5=复制 | lt=Shift"
	// fn as the base layer: only fn and the common bindings.
	hintsOfFnBase = "on:south=重做 | guide=F2 | misc3=层：extra | misc4=fn 返回 | lt=Shift"
	// extra over fn over the base: extra's label-only south wins, west is blocked.
	hintsOfFnExtra = "on:south=额外 | north=Ctrl+S | guide=F2" +
		" | leftstick=层：fn | rightstick=层：fn | leftshoulder=精确模式 | dpad.up=多 行 | dpad.left=Ctrl+K, S" +
		" | dpad.right=层：base/fn | misc1=层：fn | misc2=层：base | misc3=层：extra | misc4=fn 返回 | misc5=复制 | lt=Shift"
)

// The list has the label of each control's resolved binding, or the text of its
// first action; leaves out the control that holds the hints, a binding with
// nothing to show and a blocked control; and is in control order.
func TestHintsListOfTheBaseLayer(t *testing.T) {
	h := newHsim(t, hintsProfile)
	h.press("rightshoulder")
	h.wantShown("base layer", hintsOfBase)
}

// The list follows the layer stack while the hints are on: an overlay held, a
// layer toggled, the base switched, a cycle, and a stack of two overlays with the
// top one winning; a blocked control disappears and a control the overlay lacks
// falls through to the layer below.
func TestHintsFollowTheLayerStack(t *testing.T) {
	for _, tc := range []struct {
		name string
		// on changes the stack, off changes it back (nil: it stays).
		on, off func(h *hsim)
		want    string
	}{
		{"hold", func(h *hsim) { h.press("leftstick") }, func(h *hsim) { h.release("leftstick") }, hintsOfFnOverlay},
		{"toggle", func(h *hsim) { h.press("rightstick"); h.release("rightstick") }, func(h *hsim) { h.press("rightstick"); h.release("rightstick") }, hintsOfFnOverlay},
		{"switch", func(h *hsim) { h.press("misc1"); h.release("misc1") }, func(h *hsim) { _ = h.e.ChangeLayer("switch", "base") }, hintsOfFnBase},
		{"cycle", func(h *hsim) { h.press("dpad.right"); h.release("dpad.right") }, func(h *hsim) { _ = h.e.ChangeLayer("switch", "base") }, hintsOfFnBase},
		{"a hold gesture", func(h *hsim) { h.press("north"); h.step(400 * time.Millisecond) }, func(h *hsim) { h.release("north") }, hintsOfFnOverlay},
		{"script enable", func(h *hsim) { _ = h.e.ChangeLayer("enable", "fn") }, func(h *hsim) { _ = h.e.ChangeLayer("disable", "fn") }, hintsOfFnOverlay},
		{"script switch", func(h *hsim) { _ = h.e.ChangeLayer("switch", "fn") }, func(h *hsim) { _ = h.e.ChangeLayer("switch", "base") }, hintsOfFnBase},
		{"script hold", func(h *hsim) {
			release, err := h.e.HoldLayer("fn")
			if err != nil {
				h.t.Fatal(err)
			}
			h.t.Cleanup(release)
		}, nil, hintsOfFnOverlay},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHsim(t, hintsProfile)
			h.press("rightshoulder")
			h.wantShown("before", hintsOfBase)
			tc.on(h)
			h.wantShown("with the layer on", tc.want)
			if tc.off != nil {
				tc.off(h)
				h.wantShown("with the layer off", hintsOfBase)
			}
			h.release("rightshoulder")
			h.wantShown("after the release", "off")
		})
	}

	t.Run("two overlays, top wins", func(t *testing.T) {
		h := newHsim(t, hintsProfile)
		h.press("rightshoulder")
		h.press("leftstick") // fn
		h.press("misc3")     // extra, on top of fn
		h.wantShown("fn and extra", hintsOfFnExtra)
		h.release("misc3")
		h.wantShown("extra released", hintsOfFnOverlay)
		h.release("leftstick")
		h.wantShown("fn released", hintsOfBase)
	})

	// A change of the stack while the hints are off leaves them off; they read
	// the stack that is current when they turn on.
	t.Run("hints turn on over a layer", func(t *testing.T) {
		h := newHsim(t, hintsProfile)
		h.press("leftstick")
		h.wantShown("a layer, no hints", "off")
		h.press("rightshoulder")
		h.wantShown("hints over the layer", hintsOfFnOverlay)
	})
}

// A hint of a layer action names the layer; a cycle names them all.
func TestHintsActionTexts(t *testing.T) {
	const src = `{"schemaVersion":1,"name":"texts","layers":{"base":{
 "rightshoulder":{"press":{"type":"hud","op":"hints"}},
 "south":{"press":{"type":"profile","profile":"paint"}},
 "east":{"press":{"type":"mouse","op":"jump","x":0.5,"y":0.5}},
 "west":{"press":{"type":"macro","file":"copy all.js"}},
 "north":{"hold":{"type":"hud","op":"hints"}},
 "misc2":{"release":{"type":"keys","keys":"F7"},"tap":{"type":"keys","keys":"F6","mode":"tap"}},
 "misc3":{"doubleTap":{"type":"keys","keys":"F8","mode":"tap"},"release":{"type":"keys","keys":"F9"}},
 "guide":{"label":"label wins","press":{"type":"keys","keys":"F10"}},
 "misc1":{"hold":{"type":"keys","keys":"F11"},"doubleTap":{"type":"keys","keys":"F12","mode":"tap"}}
}}}`
	h := newHsim(t, src)
	h.press("rightshoulder")
	h.wantShown("texts", "on:"+strings.Join([]string{
		"south=配置：paint",
		"east=鼠标跳转",
		"west=copy all",
		"north=按键提示",
		"guide=label wins",
		"misc1=F11",
		"misc2=F6",
		"misc3=F8", // press and tap look first, then hold, then the multi-taps, the release last
	}, " | "))
}

// A label is one line of at most 64 characters: control characters go, runs of
// white space become one space, and a longer one is cut with an ellipsis.
func TestHintsLabelIsCleaned(t *testing.T) {
	long := strings.Repeat("长", 70)
	src := `{"schemaVersion":1,"name":"clean","layers":{"base":{
 "rightshoulder":{"press":{"type":"hud","op":"hints"}},
 "south":{"label":"\t a \u0000b\u0007\r\n  c  d ","press":{"type":"keys","keys":"A"}},
 "east":{"label":"` + long + `","press":{"type":"keys","keys":"B"}},
 "west":{"label":"` + strings.Repeat("字", 64) + `","press":{"type":"keys","keys":"C"}},
 "north":{"label":" \u0001 ","press":{"type":"keys","keys":"D"}}
}}}`
	h := newHsim(t, src)
	h.press("rightshoulder")
	hs := h.e.View().Hints
	want := map[string]string{
		"south": "a b c d",
		"east":  strings.Repeat("长", 63) + "…",
		"west":  strings.Repeat("字", 64),
		"north": "D", // nothing is left of its label: the shortcut shows
	}
	got := map[string]string{}
	for _, hint := range hs {
		got[hint.Control.String()] = hint.Label
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s = %q, want %q", name, got[name], w)
		}
	}
	for _, hint := range hs {
		if utf8.RuneCountInString(hint.Label) > 64 {
			t.Errorf("%s: a label of %d characters", hint.Control, utf8.RuneCountInString(hint.Label))
		}
	}
}

// The hints end on every path that ends a held action; the control stays down
// after the ones that cancel, and then does nothing when it is let go.
func TestHintsEndOnEveryExit(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  func(h *hsim)
	}{
		{"release", func(h *hsim) { h.release("rightshoulder") }},
		{"cancel", func(h *hsim) { h.e.CancelAll(h.now) }},
		{"pause", func(h *hsim) { h.e.Pause(h.now) }},
		{"reload", func(h *hsim) { h.e.Reload(h.now, compileLayers(t, hintsProfile)) }},
		{"reset", func(h *hsim) { h.e.Reset(h.now) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHsim(t, hintsProfile)
			h.press("rightshoulder")
			h.press("leftstick") // an overlay: the list is not the base one
			if !strings.HasPrefix(h.shown(), "on:") {
				t.Fatalf("hints %s", h.shown())
			}
			tc.end(h)
			h.wantShown("after "+tc.name, "off")
			// Nothing brings them back: not the layer going away, not the other
			// control's release, not the held control's own release, not time.
			h.release("leftstick")
			h.step(time.Second)
			h.wantShown("after the other release", "off")
			h.release("rightshoulder")
			h.wantShown("after the held control's release", "off")
			if h.out.String() != "" {
				t.Errorf("sent %q", h.out.String())
			}
			// A paused engine does not turn them on again; the others do.
			if tc.name == "pause" {
				h.e.Resume()
			}
			h.press("rightshoulder")
			if !strings.HasPrefix(h.shown(), "on:") {
				t.Errorf("hints %s at the next press", h.shown())
			}
			h.release("rightshoulder")
			h.wantShown("at the end", "off")
		})
	}
}

// While paused a press runs nothing, so it shows no hints; resuming does not
// press again.
func TestHintsWhilePaused(t *testing.T) {
	h := newHsim(t, hintsProfile)
	h.e.Pause(0)
	h.press("rightshoulder")
	h.wantShown("a press while paused", "off")
	h.e.Resume()
	h.wantShown("resumed with the control down", "off")
	h.release("rightshoulder")
	h.press("rightshoulder")
	if !strings.HasPrefix(h.shown(), "on:") {
		t.Errorf("hints %s", h.shown())
	}
}

// A Reload while the hints are on ends them, and the new program's hints come
// with the next press.
func TestHintsReloadUsesTheNewProgram(t *testing.T) {
	h := newHsim(t, hintsProfile)
	h.press("rightshoulder")
	const other = `{"schemaVersion":1,"name":"other","layers":{"base":{"rightshoulder":{"press":{"type":"hud","op":"hints"}},"south":{"label":"新"}}}}`
	h.e.Reload(h.now, compileLayers(t, other))
	h.wantShown("reloaded", "off")
	h.release("rightshoulder")
	h.press("rightshoulder")
	h.wantShown("next press", "on:south=新")
}

// A hold gesture shows the hints from the moment it is recognized until the
// release; a tap of the same control shows nothing.
func TestHintsHoldGesture(t *testing.T) {
	const src = `{"schemaVersion":1,"name":"hold","layers":{"base":{
 "rightshoulder":{"tap":{"type":"keys","keys":"T","mode":"tap"},"hold":{"type":"hud","op":"hints"}},
 "south":{"label":"A","press":{"type":"keys","keys":"A"}}
}}}`
	h := newHsim(t, src)
	h.press("rightshoulder")
	h.wantShown("just pressed", "off")
	h.step(100 * time.Millisecond)
	h.wantShown("before the hold time", "off")
	h.step(300 * time.Millisecond)
	h.wantShown("after the hold time", "on:south=A")
	h.release("rightshoulder")
	h.wantShown("released", "off")
	if h.out.String() != "" {
		t.Errorf("sent %q", h.out.String())
	}

	// A tap sends its key and never shows the hints.
	h = newHsim(t, src)
	h.press("rightshoulder")
	h.step(50 * time.Millisecond)
	h.release("rightshoulder")
	h.wantShown("a tap", "off")
	h.step(time.Second)
	h.wantShown("a tap, later", "off")
	if !strings.Contains(h.out.String(), "T") {
		t.Errorf("the tap sent %q", h.out.String())
	}
}

// A hold hints action behaves like a hold of a layer: another control pressed
// while the hold is undecided makes it a hold at once (policy holdPreferred).
func TestHintsHoldIsPreferred(t *testing.T) {
	const src = `{"schemaVersion":1,"name":"hold","layers":{"base":{
 "rightshoulder":{"tap":{"type":"keys","keys":"T","mode":"tap"},"hold":{"type":"hud","op":"hints"}},
 "south":{"label":"A","press":{"type":"keys","keys":"A"}}
}}}`
	h := newHsim(t, src)
	h.press("rightshoulder")
	h.step(20 * time.Millisecond)
	h.press("south")
	h.wantShown("another control pressed", "on:south=A")
	h.release("south")
	h.release("rightshoulder")
	h.wantShown("released", "off")
}

// A latched hints action stays on after the release, until the control is pressed
// again; the press that ends it starts nothing.
func TestHintsLatch(t *testing.T) {
	const src = `{"schemaVersion":1,"name":"latch","layers":{"base":{
 "rightshoulder":{"latch":true,"press":{"type":"hud","op":"hints"}},
 "south":{"label":"A","press":{"type":"keys","keys":"A"}}
}}}`
	h := newHsim(t, src)
	h.press("rightshoulder")
	h.release("rightshoulder")
	h.wantShown("latched", "on:south=A")
	h.step(time.Second)
	h.wantShown("latched, later", "on:south=A")
	h.press("rightshoulder")
	h.wantShown("the press that ends the latch", "off")
	h.release("rightshoulder")
	h.wantShown("its release", "off")
	h.press("rightshoulder")
	h.release("rightshoulder")
	h.wantShown("latched again", "on:south=A")
	for _, end := range []func(){
		func() { h.e.CancelAll(h.now) },
		func() { h.e.Reset(h.now) },
		func() { h.e.Reload(h.now, compileLayers(t, src)) },
		func() { h.e.Pause(h.now); h.e.Resume() },
	} {
		end()
		h.wantShown("a latch after a cancel", "off")
		h.press("rightshoulder")
		h.release("rightshoulder")
		h.wantShown("a latch again", "on:south=A")
	}
}

// Two controls that hold the hints: neither is in the list, one let go leaves it
// on, and the list takes the let go control back.
func TestHintsTwoHolders(t *testing.T) {
	const src = `{"schemaVersion":1,"name":"two","layers":{"base":{
 "rightshoulder":{"press":{"type":"hud","op":"hints"}},
 "leftshoulder":{"press":{"type":"hud","op":"hints"}},
 "south":{"label":"A","press":{"type":"keys","keys":"A"}}
}}}`
	h := newHsim(t, src)
	h.press("rightshoulder")
	h.wantShown("one", "on:south=A | leftshoulder=按键提示")
	h.press("leftshoulder")
	h.wantShown("both", "on:south=A")
	h.release("rightshoulder")
	h.wantShown("one let go", "on:south=A | rightshoulder=按键提示")
	h.release("leftshoulder")
	h.wantShown("both let go", "off")
}

// A control whose hints a cancel ended is not a holder any more: the next hints
// action turns them on again alone, and its release turns them off, though the
// canceled control is still down.
func TestHintsCanceledHolderIsNotAHolder(t *testing.T) {
	const src = `{"schemaVersion":1,"name":"two","layers":{"base":{
 "rightshoulder":{"press":{"type":"hud","op":"hints"}},
 "leftshoulder":{"press":{"type":"hud","op":"hints"}},
 "south":{"label":"A","press":{"type":"keys","keys":"A"}}
}}}`
	for _, tc := range []struct {
		name string
		end  func(h *hsim)
	}{
		{"cancel", func(h *hsim) { h.e.CancelAll(h.now) }},
		{"pause", func(h *hsim) { h.e.Pause(h.now); h.e.Resume() }},
		{"reload", func(h *hsim) { h.e.Reload(h.now, compileLayers(t, src)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHsim(t, src)
			h.press("rightshoulder")
			tc.end(h)
			h.wantShown("canceled", "off")
			h.press("leftshoulder")
			h.wantShown("the other control", "on:south=A | rightshoulder=按键提示")
			h.release("leftshoulder")
			h.wantShown("the other control let go", "off")
			h.release("rightshoulder")
			h.wantShown("the canceled control let go", "off")
		})
	}
}

// The list reports on, with nothing in it, when nothing else has a binding; it
// is never nil then.
func TestHintsOnWithNothingToShow(t *testing.T) {
	h := newHsim(t, `{"schemaVersion":1,"name":"alone","layers":{"base":{"rightshoulder":{"press":{"type":"hud","op":"hints"}}}}}`)
	h.press("rightshoulder")
	hs := h.e.View().Hints
	if hs == nil || len(hs) != 0 {
		t.Errorf("hints %#v, want on and empty", hs)
	}
}

// View hands out a copy: changing it changes nothing in the engine, and two
// views are equal.
func TestHintsViewIsACopy(t *testing.T) {
	h := newHsim(t, hintsProfile)
	h.press("rightshoulder")
	a := h.e.View().Hints
	want := hintsText(a)
	a[0].Label = "changed"
	a = append(a[:1], engine.Hint{Control: control.Back, Label: "x"})
	_ = a
	h.wantShown("after changing a copy", want)
	if h.e.View().Hints[0].Label == "changed" {
		t.Error("View shares its list with the engine")
	}
}

// The levels of a registry (ENG-21) decide the list as they decide a press: the
// top level that has a binding wins, and a binding that blocks hides the control.
func TestHintsResolveThroughARegistry(t *testing.T) {
	prog := compileLayers(t, `{"schemaVersion":1,"name":"reg","layers":{"base":{}}}`)
	hint := &profile.Binding{ID: "/reg/hints", Control: control.RightShoulder, Press: &profile.Action{ID: "/reg/hints/press", Type: profile.ActionHUD, Op: profile.HUDOpHints}}
	bind := func(id string, c control.Control, label string) *profile.Binding {
		return &profile.Binding{ID: id, Control: c, Label: label}
	}
	lower := fakeLevel{name: "lower", bindings: map[control.Control]*profile.Binding{
		control.RightShoulder: hint,
		control.South:         bind("/lower/south", control.South, "low"),
		control.East:          bind("/lower/east", control.East, "kept"),
		control.West:          bind("/lower/west", control.West, "hidden"),
	}}
	upper := fakeLevel{name: "upper", bindings: map[control.Control]*profile.Binding{
		control.South: bind("/upper/south", control.South, "high"),
		control.West:  {ID: "/upper/west", Control: control.West, Block: true},
	}}
	e := engine.New(prog, &recorder{}, engine.Options{Registry: fakeRegistry{levels: []engine.Level{lower, upper}}})
	e.Step(engine.Input{Kind: engine.InputButton, Control: control.RightShoulder, Down: true})
	if got, want := hintsText(e.View().Hints), "on:south=high | east=kept"; got != want {
		t.Errorf("hints %s, want %s", got, want)
	}
}

// The trace shows a hints action as "hud hints", and its decision is a dispatch.
func TestHintsDecision(t *testing.T) {
	var got []string
	prog := compileLayers(t, hintsProfile)
	e := engine.New(prog, &recorder{}, engine.Options{Trace: func(d engine.Decision) { got = append(got, d.String()) }})
	e.Step(engine.Input{Kind: engine.InputButton, Control: control.RightShoulder, Down: true})
	e.Step(engine.Input{T: clock.Time(time.Millisecond), Kind: engine.InputButton, Control: control.RightShoulder, Down: false})
	want := []string{
		`{"t":0,"control":"rightshoulder","edge":"down","cycle":1,"gesture":"press","level":"base","binding":"/layers/base/rightshoulder","label":"提示","action":"hud hints","owner":"decl/1","reason":"dispatch"}`,
		`{"t":1,"control":"rightshoulder","edge":"up","cycle":1,"gesture":"press","level":"base","binding":"/layers/base/rightshoulder","label":"提示","reason":"end"}`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("decisions\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Compile and CheckBinding refuse a hints action on a gesture that is over
// when the control is released, and an operation that is not hints.
func TestHintsCheckAction(t *testing.T) {
	for _, g := range []engine.Gesture{engine.GesturePress, engine.GestureHold, engine.GestureTap, engine.GestureRelease, engine.GestureDoubleTap, engine.GestureTripleTap, engine.GestureQuadTap} {
		a := &profile.Action{ID: "/b/x", Type: profile.ActionHUD, Op: profile.HUDOpHints}
		b := &profile.Binding{ID: "/b", Control: control.West}
		switch g {
		case engine.GesturePress:
			b.Press = a
		case engine.GestureHold:
			b.Hold = a
		case engine.GestureTap:
			b.Tap = a
		case engine.GestureRelease:
			b.Release = a
		case engine.GestureDoubleTap:
			b.DoubleTap = a
		case engine.GestureTripleTap:
			b.TripleTap = a
		case engine.GestureQuadTap:
			b.QuadTap = a
		}
		err := engine.CheckBinding(b)
		if g.Holds() != (err == nil) {
			t.Errorf("%s: CheckBinding %v", g, err)
		}
		if err != nil && !strings.Contains(err.Error(), profile.HintsGestureMessage(g)) {
			t.Errorf("%s: %v", g, err)
		}
	}
	b := &profile.Binding{ID: "/b", Control: control.West, Press: &profile.Action{ID: "/b/press", Type: profile.ActionHUD, Op: "map"}}
	if err := engine.CheckBinding(b); err == nil || !strings.Contains(err.Error(), `unknown hud operation "map"`) {
		t.Errorf("CheckBinding of an unknown operation: %v", err)
	}
}

// FuzzKeyHints drives presses, layers, pauses, cancels, resets and reloads at
// random and checks that the hints are on only while a control that holds them
// is down, that they are gone after every path that cancels and after everything
// is let go, and that they are never on with no control to hold them. Each byte
// is one operation: its low 4 bits select it.
//
//	0–6   toggle rightshoulder (hints), leftshoulder (hints on hold), south, east, north (hold fn), leftstick (hold fn), rightstick (toggle fn)
//	7     wait next byte ms
//	8     Pause, or Resume when paused
//	9     CancelAll
//	10    Reset
//	11    Reload
//	12    ChangeLayer enable/disable fn
func FuzzKeyHints(f *testing.F) {
	f.Add([]byte{0, 0, 5, 0, 7, 100, 0, 0, 5, 0})
	f.Add([]byte{1, 0, 7, 255, 7, 255, 9, 0, 1, 0})
	f.Add([]byte{0, 0, 4, 0, 7, 200, 12, 0, 8, 0, 8, 0, 0, 0})
	f.Add([]byte{0, 0, 6, 0, 11, 0, 10, 0, 6, 0, 0, 0})
	const hold = `{"schemaVersion":1,"name":"hold","layers":{"base":{"rightshoulder":{"press":{"type":"hud","op":"hints"}},"leftshoulder":{"tap":{"type":"keys","keys":"T","mode":"tap"},"hold":{"type":"hud","op":"hints"}},"south":{"label":"A","press":{"type":"keys","keys":"A"}},"east":{"press":{"type":"keys","keys":"B"}},"north":{"hold":{"type":"layer","layer":"fn","op":"hold"}},"leftstick":{"press":{"type":"layer","layer":"fn","op":"hold"}},"rightstick":{"press":{"type":"layer","layer":"fn","op":"toggle"}}},"fn":{"south":{"label":"B"}}}}`
	prog := compileLayers(f, hold)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 400 {
			data = data[:400]
		}
		out := &recorder{}
		e := engine.New(prog, out, engine.Options{})
		var now clock.Time
		controls := [...]control.Control{control.RightShoulder, control.LeftShoulder, control.South, control.East, control.North, control.LeftStick, control.RightStick}
		down := map[control.Control]bool{}
		paused, fn := false, false
		run := func(d time.Duration) {
			target := now.Add(d)
			for steps := 0; ; steps++ {
				dl, ok := e.NextDeadline()
				if !ok || dl > target {
					break
				}
				if steps > 100000 {
					t.Fatalf("still running deadlines after %d steps", steps)
				}
				now = max(dl, now)
				out.now = now
				e.Apply(now)
			}
			now = target
		}
		check := func(what string) {
			t.Helper()
			hs := e.View().Hints
			if hs == nil {
				return
			}
			if !down[control.RightShoulder] && !down[control.LeftShoulder] {
				t.Fatalf("hints are on after %s with neither control down", what)
			}
			for _, h := range hs {
				if h.Label == "" || !h.Control.IsValid() {
					t.Fatalf("hints after %s have an empty entry: %+v", what, h)
				}
			}
		}
		for i := 0; i < len(data); i += 2 {
			op := int(data[i] & 15)
			arg := 0
			if i+1 < len(data) {
				arg = int(data[i+1])
			}
			run(time.Millisecond)
			switch {
			case op < len(controls):
				c := controls[op]
				down[c] = !down[c]
				e.Step(engine.Input{T: now, Kind: engine.InputButton, Control: c, Down: down[c]})
			case op == 7:
				run(time.Duration(arg) * time.Millisecond)
			case op == 8:
				if paused {
					e.Resume()
				} else {
					e.Pause(now)
					if e.View().Hints != nil {
						t.Fatal("hints are on after Pause")
					}
				}
				paused = !paused
			case op == 9:
				e.CancelAll(now)
				if e.View().Hints != nil {
					t.Fatal("hints are on after CancelAll")
				}
			case op == 10:
				e.Reset(now)
				clear(down)
				if e.View().Hints != nil {
					t.Fatal("hints are on after Reset")
				}
			case op == 11:
				e.Reload(now, prog)
				if e.View().Hints != nil {
					t.Fatal("hints are on after Reload")
				}
			default:
				fn = !fn
				name := "disable"
				if fn {
					name = "enable"
				}
				_ = e.ChangeLayer(name, "fn")
			}
			check(fmt.Sprintf("operation %d (%d)", i/2, op))
		}
		// Everything let go and let settle: nothing is shown.
		for c, d := range down {
			if d {
				e.Step(engine.Input{T: now, Kind: engine.InputButton, Control: c, Down: false})
			}
		}
		run(time.Second)
		if e.View().Hints != nil {
			t.Fatal("hints are on after every control was let go")
		}
		if dl, ok := e.NextDeadline(); ok {
			// Only a toggled layer or nothing: the engine has no timer left.
			t.Fatalf("a deadline at %v is left", time.Duration(dl))
		}
	})
}
