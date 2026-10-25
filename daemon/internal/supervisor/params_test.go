package supervisor_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

type paramsGateRuntime struct {
	padKeyCapture
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
	commits atomic.Uint32
}

func TestParamsStopAbortsParkedTransaction(t *testing.T) {
	blocked := make(chan struct{})
	release := sync.OnceFunc(func() { close(blocked) })
	runtime := &paramsGateRuntime{padKeyCapture: padKeyCapture{snapshots: make(chan slot.PadSnapshot, 32)}, entered: make(chan struct{}), release: blocked}
	h := start(t, opts{scriptFactory: func(_ uint8, _ uint32, _ clock.Clock, _ slot.Arbiter) (slot.ScriptRuntime, error) {
		return runtime, nil
	}})
	t.Cleanup(release)
	done := make(chan error, 1)
	var configs atomic.Uint32
	go func() {
		done <- h.sup.UpdateScriptParams(context.Background(), map[string]any{"key": "Z"}, func() { configs.Add(1) })
	}()
	select {
	case <-runtime.entered:
	case <-time.After(waitLimit):
		t.Fatal("slot did not prepare")
	}
	h.cancel()
	select {
	case err := <-done:
		if !errors.Is(err, supervisor.ErrStopped) {
			t.Fatalf("stopped transaction: %v", err)
		}
	case <-time.After(waitLimit):
		t.Fatal("shutdown deadlocked while the transaction waited for a slot")
	}
	release()
	if err := h.wait(); err != nil {
		t.Fatal(err)
	}
	if configs.Load() != 0 || runtime.commits.Load() != 0 {
		t.Fatal("shutdown committed a pending context")
	}
}

func (r *paramsGateRuntime) PrepareParams(map[string]any) (func(), error) {
	r.once.Do(func() { close(r.entered) })
	if r.release != nil {
		<-r.release
	}
	return func() { r.commits.Add(1) }, nil
}

func TestParamsCanceledTransactionUnblocksPreparedSlots(t *testing.T) {
	blocked := make(chan struct{})
	release := sync.OnceFunc(func() { close(blocked) })
	var runtimes [2]*paramsGateRuntime
	for i := range runtimes {
		runtimes[i] = &paramsGateRuntime{padKeyCapture: padKeyCapture{snapshots: make(chan slot.PadSnapshot, 32)}, entered: make(chan struct{})}
	}
	runtimes[1].release = blocked
	h := start(t, opts{slots: 2, scriptFactory: func(index uint8, _ uint32, _ clock.Clock, _ slot.Arbiter) (slot.ScriptRuntime, error) {
		return runtimes[index], nil
	}})
	t.Cleanup(release)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var configs atomic.Uint32
	go func() { done <- h.sup.UpdateScriptParams(ctx, map[string]any{"key": "Z"}, func() { configs.Add(1) }) }()
	for _, r := range runtimes {
		select {
		case <-r.entered:
		case <-time.After(waitLimit):
			t.Fatal("slot did not prepare")
		}
	}
	// The router must still take input while a transaction parks slot loops.
	h.send(added(pad(1, "Pad", "03ab")))
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled transaction: %v", err)
		}
	case <-time.After(waitLimit):
		t.Fatal("canceled transaction did not return")
	}
	release()
	h.sync()
	if configs.Load() != 0 || runtimes[0].commits.Load() != 0 || runtimes[1].commits.Load() != 0 {
		t.Fatal("canceled transaction committed a prepared context")
	}
	if err := h.sup.UpdateScriptParams(context.Background(), map[string]any{"key": "Y"}, func() { configs.Add(1) }); err != nil {
		t.Fatal(err)
	}
	if configs.Load() != 1 || runtimes[0].commits.Load() != 1 || runtimes[1].commits.Load() != 1 {
		t.Fatal("next transaction did not commit once on every slot")
	}
}
