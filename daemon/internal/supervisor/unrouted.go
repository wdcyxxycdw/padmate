package supervisor

import "github.com/wdcyxxycdw/padmate/daemon/internal/input"

// The reasons Config.OnUnrouted gives for a button edge of a gamepad that no
// slot maps (ADR-0071). They are the words of padmated's unrouted event.
const (
	// UnroutedQueued is a gamepad that waits for a slot: every slot is taken.
	UnroutedQueued = "queued"
	// UnroutedIgnored is a gamepad that the device filter or the ignore
	// setting leaves out.
	UnroutedIgnored = "ignored"
	// UnroutedNotGamepad is a joystick without a gamepad mapping.
	UnroutedNotGamepad = "notGamepad"
	// UnroutedDisabled is a gamepad whose slot was disabled after repeated
	// errors.
	UnroutedDisabled = "disabled"
)

// wantsUnrouted reports whether e is an event that Config.OnUnrouted is to
// hear of: a button edge, when it is set. Axes and every other event are no
// one's concern: a stick at rest jitters all the time.
func (r *router) wantsUnrouted(e input.Event) bool {
	return r.s.cfg.OnUnrouted != nil && (e.Kind == input.ButtonDown || e.Kind == input.ButtonUp)
}

// unrouted reports the button edge e of a gamepad that maps to nothing, with
// why.
func (r *router) unrouted(e input.Event, why string) {
	if r.wantsUnrouted(e) {
		r.s.cfg.OnUnrouted(e, why)
	}
}

// unroutedDevice reports e of gamepad d, which has no route. Its reason costs
// a device filter match and a lookup of the ignore setting, so it is worked
// out for the edges that are reported only, not for every event of the
// jittering sticks of a gamepad that waits.
func (r *router) unroutedDevice(e input.Event, d *device) {
	if r.wantsUnrouted(e) {
		r.unrouted(e, r.unroutedWhy(d))
	}
}

// unroutedWhy is the reason that gamepad d, which has no route, maps to nothing.
func (r *router) unroutedWhy(d *device) string {
	if !r.matches(d) {
		return UnroutedIgnored
	}
	return UnroutedQueued
}
