package pgcrud

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBExecutor is the subset of pgx that pgcrud needs to run a query.
// *pgxpool.Pool and pgx.Tx satisfy it structurally.
type DBExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var (
	_ DBExecutor = (*pgxpool.Pool)(nil)
	_ DBExecutor = (pgx.Tx)(nil)
)

// ExecutorResolver returns the executor to use for a request, typically the
// pgx.Tx stored in ctx by the application's unit of work, or the pool.
//
// The resolver must return a nil interface, not a typed nil (for example a nil
// pgx.Tx inside a non-nil DBExecutor), because only a nil interface is
// detected and reported as ErrNilExecutor.
type ExecutorResolver func(ctx context.Context) DBExecutor

// ErrNilExecutor is returned when the resolver produced no executor.
var ErrNilExecutor = errors.New("pgcrud: executor resolver returned nil")

// ErrTxRequired is returned by writes when WithTxRequiredForWrites is set and
// the resolved executor is not a pgx.Tx.
var ErrTxRequired = errors.New("pgcrud: write outside a transaction")

// isTx reports whether exec is a pgx transaction.
func isTx(exec DBExecutor) bool {
	_, ok := exec.(pgx.Tx)
	return ok
}

// isPool reports whether exec is a connection pool, and so safe for concurrent queries.
func isPool(exec DBExecutor) bool {
	_, ok := exec.(*pgxpool.Pool)
	return ok
}

// poolResolver always returns pool. It returns a nil interface, not a typed
// nil, when pool is nil so that callers can detect the missing pool.
func poolResolver(pool *pgxpool.Pool) ExecutorResolver {
	return func(context.Context) DBExecutor {
		if pool == nil {
			return nil
		}
		return pool
	}
}
