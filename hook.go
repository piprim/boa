package pgcrud

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgconn"
)

// QueryEvent captures information about a query execution for hooks.
type QueryEvent struct {
	DB *DB

	IQuery        Query
	Query         string
	QueryTemplate string
	QueryArgs     []any
	Model         Model

	StartTime time.Time
	Result    pgconn.CommandTag
	Err       error

	Stash map[any]any
}

// Operation returns the SQL operation name such as SELECT or UPDATE.
func (e *QueryEvent) Operation() string {
	if e.IQuery != nil {
		return e.IQuery.Operation()
	}
	return queryOperation(e.Query)
}

func queryOperation(query string) string {
	queryOp := strings.TrimLeftFunc(query, unicode.IsSpace)

	if idx := strings.IndexByte(queryOp, ' '); idx > 0 {
		queryOp = queryOp[:idx]
	}
	if len(queryOp) > 16 {
		queryOp = queryOp[:16]
	}
	return queryOp
}

// QueryHook allows observing queries before and after execution.
type QueryHook interface {
	BeforeQuery(context.Context, *QueryEvent) context.Context
	AfterQuery(context.Context, *QueryEvent)
}

func (db *DB) beforeQuery(
	ctx context.Context,
	iquery Query,
	queryTemplate string,
	queryArgs []any,
	query string,
	model Model,
) (context.Context, *QueryEvent) {
	if len(db.queryHooks) == 0 {
		return ctx, nil
	}

	event := &QueryEvent{
		DB: db,

		Model:         model,
		IQuery:        iquery,
		Query:         query,
		QueryTemplate: queryTemplate,
		QueryArgs:     queryArgs,

		StartTime: time.Now(),
	}

	for _, hook := range db.queryHooks {
		ctx = hook.BeforeQuery(ctx, event)
	}

	return ctx, event
}

func (db *DB) afterQuery(
	ctx context.Context,
	event *QueryEvent,
	res pgconn.CommandTag,
	err error,
) {
	if event == nil {
		return
	}

	event.Result = res
	event.Err = err

	for i := len(db.queryHooks) - 1; i >= 0; i-- {
		db.queryHooks[i].AfterQuery(ctx, event)
	}
}
