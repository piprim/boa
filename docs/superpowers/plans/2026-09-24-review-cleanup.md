# Review Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Act on all nine recommendations of the architecture review so pgcrud's exported API only promises Postgres behaviour, the pgx protocol and SQL classification each live in one place, `DB` owns table metadata, and the ported pgdialect value types have round-trip tests.

**Architecture:** Five stages, each leaving build, vet and tests green. Stage 1 adds an `internal/sqlstmt` classifier. Stage 2 centralizes the pgx call on `DB` and replaces the pool type assertion with a structural capability. Stage 3 deletes the multi-dialect layer (feature flags, dialect names, index hints, DDL leftovers) and resolves every feature check to its Postgres constant. Stage 4 moves the table registry from the dialect to `DB` and drops the ignored dialect parameter from the appender and scanner caches. Stage 5 adds tests for `Range`, `MultiRange`, `HStoreValue` and `ArrayValue`.

**Tech Stack:** Go 1.25, pgx v5.9.2, testify, xsync. Golden SQL tests in `query_test.go` with snapshots under `testdata/snapshots`. Integration tests need Docker Postgres 16 (`docker-compose.yml`, host port 5442).

**Spec:** `docs/superpowers/specs/2026-09-24-review-cleanup-design.md`

## Global Constraints

- The user commits. Never run `git commit`, `git add` or `git push`. Each task ends by listing the files ready to commit.
- Project rule (CLAUDE.md): run GitNexus `impact({target, direction: "upstream"})` on each function, type or method before editing it and report the blast radius; run `detect_changes()` before handing files to the user. If the index is stale, run `node .gitnexus/run.cjs analyze` from the project root.
- Global rule: every distinct assertion or scenario inside a `Test*` function is wrapped in `t.Run("descriptive label", ...)`, even single-assertion tests.
- Golden suite: no snapshot for a surviving case may change. Never run `go test -update` in this plan; deleted cases have their snapshot file removed by hand.
- After every task: `go build ./... && go vet ./... && go test ./...` must pass.
- Module path is `github.com/piprim/pgcrud`. Test files in the root use package `pgcrud_test` unless they test an unexported symbol, in which case they use package `pgcrud` and end in `_internal_test.go`.
- Line numbers below were read on 2026-09-24 before any task ran. Later tasks shift them; always confirm with `grep -n` before editing.

## Review Focus

1. A hook that implements `Init(*DB)` still receives the finished `DB` after `New` is rebuilt around a config struct (Task 10 test "hook Init receives the DB").
2. A raw statement wrapped in comments or parentheses reports the real keyword to hooks and is classified as a read (Task 2 test "hook sees SELECT for a commented raw select").
3. An empty raw SQL string reports operation `""` and is refused under `WithTxRequiredForWrites` (Task 1 test "empty statement" and Task 2 test "empty raw statement is a write").
4. An executor that embeds `*pgxpool.Pool` keeps concurrent `ScanAndCount`; one that only embeds `DBExecutor` runs sequentially (Task 4 test).
5. `Range[int64].Scan` panics with "unsupported range type" because `scanElem` only handles `*time.Time` and `sql.Scanner`. Pinned as a known limitation, not fixed (Task 14 test "int64 element type is not scannable").

---

## Stage 1: statement classifier

### Task 1: `internal/sqlstmt` package

**Files:**
- Create: `internal/sqlstmt/sqlstmt.go`
- Create: `internal/sqlstmt/sqlstmt_test.go`
- Delete: `db_internal_test.go`
- Modify: `db.go:147-291` (delete `sqlSpace` through `containsWord`)
- Modify: `query_base.go:84-93` (`isWrite`)

**Interfaces:**
- Consumes: nothing new.
- Produces: `sqlstmt.Operation(sql string) string` and `sqlstmt.IsRead(sql string) bool`.

- [ ] **Step 1: Run impact analysis**

Run `impact({target: "isReadStatement", direction: "upstream"})` and `impact({target: "isWrite", direction: "upstream"})`. Report the callers. Expected: `isReadStatement` is called only by `isWrite`; `isWrite` only by `resolveExecutor`.

- [ ] **Step 2: Write the failing tests**

Create `internal/sqlstmt/sqlstmt_test.go`:

```go
package sqlstmt

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsRead(t *testing.T) {
	tests := []struct {
		name  string
		query string
		read  bool
	}{
		{name: "plain select", query: "SELECT 1", read: true},
		{name: "lowercase select with leading space and newline", query: "  select\n1", read: true},
		{name: "block comment before select", query: "/* c */ SELECT 1", read: true},
		{name: "line comment before select", query: "-- c\nSELECT 1", read: true},
		{name: "block and line comment before select", query: "/* a */ -- b\n SELECT 1", read: true},
		{name: "parenthesized union of selects", query: "(SELECT 1) UNION (SELECT 2)", read: true},
		{name: "show", query: "SHOW search_path", read: true},
		{name: "values", query: "VALUES (1)", read: true},
		{name: "explain select", query: "EXPLAIN SELECT 1", read: true},
		{name: "explain with non-analyze option", query: "EXPLAIN (VERBOSE) SELECT 1", read: true},
		{name: "explain analyze update", query: "EXPLAIN ANALYZE UPDATE t SET a = 1", read: false},
		{name: "explain analyze option list delete", query: "EXPLAIN (ANALYZE, VERBOSE) DELETE FROM t", read: false},
		{name: "explain block comment before analyze update", query: "EXPLAIN /* c */ ANALYZE UPDATE t SET a = 1", read: false},
		{name: "explain line comment before analyze delete", query: "EXPLAIN -- c\nANALYZE DELETE FROM t", read: false},
		{name: "lowercase explain analyze select executes", query: "explain analyze select 1", read: false},
		{name: "with cte wrapping insert", query: "WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x", read: false},
		{name: "insert", query: "INSERT INTO t VALUES (1)", read: false},
		{name: "empty statement", query: "", read: false},
		{name: "unterminated block comment", query: "/* SELECT 1", read: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.read, IsRead(tt.query), "query: %q", tt.query)
		})
	}
}

func TestOperation(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{name: "uppercases the keyword", query: "select 1", want: "SELECT"},
		{name: "skips leading whitespace", query: " \n\tUPDATE t SET a = 1", want: "UPDATE"},
		{name: "skips a block comment", query: "/* c */ DELETE FROM t", want: "DELETE"},
		{name: "skips a line comment", query: "-- c\nINSERT INTO t VALUES (1)", want: "INSERT"},
		{name: "skips opening parentheses", query: "((SELECT 1))", want: "SELECT"},
		{name: "keyword ends at a parenthesis", query: "VALUES(1)", want: "VALUES"},
		{name: "with is reported as WITH", query: "WITH x AS (SELECT 1) SELECT * FROM x", want: "WITH"},
		{name: "empty statement", query: "", want: ""},
		{name: "only a comment", query: "/* nothing */", want: ""},
		{name: "caps the keyword at 16 bytes", query: strings.Repeat("a", 20) + " 1", want: strings.Repeat("A", 16)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, Operation(tt.query), "query: %q", tt.query)
		})
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/sqlstmt/`
Expected: FAIL to build with `undefined: IsRead` and `undefined: Operation` (the package does not exist yet, so the error may read "no Go files" or "cannot find package"; either counts).

- [ ] **Step 4: Create the package by moving the tokenizer out of db.go**

Create `internal/sqlstmt/sqlstmt.go`. The six helpers are copied verbatim from `db.go` (lines 147-291: `sqlSpace`, `skipStatementPrefix`, `skipSpaceAndComments`, `readKeyword`, `explainAnalyzes`, `optionList`, `containsWord`); only `Operation` and `IsRead` are new:

```go
// Package sqlstmt classifies raw SQL text by its leading keyword. It is the
// single tokenizer behind RawQuery.Operation, QueryEvent.Operation and the
// WithTxRequiredForWrites guard, so query hooks and the write guard agree by
// construction.
package sqlstmt

import "strings"

// maxOperationLen caps the keyword Operation returns, matching bun's
// QueryEvent.Operation.
const maxOperationLen = 16

// sqlSpace holds the characters SQL treats as whitespace between tokens.
const sqlSpace = " \t\n\r\v\f"

// Operation returns the first keyword of sql, uppercased, after skipping
// leading whitespace, block and line comments and opening parentheses. It is
// capped at 16 bytes. An empty statement yields "".
func Operation(sql string) string {
	keyword, _ := readKeyword(skipStatementPrefix(sql))
	if len(keyword) > maxOperationLen {
		keyword = keyword[:maxOperationLen]
	}
	return strings.ToUpper(keyword)
}

// IsRead reports whether sql is a read-only statement: SELECT, SHOW, VALUES,
// or EXPLAIN without ANALYZE. Anything else, including WITH, an empty
// statement and unknown keywords, is a write so that callers fail closed.
func IsRead(sql string) bool {
	keyword, rest := readKeyword(skipStatementPrefix(sql))

	switch strings.ToUpper(keyword) {
	case "SELECT", "SHOW", "VALUES":
		return true
	case "EXPLAIN":
		// EXPLAIN ANALYZE actually runs the statement, so it is a write
		// whenever the explained statement could be one.
		return !explainAnalyzes(rest)
	}
	return false
}

// skipStatementPrefix removes leading whitespace, "/* ... */" block comments,
// "-- ..." line comments and opening parentheses, repeatedly and in any order,
// and returns the remainder.
func skipStatementPrefix(s string) string {
	for {
		s = skipSpaceAndComments(s)
		if !strings.HasPrefix(s, "(") {
			return s
		}
		s = s[1:]
	}
}

// skipSpaceAndComments removes leading whitespace, "/* ... */" block comments
// and "-- ..." line comments, repeatedly and in any order, and returns the
// remainder. Unlike skipStatementPrefix it leaves opening parentheses alone.
func skipSpaceAndComments(s string) string {
	for {
		s = strings.TrimLeft(s, sqlSpace)

		switch {
		case strings.HasPrefix(s, "/*"):
			end := strings.Index(s[2:], "*/")
			if end < 0 {
				return ""
			}
			s = s[2+end+2:]
		case strings.HasPrefix(s, "--"):
			end := strings.IndexByte(s, '\n')
			if end < 0 {
				return ""
			}
			s = s[end+1:]
		default:
			return s
		}
	}
}

// readKeyword returns the run of characters up to the next whitespace or "(",
// along with the rest of the string.
func readKeyword(s string) (keyword, rest string) {
	i := strings.IndexAny(s, sqlSpace+"(")
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i:]
}

// explainAnalyzes reports whether the text following the EXPLAIN keyword asks
// for ANALYZE, either as a parenthesised option or as the next keyword.
// Comments between EXPLAIN and what follows are skipped.
func explainAnalyzes(s string) bool {
	s = skipSpaceAndComments(s)

	if strings.HasPrefix(s, "(") {
		return containsWord(optionList(s), "ANALYZE")
	}

	keyword, _ := readKeyword(s)
	return strings.EqualFold(keyword, "ANALYZE")
}

// optionList returns the contents of the parenthesised list starting at s,
// which must begin with "(", up to its matching ")".
func optionList(s string) string {
	depth := 0
	for i := range len(s) {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[1:i]
			}
		}
	}
	return s[1:]
}

// containsWord reports whether s contains word as a whole word, ignoring case.
// Word characters are ASCII letters, digits and underscores.
func containsWord(s, word string) bool {
	isWordByte := func(c byte) bool {
		return c == '_' ||
			(c >= '0' && c <= '9') ||
			(c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z')
	}

	start := -1
	for i := 0; i <= len(s); i++ {
		if i < len(s) && isWordByte(s[i]) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			if strings.EqualFold(s[start:i], word) {
				return true
			}
			start = -1
		}
	}
	return false
}
```

- [ ] **Step 5: Run the package tests to verify they pass**

Run: `go test ./internal/sqlstmt/`
Expected: PASS, both test functions with every subtest.

- [ ] **Step 6: Delete the tokenizer from db.go and route isWrite through the package**

In `db.go`, delete everything from the line `// sqlSpace holds the characters SQL treats as whitespace between tokens.` (line 147) through the closing brace of `containsWord` (line 291), including `isReadStatement`. Remove `"strings"` from the import block; nothing else in `db.go` uses it. Add nothing else yet.

In `query_base.go`, change `isWrite` (lines 84-93) to:

```go
func (q *baseQuery) isWrite(iquery Query, query string) bool {
	if _, ok := iquery.(*RawQuery); ok {
		return !sqlstmt.IsRead(query)
	}
	switch iquery.Operation() {
	case "INSERT", "UPDATE", "DELETE":
		return true
	}
	return false
}
```

Add `"github.com/piprim/pgcrud/internal/sqlstmt"` to the imports of `query_base.go`.

Delete `db_internal_test.go` (its cases now live in `internal/sqlstmt/sqlstmt_test.go`).

- [ ] **Step 7: Verify the whole module**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages PASS. `TestTxRequiredForWrites` in `db_test.go` still passes; it exercises the guard end to end.

- [ ] **Step 8: Hand off for commit**

Run `detect_changes()` and confirm only `isWrite` and the deleted `db.go` helpers appear as changed symbols. Report to the user: files ready to commit are `internal/sqlstmt/sqlstmt.go`, `internal/sqlstmt/sqlstmt_test.go`, `db.go`, `query_base.go`, and the deleted `db_internal_test.go`. Suggested message: `refactor: move SQL statement classifier into internal/sqlstmt`.

---

### Task 2: One classifier for hooks and the write guard

**Files:**
- Modify: `query_raw.go:93-95` (`Operation`)
- Modify: `hook.go:29-46` (`QueryEvent.Operation`, delete `queryOperation`)
- Modify: `query_base.go:74-93` (`resolveExecutor` comment, `isWrite`)
- Test: `db_test.go`

**Interfaces:**
- Consumes: `sqlstmt.Operation`, `sqlstmt.IsRead` from Task 1.
- Produces: `RawQuery.Operation()` returns the statement's real keyword; `baseQuery.isWrite` has no type switch.

- [ ] **Step 1: Run impact analysis**

Run `impact({target: "Operation", direction: "upstream"})` for the `RawQuery` receiver and `impact({target: "queryOperation", direction: "upstream"})`. Expected: `RawQuery.Operation` is reached through the `Query` interface from `isWrite` and `QueryEvent.Operation`; `queryOperation` is called only by `QueryEvent.Operation`.

- [ ] **Step 2: Write the failing tests**

Append to `db_test.go`:

```go
func TestRawQueryOperation(t *testing.T) {
	ctx := context.Background()

	t.Run("hook sees the real keyword of a raw update", func(t *testing.T) {
		hook := &recordingHook{}
		exec := &fakeExecutor{tag: pgconn.NewCommandTag("UPDATE 1")}
		db := withExec(exec).WithQueryHook(hook)
		_, err := db.NewRaw("update users set name = 'x'").Exec(ctx)
		require.NoError(t, err)
		require.Equal(t, "UPDATE", hook.last.Operation())
	})

	t.Run("hook sees SELECT for a commented raw select", func(t *testing.T) {
		hook := &recordingHook{}
		exec := &fakeExecutor{rows: newFakeRows(cols("id", "name"), []any{int64(1), "a"})}
		db := withExec(exec).WithQueryHook(hook)
		var u User
		require.NoError(t, db.NewRaw("/* c */ (select id, name from users)").Scan(ctx, &u))
		require.Equal(t, "SELECT", hook.last.Operation())
	})

	t.Run("DB.Exec event normalizes the keyword", func(t *testing.T) {
		hook := &recordingHook{}
		exec := &fakeExecutor{tag: pgconn.NewCommandTag("DELETE 0")}
		db := withExec(exec).WithQueryHook(hook)
		_, err := db.Exec(ctx, "-- cleanup\ndelete from users")
		require.NoError(t, err)
		require.Equal(t, "DELETE", hook.last.Operation())
	})

	t.Run("a raw select behind a comment is a read under WithTxRequiredForWrites", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("id", "name"), []any{int64(1), "a"})}
		db := pgcrud.New(nil,
			pgcrud.WithExecutorResolver(func(context.Context) pgcrud.DBExecutor { return exec }),
			pgcrud.WithTxRequiredForWrites())
		var u User
		require.NoError(t, db.NewRaw("-- c\nselect id, name from users").Scan(ctx, &u))
	})

	t.Run("a raw explain analyze is a write", func(t *testing.T) {
		exec := &fakeExecutor{}
		db := pgcrud.New(nil,
			pgcrud.WithExecutorResolver(func(context.Context) pgcrud.DBExecutor { return exec }),
			pgcrud.WithTxRequiredForWrites())
		_, err := db.NewRaw("EXPLAIN ANALYZE DELETE FROM users").Exec(ctx)
		require.ErrorIs(t, err, pgcrud.ErrTxRequired)
		require.Empty(t, exec.calls)
	})

	t.Run("empty raw statement is a write", func(t *testing.T) {
		exec := &fakeExecutor{}
		db := pgcrud.New(nil,
			pgcrud.WithExecutorResolver(func(context.Context) pgcrud.DBExecutor { return exec }),
			pgcrud.WithTxRequiredForWrites())
		_, err := db.NewRaw("").Exec(ctx)
		require.ErrorIs(t, err, pgcrud.ErrTxRequired)
	})
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./ -run TestRawQueryOperation -v`
Expected: "hook sees the real keyword of a raw update" FAILS with `expected: "UPDATE" actual: "SELECT"`. "DB.Exec event normalizes the keyword" FAILS with `actual: "delete"`. The others pass already.

- [ ] **Step 4: Implement**

`query_raw.go`: replace `Operation`:

```go
// Operation returns the statement's leading keyword, uppercased, so hooks
// see UPDATE for a raw update rather than a hard-coded SELECT.
func (q *RawQuery) Operation() string {
	return sqlstmt.Operation(q.query)
}
```

Add `"github.com/piprim/pgcrud/internal/sqlstmt"` to its imports.

`hook.go`: replace `Operation` and delete `queryOperation`:

```go
// Operation returns the SQL operation name such as SELECT or UPDATE.
func (e *QueryEvent) Operation() string {
	if e.IQuery != nil {
		return e.IQuery.Operation()
	}
	return sqlstmt.Operation(e.Query)
}
```

Remove `"strings"` and `"unicode"` from the imports of `hook.go`; add `"github.com/piprim/pgcrud/internal/sqlstmt"`.

`query_base.go`: replace the comment above `resolveExecutor` and the body of `isWrite`:

```go
// resolveExecutor picks the executor for iquery. Writes go through
// writeExecutor so WithTxRequiredForWrites is enforced. Builder queries report
// a fixed operation and are trusted; any other operation, which is only ever a
// raw statement, is classified from its SQL text and fails closed.
func (q *baseQuery) resolveExecutor(ctx context.Context, iquery Query, query string) (DBExecutor, error) {
	if q.isWrite(iquery, query) {
		return q.db.writeExecutor(ctx)
	}
	return q.db.Executor(ctx)
}

func (q *baseQuery) isWrite(iquery Query, query string) bool {
	switch iquery.Operation() {
	case "SELECT", "VALUES":
		return false
	case "INSERT", "UPDATE", "DELETE":
		return true
	}
	return !sqlstmt.IsRead(query)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./ -run 'TestRawQueryOperation|TestTxRequiredForWrites|TestQueryHooks' -v`
Expected: PASS for every subtest.

- [ ] **Step 6: Verify the whole module**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 7: Hand off for commit**

Run `detect_changes()`. Report files ready to commit: `query_raw.go`, `hook.go`, `query_base.go`, `db_test.go`. Suggested message: `fix: report the real operation of raw queries to hooks`.

---

## Stage 2: pgx call policy and concurrency capability

### Task 3: One place for the execution mode

**Files:**
- Modify: `executor.go` (add three methods)
- Modify: `db.go` (`Exec`, `Query`, `QueryRow` around lines 260-310 after Task 1's deletion)
- Modify: `query_base.go` (`_scan`, `exec`)
- Modify: `query_select.go` (`Rows`, `Count`, `selectExists`)
- Test: `db_test.go`

**Interfaces:**
- Produces: `(*DB).execSQL(ctx, exec DBExecutor, sql string) (pgconn.CommandTag, error)`, `(*DB).querySQL(ctx, exec DBExecutor, sql string) (pgx.Rows, error)`, `(*DB).queryRowSQL(ctx, exec DBExecutor, sql string) pgx.Row`.

- [ ] **Step 1: Run impact analysis**

Run `impact` with `direction: "upstream"` on `Exec`, `Query`, `QueryRow` (the `DB` receivers), `_scan`, `exec` (the `baseQuery` receivers), `Rows`, `Count`, `selectExists`. Report the counts. These are the eight sites; the change is internal to each, so expected risk is LOW.

- [ ] **Step 2: Write the failing test**

Append to `db_test.go`:

```go
func TestSimpleProtocolOnEveryPath(t *testing.T) {
	ctx := context.Background()

	userRows := func() *fakeRows { return newFakeRows(cols("id", "name"), []any{int64(1), "a"}) }
	countRows := func() *fakeRows { return newFakeRows(cols("count"), []any{int64(1)}) }
	boolRows := func() *fakeRows { return newFakeRows(cols("exists"), []any{true}) }

	paths := []struct {
		name string
		rows func() *fakeRows
		run  func(db *pgcrud.DB) error
	}{
		{"DB.Exec", userRows, func(db *pgcrud.DB) error {
			_, err := db.Exec(ctx, "DELETE FROM users")
			return err
		}},
		{"DB.Query", userRows, func(db *pgcrud.DB) error {
			rows, err := db.Query(ctx, "SELECT id, name FROM users")
			if err == nil {
				rows.Close()
			}
			return err
		}},
		{"DB.QueryRow", countRows, func(db *pgcrud.DB) error {
			var n int64
			return db.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&n)
		}},
		{"Select.Scan", userRows, func(db *pgcrud.DB) error {
			var us []User
			return db.NewSelect().Model(&us).Scan(ctx)
		}},
		{"Select.Rows", userRows, func(db *pgcrud.DB) error {
			rows, err := db.NewSelect().Model((*User)(nil)).Rows(ctx)
			if err == nil {
				rows.Close()
			}
			return err
		}},
		{"Select.Count", countRows, func(db *pgcrud.DB) error {
			_, err := db.NewSelect().Model((*User)(nil)).Count(ctx)
			return err
		}},
		{"Select.Exists", boolRows, func(db *pgcrud.DB) error {
			_, err := db.NewSelect().Model((*User)(nil)).Exists(ctx)
			return err
		}},
		{"Insert.Exec", userRows, func(db *pgcrud.DB) error {
			_, err := db.NewInsert().Model(&User{ID: 1, Name: "a"}).Exec(ctx)
			return err
		}},
		{"Update.Exec", userRows, func(db *pgcrud.DB) error {
			_, err := db.NewUpdate().Model((*User)(nil)).Set("name = 'b'").Where("id = 1").Exec(ctx)
			return err
		}},
		{"Delete.Exec", userRows, func(db *pgcrud.DB) error {
			_, err := db.NewDelete().Model((*User)(nil)).Where("id = 1").Exec(ctx)
			return err
		}},
		{"Raw.Exec", userRows, func(db *pgcrud.DB) error {
			_, err := db.NewRaw("DELETE FROM users").Exec(ctx)
			return err
		}},
		{"Raw.Scan", userRows, func(db *pgcrud.DB) error {
			var u User
			return db.NewRaw("SELECT id, name FROM users").Scan(ctx, &u)
		}},
	}

	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			exec := &fakeExecutor{rows: p.rows(), tag: pgconn.NewCommandTag("DELETE 1")}
			require.NoError(t, p.run(withExec(exec)))
			require.NotEmpty(t, exec.calls)
			for _, c := range exec.calls {
				require.Equal(t, []any{pgx.QueryExecModeSimpleProtocol}, c.args, "%s: %s", c.method, c.sql)
			}
		})
	}
}
```

- [ ] **Step 3: Run the test to verify it passes today**

Run: `go test ./ -run TestSimpleProtocolOnEveryPath -v`
Expected: PASS. This test is a characterization test: it pins the behaviour before the refactor so the next step cannot silently drop the mode on any path. If any subtest fails here, fix the test's fake rows, not the library.

- [ ] **Step 4: Add the three methods to executor.go**

Append to `executor.go`:

```go
// execMode is the only pgx execution mode pgcrud uses. Every statement is
// sent as text with the simple protocol. With it, pgx hands every sql.Scanner
// a freshly allocated []byte or an immutable string, so scanners may retain
// what they receive. The extended protocol would hand them a slice of pgx's
// read buffer that is only valid until the next Scan; switching modes requires
// every retaining scanner to copy first. See section 8 of
// docs/superpowers/specs/2026-09-22-pgcrud-design.md.
const execMode = pgx.QueryExecModeSimpleProtocol

// execSQL runs sql without returning rows. It is the only Exec call pgcrud
// makes on an executor.
func (db *DB) execSQL(ctx context.Context, exec DBExecutor, sql string) (pgconn.CommandTag, error) {
	return exec.Exec(ctx, sql, execMode)
}

// querySQL runs sql and returns its rows. It is the only Query call pgcrud
// makes on an executor.
func (db *DB) querySQL(ctx context.Context, exec DBExecutor, sql string) (pgx.Rows, error) {
	return exec.Query(ctx, sql, execMode)
}

// queryRowSQL runs sql and returns its single row. It is the only QueryRow
// call pgcrud makes on an executor.
func (db *DB) queryRowSQL(ctx context.Context, exec DBExecutor, sql string) pgx.Row {
	return exec.QueryRow(ctx, sql, execMode)
}
```

- [ ] **Step 5: Route the eight sites through them**

`db.go`:
- In `Exec`: `res, err = exec.Exec(ctx, formattedQuery, pgx.QueryExecModeSimpleProtocol)` becomes `res, err = db.execSQL(ctx, exec, formattedQuery)`.
- In `Query`: `rows, err = exec.Query(ctx, formattedQuery, pgx.QueryExecModeSimpleProtocol)` becomes `rows, err = db.querySQL(ctx, exec, formattedQuery)`.
- In `QueryRow`: `row := exec.QueryRow(ctx, formattedQuery, pgx.QueryExecModeSimpleProtocol)` becomes `row := db.queryRowSQL(ctx, exec, formattedQuery)`.
- `"github.com/jackc/pgx/v5"` stays imported (`pgx.Rows`, `pgx.Row` in signatures).

`query_base.go`:
- In `_scan`: `rows, err := exec.Query(ctx, query, pgx.QueryExecModeSimpleProtocol)` becomes `rows, err := q.db.querySQL(ctx, exec, query)`.
- In `exec`: `res, err = exec.Exec(ctx, query, pgx.QueryExecModeSimpleProtocol)` becomes `res, err = q.db.execSQL(ctx, exec, query)`.
- `pgx` stays imported for `pgx.ErrNoRows`.

`query_select.go`:
- In `Rows`: `rows, err = exec.Query(ctx, query, pgx.QueryExecModeSimpleProtocol)` becomes `rows, err = q.db.querySQL(ctx, exec, query)`.
- In `Count`: `err = exec.QueryRow(ctx, query, pgx.QueryExecModeSimpleProtocol).Scan(&num)` becomes `err = q.db.queryRowSQL(ctx, exec, query).Scan(&num)`.
- In `selectExists`: `err = exec.QueryRow(ctx, query, pgx.QueryExecModeSimpleProtocol).Scan(&exists)` becomes `err = q.db.queryRowSQL(ctx, exec, query).Scan(&exists)`.
- `pgx` stays imported for `pgx.Rows`.

- [ ] **Step 6: Verify the constant appears in exactly one production file**

Run: `grep -rn "QueryExecMode" --include='*.go' . | grep -v _test.go`
Expected: exactly one line, in `executor.go`.

- [ ] **Step 7: Verify the whole module**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS, including `TestSimpleProtocolOnEveryPath` and the two existing assertions in `TestExec` and `TestExecutorResolver` that check the mode argument.

- [ ] **Step 8: Hand off for commit**

Run `detect_changes()`. Report files ready to commit: `executor.go`, `db.go`, `query_base.go`, `query_select.go`, `db_test.go`. Suggested message: `refactor: route every pgx call through one execution-mode policy`.

---

### Task 4: Concurrency capability for ScanAndCount

**Files:**
- Modify: `executor.go:44-48` (replace `isPool`)
- Modify: `query_select.go:1040-1060` (`ScanAndCount`)
- Create: `executor_internal_test.go`

**Interfaces:**
- Produces: unexported `poolAcquirer` interface and `supportsConcurrentQueries(exec DBExecutor) bool`.

- [ ] **Step 1: Run impact analysis**

Run `impact({target: "isPool", direction: "upstream"})` and `impact({target: "ScanAndCount", direction: "upstream"})`. Expected: `isPool` is called only by `ScanAndCount`; `ScanAndCount` has test callers and no library callers.

- [ ] **Step 2: Write the failing test**

Create `executor_internal_test.go`:

```go
package pgcrud

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// txOnly has every pgx.Tx method through the embedded interface and no Acquire.
type txOnly struct{ pgx.Tx }

// embedsPool is what an application wrapper that adds logging around the pool
// looks like: it inherits Acquire from the embedded *pgxpool.Pool.
type embedsPool struct{ *pgxpool.Pool }

// wrapsExecutor forwards to some DBExecutor without exposing the pool.
type wrapsExecutor struct{ DBExecutor }

func TestSupportsConcurrentQueries(t *testing.T) {
	t.Run("a pool supports concurrent queries", func(t *testing.T) {
		require.True(t, supportsConcurrentQueries((*pgxpool.Pool)(nil)))
	})

	t.Run("a transaction does not", func(t *testing.T) {
		require.False(t, supportsConcurrentQueries(txOnly{}))
	})

	t.Run("a wrapper embedding the pool inherits the capability", func(t *testing.T) {
		require.True(t, supportsConcurrentQueries(embedsPool{}))
	})

	t.Run("a wrapper that hides the pool does not", func(t *testing.T) {
		require.False(t, supportsConcurrentQueries(wrapsExecutor{}))
	})
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./ -run TestSupportsConcurrentQueries`
Expected: FAIL to build with `undefined: supportsConcurrentQueries`.

- [ ] **Step 4: Implement**

In `executor.go`, replace `isPool` and its comment with:

```go
// poolAcquirer is satisfied by *pgxpool.Pool and by any executor that embeds
// it. Only such executors may run two queries at once; pgx.Tx and
// *pgxpool.Conn are single connections and never satisfy it.
type poolAcquirer interface {
	Acquire(ctx context.Context) (*pgxpool.Conn, error)
}

// supportsConcurrentQueries reports whether exec can serve two queries at the
// same time. A wrapper that hides the pool behind DBExecutor alone degrades to
// sequential execution, which is always safe.
func supportsConcurrentQueries(exec DBExecutor) bool {
	_, ok := exec.(poolAcquirer)
	return ok
}
```

In `query_select.go`, update `ScanAndCount`:

```go
// ScanAndCount executes the query, scans results into dest, and returns the total count.
// The fetch and the count run concurrently only when the executor can serve
// two queries at once (a pool, or a wrapper embedding one); a transaction or a
// dedicated connection is a single connection and gets them in sequence.
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
	if supportsConcurrentQueries(exec) {
		return q.scanAndCountConcurrently(ctx, dest...)
	}
	return q.scanAndCountSeq(ctx, dest...)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./ -run 'TestSupportsConcurrentQueries|TestScanAndCount' -v`
Expected: PASS.

- [ ] **Step 6: Verify the whole module**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS. `grep -n "isPool" *.go` finds nothing.

- [ ] **Step 7: Hand off for commit**

Run `detect_changes()`. Report files ready to commit: `executor.go`, `query_select.go`, `executor_internal_test.go`. Suggested message: `fix: detect concurrent-capable executors structurally instead of by pool type`.

---

## Stage 3: collapse the dialect layer to Postgres

### Task 5: Remove index hints

**Files:**
- Modify: `query_select.go` (struct at line 25; methods 197-295; `appendIndexHints` call at 647-650; `Clone` at 1191 and 1226-1230)
- Modify: `query_update.go` (struct at line 18; `appendIndexHints` call at 285-288; methods 654-675)
- Modify: `query_base.go:1193-1424` (`idxHintsQuery`, `indexHints` and all their methods)
- Modify: `query_test.go` (cases 131-145, 149, 150)
- Delete: `testdata/snapshots/TestQuery-pg-131` … `-145`, `-149`, `-150` (17 files)

**Interfaces:**
- Produces: `SelectQuery` and `UpdateQuery` no longer embed `idxHintsQuery`.

- [ ] **Step 1: Run impact analysis**

Run `impact({target: "idxHintsQuery", direction: "upstream"})` and `impact({target: "appendIndexHints", direction: "upstream"})`. Expected: only `SelectQuery`, `UpdateQuery` and the golden tests depend on them.

- [ ] **Step 2: Remove the golden cases and their snapshots**

In `query_test.go` delete the `test` literals with `id` 131, 132, 133, 134, 135, 136, 137, 138, 139, 140, 141, 142, 143, 144, 145, 149 and 150 (each is a `{ id: N, query: func(...) {...} },` block; 131-145 are contiguous, 149-150 sit after 148).

Run: `cd testdata/snapshots && rm TestQuery-pg-131 TestQuery-pg-132 TestQuery-pg-133 TestQuery-pg-134 TestQuery-pg-135 TestQuery-pg-136 TestQuery-pg-137 TestQuery-pg-138 TestQuery-pg-139 TestQuery-pg-140 TestQuery-pg-141 TestQuery-pg-142 TestQuery-pg-143 TestQuery-pg-144 TestQuery-pg-145 TestQuery-pg-149 TestQuery-pg-150`

- [ ] **Step 3: Run the golden suite to confirm the remaining cases still pass**

Run: `go test ./ -run TestQuery`
Expected: PASS (the deleted cases are gone; nothing else changed).

- [ ] **Step 4: Delete the methods and the mixin**

`query_select.go`:
- Remove the `idxHintsQuery` line from the `SelectQuery` struct.
- Delete the twelve methods `UseIndex`, `UseIndexForJoin`, `UseIndexForOrderBy`, `UseIndexForGroupBy`, `IgnoreIndex`, `IgnoreIndexForJoin`, `IgnoreIndexForOrderBy`, `IgnoreIndexForGroupBy`, `ForceIndex`, `ForceIndexForJoin`, `ForceIndexForOrderBy`, `ForceIndexForGroupBy` together with their doc comments and the `//----` separator that precedes them (lines 196-295).
- In `appendQuery`, delete the four lines:
  ```go
  b, err = q.appendIndexHints(gen, b)
  if err != nil {
  	return nil, err
  }
  ```
- In `Clone`, delete the local `cloneHints := func(hints *indexHints) *indexHints { ... }` closure (starts at line 1191, ends at its closing `}`) and the `idxHintsQuery: idxHintsQuery{ ... },` block in the returned literal.

`query_update.go`:
- Remove the `idxHintsQuery` line from the `UpdateQuery` struct.
- In `AppendQuery`, delete the four-line `appendIndexHints` block.
- Delete `UseIndex`, `IgnoreIndex`, `ForceIndex` (lines 654-675) and the separator before them.
- Remove `"github.com/piprim/pgcrud/dialect"` from the imports (those three methods were its only users in this file).

`query_base.go`:
- Delete from the `//----` separator at line 1193 through the closing brace of `bufIndexHint` (line 1423), so that `cascadeQuery.appendCascade` is followed directly by the separator above `orderLimitOffsetQuery`.
- Run `go build ./...`; if it reports `"fmt" imported and not used`, remove the import (`bufIndexHint` used `fmt.Sprintf`).

- [ ] **Step 5: Verify the whole module**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS. `grep -rn "Index(" --include='*.go' . | grep -i "useindex\|ignoreindex\|forceindex"` finds nothing.

- [ ] **Step 6: Hand off for commit**

Run `detect_changes()`. Report files ready to commit: `query_select.go`, `query_update.go`, `query_base.go`, `query_test.go`, and the 17 deleted snapshot files. Suggested message: `refactor: drop MySQL index hints from the query builders`.

---

### Task 6: Remove Order and Limit on Delete and Update, and Insert Replace

**Files:**
- Modify: `query_delete.go` (struct line 18; `Order`/`OrderExpr`/`Limit` at 131-165; `AppendQuery` at 193-280)
- Modify: `query_update.go` (struct line 20; `Order`/`OrderExpr`/`Limit` at 206-235; `AppendQuery`)
- Modify: `query_insert.go` (struct 17-28; `Ignore` and `Replace` at 162-180; `AppendQuery` at 209-218)
- Modify: `query_test.go` (cases 55, 168, 169, 170, 171)
- Delete: `testdata/snapshots/TestQuery-pg-55`, `-168`, `-169`, `-170`, `-171`

**Interfaces:**
- Produces: `DeleteQuery` and `UpdateQuery` no longer embed `orderLimitOffsetQuery`; `InsertQuery` loses `Replace`, the `ignore` and `replace` fields.

- [ ] **Step 1: Run impact analysis**

Run `impact` upstream on `Order`, `OrderExpr`, `Limit` (the `DeleteQuery` and `UpdateQuery` receivers) and on `Replace`. Expected: only the golden tests call them.

- [ ] **Step 2: Remove the golden cases and their snapshots**

In `query_test.go` delete the `test` literals with `id` 55, 168, 169, 170 and 171.

Run: `cd testdata/snapshots && rm TestQuery-pg-55 TestQuery-pg-168 TestQuery-pg-169 TestQuery-pg-170 TestQuery-pg-171`

Run: `go test ./ -run TestQuery`
Expected: PASS.

- [ ] **Step 3: Edit DeleteQuery**

`query_delete.go`:
- Remove `orderLimitOffsetQuery` from the struct.
- Delete `Order`, `OrderExpr` and `Limit` (keep `ForceDelete`, which sits between them).
- In `AppendQuery`, delete these three blocks:
  ```go
  if q.hasMultiTables() && (len(q.order) > 0 || q.limit > 0) {
  	return nil, errors.New("pgcrud: can't use ORDER or LIMIT with multiple tables")
  }

  b, err = q.appendOrder(gen, b)
  if err != nil {
  	return nil, err
  }

  b, err = q.appendLimitOffset(gen, b)
  if err != nil {
  	return nil, err
  }
  ```
- Remove `"errors"` from the imports if the build reports it unused.

- [ ] **Step 4: Edit UpdateQuery**

`query_update.go`:
- Remove `orderLimitOffsetQuery` from the struct.
- Delete `Order`, `OrderExpr` and `Limit` and the separator comment above `Order`.
- In `AppendQuery`, delete the `appendOrder` and `appendLimitOffset` four-line blocks.

- [ ] **Step 5: Edit InsertQuery**

`query_insert.go`:
- Remove the `ignore bool` and `replace bool` fields from the struct.
- Replace `Ignore` and delete `Replace`:
  ```go
  // Ignore makes conflicting rows be skipped: INSERT ... ON CONFLICT DO NOTHING.
  func (q *InsertQuery) Ignore() *InsertQuery {
  	return q.On("CONFLICT DO NOTHING")
  }
  ```
- In `AppendQuery`, replace
  ```go
  if q.replace {
  	b = append(b, "REPLACE "...)
  } else {
  	b = append(b, "INSERT "...)
  	if q.ignore {
  		b = append(b, "IGNORE "...)
  	}
  }
  b = append(b, "INTO "...)
  ```
  with
  ```go
  b = append(b, "INSERT INTO "...)
  ```

- [ ] **Step 6: Verify the whole module**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS. Golden case 54 (`Ignore()`) still renders `ON CONFLICT DO NOTHING`.

- [ ] **Step 7: Hand off for commit**

Run `detect_changes()`. Report files ready to commit: `query_delete.go`, `query_update.go`, `query_insert.go`, `query_test.go`, five deleted snapshots. Suggested message: `refactor: drop ORDER/LIMIT on DELETE and UPDATE and REPLACE INTO`.

---

### Task 7: Resolve every feature and dialect-name check in the root package

**Files:**
- Modify: `query_base.go` (`hasFeature`, `appendCTE`, `appendSelectFromValues`, `_appendTables`, `appendOutput`, `appendSetStruct`, `appendCascade`, `appendOrder`, `appendLimitOffset`)
- Modify: `query_select.go` (`appendQuery` union wrap and MSSQL `_temp_sort`)
- Modify: `query_insert.go` (`AppendQuery`, `appendColumnsValues`, `appendStructValues`, `getFields`, `appendOn`, `scanOrExec`)
- Modify: `query_update.go` (`SetColumn`, `AppendQuery`, `updateSliceSet`, `scanOrExec`, `FQN`, `hasTableAlias`)
- Modify: `query_delete.go` (`Returning`, `AppendQuery`, `softDeleteSet`, `scanOrExec`)
- Modify: `query_values.go` (`AppendQuery`, `appendValues`)
- Modify: `model_map_slice.go` (around line 111)
- Modify: `relation_join.go` (line 62, delete `manyQueryMulti`)
- Modify: `db.go` (`UpdateFQN`, delete `HasFeature`)
- Modify: `schema/table.go:670-673`
- Modify: `schema/querygen.go:114-116` (delete `HasFeature`)

**Interfaces:**
- Produces: no `feature` or `dialect.Name` reference remains in the root package or in `schema/table.go`. `DB.HasFeature`, `QueryGen.HasFeature` and `baseQuery.hasFeature` are gone.

Postgres's answers, from `pgdialect.New` on 2026-09-24. True: `CTE`, `WithValues`, `Returning`, `InsertReturning`, `DefaultPlaceholder`, `DoubleColonCast`, `InsertTableAlias`, `UpdateTableAlias`, `DeleteTableAlias`, `TableCascade`, `InsertOnConflict`, `SelectExists`, `CompositeIn`, `FKDefaultOnAction`, `DeleteReturning`. False: `Output`, `ValuesRow`, `OffsetFetch`, `InsertOnDuplicateKey`, `InsertIgnore`, `UpdateMultiTable`, `UpdateOrderLimit`, `DeleteOrderLimit`, `Identity`. Name is `PG`.

- [ ] **Step 1: Run impact analysis**

Run `impact({target: "HasFeature", direction: "upstream"})` for both the `DB` and `QueryGen` receivers and `impact({target: "hasFeature", direction: "upstream"})`. Report the list; it should match the file list above. Risk is MEDIUM by count but every edit replaces a check by its constant, and the golden suite pins the output.

- [ ] **Step 2: Confirm the golden suite is green before touching anything**

Run: `go test ./ -run TestQuery`
Expected: PASS. This suite is the test for this task: after every edit below it must still pass with no snapshot change.

- [ ] **Step 3: query_base.go**

- Delete `hasFeature` (the three-line method after `beforeAppendModel`).
- In `appendCTE`, delete:
  ```go
  if !gen.Dialect().Features().Has(feature.WithValues) {
  	if values, ok := cte.query.(*ValuesQuery); ok {
  		return q.appendSelectFromValues(gen, b, cte, values)
  	}
  }
  ```
  and change the switch to:
  ```go
  switch {
  case cte.materialized:
  	b = append(b, " AS MATERIALIZED ("...)
  case cte.notMaterialized:
  	b = append(b, " AS NOT MATERIALIZED ("...)
  default:
  	b = append(b, " AS ("...)
  }
  ```
- Delete the whole `appendSelectFromValues` method.
- In `_appendTables`, replace
  ```go
  if q.db.dialect.Name() == dialect.Oracle {
  	b = append(b, ' ')
  } else {
  	b = append(b, " AS "...)
  }
  ```
  with `b = append(b, " AS "...)`.
- In `appendSetStruct`, delete the line `defaultPlaceholder := gen.HasFeature(feature.DefaultPlaceholder)` and replace
  ```go
  } else if defaultPlaceholder {
  	b = f.AppendValueOrDefault(gen, b, model.strct)
  } else {
  	b = f.AppendValue(gen, b, model.strct)
  }
  ```
  with
  ```go
  } else {
  	b = f.AppendValueOrDefault(gen, b, model.strct)
  }
  ```
- In `appendCascade`, delete the `if !gen.HasFeature(feature.TableCascade) { return b }` guard.
- Delete `returningQuery.appendOutput` (three lines plus comment).
- In `appendOrder`, delete the MSSQL comment and the `if q.limit > 0 && gen.Dialect().Name() == dialect.MSSQL { ... }` block, leaving `return b, nil`.
- Replace `appendLimitOffset` with:
  ```go
  func (q *orderLimitOffsetQuery) appendLimitOffset(gen schema.QueryGen, b []byte) (_ []byte, err error) {
  	if q.limit > 0 {
  		b = append(b, " LIMIT "...)
  		b = strconv.AppendInt(b, int64(q.limit), 10)
  	}
  	if q.offset > 0 {
  		b = append(b, " OFFSET "...)
  		b = strconv.AppendInt(b, int64(q.offset), 10)
  	}
  	return b, nil
  }
  ```
- Remove `"github.com/piprim/pgcrud/dialect"` and `"github.com/piprim/pgcrud/dialect/feature"` from the imports.

Run: `go build ./ && go test ./ -run TestQuery` — expected PASS (build may fail until the other files in this task are done; if so, continue and rerun at the end).

- [ ] **Step 4: query_select.go**

- Replace `wrapUnion := len(q.union) > 0 && gen.Dialect().Name() != dialect.SQLite` and its SQLite comment with `wrapUnion := len(q.union) > 0`.
- In `appendQuery`, delete the MSSQL comment and:
  ```go
  if q.limit > 0 && len(q.order) == 0 && gen.Dialect().Name() == dialect.MSSQL {
  	b = append(b, "0 AS _temp_sort, "...)
  }
  ```
- Remove `"github.com/piprim/pgcrud/dialect"` from the imports.

- [ ] **Step 5: query_insert.go**

- In `AppendQuery`: `if q.db.HasFeature(feature.InsertTableAlias) && !q.on.IsZero() {` becomes `if !q.on.IsZero() {`; `if q.hasFeature(feature.InsertReturning) && q.hasReturning() {` becomes `if q.hasReturning() {`.
- In `appendColumnsValues`: remove the `skipOutput bool` parameter; delete both `if q.hasFeature(feature.Output) && ... { b = append(b, " OUTPUT "...) ... }` blocks. Change the caller `q.appendColumnsValues(gen, b, false)` to `q.appendColumnsValues(gen, b)`.
- In `appendStructValues`, replace
  ```go
  case q.marshalsToDefault(f, strct):
  	if q.db.HasFeature(feature.DefaultPlaceholder) {
  		b = append(b, "DEFAULT"...)
  	} else if f.SQLDefault != "" {
  		b = append(b, f.SQLDefault...)
  	} else {
  		b = append(b, "NULL"...)
  	}
  	q.addReturningField(f)
  ```
  with
  ```go
  case q.marshalsToDefault(f, strct):
  	b = append(b, "DEFAULT"...)
  	q.addReturningField(f)
  ```
- Replace the whole `getFields` method with:
  ```go
  func (q *InsertQuery) getFields() ([]*schema.Field, error) {
  	return q.baseQuery.getFields()
  }
  ```
  The old body only ran when `DefaultPlaceholder` was false or `Identity` true; neither holds on Postgres. If `go vet` then reports `"reflect"` unused, remove that import.
- In `appendOn`, replace
  ```go
  if gen.HasFeature(feature.InsertOnDuplicateKey) {
  	b = append(b, ' ')
  } else {
  	b = append(b, " SET "...)
  }
  ```
  with `b = append(b, " SET "...)`.
- In `scanOrExec`: `useScan := hasDest || (q.hasReturning() && q.hasFeature(feature.InsertReturning|feature.Output))` becomes `useScan := hasDest || q.hasReturning()`.
- Remove `"github.com/piprim/pgcrud/dialect/feature"` from the imports.

- [ ] **Step 6: query_update.go**

- `SetColumn`: delete the `if q.db.HasFeature(feature.UpdateMultiTable) { column = q.table.Alias + "." + column }` block.
- `AppendQuery`: replace the three-way table append with `b, err = q.appendFirstTableWithAlias(gen, b)`; delete the `if !gen.HasFeature(feature.UpdateMultiTable) {` guard so `appendOtherTables` runs unconditionally; delete the `Output` block; `q.mustAppendWhere(gen, b, q.hasTableAlias(gen))` becomes `q.mustAppendWhere(gen, b, true)`; `if q.hasFeature(feature.Returning) && q.hasReturning() {` becomes `if q.hasReturning() {`.
- `updateSliceSet`: delete the `if gen.HasFeature(feature.UpdateMultiTable) { ... }` alias-prefix block.
- `scanOrExec`: `useScan := hasDest || q.hasReturning()`.
- `FQN`: replace the body after the panic with `return Ident(q.table.Alias + "." + column)` and delete `hasTableAlias`.
- Remove `"github.com/piprim/pgcrud/dialect/feature"` from the imports.

- [ ] **Step 7: query_delete.go**

- `Returning`: delete the `if !q.hasFeature(feature.DeleteReturning) { ... }` guard.
- `AppendQuery`: delete `withAlias := q.db.HasFeature(feature.DeleteTableAlias)`; replace the `if withAlias { ... } else { ... }` with `b, err = q.appendFirstTableWithAlias(gen, b)`; delete the `Output` block; `q.mustAppendWhere(gen, b, withAlias)` becomes `q.mustAppendWhere(gen, b, true)`; `if q.hasFeature(feature.DeleteReturning) && q.hasReturning() {` becomes `if q.hasReturning() {`.
- `softDeleteSet`: delete the `if gen.HasFeature(feature.UpdateMultiTable) { ... }` block.
- `scanOrExec`: `useScan := hasDest || q.hasReturning()`.
- Remove `"github.com/piprim/pgcrud/dialect/feature"` from the imports.

- [ ] **Step 8: query_values.go, model_map_slice.go, relation_join.go, db.go, schema**

`query_values.go`:
- Both `if q.db.HasFeature(feature.ValuesRow) { b = append(b, "ROW("...) } else { b = append(b, '(') }` become `b = append(b, '(')`.
- In `appendValues`, the `if gen.HasFeature(feature.DoubleColonCast) {` guard goes; its two lines run unconditionally.
- Remove the `feature` import.

`model_map_slice.go`: the same `ValuesRow` replacement; remove the `feature` import.

`relation_join.go`: `if q.db.HasFeature(feature.CompositeIn) { return j.manyQueryCompositeIn(where, q) } return j.manyQueryMulti(where, q)` becomes `return j.manyQueryCompositeIn(where, q)`. Delete `manyQueryMulti` (line 102 to its closing brace). Remove the `feature` import.

`db.go`:
- `UpdateFQN` becomes:
  ```go
  // UpdateFQN returns the column name to use in an UPDATE's SET clause.
  func (db *DB) UpdateFQN(alias, column string) Ident {
  	return Ident(column)
  }
  ```
- Delete `HasFeature` and the `feature` import.

`schema/table.go`: replace
```go
if t.dialect.Features().Has(feature.FKDefaultOnAction) {
	rel.OnUpdate = "ON UPDATE NO ACTION"
	rel.OnDelete = "ON DELETE NO ACTION"
}
```
with the two assignments unguarded; remove the `feature` import.

`schema/querygen.go`: delete `HasFeature`; remove the `feature` import.

- [ ] **Step 9: Verify nothing references feature flags or dialect names outside the dialect packages**

Run: `grep -rn "feature\.\|dialect\.\(PG\|MySQL\|MSSQL\|Oracle\|SQLite\|Invalid\)\b\|HasFeature\|hasFeature" --include='*.go' . | grep -v "^./dialect/\|^./schema/dialect.go\|^./schema/querygen"`
Expected: no output. (`schema/dialect.go` and `schema/querygen.go` still mention `feature` and `dialect.Invalid` in the interface and `IsNop`; Task 8 removes those.)

- [ ] **Step 10: Verify the whole module**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS with no snapshot change. If a golden case fails, the edit that caused it replaced a check with the wrong constant; re-read the true/false lists at the top of this task.

- [ ] **Step 11: Hand off for commit**

Run `detect_changes()`. Report files ready to commit: `query_base.go`, `query_select.go`, `query_insert.go`, `query_update.go`, `query_delete.go`, `query_values.go`, `model_map_slice.go`, `relation_join.go`, `db.go`, `schema/table.go`, `schema/querygen.go`. Suggested message: `refactor: resolve dialect feature checks to their Postgres constants`.

---

### Task 8: Delete the feature package and dialect names

**Files:**
- Modify: `schema/dialect.go` (interface, `nopDialect`)
- Modify: `schema/querygen.go` (`QueryGen` struct, `nopQueryGen`, `IsNop`, `WithArg`, `WithNamedArg`)
- Modify: `schema/querygen_linecomment_test.go`
- Modify: `dialect/pgdialect/dialect.go`
- Modify: `db.go` (`String`)
- Delete: `dialect/feature/feature.go` (whole directory), `dialect/dialect.go`

**Interfaces:**
- Produces: `schema.Dialect` without `Name()` and `Features()`; `QueryGen` with unexported `nop bool`; `pgdialect.Dialect` without `features`; no `dialect.Name` type.

- [ ] **Step 1: Run impact analysis**

Run `impact({target: "Name", direction: "upstream"})` for the `pgdialect.Dialect` receiver, `impact({target: "Features", direction: "upstream"})`, and `impact({target: "IsNop", direction: "upstream"})`. Expected: `Name` and `Features` are reached only from `DB.String`, `IsNop`, and `nopDialect`; `IsNop` has about a dozen callers that keep working.

- [ ] **Step 2: Write the failing test**

In `schema/querygen_linecomment_test.go`, delete the `lcTestDialect` type and its `Name` method, delete the `dialect` import, and change the generator construction to:

```go
gen := NewQueryGen(newNopDialect())
```

Also add to the same file:

```go
func TestNewQueryGenIsNotNop(t *testing.T) {
	t.Run("a generator built with NewQueryGen substitutes placeholders", func(t *testing.T) {
		gen := NewQueryGen(newNopDialect())
		require.False(t, gen.IsNop())
		require.Equal(t, "id = 1", gen.FormatQuery("id = ?", 1))
	})

	t.Run("the nop generator leaves the query untouched", func(t *testing.T) {
		gen := NewNopQueryGen()
		require.True(t, gen.IsNop())
		require.Equal(t, "id = ?", gen.FormatQuery("id = ?", 1))
	})
}
```

Add `"github.com/stretchr/testify/require"` to the imports.

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./schema/ -run 'TestQueryGen_Append_NegativeNumberDoesNotCreateLineComment|TestNewQueryGenIsNotNop' -v`
Expected: both FAIL. Today `NewQueryGen(newNopDialect())` reports `IsNop() == true` because the nop dialect's name is `Invalid`, so no substitution happens.

- [ ] **Step 4: Make nop an explicit flag on QueryGen**

`schema/querygen.go`:

```go
var nopQueryGen = QueryGen{
	dialect: newNopDialect(),
	nop:     true,
}

type QueryGen struct {
	dialect Dialect
	args    *namedArgList

	// nop marks the generator used to render query templates: placeholders
	// stay as "?" and arguments are not substituted.
	nop bool
}

func NewQueryGen(dialect Dialect) QueryGen {
	return QueryGen{
		dialect: dialect,
	}
}

func NewNopQueryGen() QueryGen {
	return nopQueryGen
}

func (f QueryGen) IsNop() bool {
	return f.nop
}
```

In `WithArg` and `WithNamedArg`, add `nop: f.nop,` to the returned literal. Remove the `dialect` import only if the build says it is unused (it is still used for `dialect.AppendNull` and friends, so it stays).

- [ ] **Step 5: Shrink the Dialect interface**

`schema/dialect.go`:
- Delete `Name() dialect.Name` and `Features() feature.Feature` from the interface.
- In `nopDialect`: delete the `features` field, the `d.features = feature.Returning` line in `newNopDialect`, and the `Name` and `Features` methods.
- Remove the `feature` import. The `dialect` import stays (`dialect.AppendError`, `dialect.AppendNull`, `dialect.AppendBool`).

`dialect/pgdialect/dialect.go`:
- Delete the `features feature.Feature` field, the whole `d.features = feature.CTE | ...` assignment in `New`, `WithoutFeature`, `Name`, and `Features`.
- Remove the `dialect` and `feature` imports.

`db.go`: `String` returns the literal:

```go
// String returns a string representation of the DB.
func (db *DB) String() string {
	return "DB<dialect=pg>"
}
```

- [ ] **Step 6: Delete the packages**

Run: `rm -r dialect/feature && rm dialect/dialect.go`

`dialect/append.go` is now the only file in package `dialect`; it needs no change.

- [ ] **Step 7: Verify the whole module**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS. `grep -rn "dialect/feature\|dialect\.Name\b" --include='*.go' .` finds nothing. `TestNew/String names the dialect` in `db_test.go` still passes with the constant.

- [ ] **Step 8: Hand off for commit**

Run `detect_changes()`. Report files ready to commit: `schema/dialect.go`, `schema/querygen.go`, `schema/querygen_linecomment_test.go`, `dialect/pgdialect/dialect.go`, `db.go`, deleted `dialect/feature/feature.go` and `dialect/dialect.go`. Suggested message: `refactor: delete the feature flags and dialect name enum`.

---

### Task 9: Remove DDL leftovers from the dialect and field metadata

**Files:**
- Modify: `schema/dialect.go` (interface, `nopDialect`)
- Modify: `schema/field.go:27`
- Modify: `schema/tables.go:55-62`
- Modify: `dialect/pgdialect/dialect.go` (`onField`, `AppendSequence`, `appendGeneratedAsIdentity`)
- Modify: `dialect/pgdialect/sqltype.go` (`DefaultVarcharLen`, serial constants)

**Interfaces:**
- Produces: `schema.Dialect` is `IdentQuote`, seven `Append*`, `Tables`, `OnTable`, `DefaultSchema`. `schema.Field` has no `CreateTableSQLType`.

- [ ] **Step 1: Run impact analysis**

Run `impact` upstream on `AppendSequence`, `DefaultVarcharLen`, and `CreateTableSQLType`. Expected: no callers outside the definitions and `Tables.Get`'s fill loop.

- [ ] **Step 2: Write the failing test**

Append to `schema/table_test.go` inside `TestTable`:

```go
t.Run("autoincrement fields keep their discovered type", func(t *testing.T) {
	type Model struct {
		ID int64 `bun:",pk,autoincrement"`
	}

	table := tables.Get(reflect.TypeFor[*Model]())
	id := table.FieldMap["id"]
	require.True(t, id.AutoIncrement)
	require.Equal(t, "BIGINT", id.DiscoveredSQLType)
	require.Equal(t, "BIGINT", id.UserSQLType)
})
```

Run: `go test ./schema/ -run 'TestTable/autoincrement' -v`
Expected: PASS today (`DiscoverSQLType` maps `int64` to `sqltype.BigInt`, which is `"BIGINT"`). This pins that removing the serial mapping in Step 3 does not disturb the surviving fields.

- [ ] **Step 3: Remove the members**

`schema/dialect.go`: delete `AppendSequence` and `DefaultVarcharLen` from the interface (and their comments), and from `nopDialect`. The `time` import stays for `AppendTime`.

`schema/field.go`: delete the `CreateTableSQLType string` field.

`schema/tables.go`, in `Get`, replace
```go
for _, field := range table.FieldMap {
	if field.UserSQLType == "" {
		field.UserSQLType = field.DiscoveredSQLType
	}
	if field.CreateTableSQLType == "" {
		field.CreateTableSQLType = field.UserSQLType
	}
}
```
with
```go
for _, field := range table.FieldMap {
	if field.UserSQLType == "" {
		field.UserSQLType = field.DiscoveredSQLType
	}
}
```

`dialect/pgdialect/dialect.go`:
- In `onField`, delete the `if field.AutoIncrement && !field.Identity { switch ... }` block, leaving `field.DiscoveredSQLType = fieldSQLType(field)` followed by the array check.
- Delete `AppendSequence` and `appendGeneratedAsIdentity`.

`dialect/pgdialect/sqltype.go`: delete `DefaultVarcharLen` and the `pgTypeSmallSerial`, `pgTypeSerial`, `pgTypeBigSerial` constants and their `// Serial Types` header. Run `go build ./...`; if any other constant in that block becomes unused the compiler does not complain (constants may be unused), so also run `grep -rn "pgTypeSmallSerial\|pgTypeSerial\|pgTypeBigSerial" --include='*.go' .` and expect nothing.

- [ ] **Step 4: Verify the whole module**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS, including the new subtest.

- [ ] **Step 5: Hand off for commit**

Run `detect_changes()`. Report files ready to commit: `schema/dialect.go`, `schema/field.go`, `schema/tables.go`, `schema/table_test.go`, `dialect/pgdialect/dialect.go`, `dialect/pgdialect/sqltype.go`. Suggested message: `refactor: drop DDL-only dialect methods and CreateTableSQLType`.

---

### Task 10: WithAppendUintAsInt DB option and two-phase New; README wording

**Files:**
- Modify: `db.go:20-108` (options, `New`)
- Modify: `README.md:4`
- Test: `db_test.go`

**Interfaces:**
- Produces: `pgcrud.WithAppendUintAsInt() DBOption`; `DBOption` is now `func(*config)`; `New` builds the dialect after applying options.
- Consumes: `pgdialect.WithAppendUintAsInt(on bool) DialectOption` (exists).

- [ ] **Step 1: Run impact analysis**

Run `impact({target: "New", direction: "upstream"})` and `impact({target: "DBOption", direction: "upstream"})`. Expected: every `With*` option and every test constructs through them; the option functions' bodies change but their names and call shapes do not.

- [ ] **Step 2: Write the failing tests**

Append to `db_test.go`:

```go
// initHook records the DB handed to Init so the test can prove the two-phase
// New still initializes hooks against the finished DB.
type initHook struct {
	recordingHook
	initialized *pgcrud.DB
}

func (h *initHook) Init(db *pgcrud.DB) { h.initialized = db }

func TestNewOptions(t *testing.T) {
	const selectUsers = `SELECT "user"."id", "user"."name" FROM "users" AS "user"`

	t.Run("default renders uint64 unsigned", func(t *testing.T) {
		db := pgcrud.New(nil)
		q := db.NewSelect().Model((*User)(nil)).Where("id = ?", uint64(math.MaxUint64))
		require.Equal(t, selectUsers+` WHERE (id = 18446744073709551615)`, q.String())
	})

	t.Run("WithAppendUintAsInt renders a large uint64 as a negative bigint", func(t *testing.T) {
		db := pgcrud.New(nil, pgcrud.WithAppendUintAsInt())
		q := db.NewSelect().Model((*User)(nil)).Where("id = ?", uint64(math.MaxUint64))
		require.Equal(t, selectUsers+` WHERE (id = -1)`, q.String())
	})

	t.Run("WithAppendUintAsInt renders uint32 through int32", func(t *testing.T) {
		db := pgcrud.New(nil, pgcrud.WithAppendUintAsInt())
		q := db.NewSelect().Model((*User)(nil)).Where("id = ?", uint32(math.MaxUint32))
		require.Equal(t, selectUsers+` WHERE (id = -1)`, q.String())
	})

	t.Run("hook Init receives the DB", func(t *testing.T) {
		hook := &initHook{}
		db := pgcrud.New(nil, pgcrud.WithQueryHook(hook))
		require.Same(t, db, hook.initialized)
	})

	t.Run("WithOptions applies nested options", func(t *testing.T) {
		exec := &fakeExecutor{}
		db := pgcrud.New(nil, pgcrud.WithOptions(
			pgcrud.WithExecutorResolver(func(context.Context) pgcrud.DBExecutor { return exec }),
			pgcrud.WithTxRequiredForWrites(),
		))
		_, err := db.NewDelete().Model((*User)(nil)).Where("id = 1").Exec(context.Background())
		require.ErrorIs(t, err, pgcrud.ErrTxRequired)
	})
}
```

Add `"math"` to the imports of `db_test.go`.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./ -run TestNewOptions -v`
Expected: build FAILS with `undefined: pgcrud.WithAppendUintAsInt`.

- [ ] **Step 4: Rebuild the options around a config struct**

In `db.go`, replace everything from `// DBOption mutates DB configuration during construction.` through the end of `New` with:

```go
// config collects DBOption values before the DB and its dialect are built.
type config struct {
	resolver   ExecutorResolver
	flags      internal.Flag
	uintAsInt  bool
	queryHooks []QueryHook
}

// DBOption configures a DB during construction.
type DBOption func(cfg *config)

// WithOptions applies multiple DBOption values at once.
func WithOptions(opts ...DBOption) DBOption {
	return func(cfg *config) {
		for _, opt := range opts {
			opt(cfg)
		}
	}
}

// WithDiscardUnknownColumns ignores columns returned by queries that are not present in models.
func WithDiscardUnknownColumns() DBOption {
	return func(cfg *config) {
		cfg.flags = cfg.flags.Set(discardUnknownColumns)
	}
}

// WithExecutorResolver sets the function that picks the executor for each query.
// Pass the Executor method of the application's unit of work.
//
// fn must return a nil interface, not a typed nil (for example a nil pgx.Tx
// stored in a non-nil DBExecutor), because only a nil interface is detected
// and reported as ErrNilExecutor.
func WithExecutorResolver(fn ExecutorResolver) DBOption {
	return func(cfg *config) {
		if fn != nil {
			cfg.resolver = fn
		}
	}
}

// WithTxRequiredForWrites makes Insert, Update, Delete, DB.Exec and non-SELECT
// Raw queries fail with ErrTxRequired unless the resolved executor is a pgx.Tx.
func WithTxRequiredForWrites() DBOption {
	return func(cfg *config) {
		cfg.flags = cfg.flags.Set(txRequiredForWrites)
	}
}

// WithQueryHook registers a query hook at construction time.
func WithQueryHook(hook QueryHook) DBOption {
	return func(cfg *config) {
		cfg.queryHooks = append(cfg.queryHooks, hook)
	}
}

// WithAppendUintAsInt renders uint32 and uint64 values as signed integers of
// the same width, so values above the signed maximum wrap to negative
// literals. Postgres has no unsigned integer types; this keeps such values
// storable in integer and bigint columns.
func WithAppendUintAsInt() DBOption {
	return func(cfg *config) {
		cfg.uintAsInt = true
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
//
// Options are collected first and the dialect is built from them, so options
// that shape value rendering such as WithAppendUintAsInt take effect.
func New(pool *pgxpool.Pool, opts ...DBOption) *DB {
	cfg := config{resolver: poolResolver(pool)}
	for _, opt := range opts {
		opt(&cfg)
	}

	dialect := pgdialect.New(pgdialect.WithAppendUintAsInt(cfg.uintAsInt))

	db := &DB{
		noCopyState: &noCopyState{
			pool:     pool,
			resolver: cfg.resolver,
			dialect:  dialect,
			flags:    cfg.flags,
		},
		gen: schema.NewQueryGen(dialect),
	}

	for _, hook := range cfg.queryHooks {
		if initer, ok := hook.(queryHookIniter); ok {
			initer.Init(db)
		}
		db.queryHooks = append(db.queryHooks, hook)
	}

	return db
}
```

The `queryHookIniter` interface and `DB.WithQueryHook` method further down stay as they are.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./ -run 'TestNewOptions|TestQueryHooks|TestTxRequiredForWrites' -v`
Expected: PASS.

- [ ] **Step 6: README wording**

In `README.md` line 4, change `` `pgxpool.Pool` and `pgx.Tx` instead of `database/sql`. `` to `` `pgxpool.Pool` and `pgx.Tx` instead of a `database/sql` driver. ``

Run: `sed -n 3,4p README.md`
Expected: the two lines read "A Postgres-only CRUD ORM-like derived from bun that runs on `pgxpool.Pool` and `pgx.Tx` instead of a `database/sql` driver."

- [ ] **Step 7: Verify the whole module**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS. `grep -rn "WithoutFeature" --include='*.go' .` finds nothing (deleted in Task 8), and `go run golang.org/x/tools/cmd/deadcode@latest ./...` no longer lists `WithAppendUintAsInt`.

- [ ] **Step 8: Hand off for commit**

Run `detect_changes()`. Report files ready to commit: `db.go`, `db_test.go`, `README.md`. Suggested message: `feat: expose WithAppendUintAsInt as a DB option`.

---

## Stage 4: Tables ownership and cache signatures

### Task 11: Drop the dialect parameter from the appender and scanner caches

**Files:**
- Modify: `schema/append_value.go:56-96, 160-165` (`FieldAppender`, `Appender`, `appender`, `ifaceAppenderFunc`)
- Modify: `schema/scan.go:59-73` (`FieldScanner`)
- Modify: `schema/querygen.go:93, 110` (two `Appender` calls)
- Modify: `schema/table.go:593-594`
- Modify: `dialect/pgdialect/array.go:30-42, 75-159` (`Array`, `arrayAppender`, `arrayElemAppender`)
- Modify: `dialect/pgdialect/append.go:38-60` (`hstoreAppender`)
- Modify: `dialect/pgdialect/hstore.go:24-44` (`HStore`)
- Modify: `dialect/pgdialect/dialect.go` (delete `pgDialect`, `onField` calls)
- Modify: `dialect/pgdialect/append_test.go:36-40`

**Interfaces:**
- Produces: `schema.Appender(typ reflect.Type) AppenderFunc`, `schema.FieldAppender(field *Field) AppenderFunc`, `schema.FieldScanner(field *Field) ScannerFunc`; package-level `pgdialect.arrayAppender`, `arrayElemAppender`, `hstoreAppender`. No `pgDialect` singleton.

- [ ] **Step 1: Run impact analysis**

Run `impact` upstream on `Appender`, `FieldAppender`, `FieldScanner`, `arrayAppender`, `hstoreAppender`, and `pgDialect`. Report the callers; they should match the file list above.

- [ ] **Step 2: Write the failing test**

Append to `dialect/pgdialect/array_test.go`:

```go
func TestArrayHelpersNeedNoDialectInstance(t *testing.T) {
	t.Run("Array appends through the generator's dialect", func(t *testing.T) {
		out, err := Array([]int64{1, 2}).AppendQuery(schema.NewQueryGen(New()), nil)
		require.NoError(t, err)
		require.Equal(t, `'{1,2}'`, string(out))
	})

	t.Run("nested slices resolve their element appender without a dialect", func(t *testing.T) {
		fn := arrayAppender(reflect.TypeFor[[][]int64]())
		require.NotNil(t, fn)
	})
}
```

Add `"reflect"` and `"github.com/stretchr/testify/require"` to that file's imports.

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./dialect/pgdialect/ -run TestArrayHelpersNeedNoDialectInstance`
Expected: build FAILS: `arrayAppender` is a method on `*Dialect`, so `arrayAppender(...)` is undefined.

- [ ] **Step 4: Change the schema signatures**

`schema/append_value.go`:
- `func FieldAppender(dialect Dialect, field *Field) AppenderFunc` becomes `func FieldAppender(field *Field) AppenderFunc`; its final line becomes `return Appender(fieldType)`.
- `func Appender(dialect Dialect, typ reflect.Type) AppenderFunc` becomes `func Appender(typ reflect.Type) AppenderFunc`; the call inside becomes `fn := appender(typ)`.
- `func appender(dialect Dialect, typ reflect.Type) AppenderFunc` becomes `func appender(typ reflect.Type) AppenderFunc`; the recursive call becomes `Appender(typ.Elem())`.
- In `ifaceAppenderFunc`: `appender := Appender(elem.Type())`.

`schema/scan.go`: `func FieldScanner(dialect Dialect, field *Field) ScannerFunc` becomes `func FieldScanner(field *Field) ScannerFunc`.

`schema/querygen.go`: `Appender(gen.Dialect(), vv.Type())` becomes `Appender(vv.Type())`; `Appender(f.dialect, v.Type())` becomes `Appender(v.Type())`.

`schema/table.go`: `field.Append = FieldAppender(field)` and `field.Scan = FieldScanner(field)`.

- [ ] **Step 5: Turn the pgdialect appenders into functions and delete the singleton**

`dialect/pgdialect/array.go`:
- `func (d *Dialect) arrayAppender(typ reflect.Type)` becomes `func arrayAppender(typ reflect.Type)`; inside, `d.arrayAppender(typ.Elem())` becomes `arrayAppender(typ.Elem())` and `d.arrayElemAppender(elemType)` becomes `arrayElemAppender(elemType)`.
- `func (d *Dialect) arrayElemAppender(typ reflect.Type)` becomes `func arrayElemAppender(typ reflect.Type)`; inside, `schema.PtrAppender(d.arrayElemAppender(typ.Elem()))` becomes `schema.PtrAppender(arrayElemAppender(typ.Elem()))` and `return schema.Appender(d, typ)` becomes `return schema.Appender(typ)`.
- In `Array`: `append: pgDialect.arrayAppender(v.Type()),` becomes `append: arrayAppender(v.Type()),`.

`dialect/pgdialect/append.go`: `func (d *Dialect) hstoreAppender(typ reflect.Type)` becomes `func hstoreAppender(typ reflect.Type)`; the recursive call drops `d.`.

`dialect/pgdialect/hstore.go`: `append: pgDialect.hstoreAppender(v.Type()),` becomes `append: hstoreAppender(v.Type()),`.

`dialect/pgdialect/dialect.go`: delete `var pgDialect = New()`; in `onField`, `d.arrayAppender(...)` becomes `arrayAppender(...)` and `d.hstoreAppender(...)` becomes `hstoreAppender(...)`.

`dialect/pgdialect/append_test.go`: `appendFunc := pgDialect.hstoreAppender(...)` becomes `appendFunc := hstoreAppender(...)`, and `schema.NewQueryGen(pgDialect)` becomes `schema.NewQueryGen(New())`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./schema/ ./dialect/pgdialect/`
Expected: PASS.

- [ ] **Step 7: Verify the whole module**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS. `grep -rn "pgDialect\b" --include='*.go' .` finds nothing.

- [ ] **Step 8: Hand off for commit**

Run `detect_changes()`. Report files ready to commit: `schema/append_value.go`, `schema/scan.go`, `schema/querygen.go`, `schema/table.go`, `dialect/pgdialect/array.go`, `dialect/pgdialect/append.go`, `dialect/pgdialect/hstore.go`, `dialect/pgdialect/dialect.go`, `dialect/pgdialect/append_test.go`, `dialect/pgdialect/array_test.go`. Suggested message: `refactor: drop the ignored dialect argument from appender and scanner lookups`.

---

### Task 12: DB owns the table registry

**Files:**
- Modify: `schema/tables.go:75` (`InProgress`)
- Modify: `schema/table.go` (struct line 41; `init` 87-102; every `t.dialect` use at 169, 214, 251, 256, 524, 646, 651, 770, 840, 934, 948; `Dialect()` at 1014; `quoteIdent` at 1043)
- Modify: `schema/dialect.go` (interface, `nopDialect`)
- Modify: `schema/querygen.go` (struct, `NewQueryGen`, `nopQueryGen`, `WithArg`, `WithNamedArg`, `structArgs` at 296)
- Modify: `schema/table_test.go:367-370, 410-414`
- Modify: `schema/querygen_linecomment_test.go`
- Modify: `dialect/pgdialect/dialect.go` (struct, `New`, `Tables`)
- Modify: `dialect/pgdialect/array_test.go`, `append_test.go` (`NewQueryGen` calls)
- Modify: `db.go` (`noCopyState`, `New`, `Table`, `RegisterModel`)
- Modify: `model.go:174, 188`
- Test: `db_test.go`

**Interfaces:**
- Produces: `schema.NewQueryGen(dialect Dialect, tables *Tables) QueryGen` (a nil `tables` gets a fresh registry); `schema.Table.init(tables *Tables, typ reflect.Type)`; `DB.tables`. `schema.Dialect` loses `Tables()`; `Table.Dialect()` is gone.

- [ ] **Step 1: Run impact analysis**

Run `impact` upstream on `Tables` (the `pgdialect.Dialect` and `nopDialect` receivers), `Dialect` (the `Table` receiver), `NewQueryGen`, and `init` (the `Table` receiver). Report the blast radius. This is the widest task; expect MEDIUM risk. All callers are inside this module and listed above.

- [ ] **Step 2: Write the failing tests**

Append to `db_test.go`:

```go
func TestTablesOwnership(t *testing.T) {
	typ := reflect.TypeFor[User]()

	t.Run("each DB owns its own table registry", func(t *testing.T) {
		a := pgcrud.New(nil)
		b := pgcrud.New(nil)
		require.NotSame(t, a.Table(typ), b.Table(typ))
	})

	t.Run("one DB returns the same table for the same type", func(t *testing.T) {
		db := pgcrud.New(nil)
		require.Same(t, db.Table(typ), db.Table(typ))
	})

	t.Run("RegisterModel makes the table reachable by name for m2m lookups", func(t *testing.T) {
		db := pgcrud.New(nil)
		db.RegisterModel((*User)(nil))
		require.Equal(t, "users", db.Table(typ).Name)
	})
}
```

Add `"reflect"` to the imports of `db_test.go`.

In `schema/table_test.go`, update the two subtests that call `dialect.Tables()` (around lines 367 and 410) to:

```go
tables := NewTables(newNopDialect())
tables.Register((*OrderToItem)(nil))

outer := tables.Get(reflect.TypeOf((*OrderWrap)(nil)).Elem())
```

and

```go
tables := NewTables(newNopDialect())
tables.Register((*OrderToItem)(nil))

require.PanicsWithError(t, "pgcrud: OrderToItem belongs-to Order: OrderWrap must have column id", func() {
	tables.Get(reflect.TypeOf((*OrderWrap)(nil)).Elem())
})
```

- [ ] **Step 3: Run the tests to verify the state**

Run: `go test ./ -run TestTablesOwnership -v && go test ./schema/ -run TestTable -v`
Expected: `TestTablesOwnership` PASSES already (each `pgdialect.New()` has its own registry today; the test pins the guarantee across the move). The `schema` tests PASS. The failing signal for this task is the build after Step 4, which must be brought back to green by Steps 5-7.

- [ ] **Step 4: Move ownership in schema**

`schema/dialect.go`: delete `Tables() *Tables` from the interface. In `nopDialect`, delete the `tables` field, the `Tables()` method, and the `d.tables = NewTables(d)` line, so `newNopDialect` is `return new(nopDialect)`.

`schema/tables.go`, in `InProgress`: `table.init(t.dialect, typ)` becomes `table.init(t, typ)`.

`schema/table.go`:
- Replace the field `dialect Dialect` with `tables *Tables // registry that owns this table; its dialect formats identifiers and discovers field types`.
- `func (table *Table) init(dialect Dialect, typ reflect.Type)` becomes `func (table *Table) init(tables *Tables, typ reflect.Type)`; `table.dialect = dialect` becomes `table.tables = tables`; `table.Schema = dialect.DefaultSchema()` becomes `table.Schema = tables.dialect.DefaultSchema()`.
- Every `t.dialect.Tables().X(...)` becomes `t.tables.X(...)` (lines 169, 214, 251, 256, 646, 651, 770, 840, 934, 948).
- `schema, table := t.dialect.DefaultSchema(), name` becomes `schema, table := t.tables.dialect.DefaultSchema(), name`.
- Delete `func (t *Table) Dialect() Dialect { return t.dialect }`.
- `quoteIdent` becomes `return Safe(NewQueryGen(t.tables.dialect, t.tables).AppendIdent(nil, s))`.

`schema/querygen.go`:

```go
var nopQueryGen = newNopQueryGen()

func newNopQueryGen() QueryGen {
	d := newNopDialect()
	return QueryGen{dialect: d, tables: NewTables(d), nop: true}
}

type QueryGen struct {
	dialect Dialect
	tables  *Tables
	args    *namedArgList

	// nop marks the generator used to render query templates: placeholders
	// stay as "?" and arguments are not substituted.
	nop bool
}

// NewQueryGen returns a generator that formats values through dialect and
// resolves struct named arguments through tables. A nil tables gets a fresh
// registry, which is enough for callers that never format struct arguments.
func NewQueryGen(dialect Dialect, tables *Tables) QueryGen {
	if tables == nil {
		tables = NewTables(dialect)
	}
	return QueryGen{
		dialect: dialect,
		tables:  tables,
	}
}
```

Add `tables: f.tables,` to the literals returned by `WithArg` and `WithNamedArg`. In the struct named-args constructor, `table: gen.Dialect().Tables().Get(v.Type()),` becomes `table: gen.tables.Get(v.Type()),`.

`schema/querygen_linecomment_test.go`: both `NewQueryGen(newNopDialect())` calls become `NewQueryGen(newNopDialect(), nil)`.

- [ ] **Step 5: pgdialect**

`dialect/pgdialect/dialect.go`: delete the `tables *schema.Tables` field, the `d.tables = schema.NewTables(d)` line in `New`, and the `Tables()` method.

`dialect/pgdialect/array_test.go` and `append_test.go`: every `schema.NewQueryGen(New())` becomes `schema.NewQueryGen(New(), nil)`.

- [ ] **Step 6: DB and models**

`db.go`:
- Add `tables *schema.Tables` to `noCopyState` after `dialect`.
- In `New`, after building the dialect:
  ```go
  tables := schema.NewTables(dialect)

  db := &DB{
  	noCopyState: &noCopyState{
  		pool:     pool,
  		resolver: cfg.resolver,
  		dialect:  dialect,
  		tables:   tables,
  		flags:    cfg.flags,
  	},
  	gen: schema.NewQueryGen(dialect, tables),
  }
  ```
- `Table`: `return db.tables.Get(typ)`.
- `RegisterModel`: `db.tables.Register(models...)`.

`model.go`, in `newTableModelIndex`: `table: table.Dialect().Tables().Get(typ),` becomes `table: db.Table(typ),` and `table: table.Dialect().Tables().Get(structType),` becomes `table: db.Table(structType),`.

- [ ] **Step 7: Verify the whole module**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS. `grep -rn "\.Tables()" --include='*.go' .` finds nothing. `grep -rn "Dialect()" --include='*.go' . | grep -v "gen.Dialect()\|db.Dialect()\|q.db.Dialect()\|func (q \*baseQuery) Dialect\|func (db \*DB) Dialect\|func (f QueryGen) Dialect"` finds nothing.

- [ ] **Step 8: Hand off for commit**

Run `detect_changes()`. Report files ready to commit: `schema/dialect.go`, `schema/tables.go`, `schema/table.go`, `schema/querygen.go`, `schema/table_test.go`, `schema/querygen_linecomment_test.go`, `dialect/pgdialect/dialect.go`, `dialect/pgdialect/array_test.go`, `dialect/pgdialect/append_test.go`, `db.go`, `model.go`, `db_test.go`. Suggested message: `refactor: move the table registry from the dialect to DB`.

---

## Stage 5: round-trip tests for pgdialect value types

### Task 13: Range and MultiRange tests

**Files:**
- Create: `dialect/pgdialect/range_test.go`

**Interfaces:**
- Consumes: `Range[T]`, `MultiRange[T]`, `NewRange`, `NewEmptyRange`, the `RangeBound*` constants, `schema.NewQueryGen(New(), nil)`.

- [ ] **Step 1: Write the tests**

Create `dialect/pgdialect/range_test.go`:

```go
package pgdialect

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/piprim/pgcrud/schema"
)

func TestRangeScan(t *testing.T) {
	t.Run("sql.Scanner element type", func(t *testing.T) {
		tests := []struct {
			name string
			src  any
			want Range[sql.NullInt64]
		}{
			{
				name: "inclusive lower exclusive upper from string",
				src:  "[1,5)",
				want: Range[sql.NullInt64]{
					Lower: sql.NullInt64{Int64: 1, Valid: true}, Upper: sql.NullInt64{Int64: 5, Valid: true},
					LowerBound: RangeBoundInclusiveLeft, UpperBound: RangeBoundExclusiveRight,
				},
			},
			{
				name: "exclusive lower inclusive upper from bytes",
				src:  []byte("(1,5]"),
				want: Range[sql.NullInt64]{
					Lower: sql.NullInt64{Int64: 1, Valid: true}, Upper: sql.NullInt64{Int64: 5, Valid: true},
					LowerBound: RangeBoundExclusiveLeft, UpperBound: RangeBoundInclusiveRight,
				},
			},
			{
				name: "missing lower bound is unset",
				src:  "(,5)",
				want: Range[sql.NullInt64]{
					Upper:      sql.NullInt64{Int64: 5, Valid: true},
					LowerBound: RangeBoundUnset, UpperBound: RangeBoundExclusiveRight,
				},
			},
			{
				name: "missing upper bound is unset",
				src:  "[1,)",
				want: Range[sql.NullInt64]{
					Lower:      sql.NullInt64{Int64: 1, Valid: true},
					LowerBound: RangeBoundInclusiveLeft, UpperBound: RangeBoundUnset,
				},
			},
			{
				name: "surrounding whitespace is ignored",
				src:  "  [1,5)\n",
				want: Range[sql.NullInt64]{
					Lower: sql.NullInt64{Int64: 1, Valid: true}, Upper: sql.NullInt64{Int64: 5, Valid: true},
					LowerBound: RangeBoundInclusiveLeft, UpperBound: RangeBoundExclusiveRight,
				},
			},
			{
				name: "empty literal",
				src:  "empty",
				want: Range[sql.NullInt64]{LowerBound: RangeBoundEmpty, UpperBound: RangeBoundEmpty},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var r Range[sql.NullInt64]
				require.NoError(t, r.Scan(tt.src))
				require.Equal(t, tt.want, r)
			})
		}
	})

	t.Run("time element type", func(t *testing.T) {
		var r Range[time.Time]
		require.NoError(t, r.Scan(`["2024-01-01 00:00:00+00","2024-12-31 00:00:00+00")`))
		require.Equal(t, RangeBoundInclusiveLeft, r.LowerBound)
		require.Equal(t, RangeBoundExclusiveRight, r.UpperBound)
		require.True(t, r.Lower.Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)), "lower %v", r.Lower)
		require.True(t, r.Upper.Equal(time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)), "upper %v", r.Upper)
	})

	t.Run("nil leaves the range zero", func(t *testing.T) {
		var r Range[sql.NullInt64]
		require.NoError(t, r.Scan(nil))
		require.True(t, r.IsZero())
	})

	t.Run("empty input leaves the range zero", func(t *testing.T) {
		var r Range[sql.NullInt64]
		require.NoError(t, r.Scan(""))
		require.True(t, r.IsZero())
	})

	t.Run("unsupported source type is an error", func(t *testing.T) {
		var r Range[sql.NullInt64]
		require.EqualError(t, r.Scan(42), "pgdialect: Range can't scan int")
	})

	t.Run("bad lower bound is an error", func(t *testing.T) {
		var r Range[sql.NullInt64]
		require.EqualError(t, r.Scan("x1,5)"), "unexpected lower bound: x")
	})

	t.Run("bad upper bound is an error", func(t *testing.T) {
		var r Range[sql.NullInt64]
		require.EqualError(t, r.Scan("[1,5x"), "unexpected upper bound: x")
	})

	t.Run("missing comma is an error", func(t *testing.T) {
		var r Range[sql.NullInt64]
		require.EqualError(t, r.Scan("[15]"), "invalid range: wanted comma, got 15")
	})

	t.Run("int64 element type is not scannable", func(t *testing.T) {
		// scanElem only handles *time.Time and sql.Scanner. This pins the
		// limitation so a future fix changes it deliberately.
		var r Range[int64]
		require.PanicsWithError(t, "unsupported range type: *int64", func() {
			_ = r.Scan("[1,5)")
		})
	})
}

func TestRangeAppendQuery(t *testing.T) {
	gen := schema.NewQueryGen(New(), nil)

	t.Run("int64 element type", func(t *testing.T) {
		tests := []struct {
			name string
			r    Range[int64]
			want string
		}{
			{name: "NewRange is inclusive-exclusive", r: NewRange[int64](1, 5), want: `'[1,5)'`},
			{name: "empty range", r: NewEmptyRange[int64](), want: `'empty'`},
			{
				name: "unset lower bound renders as exclusive",
				r:    Range[int64]{Upper: 5, UpperBound: RangeBoundExclusiveRight},
				want: `'(,5)'`,
			},
			{
				name: "unset upper bound renders as exclusive",
				r:    Range[int64]{Lower: 1, LowerBound: RangeBoundInclusiveLeft},
				want: `'[1,)'`,
			},
			{
				name: "inclusive both sides",
				r:    Range[int64]{Lower: 1, Upper: 5, LowerBound: RangeBoundInclusiveLeft, UpperBound: RangeBoundInclusiveRight},
				want: `'[1,5]'`,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got, err := tt.r.AppendQuery(gen, nil)
				require.NoError(t, err)
				require.Equal(t, tt.want, string(got))
			})
		}
	})

	t.Run("time element type quotes each bound", func(t *testing.T) {
		r := NewRange(
			time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
		)
		got, err := r.AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, `'["2024-01-01 00:00:00+00:00","2024-12-31 00:00:00+00:00")'`, string(got))
	})

	t.Run("sql.Scanner element type appends its driver value", func(t *testing.T) {
		r := NewRange(sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Int64: 5, Valid: true})
		got, err := r.AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, `'[1,5)'`, string(got))
	})

	t.Run("scan then append reproduces the literal", func(t *testing.T) {
		var r Range[sql.NullInt64]
		require.NoError(t, r.Scan("[1,5)"))
		got, err := r.AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, `'[1,5)'`, string(got))
	})
}

func TestMultiRangeAppendQuery(t *testing.T) {
	gen := schema.NewQueryGen(New(), nil)

	tests := []struct {
		name string
		m    MultiRange[int64]
		want string
	}{
		{name: "nil", m: nil, want: `'{}'`},
		{name: "empty", m: MultiRange[int64]{}, want: `'{}'`},
		{name: "one range", m: MultiRange[int64]{NewRange[int64](1, 5)}, want: `'{[1,5)}'`},
		{
			name: "two ranges",
			m: MultiRange[int64]{
				NewRange[int64](1, 5),
				{Lower: 10, Upper: 20, LowerBound: RangeBoundInclusiveLeft, UpperBound: RangeBoundInclusiveRight},
			},
			want: `'{[1,5),[10,20]}'`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.m.AppendQuery(gen, nil)
			require.NoError(t, err)
			require.Equal(t, tt.want, string(got))
		})
	}

	t.Run("Len and IsZero", func(t *testing.T) {
		var m MultiRange[int64]
		require.Equal(t, 0, m.Len())
		require.True(t, m.IsZero())
		m = MultiRange[int64]{NewRange[int64](1, 2)}
		require.Equal(t, 1, m.Len())
		require.False(t, m.IsZero())
	})
}
```

- [ ] **Step 2: Run the tests**

Run: `go test ./dialect/pgdialect/ -run 'TestRange|TestMultiRange' -v`
Expected: PASS. If a subtest fails only on the exact text of an error or a time literal, print the actual value, confirm it against the source in `range.go` and `elem.go` (`appendTime` uses the layout `2006-01-02 15:04:05.999999-07:00`), and correct the expectation. If a subtest fails on structure (a bound or a value), that is a real bug in the port: stop and report it to the user rather than adjusting the test.

- [ ] **Step 3: Verify the whole module**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 4: Hand off for commit**

Report the file ready to commit: `dialect/pgdialect/range_test.go`. Suggested message: `test: round-trip Range and MultiRange through Scan and AppendQuery`.

---

### Task 14: HStoreValue and ArrayValue tests

**Files:**
- Create: `dialect/pgdialect/value_test.go`

**Interfaces:**
- Consumes: `HStore`, `Array`, `HStoreValue`, `ArrayValue`, `schema.NewQueryGen(New(), nil)`.

- [ ] **Step 1: Write the tests**

Create `dialect/pgdialect/value_test.go`:

```go
package pgdialect

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/piprim/pgcrud/schema"
)

func TestHStoreValue(t *testing.T) {
	gen := schema.NewQueryGen(New(), nil)

	t.Run("Scan from string", func(t *testing.T) {
		var m map[string]string
		h := HStore(&m)
		require.NoError(t, h.Scan(`"a"=>"1", "b"=>"2"`))
		require.Equal(t, map[string]string{"a": "1", "b": "2"}, m)
	})

	t.Run("Scan from bytes", func(t *testing.T) {
		var m map[string]string
		require.NoError(t, HStore(&m).Scan([]byte(`"k"=>"v"`)))
		require.Equal(t, map[string]string{"k": "v"}, m)
	})

	t.Run("Scan nil yields a nil map", func(t *testing.T) {
		m := map[string]string{"stale": "x"}
		require.NoError(t, HStore(&m).Scan(nil))
		require.Nil(t, m)
	})

	t.Run("Scan into an empty hstore yields an empty map", func(t *testing.T) {
		var m map[string]string
		require.NoError(t, HStore(&m).Scan(""))
		require.NotNil(t, m)
		require.Empty(t, m)
	})

	t.Run("Scan requires a pointer", func(t *testing.T) {
		m := map[string]string{}
		require.EqualError(t, HStore(m).Scan(`"a"=>"1"`), "pgcrud: HStore(non-pointer map[string]string)")
	})

	t.Run("Scan into an unsupported map type is an error", func(t *testing.T) {
		var m map[string]int
		require.EqualError(t, HStore(&m).Scan(`"a"=>"1"`), "pgcrud: Hstore(unsupported map[string]int)")
	})

	t.Run("Value returns the wrapped pointer", func(t *testing.T) {
		var m map[string]string
		h := HStore(&m)
		require.Same(t, &m, h.Value())
	})

	t.Run("Value returns the wrapped map", func(t *testing.T) {
		m := map[string]string{"a": "1"}
		require.Equal(t, m, HStore(m).Value())
	})

	t.Run("scan then append reproduces the literal", func(t *testing.T) {
		var m map[string]string
		require.NoError(t, HStore(&m).Scan(`"a"=>"1"`))
		got, err := HStore(m).AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, `'"a"=>"1"'`, string(got))
	})

	t.Run("HStore panics on nil and on a non-map", func(t *testing.T) {
		require.PanicsWithError(t, "pgcrud: HStore(nil)", func() { HStore(nil) })
		require.PanicsWithError(t, "pgcrud: Hstore(unsupported int)", func() { HStore(1) })
	})
}

func TestArrayValue(t *testing.T) {
	gen := schema.NewQueryGen(New(), nil)

	t.Run("Scan int64 slice from string", func(t *testing.T) {
		var xs []int64
		require.NoError(t, Array(&xs).Scan("{1,2,3}"))
		require.Equal(t, []int64{1, 2, 3}, xs)
	})

	t.Run("Scan string slice from bytes", func(t *testing.T) {
		var xs []string
		require.NoError(t, Array(&xs).Scan([]byte(`{"foo","bar"}`)))
		require.Equal(t, []string{"foo", "bar"}, xs)
	})

	t.Run("Scan float64 slice", func(t *testing.T) {
		var xs []float64
		require.NoError(t, Array(&xs).Scan("{1.5,2}"))
		require.Equal(t, []float64{1.5, 2}, xs)
	})

	t.Run("Scan time slice through the generic element scanner", func(t *testing.T) {
		var xs []time.Time
		require.NoError(t, Array(&xs).Scan(`{"2024-01-01 00:00:00+00"}`))
		require.Len(t, xs, 1)
		require.True(t, xs[0].Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)), "got %v", xs[0])
	})

	t.Run("Scan nil yields a nil slice", func(t *testing.T) {
		xs := []int64{9}
		require.NoError(t, Array(&xs).Scan(nil))
		require.Nil(t, xs)
	})

	t.Run("Scan an empty array yields an empty slice", func(t *testing.T) {
		var xs []int64
		require.NoError(t, Array(&xs).Scan("{}"))
		require.NotNil(t, xs)
		require.Empty(t, xs)
	})

	t.Run("Scan requires a pointer", func(t *testing.T) {
		xs := []int64{}
		require.EqualError(t, Array(xs).Scan("{1}"), "pgcrud: Array(non-pointer []int64)")
	})

	t.Run("Scan rejects an unsupported source type", func(t *testing.T) {
		var xs []int64
		require.EqualError(t, Array(&xs).Scan(42), "pgdialect: got int, wanted []byte or string")
	})

	t.Run("Value returns the wrapped pointer", func(t *testing.T) {
		var xs []int64
		require.Same(t, &xs, Array(&xs).Value())
	})

	t.Run("Value returns the wrapped slice", func(t *testing.T) {
		xs := []int64{1}
		require.Equal(t, xs, Array(xs).Value())
	})

	t.Run("scan then append reproduces the literal", func(t *testing.T) {
		var xs []int64
		require.NoError(t, Array(&xs).Scan("{1,2,3}"))
		got, err := Array(xs).AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, `'{1,2,3}'`, string(got))
	})

	t.Run("Array panics on nil", func(t *testing.T) {
		require.PanicsWithError(t, "pgcrud: Array(nil)", func() { Array(nil) })
	})
}
```

- [ ] **Step 2: Run the tests**

Run: `go test ./dialect/pgdialect/ -run 'TestHStoreValue|TestArrayValue' -v`
Expected: PASS. Apply the same rule as Task 13: correct an expectation only for exact wording after reading the source (`array.go`, `hstore.go`, `hstore_scan.go`); a structural mismatch is a bug to report, not to paper over.

- [ ] **Step 3: Verify the whole module**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 4: Hand off for commit**

Report the file ready to commit: `dialect/pgdialect/value_test.go`. Suggested message: `test: round-trip HStoreValue and ArrayValue through Scan, Value and AppendQuery`.

---

### Task 15: Final verification and spec note

**Files:**
- Modify: `docs/superpowers/specs/2026-09-22-pgcrud-design.md` (section 3 header, section 8 second bullet)

- [ ] **Step 1: Integration suite**

Run:

```sh
docker compose up -d --wait
PGCRUD_TEST_DSN='postgres://postgres:postgres@localhost:5442/pgcrud?sslmode=disable' go test ./... 2>&1 | tail -20
```

Expected: every package PASS, including `integration_test.go` (its `ScanAndCount` subtests cover both the pool and the transaction path) and `uow_test.go`. If Docker is unavailable, say so in the report; do not skip silently.

- [ ] **Step 2: Dead code**

Run: `timeout 240 go run golang.org/x/tools/cmd/deadcode@latest -test ./... 2>&1`
Expected: no line mentions `WithoutFeature`, `WithAppendUintAsInt`, `Range.Scan`, `Range.AppendQuery`, `MultiRange.AppendQuery`, `HStoreValue.Scan`, `HStoreValue.Value`, `ArrayValue.Scan`, `ArrayValue.Value`, `appendSelectFromValues`, `manyQueryMulti`, `appendOutput`, `hasTableAlias`, `appendGeneratedAsIdentity`, `isPool`, or `queryOperation`. Paste the full output into the report; exported API that is deliberately unused by tests is acceptable and is listed as such.

- [ ] **Step 3: Grep gates**

Run each and expect the stated output:

```sh
grep -rn "feature\.\|dialect/feature\|dialect\.\(PG\|MySQL\|MSSQL\|Oracle\|SQLite\|Invalid\)\b" --include='*.go' .
```
Expected: nothing.

```sh
grep -rn "QueryExecMode" --include='*.go' . | grep -v _test.go
```
Expected: one line, in `executor.go`.

```sh
grep -rn "pgDialect\b\|\.Tables()\|isReadStatement\|queryOperation\|CreateTableSQLType\|AppendSequence\|DefaultVarcharLen" --include='*.go' .
```
Expected: nothing.

- [ ] **Step 4: Note in the original spec**

In `docs/superpowers/specs/2026-09-22-pgcrud-design.md`, insert directly under the `## 3. Package layout` heading:

```markdown
> Superseded on 2026-09-24: `dialect/dialect.go` and `dialect/feature/` were removed, `internal/sqlstmt/` was added, and the table registry moved from the dialect to `DB`. See `2026-09-24-review-cleanup-design.md`.
```

And change the section 8 bullet `- Removing feature-flag branches that only mattered for other databases.` to `- ~~Removing feature-flag branches that only mattered for other databases.~~ Done on 2026-09-24, see `2026-09-24-review-cleanup-design.md`.`

- [ ] **Step 5: Re-index and report scope**

Run: `node .gitnexus/run.cjs analyze` from the project root, then `detect_changes({scope: "compare", base_ref: "master"})`. Report the affected symbols and execution flows to the user.

- [ ] **Step 6: Hand off for commit**

Run: `git status --short` and paste it. Report: the spec note file, and that every stage has been handed off. Remind the user that `review.md`, `AGENTS.md`, `CLAUDE.md` and `.claude/` are untracked and were not part of this work.

---

## Self-review notes

- Spec 3.1-3.4 → Tasks 1-2. Spec 4.1 → Task 3. Spec 4.2-4.3 → Tasks 3-4. Spec 5.1 → Tasks 8-9. Spec 5.2 → Tasks 8-9 (interface) and 12 (`Tables()`). Spec 5.3 → Task 7. Spec 5.4 → Tasks 5-7. Spec 5.5 → Tasks 8, 10. Spec 5.6 → Task 10. Spec 5.7 → Tasks 7, 10. Spec 6.1 → Task 12. Spec 6.2 → Task 11. Spec 6.3 → Task 12. Spec 7 → Tasks 13-14. Spec 9 → Task 15.
- The spec's section 7 names `int64` as a Range scan element type; `scanElem` cannot scan it. Task 13 uses `sql.NullInt64` for Scan, keeps `int64` for AppendQuery, and pins the panic.
- `NewQueryGen` takes two parameters from Task 12 onward. Tasks 8 and 11 use the one-parameter form because they run before Task 12; Task 12 updates every call site, including the pgdialect tests written in Task 11.
- `DBOption` changes its underlying type in Task 10; no test or file constructs a `DBOption` literal directly, so only `db.go` changes.
