package csp

import (
	"errors"
	"os"
	"slices"
	"testing"
)

// rowsOf returns "id type/cmd key mod" for every row of the khc.
func rowsOf(t *testing.T, path string) []string {
	t.Helper()
	set, err := ReadKHC(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range set.Rows {
		out = append(out, string(rune('0'+r.RowID))+" "+r.ID()+" "+r.Key+" "+string(rune('0'+r.Modifier)))
	}
	return out
}

func TestApplyCommandEdits(t *testing.T) {
	base := []khcRow{
		{"basiccommand", "a", "A", 4},
		{"basiccommand", "a", "B", 4},
		{"basiccommand", "b", "NULL", 0},
		{"basiccommand", "c", "C", 4},
		{"advancedcommand", "d", "D", 0},
	}
	tests := []struct {
		name  string
		edits []CommandEdit
		want  []string // nil: expect an error and unchanged file
		err   error
	}{
		{
			name:  "fill NULL row in place",
			edits: []CommandEdit{AddShortcut("basiccommand/b", "x", 6)},
			want:  []string{"1 basiccommand/a A 4", "2 basiccommand/a B 4", "3 basiccommand/b X 6", "4 basiccommand/c C 4", "5 advancedcommand/d D 0"},
		},
		{
			name:  "insert for a command with shortcuts",
			edits: []CommandEdit{AddShortcut("basiccommand/c", "F5", 0)},
			want:  []string{"1 basiccommand/a A 4", "2 basiccommand/a B 4", "3 basiccommand/b NULL 0", "4 basiccommand/c C 4", "5 advancedcommand/d D 0", "6 basiccommand/c F5 0"},
		},
		{
			name:  "remove one of several deletes the row",
			edits: []CommandEdit{RemoveShortcut("basiccommand/a", "A", 4)},
			want:  []string{"2 basiccommand/a B 4", "3 basiccommand/b NULL 0", "4 basiccommand/c C 4", "5 advancedcommand/d D 0"},
		},
		{
			name:  "remove the last sets NULL",
			edits: []CommandEdit{RemoveShortcut("advancedcommand/d", "d", 0)},
			want:  []string{"1 basiccommand/a A 4", "2 basiccommand/a B 4", "3 basiccommand/b NULL 0", "4 basiccommand/c C 4", "5 advancedcommand/d NULL 0"},
		},
		{
			name: "edits compose in order",
			edits: []CommandEdit{
				RemoveShortcut("basiccommand/c", "C", 4),
				AddShortcut("basiccommand/b", "C", 4), // free after the removal
			},
			want: []string{"1 basiccommand/a A 4", "2 basiccommand/a B 4", "3 basiccommand/b C 4", "4 basiccommand/c NULL 0", "5 advancedcommand/d D 0"},
		},
		{
			name:  "duplicate of another command",
			edits: []CommandEdit{AddShortcut("basiccommand/b", "A", 4)},
			err:   ErrDuplicateShortcut,
		},
		{
			name:  "duplicate of itself",
			edits: []CommandEdit{AddShortcut("basiccommand/a", "A", 4)},
			err:   ErrDuplicateShortcut,
		},
		{
			name:  "failure rolls back earlier edits",
			edits: []CommandEdit{AddShortcut("basiccommand/b", "X", 0), AddShortcut("basiccommand/nope", "Y", 0)},
			err:   ErrUnknownCommand,
		},
		{name: "unknown command", edits: []CommandEdit{AddShortcut("basiccommand/zzz", "X", 0)}, err: ErrUnknownCommand},
		{name: "wrong type", edits: []CommandEdit{AddShortcut("advancedcommand/a", "X", 0)}, err: ErrUnknownCommand},
		{name: "bad command id", edits: []CommandEdit{AddShortcut("a", "X", 0)}, err: ErrBadEdit},
		{name: "modifier bit 8", edits: []CommandEdit{AddShortcut("basiccommand/b", "X", 8)}, err: ErrBadEdit},
		{name: "negative modifier", edits: []CommandEdit{AddShortcut("basiccommand/b", "X", -1)}, err: ErrBadEdit},
		{name: "unknown key name", edits: []CommandEdit{AddShortcut("basiccommand/b", "BOGUS", 0)}, err: ErrBadEdit},
		{name: "NULL as key", edits: []CommandEdit{AddShortcut("basiccommand/b", "NULL", 0)}, err: ErrBadEdit},
		{name: "remove a shortcut it does not have", edits: []CommandEdit{RemoveShortcut("basiccommand/a", "A", 0)}, err: ErrNoSuchShortcut},
		{name: "remove from NULL command", edits: []CommandEdit{RemoveShortcut("basiccommand/b", "X", 0)}, err: ErrNoSuchShortcut},
		{name: "zero op", edits: []CommandEdit{{Command: "basiccommand/a", Key: "A", Modifier: 4}}, err: ErrBadEdit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := makeKHC(t, base)
			before := rowsOf(t, path)
			err := ApplyCommandEdits(path, tt.edits)
			if tt.err != nil {
				if !errors.Is(err, tt.err) {
					t.Fatalf("err = %v, want %v", err, tt.err)
				}
				if got := rowsOf(t, path); !slices.Equal(got, before) {
					t.Errorf("file changed after a failed edit: %v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := rowsOf(t, path); !slices.Equal(got, tt.want) {
				t.Errorf("rows =\n%v\nwant\n%v", got, tt.want)
			}
		})
	}
}

func TestApplyCommandEditsKeepsOtherData(t *testing.T) {
	path := makeKHC(t, sampleKHC())
	beforeInfo := dumpTable(t, path, "shortcutinfo")
	beforeExtra := dumpTable(t, path, "shortcutmenu")
	if err := ApplyCommandEdits(path, []CommandEdit{AddShortcut("advancedcommand/layermergevisiblelayer", "M", 4)}); err != nil {
		t.Fatal(err)
	}
	if got := dumpTable(t, path, "shortcutinfo"); !slices.Equal(got, beforeInfo) {
		t.Error("shortcutinfo changed")
	}
	got := dumpTable(t, path, "shortcutmenu")
	diff := 0
	for i := range got {
		if got[i] != beforeExtra[i] {
			diff++
		}
	}
	if diff != 1 || len(got) != len(beforeExtra) {
		t.Errorf("%d rows differ", diff)
	}
}

func TestApplyCommandEditsUnknownFormat(t *testing.T) {
	path := makeTODB(t, sampleTools()) // not a khc
	if err := ApplyCommandEdits(path, nil); !errors.Is(err, ErrUnknownFormat) {
		t.Errorf("err = %v", err)
	}
	if err := ApplyCommandEdits(path+".missing", nil); err == nil {
		t.Error("expected an error for a missing file")
	}
	if _, err := os.Stat(path + ".missing"); err == nil {
		t.Error("a missing file was created")
	}
}

func TestApplyToolEdits(t *testing.T) {
	path := makeTODB(t, sampleTools())
	before := map[string][]string{}
	for _, tbl := range []string{"Node", "Manager", "Other"} {
		before[tbl] = dumpTable(t, path, tbl)
	}
	err := ApplyToolEdits(path, []ToolEdit{
		SetToolKey(uidHex(7), 16),    // G-pen joins the P cycle
		ClearToolKey(uidHex(2)),      // Pen loses P
		SetToolKey(uidHex(8), 0),     // Eraser cleared (same as ClearToolKey)
		SetToolKey(uidHex(10), 5),    // sub-tool takes E
		SetToolKey(uidHex(4), 26),    // Pencil to Z
		SetToolKey(uidHex(3), 1),     // group gets A
		SetToolKey(uidHex(3), 1),     // repeating is fine
		SetToolKey(uidHex(5), 0x0+2), // Mechanical to B
	})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := ReadTODB(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[byte]int{2: 0, 3: 1, 4: 26, 5: 2, 7: 16, 8: 0, 10: 5}
	for id, code := range want {
		if n := tree.Node(uidHex(id)); n.KeyCode != code {
			t.Errorf("node %d code = %d, want %d", id, n.KeyCode, code)
		}
	}
	for _, tbl := range []string{"Manager", "Other"} {
		if got := dumpTable(t, path, tbl); !slices.Equal(got, before[tbl]) {
			t.Errorf("table %s changed", tbl)
		}
	}
	// Every column but NodeShortCutKey is identical, row for row.
	other := func(path string) []string {
		db, err := openRO(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		rows, err := db.Query(`SELECT hex(NodeUuid), NodeName, hex(NodeFirstChildUuid), hex(NodeNextUuid), NodeVariantId, hex(NodeBlob), NodeScore, typeof(NodeName) FROM Node ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var a, b, c, d, f, h string
			var e int
			var g float64
			if err := rows.Scan(&a, &b, &c, &d, &e, &f, &g, &h); err != nil {
				t.Fatal(err)
			}
			out = append(out, a+"|"+b+"|"+c+"|"+d+"|"+f+"|"+h)
		}
		return out
	}
	ref := makeTODB(t, sampleTools())
	if a, b := other(ref), other(path); !slices.Equal(a, b) {
		t.Errorf("other columns changed:\n%v\n%v", a, b)
	}
}

func TestApplyToolEditsErrors(t *testing.T) {
	tests := []struct {
		name string
		edit ToolEdit
		err  error
	}{
		{"code 27", SetToolKey(uidHex(2), 27), ErrBadEdit},
		{"negative", SetToolKey(uidHex(2), -1), ErrBadEdit},
		{"bad uuid", SetToolKey("xyz", 1), ErrBadEdit},
		{"short uuid", SetToolKey("abcd", 1), ErrBadEdit},
		{"unknown node", SetToolKey(uidHex(99), 1), ErrUnknownTool},
	}
	for _, tt := range tests {
		path := makeTODB(t, sampleTools())
		before := dumpTable(t, path, "Node")
		err := ApplyToolEdits(path, []ToolEdit{SetToolKey(uidHex(7), 3), tt.edit})
		if !errors.Is(err, tt.err) {
			t.Errorf("%s: err = %v", tt.name, err)
		}
		if got := dumpTable(t, path, "Node"); !slices.Equal(got, before) {
			t.Errorf("%s: file changed", tt.name)
		}
	}
	if err := ApplyToolEdits(makeKHC(t, nil), nil); !errors.Is(err, ErrUnknownFormat) {
		t.Errorf("khc as todb: %v", err)
	}
}
