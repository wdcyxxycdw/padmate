package output_test

import (
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// These tests are the output half of the latency statistics (DMN-15, ADR-0071):
// the Arbiter ends the measurement of an input edge (output.InputMark) at the
// first Send that really went out for the command under way, and tells the
// ArbiterOptions.Latency sink, on T-output.

// sample is one call of the Latency sink.
type sample struct {
	key string
	d   time.Duration
}

// sinkOf returns an ArbiterOptions.Latency that appends to the returned
// slice, and a function to read it (the sink runs on T-output).
func sinkOf() (func(string, time.Duration), func() []sample) {
	var mu sync.Mutex
	var got []sample
	return func(key string, d time.Duration) {
			mu.Lock()
			got = append(got, sample{key, d})
			mu.Unlock()
		}, func() []sample {
			mu.Lock()
			defer mu.Unlock()
			return append([]sample(nil), got...)
		}
}

// sender is a Sender that injects as many events as accept says (all if
// negative), with err.
type sender struct {
	accept int
	err    error
	calls  int
}

func (s *sender) Send(events []output.KeyEvent) (int, error) {
	s.calls++
	if s.accept >= 0 {
		return min(s.accept, len(events)), s.err
	}
	return len(events), s.err
}

// TestInputMarkEndsAtTheSend: the measurement of the mark ends at the first
// real Send of the command: the sink gets the mark's key and the time from the
// stamp to the return of Send, and the mark is spent, so that a second
// command of the same edge adds no sample.
func TestInputMarkEndsAtTheSend(t *testing.T) {
	sink, got := sinkOf()
	s := &sender{accept: -1}
	a := output.NewArbiter(clock.NewFake(), s, output.ArbiterOptions{Latency: sink})
	m := &output.InputMark{Stamp: latency.Now(), Key: "pad-1"}
	output.SetInputMark(a, m)
	// Wait on the counter that the sample is measured with: time.Sleep is timed
	// by the runtime's clock, which moves in 0.5-2 ms steps on Windows, so the
	// interval on the counter may come out shorter than the sleep.
	for latency.Now().Sub(m.Stamp) < 2*time.Millisecond {
		runtime.Gosched()
	}
	c := keys.MustParse("F13")
	if err := a.Down(ownerA, c); err != nil {
		t.Fatal(err)
	}
	samples := got()
	if len(samples) != 1 || samples[0].key != "pad-1" {
		t.Fatalf("samples %v, want one of pad-1", samples)
	}
	if d := samples[0].d; d < 2*time.Millisecond || d > time.Minute {
		t.Errorf("sample %v, want the 2ms and more since the stamp", d)
	}
	if m.Stamp != 0 {
		t.Errorf("the mark is not spent: Stamp %d", m.Stamp)
	}
	// The second command of the edge: its Send is not measured again.
	if err := a.Up(ownerA, c); err != nil {
		t.Fatal(err)
	}
	if s.calls != 2 || len(got()) != 1 {
		t.Errorf("%d Send calls and %d samples, want 2 and 1", s.calls, len(got()))
	}
}

// TestInputMarkNotMeasured: no sample comes from a command that sent nothing
// (a key already down, a closed gate), from the local dry run (no Sender call
// at all), from a Send that injected fewer events than it was given, or from a
// mark without a stamp. A Send that only warns (all events injected) counts.
// The mark of a Send that failed is spent, but not the one of a command that
// sent nothing: the edge's next command still can be measured.
func TestInputMarkNotMeasured(t *testing.T) {
	c := keys.MustParse("F13")
	t.Run("already down", func(t *testing.T) {
		sink, got := sinkOf()
		a := output.NewArbiter(clock.NewFake(), &sender{accept: -1}, output.ArbiterOptions{Latency: sink})
		if err := a.Down(ownerA, c); err != nil {
			t.Fatal(err)
		}
		m := &output.InputMark{Stamp: latency.Now(), Key: "pad"}
		output.SetInputMark(a, m)
		_ = a.Down(ownerA, c) // a duplicate: no Send
		if len(got()) != 0 || m.Stamp == 0 {
			t.Errorf("samples %v, stamp %d: want none and the mark kept", got(), m.Stamp)
		}
		output.SetInputMark(a, nil)
		_ = a.ReleaseAll()
	})
	t.Run("gate closed", func(t *testing.T) {
		sink, got := sinkOf()
		s := &sender{accept: -1}
		a := output.NewArbiter(clock.NewFake(), s, output.ArbiterOptions{Latency: sink})
		a.SetBlocked(true)
		output.SetInputMark(a, &output.InputMark{Stamp: latency.Now(), Key: "pad"})
		_ = a.Down(ownerA, c)
		if len(got()) != 0 || s.calls != 0 {
			t.Errorf("samples %v, %d Send calls, want none", got(), s.calls)
		}
	})
	t.Run("local dry run", func(t *testing.T) {
		sink, got := sinkOf()
		s := &sender{accept: -1}
		a := output.NewArbiter(clock.NewFake(), s, output.ArbiterOptions{Latency: sink})
		if err := a.SetLocalDryRun(true); err != nil {
			t.Fatal(err)
		}
		m := &output.InputMark{Stamp: latency.Now(), Key: "pad"}
		output.SetInputMark(a, m)
		if err := a.Down(ownerA, c); err != nil {
			t.Fatal(err)
		}
		if len(got()) != 0 || s.calls != 0 || m.Stamp == 0 {
			t.Errorf("samples %v, %d Send calls, stamp %d: want none, no call and the mark kept", got(), s.calls, m.Stamp)
		}
		output.SetInputMark(a, nil)
		_ = a.ReleaseAll()
	})
	t.Run("injected fewer", func(t *testing.T) {
		sink, got := sinkOf()
		s := &sender{accept: 0, err: errors.New("blocked")}
		a := output.NewArbiter(clock.NewFake(), s, output.ArbiterOptions{Latency: sink})
		m := &output.InputMark{Stamp: latency.Now(), Key: "pad"}
		output.SetInputMark(a, m)
		_ = a.Down(ownerA, c)
		if s.calls == 0 || len(got()) != 0 || m.Stamp != 0 {
			t.Errorf("%d calls, samples %v, stamp %d: want a call, no sample and a spent mark", s.calls, got(), m.Stamp)
		}
	})
	t.Run("warning", func(t *testing.T) {
		sink, got := sinkOf()
		a := output.NewArbiter(clock.NewFake(), &sender{accept: -1, err: errors.New("warning")}, output.ArbiterOptions{Latency: sink})
		output.SetInputMark(a, &output.InputMark{Stamp: latency.Now(), Key: "pad"})
		_ = a.Down(ownerA, c)
		if len(got()) != 1 {
			t.Errorf("samples %v, want one for a Send that injected all", got())
		}
		_ = a.ReleaseAll()
	})
	t.Run("no stamp, no mark, no sink", func(t *testing.T) {
		sink, got := sinkOf()
		a := output.NewArbiter(clock.NewFake(), &sender{accept: -1}, output.ArbiterOptions{Latency: sink})
		output.SetInputMark(a, &output.InputMark{Key: "pad"})
		_ = a.Down(ownerA, c)
		_ = a.Up(ownerA, c)
		output.SetInputMark(a, nil)
		_ = a.Down(ownerA, c)
		_ = a.Up(ownerA, c)
		if len(got()) != 0 {
			t.Errorf("samples %v, want none", got())
		}
		// Without a sink the mark is still spent and nothing breaks.
		plain := output.NewArbiter(clock.NewFake(), &sender{accept: -1}, output.ArbiterOptions{})
		m := &output.InputMark{Stamp: latency.Now(), Key: "pad"}
		output.SetInputMark(plain, m)
		_ = plain.Down(ownerA, c)
		_ = plain.Up(ownerA, c)
		if m.Stamp != 0 {
			t.Errorf("the mark of an Arbiter without a sink is not spent: %d", m.Stamp)
		}
	})
}

// TestInputMarkNoAllocs (DMN-15): measuring adds no allocation to the hot path
// of T-output: a key down and up with a mark on each, and the sink, allocates
// exactly as many times as the same without marks.
func TestInputMarkNoAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector may allocate; the other CI jobs run this")
	}
	rec := latency.NewRecorder()
	c := keys.MustParse("F13")
	measure := func(marked bool) float64 {
		a := output.NewArbiter(clock.NewFake(), nopSender{}, output.ArbiterOptions{Latency: rec.Record})
		m := &output.InputMark{Key: "DeviceKey-1"}
		_ = a.Down(ownerA, c) // warm up
		_ = a.Up(ownerA, c)
		return testing.AllocsPerRun(200, func() {
			if marked {
				m.Stamp = latency.Now()
				output.SetInputMark(a, m)
			}
			_ = a.Down(ownerA, c)
			if marked {
				m.Stamp = latency.Now()
			}
			_ = a.Up(ownerA, c)
			output.SetInputMark(a, nil)
		})
	}
	plain, marked := measure(false), measure(true)
	if marked != plain {
		t.Errorf("%v allocations per down and up with marks, %v without", marked, plain)
	}
	// AllocsPerRun makes one more run than it measures, and each run ends two
	// measurements, of the key down and of the key up.
	if s := rec.Summary("DeviceKey-1"); s.N != 2*(200+1) {
		t.Errorf("the recorder has %d samples, want %d", s.N, 2*(200+1))
	}
}

// TestRemoteInputMark: through a Loop, the RemoteArbiter hands the mark of the
// slot's input edge to T-output with each command: the first command that
// sends ends the measurement, the next ones of the edge, MarkInput(0, "") and
// the commands that are not the edge's own (pointer moves, StopRepeat,
// releases of owners) do not.
func TestRemoteInputMark(t *testing.T) {
	sink, got := sinkOf()
	lt := startLoop(t, output.LoopOptions{Arbiter: output.ArbiterOptions{Latency: sink}})
	r := lt.l.Remote()
	f13, f14 := keys.MustParse("F13"), keys.MustParse("F14")

	// Down is measured once; Tap of the same edge is not.
	r.MarkInput(latency.Now(), "pad-A")
	if err := r.Down(ownerA, f13); err != nil {
		t.Fatal(err)
	}
	if err := r.Tap(ownerA, f14, 0); err != nil {
		t.Fatal(err)
	}
	if s := got(); len(s) != 1 || s[0].key != "pad-A" || s[0].d < 0 || s[0].d > time.Minute {
		t.Fatalf("samples %v, want one of pad-A", s)
	}

	// The release is the next edge's.
	r.MarkInput(latency.Now(), "pad-B")
	if err := r.Up(ownerA, f13); err != nil {
		t.Fatal(err)
	}
	if s := got(); len(s) != 2 || s[1].key != "pad-B" {
		t.Fatalf("samples %v, want the release's of pad-B", s)
	}

	// Cleared, or never marked: no sample. Pointer commands never consume.
	r.MarkInput(0, "")
	_ = r.Down(ownerA, f13)
	_ = r.Up(ownerA, f13)
	r.MarkInput(latency.Now(), "pad-C")
	if err := r.Move(ownerA, 3, 3); err != nil {
		t.Fatal(err)
	}
	if err := r.Jump(ownerA, 10, 10); err != nil {
		t.Fatal(err)
	}
	_ = r.StopRepeat(ownerA, f13)
	_ = r.ReleaseOwners(func(output.Owner) bool { return true })
	if s := got(); len(s) != 2 {
		t.Errorf("samples %v, want only the two of the keys", s)
	}
	// The mark is still waiting for a command that sends: spend it.
	if err := r.Tap(ownerA, f14, 0); err != nil {
		t.Fatal(err)
	}
	if s := got(); len(s) != 3 || s[2].key != "pad-C" {
		t.Errorf("samples %v, want a third of pad-C", s)
	}
	r.MarkInput(0, "")
}

// TestRemoteInputMarkOtherCommands: the commands of the edge that press keys
// carry the mark, whichever they are.
func TestRemoteInputMarkOtherCommands(t *testing.T) {
	for name, f := range map[string]func(*output.RemoteArbiter) error{
		"DownFor": func(r *output.RemoteArbiter) error { return r.DownFor(ownerA, keys.MustParse("F13"), 0) },
		"Tap":     func(r *output.RemoteArbiter) error { return r.Tap(ownerA, keys.MustParse("F13"), 0) },
		"Sequence": func(r *output.RemoteArbiter) error {
			return r.TapSequence(ownerA, []keys.Chord{keys.MustParse("F13")}, 0, 0)
		},
		"Steps": func(r *output.RemoteArbiter) error {
			return r.Sequence(ownerA, []keys.Step{keys.KeysStep(keys.MustParse("F13"))}, 0, 0)
		},
		"Text": func(r *output.RemoteArbiter) error { return r.TypeText(ownerA, "a", 0) },
	} {
		t.Run(name, func(t *testing.T) {
			sink, got := sinkOf()
			lt := startLoop(t, output.LoopOptions{Arbiter: output.ArbiterOptions{Latency: sink}})
			r := lt.l.Remote()
			r.MarkInput(latency.Now(), "pad")
			if err := f(r); err != nil {
				t.Fatal(err)
			}
			r.MarkInput(0, "")
			if s := got(); len(s) != 1 {
				t.Errorf("samples %v, want one", s)
			}
		})
	}
}

// TestRemoteInputMarkLaterSteps (DMN-15, ADR-0071): of a sequence, only the first
// step is the input edge's; the steps behind it go out at the Arbiter's
// deadlines, from the Loop, where no mark is.
func TestRemoteInputMarkLaterSteps(t *testing.T) {
	sink, got := sinkOf()
	lt := startLoop(t, output.LoopOptions{Arbiter: output.ArbiterOptions{Latency: sink}})
	r := lt.l.Remote()
	steps := []keys.Step{
		keys.KeysStep(keys.MustParse("F13")), keys.WaitStep(30 * time.Millisecond), keys.KeysStep(keys.MustParse("F14")),
	}
	r.MarkInput(latency.Now(), "pad")
	if err := r.Sequence(ownerA, steps, 0, 0); err != nil {
		t.Fatal(err)
	}
	r.MarkInput(0, "")
	if s := got(); len(s) != 1 {
		t.Fatalf("samples %v after the first step, want one", s)
	}
	for range 10 {
		lt.clk.Advance(10 * time.Millisecond)
		lt.sync()
	}
	calls := 0
	lt.rec.With(func(rec *simtest.Recorder) { calls = rec.Calls() })
	if calls != 2 { // a tap without a hold is one Send
		t.Fatalf("%d Send calls, want the two steps'", calls)
	}
	if s := got(); len(s) != 1 {
		t.Errorf("samples %v, want only the first step's", s)
	}
}
