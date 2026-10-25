package script

import (
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// AppInfo contains only process metadata. Window titles are never collected.
type AppInfo struct {
	Exe      string
	Path     string
	PID      uint32
	Elevated bool
}

// Environment shares immutable snapshots with slot-owned hosts. Publishers
// never run JavaScript or access an engine. The zero value is ready to use.
type Environment struct {
	app       atomic.Pointer[AppInfo]
	profiles  atomic.Pointer[[]string]
	hud       atomic.Pointer[hudHolder]
	effects   atomic.Pointer[effectsHolder]
	mu        sync.Mutex
	listeners map[*Host]func()
}

// SetApp publishes the latest foreground. Intermediate changes are coalesced.
func (e *Environment) SetApp(app *AppInfo) {
	e.mu.Lock()
	defer e.mu.Unlock()
	old := e.app.Load()
	if old == nil && app == nil || old != nil && app != nil && *old == *app {
		return
	}
	if app != nil {
		copyApp := *app
		app = &copyApp
	}
	e.app.Store(app)
	for _, wake := range e.listeners {
		if wake != nil {
			wake()
		}
	}
}

// SetProfiles updates a directory cache prepared away from the slot threads.
func (e *Environment) SetProfiles(ids []string) {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	e.profiles.Store(&ids)
}

func (h *Host) startEnvironment() {
	if h.environment == nil || h.candidate || h.disposed || h.mode == "describe" {
		return
	}
	e := h.environment
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.listeners == nil {
		e.listeners = make(map[*Host]func())
	}
	h.appVersion = e.app.Load()
	e.listeners[h] = h.environmentWake
}

func (h *Host) stopEnvironment() {
	if h.environment == nil {
		return
	}
	h.environment.mu.Lock()
	delete(h.environment.listeners, h)
	h.environment.mu.Unlock()
}

func (h *Host) environmentPending() bool {
	return h.active && !h.candidate && !h.disposed && h.environment != nil && h.appVersion != h.environment.app.Load()
}

func (h *Host) pollEnvironment() {
	if !h.environmentPending() {
		return
	}
	app := h.environment.app.Load()
	h.appVersion = app
	for _, listener := range h.appListeners {
		h.Loop.QueueMicrotask(func() {
			if listener.disposed || !h.active || h.disposed {
				return
			}
			_, err := listener.fn(goja.Undefined(), h.appValue(app))
			h.base.addError(Diagnose(PhaseHandler, err))
		})
	}
}

func (h *Host) currentApp() *AppInfo {
	if h.environment == nil {
		return nil
	}
	return h.environment.app.Load()
}

func (h *Host) appValue(a *AppInfo) goja.Value {
	if a == nil {
		return goja.Null()
	}
	o := h.VM.NewObject()
	for _, field := range []struct {
		name  string
		value any
	}{
		{"exe", a.Exe}, {"path", a.Path}, {"pid", a.PID}, {"elevated", a.Elevated},
	} {
		_ = o.DefineDataProperty(field.name, h.VM.ToValue(field.value), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
	}
	return o
}

func (h *Host) installEnvironmentAPI(host *goja.Object) {
	app := h.VM.NewObject()
	_ = app.DefineAccessorProperty("current", h.VM.ToValue(func(goja.FunctionCall) goja.Value { return h.appValue(h.currentApp()) }), nil, goja.FLAG_FALSE, goja.FLAG_TRUE)
	setFunction(h.VM, app, "is", 1, func(call goja.FunctionCall) goja.Value {
		a := h.currentApp()
		return h.VM.ToValue(a != nil && strings.EqualFold(a.Exe, call.Argument(0).String()))
	})
	setFunction(h.VM, app, "onChange", 1, func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			panic(h.VM.NewTypeError("app.onChange: callback must be a function"))
		}
		listener := &layerListener{fn: fn}
		h.appListeners = append(h.appListeners, listener)
		return h.VM.ToValue(h.disposable(func() { listener.disposed = true }))
	})
	set(host, "app", app)
	p := h.VM.NewObject()
	_ = p.DefineAccessorProperty("current", h.VM.ToValue(func(goja.FunctionCall) goja.Value { return h.VM.ToValue(h.profile) }), nil, goja.FLAG_FALSE, goja.FLAG_TRUE)
	setFunction(h.VM, p, "list", 0, func(goja.FunctionCall) goja.Value {
		ids := []string{}
		if h.environment != nil {
			if snapshot := h.environment.profiles.Load(); snapshot != nil {
				ids = append(ids, (*snapshot)...)
			}
		}
		return h.VM.ToValue(ids)
	})
	setFunction(h.VM, p, "switchTo", 1, func(call goja.FunctionCall) goja.Value {
		id, ok := call.Argument(0).Export().(string)
		if !ok || !profile.ValidLibraryID(id) {
			panic(h.VM.NewTypeError("profiles.switchTo: invalid profile library id"))
		}
		if h.setup || h.candidate || !h.active || h.disposed || h.mode == "describe" {
			panic(h.VM.NewTypeError("profiles.switchTo: requires an active handler or onActivate"))
		}
		if h.switchProfile == nil {
			panic(h.VM.NewTypeError("profiles.switchTo: no profile library is connected"))
		}
		if err := h.switchProfile(id); err != nil {
			panic(h.VM.NewTypeError(err.Error()))
		}
		return goja.Undefined()
	})
	set(host, "profiles", p)
}
