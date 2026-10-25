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
	// IsFunctionKey is set when the control is itself a function key
	// (MAP-27): it sends nothing and has no binding to explain.
	IsFunctionKey bool                 `json:"isFunctionKey"`
	Slots         []rpcSlotExplanation `json:"slots"`
}

type rpcSlotExplanation struct {
	Index  int              `json:"slot"` // -1 when querying a library profile
	Device input.DeviceID   `json:"device"`
	Layers []rpcLayerResult `json:"layers"`
	// FunctionKey is the function key held on the slot now, "back" or
	// "start", and absent when none is; a library profile holds none.
	FunctionKey string `json:"functionKey,omitempty"`
	// FunctionSets is the set of each function key, back's first, always
	// both (ADR-0071). A set wins over the layers for the control while its
	// key is the one held and the set binds the control.
	FunctionSets []rpcFunctionSetResult `json:"functionSets"`
}

// rpcFunctionSetResult is the result of a function key's set for the control,
// shaped like a layer's: Layer is the name of the level in decisions,
// "function:back" or "function:start". Active says the key is the one held
// now. Resolution is "inactive" while the key is not held, "fallthrough"
// when it is held and the set does not bind the control, and otherwise
// "binding", "blocked" or "label-only" as a layer's.
type rpcFunctionSetResult struct {
	rpcLayerResult
	// Key is the function key, "back" or "start".
	Key string `json:"key"`
	// HasSet is whether the profile has a set for the key at all.
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

// explainFunctionSets lists the sets of the function keys for c, back's
// first, and marks the one that fn, the key held now (control.Invalid for
// none), selects. Where a held key's set binds c, no layer is selected: the
// lookup stops at the set, and layers is changed to say so.
func explainFunctionSets(p *profile.Profile, c control.Control, fn control.Control, layers []rpcLayerResult) []rpcFunctionSetResult {
	sets := make([]rpcFunctionSetResult, 0, 2)
	for _, key := range []control.Control{control.Back, control.Start} {
		set := p.FunctionKeys.Set(key)
		entry := rpcFunctionSetResult{Key: key.String(), HasSet: set != nil}
		entry.Layer, entry.Actions, entry.Resolution = profile.FunctionLevel(key), map[string]string{}, "inactive"
		entry.Active = key == fn
		if entry.Active {
			entry.Resolution = "fallthrough"
		}
		if set != nil {
			if binding := set.Binding(c); binding != nil {
				explainLayerEntry(&entry.rpcLayerResult, binding)
				if entry.Active {
					entry.Selected, entry.Resolution = true, "binding"
					if entry.Block {
						entry.Resolution = "blocked"
					} else if len(entry.Actions) == 0 {
						entry.Resolution = "label-only"
					}
				}
			}
		}
		sets = append(sets, entry)
	}
	for _, s := range sets {
		if !s.Selected {
			continue
		}
		for i := range layers {
			if layers[i].Selected {
				layers[i].Selected, layers[i].Resolution = false, "shadowed"
			}
		}
	}
	return sets
}

// explainSlot explains c on one slot, from the layers and the held function
// key of its view (a library profile has the default layer and no key held).
func explainSlot(p *profile.Profile, c control.Control, index int, device input.DeviceID, v engine.View) rpcSlotExplanation {
	layers := explainLayers(p, c, v.Layers)
	s := rpcSlotExplanation{Index: index, Device: device, Layers: layers, FunctionSets: explainFunctionSets(p, c, v.FunctionKey, layers)}
	if v.FunctionKey.IsFunctionKey() {
		s.FunctionKey = v.FunctionKey.String()
	}
	return s
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
	result := rpcExplanation{Control: c.String(), DeclarativeOnly: true, IsFunctionKey: c.IsFunctionKey(), Slots: []rpcSlotExplanation{}}
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
		result.Slots = append(result.Slots, explainSlot(doc.Profile, c, -1, 0, engine.View{Layers: engine.LayerState{Base: base}}))
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
			result.Slots = append(result.Slots, explainSlot(r.cfg.profile, c, int(v.Index), v.Device, v.Mapping))
		}
	}
	if p.Device != "" && len(result.Slots) == 0 {
		return nil, &jrpc2.Error{Code: -32004, Message: "device has no available mapping slot"}
	}
	return result, nil
}
