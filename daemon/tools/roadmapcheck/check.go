package main

import (
	"fmt"
	"sort"
	"strings"
)

// Statuses of the §4.1 overview table.
const (
	statusNotStarted = "未开始"
	statusInProgress = "进行中"
	statusDone       = "完成"
	// statusDeferred marks a milestone that ships after 1.0: it keeps its
	// number, section and items, but nothing in it is checked, it is skipped
	// by the 当前里程碑 line and the 1.0 gate does not require its items.
	statusDeferred = "1.0 之后"
)

const (
	// earlyLimit is the last milestone number where P1 items are written
	// P1（提前） and where P0 items belong.
	earlyLimit = 14
	// installerMilestone ships the first installer: when it is 完成, every
	// P0 item in milestones ≤ M14 must be checked (M14 自动验收).
	installerMilestone = "M14"
	// releaseMilestone is 1.0: when it is 完成, every P0 and P1 item must be
	// checked, except those of milestones that are 1.0 之后.
	releaseMilestone = "M28"
)

// maxListed caps the IDs quoted in one message.
const maxListed = 8

// listing records where a checklist names an ID.
type listing struct {
	m      *Milestone
	e      *Entry
	placed bool // listed under the milestone its 里程碑 column names
}

type checker struct {
	rm       *Roadmap
	problems []Problem

	defs  map[string]*Item // first definition of each ID
	items []*Item          // first definitions in document order

	ids    map[*Entry][]string // expanded IDs; nil for an invalid range
	listed map[string]listing  // first checklist listing of each ID

	overview map[string]*OverviewRow // first §4.1 row of each milestone
	rows     []*OverviewRow          // first rows in document order
	sections map[string]*Milestone   // first section of each milestone
	order    []*Milestone            // first sections in document order
}

// check applies the ROADMAP rules to a parsed roadmap.
func check(rm *Roadmap) []Problem {
	c := &checker{
		rm:       rm,
		defs:     make(map[string]*Item),
		ids:      make(map[*Entry][]string),
		listed:   make(map[string]listing),
		overview: make(map[string]*OverviewRow),
		sections: make(map[string]*Milestone),
	}
	c.definitions()
	c.milestones()
	c.expandAll()
	c.placement()
	c.backlog()
	c.priorities()
	c.statuses()
	c.current()
	c.gates()
	c.references()
	return c.problems
}

func (c *checker) addf(line int, format string, args ...any) {
	c.problems = append(c.problems, Problem{Line: line, Msg: fmt.Sprintf(format, args...)})
}

// definitions checks that every ID is well-formed, defined once and in the
// table for its prefix, and that no number is skipped.
func (c *checker) definitions() {
	for _, it := range c.rm.Items {
		if first, dup := c.defs[it.ID]; dup {
			c.addf(it.Line, "%s is defined more than once (first at line %d)", it.ID, first.Line)
			continue
		}
		c.defs[it.ID] = it
		c.items = append(c.items, it)
		if it.Deprecated && !it.DeprecationNote {
			c.addf(it.Line, "%s is struck through but its row does not say 废弃", it.ID)
		}
		prefix, _, ok := splitID(it.ID)
		switch {
		case !ok:
			c.addf(it.Line, "%s is not a valid ID (want PREFIX-NN)", it.ID)
		case !knownPrefix[prefix]:
			c.addf(it.Line, "%s has unknown prefix %s", it.ID, prefix)
		case it.Spike && prefix != spikePrefix:
			c.addf(it.Line, "%s is in the §5.1 spike table, which is only for SPK IDs", it.ID)
		case !it.Spike && prefix == spikePrefix:
			c.addf(it.Line, "%s must be defined in the §5.1 spike table", it.ID)
		case !it.Spike && it.SectionPrefix != "" && prefix != it.SectionPrefix:
			c.addf(it.Line, "%s is in the %s table, which is for %s IDs", it.ID, it.Section, it.SectionPrefix)
		}
	}
	c.gaps()
}

// gaps reports skipped numbers: IDs are never removed, only struck through.
func (c *checker) gaps() {
	byPrefix := make(map[string][]*Item)
	for _, it := range c.items {
		if p, _, ok := splitID(it.ID); ok && knownPrefix[p] {
			byPrefix[p] = append(byPrefix[p], it)
		}
	}
	for p, items := range byPrefix {
		num := func(it *Item) int { _, n, _ := splitID(it.ID); return n }
		sort.Slice(items, func(i, j int) bool { return num(items[i]) < num(items[j]) })
		next := 1
		for _, it := range items {
			switch n := num(it); {
			case n == next+1:
				c.addf(it.Line, "%s-%02d is missing; IDs are never removed (strike the row through instead)", p, next)
			case n > next+1:
				c.addf(it.Line, "%s-%02d 至 %s-%02d are missing; IDs are never removed (strike the rows through instead)", p, next, p, n-1)
			}
			next = max(next, num(it)+1)
		}
	}
}

// milestones checks that §4.1 and the milestone sections list the same
// milestones once each, in the same ascending order, with matching titles
// and versions.
func (c *checker) milestones() {
	var prev *OverviewRow
	var prevKey milestoneKey
	for _, r := range c.rm.Overview {
		if first, dup := c.overview[r.Milestone]; dup {
			c.addf(r.Line, "%s is listed twice in §4.1 (first at line %d)", r.Milestone, first.Line)
			continue
		}
		c.overview[r.Milestone] = r
		c.rows = append(c.rows, r)
		k, _ := parseMilestone(r.Milestone)
		if prev != nil && !prevKey.less(k) {
			c.addf(r.Line, "%s is listed after %s in §4.1; milestones must be in ascending order", r.Milestone, prev.Milestone)
		}
		prev, prevKey = r, k
	}
	for _, m := range c.rm.Milestones {
		if first, dup := c.sections[m.Name]; dup {
			c.addf(m.Line, "second section for %s (first at line %d)", m.Name, first.Line)
			continue
		}
		c.sections[m.Name] = m
		c.order = append(c.order, m)
	}

	var inBoth []string
	for _, r := range c.rows {
		if c.sections[r.Milestone] == nil {
			c.addf(r.Line, "%s has no milestone section", r.Milestone)
			continue
		}
		inBoth = append(inBoth, r.Milestone)
	}
	i := 0
	reported := false
	for _, m := range c.order {
		r := c.overview[m.Name]
		if r == nil {
			c.addf(m.Line, "%s is not listed in the §4.1 overview", m.Name)
			continue
		}
		if !reported && inBoth[i] != m.Name {
			c.addf(m.Line, "milestone sections are out of order: §4.1 has %s here, found %s", inBoth[i], m.Name)
			reported = true
		}
		i++
		if m.Title != "" && r.Title != "" && normalize(m.Title) != normalize(r.Title) {
			c.addf(m.Line, "%s title %q differs from §4.1 (%q)", m.Name, m.Title, r.Title)
		}
		if m.Version != "" && r.Version != "" && m.Version != r.Version {
			c.addf(m.Line, "%s version %s differs from §4.1 (%s)", m.Name, m.Version, r.Version)
		}
	}
}

// expandAll expands every checklist and Backlog entry.
func (c *checker) expandAll() {
	for _, m := range c.rm.Milestones {
		for _, e := range m.Entries {
			c.ids[e] = c.expand(e)
		}
	}
	for _, e := range c.rm.Backlog {
		c.ids[e] = c.expand(e)
	}
}

func (c *checker) expand(e *Entry) []string {
	ids, err := expandEntry(e)
	if err != nil {
		c.addf(e.Line, "range %s: %v", e, err)
		return nil
	}
	if e.To == "" {
		return ids
	}
	ok := true
	for _, end := range []string{e.From, e.To} {
		if c.defs[end] == nil {
			c.addf(e.Line, "range %s: %s is not defined", e, end)
			ok = false
		}
	}
	if !ok {
		return nil
	}
	return ids
}

// placement checks that checklists name only defined IDs, and that every
// scheduled ID is listed exactly once, under its own milestone.
func (c *checker) placement() {
	for _, m := range c.rm.Milestones {
		for _, e := range m.Entries {
			for _, id := range c.ids[e] {
				it := c.defs[id]
				if it == nil {
					if e.To == "" {
						c.addf(e.Line, "%s is not defined", id)
					} else {
						c.addf(e.Line, "%s (in range %s) is not defined", id, e)
					}
					continue
				}
				if prev, dup := c.listed[id]; dup {
					c.addf(e.Line, "%s is already listed at line %d", id, prev.e.Line)
					continue
				}
				l := listing{m: m, e: e}
				switch {
				case it.Deprecated:
					c.addf(e.Line, "%s is deprecated and must not be listed in a milestone", id)
				case it.Milestone == "":
					// The 里程碑 cell did not parse; parse reported it.
				case it.Milestone != m.Name:
					c.addf(e.Line, "%s is listed under %s but its milestone is %s", id, m.Name, it.Milestone)
				default:
					l.placed = true
				}
				c.listed[id] = l
			}
		}
	}
	for _, it := range c.items {
		if it.Deprecated || it.Milestone == "" || it.Milestone == backlog {
			continue
		}
		if _, ok := c.listed[it.ID]; ok {
			continue
		}
		if c.sections[it.Milestone] == nil && c.overview[it.Milestone] == nil {
			c.addf(it.Line, "%s has milestone %s, which is not in §4", it.ID, it.Milestone)
		} else {
			c.addf(it.Line, "%s is not listed in the %s checklist", it.ID, it.Milestone)
		}
	}
}

// backlog checks that the Backlog section lists exactly the Backlog IDs.
func (c *checker) backlog() {
	seen := make(map[string]*Entry)
	for _, e := range c.rm.Backlog {
		for _, id := range c.ids[e] {
			it := c.defs[id]
			switch first := seen[id]; {
			case it == nil:
				c.addf(e.Line, "Backlog lists %s, which is not defined", id)
			case first != nil:
				c.addf(e.Line, "Backlog lists %s twice (first at line %d)", id, first.Line)
			case it.Deprecated:
				c.addf(e.Line, "Backlog lists deprecated %s", id)
			case it.Milestone != backlog && it.Milestone != "":
				c.addf(e.Line, "Backlog lists %s, whose milestone is %s", id, it.Milestone)
			}
			if seen[id] == nil {
				seen[id] = e
			}
		}
	}
	for _, it := range c.items {
		if !it.Deprecated && it.Milestone == backlog && seen[it.ID] == nil {
			c.addf(it.Line, "%s has milestone Backlog but is not listed in the Backlog section", it.ID)
		}
	}
}

// priorities checks the 优先级 column against the item's milestone and its
// checklist line.
func (c *checker) priorities() {
	for _, it := range c.items {
		if it.Deprecated || it.Priority == PriorityNone || it.Milestone == "" {
			continue
		}
		pr := it.Priority
		if it.Milestone == backlog {
			if !pr.isP2() {
				c.addf(it.Line, "%s is %s in Backlog; Backlog items must be P2", it.ID, pr)
			}
			continue
		}
		k, _ := parseMilestone(it.Milestone)
		early := k.n <= earlyLimit
		switch {
		case pr == P0 && !early:
			c.addf(it.Line, "%s is P0 in %s; P0 items belong to milestones ≤ M%d", it.ID, it.Milestone, earlyLimit)
		case pr == P1 && early:
			c.addf(it.Line, "%s is plain P1 in %s; P1 items in milestones ≤ M%d must be written P1（提前）", it.ID, it.Milestone, earlyLimit)
		case pr == P1Early && !early:
			c.addf(it.Line, "%s is P1（提前） in %s; P1（提前） is only for milestones ≤ M%d, write P1", it.ID, it.Milestone, earlyLimit)
		case pr == P2:
			c.addf(it.Line, "%s is plain P2 in %s; a P2 item in a milestone must be written P2（可选）", it.ID, it.Milestone)
		}
		l, ok := c.listed[it.ID]
		if ok && l.placed && l.e.Optional && !pr.isP2() {
			c.addf(l.e.Line, "%s is %s but its checklist line is marked （可选）; only P2 items are optional", it.ID, pr)
		}
	}
}

// optional reports whether id, listed by e, does not block its milestone.
func (c *checker) optional(e *Entry, id string) bool {
	if e.Optional {
		return true
	}
	it := c.defs[id]
	return it != nil && it.Priority.isP2()
}

// statuses checks each §4.1 status against its checklist. A 1.0 之后
// milestone needs required items like any other, but none of its items may
// be checked.
func (c *checker) statuses() {
	for _, r := range c.rows {
		switch r.Status {
		case statusNotStarted, statusInProgress, statusDone, statusDeferred:
		default:
			c.addf(r.Line, "%s has unknown status %q (want %s, %s, %s or %s)",
				r.Milestone, r.Status, statusNotStarted, statusInProgress, statusDone, statusDeferred)
			continue
		}
		m := c.sections[r.Milestone]
		if m == nil {
			continue
		}
		required, checked, optionalLeft := 0, 0, 0
		var unchecked []string
		for _, e := range m.Entries {
			ids := c.ids[e]
			if ids == nil {
				ids = []string{e.String()}
			}
			for _, id := range ids {
				if e.Checked {
					checked++
				}
				if c.optional(e, id) {
					if !e.Checked {
						optionalLeft++
					}
					continue
				}
				required++
				if !e.Checked {
					unchecked = append(unchecked, id)
				}
			}
		}
		if required == 0 {
			c.addf(m.Line, "%s has no non-optional checklist items", m.Name)
			continue
		}
		if r.Status == statusDeferred {
			// Work has not started, so nothing is checked, optional items
			// included.
			if checked > 0 {
				c.addf(r.Line, "%s is %s but has checked items", r.Milestone, statusDeferred)
			}
			continue
		}
		want := statusInProgress
		switch {
		case len(unchecked) == 0:
			want = statusDone
		case checked == 0:
			want = statusNotStarted
		}
		if r.Status == want {
			continue
		}
		// Optional items never block 完成, but a milestone may stay 进行中
		// while some are unchecked: the CLAUDE.md workflow sets 完成 once
		// every item is checked. Both statuses are accepted then.
		wantText := want
		if want == statusDone && optionalLeft > 0 {
			if r.Status == statusInProgress {
				continue
			}
			wantText = statusInProgress + " or " + statusDone
		}
		switch r.Status {
		case statusDone:
			c.addf(r.Line, "%s is %s but required items are unchecked: %s", r.Milestone, statusDone, joinIDs(unchecked))
		case statusNotStarted:
			c.addf(r.Line, "%s is %s but has checked items (want %s)", r.Milestone, statusNotStarted, wantText)
		default:
			if want == statusDone {
				c.addf(r.Line, "%s is %s but all items are checked (want %s)", r.Milestone, statusInProgress, want)
			} else {
				c.addf(r.Line, "%s is %s but no item is checked (want %s)", r.Milestone, statusInProgress, want)
			}
		}
	}
}

// current checks the 当前里程碑 line of §0 against §4.1: it names the first
// milestone that is neither 完成 nor 1.0 之后, with that row's 名称 and
// status, or reads 无（全部完成） once every milestone is one of the two. A
// roadmap without §0 is not checked.
func (c *checker) current() {
	if c.rm.SummaryLine == 0 || len(c.rows) == 0 {
		return
	}
	var next *OverviewRow
	for _, r := range c.rows {
		if r.Status != statusDone && r.Status != statusDeferred {
			next = r
			break
		}
	}
	want := currentNone + "（" + currentAllDone + "）"
	why := "every milestone in §4.1 is " + statusDone + " or " + statusDeferred
	if next != nil {
		want = next.Milestone
		if t := plain(next.Title); t != "" {
			want += " " + t
		}
		want += "（" + next.Status + "）"
		why = "first milestone in §4.1 that is not " + statusDone + " or " + statusDeferred
	}
	if c.rm.CurrentLine == 0 {
		c.addf(c.rm.SummaryLine, "§0 has no 当前里程碑 line (want %q)", currentMarker+"："+want+"。")
		return
	}
	cur := c.rm.Current
	if cur == nil {
		return // malformed; parse reported it
	}
	ok := cur.Milestone == "" && cur.Status == currentAllDone
	if next != nil {
		// Like the title check, an empty §4.1 名称 is not compared.
		ok = cur.Milestone == next.Milestone &&
			(next.Title == "" || normalize(cur.Title) == normalize(next.Title)) &&
			cur.Status == next.Status
	}
	if !ok {
		c.addf(cur.Line, "§0 当前里程碑 should be %q (%s)", want, why)
	}
}

func joinIDs(ids []string) string {
	if len(ids) <= maxListed {
		return strings.Join(ids, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(ids[:maxListed], ", "), len(ids)-maxListed)
}

// gates applies the release gates: at 1.0 every P0 and P1 item is checked,
// except those of 1.0 之后 milestones; at the first installer every P0 item
// of milestones ≤ M14 is checked.
func (c *checker) gates() {
	status := func(name string) string {
		if r := c.overview[name]; r != nil {
			return r.Status
		}
		return ""
	}
	var gate string
	var needed func(it *Item) bool
	switch {
	case status(releaseMilestone) == statusDone:
		gate = releaseMilestone
		needed = func(it *Item) bool {
			return (it.Priority == P0 || it.Priority.isP1()) && status(it.Milestone) != statusDeferred
		}
	case status(installerMilestone) == statusDone:
		gate = installerMilestone
		needed = func(it *Item) bool {
			k, ok := parseMilestone(it.Milestone)
			return it.Priority == P0 && ok && k.n <= earlyLimit
		}
	default:
		return
	}
	for _, it := range c.items {
		if it.Deprecated || !needed(it) {
			continue
		}
		l, ok := c.listed[it.ID]
		switch {
		case !ok:
			c.addf(it.Line, "%s is %s but %s item %s is not checked", gate, statusDone, it.Priority, it.ID)
		case !l.e.Checked:
			c.addf(l.e.Line, "%s is %s but %s item %s is not checked", gate, statusDone, it.Priority, it.ID)
		}
	}
}

// references reports IDs mentioned in running text that are not defined.
func (c *checker) references() {
	seen := make(map[Ref]bool)
	for _, r := range c.rm.Refs {
		if c.defs[r.ID] == nil && !seen[r] {
			seen[r] = true
			c.addf(r.Line, "reference to undefined ID %s", r.ID)
		}
	}
}
