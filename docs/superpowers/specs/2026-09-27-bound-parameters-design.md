# pgcrud bound parameters: design

Date: 2026-09-27. Implements item 1 of `todo.md`, the "real placeholders" enhancement recorded in section 8 of `2026-09-22-pgcrud-design.md`. It absorbs stage 2 of `2026-09-24-review-cleanup-design.md` (the pgx call policy on `DB`) and is compatible with that document's other stages and with `2026-09-24-sqlc-models-design.md`; section 9 says where they meet.

## 1. Goal

Every statement the library executes reaches Postgres as SQL text with `$1..$n` placeholders plus a slice of Go values, instead of fully inlined literals. This enables pgx's prepared-statement cache and binary parameter encoding, and removes the inline escaping code and the SQL-injection surface that comes with it. The `?` builder syntax stays the public API: `db.NewSelect().Model(&u).Where("id = ?", id).Scan(ctx)` reads the same and sends `WHERE (id = $1)` with `[]any{id}`.

Decisions taken during design:

- **Single bind path.** There is no inline literal mode. `String()` on a query returns the `$n` SQL and a new `Args()` returns the values. A statement that needs more than 65535 parameters fails with a clear error rather than falling back to inline text.
- **Bun's value rules are preserved.** How a Go value maps to a Postgres representation (JSON for untagged structs, arrays for `array`-tagged fields, and so on) is decided by pgcrud as today. How that representation is encoded on the wire is delegated to pgx wherever the representation is a Go value pgx can encode.
- **Binary results where safe.** Result columns arrive binary except arrays, ranges and multiranges, which the library's text parsers need in text form.

Constraints and assumptions:

- The module has no external consumers. Removing exported methods is acceptable and needs no deprecation period.
- `go build ./... && go vet ./... && go test ./...` stays green after every stage in section 8.
- The project's GitNexus rules apply during implementation: impact analysis before each symbol edit, `detect_changes` before the user commits. The user commits; the implementation never runs `git commit`.
- Every test uses `t.Run` per assertion or table row.
- pgx is `github.com/jackc/pgx/v5` v5.9.2. The codec behaviour described in section 5 was read from that version's `pgtype` package.

## 2. Approach

A shared argument collector carried by `schema.QueryGen`. Every leaf function that emits a value appends `$n` to the SQL bytes and pushes the Go value onto the collector. Subqueries, named args, relation filters and `SafeQuery` fragments all render through the same generator, so numbering is sequential across the whole statement with no coordination. The `QueryAppender` interface keeps its signature; its implementors and any appender written outside the module keep compiling.

Alternatives rejected:

- **Return args from `AppendQuery`.** Changing the signature to `([]byte, []any, error)` is explicit but touches every appender in the module and breaks any appender written outside it, for no gain over the collector.
- **Two-pass rendering.** Render with sentinel markers, then substitute `$n` and collect values in a second scan. Fragile around user text that could contain the marker, and slower.
- **Keep inline formatting for `String()` and as a fallback above the parameter limit.** Two encoding modes per value type maintained forever, and the injection-sensitive code stays. Rejected in favour of the single bind path.

## 3. Architecture and execution path

A query executes in three steps. The first two change.

### 3.1 Build

```go
// package pgcrud
// build renders q into SQL with $n placeholders and returns the bound values.
// It fails with ErrTooManyParams when more than maxParams values were bound.
func (q *baseQuery) build(iquery schema.QueryAppender) (sql string, args []any, err error)
```

`build` creates a fresh collector, wraps the DB's generator with it (`gen := q.db.gen.WithArgList(list)`), renders through `iquery.AppendQuery(gen, q.db.makeQueryBytes())`, checks the count, and returns. Rendering errors, including marshal errors from section 4.3, are returned here before an executor is resolved.

Callers: `SelectQuery.Scan`, `Rows`, `Count`, `Exists` and `ScanAndCount` (through `Count` and `Scan`), `InsertQuery.Exec`, `UpdateQuery.Exec`, `DeleteQuery.Exec`, `RawQuery.scanOrExec`, and `DB.Exec`, `DB.Query`, `DB.QueryRow` (which build a `RawQuery` internally so raw SQL goes through the same path).

### 3.2 Send

Every call on the executor has the shape:

```go
exec.Query(ctx, sql, db.queryOptions(args)...)
```

where `queryOptions` returns, in order: the `pgx.QueryExecMode` when `WithQueryExecMode` was set; the `pgx.QueryResultFormatsByOID` map from section 5.1; then the bound values. pgx reads option values from the front of the args and treats the remainder as parameters. `Exec` does not take result formats, so it receives only the exec mode, if any, and the values.

Without `WithQueryExecMode` no mode is passed and pgx's connection default applies, `QueryExecModeCacheStatement`: the statement is prepared once per connection and reused by SQL text.

```go
// WithQueryExecMode overrides pgx's default execution mode for every query.
func WithQueryExecMode(mode pgx.QueryExecMode) DBOption

// WithTextResultTypes asks Postgres to return the given column types in text
// format, for types the application registered on the pool with a codec that
// prefers binary but that pgcrud scans with its text parsers.
func WithTextResultTypes(oids ...uint32) DBOption
```

The three helper methods proposed by stage 2 of the review-cleanup spec (`execQuery`, `execExec`, `execQueryRow`) are the place `queryOptions` is applied; that stage is implemented here instead of separately.

### 3.3 Scan

Unchanged in structure. The model remains the `sql.Scanner` for every column; `ScanRows` drives `rows.Next`, `rows.Scan`, `rows.Err`. What the scanners receive changes for some column types; section 5 covers it.

### 3.4 Public surface changes

- `Query.String() string` returns the `$n` SQL. New `Query.Args() []any` returns the bound values. Both render the query, and a caller who needs both consistent uses the new `Query.Build() (string, []any, error)`; `String` and `Args` are conveniences over it. `String` panics on a render error as today.
- `QueryEvent.Query` is the SQL sent; `QueryEvent.QueryArgs` is the args sent, without the pgx option values. `QueryEvent.QueryTemplate` is removed: the template with `?` is no longer a distinct thing from what is sent, and hooks that want the builder call `IQuery.String()`.
- `DB.Exec`, `DB.Query`, `DB.QueryRow` and `DB.NewRaw` translate `?` to `$n`. A raw query containing no `?` at all passes its args straight through unchanged, so plain pgx-style SQL with `$1` works. A query that mixes `?` and `$n` is documented as unsupported and is not detected (section 6).
- Removed: `QueryGen.FormatQuery` and the dialect methods listed in section 4.4. `QueryGen.Append` keeps its name and signature but becomes a bind point.
- `schema.QueryGen` gains `WithArgList(*ArgList) QueryGen`, `Bind(b []byte, v any) []byte` (appends `$n`, pushes `v`) and `Args() []any`. `NewNopQueryGen` and `IsNop` stay: the nop generator is still used to render templates for table names and column lists, and in nop mode `Bind` writes `?` so those templates read as today.

## 4. Value binding rules

### 4.1 Bind points

Three functions in `schema` are the only places a value turns into SQL:

- `QueryGen.Append(b []byte, v any) []byte`, for `any` values from `?` args, `gen.Append` calls in the map models and `appendDriverValue`.
- `QueryGen.AppendValue(b []byte, v reflect.Value) []byte`, for reflected values from `List`, `Tuple`, `In`, `NullZero` and struct named args.
- `Field.appendValue(gen, b, strct, defaultPlaceholder)`, for model fields in Insert, Update and Values.

Each resolves the value by the rules in 4.2, then either writes inline SQL (`NULL`, `DEFAULT`, or a `QueryAppender`'s output) or calls `gen.Bind`. `AppenderFunc` keeps its signature `func(gen QueryGen, b []byte, v reflect.Value) []byte`; the per-type appenders that today write literal text instead call `gen.Bind` with the value to send. `Appender(dialect, typ)` and `FieldAppender` keep selecting the function per type, so the msgpack, JSON and array tag handling stays where it is.

### 4.2 Mapping

| Source | Bound value |
|---|---|
| `bool`, signed and unsigned ints, floats, `string`, `time.Time`, `[]byte` | as-is |
| `driver.Valuer` (`sql.Null*`, `pgtype.*`, `presence.Of[T]`, `pgcrud.NullTime`) | as-is; pgx calls `Value()` |
| `*T`, non-nil | the pointed-to value, by these rules |
| untagged struct, map, non-byte slice or array | JSON bytes from `bunjson.Marshal`, trailing newline trimmed |
| field with `type:json` or `type:jsonb`, any Go type | JSON bytes from `bunjson.Marshal`, unless the type is a `driver.Valuer` (then as-is) |
| field tagged `msgpack` | msgpack bytes |
| field tagged `array` or with a `[]` SQL type; `pgdialect.Array(v)` | the slice or array as-is; pgx encodes the Postgres array. A nil slice is inline `NULL` |
| field with hstore SQL type; `pgdialect.HStore(m)` | hstore text as a `string`. Postgres coerces a text parameter to hstore. A nil map is inline `NULL` |
| `pgdialect.Range[T]`, `pgdialect.MultiRange[T]` | range text as a `string`; Postgres coerces it to the range type it infers from the column or operator. Where nothing fixes the type, the caller writes the cast in the SQL, `?::daterange` |
| `net.IP`, `net.IPNet`, `netip.Addr`, `netip.Prefix` | their `String()` form |
| `json.RawMessage` | the bytes as-is; nil is inline `NULL` |
| `uint32`, `uint64` with `WithAppendUintAsInt` | converted to `int32` / `int64` before binding, as the current option does textually |


### 4.3 What stays inline

- `NULL` from a nil pointer, nil interface, nil slice or map in the array and hstore cases, `nullzero` zero, and `NullZero(v)` with a zero `v`. Inline because Postgres always types an inline `NULL`, and pgx cannot always type a bound one.
- `DEFAULT` on inserts for `nullzero` zero values.
- Every `QueryAppender`: `Safe`, `Name`, `Ident`, `QueryWithArgs`, subqueries, `WithQuery`, `List`, `Tuple`, `In`, `NullZero`, `ArrayValue`, `HStoreValue`, `Range`. Their `AppendQuery` runs with the shared generator, so any value inside them binds through the same collector. `List`, `Tuple` and `In` bind each element, so `IN (?)` with three ids renders `IN ($1, $2, $3)`.
- Identifiers from `?TableName`, `?TableAlias`, `?PKs`, `?TablePKs`, `?Columns` and `?TableColumns`. A struct named arg such as `?name` resolves to the field's value and binds through `Field.appendValue`.

A value that cannot be marshalled to JSON or msgpack fails the render: the leaf records the error on the collector and `build` returns it wrapped as `pgcrud: bind arg N: <err>`. Today such errors are written into the SQL as `?!(error)` text and fail at the server; failing before anything is sent is the intended improvement. `dialect.AppendError` is removed with the rest of the inline code.

### 4.4 Code removed

- `schema.Dialect` methods `AppendUint32`, `AppendUint64`, `AppendTime`, `AppendString`, `AppendBytes`, `AppendJSON`, `AppendBool`, and `schema.BaseDialect`.
- `schema.guardLineComment`, `dialect.AppendFloat32/64`, `dialect.AppendBool`, `dialect.AppendError`, `internal.NewHexEncoder` and `internal/hex.go`.
- The pgdialect text appenders: `appendTime`, `appendStringSlice`, `appendIntSlice`, `appendInt64Slice`, `appendFloat64Slice`, `appendTimeSlice`, `appendMapStringString`, `appendElem`, `appendStringElem`, `appendBytesElem`, `arrayAppendFloat64`, `appendRange` and the `*Value` wrappers around them. `ArrayValue`, `HStoreValue` and `Range` keep their `AppendQuery` methods, reimplemented over `gen.Bind`.
- `QueryGen.FormatQuery`, `DB.format`.

`Dialect` shrinks to `Name`, `Features`, `Tables`, `OnTable`, `IdentQuote`, `AppendSequence`, `DefaultVarcharLen`, `DefaultSchema`. The array, hstore and range text *parsers* stay, because scanning still receives those types as text (section 5).

### 4.5 The Values query

`ValuesQuery` keeps its per-column casts, taken from the field's discovered SQL type, so a CTE built from a model renders `VALUES ($1::BIGINT, $2::VARCHAR)`. The casts are what let Postgres type CTE columns; with bound values they are the only source of that information.

### 4.6 Relation loading

`relation_join.go` today pre-renders the parent key values of a has-many or many-to-many condition into a string with `q.db.QueryGen()` and passes the string to `Where` or `Join`. That would bypass the collector. The three builders `manyQueryCompositeIn`, `manyQueryMulti` and `m2mQuery`, with their helpers `appendChildValues`, `appendMultiValues` and `appendAdditionalJoinOnConditions`, produce `QueryAppender` values passed to `Where` and `Join` as `?` arguments, so the keys bind as `$n` in the relation statement. Each has-many or many-to-many relation still runs as its own statement with its own numbering, exactly as it does today.

### 4.7 Nested queries

A subquery passed as a `?` argument, as `TableExpr("(?) AS t", sub)`, as a `With("cte", sub)` clause, or as the `SetValues` source of an insert renders through the outer query's generator and shares its collector. `String()` on the subquery alone starts at `$1`; the same subquery embedded in an outer query is renumbered as part of the outer statement.

## 5. Scanning and result formats

### 5.1 Result formats

pgx requests each result column in the format its codec prefers, and for a `sql.Scanner` destination hands the scanner the codec's `DecodeDatabaseSQLValue` result. Read from pgx 5.9.2:

| Column type | Scanner receives in binary format |
|---|---|
| int2, int4, int8 | `int64` |
| float4, float8 | `float64` |
| bool | `bool` |
| timestamp, timestamptz, date | `time.Time` |
| uuid, numeric, interval, inet, cidr, hstore (if registered) | `string` in text form |
| bytea, json, jsonb | freshly allocated `[]byte` (jsonb without its version byte) |
| arrays, ranges, multiranges | freshly allocated `[]byte` in binary wire format |
| any unregistered type | never requested binary; arrives as `string` |

The scanners in `schema/scan.go` and the `sql.Scanner` types already accept everything except the binary wire bytes of the last row. So `DB` builds one `pgx.QueryResultFormatsByOID` map at construction that maps every array, range and multirange OID pgx registers by default to `pgx.TextFormatCode`, and passes it on every `Query` and `QueryRow`. `WithTextResultTypes` adds OIDs to the map. The OID list is written out from the `pgtype` constants (`Int4ArrayOID`, `TextArrayOID`, `Int4rangeOID`, `Int4multirangeOID` and so on) with a test that checks each listed OID's default codec is an `*ArrayCodec`, `*RangeCodec` or `*MultirangeCodec`, so a pgx upgrade that adds a type shows up as a failing test rather than a runtime parse error.

### 5.2 Aliasing

The constraint recorded in `todo.md` and section 8 of the original design does not apply. Every registered codec's `DecodeDatabaseSQLValue` returns either a typed value or a fresh copy. The path that hands a scanner the raw read buffer, `scanPlanSQLScanner` in binary, is reached only for unregistered types requested in binary, which never happens. The defensive clone in `mapModel.Scan` stays because it costs nothing.

### 5.3 Scanner changes

None structural. `scanTime` receives `time.Time` for timestamp columns and text for anything else. Ranges, arrays and hstore receive text. `ArrayValue.Scan`, `HStoreValue.Scan`, `Range.Scan` and `NullTime.Scan` are unchanged. `mapModel` stores what pgx hands it, so a map model's ints become `int64` and its timestamps `time.Time`, as they already do for text-decoded known types.

## 6. Error handling

- `ErrTooManyParams`, exported: `pgcrud: query binds N parameters, Postgres allows at most 65535`. Raised in `build`, before the executor is resolved. Query hooks see it in `AfterQuery` with no statement sent. The README points to chunking or `pgx.CopyFrom` for bulk loads.
- A JSON or msgpack marshal failure returns from `build` as `pgcrud: bind arg N: <err>`.
- pgx encoding errors, such as a `string` bound where Postgres inferred `int8`, surface from `Query` or `Exec` unchanged and are `errors.As`-able against pgx's error types.
- `pgx.ErrNoRows`, `ErrNilExecutor`, `ErrTxRequired` and the statement classifier are unaffected; the classifier reads the first keyword of the SQL text, which `$n` does not change.
- A raw query that mixes `?` and `$n` is unsupported and undetected. Detecting `$n` reliably would need a tokenizer aware of string literals and dollar quoting, which is out of scope. The README documents the rule: use `?` throughout, or `$n` throughout with no `?` anywhere.
- pgx's prepared-statement cache and a schema change: a cached statement can fail with `cached plan must change result type` after a `DDL` on the queried table. This is pgx behaviour, not pgcrud's; the README notes it and points to `WithQueryExecMode(pgx.QueryExecModeCacheDescribe)` for applications that migrate while running.

## 7. Testing

All tests use `t.Run` per assertion or table row.

1. **Golden tests.** The 177 snapshots under `testdata/snapshots` are regenerated. Each file holds the SQL, then a line `-- args: [...]` rendered with `%#v`, so a snapshot reads as the statement pgx receives; a query with no bound values shows `-- args: []`. The regenerated diff is reviewed so the SQL side differs only by `$n` replacing literals.
2. **Generator unit tests in `schema`.** Positional and named `?`, escaped `\?`, `?N` indexed args, sequential numbering across a nested `QueryAppender`, each row of the section 4.2 table, inline `NULL` and `DEFAULT`, nop-mode `?`, the parameter limit, and marshal errors failing the render.
3. **Executor tests in the root package.** The fake executor already records each call's args. Assertions change from "args equal the simple-protocol mode" to "args are the result-format map followed by the bound values", and the fake rows scan helper accepts the `[]byte` and typed inputs pgx hands over in binary. New cases: `String()`, `Args()` and `Build()` agree; `QueryEvent.QueryArgs` carries the bound values and not the option values; raw `$1` passthrough; a raw query with `?` and args; has-many and m2m loading bind parent keys; `WithQueryExecMode` puts the mode first; `WithTextResultTypes` extends the map; `ErrTooManyParams` before the executor is called.
4. **Integration tests.** Still skip without `PGCRUD_TEST_DSN`. New round trips: every row of the mapping table against a real server; arrays and ranges scanned from text while ints and timestamps arrive binary; a nested select; a Values CTE with casts; has-many loading; a bulk insert just under and just over the limit; the same statement executed twice and found once in `pg_prepared_statements`; `presence`-style `driver.Valuer` NULL and value. Run with the compose Postgres from the README.

## 8. Sequencing

Three stages, each leaving build, vet and tests green so they can be committed separately. Within a stage every task is also runnable, because a statement may mix inline literals and `$n` placeholders: a leaf that has not been converted yet still writes a valid literal while converted leaves bind.

| Stage | Summary |
|-------|---------|
| 1 | Collector and bound execution: `ArgList`, `WithArgList`, `Bind`, `BindArgs`, `BindError`; `$n` emission in every leaf of section 4; `baseQuery.build`, `queryOptions`, the result-format map, `WithQueryExecMode`, `WithTextResultTypes`; `DB.Exec/Query/QueryRow` and `NewRaw` through `build` with `$1` passthrough; `QueryEvent` changes; `String`, `Args` and `Build` on the six builders; relation join appenders; soft-delete expressions; `ErrTooManyParams`; snapshots regenerated with args. |
| 2 | Deletion: everything in section 4.4, the `Dialect` interface slimmed, `FormatQuery` gone, the `go-hex` dependency dropped, `todo.md` item 1 and the original spec's section 8 updated with the outcome including the aliasing correction. |
| 3 | Integration tests against Postgres, README sections on placeholders, `$1` passthrough, the parameter limit, exec modes and the cached-plan note. |

`Bind` on a generator with no collector attached writes `?`, the same template form the nop generator produces. The only callers without a collector are template renderers (`GetTableName`, the nop generator) where a placeholder is the right output. Stage 2 follows stage 1 rather than merging with it so the deletion diff is pure removal and easy to review.

## 9. Relation to the other specs

- **Review cleanup, stage 2** (pgx call policy on `DB`): implemented here through `queryOptions` and the three executor helpers. That stage is not to be executed separately.
- **Review cleanup, stages 1, 3, 4, 5**: independent. Stage 3 (collapse the dialect layer) becomes smaller after this work because the `Append*` methods are already gone; it should be re-planned against the slimmed `Dialect` in section 4.4.
- **sqlc models**: independent. `pgtype` and `presence.Of[T]` values bind through `driver.Valuer` and need nothing from that spec. The three-state insert and update semantics for `presence.Of[T]` remain a later design; the extension point is a predicate in the field selection loop of Insert and Update, untouched here.

## 10. Out of scope

- A per-query inline or debug rendering of the SQL with literals. Log hooks receive SQL and args separately, as pgx's own tracer does.
- Binary decoding of arrays, ranges and multiranges in the library's scanners. They stay text via the result-format map.
- Automatic chunking of statements above the parameter limit, and `= ANY($1)` rewriting of `IN` lists.
- Detecting mixed `?` and `$n` placeholders in raw SQL.
- Using `SendBatch`.
