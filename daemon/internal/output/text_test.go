package output_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// typed returns the trace of typing the units of text: a press and a release of
// each UTF-16 code unit, as TypeText sends them, all at time t.
func typed(t int, units ...uint16) string {
	var parts []string
	for _, u := range units {
		name := output.UnicodeEvent(u, false).ID().String()
		parts = append(parts, "+"+name+"@"+strconv.Itoa(t), "-"+name+"@"+strconv.Itoa(t))
	}
	return strings.Join(parts, " ")
}

func (s tsim) typeText(o output.Owner, text string, gap time.Duration) {
	s.t.Helper()
	if err := s.Arb.TypeText(o, text, gap); err != nil {
		s.t.Fatalf("TypeText(%v, %q): %v", o, text, err)
	}
}

// completions records the notifications of TypeTextWithCompletion.
type completions struct{ got []output.TapCompletion }

func (c *completions) add(r output.TapCompletion) { c.got = append(c.got, r) }

func (c *completions) want(t *testing.T, want ...output.TapCompletion) {
	t.Helper()
	if len(c.got) != len(want) {
		t.Fatalf("%d completions %+v, want %d %+v", len(c.got), c.got, len(want), want)
	}
	for i := range want {
		if c.got[i] != want[i] {
			t.Errorf("completion %d = %+v, want %+v", i, c.got[i], want[i])
		}
	}
}

// With no gap every character goes out in the command that asked, each in a
// Send call of its own: a press and a release of its UTF-16 unit.
func TestTypeTextNoGap(t *testing.T) {
	s := newSim(t)
	s.typeText(ownerA, "Hi", 0)
	s.ExpectOutput(typed(0, 'H', 'i'))
	if got := s.Out.Calls(); got != 2 {
		t.Errorf("%d Send calls, want 1 per character", got)
	}
	if s.Arb.Queued() != 0 {
		t.Errorf("Queued() = %d after the last character", s.Arb.Queued())
	}
	if d, ok := s.Arb.NextDeadline(); ok {
		t.Errorf("NextDeadline = %v after the last character", d)
	}
	if got := s.Arb.Stats().Typed; got != 2 {
		t.Errorf("Typed = %d, want 2", got)
	}
	if h := s.Arb.Held(); len(h) != 0 {
		t.Errorf("Held() = %v: a typed character is never held", h)
	}
}

// With a gap the first character is typed at once and each next one a gap
// after the previous one.
func TestTypeTextGap(t *testing.T) {
	s := newSim(t)
	s.typeText(ownerA, "abc", 20*ms)
	s.ExpectOutput(typed(0, 'a'))
	if d, ok := s.Arb.NextDeadline(); !ok || d != clock.Time(20*ms) {
		t.Fatalf("NextDeadline = %v, %v, want 20ms", d, ok)
	}
	if s.Arb.Queued() != 1 {
		t.Errorf("Queued() = %d with a text under way, want 1", s.Arb.Queued())
	}
	s.Advance(19 * ms)
	s.ExpectOutput("")
	s.Advance(1 * ms)
	s.ExpectOutput(typed(20, 'b'))
	s.Advance(20 * ms)
	s.ExpectOutput(typed(40, 'c'))
	if _, ok := s.Arb.NextDeadline(); ok || s.Arb.Queued() != 0 {
		t.Errorf("text left: deadline %v, Queued %d", ok, s.Arb.Queued())
	}
}

// A character outside the Basic Multilingual Plane is two UTF-16 units, each
// with a press and a release, back to back in one Send; the gap is between
// characters, not units.
func TestTypeTextSurrogatePair(t *testing.T) {
	s := newSim(t)
	s.typeText(ownerA, "a😀b", 10*ms)
	s.Advance(10 * ms)
	s.Advance(10 * ms)
	want := typed(0, 'a') + " " + typed(10, 0xD83D, 0xDE00) + " " + typed(20, 'b')
	s.ExpectOutput(want)
	recs := s.Out.Records()
	if len(recs) != 8 {
		t.Fatalf("%d events, want 8", len(recs))
	}
	if recs[2].Batch != recs[5].Batch || recs[2].Batch == recs[0].Batch || recs[6].Batch == recs[5].Batch {
		t.Errorf("batches %d %d %d %d: the pair must be one Send, apart from its neighbors", recs[0].Batch, recs[2].Batch, recs[5].Batch, recs[6].Batch)
	}
	// The order is high press, high release, low press, low release.
	for i, wantUnit := range []uint16{0xD83D, 0xD83D, 0xDE00, 0xDE00} {
		e := recs[2+i].Event
		if !e.Unicode || e.Unit != wantUnit || e.Up != (i%2 == 1) {
			t.Errorf("event %d of the pair = %v, want unit %#x up %v", i, e, wantUnit, i%2 == 1)
		}
	}
	if got := s.Arb.Stats().Typed; got != 3 {
		t.Errorf("Typed = %d, want 3 characters", got)
	}
}

// CJK and the other characters of the Basic Multilingual Plane are one unit.
func TestTypeTextCJK(t *testing.T) {
	s := newSim(t)
	s.typeText(ownerA, "你好", 0)
	s.ExpectOutput(typed(0, 0x4F60, 0x597D))
}

// A newline is an Enter tap and a tab a Tab tap, as ordinary keys: the next
// character comes a gap after the tap ended.
func TestTypeTextNewlineAndTab(t *testing.T) {
	s := newSim(t)
	s.typeText(ownerA, "a\n\tb", 10*ms)
	s.ExpectOutput(typed(0, 'a'))
	s.Advance(10 * ms)
	s.ExpectOutput("+Enter@10 -Enter@10")
	s.Advance(10 * ms)
	s.ExpectOutput("+Tab@20 -Tab@20")
	s.Advance(10 * ms)
	s.ExpectOutput(typed(30, 'b'))
	if s.Arb.Queued() != 0 {
		t.Errorf("Queued() = %d", s.Arb.Queued())
	}
}

// The tap of a newline keeps the minimum press: the text goes on a gap after
// the physical release.
func TestTypeTextTapMinPress(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: 20 * ms}))
	s.typeText(ownerA, "\na", 5*ms)
	s.ExpectOutput("+Enter@0")
	if d, ok := s.Arb.NextDeadline(); !ok || d != clock.Time(20*ms) {
		t.Fatalf("NextDeadline = %v, %v, want the release at 20ms", d, ok)
	}
	s.Advance(20 * ms)
	s.ExpectOutput("-Enter@20")
	s.Advance(4 * ms)
	s.ExpectOutput("")
	s.Advance(1 * ms)
	s.ExpectOutput(typed(25, 'a'))
}

// A text of newlines only: the taps are one after another, a gap apart, and
// never overlap.
func TestTypeTextOnlyNewlines(t *testing.T) {
	s := newSim(t)
	s.typeText(ownerA, "\n\n", 7*ms)
	s.ExpectOutput("+Enter@0 -Enter@0")
	s.Advance(7 * ms)
	s.ExpectOutput("+Enter@7 -Enter@7")
	if s.Arb.Queued() != 0 {
		t.Errorf("Queued() = %d", s.Arb.Queued())
	}
}

// The Enter of a newline is held by someone else: it is pressed once more, like
// any tap of a held key, and the text goes on.
func TestTypeTextNewlineOnHeldEnter(t *testing.T) {
	s := newSim(t)
	s.down(ownerB, "Enter")
	s.ExpectOutput("+Enter@0")
	s.typeText(ownerA, "\nx", 0)
	s.ExpectOutput("+Enter@0 " + typed(0, 'x'))
	s.up(ownerB, "Enter")
	s.ExpectOutput("-Enter@0")
}

// ReleaseOwner ends the unsent rest of the owner's text; another owner's text
// goes on.
func TestTypeTextReleaseOwner(t *testing.T) {
	s := newSim(t)
	var doneA, doneB completions
	if err := s.Arb.TypeTextWithCompletion(ownerA, "abc", 10*ms, doneA.add); err != nil {
		t.Fatal(err)
	}
	if err := s.Arb.TypeTextWithCompletion(ownerB, "xyz", 10*ms, doneB.add); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput(typed(0, 'a') + " " + typed(0, 'x'))
	s.Advance(5 * ms)
	if err := s.Arb.ReleaseOwner(ownerA); err != nil {
		t.Fatal(err)
	}
	doneA.want(t, output.TapCompletion{Dropped: true, Canceled: true})
	s.Advance(30 * ms)
	s.ExpectOutput(typed(10, 'y') + " " + typed(20, 'z'))
	doneA.want(t, output.TapCompletion{Dropped: true, Canceled: true})
	doneB.want(t, output.TapCompletion{})
	if s.Arb.Queued() != 0 {
		t.Errorf("Queued() = %d", s.Arb.Queued())
	}
}

// ReleaseAll, which the pause, a profile change and the emergency release all
// use, ends every text.
func TestTypeTextReleaseAll(t *testing.T) {
	s := newSim(t)
	var done completions
	if err := s.Arb.TypeTextWithCompletion(ownerA, "abc", 10*ms, done.add); err != nil {
		t.Fatal(err)
	}
	if err := s.Arb.TypeText(ownerB, "xyz", 10*ms); err != nil {
		t.Fatal(err)
	}
	if err := s.Arb.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	s.ClearOutput()
	s.Advance(50 * ms)
	s.ExpectOutput("")
	done.want(t, output.TapCompletion{Dropped: true, Canceled: true})
	if _, ok := s.Arb.NextDeadline(); ok || s.Arb.Queued() != 0 {
		t.Errorf("work left after ReleaseAll: Queued %d", s.Arb.Queued())
	}
}

// A text whose Enter tap is under way when the owner is released ends with the
// tap, and the tap's keys are released too.
func TestTypeTextReleaseDuringTap(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: 20 * ms}))
	var done completions
	if err := s.Arb.TypeTextWithCompletion(ownerA, "\nabc", 0, done.add); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("+Enter@0")
	s.Advance(5 * ms)
	if err := s.Arb.ReleaseOwner(ownerA); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("-Enter@5")
	done.want(t, output.TapCompletion{Dropped: true, Canceled: true})
	s.Advance(100 * ms)
	s.ExpectOutput("")
}

// Closing the gate ends the texts and counts their unsent characters in
// Blocked. A text started while it is closed types nothing and counts one.
func TestTypeTextBlocked(t *testing.T) {
	s := newSim(t)
	var done completions
	if err := s.Arb.TypeTextWithCompletion(ownerA, "abcd", 10*ms, done.add); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput(typed(0, 'a'))
	s.Arb.SetBlocked(true)
	if got := s.Arb.Stats().Blocked; got != 3 {
		t.Errorf("Blocked = %d after the gate closed on 3 unsent characters", got)
	}
	done.want(t, output.TapCompletion{Dropped: true, Canceled: true})
	s.Advance(50 * ms)
	s.ExpectOutput("")

	var again completions
	if err := s.Arb.TypeTextWithCompletion(ownerA, "xyz", 0, again.add); err != nil {
		t.Fatal(err)
	}
	if got := s.Arb.Stats().Blocked; got != 4 {
		t.Errorf("Blocked = %d after a TypeText at a closed gate, want 4", got)
	}
	again.want(t, output.TapCompletion{Dropped: true, Canceled: true})
	s.ExpectOutput("")
	s.Arb.SetBlocked(false)
	s.Advance(50 * ms)
	s.ExpectOutput("")
	if s.Arb.Queued() != 0 {
		t.Errorf("Queued() = %d", s.Arb.Queued())
	}
}

// What is wrong with a text or an owner is refused: nothing is typed, the
// error says why, and the notification says dropped and canceled.
func TestTypeTextRejected(t *testing.T) {
	tests := []struct {
		name  string
		o     output.Owner
		text  string
		error string
	}{
		{"empty", ownerA, "", "empty"},
		{"carriage return", ownerA, "a\r\nb", "control character U+000D"},
		{"NUL", ownerA, "\x00", "control character U+0000"},
		{"escape", ownerA, "\x1b", "control character U+001B"},
		{"too long", ownerA, strings.Repeat("a", 257), "too long"},
		{"pairs too long", ownerA, strings.Repeat("😀", 129), "too long"},
		{"bad UTF-8", ownerA, "a\xffb", "UTF-8"},
		{"zero owner", output.Owner{}, "a", "invalid owner"},
		{"owner with no source", output.Owner{Slot: 1}, "a", "invalid owner"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSim(t)
			var done completions
			err := s.Arb.TypeTextWithCompletion(tt.o, tt.text, 0, done.add)
			if err == nil || !strings.Contains(err.Error(), tt.error) {
				t.Errorf("error = %v, want one with %q", err, tt.error)
			}
			if err := s.Arb.TypeText(tt.o, tt.text, 0); err == nil {
				t.Error("TypeText accepted it")
			}
			done.want(t, output.TapCompletion{Dropped: true, Canceled: true})
			if s.Out.Calls() != 0 || s.Arb.Queued() != 0 || s.Arb.Stats().Typed != 0 {
				t.Errorf("a rejected text sent %d Send calls, Queued %d, Typed %d", s.Out.Calls(), s.Arb.Queued(), s.Arb.Stats().Typed)
			}
		})
	}
	// The longest text is accepted: 256 units, as ASCII and as pairs.
	for _, text := range []string{strings.Repeat("a", 256), strings.Repeat("😀", 128)} {
		s := newSim(t)
		s.typeText(ownerA, text, 0)
		if got := s.Out.Calls(); got != len([]rune(text)) {
			t.Errorf("%d Send calls for %d characters", got, len([]rune(text)))
		}
	}
}

// A character that cannot be sent ends the text: the error comes back, the rest
// is dropped, and the notification says dropped, but not canceled.
func TestTypeTextSendFails(t *testing.T) {
	s := newSim(t)
	var done completions
	s.Out.FailNext(0, nil)
	err := s.Arb.TypeTextWithCompletion(ownerA, "abc", 10*ms, done.add)
	if !errors.Is(err, simtest.ErrBlocked) {
		t.Fatalf("TypeText = %v, want the Send error", err)
	}
	done.want(t, output.TapCompletion{Dropped: true})
	// The release of the press that did not go out is resent at once, as any
	// release is: one more release of a key that is not down, which does no harm.
	s.ExpectOutput("-" + output.UnicodeEvent('a', true).ID().String() + "@0")
	s.Advance(100 * ms)
	s.ExpectOutput("")
	if got := s.Arb.Stats().SendErrors; got != 1 {
		t.Errorf("SendErrors = %d, want 1", got)
	}
	if got := s.Arb.Stats().Typed; got != 0 {
		t.Errorf("Typed = %d, want 0: the character did not go out", got)
	}
	if s.Arb.Queued() != 0 {
		t.Errorf("Queued() = %d: the text is over", s.Arb.Queued())
	}
	if _, ok := s.Arb.NextDeadline(); ok {
		t.Error("a deadline is left")
	}
}

// If the press of a character went out and the release did not, the release is
// resent at once, like any release; the rest of the text is dropped. Nothing is
// left down (the Sim checks it).
func TestTypeTextPartialSend(t *testing.T) {
	for _, text := range []string{"a", "😀"} {
		s := newSim(t)
		var done completions
		s.Out.FailNext(1, nil)
		err := s.Arb.TypeTextWithCompletion(ownerA, text+"b", 10*ms, done.add)
		if err == nil {
			t.Fatalf("TypeText(%q) = nil, want the Send error", text)
		}
		done.want(t, output.TapCompletion{Dropped: true})
		if down := s.Out.Down(); len(down) != 0 {
			t.Errorf("%q: still down after the resent release: %v", text, down)
		}
		if got := s.Arb.Stats().Retries; got != 1 {
			t.Errorf("%q: Retries = %d, want 1 resend of the releases", text, got)
		}
		s.Advance(100 * ms)
		for _, rec := range s.Out.Records() {
			if rec.Event.Unit == 'b' {
				t.Errorf("%q: the text went on after the failure: %v", text, rec.Event)
			}
		}
	}
}

// If the press of a character went out and its release failed twice (the resend
// included), the unit is down with nothing in the key table to say so. The
// Arbiter still keeps its release pending: it is counted, has a deadline,
// is resent at it, and is sent at once by ReleaseAll (CLAUDE.md invariant 1).
func TestTypeTextReleaseFailsTwice(t *testing.T) {
	for _, text := range []string{"a", "😀"} {
		hi := []rune(text)[0]
		if hi >= 0x10000 {
			hi = 0xD83D
		}
		unit := output.UnicodeEvent(uint16(hi), true).ID().String()
		setup := func() tsim {
			s := newSim(t, withRetry(7*ms))
			s.Out.FailNext(1, nil) // only the press of the first unit goes out
			s.Out.FailNext(0, nil) // and the resent release fails too
			var done completions
			if err := s.Arb.TypeTextWithCompletion(ownerA, text+"b", 10*ms, done.add); err == nil {
				t.Fatalf("%q: TypeText = nil, want the Send error", text)
			}
			done.want(t, output.TapCompletion{Dropped: true})
			if down := unicodeDown(s.Out); len(down) != 1 {
				t.Fatalf("%q: units down = %v, want only the first one: the test needs it stuck", text, down)
			}
			return s
		}

		// Resent at the retry time, with no other command.
		s := setup()
		if got := s.Arb.Stats().PendingReleases; got == 0 {
			t.Errorf("%q: PendingReleases = 0 with a unit down", text)
		}
		if d, ok := s.Arb.NextDeadline(); !ok || d != clock.Time(7*ms) {
			t.Errorf("%q: NextDeadline = %v, %v; want the retry at 7ms", text, d, ok)
		}
		s.Advance(7 * ms)
		if down := unicodeDown(s.Out); len(down) != 0 {
			t.Errorf("%q: still down after the retry: %v", text, down)
		}
		if got := s.Arb.Stats().PendingReleases; got != 0 {
			t.Errorf("%q: PendingReleases = %d after the retry", text, got)
		}
		if _, ok := s.Arb.NextDeadline(); ok {
			t.Errorf("%q: a deadline is left after the release went out", text)
		}
		if got := s.Output(); !strings.Contains(got, "-"+unit+"@7") {
			t.Errorf("%q: no release at 7ms in %s", text, got)
		}
		if errs := s.Errors(); len(errs) != 0 {
			t.Errorf("%q: Advance: %v", text, errs)
		}

		// ReleaseAll sends it before the retry time, and so does a release of
		// another owner, since the unit has no owner.
		for name, release := range map[string]func(*output.Arbiter) error{
			"ReleaseAll":   (*output.Arbiter).ReleaseAll,
			"ReleaseOwner": func(a *output.Arbiter) error { return a.ReleaseOwner(ownerB) },
		} {
			s = setup()
			if err := release(s.Arb); err != nil {
				t.Errorf("%q: %s = %v", text, name, err)
			}
			if down := unicodeDown(s.Out); len(down) != 0 {
				t.Errorf("%q: still down after %s: %v", text, name, down)
			}
			if got := s.Arb.Stats().PendingReleases; got != 0 {
				t.Errorf("%q: PendingReleases = %d after %s", text, got, name)
			}
		}

		// A release that fails again stays pending, with a later deadline.
		s = setup()
		s.Out.FailNext(0, nil)
		s.Advance(7 * ms)
		if errs := s.Errors(); len(errs) != 1 {
			t.Errorf("%q: Advance errors = %v, want the failed retry", text, errs)
		}
		if down := unicodeDown(s.Out); len(down) != 1 {
			t.Errorf("%q: units down = %v after a failed retry", text, down)
		}
		if d, ok := s.Arb.NextDeadline(); !ok || d != clock.Time(14*ms) {
			t.Errorf("%q: NextDeadline = %v, %v; want the next retry at 14ms", text, d, ok)
		}
		s.Advance(7 * ms)
		if down := unicodeDown(s.Out); len(down) != 0 {
			t.Errorf("%q: still down after the second retry: %v", text, down)
		}
	}
}

// The release of a unit that failed twice is also sent by Stop, which retries
// the pending releases until they are gone.
func TestLoopStopReleasesFailedUnit(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	lt.rec.With(func(r *simtest.Recorder) {
		r.FailNext(1, nil)
		r.FailNext(0, nil)
	})
	if err := lt.l.Remote().TypeText(ownerA, "ab", 10*ms); err == nil {
		t.Fatal("TypeText = nil, want the Send error")
	}
	lt.rec.With(func(r *simtest.Recorder) {
		if down := unicodeDown(r); len(down) != 1 {
			t.Fatalf("units down = %v, want the first one: the test needs it stuck", down)
		}
	})
	if err := lt.stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	lt.rec.With(func(r *simtest.Recorder) {
		if down := unicodeDown(r); len(down) != 0 {
			t.Errorf("units down after Stop: %v", down)
		}
	})
}

// A warning (the Send injected everything and still returned an error) does not
// stop the text.
func TestTypeTextWarning(t *testing.T) {
	s := newSim(t)
	s.Out.WarnNext(errWarn)
	err := s.Arb.TypeText(ownerA, "ab", 0)
	if !errors.Is(err, errWarn) {
		t.Fatalf("TypeText = %v, want the warning", err)
	}
	s.ExpectOutput(typed(0, 'a', 'b'))
	if got := s.Arb.Stats().SendWarnings; got != 1 {
		t.Errorf("SendWarnings = %d, want 1", got)
	}
}

// A text takes no part in the key bookkeeping: keys held across it stay held and
// are released by their own Up, and the text arrives with them down.
func TestTypeTextWhileKeysHeld(t *testing.T) {
	s := newSim(t)
	s.down(ownerB, "Ctrl")
	s.typeText(ownerA, "ab", 10*ms)
	s.ExpectOutput("+LCtrl@0 " + typed(0, 'a'))
	if held := s.Arb.Held(); len(held) != 1 || held[0].Press.Key.String() != "LCtrl" {
		t.Errorf("Held() = %v, want only LCtrl", held)
	}
	s.up(ownerB, "Ctrl")
	s.Advance(10 * ms)
	s.ExpectOutput("-LCtrl@0 " + typed(10, 'b'))
}

// Texts of two owners take turns, a character each when it is due, in the order
// they were started.
func TestTypeTextInterleaved(t *testing.T) {
	s := newSim(t)
	s.typeText(ownerA, "ab", 10*ms)
	s.typeText(ownerB, "xy", 10*ms)
	s.typeText(ownerA, "12", 10*ms)
	s.Advance(10 * ms)
	want := typed(0, 'a') + " " + typed(0, 'x') + " " + typed(0, '1') + " " +
		typed(10, 'b') + " " + typed(10, 'y') + " " + typed(10, '2')
	s.ExpectOutput(want)
}

// A negative gap is none.
func TestTypeTextNegativeGap(t *testing.T) {
	s := newSim(t)
	s.typeText(ownerA, "ab", -5*ms)
	s.ExpectOutput(typed(0, 'a', 'b'))
}

// A local dry run (the window of padmate itself is in front) sends nothing, and
// the text still runs its course.
func TestTypeTextLocalDryRun(t *testing.T) {
	s := newSim(t)
	if err := s.Arb.SetLocalDryRun(true); err != nil {
		t.Fatal(err)
	}
	var done completions
	if err := s.Arb.TypeTextWithCompletion(ownerA, "ab", 10*ms, done.add); err != nil {
		t.Fatal(err)
	}
	s.Advance(10 * ms)
	s.ExpectOutput("")
	done.want(t, output.TapCompletion{})
	if got := s.Arb.Stats().Typed; got != 2 {
		t.Errorf("Typed = %d, want 2", got)
	}
}

// The notification comes once, after the last character, with nothing dropped.
func TestTypeTextCompletion(t *testing.T) {
	s := newSim(t)
	var done completions
	if err := s.Arb.TypeTextWithCompletion(ownerA, "ab\n", 10*ms, done.add); err != nil {
		t.Fatal(err)
	}
	done.want(t)
	s.Advance(10 * ms)
	done.want(t)
	s.Advance(10 * ms)
	done.want(t, output.TapCompletion{})
	s.Advance(100 * ms)
	done.want(t, output.TapCompletion{})
}

// With an event gap the events the gap holds back go first, a text waits for
// them and for the gap since the last Send, and each of its characters is still
// one Send.
func TestTypeTextEventGap(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{EventGap: 10 * ms}))
	s.tap(ownerB, "Ctrl+Z", 0)
	s.typeText(ownerA, "ab", 0)
	s.Settle()
	recs := s.Out.Records()
	// The four key events of the tap, then the four events of the two characters;
	// every Send at least the gap after the one before.
	if len(recs) != 8 {
		t.Fatalf("%d events, want 8: %s", len(recs), s.Output())
	}
	for i, rec := range recs {
		if rec.Event.Unicode != (i >= 4) {
			t.Errorf("event %d is %v: the keys of the tap come before the text", i, rec.Event)
		}
		if i > 0 && rec.Batch != recs[i-1].Batch && rec.T.Sub(recs[i-1].T) < 10*ms {
			t.Errorf("event %d at %v is less than the gap after the Send before it, at %v", i, rec.T, recs[i-1].T)
		}
	}
	if recs[4].Event.Unit != 'a' || recs[6].Event.Unit != 'b' {
		t.Errorf("the characters are %v and %v, want a and b", recs[4].Event, recs[6].Event)
	}
	if n := s.Out.Calls(); n != 4+2 {
		t.Errorf("%d Send calls, want one per key event and one per character", n)
	}
}

// An event gap makes a text's characters at least a gap apart even with no gap
// of its own.
func TestTypeTextEventGapSpacing(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{EventGap: 15 * ms}))
	s.typeText(ownerA, "abc", 0)
	s.ExpectOutput(typed(0, 'a'))
	if d, ok := s.Arb.NextDeadline(); !ok || d != clock.Time(15*ms) {
		t.Fatalf("NextDeadline = %v, %v, want 15ms", d, ok)
	}
	s.Settle()
	s.ExpectOutput(typed(15, 'b') + " " + typed(30, 'c'))
}

// Releasing an owner whose text waits behind the event gap's queue ends it.
func TestTypeTextEventGapRelease(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{EventGap: 10 * ms}))
	s.tap(ownerB, "Ctrl+Z", 0)
	s.typeText(ownerA, "ab", 0)
	if err := s.Arb.ReleaseOwner(ownerA); err != nil {
		t.Fatal(err)
	}
	s.Settle()
	for _, rec := range s.Out.Records() {
		if rec.Event.Unicode {
			t.Errorf("a character of a released text was typed: %v", rec.Event)
		}
	}
}

// Raising the gap in the middle of a text slows it down from then on.
func TestTypeTextSetEventGap(t *testing.T) {
	s := newSim(t)
	s.typeText(ownerA, "abc", 5*ms)
	s.ExpectOutput(typed(0, 'a'))
	if err := s.Arb.SetEventGap(20 * ms); err != nil {
		t.Fatal(err)
	}
	s.Settle()
	s.ExpectOutput(typed(20, 'b') + " " + typed(40, 'c'))
}

// The menu mask under the Arbiter does not take a typed character for a key: a
// lone Alt released after a text still gets its mask key, which does no harm.
func TestTypeTextMenuMask(t *testing.T) {
	s := newSim(t, simtest.WithMenuMask())
	s.down(ownerB, "Alt")
	s.typeText(ownerA, "a", 0)
	s.up(ownerB, "Alt")
	s.ExpectOutput("+LAlt@0 " + typed(0, 'a') + " +vk:0xE8@0 -vk:0xE8@0 -LAlt@0")
}

// A text is typed through the Loop (T-output) as through the Arbiter, and its
// notification arrives once.
func TestRemoteTypeText(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	r := lt.l.Remote()
	done := make(chan output.TapCompletion, 2)
	if err := r.TypeTextWithCompletion(ownerA, "ab", 10*ms, func(c output.TapCompletion) { done <- c }); err != nil {
		t.Fatal(err)
	}
	if got := lt.rec.Trace(); got != typed(0, 'a') {
		t.Errorf("after the command: %s, want the first character", got)
	}
	lt.clk.Set(clock.Time(10 * ms))
	select {
	case c := <-done:
		if c != (output.TapCompletion{}) {
			t.Errorf("completion = %+v", c)
		}
	case <-time.After(waitLimit):
		t.Fatal("no completion")
	}
	lt.sync()
	if got := lt.rec.Trace(); got != typed(0, 'a')+" "+typed(10, 'b') {
		t.Errorf("output: %s", got)
	}
	if err := r.TypeText(ownerA, "", 0); err == nil {
		t.Error("TypeText of nothing succeeded")
	}
}

// After the Loop has ended a TypeText fails with ErrLoopClosed and its
// notification says dropped and canceled, once.
func TestRemoteTypeTextClosed(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	r := lt.l.Remote()
	if err := lt.stop(); err != nil {
		t.Fatal(err)
	}
	var done completions
	if err := r.TypeTextWithCompletion(ownerA, "a", 0, done.add); !errors.Is(err, output.ErrLoopClosed) {
		t.Fatalf("TypeTextWithCompletion = %v, want ErrLoopClosed", err)
	}
	done.want(t, output.TapCompletion{Dropped: true, Canceled: true})
}

// Stop ends a text still under way, and no key is left down.
func TestLoopStopEndsText(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	r := lt.l.Remote()
	if err := r.TypeText(ownerA, "abc", 50*ms); err != nil {
		t.Fatal(err)
	}
	// lt.stop would move the clock to the text's deadline and race with Stop.
	if err := lt.l.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := lt.rec.Trace(); got != typed(0, 'a') {
		t.Errorf("output: %s", got)
	}
}

// BlockFor, which the pause and the emergency release use, ends a text under
// way and keeps the next from starting.
func TestLoopBlockEndsText(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	r := lt.l.Remote()
	if err := r.TypeText(ownerA, "abc", 50*ms); err != nil {
		t.Fatal(err)
	}
	lt.l.Block(true)
	lt.sync()
	lt.clk.Set(clock.Time(200 * ms))
	lt.sync()
	if got := lt.rec.Trace(); got != typed(0, 'a') {
		t.Errorf("output: %s, want the first character only", got)
	}
	if err := r.TypeText(ownerA, "z", 0); err != nil {
		t.Fatal(err)
	}
	if got := lt.rec.Trace(); got != typed(0, 'a') {
		t.Errorf("output: %s: nothing is typed while the gate is closed", got)
	}
	lt.l.Block(false)
	lt.sync()
}

// A typed unit is never held, so the held-key log has no bit for one, and the
// sweep of a log cannot name one (OUT-14, OUT-21).
func TestHeldLogNeverHasUnicodeEvents(t *testing.T) {
	for _, unit := range []uint16{'A', 0x4F60, 0xD83D, 0xDE00, 0, 0xE7, 0xFFFF} {
		b := output.EncodeHeld(output.UnicodeEvent(unit, false), press(t, "LCtrl"))
		ids, err := output.DecodeHeld(b)
		if err != nil || len(ids) != 1 || ids[0] != press(t, "LCtrl").ID() {
			t.Errorf("a log with unit %#x and LCtrl has %v, %v; want only LCtrl", unit, ids, err)
		}
		ups, err := output.HeldReleases(b)
		if err != nil || len(ups) != 1 || ups[0].Unicode {
			t.Errorf("the sweep of a log with unit %#x releases %v, %v; want only LCtrl", unit, ups, err)
		}
	}
}

// A media key is a key sent by VK, so it has a bit of the VK bitmap like Pause,
// and the sweep releases it as the key of the table with its E0 scan code.
func TestHeldLogMediaKeys(t *testing.T) {
	for name, vk := range map[string]uint16{
		"VolumeUp": 0xAF, "VolumeDown": 0xAE, "VolumeMute": 0xAD, "MediaPlayPause": 0xB3,
		"MediaNext": 0xB0, "MediaPrevious": 0xB1, "MediaStop": 0xB2,
	} {
		b := output.EncodeHeld(press(t, name))
		ids, err := output.DecodeHeld(b)
		if err != nil || len(ids) != 1 || ids[0] != (output.KeyID{ByVK: true, VK: vk}) {
			t.Errorf("a log with %s has %v, %v; want the VK %#x", name, ids, err, vk)
		}
		ups, err := output.HeldReleases(b)
		if err != nil || len(ups) != 1 {
			t.Errorf("the sweep of a log with %s releases %v, %v", name, ups, err)
			continue
		}
		want := press(t, name)
		want.Up = true
		if ups[0] != want {
			t.Errorf("the sweep releases %+v for %s, want %+v", ups[0], name, want)
		}
	}
}

// The held-key log has no unit of a text, whatever the Loop writes around it,
// and a text typed across a held key leaves that key in the log.
func TestLoopHeldLogSkipsText(t *testing.T) {
	path := heldPath(t)
	lt := startLoopWith(t, output.LoopOptions{HeldFile: path, KeyDown: func(output.KeyEvent) bool { return false }}, nil, nil)
	if res := lt.l.Swept(); res.Err != nil || res.Off || len(res.Released) > 0 {
		t.Fatalf("Swept() = %+v, want nothing done", res)
	}
	r := lt.l.Remote()
	lt.do(down(ownerB, "Ctrl"))
	wantHeld(t, path, "LCtrl")
	if err := r.TypeText(ownerA, "a😀\nb", 0); err != nil {
		t.Fatal(err)
	}
	wantHeld(t, path, "LCtrl")
	lt.do(up(ownerB, "Ctrl"))
	wantHeld(t, path)
	if err := lt.stop(); err != nil {
		t.Fatal(err)
	}
	wantNoHeld(t, path)
	want := "+LCtrl@0 " + typed(0, 'a', 0xD83D, 0xDE00) + " +Enter@0 -Enter@0 " + typed(0, 'b') + " -LCtrl@0"
	if got := lt.rec.Trace(); got != want {
		t.Errorf("output:\n got %s\nwant %s", got, want)
	}
}

// If the Sender panics in the Send of a character, the ledger cannot tell what
// went out and releases its press, as for any key: the release of a unit that
// is not down does no harm. Nothing stays down.
func TestLoopPanicInTextSend(t *testing.T) {
	for _, after := range []bool{false, true} {
		fake := clock.NewFake()
		rec := simtest.NewSyncRecorder(fake)
		l := output.StartLoop(fake, &panicSender{rec: rec, panics: []int{1}, after: after}, output.LoopOptions{})
		err := l.Remote().TypeText(ownerA, "abc", 0)
		checkPanic(t, l, err, "boom 1", "-text:U+0062", nil)
		want := "+text:U+0061@0 -text:U+0061@0 -text:U+0062@0"
		if after {
			want = "+text:U+0061@0 -text:U+0061@0 +text:U+0062@0 -text:U+0062@0 -text:U+0062@0"
		}
		if got := rec.Trace(); got != want {
			t.Errorf("after %v: output:\n got %s\nwant %s", after, got, want)
		}
		releaseDown(t, rec)
	}
}
