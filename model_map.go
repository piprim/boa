package boa

import (
	"bytes"
	"context"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/piprim/boa/schema"
)

type mapModel struct {
	db *DB

	dest *map[string]any
	m    map[string]any

	columns   []string
	scanIndex int
}

var _ Model = (*mapModel)(nil)

func newMapModel(db *DB, dest *map[string]any) *mapModel {
	m := &mapModel{
		db:   db,
		dest: dest,
	}
	if dest != nil {
		m.m = *dest
	}
	return m
}

func (m *mapModel) Value() any {
	return m.dest
}

func (m *mapModel) ScanRows(ctx context.Context, rows pgx.Rows) (int, error) {
	if !rows.Next() {
		return 0, rows.Err()
	}

	m.columns = columnNames(rows)
	dest := makeDest(m, len(m.columns))

	if m.m == nil {
		m.m = make(map[string]any, len(m.columns))
	}

	m.scanIndex = 0
	if err := rows.Scan(dest...); err != nil {
		return 0, err
	}

	*m.dest = m.m

	return 1, nil
}

// Scan stores the value pgx decoded for the current column. pgx hands typed Go
// values (int64, float64, bool, time.Time, string, []byte) to sql.Scanner
// destinations, so no further conversion is needed. Byte slices are cloned
// because they may alias a buffer pgx reuses.
func (m *mapModel) Scan(src any) error {
	if b, ok := src.([]byte); ok {
		src = bytes.Clone(b)
	}
	return m.scanRaw(src)
}

func (m *mapModel) scanRaw(src any) error {
	columnName := m.columns[m.scanIndex]
	m.scanIndex++
	m.m[columnName] = src
	return nil
}

func (m *mapModel) appendColumnsValues(gen schema.QueryGen, b []byte) []byte {
	keys := make([]string, 0, len(m.m))

	for k := range m.m {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	b = append(b, " ("...)

	for i, k := range keys {
		if i > 0 {
			b = append(b, ", "...)
		}
		b = gen.AppendIdent(b, k)
	}

	b = append(b, ") VALUES ("...)

	isTemplate := gen.IsNop()
	for i, k := range keys {
		if i > 0 {
			b = append(b, ", "...)
		}
		if isTemplate {
			b = append(b, '?')
		} else {
			b = gen.Append(b, m.m[k])
		}
	}

	b = append(b, ")"...)

	return b
}

func (m *mapModel) appendSet(gen schema.QueryGen, b []byte) []byte {
	keys := make([]string, 0, len(m.m))

	for k := range m.m {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	isTemplate := gen.IsNop()
	for i, k := range keys {
		if i > 0 {
			b = append(b, ", "...)
		}

		b = gen.AppendIdent(b, k)
		b = append(b, " = "...)
		if isTemplate {
			b = append(b, '?')
		} else {
			b = gen.Append(b, m.m[k])
		}
	}

	return b
}

func makeDest(v any, n int) []any {
	dest := make([]any, n)
	for i := range dest {
		dest[i] = v
	}
	return dest
}
