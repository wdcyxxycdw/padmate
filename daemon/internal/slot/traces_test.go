package slot_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// tracesDir holds the recorded traces (ENG-08): testdata/traces of the
// daemon module, one directory per device.
var tracesDir = filepath.Join("..", "..", "testdata", "traces")

// TestTraces replays every testdata/traces/**/*.jsonl, as "padmatectl
// record" writes them, through a slot with examples/profiles/basic.json:
// each must read with input.ReadEvents and replay without a slot error,
// and nothing may be held after the exit path, ReleaseAll. So each trace
// added there is a regression test, but only a smoke test: it does not
// check the decisions or the keys sent, which the goldens of this package
// and of cmd/padmated do.
func TestTraces(t *testing.T) {
	var paths []string
	err := filepath.WalkDir(tracesDir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".jsonl") {
			paths = append(paths, path)
		}
		return err
	})
	if err != nil || len(paths) == 0 {
		t.Fatalf("no traces in %s: %v", tracesDir, err)
	}
	prog := compileProfile(t, filepath.Join("..", "..", "..", "examples", "profiles", "basic.json"))
	for _, path := range paths {
		rel, _ := filepath.Rel(tracesDir, path)
		t.Run(filepath.ToSlash(rel), func(t *testing.T) {
			evs := readTrace(t, path)
			sim := simtest.New(t) // asserts that nothing is held at the end
			s, err := slot.New(slot.Config{Gen: 1, Program: prog, Arbiter: sim.Arb, Clock: sim.Clock})
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
			if d, ok := s.NextDeadline(); ok {
				t.Errorf("a deadline at %v is left after ReleaseAll", d)
			}
		})
	}
}

// readTrace reads the trace at path and checks that it is a recording of
// one device that starts with its added event, and rebases it to that
// event, as padmated does.
func readTrace(t *testing.T, path string) []input.Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	evs, err := input.ReadEvents(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) == 0 || evs[0].Kind != input.DeviceAdded || evs[0].Info == nil {
		t.Fatal("the trace does not start with an added event with its info")
	}
	t0, dev := evs[0].Time, evs[0].Device
	for i := range evs {
		if evs[i].Device != dev {
			t.Fatalf("event %d is of device %d, not %d: a trace records one device", i, evs[i].Device, dev)
		}
		evs[i].Time -= t0 // ReadEvents checked that the times of a device do not decrease
	}
	return evs
}
