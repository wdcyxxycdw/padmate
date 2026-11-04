package main

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/integration"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

// The software integration methods (ADR-0019, ARCHITECTURE section 16.4):
//
//	integrations.list                                   → [{id,name,exe[],found,problem?,dataDir?,installDir?,running,profiles[]}]
//	integrations.functions {integration,language?}      → {revision,categories[],functions[]}
//	integrations.check {integration,function?,keys,language?} → {status,owners[],notes[]}
//	integrations.items {profile,integration,language?}  → {revision,items[],statuses[],consistent,differenceId?,dismissed}
//	integrations.putItems {profile,integration,revision,items} → {revision}
//	integrations.plan {profile,integration,language?}   → {entries[]}
//	integrations.write {integration,changes}            → {backup,revision}
//	integrations.backups {integration}                  → [{id,created,reason,changes}]
//	integrations.restore {integration,backup}           → {id,created,reason,changes} (the backup made before restoring)
//	integrations.dismiss {profile,integration,differenceId} → {dismissed:true}
//
// Nothing here knows a particular piece of software. Which profiles are bound
// to which software is decided by integration.Bound alone. All ten methods are
// in the UI bridge's allowlist.

// Error codes of the integration methods.
const (
	// rpcCodeIntegrationRunning: the software runs; data {integration}.
	rpcCodeIntegrationRunning = -32028
	// rpcCodeIntegrationUnavailable: the integration cannot be used; data
	// {problem}.
	rpcCodeIntegrationUnavailable = -32029
	// rpcCodeIntegrationBusy: the software's files are in use or changed
	// between reading and writing; nothing was written.
	rpcCodeIntegrationBusy = -32030
)

// maxIntegrationChanges is the most changes one integrations.write takes.
const maxIntegrationChanges = 1024

var (
	languagePattern     = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8}){0,3}$`)
	differenceIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func badIntegrationParams(msg string) error {
	return &jrpc2.Error{Code: jrpc2.InvalidParams, Message: msg}
}

// integrationParam finds the integration with the ID in a request.
func (r *liveRun) integrationParam(id string) (integration.Integration, error) {
	if !profile.ValidIntegrationID(id) {
		return nil, badIntegrationParams("integration must be an integration id")
	}
	in, ok := r.integrations().Get(id)
	if !ok {
		return nil, &jrpc2.Error{Code: -32004, Message: "no such integration"}
	}
	return in, nil
}

// languageParam resolves the optional language of a request.
func (r *liveRun) languageParam(language string) (string, error) {
	if language != "" && !languagePattern.MatchString(language) {
		return "", badIntegrationParams("language must be a language tag such as zh-CN or en")
	}
	return r.integLang(language), nil
}

func profileParam(id string) error {
	if !profilelib.ValidID(id) {
		return invalidLibraryID("profile")
	}
	return nil
}

// keysParam checks a KeySpec chord of a request and returns it in canonical
// spelling.
func keysParam(field, spec string) (string, error) {
	canon, err := integration.CanonicalKeys(spec)
	if err != nil {
		return "", badIntegrationParams(field + " must be one key chord: " + err.Error())
	}
	return canon, nil
}

// integrationError maps the errors of an integration to the codes of the
// methods.
func integrationError(id string, err error) error {
	var unavailable *integration.UnavailableError
	var change *integration.ChangeError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, integration.ErrRunning):
		data, _ := json.Marshal(map[string]string{"integration": id})
		return &jrpc2.Error{Code: rpcCodeIntegrationRunning, Message: "the software is running; close it first", Data: data}
	case errors.As(err, &unavailable):
		data, _ := json.Marshal(map[string]string{"problem": unavailable.Problem})
		return &jrpc2.Error{Code: rpcCodeIntegrationUnavailable, Message: unavailable.Error(), Data: data}
	case errors.Is(err, integration.ErrBusy):
		return &jrpc2.Error{Code: rpcCodeIntegrationBusy, Message: "the software's files are in use or changed; nothing was written"}
	case errors.Is(err, integration.ErrNotFound):
		return &jrpc2.Error{Code: -32004, Message: "backup not found"}
	case errors.As(err, &change):
		data, _ := json.Marshal(map[string]string{"function": change.Function, "keys": change.Keys, "reason": change.Reason})
		return &jrpc2.Error{Code: jrpc2.InvalidParams, Message: change.Error(), Data: data}
	}
	return err
}

// rpcIntegration is one entry of integrations.list.
type rpcIntegration struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Exe        []string `json:"exe"`
	Found      bool     `json:"found"`
	Problem    string   `json:"problem,omitempty"`
	DataDir    string   `json:"dataDir,omitempty"`
	InstallDir string   `json:"installDir,omitempty"`
	Running    bool     `json:"running"`
	Profiles   []string `json:"profiles"`
}

// rpcIntegrationBackup is a backup in integrations.backups, integrations.restore
// and integrations.write. Reason is "write" or "beforeRestore"; Changes is the
// number of changed functions of the write (0 for beforeRestore).
type rpcIntegrationBackup struct {
	ID      string `json:"id"`
	Created string `json:"created"`
	Reason  string `json:"reason"`
	Changes int    `json:"changes"`
}

func newRPCBackup(b integration.BackupInfo) rpcIntegrationBackup {
	out := rpcIntegrationBackup{ID: b.ID, Created: b.Created.UTC().Format(time.RFC3339), Reason: "write", Changes: b.Changes}
	if b.Reason == "before-restore" {
		out.Reason, out.Changes = "beforeRestore", 0
	}
	return out
}

type rpcIntegrationItems struct {
	Revision     string                   `json:"revision"`
	Items        []integration.Item       `json:"items"`
	Statuses     []integration.ItemStatus `json:"statuses"`
	Consistent   bool                     `json:"consistent"`
	DifferenceID string                   `json:"differenceId,omitempty"`
	Dismissed    bool                     `json:"dismissed"`
}

func (r *liveRun) integrationMethods(methods handler.Map) {
	methods["integrations.list"] = r.rpcIntegrationList
	methods["integrations.functions"] = r.rpcIntegrationFunctions
	methods["integrations.check"] = r.rpcIntegrationCheck
	methods["integrations.items"] = r.rpcIntegrationItems
	methods["integrations.putItems"] = r.rpcIntegrationPutItems
	methods["integrations.plan"] = r.rpcIntegrationPlan
	methods["integrations.write"] = r.rpcIntegrationWrite
	methods["integrations.backups"] = r.rpcIntegrationBackups
	methods["integrations.restore"] = r.rpcIntegrationRestore
	methods["integrations.dismiss"] = r.rpcIntegrationDismiss
}

func (r *liveRun) rpcIntegrationList(_ context.Context, req *jrpc2.Request) (any, error) {
	if err := req.UnmarshalParams(jrpc2.StrictFields(new(struct{}))); err != nil {
		return nil, err
	}
	rules := r.selectionConfig().Rules
	out := []rpcIntegration{}
	for _, in := range r.integrations().All() {
		loc := in.Locate()
		running, _ := in.Running()
		profiles := integration.BoundProfiles(rules, in.Exes())
		if profiles == nil {
			profiles = []string{}
		}
		out = append(out, rpcIntegration{ID: in.ID(), Name: in.Name(), Exe: slices.Clone(in.Exes()), Found: loc.Found,
			Problem: loc.Problem, DataDir: loc.DataDir, InstallDir: loc.InstallDir, Running: running, Profiles: profiles})
	}
	return out, nil
}

func (r *liveRun) rpcIntegrationFunctions(_ context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		Integration string `json:"integration"`
		Language    string `json:"language"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	in, err := r.integrationParam(p.Integration)
	if err != nil {
		return nil, err
	}
	lang, err := r.languageParam(p.Language)
	if err != nil {
		return nil, err
	}
	l, err := in.Functions(lang)
	if err != nil {
		return nil, integrationError(in.ID(), err)
	}
	if l.Categories == nil {
		l.Categories = []integration.Category{}
	}
	if l.Functions == nil {
		l.Functions = []integration.Function{}
	}
	return l, nil
}

func (r *liveRun) rpcIntegrationCheck(_ context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		Integration string `json:"integration"`
		Function    string `json:"function"`
		Keys        string `json:"keys"`
		Language    string `json:"language"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	in, err := r.integrationParam(p.Integration)
	if err != nil {
		return nil, err
	}
	if len(p.Function) > integration.MaxFunctionLen {
		return nil, badIntegrationParams("function is too long")
	}
	keys, err := keysParam("keys", p.Keys)
	if err != nil {
		return nil, err
	}
	lang, err := r.languageParam(p.Language)
	if err != nil {
		return nil, err
	}
	res, err := in.Check(p.Function, keys, lang)
	if err != nil {
		return nil, integrationError(in.ID(), err)
	}
	if res.Owners == nil {
		res.Owners = []integration.Owner{}
	}
	if res.Notes == nil {
		res.Notes = []string{}
	}
	return res, nil
}

// profileItems reads the effective items of a profile for the integration.
func (r *liveRun) profileItems(profileID, integ string) (own profilelib.IntegrationFile, items []integration.Item, err error) {
	l, err := r.library()
	if err != nil {
		return own, nil, err
	}
	if own, err = l.Integration(profileID, integ); err != nil {
		return own, nil, libraryRPCError(err)
	}
	if items, err = l.EffectiveIntegration(profileID, integ); err != nil {
		return own, nil, libraryRPCError(err)
	}
	if items == nil {
		items = []integration.Item{}
	}
	return own, items, nil
}

// usedItems keeps the items whose keys the bindings of the profile send;
// comparing, planning and the activation check look at these only.
func (r *liveRun) usedItems(profileID string, items []integration.Item) []integration.Item {
	if lib := r.cfg.opts.library; lib != nil {
		return lib.UsedIntegrationItems(profileID, items)
	}
	return items
}

type profileIntegrationParams struct {
	Profile     string `json:"profile"`
	Integration string `json:"integration"`
	Language    string `json:"language"`
}

func (r *liveRun) rpcIntegrationItems(_ context.Context, req *jrpc2.Request) (any, error) {
	var p profileIntegrationParams
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	in, err := r.integrationParam(p.Integration)
	if err != nil {
		return nil, err
	}
	if err := profileParam(p.Profile); err != nil {
		return nil, err
	}
	lang, err := r.languageParam(p.Language)
	if err != nil {
		return nil, err
	}
	own, items, err := r.profileItems(p.Profile, in.ID())
	if err != nil {
		return nil, err
	}
	statuses := []integration.ItemStatus{}
	// All items are returned, but only those whose keys a binding sends are
	// compared (ARCHITECTURE section 16.2).
	if used := r.usedItems(p.Profile, items); len(used) > 0 {
		if statuses, _, err = in.Compare(used, lang); err != nil {
			return nil, integrationError(in.ID(), err)
		}
	}
	out := rpcIntegrationItems{Revision: own.Revision, Items: items, Statuses: statuses, Consistent: integration.Consistent(statuses),
		DifferenceID: integration.DifferenceID(statuses)}
	if m := r.cfg.opts.memory; m != nil && out.DifferenceID != "" {
		out.Dismissed = m.Dismissed(p.Profile, in.ID()) == out.DifferenceID
	}
	return out, nil
}

func (r *liveRun) rpcIntegrationPutItems(_ context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		Profile     string             `json:"profile"`
		Integration string             `json:"integration"`
		Revision    *string            `json:"revision"`
		Items       []integration.Item `json:"items"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	in, err := r.integrationParam(p.Integration)
	if err != nil {
		return nil, err
	}
	if err := profileParam(p.Profile); err != nil {
		return nil, err
	}
	if p.Revision == nil || p.Items == nil {
		return nil, badIntegrationParams("revision and items are required")
	}
	items := slices.Clone(p.Items)
	for i := range items {
		canon, err := keysParam("items[].keys", items[i].Keys)
		if err != nil {
			return nil, err
		}
		items[i].Keys = canon
	}
	// The limits and the one-entry-per-key rule are those of the file.
	if _, err := integration.ParseItems(integration.FormatItems(items)); err != nil {
		return nil, badIntegrationParams("items: " + err.Error())
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	saved, err := l.PutIntegration(p.Profile, in.ID(), items, *p.Revision)
	if err != nil {
		return nil, libraryRPCError(err)
	}
	if saved.HistoryErr != nil {
		r.log.Warn("integration items: earlier version not kept", "profile", p.Profile, "integration", in.ID(), "err", saved.HistoryErr)
	}
	// The active profile's differences may have changed.
	r.integ.recheck.Store(true)
	r.integKick()
	return map[string]string{"revision": saved.Revision}, nil
}

func (r *liveRun) rpcIntegrationPlan(_ context.Context, req *jrpc2.Request) (any, error) {
	var p profileIntegrationParams
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	in, err := r.integrationParam(p.Integration)
	if err != nil {
		return nil, err
	}
	if err := profileParam(p.Profile); err != nil {
		return nil, err
	}
	lang, err := r.languageParam(p.Language)
	if err != nil {
		return nil, err
	}
	_, items, err := r.profileItems(p.Profile, in.ID())
	if err != nil {
		return nil, err
	}
	items = r.usedItems(p.Profile, items)
	if len(items) == 0 {
		return integration.Plan{Entries: []integration.PlanEntry{}}, nil
	}
	plan, err := in.Plan(items, lang)
	if err != nil {
		return nil, integrationError(in.ID(), err)
	}
	if plan.Entries == nil {
		plan.Entries = []integration.PlanEntry{}
	}
	return plan, nil
}

func (r *liveRun) rpcIntegrationWrite(_ context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		Integration string               `json:"integration"`
		Changes     []integration.Change `json:"changes"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	in, err := r.integrationParam(p.Integration)
	if err != nil {
		return nil, err
	}
	if len(p.Changes) > maxIntegrationChanges {
		return nil, badIntegrationParams("too many changes")
	}
	changes := slices.Clone(p.Changes)
	for i, c := range changes {
		if c.Function == "" || len(c.Function) > integration.MaxFunctionLen || c.Add == "" && c.Remove == "" {
			return nil, badIntegrationParams("a change needs a function and add or remove")
		}
		if c.Add != "" {
			if changes[i].Add, err = keysParam("add", c.Add); err != nil {
				return nil, err
			}
		}
		if c.Remove != "" {
			if changes[i].Remove, err = keysParam("remove", c.Remove); err != nil {
				return nil, err
			}
		}
	}
	res, err := in.Write(changes)
	if err != nil {
		return nil, integrationError(in.ID(), err)
	}
	// Nothing changed: no file was written and there is no backup.
	var backup *rpcIntegrationBackup
	if res.Backup.ID != "" {
		b := newRPCBackup(res.Backup)
		backup = &b
	}
	r.log.Info("integration settings written", "integration", in.ID(), "changes", len(changes), "backup", res.Backup.ID)
	r.integKick()
	return struct {
		Backup   *rpcIntegrationBackup `json:"backup"`
		Revision string                `json:"revision"`
	}{backup, res.Revision}, nil
}

func (r *liveRun) rpcIntegrationBackups(_ context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		Integration string `json:"integration"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	in, err := r.integrationParam(p.Integration)
	if err != nil {
		return nil, err
	}
	list, err := in.Backups()
	if err != nil {
		return nil, integrationError(in.ID(), err)
	}
	out := make([]rpcIntegrationBackup, 0, len(list))
	for _, b := range list {
		out = append(out, newRPCBackup(b))
	}
	return out, nil
}

func (r *liveRun) rpcIntegrationRestore(_ context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		Integration string `json:"integration"`
		Backup      string `json:"backup"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	in, err := r.integrationParam(p.Integration)
	if err != nil {
		return nil, err
	}
	if p.Backup == "" || len(p.Backup) > 128 {
		return nil, badIntegrationParams("backup must be a backup id")
	}
	res, err := in.Restore(p.Backup)
	if err != nil {
		return nil, integrationError(in.ID(), err)
	}
	r.log.Info("integration settings restored", "integration", in.ID(), "backup", p.Backup, "before", res.Backup.ID)
	r.integKick()
	return newRPCBackup(res.Backup), nil
}

func (r *liveRun) rpcIntegrationDismiss(_ context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		Profile      string `json:"profile"`
		Integration  string `json:"integration"`
		DifferenceID string `json:"differenceId"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	in, err := r.integrationParam(p.Integration)
	if err != nil {
		return nil, err
	}
	if err := profileParam(p.Profile); err != nil {
		return nil, err
	}
	if !differenceIDPattern.MatchString(p.DifferenceID) {
		return nil, badIntegrationParams("differenceId must be a difference id")
	}
	m := r.cfg.opts.memory
	if m == nil {
		return nil, &jrpc2.Error{Code: -32008, Message: "state.json unavailable; start with --data-dir"}
	}
	m.SetDismissed(p.Profile, in.ID(), p.DifferenceID)
	return map[string]bool{"dismissed": true}, nil
}
