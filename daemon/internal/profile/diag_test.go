package profile

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestPosition(t *testing.T) {
	const bom = "\xEF\xBB\xBF"
	for _, tc := range []struct {
		name      string
		data      string
		off       int
		line, col int
	}{
		{"empty", "", 0, 1, 1},
		{"start", "abc", 0, 1, 1},
		{"middle", "abc", 2, 1, 3},
		{"EOF", "abc", 3, 1, 4},
		{"newline itself", "a\nb", 1, 1, 2},
		{"after newline", "a\nb", 2, 2, 1},
		{"EOF after newline", "a\n", 2, 2, 1},
		{"CR belongs to its line", "a\r\nb", 1, 1, 2},
		{"LF after CR", "a\r\nb", 2, 1, 3},
		{"after CRLF", "a\r\nb", 3, 2, 1},
		{"third CRLF line", "a\r\nb\r\n  c", 8, 3, 3},
		{"lone CR is not a line end", "a\rb", 2, 1, 3},
		{"tab is one column", "\t\tx", 2, 1, 3},
		{"Chinese", "撤销x", 6, 1, 3},
		{"Chinese first rune", "撤销x", 3, 1, 2},
		{"Chinese label then error", "{\"label\": \"撤销\"}", 17, 1, 14},
		{"emoji", "😀x", 4, 1, 2},
		{"invalid bytes count one each", "\xff\xfex", 2, 1, 3},
		{"BOM is not a column", bom + "{", 3, 1, 1},
		{"inside BOM", bom + "{", 1, 1, 1},
		{"after BOM", bom + "ab", 4, 1, 2},
		{"BOM then second line", bom + "{\n}", 5, 2, 1},
		{"only a leading BOM is skipped", bom + "a\n" + bom + "b", 8, 2, 2},
		{"BOM and EOF", bom, 3, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line, col := position([]byte(tc.data), tc.off)
			if line != tc.line || col != tc.col {
				t.Errorf("position(%q, %d) = %d:%d, want %d:%d", tc.data, tc.off, line, col, tc.line, tc.col)
			}
		})
	}
}

// TestCursor checks that a cursor gives the same positions as position
// for increasing, repeated and decreasing offsets.
func TestCursor(t *testing.T) {
	data := []byte("\xEF\xBB\xBF{\r\n  \"撤销\": 1,\n\t\"😀\": [\xff]\n}")
	c := newCursor(data)
	offs := []int{0, 3, 4, 5, 6, 9, 9, 12, 13, 2, 20, 17, 25, len(data), 7, len(data)}
	for _, off := range offs {
		line, col := c.at(off)
		wl, wc := position(data, off)
		if line != wl || col != wc {
			t.Errorf("cursor at %d = %d:%d, position = %d:%d", off, line, col, wl, wc)
		}
	}
}

func TestDiagnosticString(t *testing.T) {
	for _, tc := range []struct {
		d    Diagnostic
		want string
	}{
		{Diagnostic{Severity: SeverityError, Pointer: "/layers/base/southh", Line: 12, Col: 5, Message: `unknown control "southh"`},
			`12:5: error: unknown control "southh" (/layers/base/southh)`},
		{Diagnostic{Severity: SeverityWarning, Pointer: "/layers/base/lt", Line: 3, Col: 7, Message: "binding has no action"},
			`3:7: warning: binding has no action (/layers/base/lt)`},
		{Diagnostic{Severity: SeverityError, Line: 1, Col: 1, Message: "the file is empty"},
			`1:1: error: the file is empty`},
	} {
		if got := tc.d.String(); got != tc.want {
			t.Errorf("String() = %q, want %q", got, tc.want)
		}
	}
}

func TestSeverityText(t *testing.T) {
	for _, s := range []Severity{SeverityError, SeverityWarning} {
		text, err := s.MarshalText()
		if err != nil {
			t.Fatal(err)
		}
		if string(text) != s.String() {
			t.Errorf("MarshalText = %q, String = %q", text, s.String())
		}
		var back Severity
		if err := back.UnmarshalText(text); err != nil || back != s {
			t.Errorf("UnmarshalText(%q) = %v, %v; want %v", text, back, err, s)
		}
	}
	if got := Severity(7).String(); got != "severity(7)" {
		t.Errorf("Severity(7).String() = %q", got)
	}
	if _, err := Severity(7).MarshalText(); err == nil {
		t.Error("MarshalText of an invalid severity succeeded")
	}
	var s Severity
	if err := s.UnmarshalText([]byte("fatal")); err == nil {
		t.Error(`UnmarshalText("fatal") succeeded`)
	}
}

func TestDiagnosticJSON(t *testing.T) {
	d := Diagnostic{Severity: SeverityWarning, Pointer: "/layers/base/a", Offset: 40, Line: 3, Col: 7, Message: "binding has no action"}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"severity":"warning","pointer":"/layers/base/a","offset":40,"line":3,"col":7,"message":"binding has no action"}`
	if string(b) != want {
		t.Errorf("json.Marshal = %s\nwant %s", b, want)
	}
	var back Diagnostic
	if err := json.Unmarshal(b, &back); err != nil || back != d {
		t.Errorf("round trip = %+v, %v", back, err)
	}
}

func TestDiagnosticsErr(t *testing.T) {
	warn := Diagnostic{Severity: SeverityWarning, Pointer: "/layers/base/lt", Line: 3, Col: 7, Message: "binding has no action"}
	e1 := Diagnostic{Severity: SeverityError, Pointer: "/name", Line: 2, Col: 11, Message: "name must not be empty"}
	e2 := Diagnostic{Severity: SeverityError, Pointer: "/schemaVersion", Line: 4, Col: 3, Message: "schemaVersion must be 0"}

	for _, ds := range []Diagnostics{nil, {}, {warn}} {
		if ds.HasErrors() {
			t.Errorf("%v.HasErrors() = true", ds)
		}
		if err := ds.Err(); err != nil {
			t.Errorf("%v.Err() = %v, want nil", ds, err)
		}
	}

	for _, tc := range []struct {
		ds   Diagnostics
		want string
	}{
		{Diagnostics{warn, e1}, "2:11: error: name must not be empty (/name)"},
		{Diagnostics{warn, e1, e2}, "2:11: error: name must not be empty (/name) (and 1 more error)"},
		{Diagnostics{e1, e2, e2, warn}, "2:11: error: name must not be empty (/name) (and 2 more errors)"},
	} {
		if !tc.ds.HasErrors() {
			t.Errorf("%v.HasErrors() = false", tc.ds)
		}
		err := tc.ds.Err()
		if err == nil || err.Error() != tc.want {
			t.Errorf("Err() = %v, want %q", err, tc.want)
			continue
		}
		var perr *Error
		if !errors.As(err, &perr) || len(perr.Diagnostics) != len(tc.ds) {
			t.Errorf("Err() = %#v, want an *Error with all diagnostics", err)
		}
		if strings.Contains(err.Error(), "warning") {
			t.Errorf("Err() = %q mentions the warning", err)
		}
	}
}
