package cspint

import (
	"slices"
	"strconv"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/csp"
	"github.com/wdcyxxycdw/padmate/daemon/internal/integration"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// parseFunction splits a function ID into its kind and native ID (the command
// identity "<type>/<command>" or the tool node UUID).
func parseFunction(id string) (kind csp.ItemKind, native string, ok bool) {
	if rest, found := strings.CutPrefix(id, commandPrefix); found {
		typ, cmd, cut := strings.Cut(rest, "/")
		return csp.KindCommand, rest, cut && typ != "" && cmd != ""
	}
	if rest, found := strings.CutPrefix(id, toolPrefix); found {
		return csp.KindTool, rest, validUUID(rest)
	}
	return 0, "", false
}

// validUUID reports whether s is 32 lower-case hex characters.
func validUUID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if isHex := c >= '0' && c <= '9' || c >= 'a' && c <= 'f'; !isHex {
			return false
		}
	}
	return true
}

func commandFunctionID(native string) string { return commandPrefix + native }
func toolFunctionID(uuid string) string      { return toolPrefix + uuid }

// chordStatus classifies a chord for CSP. A symbol key has no confirmed
// counterpart yet, so it is unconfirmed rather than unsupported.
func chordStatus(c keys.Chord) csp.KeyStatus {
	_, _, st := csp.FormatCSPKey(c)
	if st == csp.KeyOK {
		return st
	}
	if r, ok := c.Key().Char(); ok {
		if isAlnum := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'; !isAlnum {
			return csp.KeyUnconfirmed
		}
	}
	return csp.KeyUnsupported
}

// rawKeyText spells a CSP key that has no padmate chord, as "Ctrl+Alt+Name".
func rawKeyText(key string, mod int) string {
	var b strings.Builder
	for _, m := range []struct {
		bit  int
		name string
	}{{csp.ModAlt, "Alt"}, {csp.ModShift, "Shift"}, {csp.ModCtrl, "Ctrl"}} {
		if mod&m.bit != 0 {
			b.WriteString(m.name + "+")
		}
	}
	if rest := mod &^ (csp.ModAlt | csp.ModShift | csp.ModCtrl); rest != 0 {
		b.WriteString("mod" + strconv.Itoa(rest) + "+")
	}
	b.WriteString(key)
	return b.String()
}

// commandInfo names a command; one the namer does not know is named by its
// ID.
func (in *Integration) commandInfo(id, lang string) integration.CommandInfo {
	info, ok := in.opts.Namer.CommandName(id, lang)
	if !ok || info.Name == "" {
		return integration.CommandInfo{Name: id}
	}
	return info
}

func (in *Integration) toIntegrationOwner(o csp.Owner, lang string) integration.Owner {
	if o.Kind == csp.KindTool {
		return integration.Owner{ID: toolFunctionID(o.Function), Name: o.Name, Path: slices.Clone(o.Path)}
	}
	info := in.commandInfo(o.Function, lang)
	return integration.Owner{ID: commandFunctionID(o.Function), Name: info.Name, Path: info.Path}
}

func (in *Integration) owners(list []csp.Owner, lang string) []integration.Owner {
	out := make([]integration.Owner, 0, len(list))
	for _, o := range list {
		out = append(out, in.toIntegrationOwner(o, lang))
	}
	return out
}

// Functions lists every command and tool node, and the tree they are
// placed in (tree.go).
func (in *Integration) Functions(lang string) (integration.Listing, error) {
	s, err := in.snapshot()
	if err != nil {
		return integration.Listing{}, err
	}
	l := integration.Listing{Revision: s.revision, Functions: []integration.Function{}}
	var cmds []cmdEntry
	if s.cmds != nil {
		for _, c := range s.cmds.Commands {
			info := in.commandInfo(c.ID, lang)
			f := integration.Function{
				ID: commandFunctionID(c.ID), Kind: integration.KindCommand, Name: info.Name,
				Path: info.Path, Keys: []string{},
			}
			if f.Path == nil {
				f.Path = []string{}
			}
			for _, sc := range c.Shortcuts {
				switch sc.Status {
				case csp.KeyOK:
					f.Keys = append(f.Keys, sc.Chord.String())
				case csp.KeyUnconfirmed:
					f.Unconfirmed = true
				default:
					f.Unsupported = true
				}
			}
			l.Functions = append(l.Functions, f)
			cmds = append(cmds, cmdEntry{native: c.ID, id: f.ID, info: info, name: f.Name})
		}
	}
	var tools []*csp.ToolNode
	if s.tools != nil {
		tools = s.tools.Nodes
		byCode := map[int][]string{}
		for _, n := range tools {
			if n.KeyCode != 0 {
				byCode[n.KeyCode] = append(byCode[n.KeyCode], toolFunctionID(n.UUID))
			}
		}
		for _, n := range tools {
			id := toolFunctionID(n.UUID)
			f := integration.Function{
				ID: id, Kind: integration.KindTool, Name: n.Name, Path: slices.Clone(n.Path), Keys: []string{},
			}
			if n.KeyCode != 0 {
				if c, ok := csp.ToolKeyChord(n.KeyCode); ok {
					f.Keys = append(f.Keys, c.String())
				} else {
					f.Unsupported = true
				}
				for _, other := range byCode[n.KeyCode] {
					if other != id {
						f.Cycle = append(f.Cycle, other)
					}
				}
			}
			l.Functions = append(l.Functions, f)
		}
	}
	l.Tree = buildTree(in.opts.Namer.Layout(lang), cmds, tools, lang)
	return l, nil
}

// Check says who owns keys in CSP.
func (in *Integration) Check(function, spec, lang string) (integration.CheckResult, error) {
	chord, err := keys.Parse(spec)
	if err != nil {
		return integration.CheckResult{}, err
	}
	res := integration.CheckResult{Owners: []integration.Owner{}, Notes: []string{}}
	switch chordStatus(chord) {
	case csp.KeyUnconfirmed:
		res.Status = integration.StatusUnconfirmed
		return res, nil
	case csp.KeyOK:
	default:
		res.Status = integration.StatusUnsupported
		return res, nil
	}
	s, err := in.snapshot()
	if err != nil {
		return integration.CheckResult{}, err
	}
	var target csp.Target
	if function != "" {
		kind, native, ok := parseFunction(function)
		if !ok {
			return integration.CheckResult{}, &integration.ChangeError{Function: function, Keys: spec, Reason: integration.ReasonUnknownFunction}
		}
		target = csp.Target{Kind: kind, Function: native}
	}
	r := s.an.Check(target, chord)
	res.Owners = in.owners(r.Owners, lang)
	switch r.State {
	case csp.CheckFree:
		res.Status = integration.StatusFree
		if target.Kind != 0 && slices.ContainsFunc(s.an.Owners(chord), func(o csp.Owner) bool {
			return o.Kind == target.Kind && o.Function == target.Function
		}) {
			res.Status = integration.StatusSame
		}
	case csp.CheckCycle:
		res.Status = integration.StatusCycle
	default:
		res.Status = integration.StatusConflict
	}
	if s.toolShift && chord.Mods() == 0 {
		res.Notes = append(res.Notes, integration.NoteToolShift)
	}
	// integration.NoteModifierKey needs the modifier-key settings (tomd),
	// which are not decoded yet.
	return res, nil
}

// toCSPItems converts items; ok[i] is false for an item whose function ID or
// keys are not CSP's.
func toCSPItems(items []integration.Item) (out []csp.Item, ok []bool) {
	out = make([]csp.Item, len(items))
	ok = make([]bool, len(items))
	for i, it := range items {
		kind, native, valid := parseFunction(it.Function)
		chord, err := keys.Parse(it.Keys)
		if !valid || err != nil {
			continue
		}
		out[i] = csp.Item{Kind: kind, Function: native, Chord: chord, Name: it.Name}
		ok[i] = true
	}
	return out, ok
}

// Compare compares items with the current settings.
func (in *Integration) Compare(items []integration.Item, lang string) ([]integration.ItemStatus, string, error) {
	s, err := in.snapshot()
	if err != nil {
		return nil, "", err
	}
	return in.compare(s, items, lang), s.revision, nil
}

func (in *Integration) compare(s *snapshot, items []integration.Item, lang string) []integration.ItemStatus {
	cspItems, ok := toCSPItems(items)
	valid := make([]csp.Item, 0, len(items))
	for i := range items {
		if ok[i] {
			valid = append(valid, cspItems[i])
		}
	}
	results := csp.Compare(valid, s.cmds, s.tools)
	out := make([]integration.ItemStatus, 0, len(items))
	next := 0
	for i, it := range items {
		st := integration.ItemStatus{Keys: it.Keys, Function: it.Function}
		if !ok[i] {
			st.State = integration.StateMissing
			out = append(out, st)
			continue
		}
		r := results[next]
		next++
		st.State = r.State.String()
		for _, c := range r.Current {
			switch {
			case c.Chord.IsValid():
				st.Current = append(st.Current, c.Chord.String())
			case c.Code != 0:
				st.Current = append(st.Current, "tool#"+strconv.Itoa(c.Code))
			default:
				st.Current = append(st.Current, rawKeyText(c.Key, c.Modifier))
			}
		}
		st.Owners = in.owners(r.Owners, lang)
		if len(st.Owners) == 0 {
			st.Owners = nil
		}
		out = append(out, st)
	}
	return out
}

// Plan says what to change in CSP so that each item that is not consistent
// holds.
func (in *Integration) Plan(items []integration.Item, lang string) (integration.Plan, error) {
	s, err := in.snapshot()
	if err != nil {
		return integration.Plan{}, err
	}
	statuses := in.compare(s, items, lang)
	plan := integration.Plan{Entries: []integration.PlanEntry{}}
	for i, st := range statuses {
		if st.State == integration.StateConsistent {
			continue
		}
		plan.Entries = append(plan.Entries, in.planEntry(s, items[i], st, lang))
	}
	return plan, nil
}

func (in *Integration) planEntry(s *snapshot, it integration.Item, st integration.ItemStatus, lang string) integration.PlanEntry {
	e := integration.PlanEntry{Item: it, Changes: []integration.Change{}, Displaced: []integration.Displaced{}}
	kind, native, valid := parseFunction(it.Function)
	chord, err := keys.Parse(it.Keys)
	switch {
	case !valid || err != nil:
		e.Skipped = integration.SkipBadFunction
		return e
	case st.State == integration.StateMissing:
		e.Skipped = integration.SkipMissing
		return e
	}
	switch chordStatus(chord) {
	case csp.KeyOK:
	case csp.KeyUnconfirmed:
		e.Skipped = integration.SkipUnconfirmedKey
		return e
	default:
		e.Skipped = integration.SkipUnsupportedKey
		return e
	}
	if kind == csp.KindTool {
		if _, ok := csp.ToolKeyCode(chord); !ok {
			e.Skipped = integration.SkipUnsupportedKey
			return e
		}
	}
	spec := chord.String()
	e.Changes = append(e.Changes, integration.Change{Function: it.Function, Add: spec})
	for _, o := range s.an.Owners(chord) {
		if o.Kind == kind && o.Function == native {
			continue
		}
		// Tools sharing a tool's key are CSP's cycle shortcut, not a conflict.
		if kind == csp.KindTool && o.Kind == csp.KindTool {
			continue
		}
		ow := in.toIntegrationOwner(o, lang)
		e.Changes = append(e.Changes, integration.Change{Function: ow.ID, Remove: spec})
		e.Displaced = append(e.Displaced, integration.Displaced{Function: ow.ID, Name: ow.Name, Keys: spec})
	}
	return e
}
