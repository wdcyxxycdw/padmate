package script

import (
	"context"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

func TestLifecycleParamsRemainValidatedBetweenAutomaticRestarts(t *testing.T) {
	clk := clock.NewFake()
	life := NewLifecycle(clk, HostOptions{Params: map[string]any{"key": "X"}})
	t.Cleanup(func() { _ = life.Dispose() })
	build := func(context.Context) (*goja.Program, error) {
		return goja.Compile("params.js", `globalThis.__padmate_bundle = {default:{
  params:{key:__padmate.param.key({default:"X"})},
  onActivate(ctx) { globalThis.activated = ctx.params.key; }
}};`, false)
	}
	loaded := <-life.Load(context.Background(), build)
	if loaded.Err != nil {
		t.Fatal(loaded.Err)
	}
	if err := life.Swap(loaded.Host); err != nil {
		t.Fatal(err)
	}
	life.RecordInterruption(clk.Now())
	if life.Current() != nil {
		t.Fatal("interrupted host remained active")
	}
	if _, err := life.PrepareParams(map[string]any{"key": true}); err == nil {
		t.Fatal("restart backoff lost its parameter schema")
	}
	commit, err := life.PrepareParams(map[string]any{"key": "Z"})
	if err != nil {
		t.Fatal(err)
	}
	commit()
	clk.Advance(time.Second)
	deadline := time.Now().Add(time.Second)
	for life.Current() == nil && time.Now().Before(deadline) {
		if err := life.RunDue(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if life.Current() == nil || life.Current().VM.Get("activated").String() != "Z" {
		t.Fatal("automatic restart did not inherit the latest validated params")
	}
}
