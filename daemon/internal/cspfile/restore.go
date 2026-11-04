package cspfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// RestoreOptions are the checks around a restore.
type RestoreOptions struct {
	// Recheck must pass before anything is written and again just before the
	// replace; padmate passes "CSP is not running". It is required.
	Recheck func() error

	// replace is the same test hook as Options.replace.
	replace func(target, tmp string) error
}

// Restore puts the files of backup b back where they came from. It first
// backs up the files as they are now (note {"reason":"before-restore",
// "from":"<id>"}), then replaces each target from the backup copy through
// the same safe path as Update and checks the SHA-256 of what it wrote.
// It returns the pre-restore backup. A file whose folder no longer exists,
// or whose copy in the backup is damaged, fails the restore before anything
// is written. A target that no longer exists is recreated.
func (bk *Backups) Restore(b Backup, opts RestoreOptions) (Backup, error) {
	if opts.Recheck == nil {
		return Backup{}, errors.New("cspfile: restore needs a Recheck")
	}
	if len(b.Manifest.Files) == 0 {
		return Backup{}, errors.New("cspfile: backup has no files")
	}
	if err := opts.Recheck(); err != nil {
		return Backup{}, err
	}

	var current []string
	for _, e := range b.Manifest.Files {
		fp, err := FingerprintOf(b.FilePath(e.Name))
		if err != nil {
			return Backup{}, fmt.Errorf("cspfile: backup copy of %s: %w", e.Name, err)
		}
		if fp.SHA256 != e.SHA256 {
			return Backup{}, fmt.Errorf("cspfile: backup copy of %s is damaged", e.Name)
		}
		if fi, err := os.Stat(filepath.Dir(e.Source)); err != nil || !fi.IsDir() {
			return Backup{}, fmt.Errorf("cspfile: folder of %s no longer exists", e.Source)
		}
		if _, err := os.Lstat(e.Source); err == nil {
			current = append(current, e.Source)
		}
	}

	note, err := json.Marshal(map[string]string{"reason": "before-restore", "from": b.ID})
	if err != nil {
		return Backup{}, err
	}
	var pre Backup
	if len(current) > 0 {
		if pre, err = bk.Create(current, note); err != nil {
			return Backup{}, fmt.Errorf("cspfile: backing up before restore: %w", err)
		}
	}

	want := map[string]string{}
	updates := make([]FileUpdate, 0, len(b.Manifest.Files))
	for _, e := range b.Manifest.Files {
		want[e.Source] = e.SHA256
		src := b.FilePath(e.Name)
		updates = append(updates, FileUpdate{
			Target: e.Source,
			Create: true,
			Edit:   func(tmp string) error { return overwrite(tmp, src) },
		})
	}
	err = UpdateMany(updates, Options{
		Recheck: opts.Recheck,
		Verify: func(target string) error {
			fp, err := FingerprintOf(target)
			if err != nil {
				return err
			}
			if fp.SHA256 != want[target] {
				return fmt.Errorf("SHA-256 of %s differs from the backup", target)
			}
			return nil
		},
		replace: opts.replace,
	})
	if err != nil {
		return pre, err
	}
	return pre, nil
}

// overwrite replaces the content of the existing file dst with src's,
// keeping dst's mode.
func overwrite(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}
