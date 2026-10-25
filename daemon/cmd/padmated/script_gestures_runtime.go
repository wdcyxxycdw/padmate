package main

import (
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
)

func (r *scriptRuntime) ObserveGesture(e engine.GestureEvent) error {
	defer r.flushLogs()
	if r.paused || r.wallStopped || r.lifecycle == nil {
		return nil
	}
	err := r.lifecycle.ObserveGesture(e)
	r.rememberError(err)
	if script.IsWallBudget(err) {
		r.stopForWall()
	}
	return err
}

func (r *scriptRuntime) GestureEntries() []engine.GestureEntry {
	if r.paused || r.wallStopped || r.lifecycle == nil {
		return nil
	}
	return r.lifecycle.GestureEntries()
}

func (r *scriptRuntime) DispatchGesture(id uint64, e engine.GestureEvent) (bool, error) {
	defer r.flushLogs()
	if r.paused || r.wallStopped || r.lifecycle == nil {
		return false, nil
	}
	consumed, err := r.lifecycle.DispatchGesture(id, e)
	r.rememberError(err)
	if script.IsWallBudget(err) {
		r.stopForWall()
	}
	return consumed, err
}

func (r *scriptRuntime) EndGesture(e engine.GestureEvent) error {
	if r.lifecycle == nil {
		return nil
	}
	err := r.lifecycle.EndGesture(e)
	r.rememberError(err)
	return err
}

func (r *scriptRuntime) BindLayers(access engine.LayerAccess) {
	r.layers = access
	if r.lifecycle != nil {
		r.lifecycle.BindLayers(access)
	}
	if r.pending != nil {
		r.pending.life.BindLayers(access)
	}
}

func (r *scriptRuntime) layerNames() []string {
	if r.layers == nil {
		return nil
	}
	return r.layers.LayerNames()
}

func (r *scriptRuntime) layerSnapshot() engine.LayerState {
	if r.layers == nil {
		return engine.LayerState{}
	}
	return r.layers.LayerState()
}
