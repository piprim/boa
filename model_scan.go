package boa

import (
	"context"
	"reflect"

	"github.com/jackc/pgx/v5"

	"github.com/piprim/boa/schema"
)

type scanModel struct {
	db *DB

	dest      []any
	scanIndex int
}

var _ Model = (*scanModel)(nil)

func newScanModel(db *DB, dest []any) *scanModel {
	return &scanModel{
		db:   db,
		dest: dest,
	}
}

func (m *scanModel) Value() any {
	return m.dest
}

func (m *scanModel) ScanRows(ctx context.Context, rows pgx.Rows) (int, error) {
	if !rows.Next() {
		return 0, rows.Err()
	}

	dest := makeDest(m, len(m.dest))

	m.scanIndex = 0
	if err := rows.Scan(dest...); err != nil {
		return 0, err
	}

	return 1, nil
}

func (m *scanModel) ScanRow(ctx context.Context, rows pgx.Rows) error {
	return rows.Scan(m.dest...)
}

func (m *scanModel) Scan(src any) error {
	dest := reflect.ValueOf(m.dest[m.scanIndex])
	m.scanIndex++

	scanner := schema.Scanner(dest.Type())
	return scanner(dest, src)
}
