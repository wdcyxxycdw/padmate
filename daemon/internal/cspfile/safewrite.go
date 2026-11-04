package cspfile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

const (
	tmpSuffix  = ".padmate-tmp"
	undoSuffix = ".padmate-undo"
)

var (
	// ErrJournal means the target has a -journal or -wal file next to it:
	// SQLite is in the middle of something, or CSP crashed in it.
	ErrJournal = errors.New("cspfile: SQLite journal or WAL file present")
	// ErrChanged means the target changed while the copy was being edited.
	ErrChanged = errors.New("cspfile: file changed while it was being edited")
)

// Options are the checks around a replace. All are optional.
type Options struct {
	// Recheck runs after the edit and just before the replace; padmate passes
	// "CSP is not running". An error aborts the update.
	Recheck func() error
	// Verify runs after each target was replaced, to read it back. An error
	// puts the original bytes back.
	Verify func(target string) error

	// replace swaps tmp over target; nil means replaceAtomic. Tests inject
	// failures here.
	replace func(target, tmp string) error
}

func (o Options) replaceFn() func(target, tmp string) error {
	if o.replace != nil {
		return o.replace
	}
	return replaceAtomic
}

// FileUpdate is one file for UpdateMany.
type FileUpdate struct {
	// Target is the file to change.
	Target string
	// Edit changes the copy at tmpPath. It must leave a complete file there.
	Edit func(tmpPath string) error
	// Create allows Target not to exist: the copy then starts empty and a
	// failed update removes the new Target.
	Create bool
}

// Update changes target safely:
//
//  1. refuse if target-journal or target-wal exists (ErrJournal);
//  2. fingerprint target;
//  3. copy it to target.padmate-tmp in the same folder;
//  4. call edit on the copy;
//  5. call opts.Recheck and check the target's fingerprint is unchanged
//     (ErrChanged);
//  6. replace target with the copy atomically;
//  7. call opts.Verify on target.
//
// A failure before step 6 leaves target untouched; a failure in step 6 or 7
// puts the original bytes back. The copy never survives.
func Update(target string, edit func(tmpPath string) error, opts Options) error {
	return UpdateMany([]FileUpdate{{Target: target, Edit: edit}}, opts)
}

// pending is the state of one file during UpdateMany.
type pending struct {
	FileUpdate
	tmp, undo string
	existed   bool
	fp        Fingerprint
	replaced  bool
}

// UpdateMany changes several files as one: every copy is edited and every
// check passes before the first replace, and when a later replace or read
// back fails the files already replaced are put back from copies made just
// before. The caller backs the files up first (Backups.Create).
func UpdateMany(updates []FileUpdate, opts Options) (err error) {
	if len(updates) == 0 {
		return nil
	}
	seen := map[string]bool{}
	ps := make([]*pending, len(updates))
	for i, u := range updates {
		key := strings.ToLower(u.Target)
		if u.Target == "" || u.Edit == nil || seen[key] {
			return fmt.Errorf("cspfile: bad update of %q", u.Target)
		}
		seen[key] = true
		ps[i] = &pending{FileUpdate: u, tmp: u.Target + tmpSuffix, undo: u.Target + undoSuffix}
	}
	keepUndo := false
	defer func() {
		for _, p := range ps {
			_ = os.Remove(p.tmp)
			if !keepUndo {
				_ = os.Remove(p.undo)
			}
		}
	}()

	for _, p := range ps {
		if err := noJournal(p.Target); err != nil {
			return err
		}
	}
	for _, p := range ps {
		if err := p.prepare(); err != nil {
			return err
		}
	}

	if opts.Recheck != nil {
		if err := opts.Recheck(); err != nil {
			return err
		}
	}
	for _, p := range ps {
		if err := p.unchanged(); err != nil {
			return err
		}
	}
	for _, p := range ps {
		if !p.existed {
			continue
		}
		if err := removeStaleRegular(p.undo); err != nil {
			return err
		}
		if err := copyFile(p.undo, p.Target, 0o600, os.O_EXCL); err != nil {
			return fmt.Errorf("cspfile: keeping the original of %s: %w", p.Target, err)
		}
	}

	replace := opts.replaceFn()
	for i, p := range ps {
		if err := replace(p.Target, p.tmp); err != nil {
			rerr := rollback(ps[:i], replace)
			keepUndo = rerr != nil
			return errors.Join(fmt.Errorf("cspfile: replacing %s: %w", p.Target, err), rerr)
		}
		p.replaced = true
		if opts.Verify == nil {
			continue
		}
		if err := opts.Verify(p.Target); err != nil {
			rerr := rollback(ps[:i+1], replace)
			keepUndo = rerr != nil
			return errors.Join(fmt.Errorf("cspfile: reading back %s: %w", p.Target, err), rerr)
		}
	}
	return nil
}

// prepare runs steps 2 to 4 for one file.
func (p *pending) prepare() error {
	fi, err := os.Lstat(p.Target)
	switch {
	case err == nil:
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("cspfile: %s is not a regular file", p.Target)
		}
		p.existed = true
	case errors.Is(err, fs.ErrNotExist) && p.Create:
	default:
		return err
	}
	if p.existed {
		if p.fp, err = FingerprintOf(p.Target); err != nil {
			return err
		}
	}
	if err := removeStaleRegular(p.tmp); err != nil {
		return err
	}
	if p.existed {
		err = copyFile(p.tmp, p.Target, fi.Mode().Perm(), os.O_EXCL)
	} else {
		err = createEmpty(p.tmp)
	}
	if err != nil {
		return err
	}
	return p.Edit(p.tmp)
}

// unchanged checks the target still has the fingerprint taken before the edit.
func (p *pending) unchanged() error {
	if !p.existed {
		if _, err := os.Lstat(p.Target); err == nil {
			return fmt.Errorf("%w: %s appeared", ErrChanged, p.Target)
		}
		return nil
	}
	now, err := FingerprintOf(p.Target)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrChanged, p.Target, err)
	}
	if !Same(p.fp, now) {
		return fmt.Errorf("%w: %s", ErrChanged, p.Target)
	}
	return nil
}

// rollback puts the original of every replaced file back.
func rollback(ps []*pending, replace func(target, tmp string) error) error {
	var errs []error
	for i := len(ps) - 1; i >= 0; i-- {
		p := ps[i]
		if !p.replaced {
			continue
		}
		var err error
		if p.existed {
			if err = replace(p.Target, p.undo); err != nil {
				err = fmt.Errorf("cspfile: could not put %s back (original kept at %s): %w", p.Target, p.undo, err)
			}
		} else if err = os.Remove(p.Target); err != nil {
			err = fmt.Errorf("cspfile: could not remove new %s: %w", p.Target, err)
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func noJournal(target string) error {
	for _, suffix := range []string{"-journal", "-wal"} {
		if _, err := os.Lstat(target + suffix); err == nil {
			return fmt.Errorf("%w: %s%s", ErrJournal, target, suffix)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// removeStaleRegular deletes path if it is a regular file left by an
// earlier run; anything else there (folder, link) is an error.
func removeStaleRegular(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("cspfile: %s exists and is not a regular file", path)
	}
	return os.Remove(path)
}

func createEmpty(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

// copyFile copies src to dst, creating dst with perm and flag (os.O_EXCL),
// and syncs it. A failed copy removes dst.
func copyFile(dst, src string, perm fs.FileMode, flag int) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|flag, perm)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(dst)
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
