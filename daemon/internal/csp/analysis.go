package csp

import (
	"slices"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// Owner is a function that holds a key.
type Owner struct {
	Kind ItemKind
	// Function is the command identity "<type>/<command>" or the tool node UUID.
	Function string
	// Name is the tool node name; empty for commands (their names come from
	// CSP's resources, not from the files).
	Name string
	// Path is the tool path of names; nil for commands.
	Path []string
}

// Analyzer answers questions about who holds which key. Either part may be
// nil, for example when only the khc could be read.
type Analyzer struct {
	cmds  *CommandSet
	tools *ToolTree
}

// NewAnalyzer returns an Analyzer over cmds and tools, either of which may be nil.
func NewAnalyzer(cmds *CommandSet, tools *ToolTree) *Analyzer {
	return &Analyzer{cmds: cmds, tools: tools}
}

func commandOwner(c *Command) Owner { return Owner{Kind: KindCommand, Function: c.ID} }

func toolOwner(n *ToolNode) Owner {
	return Owner{Kind: KindTool, Function: n.UUID, Name: n.Name, Path: slices.Clone(n.Path)}
}

// Owners returns every function that uses chord: commands in khc row order,
// then tools in tree order. A chord CSP cannot spell has no owners.
func (a *Analyzer) Owners(chord keys.Chord) []Owner {
	name, mod, st := FormatCSPKey(chord)
	if st != KeyOK {
		return nil
	}
	var out []Owner
	if a.cmds != nil {
		for _, c := range a.cmds.Commands {
			for _, s := range c.Shortcuts {
				if s.Key == name && s.Modifier == mod {
					out = append(out, commandOwner(c))
					break
				}
			}
		}
	}
	if code, ok := ToolKeyCode(chord); ok && a.tools != nil {
		for _, n := range a.tools.Nodes {
			if n.KeyCode == code {
				out = append(out, toolOwner(n))
			}
		}
	}
	return out
}

// CheckState is the verdict of Check.
type CheckState uint8

// Check verdicts.
const (
	// CheckFree: nobody else holds the chord.
	CheckFree CheckState = iota
	// CheckCycle: the target is a tool and only other tools hold the key, which
	// is CSP's cycle shortcut, not a conflict.
	CheckCycle
	// CheckConflict: a different command, or a command against a tool.
	CheckConflict
)

// String returns "free", "cycle" or "conflict".
func (s CheckState) String() string {
	switch s {
	case CheckFree:
		return "free"
	case CheckCycle:
		return "cycle"
	case CheckConflict:
		return "conflict"
	}
	return "invalid"
}

// Target names the function a key is meant for.
type Target struct {
	Kind ItemKind
	// Function is the command identity or the tool node UUID.
	Function string
}

// CheckResult is the answer of Check.
type CheckResult struct {
	State CheckState
	// Owners are the other functions that hold the chord (never the target).
	Owners []Owner
}

// Check says what happens if target takes chord. Owners lists the other holders
// in khc row order then tree order.
func (a *Analyzer) Check(target Target, chord keys.Chord) CheckResult {
	var others []Owner
	toolsOnly := true
	for _, o := range a.Owners(chord) {
		if o.Kind == target.Kind && o.Function == target.Function {
			continue
		}
		others = append(others, o)
		if o.Kind != KindTool {
			toolsOnly = false
		}
	}
	switch {
	case len(others) == 0:
		return CheckResult{State: CheckFree}
	case target.Kind == KindTool && toolsOnly:
		return CheckResult{State: CheckCycle, Owners: others}
	}
	return CheckResult{State: CheckConflict, Owners: others}
}

// Cycle is one single key shared by several tool nodes.
type Cycle struct {
	// Code is the NodeShortCutKey and Chord its key (invalid for a code that
	// is not 1-26).
	Code  int
	Chord keys.Chord
	// Tools are the sharing nodes in tree order.
	Tools []*ToolNode
}

// Cycles lists every key shared by more than one tool node, by ascending code.
func (a *Analyzer) Cycles() []Cycle {
	if a.tools == nil {
		return nil
	}
	by := map[int][]*ToolNode{}
	for _, n := range a.tools.Nodes {
		if n.KeyCode != 0 {
			by[n.KeyCode] = append(by[n.KeyCode], n)
		}
	}
	var out []Cycle
	for code, nodes := range by {
		if len(nodes) > 1 {
			c, _ := ToolKeyChord(code)
			out = append(out, Cycle{Code: code, Chord: c, Tools: nodes})
		}
	}
	slices.SortFunc(out, func(x, y Cycle) int { return x.Code - y.Code })
	return out
}
