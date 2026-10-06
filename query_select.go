package boa

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/piprim/boa/schema"
)

type union struct {
	expr  string
	query *SelectQuery
}

// SelectQuery builds SQL SELECT statements.
type SelectQuery struct {
	whereBaseQuery
	orderLimitOffsetQuery

	distinctOn []schema.QueryWithArgs
	joins      []joinQuery
	group      []schema.QueryWithArgs
	having     []schema.QueryWithArgs
	selFor     schema.QueryWithArgs

	union   []union
	comment string
}

var _ Query = (*SelectQuery)(nil)

// NewSelectQuery returns a SelectQuery attached to the provided DB.
func NewSelectQuery(db *DB) *SelectQuery {
	return &SelectQuery{
		whereBaseQuery: whereBaseQuery{
			baseQuery: baseQuery{
				db: db,
			},
		},
	}
}

// Model sets the model to select into and generates SELECT and FROM clauses.
func (q *SelectQuery) Model(model any) *SelectQuery {
	q.setModel(model)
	return q
}

// Err sets an error on the query, causing subsequent operations to fail.
func (q *SelectQuery) Err(err error) *SelectQuery {
	q.setErr(err)
	return q
}

// Apply calls each function in fns, passing the SelectQuery as an argument.
func (q *SelectQuery) Apply(fns ...func(*SelectQuery) *SelectQuery) *SelectQuery {
	for _, fn := range fns {
		if fn != nil {
			q = fn(q)
		}
	}
	return q
}

// With adds a WITH clause (Common Table Expression) to the query.
func (q *SelectQuery) With(name string, query Query) *SelectQuery {
	q.addWith(NewWithQuery(name, query))
	return q
}

// WithRecursive adds a WITH RECURSIVE clause to the query.
func (q *SelectQuery) WithRecursive(name string, query Query) *SelectQuery {
	q.addWith(NewWithQuery(name, query).Recursive())
	return q
}

// WithQuery adds a pre-configured WITH clause to the query.
func (q *SelectQuery) WithQuery(query *WithQuery) *SelectQuery {
	q.addWith(query)
	return q
}

// Distinct adds a DISTINCT clause to eliminate duplicate rows.
func (q *SelectQuery) Distinct() *SelectQuery {
	q.distinctOn = make([]schema.QueryWithArgs, 0)
	return q
}

// DistinctOn adds a DISTINCT ON clause for PostgreSQL-specific distinct behavior.
func (q *SelectQuery) DistinctOn(query string, args ...any) *SelectQuery {
	q.distinctOn = append(q.distinctOn, schema.SafeQuery(query, args))
	return q
}

//------------------------------------------------------------------------------

// Table specifies the table(s) to select from.
func (q *SelectQuery) Table(tables ...string) *SelectQuery {
	for _, table := range tables {
		q.addTable(schema.UnsafeIdent(table))
	}
	return q
}

// TableExpr adds a table expression to the FROM clause with arguments.
func (q *SelectQuery) TableExpr(query string, args ...any) *SelectQuery {
	q.addTable(schema.SafeQuery(query, args))
	return q
}

// ModelTableExpr overrides the table name derived from the model.
func (q *SelectQuery) ModelTableExpr(query string, args ...any) *SelectQuery {
	q.modelTableName = schema.SafeQuery(query, args)
	return q
}

//------------------------------------------------------------------------------

// Column adds columns to the SELECT clause.
func (q *SelectQuery) Column(columns ...string) *SelectQuery {
	for _, column := range columns {
		q.addColumn(schema.UnsafeIdent(column))
	}
	return q
}

// ColumnExpr adds a column expression to the SELECT clause with arguments.
func (q *SelectQuery) ColumnExpr(query string, args ...any) *SelectQuery {
	q.addColumn(schema.SafeQuery(query, args))
	return q
}

// ExcludeColumn excludes specific columns from being selected.
func (q *SelectQuery) ExcludeColumn(columns ...string) *SelectQuery {
	q.excludeColumn(columns)
	return q
}

//------------------------------------------------------------------------------

// WherePK adds a WHERE condition on the model's primary key columns.
func (q *SelectQuery) WherePK(cols ...string) *SelectQuery {
	q.addWhereCols(cols)
	return q
}

// Where adds a WHERE condition combined with AND.
func (q *SelectQuery) Where(query string, args ...any) *SelectQuery {
	q.addWhere(schema.SafeQueryWithSep(query, args, " AND "))
	return q
}

// WhereOr adds a WHERE condition combined with OR.
func (q *SelectQuery) WhereOr(query string, args ...any) *SelectQuery {
	q.addWhere(schema.SafeQueryWithSep(query, args, " OR "))
	return q
}

// WhereGroup groups WHERE conditions with the given separator (AND/OR).
func (q *SelectQuery) WhereGroup(sep string, fn func(*SelectQuery) *SelectQuery) *SelectQuery {
	saved, savedHasOr := q.where, q.whereHasOr
	q.where, q.whereHasOr = nil, false

	q = fn(q)

	where := q.where
	q.where, q.whereHasOr = saved, savedHasOr

	q.addWhereGroup(sep, where)

	return q
}

// WhereDeleted adds a WHERE condition to select soft-deleted rows only.
func (q *SelectQuery) WhereDeleted() *SelectQuery {
	q.whereDeleted()
	return q
}

// WhereAllWithDeleted includes both active and soft-deleted rows.
func (q *SelectQuery) WhereAllWithDeleted() *SelectQuery {
	q.whereAllWithDeleted()
	return q
}

//------------------------------------------------------------------------------

//------------------------------------------------------------------------------

// Group adds columns to the GROUP BY clause.
func (q *SelectQuery) Group(columns ...string) *SelectQuery {
	for _, column := range columns {
		q.group = append(q.group, schema.UnsafeIdent(column))
	}
	return q
}

// GroupExpr adds a GROUP BY expression with optional arguments.
func (q *SelectQuery) GroupExpr(group string, args ...any) *SelectQuery {
	q.group = append(q.group, schema.SafeQuery(group, args))
	return q
}

// Having adds a HAVING clause condition to filter grouped results.
func (q *SelectQuery) Having(having string, args ...any) *SelectQuery {
	q.having = append(q.having, schema.SafeQuery(having, args))
	return q
}

// Order adds columns to the ORDER BY clause.
func (q *SelectQuery) Order(orders ...string) *SelectQuery {
	q.addOrder(orders...)
	return q
}

// OrderBy adds an ORDER BY clause with explicit sort direction.
func (q *SelectQuery) OrderBy(colName string, sortDir Order) *SelectQuery {
	q.addOrderBy(colName, sortDir)
	return q
}

// OrderExpr adds an ORDER BY expression with optional arguments.
func (q *SelectQuery) OrderExpr(query string, args ...any) *SelectQuery {
	q.addOrderExpr(query, args...)
	return q
}

// Limit sets the maximum number of rows to return.
func (q *SelectQuery) Limit(n int64) *SelectQuery {
	q.setLimit(n)
	return q
}

// Offset sets the number of rows to skip before returning results.
func (q *SelectQuery) Offset(n int64) *SelectQuery {
	q.setOffset(n)
	return q
}

// For adds a FOR clause for row locking (e.g., "UPDATE", "SHARE").
func (q *SelectQuery) For(s string, args ...any) *SelectQuery {
	q.selFor = schema.SafeQuery(s, args)
	return q
}

//------------------------------------------------------------------------------

// Union combines this query with another using UNION (removes duplicates).
func (q *SelectQuery) Union(other *SelectQuery) *SelectQuery {
	return q.addUnion(" UNION ", other)
}

// UnionAll combines this query with another using UNION ALL (keeps duplicates).
func (q *SelectQuery) UnionAll(other *SelectQuery) *SelectQuery {
	return q.addUnion(" UNION ALL ", other)
}

// Intersect returns rows that appear in both this query and another (removes duplicates).
func (q *SelectQuery) Intersect(other *SelectQuery) *SelectQuery {
	return q.addUnion(" INTERSECT ", other)
}

// IntersectAll returns rows that appear in both this query and another (keeps duplicates).
func (q *SelectQuery) IntersectAll(other *SelectQuery) *SelectQuery {
	return q.addUnion(" INTERSECT ALL ", other)
}

// Except returns rows in this query that are not in another (removes duplicates).
func (q *SelectQuery) Except(other *SelectQuery) *SelectQuery {
	return q.addUnion(" EXCEPT ", other)
}

// ExceptAll returns rows in this query that are not in another (keeps duplicates).
func (q *SelectQuery) ExceptAll(other *SelectQuery) *SelectQuery {
	return q.addUnion(" EXCEPT ALL ", other)
}

func (q *SelectQuery) addUnion(expr string, other *SelectQuery) *SelectQuery {
	q.union = append(q.union, union{
		expr:  expr,
		query: other,
	})
	return q
}

//------------------------------------------------------------------------------

// Join adds a JOIN clause with the specified join expression.
func (q *SelectQuery) Join(join string, args ...any) *SelectQuery {
	q.joins = append(q.joins, joinQuery{
		join: schema.SafeQuery(join, args),
	})
	return q
}

// JoinOn adds an ON condition to the most recent JOIN, combined with AND.
func (q *SelectQuery) JoinOn(cond string, args ...any) *SelectQuery {
	return q.joinOn(cond, args, " AND ")
}

// JoinOnOr adds an ON condition to the most recent JOIN, combined with OR.
func (q *SelectQuery) JoinOnOr(cond string, args ...any) *SelectQuery {
	return q.joinOn(cond, args, " OR ")
}

func (q *SelectQuery) joinOn(cond string, args []any, sep string) *SelectQuery {
	if len(q.joins) == 0 {
		q.setErr(errors.New("boa: query has no joins"))
		return q
	}
	j := &q.joins[len(q.joins)-1]
	j.on = append(j.on, schema.SafeQueryWithSep(cond, args, sep))
	return q
}

//------------------------------------------------------------------------------

// Relation adds a relation to the query.
func (q *SelectQuery) Relation(name string, apply ...func(*SelectQuery) *SelectQuery) *SelectQuery {
	if len(apply) > 1 {
		panic("only one apply function is supported")
	}

	if q.tableModel == nil {
		q.setErr(errNilModel)
		return q
	}

	join := q.tableModel.join(name)
	if join == nil {
		q.setErr(fmt.Errorf("%s does not have relation=%q", q.table, name))
		return q
	}

	q.applyToRelation(join, apply...)

	return q
}

// RelationOpts configures how a relation is joined in a SelectQuery.
type RelationOpts struct {
	// Apply applies additional options to the relation.
	Apply func(*SelectQuery) *SelectQuery
	// AdditionalJoinOnConditions adds additional conditions to the JOIN ON clause.
	AdditionalJoinOnConditions []schema.QueryWithArgs
}

// RelationWithOpts adds a relation to the query with additional options.
func (q *SelectQuery) RelationWithOpts(name string, opts RelationOpts) *SelectQuery {
	if q.tableModel == nil {
		q.setErr(errNilModel)
		return q
	}

	join := q.tableModel.join(name)
	if join == nil {
		q.setErr(fmt.Errorf("%s does not have relation=%q", q.table, name))
		return q
	}

	if opts.Apply != nil {
		q.applyToRelation(join, opts.Apply)
	}

	if len(opts.AdditionalJoinOnConditions) > 0 {
		join.additionalJoinOnConditions = opts.AdditionalJoinOnConditions
	}

	return q
}

func (q *SelectQuery) applyToRelation(join *relationJoin, apply ...func(*SelectQuery) *SelectQuery) {
	var apply1, apply2 func(*SelectQuery) *SelectQuery

	if len(join.Relation.Condition) > 0 {
		apply1 = func(q *SelectQuery) *SelectQuery {
			for _, opt := range join.Relation.Condition {
				q.addWhere(schema.SafeQueryWithSep(opt, nil, " AND "))
			}

			return q
		}
	}

	if len(apply) == 1 {
		apply2 = apply[0]
	}

	join.apply = func(q *SelectQuery) *SelectQuery {
		if apply1 != nil {
			q = apply1(q)
		}
		if apply2 != nil {
			q = apply2(q)
		}

		return q
	}
}

func (q *SelectQuery) forEachInlineRelJoin(fn func(*relationJoin) error) error {
	if q.tableModel == nil {
		return nil
	}
	return q._forEachInlineRelJoin(fn, q.tableModel.getJoins())
}

func (q *SelectQuery) _forEachInlineRelJoin(fn func(*relationJoin) error, joins []relationJoin) error {
	for i := range joins {
		j := &joins[i]
		switch j.Relation.Type {
		case schema.HasOneRelation, schema.BelongsToRelation:
			if err := fn(j); err != nil {
				return err
			}
			if err := q._forEachInlineRelJoin(fn, j.JoinModel.getJoins()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (q *SelectQuery) selectJoins(ctx context.Context, joins []relationJoin) error {
	for i := range joins {
		j := &joins[i]

		var err error

		switch j.Relation.Type {
		case schema.HasOneRelation, schema.BelongsToRelation:
			err = q.selectJoins(ctx, j.JoinModel.getJoins())
		case schema.HasManyRelation:
			err = j.selectMany(ctx, q.db.NewSelect())
		case schema.ManyToManyRelation:
			err = j.selectM2M(ctx, q.db.NewSelect())
		default:
			panic("not reached")
		}

		if err != nil {
			return err
		}
	}
	return nil
}

//------------------------------------------------------------------------------

// Comment adds a comment to the query, wrapped by /* ... */.
func (q *SelectQuery) Comment(comment string) *SelectQuery {
	q.comment = comment
	return q
}

//------------------------------------------------------------------------------

// Operation returns the query operation name ("SELECT").
func (q *SelectQuery) Operation() string {
	return "SELECT"
}

func (q *SelectQuery) AppendQuery(gen schema.QueryGen, b []byte) (_ []byte, err error) {
	b = appendComment(b, q.comment)

	return q.appendQuery(gen, b, false)
}

func (q *SelectQuery) appendQuery(
	gen schema.QueryGen, b []byte, count bool,
) (_ []byte, err error) {
	if q.err != nil {
		return nil, q.err
	}

	gen = formatterWithModel(gen, q)

	cteCount := count && (len(q.group) > 0 || q.distinctOn != nil)
	if cteCount {
		b = append(b, "WITH _count_wrapper AS ("...)
	}

	wrapUnion := len(q.union) > 0

	if wrapUnion {
		b = append(b, '(')
	}

	b, err = q.appendWith(gen, b)
	if err != nil {
		return nil, err
	}

	if err := q.forEachInlineRelJoin(func(j *relationJoin) error {
		j.applyTo(q)
		return nil
	}); err != nil {
		return nil, err
	}

	b = append(b, "SELECT "...)

	if len(q.distinctOn) > 0 {
		b = append(b, "DISTINCT ON ("...)
		for i, app := range q.distinctOn {
			if i > 0 {
				b = append(b, ", "...)
			}
			b, err = app.AppendQuery(gen, b)
			if err != nil {
				return nil, err
			}
		}
		b = append(b, ") "...)
	} else if q.distinctOn != nil {
		b = append(b, "DISTINCT "...)
	}

	if count && !cteCount {
		b = append(b, "count(*)"...)
	} else {
		b, err = q.appendColumns(gen, b)
		if err != nil {
			return nil, err
		}
	}

	if q.hasTables() {
		b, err = q.appendTables(gen, b)
		if err != nil {
			return nil, err
		}
	}

	if err := q.forEachInlineRelJoin(func(j *relationJoin) error {
		b = append(b, ' ')
		b, err = j.appendHasOneJoin(gen, b, q)
		return err
	}); err != nil {
		return nil, err
	}

	for _, join := range q.joins {
		b, err = join.AppendQuery(gen, b)
		if err != nil {
			return nil, err
		}
	}

	b, err = q.appendWhere(gen, b, true)
	if err != nil {
		return nil, err
	}

	if len(q.group) > 0 {
		b = append(b, " GROUP BY "...)
		for i, f := range q.group {
			if i > 0 {
				b = append(b, ", "...)
			}
			b, err = f.AppendQuery(gen, b)
			if err != nil {
				return nil, err
			}
		}
	}

	if len(q.having) > 0 {
		b = append(b, " HAVING "...)
		for i, f := range q.having {
			if i > 0 {
				b = append(b, " AND "...)
			}
			b = append(b, '(')
			b, err = f.AppendQuery(gen, b)
			if err != nil {
				return nil, err
			}
			b = append(b, ')')
		}
	}

	if !count {
		b, err = q.appendOrder(gen, b)
		if err != nil {
			return nil, err
		}

		b, err = q.appendLimitOffset(gen, b)
		if err != nil {
			return nil, err
		}

		if !q.selFor.IsZero() {
			b = append(b, " FOR "...)
			b, err = q.selFor.AppendQuery(gen, b)
			if err != nil {
				return nil, err
			}
		}
	}

	if wrapUnion {
		b = append(b, ')')
	}

	for _, u := range q.union {
		b = append(b, u.expr...)
		if wrapUnion {
			b = append(b, '(')
		}
		b, err = u.query.AppendQuery(gen, b)
		if err != nil {
			return nil, err
		}
		if wrapUnion {
			b = append(b, ')')
		}
	}

	if cteCount {
		b = append(b, ") SELECT count(*) FROM _count_wrapper"...)
	}

	return b, nil
}

func (q *SelectQuery) appendColumns(gen schema.QueryGen, b []byte) (_ []byte, err error) {
	start := len(b)

	switch {
	case q.columns != nil:
		for i, col := range q.columns {
			if i > 0 {
				b = append(b, ", "...)
			}

			if col.Args == nil && q.table != nil {
				if field, ok := q.table.FieldMap[col.Query]; ok {
					b = append(b, q.table.SQLAlias...)
					b = append(b, '.')
					b = append(b, field.SQLName...)
					continue
				}
			}

			b, err = col.AppendQuery(gen, b)
			if err != nil {
				return nil, err
			}
		}
	case q.table != nil:
		if len(q.table.Fields) > 10 && gen.IsNop() {
			b = append(b, q.table.SQLAlias...)
			b = append(b, '.')
			b = append(b, '\'')
			b = fmt.Appendf(b, "%d columns", len(q.table.Fields))
			b = append(b, '\'')
		} else {
			b = appendColumns(b, q.table.SQLAlias, q.table.Fields)
		}
	default:
		b = append(b, '*')
	}

	if err := q.forEachInlineRelJoin(func(join *relationJoin) error {
		if len(b) != start {
			b = append(b, ", "...)
			start = len(b)
		}

		b, err = q.appendInlineRelColumns(gen, b, join)
		if err != nil {
			return err
		}

		return nil
	}); err != nil {
		return nil, err
	}

	b = bytes.TrimSuffix(b, []byte(", "))

	return b, nil
}

func (q *SelectQuery) appendInlineRelColumns(
	gen schema.QueryGen, b []byte, join *relationJoin,
) (_ []byte, err error) {
	if join.columns != nil {
		table := join.JoinModel.Table()
		for i, col := range join.columns {
			if i > 0 {
				b = append(b, ", "...)
			}

			if col.Args == nil {
				if field, ok := table.FieldMap[col.Query]; ok {
					b = join.appendAlias(gen, b)
					b = append(b, '.')
					b = append(b, field.SQLName...)
					b = append(b, " AS "...)
					b = join.appendAliasColumn(gen, b, field.Name)
					continue
				}
			}

			b, err = col.AppendQuery(gen, b)
			if err != nil {
				return nil, err
			}
		}
		return b, nil
	}

	for i, field := range join.JoinModel.Table().Fields {
		if i > 0 {
			b = append(b, ", "...)
		}
		b = join.appendAlias(gen, b)
		b = append(b, '.')
		b = append(b, field.SQLName...)
		b = append(b, " AS "...)
		b = join.appendAliasColumn(gen, b, field.Name)
	}
	return b, nil
}

func (q *SelectQuery) appendTables(gen schema.QueryGen, b []byte) (_ []byte, err error) {
	b = append(b, " FROM "...)
	return q.appendTablesWithAlias(gen, b)
}

//------------------------------------------------------------------------------

// Rows executes the query and returns the result rows for manual scanning.
// The caller must close the rows.
//
// On error pgx may return a non-nil, already-closed pgx.Rows alongside the
// error (boa returned nil rows), so check the error before using the rows.
func (q *SelectQuery) Rows(ctx context.Context) (pgx.Rows, error) {
	if q.err != nil {
		return nil, q.err
	}

	if err := q.beforeAppendModel(ctx, q); err != nil {
		return nil, err
	}

	// if a comment is propagated via the context, use it
	setCommentFromContext(ctx, q)

	query, args, err := q.build(q)
	if err != nil {
		return nil, q.db.failBuild(ctx, q, q.model, err)
	}

	ctx, event := q.db.beforeQuery(ctx, q, query, args, q.model)

	var rows pgx.Rows
	exec, err := q.resolveExecutor(ctx, q, query)
	if err == nil {
		rows, err = exec.Query(ctx, query, q.db.queryArgs(args)...)
	}

	q.db.afterQuery(ctx, event, pgconn.CommandTag{}, err)
	return rows, err
}

// Exec executes the query and optionally scans results into dest.
func (q *SelectQuery) Exec(ctx context.Context, dest ...any) (res pgconn.CommandTag, err error) {
	if q.err != nil {
		return pgconn.CommandTag{}, q.err
	}
	if err := q.beforeAppendModel(ctx, q); err != nil {
		return pgconn.CommandTag{}, err
	}

	// if a comment is propagated via the context, use it
	setCommentFromContext(ctx, q)

	query, args, err := q.build(q)
	if err != nil {
		return pgconn.CommandTag{}, q.db.failBuild(ctx, q, q.model, err)
	}

	if len(dest) > 0 {
		model, err := q.getModel(dest)
		if err != nil {
			return pgconn.CommandTag{}, err
		}

		res, err = q.scan(ctx, q, query, args, model, true)
		if err != nil {
			return pgconn.CommandTag{}, err
		}
	} else {
		res, err = q.exec(ctx, q, query, args)
		if err != nil {
			return pgconn.CommandTag{}, err
		}
	}

	return res, nil
}

// Scan executes the query and scans the results into dest.
func (q *SelectQuery) Scan(ctx context.Context, dest ...any) error {
	_, err := q.scanResult(ctx, dest...)
	return err
}

func (q *SelectQuery) scanResult(ctx context.Context, dest ...any) (pgconn.CommandTag, error) {
	if q.err != nil {
		return pgconn.CommandTag{}, q.err
	}

	model, err := q.getModel(dest)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	if len(dest) > 0 && q.tableModel != nil && len(q.tableModel.getJoins()) > 0 {
		for _, j := range q.tableModel.getJoins() {
			switch j.Relation.Type {
			case schema.HasManyRelation, schema.ManyToManyRelation:
				return pgconn.CommandTag{}, fmt.Errorf("When querying has-many or many-to-many relationships, you should use Model instead of the dest parameter in Scan.")
			}
		}
	}

	if q.table != nil {
		if err := q.beforeSelectHook(ctx); err != nil {
			return pgconn.CommandTag{}, err
		}
	}

	if err := q.beforeAppendModel(ctx, q); err != nil {
		return pgconn.CommandTag{}, err
	}

	// if a comment is propagated via the context, use it
	setCommentFromContext(ctx, q)

	query, args, err := q.build(q)
	if err != nil {
		return pgconn.CommandTag{}, q.db.failBuild(ctx, q, q.model, err)
	}

	res, err := q.scan(ctx, q, query, args, model, true)
	if err != nil {
		return pgconn.CommandTag{}, err
	}

	if res.RowsAffected() > 0 {
		if tableModel, ok := model.(TableModel); ok {
			if err := q.selectJoins(ctx, tableModel.getJoins()); err != nil {
				return pgconn.CommandTag{}, err
			}
		}
	}

	if q.table != nil {
		if err := q.afterSelectHook(ctx); err != nil {
			return pgconn.CommandTag{}, err
		}
	}

	return res, nil
}

func (q *SelectQuery) beforeSelectHook(ctx context.Context) error {
	if hook, ok := q.table.ZeroIface.(BeforeSelectHook); ok {
		if err := hook.BeforeSelect(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

func (q *SelectQuery) afterSelectHook(ctx context.Context) error {
	if hook, ok := q.table.ZeroIface.(AfterSelectHook); ok {
		if err := hook.AfterSelect(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// Count executes the query and returns the number of rows that match.
func (q *SelectQuery) Count(ctx context.Context) (int64, error) {
	if q.err != nil {
		return 0, q.err
	}

	// if a comment is propagated via the context, use it
	setCommentFromContext(ctx, q)

	qq := countQuery{q}

	query, args, err := q.build(qq)
	if err != nil {
		return 0, q.db.failBuild(ctx, qq, q.model, err)
	}
	ctx, event := q.db.beforeQuery(ctx, qq, query, args, q.model)

	var num int64
	exec, err := q.resolveExecutor(ctx, qq, query)
	if err == nil {
		err = exec.QueryRow(ctx, query, q.db.queryArgs(args)...).Scan(&num)
	}

	q.db.afterQuery(ctx, event, pgconn.CommandTag{}, err)

	return num, err
}

// ScanAndCount executes the query, scans results into dest, and returns the total count.
// The fetch and the count run concurrently only when the executor is a pool;
// a transaction or a dedicated connection is a single connection and gets them in sequence.
func (q *SelectQuery) ScanAndCount(ctx context.Context, dest ...any) (int64, error) {
	if q.offset == 0 && q.limit == 0 {
		res, err := q.scanResult(ctx, dest...)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected(), nil
	}

	exec, err := q.db.Executor(ctx)
	if err != nil {
		return 0, err
	}
	if isPool(exec) {
		return q.scanAndCountConcurrently(ctx, dest...)
	}
	return q.scanAndCountSeq(ctx, dest...)
}

func (q *SelectQuery) scanAndCountConcurrently(
	ctx context.Context, dest ...any,
) (int64, error) {
	var count int64
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	// FIXME: clone should not be needed, because the query is not modified here
	// and should not be implicitly modified by the Boa lib.
	countQuery := q.Clone()

	// Don't scan results if the user explicitly set Limit(-1).
	if q.limit >= 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			if err := q.Scan(ctx, dest...); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()

		var err error
		count, err = countQuery.Count(ctx)
		if err != nil {
			mu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
		}
	}()

	wg.Wait()
	return count, firstErr
}

func (q *SelectQuery) scanAndCountSeq(ctx context.Context, dest ...any) (int64, error) {
	var firstErr error

	// Don't scan results if the user explicitly set Limit(-1).
	if q.limit >= 0 {
		firstErr = q.Scan(ctx, dest...)
	}

	count, err := q.Count(ctx)
	if err != nil && firstErr == nil {
		firstErr = err
	}

	return count, firstErr
}

// Exists checks whether any rows match the query.
func (q *SelectQuery) Exists(ctx context.Context) (bool, error) {
	if q.err != nil {
		return false, q.err
	}

	return q.selectExists(ctx)
}

func (q *SelectQuery) selectExists(ctx context.Context) (bool, error) {
	// if a comment is propagated via the context, use it
	setCommentFromContext(ctx, q)

	qq := selectExistsQuery{q}

	query, args, err := q.build(qq)
	if err != nil {
		return false, q.db.failBuild(ctx, qq, q.model, err)
	}
	ctx, event := q.db.beforeQuery(ctx, qq, query, args, q.model)

	var exists bool
	exec, err := q.resolveExecutor(ctx, qq, query)
	if err == nil {
		err = exec.QueryRow(ctx, query, q.db.queryArgs(args)...).Scan(&exists)
	}

	q.db.afterQuery(ctx, event, pgconn.CommandTag{}, err)

	return exists, err
}

// Build renders the query and returns the SQL with $n placeholders together
// with the values bound to them. The query must not be modified while
// rendering, so repeated calls return identical results.
func (q *SelectQuery) Build() (string, []any, error) {
	return q.db.build(q)
}

// String returns the SQL with $n placeholders. It panics on a render error.
func (q *SelectQuery) String() string {
	sql, _, err := q.Build()
	if err != nil {
		panic(err)
	}
	return sql
}

// Args returns the values bound to the placeholders of String. It panics on
// a render error.
func (q *SelectQuery) Args() []any {
	_, args, err := q.Build()
	if err != nil {
		panic(err)
	}
	return args
}

// Clone creates a deep copy of the SelectQuery.
func (q *SelectQuery) Clone() *SelectQuery {
	if q == nil {
		return nil
	}

	cloneArgs := func(args []schema.QueryWithArgs) []schema.QueryWithArgs {
		if args == nil {
			return nil
		}
		clone := make([]schema.QueryWithArgs, len(args))
		copy(clone, args)
		return clone
	}
	cloneWhereFields := func(fields []*schema.Field) []*schema.Field {
		if fields == nil {
			return nil
		}
		clone := make([]*schema.Field, len(fields))
		copy(clone, fields)
		return clone
	}

	var tableModel TableModel
	if q.tableModel != nil {
		tableModel = q.tableModel.clone()
	}
	clone := &SelectQuery{
		whereBaseQuery: whereBaseQuery{
			baseQuery: baseQuery{
				db:             q.db,
				table:          q.table,
				model:          q.model,
				err:            q.err,
				tableModel:     tableModel,
				with:           make([]WithQuery, len(q.with)),
				tables:         cloneArgs(q.tables),
				columns:        cloneArgs(q.columns),
				modelTableName: q.modelTableName,
				flags:          q.flags,
			},
			where:       make([]schema.QueryWithSep, len(q.where)),
			whereFields: cloneWhereFields(q.whereFields),
			whereHasOr:  q.whereHasOr,
		},

		orderLimitOffsetQuery: orderLimitOffsetQuery{
			order:  cloneArgs(q.order),
			limit:  q.limit,
			offset: q.offset,
		},

		distinctOn: cloneArgs(q.distinctOn),
		joins:      make([]joinQuery, len(q.joins)),
		group:      cloneArgs(q.group),
		having:     cloneArgs(q.having),
		union:      make([]union, len(q.union)),
		comment:    q.comment,
	}

	for i, w := range q.with {
		clone.with[i] = WithQuery{
			name:            w.name,
			recursive:       w.recursive,
			materialized:    w.materialized,
			notMaterialized: w.notMaterialized,
			query:           w.query, // TODO: maybe clone is need
		}
	}

	if !q.modelTableName.IsZero() {
		clone.modelTableName = schema.SafeQuery(
			q.modelTableName.Query,
			append([]any(nil), q.modelTableName.Args...),
		)
	}

	for i, w := range q.where {
		clone.where[i] = schema.SafeQueryWithSep(
			w.Query,
			append([]any(nil), w.Args...),
			w.Sep,
		)
	}

	for i, j := range q.joins {
		clone.joins[i] = joinQuery{
			join: schema.SafeQuery(j.join.Query, append([]any(nil), j.join.Args...)),
			on:   make([]schema.QueryWithSep, len(j.on)),
		}
		for k, on := range j.on {
			clone.joins[i].on[k] = schema.SafeQueryWithSep(
				on.Query,
				append([]any(nil), on.Args...),
				on.Sep,
			)
		}
	}

	for i, u := range q.union {
		clone.union[i] = union{
			expr:  u.expr,
			query: u.query.Clone(),
		}
	}

	if !q.selFor.IsZero() {
		clone.selFor = schema.SafeQuery(
			q.selFor.Query,
			append([]any(nil), q.selFor.Args...),
		)
	}

	return clone
}

//------------------------------------------------------------------------------

// QueryBuilder wraps the SelectQuery in a generic QueryBuilder interface.
func (q *SelectQuery) QueryBuilder() QueryBuilder {
	return &selectQueryBuilder{q}
}

// ApplyQueryBuilder applies a function to a generic QueryBuilder and returns the modified SelectQuery.
func (q *SelectQuery) ApplyQueryBuilder(fn func(QueryBuilder) QueryBuilder) *SelectQuery {
	return fn(q.QueryBuilder()).Unwrap().(*SelectQuery)
}

type selectQueryBuilder struct {
	*SelectQuery
}

func (q *selectQueryBuilder) WhereGroup(
	sep string, fn func(QueryBuilder) QueryBuilder,
) QueryBuilder {
	q.SelectQuery = q.SelectQuery.WhereGroup(sep, func(qs *SelectQuery) *SelectQuery {
		return fn(q).(*selectQueryBuilder).SelectQuery
	})
	return q
}

func (q *selectQueryBuilder) Where(query string, args ...any) QueryBuilder {
	q.SelectQuery.Where(query, args...)
	return q
}

func (q *selectQueryBuilder) WhereOr(query string, args ...any) QueryBuilder {
	q.SelectQuery.WhereOr(query, args...)
	return q
}

func (q *selectQueryBuilder) WhereDeleted() QueryBuilder {
	q.SelectQuery.WhereDeleted()
	return q
}

func (q *selectQueryBuilder) WhereAllWithDeleted() QueryBuilder {
	q.SelectQuery.WhereAllWithDeleted()
	return q
}

func (q *selectQueryBuilder) WherePK(cols ...string) QueryBuilder {
	q.SelectQuery.WherePK(cols...)
	return q
}

func (q *selectQueryBuilder) Unwrap() any {
	return q.SelectQuery
}

//------------------------------------------------------------------------------

type joinQuery struct {
	join schema.QueryWithArgs
	on   []schema.QueryWithSep
}

func (j *joinQuery) AppendQuery(gen schema.QueryGen, b []byte) (_ []byte, err error) {
	b = append(b, ' ')

	b, err = j.join.AppendQuery(gen, b)
	if err != nil {
		return nil, err
	}

	if len(j.on) > 0 {
		b = append(b, " ON "...)
		for i, on := range j.on {
			if i > 0 {
				b = append(b, on.Sep...)
			}

			b = append(b, '(')
			b, err = on.AppendQuery(gen, b)
			if err != nil {
				return nil, err
			}
			b = append(b, ')')
		}
	}

	return b, nil
}

//------------------------------------------------------------------------------

type countQuery struct {
	*SelectQuery
}

func (q countQuery) AppendQuery(gen schema.QueryGen, b []byte) (_ []byte, err error) {
	if q.err != nil {
		return nil, q.err
	}
	return q.appendQuery(gen, b, true)
}

//------------------------------------------------------------------------------

type selectExistsQuery struct {
	*SelectQuery
}

func (q selectExistsQuery) AppendQuery(gen schema.QueryGen, b []byte) (_ []byte, err error) {
	if q.err != nil {
		return nil, q.err
	}

	b = append(b, "SELECT EXISTS ("...)

	b, err = q.appendQuery(gen, b, false)
	if err != nil {
		return nil, err
	}

	b = append(b, ")"...)

	return b, nil
}

//------------------------------------------------------------------------------
