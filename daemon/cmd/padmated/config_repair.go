package main

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

// rpcConfigRepair is a repair of config.json (PRO-29) the UI has not
// shown yet: status.get's configRepair until config.repairSeen, and the
// data of profile.changed {kind:"configRepaired"} for a repair after the
// start. The UI shows one dialog that names Backup and can open its
// folder.
type rpcConfigRepair struct {
	// Backup is the name of the copy of the file as it was, in the data
	// folder: config.broken-YYYYMMDD-HHMMSS.json.
	Backup string `json:"backup"`
	// Path is Backup's absolute path, for Electron main to select.
	Path string `json:"path"`
	// Reset is true when the file could not be parsed at all and the
	// defaults replaced it; otherwise only its members with errors were
	// reset.
	Reset bool `json:"reset"`
}

// configNotice returns what the UI shows of repair, a repair of the
// config.json in dir: nil when none was written, which leaves the file as
// it was.
func configNotice(dir string, repair *profilelib.ConfigRepair) *rpcConfigRepair {
	if repair == nil || repair.Backup == "" {
		return nil
	}
	return &rpcConfigRepair{Backup: repair.Backup, Path: filepath.Join(dir, repair.Backup), Reset: repair.Reset}
}

// logConfigRepair logs repair, a repair of config.json, if any: what it
// reset, or that it replaced the whole file, and its backup, or why the
// file could not be written.
func logConfigRepair(log *slog.Logger, repair *profilelib.ConfigRepair) {
	if repair == nil {
		return
	}
	if repair.Known {
		// The same file as before, whose repair could not be written.
		log.Debug("config.json still has errors; repaired in memory only, the file is unchanged", "writeErr", repair.WriteErr)
		return
	}
	attrs := []any{"err", repair.Err}
	if repair.Reset {
		attrs = append(attrs, "replaced", true)
	} else {
		attrs = append(attrs, "reset", repair.Members)
	}
	if repair.Backup == "" {
		log.Error("config.json had errors; repaired in memory only, the file is unchanged", append(attrs, "writeErr", repair.WriteErr)...)
		return
	}
	log.Warn("config.json had errors; repaired", append(attrs, "backup", repair.Backup)...)
}

// readConfig reads config.json as padmated does after the start (PRO-29):
// a file with errors is backed up and repaired (ReadRepairedConfig), the
// repair logged, and the UI told: status.get's configRepair and
// profile.changed {kind:"configRepaired"} with the revision of the
// repaired file. The running settings stay as config.put last applied
// them, as for any edit by hand.
func (r *liveRun) readConfig(l *profilelib.Library) (*profile.Config, string, error) {
	c, revision, repair, err := l.ReadRepairedConfig(true)
	logConfigRepair(r.log, repair)
	if n := configNotice(l.Dir(), repair); n != nil {
		r.configNotice.Store(n)
		r.events.Publish("profile.changed", map[string]any{"kind": "configRepaired", "backup": n.Backup, "path": n.Path, "reset": n.Reset, "revision": repair.Revision})
	}
	return c, revision, err
}

// rpcConfigRepairSeen is config.repairSeen {backup}: the UI has shown the
// repair that made backup, so status.get no longer reports it. A later
// repair, with another backup, stays. cleared says whether it went.
func (r *liveRun) rpcConfigRepairSeen(_ context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		Backup *string `json:"backup"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	if p.Backup == nil {
		return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "backup is required"}
	}
	cleared := false
	for {
		n := r.configNotice.Load()
		if n == nil || n.Backup != *p.Backup {
			break
		}
		if r.configNotice.CompareAndSwap(n, nil) {
			cleared = true
			break
		}
	}
	return map[string]bool{"cleared": cleared}, nil
}
