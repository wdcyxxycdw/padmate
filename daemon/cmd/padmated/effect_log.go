package main

import (
	"sync"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// maxEffectLog bounds an effectLog: the oldest call goes first.
const maxEffectLog = 1024

// effectCall is one effect request that a fake source received: which
// DeviceID got what.
type effectCall struct {
	Kind     string // "feedback", "play", "light", "stop" or "identify"
	Device   input.DeviceID
	Feedback input.Feedback // for "feedback"
	Effect   input.Effect   // for "play"
	Light    input.Light    // for "light"
	Stop     input.Stop     // for "stop"
	Cues     haptic.Cues    // for "identify": what the fake gamepad answered
}

// effectLog records the effect calls of a fake input source: the fake
// source of the IPC (--fake-input ipc) and the tests'. A real controller is
// never reached by either, so what they received is the only way to see
// what padmated asked of the input.
type effectLog struct {
	mu    sync.Mutex
	calls []effectCall
}

func (l *effectLog) add(c effectCall) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.calls) >= maxEffectLog {
		l.calls = append(l.calls[:0], l.calls[1:]...)
	}
	l.calls = append(l.calls, c)
}

// all returns the recorded calls, oldest first.
func (l *effectLog) all() []effectCall { return l.take(false) }

// take returns the recorded calls, oldest first, and forgets them when
// forget is set.
func (l *effectLog) take(forget bool) []effectCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	calls := append([]effectCall(nil), l.calls...)
	if forget {
		l.calls = nil
	}
	return calls
}
