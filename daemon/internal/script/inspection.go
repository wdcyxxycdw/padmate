package script

// Metadata contains detached Go values, never callbacks or VM objects. Owners
// build it on the slot loop, then RPC may encode it on another goroutine.
type Metadata struct {
	Runtime       uint64               `json:"runtime"`
	Params        map[string]ParamInfo `json:"params"`
	Actions       []ActionInfo         `json:"actions"`
	Registrations []RegistrationInfo   `json:"registrations"`
	Continuous    []ContinuousInfo     `json:"continuous"`
}

type ContinuousInfo struct {
	ID      uint64 `json:"id"`
	Kind    string `json:"kind"`
	Control string `json:"control,omitempty"`
}

type RegistrationInfo struct {
	ID       uint64         `json:"id"`
	Control  string         `json:"control"`
	Gesture  string         `json:"gesture"`
	Scope    string         `json:"scope"`
	Observe  bool           `json:"observe"`
	Claim    bool           `json:"claim"`
	Priority int            `json:"priority"`
	Label    string         `json:"label,omitempty"`
	Timing   map[string]int `json:"timing"`
}

// EmptyMetadata uses empty collections rather than null in the wire format.
func EmptyMetadata() Metadata {
	return Metadata{Params: map[string]ParamInfo{}, Actions: []ActionInfo{}, Registrations: []RegistrationInfo{}, Continuous: []ContinuousInfo{}}
}

func (h *Host) Metadata() Metadata {
	m := EmptyMetadata()
	m.Runtime, m.Params, m.Actions = h.runtimeID, h.ParamsSchema(), h.Actions()
	for i, r := range h.registrations {
		if r.disposed {
			continue
		}
		scope := "base"
		switch r.Scope {
		case ScopeGlobal:
			scope = "global"
		case ScopeLayer:
			scope = "layer:" + r.Layer
		}
		timing := map[string]int{}
		if r.Timing.HoldSet {
			timing["holdMs"] = r.Timing.HoldMs
		}
		if r.Timing.MultiTapSet {
			timing["multiTapMs"] = r.Timing.MultiTapMs
		}
		m.Registrations = append(m.Registrations, RegistrationInfo{ID: uint64(i + 1), Control: r.Control, Gesture: r.Gesture, Scope: scope, Observe: r.Observe, Claim: r.Claim, Priority: r.Priority, Label: r.Label, Timing: timing})
	}
	for i, r := range h.continuous {
		if r.disposed {
			continue
		}
		entry := ContinuousInfo{ID: uint64(i + 1), Kind: "tick"}
		if r.stick != "" {
			entry.Kind, entry.Control = "stick", r.stick
		} else if r.axis != 0 {
			entry.Kind, entry.Control = "axis", r.axis.String()
		}
		m.Continuous = append(m.Continuous, entry)
	}
	return m
}

// Inspection combines runtime lifecycle state with optional metadata.
// Runtime is zero while no installed Host exists. A pending worker is never
// inspected; only the owner may access an installed Host's metadata.
type Inspection struct {
	State        string   `json:"state"`
	Entry        string   `json:"entry,omitempty"`
	API          string   `json:"api,omitempty"`
	Paused       bool     `json:"paused"`
	Pending      bool     `json:"pending"`
	WallStopped  bool     `json:"wallStopped"`
	LastError    string   `json:"lastError,omitempty"`
	RestartCount int      `json:"restartCount"`
	Permanent    bool     `json:"permanent"`
	Metadata     Metadata `json:"metadata"`
	// Macros are the macros of the runtime with a problem (SCR-50): the
	// latest error of each run, sorted by file, and the macros disabled
	// after an interruption. A runtime built afterwards clears the errors,
	// as it does LastError; a disabled macro stays until its file changes.
	Macros []MacroStatus `json:"macros,omitempty"`
}

// MacroStatus is a macro of an Inspection.
type MacroStatus struct {
	File  string `json:"file"`
	Error string `json:"error,omitempty"`
	// Disabled reports a macro stopped after it used too much CPU or
	// overflowed its stack; saving its file loads it again.
	Disabled bool `json:"disabled,omitempty"`
}

func (h *Host) RuntimeID() uint64 { return h.runtimeID }
