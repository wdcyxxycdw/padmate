package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// DefaultConfigText is the config.json that replaces one RepairConfig
// cannot read at all: the defaults (DefaultConfig), written as the UI
// writes the file.
const DefaultConfigText = "{\n  \"schemaVersion\": 0\n}\n"

// ConfigRepair is what RepairConfig did to a config.json (PRO-29).
type ConfigRepair struct {
	// Reset is true when the file was no JSON object at all (a syntax
	// error, a duplicate key, not UTF-8, another JSON value): the repair
	// is DefaultConfigText.
	Reset bool
	// Members are the JSON Pointers, into the file, of the members reset
	// to their defaults: left out, or schemaVersion set to 0. They are in
	// file order, and none is inside another.
	Members []string
}

// maxRepairRounds bounds RepairConfig. Each round leaves out the members
// that have errors, and leaving one out can only make its parent invalid
// (a required member is missing), so the nesting of the schema bounds the
// rounds a repair needs.
const maxRepairRounds = 8

// RepairConfig returns data, a config.json, with every error repaired
// (PRO-29): each error resets the smallest member around it that has a
// default, which is leaving the member out, and every other member stays,
// in its order and with its bytes. An error inside a list resets the
// whole list, apart from rules, where it resets one rule: a combo with
// a wrong button goes as a whole, so the built-in combo of its family
// applies, never the remaining buttons. A member that only lacks a
// required member (a rule without its exe, a combo without its holdMs)
// goes too. schemaVersion is set to 0. validDevice reports whether a key
// of devices is a valid DeviceKey, which this package cannot check; its
// settings go with a key it rejects.
//
// A file that is no JSON object at all is replaced by DefaultConfigText.
// The result always parses (ParseConfig) and is indented as the UI writes
// the file, without a BOM. For a valid file it has the same settings and
// ConfigRepair is zero.
func RepairConfig(data []byte, validDevice func(key string) bool) ([]byte, ConfigRepair) {
	reset := ConfigRepair{Reset: true}
	root, serr := scan(data)
	if serr != nil {
		return []byte(DefaultConfigText), reset
	}
	if _, ok := root.val.(map[string]any); !ok {
		return []byte(DefaultConfigText), reset
	}
	r := &configRepair{data: data, root: root, dropped: map[*node]bool{}, parent: map[*node]*node{}, ptr: map[*node]string{}}
	r.index(root, "")
	r.version = root.members["schemaVersion"]
	r.forceVersion = true
	if r.version != nil {
		v, ok := r.version.val.(json.Number)
		r.forceVersion = !ok || !isZero(v)
	}
	for range maxRepairRounds {
		doc, at := r.emit()
		drop, ok := r.problems(doc, at, validDevice)
		if !ok {
			break
		}
		if len(drop) == 0 {
			return indentConfig(doc), r.result()
		}
		for _, n := range drop {
			r.dropped[n] = true
		}
	}
	return []byte(DefaultConfigText), reset
}

func isZero(v json.Number) bool {
	f, err := v.Float64()
	return err == nil && f == 0
}

// indentConfig indents data, compact JSON, as the UI writes config.json:
// two spaces and a final newline.
func indentConfig(data []byte) []byte {
	var b bytes.Buffer
	if err := json.Indent(&b, data, "", "  "); err != nil {
		return []byte(DefaultConfigText) // not reached: data is valid JSON
	}
	b.WriteByte('\n')
	return b.Bytes()
}

// configRepair is the state of one RepairConfig: the scanned file, its
// members left out so far, and each node's parent and pointer in the
// file.
type configRepair struct {
	data         []byte
	root         *node
	version      *node // the root's schemaVersion member, or nil
	forceVersion bool  // write schemaVersion 0 instead of version
	dropped      map[*node]bool
	parent       map[*node]*node
	ptr          map[*node]string
}

func (r *configRepair) index(n *node, ptr string) {
	r.ptr[n] = ptr
	for i, c := range n.order {
		r.parent[c] = n
		if _, ok := n.val.(map[string]any); ok {
			r.index(c, childPointer(ptr, c.name))
		} else {
			r.index(c, ptr+"/"+strconv.Itoa(i))
		}
	}
}

// emit writes the file without the members left out, compact, and maps
// each pointer into the result to the node of the file it came from.
func (r *configRepair) emit() ([]byte, map[string]*node) {
	var b bytes.Buffer
	at := map[string]*node{}
	r.value(&b, r.root, "", at)
	return b.Bytes(), at
}

func (r *configRepair) value(b *bytes.Buffer, n *node, ptr string, at map[string]*node) {
	at[ptr] = n
	switch n.val.(type) {
	case map[string]any:
		b.WriteByte('{')
		first := true
		next := func() {
			if !first {
				b.WriteByte(',')
			}
			first = false
		}
		if n == r.root && r.version == nil {
			next()
			b.WriteString(`"schemaVersion":0`)
		}
		for _, m := range n.order {
			if r.dropped[m] {
				continue
			}
			next()
			b.Write(r.data[m.key:stringEnd(r.data, m.key)])
			b.WriteByte(':')
			if m == r.version && r.forceVersion {
				b.WriteByte('0')
				continue
			}
			r.value(b, m, childPointer(ptr, m.name), at)
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		i := 0
		for _, e := range n.order {
			if r.dropped[e] {
				continue
			}
			if i > 0 {
				b.WriteByte(',')
			}
			r.value(b, e, ptr+"/"+strconv.Itoa(i), at)
			i++
		}
		b.WriteByte(']')
	default:
		b.Write(r.data[n.start:n.end])
	}
}

// stringEnd returns the offset after the JSON string that starts at
// start, a valid string the scanner read.
func stringEnd(data []byte, start int) int {
	for i := start + 1; i < len(data); i++ {
		switch data[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(data)
}

// problems returns the nodes of the file to leave out so that doc, the
// file as emit wrote it with at, has no more errors than it would have
// without them: those the schema reports, else those ParseConfig reports,
// else the devices validDevice rejects. ok is false when an error cannot
// be repaired by leaving a member out.
func (r *configRepair) problems(doc []byte, at map[string]*node, validDevice func(string) bool) (drop []*node, ok bool) {
	tree, serr := scan(doc)
	schema, err := compiledConfigSchema()
	if serr != nil || err != nil {
		return nil, false
	}
	var ptrs []string
	if err := schema.Validate(tree.val); err != nil {
		var ve *jsonschema.ValidationError
		if !errors.As(err, &ve) {
			return nil, false
		}
		for _, e := range leaves(ve, nil) {
			loc := pointer(e.InstanceLocation)
			if k, ok := e.ErrorKind.(*kind.AdditionalProperties); ok {
				for _, p := range k.Properties {
					ptrs = append(ptrs, childPointer(loc, p))
				}
				continue
			}
			ptrs = append(ptrs, loc)
		}
	} else if c, ds := ParseConfig(doc); ds.HasErrors() {
		for _, d := range ds {
			if d.Severity == SeverityError {
				ptrs = append(ptrs, d.Pointer)
			}
		}
	} else {
		for key := range c.Devices {
			if !validDevice(key) {
				ptrs = append(ptrs, childPointer("/devices", key))
			}
		}
	}
	for _, p := range ptrs {
		n := r.member(at, p)
		if n == nil {
			return nil, false
		}
		if !slices.Contains(drop, n) {
			drop = append(drop, n)
		}
	}
	return drop, true
}

// member returns the node of the file to leave out for an error at p, a
// pointer into the emitted file: the member at p, or for p inside a list
// other than rules, the member that holds the list. It returns nil for
// the root and for schemaVersion, which leaving out cannot repair.
func (r *configRepair) member(at map[string]*node, p string) *node {
	n := at[p]
	for n == nil && p != "" {
		p = p[:strings.LastIndexByte(p, '/')]
		n = at[p]
	}
	rules := r.root.members["rules"]
	for n != nil && r.parent[n] != nil {
		parent := r.parent[n]
		if _, list := parent.val.([]any); !list || parent == rules {
			break
		}
		n = parent
	}
	if n == nil || n == r.root || n == r.version {
		return nil
	}
	return n
}

// result lists the members left out that are not inside another one left
// out, and schemaVersion if it was set, in file order.
func (r *configRepair) result() ConfigRepair {
	type reset struct {
		off int
		ptr string
	}
	var resets []reset
	if r.forceVersion {
		off := -1 // a missing schemaVersion is written first
		if r.version != nil {
			off = r.version.key
		}
		resets = append(resets, reset{off, "/schemaVersion"})
	}
	for n := range r.dropped {
		inside := false
		for p := r.parent[n]; p != nil; p = r.parent[p] {
			inside = inside || r.dropped[p]
		}
		if inside {
			continue
		}
		off := n.start
		if n.key >= 0 {
			off = n.key
		}
		resets = append(resets, reset{off, r.ptr[n]})
	}
	slices.SortFunc(resets, func(a, b reset) int { return a.off - b.off })
	var repair ConfigRepair
	for _, x := range resets {
		repair.Members = append(repair.Members, x.ptr)
	}
	return repair
}
