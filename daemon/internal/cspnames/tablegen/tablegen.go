// Package tablegen regenerates the bundled table of CSP command names
// (internal/cspnames/table.json) from the CLIP STUDIO PAINT executable and
// its resources. It is a development tool's engine: padmate-probe csp-names
// runs it; padmate itself never reads the executable.
//
// CSP keeps the text of commands outside the menus in its code. About 2400
// static initialisers build the command objects, each pairing a global with
// its command id:
//
//	mov r9d,1 ; lea r8,[id] ; lea rdx,[GUID] ; lea rcx,[global] ; jmp ctor
//
// Two big functions then register every command: lea rcx,[global] ... mov
// dword [rsp+20h|28h],STRREF ... call add. A STRREF is (block id << 16) |
// string id into the string blocks of the main resource, the same in every
// language. One of the functions (region A) builds the tree of the shortcut
// dialog and also passes (is-leaf, depth, flag); the other (region B) is the
// command table. The table's tree is region A: groups with names, commands
// by id, in dialog order. The code regions are virtual-address ranges of one CSP
// version, so they are flags; the defaults are those of CSP 5.1.5.
package tablegen

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/cspnames"
)

// Region is a range of virtual addresses.
type Region struct{ Start, End uint64 }

func (r Region) contains(va uint64) bool { return va >= r.Start && va < r.End }

// Defaults of CSP 5.1.5.
var (
	DefaultRegionA = Region{0x140f80000, 0x140fb0000}
	DefaultRegionB = Region{0x14111a000, 0x141130000}
)

// DefaultVersion is the CSP version the default regions belong to.
const DefaultVersion = "5.1.5"

// Options configures Generate.
type Options struct {
	// InstallDir holds CLIPStudioPaint.exe and resource\.
	InstallDir string
	// Version is recorded in the table.
	Version string
	// RegionA and RegionB are the code ranges to scan; zero values mean the
	// defaults.
	RegionA, RegionB Region
}

// Report counts what Generate found, for the tool to print.
type Report struct {
	Stubs, MainStubs int // static initialisers, and those of the main resource
	Entries          int // registrations in regions A and B
	WithText         int // of those, with a string reference that resolves
	IDs              int // distinct command ids with a name
	MenuCovered      int // of those, named by the menu resource
	Commands         int // commands in the table
	Ambiguous        int // commands with more than one candidate name
	RegionAEntries   int
	// UnknownDepth counts tree entries whose depth the register tracking
	// could not find; they are put under the group that is open.
	UnknownDepth int
	// PhoneGroups counts the top-level groups left out because they are the
	// phone layout's.
	PhoneGroups int
	// Groups and Leaves count the nodes of the tree in the table.
	Groups, Leaves int
	// DissolvedGroups are the ids of the groups of the tree that have no
	// plain name (CSP names them by another kind of reference): their
	// commands are in the tree, the level is not.
	DissolvedGroups []string
}

// Plausibility bounds: the numbers of CSP 5.1.5 are 2425, 2019, 1722, 1324,
// 1016 and about 480. Far fewer means the patterns no longer match this
// executable.
const (
	minStubs, minMainStubs, minEntries, minWithText, minIDs, minCommands, minRegionA = 1500, 1200, 1000, 800, 600, 150, 500
)

// ErrImplausible is returned when the scan finds far fewer things than the
// executable of CSP 5.1.5 has: the patterns or the regions do not fit.
var ErrImplausible = errors.New("tablegen: the scan found implausibly little")

func readResource(dir, lang string) (*cspnames.Resource, error) {
	data, err := os.ReadFile(filepath.Join(dir, "resource", lang, cspnames.MainResourceName))
	if err != nil {
		return nil, err
	}
	r, err := cspnames.ParseResource(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", lang, err)
	}
	return r, nil
}

// treeNode is a node of the dialog tree as the walk of region A finds it.
type treeNode struct {
	id       string // lower-case
	group    bool
	strref   uint32
	hasRef   bool
	children []*treeNode
}

// info is what the walk learns about one command id.
type info struct {
	cands []uint32
}

// menuBarEnd is the id of the first top-level group after the menu bar: the
// groups before it are the menus, the ones from it on are the palettes and
// the other places a shortcut can sit.
const menuBarEnd = "canvasparent"

// Generate builds the table from the installation in opts.InstallDir.
func Generate(opts Options) (*cspnames.Table, *Report, error) {
	if opts.Version == "" {
		opts.Version = DefaultVersion
	}
	if opts.RegionA == (Region{}) {
		opts.RegionA = DefaultRegionA
	}
	if opts.RegionB == (Region{}) {
		opts.RegionB = DefaultRegionB
	}
	zh, err := readResource(opts.InstallDir, "chinese_sc")
	if err != nil {
		return nil, nil, err
	}
	en, err := readResource(opts.InstallDir, "english")
	if err != nil {
		return nil, nil, err
	}
	exe, err := os.ReadFile(filepath.Join(opts.InstallDir, "CLIPStudioPaint.exe"))
	if err != nil {
		return nil, nil, err
	}
	im, err := openImage(exe)
	if err != nil {
		return nil, nil, err
	}
	textSec := im.section(".text")
	if textSec == nil {
		return nil, nil, errNoText
	}
	text, err := im.bytesOf(textSec)
	if err != nil {
		return nil, nil, err
	}
	textVA := im.base + uint64(textSec.VirtualAddress)

	rep := &Report{}
	stubs := findStubs(im, text, textVA)
	rep.Stubs = len(stubs)
	allGlobals := make(map[uint64]struct{}, len(stubs))
	mainGlobal := map[uint64]string{}
	for _, s := range stubs {
		allGlobals[s.global] = struct{}{}
		if strings.EqualFold(s.guid, cspnames.MainResourceName) {
			mainGlobal[s.global] = s.id
		}
	}
	rep.MainStubs = len(mainGlobal)
	refs := findRefs(text, textVA, allGlobals)
	entries := extract(im, refs, mainGlobal, opts.RegionA, opts.RegionB)
	rep.Entries = len(entries)

	infos := map[string]*info{} // by lower-case id
	order := []string{}
	var roots []*treeNode // the dialog's top-level nodes, in tree order
	var stack []*treeNode // the open groups
	for _, e := range entries {
		var cands []uint32
		for _, v := range e.c7 {
			if _, ok := zh.StringRef(v); ok {
				cands = []uint32{v}
				break
			}
		}
		if cands == nil {
			for _, v := range e.mov {
				if _, ok := zh.StringRef(v); ok {
					cands = append(cands, v)
				}
			}
		}
		key := strings.ToLower(e.id)
		if len(cands) > 0 {
			rep.WithText++
			in := infos[key]
			if in == nil {
				in = &info{}
				infos[key] = in
				order = append(order, key)
			}
			in.cands = append(in.cands, cands...)
		}
		if e.region != 'A' {
			continue
		}
		rep.RegionAEntries++
		edx, r8 := e.args[0], e.args[1]
		depth := len(stack)
		if r8 != nil && *r8 <= maxDepth {
			depth = int(*r8)
		} else {
			rep.UnknownDepth++
		}
		stack = stack[:min(len(stack), depth)]
		n := &treeNode{id: key, group: edx != nil && *edx == 0}
		if len(cands) > 0 {
			n.strref, n.hasRef = cands[0], true
		}
		if len(stack) == 0 {
			roots = append(roots, n)
		} else {
			p := stack[len(stack)-1]
			p.children = append(p.children, n)
		}
		if n.group {
			stack = append(stack, n)
		}
	}
	rep.IDs = len(infos)
	if rep.Stubs < minStubs || rep.MainStubs < minMainStubs || rep.Entries < minEntries ||
		rep.WithText < minWithText || rep.IDs < minIDs || rep.RegionAEntries < minRegionA {
		return nil, rep, fmt.Errorf("%w: %+v", ErrImplausible, *rep)
	}

	// A command is covered by the menu resource only when the menu resource
	// of both languages has it; one that is in the menu of a single language
	// goes in the table, so that the other language has a name too.
	inZH := map[string]bool{}
	for _, c := range zh.Menu {
		inZH[strings.ToLower(c.ID)] = true
	}
	menu := map[string]bool{}
	for _, c := range en.Menu {
		if key := strings.ToLower(c.ID); inZH[key] {
			menu[key] = true
		}
	}

	t := &cspnames.Table{CSP: opts.Version, Commands: map[string]cspnames.TableCommand{}}
	for _, key := range order {
		if menu[key] {
			rep.MenuCovered++
			continue
		}
		in := infos[key]
		cmd := cspnames.TableCommand{}
		cmd.ZH, cmd.AltZH = names(zh, in.cands)
		cmd.EN, cmd.AltEN = names(en, in.cands)
		if cmd.ZH == "" || cmd.EN == "" {
			continue
		}
		if len(cmd.AltZH) > 0 || len(cmd.AltEN) > 0 {
			rep.Ambiguous++
		}
		t.Commands[key] = cmd
	}
	rep.Commands = len(t.Commands)
	if rep.Commands < minCommands {
		return nil, rep, fmt.Errorf("%w: %d commands in the table", ErrImplausible, rep.Commands)
	}

	b := &treeBuilder{zh: zh, en: en, chosen: chooseLeaves(roots), rep: rep}
	for _, a := range splitAreas(roots, rep) {
		if children := b.build(a.roots); len(children) > 0 {
			t.Tree = append(t.Tree, cspnames.TableNode{ID: a.id, ZH: a.zh, EN: a.en, Children: children})
		}
	}
	if len(t.Tree) == 0 {
		return nil, rep, fmt.Errorf("%w: the dialog tree is empty", ErrImplausible)
	}
	if err := applyOverrides(t); err != nil {
		return nil, rep, err
	}
	rep.Commands = len(t.Commands)
	rep.Groups, rep.Leaves = countTree(t.Tree)
	return t, rep, nil
}

// maxDepth is the deepest level of the dialog tree the walk accepts.
const maxDepth = 8

// chooseLeaves decides, for a command the tree places more than once, which
// placement stays: CSP builds the tree for several platforms and modes, so
// the same command sits in the phone's "App Settings" and in the File menu.
// The last placement inside the menu bar (the groups before menuBarEnd) wins;
// a command that sits in no menu keeps its first placement. The result maps a
// command id to the node (by pointer) that stays.
func chooseLeaves(roots []*treeNode) map[string]*treeNode {
	end := len(roots)
	for i, r := range roots {
		if r.id == menuBarEnd {
			end = i
			break
		}
	}
	chosen := map[string]*treeNode{}
	inBar := map[string]bool{}
	var walk func(nodes []*treeNode, bar bool)
	walk = func(nodes []*treeNode, bar bool) {
		for _, n := range nodes {
			if n.group {
				walk(n.children, bar)
				continue
			}
			switch {
			case bar:
				chosen[n.id], inBar[n.id] = n, true
			case !inBar[n.id]:
				if _, seen := chosen[n.id]; !seen {
					chosen[n.id] = n
				}
			}
		}
	}
	for i, r := range roots {
		if r.group {
			walk(r.children, i < end)
		}
	}
	return chosen
}

// treeBuilder turns the walked nodes into table nodes.
type treeBuilder struct {
	zh, en *cspnames.Resource
	chosen map[string]*treeNode
	rep    *Report
}

// build converts siblings. A group without a plausible name in both
// languages is dissolved: its children take its place (the report counts it).
// Groups left without children are dropped.
func (b *treeBuilder) build(nodes []*treeNode) []cspnames.TableNode {
	var out []cspnames.TableNode
	for _, n := range nodes {
		if !n.group {
			if b.chosen[n.id] == n {
				out = append(out, cspnames.TableNode{ID: n.id})
			}
			continue
		}
		children := b.build(n.children)
		zn, ok1 := b.name(b.zh, n)
		en, ok2 := b.name(b.en, n)
		if !ok1 || !ok2 {
			b.rep.DissolvedGroups = append(b.rep.DissolvedGroups, n.id)
			out = append(out, children...)
			continue
		}
		if len(children) == 0 {
			continue
		}
		out = append(out, cspnames.TableNode{ID: n.id, ZH: zn, EN: en, Children: children})
	}
	return out
}

func (b *treeBuilder) name(r *cspnames.Resource, n *treeNode) (string, bool) {
	if !n.hasRef {
		return "", false
	}
	return plausible(r, n.strref)
}

// area is one entry of the dialog's "setting area" drop-down, with the
// top-level groups of the executable's tree that belong to it.
type area struct {
	id, zh, en string
	roots      []*treeNode
}

// splitAreas sorts the top-level groups of the tree into the areas of the
// dialog's drop-down (CSP's own names: Main menu, Pop-up palettes, Options,
// Pen and Touch; Tools and Auto actions have no group in the tree, the first
// is the user's tool tree and the second the user's own commands). The menu
// bar is the main menu; the groups for the phone's layout are left out; the
// groups of the Windows pop-up palettes are the pop-up palettes; the touch
// groups are Pen and Touch; every other group is an option.
func splitAreas(roots []*treeNode, rep *Report) []area {
	main := area{id: "mainmenu", zh: "主菜单", en: "Main menu"}
	popup := area{id: "popuppalettes", zh: "弹出式面板", en: "Pop-up palettes"}
	options := area{id: "options", zh: "选项", en: "Options"}
	touch := area{id: "pentouch", zh: "笔和触摸", en: "Pen and Touch"}
	end := len(roots)
	for i, r := range roots {
		if r.id == menuBarEnd {
			end = i
			break
		}
	}
	for i, r := range roots {
		switch {
		case !r.group:
		case i < end:
			main.roots = append(main.roots, r)
		case strings.HasPrefix(r.id, "phone"):
			rep.PhoneGroups++
		case strings.HasPrefix(r.id, "windowbubble"):
			popup.roots = append(popup.roots, r)
		case strings.HasPrefix(r.id, "gesture") || r.id == "editparent":
			touch.roots = append(touch.roots, r)
		default:
			options.roots = append(options.roots, r)
		}
	}
	return []area{main, popup, options, touch}
}

// countTree counts the groups and the commands of a tree.
func countTree(nodes []cspnames.TableNode) (groups, leaves int) {
	for _, n := range nodes {
		if !n.IsGroup() {
			leaves++
			continue
		}
		groups++
		g, l := countTree(n.Children)
		groups, leaves = groups+g, leaves+l
	}
	return groups, leaves
}

// names resolves the candidates in one language: the first is the likeliest
// name, the rest are alternatives.
func names(r *cspnames.Resource, cands []uint32) (name string, alt []string) {
	seen := map[string]bool{}
	var all []string
	for _, v := range cands {
		s, ok := plausible(r, v)
		if !ok || seen[s] {
			continue
		}
		seen[s] = true
		all = append(all, s)
	}
	if len(all) == 0 {
		return "", nil
	}
	return all[0], all[1:]
}

// plausible resolves a name and rejects the values that are not text: CSP's
// tool property groups yield "0" and format templates such as "^0%1d)" (the caret marks them) when
// their reference is not a plain string.
func plausible(r *cspnames.Resource, ref uint32) (string, bool) {
	s, ok := r.StringRef(ref)
	if !ok || s == "" || s == "0" || strings.Contains(s, "^") {
		return "", false
	}
	return s, true
}

// Sorted returns the ids of the table in order, for reports.
func Sorted(t *cspnames.Table) []string {
	ids := make([]string, 0, len(t.Commands))
	for id := range t.Commands {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
