package pgcrud

import (
	"fmt"
	"reflect"
	"strconv"

	"github.com/piprim/pgcrud/dialect/feature"
	"github.com/piprim/pgcrud/dialect/sqltype"
	"github.com/piprim/pgcrud/schema"
)

// ValuesQuery builds VALUES clauses that can be used as subqueries.
type ValuesQuery struct {
	baseQuery
	setQuery

	withOrder bool
	comment   string
}

var (
	_ Query                   = (*ValuesQuery)(nil)
	_ schema.NamedArgAppender = (*ValuesQuery)(nil)
)

// NewValuesQuery creates a VALUES query for the given model.
func NewValuesQuery(db *DB, model any) *ValuesQuery {
	q := &ValuesQuery{
		baseQuery: baseQuery{
			db: db,
		},
	}
	q.setModel(model)
	return q
}

func (q *ValuesQuery) Err(err error) *ValuesQuery {
	q.setErr(err)
	return q
}

func (q *ValuesQuery) Column(columns ...string) *ValuesQuery {
	for _, column := range columns {
		q.addColumn(schema.UnsafeIdent(column))
	}
	return q
}

// Value overwrites model value for the column.
func (q *ValuesQuery) Value(column string, expr string, args ...any) *ValuesQuery {
	if q.table == nil {
		q.setErr(errNilModel)
		return q
	}
	q.addValue(q.table, column, expr, args)
	return q
}

func (q *ValuesQuery) OmitZero() *ValuesQuery {
	q.omitZero = true
	return q
}

func (q *ValuesQuery) WithOrder() *ValuesQuery {
	q.withOrder = true
	return q
}

// Comment adds a comment to the query, wrapped by /* ... */.
func (q *ValuesQuery) Comment(comment string) *ValuesQuery {
	q.comment = comment
	return q
}

func (q *ValuesQuery) AppendNamedArg(gen schema.QueryGen, b []byte, name string) ([]byte, bool) {
	switch name {
	case "Columns":
		bb, err := q.AppendColumns(gen, b)
		if err != nil {
			q.setErr(err)
			return b, true
		}
		return bb, true
	}
	return b, false
}

// AppendColumns appends the table columns. It is used by CTE.
func (q *ValuesQuery) AppendColumns(gen schema.QueryGen, b []byte) (_ []byte, err error) {
	if q.err != nil {
		return nil, q.err
	}
	if q.model == nil {
		return nil, errNilModel
	}

	if q.tableModel != nil {
		fields, err := q.getFields()
		if err != nil {
			return nil, err
		}

		b = appendColumns(b, "", fields)

		if q.withOrder {
			b = append(b, ", _order"...)
		}

		return b, nil
	}

	switch model := q.model.(type) {
	case *mapSliceModel:
		return model.appendColumns(gen, b)
	}

	return nil, fmt.Errorf("pgcrud: Values does not support %T", q.model)
}

func (q *ValuesQuery) Operation() string {
	return "VALUES"
}

func (q *ValuesQuery) AppendQuery(gen schema.QueryGen, b []byte) (_ []byte, err error) {
	if q.err != nil {
		return nil, q.err
	}
	if q.model == nil {
		return nil, errNilModel
	}

	b = appendComment(b, q.comment)

	gen = formatterWithModel(gen, q)

	b = append(b, "VALUES "...)
	if q.db.HasFeature(feature.ValuesRow) {
		b = append(b, "ROW("...)
	} else {
		b = append(b, '(')
	}

	switch model := q.model.(type) {
	case *structTableModel:
		fields, err := q.getFields()
		if err != nil {
			return nil, err
		}

		b, err = q.appendValues(gen, b, fields, model.strct)
		if err != nil {
			return nil, err
		}

		if q.withOrder {
			b = append(b, ", "...)
			b = strconv.AppendInt(b, 0, 10)
		}

	case *sliceTableModel:
		fields, err := q.getFields()
		if err != nil {
			return nil, err
		}

		sliceLen := model.slice.Len()
		for i := range sliceLen {
			if i > 0 {
				b = append(b, "), "...)
				if q.db.HasFeature(feature.ValuesRow) {
					b = append(b, "ROW("...)
				} else {
					b = append(b, '(')
				}
			}

			b, err = q.appendValues(gen, b, fields, model.slice.Index(i))
			if err != nil {
				return nil, err
			}

			if q.withOrder {
				b = append(b, ", "...)
				b = strconv.AppendInt(b, int64(i), 10)
			}
		}

	case *mapSliceModel:
		b, err = model.appendValues(gen, b)
		if err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("pgcrud: Values does not support %T", model)
	}

	b = append(b, ')')
	return b, nil
}

func (q *ValuesQuery) appendValues(
	gen schema.QueryGen, b []byte, fields []*schema.Field, strct reflect.Value,
) (_ []byte, err error) {
	isTemplate := gen.IsNop()
	for i, f := range fields {
		if i > 0 {
			b = append(b, ", "...)
		}

		app, ok := q.modelValues[f.Name]
		switch {
		case ok:
			b, err = app.AppendQuery(gen, b)
			if err != nil {
				return nil, err
			}
		case isTemplate:
			b = append(b, '?')
		default:
			b = f.AppendValue(gen, b, indirect(strct))
		}

		// The cast is what types the column of a VALUES list, since a bound
		// value carries no type of its own.
		if gen.HasFeature(feature.DoubleColonCast) && f.UserSQLType != "" {
			b = append(b, "::"...)
			b = append(b, f.UserSQLType...)
		}
	}
	return b, nil
}

func (q *ValuesQuery) appendSet(gen schema.QueryGen, b []byte) (_ []byte, err error) {
	switch model := q.model.(type) {
	case *mapModel:
		return model.appendSet(gen, b), nil
	case *structTableModel:
		fields, err := q.getDataFields()
		if err != nil {
			return nil, err
		}
		return q.appendSetStruct(gen, b, model, fields)
	default:
		return nil, fmt.Errorf("pgcrud: SetValues(unsupported %T)", model)
	}
}

// Build renders the query and returns the SQL with $n placeholders together
// with the values bound to them. The query must not be modified while
// rendering, so repeated calls return identical results.
func (q *ValuesQuery) Build() (string, []any, error) {
	return q.db.build(q)
}

// String returns the SQL with $n placeholders. It panics on a render error.
func (q *ValuesQuery) String() string {
	sql, _, err := q.Build()
	if err != nil {
		panic(err)
	}
	return sql
}

// Args returns the values bound to the placeholders of String. It panics on
// a render error.
func (q *ValuesQuery) Args() []any {
	_, args, err := q.Build()
	if err != nil {
		panic(err)
	}
	return args
}

// valuesCast returns the SQL type a VALUES list casts a map value to, derived
// from the value's Go type, so Postgres can type the column of the list. It
// is empty when the type is unknown or the value is nil.
func valuesCast(v any) string {
	if v == nil {
		return ""
	}
	typ := schema.DiscoverSQLType(reflect.TypeOf(v))
	if typ == sqltype.Blob {
		return "BYTEA"
	}
	return typ
}
