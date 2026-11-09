package csp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// nullShortcut is the literal text (not SQL NULL) of a row without shortcut.
const nullShortcut = "NULL"

// CommandRow is one row of the shortcutmenu table.
type CommandRow struct {
	// RowID is _PW_ID.
	RowID int64
	// Type is menucommandtype ("basiccommand" or "advancedcommand"), Command is
	// menucommand. Both are opaque.
	Type, Command string
	// Key is the CSP key name, or "NULL" for no shortcut.
	Key string
	// Modifier is the raw modifier bitmask.
	Modifier int
}

// ID returns the command identity "<type>/<command>".
func (r CommandRow) ID() string { return CommandID(r.Type, r.Command) }

// IsNull reports whether the row means "no shortcut".
func (r CommandRow) IsNull() bool { return r.Key == nullShortcut }

// Supported reports whether the modifier bitmask uses only Alt, Shift and Ctrl.
func (r CommandRow) Supported() bool { return r.Modifier >= 0 && r.Modifier&^modMask == 0 }

// CommandID joins a command type and name into the identity used everywhere:
// "advancedcommand/layermergevisiblelayer".
func CommandID(typ, command string) string { return typ + "/" + command }

// Shortcut is one shortcut of a command.
type Shortcut struct {
	// RowID is the _PW_ID of its row.
	RowID int64
	// Key is the CSP key name and Modifier the raw bitmask.
	Key      string
	Modifier int
	// Chord is the padmate chord; it is valid only when Status is KeyOK.
	Chord  keys.Chord
	Status KeyStatus
	// Unsupported reports a modifier bitmask with bits beyond Alt, Shift and
	// Ctrl. Such a row is kept but cannot be converted or edited.
	Unsupported bool
}

// Command is one menu command or other command, with all its shortcuts in row
// order.
type Command struct {
	// ID is "<type>/<command>".
	ID            string
	Type, Command string
	// Shortcuts are the non-NULL rows in _PW_ID order. Empty when the command
	// has no shortcut.
	Shortcuts []Shortcut
	// NullRowID is the _PW_ID of the command's 'NULL' row, or 0 if none.
	NullRowID int64
}

// Chords returns the chords of the shortcuts that converted (KeyOK).
func (c *Command) Chords() []keys.Chord {
	var out []keys.Chord
	for _, s := range c.Shortcuts {
		if s.Status == KeyOK {
			out = append(out, s.Chord)
		}
	}
	return out
}

// CommandSet is the content of a default.khc.
type CommandSet struct {
	// Info is shortcutinfo.UseMultilingualSupportingCommandShortcut (1 in the
	// files seen), or 0 when the table has no row.
	Info int64
	// Rows are all rows in _PW_ID order.
	Rows []CommandRow
	// Commands are grouped per command, ordered by the first row of each.
	Commands []*Command
	// Unsupported are the rows with modifier bits beyond 1|2|4.
	Unsupported []CommandRow

	byID map[string]*Command
}

// Command returns the command with the given identity, or nil.
func (s *CommandSet) Command(id string) *Command { return s.byID[id] }

var khcRequiredColumns = []string{"_PW_ID", "menucommandtype", "menucommand", "shortcut", "modifier"}

// validateKHC checks the tables and columns this package needs.
func validateKHC(ctx context.Context, q querier) error {
	if err := requireColumns(ctx, q, "shortcutinfo", "UseMultilingualSupportingCommandShortcut"); err != nil {
		return err
	}
	return requireColumns(ctx, q, "shortcutmenu", khcRequiredColumns...)
}

// ReadKHC reads the command shortcuts from the default.khc at path. The file is
// opened read-only and immutable. A missing table or column is reported as
// ErrUnknownFormat.
func ReadKHC(path string) (*CommandSet, error) {
	db, err := openRO(path)
	if err != nil {
		return nil, fmt.Errorf("csp: open khc: %w", err)
	}
	defer func() { _ = db.Close() }()
	return readKHC(context.Background(), db)
}

func readKHC(ctx context.Context, db *sql.DB) (*CommandSet, error) {
	if err := validateKHC(ctx, db); err != nil {
		return nil, err
	}
	set := &CommandSet{byID: map[string]*Command{}}

	var info sql.NullInt64
	err := db.QueryRowContext(ctx,
		"SELECT UseMultilingualSupportingCommandShortcut FROM shortcutinfo LIMIT 1").Scan(&info)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("csp: read shortcutinfo: %w", err)
	}
	set.Info = info.Int64

	rows, err := db.QueryContext(ctx,
		"SELECT _PW_ID, menucommandtype, menucommand, shortcut, modifier FROM shortcutmenu ORDER BY _PW_ID")
	if err != nil {
		return nil, fmt.Errorf("csp: read shortcutmenu: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			id       sql.NullInt64
			typ, cmd sql.NullString
			key      sql.NullString
			mod      sql.NullInt64
		)
		if err := rows.Scan(&id, &typ, &cmd, &key, &mod); err != nil {
			return nil, fmt.Errorf("csp: read shortcutmenu row: %w", err)
		}
		r := CommandRow{RowID: id.Int64, Type: typ.String, Command: cmd.String, Key: key.String, Modifier: int(mod.Int64)}
		if !key.Valid {
			r.Key = nullShortcut
		}
		set.add(r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("csp: read shortcutmenu: %w", err)
	}
	return set, nil
}

func (s *CommandSet) add(r CommandRow) {
	s.Rows = append(s.Rows, r)
	cmd := s.byID[r.ID()]
	if cmd == nil {
		cmd = &Command{ID: r.ID(), Type: r.Type, Command: r.Command}
		s.byID[cmd.ID] = cmd
		s.Commands = append(s.Commands, cmd)
	}
	if r.IsNull() {
		if cmd.NullRowID == 0 {
			cmd.NullRowID = r.RowID
		}
		return
	}
	sc := Shortcut{RowID: r.RowID, Key: r.Key, Modifier: r.Modifier}
	if r.Supported() {
		sc.Chord, sc.Status = ParseCSPKey(r.Key, r.Modifier)
	} else {
		sc.Unsupported = true
		sc.Status = KeyUnsupported
		s.Unsupported = append(s.Unsupported, r)
	}
	cmd.Shortcuts = append(cmd.Shortcuts, sc)
}
