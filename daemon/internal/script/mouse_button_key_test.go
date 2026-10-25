package script

import (
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// A script clicks with key.tap and holds a button with key.down like any key
// (ADR-0062): the button goes down once and up with key.up or the script's
// releaseAll, a chord's modifier is around it, and the names, aliases
// included, parse. There is no script repeat in API 0.8; when the repeat
// helper (SCRIPTING §5) arrives it follows the rule of the Arbiter, which
// never presses a button that is down.
func TestScriptMouseButtons(t *testing.T) {
	clk := clock.NewFake()
	rec := new(tapSender)
	loop := output.StartLoop(clk, rec, output.LoopOptions{})
	defer func() {
		if err := loop.Stop(); err != nil {
			t.Fatal(err)
		}
		if got := rec.trace(); strings.Count(got, "+") != strings.Count(got, "-") {
			t.Fatalf("keys remain held: %s", got)
		}
	}()
	h := NewHostWithOptions(HostOptions{Clock: clk, Output: loop.Remote()})
	h.active = true
	sync := func() {
		t.Helper()
		if err := loop.Do(func(*output.Arbiter) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if err := h.RunDue(); err != nil {
			t.Fatal(err)
		}
	}

	// Clicks: each a press and a release, a chord's modifier around its click.
	if _, err := h.VM.RunString(`globalThis.done = false; __padmate.key.tap(["MouseLeft", "Ctrl+RButton", "xbutton1", "MouseX2"], {holdMs: 10}).then(() => { done = true });`); err != nil {
		t.Fatal(err)
	}
	for range 6 {
		clk.Advance(10 * time.Millisecond)
		sync()
	}
	rec.want(t, "[+MouseLeft -MouseLeft +LCtrl +MouseRight -MouseRight -LCtrl +MouseX1 -MouseX1 +MouseX2 -MouseX2]")
	if !h.VM.Get("done").ToBoolean() {
		t.Error("the promise of the tap array is still pending")
	}

	// A held button: down once however often the script presses it, held for
	// isHeld whatever the spelling, and up with key.up.
	v, err := h.VM.RunString(`
__padmate.key.down("MButton"); __padmate.key.down("MouseMiddle");
const before = [__padmate.key.isHeld("MouseMiddle"), __padmate.key.isHeld("mbutton"), __padmate.key.isHeld("MouseLeft")];
__padmate.key.up("MouseMiddle");
JSON.stringify(before)`)
	if err != nil {
		t.Fatal(err)
	}
	sync()
	if got, want := v.String(), `[true,true,false]`; got != want {
		t.Errorf("isHeld before the release = %s, want %s", got, want)
	}
	rec.want(t, "[+MouseLeft -MouseLeft +LCtrl +MouseRight -MouseRight -LCtrl +MouseX1 -MouseX1 +MouseX2 -MouseX2 +MouseMiddle -MouseMiddle]")
	v, err = h.VM.RunString(`__padmate.key.isHeld("MouseMiddle")`)
	if err != nil {
		t.Fatal(err)
	}
	if v.ToBoolean() {
		t.Error("MouseMiddle is still held after key.up")
	}

	// releaseAll lets go of a button the script holds, with its modifier.
	if _, err := h.VM.RunString(`__padmate.key.down("Shift+RButton"); __padmate.key.down("XButton2");`); err != nil {
		t.Fatal(err)
	}
	sync()
	if _, err := h.VM.RunString(`__padmate.key.releaseAll()`); err != nil {
		t.Fatal(err)
	}
	sync()
	rec.want(t, "[+MouseLeft -MouseLeft +LCtrl +MouseRight -MouseRight -LCtrl +MouseX1 -MouseX1 +MouseX2 -MouseX2 +MouseMiddle -MouseMiddle "+
		"+LShift +MouseRight +MouseX2 -MouseX2 -MouseRight -LShift]")

	// The names parse to their canonical form, and a button cannot lead a chord.
	v, err = h.VM.RunString(`JSON.stringify([__padmate.key.parse("ctrl+lbutton"), __padmate.key.parse("XBUTTON2"), __padmate.key.parse("MouseLeft+Ctrl").ok, __padmate.key.parse("XButton3").ok])`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := v.String(), `[{"ok":true,"canonical":"Ctrl+MouseLeft"},{"ok":true,"canonical":"MouseX2"},false,false]`; got != want {
		t.Errorf("key.parse = %s, want %s", got, want)
	}
}
