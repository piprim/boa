package boa

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/piprim/boa/schema"
)

// InsertQuery builds SQL INSERT statements.
type InsertQuery struct {
	whereBaseQuery
	returningQuery
	customValueQuery

	on schema.QueryWithArgs
	setQuery

	comment string
}

var _ Query = (*InsertQuery)(nil)

// NewInsertQuery returns an InsertQuery tied to the provided DB.
func NewInsertQuery(db *DB) *InsertQuery {
	q := &InsertQuery{
		whereBaseQuery: whereBaseQuery{
			baseQuery: baseQuery{
				db: db,
			},
		},
	}
	return q
}

// Model sets the model whose values will be inserted.
func (q *InsertQuery) Model(model any) *InsertQuery {
	q.setModel(model)
	return q
}

// Err sets an error on the query, causing subsequent operations to fail.
func (q *InsertQuery) Err(err error) *InsertQuery {
	q.setErr(err)
	return q
}

// Apply calls each function in fns, passing the InsertQuery as an argument.
func (q *InsertQuery) Apply(fns ...func(*InsertQuery) *InsertQuery) *InsertQuery {
	for _, fn := range fns {
		if fn != nil {
			q = fn(q)
		}
	}
	return q
}

// With adds a WITH clause (Common Table Expression) to the query.
func (q *InsertQuery) With(name string, query Query) *InsertQuery {
	q.addWith(NewWithQuery(name, query))
	return q
}

// WithRecursive adds a WITH RECURSIVE clause to the query.
func (q *InsertQuery) WithRecursive(name string, query Query) *InsertQuery {
	q.addWith(NewWithQuery(name, query).Recursive())
	return q
}

// WithQuery adds a pre-configured WITH clause to the query.
func (q *InsertQuery) WithQuery(query *WithQuery) *InsertQuery {
	q.addWith(query)
	return q
}

//------------------------------------------------------------------------------

// Table specifies the table(s) to insert into.
func (q *InsertQuery) Table(tables ...string) *InsertQuery {
	for _, table := range tables {
		q.addTable(schema.UnsafeIdent(table))
	}
	return q
}

// TableExpr adds a table expression for the INSERT target with arguments.
func (q *InsertQuery) TableExpr(query string, args ...any) *InsertQuery {
	q.addTable(schema.SafeQuery(query, args))
	return q
}

// ModelTableExpr overrides the table name derived from the model.
func (q *InsertQuery) ModelTableExpr(query string, args ...any) *InsertQuery {
	q.modelTableName = schema.SafeQuery(query, args)
	return q
}

//------------------------------------------------------------------------------

// Column adds columns to the INSERT statement's column list.
func (q *InsertQuery) Column(columns ...string) *InsertQuery {
	for _, column := range columns {
		q.addColumn(schema.UnsafeIdent(column))
	}
	return q
}

// ColumnExpr adds a column expression to the INSERT statement with arguments.
func (q *InsertQuery) ColumnExpr(query string, args ...any) *InsertQuery {
	q.addColumn(schema.SafeQuery(query, args))
	return q
}

// ExcludeColumn excludes specific columns from being inserted.
func (q *InsertQuery) ExcludeColumn(columns ...string) *InsertQuery {
	q.excludeColumn(columns)
	return q
}

// Value overwrites model value for the column.
func (q *InsertQuery) Value(column string, expr string, args ...any) *InsertQuery {
	if q.table == nil {
		q.setErr(errNilModel)
		return q
	}
	q.addValue(q.table, column, expr, args)
	return q
}

// Where adds a WHERE condition for the upsert UPDATE branch, combined with AND.
func (q *InsertQuery) Where(query string, args ...any) *InsertQuery {
	q.addWhere(schema.SafeQueryWithSep(query, args, " AND "))
	return q
}

// WhereOr adds a WHERE condition for the upsert UPDATE branch, combined with OR.
func (q *InsertQuery) WhereOr(query string, args ...any) *InsertQuery {
	q.addWhere(schema.SafeQueryWithSep(query, args, " OR "))
	return q
}

//------------------------------------------------------------------------------

// Returning adds a RETURNING clause to the query.
//
// To suppress the auto-generated RETURNING clause, use `Returning("")`.
func (q *InsertQuery) Returning(query string, args ...any) *InsertQuery {
	q.addReturning(schema.SafeQuery(query, args))
	return q
}

//------------------------------------------------------------------------------

// Ignore skips conflicting rows: INSERT ... ON CONFLICT DO NOTHING.
func (q *InsertQuery) Ignore() *InsertQuery {
	return q.On("CONFLICT DO NOTHING")
}

//------------------------------------------------------------------------------

// Comment adds a comment to the query, wrapped by /* ... */.
func (q *InsertQuery) Comment(comment string) *InsertQuery {
	q.comment = comment
	return q
}

//------------------------------------------------------------------------------

// Operation returns the query operation name ("INSERT").
func (q *InsertQuery) Operation() string {
	return "INSERT"
}

func (q *InsertQuery) AppendQuery(gen schema.QueryGen, b []byte) (_ []byte, err error) {
	if q.err != nil {
		return nil, q.err
	}

	b = appendComment(b, q.comment)

	gen = formatterWithModel(gen, q)

	b, err = q.appendWith(gen, b)
	if err != nil {
		return nil, err
	}

	b = append(b, "INSERT INTO "...)

	if !q.on.IsZero() {
		b, err = q.appendFirstTableWithAlias(gen, b)
	} else {
		b, err = q.appendFirstTable(gen, b)
	}
	if err != nil {
		return nil, err
	}

	b, err = q.appendColumnsValues(gen, b)
	if err != nil {
		return nil, err
	}

	b, err = q.appendOn(gen, b)
	if err != nil {
		return nil, err
	}

	if q.hasReturning() {
		b = append(b, " RETURNING "...)
		b, err = q.appendReturning(gen, b)
		if err != nil {
			return nil, err
		}
	}

	return b, nil
}

func (q *InsertQuery) appendColumnsValues(
	gen schema.QueryGen, b []byte,
) (_ []byte, err error) {
	if q.hasMultiTables() {
		if q.columns != nil {
			b = append(b, " ("...)
			b, err = q.appendColumns(gen, b)
			if err != nil {
				return nil, err
			}
			b = append(b, ")"...)
		}

		b = append(b, " SELECT "...)

		if q.columns != nil {
			b, err = q.appendColumns(gen, b)
			if err != nil {
				return nil, err
			}
		} else {
			b = append(b, "*"...)
		}

		b = append(b, " FROM "...)
		b, err = q.appendOtherTables(gen, b)
		if err != nil {
			return nil, err
		}

		return b, nil
	}

	if m, ok := q.model.(*mapModel); ok {
		return m.appendColumnsValues(gen, b), nil
	}
	if _, ok := q.model.(*mapSliceModel); ok {
		return nil, fmt.Errorf("Insert(*[]map[string]any) is not supported")
	}

	if q.model == nil {
		return nil, errNilModel
	}

	// Build fields to populate RETURNING clause.
	fields, err := q.getFields()
	if err != nil {
		return nil, err
	}

	b = append(b, " ("...)
	b = q.appendFields(gen, b, fields)
	b = append(b, ")"...)

	b = append(b, " VALUES ("...)

	switch model := q.tableModel.(type) {
	case *structTableModel:
		b, err = q.appendStructValues(gen, b, fields, model.strct)
		if err != nil {
			return nil, err
		}
	case *sliceTableModel:
		b, err = q.appendSliceValues(gen, b, fields, model.slice)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("boa: Insert does not support %T", q.tableModel)
	}

	b = append(b, ')')

	return b, nil
}

func (q *InsertQuery) appendStructValues(
	gen schema.QueryGen, b []byte, fields []*schema.Field, strct reflect.Value,
) (_ []byte, err error) {
	isTemplate := gen.IsNop()
	for i, f := range fields {
		if i > 0 {
			b = append(b, ", "...)
		}

		app, ok := q.modelValues[f.Name]
		if ok {
			b, err = app.AppendQuery(gen, b)
			if err != nil {
				return nil, err
			}
			q.addReturningField(f)
			continue
		}

		switch {
		case isTemplate:
			b = append(b, '?')
		case q.marshalsToDefault(f, strct):
			b = append(b, "DEFAULT"...)
			q.addReturningField(f)
		default:
			b = f.AppendValue(gen, b, strct)
		}
	}

	for i, v := range q.extraValues {
		if i > 0 || len(fields) > 0 {
			b = append(b, ", "...)
		}

		b, err = v.value.AppendQuery(gen, b)
		if err != nil {
			return nil, err
		}
	}

	return b, nil
}

func (q *InsertQuery) appendSliceValues(
	gen schema.QueryGen, b []byte, fields []*schema.Field, slice reflect.Value,
) (_ []byte, err error) {
	if gen.IsNop() {
		return q.appendStructValues(gen, b, fields, reflect.Value{})
	}

	sliceLen := slice.Len()
	for i := 0; i < sliceLen; i++ {
		if i > 0 {
			b = append(b, "), ("...)
		}
		el := indirect(slice.Index(i))
		b, err = q.appendStructValues(gen, b, fields, el)
		if err != nil {
			return nil, err
		}
	}

	return b, nil
}

// marshalsToDefault checks if the value will be marshaled as DEFAULT
// when appending it to the VALUES clause in place of the given field.
// An unset field (see schema.Field.IsUnset) is always DEFAULT: Postgres
// resolves it to NULL when the column has no default.
func (q InsertQuery) marshalsToDefault(f *schema.Field, v reflect.Value) bool {
	return (f.IsPtr && f.HasNilValue(v)) ||
		f.HasUnsetValue(v) ||
		(f.HasZeroValue(v) && (f.NullZero || f.SQLDefault != ""))
}

func (q *InsertQuery) appendFields(
	gen schema.QueryGen, b []byte, fields []*schema.Field,
) []byte {
	b = appendColumns(b, "", fields)
	for i, v := range q.extraValues {
		if i > 0 || len(fields) > 0 {
			b = append(b, ", "...)
		}
		b = gen.AppendIdent(b, v.column)
	}
	return b
}

//------------------------------------------------------------------------------

// On adds an ON clause for upsert behavior (e.g., "CONFLICT (id) DO UPDATE").
func (q *InsertQuery) On(s string, args ...any) *InsertQuery {
	q.on = schema.SafeQuery(s, args)
	return q
}

// Set adds a SET expression for the upsert UPDATE branch with arguments.
func (q *InsertQuery) Set(query string, args ...any) *InsertQuery {
	q.addSet(schema.SafeQuery(query, args))
	return q
}

// SetValues attaches a ValuesQuery used to populate SET expressions for the upsert UPDATE branch.
func (q *InsertQuery) SetValues(values *ValuesQuery) *InsertQuery {
	q.setValues = values
	return q
}

func (q *InsertQuery) appendOn(gen schema.QueryGen, b []byte) (_ []byte, err error) {
	if q.on.IsZero() {
		return b, nil
	}

	b = append(b, " ON "...)
	b, err = q.on.AppendQuery(gen, b)
	if err != nil {
		return nil, err
	}

	if len(q.set) > 0 || q.setValues != nil {
		b = append(b, " SET "...)

		b, err = q.appendSet(gen, b)
		if err != nil {
			return nil, err
		}
	} else if q.onConflictDoUpdate() {
		fields, err := q.getDataFields()
		if err != nil {
			return nil, err
		}
		b = q.appendSetExcluded(b, fields)
	}

	if len(q.where) > 0 {
		b = append(b, " WHERE "...)

		b, err = appendWhere(gen, b, q.where)
		if err != nil {
			return nil, err
		}
	}

	return b, nil
}

func (q *InsertQuery) onConflictDoUpdate() bool {
	return strings.HasSuffix(strings.ToUpper(q.on.Query), " DO UPDATE")
}

func (q *InsertQuery) appendSetExcluded(b []byte, fields []*schema.Field) []byte {
	b = append(b, " SET "...)
	for i, f := range fields {
		if i > 0 {
			b = append(b, ", "...)
		}
		b = append(b, f.SQLName...)
		b = append(b, " = EXCLUDED."...)
		b = append(b, f.SQLName...)
	}
	return b
}

//------------------------------------------------------------------------------

// Scan executes the INSERT and scans RETURNING results into dest.
func (q *InsertQuery) Scan(ctx context.Context, dest ...any) error {
	_, err := q.scanOrExec(ctx, dest, true)
	return err
}

// Exec executes the INSERT and optionally scans RETURNING results into dest when provided.
func (q *InsertQuery) Exec(ctx context.Context, dest ...any) (pgconn.CommandTag, error) {
	return q.scanOrExec(ctx, dest, len(dest) > 0)
}

func (q *InsertQuery) scanOrExec(
	ctx context.Context, dest []any, hasDest bool,
) (pgconn.CommandTag, error) {
	if q.err != nil {
		return pgconn.CommandTag{}, q.err
	}

	if q.table != nil {
		if err := q.beforeInsertHook(ctx); err != nil {
			return pgconn.CommandTag{}, err
		}
	}

	// Run append model hooks before generating the query.
	if err := q.beforeAppendModel(ctx, q); err != nil {
		return pgconn.CommandTag{}, err
	}

	// if a comment is propagated via the context, use it
	setCommentFromContext(ctx, q)

	// Generate the query before checking hasReturning.
	query, args, err := q.build(q)
	if err != nil {
		return pgconn.CommandTag{}, q.db.failBuild(ctx, q, q.model, err)
	}

	useScan := hasDest || q.hasReturning()
	var model Model

	if useScan {
		var err error
		model, err = q.getModel(dest)
		if err != nil {
			return pgconn.CommandTag{}, err
		}
	}

	var res pgconn.CommandTag

	if useScan {
		res, err = q.scan(ctx, q, query, args, model, hasDest)
		if err != nil {
			return pgconn.CommandTag{}, err
		}
	} else {
		res, err = q.exec(ctx, q, query, args)
		if err != nil {
			return pgconn.CommandTag{}, err
		}
	}

	if q.table != nil {
		if err := q.afterInsertHook(ctx); err != nil {
			return pgconn.CommandTag{}, err
		}
	}

	return res, nil
}

func (q *InsertQuery) beforeInsertHook(ctx context.Context) error {
	if hook, ok := q.table.ZeroIface.(BeforeInsertHook); ok {
		if err := hook.BeforeInsert(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

func (q *InsertQuery) afterInsertHook(ctx context.Context) error {
	if hook, ok := q.table.ZeroIface.(AfterInsertHook); ok {
		if err := hook.AfterInsert(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// Build renders the query and returns the SQL with $n placeholders together
// with the values bound to them. The query must not be modified while
// rendering, so repeated calls return identical results.
func (q *InsertQuery) Build() (string, []any, error) {
	return q.db.build(q)
}

// String returns the SQL with $n placeholders. It panics on a render error.
func (q *InsertQuery) String() string {
	sql, _, err := q.Build()
	if err != nil {
		panic(err)
	}
	return sql
}

// Args returns the values bound to the placeholders of String. It panics on
// a render error.
func (q *InsertQuery) Args() []any {
	_, args, err := q.Build()
	if err != nil {
		panic(err)
	}
	return args
}
