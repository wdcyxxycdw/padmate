package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"unicode/utf8"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/dtsgen"
)

// Script file error codes continue after library management's
// (ipc_library.go, -32012..-32015). A missing profile is -32004, a linked
// profile folder or a script name that is a link or folder -32015, and an
// invalid id, name or text InvalidParams.
const (
	// rpcCodeScriptExists: script.import without replace onto an existing
	// script; data is {"exists":true,"name":NAME}. The client asks the user
	// and repeats the request with replace.
	rpcCodeScriptExists = -32016
)

// scriptFileMethods write script files into a library profile's folder
// for the UI's new and import script buttons (PRO-14: only the daemon
// writes into the library), and name that folder for opening it. None of
// them edits profile.json: the client sets profile.script through
// profiles.put with its revision.
func (r *liveRun) scriptFileMethods(methods handler.Map) {
	methods["script.init"] = r.rpcInitScript
	methods["script.import"] = r.rpcImportScript
	methods["profiles.folder"] = r.rpcProfileFolder
}

type rpcScriptInit struct {
	Entry    string   `json:"entry"`
	Created  []string `json:"created"`
	Existing []string `json:"existing"`
}

type rpcScriptImport struct {
	Entry    string   `json:"entry"`
	Created  []string `json:"created"`
	Replaced bool     `json:"replaced"`
}

// scriptFileError maps a library error of method for profile id. An error
// the library does not classify, such as a failed write, reaches the client
// as jrpc2's SystemError with its message, which can name the profile
// folder's absolute path. It is logged in full here, because the UI's main
// process shows the renderer a fixed message instead (script-handlers.ts).
func (r *liveRun) scriptFileError(method, id string, err error) error {
	var exists *profilelib.ScriptExistsError
	if errors.As(err, &exists) {
		data, _ := json.Marshal(map[string]any{"exists": true, "name": exists.Name})
		return &jrpc2.Error{Code: rpcCodeScriptExists, Message: exists.Error(), Data: data}
	}
	mapped := libraryManagementError(err)
	if !errors.As(mapped, new(*jrpc2.Error)) {
		r.log.Warn("script files failed", "method", method, "profile", id, "err", err)
	}
	return mapped
}

// scriptText decodes script.import's text, a JSON string of valid UTF-8.
// encoding/json replaces invalid UTF-8 and unpaired surrogate escapes such
// as "\ud800" with U+FFFD without an error, so the raw string is checked
// as well: a client gets InvalidParams rather than a script whose bytes
// differ from the text it sent.
func scriptText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "text is required"}
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil || !utf8.Valid(raw) || hasLoneSurrogate(raw) || len(text) > profilelib.MaxScriptBytes {
		return "", &jrpc2.Error{Code: jrpc2.InvalidParams, Message: fmt.Sprintf("text must be a string of UTF-8 of at most %d KiB", profilelib.MaxScriptBytes>>10)}
	}
	return text, nil
}

// hasLoneSurrogate reports whether raw, a JSON string token that decoded
// without error, has a \u escape of a UTF-16 surrogate that is not half
// of a high-low pair of escapes.
func hasLoneSurrogate(raw []byte) bool {
	high := false // the previous character was a high surrogate escape
	for i := 0; i < len(raw); i++ {
		code := uint64(utf8.MaxRune + 1) // anything but a \u escape
		if raw[i] == '\\' {
			if raw[i+1] == 'u' {
				code, _ = strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
				i += 5
			} else {
				i++ // a two-character escape such as \\ or \"
			}
		}
		if low := code >= 0xdc00 && code <= 0xdfff; low != high {
			return true
		}
		high = code >= 0xd800 && code <= 0xdbff
	}
	return high
}

// rpcInitScript scaffolds a script project into an existing profile
// folder: main.ts with the neutral template, which binds no control, and
// the editor files tsconfig.json and the declarations. It creates only
// missing files and reports the others as existing, so a user's main.ts is
// kept and the client can reference it as the entry.
func (r *liveRun) rpcInitScript(ctx context.Context, req *jrpc2.Request) (any, error) {
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
	files, err := dtsgen.Project(dtsgen.HostAPI, dtsgen.MainEntry, []byte(dtsgen.MainTemplate), nil, nil)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := rpcScriptInit{Entry: dtsgen.MainEntry}
	err = l.UpdateFolder(p.ID, func(folder *os.Root) error {
		var err error
		result.Created, result.Existing, err = dtsgen.ScaffoldFS(folder, files)
		return err
	})
	if err != nil {
		return nil, r.scriptFileError("script.init", p.ID, err)
	}
	r.log.Info("script project created", "profile", p.ID, "created", result.Created, "existing", result.Existing)
	return result, nil
}

// rpcImportScript writes one script file that the client read, such as
// the UI's main process after an open dialog, into an existing profile
// folder, then the editor files that are missing: the tsconfig.json, which
// covers every script in the folder, and the declarations.
func (r *liveRun) rpcImportScript(ctx context.Context, req *jrpc2.Request) (any, error) {
	var p struct {
		ID      string          `json:"id"`
		Name    string          `json:"name"`
		Text    json.RawMessage `json:"text"`
		Replace bool            `json:"replace"`
	}
	if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	if !profilelib.ValidID(p.ID) {
		return nil, invalidLibraryID("id")
	}
	if err := profilelib.CheckScriptName(p.Name); err != nil {
		return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: err.Error()}
	}
	text, err := scriptText(p.Text)
	if err != nil {
		return nil, err
	}
	l, err := r.library()
	if err != nil {
		return nil, err
	}
	helpers, err := dtsgen.Project(dtsgen.HostAPI, p.Name, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := rpcScriptImport{Entry: p.Name, Created: []string{}}
	err = l.UpdateFolder(p.ID, func(folder *os.Root) error {
		replaced, err := profilelib.WriteScript(folder, p.ID, p.Name, []byte(text), p.Replace)
		if err != nil {
			return err
		}
		result.Replaced = replaced
		if !replaced {
			result.Created = append(result.Created, p.Name)
		}
		created, _, err := dtsgen.ScaffoldFS(folder, helpers)
		result.Created = append(result.Created, created...)
		return err
	})
	if err != nil {
		return nil, r.scriptFileError("script.import", p.ID, err)
	}
	r.log.Info("script imported", "profile", p.ID, "name", p.Name, "replaced", result.Replaced, "created", result.Created)
	return result, nil
}

// rpcProfileFolder returns the absolute folder of an existing library
// profile, for the UI's main process to open in Explorer; the renderer
// never receives it.
func (r *liveRun) rpcProfileFolder(_ context.Context, req *jrpc2.Request) (any, error) {
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
	dir, err := l.Folder(p.ID)
	if err != nil {
		return nil, r.scriptFileError("profiles.folder", p.ID, err)
	}
	return map[string]string{"dir": dir}, nil
}
