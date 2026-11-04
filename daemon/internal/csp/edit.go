package csp

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Errors of the edit functions. They wrap with the detail.
var (
	// ErrUnknownCommand: the command has no row in shortcutmenu.
	ErrUnknownCommand = errors.New("csp: unknown command")
	// ErrUnknownTool: no node has the UUID.
	ErrUnknownTool = errors.New("csp: unknown tool")
	// ErrDuplicateShortcut: another row already has the (key, modifier).
	ErrDuplicateShortcut = errors.New("csp: shortcut already in use in the khc")
	// ErrNoSuchShortcut: the command does not have the shortcut to remove.
	ErrNoSuchShortcut = errors.New("csp: command does not have that shortcut")
	// ErrBadEdit: the edit is malformed (unknown key name, modifier bits
	// outside Alt|Shift|Ctrl, tool code outside 0-26, bad UUID).
	ErrBadEdit = errors.New("csp: invalid edit")
)

// CommandEditOp is the operation of a CommandEdit.
type CommandEditOp uint8

// Command edit operations.
const (
	opAdd CommandEditOp = iota + 1
	opRemove
)

// CommandEdit adds or removes one shortcut of one command.
type CommandEdit struct {
	Op CommandEditOp
	// Command is the identity "<type>/<command>".
	Command string
	// Key is a CSP key name and Modifier a bitmask of 1 Alt, 2 Shift, 4 Ctrl.
	Key      string
	Modifier int
}

// AddShortcut returns an edit that gives command the shortcut.
func AddShortcut(command, key string, modifier int) CommandEdit {
	return CommandEdit{Op: opAdd, Command: command, Key: key, Modifier: modifier}
}

// RemoveShortcut returns an edit that takes the shortcut away from command.
func RemoveShortcut(command, key string, modifier int) CommandEdit {
	return CommandEdit{Op: opRemove, Command: command, Key: key, Modifier: modifier}
}

// splitCommandID splits "<type>/<command>" at the first slash. The command part
// may itself contain slashes in principle, the type never does.
func splitCommandID(id string) (typ, cmd string, ok bool) {
	typ, cmd, ok = strings.Cut(id, "/")
	return typ, cmd, ok && typ != "" && cmd != ""
}

// ApplyCommandEdits applies edits in order to the default.khc at path in one
// transaction, then runs PRAGMA integrity_check. Any failure rolls the whole
// transaction back. path must be a copy: the caller handles backup, locking
// and atomic replacement.
//
// Rules: adding to a command whose only row is 'NULL' fills that row in place;
// adding to a command that already has shortcuts inserts a new row; removing
// the last shortcut sets its row to 'NULL', 0 instead of deleting; removing one
// of several deletes its row. An edit that would give two non-NULL rows the
// same (shortcut, modifier) is refused, as is an unknown command.
func ApplyCommandEdits(path string, edits []CommandEdit) error {
	ctx := context.Background()
	db, err := openRW(path)
	if err != nil {
		return fmt.Errorf("csp: open khc: %w", err)
	}
	defer func() { _ = db.Close() }()
	if err := validateKHC(ctx, db); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for i, e := range edits {
		if err := applyCommandEdit(ctx, tx, e); err != nil {
			return fmt.Errorf("edit %d (%s): %w", i, e.Command, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return integrityCheck(ctx, db)
}

func applyCommandEdit(ctx context.Context, tx *sql.Tx, e CommandEdit) error {
	typ, cmd, ok := splitCommandID(e.Command)
	if !ok {
		return fmt.Errorf("%w: bad command id %q", ErrBadEdit, e.Command)
	}
	key := strings.ToUpper(e.Key)
	if st := KeyNameStatus(key); st == KeyUnknown || st == KeyNone {
		return fmt.Errorf("%w: unknown key name %q", ErrBadEdit, e.Key)
	}
	if e.Modifier < 0 || e.Modifier&^modMask != 0 {
		return fmt.Errorf("%w: modifier %d", ErrBadEdit, e.Modifier)
	}
	rows, err := commandRows(ctx, tx, typ, cmd)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return ErrUnknownCommand
	}
	switch e.Op {
	case opAdd:
		return addRow(ctx, tx, typ, cmd, key, e.Modifier, rows)
	case opRemove:
		return removeRow(ctx, tx, key, e.Modifier, rows)
	}
	return fmt.Errorf("%w: operation %d", ErrBadEdit, e.Op)
}

func commandRows(ctx context.Context, tx *sql.Tx, typ, cmd string) ([]CommandRow, error) {
	rs, err := tx.QueryContext(ctx,
		"SELECT _PW_ID, shortcut, modifier FROM shortcutmenu WHERE menucommandtype = ? AND menucommand = ? ORDER BY _PW_ID",
		typ, cmd)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []CommandRow
	for rs.Next() {
		var (
			id  int64
			key sql.NullString
			mod sql.NullInt64
		)
		if err := rs.Scan(&id, &key, &mod); err != nil {
			return nil, err
		}
		r := CommandRow{RowID: id, Type: typ, Command: cmd, Key: key.String, Modifier: int(mod.Int64)}
		if !key.Valid {
			r.Key = nullShortcut
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

func addRow(ctx context.Context, tx *sql.Tx, typ, cmd, key string, mod int, rows []CommandRow) error {
	var dup int
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM shortcutmenu WHERE shortcut = ? AND modifier = ?", key, mod).Scan(&dup); err != nil {
		return err
	}
	if dup > 0 {
		return fmt.Errorf("%w: %s modifier %d", ErrDuplicateShortcut, key, mod)
	}
	for _, r := range rows {
		if r.IsNull() {
			_, err := tx.ExecContext(ctx,
				"UPDATE shortcutmenu SET shortcut = ?, modifier = ? WHERE _PW_ID = ?", key, mod, r.RowID)
			return err
		}
	}
	// OPEN MANUAL TEST: the new row gets the next _PW_ID, so it is not
	// consecutive with the other rows of the command. Whether CSP accepts that
	// (it writes a command's rows consecutively) has not been verified yet.
	_, err := tx.ExecContext(ctx,
		"INSERT INTO shortcutmenu (menucommandtype, menucommand, shortcut, modifier) VALUES (?, ?, ?, ?)",
		typ, cmd, key, mod)
	return err
}

func removeRow(ctx context.Context, tx *sql.Tx, key string, mod int, rows []CommandRow) error {
	var target *CommandRow
	live := 0
	for i := range rows {
		if rows[i].IsNull() {
			continue
		}
		live++
		if rows[i].Key == key && rows[i].Modifier == mod {
			target = &rows[i]
		}
	}
	if target == nil {
		return fmt.Errorf("%w: %s modifier %d", ErrNoSuchShortcut, key, mod)
	}
	if live == 1 {
		_, err := tx.ExecContext(ctx,
			"UPDATE shortcutmenu SET shortcut = 'NULL', modifier = 0 WHERE _PW_ID = ?", target.RowID)
		return err
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM shortcutmenu WHERE _PW_ID = ?", target.RowID)
	return err
}

// ToolEdit sets or clears the shortcut key of one tool node.
type ToolEdit struct {
	// UUID is the node UUID, 32 hex characters (either case).
	UUID string
	// Code is the new NodeShortCutKey: 0 clears it, 1-26 is A-Z.
	Code int
}

// SetToolKey returns an edit that gives the node the key code (1-26).
func SetToolKey(uuid string, code int) ToolEdit { return ToolEdit{UUID: uuid, Code: code} }

// ClearToolKey returns an edit that removes the node's shortcut key.
func ClearToolKey(uuid string) ToolEdit { return ToolEdit{UUID: uuid} }

// ApplyToolEdits applies edits in order to the EditImageTool.todb at path in
// one transaction and runs PRAGMA integrity_check. It updates only
// Node.NodeShortCutKey; no other column or table is touched. path must be a
// copy. Sharing a key between nodes is allowed (CSP's cycle shortcut).
func ApplyToolEdits(path string, edits []ToolEdit) error {
	ctx := context.Background()
	db, err := openRW(path)
	if err != nil {
		return fmt.Errorf("csp: open todb: %w", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := validateTODB(ctx, db); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for i, e := range edits {
		if e.Code < 0 || e.Code > 26 {
			return fmt.Errorf("edit %d: %w: key code %d", i, ErrBadEdit, e.Code)
		}
		id, err := hex.DecodeString(e.UUID)
		if err != nil || len(id) != uuidLen {
			return fmt.Errorf("edit %d: %w: uuid %q", i, ErrBadEdit, e.UUID)
		}
		res, err := tx.ExecContext(ctx, "UPDATE Node SET NodeShortCutKey = ? WHERE NodeUuid = ?", e.Code, id)
		if err != nil {
			return fmt.Errorf("edit %d: %w", i, err)
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return fmt.Errorf("edit %d: %w: %s", i, ErrUnknownTool, e.UUID)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return integrityCheck(ctx, db)
}
