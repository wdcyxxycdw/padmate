package cspnames

import (
	"bytes"
	"errors"
	"fmt"
	"unicode/utf8"
)

// MainResourceName is the file name of the main resource under
// resource\<language>\ of the CSP install folder.
const MainResourceName = "742DEA58-ED6B-4402-BC11-20DFC6D08040"

// ErrUnknownFormat marks a resource whose structure is not the one this
// package understands. Errors wrap it with the detail.
var ErrUnknownFormat = errors.New("cspnames: unknown resource format")

func unknown(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnknownFormat, fmt.Sprintf(format, args...))
}

// MenuCommand is one command found in a menu of the main resource.
type MenuCommand struct {
	// ID is the command id as the shortcut file spells it (menucommand).
	ID string
	// Name is the label of the menu item.
	Name string
	// Path is the menu path from the top-level menu down to, but not
	// including, the item. Empty when the top-level menu has no name.
	Path []string
	// Menu is the container id of the top-level menu the item is in.
	Menu uint32
}

// MenuTop is a top-level menu.
type MenuTop struct {
	// ID is the id of the menu in the container, the same in every language.
	ID uint32
	// Name is its localized title; empty when the resource has none.
	Name string
}

// Resource is a parsed main resource.
type Resource struct {
	// Commands maps command number n to Commands[n-1], its id.
	Commands []string
	// Menu lists the commands of all menus in resource order. A command may
	// appear in several menus; the first one is the canonical.
	Menu []MenuCommand
	// Tops lists the top-level menus in resource order.
	Tops []MenuTop

	data      []byte
	blocks    []dirEntry
	mainBlock uint32
	tables    map[uint32]map[uint32]string
	loaded    bool
}

var (
	itemHeader    = []byte{0, 0, 0, 0x10, 0, 0, 0, 0x1e} // object header of a PWMenuItem
	menuHeader    = []byte{0, 0, 0, 0x10, 0, 0, 0, 0x1a} // object header of a PWMenu
	menuItemName  = []byte("PWMenuItem")
	shortcutClass = []byte("PWCommandShortcut")
	menuClass     = []byte("PWMenu")
	// commandStart is what follows the size of the first command record.
	commandStart = []byte{0, 0, 0, 0x10, 0, 0, 0, 0x14, 0, 0, 0, 0x4a}
)

// ParseResource parses the main resource (the file named MainResourceName).
// It never panics; a structure it does not understand yields an error that
// wraps ErrUnknownFormat.
func ParseResource(data []byte) (res *Resource, err error) {
	defer func() {
		if r := recover(); r != nil {
			res, err = nil, unknown("internal error: %v", r)
		}
	}()
	if len(data) > maxResourceSize {
		return nil, unknown("resource is %d bytes", len(data))
	}
	secs, ok := readSections(data)
	if !ok {
		return nil, unknown("no section table")
	}
	r := &Resource{data: data}
	r.blocks = readDir(data, int(secs[0].off))
	if r.blocks == nil {
		return nil, unknown("section 1 is not a directory")
	}
	r.Commands, err = readCommands(data[secs[1].off : secs[1].off+secs[1].size])
	if err != nil {
		return nil, err
	}
	var main dirEntry
	found := false
	for _, b := range r.blocks {
		if bytes.Contains(b.content(data), shortcutClass) {
			main, found = b, true
			r.mainBlock = b.id
			break
		}
	}
	if !found {
		return nil, unknown("main block not found")
	}
	strs, menus := findMainDirs(data, main)
	if strs == nil || menus == nil {
		return nil, unknown("string or menu directory not found")
	}
	r.Menu, r.Tops = parseMenus(data, menus, strs, r.Commands)
	if len(r.Menu) == 0 {
		return nil, unknown("no menu commands")
	}
	r.tables = map[uint32]map[uint32]string{r.mainBlock: strs}
	return r, nil
}

// readCommands reads section 2: an array of records, each starting with its
// own u32 size, holding a GUID and the command id as strings. Entry n-1 is
// the command with number n.
func readCommands(s []byte) ([]string, error) {
	pos := findCommandStart(s)
	if pos < 0 {
		return nil, unknown("command records not found")
	}
	var out []string
	for pos+4 <= len(s) {
		size, _ := be32(s, pos)
		if size < 4 {
			return nil, unknown("bad command record size %d", size)
		}
		end := pos + int(size)
		if end > len(s) || end < pos {
			end = len(s)
		}
		out = append(out, commandID(s[pos:end]))
		if len(out) > maxDirEntries {
			return nil, unknown("too many command records")
		}
		pos = end
	}
	if len(out) == 0 {
		return nil, unknown("no command records")
	}
	return out, nil
}

// findCommandStart finds the first record: 00 00 00 ?? 00 00 00 10 00 00 00
// 14 00 00 00 4a.
func findCommandStart(s []byte) int {
	for i := 0; i+4+len(commandStart) <= len(s); i++ {
		if s[i] != 0 || s[i+1] != 0 || s[i+2] != 0 {
			continue
		}
		if bytes.Equal(s[i+4:i+4+len(commandStart)], commandStart) {
			return i
		}
	}
	return -1
}

// commandID extracts the command id from a record: the string after
// 00 00 00 24 <36 character GUID> 00 00 00 <length>.
func commandID(rec []byte) string {
	const guid = 36
	for i := 0; i+4+guid+4 <= len(rec); i++ {
		if rec[i] != 0 || rec[i+1] != 0 || rec[i+2] != 0 || rec[i+3] != 0x24 {
			continue
		}
		if !isGUIDText(rec[i+4 : i+4+guid]) {
			continue
		}
		q := i + 4 + guid
		if rec[q] != 0 || rec[q+1] != 0 || rec[q+2] != 0 {
			continue
		}
		n := int(rec[q+3])
		if q+4+n > len(rec) {
			return ""
		}
		return string(rec[q+4 : q+4+n])
	}
	return ""
}

// findMainDirs finds, inside the main block, the string directory (id to
// length-prefixed UTF-8, over a thousand entries) and the menu directory
// (id to a blob that starts with a PWMenu object).
func findMainDirs(d []byte, main dirEntry) (map[uint32]string, []dirEntry) {
	var sdir, mdir []dirEntry
	end := int(main.off) + int(main.size)
	for p := int(main.off); p < end-16; p++ {
		e := readDir(d, p)
		if e == nil {
			continue
		}
		if sdir == nil && len(e) > 1000 {
			sdir = e
		}
		if mdir == nil && len(e) > 5 {
			head := e[0].content(d)
			if len(head) > 60 {
				head = head[:60]
			}
			if bytes.Contains(head, menuClass) {
				mdir = e
			}
		}
		if sdir != nil && mdir != nil {
			break
		}
	}
	if sdir == nil || mdir == nil {
		return nil, nil
	}
	strs := make(map[uint32]string, len(sdir))
	for _, e := range sdir {
		if s, ok := lengthPrefixed(e.content(d)); ok {
			strs[e.id] = s
		}
	}
	return strs, mdir
}

// parseMenus walks every PWMenuItem of every menu. A menu item serializes as
// a field count (5 or 6 for a command, 9 or 10 for a submenu), a run of
// 00 0d hi lo string references whose last one is the label, and then the
// command number (commands) or the byte size of the embedded PWMenu
// (submenus).
func parseMenus(d []byte, mdir []dirEntry, strs map[uint32]string, cmds []string) ([]MenuCommand, []MenuTop) {
	type frame struct {
		end   int
		label string
	}
	var out []MenuCommand
	tops := make([]MenuTop, 0, len(mdir))
	for _, m := range mdir {
		blob := m.content(d)
		top := strs[m.id+1]
		tops = append(tops, MenuTop{ID: m.id, Name: top})
		stack := []frame{{end: len(blob), label: top}}
		noMenuFrom := len(blob) + 1 // searches for a PWMenu header from here on find nothing
		for pos := indexFrom(blob, menuItemName, 0); pos >= 0; pos = indexFrom(blob, menuItemName, pos+len(menuItemName)) {
			mEnd := pos + len(menuItemName)
			for len(stack) > 1 && pos >= stack[len(stack)-1].end {
				stack = stack[:len(stack)-1]
			}
			e := indexFrom(blob, itemHeader, mEnd)
			if e < 0 {
				e = len(blob)
			}
			seg := blob[mEnd:e]
			nf, ok := be32(seg, 0)
			if !ok {
				continue
			}
			last, lastEnd, runLen := lastRefRun(seg)
			if runLen == 0 {
				continue
			}
			ref, _ := be16(seg, last+(runLen-1)*4+2)
			label := strs[uint32(ref)]
			tail := seg[lastEnd:]
			switch {
			case nf == 10:
				off := 2
				if runLen == 1 {
					off = 0
				}
				size, ok := be32(tail, off)
				if !ok || len(stack) >= maxMenuDepth {
					continue
				}
				q := -1
				if mEnd < noMenuFrom {
					q = indexFrom(blob, menuHeader, mEnd)
					if q < 0 {
						noMenuFrom = mEnd
					}
				}
				if q >= 0 {
					end := q + int(size)
					if size > uint32(len(blob)) || end < q {
						end = len(blob)
					}
					stack = append(stack, frame{end: end, label: label})
				}
			case (nf == 5 || nf == 6) && len(tail) >= 6:
				var no uint32
				if tail[0] == 0 && tail[1] == 0 && (tail[2] != 0 || tail[3] != 0) {
					no, _ = be32(tail, 0)
				} else {
					no, _ = be32(tail, 2)
				}
				if no < 1 || int(no) > len(cmds) || cmds[no-1] == "" || label == "" {
					continue
				}
				var path []string
				for _, f := range stack {
					if f.label != "" {
						path = append(path, f.label)
					}
				}
				out = append(out, MenuCommand{ID: cmds[no-1], Name: label, Path: path, Menu: m.id})
			}
		}
	}
	return out, tops
}

// lastRefRun scans b for runs of 00 0d hi lo (a string reference), the way a
// leftmost, non-overlapping match of (?:\x00\x0d..)+ would, and returns the
// start, the end and the number of references of the last run.
func lastRefRun(b []byte) (start, end, n int) {
	for i := 0; i+4 <= len(b); {
		if b[i] != 0 || b[i+1] != 0x0d {
			i++
			continue
		}
		j := i
		for j+4 <= len(b) && b[j] == 0 && b[j+1] == 0x0d {
			j += 4
		}
		start, end, n = i, j, (j-i)/4
		i = j
	}
	return start, end, n
}

// StringRef returns the string a CSP string reference points to: the block
// id is the high 16 bits and the string id in the block the low 16 bits.
// Strings are found the same way in the resource of every language.
func (r *Resource) StringRef(ref uint32) (string, bool) {
	if !r.loaded {
		r.loadTables()
	}
	s, ok := r.tables[ref>>16][ref&0xFFFF]
	return s, ok && ref>>16 > 0
}

// loadTables reads every string table of every block: a block is a directory
// of directories whose entries are length-prefixed strings.
func (r *Resource) loadTables() {
	main := r.tables[r.mainBlock]
	t := map[uint32]map[uint32]string{}
	for _, b := range r.blocks {
		subs := readDir(r.data, int(b.off))
		if subs == nil {
			continue
		}
		for _, sub := range subs {
			ents := readDir(r.data, int(sub.off))
			if ents == nil {
				continue
			}
			for _, e := range ents {
				c := e.content(r.data)
				n, ok := be32(c, 0)
				if !ok || uint64(n)+4 != uint64(e.size) {
					continue
				}
				if !utf8.Valid(c[4:]) {
					continue
				}
				if t[b.id] == nil {
					t[b.id] = map[uint32]string{}
				}
				t[b.id][e.id] = string(c[4:])
			}
		}
	}
	t[r.mainBlock] = main // the big table of the main block comes from its string directory
	r.tables = t
	r.loaded = true
}

// isGUIDText reports whether g consists of hexadecimal digits and dashes, the
// characters of a GUID.
func isGUIDText(g []byte) bool {
	for _, c := range g {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F', c == '-':
		default:
			return false
		}
	}
	return true
}
