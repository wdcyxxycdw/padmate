package script

import (
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// OutputRate is the sustained number of requested key changes per second.
// Presses reserve tokens for their future releases; releases never wait.
const OutputRate = 500

// OutputBurst is the maximum number of requested key changes that can pass at
// once for one script runtime.
const OutputBurst = 100

// OutputBudget is the M5 output guard of one output scope. Its token bucket
// belongs to the runtime: tokens refill at OutputRate per second and the
// bucket is capped at OutputBurst. Taps also have one pending entry per main
// key, which belongs to the scope: each run of a macro forks the runtime's
// budget (SCR-50), so two macros can tap the same key at once while both
// draw on one bucket. The budget is owned by the runtime's slot loop and is
// not safe for concurrent use.
type OutputBudget struct {
	bucket  *tokenBucket
	pending map[keys.Key]bool
}

// tokenBucket is a fixed point token bucket shared by the budgets of one
// runtime.
type tokenBucket struct {
	clock clock.Clock
	last  clock.Time

	tokens int64
	frac   int64 // refill numerator below one token, in nanoseconds*rate
}

// NewOutputBudget creates a full budget driven by c. A nil clock uses the
// process real clock.
func NewOutputBudget(c clock.Clock) *OutputBudget {
	if c == nil {
		c = clock.Real()
	}
	return &OutputBudget{
		bucket:  &tokenBucket{clock: c, last: c.Now(), tokens: OutputBurst},
		pending: make(map[keys.Key]bool),
	}
}

// fork returns a budget that draws on b's token bucket and has no pending
// taps of its own yet.
func (b *OutputBudget) fork() *OutputBudget {
	return &OutputBudget{bucket: b.bucket, pending: make(map[keys.Key]bool)}
}

// Allow reserves changes requested key changes. It returns false when the
// request is invalid, larger than the burst, or the bucket has insufficient
// tokens. Releases should not call Allow: safety requires releases to always
// reach the output arbiter.
func (b *OutputBudget) Allow(changes int) bool {
	if b == nil || changes <= 0 || changes > OutputBurst {
		return false
	}
	return b.bucket.allow(int64(changes))
}

// AllowTap reserves a tap's requested presses and eventual releases, and its
// main key until the Arbiter reports completion. A hold of zero still needs
// completion before another tap of that main key may start.
func (b *OutputBudget) AllowTap(chord keys.Chord, _ time.Duration) bool {
	if b == nil || !chord.IsValid() {
		return false
	}
	chord = chord.Sided()
	key := chord.Key()
	if b.pending[key] {
		return false
	}
	if !b.Allow(2 * len(chord.Keys())) {
		return false
	}
	b.pending[key] = true
	return true
}

// CompleteTap frees the main-key reservation only after the Arbiter has
// reported the tap's final release or cancellation.
func (b *OutputBudget) CompleteTap(chord keys.Chord) {
	if b != nil {
		delete(b.pending, chord.Sided().Key())
	}
}

// ReleaseAll clears pending tap reservations. It deliberately leaves the
// token balance unchanged: a runtime that is still active keeps its current
// rate budget after releasing its keys.
func (b *OutputBudget) ReleaseAll() {
	if b == nil {
		return
	}
	clear(b.pending)
}

func (b *tokenBucket) allow(need int64) bool {
	b.refill(b.clock.Now())
	if b.tokens < need {
		return false
	}
	b.tokens -= need
	return true
}

func (b *tokenBucket) refill(now clock.Time) {
	if now <= b.last {
		return
	}
	if b.tokens >= OutputBurst {
		b.last = now
		b.frac = 0
		return
	}

	// Once this bounded interval elapses, the bucket is full. Capping before
	// multiplication keeps the fixed-point arithmetic from overflowing even
	// if a real clock is left idle for years.
	missing := int64(OutputBurst) - b.tokens
	need := missing*int64(time.Second) - b.frac
	fillAfter := (need + OutputRate - 1) / OutputRate
	elapsed := now.Sub(b.last)
	if elapsed >= time.Duration(fillAfter) {
		b.tokens = OutputBurst
		b.frac = 0
		b.last = now
		return
	}

	numerator := int64(elapsed)*OutputRate + b.frac
	b.tokens += numerator / int64(time.Second)
	b.frac = numerator % int64(time.Second)
	if b.tokens > OutputBurst {
		b.tokens = OutputBurst
		b.frac = 0
	}
	b.last = now
}
