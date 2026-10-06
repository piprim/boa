package boa_test

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// cols builds field descriptions for the given column names.
func cols(names ...string) []pgconn.FieldDescription {
	fds := make([]pgconn.FieldDescription, len(names))
	for i, n := range names {
		fds[i] = pgconn.FieldDescription{Name: n}
	}
	return fds
}

// fakeRows is an in-memory pgx.Rows. Scan hands each value to the
// destination the way pgx does for sql.Scanner targets.
type fakeRows struct {
	fds    []pgconn.FieldDescription
	data   [][]any
	idx    int
	closed bool
	err    error
}

func newFakeRows(fds []pgconn.FieldDescription, data ...[]any) *fakeRows {
	return &fakeRows{fds: fds, data: data}
}

func (r *fakeRows) Close()                                       { r.closed = true }
func (r *fakeRows) Err() error                                   { return r.err }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return r.fds }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }

func (r *fakeRows) CommandTag() pgconn.CommandTag {
	return pgconn.NewCommandTag(fmt.Sprintf("SELECT %d", len(r.data)))
}

func (r *fakeRows) Next() bool {
	if r.idx >= len(r.data) {
		return false
	}
	r.idx++
	return true
}

func (r *fakeRows) Values() ([]any, error) {
	return r.data[r.idx-1], nil
}

func (r *fakeRows) Scan(dest ...any) error {
	return scanRow(r.data[r.idx-1], dest)
}

func scanRow(row []any, dest []any) error {
	if len(dest) != len(row) {
		return fmt.Errorf("fakeRows: %d dest for %d values", len(dest), len(row))
	}
	for i, d := range dest {
		switch d := d.(type) {
		case sql.Scanner:
			if err := d.Scan(row[i]); err != nil {
				return err
			}
		case *int64:
			*d = row[i].(int64)
		case *bool:
			*d = row[i].(bool)
		case *string:
			*d = row[i].(string)
		default:
			return fmt.Errorf("fakeRows: unsupported dest %T", d)
		}
	}
	return nil
}

type fakeRow struct{ rows *fakeRows }

func (r fakeRow) Scan(dest ...any) error {
	if !r.rows.Next() {
		return pgx.ErrNoRows
	}
	// A QueryRow caller such as Count scans a single scalar; like the two-query
	// ScanAndCount test, it reads column 0 of whatever canned row is served.
	row := r.rows.data[r.rows.idx-1]
	if len(dest) < len(row) {
		row = row[:len(dest)]
	}
	return scanRow(row, dest)
}

type call struct {
	method string
	sql    string
	args   []any
}

// fakeExecutor records calls and replays canned results.
type fakeExecutor struct {
	calls []call
	rows  *fakeRows
	tag   pgconn.CommandTag
	err   error
}

func (e *fakeExecutor) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	e.calls = append(e.calls, call{"Exec", sql, args})
	return e.tag, e.err
}

func (e *fakeExecutor) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	e.calls = append(e.calls, call{"Query", sql, args})
	if e.err != nil {
		return nil, e.err
	}
	return e.rows, nil
}

func (e *fakeExecutor) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	e.calls = append(e.calls, call{"QueryRow", sql, args})
	if e.rows != nil {
		e.rows.idx = 0 // a fresh row set for each QueryRow, so one fake can serve fetch and count
	}
	return fakeRow{rows: e.rows}
}
