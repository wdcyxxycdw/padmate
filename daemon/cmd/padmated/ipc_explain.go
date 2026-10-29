package main

import (
	"context"
	"encoding/json"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

func (r *liveRun) rpcActivateProfile(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		ID json.RawMessage `json:"id"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	if len(p.ID) == 0 {
		return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "id is required; null disables mapping"}
	}
	var id string
	if string(p.ID) != "null" {
		if err := json.Unmarshal(p.ID, &id); err != nil || !profile.ValidLibraryID(id) {
			return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "id must be a library ID or null"}
		}
	}
	r.profileMu.Lock()
	defer r.profileMu.Unlock()
	if err := r.activateProfileLocked(ctx, id, nil); err != nil {
		return nil, libraryRPCError(err)
	}
	target := r.targetLocked()
	return map[string]any{"profileId": target.id, "generation": target.gen}, nil
}

type rpcExplanation struct {
	Control         string `json:"control"`
	ProfileID       string `json:"profileId"`
	Name            string `json:"name"`
	DeclarativeOnly bool   `json:"declarativeOnly"`
	// FunctionKey is the function key config.json chooses (MAP-27, ADR-0075),
	// "back", "start" or "guide": the one for every slot, and the key the
	// FunctionSet of each slot belongs to.
	FunctionKey string `json:"functionKey"`
	// IsFunctionKey is set when the control is the function key: it sends
	// nothing, and a binding on it in a layer or in the set is ignored
	// (Resolution "ignored"), so there is no binding to explain.
	IsFunctionKey bool                 `json:"isFunctionKey"`
	Slots         []rpcSlotExplanation `json:"slots"`
}

type rpcSlotExplanation struct {
	Index  int              `json:"slot"` // -1 when querying a library profile
	Device input.DeviceID   `json:"device"`
	Layers []rpcLayerResult `json:"layers"`
	// FunctionHeld is set while the function key is held on the slot now; a
	// library profile holds none.
	FunctionHeld bool `json:"functionHeld"`
	// FunctionSet is the profile's function set for the control, always there
	// (ADR-0071, ADR-0075). It wins over the layers for the control while the
	// function key is held and the set binds the control.
	FunctionSet rpcFunctionSetResult `json:"functionSet"`
}

// rpcFunctionSetResult is the result of the function set for the control,
// shaped like a layer's: Layer is the name of the level in decisions,
// "function:<key>", such as "function:start". Active says the function key is
// held now. Resolution is "inactive" while it is not held, "fallthrough" when
// it is held and the set does not bind the control, "ignored" when the control
// is the function key itself, and otherwise "binding", "blocked" or
// "label-only" as a layer's.
type rpcFunctionSetResult struct {
	rpcLayerResult
	// Key is the function key, "back", "start" or "guide".
	Key string `json:"key"`
	// HasSet is whether the profile has a function set at all.
	HasSet bool `json:"hasSet"`
}

type rpcLayerResult struct {
	Layer      string            `json:"layer"`
	Active     bool              `json:"active"`
	Selected   bool              `json:"selected"`
	Binding    string            `json:"binding,omitempty"`
	Label      string            `json:"label,omitempty"`
	Block      bool              `json:"block"`
	Latch      bool              `json:"latch"`
	Resolution string            `json:"resolution"`
	Actions    map[string]string `json:"actions"`
}

// explainLayers lists every declared layer, including inactive and empty
// ones. A binding (even label-only) owns all gestures; only its absence falls
// through. Script handlers are separately discoverable via registrations.
func explainLayers(p *profile.Profile, c control.Control, state engine.LayerState) []rpcLayerResult {
	priority := map[string]int{"common": 1, state.Base: 2}
	for i, name := range state.Overlays {
		priority[name] = i + 3
	}
	layers := append([]*profile.Layer{}, p.Layers...)
	if p.Common != nil {
		layers = append(layers, p.Common)
	}
	result := make([]rpcLayerResult, 0, len(layers))
	selected, best := -1, 0
	for _, layer := range layers {
		rank := priority[layer.Name]
		entry := rpcLayerResult{Layer: layer.Name, Active: rank > 0, Actions: map[string]string{}, Resolution: "inactive"}
		binding := layer.Binding(c)
		if entry.Active {
			entry.Resolution = "fallthrough"
		}
		if binding != nil {
			explainLayerEntry(&entry, binding)
			if entry.Active {
				entry.Resolution = "shadowed"
			}
			if rank > best {
				selected, best = len(result), rank
			}
		}
		result = append(result, entry)
	}
	if selected >= 0 {
		entry := &result[selected]
		entry.Selected, entry.Resolution = true, "binding"
		if entry.Block {
			entry.Resolution = "blocked"
		} else if len(entry.Actions) == 0 {
			entry.Resolution = "label-only"
		}
	}
	return result
}

// explainLayerEntry describes binding, the one a layer or a function set has
// for the control, or nil: its identity and its actions by gesture.
func explainLayerEntry(entry *rpcLayerResult, binding *profile.Binding) {
	entry.Binding, entry.Label, entry.Block, entry.Latch = binding.ID, binding.Label, binding.Block, binding.Latch
	for g := profile.GesturePress; g <= profile.GestureHold; g++ {
		if a := binding.Action(g); a != nil {
			entry.Actions[g.String()] = a.String()
		}
	}
}

// explainFunctionSet describes the function set of p for c, with key as the
// function key and held saying whether it is held now. Where the held key's set
// binds c, no layer is selected: the lookup stops at the set, and layers is
// changed to say so. When c is the function key itself nothing is selected: its
// bindings, in the layers and in the set, are ignored.
func explainFunctionSet(p *profile.Profile, key, c control.Control, held bool, layers []rpcLayerResult) rpcFunctionSetResult {
	set := p.EffectiveFunctionSet()
	entry := rpcFunctionSetResult{Key: key.String(), HasSet: set != nil}
	entry.Layer, entry.Actions, entry.Resolution = profile.FunctionLevel(key), map[string]string{}, "inactive"
	entry.Active = held
	if held {
		entry.Resolution = "fallthrough"
	}
	var binding *profile.Binding
	if set != nil {
		binding = set.Binding(c)
	}
	if binding != nil {
		explainLayerEntry(&entry.rpcLayerResult, binding)
	}
	if c == key {
		// The function key has no binding that does anything.
		for i := range layers {
			if layers[i].Binding != "" {
				layers[i].Selected, layers[i].Resolution = false, "ignored"
			}
		}
		if binding != nil {
			entry.Resolution = "ignored"
		}
		return entry
	}
	if binding != nil && held {
		entry.Selected, entry.Resolution = true, "binding"
		if entry.Block {
			entry.Resolution = "blocked"
		} else if len(entry.Actions) == 0 {
			entry.Resolution = "label-only"
		}
	}
	if entry.Selected {
		for i := range layers {
			if layers[i].Selected {
				layers[i].Selected, layers[i].Resolution = false, "shadowed"
			}
		}
	}
	return entry
}

// explainSlot explains c on one slot, from the layers and the held function
// key of its view (a library profile has the default layer and no key held);
// key is the function key of the config.
func explainSlot(p *profile.Profile, key, c control.Control, index int, device input.DeviceID, v engine.View) rpcSlotExplanation {
	layers := explainLayers(p, c, v.Layers)
	held := v.FunctionKey != control.Invalid
	return rpcSlotExplanation{Index: index, Device: device, Layers: layers, FunctionHeld: held, FunctionSet: explainFunctionSet(p, key, c, held, layers)}
}

func (r *liveRun) rpcExplainProfile(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		ID      string `json:"id,omitempty"`
		Device  string `json:"device,omitempty"`
		Control string `json:"control"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	c, ok := control.Parse(p.Control)
	if !ok {
		return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "unknown control"}
	}
	key := r.selectionConfig().FunctionKeyOrDefault()
	result := rpcExplanation{Control: c.String(), DeclarativeOnly: true, FunctionKey: key.String(), IsFunctionKey: c == key, Slots: []rpcSlotExplanation{}}
	if p.ID != "" {
		if p.Device != "" {
			return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "select either a library id or a live device"}
		}
		l, err := r.library()
		if err != nil {
			return nil, err
		}
		doc, err := l.Load(p.ID)
		if err != nil {
			return nil, libraryRPCError(err)
		}
		base := doc.Profile.DefaultLayer
		if base == "" {
			base = "base"
		}
		result.ProfileID, result.Name = p.ID, doc.Name
		result.Slots = append(result.Slots, explainSlot(doc.Profile, key, c, -1, 0, engine.View{Layers: engine.LayerState{Base: base}}))
		return result, nil
	}
	r.profileMu.Lock()
	defer r.profileMu.Unlock()
	views, err := r.sup.Inspect(ctx)
	if err != nil {
		return nil, err
	}
	result.ProfileID, result.Name = r.targetLocked().id, r.cfg.profile.Name
	for _, v := range views {
		if matchesDevice(p.Device, v.Key, v.Device) {
			result.Slots = append(result.Slots, explainSlot(r.cfg.profile, key, c, int(v.Index), v.Device, v.Mapping))
		}
	}
	if p.Device != "" && len(result.Slots) == 0 {
		return nil, &jrpc2.Error{Code: -32004, Message: "device has no available mapping slot"}
	}
	return result, nil
}
