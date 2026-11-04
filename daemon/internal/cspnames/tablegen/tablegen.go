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
// command table. The code regions are virtual-address ranges of one CSP
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
	Stubs, MainStubs   int // static initialisers, and those of the main resource
	Entries            int // registrations in regions A and B
	WithText           int // of those, with a string reference that resolves
	IDs                int // distinct command ids with a name
	MenuCovered        int // of those, named by the menu resource
	Commands           int // commands in the table
	Categories         int
	Ambiguous          int // commands with more than one candidate name
	RegionAEntries     int
	GroupsWithoutNames int
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

// group is a node of the dialog tree on the stack while walking region A.
type group struct {
	id     string
	strref uint32
	ok     bool
}

// info is what the walk learns about one command id.
type info struct {
	cands  []uint32
	groups []group // ancestors in the dialog tree at its first appearance
	inA    bool
}

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
	var tops []group // top-level groups of the dialog, in tree order
	var stack []group
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
		in := infos[key]
		if len(cands) > 0 {
			rep.WithText++
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
		if r8 == nil || *r8 > 8 {
			continue
		}
		stack = stack[:min(len(stack), int(*r8))]
		isGroup := edx != nil && *edx == 0
		if in != nil && !in.inA {
			in.inA = true
			in.groups = append([]group(nil), stack...)
		}
		if isGroup {
			g := group{id: strings.ToLower(e.id)}
			if len(cands) > 0 {
				g.strref, g.ok = cands[0], true
			}
			stack = append(stack, g)
			if *r8 == 0 {
				tops = append(tops, g)
			}
		}
	}
	rep.IDs = len(infos)
	if rep.Stubs < minStubs || rep.MainStubs < minMainStubs || rep.Entries < minEntries ||
		rep.WithText < minWithText || rep.IDs < minIDs || rep.RegionAEntries < minRegionA {
		return nil, rep, fmt.Errorf("%w: %+v", ErrImplausible, *rep)
	}

	menu := map[string]bool{}
	for _, c := range zh.Menu {
		menu[strings.ToLower(c.ID)] = true
	}
	for _, c := range en.Menu {
		menu[strings.ToLower(c.ID)] = true
	}

	t := &cspnames.Table{CSP: opts.Version, Commands: map[string]cspnames.TableCommand{}}
	catIndex := map[string]int{}
	addCategory := func(g group) (string, bool) {
		if !g.ok {
			return "", false
		}
		zn, ok1 := plausible(zh, g.strref)
		en2, ok2 := plausible(en, g.strref)
		if !ok1 || !ok2 {
			return "", false
		}
		id := slug(g.id)
		if _, dup := catIndex[id]; !dup {
			catIndex[id] = len(t.Categories)
			t.Categories = append(t.Categories, cspnames.TableCategory{ID: id, ZH: zn, EN: en2})
		}
		return id, true
	}
	// The groups of the dialog first, in the order of the tree: their ids
	// are the stable category ids the menu categories borrow.
	for _, g := range tops {
		addCategory(g)
	}
	other := false
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
		cmd.Category = "other"
		if in.inA && len(in.groups) > 0 {
			if id, ok := addCategory(in.groups[0]); ok {
				cmd.Category = id
				cmd.PathZH, cmd.PathEN = path(zh, en, in.groups)
			} else {
				rep.GroupsWithoutNames++
			}
		}
		if cmd.Category == "other" {
			other = true
		}
		t.Commands[key] = cmd
	}
	if other {
		t.Categories = append(t.Categories, cspnames.TableCategory{ID: "other", ZH: "其他", EN: "Other"})
	}
	rep.Commands = len(t.Commands)
	rep.Categories = len(t.Categories)
	if rep.Commands < minCommands {
		return nil, rep, fmt.Errorf("%w: %d commands in the table", ErrImplausible, rep.Commands)
	}
	return t, rep, nil
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

// path is the group path of a command in both languages: the top-level
// group and, when it has a plain name, the group below it. Deeper groups are
// not reliable.
func path(zh, en *cspnames.Resource, groups []group) (pz, pe []string) {
	for i, g := range groups {
		if i >= 2 || !g.ok {
			break
		}
		a, ok1 := plausible(zh, g.strref)
		b, ok2 := plausible(en, g.strref)
		if !ok1 || !ok2 {
			break
		}
		pz, pe = append(pz, a), append(pe, b)
	}
	return pz, pe
}

// slug turns the id of a group node ("menueditparent", "canvasparent") into
// a category id ("edit", "canvas").
func slug(id string) string {
	s := strings.TrimSuffix(id, "parent")
	if t := strings.TrimPrefix(s, "menu"); t != "" {
		s = t
	}
	if s == "" {
		return id
	}
	return s
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
