package pgcrud

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/piprim/pgcrud/dialect/feature"
	"github.com/piprim/pgcrud/internal"
	"github.com/piprim/pgcrud/schema"
)

// DeleteQuery builds SQL DELETE statements.
type DeleteQuery struct {
	whereBaseQuery
	orderLimitOffsetQuery
	returningQuery

	comment string
}

var _ Query = (*DeleteQuery)(nil)

// NewDeleteQuery returns a DeleteQuery associated with the provided DB.
func NewDeleteQuery(db *DB) *DeleteQuery {
	q := &DeleteQuery{
		whereBaseQuery: whereBaseQuery{
			baseQuery: baseQuery{
				db: db,
			},
		},
	}
	return q
}

func (q *DeleteQuery) Model(model any) *DeleteQuery {
	q.setModel(model)
	return q
}

func (q *DeleteQuery) Err(err error) *DeleteQuery {
	q.setErr(err)
	return q
}

// Apply calls each function in fns, passing the DeleteQuery as an argument.
func (q *DeleteQuery) Apply(fns ...func(*DeleteQuery) *DeleteQuery) *DeleteQuery {
	for _, fn := range fns {
		if fn != nil {
			q = fn(q)
		}
	}
	return q
}

func (q *DeleteQuery) With(name string, query Query) *DeleteQuery {
	q.addWith(NewWithQuery(name, query))
	return q
}

func (q *DeleteQuery) WithRecursive(name string, query Query) *DeleteQuery {
	q.addWith(NewWithQuery(name, query).Recursive())
	return q
}

func (q *DeleteQuery) WithQuery(query *WithQuery) *DeleteQuery {
	q.addWith(query)
	return q
}

func (q *DeleteQuery) Table(tables ...string) *DeleteQuery {
	for _, table := range tables {
		q.addTable(schema.UnsafeIdent(table))
	}
	return q
}

func (q *DeleteQuery) TableExpr(query string, args ...any) *DeleteQuery {
	q.addTable(schema.SafeQuery(query, args))
	return q
}

func (q *DeleteQuery) ModelTableExpr(query string, args ...any) *DeleteQuery {
	q.modelTableName = schema.SafeQuery(query, args)
	return q
}

//------------------------------------------------------------------------------

func (q *DeleteQuery) WherePK(cols ...string) *DeleteQuery {
	q.addWhereCols(cols)
	return q
}

func (q *DeleteQuery) Where(query string, args ...any) *DeleteQuery {
	q.addWhere(schema.SafeQueryWithSep(query, args, " AND "))
	return q
}

func (q *DeleteQuery) WhereOr(query string, args ...any) *DeleteQuery {
	q.addWhere(schema.SafeQueryWithSep(query, args, " OR "))
	return q
}

func (q *DeleteQuery) WhereGroup(sep string, fn func(*DeleteQuery) *DeleteQuery) *DeleteQuery {
	saved, savedHasOr := q.where, q.whereHasOr
	q.where, q.whereHasOr = nil, false

	q = fn(q)

	where := q.where
	q.where, q.whereHasOr = saved, savedHasOr

	q.addWhereGroup(sep, where)

	return q
}

func (q *DeleteQuery) WhereDeleted() *DeleteQuery {
	q.whereDeleted()
	return q
}

func (q *DeleteQuery) WhereAllWithDeleted() *DeleteQuery {
	q.whereAllWithDeleted()
	return q
}

func (q *DeleteQuery) Order(orders ...string) *DeleteQuery {
	if !q.hasFeature(feature.DeleteOrderLimit) {
		q.setErr(feature.NewNotSupportError(feature.DeleteOrderLimit))
		return q
	}
	q.addOrder(orders...)
	return q
}

func (q *DeleteQuery) OrderExpr(query string, args ...any) *DeleteQuery {
	if !q.hasFeature(feature.DeleteOrderLimit) {
		q.setErr(feature.NewNotSupportError(feature.DeleteOrderLimit))
		return q
	}
	q.addOrderExpr(query, args...)
	return q
}

func (q *DeleteQuery) ForceDelete() *DeleteQuery {
	q.flags = q.flags.Set(forceDeleteFlag)
	return q
}

// ------------------------------------------------------------------------------
func (q *DeleteQuery) Limit(n int64) *DeleteQuery {
	if !q.hasFeature(feature.DeleteOrderLimit) {
		q.setErr(feature.NewNotSupportError(feature.DeleteOrderLimit))
		return q
	}
	q.setLimit(n)
	return q
}

//------------------------------------------------------------------------------

// Returning adds a RETURNING clause to the query.
//
// To suppress the auto-generated RETURNING clause, use `Returning("NULL")`.
func (q *DeleteQuery) Returning(query string, args ...any) *DeleteQuery {
	if !q.hasFeature(feature.DeleteReturning) {
		q.setErr(feature.NewNotSupportError(feature.DeleteReturning))
		return q
	}

	q.addReturning(schema.SafeQuery(query, args))
	return q
}

//------------------------------------------------------------------------------

// Comment adds a comment to the query, wrapped by /* ... */.
func (q *DeleteQuery) Comment(comment string) *DeleteQuery {
	q.comment = comment
	return q
}

//------------------------------------------------------------------------------

func (q *DeleteQuery) Operation() string {
	return "DELETE"
}

func (q *DeleteQuery) AppendQuery(gen schema.QueryGen, b []byte) (_ []byte, err error) {
	if q.err != nil {
		return nil, q.err
	}

	b = appendComment(b, q.comment)

	gen = formatterWithModel(gen, q)

	if q.isSoftDelete() {
		now := time.Now()

		if err := q.tableModel.updateSoftDeleteField(now); err != nil {
			return nil, err
		}

		upd := &UpdateQuery{
			whereBaseQuery: q.whereBaseQuery,
			returningQuery: q.returningQuery,
		}
		upd.Set(q.softDeleteSet(gen), now)

		return upd.AppendQuery(gen, b)
	}

	withAlias := q.db.HasFeature(feature.DeleteTableAlias)

	b, err = q.appendWith(gen, b)
	if err != nil {
		return nil, err
	}

	b = append(b, "DELETE FROM "...)

	if withAlias {
		b, err = q.appendFirstTableWithAlias(gen, b)
	} else {
		b, err = q.appendFirstTable(gen, b)
	}
	if err != nil {
		return nil, err
	}

	if q.hasMultiTables() {
		b = append(b, " USING "...)
		b, err = q.appendOtherTables(gen, b)
		if err != nil {
			return nil, err
		}
	}

	if q.hasFeature(feature.Output) && q.hasReturning() {
		b = append(b, " OUTPUT "...)
		b, err = q.appendOutput(gen, b)
		if err != nil {
			return nil, err
		}
	}

	b, err = q.mustAppendWhere(gen, b, withAlias)
	if err != nil {
		return nil, err
	}

	if q.hasMultiTables() && (len(q.order) > 0 || q.limit > 0) {
		return nil, errors.New("pgcrud: can't use ORDER or LIMIT with multiple tables")
	}

	b, err = q.appendOrder(gen, b)
	if err != nil {
		return nil, err
	}

	b, err = q.appendLimitOffset(gen, b)
	if err != nil {
		return nil, err
	}

	if q.hasFeature(feature.DeleteReturning) && q.hasReturning() {
		b = append(b, " RETURNING "...)
		b, err = q.appendReturning(gen, b)
		if err != nil {
			return nil, err
		}
	}

	return b, nil
}

func (q *DeleteQuery) isSoftDelete() bool {
	return q.tableModel != nil && q.table.SoftDeleteField != nil && !q.flags.Has(forceDeleteFlag)
}

// softDeleteSet returns the SET expression that marks a row deleted, with a
// placeholder for the timestamp, which the caller passes as the Set argument.
func (q *DeleteQuery) softDeleteSet(gen schema.QueryGen) string {
	b := make([]byte, 0, 32)
	if gen.HasFeature(feature.UpdateMultiTable) {
		b = append(b, q.table.SQLAlias...)
		b = append(b, '.')
	}
	b = append(b, q.table.SoftDeleteField.SQLName...)
	b = append(b, " = ?"...)
	return internal.String(b)
}

//------------------------------------------------------------------------------

func (q *DeleteQuery) Scan(ctx context.Context, dest ...any) error {
	_, err := q.scanOrExec(ctx, dest, true)
	return err
}

func (q *DeleteQuery) Exec(ctx context.Context, dest ...any) (pgconn.CommandTag, error) {
	return q.scanOrExec(ctx, dest, len(dest) > 0)
}

func (q *DeleteQuery) scanOrExec(
	ctx context.Context, dest []any, hasDest bool,
) (pgconn.CommandTag, error) {
	if q.err != nil {
		return pgconn.CommandTag{}, q.err
	}

	if q.table != nil {
		if err := q.beforeDeleteHook(ctx); err != nil {
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

	useScan := hasDest || (q.hasReturning() && q.hasFeature(feature.DeleteReturning|feature.Output))
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
		if err := q.afterDeleteHook(ctx); err != nil {
			return pgconn.CommandTag{}, err
		}
	}

	return res, nil
}

func (q *DeleteQuery) beforeDeleteHook(ctx context.Context) error {
	if hook, ok := q.table.ZeroIface.(BeforeDeleteHook); ok {
		if err := hook.BeforeDelete(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

func (q *DeleteQuery) afterDeleteHook(ctx context.Context) error {
	if hook, ok := q.table.ZeroIface.(AfterDeleteHook); ok {
		if err := hook.AfterDelete(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// Build renders the query and returns the SQL with $n placeholders together
// with the values bound to them. The query must not be modified while
// rendering, so repeated calls return identical results.
func (q *DeleteQuery) Build() (string, []any, error) {
	return q.db.build(q)
}

// String returns the SQL with $n placeholders. It panics on a render error.
func (q *DeleteQuery) String() string {
	sql, _, err := q.Build()
	if err != nil {
		panic(err)
	}
	return sql
}

// Args returns the values bound to the placeholders of String. It panics on
// a render error.
func (q *DeleteQuery) Args() []any {
	_, args, err := q.Build()
	if err != nil {
		panic(err)
	}
	return args
}

//------------------------------------------------------------------------------

func (q *DeleteQuery) QueryBuilder() QueryBuilder {
	return &deleteQueryBuilder{q}
}

func (q *DeleteQuery) ApplyQueryBuilder(fn func(QueryBuilder) QueryBuilder) *DeleteQuery {
	return fn(q.QueryBuilder()).Unwrap().(*DeleteQuery)
}

type deleteQueryBuilder struct {
	*DeleteQuery
}

func (q *deleteQueryBuilder) WhereGroup(
	sep string, fn func(QueryBuilder) QueryBuilder,
) QueryBuilder {
	q.DeleteQuery = q.DeleteQuery.WhereGroup(sep, func(qs *DeleteQuery) *DeleteQuery {
		return fn(q).(*deleteQueryBuilder).DeleteQuery
	})
	return q
}

func (q *deleteQueryBuilder) Where(query string, args ...any) QueryBuilder {
	q.DeleteQuery.Where(query, args...)
	return q
}

func (q *deleteQueryBuilder) WhereOr(query string, args ...any) QueryBuilder {
	q.DeleteQuery.WhereOr(query, args...)
	return q
}

func (q *deleteQueryBuilder) WhereDeleted() QueryBuilder {
	q.DeleteQuery.WhereDeleted()
	return q
}

func (q *deleteQueryBuilder) WhereAllWithDeleted() QueryBuilder {
	q.DeleteQuery.WhereAllWithDeleted()
	return q
}

func (q *deleteQueryBuilder) WherePK(cols ...string) QueryBuilder {
	q.DeleteQuery.WherePK(cols...)
	return q
}

func (q *deleteQueryBuilder) Unwrap() any {
	return q.DeleteQuery
}
