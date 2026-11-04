package csp

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"
)

// khcRow is a row of a synthetic khc.
type khcRow struct {
	typ, cmd, key string
	mod           int
}

// makeKHC creates a synthetic default.khc in a temp dir and returns its path.
func makeKHC(t *testing.T, rows []khcRow) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "default.khc")
	db := mustOpenNew(t, path, "")
	mustExec(t, db, `CREATE TABLE shortcutinfo (_PW_ID INTEGER, UseMultilingualSupportingCommandShortcut INTEGER)`)
	mustExec(t, db, `INSERT INTO shortcutinfo VALUES (1, 1)`)
	mustExec(t, db, `CREATE TABLE shortcutmenu (_PW_ID INTEGER PRIMARY KEY AUTOINCREMENT, menucommandtype TEXT, menucommand TEXT, shortcut TEXT, modifier INTEGER, extra TEXT DEFAULT 'x')`)
	for _, r := range rows {
		mustExec(t, db, `INSERT INTO shortcutmenu (menucommandtype, menucommand, shortcut, modifier) VALUES (?,?,?,?)`,
			r.typ, r.cmd, r.key, r.mod)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// uid returns the 16-byte uuid whose last byte is n.
func uid(n byte) []byte {
	b := make([]byte, 16)
	b[0] = 0xAB
	b[15] = n
	return b
}

func uidHex(n byte) string { return hex.EncodeToString(uid(n)) }

// toolRow is a node of a synthetic todb. first and next are uuid numbers, 0 for none.
type toolRow struct {
	id, first, next byte
	name            string
	key             int
}

// makeTODB creates a synthetic EditImageTool.todb (UTF-16le) with root uuid 1.
func makeTODB(t *testing.T, nodes []toolRow) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "EditImageTool.todb")
	db := mustOpenNew(t, path, "PRAGMA encoding='UTF-16le'")
	mustExec(t, db, `CREATE TABLE Manager (ManagerId INTEGER, RootNodeUuid BLOB, SavedCount INTEGER, Note TEXT)`)
	mustExec(t, db, `INSERT INTO Manager VALUES (1, ?, 7, '工具')`, uid(1))
	mustExec(t, db, `CREATE TABLE Node (NodeUuid BLOB, NodeName TEXT, NodeFirstChildUuid BLOB, NodeNextUuid BLOB, NodeShortCutKey INTEGER, NodeVariantId INTEGER, NodeBlob BLOB, NodeScore REAL)`)
	mustExec(t, db, `CREATE TABLE Other (a INTEGER, b TEXT)`)
	mustExec(t, db, `INSERT INTO Other VALUES (5, '笔')`)
	for _, n := range nodes {
		var first, next any
		if n.first != 0 {
			first = uid(n.first)
		}
		if n.next != 0 {
			next = uid(n.next)
		}
		mustExec(t, db, `INSERT INTO Node VALUES (?,?,?,?,?,?,?,?)`,
			uid(n.id), n.name, first, next, n.key, int(n.id)*10, []byte{1, 2, n.id}, float64(n.id)/3)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// sampleTools is root(1) > Pen(2){ Pencil(3) key 16, Brush(4) key 2 }... see
// the layout:
//
//	1 root
//	2 Pen (tool, P)               level 1
//	  3 Pencils (group)           level 2
//	    4 Pencil (P)              level 3
//	    5 Mechanical (P)          level 3
//	  6 Inks (group)              level 2
//	    7 G-pen (none)            level 3
//	8 Eraser (E)                  level 1
//	  9 Soft (group)              level 2
//	    10 Eraser soft (code 40)  level 3
func sampleTools() []toolRow {
	return []toolRow{
		{1, 2, 0, "root", 0},
		{2, 3, 8, "Pen", 16},
		{3, 4, 6, "Pencils", 0},
		{4, 0, 5, "Pencil", 16},
		{5, 0, 0, "Mechanical", 16},
		{6, 7, 0, "Inks", 0},
		{7, 0, 0, "G-pen", 0},
		{8, 9, 0, "Eraser", 5},
		{9, 10, 0, "Soft", 0},
		{10, 0, 0, "Eraser soft", 40},
	}
}

func mustOpenNew(t *testing.T, path, pragma string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if pragma != "" {
		mustExec(t, db, pragma)
	}
	return db
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// dumpTable returns every row of table as text, ordered by rowid.
func dumpTable(t *testing.T, path, table string) []string {
	t.Helper()
	db, err := openRO(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query("SELECT * FROM " + table + " ORDER BY rowid")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	cols, _ := rows.Columns()
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%#v", vals))
	}
	return out
}

// sampleKHC is a small realistic set.
func sampleKHC() []khcRow {
	return []khcRow{
		{"basiccommand", "filenew", "N", 4},
		{"basiccommand", "undo", "Z", 4},
		{"basiccommand", "undo", "Z", 5}, // Ctrl+Alt+Z
		{"advancedcommand", "layermergevisiblelayer", "NULL", 0},
		{"basiccommand", "28691CF0-1111-2222-3333-444455556666", "F5", 0},
		{"basiccommand", "weird", "A", 8},
		{"basiccommand", "symbols", "[", 4},
		{"basiccommand", "devicecmd", "T1", 0},
	}
}
