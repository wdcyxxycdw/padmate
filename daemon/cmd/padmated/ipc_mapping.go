package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
	"github.com/wdcyxxycdw/padmate/daemon/internal/sdlmapping"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

// The mapping wizard and custom mappings of the daemon (IN-17, IN-18, IN-19,
// ADR-0072): devices.rawInfo and the input.raw topic give the UI the elements
// of a joystick, devices.setMapping and devices.resetMapping save and apply a
// mapping for it.

// The error codes of these methods.
const (
	// rpcCodeMappingInvalid is a mapping that cannot be saved: it does not
	// parse, binds nothing, or is for another GUID than the device's. data is
	// {reason}.
	rpcCodeMappingInvalid = -32025
	// rpcCodeMappingUnsupported is a request the input cannot serve: it has no
	// gamepad mappings or raw events (the fake input), or the controller is
	// mapped and the mapping is not paused (capture.start).
	rpcCodeMappingUnsupported = -32026
)

// mappingSource is the part of an *input.Source that custom mappings need
// (IN-18). A source without it, such as a test's fake, keeps config.json's
// mappings without applying them.
type mappingSource interface {
	SetMappings([]string) (input.MappingReport, error)
}

// rawSource is the part of an *input.Source that the raw stream needs
// (IN-17).
type rawSource interface {
	RawEvents() <-chan input.RawEvent
	RawStart(input.DeviceID) (input.RawInfo, error)
	RawStop(input.DeviceID) error
}

// startupMappings returns the mappings that Open gives SDL before it
// enumerates the devices: those of config.json at the start. The list is
// never nil: it makes Open keep the database file that the later changes
// rewrite.
func startupMappings(c *profile.Config) []string {
	if c == nil {
		return []string{}
	}
	return append([]string{}, profile.DeviceMappings(c.Devices)...)
}

// mappingOutcome is what the last application of the mappings found, for the
// method that caused it.
type mappingOutcome struct {
	report input.MappingReport
	err    error
}

// applyMappings gives the input the mappings of next, the effective config
// just stored, when they differ from the ones before it (applyLiveSettings).
// The caller holds configApplyMu. The input reports each device it read the
// mapping back for; a mapping that SDL does not read back is logged, it is no
// error of the commit.
func (r *liveRun) applyMappings(next *profile.Config) error {
	src, ok := r.src.(mappingSource)
	if !ok {
		return nil
	}
	list := profile.DeviceMappings(next.Devices)
	rep, err := src.SetMappings(list)
	r.mappingOutcome.Store(&mappingOutcome{report: rep, err: err})
	if err != nil {
		r.log.Warn("gamepad mappings not applied", "err", err)
		return fmt.Errorf("gamepad mapping not applied: %w", err)
	}
	for _, d := range rep.Devices {
		if !d.Applied {
			r.log.Warn("SDL does not read the mapping it was given", "device", d.Device, "gamepad", d.Gamepad, "read", d.Got)
		}
	}
	r.log.Info("gamepad mappings applied", "mappings", len(list), "devices", len(rep.Devices))
	return nil
}

// rawFeed publishes the raw events of the devices that clients asked for as
// input.raw events, and keeps who asked: an event goes only to the connections
// that asked for its device.
type rawFeed struct {
	// ctl serializes the starting and stopping of a device with the notes of
	// who asks for it (rawAcquire, rawRelease). It is held across calls to the
	// input; mu never is.
	ctl  sync.Mutex
	mu   sync.Mutex
	by   map[*ipc.Session]map[input.DeviceID]bool // what each connection asked for
	stop context.CancelFunc
	done chan struct{}
}

// rpcRaw is the data of an input.raw event: the element of a joystick that
// changed (IN-17). Device is the connection ID, as in the input topic; kind is
// button (value 1 or 0), axis (value -32768..32767) or hat (value the
// direction mask: up 1, right 2, down 4, left 8, 0 centered).
type rpcRaw struct {
	TimeNs uint64         `json:"timeNs"`
	Device input.DeviceID `json:"device"`
	Kind   string         `json:"kind"`
	Index  uint8          `json:"index"`
	Value  int16          `json:"value"`
}

// startRawFeed starts the goroutine that publishes the raw events, when the
// input has any. It publishes only while a client subscribes to input.raw.
func (r *liveRun) startRawFeed() {
	rs, ok := r.src.(rawSource)
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(r.end)
	r.raw.stop, r.raw.done = cancel, make(chan struct{})
	go func() {
		defer close(r.raw.done)
		defer func() {
			if v := recover(); v != nil {
				r.log.Error("raw feed stopped", "panic", v)
			}
		}()
		events := rs.RawEvents()
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-events:
				if !ok {
					return
				}
				if r.events.Listening("input.raw") {
					r.events.PublishTo("input.raw", rpcRaw{TimeNs: e.Time, Device: e.Device, Kind: e.Kind.String(), Index: e.Index, Value: e.Value},
						func(session *ipc.Session) bool { return r.raw.wants(session, e.Device) })
				}
			}
		}
	}()
}

// stopRawFeed stops the publisher and waits for it, watchStopWait at most.
func (r *liveRun) stopRawFeed() {
	if r.raw.stop == nil {
		return
	}
	r.raw.stop()
	select {
	case <-r.raw.done:
	case <-time.After(watchStopWait):
		r.log.Debug("raw feed did not stop before shutdown")
	}
}

// add notes that session asks for the raw events of id.
func (f *rawFeed) add(session *ipc.Session, id input.DeviceID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.by == nil {
		f.by = map[*ipc.Session]map[input.DeviceID]bool{}
	}
	if f.by[session] == nil {
		f.by[session] = map[input.DeviceID]bool{}
	}
	f.by[session][id] = true
}

// drop notes that session no longer asks for id. It reports whether it had,
// and whether now nobody does, which is when the device stops sending.
func (f *rawFeed) drop(session *ipc.Session, id input.DeviceID) (had, last bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	had = f.by[session][id]
	delete(f.by[session], id)
	if len(f.by[session]) == 0 {
		delete(f.by, session)
	}
	for _, ids := range f.by {
		if ids[id] {
			return had, false
		}
	}
	return had, true
}

// asked returns the devices that session asked for.
func (f *rawFeed) asked(session *ipc.Session) []input.DeviceID {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]input.DeviceID, 0, len(f.by[session]))
	for id := range f.by[session] {
		ids = append(ids, id)
	}
	return ids
}

// rawAcquire starts the raw events of the device id and notes that session
// asks for them. It and rawRelease run one at a time: a session that lets go
// of a device nobody else asks for stops it, and must not do so after another
// session has asked and been told that the device sends.
func (r *liveRun) rawAcquire(rs rawSource, session *ipc.Session, id input.DeviceID) (input.RawInfo, error) {
	r.raw.ctl.Lock()
	defer r.raw.ctl.Unlock()
	info, err := rs.RawStart(id)
	if err != nil {
		return info, err
	}
	r.raw.add(session, id)
	return info, nil
}

// rawRelease notes that session no longer asks for the device id, and stops
// its raw events when nobody else does.
func (r *liveRun) rawRelease(session *ipc.Session, id input.DeviceID) {
	r.raw.ctl.Lock()
	defer r.raw.ctl.Unlock()
	if _, last := r.raw.drop(session, id); last {
		r.rawStop(id)
	}
}

// wants reports whether session asked for the raw events of id.
func (f *rawFeed) wants(session *ipc.Session, id input.DeviceID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.by[session][id]
}

// rpcRawInfo is the result of devices.rawInfo.
type rpcRawInfo struct {
	// Device is the DeviceKey and ID the connection ID, as devices.list gives
	// them.
	Device string         `json:"device"`
	ID     input.DeviceID `json:"id"`
	GUID   string         `json:"guid"`
	// Gamepad is true when SDL reads the controller as a gamepad now.
	Gamepad  bool    `json:"gamepad"`
	Axes     int     `json:"axes"`
	Buttons  int     `json:"buttons"`
	Hats     int     `json:"hats"`
	AxisRest []int16 `json:"axisRest"`
	// Watching is true while raw events of the device are published as input.raw
	// events to this session's subscription.
	Watching bool `json:"watching"`
}

// mappingMethods adds the methods of the mapping wizard to methods; session
// owns the raw watches that its calls start.
func (r *liveRun) mappingMethods(methods handler.Map, session *ipc.Session) {
	methods["devices.setMapping"] = r.rpcSetMapping
	methods["devices.resetMapping"] = r.rpcResetMapping
	session.OnClose(func() {
		for _, id := range r.raw.asked(session) {
			r.rawRelease(session, id)
		}
	})
	methods["devices.rawInfo"] = func(ctx context.Context, req *jrpc2.Request) (any, error) {
		var p struct {
			Device string `json:"device"`
			Watch  *bool  `json:"watch"`
		}
		if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
			return nil, err
		}
		dev, err := r.singleDevice(ctx, p.Device)
		if err != nil {
			return nil, err
		}
		rs, ok := r.src.(rawSource)
		if !ok {
			return nil, &jrpc2.Error{Code: rpcCodeMappingUnsupported, Message: "this input has no raw joystick events"}
		}
		id := dev.Info.ID
		if p.Watch != nil && !*p.Watch {
			r.rawRelease(session, id)
			return rpcRawInfo{Device: dev.Key, ID: id, GUID: dev.Info.GUID, Gamepad: dev.Info.IsGamepad, AxisRest: []int16{}}, nil
		}
		// A gamepad sends its keys as the user presses what the wizard asks for,
		// and the press of one that waits for a slot takes a slot over (IN-05): the
		// mapping must be paused first (capture.start). A joystick that SDL has no
		// mapping for maps to nothing.
		if dev.Info.IsGamepad && !r.sup.Status().Capturing {
			return nil, &jrpc2.Error{Code: rpcCodeMappingUnsupported, Message: "the controller is a gamepad: call capture.start first so that its buttons send no keys"}
		}
		info, err := r.rawAcquire(rs, session, id)
		if errors.Is(err, input.ErrNoDevice) {
			return nil, &jrpc2.Error{Code: -32004, Message: "device is not connected"}
		}
		if err != nil {
			return nil, err
		}
		return rpcRawInfo{
			Device: dev.Key, ID: id, GUID: info.GUID, Gamepad: info.Gamepad, Axes: info.Axes, Buttons: info.Buttons,
			Hats: info.Hats, AxisRest: info.AxisRest, Watching: true,
		}, nil
	}
}

// rawStop ends the raw events of the device id, which nobody asks for.
func (r *liveRun) rawStop(id input.DeviceID) {
	if rs, ok := r.src.(rawSource); ok {
		_ = rs.RawStop(id)
	}
}

// singleDevice returns the connected device that selector names, a connection
// ID or a DeviceKey as devices.list gives them. The selector is required.
func (r *liveRun) singleDevice(ctx context.Context, selector string) (supervisor.DeviceView, error) {
	if selector == "" {
		return supervisor.DeviceView{}, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "device is required: a connection ID or a DeviceKey"}
	}
	devices, err := r.selectedDevices(ctx, selector)
	if err != nil {
		return supervisor.DeviceView{}, err
	}
	return devices[0], nil
}

// rpcMappingDevice is the read-back of one device in the result of
// devices.setMapping.
type rpcMappingDevice struct {
	Device  input.DeviceID `json:"device"`
	Gamepad bool           `json:"gamepad"`
	Applied bool           `json:"applied"`
	Got     string         `json:"got,omitempty"`
}

// rpcMappingWarning is something to tell the user after a mapping: code is
// noBack (View is not mapped) or noStart (Menu is not mapped) -- the emergency
// combo and the function keys need them -- shadowed (SDL reads another
// mapping for the controller, such as the one in the user's
// SDL_GAMECONTROLLERCONFIG, which has a higher priority than ours) or
// notGamepad (SDL still reads the controller as a joystick).
type rpcMappingWarning struct {
	Code   string         `json:"code"`
	Device input.DeviceID `json:"device,omitempty"`
}

// rpcMappingResult is the result of devices.setMapping and
// devices.resetMapping.
type rpcMappingResult struct {
	// Device is the DeviceKey of the controller the request named.
	Device string `json:"device"`
	GUID   string `json:"guid"`
	// Mapping is the string that was saved: the request's with the name *, no
	// type, face or hint field, and platform:Windows; "" after a reset.
	Mapping string `json:"mapping"`
	// Keys are the device keys of config.json whose entry was written: all that
	// have the controller's GUID, since SDL applies a mapping to the GUID.
	Keys []string `json:"keys"`
	// Changed is false when config.json already had this mapping for all of them.
	Changed bool `json:"changed"`
	// Applied, Problem and Revision are those of config.put.
	Applied  bool   `json:"applied"`
	Problem  string `json:"problem,omitempty"`
	Revision string `json:"revision,omitempty"`
	// Notes say what was changed in the request's string.
	Notes []string `json:"notes"`
	// Devices are the controllers with this GUID that SDL was asked to read
	// back.
	Devices  []rpcMappingDevice  `json:"devices"`
	Warnings []rpcMappingWarning `json:"warnings"`
}

func (r *liveRun) rpcSetMapping(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		Device  string `json:"device"`
		Mapping string `json:"mapping"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	dev, err := r.singleDevice(ctx, p.Device)
	if err != nil {
		return nil, err
	}
	if dev.Info.GUID == "" {
		return nil, &jrpc2.Error{Code: rpcCodeMappingInvalid, Message: "the controller has no GUID, so SDL cannot be given a mapping for it", Data: reasonData("guid")}
	}
	m, err := sdlmapping.Parse(p.Mapping)
	if err != nil {
		return nil, &jrpc2.Error{Code: rpcCodeMappingInvalid, Message: err.Error(), Data: reasonData("syntax")}
	}
	norm, notes, err := sdlmapping.Normalize(m, dev.Info.GUID)
	if err != nil {
		return nil, &jrpc2.Error{Code: rpcCodeMappingInvalid, Message: err.Error(), Data: reasonData("mapping")}
	}
	return r.changeMapping(ctx, dev, norm, notes)
}

func (r *liveRun) rpcResetMapping(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		Device string `json:"device"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	dev, err := r.singleDevice(ctx, p.Device)
	if err != nil {
		return nil, err
	}
	return r.changeMapping(ctx, dev, sdlmapping.Mapping{}, nil)
}

func reasonData(reason string) json.RawMessage {
	data, _ := json.Marshal(map[string]string{"reason": reason})
	return data
}

// changeMapping saves mapping for dev and applies it, or takes the mapping
// of dev's GUID away for the zero Mapping. It writes config.json (the same
// commit as config.put, so the settings that apply at once do) with the
// mapping on every device key that has the GUID, the keys of the connected
// controllers and those config.json has; the mapping reaches SDL through
// applyMappings.
func (r *liveRun) changeMapping(ctx context.Context, dev supervisor.DeviceView, mapping sdlmapping.Mapping, notes []string) (any, error) {
	if _, ok := r.src.(mappingSource); !ok {
		return nil, &jrpc2.Error{Code: rpcCodeMappingUnsupported, Message: "this input has no gamepad mappings"}
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	text := ""
	if mapping.GUID != "" {
		text = mapping.String()
	}
	r.configApplyMu.Lock()
	defer r.configApplyMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A file with errors is repaired first (PRO-29), as config.get does.
	_, _, _ = r.readConfig(l)
	data, revision, err := l.ConfigText()
	if err != nil {
		return nil, libraryRPCError(err)
	}
	cur, err := profilelib.ValidateConfig(data)
	if err != nil {
		return nil, libraryRPCError(err)
	}
	guid := strings.ToLower(dev.Info.GUID)
	set := map[string]string{}
	add := func(key string) {
		if strings.EqualFold(profile.KeyGUID(key), guid) {
			set[key] = text
		}
	}
	add(dev.Key)
	for key := range cur.Devices {
		add(key)
	}
	if all, err := r.sup.Devices(ctx); err == nil {
		for _, v := range all {
			add(v.Key)
		}
	}
	// What a reset would write where there is no mapping is nothing.
	if text == "" {
		for key := range set {
			if cur.Devices[key].Mapping == "" {
				delete(set, key)
			}
		}
	}
	keys := make([]string, 0, len(set))
	changed := false
	for key, v := range set {
		keys = append(keys, key)
		changed = changed || !sameMapping(cur.Devices[key].Mapping, v)
	}
	slices.Sort(keys)
	res := rpcMappingResult{
		Device: dev.Key, GUID: guid, Mapping: text, Keys: keys, Changed: changed,
		Notes: append([]string{}, notes...), Devices: []rpcMappingDevice{}, Warnings: []rpcMappingWarning{},
	}
	if res.Keys == nil {
		res.Keys = []string{}
	}
	if changed {
		out, err := profile.SetDeviceMappings(data, set)
		if err != nil {
			return nil, &jrpc2.Error{Code: rpcCodeMappingInvalid, Message: err.Error(), Data: reasonData("config")}
		}
		c, err := profilelib.ValidateConfig(out)
		if err != nil {
			return nil, &jrpc2.Error{Code: rpcCodeMappingInvalid, Message: err.Error(), Data: reasonData("config")}
		}
		r.mappingOutcome.Store(nil)
		saved, err := r.commitConfigLocked(l, revision, out, c)
		if err != nil {
			return nil, libraryRPCError(err)
		}
		res.Applied, res.Problem, res.Revision = saved.Applied, saved.Problem, saved.Revision
		if o := r.mappingOutcome.Swap(nil); o != nil {
			for _, d := range o.report.Devices {
				if d.GUID == guid {
					res.Devices = append(res.Devices, rpcMappingDevice{Device: d.Device, Gamepad: d.Gamepad, Applied: d.Applied, Got: d.Got})
					switch {
					case !d.Gamepad:
						res.Warnings = append(res.Warnings, rpcMappingWarning{Code: "notGamepad", Device: d.Device})
					case !d.Applied:
						res.Warnings = append(res.Warnings, rpcMappingWarning{Code: "shadowed", Device: d.Device})
					}
				}
			}
		}
	} else {
		res.Applied, res.Revision = true, profilelib.Revision(data)
	}
	if mapping.GUID != "" {
		// The emergency combo is back+start on most controllers, and View and
		// Menu are function keys (ADR-0069): a mapping without them leaves the
		// controller without a way to pause (ARCHITECTURE section 5).
		if !mapping.Binds("back") {
			res.Warnings = append(res.Warnings, rpcMappingWarning{Code: "noBack"})
		}
		if !mapping.Binds("start") {
			res.Warnings = append(res.Warnings, rpcMappingWarning{Code: "noStart"})
		}
	}
	r.log.Info("gamepad mapping saved", "device", dev.Key, "guid", guid, "keys", len(keys), "reset", mapping.GUID == "", "changed", changed)
	return res, nil
}

// sameMapping reports whether a and b, the mapping strings of config.json
// and of a request, bind the same.
func sameMapping(a, b string) bool {
	if a == b {
		return true
	}
	ma, errA := sdlmapping.Parse(a)
	mb, errB := sdlmapping.Parse(b)
	return errA == nil && errB == nil && sdlmapping.Equivalent(ma, mb)
}
