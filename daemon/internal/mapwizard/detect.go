// Package mapwizard is the logic of the controller mapping wizard (IN-19,
// ADR-0072): from the raw events of one joystick it finds the element the
// user pressed or pushed for each control that the wizard asks for, and
// builds the SDL mapping string from the answers.
//
// It is pure: no SDL, no Windows, no clock (event times come with the
// events). The thresholds follow SDL 3.4.16's test/testcontroller.c, which
// padmate reads and writes its own way. The UI that shows the dialog uses
// the same rules; testdata/vectors.json holds the cases both must agree on.
package mapwizard

import (
	"strconv"

	"github.com/wdcyxxycdw/padmate/daemon/internal/sdlmapping"
)

// The thresholds of the wizard, in SDL joystick axis units (-32768..32767)
// and nanoseconds.
const (
	// Excursion is how far an axis must move from its rest value before it
	// counts.
	Excursion = 16000
	// Return is how close to the rest value it must come back.
	Return = 10000
	// Jitter is the movement SDL itself ignores (32767/80): smaller moves
	// are not events of a real device and are not noise either.
	Jitter = 409
	// Quiet is how long the wizard watches before the first prompt, for
	// axes and buttons that move on their own.
	Quiet int64 = 1_000_000_000
	// NoiseEvents is how many events an element sends in the quiet period
	// to be called noisy.
	NoiseEvents = 4
)

// Kind is the kind of a raw event.
type Kind uint8

// The kinds of raw events.
const (
	Button Kind = iota + 1
	Axis
	Hat
)

// Event is one raw event of the joystick being mapped.
type Event struct {
	Kind  Kind
	Index int
	// Value is 1 (down) or 0 (up) for a button, the position for an axis
	// and the direction mask for a hat: up 1, right 2, down 4, left 8,
	// 0 for centered.
	Value int
	// Time is a monotonic time in nanoseconds; only differences matter.
	Time int64
}

// Info is what the wizard needs to know about the joystick.
type Info struct {
	Axes, Buttons, Hats int
	// AxisRest is the rest value of each axis (SDL_GetJoystickAxisInitialState);
	// an axis beyond its length rests at 0.
	AxisRest []int16
}

func (i Info) rest(axis int) int {
	if axis >= 0 && axis < len(i.AxisRest) {
		return int(i.AxisRest[axis])
	}
	return 0
}

// Status is what Detector.Feed concludes from an event.
type Status uint8

// The statuses of a Feed.
const (
	// Waiting means nothing is decided.
	Waiting Status = iota
	// Done means the Outcome's Input is the element the user used.
	Done
	// Rejected means the event cannot be used and the user is to try again;
	// the Outcome's Reason says why.
	Rejected
)

// The Reasons of a Rejected outcome.
const (
	// ReasonDiagonal is a hat pushed to a diagonal: only one direction counts.
	ReasonDiagonal = "diagonal"
)

// Outcome is the result of Feed.
type Outcome struct {
	Status Status
	// Input is the element, for Done.
	Input Input
	// Reason is why an event is Rejected.
	Reason string
}

// Input is an element the wizard found: a button, a hat direction or an
// axis with its rest and farthest values.
type Input struct {
	Kind  Kind
	Index int
	// Mask is the hat direction, for a hat.
	Mask int
	// Rest and Far are the rest value and the farthest value of an axis.
	Rest, Far int
}

// Direction is the sign of the axis excursion: 1 when the axis went up from
// rest, -1 when it went down, 0 for no excursion or another kind.
func (in Input) Direction() int {
	if in.Kind != Axis {
		return 0
	}
	switch {
	case in.Far > in.Rest:
		return 1
	case in.Far < in.Rest:
		return -1
	}
	return 0
}

// Key identifies the element and, for an axis, the direction it was pushed
// in: two Inputs with the same Key read the same thing.
func (in Input) Key() string {
	switch in.Kind {
	case Button:
		return "b" + itoa(in.Index)
	case Hat:
		return "h" + itoa(in.Index) + "." + itoa(in.Mask)
	case Axis:
		if in.Direction() < 0 {
			return "a" + itoa(in.Index) + "-"
		}
		return "a" + itoa(in.Index) + "+"
	}
	return ""
}

// Value returns the binding value that reads the element as a button, a
// trigger or a half axis: bN, hN.M, or for an axis AxisSpec.
func (in Input) Value() string {
	switch in.Kind {
	case Button:
		return "b" + itoa(in.Index)
	case Hat:
		return "h" + itoa(in.Index) + "." + itoa(in.Mask)
	case Axis:
		return AxisSpec(in.Index, in.Rest, in.Far)
	}
	return ""
}

func itoa(n int) string { return strconv.Itoa(n) }

// AxisSpec returns the binding value for axis index that rests at rest and
// was pushed to far, in SDL's terms: the half of the range that the axis
// covers, and "~" when it runs backwards. SDL maps the range from the
// rest value to the far value onto the whole travel of the element it
// drives, and reads 0 for a value outside of it, so the range must not be
// narrower than what the axis can do:
//
//	rest 0,      far 32767: +aN   (a trigger that rests at 0)
//	rest 0,      far -32768: -aN
//	rest -32768, far anywhere above: aN  (a trigger on the full range)
//	rest 32767,  far anywhere below: aN~ (reversed full range)
//
// An axis that rests at one end of its range and was moved towards the other
// gets the whole range, however far it was pushed: the user may not have
// pulled a trigger all the way, and a half range would read 0 for the rest of
// the pull (SDL's own test program does the same). That a trigger really only
// travels half of the range is the rarer case, and costs less: it then reads
// half of its output. An axis that rests in the middle gets the half it was
// pushed into.
func AxisSpec(index, rest, far int) string {
	in := sdlmapping.Input{Kind: sdlmapping.InputAxis, Index: index}
	r, f := quantize(rest), quantize(far)
	switch {
	case r == 0 && f > 0:
		in.Half = sdlmapping.Positive
	case r == 0 && f < 0:
		in.Half = sdlmapping.Negative
	case r < 0 && far > rest:
		// the whole range, upwards
	case r > 0 && far < rest:
		in.Inverted = true
	case far > rest: // a small range inside the middle, or past an end
		in.Half = sdlmapping.Positive
	default:
		in.Half = sdlmapping.Negative
	}
	return in.String()
}

// quantize puts v in one of the three zones of SDL's test program: above
// 16383 is 1, below -16384 is -1, the rest 0.
func quantize(v int) int {
	switch {
	case v > 16383:
		return 1
	case v < -16384:
		return -1
	}
	return 0
}

// elem names one joystick element for the noise count.
type elem struct {
	kind  Kind
	index int
}

// Detector finds the element a user presses or pushes for one prompt at a
// time. Use Observe during the quiet period, Begin to start a prompt and
// Feed for the events of the prompt. It is not safe for concurrent use.
type Detector struct {
	info  Info
	seen  map[elem]int   // events per element in the quiet period
	noisy map[elem]bool  // elements that move on their own
	last  map[elem]int64 // time of the last event of an element that is or was counted
	cur   []int          // last seen value of each axis

	// State of the prompt.
	pressed map[int]bool // buttons that went down during the prompt
	hatCur  map[int]int  // last seen direction mask of each hat
	hatAway map[int]bool // hats that must come back to the center first
	armed   []bool       // axes that rested within Return of rest when the prompt began
	far     []int        // farthest value of each armed axis
}

// NewDetector returns a Detector for a joystick described by info.
func NewDetector(info Info) *Detector {
	d := &Detector{
		info:   info,
		seen:   map[elem]int{},
		noisy:  map[elem]bool{},
		last:   map[elem]int64{},
		cur:    make([]int, info.Axes),
		hatCur: map[int]int{},
	}
	for a := range d.cur {
		d.cur[a] = info.rest(a)
	}
	return d
}

// Noisy reports whether the element moved on its own in the quiet period
// and is left out. Only elements Observe saw are noisy.
func (d *Detector) Noisy(kind Kind, index int) bool { return d.noisy[elem{kind, index}] }

// NoisyAxes returns the indices of the axes left out as noisy, in order.
func (d *Detector) NoisyAxes() []int {
	var out []int
	for a := range d.info.Axes {
		if d.noisy[elem{Axis, a}] {
			out = append(out, a)
		}
	}
	return out
}

// Observe takes an event of the quiet period, which comes before the first
// prompt: an axis that sends NoiseEvents events away from its rest value,
// or a button or hat that sends as many, moves on its own and is not used
// -- until it has sent nothing for Quiet (see Feed). It also keeps the
// position of each axis for Begin.
func (d *Detector) Observe(ev Event) {
	e := elem{ev.Kind, ev.Index}
	switch ev.Kind {
	case Axis:
		if ev.Index < 0 || ev.Index >= len(d.cur) {
			return
		}
		d.cur[ev.Index] = ev.Value
		if abs(ev.Value-d.info.rest(ev.Index)) <= Jitter {
			return
		}
	case Hat:
		d.hatCur[ev.Index] = ev.Value
	case Button:
	default:
		return
	}
	d.seen[e]++
	d.last[e] = ev.Time
	if d.seen[e] >= NoiseEvents {
		d.noisy[e] = true
	}
}

// stillNoisy reports whether the element e, if it is marked noisy, is still
// moving on its own at time t, and takes the event as one more sign of it. An
// element that has sent nothing for Quiet is not: a thumb that rested on a
// stick while the dialog opened moved it for a while, then let go, and the
// user is to map it. The mark is lifted, and the event counts. Chatter does
// not stop for a second.
func (d *Detector) stillNoisy(e elem, t int64) bool {
	if !d.noisy[e] {
		return false
	}
	if t-d.last[e] >= Quiet {
		delete(d.noisy, e)
		delete(d.seen, e)
		return false
	}
	d.last[e] = t
	return true
}

// Begin starts a prompt: it forgets what the last one saw. An axis that is
// away from its rest value now does not count until it has come back.
func (d *Detector) Begin() {
	d.pressed = map[int]bool{}
	d.hatAway = map[int]bool{}
	for h, v := range d.hatCur {
		d.hatAway[h] = v != 0
	}
	d.armed = make([]bool, d.info.Axes)
	d.far = make([]int, d.info.Axes)
	for a := range d.far {
		rest := d.info.rest(a)
		d.far[a] = rest
		d.armed[a] = abs(d.cur[a]-rest) <= Return
	}
}

// Feed takes an event of the prompt and says what it makes of it. Buttons
// count when released, after they went down in this prompt. A hat counts
// when pushed from the center to one direction; a diagonal is Rejected, and
// the hat must come back to the center before the next direction counts. An
// axis counts when it has moved Excursion from its rest value and come back
// within Return of it, once no other axis is still out. Of axes that all
// came back, the one that moved farthest wins. An element that Observe marked
// noisy is left out, until it has sent nothing for Quiet.
func (d *Detector) Feed(ev Event) Outcome {
	if d.pressed == nil {
		d.Begin()
	}
	switch ev.Kind {
	case Button:
		if d.stillNoisy(elem{Button, ev.Index}, ev.Time) {
			return Outcome{}
		}
		if ev.Value != 0 {
			d.pressed[ev.Index] = true
			return Outcome{}
		}
		if d.pressed[ev.Index] {
			delete(d.pressed, ev.Index)
			return Outcome{Status: Done, Input: Input{Kind: Button, Index: ev.Index}}
		}
	case Hat:
		return d.feedHat(ev)
	case Axis:
		return d.feedAxis(ev)
	}
	return Outcome{}
}

// feedHat takes a hat event. A hat counts when it leaves the center in one
// of the four directions; one that is away from the center (it was when
// the prompt began, or it has counted or been rejected) must come back to
// the center first.
func (d *Detector) feedHat(ev Event) Outcome {
	if d.stillNoisy(elem{Hat, ev.Index}, ev.Time) {
		return Outcome{}
	}
	away := d.hatAway[ev.Index]
	d.hatCur[ev.Index] = ev.Value
	d.hatAway[ev.Index] = ev.Value != 0
	if ev.Value == 0 || away {
		return Outcome{}
	}
	switch ev.Value {
	case 1, 2, 4, 8:
		return Outcome{Status: Done, Input: Input{Kind: Hat, Index: ev.Index, Mask: ev.Value}}
	}
	return Outcome{Status: Rejected, Reason: ReasonDiagonal}
}

func (d *Detector) feedAxis(ev Event) Outcome {
	a := ev.Index
	if a < 0 || a >= len(d.cur) {
		return Outcome{}
	}
	d.cur[a] = ev.Value
	if d.stillNoisy(elem{Axis, a}, ev.Time) {
		return Outcome{}
	}
	rest := d.info.rest(a)
	dist := abs(ev.Value - rest)
	if !d.armed[a] {
		if dist <= Return {
			d.armed[a], d.far[a] = true, rest
		}
		return Outcome{}
	}
	if dist > abs(d.far[a]-rest) {
		d.far[a] = ev.Value
	}
	best, bestDist := -1, 0
	for i := range d.cur {
		if !d.armed[i] || d.noisy[elem{Axis, i}] {
			continue
		}
		r := d.info.rest(i)
		out := abs(d.far[i] - r)
		if out < Excursion {
			continue
		}
		if abs(d.cur[i]-r) > Return {
			return Outcome{} // another axis is still out: wait for it
		}
		if out > bestDist {
			best, bestDist = i, out
		}
	}
	if best < 0 {
		return Outcome{}
	}
	r := d.info.rest(best)
	return Outcome{Status: Done, Input: Input{Kind: Axis, Index: best, Rest: r, Far: d.far[best]}}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
