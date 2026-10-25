// Package profilelib reads the user's profile library. IDs are portable,
// canonical directory names; all reads are confined by os.Root, including
// symlinks and Windows junctions. Reading never changes the source files.
package profilelib

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// MaxDocumentBytes bounds a single library document before parsing it.
const MaxDocumentBytes = 4 << 20

// Library refers to a data directory containing profiles/<id>/profile.json.
// It is safe for concurrent reads and holds no open handles between calls.
type Library struct {
	dir     string
	writeMu sync.Mutex
	// copyMu serializes Copy and Remove, which write or delete their
	// temporary folders without writeMu and remove the ones that
	// interrupted copies and incomplete removals left.
	copyMu sync.Mutex
	// removeHook, under copyMu, runs in Remove after the folder has left
	// the library, before it is deleted (SetRemoveHook).
	removeHook func(tmp string)
	// putHook, under writeMu, fails a write of profile id's profile.json
	// when it returns an error; tests set it.
	putHook func(id string) error
	// configHook, under writeMu, fails a repair's replacement of
	// config.json after its backup when it returns an error; tests set it.
	configHook func() error
	// moveHook, under writeMu, fails the move of profile id's macro file
	// in MigrateMacros when it returns an error; tests set it.
	moveHook func(id, file string) error
	// repairMu guards repairFailed: the config.json whose repair could not
	// be written, and why. The write is not tried again for that file
	// (ReadRepairedConfig).
	repairMu     sync.Mutex
	repairFailed *failedRepair
}

// New selects a data directory. It does not create or read any files.
func New(dir string) *Library { return &Library{dir: dir} }

// DefaultDir returns %APPDATA%/padmate on Windows (the user config directory
// on other platforms). Tests and isolated daemons supply a directory explicitly.
func DefaultDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "padmate"), nil
}

// Dir is the selected data directory.
func (l *Library) Dir() string { return l.dir }

// Path returns the profile path for diagnostics and relative script resolution.
// Use Read for file access, so links cannot escape the library.
func (l *Library) Path(id string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("profile library: invalid id %q", id)
	}
	return filepath.Join(l.dir, "profiles", id, "profile.json"), nil
}

// ValidID accepts lowercase ASCII names of 1..64 characters, starting with a
// letter or digit, followed by letters, digits, '-' or '_'. Windows device names
// are rejected on all platforms to keep exported libraries portable.
func ValidID(id string) bool {
	return profile.ValidLibraryID(id)
}

// Entry describes a library item. Invalid items remain visible with a Problem
// so a UI can help repair them instead of silently hiding them. Default
// marks the fixed default profile (DefaultID). Created is when the
// profile's folder was created, in Unix milliseconds, and absent when the
// platform does not say: not profile.json's, which every save replaces.
// Extends is the parent the profile.json names (PRO-19), even when it has
// other problems; Diagnostics are the ones in the profile's own text, and
// Problem the first error, which may be in a parent's.
type Entry struct {
	ID          string              `json:"id"`
	Name        string              `json:"name,omitempty"`
	Default     bool                `json:"default,omitempty"`
	Created     int64               `json:"created,omitempty"`
	Extends     string              `json:"extends,omitempty"`
	Revision    string              `json:"revision,omitempty"`
	Problem     string              `json:"problem,omitempty"`
	Diagnostics profile.Diagnostics `json:"diagnostics,omitempty"`
}

// Document is a loaded and validated profile. Revision hashes the original
// bytes on disk; Data is the migrated form and Path anchors relative scripts.
// A profile with a parent is the merge of the chain (PRO-19): Profile is the
// merged profile, Resolved says where its parts come from, and
// ParentDiagnostics are the problems in the parents' files, which Entry's
// diagnostics (those of Data) do not have.
type Document struct {
	Entry
	Path              string
	Data              []byte
	Profile           *profile.Profile
	Resolved          *profile.Resolved
	ParentDiagnostics profile.Diagnostics
}

// Revision returns the content revision used for optimistic writes in M9.
func Revision(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

// Read returns original profile bytes, with a size limit and confined paths.
func (l *Library) Read(id string) ([]byte, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("profile library: invalid id %q", id)
	}
	root, err := os.OpenRoot(filepath.Join(l.dir, "profiles"))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(filepath.Join(id, "profile.json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxDocumentBytes {
		return nil, fmt.Errorf("profile library: %s exceeds %d bytes", id, MaxDocumentBytes)
	}
	return data, nil
}

// Load reads and migrates a profile, and validates its schema and semantics.
func (l *Library) Load(id string) (*Document, error) {
	data, err := l.Read(id)
	if err != nil {
		return nil, err
	}
	doc := &Document{Entry: Entry{ID: id, Revision: Revision(data), Extends: profile.ExtendsOf(data)}, Path: filepath.Join(l.dir, "profiles", id, "profile.json")}
	doc.Data, doc.Diagnostics = profile.Migrate(data)
	if err := doc.Diagnostics.Err(); err != nil {
		return doc, err
	}
	var ds profile.Diagnostics
	doc.Resolved, ds = l.Resolve(id, doc.Data)
	doc.Diagnostics = ds.Local()
	for _, d := range ds {
		if d.Profile != "" {
			doc.ParentDiagnostics = append(doc.ParentDiagnostics, d)
		}
	}
	if err := ds.Err(); err != nil {
		return doc, err
	}
	doc.Profile = doc.Resolved.Profile
	doc.Name = doc.Profile.Name
	return doc, nil
}

// List returns items sorted by ID. A missing library is empty. Files and
// noncanonical directory names are ignored; unreadable profiles stay visible.
func (l *Library) List() ([]Entry, error) {
	ids, err := l.IDs()
	if err != nil {
		return nil, err
	}
	profiles, err := os.OpenRoot(filepath.Join(l.dir, "profiles"))
	if err == nil {
		defer profiles.Close()
	}
	entries := make([]Entry, 0, len(ids))
	for _, id := range ids {
		e := Entry{ID: id}
		doc, err := l.Load(e.ID)
		if doc != nil {
			e = doc.Entry
		}
		if err != nil {
			e.Problem = err.Error()
		}
		e.Default = id == DefaultID
		if profiles != nil {
			if info, err := profiles.Stat(id); err == nil {
				if created, ok := birthTime(info); ok {
					e.Created = created.UnixMilli()
				}
			}
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// IDs enumerates the library without opening or validating each profile.
func (l *Library) IDs() ([]string, error) {
	root, err := os.OpenRoot(filepath.Join(l.dir, "profiles"))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	files, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(files))
	for _, f := range files {
		if !ValidID(f.Name()) || !f.IsDir() && f.Type()&os.ModeSymlink == 0 {
			continue
		}
		ids = append(ids, f.Name())
	}
	slices.Sort(ids)
	return ids, nil
}
