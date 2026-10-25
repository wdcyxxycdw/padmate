package main

import (
	"context"
	"errors"
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

// No input or job is sent after startup. T-output's completion must wake the
// real slot loop for the array's next tap and its Promise continuation.
func TestScriptTapIdleSlotCompletesThroughOutputLoop(t *testing.T) {
	clk := clock.Real()
	sender := &reloadEventSender{events: make(chan reloadEvent, 32)}
	loop := output.StartLoop(clk, sender, output.LoopOptions{})
	remote := loop.Remote()
	dir := t.TempDir()
	source := `import { defineScript, key } from "padmate";
export default defineScript({ onActivate() {
  key.tap(["Z", "X"], {holdMs: 20}).then(r => { if (!r.dropped) key.down("Y"); });
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
	deadline := time.After(time.Second)
	for len(got) < 5 {
		select {
		case e := <-sender.events:
			if !e.up {
				got = append(got, "+"+e.key)
			} else {
				got = append(got, "-"+e.key)
			}
		case <-deadline:
			t.Fatalf("idle slot tap output = %v, want Z down/up, X down/up, Y down", got)
		}
	}
	if want := []string{"+Z", "-Z", "+X", "-X", "+Y"}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("idle slot tap output = %v, want %v", got, want)
	}
}

func TestScriptTapContinuationWatchdogReachesSlotErrors(t *testing.T) {
	clk := clock.Real()
	sender := &reloadEventSender{events: make(chan reloadEvent, 16)}
	loop := output.StartLoop(clk, sender, output.LoopOptions{})
	remote := loop.Remote()
	dir := t.TempDir()
	source := `import { defineScript, key } from "padmate";
export default defineScript({ onActivate() {
  key.tap("Z", {holdMs: 10}).then(() => { while (true) {} });
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
	errorsC := make(chan []error, 4)
	go func() {
		done <- slot.RunLoop(context.Background(), slot.LoopConfig{Slot: s, Edges: edges, Errors: func(errs []error) { errorsC <- errs }})
	}()
	t.Cleanup(func() {
		close(edges)
		if err := <-done; err != nil {
			t.Errorf("slot loop: %v", err)
		}
		if err := loop.Stop(); err != nil {
			t.Errorf("output loop: %v", err)
		}
	})
	select {
	case errs := <-errorsC:
		if msg := errors.Join(errs...).Error(); !strings.Contains(msg, "budget") {
			t.Fatalf("tap continuation error = %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("idle slot did not report tap continuation watchdog error")
	}
}

func TestScriptTapSafetyReleaseDoesNotRunOldContinuation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		release func(*slot.Slot) error
	}{
		{"pause", func(s *slot.Slot) error { return s.SetPaused(true) }},
		{"releaseAll", (*slot.Slot).ReleaseAll},
		{"dispose", (*slot.Slot).Dispose},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk := clock.Real()
			sender := &reloadEventSender{events: make(chan reloadEvent, 32)}
			loop := output.StartLoop(clk, sender, output.LoopOptions{})
			remote := loop.Remote()
			dir := t.TempDir()
			source := `import { defineScript, key } from "padmate";
export default defineScript({ onActivate() {
  key.tap("Z", {holdMs: 100}).then(() => { key.down("X"); while (true) {} });
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
			jobs := make(chan func(*slot.Slot))
			done := make(chan error, 1)
			go func() { done <- slot.RunLoop(context.Background(), slot.LoopConfig{Slot: s, Edges: edges, Jobs: jobs}) }()
			t.Cleanup(func() {
				close(edges)
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("slot loop: %v", err)
					}
				case <-time.After(300 * time.Millisecond):
					t.Error("slot loop stayed blocked during safety release")
				}
				if err := loop.Stop(); err != nil {
					t.Errorf("output loop: %v", err)
				}
			})
			select {
			case e := <-sender.events:
				if e.up || e.key != "Z" {
					t.Fatalf("initial output = %+v", e)
				}
			case <-time.After(time.Second):
				t.Fatal("tap did not start")
			}
			ack := make(chan error, 1)
			select {
			case jobs <- func(s *slot.Slot) { ack <- tc.release(s) }:
			case <-time.After(time.Second):
				t.Fatal("slot did not accept release job")
			}
			select {
			case err := <-ack:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(300 * time.Millisecond):
				t.Fatal("await tap continuation blocked the safety release")
			}
			seenUp := false
			deadline := time.After(150 * time.Millisecond)
			for {
				select {
				case e := <-sender.events:
					if e.key == "X" {
						t.Fatalf("old continuation emitted X after safety release: %+v", e)
					}
					if e.key == "Z" && e.up {
						seenUp = true
					}
				case <-deadline:
					if !seenUp {
						t.Fatal("safety release did not release Z")
					}
					return
				}
			}
		})
	}
}
