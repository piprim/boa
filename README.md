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

- Queries use bun's `?` placeholders. pgcrud translates them to `$1..$n` and passes the
  values to pgx, so statements are prepared and cached per connection and parameters travel
  in binary. `q.String()` shows the SQL that is sent and `q.Args()` the values.
- A raw query that contains no `?` is passed to pgx unchanged with its args, so pgx-style
  SQL with `$1` works too. Do not mix `?` and `$n` in one query, and use such a raw query
  only on its own or as the first bound part of a larger statement: nested after other
  bound values its `$n` would point at the wrong parameters, so pgcrud rejects that case
  with an error.
- A parameter with nothing to type it, such as `SELECT ?` or `ColumnExpr("?", v)`, needs a
  cast in the SQL (`?::int8`); Postgres cannot infer its type from a bare placeholder.
- Untagged structs, maps and slices are sent as JSON; `bun:",array"` fields and
  `pgdialect.Array(v)` are sent as Postgres arrays; `pgdialect.Range` values are sent as
  text and typed by Postgres from the column or operator. Where nothing fixes the type, cast
  in the SQL: `?::daterange`.
- Postgres accepts at most 65535 parameters per statement. A larger statement fails with
  `pgcrud.ErrTooManyParams` before anything is sent; chunk the rows or use `pgx.CopyFrom`.
- Result columns arrive in pgx's preferred format, binary for most scalar types. Arrays,
  ranges and multiranges are requested in text because pgcrud parses them itself. If your
  pool registers extra types with a binary codec that pgcrud must scan as text, list their
  OIDs with `pgcrud.WithTextResultTypes(oids...)`.
- pgx's default execution mode caches prepared statements per connection. After a schema
  change a cached statement can fail with `cached plan must change result type`; pass
  `pgcrud.WithQueryExecMode(pgx.QueryExecModeCacheDescribe)` if the application alters
  tables while running.
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
