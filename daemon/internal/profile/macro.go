package profile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
)

// MacroDir is the folder that holds macros (SCR-50, ADR-0055): each
// top-level .ts or .js file in it, not a .d.ts declaration, is one macro.
// The script library is the one in the data folder, which every library
// profile runs its macros from (SCR-51); a --profile file without a
// library runs them from the one next to it. A legacy script.entry must
// not be in its profile folder's.
const MacroDir = "macros"

// MacroTypesDir is the folder of MacroDir where the daemon writes the
// macros' type declarations for editors (dtsgen.MacroProject). It starts
// with a dot: tsc's ** does not match it, so a legacy script's
// tsconfig.json, which includes **/*.ts, does not read a second padmate
// module. padmate writes it again when needed: no bundle reads it, and an
// exported profile leaves it out.
const MacroTypesDir = ".padmate"

// MaxMacroName is the most characters (code points) a macro's name, its
// file name without the extension, may have.
const MaxMacroName = 40

// MaxMacros is the most macros a macros folder may have: the script
// library that every profile shares (SCR-51).
const MaxMacros = 64

// MacroNameKind says why CheckMacroFile refused a name. The UI shows one
// message per kind; daemon/testdata/macro-names.json lists cases of each,
// which the Go and the TypeScript checks share.
type MacroNameKind string

// The kinds of a refused macro file name.
const (
	// MacroNameExtension: the name does not end in a lowercase .ts or .js.
	MacroNameExtension MacroNameKind = "extension"
	// MacroNameEmpty: the name without the extension is empty.
	MacroNameEmpty MacroNameKind = "empty"
	// MacroNameLength: the name has more than MaxMacroName characters.
	MacroNameLength MacroNameKind = "length"
	// MacroNameChar: the name has a character Windows does not allow in a
	// file name, a dot, a tilde, a space other than U+0020 or a character
	// that is not visible.
	MacroNameChar MacroNameKind = "char"
	// MacroNameInvalid: the name is not NFC, starts with a space or "-",
	// ends with a space or is a Windows device name.
	MacroNameInvalid MacroNameKind = "invalid"
)

// MacroNameError is the error of CheckMacroFile.
type MacroNameError struct {
	Name string
	Kind MacroNameKind
	// Detail says what is wrong, in English, for logs and diagnostics.
	Detail string
}

func (e *MacroNameError) Error() string {
	return fmt.Sprintf("macro file name %q: %s", e.Name, e.Detail)
}

// CheckMacroFile accepts the file name of a macro: "<name>.ts" or
// "<name>.js" with a lowercase extension, where name is 1 to MaxMacroName
// characters in NFC; each character is visible (unicode.IsGraphic), a
// space is U+0020, and none is one of \ / : * ? " < > | (Windows refuses
// them), "." (no .d.ts, hidden files or a trailing dot that Windows drops)
// or "~" (an 8.3 short name such as ABCDEF~1.TS may name another file).
// The name does not start with a space or "-", does not end with a space
// and is not a Windows device name, compared case-insensitively: CON, PRN,
// AUX, NUL, CONIN$, CONOUT$, COM0 to COM9, LPT0 to LPT9 and COM or LPT
// with ¹, ² or ³. These are the device names an export refuses
// (profilelib's archiveElementProblem), so every macro exports.
//
// The checks run in that order, after the extension, the empty name and
// valid UTF-8, so the error's Kind is the first rule the name breaks; the
// TypeScript check in the UI follows the same order
// (daemon/testdata/macro-names.json).
func CheckMacroFile(file string) error {
	stem, ok := strings.CutSuffix(file, ".ts")
	if !ok {
		stem, ok = strings.CutSuffix(file, ".js")
	}
	switch {
	case !ok:
		return &MacroNameError{file, MacroNameExtension, "the name must end in a lowercase .ts or .js"}
	case stem == "":
		return &MacroNameError{file, MacroNameEmpty, "the name before the extension is empty"}
	case !utf8.ValidString(stem):
		return &MacroNameError{file, MacroNameChar, "the name is not valid UTF-8"}
	case !norm.NFC.IsNormalString(stem):
		return &MacroNameError{file, MacroNameInvalid, "the name is not in Unicode normalization form C (NFC)"}
	case utf8.RuneCountInString(stem) > MaxMacroName:
		return &MacroNameError{file, MacroNameLength, fmt.Sprintf("the name has more than %d characters", MaxMacroName)}
	}
	for _, r := range stem {
		switch {
		case strings.ContainsRune(`\/:*?"<>|.~`, r):
			return &MacroNameError{file, MacroNameChar, fmt.Sprintf("the name contains %q", r)}
		case r != ' ' && unicode.IsSpace(r), !unicode.IsGraphic(r):
			return &MacroNameError{file, MacroNameChar, fmt.Sprintf("the name contains %U", r)}
		}
	}
	switch {
	case stem[0] == ' ' || stem[0] == '-':
		return &MacroNameError{file, MacroNameInvalid, "the name starts with a space or \"-\""}
	case stem[len(stem)-1] == ' ':
		return &MacroNameError{file, MacroNameInvalid, "the name ends with a space"}
	case isDeviceName(stem):
		return &MacroNameError{file, MacroNameInvalid, "the name is a Windows device name"}
	}
	return nil
}

// isDeviceName reports whether stem is a Windows device name, which
// Windows opens as the device whatever the folder and extension. Like the
// export's check it compares strings.ToLower(stem), which also makes the
// dotted İ an i.
func isDeviceName(stem string) bool {
	lower := strings.ToLower(stem)
	switch lower {
	case "con", "prn", "aux", "nul", "conin$", "conout$":
		return true
	}
	r := []rune(lower)
	if len(r) != 4 || string(r[:3]) != "com" && string(r[:3]) != "lpt" {
		return false
	}
	return '0' <= r[3] && r[3] <= '9' || r[3] == '¹' || r[3] == '²' || r[3] == '³'
}

// MacroName returns the name of the macro file: file without its .ts or .js
// extension, in any case, as the UI shows it. A listed file whose
// extension is in upper case, which CheckMacroFile refuses, still has the
// name of the file it would replace on Windows.
func MacroName(file string) string {
	if n := len(file); n >= 3 {
		if ext := strings.ToLower(file[n-3:]); ext == ".ts" || ext == ".js" {
			return file[:n-3]
		}
	}
	return file
}

// SameMacroName reports whether the macro files a and b have the same
// name, ignoring case and the extension: a profile cannot have both. Like
// Windows, it compares the names case-insensitively.
func SameMacroName(a, b string) bool {
	return strings.EqualFold(MacroName(a), MacroName(b))
}

// InMacroDir reports whether entry, a legacy script entry relative to the
// profile's folder as profile.json writes it, is in MacroDir: its first
// element, with either separator and after cleaning, is "macros" in any
// case. An absolute entry or one that starts with ".." is not: the script
// compiler refuses those as outside the folder.
func InMacroDir(entry string) bool {
	cleaned := path.Clean(strings.ReplaceAll(entry, `\`, "/"))
	first, _, _ := strings.Cut(cleaned, "/")
	return strings.EqualFold(first, MacroDir)
}

// MacroCandidate reports whether name, a file at the top of MacroDir, is
// listed as a macro: its extension is .ts or .js in any case, and it is not
// a .d.ts declaration. A candidate whose name CheckMacroFile refuses is
// listed too, so the user can rename or delete it; it never builds.
func MacroCandidate(name string) bool {
	lower := strings.ToLower(name)
	return (strings.HasSuffix(lower, ".ts") || strings.HasSuffix(lower, ".js")) && !strings.HasSuffix(lower, ".d.ts")
}

// MacroFiles returns the macro files p's actions reference, in every layer,
// common and function key set, without repeats, sorted.
func (p *Profile) MacroFiles() []string {
	var files []string
	p.eachAction(func(_ *Binding, _ Gesture, a *Action) {
		if a.Type == ActionMacro && !slices.Contains(files, a.File) {
			files = append(files, a.File)
		}
	})
	slices.Sort(files)
	return files
}

// eachAction calls f for every action of p's layers, then of common and the
// function key sets.
func (p *Profile) eachAction(f func(*Binding, Gesture, *Action)) {
	layers := slices.Clone(p.Layers)
	if p.Common != nil {
		layers = append(layers, p.Common)
	}
	layers = append(layers, p.FunctionSets()...)
	for _, l := range layers {
		for _, b := range l.Bindings {
			for g := GesturePress; g <= GestureHold; g++ {
				if a := b.Action(g); a != nil {
					f(b, g, a)
				}
			}
		}
	}
}

// MacroUse is a control and gesture that run a macro.
type MacroUse struct {
	Control control.Control
	Gesture Gesture
}

// gestureMember is a binding member that holds an action.
type gestureMember struct {
	name string
	g    Gesture
}

// gestureMembers are the binding members that hold an action, by gesture.
var gestureMembers = []gestureMember{
	{"press", GesturePress}, {"release", GestureRelease}, {"tap", GestureTap},
	{"doubleTap", GestureDoubleTap}, {"tripleTap", GestureTripleTap},
	{"quadTap", GestureQuadTap}, {"hold", GestureHold},
}

// gestureOf returns the gesture of the binding member called name.
func gestureOf(name string) (Gesture, bool) {
	for _, m := range gestureMembers {
		if m.name == name {
			return m.g, true
		}
	}
	return GestureNone, false
}

// actionRef is one action object of a scanned profile: a member of its
// binding, which is a member of its layer. prefix is the JSON Pointer tokens
// of the layer, such as {"layers", "base"}, {"common"}, {"functionSet"} or
// {"functionKeys", "back"}.
type actionRef struct {
	layer, binding, action *node
	prefix                 []string
	control                control.Control
	gesture                Gesture
}

// actionRefs returns every action object of the scanned profile root, in
// document order: the members of each layer's bindings, then of common's,
// of functionSet and of the old function keys' sets, that name a gesture.
// Only the syntax matters, so it works on a profile that fails the schema or
// the semantic checks; names that are not controls, and values that are not
// objects, are skipped.
func actionRefs(root *node) []actionRef {
	var refs []actionRef
	visit := func(layer *node, prefix ...string) {
		if layer == nil || layer.members == nil {
			return
		}
		for _, b := range layer.order {
			c, ok := control.Parse(b.name)
			if !ok || b.members == nil {
				continue
			}
			for _, m := range b.order {
				g, ok := gestureOf(m.name)
				if !ok || m.members == nil {
					continue
				}
				refs = append(refs, actionRef{layer: layer, binding: b, action: m, prefix: prefix, control: c, gesture: g})
			}
		}
	}
	if root == nil || root.members == nil {
		return nil
	}
	if layers := root.members["layers"]; layers != nil {
		for _, l := range layers.order {
			visit(l, "layers", l.name)
		}
	}
	visit(root.members["common"], "common")
	visit(root.members["functionSet"], "functionSet")
	if fk := root.members["functionKeys"]; fk != nil {
		for _, name := range []string{"back", "start"} {
			visit(fk.members[name], "functionKeys", name)
		}
	}
	return refs
}

// macroFileOf returns the file member of the action object a when its type
// is "macro" and file is a string, or nil.
func macroFileOf(a *node) *node {
	t, f := a.members["type"], a.members["file"]
	if t == nil || t.val != "macro" || f == nil {
		return nil
	}
	if _, ok := f.val.(string); !ok {
		return nil
	}
	return f
}

// MacroRefs returns, by file, the controls and gestures whose actions in
// the profile data run that macro: aliases count as their canonical
// control and every layer and common are merged, so a use is listed once,
// in control then gesture order. Only the syntax of data matters
// (macros.list reads a profile.json that may not pass Parse); the error is
// its syntax error.
func MacroRefs(data []byte) (map[string][]MacroUse, error) {
	root, serr := scan(data)
	if serr != nil {
		return nil, syntaxErr(data, serr)
	}
	refs := map[string][]MacroUse{}
	for _, r := range actionRefs(root) {
		f := macroFileOf(r.action)
		if f == nil {
			continue
		}
		file := f.val.(string)
		use := MacroUse{Control: r.control, Gesture: r.gesture}
		if !slices.Contains(refs[file], use) {
			refs[file] = append(refs[file], use)
		}
	}
	for _, uses := range refs {
		slices.SortFunc(uses, func(a, b MacroUse) int {
			if a.Control != b.Control {
				return int(a.Control) - int(b.Control)
			}
			return int(a.Gesture) - int(b.Gesture)
		})
	}
	return refs, nil
}

// MacroActionCounts returns, by file, how many actions of the profile data
// run that macro, in every layer and common: as many as RemoveMacroRefs
// removes, where MacroRefs merges the uses of a control and gesture. Only
// the syntax of data matters; the error is its syntax error.
func MacroActionCounts(data []byte) (map[string]int, error) {
	root, serr := scan(data)
	if serr != nil {
		return nil, syntaxErr(data, serr)
	}
	counts := map[string]int{}
	for _, r := range actionRefs(root) {
		if f := macroFileOf(r.action); f != nil {
			counts[f.val.(string)]++
		}
	}
	return counts, nil
}

// MissingMacros returns a warning at each action of the profile data that
// runs a macro file the macros folder it runs from lacks; listed reports
// whether the folder lists a file under exactly its name, as the runtime
// finds it. A name CheckMacroFile refuses is Parse's error and gets no
// warning. Only the syntax of data matters: a syntax error gives none.
// padmatectl validate warns with it (SCR-50); Parse does not, as a missing
// file must not keep the profile from loading.
func MissingMacros(data []byte, listed func(file string) bool) Diagnostics {
	root, serr := scan(data)
	if serr != nil || root == nil {
		return nil
	}
	var ds Diagnostics
	for _, r := range actionRefs(root) {
		f := macroFileOf(r.action)
		if f == nil {
			continue
		}
		file := f.val.(string)
		if CheckMacroFile(file) != nil || listed(file) {
			continue
		}
		tokens := append(slices.Clone(r.prefix), r.binding.name, r.action.name, "file")
		ds = append(ds, Diagnostic{Severity: SeverityWarning, Pointer: pointer(tokens), Offset: f.start,
			Message: fmt.Sprintf("macro file %q is not in the macros folder", file)})
	}
	if ds == nil {
		return nil
	}
	return place(data, ds)
}

// syntaxErr formats the syntax error of data as Parse reports it.
func syntaxErr(data []byte, serr *syntaxError) error {
	ds := place(data, Diagnostics{{Severity: SeverityError, Offset: serr.off, Message: serr.msg}})
	return fmt.Errorf("profile: %s", ds[0].String())
}

// RenameMacroRefs returns data with every macro action that runs from
// running to instead, and how many actions it changed. Only those file
// strings change, in place: every other byte of data stays as it is, the
// formatting and the order of members included. data needs valid syntax
// only; the caller checks the result as it checks any profile it writes.
func RenameMacroRefs(data []byte, from, to string) ([]byte, int, error) {
	return RenameMacroRefsAll(data, map[string]string{from: to})
}

// RenameMacroRefsAll is RenameMacroRefs for several macros at once: every
// macro action that runs a key of renames runs its value instead. The
// renames happen together, so a file renamed to another's old name is not
// renamed again.
func RenameMacroRefsAll(data []byte, renames map[string]string) ([]byte, int, error) {
	root, serr := scan(data)
	if serr != nil {
		return nil, 0, syntaxErr(data, serr)
	}
	encoded := make(map[string]string, len(renames))
	for from, to := range renames {
		e, err := encodeString(to)
		if err != nil {
			return nil, 0, err
		}
		encoded[from] = e
	}
	var edits []edit
	for _, r := range actionRefs(root) {
		f := macroFileOf(r.action)
		if f == nil {
			continue
		}
		if e, ok := encoded[f.val.(string)]; ok {
			edits = append(edits, edit{f.start, f.end, e})
		}
	}
	return applyEdits(data, edits), len(edits), nil
}

// RemoveMacroRefs returns data without the macro actions that run file,
// and how many it removed. A binding left with no action and no block is
// removed as a whole, its label and other settings with it. Each removal
// takes out the member and the comma and space that separate it from its
// neighbor; everything else stays byte for byte.
func RemoveMacroRefs(data []byte, file string) ([]byte, int, error) {
	return removeActions(data, func(a *node) bool {
		f := macroFileOf(a)
		return f != nil && f.val == file
	}, false)
}

// DetachScript returns data without its legacy script: the "script"
// member and every action of type "script", and how many actions it
// removed. Bindings left with no action are removed as RemoveMacroRefs
// does.
func DetachScript(data []byte) ([]byte, int, error) {
	return removeActions(data, func(a *node) bool {
		t := a.members["type"]
		return t != nil && t.val == "script"
	}, true)
}

func removeActions(data []byte, match func(*node) bool, script bool) ([]byte, int, error) {
	root, serr := scan(data)
	if serr != nil {
		return nil, 0, syntaxErr(data, serr)
	}
	removed := map[*node][]*node{} // members to remove, by object
	n := 0
	var bindings []actionRef
	for _, r := range actionRefs(root) {
		if !match(r.action) {
			continue
		}
		n++
		removed[r.binding] = append(removed[r.binding], r.action)
		if len(bindings) == 0 || bindings[len(bindings)-1].binding != r.binding {
			bindings = append(bindings, r)
		}
	}
	for _, r := range bindings {
		if !keepsAction(r.binding, removed[r.binding]) {
			delete(removed, r.binding)
			removed[r.layer] = append(removed[r.layer], r.binding)
		}
	}
	if s := root.members["script"]; script && s != nil {
		removed[root] = append(removed[root], s)
	}
	var edits []edit
	for obj, members := range removed {
		edits = append(edits, removeMembers(obj, members)...)
	}
	return applyEdits(data, edits), n, nil
}

// keepsAction reports whether binding still does something once members
// are removed: another gesture's action, or block.
func keepsAction(binding *node, members []*node) bool {
	if b := binding.members["block"]; b != nil && b.val == true {
		return true
	}
	for _, m := range binding.order {
		if slices.Contains(members, m) {
			continue
		}
		if _, ok := gestureOf(m.name); ok {
			return true
		}
	}
	return false
}

// removeMembers returns the edits that remove members from the object obj.
// A member followed by a kept member goes up to that member's key; the
// members after the last kept one go from its end, which takes the comma
// before them; with every member removed the object becomes {}.
func removeMembers(obj *node, members []*node) []edit {
	last := -1
	for i, m := range obj.order {
		if !slices.Contains(members, m) {
			last = i
		}
	}
	if last < 0 {
		return []edit{{obj.start, obj.end, "{}"}}
	}
	var edits []edit
	for i, m := range obj.order[:last] {
		if slices.Contains(members, m) {
			edits = append(edits, edit{m.key, obj.order[i+1].key, ""})
		}
	}
	if last < len(obj.order)-1 {
		edits = append(edits, edit{obj.order[last].end, obj.order[len(obj.order)-1].end, ""})
	}
	return edits
}

// encodeString returns s as a JSON string, without escaping the HTML
// characters as encoding/json does by default.
func encodeString(s string) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// edit replaces data[start:end] with text.
type edit struct {
	start, end int
	text       string
}

// applyEdits returns a copy of data with the edits, which do not overlap,
// applied.
func applyEdits(data []byte, edits []edit) []byte {
	slices.SortFunc(edits, func(a, b edit) int { return a.start - b.start })
	out := make([]byte, 0, len(data))
	at := 0
	for _, e := range edits {
		out = append(out, data[at:e.start]...)
		out = append(out, e.text...)
		at = e.end
	}
	return append(out, data[at:]...)
}
