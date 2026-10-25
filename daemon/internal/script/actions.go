package script

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dop251/goja"
)

// ActionInfo describes a named action without exposing a VM object.
type ActionInfo struct {
	Name  string               `json:"name"`
	Label string               `json:"label,omitempty"`
	Args  map[string]ParamInfo `json:"args"`
}

type namedAction struct {
	info ActionInfo
	run  goja.Callable
}

func (h *Host) initActions(opts HostOptions) {
	h.profile = opts.Profile
	h.rawParams, h.paramsError = cloneJSONValues(opts.Params)
	h.paramSpecs = make(map[*goja.Object]ParamInfo)
	h.actions = make(map[string]namedAction)
	h.paramSchema = make(map[string]ParamInfo)
	h.paramsValue = h.readonlyValues(nil)
	h.contextValue = h.VM.NewObject()
	_ = h.contextValue.DefineAccessorProperty("params", h.VM.ToValue(func(goja.FunctionCall) goja.Value {
		return h.paramsValue
	}), goja.Undefined(), goja.FLAG_FALSE, goja.FLAG_TRUE)
	_ = h.contextValue.DefineDataProperty("profile", h.VM.ToValue(h.profile), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
}

func (h *Host) installActions(root *goja.Object) {
	set(root, "param", h.parameterAPI())
	setFunction(h.VM, root, "defineAction", 1, func(call goja.FunctionCall) goja.Value {
		if _, err := h.readAction("", call.Argument(0)); err != nil {
			panic(h.VM.NewTypeError("defineAction: %s", err))
		}
		return call.Argument(0)
	})
}

func (h *Host) readAction(name string, v goja.Value) (namedAction, error) {
	var action namedAction
	o, ok := v.(*goja.Object)
	if !ok {
		return action, fmt.Errorf("action %q must be an object", name)
	}
	action.run, ok = goja.AssertFunction(o.Get("run"))
	if !ok {
		return action, fmt.Errorf("action %q run must be a function", name)
	}
	action.info.Name = name
	if v := o.Get("label"); !absent(v) {
		action.info.Label, ok = jsPrimitive(v).(string)
		if !ok {
			return action, fmt.Errorf("action %q label must be a string", name)
		}
	}
	var err error
	action.info.Args, err = h.readParamSchema(o.Get("args"))
	return action, err
}

func (h *Host) loadActionDefinition(def *goja.Object) error {
	if h.paramsError != nil {
		return h.paramsError
	}
	schema, err := h.readParamSchema(def.Get("params"))
	if err != nil {
		return err
	}
	values, err := normalizeParams(schema, h.rawParams)
	if err != nil {
		return err
	}
	h.paramSchema, h.paramsValue = schema, h.readonlyValues(values)
	v := def.Get("actions")
	if absent(v) {
		return nil
	}
	actions, ok := v.(*goja.Object)
	if !ok || actions.ClassName() != "Object" {
		return fmt.Errorf("actions must be an object")
	}
	for _, name := range actions.Keys() {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("action name must not be blank")
		}
		action, err := h.readAction(name, actions.Get(name))
		if err != nil {
			return err
		}
		h.actions[name] = action
	}
	return nil
}

// UpdateParams validates a complete replacement without re-running setup.
// A failed update leaves the current context untouched.
func (h *Host) UpdateParams(raw map[string]any) error {
	commit, err := h.PrepareParams(raw)
	if err != nil {
		return err
	}
	commit()
	return nil
}

// Actions returns detached metadata, safe for consumers outside the VM.
func (h *Host) Actions() []ActionInfo {
	names := make([]string, 0, len(h.actions))
	for name := range h.actions {
		names = append(names, name)
	}
	slices.Sort(names)
	infos := make([]ActionInfo, 0, len(names))
	for _, name := range names {
		info := h.actions[name].info
		args := make(map[string]ParamInfo, len(info.Args))
		for key, param := range info.Args {
			args[key] = param.clone()
		}
		info.Args = args
		infos = append(infos, info)
	}
	return infos
}

// InvokeAction runs a named declaration with the same watchdog and event
// helpers as an input handler. Async continuations remain slot-owned.
func (h *Host) InvokeAction(name string, args map[string]any, e Event) error {
	if h.mode == "describe" {
		return fmt.Errorf("actions cannot run in describe mode")
	}
	if h.disposed || !h.active || h.setup {
		return nil
	}
	action, ok := h.actions[name]
	if !ok {
		return fmt.Errorf("unknown script action %q", name)
	}
	_, err := h.withWatchdog(WatchdogCallHandler, func() (goja.Value, error) {
		values, err := normalizeParams(action.info.Args, args)
		if err != nil {
			return nil, fmt.Errorf("action %q: %w", name, err)
		}
		return action.run(goja.Undefined(), h.readonlyValues(values), h.contextValue, h.event(e))
	})
	return errors.Join(Diagnose(PhaseHandler, err), h.takeErrors())
}

// InvokeAction applies the lifecycle error policy to named actions as well.
func (l *Lifecycle) InvokeAction(name string, args map[string]any, e Event) error {
	if l.current == nil {
		return nil
	}
	err := l.current.InvokeAction(name, args, e)
	l.handleRuntimeFailure(err)
	return err
}
