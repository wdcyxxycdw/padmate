package csp

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadKHC(t *testing.T) {
	set, err := ReadKHC(makeKHC(t, sampleKHC()))
	if err != nil {
		t.Fatal(err)
	}
	if set.Info != 1 {
		t.Errorf("Info = %d", set.Info)
	}
	if len(set.Rows) != 8 {
		t.Fatalf("rows = %d", len(set.Rows))
	}
	for i, r := range set.Rows {
		if r.RowID != int64(i+1) {
			t.Errorf("row %d has id %d", i, r.RowID)
		}
	}
	var order []string
	for _, c := range set.Commands {
		order = append(order, c.ID)
	}
	want := []string{
		"basiccommand/filenew", "basiccommand/undo", "advancedcommand/layermergevisiblelayer",
		"basiccommand/28691CF0-1111-2222-3333-444455556666", "basiccommand/weird",
		"basiccommand/symbols", "basiccommand/devicecmd",
	}
	if len(order) != len(want) {
		t.Fatalf("commands = %v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("command %d = %s, want %s", i, order[i], want[i])
		}
	}

	undo := set.Command("basiccommand/undo")
	if len(undo.Shortcuts) != 2 || undo.Shortcuts[1].Modifier != 5 || undo.Shortcuts[1].Status != KeyOK {
		t.Errorf("undo = %+v", undo.Shortcuts)
	}
	if len(undo.Chords()) != 2 {
		t.Errorf("undo chords = %v", undo.Chords())
	}
	merge := set.Command("advancedcommand/layermergevisiblelayer")
	if len(merge.Shortcuts) != 0 || merge.NullRowID != 4 {
		t.Errorf("merge = %+v", merge)
	}
	weird := set.Command("basiccommand/weird")
	if len(weird.Shortcuts) != 1 || !weird.Shortcuts[0].Unsupported || weird.Shortcuts[0].Status != KeyUnsupported {
		t.Errorf("weird = %+v", weird.Shortcuts)
	}
	if len(set.Unsupported) != 1 || set.Unsupported[0].Command != "weird" {
		t.Errorf("unsupported = %+v", set.Unsupported)
	}
	if s := set.Command("basiccommand/symbols").Shortcuts[0]; s.Status != KeyUnconfirmed {
		t.Errorf("symbol status = %v", s.Status)
	}
	if s := set.Command("basiccommand/devicecmd").Shortcuts[0]; s.Status != KeyUnsupported || s.Unsupported {
		t.Errorf("device = %+v", s)
	}
	if set.Command("nope") != nil {
		t.Error("unknown command found")
	}
}

func TestReadKHCUnknownFormat(t *testing.T) {
	for name, ddl := range map[string][]string{
		"no shortcutmenu": {`CREATE TABLE shortcutinfo (_PW_ID INTEGER, UseMultilingualSupportingCommandShortcut INTEGER)`},
		"no shortcutinfo": {`CREATE TABLE shortcutmenu (_PW_ID INTEGER, menucommandtype TEXT, menucommand TEXT, shortcut TEXT, modifier INTEGER)`},
		"missing column": {
			`CREATE TABLE shortcutinfo (_PW_ID INTEGER, UseMultilingualSupportingCommandShortcut INTEGER)`,
			`CREATE TABLE shortcutmenu (_PW_ID INTEGER, menucommandtype TEXT, menucommand TEXT, shortcut TEXT)`,
		},
		"empty database": nil,
	} {
		path := filepath.Join(t.TempDir(), "x.khc")
		db := mustOpenNew(t, path, "")
		for _, q := range ddl {
			mustExec(t, db, q)
		}
		mustExec(t, db, `CREATE TABLE filler (a INTEGER)`)
		_ = db.Close()
		if _, err := ReadKHC(path); !errors.Is(err, ErrUnknownFormat) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestReadKHCNotADatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.khc")
	if err := os.WriteFile(path, []byte("this is not sqlite, just some text that is long enough"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadKHC(path); err == nil {
		t.Error("expected an error")
	}
	if _, err := ReadKHC(filepath.Join(t.TempDir(), "missing.khc")); err == nil {
		t.Error("expected an error for a missing file")
	}
}

func TestReadTODB(t *testing.T) {
	tree, err := ReadTODB(makeTODB(t, sampleTools()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for i, n := range tree.Nodes {
		names = append(names, n.Name)
		if n.Index != i {
			t.Errorf("%s index = %d", n.Name, n.Index)
		}
	}
	wantOrder := []string{"Pen", "Pencils", "Pencil", "Mechanical", "Inks", "G-pen", "Eraser", "Soft", "Eraser soft"}
	if len(names) != len(wantOrder) {
		t.Fatalf("names = %v", names)
	}
	for i := range wantOrder {
		if names[i] != wantOrder[i] {
			t.Errorf("order %d = %s, want %s", i, names[i], wantOrder[i])
		}
	}
	pencil := tree.Node(uidHex(4))
	if pencil == nil || pencil.Level != 3 || pencil.ParentUUID != uidHex(3) || pencil.KeyCode != 16 {
		t.Fatalf("pencil = %+v", pencil)
	}
	if got := pencil.Path; len(got) != 3 || got[0] != "Pen" || got[1] != "Pencils" || got[2] != "Pencil" {
		t.Errorf("path = %v", got)
	}
	pen := tree.Node(uidHex(2))
	if pen.Level != 1 || pen.ParentUUID != "" {
		t.Errorf("pen = %+v", pen)
	}
	if n := tree.Node(uidHex(10)); n.KeyCode != 40 {
		t.Errorf("raw code = %d", n.KeyCode)
	}
	if tree.Node(uidHex(1)) != nil {
		t.Error("root listed")
	}
}

func TestReadTODBBadLinks(t *testing.T) {
	tests := map[string][]toolRow{
		"cycle in next": {
			{1, 2, 0, "root", 0}, {2, 0, 3, "a", 0}, {3, 0, 2, "b", 0},
		},
		"child points to ancestor": {
			{1, 2, 0, "root", 0}, {2, 3, 0, "a", 0}, {3, 2, 0, "b", 0},
		},
		"shared child": {
			{1, 2, 0, "root", 0}, {2, 4, 3, "a", 0}, {3, 4, 0, "b", 0}, {4, 0, 0, "c", 0},
		},
		"dangling link": {
			{1, 2, 0, "root", 0}, {2, 0, 99, "a", 0},
		},
		"no root node": {
			{2, 0, 0, "a", 0},
		},
	}
	for name, nodes := range tests {
		if _, err := ReadTODB(makeTODB(t, nodes)); !errors.Is(err, ErrUnknownFormat) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestReadTODBUnknownFormat(t *testing.T) {
	for name, ddl := range map[string][]string{
		"no Manager": {`CREATE TABLE Node (NodeUuid BLOB, NodeName TEXT, NodeFirstChildUuid BLOB, NodeNextUuid BLOB, NodeShortCutKey INTEGER)`},
		"no Node":    {`CREATE TABLE Manager (RootUuid BLOB)`},
		"no root column": {
			`CREATE TABLE Manager (Other BLOB)`,
			`CREATE TABLE Node (NodeUuid BLOB, NodeName TEXT, NodeFirstChildUuid BLOB, NodeNextUuid BLOB, NodeShortCutKey INTEGER)`,
		},
		"no key column": {
			`CREATE TABLE Manager (RootUuid BLOB)`,
			`CREATE TABLE Node (NodeUuid BLOB, NodeName TEXT, NodeFirstChildUuid BLOB, NodeNextUuid BLOB)`,
		},
		"Manager empty": {
			`CREATE TABLE Manager (RootUuid BLOB)`,
			`CREATE TABLE Node (NodeUuid BLOB, NodeName TEXT, NodeFirstChildUuid BLOB, NodeNextUuid BLOB, NodeShortCutKey INTEGER)`,
		},
	} {
		path := filepath.Join(t.TempDir(), "x.todb")
		db := mustOpenNew(t, path, "")
		for _, q := range ddl {
			mustExec(t, db, q)
		}
		_ = db.Close()
		if _, err := ReadTODB(path); !errors.Is(err, ErrUnknownFormat) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
