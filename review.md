# Architecture Code Review
I'll review the pgcrud repository's architecture. Using the gitnexus-exploring skill first, since this is an architecture-understanding task.

 │ Skill 
 │ ToolSearch
 │ ReadMcpResourceTool
 │ ReadMcpResourceTool
 │ Read   /home/pi/code/pi/pgcrud/README.md
 │ Read   /home/pi/code/pi/pgcrud/docs/superpowers/specs/2026-09-22-pgcrud-design.md
 │ Read   /home/pi/code/pi/pgcrud/AGENTS.md
 │ Bash   cat go.mod && echo ---- && wc -l *.go dialect/*.go dialect/*/*.go schema/*.go...
 │ Bash   ls -la .gitnexus 2>/dev/null | head; ls .claude/skills 2>/dev/null; git log -...
 │ Grep   database/sql|sql\.|driver\.
 │ Read   /home/pi/code/pi/pgcrud/db.go
 │ Read   /home/pi/code/pi/pgcrud/executor.go
 │ Read   /home/pi/code/pi/pgcrud/hook.go
 │ Read   /home/pi/code/pi/pgcrud/schema/hook.go
 │ Read   /home/pi/code/pi/pgcrud/schema/dialect.go
 │ Read   /home/pi/code/pi/pgcrud/dialect/pgdialect/dialect.go
 │ Read   /home/pi/code/pi/pgcrud/pgcrud.go
 │ Read   /home/pi/code/pi/pgcrud/model.go
 │ mcp__gitnexus__query
 │ Read   /home/pi/code/pi/pgcrud/query_base.go
 │ Read   /home/pi/code/pi/pgcrud/model_table_struct.go
 │ Read   /home/pi/code/pi/pgcrud/model_scan.go
 │ Read   /home/pi/code/pi/pgcrud/schema/querygen.go
 │ Read   /home/pi/code/pi/pgcrud/dialect/feature/feature.go
 │ Grep   HasFeature\(feature\.\w+\)|Features\(\)\.Has\(feature\.\w+\)|dialect\.Name\(\) == |Name\(\) == dialect\.
 │ Grep   ^func (Test|Benchmark)\w+
 │ Bash   go build ./... && go vet ./... 2>&1 | tail -5; echo "vet exit: $?"; go test -...
 │ Read   /home/pi/code/pi/pgcrud/query_select.go
 │ Grep   ^func \(q \*SelectQuery\) (Scan|ScanAndCount|Count|Exists|Rows|Exec|scanResult|selectJoins|afterSelectHook|beforeSelectHook|forEachInlineRelJoin|Rows)\b  /home/pi/code/pi/pgcrud/query_select.go
 │ Read   /home/pi/code/pi/pgcrud/query_select.go
 │ Read   /home/pi/code/pi/pgcrud/relation_join.go
 │ Read   /home/pi/code/pi/pgcrud/model_table_has_many.go
 │ Read   /home/pi/code/pi/pgcrud/executor_test.go
 │ Read   /home/pi/code/pi/pgcrud/uow_test.go
 │ Read   /home/pi/code/pi/pgcrud/query_raw.go
 │ Bash   timeout 240 go run golang.org/x/tools/cmd/deadcode@latest -test ./... 2>&1 | ...
 │ Bash   grep -n "^func\|^type\|^var\|^const" schema/table.go | head -120; echo ----; ...
 │ Read   /home/pi/code/pi/pgcrud/schema/tables.go
 │ Read   /home/pi/code/pi/pgcrud/schema/table.go
 │ Read   /home/pi/code/pi/pgcrud/model_table_m2m.go
 │ Read   /home/pi/code/pi/pgcrud/schema/scan.go
 │ Read   /home/pi/code/pi/pgcrud/schema/append_value.go
 │ Read   /home/pi/code/pi/pgcrud/query_select.go
 │ Read   /home/pi/code/pi/pgcrud/query_select.go
 │ Read   /home/pi/code/pi/pgcrud/db_test.go
 │ Read   /home/pi/code/pi/pgcrud/integration_test.go
 │ Bash   grep -rn "dialect.Name()\|\.Name() ==" --include=*.go schema/ dialect/ | grep...
 │ Grep   pgDialect\b  /home/pi/code/pi/pgcrud/dialect/pgdialect
 │ Grep   Tables\(\)|db\.Table\(|table\.Dialect\(\)
 │ Read   /home/pi/code/pi/pgcrud/model_map.go


Analysis complete. I verified the package graph with go list, ran the build, vet and unit tests (all pass), and ran the deadcode analyzer to find unreachable code.
You can use gitnexus to analyze the code.

## ARCHITECTURE OVERVIEW

pgcrud is a Postgres-only fork of bun's core, re-targeted from database/sql to pgx. It is one Go module with five layers:

 Package                                                            │ Role
────────────────────────────────────────────────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
 pgcrud (root)                                                      │ Public API: DB, options, query builders (Select/Insert/Update/Delete/Raw/Values), model wrappers, relation loading, query hooks, executor port
 dialect/pgdialect                                                  │ Postgres value appenders and scanners (arrays, hstore, ranges), SQL type discovery, feature set
 schema                                                             │ Reflection metadata (Table, Field, Relation), QueryGen placeholder formatter, generic append/scan pipelines, Dialect and Model interfaces
 dialect, dialect/feature, dialect/sqltype                          │ Dialect name enum, feature bit flags, SQL type constants
 internal/*, extra/bunjson                                          │ Flags, parsers, unsafe string conversion, logger, JSON provider

The import graph is acyclic and strictly layered: root imports pgdialect and schema, pgdialect imports schema, schema imports only the leaf packages. Nothing below the root knows about DB or the executor.

The data flow for every query is the same. A builder accumulates clauses, AppendQuery renders fully inlined SQL through QueryGen, baseQuery.resolveExecutor asks the application-supplied resolver for a DBExecutor
from the context, the SQL is sent with pgx's simple protocol, and Model.ScanRows drives rows.Scan with the model itself as an sql.Scanner for every column. Relation sub-queries for has-many and m2m are built with
db.NewSelect(), so they pass through the same resolver and hooks.

## STRENGTHS

• Executor port with dependency inversion. DBExecutor in executor.go is a four-method interface satisfied structurally by *pgxpool.Pool and pgx.Tx. The library never begins or commits a transaction. The
application's unit of work hands its Executor method in as the resolver, and the library stays ignorant of the unit-of-work package. This is the cleanest architectural decision in the port.
• Fail-closed write guard. WithTxRequiredForWrites classifies raw SQL with a comment-aware tokenizer and treats anything unrecognised as a write. Misclassification only ever produces a loud error, never a silent
write outside a transaction.
• Acyclic package graph and a deliberate cut point. Removing Dialect.Init kept schema from importing the root. schema now depends on pgx only for the pgx.Rows type in the Model interface.
• Building is separable from executing. New(nil) yields a working query builder, which is what the 1600-line golden test suite in query_test.go relies on.
• Uniform hook wrapping. Every execution path calls beforeQuery/afterQuery, including the error branches where the resolver fails, so observability hooks see every outcome.
• Testability. A fake executor and fake rows in executor_test.go let the whole scan and exec path run in-memory. Integration tests replicate the real unit of work in uow_test.go.
• Design record. The spec and implementation notes in docs/ record deviations from the plan, which is rare and valuable for a fork.

## CONCERNS

1. Multi-dialect scaffolding leaks into a single-dialect public API. dialect.Name still enumerates SQLite, MySQL, MSSQL and Oracle. SelectQuery exports twelve MySQL index-hint methods that are silent no-ops on
Postgres, backed by about 230 lines of idxHintsQuery in query_base.go that can never render. query_base.go also branches on dialect.Oracle and dialect.MSSQL. The feature package defines 34 flags, but every check
against the fixed pgdialect set is a compile-time constant in disguise. The schema.Dialect interface carries DDL-only methods (DefaultVarcharLen, AppendSequence) and Field carries CreateTableSQLType, while all DDL
builders were removed. The public surface promises behaviour that cannot occur.
2. Unreachable dialect configuration. pgdialect.WithoutFeature and pgdialect.WithAppendUintAsInt exist, but pgcrud.New in db.go always calls pgdialect.New() with no options and there is no DB option that forwards
any. The deadcode analyzer confirms both are unreachable from any entry point, including tests.
3. Two SQL keyword classifiers that can disagree. queryOperation in hook.go trims whitespace and splits on a space. isReadStatement in db.go skips comments and parentheses. On top of that, RawQuery.Operation()
hard-codes "SELECT", so a query hook is told a raw UPDATE is a SELECT, and baseQuery.isWrite has to special-case *RawQuery to route around the lie. This is a missing shared abstraction rather than a bug today, but
it is the kind of seam that drifts.
4. Execution mode is a repeated literal, not a policy. pgx.QueryExecModeSimpleProtocol appears at eight call sites across db.go, query_base.go and query_select.go. The whole scanning design depends on it: with the
simple protocol pgx hands scanners freshly allocated values, while the extended protocol would hand scanners aliased buffers, as the spec's section 8 warns. A single omitted argument on a new call site would
silently change semantics. There is no one place that owns "how we talk to pgx".
5. Table registry is owned by the dialect. schema.Tables hangs off Dialect.Tables(), each Table keeps a dialect back-pointer, and the root reaches metadata through db.dialect.Tables() and table.Dialect().Tables().
This is a bun inheritance that made sense with pluggable dialects. Here it means a package-level pgDialect singleton in dialect.go owns a second, hidden registry used by the Array() and HStore() helpers, separate
from the one your DB uses. Harmless today because those appenders do not consult tables, but it is two sources of truth.
6. Concrete-type sniffing through the port. ScanAndCount decides whether to run fetch and count concurrently by asserting *pgxpool.Pool on the resolved executor. Any wrapper an application places around the pool,
for logging or metrics, silently degrades to sequential execution with no error and no signal. The executor is also resolved three times on that path.
7. Package-level mutable state reduces isolation. scannerCache and appenderCache in schema are keyed by reflect.Type only, yet Appender and Scanner accept a dialect parameter that the cache ignores.
tableNameInflector, the bunjson provider and the internal logger are process-wide as well. None of this breaks with one dialect, but the dialect parameter is misleading and the globals make parallel tests with
different settings impossible.
8. Untested Postgres value types. The deadcode run with tests included reports that Range.Scan, Range.AppendQuery, MultiRange.AppendQuery, HStoreValue.Scan, HStoreValue.Value, ArrayValue.Scan and ArrayValue.Value
are never reached, even by tests. The only coverage is the parsers and the field-level appender. Those are the parts most likely to be affected by the switch from bun's driver to pgx's typed scanner values.
9. Large mixin files. query_base.go is 1540 lines holding eight embedded mixin structs with overlapping method names (appendWhere, appendSet, appendReturning, appendCascade). This is bun's shape and is workable,
but the dead mixins above make it heavier than it needs to be.

## RECOMMENDATIONS

1. Prune the multi-dialect layer now, not later. Remove the index-hint methods and idxHintsQuery, the Oracle/MSSQL branches, DefaultVarcharLen, AppendSequence, CreateTableSQLType, and the unused entries in
dialect.Name and feature. Keep only the flags the code actually checks, or collapse the checks entirely since they are constants. Rationale: the spec lists this as a follow-up, but until it happens the exported
API advertises behaviour that does not exist, and every new contributor has to learn which half of the builder is live.
2. Resolve the dialect option dead end. Either add a WithDialectOptions(...pgdialect.DialectOption) DB option, or delete WithoutFeature and WithAppendUintAsInt. Unreachable configuration is worse than absent
configuration because it looks supported.
3. Introduce one statement classifier. Move the tokenizer from db.go into a small internal package with Operation(sql) string and IsRead(sql) bool. Have RawQuery.Operation() and queryOperation call it, and drop
the *RawQuery special case from isWrite. Hooks and the write guard then agree by construction.
4. Centralize the pgx call. Add two unexported methods on DB, one for Exec and one for Query/QueryRow, that take the resolved executor and always pass the simple-protocol mode. Route all eight sites through them
and add a unit test that asserts the mode on every path. This turns the protocol choice from a convention into a policy.
5. Replace the pool type assertion with a capability. Define an unexported marker such as interface{ Acquire(context.Context) (*pgxpool.Conn, error) } or, simpler, a ConcurrentScanAndCount query option that the
caller sets when it knows the executor is a pool. Either way a wrapped pool stops silently losing concurrency.
6. Move Tables ownership to DB, longer term. Construct schema.NewTables in pgcrud.New, pass it to models and to QueryGen, and shrink schema.Dialect to formatting and per-field hooks. This removes the dialect back-
pointer from Table and the hidden second registry behind pgDialect. It is a medium refactor touching schema/table.go, so schedule it rather than bundle it with item 1.
7. Clean up the cache signatures. Drop the ignored dialect argument from schema.Appender and schema.Scanner, or key the caches by dialect as well. Pick one so the signature tells the truth.
8. Add round-trip tests for pgdialect value types. Table-driven t.Run cases for Range, MultiRange, HStoreValue and ArrayValue covering Scan from the typed values pgx produces (string, []byte, nil) and AppendQuery.
These are the seams the port changed and they currently have zero coverage.
9. Tighten the README wording. It says the library runs "instead of database/sql", but the stdlib package is still imported for sql.Scanner and the sql.Null* types. "Instead of a database/sql driver" is accurate.

## DEPENDENCY MAP

Internal packages, arrows read "imports":

 From                                                      │ To
───────────────────────────────────────────────────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
 pgcrud (root)                                             │ dialect, dialect/feature, dialect/pgdialect, schema, internal, pgx, pgxpool, pgconn
 dialect/pgdialect                                         │ dialect, dialect/feature, dialect/sqltype, schema, internal, internal/parser
 schema                                                    │ dialect, dialect/feature, dialect/sqltype, extra/bunjson, internal, internal/parser, internal/tagparser, pgx (for pgx.Rows), xsync, msgpack, inflection
 dialect, dialect/feature                                  │ internal
 dialect/sqltype, extra/bunjson, internal/*                │ leaf packages, no internal imports

Key runtime relationships inside the root package:

• DB holds the resolver, the dialect, and QueryGen. Every builder embeds baseQuery, which holds a *DB.
• baseQuery.resolveExecutor chooses between DB.Executor and DB.writeExecutor based on the operation classifier.
• Model implementations (structTableModel, sliceTableModel, mapModel, scanModel, hasManyModel, m2mModel) all hold a *DB and reach table metadata through db.Table, which delegates to the dialect's registry.
• relationJoin builds child SelectQuery values from db.NewSelect(), closing the loop back through the same executor and hooks.

No circular dependencies exist. The one structural inversion worth fixing is that metadata ownership (Tables) sits below the layer that uses it, in the dialect, rather than beside it, in DB.
