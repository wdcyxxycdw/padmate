// Package integration is the generic side of padmate's software
// integrations (ADR-0019, ARCHITECTURE section 16): the types every
// integration speaks, the integrations/<id>.json sidecar file of a profile,
// the rule that binds a profile to an integration, and the registry.
//
// Nothing here knows a particular piece of software. An integration (CSP is
// the first, internal/integration/cspint) says where the software keeps its
// shortcuts, lists the functions that can have one, tells who owns a key,
// compares a profile's items with the software's current settings and
// writes the software's settings.
package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Function kinds.
const (
	KindCommand = "command"
	KindTool    = "tool"
)

// Function is something in the software that can have a shortcut.
type Function struct {
	// ID is opaque to everything but the integration.
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Path is where the function sits in the software's menus or tool tree.
	Path []string `json:"path"`
	// Category is the ID of one of the Categories of the same listing.
	Category string `json:"category"`
	// Keys are the function's shortcuts as KeySpec chords.
	Keys []string `json:"keys"`
	// Unconfirmed: a shortcut of the function is a key that has no confirmed
	// counterpart yet (symbol keys). Unsupported: a shortcut padmate cannot
	// spell (device buttons, unusual modifier bits).
	Unconfirmed bool `json:"unconfirmed,omitempty"`
	Unsupported bool `json:"unsupported,omitempty"`
	// Cycle lists the other functions that share the function's single key
	// (CLIP STUDIO PAINT's cycle shortcut).
	Cycle []string `json:"cycle,omitempty"`
}

// Category groups functions in the picker.
type Category struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Listing is the answer of Integration.Functions.
type Listing struct {
	// Revision identifies the state of the software's files the listing was
	// read from.
	Revision   string     `json:"revision"`
	Categories []Category `json:"categories"`
	Functions  []Function `json:"functions"`
}

// Item is one entry of integrations/<id>.json: the key is this function in
// the software. Name and Path are only for display.
type Item struct {
	// Keys is one KeySpec chord in canonical spelling.
	Keys     string   `json:"keys"`
	Function string   `json:"function"`
	Name     string   `json:"name,omitempty"`
	Path     []string `json:"path,omitempty"`
}

// Owner is a function that holds a key.
type Owner struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Path []string `json:"path"`
}

// Check statuses.
const (
	StatusFree        = "free"
	StatusSame        = "same"
	StatusCycle       = "cycle"
	StatusConflict    = "conflict"
	StatusUnconfirmed = "unconfirmed"
	StatusUnsupported = "unsupported"
)

// Check notes.
const (
	// NoteToolShift: holding the key switches tool temporarily.
	NoteToolShift = "toolShift"
	// NoteModifierKey: the key is taken by the software's modifier-key
	// settings. Not produced yet: that file is not decoded.
	NoteModifierKey = "modifierKey"
)

// CheckResult says who owns a key in the software.
type CheckResult struct {
	Status string   `json:"status"`
	Owners []Owner  `json:"owners"`
	Notes  []string `json:"notes"`
}

// Item states (ADR-0019 section 6).
const (
	StateConsistent = "consistent"
	StateChanged    = "changed"
	StateRemoved    = "removed"
	StateMissing    = "missing"
)

// ItemStatus is the comparison of one Item with the software.
type ItemStatus struct {
	Keys     string `json:"keys"`
	Function string `json:"function"`
	State    string `json:"state"`
	// Current are the function's shortcuts now (state changed).
	Current []string `json:"current,omitempty"`
	// Owners are the other functions that hold Keys now (state changed).
	Owners []Owner `json:"owners,omitempty"`
}

// DifferenceID identifies the set of differences in statuses: the hex
// SHA-256 of a canonical encoding of the statuses that are not consistent,
// in a stable order. It is "" when everything is consistent. "Not now" is
// stored with it, so the same differences are not offered again.
func DifferenceID(statuses []ItemStatus) string {
	var lines []string
	for _, s := range statuses {
		if s.State == StateConsistent {
			continue
		}
		cur := slices.Clone(s.Current)
		slices.Sort(cur)
		lines = append(lines, strings.Join([]string{s.Keys, s.Function, s.State, strings.Join(cur, "\x1f")}, "\x00"))
	}
	if len(lines) == 0 {
		return ""
	}
	slices.Sort(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// Consistent reports whether every status is consistent.
func Consistent(statuses []ItemStatus) bool {
	for _, s := range statuses {
		if s.State != StateConsistent {
			return false
		}
	}
	return true
}

// Change adds or removes one key of one function. For a command Add gives it
// one more shortcut. A tool has a single key: Add sets it, Remove clears it
// (and must name the key it has). Both are KeySpec chords.
type Change struct {
	Function string `json:"function"`
	Add      string `json:"add,omitempty"`
	Remove   string `json:"remove,omitempty"`
}

// Displaced is a function that loses Keys when a plan is carried out.
type Displaced struct {
	Function string `json:"function"`
	Name     string `json:"name"`
	Keys     string `json:"keys"`
}

// Skip reasons.
const (
	// SkipMissing: the function is not in the software any more.
	SkipMissing = "missing"
	// SkipUnsupportedKey: the software cannot have the key on that function
	// (a tool takes only A-Z, a key the software does not have).
	SkipUnsupportedKey = "unsupportedKey"
	// SkipUnconfirmedKey: the key is a symbol key that is not confirmed yet.
	SkipUnconfirmedKey = "unconfirmedKey"
	// SkipBadFunction: the function ID is not one this integration makes.
	SkipBadFunction = "badFunction"
)

// PlanEntry is what it takes to make the software match one item that is
// not consistent.
type PlanEntry struct {
	Item      Item        `json:"item"`
	Changes   []Change    `json:"changes"`
	Displaced []Displaced `json:"displaced"`
	// Skipped is the reason the item cannot be written, or "".
	Skipped string `json:"skipped,omitempty"`
}

// Plan is the result of Integration.Plan: one entry per item that is not
// consistent, in item order. The caller (the UI) lets the user decide each
// conflict and assembles the final changes itself.
type Plan struct {
	Entries []PlanEntry `json:"entries"`
}

// BackupInfo describes one backup of the software's files.
type BackupInfo struct {
	ID      string    `json:"id"`
	Created time.Time `json:"created"`
	// Reason is why it was made: "write", "before-restore" or "".
	Reason string `json:"reason,omitempty"`
}

// WriteResult is the result of Integration.Write.
type WriteResult struct {
	// Backup is the backup taken before writing; the zero value when the
	// changes changed nothing and no file was written.
	Backup   BackupInfo `json:"backup"`
	Revision string     `json:"revision"`
}

// Problems of an integration that cannot be used.
const (
	ProblemNotFound      = "notFound"
	ProblemUnknownFormat = "unknownFormat"
	ProblemUnsupported   = "unsupported"
)

// Location says where the software's data is and whether it can be used.
type Location struct {
	Found bool `json:"found"`
	// Problem is one of the Problem constants when Found is false.
	Problem    string `json:"problem,omitempty"`
	DataDir    string `json:"dataDir,omitempty"`
	InstallDir string `json:"installDir,omitempty"`
}

// Errors of the integrations. They are wrapped with detail; test with errors.Is
// or errors.As.
var (
	// ErrRunning: the software is running; its files are not touched.
	ErrRunning = errors.New("integration: the software is running")
	// ErrBusy: a file is in use or changed between reading and writing (a
	// journal file, a different fingerprint); nothing was written.
	ErrBusy = errors.New("integration: the software's files are in use or changed")
	// ErrNotFound: no such backup.
	ErrNotFound = errors.New("integration: not found")
)

// UnavailableError means the integration cannot be used; Problem is one of the
// Problem constants.
type UnavailableError struct {
	Problem string
	Err     error
}

func (e *UnavailableError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("integration unavailable (%s): %v", e.Problem, e.Err)
	}
	return "integration unavailable (" + e.Problem + ")"
}

func (e *UnavailableError) Unwrap() error { return e.Err }

// ChangeError refuses a set of changes as a whole: a function that does not
// exist, a key the software cannot have there, a duplicate or clashing key.
type ChangeError struct {
	Function string
	Keys     string
	Reason   string
}

func (e *ChangeError) Error() string {
	return fmt.Sprintf("integration: change of %s %s refused: %s", e.Function, e.Keys, e.Reason)
}

// Change refusal reasons.
const (
	ReasonUnknownFunction = "unknownFunction"
	ReasonBadKeys         = "badKeys"
	ReasonUnsupportedKey  = "unsupportedKey"
	ReasonNoSuchKey       = "noSuchKey"
	ReasonConflict        = "conflict"
)
