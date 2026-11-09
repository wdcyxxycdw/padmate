package csp

import (
	"context"
	"database/sql"
	"errors"
)

// ReadUseToolShift reads Tool.UseToolShift from the user's
// Preference\Config.sqlite at path: whether holding a tool's single key
// switches to the tool temporarily. The file is opened read-only and
// immutable, so the caller passes a copy. A missing table, column or row is
// (false, nil): the preference is only a hint.
func ReadUseToolShift(path string) (bool, error) {
	db, err := openRO(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = db.Close() }()
	cols, err := tableColumns(context.Background(), db, "Tool")
	if err != nil {
		return false, err
	}
	found := false
	for _, c := range cols {
		found = found || c == "usetoolshift"
	}
	if !found {
		return false, nil
	}
	var v sql.NullInt64
	err = db.QueryRowContext(context.Background(), "SELECT UseToolShift FROM Tool LIMIT 1").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return v.Int64 == 1, nil
}
