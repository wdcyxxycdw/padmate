package script

import (
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// A script taps the wheel keys with key.tap like any key (ADR-0062): one
// notch each, a chord's modifier around it, and the release the Arbiter sends
// for a wheel key is one a Sender drops.
func TestScriptTapsWheelKeys(t *testing.T) {
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
	_, err := h.VM.RunString(`globalThis.done = false; __padmate.key.tap(["Ctrl+WheelUp", "WheelDown", "WheelLeft"], {holdMs: 10}).then(() => { done = true });`)
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		clk.Advance(10 * time.Millisecond)
		if err := loop.Do(func(*output.Arbiter) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if err := h.RunDue(); err != nil {
			t.Fatal(err)
		}
	}
	rec.want(t, "[+LCtrl +WheelUp -WheelUp -LCtrl +WheelDown -WheelDown +WheelLeft -WheelLeft]")
	if !h.VM.Get("done").ToBoolean() {
		t.Error("the promise of the tap array is still pending")
	}
	// A wheel key has no held state of its own beyond its hold, and the parse
	// helper knows the name.
	v, err := h.VM.RunString(`JSON.stringify([__padmate.key.parse("ctrl+wheeldown"), __padmate.key.parse("WheelUp+Ctrl").ok, __padmate.key.isHeld("WheelUp")])`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := v.String(), `[{"ok":true,"canonical":"Ctrl+WheelDown"},false,false]`; got != want {
		t.Errorf("key.parse = %s, want %s", got, want)
	}
}
