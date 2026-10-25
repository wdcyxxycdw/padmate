package profilelib

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// HistoryDir is the hidden folder in profiles/<id>/ that keeps earlier
// versions of profile.json (PRO-17). ValidID never accepts the name, Copy
// skips it, Remove deletes it with the folder, and the script compiler
// refuses an entry or import in it.
const HistoryDir = profile.HistoryDir

// MaxSnapshots is how many earlier versions of profile.json a profile
// keeps; keeping one more deletes the oldest.
const MaxSnapshots = 30

// versionLayout is the time part of a snapshot version, in UTC.
const versionLayout = "20060102-150405"

// versionPattern matches a snapshot version: the UTC time it was taken, to
// the second, and the first 8 hex digits of the SHA-256 of its bytes. The
// file is HistoryDir/<version>.json.
var versionPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{8}$`)

// ValidVersion reports whether version names a snapshot as History lists
// it. It accepts no path separators, dots or other file names, so a
// version always names a file directly in HistoryDir.
func ValidVersion(version string) bool {
	if !versionPattern.MatchString(version) {
		return false
	}
	_, err := time.Parse(versionLayout, version[:len(versionLayout)])
	return err == nil
}

// versionTime is the time in a valid version's name.
func versionTime(version string) time.Time {
	t, _ := time.Parse(versionLayout, version[:len(versionLayout)])
	return t
}

// Snapshot describes one earlier version of a profile's profile.json.
type Snapshot struct {
	// Version names the snapshot for Restore. Versions sort by the time a
	// save replaced them.
	Version string
	// SavedAt is when this version was saved: the modification time
	// profile.json had when a later save replaced it.
	SavedAt time.Time
	// Bytes is the size of the snapshot.
	Bytes int64
	// Name is the profile's name in this version; "" when it has none or
	// the snapshot is not JSON.
	Name string
}

// Saved reports a profile.json that Put or Restore wrote.
type Saved struct {
	Revision string
	// HistoryErr says why the replaced profile.json was not kept as a
	// snapshot. The save itself succeeded; the caller logs it.
	HistoryErr error

	// snapshot is the version this write added to the history, "" when it
	// kept none (the replaced text was the newest snapshot already, or the
	// write replaced nothing). dropSnapshot takes it back.
	snapshot string
}

// History lists the snapshots of profiles/<id>, newest first. A profile
// without any is an empty list; a missing profile folder is fs.ErrNotExist
// and a linked one, or a HistoryDir that is not a folder, an
// UnsupportedError. It reads under the writer lock, so a save does not
// prune a snapshot that History holds open (PITFALLS "rename 替换文件").
func (l *Library) History(id string) ([]Snapshot, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("profile library: invalid id %q", id)
	}
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	history, err := l.openHistory(id)
	var missing *missingHistoryError
	if errors.As(err, &missing) {
		return []Snapshot{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer history.Close()
	versions, err := listVersions(history)
	if err != nil {
		return nil, err
	}
	snapshots := make([]Snapshot, 0, len(versions))
	for _, version := range slices.Backward(versions) {
		info, err := history.Lstat(version + ".json")
		if errors.Is(err, fs.ErrNotExist) {
			continue // deleted by hand since the listing
		}
		if err != nil {
			return nil, err
		}
		s := Snapshot{Version: version, SavedAt: info.ModTime(), Bytes: info.Size()}
		if data, err := readSnapshot(history, id, version); err == nil {
			s.Name = snapshotName(data)
		}
		snapshots = append(snapshots, s)
	}
	return snapshots, nil
}

// Restore saves snapshot version of profiles/<id> as its profile.json, as
// Put saves data: the current profile.json must still have revision
// expected and becomes a snapshot itself. A snapshot that fails
// validation is refused like an invalid Put (*profile.Error); a missing
// one is fs.ErrNotExist. Restore returns the bytes it wrote.
func (l *Library) Restore(id, version, expected string) (Saved, []byte, error) {
	if !ValidID(id) {
		return Saved{}, nil, fmt.Errorf("profile library: invalid id %q", id)
	}
	if !ValidVersion(version) {
		return Saved{}, nil, fmt.Errorf("profile library: invalid snapshot version %q", version)
	}
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	history, err := l.openHistory(id)
	if err != nil {
		return Saved{}, nil, err
	}
	data, err := readSnapshot(history, id, version)
	_ = history.Close()
	if err != nil {
		return Saved{}, nil, err
	}
	if _, ds := l.Parse(id, data); ds.Err() != nil {
		return Saved{}, nil, ds.Err()
	}
	saved, err := l.putLocked(id, expected, data, false)
	if err != nil {
		return Saved{}, nil, err
	}
	return saved, data, nil
}

// missingHistoryError is the fs.ErrNotExist of a profile folder that has
// no HistoryDir yet, which History lists as empty.
type missingHistoryError struct{ err error }

func (e *missingHistoryError) Error() string { return e.err.Error() }
func (e *missingHistoryError) Unwrap() error { return e.err }

// openHistory opens profiles/<id>/HistoryDir. A profile folder or a
// HistoryDir that is a link, junction or file is an UnsupportedError,
// never followed; a missing HistoryDir is a *missingHistoryError.
func (l *Library) openHistory(id string) (*os.Root, error) {
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
	if err := checkHistoryDir(folder, id); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, &missingHistoryError{err: err}
		}
		return nil, err
	}
	return folder.OpenRoot(HistoryDir)
}

// checkHistoryDir refuses a HistoryDir in folder that is not a real
// folder.
func checkHistoryDir(folder *os.Root, id string) error {
	info, err := folder.Lstat(HistoryDir)
	if err != nil {
		return err
	}
	if info.Mode().Type() != fs.ModeDir {
		return &UnsupportedError{Path: id + "/" + HistoryDir, Reason: "is a link or file, not the history folder"}
	}
	return nil
}

// listVersions returns the versions of the snapshot files in history,
// oldest first. Other names, such as an interrupted write's temporary
// file, and anything but regular files are left out.
func listVersions(history *os.Root) ([]string, error) {
	dir, err := history.Open(".")
	if err != nil {
		return nil, err
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return nil, err
	}
	var versions []string
	for _, e := range entries {
		version, ok := strings.CutSuffix(e.Name(), ".json")
		if ok && ValidVersion(version) && e.Type().IsRegular() {
			versions = append(versions, version)
		}
	}
	slices.Sort(versions)
	return versions, nil
}

// readSnapshot reads one snapshot file, a regular file of at most
// MaxDocumentBytes; id names the profile in errors.
func readSnapshot(history *os.Root, id, version string) ([]byte, error) {
	name := version + ".json"
	info, err := history.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, &UnsupportedError{Path: id + "/" + HistoryDir + "/" + name, Reason: "is not a regular file"}
	}
	f, err := history.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxDocumentBytes {
		return nil, fmt.Errorf("profile library: snapshot %s of %s exceeds %d bytes", version, id, MaxDocumentBytes)
	}
	return data, nil
}

// snapshotName is the profile's name in a snapshot, if it has one.
func snapshotName(data []byte) string {
	var doc struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &doc) != nil {
		return ""
	}
	return doc.Name
}

// keepSnapshot keeps old, the profile.json of folder (profiles/<id>) that
// a save is about to replace, as its newest snapshot, unless it equals the
// newest one; then it deletes the oldest snapshots beyond MaxSnapshots.
// The caller holds the writer lock. The snapshot is written beside its
// name, synced and renamed into place, with profile.json's modification
// time, so SavedAt is when that version was saved. now names the
// snapshot; a new name always sorts after the newest, even when the clock
// went back or two saves share a second. It returns the version it added,
// "" when it added none.
func keepSnapshot(folder *os.Root, id string, old []byte, now time.Time) (string, error) {
	savedAt := now
	if info, err := folder.Lstat("profile.json"); err == nil {
		savedAt = info.ModTime()
	}
	if err := folder.Mkdir(HistoryDir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	if err := checkHistoryDir(folder, id); err != nil {
		return "", err
	}
	history, err := folder.OpenRoot(HistoryDir)
	if err != nil {
		return "", err
	}
	defer history.Close()
	versions, err := listVersions(history)
	if err != nil {
		return "", err
	}
	at := now.UTC().Truncate(time.Second)
	if n := len(versions); n > 0 {
		newest := versions[n-1]
		if data, err := readSnapshot(history, id, newest); err == nil && bytes.Equal(data, old) {
			return "", nil
		}
		if t := versionTime(newest); !at.After(t) {
			at = t.Add(time.Second)
		}
	}
	sum := sha256.Sum256(old)
	version := at.Format(versionLayout) + "-" + hex.EncodeToString(sum[:4])
	if err := atomicWrite(history, version+".json", old, savedAt); err != nil {
		return "", err
	}
	versions = append(versions, version)
	var errs []error
	for len(versions) > MaxSnapshots {
		if err := history.Remove(versions[0] + ".json"); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
		versions = versions[1:]
	}
	return version, errors.Join(errs...)
}

// dropSnapshot deletes snapshot version of profiles/<id>, which a write of
// the same critical section added (Saved.snapshot), so that an undo of that
// write leaves the history as it was. A snapshot already gone, or one that
// cannot be deleted, stays or goes without an error: it is a copy of the text
// that is current again. The caller holds writeMu.
func (l *Library) dropSnapshot(id, version string) {
	if version == "" {
		return
	}
	history, err := l.openHistory(id)
	if err != nil {
		return
	}
	defer history.Close()
	_ = history.Remove(version + ".json")
}
