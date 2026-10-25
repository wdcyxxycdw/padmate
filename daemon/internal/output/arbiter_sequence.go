package output

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// Sequences (MAP-22, ADR-0070). A sequence is a list of steps, each of which
// taps a shortcut, waits or types a text (keys.Step). One queue of the Arbiter
// runs the whole of it, so that the owner's release (ReleaseOwners, so
// ReleaseAll, the pause, a reload and a change of profile), the closing of the
// gate (SetBlocked) and the end of the loop cancel all of it: the steps not
// started are never sent, a tap or a text under way ends as it does when
// canceled alone, and no step holds a key past its own end (invariant 1). A step
// is a tap, never a press: a sequence leaves nothing down.
//
// The steps run in order, one at a time:
//   - A keys step is a tap, as an item of a TapSequence is: it presses for hold
//     (and the minimum press), waits for its main key if another tap has it, and
//     resolves the layout when it starts. When it has been released, the next
//     step comes a gap later.
//   - A wait counts from the release of the step before it (from the start of the
//     sequence, if it is first), not from the gap after it: a wait of 100 ms
//     between two shortcuts puts them 100 ms apart, however short the gap. The gap
//     is the least time between two steps, so a wait shorter than it changes
//     nothing. Waits one after another add up.
//   - A text step types its text a character at a time, a gap apart (TypeText,
//     arbiter_text.go), and the next step comes a gap after the last character.
//
// A Sequence waits for the Sequences before it, whoever they belong to, and
// starts when they are over: a second trigger of a binding runs after the first
// and not among its steps, which would garble a text. A TapSequence (a list of
// shortcuts alone, the older form) does not, and is not held back by one. At most
// MaxQueuedSequences are queued, the running one included; the next is dropped
// (DroppedSequences). Waiting for its turn costs a sequence nothing: its first
// wait, if any, counts from the start of its own run.

// MaxQueuedSequences is the most sequences the Arbiter queues, the running one
// included. Pressing a button that types a long text faster than it is typed
// would otherwise queue minutes of typing that only a pause could stop.
const MaxQueuedSequences = 16

// Sequence runs steps for o: see the comment of this file. hold is the press
// time of a keys step and gap the least time between two steps, and between the
// characters of a text; a negative gap is 0. The steps must pass
// keys.ValidateSteps, and are copied. Otherwise Sequence returns the error and
// sends nothing.
func (a *Arbiter) Sequence(o Owner, steps []keys.Step, hold, gap time.Duration) error {
	return a.SequenceWithCompletion(o, steps, hold, gap, nil)
}

// SequenceWithCompletion is Sequence with one terminal notification, like
// TapWithCompletion's: done runs on the Arbiter's owner thread when the last step
// has ended (Dropped is then true if a step was lost to a failed Send or a merged
// tap), or when the sequence ended early (Dropped and Canceled for an owner
// release, the gate, or the end of the loop; Dropped alone when it was dropped
// because MaxQueuedSequences are queued). It also runs, with Dropped and
// Canceled, when the call is rejected, and when the gate is closed. It must not
// call the Arbiter.
func (a *Arbiter) SequenceWithCompletion(o Owner, steps []keys.Step, hold, gap time.Duration, done func(TapCompletion)) error {
	refused := func() {
		if done != nil {
			done(TapCompletion{Dropped: true, Canceled: true})
		}
	}
	if !o.IsValid() {
		refused()
		return fmt.Errorf("output: invalid owner %v", o)
	}
	if err := keys.ValidateSteps(steps); err != nil {
		refused()
		return fmt.Errorf("output: %w", err)
	}
	if a.blocked {
		refused()
		return a.drop()
	}
	if a.sequencesQueued() >= MaxQueuedSequences {
		a.stats.DroppedSequences++
		if done != nil {
			done(TapCompletion{Dropped: true})
		}
		return fmt.Errorf("output: too many sequences queued (%d): dropped", MaxQueuedSequences)
	}
	seq := &tapSeq{o: o, steps: slices.Clone(steps), hold: hold, gap: max(gap, 0), serial: true, done: done}
	a.seqs = append(a.seqs, seq)
	return errors.Join(a.retryPending(), a.runReady())
}

// sequencesQueued counts the Sequences not over, the running one included.
func (a *Arbiter) sequencesQueued() int {
	n := 0
	for _, seq := range a.seqs {
		if seq.serial && !seq.over {
			n++
		}
	}
	return n
}

// runSeqs starts what the sequences have due: the next step of each, in the
// order they were asked for. It never recurses: a step's completion only updates
// its sequence's state, so that steps of no duration complete in order in the one
// owner call, and reports whether it started or ended anything.
func (a *Arbiter) runSeqs(now clock.Time) (progress bool, errs []error) {
	// turn: no Sequence before this one is still under way.
	turn := true
	for _, seq := range a.seqs {
		if seq.over {
			continue
		}
		if seq.serial {
			if !turn {
				continue
			}
			turn = false
			if !seq.started {
				seq.started, seq.waiting, seq.at, seq.mark = true, true, now, now
			}
		}
		if !seq.waiting || seq.at > now {
			continue
		}
		progress = true
		if seq.next == len(seq.steps) {
			a.endSeq(seq, TapCompletion{Dropped: seq.failed})
			continue
		}
		step := seq.steps[seq.next]
		seq.next++
		switch step.Kind {
		case keys.StepWait:
			// The wait is over a duration after the last step ended, or after the
			// waits before it; the gap is the least time between two steps.
			seq.mark = seq.mark.Add(step.Wait)
			seq.at = max(seq.at, seq.mark)
		case keys.StepText:
			seq.waiting = false
			a.texts = append(a.texts, &textJob{
				o: seq.o, runes: []rune(step.Text), gap: seq.gap, at: now, waiting: true,
				done: a.seqStepEnded(seq),
			})
		default:
			errs = append(errs, a.startSeqTap(seq, step.Chord))
		}
	}
	return progress, errs
}

// startSeqTap starts the tap of a keys step of seq, or drops it when the chord
// does not resolve.
func (a *Arbiter) startSeqTap(seq *tapSeq, chord keys.Chord) error {
	c := chord.Sided()
	seq.waiting = false
	ended := a.seqStepEnded(seq)
	presses, err := a.presses(c)
	if err != nil {
		ended(TapCompletion{Dropped: true})
		return err
	}
	return a.queueOrStart(seq.o, c, seq.hold, ended, false, presses)
}

// seqStepEnded returns the function that tells seq that the tap or the text it
// started has ended. It runs on the owner thread, from a tap's settling or a
// text's end, or from a cancel; a canceled step ends the sequence, a lost one
// does not.
func (a *Arbiter) seqStepEnded(seq *tapSeq) func(TapCompletion) {
	return func(res TapCompletion) {
		switch {
		case seq.over:
		case res.Canceled:
			a.endSeq(seq, TapCompletion{Dropped: true, Canceled: true})
		default:
			seq.failed = seq.failed || res.Dropped
			now := a.clk.Now()
			seq.mark = now
			if seq.next == len(seq.steps) {
				a.endSeq(seq, TapCompletion{Dropped: seq.failed})
				return
			}
			seq.waiting, seq.at = true, now.Add(seq.gap)
		}
	}
}

// endSeq ends seq with res, once; the end of the list of sequences takes it out
// at the end of the command.
func (a *Arbiter) endSeq(seq *tapSeq, res TapCompletion) {
	if seq.over {
		return
	}
	seq.over, seq.waiting = true, false
	if done := seq.done; done != nil {
		seq.done = nil
		done(res)
	}
}

// unsent counts what the steps not yet started would send: a tap for each keys
// step and a character for each character of a text, and nothing for a wait.
// The closing gate counts it in Stats.Blocked.
func (s *tapSeq) unsent() uint64 {
	var n uint64
	for _, step := range s.steps[s.next:] {
		switch step.Kind {
		case keys.StepKeys:
			n++
		case keys.StepText:
			n += uint64(len([]rune(step.Text)))
		}
	}
	return n
}
