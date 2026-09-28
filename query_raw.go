package pgcrud

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/piprim/pgcrud/schema"
)

// RawQuery executes a plain SQL query using Bun formatting and hooks.
type RawQuery struct {
	baseQuery

	query   string
	args    []any
	comment string
}

// NewRawQuery creates a RawQuery with the provided SQL template and arguments.
func NewRawQuery(db *DB, query string, args ...any) *RawQuery {
	return &RawQuery{
		baseQuery: baseQuery{
			db: db,
		},
		query: query,
		args:  args,
	}
}

func (q *RawQuery) Err(err error) *RawQuery {
	q.setErr(err)
	return q
}

func (q *RawQuery) Exec(ctx context.Context, dest ...any) (pgconn.CommandTag, error) {
	return q.scanOrExec(ctx, dest, len(dest) > 0)
}

func (q *RawQuery) Scan(ctx context.Context, dest ...any) error {
	_, err := q.scanOrExec(ctx, dest, true)
	return err
}

// Comment adds a comment to the query, wrapped by /* ... */.
func (q *RawQuery) Comment(comment string) *RawQuery {
	q.comment = comment
	return q
}

func (q *RawQuery) scanOrExec(
	ctx context.Context, dest []any, hasDest bool,
) (pgconn.CommandTag, error) {
	if q.err != nil {
		return pgconn.CommandTag{}, q.err
	}

	var model Model
	var err error

	if hasDest {
		model, err = q.getModel(dest)
		if err != nil {
			return pgconn.CommandTag{}, err
		}
	}

	// if a comment is propagated via the context, use it
	setCommentFromContext(ctx, q)

	query, args, err := q.build(q)
	if err != nil {
		return pgconn.CommandTag{}, q.db.failBuild(ctx, q, q.model, err)
	}
	var res pgconn.CommandTag

	if hasDest {
		res, err = q.scan(ctx, q, query, args, model, hasDest)
	} else {
		res, err = q.exec(ctx, q, query, args)
	}

	if err != nil {
		return pgconn.CommandTag{}, err
	}

	return res, nil
}

// AppendQuery renders the raw SQL. Question marks are translated to $n and
// their args bound. A query with no question mark at all is pgx-style SQL
// that already carries $n placeholders: it is written unchanged and its args
// are passed through in order. Mixing both styles is not supported.
func (q *RawQuery) AppendQuery(gen schema.QueryGen, b []byte) ([]byte, error) {
	b = appendComment(b, q.comment)

	if len(q.args) > 0 && strings.IndexByte(q.query, '?') == -1 {
		if len(gen.Args()) > 0 {
			// The $n in the raw text would refer to values bound before it.
			return nil, errors.New("pgcrud: raw SQL with $n placeholders cannot follow bound values; use ? placeholders")
		}
		gen.BindArgs(q.args...)
		return append(b, q.query...), nil
	}

	return gen.AppendQuery(b, q.query, q.args...), nil
}

func (q *RawQuery) Operation() string {
	return "SELECT"
}

// Build renders the query and returns the SQL with $n placeholders together
// with the values bound to them. The query must not be modified while
// rendering, so repeated calls return identical results.
func (q *RawQuery) Build() (string, []any, error) {
	return q.db.build(q)
}

// String returns the SQL with $n placeholders. It panics on a render error.
func (q *RawQuery) String() string {
	sql, _, err := q.Build()
	if err != nil {
		panic(err)
	}
	return sql
}

// Args returns the values bound to the placeholders of String. It panics on
// a render error.
func (q *RawQuery) Args() []any {
	_, args, err := q.Build()
	if err != nil {
		panic(err)
	}
	return args
}
