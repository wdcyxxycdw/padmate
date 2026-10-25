package script

import (
	"fmt"
	"slices"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// outputScope is the output of one owner (SCR-50, design §6.2): its keys
// held with key.down, its taps still running, its errors and its timers. A
// runtime has one for the legacy script's key API, and each run of a macro
// has one of its own, so a run's end or key.releaseAll touches nothing
// else. Gesture holds (holdKeys) keep their own owners, as before.
type outputScope struct {
	owner output.Owner
	held  map[string]bool
	// order lists the chords of held in the order they went down; releases
	// go in reverse, so the output does not depend on map order.
	order       []string
	pendingTaps map[*tapOperation]bool
	// budget shares the runtime's token bucket; the pending main key of each
	// tap is the scope's own.
	budget         *OutputBudget
	errs           []error
	budgetReported bool
	// loop holds the scope's timers. The runtime scope uses Host.Loop; a
	// run has a loop of its own, which its end clears.
	loop *EventLoop
	// macro is the macro of a run's scope, nil for the runtime's scope.
	macro *macroFile
	// deadline is when a run is canceled (MacroRunLimit).
	deadline clock.Time
	ended    bool
}

func newOutputScope(owner output.Owner, budget *OutputBudget, loop *EventLoop) *outputScope {
	return &outputScope{owner: owner, held: make(map[string]bool), pendingTaps: make(map[*tapOperation]bool), budget: budget, loop: loop}
}

func (s *outputScope) addError(err error) {
	if err != nil {
		s.errs = append(s.errs, err)
	}
}

// takeErrors returns and forgets the scope's errors. A new budget error can
// be reported once again afterwards.
func (s *outputScope) takeErrors() []error {
	errs := s.errs
	s.errs = nil
	s.budgetReported = false
	return errs
}

func (h *Host) parseChord(s *outputScope, spec string) (keys.Chord, bool) {
	chord, err := keys.Parse(strings.TrimSpace(spec))
	if err != nil {
		s.addError(fmt.Errorf("script: key %q: %w", spec, err))
		return keys.Chord{}, false
	}
	return chord, true
}

func (h *Host) recordOutput(s *outputScope, chord keys.Chord, up bool) {
	h.outputs = append(h.outputs, OutputEvent{Spec: chord.String(), Up: up, Time: h.clock.Now(), Owner: s.owner, Chord: chord})
}

func (h *Host) keyDown(s *outputScope, spec string) {
	if !h.active || h.setup || h.candidate {
		return
	}
	chord, ok := h.parseChord(s, spec)
	if ok {
		h.keyDownChord(s, chord)
	}
}

func (h *Host) keyUp(s *outputScope, spec string) {
	chord, ok := h.parseChord(s, spec)
	if ok {
		h.keyUpChord(s, chord)
	}
}

func (h *Host) keyDownChord(s *outputScope, chord keys.Chord) {
	chord = chord.Sided()
	spec := chord.String()
	if s.held[spec] {
		return
	}
	changes := 2 * len(chord.Keys())
	if !s.budget.Allow(changes) {
		s.addError(fmt.Errorf("%w: key.down %s (%d changes including release)", ErrOutputBudget, spec, changes))
		return
	}
	s.held[spec] = true
	s.order = append(s.order, spec)
	h.recordOutput(s, chord, false)
	if h.output != nil {
		s.addError(h.output.Down(s.owner, chord))
	}
}

func (h *Host) keyUpChord(s *outputScope, chord keys.Chord) {
	chord = chord.Sided()
	spec := chord.String()
	if !s.held[spec] {
		return
	}
	delete(s.held, spec)
	if i := slices.Index(s.order, spec); i >= 0 {
		s.order = slices.Delete(s.order, i, i+1)
	}
	h.recordOutput(s, chord, true)
	if h.output != nil {
		s.addError(h.output.Up(s.owner, chord))
	}
}

// upHeld releases the scope's held chords, last pressed first, with Up:
// the Arbiter keeps their minimum press time and the references of other
// owners. It reports whether every Up reached the Arbiter.
func (h *Host) upHeld(s *outputScope) bool {
	ok := true
	for i := len(s.order) - 1; i >= 0; i-- {
		spec := s.order[i]
		chord, err := keys.Parse(spec)
		if err != nil {
			// A held spec is always a canonical chord.
			continue
		}
		before := len(s.errs)
		h.keyUpChord(s, chord)
		if len(s.errs) != before {
			ok = false
		}
	}
	return ok
}

// releaseScope releases everything the scope holds: its keys, and its owner
// at the Arbiter, which ends the taps it has there. Script-initiated
// releases settle the tap Promises under the caller's watchdog; safety
// boundaries abandon them so no old JS can run while keys are released.
func (h *Host) releaseScope(s *outputScope, settlePromises bool) {
	h.upHeld(s)
	s.budget.ReleaseAll()
	if h.output != nil {
		s.addError(h.output.ReleaseOwner(s.owner))
	}
	h.cancelTaps(s, settlePromises)
}

// isChordHeld reports whether the scope holds chord with key.down.
func (s *outputScope) isChordHeld(chord keys.Chord) bool {
	return s.held[chord.Sided().String()]
}
