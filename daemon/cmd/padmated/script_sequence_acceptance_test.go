package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// textEventSender reports the keys, and the units of the typed text, that the
// output loop sends: "+Z", "-Z", and the character of a unit as itself (the two
// units of an emoji as uD83D and uDE00).
type textEventSender struct{ events chan string }

func (s *textEventSender) Send(events []output.KeyEvent) (int, error) {
	for _, e := range events {
		switch {
		case e.Unicode && e.Up:
		case e.Unicode && e.Unit >= 0xD800 && e.Unit < 0xE000:
			s.events <- fmt.Sprintf("u%04X", e.Unit) // half of a pair
		case e.Unicode:
			s.events <- string(rune(e.Unit))
		case e.Up:
			s.events <- "-" + e.Key.String()
		default:
			s.events <- "+" + e.Key.String()
		}
	}
	return len(events), nil
}

// key.sequence on the real slot loop, the real output loop and the padmated glue
// (scriptArbiter forwards TypeTextWithCompletion to the RemoteArbiter): the
// steps run in order with no input after startup, each waiting for the output's
// notice of the one before, and the Promise ends after the last (SCR-26).
func TestScriptSequenceIdleSlotRunsThroughOutputLoop(t *testing.T) {
	clk := clock.Real()
	sender := &textEventSender{events: make(chan string, 64)}
	loop := output.StartLoop(clk, sender, output.LoopOptions{})
	remote := loop.Remote()
	dir := t.TempDir()
	source := `import { defineScript, key } from "padmate";
export default defineScript({ onActivate() {
  key.sequence([{ keys: "Z" }, { wait: 30 }, { text: "你好" }, { keys: "X" }]).then(r => { if (!r.dropped) key.down("Y"); });
} });`
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	r, err := newScriptRuntime(def, 0, 1, clk, remote)
	if err != nil {
		t.Fatal(err)
	}
	s := newScriptTestSlot(t, clk, remote, r, `{}`)
	edges := make(chan slot.Edge)
	done := make(chan error, 1)
	go func() { done <- slot.RunLoop(context.Background(), slot.LoopConfig{Slot: s, Edges: edges}) }()
	t.Cleanup(func() {
		close(edges)
		if err := <-done; err != nil {
			t.Errorf("slot loop: %v", err)
		}
		if err := loop.Stop(); err != nil {
			t.Errorf("output loop: %v", err)
		}
	})
	var got []string
	started := time.Now()
	deadline := time.After(5 * time.Second)
	for len(got) < 7 {
		select {
		case e := <-sender.events:
			got = append(got, e)
		case <-deadline:
			t.Fatalf("output = %v, want Z, the text, X and Y down", got)
		}
	}
	if want := "+Z -Z 你 好 +X -X +Y"; strings.Join(got, " ") != want {
		t.Fatalf("output = %v, want %s", got, want)
	}
	// The wait counts from the release of Z, and the characters are a gap apart.
	if took := time.Since(started); took < 30*time.Millisecond+2*20*time.Millisecond {
		t.Errorf("the sequence took only %v: the wait and the gaps were not kept", took)
	}
}

// key.type with a newline and an emoji: the text goes out through the real
// loop, and a pair of units arrives together.
func TestScriptTypeRunsThroughOutputLoop(t *testing.T) {
	clk := clock.Real()
	sender := &textEventSender{events: make(chan string, 64)}
	loop := output.StartLoop(clk, sender, output.LoopOptions{})
	remote := loop.Remote()
	dir := t.TempDir()
	source := fmt.Sprintf(`import { defineScript, key } from "padmate";
export default defineScript({ onActivate() { key.type(%q); } });`, "a\n😀")
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	r, err := newScriptRuntime(def, 0, 1, clk, remote)
	if err != nil {
		t.Fatal(err)
	}
	s := newScriptTestSlot(t, clk, remote, r, `{}`)
	edges := make(chan slot.Edge)
	done := make(chan error, 1)
	go func() { done <- slot.RunLoop(context.Background(), slot.LoopConfig{Slot: s, Edges: edges}) }()
	t.Cleanup(func() {
		close(edges)
		if err := <-done; err != nil {
			t.Errorf("slot loop: %v", err)
		}
		if err := loop.Stop(); err != nil {
			t.Errorf("output loop: %v", err)
		}
	})
	var got []string
	deadline := time.After(5 * time.Second)
	for len(got) < 5 {
		select {
		case e := <-sender.events:
			got = append(got, e)
		case <-deadline:
			t.Fatalf("output = %v", got)
		}
	}
	if want := "a +Enter -Enter uD83D uDE00"; strings.Join(got, " ") != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
