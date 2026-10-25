package output_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// Operations of FuzzTypeText, each with the opcode byte and two argument bytes
// a and b.
const (
	ftType         = iota // TypeText(owner a, text b, gap (a/3%4)*5ms)
	ftAdvance             // Sim.Advance(b%64 ms)
	ftReleaseOwner        // ReleaseOwner(owner a)
	ftReleaseAll          // ReleaseAll
	ftBlock               // SetBlocked(a%2 == 1)
	ftFail                // the next Send call injects nothing
	ftWarn                // the next Send call warns
	ftDown                // Down(owner a, Ctrl)
	ftUp                  // Up(owner a, Ctrl)
	ftTap                 // Tap(owner a, Ctrl+Z or Enter by b)
	ftEventGap            // SetEventGap((b%4)*5ms)
	ftPartial             // the next Send call injects only its first event (ahead of ftFail)
	ftOps
)

var fuzzTexts = [...]string{
	"abc", "a", "ab\ncd", "😀a", "\n", "\t\t", "pq", "stuv", "x😀z", "你好", "a\tb\nc", "😀😀",
}

// FuzzTypeText runs random texts of three owners among keys, tabs, newlines,
// releases, a closing gate, failed Sends (whole and partial) and an event gap, and
// checks that a typed unit is never left down: each Send that puts one down
// injects its release in the same call, and when it cannot (the press went out and
// the release failed twice) the Arbiter lists the unit as pending release; that a
// surrogate pair is back to back in one Send; that nothing is typed while the gate
// is closed; and that, at the end, once everything is released and settled,
// nothing is queued, held or down.
func FuzzTypeText(f *testing.F) {
	for _, seed := range [][]byte{
		{ftType, 0, 0, ftAdvance, 0, 40},
		{ftType, 9, 2, ftAdvance, 0, 30, ftReleaseOwner, 0, 0, ftAdvance, 0, 40},
		{ftType, 3, 3, ftType, 4, 8, ftAdvance, 0, 20, ftReleaseAll, 0, 0},
		{ftType, 3, 2, ftBlock, 1, 0, ftAdvance, 0, 40, ftBlock, 0, 0, ftType, 3, 2},
		{ftFail, 0, 0, ftType, 0, 0, ftAdvance, 0, 40},
		{ftWarn, 0, 0, ftType, 6, 2, ftAdvance, 0, 40},
		{ftDown, 0, 0, ftType, 6, 2, ftUp, 0, 0, ftAdvance, 0, 40},
		{ftEventGap, 0, 1, ftTap, 1, 0, ftType, 6, 2, ftAdvance, 0, 60},
		{ftEventGap, 0, 3, ftType, 6, 7, ftEventGap, 0, 0, ftAdvance, 0, 60},
		{ftTap, 0, 1, ftType, 3, 4, ftReleaseOwner, 0, 0, ftAdvance, 0, 60},
		{ftPartial, 0, 0, ftFail, 0, 0, ftType, 0, 0, ftAdvance, 0, 40},
		{ftPartial, 0, 0, ftFail, 0, 0, ftType, 3, 2, ftReleaseAll, 0, 0},
		{ftPartial, 0, 0, ftFail, 0, 0, ftType, 6, 2, ftReleaseOwner, 1, 0, ftType, 0, 0},
		{ftPartial, 0, 0, ftType, 3, 2, ftEventGap, 0, 1, ftType, 3, 4, ftAdvance, 0, 40},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 3*200 {
			data = data[:3*200]
		}
		s := newSim(t, withRetry(7*ms))
		blocked, fail, partial := false, 0, 0
		injectedAfterBlock := false
		s.Out.Inject = func(events []output.KeyEvent) (int, error) {
			if blocked {
				for _, e := range events {
					if e.Unicode && !e.Up {
						injectedAfterBlock = true
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
		for i := 0; i < len(data); i += 3 {
			var a, b byte
			if i+1 < len(data) {
				a = data[i+1]
			}
			if i+2 < len(data) {
				b = data[i+2]
			}
			o := fuzzOwners[int(a)%len(fuzzOwners)]
			var err error
			switch data[i] % ftOps {
			case ftType:
				err = s.Arb.TypeText(o, fuzzTexts[int(b)%len(fuzzTexts)], time.Duration(a/3%4)*5*ms)
			case ftAdvance:
				s.Advance(time.Duration(b%64) * ms)
			case ftReleaseOwner:
				err = s.Arb.ReleaseOwner(o)
			case ftReleaseAll:
				err = s.Arb.ReleaseAll()
			case ftBlock:
				blocked = a%2 == 1
				s.Arb.SetBlocked(blocked)
			case ftFail:
				fail++
			case ftPartial:
				partial++
			case ftWarn:
				s.Out.WarnNext(errWarn)
			case ftDown:
				err = s.Arb.Down(o, keys.MustParse("Ctrl"))
			case ftUp:
				err = s.Arb.Up(o, keys.MustParse("Ctrl"))
			case ftTap:
				spec := "Ctrl+Z"
				if b%2 == 1 {
					spec = "Enter"
				}
				err = s.Arb.Tap(o, keys.MustParse(spec), time.Duration(a/3%4)*10*ms)
			case ftEventGap:
				err = s.Arb.SetEventGap(time.Duration(b%4) * 5 * ms)
			}
			_ = err // failures are the point; the checks are below
			if injectedAfterBlock {
				t.Fatalf("step %d: a character was typed while the gate is closed", i/3)
			}
			if down := untrackedUnits(s); len(down) > 0 {
				t.Fatalf("step %d: units left down that the Arbiter does not list as pending: %v", i/3, down)
			}
			if st := s.Arb.Stats(); st.PendingReleases > 0 {
				if _, ok := s.Arb.NextDeadline(); !ok {
					t.Fatalf("step %d: %d releases pending and no deadline to resend them", i/3, st.PendingReleases)
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
		if down := unicodeDown(s.Out); len(down) > 0 {
			t.Fatalf("units left down at the end: %v", down)
		}
		if n := s.Arb.Queued(); n != 0 {
			t.Fatalf("%d texts or taps queued at the end", n)
		}
		if _, ok := s.Arb.NextDeadline(); ok {
			t.Fatal("a deadline is left at the end")
		}
	})
}

// checkTextBatch checks the Unicode events of one Send call as TypeText makes
// them: whole characters, each unit a press and its release, and the two units
// of a pair together.
func checkTextBatch(t *testing.T, events []output.KeyEvent) {
	t.Helper()
	var text []output.KeyEvent
	for _, e := range events {
		if e.Unicode {
			text = append(text, e)
		}
	}
	if len(text) == 0 {
		return
	}
	if !slices.ContainsFunc(text, func(e output.KeyEvent) bool { return !e.Up }) {
		return // the resend of releases that did not go out
	}
	if len(text) != len(events) {
		t.Fatalf("a Send call mixes Unicode events with others: %s", eventsString(events))
	}
	if len(text) != 2 && len(text) != 4 {
		t.Fatalf("a Send call has %d Unicode events, want a character of 1 or 2 units: %s", len(text), eventsString(events))
	}
	for i := 0; i < len(text); i += 2 {
		if text[i].Up || !text[i+1].Up || text[i].Unit != text[i+1].Unit {
			t.Fatalf("units are not press and release by turns: %s", eventsString(events))
		}
	}
	hi := text[0].Unit
	isHigh := hi >= 0xD800 && hi < 0xDC00
	if isHigh != (len(text) == 4) {
		t.Fatalf("a surrogate pair is not back to back in one Send call: %s", eventsString(events))
	}
	if len(text) == 4 {
		if lo := text[2].Unit; lo < 0xDC00 || lo >= 0xE000 {
			t.Fatalf("the second unit of a pair is not a low surrogate: %s", eventsString(events))
		}
	}
}

// untrackedUnits lists the Unicode events the Recorder has down that the Arbiter
// does not list as held (pending release).
func untrackedUnits(s tsim) []string {
	var out []string
	for _, e := range s.Out.Down() {
		if !e.Unicode {
			continue
		}
		if !slices.ContainsFunc(s.Arb.Held(), func(h output.HeldKey) bool { return h.Press.ID() == e.ID() && h.Pending }) {
			out = append(out, fmt.Sprint(e))
		}
	}
	return out
}

// unicodeDown lists the Unicode events the Recorder has down.
func unicodeDown(r *simtest.Recorder) []string {
	var out []string
	for _, e := range r.Down() {
		if e.Unicode {
			out = append(out, fmt.Sprint(e))
		}
	}
	return slices.Clone(out)
}
