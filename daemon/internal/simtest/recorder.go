package simtest

import (
	"errors"
	"slices"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// ErrBlocked is the error of a FailNext call given a nil error.
var ErrBlocked = errors.New("simtest: injection blocked")

// Record is one event offered to a Recorder.
type Record struct {
	Event    output.KeyEvent
	T        clock.Time // clock time of the Send call
	Batch    int        // index of the Send call, from 0
	Injected bool       // false when the call left it out
}

// Edge returns r as an output edge: a press is a Down edge and a release
// an Up edge, named by the event's Key, or else by its code as a raw
// KeySpec (sc:0x2C, sc:0xE01D) or vk:0x13 for an event injected by VK.
//
// A pointer event is a Move or Jump edge: the pointer has no press and no
// release.
func (r Record) Edge() Edge {
	switch r.Event.Mouse {
	case keys.MouseMove:
		return Edge{Name: "move", Kind: Move, Dx: r.Event.Dx, Dy: r.Event.Dy, T: r.T}
	case keys.MouseJump:
		return Edge{Name: "jump", Kind: Jump, Dx: r.Event.Dx, Dy: r.Event.Dy, T: r.T}
	}
	e := Edge{Name: r.Event.ID().String(), Kind: Down, T: r.T}
	if r.Event.Key.IsValid() {
		e.Name = r.Event.Key.String()
	}
	if r.Event.Up {
		e.Kind = Up
	}
	return e
}

// Recorder is a fake output.Sender. It records every event it is offered,
// tracks which physical keys the injected events left down (by
// output.KeyID, like the Arbiter), and fails or warns on demand.
//
// A Recorder is not safe for concurrent use.
type Recorder struct {
	// Inject, when set, decides how many events of each Send call are
	// injected and what error the call returns, for calls FailNext and
	// WarnNext did not plan. Counts are clamped to [0, len(events)].
	Inject func(events []output.KeyEvent) (int, error)

	clk     clock.Clock
	records []Record
	calls   int
	plans   []plan
	down    []output.KeyEvent // injected presses still down, in press order
}

type plan struct {
	n    int // events to inject, unless warn
	err  error
	warn bool // inject every event and still return err
}

// NewRecorder returns a Recorder that stamps records with clk's time.
func NewRecorder(clk clock.Clock) *Recorder { return &Recorder{clk: clk} }

// FailNext makes a coming Send call inject at most n events and return
// err, or ErrBlocked when err is nil. Plans apply to the calls in the
// order they were made, before Inject.
func (r *Recorder) FailNext(n int, err error) {
	if err == nil {
		err = ErrBlocked
	}
	r.plans = append(r.plans, plan{n: n, err: err})
}

// WarnNext makes a coming Send call inject every event and still return
// err: a warning, in output.Sender's terms.
func (r *Recorder) WarnNext(err error) {
	r.plans = append(r.plans, plan{err: err, warn: true})
}

// Send implements output.Sender.
func (r *Recorder) Send(events []output.KeyEvent) (int, error) {
	n, err := len(events), error(nil)
	switch {
	case len(r.plans) > 0:
		p := r.plans[0]
		r.plans = r.plans[1:]
		err = p.err
		if !p.warn {
			n = p.n
		}
	case r.Inject != nil:
		n, err = r.Inject(events)
	}
	n = min(max(n, 0), len(events))
	now := r.clk.Now()
	for i, e := range events {
		r.records = append(r.records, Record{Event: e, T: now, Batch: r.calls, Injected: i < n})
		if i < n {
			r.apply(e)
		}
	}
	r.calls++
	return n, err
}

// apply updates the physical key state with an injected event. A repeated
// press keeps the key's place; a release of a key that is up is ignored.
func (r *Recorder) apply(e output.KeyEvent) {
	if e.Mouse.IsPointer() {
		return // the pointer moves; no key goes down
	}
	i := slices.IndexFunc(r.down, func(d output.KeyEvent) bool { return d.ID() == e.ID() })
	switch {
	case e.Up && i >= 0:
		r.down = slices.Delete(r.down, i, i+1)
	case !e.Up && i < 0:
		r.down = append(r.down, e)
	}
}

// Records returns every event offered so far, in order.
func (r *Recorder) Records() []Record { return slices.Clone(r.records) }

// Calls returns the number of Send calls so far.
func (r *Recorder) Calls() int { return r.calls }

// Down returns the first injected press of each key that is physically
// down, in press order.
func (r *Recorder) Down() []output.KeyEvent { return slices.Clone(r.down) }

// Edges returns the injected events as output edges.
func (r *Recorder) Edges() []Edge {
	var edges []Edge
	for _, rec := range r.records {
		if rec.Injected {
			edges = append(edges, rec.Edge())
		}
	}
	return edges
}
