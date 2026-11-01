package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/padfamily"
	"github.com/wdcyxxycdw/padmate/daemon/internal/padlabel"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

// The settings of each controller (ADR-0017): the function key, the feedback
// switches and the emergency combo are settings of a controller, not global,
// since controllers differ. config.json keeps them in devices.<DeviceKey>; the
// daemon lists the controllers that have been connected, and writes one
// controller's settings, on behalf of the settings page.
//
//	pads.list                      → {revision, legacy, pads:[…]}
//	pads.set {key, revision?, functionKey?, feedback?, panic?} → {…saved, pad}
//	pads.migrate {revision?}       → {changed, …saved, pads}
//
// A member of pads.set that is absent stays; null takes the controller's own
// value away (the retired top-level member, or the family's default, applies
// again); anything else sets it. These methods are part of the bridge's
// allowlist (padmatectl ui-bridge).

// rpcPadOption is a button that can be a controller's function key.
type rpcPadOption struct {
	Control string `json:"control"`
	// Label is the name printed on the controller: "ZR", "Menu", "−".
	Label string `json:"label"`
}

type rpcPadFunctionKey struct {
	// Default and Options are those of the family; Default is Options[0].
	Default string         `json:"default"`
	Options []rpcPadOption `json:"options"`
	// Stored is devices.<key>.functionKey as the file has it, absent when it
	// has none; Effective is the function key that applies, Source where it
	// comes from: "device", "legacy" or "default".
	Stored    string `json:"stored,omitempty"`
	Effective string `json:"effective"`
	Source    string `json:"source"`
}

// rpcPadSwitches are the four feedback switches (ADR-0015).
type rpcPadSwitches struct {
	Rumble     bool `json:"rumble"`
	LED        bool `json:"led"`
	Pause      bool `json:"pause"`
	LowBattery bool `json:"lowBattery"`
}

// rpcPadStoredSwitches are the switches devices.<key>.feedback sets.
type rpcPadStoredSwitches struct {
	Rumble     *bool `json:"rumble,omitempty"`
	LED        *bool `json:"led,omitempty"`
	Pause      *bool `json:"pause,omitempty"`
	LowBattery *bool `json:"lowBattery,omitempty"`
}

type rpcPadFeedback struct {
	Stored    *rpcPadStoredSwitches `json:"stored,omitempty"`
	Effective rpcPadSwitches        `json:"effective"`
	// Source is the strongest source among the switches.
	Source string `json:"source"`
}

type rpcPadPanic struct {
	// Default is the family's built-in combo.
	Default rpcPadCombo  `json:"default"`
	Stored  *rpcPadCombo `json:"stored,omitempty"`
	// Effective is the combo T-input detects: the one that is configured if
	// the controller has all its buttons, else Default.
	Effective rpcPadCombo `json:"effective"`
	Source    string      `json:"source"`
}

type rpcPadCombo struct {
	Buttons []string `json:"buttons"`
	HoldMs  int      `json:"holdMs"`
}

// rpcPad is one controller that has been connected, or that config.json
// has settings for.
type rpcPad struct {
	Key string `json:"key"`
	// Name and Type are the last the daemon saw (state.json); empty and
	// "unknown" with Known false for a controller that only config.json
	// mentions.
	Name       string `json:"name"`
	Type       string `json:"type"`
	ButtonMask uint32 `json:"buttonMask"`
	Known      bool   `json:"known"`
	Connected  bool   `json:"connected"`
	// Device is the connection ID while connected, as in devices.list.
	Device  *input.DeviceID `json:"device,omitempty"`
	Ignored bool            `json:"ignored"`
	// LastSeen is the last connection in milliseconds since the Unix epoch,
	// the current time while connected; absent when not known.
	LastSeen int64 `json:"lastSeen,omitempty"`
	// Family is the padfamily name: standard, joyconLeft, joyconRight,
	// joyconLeftVertical, joyconRightVertical, joyconPair or unknown.
	Family      string            `json:"family"`
	FunctionKey rpcPadFunctionKey `json:"functionKey"`
	Feedback    rpcPadFeedback    `json:"feedback"`
	Panic       rpcPadPanic       `json:"panic"`
}

type rpcPadLegacy struct {
	// Any is true while config.json has a retired top-level member that
	// pads.migrate would fold into the controllers.
	Any         bool `json:"any"`
	FunctionKey bool `json:"functionKey"`
	Feedback    bool `json:"feedback"`
	Panic       bool `json:"panic"`
}

type rpcPads struct {
	// Revision is that of config.json, which the pads were read from.
	Revision string       `json:"revision"`
	Legacy   rpcPadLegacy `json:"legacy"`
	Pads     []rpcPad     `json:"pads"`
}

type rpcPadSet struct {
	rpcSaved
	Pad rpcPad `json:"pad"`
}

type rpcPadMigrated struct {
	rpcSaved
	// Changed is false when config.json had nothing to fold.
	Changed bool         `json:"changed"`
	Legacy  rpcPadLegacy `json:"legacy"`
	Pads    []rpcPad     `json:"pads"`
}

func (r *liveRun) padMethods(methods handler.Map) {
	methods["pads.list"] = r.rpcPadsList
	methods["pads.set"] = r.rpcPadsSet
	methods["pads.migrate"] = r.rpcPadsMigrate
}

// knownPad is what the daemon knows of one controller.
type knownPad struct {
	key       string
	info      input.DeviceInfo // Type, Name, ButtonMask; with Known true
	known     bool
	connected bool
	device    input.DeviceID
	lastSeen  int64
}

// verticalJoyCons is the grip hint of the input, false for one without it.
func (r *liveRun) verticalJoyCons() bool {
	if src, ok := r.src.(comboSource); ok {
		return src.VerticalJoyCons()
	}
	return false
}

// knownPads returns the controllers cfg or state.json mention or that are
// connected: the connected ones first, in connection order, then the others
// by the time they were last seen, newest first.
func (r *liveRun) knownPads(ctx context.Context, cfg *profile.Config, now time.Time) []knownPad {
	byKey := map[string]*knownPad{}
	var order []string
	pad := func(key string) *knownPad {
		p := byKey[key]
		if p == nil {
			p = &knownPad{key: key}
			byKey[key] = p
			order = append(order, key)
		}
		return p
	}
	if m := r.cfg.opts.memory; m != nil {
		for key, c := range m.Controllers() {
			p := pad(key)
			p.info = input.DeviceInfo{Name: c.Name, Type: c.Type, ButtonMask: uint32(c.ButtonMask)}
			p.known, p.lastSeen = true, int64(c.LastSeen)
		}
	}
	for key := range cfg.Devices {
		pad(key)
	}
	var connected []string
	if r.sup != nil {
		if devices, err := r.sup.Devices(ctx); err == nil {
			for _, d := range devices {
				if !d.Info.IsGamepad || d.Key == "" {
					continue
				}
				p := pad(d.Key)
				p.info = d.Info
				p.known, p.connected, p.device, p.lastSeen = true, true, d.Info.ID, now.UnixMilli()
				connected = append(connected, d.Key)
			}
		}
	}
	rest := slices.DeleteFunc(slices.Clone(order), func(key string) bool { return byKey[key].connected })
	sort.SliceStable(rest, func(i, j int) bool {
		a, b := byKey[rest[i]], byKey[rest[j]]
		if a.lastSeen != b.lastSeen {
			return a.lastSeen > b.lastSeen
		}
		return a.key < b.key
	})
	out := make([]knownPad, 0, len(order))
	for _, key := range append(connected, rest...) {
		out = append(out, *byKey[key])
	}
	return out
}

// family is the family of a controller: by its buttons when the daemon has
// seen it, else by its key's GUID.
func (p knownPad) family(vertical bool) padfamily.Family {
	if p.known {
		return input.FamilyOf(p.info, vertical)
	}
	return padfamily.FromGUID(profile.KeyGUID(p.key), vertical)
}

// padLabel is the name printed on the controller for the control c, as the
// UI's mapping page shows it. The family stands in for a type the daemon has
// not seen.
func padLabel(info input.DeviceInfo, family padfamily.Family, c control.Control) string {
	typ, mask := info.Type, info.ButtonMask
	switch family {
	case padfamily.JoyConPair:
		typ = "joyconpair"
	case padfamily.JoyConLeft, padfamily.JoyConLeftVertical:
		typ = "joyconleft"
	case padfamily.JoyConRight, padfamily.JoyConRightVertical:
		typ = "joyconright"
	}
	if family.Sideways() {
		mask |= 1 // a south button, which a Joy-Con held sideways has (padlabel)
	}
	if label := padlabel.Labels(padlabel.Pad{Type: typ, ButtonMask: mask})[c.String()]; label != "" {
		return label
	}
	return c.String()
}

// padView builds the entry of p from the settings cfg.
func padView(cfg *profile.Config, p knownPad, vertical bool) rpcPad {
	family := p.family(vertical)
	s := cfg.PadSettings(p.key, family)
	dev := cfg.Devices[p.key]
	keys := s.Family.FunctionKeys()
	out := rpcPad{
		Key: p.key, Name: p.info.Name, Type: p.info.Type, ButtonMask: p.info.ButtonMask, Known: p.known,
		Connected: p.connected, Ignored: dev.Ignored, LastSeen: p.lastSeen, Family: s.Family.String(),
	}
	if out.Type == "" {
		out.Type = "unknown"
	}
	if p.connected {
		id := p.device
		out.Device = &id
	}
	out.FunctionKey = rpcPadFunctionKey{
		Default: keys.Default.String(), Options: []rpcPadOption{}, Stored: dev.FunctionKey,
		Effective: s.FunctionKey.String(), Source: s.FunctionKeySource.String(),
	}
	for _, c := range keys.Options {
		out.FunctionKey.Options = append(out.FunctionKey.Options, rpcPadOption{Control: c.String(), Label: padLabel(p.info, s.Family, c)})
	}
	out.Feedback = rpcPadFeedback{
		Effective: rpcPadSwitches{Rumble: s.Feedback.Rumble, LED: s.Feedback.LED, Pause: s.Feedback.Pause, LowBattery: s.Feedback.LowBattery},
		Source:    s.FeedbackSource.String(),
	}
	if f := dev.Feedback; f != nil && (f.Rumble != nil || f.Led != nil || f.Pause != nil || f.LowBattery != nil) {
		out.Feedback.Stored = &rpcPadStoredSwitches{Rumble: f.Rumble, LED: f.Led, Pause: f.Pause, LowBattery: f.LowBattery}
	}
	def := profile.DefaultPanic(s.Family)
	combo, source := s.Panic, s.PanicSource
	// A controller that lacks a button of the combo keeps the built-in one,
	// so it still has an emergency pause (effectiveCombo).
	if slices.ContainsFunc(combo.Buttons, func(b string) bool { return !p.info.MayHaveButton(b) }) {
		combo, source = def, profile.SourceDefault
	}
	out.Panic = rpcPadPanic{
		Default:   rpcPadCombo{Buttons: def.Buttons, HoldMs: def.HoldMs},
		Effective: rpcPadCombo{Buttons: slices.Clone(combo.Buttons), HoldMs: combo.HoldMs},
		Source:    source.String(),
	}
	if dev.Panic != nil {
		out.Panic.Stored = &rpcPadCombo{Buttons: slices.Clone(dev.Panic.Buttons), HoldMs: dev.Panic.HoldMs}
	}
	return out
}

func padLegacy(c *profile.Config) rpcPadLegacy {
	l := c.Legacy
	return rpcPadLegacy{Any: l.Any(), FunctionKey: l.FunctionKey, Feedback: l.Feedback, Panic: l.Panic}
}

// padsConfig reads config.json as the daemon has it now (a file with errors is
// repaired first, as config.get does).
func (r *liveRun) padsConfig(l *profilelib.Library) (data []byte, revision string, cfg *profile.Config, err error) {
	_, _, _ = r.readConfig(l)
	data, revision, err = l.ConfigText()
	if err != nil {
		return nil, "", nil, libraryRPCError(err)
	}
	cfg, err = profilelib.ValidateConfig(data)
	if err != nil {
		return nil, "", nil, libraryRPCError(err)
	}
	return data, revision, cfg, nil
}

func (r *liveRun) padViews(ctx context.Context, cfg *profile.Config) []rpcPad {
	vertical := r.verticalJoyCons()
	known := r.knownPads(ctx, cfg, time.Now())
	out := make([]rpcPad, 0, len(known))
	for _, p := range known {
		out = append(out, padView(cfg, p, vertical))
	}
	return out
}

func (r *liveRun) rpcPadsList(ctx context.Context, req *jrpc2.Request) (any, error) {
	if err := req.UnmarshalParams(jrpc2.StrictFields(new(struct{}))); err != nil {
		return nil, err
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	_, revision, cfg, err := r.padsConfig(l)
	if err != nil {
		return nil, err
	}
	return rpcPads{Revision: revision, Legacy: padLegacy(cfg), Pads: r.padViews(ctx, cfg)}, nil
}

// optionalMember is a member of a request that may be absent, null or a value.
type optionalMember = json.RawMessage

func isNull(m optionalMember) bool { return string(bytes.TrimSpace(m)) == "null" }

func invalidParams(msg string) error { return &jrpc2.Error{Code: jrpc2.InvalidParams, Message: msg} }

// strictJSON decodes m into v, refusing members v does not have.
func strictJSON(m optionalMember, v any) error {
	d := json.NewDecoder(bytes.NewReader(m))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.More() {
		return errors.New("unexpected data after the value")
	}
	return nil
}

func (r *liveRun) rpcPadsSet(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		Key         string         `json:"key"`
		Revision    *string        `json:"revision"`
		FunctionKey optionalMember `json:"functionKey"`
		Feedback    optionalMember `json:"feedback"`
		Panic       optionalMember `json:"panic"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	if _, err := input.ParseDeviceKey(p.Key); err != nil || p.Key == "" {
		return nil, invalidParams("key is not a canonical DeviceKey")
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	r.configApplyMu.Lock()
	defer r.configApplyMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, revision, cfg, err := r.padsConfig(l)
	if err != nil {
		return nil, err
	}
	if p.Revision != nil && *p.Revision != revision {
		return nil, libraryRPCError(&profilelib.ConflictError{Current: revision})
	}
	vertical := r.verticalJoyCons()
	var target *knownPad
	for _, k := range r.knownPads(ctx, cfg, time.Now()) {
		if k.key == p.Key {
			target = &k
			break
		}
	}
	if target == nil {
		return nil, &jrpc2.Error{Code: -32004, Message: "unknown controller: none of that key has been connected or has settings"}
	}
	family := target.family(vertical)
	var change profile.PadChange
	if p.FunctionKey != nil {
		change.SetFunctionKey = true
		if !isNull(p.FunctionKey) {
			var name string
			if err := strictJSON(p.FunctionKey, &name); err != nil {
				return nil, invalidParams("functionKey must be a control name or null")
			}
			c, ok := control.Parse(name)
			options := family.FunctionKeys()
			if family == padfamily.Unknown {
				options = padfamily.Standard.FunctionKeys()
			}
			if !ok || c.String() != name || !options.Allows(c) {
				names := make([]string, len(options.Options))
				for i, o := range options.Options {
					names[i] = o.String()
				}
				return nil, invalidParams("functionKey " + name + " cannot be the function key of a controller of family " + family.String() + ": it can be " + strings.Join(names, ", "))
			}
			change.FunctionKey = name
		}
	}
	if p.Feedback != nil {
		change.SetFeedback = true
		if !isNull(p.Feedback) {
			var f profile.FeedbackOverrides
			if err := strictJSON(p.Feedback, &f); err != nil {
				return nil, invalidParams("feedback must be null or an object of the booleans rumble, led, pause, lowBattery")
			}
			change.Feedback = &f
		}
	}
	if p.Panic != nil {
		change.SetPanic = true
		if !isNull(p.Panic) {
			var c profile.PanicCombo
			if err := strictJSON(p.Panic, &c); err != nil || c.Buttons == nil {
				return nil, invalidParams("panic must be null or an object {buttons, holdMs}")
			}
			change.Panic = &c
		}
	}
	out, err := profile.SetPadSettings(data, p.Key, change)
	if err != nil {
		return nil, invalidParams(err.Error())
	}
	next, err := profilelib.ValidateConfig(out)
	if err != nil {
		return nil, invalidParams(err.Error())
	}
	saved := rpcSaved{Revision: revision, Applied: true}
	if !bytes.Equal(out, data) {
		saved, err = r.commitConfigLocked(l, revision, out, next)
		if err != nil {
			return nil, libraryRPCError(err)
		}
	}
	r.log.Info("controller settings saved", "device", p.Key, "family", family.String(), "functionKey", change.SetFunctionKey, "feedback", change.SetFeedback, "panic", change.SetPanic)
	return rpcPadSet{rpcSaved: saved, Pad: padView(next, *target, vertical)}, nil
}

// rpcPadsMigrate is pads.migrate: it folds the retired top-level functionKey,
// feedback and safety.panic into the controllers the daemon knows and takes
// them out of config.json (Config.Materialized, profile.MigratePadSettings),
// so a controller behaves as before. A controller that has not been connected
// since state.json began to list them takes the defaults of its family from
// then on. It is a no-op, answered with changed false, for a file without
// them.
func (r *liveRun) rpcPadsMigrate(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		Revision *string `json:"revision"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	r.configApplyMu.Lock()
	defer r.configApplyMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, revision, cfg, err := r.padsConfig(l)
	if err != nil {
		return nil, err
	}
	if p.Revision != nil && *p.Revision != revision {
		return nil, libraryRPCError(&profilelib.ConflictError{Current: revision})
	}
	vertical := r.verticalJoyCons()
	var refs []profile.PadRef
	for _, k := range r.knownPads(ctx, cfg, time.Now()) {
		refs = append(refs, profile.PadRef{Key: k.key, Family: k.family(vertical)})
	}
	out, changed, err := profile.MigratePadSettings(data, refs)
	if err != nil {
		return nil, invalidParams(err.Error())
	}
	res := rpcPadMigrated{rpcSaved: rpcSaved{Revision: revision, Applied: true}, Changed: changed}
	next := cfg
	if changed {
		next, err = profilelib.ValidateConfig(out)
		if err != nil {
			return nil, invalidParams(err.Error())
		}
		res.rpcSaved, err = r.commitConfigLocked(l, revision, out, next)
		if err != nil {
			return nil, libraryRPCError(err)
		}
		r.log.Info("controller settings migrated from the global members", "controllers", len(refs))
	}
	res.Legacy, res.Pads = padLegacy(next), r.padViews(ctx, next)
	return res, nil
}

// markPadsDirty says that a controller came or changed: the persistence
// worker then notes the connected controllers in state.json
// (rememberControllers).
func (r *liveRun) markPadsDirty() { r.padsDirty.Store(true) }

// rememberControllers records the connected controllers in state.json
// (Memory.RememberController), so that the settings page can list them when
// they are not connected. It runs on the persistence worker, never on an input,
// slot or output thread. The router is asked for the devices, as devices.list
// does, because only it knows their DeviceKeys.
func (r *liveRun) rememberControllers() {
	m := r.cfg.opts.memory
	if m == nil || r.sup == nil || !r.padsDirty.Swap(false) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	devices, err := r.sup.Devices(ctx)
	if err != nil {
		r.padsDirty.Store(true) // the next tick asks again
		return
	}
	now := time.Now().UnixMilli()
	for _, d := range devices {
		if d.Info.IsGamepad && d.Key != "" {
			m.RememberController(d.Key, profile.KnownController{Name: d.Info.Name, Type: d.Info.Type, ButtonMask: int(d.Info.ButtonMask), LastSeen: int(now)})
		}
	}
}
