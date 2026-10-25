package profilelib

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// DefaultID is the library ID of the fixed default profile (PRO-28,
// ADR-0053), the profile of every application no rule binds. An existing
// profiles/default folder is the default as it is.
const DefaultID = profile.DefaultProfileID

// DefaultProfileText is the profile.json EnsureDefault writes: no mapping, at
// the current schema version. Clients show the default by its ID, whatever
// name it stores. A default of version 0 that is already there stays as it is.
const DefaultProfileText = `{"schemaVersion":1,"name":"默认","layers":{"base":{}}}` + "\n"

// FixedError refuses an operation the fixed default profile does not
// allow: Op is "delete" or "rename".
type FixedError struct {
	ID string
	Op string
}

func (e *FixedError) Error() string {
	switch e.Op {
	case "rename":
		return fmt.Sprintf("profile library: %s is the fixed default profile and cannot be renamed", e.ID)
	default:
		return fmt.Sprintf("profile library: %s is the fixed default profile and cannot be deleted", e.ID)
	}
}

// EnsureDefault writes DefaultProfileText as the default's profile.json when
// that file is missing, and reports whether it did. A profile.json already
// there is kept as it is, even when it does not parse; one that cannot be
// read for another reason is an error. First it renames back the folders
// of profiles that an import was cut off while replacing (Import), so a
// replaced default is not mistaken for a missing one.
func (l *Library) EnsureDefault() (bool, error) {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	if root, err := os.OpenRoot(filepath.Join(l.dir, "profiles")); err == nil {
		restoreReplaced(root)
		_ = root.Close()
	}
	_, err := l.putLocked(DefaultID, "", []byte(DefaultProfileText), false)
	var conflict *ConflictError
	if errors.As(err, &conflict) {
		return false, nil
	}
	return err == nil, err
}

// renames reports whether writing data over old, the current
// profile.json, changes its name. A current file that does not parse has
// no name to keep, so it can be repaired with any.
func (l *Library) renames(id string, old, data []byte) bool {
	before, ds := l.Parse(id, old)
	if ds.Err() != nil || before == nil {
		return false
	}
	after, ds := l.Parse(id, data)
	return ds.Err() == nil && after != nil && after.Name != before.Name
}
