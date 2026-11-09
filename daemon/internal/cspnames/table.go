package cspnames

import (
	"bytes"
	_ "embed" // table.json
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Table is the bundled table of command names and of the shortcut dialog's
// tree, generated from the CSP executable by `padmate-probe csp-names`
// (ADR-0019). Its commands are only those that no menu of the main resource
// covers; its tree places every command of the dialog, menu commands too.
type Table struct {
	// CSP is the version of CSP the table was generated from.
	CSP string `json:"csp"`
	// Tree is the shortcut dialog, in dialog order: top-level groups, each
	// with its children. Every command id is placed at most once.
	Tree []TableNode `json:"tree"`
	// Commands maps a lower-case command id to its names.
	Commands map[string]TableCommand `json:"commands"`
}

// maxTreeDepth and maxTreeNodes bound a parsed tree.
const (
	maxTreeDepth = 8
	maxTreeNodes = 20000
)

// TableNode is a node of the dialog tree: a group (Children, names) or a
// command (only ID). A group may also be a command: its ID is then a command
// id too; the table cannot tell, the user's shortcut file does.
type TableNode struct {
	// ID is the group's id ("fileparent", "canvasviewparent", or a command id
	// such as "editquickshape" for a group that is also a command) or the
	// id of the command, lower-case.
	ID string
	// ZH and EN name a group; they are empty for a command.
	ZH, EN string
	// Children of a group, in dialog order.
	Children []TableNode
}

// IsGroup reports whether the node is a group.
func (n TableNode) IsGroup() bool { return n.ZH != "" }

type tableNodeJSON struct {
	ID       string      `json:"id"`
	ZH       string      `json:"zh"`
	EN       string      `json:"en"`
	Children []TableNode `json:"c"`
}

// MarshalJSON writes a command as its id string and a group as an object.
func (n TableNode) MarshalJSON() ([]byte, error) {
	if !n.IsGroup() {
		return marshalLine(n.ID)
	}
	return marshalLine(tableNodeJSON(n))
}

// UnmarshalJSON reads what MarshalJSON writes.
func (n *TableNode) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		*n = TableNode{}
		return json.Unmarshal(data, &n.ID)
	}
	var g tableNodeJSON
	if err := json.Unmarshal(data, &g); err != nil {
		return err
	}
	*n = TableNode(g)
	return nil
}

// TableCommand is the name of one command in Simplified Chinese and in
// English, as CSP shows them.
type TableCommand struct {
	ZH string `json:"zh"`
	EN string `json:"en"`
	// AltZH and AltEN are other labels the CSP code gives the same id (it
	// picks one at run time); ZH and EN are the likeliest.
	AltZH []string `json:"alt_zh,omitempty"`
	AltEN []string `json:"alt_en,omitempty"`
}

//go:embed table.json
var tableJSON []byte

var (
	tableOnce sync.Once
	table     *Table
	tableErr  error
)

// BundledTable returns the table that ships with padmate. The result is
// shared; callers must not modify it.
func BundledTable() (*Table, error) {
	tableOnce.Do(func() { table, tableErr = ParseTable(tableJSON) })
	return table, tableErr
}

// ParseTable parses and checks a table.
func ParseTable(data []byte) (*Table, error) {
	var t Table
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("cspnames: table: %w", err)
	}
	for id, c := range t.Commands {
		if id == "" || id != strings.ToLower(id) || c.ZH == "" || c.EN == "" {
			return nil, fmt.Errorf("cspnames: table: bad command %q", id)
		}
	}
	count := 0
	var check func(nodes []TableNode, depth int) error
	check = func(nodes []TableNode, depth int) error {
		if depth > maxTreeDepth {
			return fmt.Errorf("cspnames: table: tree deeper than %d", maxTreeDepth)
		}
		for _, n := range nodes {
			if count++; count > maxTreeNodes {
				return errors.New("cspnames: table: tree too large")
			}
			if n.ID == "" || n.ID != strings.ToLower(n.ID) {
				return fmt.Errorf("cspnames: table: bad tree node %q", n.ID)
			}
			if !n.IsGroup() {
				continue
			}
			if n.EN == "" || len(n.Children) == 0 {
				return fmt.Errorf("cspnames: table: bad group %q", n.ID)
			}
			if err := check(n.Children, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check(t.Tree, 1); err != nil {
		return nil, err
	}
	for _, n := range t.Tree {
		if !n.IsGroup() {
			return nil, fmt.Errorf("cspnames: table: command %q outside a group", n.ID)
		}
	}
	return &t, nil
}

// pick returns the name for a language: zh for "zh", English otherwise.
func (c TableCommand) pick(zh bool) string {
	if zh {
		return c.ZH
	}
	return c.EN
}

// MarshalTable renders a table as JSON with one group header, runs of
// commands and one command entry per line, in a fixed order, so that
// regenerating it gives a readable diff.
func MarshalTable(t *Table) ([]byte, error) {
	var b bytes.Buffer
	csp, _ := marshalLine(t.CSP)
	fmt.Fprintf(&b, "{\n\"csp\": %s,\n\"tree\": [\n", csp)
	if err := marshalNodes(&b, t.Tree, 0); err != nil {
		return nil, err
	}
	b.WriteString("],\n\"commands\": {\n")
	ids := make([]string, 0, len(t.Commands))
	for id := range t.Commands {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for i, id := range ids {
		key, _ := marshalLine(id)
		line, err := marshalLine(t.Commands[id])
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteString(": ")
		b.Write(line)
		if i < len(ids)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("}\n}\n")
	return b.Bytes(), nil
}

// marshalNodes writes siblings: a group as a header line, its children
// indented, and a closing line; runs of commands are wrapped at about 100
// characters.
func marshalNodes(b *bytes.Buffer, nodes []TableNode, depth int) error {
	indent := strings.Repeat(" ", depth)
	line := 0
	flush := func() {
		if line > 0 {
			b.WriteByte('\n')
			line = 0
		}
	}
	for i, n := range nodes {
		sep := ","
		if i == len(nodes)-1 {
			sep = ""
		}
		if !n.IsGroup() {
			id, err := marshalLine(n.ID)
			if err != nil {
				return err
			}
			if line > 0 && line+len(id) > 100 {
				flush()
			}
			if line == 0 {
				b.WriteString(indent)
			}
			b.Write(id)
			b.WriteString(sep)
			line += len(id) + len(sep)
			continue
		}
		flush()
		head, err := marshalLine(struct {
			ID string `json:"id"`
			ZH string `json:"zh"`
			EN string `json:"en"`
		}{n.ID, n.ZH, n.EN})
		if err != nil {
			return err
		}
		// {"id":..,"zh":..,"en":..} becomes {"id":..,"zh":..,"en":..,"c":[
		fmt.Fprintf(b, "%s%s,\"c\":[\n", indent, head[:len(head)-1])
		if err := marshalNodes(b, n.Children, depth+1); err != nil {
			return err
		}
		fmt.Fprintf(b, "%s]}%s\n", indent, sep)
	}
	flush()
	return nil
}

// marshalLine encodes v on one line without escaping <, > and &.
func marshalLine(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}
