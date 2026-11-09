// Package csptest builds synthetic CLIP STUDIO PAINT settings files for
// tests: a default.khc, an EditImageTool.todb and a Preference\Config.sqlite
// with the tables and columns internal/csp reads. It never touches a real
// CSP file.
package csptest

import (
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// KHCRow is a row of a synthetic khc.
type KHCRow struct {
	Type, Cmd, Key string
	Mod            int
}

// ToolRow is a node of a synthetic todb. First and Next are uuid numbers,
// 0 for none. Node 1 must be the root.
type ToolRow struct {
	ID, First, Next byte
	Name            string
	Key             int
}

// UID returns the 16-byte uuid whose last byte is n.
func UID(n byte) []byte {
	b := make([]byte, 16)
	b[0] = 0xAB
	b[15] = n
	return b
}

// UIDHex is UID as the 32 lower-case hex characters the tool functions use.
func UIDHex(n byte) string { return hex.EncodeToString(UID(n)) }

// MakeKHC creates a default.khc in dir (created if needed) and returns its path.
func MakeKHC(t *testing.T, dir string, rows []KHCRow) string {
	t.Helper()
	path := filepath.Join(dir, "Shortcut", "default.khc")
	db := openNew(t, path, "")
	exec(t, db, `CREATE TABLE shortcutinfo (_PW_ID INTEGER, UseMultilingualSupportingCommandShortcut INTEGER)`)
	exec(t, db, `INSERT INTO shortcutinfo VALUES (1, 1)`)
	exec(t, db, `CREATE TABLE shortcutmenu (_PW_ID INTEGER PRIMARY KEY AUTOINCREMENT, menucommandtype TEXT, menucommand TEXT, shortcut TEXT, modifier INTEGER, extra TEXT DEFAULT 'x')`)
	for _, r := range rows {
		exec(t, db, `INSERT INTO shortcutmenu (menucommandtype, menucommand, shortcut, modifier) VALUES (?,?,?,?)`,
			r.Type, r.Cmd, r.Key, r.Mod)
	}
	closeDB(t, db)
	return path
}

// MakeTODB creates an EditImageTool.todb (UTF-16le, root uuid 1) in dir and
// returns its path.
func MakeTODB(t *testing.T, dir string, nodes []ToolRow) string {
	t.Helper()
	path := filepath.Join(dir, "Tool", "EditImageTool.todb")
	db := openNew(t, path, "PRAGMA encoding='UTF-16le'")
	exec(t, db, `CREATE TABLE Manager (ManagerId INTEGER, RootNodeUuid BLOB, SavedCount INTEGER, Note TEXT)`)
	exec(t, db, `INSERT INTO Manager VALUES (1, ?, 7, 'tools')`, UID(1))
	exec(t, db, `CREATE TABLE Node (NodeUuid BLOB, NodeName TEXT, NodeFirstChildUuid BLOB, NodeNextUuid BLOB, NodeShortCutKey INTEGER, NodeVariantId INTEGER, NodeBlob BLOB, NodeScore REAL)`)
	for _, n := range nodes {
		var first, next any
		if n.First != 0 {
			first = UID(n.First)
		}
		if n.Next != 0 {
			next = UID(n.Next)
		}
		exec(t, db, `INSERT INTO Node VALUES (?,?,?,?,?,?,?,?)`,
			UID(n.ID), n.Name, first, next, n.Key, int(n.ID)*10, []byte{1, 2, n.ID}, float64(n.ID)/3)
	}
	closeDB(t, db)
	return path
}

// MakeAppConfig creates Preference\Config.sqlite in dir with a Tool table
// whose UseToolShift is v, and returns its path.
func MakeAppConfig(t *testing.T, dir string, v int) string {
	t.Helper()
	path := filepath.Join(dir, "Preference", "Config.sqlite")
	db := openNew(t, path, "")
	exec(t, db, `CREATE TABLE Tool (_PW_ID INTEGER, UseToolShift INTEGER, ToolShiftTime INTEGER)`)
	exec(t, db, `INSERT INTO Tool VALUES (1, ?, 500)`, v)
	closeDB(t, db)
	return path
}

// SampleKHC is a small realistic set of commands.
func SampleKHC() []KHCRow {
	return []KHCRow{
		{"basiccommand", "filenew", "N", 4},
		{"basiccommand", "undo", "Z", 4},
		{"basiccommand", "undo", "Z", 5}, // Ctrl+Alt+Z
		{"advancedcommand", "layermergevisiblelayer", "NULL", 0},
		{"basiccommand", "swapcolor", "X", 0},
		{"basiccommand", "weird", "A", 8},
		{"basiccommand", "symbols", "[", 4},
	}
}

// SampleTools is a tool tree:
//
//	1 root
//	2 Pen (P)                     level 1
//	  3 Pencils (group)           level 2
//	    4 Pencil (P)              level 3
//	    5 Mechanical (P)          level 3
//	  6 Inks (group)              level 2
//	    7 G-pen (none)            level 3
//	8 Eraser (E)                  level 1
//	  9 Soft (group)              level 2
//	    10 Eraser soft (code 40)  level 3
func SampleTools() []ToolRow {
	return []ToolRow{
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

func openNew(t *testing.T, path, pragma string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if pragma != "" {
		exec(t, db, pragma)
	}
	return db
}

func exec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func closeDB(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}
