package script

import (
	"slices"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
)

type layerListener struct {
	fn       goja.Callable
	disposed bool
}

type pendingLayerHold struct {
	disposed bool
	release  func()
}
type pendingLayerOp struct {
	op, name string
	hold     *pendingLayerHold
}

// LayerNames returns the immutable profile layer names available to setup
// and parameter validation without reading an engine from a worker.
func (h *Host) LayerNames() []string { return slices.Clone(h.layerNames) }

func (h *Host) installGestureAPI(host *goja.Object) {
	setFunction(h.VM, host, "claim", 1, func(call goja.FunctionCall) goja.Value { return h.claimGesture(ScopeBase, "", call) })
	setFunction(h.VM, host, "layer", 1, func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		if name == "" {
			panic(h.VM.NewTypeError("layer: name must not be empty"))
		}
		if h.layerNames != nil && !slices.Contains(h.layerNames, name) {
			panic(h.VM.NewTypeError("layer: unknown layer " + name))
		}
		o := h.VM.NewObject()
		setFunction(h.VM, o, "on", 3, func(call goja.FunctionCall) goja.Value { return h.onGesture(ScopeLayer, name, call) })
		setFunction(h.VM, o, "claim", 1, func(call goja.FunctionCall) goja.Value { return h.claimGesture(ScopeLayer, name, call) })
		return o
	})
	o := h.VM.NewObject()
	_ = o.DefineAccessorProperty("base", h.VM.ToValue(func(goja.FunctionCall) goja.Value { return h.VM.ToValue(h.currentLayers().Base) }), nil, goja.FLAG_FALSE, goja.FLAG_TRUE)
	_ = o.DefineAccessorProperty("active", h.VM.ToValue(func(goja.FunctionCall) goja.Value { return h.VM.ToValue(h.activeLayers()) }), nil, goja.FLAG_FALSE, goja.FLAG_TRUE)
	for _, op := range []string{"switch", "enable", "disable", "toggle"} {
		setFunction(h.VM, o, op, 1, func(call goja.FunctionCall) goja.Value {
			name := call.Argument(0).String()
			if h.setup || h.candidate {
				h.checkLayerName(name)
				h.pendingLayerOps = append(h.pendingLayerOps, pendingLayerOp{op: op, name: name})
				return goja.Undefined()
			}
			if !h.active {
				return goja.Undefined()
			}
			if h.layerAccess == nil {
				panic(h.VM.NewTypeError("layers: no layer controller is connected"))
			}
			if err := h.layerAccess.ChangeLayer(op, name); err != nil {
				panic(h.VM.NewTypeError(err.Error()))
			}
			h.UpdateLayers(h.layerAccess.LayerState())
			return goja.Undefined()
		})
	}
	setFunction(h.VM, o, "hold", 1, func(call goja.FunctionCall) goja.Value {
		dispose := h.holdLayer(call.Argument(0).String())
		h.layerHolds = append(h.layerHolds, dispose)
		return h.VM.ToValue(h.disposable(dispose))
	})
	setFunction(h.VM, o, "isActive", 1, func(call goja.FunctionCall) goja.Value {
		return h.VM.ToValue(slices.Contains(h.activeLayers(), call.Argument(0).String()))
	})
	setFunction(h.VM, o, "onChange", 1, func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			panic(h.VM.NewTypeError("layers.onChange: callback must be a function"))
		}
		listener := &layerListener{fn: fn}
		h.layerListeners = append(h.layerListeners, listener)
		return h.VM.ToValue(h.disposable(func() { listener.disposed = true }))
	})
	set(host, "layers", o)
}

func (h *Host) holdLayer(name string) func() {
	if h.setup || h.candidate {
		h.checkLayerName(name)
		hold := new(pendingLayerHold)
		h.pendingLayerOps = append(h.pendingLayerOps, pendingLayerOp{op: "hold", name: name, hold: hold})
		return func() {
			hold.disposed = true
			if hold.release != nil {
				hold.release()
			}
		}
	}
	if !h.active {
		return func() {}
	}
	if h.layerAccess == nil {
		panic(h.VM.NewTypeError("layers: no layer controller is connected"))
	}
	dispose, err := h.layerAccess.HoldLayer(name)
	if err != nil {
		panic(h.VM.NewTypeError(err.Error()))
	}
	h.UpdateLayers(h.layerAccess.LayerState())
	return func() { dispose(); h.UpdateLayers(h.layerAccess.LayerState()) }
}

func (h *Host) checkLayerName(name string) {
	if name == "" || h.layerNames != nil && !slices.Contains(h.layerNames, name) {
		panic(h.VM.NewTypeError("layers: unknown layer " + name))
	}
}

// applyPendingLayers performs only Go state changes. BindLayers can call it
// after a synchronous standalone load has become attached to a slot.
func (h *Host) applyPendingLayers() {
	if h.layerAccess == nil || !h.active || h.setup || h.candidate || h.disposed {
		return
	}
	ops := h.pendingLayerOps
	h.pendingLayerOps = nil
	for _, op := range ops {
		if op.hold != nil {
			if op.hold.disposed {
				continue
			}
			dispose, err := h.layerAccess.HoldLayer(op.name)
			h.base.addError(err)
			op.hold.release = dispose
		} else {
			h.base.addError(h.layerAccess.ChangeLayer(op.op, op.name))
		}
	}
	h.UpdateLayers(h.layerAccess.LayerState())
}

func (h *Host) currentLayers() engine.LayerState {
	if h.active && !h.setup && !h.candidate && h.layerAccess != nil {
		return h.layerAccess.LayerState()
	}
	return h.layerState
}

func (h *Host) activeLayers() []string {
	s := h.currentLayers()
	return append([]string{s.Base}, s.Overlays...)
}

// BindLayers changes only Go state and is safe outside a script watchdog.
func (h *Host) BindLayers(access engine.LayerAccess) {
	h.layerAccess = access
	if access != nil {
		h.layerNames = access.LayerNames()
		h.UpdateLayers(access.LayerState())
		h.applyPendingLayers()
	}
}

// UpdateLayers stores an independent snapshot and queues notifications. It
// never executes JS while an engine transition or slot safety path is running.
func (h *Host) UpdateLayers(s engine.LayerState) {
	changed := s.Base != h.layerState.Base || !slices.Equal(s.Overlays, h.layerState.Overlays)
	s.Overlays = slices.Clone(s.Overlays)
	h.layerState = s
	if !changed || !h.active || h.disposed {
		return
	}
	active := append([]string{s.Base}, s.Overlays...)
	for _, listener := range h.layerListeners {
		h.Loop.QueueMicrotask(func() {
			if listener.disposed || !h.active || h.disposed {
				return
			}
			_, err := listener.fn(goja.Undefined(), h.VM.ToValue(slices.Clone(active)))
			h.base.addError(Diagnose(PhaseHandler, err))
		})
	}
}

func (h *Host) releaseLayerHolds() {
	for _, dispose := range h.layerHolds {
		dispose()
	}
	h.layerHolds = nil
	h.pendingLayerOps = nil
}
