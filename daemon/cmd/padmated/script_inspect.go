package main

import (
	"errors"
	"slices"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
)

// maxErrorText bounds an error kept for script.status.
const maxErrorText = 16 << 10

// rememberError keeps err for script.status: the errors of macros by file
// (SCR-50), the rest as lastError, which is the legacy script's.
func (r *scriptRuntime) rememberError(err error) {
	if err == nil {
		return
	}
	failing := r.lastError != "" || len(r.macroErrors) > 0
	macros, rest := splitMacroErrors(err)
	for _, me := range macros {
		if r.macroErrors == nil {
			r.macroErrors = map[string]string{}
		}
		r.macroErrors[me.File] = errorText(me)
	}
	if rest != nil {
		r.lastError = errorText(rest)
	}
	// A failure episode lasts until forgetErrors clears the errors (lastError,
	// macroErrors): its first error buzzes once (SCRIPTING 4.2).
	if !failing && r.def.errorCue != nil && r.pad.Connected && r.pad.Info.ID != 0 {
		r.def.errorCue(r.index, r.pad.Info.ID)
	}
}

// forgetErrors ends the failure episode: the script and the macros have none
// of the errors that script.status shows, and the next one buzzes again. A
// reload does it when it is requested, and so does every other way a runtime
// is installed on purpose: a profile reload, a saved script file. The runtime
// that the lifecycle restarts by itself after an interruption is not one, so
// it keeps the errors of the runtime it replaces (ADR-0066).
func (r *scriptRuntime) forgetErrors() {
	r.lastError = ""
	r.macroErrors = nil
}

// errorText is err's message, valid UTF-8 and at most maxErrorText bytes.
func errorText(err error) string {
	text := err.Error()
	if len(text) > maxErrorText {
		text = text[:maxErrorText]
	}
	return strings.ToValidUTF8(text, "�")
}

// splitMacroErrors separates the errors of macros in err, which may join
// several, from the others. An error that wraps a macro's is the macro's.
func splitMacroErrors(err error) (macros []*script.MacroError, rest error) {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		var others []error
		for _, inner := range j.Unwrap() {
			if inner == nil {
				continue
			}
			m, o := splitMacroErrors(inner)
			macros = append(macros, m...)
			if o != nil {
				others = append(others, o)
			}
		}
		return macros, errors.Join(others...)
	}
	if m := script.MacroErrorsOf(err); len(m) > 0 {
		return m, nil
	}
	return nil, err
}

// macroStatus returns the macros of script.status: the latest error of
// each and the disabled ones, sorted by file.
func (r *scriptRuntime) macroStatus() []script.MacroStatus {
	byFile := map[string]*script.MacroStatus{}
	add := func(file string) *script.MacroStatus {
		if s := byFile[file]; s != nil {
			return s
		}
		s := &script.MacroStatus{File: file}
		byFile[file] = s
		return s
	}
	for file, text := range r.macroErrors {
		add(file).Error = text
	}
	for _, file := range r.disabled.Files() {
		add(file).Disabled = true
	}
	if len(byFile) == 0 {
		return nil
	}
	out := make([]script.MacroStatus, 0, len(byFile))
	for _, s := range byFile {
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b script.MacroStatus) int { return strings.Compare(a.File, b.File) })
	return out
}

func (r *scriptRuntime) InspectScript(metadata bool) script.Inspection {
	spec := r.def.spec()
	v := script.Inspection{State: "Absent", Paused: r.paused, Pending: r.pending != nil, WallStopped: r.wallStopped, LastError: r.lastError, Metadata: script.EmptyMetadata()}
	if !spec.needed() {
		return v
	}
	v.State = "Dormant"
	if spec.cfg != nil {
		v.Entry, v.API = spec.cfg.Entry, spec.cfg.API
	}
	v.Macros = r.macroStatus()
	switch {
	case r.wallStopped:
		v.State = "Stopped"
	case r.paused:
		v.State = "Paused"
	case r.lifecycle != nil:
		v.State = string(r.lifecycle.State())
	case r.pending != nil:
		v.State = "Building"
	}
	if r.lifecycle != nil {
		v.RestartCount, v.Permanent = r.lifecycle.RestartStatus()
		if host := r.lifecycle.Current(); host != nil {
			v.Metadata.Runtime = host.RuntimeID()
			if metadata {
				v.Metadata = host.Metadata()
			}
		}
	}
	v.Permanent = v.Permanent || r.wallStopped
	return v
}
