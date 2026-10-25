package output

import (
	"cmp"
	"errors"
	"slices"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

// The event gap (OUT-17, config.json output.eventGapMs): some programs
// miss a modifier that arrives in the same SendInput call as the key it
// modifies. With a gap the Arbiter keeps the events of its commands in one
// queue, the outbox, and sends them in order, one per Send, at least the
// gap after the previous Send. The queue is above the Sender, so the
// MenuMask still puts the mask key in the same Send as the release it
// protects, and the Loop's held-key log is written once per event.
//
// The logical state still follows the commands. A key whose press is
// queued (waitPress) is held as usual; MinPress and MinGap count from the
// Send that actually carried the press or the release, and so does the
// hold of a tap, the minimum of every key the tap presses; a tap completes
// only once its events went out.
//
// A release goes into the queue when it would go out without a gap: at
// once, or when the command's time plus the key's minimum has come
// (zeroDeadline; releaseDue, or commitDue before a command queues
// presses). It keeps its minimum from the Send of its press there, holding
// back the events behind it (outEvent.at, pending), so the releases go out
// in the order they would without a gap and before the presses of a chord
// the user asked for later (Ctrl+Z after Ctrl+Shift+S is not Ctrl+Shift+Z).
// The keys one reference lets go of together wait together, until the
// latest of their deadlines (releaseGroup), and go up in reverse press
// order, keys before modifiers. A modifier also waits for the keys pressed
// after it whose releases already wait (waitingAfter), and takes along into
// the queue the ones that wait when it goes in (commit).
//
// A key pressed again while its release waits goes up and down again, as
// it would without a gap: a Down's main key (pressAgain) unless the Down
// comes before the release would have gone out without a gap, where a Down
// without a gap takes over a release MinPress delays; and a modifier whose
// queued release another key's press follows, which must not go down with
// it. The new press is queued behind the release, which goes into the
// queue first when it still waits (commit). Otherwise a Down or a tap's
// modifier takes over a release still queued or waiting, as one MinPress
// delays.
//
// The queue does not grow with Repeat or taps however fast they come: a
// key has at most one extra press (Repeat, a tap on a key held by Down)
// queued. Repeat merges into it, a tap on the key waits for it like a tap
// on a busy key (busy), and Up drops the extra presses Repeat queued for
// the hold it ends, so a typematic repeat stops with the button; but for a
// Down that pressed modifiers and not its main key, down already
// (ref.modsOnly), whose modifiers would go down and up with no key between
// (Shift alone switching the input method). Nor with
// Downs: a Down whose main key already has a press queued behind another
// one of its presses (outEvent.old) merges into the last one (mergeDown),
// as excess taps of a main key merge (OUT-10): no more than two presses of
// a main key wait. What stays is a Down whose main key is a modifier, which
// does not merge: bindings of modifiers only (Ctrl+Shift, Alt) pressed one
// after another faster than the gap lets their events out still queue up,
// as long as they come, and go out after them: with a gap of 50 ms, Down
// and Up of Ctrl+Shift and of Alt by turns, a pair every 100 ms, leave
// 15 s of events in the queue after 30 s.
//
// The forced releases are not paced: ReleaseOwners and ReleaseAll, which
// every closing gate, Stop and owner release use, send at once in one
// batch, the queued events first in their order, so no press goes out
// while an earlier chord's modifier is down. A press never sent that only
// the released owners used is dropped with its key's events up to its
// next press (takeOutbox); one another owner's command used goes out, as it
// would without a gap, and so do the dropped modifier presses before it
// that would be down without a gap, so that it keeps its chord: Ctrl+Alt+X
// another owner's Down X relies on does not go out as Ctrl+X (keepChords).
// When a dropped press leaves the modifiers going up with no other key
// pressed before their first release in the batch, the mask key goes down
// and up before it, so that Windows does not take them for a hotkey of
// their own (Ctrl+Shift switching the layout, Ctrl showing the pointer).
// The panic release of the Loop uses its ledger, which knows only the
// events that were sent. SetEventGap(0) sends the queue at once, but for a
// release whose minimum has not passed and the events behind it; the
// commands go through the queue until the keys pressed with the gap went
// up (linger).

// outEvent is a key event waiting for the event gap.
type outEvent struct {
	e KeyEvent
	// extra: e presses a key that is already down, for Repeat or a tap on a
	// key held by Down; it is not the press of the key's keyState.
	extra bool
	// pending: at is not known yet; it is the Send of the key's press
	// queued before the release plus the larger of MinPress and hold
	// (pressSent).
	pending bool
	// old: e is a press of a key that was pressed again behind it
	// (pressAgain); users are the owners that used it, as keyState.users.
	old bool

	o   Owner // the owner whose command queued e; zero for a due release
	tap *ref  // the tap whose command queued e: a failed Send fails the tap
	rep *ref  // the hold whose Repeat queued e, an extra press: its Up drops e
	// seq is the press order (keyState.seq) of the press a release ends: a
	// tap waits for the releases of its presses (tapWaits).
	seq uint64
	// at: a release that went into the queue before its minimum passed
	// (keepMinimum) does not go out before at, nor do the events behind
	// it; see pending.
	at    clock.Time
	hold  time.Duration
	users []Owner
}

// releaseGroup links the keys whose delayed releases go together, while
// they wait (unrefPaced). A member is a key whose keyState still points to
// the group.
type releaseGroup struct{ keys []KeyID }

// EventGap returns the minimum time between two Send calls of key events.
func (a *Arbiter) EventGap() time.Duration { return a.gap }

// SetEventGap changes the minimum time between two Send calls of key
// events (OUT-17). A negative gap is 0. The events already queued go out
// the new gap after the last Send; with 0 they go out at once, in one
// batch, but for a release whose minimum has not passed and the events
// behind it (outEvent.at), and every later command sends its events in one
// batch again once the queue is empty and the keys pressed with the gap
// went up (linger).
func (a *Arbiter) SetEventGap(d time.Duration) error {
	if d <= 0 && a.gap > 0 {
		a.lingering, a.lingerSeq = true, a.seq
	}
	a.gap = max(d, 0)
	return errors.Join(a.retryPending(), a.flush(), a.runReady())
}

// paced reports whether commands queue their events: with an event gap,
// while the queue still holds events after SetEventGap(0), or while a key
// pressed with the gap is still in the ledger (linger).
func (a *Arbiter) paced() bool { return a.gap > 0 || len(a.outbox) > 0 || a.lingering && a.linger() }

// linger reports whether a key pressed before SetEventGap(0) is still in
// the ledger. Its press may have gone out later than its command, and the
// presses of one command at different times: its release keeps the queue's
// order and its minimum from its Send (keepMinimum), with the keys it goes
// up with (releaseGroup), and a later command queues behind it, so that its
// keys do not go down while that release waits (Ctrl+Shift+X tapped for
// 40 ms keeps its Shift down 40 ms from its late Send: a Z tapped meanwhile
// is not Shift+Z, and Ctrl does not go up before X). Without a gap every
// press would go out with its command, and the keys of one command
// together. The key is one pressed up to lingerSeq, the press order when
// the gap went to 0, not one whose Send came later than its command: with
// a clock that runs, that is every key. Once none is left, it stops
// looking.
func (a *Arbiter) linger() bool {
	for _, ks := range a.keys {
		if ks.seq <= a.lingerSeq {
			return true
		}
	}
	a.lingering = false
	return false
}

// sendFor sends batch, the events of o's command for the tap r or nil: at
// once in one Send, or with an event gap through the outbox. batch[extra],
// when extra is an index of batch, is an extra press (outEvent.extra), of
// the hold rep's Repeat when rep is not nil. A failed Send of r's events
// fails r.
func (a *Arbiter) sendFor(o Owner, r *ref, batch []KeyEvent, extra int, rep *ref) error {
	queuing := a.queuing
	a.queuing = false
	if !a.paced() && !queuing {
		err := a.sendBatch(batch)
		if err != nil && r != nil {
			r.failed = true
		}
		return err
	}
	for i, e := range batch {
		q := outEvent{e: e, o: o, tap: r, extra: i == extra}
		if q.extra {
			q.rep = rep
		}
		if ks := a.keys[e.ID()]; ks != nil && e.Up {
			ks.waitRelease, q.seq = true, ks.seq
			a.keepMinimum(&q, ks)
		}
		a.outbox = append(a.outbox, q)
		if r != nil {
			r.queued++
		}
	}
	return a.flush()
}

// gapOver reports whether the event gap since the last Send has passed.
func (a *Arbiter) gapOver() bool {
	return !a.sent || a.clk.Now() >= a.lastSent.Add(a.gap)
}

// due reports whether the queued event at index i may go out now, as far
// as its own deadline goes: a release committed early waits for at, and
// one whose press is queued before it for that press (pending).
func (a *Arbiter) due(i int) bool {
	q := a.outbox[i]
	return (q.at == 0 || q.at <= a.clk.Now()) && (i == 0 || !q.pending)
}

// flush sends the queued events the event gap allows now, each in its own
// Send; without a gap the due ones in one batch (drain).
func (a *Arbiter) flush() error {
	if a.gap == 0 {
		return a.drain()
	}
	var errs []error
	// A wheel release injects nothing, and a pointer move is not paced, so
	// neither takes a gap: it goes out as soon as it is its turn, and the next
	// event still waits for the gap since the last Send that injected a key or
	// a button (sendOnce).
	for len(a.outbox) > 0 && (a.gapOver() || a.outbox[0].e.gapless()) && a.due(0) {
		q := a.pop(1)[0]
		a.one[0] = q.e
		err := a.sendBatch(a.one[:])
		if q.left(err != nil) {
			a.flushTapCompletions()
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// left settles the tap of q, which left the outbox: sent, by a Send that
// failed when failed is true, or dropped. It reports whether the tap has
// no more queued events, so it may complete. A tap's completion waits for
// its queued events, so that a failed Send is known before it.
func (q outEvent) left(failed bool) bool {
	r := q.tap
	if r == nil {
		return false
	}
	r.failed = r.failed || failed
	r.queued--
	return r.queued == 0
}

// settleQueued settles the taps of events, which left the outbox
// together: a tap whose events all went out may complete.
func (a *Arbiter) settleQueued(events []outEvent, failed bool) {
	done := false
	for _, q := range events {
		done = q.left(failed) || done
	}
	if done {
		a.flushTapCompletions()
	}
}

// drain sends the queued events that are due at once, in one batch, and
// goes on while that makes more of them due: a release whose press failed
// has no minimum to keep.
func (a *Arbiter) drain() error {
	var errs []error
	for {
		n := 0
		for n < len(a.outbox) && a.due(n) {
			n++
		}
		if n == 0 {
			return errors.Join(errs...)
		}
		taken := a.pop(n)
		batch := make([]KeyEvent, n)
		for i, q := range taken {
			batch[i] = q.e
		}
		err := a.sendBatch(batch)
		a.settleQueued(taken, err != nil)
		errs = append(errs, err)
	}
}

// pop removes the first n queued events, which go out or are dropped, and
// returns them. A key's waitPress and waitRelease follow what is still
// queued: a key pressed again behind its release has more.
func (a *Arbiter) pop(n int) []outEvent {
	taken := slices.Clone(a.outbox[:n])
	a.outbox = slices.Delete(a.outbox, 0, n)
	for _, q := range taken {
		id := q.e.ID()
		if ks := a.keys[id]; ks != nil {
			switch {
			case q.e.Up:
				ks.waitRelease = a.releaseQueued(id)
			case !q.extra:
				ks.waitPress = a.pressQueued(id)
			}
		}
	}
	return taken
}

// takeAll empties the outbox and returns its events, to be sent at once
// or dropped.
func (a *Arbiter) takeAll() []outEvent { return a.pop(len(a.outbox)) }

// queuedFor reports whether the outbox has an event of id.
func (a *Arbiter) queuedFor(id KeyID) bool {
	return slices.ContainsFunc(a.outbox, func(q outEvent) bool { return q.e.ID() == id })
}

// releaseQueued reports whether the outbox has a release of id.
func (a *Arbiter) releaseQueued(id KeyID) bool {
	return slices.ContainsFunc(a.outbox, func(q outEvent) bool { return q.e.Up && q.e.ID() == id })
}

// pressQueued reports whether the outbox has a press of id that is not an
// extra press: the key's own, or one it was pressed again behind.
func (a *Arbiter) pressQueued(id KeyID) bool {
	return slices.ContainsFunc(a.outbox, func(q outEvent) bool { return !q.e.Up && !q.extra && q.e.ID() == id })
}

// pressWaiting reports whether the outbox has a press of id, an extra one
// or not.
func (a *Arbiter) pressWaiting(id KeyID) bool {
	return slices.ContainsFunc(a.outbox, func(q outEvent) bool { return !q.e.Up && q.e.ID() == id })
}

// extraQueued reports whether an extra press of id waits in the outbox.
func (a *Arbiter) extraQueued(id KeyID) bool {
	return slices.ContainsFunc(a.outbox, func(q outEvent) bool { return q.extra && q.e.ID() == id })
}

// lastQueued returns the index of the last queued event of id, or -1.
func (a *Arbiter) lastQueued(id KeyID) int {
	for i, q := range slices.Backward(a.outbox) {
		if q.e.ID() == id {
			return i
		}
	}
	return -1
}

// dropRepeats drops the extra presses the Repeat of the hold r queued: the
// hold ends, and its typematic repeat with it; not another hold's of the
// same key and owner, still held.
func (a *Arbiter) dropRepeats(r *ref) {
	if len(a.outbox) > 0 {
		a.outbox = slices.DeleteFunc(a.outbox, func(q outEvent) bool { return q.rep == r })
	}
}

// unqueueRelease drops the release of id that is the key's last queued
// event: the key is taken again (take). The tap whose release it drops
// may complete.
func (a *Arbiter) unqueueRelease(id KeyID) {
	if i := a.lastQueued(id); i >= 0 && a.outbox[i].e.Up {
		a.outbox[i].left(false)
		a.outbox = slices.Delete(a.outbox, i, i+1)
	}
	a.keys[id].waitRelease = a.releaseQueued(id)
}

// usedBy records that o's command relies on the key while its press is
// queued: a forced release of the other users must still send the press.
func (ks *keyState) usedBy(o Owner) {
	if ks.waitPress && !slices.Contains(ks.users, o) {
		ks.users = append(ks.users, o)
	}
}

// again reports whether a new press of ks's key, which has no reference
// and waits for its release, goes out behind that release instead of
// taking the key over (pressAgain). Any key presses again when its release
// is queued, as its last queued event, and another key's press is queued
// after it: taking it over would hold it down for that key. main is the
// main key of a Down: it presses again unless the command comes before
// the key's release would go out without a gap (zeroDeadline), as a Down
// without a gap takes over a release MinPress delays, or a press of the
// key already waits behind another one of its presses (mergeDown).
func (a *Arbiter) again(ks *keyState, main bool) bool {
	id := ks.press.ID()
	if main && a.mergeDown(id) {
		return false
	}
	last := a.lastQueued(id)
	up := last >= 0 && a.outbox[last].e.Up
	if up && slices.ContainsFunc(a.outbox[last+1:], func(q outEvent) bool { return !q.e.Up }) {
		return true
	}
	if !main || !up && ks.releaseAt == 0 && !ks.waitPress {
		return false
	}
	if a.oldQueued(id) {
		return false
	}
	return a.clk.Now() >= a.zeroDeadline(ks)
}

// oldQueued reports whether a press of id waits behind another one of its
// presses (outEvent.old).
func (a *Arbiter) oldQueued(id KeyID) bool {
	return slices.ContainsFunc(a.outbox, func(q outEvent) bool { return q.old && q.e.ID() == id })
}

// mergeDown reports whether a Down whose main key is id merges into the
// key's last press, which no one references and which waits behind another
// one of its presses still queued (outEvent.old), as excess taps of a main
// key merge (OUT-10): it takes the key over, wherever its release is, and
// sends nothing. So Downs that come faster than the gap lets their events
// out do not pile up in the queue (OUT-17). The key stays down across the
// events queued after its press. Of its modifiers the Down holds only the
// ones it can take over without a press (takesOver): pressed again, they
// would go down after the key and up again with nothing between, a hotkey
// of their own (Shift alone switching the input method). When it leaves
// one out, Repeat does nothing for it (ref.merged): it would press the key
// without that modifier. Not for a modifier as the main key: held across
// another key's press it would change that key.
func (a *Arbiter) mergeDown(id KeyID) bool {
	ks := a.keys[id]
	return ks != nil && ks.refs == 0 && !modifierIDs[id] && len(a.outbox) > 0 && a.oldQueued(id)
}

// takesOver reports whether a Down's key that is not its main key gets a
// reference without a press (take): it is down for another reference, or
// its release waits or is queued and may be taken over (again).
func (a *Arbiter) takesOver(id KeyID) bool {
	ks := a.keys[id]
	return ks != nil && (ks.refs > 0 || !a.again(ks, false) && (ks.releaseAt > a.clk.Now() || a.queuedFor(id)))
}

// zeroDeadline returns when the release of ks's key would go out without
// an event gap: the time of the command that pressed it plus its minimum.
// With a gap the release goes into the queue then (commitDue, releaseDue),
// in the order it would go out without one, and keeps its minimum from the
// Send of its press there (keepMinimum).
func (a *Arbiter) zeroDeadline(ks *keyState) clock.Time {
	return ks.issued.Add(max(a.minPress, ks.minHold))
}

// pressAgain makes e o's new press of ks's key, queued behind the key's
// release, which it first queues when it still waits (commit). A
// press of the key still queued keeps its users (outEvent.old), so that a
// forced release drops it only as it would have. The key may be down
// before the new press when the previous one went out (mayBeDown).
func (a *Arbiter) pressAgain(o Owner, ks *keyState, e KeyEvent, minPress time.Duration) {
	id := ks.press.ID()
	if i := a.lastQueued(id); i < 0 || !a.outbox[i].e.Up {
		a.commit([]*keyState{ks})
	}
	if ks.waitPress {
		for i, q := range slices.Backward(a.outbox) {
			if q.e.ID() == id && !q.e.Up && !q.extra {
				a.outbox[i].old, a.outbox[i].users = true, ks.users
				break
			}
		}
	}
	a.seq++
	now := a.clk.Now()
	*ks = keyState{
		press: e, refs: 1, seq: a.seq, pressedAt: now, issued: now, minHold: minPress,
		injected: ks.injected, users: []Owner{o}, waitPress: true, waitRelease: true,
		mayBeDown: ks.mayBeDown || ks.injected,
	}
	a.queuing = true
}

// commit queues now the releases of set, keys that wait for a deadline
// (releaseAt), together with the other keys of their groups and, for a
// modifier among them, the waiting keys that are not modifiers and were
// pressed after it, so that it does not go up before them. They go in the
// order releaseDue sends them: non-modifiers first, each part in reverse
// press order. Each release keeps its minimum from the Send of its press
// (keepMinimum); the events behind it wait with it.
func (a *Arbiter) commit(set []*keyState) {
	for i := 0; i < len(set); i++ {
		ks := set[i]
		if g := ks.group; g != nil {
			for _, id := range g.keys {
				if m := a.keys[id]; m != nil && m.group == g && m.refs == 0 && !slices.Contains(set, m) {
					set = append(set, m)
				}
			}
		}
		if modifierIDs[ks.press.ID()] {
			for id, m := range a.keys {
				if m.refs == 0 && m.releaseAt != 0 && !modifierIDs[id] && m.seq > ks.seq && !slices.Contains(set, m) {
					set = append(set, m)
				}
			}
		}
	}
	slices.SortFunc(set, func(x, y *keyState) int {
		if xm, ym := modifierIDs[x.press.ID()], modifierIDs[y.press.ID()]; xm != ym {
			if xm {
				return 1
			}
			return -1
		}
		return cmp.Compare(y.seq, x.seq)
	})
	for _, m := range set {
		q := outEvent{e: m.press.release(), seq: m.seq}
		a.keepMinimum(&q, m)
		m.releaseAt, m.group, m.waitRelease = 0, nil, true
		a.outbox = append(a.outbox, q)
	}
}

// keepMinimum makes q, the release of ks's key that goes into the queue
// now, keep the key's minimum from the Send of its press: at, or pending
// while that press is queued (pressSent). Nor does it go out before it
// would without a gap (zeroDeadline), when it goes in early with another
// key (commit), also after a press that failed: the key may be down since
// an earlier one, as without a gap.
func (a *Arbiter) keepMinimum(q *outEvent, ks *keyState) {
	m := max(a.minPress, ks.minHold)
	if m <= 0 || ks.press.momentary() { // a wheel release injects nothing
		return
	}
	if a.pressPending(ks) {
		q.pending, q.hold = true, ks.minHold
		return
	}
	at := a.zeroDeadline(ks)
	if ks.injected {
		at = max(at, ks.pressedAt.Add(m))
	}
	if at > a.clk.Now() {
		q.at = at
	}
}

// commitDue queues the waiting releases of keys whose release would have
// gone out by now without an event gap (zeroDeadline), with their groups
// (commit), before a command queues presses: those keys were up when the
// command came, so its keys must not go down while they are (OUT-17).
func (a *Arbiter) commitDue() {
	now := a.clk.Now()
	var set []*keyState
	for _, ks := range a.keys {
		if ks.refs == 0 && ks.releaseAt != 0 && a.zeroDeadline(ks) <= now {
			set = append(set, ks)
		}
	}
	if len(set) > 0 {
		a.commit(set)
	}
}

// keepHold makes the hold of the tap r, which ends now, the minimum of its
// keys (minHold), so that their releases keep it from the Send of their
// presses (keepMinimum): with an event gap startTap leaves it in r.minPress.
// Only now, at the tap's end: a tap canceled before (ReleaseOwners) leaves
// no minimum, and a key another reference also holds goes up after that
// one as it would without a gap, not the canceled tap's hold after its
// Send. Without a gap the hold is the tap's end, which has come.
func (a *Arbiter) keepHold(r *ref) {
	if r.minPress <= 0 || !a.paced() {
		return
	}
	for _, id := range r.keys {
		ks := a.keys[id]
		ks.minHold = max(ks.minHold, r.minPress)
	}
}

// pressSent starts the minimum of the release of id that went into the
// queue before its press went out at sentAt (keepMinimum).
func (a *Arbiter) pressSent(id KeyID, sentAt clock.Time) {
	for i, q := range a.outbox {
		if q.e.ID() != id || !q.e.Up {
			continue
		}
		if q.pending {
			a.outbox[i].pending = false
			a.outbox[i].at = sentAt.Add(max(a.minPress, q.hold))
		}
		return
	}
}

// takeOutbox empties the outbox for a forced release of the owners match
// accepts, after their references are gone. It returns the events to send
// at once, in their order, ahead of the forced releases (forcedBatch), and
// whether it dropped a press never sent. It drops a key's press never sent
// that only those owners used (users) and that no reference holds, and the
// key's events after it up to its next press. The events before that press
// go out; the key leaves the ledger, with nothing to undo, unless some of
// its events go out, so that a release that fails is resent, or it may be
// down before the dropped press (mayBeDown). It also drops those owners'
// extra presses (Repeat, a tap on a held key) of the other keys, which
// counts as a dropped press too: it may have been the only key modifiers
// went down with (a tap's Shift on a Z held by Down, a Repeat of a Down that
// pressed only modifiers). A dropped modifier press a kept press comes after
// goes out all the same (keepChords). The taps of dropped events fail.
func (a *Arbiter) takeOutbox(match func(Owner) bool) (kept []outEvent, droppedPress bool) {
	others := func(o Owner) bool { return !match(o) }
	gone := map[KeyID]bool{}
	for id, ks := range a.keys {
		if ks.waitPress && ks.refs == 0 && !slices.ContainsFunc(ks.users, others) {
			gone[id] = true
		}
	}
	taken := a.takeAll()
	// from[i] is the index of the dropped press taken[i] goes with, its own
	// for a press, or -1 when taken[i] goes out.
	from := make([]int, len(taken))
	dropping := map[KeyID]int{} // the dropped press of a key, while its events go with it
	for i, q := range taken {
		id := q.e.ID()
		if !q.e.Up && !q.extra {
			// A press of the key's own: its keyState's, the last one, or one
			// it was pressed again behind.
			drop := gone[id]
			if q.old {
				drop = !slices.ContainsFunc(q.users, others)
			}
			if drop {
				dropping[id] = i
			} else {
				delete(dropping, id)
			}
		}
		from[i] = -1
		if p, ok := dropping[id]; ok {
			from[i] = p
		} else if q.extra && match(q.o) || q.e.Pointer() && match(q.o) {
			// A pointer move or jump of a released owner is dropped with
			// nothing to undo.
			from[i] = i
		}
	}
	keepChords(taken, from)
	var dropped []outEvent
	for i, q := range taken {
		if from[i] < 0 {
			kept = append(kept, q)
			continue
		}
		dropped = append(dropped, q)
		// A dropped pointer move is no press of a key, and needs no mask key.
		droppedPress = droppedPress || !q.e.Up && !q.e.Pointer()
	}
	for id := range gone {
		if !a.keys[id].mayBeDown && !slices.ContainsFunc(kept, func(q outEvent) bool { return q.e.ID() == id }) {
			delete(a.keys, id)
		}
	}
	a.settleQueued(dropped, true)
	// A tap may have waited for nothing but a key that just left the
	// ledger, and no Send may follow to complete it.
	if len(gone) > 0 {
		a.flushTapCompletions()
	}
	return kept, droppedPress
}

// keepChords keeps the dropped modifier presses that a press going out, of
// a key that is not a modifier, comes after while the modifier would be
// down without a gap: it goes from[i] to -1 for them and for the events of
// their keys that go with them. So a press that goes out keeps its chord,
// as without a gap. Another owner's Down X that relies on the queued X of
// the released owner's Ctrl+Alt+X does not press X with the Ctrl that went
// out and without the Alt that did not, a Ctrl+X no one asked for; Alt goes
// down before X and up in the forced releases, as Ctrl+Alt+X would have
// gone out without a gap. A dropped modifier press counts up to the next
// event of its key: after a release the modifier is up, and after a press
// of its own that goes out it is down by that one.
func keepChords(taken []outEvent, from []int) {
	var down []int // the dropped modifier presses down at i, by index
	for i, q := range taken {
		id := q.e.ID()
		switch {
		case q.e.Pointer():
			// Not a key: it keeps no chord.
		case q.extra && modifierIDs[id]:
		case modifierIDs[id]:
			down = slices.DeleteFunc(down, func(p int) bool { return taken[p].e.ID() == id })
			if !q.e.Up && from[i] == i {
				down = append(down, i)
			}
		case !q.e.Up && from[i] < 0:
			for _, p := range down {
				for j := p; j < len(from); j++ {
					if from[j] == p {
						from[j] = -1
					}
				}
			}
			down = down[:0]
		}
	}
}

// forcedBatch returns the batch of a forced release: the events kept from
// the outbox, then the forced releases ups of the keys the kept events do
// not leave up. When a press was dropped (dropped) and the batch presses
// no key but modifiers before its first modifier release, the mask key
// goes down and up before that release: the modifiers that went down
// without their key must not make a hotkey of their own. What the batch
// sends after that release does not count: it comes too late.
func (a *Arbiter) forcedBatch(kept []outEvent, ups []KeyEvent, dropped bool) []KeyEvent {
	batch := make([]KeyEvent, 0, len(kept)+len(ups)+2)
	for _, q := range kept {
		batch = append(batch, q.e)
	}
	for _, e := range ups {
		if !leftUp(kept, e.ID()) {
			batch = append(batch, e)
		}
	}
	if !dropped {
		return batch
	}
	i := slices.IndexFunc(batch, func(e KeyEvent) bool { return e.Up && modifierIDs[e.ID()] })
	// A mouse event is no key for the mask (dirtyAfter).
	if i < 0 || slices.ContainsFunc(batch[:i], func(e KeyEvent) bool { return !e.Up && !modifierIDs[e.ID()] && !e.isMouse() }) {
		return batch
	}
	return slices.Insert(batch, i, a.maskTap()...)
}

// leftUp reports whether the last of kept's events of id is a release.
func leftUp(kept []outEvent, id KeyID) bool {
	for _, q := range slices.Backward(kept) {
		if q.e.ID() == id {
			return q.e.Up
		}
	}
	return false
}

// maskTap returns a press and a release of the menu mask key, or nothing
// while a reference holds the key. The ledger tracks the key without a
// reference, so that a release that fails is resent like any other
// (CLAUDE.md invariant 1).
func (a *Arbiter) maskTap() []KeyEvent {
	ks := a.keys[menuMaskID]
	if ks != nil && ks.refs > 0 {
		return nil
	}
	if ks == nil {
		a.seq++
		now := a.clk.Now()
		a.keys[menuMaskID] = &keyState{press: MenuMaskEvent(false), seq: a.seq, pressedAt: now, issued: now}
	}
	return []KeyEvent{MenuMaskEvent(false), MenuMaskEvent(true)}
}

// unrefPaced is unref with an event gap. The keys of ids left without a
// reference go up when they would without a gap (zeroDeadline): a key
// whose deadline has not come yet waits, and every such key of ids waits
// with it until the latest of their deadlines, in a releaseGroup, so that
// they go into the queue together (releaseDue). A modifier of ids also
// waits with the keys pressed after it whose releases wait (waitingAfter).
// Otherwise it returns their releases, in reverse order of ids, to be
// queued behind their presses; each keeps its minimum from the Send of its
// press there (keepMinimum). The release of a wheel key injects nothing: it
// never waits and is always returned.
func (a *Arbiter) unrefPaced(ids []KeyID) []KeyEvent {
	now := a.clk.Now()
	var free []KeyID
	var wheel []KeyEvent
	var at clock.Time
	for _, id := range slices.Backward(ids) {
		ks := a.keys[id]
		ks.refs--
		if ks.refs > 0 {
			continue
		}
		if id.momentary() {
			// A wheel release injects nothing: it has no minimum to wait for and
			// joins no group, and goes into the queue now, ahead of the releases
			// of the modifiers that wait.
			ks.releaseAt, ks.group = 0, nil
			wheel = append(wheel, ks.press.release())
			continue
		}
		free = append(free, id)
		at = max(at, a.zeroDeadline(ks))
	}
	members := slices.Clone(free)
	for _, id := range free {
		if modifierIDs[id] {
			members = a.waitingAfter(a.keys[id], members)
		}
	}
	for _, id := range members[len(free):] {
		at = max(at, a.keys[id].releaseAt)
	}
	switch {
	case at > now:
		g := &releaseGroup{keys: members}
		for _, id := range members {
			ks := a.keys[id]
			ks.releaseAt, ks.group = at, g
		}
		return wheel
	case len(members) > len(free):
		// The keys pressed after a modifier wait for a deadline that has
		// passed: they go into the queue now, ahead of it. Not from a tap
		// that queues its presses after this: commitDue came first.
		set := make([]*keyState, len(members))
		for i, id := range members {
			set[i] = a.keys[id]
		}
		a.commit(set)
		return wheel
	}
	ups := append(make([]KeyEvent, 0, len(wheel)+len(free)), wheel...)
	for _, id := range free {
		ks := a.keys[id]
		ks.releaseAt, ks.group = 0, nil
		ups = append(ups, ks.press.release())
	}
	return ups
}

// waitingAfter returns members with the keys, not in it yet, that are not
// modifiers, were pressed after mod and wait for a deadline to go up, and
// with the members of their groups. They went down while mod was down:
// they go up first, as the keys of a chord go up before its modifiers.
func (a *Arbiter) waitingAfter(mod *keyState, members []KeyID) []KeyID {
	for id, ks := range a.keys {
		if ks.refs > 0 || ks.releaseAt == 0 || modifierIDs[id] || ks.seq < mod.seq || slices.Contains(members, id) {
			continue
		}
		members = append(members, id)
		if g := ks.group; g != nil {
			for _, m := range g.keys {
				if mk := a.keys[m]; mk != nil && mk.group == g && !slices.Contains(members, m) {
					members = append(members, m)
				}
			}
		}
	}
	return members
}

// pressPending reports whether the press that starts ks's physical minimum
// is queued: the key's own press (waitPress), or, while no press went out,
// the extra press of a tap or Repeat after a press that failed.
func (a *Arbiter) pressPending(ks *keyState) bool {
	if ks.waitPress {
		return true
	}
	if ks.injected || len(a.outbox) == 0 {
		return false
	}
	id := ks.press.ID()
	for _, q := range a.outbox {
		if !q.e.Up && q.e.ID() == id {
			return true
		}
	}
	return false
}

// tapWaits reports whether the tap r cannot complete yet: events of its
// command are queued, or a key it pressed is still pending release. With
// an event gap that is the key's keyState when it was pressed no later
// than the tap's keys (ref.last), not a newer press of the key by another
// command, or a release queued for such a press of the key, which was
// pressed again behind it (pressAgain). Without a gap any keyState of the
// key, as before OUT-17: a newer one is there only after a failed release.
func (a *Arbiter) tapWaits(r *ref) bool {
	if r.queued > 0 {
		return true
	}
	paced := a.paced()
	for _, id := range r.keys {
		if ks := a.keys[id]; ks != nil && ks.refs == 0 && (ks.seq <= r.last || !paced) {
			return true
		}
	}
	for _, q := range a.outbox {
		if q.e.Up && q.seq != 0 && q.seq <= r.last && slices.Contains(r.keys, q.e.ID()) {
			return true
		}
	}
	return false
}
