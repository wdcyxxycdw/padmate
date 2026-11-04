package cspint

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/csp"
	"github.com/wdcyxxycdw/padmate/daemon/internal/csp/csptest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/cspfile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/integration"
)

// namer knows two commands.
type namer struct{}

func (namer) CommandName(id, lang string) (string, []string, string, bool) {
	switch id {
	case "basiccommand/undo":
		return "Undo", []string{"Edit"}, "edit", true
	case "basiccommand/filenew":
		return "New", []string{"File"}, "file", true
	}
	return "", nil, "", false
}

func (namer) Categories(lang string) []integration.Category {
	return []integration.Category{{ID: "file", Name: "File"}, {ID: "edit", Name: "Edit"}}
}

type fixture struct {
	in      *Integration
	dir     string // the CSP data folder
	padmate string
	running bool
	khc     string
	todb    string
}

func tool(n byte) string { return "tool:" + csptest.UIDHex(n) }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{dir: t.TempDir(), padmate: t.TempDir()}
	f.khc = csptest.MakeKHC(t, f.dir, csptest.SampleKHC())
	f.todb = csptest.MakeTODB(t, f.dir, csptest.SampleTools())
	csptest.MakeAppConfig(t, f.dir, 1)
	f.in = New(Options{
		PadmateDir:     f.padmate,
		DataDir:        func() string { return f.dir },
		Namer:          namer{},
		Running:        func() (bool, error) { return f.running, nil },
		FindInstallDir: func() (cspfile.InstallDir, error) { return cspfile.InstallDir{Root: `C:\CSP`}, nil },
	})
	return f
}

func (f *fixture) listing(t *testing.T) map[string]integration.Function {
	t.Helper()
	l, err := f.in.Functions("en")
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]integration.Function{}
	for _, fn := range l.Functions {
		m[fn.ID] = fn
	}
	return m
}

func TestIdentity(t *testing.T) {
	f := newFixture(t)
	if f.in.ID() != "csp" || f.in.Name() != "CLIP STUDIO PAINT" || !reflect.DeepEqual(f.in.Exes(), []string{"CLIPStudioPaint.exe"}) {
		t.Error("identity")
	}
	loc := f.in.Locate()
	if !loc.Found || loc.DataDir != f.dir || loc.InstallDir != `C:\CSP` {
		t.Errorf("Locate = %+v", loc)
	}
}

func TestLocateProblems(t *testing.T) {
	empty := t.TempDir()
	in := New(Options{PadmateDir: t.TempDir(), DataDir: func() string { return empty }})
	if loc := in.Locate(); loc.Found || loc.Problem != integration.ProblemNotFound {
		t.Errorf("empty folder: %+v", loc)
	}
	in = New(Options{PadmateDir: t.TempDir(), FindDataDirs: func() []cspfile.DataDir { return nil }})
	if loc := in.Locate(); loc.Found || loc.Problem != integration.ProblemNotFound {
		t.Errorf("none found: %+v", loc)
	}
	bad := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bad, "Shortcut"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "Shortcut", "default.khc"), []byte("this is not a database at all, not even close"), 0o644); err != nil {
		t.Fatal(err)
	}
	in = New(Options{PadmateDir: t.TempDir(), DataDir: func() string { return bad }})
	if loc := in.Locate(); loc.Found || loc.Problem != integration.ProblemUnknownFormat {
		t.Errorf("garbage: %+v", loc)
	}
	// A khc of another structure.
	other := t.TempDir()
	csptest.MakeAppConfig(t, other, 0)
	if err := os.MkdirAll(filepath.Join(other, "Shortcut"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(other, "Preference", "Config.sqlite"), filepath.Join(other, "Shortcut", "default.khc")); err != nil {
		t.Fatal(err)
	}
	in = New(Options{PadmateDir: t.TempDir(), DataDir: func() string { return other }})
	if loc := in.Locate(); loc.Found || loc.Problem != integration.ProblemUnknownFormat {
		t.Errorf("other structure: %+v", loc)
	}
	if _, err := in.Functions("en"); err == nil {
		t.Error("Functions of an unreadable folder")
	} else {
		var un *integration.UnavailableError
		if !errors.As(err, &un) || un.Problem != integration.ProblemUnknownFormat {
			t.Errorf("error = %v", err)
		}
	}
}

func TestFunctions(t *testing.T) {
	f := newFixture(t)
	l, err := f.in.Functions("en")
	if err != nil {
		t.Fatal(err)
	}
	if l.Revision == "" || len(l.Functions) != 6+9 {
		t.Fatalf("revision %q, %d functions", l.Revision, len(l.Functions))
	}
	m := f.listing(t)
	undo := m["command:basiccommand/undo"]
	if undo.Name != "Undo" || undo.Category != "edit" || !reflect.DeepEqual(undo.Path, []string{"Edit"}) ||
		!reflect.DeepEqual(undo.Keys, []string{"Ctrl+Z", "Ctrl+Alt+Z"}) || undo.Kind != "command" {
		t.Errorf("undo = %+v", undo)
	}
	swap := m["command:basiccommand/swapcolor"]
	if swap.Name != "basiccommand/swapcolor" || swap.Category != "other" || !reflect.DeepEqual(swap.Keys, []string{"X"}) {
		t.Errorf("swap = %+v", swap)
	}
	if merge := m["command:advancedcommand/layermergevisiblelayer"]; len(merge.Keys) != 0 || merge.Unsupported || merge.Unconfirmed {
		t.Errorf("merge = %+v", merge)
	}
	if !m["command:basiccommand/weird"].Unsupported || !m["command:basiccommand/symbols"].Unconfirmed {
		t.Error("flags of weird/symbols")
	}
	pen := m[tool(2)]
	if pen.Kind != "tool" || pen.Category != "tools" || pen.Name != "Pen" || !reflect.DeepEqual(pen.Keys, []string{"P"}) ||
		!reflect.DeepEqual(pen.Cycle, []string{tool(4), tool(5)}) {
		t.Errorf("pen = %+v", pen)
	}
	pencil := m[tool(4)]
	if !reflect.DeepEqual(pencil.Path, []string{"Pen", "Pencils", "Pencil"}) || !reflect.DeepEqual(pencil.Cycle, []string{tool(2), tool(5)}) {
		t.Errorf("pencil = %+v", pencil)
	}
	if eraser := m[tool(8)]; len(eraser.Cycle) != 0 || !reflect.DeepEqual(eraser.Keys, []string{"E"}) {
		t.Errorf("eraser = %+v", eraser)
	}
	if soft := m[tool(10)]; !soft.Unsupported || len(soft.Keys) != 0 {
		t.Errorf("soft = %+v", soft)
	}
	if g := m[tool(7)]; len(g.Keys) != 0 || g.Cycle != nil {
		t.Errorf("g-pen = %+v", g)
	}
	var cats []string
	for _, c := range l.Categories {
		cats = append(cats, c.ID)
	}
	if !reflect.DeepEqual(cats, []string{"file", "edit", "tools", "other"}) {
		t.Errorf("categories = %v", cats)
	}
	// The listing is stable while the files do not change.
	if r, err := f.in.Revision(); err != nil || r != l.Revision {
		t.Errorf("revision %q %v", r, err)
	}
}

func TestCheck(t *testing.T) {
	f := newFixture(t)
	check := func(function, keys string) integration.CheckResult {
		t.Helper()
		r, err := f.in.Check(function, keys, "en")
		if err != nil {
			t.Fatalf("Check(%q, %q): %v", function, keys, err)
		}
		return r
	}
	owner := func(r integration.CheckResult) []string {
		var ids []string
		for _, o := range r.Owners {
			ids = append(ids, o.ID)
		}
		return ids
	}
	if r := check("", "Ctrl+Shift+Q"); r.Status != "free" || len(r.Owners) != 0 || len(r.Notes) != 0 {
		t.Errorf("free = %+v", r)
	}
	if r := check("command:basiccommand/filenew", "control+n"); r.Status != "same" {
		t.Errorf("same = %+v", r)
	}
	r := check("command:basiccommand/undo", "Ctrl+N")
	if r.Status != "conflict" || !reflect.DeepEqual(owner(r), []string{"command:basiccommand/filenew"}) || r.Owners[0].Name != "New" {
		t.Errorf("conflict = %+v", r)
	}
	r = check("", "Ctrl+N")
	if r.Status != "conflict" {
		t.Errorf("no target = %+v", r)
	}
	// A tool taking P with other tools on it is a cycle, not a conflict.
	r = check(tool(7), "P")
	if r.Status != "cycle" || len(r.Owners) != 3 {
		t.Errorf("cycle = %+v", r)
	}
	if !slices.Contains(r.Notes, integration.NoteToolShift) {
		t.Errorf("toolShift note missing: %v", r.Notes)
	}
	if r := check(tool(4), "Ctrl+Z"); slices.Contains(r.Notes, integration.NoteToolShift) {
		t.Errorf("toolShift on a chord with modifiers: %v", r.Notes)
	}
	// A command against a tool is a conflict, both ways.
	if r := check("command:basiccommand/swapcolor", "E"); r.Status != "conflict" || owner(r)[0] != tool(8) {
		t.Errorf("command on tool key = %+v", r)
	}
	if r := check(tool(8), "X"); r.Status != "conflict" || owner(r)[0] != "command:basiccommand/swapcolor" {
		t.Errorf("tool on command key = %+v", r)
	}
	if r := check(tool(8), "E"); r.Status != "same" {
		t.Errorf("tool same = %+v", r)
	}
	if r := check("", "Win+A"); r.Status != "unsupported" {
		t.Errorf("win = %+v", r)
	}
	if r := check("", "["); r.Status != "unconfirmed" {
		t.Errorf("symbol = %+v", r)
	}
	if _, err := f.in.Check("", "Ctrl+", "en"); err == nil {
		t.Error("bad keys accepted")
	}
	if _, err := f.in.Check("nonsense", "A", "en"); err == nil {
		t.Error("bad function accepted")
	}

	// Without the preference, no toolShift note.
	if err := os.Remove(filepath.Join(f.dir, "Preference", "Config.sqlite")); err != nil {
		t.Fatal(err)
	}
	if r := check(tool(7), "P"); slices.Contains(r.Notes, integration.NoteToolShift) {
		t.Errorf("note without the preference file: %v", r.Notes)
	}
}

func TestCompareAndPlan(t *testing.T) {
	f := newFixture(t)
	items := []integration.Item{
		{Keys: "Ctrl+N", Function: "command:basiccommand/filenew", Name: "New"},                  // consistent
		{Keys: "Ctrl+Shift+N", Function: "command:basiccommand/filenew"},                         // changed
		{Keys: "Ctrl+M", Function: "command:advancedcommand/layermergevisiblelayer"},             // removed
		{Keys: "Ctrl+Q", Function: "command:basiccommand/gone"},                                  // missing
		{Keys: "X", Function: "tool:" + csptest.UIDHex(7)},                                       // tool: command holds X
		{Keys: "E", Function: tool(8)},                                                           // consistent
		{Keys: "Q", Function: tool(7)},                                                           // removed (G-pen has no key)
		{Keys: "Ctrl+Z", Function: tool(7)},                                                      // tool with a chord
		{Keys: "Ctrl+Alt+Z", Function: "command:basiccommand/filenew"},                           // takes undo's key
		{Keys: "F", Function: "weird"},                                                           // not a CSP function
		{Keys: "P", Function: tool(8)},                                                           // changed, cycle ok
		{Keys: "Ctrl+Alt+Shift+Q", Function: "command:basiccommand/symbols", Name: "symbols"},    // changed
		{Keys: "Ctrl+Backspace", Function: "command:basiccommand/symbols"},                       // changed
		{Keys: "Ctrl+Alt+Backspace", Function: "command:advancedcommand/layermergevisiblelayer"}, // removed
	}
	statuses, rev, err := f.in.Compare(items, "en")
	if err != nil || rev == "" {
		t.Fatal(err)
	}
	var states []string
	for _, s := range statuses {
		states = append(states, s.State)
	}
	want := []string{"consistent", "changed", "removed", "missing", "removed", "consistent", "removed", "removed", "changed", "missing", "changed", "changed", "changed", "removed"}
	if !reflect.DeepEqual(states, want) {
		t.Fatalf("states = %v", states)
	}
	if !reflect.DeepEqual(statuses[1].Current, []string{"Ctrl+N"}) {
		t.Errorf("current = %v", statuses[1].Current)
	}
	if len(statuses[8].Owners) != 1 || statuses[8].Owners[0].ID != "command:basiccommand/undo" {
		t.Errorf("owners = %+v", statuses[8].Owners)
	}
	if !reflect.DeepEqual(statuses[11].Current, []string{"Ctrl+["}) {
		t.Errorf("symbol current = %v", statuses[11].Current)
	}
	if id := integration.DifferenceID(statuses); len(id) != 64 {
		t.Errorf("difference id = %q", id)
	}

	plan, err := f.in.Plan(items, "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries) != 12 {
		t.Fatalf("entries = %d", len(plan.Entries))
	}
	byKey := func(keys, fn string) integration.PlanEntry {
		t.Helper()
		for _, e := range plan.Entries {
			if e.Item.Keys == keys && e.Item.Function == fn {
				return e
			}
		}
		t.Fatalf("no entry for %s %s", keys, fn)
		return integration.PlanEntry{}
	}
	// Free key: just an add.
	e := byKey("Ctrl+Shift+N", "command:basiccommand/filenew")
	if !reflect.DeepEqual(e.Changes, []integration.Change{{Function: "command:basiccommand/filenew", Add: "Ctrl+Shift+N"}}) || len(e.Displaced) != 0 || e.Skipped != "" {
		t.Errorf("free = %+v", e)
	}
	// A key another command holds: it loses the key.
	e = byKey("Ctrl+Alt+Z", "command:basiccommand/filenew")
	if len(e.Changes) != 2 || e.Changes[1] != (integration.Change{Function: "command:basiccommand/undo", Remove: "Ctrl+Alt+Z"}) ||
		!reflect.DeepEqual(e.Displaced, []integration.Displaced{{Function: "command:basiccommand/undo", Name: "Undo", Keys: "Ctrl+Alt+Z"}}) {
		t.Errorf("displace command = %+v", e)
	}
	// A tool item on a command's key: the command loses it.
	e = byKey("X", tool(7))
	if len(e.Changes) != 2 || e.Changes[1].Function != "command:basiccommand/swapcolor" || e.Changes[1].Remove != "X" || len(e.Displaced) != 1 {
		t.Errorf("tool on command key = %+v", e)
	}
	// A command on tools' key: all the tools lose it.
	// (the sample has no such item; checked in the write tests)
	// A tool on a cycle: nobody is displaced.
	e = byKey("P", tool(8))
	if len(e.Changes) != 1 || len(e.Displaced) != 0 {
		t.Errorf("cycle = %+v", e)
	}
	if e = byKey("Q", tool(7)); len(e.Changes) != 1 || e.Skipped != "" {
		t.Errorf("tool free = %+v", e)
	}
	if e = byKey("Ctrl+Z", tool(7)); e.Skipped != "unsupportedKey" || len(e.Changes) != 0 {
		t.Errorf("tool chord = %+v", e)
	}
	if e = byKey("Ctrl+Q", "command:basiccommand/gone"); e.Skipped != "missing" {
		t.Errorf("missing = %+v", e)
	}
	if e = byKey("F", "weird"); e.Skipped != "badFunction" && e.Skipped != "missing" {
		t.Errorf("bad function = %+v", e)
	}
	// Plan's changes are what Write accepts.
	var all []integration.Change
	for _, e := range plan.Entries {
		if e.Item.Keys == "P" || e.Item.Keys == "X" || e.Item.Keys == "Ctrl+Alt+Z" {
			continue // these clash with each other on purpose; the UI resolves it
		}
		all = append(all, e.Changes...)
	}
	if _, err := f.in.Write(all); err != nil {
		t.Fatalf("writing the plan: %v", err)
	}
	after, _, err := f.in.Compare(items, "en")
	if err != nil {
		t.Fatal(err)
	}
	if after[1].State != "consistent" || after[2].State != "consistent" || after[6].State != "consistent" {
		t.Errorf("after write: %v", after)
	}
}

func readKHC(t *testing.T, f *fixture) *csp.CommandSet {
	t.Helper()
	set, err := csp.ReadKHC(f.khc)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func readTODB(t *testing.T, f *fixture) *csp.ToolTree {
	t.Helper()
	tree, err := csp.ReadTODB(f.todb)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func chordsOf(set *csp.CommandSet, id string) []string {
	var out []string
	for _, c := range set.Command(id).Chords() {
		out = append(out, c.String())
	}
	return out
}

func TestWrite(t *testing.T) {
	f := newFixture(t)
	before := f.listing(t)
	rev0, _ := f.in.Revision()

	// Move a key: undo gives Ctrl+Alt+Z to filenew, takes Ctrl+Alt+Y; the
	// tool Q is set, the eraser loses E. Order of the changes is free.
	res, err := f.in.Write([]integration.Change{
		{Function: "command:basiccommand/filenew", Add: "Ctrl+Alt+Z"},
		{Function: "command:basiccommand/undo", Remove: "Ctrl+Alt+Z"},
		{Function: "command:basiccommand/undo", Add: "Ctrl+Alt+Y"},
		{Function: tool(7), Add: "Q"},
		{Function: tool(8), Remove: "E"},
		{Function: "command:advancedcommand/layermergevisiblelayer", Add: "Ctrl+Shift+M"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Backup.ID == "" || res.Backup.Reason != "write" || res.Revision == "" || res.Revision == rev0 {
		t.Errorf("result = %+v", res)
	}
	set := readKHC(t, f)
	if got := chordsOf(set, "basiccommand/filenew"); !reflect.DeepEqual(got, []string{"Ctrl+N", "Ctrl+Alt+Z"}) {
		t.Errorf("filenew = %v", got)
	}
	if got := chordsOf(set, "basiccommand/undo"); !reflect.DeepEqual(got, []string{"Ctrl+Z", "Ctrl+Alt+Y"}) {
		t.Errorf("undo = %v", got)
	}
	if got := chordsOf(set, "advancedcommand/layermergevisiblelayer"); !reflect.DeepEqual(got, []string{"Ctrl+Shift+M"}) {
		t.Errorf("merge = %v", got)
	}
	tree := readTODB(t, f)
	if tree.Node(csptest.UIDHex(7)).KeyCode != 17 || tree.Node(csptest.UIDHex(8)).KeyCode != 0 {
		t.Error("tool keys")
	}
	// Everything else is as it was.
	after := f.listing(t)
	for id, fn := range before {
		switch id {
		case "command:basiccommand/filenew", "command:basiccommand/undo", "command:advancedcommand/layermergevisiblelayer", tool(7), tool(8):
			continue
		}
		if !reflect.DeepEqual(fn.Keys, after[id].Keys) || fn.Unsupported != after[id].Unsupported || fn.Unconfirmed != after[id].Unconfirmed {
			t.Errorf("%s changed: %+v -> %+v", id, fn, after[id])
		}
	}
	for _, p := range []string{f.khc + ".padmate-tmp", f.todb + ".padmate-tmp", f.khc + ".padmate-undo"} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s left behind", p)
		}
	}

	// The backup holds the old files; restoring brings them back.
	list, err := f.in.Backups()
	if err != nil || len(list) != 1 || list[0].ID != res.Backup.ID {
		t.Fatalf("backups = %+v %v", list, err)
	}
	restored, err := f.in.Restore(res.Backup.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Revision == res.Revision || restored.Backup.ID == "" || restored.Backup.Reason != "before-restore" {
		t.Errorf("restore result = %+v", restored)
	}
	if got := chordsOf(readKHC(t, f), "basiccommand/undo"); !reflect.DeepEqual(got, []string{"Ctrl+Z", "Ctrl+Alt+Z"}) {
		t.Errorf("undo after restore = %v", got)
	}
	if readTODB(t, f).Node(csptest.UIDHex(8)).KeyCode != 5 {
		t.Error("eraser after restore")
	}
	if list, _ := f.in.Backups(); len(list) != 2 {
		t.Errorf("backups after restore = %d", len(list))
	}
	if _, err := f.in.Restore("20200101T000000.000Z"); !errors.Is(err, integration.ErrNotFound) {
		t.Errorf("restore of nothing: %v", err)
	}
	if _, err := f.in.Restore("../x"); err == nil {
		t.Error("restore of a path")
	}
}

func TestWriteRefusals(t *testing.T) {
	f := newFixture(t)
	snapshot := func(p string) string {
		fp, err := cspfile.FingerprintOf(p)
		if err != nil {
			t.Fatal(err)
		}
		return fp.SHA256
	}
	k0, d0 := snapshot(f.khc), snapshot(f.todb)

	refused := func(name string, reason string, changes ...integration.Change) {
		t.Helper()
		_, err := f.in.Write(changes)
		var ce *integration.ChangeError
		if !errors.As(err, &ce) || ce.Reason != reason {
			t.Errorf("%s: err = %v, want reason %s", name, err, reason)
		}
		if snapshot(f.khc) != k0 || snapshot(f.todb) != d0 {
			t.Errorf("%s: files changed", name)
		}
		if l, _ := f.in.Backups(); len(l) != 0 {
			t.Errorf("%s: backup made", name)
		}
	}
	refused("command twice on a key", "conflict",
		integration.Change{Function: "command:basiccommand/undo", Add: "Ctrl+N"})
	refused("command on a tool key", "conflict",
		integration.Change{Function: "command:basiccommand/filenew", Add: "E"})
	refused("tool on a command key", "conflict",
		integration.Change{Function: tool(7), Add: "X"})
	refused("tool chord", "unsupportedKey", integration.Change{Function: tool(7), Add: "Ctrl+Q"})
	refused("tool digit", "unsupportedKey", integration.Change{Function: tool(7), Add: "1"})
	refused("symbol", "unsupportedKey", integration.Change{Function: "command:basiccommand/filenew", Add: "["})
	refused("win", "unsupportedKey", integration.Change{Function: "command:basiccommand/filenew", Add: "Win+A"})
	refused("bad keys", "badKeys", integration.Change{Function: "command:basiccommand/filenew", Add: "Ctrl+"})
	refused("unknown command", "unknownFunction", integration.Change{Function: "command:basiccommand/nope", Add: "F9"})
	refused("unknown tool", "unknownFunction", integration.Change{Function: "tool:" + csptest.UIDHex(99), Add: "A"})
	refused("not a function", "unknownFunction", integration.Change{Function: "xyz", Add: "A"})
	refused("remove what is not there", "noSuchKey", integration.Change{Function: "command:basiccommand/undo", Remove: "Ctrl+Q"})
	refused("remove wrong tool key", "noSuchKey", integration.Change{Function: tool(8), Remove: "Q"})
	// A set that is fine only together is accepted; one bad change refuses all.
	refused("one bad change refuses all", "conflict",
		integration.Change{Function: "command:advancedcommand/layermergevisiblelayer", Add: "F9"},
		integration.Change{Function: "command:basiccommand/undo", Add: "Ctrl+N"})
	// Two commands on one new key in the same set.
	refused("two new on one key", "conflict",
		integration.Change{Function: "command:advancedcommand/layermergevisiblelayer", Add: "F9"},
		integration.Change{Function: "command:basiccommand/swapcolor", Add: "F9"})
	// A tool cycle is allowed.
	if _, err := f.in.Write([]integration.Change{{Function: tool(7), Add: "P"}}); err != nil {
		t.Errorf("cycle refused: %v", err)
	}
	// Nothing to do: no write, no backup.
	n, _ := f.in.Backups()
	res, err := f.in.Write([]integration.Change{{Function: "command:basiccommand/filenew", Add: "Ctrl+N"}, {Function: tool(8)}})
	if err != nil || res.Backup.ID != "" || res.Revision == "" {
		t.Errorf("no-op = %+v %v", res, err)
	}
	if m, _ := f.in.Backups(); len(m) != len(n) {
		t.Error("no-op made a backup")
	}
}

func TestWriteRunningAndBusy(t *testing.T) {
	f := newFixture(t)
	change := []integration.Change{{Function: "command:basiccommand/filenew", Add: "F9"}}
	f.running = true
	if _, err := f.in.Write(change); !errors.Is(err, integration.ErrRunning) {
		t.Errorf("running: %v", err)
	}
	if _, err := f.in.Restore("20200101T000000.000Z"); err == nil {
		t.Error("restore while running with no backup")
	}
	if l, _ := f.in.Backups(); len(l) != 0 {
		t.Error("backup while running")
	}
	f.running = false
	// Backups taken, then CSP started: the restore is refused.
	res, err := f.in.Write(change)
	if err != nil {
		t.Fatal(err)
	}
	f.running = true
	if _, err := f.in.Restore(res.Backup.ID); !errors.Is(err, integration.ErrRunning) {
		t.Errorf("restore while running: %v", err)
	}
	f.running = false

	// A journal file next to a target: busy, nothing changes.
	if err := os.WriteFile(f.khc+"-journal", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := cspfile.FingerprintOf(f.khc)
	if _, err := f.in.Write([]integration.Change{{Function: "command:basiccommand/filenew", Add: "F10"}}); !errors.Is(err, integration.ErrBusy) {
		t.Errorf("journal: %v", err)
	}
	if after, _ := cspfile.FingerprintOf(f.khc); after != before {
		t.Error("file changed despite the journal")
	}
	// Reading with a journal there is busy too.
	f.in.invalidate()
	if _, err := f.in.Functions("en"); !errors.Is(err, integration.ErrBusy) {
		t.Errorf("read with journal: %v", err)
	}
}

func TestRunningCheckedAgainBeforeReplace(t *testing.T) {
	f := newFixture(t)
	calls := 0
	f.in.opts.Running = func() (bool, error) {
		calls++
		return calls >= 2, nil // CSP starts while the copy is edited
	}
	before, _ := cspfile.FingerprintOf(f.khc)
	_, err := f.in.Write([]integration.Change{{Function: "command:basiccommand/filenew", Add: "F9"}})
	if !errors.Is(err, integration.ErrRunning) {
		t.Fatalf("err = %v", err)
	}
	if after, _ := cspfile.FingerprintOf(f.khc); after != before {
		t.Error("file replaced")
	}
}

func TestBackupsKeep30(t *testing.T) {
	f := newFixture(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f.in.backups.Now = func() time.Time { now = now.Add(time.Second); return now }
	for i := range 32 {
		key := "F9"
		if i%2 == 1 {
			key = "F10"
		}
		// Alternate between two keys so that every write changes something.
		changes := []integration.Change{{Function: "command:basiccommand/filenew", Add: key}}
		if i > 0 {
			other := "F10"
			if i%2 == 1 {
				other = "F9"
			}
			changes = append(changes, integration.Change{Function: "command:basiccommand/filenew", Remove: other})
		}
		if _, err := f.in.Write(changes); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	list, err := f.in.Backups()
	if err != nil || len(list) != 30 {
		t.Fatalf("backups = %d %v", len(list), err)
	}
	if !list[0].Created.After(list[29].Created) {
		t.Error("not newest first")
	}
	if want := filepath.Join(f.padmate, "integration-backups", "csp"); !isDir(want) {
		t.Errorf("backup root %s missing", want)
	}
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func TestCacheFollowsFiles(t *testing.T) {
	f := newFixture(t)
	l1, _ := f.in.Functions("en")
	// CSP (the test) changes the file behind our back: the next read sees it.
	if err := csp.ApplyCommandEdits(f.khc, []csp.CommandEdit{csp.AddShortcut("basiccommand/filenew", "F9", 0)}); err != nil {
		t.Fatal(err)
	}
	l2, _ := f.in.Functions("en")
	if l1.Revision == l2.Revision {
		t.Error("revision did not change")
	}
	if !slices.Contains(f.listing(t)["command:basiccommand/filenew"].Keys, "F9") {
		t.Error("new key not listed")
	}
}
