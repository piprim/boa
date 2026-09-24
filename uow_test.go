package pgcrud_test

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/piprim/pgcrud"
)

type txKey struct{}

// UnitOfWork mirrors the application's unit of work so the tests prove the
// library works with it unchanged.
type UnitOfWork struct {
	pool *pgxpool.Pool
}

func NewUnitOfWork(pool *pgxpool.Pool) *UnitOfWork {
	return &UnitOfWork{pool: pool}
}

func (u *UnitOfWork) Executor(ctx context.Context) pgcrud.DBExecutor {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return u.pool
}

func (u *UnitOfWork) WithTransaction(ctx context.Context, fn func(context.Context) error) error {
	if existingTx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		nestedTx, err := existingTx.Begin(ctx)
		if err != nil {
			return fmt.Errorf("cannot start nested transaction (savepoint): %w", err)
		}
		defer func() { _ = nestedTx.Rollback(ctx) }()

		if err := fn(context.WithValue(ctx, txKey{}, nestedTx)); err != nil {
			return err
		}
		if err := nestedTx.Commit(ctx); err != nil {
			return fmt.Errorf("cannot release savepoint: %w", err)
		}
		return nil
	}

	tx, err := u.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("cannot start root transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("cannot commit root transaction: %w", err)
	}
	return nil
}
