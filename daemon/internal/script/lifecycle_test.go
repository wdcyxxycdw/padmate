package script

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

func TestLifecycleWorkerSwapActivateAndDispose(t *testing.T) {
	c := clock.NewFake()
	root := t.TempDir()
	src := `import { defineScript, key } from "padmate";
export default defineScript({
  setup() {},
  onActivate() { key.down("Z"); },
  teardown() { key.down("Y"); }
});`
	if err := os.WriteFile(filepath.Join(root, "main.ts"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	l := NewLifecycle(c)
	result := l.Load(context.Background(), func(context.Context) (*goja.Program, error) {
		r, err := compile.Build(compile.BuildOptions{Root: root, Entry: "main.ts", API: "0.8"})
		if err != nil {
			return nil, err
		}
		return goja.Compile("main.js", string(r.JS), false)
	})
	loaded := <-result
	if loaded.Err != nil {
		t.Fatal(loaded.Err)
	}
	if loaded.Host == nil || loaded.Host.State() != StateStarting {
		t.Fatalf("loaded host/state = %v/%v, want starting host", loaded.Host, loaded.Host.State())
	}
	if got := loaded.Host.Output(); len(got) != 0 {
		t.Fatalf("setup output = %+v, want disabled", got)
	}
	if err := l.Swap(loaded.Host); err != nil {
		t.Fatal(err)
	}
	if l.State() != StateRunning {
		t.Fatalf("lifecycle state = %v, want running", l.State())
	}
	if got := l.Current().Output(); len(got) != 1 || got[0].Spec != "Z" || got[0].Up {
		t.Fatalf("onActivate output = %+v, want Z down", got)
	}
	if err := l.Dispose(); err != nil {
		t.Fatal(err)
	}
	got := l.Current()
	if got != nil || l.State() != StateDisposed {
		t.Fatalf("after Dispose current/state = %v/%v", got, l.State())
	}
	// The slot releases immediately; teardown runs on a worker with no output
	// seam, so even a teardown key.down cannot introduce another key.
	out := loaded.Host.Output()
	if len(out) != 2 || out[0].Spec != "Z" || !out[1].Up {
		t.Fatalf("immediate release output = %+v", out)
	}
	deadline := time.Now().Add(time.Second)
	for l.TeardownsPending() && time.Now().Before(deadline) {
		if err := l.RunDue(); err != nil {
			t.Fatal(err)
		}
		runtime.Gosched()
	}
	if l.TeardownsPending() || loaded.Host.State() != StateDisposed {
		t.Fatal("teardown worker did not finish")
	}
	if out := loaded.Host.Output(); len(out) != 2 {
		t.Fatalf("teardown produced late output = %+v", out)
	}
}

func TestLifecycleAsyncOnActivate(t *testing.T) {
	c := clock.NewFake()
	h := NewHost(c)
	root := t.TempDir()
	src := `import { defineScript, sleep, key } from "padmate";
export default defineScript({
  onActivate: async () => { await sleep(5); key.down("X"); }
});`
	if err := os.WriteFile(filepath.Join(root, "main.ts"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := compile.Build(compile.BuildOptions{Root: root, Entry: "main.ts", API: "0.8"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := goja.Compile("main.js", string(r.JS), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(p); err != nil {
		t.Fatal(err)
	}
	if err := h.OnActivate(); err != nil {
		t.Fatal(err)
	}
	c.Advance(5 * time.Millisecond)
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	if got := h.Output(); len(got) != 1 || got[0].Spec != "X" {
		t.Fatalf("async activation output = %+v, want X down", got)
	}
}

func TestLifecycleAutomaticallyRestartsAfterInterruption(t *testing.T) {
	c := clock.NewFake()
	l := NewLifecycle(c)
	var builds int
	build := func(context.Context) (*goja.Program, error) {
		builds++
		return goja.Compile("main.js", "", false)
	}
	loaded := <-l.Load(context.Background(), build)
	if loaded.Err != nil {
		t.Fatal(loaded.Err)
	}
	if err := l.Swap(loaded.Host); err != nil {
		t.Fatal(err)
	}
	decision := l.RecordInterruption(c.Now())
	if decision.Delay != time.Second || decision.Permanent {
		t.Fatalf("first restart decision = %+v", decision)
	}
	c.Advance(time.Second)
	deadline := time.Now().Add(time.Second)
	for l.Current() == nil && time.Now().Before(deadline) {
		if err := l.RunDue(); err != nil {
			t.Fatal(err)
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	if l.Current() == nil || l.State() != StateRunning || builds != 2 {
		t.Fatalf("after restart current/state/builds = %v/%v/%d", l.Current(), l.State(), builds)
	}
	if count, stopped := l.RestartStatus(); count != 1 || stopped {
		t.Fatal(count, stopped)
	}
	l.RecordInterruption(c.Now())
	l.RecordInterruption(c.Now())
	if _, stopped := l.RestartStatus(); !stopped {
		t.Fatal("permanent stop not observable")
	}
	l.ResetRestartPolicy()
	if count, stopped := l.RestartStatus(); count != 0 || stopped {
		t.Fatal("reset did not reset inspection", count, stopped)
	}
}

func TestLifecycleRunDueReportsRestartCandidateFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail func() (*goja.Program, error)
	}{
		{"build", func() (*goja.Program, error) { return nil, errors.New("restart build failed") }},
		{"onActivate", func() (*goja.Program, error) {
			return goja.Compile("restart.js", `globalThis.__padmate_bundle = { default: { onActivate() { throw new Error("restart activation failed"); } } };`, false)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := clock.NewFake()
			l := NewLifecycle(c)
			builds := 0
			build := func(context.Context) (*goja.Program, error) {
				builds++
				if builds == 1 {
					return goja.Compile("initial.js", "", false)
				}
				return tc.fail()
			}
			loaded := <-l.Load(context.Background(), build)
			if loaded.Err != nil {
				t.Fatal(loaded.Err)
			}
			if err := l.Swap(loaded.Host); err != nil {
				t.Fatal(err)
			}
			l.RecordInterruption(c.Now())
			c.Advance(time.Second)
			deadline := time.Now().Add(time.Second)
			var got error
			for got == nil && time.Now().Before(deadline) {
				got = l.RunDue()
				runtime.Gosched()
				time.Sleep(time.Millisecond)
			}
			if got == nil || !strings.Contains(got.Error(), "restart ") {
				t.Fatalf("RunDue error = %v, want candidate failure", got)
			}
			if l.Current() != nil || l.State() != StateDisposed {
				t.Fatalf("state/current = %v/%v, want disposed", l.State(), l.Current())
			}
		})
	}
}
