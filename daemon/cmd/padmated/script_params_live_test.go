package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
	"github.com/wdcyxxycdw/padmate/daemon/internal/watch"
)

const liveParamsSource = `import { defineScript, param, on, every, key } from "padmate";
export default defineScript({
  params:{key:param.key({default:"X"})},
  setup(ctx) {
    globalThis.ticks = 0; globalThis.releases = 0;
    every(10, () => globalThis.ticks++);
    on("south", "press", () => key.down("LCtrl"));
    on("south", "release", () => { globalThis.releases++; key.up("LCtrl"); });
    on("east", "press", () => key.tap(ctx.params.key,{holdMs:0}));
  }
});`

type paramsObservation struct {
	host     *script.Host
	ticks    int64
	releases int64
	pending  bool
}

// Inspect the VM only from the slot's dispatch, never from the test goroutine.
type paramsObservedRuntime struct {
	*scriptRuntime
	observed chan paramsObservation
}

func (r *paramsObservedRuntime) DispatchGesture(id uint64, e engine.GestureEvent) (bool, error) {
	consumed, err := r.scriptRuntime.DispatchGesture(id, e)
	if r.lifecycle != nil && r.lifecycle.Current() != nil {
		h := r.lifecycle.Current()
		r.observed <- paramsObservation{host: h, ticks: h.VM.Get("ticks").ToInteger(), releases: h.VM.Get("releases").ToInteger(), pending: r.pending != nil}
	}
	return consumed, err
}

type liveParamsHarness struct {
	t        *testing.T
	run      *liveRun
	events   chan input.Event
	rec      *simtest.SyncRecorder
	observed []chan paramsObservation
	path     string
}

func newLiveParamsHarness(t *testing.T, source string, slots int, override func(context.Context, string, *profile.ScriptConfig) (*goja.Program, error), watchScript ...bool) *liveParamsHarness {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &liveParamsHarness{t: t, path: filepath.Join(dir, "profile.json"), events: make(chan input.Event, 32)}
	data := paramsProfile("params", `"X"`, "")
	p, prog, _, err := parseProfile(h.path, data, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.Real()
	h.rec = simtest.NewSyncRecorder(clk)
	out := output.StartLoop(clk, h.rec, output.LoopOptions{})
	def := newScriptDefinition(h.path, p.Script, len(watchScript) > 0 && watchScript[0])
	for range slots {
		h.observed = append(h.observed, make(chan paramsObservation, 256))
	}
	sup, err := supervisor.New(supervisor.Config{
		Clock: clk, Events: h.events, Output: out, Program: prog, Slots: slots,
		ScriptConfigured: def.Configured,
		ScriptFactory: func(index uint8, gen uint32, c clock.Clock, a slot.Arbiter) (slot.ScriptRuntime, error) {
			runtime, factoryErr := def.Factory(index, gen, c, a)
			if factoryErr != nil {
				return nil, factoryErr
			}
			r := runtime.(*scriptRuntime)
			r.buildOverride = override
			return &paramsObservedRuntime{scriptRuntime: r, observed: h.observed[index]}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.run = &liveRun{cfg: liveConfig{opts: options{profile: h.path}, profile: p, prog: prog, stderr: io.Discard},
		log: slog.New(slog.DiscardHandler), sup: sup, loop: out, scriptDef: def, sum: sha256.Sum256(data)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		if err := out.Stop(); err != nil {
			t.Error(err)
		}
		h.rec.With(func(r *simtest.Recorder) { simtest.AssertNoneHeld(t, nil, r) })
	})
	h.sync()
	return h
}

func paramsProfile(name, key, extra string) []byte {
	return fmt.Appendf(nil, `{"schemaVersion":0,"name":%q,"script":{"entry":"main.ts","api":"0.21","params":{"key":%s}%s},"timing":{"tapPressMs":0,"tapGapMs":0},"layers":{"base":{}}}`, name, key, extra)
}

func (h *liveParamsHarness) sync() {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.run.sup.Sync(ctx); err != nil {
		h.t.Fatal(err)
	}
}

func (h *liveParamsHarness) add(id input.DeviceID) {
	h.t.Helper()
	h.events <- input.Event{Kind: input.DeviceAdded, Device: id, Info: &input.DeviceInfo{
		ID: id, Name: fmt.Sprintf("Pad %d", id), GUID: "0123456789abcdef0123456789abcdef", Type: "xboxone", IsGamepad: true, ButtonMask: 3,
	}}
	h.sync()
}

func (h *liveParamsHarness) edge(id input.DeviceID, control string, kind input.EventKind) {
	h.events <- input.Event{Kind: kind, Device: id, Control: control}
	h.sync()
}

func (h *liveParamsHarness) observation(index int) paramsObservation {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.edge(input.DeviceID(index+1), "east", input.ButtonDown)
		h.edge(input.DeviceID(index+1), "east", input.ButtonUp)
		var got paramsObservation
		for len(h.observed[index]) > 0 {
			got = <-h.observed[index]
		}
		if got.host != nil {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatal("script did not activate")
	return paramsObservation{}
}

func (h *liveParamsHarness) save(data []byte) {
	h.run.reloadProfile(context.Background(), watch.Change{Data: data, Sum: sha256.Sum256(data)})
}

func TestLiveParamsOnlyPreservesSlotsTimersAndHeldCycles(t *testing.T) {
	h := newLiveParamsHarness(t, liveParamsSource, 2, nil)
	for id := input.DeviceID(1); id <= 2; id++ {
		h.add(id)
	}
	before := []paramsObservation{h.observation(0), h.observation(1)}
	h.edge(1, "south", input.ButtonDown)
	h.edge(2, "south", input.ButtonDown)
	trace := h.rec.Trace()
	h.save(paramsProfile("params", `"Z"`, ""))
	if strings.Contains(h.rec.Trace()[len(trace):], "-LCtrl") {
		t.Fatal("params-only save released a held key")
	}
	time.Sleep(30 * time.Millisecond)
	trace = h.rec.Trace()
	for index := range 2 {
		after := h.observation(index)
		if after.host != before[index].host || after.ticks <= before[index].ticks {
			t.Fatalf("slot %d lost host/timer: before=%+v after=%+v", index, before[index], after)
		}
		if gen := h.run.sup.Status().Slots[index].Gen; gen != 1 {
			t.Fatalf("slot %d generation=%d, want 1", index, gen)
		}
		h.edge(input.DeviceID(index+1), "south", input.ButtonUp)
		after = h.observation(index)
		if after.releases != 1 {
			t.Fatalf("slot %d lost its held control cycle: releases=%d", index, after.releases)
		}
	}
	if got := h.rec.Trace()[len(trace):]; strings.Count(got, "+Z@") < 2 || strings.Contains(got, "+X@") {
		t.Fatalf("both slots did not use updated params: %s", got)
	}
}

func TestLiveParamsFailureIsAtomicAndDoesNotPolluteReload(t *testing.T) {
	source := strings.Replace(liveParamsSource, `import { defineScript, param, on, every, key }`, `import { defineScript, param, on, every, key, pad }`, 1)
	source = strings.Replace(source, `param.key({default:"X"})`, `pad.info.name === "Pad 2" ? param.choice({default:"X",options:["X","Y"]}) : param.key({default:"X"})`, 1)
	h := newLiveParamsHarness(t, source, 2, nil, true)
	h.add(1)
	h.add(2)
	before := []paramsObservation{h.observation(0), h.observation(1)}
	oldSum := h.run.sum
	h.save(paramsProfile("params", `"Z"`, "")) // valid for slot 1, invalid for slot 2
	if h.run.sum != oldSum || h.run.cfg.profile.Script.Params["key"] != "X" {
		t.Fatal("failed save changed committed profile")
	}
	trace := h.rec.Trace()
	for index := range 2 {
		if after := h.observation(index); after.host != before[index].host {
			t.Fatalf("failed save replaced slot %d host", index)
		}
	}
	if got := h.rec.Trace()[len(trace):]; strings.Contains(got, "+Z@") || strings.Count(got, "+X@") < 2 {
		t.Fatalf("failed transaction changed one slot: %s", got)
	}
	// Source watcher candidates inherit the previous valid configuration even
	// though no later profile save repaired the rejected values.
	if err := os.WriteFile(filepath.Join(filepath.Dir(h.path), "main.ts"), []byte(source+"\n// reload source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for index := range 2 {
		deadline := time.Now().Add(3 * time.Second)
		var after paramsObservation
		for after = h.observation(index); after.host == before[index].host && time.Now().Before(deadline); after = h.observation(index) {
			time.Sleep(5 * time.Millisecond)
		}
		if after.host == before[index].host {
			t.Fatalf("source watcher did not reload slot %d after rejected params", index)
		}
	}
	if got := h.rec.Trace()[len(trace):]; strings.Contains(got, "+Z@") {
		t.Fatalf("source reload inherited rejected params: %s", got)
	}
	// Other resolved profile/script changes still spend a new generation.
	h.save(paramsProfile("renamed", `"X"`, `,"tickHz":30`))
	for index := range 2 {
		if h.run.sup.Status().Slots[index].Gen != 2 {
			t.Fatalf("ordinary reload did not switch slot %d", index)
		}
	}
}

func TestLiveParamsPausedAndUnassignedSlots(t *testing.T) {
	h := newLiveParamsHarness(t, liveParamsSource, 2, nil)
	h.add(1)
	h.observation(0)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.run.sup.Pause(ctx, true); err != nil {
		t.Fatal(err)
	}
	oldSum := h.run.sum
	h.save(paramsProfile("params", `true`, ""))
	if h.run.sum != oldSum {
		t.Fatal("paused runtime forgot its parameter schema")
	}
	h.save(paramsProfile("params", `"Z"`, ""))
	h.add(2)
	if err := h.run.sup.Pause(ctx, false); err != nil {
		t.Fatal(err)
	}
	trace := h.rec.Trace()
	h.observation(0)
	h.observation(1)
	if got := h.rec.Trace()[len(trace):]; strings.Contains(got, "+X@") || strings.Count(got, "+Z@") < 2 {
		t.Fatalf("resumed/dormant slot used stale params: %s", got)
	}
}

func TestLiveParamsSourceWatcherInheritsLatestValues(t *testing.T) {
	source := strings.Replace(liveParamsSource, "  setup(ctx) {", `  onActivate(ctx) { key.tap(ctx.params.key,{holdMs:0}); },
  setup(ctx) {`, 1)
	h := newLiveParamsHarness(t, source, 1, nil, true)
	h.add(1)
	before := h.observation(0)
	h.save(paramsProfile("params", `"Z"`, ""))
	trace := h.rec.Trace()
	if err := os.WriteFile(filepath.Join(filepath.Dir(h.path), "main.ts"), []byte(source+"\n// successful source reload\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var after paramsObservation
	for after = h.observation(0); after.host == before.host && time.Now().Before(deadline); after = h.observation(0) {
		time.Sleep(5 * time.Millisecond)
	}
	if after.host == before.host {
		t.Fatal("source watcher did not install its candidate")
	}
	if got := h.rec.Trace()[len(trace):]; strings.Contains(got, "+X@") || !strings.Contains(got, "+Z@") {
		t.Fatalf("source watcher inherited stale lifecycle options: %s", got)
	}
}

func TestLiveParamsRefreshesInFlightCandidateBeforeActivation(t *testing.T) {
	entered, released := make(chan struct{}), make(chan struct{})
	var builds atomic.Uint32
	override := func(ctx context.Context, root string, cfg *profile.ScriptConfig) (*goja.Program, error) {
		if builds.Add(1) == 2 {
			close(entered)
			select {
			case <-released:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		result, err := compile.Build(compile.BuildOptions{Root: root, Entry: cfg.Entry, API: cfg.API})
		if err != nil {
			return nil, err
		}
		return goja.Compile(cfg.Entry, string(result.JS), false)
	}
	source := strings.Replace(liveParamsSource, "  setup(ctx) {", `  onActivate(ctx) { key.tap(ctx.params.key,{holdMs:0}); },
  setup(ctx) {`, 1)
	h := newLiveParamsHarness(t, source, 1, override)
	t.Cleanup(func() {
		select {
		case <-released:
		default:
			close(released)
		}
	})
	h.add(1)
	before := h.observation(0)
	h.save(paramsProfile("renamed", `"X"`, ""))
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("candidate did not start")
	}
	h.save(paramsProfile("renamed", `"Z"`, ""))
	trace := h.rec.Trace()
	close(released)
	deadline := time.Now().Add(3 * time.Second)
	var after paramsObservation
	for after = h.observation(0); after.host == before.host && time.Now().Before(deadline); after = h.observation(0) {
		time.Sleep(5 * time.Millisecond)
	}
	if after.host == before.host {
		t.Fatal("pending candidate did not replace its previous host")
	}
	if got := h.rec.Trace()[len(trace):]; strings.Contains(got, "+X@") || !strings.Contains(got, "+Z@") {
		t.Fatalf("candidate activation used stale params: %s", got)
	}
	if gen := h.run.sup.Status().Slots[0].Gen; gen != 2 {
		t.Fatalf("params save superseded the pending generation: %d", gen)
	}
}

func TestLiveParamsRejectsCandidateWithIncompatibleSchema(t *testing.T) {
	entered, released := make(chan struct{}), make(chan struct{})
	var builds atomic.Uint32
	source := strings.Replace(liveParamsSource, "  setup(ctx) {", `  onActivate(ctx) { key.tap(ctx.params.key,{holdMs:0}); },
  setup(ctx) {`, 1)
	override := func(ctx context.Context, root string, cfg *profile.ScriptConfig) (*goja.Program, error) {
		if builds.Add(1) == 2 {
			close(entered)
			select {
			case <-released:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			candidate := strings.Replace(source, `param.key({default:"X"})`, `param.choice({default:"X",options:["X"]})`, 1)
			if err := os.WriteFile(filepath.Join(root, "candidate.ts"), []byte(candidate), 0o600); err != nil {
				return nil, err
			}
			result, err := compile.Build(compile.BuildOptions{Root: root, Entry: "candidate.ts", API: cfg.API})
			if err != nil {
				return nil, err
			}
			return goja.Compile(cfg.Entry, string(result.JS), false)
		}
		result, err := compile.Build(compile.BuildOptions{Root: root, Entry: cfg.Entry, API: cfg.API})
		if err != nil {
			return nil, err
		}
		return goja.Compile(cfg.Entry, string(result.JS), false)
	}
	h := newLiveParamsHarness(t, source, 1, override)
	t.Cleanup(func() {
		select {
		case <-released:
		default:
			close(released)
		}
	})
	h.add(1)
	before := h.observation(0)
	h.save(paramsProfile("renamed", `"X"`, ""))
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("candidate did not start")
	}
	h.save(paramsProfile("renamed", `"Z"`, ""))
	trace := h.rec.Trace()
	close(released)
	deadline := time.Now().Add(3 * time.Second)
	after := h.observation(0)
	for after.pending && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		after = h.observation(0)
	}
	if after.pending || after.host != before.host {
		t.Fatalf("incompatible candidate replaced the valid host: %+v", after)
	}
	if got := h.rec.Trace()[len(trace):]; strings.Contains(got, "+X@") || !strings.Contains(got, "+Z@") {
		t.Fatalf("incompatible candidate activated or changed valid params: %s", got)
	}
}
