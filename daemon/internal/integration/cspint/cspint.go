// Package cspint is the CLIP STUDIO PAINT integration (ADR-0019): it joins
// the pure model of internal/csp with the file handling of internal/cspfile
// behind the generic integration.Integration interface.
//
// CSP's own files are never opened for SQLite reading in place: a read copies
// default.khc, EditImageTool.todb and Preference\Config.sqlite to a private
// temporary folder and reads the copies, because CSP may be writing. A write
// edits copies too and replaces the originals atomically (cspfile).
package cspint

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/cspfile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/integration"
)

// Identity of the integration.
const (
	ID   = "csp"
	Name = "CLIP STUDIO PAINT"
)

// Function ID prefixes: "command:<menucommandtype>/<menucommand>" and
// "tool:<32 lower-case hex characters>".
const (
	commandPrefix = "command:"
	toolPrefix    = "tool:"
)

// Options configure the integration. Only PadmateDir is required.
type Options struct {
	// PadmateDir is padmate's data directory; backups go to
	// <PadmateDir>\integration-backups\csp.
	PadmateDir string
	// DataDir returns the folder that config.json's integrations.csp.dataDir
	// names (the CLIPStudioPaintVer* folder), or "" to look for it. It is
	// called for every operation, so a changed setting applies at once. Nil
	// means always look.
	DataDir func() string
	// Namer supplies command names and the dialog's layout; nil names a
	// command by its ID.
	Namer integration.Namer

	// The rest are for tests; the zero values use the real system.

	// FindDataDirs finds CSP's data folders; nil means
	// cspfile.FindSystemDataDirs, which finds nothing off Windows.
	FindDataDirs func() []cspfile.DataDir
	// FindInstallDir finds the installation; nil means cspfile.FindInstallDir.
	FindInstallDir func() (cspfile.InstallDir, error)
	// Running reports whether CSP runs; nil means cspfile.Running.
	Running func() (bool, error)
	// Now is the clock of the backups; nil means time.Now.
	Now func() time.Time
}

// Integration is the CSP integration. Create it with New.
type Integration struct {
	opts    Options
	backups *cspfile.Backups

	// writeMu serializes Write and Restore.
	writeMu sync.Mutex

	cacheMu sync.Mutex
	cache   *cached
}

var _ integration.Integration = (*Integration)(nil)

// New returns the CSP integration.
func New(opts Options) *Integration {
	opts.Namer = integration.NamerOrFallback(opts.Namer)
	if opts.Running == nil {
		opts.Running = cspfile.Running
	}
	bk := cspfile.NewBackups(filepath.Join(opts.PadmateDir, "integration-backups", ID))
	bk.Now = opts.Now
	return &Integration{opts: opts, backups: bk}
}

// ID is "csp".
func (*Integration) ID() string { return ID }

// Name is "CLIP STUDIO PAINT".
func (*Integration) Name() string { return Name }

// Exes is CLIPStudioPaint.exe.
func (*Integration) Exes() []string { return []string{cspfile.ExeName} }

// Running reports whether CSP runs, by image name.
func (in *Integration) Running() (bool, error) { return in.opts.Running() }

// files are the paths of the settings files of one CSP data folder.
type files struct {
	dir      string
	khc      string // Shortcut\default.khc
	todb     string // Tool\EditImageTool.todb
	appConf  string // Preference\Config.sqlite
	hasKHC   bool
	hasTODB  bool
	hasAConf bool
}

func filesOf(dir string) files {
	f := files{
		dir:     dir,
		khc:     filepath.Join(dir, "Shortcut", "default.khc"),
		todb:    filepath.Join(dir, "Tool", "EditImageTool.todb"),
		appConf: filepath.Join(dir, "Preference", "Config.sqlite"),
	}
	f.hasKHC = isFile(f.khc)
	f.hasTODB = isFile(f.todb)
	f.hasAConf = isFile(f.appConf)
	return f
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// locate finds the data folder: the override when set, else the newest of
// the folders found. It returns an *integration.UnavailableError when there
// is none.
func (in *Integration) locate() (files, error) {
	if in.opts.DataDir != nil {
		if dir := in.opts.DataDir(); dir != "" {
			f := filesOf(dir)
			if !f.hasKHC && !f.hasTODB {
				return files{}, &integration.UnavailableError{Problem: integration.ProblemNotFound,
					Err: fmt.Errorf("no Shortcut\\default.khc or Tool\\EditImageTool.todb in %s", dir)}
			}
			return f, nil
		}
	}
	find := in.opts.FindDataDirs
	if find == nil {
		if runtime.GOOS != "windows" {
			return files{}, &integration.UnavailableError{Problem: integration.ProblemUnsupported}
		}
		find = cspfile.FindSystemDataDirs
	}
	dirs := find()
	if len(dirs) == 0 {
		return files{}, &integration.UnavailableError{Problem: integration.ProblemNotFound}
	}
	return filesOf(dirs[0].Path), nil
}

// Locate says where CSP's data is and whether the files can be read.
func (in *Integration) Locate() integration.Location {
	f, err := in.locate()
	loc := integration.Location{DataDir: f.dir}
	if find := in.opts.FindInstallDir; find != nil {
		if d, err := find(); err == nil {
			loc.InstallDir = d.Root
		}
	} else if d, err := cspfile.FindInstallDir(); err == nil {
		loc.InstallDir = d.Root
	}
	if err != nil {
		var un *integration.UnavailableError
		if errors.As(err, &un) {
			loc.Problem = un.Problem
		} else {
			loc.Problem = integration.ProblemNotFound
		}
		return loc
	}
	if _, err := in.snapshot(); err != nil {
		var un *integration.UnavailableError
		if errors.As(err, &un) {
			loc.Problem = un.Problem
		} else {
			loc.Problem = integration.ProblemNotFound
		}
		return loc
	}
	loc.Found = true
	return loc
}

// Revision identifies the state of the files.
func (in *Integration) Revision() (string, error) {
	s, err := in.snapshot()
	if err != nil {
		return "", err
	}
	return s.revision, nil
}

// recheck is the Recheck of cspfile: CSP must not be running.
func (in *Integration) recheck() error {
	running, err := in.opts.Running()
	if err != nil {
		return fmt.Errorf("checking whether CLIP STUDIO PAINT runs: %w", err)
	}
	if running {
		return integration.ErrRunning
	}
	return nil
}
