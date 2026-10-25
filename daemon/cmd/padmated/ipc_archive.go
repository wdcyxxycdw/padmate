package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
	"github.com/wdcyxxycdw/padmate/daemon/internal/version"
)

// Export and import error codes continue after the fixed default's
// (-32018) and the code reserved for script macros (-32019).
const (
	// rpcCodeArchiveInvalid: profiles.import of data that is not an
	// archive it accepts; data is {"reason","path"?,"diagnostics"?} with
	// reason format, version, entry, limit, encoding or profile
	// (profilelib.ArchiveError).
	rpcCodeArchiveInvalid = -32020
	// rpcCodeProfileNameTaken: profiles.import whose name another profile
	// goes by; data is {"name","id"}, id the profile with the name. The
	// client asks the user and repeats the request with replace or name.
	rpcCodeProfileNameTaken = -32021
	// rpcCodeMacroConflict: profiles.import without macros of an archive
	// whose macros the script library has under the same names with other
	// bytes (SCR-51); data is {"conflicts":[{"file","existing",
	// "replaceable"}]}: file the archive's macro, existing the library's,
	// replaceable whether macros "replace" replaces it (the same
	// extension). Nothing was written. The client asks the user and
	// repeats the request with macros "replace" or "keep".
	rpcCodeMacroConflict = -32022
)

// archiveMethods back up one library profile as an archive and add an
// archive to the library (PRO-20). The daemon reads and writes the files;
// the client only moves the archive's bytes, so no method takes a path.
func (r *liveRun) archiveMethods(methods handler.Map) {
	methods["profiles.export"] = r.rpcExportProfile
	methods["profiles.import"] = r.rpcImportProfile
}

type rpcExported struct {
	// Name is the profile's name, for the client's file name.
	Name string `json:"name"`
	// Data is the archive, base64 (standard, padded).
	Data string `json:"data"`
}

type rpcImported struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Macros are the archive's macros, in the script library now.
	Macros []rpcImportedMacro `json:"macros"`
	rpcSaved
}

// rpcImportedMacro is a macro of an imported archive: file its name in the
// archive, name in the library, which the imported profile runs; same
// when the library had it with the same bytes, replaced when the
// archive's replaced the library's.
type rpcImportedMacro struct {
	File     string `json:"file"`
	Name     string `json:"name"`
	Same     bool   `json:"same,omitempty"`
	Replaced bool   `json:"replaced,omitempty"`
}

// archiveError maps the errors of method's import or export of profile
// id. An error the library does not classify, such as a failed write,
// reaches the client as jrpc2's SystemError, whose message can name a
// path in the library; it is logged in full here for the UI's main
// process, which shows the renderer a fixed message instead.
func (r *liveRun) archiveError(method, id string, err error) error {
	mapped := archiveRPCError(err)
	if !errors.As(mapped, new(*jrpc2.Error)) {
		r.log.Warn("profile archive failed", "method", method, "profile", id, "err", err)
	}
	return mapped
}

// archiveRPCError maps the errors of an import or export.
func archiveRPCError(err error) error {
	var invalid *profilelib.ArchiveError
	var taken *profilelib.NameTakenError
	var conflict *profilelib.MacroConflictError
	switch {
	case errors.As(err, &invalid):
		data, _ := json.Marshal(struct {
			Reason      string              `json:"reason"`
			Path        string              `json:"path,omitempty"`
			Diagnostics profile.Diagnostics `json:"diagnostics,omitempty"`
		}{invalid.Reason, invalid.Path, invalid.Diagnostics})
		return &jrpc2.Error{Code: rpcCodeArchiveInvalid, Message: invalid.Error(), Data: data}
	case errors.As(err, &taken):
		data, _ := json.Marshal(map[string]string{"name": taken.Name, "id": taken.ID})
		return &jrpc2.Error{Code: rpcCodeProfileNameTaken, Message: taken.Error(), Data: data}
	case errors.As(err, &conflict):
		type item struct {
			File        string `json:"file"`
			Existing    string `json:"existing"`
			Replaceable bool   `json:"replaceable"`
		}
		items := make([]item, len(conflict.Conflicts))
		for i, c := range conflict.Conflicts {
			items[i] = item{c.File, c.Existing, c.Replaceable}
		}
		data, _ := json.Marshal(map[string][]item{"conflicts": items})
		return &jrpc2.Error{Code: rpcCodeMacroConflict, Message: conflict.Error(), Data: data}
	}
	return libraryManagementError(err)
}

// rpcExportProfile returns profiles/<id> as an archive of at most
// profilelib.MaxArchiveBytes. A missing profile is -32004; a link, a
// special file, a name or text an import would refuse, or a folder beyond
// the limits is -32015 with data.path; a profile.json that does not
// validate InvalidParams with its diagnostics.
func (r *liveRun) rpcExportProfile(_ context.Context, req *jrpc2.Request) (any, error) {
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
	exported, err := l.Export(p.ID, version.String())
	if err != nil {
		return nil, r.archiveError("profiles.export", p.ID, err)
	}
	return rpcExported{Name: exported.Name, Data: base64.StdEncoding.EncodeToString(exported.Data)}, nil
}

// rpcImportProfile adds an archive to the library: a new profile, its ID
// made from the name, or with replace the profile whose folder it
// replaces. Its macros go into the script library (SCR-51); macros
// settles those whose names the library has with other bytes, which are
// otherwise rpcCodeMacroConflict: "replace" writes the archive's over the
// library's, "keep" adds them under numbered names that the imported
// profile runs. A replaced active profile is applied at once, like a
// save. The result and the profile.changed {kind:"saved"} event are
// profiles.put's, with the ID, name and macros.
func (r *liveRun) rpcImportProfile(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		Data    *string `json:"data"`
		Name    string  `json:"name"`
		Replace string  `json:"replace"`
		Macros  string  `json:"macros"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	switch {
	case p.Data == nil:
		return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "data is required"}
	case p.Name != "" && p.Replace != "":
		return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "give name or replace, not both"}
	case p.Replace != "" && !profilelib.ValidID(p.Replace):
		return nil, invalidLibraryID("replace")
	case p.Macros != profilelib.MacrosAsk && p.Macros != profilelib.MacrosReplace && p.Macros != profilelib.MacrosKeep:
		return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: `macros must be "replace" or "keep"`}
	case p.Name != "":
		if err := profilelib.CheckProfileName(p.Name); err != nil {
			return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: err.Error()}
		}
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	if len(*p.Data) > base64.StdEncoding.EncodedLen(profilelib.MaxArchiveBytes) {
		return nil, archiveRPCError(&profilelib.ArchiveError{Reason: profilelib.ArchiveLimitReason, Detail: "the file is larger than 4 MiB"})
	}
	data, err := base64.StdEncoding.DecodeString(*p.Data)
	if err != nil {
		return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "data must be standard base64"}
	}
	archive, err := profilelib.ReadArchive(data)
	if err != nil {
		return nil, archiveRPCError(err)
	}
	r.profileMu.Lock()
	defer r.profileMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	imported, err := l.Import(archive, profilelib.ImportOptions{Name: p.Name, Replace: p.Replace, Macros: p.Macros})
	if err != nil {
		return nil, r.archiveError("profiles.import", p.Replace, err)
	}
	if imported.CleanupErr != nil {
		r.log.Warn("profile imported; some files of the replaced folder remain until the next copy, import or delete", "profile", imported.ID, "err", imported.CleanupErr)
	}
	r.log.Info("profile imported", "profile", imported.ID, "replaced", p.Replace != "")
	macros := make([]rpcImportedMacro, len(imported.Macros))
	for i, m := range imported.Macros {
		macros[i] = rpcImportedMacro{File: m.File, Name: m.Name, Same: m.Same, Replaced: m.Replaced}
		if !m.Same {
			r.log.Info("macro imported into the script library", "profile", imported.ID, "file", m.File, "to", m.Name, "replaced", m.Replaced)
		}
	}
	saved := r.profileSavedLocked(imported.ID, imported.Data, imported.Saved)
	return rpcImported{ID: imported.ID, Name: imported.Name, Macros: macros, rpcSaved: saved}, nil
}
