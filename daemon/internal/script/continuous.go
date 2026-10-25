package script

import (
	"math"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

const defaultTickHz = 125

type continuousRegistration struct {
	fn       goja.Callable
	axis     control.Axis
	stick    string
	timer    Handle
	last     clock.Time
	disposed bool
}

func (h *Host) installContinuousAPI(o *goja.Object) {
	setFunction(h.VM, o, "onTick", 1, func(call goja.FunctionCall) goja.Value { return h.registerContinuous(call.Argument(0), 0, "") })
	setFunction(h.VM, o, "onAxis", 2, func(call goja.FunctionCall) goja.Value {
		a, ok := parseScriptAxis(call.Argument(0).String())
		if !ok {
			panic(h.VM.NewTypeError("onAxis: unknown axis"))
		}
		return h.registerContinuous(call.Argument(1), a, "")
	})
	setFunction(h.VM, o, "onStick", 2, func(call goja.FunctionCall) goja.Value {
		s := call.Argument(0).String()
		h.checkStick(s)
		return h.registerContinuous(call.Argument(1), 0, s)
	})
}

func (h *Host) registerContinuous(value goja.Value, a control.Axis, s string) goja.Value {
	fn, ok := goja.AssertFunction(value)
	if !ok {
		panic(h.VM.NewTypeError("continuous input: callback is not a function"))
	}
	r := &continuousRegistration{fn: fn, axis: a, stick: s}
	h.continuous = append(h.continuous, r)
	h.armContinuous(r)
	return h.VM.ToValue(h.disposable(func() { r.disposed = true; r.timer.Cancel() }))
}

func (h *Host) armContinuous(r *continuousRegistration) {
	r.timer.Cancel()
	r.last = h.clock.Now()
	r.timer = h.Loop.Every(h.tickPeriod, func() {
		if !h.active || h.disposed || r.disposed {
			return
		}
		now := h.clock.Now()
		dt := float64(now-r.last) / float64(time.Millisecond)
		r.last = now
		args := []goja.Value{h.VM.ToValue(dt)}
		if r.stick != "" {
			args = append([]goja.Value{h.stickState(r.stick, nil)}, args...)
		} else if r.axis != 0 {
			args = append([]goja.Value{h.VM.ToValue(h.axisValue(r.axis))}, args...)
		}
		if _, err := r.fn(goja.Undefined(), args...); err != nil {
			h.base.addError(Diagnose(PhaseHandler, err))
		}
	})
}

func (h *Host) resetContinuous() {
	for _, r := range h.continuous {
		if !r.disposed {
			h.armContinuous(r)
		}
	}
}
func (h *Host) cancelContinuous() {
	for _, r := range h.continuous {
		r.timer.Cancel()
		r.disposed = true
	}
}

func (h *Host) axisValue(a control.Axis) float64 {
	if !h.padState.Connected {
		return 0
	}
	v := float64(h.padState.Axes[a])
	if a == control.LeftTrigger || a == control.RightTrigger {
		return math.Max(0, v/32767)
	}
	if v < 0 {
		v /= 32768
	} else {
		v /= 32767
	}
	if a == control.LeftY || a == control.RightY {
		v = -v
	}
	return v
}

func (h *Host) installAnalogAPI(o *goja.Object) {
	setFunction(h.VM, o, "stick", 1, func(call goja.FunctionCall) goja.Value {
		var opts *goja.Object
		if !goja.IsUndefined(call.Argument(1)) && !goja.IsNull(call.Argument(1)) {
			opts = call.Argument(1).ToObject(h.VM)
		}
		return h.stickState(call.Argument(0).String(), opts)
	})
	setFunction(h.VM, o, "trigger", 1, func(call goja.FunctionCall) goja.Value {
		s := call.Argument(0).String()
		if s != "lt" && s != "rt" {
			panic(h.VM.NewTypeError("pad.trigger: unknown trigger"))
		}
		a, _ := parseScriptAxis(s)
		return h.VM.ToValue(h.axisValue(a))
	})
}

func (h *Host) checkStick(s string) {
	if s != "ls" && s != "rs" {
		panic(h.VM.NewTypeError("pad.stick: unknown stick"))
	}
}

func parseScriptAxis(s string) (control.Axis, bool) {
	switch s {
	case "lt":
		return control.LeftTrigger, true
	case "rt":
		return control.RightTrigger, true
	default:
		return control.ParseAxis(s)
	}
}

func (h *Host) stickState(s string, opts *goja.Object) *goja.Object {
	h.checkStick(s)
	a := h.padState.Analog
	if !h.padState.AnalogResolved {
		a = profile.DefaultAnalog()
	}
	settings := a.LS
	xAxis, yAxis := control.LeftX, control.LeftY
	if s == "rs" {
		settings = a.RS
		xAxis, yAxis = control.RightX, control.RightY
	}
	inner, outer, kind := settings.Deadzone, settings.Saturation, "linear"
	if opts != nil {
		if v := opts.Get("deadzone"); v != nil && !goja.IsUndefined(v) {
			inner = v.ToFloat()
		}
		if v := opts.Get("outer"); v != nil && !goja.IsUndefined(v) {
			outer = v.ToFloat()
		}
		if v := opts.Get("curve"); v != nil && !goja.IsUndefined(v) {
			kind = v.String()
		}
	}
	if !finite(inner) || !finite(outer) || inner < 0 || outer > 1 || outer <= inner {
		panic(h.VM.NewTypeError("pad.stick: require 0 <= deadzone < outer <= 1"))
	}
	power := 1.0
	switch kind {
	case "linear":
	case "quadratic":
		power = 2
	case "cubic":
		power = 3
	default:
		panic(h.VM.NewTypeError("pad.stick: unknown curve"))
	}
	x, y := h.axisValue(xAxis), h.axisValue(yAxis)
	raw := math.Hypot(x, y)
	magnitude := math.Pow(clamp((raw-inner)/(outer-inner), 0, 1), power)
	if raw > 0 {
		x *= magnitude / raw
		y *= magnitude / raw
	}
	angle := angleDegrees(x, y)
	o := h.VM.NewObject()
	set(o, "x", x)
	set(o, "y", y)
	set(o, "magnitude", magnitude)
	set(o, "angle", angle)
	setFunction(h.VM, o, "direction", 1, func(call goja.FunctionCall) goja.Value {
		n := int(call.Argument(0).ToInteger())
		if n != 4 && n != 8 {
			panic(h.VM.NewTypeError("direction: sectors must be 4 or 8"))
		}
		if magnitude == 0 {
			return h.VM.ToValue(-1)
		}
		offset, hysteresis, previous := 0.0, 0.0, -1
		if v := call.Argument(1); !goja.IsUndefined(v) && !goja.IsNull(v) {
			options := v.ToObject(h.VM)
			offset = optionalNumber(options, "offsetDeg", 0)
			hysteresis = optionalNumber(options, "hysteresisDeg", 0)
			previous = int(optionalNumber(options, "previous", -1))
		}
		if !finite(offset) || !finite(hysteresis) || hysteresis < 0 || hysteresis > 180/float64(n) || previous < -1 || previous >= n {
			panic(h.VM.NewTypeError("direction: invalid offset, hysteresis or previous sector"))
		}
		return h.VM.ToValue(trackSector(angle, n, offset, hysteresis, previous))
	})
	return o
}

func optionalNumber(o *goja.Object, name string, fallback float64) float64 {
	v := o.Get(name)
	if v == nil || goja.IsUndefined(v) {
		return fallback
	}
	return v.ToFloat()
}
func finite(v float64) bool             { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func clamp(v, lo, hi float64) float64   { return math.Max(lo, math.Min(v, hi)) }
func angleDegrees(x, y float64) float64 { return wrapDegrees(math.Atan2(x, y) * 180 / math.Pi) }
func wrapDegrees(v float64) float64 {
	v = math.Mod(v, 360)
	if v < 0 {
		v += 360
	}
	return v
}
func trackSector(angle float64, n int, offset, hysteresis float64, previous int) int {
	width := 360 / float64(n)
	relative := wrapDegrees(angle - offset)
	if previous >= 0 && previous < n {
		distance := math.Abs(wrapDegrees(relative-float64(previous)*width+180) - 180)
		if distance <= width/2+clamp(hysteresis, 0, width/2) {
			return previous
		}
	}
	return int(math.Floor((relative+width/2)/width)) % n
}
