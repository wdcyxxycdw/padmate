package profile

import (
	"cmp"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
)

// Profile inheritance (PRO-19, ADR-0070). A profile of the library may name
// another, its parent, in a top-level "extends": what the profile does not
// write is taken from the parent, which may have a parent of its own.
//
// The merge works on documents, not on the parsed models. The parent's merged
// document is the base, and the child's document overrides it:
//
//   - layers and common are merged per layer and then per control: a control
//     the child writes replaces the parent's whole control (under whatever
//     name either of them wrote it: an alias is the same control); a layer
//     the child does not have is the parent's; a layer's color is the
//     child's if it has one;
//   - the function set (functionSet, ADR-0075) is merged per control like a
//     layer. A side that writes the old functionKeys is taken as the one set
//     it stands for (Profile.EffectiveFunctionSet) first: when both sides
//     have a set, the merged document has functionSet and no functionKeys;
//     when only one does, the document keeps it as it was written;
//   - timing and feedback are merged member by member, analog one level
//     deeper (analog.ls.deadzone is a member), and controlNames per control;
//   - defaultLayer, $schema and name are the child's if it writes them (name
//     always is); script is the child's as a whole, or else the parent's.
//
// The result is written out as a document of its own (Resolved.Merged) and
// parsed like any profile, so the merged profile passes every check of a
// profile that was written whole, and the engine compiles it as such. The text
// is assembled from the tokens of the source documents, so every byte of it
// maps back to a position in the file it came from: a diagnostic of the
// merged document is reported in that file (Diagnostic.Profile), with the
// pointer it has there too, because the merge keeps the structure.
//
// A parent must be a valid profile by itself (a parent with errors is an
// error of the child that says which parent), so only combinations of what
// the child and its parents write can be wrong in the merge, such as a
// deadzone above the saturation of the other.

// MaxExtendsDepth is the longest chain of parents: a profile may have up to
// this many of them, a parent's parent and so on.
const MaxExtendsDepth = 4

// maxMergedBytes bounds the text of a merged profile, which is made of up to
// MaxExtendsDepth+1 documents.
const maxMergedBytes = 16 << 20

// Library lets Resolve find the profiles that "extends" names: the profile
// library. ID is the library ID of the profile being resolved, "" when the
// text is not (yet) one of them, such as a draft in an editor; Load returns
// the file of profile id, and an error that is fs.ErrNotExist for a profile
// that is not there.
type Library struct {
	ID   string
	Load func(id string) ([]byte, error)
}

// ParentRef is a parent that went into a profile, with the sha256 of the
// bytes that Load returned for it.
type ParentRef struct {
	ID  string
	Sum [32]byte
}

// Origin says which profile's file a member of a merged profile comes from.
type Origin struct {
	Pointer string `json:"pointer"` // of the member in the merged profile
	Profile string `json:"profile"` // the library ID of the profile that wrote it
}

// Resolved is a profile with its parents merged in.
type Resolved struct {
	// Profile is the merged profile, as Parse returns a profile that is
	// written whole.
	Profile *Profile
	// Merged is the merged profile as a document of its own, without
	// "extends", indented with two spaces: what flattening writes. It is nil
	// for a profile without extends, whose own text is the whole profile.
	Merged []byte
	// Parents are the parents, nearest first, as in Profile.Parents.
	Parents []ParentRef

	self    string
	file    *doc
	origins map[string]string
}

// originOf returns the library ID of the profile that wrote the member of
// the merged profile at ptr, or of the closest value above it that the merge
// copied as a whole.
func (r *Resolved) originOf(ptr string) string {
	for p := ptr; ; {
		if id, ok := r.origins[p]; ok {
			return id
		}
		i := strings.LastIndexByte(p, '/')
		if i < 0 {
			return r.self
		}
		p = p[:i]
	}
}

// Inherited lists the members of the merged profile that a parent wrote, by
// pointer, with that parent: what the merge took from the parents. A control
// the child writes is not listed; a value that the merge copied as a whole is
// one entry, such as "/timing" or "/layers/base/south". It is empty for a
// profile without extends.
func (r *Resolved) Inherited() []Origin {
	var out []Origin
	for ptr, id := range r.origins {
		if id != r.self {
			out = append(out, Origin{Pointer: ptr, Profile: id})
		}
	}
	slices.SortFunc(out, func(a, b Origin) int { return strings.Compare(a.Pointer, b.Pointer) })
	return out
}

// Locate reports diagnostics that were found in Merged, such as a check of
// the merged text, in the files they come from, as the diagnostics of Resolve
// are. For a profile without extends there is no merged text, and ds is
// returned as it is.
func (r *Resolved) Locate(ds Diagnostics) Diagnostics {
	if r.file == nil {
		return ds
	}
	return r.file.locateAll(ds)
}

// ExtendsOf returns the "extends" of the profile in data, "" when it has
// none or cannot be read. It checks nothing else, not even the id.
func ExtendsOf(data []byte) string {
	root, serr := scan(data)
	if serr != nil {
		return ""
	}
	s, _ := root.members["extends"].valueString()
	return s
}

func (n *node) valueString() (string, bool) {
	if n == nil {
		return "", false
	}
	s, ok := n.val.(string)
	return s, ok
}

// ParseIn is Parse for the profile of the library lib: its extends is
// resolved. The diagnostics may be in the files of its parents (Local).
func ParseIn(data []byte, lib Library) (*Profile, Diagnostics) {
	r, ds := Resolve(data, &lib)
	if r == nil {
		return nil, ds
	}
	return r.Profile, ds
}

// Resolve parses the profile in data like Parse does and, when it has an
// "extends", merges its parents in. lib is nil for a profile that is not of
// the library: extends is an error there. A parent is read with lib.Load, and
// parsed as a profile of its own first. The diagnostics are those of data,
// sorted by position, then those of parent files (Diagnostic.Profile).
func Resolve(data []byte, lib *Library) (*Resolved, Diagnostics) {
	d, ds := open(data)
	if d == nil {
		return nil, ds
	}
	var load func(string) ([]byte, error)
	var chain []string
	if lib != nil && lib.Load != nil {
		d.id, load = lib.ID, lib.Load
		if lib.ID != "" {
			chain = []string{lib.ID}
		}
	}
	return resolveDoc(d, load, chain, 0)
}

// resolveDoc resolves the document d, which passed the schema. load is nil
// when there is no library; chain holds the IDs from the profile that was
// asked for down to d, and depth is the number of parents between them.
func resolveDoc(d *doc, load func(string) ([]byte, error), chain []string, depth int) (*Resolved, Diagnostics) {
	ext := d.root.members["extends"]
	if ext == nil {
		p, ds := resolve(d.data, d.root, nil)
		ds = place(d.orig, ds)
		if ds.HasErrors() {
			return nil, ds
		}
		return &Resolved{Profile: p, self: d.id}, ds
	}
	parentID, _ := ext.val.(string)
	failure := func(msg string) (*Resolved, Diagnostics) {
		return nil, place(d.orig, Diagnostics{{Severity: SeverityError, Pointer: "/extends", Offset: ext.start, Message: msg}})
	}
	switch {
	case load == nil:
		return failure("extends works only in a profile of the library; a profile loaded from a file by itself cannot extend another")
	case !ValidLibraryID(parentID):
		return failure("invalid profile library id")
	case parentID == d.id:
		return failure("a profile cannot extend itself")
	case slices.Contains(chain, parentID):
		loop := append(slices.Clone(chain[slices.Index(chain, parentID):]), parentID)
		for i, id := range loop {
			loop[i] = strconv.Quote(id)
		}
		return failure("extends loops: " + strings.Join(loop, " → "))
	case depth >= MaxExtendsDepth:
		return failure(fmt.Sprintf("too many levels of extends: a profile can have at most %d parents in a chain", MaxExtendsDepth))
	}
	raw, err := load(parentID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return failure("parent " + strconv.Quote(parentID) + " is not in the profile library")
		}
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return failure("cannot read parent " + strconv.Quote(parentID) + ": " + err.Error())
	}
	pd, pds := open(raw)
	var pres *Resolved
	if pd != nil {
		pd.id = parentID
		pres, pds = resolveDoc(pd, load, append(slices.Clone(chain), parentID), depth+1)
	}
	if pres == nil {
		return nil, parentFailure(d, ext, parentID, pds)
	}
	base := pres.file
	if base == nil {
		base = pd
	}
	parents := append([]ParentRef{{ID: parentID, Sum: sha256.Sum256(raw)}}, pres.Parents...)
	m := &merger{parent: base, pres: pres, child: d, origins: map[string]string{}}
	m.merge()
	if m.over {
		return failure(fmt.Sprintf("the merged profile is larger than %d MiB", maxMergedBytes>>20))
	}
	root, serr := scan(m.out)
	if serr != nil { // not expected: the merge writes valid JSON
		return nil, Diagnostics{internalError(errors.New("merged profile: " + serr.msg))}
	}
	merged := &doc{id: d.id, orig: m.out, data: m.out, root: root, pieces: m.pieces}
	if ds := validate(root); ds.HasErrors() { // not expected: the parts passed it
		return nil, mergeFailure(d, ext, merged.locateAll(ds))
	}
	p, ds := resolve(m.out, root, merged.position)
	ds = merged.locateAll(ds)
	if ds.HasErrors() {
		return nil, mergeFailure(d, ext, ds)
	}
	p.Extends, p.Parents = parentID, parents
	if p.Script != nil {
		if id := (&Resolved{self: d.id, origins: m.origins}).originOf("/script"); id != d.id {
			p.Script.Origin = id
		}
	}
	return &Resolved{Profile: p, Merged: m.out, Parents: parents, self: d.id, file: merged, origins: m.origins}, ds
}

// parentFailure reports the errors pds of parent parentID, which were found
// by resolving it, as the errors of d, whose "extends" ext names it: the
// parent's diagnostics in its own file, and one error at the extends that
// says which parent and gives the first of them.
func parentFailure(d *doc, ext *node, parentID string, pds Diagnostics) Diagnostics {
	var own Diagnostic
	n := 0
	out := make(Diagnostics, 0, len(pds)+1)
	for _, p := range pds {
		if p.Profile == "" {
			if p.Severity == SeverityError {
				if n == 0 {
					own = p
				}
				n++
			}
			p.Profile = parentID
		}
		out = append(out, p)
	}
	msg := fmt.Sprintf("parent %s has errors: %d:%d: %s", strconv.Quote(parentID), own.Line, own.Col, own.Message)
	switch {
	case n == 0: // an error in a parent further up, which the parent reports as its own
		msg = "parent " + strconv.Quote(parentID) + " has errors"
	case n == 2:
		msg += " (and 1 more error)"
	case n > 2:
		msg += " (and " + strconv.Itoa(n-1) + " more errors)"
	}
	summary := place(d.orig, Diagnostics{{Severity: SeverityError, Pointer: "/extends", Offset: ext.start, Message: msg}})
	return append(summary, out...)
}

// mergeFailure adds to ds, the errors of the merge of d with its parents, one
// at the extends of d when none of them is in d itself, so that the profile
// says it has a problem whichever file it is in.
func mergeFailure(d *doc, ext *node, ds Diagnostics) Diagnostics {
	if ds.Local().HasErrors() {
		return ds
	}
	var first Diagnostic
	n := 0
	for _, x := range ds {
		if x.Severity == SeverityError {
			if n == 0 {
				first = x
			}
			n++
		}
	}
	msg := fmt.Sprintf("merged with its parents the profile has errors: parent %s %d:%d: %s", strconv.Quote(first.Profile), first.Line, first.Col, first.Message)
	switch {
	case n == 2:
		msg += " (and 1 more error)"
	case n > 2:
		msg += " (and " + strconv.Itoa(n-1) + " more errors)"
	}
	summary := place(d.orig, Diagnostics{{Severity: SeverityError, Pointer: "/extends", Offset: ext.start, Message: msg}})
	return append(summary, ds...)
}

// doc is a document that is merged or merged into: a file of the library, or
// a merged profile whose text is made of pieces of others.
type doc struct {
	id   string // the library ID of the file, "" when it has none
	orig []byte // the file as it was read: what positions are counted in
	data []byte // orig, migrated: same length and offsets
	root *node
	// pieces is set for a merged document: where each token of its text comes
	// from, ordered by the position in it.
	pieces []piece
}

// piece says that data[start:start+n] of a merged document is
// src.data[off:off+n].
type piece struct {
	start, n int
	src      *doc
	off      int
}

// locate maps offset off of d to the file it comes from, through the pieces
// of the merges between: the file and the offset in its text. An offset
// between two tokens is mapped to the end of the token before it.
func (d *doc) locate(off int) (*doc, int) {
	for d.pieces != nil {
		if len(d.pieces) == 0 {
			break
		}
		i := sort.Search(len(d.pieces), func(i int) bool { return d.pieces[i].start > off }) - 1
		i = max(i, 0)
		p := d.pieces[i]
		d, off = p.src, p.off+min(max(off-p.start, 0), p.n)
	}
	return d, off
}

// position formats the position in the file of offset off of d, for a
// message of the checks of a merged profile: "line:col", with the parent's ID
// before it when the file is not the one of d.
func (d *doc) position(off int) string {
	file, at := d.locate(off)
	line, col := position(file.orig, at)
	s := strconv.Itoa(line) + ":" + strconv.Itoa(col)
	if file.id != d.id {
		s = strconv.Quote(file.id) + " " + s
	}
	return s
}

// locateAll maps diagnostics ds of the text of the merged document d to the
// files they come from and places them there: the ones in the file of d
// first, sorted, then each parent's. A diagnostic that points at the whole
// document, as a check that finds a mistake in no one place does, is moved
// to its pointer when the pointer is in the document.
func (d *doc) locateAll(ds Diagnostics) Diagnostics {
	var files []*doc
	groups := map[*doc]Diagnostics{}
	for _, x := range ds {
		off := x.Offset
		if x.Pointer != "" && off == d.root.start {
			if n := d.root.lookupPointer(x.Pointer); n != nil {
				off = n.start
			}
		}
		file, at := d.locate(off)
		x.Offset = at
		if file.id != d.id {
			x.Profile = file.id
		} else {
			x.Profile = ""
		}
		if _, ok := groups[file]; !ok {
			files = append(files, file)
		}
		groups[file] = append(groups[file], x)
	}
	// The file of d has to come first, whether or not it has diagnostics.
	slices.SortStableFunc(files, func(a, b *doc) int {
		return cmp.Compare(fileRank(a.id != d.id), fileRank(b.id != d.id))
	})
	var out Diagnostics
	for _, f := range files {
		out = append(out, place(f.orig, groups[f])...)
	}
	return out
}

func fileRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// keyEnd returns the end of the key string that starts at start in data,
// which is valid JSON.
func keyEnd(data []byte, start int) int {
	i := start + 1
	for i < len(data) && data[i] != '"' {
		if data[i] == '\\' {
			i++
		}
		i++
	}
	return min(i+1, len(data))
}

func isObject(n *node) bool {
	_, ok := n.val.(map[string]any)
	return ok
}

// ref is a node of a document.
type ref struct {
	d *doc
	n *node
}

// mval is a value of the merged document: a node copied from a document as it
// is, an object made of members of both, or the version literal.
type mval struct {
	src ref
	obj *mobj
	lit string // set: write lit, a token that maps to src
}

type mobj struct {
	at      ref // the object this merges: its "{" maps there
	members []mmember
}

type mmember struct {
	name string
	key  ref // the member whose key token the document copies
	// keyLit, when set, is the key that the document writes instead of key's:
	// functionSet, for the members that were functionKeys. The key token maps
	// to key's all the same.
	keyLit string
	val    mval
}

// merger writes the merge of parent and child, the parent's merged document
// or file and the child's file. pres is the parent's resolution, which knows
// where its members come from.
type merger struct {
	parent, child *doc
	pres          *Resolved
	out           []byte
	pieces        []piece
	// over is set once out is larger than maxMergedBytes: nothing is written
	// after that, so a document made to be large with its indentation costs
	// no memory.
	over bool
	// origins maps the pointer of every value that the merge copied or
	// replaced as a whole to the library ID of the profile that wrote it.
	origins map[string]string
	// parentKeys is the pointers of pres.origins in sorted order, once
	// parentKeysDone: the ones below a pointer are then one range of it.
	parentKeys     []string
	parentKeysDone bool
}

// originsBelow returns the pointers of the parent's origins that are below
// ptr, which is the prefix ptr + "/" of the sorted keys.
func (m *merger) originsBelow(ptr string) []string {
	if !m.parentKeysDone {
		m.parentKeys, m.parentKeysDone = slices.Sorted(maps.Keys(m.pres.origins)), true
	}
	prefix := ptr + "/"
	start, _ := slices.BinarySearch(m.parentKeys, prefix)
	end := start
	for end < len(m.parentKeys) && strings.HasPrefix(m.parentKeys[end], prefix) {
		end++
	}
	return m.parentKeys[start:end]
}

func (m *merger) originOf(side *doc, ptr string) string {
	if side == m.child {
		return m.child.id
	}
	return m.pres.originOf(ptr)
}

// leaf takes the whole value n of side, which is at ptr in the merged
// document and, because the merge keeps the structure, in side.
func (m *merger) leaf(side *doc, n *node, ptr string) mval {
	return m.leafFrom(side, n, ptr, ptr)
}

// leafFrom is leaf for a value that is at src in side but at dst in the merged
// document: a member of the old functionKeys that the merge moves into
// functionSet.
func (m *merger) leafFrom(side *doc, n *node, src, dst string) mval {
	if side == m.parent {
		// A parent that is a merge itself knows where the parts of the value
		// come from; keep that, and name the value only if it does not.
		below := m.originsBelow(src)
		for _, k := range below {
			m.origins[dst+strings.TrimPrefix(k, src)] = m.pres.origins[k]
		}
		if len(below) > 0 {
			return mval{src: ref{side, n}}
		}
	}
	m.origins[dst] = m.originOf(side, src)
	return mval{src: ref{side, n}}
}

// merge writes the merged document to m.out.
func (m *merger) merge() {
	m.writeObj(m.top(), 0)
	m.lit("\n")
}

// top merges the two documents' roots. The members come in a fixed order, so
// that the text does not depend on how the files were written.
func (m *merger) top() *mobj {
	pr, cr := m.parent.root, m.child.root
	o := &mobj{at: ref{m.child, cr}}
	add := func(name string, key ref, v mval) {
		o.members = append(o.members, mmember{name: name, key: key, val: v})
	}
	// pick takes the child's member if it has one, else the parent's.
	pick := func(name string) {
		ptr := childPointer("", name)
		if cm := cr.members[name]; cm != nil {
			add(name, ref{m.child, cm}, m.leaf(m.child, cm, ptr))
		} else if pm := pr.members[name]; pm != nil {
			add(name, ref{m.parent, pm}, m.leaf(m.parent, pm, ptr))
		}
	}
	// combine merges the member when both have an object, else picks.
	combine := func(name string, merge func(ptr string, pn, cn *node) mval) {
		pm, cm := pr.members[name], cr.members[name]
		if pm != nil && cm != nil && isObject(pm) && isObject(cm) {
			add(name, ref{m.child, cm}, merge(childPointer("", name), pm, cm))
			return
		}
		pick(name)
	}
	pick("$schema")
	if cm := cr.members["schemaVersion"]; cm != nil {
		add("schemaVersion", ref{m.child, cm}, mval{src: ref{m.child, cm}, lit: strconv.Itoa(CurrentSchema)})
	}
	pick("name")
	pick("defaultLayer")
	combine("controlNames", m.members(1))
	combine("timing", m.members(1))
	combine("analog", m.members(2))
	combine("feedback", m.members(1))
	pick("script")
	combine("common", m.layer)
	combine("layers", m.named)
	m.functionSets(o, add, pick)
	return o
}

// setMember is a member of a layer or set, with where it is in its document.
type setMember struct {
	side ref    // the member's node and the document it is in
	src  string // its JSON Pointer in that document
}

// functionSet returns the function set of document d (ADR-0075): the members
// of its functionSet, or else of the one set its old functionKeys stands for:
// start's, then back's for the controls start's does not bind. at is the node
// whose "{" the merged object maps to; it is nil when d has no set.
func functionSet(d *doc) (at *node, members []setMember) {
	root := d.root
	if fs := root.members["functionSet"]; fs != nil && isObject(fs) {
		return fs, nodeMembers(d, fs, "/functionSet")
	}
	fk := root.members["functionKeys"]
	if fk == nil || !isObject(fk) {
		return nil, nil
	}
	taken := map[string]bool{}
	for _, name := range []string{"start", "back"} {
		set := fk.members[name]
		if set == nil || !isObject(set) {
			continue
		}
		for _, e := range nodeMembers(d, set, childPointer("/functionKeys", name)) {
			if name == "start" || !taken[canonicalName(e.side.n.name)] {
				members = append(members, e)
			}
		}
		for _, e := range set.order {
			taken[canonicalName(e.name)] = true
		}
	}
	return fk, members
}

// nodeMembers lists the members of n, an object of d at ptr, in order.
func nodeMembers(d *doc, n *node, ptr string) []setMember {
	out := make([]setMember, len(n.order))
	for i, e := range n.order {
		out[i] = setMember{ref{d, e}, childPointer(ptr, e.name)}
	}
	return out
}

// canonicalName is the name of the control that name is a key of, or name
// itself when it is none.
func canonicalName(name string) string {
	if c, ok := control.Parse(name); ok {
		return c.String()
	}
	return name
}

// functionSets adds the function set members to the merged document. When both
// documents have a set (functionSet, or the old functionKeys) the merged one
// is functionSet, merged per control like a layer; the child's own
// functionKeys stays when it also writes functionSet, so that the warning
// about it is still given. Otherwise the members are picked as they are.
func (m *merger) functionSets(o *mobj, add func(name string, key ref, v mval), pick func(name string)) {
	pat, pmem := functionSet(m.parent)
	cat, cmem := functionSet(m.child)
	if pat == nil || cat == nil {
		pick("functionSet")
		pick("functionKeys")
		return
	}
	merged := m.mergeSets("/functionSet", ref{m.child, cat}, pmem, cmem)
	add("functionSet", ref{m.child, cat}, merged)
	o.members[len(o.members)-1].keyLit = "functionSet" // whichever format the child wrote
	if m.child.root.members["functionSet"] != nil {
		if fk := m.child.root.members["functionKeys"]; fk != nil {
			add("functionKeys", ref{m.child, fk}, m.leaf(m.child, fk, "/functionKeys"))
		}
	}
}

// members merges two objects member by member: a member of the child
// replaces the parent's, down to depth levels of objects.
func (m *merger) members(depth int) func(ptr string, pn, cn *node) mval {
	var merge func(ptr string, pn, cn *node, depth int) mval
	merge = func(ptr string, pn, cn *node, depth int) mval {
		o := &mobj{at: ref{m.child, cn}}
		seen := map[string]bool{}
		for _, side := range []*node{pn, cn} {
			for _, e := range side.order {
				if seen[e.name] {
					continue
				}
				seen[e.name] = true
				pm, cm := pn.members[e.name], cn.members[e.name]
				p := childPointer(ptr, e.name)
				mem := mmember{name: e.name}
				switch {
				case cm == nil:
					mem.key, mem.val = ref{m.parent, pm}, m.leaf(m.parent, pm, p)
				case pm != nil && depth > 1 && isObject(pm) && isObject(cm):
					mem.key, mem.val = ref{m.child, cm}, merge(p, pm, cm, depth-1)
				default:
					mem.key, mem.val = ref{m.child, cm}, m.leaf(m.child, cm, p)
				}
				o.members = append(o.members, mem)
			}
		}
		return mval{obj: o}
	}
	return func(ptr string, pn, cn *node) mval { return merge(ptr, pn, cn, depth) }
}

// named merges two objects of layers by name: a name only one has is taken as
// it is, and one both have is merged as a layer. The parent's names come first.
func (m *merger) named(ptr string, pn, cn *node) mval {
	o := &mobj{at: ref{m.child, cn}}
	seen := map[string]bool{}
	for _, side := range []*node{pn, cn} {
		for _, e := range side.order {
			if seen[e.name] {
				continue
			}
			seen[e.name] = true
			pm, cm := pn.members[e.name], cn.members[e.name]
			p := childPointer(ptr, e.name)
			mem := mmember{name: e.name}
			switch {
			case cm == nil:
				mem.key, mem.val = ref{m.parent, pm}, m.leaf(m.parent, pm, p)
			case pm != nil && isObject(pm) && isObject(cm):
				mem.key, mem.val = ref{m.child, cm}, m.layer(p, pm, cm)
			default:
				mem.key, mem.val = ref{m.child, cm}, m.leaf(m.child, cm, p)
			}
			o.members = append(o.members, mem)
		}
	}
	return mval{obj: o}
}

// layer merges two layers, or sets, per control: every control the child
// writes replaces the parent's, whatever name each of them used. The child's
// own members are all kept, so that a control it writes under two names is
// still reported. The color is the child's if it has one. The controls come
// in control order.
func (m *merger) layer(ptr string, pn, cn *node) mval {
	return m.mergeSets(ptr, ref{m.child, cn}, nodeMembers(m.parent, pn, ptr), nodeMembers(m.child, cn, ptr))
}

// mergeSets is layer for two lists of members, which are at ptr in the merged
// document, whatever they are in their own: at is the child's object that the
// merged "{" maps to.
func (m *merger) mergeSets(ptr string, at ref, parent, child []setMember) mval {
	type entry struct {
		mem  setMember
		rank int
	}
	canon := func(name string) (string, int) {
		if name == "color" {
			return name, -1
		}
		if c, ok := control.Parse(name); ok {
			return c.String(), int(c)
		}
		return name, math.MaxInt
	}
	written := map[string]bool{}
	for _, e := range child {
		c, _ := canon(e.side.n.name)
		written[c] = true
	}
	var ents []entry
	for _, side := range [][]setMember{parent, child} {
		for _, e := range side {
			c, rank := canon(e.side.n.name)
			if e.side.d == m.parent && written[c] {
				continue
			}
			ents = append(ents, entry{e, rank})
		}
	}
	slices.SortStableFunc(ents, func(a, b entry) int {
		if a.rank != b.rank {
			return cmp.Compare(a.rank, b.rank)
		}
		return strings.Compare(a.mem.side.n.name, b.mem.side.n.name)
	})
	o := &mobj{at: at}
	for _, e := range ents {
		name := e.mem.side.n.name
		o.members = append(o.members, mmember{name: name, key: e.mem.side, val: m.leafFrom(e.mem.side.d, e.mem.side.n, e.mem.src, childPointer(ptr, name))})
	}
	return mval{obj: o}
}

// copy writes src.data[from:to] and records where it comes from.
func (m *merger) copy(src *doc, from, to int) {
	if m.over {
		return
	}
	m.pieces = append(m.pieces, piece{start: len(m.out), n: to - from, src: src, off: from})
	m.out = append(m.out, src.data[from:to]...)
	m.over = len(m.out) > maxMergedBytes
}

// lit writes s, which has no source.
func (m *merger) lit(s string) {
	if m.over {
		return
	}
	m.out = append(m.out, s...)
	m.over = len(m.out) > maxMergedBytes
}

// nl starts a line indented by depth levels.
func (m *merger) nl(depth int) {
	if m.over {
		return
	}
	m.out = append(m.out, '\n')
	for range depth {
		m.out = append(m.out, ' ', ' ')
	}
	m.over = len(m.out) > maxMergedBytes
}

// bracket writes the one-byte bracket s of the node at src.
func (m *merger) bracket(src ref, s string) {
	if m.over {
		return
	}
	m.pieces = append(m.pieces, piece{start: len(m.out), n: 1, src: src.d, off: src.n.start})
	m.out = append(m.out, s...)
	m.over = len(m.out) > maxMergedBytes
}

func (m *merger) writeVal(v mval, depth int) {
	switch {
	case v.obj != nil:
		m.writeObj(v.obj, depth)
	case v.lit != "":
		if !m.over {
			m.pieces = append(m.pieces, piece{start: len(m.out), n: 1, src: v.src.d, off: v.src.n.start})
		}
		m.lit(v.lit)
	default:
		m.writeNode(v.src.d, v.src.n, depth)
	}
}

func (m *merger) writeObj(o *mobj, depth int) {
	m.bracket(o.at, "{")
	if len(o.members) == 0 {
		m.lit("}")
		return
	}
	for i, mem := range o.members {
		m.nl(depth + 1)
		if mem.keyLit != "" {
			if !m.over {
				m.pieces = append(m.pieces, piece{start: len(m.out), n: 1, src: mem.key.d, off: mem.key.n.key})
			}
			m.lit(strconv.Quote(mem.keyLit))
		} else {
			m.copy(mem.key.d, mem.key.n.key, keyEnd(mem.key.d.data, mem.key.n.key))
		}
		m.lit(": ")
		m.writeVal(mem.val, depth+1)
		if i < len(o.members)-1 {
			m.lit(",")
		}
	}
	m.nl(depth)
	m.lit("}")
}

// writeNode writes the node n of d as it is, re-indented: the tokens are
// d's, the layout is the merged document's.
func (m *merger) writeNode(d *doc, n *node, depth int) {
	switch n.val.(type) {
	case map[string]any:
		m.bracket(ref{d, n}, "{")
		if len(n.order) == 0 {
			m.lit("}")
			return
		}
		for i, c := range n.order {
			m.nl(depth + 1)
			m.copy(d, c.key, keyEnd(d.data, c.key))
			m.lit(": ")
			m.writeNode(d, c, depth+1)
			if i < len(n.order)-1 {
				m.lit(",")
			}
		}
		m.nl(depth)
		m.lit("}")
	case []any:
		m.bracket(ref{d, n}, "[")
		scalars := !slices.ContainsFunc(n.order, func(c *node) bool {
			switch c.val.(type) {
			case map[string]any, []any:
				return true
			}
			return false
		})
		for i, c := range n.order {
			switch {
			case scalars && i > 0:
				m.lit(", ")
			case !scalars:
				m.nl(depth + 1)
			}
			m.writeNode(d, c, depth+1)
			if !scalars && i < len(n.order)-1 {
				m.lit(",")
			}
		}
		if !scalars {
			m.nl(depth)
		}
		m.lit("]")
	default:
		m.copy(d, n.start, n.end)
	}
}
