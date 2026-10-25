package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

const validateUsage = `usage: padmatectl validate [--json | --merged] <profile.json>...

Checks each profile file: its JSON syntax, the profile schema and the
bindings. A macro that an action runs and the macros folder lacks is a
warning: the data folder's script library for a profile of a library
(profiles/<id>/profile.json), otherwise the macros folder next to the
file. A profile of a library may extend another (PRO-19): the file is
checked merged with its parents, which are read from the same library, and
a problem in a parent's file is printed with that file's path and
position; a parent that is missing or has errors is an error of the
profile that says which. Bindings are those of the merge. Prints one line per problem, in file order, as

  path:line:col: severity: message (JSON pointer)

with columns counted in characters, or "path: ok" for a file without
problems. After a file without errors come its bindings, one per line,
with the latency the engine adds to the gesture (PRO-04), as

  /layers/base/south "Undo": press +0 ms
  /layers/base/start "Later": no action

where the label is left out when the binding has none. Exits 0 if no
file has errors (warnings are allowed), 1 if a file has errors and 2 if
a file cannot be read.

flags:
  --json    print one JSON object per file, with "file", "ok" (no errors),
            "diagnostics" (a problem in a parent's file has "profile", the
            parent's ID, and the position in that file) and, for a file
            without errors, "bindings": each with "id", "control", "label"
            (when set), "gesture" (when the binding has an action), "haptic"
            (when the binding sets one and has an action to play it with)
            and "addedLatencyMs"
  --merged  print each file whole to stdout, the profile with its parents
            merged in and no "extends" (the file itself when it extends
            nothing), instead of the bindings; the problems go to stderr.
            A profile written to a file of another library, or by itself,
            is read like any profile; an "extends" there is an error.
`

// validateJSONObject is one file in "validate --json".
type validateJSONObject struct {
	File        string                `json:"file"`
	OK          bool                  `json:"ok"`
	Diagnostics profile.Diagnostics   `json:"diagnostics"`
	Bindings    []validateJSONBinding `json:"bindings,omitzero"` // nil for a file with errors
}

// validateJSONBinding is one binding in "validate --json".
type validateJSONBinding struct {
	ID             string  `json:"id"`
	Control        string  `json:"control"`
	Label          string  `json:"label,omitempty"`
	Gesture        string  `json:"gesture,omitempty"` // "" for a binding without an action
	Haptic         string  `json:"haptic,omitempty"`  // the preset name (MAP-25), "" for none
	AddedLatencyMs float64 `json:"addedLatencyMs"`
}

// runValidate implements "padmatectl validate".
func runValidate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print one JSON object per file")
	merged := fs.Bool("merged", false, "print each profile with its parents merged in")
	if code, ok := parseFlags(fs, args, validateUsage, stdout, stderr); !ok {
		return code
	}
	if *asJSON && *merged {
		fmt.Fprintln(stderr, "padmatectl validate: --json and --merged cannot be used together")
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "padmatectl validate: no profile files given")
		fmt.Fprint(stderr, validateUsage)
		return 2
	}
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	status := 0
	for _, path := range fs.Args() {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(stderr, "padmatectl validate:", profile.ReadError(path, err))
			status = 2
			continue
		}
		lib, id, inLibrary := libraryOf(path)
		var res *profile.Resolved
		var p *profile.Profile
		var ds profile.Diagnostics
		if inLibrary {
			res, ds = profile.Resolve(data, &profile.Library{ID: id, Load: lib.Read})
		} else {
			res, ds = profile.Resolve(data, nil)
		}
		if res != nil {
			p = res.Profile
		}
		// The macros are those of the merged profile: the file's text, or the
		// merge of the chain, whose problems are located in the files they come from.
		text := data
		if res != nil && res.Merged != nil {
			text = res.Merged
		}
		if missing := missingMacros(path, text); len(missing) > 0 {
			if res != nil {
				missing = res.Locate(missing)
			}
			ds = append(ds, missing...)
			sortDiagnostics(ds)
		}
		var bs []engine.BindingInfo
		if !ds.HasErrors() {
			var cds profile.Diagnostics
			bs, cds = compileBindings(p)
			ds = append(ds, cds...)
		}
		if ds.HasErrors() && status == 0 {
			status = 1
		}
		if *merged {
			for _, d := range ds {
				fmt.Fprintf(stderr, "%s:%s\n", diagnosticFile(path, lib, d), d)
			}
			if !ds.HasErrors() {
				whole := data
				if res.Merged != nil {
					whole = res.Merged
				}
				if _, err := stdout.Write(whole); err != nil {
					fmt.Fprintln(stderr, "padmatectl validate:", err)
					return 2
				}
			}
			continue
		}
		if *asJSON {
			if ds == nil {
				ds = profile.Diagnostics{} // "diagnostics": [], not null
			}
			obj := validateJSONObject{File: path, OK: !ds.HasErrors(), Diagnostics: ds}
			if !ds.HasErrors() {
				obj.Bindings = make([]validateJSONBinding, 0, len(bs)) // [] when there are none
			}
			for _, b := range bs {
				jb := validateJSONBinding{
					ID:             b.Binding.ID,
					Control:        b.Binding.Control.String(),
					Label:          b.Binding.Label,
					AddedLatencyMs: float64(b.AddedLatency) / float64(time.Millisecond),
				}
				if b.Gesture != engine.GestureNone {
					jb.Gesture = b.Gesture.String()
				} else if b.Binding.Block {
					jb.Gesture = "block"
				}
				if b.Binding.Haptic != 0 && b.Gesture != engine.GestureNone {
					jb.Haptic = b.Binding.Haptic.String() // what plays, not what the file says
				}
				obj.Bindings = append(obj.Bindings, jb)
			}
			if err := enc.Encode(obj); err != nil {
				fmt.Fprintln(stderr, "padmatectl validate:", err)
				return 2
			}
			continue
		}
		if len(ds) == 0 {
			fmt.Fprintf(stdout, "%s: ok\n", path)
		}
		for _, d := range ds {
			fmt.Fprintf(stdout, "%s:%s\n", diagnosticFile(path, lib, d), d)
		}
		for _, b := range bs {
			label := ""
			if b.Binding.Label != "" {
				label = " " + strconv.Quote(b.Binding.Label)
			}
			if b.Binding.Block {
				fmt.Fprintf(stdout, "  %s%s: block\n", b.Binding.ID, label)
				continue
			}
			if b.Gesture == engine.GestureNone {
				fmt.Fprintf(stdout, "  %s%s: no action\n", b.Binding.ID, label)
				continue
			}
			haptic := ""
			if b.Binding.Haptic != 0 {
				haptic = ", haptic " + b.Binding.Haptic.String()
			}
			fmt.Fprintf(stdout, "  %s%s: %s +%s ms%s\n", b.Binding.ID, label, b.Gesture, latencyMs(b.AddedLatency), haptic)
		}
	}
	return status
}

// missingMacros warns at each action of the profile data at path that
// runs a macro file the macros folder (macroFolder) does not list under
// exactly its name, the folder padmated runs them from (SCR-50, SCR-51).
// A folder that cannot be read, other than a missing one, gives no
// warnings.
func missingMacros(path string, data []byte) profile.Diagnostics {
	entries, err := os.ReadDir(macroFolder(path))
	if err != nil && !errors.Is(err, iofs.ErrNotExist) {
		return nil
	}
	var listed []string
	for _, e := range entries {
		if e.Type().IsRegular() {
			listed = append(listed, e.Name())
		}
	}
	return profile.MissingMacros(data, func(file string) bool { return slices.Contains(listed, file) })
}

// macroFolder returns the macros folder that padmated runs the macros of
// the profile file path from: for a profile of a library,
// <data>/profiles/<id>/profile.json, the data folder's script library
// (SCR-51); otherwise, as for a --profile file without one, the macros
// folder next to the file.
func macroFolder(path string) string {
	if lib, _, ok := libraryOf(path); ok {
		return filepath.Join(lib.Dir(), profile.MacroDir)
	}
	return filepath.Join(filepath.Dir(path), profile.MacroDir)
}

// libraryOf returns the library and the ID of the profile whose file path
// is, when path is <data>/profiles/<id>/profile.json: such a profile can
// extend another (PRO-19, ADR-0070), which is read from the same library.
func libraryOf(path string) (lib *profilelib.Library, id string, ok bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, "", false
	}
	folder := filepath.Dir(abs)
	profiles := filepath.Dir(folder)
	if strings.EqualFold(filepath.Base(abs), "profile.json") && strings.EqualFold(filepath.Base(profiles), "profiles") && profilelib.ValidID(filepath.Base(folder)) {
		return profilelib.New(filepath.Dir(profiles)), filepath.Base(folder), true
	}
	return nil, "", false
}

// diagnosticFile is the file that d is in: path, or the file of the parent d
// says it is in.
func diagnosticFile(path string, lib *profilelib.Library, d profile.Diagnostic) string {
	if d.Profile != "" && lib != nil {
		if parent, err := lib.Path(d.Profile); err == nil {
			return parent
		}
	}
	return path
}

// diagnosticLine formats d for a message that does not start with the file:
// a problem in a parent's file says which.
func diagnosticLine(d profile.Diagnostic) string {
	if d.Profile != "" {
		return "parent " + strconv.Quote(d.Profile) + " " + d.String()
	}
	return d.String()
}

// sortDiagnostics orders ds: the file's own by offset, then each parent's
// file's, by the parent's ID and offset. Equal ones keep their order.
func sortDiagnostics(ds profile.Diagnostics) {
	slices.SortStableFunc(ds, func(a, b profile.Diagnostic) int {
		if c := strings.Compare(a.Profile, b.Profile); c != 0 {
			return c // the file's own, which has no ID, first
		}
		return a.Offset - b.Offset
	})
}

// compileBindings compiles p, a profile without errors, and returns its
// bindings. When Compile fails, which it does not for a parsed profile,
// it returns an error diagnostic at the start of the file instead.
func compileBindings(p *profile.Profile) ([]engine.BindingInfo, profile.Diagnostics) {
	prog, err := engine.Compile(p)
	if err != nil {
		return nil, profile.Diagnostics{{
			Severity: profile.SeverityError, Line: 1, Col: 1,
			Message: "cannot compile the profile: " + err.Error(),
		}}
	}
	return prog.Bindings(), nil
}

// latencyMs formats d in milliseconds, as an integer or the shortest
// decimal.
func latencyMs(d time.Duration) string {
	return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', -1, 64)
}
