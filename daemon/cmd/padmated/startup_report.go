package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// exitStartup is padmated's exit code when live mode does not start (UI-45,
// ADR-0056): the arguments are fine, but a file it reads, the instance,
// SDL, the log, IPC or the output stopped it before mapping began. With
// --startup-report FILE, FILE says why.
const exitStartup = 3

// maxReportedProblems is how many errors of a file a report lists.
const maxReportedProblems = 20

// startupReport is what --startup-report FILE gets when padmated does not
// start (UI-45, ADR-0056): one JSON object, for the UI that started
// padmated to show. Every field is always there; "" and 0 mean unknown.
// It never holds the IPC token.
type startupReport struct {
	Version int `json:"version"` // 1
	// Kind is what stopped the start: usage (exit 2), instance, library,
	// config (a config.json that cannot be read; one with errors is
	// repaired, PRO-29), profile, log, ipc, sdl, output or internal (exit
	// 3). state.json never stops the start.
	Kind string `json:"kind"`
	// Cause is how: invalid (File has the errors listed in Errors), missing,
	// denied (no permission), refused (the running instance refused the
	// request), unavailable (it did not answer) or other.
	Cause string `json:"cause"`
	// File is the file at fault, absolute: config.json, the profile, the
	// log, or the data folder itself.
	File string `json:"file"`
	// Line, Col and Pointer locate the first error of File (1-based; Col in
	// characters), as "padmatectl validate" does.
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Pointer string `json:"pointer"`
	// Message is padmated's own words, in English, without File.
	Message string `json:"message"`
	// DataDir is the data folder, absolute; "" without one.
	DataDir string `json:"dataDir"`
	// Errors are the errors of File, first 20, in file order.
	Errors []startupProblem `json:"errors"`
}

// startupProblem is an error of a file a startupReport names.
type startupProblem struct {
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Pointer string `json:"pointer"`
	Message string `json:"message"`
}

// startupError is why live mode did not start: the report's kind, file and
// cause, which an empty cause leaves to newStartupReport, and the error.
// Error is what stderr has always said.
type startupError struct {
	kind, cause, file string
	prefix            string // what Error starts with, such as "config.json"
	err               error
}

func (e *startupError) Error() string {
	if e.prefix == "" {
		return e.err.Error()
	}
	return e.prefix + ": " + e.err.Error()
}

func (e *startupError) Unwrap() error { return e.err }

// forwardFailure is the startupError of a request the running instance
// did not take: it refused it, or it did not answer.
func forwardFailure(err error) error {
	cause := "unavailable"
	if _, ok := errors.AsType[*jrpc2.Error](err); ok {
		cause = "refused"
	}
	return &startupError{kind: "instance", cause: cause, err: err}
}

// profileFailure is the startupError of the startup profile at path: err
// read it, or its diagnostics ds have errors.
func profileFailure(path string, ds profile.Diagnostics, err error) error {
	if ds.HasErrors() {
		err = ds.Err()
	}
	return &startupError{kind: "profile", file: path, err: err}
}

// startupPanic is the startupError of a panic, with value v, that stopped
// the start.
func startupPanic(v any) error {
	return &startupError{kind: "internal", err: fmt.Errorf("panic: %v", v)}
}

// newStartupReport describes err, why padmated with opts did not start.
func newStartupReport(opts options, err error) startupReport {
	rep := startupReport{Version: 1, Kind: "internal", Cause: "other", Message: err.Error(), DataDir: absPath(opts.dataDir), Errors: []startupProblem{}}
	if se, ok := errors.AsType[*startupError](err); ok {
		rep.Kind, rep.File, rep.Message = se.kind, absPath(se.file), se.err.Error()
		if se.cause != "" {
			rep.Cause = se.cause
		}
	}
	if pe, ok := errors.AsType[*profile.Error](err); ok {
		rep.Cause = "invalid"
		for _, d := range pe.Diagnostics {
			if d.Severity == profile.SeverityError && len(rep.Errors) < maxReportedProblems {
				rep.Errors = append(rep.Errors, startupProblem{Line: d.Line, Col: d.Col, Pointer: d.Pointer, Message: d.Message})
			}
		}
		if len(rep.Errors) > 0 {
			first := rep.Errors[0]
			rep.Line, rep.Col, rep.Pointer, rep.Message = first.Line, first.Col, first.Pointer, first.Message
		}
		return rep
	}
	if re, ok := errors.AsType[*jrpc2.Error](err); ok {
		rep.Message = re.Message // without the code
	}
	if pe, ok := errors.AsType[*fs.PathError](err); ok && rep.File != "" && filepath.Base(pe.Path) == filepath.Base(rep.File) {
		rep.Message = pe.Err.Error() // without the path, which is File
	}
	switch {
	case rep.Cause != "other":
	case errors.Is(err, input.ErrNoLibrary), errors.Is(err, fs.ErrNotExist):
		rep.Cause = "missing"
	case errors.Is(err, fs.ErrPermission):
		rep.Cause = "denied"
	}
	return rep
}

// absPath returns path made absolute, or path when that fails.
func absPath(path string) string {
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// startupFailed writes the report of err to opts.startupReport, if given,
// and returns the exit code: 2 for a usage error, otherwise exitStartup. A
// report that cannot be written changes nothing but stderr.
func startupFailed(opts options, stderr io.Writer, err error) int {
	writeStartupReport(opts, stderr, err)
	if se, ok := errors.AsType[*startupError](err); ok && se.kind == "usage" {
		return 2
	}
	return exitStartup
}

// writeStartupReport writes the report of err to opts.startupReport, if
// given, creating its folder as --log-file does.
func writeStartupReport(opts options, stderr io.Writer, err error) {
	if opts.startupReport == "" {
		return
	}
	data, werr := json.Marshal(newStartupReport(opts, err))
	if werr == nil {
		werr = os.MkdirAll(filepath.Dir(opts.startupReport), 0o700)
	}
	if werr == nil {
		werr = os.WriteFile(opts.startupReport, append(data, '\n'), 0o600)
	}
	if werr != nil {
		fmt.Fprintln(stderr, "padmated: writing the startup report:", werr)
	}
}

// clearStartupReport removes opts.startupReport, if given, before padmated
// starts: a report left by an earlier start must not describe this one,
// also when this one hangs or is killed before it writes its own.
func clearStartupReport(opts options, stderr io.Writer) {
	if opts.startupReport == "" {
		return
	}
	if err := os.Remove(opts.startupReport); err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintln(stderr, "padmated: removing the startup report:", err)
	}
}
