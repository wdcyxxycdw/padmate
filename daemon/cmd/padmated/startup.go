package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"sync"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

// rpcCodeStartupUnavailable is startup.set's error when this padmated has
// no startup entry: a development or test build, or no system support.
// It follows the library and script codes (-32012..-32016).
const rpcCodeStartupUnavailable = -32017

// errDevelopmentBuild is why a development or test build has no startup
// entry (newStartup).
var errDevelopmentBuild = errors.New("development build")

// errStartupUnavailable is what set returns without an entry.
var errStartupUnavailable = errors.New("login startup is unavailable")

// startupState is what startup.get and startup.set return (UI-19,
// ADR-0052). Enabled is any padmate value in Run; Current that it is the
// command this padmated would write. Mode is how that command starts
// padmated: "inject" or "dry-run", this padmated's own mode. Reason says
// why it is not available: "development" or "unsupported".
type startupState struct {
	Available bool   `json:"available"`
	Enabled   bool   `json:"enabled"`
	Current   bool   `json:"current"`
	Mode      string `json:"mode"`
	Reason    string `json:"reason,omitempty"`
}

// startupControl is padmated's login startup entry, shared by the tray's
// "登录时启动" and the startup RPCs. mu serializes them, as Set changes
// the entry. Neither the tray nor a client chooses the executable or the
// arguments: the entry has padmated's own (startupArgs).
type startupControl struct {
	mu     sync.Mutex
	entry  startupSetting // nil when unavailable
	reason string         // why entry is nil
	mode   string
}

// memoryEntry is a test's in-memory startup entry, the only kind a
// development or test build uses: any other would be the user's real
// HKCU Run. Only test files implement it.
type memoryEntry interface {
	startupSetting
	inMemory()
}

// newStartupControl makes the control of opts from create; nil create
// means systemStartup. Only tests pass create, with an entry of their own.
func newStartupControl(opts options, create func(options) (startupSetting, error), log *slog.Logger) *startupControl {
	c := &startupControl{mode: "dry-run"}
	if opts.inject {
		c.mode = "inject"
	}
	if create == nil {
		create = systemStartup
	}
	entry, err := create(opts)
	if err == nil && entry != nil && !usableEntry(entry) {
		// newStartup gives a development build none; this is the second
		// guard, should anything hand it a real one anyway.
		log.Error("startup entry refused in a development build")
		entry, err = nil, errDevelopmentBuild
	}
	switch {
	case err == nil && entry != nil:
		c.entry = entry
	case errors.Is(err, errDevelopmentBuild):
		c.reason = "development"
		log.Info("startup entry off in a development build")
	default:
		c.reason = "unsupported"
		log.Warn("startup entry unavailable", "err", err)
	}
	return c
}

// systemStartup is the user's entry in HKCU Run. A development or test
// build gets none, and touches neither the executable nor the registry.
func systemStartup(opts options) (startupSetting, error) {
	return newStartup(opts, os.Executable, newSystemStartup)
}

// usableEntry reports whether this build may use entry: any in a release
// build, only a test's in-memory one in a development or test build.
func usableEntry(entry startupSetting) bool {
	if !developmentBuild() {
		return true
	}
	_, ok := entry.(memoryEntry)
	return ok
}

// usable is the entry, checked again on every use, not only when made:
// nil when there is none or the build may not use it.
func (c *startupControl) usable() startupSetting {
	if c.entry == nil || !usableEntry(c.entry) {
		return nil
	}
	return c.entry
}

func (c *startupControl) available() bool { return c.usable() != nil }

// development reports whether a development or test build turned the
// entry off.
func (c *startupControl) development() bool { return c.reason == "development" }

// enabled is what the tray's check mark shows.
func (c *startupControl) enabled() (bool, error) {
	entry := c.usable()
	if entry == nil {
		return false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return entry.Enabled()
}

func (c *startupControl) state() (startupState, error) {
	entry := c.usable()
	st := startupState{Available: entry != nil, Mode: c.mode, Reason: c.reason}
	if entry == nil {
		return st, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return readStartup(entry, st)
}

func readStartup(entry startupSetting, st startupState) (startupState, error) {
	var err error
	if st.Enabled, err = entry.Enabled(); err != nil {
		return st, err
	}
	if st.Enabled {
		st.Current, err = entry.Current()
	}
	return st, err
}

// set writes or removes the entry. Enabling writes this padmated's
// command over another padmate's.
func (c *startupControl) set(enabled bool) (startupState, error) {
	entry := c.usable()
	st := startupState{Available: entry != nil, Mode: c.mode, Reason: c.reason}
	if entry == nil {
		return st, errStartupUnavailable
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := entry.Set(enabled); err != nil {
		return st, err
	}
	return readStartup(entry, st)
}

// toggle is the tray's click: on when Run has no padmate value, else off.
func (c *startupControl) toggle() error {
	entry := c.usable()
	if entry == nil {
		return errStartupUnavailable
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	on, err := entry.Enabled()
	if err != nil {
		return err
	}
	return entry.Set(!on)
}

// startupMethods adds startup.get {} and startup.set {enabled}. Neither
// takes an executable or arguments.
func (r *liveRun) startupMethods(methods handler.Map) {
	methods["startup.get"] = func(_ context.Context, req *jrpc2.Request) (any, error) {
		if err := req.UnmarshalParams(jrpc2.StrictFields(new(struct{}))); err != nil {
			return nil, err
		}
		st, err := r.startup.state()
		if err != nil {
			r.log.Warn("cannot read startup entry", "err", err)
			return nil, err
		}
		return st, nil
	}
	methods["startup.set"] = func(_ context.Context, req *jrpc2.Request) (any, error) {
		var p struct {
			Enabled *bool `json:"enabled"`
		}
		if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
			return nil, err
		}
		if p.Enabled == nil {
			return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "enabled is required"}
		}
		st, err := r.startup.set(*p.Enabled)
		if errors.Is(err, errStartupUnavailable) {
			msg := "login startup is unavailable on this system"
			if r.startup.development() {
				msg = "login startup cannot be set in a development build"
			}
			data, _ := json.Marshal(st)
			return nil, &jrpc2.Error{Code: rpcCodeStartupUnavailable, Message: msg, Data: data}
		}
		if err != nil {
			r.log.Error("startup entry not changed", "err", err)
			return nil, err
		}
		r.log.Info("startup entry changed", "enabled", st.Enabled, "mode", st.Mode)
		return st, nil
	}
}
