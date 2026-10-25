package main

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Problem is one violation, printed as "ROADMAP.md:<Line>: <Msg>".
type Problem struct {
	Line int
	Msg  string
}

// Priority is the value of a feature table's 优先级 column.
type Priority int

// Priorities as written in ROADMAP §3.
const (
	PriorityNone Priority = iota // spikes, deprecated rows and unparsable cells
	P0
	P1
	P1Early // P1（提前）: a P1 item scheduled in a milestone ≤ M14
	P2
	P2Optional // P2（可选）: a P2 item scheduled in a milestone
)

func (p Priority) String() string {
	switch p {
	case P0:
		return "P0"
	case P1:
		return "P1"
	case P1Early:
		return "P1（提前）"
	case P2:
		return "P2"
	case P2Optional:
		return "P2（可选）"
	}
	return "none"
}

func (p Priority) isP1() bool { return p == P1 || p == P1Early }
func (p Priority) isP2() bool { return p == P2 || p == P2Optional }

// priorities maps normalized 优先级 cells to their value.
var priorities = map[string]Priority{
	"P0":     P0,
	"P1":     P1,
	"P1(提前)": P1Early,
	"P2":     P2,
	"P2(可选)": P2Optional,
}

func parsePriority(s string) (Priority, bool) {
	p, ok := priorities[normalize(s)]
	return p, ok
}

// normalize drops spaces and emphasis and folds full-width parentheses, so
// "P1（提前）", "P1 (提前)" and "**P1(提前)**" compare equal.
func normalize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '（':
			return '('
		case r == '）':
			return ')'
		case r == '*' || unicode.IsSpace(r):
			return -1
		}
		return r
	}, s)
}

// backlog is the 里程碑 value of items that are not scheduled yet.
const backlog = "Backlog"

// Item is one row of a §3 feature table or of the §5.1 spike table.
type Item struct {
	ID              string
	Name            string
	Priority        Priority // PriorityNone for spikes and deprecated rows
	Milestone       string   // "M2", backlog, or "" when deprecated or unparsable
	Deprecated      bool     // the ID cell is struck through (~~ID~~)
	DeprecationNote bool     // a deprecated row says 废弃
	Spike           bool     // defined in the §5.1 spike table
	Section         string   // "§3.1", "§5.1", ...
	SectionPrefix   string   // ID prefix named by the section heading, if any
	Line            int
}

// Entry is one milestone checklist line or one Backlog list entry. It names
// a single ID, or a range "From 至 To" when To is set.
type Entry struct {
	From, To string
	Checked  bool
	Optional bool // the line is marked （可选）
	Line     int
}

func (e *Entry) String() string {
	if e.To == "" {
		return e.From
	}
	return e.From + " 至 " + e.To
}

// Milestone is a "### M<N> title（version）" section of §4.
type Milestone struct {
	Name    string // "M0a"
	Title   string
	Version string // "" when the heading carries none
	Line    int
	Entries []*Entry
}

// OverviewRow is one row of the §4.1 overview table.
type OverviewRow struct {
	Milestone string
	Title     string
	Version   string
	Status    string
	Line      int
}

// Current is the "**当前里程碑**：M0b SendInput 验证（未开始）。" line of §0.
type Current struct {
	Milestone string // "M0b"; "" for 无（全部完成）
	Title     string
	Status    string // text in the final parentheses
	Line      int
}

// Ref is an ID mentioned in running text, outside the structures above.
type Ref struct {
	ID   string
	Line int
}

// Roadmap is the parsed structure of ROADMAP.md.
type Roadmap struct {
	Items       []*Item // every definition in document order, duplicates included
	Overview    []*OverviewRow
	Milestones  []*Milestone
	Backlog     []*Entry
	BacklogLine int      // line of the "### Backlog" heading, 0 if absent
	SummaryLine int      // line of the "## 0." heading, 0 if absent
	CurrentLine int      // line of the first 当前里程碑 line in §0, 0 if absent
	Current     *Current // that line, nil if absent or malformed
	Refs        []Ref

	sawFeatures, sawSpikes, sawOverview bool
}

const spikePrefix = "SPK"

// prefixList holds the known ID prefixes: the §3 feature tables and SPK.
var prefixList = []string{
	"IN", "OUT", "MAP", "ANA", "LAY", "PRO", "SCR", "DMN",
	"IPC", "UI", "HUD", "MOU", "PKG", "ENG", spikePrefix,
}

var knownPrefix = func() map[string]bool {
	m := make(map[string]bool, len(prefixList))
	for _, p := range prefixList {
		m[p] = true
	}
	return m
}()

var idRe = regexp.MustCompile(`^([A-Z]+)-(\d{2,})$`)

// splitID splits a well-formed ID ("ANA-19") into its prefix and number.
func splitID(id string) (prefix string, n int, ok bool) {
	m := idRe.FindStringSubmatch(id)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false
	}
	return m[1], n, true
}

// maxRange bounds the IDs one range may name, so a typo cannot expand into
// millions of IDs.
const maxRange = 100

// expandEntry returns the IDs an entry names. It checks only the shape of a
// range (well-formed ends, same prefix, ascending); callers check that the
// IDs are defined.
func expandEntry(e *Entry) ([]string, error) {
	if e.To == "" {
		return []string{e.From}, nil
	}
	fp, from, ok := splitID(e.From)
	if !ok {
		return nil, fmt.Errorf("%s is not a valid ID", e.From)
	}
	tp, to, ok := splitID(e.To)
	if !ok {
		return nil, fmt.Errorf("%s is not a valid ID", e.To)
	}
	if fp != tp {
		return nil, errors.New("ends have different prefixes")
	}
	if from >= to {
		return nil, errors.New("end must be greater than start")
	}
	if to-from >= maxRange {
		return nil, fmt.Errorf("more than %d IDs; list them separately", maxRange)
	}
	width := len(e.From) - len(fp) - 1
	ids := make([]string, 0, to-from+1)
	for n := from; n <= to; n++ {
		ids = append(ids, fmt.Sprintf("%s-%0*d", fp, width, n))
	}
	return ids, nil
}

// milestoneKey orders milestones: M0a < M0d < M1a < M1d < M2 < M14 < M28.
type milestoneKey struct {
	n   int
	sub string
}

func (k milestoneKey) less(o milestoneKey) bool {
	if k.n != o.n {
		return k.n < o.n
	}
	return k.sub < o.sub
}

var milestoneRe = regexp.MustCompile(`^M(\d+)([a-z]?)$`)

func parseMilestone(s string) (milestoneKey, bool) {
	m := milestoneRe.FindStringSubmatch(s)
	if m == nil {
		return milestoneKey{}, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return milestoneKey{}, false
	}
	return milestoneKey{n: n, sub: m[2]}, true
}
