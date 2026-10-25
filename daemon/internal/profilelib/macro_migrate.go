package profilelib

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// MigrationJournalPrefix starts the name of the file in the data folder,
// macros-migration-<profile ID>.json, that MigrateMacros writes before it
// changes a profile, and deletes when the profile is done: a migration cut
// off in between finishes it first on the next start. Each profile has a
// journal of its own, so that one that could not finish is never
// overwritten by the plan of another.
const MigrationJournalPrefix = "macros-migration-"

// journalName is the journal file of profile id.
func journalName(id string) string { return MigrationJournalPrefix + id + ".json" }

// maxMacroFileBytes bounds a file that MigrateMacros or an import moves
// into the library: larger ones stay where they are.
const maxMacroFileBytes = MaxCopyBytes

// maxNumber bounds the numbers MigrateMacros and Import try for a name.
const maxNumber = 1000

// MigratedMacro is a file MigrateMacros took from a profile's macros
// folder.
type MigratedMacro struct {
	Profile string
	// From is the file's name in the profile's macros folder, relative to
	// it with slashes; To its name in the library's.
	From, To string
	// Same reports that the library had the file already, with the same
	// bytes: the profile's copy was deleted.
	Same bool
}

// MacroMigration says what MigrateMacros did.
type MacroMigration struct {
	Moved []MigratedMacro
	// Renamed counts the actions whose macro got another name, by profile.
	Renamed map[string]int
	// Left lists the files and folders that stayed in a profile's macros
	// folder, relative to profiles/ with slashes: a link, a special or too
	// large file, or a module whose path the library has with other bytes.
	Left []string
	// Shadowed lists, among Left, the modules (not tsconfig.json) that
	// stayed because the library has another file at their path: the
	// profile's macros that import one now run the library's file, as a
	// macro's imports are not rewritten.
	Shadowed []string
	// Errs are the profiles that could not be migrated, or not entirely;
	// the others were.
	Errs []error
}

// journal is MigrationJournal: the plan for one profile.
type journal struct {
	Profile string `json:"profile"`
	// Revision is profile.json's before the migration, and Data its bytes
	// after, when references change.
	Revision string        `json:"revision,omitempty"`
	Data     []byte        `json:"data,omitempty"`
	Files    []journalFile `json:"files"`
	// refsErr says why the references could not be renamed.
	refsErr error
}

type journalFile struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// MigrateMacros moves each profile's macros folder, from before the script
// library (SCR-50), into the library's (SCR-51), one profile at a time:
//
//   - A macro whose name the library does not have moves under its name;
//     one the library has with the same extension and bytes is deleted
//     (Same), as is one the same as a numbered name ("a 2.ts"); otherwise
//     it takes the first free name with " 2", " 3"… (numberedMacro) and
//     the profile's actions that run it are renamed, in place
//     (profile.RenameMacroRefsAll), saving profile.json as Put does. A
//     macro that needs another name stays while profile.json cannot be
//     read or written: under its old name the profile would run another
//     macro of the library. The next call tries again.
//   - Another file, such as a module in a subfolder or tsconfig.json,
//     moves to the same path unless the library has one there: the same
//     bytes delete it, other bytes leave it (Left). The generated .padmate
//     folder is deleted.
//   - A link, a special file or one beyond maxMacroFileBytes stays.
//   - The emptied macros folder is removed.
//
// Before it changes a profile it writes the profile's journal
// (MigrationJournalPrefix); a migration that a crash cut off finishes that
// profile from it on the next call, so the profile's references and the
// library's names stay together. Each step can run twice: a moved file is
// written whole (atomicWrite) before its source is deleted. A profile that
// fails is in Errs; the others go on. A journal stays only for a profile
// whose references were renamed already but whose files were not all
// moved, which no new plan can repair: the plans of the other profiles
// keep away from its names (reservedNames), and the next call finishes it.
// A name a journal planned that another file has taken since stays an
// error, with the macro in its profile's folder.
// MigrateMacros holds copyMu and the writer lock. A library without
// per-profile macros folders is not touched: it is idempotent.
func (l *Library) MigrateMacros() (MacroMigration, error) {
	var m MacroMigration
	l.copyMu.Lock()
	defer l.copyMu.Unlock()
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	kept := map[string]bool{} // profiles whose journal stays
	journals, err := l.journalIDs()
	if err != nil {
		return m, err
	}
	for _, id := range journals {
		j, err := l.readJournal(id)
		if err != nil {
			m.Errs = append(m.Errs, err)
			if err := l.removeJournal(id); err != nil {
				return m, err
			}
			continue
		}
		if err := l.runJournal(&m, j); err != nil {
			m.Errs = append(m.Errs, err)
		}
		kept[id] = l.hasJournal(id)
	}
	ids, err := l.IDs()
	if err != nil {
		return m, err
	}
	profiles, err := os.OpenRoot(filepath.Join(l.dir, "profiles"))
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	defer profiles.Close()
	for _, id := range ids {
		if kept[id] || checkFolder(profiles, id) != nil {
			continue
		}
		info, err := profiles.Lstat(filepath.Join(id, profile.MacroDir))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			m.Errs = append(m.Errs, fmt.Errorf("profile library: %s: %w", id, err))
			continue
		case info.Mode().Type() != fs.ModeDir:
			m.Left = append(m.Left, id+"/"+profile.MacroDir)
			continue
		}
		j, err := l.planMigration(id)
		if err == nil && j.refsErr != nil {
			m.Errs = append(m.Errs, j.refsErr)
		}
		if err == nil {
			err = l.writeJournal(j)
		}
		if err == nil {
			err = l.runJournal(&m, j)
		}
		if err != nil {
			m.Errs = append(m.Errs, err)
		}
	}
	return m, nil
}

// maxJournalBytes bounds a migration journal: profile.json in base64 and
// the names of at most a folder's files.
const maxJournalBytes = 2*MaxDocumentBytes + 1<<20

// journalIDs lists the profiles that have a journal, sorted.
func (l *Library) journalIDs() ([]string, error) {
	entries, err := os.ReadDir(l.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		name, ok := strings.CutPrefix(e.Name(), MigrationJournalPrefix)
		if !ok || e.IsDir() {
			continue
		}
		if id, ok := strings.CutSuffix(name, ".json"); ok && ValidID(id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

// hasJournal reports whether profile id has a journal.
func (l *Library) hasJournal(id string) bool {
	_, err := os.Lstat(filepath.Join(l.dir, journalName(id)))
	return err == nil
}

// readJournal reads the journal of profile id.
func (l *Library) readJournal(id string) (*journal, error) {
	root, err := os.OpenRoot(l.dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(journalName(id))
	if err != nil {
		return nil, err
	}
	data, err := readAllLimited(f, maxJournalBytes)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	var j journal
	if err := json.Unmarshal(data, &j); err != nil || len(data) > maxJournalBytes || j.Profile != id {
		return nil, fmt.Errorf("profile library: %s is damaged; the migration it described is planned again", journalName(id))
	}
	return &j, nil
}

func (l *Library) writeJournal(j *journal) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(l.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return atomicReplace(root, journalName(j.Profile), data)
}

func (l *Library) removeJournal(id string) error {
	root, err := os.OpenRoot(l.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(journalName(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// reservedNames returns the library names that the journals of the profiles
// other than id plan to give: files that are not in the library yet but
// whose references already are. A plan keeps away from them.
func (l *Library) reservedNames(id string) []string {
	ids, err := l.journalIDs()
	if err != nil {
		return nil
	}
	var names []string
	for _, other := range ids {
		if other == id {
			continue
		}
		if j, err := l.readJournal(other); err == nil {
			for _, f := range j.Files {
				names = append(names, f.To)
			}
		}
	}
	return names
}

// libraryFiles reads the files of the library's macros folder by name, as
// a plan needs them, and remembers the ones the plan adds.
type libraryFiles struct {
	macros  *os.Root // nil while the library has no macros folder
	names   []string // listMacros, then the plan's new names
	planned map[string][]byte
	read    map[string][]byte
	// reserved are names that journals of other profiles plan to give
	// (reservedNames): no plan takes them, whatever the library has.
	reserved []string
}

func newLibraryFiles(l *Library, reserved []string) (*libraryFiles, error) {
	f := &libraryFiles{planned: map[string][]byte{}, read: map[string][]byte{}, reserved: reserved}
	macros, err := l.openMacroDir(false)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	f.macros = macros
	if f.names, err = listMacros(macros); err != nil {
		_ = macros.Close()
		return nil, err
	}
	return f, nil
}

func (f *libraryFiles) close() {
	if f.macros != nil {
		_ = f.macros.Close()
	}
}

// bytes returns the bytes of the library's file name, or nil when they
// cannot be read.
func (f *libraryFiles) bytes(name string) []byte {
	if data, ok := f.planned[name]; ok {
		return data
	}
	if data, ok := f.read[name]; ok {
		return data
	}
	var data []byte
	if f.macros != nil {
		data, _ = readSmall(f.macros, name, maxMacroFileBytes)
	}
	f.read[name] = data
	return data
}

// taken reports whether a file that the list does not show is at name: a
// folder, a link, or a file whose 8.3 short name name is.
func (f *libraryFiles) taken(name string) bool {
	if _, ok := f.planned[name]; ok {
		return true
	}
	if f.macros == nil {
		return false
	}
	_, err := f.macros.Lstat(name)
	return !errors.Is(err, fs.ErrNotExist)
}

// target returns the library's name for the macro file with data: the
// first of file, "<name> 2<ext>", "<name> 3<ext>"… (numberedMacro) that
// the library has with the same extension and bytes (same), or else the
// first free one. n is the number: 1 for file itself.
func (f *libraryFiles) target(file string, data []byte) (name string, same bool, n int, err error) {
	for n = 1; n <= maxNumber; n++ {
		candidate := numberedMacro(file, n)
		if sameName(f.reserved, candidate, "") != "" {
			continue
		}
		if existing := sameName(f.names, candidate, ""); existing != "" {
			if strings.EqualFold(path.Ext(existing), path.Ext(file)) {
				if old := f.bytes(existing); old != nil && bytes.Equal(old, data) {
					return existing, true, n, nil
				}
			}
			continue
		}
		if !f.taken(candidate) {
			return candidate, false, n, nil
		}
	}
	return "", false, 0, fmt.Errorf("profile library: no free name for the macro %q", file)
}

// add records name, with data, as a file the plan adds to the library.
func (f *libraryFiles) add(name string, data []byte) {
	f.names = append(f.names, name)
	f.planned[name] = data
}

// numberedMacro returns the macro file name file with the number n after
// its name, "<name> <n><ext>", shortened so that a name of at most
// profile.MaxMacroName characters stays within it; n 1 is file itself.
func numberedMacro(file string, n int) string {
	if n == 1 {
		return file
	}
	name := profile.MacroName(file)
	ext := file[len(name):]
	suffix := " " + strconv.Itoa(n)
	if room := profile.MaxMacroName - len(suffix); utf8.RuneCountInString(name) > room {
		name = string([]rune(name)[:max(0, room)])
	}
	return strings.TrimRight(name, " ") + suffix + ext
}

// planMigration plans the migration of profile id's macros folder: the
// macros it moves, under which names, and its profile.json with the
// references renamed. The caller holds the writer lock.
func (l *Library) planMigration(id string) (*journal, error) {
	j := &journal{Profile: id, Files: []journalFile{}}
	source, err := l.openProfileMacros(id)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	names, err := listMacros(source)
	if err != nil {
		return nil, fmt.Errorf("profile library: %s: %w", id, err)
	}
	library, err := newLibraryFiles(l, l.reservedNames(id))
	if err != nil {
		return nil, err
	}
	defer library.close()
	renames := map[string]string{}
	for _, name := range names {
		data, err := readSmall(source, name, maxMacroFileBytes)
		if err != nil || len(data) > maxMacroFileBytes {
			continue // runJournal leaves it
		}
		to, same, _, err := library.target(name, data)
		if err != nil {
			return nil, fmt.Errorf("profile library: %s: %w", id, err)
		}
		if !same {
			library.add(to, data)
		}
		if to != name {
			renames[name] = to
		}
		j.Files = append(j.Files, journalFile{From: name, To: to})
	}
	if len(renames) == 0 {
		return j, nil
	}
	old, err := l.Read(id)
	if errors.Is(err, fs.ErrNotExist) {
		return j, nil
	}
	if err != nil {
		return nil, fmt.Errorf("profile library: %s: %w", id, err)
	}
	next, n, err := profile.RenameMacroRefsAll(old, renames)
	if err != nil {
		// The macros that need another name stay until profile.json can
		// be read; the others move.
		j.refsErr = fmt.Errorf("profile library: %s: profile.json cannot be read, so its macros that need another name stay in its macros folder: %w", id, err)
		j.Files = slices.DeleteFunc(j.Files, func(f journalFile) bool { return f.From != f.To })
		return j, nil
	}
	if n > 0 {
		j.Revision, j.Data = Revision(old), next
	}
	return j, nil
}

// openProfileMacros opens profile id's macros folder, a folder, never a
// link.
func (l *Library) openProfileMacros(id string) (*os.Root, error) {
	profiles, err := os.OpenRoot(filepath.Join(l.dir, "profiles"))
	if err != nil {
		return nil, err
	}
	defer profiles.Close()
	if err := checkFolder(profiles, id); err != nil {
		return nil, err
	}
	folder, err := profiles.OpenRoot(id)
	if err != nil {
		return nil, err
	}
	defer folder.Close()
	return openMacros(folder, id+"/"+profile.MacroDir, false)
}

// runJournal carries out the plan j: profile.json first, unless it has
// changed since the plan, then the macros, then the folder's other files.
// When profile.json was not written, the macros that need another name
// stay, for a later plan. It deletes the journal when it is done, and also
// when it failed before profile.json was changed (a new plan is as good).
// With profile.json at the journal's bytes the journal is all that says
// which names its references mean: it stays after a failure, for the next
// call. The caller holds the writer lock.
func (l *Library) runJournal(m *MacroMigration, j *journal) error {
	id := j.Profile
	var errs []error
	keepRenamed := false
	applied := false // profile.json is at the journal's bytes
	if j.Data != nil {
		old, err := l.Read(id)
		switch {
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			errs, keepRenamed = append(errs, err), true
		case err == nil && Revision(old) == j.Revision:
			if _, err := l.putLocked(id, j.Revision, j.Data, false); err != nil {
				errs, keepRenamed = append(errs, err), true
			} else {
				applied = true
				if m.Renamed == nil {
					m.Renamed = map[string]int{}
				}
				_, n, _ := profile.RenameMacroRefsAll(old, renamesOf(j))
				m.Renamed[id] += n
			}
		case err == nil && Revision(old) == Revision(j.Data):
			applied = true // an earlier run wrote it
		case err == nil:
			errs, keepRenamed = append(errs, fmt.Errorf("profile library: %s changed since its macros were planned; they are planned again", id)), true
		}
	}
	finish := func(errs []error) error {
		if err := errors.Join(errs...); err != nil {
			err = fmt.Errorf("profile library: migrating the macros of %s: %w", id, err)
			if applied {
				return err
			}
			return errors.Join(err, l.removeJournal(id))
		}
		return l.removeJournal(id)
	}
	source, err := l.openProfileMacros(id)
	if errors.Is(err, fs.ErrNotExist) {
		return errors.Join(append(errs, l.removeJournal(id))...)
	}
	if err != nil {
		return finish(append(errs, err))
	}
	library, err := l.openMacroDir(true)
	if err != nil {
		_ = source.Close()
		return finish(append(errs, err))
	}
	for _, f := range j.Files {
		if keepRenamed && f.From != f.To {
			continue
		}
		var moved, same bool
		var err error
		if l.moveHook != nil {
			err = l.moveHook(id, f.From)
		}
		if err == nil {
			moved, same, err = moveFile(source, library, f.From, f.To)
		}
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("profile library: %s/%s/%s to %s: %w", id, profile.MacroDir, f.From, f.To, err))
		case moved:
			m.Moved = append(m.Moved, MigratedMacro{Profile: id, From: f.From, To: f.To, Same: same})
		}
	}
	if err := l.moveModules(m, id, source, library); err != nil {
		errs = append(errs, err)
	}
	_ = library.Close()
	_ = source.Close()
	if err := l.removeEmptyMacros(m, id); err != nil {
		errs = append(errs, err)
	}
	return finish(errs)
}

func renamesOf(j *journal) map[string]string {
	renames := map[string]string{}
	for _, f := range j.Files {
		if f.From != f.To {
			renames[f.From] = f.To
		}
	}
	return renames
}

// errTargetTaken is moveFile's error for a name that the library has with
// other bytes.
var errTargetTaken = errors.New("the library has another file with that name; the macro stays in the profile's macros folder")

// moveFile moves the regular file from of source to the name to of
// library, a path with slashes whose folders exist: a missing to gets its
// bytes, written whole (atomicWrite), and an existing one with the same
// bytes only deletes from. moved is false when from is not there any more,
// a step done already; a from that is not a regular file or too large
// stays, and is no error. A to that holds other bytes, or is not a regular
// file, is errTargetTaken: from stays.
func moveFile(source, library *os.Root, from, to string) (moved, same bool, err error) {
	info, err := source.Lstat(filepath.FromSlash(from))
	if errors.Is(err, fs.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxMacroFileBytes {
		return false, false, nil
	}
	data, err := readSmall(source, filepath.FromSlash(from), maxMacroFileBytes)
	if err != nil {
		return false, false, err
	}
	if len(data) > maxMacroFileBytes {
		return false, false, nil
	}
	target := filepath.FromSlash(to)
	existing, err := library.Lstat(target)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := atomicWrite(library, target, data, info.ModTime()); err != nil {
			return false, false, err
		}
	case err != nil:
		return false, false, err
	case !existing.Mode().IsRegular():
		return false, false, errTargetTaken
	default:
		old, err := readSmall(library, target, int64(len(data)))
		if err != nil {
			return false, false, err
		}
		if !bytes.Equal(old, data) {
			return false, false, errTargetTaken
		}
		same = true
	}
	return true, same, source.Remove(filepath.FromSlash(from))
}

// moveModules moves the files of profile id's macros folder source that
// are not macros, such as modules in subfolders and tsconfig.json, to the
// same paths in library, as moveFile does, and deletes the generated
// .padmate folder; what stays is in m.Left.
func (l *Library) moveModules(m *MacroMigration, id string, source, library *os.Root) error {
	var files []string
	err := fs.WalkDir(source.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		if d.IsDir() && !strings.Contains(p, "/") && strings.EqualFold(d.Name(), profile.MacroTypesDir) {
			return fs.SkipDir
		}
		info, err := source.Lstat(filepath.FromSlash(p))
		if err != nil {
			return err
		}
		switch {
		case info.Mode().Type() == fs.ModeDir && d.IsDir():
			if strings.Count(p, "/")+1 > MaxCopyDepth {
				return fs.SkipDir
			}
		case info.Mode().IsRegular() && d.Type().IsRegular():
			if strings.Contains(p, "/") || !profile.MacroCandidate(p) {
				files = append(files, p)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range files {
		dir, _ := path.Split(p)
		if dir != "" {
			sub, err := openSubdir(library, strings.TrimSuffix(dir, "/"))
			if err != nil {
				var unsupported *UnsupportedError
				if !errors.As(err, &unsupported) {
					errs = append(errs, err)
				}
				continue
			}
			_ = sub.Close()
		}
		moved, same, err := moveFile(source, library, p, p)
		switch {
		case errors.Is(err, errTargetTaken):
			// It stays (Left); macros that import a module are not
			// rewritten, so they run the library's file.
			if importable(p) {
				m.Shadowed = append(m.Shadowed, id+"/"+profile.MacroDir+"/"+p)
			}
		case err != nil:
			errs = append(errs, err)
		case moved:
			m.Moved = append(m.Moved, MigratedMacro{Profile: id, From: p, To: p, Same: same})
		}
	}
	if info, err := source.Lstat(profile.MacroTypesDir); err == nil && info.Mode().Type() == fs.ModeDir {
		if err := source.RemoveAll(profile.MacroTypesDir); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// importable reports whether p, a file of a macros folder, may be imported
// by a macro: a script or JSON file, not tsconfig.json.
func importable(p string) bool {
	if strings.EqualFold(p, "tsconfig.json") {
		return false
	}
	switch strings.ToLower(path.Ext(p)) {
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs", ".json":
		return true
	}
	return false
}

// removeEmptyMacros removes the folders of profile id's macros folder that
// are empty now, deepest first, then the folder itself when it is empty,
// and lists what stays in m.Left.
func (l *Library) removeEmptyMacros(m *MacroMigration, id string) error {
	profiles, err := os.OpenRoot(filepath.Join(l.dir, "profiles"))
	if err != nil {
		return err
	}
	defer profiles.Close()
	folder, err := profiles.OpenRoot(id)
	if err != nil {
		return err
	}
	defer folder.Close()
	source, err := openMacros(folder, id+"/"+profile.MacroDir, false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var dirs, left []string
	err = fs.WalkDir(source.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		info, err := source.Lstat(filepath.FromSlash(p))
		if err != nil {
			return err
		}
		if info.Mode().Type() == fs.ModeDir && d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		left = append(left, id+"/"+profile.MacroDir+"/"+p)
		return nil
	})
	if err != nil {
		_ = source.Close()
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = source.Remove(filepath.FromSlash(dirs[i])) // fails while not empty
	}
	_ = source.Close()
	m.Left = append(m.Left, left...)
	if len(left) == 0 {
		if err := folder.Remove(profile.MacroDir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// readAllLimited reads r, at most limit bytes and one more to show that it
// is longer.
func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, limit+1))
}
