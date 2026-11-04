package profilelib

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/integration"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// IntegrationsDir is the folder of a profile that holds the
// integrations/<id>.json files (ADR-0019, ARCHITECTURE section 16.2).
const IntegrationsDir = profile.IntegrationsDir

// MaxIntegrationSnapshots is how many earlier versions of one
// integrations/<id>.json a profile keeps in its HistoryDir, as
// integrations-<id>-<UTC time>.json. They are not profile.json versions:
// History and Restore do not see them.
const MaxIntegrationSnapshots = 30

// integrationSnapshotPattern matches the name of such a snapshot.
var integrationSnapshotPattern = regexp.MustCompile(`^integrations-([a-z][a-z0-9]{0,31})-([0-9]{8}-[0-9]{6})\.json$`)

func integrationPath(integ string) string { return path.Join(IntegrationsDir, integ+".json") }

// IntegrationSaved reports an integrations file that PutIntegration wrote.
type IntegrationSaved struct {
	Revision string
	// HistoryErr says why the replaced bytes were not kept; the write itself
	// succeeded and the caller logs it.
	HistoryErr error
}

// IntegrationFile is the content of one integrations/<id>.json.
type IntegrationFile struct {
	// Items are the file's items; none when the file does not exist.
	Items []integration.Item
	// Revision hashes the file's bytes; "" when it does not exist.
	Revision string
	// Exists says whether the file exists. A profile without it has no items,
	// which is not an error: the file does not decide that a profile is bound
	// to the integration (integration.Bound).
	Exists bool
}

func checkIntegrationID(id, integ string) error {
	if !ValidID(id) {
		return fmt.Errorf("profile library: invalid id %q", id)
	}
	if !profile.ValidIntegrationID(integ) {
		return fmt.Errorf("profile library: invalid integration id %q", integ)
	}
	return nil
}

// Integration reads profiles/<id>/integrations/<integ>.json. A missing file
// (or integrations folder) is an empty IntegrationFile and no error. A file
// that does not parse (integration.ParseItems) is an error, with Revision set
// so that a client can still replace it; a link, a folder in its place or a
// file over MaxDocumentBytes is an error too.
func (l *Library) Integration(id, integ string) (IntegrationFile, error) {
	if err := checkIntegrationID(id, integ); err != nil {
		return IntegrationFile{}, err
	}
	profiles, err := os.OpenRoot(filepath.Join(l.dir, "profiles"))
	if err != nil {
		return IntegrationFile{}, err
	}
	defer profiles.Close()
	if err := checkFolder(profiles, id); err != nil {
		return IntegrationFile{}, err
	}
	data, err := readIntegration(profiles, id, integ)
	if errors.Is(err, fs.ErrNotExist) {
		return IntegrationFile{}, nil
	}
	if err != nil {
		return IntegrationFile{}, err
	}
	f := IntegrationFile{Revision: Revision(data), Exists: true}
	f.Items, err = integration.ParseItems(data)
	if err != nil {
		return f, fmt.Errorf("profile library: %s/%s: %w", id, integrationPath(integ), err)
	}
	return f, nil
}

// EffectiveIntegration is the items of profile id for the integration with
// the items of the profiles it extends merged in: the parents' items first,
// the farthest parent first, then its own, and for the same keys the child's
// item wins. A parent whose file cannot be read or parsed adds nothing.
func (l *Library) EffectiveIntegration(id, integ string) ([]integration.Item, error) {
	own, err := l.Integration(id, integ)
	if err != nil {
		return nil, err
	}
	var items []integration.Item
	for _, parent := range slices.Backward(l.Parents(id)) {
		if f, err := l.Integration(parent, integ); err == nil {
			items = integration.MergeItems(items, f.Items)
		}
	}
	return integration.MergeItems(items, own.Items), nil
}

// readIntegration reads the file of profile id (a checked folder of profiles)
// without following links; fs.ErrNotExist when the file or its folder is not
// there.
func readIntegration(profiles *os.Root, id, integ string) ([]byte, error) {
	folder, err := profiles.OpenRoot(id)
	if err != nil {
		return nil, err
	}
	defer folder.Close()
	if err := checkIntegrationsDir(folder, id); err != nil {
		return nil, err
	}
	dir, err := folder.OpenRoot(IntegrationsDir)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	name := integ + ".json"
	info, err := dir.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, &UnsupportedError{Path: id + "/" + integrationPath(integ), Reason: "is not a regular file"}
	}
	f, err := dir.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxDocumentBytes {
		return nil, fmt.Errorf("profile library: %s/%s exceeds %d bytes", id, integrationPath(integ), MaxDocumentBytes)
	}
	return data, nil
}

// checkIntegrationsDir refuses an integrations entry of folder that is a
// link or a file; a missing one is fs.ErrNotExist.
func checkIntegrationsDir(folder *os.Root, id string) error {
	info, err := folder.Lstat(IntegrationsDir)
	if err != nil {
		return err
	}
	if info.Mode().Type() != fs.ModeDir {
		return &UnsupportedError{Path: id + "/" + IntegrationsDir, Reason: "is a link or file, not the integrations folder"}
	}
	return nil
}

// PutIntegration writes items as profiles/<id>/integrations/<integ>.json if
// the file still has revision expected ("" for a file that does not exist; a
// *ConflictError otherwise). The items are formatted by integration.FormatItems
// and must pass integration.ParseItems (limits, one entry per key). The file
// is written beside itself, synced and renamed into place; bytes it replaces
// are first kept in the profile's HistoryDir as
// integrations-<integ>-<UTC time>.json, at most MaxIntegrationSnapshots per
// integration. Failing to keep the snapshot does not fail the write; it is
// IntegrationSaved.HistoryErr.
func (l *Library) PutIntegration(id, integ string, items []integration.Item, expected string) (IntegrationSaved, error) {
	if err := checkIntegrationID(id, integ); err != nil {
		return IntegrationSaved{}, err
	}
	data := integration.FormatItems(items)
	if _, err := integration.ParseItems(data); err != nil {
		return IntegrationSaved{}, fmt.Errorf("profile library: integration %s: %w", integ, err)
	}
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	return l.writeIntegrationLocked(id, integ, data, expected)
}

func (l *Library) writeIntegrationLocked(id, integ string, data []byte, expected string) (IntegrationSaved, error) {
	profiles, err := os.OpenRoot(filepath.Join(l.dir, "profiles"))
	if err != nil {
		return IntegrationSaved{}, err
	}
	defer profiles.Close()
	if err := checkFolder(profiles, id); err != nil {
		return IntegrationSaved{}, err
	}
	old, err := readIntegration(profiles, id, integ)
	existed := err == nil
	if err := checkRevision(expected, old, err); err != nil {
		return IntegrationSaved{}, err
	}
	folder, err := profiles.OpenRoot(id)
	if err != nil {
		return IntegrationSaved{}, err
	}
	defer folder.Close()
	if err := folder.Mkdir(IntegrationsDir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return IntegrationSaved{}, err
	}
	if err := checkIntegrationsDir(folder, id); err != nil {
		return IntegrationSaved{}, err
	}
	dir, err := folder.OpenRoot(IntegrationsDir)
	if err != nil {
		return IntegrationSaved{}, err
	}
	defer dir.Close()
	var historyErr error
	if existed && !bytes.Equal(old, data) {
		historyErr = keepIntegrationSnapshot(folder, id, integ, old, time.Now())
	}
	if err := atomicReplace(dir, integ+".json", data); err != nil {
		return IntegrationSaved{}, err
	}
	return IntegrationSaved{Revision: Revision(data), HistoryErr: historyErr}, nil
}

// keepIntegrationSnapshot keeps old, the bytes of integrations/<integ>.json
// that a write replaces, in the HistoryDir of folder and prunes the oldest
// beyond MaxIntegrationSnapshots. The caller holds the writer lock.
func keepIntegrationSnapshot(folder *os.Root, id, integ string, old []byte, now time.Time) error {
	if err := folder.Mkdir(HistoryDir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	if err := checkHistoryDir(folder, id); err != nil {
		return err
	}
	history, err := folder.OpenRoot(HistoryDir)
	if err != nil {
		return err
	}
	defer history.Close()
	names, err := listIntegrationSnapshots(history, integ)
	if err != nil {
		return err
	}
	at := now.UTC().Truncate(time.Second)
	if n := len(names); n > 0 {
		// A new name sorts after the newest, also when two writes share a
		// second or the clock went back.
		if t, ok := integrationSnapshotTime(names[n-1]); ok && !at.After(t) {
			at = t.Add(time.Second)
		}
	}
	name := "integrations-" + integ + "-" + at.Format(versionLayout) + ".json"
	if err := atomicWrite(history, name, old, time.Time{}); err != nil {
		return err
	}
	names = append(names, name)
	var errs []error
	for len(names) > MaxIntegrationSnapshots {
		if err := history.Remove(names[0]); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
		names = names[1:]
	}
	return errors.Join(errs...)
}

// listIntegrationSnapshots lists the snapshot files of integ in history,
// oldest first (the name's time sorts like text).
func listIntegrationSnapshots(history *os.Root, integ string) ([]string, error) {
	dir, err := history.Open(".")
	if err != nil {
		return nil, err
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		m := integrationSnapshotPattern.FindStringSubmatch(e.Name())
		if m != nil && m[1] == integ && e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names, nil
}

func integrationSnapshotTime(name string) (time.Time, bool) {
	m := integrationSnapshotPattern.FindStringSubmatch(name)
	if m == nil {
		return time.Time{}, false
	}
	t, err := time.Parse(versionLayout, m[2])
	return t, err == nil
}

// integrationFilePath reports whether rel, a slash-separated path below a
// profile folder, is integrations/<something>.json directly in the
// integrations folder; the folder name is compared ignoring case.
func integrationFilePath(rel string) (stem string, ok bool) {
	dir, name, found := strings.Cut(rel, "/")
	if !found || !strings.EqualFold(dir, IntegrationsDir) || strings.Contains(name, "/") {
		return "", false
	}
	stem, isJSON := strings.CutSuffix(name, ".json")
	return stem, isJSON
}

// checkIntegrationFile validates data when rel is an integrations/<id>.json:
// the ID must be a valid integration ID and the content must pass
// integration.ParseItems. It returns nil for every other path.
func checkIntegrationFile(rel string, data []byte) error {
	stem, ok := integrationFilePath(rel)
	if !ok {
		return nil
	}
	if !profile.ValidIntegrationID(stem) {
		return fmt.Errorf("%q is not an integration ID (a lower-case letter, then letters and digits, at most 32)", stem)
	}
	if _, err := integration.ParseItems(data); err != nil {
		return err
	}
	return nil
}
