package script

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// key.sequence and key.type (SCR-26, API 0.21, ADR-0070). They take the steps of
// the profile's sequence action: tap a shortcut, wait, type a text. The host
// runs them one step at a time, like a key.tap array: it asks the output for the
// tap of a step, or for one character, and starts the next when the output has
// reported the end of it, so the owner's release cancels what is left through the
// same completion path (a Promise that resolves {dropped:true}), and the output
// limiter is asked for each step as it starts:
//   - a shortcut costs a press and a release of each of its keys, as for key.tap;
//   - a character costs a press and a release of each UTF-16 unit it is sent as
//     (two units for a character outside the Basic Multilingual Plane), which is
//     two key changes for most; a newline and a tab are an Enter and a Tab tap
//     and cost two;
//   - a wait costs nothing.
//
// The timing is the profile's defaults (engine.DefaultTapGap, 20 ms, the least
// time between two steps and between two characters; a wait counts from the
// release of the step before it, and waits one after another add up), as for
// the sequence action. A script that wants another speed writes its own loop with
// key.tap and sleep.

// The limits of the steps of key.sequence are keys's (MaxSteps, MinWait, MaxWait,
// MaxTextUnits).

// sequenceGap is the least time between two steps and between two characters.
const sequenceGap = engine.DefaultTapGap

// sequenceRun is the state of a key.sequence or key.type under way, which
// belongs to the Host owner goroutine with its tapOperation.
type sequenceRun struct {
	steps []keys.Step
	// at is when the next step may start; mark is where a wait counts from: the end of
	// the last tap or text, and of each wait since.
	at, mark clock.Time
	// text and textNext are the characters of the text step under way, and the one to
	// type next; text is nil between texts.
	text     []rune
	textNext int
	// afterWait: the last step to run was a wait, so a sequence that has no more steps
	// is over when the wait is, and not at the end of the step before it.
	afterWait bool
	// timer is the wait, or the gap, the operation is in.
	timer Handle
}

// stepEnded notes that a tap or a text has ended at now: the next step comes a gap
// later, and a wait counts from now.
func (r *sequenceRun) stepEnded(now clock.Time) {
	r.mark = now
	r.at = now.Add(sequenceGap)
	r.afterWait = false
}

// startSequenceCall is key.sequence(steps).
func (h *Host) startSequenceCall(s *outputScope, call goja.FunctionCall) goja.Value {
	return h.startSequence(s, h.parseSteps("key.sequence", call.Argument(0)))
}

// startTypeCall is key.type(text).
func (h *Host) startTypeCall(s *outputScope, call goja.FunctionCall) goja.Value {
	text, ok := call.Argument(0).Export().(string)
	if !ok {
		panic(h.VM.NewTypeError("key.type: text must be a string"))
	}
	if err := keys.ValidateText(text); err != nil {
		panic(h.VM.NewTypeError("key.type: %s", err))
	}
	return h.startSequence(s, []keys.Step{keys.TextStep(text)})
}

// startSequence starts the steps, already checked, and returns the Promise of
// their end.
func (h *Host) startSequence(s *outputScope, steps []keys.Step) goja.Value {
	op := &tapOperation{scope: s}
	now := h.clock.Now()
	op.seq = &sequenceRun{steps: steps, at: now, mark: now}
	p, resolve, _ := h.VM.NewPromise()
	op.resolve = resolve
	s.pendingTaps[op] = true
	h.startNextTap(op)
	return h.VM.ToValue(p)
}

// parseSteps checks the steps a script gave to name, an array of 1 to
// keys.MaxSteps objects with one member each, and returns them. It throws a
// TypeError naming the step that is wrong, as the profile names its pointer.
func (h *Host) parseSteps(name string, v goja.Value) []keys.Step {
	arr, ok := v.(*goja.Object)
	if !ok || arr.ClassName() != "Array" {
		panic(h.VM.NewTypeError("%s: steps must be an array of 1 to %d steps", name, keys.MaxSteps))
	}
	n := arr.Get("length").ToInteger()
	if n < 1 || n > keys.MaxSteps {
		panic(h.VM.NewTypeError("%s: steps must be an array of 1 to %d steps, not %d", name, keys.MaxSteps, n))
	}
	steps := make([]keys.Step, 0, n)
	for i := range int(n) {
		step, err := h.parseStep(arr.Get(strconv.Itoa(i)))
		if err != nil {
			panic(h.VM.NewTypeError("%s: step %d: %s", name, i, err))
		}
		steps = append(steps, step)
	}
	return steps
}

// parseStep checks one step: an object with just one of keys, wait and text.
func (h *Host) parseStep(v goja.Value) (keys.Step, error) {
	o, ok := v.(*goja.Object)
	if !ok || o.ClassName() != "Object" {
		return keys.Step{}, errors.New(`a step is an object such as {keys: "Ctrl+A"}, {wait: 100} or {text: "hi"}`)
	}
	var kind string
	for _, k := range o.Keys() {
		switch k {
		case "keys", "wait", "text":
		default:
			return keys.Step{}, fmt.Errorf("unknown property %q; a step has keys, wait or text", k)
		}
		if kind != "" {
			return keys.Step{}, fmt.Errorf("a step has only one of keys, wait and text, not %s and %s", kind, k)
		}
		kind = k
	}
	switch kind {
	case "keys":
		spec, ok := o.Get("keys").Export().(string)
		if !ok {
			return keys.Step{}, errors.New("keys must be a string")
		}
		chord, err := keys.Parse(strings.TrimSpace(spec))
		if err != nil {
			return keys.Step{}, fmt.Errorf("keys %q: %w", spec, err)
		}
		return keys.KeysStep(chord.Sided()), nil
	case "wait":
		var ms float64
		switch n := o.Get("wait").Export().(type) {
		case int64:
			ms = float64(n)
		case float64:
			if n != math.Trunc(n) {
				return keys.Step{}, errors.New("wait must be a whole number of milliseconds")
			}
			ms = n
		default:
			return keys.Step{}, errors.New("wait must be a number of milliseconds")
		}
		if ms < float64(keys.MinWait/time.Millisecond) || ms > float64(keys.MaxWait/time.Millisecond) {
			return keys.Step{}, fmt.Errorf("wait must be %d to %d milliseconds", keys.MinWait/time.Millisecond, keys.MaxWait/time.Millisecond)
		}
		return keys.WaitStep(time.Duration(ms) * time.Millisecond), nil
	case "text":
		text, ok := o.Get("text").Export().(string)
		if !ok {
			return keys.Step{}, errors.New("text must be a string")
		}
		if err := keys.ValidateText(text); err != nil {
			return keys.Step{}, err
		}
		return keys.TextStep(text), nil
	}
	return keys.Step{}, errors.New("a step needs one of keys, wait and text")
}

// typeSink is the output a text needs: a character at a time, with the end of it
// told. The Arbiter and the RemoteArbiter have it.
type typeSink interface {
	TypeTextWithCompletion(output.Owner, string, time.Duration, func(output.TapCompletion)) error
}

// startNextStep runs the steps of op from the one it is at, until one needs the
// output to finish (a tap, a character) or time to pass (a wait, a gap), which
// resume it, and ends the operation after the last. Like startNextTap it bounds
// the work of a call.
func (h *Host) startNextStep(op *tapOperation) {
	s := op.scope
	r := op.seq
	for steps := 0; s.pendingTaps[op]; steps++ {
		if steps == 64 {
			h.tapCompletions.add(tapCompletion{op: op, resume: true})
			return
		}
		now := h.clock.Now()
		if r.text == nil && op.next >= int64(len(r.steps)) && (!r.afterWait || r.at <= now) {
			// Over: after the last tap or text at once, after a last wait when it ends.
			h.finishTap(op)
			return
		}
		if r.at > now {
			r.timer = s.loop.After(r.at.Sub(now), func() { h.tapCompletions.add(tapCompletion{op: op, resume: true}) })
			return
		}
		if r.text != nil {
			if h.typeNextChar(op) {
				return
			}
			continue
		}
		step := r.steps[op.next]
		if !h.active || h.setup || h.candidate {
			// Output is off (setup, a candidate that is not yet running): the step is lost.
			op.dropped = true
			op.next++
			continue
		}
		switch step.Kind {
		case keys.StepWait:
			op.next++
			r.mark = r.mark.Add(step.Wait)
			r.at = max(r.at, r.mark)
			r.afterWait = true
		case keys.StepText:
			r.text, r.textNext = []rune(step.Text), 0
		default:
			if h.startStepTap(op, step.Chord) {
				return
			}
		}
	}
}

// startStepTap taps the chord of the step op is at, and reports whether the tap is
// under way, to be told when it ends; otherwise the step is lost and op is at the
// next.
func (h *Host) startStepTap(op *tapOperation, chord keys.Chord) bool {
	s := op.scope
	chord = chord.Sided()
	if !s.budget.AllowTap(chord, op.hold) {
		op.dropped = true
		if !s.budgetReported {
			s.addError(fmt.Errorf("%w: key.sequence %s (rate or pending main key)", ErrOutputBudget, chord))
			s.budgetReported = true
		}
		op.next++
		op.seq.stepEnded(h.clock.Now())
		return false
	}
	h.recordOutput(s, chord, false)
	op.chord = chord
	if h.output == nil {
		if op.hold <= 0 {
			h.tapCompletions.add(tapCompletion{op: op, chord: chord})
		} else {
			s.loop.After(op.hold, func() { h.tapCompletions.add(tapCompletion{op: op, chord: chord}) })
		}
		return true
	}
	sink, ok := h.output.(interface {
		TapWithCompletion(output.Owner, keys.Chord, time.Duration, func(output.TapCompletion)) error
	})
	if !ok {
		s.addError(errors.New("script: output sink lacks tap completion"))
		s.budget.CompleteTap(chord)
		op.chord = keys.Chord{}
		op.dropped = true
		op.next++
		op.seq.stepEnded(h.clock.Now())
		return false
	}
	s.addError(sink.TapWithCompletion(s.owner, chord, op.hold, func(result output.TapCompletion) {
		h.tapCompletions.add(tapCompletion{op: op, chord: chord, result: result})
	}))
	return true
}

// typeNextChar types the next character of the text step op is in, and reports
// whether it is under way, to be told when it ends; otherwise the rest of the
// text is lost (the output limiter refused a character, or there is no output
// that types) and op is at the next step.
func (h *Host) typeNextChar(op *tapOperation) bool {
	s := op.scope
	r := op.seq
	c := r.text[r.textNext]
	changes := 2 * utf16.RuneLen(c)
	if !s.budget.Allow(changes) {
		op.dropped = true
		if !s.budgetReported {
			s.addError(fmt.Errorf("%w: key.type %q (%d changes including release)", ErrOutputBudget, string(c), changes))
			s.budgetReported = true
		}
		h.endText(op)
		return false
	}
	h.outputs = append(h.outputs, OutputEvent{Text: string(c), Time: h.clock.Now(), Owner: s.owner})
	if h.output == nil {
		h.tapCompletions.add(tapCompletion{op: op, char: true})
		return true
	}
	sink, ok := h.output.(typeSink)
	if !ok {
		s.addError(errors.New("script: output sink cannot type text"))
		op.dropped = true
		h.endText(op)
		return false
	}
	s.addError(sink.TypeTextWithCompletion(s.owner, string(c), 0, func(result output.TapCompletion) {
		h.tapCompletions.add(tapCompletion{op: op, result: result, char: true})
	}))
	return true
}

// charTyped handles the end of a character: the output has typed it, or lost it,
// or its owner was released, which ends the operation.
func (h *Host) charTyped(op *tapOperation, result output.TapCompletion) {
	op.dropped = op.dropped || result.Dropped
	if result.Canceled {
		h.finishTap(op)
		return
	}
	r := op.seq
	r.textNext++
	r.mark = h.clock.Now()
	r.at = r.mark.Add(sequenceGap)
	if r.textNext >= len(r.text) {
		h.endText(op)
	}
	h.startNextTap(op)
}

// endText ends the text step op is in, typed or given up, and goes on to the next
// step a gap after the last character.
func (h *Host) endText(op *tapOperation) {
	r := op.seq
	r.text, r.textNext = nil, 0
	op.next++
	r.stepEnded(h.clock.Now())
}
