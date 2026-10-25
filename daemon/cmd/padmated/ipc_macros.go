package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/dtsgen"
)

// rpcCodeMacroNotFound continues the script file codes (-32016, and
// -32017 and -32018 of other methods): an edit or macros.path names a
// macro that the macros folder does not list under exactly that name, or
// script.path a legacy entry that does not exist. Data is {"file":FILE}.
const rpcCodeMacroNotFound = -32019

// macroMethods manage the script library (SCR-51; SCR-50, ADR-0055,
// design §9): the macros folder of the data folder, which every profile
// runs its macros from. Every write goes through the library's writer
// lock and os.Root. macros.create and macros.import write only the macros
// folder; macros.rename and macros.delete also edit, in place, every
// profile.json that runs the macro, under profileMu, and apply the active
// one, as profiles.put does. script.detach edits one profile's legacy
// script. macros.path and script.path give the UI's main process the file
// to open in an editor. The macros.* methods take no profile; an "id" a
// client from before the library sends is accepted and ignored.
func (r *liveRun) macroMethods(methods handler.Map) {
	methods["macros.list"] = r.rpcListMacros
	methods["macros.create"] = r.rpcCreateMacro
	methods["macros.import"] = r.rpcImportMacro
	methods["macros.rename"] = r.rpcRenameMacro
	methods["macros.delete"] = r.rpcDeleteMacro
	methods["macros.path"] = r.rpcMacroPath
	methods["script.path"] = r.rpcScriptPath
	methods["script.detach"] = r.rpcDetachScript
}

// rpcMacroUse is a control and gesture of a profile that run a macro.
type rpcMacroUse struct {
	// Profile is the profile's ID, ProfileName its name, or the ID when
	// it has none.
	Profile     string `json:"profile"`
	ProfileName string `json:"profileName"`
	Control     string `json:"control"`
	Gesture     string `json:"gesture"`
}

// rpcMacro is a macro of macros.list.
type rpcMacro struct {
	File string `json:"file"`
	// Name is File without its extension, as the UI shows it.
	Name string `json:"name"`
	// Created is when the file was created, in Unix milliseconds; absent
	// when the platform does not say. A rename keeps it, and so does an
	// import that replaces the macro.
	Created int64         `json:"created,omitempty"`
	UsedBy  []rpcMacroUse `json:"usedBy"`
	// Actions is how many actions run the macro, in every profile, layer
	// and common: as many as macros.delete clears, where UsedBy merges
	// them.
	Actions int `json:"actions"`
	// Problem is why the macro does not build or load, as the runtime
	// reports it.
	Problem string `json:"problem,omitempty"`
	// InvalidName is a file whose name CheckMacroFile refuses: it never
	// builds, and can only be renamed or deleted.
	InvalidName bool `json:"invalidName,omitempty"`
}

// rpcMissingMacro is a macro that bindings run but the library lacks.
type rpcMissingMacro struct {
	File    string        `json:"file"`
	UsedBy  []rpcMacroUse `json:"usedBy"`
	Actions int           `json:"actions"`
}

// rpcLegacyScript is a profile's legacy script (script.entry), which runs
// with that profile only.
type rpcLegacyScript struct {
	Profile     string `json:"profile"`
	ProfileName string `json:"profileName"`
	Entry       string `json:"entry"`
}

type rpcMacroList struct {
	Macros  []rpcMacro        `json:"macros"`
	Missing []rpcMissingMacro `json:"missing"`
	Legacy  []rpcLegacyScript `json:"legacy"`
}

// rpcEditedProfile is a profile.json that macros.rename or macros.delete
// wrote: its revision afterwards and how many of its actions changed.
type rpcEditedProfile struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
	Bindings int    `json:"bindings"`
}

// rpcMacroEdit is the result of macros.rename and macros.delete: the
// actions they changed in every profile, and the profiles they wrote.
type rpcMacroEdit struct {
	File     string             `json:"file,omitempty"`
	Bindings int                `json:"bindings"`
	Profiles []rpcEditedProfile `json:"profiles"`
}

// rpcScriptEdit is the result of script.detach: profile.json's revision
// afterwards and how many actions it removed.
type rpcScriptEdit struct {
	Revision string `json:"revision"`
	Bindings int    `json:"bindings"`
}

type rpcMacroFile struct {
	File     string `json:"file"`
	Replaced *bool  `json:"replaced,omitempty"`
}

// macroError maps a library error of method for profile id ("" for the
// library), after the macro errors as scriptFileError does.
func (r *liveRun) macroError(method, id string, err error) error {
	var notFound *profilelib.MacroNotFoundError
	var entry *profilelib.EntryNotFoundError
	var exists *profilelib.MacroExistsError
	var limit *profilelib.MacroLimitError
	var name *profile.MacroNameError
	rpcErr := func(code jrpc2.Code, data any) error {
		raw, _ := json.Marshal(data)
		return &jrpc2.Error{Code: code, Message: err.Error(), Data: raw}
	}
	switch {
	case errors.As(err, &notFound):
		return rpcErr(rpcCodeMacroNotFound, map[string]string{"file": notFound.File})
	case errors.As(err, &entry):
		return rpcErr(rpcCodeMacroNotFound, map[string]string{"file": entry.Entry})
	case errors.As(err, &exists):
		return rpcErr(rpcCodeScriptExists, map[string]any{"exists": true, "name": exists.Name, "replaceable": exists.Replaceable})
	case errors.As(err, &limit):
		return rpcErr(jrpc2.InvalidParams, map[string]int{"limit": profile.MaxMacros})
	case errors.As(err, &name):
		return rpcErr(jrpc2.InvalidParams, map[string]string{"kind": string(name.Kind)})
	case errors.Is(err, profilelib.ErrMacroExtension), errors.Is(err, profilelib.ErrNoScript):
		return &jrpc2.Error{Code: jrpc2.InvalidParams, Message: err.Error()}
	}
	return r.scriptFileError(method, id, err)
}

// profileParams decodes the params of a method of one profile into p,
// whose ID field id points at, and checks the ID.
func profileParams(req *jrpc2.Request, p any, id *string) error {
	if err := req.UnmarshalParams(jrpc2.StrictFields(p)); err != nil {
		return err
	}
	if !profilelib.ValidID(*id) {
		return invalidLibraryID("id")
	}
	return nil
}

// libraryParams decodes the params of a macros.* method into p, which
// embeds ignoredID.
func libraryParams(req *jrpc2.Request, p any) error {
	return req.UnmarshalParams(jrpc2.StrictFields(p))
}

// ignoredID is the profile ID that clients from before the library
// (SCR-51) send with the macros.* methods.
type ignoredID struct {
	ID *string `json:"id,omitempty"`
}

// macroTypes are the editor files of a macros folder (dtsgen.MacroProject):
// tsconfig.json only when missing, the declarations when they differ.
func macroTypes() ([]profilelib.MacroFile, error) {
	files, err := dtsgen.MacroProject(dtsgen.HostAPI)
	if err != nil {
		return nil, err
	}
	out := make([]profilelib.MacroFile, len(files))
	for i, f := range files {
		out[i] = profilelib.MacroFile{Name: f.Name, Data: f.Data, Keep: f.Name == "tsconfig.json"}
	}
	return out, nil
}

// listedProfile is what macros.list reads of a profile.json on disk.
type listedProfile struct {
	id, name string
	refs     map[string][]profile.MacroUse
	counts   map[string]int
	legacy   string
}

// readLibraryProfiles reads every library profile's name, the macros its
// actions run and its legacy entry, from the profile.json on disk, not
// the running one. A profile.json that cannot be read, or is not valid
// JSON, runs no macro and goes by its ID.
func readLibraryProfiles(l *profilelib.Library) ([]listedProfile, error) {
	ids, err := l.IDs()
	if err != nil {
		return nil, err
	}
	out := make([]listedProfile, 0, len(ids))
	for _, id := range ids {
		lp := listedProfile{id: id, name: id}
		data, err := l.Read(id)
		if err != nil {
			continue // not a profile: nothing to list
		}
		lp.refs, _ = profile.MacroRefs(data)
		lp.counts, _ = profile.MacroActionCounts(data)
		var doc struct {
			Name   string `json:"name"`
			Script *struct {
				Entry string `json:"entry"`
			} `json:"script"`
		}
		if json.Unmarshal(profile.TrimBOM(data), &doc) == nil {
			if strings.TrimSpace(doc.Name) != "" {
				lp.name = doc.Name
			}
			if doc.Script != nil {
				lp.legacy = doc.Script.Entry
			}
		}
		out = append(out, lp)
	}
	return out, nil
}

// rpcListMacros lists the script library (design §9, SCR-51): each file
// of its macros folder with the profiles, controls and gestures that run
// it in the profile.json files on disk, and its build or load problem
// (macroChecks), which needs no gamepad; the macros that bindings run but
// the library lacks; and each profile's legacy entry, sorted by profile.
// The macros are sorted by name without the extension, with when each file
// was created (profilelib.MacroEntries).
func (r *liveRun) rpcListMacros(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p ignoredID
	if err := libraryParams(req, &p); err != nil {
		return nil, err
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	entries, err := l.MacroEntries()
	if err != nil {
		return nil, r.macroError("macros.list", "", err)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	profiles, err := readLibraryProfiles(l)
	if err != nil {
		return nil, r.macroError("macros.list", "", err)
	}
	result := rpcMacroList{Macros: []rpcMacro{}, Missing: []rpcMissingMacro{}, Legacy: []rpcLegacyScript{}}
	uses := map[string][]rpcMacroUse{}
	actions := map[string]int{}
	for _, lp := range profiles {
		for file, refs := range lp.refs {
			for _, u := range refs {
				uses[file] = append(uses[file], rpcMacroUse{Profile: lp.id, ProfileName: lp.name, Control: u.Control.String(), Gesture: u.Gesture.String()})
			}
		}
		for file, n := range lp.counts {
			actions[file] += n
		}
		if lp.legacy != "" {
			result.Legacy = append(result.Legacy, rpcLegacyScript{Profile: lp.id, ProfileName: lp.name, Entry: lp.legacy})
		}
	}
	var valid []string
	for _, name := range names {
		if profile.CheckMacroFile(name) == nil {
			valid = append(valid, name)
		}
	}
	problems, err := r.macroChecks.check(ctx, l.Dir(), valid, dtsgen.HostAPI.String())
	if err != nil {
		return nil, err
	}
	usedBy := func(file string) []rpcMacroUse {
		if u := uses[file]; u != nil {
			return u
		}
		return []rpcMacroUse{}
	}
	for _, e := range entries {
		name := e.Name
		m := rpcMacro{File: name, Name: profile.MacroName(name), UsedBy: usedBy(name), Actions: actions[name], Problem: problems[name]}
		if !e.Created.IsZero() {
			m.Created = e.Created.UnixMilli()
		}
		m.InvalidName = profile.CheckMacroFile(name) != nil
		result.Macros = append(result.Macros, m)
	}
	for file := range uses {
		if !slices.Contains(names, file) {
			result.Missing = append(result.Missing, rpcMissingMacro{File: file, UsedBy: usedBy(file), Actions: actions[file]})
		}
	}
	slices.SortFunc(result.Missing, func(a, b rpcMissingMacro) int { return strings.Compare(a.File, b.File) })
	return result, nil
}

// rpcCreateMacro creates <name>.ts with the macro template in the
// library's macros folder, and its editor files. It never replaces a
// macro.
func (r *liveRun) rpcCreateMacro(_ context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		ignoredID
		Name string `json:"name"`
	}
	if err := libraryParams(req, &p); err != nil {
		return nil, err
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	types, err := macroTypes()
	if err != nil {
		return nil, err
	}
	file := p.Name + ".ts"
	if err := l.CreateMacro(file, []byte(dtsgen.MacroTemplate), types); err != nil {
		return nil, r.macroError("macros.create", "", err)
	}
	r.log.Info("macro created", "file", file)
	return rpcMacroFile{File: file}, nil
}

// rpcImportMacro writes a macro file that the client read into the
// library's macros folder, and its editor files. name has the extension.
// A macro with the same name and extension is replaced with replace, under
// its existing exact name; the bindings that run it stay as they are.
func (r *liveRun) rpcImportMacro(_ context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		ignoredID
		Name    string          `json:"name"`
		Text    json.RawMessage `json:"text"`
		Replace bool            `json:"replace"`
	}
	if err := libraryParams(req, &p); err != nil {
		return nil, err
	}
	if err := profile.CheckMacroFile(p.Name); err != nil {
		return nil, r.macroError("macros.import", "", err)
	}
	text, err := scriptText(p.Text)
	if err != nil {
		return nil, err
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	types, err := macroTypes()
	if err != nil {
		return nil, err
	}
	file, replaced, err := l.ImportMacro(p.Name, []byte(text), p.Replace, types)
	if err != nil {
		return nil, r.macroError("macros.import", "", err)
	}
	r.log.Info("macro imported", "file", file, "replaced", replaced)
	return rpcMacroFile{File: file, Replaced: &replaced}, nil
}

// macroEditedLocked finishes an edit of the library that wrote
// profile.json files, as their saves do: each is applied when it is the
// active profile and profile.changed {kind:"saved"} is published for it.
// The caller holds profileMu.
func (r *liveRun) macroEditedLocked(edit profilelib.MacroEdit) rpcMacroEdit {
	result := rpcMacroEdit{File: edit.File, Bindings: edit.Bindings, Profiles: []rpcEditedProfile{}}
	for _, p := range edit.Profiles {
		r.profileSavedLocked(p.ID, p.Data, p.Saved)
		result.Profiles = append(result.Profiles, rpcEditedProfile{ID: p.ID, Revision: p.Saved.Revision, Bindings: p.Bindings})
	}
	return result
}

// rpcRenameMacro renames a macro of the library, file being the exact name
// the folder lists, to to, with the same extension, and every action of
// every profile that runs it.
func (r *liveRun) rpcRenameMacro(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		ignoredID
		File string `json:"file"`
		To   string `json:"to"`
	}
	if err := libraryParams(req, &p); err != nil {
		return nil, err
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
	edit, err := l.RenameMacro(p.File, p.To)
	if err != nil {
		return nil, r.macroError("macros.rename", "", err)
	}
	result := r.macroEditedLocked(edit)
	r.log.Info("macro renamed", "file", p.File, "to", p.To, "bindings", edit.Bindings, "profiles", len(edit.Profiles))
	return result, nil
}

// rpcDeleteMacro removes every action of every profile that runs a macro,
// then its file; a file already gone only loses its references. When the
// file cannot be deleted the references are gone already, and applied.
func (r *liveRun) rpcDeleteMacro(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		ignoredID
		File string `json:"file"`
	}
	if err := libraryParams(req, &p); err != nil {
		return nil, err
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
	edit, err := l.DeleteMacro(p.File)
	result := r.macroEditedLocked(edit)
	if err != nil {
		return nil, r.macroError("macros.delete", "", err)
	}
	r.log.Info("macro deleted", "file", p.File, "bindings", edit.Bindings, "profiles", len(edit.Profiles))
	return result, nil
}

// rpcDetachScript removes a profile's legacy script, its script section
// and every action of type script, from profile.json; its files stay.
func (r *liveRun) rpcDetachScript(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := profileParams(req, &p, &p.ID); err != nil {
		return nil, err
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
	edit, err := l.DetachScript(p.ID)
	if err != nil {
		return nil, r.macroError("script.detach", p.ID, err)
	}
	if edit.Data != nil {
		r.profileSavedLocked(p.ID, edit.Data, edit.Saved)
	}
	r.log.Info("legacy script detached", "profile", p.ID, "bindings", edit.Bindings)
	return rpcScriptEdit{Revision: edit.Saved.Revision, Bindings: edit.Bindings}, nil
}

// rpcMacroPath returns the absolute path of a macro of the library for the
// UI's main process, which opens it in an editor, after writing the editor
// files; the renderer never receives it.
func (r *liveRun) rpcMacroPath(_ context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		ignoredID
		File string `json:"file"`
	}
	if err := libraryParams(req, &p); err != nil {
		return nil, err
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	types, err := macroTypes()
	if err != nil {
		return nil, err
	}
	path, err := l.MacroPath(p.File, types)
	if err != nil {
		return nil, r.macroError("macros.path", "", err)
	}
	return map[string]string{"path": path}, nil
}

// rpcScriptPath returns the absolute path of a profile's legacy entry for
// the UI's main process: a regular file of the profile folder, reached
// without a link, outside .history.
func (r *liveRun) rpcScriptPath(_ context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := profileParams(req, &p, &p.ID); err != nil {
		return nil, err
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	path, err := l.ScriptPath(p.ID)
	if err != nil {
		return nil, r.macroError("script.path", p.ID, err)
	}
	return map[string]string{"path": path}, nil
}
