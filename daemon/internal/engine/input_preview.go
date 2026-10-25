package engine

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
)

// InputPreview recognizes input without owning bindings, output, layers or
// script callbacks. It is used only while a cold script is building. The
// immutable program is shared; all mutable classifier state is copied.
type InputPreview struct{ engine Engine }

func (e *Engine) PreviewInput(changed func(ButtonEvent)) *InputPreview {
	return &InputPreview{engine: Engine{
		prog: e.prog, inputOnly: true, baseLayer: e.baseLayer, fnActive: e.fnActive,
		physical: e.physical, physicalReleased: e.physicalReleased,
		axes: e.axes, sticks: e.sticks, dpadPhysical: e.dpadPhysical,
		dpadOrder: e.dpadOrder, dpadOrderN: e.dpadOrderN,
		dpadVirtual: e.dpadVirtual, halfAxis: e.halfAxis, triggerFull: e.triggerFull,
		calSrc: e.calSrc, cal: e.cal, calIgnored: e.calIgnored,
		button: func(ev ButtonEvent) bool {
			if changed != nil {
				changed(ev)
			}
			return true
		},
	}}
}

func (p *InputPreview) Step(in Input) {
	p.Apply(in.T)
	p.engine.Step(in)
}

func (p *InputPreview) Apply(now clock.Time) {
	for {
		d, ok := p.engine.InputDeadline()
		if !ok || d > now {
			return
		}
		p.engine.Apply(d)
	}
}

func (p *InputPreview) ControlDown(c control.Control) bool { return p.engine.ControlDown(c) }

// InputDeadline returns the next delayed physical direction transition. Cold
// input replay uses its original time even if a worker completed much later.
// Gesture and repeat deadlines remain governed by Apply's normal late policy.
func (e *Engine) InputDeadline() (clock.Time, bool) {
	var deadline clock.Time
	ok := false
	for _, s := range e.sticks {
		if s.pendingSet && (!ok || s.deadline < deadline) {
			deadline, ok = s.deadline, true
		}
	}
	return deadline, ok
}
