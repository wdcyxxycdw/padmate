package cspint

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/csp"
	"github.com/wdcyxxycdw/padmate/daemon/internal/cspfile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/integration"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// ErrRunning, ErrBusy and ErrNotFound are the errors of the generic
// integration, re-exported for callers of this package.
var (
	ErrRunning  = integration.ErrRunning
	ErrBusy     = integration.ErrBusy
	ErrNotFound = integration.ErrNotFound
)

// shortcut is one command shortcut: the CSP key name (upper case) and the
// modifier bitmask.
type shortcut struct {
	key string
	mod int
}

func (s shortcut) String() string { return fmt.Sprintf("%s/%d", s.key, s.mod) }

// model is the part of CSP's settings that a write changes: the convertible
// shortcuts of every command and the key code of every tool node. Shortcuts
// that do not convert (symbol keys, device buttons) are never touched and are
// not in it.
type model struct {
	cmds  map[string][]shortcut
	tools map[string]int
}

func modelOf(cmds *csp.CommandSet, tools *csp.ToolTree) model {
	m := model{cmds: map[string][]shortcut{}, tools: map[string]int{}}
	if cmds != nil {
		for _, c := range cmds.Commands {
			list := []shortcut{}
			for _, sc := range c.Shortcuts {
				if sc.Status == csp.KeyOK {
					list = append(list, shortcut{strings.ToUpper(sc.Key), sc.Modifier})
				}
			}
			m.cmds[c.ID] = list
		}
	}
	if tools != nil {
		for _, n := range tools.Nodes {
			m.tools[n.UUID] = n.KeyCode
		}
	}
	return m
}

func (m model) clone() model {
	c := model{cmds: make(map[string][]shortcut, len(m.cmds)), tools: maps.Clone(m.tools)}
	for id, l := range m.cmds {
		c.cmds[id] = slices.Clone(l)
	}
	return c
}

// equal reports whether two models hold the same shortcuts, ignoring order.
func (m model) equal(o model) bool {
	if !maps.Equal(m.tools, o.tools) || len(m.cmds) != len(o.cmds) {
		return false
	}
	for id, l := range m.cmds {
		ol, ok := o.cmds[id]
		if !ok || !sameShortcuts(l, ol) {
			return false
		}
	}
	return true
}

func sameShortcuts(a, b []shortcut) bool {
	if len(a) != len(b) {
		return false
	}
	as, bs := shortcutStrings(a), shortcutStrings(b)
	slices.Sort(as)
	slices.Sort(bs)
	return slices.Equal(as, bs)
}

func shortcutStrings(l []shortcut) []string {
	out := make([]string, len(l))
	for i, s := range l {
		out[i] = s.String()
	}
	return out
}

// edits are the file edits a set of changes comes to, and the model the
// files must have afterwards.
type edits struct {
	khc   []csp.CommandEdit
	todb  []csp.ToolEdit
	final model
	// adds and removes count the khc edits of each kind.
	adds, removes int
}

func refuse(function, spec, reason string) error {
	return &integration.ChangeError{Function: function, Keys: spec, Reason: reason}
}

// planEdits validates changes together against the snapshot, as one set,
// and turns them into file edits. The order of the changes does not matter:
// the final state is checked, and the edits remove before they add.
func (s *snapshot) planEdits(changes []integration.Change) (*edits, error) {
	start := modelOf(s.cmds, s.tools)
	cur := start.clone()
	touched := map[shortcut]bool{}  // shortcuts that must not clash afterwards
	touchedCmd := map[string]bool{} // commands changed
	touchedTool := map[string]bool{}

	for _, ch := range changes {
		kind, native, ok := parseFunction(ch.Function)
		if !ok {
			return nil, refuse(ch.Function, "", integration.ReasonUnknownFunction)
		}
		if ch.Add == "" && ch.Remove == "" {
			continue
		}
		switch kind {
		case csp.KindCommand:
			if _, known := cur.cmds[native]; !known {
				return nil, refuse(ch.Function, "", integration.ReasonUnknownFunction)
			}
			touchedCmd[native] = true
			if ch.Remove != "" {
				sc, err := commandKey(ch.Function, ch.Remove)
				if err != nil {
					return nil, err
				}
				i := slices.Index(cur.cmds[native], sc)
				if i < 0 {
					return nil, refuse(ch.Function, ch.Remove, integration.ReasonNoSuchKey)
				}
				cur.cmds[native] = slices.Delete(cur.cmds[native], i, i+1)
			}
			if ch.Add != "" {
				sc, err := commandKey(ch.Function, ch.Add)
				if err != nil {
					return nil, err
				}
				touched[sc] = true
				if !slices.Contains(cur.cmds[native], sc) {
					cur.cmds[native] = append(cur.cmds[native], sc)
				}
			}
		case csp.KindTool:
			if _, known := cur.tools[native]; !known {
				return nil, refuse(ch.Function, "", integration.ReasonUnknownFunction)
			}
			touchedTool[native] = true
			if ch.Remove != "" {
				code, err := toolKey(ch.Function, ch.Remove)
				if err != nil {
					return nil, err
				}
				if cur.tools[native] != code {
					return nil, refuse(ch.Function, ch.Remove, integration.ReasonNoSuchKey)
				}
				cur.tools[native] = 0
			}
			if ch.Add != "" {
				code, err := toolKey(ch.Function, ch.Add)
				if err != nil {
					return nil, err
				}
				cur.tools[native] = code
				touched[shortcut{string(rune('A' + code - 1)), 0}] = true
			}
		}
	}

	// No two commands on a touched shortcut, and no command on the single key
	// of a tool. Tools sharing a key are CSP's cycle shortcut.
	for sc := range touched {
		var holders []string
		for _, id := range slices.Sorted(maps.Keys(cur.cmds)) {
			if slices.Contains(cur.cmds[id], sc) {
				holders = append(holders, commandFunctionID(id))
			}
		}
		spec := ""
		if c, st := csp.ParseCSPKey(sc.key, sc.mod); st == csp.KeyOK {
			spec = c.String()
		}
		if len(holders) > 1 {
			return nil, refuse(holders[1], spec, integration.ReasonConflict)
		}
		if len(holders) == 1 && sc.mod == 0 && len(sc.key) == 1 && sc.key[0] >= 'A' && sc.key[0] <= 'Z' {
			code := int(sc.key[0]-'A') + 1
			for _, uuid := range slices.Sorted(maps.Keys(cur.tools)) {
				if cur.tools[uuid] == code {
					return nil, refuse(toolFunctionID(uuid), spec, integration.ReasonConflict)
				}
			}
		}
	}

	e := &edits{final: cur}
	var adds []csp.CommandEdit
	for _, id := range slices.Sorted(maps.Keys(touchedCmd)) {
		for _, sc := range start.cmds[id] {
			if !slices.Contains(cur.cmds[id], sc) {
				e.khc = append(e.khc, csp.RemoveShortcut(id, sc.key, sc.mod))
				e.removes++
			}
		}
		for _, sc := range cur.cmds[id] {
			if !slices.Contains(start.cmds[id], sc) {
				adds = append(adds, csp.AddShortcut(id, sc.key, sc.mod))
				e.adds++
			}
		}
	}
	e.khc = append(e.khc, adds...)
	for _, uuid := range slices.Sorted(maps.Keys(touchedTool)) {
		switch cur.tools[uuid] {
		case start.tools[uuid]:
		case 0:
			e.todb = append(e.todb, csp.ClearToolKey(uuid))
		default:
			e.todb = append(e.todb, csp.SetToolKey(uuid, cur.tools[uuid]))
		}
	}
	return e, nil
}

func commandKey(function, spec string) (shortcut, error) {
	chord, err := keys.Parse(spec)
	if err != nil {
		return shortcut{}, refuse(function, spec, integration.ReasonBadKeys)
	}
	name, mod, st := csp.FormatCSPKey(chord)
	if st != csp.KeyOK {
		return shortcut{}, refuse(function, spec, integration.ReasonUnsupportedKey)
	}
	return shortcut{strings.ToUpper(name), mod}, nil
}

func toolKey(function, spec string) (int, error) {
	chord, err := keys.Parse(spec)
	if err != nil {
		return 0, refuse(function, spec, integration.ReasonBadKeys)
	}
	code, ok := csp.ToolKeyCode(chord)
	if !ok {
		return 0, refuse(function, spec, integration.ReasonUnsupportedKey)
	}
	return code, nil
}

// Write changes CSP's shortcut settings.
func (in *Integration) Write(changes []integration.Change) (integration.WriteResult, error) {
	in.writeMu.Lock()
	defer in.writeMu.Unlock()
	if err := in.recheck(); err != nil {
		return integration.WriteResult{}, err
	}
	s, err := in.fresh()
	if err != nil {
		return integration.WriteResult{}, err
	}
	ed, err := s.planEdits(changes)
	if err != nil {
		return integration.WriteResult{}, err
	}
	if len(ed.khc) == 0 && len(ed.todb) == 0 {
		return integration.WriteResult{Revision: s.revision}, nil
	}
	if len(ed.khc) > 0 && !s.files.hasKHC {
		return integration.WriteResult{}, unavailable(integration.ProblemNotFound, errors.New("default.khc does not exist"))
	}
	if len(ed.todb) > 0 && !s.files.hasTODB {
		return integration.WriteResult{}, unavailable(integration.ProblemNotFound, errors.New("EditImageTool.todb does not exist"))
	}

	var paths []string
	if s.files.hasKHC {
		paths = append(paths, s.files.khc)
	}
	if s.files.hasTODB {
		paths = append(paths, s.files.todb)
	}
	note, err := json.Marshal(struct {
		Reason  string               `json:"reason"`
		Changes []integration.Change `json:"changes"`
	}{"write", changes})
	if err != nil {
		return integration.WriteResult{}, err
	}
	bk, err := in.backups.Create(paths, note)
	if err != nil {
		return integration.WriteResult{}, fmt.Errorf("backing up CLIP STUDIO PAINT's files: %w", err)
	}

	var updates []cspfile.FileUpdate
	if len(ed.khc) > 0 {
		khc := ed.khc
		updates = append(updates, cspfile.FileUpdate{
			Target: s.files.khc,
			Edit:   func(tmp string) error { return csp.ApplyCommandEdits(tmp, khc) },
		})
	}
	if len(ed.todb) > 0 {
		todb := ed.todb
		updates = append(updates, cspfile.FileUpdate{
			Target: s.files.todb,
			Edit:   func(tmp string) error { return csp.ApplyToolEdits(tmp, todb) },
		})
	}
	err = cspfile.UpdateMany(updates, cspfile.Options{
		Recheck: in.recheck,
		Verify:  func(target string) error { return verify(s, ed, target) },
	})
	in.invalidate()
	if err != nil {
		return integration.WriteResult{}, mapFileError(err)
	}
	rev, err := in.Revision()
	if err != nil {
		return integration.WriteResult{}, err
	}
	return integration.WriteResult{Backup: backupInfo(bk), Revision: rev}, nil
}

// verify reads target back (from a copy) and checks that it holds the model
// the edits were meant to produce: the edited shortcuts are there, nothing
// else changed, and the row and node counts are plausible.
func verify(before *snapshot, ed *edits, target string) error {
	return withCopy(target, func(path string) error {
		switch target {
		case before.files.khc:
			set, err := csp.ReadKHC(path)
			if err != nil {
				return err
			}
			if !modelOf(set, nil).equal(model{cmds: ed.final.cmds, tools: map[string]int{}}) {
				return errors.New("the command shortcuts read back differ from the ones written")
			}
			old, now := len(before.cmds.Rows), len(set.Rows)
			if now < old-ed.removes || now > old+ed.adds || len(set.Commands) != len(before.cmds.Commands) {
				return fmt.Errorf("the khc has %d rows and %d commands, was %d and %d", now, len(set.Commands), old, len(before.cmds.Commands))
			}
		case before.files.todb:
			tree, err := csp.ReadTODB(path)
			if err != nil {
				return err
			}
			if !modelOf(nil, tree).equal(model{cmds: map[string][]shortcut{}, tools: ed.final.tools}) {
				return errors.New("the tool keys read back differ from the ones written")
			}
			if len(tree.Nodes) != len(before.tools.Nodes) {
				return fmt.Errorf("the todb has %d nodes, was %d", len(tree.Nodes), len(before.tools.Nodes))
			}
		}
		return nil
	})
}

// mapFileError turns the errors of cspfile into the generic ones.
func mapFileError(err error) error {
	if errors.Is(err, cspfile.ErrJournal) || errors.Is(err, cspfile.ErrChanged) {
		return fmt.Errorf("%w: %w", integration.ErrBusy, err)
	}
	return err
}

func backupInfo(b cspfile.Backup) integration.BackupInfo {
	info := integration.BackupInfo{ID: b.ID, Created: b.Manifest.Created}
	var note struct {
		Reason  string            `json:"reason"`
		Changes []json.RawMessage `json:"changes"`
	}
	if len(b.Manifest.Note) > 0 && json.Unmarshal(b.Manifest.Note, &note) == nil {
		info.Reason = note.Reason
		info.Changes = len(note.Changes)
	}
	return info
}

// Backups lists the backups, newest first.
func (in *Integration) Backups() ([]integration.BackupInfo, error) {
	list, err := in.backups.List()
	if err != nil {
		return nil, err
	}
	out := make([]integration.BackupInfo, 0, len(list))
	for _, b := range list {
		out = append(out, backupInfo(b))
	}
	return out, nil
}

// Restore puts backup id back. CSP must not run. The current files are backed
// up first; that backup is returned.
func (in *Integration) Restore(id string) (integration.WriteResult, error) {
	in.writeMu.Lock()
	defer in.writeMu.Unlock()
	b, err := in.backups.Get(id)
	if err != nil {
		return integration.WriteResult{}, fmt.Errorf("%w: %w", integration.ErrNotFound, err)
	}
	pre, err := in.backups.Restore(b, cspfile.RestoreOptions{Recheck: in.recheck})
	in.invalidate()
	if err != nil {
		return integration.WriteResult{}, mapFileError(err)
	}
	rev, err := in.Revision()
	if err != nil {
		return integration.WriteResult{}, err
	}
	res := integration.WriteResult{Revision: rev}
	if pre.ID != "" {
		res.Backup = backupInfo(pre)
	}
	return res, nil
}
