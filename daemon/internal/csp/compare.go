package csp

import "github.com/wdcyxxycdw/padmate/daemon/internal/keys"

// Item is one saved csp.json entry (ADR-0019): a chord and the CSP function
// it was set to.
type Item struct {
	Kind ItemKind
	// Function is the command identity "<type>/<command>" or the tool node UUID.
	Function string
	Chord    keys.Chord
	// Name is the display name saved with the item. It is not compared.
	Name string
}

// State is the result of comparing one saved item with CSP's current settings.
type State uint8

// Comparison states (ADR-0019 section 6).
const (
	// Consistent: the function still has the chord.
	Consistent State = iota
	// Changed: the function exists and has shortcuts, but not this chord.
	Changed
	// Removed: the function exists and has no shortcut at all.
	Removed
	// Missing: the command or tool is not in CSP any more.
	Missing
)

// String returns the lower-case name of s.
func (s State) String() string {
	switch s {
	case Consistent:
		return "consistent"
	case Changed:
		return "changed"
	case Removed:
		return "removed"
	case Missing:
		return "missing"
	}
	return "invalid"
}

// KeyRef is a key as CSP stores it, with its chord when it converts.
type KeyRef struct {
	// Key is the CSP key name (the letter of a tool key) and Modifier the
	// bitmask. Code is the raw NodeShortCutKey of a tool key, 0 for a command.
	Key      string
	Modifier int
	Code     int
	// Chord is valid when the key converts to padmate.
	Chord keys.Chord
}

// ItemStatus is the comparison of one saved item.
type ItemStatus struct {
	Item  Item
	State State
	// Current are the function's shortcuts in CSP now (Changed only). They may
	// include keys that do not convert; those have an invalid Chord.
	Current []KeyRef
	// Owners are the other functions that now hold the item's chord (Changed
	// only; may be empty).
	Owners []Owner
}

// Compare compares each saved item with CSP's current settings. Shortcuts in
// CSP that no item mentions are never differences. cmds and tools may be nil;
// an item whose file is missing is reported Missing.
func Compare(saved []Item, cmds *CommandSet, tools *ToolTree) []ItemStatus {
	an := NewAnalyzer(cmds, tools)
	out := make([]ItemStatus, 0, len(saved))
	for _, it := range saved {
		out = append(out, compareItem(an, it, cmds, tools))
	}
	return out
}

func compareItem(an *Analyzer, it Item, cmds *CommandSet, tools *ToolTree) ItemStatus {
	st := ItemStatus{Item: it}
	var current []KeyRef
	switch it.Kind {
	case KindCommand:
		var c *Command
		if cmds != nil {
			c = cmds.Command(it.Function)
		}
		if c == nil {
			st.State = Missing
			return st
		}
		name, mod, ok := FormatCSPKey(it.Chord)
		for _, s := range c.Shortcuts {
			if ok == KeyOK && s.Key == name && s.Modifier == mod {
				st.State = Consistent
				return st
			}
			current = append(current, KeyRef{Key: s.Key, Modifier: s.Modifier, Chord: s.Chord})
		}
	case KindTool:
		var n *ToolNode
		if tools != nil {
			n = tools.Node(it.Function)
		}
		if n == nil {
			st.State = Missing
			return st
		}
		if code, ok := ToolKeyCode(it.Chord); ok && n.KeyCode == code {
			st.State = Consistent
			return st
		}
		if n.KeyCode != 0 {
			ref := KeyRef{Code: n.KeyCode}
			if c, ok := ToolKeyChord(n.KeyCode); ok {
				ref.Chord = c
				ref.Key, _, _ = FormatCSPKey(c)
			}
			current = append(current, ref)
		}
	default:
		st.State = Missing
		return st
	}
	if len(current) == 0 {
		st.State = Removed
		return st
	}
	st.State = Changed
	st.Current = current
	for _, o := range an.Owners(it.Chord) {
		if o.Kind == it.Kind && o.Function == it.Function {
			continue
		}
		st.Owners = append(st.Owners, o)
	}
	return st
}

// AllConsistent reports whether every status is Consistent. An empty list is
// consistent.
func AllConsistent(statuses []ItemStatus) bool {
	for _, s := range statuses {
		if s.State != Consistent {
			return false
		}
	}
	return true
}
