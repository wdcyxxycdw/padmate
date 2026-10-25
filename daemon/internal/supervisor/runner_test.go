package supervisor

import (
	"log/slog"
	"slices"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
)

// TestDisabledReload: a disabled slot records the program of each Reload
// that comes before the enable, whose barrier then finishes, Status
// showing the generation; the barriers that come after the enable are the
// fresh slot's, which starts with the program recorded and switches in
// order with its edges.
func TestDisabledReload(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	s := &Supervisor{status: make([]SlotStatus, 1), notes: make(chan uint8, 1), stop: make(chan struct{}), log: log}
	r := &runner{s: s, base: log, log: log, prog: &engine.Program{}, gen: firstGen, wake: make(chan struct{}, 1)}
	progs := map[uint32]*engine.Program{2: {}, 3: {}, 4: {}}
	reload := func(gen uint32) *barrier {
		b := newBarrier()
		b.prog, b.gen = progs[gen], gen
		return b
	}
	queued, sync1, before, sync2, after, sync3 := reload(2), newBarrier(), reload(3), newBarrier(), reload(4), newBarrier()
	// All in one batch: the runner splits it at the enable.
	for _, m := range []message{{b: sync2}, {b: before}, {enable: true}, {b: after}, {b: sync3}} {
		r.post(m)
	}
	rest, ok := r.disabled([]*barrier{queued, sync1})
	if !ok {
		t.Fatal("disabled returned false")
	}
	for _, b := range []*barrier{queued, sync1, before, sync2} {
		if !b.finished() {
			t.Errorf("a barrier before the enable is not finished")
		}
	}
	if !slices.Equal(rest, []*barrier{after, sync3}) || after.finished() || sync3.finished() {
		t.Errorf("returned %v, want the unfinished barriers after the enable", rest)
	}
	if r.gen != 3 || r.prog != progs[3] {
		t.Errorf("recorded generation %d, want 3 and its program", r.gen)
	}
	if st := s.status[0]; st.Gen != 3 || st.Disabled {
		t.Errorf("Status %+v, want generation 3, enabled", st)
	}
	if n := <-s.notes; n != 0 {
		t.Errorf("note of slot %d, want 0", n)
	}
}
