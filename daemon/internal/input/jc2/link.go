package jc2

import "time"

// DefaultSilence is how long a link may stay silent before it counts as
// dormant: T-input releases everything but keeps the link. The controllers
// send reports periodically, also at rest: every 15 ms after the connection
// parameters ThroughputOptimized are requested (Windows 11; the largest gap
// at rest was 33-61 ms) and every 60 ms without them (Windows 10). The
// largest gap ever measured is 136 ms, so 300 ms is over twice that at either
// setting. Windows takes about 9.7 s to notice that a link is dead, far too
// long to leave a key held.
const DefaultSilence = 300 * time.Millisecond

// DefaultLinkGiveUp is how long without a report T-input waits for a dormant
// link before it closes the link for good.
const DefaultLinkGiveUp = 3 * time.Second

// Silence is a watchdog for the reports of one link. It reads no clock: the
// caller passes monotonic times, such as time since T-input started. The
// zero value is ready, with the timeout DefaultSilence.
type Silence struct {
	// Timeout is how long without a report is dead; zero means
	// DefaultSilence.
	Timeout time.Duration
	// GiveUp is how long without a report is a lost link; zero means
	// DefaultLinkGiveUp.
	GiveUp time.Duration

	last time.Duration
	seen bool
}

// timeout returns the effective timeout.
func (s *Silence) timeout() time.Duration {
	if s.Timeout <= 0 {
		return DefaultSilence
	}
	return s.Timeout
}

// giveUp returns the effective give-up time.
func (s *Silence) giveUp() time.Duration {
	if s.GiveUp <= 0 {
		return DefaultLinkGiveUp
	}
	return s.GiveUp
}

// Seen records that the link was alive at time t: a report arrived, or the
// link has just been set up. A t before an earlier one is ignored.
func (s *Silence) Seen(t time.Duration) {
	if !s.seen || t > s.last {
		s.last, s.seen = t, true
	}
}

// Dead reports whether the link has been silent for the timeout at time now.
// A link that was never Seen is not dead: there is nothing to watch yet.
func (s *Silence) Dead(now time.Duration) bool {
	return s.seen && now-s.last >= s.timeout()
}

// GaveUp reports whether the link has been silent for GiveUp at time now, so
// that it should be closed. Like Dead, it is false for a link never Seen.
func (s *Silence) GaveUp(now time.Duration) bool {
	return s.seen && now-s.last >= s.giveUp()
}

// Deadline returns the time at which the link becomes dead if nothing is
// seen before, and false if nothing was seen yet.
func (s *Silence) Deadline() (time.Duration, bool) {
	return s.last + s.timeout(), s.seen
}

// Reset forgets what was seen.
func (s *Silence) Reset() { s.last, s.seen = 0, false }
