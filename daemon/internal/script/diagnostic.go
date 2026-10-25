package script

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

// Phase identifies the part of a script operation that produced a
// diagnostic. It is kept separately from the user-facing message so callers
// can group setup, handler and teardown failures without parsing strings.
type Phase string

const (
	PhaseCompile  Phase = "compile"
	PhaseSetup    Phase = "setup"
	PhaseHandler  Phase = "handler"
	PhaseActivate Phase = "onActivate"
	PhaseTeardown Phase = "teardown"
)

// Severity is shared with the compiler diagnostics. Runtime failures are
// errors; compile warnings remain warnings.
type Severity = compile.Severity

const (
	SeverityError   = compile.SeverityError
	SeverityWarning = compile.SeverityWarning
)

// Diagnostic is a script problem with an optional source position. Line and
// Column are one-based; Column is a UTF-8 byte column, matching esbuild's
// location convention. File paths use '/' separators.
type Diagnostic struct {
	Phase    Phase
	File     string
	Line     int
	Column   int
	Severity Severity
	Message  string
	Cause    error
}

// String renders the stable terminal/IPC representation. Phase remains a
// structured field on Diagnostic and is intentionally absent from this
// string so all script errors use the same file:line:col format.
func (d Diagnostic) String() string {
	file := filepath.ToSlash(d.File)
	var b strings.Builder
	if file != "" {
		b.WriteString(file)
		if d.Line > 0 {
			fmt.Fprintf(&b, ":%d", d.Line)
			if d.Column > 0 {
				fmt.Fprintf(&b, ":%d", d.Column)
			}
		}
		b.WriteString(": ")
	}
	sev := d.Severity
	if sev == "" {
		sev = SeverityError
	}
	b.WriteString(string(sev))
	b.WriteString(": ")
	b.WriteString(d.Message)
	return b.String()
}

// Diagnostics is a collection of source diagnostics.
type Diagnostics []Diagnostic

// Error is returned by Diagnose. It keeps the original error available to
// errors.Is/errors.As while exposing one or more source diagnostics.
type Error struct {
	Diagnostics Diagnostics
	Cause       error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	parts := make([]string, len(e.Diagnostics))
	for i, d := range e.Diagnostics {
		parts[i] = d.String()
	}
	if len(parts) == 0 && e.Cause != nil {
		return e.Cause.Error()
	}
	// Keep the long-standing setup contract for the synchronous-setup
	// rejection. It is a host-generated error, so there is no source location
	// to add and existing callers rely on the concise text.
	if len(e.Diagnostics) == 1 && e.Diagnostics[0].Phase == PhaseSetup &&
		e.Diagnostics[0].File == "" && e.Diagnostics[0].Line == 0 &&
		strings.HasPrefix(e.Diagnostics[0].Message, "setup must be synchronous") {
		return e.Diagnostics[0].Message
	}
	return strings.Join(parts, "\n")
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// DiagnosticsOf extracts diagnostics from an error. It accepts both a
// diagnostic Error and a plain error, making it convenient for logging paths
// that may receive an output or context error.
func DiagnosticsOf(err error) Diagnostics {
	if err == nil {
		return nil
	}
	var de *Error
	if errors.As(err, &de) {
		return append(Diagnostics(nil), de.Diagnostics...)
	}
	return []Diagnostic{{Severity: SeverityError, Message: plainMessage(err), Cause: err}}
}

// Diagnose converts compiler and goja errors into source diagnostics. It is
// idempotent: an existing diagnostic Error is returned unchanged except for
// filling an empty phase on its entries.
func Diagnose(phase Phase, err error) error {
	if err == nil {
		return nil
	}
	// Each error of a join keeps its own diagnostics.
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		errs := j.Unwrap()
		out := make([]error, 0, len(errs))
		for _, e := range errs {
			out = append(out, Diagnose(phase, e))
		}
		return errors.Join(out...)
	}
	// A macro's error is already diagnosed and keeps its file.
	if me := (*MacroError)(nil); errors.As(err, &me) {
		return err
	}
	var de *Error
	if errors.As(err, &de) {
		if phase == "" {
			return err
		}
		clone := &Error{Cause: de.Cause, Diagnostics: append(Diagnostics(nil), de.Diagnostics...)}
		for i := range clone.Diagnostics {
			if clone.Diagnostics[i].Phase == "" {
				clone.Diagnostics[i].Phase = phase
			}
		}
		return clone
	}
	var be *compile.BuildError
	if errors.As(err, &be) {
		ds := make(Diagnostics, len(be.Diagnostics))
		for i, d := range be.Diagnostics {
			ds[i] = Diagnostic{Phase: phase, File: d.File, Line: d.Line, Column: d.Column, Severity: d.Severity, Message: d.Text, Cause: err}
		}
		return &Error{Diagnostics: ds, Cause: err}
	}
	var ex *goja.Exception
	if errors.As(err, &ex) {
		d := Diagnostic{Phase: phase, Severity: SeverityError, Message: plainMessage(ex), Cause: err}
		// The first frame with a file is where the script threw; a native
		// function (key.tap at a macro's top level) has none.
		for _, frame := range ex.Stack() {
			if pos := frame.Position(); pos.Filename != "" {
				d.File, d.Line, d.Column = sourceFile(pos.Filename), pos.Line, pos.Column
				break
			}
		}
		return &Error{Diagnostics: Diagnostics{d}, Cause: err}
	}
	return &Error{Diagnostics: Diagnostics{{Phase: phase, Severity: SeverityError, Message: plainMessage(err), Cause: err}}, Cause: err}
}

// sourceFile is a file name of a stack frame as a person reads it. esbuild's
// source maps percent-encode the names of the sources, a macro's Chinese
// name and its parentheses included (but not its spaces).
func sourceFile(name string) string {
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}
	return filepath.ToSlash(name)
}

func plainMessage(err error) string {
	var ex *goja.Exception
	if errors.As(err, &ex) && ex.Value() != nil {
		return ex.Value().String()
	}
	return err.Error()
}

// promiseStackPosition finds the first frame of a goja stack with a file:
// "at name (file:line:col(pc))", or "at file:line:col(pc)" without a
// function name. The file runs to the location at the end of the line, so
// it can hold spaces and parentheses; the wrapped form has a second closing
// parenthesis, and its file starts after the name's " (".
var promiseStackPosition = regexp.MustCompile(`(?m)\bat (.+):(\d+):(\d+)\(\d+\)(\)?)[ \t]*\r?$`)

// stackFrameFile is the file of a promiseStackPosition match.
func stackFrameFile(match []string) string {
	file := match[1]
	if match[4] != "" {
		if _, after, ok := strings.Cut(file, " ("); ok {
			file = after
		}
	}
	return sourceFile(file)
}

// diagnoseRejection captures the Error stack while goja is still executing
// under the call budget. Rejection handling may be attached later in the same
// turn, so Host only publishes this diagnostic after the call has settled.
func diagnoseRejection(phase Phase, reason goja.Value) error {
	return diagnoseReason(phase, "unhandled Promise rejection", reason)
}

// diagnoseReason is the diagnostic of a Promise rejected with reason, its
// message prefix's and the reason's text, at the position of the reason's
// stack when it is an Error. A macro's rejected run has no prefix.
func diagnoseReason(phase Phase, prefix string, reason goja.Value) error {
	message := prefix
	if reason != nil && !goja.IsUndefined(reason) {
		if message != "" {
			message += ": "
		}
		message += reason.String()
	}
	if message == "" {
		message = "the Promise was rejected"
	}
	d := Diagnostic{Phase: phase, Severity: SeverityError, Message: message}
	if obj, ok := reason.(*goja.Object); ok {
		if stack := obj.Get("stack"); stack != nil && !goja.IsUndefined(stack) {
			if match := promiseStackPosition.FindStringSubmatch(stack.String()); len(match) == 5 {
				d.File = stackFrameFile(match)
				d.Line, _ = strconv.Atoi(match[2])
				d.Column, _ = strconv.Atoi(match[3])
			}
		}
	}
	cause := errors.New(message)
	d.Cause = cause
	return &Error{Diagnostics: Diagnostics{d}, Cause: cause}
}
