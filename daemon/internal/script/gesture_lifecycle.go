package script

import "github.com/wdcyxxycdw/padmate/daemon/internal/engine"

func (l *Lifecycle) GestureEntries() []engine.GestureEntry {
	if l.current == nil {
		return nil
	}
	return l.current.GestureEntries()
}

func (l *Lifecycle) ObserveGesture(e engine.GestureEvent) error {
	if l.current == nil {
		return nil
	}
	err := l.current.ObserveGesture(e)
	l.handleRuntimeFailure(err)
	return err
}

func (l *Lifecycle) DispatchGesture(id uint64, e engine.GestureEvent) (bool, error) {
	if l.current == nil {
		return false, nil
	}
	consumed, err := l.current.DispatchGesture(id, e)
	l.handleRuntimeFailure(err)
	return consumed, err
}

func (l *Lifecycle) EndGesture(e engine.GestureEvent) error {
	if l.current == nil {
		return nil
	}
	err := l.current.EndGesture(e)
	l.handleRuntimeFailure(err)
	return err
}

func (l *Lifecycle) BindLayers(access engine.LayerAccess) {
	l.hostOptions.Layers = access
	if access != nil {
		l.hostOptions.LayerState = access.LayerState()
		l.hostOptions.LayerNames = access.LayerNames()
	}
	if l.current != nil {
		l.current.BindLayers(access)
	}
}
