package schema

import (
	"github.com/piprim/pgcrud/dialect"
	"github.com/piprim/pgcrud/dialect/feature"
)

type Dialect interface {
	Name() dialect.Name
	Features() feature.Feature

	Tables() *Tables
	OnTable(table *Table)

	IdentQuote() byte

	// AppendSequence adds the appropriate instruction for the driver to create a sequence
	// from which (autoincremented) values for the column will be generated.
	AppendSequence(b []byte, t *Table, f *Field) []byte

	// DefaultVarcharLen should be returned for dialects in which specifying VARCHAR length
	// is mandatory in queries that modify the schema (CREATE TABLE / ADD COLUMN, etc).
	// Dialects that do not have such requirement may return 0, which should be interpreted so by the caller.
	DefaultVarcharLen() int

	// DefaultSchema should returns the name of the default database schema.
	DefaultSchema() string
}

// ------------------------------------------------------------------------------

type nopDialect struct {
	tables   *Tables
	features feature.Feature
}

func newNopDialect() *nopDialect {
	d := new(nopDialect)
	d.tables = NewTables(d)
	d.features = feature.Returning
	return d
}

func (d *nopDialect) Name() dialect.Name {
	return dialect.Invalid
}

func (d *nopDialect) Features() feature.Feature {
	return d.features
}

func (d *nopDialect) Tables() *Tables {
	return d.tables
}

func (d *nopDialect) OnField(field *Field) {}

func (d *nopDialect) OnTable(table *Table) {}

func (d *nopDialect) IdentQuote() byte {
	return '"'
}

func (d *nopDialect) DefaultVarcharLen() int {
	return 0
}

func (d *nopDialect) AppendSequence(b []byte, _ *Table, _ *Field) []byte {
	return b
}

func (d *nopDialect) DefaultSchema() string {
	return "nop"
}
