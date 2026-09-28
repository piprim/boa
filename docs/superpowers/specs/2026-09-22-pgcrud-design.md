# pgcrud design

Date: 2026-09-22
Module: `github.com/piprim/pgcrud`
Location: `/home/pi/code/bun/pgcrud`, a nested directory inside the bun checkout (not part of the bun module or its git history)
Derived from: `github.com/uptrace/bun` at commit `16613b290b9c6b69f43df052894e0fbf443f6ff9`

## 1. Goal

A Postgres-only ORM derived from bun that:

- provides Select, Insert, Update, Delete, Raw and Values queries with bun's builder API, relations (has-one, belongs-to, has-many, many-to-many) and model and query hooks;
- executes every query through a pgx executor resolved from the request context by a unit of work owned by the application;
- runs on `pgxpool.Pool` and `pgx.Tx` directly, with no `database/sql` in the dependency graph;
- drops migrations, fixtures, DDL builders, the pgdriver, non-Postgres dialects and the extra packages.

The public API keeps bun's shape. `db.NewSelect().Model(&u).Where("id = ?", id).Scan(ctx)` reads the same.

## 2. Approach

Copy bun's root package, `schema`, `internal` and `dialect/pgdialect` into the new module, delete what is out of scope, then rewrite the four points where bun touches `database/sql`:

1. the `IConn` and `IDB` interfaces in `query_base.go` and the `DB`, `Conn` and `Tx` types in `db.go`;
2. the `Model` interface in `schema/hook.go`, whose `ScanRows` takes `*sql.Rows`;
3. the `Exec` and `scanOrExec` methods on the query builders, which return `sql.Result`;
4. `Dialect.Init`, which takes `*sql.DB` (removed, see 4.2).

Everything else, in particular query formatting, value appending, table reflection and relation handling, ports unchanged apart from the module path.

Alternatives rejected: wrapping bun through pgx's `stdlib` adapter (keeps bun's transaction types and full surface, so it meets neither goal) and rewriting the query builders on top of bun's `schema` package (three to four times the work, with drift risk in relation loading).

## 3. Package layout

```
pgcrud/
  go.mod                      github.com/piprim/pgcrud, go 1.25.0
  pgcrud.go                   package-level helpers (SafeQuery, In, NullZero, List, Tuple, hook interfaces, ...)
  db.go                       DB type, options, hooks wiring
  executor.go                 DBExecutor, ExecutorResolver, defaults   (new)
  hook.go                     QueryHook, QueryEvent
  model.go, model_map.go, model_map_slice.go, model_scan.go,
  model_slice.go, model_table_has_many.go, model_table_m2m.go,
  model_table_slice.go, model_table_struct.go
  query_base.go, query_select.go, query_insert.go, query_update.go,
  query_delete.go, query_raw.go, query_values.go
  relation_join.go, util.go, version.go
  schema/                     as in bun, see section 5 for the two signature changes
  internal/                   as in bun
  extra/bunjson/              bun's replaceable JSON hook, kept because schema imports it
  dialect/dialect.go          dialect.Name
  dialect/append.go           append helpers used by query_base.go
  dialect/feature/            feature flags
  dialect/sqltype/            SQL type names
  dialect/pgdialect/          as in bun minus inspector.go and alter_table.go
  docs/superpowers/specs/     this document
  docs/superpowers/plans/     the implementation plan
  docker-compose.yml          Postgres 16 for integration tests
  .github/workflows/ci.yml    build, vet, test with a Postgres service container
```

Dropped from bun: `query_table_create.go`, `query_table_drop.go`, `query_table_truncate.go`, `query_index_create.go`, `query_index_drop.go`, `query_column_add.go`, `query_column_drop.go`, `query_merge.go`, `migrate/`, `dbfixture/`, `extra/` (except `extra/bunjson`), `driver/`, `dialect/mssqldialect`, `dialect/mysqldialect`, `dialect/oracledialect`, `dialect/sqlitedialect`, `example/`, `internal/dbtest/`, `Makefile`, `CHANGELOG.md`, `package.json`, `commitlint.config.js`.

The `DB` methods `NewCreateTable`, `NewDropTable`, `NewCreateIndex`, `NewDropIndex`, `NewTruncateTable`, `NewAddColumn`, `NewDropColumn`, `NewMerge`, `Conn`, `BeginTx`, `RunInTx`, `DBStats` and `ResetModel` are removed. The `Conn(conn)` method on every query builder is removed.

Dependencies: `github.com/jackc/pgx/v5`, plus the ones bun's core already needs: `github.com/jinzhu/inflection`, `github.com/puzpuzpuz/xsync/v3`, `github.com/tmthrgd/go-hex`, `github.com/vmihailenco/msgpack/v5`, `github.com/vmihailenco/tagparser/v2`. Test-only: `github.com/stretchr/testify`.

## 4. DB and executor model

### 4.1 executor.go

```go
// DBExecutor is satisfied structurally by *pgxpool.Pool and pgx.Tx.
type DBExecutor interface {
    Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
    QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
    SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// ExecutorResolver returns the executor to use for a request.
// The application's unit of work returns the active pgx.Tx from ctx, else the pool.
type ExecutorResolver func(ctx context.Context) DBExecutor
```

The library defines `DBExecutor` itself so the application's unit-of-work package is not imported. The interface matches the application's extract method for method, so its `Executor` method can be passed as the resolver directly.

### 4.2 DB

```go
type DB struct {
    *noCopyState            // pool, resolver, dialect, tables, flags (as in bun)
    gen        schema.QueryGen
    queryHooks []QueryHook
}

func New(pool *pgxpool.Pool, opts ...DBOption) *DB
func WithExecutorResolver(fn ExecutorResolver) DBOption
func WithQueryHook(h QueryHook) DBOption
func WithDiscardUnknownColumns() DBOption   // and bun's other existing options that still apply
```

- `pool` may be nil for query building only (golden tests, SQL previews). Executing a query with a nil pool and no resolver returns an error.
- Without `WithExecutorResolver`, the default resolver returns the pool.
- `WithTxRequiredForWrites()` makes every write fail with the exported `ErrTxRequired` when the resolved executor is not a `pgx.Tx`. A write is an Insert, Update or Delete builder, `DB.Exec`, or a Raw query that the statement classifier does not recognise as a read. The classifier skips leading whitespace, `/* */` block comments, `--` line comments and opening parentheses, then takes the first keyword up to any whitespace or `(`. `SELECT`, `SHOW` and `VALUES` are reads. `EXPLAIN` is a read unless its options include `ANALYZE` (`EXPLAIN ANALYZE ...` or `EXPLAIN (..., ANALYZE, ...) ...`), because that form executes the statement. Everything else, including `WITH`, is a write, so misclassification only ever fails closed. Reads run on the pool as before. This turns a transaction that was not attached to the context into a loud failure at the first write instead of a partial commit. The check is one type assertion on the executor.
- The dialect is always `pgdialect.New()`; there is no dialect argument. `Dialect.Init` is removed from the interface: bun's pgdialect implements it as a no-op and never probes the server, and taking a `DBExecutor` there would make `schema` import the root package.

### 4.3 Execution path

`baseQuery.resolveConn` becomes:

```go
func (q *baseQuery) resolveExecutor(ctx context.Context) (DBExecutor, error) {
    exec := q.db.resolver(ctx)
    if exec == nil {
        return nil, ErrNilExecutor // "pgcrud: executor resolver returned nil"
    }
    return exec, nil
}
```

There is no per-query connection override, so `baseQuery.conn` is removed. `ScanAndCount` decides per call from the executor's type: when it is a `*pgxpool.Pool` the fetch and the count run concurrently as in bun; when it is anything else (a `pgx.Tx` or a dedicated `*pgxpool.Conn`, both single connections) they run sequentially.

`baseQuery.exec` calls `exec.Exec(ctx, query, pgx.QueryExecModeSimpleProtocol)` and returns the `pgconn.CommandTag`. `baseQuery._scan` calls `exec.Query(ctx, query, pgx.QueryExecModeSimpleProtocol)`, hands the `pgx.Rows` to `model.ScanRows`, and returns a synthetic command tag carrying the row count (built with `pgconn.NewCommandTag`), or `pgx.ErrNoRows` when zero rows were scanned into a single-row model with a destination.

Every public `Exec` on the query builders returns `(pgconn.CommandTag, error)`. `QueryEvent.Result` becomes `pgconn.CommandTag`. Query hooks are otherwise unchanged.

Values are inlined into the SQL text by bun's formatter, exactly as today. The simple-protocol mode is passed as the sole argument on every call, so no pool configuration is needed and Postgres returns every column in text format. Superseded: values are bound as `$n` parameters since `2026-09-27-bound-parameters-design.md`.

### 4.4 Transactions

The library never begins, commits or rolls back. The application's unit of work stores a `pgx.Tx` in the context and its `Executor` method returns it; the library only calls the resolver. Nested transactions, savepoints and rollback on error are the unit of work's responsibility.

## 5. Scanning through pgx rows

### 5.1 Mechanism kept from bun

Each model implements `sql.Scanner`. `ScanRows` builds a `dest` slice containing the model once per column and calls `rows.Scan(dest...)`; the model's `Scan(src any)` routes the value to the field selected by its internal column index, via the scanner functions in `schema/scan.go`. pgx supports `sql.Scanner` destinations. For a column whose type pgx knows, it routes the text value through the codec's `DecodeDatabaseSQLValue` and hands the scanner a typed Go value: `int64` for int2, int4 and int8, `float64` for float4 and float8, `bool`, `time.Time` for timestamp, timestamptz and date, `[]byte` for bytea and json or jsonb, and `string` for text, numeric, uuid, interval, inet, arrays and hstore. For an unknown type it passes the raw text as `string`, and `nil` for NULL. The scanners in `schema/scan.go` already accept all of these sources, and `internal.ParseTime` was written for bun's text-protocol driver, so any timestamp that arrives as text still decodes. Arrays, hstore, ranges, JSON, IP and msgpack types are text or byte based already.

### 5.2 Changes

- `schema.Model.ScanRows(ctx, rows pgx.Rows) (int, error)`. The seven model files change their signatures. `rows.Columns()` is replaced by a helper that maps `rows.FieldDescriptions()` to names.
- **bytea.** pgx's bytea codec decodes the `\x` hex text before handing the scanner a `[]byte`, so no hex decoding is needed in the library.
- **Map models.** `mapModel.Scan` in `model_map.go` used `sql.ColumnType.ScanType()` to choose the Go type of each map value. It now stores whatever typed value pgx handed it (see 5.1). It still clones `[]byte` as a defensive measure: in text format pgx already allocates a fresh slice for bytea and json, so the clone only matters if the protocol changes (see section 8). No OID table is needed.

- **Single-row paths.** `Count`, `Exists` and `ScanAndCount` use `exec.QueryRow(...).Scan(&n)` with the simple-protocol mode.
- **Escape hatch.** `SelectQuery.Rows(ctx)` returns `pgx.Rows`, so callers can use `pgx.CollectRows(rows, pgx.RowToStructByName[T])` for projections that do not need bun's schema. `DB.ScanRows` and `DB.ScanRow` keep working with `pgx.Rows`.

### 5.3 Why not pgx.CollectRows internally

CollectRows maps each row through pgx's own field mapping and returns a new slice. Bun scans in place into the caller's destination, routes columns through its own schema including relation prefixes such as `author__name`, and runs `BeforeScanRow` and `AfterScanRow` per row. The explicit loop (Next, Scan, Err, Close) stays.

## 6. Error handling

- pgx errors reach the caller unchanged, so `errors.As` against `*pgconn.PgError` and `errors.Is` against `pgx.ErrNoRows` work with no library wrappers.
- Every place bun returned `sql.ErrNoRows` returns `pgx.ErrNoRows`: single-struct `Scan`, `Count` and `Exists` on empty results, and Insert, Update and Delete with `Returning` into a single-row model.
- After the scan loop the library checks `rows.Err()` before closing. A scan error stops the loop, closes the rows and is returned as is.
- A resolver that returns nil produces the exported `ErrNilExecutor` instead of a nil-interface panic.
- With `WithTxRequiredForWrites()`, a write whose executor is not a `pgx.Tx` returns the exported `ErrTxRequired` before anything is sent to Postgres. Query hooks see that error in `AfterQuery`.
- Because queries are fully formatted, a literal `$1` in raw SQL reaches Postgres unbound and fails there. Placeholders must use bun's `?` syntax. Documented in the README.
- Bun's model and schema errors are unchanged, re-prefixed from `bun:` to `pgcrud:`.
- Query hooks receive the same error in `AfterQuery` as the caller.

## 7. Testing

All tests use `t.Run` per assertion or table row.

1. **Query generation, no database.** The Postgres cases from bun's `internal/dbtest` snapshot suite that concern kept builders move into the root package as golden tests, run against `New(nil)`. Covers Select, Insert, Update, Delete, Raw, Values, relations and joins, soft delete, Returning, On Conflict.
2. **Pure unit tests.** The `schema` package tests come across unchanged. New tests use a fake `DBExecutor` and fake `pgx.Rows` to cover executor resolution (default to pool, custom resolver receives the query context, nil resolver result returns `ErrNilExecutor`), the simple-protocol argument, struct, slice, scalar and map scanning, `pgx.ErrNoRows`, command tags, query hooks, `ErrTxRequired` for each write path with the option on, and the concurrent-or-sequential choice in `ScanAndCount`.
3. **Integration tests.** A test package reads `PGCRUD_TEST_DSN` and skips when unset. `docker-compose.yml` starts Postgres 16 locally; CI uses a Postgres service container. Cases: round trips for every scalar and Postgres type bun supports, arrays, hstore, ranges, JSON and bytea in text format; has-one, belongs-to, has-many and many-to-many loading; model and query hooks; `pgx.ErrNoRows` behaviour; and a copy of the application's unit of work verifying that queries inside `WithTransaction` run on the transaction, nested calls use a savepoint, a returned error rolls back, a write outside `WithTransaction` fails with `ErrTxRequired` when the option is on, and `ScanAndCount` is correct both inside and outside a transaction.

## 8. Out of scope and future enhancements

- **Real placeholders.** Implemented by `2026-09-27-bound-parameters-design.md`. The constraint recorded here about pgx handing a `sql.Scanner` the raw read buffer in binary format was checked against pgx 5.9.2 and does not apply: every registered codec's `DecodeDatabaseSQLValue` returns a typed value or a fresh copy. The binary hazard is arrays, ranges and multiranges, whose binary wire form the text parsers cannot read; they are requested in text.
- Removing feature-flag branches that only mattered for other databases.
- `SendBatch` is part of `DBExecutor` for interface parity with the application's unit of work but is not used by the library in this version.

## 9. Implementation notes

Where the finished module differs from, or refines, the text above.

- **Go version.** `go.mod` declares `go 1.25.0`, not 1.24, because pgx v5.9.2 requires it.
- **`extra/bunjson`.** Kept (two files) because `schema` imports it. It is bun's replaceable JSON hook, not one of the observability packages that section 8 leaves out. The layout in section 3 lists it.
- **Package helpers file.** bun's `bun.go` is `pgcrud.go` in this module.
- **Statement classifier.** The `EXPLAIN ... ANALYZE` check is comment-aware: block and line comments between `EXPLAIN` and `ANALYZE` (or the option list) are skipped just as they are before the first keyword, so `EXPLAIN /* c */ ANALYZE UPDATE ...` and `EXPLAIN -- c` followed by a newline and `ANALYZE DELETE ...` are writes. Opening parentheses are not skipped there, since `EXPLAIN (ANALYZE) ...` is the option-list form.
- **Resolver contract.** Only a nil interface returned by the resolver is reported as `ErrNilExecutor`. A typed nil, such as a nil `pgx.Tx` inside a non-nil `DBExecutor`, is not detected; this is documented on `ExecutorResolver` and `WithExecutorResolver`.
- **Rows on error.** `SelectQuery.Rows` and `DB.Query` may return a non-nil, already-closed `pgx.Rows` together with an error (bun returned nil rows), so callers must check the error first.
- **Snapshot cases.** The ids of the golden cases in `query_test.go` skip the 34 bun cases that used builders pgcrud does not provide or branched on the dialect name, so those ids and snapshot files are intentionally absent.
- **CI workflow.** The workflow file is at `pgcrud/.github/workflows/ci.yml`. GitHub only runs workflows from a repository root, so it takes effect once pgcrud is its own repository.
- **Integration tests not executed.** The four `TestIntegration*` functions skip without `PGCRUD_TEST_DSN` and were not run in the sandbox where the port was built. Run them with the command in the README (`docker compose up -d --wait`, then `PGCRUD_TEST_DSN=... go test ./...`).
