package script

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// tapOperation and its Promise belong only to the Host owner goroutine. Its
// scope is the output scope that started it.
type tapOperation struct {
	scope   *outputScope
	array   *goja.Object
	single  string
	length  int64
	hold    time.Duration
	next    int64
	dropped bool
	resolve func(any) error
	chord   keys.Chord
	// seq is set for a key.sequence or key.type (SCR-26, sequence.go): its steps
	// are the operation, and array, single and length are not used.
	seq *sequenceRun
}

type tapCompletion struct {
	op     *tapOperation
	chord  keys.Chord
	result output.TapCompletion
	resume bool
	// char: the completion of one character of a text step (sequence.go), not of
	// a tap.
	char bool
}

// T-output appends completion data without accessing goja or any Host state.
// The owner slot drains it under its handler watchdog.
type tapCompletionQueue struct {
	mu    sync.Mutex
	items []tapCompletion
	wake  func()
}

func (q *tapCompletionQueue) add(item tapCompletion) {
	q.mu.Lock()
	q.items = append(q.items, item)
	q.mu.Unlock()
	if q.wake != nil {
		q.wake()
	}
}

func (q *tapCompletionQueue) take() []tapCompletion {
	q.mu.Lock()
	items := q.items
	q.items = nil
	q.mu.Unlock()
	return items
}

// drop forgets the queued completions of the scope's taps, and keeps the
// other scopes'.
func (q *tapCompletionQueue) drop(s *outputScope) {
	q.mu.Lock()
	kept := q.items[:0]
	for _, item := range q.items {
		if item.op.scope != s {
			kept = append(kept, item)
		}
	}
	clear(q.items[len(kept):])
	q.items = kept
	q.mu.Unlock()
}

// requeue puts items back before the completions queued since.
func (q *tapCompletionQueue) requeue(items []tapCompletion) {
	if len(items) == 0 {
		return
	}
	q.mu.Lock()
	q.items = append(items, q.items...)
	q.mu.Unlock()
}

func (q *tapCompletionQueue) ready() bool {
	q.mu.Lock()
	ready := len(q.items) != 0
	q.mu.Unlock()
	return ready
}

func (h *Host) startTapCall(s *outputScope, call goja.FunctionCall) goja.Value {
	spec := call.Argument(0)
	op := &tapOperation{scope: s, length: 1}
	if arr, ok := spec.(*goja.Object); ok && arr.ClassName() == "Array" {
		op.array = arr
		op.length = arr.Get("length").ToInteger()
	} else {
		op.single = spec.String()
	}
	hold := time.Duration(0)
	if o, ok := call.Argument(1).(*goja.Object); ok {
		hold = time.Duration(o.Get("holdMs").ToInteger()) * time.Millisecond
	}
	p, resolve, _ := h.VM.NewPromise()
	op.hold, op.resolve = hold, resolve
	s.pendingTaps[op] = true
	h.startNextTap(op)
	return h.VM.ToValue(p)
}

func (h *Host) startNextTap(op *tapOperation) {
	if op.seq != nil {
		h.startNextStep(op)
		return
	}
	s := op.scope
	// A script can provide a very long array of rejected specs. Bound native
	// work per slot turn because goja cannot interrupt a Go native function.
	for steps := 0; s.pendingTaps[op] && op.next < op.length; steps++ {
		if steps == 64 {
			h.tapCompletions.add(tapCompletion{op: op, resume: true})
			return
		}
		spec := op.single
		if op.array != nil {
			spec = op.array.Get(strconv.FormatInt(op.next, 10)).String()
		}
		chord, ok := h.parseChord(s, spec)
		if !ok || !h.active || h.setup || h.candidate {
			op.dropped = true
			op.next++
			continue
		}
		chord = chord.Sided()
		if !s.budget.AllowTap(chord, op.hold) {
			op.dropped = true
			if !s.budgetReported {
				s.addError(fmt.Errorf("%w: key.tap %s (rate or pending main key)", ErrOutputBudget, chord))
				s.budgetReported = true
			}
			op.next++
			continue
		}
		if h.output != nil {
			sink, ok := h.output.(interface {
				TapWithCompletion(output.Owner, keys.Chord, time.Duration, func(output.TapCompletion)) error
			})
			if !ok {
				s.addError(errors.New("script: output sink lacks tap completion"))
				s.budget.CompleteTap(chord)
				op.dropped = true
				op.next++
				continue
			}
			h.recordOutput(s, chord, false)
			op.chord = chord
			s.addError(sink.TapWithCompletion(s.owner, chord, op.hold, func(result output.TapCompletion) {
				h.tapCompletions.add(tapCompletion{op: op, chord: chord, result: result})
			}))
		} else {
			h.recordOutput(s, chord, false)
			op.chord = chord
			if op.hold <= 0 {
				h.tapCompletions.add(tapCompletion{op: op, chord: chord})
			} else {
				s.loop.After(op.hold, func() {
					h.tapCompletions.add(tapCompletion{op: op, chord: chord})
				})
			}
		}
		return
	}
	if s.pendingTaps[op] {
		h.finishTap(op)
	}
}

func (h *Host) finishTap(op *tapOperation) {
	delete(op.scope.pendingTaps, op)
	op.scope.addError(op.resolve(map[string]bool{"dropped": op.dropped}))
}

// processCompletions handles completions of one scope's taps, under that
// scope's watchdog call.
func (h *Host) processCompletions(items []tapCompletion) {
	for _, item := range items {
		s := item.op.scope
		if !s.pendingTaps[item.op] {
			continue
		}
		if item.resume {
			h.startNextTap(item.op)
			continue
		}
		if item.char {
			h.charTyped(item.op, item.result)
			continue
		}
		s.budget.CompleteTap(item.chord)
		h.recordOutput(s, item.chord, true)
		item.op.chord = keys.Chord{}
		item.op.dropped = item.op.dropped || item.result.Dropped
		item.op.next++
		if item.op.seq != nil {
			item.op.seq.stepEnded(h.clock.Now())
		}
		if item.result.Canceled {
			h.finishTap(item.op)
		} else {
			h.startNextTap(item.op)
		}
	}
}

// runCompletions processes one snapshot of the queued completions. A hold=0
// array may enqueue its next completion synchronously; the wake lets
// another budgeted owner turn handle it. Each scope's completions run in a
// call of their own, so a macro's errors are its own (design §7); a run that
// has ended ignores its completions. stop reports an error after which
// RunDue must not run more callbacks: any error of the runtime's scope, as
// before macros, or an interruption.
func (h *Host) runCompletions() (stop bool, err error) {
	items := h.tapCompletions.take()
	if len(items) == 0 {
		return false, nil
	}
	var scopes []*outputScope
	byScope := make(map[*outputScope][]tapCompletion)
	for _, item := range items {
		s := item.op.scope
		if _, ok := byScope[s]; !ok {
			scopes = append(scopes, s)
		}
		byScope[s] = append(byScope[s], item)
	}
	var errs []error
	for i, s := range scopes {
		if s.ended {
			continue
		}
		group := byScope[s]
		var callErr error
		if s.macro == nil {
			_, callErr = h.withWatchdog(WatchdogCallHandler, func() (goja.Value, error) {
				h.processCompletions(group)
				return nil, h.takeErrors()
			})
			callErr = Diagnose(PhaseHandler, callErr)
		} else {
			callErr = h.callMacro(s, func() error {
				h.processCompletions(group)
				return nil
			})
		}
		if callErr == nil {
			continue
		}
		errs = append(errs, callErr)
		if s.macro == nil || isInterruption(callErr) {
			// The other scopes' completions wait for the next turn.
			var rest []tapCompletion
			for _, later := range scopes[i+1:] {
				rest = append(rest, byScope[later]...)
			}
			h.tapCompletions.requeue(rest)
			return true, errors.Join(errs...)
		}
	}
	return false, errors.Join(errs...)
}

// cancelTaps ends the scope's taps. Their Promises are settled only for a
// script's own key.releaseAll.
func (h *Host) cancelTaps(s *outputScope, settlePromises bool) {
	// Detach every old operation and its queued Arbiter notifications before
	// resolving any Promise. A script-level releaseAll continuation may start
	// a new tap; it must not be caught by this cancellation pass.
	old := make([]*tapOperation, 0, len(s.pendingTaps))
	for op := range s.pendingTaps {
		delete(s.pendingTaps, op)
		old = append(old, op)
		if op.seq != nil {
			op.seq.timer.Cancel()
		}
		if op.chord.IsValid() {
			h.recordOutput(s, op.chord, true)
			s.budget.CompleteTap(op.chord)
		}
		op.dropped = true
	}
	h.tapCompletions.drop(s)
	if settlePromises {
		for _, op := range old {
			h.finishTap(op)
		}
	}
}

// NextDeadline includes an output completion already delivered from T-output,
// and the timers and time limits of the runs of macros.
// A disposed runtime runs nothing more.
func (h *Host) NextDeadline() (clock.Time, bool) {
	if h.disposed {
		return 0, false
	}
	if h.tapCompletions.ready() || len(h.Loop.micro) > 0 || h.environmentPending() || len(h.macroErrs) > 0 {
		return h.clock.Now(), true
	}
	d, ok := h.Loop.NextDeadline()
	for _, s := range h.runs {
		if t, due := s.loop.NextDeadline(); due && (!ok || t < d) {
			d, ok = t, true
		}
		if !ok || s.deadline < d {
			d, ok = s.deadline, true
		}
	}
	return d, ok
}
