package mapwizard

import (
	"errors"
	"slices"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/sdlmapping"
)

// Step is one control the wizard asks for.
type Step struct {
	// ID is a stable name of the step.
	ID string
	// Output is the SDL element key the answer binds: "a", "dpup", "lefttrigger",
	// or "-leftx" and "+leftx" for the two directions of a stick axis.
	Output string
}

// DefaultSteps is the order of the prompts (ADR-0072): the face buttons, the
// d-pad, the left stick (left, right, up, down) and its click, the same for
// the right stick, the shoulders and triggers, View, Menu and Home. SDL names
// View back and Menu start.
var DefaultSteps = []Step{
	{"a", "a"}, {"b", "b"}, {"x", "x"}, {"y", "y"},
	{"dpup", "dpup"}, {"dpdown", "dpdown"}, {"dpleft", "dpleft"}, {"dpright", "dpright"},
	{"ls-left", "-leftx"}, {"ls-right", "+leftx"}, {"ls-up", "-lefty"}, {"ls-down", "+lefty"}, {"ls-click", "leftstick"},
	{"rs-left", "-rightx"}, {"rs-right", "+rightx"}, {"rs-up", "-righty"}, {"rs-down", "+righty"}, {"rs-click", "rightstick"},
	{"lb", "leftshoulder"}, {"rb", "rightshoulder"}, {"lt", "lefttrigger"}, {"rt", "righttrigger"},
	{"view", "back"}, {"menu", "start"}, {"home", "guide"},
}

// The Codes of a Warning.
const (
	// WarnNoBack: View (SDL back) is not mapped.
	WarnNoBack = "noBack"
	// WarnNoStart: Menu (SDL start) is not mapped.
	WarnNoStart = "noStart"
)

// Warning is something the user should know before saving the mapping.
// View and Menu are the emergency combo, and one of them is the function key by
// default (ADR-0075).
type Warning struct {
	Code string
	Step string // the step the warning is about, when there is one
}

// Result is what Feed concludes.
type Result struct {
	// Bound is the step that got an element from this event, or "".
	Bound string
	// Input is its element.
	Input Input
	// Cleared are the steps whose binding was cleared because Bound took the
	// same element: SDL's rule is that the later binding wins, and the
	// user is to be told.
	Cleared []string
	// Rejected is why the event was refused ("diagonal"), or "".
	Rejected string
}

// Session is one run of the wizard over a joystick: the quiet period, then
// the prompts of Steps in order, with skip, back and redo. It is not safe
// for concurrent use.
type Session struct {
	guid  string
	steps []Step
	det   *Detector
	start int64
	began bool // the quiet period is over and the prompt has begun
	cur   int  // index of the step asked for; len(steps) when finished
	bound []*Input
}

// NewSession starts a session for the joystick with GUID guid at time now.
// The events of the first Quiet nanoseconds only observe noise.
func NewSession(guid string, info Info, now int64) *Session {
	return &Session{guid: strings.ToLower(guid), steps: DefaultSteps, det: NewDetector(info), start: now,
		bound: make([]*Input, len(DefaultSteps))}
}

// Steps returns the prompts, in order.
func (s *Session) Steps() []Step { return slices.Clone(s.steps) }

// Quiet reports whether the quiet period is still running at time now.
func (s *Session) Quiet(now int64) bool { return now-s.start < Quiet }

// Noisy returns the axes that the quiet period found moving on their own.
func (s *Session) Noisy() []int { return s.det.NoisyAxes() }

// Current returns the index and the step being asked for; ok is false when
// every step is answered or skipped.
func (s *Session) Current() (index int, step Step, ok bool) {
	if s.cur >= len(s.steps) {
		return s.cur, Step{}, false
	}
	return s.cur, s.steps[s.cur], true
}

// Done reports whether the session has gone through every step.
func (s *Session) Done() bool { return s.cur >= len(s.steps) }

// Tick moves the clock to now: when the quiet period is over, the first
// prompt begins.
func (s *Session) Tick(now int64) {
	if !s.began && !s.Quiet(now) {
		s.began = true
		s.det.Begin()
	}
}

// Feed takes a raw event. During the quiet period it only observes; after
// it, an event that the Detector accepts binds the current step, which then
// moves on to the next.
func (s *Session) Feed(ev Event) Result {
	s.Tick(ev.Time)
	if !s.began {
		s.det.Observe(ev)
		return Result{}
	}
	if s.Done() {
		return Result{}
	}
	out := s.det.Feed(ev)
	switch out.Status {
	case Rejected:
		return Result{Rejected: out.Reason}
	case Done:
		return s.bind(out.Input)
	}
	return Result{}
}

// bind answers the current step with in, clears the earlier steps that
// read the same element, and moves on.
func (s *Session) bind(in Input) Result {
	r := Result{Bound: s.steps[s.cur].ID, Input: in}
	for i, b := range s.bound {
		if b != nil && i != s.cur && b.Key() == in.Key() {
			s.bound[i] = nil
			r.Cleared = append(r.Cleared, s.steps[i].ID)
		}
	}
	s.bound[s.cur] = &in
	s.advance(s.cur + 1)
	return r
}

// advance moves to step i and begins its prompt.
func (s *Session) advance(i int) {
	s.cur = max(0, min(i, len(s.steps)))
	s.det.Begin()
}

// Skip leaves the current step without an element and moves on.
func (s *Session) Skip() {
	if s.Done() {
		return
	}
	s.bound[s.cur] = nil
	s.advance(s.cur + 1)
}

// Back goes to the previous step and throws its answer away, to ask again.
func (s *Session) Back() {
	if s.cur == 0 {
		return
	}
	s.advance(s.cur - 1)
	s.bound[s.cur] = nil
}

// Redo asks the current step again from the start of its prompt. After the
// last step it asks the last one again.
func (s *Session) Redo() {
	if s.Done() {
		s.cur = len(s.steps) - 1
	}
	s.bound[s.cur] = nil
	s.advance(s.cur)
}

// Binding returns the element bound to the step with id, if any.
func (s *Session) Binding(id string) (Input, bool) {
	for i, st := range s.steps {
		if st.ID == id && s.bound[i] != nil {
			return *s.bound[i], true
		}
	}
	return Input{}, false
}

// ErrNothingBound is returned by Mapping when no step has an element.
var ErrNothingBound = errors.New("no control was mapped")

// Mapping builds the SDL mapping of the answers: the GUID of the session,
// the name "*" (the device keeps its own name), the bindings in the order of
// the steps, and platform:Windows. A stick direction pair that read the
// two ends of one axis becomes the whole axis (leftx:aN, with "~" when the
// first direction went up); otherwise each direction binds its half. The
// warnings list what the emergency combo needs and is missing.
func (s *Session) Mapping() (sdlmapping.Mapping, []Warning, error) {
	m := sdlmapping.Mapping{GUID: s.guid, Name: "*"}
	byOutput := map[string]*Input{}
	for i, st := range s.steps {
		byOutput[st.Output] = s.bound[i]
	}
	done := map[string]bool{}
	for _, st := range s.steps {
		in := byOutput[st.Output]
		if axis := stickAxis(st.Output); axis != "" {
			if done[axis] {
				continue
			}
			done[axis] = true
			neg, pos := byOutput["-"+axis], byOutput["+"+axis]
			if neg != nil && pos != nil && neg.Kind == Axis && pos.Kind == Axis && neg.Index == pos.Index &&
				neg.Direction() != 0 && neg.Direction() == -pos.Direction() {
				v := "a" + itoa(neg.Index)
				if neg.Direction() > 0 {
					v += "~"
				}
				m.Fields = append(m.Fields, sdlmapping.Field{Key: axis, Value: v})
				continue
			}
			for _, h := range []struct {
				key string
				in  *Input
			}{{"-" + axis, neg}, {"+" + axis, pos}} {
				if h.in != nil {
					m.Fields = append(m.Fields, sdlmapping.Field{Key: h.key, Value: h.in.Value()})
				}
			}
			continue
		}
		if in != nil {
			m.Fields = append(m.Fields, sdlmapping.Field{Key: st.Output, Value: in.Value()})
		}
	}
	if len(m.Fields) == 0 {
		return sdlmapping.Mapping{}, nil, ErrNothingBound
	}
	m.Fields = append(m.Fields, sdlmapping.Field{Key: "platform", Value: "Windows"})
	if err := m.Validate(); err != nil {
		return sdlmapping.Mapping{}, nil, err
	}
	var warns []Warning
	if byOutput["back"] == nil {
		warns = append(warns, Warning{Code: WarnNoBack, Step: "view"})
	}
	if byOutput["start"] == nil {
		warns = append(warns, Warning{Code: WarnNoStart, Step: "menu"})
	}
	return m, warns, nil
}

// stickAxis returns the axis key of a stick direction output ("-leftx" is
// "leftx"), or "" for any other output.
func stickAxis(output string) string {
	if len(output) < 2 || (output[0] != '-' && output[0] != '+') {
		return ""
	}
	base := output[1:]
	if !slices.Contains([]string{"leftx", "lefty", "rightx", "righty"}, base) {
		return ""
	}
	return base
}
