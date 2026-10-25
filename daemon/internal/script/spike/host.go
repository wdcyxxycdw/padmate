package spike

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

// FixedTime is what Date.now() and new Date() return in a Host runtime.
var FixedTime = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// Seeds of the fixed Math.random source of a Host runtime.
const (
	randSeed1 = 0x5350_4b30_33 // "SPK03"
	randSeed2 = 0x7061_646d    // "padm"
)

// MaxCallStackSize is the call depth limit of SCRIPTING §4.1.
const MaxCallStackSize = 2000

// Host is the SPK-03 stand-in for the M5 script host (SCR-01): the least
// that runs the benchmark script. It is not the M5 host and must not grow
// into it; see the package doc.
//
// It installs the global the M0d padmate stub reads (compile.HostGlobal)
// with on, key.tap and sleep. on records a Registration; key.tap appends
// its spec to Taps and returns a promise that is already resolved;
// sleep returns a promise that stays pending until ResumeSleeps. A Host
// and its runtime belong to one goroutine at a time.
type Host struct {
	VM *goja.Runtime

	// Registrations are the on() calls, in order.
	Registrations []Registration
	// Taps are the key.tap specs, in order.
	Taps []string
	// Holds are the specs of e.holdKeys calls.
	Holds []string
	// HeldLayers are the names of e.holdLayer calls.
	HeldLayers []string
	// Passes counts e.pass calls.
	Passes int

	sleeps, spare []func(any) error
	tapResult     goja.Value
	eventProto    *goja.Object
	names         map[string]goja.Value
}

// Registration is one on() call.
type Registration struct {
	Control, Gesture string
	// Label and Layer come from the options object; layer() sets Layer.
	Label, Layer string
	Fn           goja.Callable
}

// NewHost returns a Host whose runtime has the padmate global, a fixed
// time and random source and the SCRIPTING §4.1 call depth limit.
func NewHost() *Host {
	vm := goja.New()
	vm.SetTimeSource(func() time.Time { return FixedTime })
	vm.SetRandSource(rand.New(rand.NewPCG(randSeed1, randSeed2)).Float64)
	vm.SetMaxCallStackSize(MaxCallStackSize)
	h := &Host{VM: vm, names: make(map[string]goja.Value)}

	// One shared TapResult: the stub has no rate limit, so no tap is ever
	// dropped.
	res := vm.NewObject()
	mustSet(res, "dropped", false)
	h.tapResult = res

	h.eventProto = vm.NewObject()
	mustSet(h.eventProto, "pass", func(goja.FunctionCall) goja.Value {
		h.Passes++
		return goja.Undefined()
	})
	mustSet(h.eventProto, "holdKeys", func(call goja.FunctionCall) goja.Value {
		h.Holds = append(h.Holds, call.Argument(0).String())
		return goja.Undefined()
	})
	mustSet(h.eventProto, "holdLayer", func(call goja.FunctionCall) goja.Value {
		h.HeldLayers = append(h.HeldLayers, call.Argument(0).String())
		return goja.Undefined()
	})

	key := vm.NewObject()
	mustSet(key, "tap", h.tap)
	host := vm.NewObject()
	mustSet(host, "on", h.on)
	mustSet(host, "key", key)
	mustSet(host, "sleep", h.sleep)
	mustSet(vm.GlobalObject(), compile.HostGlobal, host)
	return h
}

// Activate is a script activation without the build: a new Host that has
// run p. The M0d stub has no setup(), so "setup" is the bundle's top-level
// code, which makes the registrations.
func Activate(p *goja.Program) (*Host, error) {
	h := NewHost()
	if _, err := h.VM.RunProgram(p); err != nil {
		return nil, err
	}
	return h, nil
}

// CompileBundle compiles an esbuild bundle for Activate. goja reads the
// inline source map here.
func CompileBundle(r compile.Result) (*goja.Program, error) {
	return goja.Compile("bench.js", string(r.JS), false)
}

func (h *Host) on(call goja.FunctionCall) goja.Value {
	fn, ok := goja.AssertFunction(call.Argument(2))
	if !ok {
		panic(h.VM.NewTypeError("on: handler is not a function"))
	}
	r := Registration{Control: call.Argument(0).String(), Gesture: call.Argument(1).String(), Fn: fn}
	if o, ok := call.Argument(3).(*goja.Object); ok {
		if v := o.Get("label"); v != nil && !goja.IsUndefined(v) {
			r.Label = v.String()
		}
		if v := o.Get("layer"); v != nil && !goja.IsUndefined(v) {
			r.Layer = v.String()
		}
	}
	h.Registrations = append(h.Registrations, r)
	return goja.Undefined()
}

func (h *Host) tap(call goja.FunctionCall) goja.Value {
	h.Taps = append(h.Taps, call.Argument(0).String())
	p, resolve, _ := h.VM.NewPromise()
	// Resolving with a plain object runs no JavaScript and cannot fail;
	// the reactions run when the outermost call returns to Go.
	_ = resolve(h.tapResult)
	return h.VM.ToValue(p)
}

func (h *Host) sleep(goja.FunctionCall) goja.Value {
	p, resolve, _ := h.VM.NewPromise()
	h.sleeps = append(h.sleeps, resolve)
	return h.VM.ToValue(p)
}

// Handler returns the handler registered for control and gesture on
// layer ("" for the base layer).
func (h *Host) Handler(control, gesture, layer string) (goja.Callable, error) {
	for _, r := range h.Registrations {
		if r.Control == control && r.Gesture == gesture && r.Layer == layer {
			return r.Fn, nil
		}
	}
	return nil, fmt.Errorf("no handler for %s %s on layer %q", control, gesture, layer)
}

// Event is the part of InputEvent (SCRIPTING §2.1) the spike host fills in.
type Event struct {
	Control, Gesture, Scope string
	// Time and Duration are in milliseconds.
	Time, Duration float64
	Count          int
	Cycle          int64
	Held           bool
}

// NewEvent returns a fresh event object for a handler call. Its methods
// (pass, holdKeys, holdLayer) are on a shared prototype and record into
// the Host.
func (h *Host) NewEvent(e Event) *goja.Object {
	o := h.VM.CreateObject(h.eventProto)
	mustSet(o, "control", h.name(e.Control))
	mustSet(o, "gesture", h.name(e.Gesture))
	mustSet(o, "time", e.Time)
	mustSet(o, "duration", e.Duration)
	mustSet(o, "count", e.Count)
	mustSet(o, "scope", h.name(e.Scope))
	mustSet(o, "cycle", e.Cycle)
	mustSet(o, "held", e.Held)
	return o
}

// NewStickEvent returns a stick sample object: x and y with +y up, and dt
// in milliseconds.
func (h *Host) NewStickEvent(control string, x, y, dt float64) *goja.Object {
	o := h.VM.NewObject()
	mustSet(o, "control", h.name(control))
	mustSet(o, "gesture", h.name("stick"))
	mustSet(o, "x", x)
	mustSet(o, "y", y)
	mustSet(o, "dt", dt)
	return o
}

// name interns s as a JavaScript string, as the M5 host would for control
// and gesture names.
func (h *Host) name(s string) goja.Value {
	v, ok := h.names[s]
	if !ok {
		v = h.VM.ToValue(s)
		h.names[s] = v
	}
	return v
}

// Dispatch calls fn with ev and returns once the microtasks it queued
// have run. goja runs its job queue itself whenever the outermost call
// returns to Go, so there is no drain loop to write: the promise
// reactions of an async handler run inside this call, up to its first
// await on something still pending, such as a sleep.
func (h *Host) Dispatch(fn goja.Callable, ev goja.Value) error {
	_, err := fn(goja.Undefined(), ev)
	return err
}

// PendingSleeps is the number of sleep promises waiting for ResumeSleeps.
func (h *Host) PendingSleeps() int { return len(h.sleeps) }

// ResumeSleeps resolves the pending sleeps, as a timer would in M5.
// Each resolve is an outermost call, so the continuations run before it
// returns; sleeps they start stay pending for the next ResumeSleeps.
func (h *Host) ResumeSleeps() error {
	pending := h.sleeps
	h.sleeps = h.spare[:0]
	var errs []error
	for i, resolve := range pending {
		if err := resolve(goja.Undefined()); err != nil {
			errs = append(errs, err)
		}
		pending[i] = nil
	}
	h.spare = pending[:0]
	return errors.Join(errs...)
}

// Reset forgets the recorded taps, holds and passes, keeping the slices'
// memory so a benchmark loop does not allocate for them.
func (h *Host) Reset() {
	h.Taps = h.Taps[:0]
	h.Holds = h.Holds[:0]
	h.HeldLayers = h.HeldLayers[:0]
	h.Passes = 0
}

// mustSet sets a property on an object the host created. That only fails
// for frozen or exotic objects, so a failure is a bug here.
func mustSet(o *goja.Object, name string, v any) {
	if err := o.Set(name, v); err != nil {
		panic(fmt.Sprintf("spike: set %s: %v", name, err))
	}
}
