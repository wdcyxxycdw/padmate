package profilelib

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// ReferencedError refuses to delete a profile that config.json still selects.
// References are JSON pointers without the leading slash, such as
// "rules/3", so a client can remove them first.
type ReferencedError struct {
	ID         string
	References []string
}

func (e *ReferencedError) Error() string {
	return fmt.Sprintf("profile library: config.json still selects %s (%s)", e.ID, strings.Join(e.References, ", "))
}

// UnsupportedError reports a folder the library does not delete or copy: a
// symlink, junction or other reparse point, a special file, or a tree beyond
// the copy limits. Path is relative to the profiles directory.
type UnsupportedError struct {
	Path   string
	Reason string
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("profile library: %s %s", filepath.ToSlash(e.Path), e.Reason)
}

// ConfigReferences lists every config.json rule that selects id, including
// disabled rules, which would select it again once enabled. The retired
// defaultProfile member selects nothing (ADR-0053), so it is no reference.
func ConfigReferences(c *profile.Config, id string) []string {
	var refs []string
	for i, rule := range c.Rules {
		if rule.Profile != nil && *rule.Profile == id {
			refs = append(refs, "rules/"+strconv.Itoa(i))
		}
	}
	return refs
}

// IncompleteRemoveError reports that Remove took the profile out of the
// library but could not delete all of its files, for example one that
// another program holds open. They stay in a hidden folder in profiles/
// that the next Copy or Remove deletes.
type IncompleteRemoveError struct {
	ID  string
	Err error
}

func (e *IncompleteRemoveError) Error() string {
	return fmt.Sprintf("profile library: %s deleted, but some of its files remain: %v", e.ID, e.Err)
}

func (e *IncompleteRemoveError) Unwrap() error { return e.Err }

// SetRemoveHook makes Remove call hook with the hidden folder's name in
// profiles/ once the profile has left the library, before its files are
// deleted; nil removes the hook. Only tests set one, to hold a file open
// there and so make the deletion incomplete.
func (l *Library) SetRemoveHook(hook func(tmp string)) {
	l.copyMu.Lock()
	defer l.copyMu.Unlock()
	l.removeHook = hook
}

// Remove deletes profiles/<id> permanently, scripts included. The expected
// revision follows List: it must match profile.json's current bytes, and
// "" matches only a folder whose profile.json is missing or unreadable. A
// linked or special profile folder is refused, never followed; a link
// inside the folder is deleted itself, never followed (os.Root). config.json
// must not select the profile. The caller makes sure the profile is not the
// active one. The fixed default profile is never deleted (*FixedError).
// The profiles that extend this one (PRO-19, ADR-0070) are written whole
// first, the merge of their chain without extends, so that they keep what
// they inherited (RemoveFlattening).
//
// Under the writer lock the folder is renamed to a hidden temporary name,
// so it leaves the library whole or not at all: Windows refuses the rename
// while another process holds a file inside open, and then nothing has
// changed. The hidden folder is deleted after the lock is released. When
// that fails partway, Remove returns an *IncompleteRemoveError.
func (l *Library) Remove(id, expected string) error {
	_, err := l.RemoveFlattening(id, expected)
	return err
}

// RemoveFlattening is Remove that tells which profiles it wrote whole
// because they extended id: the profiles that name id in extends, each
// written as Put does with the merge of its chain and no extends, in the
// same critical section as the deletion and before it. All of them are
// written or none: a child that cannot be written whole (*ChildError: it has
// errors, or inherits a script) refuses the deletion, and so does a failed
// write; what was written is put back, also when the deletion itself fails.
// The edits are returned with an *IncompleteRemoveError too, since the
// profile is gone then.
func (l *Library) RemoveFlattening(id, expected string) ([]ProfileEdit, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("profile library: invalid id %q", id)
	}
	if id == DefaultID {
		return nil, &FixedError{ID: id, Op: "delete"}
	}
	l.copyMu.Lock()
	defer l.copyMu.Unlock()
	root, err := os.OpenRoot(filepath.Join(l.dir, "profiles"))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	tmp, flattened, err := l.unlink(root, id, expected)
	if err != nil {
		return nil, err
	}
	if l.removeHook != nil {
		l.removeHook(tmp)
	}
	if err := root.RemoveAll(tmp); err != nil {
		return flattened, &IncompleteRemoveError{ID: id, Err: err}
	}
	l.removeLeftovers(root)
	return flattened, nil
}

// unlink checks the folder, its revision and config.json, writes the
// profiles that extend it whole, and renames the folder to a hidden
// temporary name, all under the writer lock.
func (l *Library) unlink(root *os.Root, id, expected string) (string, []ProfileEdit, error) {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	info, err := root.Lstat(id)
	if err != nil {
		return "", nil, err
	}
	if info.Mode().Type() != fs.ModeDir {
		return "", nil, &UnsupportedError{Path: id, Reason: "is a link or special file, not a profile folder"}
	}
	// The revision is the one List reports: "" also stands for a
	// profile.json that cannot be read (over the size limit, a folder, a
	// link out of the library), so the broken items List keeps visible can
	// be removed too.
	current := ""
	if old, err := l.Read(id); err == nil {
		current = Revision(old)
	}
	if expected != current {
		return "", nil, &ConflictError{Current: current}
	}
	config, _, err := l.ReadConfig()
	if err != nil {
		// Without a valid config the references cannot be checked; keep the
		// cause readable without turning it into a profile diagnostic.
		return "", nil, fmt.Errorf("profile library: repair config.json before deleting a profile: %s", err.Error())
	}
	if refs := ConfigReferences(config, id); len(refs) > 0 {
		return "", nil, &ReferencedError{ID: id, References: refs}
	}
	flattened, err := l.flattenChildrenLocked(id)
	if err != nil {
		return "", nil, err
	}
	tmp := tempName(id)
	if err := renameDir(root, id, tmp); err != nil {
		return "", nil, errors.Join(err, l.undoLocked(flattened))
	}
	// A folder an import replaced and could not delete would otherwise be
	// renamed back as this profile (restoreReplaced); it is a leftover now.
	retireReplaced(root, id)
	return tmp, flattened, nil
}

const (
	renameAttempts   = 8
	renameRetryDelay = 25 * time.Millisecond
)

// renameDir retries briefly: Windows refuses to rename a folder while any
// process holds a file inside it open (a concurrent profiles.get, an editor's
// save or the search indexer), and those reads end quickly.
func renameDir(root *os.Root, oldname, newname string) error {
	var err error
	for attempt := range renameAttempts {
		if attempt > 0 {
			time.Sleep(renameRetryDelay)
		}
		err = root.Rename(oldname, newname)
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return err
}
