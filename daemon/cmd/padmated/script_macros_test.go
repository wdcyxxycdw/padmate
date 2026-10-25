package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// writeMacros writes files into the macros folder of dir.
func writeMacros(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, profile.MacroDir), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, profile.MacroDir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// macroFixture is a slot whose profile only runs macros, on a fake clock,
// with the runtime padmated gives it.
type macroFixture struct {
	dir      string
	s        *slot.Slot
	runtime  *scriptRuntime
	clock    *clock.Fake
	arb      *output.Arbiter
	recorder *simtest.Recorder
	builds   *atomic.Int64 // macro builds started
	gate     *buildGate    // holds the macro builds
}

// buildGate holds the macro builds of a macroFixture until it opens; hold
// closes it again for the next builds.
type buildGate struct {
	mu sync.Mutex
	ch chan struct{}
}

func newBuildGate() *buildGate { return &buildGate{ch: make(chan struct{})} }

func (g *buildGate) wait(ctx context.Context) {
	g.mu.Lock()
	ch := g.ch
	g.mu.Unlock()
	select {
	case <-ch:
	case <-ctx.Done():
	}
}

func (g *buildGate) open() {
	g.mu.Lock()
	defer g.mu.Unlock()
	select {
	case <-g.ch:
	default:
		close(g.ch)
	}
}

func (g *buildGate) hold() {
	g.mu.Lock()
	defer g.mu.Unlock()
	select {
	case <-g.ch:
		g.ch = make(chan struct{})
	default:
	}
}

// newMacroFixture starts a slot of a profile with the base layer bindings
// and the macro files. Its runtime starts cold, as after the first Resync:
// every build waits for open.
func newMacroFixture(t *testing.T, bindings string, files map[string]string) macroFixture {
	t.Helper()
	dir := t.TempDir()
	writeMacros(t, dir, files)
	p, ds := profile.Parse([]byte(`{"schemaVersion":0,"name":"macros","layers":{"base":` + bindings + `}}`))
	if ds.HasErrors() {
		t.Fatal(ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake()
	rec := simtest.NewRecorder(clk)
	arb := output.NewArbiter(clk, rec, output.ArbiterOptions{})
	def := newProfileScriptDefinition(filepath.Join(dir, "profile.json"), p, false)
	r, err := newScriptRuntime(def, 0, 1, clk, arb, true)
	if err != nil {
		t.Fatal(err)
	}
	gate := newBuildGate()
	builds := new(atomic.Int64)
	r.macroBuild = func(ctx context.Context, root string, files []string, api string) []script.MacroProgram {
		builds.Add(1)
		gate.wait(ctx)
		return buildMacroPrograms(ctx, root, files, api)
	}
	s, err := slot.New(slot.Config{Index: 0, Gen: 1, Program: prog, Arbiter: arb, Clock: clk, Script: r})
	if err != nil {
		t.Fatal(err)
	}
	s.Handle(input.Event{Kind: input.DeviceAdded, Device: 1, Info: &input.DeviceInfo{Name: "macro fixture", IsGamepad: true}})
	f := macroFixture{dir: dir, s: s, runtime: r, clock: clk, arb: arb, recorder: rec, builds: builds, gate: gate}
	t.Cleanup(func() {
		f.gate.open()
		_ = s.ReleaseAll()
		f.settle(t)
		_ = s.ReleaseAll()
		_ = s.Dispose()
		simtest.AssertNoneHeld(t, arb, rec)
	})
	// The first Resync: a runtime only needed for macros starts at once.
	if err := s.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	if r.paused || r.pending == nil {
		t.Fatalf("the runtime did not start building: paused=%v pending=%v", r.paused, r.pending != nil)
	}
	return f
}

// settle advances the clock until the runtime has no build under way.
func (f macroFixture) settle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for f.runtime.pending != nil && time.Now().Before(deadline) {
		f.clock.Advance(time.Millisecond)
		f.s.Advance()
		time.Sleep(time.Millisecond)
	}
	f.s.Advance()
	if f.runtime.pending != nil {
		t.Fatal("the runtime did not finish building")
	}
}

// ready lets the build finish and waits for it.
func (f macroFixture) ready(t *testing.T) {
	t.Helper()
	f.gate.open()
	f.settle(t)
}

// run advances the clock by d in steps of a millisecond.
func (f macroFixture) run(d time.Duration) {
	for end := f.clock.Now().Add(d); f.clock.Now() < end; {
		f.clock.Advance(time.Millisecond)
		f.s.Advance()
	}
}

func (f macroFixture) press(control string) {
	f.s.Handle(input.Event{Kind: input.ButtonDown, Control: control})
	f.s.Handle(input.Event{Kind: input.ButtonUp, Control: control})
}

// count returns how many times the key name went down.
func (f macroFixture) count(name string) int {
	n := 0
	for _, e := range f.recorder.Edges() {
		if e.Name == name && e.Kind == simtest.Down {
			n++
		}
	}
	return n
}

// ups returns how many times the key name went up.
func (f macroFixture) ups(name string) int {
	n := 0
	for _, e := range f.recorder.Edges() {
		if e.Name == name && e.Kind == simtest.Up {
			n++
		}
	}
	return n
}

// reload gives the slot the profile doc at dir, a new generation: the
// fixture's folder for a reload, another folder for a switch.
func (f macroFixture) reload(t *testing.T, dir, doc string, gen uint32) {
	t.Helper()
	p, ds := profile.Parse([]byte(doc))
	if ds.HasErrors() {
		t.Fatal(ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	f.runtime.def.setProfile(filepath.Join(dir, "profile.json"), p)
	if err := f.s.Reload(prog, gen); err != nil {
		t.Fatal(err)
	}
}

// A reload or a profile switch ends the runs of the installed runtime when
// it starts building the replacement (design §6.3), not when the
// replacement is installed: nothing more is pressed after Slot.Reload and
// the old runs' keys are up. A macro pressed while the replacement builds
// runs on it once it is installed, never on the old profile's runtime,
// which may not have the file (a reload that binds a macro first) or have
// another file of the same name (a switch). When the replacement of a
// switch fails, the old profile's runtime runs nothing for the new one.
func TestMacroReloadEndsRunsAtOnce(t *testing.T) {
	const loop = `import {key, sleep} from "padmate";
export default async function () { key.down("F22"); for (;;) { await key.tap("F21"); await sleep(20); } }`
	const bindings = `{"south":{"press":{"type":"macro","file":"loop.ts"}},"east":{"press":{"type":"macro","file":"new.ts"}}}`
	tap := func(k string) string {
		return `import {key} from "padmate"; export default function () { key.tap("` + k + `"); }`
	}
	for _, tc := range []struct {
		name   string
		other  bool   // switch to another folder
		script string // the new profile's script section
		fails  bool   // the replacement does not build
	}{
		{name: "reload"},
		{name: "switch", other: true},
		{name: "failed switch", other: true, script: `"script":{"entry":"missing.ts","api":"0.21"},`, fails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMacroFixture(t, `{"south":{"press":{"type":"macro","file":"loop.ts"}}}`,
				map[string]string{"loop.ts": loop, "new.ts": tap("F24")})
			f.ready(t)
			f.press("south")
			f.run(100 * time.Millisecond)
			if f.count("F22") != 1 || f.count("F21") < 3 {
				t.Fatalf("the loop did not run: %v", f.recorder.Edges())
			}
			dir := f.dir
			if tc.other {
				dir = t.TempDir()
				writeMacros(t, dir, map[string]string{"loop.ts": tap("F23"), "new.ts": tap("F23")})
			}
			f.gate.hold()
			taps := f.count("F21")
			f.reload(t, dir, `{"schemaVersion":0,"name":"next",`+tc.script+`"layers":{"base":`+bindings+`}}`, 2)
			f.run(300 * time.Millisecond)
			if n := f.count("F21"); n != taps {
				t.Fatalf("the old run pressed F21 %d more times after the reload", n-taps)
			}
			if f.ups("F22") != 1 {
				t.Fatalf("the old run's F22 is not up: %v", f.recorder.Edges())
			}
			if !tc.fails && f.runtime.pending == nil {
				t.Fatal("the replacement is not building")
			}
			// Pressed while the replacement builds, or after it failed.
			f.press("east")
			f.press("south")
			f.run(10 * time.Millisecond)
			if f.count("F21") != taps || f.count("F22") != 1 || f.count("F23")+f.count("F24") != 0 {
				t.Fatalf("a macro ran on the old runtime: %v", f.recorder.Edges())
			}
			f.gate.open()
			f.settle(t)
			f.run(100 * time.Millisecond)
			errs := errors.Join(f.s.Errors()...)
			switch {
			case tc.fails:
				if errs == nil || !strings.Contains(errs.Error(), "missing.ts") {
					t.Fatalf("errors %v, want the replacement's", errs)
				}
				if f.count("F21") != taps || f.count("F23")+f.count("F24") != 0 {
					t.Fatalf("after the failed switch: %v", f.recorder.Edges())
				}
				f.press("south")
				f.run(10 * time.Millisecond)
				if f.count("F21") != taps || f.count("F23") != 0 {
					t.Fatalf("the old profile's runtime ran a macro of the new one: %v", f.recorder.Edges())
				}
			case tc.other:
				if errs != nil {
					t.Fatal(errs)
				}
				// Each pressed once: new.ts and the new folder's loop.ts.
				if f.count("F23") != 2 || f.count("F21") != taps || f.count("F24") != 0 {
					t.Fatalf("after the switch: %v", f.recorder.Edges())
				}
			default:
				if errs != nil {
					t.Fatalf("errors %v; the macro bound by the reload was not found", errs)
				}
				if f.count("F24") != 1 || f.count("F22") != 2 {
					t.Fatalf("after the reload: %v", f.recorder.Edges())
				}
			}
		})
	}
}

// A macro pressed while the runtime builds runs once it is ready, once
// however often it was pressed; the keys of other bindings are not held
// back. A profile with only macros takes no input over: its other
// bindings map as without a script.
func TestMacroRuntimeColdStart(t *testing.T) {
	f := newMacroFixture(t,
		`{"south":{"press":{"type":"macro","file":"复制全部.ts"}},"east":{"press":{"type":"keys","keys":"F13"}}}`,
		map[string]string{"复制全部.ts": `import {key} from "padmate"; export default function(){ key.tap("F21"); }`})
	if !f.runtime.Needed() || f.runtime.Configured() {
		t.Fatal("a profile with a macro is needed, not configured")
	}
	f.press("south")
	f.press("south")
	f.press("east")
	f.run(10 * time.Millisecond)
	if f.count("F13") != 1 || f.count("F21") != 0 {
		t.Fatalf("before the runtime is ready: %v", f.recorder.Edges())
	}
	f.ready(t)
	f.run(100 * time.Millisecond)
	if n := f.count("F21"); n != 1 {
		t.Fatalf("the macro ran %d times, want once: %v", n, f.recorder.Edges())
	}
	// Ready, each press runs it.
	f.press("south")
	f.run(100 * time.Millisecond)
	if n := f.count("F21"); n != 2 {
		t.Fatalf("the macro ran %d times, want twice", n)
	}
}

// A release before the runtime is ready forgets the macros pressed, and
// the runtime restarts at once: no press is needed to start it again.
func TestMacroRuntimeReleaseForgetsQueued(t *testing.T) {
	f := newMacroFixture(t, `{"south":{"press":{"type":"macro","file":"a.ts"}}}`,
		map[string]string{"a.ts": `import {key} from "padmate"; export default function(){ key.tap("F21"); }`})
	f.press("south")
	if err := f.s.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	if f.runtime.paused || f.runtime.pending == nil {
		t.Fatal("the runtime did not restart after the release")
	}
	f.ready(t)
	f.run(100 * time.Millisecond)
	if n := f.count("F21"); n != 0 {
		t.Fatalf("a macro pressed before the release ran %d times", n)
	}
	f.press("south")
	f.run(100 * time.Millisecond)
	if n := f.count("F21"); n != 1 {
		t.Fatalf("the macro ran %d times, want once", n)
	}
}

// script.status lists the macros' run errors by file, not as the legacy
// script's lastError; a missing file is reported once per runtime; a
// macro that loops is disabled; a rebuild clears the errors but not the
// disabled macro, until its file changes.
func TestMacroRuntimeStatus(t *testing.T) {
	f := newMacroFixture(t,
		`{"south":{"press":{"type":"macro","file":"出错 (1).ts"}},"east":{"press":{"type":"macro","file":"不存在.ts"}},"west":{"press":{"type":"macro","file":"loop.ts"}}}`,
		map[string]string{
			"出错 (1).ts": "export default function () {\n  throw new Error(\"boom\");\n}\n",
			"loop.ts":   "export default function () { for (;;) {} }\n",
		})
	f.ready(t)
	f.press("south")
	f.press("east")
	f.press("east")
	errs := f.s.Errors()
	if len(errs) != 2 {
		t.Fatalf("errors %v, want the throw and one missing file", errs)
	}
	v := f.runtime.InspectScript(false)
	if v.LastError != "" || v.Entry != "" || len(v.Macros) != 2 {
		t.Fatalf("inspection %+v", v)
	}
	if m := v.Macros[0]; m.File != "不存在.ts" || !strings.Contains(m.Error, "not found") || m.Disabled {
		t.Errorf("missing macro: %+v", m)
	}
	if m := v.Macros[1]; m.File != "出错 (1).ts" || !strings.Contains(m.Error, "出错 (1).ts:2") || !strings.Contains(m.Error, "boom") {
		t.Errorf("throwing macro: %+v", m)
	}
	f.press("west")
	_ = f.s.Errors()
	v = f.runtime.InspectScript(false)
	var loop script.MacroStatus
	for _, m := range v.Macros {
		if m.File == "loop.ts" {
			loop = m
		}
	}
	if !loop.Disabled || loop.Error == "" {
		t.Fatalf("the looping macro: %+v in %+v", loop, v.Macros)
	}
	if err := f.s.ReloadScript(); err != nil {
		t.Fatal(err)
	}
	f.settle(t)
	v = f.runtime.InspectScript(false)
	if len(v.Macros) != 1 || v.Macros[0].File != "loop.ts" || !v.Macros[0].Disabled || v.Macros[0].Error != "" {
		t.Fatalf("after a rebuild: %+v", v.Macros)
	}
	writeMacros(t, f.dir, map[string]string{"loop.ts": `import {key} from "padmate"; export default function(){ key.tap("F22"); }`})
	if err := f.s.ReloadScript(); err != nil {
		t.Fatal(err)
	}
	f.settle(t)
	if v := f.runtime.InspectScript(false); len(v.Macros) != 0 {
		t.Fatalf("after the file changed: %+v", v.Macros)
	}
	f.press("west")
	f.run(100 * time.Millisecond)
	if n := f.count("F22"); n != 1 {
		t.Fatalf("the changed macro ran %d times, want once", n)
	}
}

// splitMacroErrors keeps the macros' errors apart from the others in any
// join, and an error that wraps a macro's is the macro's.
func TestSplitMacroErrors(t *testing.T) {
	a := &script.MacroError{File: "a.ts", Err: errors.New("a")}
	b := &script.MacroError{File: "b.ts", Err: errors.New("b")}
	other := errors.New("legacy")
	macros, rest := splitMacroErrors(errors.Join(a, errors.Join(other, b), nil))
	if rest == nil || rest.Error() != "legacy" || len(macros) != 2 || macros[0] != a || macros[1] != b {
		t.Fatalf("rest %v, macros %v", rest, macros)
	}
	if macros, rest := splitMacroErrors(errors.Join(a)); rest != nil || len(macros) != 1 {
		t.Fatalf("rest %v, macros %v", rest, macros)
	}
	if macros, rest := splitMacroErrors(other); !errors.Is(rest, other) || rest.Error() != "legacy" || macros != nil {
		t.Fatalf("rest %v, macros %v", rest, macros)
	}
}

// buildMacroPrograms finds each file under exactly its name: another case
// is a missing file. Each failure stays with its file, and the Version is
// the file's sha256.
func TestBuildMacroPrograms(t *testing.T) {
	dir := t.TempDir()
	good := `export default function () {}` + "\n"
	writeMacros(t, dir, map[string]string{"good.ts": good, "bad.ts": "export default function (", "Case.ts": good})
	if err := os.Mkdir(filepath.Join(dir, profile.MacroDir, "dir.ts"), 0o700); err != nil {
		t.Fatal(err)
	}
	ps := buildMacroPrograms(context.Background(), dir, []string{"bad.ts", "case.ts", "dir.ts", "good.ts", "none.ts"}, profile.CurrentScriptAPI)
	if len(ps) != 5 {
		t.Fatal(ps)
	}
	sum := sha256.Sum256([]byte(good))
	if g := ps[3]; g.File != "good.ts" || g.Err != nil || g.Program == nil || g.Version != hex.EncodeToString(sum[:]) {
		t.Errorf("good.ts: %+v", g)
	}
	if b := ps[0]; b.Err == nil || b.Program != nil || errors.Is(b.Err, script.ErrMacroNotFound) || b.Version == "" {
		t.Errorf("bad.ts: %+v", b)
	}
	for _, i := range []int{1, 2, 4} {
		if p := ps[i]; !errors.Is(p.Err, script.ErrMacroNotFound) || p.Program != nil {
			t.Errorf("%s: %+v, want not found", p.File, p)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ps := buildMacroPrograms(ctx, dir, []string{"good.ts"}, profile.CurrentScriptAPI); !errors.Is(ps[0].Err, context.Canceled) {
		t.Errorf("canceled: %+v", ps[0])
	}
}

// macroChecks reports each macro's build or load problem and runs the top
// level of a macro again only once its sources changed.
func TestMacroChecks(t *testing.T) {
	dir := t.TempDir()
	writeMacros(t, dir, map[string]string{
		"good.ts":     `import {log} from "padmate"; log.info("top"); export default function () {}`,
		"build.ts":    "export default function (",
		"throw.ts":    `throw new Error("top level");`,
		"nofn.ts":     `export default 1;`,
		"count.ts":    `globalThis.n = (globalThis.n ?? 0) + 1; export default function () {}`,
		"keys.ts":     `import {key} from "padmate"; key.tap("A"); export default function () {}`,
		"restrict.ts": `import {on} from "padmate"; export default function () { on(); }`,
	})
	var c macroChecks
	files := []string{"build.ts", "count.ts", "good.ts", "keys.ts", "nofn.ts", "restrict.ts", "throw.ts"}
	problems, err := c.check(context.Background(), dir, files, profile.CurrentScriptAPI)
	if err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{
		"build.ts": "build.ts:1", "throw.ts": "top level", "nofn.ts": "must be a function",
		"keys.ts": "is not running", "restrict.ts": `"on" is not available in macros`,
	} {
		if !strings.Contains(problems[file], want) {
			t.Errorf("%s: %q, want %q", file, problems[file], want)
		}
	}
	for _, file := range []string{"good.ts", "count.ts"} {
		if p, ok := problems[file]; !ok || p != "" {
			t.Errorf("%s: %q, %v", file, p, ok)
		}
	}
	// Two of them do not build: their top level does not run.
	if len(c.problems) != len(files) || c.loaded != len(files)-2 {
		t.Fatalf("cached %d, loaded %d, want %d", len(c.problems), c.loaded, len(files)-2)
	}
	again, err := c.check(context.Background(), dir, files, profile.CurrentScriptAPI)
	if err != nil || len(again) != len(files) {
		t.Fatal(again, err)
	}
	for file, p := range problems {
		if again[file] != p {
			t.Errorf("%s: %q, then %q", file, p, again[file])
		}
	}
	writeMacros(t, dir, map[string]string{"build.ts": "export default function () {}"})
	again, err = c.check(context.Background(), dir, files, profile.CurrentScriptAPI)
	// Only the changed macro runs its top level again.
	if err != nil || again["build.ts"] != "" || c.loaded != len(files)-1 {
		t.Fatal(again, err, c.loaded)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	writeMacros(t, dir, map[string]string{"good.ts": "export default function () { }"})
	if _, err := c.check(ctx, dir, []string{"good.ts"}, profile.CurrentScriptAPI); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled check: %v", err)
	}
}
