package main

import (
	"context"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

// historyMethods list and restore the earlier versions of a library
// profile's profile.json that profiles.put and profiles.restore keep
// (PRO-17, profilelib.History).
func (r *liveRun) historyMethods(methods handler.Map) {
	methods["profiles.history"] = r.rpcProfileHistory
	methods["profiles.restore"] = r.rpcRestoreProfile
}

type rpcSnapshot struct {
	Version string `json:"version"`
	// SavedAt is RFC 3339 in UTC: when this version was saved.
	SavedAt string `json:"savedAt"`
	Bytes   int64  `json:"bytes"`
	Name    string `json:"name,omitempty"`
}

type rpcHistory struct {
	Snapshots []rpcSnapshot `json:"snapshots"`
}

func invalidSnapshotVersion() error {
	return &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "version must be a snapshot version from profiles.history"}
}

// rpcProfileHistory lists a profile's snapshots, newest first. A missing
// profile folder is -32004; a linked folder or history folder -32015.
func (r *liveRun) rpcProfileHistory(_ context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	if !profilelib.ValidID(p.ID) {
		return nil, invalidLibraryID("id")
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	snapshots, err := l.History(p.ID)
	if err != nil {
		return nil, libraryManagementError(err)
	}
	result := rpcHistory{Snapshots: make([]rpcSnapshot, len(snapshots))}
	for i, s := range snapshots {
		result.Snapshots[i] = rpcSnapshot{Version: s.Version, SavedAt: s.SavedAt.UTC().Format(time.RFC3339), Bytes: s.Bytes, Name: s.Name}
	}
	return result, nil
}

// rpcRestoreProfile saves a snapshot as the profile's profile.json with
// profiles.put's revision check, result and event; the replaced file
// becomes a snapshot itself. A snapshot that fails validation is
// InvalidParams with its diagnostics, like an invalid put.
func (r *liveRun) rpcRestoreProfile(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		ID       string  `json:"id"`
		Version  string  `json:"version"`
		Revision *string `json:"revision"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	if p.Revision == nil {
		return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "revision is required; use an empty string when profile.json is missing"}
	}
	if !profilelib.ValidID(p.ID) {
		return nil, invalidLibraryID("id")
	}
	if !profilelib.ValidVersion(p.Version) {
		return nil, invalidSnapshotVersion()
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	r.profileMu.Lock()
	defer r.profileMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	saved, data, err := l.Restore(p.ID, p.Version, *p.Revision)
	if err != nil {
		return nil, libraryManagementError(err)
	}
	r.log.Info("profile restored", "profile", p.ID, "version", p.Version)
	return r.profileSavedLocked(p.ID, data, saved), nil
}
