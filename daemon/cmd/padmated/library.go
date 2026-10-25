package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

func resolveDataDir(opts *options) (err error) {
	if opts.dataDir == "" {
		opts.dataDir, err = profilelib.DefaultDir()
		if err != nil {
			return err
		}
	}
	opts.dataDir, err = filepath.Abs(opts.dataDir)
	if err != nil {
		return err
	}
	return err
}

// resolveLibrary opens the library of opts.dataDir: it reads config.json,
// repairing a file with errors (PRO-29: backed up and rewritten, or in a
// replay, which only reads, in memory; kept in configRepair for live mode
// to log and tell the UI), and state.json, where errors start an empty
// state (stateErr; live mode resets the file, a state.json it cannot
// read stays), creates the fixed default profile when it is missing
// (PRO-28, ADR-0053; a failure is kept in defaultErr, not fatal), moves
// the profiles' macros folders into the script library (SCR-51,
// MigrateMacros; what it did is kept for live mode to log, and a failure
// is not fatal either; a replay only reads, so it finds unmoved macros
// missing), and chooses the startup profile unless --profile names a
// file. An automatic start (no --profile-id) resumes state.json's last
// profile and otherwise starts with the default. Its errors are
// startupErrors (UI-45): of the library, when the data folder cannot be
// found or opened (a missing one is a first start), or of a config.json
// that cannot be read.
func resolveLibrary(opts *options) error {
	if err := resolveDataDir(opts); err != nil {
		return &startupError{kind: "library", prefix: "data directory", err: err}
	}
	if root, err := os.OpenRoot(opts.dataDir); err == nil {
		_ = root.Close()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return &startupError{kind: "library", file: opts.dataDir, prefix: "data directory", err: err}
	}
	opts.library = profilelib.New(opts.dataDir)
	settings, _, repair, err := opts.library.ReadRepairedConfig(!opts.replay())
	if err != nil {
		return &startupError{kind: "config", file: filepath.Join(opts.dataDir, "config.json"), prefix: "config.json", err: err}
	}
	opts.configRepair = repair
	state, err := opts.library.ReadState()
	if err != nil {
		opts.stateErr, state = err, profile.State{}
		if !opts.replay() && errors.Is(err, profilelib.ErrInvalidState) {
			opts.stateResetErr = opts.library.WriteState(state)
			opts.stateReset = opts.stateResetErr == nil
		}
	}
	opts.settings, opts.memory = settings, profilelib.NewMemory(state, settings.RestoreLayerState)
	if !opts.replay() {
		opts.defaultCreated, opts.defaultErr = opts.library.EnsureDefault()
		opts.macroMigration, opts.macroMigrationErr = opts.library.MigrateMacros()
	}
	if opts.profile != "" {
		return nil
	}
	opts.autoProfile = opts.profileID == ""
	if opts.autoProfile {
		opts.profileID = profilelib.DefaultID
		// A remembered profile deleted since (profiles.delete, or by hand)
		// falls back to the default instead of stopping the start; an
		// explicit --profile-id that is missing still fails. The mapping
		// disabled last (profiles.activate null) is no profile to resume.
		// ReadState has checked the id.
		if last := state.LastProfile; last != "" && last != profilelib.DefaultID {
			opts.profileID = last
			if _, err := opts.library.Read(last); errors.Is(err, fs.ErrNotExist) {
				opts.missingLastProfile, opts.profileID = last, profilelib.DefaultID
			}
		}
	}
	if opts.profile, err = opts.library.Path(opts.profileID); err != nil {
		return &startupError{kind: "library", err: err} // not reached: the id is valid
	}
	return nil
}

// disabledProfile is the profile that runs while mapping is disabled: no
// library profile is selected (profiles.activate null).
const disabledProfile = `{"schemaVersion":1,"name":"Mapping disabled","layers":{"base":{}}}`

// readLibraryProfile reads library profile id for selection. The fixed
// default never stops a selection (ADR-0053): when it is missing, create
// (live mode) writes it again (deleted by hand while padmated runs); when
// it still cannot be read, or does not parse, it runs as
// DefaultProfileText, so an application no rule binds never keeps the
// mapping of the profile before and an automatic start still starts.
// problem then says why; the file stays as it is, for profiles.get to show
// and the user to repair. A default missing in a replay runs as
// DefaultProfileText without a problem.
func readLibraryProfile(l *profilelib.Library, id string, create bool) (data []byte, problem, err error) {
	data, err = l.Read(id)
	if id != profilelib.DefaultID {
		return data, nil, err
	}
	if errors.Is(err, fs.ErrNotExist) {
		if !create {
			return []byte(profilelib.DefaultProfileText), nil, nil
		}
		if _, err = l.EnsureDefault(); err == nil {
			data, err = l.Read(id)
		}
	}
	if err == nil {
		migrated, ds := profile.Migrate(data)
		if !ds.HasErrors() {
			_, ds = l.Parse(id, migrated)
		}
		if err = ds.Err(); err == nil {
			return data, nil, nil
		}
	}
	return []byte(profilelib.DefaultProfileText), err, nil
}

// loadSelectedProfile reads, parses and compiles the profile live mode or
// a replay starts with: --profile's file, or the library's profile that
// resolveLibrary chose. It prints the problems to stderr. sum is the sha256
// of the bytes read, the content the watcher starts from. ok is false when
// the profile cannot be read or has errors.
func loadSelectedProfile(opts options, stderr io.Writer) (p *profile.Profile, prog *engine.Program, sum [32]byte, ok bool) {
	p, prog, sum, err := loadStartupProfile(opts, stderr)
	return p, prog, sum, err == nil
}

// loadStartupProfile is loadSelectedProfile with why it failed: a
// startupError of kind profile (UI-45).
func loadStartupProfile(opts options, stderr io.Writer) (p *profile.Profile, prog *engine.Program, sum [32]byte, err error) {
	var data []byte
	var lib *profilelib.Library
	switch {
	case opts.profileID != "":
		var problem error
		lib = profilelib.New(opts.dataDir)
		data, problem, err = readLibraryProfile(lib, opts.profileID, !opts.replay())
		if problem != nil {
			fmt.Fprintln(stderr, "padmated: default profile unusable; it runs without mapping:", problem)
		}
	case opts.profile != "" || opts.library == nil:
		data, err = os.ReadFile(opts.profile)
	default:
		data = []byte(disabledProfile)
	}
	if err != nil {
		fmt.Fprintln(stderr, "padmated:", profile.ReadError(opts.profile, err))
		return nil, nil, sum, profileFailure(opts.profile, nil, err)
	}
	p, prog, ds, err := parseProfileIn(lib, opts.profileID, opts.profile, data, stderr, opts.settings)
	if err != nil {
		return nil, nil, sum, profileFailure(opts.profile, ds, err)
	}
	return p, prog, sha256.Sum256(data), nil
}

// logMacroMigration logs what resolveLibrary's MigrateMacros did: each
// file it moved into the script library, the profiles whose references
// follow a new name, what stayed in a profile's macros folder (a link, a
// module the library has with other bytes, or a macro that needs another
// name while profile.json cannot follow), the modules that the library has
// with other bytes (an error: the macros that import them change), and why
// a profile could not be migrated.
func logMacroMigration(log *slog.Logger, m profilelib.MacroMigration, err error) {
	if err != nil {
		log.Error("macros not moved into the script library", "err", err)
	}
	for _, moved := range m.Moved {
		log.Info("macro moved into the script library", "profile", moved.Profile, "file", moved.From, "to", moved.To, "same", moved.Same)
	}
	for id, n := range m.Renamed {
		log.Info("macro references renamed", "profile", id, "bindings", n)
	}
	for _, left := range m.Left {
		log.Warn("file not moved into the script library; it stays in the profile's macros folder", "path", left)
	}
	for _, shadowed := range m.Shadowed {
		// Imports of a macro are not rewritten: the macros of that profile
		// that import the module run the library's file, or fail to build.
		log.Error("module not moved into the script library: the library has another file at that path, so the macros of this profile that import it use that file", "path", shadowed)
	}
	for _, err := range m.Errs {
		log.Error("macros of a profile not moved into the script library", "err", err)
	}
}
