package csp

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// fileURI builds the SQLite URI for path with the given query.
func fileURI(path, query string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // drive letter: file:///D:/x
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: query}
	return u.String(), nil
}

// openRO opens path read-only and immutable.
func openRO(path string) (*sql.DB, error) {
	uri, err := fileURI(path, "mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	return openURI(uri)
}

// openRW opens an existing file for reading and writing. It never creates it.
func openRW(path string) (*sql.DB, error) {
	uri, err := fileURI(path, "mode=rw")
	if err != nil {
		return nil, err
	}
	return openURI(uri)
}

func openURI(uri string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// querier is the read side of *sql.DB and *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// tableColumns returns the lower-cased column names of table, or nil when the
// table does not exist. The table name is a trusted constant.
func tableColumns(ctx context.Context, q querier, table string) ([]string, error) {
	rows, err := q.QueryContext(ctx, "SELECT name FROM pragma_table_info('"+table+"')")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var cols []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		cols = append(cols, strings.ToLower(n))
	}
	return cols, rows.Err()
}

// requireColumns fails with ErrUnknownFormat unless table has every column.
func requireColumns(ctx context.Context, q querier, table string, want ...string) error {
	cols, err := tableColumns(ctx, q, table)
	if err != nil {
		return err
	}
	if cols == nil {
		return fmt.Errorf("%w: missing table %s", ErrUnknownFormat, table)
	}
	for _, w := range want {
		if !slices.Contains(cols, strings.ToLower(w)) {
			return fmt.Errorf("%w: table %s has no column %s", ErrUnknownFormat, table, w)
		}
	}
	return nil
}

// integrityCheck runs PRAGMA integrity_check and fails unless it says "ok".
func integrityCheck(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var msgs []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return err
		}
		msgs = append(msgs, s)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(msgs) != 1 || msgs[0] != "ok" {
		return fmt.Errorf("csp: integrity_check failed: %s", strings.Join(msgs, "; "))
	}
	return nil
}
