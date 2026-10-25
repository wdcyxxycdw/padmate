package main

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
)

// InvokeAction is called only by the owning slot after the common gesture
// recognizer resolves a declarative script action.
func (r *scriptRuntime) InvokeAction(name string, args map[string]any, e slot.ScriptEvent) error {
	defer r.flushLogs()
	if r.paused || r.wallStopped || r.lifecycle == nil {
		return nil
	}
	err := r.lifecycle.InvokeAction(name, args, script.Event{
		Control: e.Control, Gesture: e.Gesture, Time: e.Time, Duration: e.Duration,
		Count: e.Count, Scope: e.Scope, Cycle: e.Cycle, Held: e.Held,
	})
	r.rememberError(err)
	if script.IsWallBudget(err) {
		r.stopForWall()
	}
	return err
}
