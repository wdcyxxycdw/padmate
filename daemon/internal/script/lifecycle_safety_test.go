package script

import (
	"context"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

func TestLifecycleWallBudgetPermanentlyStopsUntilManualReload(t *testing.T) {
	c := clock.NewFake()
	l := NewLifecycle(c)
	builds := 0
	build := func(context.Context) (*goja.Program, error) {
		builds++
		return goja.Compile("wall.js", `__padmate.key.down("X")`, false)
	}
	loaded := <-l.Load(context.Background(), build)
	if loaded.Err != nil {
		t.Fatal(loaded.Err)
	}
	if err := l.Swap(loaded.Host); err != nil {
		t.Fatal(err)
	}
	l.handleRuntimeFailure(&BudgetError{Clock: "wall", Budget: 2 * time.Second})
	if l.Current() != nil || !l.restart.Permanent() {
		t.Fatal("wall budget did not permanently stop runtime")
	}
	if d, ok := l.NextDeadline(); ok && !l.TeardownsPending() {
		t.Fatalf("permanent stop retained non-teardown deadline %v", d)
	}
	c.Advance(time.Minute)
	if err := l.RunDue(); err != nil || builds != 1 {
		t.Fatalf("automatic restart after wall wedge: err=%v builds=%d", err, builds)
	}
	l.ResetRestartPolicy()
	next := <-l.Load(context.Background(), build)
	if next.Err != nil {
		t.Fatal(next.Err)
	}
	if err := l.Swap(next.Host); err != nil || builds != 2 {
		t.Fatalf("manual reload: err=%v builds=%d", err, builds)
	}
	if err := l.Dispose(); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleRetireClearsOldTimersBeforeTeardown(t *testing.T) {
	c := clock.NewFake()
	l := NewLifecycle(c)
	loaded := <-l.Load(context.Background(), func(context.Context) (*goja.Program, error) {
		return goja.Compile("timer.js", `__padmate.every(10, () => __padmate.key.down("Y"));`, false)
	})
	if loaded.Err != nil {
		t.Fatal(loaded.Err)
	}
	if err := l.Swap(loaded.Host); err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Host.Loop.NextDeadline(); !ok {
		t.Fatal("old runtime has no timer before retirement")
	}
	finish := make(chan struct{})
	l.beforeTeardown = func() { <-finish }
	t.Cleanup(func() { close(finish) })
	if err := l.Dispose(); err != nil {
		t.Fatal(err)
	}
	if d, ok := loaded.Host.Loop.NextDeadline(); ok {
		t.Fatalf("retired runtime kept timer until worker teardown at %v", d)
	}
}

func TestLifecycleStackOverflowReleasesHeldKeys(t *testing.T) {
	c := clock.NewFake()
	l := NewLifecycle(c)
	loaded := <-l.Load(context.Background(), func(context.Context) (*goja.Program, error) {
		return goja.Compile("stack.js", `__padmate.on("south", "press", () => {
  __padmate.key.down("X");
  function recur() { recur(); } recur();
});`, false)
	})
	if loaded.Err != nil {
		t.Fatal(loaded.Err)
	}
	if err := l.Swap(loaded.Host); err != nil {
		t.Fatal(err)
	}
	_, err := l.Dispatch(Event{Control: "south", Gesture: "press"})
	if err == nil {
		t.Fatal("stack overflow not reported")
	}
	if l.Current() != nil {
		t.Fatal("stack overflow left runtime active")
	}
	got := loaded.Host.Output()
	if len(got) != 2 || got[0].Up || !got[1].Up {
		t.Fatalf("stack overflow did not release X: %+v", got)
	}
	_ = l.Dispose()
}

func TestLifecycleWorkerSetupWallReason(t *testing.T) {
	c := clock.NewFake()
	l := NewLifecycle(c)
	l.beforeSetup = func(h *Host) { h.VM.Interrupt(&BudgetError{Clock: "wall", Budget: 2 * time.Second}) }
	result := <-l.Load(context.Background(), func(context.Context) (*goja.Program, error) {
		return goja.Compile("setup.js", `globalThis.__padmate_bundle = { default: { setup() {} } };`, false)
	})
	if !IsWallBudget(result.Err) || result.Host != nil {
		t.Fatalf("worker setup result = %+v, want wall failure and no host", result)
	}
	_ = l.Dispose()
}
