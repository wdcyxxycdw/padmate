package script_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

type teardownSender struct{ events chan output.KeyEvent }

func (s teardownSender) Send(events []output.KeyEvent) (int, error) {
	for _, e := range events {
		s.events <- e
	}
	return len(events), nil
}

type teardownSlotRuntime struct{ life *script.Lifecycle }

func (r teardownSlotRuntime) Dispatch(e slot.ScriptEvent) (bool, error) {
	result, err := r.life.Dispatch(script.Event{Control: e.Control, Gesture: e.Gesture, Time: e.Time})
	return result.Consumed, err
}
func (r teardownSlotRuntime) RunDue() error                    { return r.life.RunDue() }
func (r teardownSlotRuntime) NextDeadline() (clock.Time, bool) { return r.life.NextDeadline() }
func (r teardownSlotRuntime) ReleaseAll() error {
	if h := r.life.Current(); h != nil {
		h.ReleaseKeys()
	}
	return nil
}
func (r teardownSlotRuntime) Reload(uint32) error { return nil }
func (r teardownSlotRuntime) Dispose() error      { return r.life.Dispose() }

func teardownCandidate(t *testing.T, l *script.Lifecycle, body string) *script.Host {
	t.Helper()
	result := <-l.Load(context.Background(), func(context.Context) (*goja.Program, error) {
		return goja.Compile("main.js", `globalThis.__padmate_bundle = {default: `+body+`};`, false)
	})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	return result.Host
}

// This goes through a real slot.RunLoop. A slow teardown must not occupy its
// goroutine: after the swap job, the next declarative edge must still run
// while the previous script finishes on a worker.
func TestSwapTeardownLeavesSlotResponsive(t *testing.T) {
	c := clock.Real()
	sender := teardownSender{events: make(chan output.KeyEvent, 32)}
	arb := output.NewArbiter(c, sender, output.ArbiterOptions{})
	p, ds := profile.Parse([]byte(`{"schemaVersion":0,"name":"teardown","layers":{"base":{"east":{"press":{"type":"keys","keys":"Space","mode":"hold"}}}}}`))
	if p == nil {
		t.Fatal(ds)
	}
	program, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	life := script.NewLifecycle(c, script.HostOptions{Output: arb, Owner: output.Owner{Slot: 0, Gen: 1, Source: output.SourceScript}})
	first := teardownCandidate(t, life, `{onActivate(){__padmate.key.down("X")}, teardown(){
		const start = Date.now(); while (Date.now()-start < 150) {}
		__padmate.key.up("X"); __padmate.key.down("Y"); __padmate.key.releaseAll();
		throw new Error("old teardown failed")
	}}`)
	s, err := slot.New(slot.Config{Index: 0, Gen: 1, Clock: c, Arbiter: arb, Program: program, Script: teardownSlotRuntime{life}})
	if err != nil {
		t.Fatal(err)
	}
	edges := make(chan slot.Edge, 2)
	jobs := make(chan func(*slot.Slot))
	loopErrors := make(chan error, 8)
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		done <- slot.RunLoop(ctx, slot.LoopConfig{Slot: s, Edges: edges, Jobs: jobs, Errors: func(errs []error) {
			loopErrors <- errors.Join(errs...)
		}})
	}()
	jobResult := make(chan error, 1)
	jobs <- func(*slot.Slot) { jobResult <- life.Swap(first) }
	if err := <-jobResult; err != nil {
		t.Fatal(err)
	}
	second := teardownCandidate(t, life, `{onActivate(){__padmate.key.down("Z")}}`)
	type result struct {
		duration time.Duration
		held     []output.HeldKey
		err      error
	}
	swapped := make(chan result, 1)
	jobs <- func(*slot.Slot) {
		start := time.Now()
		err := life.Swap(second)
		swapped <- result{duration: time.Since(start), held: arb.Held(), err: err}
	}
	r := <-swapped
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.duration > 5*time.Millisecond {
		t.Fatalf("swap blocked slot for %s; want <5ms while old teardown runs on a worker", r.duration)
	}
	if len(r.held) != 1 || len(r.held[0].Owners) != 1 || r.held[0].Owners[0].Cycle != 2 {
		t.Fatalf("after swap held = %+v, want only new runtime owner", r.held)
	}
	inputStart := time.Now()
	edges <- slot.Edge{Event: input.Event{Kind: input.ButtonDown, Control: "east"}}
	space := keys.MustParse("Space").Key()
	for {
		select {
		case e := <-sender.events:
			if e.Key == space && !e.Up {
				if d := time.Since(inputStart); d > 5*time.Millisecond {
					t.Fatalf("declarative edge took %s during teardown; want <5ms", d)
				}
				goto sent
			}
		case <-time.After(time.Second):
			t.Fatal("declarative Space press did not arrive")
		}
	}
sent:
	select {
	case err := <-loopErrors:
		if !strings.Contains(err.Error(), "old teardown failed") {
			t.Fatalf("async error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("old teardown error was not reported through slot.RunLoop")
	}
	jobs <- func(*slot.Slot) {
		if held := arb.Held(); len(held) != 2 {
			jobResult <- errors.New("late teardown changed new runtime or declarative holds")
			return
		}
		jobResult <- nil
	}
	if err := <-jobResult; err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("slot loop did not stop")
	}
}

// The slot must finish its own release path even if a detached teardown is
// still inside user code. The callback gives the test a deterministic point
// at which the teardown worker is active.
func TestSlotExitDoesNotWaitForTeardown(t *testing.T) {
	c := clock.Real()
	sender := teardownSender{events: make(chan output.KeyEvent, 32)}
	arb := output.NewArbiter(c, sender, output.ArbiterOptions{})
	p, ds := profile.Parse([]byte(`{"schemaVersion":0,"name":"exit","layers":{"base":{}}}`))
	if p == nil {
		t.Fatal(ds)
	}
	program, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	life := script.NewLifecycle(c, script.HostOptions{Output: arb, Owner: output.Owner{Slot: 0, Gen: 1, Source: output.SourceScript}})
	first := teardownCandidate(t, life, `{onActivate(){__padmate.key.down("X")}, teardown(){notifyTeardown(); __padmate.key.down("Y")}}`)
	started := make(chan struct{})
	releaseWorker := make(chan struct{})
	defer close(releaseWorker)
	if err := first.VM.Set("notifyTeardown", func(goja.FunctionCall) goja.Value {
		close(started)
		<-releaseWorker
		return goja.Undefined()
	}); err != nil {
		t.Fatal(err)
	}
	second := teardownCandidate(t, life, `{onActivate(){__padmate.key.down("Z")}}`)
	s, err := slot.New(slot.Config{Index: 0, Gen: 1, Clock: c, Arbiter: arb, Program: program, Script: teardownSlotRuntime{life}})
	if err != nil {
		t.Fatal(err)
	}
	edges := make(chan slot.Edge)
	jobs := make(chan func(*slot.Slot))
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- slot.RunLoop(ctx, slot.LoopConfig{Slot: s, Edges: edges, Jobs: jobs}) }()
	jobResult := make(chan error, 1)
	for _, candidate := range []*script.Host{first, second} {
		candidate := candidate
		jobs <- func(*slot.Slot) { jobResult <- life.Swap(candidate) }
		if err := <-jobResult; err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("teardown worker did not start")
	}
	if held := arb.Held(); len(held) != 1 || len(held[0].Owners) != 1 || held[0].Owners[0].Cycle != 2 {
		t.Fatalf("after swap held = %+v, want only new runtime owner", held)
	}
	start := time.Now()
	cancel()
	select {
	case <-done:
		if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
			t.Fatalf("slot exit waited %s for blocked teardown", elapsed)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("slot exit waited for blocked teardown")
	}
	if held := arb.Held(); len(held) != 0 {
		t.Fatalf("slot exit left keys held: %+v", held)
	}
}
