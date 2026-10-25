package main

import (
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"sync"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// maxPendingEvents bounds the one-shot effects that wait for the worker. A
// burst of knob ticks or bindings that the controller cannot play at that
// rate is not worth keeping: when the line is full the oldest event goes.
const maxPendingEvents = 32

// effectSender is what the worker of an effectQueue calls: the ways into
// input.Source.
type effectSender struct {
	feedback func(input.Feedback) error
	play     func(input.Effect) error
	light    func(input.Light) error
	stop     func(input.Stop) error
}

// effectKind is what a request in the line is.
type effectKind uint8

const (
	// kindAmbient is the state of a layer or the pause.
	kindAmbient effectKind = iota
	// kindEffect is a one-shot effect.
	kindEffect
	// kindLight is a script's claim on the LED, or the end of it.
	kindLight
	// kindStop is the end of what a script's class has going.
	kindStop
)

// effectItem is one request in the line.
type effectItem struct {
	kind     effectKind
	feedback input.Feedback // kindAmbient
	effect   input.Effect   // kindEffect
	light    input.Light    // kindLight
	stop     input.Stop     // kindStop
}

// device is the gamepad the request is for.
func (it effectItem) device() input.DeviceID {
	switch it.kind {
	case kindAmbient:
		return it.feedback.Device
	case kindLight:
		return it.light.Device
	case kindStop:
		return it.stop.Device
	}
	return it.effect.Device
}

// effectQueue serializes physical effects without blocking a slot or the
// engine's callbacks. It has one worker, which calls the Source in order:
//
//   - The ambient state replaces the pending ambient state of the same device,
//     in its place in the line, since Source.SetFeedback would itself replace
//     that device's color and pulses; so a slot that changes layers twice
//     while the worker is busy sends the last only.
//   - A one-shot effect (a binding's haptic, a knob tick, a battery alert, a
//     script's rumble) waits in a line of at most maxPendingEvents, the oldest
//     Event-class one dropped first (then the oldest of any class); they are
//     not merged, since two ticks are two ticks. The Mixer in Source decides
//     what a burst of them may interrupt and how close together they may start.
//   - A script's claim on the LED replaces its pending one for the same
//     gamepad and class, the last wins; and the end of what a script's class has
//     going (a stop) is never dropped, since the effects of a script must not
//     outlive it (invariant 1): it also takes the one-shot effects and claims
//     of that gamepad and class that still wait out of the line, which it would
//     end the moment they began.
//
// Posting never blocks. Closing waits for the worker and drops what waits;
// a posted request after it is ignored.
type effectQueue struct {
	send    effectSender
	log     *slog.Logger
	mu      sync.Mutex
	pending []effectItem
	events  int // the one-shot effects in pending
	closed  bool
	wake    chan struct{}
	done    chan struct{}
	failure error // set before done closes; read only after waiting for done
}

func startEffectQueue(send effectSender, log *slog.Logger) *effectQueue {
	q := &effectQueue{send: send, log: log, wake: make(chan struct{}, 1), done: make(chan struct{})}
	go q.run()
	return q
}

// post queues the ambient state f.
func (q *effectQueue) post(f input.Feedback) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	if i := slices.IndexFunc(q.pending, func(p effectItem) bool { return p.kind == kindAmbient && p.feedback.Device == f.Device }); i >= 0 {
		q.pending[i].feedback = f
	} else {
		q.pending = append(q.pending, effectItem{kind: kindAmbient, feedback: f})
	}
	q.wakeLocked()
}

// postEffect queues the one-shot effect e.
func (q *effectQueue) postEffect(e input.Effect) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	if q.events >= maxPendingEvents {
		// A burst of ticks goes first: an alert (the low battery) or a script's
		// effect waits for its turn rather than make room for them.
		i := slices.IndexFunc(q.pending, func(p effectItem) bool { return p.kind == kindEffect && p.effect.Class == haptic.Event })
		if i < 0 {
			i = slices.IndexFunc(q.pending, func(p effectItem) bool { return p.kind == kindEffect })
		}
		if i >= 0 {
			q.pending = slices.Delete(q.pending, i, i+1)
			q.events--
		}
	}
	q.pending = append(q.pending, effectItem{kind: kindEffect, effect: e})
	q.events++
	q.wakeLocked()
}

// postLight queues l, a claim on the LED or the end of one. It replaces the
// pending one for the same gamepad and class.
func (q *effectQueue) postLight(l input.Light) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	if i := slices.IndexFunc(q.pending, func(p effectItem) bool {
		return p.kind == kindLight && p.light.Device == l.Device && p.light.Class == l.Class
	}); i >= 0 {
		q.pending[i].light = l
	} else {
		q.pending = append(q.pending, effectItem{kind: kindLight, light: l})
	}
	q.wakeLocked()
}

// postStop queues s, the end of what a class has going on a gamepad. What the
// class has waiting for that gamepad is dropped: its one-shot effects, and with
// Everything its claims on the LED.
func (q *effectQueue) postStop(s input.Stop) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.pending = slices.DeleteFunc(q.pending, func(p effectItem) bool {
		switch {
		case p.kind == kindEffect && p.effect.Device == s.Device && p.effect.Class == s.Class:
			q.events--
			return true
		case p.kind == kindLight && s.Everything && p.light.Device == s.Device && p.light.Class == s.Class:
			return true
		}
		return false
	})
	q.pending = append(q.pending, effectItem{kind: kindStop, stop: s})
	q.wakeLocked()
}

func (q *effectQueue) wakeLocked() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// call sends one item to the Source and logs its failure at debug level, as
// long as the queue is open (an effect that fails while the Source closes is
// no news).
func (q *effectQueue) call(it effectItem) {
	var err error
	switch it.kind {
	case kindAmbient:
		if q.send.feedback != nil {
			err = q.send.feedback(it.feedback)
		}
	case kindEffect:
		if q.send.play != nil {
			err = q.send.play(it.effect)
		}
	case kindLight:
		if q.send.light != nil {
			err = q.send.light(it.light)
		}
	case kindStop:
		if q.send.stop != nil {
			err = q.send.stop(it.stop)
		}
	}
	if err == nil || q.log == nil {
		return
	}
	q.mu.Lock()
	closed := q.closed
	q.mu.Unlock()
	if !closed {
		q.log.Debug("controller effect unavailable", "device", it.device(), "ambient", it.kind == kindAmbient, "err", err)
	}
}

func (q *effectQueue) run() {
	defer close(q.done)
	finished := false
	// A failing controller effect or logger must not bypass key cleanup.
	defer func() { _ = recover() }()
	defer func() {
		var failure any
		if !finished {
			failure = "runtime.Goexit"
			if p := recover(); p != nil {
				failure = p
			}
			q.failure = fmt.Errorf("controller effects stopped: %v", failure)
		}
		q.mu.Lock()
		wasClosed := q.closed
		q.closed = true
		q.pending = nil
		q.events = 0
		q.mu.Unlock()
		if failure != nil && !wasClosed && q.log != nil {
			q.log.Error("controller effects stopped", "panic", failure, "stack", string(debug.Stack()))
		}
	}()
	for range q.wake {
		for {
			q.mu.Lock()
			if q.closed {
				q.mu.Unlock()
				finished = true
				return
			}
			if len(q.pending) == 0 {
				q.mu.Unlock()
				break
			}
			it := q.pending[0]
			q.pending = slices.Delete(q.pending, 0, 1)
			if it.kind == kindEffect {
				q.events--
			}
			q.mu.Unlock()
			q.call(it)
		}
	}
	finished = true
}

func (q *effectQueue) close() {
	if q == nil {
		return
	}
	q.mu.Lock()
	q.closed = true
	q.pending = nil
	q.events = 0
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
	<-q.done
}
