package output_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// The sequence (MAP-22, ADR-0070): steps that tap a shortcut, wait or type a
// text, run by one owner-cancellable queue of the Arbiter.

func kstep(spec string) keys.Step { return keys.KeysStep(keys.MustParse(spec)) }

func wstep(d time.Duration) keys.Step { return keys.WaitStep(d) }

func tstep(text string) keys.Step { return keys.TextStep(text) }

func (s tsim) sequence(o output.Owner, hold, gap time.Duration, steps ...keys.Step) {
	s.t.Helper()
	if err := s.Arb.Sequence(o, steps, hold, gap); err != nil {
		s.t.Fatalf("Sequence(%v): %v", o, err)
	}
}

func (s tsim) sequenceDone(o output.Owner, hold, gap time.Duration, done *completions, steps ...keys.Step) {
	s.t.Helper()
	if err := s.Arb.SequenceWithCompletion(o, steps, hold, gap, done.add); err != nil {
		s.t.Fatalf("SequenceWithCompletion(%v): %v", o, err)
	}
}

// A sequence of shortcuts alone runs as TapSequence does: each item is tapped
// for the press time, and the next starts a gap after the release.
func TestSequenceKeysOnly(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: 20 * ms}))
	var done completions
	s.sequenceDone(ownerA, 5*ms, 10*ms, &done, kstep("A"), kstep("B"))
	s.Settle()
	if got := s.Output(); got != "+A@0 -A@20 +B@30 -B@50" {
		t.Fatalf("output=%s", got)
	}
	done.want(t, output.TapCompletion{})
}

// A wait counts from the release of the step before it, not from the gap
// after it.
func TestSequenceWaitCountsFromTheRelease(t *testing.T) {
	s := newSim(t)
	s.sequence(ownerA, 5*ms, 10*ms, kstep("A"), wstep(100*ms), kstep("B"))
	s.Settle()
	if got := s.Output(); got != "+A@0 -A@5 +B@105 -B@110" {
		t.Fatalf("output=%s", got)
	}
}

// The gap between two steps is a floor: a wait shorter than it does not
// shorten it.
func TestSequenceWaitShorterThanTheGap(t *testing.T) {
	s := newSim(t)
	s.sequence(ownerA, 0, 20*ms, kstep("A"), wstep(5*ms), kstep("B"))
	s.Settle()
	if got := s.Output(); got != "+A@0 -A@0 +B@20 -B@20" {
		t.Fatalf("output=%s", got)
	}
}

// Waits one after another add up, from the same release.
func TestSequenceWaitsAddUp(t *testing.T) {
	s := newSim(t)
	s.sequence(ownerA, 0, 10*ms, kstep("A"), wstep(40*ms), wstep(60*ms), kstep("B"))
	s.Settle()
	if got := s.Output(); got != "+A@0 -A@0 +B@100 -B@100" {
		t.Fatalf("output=%s", got)
	}
}

// A wait first counts from the start of the sequence.
func TestSequenceLeadingWait(t *testing.T) {
	s := newSim(t)
	s.sequence(ownerA, 0, 10*ms, wstep(30*ms), kstep("A"))
	if got := s.Output(); got != "" {
		t.Fatalf("output=%s before the wait is over", got)
	}
	s.expectDeadline(30 * ms)
	s.Settle()
	if got := s.Output(); got != "+A@30 -A@30" {
		t.Fatalf("output=%s", got)
	}
}

// A wait last keeps the sequence going until it is over: the sequence ends then,
// not at the release before it.
func TestSequenceTrailingWait(t *testing.T) {
	s := newSim(t)
	var done completions
	s.sequenceDone(ownerA, 0, 10*ms, &done, kstep("A"), wstep(50*ms))
	s.ExpectOutput("+A@0 -A@0")
	done.want(t)
	if s.Arb.Queued() != 1 {
		t.Errorf("Queued() = %d during the last wait, want 1", s.Arb.Queued())
	}
	s.Advance(49 * ms)
	done.want(t)
	s.Advance(1 * ms)
	done.want(t, output.TapCompletion{})
	if s.Arb.Queued() != 0 {
		t.Errorf("Queued() = %d after the last wait", s.Arb.Queued())
	}
}

// A text step types its characters a gap apart, and the step after it starts a
// gap after the last character.
func TestSequenceTextStep(t *testing.T) {
	s := newSim(t)
	var done completions
	s.sequenceDone(ownerA, 0, 10*ms, &done, kstep("A"), tstep("hi"), kstep("B"))
	s.Settle()
	want := "+A@0 -A@0 " + typed(10, 'h') + " " + typed(20, 'i') + " +B@30 -B@30"
	if got := s.Output(); got != want {
		t.Fatalf("output=%s\nwant=%s", got, want)
	}
	done.want(t, output.TapCompletion{})
	if got := s.Arb.Stats().Typed; got != 2 {
		t.Errorf("Typed = %d, want 2", got)
	}
}

// A text alone is the same as TypeText.
func TestSequenceTextOnly(t *testing.T) {
	s := newSim(t)
	s.sequence(ownerA, 0, 10*ms, tstep("你好"))
	s.Settle()
	if got, want := s.Output(), typed(0, 0x4F60)+" "+typed(10, 0x597D); got != want {
		t.Fatalf("output=%s\nwant=%s", got, want)
	}
}

// A newline in a text is an Enter tap and a tab a Tab tap, as TypeText types
// them; the next character comes a gap after the tap.
func TestSequenceTextNewline(t *testing.T) {
	s := newSim(t)
	s.sequence(ownerA, 0, 10*ms, tstep("a\nb"))
	s.Settle()
	want := typed(0, 'a') + " +Enter@10 -Enter@10 " + typed(20, 'b')
	if got := s.Output(); got != want {
		t.Fatalf("output=%s\nwant=%s", got, want)
	}
}

// With no timing at all a sequence is done in the one command that asked.
func TestSequenceZeroTimingCompletesInOneCommand(t *testing.T) {
	s := newSim(t)
	var done completions
	s.sequenceDone(ownerA, 0, 0, &done, kstep("A"), tstep("é"), kstep("B"))
	want := "+A@0 -A@0 " + typed(0, 0xE9) + " +B@0 -B@0"
	if got := s.Output(); got != want {
		t.Fatalf("output=%s\nwant=%s", got, want)
	}
	done.want(t, output.TapCompletion{})
	if s.Arb.Queued() != 0 {
		t.Errorf("Queued() = %d: the sequence did not finish in one command", s.Arb.Queued())
	}
}

// A negative gap is zero.
func TestSequenceNegativeGap(t *testing.T) {
	s := newSim(t)
	s.sequence(ownerA, 0, -time.Second, kstep("A"), kstep("B"))
	if got := s.Output(); got != "+A@0 -A@0 +B@0 -B@0" {
		t.Fatalf("output=%s", got)
	}
}

// A sequence waits for the ones before it, whoever they belong to: the second
// trigger of a binding runs after the first (and not between its steps).
func TestSequenceRunsAfterTheEarlierOnes(t *testing.T) {
	s := newSim(t)
	var doneA, doneB completions
	s.sequenceDone(ownerA, 0, 10*ms, &doneA, kstep("A"), wstep(50*ms), kstep("B"))
	s.sequenceDone(ownerB, 0, 10*ms, &doneB, kstep("C"), tstep("x"))
	s.ExpectOutput("+A@0 -A@0")
	s.Advance(49 * ms)
	s.ExpectOutput("")
	s.Advance(1 * ms)
	// B ends the first sequence, and the second starts in the same instant.
	s.ExpectOutput("+B@50 -B@50 +C@50 -C@50")
	doneA.want(t, output.TapCompletion{})
	doneB.want(t)
	s.Settle()
	s.ExpectOutput(typed(60, 'x'))
	doneB.want(t, output.TapCompletion{})
}

// The wait of a queued sequence counts from its start, not from when it was
// asked for.
func TestSequenceQueuedWaitStartsWithTheSequence(t *testing.T) {
	s := newSim(t)
	s.sequence(ownerA, 0, 0, wstep(100*ms), kstep("A"))
	s.sequence(ownerB, 0, 0, wstep(30*ms), kstep("B"))
	s.Settle()
	if got := s.Output(); got != "+A@100 -A@100 +B@130 -B@130" {
		t.Fatalf("output=%s", got)
	}
}

// A TapSequence does not wait for a Sequence, and a Sequence not for it.
func TestSequenceAndTapSequenceAreIndependent(t *testing.T) {
	s := newSim(t)
	s.sequence(ownerA, 0, 0, wstep(50*ms), kstep("A"))
	if err := s.Arb.TapSequence(ownerB, []keys.Chord{keys.MustParse("B")}, 0, 0); err != nil {
		t.Fatal(err)
	}
	s.Settle()
	if got := s.Output(); got != "+B@0 -B@0 +A@50 -A@50" {
		t.Fatalf("output=%s", got)
	}
}

// At most MaxQueuedSequences are queued, the running one included. The next is
// dropped, counted, and told so.
func TestSequenceBacklogIsBounded(t *testing.T) {
	s := newSim(t)
	var done completions
	for range output.MaxQueuedSequences {
		s.sequence(ownerA, 0, 0, wstep(time.Second))
	}
	err := s.Arb.SequenceWithCompletion(ownerA, []keys.Step{kstep("A")}, 0, 0, done.add)
	if err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("Sequence over the backlog = %v, want a too many error", err)
	}
	done.want(t, output.TapCompletion{Dropped: true})
	if got := s.Arb.Stats().DroppedSequences; got != 1 {
		t.Errorf("DroppedSequences = %d, want 1", got)
	}
	if got := s.Arb.Queued(); got != output.MaxQueuedSequences {
		t.Errorf("Queued() = %d, want %d", got, output.MaxQueuedSequences)
	}
	// A finished one makes room.
	s.Advance(time.Second)
	s.sequence(ownerA, 0, 0, wstep(time.Millisecond))
	s.Settle()
	s.ExpectOutput("")
}

// A step whose Send failed is lost; the sequence goes on, and the notification
// says dropped.
func TestSequenceDroppedStepGoesOn(t *testing.T) {
	s := newSim(t)
	var done completions
	s.Out.FailNext(0, nil)
	err := s.Arb.SequenceWithCompletion(ownerA, []keys.Step{kstep("A"), kstep("B")}, 0, 0, done.add)
	if err == nil {
		t.Fatal("the failed Send was not reported")
	}
	s.Settle()
	if !strings.Contains(s.Output(), "+B@0 -B@0") {
		t.Errorf("output=%s: the step after the failed one was not sent", s.Output())
	}
	done.want(t, output.TapCompletion{Dropped: true})
}

// A text that cannot be sent is lost, and the sequence goes on to its next
// step, as for a tap.
func TestSequenceFailedTextGoesOn(t *testing.T) {
	s := newSim(t)
	var done completions
	s.Out.FailNext(0, nil)
	err := s.Arb.SequenceWithCompletion(ownerA, []keys.Step{tstep("ab"), kstep("B")}, 0, 5*ms, done.add)
	if err == nil {
		t.Fatal("the failed Send was not reported")
	}
	s.Settle()
	if !strings.Contains(s.Output(), "+B@5 -B@5") {
		t.Errorf("output=%s: the step after the failed text was not sent", s.Output())
	}
	done.want(t, output.TapCompletion{Dropped: true})
	if s.Arb.Queued() != 0 {
		t.Errorf("Queued() = %d", s.Arb.Queued())
	}
}

// What is wrong with a sequence is refused: nothing is sent, the error says
// what and where, and the notification says dropped and canceled.
func TestSequenceRejected(t *testing.T) {
	long := make([]keys.Step, keys.MaxSteps+1)
	for i := range long {
		long[i] = kstep("A")
	}
	tests := []struct {
		name  string
		o     output.Owner
		steps []keys.Step
		error string
	}{
		{"invalid owner", output.Owner{}, []keys.Step{kstep("A")}, "owner"},
		{"no steps", ownerA, nil, "at least one"},
		{"too many steps", ownerA, long, "at most 64"},
		{"zero step", ownerA, []keys.Step{kstep("A"), {}}, "step 1"},
		{"wait of zero", ownerA, []keys.Step{wstep(0)}, "wait"},
		{"wait too long", ownerA, []keys.Step{kstep("A"), wstep(11 * time.Second)}, "step 1"},
		{"empty text", ownerA, []keys.Step{tstep("")}, "empty"},
		{"control character", ownerA, []keys.Step{kstep("A"), tstep("a\x1b")}, "step 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSim(t)
			var done completions
			err := s.Arb.SequenceWithCompletion(tt.o, tt.steps, 0, 0, done.add)
			if err == nil || !strings.Contains(err.Error(), tt.error) {
				t.Fatalf("Sequence = %v, want an error with %q", err, tt.error)
			}
			done.want(t, output.TapCompletion{Dropped: true, Canceled: true})
			s.Advance(time.Second)
			s.ExpectOutput("")
			if s.Arb.Queued() != 0 {
				t.Errorf("Queued() = %d", s.Arb.Queued())
			}
		})
	}
}

// The steps are checked before anything is sent: an invalid later step refuses
// the whole sequence, the earlier ones included.
func TestSequenceRejectedSendsNothing(t *testing.T) {
	s := newSim(t)
	if err := s.Arb.Sequence(ownerA, []keys.Step{kstep("A"), tstep("")}, 0, 0); err == nil {
		t.Fatal("the empty text was accepted")
	}
	s.ExpectOutput("")
}

// The sequence copies its steps: the caller may change the slice.
func TestSequenceCopiesTheSteps(t *testing.T) {
	s := newSim(t)
	steps := []keys.Step{kstep("A"), wstep(10 * ms), kstep("B")}
	s.sequence(ownerA, 0, 0, steps...)
	steps[2] = kstep("C")
	s.Settle()
	if got := s.Output(); got != "+A@0 -A@0 +B@10 -B@10" {
		t.Fatalf("output=%s", got)
	}
}

// A gate that is closed drops what is asked for: nothing is sent, one is
// counted.
func TestSequenceAtAClosedGate(t *testing.T) {
	s := newSim(t)
	s.Arb.SetBlocked(true)
	var done completions
	err := s.Arb.SequenceWithCompletion(ownerA, []keys.Step{kstep("A"), tstep("abc")}, 0, 0, done.add)
	if err != nil {
		t.Fatalf("Sequence at a closed gate = %v", err)
	}
	done.want(t, output.TapCompletion{Dropped: true, Canceled: true})
	if got := s.Arb.Stats().Blocked; got != 1 {
		t.Errorf("Blocked = %d, want 1", got)
	}
	s.Arb.SetBlocked(false)
	s.Advance(time.Second)
	s.ExpectOutput("")
}

// Where a sequence can be when its owner is released.
var sequenceCancelStates = []struct {
	name  string
	steps []keys.Step
	// advance is how far the sequence has got when it is canceled, and sent what
	// it has sent by then.
	advance time.Duration
	sent    string
	// blocked is the number the closing gate counts for the rest of it.
	blocked uint64
}{
	{"in a wait", []keys.Step{kstep("A"), wstep(100 * ms), kstep("B"), tstep("xy")}, 50 * ms, "+A@0 -A@0", 1 + 2},
	{"in a text", []keys.Step{tstep("abc"), kstep("B"), tstep("xy")}, 15 * ms, typed(0, 'a'), 2 + 1 + 2},
	{"in the gap", []keys.Step{kstep("A"), kstep("B")}, 5 * ms, "+A@0 -A@0", 1},
	{"in a leading wait", []keys.Step{wstep(100 * ms), kstep("A")}, 50 * ms, "", 1},
	{"in a trailing wait", []keys.Step{kstep("A"), wstep(100 * ms)}, 50 * ms, "+A@0 -A@0", 0},
}

// Every way the owner's work is ended ends a sequence: the unsent rest is never
// sent, the notification says dropped and canceled once, and nothing is left
// down (the Sim checks that).
func TestSequenceCancel(t *testing.T) {
	cancels := []struct {
		name   string
		cancel func(*output.Arbiter) error
	}{
		{"owner", func(a *output.Arbiter) error { return a.ReleaseOwner(ownerA) }},
		{"owners", func(a *output.Arbiter) error {
			return a.ReleaseOwners(func(o output.Owner) bool { return o.Slot == ownerA.Slot })
		}},
		{"all", (*output.Arbiter).ReleaseAll},
		{"gate", func(a *output.Arbiter) error { a.SetBlocked(true); return nil }},
	}
	for _, st := range sequenceCancelStates {
		for _, c := range cancels {
			t.Run(st.name+"/"+c.name, func(t *testing.T) {
				s := newSim(t)
				var done completions
				s.sequenceDone(ownerA, 0, 20*ms, &done, st.steps...)
				s.Advance(st.advance)
				s.ExpectOutput(st.sent)
				before := s.Out.Calls()
				if err := c.cancel(s.Arb); err != nil {
					t.Fatal(err)
				}
				done.want(t, output.TapCompletion{Dropped: true, Canceled: true})
				if c.name == "gate" {
					if got := s.Arb.Stats().Blocked; got != st.blocked {
						t.Errorf("Blocked = %d, want %d", got, st.blocked)
					}
				}
				s.Advance(10 * time.Second)
				if after := s.Out.Calls(); after != before {
					t.Errorf("%d Send calls after the cancel", after-before)
				}
				done.want(t, output.TapCompletion{Dropped: true, Canceled: true})
				if s.Arb.Queued() != 0 {
					t.Errorf("Queued() = %d", s.Arb.Queued())
				}
				if _, ok := s.Arb.NextDeadline(); ok {
					t.Error("a deadline is left")
				}
			})
		}
	}
}

// Another owner's release leaves a sequence alone, and the one that is queued
// behind is canceled by its own owner's.
func TestSequenceCancelOfAnotherOwner(t *testing.T) {
	s := newSim(t)
	var doneA, doneB completions
	s.sequenceDone(ownerA, 0, 0, &doneA, kstep("A"), wstep(100*ms), kstep("B"))
	s.sequenceDone(ownerB, 0, 0, &doneB, kstep("C"))
	s.ExpectOutput("+A@0 -A@0")
	s.Advance(10 * ms)
	// The one in the queue is canceled: the running one is not.
	if err := s.Arb.ReleaseOwner(ownerB); err != nil {
		t.Fatal(err)
	}
	doneB.want(t, output.TapCompletion{Dropped: true, Canceled: true})
	doneA.want(t)
	s.Settle()
	if got := s.Output(); got != "+A@0 -A@0 +B@100 -B@100" {
		t.Fatalf("output=%s", got)
	}
	doneA.want(t, output.TapCompletion{})
}

// The queue goes on with the next sequence when the running one is canceled.
func TestSequenceCancelOfTheRunningOneStartsTheNext(t *testing.T) {
	s := newSim(t)
	var doneA, doneB completions
	s.sequenceDone(ownerA, 0, 0, &doneA, kstep("A"), wstep(100*ms), kstep("B"))
	s.sequenceDone(ownerB, 0, 0, &doneB, kstep("C"))
	s.ExpectOutput("+A@0 -A@0")
	s.Advance(10 * ms)
	if err := s.Arb.ReleaseOwner(ownerA); err != nil {
		t.Fatal(err)
	}
	doneA.want(t, output.TapCompletion{Dropped: true, Canceled: true})
	s.Settle()
	s.ExpectOutput("+C@10 -C@10")
	doneB.want(t, output.TapCompletion{})
}

// A keys step waiting for its main key, which another owner holds with a tap
// under way, is canceled with its owner too.
func TestSequenceCancelWhileWaitingForAKey(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: 20 * ms, MinGap: 20 * ms}))
	s.tap(ownerB, "A", 0)
	var done completions
	s.sequenceDone(ownerA, 0, 0, &done, kstep("A"), kstep("B"))
	s.ExpectOutput("+A@0")
	if err := s.Arb.ReleaseOwner(ownerA); err != nil {
		t.Fatal(err)
	}
	done.want(t, output.TapCompletion{Dropped: true, Canceled: true})
	s.Settle()
	for _, e := range s.Out.Records() {
		if e.Edge().Name == "B" {
			t.Fatal("canceled sequence sent B")
		}
	}
}

// A key step under way when the owner is released is released with it.
func TestSequenceCancelDuringATap(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: 30 * ms}))
	var done completions
	s.sequenceDone(ownerA, 0, 0, &done, kstep("Ctrl+A"), kstep("B"))
	s.ExpectOutput("+LCtrl@0 +A@0")
	s.Advance(10 * ms)
	if err := s.Arb.ReleaseOwner(ownerA); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("-A@10 -LCtrl@10")
	done.want(t, output.TapCompletion{Dropped: true, Canceled: true})
	s.Advance(time.Second)
	s.ExpectOutput("")
}

// A text step under way, with an Enter tap down, is released with its owner.
func TestSequenceCancelDuringATextTap(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: 30 * ms}))
	var done completions
	s.sequenceDone(ownerA, 0, 0, &done, tstep("\nabc"), kstep("B"))
	s.ExpectOutput("+Enter@0")
	s.Advance(10 * ms)
	if err := s.Arb.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	s.ExpectOutput("-Enter@10")
	done.want(t, output.TapCompletion{Dropped: true, Canceled: true})
	s.Advance(time.Second)
	s.ExpectOutput("")
}

// The gate closing counts what a sequence would still have sent: its shortcuts,
// the characters of its texts, and not its waits; a queued sequence counts all
// of its own.
func TestSequenceBlockedCount(t *testing.T) {
	s := newSim(t)
	s.sequence(ownerA, 0, 0, kstep("A"), wstep(100*ms), kstep("B"), tstep("你好!"))
	s.sequence(ownerB, 0, 0, wstep(time.Second), kstep("C"), tstep("ab"))
	s.Advance(10 * ms)
	s.Arb.SetBlocked(true)
	// B and three characters; C and two characters.
	if got := s.Arb.Stats().Blocked; got != 1+3+1+2 {
		t.Errorf("Blocked = %d, want 7", got)
	}
}

// The notification comes once, though the gate closes and the owner is released
// after it.
func TestSequenceNotifiesOnce(t *testing.T) {
	s := newSim(t)
	var done completions
	s.sequenceDone(ownerA, 0, 10*ms, &done, tstep("ab"))
	s.Advance(5 * ms)
	s.Arb.SetBlocked(true)
	if err := s.Arb.ReleaseAll(); err != nil {
		t.Fatal(err)
	}
	s.Arb.SetBlocked(false)
	s.Advance(time.Second)
	done.want(t, output.TapCompletion{Dropped: true, Canceled: true})
}

// A gate closed and opened again between steps does not revive the sequence.
func TestSequenceDoesNotSurviveTheGate(t *testing.T) {
	s := newSim(t)
	s.sequence(ownerA, 0, 0, kstep("A"), wstep(100*ms), kstep("B"))
	s.ExpectOutput("+A@0 -A@0")
	s.Arb.SetBlocked(true)
	s.Arb.SetBlocked(false)
	s.Advance(time.Second)
	s.ExpectOutput("")
}

// The event gap (OUT-17) spaces a sequence's events like any other, and the
// events keep their order: a text waits for the events of the step before it.
func TestSequenceWithEventGap(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{EventGap: 5 * ms}))
	s.sequence(ownerA, 0, 0, kstep("A"), tstep("b"), kstep("C"))
	s.Settle()
	var names []string
	var last clock.Time
	lastBatch := -1
	for _, r := range s.Out.Records() {
		names = append(names, r.Edge().Name)
		if r.Batch != lastBatch && lastBatch >= 0 && r.T-last < clock.Time(5*ms) {
			t.Errorf("batch %d at %v is less than the event gap after the one before it, at %v", r.Batch, r.T, last)
		}
		if r.Batch != lastBatch {
			last, lastBatch = r.T, r.Batch
		}
	}
	unit := output.UnicodeEvent('b', false).ID().String()
	if got, want := strings.Join(names, " "), "A A "+unit+" "+unit+" C C"; got != want {
		t.Errorf("events %s, want %s", got, want)
	}
}

// A sequence is run through the Loop (T-output) as through the Arbiter, and its
// notification arrives once.
func TestRemoteSequence(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	r := lt.l.Remote()
	done := make(chan output.TapCompletion, 2)
	steps := []keys.Step{kstep("A"), wstep(10 * ms), tstep("b")}
	if err := r.SequenceWithCompletion(ownerA, steps, 0, 0, func(c output.TapCompletion) { done <- c }); err != nil {
		t.Fatal(err)
	}
	if got := lt.rec.Trace(); got != "+A@0 -A@0" {
		t.Errorf("after the command: %s, want the first step", got)
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
	if got := lt.rec.Trace(); got != "+A@0 -A@0 "+typed(10, 'b') {
		t.Errorf("output: %s", got)
	}
	if err := r.Sequence(ownerA, nil, 0, 0); err == nil {
		t.Error("Sequence of nothing succeeded")
	}
}

// After the Loop has ended a Sequence fails with ErrLoopClosed and its
// notification says dropped and canceled, once.
func TestRemoteSequenceClosed(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	r := lt.l.Remote()
	if err := lt.stop(); err != nil {
		t.Fatal(err)
	}
	var done completions
	err := r.SequenceWithCompletion(ownerA, []keys.Step{kstep("A")}, 0, 0, done.add)
	if !errors.Is(err, output.ErrLoopClosed) {
		t.Fatalf("SequenceWithCompletion = %v, want ErrLoopClosed", err)
	}
	done.want(t, output.TapCompletion{Dropped: true, Canceled: true})
}

// Stop ends a sequence still under way, and the rest of it is not sent.
func TestLoopStopEndsSequence(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	r := lt.l.Remote()
	var done completions
	steps := []keys.Step{kstep("A"), wstep(10 * time.Second), kstep("B"), tstep("xyz")}
	if err := r.SequenceWithCompletion(ownerA, steps, 0, 0, done.add); err != nil {
		t.Fatal(err)
	}
	if err := lt.l.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := lt.rec.Trace(); got != "+A@0 -A@0" {
		t.Errorf("output: %s, want the first step only", got)
	}
	done.want(t, output.TapCompletion{Dropped: true, Canceled: true})
}

// BlockFor, which the pause and the emergency release use, ends a sequence under
// way and keeps the next from starting.
func TestLoopBlockEndsSequence(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	r := lt.l.Remote()
	var done completions
	if err := r.SequenceWithCompletion(ownerA, []keys.Step{kstep("A"), wstep(50 * ms), kstep("B")}, 0, 0, done.add); err != nil {
		t.Fatal(err)
	}
	lt.l.Block(true)
	lt.sync()
	lt.clk.Set(clock.Time(200 * ms))
	lt.sync()
	if got := lt.rec.Trace(); got != "+A@0 -A@0" {
		t.Errorf("output: %s, want the first step only", got)
	}
	done.want(t, output.TapCompletion{Dropped: true, Canceled: true})
	if err := r.Sequence(ownerA, []keys.Step{kstep("Z")}, 0, 0); err != nil {
		t.Fatal(err)
	}
	if got := lt.rec.Trace(); got != "+A@0 -A@0" {
		t.Errorf("output: %s: nothing is sent while the gate is closed", got)
	}
	lt.l.Block(false)
	lt.sync()
}

// A wait holds nothing: Held() lists no key during it.
func TestSequenceWaitHoldsNothing(t *testing.T) {
	s := newSim(t)
	s.sequence(ownerA, 0, 0, kstep("A"), wstep(time.Second))
	if h := s.Arb.Held(); len(h) != 0 {
		t.Errorf("Held() = %v during a wait", h)
	}
	s.Settle()
}
