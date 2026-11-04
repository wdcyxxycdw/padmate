package cspnames

import (
	"bytes"
	_ "embed" // table.json
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Table is the bundled table of command names, generated from the CSP
// executable by `padmate-probe csp-names` (ADR-0019). It holds only the
// commands that no menu of the main resource covers.
type Table struct {
	// CSP is the version of CSP the table was generated from.
	CSP string `json:"csp"`
	// Categories are the groups the commands belong to, in display order.
	Categories []TableCategory `json:"categories"`
	// Commands maps a lower-case command id to its names.
	Commands map[string]TableCommand `json:"commands"`
}

// TableCategory is a group of commands outside the menus.
type TableCategory struct {
	ID string `json:"id"`
	ZH string `json:"zh"`
	EN string `json:"en"`
}

// TableCommand is the name of one command in Simplified Chinese and in
// English, as CSP shows them.
type TableCommand struct {
	ZH string `json:"zh"`
	EN string `json:"en"`
	// Category is the ID of a TableCategory.
	Category string   `json:"cat,omitempty"`
	PathZH   []string `json:"path_zh,omitempty"`
	PathEN   []string `json:"path_en,omitempty"`
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
	cats := map[string]bool{}
	for _, c := range t.Categories {
		if c.ID == "" || c.ZH == "" || c.EN == "" || cats[c.ID] {
			return nil, fmt.Errorf("cspnames: table: bad category %+v", c)
		}
		cats[c.ID] = true
	}
	for id, c := range t.Commands {
		if id == "" || id != strings.ToLower(id) || c.ZH == "" || c.EN == "" {
			return nil, fmt.Errorf("cspnames: table: bad command %q", id)
		}
		if c.Category != "" && !cats[c.Category] {
			return nil, fmt.Errorf("cspnames: table: command %q has unknown category %q", id, c.Category)
		}
	}
	return &t, nil
}

// pick returns the names for a language: zh for "zh", English otherwise.
func (c TableCommand) pick(zh bool) (name string, path []string) {
	if zh {
		return c.ZH, c.PathZH
	}
	return c.EN, c.PathEN
}

// MarshalTable renders a table as JSON with one category and one command per
// line, in a fixed order, so that regenerating it gives a readable diff.
func MarshalTable(t *Table) ([]byte, error) {
	var b bytes.Buffer
	csp, _ := marshalLine(t.CSP)
	fmt.Fprintf(&b, "{\n\"csp\": %s,\n\"categories\": [\n", csp)
	for i, c := range t.Categories {
		line, err := marshalLine(c)
		if err != nil {
			return nil, err
		}
		b.Write(line)
		if i < len(t.Categories)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
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
