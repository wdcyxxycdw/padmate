package profile

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Severity says whether a diagnostic stops a profile from loading.
type Severity uint8

const (
	// SeverityError: the profile is rejected.
	SeverityError Severity = iota
	// SeverityWarning: the profile loads, but probably not as intended.
	SeverityWarning
)

// String returns "error" or "warning", or "severity(N)" for invalid values.
func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	}
	return "severity(" + strconv.Itoa(int(s)) + ")"
}

// MarshalText implements encoding.TextMarshaler.
func (s Severity) MarshalText() ([]byte, error) {
	if s > SeverityWarning {
		return nil, fmt.Errorf("invalid severity %d", s)
	}
	return []byte(s.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (s *Severity) UnmarshalText(text []byte) error {
	switch string(text) {
	case "error":
		*s = SeverityError
	case "warning":
		*s = SeverityWarning
	default:
		return fmt.Errorf("invalid severity %q", text)
	}
	return nil
}

// Diagnostic is one problem found in a profile, located both by JSON
// Pointer and by position in the file.
//
// Col counts code points, not UTF-16 units, so after a character outside
// the BMP, such as an emoji, it is smaller than the column of editors that
// count UTF-16 units (VS Code, Monaco). They should convert from Offset,
// which is exact.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Pointer  string   `json:"pointer"` // RFC 6901 JSON Pointer of the value, "" for the root
	Offset   int      `json:"offset"`  // 0-based byte offset in the file
	Line     int      `json:"line"`    // 1-based
	Col      int      `json:"col"`     // 1-based, in Unicode code points from the line start
	Message  string   `json:"message"`
	// Profile is the library ID of the parent whose file has the problem when
	// that is not the file that was checked (PRO-19, ADR-0070): Pointer,
	// Offset, Line and Col are then those of the parent's file. "" is the
	// checked file itself.
	Profile string `json:"profile,omitempty"`
}

// String formats d as "line:col: severity: message (pointer)", leaving out
// the pointer when it is the root. Callers prefix the file name: the parent's
// for a problem in a parent's file (Profile).
func (d Diagnostic) String() string {
	s := strconv.Itoa(d.Line) + ":" + strconv.Itoa(d.Col) + ": " + d.Severity.String() + ": " + d.Message
	if d.Pointer != "" {
		s += " (" + d.Pointer + ")"
	}
	return s
}

// Diagnostics is the result of checking a profile, in file order.
type Diagnostics []Diagnostic

// Local returns the diagnostics that are in the file that was checked, not
// in a parent's: the ones whose positions are valid for its text. It is ds
// itself when there are no others.
func (ds Diagnostics) Local() Diagnostics {
	if !slices.ContainsFunc(ds, func(d Diagnostic) bool { return d.Profile != "" }) {
		return ds
	}
	local := make(Diagnostics, 0, len(ds))
	for _, d := range ds {
		if d.Profile == "" {
			local = append(local, d)
		}
	}
	return local
}

// HasErrors reports whether any diagnostic is an error.
func (ds Diagnostics) HasErrors() bool {
	return slices.ContainsFunc(ds, func(d Diagnostic) bool { return d.Severity == SeverityError })
}

// Err returns an *Error holding ds if ds has errors, and nil otherwise.
func (ds Diagnostics) Err() error {
	if !ds.HasErrors() {
		return nil
	}
	return &Error{Diagnostics: ds}
}

// Error is the error returned by Diagnostics.Err.
type Error struct {
	Diagnostics Diagnostics // all of them, warnings included
}

// Error returns the first error and the number of further errors.
func (e *Error) Error() string {
	first, n := "", 0
	for _, d := range e.Diagnostics {
		if d.Severity != SeverityError {
			continue
		}
		if n == 0 {
			first = d.String()
			if d.Profile != "" {
				first = "parent " + strconv.Quote(d.Profile) + ": " + first
			}
		}
		n++
	}
	switch n {
	case 0:
		return "no errors"
	case 1:
		return first
	case 2:
		return first + " (and 1 more error)"
	}
	return first + " (and " + strconv.Itoa(n-1) + " more errors)"
}

// bom is the UTF-8 byte order mark that Notepad writes.
var bom = []byte{0xEF, 0xBB, 0xBF}

// bomLen returns the length of a leading BOM in data: 3 or 0.
func bomLen(data []byte) int {
	if bytes.HasPrefix(data, bom) {
		return len(bom)
	}
	return 0
}

// TrimBOM returns data without a leading UTF-8 BOM, which Parse accepts
// (Notepad writes one) and encoding/json refuses.
func TrimBOM(data []byte) []byte { return data[bomLen(data):] }

// position returns the 1-based line and column of byte offset off in data.
// A line ends at "\n"; a "\r" before it is part of the line. The column
// counts the code points that start before off on its line, so a tab is one
// column and so is each byte of invalid UTF-8. A leading BOM is not a
// column.
func position(data []byte, off int) (line, col int) {
	return newCursor(data).at(off)
}

// cursor computes positions for a series of offsets. Increasing offsets
// cost linear time in total, so diagnostics sorted by offset are
// positioned in one pass.
type cursor struct {
	data      []byte
	req       int // the last offset asked for
	off       int // bytes before off have been counted; off >= req
	line, col int // position of req
}

func newCursor(data []byte) *cursor {
	n := bomLen(data)
	return &cursor{data: data, req: n, off: n, line: 1, col: 1}
}

// at returns the position of off, which is clamped to the data.
func (c *cursor) at(off int) (line, col int) {
	off = min(max(off, 0), len(c.data))
	if off < c.req {
		*c = *newCursor(c.data)
	}
	c.req = max(c.req, off)
	for c.off < off {
		if c.data[c.off] == '\n' {
			c.line, c.col = c.line+1, 1
			c.off++
			continue
		}
		_, size := utf8.DecodeRune(c.data[c.off:])
		c.col++
		c.off += size
	}
	return c.line, c.col
}

// place sorts ds by offset, then message, drops exact duplicates and fills
// in the line and column of every diagnostic from its offset.
func place(data []byte, ds Diagnostics) Diagnostics {
	slices.SortFunc(ds, func(a, b Diagnostic) int {
		if a.Offset != b.Offset {
			return a.Offset - b.Offset
		}
		if c := strings.Compare(a.Message, b.Message); c != 0 {
			return c
		}
		if c := strings.Compare(a.Pointer, b.Pointer); c != 0 {
			return c
		}
		return int(a.Severity) - int(b.Severity)
	})
	ds = slices.Compact(ds)
	c := newCursor(data)
	for i := range ds {
		ds[i].Line, ds[i].Col = c.at(ds[i].Offset)
	}
	return ds
}
