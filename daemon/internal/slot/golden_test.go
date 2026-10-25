package slot_test

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

var update = flag.Bool("update", false, "rewrite testdata/golden/*/output.trace, and decisions.jsonl and haptics.trace where they exist, from the current output (create an empty one to add it)")

// TestGolden replays each case of testdata/golden (ENG-09): the profile
// (profile.json, or the file profile.path names relative to the case),
// input.trace driven through a Slot and then the exit path, ReleaseAll.
// The key output, through a MenuMask as padmated injects it (OUT-08), must
// match output.trace, the decisions decisions.jsonl when the case has one, and
// the haptics (MAP-25, HUD-10) haptics.trace when it has one: a line for each
// call of Config.OnHaptic, such as "tick@104" for a tick at 104 ms.
//
// A profile with macro actions gets a fake runtime that only runs macros
// (fakeMacros, SCR-50): a run of "F13.ts" presses F13 for 100 ms.
func TestGolden(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("testdata", "golden", "*"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no golden cases: %v", err)
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) { runGolden(t, dir, *update) })
	}
}

// TestGoldenUpdate checks that -update creates the output.trace of a new
// case, which then passes, and fills a decisions.jsonl with a comment,
// which then passes too, and that without it a missing file says how.
func TestGoldenUpdate(t *testing.T) {
	dir := t.TempDir()
	const prof = `{"schemaVersion": 0, "name": "t", "layers": {"base": {"east": {"press": {"type": "keys", "keys": "Space", "mode": "hold"}}}}}`
	for name, text := range map[string]string{"profile.json": prof, "input.trace": "+east@0 -east@300\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "output.trace")
	if _, err := simtest.ReadGolden(out, false, goldenCmd); err == nil || !strings.Contains(err.Error(), "-update") {
		t.Errorf("ReadGolden of a missing file = %v; want an error that mentions -update", err)
	}
	runGolden(t, dir, true)
	if got := readFile(t, out); got != "+Space@0\n-Space@300\n" {
		t.Errorf("output.trace = %q, want the hold", got)
	}
	runGolden(t, dir, false)
	// An empty decisions.jsonl adds the decisions; -update keeps its
	// comment, which the comparison skips.
	const comment = "# The decisions of the hold.\n"
	decs := filepath.Join(dir, "decisions.jsonl")
	if err := os.WriteFile(decs, []byte(comment), 0o644); err != nil {
		t.Fatal(err)
	}
	runGolden(t, dir, true)
	if got := readFile(t, decs); !strings.HasPrefix(got, comment) || strings.Count(got, "\n") < 3 {
		t.Errorf("decisions.jsonl = %q, want the comment, then the decisions", got)
	}
	runGolden(t, dir, false)
}

func runGolden(t *testing.T, dir string, update bool) {
	prog, macros := loadProfile(t, dir)
	evs, err := parseInput(readFile(t, filepath.Join(dir, "input.trace")))
	if err != nil {
		t.Fatalf("input.trace: %v", err)
	}
	timing := prog.Timing()
	sim := simtest.New(t, simtest.WithMenuMask(), simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: time.Duration(timing.TapPressMs) * time.Millisecond, MinGap: time.Duration(timing.TapGapMs) * time.Millisecond}))
	var decs, haptics []string
	var script slot.ScriptRuntime
	if macros {
		script = newFakeMacros(t, sim.Clock, sim.Arb)
	}
	s, err := slot.New(slot.Config{
		Gen: 1, Program: prog, Arbiter: sim.Arb, Clock: sim.Clock, Script: script,
		Trace: func(d engine.Decision) { decs = append(decs, d.String()) },
		OnHaptic: func(_ input.DeviceID, h engine.Haptic) {
			haptics = append(haptics, fmt.Sprintf("%s@%d", h.Preset, time.Duration(sim.Clock.Now())/time.Millisecond))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Replay(sim.Clock, evs); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if err := s.ReleaseAll(); err != nil {
		t.Fatalf("ReleaseAll: %v", err)
	}
	if errs := s.Errors(); len(errs) > 0 {
		t.Fatalf("slot errors:\n%v", errors.Join(errs...))
	}
	checkOutput(t, filepath.Join(dir, "output.trace"), sim.Out.Edges(), update)
	if path := filepath.Join(dir, "decisions.jsonl"); fileExists(t, path) {
		checkDecisions(t, path, decs, update)
	}
	if path := filepath.Join(dir, "haptics.trace"); fileExists(t, path) {
		checkLines(t, path, "haptics", haptics, update)
	}
}

// loadProfile parses and compiles the profile of the case in dir, and
// reports whether it runs macros. Warnings are fine.
func loadProfile(t *testing.T, dir string) (*engine.Program, bool) {
	t.Helper()
	path := filepath.Join(dir, "profile.json")
	if !fileExists(t, path) {
		rel := strings.TrimSpace(readFile(t, filepath.Join(dir, "profile.path")))
		path = filepath.Join(dir, filepath.FromSlash(rel))
	}
	p, _ := profile.Parse([]byte(readFile(t, path)))
	return compileProfile(t, path), p != nil && len(p.MacroFiles()) > 0
}

// compileProfile parses and compiles the profile at path. Warnings are
// fine.
func compileProfile(t *testing.T, path string) *engine.Program {
	t.Helper()
	p, ds := profile.Parse([]byte(readFile(t, path)))
	if p == nil || ds.HasErrors() {
		t.Fatalf("%s: %v", path, ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return prog
}

// parseInput converts a trace of input edges into input events, as input
// would send them: "+south@0" is a ButtonDown of south, and
// "lefttrigger=0.35@300" an AxisMotion with the raw value
// round(0.35 × 32767). Buttons may be aliases and come out canonical;
// trigger values must be in [0, 1], stick values in [-1, 1].
func parseInput(s string) ([]input.Event, error) {
	edges, err := simtest.ParseTrace(s)
	if err != nil {
		return nil, err
	}
	evs := make([]input.Event, 0, len(edges))
	for _, e := range edges {
		ev := input.Event{Time: uint64(e.T)}
		switch e.Kind {
		case simtest.Down, simtest.Up:
			c, ok := control.Parse(e.Name)
			if !ok || c.IsTrigger() {
				return nil, fmt.Errorf("%v: not a button; the triggers are axes, such as lefttrigger=0.5", e)
			}
			ev.Kind, ev.Control = input.ButtonUp, c.String()
			if e.Kind == simtest.Down {
				ev.Kind = input.ButtonDown
			}
		case simtest.Value:
			a, ok := control.ParseAxis(e.Name)
			if !ok {
				return nil, fmt.Errorf("%v: not an axis", e)
			}
			lo := -1.0
			if a == control.LeftTrigger || a == control.RightTrigger {
				lo = 0
			}
			if !(e.Value >= lo && e.Value <= 1) {
				return nil, fmt.Errorf("%v: the value of %s must be in [%v, 1]", e, a, lo)
			}
			raw := math.Round(e.Value * math.MaxInt16)
			ev.Kind, ev.Control, ev.Value = input.AxisMotion, a.String(), int16(max(min(raw, math.MaxInt16), math.MinInt16))
		}
		evs = append(evs, ev)
	}
	return evs, nil
}

// goldenCmd is the command that -update goes with.
const goldenCmd = "go test ./internal/slot -run TestGolden"

// checkOutput compares the output edges with the trace in path, or
// rewrites it with update, keeping its leading comment lines. update also
// creates a missing path, for a new case.
func checkOutput(t *testing.T, path string, got []simtest.Edge, update bool) {
	t.Helper()
	old, err := simtest.ReadGolden(path, update, goldenCmd)
	if err != nil {
		t.Fatal(err)
	}
	if update {
		simtest.RewriteGolden(t, path, old, simtest.FormatTraceLines(got))
		return
	}
	want, err := simtest.ParseTrace(old)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("output differs from %s (rerun with -update -v and review the diff):\n%s",
			path, simtest.LineDiff(simtest.FormatTraceLines(want), simtest.FormatTraceLines(got)))
	}
}

// checkDecisions compares the decisions, one JSON object each, with the
// lines of path, or rewrites it with -update.
func checkDecisions(t *testing.T, path string, got []string, update bool) {
	t.Helper()
	checkLines(t, path, "decisions", got, update)
}

// checkLines compares got, the lines of what, with those of path after its
// leading comment lines, or rewrites it with -update.
func checkLines(t *testing.T, path, what string, got []string, update bool) {
	t.Helper()
	old, err := simtest.ReadGolden(path, false, goldenCmd)
	if err != nil {
		t.Fatal(err)
	}
	text := ""
	if len(got) > 0 {
		text = strings.Join(got, "\n") + "\n"
	}
	if update {
		simtest.RewriteGolden(t, path, old, text)
		return
	}
	// Leading comment lines, which -update keeps, are not decisions.
	if want := strings.TrimPrefix(old, simtest.GoldenComments(old)); text != want {
		t.Errorf("decisions differ from %s (rerun with -update -v and review the diff):\n%s", path, simtest.LineDiff(want, text))
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func fileExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}
