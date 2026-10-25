package script

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/dtsgen"
)

// activeMacroHost activates ps in a runtime without an output sink, which
// records its output (Host.Output).
func activeMacroHost(t *testing.T, c clock.Clock, ps Programs) *Host {
	t.Helper()
	h := NewHostWithOptions(HostOptions{Clock: c})
	if err := h.ActivatePrograms(ps); err != nil {
		t.Fatal(err)
	}
	if err := h.OnActivate(); err != nil {
		t.Fatal(err)
	}
	return h
}

// outputTrace renders a runtime's recorded output as "+A -A".
func outputTrace(h *Host) string {
	var edges []string
	for _, e := range h.Output() {
		prefix := "+"
		if e.Up {
			prefix = "-"
		}
		edges = append(edges, prefix+e.Spec)
	}
	return strings.Join(edges, " ")
}

// Each macro that fails to build or load keeps its own error; the others
// load and run, and each failing one reports its error once per runtime.
func TestMacroLoadKeepsEachFileError(t *testing.T) {
	ps := ProgramsForTest(t, map[string]string{
		"macros/ok.ts":         `import { key } from "padmate"; export default function () { key.down("A"); key.up("A"); }`,
		"macros/broken.ts":     "const = 1\n",
		"macros/throws.ts":     "throw new Error(\"top\");\nexport default function () {}\n",
		"macros/notfn.ts":      "export default 42;\n",
		"macros/nodefault.ts":  "export const x = 1;\n",
		"macros/keyattop.ts":   "import { key } from \"padmate\";\nkey.tap(\"A\");\nexport default function () {}\n",
		"macros/sleepattop.ts": "import { sleep } from \"padmate\";\nsleep(1);\nexport default function () {}\n",
		"macros/zlast.ts":      `import { key } from "padmate"; export default function () { key.down("Z"); key.up("Z"); }`,
	}, "", "ok.ts", "broken.ts", "throws.ts", "notfn.ts", "nodefault.ts", "keyattop.ts", "sleepattop.ts", "missing.ts", "zlast.ts")
	h := activeMacroHost(t, clock.NewFake(), ps)
	got := map[string]string{}
	var files []string
	for _, me := range h.MacroErrors() {
		files = append(files, me.File)
		got[me.File] = me.Error()
	}
	want := []string{"broken.ts", "keyattop.ts", "missing.ts", "nodefault.ts", "notfn.ts", "sleepattop.ts", "throws.ts"}
	if !slices.Equal(files, want) {
		t.Fatalf("macros with errors %q, want %q", files, want)
	}
	for file, text := range map[string]string{
		"broken.ts":     "broken.ts:1:7: error:",
		"throws.ts":     "throws.ts:1:",
		"notfn.ts":      "the default export of a macro must be a function",
		"nodefault.ts":  "the default export of a macro must be a function",
		"keyattop.ts":   `TypeError: macro "keyattop.ts" is not running`,
		"sleepattop.ts": `macro "sleepattop.ts" is not running`,
	} {
		if !strings.Contains(got[file], text) {
			t.Errorf("%s: error %q, want it to contain %q", file, got[file], text)
		}
	}
	for _, file := range append(want, "unknown.ts") {
		err := h.RunMacro(file, Event{})
		if len(MacroErrorsOf(err)) != 1 || MacroErrorsOf(err)[0].File != file {
			t.Errorf("RunMacro(%s) = %v, want the macro's error", file, err)
		}
		if err := h.RunMacro(file, Event{}); err != nil {
			t.Errorf("RunMacro(%s) again = %v, want the error reported once", file, err)
		}
	}
	if err := h.RunMacro("unknown.ts", Event{}); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"ok.ts", "zlast.ts"} {
		if err := h.RunMacro(file, Event{}); err != nil {
			t.Fatalf("RunMacro(%s): %v", file, err)
		}
	}
	if got := outputTrace(h); got != "+A -A +Z -Z" {
		t.Fatalf("output %q", got)
	}
}

// A missing macro and one never built report ErrMacroNotFound.
func TestMacroNotFound(t *testing.T) {
	h := activeMacroHost(t, clock.NewFake(), Programs{Macros: []MacroProgram{{File: "gone.ts", Err: ErrMacroNotFound}}})
	for _, file := range []string{"gone.ts", "other.ts"} {
		if err := h.RunMacro(file, Event{}); !errors.Is(err, ErrMacroNotFound) {
			t.Errorf("RunMacro(%s) = %v", file, err)
		}
	}
}

// padmate/math works in a macro, alone and beside a legacy entry, and a
// helper module a macro imports dynamically gets the macro's own padmate
// module; the legacy entry's dynamic import is unchanged.
func TestMacroMathAndDynamicImport(t *testing.T) {
	files := map[string]string{
		"macros/m.ts": `import { key } from "padmate";
import { clamp } from "padmate/math";
export default async function () {
  key.down(clamp(5, 0, 1) === 1 ? "B" : "C");
  const { press } = await import("./lib/press");
  press("D");
}`,
		"macros/lib/press.ts": `import { key } from "padmate";
export function press(k: string) { key.up("B"); key.down(k); key.up(k); }`,
		"main.ts": `import { defineScript, on } from "padmate";
export default defineScript({ setup() {
  on("south", "press", async () => { const { go } = await import("./helper"); go(); });
} });`,
		"helper.ts": `import { key } from "padmate";
export function go() { key.down("L"); key.up("L"); }`,
	}
	for _, entry := range []string{"", "main.ts"} {
		t.Run("entry="+entry, func(t *testing.T) {
			h := activeMacroHost(t, clock.NewFake(), ProgramsForTest(t, files, entry, "m.ts"))
			if err := h.RunMacro("m.ts", Event{}); err != nil {
				t.Fatal(err)
			}
			want := "+B -B +D -D"
			if entry != "" {
				if err := h.Dispatch(Event{Control: "south", Gesture: "press"}); err != nil {
					t.Fatal(err)
				}
				want += " +L -L"
			}
			if err := h.RunDue(); err != nil {
				t.Fatal(err)
			}
			if got := outputTrace(h); got != want {
				t.Fatalf("output %q, want %q", got, want)
			}
		})
	}
}

// A macro's errors name its file as a person reads it: Chinese, spaces and
// parentheses decoded from the source map, relative to the macros folder.
func TestMacroDiagnosticsNameTheFile(t *testing.T) {
	const (
		syncFile  = "复制全部 (1).ts"
		asyncFile = "下一个 笔刷 (2).ts"
		buildFile = "坏（2）.ts"
	)
	ps := ProgramsForTest(t, map[string]string{
		"macros/" + syncFile:  "export default function () {\n  throw new Error(\"sync\");\n}\n",
		"macros/" + asyncFile: "import { sleep } from \"padmate\";\nexport default async function () {\n  await sleep(5);\n  throw new Error(\"async\");\n}\n",
		"macros/" + buildFile: "const = 1\n",
	}, "", syncFile, asyncFile, buildFile)
	c := clock.NewFake()
	h := activeMacroHost(t, c, ps)
	check := func(err error, file string, line int) {
		t.Helper()
		mes := MacroErrorsOf(err)
		if len(mes) != 1 || mes[0].File != file {
			t.Fatalf("error %v, want one of %s", err, file)
		}
		ds := DiagnosticsOf(err)
		if len(ds) != 1 || ds[0].File != file || ds[0].Line != line || ds[0].Column == 0 {
			t.Fatalf("diagnostics %+v, want %s:%d:<column>", ds, file, line)
		}
		if prefix := fmt.Sprintf("%s:%d:", file, line); !strings.HasPrefix(err.Error(), prefix) {
			t.Fatalf("error %q does not start with %q", err, prefix)
		}
	}
	check(h.RunMacro(syncFile, Event{}), syncFile, 2)
	check(h.RunMacro(buildFile, Event{}), buildFile, 1)
	if err := h.RunMacro(asyncFile, Event{}); err != nil {
		t.Fatal(err)
	}
	c.Advance(5 * time.Millisecond)
	check(h.RunDue(), asyncFile, 4)
}

// The bridge of a macro has exactly the members its padmate module exports
// and its declarations mark @macro; the bridges are frozen.
func TestMacroBridgeMembers(t *testing.T) {
	h := activeMacroHost(t, clock.NewFake(), ProgramsForTest(t, map[string]string{
		"macros/x.ts": "export default function () {}\n",
	}, "", "x.ts"))
	api := h.VM.Get("__padmate_macro_api").ToObject(h.VM)
	bridge := api.Get("x.ts").ToObject(h.VM)
	got := bridge.Keys()
	want, err := dtsgen.MacroMembers(dtsgen.HostAPI)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("bridge members %q, @macro members %q", got, want)
	}
	if _, err := h.VM.RunString(`"use strict"; __padmate_macro_api["x.ts"].key = null;`); err == nil {
		t.Error("a bridge member could be replaced")
	}
	if _, err := h.VM.RunString(`"use strict"; __padmate_macro_api["y.ts"] = {};`); err == nil {
		t.Error("a bridge could be added")
	}
	if _, err := h.VM.RunString(`"use strict"; __padmate_macro_api = {};`); err == nil {
		t.Error("the bridges could be replaced")
	}
}

// RunDue enters no watchdog when nothing is due, with or without a run of
// a macro waiting; a due timer costs one call.
func TestRunDueIdleEntersNoWatchdog(t *testing.T) {
	c := clock.NewFake()
	h := NewHostWithOptions(HostOptions{Clock: c})
	h.EnableWatchdog()
	if err := h.ActivatePrograms(ProgramsForTest(t, map[string]string{
		"macros/wait.ts": `import { key, sleep } from "padmate"; export default async function () { await sleep(10); key.down("A"); key.up("A"); }`,
	}, "", "wait.ts")); err != nil {
		t.Fatal(err)
	}
	idle := func(what string) {
		t.Helper()
		before := h.watchdogCalls
		for range 3 {
			if err := h.RunDue(); err != nil {
				t.Fatal(err)
			}
		}
		if h.watchdogCalls != before {
			t.Fatalf("%s: RunDue made %d watchdog calls", what, h.watchdogCalls-before)
		}
	}
	idle("no run")
	if err := h.RunMacro("wait.ts", Event{}); err != nil {
		t.Fatal(err)
	}
	idle("run sleeping")
	c.Advance(10 * time.Millisecond)
	before := h.watchdogCalls
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	if n := h.watchdogCalls - before; n != 1 {
		t.Fatalf("due timer: %d watchdog calls, want 1", n)
	}
	idle("run ended")
	if got := outputTrace(h); got != "+A -A" {
		t.Fatalf("output %q", got)
	}
}

// console's lines name the macro whose call writes them, at its top level,
// in its run and after an await; the legacy script's name none.
func TestMacroConsoleNamesTheMacro(t *testing.T) {
	c := clock.NewFake()
	logs := NewLogBuffer(nil)
	h := NewHostWithOptions(HostOptions{Clock: c, Logs: logs})
	if err := h.ActivatePrograms(ProgramsForTest(t, map[string]string{
		"main.ts": `import { defineScript, on } from "padmate";
console.log("legacy top");
export default defineScript({ setup() { on("south", "press", () => console.warn("legacy handler")); } });`,
		"macros/m.ts": `import { sleep } from "padmate";
console.log("top");
export default async function () { console.log("run"); await sleep(10); console.error("after"); }`,
	}, "main.ts", "m.ts")); err != nil {
		t.Fatal(err)
	}
	if err := h.RunMacro("m.ts", Event{}); err != nil {
		t.Fatal(err)
	}
	c.Advance(10 * time.Millisecond)
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	if err := h.Dispatch(Event{Control: "south", Gesture: "press"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := logs.Drain()
	var got []string
	for _, e := range entries {
		got = append(got, e.Message+"@"+e.Macro)
	}
	if want := []string{"legacy top@", "top@m.ts", "run@m.ts", "after@m.ts", "legacy handler@"}; !slices.Equal(got, want) {
		t.Fatalf("logs %q, want %q", got, want)
	}
}

// A macro that is running ignores its trigger, with a debug log line that
// names it; its next trigger after the run ends starts a new run.
func TestMacroRetriggerIgnored(t *testing.T) {
	c := clock.NewFake()
	logs := NewLogBuffer(nil)
	h := NewHostWithOptions(HostOptions{Clock: c, Logs: logs})
	if err := h.ActivatePrograms(ProgramsForTest(t, map[string]string{
		"macros/m.ts": `import { key, sleep, log } from "padmate"; export default async function () { log.info("run"); key.down("A"); await sleep(10); }`,
	}, "", "m.ts")); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := h.RunMacro("m.ts", Event{}); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := logs.Drain()
	if len(entries) != 2 || entries[0].Message != "run" || entries[1].Level != "debug" || entries[1].Macro != "m.ts" {
		t.Fatalf("logs %+v", entries)
	}
	c.Advance(10 * time.Millisecond)
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	if err := h.RunMacro("m.ts", Event{}); err != nil {
		t.Fatal(err)
	}
	if got := outputTrace(h); got != "+A -A +A" {
		t.Fatalf("output %q", got)
	}
	h.ReleaseAll()
	if got := outputTrace(h); got != "+A -A +A -A" {
		t.Fatalf("output after ReleaseAll %q", got)
	}
}

// A macro triggered while the runtime builds runs once, when it is
// installed; Dispose and a failed build forget it.
func TestLifecycleQueuesMacrosWhileBuilding(t *testing.T) {
	ps := ProgramsForTest(t, map[string]string{
		"macros/m.ts": `import { key } from "padmate"; export default function () { key.down("A"); key.up("A"); }`,
	}, "", "m.ts")
	c := clock.NewFake()
	release := make(chan struct{})
	l := NewLifecycle(c)
	result := l.LoadPrograms(context.Background(), func(context.Context) (Programs, error) {
		<-release
		return ps, nil
	})
	for range 2 {
		if err := l.RunMacro("m.ts", Event{}); err != nil {
			t.Fatal(err)
		}
	}
	close(release)
	loaded := <-result
	if loaded.Err != nil {
		t.Fatal(loaded.Err)
	}
	if err := l.Swap(loaded.Host); err != nil {
		t.Fatal(err)
	}
	if d, ok := l.NextDeadline(); !ok || d != c.Now() {
		t.Fatalf("deadline with a queued macro = %v, %v", d, ok)
	}
	if err := l.RunDue(); err != nil {
		t.Fatal(err)
	}
	if got := outputTrace(loaded.Host); got != "+A -A" {
		t.Fatalf("output %q, want one run", got)
	}

	// Dispose forgets a queued macro.
	l2 := NewLifecycle(c)
	block := make(chan struct{})
	result2 := l2.LoadPrograms(context.Background(), func(context.Context) (Programs, error) {
		<-block
		return ps, nil
	})
	if err := l2.RunMacro("m.ts", Event{}); err != nil {
		t.Fatal(err)
	}
	if err := l2.Dispose(); err != nil {
		t.Fatal(err)
	}
	close(block)
	late := <-result2
	if late.Err != nil {
		t.Fatal(late.Err)
	}
	if err := l2.Swap(late.Host); err != nil {
		t.Fatal(err)
	}
	if err := l2.RunDue(); err != nil {
		t.Fatal(err)
	}
	if got := outputTrace(late.Host); got != "" {
		t.Fatalf("a disposed queue ran: %q", got)
	}
	_ = l2.Dispose()
}

// A restart build that fails forgets the macros triggered while it ran.
func TestLifecycleFailedRestartForgetsQueuedMacros(t *testing.T) {
	ps := ProgramsForTest(t, map[string]string{
		"macros/m.ts": `import { key } from "padmate"; export default function () { key.down("A"); key.up("A"); }`,
	}, "", "m.ts")
	c := clock.NewFake()
	l := NewLifecycle(c)
	fail := make(chan struct{})
	builds := 0
	first := <-l.LoadPrograms(context.Background(), func(context.Context) (Programs, error) {
		builds++
		if builds > 1 {
			<-fail
			return Programs{}, errors.New("rebuild failed")
		}
		return ps, nil
	})
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	if err := l.Swap(first.Host); err != nil {
		t.Fatal(err)
	}
	l.RecordInterruption(c.Now())
	c.Advance(time.Second)
	if err := l.RunDue(); err != nil {
		t.Fatal(err)
	}
	if l.State() != StateBuilding {
		t.Fatalf("state %v, want a restart building", l.State())
	}
	if err := l.RunMacro("m.ts", Event{}); err != nil {
		t.Fatal(err)
	}
	close(fail)
	deadline := time.Now().Add(5 * time.Second)
	for l.State() == StateBuilding && time.Now().Before(deadline) {
		_ = l.RunDue()
		time.Sleep(time.Millisecond)
	}
	if l.State() != StateDisposed || len(l.queued) != 0 {
		t.Fatalf("state %v, queued %v after a failed restart", l.State(), l.queued)
	}
}

// The adapter of a legacy BuildFunc keeps its nil checks.
func TestProgramsOfLegacyBuild(t *testing.T) {
	if programsOf(nil) != nil {
		t.Fatal("programsOf(nil) is not nil")
	}
	_, err := programsOf(func(context.Context) (*goja.Program, error) { return nil, nil })(context.Background())
	if err == nil || !strings.Contains(err.Error(), "nil program") {
		t.Fatalf("nil program error = %v", err)
	}
}

// The first frame of a stack names its file whole: with spaces and
// parentheses, inside a function's parentheses or not, percent-decoded.
func TestPromiseStackPosition(t *testing.T) {
	for _, tc := range []struct{ stack, file, line, column string }{
		{"Error: x\n\tat default (macros/a b %281%29.ts:3:9(12))\n\tat native", "macros/a b (1).ts", "3", "9"},
		{"Error: x\n\tat a (1).js:4:2(7)\n", "a (1).js", "4", "2"},
		{"Error: x\n\tat run (a (1).js:5:6(0))", "a (1).js", "5", "6"},
		{"Error: x\r\n\tat %E5%A4%8D.ts:1:1(3)\r\n", "复.ts", "1", "1"},
		{"Error: x\n\tat 100%.ts:2:3(4)", "100%.ts", "2", "3"},
	} {
		m := promiseStackPosition.FindStringSubmatch(tc.stack)
		if len(m) != 5 {
			t.Errorf("%q: no frame", tc.stack)
			continue
		}
		if got := stackFrameFile(m); got != tc.file || m[2] != tc.line || m[3] != tc.column {
			t.Errorf("%q: %s:%s:%s, want %s:%s:%s", tc.stack, got, m[2], m[3], tc.file, tc.line, tc.column)
		}
	}
}

// A describe-mode runtime loads the macros, running their top level, and
// reports which do not load; it runs none.
func TestMacroDescribeMode(t *testing.T) {
	logs := NewLogBuffer(nil)
	h := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), Mode: "describe", Logs: logs})
	if err := h.ActivatePrograms(ProgramsForTest(t, map[string]string{
		"macros/ok.ts":  `import { log } from "padmate"; log.info("loaded"); export default function () {}`,
		"macros/bad.ts": "export default 1;\n",
	}, "", "ok.ts", "bad.ts")); err != nil {
		t.Fatal(err)
	}
	if mes := h.MacroErrors(); len(mes) != 1 || mes[0].File != "bad.ts" {
		t.Fatalf("macro errors %v", mes)
	}
	if entries, _ := logs.Drain(); len(entries) != 1 || entries[0].Message != "loaded" || entries[0].Macro != "ok.ts" {
		t.Fatalf("logs %+v", entries)
	}
	if err := h.RunMacro("ok.ts", Event{}); err == nil {
		t.Fatal("a describe-mode runtime ran a macro")
	}
}

// failingUp is an output sink whose Up does not reach the Arbiter.
type failingUp struct{ scriptOutputRecorder }

func (f *failingUp) Up(o output.Owner, c keys.Chord) error {
	_ = f.scriptOutputRecorder.Up(o, c)
	return errors.New("queue full")
}

// A run's normal end releases with Up; when an Up fails, it releases the
// run's owner, so no key stays down.
func TestMacroEndReleasesOwnerWhenUpFails(t *testing.T) {
	sink := new(failingUp)
	h := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), Output: sink, Owner: output.Owner{Slot: 2, Gen: 1}})
	if err := h.ActivatePrograms(ProgramsForTest(t, map[string]string{
		"macros/m.ts": `import { key } from "padmate"; export default function () { key.down("Shift"); }`,
	}, "", "m.ts")); err != nil {
		t.Fatal(err)
	}
	err := h.RunMacro("m.ts", Event{})
	if mes := MacroErrorsOf(err); len(mes) != 1 || !strings.Contains(err.Error(), "queue full") {
		t.Fatalf("error %v, want the macro's Up error", err)
	}
	var kinds []string
	for _, call := range sink.calls {
		kinds = append(kinds, call.kind)
		if call.owner.Cycle == 0 || call.owner.Slot != 2 {
			t.Errorf("call %+v is not the run's owner", call)
		}
	}
	if got := strings.Join(kinds, " "); got != "down up release" {
		t.Fatalf("calls %q, want down up release", got)
	}
}
