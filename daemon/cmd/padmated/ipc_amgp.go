package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/amgp"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

// rpcCodeAmgpInvalid: profiles.importAmgp of a file that is not an AntiMicroX
// profile padmate imports; data is {"reason"} with amgp.Error's reason (size,
// encoding, xml, limit, doctype, root, joystick, version) and the message says
// why in words for the user. Nothing was written.
const rpcCodeAmgpInvalid = -32024

// amgpMethods adds the import of AntiMicroX profiles (PRO-21, ADR-0072).
func (r *liveRun) amgpMethods(methods handler.Map) {
	methods["profiles.importAmgp"] = r.rpcImportAmgp
}

type rpcAmgpImported struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Report is what the import dropped or approximated; never null.
	Report []amgp.ReportLine `json:"report"`
	rpcSaved
}

// rpcImportAmgp makes a new library profile from an AntiMicroX file
// (.amgp, or the .xml of older versions). The file comes as data, the bytes in
// standard base64, as the command line and the UI's main process pass a file
// they have read: no method of the daemon takes a path to open (PRO-20), so
// that a client of the bridge cannot make the daemon read a file or reach a
// network share. The file is untrusted: amgp.Import reads at most amgp.MaxBytes
// of it, with limits on nesting and size, and no DOCTYPE; it executes and
// opens nothing.
//
// The name is name when given, else the file's profilename, else fileName
// without its extension. A given name that another profile goes by is
// rpcCodeProfileNameTaken like profiles.import; a name taken from the file is
// numbered ("name 2") instead, so that importing the same file twice works. The
// result and the profile.changed {kind:"saved"} event are profiles.put's, with
// the ID, the name and the report.
func (r *liveRun) rpcImportAmgp(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		Data     *string `json:"data"`
		FileName string  `json:"fileName"`
		Name     string  `json:"name"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	switch {
	case p.Data == nil:
		return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "give data"}
	case p.Name != "":
		if err := profilelib.CheckProfileName(p.Name); err != nil {
			return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: err.Error()}
		}
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	if len(*p.Data) > base64.StdEncoding.EncodedLen(amgp.MaxBytes) {
		return nil, amgpRPCError(&amgp.Error{Reason: amgp.ReasonSize, Message: fmt.Sprintf("文件超过 %d MiB", amgp.MaxBytes>>20)})
	}
	file, err := base64.StdEncoding.DecodeString(*p.Data)
	if err != nil {
		return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "data must be standard base64"}
	}
	out, report, err := amgp.Import(file, amgp.Options{Name: p.Name, FileName: p.FileName})
	if err != nil {
		var refused *amgp.Error
		if errors.As(err, &refused) && refused.Reason != amgp.ReasonInternal {
			return nil, amgpRPCError(refused)
		}
		r.log.Error("AntiMicroX import made an invalid profile", "err", err)
		return nil, err
	}
	r.profileMu.Lock()
	defer r.profileMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	imported, err := l.ImportProfile(out, "", p.Name == "")
	if err != nil {
		return nil, r.archiveError("profiles.importAmgp", "", err)
	}
	dropped, approximate, info := amgp.Counts(report)
	r.log.Info("AntiMicroX profile imported", "profile", imported.ID, "dropped", dropped, "approximate", approximate, "info", info)
	saved := r.profileSavedLocked(imported.ID, imported.Data, imported.Saved)
	return rpcAmgpImported{ID: imported.ID, Name: imported.Name, Report: report, rpcSaved: saved}, nil
}

// amgpRPCError is the error for a file that is refused.
func amgpRPCError(e *amgp.Error) error {
	data, _ := json.Marshal(map[string]string{"reason": e.Reason})
	return &jrpc2.Error{Code: rpcCodeAmgpInvalid, Message: e.Message, Data: data}
}
