package haptic

import (
	"encoding/hex"
	"time"
)

// RGB is an LED color.
type RGB [3]uint8

// ParseColor reads "#RRGGBB", in either case of the hex digits: the color
// notation of the profile schema.
func ParseColor(s string) (RGB, bool) {
	var c RGB
	if len(s) != 7 || s[0] != '#' {
		return c, false
	}
	if _, err := hex.Decode(c[:], []byte(s[1:])); err != nil {
		return RGB{}, false
	}
	return c, true
}

// Phase is one stretch of a timed pattern of an LED color or a player index:
// the value shows for Ms milliseconds, or, when Show is false, the owner
// claims nothing for that long and the claim of the next owner below shows
// through.
type Phase[T any] struct {
	Value T
	Show  bool
	Ms    uint16
}

// LEDPhase and PlayerPhase are the phases of an LED pattern and of a
// player-light pattern.
type (
	LEDPhase    = Phase[RGB]
	PlayerPhase = Phase[int]
)

// Blink returns the phases of n flashes of color, each on for onMs and then
// off for offMs, ending with the last flash on.
func Blink(color RGB, onMs, offMs, n int) []LEDPhase {
	n = min(max(n, 1), MaxSteps/2)
	phases := make([]LEDPhase, 0, 2*n)
	for i := range n {
		if i > 0 {
			phases = append(phases, LEDPhase{Ms: clampMs(offMs)})
		}
		phases = append(phases, LEDPhase{Value: color, Show: true, Ms: clampMs(onMs)})
	}
	return phases
}

// claim is what one owner asks of a level effect, an effect that stays until
// changed: the LED color, the player index. It is either a static value,
// held until released, or a timed pattern, which ends by itself and then
// leaves the effect to the owners below.
type claim[T comparable] struct {
	on    bool
	val   T // the static value, when seq is nil
	seq   []Phase[T]
	i     int
	until time.Time // the end of seq[i]
}

// hold makes the claim a static one of v.
func (c *claim[T]) hold(v T) { *c = claim[T]{on: true, val: v} }

// play makes the claim the pattern seq, which starts at now. An empty
// pattern is no claim.
func (c *claim[T]) play(seq []Phase[T], now time.Time) {
	if len(seq) == 0 {
		*c = claim[T]{}
		return
	}
	if len(seq) > MaxSteps {
		seq = seq[:MaxSteps]
	}
	*c = claim[T]{on: true, seq: seq, until: now.Add(phaseDuration(seq[0].Ms))}
}

// release gives up the claim.
func (c *claim[T]) release() { *c = claim[T]{} }

// timed reports whether the claim is a pattern that is still running.
func (c *claim[T]) timed() bool { return c.on && c.seq != nil }

// value returns what the claim shows now, and false when it shows nothing.
func (c *claim[T]) value() (T, bool) {
	var zero T
	switch {
	case !c.on:
		return zero, false
	case c.seq == nil:
		return c.val, true
	case c.seq[c.i].Show:
		return c.seq[c.i].Value, true
	}
	return zero, false
}

// advance moves a running pattern on to now, and reports whether it moved to
// another phase or ended. A phase starts when the previous one is seen to be
// over, not at the exact time it was due, since the Mixer is driven by a
// loop.
func (c *claim[T]) advance(now time.Time) bool {
	if !c.timed() || now.Before(c.until) {
		return false
	}
	c.i++
	if c.i >= len(c.seq) {
		c.release()
		return true
	}
	c.until = now.Add(phaseDuration(c.seq[c.i].Ms))
	return true
}

func phaseDuration(ms uint16) time.Duration {
	return time.Duration(clampMs(int(ms))) * time.Millisecond
}

// levelOrder is who wins a level effect, first to last. It differs from the
// order of the classes for rumble: an ambient claim, the layer color, is the
// weakest, because anything that blinks or is asked for explicitly must show
// above it and give it back.
var levelOrder = [...]Class{Identify, Alert, Script, Ambient}

// winnerOf returns the strongest owner that shows something, and what it asks
// for.
func winnerOf[T comparable](claims *[numClasses]claim[T]) (Class, T, bool) {
	for _, c := range levelOrder {
		if v, ok := claims[c].value(); ok {
			return c, v, true
		}
	}
	var zero T
	return 0, zero, false
}

// winner returns what the strongest owner that shows something asks for.
func winner[T comparable](claims *[numClasses]claim[T]) (T, bool) {
	_, v, ok := winnerOf(claims)
	return v, ok
}
