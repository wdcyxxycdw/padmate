package profilelib

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// MaxNameRunes bounds a profile name that a client chooses, as the UI
// does.
const MaxNameRunes = 64

// DefaultNames are the names the fixed default profile goes by in the UI,
// whatever name it stores, so no other profile may take them.
var DefaultNames = []string{"默认", "Default"}

// CheckProfileName accepts a name that a client chooses for a profile:
// not empty once trimmed, at most MaxNameRunes characters.
func CheckProfileName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return errors.New("name must not be empty")
	}
	if utf8.RuneCountInString(trimmed) > MaxNameRunes {
		return fmt.Errorf("name must be at most %d characters", MaxNameRunes)
	}
	return nil
}

// nameKey is how profile names compare, as in the UI: trimmed, Unicode
// NFC, ignoring case.
func nameKey(name string) string {
	return strings.ToLower(norm.NFC.String(strings.TrimSpace(name)))
}

// NameTakenError refuses an import whose name another profile, ID, goes by.
type NameTakenError struct {
	Name string
	ID   string
}

func (e *NameTakenError) Error() string {
	return fmt.Sprintf("profile library: profile %s is already named %q", e.ID, e.Name)
}

// checkName refuses name when a profile other than self goes by it: its
// stored name, or its ID when it has none or does not load; the fixed
// default by DefaultNames.
func (l *Library) checkName(name, self string) error {
	entries, err := l.List()
	if err != nil {
		return err
	}
	key := nameKey(name)
	for _, e := range entries {
		if e.ID == self {
			continue
		}
		names := []string{e.Name}
		switch {
		case e.ID == DefaultID:
			names = DefaultNames
		case strings.TrimSpace(e.Name) == "":
			names = []string{e.ID}
		}
		for _, n := range names {
			if nameKey(n) == key {
				return &NameTakenError{Name: strings.TrimSpace(name), ID: e.ID}
			}
		}
	}
	return nil
}

// ImportOptions selects how Import adds an archive to the library.
type ImportOptions struct {
	// Name, unless empty, replaces the archive's name; CheckProfileName
	// applies and it is stored trimmed.
	Name string
	// Replace, unless empty, is the existing profile whose folder the
	// archive replaces; otherwise the archive becomes a new profile. The
	// two are exclusive.
	Replace string
	// Macros settles the archive's macros whose names the library has
	// with other bytes: MacrosAsk, MacrosReplace or MacrosKeep.
	Macros string
}

// Imported reports a profile that Import wrote.
type Imported struct {
	ID   string
	Name string
	// Data is the profile.json written: the archive's, with the name
	// changed when Name or the fixed default required it.
	Data []byte
	Saved
	// CleanupErr says why the replaced folder was not deleted entirely;
	// the import succeeded and the next Copy, Import or Remove deletes the
	// rest.
	CleanupErr error
	// Macros are the archive's macros, in the library now.
	Macros []ImportedMacro
}

// Import adds a checked archive to the library. Without Replace it is a
// new profile with a fresh ID made from its name (NewID); its name must
// not be one another profile goes by (*NameTakenError, as the UI
// compares names). With Replace, profiles/<Replace> becomes the
// archive's folder: its scripts and other files go, its HistoryDir stays
// and keeps the replaced profile.json as a snapshot. The archive's name
// may then be the replaced profile's own but no other's; replacing the
// fixed default keeps the default's stored name instead. The archive's
// name, when it is used, must pass CheckProfileName (an *ArchiveError
// with reason profile otherwise) and is stored trimmed. An item that the
// file system refuses as a duplicate (fs.ErrExist) is an *ArchiveError
// too.
//
// The archive's macros folder goes into the library (SCR-51), not the
// profile folder: a macro the library lacks is added, one it has with
// the same bytes is used, and one whose name it has with other bytes is a
// *MacroConflictError with opts.Macros MacrosAsk, before anything is
// written; MacrosReplace replaces the library's and MacrosKeep adds it
// under a numbered name (planImport). The imported profile.json runs the
// names the macros have in the library (profile.RenameMacroRefsAll). Its
// other files are written where the library has none.
//
// Like Copy, Import writes and syncs the files into a hidden temporary
// folder without the writer lock and, under it, renames the folder into
// place; for a replacement it first renames the existing folder aside to
// a hidden ".old-" name, which a cut off import leaves for the next
// Copy, Import, Remove or EnsureDefault to rename back. Windows refuses
// that rename while another program holds a file in the folder open:
// then nothing has changed.
func (l *Library) Import(a *Archive, opts ImportOptions) (Imported, error) {
	switch {
	case opts.Replace != "" && opts.Name != "":
		return Imported{}, errors.New("profile library: an import takes a new name or a profile to replace, not both")
	case opts.Replace != "" && !ValidID(opts.Replace):
		return Imported{}, fmt.Errorf("profile library: invalid id %q", opts.Replace)
	case opts.Macros != MacrosAsk && opts.Macros != MacrosReplace && opts.Macros != MacrosKeep:
		return Imported{}, fmt.Errorf("profile library: macros must be %q or %q", MacrosReplace, MacrosKeep)
	case opts.Name != "":
		if err := CheckProfileName(opts.Name); err != nil {
			return Imported{}, err
		}
	}
	data := a.profileData()
	name := a.Profile.Name
	rename := strings.TrimSpace(opts.Name)
	if opts.Replace == DefaultID {
		rename = ""
		if current, err := l.Read(DefaultID); err == nil {
			if p, ds := l.Parse(DefaultID, current); ds.Err() == nil {
				rename = p.Name
			}
		}
	}
	// The archive's own name, as a client would have to choose it.
	if rename == "" {
		if err := CheckProfileName(name); err != nil {
			return Imported{}, archiveNameError(err)
		}
		rename = strings.TrimSpace(name)
	}
	if rename != name {
		var err error
		if data, err = setName(data, rename); err != nil {
			return Imported{}, err
		}
		name = rename
	}
	items, macros, modules := splitArchive(a.items)
	if err := os.MkdirAll(filepath.Join(l.dir, "profiles"), 0o700); err != nil {
		return Imported{}, err
	}
	l.copyMu.Lock()
	defer l.copyMu.Unlock()
	root, err := os.OpenRoot(filepath.Join(l.dir, "profiles"))
	if err != nil {
		return Imported{}, err
	}
	defer root.Close()
	l.removeLeftovers(root)
	// Refuse a missing profile or a taken name before writing anything;
	// publishing checks them again under the writer lock.
	if opts.Replace != "" {
		if err := checkFolder(root, opts.Replace); err != nil {
			return Imported{}, err
		}
	}
	if opts.Replace != DefaultID {
		if err := l.checkName(name, opts.Replace); err != nil {
			return Imported{}, err
		}
	}
	l.writeMu.Lock()
	plan, err := l.planImport(macros, modules, opts.Macros)
	l.writeMu.Unlock()
	if err == nil {
		err = conflictsOf(plan)
	}
	if err != nil {
		return Imported{}, err
	}
	named := data
	if data, err = renameRefs(named, plan); err != nil {
		return Imported{}, err
	}
	for i := range items {
		if items[i].path == "profile.json" {
			items[i].data = data
		}
	}
	tmp := tempName("import")
	if err := root.Mkdir(tmp, 0o700); err != nil {
		return Imported{}, err
	}
	result := Imported{Name: name, Data: data, Macros: plan.macros}
	result.Revision, err = writeItems(root, tmp, items)
	err = clashError(err)
	trash := ""
	if err == nil {
		err = l.publishMacros(func(now *importPlan) error {
			if !sameRenames(plan, now) {
				// The library changed since the plan: the folder's
				// profile.json runs the names the macros get now.
				next, err := renameRefs(named, now)
				if err != nil {
					return err
				}
				if err := replaceInFolder(root, tmp, next); err != nil {
					return err
				}
				result.Data, result.Revision = next, Revision(next)
			}
			result.Macros = now.macros
			var err error
			if opts.Replace == "" {
				result.ID, err = l.publishImportLocked(root, tmp, name)
			} else {
				result.ID = opts.Replace
				result.HistoryErr, trash, err = l.publishReplaceLocked(root, tmp, opts.Replace, name, result.Data)
			}
			return err
		}, macros, modules, opts.Macros)
	}
	if err != nil {
		if removeErr := root.RemoveAll(tmp); removeErr != nil {
			err = errors.Join(err, removeErr)
		}
		return Imported{}, err
	}
	if trash != "" {
		result.CleanupErr = root.RemoveAll(trash)
	}
	return result, nil
}

// archiveNameError refuses an archive whose profile.json has a name that
// CheckProfileName does not accept and no other name was given.
func archiveNameError(err error) *ArchiveError {
	e := archiveError(ArchiveProfileReason, ArchiveFolder+"profile.json", "has a name padmate does not accept: "+err.Error())
	e.Diagnostics = profile.Diagnostics{{Severity: profile.SeverityError, Pointer: "/name", Message: err.Error()}}
	return e
}

// clashError makes err, from writing a checked archive's items, an
// ArchiveError when an item could not be created because the file system
// already has that name for another item: one ReadArchive did not see as
// the same, such as a long name whose 8.3 short name another item has.
func clashError(err error) error {
	if !errors.Is(err, fs.ErrExist) {
		return err
	}
	item := ""
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		item = ArchiveFolder + filepath.ToSlash(pathErr.Path)
	}
	return archiveError(ArchiveEntryReason, item, "clashes with another entry on this file system")
}

// renameRefs returns the profile.json data with the references the plan
// renames.
func renameRefs(data []byte, plan *importPlan) ([]byte, error) {
	if len(plan.renames) == 0 {
		return data, nil
	}
	next, _, err := profile.RenameMacroRefsAll(data, plan.renames)
	if err != nil {
		return nil, err
	}
	if _, ds := profile.Parse(next); ds.Err() != nil {
		return nil, ds.Err()
	}
	return next, nil
}

// replaceInFolder replaces profile.json of the temporary folder tmp with
// data.
func replaceInFolder(root *os.Root, tmp string, data []byte) error {
	folder, err := root.OpenRoot(tmp)
	if err != nil {
		return err
	}
	defer folder.Close()
	return atomicReplace(folder, "profile.json", data)
}

// publishMacros plans the archive's macros again under the writer lock,
// writes them into the library and runs publish, which puts the folder in
// place; when publish fails, the library gets its macros back. A conflict
// that the library got since Import planned is a *MacroConflictError.
func (l *Library) publishMacros(publish func(*importPlan) error, macros, modules []copyItem, choice string) error {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	plan, err := l.planImport(macros, modules, choice)
	if err == nil {
		err = conflictsOf(plan)
	}
	if err != nil {
		return err
	}
	undo, err := l.writeImport(plan)
	if err != nil {
		return err
	}
	if err := publish(plan); err != nil {
		if u := undo(); u != nil {
			err = errors.Join(err, fmt.Errorf("profile library: restoring the macros: %w", u))
		}
		return err
	}
	return nil
}

// publishImportLocked renames the complete temporary folder to a new ID
// made from name, unless another profile has taken the name since Import
// checked it. The caller holds the writer lock.
func (l *Library) publishImportLocked(root *os.Root, tmp, name string) (string, error) {
	if err := l.checkName(name, ""); err != nil {
		return "", err
	}
	// Lstat also finds a folder whose name differs only in case, which
	// Windows would not create beside it.
	id := NewID(name, func(id string) bool {
		_, err := root.Lstat(id)
		return !errors.Is(err, fs.ErrNotExist)
	})
	return id, renameDir(root, tmp, id)
}

// publishReplaceLocked moves profiles/<id> aside to a hidden
// replacedName, renames the temporary folder to id, keeps old, the
// replaced profile.json, as a snapshot and moves the replaced HistoryDir
// into the new folder; the caller holds the writer lock. The replaced
// folder is renamed to a temporary name for the caller to delete without
// the lock (trash). A failure to keep the history is historyErr; the
// import itself succeeded.
func (l *Library) publishReplaceLocked(root *os.Root, tmp, id, name string, data []byte) (historyErr error, trash string, err error) {
	if err := checkFolder(root, id); err != nil {
		return nil, "", err
	}
	if id != DefaultID {
		if err := l.checkName(name, id); err != nil {
			return nil, "", err
		}
	}
	old, readErr := l.Read(id)
	// The renamed folder carries its own creation time, and the profiles
	// page lists by it, so the replaced profile keeps the place of the
	// folder it replaces. Best effort, as for a macro.
	if info, err := root.Lstat(id); err == nil {
		if born, ok := birthTime(info); ok {
			_ = setFolderBirthTime(root, tmp, born)
		}
	}
	held := replacedName(id)
	if err := renameDir(root, id, held); err != nil {
		return nil, "", err
	}
	if err := renameDir(root, tmp, id); err != nil {
		// When this fails too, the next cleanup renames the folder back.
		if back := renameDir(root, held, id); back != nil {
			err = errors.Join(err, back)
		}
		return nil, "", err
	}
	if readErr == nil && !bytes.Equal(old, data) {
		historyErr = keepReplaced(root, held, id, old)
	}
	if info, err := root.Lstat(filepath.Join(held, HistoryDir)); err == nil && info.Mode().Type() == fs.ModeDir {
		if err := root.Rename(filepath.Join(held, HistoryDir), filepath.Join(id, HistoryDir)); err != nil {
			historyErr = errors.Join(historyErr, err)
		}
	}
	trash = tempName(id)
	if err := renameDir(root, held, trash); err != nil {
		trash = held // deleted directly; a rest is a leftover once id exists
	}
	return historyErr, trash, nil
}

// keepReplaced keeps old, the profile.json still in the replaced folder
// held, as a snapshot in held's HistoryDir, dated like a save's.
func keepReplaced(root *os.Root, held, id string, old []byte) error {
	folder, err := root.OpenRoot(held)
	if err != nil {
		return err
	}
	defer folder.Close()
	_, err = keepSnapshot(folder, id, old, time.Now())
	return err
}

// replacedPrefix starts the hidden name of a folder that Import moved
// aside while it replaces the profile; ValidID never accepts it.
const replacedPrefix = ".old-"

// replacedSuffix is the length of the random part of a replacedName,
// with its dash.
const replacedSuffix = 13

func replacedName(id string) string {
	return replacedPrefix + id + "-" + strings.ToLower(rand.Text()[:replacedSuffix-1])
}

// replacedID returns the profile ID in a replacedName.
func replacedID(name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, replacedPrefix)
	if !ok || len(rest) <= replacedSuffix || rest[len(rest)-replacedSuffix] != '-' {
		return "", false
	}
	id := rest[:len(rest)-replacedSuffix]
	return id, ValidID(id)
}

// restoreReplaced renames each replaced folder whose profile is missing
// back to its ID: an import that was cut off between moving the folder
// aside and the new one into place. The caller holds the writer lock.
func restoreReplaced(root *os.Root) {
	for _, name := range hiddenFolders(root, replacedPrefix) {
		id, ok := replacedID(name)
		if !ok {
			continue
		}
		if _, err := root.Lstat(id); errors.Is(err, fs.ErrNotExist) {
			_ = renameDir(root, name, id)
		}
	}
}

// retireReplaced renames the replaced folders left of profile id to
// temporary names, so that restoreReplaced does not bring them back once
// id is deleted. The caller holds the writer lock.
func retireReplaced(root *os.Root, id string) {
	for _, name := range hiddenFolders(root, replacedPrefix) {
		if held, ok := replacedID(name); ok && held == id {
			_ = renameDir(root, name, tempName(id))
		}
	}
}

// NewID makes a library ID from a profile name as the UI does: the name's
// ASCII letters and digits, lower case, without accents, joined by "-",
// or "profile" when none remain, then "-2", "-3"… until it is a valid ID
// that is not DefaultID and that taken does not report.
func NewID(name string, taken func(string) bool) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFKD.String(name) {
		r = unicode.ToLower(r)
		switch {
		case unicode.Is(unicode.M, r):
			continue
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		default:
			dash = true
		}
	}
	base := b.String()
	if base == "" {
		base = "profile"
	}
	for n := 1; ; n++ {
		suffix := ""
		if n > 1 {
			suffix = "-" + strconv.Itoa(n)
		}
		stem := strings.TrimRight(base[:min(len(base), 64-len(suffix))], "-")
		if stem == "" {
			stem = "profile"
		}
		id := stem + suffix
		if ValidID(id) && id != DefaultID && !taken(id) {
			return id
		}
	}
}

// setName returns data, a valid profile document, with its top-level name
// set to name; everything else keeps its bytes.
func setName(data []byte, name string) ([]byte, error) {
	start, end, err := nameSpan(data)
	if err != nil {
		return nil, err
	}
	var value bytes.Buffer
	encoder := json.NewEncoder(&value)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(name); err != nil {
		return nil, err
	}
	out := slices.Concat(data[:start], bytes.TrimSuffix(value.Bytes(), []byte("\n")), data[end:])
	p, ds := profile.Parse(out)
	if err := ds.Err(); err != nil {
		return nil, err
	}
	if p.Name != name {
		return nil, errors.New("profile library: could not rename the profile")
	}
	return out, nil
}

// nameSpan finds the value of the top-level member "name" in data.
func nameSpan(data []byte) (start, end int, err error) {
	bom := 0
	if bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) {
		bom = 3
	}
	decoder := json.NewDecoder(bytes.NewReader(data[bom:]))
	if t, err := decoder.Token(); err != nil || t != json.Delim('{') {
		return 0, 0, errors.New("profile library: profile.json is not an object")
	}
	start = -1
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return 0, 0, err
		}
		at := bom + int(decoder.InputOffset())
		for at < len(data) && (data[at] == ':' || data[at] == ' ' || data[at] == '\t' || data[at] == '\r' || data[at] == '\n') {
			at++
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return 0, 0, err
		}
		if key == "name" {
			start, end = at, bom+int(decoder.InputOffset())
		}
	}
	if start < 0 {
		return 0, 0, errors.New("profile library: profile.json has no name")
	}
	return start, end, nil
}
