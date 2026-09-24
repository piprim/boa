package pgcrud_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/piprim/pgcrud"
)

type User struct {
	ID   int64 `bun:",pk"`
	Name string
}

type ctxKey struct{}

func withExec(exec pgcrud.DBExecutor) *pgcrud.DB {
	return pgcrud.New(nil, pgcrud.WithExecutorResolver(func(context.Context) pgcrud.DBExecutor {
		return exec
	}))
}

func TestNew(t *testing.T) {
	ctx := context.Background()

	t.Run("builds SQL without a pool", func(t *testing.T) {
		db := pgcrud.New(nil)
		q := db.NewSelect().Model((*User)(nil)).Where("id = ?", 1)
		require.Equal(t, `SELECT "user"."id", "user"."name" FROM "users" AS "user" WHERE (id = 1)`, q.String())
	})

	t.Run("executing without a pool returns ErrNilExecutor", func(t *testing.T) {
		db := pgcrud.New(nil)
		var u User
		err := db.NewSelect().Model(&u).Scan(ctx)
		require.ErrorIs(t, err, pgcrud.ErrNilExecutor)
	})

	t.Run("String names the dialect", func(t *testing.T) {
		require.Equal(t, "DB<dialect=pg>", pgcrud.New(nil).String())
	})
}

func TestExecutorResolver(t *testing.T) {
	exec := &fakeExecutor{rows: newFakeRows(cols("id", "name"), []any{int64(1), "ann"})}
	var seen context.Context
	db := pgcrud.New(nil, pgcrud.WithExecutorResolver(func(ctx context.Context) pgcrud.DBExecutor {
		seen = ctx
		return exec
	}))
	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")

	var u User
	err := db.NewSelect().Model(&u).Scan(ctx)
	require.NoError(t, err)

	t.Run("resolver receives the query context", func(t *testing.T) {
		require.Equal(t, "marker", seen.Value(ctxKey{}))
	})

	t.Run("query is sent with the simple protocol as its only argument", func(t *testing.T) {
		require.Len(t, exec.calls, 1)
		require.Equal(t, "Query", exec.calls[0].method)
		require.Equal(t, []any{pgx.QueryExecModeSimpleProtocol}, exec.calls[0].args)
		require.Equal(t, `SELECT "user"."id", "user"."name" FROM "users" AS "user"`, exec.calls[0].sql)
	})

	t.Run("nil resolver result returns ErrNilExecutor", func(t *testing.T) {
		db := withExec(nil)
		_, err := db.NewDelete().Model((*User)(nil)).Where("id = 1").Exec(context.Background())
		require.ErrorIs(t, err, pgcrud.ErrNilExecutor)
	})
}

func TestScan(t *testing.T) {
	ctx := context.Background()

	t.Run("struct", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("id", "name"), []any{int64(7), "bob"})}
		var u User
		require.NoError(t, withExec(exec).NewSelect().Model(&u).Scan(ctx))
		require.Equal(t, User{ID: 7, Name: "bob"}, u)
		require.True(t, exec.rows.closed, "rows must be closed")
	})

	t.Run("slice", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("id", "name"),
			[]any{int64(1), "a"}, []any{int64(2), "b"})}
		var us []User
		require.NoError(t, withExec(exec).NewSelect().Model(&us).Scan(ctx))
		require.Equal(t, []User{{1, "a"}, {2, "b"}}, us)
	})

	t.Run("scalar destinations", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("id", "name"), []any{int64(3), "c"})}
		var id int64
		var name string
		require.NoError(t, withExec(exec).NewSelect().Model((*User)(nil)).Scan(ctx, &id, &name))
		require.Equal(t, int64(3), id)
		require.Equal(t, "c", name)
	})

	t.Run("no rows into a struct returns pgx.ErrNoRows", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("id", "name"))}
		var u User
		err := withExec(exec).NewSelect().Model(&u).Scan(ctx)
		require.ErrorIs(t, err, pgx.ErrNoRows)
	})

	t.Run("no rows into a slice is not an error", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("id", "name"))}
		var us []User
		require.NoError(t, withExec(exec).NewSelect().Model(&us).Scan(ctx))
		require.Empty(t, us)
	})

	t.Run("Exec with dest reports scanned rows", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("id", "name"),
			[]any{int64(1), "a"}, []any{int64(2), "b"})}
		var us []User
		res, err := withExec(exec).NewSelect().Model(&us).Exec(ctx, &us)
		require.NoError(t, err)
		require.Equal(t, int64(2), res.RowsAffected())
	})

	t.Run("map clones byte slices", func(t *testing.T) {
		raw := []byte("abc")
		exec := &fakeExecutor{rows: newFakeRows(cols("id", "data"), []any{int64(1), raw})}
		var m map[string]any
		require.NoError(t, withExec(exec).NewSelect().Table("t").Scan(ctx, &m))
		raw[0] = 'z'
		require.Equal(t, int64(1), m["id"])
		require.Equal(t, []byte("abc"), m["data"])
	})

	t.Run("rows error is returned", func(t *testing.T) {
		rows := newFakeRows(cols("id", "name"))
		rows.err = errors.New("boom")
		exec := &fakeExecutor{rows: rows}
		var us []User
		err := withExec(exec).NewSelect().Model(&us).Scan(ctx)
		require.EqualError(t, err, "boom")
	})
}

func TestExec(t *testing.T) {
	ctx := context.Background()

	t.Run("returns the executor's command tag", func(t *testing.T) {
		exec := &fakeExecutor{tag: pgconn.NewCommandTag("DELETE 3")}
		res, err := withExec(exec).NewDelete().Model((*User)(nil)).Where("id > 0").Exec(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(3), res.RowsAffected())
		require.Equal(t, "Exec", exec.calls[0].method)
		require.Equal(t, []any{pgx.QueryExecModeSimpleProtocol}, exec.calls[0].args)
	})

	t.Run("executor error is returned unchanged", func(t *testing.T) {
		pgErr := &pgconn.PgError{Code: "23505"}
		exec := &fakeExecutor{err: pgErr}
		_, err := withExec(exec).NewDelete().Model((*User)(nil)).Where("id > 0").Exec(ctx)
		var got *pgconn.PgError
		require.ErrorAs(t, err, &got)
		require.Equal(t, "23505", got.Code)
	})

	t.Run("Count scans through QueryRow", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("count"), []any{int64(42)})}
		n, err := withExec(exec).NewSelect().Model((*User)(nil)).Count(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(42), n)
		require.Equal(t, "QueryRow", exec.calls[0].method)
	})

	t.Run("Exists scans a bool", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("exists"), []any{true})}
		ok, err := withExec(exec).NewSelect().Model((*User)(nil)).Exists(ctx)
		require.NoError(t, err)
		require.True(t, ok)
	})

	t.Run("DB.Exec formats placeholders", func(t *testing.T) {
		exec := &fakeExecutor{tag: pgconn.NewCommandTag("UPDATE 1")}
		res, err := withExec(exec).Exec(ctx, "UPDATE users SET name = ? WHERE id = ?", "x", 5)
		require.NoError(t, err)
		require.Equal(t, int64(1), res.RowsAffected())
		require.Equal(t, "UPDATE users SET name = 'x' WHERE id = 5", exec.calls[0].sql)
	})
}

// fakeTx is a fakeExecutor that also satisfies pgx.Tx, so isTx sees it as a
// transaction. Only the DBExecutor methods are used by the tests.
type fakeTx struct {
	fakeExecutor
	pgx.Tx
}

func (t *fakeTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return t.fakeExecutor.Exec(ctx, sql, args...)
}

func (t *fakeTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return t.fakeExecutor.Query(ctx, sql, args...)
}

func (t *fakeTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return t.fakeExecutor.QueryRow(ctx, sql, args...)
}

func (t *fakeTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	return t.fakeExecutor.SendBatch(ctx, b)
}

func TestTxRequiredForWrites(t *testing.T) {
	ctx := context.Background()
	strict := func(exec pgcrud.DBExecutor) *pgcrud.DB {
		return pgcrud.New(nil,
			pgcrud.WithExecutorResolver(func(context.Context) pgcrud.DBExecutor { return exec }),
			pgcrud.WithTxRequiredForWrites())
	}

	t.Run("insert outside a transaction fails before reaching the executor", func(t *testing.T) {
		exec := &fakeExecutor{tag: pgconn.NewCommandTag("INSERT 0 1")}
		_, err := strict(exec).NewInsert().Model(&User{ID: 1, Name: "a"}).Exec(ctx)
		require.ErrorIs(t, err, pgcrud.ErrTxRequired)
		require.Empty(t, exec.calls)
	})

	t.Run("update and delete fail the same way", func(t *testing.T) {
		exec := &fakeExecutor{}
		_, err := strict(exec).NewUpdate().Model((*User)(nil)).Set("name = 'b'").Where("id = 1").Exec(ctx)
		require.ErrorIs(t, err, pgcrud.ErrTxRequired)
		_, err = strict(exec).NewDelete().Model((*User)(nil)).Where("id = 1").Exec(ctx)
		require.ErrorIs(t, err, pgcrud.ErrTxRequired)
		require.Empty(t, exec.calls)
	})

	t.Run("DB.Exec is a write", func(t *testing.T) {
		exec := &fakeExecutor{}
		_, err := strict(exec).Exec(ctx, "TRUNCATE users")
		require.ErrorIs(t, err, pgcrud.ErrTxRequired)
	})

	t.Run("raw non-select is a write, raw select is a read", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("id", "name"), []any{int64(1), "a"})}
		_, err := strict(exec).NewRaw("WITH x AS (SELECT 1) INSERT INTO users SELECT 1, 'a'").Exec(ctx)
		require.ErrorIs(t, err, pgcrud.ErrTxRequired)
		var u User
		require.NoError(t, strict(exec).NewRaw("SELECT id, name FROM users").Scan(ctx, &u))
		require.Equal(t, "a", u.Name)
	})

	t.Run("selects still run on the pool", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("id", "name"), []any{int64(1), "a"})}
		var u User
		require.NoError(t, strict(exec).NewSelect().Model(&u).Scan(ctx))
	})

	t.Run("writes run when the executor is a transaction", func(t *testing.T) {
		tx := &fakeTx{fakeExecutor: fakeExecutor{tag: pgconn.NewCommandTag("DELETE 1")}}
		res, err := strict(tx).NewDelete().Model((*User)(nil)).Where("id = 1").Exec(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(1), res.RowsAffected())
		require.Len(t, tx.fakeExecutor.calls, 1)
	})

	t.Run("the query hook sees ErrTxRequired", func(t *testing.T) {
		hook := &recordingHook{}
		db := strict(&fakeExecutor{}).WithQueryHook(hook)
		_, err := db.NewDelete().Model((*User)(nil)).Where("id = 1").Exec(ctx)
		require.ErrorIs(t, err, pgcrud.ErrTxRequired)
		require.ErrorIs(t, hook.last.Err, pgcrud.ErrTxRequired)
	})

	t.Run("option off allows writes on any executor", func(t *testing.T) {
		exec := &fakeExecutor{tag: pgconn.NewCommandTag("DELETE 1")}
		_, err := withExec(exec).NewDelete().Model((*User)(nil)).Where("id = 1").Exec(ctx)
		require.NoError(t, err)
	})
}

func TestScanAndCount(t *testing.T) {
	ctx := context.Background()

	t.Run("non-pool executor runs fetch then count in sequence", func(t *testing.T) {
		tx := &fakeTx{fakeExecutor: fakeExecutor{rows: newFakeRows(cols("id", "name"), []any{int64(1), "a"})}}
		var us []User
		// Limit forces the two-query path. The fake serves the same rows to both
		// calls: the fetch scans one user, the count reads the int64 from column 0.
		n, err := withExec(tx).NewSelect().Model(&us).Limit(10).ScanAndCount(ctx)
		require.NoError(t, err)
		require.Len(t, us, 1)
		require.Equal(t, int64(1), n)
		require.Equal(t, []string{"Query", "QueryRow"}, methods(tx.fakeExecutor.calls))
	})

	t.Run("without limit and offset a single query serves both", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("id", "name"), []any{int64(1), "a"}, []any{int64(2), "b"})}
		var us []User
		n, err := withExec(exec).NewSelect().Model(&us).ScanAndCount(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(2), n)
		require.Len(t, exec.calls, 1)
	})
}

func methods(calls []call) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.method
	}
	return out
}

type recordingHook struct {
	before, after int
	last          *pgcrud.QueryEvent
}

func (h *recordingHook) BeforeQuery(ctx context.Context, e *pgcrud.QueryEvent) context.Context {
	h.before++
	return ctx
}

func (h *recordingHook) AfterQuery(ctx context.Context, e *pgcrud.QueryEvent) {
	h.after++
	h.last = e
}

func TestQueryHooks(t *testing.T) {
	ctx := context.Background()

	t.Run("hook sees the command tag on success", func(t *testing.T) {
		hook := &recordingHook{}
		exec := &fakeExecutor{tag: pgconn.NewCommandTag("DELETE 2")}
		db := pgcrud.New(nil,
			pgcrud.WithExecutorResolver(func(context.Context) pgcrud.DBExecutor { return exec }),
			pgcrud.WithQueryHook(hook))
		_, err := db.NewDelete().Model((*User)(nil)).Where("id > 0").Exec(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, hook.before)
		require.Equal(t, 1, hook.after)
		require.Equal(t, int64(2), hook.last.Result.RowsAffected())
		require.NoError(t, hook.last.Err)
		require.Equal(t, "DELETE", hook.last.Operation())
	})

	t.Run("hook sees the error on failure", func(t *testing.T) {
		hook := &recordingHook{}
		exec := &fakeExecutor{err: errors.New("down")}
		db := withExec(exec).WithQueryHook(hook)
		_, err := db.NewDelete().Model((*User)(nil)).Where("id > 0").Exec(ctx)
		require.EqualError(t, err, "down")
		require.EqualError(t, hook.last.Err, "down")
	})
}
