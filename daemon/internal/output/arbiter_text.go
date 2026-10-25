package output

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// Text input (OUT-21, ADR-0070). TypeText types a text one character at a time
// with KEYEVENTF_UNICODE, so the layout, the IME and the modifier keys padmate
// holds play no part: each UTF-16 code unit is a press and a release of
// VK_PACKET carrying the unit, and the two units of a character outside the Basic
// Multilingual Plane go back to back in one Send. A newline is an Enter tap and
// a tab a Tab tap, as ordinary keys, through the tap machinery, so they keep the
// minimum press and the same-key gap.
//
// A typed unit is not "held": its release is in the batch of its press, and no
// keyState, held-key log bit or owner is ever made for it, so in the normal case
// none of ReleaseAll, the sweep and the pause has anything to let go of. What
// stays to do is to stop: the unsent rest of a text ends with its owner
// (ReleaseOwners), with the gate (SetBlocked), and when a Send fails.
//
// The one exception is a Send that injected the press of a unit and not its
// release, and a resend of the release that failed too (sendBatchAll). Then the
// unit may be down, and nothing but the Arbiter knows it: the release goes into
// stuck, which counts in Stats.PendingReleases, gives NextDeadline the retry time,
// is resent by every command like a pending key release (retryPending), and by
// ReleaseOwners (so ReleaseAll, and so Stop, the pause and a switch of profile),
// whatever owner is released, since a unit has none. Held() lists it as pending.
// The system keeps no autorepeat for an injected key, so a unit left down is
// probably harmless, but the release is cheap and CLAUDE.md invariant 1 puts
// every key we may have left down in the same bookkeeping.

// enterChord and tabChord are the taps a newline and a tab in a text make.
var (
	enterChord = keys.MustParse("Enter")
	tabChord   = keys.MustParse("Tab")
)

// textJob is a text being typed for an owner.
type textJob struct {
	o      Owner
	runes  []rune
	next   int // the character to type next
	gap    time.Duration
	at     clock.Time // when the next character may be typed, while waiting
	done   func(TapCompletion)
	failed bool // a character was lost: its Send failed, or its tap was merged away

	// waiting: the next character is due at at; false while the tap of a
	// newline or a tab is under way.
	waiting bool
	// over: the job has ended, completed or canceled; it is dropped from the
	// Arbiter's list at the end of the command.
	over bool
}

// TypeText types text for o, one character every gap: the first at once, each
// next one gap after the previous one was typed (for a newline or a tab, after
// its tap ended). text must pass keys.ValidateText (1 to 256 UTF-16 units, no
// control character but "\n" and "\t"); otherwise TypeText returns the error and
// types nothing.
//
// A character is one Send of its own: the press and the release of its UTF-16
// unit, or of both units of a surrogate pair. The characters are typed by
// Advance, as a TapSequence's items are, at the deadline NextDeadline reports.
// With an event gap (SetEventGap) a text waits for the queued events, which keep
// their order before it, and then for the gap since the last Send; a character
// goes out in one Send all the same, since no modifier is involved.
//
// Calls from several owners interleave, one character each in turn when due;
// the same owner's second text does not wait for its first. ReleaseOwners of o
// ends the unsent rest of o's texts, and so does closing the gate (SetBlocked),
// which counts the rest in Stats.Blocked; a TypeText while the gate is closed
// types nothing and counts one. A Send that injects only a part of a character
// ends the text: its release is resent like any release (sendBatch; one that
// fails again is kept pending, see above), the error is returned, and the rest
// is dropped, since the input is blocked (secure desktop, UIPI).
func (a *Arbiter) TypeText(o Owner, text string, gap time.Duration) error {
	return a.TypeTextWithCompletion(o, text, gap, nil)
}

// TypeTextWithCompletion is TypeText with one terminal notification, like
// TapWithCompletion: done runs on the Arbiter's owner thread when the last
// character has gone out (Dropped is then true if one was lost), or when the text
// ended early (Dropped and Canceled for an owner release or the gate, Dropped
// alone for a failed Send). It also runs, with Dropped and Canceled, when the
// call is rejected. It must not call the Arbiter.
func (a *Arbiter) TypeTextWithCompletion(o Owner, text string, gap time.Duration, done func(TapCompletion)) error {
	refused := func() {
		if done != nil {
			done(TapCompletion{Dropped: true, Canceled: true})
		}
	}
	if !o.IsValid() {
		refused()
		return fmt.Errorf("output: invalid owner %v", o)
	}
	if err := keys.ValidateText(text); err != nil {
		refused()
		return fmt.Errorf("output: %w", err)
	}
	if a.blocked {
		refused()
		return a.drop()
	}
	job := &textJob{o: o, runes: []rune(text), gap: max(gap, 0), at: a.clk.Now(), waiting: true, done: done}
	a.texts = append(a.texts, job)
	return errors.Join(a.retryPending(), a.runReady())
}

// stickUnit notes that the release e of a typed unit may not have gone out.
func (a *Arbiter) stickUnit(e KeyEvent) {
	if !slices.ContainsFunc(a.stuck, e.sameKey) {
		a.stuck = append(a.stuck, e)
	}
}

// unstickUnit forgets the release e of a typed unit, which went out.
func (a *Arbiter) unstickUnit(e KeyEvent) {
	a.stuck = slices.DeleteFunc(a.stuck, e.sameKey)
}

// textTurn reports whether a text may type its next character now: the events
// the event gap holds back go first, and the gap since the last Send has
// passed.
func (a *Arbiter) textTurn() bool { return len(a.outbox) == 0 && a.gapOver() }

// typeNext types the next character of job, which is due.
func (a *Arbiter) typeNext(job *textJob) error {
	r := job.runes[job.next]
	job.next++
	if r == '\n' || r == '\t' {
		a.stats.Typed++ // the tap starts now
		return a.typeTap(job, r)
	}
	failedBefore := a.stats.SendErrors
	err := a.sendBatch(unicodeBatch(make([]KeyEvent, 0, 4), r))
	if a.stats.SendErrors != failedBefore {
		// Part of the character did not go out: the input is blocked, and the rest
		// would not get through either.
		a.endText(job, TapCompletion{Dropped: true})
		return err
	}
	a.stats.Typed++
	a.textTyped(job, false)
	return err
}

// typeTap taps Enter for a newline or Tab for a tab, and goes on when the tap
// has ended.
func (a *Arbiter) typeTap(job *textJob, r rune) error {
	c := enterChord
	if r == '\t' {
		c = tabChord
	}
	job.waiting = false
	complete := func(res TapCompletion) {
		switch {
		case job.over:
		case res.Canceled:
			a.endText(job, TapCompletion{Dropped: true, Canceled: true})
		default:
			// A tap merged into one already queued, or one whose Send failed, is a
			// character lost; the text goes on, as a TapSequence does.
			a.textTyped(job, res.Dropped)
		}
	}
	presses, err := a.presses(c)
	if err != nil {
		a.endText(job, TapCompletion{Dropped: true})
		return err
	}
	return a.queueOrStart(job.o, c, 0, complete, false, presses)
}

// textTyped ends the character of job just typed: the text is over after its
// last one, else the next is due gap later. failed notes a character that was
// lost.
func (a *Arbiter) textTyped(job *textJob, failed bool) {
	job.failed = job.failed || failed
	if job.next == len(job.runes) {
		a.endText(job, TapCompletion{Dropped: job.failed})
		return
	}
	job.waiting, job.at = true, a.clk.Now().Add(job.gap)
}

// endText ends job with res, once.
func (a *Arbiter) endText(job *textJob, res TapCompletion) {
	if job.over {
		return
	}
	job.over, job.waiting = true, false
	if done := job.done; done != nil {
		job.done = nil
		done(res)
	}
}

// cancelTexts ends the texts of the owners match accepts, without typing the
// rest. With blocked set (the gate closing) the characters not typed are counted
// in Stats.Blocked.
func (a *Arbiter) cancelTexts(match func(Owner) bool, blocked bool) {
	for _, job := range a.texts {
		if job.over || !match(job.o) {
			continue
		}
		if blocked {
			a.stats.Blocked += uint64(len(job.runes) - job.next)
		}
		a.endText(job, TapCompletion{Dropped: true, Canceled: true})
	}
	a.texts = slices.DeleteFunc(a.texts, func(job *textJob) bool { return job.over })
}

// textDeadline returns when the first text that waits can type its next
// character. A text held back by the queue of the event gap has no deadline of
// its own: the queue's decides, and the text goes on when it is empty.
func (a *Arbiter) textDeadline() (clock.Time, bool) {
	var d clock.Time
	ok := false
	if len(a.outbox) > 0 {
		return 0, false
	}
	for _, job := range a.texts {
		if job.over || !job.waiting {
			continue
		}
		at := job.at
		if a.sent {
			at = max(at, a.lastSent.Add(a.gap))
		}
		if !ok || at < d {
			d, ok = at, true
		}
	}
	return d, ok
}
