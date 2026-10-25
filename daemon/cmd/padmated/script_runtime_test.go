package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
)

func TestScriptRuntimeLogsReachProductionLogger(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.ts")
	source := `import { log, on } from "padmate";
log.info("setup", 1);
on("south", "press", () => log.warn("press", 2));
export default {teardown() {log.error("teardown");}};`
	if err := os.WriteFile(entry, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logged, nil))
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false, logger)
	clk := clock.NewFake()
	arb := output.NewArbiter(clk, simtest.NewRecorder(clk), output.ArbiterOptions{})
	r, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Dispose() }()
	if _, err := r.Dispatch(slot.ScriptEvent{Control: "south", Gesture: "press"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"msg":"setup 1"`, `"msg":"press 2"`, `"source":"script"`} {
		if !strings.Contains(logged.String(), want) {
			t.Fatalf("production script log missing %s: %s", want, logged.String())
		}
	}
	r.buildOverride = func(context.Context, string, *profile.ScriptConfig) (*goja.Program, error) {
		return goja.Compile("next.js", `globalThis.__padmate_bundle = {default:{}};`, false)
	}
	if err := r.Reload(2); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(logged.String(), `"msg":"teardown"`) && time.Now().Before(deadline) {
		clk.Advance(time.Millisecond)
		if err := r.RunDue(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(logged.String(), `"msg":"teardown"`) {
		t.Fatalf("retired teardown log not drained: %s", logged.String())
	}
}

func TestScriptRuntimeDrainsFailedAndCanceledCandidateLogs(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(entry, []byte(`export default {};`), 0o600); err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logged, nil))
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false, logger)
	clk := clock.NewFake()
	arb := output.NewArbiter(clk, simtest.NewRecorder(clk), output.ArbiterOptions{})
	r, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Dispose() }()
	r.buildOverride = func(context.Context, string, *profile.ScriptConfig) (*goja.Program, error) {
		return goja.Compile("failed.js", `__padmate.log.warn("failed candidate"); throw new Error("setup failed");`, false)
	}
	if err := r.Reload(2); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for r.pending != nil && time.Now().Before(deadline) {
		clk.Advance(time.Millisecond)
		_ = r.RunDue()
		time.Sleep(time.Millisecond)
	}
	if r.pending != nil || !strings.Contains(logged.String(), `"msg":"failed candidate"`) {
		t.Fatalf("failed candidate log not drained: %s", logged.String())
	}
	for len(r.wake) > 0 {
		<-r.wake
	}
	r.buildOverride = func(context.Context, string, *profile.ScriptConfig) (*goja.Program, error) {
		return goja.Compile("canceled.js", `__padmate.log.info("canceled candidate"); globalThis.__padmate_bundle = {default:{}};`, false)
	}
	if err := r.Reload(3); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.wake: // worker formatted a log before its result was adopted
	case <-time.After(time.Second):
		t.Fatal("candidate setup did not log")
	}
	def.update(nil)
	if err := r.Reload(4); err != nil {
		t.Fatal(err)
	}
	if err := r.RunDue(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logged.String(), `"msg":"canceled candidate"`) {
		t.Fatalf("canceled candidate log not drained: %s", logged.String())
	}
}

func TestScriptRuntimeReloadFailureKeepsWatcher(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.ts")
	valid := `import { defineScript } from "padmate";
export default defineScript({ setup() {} });
`
	if err := os.WriteFile(entry, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, true)
	clk := clock.NewFake()
	recorder := simtest.NewRecorder(clk)
	arb := output.NewArbiter(clk, recorder, output.ArbiterOptions{})
	got, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = got.Dispose() }()
	oldLife, oldWatcher, oldDone := got.lifecycle, got.watcher, got.done

	bad := filepath.Join(dir, "bad.ts")
	if err := os.WriteFile(bad, []byte(`import {`), 0o600); err != nil {
		t.Fatal(err)
	}
	def.update(&profile.ScriptConfig{Entry: "bad.ts", API: profile.CurrentScriptAPI})
	if err := got.Reload(2); err != nil {
		t.Fatalf("Reload returned scheduling error: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	var reloadErr error
	for got.pending != nil && time.Now().Before(deadline) {
		reloadErr = errors.Join(reloadErr, got.RunDue())
		time.Sleep(time.Millisecond)
	}
	if got.pending != nil {
		t.Fatal("failed candidate remained pending")
	}
	if reloadErr == nil {
		t.Fatal("failed reload did not retain candidate error")
	}
	if got.lifecycle != oldLife {
		t.Fatal("failed reload replaced the active lifecycle")
	}
	if got.watcher != oldWatcher || got.done != oldDone || got.stop == nil {
		t.Fatal("failed reload stopped the old script watcher")
	}
}

func TestScriptRuntimeReceivesPhysicalCycleMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(`import { defineScript, on, key } from "padmate";
let cycle = 0;
export default defineScript({ setup() {
  on("south", "press", e => { if (e.duration === 0 && e.count === 1 && e.cycle > 0) cycle = e.cycle; });
  on("south", "release", e => { if (e.duration === 5 && e.count === 1 && e.cycle === cycle) key.down("X"); });
} });`), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake()
	recorder := simtest.NewRecorder(clk)
	arb := output.NewArbiter(clk, recorder, output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	r, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	s := newScriptTestSlot(t, clk, arb, r, `{}`)
	t.Cleanup(func() { _ = s.ReleaseAll(); _ = s.Dispose(); simtest.AssertNoneHeld(t, arb, recorder) })
	clk.Set(clock.Time(10 * time.Millisecond))
	s.Handle(input.Event{Kind: input.ButtonDown, Control: "south"})
	clk.Set(clock.Time(15 * time.Millisecond))
	s.Handle(input.Event{Kind: input.ButtonUp, Control: "south"})
	if len(recorder.Down()) != 1 || recorder.Down()[0].Key.String() != "X" {
		t.Fatalf("production metadata handler did not run: %v", recorder.Edges())
	}
}

func TestScriptWatcherWallFailureStopsOldRuntime(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(entry, []byte(`import { defineScript, key, every } from "padmate";
export default defineScript({ setup() { every(10, () => key.down("Y")); }, onActivate() { key.down("X"); } });`), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.Real()
	recorder := simtest.NewRecorder(clk)
	arb := output.NewArbiter(clk, recorder, output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, true)
	r, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Dispose(); simtest.AssertNoneHeld(t, arb, recorder) })
	if len(recorder.Down()) != 1 || recorder.Down()[0].Key.String() != "X" {
		t.Fatalf("old runtime did not hold X: %v", recorder.Down())
	}
	r.buildOverride = func(context.Context, string, *profile.ScriptConfig) (*goja.Program, error) {
		return nil, &script.BudgetError{Clock: "wall", Budget: 2 * time.Second}
	}
	if err := os.WriteFile(entry, []byte(`import { defineScript } from "padmate";
export default defineScript({ setup() { /* changed */ } });`), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var reported error
	for !r.wallStopped && time.Now().Before(deadline) {
		reported = errors.Join(reported, r.RunDue())
		time.Sleep(2 * time.Millisecond)
	}
	if !script.IsWallBudget(reported) || !r.wallStopped || r.lifecycle != nil {
		t.Fatalf("watcher wall failure retained old runtime: err=%v stopped=%v", reported, r.wallStopped)
	}
	if len(recorder.Down()) != 0 {
		t.Fatalf("watcher wall failure left keys held: %v", recorder.Down())
	}
	before := len(recorder.Records())
	time.Sleep(30 * time.Millisecond)
	if err := r.RunDue(); err != nil {
		t.Fatal(err)
	}
	if len(recorder.Records()) != before {
		t.Fatalf("old timer emitted after watcher wall failure: %v", recorder.Edges()[before:])
	}
}

// This exercises the watcher through a real slot loop with no input or jobs
// after the save. A script file change must wake a sleeping slot by itself.
func TestScriptRuntimeIdleSlotAdoptsSavedScript(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.ts")
	write := func(source string) {
		t.Helper()
		if err := os.WriteFile(entry, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`import { defineScript } from "padmate";
export default defineScript({ setup() {} });`)
	clk := clock.Real()
	sender := &reloadEventSender{events: make(chan reloadEvent, 32)}
	arb := output.NewArbiter(clk, sender, output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, true)
	r, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	s := newScriptTestSlot(t, clk, arb, r, `{}`)
	edges := make(chan slot.Edge)
	done := make(chan error, 1)
	errorsC := make(chan []error, 4)
	go func() {
		done <- slot.RunLoop(context.Background(), slot.LoopConfig{
			Slot: s, Edges: edges,
			Errors: func(errs []error) { errorsC <- errs },
		})
	}()
	t.Cleanup(func() {
		close(edges)
		if err := <-done; err != nil {
			t.Errorf("RunLoop: %v", err)
		}
	})
	write(`import { defineScript, key } from "padmate";
export default defineScript({ onActivate() { key.down("X"); } });`)
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case ev := <-sender.events:
			if !ev.up && ev.key == "X" {
				goto activated
			}
		case <-deadline.C:
			t.Fatal("idle slot did not activate saved main.ts within 1 s")
		}
	}
activated:
	write(`import {`)
	select {
	case errs := <-errorsC:
		if len(errs) == 0 {
			t.Fatal("failed saved script did not report an error")
		}
	case <-time.After(time.Second):
		t.Fatal("failed saved script did not report a slot error within 1 s")
	}
	write(`import { defineScript, key } from "padmate";
export default defineScript({ onActivate() { key.down("Y"); } });`)
	select {
	case ev := <-sender.events:
		if ev.key != "Y" || ev.up {
			t.Fatalf("recovered saved script output = %+v, want Y down", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("script watcher did not recover after failed save within 1 s")
	}
}

func newScriptTestSlot(t *testing.T, clk clock.Clock, arb slot.Arbiter, r *scriptRuntime, bindings string) *slot.Slot {
	t.Helper()
	program := newScriptTestProgram(t, bindings)
	s, err := slot.New(slot.Config{Index: 0, Gen: 1, Program: program, Arbiter: arb, Clock: clk, Script: r})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newScriptTestProgram(t *testing.T, bindings string) *engine.Program {
	t.Helper()
	p, diagnostics := profile.Parse([]byte(`{"schemaVersion":0,"name":"script-test","layers":{"base":` + bindings + `}}`))
	if p == nil || diagnostics.HasErrors() {
		t.Fatalf("profile.Parse: %v", diagnostics)
	}
	program, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	return program
}

type reloadEvent struct {
	key string
	up  bool
	at  time.Time
}

type reloadEventSender struct{ events chan reloadEvent }

func (s *reloadEventSender) Send(events []output.KeyEvent) (int, error) {
	for _, e := range events {
		s.events <- reloadEvent{key: e.Key.String(), up: e.Up, at: time.Now()}
	}
	return len(events), nil
}

// A real TypeScript setup runs for 900 ms on the candidate worker. The
// declaration layer must still emit on the same slot in under 5 ms.
func TestScriptRuntimeSlowSetupKeepsDeclarationResponsive(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(entry, []byte(`import { defineScript } from "padmate";
export default defineScript({ setup() {} });`), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.Real()
	sender := &reloadEventSender{events: make(chan reloadEvent, 32)}
	arb := output.NewArbiter(clk, sender, output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	r, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	bindings := `{"east":{"press":{"type":"keys","keys":"Space","mode":"hold"}}}`
	s := newScriptTestSlot(t, clk, arb, r, bindings)
	nextProgram := newScriptTestProgram(t, bindings)
	edges := make(chan slot.Edge, 1)
	jobs := make(chan func(*slot.Slot))
	done := make(chan error, 1)
	errorsC := make(chan []error, 4)
	go func() {
		done <- slot.RunLoop(context.Background(), slot.LoopConfig{
			Slot: s, Edges: edges, Jobs: jobs,
			Errors: func(errs []error) { errorsC <- errs },
		})
	}()
	t.Cleanup(func() {
		close(edges)
		if err := <-done; err != nil {
			t.Errorf("RunLoop: %v", err)
		}
	})
	if err := os.WriteFile(entry, []byte(`import { defineScript } from "padmate";
export default defineScript({ setup() {
  const until = Date.now() + 900;
  while (Date.now() < until) {}
} });`), 0o600); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	r.buildOverride = func(ctx context.Context, root string, cfg *profile.ScriptConfig) (*goja.Program, error) {
		result, err := compile.BuildCached(compile.BuildOptions{Root: root, Entry: cfg.Entry, API: cfg.API})
		if err != nil {
			return nil, err
		}
		program, err := goja.Compile(cfg.Entry, string(result.JS), false)
		close(started)
		return program, err
	}
	jobErr := make(chan error, 1)
	jobs <- func(s *slot.Slot) { jobErr <- s.Reload(nextProgram, 2) }
	if err := <-jobErr; err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("real TypeScript candidate did not start")
	}
	time.Sleep(100 * time.Millisecond)
	busy := make(chan bool, 1)
	jobs <- func(*slot.Slot) { busy <- r.pending != nil }
	if !<-busy {
		t.Fatal("TypeScript setup finished before declaration input")
	}
	start := time.Now()
	edges <- slot.Edge{Event: input.Event{Kind: input.ButtonDown, Control: "east"}}
	select {
	case ev := <-sender.events:
		if ev.key != "Space" || ev.up {
			t.Fatalf("declaration output = %+v, want Space down", ev)
		}
		if elapsed := ev.at.Sub(start); elapsed >= 5*time.Millisecond {
			t.Fatalf("declaration output took %s during 900 ms setup, want <5 ms", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("declaration output stalled during TypeScript setup")
	}
	edges <- slot.Edge{Event: input.Event{Kind: input.ButtonUp, Control: "east"}}
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case errs := <-errorsC:
			t.Fatalf("900 ms TypeScript candidate failed: %v", errs)
		case <-tick.C:
			swapped := make(chan bool, 1)
			select {
			case jobs <- func(*slot.Slot) { swapped <- r.pending == nil && r.gen == 2 }:
			case <-deadline.C:
				t.Fatal("900 ms TypeScript candidate did not finish")
			}
			if <-swapped {
				return
			}
		case <-deadline.C:
			t.Fatal("900 ms TypeScript candidate did not swap successfully")
		}
	}
}

func TestScriptRuntimeIdlePromiseErrorReachesSlotErrors(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(entry, []byte(`import { defineScript, sleep } from "padmate";
export default defineScript({
  onActivate: async () => { await sleep(50); throw new Error("idle promise failure"); }
});`), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.Real()
	sender := &reloadEventSender{events: make(chan reloadEvent, 8)}
	arb := output.NewArbiter(clk, sender, output.ArbiterOptions{})
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	r, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	s := newScriptTestSlot(t, clk, arb, r, `{}`)
	edges := make(chan slot.Edge)
	errorsC := make(chan []error, 4)
	done := make(chan error, 1)
	go func() {
		done <- slot.RunLoop(context.Background(), slot.LoopConfig{
			Slot: s, Edges: edges,
			Errors: func(errs []error) { errorsC <- errs },
		})
	}()
	t.Cleanup(func() {
		close(edges)
		if err := <-done; err != nil {
			t.Errorf("RunLoop: %v", err)
		}
	})
	select {
	case errs := <-errorsC:
		if !strings.Contains(errors.Join(errs...).Error(), "idle promise failure") {
			t.Fatalf("idle timer error = %v, want Promise rejection", errs)
		}
	case <-time.After(time.Second):
		t.Fatal("idle slot did not report Promise timer error within 1 s")
	}
}

func TestScriptRuntimeReloadDoesNotBlockDispatch(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.ts")
	valid := `import { defineScript, on, key } from "padmate";
export default defineScript({ setup() {
  on("south", "press", () => { key.down("Z"); });
} });
`
	if err := os.WriteFile(entry, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	clk := clock.NewFake()
	recorder := simtest.NewRecorder(clk)
	arb := output.NewArbiter(clk, recorder, output.ArbiterOptions{})
	got, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = got.Dispose() }()
	started := make(chan struct{})
	release := make(chan struct{})
	got.buildOverride = func(context.Context, string, *profile.ScriptConfig) (*goja.Program, error) {
		close(started)
		<-release
		return goja.Compile("candidate.js", `globalThis.__padmate_bundle = { default: { setup() {} } };`, false)
	}
	if err := got.Reload(2); err != nil {
		t.Fatalf("Reload returned scheduling error: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("candidate worker did not start")
	}
	firstDeadline, ok := got.NextDeadline()
	if !ok {
		t.Fatal("pending profile reload has no deadline")
	}
	clk.Advance(reloadPollInterval)
	if next, ok := got.NextDeadline(); !ok || next != firstDeadline {
		t.Fatalf("profile reload deadline moved without RunDue: %v, %v; want %v", next, ok, firstDeadline)
	}
	// The old runtime must continue to receive declaration-layer input while
	// the replacement worker is blocked.
	if consumed, err := got.Dispatch(slot.ScriptEvent{Control: "south", Gesture: "press", Time: clk.Now()}); err != nil || !consumed {
		t.Fatalf("Dispatch while reload pending = consumed %v, err %v", consumed, err)
	}
	if recorder.Calls() == 0 {
		t.Fatal("Dispatch while reload pending produced no output")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for got.pending != nil && time.Now().Before(deadline) {
		clk.Advance(time.Millisecond)
		if err := got.RunDue(); err != nil {
			t.Fatalf("RunDue while reload pending: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	if got.pending != nil {
		t.Fatal("candidate did not swap after worker completion")
	}
	if got.reloadErr != nil {
		t.Fatalf("candidate swap failed: %v", got.reloadErr)
	}
}

func TestScriptRuntimeReportsRetiredTeardown(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.ts")
	old := `import { defineScript, key } from "padmate";
export default defineScript({ onActivate() { key.down("X"); }, teardown() {
  const start = Date.now(); while (Date.now() - start < 50) {}
  key.up("X"); key.down("Y"); key.releaseAll();
  throw new Error("retired teardown failed");
} });`
	if err := os.WriteFile(entry, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	def := newScriptDefinition(filepath.Join(dir, "profile.json"), &profile.ScriptConfig{Entry: "main.ts", API: profile.CurrentScriptAPI}, false)
	clk := clock.NewFake()
	arb := output.NewArbiter(clk, simtest.NewRecorder(clk), output.ArbiterOptions{})
	got, err := newScriptRuntime(def, 0, 1, clk, arb)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = got.Dispose() }()
	got.buildOverride = func(context.Context, string, *profile.ScriptConfig) (*goja.Program, error) {
		return goja.Compile("candidate.js", `globalThis.__padmate_bundle = {default: {onActivate(){__padmate.key.down("Z")}}};`, false)
	}
	if err := got.Reload(2); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for got.pending != nil && time.Now().Before(deadline) {
		clk.Advance(time.Millisecond)
		if err := got.RunDue(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if got.pending != nil {
		t.Fatal("replacement runtime did not install")
	}
	if held := arb.Held(); len(held) != 1 || len(held[0].Owners) != 1 || held[0].Owners[0].Gen != 2 {
		t.Fatalf("after reload held = %+v, want only new runtime owner", held)
	}
	for time.Now().Before(deadline) {
		clk.Advance(time.Millisecond)
		if err := got.RunDue(); err != nil {
			if !strings.Contains(err.Error(), "retired teardown failed") {
				t.Fatalf("asynchronous teardown error = %v", err)
			}
			if held := arb.Held(); len(held) != 1 || len(held[0].Owners) != 1 || held[0].Owners[0].Gen != 2 {
				t.Fatalf("late teardown affected new owner: %+v", held)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("retired teardown error was not reported")
}
