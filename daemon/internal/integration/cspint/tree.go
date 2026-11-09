package cspint

import (
	"slices"
	"strconv"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/csp"
	"github.com/wdcyxxycdw/padmate/daemon/internal/integration"
)

// Node IDs of the tree that are not functions.
const (
	toolsNodeID = "tools"
	groupPrefix = "g:"
)

// cmdEntry is a command of the user's shortcut file.
type cmdEntry struct {
	native string // "<menucommandtype>/<menucommand>"
	id     string // the function ID
	name   string
	info   integration.CommandInfo
}

// cmdKey is the key a layout node finds a command by: the command id,
// lower-case.
func cmdKey(native string) string {
	_, cmd, ok := strings.Cut(native, "/")
	if !ok {
		cmd = native
	}
	return strings.ToLower(cmd)
}

// treeBuilder places commands and tools in the picker's tree.
type treeBuilder struct {
	byKey  map[string]*cmdEntry
	placed map[string]bool // function IDs
	ids    map[string]int  // group node IDs handed out
}

// buildTree builds the tree the way CSP's shortcut dialog is laid out. The
// layout (the dialog's areas, groups and order) says where a command goes; a
// command of the user's file that the layout does not list goes where its
// menu path or its plug-in group says, else into a trailing "Other" group. The
// tool tree is the "Tools" area, second like CSP's "setting area" list.
// Groups that end up with nothing in them are left out. Every function is
// placed exactly once.
func buildTree(layout []integration.LayoutNode, cmds []cmdEntry, tools []*csp.ToolNode, lang string) []integration.TreeNode {
	b := &treeBuilder{byKey: map[string]*cmdEntry{}, placed: map[string]bool{}, ids: map[string]int{}}
	for i := range cmds {
		if _, dup := b.byKey[cmdKey(cmds[i].native)]; !dup {
			b.byKey[cmdKey(cmds[i].native)] = &cmds[i]
		}
	}
	var tree []integration.TreeNode
	for _, n := range layout {
		if t, ok := b.convert(n); ok {
			tree = append(tree, t)
		}
	}

	// Commands the layout does not list.
	var rest []*cmdEntry
	for i := range cmds {
		if !b.placed[cmds[i].id] {
			rest = append(rest, &cmds[i])
		}
	}
	slices.SortStableFunc(rest, func(a, c *cmdEntry) int {
		return strings.Compare(strings.ToLower(a.name), strings.ToLower(c.name))
	})
	byChain := map[string]string{}  // names of the groups from the area down, to node ID
	byLayout := map[string]string{} // layout ID to node ID
	indexGroups(tree, nil, byChain, byLayout, true)
	adds := map[string][]integration.TreeNode{}
	subs := map[string]map[string][]integration.TreeNode{}
	var other []integration.TreeNode
	for _, c := range rest {
		leaf := integration.TreeNode{ID: c.id, Name: c.name, Function: c.id}
		target := ""
		switch {
		case c.info.Group != "":
			target = byLayout[c.info.Group]
		case len(c.info.Path) > 0:
			target = byChain[strings.Join(c.info.Path, "\x00")]
		}
		switch {
		case target == "":
			other = append(other, leaf)
		case c.info.Sub != "":
			if subs[target] == nil {
				subs[target] = map[string][]integration.TreeNode{}
			}
			subs[target][c.info.Sub] = append(subs[target][c.info.Sub], leaf)
		default:
			adds[target] = append(adds[target], leaf)
		}
		b.placed[c.id] = true
	}
	if len(adds) > 0 || len(subs) > 0 {
		tree = appendAdds(tree, adds, subs)
	}

	if t, ok := toolsArea(tools, lang); ok {
		at := min(1, len(tree))
		tree = slices.Insert(tree, at, t)
	}
	if len(other) > 0 {
		tree = append(tree, integration.TreeNode{ID: integration.OtherNode, Name: integration.OtherName(lang), Children: other})
	}
	return tree
}

// convert places the commands of a layout node and its descendants. A node
// that is a command the user has, and not yet placed, is a function (a group
// too); a group left with nothing is dropped.
func (b *treeBuilder) convert(n integration.LayoutNode) (integration.TreeNode, bool) {
	var fn *cmdEntry
	if e := b.byKey[strings.ToLower(n.ID)]; e != nil && !b.placed[e.id] {
		fn = e
		b.placed[e.id] = true
	}
	if len(n.Children) == 0 {
		if fn == nil {
			return integration.TreeNode{}, false
		}
		return integration.TreeNode{ID: fn.id, Name: fn.name, Function: fn.id}, true
	}
	var children []integration.TreeNode
	for _, c := range n.Children {
		if t, ok := b.convert(c); ok {
			children = append(children, t)
		}
	}
	if len(children) == 0 && fn == nil {
		return integration.TreeNode{}, false
	}
	t := integration.TreeNode{Name: n.Name, Children: children}
	if fn != nil {
		t.ID, t.Function = fn.id, fn.id
	} else {
		t.ID = b.groupID(n.ID)
	}
	return t, true
}

// groupID returns a node ID for a group of the layout, unique in the tree: a
// group that the layout repeats gets "#2", "#3" and so on.
func (b *treeBuilder) groupID(layoutID string) string {
	id := groupPrefix + layoutID
	b.ids[id]++
	if n := b.ids[id]; n > 1 {
		id += "#" + strconv.Itoa(n)
	}
	return id
}

// indexGroups records, for every group, the chain of names from the area's
// children down (byChain) and the first node of each layout ID (byLayout).
// The layout ID is read back from the node ID, which is "g:<id>" for a
// plain group.
func indexGroups(nodes []integration.TreeNode, chain []string, byChain, byLayout map[string]string, area bool) {
	for _, n := range nodes {
		if len(n.Children) == 0 {
			continue
		}
		c := chain
		if !area {
			c = append(slices.Clone(chain), n.Name)
			key := strings.Join(c, "\x00")
			if _, dup := byChain[key]; !dup {
				byChain[key] = n.ID
			}
		}
		if lid, ok := strings.CutPrefix(n.ID, groupPrefix); ok {
			lid, _, _ = strings.Cut(lid, "#")
			if _, dup := byLayout[lid]; !dup {
				byLayout[lid] = n.ID
			}
		}
		indexGroups(n.Children, c, byChain, byLayout, false)
	}
}

// appendAdds appends the added commands, and the subgroups (by name) after
// them, to the groups they are for.
func appendAdds(nodes []integration.TreeNode, adds map[string][]integration.TreeNode, subs map[string]map[string][]integration.TreeNode) []integration.TreeNode {
	for i := range nodes {
		n := &nodes[i]
		if len(n.Children) == 0 {
			continue
		}
		n.Children = appendAdds(n.Children, adds, subs)
		n.Children = append(n.Children, adds[n.ID]...)
		names := make([]string, 0, len(subs[n.ID]))
		for name := range subs[n.ID] {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			n.Children = append(n.Children, integration.TreeNode{ID: n.ID + "/" + name, Name: name, Children: subs[n.ID][name]})
		}
	}
	return nodes
}

// toolsArea is the "Tools" area: CSP's tool tree, whose three levels (tool,
// sub-tool group, sub-tool) each can have a key, so every node is a function
// and a node with children is both.
func toolsArea(tools []*csp.ToolNode, lang string) (integration.TreeNode, bool) {
	if len(tools) == 0 {
		return integration.TreeNode{}, false
	}
	have := map[string]bool{}
	for _, n := range tools {
		have[n.UUID] = true
	}
	kids := map[string][]*csp.ToolNode{}
	for _, n := range tools {
		parent := n.ParentUUID
		if !have[parent] {
			parent = ""
		}
		kids[parent] = append(kids[parent], n)
	}
	var build func(parent string, depth int) []integration.TreeNode
	build = func(parent string, depth int) []integration.TreeNode {
		var out []integration.TreeNode
		if depth > 16 {
			return nil
		}
		for _, n := range kids[parent] {
			id := toolFunctionID(n.UUID)
			out = append(out, integration.TreeNode{ID: id, Name: n.Name, Function: id, Children: build(n.UUID, depth+1)})
		}
		return out
	}
	name := "Tools"
	if strings.HasPrefix(strings.ToLower(lang), "zh") {
		name = "工具"
	}
	return integration.TreeNode{ID: toolsNodeID, Name: name, Children: build("", 0)}, true
}
