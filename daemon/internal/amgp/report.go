package amgp

import (
	"fmt"
	"strings"
)

// Severity says how much of a construct reached the padmate profile.
type Severity string

const (
	// SeverityDropped: the construct is not in the profile at all.
	SeverityDropped Severity = "dropped"
	// SeverityApproximate: the profile has something close to it, which
	// behaves a little differently.
	SeverityApproximate Severity = "approximate"
	// SeverityInfo: nothing was lost; the line says what was done, or
	// something the user should check.
	SeverityInfo Severity = "info"
)

// label is the word that text output uses for the severity.
func (s Severity) label() string {
	switch s {
	case SeverityDropped:
		return "丢弃"
	case SeverityApproximate:
		return "近似"
	}
	return "信息"
}

// maxReportLines bounds the report, so that a file with thousands of slots
// that cannot be imported does not make a report of the same size. The last
// line then says how many were left out.
const maxReportLines = 1000

// ReportLine is one thing the import did not carry over exactly. Set is the
// AntiMicroX set (1 to 8), 0 for the file; Control is the padmate control
// the binding belongs to ("leftshoulder", "ls.up"), or the AntiMicroX
// element when it has none; Slot is the 1-based position in the button's
// slot list, 0 when the line is not about one slot. Code is a stable name of
// the kind of line, for programs; Message is for the user.
type ReportLine struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Set      int      `json:"set,omitempty"`
	Control  string   `json:"control,omitempty"`
	Slot     int      `json:"slot,omitempty"`
	Message  string   `json:"message"`
}

// Location returns where the line applies, such as "set 2 / lt / slot 3",
// or "" for a line about the file.
func (l ReportLine) Location() string {
	var parts []string
	if l.Set > 0 {
		parts = append(parts, fmt.Sprintf("set %d", l.Set))
	}
	if l.Control != "" {
		parts = append(parts, l.Control)
	}
	if l.Slot > 0 {
		parts = append(parts, fmt.Sprintf("slot %d", l.Slot))
	}
	return strings.Join(parts, " / ")
}

// String returns the line as text output shows it: the severity, the
// location and the message.
func (l ReportLine) String() string {
	if loc := l.Location(); loc != "" {
		return fmt.Sprintf("%s  %s：%s", l.Severity.label(), loc, l.Message)
	}
	return fmt.Sprintf("%s  %s", l.Severity.label(), l.Message)
}

// FormatReport returns the lines as text, one to a line, each ending in a
// newline. It is what padmatectl prints and what the golden files hold.
func FormatReport(lines []ReportLine) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// Counts returns how many lines of each severity the report has.
func Counts(lines []ReportLine) (dropped, approximate, info int) {
	for _, l := range lines {
		switch l.Severity {
		case SeverityDropped:
			dropped++
		case SeverityApproximate:
			approximate++
		default:
			info++
		}
	}
	return dropped, approximate, info
}

// loc is where a line applies while the importer works.
type loc struct {
	set     int
	control string
	slot    int
}

func (l loc) withSlot(n int) loc {
	l.slot = n
	return l
}

// reporter collects the report. A repeated line, the same severity, code,
// place and message, is kept once.
type reporter struct {
	lines   []ReportLine
	seen    map[ReportLine]bool
	omitted int
}

func newReporter() *reporter {
	return &reporter{lines: []ReportLine{}, seen: map[ReportLine]bool{}}
}

func (r *reporter) add(sev Severity, code string, at loc, format string, args ...any) {
	line := ReportLine{Severity: sev, Code: code, Set: at.set, Control: at.control, Slot: at.slot, Message: fmt.Sprintf(format, args...)}
	if r.seen[line] {
		return
	}
	r.seen[line] = true
	if len(r.lines) >= maxReportLines {
		r.omitted++
		return
	}
	r.lines = append(r.lines, line)
}

func (r *reporter) dropped(code string, at loc, format string, args ...any) {
	r.add(SeverityDropped, code, at, format, args...)
}

func (r *reporter) approx(code string, at loc, format string, args ...any) {
	r.add(SeverityApproximate, code, at, format, args...)
}

func (r *reporter) info(code string, at loc, format string, args ...any) {
	r.add(SeverityInfo, code, at, format, args...)
}

// finish returns the report, with the line about the omitted ones.
func (r *reporter) finish() []ReportLine {
	if r.omitted > 0 {
		r.lines = append(r.lines, ReportLine{
			Severity: SeverityInfo, Code: "report.omitted",
			Message: fmt.Sprintf("还有 %d 条没有列出", r.omitted),
		})
	}
	return r.lines
}
