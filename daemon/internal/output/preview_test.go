package output

import (
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

func TestLocalDryRunReleasesAndDoesNotReplay(t *testing.T) {
	clk := clock.NewFake()
	var physical []KeyEvent
	var preview []string
	a := NewArbiter(clk, previewSender(func(events []KeyEvent) (int, error) { physical = append(physical, events...); return len(events), nil }), ArbiterOptions{Preview: func(c keys.Chord, local bool) {
		if local {
			preview = append(preview, c.String())
		}
	}})
	o := Owner{Slot: 0, Gen: 1, Cycle: 1, Source: SourceDecl}
	c, _ := keys.Parse("Ctrl+Z")
	if err := a.Down(o, c); err != nil {
		t.Fatal(err)
	}
	if err := a.SetLocalDryRun(true); err != nil {
		t.Fatal(err)
	}
	boundary := len(physical)
	if err := a.Down(o, c); err != nil {
		t.Fatal(err)
	}
	if err := a.Tap(o, c, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if len(physical) != boundary {
		t.Fatal("local dry run injected", physical)
	}
	if len(preview) == 0 {
		t.Fatal("no physical mapping preview")
	}
	if err := a.SetLocalDryRun(false); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Second)
	if err := a.Advance(); err != nil {
		t.Fatal(err)
	}
	if len(physical) != boundary || len(a.Held()) != 0 {
		t.Fatal("preview replayed", physical, a.Held())
	}
	if err := a.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
}

type previewSender func([]KeyEvent) (int, error)

func (f previewSender) Send(events []KeyEvent) (int, error) { return f(events) }
