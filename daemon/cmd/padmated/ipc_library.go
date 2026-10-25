package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

// Library management error codes extend the daemon's application range
// (-32001 authentication, -32004 not found, -32008 no library, -32009
// revision conflict, -32010 fake input, -32011 forwarded start conflict).
const (
	// rpcCodeProfileActive: profiles.delete of the current target; the
	// client activates another profile first.
	rpcCodeProfileActive = -32012
	// rpcCodeProfileReferenced: config.json, or the running daemon, still
	// selects the profile; data.references lists config pointers such as
	// "rules/3", and "startup" for the profile padmated was started with,
	// which only a restart releases.
	rpcCodeProfileReferenced = -32013
	// rpcCodeProfileExists: profiles.copy onto an existing library ID.
	rpcCodeProfileExists = -32014
	// rpcCodeProfileUnsupported: the folder is a link, contains links or
	// special files, or exceeds the copy limits; data.path names it.
	rpcCodeProfileUnsupported = -32015
	// rpcCodeProfileFixed: profiles.delete of the fixed default profile, or
	// a profiles.put that changes its name (PRO-28, ADR-0053); data.op is
	// "delete" or "rename". It follows the startup code (-32017).
	rpcCodeProfileFixed = -32018
	// rpcCodeProfileChild: profiles.delete of a profile that others extend
	// (PRO-19, ADR-0070), when one of them cannot be written whole first;
	// data is {id, reason, diagnostics?}: the profile that cannot, why
	// ("invalid": it has errors, in diagnostics; "script": it inherits the
	// parent's script).
	rpcCodeProfileChild = -32023
)

func (r *liveRun) libraryMethods(methods handler.Map) {
	methods["profiles.delete"] = r.rpcDeleteProfile
	methods["profiles.copy"] = r.rpcCopyProfile
	r.historyMethods(methods)
	r.archiveMethods(methods)
	r.amgpMethods(methods)
}

func libraryManagementError(err error) error {
	var referenced *profilelib.ReferencedError
	var exists *profilelib.ExistsError
	var unsupported *profilelib.UnsupportedError
	var child *profilelib.ChildError
	switch {
	case errors.As(err, &child):
		data := map[string]any{"id": child.ID, "reason": "invalid"}
		if errors.Is(child.Err, profilelib.ErrInheritedScript) {
			data["reason"] = "script"
		}
		if invalid, ok := errors.AsType[*profile.Error](child.Err); ok {
			data["diagnostics"] = invalid.Diagnostics.Local()
		}
		raw, _ := json.Marshal(data)
		return &jrpc2.Error{Code: rpcCodeProfileChild, Message: child.Error(), Data: raw}
	case errors.As(err, &referenced):
		data, _ := json.Marshal(map[string][]string{"references": referenced.References})
		return &jrpc2.Error{Code: rpcCodeProfileReferenced, Message: referenced.Error(), Data: data}
	case errors.As(err, &exists):
		return &jrpc2.Error{Code: rpcCodeProfileExists, Message: exists.Error()}
	case errors.As(err, &unsupported):
		data, _ := json.Marshal(map[string]string{"path": unsupported.Path})
		return &jrpc2.Error{Code: rpcCodeProfileUnsupported, Message: unsupported.Error(), Data: data}
	}
	return libraryRPCError(err)
}

func invalidLibraryID(field string) error {
	return &jrpc2.Error{Code: jrpc2.InvalidParams, Message: field + " must be a profile library id"}
}

// rpcDeleteProfile permanently deletes an inactive, unreferenced profile
// folder, scripts included; the UI confirms first. The fixed default
// profile is never deleted (rpcCodeProfileFixed). profileMu spans the
// active and running-reference checks and the deletion, so no selection
// can activate the profile in between; the library's writer lock makes the
// revision and config.json checks atomic with taking the folder out of the
// library. A remembered last profile that is gone no longer stops the next
// start (resolveLibrary).
func (r *liveRun) rpcDeleteProfile(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		ID       string  `json:"id"`
		Revision *string `json:"revision"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	if p.Revision == nil {
		return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "revision is required; use an empty string only for a folder without a readable profile.json"}
	}
	if !profilelib.ValidID(p.ID) {
		return nil, invalidLibraryID("id")
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	if p.ID == profilelib.DefaultID {
		return nil, libraryRPCError(&profilelib.FixedError{ID: p.ID, Op: "delete"})
	}
	r.profileMu.Lock()
	defer r.profileMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := l.Path(p.ID)
	if err != nil {
		return nil, invalidLibraryID("id")
	}
	folder := filepath.Dir(path)
	// A profile started by file (--profile inside the library) has no ID;
	// its folder still holds the running profile and its scripts.
	if target := r.targetLocked(); target.id == p.ID || target.path != "" && insideFolder(target.path, folder) {
		return nil, &jrpc2.Error{Code: rpcCodeProfileActive, Message: "profile is active; activate another profile before deleting it"}
	}
	// Remove reads config.json for the profile's references: repair a file
	// with errors first (PRO-29). One that cannot be read stops Remove.
	_, _, _ = r.readConfig(l)
	if err := r.runningReferences(l, p.ID, folder); err != nil {
		return nil, err
	}
	// Startup selects state.json's last profile. Persist the switch away
	// from this profile now rather than at the next periodic flush. Without
	// library selection nothing persists; startup then falls back from the
	// missing profile.
	if m := r.cfg.opts.memory; m != nil && r.cfg.opts.librarySelection() {
		if err := m.Flush(l); err != nil {
			r.log.Warn("state not saved before profile deletion", "err", err)
		}
	}
	// Files that could not be deleted (another program holds one open) no
	// longer belong to the library, so the deletion still succeeded.
	var incomplete *profilelib.IncompleteRemoveError
	flattened, err := l.RemoveFlattening(p.ID, *p.Revision)
	switch {
	case errors.As(err, &incomplete):
		r.log.Warn("profile deleted; some files remain until the next copy or delete", "profile", p.ID, "err", incomplete.Err)
	case err != nil:
		return nil, libraryManagementError(err)
	default:
		r.log.Info("profile deleted", "profile", p.ID)
	}
	// The profiles that extended it were written whole, with their parent's
	// content, before it went (PRO-19, ADR-0070): they are saved like any
	// other write, and the running one is merged again.
	result := rpcDeleted{}
	for _, f := range flattened {
		r.log.Info("profile written whole: its parent is deleted", "profile", f.ID, "parent", p.ID)
		r.profileSavedLocked(f.ID, f.Data, f.Saved)
		result.Flattened = append(result.Flattened, f.ID)
	}
	r.refreshScriptProfiles()
	r.events.Publish("profile.changed", map[string]any{"kind": "deleted", "id": p.ID})
	return result, nil
}

// rpcDeleted is the result of profiles.delete: Flattened lists the profiles
// that extended the deleted one and were written whole first.
type rpcDeleted struct {
	Flattened []string `json:"flattened,omitempty"`
}

// runningReferences refuses to delete a profile that the running daemon
// still selects beyond config.json, which Remove checks: "startup" when
// padmated was started with it (--profile-id, or --profile naming a file
// in its folder), as the login entry may have saved; and the running
// selection settings, which differ from config.json after an edit that
// no config.put applied. Data lists the references like a config.json
// refusal, so clients handle both alike.
func (r *liveRun) runningReferences(l *profilelib.Library, id, folder string) error {
	opts := r.cfg.opts
	var refs []string
	message := ""
	if !opts.autoProfile && opts.profile != "" && insideFolder(opts.profile, folder) {
		refs = append(refs, "startup")
		message = "padmate was started with this profile; restart it with another profile before deleting"
	}
	// When config.json selects the profile too, Remove reports the file's
	// own references instead.
	if running := r.selectionReferences(id); len(running) > 0 {
		if saved, _, err := r.readConfig(l); err != nil || len(profilelib.ConfigReferences(saved, id)) == 0 {
			refs = append(refs, running...)
			if message == "" {
				message = "padmate's running rules still select this profile; save config.json to apply its rules before deleting"
			}
		}
	}
	if len(refs) == 0 {
		return nil
	}
	data, _ := json.Marshal(map[string][]string{"references": refs})
	return &jrpc2.Error{Code: rpcCodeProfileReferenced, Message: message, Data: data}
}

// selectionReferences lists the running selection settings' references to
// id; without library selection they select nothing.
func (r *liveRun) selectionReferences(id string) []string {
	if !r.cfg.opts.librarySelection() {
		return nil
	}
	return profilelib.ConfigReferences(r.selectionConfig(), id)
}

// insideFolder reports whether path names folder or a file below it,
// comparing canonical paths: links resolved, case folded on Windows.
func insideFolder(path, folder string) bool {
	p, f := canonicalProfileKey(path), canonicalProfileKey(folder)
	return p == f || strings.HasPrefix(p, f+string(filepath.Separator))
}

// rpcCopyProfile duplicates a profile folder, scripts included, to a new
// library ID. The copy is never the active profile, so it is not applied;
// clients rename it afterwards through profiles.put.
func (r *liveRun) rpcCopyProfile(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	if !profilelib.ValidID(p.From) {
		return nil, invalidLibraryID("from")
	}
	if !profilelib.ValidID(p.To) {
		return nil, invalidLibraryID("to")
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	revision, err := l.Copy(p.From, p.To)
	if err != nil {
		return nil, libraryManagementError(err)
	}
	r.refreshScriptProfiles()
	r.events.Publish("profile.changed", map[string]any{"kind": "saved", "id": p.To, "revision": revision, "applied": false})
	return map[string]string{"revision": revision}, nil
}
