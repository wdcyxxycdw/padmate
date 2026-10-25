package profilelib

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Copy limits keep a duplicate small and bounded: a profile folder holds a
// profile.json and its script sources, not assets.
const (
	MaxCopyEntries = 256      // files and folders below the profile folder
	MaxCopyBytes   = 32 << 20 // total size of the copied files
	MaxCopyDepth   = 8        // folder levels below the profile folder
)

// tempPrefix starts the hidden folders in profiles/ that a copy or an
// import is written into and that Remove renames a profile to before
// deleting it. ValidID never accepts the name, so List and IDs skip them.
const tempPrefix = ".tmp-"

// tempName is a fresh hidden folder name for id.
func tempName(id string) string {
	return tempPrefix + id + "-" + strings.ToLower(rand.Text()[:12])
}

// copyWriteHook runs in Copy after the source is read, before the copy is
// written; tests check there that the writer lock is free.
var copyWriteHook = func(*Library) {}

// ExistsError refuses to copy onto an existing library ID.
type ExistsError struct{ ID string }

func (e *ExistsError) Error() string {
	return fmt.Sprintf("profile library: %s already exists", e.ID)
}

type copyItem struct {
	path string // slash-separated, relative to the profile folder
	dir  bool
	data []byte // a file's bytes, read under writeMu
}

// Copy duplicates profiles/<from>, including its scripts, into the new
// profiles/<to> and returns the revision of the copied profile.json. The
// source must hold only regular files and folders within the copy limits;
// links and special files are refused rather than skipped. Its HistoryDir
// is left out: the copy starts without history.
//
// The source is read into memory, at most MaxCopyBytes, under the writer
// lock, so the copy is one consistent version and Put never meets an open
// source file, which Windows refuses to replace (PITFALLS "rename 替换文
// 件"). The files are then written and synced into a hidden temporary
// folder without the lock, so a large copy does not hold up saves, and
// renamed into place under the lock only when complete: a failure leaves
// no partial profile behind. A copy also removes the temporary folders of
// copies a crash or kill cut off.
func (l *Library) Copy(from, to string) (string, error) {
	for _, id := range []string{from, to} {
		if !ValidID(id) {
			return "", fmt.Errorf("profile library: invalid id %q", id)
		}
	}
	l.copyMu.Lock()
	defer l.copyMu.Unlock()
	root, err := os.OpenRoot(filepath.Join(l.dir, "profiles"))
	if err != nil {
		return "", err
	}
	defer root.Close()
	items, err := l.readSource(root, from, to)
	if err != nil {
		return "", err
	}
	l.removeLeftovers(root)
	copyWriteHook(l)
	tmp := tempName(to)
	if err := root.Mkdir(tmp, 0o700); err != nil {
		return "", err
	}
	revision, err := writeItems(root, tmp, items)
	if err == nil {
		err = l.publishCopy(root, tmp, to)
	}
	if err != nil {
		if removeErr := root.RemoveAll(tmp); removeErr != nil {
			err = errors.Join(err, removeErr)
		}
		return "", err
	}
	return revision, nil
}

// readSource checks the source and the free destination ID, plans the copy
// and reads every planned file, all under the writer lock.
func (l *Library) readSource(root *os.Root, from, to string) ([]copyItem, error) {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	info, err := root.Lstat(from)
	if err != nil {
		return nil, err
	}
	if info.Mode().Type() != fs.ModeDir {
		return nil, &UnsupportedError{Path: from, Reason: "is a link or special file, not a profile folder"}
	}
	if err := checkAbsent(root, to); err != nil {
		return nil, err
	}
	return readTree(root, from, profile.IsHistoryDir)
}

// readTree plans and reads the tree of profiles/<from>, a folder, leaving
// out the paths skip selects; the caller holds the writer lock.
func readTree(root *os.Root, from string, skip func(string) bool) ([]copyItem, error) {
	source, err := root.OpenRoot(from)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	items, err := planCopy(source, from, skip)
	if err != nil {
		return nil, err
	}
	budget := int64(MaxCopyBytes)
	for i := range items {
		if items[i].dir {
			continue
		}
		data, err := readCopyFile(source, from, items[i].path, budget)
		if err != nil {
			return nil, err
		}
		budget -= int64(len(data))
		items[i].data = data
	}
	return items, nil
}

// publishCopy renames the complete temporary folder to its ID, unless a
// save or another copy has taken the ID since readSource.
func (l *Library) publishCopy(root *os.Root, tmp, to string) error {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	if err := checkAbsent(root, to); err != nil {
		return err
	}
	return renameDir(root, tmp, to)
}

func checkAbsent(root *os.Root, id string) error {
	_, err := root.Lstat(id)
	if err == nil {
		return &ExistsError{ID: id}
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// removeLeftovers deletes the temporary folders that interrupted copies,
// imports and incomplete removals left, after renaming back the folders
// of profiles that an import cut off while it replaced them
// (restoreReplaced). The caller holds copyMu, so no copy, import or
// removal in this process is using one; only folders are removed, never
// links. It is best effort: a folder that cannot be removed now is tried
// again by the next Copy, Import or Remove.
func (l *Library) removeLeftovers(root *os.Root) {
	l.writeMu.Lock()
	restoreReplaced(root)
	l.writeMu.Unlock()
	for _, name := range hiddenFolders(root, tempPrefix) {
		_ = root.RemoveAll(name)
	}
	// A replaced folder whose profile is back in place is a leftover.
	for _, name := range hiddenFolders(root, replacedPrefix) {
		if id, ok := replacedID(name); ok {
			if _, err := root.Lstat(id); err == nil {
				_ = root.RemoveAll(name)
			}
		}
	}
}

// hiddenFolders lists the folders in root whose names start with prefix;
// links and files are left out.
func hiddenFolders(root *os.Root, prefix string) []string {
	dir, err := root.Open(".")
	if err != nil {
		return nil
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		if info, err := root.Lstat(e.Name()); err == nil && info.Mode().Type() == fs.ModeDir {
			names = append(names, e.Name())
		}
	}
	return names
}

// planCopy walks the source before anything is read or written, so limits
// and unsupported entries refuse the copy without a partial result. A path
// that skip selects is neither entered nor counted.
func planCopy(source *os.Root, from string, skip func(string) bool) ([]copyItem, error) {
	var items []copyItem
	var total int64
	hasProfile := false
	err := fs.WalkDir(source.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		// A copy is a new profile without history; whatever HistoryDir
		// is, in any case of its name, it is neither entered nor counted.
		if skip(p) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel := from + "/" + p
		// Lstat confirms the listing's type: links and Windows junctions
		// report a non-directory type and are never entered.
		info, err := source.Lstat(filepath.FromSlash(p))
		if err != nil {
			return err
		}
		switch {
		case info.Mode().Type() == fs.ModeDir && d.IsDir():
			if strings.Count(p, "/")+1 > MaxCopyDepth {
				return &UnsupportedError{Path: rel, Reason: fmt.Sprintf("is nested more than %d folders deep", MaxCopyDepth)}
			}
		case info.Mode().IsRegular() && d.Type().IsRegular():
			total += info.Size()
			if total > MaxCopyBytes {
				return &UnsupportedError{Path: from, Reason: fmt.Sprintf("holds more than %d MiB of files", MaxCopyBytes>>20)}
			}
			hasProfile = hasProfile || p == "profile.json"
		default:
			return &UnsupportedError{Path: rel, Reason: "is a link or special file; copy only regular files and folders"}
		}
		items = append(items, copyItem{path: p, dir: d.IsDir()})
		if len(items) > MaxCopyEntries {
			return &UnsupportedError{Path: from, Reason: fmt.Sprintf("holds more than %d files and folders", MaxCopyEntries)}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !hasProfile {
		return nil, fmt.Errorf("profile library: %s has no profile.json: %w", from, fs.ErrNotExist)
	}
	return items, nil
}

// readCopyFile reads one planned file, at most budget+1 bytes, so a file
// that grew since the plan still counts against MaxCopyBytes. profile.json
// is also held to the document limit, like every other library read.
func readCopyFile(source *os.Root, from, p string, budget int64) ([]byte, error) {
	in, err := source.Open(filepath.FromSlash(p))
	if err != nil {
		return nil, err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, &UnsupportedError{Path: from + "/" + p, Reason: "is not a regular file"}
	}
	data, err := io.ReadAll(io.LimitReader(in, budget+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > budget {
		return nil, &UnsupportedError{Path: from, Reason: fmt.Sprintf("holds more than %d MiB of files", MaxCopyBytes>>20)}
	}
	if p == "profile.json" && len(data) > MaxDocumentBytes {
		return nil, fmt.Errorf("profile library: profile.json exceeds %d bytes", MaxDocumentBytes)
	}
	return data, nil
}

// writeItems recreates the planned tree below root/tmp and returns the
// revision of the profile.json bytes it wrote.
func writeItems(root *os.Root, tmp string, items []copyItem) (revision string, err error) {
	destination, err := root.OpenRoot(tmp)
	if err != nil {
		return "", err
	}
	// The handle must be closed before renaming tmp on Windows.
	defer func() { err = errors.Join(err, destination.Close()) }()
	for _, item := range items {
		name := filepath.FromSlash(item.path)
		if item.dir {
			if err := destination.Mkdir(name, 0o700); err != nil {
				return "", err
			}
			continue
		}
		if err := writeCopyFile(destination, name, item.data); err != nil {
			return "", err
		}
		if item.path == "profile.json" {
			revision = Revision(item.data)
		}
	}
	return revision, nil
}

// writeCopyFile creates name and syncs it, so the renamed folder never
// holds a file whose data a power loss could still drop.
func writeCopyFile(destination *os.Root, name string, data []byte) error {
	out, err := destination.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	if err == nil {
		err = out.Sync()
	}
	return errors.Join(err, out.Close())
}
