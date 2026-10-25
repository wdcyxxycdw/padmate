package supervisor

import (
	"maps"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// PowerUpdate is what Config.OnPower gets: the power a gamepad reported (IN-12),
// with what padmated needs to decide whether to announce it.
type PowerUpdate struct {
	// Device is the connection ID of the gamepad, Key its stable identity
	// (the one a receiver's revival, which gives a new ID, keeps) and Name its
	// name.
	Device input.DeviceID
	Key    input.DeviceKey
	Name   string
	Power  input.Power
	// Mapped says that a slot holds the gamepad; a gamepad that waits for one
	// is not mapped.
	Mapped bool
	// Feedback is the feedback of the profile the gamepad is mapped with, in
	// the router's program of this moment (a reload is ordered with the
	// events): the strength and the pulse times of the alert's buzz. A gamepad
	// that holds no slot has no profile, and gets the defaults. The switches
	// (UI-27) are not in it: they are global (config.json feedback, ADR-0067).
	Feedback profile.Feedback
}

// devicePower keeps the latest power of a gamepad on its record (IN-12),
// for Devices, and passes it to Config.OnPower. It is no control: no slot
// gets it, and it neither takes a slot over nor confirms one. The value is
// copied, since the event is shared with the other consumers of the stream.
//
// A gamepad that the device filter or the ignore setting leaves out is not
// reported to OnPower: it is not one padmate maps, and the user has told
// padmate to leave it alone.
func (r *router) devicePower(e input.Event) {
	d := r.devices[e.Device]
	if d == nil || e.Power == nil {
		return
	}
	p := *e.Power
	d.power = &p
	r.s.setPower(d.id, &p)
	f := r.s.cfg.OnPower
	if f == nil || !r.matches(d) {
		return
	}
	u := PowerUpdate{Device: d.id, Key: d.key, Name: d.info.Name, Power: p, Feedback: profile.DefaultFeedback()}
	if r.routeOf(d) != nil {
		u.Mapped = true
		if r.program != nil {
			if pf := r.program.Profile(); pf != nil && pf.Feedback != nil {
				u.Feedback = *pf.Feedback
			}
		}
	}
	f(u)
}

// powerOf is the power the gamepad with connection id last reported, false
// until it has reported one and after it was removed. Slots read it for a
// script's pad.info.battery; the table it reads is never changed, so it takes
// no lock.
func (s *Supervisor) powerOf(id input.DeviceID) (input.Power, bool) {
	if t := s.powers.Load(); t != nil {
		p, ok := (*t)[id]
		return p, ok
	}
	return input.Power{}, false
}

// setPower replaces the table with one that has p for id, or, when p is nil,
// without id. Only the router calls it, so the copy needs no loop.
func (s *Supervisor) setPower(id input.DeviceID, p *input.Power) {
	old := s.powers.Load()
	next := make(map[input.DeviceID]input.Power)
	if old != nil {
		maps.Copy(next, *old)
	}
	if p != nil {
		next[id] = *p
	} else {
		delete(next, id)
	}
	s.powers.Store(&next)
}
