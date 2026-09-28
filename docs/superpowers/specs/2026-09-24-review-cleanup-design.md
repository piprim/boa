# pgcrud review cleanup: design

Date: 2026-09-24. Supersedes the package layout in section 3 and the first bullet of section 8 of `2026-09-22-pgcrud-design.md`.

## 1. Goal

Act on all nine recommendations of the architecture review in `review.md` so that:

- the exported API only promises behaviour that can occur on Postgres;
- the choice of pgx execution protocol and the classification of SQL statements each live in exactly one place;
- table metadata is owned by `DB`, not by the dialect, and no hidden second registry exists;
- the pgdialect value types changed by the port have round-trip tests.

Constraints and assumptions:

- The module has no external consumers. Removing exported methods is acceptable and needs no deprecation period.
- The golden test suite in `query_test.go`, the in-memory executor tests and the integration suite stay green after every stage.
- The project's GitNexus rules apply during implementation: impact analysis before each symbol edit, `detect_changes` before the user commits. The user commits; the implementation never runs `git commit`.

## 2. Sequencing

Five stages, each leaving `go build ./... && go vet ./... && go test ./...` green so they can be committed separately.

| Stage | Review items | Summary |
|-------|--------------|---------|
| 1 | 3 | Statement classifier in `internal/sqlstmt` |
| 2 | 4, 5 | pgx call policy on `DB`; concurrency capability for `ScanAndCount` |
| 3 | 1, 2, 9 | Collapse the dialect layer to Postgres; README wording |
| 4 | 6, 7 | `DB` owns `schema.Tables`; cache signatures lose the dialect parameter |
| 5 | 8 | Round-trip tests for pgdialect value types |

Stages 1 and 2 are small, independent and test-backed, so they land before the large deletion in stage 3. Stage 4 depends on stage 3 because it needs the slimmed `schema.Dialect`. Stage 5 is last so that it tests the final API.

## 3. Statement classifier (stage 1)

### 3.1 Package

New package `internal/sqlstmt` with two functions. The tokenizer currently in `db.go` (`skipStatementPrefix`, `skipSpaceAndComments`, `readKeyword`, `explainAnalyzes`, `optionList`, `containsWord`) moves there unchanged.

```go
// Operation returns the first keyword of sql, uppercased, after skipping
// leading whitespace, block and line comments and opening parentheses.
// It is capped at 16 bytes. An empty statement yields "".
func Operation(sql string) string

// IsRead reports whether sql is a read-only statement: SELECT, SHOW, VALUES,
// or EXPLAIN without ANALYZE. Anything else, including WITH, an empty
// statement and unknown keywords, is a write so that callers fail closed.
func IsRead(sql string) bool
```

### 3.2 Callers

- `RawQuery.Operation()` returns `sqlstmt.Operation(q.query)` instead of the constant `"SELECT"`.
- `QueryEvent.Operation()` falls back to `sqlstmt.Operation(e.Query)`; `queryOperation` in `hook.go` is deleted.
- `baseQuery.isWrite` drops the `*RawQuery` type switch and becomes:

```go
switch iquery.Operation() {
case "SELECT", "VALUES":
    return false
case "INSERT", "UPDATE", "DELETE":
    return true
}
return !sqlstmt.IsRead(query)
```

Builder queries report constant operations and are trusted. A raw query reports the keyword the same tokenizer found, so hooks and the write guard agree by construction. A raw statement whose keyword is not in the two lists (WITH, EXPLAIN, TRUNCATE, an empty string) is classified from its text and fails closed, exactly as today.

### 3.3 Behaviour changes

- Hooks observing `DB.Exec`, `DB.Query` and `DB.QueryRow` now see a normalized keyword (`SELECT`, not `select`) and comments before the keyword are skipped.
- Hooks observing a raw `UPDATE` now see `UPDATE`, not `SELECT`.

### 3.4 Tests

The classifier tests in `db_internal_test.go` move to `internal/sqlstmt` and gain table-driven `t.Run` cases for `Operation`: leading comment, leading parenthesis, lowercase keyword, empty string, keyword longer than 16 bytes. A test in `db_test.go` asserts that a hook sees `UPDATE` for a raw update.

## 4. pgx call policy and concurrency capability (stage 2)

### 4.1 One place for the execution mode

Three unexported methods on `DB`, placed in `executor.go`, become the only production code that names `pgx.QueryExecModeSimpleProtocol`:

```go
func (db *DB) execSQL(ctx context.Context, exec DBExecutor, sql string) (pgconn.CommandTag, error)
func (db *DB) querySQL(ctx context.Context, exec DBExecutor, sql string) (pgx.Rows, error)
func (db *DB) queryRowSQL(ctx context.Context, exec DBExecutor, sql string) pgx.Row
```

Their shared doc comment records the invariant from section 8 of the original spec: with the simple protocol every value reaches a scanner as a freshly allocated slice or an immutable string, so scanners may retain what they receive. Switching modes requires every retaining scanner to copy first.

The eight call sites (`DB.Exec`, `DB.Query`, `DB.QueryRow`, `baseQuery._scan`, `baseQuery.exec`, `SelectQuery.Rows`, `SelectQuery.Count`, `SelectQuery.Exists`) call these methods with the executor they already resolved.

### 4.2 Concurrency capability

`isPool` in `executor.go` is replaced by:

```go
// poolAcquirer is satisfied by *pgxpool.Pool and by any executor that embeds
// it. Only such executors may run two queries at once.
type poolAcquirer interface {
    Acquire(ctx context.Context) (*pgxpool.Conn, error)
}

func supportsConcurrentQueries(exec DBExecutor) bool
```

`ScanAndCount` runs fetch and count concurrently when the predicate is true and sequentially otherwise. `pgx.Tx` and `*pgxpool.Conn` never satisfy it. A wrapper that embeds the pool inherits `Acquire` and keeps concurrency; a wrapper that does not embed the pool degrades to sequential, which is safe.

Rejected: a query or DB option that forces concurrency. The resolver picks the executor per context, so the caller cannot know at call time whether the executor is a single connection, and forcing concurrency on a `pgx.Tx` fails with a busy connection. Rejected for now: an exported marker interface for non-embedding wrappers. It can be added later without breaking anything.

`ScanAndCount` still resolves the executor once for the predicate and once per sub-query. That is unchanged; the resolver is a context lookup.

### 4.3 Tests

- A table-driven test in `db_test.go` runs every public execution path against the fake executor and asserts each recorded call carries exactly `[]any{pgx.QueryExecModeSimpleProtocol}`. Paths: `DB.Exec`, `DB.Query`, `DB.QueryRow`, `SelectQuery.Scan`, `SelectQuery.Rows`, `SelectQuery.Count`, `SelectQuery.Exists`, `InsertQuery.Exec`, `UpdateQuery.Exec`, `DeleteQuery.Exec`, `RawQuery.Exec`, `RawQuery.Scan`.
- A unit test for `supportsConcurrentQueries` with a `*pgxpool.Pool`, a `pgx.Tx` fake, and a struct that embeds `*pgxpool.Pool`.

## 5. Collapse the dialect layer to Postgres (stage 3)

### 5.1 Packages and types removed

- Package `dialect/feature` is deleted.
- `dialect.Name` and its constants are deleted from package `dialect`. The package keeps its append helpers (`AppendNull`, `AppendBool`, `AppendError`, `AppendFloat32`, `AppendFloat64`).
- `schema.Field.CreateTableSQLType` is deleted, together with the code in `schema.Tables.Get` that fills it and the serial-type assignment in `pgdialect.Dialect.onField`. The `pgTypeSmallSerial`, `pgTypeSerial` and `pgTypeBigSerial` constants go if nothing else uses them.

### 5.2 `schema.Dialect` after this stage

```go
type Dialect interface {
    IdentQuote() byte
    AppendUint32(b []byte, n uint32) []byte
    AppendUint64(b []byte, n uint64) []byte
    AppendTime(b []byte, tm time.Time) []byte
    AppendString(b []byte, s string) []byte
    AppendBytes(b []byte, bs []byte) []byte
    AppendJSON(b, jsonb []byte) []byte
    AppendBool(b []byte, v bool) []byte
    OnTable(table *Table)
    DefaultSchema() string
    Tables() *Tables // removed in stage 4
}
```

`Name`, `Features`, `AppendSequence` and `DefaultVarcharLen` are removed from the interface, from `pgdialect.Dialect` and from `schema.nopDialect`.

- `QueryGen.IsNop` no longer compares dialect names. `QueryGen` gains an unexported `nop bool` field that `NewNopQueryGen` sets; `IsNop` returns it.
- `DB.String()` returns the constant `"DB<dialect=pg>"`.
- `DB.HasFeature`, `QueryGen.HasFeature` and `baseQuery.hasFeature` are deleted.

### 5.3 Feature checks resolve to constants

Every feature check is replaced by Postgres's fixed answer.

Flags that are true on Postgres; the guarded code stays, unguarded: `CTE`, `WithValues`, `Returning`, `InsertReturning`, `DefaultPlaceholder`, `DoubleColonCast`, `InsertTableAlias`, `UpdateTableAlias`, `DeleteTableAlias`, `TableCascade`, `InsertOnConflict`, `SelectExists`, `CompositeIn`, `FKDefaultOnAction`, `DeleteReturning`.

Flags that are false on Postgres; the guarded branch is deleted: `Output`, `ValuesRow`, `OffsetFetch`, `InsertOnDuplicateKey`, `InsertIgnore`, `UpdateMultiTable`, `UpdateOrderLimit`, `DeleteOrderLimit`, `Identity`.

Dialect-name branches resolve the same way: the `dialect.PG` branches for materialized CTEs stay unconditionally; the `MySQL`, `SQLite`, `MSSQL` and `Oracle` branches are deleted. The UNION wrapping guarded by "not SQLite" becomes unconditional.

### 5.4 Exported API removed

Rule: an exported method is deleted when, on Postgres, its only effect is a no-op, a `NotSupportError`, or SQL that Postgres rejects.

- `SelectQuery`: `UseIndex`, `UseIndexForJoin`, `UseIndexForOrderBy`, `UseIndexForGroupBy`, `IgnoreIndex`, `IgnoreIndexForJoin`, `IgnoreIndexForOrderBy`, `IgnoreIndexForGroupBy`, `ForceIndex`, `ForceIndexForJoin`, `ForceIndexForOrderBy`, `ForceIndexForGroupBy`; the `idxHintsQuery` mixin and `indexHints` type in `query_base.go`; the hint cloning in `SelectQuery.Clone`.
- `DeleteQuery`: `Order`, `OrderExpr`, `Limit`.
- `UpdateQuery`: `Order`, `OrderExpr`, `Limit`.
- `InsertQuery`: `Replace` (renders `REPLACE INTO`). `Ignore` stays and always renders `ON CONFLICT DO NOTHING`.
- `DB.UpdateFQN` simplifies to return the bare column, since `UpdateMultiTable` is false.

The golden cases in `query_test.go` that exercise these methods, and their snapshot files under `testdata`, are deleted.

### 5.5 Dialect options

- `pgdialect.WithoutFeature` and the `features` field are deleted.
- `pgdialect.WithAppendUintAsInt` stays. It becomes reachable through a new DB option `pgcrud.WithAppendUintAsInt()`.

To make that possible, `pgcrud.New` builds in two phases. Options are applied to an unexported `config` struct first. `New` then constructs the dialect with the collected dialect options, the table registry (stage 4), the query generator and finally the `DB`. Query hooks registered through `WithQueryHook` are collected in the config and their `Init(db)` runs after the `DB` exists, preserving today's behaviour. `DBOption` stays an opaque exported type; its underlying function signature changes, which no consumer depends on.

### 5.6 README

Line 4 becomes "instead of a `database/sql` driver". No other README changes.

### 5.7 Tests

- The golden suite must pass after removing the deleted cases; no snapshot for a surviving case may change.
- A `db_test.go` case constructs `New(nil, WithAppendUintAsInt())` and asserts a `uint64` above `math.MaxInt64` renders as a negative literal.

## 6. Tables ownership and cache signatures (stage 4)

### 6.1 Ownership

- `DB` gains `tables *schema.Tables`, constructed in `New` as `schema.NewTables(dialect)`. `DB.Table` and `DB.RegisterModel` use it. `DB.Dialect()` stays and returns the slim interface.
- `schema.Dialect.Tables()` is removed. `pgdialect.Dialect` loses its `tables` field and `Tables()` method. The package-level `pgDialect` singleton in `dialect/pgdialect/dialect.go` is deleted.
- `schema.Tables` keeps its `dialect` field. It is the registry's single dialect, used for `OnTable`, `DefaultSchema` and identifier quoting.
- `schema.Table` replaces `dialect Dialect` with `tables *Tables`. `Table.init` takes `*Tables`. Every `t.dialect.Tables()` becomes `t.tables`; every other `t.dialect` use becomes `t.tables.dialect`. The `FKDefaultOnAction` check in `initRelations` is already gone after stage 3.
- `Table.Dialect()` is deleted. The two sites in `model.go` that reach a related table through it call `db.Table(typ)` instead.
- `schema.QueryGen` gains `tables *Tables`. `NewQueryGen(dialect Dialect, tables *Tables)` takes both. The struct named-args path in `querygen.go` uses `gen.tables.Get`. `nopQueryGen` is built with `NewTables(newNopDialect())`. `Table.quoteIdent` builds `NewQueryGen(t.tables.dialect, t.tables)`.

### 6.2 Cache signatures

`schema.Appender(typ)`, `schema.FieldAppender(field)` and `schema.FieldScanner(field)` lose their `Dialect` parameter. The caches are keyed by `reflect.Type` only, and every appender reaches the dialect through `gen.Dialect()` at call time, so the parameter never influenced the result. Callers in `querygen.go`, `table.go`, `dialect/pgdialect/array.go` and `dialect/pgdialect/append.go` are updated.

Once `Appender` needs no dialect, `pgdialect.arrayAppender`, `arrayElemAppender` and `hstoreAppender` become package-level functions. `Array()` and `HStore()` call them directly, which is what removes the last use of the singleton.

### 6.3 Tests

- `schema/table_test.go`, `issue1243_test.go` and the lookup benchmarks construct `NewTables(newNopDialect())` as they do today; only `Table.init` call sites inside `schema` change.
- A `db_test.go` case asserts that two `DB` values built by `New` do not share table metadata: registering a model on one leaves the other's `Table` lookup to build its own.

## 7. Round-trip tests for pgdialect value types (stage 5)

Table-driven `t.Run` tests in `dialect/pgdialect`, using `schema.NewQueryGen(pgdialect.New(), nil)` where a generator is needed:

- `Range[T].Scan` from `string`, `[]byte` and `nil` for `int64` and `time.Time` element types, covering inclusive, exclusive, infinite and empty bounds; and `Range[T].AppendQuery` for the same shapes.
- `MultiRange[T].AppendQuery` for empty, one-element and two-element inputs.
- `HStoreValue.Scan` from `string`, `[]byte` and `nil` into `map[string]string`, and `Value()` returning the scanned map.
- `ArrayValue.Scan` from `string`, `[]byte` and `nil` into `[]int64`, `[]string` and `[]time.Time`, and `Value()` returning the scanned slice.

Each case asserts both the decoded value and, where applicable, that appending it reproduces the source literal.

## 8. Error handling

No new error values. The classifier keeps failing closed for unknown statements. `ErrNilExecutor` and `ErrTxRequired` paths are unchanged, and every execution path still calls `beforeQuery` and `afterQuery`, including the resolver-error branches. Removing feature-gated methods removes the only uses of `NotSupportError`, which goes with the feature package.

## 9. Verification

After every stage: `go build ./... && go vet ./... && go test ./...`.

At the end:

- integration suite: `docker compose up -d --wait` then `go test ./...` with `PGCRUD_TEST_DSN` set to the compose database on port 5442;
- `go run golang.org/x/tools/cmd/deadcode@latest -test ./...` reports nothing unreachable except intentionally public API;
- `grep -rn "feature\.\|dialect\.\(PG\|MySQL\|MSSQL\|Oracle\|SQLite\)\|QueryExecMode" --include='*.go' .` finds only the three methods in `executor.go` and their test;
- GitNexus re-index (`node .gitnexus/run.cjs analyze`) and `detect_changes` reported to the user before they commit;
- section 3 and the first bullet of section 8 in `2026-09-22-pgcrud-design.md` get a one-line note pointing here.

## 10. Out of scope

- Real `$n` placeholders and binary transfer (still the first enhancement after the port).
- Resolving the executor once per `ScanAndCount` instead of once per sub-query.
- Removing package-level state in `schema` other than the pgdialect singleton: `tableNameInflector`, the JSON provider and the logger stay.
- Changing `Version()`.
