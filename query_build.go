package pgcrud

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/piprim/pgcrud/internal"
	"github.com/piprim/pgcrud/schema"
)

// maxParams is the number of bind parameters Postgres accepts in one statement.
const maxParams = 65535

// ErrTooManyParams is returned when a statement binds more than 65535 values.
// Chunk the rows, or use pgx.CopyFrom for bulk loads.
var ErrTooManyParams = errors.New("pgcrud: too many bound parameters")

// build renders app into SQL with $n placeholders and returns the values bound
// to them, in order. It fails before anything is sent when a value cannot be
// encoded or the parameter limit is exceeded.
func (db *DB) build(app schema.QueryAppender) (string, []any, error) {
	list := schema.NewArgList()
	b, err := app.AppendQuery(db.gen.WithArgList(list), db.makeQueryBytes())
	if err != nil {
		return "", nil, err
	}
	if err := list.Err(); err != nil {
		return "", nil, err
	}
	args := list.Args()
	if len(args) > maxParams {
		return "", nil, fmt.Errorf("pgcrud: query binds %d parameters, Postgres allows at most %d: %w",
			len(args), maxParams, ErrTooManyParams)
	}
	return internal.String(b), args, nil
}

func (q *baseQuery) build(app schema.QueryAppender) (string, []any, error) {
	return q.db.build(app)
}

// failBuild reports a render error to the query hooks and returns it. The
// hooks see an empty Query and no args because nothing was sent.
func (db *DB) failBuild(ctx context.Context, iquery Query, model Model, err error) error {
	ctx, event := db.beforeQuery(ctx, iquery, "", nil, model)
	db.afterQuery(ctx, event, pgconn.CommandTag{}, err)
	return err
}

// queryArgs returns the arguments of an executor Query or QueryRow call: the
// pgx option values first, then the bound values.
func (db *DB) queryArgs(args []any) []any {
	out := make([]any, 0, len(args)+2)
	if db.hasExecMode {
		out = append(out, db.execMode)
	}
	out = append(out, db.resultFormats)
	return append(out, args...)
}

// execArgs returns the arguments of an executor Exec call. Exec takes no
// result formats, so only the exec mode precedes the values.
func (db *DB) execArgs(args []any) []any {
	if !db.hasExecMode {
		return args
	}
	out := make([]any, 0, len(args)+1)
	out = append(out, db.execMode)
	return append(out, args...)
}
