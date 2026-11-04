package integration

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Integration is one piece of software padmate reads and writes the shortcut
// settings of. Methods may be called from several goroutines; reading never
// changes the software's files.
type Integration interface {
	// ID is the lower-case ID (profile.ValidIntegrationID) that names
	// integrations/<id>.json.
	ID() string
	// Name is the software's name for people.
	Name() string
	// Exes are the executable file names of the software; a profile is bound
	// to the integration by a rule for one of them (Bound).
	Exes() []string

	// Locate says where the data is and whether it can be used.
	Locate() Location
	// Revision identifies the state of the software's files.
	Revision() (string, error)
	// Running reports whether the software runs.
	Running() (bool, error)

	// Functions lists everything that can have a shortcut, named in lang
	// ("zh-CN", "en", ...).
	Functions(lang string) (Listing, error)
	// Check says who owns keys (a KeySpec chord) in the software. function,
	// when not empty, is the function that would take the key: a key it
	// already holds is "same", and other tools sharing a tool's key are a
	// "cycle". An invalid chord is an error.
	Check(function, keys, lang string) (CheckResult, error)
	// Compare compares items with the software's current settings.
	Compare(items []Item, lang string) ([]ItemStatus, string, error)
	// Plan says, for each item that is not consistent, what it takes to
	// make the software match it.
	Plan(items []Item, lang string) (Plan, error)

	// Write changes the software's settings. Every change is validated
	// together against the current files before anything is written
	// (*ChangeError); the software must not run (ErrRunning); the files are
	// backed up first; a file that changes under the write gives ErrBusy.
	Write(changes []Change) (WriteResult, error)
	// Backups lists the backups, newest first.
	Backups() ([]BackupInfo, error)
	// Restore puts a backup back (the software must not run) after backing
	// up the current files, and returns that new backup and the revision.
	Restore(id string) (WriteResult, error)
}

// Registry holds the integrations by ID.
type Registry struct {
	mu   sync.RWMutex
	byID map[string]Integration
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{byID: map[string]Integration{}} }

// Register adds in. It panics on an invalid or duplicate ID: both are
// programming errors.
func (r *Registry) Register(in Integration) {
	id := in.ID()
	if !profile.ValidIntegrationID(id) {
		panic(fmt.Sprintf("integration: invalid ID %q", id))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.byID[id]; dup {
		panic(fmt.Sprintf("integration: %q registered twice", id))
	}
	r.byID[id] = in
}

// Get returns the integration with the ID.
func (r *Registry) Get(id string) (Integration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	in, ok := r.byID[id]
	return in, ok
}

// All returns the integrations sorted by ID.
func (r *Registry) All() []Integration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Integration, 0, len(r.byID))
	for _, in := range r.byID {
		out = append(out, in)
	}
	slices.SortFunc(out, func(a, b Integration) int { return strings.Compare(a.ID(), b.ID()) })
	return out
}

// Bound reports whether profile profileID is bound to an integration whose
// executables are exes.
//
// This is the ONLY place that decides it. Every caller (integrations.list,
// the check when a profile becomes active, anything else) goes through Bound
// or BoundProfiles, so a later change of the rule (an explicit switch in
// addition to or instead of the program rule) touches this function alone.
//
// A profile is bound when profile.Config.Select, the function that picks the
// profile of the foreground program, would pick it for one of the
// integration's programs. So the rule order counts (the first enabled rule
// that matches decides), a disabled rule or one without a profile binds
// nothing, and when automatic selection is off (cfg.Enabled false) no rule
// selects anything, so nothing is bound.
//
// Where the program lives is not known, so two kinds of location are tried
// for each exe: its bare file name (the program is somewhere no full-path
// rule names; only a rule with a bare name can select it) and each full
// path that an enabled rule names for a program with that file name. The
// profile is bound if Select picks it for any of them. A full-path rule that
// comes after a bare-name rule for the same program therefore binds nothing,
// because the bare-name rule always wins first.
func Bound(cfg *profile.Config, profileID string, exes []string) bool {
	if cfg == nil || !cfg.Enabled {
		return false
	}
	for _, loc := range locations(cfg.Rules, exes) {
		if sel := cfg.Select(loc, ""); sel.Reason == "rule" && sel.Profile == profileID {
			return true
		}
	}
	return false
}

// BoundProfiles lists the IDs of the profiles bound to an integration whose
// executables are exes, sorted and without duplicates. It is built on Bound.
func BoundProfiles(cfg *profile.Config, exes []string) []string {
	if cfg == nil {
		return nil
	}
	var out []string
	for _, r := range cfg.Rules {
		if r.Profile == nil || slices.Contains(out, *r.Profile) {
			continue
		}
		if Bound(cfg, *r.Profile, exes) {
			out = append(out, *r.Profile)
		}
	}
	slices.Sort(out)
	return out
}

// locations are the program paths Bound asks Select about: each exe by its
// bare name, and each absolute path an enabled rule names whose file name is
// an exe (compared ignoring case).
func locations(rules []profile.Rule, exes []string) []string {
	out := slices.Clone(exes)
	for _, r := range rules {
		if !r.Enabled || r.Profile == nil || !strings.ContainsAny(r.Exe, `\/`) {
			continue
		}
		base := path.Base(strings.ReplaceAll(r.Exe, `\`, "/"))
		if slices.ContainsFunc(exes, func(e string) bool { return strings.EqualFold(e, base) }) {
			out = append(out, r.Exe)
		}
	}
	return out
}

// Namer supplies the names of the software's functions that do not come from
// its settings files (CSP: command names, menu paths, categories), so that
// they can be plugged in separately from the integration.
type Namer interface {
	// CommandName returns the name, the menu path and the category ID of a
	// command in lang. ok is false when the namer does not know it.
	CommandName(id, lang string) (name string, path []string, category string, ok bool)
	// Categories lists the categories in lang, in display order.
	Categories(lang string) []Category
}

// OtherCategory is the category of a function the namer does not know.
const OtherCategory = "other"

// FallbackNamer names a command by its ID and puts it in OtherCategory.
type FallbackNamer struct{}

// CommandName returns id as the name.
func (FallbackNamer) CommandName(id, _ string) (string, []string, string, bool) {
	return id, nil, OtherCategory, true
}

// Categories returns OtherCategory only.
func (FallbackNamer) Categories(lang string) []Category {
	return []Category{OtherCategoryIn(lang)}
}

// OtherCategoryIn is the "other" category named in lang.
func OtherCategoryIn(lang string) Category {
	if strings.HasPrefix(strings.ToLower(lang), "zh") {
		return Category{ID: OtherCategory, Name: "其他"}
	}
	return Category{ID: OtherCategory, Name: "Other"}
}

// NamerOrFallback returns n, or FallbackNamer when n is nil.
func NamerOrFallback(n Namer) Namer {
	if n == nil {
		return FallbackNamer{}
	}
	return n
}
