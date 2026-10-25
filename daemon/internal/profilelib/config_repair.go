package profilelib

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// ConfigRepair is a config.json with errors that ReadRepairedConfig
// repaired (PRO-29).
type ConfigRepair struct {
	// ConfigRepair says what the repair reset: the members, or the whole
	// file for one that could not be parsed.
	profile.ConfigRepair
	// Err is the error the file had.
	Err error
	// Backup is the name, in the data folder, of the copy of the file as
	// it was, and Revision the revision of the repaired config.json; both
	// are "" when nothing was written.
	Backup, Revision string
	// WriteErr is why a repair that was to be written was not: the file
	// is as it was, without a backup, and the repair is in memory only.
	WriteErr error
	// Known reports that this write failed on an earlier read of the same
	// file: it was not tried again, and the failure is no news.
	Known bool
}

// configClock names the backups; tests set it.
var configClock = time.Now

// repairAttempts bounds ReadRepairedConfig's reads when config.json
// changes between reading and replacing it.
const repairAttempts = 3

// ReadRepairedConfig is ReadConfig for padmated, which never stops at a
// config.json with errors (PRO-29): it returns the settings of the repair
// (profile.RepairConfig), and with write, first copies the file to a new
// backup in the data folder, config.broken-YYYYMMDD-HHMMSS.json in local
// time (config.broken-…-2.json and so on when the name is taken: no
// backup is ever overwritten), then replaces config.json with the repair.
// Without write (a replay) nothing is written and the repair is in memory
// only. repair is nil for a file without errors and for a missing file,
// which is the defaults. Read errors (no permission, a folder, over the
// size limit) are no errors of the file: err returns them, as ReadConfig
// does. revision is that of the file on disk, the repaired one once
// written.
func (l *Library) ReadRepairedConfig(write bool) (c *profile.Config, revision string, repair *ConfigRepair, err error) {
	for range repairAttempts {
		data, err := l.readData("config.json")
		if errors.Is(err, fs.ErrNotExist) {
			return profile.DefaultConfig(), "", nil, nil
		}
		if err != nil {
			return nil, "", nil, err
		}
		problem := error(nil)
		if c, problem = ValidateConfig(data); problem == nil {
			return c, Revision(data), nil, nil
		}
		fixed, fix := profile.RepairConfig(data, validDeviceKey)
		if c, err = ValidateConfig(fixed); err != nil {
			return nil, Revision(data), nil, fmt.Errorf("profile library: config.json repair: %w", err) // not reached
		}
		repair = &ConfigRepair{ConfigRepair: fix, Err: problem}
		if !write {
			return c, Revision(data), repair, nil
		}
		// A file that could not be replaced is not tried again until it
		// changes: every config.get would write and remove a backup and
		// fail again.
		if failed := l.failedRepair(Revision(data)); failed != nil {
			repair.WriteErr, repair.Known = failed.err, true
			return c, Revision(data), repair, nil
		}
		if !l.writeRepair(repair, data, fixed) {
			continue // config.json changed since it was read: read it again
		}
		if repair.Backup == "" {
			return c, Revision(data), repair, nil
		}
		return c, repair.Revision, repair, nil
	}
	return nil, "", nil, errors.New("profile library: config.json keeps changing; not repaired")
}

// writeRepair backs up data, the bytes of config.json, and replaces the
// file with fixed (replaceConfig), and records in repair how that went:
// Backup and Revision, or WriteErr, which leaves the repair in memory
// only. It reports false when config.json no longer has data.
func (l *Library) writeRepair(repair *ConfigRepair, data, fixed []byte) bool {
	backup, err := l.replaceConfig(data, fixed)
	if _, changed := errors.AsType[*ConflictError](err); changed {
		return false
	}
	if err != nil {
		repair.WriteErr = err
		l.rememberFailedRepair(Revision(data), err)
		return true
	}
	repair.Backup, repair.Revision = backup, Revision(fixed)
	return true
}

// rememberFailedRepair records that config.json, as of revision, could not
// be replaced with its repair (err).
func (l *Library) rememberFailedRepair(revision string, err error) {
	l.repairMu.Lock()
	defer l.repairMu.Unlock()
	l.repairFailed = &failedRepair{revision: revision, err: err}
}

// failedRepair is a repair of config.json that could not be written.
type failedRepair struct {
	revision string // of the file as it was
	err      error
}

// failedRepair returns the failure of the repair of config.json at
// revision, or nil when it was not tried or the file has changed since.
func (l *Library) failedRepair(revision string) *failedRepair {
	l.repairMu.Lock()
	defer l.repairMu.Unlock()
	if l.repairFailed != nil && l.repairFailed.revision == revision {
		return l.repairFailed
	}
	return nil
}

func validDeviceKey(key string) bool {
	_, err := input.ParseDeviceKey(key)
	return err == nil
}

// replaceConfig backs up old, the bytes of config.json, and then replaces
// the file with fixed, unless it no longer has old (a ConflictError). The
// backup is removed again when config.json cannot be replaced.
func (l *Library) replaceConfig(old, fixed []byte) (backup string, err error) {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	current, err := l.readData("config.json")
	if err != nil {
		return "", err
	}
	if !bytes.Equal(current, old) {
		return "", &ConflictError{Current: Revision(current)}
	}
	root, err := os.OpenRoot(l.dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if backup, err = writeBackup(root, old, configClock()); err != nil {
		return "", err
	}
	if l.configHook != nil {
		err = l.configHook()
	}
	if err == nil {
		err = atomicReplace(root, "config.json", fixed)
	}
	if err != nil {
		if removeErr := root.Remove(backup); removeErr != nil {
			err = errors.Join(err, removeErr)
		}
		return "", err
	}
	return backup, nil
}

// maxBackupsPerSecond bounds the names writeBackup tries.
const maxBackupsPerSecond = 100

// writeBackup writes data to a new file named after at,
// config.broken-YYYYMMDD-HHMMSS.json, or with -2, -3, … when that name is
// taken, and syncs it. It never replaces a file. A file it could not
// write whole is removed.
func writeBackup(root *os.Root, data []byte, at time.Time) (string, error) {
	stamp := at.Format("20060102-150405")
	for i := 1; i <= maxBackupsPerSecond; i++ {
		name := "config.broken-" + stamp + ".json"
		if i > 1 {
			name = fmt.Sprintf("config.broken-%s-%d.json", stamp, i)
		}
		f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		if err = errors.Join(err, f.Close()); err != nil {
			return "", errors.Join(err, root.Remove(name))
		}
		return name, nil
	}
	return "", fmt.Errorf("profile library: %d backups of config.json in one second", maxBackupsPerSecond)
}
