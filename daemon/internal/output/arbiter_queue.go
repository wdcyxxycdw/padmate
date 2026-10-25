package output

import (
	"errors"
	"slices"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

type queuedTap struct {
	o      Owner
	c      keys.Chord
	main   KeyID
	hold   time.Duration
	done   func(TapCompletion)
	repeat bool
}

// tapSeq is a sequence of steps for an owner: a TapSequence's items, or the
// steps of a Sequence (arbiter_sequence.go).
type tapSeq struct {
	o         Owner
	steps     []keys.Step
	next      int // the step to start next
	hold, gap time.Duration
	// at is when the next step may start, while waiting; mark is where a wait is
	// counted from: the end of the last tap or text, and the end of each wait
	// since, so that the waits one after another add up.
	at, mark      clock.Time
	waiting, over bool
	// serial: the sequence is a Sequence's, which waits for the Sequences before it
	// (started says that its turn has come). A TapSequence's does not.
	serial, started bool
	// failed: a step was lost, to a Send that failed or a tap merged away.
	failed bool
	// done is the notification of the end of the sequence, once.
	done func(TapCompletion)
}

// Timing reports the profile's physical minimum press and same-key gap.
func (a *Arbiter) Timing() (time.Duration, time.Duration) { return a.minPress, a.minGap }

// SetTiming changes the limits for future releases. Deadlines already
// committed by a release remain unchanged, including same-key gaps. A
// release that went into the event gap's queue with a minimum before its
// press went out (SetEventGap) counts it from the press, with the limits
// then; one that went in without a minimum keeps none.
func (a *Arbiter) SetTiming(minPress, minGap time.Duration) error {
	a.minPress, a.minGap = max(minPress, 0), max(minGap, 0)
	return errors.Join(a.retryPending(), a.runReady())
}

// Queued counts taps waiting for their main key, unfinished sequences and
// unfinished texts (TypeText).
func (a *Arbiter) Queued() int { return len(a.queued) + len(a.seqs) + len(a.texts) }

// RepeatTap taps c like Tap, but StopRepeat may cancel its queued press.
func (a *Arbiter) RepeatTap(o Owner, c keys.Chord, hold time.Duration) error {
	return a.tap(o, c, hold, nil, true)
}

// StopRepeat cancels queued repeats of this owner and sided chord, even
// when the press gate is closed. A tap already started finishes normally.
func (a *Arbiter) StopRepeat(o Owner, c keys.Chord) error {
	c, err := check(o, c)
	if err != nil {
		return err
	}
	a.queued = slices.DeleteFunc(a.queued, func(q *queuedTap) bool { return q.repeat && q.o == o && q.c == c })
	return errors.Join(a.retryPending(), a.runReady())
}

// TapSequence taps items in order. Each item starts after the previous
// item's physical release or dropped completion, plus gap. Layout resolution
// happens when an item starts, including after waiting for its main key.
func (a *Arbiter) TapSequence(o Owner, items []keys.Chord, hold, gap time.Duration) error {
	if !o.IsValid() {
		return errors.New("output: invalid sequence owner")
	}
	for _, c := range items {
		if _, err := check(o, c); err != nil {
			return err
		}
	}
	if len(items) == 0 {
		return errors.New("output: empty tap sequence")
	}
	if a.blocked {
		return a.drop()
	}
	steps := make([]keys.Step, len(items))
	for i, c := range items {
		steps[i] = keys.KeysStep(c)
	}
	now := a.clk.Now()
	seq := &tapSeq{o: o, steps: steps, hold: hold, gap: max(gap, 0), waiting: true, started: true, at: now, mark: now}
	a.seqs = append(a.seqs, seq)
	return errors.Join(a.retryPending(), a.runReady())
}

func (a *Arbiter) heldByDown(id KeyID) bool {
	for _, r := range a.holds {
		if slices.Contains(r.keys, id) {
			return true
		}
	}
	return false
}

func (a *Arbiter) busy(id KeyID) bool {
	if id.momentary() {
		// A wheel key has no state for a tap to wait for: its notch goes out
		// whether the key is held, pending release or under a minimum gap
		// (ADR-0062). Only the event gap's queue bounds it, as it bounds
		// the extra presses of any key: one waits at a time.
		return len(a.outbox) > 0 && a.pressWaiting(id)
	}
	if a.heldByDown(id) {
		// A tap on a key held by Down presses it once more. With an event
		// gap one such press waits at a time (OUT-17): the next tap queues
		// behind it, and further ones merge, as on a busy key.
		return len(a.outbox) > 0 && a.extraQueued(id)
	}
	if a.keys[id] != nil {
		// A zero-reference key remains in the ledger until its physical up
		// succeeds, including after a due release and its retry both fail.
		return true
	}
	return a.released[id] > a.clk.Now()
}

func (a *Arbiter) readyAt(id KeyID) (clock.Time, bool) {
	if ks := a.keys[id]; ks != nil {
		if ks.releaseAt != 0 {
			return ks.releaseAt, true
		}
		// Active taps, failed releases and the event gap's queue already
		// contribute a deadline.
		return 0, false
	}
	at, ok := a.released[id]
	return at, ok
}

func (a *Arbiter) queueOrStart(o Owner, c keys.Chord, hold time.Duration, done func(TapCompletion), repeat bool, presses []KeyEvent) error {
	main := presses[len(presses)-1].ID()
	if !a.busy(main) {
		return a.startTap(o, hold, done, presses)
	}
	// ROADMAP OUT-10 bounds backlog by physical main key, across owners,
	// layouts, and modifiers. The earliest tap keeps its chord and position.
	if slices.ContainsFunc(a.queued, func(q *queuedTap) bool { return q.main == main }) {
		a.stats.MergedTaps++
		if done != nil {
			done(TapCompletion{Dropped: true})
		}
		return nil
	}
	a.queued = append(a.queued, &queuedTap{o: o, c: c, main: main, hold: hold, done: done, repeat: repeat})
	a.stats.QueuedTaps++
	return nil
}

// runReady never recurses: sequence callbacks only update their own state.
// Zero duration items therefore complete in order in this one owner call.
func (a *Arbiter) runReady() error {
	var errs []error
	for {
		now := a.clk.Now()
		for id, at := range a.released {
			if at <= now {
				delete(a.released, id)
			}
		}
		progress := false
		for i := 0; i < len(a.queued); {
			q := a.queued[i]
			if a.busy(q.main) {
				i++
				continue
			}
			presses, err := a.presses(q.c)
			if err != nil {
				a.queued = slices.Delete(a.queued, i, i+1)
				progress = true
				if q.done != nil {
					q.done(TapCompletion{Dropped: true})
				}
				errs = append(errs, err)
				continue
			}
			main := presses[len(presses)-1].ID()
			if a.busy(main) {
				// Preserve the arrival position when a new layout remaps a
				// waiting chord onto another busy physical key. It is still
				// the same queued request, not a second QueuedTaps event.
				q.main = main
				other := slices.IndexFunc(a.queued, func(item *queuedTap) bool { return item != q && item.main == main })
				if other >= 0 {
					progress = true
					a.stats.MergedTaps++
					drop := other
					if other < i {
						drop = i
					}
					merged := a.queued[drop]
					a.queued = slices.Delete(a.queued, drop, drop+1)
					if merged.done != nil {
						merged.done(TapCompletion{Dropped: true})
					}
					if drop == i {
						continue
					}
				}
				i++
				continue
			}
			a.queued = slices.Delete(a.queued, i, i+1)
			progress = true
			errs = append(errs, a.startTap(q.o, q.hold, q.done, presses))
		}
		seqProgress, seqErrs := a.runSeqs(now)
		progress = progress || seqProgress
		errs = append(errs, seqErrs...)
		a.seqs = slices.DeleteFunc(a.seqs, func(seq *tapSeq) bool { return seq.over })
		for _, job := range a.texts {
			if job.over || !job.waiting || job.at > now || !a.textTurn() {
				continue
			}
			progress = true
			errs = append(errs, a.typeNext(job))
		}
		a.texts = slices.DeleteFunc(a.texts, func(job *textJob) bool { return job.over })
		if !progress {
			return errors.Join(errs...)
		}
	}
}

func (a *Arbiter) cancelQueued(match func(Owner) bool, blocked bool) {
	// End the sequences first: the notifications of the taps and texts they have
	// under way, which come next, then find them over and ignore them.
	for _, seq := range a.seqs {
		if seq.over || !match(seq.o) {
			continue
		}
		if blocked {
			a.stats.Blocked += seq.unsent()
		}
		a.endSeq(seq, TapCompletion{Dropped: true, Canceled: true})
	}
	a.seqs = slices.DeleteFunc(a.seqs, func(seq *tapSeq) bool { return seq.over })
	a.cancelTexts(match, blocked)
	kept := a.queued[:0]
	for _, q := range a.queued {
		if !match(q.o) {
			kept = append(kept, q)
			continue
		}
		if blocked {
			a.stats.Blocked++
		}
		if q.done != nil {
			q.done(TapCompletion{Dropped: true, Canceled: true})
		}
	}
	clear(a.queued[len(kept):])
	a.queued = kept
}
