// Package csp is the pure model of the CLIP STUDIO PAINT (CSP) shortcut
// settings (ADR-0019): the command shortcuts in Shortcut\default.khc, the tool
// shortcuts in Tool\EditImageTool.todb, the conversion between CSP key names
// and padmate chords, conflict analysis, and the comparison behind the
// configuration sync check.
//
// Both files are plain SQLite databases of an undocumented format. The readers
// are tolerant of extra columns and tables and fail with ErrUnknownFormat when
// a required table, column or link is missing, so that the caller can switch
// the CSP feature off. The edit functions modify only the file path they are
// given (a copy; backup, locking, fingerprints and atomic replacement belong to
// the caller) and touch nothing but the shortcut columns.
//
// The package has no platform dependencies and builds and tests on every OS.
// SQLite is modernc.org/sqlite (pure Go); all SQL lives in db.go, khc.go,
// todb.go and edit.go.
package csp

import "errors"

// ErrUnknownFormat marks a file whose structure is not the one this package
// understands: a required table or column is missing, or the tool tree has a
// bad link. Errors wrap it with the detail.
var ErrUnknownFormat = errors.New("csp: unknown file format")

// ItemKind says whether a function is a command (default.khc) or a tool
// (EditImageTool.todb).
type ItemKind uint8

// Function kinds.
const (
	KindCommand ItemKind = iota + 1
	KindTool
)

// String returns "command", "tool" or "invalid".
func (k ItemKind) String() string {
	switch k {
	case KindCommand:
		return "command"
	case KindTool:
		return "tool"
	}
	return "invalid"
}
