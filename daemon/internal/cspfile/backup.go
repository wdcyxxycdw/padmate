package cspfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// ManifestName is the file that completes a backup folder.
	ManifestName = "manifest.json"
	// ManifestFormat is the manifest format this package writes and reads.
	ManifestFormat = 1
	// DefaultKeep is how many complete backups are kept.
	DefaultKeep = 30

	idLayout = "20060102T150405.000Z"
)

// idPattern matches a backup id: UTC time to the millisecond, then an
// optional -n when several backups share that millisecond.
var idPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{3}Z(-[0-9]+)?$`)

// FileEntry describes one file in a backup.
type FileEntry struct {
	// Name is the file's name inside the backup folder (its base name).
	Name string `json:"name"`
	// Source is the full path the file was copied from, and is restored to.
	Source  string    `json:"source"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	SHA256  string    `json:"sha256"`
}

// Manifest is manifest.json of a backup.
type Manifest struct {
	Format  int             `json:"format"`
	Created time.Time       `json:"created"`
	Files   []FileEntry     `json:"files"`
	Note    json.RawMessage `json:"note,omitempty"`
}

// Backup is one complete backup folder.
type Backup struct {
	// ID is the folder's name.
	ID string
	// Dir is the backup folder.
	Dir      string
	Manifest Manifest
}

// FilePath is the path of the copy of the file called name.
func (b Backup) FilePath(name string) string { return filepath.Join(b.Dir, name) }

// Backups is a folder of backups.
type Backups struct {
	// Root holds the backup folders; created on first use.
	Root string
	// Keep is how many complete backups to keep; 0 means DefaultKeep.
	Keep int
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// NewBackups returns Backups rooted at root.
func NewBackups(root string) *Backups { return &Backups{Root: root} }

func (bk *Backups) keep() int {
	if bk.Keep > 0 {
		return bk.Keep
	}
	return DefaultKeep
}

func (bk *Backups) now() time.Time {
	if bk.Now != nil {
		return bk.Now()
	}
	return time.Now()
}

// Create copies files into a new backup folder named by the current UTC
// time and writes manifest.json last, so a folder without one is an
// unfinished backup that List ignores. note is stored as given and must be
// valid JSON when not empty. When done it deletes the backups beyond the
// newest Keep complete ones; a failure there does not fail Create (call
// Prune to see it).
func (bk *Backups) Create(files []string, note json.RawMessage) (Backup, error) {
	return bk.create(files, note, "")
}

// create is Create that never prunes the backup protect (when not empty).
func (bk *Backups) create(files []string, note json.RawMessage, protect string) (Backup, error) {
	if len(files) == 0 {
		return Backup{}, errors.New("cspfile: backup of no files")
	}
	if len(note) > 0 && !json.Valid(note) {
		return Backup{}, errors.New("cspfile: backup note is not valid JSON")
	}
	names := map[string]bool{}
	for _, f := range files {
		n := strings.ToLower(filepath.Base(f))
		if names[n] {
			return Backup{}, fmt.Errorf("cspfile: two files named %s in one backup", filepath.Base(f))
		}
		names[n] = true
	}
	if err := os.MkdirAll(bk.Root, 0o755); err != nil {
		return Backup{}, err
	}

	created := bk.now().UTC()
	id, dir, err := bk.newDir(created)
	if err != nil {
		return Backup{}, err
	}
	b := Backup{ID: id, Dir: dir, Manifest: Manifest{Format: ManifestFormat, Created: created, Note: note}}
	fail := func(err error) (Backup, error) {
		_ = bk.removeBackupDir(id)
		return Backup{}, err
	}

	for _, src := range files {
		abs, err := filepath.Abs(src)
		if err != nil {
			return fail(err)
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return fail(err)
		}
		if !fi.Mode().IsRegular() {
			return fail(fmt.Errorf("cspfile: %s is not a regular file", abs))
		}
		dst := filepath.Join(dir, filepath.Base(abs))
		if err := copyFile(dst, abs, 0o644, os.O_EXCL); err != nil {
			return fail(err)
		}
		fp, err := FingerprintOf(dst)
		if err != nil {
			return fail(err)
		}
		b.Manifest.Files = append(b.Manifest.Files, FileEntry{
			Name: filepath.Base(abs), Source: abs, Size: fp.Size, ModTime: fi.ModTime().UTC(), SHA256: fp.SHA256,
		})
	}
	if err := writeManifest(dir, b.Manifest); err != nil {
		return fail(err)
	}
	_ = bk.prune(protect)
	return b, nil
}

// newDir makes the backup folder for time t, adding -2, -3 ... when the
// name is taken.
func (bk *Backups) newDir(t time.Time) (id, dir string, err error) {
	base := t.Format(idLayout)
	for n := 1; n < 10000; n++ {
		id = base
		if n > 1 {
			id = base + "-" + strconv.Itoa(n)
		}
		dir = filepath.Join(bk.Root, id)
		err = os.Mkdir(dir, 0o755)
		if err == nil {
			return id, dir, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", "", err
		}
	}
	return "", "", fmt.Errorf("cspfile: no free backup name for %s", base)
}

func writeManifest(dir string, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, ManifestName+".tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, ManifestName)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// idKey splits an id into its time part and its -n suffix (1 when absent).
func idKey(id string) (string, int) {
	base, suffix, found := strings.Cut(id, "-")
	if !found {
		return base, 1
	}
	n, _ := strconv.Atoi(suffix)
	return base, n
}

// compareIDs orders ids oldest first.
func compareIDs(a, b string) int {
	ab, an := idKey(a)
	bb, bn := idKey(b)
	if c := strings.Compare(ab, bb); c != 0 {
		return c
	}
	return an - bn
}

// List returns the complete backups, newest first. Folders without a
// readable manifest, with an unknown format, or not named like a backup
// are ignored. A missing Root is an empty list.
func (bk *Backups) List() ([]Backup, error) {
	complete, _, err := bk.scan()
	return complete, err
}

// scan returns the complete backups newest first and the ids of the other
// backup-named folders.
func (bk *Backups) scan() (complete []Backup, incomplete []string, err error) {
	entries, err := os.ReadDir(bk.Root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		id := e.Name()
		if !e.IsDir() || !idPattern.MatchString(id) {
			continue
		}
		b, ok := bk.load(id)
		if !ok {
			incomplete = append(incomplete, id)
			continue
		}
		complete = append(complete, b)
	}
	slices.SortFunc(complete, func(a, b Backup) int { return compareIDs(b.ID, a.ID) })
	slices.SortFunc(incomplete, func(a, b string) int { return compareIDs(b, a) })
	return complete, incomplete, nil
}

func (bk *Backups) load(id string) (Backup, bool) {
	dir := filepath.Join(bk.Root, id)
	path := filepath.Join(dir, ManifestName)
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
		return Backup{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return Backup{}, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4<<20))
	if err != nil {
		return Backup{}, false
	}
	var m Manifest
	if json.Unmarshal(data, &m) != nil || m.Format != ManifestFormat {
		return Backup{}, false
	}
	return Backup{ID: id, Dir: dir, Manifest: m}, true
}

// Get returns the complete backup called id.
func (bk *Backups) Get(id string) (Backup, error) {
	if !idPattern.MatchString(id) {
		return Backup{}, fmt.Errorf("cspfile: %q is not a backup id", id)
	}
	b, ok := bk.load(id)
	if !ok {
		return Backup{}, fmt.Errorf("cspfile: no complete backup %s", id)
	}
	return b, nil
}

// Prune deletes the complete backups beyond the newest Keep, and unfinished
// folders older than the newest complete backup.
func (bk *Backups) Prune() error { return bk.prune("") }

// prune is Prune that keeps the backup protect (when not empty) even when it
// is beyond the newest Keep; Restore protects the backup it is restoring.
func (bk *Backups) prune(protect string) error {
	complete, incomplete, err := bk.scan()
	if err != nil {
		return err
	}
	var errs []error
	if len(complete) > bk.keep() {
		for _, b := range complete[bk.keep():] {
			if b.ID == protect {
				continue
			}
			errs = append(errs, bk.removeBackupDir(b.ID))
		}
	}
	if len(complete) > 0 {
		for _, id := range incomplete {
			if compareIDs(id, complete[0].ID) < 0 {
				errs = append(errs, bk.removeBackupDir(id))
			}
		}
	}
	return errors.Join(errs...)
}

// removeBackupDir deletes the backup folder id under Root without following
// links: a link or junction in its place is unlinked, never entered.
func (bk *Backups) removeBackupDir(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("cspfile: %q is not a backup id", id)
	}
	root, err := os.OpenRoot(bk.Root)
	if err != nil {
		return err
	}
	defer root.Close()
	fi, err := root.Lstat(id)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeType != fs.ModeDir {
		return root.Remove(id)
	}
	return root.RemoveAll(id)
}
