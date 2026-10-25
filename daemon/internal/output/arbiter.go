package output

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// DefaultRetryInterval is the RetryInterval of an Arbiter whose options
// leave it at 0.
const DefaultRetryInterval = 100 * time.Millisecond

// ArbiterOptions configures an Arbiter.
type ArbiterOptions struct {
	// Preview observes a resolved mapping command on T-output. It must only
	// publish bounded immutable data; it must not call the Arbiter or Loop.
	Preview func(keys.Chord, bool)
	// Resolve turns a chord into its stroke; only Stroke.Down is used. nil
	// means ChordStroke (US layout); a Resolver's Resolve follows the
	// foreground layout (NewResolver, OUT-04).
	Resolve func(keys.Chord) (Stroke, error)
	// RetryInterval is how long to wait before resending releases that
	// failed twice. 0 means DefaultRetryInterval.
	RetryInterval time.Duration
	// MinPress is the minimum duration of every physical press; forced
	// owner releases bypass it. MinGap separates taps of the same main key.
	MinPress time.Duration
	MinGap   time.Duration
	// EventGap is the minimum time between two key events the Arbiter
	// injects (OUT-17, config.json output.eventGapMs). 0 sends each
	// command's events in one Send call; above 0 every event goes out in
	// its own Send, at least EventGap after the previous one (SetEventGap).
	EventGap time.Duration
	// Latency receives the latency statistics of the input edges that the slots
	// mark (InputMark, DMN-15): the key of the mark and the time from its stamp to
	// the return of the first real Send of the command that carries it. It runs
	// on T-output inside sendOnce, so it must not block, allocate or call the
	// Arbiter or the Loop (latency.Recorder.Record is one). It is not called
	// when no Sender call was made (the local dry run, a gate that is closed, a
	// key that was already down), nor when the Send injected fewer events than it
	// was given. nil measures nothing.
	Latency func(key string, d time.Duration)
}

// Arbiter owns the keyboard state of the daemon (OUT-06). Owners hold
// chords with Down and Up, tap them, and are released together with
// ReleaseOwners; the Arbiter reference-counts every physical key, so a key
// goes down with its first reference and up with its last, and sends only
// the events that change a key.
//
// Commands normalize their chord with keys.Chord.Sided: "Ctrl+Z" and
// "LCtrl+Z" are one chord. A physical key is a KeyID, so "Z" and "KeyZ"
// are distinct chords on one key. A key is released with the event that
// pressed it.
//
// A wheel key (keys.WheelUp and its kin, ADR-0062) is momentary: its press
// turns the wheel one notch and its release injects nothing (KeyEvent.Silent).
// The Arbiter still counts it like any key, so that Up, Repeat, ReleaseAll
// and the ledger need no special case, with three differences. A press always
// goes out, even when another owner or the same one already holds the key:
// two overlapping holds of WheelUp turn the wheel twice. A tap never waits
// for the key to be free. And the key has no minimum press, no gap between
// taps (MinPress, MinGap) and no event gap for its release; the knob and
// repeat rates of the engine pace the notches.
//
// A mouse button (keys.MouseLeft and its kin) is held like a key: it goes down
// with its first reference and up with its last, keeps the minimum press, the
// gap between taps and the event gap, and is released on every path that
// releases a key. Unlike a key it never gets a press while it is down, since a
// second button down is a double click to the application: Repeat does nothing
// for it, and neither does a tap of a button some hold has down. A tap repeat,
// which clicks again after each release (RepeatTap), is unaffected.
//
// The logical state follows the commands, not the Send results: a press
// that failed still counts as down, so its release goes out with its last
// reference. A release batch injected only in part is resent at once; the
// releases that fail again stay pending. Advance and every later command
// resend them first, and NextDeadline says when to call Advance for them
// (CLAUDE.md invariant 1). A command rejected for its arguments (an
// invalid owner or chord, or a chord that does not resolve) sends nothing.
//
// SetBlocked closes a gate on presses (DMN-06): while it is closed, a
// Down, DownFor, Tap, TapSequence, Sequence, RepeatTap, Repeat or TypeText with a valid owner and chord presses nothing and
// holds nothing, without resolving the chord; it only resends the pending
// releases and counts in ArbiterStats.Blocked. Up, ReleaseOwners,
// ReleaseAll, the end of a tap and the retries of pending releases go on
// as ever, so the gate never keeps a key down. StopRepeat also remains
// available while the gate is closed. Closing cancels queued taps, sequence
// items and the unsent rest of texts (TypeText), without releasing a tap already
// started.
//
// Sequence runs a list of steps that tap a shortcut, wait or type a text, in one
// queue that the owner's release, the gate and the end of the loop cancel as a
// whole (MAP-22, ADR-0070). See arbiter_sequence.go.
//
// TypeText types a text with KEYEVENTF_UNICODE events, a character per Send
// (OUT-21, ADR-0070). Such an event is not held: it has no keyState and no bit
// in the held-key log, and its release is in the batch of its press. Only a
// release that failed twice is kept, as a pending release (Arbiter.stuck, in
// Held, PendingReleases and the retries). See arbiter_text.go.
//
// With an event gap (SetEventGap, OUT-17) the events of every command
// wait in one queue and go out one per Send, the gap apart; ReleaseOwners
// and ReleaseAll still send at once (pace.go).
//
// An Arbiter is not safe for concurrent use. One goroutine owns it. In the
// daemon that must be a Loop (T-output) started with LoopOptions.Lock, so
// that its Sender is called from one OS thread only (CLAUDE.md invariant
// 3). It reads time only from its clock.
type Arbiter struct {
	clk      clock.Clock
	s        Sender
	resolve  func(keys.Chord) (Stroke, error)
	retry    time.Duration
	minPress time.Duration
	minGap   time.Duration
	released map[KeyID]clock.Time
	queued   []*queuedTap
	seqs     []*tapSeq
	texts    []*textJob
	// stuck: the releases of typed Unicode units whose press went out and whose
	// release failed twice (sendBatchAll). A unit has no keyState, so these are the
	// only record that it is down; retryPending, ReleaseOwners, pending and Held go
	// through them (arbiter_text.go).
	stuck []KeyEvent

	gap      time.Duration // the event gap; 0 sends each batch at once
	outbox   []outEvent    // events waiting for the gap, in order
	one      [1]KeyEvent   // the batch of a paced Send
	lastSent clock.Time    // the last Send call, when sent
	sent     bool
	// queuing: the command under way queued a press (waitPress), so its
	// batch goes through the outbox though a take over emptied it (sendFor).
	queuing bool
	// lingering: after SetEventGap(0), the keys pressed up to lingerSeq may
	// still be in the ledger (linger).
	lingering bool
	lingerSeq uint64

	keys        map[KeyID]*keyState
	holds       map[holdID]*ref
	taps        []*ref     // pending taps by end, ties in creation order
	waiting     []*ref     // taps whose last release is delayed or needs retry
	seq         uint64     // orders presses and references
	retryAt     clock.Time // when pending releases are resent, while there are any
	blocked     bool       // the gate is closed: Down, Tap and Repeat press nothing
	localDryRun bool
	// pointerErrAt is when the Send error of a pointer event was last reported
	// (pointerError), when pointerErrSet.
	pointerErrAt  clock.Time
	pointerErrSet bool
	preview       func(keys.Chord, bool)
	// latency and mark are DMN-15: the sink of ArbiterOptions.Latency, and the input
	// mark of the command under way, which Loop.run sets for a command that carries
	// one and sendOnce spends (endMark).
	latency func(string, time.Duration)
	mark    *InputMark
	stats   ArbiterStats
}

// keyState is a physical key that is down, or up with its release not
// injected yet. Outside a command, a key with no references is pending
// release.
type keyState struct {
	press    KeyEvent // the press that put the key down; its release is sent
	injected bool     // a successful press has established the physical minimum

	// With an event gap only (pace.go), the flags, which fit in the padding
	// after the ones above; issued, users and group below grow the struct
	// from 64 to 96 bytes, also without a gap (ADR-0049 decision 7):
	waitPress   bool // the press is queued and was never sent
	waitRelease bool // a release of the key is queued
	// mayBeDown: the key may be down before the press is sent: the press
	// replaced a key whose release had not gone out, or it presses the key
	// again behind a press that went out (pressAgain) and whose release may
	// fail (sendBatch).
	mayBeDown bool

	refs      int    // holds and pending taps referencing the key
	seq       uint64 // press order
	pressedAt clock.Time
	releaseAt clock.Time
	minHold   time.Duration
	issued    clock.Time // when the command that pressed the key came

	// With an event gap only (pace.go):
	users []Owner       // the owners whose commands used the key while waitPress
	group *releaseGroup // the keys whose delayed releases go together
}

type holdID struct {
	o Owner
	c keys.Chord // sided
}

// ref is a hold or a pending tap: an owner's references to physical keys.
type ref struct {
	o        Owner
	keys     []KeyID // in press order
	seq      uint64
	end      clock.Time // when a tap releases its keys
	done     func(TapCompletion)
	minPress time.Duration // DownFor's minimum; with an event gap a tap's hold (keepHold)
	last     uint64        // a tap's last press order: newer presses of its keys are not its own
	queued   int32         // the tap's events still in the event gap's queue
	failed   bool
	// merged: a Down merged into an earlier press of its main key that
	// holds only some of its modifiers (mergeDown).
	merged bool
	// modsOnly: a Down pressed modifiers and not its main key, which was
	// down already; Up does not drop its Repeat still queued (dropRepeats).
	modsOnly bool
}

// TapCompletion reports that a tap has ended on the Arbiter's owner thread.
// Dropped is true when the output gate, an owner release, or a send failure
// prevented the tap from completing normally. Canceled distinguishes a gate
// or release from a failed Send: callers must not start a sequence's next
// item after cancellation. A callback must not run JS or call the Arbiter.
type TapCompletion struct {
	Dropped  bool
	Canceled bool
}

// HeldKey is a physical key the Arbiter has down or has yet to release.
type HeldKey struct {
	// Press is the event that pressed the key; the Arbiter releases the
	// key with its release.
	Press KeyEvent
	// Owners are the owners of the holds and pending taps that reference
	// the key, each once, in the order of their first reference.
	Owners []Owner
	// Pending reports that no one references the key any more and its
	// release failed: it is resent at NextDeadline.
	Pending bool
}

// ArbiterStats counts no-op commands and Send outcomes.
type ArbiterStats struct {
	DuplicateDowns   uint64 // Down of a chord the owner already held
	UnmatchedUps     uint64 // Up of a chord the owner did not hold
	InvalidRepeats   uint64 // Repeat of a chord the owner did not hold
	TapsOnHeld       uint64 // taps whose main key is held by Down
	QueuedTaps       uint64 // taps queued behind their physical main key
	MergedTaps       uint64 // excess taps merged into the main key's queued tap
	Blocked          uint64 // Down, Tap, Repeat, and the taps and characters of sequences and texts dropped while the gate was closed
	Typed            uint64 // characters TypeText has typed, a newline and a tab included
	DroppedSequences uint64 // Sequences dropped because MaxQueuedSequences were queued already
	SendErrors       uint64 // Send calls that injected only part of a batch
	SendWarnings     uint64 // Send calls that injected all with an error
	Retries          uint64 // Send calls that resent failed releases
	PendingReleases  int    // keys whose release is pending now
}

// NewArbiter returns an Arbiter that reads time from clk and injects
// through s. It panics if clk or s is nil.
func NewArbiter(clk clock.Clock, s Sender, opts ArbiterOptions) *Arbiter {
	if clk == nil || s == nil {
		panic("output: NewArbiter needs a clock and a sender")
	}
	a := &Arbiter{
		clk:      clk,
		s:        s,
		resolve:  opts.Resolve,
		retry:    opts.RetryInterval,
		minPress: max(opts.MinPress, 0),
		minGap:   max(opts.MinGap, 0),
		gap:      max(opts.EventGap, 0),
		released: map[KeyID]clock.Time{},
		keys:     map[KeyID]*keyState{},
		holds:    map[holdID]*ref{},
		preview:  opts.Preview,
		latency:  opts.Latency,
	}
	if a.resolve == nil {
		a.resolve = ChordStroke
	}
	if a.retry <= 0 {
		a.retry = DefaultRetryInterval
	}
	return a
}

// Down makes o hold c: it presses, in one batch and in press order, the
// keys of c that were up. Down of a chord o already holds does nothing,
// and so does Down while the gate is closed.
func (a *Arbiter) Down(o Owner, c keys.Chord) error {
	return a.DownFor(o, c, 0)
}

// DownFor is Down with a binding's minimum press duration. Every physical
// key retains the largest minimum of the references to that press.
func (a *Arbiter) DownFor(o Owner, c keys.Chord, minPress time.Duration) error {
	c, err := check(o, c)
	if err != nil {
		return err
	}
	if a.blocked {
		return a.drop()
	}
	id := holdID{o, c}
	if _, ok := a.holds[id]; ok {
		a.stats.DuplicateDowns++
		return errors.Join(a.retryPending(), a.runReady())
	}
	presses, err := a.presses(c)
	if err != nil {
		return err
	}
	err = errors.Join(a.retryPending(), a.runReady())
	if a.paced() {
		a.commitDue()
	}
	r := a.newRef(o)
	r.minPress = max(minPress, 0)
	if main := presses[len(presses)-1]; a.paced() && a.mergeDown(main.ID()) {
		for _, e := range presses[:len(presses)-1] {
			if !a.takesOver(e.ID()) {
				r.merged = true
				continue
			}
			a.take(o, e, r.minPress, false)
			r.keys = append(r.keys, e.ID())
		}
		a.take(o, main, r.minPress, true)
		r.keys = append(r.keys, main.ID())
		a.holds[id] = r
		return errors.Join(err, a.runReady())
	}
	// A wheel notch pressed while some reference holds the key is an extra
	// press, as Repeat's: with an event gap it is queued as one, so that the
	// key's own press stays the only one that tells whether the key was sent.
	extraMain := false
	if main := presses[len(presses)-1]; main.momentary() {
		ks := a.keys[main.ID()]
		extraMain = ks != nil && ks.refs > 0
	}
	var batch []KeyEvent
	for i, e := range presses {
		if a.take(o, e, r.minPress, i == len(presses)-1) {
			batch = append(batch, e)
		}
		r.keys = append(r.keys, e.ID())
	}
	r.modsOnly = len(batch) > 0 && batch[len(batch)-1].ID() != r.keys[len(r.keys)-1]
	a.holds[id] = r
	extra := -1
	if extraMain {
		extra = len(batch) - 1 // take pressed it: the main key is last
	}
	return errors.Join(err, a.sendFor(o, nil, batch, extra, nil), a.runReady())
}

// Up ends o's hold of c: it releases, in one batch and in reverse press
// order, the keys of c that no one else references. Up of a chord o does
// not hold does nothing.
func (a *Arbiter) Up(o Owner, c keys.Chord) error {
	c, err := check(o, c)
	if err != nil {
		return err
	}
	err = errors.Join(a.retryPending(), a.runReady())
	id := holdID{o, c}
	r, ok := a.holds[id]
	if !ok {
		a.stats.UnmatchedUps++
		return err
	}
	delete(a.holds, id)
	if !r.modsOnly {
		a.dropRepeats(r)
	}
	return errors.Join(err, a.send(o, a.unref(r.keys)), a.runReady())
}

// Tap presses c and releases it after hold. A main key held by Down gets
// one extra press and no release, a mouse button none at all; a key occupied
// by a tap or its minimum gap queues instead. There is at most one queued tap
// per physical main key; further requests merge. A modifier already down gets
// no event.
//
// With hold <= 0 the presses and releases go out in one batch. With hold >
// 0 the presses go out now and Advance releases them at now+hold, unless a
// ReleaseOwners of o comes first. While the gate is closed Tap does
// nothing.
func (a *Arbiter) Tap(o Owner, c keys.Chord, hold time.Duration) error {
	return a.TapWithCompletion(o, c, hold, nil)
}

// TapWithCompletion is Tap with one terminal notification. The callback runs
// after the final Send or cancellation, on the Arbiter's owner thread.
func (a *Arbiter) TapWithCompletion(o Owner, c keys.Chord, hold time.Duration, done func(TapCompletion)) error {
	return a.tap(o, c, hold, done, false)
}

func (a *Arbiter) tap(o Owner, c keys.Chord, hold time.Duration, done func(TapCompletion), repeat bool) error {
	c, err := check(o, c)
	if err != nil {
		if done != nil {
			done(TapCompletion{Dropped: true, Canceled: true})
		}
		return err
	}
	if a.blocked {
		if done != nil {
			done(TapCompletion{Dropped: true, Canceled: true})
		}
		return a.drop()
	}
	presses, err := a.presses(c)
	if err != nil {
		if done != nil {
			done(TapCompletion{Dropped: true})
		}
		return err
	}
	err = errors.Join(a.retryPending(), a.runReady())
	err = errors.Join(err, a.queueOrStart(o, c, hold, done, repeat, presses))
	return errors.Join(err, a.runReady())
}

func (a *Arbiter) startTap(o Owner, hold time.Duration, done func(TapCompletion), presses []KeyEvent) error {
	r := a.newRef(o)
	r.done = done
	// With an event gap the hold counts from the Send of each press, as
	// MinPress does: once the tap ends, it is the minimum of the keys the
	// tap presses (keepHold, OUT-17).
	if a.paced() {
		r.minPress = max(hold, 0)
		a.commitDue()
	}
	var batch []KeyEvent
	extra := -1
	// A button that is down gets no second press, which the application would
	// take for a double click, so the tap of a chord that ends in one does nothing
	// at all: its modifiers are not pressed either (a lone Shift toggles the
	// Chinese IME). The tap still completes, and counts as a tap on a held key.
	main := presses[len(presses)-1].ID()
	onHeldButton := main.button() && a.keys[main] != nil && a.keys[main].refs > 0
	for i, e := range presses {
		if onHeldButton && i < len(presses)-1 {
			continue
		}
		if ks := a.keys[e.ID()]; ks != nil && ks.refs > 0 {
			ks.usedBy(o)
			if i == len(presses)-1 {
				a.stats.TapsOnHeld++
				// The button that is down gets nothing (onHeldButton); a key
				// gets an extra press.
				if !onHeldButton {
					extra = len(batch)
					batch = append(batch, ks.press)
				}
			}
			continue
		}
		if a.take(o, e, 0, false) {
			batch = append(batch, e)
		}
		r.keys = append(r.keys, e.ID())
	}
	r.last = a.seq
	if hold <= 0 || len(r.keys) == 0 {
		batch = append(batch, a.unref(r.keys)...)
	} else {
		r.end = a.clk.Now().Add(hold)
		// After the taps that end at the same time.
		i := slices.IndexFunc(a.taps, func(t *ref) bool { return t.end > r.end })
		if i < 0 {
			i = len(a.taps)
		}
		a.taps = slices.Insert(a.taps, i, r)
	}
	sendErr := a.sendFor(o, r, batch, extra, nil)
	if hold <= 0 || len(r.keys) == 0 {
		a.settleTap(r)
	}
	return sendErr
}

func (a *Arbiter) completeTap(r *ref, canceled bool) {
	if r.done != nil {
		r.done(TapCompletion{Dropped: canceled || r.failed, Canceled: canceled})
		r.done = nil
	}
}

// settleTap waits for every unreferenced key to leave the ledger, and with
// an event gap for the tap's queued events to go out (tapWaits). MinPress
// and failed releases therefore cannot falsely report normal completion.
func (a *Arbiter) settleTap(r *ref) {
	if r.done == nil {
		return
	}
	if a.tapWaits(r) {
		a.waiting = append(a.waiting, r)
		return
	}
	a.completeTap(r, false)
}

func (a *Arbiter) flushTapCompletions() {
	remaining := a.waiting[:0]
	for _, r := range a.waiting {
		if a.tapWaits(r) {
			remaining = append(remaining, r)
		} else {
			a.completeTap(r, false)
		}
	}
	clear(a.waiting[len(remaining):])
	a.waiting = remaining
}

// Repeat sends one more press of the main key of c, as typematic repeat
// would, while o holds c and the gate is open. Otherwise it does nothing.
// With an event gap it also does nothing while an extra press of the key
// waits for the gap, so that the queue does not grow when Repeat comes
// faster than the gap, or for a Down merged into an earlier press of its
// main key, which does not hold its modifiers (mergeDown); Up drops o's
// repeat still queued, unless the Down pressed only modifiers, its main key
// down already, which would then go down and up alone (OUT-17). It also does
// nothing for a chord whose main key is a mouse button: a button that is down
// is never pressed again, whatever the profile says (it rejects such a repeat,
// so this is defense in depth).
func (a *Arbiter) Repeat(o Owner, c keys.Chord) error {
	c, err := check(o, c)
	if err != nil {
		return err
	}
	if a.blocked {
		return a.drop()
	}
	err = errors.Join(a.retryPending(), a.runReady())
	r, ok := a.holds[holdID{o, c}]
	if !ok {
		a.stats.InvalidRepeats++
		return err
	}
	main := r.keys[len(r.keys)-1]
	ks := a.keys[main]
	if main.button() || r.merged || len(a.outbox) > 0 && a.extraQueued(ks.press.ID()) {
		return errors.Join(err, a.runReady())
	}
	if a.paced() {
		a.commitDue()
	}
	return errors.Join(err, a.sendFor(o, nil, []KeyEvent{ks.press}, 0, r), a.runReady())
}

// ReleaseOwners ends the holds and pending taps of every owner match
// accepts. It releases every key left without a reference, together with
// the pending releases, in one batch: non-modifiers first, then
// modifiers, each in reverse press order.
//
// It is the cancel path: the owners' pending taps end at once, not at
// their hold. An owner ending a press cycle normally ends its holds with
// Up and lets its taps run.
//
// With an event gap the batch goes out at once all the same, after the
// queued events in their order; the presses only the released owners
// used and never sent are dropped with their releases (takeOutbox), and
// the mask key keeps the modifiers they leave alone from making a hotkey
// (forcedBatch).
func (a *Arbiter) ReleaseOwners(match func(Owner) bool) error {
	if match == nil {
		return errors.New("output: ReleaseOwners needs a non-nil match")
	}
	a.cancelQueued(match, false)
	retried := a.retryPendingCount() > 0
	for id, r := range a.holds {
		if match(id.o) {
			delete(a.holds, id)
			a.unrefForce(r.keys)
		}
	}
	taps := a.taps[:0]
	var canceled []*ref
	for _, r := range a.taps {
		if match(r.o) {
			a.unrefForce(r.keys)
			canceled = append(canceled, r)
		} else {
			taps = append(taps, r)
		}
	}
	clear(a.taps[len(taps):])
	a.taps = taps
	waiting := a.waiting[:0]
	for _, r := range a.waiting {
		if match(r.o) {
			canceled = append(canceled, r)
		} else {
			waiting = append(waiting, r)
		}
	}
	clear(a.waiting[len(waiting):])
	a.waiting = waiting
	var kept []outEvent
	mask := false
	if len(a.outbox) > 0 {
		kept, mask = a.takeOutbox(match)
	}
	batch := a.unreferencedForce()
	if len(kept) > 0 || mask {
		batch = a.forcedBatch(kept, batch, mask)
	}
	if len(a.stuck) > 0 {
		// A unit has no owner: its release goes whoever is released. A copy, since
		// the Send takes the units that went out off a.stuck (unstickUnit).
		batch = append(slices.Clone(a.stuck), batch...)
	}
	if retried && len(batch) > 0 {
		a.stats.Retries++
	}
	err := a.sendBatch(batch)
	a.settleQueued(kept, err != nil)
	for _, r := range canceled {
		a.completeTap(r, true)
	}
	return errors.Join(err, a.runReady())
}

// ReleaseOwner ends the holds and pending taps of o, like ReleaseOwners
// with an exact match.
func (a *Arbiter) ReleaseOwner(o Owner) error {
	if !o.IsValid() {
		return fmt.Errorf("output: invalid owner %v", o)
	}
	return a.ReleaseOwners(func(x Owner) bool { return x == o })
}

// ReleaseAll ends every hold and pending tap and resends the pending
// releases, in one batch. Afterwards nothing references any key; keys
// whose release fails again stay pending.
func (a *Arbiter) ReleaseAll() error {
	return a.ReleaseOwners(func(Owner) bool { return true })
}

// Advance runs the work that is due at the clock's Now: it resends the
// pending releases, sends the next queued event when the event gap allows,
// then ends the due taps in deadline order, ties in creation order, each
// tap's releases in their own batch. With an event gap it goes on until
// nothing is due: a press it sent may end the wait of delayed releases.
func (a *Arbiter) Advance() error {
	errs := []error{a.retryPending()}
	for {
		if len(a.outbox) > 0 {
			errs = append(errs, a.flush())
		}
		now := a.clk.Now()
		errs = append(errs, a.send(Owner{}, a.releaseDue(now)))
		for len(a.taps) > 0 && a.taps[0].end <= now {
			r := a.taps[0]
			a.taps[0] = nil
			a.taps = a.taps[1:]
			a.keepHold(r)
			errs = append(errs, a.sendFor(r.o, r, a.unref(r.keys), -1, nil))
			a.settleTap(r)
		}
		errs = append(errs, a.runReady())
		if !a.paced() {
			break
		}
		if d, ok := a.NextDeadline(); !ok || d > a.clk.Now() {
			break
		}
	}
	return errors.Join(errs...)
}

// NextDeadline returns when Advance next has work: the end of the first
// pending tap, or the retry time of pending releases, whichever is
// earlier. It returns false when there is none.
func (a *Arbiter) NextDeadline() (clock.Time, bool) {
	var d clock.Time
	ok := false
	if len(a.taps) > 0 {
		d, ok = a.taps[0].end, true
	}
	if a.retryPendingCount() > 0 && a.retryAt != 0 && (!ok || a.retryAt < d) {
		d, ok = a.retryAt, true
	}
	if len(a.outbox) > 0 {
		// A release that must keep its minimum holds the queue back.
		at := a.outbox[0].at
		if !a.outbox[0].e.gapless() { // a wheel release and a pointer move take no gap
			at = max(a.lastSent.Add(a.gap), at)
		}
		if !ok || at < d {
			d, ok = at, true
		}
	}
	for _, ks := range a.keys {
		if ks.releaseAt != 0 && (!ok || ks.releaseAt < d) {
			d, ok = ks.releaseAt, true
		}
	}
	for _, q := range a.queued {
		if at, ready := a.readyAt(q.main); ready && (!ok || at < d) {
			d, ok = at, true
		}
	}
	for _, seq := range a.seqs {
		if !seq.over && seq.waiting && (!ok || seq.at < d) {
			d, ok = seq.at, true
		}
	}
	if at, ready := a.textDeadline(); ready && (!ok || at < d) {
		d, ok = at, true
	}
	return d, ok
}

// Held returns every physical key that is down or pending release, in
// press order; with an event gap also the keys whose press is queued.
func (a *Arbiter) Held() []HeldKey {
	refs := make([]*ref, 0, len(a.holds)+len(a.taps))
	for _, r := range a.holds {
		refs = append(refs, r)
	}
	refs = append(refs, a.taps...)
	slices.SortFunc(refs, func(x, y *ref) int { return cmp.Compare(x.seq, y.seq) })
	owners := map[KeyID][]Owner{}
	for _, r := range refs {
		for _, id := range r.keys {
			if !slices.Contains(owners[id], r.o) {
				owners[id] = append(owners[id], r.o)
			}
		}
	}
	held := make([]HeldKey, 0, len(a.keys))
	for _, id := range a.byPress() {
		ks := a.keys[id]
		held = append(held, HeldKey{Press: ks.press, Owners: owners[id], Pending: ks.refs == 0})
	}
	for _, up := range a.stuck {
		press := up
		press.Up = false
		held = append(held, HeldKey{Press: press, Pending: true})
	}
	return held
}

// SetBlocked closes the gate on presses, or with on false opens it. It
// sends nothing and releases nothing: keys already down stay down until
// their Up, ReleaseOwners, ReleaseAll or the end of their tap, and events
// the event gap holds still go out. Loop.BlockFor runs ReleaseAll first.
func (a *Arbiter) SetBlocked(on bool) {
	a.blocked = on
	if on {
		a.cancelQueued(func(Owner) bool { return true }, true)
	}
}

// Blocked reports whether the gate on presses is closed.
func (a *Arbiter) Blocked() bool { return a.blocked }

// drop counts a Down, Tap or Repeat the closed gate drops and resends the
// pending releases, as every valid command does.
func (a *Arbiter) drop() error {
	a.stats.Blocked++
	return a.retryPending()
}

// Stats returns the counters since NewArbiter.
func (a *Arbiter) Stats() ArbiterStats {
	st := a.stats
	st.PendingReleases = a.pending()
	return st
}

// check validates a command's owner and chord and returns the chord sided.
func check(o Owner, c keys.Chord) (keys.Chord, error) {
	if !o.IsValid() {
		return keys.Chord{}, fmt.Errorf("output: invalid owner %v", o)
	}
	if !c.IsValid() {
		return keys.Chord{}, errors.New("output: invalid chord")
	}
	return c.Sided(), nil
}

// presses resolves c to the presses of its physical keys, each once, in
// press order.
func (a *Arbiter) presses(c keys.Chord) ([]KeyEvent, error) {
	st, err := a.resolve(c)
	if err != nil {
		return nil, fmt.Errorf("output: resolving %v: %w", c, err)
	}
	presses := make([]KeyEvent, 0, len(st.Down))
	for _, e := range st.Down {
		e.Up = false
		if !slices.ContainsFunc(presses, e.sameKey) {
			presses = append(presses, e)
		}
	}
	if len(presses) == 0 {
		return nil, fmt.Errorf("output: %v resolves to no keys", c)
	}
	if a.preview != nil {
		a.preview(c, a.localDryRun)
	}
	return presses, nil
}

func (a *Arbiter) newRef(o Owner) *ref {
	a.seq++
	return &ref{o: o, seq: a.seq}
}

// take adds a reference to the key of e for o and reports whether the key
// goes down with it: it was up, or only pending release. Then e becomes
// the key's press and a pending release is dropped; with an event gap the
// key may go up and down again instead (again, pressAgain, OUT-17). main
// is the main key of a Down.
func (a *Arbiter) take(o Owner, e KeyEvent, minPress time.Duration, main bool) bool {
	id := e.ID()
	if ks := a.keys[id]; ks != nil {
		if ks.refs > 0 {
			ks.minHold = max(ks.minHold, minPress)
			ks.refs++
			ks.usedBy(o)
			// A wheel notch is the whole action of its press, so it goes out
			// whoever else references the key: two overlapping holds of
			// WheelUp turn the wheel twice (ADR-0062).
			return e.momentary()
		}
		// The release of a wheel key injects nothing, so there is none to
		// take over or to press again behind: its press starts afresh.
		if a.paced() && !e.momentary() && a.again(ks, main) {
			a.pressAgain(o, ks, e, minPress)
			return true
		}
		// DownFor and tap modifiers may take over a deliberately delayed
		// release, or with an event gap a release still queued. A tap main
		// key queues instead.
		if !e.momentary() && (ks.releaseAt > a.clk.Now() || a.queuedFor(id)) {
			ks.minHold = max(ks.minHold, minPress)
			if ks.waitRelease {
				a.unqueueRelease(id)
			}
			ks.refs, ks.releaseAt, ks.group = 1, 0, nil
			ks.usedBy(o)
			// A tap waiting for MinPress is complete once another owner takes
			// the reference. No Send batch or deadline follows this path.
			a.flushTapCompletions()
			return false
		}
	}
	a.seq++
	now := a.clk.Now()
	ks := &keyState{press: e, refs: 1, seq: a.seq, pressedAt: now, minHold: minPress, issued: now}
	if a.paced() {
		ks.users, ks.waitPress, ks.mayBeDown = []Owner{o}, true, a.keys[id] != nil
		a.queuing = true
	}
	a.keys[id] = ks
	return true
}

// unref drops one reference to each of ids and returns the releases of
// the keys left without one, in reverse order of ids.
func (a *Arbiter) unref(ids []KeyID) []KeyEvent {
	return a.unrefAt(ids, false)
}

func (a *Arbiter) unrefForce(ids []KeyID) []KeyEvent { return a.unrefAt(ids, true) }

func (a *Arbiter) unrefAt(ids []KeyID, force bool) []KeyEvent {
	if a.paced() && !force {
		return a.unrefPaced(ids)
	}
	var ups []KeyEvent
	for _, id := range slices.Backward(ids) {
		ks := a.keys[id]
		ks.refs--
		if ks.refs == 0 {
			// A wheel key keeps no minimum: its release injects nothing.
			if !force && max(a.minPress, ks.minHold) > 0 && !id.momentary() {
				at := ks.pressedAt.Add(max(a.minPress, ks.minHold))
				if at > a.clk.Now() {
					ks.releaseAt = at
					continue
				}
			}
			ks.releaseAt = 0
			ups = append(ups, ks.press.release())
		}
	}
	return ups
}

func (a *Arbiter) unreferencedForce() []KeyEvent { return a.unreferencedAt(true) }

func (a *Arbiter) unreferencedAt(force bool) []KeyEvent {
	var mods, ups []KeyEvent
	for _, id := range slices.Backward(a.byPress()) {
		ks := a.keys[id]
		switch {
		case ks.refs > 0 || (!force && ks.releaseAt != 0 && ks.releaseAt > a.clk.Now()):
		case force:
			ks.releaseAt, ks.group = 0, nil
			if modifierIDs[id] {
				mods = append(mods, ks.press.release())
			} else {
				ups = append(ups, ks.press.release())
			}
		case modifierIDs[id]:
			mods = append(mods, ks.press.release())
		default:
			ups = append(ups, ks.press.release())
		}
	}
	return append(ups, mods...)
}

// releaseDue returns the releases whose deadline has come. A deadline set
// with an event gap (a releaseGroup, or any while paced) is the one without
// a gap (zeroDeadline): such a release goes into the queue now instead,
// keeping its minimum from its press's Send there (commit).
func (a *Arbiter) releaseDue(now clock.Time) []KeyEvent {
	var mods, ups []KeyEvent
	var due []*keyState
	for _, id := range slices.Backward(a.byPress()) {
		ks := a.keys[id]
		if ks.refs != 0 || ks.releaseAt == 0 || ks.releaseAt > now {
			continue
		}
		if ks.group != nil || a.paced() {
			due = append(due, ks)
			continue
		}
		ks.releaseAt = 0
		if modifierIDs[id] {
			mods = append(mods, ks.press.release())
		} else {
			ups = append(ups, ks.press.release())
		}
	}
	if len(due) > 0 {
		a.commit(due)
	}
	return append(ups, mods...)
}

// pending counts the keys without a reference. Outside a command their
// release is pending.
func (a *Arbiter) pending() int {
	n := len(a.stuck)
	for _, ks := range a.keys {
		if ks.refs == 0 {
			n++
		}
	}
	return n
}

// byPress returns the keys in press order.
func (a *Arbiter) byPress() []KeyID {
	ids := make([]KeyID, 0, len(a.keys))
	for id := range a.keys {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(x, y KeyID) int { return cmp.Compare(a.keys[x].seq, a.keys[y].seq) })
	return ids
}

// retryPending resends the pending releases in one batch. The ones that
// fail again stay pending until RetryInterval from now.
func (a *Arbiter) retryPending() error {
	// A releaseAt deadline is a deliberate MinPress delay, not a failed
	// Send, and a queued release waits for the event gap. Only keys with
	// neither are pending retries.
	var mods, ups []KeyEvent
	for _, id := range slices.Backward(a.byPress()) {
		ks := a.keys[id]
		if ks.retrying() {
			if modifierIDs[id] {
				mods = append(mods, ks.press.release())
			} else {
				ups = append(ups, ks.press.release())
			}
		}
	}
	ups = append(ups, a.stuck...) // a copy: the Send takes the ones that went out off a.stuck
	ups = append(ups, mods...)
	if len(ups) == 0 {
		return nil
	}
	a.stats.Retries++
	n, err := a.sendOnce(ups)
	if n == len(ups) {
		return err
	}
	a.retryAt = a.clk.Now().Add(a.retry)
	return fmt.Errorf("output: releasing %s failed again, the keys may be held: %w", names(ups[n:]), err)
}

func (a *Arbiter) retryPendingCount() int {
	n := len(a.stuck)
	for _, ks := range a.keys {
		if ks.retrying() {
			n++
		}
	}
	return n
}

// retrying reports whether the key's release is pending retry: no one
// references it, and its release is neither delayed nor queued.
func (ks *keyState) retrying() bool {
	return ks.refs == 0 && ks.releaseAt == 0 && !ks.waitRelease && !ks.waitPress
}

// send sends batch as o's command's events: at once, or queued behind the
// event gap (sendFor).
func (a *Arbiter) send(o Owner, batch []KeyEvent) error { return a.sendFor(o, nil, batch, -1, nil) }

// sendBatch sends batch in one Send call. Releases the call left out are
// resent once at once; the ones that fail again become pending, to be
// retried RetryInterval from now. Presses that failed are not resent:
// their keys count as down. The error of a batch of pointer events alone is
// reported at a limited rate (pointerError): a stick sends 125 a second.
func (a *Arbiter) sendBatch(batch []KeyEvent) error {
	err := a.sendBatchAll(batch)
	if err != nil && onlyPointer(batch) {
		return a.pointerError(err)
	}
	return err
}

func (a *Arbiter) sendBatchAll(batch []KeyEvent) error {
	n, err := a.sendOnce(batch)
	if n == len(batch) {
		return err
	}
	err = partial(n, len(batch), err)
	var ups []KeyEvent
	for _, e := range batch[n:] {
		if e.Up {
			ups = append(ups, e)
		}
	}
	if len(ups) == 0 {
		return err
	}
	a.stats.Retries++
	m, rerr := a.sendOnce(ups)
	if m == len(ups) {
		return errors.Join(err, rerr)
	}
	for _, e := range ups[m:] {
		// The release of a press the key was pressed again behind (OUT-17)
		// is not pending: the key's new press holds it. The key may be down
		// until that press's release, which a forced release must send.
		if ks := a.keys[e.ID()]; ks != nil && (ks.refs > 0 || ks.waitPress) {
			ks.mayBeDown = true
		}
		if e.Unicode {
			a.stickUnit(e) // no keyState holds it: this is the only record
		}
	}
	a.retryAt = a.clk.Now().Add(a.retry)
	return errors.Join(err, fmt.Errorf("output: releasing %s failed twice, the keys may be held: %w", names(ups[m:]), rerr))
}

// sendOnce makes one Send call, counts its outcome and forgets the keys
// whose release it injected. It makes no call for an empty batch.
func (a *Arbiter) sendOnce(batch []KeyEvent) (int, error) {
	if len(batch) == 0 {
		return 0, nil
	}
	sentAt := a.clk.Now()
	// Wheel releases inject nothing and pointer moves are not paced, so neither
	// starts an event gap (pace.go).
	if slices.ContainsFunc(batch, func(e KeyEvent) bool { return !e.gapless() }) {
		a.lastSent, a.sent = sentAt, true
	}
	n, err := len(batch), error(nil)
	if !a.localDryRun {
		n, err = send(a.s, batch)
		a.endMark(n == len(batch))
	}
	if len(a.stuck) > 0 {
		for _, e := range batch[:n] {
			if e.Unicode && e.Up {
				a.unstickUnit(e)
			}
		}
	}
	if n < len(batch) {
		a.markFailedTapReleases(batch[n:])
	} else if err != nil {
		a.markFailedTapReleases(batch)
	}
	switch {
	case n < len(batch):
		a.stats.SendErrors++
	case err != nil:
		a.stats.SendWarnings++
	}
	for i, e := range batch[:n] {
		ks := a.keys[e.ID()]
		switch {
		case ks == nil:
		case !e.Up:
			if !ks.injected {
				ks.injected, ks.pressedAt = true, sentAt
				// With an event gap (a releaseGroup) the deadline is the one
				// without a gap, and the release keeps its minimum in the
				// queue (keepMinimum).
				if ks.releaseAt != 0 && ks.group == nil {
					ks.releaseAt = max(ks.releaseAt, sentAt.Add(max(a.minPress, ks.minHold)))
				}
			}
			// A release of the key later in this batch is this press's own; a
			// queued one is then a later press's (SetEventGap(0) drains both).
			if len(a.outbox) > 0 && !slices.ContainsFunc(batch[i+1:], func(x KeyEvent) bool { return x.Up && x.ID() == e.ID() }) {
				a.pressSent(e.ID(), sentAt)
			}
		case ks.refs > 0 || ks.waitPress || slices.ContainsFunc(batch[i+1:], e.sameKey) ||
			len(a.outbox) > 0 && a.queuedFor(e.ID()):
			// The release of a key pressed again behind it (take, OUT-17):
			// the press after it starts the physical minimum anew. Its keyState
			// stays while any later event of the key is to go out, also one
			// this Send failed to inject or an extra press still queued: the
			// key goes down again with it and must still go up.
			ks.injected = false
		default:
			delete(a.keys, e.ID())
			if a.minGap > 0 && !e.momentary() { // a wheel key keeps no gap
				a.released[e.ID()] = a.clk.Now().Add(a.minGap)
			}
		}
	}
	a.flushTapCompletions()
	return n, err
}

func (a *Arbiter) markFailedTapReleases(events []KeyEvent) {
	if len(a.waiting) == 0 {
		return
	}
	for _, e := range events {
		if !e.Up {
			continue
		}
		id := e.ID()
		for _, r := range a.waiting {
			if slices.Contains(r.keys, id) {
				r.failed = true
			}
		}
	}
}

// names lists the keys of events for error messages.
func names(events []KeyEvent) string {
	s := make([]string, len(events))
	for i, e := range events {
		s[i] = e.name()
	}
	return strings.Join(s, ", ")
}

// modifierIDs are the physical keys of the modifiers on the US layout.
var modifierIDs = func() map[KeyID]bool {
	m := map[KeyID]bool{}
	for _, k := range keys.Table() {
		if !k.IsModifier() {
			continue
		}
		if e, err := KeyEventFor(k, false); err == nil {
			m[e.ID()] = true
		}
	}
	return m
}()
