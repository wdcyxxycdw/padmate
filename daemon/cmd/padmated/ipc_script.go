package main

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

func (r *liveRun) scriptMethods(methods handler.Map) {
	for _, method := range []string{"script.status", "script.registrations", "script.reload"} {
		methods[method] = func(ctx context.Context, req *jrpc2.Request) (any, error) {
			var p deviceParams
			if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
				return nil, err
			}
			return r.sup.Scripts(ctx, p.Device, method == "script.registrations", method == "script.reload")
		}
	}
	methods["script.describe"] = r.rpcDescribeScript
	methods["script.logs"] = r.rpcScriptLogs
	r.scriptFileMethods(methods)
	r.macroMethods(methods)
}

func (r *liveRun) rpcScriptLogs(_ context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		Device string `json:"device,omitempty"`
		After  uint64 `json:"after,omitempty"`
		Limit  int    `json:"limit,omitempty"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	if p.Limit < 0 || p.Limit > 256 {
		return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "limit must be between 1 and 256"}
	}
	if p.Limit == 0 {
		p.Limit = 100
	}
	rows, oldest := r.ipcLogs.Snapshot(p.After)
	entries := []ipc.LogRecord{}
	next := p.After
	for _, row := range rows {
		next = row.Sequence
		var header struct {
			Source string         `json:"source"`
			Device input.DeviceID `json:"device"`
			Key    string         `json:"device_key"`
		}
		if json.Unmarshal(row.Record, &header) != nil || header.Source != "script" || !matchesDevice(p.Device, header.Key, header.Device) {
			continue
		}
		entries = append(entries, row)
		if len(entries) == p.Limit {
			break
		}
	}
	return map[string]any{"entries": entries, "oldest": oldest, "next": next}, nil
}

type rpcScriptDescription struct {
	Entry    string          `json:"entry"`
	API      string          `json:"api"`
	Metadata script.Metadata `json:"metadata"`
}

func (r *liveRun) rpcDescribeScript(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		ID string `json:"id,omitempty"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	r.profileMu.Lock()
	prof, root := r.cfg.profile, filepath.Dir(r.targetLocked().path)
	r.profileMu.Unlock()
	if p.ID != "" {
		l, err := r.library()
		if err != nil {
			return nil, err
		}
		doc, err := l.Load(p.ID)
		if err != nil {
			return nil, libraryRPCError(err)
		}
		prof, root = doc.Profile, filepath.Dir(doc.Path)
	}
	if prof.Script == nil {
		return rpcScriptDescription{Metadata: script.EmptyMetadata()}, nil
	}
	if origin := prof.Script.Origin; origin != "" && r.cfg.opts.library != nil {
		// An inherited script (PRO-19) is in the folder of the parent that has it.
		root = filepath.Join(r.cfg.opts.library.Dir(), "profiles", origin)
	}
	cfg := *prof.Script
	layers := make([]string, 0, len(prof.Layers))
	for _, layer := range prof.Layers {
		layers = append(layers, layer.Name)
	}
	// A separate worker runs only describe mode, without an output sink,
	// session/environment or access to the live engine. UI never runs JS.
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	life := script.NewLifecycle(clock.Real(), script.HostOptions{Mode: "describe", LayerNames: layers})
	result := life.Load(work, func(context.Context) (*goja.Program, error) {
		compiled, err := compile.BuildCached(compile.BuildOptions{Root: root, Entry: cfg.Entry, API: cfg.API})
		if err != nil {
			return nil, err
		}
		return goja.Compile(cfg.Entry, string(compiled.JS), false)
	})
	defer func() { _ = life.Dispose() }()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case loaded := <-result:
		if loaded.Err != nil {
			return nil, loaded.Err
		}
		defer func() { _ = loaded.Host.Discard() }()
		return rpcScriptDescription{Entry: cfg.Entry, API: cfg.API, Metadata: loaded.Host.Metadata()}, nil
	}
}
