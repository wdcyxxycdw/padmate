package profilelib

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// ConflictError reports an optimistic write against stale original bytes.
// Empty revisions represent missing files, never an unconditional overwrite.
type ConflictError struct {
	Current string
}

func (e *ConflictError) Error() string {
	return "profile library: revision conflict; read the document again"
}

// Put validates a profile before touching disk and preserves its exact bytes.
// The daemon shares one Library; revision check and atomic replace have a
// single writer. External editors are detected on the next revision check.
// A profile.json that Put replaces with other bytes is kept as a snapshot
// first (History); failing to keep it never fails the save but is reported
// in Saved.HistoryErr. Put refuses to change the name of the fixed default
// profile (*FixedError); Restore may bring back an earlier version whole.
func (l *Library) Put(id, expected string, data []byte) (Saved, error) {
	if !ValidID(id) {
		return Saved{}, fmt.Errorf("profile library: invalid id %q", id)
	}
	if len(data) > MaxDocumentBytes {
		return Saved{}, errors.New("profile library: document exceeds size limit")
	}
	if _, ds := l.Parse(id, data); ds.Err() != nil {
		return Saved{}, ds.Err()
	}
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	return l.putLocked(id, expected, data, id == DefaultID)
}

// putLocked writes data, which is valid, as profiles/<id>/profile.json if
// the file still has revision expected; keepName refuses data that renames
// the current profile. The caller holds writeMu.
func (l *Library) putLocked(id, expected string, data []byte, keepName bool) (Saved, error) {
	return l.writeLocked(id, expected, data, keepName, true)
}

// writeLocked is putLocked that keeps the replaced text as a snapshot only
// if keepHistory is set. An undo of an earlier write passes false: the text it
// replaces is one the user never saved.
func (l *Library) writeLocked(id, expected string, data []byte, keepName, keepHistory bool) (Saved, error) {
	if l.putHook != nil {
		if err := l.putHook(id); err != nil {
			return Saved{}, err
		}
	}
	old, err := l.Read(id)
	existed := err == nil
	if err := checkRevision(expected, old, err); err != nil {
		return Saved{}, err
	}
	if keepName && existed && l.renames(id, old, data) {
		return Saved{}, &FixedError{ID: id, Op: "rename"}
	}
	if err := os.MkdirAll(filepath.Join(l.dir, "profiles"), 0o700); err != nil {
		return Saved{}, err
	}
	root, err := os.OpenRoot(filepath.Join(l.dir, "profiles"))
	if err != nil {
		return Saved{}, err
	}
	defer root.Close()
	if err := root.MkdirAll(id, 0o700); err != nil {
		return Saved{}, err
	}
	destination, err := root.OpenRoot(id)
	if err != nil {
		return Saved{}, err
	}
	defer destination.Close()
	var saved Saved
	// The old version is safe in the history before it is replaced.
	if keepHistory && existed && !bytes.Equal(old, data) {
		saved.snapshot, saved.HistoryErr = keepSnapshot(destination, id, old, time.Now())
	}
	if err := atomicReplace(destination, "profile.json", data); err != nil {
		return Saved{}, err
	}
	saved.Revision = Revision(data)
	return saved, nil
}

func checkRevision(expected string, old []byte, err error) error {
	current := ""
	if err == nil {
		current = Revision(old)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if expected != current {
		return &ConflictError{Current: current}
	}
	return nil
}

// ConfigText returns original bytes even when parsing fails, allowing a client
// to repair the document. A missing file returns a default document and "".
func (l *Library) ConfigText() ([]byte, string, error) {
	data, err := l.readData("config.json")
	if errors.Is(err, os.ErrNotExist) {
		return []byte(`{"schemaVersion":0}`), "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return data, Revision(data), nil
}

// ValidateConfig also checks platform-neutral DeviceKey syntax, which the
// profile package cannot import from input.
func ValidateConfig(data []byte) (*profile.Config, error) {
	if len(data) > MaxDocumentBytes {
		return nil, errors.New("profile library: config exceeds size limit")
	}
	c, ds := profile.ParseConfig(data)
	if err := ds.Err(); err != nil {
		return nil, err
	}
	for key := range c.Devices {
		if _, err := input.ParseDeviceKey(key); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// PutConfig shares the profile/state writer and uses the same optimistic
// revision and atomic replacement rules. It does not change runtime settings.
func (l *Library) PutConfig(expected string, data []byte) (string, error) {
	if _, err := ValidateConfig(data); err != nil {
		return "", err
	}
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	old, err := l.readData("config.json")
	if err := checkRevision(expected, old, err); err != nil {
		return "", err
	}
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(l.dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := atomicReplace(root, "config.json", data); err != nil {
		return "", err
	}
	return Revision(data), nil
}
