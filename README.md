# PgCrud

A Postgres-only CRUD ORM-like derived from [bun](https://github.com/uptrace/bun) that runs on
`pgxpool.Pool` and `pgx.Tx` instead of `database/sql`.

It keeps bun's query builders, relations and hooks for Select, Insert, Update, Delete, Raw
and Values queries, and drops migrations, fixtures, DDL builders and non-Postgres dialects.

## Usage

```go
pool, _ := pgxpool.New(ctx, dsn)
uow := uow.New(pool)                                   // your unit of work
db := pgcrud.New(pool, pgcrud.WithExecutorResolver(uow.Executor))

var users []User
err := db.NewSelect().Model(&users).Where("active = ?", true).Scan(ctx)

err = uow.WithTransaction(ctx, func(ctx context.Context) error {
    _, err := db.NewInsert().Model(&User{Name: "ann"}).Exec(ctx)
    return err
})
```

The resolver picks the executor for every query from the context: the `pgx.Tx` your unit of
work stored there, or the pool. pgcrud never begins, commits or rolls back a transaction.

Pass `pgcrud.WithTxRequiredForWrites()` to make Insert, Update, Delete, `DB.Exec` and
non-SELECT Raw queries fail with `pgcrud.ErrTxRequired` when no transaction is attached to
the context. Reads still run on the pool.

## Notes

- Queries are formatted with bun's `?` placeholders and sent as text with pgx's simple
  protocol. A literal `$1` reaches Postgres unbound and fails there.
- Errors are pgx errors: use `errors.Is(err, pgx.ErrNoRows)` and `errors.As(err, &pgErr)`
  with `*pgconn.PgError`.
- `SelectQuery.Rows` returns `pgx.Rows`, so `pgx.CollectRows` works for projections that do
  not need the model scanner.
- Model struct tags keep bun's `bun:"..."` syntax.

## Tests

```sh
docker compose up -d --wait
PGCRUD_TEST_DSN='postgres://postgres:postgres@localhost:5442/pgcrud?sslmode=disable' go test -count=1  ./...
```

Without `PGCRUD_TEST_DSN` the integration tests are skipped.

## License

BSD-2-Clause, inherited from bun. See LICENSE.
