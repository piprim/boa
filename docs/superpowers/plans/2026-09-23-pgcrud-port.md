# pgcrud Port Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `github.com/piprim/pgcrud`, a Postgres-only CRUD ORM derived from bun that executes through a pgx executor resolved from the request context.

**Architecture:** Copy bun's root package, `schema`, `internal` and `dialect/pgdialect` into a new module, delete everything outside CRUD scope, and rewrite the four points where bun touches `database/sql`: the connection interfaces and DB wrappers, `Model.ScanRows`, the `sql.Result` returns on the query builders, and `Dialect.Init`. Query formatting, value appending, table reflection and relation loading port unchanged apart from the module path.

**Tech Stack:** Go 1.24+ (toolchain 1.27.1 on this machine), `github.com/jackc/pgx/v5` v5.9.2 (already in the module cache), testify for tests. Postgres 16 in Docker for integration tests.

**Spec:** `/home/pi/code/bun/pgcrud/docs/superpowers/specs/2026-09-22-pgcrud-design.md`

## Global Constraints

- Module path is `github.com/piprim/pgcrud`; project root is `/home/pi/code/bun/pgcrud`. It is a separate Go module nested inside the bun checkout. The bun checkout at `/home/pi/code/bun` is the read-only source to copy from, at commit `16613b290b9c6b69f43df052894e0fbf443f6ff9`.
- **Never run any `git` command.** The user commits themselves. Where a step would normally commit, stop and report which files are ready instead.
- `go 1.24` in go.mod. Dependencies: `github.com/jackc/pgx/v5` plus what bun's core already needs (`jinzhu/inflection`, `puzpuzpuz/xsync/v3`, `tmthrgd/go-hex`, `vmihailenco/msgpack/v5`, `vmihailenco/tagparser/v2`). Test-only: `github.com/stretchr/testify`. No `database/sql` driver anywhere; the stdlib `database/sql` package may still be imported for `sql.Scanner` and the `sql.Null*` types.
- The public builder API keeps bun's shape and bun's struct tag name `bun:"..."` stays as is. Only error message prefixes change from `bun:` to `pgcrud:`.
- Every query is sent fully formatted with `pgx.QueryExecModeSimpleProtocol` as the only argument. No `$1` placeholders in this version.
- The library never begins, commits or rolls back a transaction.
- Every test uses `t.Run` per assertion or table row.
- The word `bun` may remain in identifiers copied from bun (for example `BaseModel`, the `bun` tag) but not in error strings or documentation that describes pgcrud itself.

---

## File Structure

Created or modified relative to `/home/pi/code/bun/pgcrud`:

| Path | Responsibility |
| --- | --- |
| `go.mod`, `go.sum` | Module definition |
| `executor.go` | `DBExecutor`, `ExecutorResolver`, `ErrNilExecutor`, pool resolver (new) |
| `db.go` | `DB`, options, `New`, executor resolution, `Exec`/`Query`/`QueryRow`, `ScanRows`/`ScanRow` (rewritten) |
| `hook.go` | `QueryHook`, `QueryEvent` with `pgconn.CommandTag` result (rewritten) |
| `pgcrud.go` | Package aliases and hook interfaces, from bun's `bun.go` minus DDL hooks |
| `model.go` + `model_*.go` | Models; `ScanRows` takes `pgx.Rows`; `columnNames` helper |
| `query_base.go` | `baseQuery`; executor resolution; `scan`/`exec` returning `pgconn.CommandTag` |
| `query_select.go`, `query_insert.go`, `query_update.go`, `query_delete.go`, `query_raw.go`, `query_values.go` | Builders; `Conn` removed; `pgconn.CommandTag` returns |
| `relation_join.go`, `util.go`, `version.go` | Unchanged apart from package name |
| `schema/` | bun's schema; `Model.ScanRows` takes `pgx.Rows`; `Dialect.Init` removed |
| `internal/` | Unchanged |
| `dialect/dialect.go`, `dialect/append.go`, `dialect/feature/`, `dialect/sqltype/` | Unchanged |
| `dialect/pgdialect/` | bun's pgdialect minus migration and inspector code, no import of the root package |
| `executor_test.go`, `db_test.go` | Fake executor and unit tests (new) |
| `query_test.go`, `snapshot_test.go`, `testdata/snapshots/` | Golden SQL tests ported from bun |
| `integration_test.go`, `uow_test.go` | Real Postgres tests, skipped without `PGCRUD_TEST_DSN` |
| `docker-compose.yml`, `.github/workflows/ci.yml` | Test infrastructure |
| `README.md`, `LICENSE` | Documentation and the BSD-2 license inherited from bun |

Spec deviations found while reading the code, applied in this plan (the spec is updated in Task 6):

1. bun's `pgdialect.Init` is already a no-op and there is no server version probe, so `Init` is removed from `schema.Dialect` instead of taking a `DBExecutor`. That also avoids an import cycle between `schema` and the root package.
2. pgx hands a `sql.Scanner` typed Go values for known column types even in text format (int64, float64, bool, time.Time, []byte for bytea and json, string otherwise), because it routes through each codec's `DecodeDatabaseSQLValue`. So no bytea hex decoding is needed and the map model needs no OID table: it stores what pgx gives it, cloning `[]byte`.
3. `ScanAndCount` runs its two queries concurrently only when the executor is a `*pgxpool.Pool`; for a `pgx.Tx` or `*pgxpool.Conn` (single connections) it runs them sequentially.
4. The nil-resolver error is exported as `ErrNilExecutor` so callers can use `errors.Is`.
5. `dialect/append.go` is kept; `query_base.go` imports it.
6. `WithTxRequiredForWrites()` and `ErrTxRequired` make writes fail loudly when the executor is not a `pgx.Tx`.

---

### Task 1: Module skeleton and the pure packages

**Files:**
- Create: `go.mod`
- Create (copied): `internal/**`, `dialect/dialect.go`, `dialect/append.go`, `dialect/feature/feature.go`, `dialect/sqltype/sqltype.go`, `schema/**`, `dialect/pgdialect/**`
- Modify: `schema/hook.go`, `schema/dialect.go`, `dialect/pgdialect/dialect.go`, `dialect/pgdialect/sqltype.go`

**Interfaces:**
- Produces: `schema.Model` with `ScanRows(ctx context.Context, rows pgx.Rows) (int, error)`; `schema.Dialect` without `Init`; `pgdialect.New(opts ...DialectOption) *Dialect` importing nothing from the root package.

- [ ] **Step 1: Create the module and copy the packages**

```bash
cd /home/pi/code/bun/pgcrud
mkdir -p internal dialect schema
cp -r /home/pi/code/bun/internal/. internal/
rm -rf internal/dbtest
cp /home/pi/code/bun/dialect/dialect.go /home/pi/code/bun/dialect/append.go dialect/
cp -r /home/pi/code/bun/dialect/feature dialect/feature
cp -r /home/pi/code/bun/dialect/sqltype dialect/sqltype
cp -r /home/pi/code/bun/dialect/pgdialect dialect/pgdialect
cp -r /home/pi/code/bun/schema/. schema/
rm -f dialect/pgdialect/go.mod dialect/pgdialect/go.sum \
      dialect/pgdialect/inspector.go dialect/pgdialect/alter_table.go \
      dialect/pgdialect/version.go dialect/pgdialect/sqltype_test.go
cat > go.mod <<'EOF'
module github.com/piprim/pgcrud

go 1.24
EOF
```

- [ ] **Step 2: Rewrite import paths and error prefixes in the copied packages**

```bash
cd /home/pi/code/bun/pgcrud
find internal dialect schema -name '*.go' -print0 | xargs -0 sed -i \
  -e 's#github.com/uptrace/bun#github.com/piprim/pgcrud#g' \
  -e 's#"bun: #"pgcrud: #g'
grep -rn "uptrace" internal dialect schema || echo "no uptrace imports left"
```

Expected: the final grep prints `no uptrace imports left`.

- [ ] **Step 3: Change `schema.Model.ScanRows` to take pgx rows**

Edit `schema/hook.go`. Replace the import block and the `Model` interface:

```go
package schema

import (
	"context"
	"reflect"

	"github.com/jackc/pgx/v5"
)

type Model interface {
	ScanRows(ctx context.Context, rows pgx.Rows) (int, error)
	Value() any
}
```

Leave the rest of the file unchanged.

- [ ] **Step 4: Remove `Init` from `schema.Dialect`**

Edit `schema/dialect.go`:
- Delete the line `Init(db *sql.DB)` from the `Dialect` interface.
- Delete the method `func (d *nopDialect) Init(*sql.DB) {}`.
- Delete `"database/sql"` from the import block.

- [ ] **Step 5: Cut migration and version code out of pgdialect**

Edit `dialect/pgdialect/dialect.go`:
- Delete the whole `func init() { ... }` block that compares `Version()` with `bun.Version()`.
- Delete the `var pgDialect = New()` line only if nothing else in the package uses `pgDialect` (check with `grep -n pgDialect dialect/pgdialect/*.go`; if it is used, keep it).
- Delete the two lines `var _ sqlschema.InspectorDialect = (*Dialect)(nil)` and `var _ sqlschema.MigratorDialect = (*Dialect)(nil)`.
- Delete the method `func (d *Dialect) Init(*sql.DB) {}`.
- Remove `"database/sql"`, `"fmt"`, `"github.com/piprim/pgcrud"` and `"github.com/piprim/pgcrud/migrate/sqlschema"` from the imports if they are now unused (`fmt` may still be used elsewhere in the file; the compiler will say).

Edit `dialect/pgdialect/sqltype.go`:
- Delete the methods `CompareType` and `checkVarcharLen` and the `var ( char = ...; varchar = ...; timestampTz = ...; bigint = ...; integer = ...; smallint = ... )` block that only they use.
- If `newAliases` and the `typeAlias` type are now unused, delete them too.
- Remove the `"github.com/piprim/pgcrud/migrate/sqlschema"` import and `"strings"` if it became unused.

- [ ] **Step 6: Build and test the pure packages**

```bash
cd /home/pi/code/bun/pgcrud
go mod tidy
go build ./internal/... ./dialect/... ./schema/...
go vet ./internal/... ./dialect/... ./schema/...
go test ./internal/... ./dialect/... ./schema/...
```

Expected: `go mod tidy` adds pgx v5.9.2 and bun's dependencies to go.mod; build, vet and tests pass. If a schema test asserts on a message starting with `bun:`, change the expectation to `pgcrud:`.

- [ ] **Step 7: Report files ready to commit**

Do not run git. Tell the user the module skeleton, `internal`, `dialect` and `schema` are ready to commit.

---

### Task 2: Port the root package

**Files:**
- Create: `executor.go`, `db.go`, `hook.go`, `pgcrud.go`
- Create (copied and edited): `model.go`, `model_map.go`, `model_map_slice.go`, `model_scan.go`, `model_slice.go`, `model_table_has_many.go`, `model_table_m2m.go`, `model_table_slice.go`, `model_table_struct.go`, `query_base.go`, `query_select.go`, `query_insert.go`, `query_update.go`, `query_delete.go`, `query_raw.go`, `query_values.go`, `relation_join.go`, `util.go`, `version.go`, `util_test.go`, `list_tuple_test.go`, `query_select_clone_test.go`

**Interfaces:**
- Consumes: `schema.Model` and `schema.Dialect` from Task 1.
- Produces:
  - `type DBExecutor interface { Exec(ctx, sql string, args ...any) (pgconn.CommandTag, error); Query(ctx, sql string, args ...any) (pgx.Rows, error); QueryRow(ctx, sql string, args ...any) pgx.Row; SendBatch(ctx, *pgx.Batch) pgx.BatchResults }`
  - `type ExecutorResolver func(ctx context.Context) DBExecutor`
  - `var ErrNilExecutor error`, `var ErrTxRequired error`
  - `func New(pool *pgxpool.Pool, opts ...DBOption) *DB`, `func WithExecutorResolver(fn ExecutorResolver) DBOption`, `func WithQueryHook(hook QueryHook) DBOption`, `func WithDiscardUnknownColumns() DBOption`, `func WithTxRequiredForWrites() DBOption`
  - `func (db *DB) Executor(ctx) (DBExecutor, error)`, `Exec`, `Query`, `QueryRow`, `ScanRows`, `ScanRow`, `NewSelect`, `NewInsert`, `NewUpdate`, `NewDelete`, `NewRaw`, `NewValues`
  - `(*SelectQuery).Exec(ctx, dest ...any) (pgconn.CommandTag, error)`, `Scan`, `Rows(ctx) (pgx.Rows, error)`, `Count`, `Exists`, `ScanAndCount`; the same `Exec`/`Scan` on Insert, Update, Delete and Raw.
  - `QueryEvent.Result pgconn.CommandTag`

- [ ] **Step 1: Copy the root files and rename the package**

```bash
cd /home/pi/code/bun/pgcrud
for f in model.go model_map.go model_map_slice.go model_scan.go model_slice.go \
         model_table_has_many.go model_table_m2m.go model_table_slice.go model_table_struct.go \
         query_base.go query_select.go query_insert.go query_update.go query_delete.go \
         query_raw.go query_values.go relation_join.go util.go version.go \
         util_test.go list_tuple_test.go query_select_clone_test.go; do
  cp /home/pi/code/bun/$f .
done
cp /home/pi/code/bun/bun.go pgcrud.go
find . -maxdepth 1 -name '*.go' -print0 | xargs -0 sed -i \
  -e 's/^package bun$/package pgcrud/' \
  -e 's/^package bun_test$/package pgcrud_test/' \
  -e 's#github.com/uptrace/bun#github.com/piprim/pgcrud#g' \
  -e 's#"bun: #"pgcrud: #g'
```

`db.go` and `hook.go` are not copied; they are written from scratch in steps 3 and 4.

- [ ] **Step 2: Write executor.go**

```go
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
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

var (
	_ DBExecutor = (*pgxpool.Pool)(nil)
	_ DBExecutor = (pgx.Tx)(nil)
)

// ExecutorResolver returns the executor to use for a request, typically the
// pgx.Tx stored in ctx by the application's unit of work, or the pool.
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
```

- [ ] **Step 3: Write db.go**

```go
package pgcrud

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/piprim/pgcrud/dialect/feature"
	"github.com/piprim/pgcrud/dialect/pgdialect"
	"github.com/piprim/pgcrud/internal"
	"github.com/piprim/pgcrud/schema"
)

const (
	discardUnknownColumns internal.Flag = 1 << iota
	txRequiredForWrites
)

// DBOption mutates DB configuration during construction.
type DBOption func(db *DB)

// WithOptions applies multiple DBOption values at once.
func WithOptions(opts ...DBOption) DBOption {
	return func(db *DB) {
		for _, opt := range opts {
			opt(db)
		}
	}
}

// WithDiscardUnknownColumns ignores columns returned by queries that are not present in models.
func WithDiscardUnknownColumns() DBOption {
	return func(db *DB) {
		db.flags = db.flags.Set(discardUnknownColumns)
	}
}

// WithExecutorResolver sets the function that picks the executor for each query.
// Pass the Executor method of the application's unit of work.
func WithExecutorResolver(fn ExecutorResolver) DBOption {
	return func(db *DB) {
		if fn != nil {
			db.resolver = fn
		}
	}
}

// WithTxRequiredForWrites makes Insert, Update, Delete, DB.Exec and non-SELECT
// Raw queries fail with ErrTxRequired unless the resolved executor is a pgx.Tx.
func WithTxRequiredForWrites() DBOption {
	return func(db *DB) {
		db.flags = db.flags.Set(txRequiredForWrites)
	}
}

// WithQueryHook registers a query hook at construction time.
func WithQueryHook(hook QueryHook) DBOption {
	return func(db *DB) {
		if initer, ok := hook.(queryHookIniter); ok {
			initer.Init(db)
		}
		db.queryHooks = append(db.queryHooks, hook)
	}
}

// DB is the central access point for building and executing queries.
type DB struct {
	// Must be a pointer so we copy the whole state, not individual fields.
	*noCopyState

	gen        schema.QueryGen
	queryHooks []QueryHook
}

// noCopyState contains DB fields that must not be copied on clone().
type noCopyState struct {
	pool     *pgxpool.Pool
	resolver ExecutorResolver
	dialect  schema.Dialect

	flags internal.Flag
}

// New creates a DB on top of pool. pool may be nil to build queries without
// executing them; executing then returns ErrNilExecutor unless a resolver is set.
func New(pool *pgxpool.Pool, opts ...DBOption) *DB {
	dialect := pgdialect.New()

	db := &DB{
		noCopyState: &noCopyState{
			pool:     pool,
			resolver: poolResolver(pool),
			dialect:  dialect,
		},
		gen: schema.NewQueryGen(dialect),
	}

	for _, opt := range opts {
		opt(db)
	}

	return db
}

// String returns a string representation of the DB showing its dialect.
func (db *DB) String() string {
	return "DB<dialect=" + db.dialect.Name().String() + ">"
}

// Pool returns the pool passed to New. It may be nil.
func (db *DB) Pool() *pgxpool.Pool {
	return db.pool
}

// Executor resolves the executor for ctx through the configured resolver.
func (db *DB) Executor(ctx context.Context) (DBExecutor, error) {
	exec := db.resolver(ctx)
	if exec == nil {
		return nil, ErrNilExecutor
	}
	return exec, nil
}

// writeExecutor resolves the executor for a write and enforces
// WithTxRequiredForWrites.
func (db *DB) writeExecutor(ctx context.Context) (DBExecutor, error) {
	exec, err := db.Executor(ctx)
	if err != nil {
		return nil, err
	}
	if db.flags.Has(txRequiredForWrites) && !isTx(exec) {
		return nil, ErrTxRequired
	}
	return exec, nil
}

// isReadStatement reports whether a raw SQL text starts with a read-only
// keyword. Anything else, including WITH, is treated as a write.
func isReadStatement(query string) bool {
	switch strings.ToUpper(queryOperation(query)) {
	case "SELECT", "SHOW", "EXPLAIN":
		return true
	}
	return false
}

// NewValues creates a VALUES query for inserting multiple rows efficiently.
func (db *DB) NewValues(model any) *ValuesQuery {
	return NewValuesQuery(db, model)
}

// NewSelect creates a SELECT query builder.
func (db *DB) NewSelect() *SelectQuery {
	return NewSelectQuery(db)
}

// NewInsert creates an INSERT query builder.
func (db *DB) NewInsert() *InsertQuery {
	return NewInsertQuery(db)
}

// NewUpdate creates an UPDATE query builder.
func (db *DB) NewUpdate() *UpdateQuery {
	return NewUpdateQuery(db)
}

// NewDelete creates a DELETE query builder.
func (db *DB) NewDelete() *DeleteQuery {
	return NewDeleteQuery(db)
}

// NewRaw creates a raw SQL query with the given query string and arguments.
func (db *DB) NewRaw(query string, args ...any) *RawQuery {
	return NewRawQuery(db, query, args...)
}

// Dialect returns the database dialect being used.
func (db *DB) Dialect() schema.Dialect {
	return db.dialect
}

// ScanRows scans all rows from the result set into the destination values.
// It closes the rows when complete.
func (db *DB) ScanRows(ctx context.Context, rows pgx.Rows, dest ...any) error {
	defer rows.Close()

	model, err := newModel(db, dest)
	if err != nil {
		return err
	}

	_, err = model.ScanRows(ctx, rows)
	if err != nil {
		return err
	}

	return rows.Err()
}

// ScanRow scans a single row from the result set into the destination values.
func (db *DB) ScanRow(ctx context.Context, rows pgx.Rows, dest ...any) error {
	model, err := newModel(db, dest)
	if err != nil {
		return err
	}

	rs, ok := model.(rowScanner)
	if !ok {
		return fmt.Errorf("pgcrud: %T does not support ScanRow", model)
	}

	return rs.ScanRow(ctx, rows)
}

// Table returns the schema table metadata for the given type.
func (db *DB) Table(typ reflect.Type) *schema.Table {
	return db.dialect.Tables().Get(typ)
}

// RegisterModel registers models by name so they can be referenced in table relations.
func (db *DB) RegisterModel(models ...any) {
	db.dialect.Tables().Register(models...)
}

// clone creates a shallow copy of the DB with independent query hooks.
func (db *DB) clone() *DB {
	clone := *db

	l := len(clone.queryHooks)
	clone.queryHooks = clone.queryHooks[:l:l]

	return &clone
}

// WithNamedArg returns a copy of the DB with an additional named argument
// bound into its query generator.
func (db *DB) WithNamedArg(name string, value any) *DB {
	clone := db.clone()
	clone.gen = clone.gen.WithNamedArg(name, value)
	return clone
}

// QueryGen returns the query generator used for formatting SQL queries.
func (db *DB) QueryGen() schema.QueryGen {
	return db.gen
}

type queryHookIniter interface {
	Init(db *DB)
}

// WithQueryHook returns a copy of the DB with the provided query hook attached.
func (db *DB) WithQueryHook(hook QueryHook) *DB {
	if initer, ok := hook.(queryHookIniter); ok {
		initer.Init(db)
	}

	clone := db.clone()
	clone.queryHooks = append(clone.queryHooks, hook)
	return clone
}

// UpdateFQN returns a fully qualified column name for UPDATE statements.
func (db *DB) UpdateFQN(alias, column string) Ident {
	if db.HasFeature(feature.UpdateMultiTable) {
		return Ident(alias + "." + column)
	}
	return Ident(column)
}

// HasFeature reports whether the dialect supports this feature.
func (db *DB) HasFeature(feat feature.Feature) bool {
	return db.dialect.Features().Has(feat)
}

//------------------------------------------------------------------------------

// Exec formats query with pgcrud placeholders and executes it without returning rows.
// It is always treated as a write for WithTxRequiredForWrites.
func (db *DB) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	formattedQuery := db.format(query, args)
	ctx, event := db.beforeQuery(ctx, nil, query, args, formattedQuery, nil)

	var res pgconn.CommandTag
	exec, err := db.writeExecutor(ctx)
	if err == nil {
		res, err = exec.Exec(ctx, formattedQuery, pgx.QueryExecModeSimpleProtocol)
	}

	db.afterQuery(ctx, event, res, err)
	return res, err
}

// Query formats query with pgcrud placeholders and executes it, returning pgx rows.
// The caller must close the rows.
func (db *DB) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	formattedQuery := db.format(query, args)
	ctx, event := db.beforeQuery(ctx, nil, query, args, formattedQuery, nil)

	var rows pgx.Rows
	exec, err := db.Executor(ctx)
	if err == nil {
		rows, err = exec.Query(ctx, formattedQuery, pgx.QueryExecModeSimpleProtocol)
	}

	db.afterQuery(ctx, event, pgconn.CommandTag{}, err)
	return rows, err
}

// QueryRow formats query with pgcrud placeholders and executes it, returning a pgx row.
// Errors surface from the row's Scan; the query hook sees a nil error.
func (db *DB) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	formattedQuery := db.format(query, args)
	ctx, event := db.beforeQuery(ctx, nil, query, args, formattedQuery, nil)

	exec, err := db.Executor(ctx)
	if err != nil {
		db.afterQuery(ctx, event, pgconn.CommandTag{}, err)
		return errRow{err: err}
	}

	row := exec.QueryRow(ctx, formattedQuery, pgx.QueryExecModeSimpleProtocol)
	db.afterQuery(ctx, event, pgconn.CommandTag{}, nil)
	return row
}

// errRow is a pgx.Row that fails with a fixed error.
type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

func (db *DB) format(query string, args []any) string {
	return db.gen.FormatQuery(query, args...)
}

func (db *DB) makeQueryBytes() []byte {
	return internal.MakeQueryBytes()
}
```

- [ ] **Step 4: Write hook.go**

```go
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
```

- [ ] **Step 5: Trim pgcrud.go**

In `pgcrud.go` (copied from `bun.go`) delete the four interfaces `BeforeCreateTableHook`, `AfterCreateTableHook`, `BeforeDropTableHook` and `AfterDropTableHook` with their doc comments. Everything else stays.

- [ ] **Step 6: Rewrite the execution seam in query_base.go**

Apply these edits to `query_base.go`:

1. Imports: remove `"database/sql"` and `"database/sql/driver"`; add `"strconv"` if not present, `"github.com/jackc/pgx/v5"` and `"github.com/jackc/pgx/v5/pgconn"`.
2. Delete the `IConn` interface, the `var ( _ IConn = ... )` block, the `IDB` interface and the `var ( _ IDB = ... )` block.
3. In `type baseQuery struct`, delete the field `conn IConn`.
4. Replace the whole `resolveConn` method with:

```go
// resolveExecutor picks the executor for iquery. Writes go through
// writeExecutor so WithTxRequiredForWrites is enforced. A RawQuery reports
// Operation() as SELECT, so it is classified from its SQL text instead.
func (q *baseQuery) resolveExecutor(ctx context.Context, iquery Query, query string) (DBExecutor, error) {
	if q.isWrite(iquery, query) {
		return q.db.writeExecutor(ctx)
	}
	return q.db.Executor(ctx)
}

func (q *baseQuery) isWrite(iquery Query, query string) bool {
	if _, ok := iquery.(*RawQuery); ok {
		return !isReadStatement(query)
	}
	switch iquery.Operation() {
	case "INSERT", "UPDATE", "DELETE":
		return true
	}
	return false
}
```

5. Delete the whole `setConn` method.
6. Replace `scan`, `_scan` and `exec` with:

```go
func (q *baseQuery) scan(
	ctx context.Context,
	iquery Query,
	query string,
	model Model,
	hasDest bool,
) (pgconn.CommandTag, error) {
	ctx, event := q.db.beforeQuery(ctx, iquery, query, nil, query, q.model)
	res, err := q._scan(ctx, iquery, query, model, hasDest)
	q.db.afterQuery(ctx, event, res, err)
	return res, err
}

func (q *baseQuery) _scan(
	ctx context.Context,
	iquery Query,
	query string,
	model Model,
	hasDest bool,
) (pgconn.CommandTag, error) {
	exec, err := q.resolveExecutor(ctx, iquery, query)
	if err != nil {
		return pgconn.CommandTag{}, err
	}

	rows, err := exec.Query(ctx, query, pgx.QueryExecModeSimpleProtocol)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	defer rows.Close()

	numRow, err := model.ScanRows(ctx, rows)
	if err != nil {
		return pgconn.CommandTag{}, err
	}

	if numRow == 0 && hasDest && isSingleRowModel(model) {
		return pgconn.CommandTag{}, pgx.ErrNoRows
	}
	return rowsAffectedTag(numRow), nil
}

func (q *baseQuery) exec(
	ctx context.Context,
	iquery Query,
	query string,
) (pgconn.CommandTag, error) {
	ctx, event := q.db.beforeQuery(ctx, iquery, query, nil, query, q.model)

	var res pgconn.CommandTag
	exec, err := q.resolveExecutor(ctx, iquery, query)
	if err == nil {
		res, err = exec.Exec(ctx, query, pgx.QueryExecModeSimpleProtocol)
	}

	q.db.afterQuery(ctx, event, res, err)
	return res, err
}

// rowsAffectedTag builds a command tag whose RowsAffected reports n scanned rows.
func rowsAffectedTag(n int) pgconn.CommandTag {
	return pgconn.NewCommandTag("SELECT " + strconv.Itoa(n))
}
```

7. In the block of constructors near the end of the file (`NewValues`, `NewSelect`, `NewInsert`, `NewUpdate`, `NewDelete`, `NewRaw` on `*baseQuery`), remove the `.Conn(q.conn)` suffix from each, so for example `return NewSelectQuery(q.db)`. Delete the constructors `NewCreateTable`, `NewDropTable`, `NewCreateIndex`, `NewDropIndex`, `NewTruncateTable`, `NewAddColumn`, `NewDropColumn` and `NewMerge` entirely.

- [ ] **Step 7: Update the models to pgx rows**

Add to `model.go`, after the `rowScanner` interface, and change `rowScanner` itself:

```go
type rowScanner interface {
	ScanRow(ctx context.Context, rows pgx.Rows) error
}

// columnNames returns the column names of rows in result order.
func columnNames(rows pgx.Rows) []string {
	fds := rows.FieldDescriptions()
	names := make([]string, len(fds))
	for i, fd := range fds {
		names[i] = fd.Name
	}
	return names
}
```

Add `"github.com/jackc/pgx/v5"` to the imports of `model.go`. Keep `"database/sql"` there because `_newModel` matches on `sql.Scanner`.

Then in every model file replace `rows *sql.Rows` with `rows pgx.Rows` in method signatures, replace the import `"database/sql"` with `"github.com/jackc/pgx/v5"` (unless `sql.` is still used elsewhere in that file), and replace each

```go
	columns, err := rows.Columns()
	if err != nil {
		return 0, err
	}
```

(or `return err` in `ScanRow`) with

```go
	columns := columnNames(rows)
```

Files and methods affected: `model_scan.go` (`ScanRows`, `ScanRow`), `model_slice.go` (`ScanRows`), `model_map_slice.go` (`ScanRows`; also delete the line `m.rows = rows`), `model_table_struct.go` (`ScanRows`, `ScanRow`, `scanRow`), `model_table_slice.go` (`ScanRows`; it contains a duplicated `m.columns = columns` line, keep one), `model_table_has_many.go` (`ScanRows`), `model_table_m2m.go` (`ScanRows`).

Replace `model_map.go` entirely with:

```go
package pgcrud

import (
	"bytes"
	"context"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/piprim/pgcrud/schema"
)

type mapModel struct {
	db *DB

	dest *map[string]any
	m    map[string]any

	columns   []string
	scanIndex int
}

var _ Model = (*mapModel)(nil)

func newMapModel(db *DB, dest *map[string]any) *mapModel {
	m := &mapModel{
		db:   db,
		dest: dest,
	}
	if dest != nil {
		m.m = *dest
	}
	return m
}

func (m *mapModel) Value() any {
	return m.dest
}

func (m *mapModel) ScanRows(ctx context.Context, rows pgx.Rows) (int, error) {
	if !rows.Next() {
		return 0, rows.Err()
	}

	m.columns = columnNames(rows)
	dest := makeDest(m, len(m.columns))

	if m.m == nil {
		m.m = make(map[string]any, len(m.columns))
	}

	m.scanIndex = 0
	if err := rows.Scan(dest...); err != nil {
		return 0, err
	}

	*m.dest = m.m

	return 1, nil
}

// Scan stores the value pgx decoded for the current column. pgx hands typed Go
// values (int64, float64, bool, time.Time, string, []byte) to sql.Scanner
// destinations, so no further conversion is needed. Byte slices are cloned
// because they may alias a buffer pgx reuses.
func (m *mapModel) Scan(src any) error {
	if b, ok := src.([]byte); ok {
		src = bytes.Clone(b)
	}
	return m.scanRaw(src)
}

func (m *mapModel) scanRaw(src any) error {
	columnName := m.columns[m.scanIndex]
	m.scanIndex++
	m.m[columnName] = src
	return nil
}

func (m *mapModel) appendColumnsValues(gen schema.QueryGen, b []byte) []byte {
	keys := make([]string, 0, len(m.m))

	for k := range m.m {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	b = append(b, " ("...)

	for i, k := range keys {
		if i > 0 {
			b = append(b, ", "...)
		}
		b = gen.AppendIdent(b, k)
	}

	b = append(b, ") VALUES ("...)

	isTemplate := gen.IsNop()
	for i, k := range keys {
		if i > 0 {
			b = append(b, ", "...)
		}
		if isTemplate {
			b = append(b, '?')
		} else {
			b = gen.Append(b, m.m[k])
		}
	}

	b = append(b, ")"...)

	return b
}

func (m *mapModel) appendSet(gen schema.QueryGen, b []byte) []byte {
	keys := make([]string, 0, len(m.m))

	for k := range m.m {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	isTemplate := gen.IsNop()
	for i, k := range keys {
		if i > 0 {
			b = append(b, ", "...)
		}

		b = gen.AppendIdent(b, k)
		b = append(b, " = "...)
		if isTemplate {
			b = append(b, '?')
		} else {
			b = gen.Append(b, m.m[k])
		}
	}

	return b
}

func makeDest(v any, n int) []any {
	dest := make([]any, n)
	for i := range dest {
		dest[i] = v
	}
	return dest
}
```

- [ ] **Step 8: Update the query builders**

`query_select.go`:
1. Delete the `Conn` method (`func (q *SelectQuery) Conn(db IConn) *SelectQuery`).
2. In `selectJoins`, change both `q.db.NewSelect().Conn(q.conn)` to `q.db.NewSelect()`.
3. `Rows` returns `(pgx.Rows, error)`. Replace its last four lines with:

```go
	ctx, event := q.db.beforeQuery(ctx, q, query, nil, query, q.model)

	var rows pgx.Rows
	exec, err := q.resolveExecutor(ctx, q, query)
	if err == nil {
		rows, err = exec.Query(ctx, query, pgx.QueryExecModeSimpleProtocol)
	}

	q.db.afterQuery(ctx, event, pgconn.CommandTag{}, err)
	return rows, err
```

4. `Exec` signature becomes `(res pgconn.CommandTag, err error)`; every `return nil, ...` inside `Exec` becomes `return pgconn.CommandTag{}, ...`.
5. `scanResult` returns `(pgconn.CommandTag, error)`; every `return nil, ...` inside it becomes `return pgconn.CommandTag{}, ...`; replace `if n, _ := res.RowsAffected(); n > 0 {` with `if res.RowsAffected() > 0 {`.
6. In `Count`, replace `err = q.resolveConn(ctx, q).QueryRowContext(ctx, query).Scan(&num)` with:

```go
	var num int64
	exec, err := q.resolveExecutor(ctx, qq, query)
	if err == nil {
		err = exec.QueryRow(ctx, query, pgx.QueryExecModeSimpleProtocol).Scan(&num)
	}
```

   (delete the earlier `var num int64` line so it is declared once.)
7. Replace `ScanAndCount` with a version that chooses by executor type. Keep `scanAndCountConcurrently` and `scanAndCountSeq` as they are in bun:

```go
// ScanAndCount executes the query, scans results into dest, and returns the total count.
// The fetch and the count run concurrently only when the executor is a pool;
// a transaction or a dedicated connection is a single connection and gets them in sequence.
func (q *SelectQuery) ScanAndCount(ctx context.Context, dest ...any) (int64, error) {
	if q.offset == 0 && q.limit == 0 {
		res, err := q.scanResult(ctx, dest...)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected(), nil
	}

	exec, err := q.db.Executor(ctx)
	if err != nil {
		return 0, err
	}
	if isPool(exec) {
		return q.scanAndCountConcurrently(ctx, dest...)
	}
	return q.scanAndCountSeq(ctx, dest...)
}
```
8. `Exists` calls `q.selectExists(ctx)` unconditionally. Delete `whereExists`, the `whereExistsQuery` type and its `AppendQuery` method.
9. In `selectExists`, replace `err = q.resolveConn(ctx, q).QueryRowContext(ctx, query).Scan(&exists)` with:

```go
	var exists bool
	exec, err := q.resolveExecutor(ctx, qq, query)
	if err == nil {
		err = exec.QueryRow(ctx, query, pgx.QueryExecModeSimpleProtocol).Scan(&exists)
	}
```

   (delete the earlier `var exists bool` line.)
10. In `Clone`, delete the line `conn: q.conn,`.
11. Imports: remove `"database/sql"`; add `"github.com/jackc/pgx/v5"` and `"github.com/jackc/pgx/v5/pgconn"`.

`query_insert.go`:
1. Delete the `Conn` method.
2. `Exec` and `scanOrExec` return `(pgconn.CommandTag, error)`; inside `scanOrExec` change `var res sql.Result` to `var res pgconn.CommandTag` and each `return nil, ...` to `return pgconn.CommandTag{}, ...`.
3. Delete the block

```go
		if err := q.tryLastInsertID(res, dest); err != nil {
			return nil, err
		}
```

   and delete the whole `tryLastInsertID` method. Postgres always uses RETURNING.
4. Imports: remove `"database/sql"`; add `"github.com/jackc/pgx/v5/pgconn"`.

`query_update.go`, `query_delete.go`, `query_raw.go`: same as items 1, 2 and 4 above for their `Conn`, `Exec` and `scanOrExec` methods.

`query_values.go`: delete the `Conn` method only.

- [ ] **Step 9: Adapt the carried root tests**

In `query_select_clone_test.go` delete these three lines from `TestSelectQueryCloneCopiesExecutionState`:

```go
	conn := &DB{}
	q.conn = conn
	require.True(t, clone.conn == IConn(conn), "conn must be copied")
```

`util_test.go` and `list_tuple_test.go` need no change beyond the package rename done in step 1.

- [ ] **Step 10: Build, vet and run the carried tests**

```bash
cd /home/pi/code/bun/pgcrud
go mod tidy
go build ./...
go vet ./...
go test ./...
grep -rn "database/sql\"" --include='*.go' . | grep -v _test.go
```

Expected: build, vet and tests pass. The final grep lists only `model.go`, `schema/sqltype.go`, `schema/table.go`, `schema/scan.go`, `schema/reflect.go`, `schema/zerochecker.go`, `schema/append_value.go`, `model_table_m2m.go` (if it still uses `sql.Scanner`), and pgdialect files that assert `sql.Scanner`. None of them may reference `sql.DB`, `sql.Rows`, `sql.Result` or `sql.Tx`; check with:

```bash
grep -rn "sql\.\(DB\|Rows\|Result\|Tx\|Row\b\|ColumnType\)" --include='*.go' . ; echo "exit=$?"
```

Expected: no matches, `exit=1`.

- [ ] **Step 11: Report files ready to commit**

Do not run git. Tell the user the root package compiles and its carried tests pass.

---

### Task 3: Unit tests with a fake executor

**Files:**
- Create: `executor_test.go`, `db_test.go`

**Interfaces:**
- Consumes: everything Task 2 produces.
- Produces: `fakeExecutor`, `fakeRows`, `cols(...)` and `fakeRow` helpers in package `pgcrud_test` for later tests.

- [ ] **Step 1: Write the fakes in executor_test.go**

```go
package pgcrud_test

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// cols builds field descriptions for the given column names.
func cols(names ...string) []pgconn.FieldDescription {
	fds := make([]pgconn.FieldDescription, len(names))
	for i, n := range names {
		fds[i] = pgconn.FieldDescription{Name: n}
	}
	return fds
}

// fakeRows is an in-memory pgx.Rows. Scan hands each value to the
// destination the way pgx does for sql.Scanner targets.
type fakeRows struct {
	fds    []pgconn.FieldDescription
	data   [][]any
	idx    int
	closed bool
	err    error
}

func newFakeRows(fds []pgconn.FieldDescription, data ...[]any) *fakeRows {
	return &fakeRows{fds: fds, data: data}
}

func (r *fakeRows) Close()                                       { r.closed = true }
func (r *fakeRows) Err() error                                   { return r.err }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return r.fds }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }

func (r *fakeRows) CommandTag() pgconn.CommandTag {
	return pgconn.NewCommandTag(fmt.Sprintf("SELECT %d", len(r.data)))
}

func (r *fakeRows) Next() bool {
	if r.idx >= len(r.data) {
		return false
	}
	r.idx++
	return true
}

func (r *fakeRows) Values() ([]any, error) {
	return r.data[r.idx-1], nil
}

func (r *fakeRows) Scan(dest ...any) error {
	row := r.data[r.idx-1]
	if len(dest) != len(row) {
		return fmt.Errorf("fakeRows: %d dest for %d values", len(dest), len(row))
	}
	for i, d := range dest {
		switch d := d.(type) {
		case sql.Scanner:
			if err := d.Scan(row[i]); err != nil {
				return err
			}
		case *int64:
			*d = row[i].(int64)
		case *bool:
			*d = row[i].(bool)
		case *string:
			*d = row[i].(string)
		default:
			return fmt.Errorf("fakeRows: unsupported dest %T", d)
		}
	}
	return nil
}

type fakeRow struct{ rows *fakeRows }

func (r fakeRow) Scan(dest ...any) error {
	if !r.rows.Next() {
		return pgx.ErrNoRows
	}
	return r.rows.Scan(dest...)
}

type call struct {
	method string
	sql    string
	args   []any
}

// fakeExecutor records calls and replays canned results.
type fakeExecutor struct {
	calls []call
	rows  *fakeRows
	tag   pgconn.CommandTag
	err   error
}

func (e *fakeExecutor) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	e.calls = append(e.calls, call{"Exec", sql, args})
	return e.tag, e.err
}

func (e *fakeExecutor) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	e.calls = append(e.calls, call{"Query", sql, args})
	if e.err != nil {
		return nil, e.err
	}
	return e.rows, nil
}

func (e *fakeExecutor) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	e.calls = append(e.calls, call{"QueryRow", sql, args})
	if e.rows != nil {
		e.rows.idx = 0 // a fresh row set for each QueryRow, so one fake can serve fetch and count
	}
	return fakeRow{rows: e.rows}
}

func (e *fakeExecutor) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	e.calls = append(e.calls, call{"SendBatch", "", nil})
	return nil
}
```

- [ ] **Step 2: Write the failing unit tests in db_test.go**

```go
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
```

- [ ] **Step 3: Run the tests and fix what fails**

```bash
cd /home/pi/code/bun/pgcrud && go test ./ -run 'TestNew|TestExecutorResolver|TestScan|TestExec|TestQueryHooks|TestTxRequiredForWrites|TestScanAndCount' -v
```

Expected: all pass. Likely adjustments if not: the exact SQL strings in `TestNew` and `TestExecutorResolver` must match what bun generates for this model (compare with the `String()` output and fix the expectation, never the generator); the `Count` alias column name is irrelevant because the fake ignores names for scalar destinations. In `TestScanAndCount` the sequential case reuses one `fakeRows` for both calls; `fakeExecutor.QueryRow` rewinds it so the count reads the first row again. The concurrent pool path is covered by the integration test in Task 5, since a real `*pgxpool.Pool` is needed for `isPool` to be true.

- [ ] **Step 4: Run the whole suite**

```bash
cd /home/pi/code/bun/pgcrud && go vet ./... && go test ./...
```

Expected: PASS.

- [ ] **Step 5: Report files ready to commit**

Do not run git. Tell the user `executor_test.go` and `db_test.go` are ready.

---

### Task 4: Golden SQL tests ported from bun

**Files:**
- Create: `snapshot_test.go`, `query_test.go`, `testdata/snapshots/TestQuery-pg-*`

**Interfaces:**
- Consumes: `pgcrud.New(nil)`, `DB.QueryGen()`, builders.
- Produces: `assertSnapshot(t *testing.T, got string)` helper with an `-update` flag.

- [ ] **Step 1: Write the snapshot helper**

```go
package pgcrud_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var updateSnapshots = flag.Bool("update", false, "rewrite golden files under testdata/snapshots")

// assertSnapshot compares got with testdata/snapshots/<TestName with / as ->.
// Files hold the value followed by one newline, the format bun's suite used.
func assertSnapshot(t *testing.T, got string) {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "-")
	path := filepath.Join("testdata", "snapshots", name)

	if *updateSnapshots {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got+"\n"), 0o644))
		return
	}

	want, err := os.ReadFile(path)
	require.NoError(t, err, "missing snapshot %s (run with -update to create)", path)
	require.Equal(t, strings.TrimSuffix(string(want), "\n"), got)
}
```

- [ ] **Step 2: Copy the snapshots and the TestQuery cases**

```bash
cd /home/pi/code/bun/pgcrud
mkdir -p testdata/snapshots
cp /home/pi/code/bun/internal/dbtest/testdata/snapshots/TestQuery-pg-* testdata/snapshots/
DROP="32 33 34 37 38 39 42 43 44 45 57 70 71 83 98 101 102 103 151 154 155 156 157 164 173 174 176 177 179 182 183 184 187 193"
for id in $DROP; do rm -f testdata/snapshots/TestQuery-pg-$id; done
ls testdata/snapshots | wc -l
```

Expected: `177`. These ids use builders pgcrud does not provide (CreateTable, DropTable, TruncateTable, CreateIndex, DropIndex, AddColumn, DropColumn, Merge, ResetModel) or branch on the dialect name.

Now extract `TestQuery` from bun and drop the same cases:

```bash
cd /home/pi/code/bun/pgcrud
{
cat <<'EOF'
package pgcrud_test

import (
	"encoding/json"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/piprim/pgcrud"
	"github.com/piprim/pgcrud/dialect/sqltype"
	"github.com/piprim/pgcrud/internal"
	"github.com/piprim/pgcrud/schema"
)

EOF
awk 'NR>=27 && /^func TestSelectQueryClone/{exit} NR>=27{print}' /home/pi/code/bun/internal/dbtest/query_test.go
} > query_test.go

DROP="32 33 34 37 38 39 42 43 44 45 57 70 71 83 98 101 102 103 151 154 155 156 157 164 173 174 176 177 179 182 183 184 187 193"
awk -v ids="$DROP" '
BEGIN { n = split(ids, a, " "); for (i = 1; i <= n; i++) drop[a[i]] = 1 }
/^\t\t\{$/ { buf = $0; inblock = 1; skip = 0; next }
inblock {
  buf = buf "\n" $0
  if ($0 ~ /^\t\t\tid: /) { id = $2; sub(",", "", id); if (drop[id]) skip = 1 }
  if ($0 ~ /^\t\t\},$/) { inblock = 0; if (!skip) print buf }
  next
}
{ print }' query_test.go > query_test.go.tmp && mv query_test.go.tmp query_test.go

sed -i -e 's/\bbun\./pgcrud./g' -e 's/\*bun\.DB/*pgcrud.DB/g' query_test.go
grep -c '^\t\t\tid: ' query_test.go
```

Expected: `177`.

- [ ] **Step 3: Replace the multi-database runner with the pg-only runner**

In `query_test.go`, find the tail of `TestQuery`:

```go
	testEachDB(t, func(t *testing.T, dbName string, db *pgcrud.DB) {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("%d", tt.id), func(t *testing.T) {
				q := tt.query(db)

				query, err := q.AppendQuery(db.QueryGen(), nil)
				if err != nil {
					cupaloy.SnapshotT(t, err.Error())
				} else {
					query = timeRE.ReplaceAll(query, []byte("[TIME]"))
					cupaloy.SnapshotT(t, string(query))
				}
			})
		}
	})
```

and replace it with:

```go
	t.Run("pg", func(t *testing.T) {
		db := pgcrud.New(nil)
		for _, tt := range tests {
			t.Run(fmt.Sprintf("%d", tt.id), func(t *testing.T) {
				q := tt.query(db)

				query, err := q.AppendQuery(db.QueryGen(), nil)
				if err != nil {
					assertSnapshot(t, err.Error())
				} else {
					query = timeRE.ReplaceAll(query, []byte("[TIME]"))
					assertSnapshot(t, string(query))
				}
			})
		}
	})
```

The subtest names become `TestQuery/pg/<id>`, which the helper maps to the copied file names `TestQuery-pg-<id>`.

- [ ] **Step 4: Compile and prune**

```bash
cd /home/pi/code/bun/pgcrud && go vet ./ 2>&1 | head -40
```

Expected failures at this point are only unused imports (`encoding/json`, `sqltype`, `internal`, `require`, `time`, `schema`) or a stray reference to a dropped feature. Remove unused imports; if a remaining case references `migrate`, `sqlschema` or a dropped builder, delete that case and its snapshot file too and report the id. Repeat until `go vet ./` is clean.

- [ ] **Step 5: Run the golden tests**

```bash
cd /home/pi/code/bun/pgcrud && go test ./ -run TestQuery -v 2>&1 | tail -30
```

Expected: PASS for every case. A mismatch means the port changed SQL generation; fix the generator, not the snapshot. The only acceptable snapshot edit is an error message whose prefix changed from `bun:` to `pgcrud:`; for those, update the file content by hand and report the id.

- [ ] **Step 6: Report files ready to commit**

Do not run git. Tell the user `query_test.go`, `snapshot_test.go` and `testdata/snapshots` are ready, and list any case ids removed or edited in steps 4 and 5.

---

### Task 5: Integration tests, docker-compose and CI

**Files:**
- Create: `integration_test.go`, `uow_test.go`, `docker-compose.yml`, `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: the full public API, `PGCRUD_TEST_DSN` environment variable.
- Produces: `testDB(t) (*pgcrud.DB, *pgxpool.Pool)` helper; `UnitOfWork` copy for tests.

- [ ] **Step 1: docker-compose.yml**

```yaml
services:
  postgres:
    image: postgres:16
    environment:
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: postgres
      POSTGRES_DB: pgcrud
    ports:
      - "5432:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
      timeout: 5s
      retries: 10
```

Local DSN: `postgres://postgres:postgres@localhost:5432/pgcrud?sslmode=disable`.

- [ ] **Step 2: Write uow_test.go, a copy of the application's unit of work**

```go
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
```

- [ ] **Step 3: Write integration_test.go**

```go
package pgcrud_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/piprim/pgcrud"
)

const schemaSQL = `
DROP TABLE IF EXISTS story_tags, tags, comments, stories, authors CASCADE;

CREATE TABLE authors (
	id         bigserial PRIMARY KEY,
	name       text NOT NULL UNIQUE,
	emails     text[] NOT NULL DEFAULT '{}',
	meta       jsonb,
	avatar     bytea,
	balance    numeric(12,2) NOT NULL DEFAULT 0,
	active     boolean NOT NULL DEFAULT true,
	created_at timestamptz NOT NULL
);
CREATE TABLE stories (
	id        bigserial PRIMARY KEY,
	title     text NOT NULL,
	author_id bigint NOT NULL REFERENCES authors(id)
);
CREATE TABLE comments (
	id       bigserial PRIMARY KEY,
	story_id bigint NOT NULL REFERENCES stories(id),
	body     text NOT NULL
);
CREATE TABLE tags (
	id   bigserial PRIMARY KEY,
	name text NOT NULL
);
CREATE TABLE story_tags (
	story_id bigint NOT NULL REFERENCES stories(id),
	tag_id   bigint NOT NULL REFERENCES tags(id),
	PRIMARY KEY (story_id, tag_id)
);
`

type Author struct {
	ID        int64          `bun:",pk,autoincrement"`
	Name      string
	Emails    []string       `bun:",array"`
	Meta      map[string]any `bun:",type:jsonb"`
	Avatar    []byte
	Balance   float64
	Active    bool
	CreatedAt time.Time
}

type Story struct {
	ID       int64 `bun:",pk,autoincrement"`
	Title    string
	AuthorID int64
	Author   *Author    `bun:"rel:belongs-to,join:author_id=id"`
	Comments []*Comment `bun:"rel:has-many,join:id=story_id"`
	Tags     []Tag      `bun:"m2m:story_tags,join:Story=Tag"`
}

type Comment struct {
	ID      int64 `bun:",pk,autoincrement"`
	StoryID int64
	Body    string
}

type Tag struct {
	ID   int64 `bun:",pk,autoincrement"`
	Name string
}

type StoryTag struct {
	StoryID int64  `bun:",pk"`
	Story   *Story `bun:"rel:belongs-to,join:story_id=id"`
	TagID   int64  `bun:",pk"`
	Tag     *Tag   `bun:"rel:belongs-to,join:tag_id=id"`
}

// testDB connects to PGCRUD_TEST_DSN, recreates the schema and returns a DB
// wired to a UnitOfWork. It skips the test when the variable is unset.
func testDB(t *testing.T) (*pgcrud.DB, *UnitOfWork) {
	t.Helper()
	dsn := os.Getenv("PGCRUD_TEST_DSN")
	if dsn == "" {
		t.Skip("PGCRUD_TEST_DSN not set")
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	uow := NewUnitOfWork(pool)
	db := pgcrud.New(pool, pgcrud.WithExecutorResolver(uow.Executor))
	db.RegisterModel((*StoryTag)(nil))

	_, err = db.NewRaw(schemaSQL).Exec(ctx)
	require.NoError(t, err)

	return db, uow
}

func TestIntegrationCRUD(t *testing.T) {
	db, _ := testDB(t)
	ctx := context.Background()

	created := time.Date(2026, 9, 23, 10, 30, 0, 123456000, time.UTC)
	a := &Author{
		Name:      "ann",
		Emails:    []string{"a@x.io", "b@x.io"},
		Meta:      map[string]any{"k": "v", "n": float64(2)},
		Avatar:    []byte{0x00, 0xff, 0x10},
		Balance:   12.34,
		Active:    true,
		CreatedAt: created,
	}
	_, err := db.NewInsert().Model(a).Returning("id").Exec(ctx)
	require.NoError(t, err)

	t.Run("insert returns the generated id", func(t *testing.T) {
		require.NotZero(t, a.ID)
	})

	var got Author
	require.NoError(t, db.NewSelect().Model(&got).Where("id = ?", a.ID).Scan(ctx))

	t.Run("text and bool round trip", func(t *testing.T) {
		require.Equal(t, "ann", got.Name)
		require.True(t, got.Active)
	})
	t.Run("text array round trips", func(t *testing.T) {
		require.Equal(t, []string{"a@x.io", "b@x.io"}, got.Emails)
	})
	t.Run("jsonb round trips", func(t *testing.T) {
		require.Equal(t, map[string]any{"k": "v", "n": float64(2)}, got.Meta)
	})
	t.Run("bytea round trips", func(t *testing.T) {
		require.Equal(t, []byte{0x00, 0xff, 0x10}, got.Avatar)
	})
	t.Run("numeric round trips", func(t *testing.T) {
		require.InDelta(t, 12.34, got.Balance, 0.0001)
	})
	t.Run("timestamptz round trips", func(t *testing.T) {
		require.True(t, created.Equal(got.CreatedAt), "want %s got %s", created, got.CreatedAt)
	})

	t.Run("update with returning", func(t *testing.T) {
		var name string
		err := db.NewUpdate().Model(a).Set("name = ?", "ann2").WherePK().Returning("name").Scan(ctx, &name)
		require.NoError(t, err)
		require.Equal(t, "ann2", name)
	})

	t.Run("count and exists", func(t *testing.T) {
		n, err := db.NewSelect().Model((*Author)(nil)).Count(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(1), n)
		ok, err := db.NewSelect().Model((*Author)(nil)).Where("name = ?", "nobody").Exists(ctx)
		require.NoError(t, err)
		require.False(t, ok)
	})

	t.Run("scan into map", func(t *testing.T) {
		var m map[string]any
		require.NoError(t, db.NewSelect().Model((*Author)(nil)).
			Column("id", "name", "avatar").Where("id = ?", a.ID).Scan(ctx, &m))
		require.Equal(t, a.ID, m["id"])
		require.Equal(t, "ann2", m["name"])
		require.Equal(t, []byte{0x00, 0xff, 0x10}, m["avatar"])
	})

	t.Run("unique violation surfaces as pgconn.PgError", func(t *testing.T) {
		_, err := db.NewInsert().Model(&Author{Name: "ann2", CreatedAt: created}).Exec(ctx)
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr)
		require.Equal(t, "23505", pgErr.Code)
	})

	t.Run("no rows returns pgx.ErrNoRows", func(t *testing.T) {
		var missing Author
		err := db.NewSelect().Model(&missing).Where("id = ?", -1).Scan(ctx)
		require.ErrorIs(t, err, pgx.ErrNoRows)
	})

	t.Run("delete reports rows affected", func(t *testing.T) {
		res, err := db.NewDelete().Model((*Author)(nil)).Where("id = ?", a.ID).Exec(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(1), res.RowsAffected())
	})
}

func TestIntegrationRelations(t *testing.T) {
	db, _ := testDB(t)
	ctx := context.Background()

	author := &Author{Name: "rel", CreatedAt: time.Now()}
	_, err := db.NewInsert().Model(author).Returning("id").Exec(ctx)
	require.NoError(t, err)

	story := &Story{Title: "s1", AuthorID: author.ID}
	_, err = db.NewInsert().Model(story).Returning("id").Exec(ctx)
	require.NoError(t, err)

	comments := []*Comment{{StoryID: story.ID, Body: "c1"}, {StoryID: story.ID, Body: "c2"}}
	_, err = db.NewInsert().Model(&comments).Returning("id").Exec(ctx)
	require.NoError(t, err)

	tags := []Tag{{Name: "go"}, {Name: "sql"}}
	_, err = db.NewInsert().Model(&tags).Returning("id").Exec(ctx)
	require.NoError(t, err)

	links := []StoryTag{{StoryID: story.ID, TagID: tags[0].ID}, {StoryID: story.ID, TagID: tags[1].ID}}
	_, err = db.NewInsert().Model(&links).Exec(ctx)
	require.NoError(t, err)

	var got Story
	err = db.NewSelect().Model(&got).
		Relation("Author").
		Relation("Comments").
		Relation("Tags").
		Where("story.id = ?", story.ID).
		Scan(ctx)
	require.NoError(t, err)

	t.Run("belongs-to is loaded through a join", func(t *testing.T) {
		require.NotNil(t, got.Author)
		require.Equal(t, "rel", got.Author.Name)
	})
	t.Run("has-many is loaded with a second query", func(t *testing.T) {
		require.Len(t, got.Comments, 2)
		require.Equal(t, "c1", got.Comments[0].Body)
	})
	t.Run("many-to-many is loaded through the join table", func(t *testing.T) {
		require.Len(t, got.Tags, 2)
	})

	t.Run("Rows works with pgx.CollectRows", func(t *testing.T) {
		rows, err := db.NewSelect().Model((*Comment)(nil)).Column("id", "body").OrderExpr("id").Rows(ctx)
		require.NoError(t, err)
		type row struct {
			ID   int64  `db:"id"`
			Body string `db:"body"`
		}
		got, err := pgx.CollectRows(rows, pgx.RowToStructByName[row])
		require.NoError(t, err)
		require.Equal(t, []row{{comments[0].ID, "c1"}, {comments[1].ID, "c2"}}, got)
	})
}

var _ pgcrud.BeforeAppendModelHook = (*Author)(nil)

// BeforeAppendModel fills CreatedAt on insert when the caller left it zero.
func (a *Author) BeforeAppendModel(ctx context.Context, query pgcrud.Query) error {
	if _, ok := query.(*pgcrud.InsertQuery); ok && a.CreatedAt.IsZero() {
		a.CreatedAt = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return nil
}

func TestIntegrationHooks(t *testing.T) {
	db, _ := testDB(t)
	ctx := context.Background()
	hook := &recordingHook{}
	db = db.WithQueryHook(hook)

	h := &Author{Name: "hooked"}
	_, err := db.NewInsert().Model(h).Returning("id").Exec(ctx)
	require.NoError(t, err)

	t.Run("model hook filled created_at before append", func(t *testing.T) {
		require.Equal(t, 2000, h.CreatedAt.Year())
	})
	t.Run("query hook observed the insert", func(t *testing.T) {
		require.Equal(t, 1, hook.after)
		require.Equal(t, "INSERT", hook.last.Operation())
		require.NoError(t, hook.last.Err)
	})
}

func TestIntegrationUnitOfWork(t *testing.T) {
	db, uow := testDB(t)
	ctx := context.Background()

	count := func(t *testing.T) int64 {
		n, err := db.NewSelect().Model((*Tag)(nil)).Count(ctx)
		require.NoError(t, err)
		return n
	}

	t.Run("committed transaction persists", func(t *testing.T) {
		err := uow.WithTransaction(ctx, func(ctx context.Context) error {
			_, err := db.NewInsert().Model(&Tag{Name: "t1"}).Exec(ctx)
			return err
		})
		require.NoError(t, err)
		require.Equal(t, int64(1), count(t))
	})

	t.Run("returned error rolls back", func(t *testing.T) {
		boom := errors.New("boom")
		err := uow.WithTransaction(ctx, func(ctx context.Context) error {
			if _, err := db.NewInsert().Model(&Tag{Name: "t2"}).Exec(ctx); err != nil {
				return err
			}
			return boom
		})
		require.ErrorIs(t, err, boom)
		require.Equal(t, int64(1), count(t))
	})

	t.Run("nested failure rolls back to the savepoint only", func(t *testing.T) {
		err := uow.WithTransaction(ctx, func(ctx context.Context) error {
			if _, err := db.NewInsert().Model(&Tag{Name: "outer"}).Exec(ctx); err != nil {
				return err
			}
			inner := uow.WithTransaction(ctx, func(ctx context.Context) error {
				if _, err := db.NewInsert().Model(&Tag{Name: "inner"}).Exec(ctx); err != nil {
					return err
				}
				return errors.New("inner fails")
			})
			require.Error(t, inner)
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, int64(2), count(t))
		var names []string
		require.NoError(t, db.NewSelect().Model((*Tag)(nil)).Column("name").OrderExpr("id").Scan(ctx, &names))
		require.Equal(t, []string{"t1", "outer"}, names)
	})

	t.Run("ScanAndCount inside a transaction is sequential and correct", func(t *testing.T) {
		err := uow.WithTransaction(ctx, func(ctx context.Context) error {
			for _, name := range []string{"p1", "p2", "p3"} {
				if _, err := db.NewInsert().Model(&Tag{Name: name}).Exec(ctx); err != nil {
					return err
				}
			}
			var page []Tag
			n, err := db.NewSelect().Model(&page).Where("name LIKE 'p%'").OrderExpr("id").Limit(2).ScanAndCount(ctx)
			if err != nil {
				return err
			}
			require.Len(t, page, 2)
			require.Equal(t, int64(3), n)
			return errors.New("discard")
		})
		require.Error(t, err)
	})

	t.Run("ScanAndCount on the pool runs concurrently and is correct", func(t *testing.T) {
		for _, name := range []string{"q1", "q2", "q3"} {
			_, err := db.NewInsert().Model(&Tag{Name: name}).Exec(ctx)
			require.NoError(t, err)
		}
		var page []Tag
		n, err := db.NewSelect().Model(&page).Where("name LIKE 'q%'").OrderExpr("id").Limit(2).ScanAndCount(ctx)
		require.NoError(t, err)
		require.Len(t, page, 2)
		require.Equal(t, int64(3), n)
	})

	t.Run("write outside a transaction fails when required", func(t *testing.T) {
		strict := pgcrud.New(db.Pool(),
			pgcrud.WithExecutorResolver(uow.Executor),
			pgcrud.WithTxRequiredForWrites())
		_, err := strict.NewInsert().Model(&Tag{Name: "escaped"}).Exec(ctx)
		require.ErrorIs(t, err, pgcrud.ErrTxRequired)
		n, err := strict.NewSelect().Model((*Tag)(nil)).Where("name = ?", "escaped").Count(ctx)
		require.NoError(t, err)
		require.Zero(t, n)
		err = uow.WithTransaction(ctx, func(ctx context.Context) error {
			_, err := strict.NewInsert().Model(&Tag{Name: "escaped"}).Exec(ctx)
			return err
		})
		require.NoError(t, err)
	})

	t.Run("queries inside the transaction see uncommitted rows", func(t *testing.T) {
		err := uow.WithTransaction(ctx, func(ctx context.Context) error {
			if _, err := db.NewInsert().Model(&Tag{Name: "visible"}).Exec(ctx); err != nil {
				return err
			}
			n, err := db.NewSelect().Model((*Tag)(nil)).Where("name = ?", "visible").Count(ctx)
			if err != nil {
				return err
			}
			require.Equal(t, int64(1), n)
			return errors.New("discard")
		})
		require.Error(t, err)
		n, err := db.NewSelect().Model((*Tag)(nil)).Where("name = ?", "visible").Count(ctx)
		require.NoError(t, err)
		require.Zero(t, n)
	})
}
```

- [ ] **Step 4: Run against Postgres**

```bash
cd /home/pi/code/bun/pgcrud
docker compose up -d --wait
PGCRUD_TEST_DSN='postgres://postgres:postgres@localhost:5432/pgcrud?sslmode=disable' go test ./ -run Integration -v 2>&1 | tail -60
docker compose down
```

Expected: all `TestIntegration*` pass. If Docker is not available in the sandbox, report that the integration tests could not be run here and leave them for the user to run with the same command; do not mark the task complete.

Known things to check if a subtest fails:
- `Relation("Tags")` requires `db.RegisterModel((*StoryTag)(nil))` before the first query on `Story`; `testDB` does that.
- `Avatar []byte` scanned from bytea arrives as `[]byte` from pgx's codec; if it arrives as a `\x` string instead, the scan plan did not go through the codec and `scanBytes` in `schema/scan.go` needs a hex decode branch for a `\x` prefix. Add it with a `t.Run` unit test rather than working around it in the integration test.
- Timestamps compare with `Equal`, not `==`, because pgx returns a location that differs from `time.UTC`.
- Arrays, hstore and ranges arrive as a Go `string` from pgx. If a pgdialect scanner rejects a `string` source (its type switch only lists `[]byte`), add a `case string:` branch that converts with `internal.Bytes` and cover it with a `t.Run` unit test in `dialect/pgdialect`.

- [ ] **Step 5: CI workflow**

`.github/workflows/ci.yml`:

```yaml
name: CI

on:
  push:
    branches: [master]
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16
        env:
          POSTGRES_USER: postgres
          POSTGRES_PASSWORD: postgres
          POSTGRES_DB: pgcrud
        ports:
          - 5432:5432
        options: >-
          --health-cmd "pg_isready -U postgres" --health-interval 5s --health-timeout 5s --health-retries 10
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.27.x"
      - run: go vet ./...
      - run: go test ./...
        env:
          PGCRUD_TEST_DSN: postgres://postgres:postgres@localhost:5432/pgcrud?sslmode=disable
```

- [ ] **Step 6: Report files ready to commit**

Do not run git. Report the integration test results (or that they could not run) and that `docker-compose.yml`, the workflow and the two test files are ready.

---

### Task 6: README, LICENSE and spec synchronisation

**Files:**
- Create: `README.md`, `LICENSE`
- Modify: `docs/superpowers/specs/2026-09-22-pgcrud-design.md`

- [ ] **Step 1: LICENSE**

```bash
cp /home/pi/code/bun/LICENSE /home/pi/code/bun/pgcrud/LICENSE
```

bun is BSD-2-Clause; the copyright notice must be retained unchanged.

- [ ] **Step 2: README.md**

````markdown
# pgcrud

A Postgres-only CRUD ORM derived from [bun](https://github.com/uptrace/bun) that runs on
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
PGCRUD_TEST_DSN='postgres://postgres:postgres@localhost:5432/pgcrud?sslmode=disable' go test ./...
```

Without `PGCRUD_TEST_DSN` the integration tests are skipped.

## License

BSD-2-Clause, inherited from bun. See LICENSE.
````

- [ ] **Step 3: Check the spec still matches what was built**

The spec at `docs/superpowers/specs/2026-09-22-pgcrud-design.md` was already updated for the five deviations listed at the top of this plan. Re-read sections 4.2, 4.3, 5.2 and 6 and fix any statement that no longer matches the code, for example if a pgdialect scanner needed a `string` branch (Task 5) or a snapshot case had to be removed (Task 4). Record such changes in a short "Implementation notes" section at the end of the spec.

- [ ] **Step 4: Final full run**

```bash
cd /home/pi/code/bun/pgcrud && go mod tidy && go vet ./... && go test ./... && grep -rn "uptrace\|\"bun: " --include='*.go' . ; echo "grep exit=$?"
```

Expected: vet and tests pass; the grep finds nothing (`grep exit=1`).

- [ ] **Step 5: Report files ready to commit**

Do not run git. Tell the user the project is complete, which files exist, and the exact commands to run the integration suite.
