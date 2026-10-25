package compile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/evanw/esbuild/pkg/api"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// HostGlobal is the global object the virtual padmate module reads its
// members from. The host defines it before it runs the bundle.
const HostGlobal = "__padmate"

// BundleGlobal is the global variable that receives an entry module's
// exports. The host reads its default export for defineScript setup.
const BundleGlobal = "__padmate_bundle"

// MacroGlobal is the global variable that receives a macro's exports
// (SCR-50, ADR-0055). esbuild declares it with a top-level var, which a
// script cannot delete: each macro the host loads overwrites it, and the
// host reads its default export, the macro's function, after each.
const MacroGlobal = "__padmate_macro"

// MacroAPIGlobal is the global object a macro's padmate module reads its
// bridge from: the bridge of each macro is the member named by the macro's
// file name, which the build writes into the module.
const MacroAPIGlobal = "__padmate_macro_api"

// MacroExports are the members of the padmate module in a macro, in the
// order the module exports them. A macro cannot import the members that
// take over input or outlive a run (ADR-0055 decision 1).
var MacroExports = []string{"host", "key", "sleep", "now", "log", "pad", "session"}

// BuildOptions says what to bundle.
type BuildOptions struct {
	// Root is the script root: the profile directory, or for a Macro the
	// macros folder. Every imported file must be inside it, and
	// diagnostics name files relative to it.
	Root string
	// Entry is the entry file, absolute or relative to Root.
	Entry string
	// API is the script API version used for this build. It is part of a
	// cache key; an empty API is accepted for callers that only need a
	// bundle and uses the host's default.
	API string
	// Macro builds a macro (SCR-50, SCR-51): Root is the macros folder,
	// Entry must be a top-level file of it whose name passes
	// profile.CheckMacroFile, named exactly as it is on disk, and the
	// bundle gets the macro's padmate module and MacroGlobal. Without
	// Macro, an entry in the root's macros folder is refused: each file
	// there is a macro.
	Macro bool
}

// Result is a bundled script.
type Result struct {
	// JS is the IIFE bundle. It ends with an inline source map
	// (//# sourceMappingURL=data:...), which goja reads for stack traces.
	JS []byte
	// Size is len(JS) in bytes, source map included.
	Size int
	// Diagnostics are esbuild's warnings.
	Diagnostics []Diagnostic
}

// Severity is how bad a Diagnostic is.
type Severity string

// The severities Build reports.
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Diagnostic is one esbuild message, located in the script's sources.
type Diagnostic struct {
	// File is relative to the script root with forward slashes, or
	// "padmate:<module>" for a virtual module. It is empty when esbuild
	// gives no location.
	File string
	// Line is 1-based; 0 when unknown.
	Line int
	// Column is 1-based and counts bytes (UTF-8), not UTF-16 units; 0 when
	// unknown.
	Column   int
	Severity Severity
	Text     string
}

// String formats d as file:line:col: severity: text, dropping the parts
// that are unknown.
func (d Diagnostic) String() string {
	var b strings.Builder
	if d.File != "" {
		b.WriteString(d.File)
		if d.Line > 0 {
			fmt.Fprintf(&b, ":%d", d.Line)
			if d.Column > 0 {
				fmt.Fprintf(&b, ":%d", d.Column)
			}
		}
		b.WriteString(": ")
	}
	b.WriteString(string(d.Severity))
	b.WriteString(": ")
	b.WriteString(d.Text)
	return b.String()
}

// BuildError is returned when esbuild reports errors. Diagnostics holds the
// errors first, then any warnings.
type BuildError struct {
	Diagnostics []Diagnostic
}

func (e *BuildError) Error() string {
	lines := make([]string, len(e.Diagnostics))
	for i, d := range e.Diagnostics {
		lines[i] = d.String()
	}
	return strings.Join(lines, "\n")
}

// tlaText replaces esbuild's top-level await errors. Depending on the
// options esbuild says the IIFE format or the target does not support it;
// neither tells a script author what to do.
const tlaText = "top-level await is not allowed in padmate scripts: the bundle runs as a classic script, not a module; move the await into an async function"

// tsconfigRaw keeps the build hermetic: without it esbuild would read any
// tsconfig.json in or above the entry's directory, including ones outside
// the script root. alwaysStrict keeps the strict mode of the ES module
// sources, which the IIFE wrapper would otherwise drop.
const tsconfigRaw = `{"compilerOptions":{"alwaysStrict":true}}`

// Build bundles opts.Entry. It returns a *BuildError when esbuild reports
// errors, and a plain error when the options are unusable.
func Build(opts BuildOptions) (Result, error) {
	root, entry, err := resolvePaths(opts)
	if err != nil {
		return Result{}, err
	}
	r, _, err := build(opts, root, entry)
	return r, err
}

// build bundles the canonical entry in the canonical root and reports
// whether the bundle reads the profile's profile.json, which esbuild can
// import as JSON.
func build(opts BuildOptions, root, entry string) (Result, bool, error) {
	modules := map[string]string{"padmate": padmateStub, "padmate/math": mathModule}
	global := BundleGlobal
	if opts.Macro {
		modules["padmate"] = macroStub(filepath.Base(entry))
		global = MacroGlobal
	}
	r := api.Build(api.BuildOptions{
		EntryPoints:    []string{entry},
		AbsWorkingDir:  root,
		Bundle:         true,
		Write:          false,
		Format:         api.FormatIIFE,
		Target:         api.ESNext,
		Supported:      supported,
		Platform:       api.PlatformNeutral,
		GlobalName:     global,
		Sourcemap:      api.SourceMapInline,
		SourcesContent: api.SourcesContentInclude,
		TsconfigRaw:    tsconfigRaw,
		LogLevel:       api.LogLevelSilent,
		Metafile:       true,
		Plugins:        []api.Plugin{plugin(root, modules)},
	})
	errs, warns := r.Errors, r.Warnings
	if opts.Macro {
		errs, warns = restrictMacro(errs, warns)
	}
	diags := diagnostics(errs, SeverityError)
	warnings := diagnostics(warns, SeverityWarning)
	if len(diags) > 0 {
		return Result{}, false, &BuildError{Diagnostics: append(diags, warnings...)}
	}
	if len(r.OutputFiles) != 1 {
		return Result{}, false, fmt.Errorf("compile: esbuild produced %d output files, want 1", len(r.OutputFiles))
	}
	js := r.OutputFiles[0].Contents
	return Result{JS: js, Size: len(js), Diagnostics: warnings}, readsProfile(r.Metafile), nil
}

// macroStub is the padmate module of the macro file: the members of
// MacroExports from the macro's bridge.
func macroStub(file string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "const bridge = globalThis.%s[%s];\n", MacroAPIGlobal, jsString(file))
	for _, name := range MacroExports {
		fmt.Fprintf(&b, "export const %s = bridge.%s;\n", name, name)
	}
	return b.String()
}

// jsString returns s as a JavaScript string literal.
func jsString(s string) string {
	b, _ := json.Marshal(s) // a string always marshals
	return string(b)
}

// readsProfile reports whether the esbuild metafile lists the profile's
// profile.json among the bundle's inputs.
func readsProfile(metafile string) bool {
	var m struct {
		Inputs map[string]json.RawMessage `json:"inputs"`
	}
	if json.Unmarshal([]byte(metafile), &m) != nil {
		return true // unknown: assume it does
	}
	for name := range m.Inputs {
		if strings.EqualFold(name, profileFile) {
			return true
		}
	}
	return false
}

// macroText is the error for a member of padmate that macros do not have.
func macroText(member string) string {
	return fmt.Sprintf("%q is not available in macros; a macro can use host, key, log, now, pad, session, sleep and padmate/math", member)
}

var (
	// noMatchingExport is esbuild's error for a named import that the
	// module does not export.
	noMatchingExport = regexp.MustCompile(`^No matching export in "padmate:padmate" for import "(.*)"$`)
	// alwaysUndefined is esbuild's warning for a member of a namespace
	// import (import * as p) that the module does not export.
	alwaysUndefined = regexp.MustCompile(`^Import "(.*)" will always be undefined because there is no matching export in "padmate:padmate"$`)
)

// restrictMacro turns esbuild's messages about the members of padmate a
// macro's module lacks into one clear error each, named imports and
// members of a namespace import alike; the warning becomes an error.
func restrictMacro(errs, warns []api.Message) ([]api.Message, []api.Message) {
	out := make([]api.Message, 0, len(errs))
	for _, m := range errs {
		if g := noMatchingExport.FindStringSubmatch(m.Text); g != nil {
			m.Text = macroText(g[1])
		}
		out = append(out, m)
	}
	var kept []api.Message
	for _, m := range warns {
		if g := alwaysUndefined.FindStringSubmatch(m.Text); g != nil {
			m.Text = macroText(g[1])
			out = append(out, m)
			continue
		}
		kept = append(kept, m)
	}
	return out, kept
}

// resolvePaths returns the canonical root and entry (absolute, with every
// link resolved), checking that the entry is a file inside the root.
func resolvePaths(opts BuildOptions) (root, entry string, err error) {
	if opts.Root == "" || opts.Entry == "" {
		return "", "", errors.New("compile: Root and Entry are required")
	}
	root, err = canonical(opts.Root)
	if err != nil {
		return "", "", fmt.Errorf("compile: script root: %w", err)
	}
	entry = opts.Entry
	if !filepath.IsAbs(entry) {
		entry = filepath.Join(root, entry)
	}
	entry, err = canonical(entry)
	if err != nil {
		return "", "", fmt.Errorf("compile: entry: %w", err)
	}
	if fi, err := os.Stat(entry); err != nil {
		return "", "", fmt.Errorf("compile: entry: %w", err)
	} else if fi.IsDir() {
		return "", "", fmt.Errorf("compile: entry %s is a directory", opts.Entry)
	}
	if !inside(root, entry) {
		return "", "", fmt.Errorf("compile: entry %s is outside the script root", opts.Entry)
	}
	if historyPath(root, entry) {
		return "", "", fmt.Errorf("compile: entry %s is in the profile's %s folder of saved versions", opts.Entry, profile.HistoryDir)
	}
	// The checks of the macros folder are on the canonical path, so a link
	// or another case of the name does not get around them.
	if opts.Macro {
		rel, err := filepath.Rel(root, entry)
		name := filepath.ToSlash(rel)
		if err != nil || strings.Contains(name, "/") || name != path.Base(filepath.ToSlash(opts.Entry)) {
			return "", "", fmt.Errorf("compile: macro %s is not a file of the %s folder as named", opts.Entry, profile.MacroDir)
		}
		if err := profile.CheckMacroFile(name); err != nil {
			return "", "", fmt.Errorf("compile: %w", err)
		}
		return root, entry, nil
	}
	if inMacroDir(root, entry) {
		return "", "", fmt.Errorf("compile: entry %s is in the profile's %s folder, where each file is a macro", opts.Entry, profile.MacroDir)
	}
	return root, entry, nil
}

// inMacroDir reports whether the canonical path, inside root, is in the
// profile's macros folder, whose name is compared case-insensitively as
// Windows finds it.
func inMacroDir(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	first, rest, _ := strings.Cut(filepath.ToSlash(rel), "/")
	return strings.EqualFold(first, profile.MacroDir) && rest != ""
}

// inHistory reports whether the existing file path, once every link in it
// is resolved, is in root's profile.HistoryDir: the profile library keeps
// earlier versions of profile.json there (PRO-17). They are never script
// source, so Build refuses an entry or import in it. root must be
// canonical.
func inHistory(root, path string) bool {
	c, err := canonical(path)
	return err == nil && historyPath(root, c)
}

// historyPath is inHistory for a canonical path inside root. The folder
// name is compared case-insensitively, as Windows finds it.
func historyPath(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	first, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	return profile.IsHistoryDir(first)
}

// inRoot reports whether the existing file path is inside root once every
// link in it is resolved. root must be canonical.
func inRoot(root, path string) bool {
	c, err := canonical(path)
	return err == nil && inside(root, c)
}

// inside reports whether path is root or below it, comparing the paths as
// written. Both must be absolute and cleaned; filepath.Rel compares
// case-insensitively on Windows.
func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func diagnostics(msgs []api.Message, sev Severity) []Diagnostic {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]Diagnostic, len(msgs))
	for i, m := range msgs {
		d := Diagnostic{Severity: sev, Text: m.Text}
		if strings.HasPrefix(m.Text, "Top-level await is ") {
			d.Text = tlaText
		}
		if l := m.Location; l != nil {
			d.File = filepath.ToSlash(l.File)
			if l.Namespace != "" && l.Namespace != "file" && !strings.HasPrefix(d.File, l.Namespace+":") {
				d.File = l.Namespace + ":" + d.File
			}
			d.Line = l.Line
			if l.Line > 0 {
				d.Column = l.Column + 1
			}
		}
		out[i] = d
	}
	return out
}
