package logging

import (
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

// maxKeys bounds the keys a Limiter remembers.
const maxKeys = 256

// Limiter lets each key through at most once per interval: rate limiting
// for errors that repeat, keyed by their message. It remembers at most
// 256 keys; when full, it forgets the key it allowed longest ago, which is
// then allowed at once. It reads time only from its clock. A Limiter is
// not safe for concurrent use.
type Limiter struct {
	clk   clock.Clock
	every time.Duration
	keys  map[string]*limited
	seq   uint64 // counts the allows, for the order of forgetting
}

type limited struct {
	at         clock.Time // when it was last allowed
	seq        uint64     // Limiter.seq then
	suppressed int        // refusals since
}

// NewLimiter returns a Limiter that allows each key once per every on
// clk. With every 0 it allows everything.
func NewLimiter(clk clock.Clock, every time.Duration) *Limiter {
	return &Limiter{clk: clk, every: every, keys: map[string]*limited{}}
}

// Allow reports whether key may go through now, and how many times it was
// refused since it was last allowed, a refusal now included.
func (l *Limiter) Allow(key string) (ok bool, suppressed int) {
	now := l.clk.Now()
	e := l.keys[key]
	switch {
	case e == nil:
		if len(l.keys) == maxKeys {
			l.forget()
		}
		e = &limited{}
		l.keys[key] = e
	case now.Sub(e.at) < l.every:
		e.suppressed++
		return false, e.suppressed
	}
	suppressed = e.suppressed
	l.seq++
	e.at, e.seq, e.suppressed = now, l.seq, 0
	return true, suppressed
}

// forget drops the key allowed longest ago.
func (l *Limiter) forget() {
	var oldest *limited
	var key string
	for k, e := range l.keys {
		if oldest == nil || e.seq < oldest.seq {
			oldest, key = e, k
		}
	}
	delete(l.keys, key)
}
