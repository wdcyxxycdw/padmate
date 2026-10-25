package profilelib

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Profile inheritance in the library (PRO-19, ADR-0070). A profile.json may
// name another profile of the library in "extends"; profile.Resolve merges
// the chain, reading each parent through Read, so every read is bounded and
// confined by os.Root like any other. Nothing here rewrites a file because it
// has a parent: a child keeps its own text, and the merged profile is only
// computed (Resolve), or written out on request (flattening).

// ErrInheritedScript is the reason a profile cannot be written whole: its
// script is a parent's, and the files of a script stay in the folder of the
// profile that has it.
var ErrInheritedScript = errors.New("it inherits the script of a parent, whose files stay in the parent's folder; give the profile a script of its own first")

// ChildError reports that a profile which extends another cannot be written
// whole, which deleting the parent or exporting the child needs: it has
// errors itself or in a parent, or it inherits a script (ErrInheritedScript).
type ChildError struct {
	ID  string
	Err error
}

func (e *ChildError) Error() string {
	return fmt.Sprintf("profile library: %s cannot be written whole: %v", e.ID, e.Err)
}

func (e *ChildError) Unwrap() error { return e.Err }

// Resolve is profile.Resolve for profile id of the library, whose text is
// data: its extends is resolved with the library's profiles. The id need not
// be in the library yet, as a profile that is about to be saved.
func (l *Library) Resolve(id string, data []byte) (*profile.Resolved, profile.Diagnostics) {
	return profile.Resolve(data, &profile.Library{ID: id, Load: l.Read})
}

// Parse is profile.Parse for profile id of the library, whose text is data:
// its extends is resolved with the library's profiles. The diagnostics may be
// in a parent's file (Diagnostics.Local).
func (l *Library) Parse(id string, data []byte) (*profile.Profile, profile.Diagnostics) {
	r, ds := l.Resolve(id, data)
	if r == nil {
		return nil, ds
	}
	return r.Profile, ds
}

// Children lists the profiles whose profile.json names id in extends, sorted
// by ID. A profile that cannot be read is not one. A folder that is a link
// is not read.
func (l *Library) Children(id string) ([]string, error) {
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
	return l.childrenIn(profiles, ids, id), nil
}

func (l *Library) childrenIn(profiles *os.Root, ids []string, id string) []string {
	var children []string
	for _, cid := range ids {
		if cid == id || checkFolder(profiles, cid) != nil {
			continue
		}
		if data, err := l.Read(cid); err == nil && profile.ExtendsOf(data) == id {
			children = append(children, cid)
		}
	}
	return children
}

// Parents follows the extends of profile id as far as the library goes and
// returns the IDs it passes, nearest first, without checking anything else:
// it stops at a profile that is not there or cannot be read, at one that
// extends no one, at a loop, and after profile.MaxExtendsDepth parents. It is
// what a client lists as the chain of a profile that is perhaps broken.
func (l *Library) Parents(id string) []string {
	var chain []string
	seen := []string{id}
	for len(chain) < profile.MaxExtendsDepth {
		data, err := l.Read(id)
		if err != nil {
			break
		}
		parent := profile.ExtendsOf(data)
		if parent == "" || slices.Contains(seen, parent) || !ValidID(parent) {
			break
		}
		chain, seen = append(chain, parent), append(seen, parent)
		id = parent
	}
	return chain
}

// flatten returns profile id written whole, the merge of its chain without
// extends (profile.Resolved.Merged), for the text old of its profile.json.
// A profile that does not extend is returned as it is. It is a *ChildError if
// the profile has errors, or inherits a script.
func (l *Library) flatten(id string, old []byte) ([]byte, error) {
	r, ds := l.Resolve(id, old)
	if r == nil {
		return nil, &ChildError{ID: id, Err: ds.Err()}
	}
	if r.Merged == nil {
		return old, nil
	}
	if s := r.Profile.Script; s != nil && s.Origin != "" {
		return nil, &ChildError{ID: id, Err: fmt.Errorf("%w (%s)", ErrInheritedScript, s.Origin)}
	}
	return r.Merged, nil
}

// flattenChildrenLocked writes every profile whose profile.json extends id as
// the merge of its chain, without extends, so that it survives the deletion
// of id: each one as Put does, the old version kept in its history. Every
// one is merged and checked before any is written, so that a child that
// cannot be written whole (*ChildError) changes nothing, and nothing that is
// watched flickers; when a write fails, the ones written before are put back
// as they were. A profile whose profile.json cannot be read is not a child
// (Children lists none either). The returned edits have the old bytes, for
// undoLocked. The caller holds writeMu.
func (l *Library) flattenChildrenLocked(id string) ([]ProfileEdit, error) {
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
	var planned []ProfileEdit
	for _, cid := range ids {
		if cid == id || checkFolder(profiles, cid) != nil {
			continue
		}
		// A profile.json that cannot be read (over the size limit, a folder,
		// held open by a program) cannot be shown to extend id, and is not
		// one that loads anyway, so it is not a child, as in Children; it
		// must not keep an unrelated profile from being deleted.
		old, err := l.Read(cid)
		if err != nil || profile.ExtendsOf(old) != id {
			continue
		}
		flat, err := l.flatten(cid, old)
		if err != nil {
			return nil, err
		}
		if len(flat) > MaxDocumentBytes {
			return nil, &ChildError{ID: cid, Err: errors.New("the document would exceed the size limit")}
		}
		planned = append(planned, ProfileEdit{ID: cid, Bindings: 1, Data: flat, Old: old})
	}
	var done []ProfileEdit
	for _, edit := range planned {
		saved, err := l.putLocked(edit.ID, Revision(edit.Old), edit.Data, false)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("profile library: %s: %w", edit.ID, err), l.undoLocked(done))
		}
		edit.Saved = saved
		done = append(done, edit)
	}
	return done, nil
}

// undoLocked puts the profiles of done back as they were, last first, and
// returns what it could not put back. The history of each is left as it was
// before its write: the text it replaces is not one the user saved, so it
// is not kept, and the snapshot of the old text that the write kept is
// dropped (a snapshot of the oldest version that it pruned is not brought
// back). The caller holds writeMu.
func (l *Library) undoLocked(done []ProfileEdit) error {
	var errs []error
	for i := len(done) - 1; i >= 0; i-- {
		edit := done[i]
		if _, err := l.writeLocked(edit.ID, edit.Saved.Revision, edit.Old, false, false); err != nil {
			errs = append(errs, fmt.Errorf("profile library: restoring %s: %w", edit.ID, err))
			continue
		}
		l.dropSnapshot(edit.ID, edit.Saved.snapshot)
	}
	return errors.Join(errs...)
}
