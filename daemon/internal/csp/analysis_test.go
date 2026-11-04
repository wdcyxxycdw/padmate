package csp

import (
	"slices"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

func analysisFixture(t *testing.T) (*CommandSet, *ToolTree) {
	t.Helper()
	cmds, err := ReadKHC(makeKHC(t, []khcRow{
		{"basiccommand", "undo", "Z", 4},
		{"basiccommand", "undo", "Z", 5},
		{"basiccommand", "pencmd", "P", 0}, // a command on the tool key P
		{"basiccommand", "eraser", "E", 4},
		{"advancedcommand", "merge", "NULL", 0},
		{"basiccommand", "zoom", "NUM+", 0},
	}))
	if err != nil {
		t.Fatal(err)
	}
	tools, err := ReadTODB(makeTODB(t, sampleTools()))
	if err != nil {
		t.Fatal(err)
	}
	return cmds, tools
}

func ids(os []Owner) []string {
	var out []string
	for _, o := range os {
		out = append(out, o.Function)
	}
	return out
}

func TestOwners(t *testing.T) {
	cmds, tools := analysisFixture(t)
	an := NewAnalyzer(cmds, tools)
	tests := []struct {
		spec string
		want []string
	}{
		{"Ctrl+Z", []string{"basiccommand/undo"}},
		{"LCtrl+Z", []string{"basiccommand/undo"}},
		{"Ctrl+Alt+Z", []string{"basiccommand/undo"}},
		{"Z", nil},
		{"P", []string{"basiccommand/pencmd", uidHex(2), uidHex(4), uidHex(5)}},
		{"E", []string{uidHex(8)}},
		{"Ctrl+E", []string{"basiccommand/eraser"}},
		{"NumAdd", []string{"basiccommand/zoom"}},
		{"Win+Z", nil},
		{"Enter", nil},
	}
	for _, tt := range tests {
		got := ids(an.Owners(keys.MustParse(tt.spec)))
		if !slices.Equal(got, tt.want) {
			t.Errorf("Owners(%s) = %v, want %v", tt.spec, got, tt.want)
		}
	}
	o := an.Owners(keys.MustParse("E"))[0]
	if o.Kind != KindTool || o.Name != "Eraser" || !slices.Equal(o.Path, []string{"Eraser"}) {
		t.Errorf("tool owner = %+v", o)
	}
	if got := NewAnalyzer(nil, nil).Owners(keys.MustParse("P")); got != nil {
		t.Errorf("empty analyzer owners = %v", got)
	}
}

func TestCheck(t *testing.T) {
	cmds, tools := analysisFixture(t)
	an := NewAnalyzer(cmds, tools)
	cmd := func(id string) Target { return Target{Kind: KindCommand, Function: id} }
	tool := func(n byte) Target { return Target{Kind: KindTool, Function: uidHex(n)} }
	tests := []struct {
		name   string
		target Target
		spec   string
		state  CheckState
		owners []string
	}{
		{"free key for command", cmd("advancedcommand/merge"), "Ctrl+M", CheckFree, nil},
		{"command already holds it", cmd("basiccommand/undo"), "Ctrl+Z", CheckFree, nil},
		{"command vs command", cmd("advancedcommand/merge"), "Ctrl+Z", CheckConflict, []string{"basiccommand/undo"}},
		{"command vs tool", cmd("advancedcommand/merge"), "E", CheckConflict, []string{uidHex(8)}},
		{"tool free", tool(7), "G", CheckFree, nil},
		{"tool joins a tool cycle", tool(7), "E", CheckCycle, []string{uidHex(8)}},
		{"tool already in cycle", tool(4), "P", CheckConflict, []string{"basiccommand/pencmd", uidHex(2), uidHex(5)}},
		{"tool vs command only", tool(7), "Ctrl+E", CheckConflict, []string{"basiccommand/eraser"}},
		{"tool unspellable", tool(7), "Ctrl+Shift+Win+P", CheckFree, nil},
	}
	for _, tt := range tests {
		got := an.Check(tt.target, keys.MustParse(tt.spec))
		if got.State != tt.state || !slices.Equal(ids(got.Owners), tt.owners) {
			t.Errorf("%s: %v %v, want %v %v", tt.name, got.State, ids(got.Owners), tt.state, tt.owners)
		}
	}
	// Tool cycle with no command: pure cycle in tree order.
	cmds2, _ := ReadKHC(makeKHC(t, nil))
	an2 := NewAnalyzer(cmds2, tools)
	got := an2.Check(tool(7), keys.MustParse("P"))
	if got.State != CheckCycle || !slices.Equal(ids(got.Owners), []string{uidHex(2), uidHex(4), uidHex(5)}) {
		t.Errorf("cycle = %v %v", got.State, ids(got.Owners))
	}
}

func TestCycles(t *testing.T) {
	_, tools := analysisFixture(t)
	cy := NewAnalyzer(nil, tools).Cycles()
	if len(cy) != 1 || cy[0].Code != 16 || cy[0].Chord != keys.MustParse("P") || len(cy[0].Tools) != 3 {
		t.Fatalf("cycles = %+v", cy)
	}
	for i, id := range []byte{2, 4, 5} {
		if cy[0].Tools[i].UUID != uidHex(id) {
			t.Errorf("tool %d = %s", i, cy[0].Tools[i].Name)
		}
	}
	if got := NewAnalyzer(nil, nil).Cycles(); got != nil {
		t.Errorf("no tools: %v", got)
	}
}

func TestCompare(t *testing.T) {
	cmds, tools := analysisFixture(t)
	chord := keys.MustParse
	items := []Item{
		{KindCommand, "basiccommand/undo", chord("Ctrl+Z"), "Undo"},               // consistent
		{KindCommand, "basiccommand/undo", chord("Ctrl+Alt+Z"), "Undo"},           // consistent (second row)
		{KindCommand, "basiccommand/zoom", chord("Ctrl+NumAdd"), "Zoom"},          // changed: key differs
		{KindCommand, "advancedcommand/merge", chord("Ctrl+M"), "Merge"},          // removed
		{KindCommand, "basiccommand/gone", chord("Ctrl+G"), "Gone"},               // missing
		{KindCommand, "basiccommand/zoom", chord("Ctrl+Z"), "Zoom on Undo's key"}, // changed, chord owned by undo
		{KindTool, uidHex(8), chord("E"), "Eraser"},                               // consistent
		{KindTool, uidHex(8), chord("Q"), "Eraser to Q"},                          // changed
		{KindTool, uidHex(7), chord("G"), "G-pen"},                                // removed
		{KindTool, uidHex(99), chord("G"), "Gone tool"},                           // missing
		{KindTool, uidHex(4), chord("P"), "Pencil"},                               // consistent (cycle)
		{KindTool, uidHex(10), chord("E"), "Soft"},                                // changed (code 40), E owned by Eraser and eraser cmd? (cmd is Ctrl+E)
	}
	got := Compare(items, cmds, tools)
	wantStates := []State{Consistent, Consistent, Changed, Removed, Missing, Changed, Consistent, Changed, Removed, Missing, Consistent, Changed}
	for i, w := range wantStates {
		if got[i].State != w {
			t.Errorf("item %d (%s) = %v, want %v", i, items[i].Name, got[i].State, w)
		}
		if got[i].Item != items[i] {
			t.Errorf("item %d not echoed", i)
		}
	}

	zoom := got[2]
	if len(zoom.Current) != 1 || zoom.Current[0].Key != "NUM+" || zoom.Current[0].Chord != chord("NumAdd") || len(zoom.Owners) != 0 {
		t.Errorf("zoom = %+v", zoom)
	}
	zoomOnUndo := got[5]
	if !slices.Equal(ids(zoomOnUndo.Owners), []string{"basiccommand/undo"}) || len(zoomOnUndo.Current) != 1 {
		t.Errorf("zoomOnUndo = %+v", zoomOnUndo)
	}
	q := got[7]
	if len(q.Current) != 1 || q.Current[0].Chord != chord("E") || q.Current[0].Code != 5 || len(q.Owners) != 0 {
		t.Errorf("q = %+v", q)
	}
	soft := got[11]
	if len(soft.Current) != 1 || soft.Current[0].Code != 40 || soft.Current[0].Chord.IsValid() ||
		!slices.Equal(ids(soft.Owners), []string{uidHex(8)}) {
		t.Errorf("soft = %+v", soft)
	}
	if AllConsistent(got) {
		t.Error("AllConsistent over mixed states")
	}
	if !AllConsistent(got[:2]) || !AllConsistent(nil) {
		t.Error("AllConsistent over consistent items")
	}
}

func TestCompareExtrasAndNilSources(t *testing.T) {
	cmds, tools := analysisFixture(t)
	// CSP has many shortcuts the saved list does not mention: no difference.
	got := Compare([]Item{{KindCommand, "basiccommand/pencmd", keys.MustParse("P"), ""}}, cmds, tools)
	if !AllConsistent(got) {
		t.Errorf("extra shortcuts counted: %+v", got)
	}
	got = Compare([]Item{
		{KindCommand, "basiccommand/pencmd", keys.MustParse("P"), ""},
		{KindTool, uidHex(2), keys.MustParse("P"), ""},
		{Kind: 0, Function: "x"},
	}, nil, nil)
	for _, s := range got {
		if s.State != Missing {
			t.Errorf("nil sources: %+v", s)
		}
	}
}

func TestCompareUnsupportedRow(t *testing.T) {
	cmds, err := ReadKHC(makeKHC(t, []khcRow{{"basiccommand", "weird", "A", 8}, {"basiccommand", "dev", "T3", 0}}))
	if err != nil {
		t.Fatal(err)
	}
	got := Compare([]Item{
		{KindCommand, "basiccommand/weird", keys.MustParse("A"), ""},
		{KindCommand, "basiccommand/dev", keys.MustParse("A"), ""},
	}, cmds, nil)
	for _, s := range got {
		if s.State != Changed || len(s.Current) != 1 || s.Current[0].Chord.IsValid() {
			t.Errorf("unsupported row: %+v", s)
		}
	}
}
