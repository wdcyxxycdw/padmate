package simtest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	runtimeScript "github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

// Script is a script.Host connected to a Sim's Arbiter and fake clock.
//
// Script owns no goroutines. Dispatch runs on the caller's goroutine, and
// Sim.Advance/AdvanceTo runs the host event loop at every script deadline.
// The Sim cleanup disposes every loaded Script, so a test that leaves a key
// down still gets the same AssertNoneHeld check as a declarative test.
type Script struct {
	*runtimeScript.Host
	sim   *Sim
	owner output.Owner
}

// LoadScript compiles and activates a TypeScript source string in a fresh
// runtime connected to s. It is a testing helper: build or activation errors
// fail t. The source is written to a temporary script root so imports are
// checked by the same compile.Build path as a profile script.
func LoadScript(t testing.TB, s *Sim, source string) *Script {
	t.Helper()
	if s == nil {
		t.Fatal("simtest: LoadScript: nil Sim")
	}
	root := t.TempDir()
	entry := filepath.Join(root, "main.ts")
	if err := os.WriteFile(entry, []byte(source), 0o600); err != nil {
		t.Fatalf("simtest: write script: %v", err)
	}
	bundle, err := compile.Build(compile.BuildOptions{Root: root, Entry: "main.ts", API: "0.8"})
	if err != nil {
		t.Fatalf("simtest: compile script: %v", err)
	}
	program, err := goja.Compile("main.js", string(bundle.JS), false)
	if err != nil {
		t.Fatalf("simtest: parse script bundle: %v", err)
	}
	s.cycle++
	owner := output.Owner{Slot: 0, Gen: 1, Source: output.SourceScript, Cycle: s.cycle}
	h := runtimeScript.NewHostWithOptions(runtimeScript.HostOptions{
		Clock:  s.Clock,
		Output: s.Arb,
		Owner:  owner,
		Mode:   "test",
	})
	if err := h.Activate(program); err != nil {
		_ = h.Dispose()
		t.Fatalf("simtest: activate script: %v", err)
	}
	if err := h.OnActivate(); err != nil {
		_ = h.Dispose()
		t.Fatalf("simtest: onActivate script: %v", err)
	}
	r := &Script{Host: h, sim: s, owner: owner}
	s.scripts = append(s.scripts, r)
	return r
}

// LoadScriptFile compiles and activates an existing script tree. It is useful
// for tests that need sibling imports or fixture files; LoadScript is the
// convenient inline-source form.
func LoadScriptFile(t testing.TB, s *Sim, opts compile.BuildOptions) *Script {
	t.Helper()
	if s == nil {
		t.Fatal("simtest: LoadScriptFile: nil Sim")
	}
	if opts.API == "" {
		opts.API = "0.8"
	}
	bundle, err := compile.Build(opts)
	if err != nil {
		t.Fatalf("simtest: compile script: %v", err)
	}
	return loadBundle(t, s, bundle.JS)
}

func loadBundle(t testing.TB, s *Sim, js []byte) *Script {
	t.Helper()
	program, err := goja.Compile("main.js", string(js), false)
	if err != nil {
		t.Fatalf("simtest: parse script bundle: %v", err)
	}
	s.cycle++
	owner := output.Owner{Slot: 0, Gen: 1, Source: output.SourceScript, Cycle: s.cycle}
	h := runtimeScript.NewHostWithOptions(runtimeScript.HostOptions{
		Clock:  s.Clock,
		Output: s.Arb,
		Owner:  owner,
		Mode:   "test",
	})
	if err := h.Activate(program); err != nil {
		_ = h.Dispose()
		t.Fatalf("simtest: activate script: %v", err)
	}
	if err := h.OnActivate(); err != nil {
		_ = h.Dispose()
		t.Fatalf("simtest: onActivate script: %v", err)
	}
	r := &Script{Host: h, sim: s, owner: owner}
	s.scripts = append(s.scripts, r)
	return r
}

// LoadScript is also available as a Sim method for tests that prefer to keep
// the harness as the receiver.
func (s *Sim) LoadScript(t testing.TB, source string) *Script {
	return LoadScript(t, s, source)
}

// Owner returns the runtime owner used for this script's output.
func (r *Script) Owner() output.Owner { return r.owner }

// NextDeadline reports the next timer or queued output completion.
func (r *Script) NextDeadline() (clock.Time, bool) {
	if r == nil || r.Host == nil || r.State() == runtimeScript.StateDisposed {
		return 0, false
	}
	return r.Host.NextDeadline()
}

// RunDue runs this script's timers and microtasks at the Sim's current time.
func (r *Script) RunDue() error {
	if r == nil || r.Host == nil || r.State() == runtimeScript.StateDisposed {
		return nil
	}
	return r.Host.RunDue()
}

// Dispatch sends one input event to the script and drains its microtasks.
func (r *Script) Dispatch(e runtimeScript.Event) error {
	if r == nil || r.Host == nil {
		return errors.New("simtest: nil script")
	}
	if e.Time == 0 {
		e.Time = r.sim.Clock.Now()
	}
	return r.Host.Dispatch(e)
}

// Input converts an input trace edge into a press or release event. If the
// edge is in the future, the Sim is advanced to its timestamp first.
func (r *Script) Input(e Edge) error {
	if r == nil || r.sim == nil {
		return errors.New("simtest: nil script")
	}
	if e.Kind != Down && e.Kind != Up {
		return fmt.Errorf("simtest: script input %q is a value edge", e.Name)
	}
	if e.T < r.sim.Clock.Now() {
		return fmt.Errorf("simtest: script input time %v is before clock %v", e.T, r.sim.Clock.Now())
	}
	r.sim.AdvanceTo(e.T)
	gesture := "press"
	if e.Kind == Up {
		gesture = "release"
	}
	return r.Dispatch(runtimeScript.Event{Control: e.Name, Gesture: gesture, Time: e.T})
}

// Replay parses and sends a trace of button edges to the script. Axis/value
// edges are rejected because the M5 Host has no axis event API yet.
func (r *Script) Replay(trace string) error {
	edges, err := ParseTrace(trace)
	if err != nil {
		return err
	}
	for _, e := range edges {
		if err := r.Input(e); err != nil {
			return err
		}
	}
	return nil
}

// Dispose tears down the runtime and releases all output owned by it.
func (r *Script) Dispose() error {
	if r == nil || r.Host == nil {
		return nil
	}
	return r.Host.Dispose()
}
