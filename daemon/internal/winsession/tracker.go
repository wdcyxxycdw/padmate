package winsession

import (
	"math/bits"
	"time"
)

// DefaultStaleAfter is how long Sleep and End may stay set, in awake time,
// before Tracker.Step clears them as stale: a missed wake or a missed
// WM_ENDSESSION(FALSE) must not hold the mapping forever.
const DefaultStaleAfter = 60 * time.Second

// SessionInfo is what Querier.Session reads about this process's session.
type SessionInfo struct {
	// ID is the session ID.
	ID uint32
	// Active reports WTSActive: a user is signed in and the session owns
	// the console or a remote client. A disconnected session (switched
	// away from, RDP client gone) is not active.
	Active bool
	// LockKnown reports that Locked is known; the system may not say.
	LockKnown bool
	// Locked reports the workstation locked. It means nothing unless
	// LockKnown.
	Locked bool
}

// Querier reads the state Tracker.Step corrects its reasons with. The
// session thread's is the system's (WTSQuerySessionInformationW and the
// input desktop); tests and the supervisor's goldens use fakes.
type Querier interface {
	// Session reads the session's state.
	Session() (SessionInfo, error)
	// Desktop reports whether the input desktop is not this thread's,
	// such as the secure desktop of UAC or Ctrl+Alt+Del.
	Desktop() (other bool, err error)
}

// Change is what one Tracker.Step or Recheck did to the reasons.
type Change struct {
	// Signal is the step's signal.
	Signal Signal
	// Was and Now are the reasons before and after the step. Was == Now
	// means the step changed nothing.
	Was, Now Reason
	// Stale is the reasons the step cleared as stale (Sleep, End), before
	// it applied the signal.
	Stale Reason
}

// Changed reports Was != Now.
func (c Change) Changed() bool { return c.Was != c.Now }

// Tracker turns signals into the set of reasons the mapping is suspended
// for. It is pure: time comes in as the awake time of each step and the
// system through a Querier, so it runs the same on the session thread, in
// the supervisor's goldens and in fuzz tests. It is not safe for
// concurrent use; the session thread owns it.
//
// The rules of Step, after the stale guard:
//
//   - SigInit starts over: every reason cleared, then the session and the
//     desktop queried.
//   - SigLock, SigDisconnect, SigSleep, SigQueryEnd and SigEnd set their
//     reason and never query, so the release waits for no RPC.
//   - SigUnlock, SigConnect, SigWake and SigEndCanceled clear their reason
//     and then query the session and the desktop, whose answers win: an
//     unlock while the session still says locked keeps Lock.
//   - SigDesktopSwitch queries the desktop.
//   - SigTick queries the desktop, and the session while Lock or
//     Disconnect is set, while notifications are not registered
//     (SetRegistered), and on every fifth tick.
//
// A successful session query sets Lock to Locked when LockKnown and
// Disconnect to !Active; a successful desktop query sets Desktop. A failed
// query changes nothing. Queries never touch Sleep or End: the stale guard
// clears them once they have been set for staleAfter of awake time, which
// excludes the time the machine slept. Lock, Disconnect and Desktop never
// go stale; the queries correct them.
//
// Every signal that sets a reason also advances that reason's generation
// (Gen). The session thread applies a clearing signal later, from a posted
// recheck that carries the generation it was posted at; Recheck clears the
// reason only if no signal set it again in between, so a lock, a sleep or
// the end of the session handled before an older unlock's, wake's or
// cancel's recheck is not undone by it.
//
// A Querier that panics leaves the step half done: what came before the
// query stays, such as the stale guard, the cleared reason, the tick and
// the tick's desktop answer. That is a state an unchecked step (q nil)
// could reach too; the session thread reports it (Watcher.resync), with
// what the guard cleared (LastStale), rather than undoing it, so that a
// Querier that always panics cannot freeze the reasons.
type Tracker struct {
	reasons    Reason
	gens       [len(reasonNames)]uint32 // per Reason bit, advanced by its set signals
	staleAfter time.Duration
	sleepSince time.Duration
	endSince   time.Duration
	ticks      uint64
	registered bool
	// lastStale is what the last step's stale guard cleared (LastStale).
	lastStale Reason
}

// sessionEvery is how many ticks apart the tick queries the session while
// nothing needs it sooner.
const sessionEvery = 5

// NewTracker returns a Tracker with no reasons that clears Sleep and End
// after staleAfter of awake time, or DefaultStaleAfter if staleAfter <= 0.
// It starts registered.
func NewTracker(staleAfter time.Duration) *Tracker {
	if staleAfter <= 0 {
		staleAfter = DefaultStaleAfter
	}
	return &Tracker{staleAfter: staleAfter, registered: true}
}

// SetRegistered records whether the session thread receives session
// notifications. While it does not, every tick queries the session.
func (t *Tracker) SetRegistered(ok bool) { t.registered = ok }

// Reasons returns the current reasons.
func (t *Tracker) Reasons() Reason { return t.reasons }

// LastStale returns what the stale guard of the last Step or Recheck
// cleared, its Change.Stale, also when a Querier's panic lost that
// Change: the guard runs before the queries.
func (t *Tracker) LastStale() Reason { return t.lastStale }

// Step applies one signal at awake time awake, the monotonic time the
// machine has been awake (QueryUnbiasedInterruptTime), and returns what it
// changed. q may be nil: the step then queries nothing, as the session
// thread does when it cannot post itself a recheck. An invalid signal only
// runs the stale guard.
func (t *Tracker) Step(sig Signal, awake time.Duration, q Querier) Change {
	return t.apply(sig, awake, q, true)
}

// Gen returns the generation of the reason the clearing signal sig clears:
// how many times a signal has set that reason. It is 0 for any other
// signal. The session thread posts it with its recheck (Recheck).
func (t *Tracker) Gen(sig Signal) uint32 {
	r := cleared(sig)
	if r == 0 {
		return 0
	}
	return t.gens[bits.TrailingZeros8(uint8(r))]
}

// Recheck applies the clearing signal sig as Step does, but clears its
// reason only if gen is still its Gen: a signal that set the reason after
// the recheck was posted wins over it. The queries run either way; they
// correct Lock and Disconnect, but never Sleep or End. A signal that does
// not clear only runs the stale guard, like an invalid one.
func (t *Tracker) Recheck(sig Signal, gen uint32, awake time.Duration, q Querier) Change {
	if !sig.Clears() {
		c := t.apply(0, awake, nil, false)
		c.Signal = sig
		return c
	}
	return t.apply(sig, awake, q, gen == t.Gen(sig))
}

// apply is Step, except that a clearing signal clears its reason only if
// mayClear is set.
func (t *Tracker) apply(sig Signal, awake time.Duration, q Querier, mayClear bool) Change {
	c := Change{Signal: sig, Was: t.reasons}
	t.lastStale = t.expire(awake) // on every step, so that none reuses an older one
	c.Stale = t.lastStale
	switch sig {
	case SigInit:
		t.reasons = 0
		t.querySession(q)
		t.queryDesktop(q)
	case SigLock:
		t.set(Lock)
	case SigDisconnect:
		t.set(Disconnect)
	case SigSleep:
		t.set(Sleep)
		t.sleepSince = awake
	case SigQueryEnd, SigEnd:
		t.set(End)
		t.endSince = awake
	case SigUnlock, SigConnect, SigWake, SigEndCanceled:
		if mayClear {
			t.reasons &^= cleared(sig)
		}
		t.querySession(q)
		t.queryDesktop(q)
	case SigDesktopSwitch:
		t.queryDesktop(q)
	case SigTick:
		t.ticks++
		t.queryDesktop(q)
		if t.reasons&(Lock|Disconnect) != 0 || !t.registered || t.ticks%sessionEvery == 0 {
			t.querySession(q)
		}
	}
	c.Now = t.reasons
	return c
}

// set sets the reason bit r and advances its generation.
func (t *Tracker) set(r Reason) {
	t.reasons |= r
	t.gens[bits.TrailingZeros8(uint8(r))]++
}

// cleared returns the reason a clearing signal clears.
func cleared(sig Signal) Reason {
	switch sig {
	case SigUnlock:
		return Lock
	case SigConnect:
		return Disconnect
	case SigWake:
		return Sleep
	case SigEndCanceled:
		return End
	}
	return 0
}

// expire clears Sleep and End once they have been set for staleAfter of
// awake time and returns what it cleared. An awake time before the stamp
// (which a monotonic clock never gives) counts as no time.
func (t *Tracker) expire(awake time.Duration) Reason {
	var stale Reason
	if t.reasons&Sleep != 0 && awake-t.sleepSince >= t.staleAfter {
		stale |= Sleep
	}
	if t.reasons&End != 0 && awake-t.endSince >= t.staleAfter {
		stale |= End
	}
	t.reasons &^= stale
	return stale
}

func (t *Tracker) querySession(q Querier) {
	if q == nil {
		return
	}
	info, err := q.Session()
	if err != nil {
		return
	}
	if info.LockKnown {
		t.reasons = setIf(t.reasons, Lock, info.Locked)
	}
	t.reasons = setIf(t.reasons, Disconnect, !info.Active)
}

func (t *Tracker) queryDesktop(q Querier) {
	if q == nil {
		return
	}
	other, err := q.Desktop()
	if err != nil {
		return
	}
	t.reasons = setIf(t.reasons, Desktop, other)
}

// setIf returns r with bit set if on, and cleared otherwise.
func setIf(r, bit Reason, on bool) Reason {
	if on {
		return r | bit
	}
	return r &^ bit
}
