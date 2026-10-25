package supervisor

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// deviceState corrects the entire state without treating a sampled held
// button as a new press that confirms or takes over a provisional slot.
func (r *router) deviceState(e input.Event) {
	d := r.devices[e.Device]
	if d == nil || e.Snapshot == nil {
		return
	}
	// Losses after this sample may already have reset the slots while the
	// snapshot waited in Source's critical backlog. Never revive that state.
	if e.Snapshot.Dropped < r.dropped {
		return
	}
	d.state = slot.State{}
	d.snapshotDrops, d.hasSnapshot = e.Snapshot.Dropped, true
	for _, c := range e.Snapshot.Buttons {
		d.state.Apply(input.Event{Kind: input.ButtonDown, Control: c})
	}
	for a, v := range e.Snapshot.Axes {
		d.state.Apply(input.Event{Kind: input.AxisMotion, Control: a, Value: v})
	}
	if r.recoverySuspended() {
		d.recovery.capture(d.state, r.program)
	}
	d.recovery.mask(&d.state)
	if rt := r.routeOf(d); rt != nil {
		r.deliver(rt, r.resync(d.state))
	}
}
