package latency

import (
	"fmt"
	"io"
	"time"
)

// Gate is whether a report's p99 gate was evaluated.
type Gate int

const (
	GateOff     Gate = iota // turned off (PADMATE_BENCH_GATE=0) or not applicable (info-only reports)
	GateSkipped             // could not be evaluated: no injection, or a gated row below MinN
	GateEvaluated
)

func (g Gate) String() string {
	switch g {
	case GateOff:
		return "off"
	case GateSkipped:
		return "skipped"
	case GateEvaluated:
		return "evaluated"
	}
	return fmt.Sprintf("Gate(%d)", int(g))
}

// Stage is one hop of a row's latency.
type Stage struct {
	Name    string // "sdl→slot", ...
	Summary Summary
}

// Row is one measured configuration.
type Row struct {
	Name      string        // "A", "B", ..., "M1"
	Gap       time.Duration // idle before each edge
	Sender    string        // "SendInput", "no-op"
	Events    string        // events per Send: "1+3", "1", "3"
	Total     Summary       // L
	Stages    []Stage
	HookFirst int           // edges whose last hook stamp is before t_ret; -1: no hook
	GCs       uint64        // GC cycles during the row (runtime/metrics /gc/cycles/total:gc-cycles)
	Target    time.Duration // p99 gate; 0 means info only
	MinN      int           // the sample count the gate needs; 0 for info rows
	Note      string
}

// Gated reports whether the row has a p99 gate.
func (r Row) Gated() bool { return r.Target > 0 }

// Enough reports whether the row has the samples its gate needs.
func (r Row) Enough() bool { return r.Total.N >= r.MinN }

// Pass reports whether the row meets its gate; info rows always do.
func (r Row) Pass() bool { return !r.Gated() || r.Total.P99 < r.Target }

// Report is the results of one latency test.
type Report struct {
	Title       string
	Env         Environment
	Calibration string // width, both frequencies, start/end agreement
	Gate        Gate
	GateReason  string // why it was not evaluated; "" when evaluated
	Rows        []Row
	Split       []Row // gated rows split by events per Send; info only
	Notes       []string
}

// minP999 is the least N for which p99.9 is shown: with fewer samples it
// is one of the last few.
const minP999 = 1000

// WriteMarkdown writes the report in the style of SPK-03: a heading, the
// gate line, the environment as a list, the calibration, then the tables.
// The gate line comes first, so that a report whose gate was not
// evaluated cannot be read as a pass.
func (r Report) WriteMarkdown(w io.Writer) error {
	m := &mdWriter{w: w}
	m.printf("## %s\n\n", r.Title)
	if r.Gate == GateEvaluated {
		m.printf("**gate: evaluated**\n\n")
	} else {
		m.printf("**GATE NOT EVALUATED** (%s): %s\n\n", r.Gate, r.GateReason)
	}
	e := r.Env
	m.printf("- Date: %s\n- Go: %s\n- OS: %s\n- CPUs: %d, GOMAXPROCS %d\n- Counter: %d Hz\n",
		e.Date, e.GoVersion, e.OS, e.NumCPU, e.GOMAXPROCS, e.Freq)
	for _, x := range e.Extra {
		m.printf("- %s\n", x)
	}
	if r.Calibration != "" {
		m.printf("\nCalibration: %s\n", r.Calibration)
	}

	m.printf("\n| Row | Gap | Sender | Events | N | p50 | p90 | p99 | p99.9 | max | GCs | Target | Result |\n")
	m.printf("|---|---:|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|\n")
	for _, row := range r.Rows {
		m.row(row)
		switch {
		case !row.Gated():
			m.printf(" – | info |\n")
		case r.Gate != GateEvaluated:
			m.printf(" p99 < %s | not evaluated |\n", row.Target)
		case row.Pass():
			m.printf(" p99 < %s | pass |\n", row.Target)
		default:
			m.printf(" p99 < %s | FAIL |\n", row.Target)
		}
	}
	m.rowNotes(r.Rows)

	if len(r.Split) > 0 {
		m.printf("\n### Split by events per Send\n\n")
		m.printf("| Row | Gap | Sender | Events | N | p50 | p90 | p99 | p99.9 | max | GCs |\n")
		m.printf("|---|---:|---|---|---:|---:|---:|---:|---:|---:|---:|\n")
		for _, row := range r.Split {
			m.row(row)
			m.printf("\n")
		}
		m.rowNotes(r.Split)
	}

	var staged bool
	for _, row := range r.Rows {
		staged = staged || len(row.Stages) > 0 || row.HookFirst >= 0
	}
	if staged {
		m.printf("\n### Stages\n")
		for _, row := range r.Rows {
			if len(row.Stages) > 0 {
				m.printf("\n| Row | Stage | p50 | p99 | max |\n|---|---|---:|---:|---:|\n")
				for _, s := range row.Stages {
					q := s.Summary
					m.printf("| %s | %s | %s | %s | %s |\n", row.Name, s.Name, quantile(q, q.P50), quantile(q, q.P99), quantile(q, q.Max))
				}
			}
			if row.HookFirst >= 0 {
				m.printf("\n%s: hook ran before SendInput returned: %d of %d\n", row.Name, row.HookFirst, row.Total.N)
			}
		}
	}

	if len(r.Notes) > 0 {
		m.printf("\nNotes:\n\n")
		for _, n := range r.Notes {
			m.printf("- %s\n", n)
		}
	}
	return m.err
}

// row writes the columns up to GCs, and leaves the line open.
func (m *mdWriter) row(r Row) {
	t := r.Total
	p999 := "–"
	if t.N >= minP999 {
		p999 = FormatDuration(t.P999)
	}
	m.printf("| %s | %s | %s | %s | %d | %s | %s | %s | %s | %s | %d |", r.Name, r.Gap, r.Sender, r.Events, t.N,
		quantile(t, t.P50), quantile(t, t.P90), quantile(t, t.P99), p999, quantile(t, t.Max), r.GCs)
}

// rowNotes lists the notes of rows, if any has one.
func (m *mdWriter) rowNotes(rows []Row) {
	var listed bool
	for _, r := range rows {
		if r.Note == "" {
			continue
		}
		if !listed {
			m.printf("\n")
			listed = true
		}
		m.printf("- %s: %s\n", r.Name, r.Note)
	}
}

// quantile formats d, a quantile of s, as "–" when s has no samples.
func quantile(s Summary, d time.Duration) string {
	if s.N == 0 {
		return "–"
	}
	return FormatDuration(d)
}

// FormatDuration formats d as "123.4 µs" below 1 ms and as "1.234 ms"
// from there, truncated toward zero to 0.1 µs and 1 µs. Truncation keeps
// the gate table consistent: against a whole-µs Target, a p99 is shown
// at or above the Target exactly when the row fails.
func FormatDuration(d time.Duration) string {
	u := uint64(d) // |d|, exact even for math.MinInt64
	sign := ""
	if d < 0 {
		u = -u
		sign = "-"
	}
	if u < uint64(time.Millisecond) {
		r := u / 100 // tenths of a µs
		if r == 0 {
			sign = ""
		}
		return fmt.Sprintf("%s%d.%d µs", sign, r/10, r%10)
	}
	r := u / 1000 // µs
	return fmt.Sprintf("%s%d.%03d ms", sign, r/1000, r%1000)
}

// mdWriter writes Markdown and keeps the first error.
type mdWriter struct {
	w   io.Writer
	err error
}

func (m *mdWriter) printf(format string, args ...any) {
	if m.err == nil {
		_, m.err = fmt.Fprintf(m.w, format, args...)
	}
}
