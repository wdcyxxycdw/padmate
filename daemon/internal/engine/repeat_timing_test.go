package engine_test

import (
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
)

func TestTapRepeatExplicitZeroAndRelease(t *testing.T) {
	p := compile(t, `"south":{"press":{"type":"keys","keys":"A","repeat":{"style":"tap","delayMs":0,"rateHz":1000}}}`)
	r := &recorder{}
	e := engine.New(p, r, engine.Options{})
	runM2(t, e, r, "+south@0 -south@85 wait@200")
	if got, want := r.String(), "@0 tap decl/1 A 20ms, @40 repeat-tap decl/1 A 20ms, @80 repeat-tap decl/1 A 20ms, @85 stop-repeat decl/1 A"; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
	if d, ok := e.NextDeadline(); ok {
		t.Fatalf("repeat deadline after release: %v", d)
	}
}

func TestTapRepeatStopsAtBufferedPhysicalRelease(t *testing.T) {
	p := compile(t, `"east":{"press":{"type":"keys","keys":"A","repeat":{"style":"tap","delayMs":0,"rateHz":1000}}},"west":{"tap":{"type":"keys","keys":"B"},"hold":{"type":"keys","keys":"E"}},"south":{"press":{"type":"keys","keys":"Ctrl+Z","mode":"tap"}}`)
	r := &recorder{}
	e := engine.New(p, r, engine.Options{})
	runM2(t, e, r, "+east@0 +west@100 +south@110 -east@115 wait@350 -south@400 -west@410 wait@500")
	if got, want := r.String(), "@0 tap decl/1 A 20ms, @40 repeat-tap decl/1 A 20ms, @80 repeat-tap decl/1 A 20ms, @115 stop-repeat decl/1 A, @350 down decl/2 E, @350 tap decl/3 Ctrl+Z 20ms, @410 up decl/2 E"; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
}

func TestDeclarativeSequenceUsesOneOrderedCommand(t *testing.T) {
	p := compile(t, `"south":{"press":{"type":"keys","keys":["Ctrl+K","S"],"mode":"tap"},"timing":{"tapPressMs":30,"tapGapMs":10}}`)
	r := &recorder{}
	e := engine.New(p, r, engine.Options{})
	runM2(t, e, r, "+south@0 -south@1 wait@100")
	if got, want := r.String(), "@0 sequence decl/1 [Ctrl+K,S] 30ms 10ms"; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
}
