package main

import (
	"context"
	"slices"
	"strconv"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
	"github.com/wdcyxxycdw/padmate/daemon/internal/version"
)

// TestMain substitutes a process-private pipe and discovery directory and
// rejects attempts to publish the user's real daemon.json.
var ipcPipe, ipcDir string
var listenIPC = ipc.Listen

func (r *liveRun) startIPC() {
	if r.cfg.endpoint == nil {
		return
	}
	ctx, cancel := context.WithCancel(r.end)
	r.ipcStop = cancel
	r.ipcDone = make(chan struct{})
	go func() {
		defer close(r.ipcDone)
		observed := make(chan struct{})
		go func() { defer close(observed); r.observeIPC(ctx) }()
		defer func() { cancel(); <-observed }()
		if err := ipc.Serve(ctx, r.cfg.endpoint, r.events, r.rpcMethods); err != nil {
			r.log.Error("IPC server stopped", "err", err)
			r.endStop(err)
		}
	}()
}

type deviceParams struct {
	Device string `json:"device,omitempty"`
}

type rpcStatus struct {
	LocalDryRun     bool                    `json:"localDryRun"`
	SendToOwnWindow bool                    `json:"sendToOwnWindow"`
	Conflicts       []rpcConflict           `json:"conflicts"`
	Version         string                  `json:"version"`
	Protocol        int                     `json:"protocol"`
	DryRun          bool                    `json:"dryRun"`
	FakeInput       bool                    `json:"fakeInput"`
	Paused          bool                    `json:"paused"`
	Capturing       bool                    `json:"capturing"`
	Suspended       string                  `json:"suspended"`
	ProfileID       string                  `json:"profileId"`
	Slots           []rpcSlot               `json:"slots"`
	Devices         []supervisor.DeviceView `json:"devices"`
	// Combos is the emergency combo of each device, as T-input detects it
	// (UI-19).
	Combos []rpcCombo `json:"combos"`
	// ConfigRepair is the repair of config.json the UI has not shown yet
	// (PRO-29); absent when there is none.
	ConfigRepair *rpcConfigRepair `json:"configRepair,omitempty"`
}

type rpcSlot struct {
	Index   uint8          `json:"slot"`
	Device  input.DeviceID `json:"device"`
	Key     string         `json:"key"`
	Name    string         `json:"name"`
	Profile string         `json:"profile"`
	Gen     uint32         `json:"generation"`
	Layers  rpcLayers      `json:"layers"`
	Latches []string       `json:"latches"`
	Mode    int            `json:"mode"`
	// Hints is the key hints (HUD-03) while a hints action is held: the label
	// of what each control does in the current layer stack. It is absent when
	// the hints are off, and an empty array when they are on with nothing to
	// show.
	Hints *[]rpcHint `json:"hints,omitempty"`
	// Panels are the hud panels the slot's script keeps (SCR-22), in the
	// order first set; absent when there are none.
	Panels []rpcPanel `json:"panels,omitempty"`
}

// rpcPanel is one hud panel of a script: a title, absent when there is
// none, and rows of two texts.
type rpcPanel struct {
	ID    string      `json:"id"`
	Title string      `json:"title,omitempty"`
	Rows  [][2]string `json:"rows"`
}

// rpcHint is one entry of the key hints: the canonical name of a control and
// the text to show for it.
type rpcHint struct {
	Control string `json:"control"`
	Label   string `json:"label"`
}

type rpcLayers struct {
	Base     string   `json:"base"`
	Overlays []string `json:"overlays"`
	Color    string   `json:"color"`
}

func (r *liveRun) rpcMethods(session *ipc.Session) jrpc2.Assigner {
	methods := handler.Map{"status.get": r.rpcGetStatus, "devices.list": r.rpcDevices, "devices.identify": r.rpcIdentify}
	methods["instance.forward"] = r.rpcForwardInstance
	r.profileMethods(methods)
	r.libraryMethods(methods)
	r.appMethods(methods)
	r.scriptMethods(methods)
	r.fakeMethods(methods)
	r.startupMethods(methods)
	owner := r.ipcClientID.Add(1)
	r.uiMethods(methods, session, owner)
	r.latencyMethods(methods)
	r.mappingMethods(methods, session)
	r.exitMethods(methods, session)
	capturing := false // owned by this connection's serialized handlers and cleanup
	session.OnClose(func() {
		if !capturing {
			return
		}
		// Cleanup must reach the router even while it is busy. Supervisor
		// shutdown releases this wait through done, after releasing keys.
		_ = r.sup.Capture(context.Background(), owner, false)
	})
	for _, method := range []string{"engine.pause", "engine.resume", "engine.releaseAll", "capture.start", "capture.stop"} {
		methods[method] = func(ctx context.Context, req *jrpc2.Request) (any, error) {
			if err := req.UnmarshalParams(jrpc2.StrictFields(new(struct{}))); err != nil {
				return nil, err
			}
			var err error
			switch method {
			case "engine.pause":
				err = r.sup.Pause(ctx, true)
			case "engine.resume":
				err = r.sup.Pause(ctx, false)
			case "engine.releaseAll":
				err = r.sup.ReleaseAll()
			case "capture.start":
				capturing = true
				err = r.sup.Capture(ctx, owner, true)
			case "capture.stop":
				err = r.sup.Capture(ctx, owner, false)
				if err == nil {
					capturing = false
				}
			}
			if err != nil {
				return nil, err
			}
			st := r.sup.Status()
			return map[string]bool{"paused": st.Paused, "capturing": st.Capturing}, nil
		}
	}
	methods["engine.setLayer"] = func(ctx context.Context, req *jrpc2.Request) (any, error) {
		var p struct {
			Device string `json:"device,omitempty"`
			Op     string `json:"op,omitempty"`
			Layer  string `json:"layer"`
		}
		if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
			return nil, err
		}
		if p.Op == "" {
			p.Op = "switch"
		}
		if err := r.sup.SetLayer(ctx, p.Device, p.Op, p.Layer); err != nil {
			return nil, err
		}
		return map[string]bool{"changed": true}, nil
	}
	return methods
}

func (r *liveRun) selectedDevices(ctx context.Context, device string) ([]supervisor.DeviceView, error) {
	devices, err := r.sup.Devices(ctx)
	if err != nil {
		return nil, err
	}
	if device == "" {
		return devices, nil
	}
	devices = slices.DeleteFunc(devices, func(v supervisor.DeviceView) bool { return !matchesDevice(device, v.Key, v.Info.ID) })
	if len(devices) == 0 {
		return nil, &jrpc2.Error{Code: -32004, Message: "device is not connected"}
	}
	return devices, nil
}

func matchesDevice(selector, key string, id input.DeviceID) bool {
	return selector == "" || selector == key || selector == strconv.FormatUint(uint64(id), 10)
}

func (r *liveRun) rpcDevices(ctx context.Context, req *jrpc2.Request) (any, error) {
	var params deviceParams
	if err := req.UnmarshalParams(jrpc2.StrictFields(&params)); err != nil {
		return nil, err
	}
	return r.selectedDevices(ctx, params.Device)
}

func (r *liveRun) rpcGetStatus(ctx context.Context, req *jrpc2.Request) (any, error) {
	var params deviceParams
	if err := req.UnmarshalParams(jrpc2.StrictFields(&params)); err != nil {
		return nil, err
	}
	devices, err := r.selectedDevices(ctx, params.Device)
	if err != nil {
		return nil, err
	}
	views, err := r.sup.Inspect(ctx)
	if err != nil {
		return nil, err
	}
	st := r.sup.Status()
	result := rpcStatus{Version: version.String(), Protocol: ipc.Protocol, DryRun: r.cfg.opts.dryRun, Paused: st.Paused, Suspended: st.Suspended.String(), Slots: []rpcSlot{}}
	result.Devices = devices
	infos := make([]input.DeviceInfo, len(devices))
	for i, d := range devices {
		infos[i] = d.Info
	}
	result.Combos = r.combos(infos)
	result.LocalDryRun, result.SendToOwnWindow = r.ownMode.Load(), r.ownSend.Load()
	result.Conflicts = []rpcConflict{}
	if conflicts := r.conflicts.Load(); conflicts != nil {
		result.Conflicts = slices.Clone(*conflicts)
	}
	result.FakeInput = r.cfg.opts.fakeInput == "ipc"
	result.Capturing = st.Capturing
	result.ConfigRepair = r.configNotice.Load()
	if p := r.profileTarget.Load(); p != nil {
		result.ProfileID = p.id
	}
	for _, v := range views {
		if !matchesDevice(params.Device, v.Key, v.Device) {
			continue
		}
		result.Slots = append(result.Slots, rpcSlotView(v))
	}
	return result, nil
}

func rpcSlotView(v slot.View) rpcSlot {
	layers := rpcLayers{Base: v.Mapping.Layers.Base, Overlays: append([]string{}, v.Mapping.Layers.Overlays...), Color: v.Mapping.Layers.Color}
	slot := rpcSlot{Index: v.Index, Device: v.Device, Key: v.Key, Name: v.Info.Name, Profile: v.Mapping.Profile, Gen: v.Gen, Layers: layers, Latches: append([]string{}, v.Mapping.Latches...), Mode: v.Mapping.Mode}
	if v.Mapping.Hints != nil {
		hints := make([]rpcHint, len(v.Mapping.Hints))
		for i, h := range v.Mapping.Hints {
			hints[i] = rpcHint{Control: h.Control.String(), Label: h.Label}
		}
		slot.Hints = &hints
	}
	for _, p := range v.Panels {
		rows := p.Rows
		if rows == nil {
			rows = [][2]string{}
		}
		slot.Panels = append(slot.Panels, rpcPanel{ID: p.ID, Title: p.Title, Rows: rows})
	}
	return slot
}
