package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
)

// Column names of the tables the parser reads.
const (
	colID        = "ID"
	colName      = "名称"
	colQuestion  = "问题"
	colPriority  = "优先级"
	colMilestone = "里程碑"
	colVersion   = "版本"
	colStatus    = "状态"
)

// The 当前里程碑 line of §0: "**当前里程碑**：M0b SendInput 验证（未开始）。",
// or "**当前里程碑**：无（全部完成）。" once every milestone is 完成.
const (
	currentMarker  = "**当前里程碑**"
	currentNone    = "无"
	currentAllDone = "全部完成"
)

// idOrRange matches "ID" or "ID 至 ID", tolerating strikethrough around
// either end and a few alternative range separators.
const idOrRange = `(?:~~)?([A-Za-z]+-\d+)(?:~~)?(?:\s*(?:至|到|~|～|–|—)\s*(?:~~)?([A-Za-z]+-\d+)(?:~~)?)?`

var (
	headingRe        = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	sectionNumRe     = regexp.MustCompile(`^(\d+(?:\.\d+)*)\.?(?:\s+|$)`)
	milestoneHeadRe  = regexp.MustCompile(`^(M\d+[a-z]?)\b\s*(.*)$`)
	headVersionRe    = regexp.MustCompile(`^(.*?)\s*[（(]\s*(v\d[^，,）)\s]*)\s*(?:[，,][^）)]*)?[）)]$`)
	checklistRe      = regexp.MustCompile(`^\s*[-*+]\s+\[([ xX])\]\s*(.*)$`)
	looseChecklistRe = regexp.MustCompile(`^\s*[-*+]\s*\[[^\]]{0,3}\](?:[^(]|$)`)
	entryHeadRe      = regexp.MustCompile(`^` + idOrRange + `(.*)$`)
	backlogEntryRe   = regexp.MustCompile(`^` + idOrRange + `$`)
	looseIDRe        = regexp.MustCompile(`[A-Za-z]+-\d+`)
	idCellRe         = regexp.MustCompile(`^[A-Za-z]+-\d+$`)
	optionalRe       = regexp.MustCompile(`[（(]\s*可选\s*[）)]`)
	parenRe          = regexp.MustCompile(`[（(][^（）()]*[）)]`)
	bulletRe         = regexp.MustCompile(`^[-*+]\s+`)
	listSepRe        = regexp.MustCompile(`[、，,；;]`)
	separatorCellRe  = regexp.MustCompile(`^:?-+:?$`)
	refRe            = regexp.MustCompile(`\b(?:` + strings.Join(prefixList, "|") + `)-\d{2,}\b`)
	currentRe        = regexp.MustCompile(`^` + regexp.QuoteMeta(currentMarker) +
		`\s*[：:]\s*(?:(M\d+[a-z]?)(?:\s+(.+?))?|(` + currentNone + `))\s*[（(]\s*([^（）()]+?)\s*[）)]\s*[。.]?$`)
	// currentLikeRe matches lines meant as a 当前里程碑 line, including near
	// misses such as a list item or the colon inside the bold marker.
	currentLikeRe = regexp.MustCompile(`^(?:[-*+>]\s+|>)*\*\*\s*当前里程碑`)
)

// area is the kind of "###" section the parser is in.
type area int

const (
	areaOther     area = iota
	areaFeatures       // ### 3.N
	areaOverview       // ### 4.1
	areaMilestone      // ### M<N>
	areaBacklog        // ### Backlog
	areaSpikes         // ### 5.1
)

type tableKind int

const (
	tableIgnored tableKind = iota
	tableFeatures
	tableSpikes
	tableOverview
)

type table struct {
	kind    tableKind
	cols    map[string]int
	ncols   int
	line    int // header line
	rows    int // lines seen after the header
	section string
	prefix  string
}

func (t *table) cell(cells []string, name string) string {
	i, ok := t.cols[name]
	if !ok {
		return ""
	}
	return cells[i]
}

type parser struct {
	rm       *Roadmap
	problems []Problem
	line     int
	fence    string // closing marker of the open code fence, "" if none

	chapter   string // number of the current "## N." chapter
	area      area
	section   string // "§3.1" inside a §3 subsection
	prefix    string // ID prefix named by the current §3 subsection heading
	milestone *Milestone
	table     *table
}

// parse reads ROADMAP.md. It reports lines it cannot make sense of inside
// the structures it knows (feature, spike and overview tables, milestone
// checklists, the Backlog list); everything else is running text, which is
// only scanned for ID references.
func parse(r io.Reader) (*Roadmap, []Problem) {
	p := &parser{rm: &Roadmap{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		p.line++
		text := sc.Text()
		if p.line == 1 {
			text = strings.TrimPrefix(text, "\uFEFF") // byte order mark
		}
		p.handle(text)
	}
	if err := sc.Err(); err != nil {
		p.addf(p.line+1, "read error: %v", err)
	}
	p.finish()
	return p.rm, p.problems
}

func (p *parser) addf(line int, format string, args ...any) {
	p.problems = append(p.problems, Problem{Line: line, Msg: fmt.Sprintf(format, args...)})
}

func (p *parser) handle(text string) {
	trimmed := strings.TrimSpace(text)
	if p.fenced(trimmed) {
		return
	}
	isRow := strings.HasPrefix(trimmed, "|")
	if !isRow {
		p.table = nil
	}
	switch m := headingRe.FindStringSubmatch(trimmed); {
	case m != nil:
		title := strings.TrimSpace(m[2])
		p.heading(len(m[1]), title)
		p.scanRefs(title)
	case isRow:
		p.tableLine(trimmed)
	case p.checklist(text):
	case currentLikeRe.MatchString(trimmed):
		p.currentLine(trimmed)
	case p.area == areaBacklog && trimmed != "":
		p.backlogLine(trimmed)
	default:
		p.scanRefs(text)
	}
}

// fenced reports whether the line opens, closes or is inside a code fence.
func (p *parser) fenced(trimmed string) bool {
	if p.fence != "" {
		if strings.HasPrefix(trimmed, p.fence) {
			p.fence = ""
		}
		return true
	}
	for _, f := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, f) {
			p.fence = f
			p.table = nil
			return true
		}
	}
	return false
}

func (p *parser) heading(level int, title string) {
	if level > 3 {
		return // #### and deeper stay inside the current section
	}
	p.area, p.milestone, p.section, p.prefix = areaOther, nil, "", ""
	num, rest := "", title
	if m := sectionNumRe.FindStringSubmatch(title); m != nil {
		num, rest = m[1], title[len(m[0]):]
	}
	if level < 3 {
		p.chapter = ""
		if level == 2 {
			p.chapter = num
			if num == "0" && p.rm.SummaryLine == 0 {
				p.rm.SummaryLine = p.line
			}
		}
		return
	}
	switch {
	case num == "4.1":
		p.area = areaOverview
	case num == "5.1":
		p.area = areaSpikes
	case strings.HasPrefix(num, "3."):
		p.area = areaFeatures
		p.section = "§" + num
		if f := strings.Fields(rest); len(f) > 0 && knownPrefix[f[0]] {
			p.prefix = f[0]
		}
	case num != "":
		// Some other numbered section.
	case milestoneHeadRe.MatchString(title):
		m := milestoneHeadRe.FindStringSubmatch(title)
		ms := &Milestone{Name: m[1], Line: p.line}
		ms.Title, ms.Version = splitMilestoneTitle(m[2])
		p.rm.Milestones = append(p.rm.Milestones, ms)
		p.area, p.milestone = areaMilestone, ms
	case strings.EqualFold(firstField(title), backlog):
		if p.rm.BacklogLine != 0 {
			p.addf(p.line, "second Backlog section (first at line %d)", p.rm.BacklogLine)
		} else {
			p.rm.BacklogLine = p.line
		}
		p.area = areaBacklog
	}
}

// splitMilestoneTitle splits "无头引擎（v0.1.0-a）" or
// "首个安装包（v0.14.0，首个公开预览）" into title and version.
func splitMilestoneTitle(s string) (title, version string) {
	s = strings.TrimSpace(s)
	if m := headVersionRe.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1]), m[2]
	}
	return s, ""
}

func firstField(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}

func (p *parser) tableLine(trimmed string) {
	cells := splitRow(trimmed)
	t := p.table
	if t == nil {
		p.table = p.newTable(cells)
		p.scanRefs(trimmed)
		return
	}
	t.rows++
	if t.kind == tableIgnored {
		p.scanRefs(trimmed)
		return
	}
	if t.rows == 1 {
		if isSeparator(cells) {
			return
		}
		p.addf(t.line, "table header is not followed by a |---| separator row")
	}
	if len(cells) != t.ncols {
		p.addf(p.line, "table row has %d cells, header has %d", len(cells), t.ncols)
		return
	}
	switch t.kind {
	case tableFeatures, tableSpikes:
		p.itemRow(t, cells)
	case tableOverview:
		p.overviewRow(t, cells)
		p.scanRefs(trimmed)
	}
}

func (p *parser) newTable(header []string) *table {
	t := &table{cols: make(map[string]int), ncols: len(header), line: p.line}
	for i, c := range header {
		if _, dup := t.cols[c]; !dup {
			t.cols[c] = i
		}
	}
	has := func(names ...string) bool {
		for _, n := range names {
			if _, ok := t.cols[n]; !ok {
				return false
			}
		}
		return true
	}
	switch {
	case p.area == areaFeatures || p.chapter == "3":
		t.section, t.prefix = p.section, p.prefix
		if t.section == "" {
			t.section = "§3"
		}
		switch {
		case !has(colID):
			p.addf(p.line, "table in %s has no ID column", t.section)
		case !has(colPriority, colMilestone):
			p.addf(p.line, "feature table in %s needs 优先级 and 里程碑 columns", t.section)
		default:
			t.kind = tableFeatures
			p.rm.sawFeatures = true
		}
	case p.area == areaSpikes:
		t.section = "§5.1"
		if has(colID, colMilestone) {
			t.kind = tableSpikes
			p.rm.sawSpikes = true
		} else {
			p.addf(p.line, "spike table in §5.1 needs ID and 里程碑 columns")
		}
	case p.area == areaOverview:
		if has(colMilestone, colStatus) {
			t.kind = tableOverview
			p.rm.sawOverview = true
		} else {
			p.addf(p.line, "overview table in §4.1 needs 里程碑 and 状态 columns")
		}
	}
	return t
}

// splitRow splits a table row on unescaped pipes and trims the cells.
func splitRow(s string) []string {
	s = strings.TrimPrefix(strings.TrimSpace(s), "|")
	if strings.HasSuffix(s, "|") && !strings.HasSuffix(s, `\|`) {
		s = s[:len(s)-1]
	}
	var cells []string
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == '|':
			b.WriteString(`\|`)
			i++
		case s[i] == '|':
			cells = append(cells, strings.TrimSpace(b.String()))
			b.Reset()
		default:
			b.WriteByte(s[i])
		}
	}
	return append(cells, strings.TrimSpace(b.String()))
}

func isSeparator(cells []string) bool {
	for _, c := range cells {
		if !separatorCellRe.MatchString(c) {
			return false
		}
	}
	return len(cells) > 0
}

// plain trims spaces, emphasis and code marks from a cell.
func plain(s string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(s), "*`"))
}

func (p *parser) itemRow(t *table, cells []string) {
	idCell := t.cell(cells, colID)
	id, struck, ok := parseIDCell(idCell)
	if !ok {
		p.addf(p.line, "cannot parse ID cell %q", idCell)
		return
	}
	var others []string
	for i, c := range cells {
		if i != t.cols[colID] {
			others = append(others, c)
		}
	}
	rest := strings.Join(others, " | ")
	p.scanRefs(rest)

	it := &Item{
		ID:            id,
		Deprecated:    struck,
		Spike:         t.kind == tableSpikes,
		Section:       t.section,
		SectionPrefix: t.prefix,
		Line:          p.line,
	}
	name := t.cell(cells, colName)
	if name == "" {
		name = t.cell(cells, colQuestion)
	}
	it.Name = strings.TrimSpace(strings.Trim(name, "~"))
	if struck {
		it.DeprecationNote = strings.Contains(rest, "废弃")
		p.rm.Items = append(p.rm.Items, it)
		return
	}
	if t.kind == tableFeatures {
		raw := t.cell(cells, colPriority)
		pr, ok := parsePriority(raw)
		if !ok {
			p.addf(p.line, "%s: unrecognized priority %q (want P0, P1, P1（提前）, P2 or P2（可选）)", id, raw)
		}
		it.Priority = pr
	}
	raw := plain(t.cell(cells, colMilestone))
	switch {
	case strings.EqualFold(raw, backlog):
		it.Milestone = backlog
	case milestoneRe.MatchString(raw):
		it.Milestone = raw
	default:
		p.addf(p.line, "%s: unrecognized milestone %q (want M<N>, M<N><letter> or Backlog)", id, raw)
	}
	p.rm.Items = append(p.rm.Items, it)
}

// parseIDCell parses "IN-01" or a struck-through "~~MAP-24~~".
func parseIDCell(s string) (id string, struck, ok bool) {
	s = strings.TrimSpace(s)
	if len(s) > 4 && strings.HasPrefix(s, "~~") && strings.HasSuffix(s, "~~") {
		struck = true
		s = strings.TrimSpace(s[2 : len(s)-2])
	}
	return s, struck, idCellRe.MatchString(s)
}

func (p *parser) overviewRow(t *table, cells []string) {
	name := plain(t.cell(cells, colMilestone))
	if !milestoneRe.MatchString(name) {
		p.addf(p.line, "§4.1: unrecognized milestone %q", name)
		return
	}
	p.rm.Overview = append(p.rm.Overview, &OverviewRow{
		Milestone: name,
		Title:     strings.TrimSpace(t.cell(cells, colName)),
		Version:   plain(t.cell(cells, colVersion)),
		Status:    plain(t.cell(cells, colStatus)),
		Line:      p.line,
	})
}

// checklist handles a "- [ ] ID name" line and reports whether the line
// was (or looked like) a checklist item.
func (p *parser) checklist(text string) bool {
	m := checklistRe.FindStringSubmatch(text)
	if m == nil {
		if looseChecklistRe.MatchString(text) {
			p.addf(p.line, `malformed checklist line (want "- [ ] ID name" or "- [x] ID name")`)
			return true
		}
		return false
	}
	if p.milestone == nil {
		p.addf(p.line, "checklist item outside a milestone section")
		return true
	}
	body := strings.TrimSpace(m[2])
	h := entryHeadRe.FindStringSubmatch(body)
	if h == nil {
		p.addf(p.line, "checklist item does not start with an ID: %q", body)
		return true
	}
	p.scanRefs(h[3])
	p.milestone.Entries = append(p.milestone.Entries, &Entry{
		From:     h[1],
		To:       h[2],
		Checked:  m[1] != " ",
		Optional: optionalRe.MatchString(h[3]),
		Line:     p.line,
	})
	return true
}

// backlogLine parses "OUT-24、ANA-25 至 ANA-28、PRO-22（需确认）。".
// Parenthesized notes are dropped; lines without any ID are prose.
func (p *parser) backlogLine(trimmed string) {
	s := bulletRe.ReplaceAllString(trimmed, "")
	for _, note := range parenRe.FindAllString(s, -1) {
		p.scanRefs(note)
	}
	for {
		stripped := parenRe.ReplaceAllString(s, "")
		if stripped == s {
			break
		}
		s = stripped
	}
	if !looseIDRe.MatchString(s) {
		p.scanRefs(s)
		return
	}
	s = strings.TrimRight(strings.TrimSpace(s), "。.；;")
	for _, part := range listSepRe.Split(s, -1) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		m := backlogEntryRe.FindStringSubmatch(part)
		if m == nil {
			p.addf(p.line, `cannot parse Backlog entry %q (want ID or "ID 至 ID")`, part)
			continue
		}
		p.rm.Backlog = append(p.rm.Backlog, &Entry{From: m[1], To: m[2], Line: p.line})
	}
}

// currentLine parses the 当前里程碑 line of §0. check compares it with §4.1.
// Such a line anywhere else is reported, so a mistyped "## 0." heading
// cannot switch the check off.
func (p *parser) currentLine(trimmed string) {
	p.scanRefs(trimmed)
	if p.chapter != "0" {
		p.addf(p.line, `当前里程碑 line outside §0 (want it under a "## 0. <title>" heading)`)
		return
	}
	if p.rm.CurrentLine != 0 {
		p.addf(p.line, "second 当前里程碑 line in §0 (first at line %d)", p.rm.CurrentLine)
		return
	}
	p.rm.CurrentLine = p.line
	// Go's \s is ASCII only; accept full-width and no-break spaces too.
	m := currentRe.FindStringSubmatch(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, trimmed))
	if m == nil {
		p.addf(p.line, `malformed 当前里程碑 line (want "%s：<ID> <name>（<status>）。" or "%s：%s（%s）。")`,
			currentMarker, currentMarker, currentNone, currentAllDone)
		return
	}
	p.rm.Current = &Current{
		Milestone: m[1],
		Title:     strings.TrimSpace(m[2]),
		Status:    plain(m[4]),
		Line:      p.line,
	}
}

// scanRefs records IDs with a known prefix mentioned in running text.
func (p *parser) scanRefs(s string) {
	for _, id := range refRe.FindAllString(s, -1) {
		p.rm.Refs = append(p.rm.Refs, Ref{ID: id, Line: p.line})
	}
}

func (p *parser) finish() {
	if !p.rm.sawFeatures {
		p.addf(1, "no feature table (| ID | … | 优先级 | 里程碑 | … |) found in §3")
	}
	if !p.rm.sawSpikes {
		p.addf(1, "no spike table (| ID | 问题 | 里程碑 | … |) found in §5.1")
	}
	if !p.rm.sawOverview {
		p.addf(1, "no overview table (| 里程碑 | … | 状态 |) found in §4.1")
	}
	if len(p.rm.Milestones) == 0 {
		p.addf(1, "no milestone sections (### M<N> …) found")
	}
}
