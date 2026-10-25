package profilelib

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// How Import treats an archive's macro whose name the library has with
// other bytes (ImportOptions.Macros).
const (
	// MacrosAsk refuses the import with a *MacroConflictError, before
	// anything is written, so the client can ask the user.
	MacrosAsk = ""
	// MacrosReplace writes the archive's bytes over the library's macro,
	// for every profile that runs it. A macro with another extension is
	// never replaced: it is kept as with MacrosKeep.
	MacrosReplace = "replace"
	// MacrosKeep keeps both: the archive's macro takes the first free name
	// with a number (numberedMacro) and the imported profile runs it.
	MacrosKeep = "keep"
)

// MacroConflict is a macro of an archive whose name the library has with
// other bytes.
type MacroConflict struct {
	// File is the macro's name in the archive.
	File string
	// Existing is the library's macro with that name, ignoring case and
	// the extension.
	Existing string
	// Replaceable reports that Existing has the same extension, so that
	// MacrosReplace replaces it.
	Replaceable bool
}

// MacroConflictError refuses an import with MacrosAsk whose macros the
// library has under the same names with other bytes.
type MacroConflictError struct {
	Conflicts []MacroConflict
}

func (e *MacroConflictError) Error() string {
	names := make([]string, len(e.Conflicts))
	for i, c := range e.Conflicts {
		names[i] = c.File
	}
	return fmt.Sprintf("profile library: the library has other macros named %s", strings.Join(names, ", "))
}

// ImportedMacro is a macro of an archive that Import put into the library,
// or found there.
type ImportedMacro struct {
	// File is its name in the archive, Name in the library.
	File, Name string
	// Same reports that the library had it with the same bytes; Replaced
	// that its bytes replaced the library's (MacrosReplace).
	Same, Replaced bool
}

// macroWrite is a file an import writes into the library's macros folder.
type macroWrite struct {
	name    string // slash-separated, relative to the macros folder
	data    []byte
	replace bool // a macro whose bytes it replaces
	module  bool // not a macro: written only when the path is free
}

// importPlan is what an import does with an archive's macros folder.
type importPlan struct {
	renames   map[string]string // the profile's references to rename
	writes    []macroWrite
	macros    []ImportedMacro
	conflicts []MacroConflict
}

// splitArchive divides an archive's items into those of the profile
// folder, the macros at the top of its macros folder and the other files
// there, named relative to the macros folder. A macros folder is the
// library's now: its folders are not written into the profile.
func splitArchive(items []copyItem) (folder, macros, modules []copyItem) {
	for _, item := range items {
		first, rest, _ := strings.Cut(item.path, "/")
		switch {
		case !strings.EqualFold(first, profile.MacroDir):
			folder = append(folder, item)
		case item.dir || rest == "":
		case !strings.Contains(rest, "/") && profile.MacroCandidate(rest):
			macros = append(macros, copyItem{path: rest, data: item.data})
		default:
			modules = append(modules, copyItem{path: rest, data: item.data})
		}
	}
	return folder, macros, modules
}

// planImport plans the import of an archive's macros and modules into the
// library with the choice for conflicts (MacrosAsk and the others): a
// macro the library lacks is written under its name; one the library has
// with the same extension and bytes, also under a numbered name, is used
// as it is; for another, the library's same name with other bytes is a
// conflict, which choice settles. Two macros of the archive with one name
// keep both without asking. A module is written only where the library
// has nothing. The caller holds the writer lock.
func (l *Library) planImport(macros, modules []copyItem, choice string) (*importPlan, error) {
	library, err := newLibraryFiles(l, l.reservedNames(""))
	if err != nil {
		return nil, err
	}
	defer library.close()
	plan := &importPlan{renames: map[string]string{}}
	for _, item := range macros {
		file, data := item.path, item.data
		to, same, n, err := library.target(file, data)
		if err != nil {
			return nil, err
		}
		imported := ImportedMacro{File: file, Name: to, Same: same}
		existing := sameName(library.names, file, "")
		_, own := library.planned[existing]
		switch {
		case same:
		case n == 1 || own || existing == "":
			library.add(to, data)
			plan.writes = append(plan.writes, macroWrite{name: to, data: data})
		default:
			conflict := MacroConflict{File: file, Existing: existing, Replaceable: strings.EqualFold(path.Ext(existing), path.Ext(file))}
			switch {
			case choice == MacrosAsk:
				plan.conflicts = append(plan.conflicts, conflict)
				continue
			case choice == MacrosReplace && conflict.Replaceable:
				to = existing
				imported = ImportedMacro{File: file, Name: to, Replaced: true}
				library.planned[to] = data
				plan.writes = append(plan.writes, macroWrite{name: to, data: data, replace: true})
			default:
				library.add(to, data)
				plan.writes = append(plan.writes, macroWrite{name: to, data: data})
			}
		}
		if to != file {
			plan.renames[file] = to
		}
		plan.macros = append(plan.macros, imported)
	}
	for _, item := range modules {
		if library.macros != nil {
			if _, err := library.macros.Lstat(filepath.FromSlash(item.path)); !errors.Is(err, fs.ErrNotExist) {
				continue
			}
		}
		plan.writes = append(plan.writes, macroWrite{name: item.path, data: item.data, module: true})
	}
	return plan, nil
}

// sameRenames reports whether two plans rename the same references.
func sameRenames(a, b *importPlan) bool { return maps.Equal(a.renames, b.renames) }

// writeImport writes the plan's files into the library's macros folder and
// returns how to undo it. A new macro whose name is taken now, or a module
// whose folder is a link, is an error; a module whose path is taken is
// skipped. The caller holds the writer lock.
func (l *Library) writeImport(plan *importPlan) (undo func() error, err error) {
	var created []string
	var replaced []macroWrite // the old bytes
	var macros *os.Root
	undo = func() error {
		if macros == nil {
			return nil
		}
		defer macros.Close()
		var errs []error
		for i := len(created) - 1; i >= 0; i-- {
			if err := macros.Remove(filepath.FromSlash(created[i])); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
		}
		for _, w := range replaced {
			if err := replaceRetrying(macros, w.name, w.data); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}
	if len(plan.writes) == 0 {
		return undo, nil
	}
	macros, err = l.openMacroDir(true)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (func() error, error) {
		if u := undo(); u != nil {
			err = errors.Join(err, u)
		}
		return nil, err
	}
	for _, w := range plan.writes {
		name := filepath.FromSlash(w.name)
		if dir, _ := path.Split(w.name); dir != "" {
			sub, err := openSubdir(macros, strings.TrimSuffix(dir, "/"))
			if err != nil {
				return fail(err)
			}
			_ = sub.Close()
		}
		if w.replace {
			old, err := readSmall(macros, name, maxMacroFileBytes)
			if err != nil {
				return fail(err)
			}
			if err := replaceRetrying(macros, name, w.data); err != nil {
				return fail(err)
			}
			replaced = append(replaced, macroWrite{name: name, data: old})
			continue
		}
		if _, err := macros.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
			if w.module && err == nil {
				continue
			}
			return fail(&MacroExistsError{Name: w.name})
		}
		if err := atomicReplace(macros, name, w.data); err != nil {
			return fail(err)
		}
		created = append(created, w.name)
	}
	return undo, nil
}

// conflictsOf returns the plan's conflicts as an error, or nil.
func conflictsOf(plan *importPlan) error {
	if len(plan.conflicts) == 0 {
		return nil
	}
	return &MacroConflictError{Conflicts: slices.Clone(plan.conflicts)}
}
