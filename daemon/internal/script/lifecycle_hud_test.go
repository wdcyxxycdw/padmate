package script

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

// A runtime's hud panels go with it (SCR-22): the runtime a swap installs
// starts with none, the one it replaces shows none, an interruption leaves
// no current runtime and the restarted one starts empty, and so does every
// runtime after a permanent stop.
func TestLifecycleHUDPanelsGoWithTheirRuntime(t *testing.T) {
	c := clock.NewFake()
	l := NewLifecycle(c)
	t.Cleanup(func() { _ = l.Dispose() })
	build := func(context.Context) (*goja.Program, error) {
		return goja.Compile("main.js", `var setPanel = () => globalThis.__padmate.hud.panel("p", { rows: [["a", "b"]] });`, false)
	}
	load := func() *Host {
		t.Helper()
		loaded := <-l.Load(context.Background(), build)
		if loaded.Err != nil {
			t.Fatal(loaded.Err)
		}
		if err := l.Swap(loaded.Host); err != nil {
			t.Fatal(err)
		}
		return loaded.Host
	}
	setPanel := func(h *Host) {
		t.Helper()
		if _, err := h.VM.RunString("setPanel()"); err != nil {
			t.Fatal(err)
		}
		if len(h.HUDPanels()) != 1 {
			t.Fatalf("panels = %v", h.HUDPanels())
		}
	}
	waitCurrent := func() {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for l.Current() == nil && time.Now().Before(deadline) {
			if err := l.RunDue(); err != nil {
				t.Fatal(err)
			}
			runtime.Gosched()
			time.Sleep(time.Millisecond)
		}
		if l.Current() == nil {
			t.Fatal("no runtime after the restart")
		}
	}

	first := load()
	setPanel(first)
	second := load()
	if first.HUDPanels() != nil || second.HUDPanels() != nil || l.Current() != second {
		t.Fatalf("after the swap: old %v, new %v", first.HUDPanels(), second.HUDPanels())
	}

	setPanel(second)
	l.RecordInterruption(c.Now())
	if l.Current() != nil || second.HUDPanels() != nil {
		t.Fatalf("after the interruption: current %v, old panels %v", l.Current(), second.HUDPanels())
	}
	c.Advance(time.Second)
	waitCurrent()
	if got := l.Current().HUDPanels(); got != nil {
		t.Fatalf("the restarted runtime has panels: %v", got)
	}

	restarted := l.Current()
	setPanel(restarted)
	l.RecordInterruption(c.Now())
	l.RecordInterruption(c.Now())
	if _, stopped := l.RestartStatus(); !stopped {
		t.Fatal("no permanent stop")
	}
	if l.Current() != nil || restarted.HUDPanels() != nil {
		t.Fatalf("after the permanent stop: current %v, panels %v", l.Current(), restarted.HUDPanels())
	}
}
