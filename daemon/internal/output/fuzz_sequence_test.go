package output_test

import (
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// Operations of FuzzSequence, each with the opcode byte and two argument bytes
// a and b.
const (
	fsSequence     = iota // Sequence(owner a, steps b, gap (a/3%4)*5ms, hold (b/8%3)*5ms)
	fsTapSequence         // TapSequence(owner a, two shortcuts)
	fsText                // TypeText(owner a, text b)
	fsAdvance             // Sim.Advance(b%64 ms)
	fsReleaseOwner        // ReleaseOwner(owner a)
	fsReleaseAll          // ReleaseAll
	fsBlock               // SetBlocked(a%2 == 1)
	fsFail                // the next Send call injects nothing
	fsPartial             // the next Send call injects only its first event
	fsDown                // Down(owner a, A)
	fsUp                  // Up(owner a, A)
	fsTap                 // Tap(owner a, A or Enter by b)
	fsEventGap            // SetEventGap((b%4)*5ms)
	fsOps
)

var fuzzSequences = [...][]keys.Step{
	{kstep("A")},
	{kstep("A"), wstep(10 * ms), kstep("B")},
	{kstep("Ctrl+A"), wstep(30 * ms), tstep("hi"), kstep("Enter")},
	{tstep("ab\ncd")},
	{wstep(40 * ms), tstep("😀a")},
	{kstep("A"), kstep("A"), kstep("A")},
	{kstep("A"), wstep(5 * ms), wstep(5 * ms), tstep("\t"), wstep(time.Second)},
	{tstep("x"), tstep("y"), kstep("Shift+F13")},
}

// FuzzSequence runs random sequences of three owners among texts, taps, holds, a
// closing gate, failed and partial Sends, releases and an event gap, and checks
// what ADR-0070 promises: no character is typed while the gate is closed; the queue
// of sequences never goes past MaxQueuedSequences; every call is told its end
// exactly once, however it ended; and, at the end, once everything is released and
// settled, nothing is queued, held, down or due (invariant 1).
func FuzzSequence(f *testing.F) {
	for _, seed := range [][]byte{
		{fsSequence, 0, 1, fsAdvance, 0, 40},
		{fsSequence, 9, 2, fsAdvance, 0, 20, fsReleaseOwner, 0, 0, fsAdvance, 0, 40},
		{fsSequence, 3, 2, fsSequence, 4, 3, fsAdvance, 0, 60, fsReleaseAll, 0, 0},
		{fsSequence, 3, 2, fsBlock, 1, 0, fsAdvance, 0, 40, fsBlock, 0, 0, fsSequence, 3, 2},
		{fsFail, 0, 0, fsSequence, 0, 2, fsAdvance, 0, 40},
		{fsPartial, 0, 0, fsFail, 0, 0, fsSequence, 0, 3, fsAdvance, 0, 40},
		{fsDown, 0, 0, fsSequence, 6, 5, fsUp, 0, 0, fsAdvance, 0, 40},
		{fsEventGap, 0, 1, fsTap, 1, 0, fsSequence, 6, 2, fsAdvance, 0, 60},
		{fsEventGap, 0, 3, fsSequence, 6, 7, fsEventGap, 0, 0, fsAdvance, 0, 60},
		{fsTapSequence, 1, 0, fsSequence, 1, 6, fsText, 2, 1, fsAdvance, 0, 60},
		{fsSequence, 0, 6, fsSequence, 1, 6, fsSequence, 2, 6, fsReleaseOwner, 1, 0, fsAdvance, 0, 60},
		{fsSequence, 0, 2, fsAdvance, 0, 35, fsBlock, 1, 0, fsBlock, 0, 0, fsAdvance, 0, 60},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 3*200 {
			data = data[:3*200]
		}
		s := newSim(t, withRetry(7*ms))
		blocked, fail, partial := false, 0, 0
		pressedAfterBlock := false
		s.Out.Inject = func(events []output.KeyEvent) (int, error) {
			if blocked {
				// A key press the event gap held back goes out after the gate closes,
				// by design (SetBlocked); a character never waits in that queue.
				for _, e := range events {
					if e.Unicode && !e.Up {
						pressedAfterBlock = true
					}
				}
			}
			checkTextBatch(t, events)
			if partial > 0 {
				partial--
				return 1, errBlocked
			}
			if fail > 0 {
				fail--
				return 0, errBlocked
			}
			return len(events), nil
		}
		var calls int
		var ends []int // per call, how many times it was told
		notify := func() func(output.TapCompletion) {
			i := len(ends)
			ends = append(ends, 0)
			calls++
			return func(output.TapCompletion) { ends[i]++ }
		}
		for i := 0; i < len(data); i += 3 {
			var a, b byte
			if i+1 < len(data) {
				a = data[i+1]
			}
			if i+2 < len(data) {
				b = data[i+2]
			}
			o := fuzzOwners[int(a)%len(fuzzOwners)]
			switch data[i] % fsOps {
			case fsSequence:
				steps := fuzzSequences[int(b)%len(fuzzSequences)]
				_ = s.Arb.SequenceWithCompletion(o, steps, time.Duration(b/8%3)*5*ms, time.Duration(a/3%4)*5*ms, notify())
			case fsTapSequence:
				_ = s.Arb.TapSequence(o, []keys.Chord{keys.MustParse("A"), keys.MustParse("B")}, 0, time.Duration(a/3%4)*5*ms)
			case fsText:
				_ = s.Arb.TypeText(o, fuzzTexts[int(b)%len(fuzzTexts)], time.Duration(a/3%4)*5*ms)
			case fsAdvance:
				s.Advance(time.Duration(b%64) * ms)
			case fsReleaseOwner:
				_ = s.Arb.ReleaseOwner(o)
			case fsReleaseAll:
				_ = s.Arb.ReleaseAll()
			case fsBlock:
				blocked = a%2 == 1
				s.Arb.SetBlocked(blocked)
			case fsFail:
				fail++
			case fsPartial:
				partial++
			case fsDown:
				_ = s.Arb.Down(o, keys.MustParse("A"))
			case fsUp:
				_ = s.Arb.Up(o, keys.MustParse("A"))
			case fsTap:
				spec := "A"
				if b%2 == 1 {
					spec = "Enter"
				}
				_ = s.Arb.Tap(o, keys.MustParse(spec), time.Duration(a/3%4)*10*ms)
			case fsEventGap:
				_ = s.Arb.SetEventGap(time.Duration(b%4) * 5 * ms)
			}
			if pressedAfterBlock {
				t.Fatalf("step %d: a character was typed while the gate is closed", i/3)
			}
			if down := untrackedUnits(s); len(down) > 0 {
				t.Fatalf("step %d: units left down that the Arbiter does not list as pending: %v", i/3, down)
			}
			if n := s.Arb.Queued(); n > output.MaxQueuedSequences+1024 {
				t.Fatalf("step %d: %d queued", i/3, n)
			}
			for j, n := range ends {
				if n > 1 {
					t.Fatalf("step %d: call %d was told its end %d times", i/3, j, n)
				}
			}
		}
		// Open up, let go of everything, run out the clock: nothing is left.
		fail, partial = 0, 0
		blocked = false
		s.Arb.SetBlocked(false)
		_ = s.Arb.ReleaseAll()
		s.Settle()
		_ = s.Errors()
		if downs := s.Out.Down(); len(downs) > 0 {
			t.Fatalf("keys left down at the end: %v", downs)
		}
		if n := s.Arb.Queued(); n != 0 {
			t.Fatalf("%d texts, sequences or taps queued at the end", n)
		}
		if _, ok := s.Arb.NextDeadline(); ok {
			t.Fatal("a deadline is left at the end")
		}
		for j, n := range ends {
			if n != 1 {
				t.Fatalf("call %d of %d was told its end %d times, want once", j, calls, n)
			}
		}
	})
}
