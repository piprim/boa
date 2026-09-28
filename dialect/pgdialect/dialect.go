package pgdialect

import (
	"strings"

	"github.com/piprim/pgcrud/dialect/sqltype"
	"github.com/piprim/pgcrud/schema"
)

// Dialect holds the Postgres table registry and the binding options.
type Dialect struct {
	tables    *schema.Tables
	uintAsInt bool
}

func New(opts ...DialectOption) *Dialect {
	d := new(Dialect)
	d.tables = schema.NewTables(onTable)

	for _, opt := range opts {
		opt(d)
	}

	return d
}

type DialectOption func(d *Dialect)

func WithAppendUintAsInt(on bool) DialectOption {
	return func(d *Dialect) {
		d.uintAsInt = on
	}
}

func (d *Dialect) Tables() *schema.Tables {
	return d.tables
}

// UintAsInt reports whether WithAppendUintAsInt is on: unsigned values are
// bound as the signed type of the same width, wrapping on overflow.
func (d *Dialect) UintAsInt() bool {
	return d.uintAsInt
}

// onTable attaches the array and hstore codecs to the fields that need them.
func onTable(table *schema.Table) {
	for _, field := range table.FieldMap {
		onField(field)
	}
}

func onField(field *schema.Field) {
	field.DiscoveredSQLType = fieldSQLType(field)

	if field.Tag.HasOption("array") || strings.HasSuffix(field.UserSQLType, "[]") {
		field.Append = arrayAppender(field.StructField.Type)
		field.Scan = arrayScanner(field.StructField.Type)
		return
	}

	if field.Tag.HasOption("multirange") || strings.HasSuffix(field.UserSQLType, "multirange") {
		field.Scan = arrayScanner(field.StructField.Type)
		return
	}

	if field.DiscoveredSQLType == sqltype.HSTORE {
		field.Append = hstoreAppender(field.StructField.Type)
		field.Scan = hstoreScanner(field.StructField.Type)
	}
}
