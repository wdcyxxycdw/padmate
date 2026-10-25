package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
	"github.com/wdcyxxycdw/padmate/daemon/internal/watch"
)

// Text is a string rather than RawMessage so malformed files remain readable
// and repairable without corrupting the RPC response. Revision hashes disk
// bytes, including formatting and any BOM.
type rpcDocument struct {
	ID       string `json:"id,omitempty"`
	Text     string `json:"text"`
	Revision string `json:"revision"`
	// Extends is the parent that Text names (PRO-19, ADR-0070), with the
	// chain above it; Text is the profile's own, not the merge (profiles.resolve).
	Extends         *rpcExtends         `json:"extends,omitempty"`
	Diagnostics     profile.Diagnostics `json:"diagnostics"`
	Problem         string              `json:"problem,omitempty"`
	RestartRequired bool                `json:"restartRequired,omitempty"`
}

// rpcExtends says what a profile extends: Parent is the library ID that its
// text names, Chain the IDs from there up as far as the library goes (at most
// profile.MaxExtendsDepth), nearest first, so Chain[0] is Parent when the
// parent is there.
type rpcExtends struct {
	Parent string   `json:"parent"`
	Chain  []string `json:"chain"`
}

// extendsOf describes what profile id extends according to its text, or nil.
func extendsOf(l *profilelib.Library, id string, data []byte) *rpcExtends {
	parent := profile.ExtendsOf(data)
	if parent == "" {
		return nil
	}
	chain := l.Parents(id)
	if chain == nil {
		chain = []string{}
	}
	return &rpcExtends{Parent: parent, Chain: chain}
}

type rpcSaved struct {
	Revision        string `json:"revision"`
	Applied         bool   `json:"applied"`
	Problem         string `json:"problem,omitempty"`
	RestartRequired bool   `json:"restartRequired,omitempty"`
}

func (r *liveRun) profileMethods(methods handler.Map) {
	methods["profiles.list"] = r.rpcListProfiles
	methods["profiles.get"] = r.rpcGetProfile
	methods["profiles.resolve"] = r.rpcResolveProfile
	methods["profiles.put"] = r.rpcPutProfile
	methods["profiles.validate"] = r.rpcValidateProfile
	methods["profiles.activate"] = r.rpcActivateProfile
	methods["profiles.explain"] = r.rpcExplainProfile
	methods["config.get"] = r.rpcGetConfig
	methods["config.put"] = r.rpcPutConfig
	methods["config.repairSeen"] = r.rpcConfigRepairSeen
}

type rpcValidation struct {
	Valid       bool                `json:"valid"`
	Diagnostics profile.Diagnostics `json:"diagnostics"`
}

// rpcValidateProfile checks text like profiles.put does, without saving it.
// With a library, extends resolves through it, as the profile id (optional:
// a draft that has none yet); the diagnostics are those of the text, and
// what is wrong in a parent is described at the extends.
func (r *liveRun) rpcValidateProfile(_ context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		Text string `json:"text"`
		ID   string `json:"id"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	if len(p.Text) > profilelib.MaxDocumentBytes {
		return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "profile text exceeds 4 MiB"}
	}
	if p.ID != "" && !profilelib.ValidID(p.ID) {
		return nil, invalidLibraryID("id")
	}
	var ds profile.Diagnostics
	if l := r.cfg.opts.library; l != nil {
		_, ds = l.Parse(p.ID, []byte(p.Text))
	} else {
		_, ds = profile.Parse([]byte(p.Text))
	}
	ds = ds.Local()
	return rpcValidation{Valid: !ds.HasErrors(), Diagnostics: append(profile.Diagnostics{}, ds...)}, nil
}

func (r *liveRun) library() (*profilelib.Library, error) {
	if r.cfg.opts.library == nil {
		return nil, &jrpc2.Error{Code: -32008, Message: "profile library unavailable; start with --data-dir"}
	}
	return r.cfg.opts.library, nil
}

func libraryRPCError(err error) error {
	if err == nil {
		return nil
	}
	var conflict *profilelib.ConflictError
	if errors.As(err, &conflict) {
		data, _ := json.Marshal(map[string]string{"revision": conflict.Current})
		return &jrpc2.Error{Code: -32009, Message: conflict.Error(), Data: data}
	}
	var fixed *profilelib.FixedError
	if errors.As(err, &fixed) {
		data, _ := json.Marshal(map[string]string{"op": fixed.Op})
		return &jrpc2.Error{Code: rpcCodeProfileFixed, Message: fixed.Error(), Data: data}
	}
	if errors.Is(err, os.ErrNotExist) {
		return &jrpc2.Error{Code: -32004, Message: "library document not found"}
	}
	var invalid *profile.Error
	if errors.As(err, &invalid) {
		data, _ := json.Marshal(invalid.Diagnostics.Local())
		return &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "invalid library document", Data: data}
	}
	return err
}

func (r *liveRun) rpcListProfiles(_ context.Context, req *jrpc2.Request) (any, error) {
	if err := req.UnmarshalParams(jrpc2.StrictFields(new(struct{}))); err != nil {
		return nil, err
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	return l.List()
}

func (r *liveRun) rpcGetProfile(_ context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	data, err := l.Read(p.ID)
	if err != nil {
		return nil, libraryRPCError(err)
	}
	_, ds := l.Parse(p.ID, data)
	result := rpcDocument{ID: p.ID, Text: string(data), Revision: profilelib.Revision(data), Extends: extendsOf(l, p.ID, data), Diagnostics: append(profile.Diagnostics{}, ds.Local()...)}
	if err := ds.Err(); err != nil {
		result.Problem = err.Error()
	}
	return result, nil
}

// rpcResolved is the result of profiles.resolve: the profile whole.
type rpcResolved struct {
	ID string `json:"id"`
	// Text is the profile with its parents merged in, as a document of its own
	// without extends: what flattening writes. For a profile that extends
	// nothing, or whose chain has problems, it is the profile's own text.
	Text string `json:"text"`
	// Revision is that of the profile's own file, as profiles.get has it.
	Revision string      `json:"revision"`
	Extends  *rpcExtends `json:"extends,omitempty"`
	// Inherited lists what the merge took from the parents: the pointer of a
	// member of Text and the ID of the profile that wrote it. A control the
	// profile writes itself is not in it.
	Inherited   []profile.Origin    `json:"inherited"`
	Diagnostics profile.Diagnostics `json:"diagnostics"`
	Problem     string              `json:"problem,omitempty"`
}

// rpcResolveProfile returns profile id with its parents merged in, for a
// client that shows what the profile inherits. The diagnostics are those of
// the profile's own text.
func (r *liveRun) rpcResolveProfile(_ context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	data, err := l.Read(p.ID)
	if err != nil {
		return nil, libraryRPCError(err)
	}
	res, ds := l.Resolve(p.ID, data)
	result := rpcResolved{ID: p.ID, Text: string(data), Revision: profilelib.Revision(data), Extends: extendsOf(l, p.ID, data),
		Inherited: []profile.Origin{}, Diagnostics: append(profile.Diagnostics{}, ds.Local()...)}
	if err := ds.Err(); err != nil {
		result.Problem = err.Error()
	}
	if res != nil && res.Merged != nil {
		result.Text, result.Inherited = string(res.Merged), append(result.Inherited, res.Inherited()...)
	}
	return result, nil
}

func (r *liveRun) rpcPutProfile(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		ID       string  `json:"id"`
		Text     string  `json:"text"`
		Revision *string `json:"revision"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	if p.Revision == nil {
		return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "revision is required; use an empty string to create a document"}
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
	data := []byte(p.Text)
	saved, err := l.Put(p.ID, *p.Revision, data)
	if err != nil {
		return nil, libraryRPCError(err)
	}
	return r.profileSavedLocked(p.ID, data, saved), nil
}

// profileSavedLocked finishes a committed write of library profile id's
// profile.json (profiles.put and profiles.restore): it logs a snapshot the
// library could not keep, applies data when id is the current target, and
// tells subscribers with profile.changed {kind:"saved"}. The caller holds
// profileMu.
func (r *liveRun) profileSavedLocked(id string, data []byte, saved profilelib.Saved) rpcSaved {
	if saved.HistoryErr != nil {
		r.log.Warn("profile saved without keeping the previous version", "profile", id, "err", saved.HistoryErr)
	}
	result := rpcSaved{Revision: saved.Revision}
	target := r.targetLocked()
	switch {
	case target.id == id:
		// A committed write must finish applying even if the client leaves.
		// Shutdown still terminates supervisor barriers before IPC cleanup.
		if err := r.reloadProfileLocked(context.Background(), target, watch.Change{Data: data, Sum: sha256.Sum256(data)}); err != nil {
			result.Problem = err.Error()
		} else {
			result.Applied = true
		}
	case target.dependsOn(id):
		// The running profile extends this one (PRO-19, ADR-0070): it is
		// merged again with the parent as it is now.
		if err := r.reloadDependentLocked(context.Background(), target); err != nil {
			result.Problem = err.Error()
		} else {
			result.Applied = true
		}
	}
	r.refreshScriptProfiles()
	r.events.Publish("profile.changed", map[string]any{"kind": "saved", "id": id, "revision": saved.Revision, "applied": result.Applied})
	return result
}

// configNeedsRestart reports whether c differs from the running config in
// a field captured at startup. Automatic selection, output
// (sendToOwnWindow, eventGapMs), timing, safety and the feedback switches
// apply at once (liveSettings) and never require one.
func (r *liveRun) configNeedsRestart(c *profile.Config) bool {
	effective := r.cfg.opts.settings
	if effective == nil {
		effective = profile.DefaultConfig()
	}
	a, b := startupFields(effective), startupFields(c)
	return !reflect.DeepEqual(&a, &b)
}

func (r *liveRun) rpcGetConfig(_ context.Context, req *jrpc2.Request) (any, error) {
	if err := req.UnmarshalParams(jrpc2.StrictFields(new(struct{}))); err != nil {
		return nil, err
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	// A file with errors is repaired first (PRO-29); one that cannot be
	// read fails below, as before.
	_, _, _ = r.readConfig(l)
	data, revision, err := l.ConfigText()
	if err != nil {
		return nil, libraryRPCError(err)
	}
	_, ds := profile.ParseConfig(data)
	result := rpcDocument{Text: string(data), Revision: revision, Diagnostics: append(profile.Diagnostics{}, ds...)}
	c, err := profilelib.ValidateConfig(data)
	if err != nil {
		result.Problem = err.Error()
	} else {
		result.RestartRequired = r.configNeedsRestart(c)
	}
	return result, nil
}

func (r *liveRun) rpcPutConfig(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		Text     string  `json:"text"`
		Revision *string `json:"revision"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	if p.Revision == nil {
		return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "revision is required"}
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	data := []byte(p.Text)
	c, err := profilelib.ValidateConfig(data)
	if err != nil {
		return nil, libraryRPCError(err)
	}
	// Keep committed config versions in the same order as their live
	// application. Library reads use their own lock and remain available.
	r.configApplyMu.Lock()
	defer r.configApplyMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, err := r.commitConfigLocked(l, *p.Revision, data, c)
	if err != nil {
		return nil, libraryRPCError(err)
	}
	return result, nil
}

// commitConfigLocked writes data, which parses as c, if config.json still
// has revision, then applies the fields that take effect at once: the
// selection settings, then output.sendToOwnWindow and output.eventGapMs,
// then the global timing, the emergency combos and the feedback switches
// (applyLiveSettings). It
// wakes the foreground
// worker, which re-evaluates the current foreground through the same
// activation path as a foreground change, and tells subscribers with
// profile.changed {kind:"config"}. The caller holds configApplyMu, so
// commits apply in the order they were written.
func (r *liveRun) commitConfigLocked(l *profilelib.Library, revision string, data []byte, c *profile.Config) (rpcSaved, error) {
	saved, err := l.PutConfig(revision, data)
	if err != nil {
		return rpcSaved{}, err
	}
	restart := r.configNeedsRestart(c)
	result := rpcSaved{Revision: saved, Applied: !restart, RestartRequired: restart}
	prev := r.selectionConfig()
	r.selection.Store(liveSettings(r.cfg.opts.settings, c))
	if err := errors.Join(r.applyOwnOutput(c.Output.SendToOwnWindow), r.applyEventGap(c.Output.EventGapMs)); err != nil {
		result.Applied = false
		result.Problem = err.Error()
	}
	if err := r.applyLiveSettings(prev, r.selectionConfig()); err != nil {
		result.Applied = false
		result.Problem = strings.TrimPrefix(result.Problem+"; "+err.Error(), "; ")
	}
	r.wakeSelection()
	r.events.Publish("profile.changed", map[string]any{"kind": "config", "revision": saved, "applied": result.Applied, "restartRequired": restart})
	return result, nil
}
