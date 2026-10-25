package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
)

type forwardParams struct {
	Args []string `json:"args"`
}

// Forward one mutation at most. A missing discovery file may mean the first
// instance has acquired its lease but has not published its endpoint yet.
func forwardInstance(ctx context.Context, o options) error {
	args := slices.Clone(o.forwardArgs)
	if len(args) == 0 {
		var err error
		args, err = startupArgs(o)
		if err != nil {
			return err
		}
	}
	if args[len(args)-1] == "--" {
		args = args[:len(args)-1]
	}
	for _, p := range []struct{ name, value string }{{"profile", o.profile}, {"data-dir", o.dataDir}, {"log-file", o.logFile}} {
		if p.value == "" || p.name == "log-file" && p.value == "-" {
			continue
		}
		path, err := filepath.Abs(p.value)
		if err != nil {
			return err
		}
		args = append(args, "--"+p.name+"="+path)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		conn, d, err := ipc.Dial(ctx, ipcDir)
		if err == nil {
			defer conn.Close()
			client, err := ipc.NewClient(ctx, conn, d, "padmated-forward", nil)
			if err != nil {
				return err
			}
			defer client.Close()
			return client.CallResult(ctx, "instance.forward", forwardParams{Args: args}, new(any))
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("running instance IPC unavailable: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (r *liveRun) rpcForwardInstance(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p forwardParams
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	if len(p.Args) == 0 || len(p.Args) > 128 {
		return nil, forwardError("expected 1..128 startup arguments")
	}
	size := 0
	for _, arg := range p.Args {
		size += len(arg)
	}
	if size > 32<<10 {
		return nil, forwardError("startup arguments exceed 32 KiB")
	}
	o, _, ok := parseFlags(p.Args, io.Discard, io.Discard)
	if !ok || o.version || o.fakeInput != "" && o.fakeInput != "ipc" {
		return nil, forwardError("expected live startup arguments")
	}
	if err := r.checkForwardOptions(o); err != nil {
		return nil, err
	}
	r.profileMu.Lock()
	defer r.profileMu.Unlock()
	var err error
	switch {
	case o.profileID != "":
		err = r.activateProfileLocked(ctx, o.profileID, nil)
	case o.profile != "":
		err = r.activateFileLocked(ctx, o.profile)
	default:
		// Launching the same data directory without a target only acknowledges
		// the running instance; stale state.json must not overwrite its choice.
	}
	if err != nil {
		return nil, libraryRPCError(err)
	}
	target := r.targetLocked()
	return map[string]any{"forwarded": true, "profileId": target.id, "profile": target.path, "generation": target.gen}, nil
}

func forwardError(message string) error { return &jrpc2.Error{Code: -32011, Message: message} }

func (r *liveRun) checkForwardOptions(o options) error {
	current := r.cfg.opts
	if o.dryRun != current.dryRun || o.inject != current.inject {
		return forwardError("output mode differs; restart the daemon to change --dry-run/--inject")
	}
	if o.fakeInput != current.fakeInput {
		return forwardError("input mode differs; restart the daemon to change --fake-input")
	}
	if o.dataDir != "" && (!filepath.IsAbs(o.dataDir) || current.library == nil || canonicalProfileKey(o.dataDir) != canonicalProfileKey(current.dataDir)) {
		return forwardError("data directory differs; restart the daemon to change --data-dir")
	}
	if o.profile != "" && (!filepath.IsAbs(o.profile) || current.librarySelection()) {
		return forwardError("a standalone profile needs an absolute path and a standalone running instance; use --profile-id for a library instance")
	}
	if o.profileID != "" && !current.librarySelection() {
		return forwardError("--profile-id requires a running library instance")
	}
	for _, flag := range o.explicitFlags {
		same := true
		switch flag {
		case "device":
			same = o.device == current.device
		case "preset":
			same = o.sdl.Preset == current.sdl.Preset
		case "hint":
			same = slices.Equal(o.sdl.Extra, current.sdl.Extra)
		case "watch":
			same = o.watch == current.watch
		case "tray":
			same = o.tray == current.tray
		case "decisions":
			same = o.decisions == current.decisions
		case "log-level":
			same = o.logLevel == current.logLevel
		case "log-file":
			same = o.logFile == current.logFile || o.logFile != "" && o.logFile != "-" && current.logFile != "" && current.logFile != "-" && canonicalProfileKey(o.logFile) == canonicalProfileKey(current.logFile)
		}
		if !same {
			return forwardError("--" + flag + " differs; restart the daemon to change startup settings")
		}
	}
	return nil
}

func (r *liveRun) activateFileLocked(ctx context.Context, path string) error {
	old := r.targetLocked()
	if old.id == "" && canonicalProfileKey(old.path) == canonicalProfileKey(path) {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 4<<20 {
		return errors.New("profile exceeds 4 MiB")
	}
	return r.installSelectionLocked(ctx, "", path, data, nil)
}
