package profilelib

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// The script library (SCR-51): one macros folder in the data folder,
// <data>/macros, that every profile's macro actions run from (SCR-50,
// ADR-0055). Every edit runs under the library's writer lock through
// os.Root, as UpdateFolder's do, and touches a macro only under a name that
// ReadDir lists exactly, so another case or an 8.3 short name never reaches
// another file. A rename or a deletion edits the references in every
// profile.json in the same lock.

// MacroNotFoundError refuses an edit of a macro that is not in the
// library's macros folder under exactly its name.
type MacroNotFoundError struct{ File string }

func (e *MacroNotFoundError) Error() string {
	return fmt.Sprintf("profile library: no macro %q", e.File)
}

// MacroExistsError refuses a macro whose name another macro of the library
// has, ignoring case and the extension (profile.SameMacroName).
type MacroExistsError struct {
	// Name is the existing file, exactly as the folder lists it.
	Name string
	// Replaceable reports that it has the same extension, so an import may
	// replace it.
	Replaceable bool
}

func (e *MacroExistsError) Error() string {
	return fmt.Sprintf("profile library: the macro %q already exists", e.Name)
}

// MacroLimitError refuses a new macro of a library that has
// profile.MaxMacros.
type MacroLimitError struct{}

func (e *MacroLimitError) Error() string {
	return fmt.Sprintf("profile library: the library has %d macros, the most it may have", profile.MaxMacros)
}

// ErrMacroExtension refuses a rename that changes a macro's extension.
var ErrMacroExtension = errors.New("profile library: a renamed macro keeps its extension")

// ErrNoScript refuses ScriptPath for a profile without a legacy script.
var ErrNoScript = errors.New("profile library: the profile has no legacy script")

// EntryNotFoundError reports a legacy script entry that does not exist. It
// is an fs.ErrNotExist.
type EntryNotFoundError struct{ ID, Entry string }

func (e *EntryNotFoundError) Error() string {
	return fmt.Sprintf("profile library: %s has no script file %q", e.ID, e.Entry)
}

func (e *EntryNotFoundError) Unwrap() error { return fs.ErrNotExist }

// MacroFile is a file of the macros folder that the daemon writes for
// editors (dtsgen.MacroProject), named relative to the folder with
// slashes.
type MacroFile struct {
	Name string
	Data []byte
	// Keep writes the file only when it is missing, never over the user's
	// edits: tsconfig.json. Otherwise it is rewritten when it differs.
	Keep bool
}

// ProfileEdit is a profile.json that an edit changed.
type ProfileEdit struct {
	ID string
	// Bindings is how many actions the edit changed or removed.
	Bindings int
	// Data is the profile.json written, nil when the edit left it as it
	// was; Saved has its revision either way.
	Data []byte
	// Old is the profile.json that Data replaced.
	Old   []byte
	Saved Saved
}

// MacroEdit is the result of a rename or a deletion of a library macro.
type MacroEdit struct {
	// File is the macro after a rename.
	File string
	// Bindings is how many actions the edit changed or removed, in every
	// profile.
	Bindings int
	// Profiles are the profiles whose profile.json the edit wrote, by ID.
	Profiles []ProfileEdit
}

// MacroDirPath names the library's macros folder in errors.
const macroDirPath = profile.MacroDir

// MacroDir returns the library's macros folder, <data>/macros, which the
// runtime builds macros from. Use the Library's methods to write it.
func (l *Library) MacroDir() string { return filepath.Join(l.dir, profile.MacroDir) }

// openMacroDir opens the library's macros folder, creating it, and the
// data folder, when create is set and it is missing; otherwise a missing
// folder is fs.ErrNotExist. A macros folder that is a link, a junction or
// a file is an UnsupportedError, never followed.
func (l *Library) openMacroDir(create bool) (*os.Root, error) {
	if create {
		if err := os.MkdirAll(l.dir, 0o700); err != nil {
			return nil, err
		}
	}
	data, err := os.OpenRoot(l.dir)
	if err != nil {
		return nil, err
	}
	defer data.Close()
	return openMacros(data, macroDirPath, create)
}

// openMacros opens the macros folder of root, creating it when create is
// set and it is missing. where names it in errors.
func openMacros(root *os.Root, where string, create bool) (*os.Root, error) {
	info, err := root.Lstat(profile.MacroDir)
	switch {
	case errors.Is(err, fs.ErrNotExist) && create:
		if err := root.Mkdir(profile.MacroDir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
	case err != nil:
		return nil, err
	case info.Mode().Type() != fs.ModeDir:
		return nil, &UnsupportedError{Path: where, Reason: "is a link or file, not a macros folder"}
	}
	return root.OpenRoot(profile.MacroDir)
}

// MacroEntry is a macro of the library's macros folder, with when its file
// was created.
type MacroEntry struct {
	Name string
	// Created is the file's creation time, zero when the platform does not
	// say (birthTime). A rename and an import that replaces the macro keep
	// it.
	Created time.Time
}

// listMacroEntries returns the macros of the macros folder macros: the
// regular files at its top that profile.MacroCandidate accepts, names
// refused by CheckMacroFile included, sorted by file name.
func listMacroEntries(macros *os.Root) ([]MacroEntry, error) {
	dir, err := macros.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	var out []MacroEntry
	for _, e := range entries {
		if !e.Type().IsRegular() || !profile.MacroCandidate(e.Name()) {
			continue
		}
		m := MacroEntry{Name: e.Name()}
		if info, err := e.Info(); err == nil {
			m.Created, _ = birthTime(info)
		}
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b MacroEntry) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// listMacros returns the names of listMacroEntries.
func listMacros(macros *os.Root) ([]string, error) {
	entries, err := listMacroEntries(macros)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	return names, nil
}

// sameName returns the macro of names with the name of file, ignoring
// case and the extension, other than skip; "" when there is none.
func sameName(names []string, file, skip string) string {
	for _, n := range names {
		if n != skip && profile.SameMacroName(n, file) {
			return n
		}
	}
	return ""
}

// checkMacroText checks the text of a macro as WriteScript does a script.
func checkMacroText(data []byte) error {
	if len(data) > MaxScriptBytes {
		return fmt.Errorf("profile library: macro exceeds %d bytes", MaxScriptBytes)
	}
	if !utf8.Valid(data) {
		return errors.New("profile library: macro is not valid UTF-8")
	}
	return nil
}

// Macros lists the library's macros (listMacros); a missing macros folder
// has none. It reads without the writer lock, as Read does.
func (l *Library) Macros() ([]string, error) {
	macros, err := l.openMacroDir(false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer macros.Close()
	return listMacros(macros)
}

// MacroEntries lists the library's macros as Macros does, with when each
// file was created, sorted by the name without its extension, then by the
// file name: "a b.ts" follows "a.ts", where Macros' byte order of the files
// puts the space before the dot. A missing macros folder has none.
func (l *Library) MacroEntries() ([]MacroEntry, error) {
	macros, err := l.openMacroDir(false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer macros.Close()
	entries, err := listMacroEntries(macros)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(entries, func(a, b MacroEntry) int {
		return cmp.Or(strings.Compare(profile.MacroName(a.Name), profile.MacroName(b.Name)), strings.Compare(a.Name, b.Name))
	})
	return entries, nil
}

// updateMacros runs write with the library's macros folder, created when
// missing, and its macros, under the writer lock.
func (l *Library) updateMacros(write func(macros *os.Root, names []string) error) error {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	macros, err := l.openMacroDir(true)
	if err != nil {
		return err
	}
	defer macros.Close()
	names, err := listMacros(macros)
	if err != nil {
		return err
	}
	return write(macros, names)
}

// CreateMacro creates the macro file, a name CheckMacroFile accepts, with
// data in the library's macros folder, which it creates when missing, then
// writes the editor files types (writeMacroFiles). It refuses a name that
// another macro has (MacroExistsError, never replaceable) and a library
// with profile.MaxMacros macros (MacroLimitError). It edits no
// profile.json.
func (l *Library) CreateMacro(file string, data []byte, types []MacroFile) error {
	if err := profile.CheckMacroFile(file); err != nil {
		return err
	}
	if err := checkMacroText(data); err != nil {
		return err
	}
	return l.updateMacros(func(macros *os.Root, names []string) error {
		if e := sameName(names, file, ""); e != "" {
			return &MacroExistsError{Name: e}
		}
		if len(names) >= profile.MaxMacros {
			return &MacroLimitError{}
		}
		if err := createScript(macros, macroDirPath, file, data); err != nil {
			if exists := (*ScriptExistsError)(nil); errors.As(err, &exists) {
				return &MacroExistsError{Name: file}
			}
			return err
		}
		return writeMacroFiles(macros, types)
	})
}

// ImportMacro writes data as the macro file of the library, as CreateMacro
// does, and returns the file written and whether it replaced one. A macro
// with the same name and extension, ignoring case, is a MacroExistsError
// that is Replaceable; with replace, data replaces it atomically under its
// existing name, so the bindings that run it stay as they are. One with
// another extension is never replaced.
func (l *Library) ImportMacro(file string, data []byte, replace bool, types []MacroFile) (written string, replaced bool, err error) {
	if err := profile.CheckMacroFile(file); err != nil {
		return "", false, err
	}
	if err := checkMacroText(data); err != nil {
		return "", false, err
	}
	err = l.updateMacros(func(macros *os.Root, names []string) error {
		e := sameName(names, file, "")
		if e != "" {
			exists := &MacroExistsError{Name: e, Replaceable: strings.EqualFold(path.Ext(e), path.Ext(file))}
			if !replace || !exists.Replaceable {
				return exists
			}
		} else if len(names) >= profile.MaxMacros {
			return &MacroLimitError{}
		}
		if e != "" {
			if err := replaceRetrying(macros, e, data); err != nil {
				return err
			}
			written, replaced = e, true
		} else {
			if err := createScript(macros, macroDirPath, file, data); err != nil {
				if exists := (*ScriptExistsError)(nil); errors.As(err, &exists) {
					return &MacroExistsError{Name: file}
				}
				return err
			}
			written = file
		}
		return writeMacroFiles(macros, types)
	})
	if err != nil {
		return "", false, err
	}
	return written, replaced, nil
}

// replaceRetrying replaces name in root with data atomically, retrying
// while Windows refuses because a reader holds the old file open, as
// WriteScript does.
func replaceRetrying(root *os.Root, name string, data []byte) error {
	// The replacement keeps the creation time of the file it replaces, so
	// the library lists the macro where it was.
	var born time.Time
	if info, err := root.Lstat(name); err == nil {
		born, _ = birthTime(info)
	}
	for attempt := 1; ; attempt++ {
		err := atomicWriteBorn(root, name, data, time.Time{}, born)
		if err == nil || !errors.Is(err, fs.ErrPermission) || attempt == replaceAttempts {
			return err
		}
		time.Sleep(replaceRetry)
	}
}

// MacroPath returns the absolute path of the library's macro file, for a
// client that opens it in an editor, after writing the editor files types.
// The file must be in the macros folder under exactly its name.
func (l *Library) MacroPath(file string, types []MacroFile) (string, error) {
	err := l.updateMacros(func(macros *os.Root, names []string) error {
		if !slices.Contains(names, file) {
			return &MacroNotFoundError{File: file}
		}
		return writeMacroFiles(macros, types)
	})
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(l.MacroDir(), file))
}

// writeMacroFiles writes the editor files into the macros folder: one with
// Keep only when it is missing, the others when missing or different. A
// file or folder on their way that is a link or of another kind is an
// UnsupportedError, never followed or replaced.
func writeMacroFiles(macros *os.Root, files []MacroFile) error {
	for _, f := range files {
		dir, name := path.Split(f.Name)
		root := macros
		if dir != "" {
			sub, err := openSubdir(macros, strings.TrimSuffix(dir, "/"))
			if err != nil {
				return err
			}
			defer sub.Close()
			root = sub
		}
		info, err := root.Lstat(name)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := createScript(root, macroDirPath, name, f.Data); err != nil {
				return err
			}
			continue
		case err != nil:
			return err
		case f.Keep:
			continue
		case !info.Mode().IsRegular():
			return &UnsupportedError{Path: macroDirPath + "/" + f.Name, Reason: "is a link, folder or special file"}
		}
		old, err := readSmall(root, name, int64(len(f.Data)))
		if err != nil {
			return err
		}
		if bytes.Equal(old, f.Data) {
			continue
		}
		if err := replaceRetrying(root, name, f.Data); err != nil {
			return err
		}
	}
	return nil
}

// openSubdir opens the folder dir, slash-separated, of the macros folder
// root, creating each missing folder on the way; one that is a link or a
// file is an UnsupportedError.
func openSubdir(root *os.Root, dir string) (*os.Root, error) {
	current := root
	for i, elem := range strings.Split(dir, "/") {
		info, err := current.Lstat(elem)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := current.Mkdir(elem, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return nil, closeSub(current, root, err)
			}
		case err != nil:
			return nil, closeSub(current, root, err)
		case info.Mode().Type() != fs.ModeDir:
			p := strings.Join(strings.Split(dir, "/")[:i+1], "/")
			return nil, closeSub(current, root, &UnsupportedError{Path: macroDirPath + "/" + p, Reason: "is a link or file, not a folder"})
		}
		next, err := current.OpenRoot(elem)
		if err != nil {
			return nil, closeSub(current, root, err)
		}
		if current != root {
			_ = current.Close()
		}
		current = next
	}
	return current, nil
}

// closeSub closes current unless it is root, and returns err.
func closeSub(current, root *os.Root, err error) error {
	if current != root {
		_ = current.Close()
	}
	return err
}

// readSmall reads name, or as much of it as shows that it is longer than
// limit.
func readSmall(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit+1))
}

// editProfilesLocked edits the profile.json of every library profile with
// edit, which returns the new bytes and how many actions it changed, and
// writes each one it changed as Put does, keeping the old version in the
// history. A profile whose folder is a link, whose profile.json is
// missing, or whose syntax edit cannot read is left as it is. A profile
// that passed profile.Parse must pass it after the edit; one that did not
// is written with only edit's changes. When a write fails, the profiles
// written before it get their old bytes back and the error is returned.
// The caller holds writeMu.
func (l *Library) editProfilesLocked(edit func(id string, data []byte) ([]byte, int, error)) ([]ProfileEdit, error) {
	ids, err := l.IDs()
	if err != nil {
		return nil, err
	}
	profiles, err := os.OpenRoot(filepath.Join(l.dir, "profiles"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer profiles.Close()
	var done []ProfileEdit
	fail := func(err error) ([]ProfileEdit, error) {
		return nil, errors.Join(err, l.undoLocked(done))
	}
	for _, id := range ids {
		if checkFolder(profiles, id) != nil {
			continue
		}
		old, err := l.Read(id)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fail(err)
		}
		next, n, err := edit(id, old)
		if err != nil || n == 0 {
			continue // a syntax error: nothing can be found to edit
		}
		if len(next) > MaxDocumentBytes {
			return fail(fmt.Errorf("profile library: %s: document exceeds size limit", id))
		}
		if _, ds := l.Parse(id, old); ds.Err() == nil {
			if _, ds := l.Parse(id, next); ds.Err() != nil {
				return fail(fmt.Errorf("profile library: %s: %w", id, ds.Err()))
			}
		}
		saved, err := l.putLocked(id, Revision(old), next, false)
		if err != nil {
			return fail(fmt.Errorf("profile library: %s: %w", id, err))
		}
		done = append(done, ProfileEdit{ID: id, Bindings: n, Data: next, Old: old, Saved: saved})
	}
	return done, nil
}

// RenameMacro renames the library's macro from, which the macros folder
// lists exactly (a name CheckMacroFile refuses included), to to, which
// CheckMacroFile accepts, keeping its extension (ErrMacroExtension); a name
// another macro has is a MacroExistsError. Under the writer lock it renames
// the file, then has every action of every profile that runs from run to
// instead, in place (profile.RenameMacroRefs), saving each profile.json it
// changes as Put does (editProfilesLocked). When a save fails it restores
// the profiles saved before and renames the file back.
func (l *Library) RenameMacro(from, to string) (MacroEdit, error) {
	edit := MacroEdit{File: to}
	if err := profile.CheckMacroFile(to); err != nil {
		return edit, err
	}
	if !strings.EqualFold(path.Ext(from), path.Ext(to)) {
		return edit, ErrMacroExtension
	}
	err := l.updateMacros(func(macros *os.Root, names []string) error {
		if !slices.Contains(names, from) {
			return &MacroNotFoundError{File: from}
		}
		if e := sameName(names, to, from); e != "" {
			return &MacroExistsError{Name: e}
		}
		if from == to {
			return nil
		}
		// The rename would replace whatever is at to: a file the list does
		// not show, such as a folder, a link or a file whose 8.3 short name
		// to is, is in the way too. Only a case-only rename finds the file
		// itself there.
		if !strings.EqualFold(from, to) {
			if _, err := macros.Lstat(to); err == nil {
				return &MacroExistsError{Name: to}
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		if err := macros.Rename(from, to); err != nil {
			return err
		}
		profiles, err := l.editProfilesLocked(func(_ string, data []byte) ([]byte, int, error) {
			return profile.RenameMacroRefs(data, from, to)
		})
		if err != nil {
			if undo := macros.Rename(to, from); undo != nil {
				return errors.Join(err, fmt.Errorf("profile library: renaming the macro back: %w", undo))
			}
			return err
		}
		edit.setProfiles(profiles)
		return nil
	})
	return edit, err
}

func (e *MacroEdit) setProfiles(profiles []ProfileEdit) {
	e.Profiles = profiles
	for _, p := range profiles {
		e.Bindings += p.Bindings
	}
}

// DeleteMacro deletes the library's macro file: first every action of
// every profile that runs it, with a binding left with no action (profile.
// RemoveMacroRefs), saving each profile.json it changes as Put does, then
// the file, when the macros folder lists it exactly. A file that is not
// there only has its references removed. When a save fails, the profiles
// saved before get their old bytes back and the file stays. When the
// deletion fails, the references are gone already: the returned edit says
// what was saved, with the error.
func (l *Library) DeleteMacro(file string) (MacroEdit, error) {
	var edit MacroEdit
	if file == "" || strings.ContainsAny(file, `/\`) {
		return edit, &MacroNotFoundError{File: file}
	}
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	macros, err := l.openMacroDir(false)
	var names []string
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return edit, err
	default:
		defer macros.Close()
		if names, err = listMacros(macros); err != nil {
			return edit, err
		}
	}
	profiles, err := l.editProfilesLocked(func(_ string, data []byte) ([]byte, int, error) {
		return profile.RemoveMacroRefs(data, file)
	})
	if err != nil {
		return edit, err
	}
	edit.setProfiles(profiles)
	if !slices.Contains(names, file) {
		return edit, nil
	}
	return edit, macros.Remove(file)
}

// DetachScript removes profile id's legacy script from its profile.json:
// the "script" member and every action of type "script", with a binding
// left with no action (profile.DetachScript), saving it as Put does. The
// files stay in the folder.
func (l *Library) DetachScript(id string) (ProfileEdit, error) {
	edit := ProfileEdit{ID: id}
	err := l.UpdateFolder(id, func(*os.Root) error {
		old, err := l.Read(id)
		if err != nil {
			return err
		}
		next, n, err := profile.DetachScript(old)
		if err != nil {
			return err
		}
		edit.Saved = Saved{Revision: Revision(old)}
		if bytes.Equal(old, next) {
			return nil
		}
		if len(next) > MaxDocumentBytes {
			return errors.New("profile library: document exceeds size limit")
		}
		if _, ds := l.Parse(id, next); ds.Err() != nil {
			return ds.Err()
		}
		saved, err := l.putLocked(id, Revision(old), next, id == DefaultID)
		if err != nil {
			return err
		}
		edit.Bindings, edit.Data, edit.Saved = n, next, saved
		return nil
	})
	return edit, err
}

// ScriptPath returns the absolute path of profile id's legacy script
// entry, for a client that opens it in an editor. The entry must be a
// regular file in the profile's folder reached without a link, outside the
// saved versions: one that is absolute, leaves the folder, is in
// .history, or is or passes a link or a folder is an UnsupportedError; a
// missing entry is an EntryNotFoundError. A profile without one is
// ErrNoScript.
func (l *Library) ScriptPath(id string) (string, error) {
	doc, err := l.Load(id)
	if err != nil {
		return "", err
	}
	if doc.Profile.Script == nil {
		return "", fmt.Errorf("%w: %s", ErrNoScript, id)
	}
	entry := doc.Profile.Script.Entry
	// An inherited script (PRO-19) is in the folder of the parent that has it.
	folderID := id
	if o := doc.Profile.Script.Origin; o != "" {
		folderID = o
	}
	slashed := strings.ReplaceAll(entry, `\`, "/")
	cleaned := path.Clean(slashed)
	elems := strings.Split(cleaned, "/")
	if filepath.IsAbs(entry) || filepath.VolumeName(entry) != "" || path.IsAbs(slashed) || elems[0] == ".." || cleaned == "." || profile.IsHistoryDir(elems[0]) {
		return "", &UnsupportedError{Path: folderID + "/" + slashed, Reason: "is not a file of the profile folder"}
	}
	profiles, err := os.OpenRoot(filepath.Join(l.dir, "profiles"))
	if err != nil {
		return "", err
	}
	defer profiles.Close()
	if err := checkFolder(profiles, folderID); err != nil {
		return "", err
	}
	folder, err := profiles.OpenRoot(folderID)
	if err != nil {
		return "", err
	}
	defer folder.Close()
	for i := range elems {
		p := strings.Join(elems[:i+1], "/")
		info, err := folder.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return "", &EntryNotFoundError{ID: folderID, Entry: entry}
		}
		if err != nil {
			return "", err
		}
		last := i == len(elems)-1
		if last && !info.Mode().IsRegular() || !last && info.Mode().Type() != fs.ModeDir {
			return "", &UnsupportedError{Path: folderID + "/" + p, Reason: "is a link, folder or special file"}
		}
	}
	return filepath.Abs(filepath.Join(l.dir, "profiles", folderID, filepath.FromSlash(cleaned)))
}
