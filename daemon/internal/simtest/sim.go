package simtest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// settleLimit is how much virtual time Settle may use.
const settleLimit = time.Minute

// Sim is a real Arbiter writing to a Recorder, on a fake clock.
type Sim struct {
	Clock *clock.Fake
	Out   *Recorder
	Arb   *output.Arbiter

	t        testing.TB
	consumed int     // output edges consumed by ExpectOutput and ClearOutput
	errs     []error // Arbiter and script timer errors not yet taken by Errors
	scripts  []*Script
	runtimes []Runtime // driven with Drive
	cycle    uint64
}

// Option configures New.
type Option func(*config)

type config struct {
	arb  output.ArbiterOptions
	mask bool
}

// WithArbiterOptions sets the options of the Sim's Arbiter.
func WithArbiterOptions(opts output.ArbiterOptions) Option {
	return func(c *config) { c.arb = opts }
}

// WithMenuMask puts an output.MenuMask between the Arbiter and the
// Recorder, as output.Loop does: the Recorder records the mask key's events
// (vk:0xE8), and FailNext and Inject count them.
func WithMenuMask() Option {
	return func(c *config) { c.mask = true }
}

// New returns a Sim at time 0. When t's test ends, it checks with
// AssertNoneHeld that nothing is held, physically or logically, and that
// Errors took every Advance error. It does not settle declarative output or
// pending taps itself: loaded Script runtimes are disposed first so their
// owner-scoped keys cannot outlive the test.
func New(t testing.TB, opts ...Option) *Sim {
	t.Helper()
	var cfg config
	for _, opt := range opts {
		opt(&cfg)
	}
	clk := clock.NewFake()
	out := NewRecorder(clk)
	var send output.Sender = out
	if cfg.mask {
		send = output.NewMenuMask(out)
	}
	s := &Sim{Clock: clk, Out: out, Arb: output.NewArbiter(clk, send, cfg.arb), t: t}
	t.Cleanup(func() {
		t.Helper()
		for _, r := range s.scripts {
			if r != nil {
				if err := r.Dispose(); err != nil {
					t.Errorf("simtest: dispose script: %v", err)
				}
			}
		}
		for _, r := range s.runtimes {
			if err := r.Dispose(); err != nil {
				t.Errorf("simtest: dispose runtime: %v", err)
			}
		}
		if len(s.errs) > 0 {
			t.Errorf("simtest: errors the test did not take with Errors:\n%v", errors.Join(s.errs...))
		}
		AssertNoneHeld(t, s.Arb, s.Out)
	})
	return s
}

// Advance moves virtual time forward by d, like AdvanceTo.
func (s *Sim) Advance(d time.Duration) {
	s.t.Helper()
	s.AdvanceTo(s.Clock.Now().Add(d))
}

// AdvanceTo moves virtual time to target, stopping at every fake timer and
// Arbiter deadline on the way, in order, to fire the timers and run
// Arbiter.Advance at their exact time. At a tie the timers fire first. An
// Arbiter deadline already past runs at once. Errors from Arbiter.Advance
// and script timers are kept for Errors. AdvanceTo fails the test if target
// is before now.
func (s *Sim) AdvanceTo(target clock.Time) {
	s.t.Helper()
	if now := s.Clock.Now(); target < now {
		s.t.Fatalf("simtest: AdvanceTo(%v) is before now, %v", target, now)
	}
	for {
		next, ok := s.next()
		if !ok || next > target {
			break
		}
		if next > s.Clock.Now() {
			s.Clock.Set(next)
		}
		now := s.Clock.Now()
		for _, r := range s.scripts {
			if r != nil {
				if err := r.RunDue(); err != nil {
					s.errs = append(s.errs, err)
				}
			}
		}
		for _, r := range s.runtimes {
			if err := r.RunDue(); err != nil {
				s.errs = append(s.errs, err)
			}
		}
		if d, ok := s.Arb.NextDeadline(); ok && d <= now {
			if err := s.Arb.Advance(); err != nil {
				s.errs = append(s.errs, err)
			}
			if d, ok := s.Arb.NextDeadline(); ok && d <= now {
				s.t.Fatalf("simtest: Arbiter deadline %v is still due after Advance at %v", d, now)
			}
		}
	}
	s.Clock.Set(target)
}

// next returns the earliest fake timer or Arbiter deadline.
func (s *Sim) next() (clock.Time, bool) {
	t, ok := s.Clock.Next()
	for _, r := range s.scripts {
		if r == nil {
			continue
		}
		if d, dok := r.NextDeadline(); dok && (!ok || d < t) {
			t, ok = d, true
		}
	}
	for _, r := range s.runtimes {
		if d, dok := r.NextDeadline(); dok && (!ok || d < t) {
			t, ok = d, true
		}
	}
	if d, dok := s.Arb.NextDeadline(); dok && (!ok || d < t) {
		t, ok = d, true
	}
	return t, ok
}

// Runtime is a script runtime, such as a script.Host or script.Lifecycle,
// whose output goes to the Sim's Arbiter.
type Runtime interface {
	RunDue() error
	NextDeadline() (clock.Time, bool)
	Dispose() error
}

// Drive makes Advance and AdvanceTo run r at each of its deadlines, as they
// run a loaded Script, keeping its errors for Errors. The Sim's cleanup
// disposes r before it checks that nothing is held.
func (s *Sim) Drive(r Runtime) { s.runtimes = append(s.runtimes, r) }

// Settle advances until no fake timer or Arbiter deadline is left. It
// fails the test if that takes more than a minute of virtual time.
func (s *Sim) Settle() {
	s.t.Helper()
	limit := s.Clock.Now().Add(settleLimit)
	for {
		next, ok := s.next()
		if !ok {
			return
		}
		if next > limit {
			s.t.Fatalf("simtest: still busy after %v of virtual time, next deadline %v; held: %s",
				settleLimit, next, heldString(s.Arb.Held()))
		}
		s.AdvanceTo(max(next, s.Clock.Now()))
	}
}

// Errors returns Arbiter.Advance and script timer errors since the last call
// and forgets them.
func (s *Sim) Errors() []error {
	errs := s.errs
	s.errs = nil
	return errs
}

// ExpectOutput compares the output recorded since the last ExpectOutput or
// ClearOutput, as edges, with the trace want, then marks it consumed. The
// output is what the Recorder injected, stamped with the send time.
func (s *Sim) ExpectOutput(want string) {
	s.t.Helper()
	w, err := ParseTrace(want)
	if err != nil {
		s.t.Fatalf("simtest: ExpectOutput: %v", err)
	}
	edges := s.Out.Edges()
	got := edges[s.consumed:]
	s.consumed = len(edges)
	if !slices.Equal(got, w) {
		s.t.Errorf("output:\n got %s\nwant %s", traceOrNone(got), traceOrNone(w))
	}
}

// ClearOutput marks all output recorded so far consumed.
func (s *Sim) ClearOutput() { s.consumed = len(s.Out.Edges()) }

// Output returns all recorded output as a trace, consumed or not.
func (s *Sim) Output() string { return FormatTrace(s.Out.Edges()) }

func traceOrNone(edges []Edge) string {
	if len(edges) == 0 {
		return "(none)"
	}
	return FormatTrace(edges)
}

// AssertNoneHeld fails the test, with one error listing every key, if r
// has a key physically down or a has a key down or pending release. Either
// may be nil.
func AssertNoneHeld(t testing.TB, a *output.Arbiter, r *Recorder) {
	t.Helper()
	var msgs []string
	if r != nil {
		for _, e := range r.Down() {
			msgs = append(msgs, keyName(e)+" is physically down")
		}
	}
	if a != nil {
		if n := a.Queued(); n > 0 {
			msgs = append(msgs, fmt.Sprintf("Arbiter: %d queued taps or sequences", n))
		}
		for _, h := range a.Held() {
			msgs = append(msgs, "Arbiter: "+heldKeyString(h))
		}
	}
	if len(msgs) > 0 {
		t.Errorf("simtest: keys left held:\n\t%s", strings.Join(msgs, "\n\t"))
	}
}

// heldString lists held keys for messages.
func heldString(held []output.HeldKey) string {
	if len(held) == 0 {
		return "none"
	}
	s := make([]string, len(held))
	for i, h := range held {
		s[i] = heldKeyString(h)
	}
	return strings.Join(s, "; ")
}

func heldKeyString(h output.HeldKey) string {
	if h.Pending {
		return keyName(h.Press) + " pending release"
	}
	owners := make([]string, len(h.Owners))
	for i, o := range h.Owners {
		owners[i] = o.String()
	}
	return fmt.Sprintf("%s held by %s", keyName(h.Press), strings.Join(owners, ", "))
}

// keyName names the key of e as output edges do.
func keyName(e output.KeyEvent) string { return Record{Event: e}.Edge().Name }
