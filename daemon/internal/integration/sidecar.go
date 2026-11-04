package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Limits of integrations/<id>.json (ARCHITECTURE section 16.2).
const (
	SidecarFormat  = 1
	MaxItems       = 512
	MaxFunctionLen = 256
	MaxNameLen     = 256
	MaxPathLen     = 8
)

// ParseItems parses integrations/<id>.json: strict JSON (the scanner of
// profile.json: no duplicate keys, trailing commas or comments), format 1,
// at most MaxItems items, each with exactly keys, function and the optional
// name and path; any other member is an error. Keys must be one KeySpec
// chord and are stored in canonical spelling; two items with the same chord
// are an error. The error says where the problem is when it can.
func ParseItems(data []byte) ([]Item, error) {
	doc, err := profile.DecodeStrict(data)
	if err != nil {
		return nil, err
	}
	root, ok := doc.(map[string]any)
	if !ok {
		return nil, errors.New("the file must be a JSON object")
	}
	for name := range root {
		if name != "format" && name != "items" {
			return nil, fmt.Errorf("unknown member %q", name)
		}
	}
	format, ok := root["format"].(json.Number)
	if !ok || format.String() != "1" {
		return nil, fmt.Errorf("format must be %d", SidecarFormat)
	}
	list, ok := root["items"].([]any)
	if !ok {
		return nil, errors.New("items must be an array")
	}
	if len(list) > MaxItems {
		return nil, fmt.Errorf("items has more than %d entries", MaxItems)
	}
	items := make([]Item, 0, len(list))
	seen := map[string]int{}
	for i, raw := range list {
		it, err := parseItem(raw)
		if err != nil {
			return nil, fmt.Errorf("items[%d]: %w", i, err)
		}
		if j, dup := seen[it.Keys]; dup {
			return nil, fmt.Errorf("items[%d]: keys %q already used by items[%d]", i, it.Keys, j)
		}
		seen[it.Keys] = i
		items = append(items, it)
	}
	return items, nil
}

func parseItem(raw any) (Item, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return Item{}, errors.New("an item must be an object")
	}
	var it Item
	for name := range m {
		switch name {
		case "keys", "function", "name", "path":
		default:
			return Item{}, fmt.Errorf("unknown member %q", name)
		}
	}
	spec, ok := m["keys"].(string)
	if !ok {
		return Item{}, errors.New("keys must be a string")
	}
	chord, err := keys.Parse(spec)
	if err != nil {
		return Item{}, fmt.Errorf("keys: %w", err)
	}
	it.Keys = chord.String()
	if it.Function, ok = m["function"].(string); !ok || it.Function == "" {
		return Item{}, errors.New("function must be a non-empty string")
	}
	if utf8.RuneCountInString(it.Function) > MaxFunctionLen {
		return Item{}, fmt.Errorf("function is longer than %d characters", MaxFunctionLen)
	}
	if v, present := m["name"]; present {
		if it.Name, ok = v.(string); !ok {
			return Item{}, errors.New("name must be a string")
		}
		if utf8.RuneCountInString(it.Name) > MaxNameLen {
			return Item{}, fmt.Errorf("name is longer than %d characters", MaxNameLen)
		}
	}
	if v, present := m["path"]; present {
		list, ok := v.([]any)
		if !ok {
			return Item{}, errors.New("path must be an array of strings")
		}
		if len(list) > MaxPathLen {
			return Item{}, fmt.Errorf("path has more than %d entries", MaxPathLen)
		}
		for _, e := range list {
			s, ok := e.(string)
			if !ok {
				return Item{}, errors.New("path must be an array of strings")
			}
			if utf8.RuneCountInString(s) > MaxNameLen {
				return Item{}, fmt.Errorf("a path entry is longer than %d characters", MaxNameLen)
			}
			it.Path = append(it.Path, s)
		}
	}
	return it, nil
}

// FormatItems writes items as integrations/<id>.json: two-space indent, a
// final newline, members in the order keys, function, name, path. It does not
// validate; ParseItems of the result does.
func FormatItems(items []Item) []byte {
	type wire struct {
		Keys     string   `json:"keys"`
		Function string   `json:"function"`
		Name     string   `json:"name,omitempty"`
		Path     []string `json:"path,omitempty"`
	}
	doc := struct {
		Format int    `json:"format"`
		Items  []wire `json:"items"`
	}{Format: SidecarFormat, Items: make([]wire, 0, len(items))}
	for _, it := range items {
		doc.Items = append(doc.Items, wire(it))
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		panic("integration: format items: " + err.Error()) // strings and ints only
	}
	return buf.Bytes()
}

// EmptyItems is the content of a sidecar file without items.
func EmptyItems() []byte { return FormatItems(nil) }

// CanonicalKeys returns the canonical spelling of a KeySpec chord, which is
// how items compare their keys.
func CanonicalKeys(spec string) (string, error) {
	c, err := keys.Parse(spec)
	if err != nil {
		return "", err
	}
	return c.String(), nil
}

// MergeItems returns the effective items of a profile that inherits from a
// parent: the parent's items, then the child's, and for the same keys the
// child's item wins (it takes the parent item's place). Neither input is
// changed.
func MergeItems(parent, child []Item) []Item {
	out := slices.Clone(parent)
	for _, c := range child {
		if i := slices.IndexFunc(out, func(p Item) bool { return p.Keys == c.Keys }); i >= 0 {
			out[i] = c
			continue
		}
		out = append(out, c)
	}
	return out
}
