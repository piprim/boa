# Bound Parameters Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every statement pgcrud executes reaches Postgres as SQL with `$1..$n` placeholders plus a slice of Go values, so pgx can prepare, cache and binary-encode it, and the inline literal formatter is deleted.

**Architecture:** `schema.QueryGen` carries a pointer to an `ArgList`. The three value leaves (`QueryGen.Append`, `QueryGen.AppendValue`, the per-type `AppenderFunc`s) append `$n` and push the Go value; identifiers, `NULL`, `DEFAULT` and `QueryAppender` fragments stay inline. Every execution site renders through one `DB.build` that attaches a fresh list and returns SQL plus args; the executor receives pgx option values (exec mode, a by-OID result-format map forcing arrays, ranges and multiranges to text) followed by the args.

**Tech Stack:** Go 1.25, `github.com/jackc/pgx/v5` v5.9.2 (`pgx`, `pgconn`, `pgtype`), `testify/require`, golden snapshots under `testdata/snapshots`, Postgres 16 via `docker-compose.yml` for integration tests.

**Spec:** `docs/superpowers/specs/2026-09-27-bound-parameters-design.md`

## Global Constraints

- The user commits; never run `git commit`. Each task ends with a "hand off for commit" step that lists the files and a suggested message.
- Before editing any function, method or type, run GitNexus impact analysis: `impact({target: "<symbol>", direction: "upstream"})` and report direct callers, affected processes and risk level. Warn before proceeding on HIGH or CRITICAL.
- Before each hand-off, run `detect_changes()` and confirm only the expected symbols and flows changed.
- Every test wraps each assertion or table row in `t.Run("descriptive label", ...)`.
- `go build ./... && go vet ./... && go test ./...` must pass at the end of every task.
- Values are always bound. There is no inline literal mode and no fallback above the parameter limit (spec §1).
- Bun's value rules are preserved: untagged struct, map and non-byte slice values become JSON bytes from `bunjson.Marshal`; `array`-tagged fields bind the slice as-is; hstore binds text; msgpack binds bytes (spec §4.2).
- `NULL` and `DEFAULT` stay inline SQL keywords (spec §4.3).
- `Bind` on a generator with no `ArgList` writes `?` (spec §8).
- Result columns of array, range and multirange types are requested in text; everything else in pgx's preferred format (spec §5.1).
- Postgres allows at most 65535 bound parameters; more returns `ErrTooManyParams` before any executor call (spec §6).
- Module path `github.com/piprim/pgcrud`; errors are prefixed `pgcrud:`.

## Review Focus

1. A statement that binds exactly 65535 values must execute; 65536 must fail with `ErrTooManyParams` and no executor call. Pinned in Task 5.
2. Has-many loading with duplicate parent keys (two stories by the same author) must bind each key once, because de-duplication used to compare rendered literals and `$n` is unique per value. Pinned in Task 9.
3. A `nullzero` zero value must render the inline keyword `NULL` (or `DEFAULT` on insert), never a bound nil, so Postgres can type it. Pinned in Task 3.
4. Calling `String()` twice on the same builder must return identical SQL, and `Args()` must line up with it, because each call attaches a fresh collector. Pinned in Task 6.
5. A raw query with `$1` placeholders and no `?` must pass its args through untouched, and a raw query with `?` must renumber; a raw query with `?` and no args must leave `?` in place unchanged from today. Pinned in Task 7.

---

## File structure

**Created**

- `schema/arglist.go` — `ArgList` collector and the `QueryGen` methods `WithArgList`, `Bind`, `BindArgs`, `BindError`, `Args`.
- `schema/arglist_test.go` — collector, numbering, template mode, error wrapping.
- `schema/bind_test.go` — the section 4.2 mapping table exercised through `Append`, `AppendValue` and `Field.AppendValue`.
- `query_build.go` — `maxParams`, `ErrTooManyParams`, `DB.build`, `baseQuery.build`, `DB.queryArgs`, `DB.execArgs`.
- `result_format.go` — `textResultFormats()` building the by-OID map from pgx's default type map.
- `result_format_test.go` — the map contains array, range and multirange OIDs and no scalar OIDs.
- `bind_test.go` (root, package `pgcrud_test`) — `String`/`Args`/`Build`, hook args, raw passthrough, options, parameter limit, relation loading binds parent keys.
- `integration_bind_test.go` — round trips against Postgres (Stage 3).

**Modified**

- `schema/querygen.go` — `bound *ArgList` field, `Append` becomes a bind point, `WithArg`/`WithNamedArg` carry the pointer; later `FormatQuery` removed.
- `schema/append_value.go` — every `AppenderFunc` binds; `bindUint`.
- `schema/sqltype.go` — `NullTime.AppendQuery` binds.
- `schema/dialect.go` — Stage 2: `Append*` methods and `BaseDialect` removed.
- `dialect/pgdialect/array.go`, `append.go`, `elem.go`, `hstore.go`, `range.go`, `dialect.go` — appenders bind; text parsers stay; `UintAsInt()`.
- `db.go` — options, `noCopyState` fields, `Exec`/`Query`/`QueryRow` through `build`; `format` removed.
- `hook.go` — `QueryEvent` loses `QueryTemplate`; `beforeQuery` takes args.
- `query_base.go` — `scan`, `_scan`, `exec` take args; soft-delete compare binds; template column text.
- `query_select.go`, `query_insert.go`, `query_update.go`, `query_delete.go`, `query_raw.go`, `query_values.go` — execution through `build`; `Build`, `Args`, `String`.
- `relation_join.go` — key conditions become `QueryAppender`s; de-duplication by value; `hasManyColumns` passes columns through.
- `query_test.go`, `snapshot_test.go`, `testdata/snapshots/*` — SQL plus args.
- `db_test.go`, `list_tuple_test.go`, `dialect/pgdialect/array_test.go`, `dialect/pgdialect/append_test.go` — expectations move from literals to bound values.
- `README.md`, `todo.md`, `docs/superpowers/specs/2026-09-22-pgcrud-design.md` — Stage 3 docs.

**Deleted (Stage 2)**

- `internal/hex.go`, `schema/querygen_linecomment_test.go`, `schema/appendjson_test.go`, `schema/appendstring_nul_test.go`; dead functions listed in Task 10.

---

# Stage 1: collector and bound execution

### Task 1: ArgList and the Bind methods on QueryGen

**Files:**
- Create: `schema/arglist.go`
- Create: `schema/arglist_test.go`
- Modify: `schema/querygen.go:19-22` (struct), `schema/querygen.go:118-130` (`WithArg`, `WithNamedArg`)

**Interfaces:**
- Produces:
  - `type ArgList struct` with `func NewArgList() *ArgList`, `func (l *ArgList) Args() []any` (never nil), `func (l *ArgList) Err() error`.
  - `func (gen QueryGen) WithArgList(l *ArgList) QueryGen`
  - `func (gen QueryGen) Bind(b []byte, v any) []byte` — appends `$n` and pushes `v`; with no list appends `?`.
  - `func (gen QueryGen) BindArgs(args ...any)` — pushes without writing SQL; no-op without a list.
  - `func (gen QueryGen) BindError(b []byte, err error) []byte` — records the first error as `pgcrud: bind arg N: <err>` and appends `?!(<err>)`.
  - `func (gen QueryGen) Args() []any` — nil without a list.

- [ ] **Step 1: Run impact analysis**

Run `impact({target: "QueryGen", direction: "upstream"})` and `impact({target: "WithArg", direction: "upstream"})`. Report callers, processes and risk to the user before editing.

- [ ] **Step 2: Write the failing tests**

Create `schema/arglist_test.go`:

```go
package schema

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArgList(t *testing.T) {
	t.Run("Bind numbers placeholders from $1 and collects values in order", func(t *testing.T) {
		list := NewArgList()
		gen := NewQueryGen(newNopDialect()).WithArgList(list)
		b := gen.Bind(nil, 42)
		b = append(b, ", "...)
		b = gen.Bind(b, "x")
		require.Equal(t, "$1, $2", string(b))
		require.Equal(t, []any{42, "x"}, list.Args())
	})

	t.Run("Args is an empty non-nil slice when nothing was bound", func(t *testing.T) {
		list := NewArgList()
		require.NotNil(t, list.Args())
		require.Empty(t, list.Args())
	})

	t.Run("Bind without a list writes a question mark and keeps nothing", func(t *testing.T) {
		gen := NewQueryGen(newNopDialect())
		require.Equal(t, "?", string(gen.Bind(nil, 42)))
		require.Nil(t, gen.Args())
	})

	t.Run("WithArg and WithNamedArg keep the list", func(t *testing.T) {
		list := NewArgList()
		gen := NewQueryGen(newNopDialect()).WithArgList(list)
		gen = gen.WithNamedArg("n", 1).WithArg(&namedArg{name: "m", value: 2})
		gen.Bind(nil, "v")
		require.Equal(t, []any{"v"}, list.Args())
	})

	t.Run("BindArgs pushes values without writing SQL", func(t *testing.T) {
		list := NewArgList()
		gen := NewQueryGen(newNopDialect()).WithArgList(list)
		gen.BindArgs(1, "two")
		require.Equal(t, []any{1, "two"}, list.Args())
		require.Equal(t, "$3", string(gen.Bind(nil, 3)))
	})

	t.Run("BindArgs without a list is a no-op", func(t *testing.T) {
		gen := NewQueryGen(newNopDialect())
		gen.BindArgs(1)
		require.Nil(t, gen.Args())
	})

	t.Run("BindError records the first error with the position of the failing arg", func(t *testing.T) {
		list := NewArgList()
		gen := NewQueryGen(newNopDialect()).WithArgList(list)
		gen.Bind(nil, 1)
		b := gen.BindError(nil, errors.New("boom"))
		gen.BindError(nil, errors.New("second"))
		require.Equal(t, "?!(boom)", string(b))
		require.EqualError(t, list.Err(), "pgcrud: bind arg 2: boom")
	})

	t.Run("BindError without a list only writes the marker", func(t *testing.T) {
		gen := NewQueryGen(newNopDialect())
		require.Equal(t, "?!(boom)", string(gen.BindError(nil, errors.New("boom"))))
	})
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./schema -run TestArgList`
Expected: compile error, `undefined: NewArgList`.

- [ ] **Step 4: Implement**

Create `schema/arglist.go`:

```go
package schema

import (
	"fmt"
	"strconv"
)

// ArgList collects the values bound to $n placeholders while a query renders.
// One list is attached to the QueryGen for the life of one render, so nested
// appenders number their placeholders after the ones already emitted.
type ArgList struct {
	args []any
	err  error
}

// NewArgList returns an empty collector.
func NewArgList() *ArgList {
	return &ArgList{}
}

// Args returns the bound values in placeholder order. It is never nil.
func (l *ArgList) Args() []any {
	if l.args == nil {
		return []any{}
	}
	return l.args
}

// Err returns the first error a value appender reported, or nil.
func (l *ArgList) Err() error {
	return l.err
}

func (l *ArgList) add(v any) int {
	l.args = append(l.args, v)
	return len(l.args)
}

func (l *ArgList) setErr(err error) {
	if l.err == nil {
		l.err = fmt.Errorf("pgcrud: bind arg %d: %w", len(l.args)+1, err)
	}
}

// WithArgList returns a copy of gen that binds values into l.
func (gen QueryGen) WithArgList(l *ArgList) QueryGen {
	gen.bound = l
	return gen
}

// Bind appends the next $n placeholder to b and records v as its value.
// Without a list it appends a question mark, the template form used when a
// query is rendered for its name rather than for execution.
func (gen QueryGen) Bind(b []byte, v any) []byte {
	if gen.bound == nil {
		return append(b, '?')
	}
	n := gen.bound.add(v)
	b = append(b, '$')
	return strconv.AppendInt(b, int64(n), 10)
}

// BindArgs records args as bound values without writing any SQL. It is used
// for raw SQL that already carries $n placeholders.
func (gen QueryGen) BindArgs(args ...any) {
	if gen.bound == nil {
		return
	}
	for _, a := range args {
		gen.bound.add(a)
	}
}

// BindError reports that a value could not be encoded. The first error is
// kept on the list and returned by DB.build; the marker keeps the SQL readable
// in templates.
func (gen QueryGen) BindError(b []byte, err error) []byte {
	if gen.bound != nil {
		gen.bound.setErr(err)
	}
	b = append(b, "?!("...)
	b = append(b, err.Error()...)
	return append(b, ')')
}

// Args returns the values bound so far, or nil when no list is attached.
func (gen QueryGen) Args() []any {
	if gen.bound == nil {
		return nil
	}
	return gen.bound.Args()
}
```

In `schema/querygen.go` change the struct and the two copy constructors:

```go
type QueryGen struct {
	dialect Dialect
	args    *namedArgList
	bound   *ArgList
}
```

```go
func (f QueryGen) WithArg(arg NamedArgAppender) QueryGen {
	return QueryGen{
		dialect: f.dialect,
		args:    f.args.WithArg(arg),
		bound:   f.bound,
	}
}

func (f QueryGen) WithNamedArg(name string, value any) QueryGen {
	return QueryGen{
		dialect: f.dialect,
		args:    f.args.WithArg(&namedArg{name: name, value: value}),
		bound:   f.bound,
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./schema -run TestArgList`
Expected: PASS, 8 subtests.

- [ ] **Step 6: Verify the whole module and hand off for commit**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all PASS (nothing else calls the new methods yet).

Run `detect_changes()`. Expected: only `QueryGen`, `WithArg`, `WithNamedArg` and the new symbols.

Hand off: files `schema/arglist.go`, `schema/arglist_test.go`, `schema/querygen.go`. Suggested message: `feat(schema): add ArgList collector and Bind on QueryGen`.

---

### Task 2: scalar bind points in QueryGen.Append and the appender table

**Files:**
- Modify: `schema/querygen.go:57-95` (`Append`)
- Modify: `schema/append_value.go:185-229` (`AppendBoolValue` … `AppendStringValue`)
- Modify: `dialect/pgdialect/dialect.go` (add `UintAsInt`)
- Create: `schema/bind_test.go`
- Delete: `schema/querygen_linecomment_test.go`

**Interfaces:**
- Consumes: `QueryGen.Bind`, `QueryGen.BindError` from Task 1.
- Produces:
  - `func bindUint(gen QueryGen, b []byte, n uint64, bits int) []byte` in `schema/append_value.go`.
  - Optional dialect interface in `schema`: `type uintAsIntDialect interface{ UintAsInt() bool }`.
  - `func (d *Dialect) UintAsInt() bool` on `pgdialect.Dialect`.
  - Test dialect `bindTestDialect` in `schema/bind_test.go`: a non-nop dialect over `nopDialect` so `AppendQuery` substitutes `?`.

- [ ] **Step 1: Run impact analysis**

Run `impact` on `Append` (the `QueryGen` method), `AppendIntValue`, `AppendStringValue`, `appendUint32Value`, `appendUint64Value`. Report to the user. `Append` is called from every `?` substitution, so expect HIGH; state that the change is the planned bind point.

- [ ] **Step 2: Write the failing tests**

Create `schema/bind_test.go`:

```go
package schema

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/piprim/pgcrud/dialect"
)

// bindTestDialect is a non-nop dialect so AppendQuery substitutes ? args.
type bindTestDialect struct {
	*nopDialect
	uintAsInt bool
}

func (bindTestDialect) Name() dialect.Name { return dialect.PG }
func (d bindTestDialect) UintAsInt() bool { return d.uintAsInt }

func newBindGen() (QueryGen, *ArgList) {
	list := NewArgList()
	return NewQueryGen(bindTestDialect{nopDialect: newNopDialect()}).WithArgList(list), list
}

func TestAppendBindsScalars(t *testing.T) {
	tm := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		in   any
		want any
	}{
		{"bool", true, true},
		{"int", 7, 7},
		{"int8", int8(-3), int8(-3)},
		{"int32", int32(9), int32(9)},
		{"int64", int64(-500), int64(-500)},
		{"uint", uint(1), uint(1)},
		{"uint8", uint8(2), uint8(2)},
		{"uint32", uint32(4294967295), uint32(4294967295)},
		{"uint64", uint64(5), uint64(5)},
		{"float32", float32(1.5), float32(1.5)},
		{"float64", -1.5, -1.5},
		{"string", "it's", "it's"},
		{"time", tm, tm},
		{"bytes", []byte{0, 1}, []byte{0, 1}},
		{"nil bytes bind as a nil slice", []byte(nil), []byte(nil)},
		{"driver.Valuer binds as-is", sql.NullString{String: "x", Valid: true}, sql.NullString{String: "x", Valid: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gen, list := newBindGen()
			b := gen.Append(nil, tt.in)
			require.Equal(t, "$1", string(b))
			require.Equal(t, []any{tt.want}, list.Args())
		})
	}

	t.Run("nil is the inline keyword NULL", func(t *testing.T) {
		gen, list := newBindGen()
		require.Equal(t, "NULL", string(gen.Append(nil, nil)))
		require.Empty(t, list.Args())
	})

	t.Run("nil pointer is the inline keyword NULL", func(t *testing.T) {
		gen, list := newBindGen()
		var p *int
		require.Equal(t, "NULL", string(gen.Append(nil, p)))
		require.Empty(t, list.Args())
	})

	t.Run("non-nil pointer binds the pointed-to value", func(t *testing.T) {
		gen, list := newBindGen()
		n := 5
		require.Equal(t, "$1", string(gen.Append(nil, &n)))
		require.Equal(t, []any{int64(5)}, list.Args())
	})

	t.Run("Safe and Ident stay inline", func(t *testing.T) {
		gen, list := newBindGen()
		b := gen.Append(nil, Safe("now()"))
		b = append(b, ' ')
		b = gen.Append(b, Ident("t.col"))
		require.Equal(t, `now() "t"."col"`, string(b))
		require.Empty(t, list.Args())
	})

	t.Run("named value types bind through their kind", func(t *testing.T) {
		type Status int
		gen, list := newBindGen()
		require.Equal(t, "$1", string(gen.Append(nil, Status(3))))
		require.Equal(t, []any{int64(3)}, list.Args())
	})
}

func TestAppendUintAsInt(t *testing.T) {
	t.Run("off: uint32 binds unsigned", func(t *testing.T) {
		gen, list := newBindGen()
		gen.Append(nil, uint32(4294967295))
		require.Equal(t, []any{uint32(4294967295)}, list.Args())
	})

	t.Run("on: uint32 wraps to int32 and uint64 to int64", func(t *testing.T) {
		list := NewArgList()
		gen := NewQueryGen(bindTestDialect{nopDialect: newNopDialect(), uintAsInt: true}).WithArgList(list)
		gen.Append(nil, uint32(4294967295))
		gen.Append(nil, uint64(18446744073709551615))
		require.Equal(t, []any{int32(-1), int64(-1)}, list.Args())
	})

	t.Run("on: reflected uint32 field value wraps too", func(t *testing.T) {
		list := NewArgList()
		gen := NewQueryGen(bindTestDialect{nopDialect: newNopDialect(), uintAsInt: true}).WithArgList(list)
		gen.AppendValue(nil, reflect.ValueOf(uint32(4294967295)))
		require.Equal(t, []any{int32(-1)}, list.Args())
	})
}

func TestAppendQueryPlaceholders(t *testing.T) {
	t.Run("positional args are numbered in order", func(t *testing.T) {
		gen, list := newBindGen()
		got := gen.AppendQuery(nil, "a = ? AND b = ?", 1, "x")
		require.Equal(t, "a = $1 AND b = $2", string(got))
		require.Equal(t, []any{1, "x"}, list.Args())
	})

	t.Run("indexed args ?0 ?1 bind by index and may repeat", func(t *testing.T) {
		gen, list := newBindGen()
		got := gen.AppendQuery(nil, "?1 ?0 ?1", "a", "b")
		require.Equal(t, "$1 $2 $3", string(got))
		require.Equal(t, []any{"b", "a", "b"}, list.Args())
	})

	t.Run("named args bind and identifiers stay inline", func(t *testing.T) {
		gen, list := newBindGen()
		gen = gen.WithNamedArg("lim", 10)
		got := gen.AppendQuery(nil, "LIMIT ?lim")
		require.Equal(t, "LIMIT $1", string(got))
		require.Equal(t, []any{10}, list.Args())
	})

	t.Run("escaped question mark is literal", func(t *testing.T) {
		gen, list := newBindGen()
		got := gen.AppendQuery(nil, `data \? 'k' AND id = ?`, 1)
		require.Equal(t, "data ? 'k' AND id = $1", string(got))
		require.Equal(t, []any{1}, list.Args())
	})

	t.Run("missing arg leaves the question mark", func(t *testing.T) {
		gen, list := newBindGen()
		got := gen.AppendQuery(nil, "a = ? AND b = ?", 1)
		require.Equal(t, "a = $1 AND b = ?", string(got))
		require.Equal(t, []any{1}, list.Args())
	})

	t.Run("a nested appender continues the numbering", func(t *testing.T) {
		gen, list := newBindGen()
		inner := SafeQuery("x = ?", []any{"in"})
		got := gen.AppendQuery(nil, "a = ? AND (?) AND c = ?", 1, inner, 3)
		require.Equal(t, "a = $1 AND (x = $2) AND c = $3", string(got))
		require.Equal(t, []any{1, "in", 3}, list.Args())
	})

	t.Run("without a list the template keeps question marks", func(t *testing.T) {
		gen := NewQueryGen(bindTestDialect{nopDialect: newNopDialect()})
		got := gen.AppendQuery(nil, "a = ? AND b = ?", 1, "x")
		require.Equal(t, "a = ? AND b = ?", string(got))
	})
}
```

Delete `schema/querygen_linecomment_test.go`: the guard it tests protected against a negative literal following `-` in SQL text; values no longer enter the text.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./schema -run 'TestAppendBindsScalars|TestAppendUintAsInt|TestAppendQueryPlaceholders'`
Expected: FAIL. `Append` still writes literals, so `"$1"` assertions fail; `UintAsInt` undefined is not an error yet because the test dialect defines it itself.

- [ ] **Step 4: Implement**

In `schema/querygen.go` replace `Append`:

```go
// Append writes v into b as a bound parameter, or inline when v is a SQL
// fragment (QueryAppender) or nil.
func (gen QueryGen) Append(b []byte, v any) []byte {
	switch v := v.(type) {
	case nil:
		return dialect.AppendNull(b)
	case QueryAppender:
		return AppendQueryAppender(gen, b, v)
	case uint32:
		return bindUint(gen, b, uint64(v), 32)
	case uint64:
		return bindUint(gen, b, v, 64)
	case driver.Valuer:
		// A typed nil pointer satisfies the interface; it means NULL.
		if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer && rv.IsNil() {
			return dialect.AppendNull(b)
		}
		return gen.Bind(b, v)
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16,
		float32, float64, string, time.Time, []byte:
		return gen.Bind(b, v)
	default:
		vv := reflect.ValueOf(v)
		if vv.Kind() == reflect.Pointer && vv.IsNil() {
			return dialect.AppendNull(b)
		}
		appender := Appender(gen.Dialect(), vv.Type())
		return appender(gen, b, vv)
	}
}
```

Add `"database/sql/driver"` to the imports of `querygen.go`; `strconv` stays (used by `append`).

In `schema/append_value.go` replace the scalar appenders and add `bindUint`:

```go
// uintAsIntDialect is implemented by dialects that store unsigned integers in
// signed columns by reinterpreting the bits, see pgdialect.WithAppendUintAsInt.
type uintAsIntDialect interface {
	UintAsInt() bool
}

// bindUint binds n, wrapped to the signed type of the same width when the
// dialect asks for it.
func bindUint(gen QueryGen, b []byte, n uint64, bits int) []byte {
	if d, ok := gen.Dialect().(uintAsIntDialect); ok && d.UintAsInt() {
		if bits == 32 {
			return gen.Bind(b, int32(uint32(n)))
		}
		return gen.Bind(b, int64(n))
	}
	if bits == 32 {
		return gen.Bind(b, uint32(n))
	}
	return gen.Bind(b, n)
}

func AppendBoolValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, v.Bool())
}

func AppendIntValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, v.Int())
}

func AppendUintValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, v.Uint())
}

func appendUint32Value(gen QueryGen, b []byte, v reflect.Value) []byte {
	return bindUint(gen, b, v.Uint(), 32)
}

func appendUint64Value(gen QueryGen, b []byte, v reflect.Value) []byte {
	return bindUint(gen, b, v.Uint(), 64)
}

func AppendFloat32Value(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, float32(v.Float()))
}

func AppendFloat64Value(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, v.Float())
}

func appendBytesValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, v.Bytes())
}

func appendArrayBytesValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	tmp := make([]byte, v.Len())
	reflect.Copy(reflect.ValueOf(tmp), v)
	return gen.Bind(b, tmp)
}

func AppendStringValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, v.String())
}
```

Remove the now-unused `strconv` import from `append_value.go` if nothing else uses it (check with `go build`).

In `dialect/pgdialect/dialect.go` add:

```go
// UintAsInt reports whether WithAppendUintAsInt is on: unsigned values are
// bound as the signed type of the same width, wrapping on overflow.
func (d *Dialect) UintAsInt() bool {
	return d.uintAsInt
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./schema -run 'TestAppendBindsScalars|TestAppendUintAsInt|TestAppendQueryPlaceholders'`
Expected: PASS.

- [ ] **Step 6: Run the whole module and observe the expected breakage**

Run: `go build ./... && go vet ./... && go test ./... 2>&1 | tail -40`
Expected: root package `TestQuery` snapshots and `TestNew`, `TestExec` ("DB.Exec formats placeholders"), `TestListAndTuple` fail, because values now render as `?` where no list is attached. Do not fix them here; Tasks 5 and 6 own them. Everything in `schema`, `dialect/pgdialect` and `internal` must pass.

Record the exact list of failing root tests in the hand-off note so the reviewer knows they are expected.

- [ ] **Step 7: Hand off for commit**

Run `detect_changes()`. Hand off: `schema/querygen.go`, `schema/append_value.go`, `schema/bind_test.go`, `dialect/pgdialect/dialect.go`, deletion of `schema/querygen_linecomment_test.go`. Suggested message: `feat(schema): bind scalar values as $n parameters`. Note in the hand-off that the root package tests fail until Task 6 regenerates snapshots; the user may prefer to commit Tasks 2 to 6 together.

---

### Task 3: JSON, msgpack, driver.Valuer, network and NullTime appenders

**Files:**
- Modify: `schema/append_value.go:231-316` (`AppendJSONValue` … `appendMsgpack`)
- Modify: `schema/sqltype.go:106-112` (`NullTime.AppendQuery`)
- Modify: `schema/bind_test.go` (append tests)

**Interfaces:**
- Consumes: `QueryGen.Bind`, `QueryGen.BindError`.
- Produces: no new names. `AppendJSONValue` binds `[]byte`; `appendMsgpack` binds `[]byte`; `appendDriverValue` binds the valuer itself.

- [ ] **Step 1: Run impact analysis**

Run `impact` on `AppendJSONValue`, `appendDriverValue`, `appendMsgpack`, `appendJSONRawMessageValue`, `appendTimeValue`, `appendIPNetValue`, `appendStringer` and `NullTime.AppendQuery`. Report.

- [ ] **Step 2: Write the failing tests**

Append to `schema/bind_test.go`:

```go
func TestAppendBindsComposites(t *testing.T) {
	type Meta struct {
		K string `json:"k"`
	}

	t.Run("untagged struct binds JSON bytes", func(t *testing.T) {
		gen, list := newBindGen()
		require.Equal(t, "$1", string(gen.Append(nil, Meta{K: "v"})))
		require.Equal(t, []any{[]byte(`{"k":"v"}`)}, list.Args())
	})

	t.Run("map binds JSON bytes", func(t *testing.T) {
		gen, list := newBindGen()
		gen.Append(nil, map[string]int{"a": 1})
		require.Equal(t, []any{[]byte(`{"a":1}`)}, list.Args())
	})

	t.Run("non-byte slice binds JSON bytes", func(t *testing.T) {
		gen, list := newBindGen()
		gen.Append(nil, []string{"a", "b"})
		require.Equal(t, []any{[]byte(`["a","b"]`)}, list.Args())
	})

	t.Run("json.RawMessage binds its bytes and nil is NULL", func(t *testing.T) {
		gen, list := newBindGen()
		b := gen.Append(nil, json.RawMessage(`{"x":1}`))
		b = gen.Append(b, json.RawMessage(nil))
		require.Equal(t, "$1NULL", string(b))
		require.Equal(t, []any{[]byte(`{"x":1}`)}, list.Args())
	})

	t.Run("net types bind their string form", func(t *testing.T) {
		gen, list := newBindGen()
		_, ipnet, _ := net.ParseCIDR("10.0.0.0/8")
		gen.Append(nil, net.ParseIP("10.1.2.3"))
		gen.Append(nil, *ipnet)
		gen.Append(nil, netip.MustParseAddr("::1"))
		gen.Append(nil, netip.MustParsePrefix("10.0.0.0/24"))
		require.Equal(t, []any{"10.1.2.3", "10.0.0.0/8", "::1", "10.0.0.0/24"}, list.Args())
	})

	t.Run("pointer to driver.Valuer nil is NULL, non-nil binds the pointer", func(t *testing.T) {
		gen, list := newBindGen()
		var p *sql.NullInt64
		b := gen.Append(nil, p)
		v := &sql.NullInt64{Int64: 3, Valid: true}
		b = gen.Append(b, v)
		require.Equal(t, "NULL$1", string(b))
		require.Equal(t, []any{v}, list.Args())
	})

	t.Run("unmarshalable JSON records a bind error", func(t *testing.T) {
		gen, list := newBindGen()
		b := gen.Append(nil, map[string]any{"f": func() {}})
		require.Contains(t, string(b), "?!(")
		require.Error(t, list.Err())
		require.Contains(t, list.Err().Error(), "pgcrud: bind arg 1:")
	})

	t.Run("NullTime zero is NULL and set binds the time", func(t *testing.T) {
		gen, list := newBindGen()
		tm := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		b, err := NullTime{}.AppendQuery(gen, nil)
		require.NoError(t, err)
		b, err = NullTime{Time: tm}.AppendQuery(gen, b)
		require.NoError(t, err)
		require.Equal(t, "NULL$1", string(b))
		require.Equal(t, []any{tm}, list.Args())
	})
}

func TestFieldAppendValue(t *testing.T) {
	type Model struct {
		ID      int64  `bun:",pk"`
		Name    string `bun:",nullzero"`
		Note    *string
		Meta    map[string]int `bun:",type:jsonb"`
		Payload map[string]int `bun:",msgpack"`
		Tags    []string       `bun:",array"`
	}
	tables := NewTables(bindTestDialect{nopDialect: newNopDialect()})
	table := tables.Get(reflect.TypeFor[*Model]())

	field := func(name string) *Field {
		f, ok := table.FieldMap[name]
		require.True(t, ok, name)
		return f
	}

	t.Run("nullzero zero string is inline NULL", func(t *testing.T) {
		gen, list := newBindGen()
		b := field("name").AppendValue(gen, nil, reflect.ValueOf(Model{}))
		require.Equal(t, "NULL", string(b))
		require.Empty(t, list.Args())
	})

	t.Run("nullzero zero string is inline DEFAULT with the default placeholder", func(t *testing.T) {
		gen, list := newBindGen()
		b := field("name").AppendValueOrDefault(gen, nil, reflect.ValueOf(Model{}))
		require.Equal(t, "DEFAULT", string(b))
		require.Empty(t, list.Args())
	})

	t.Run("nil pointer field is inline NULL", func(t *testing.T) {
		gen, list := newBindGen()
		b := field("note").AppendValue(gen, nil, reflect.ValueOf(Model{}))
		require.Equal(t, "NULL", string(b))
		require.Empty(t, list.Args())
	})

	t.Run("set values bind", func(t *testing.T) {
		gen, list := newBindGen()
		note := "n"
		m := Model{ID: 1, Name: "a", Note: &note}
		b := field("id").AppendValue(gen, nil, reflect.ValueOf(m))
		b = field("name").AppendValue(gen, b, reflect.ValueOf(m))
		b = field("note").AppendValue(gen, b, reflect.ValueOf(m))
		require.Equal(t, "$1$2$3", string(b))
		require.Equal(t, []any{int64(1), "a", "n"}, list.Args())
	})

	t.Run("jsonb-typed field binds JSON bytes", func(t *testing.T) {
		gen, list := newBindGen()
		field("meta").AppendValue(gen, nil, reflect.ValueOf(Model{Meta: map[string]int{"a": 1}}))
		require.Equal(t, []any{[]byte(`{"a":1}`)}, list.Args())
	})

	t.Run("msgpack field binds msgpack bytes", func(t *testing.T) {
		gen, list := newBindGen()
		field("payload").AppendValue(gen, nil, reflect.ValueOf(Model{Payload: map[string]int{"a": 1}}))
		require.Len(t, list.Args(), 1)
		raw, ok := list.Args()[0].([]byte)
		require.True(t, ok)
		var back map[string]int
		require.NoError(t, msgpack.Unmarshal(raw, &back))
		require.Equal(t, map[string]int{"a": 1}, back)
	})
}
```

Add imports to `schema/bind_test.go`: `"encoding/json"`, `"net"`, `"net/netip"`, `"github.com/vmihailenco/msgpack/v5"`.

Note: the `array` field case is asserted in Task 4 because the nop dialect does not install the array appender; the field is declared here so the table has it.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./schema -run 'TestAppendBindsComposites|TestFieldAppendValue'`
Expected: FAIL on the JSON, msgpack, net and NullTime cases (`$1` vs literal text). The `nullzero` and nil-pointer cases already pass, which is the point: `Field.appendValue` is unchanged.

- [ ] **Step 4: Implement**

In `schema/append_value.go` replace the composite appenders:

```go
func AppendJSONValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	bb, err := bunjson.Marshal(v.Interface())
	if err != nil {
		return gen.BindError(b, err)
	}

	if len(bb) > 0 && bb[len(bb)-1] == '\n' {
		bb = bb[:len(bb)-1]
	}

	return gen.Bind(b, bb)
}

func appendTimeValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, v.Interface().(time.Time))
}

func appendIPNetValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	ipnet := v.Interface().(net.IPNet)
	return gen.Bind(b, ipnet.String())
}

func appendStringer(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, v.Interface().(fmt.Stringer).String())
}

func appendJSONRawMessageValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	bytes := v.Bytes()
	if bytes == nil {
		return dialect.AppendNull(b)
	}
	return gen.Bind(b, []byte(bytes))
}

func appendQueryAppenderValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	return AppendQueryAppender(gen, b, v.Interface().(QueryAppender))
}

// appendDriverValue binds the valuer itself; pgx calls Value() when encoding.
func appendDriverValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, v.Interface())
}

func addrAppender(fn AppenderFunc) AppenderFunc {
	return func(gen QueryGen, b []byte, v reflect.Value) []byte {
		if !v.CanAddr() {
			err := fmt.Errorf("pgcrud: Append(nonaddressable %T)", v.Interface())
			return gen.BindError(b, err)
		}
		return fn(gen, b, v.Addr())
	}
}

func appendMsgpack(gen QueryGen, b []byte, v reflect.Value) []byte {
	var buf bytes.Buffer

	enc := msgpack.GetEncoder()
	defer msgpack.PutEncoder(enc)

	enc.Reset(&buf)
	if err := enc.EncodeValue(v); err != nil {
		return gen.BindError(b, err)
	}

	return gen.Bind(b, buf.Bytes())
}

func AppendQueryAppender(gen QueryGen, b []byte, app QueryAppender) []byte {
	bb, err := app.AppendQuery(gen, b)
	if err != nil {
		return gen.BindError(b, err)
	}
	return bb
}
```

Imports of `append_value.go`: add `"bytes"`; remove `"database/sql/driver"` and `"github.com/piprim/pgcrud/internal"` if now unused (`go build` tells you). `driverValuerType` in `reflect.go` stays; `appender()` still uses it to select `appendDriverValue`.

In `schema/sqltype.go`:

```go
func (tm NullTime) AppendQuery(gen QueryGen, b []byte) ([]byte, error) {
	if tm.IsZero() {
		return dialect.AppendNull(b), nil
	}
	return gen.Bind(b, tm.Time), nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./schema`
Expected: PASS for the new tests. `TestBaseDialectAppendJSON*` and `TestBaseDialectAppendString*` still pass since `BaseDialect` is untouched until Task 10.

- [ ] **Step 6: Hand off for commit**

Run `go build ./... && go vet ./...` (must pass) and `detect_changes()`. Root snapshot failures are still expected. Hand off: `schema/append_value.go`, `schema/sqltype.go`, `schema/bind_test.go`. Suggested message: `feat(schema): bind JSON, msgpack, valuer and network values`.

---

### Task 4: pgdialect arrays, hstore and ranges bind

**Files:**
- Modify: `dialect/pgdialect/array.go:75-337` (appenders; scanners from line 339 stay)
- Modify: `dialect/pgdialect/append.go` (hstore appender, type vars)
- Modify: `dialect/pgdialect/elem.go`
- Modify: `dialect/pgdialect/range.go:125-190` (`AppendQuery` for `Range`, `MultiRange`, `appendRange`)
- Modify: `dialect/pgdialect/array_test.go`, `dialect/pgdialect/append_test.go`
- Create: `dialect/pgdialect/range_test.go`

**Interfaces:**
- Consumes: `schema.QueryGen.Bind`, `BindError`.
- Produces:
  - `func bindArrayValue(gen schema.QueryGen, b []byte, v reflect.Value) []byte`
  - `func hstoreText(m map[string]string) string`
  - `func appendRange[T any](buf []byte, r Range[T]) ([]byte, error)`
  - `func appendElem(buf []byte, val any) ([]byte, error)`

- [ ] **Step 1: Run impact analysis**

Run `impact` on `arrayAppender`, `arrayElemAppender`, `hstoreAppender`, `appendMapStringString`, `appendElem`, `appendStringElem`, `appendRange`, and the `AppendQuery` methods of `ArrayValue`, `HStoreValue`, `Range`, `MultiRange`. Report.

- [ ] **Step 2: Write the failing tests**

Replace `dialect/pgdialect/array_test.go`:

```go
package pgdialect

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/piprim/pgcrud/schema"
)

func ptr[T any](v T) *T {
	return &v
}

func bindGen() (schema.QueryGen, *schema.ArgList) {
	list := schema.NewArgList()
	return schema.NewQueryGen(New()).WithArgList(list), list
}

func TestArrayAppend(t *testing.T) {
	tcases := []struct {
		name  string
		input any
	}{
		{"[]byte elements", []byte{1, 2}},
		{"[]*byte", []*byte{ptr(byte(1)), ptr(byte(2))}},
		{"[]int", []int{1, 2}},
		{"[]*int", []*int{ptr(1), ptr(2)}},
		{"[]string", []string{"foo", "bar"}},
		{"[]*string", []*string{ptr("foo"), ptr("bar")}},
		{"[][]byte", [][]byte{{1, 2, 3}, {4, 5, 6}}},
		{"[]time.Time", []time.Time{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}},
		{"[]float64", []float64{1.5, 2.5}},
	}

	for _, tcase := range tcases {
		t.Run(tcase.name, func(t *testing.T) {
			gen, list := bindGen()
			out, err := Array(tcase.input).AppendQuery(gen, []byte{})
			require.NoError(t, err)
			require.Equal(t, "$1", string(out))
			require.Equal(t, []any{tcase.input}, list.Args())
		})
	}

	t.Run("nil slice is inline NULL", func(t *testing.T) {
		gen, list := bindGen()
		out, err := Array([]string(nil)).AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, "NULL", string(out))
		require.Empty(t, list.Args())
	})

	t.Run("pointer to slice binds the slice", func(t *testing.T) {
		gen, list := bindGen()
		s := []int64{7}
		out, err := Array(&s).AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, "$1", string(out))
		require.Equal(t, []any{[]int64{7}}, list.Args())
	})

	t.Run("array-tagged field binds the slice", func(t *testing.T) {
		type Model struct {
			ID   int64    `bun:",pk"`
			Tags []string `bun:",array"`
		}
		d := New()
		table := d.Tables().Get(reflect.TypeFor[*Model]())
		gen, list := bindGen()
		b := table.FieldMap["tags"].AppendValue(gen, nil, reflect.ValueOf(Model{Tags: []string{"a"}}))
		require.Equal(t, "$1", string(b))
		require.Equal(t, []any{[]string{"a"}}, list.Args())
	})
}
```

Replace `dialect/pgdialect/append_test.go`:

```go
package pgdialect

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHStoreAppender(t *testing.T) {
	tests := []struct {
		input      map[string]string
		expectedIn []string // maps being unsorted, several outputs are valid
	}{
		{map[string]string{}, []string{``}},
		{map[string]string{"": ""}, []string{`""=>""`}},
		{map[string]string{`\`: `\`}, []string{`"\\"=>"\\"`}},
		{map[string]string{"'": "'"}, []string{`"'"=>"'"`}},
		{map[string]string{`'"{}`: `'"{}`}, []string{`"'\"{}"=>"'\"{}"`}},
		{map[string]string{"1": "2", "3": "4"}, []string{`"1"=>"2","3"=>"4"`, `"3"=>"4","1"=>"2"`}},
		{map[string]string{"1": ""}, []string{`"1"=>""`}},
		{map[string]string{"1": "NULL"}, []string{`"1"=>"NULL"`}},
	}

	appendFunc := pgDialect.hstoreAppender(reflect.TypeFor[map[string]string]())

	for i, test := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			gen, list := bindGen()
			got := appendFunc(gen, []byte{}, reflect.ValueOf(test.input))
			require.Equal(t, "$1", string(got))
			require.Len(t, list.Args(), 1)
			require.Contains(t, test.expectedIn, list.Args()[0])
		})
	}

	t.Run("nil map is inline NULL", func(t *testing.T) {
		gen, list := bindGen()
		got := appendFunc(gen, nil, reflect.ValueOf(map[string]string(nil)))
		require.Equal(t, "NULL", string(got))
		require.Empty(t, list.Args())
	})

	t.Run("HStore wrapper binds the text", func(t *testing.T) {
		gen, list := bindGen()
		got, err := HStore(map[string]string{"k": "v"}).AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, "$1", string(got))
		require.Equal(t, []any{`"k"=>"v"`}, list.Args())
	})
}
```

Create `dialect/pgdialect/range_test.go`:

```go
package pgdialect

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRangeAppend(t *testing.T) {
	t.Run("int64 range binds its text", func(t *testing.T) {
		gen, list := bindGen()
		got, err := NewRange[int64](1, 5).AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, "$1", string(got))
		require.Equal(t, []any{"[1,5)"}, list.Args())
	})

	t.Run("int range binds through its kind", func(t *testing.T) {
		gen, list := bindGen()
		_, err := NewRange[int](1, 5).AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, []any{"[1,5)"}, list.Args())
	})

	t.Run("time range quotes bounds", func(t *testing.T) {
		gen, list := bindGen()
		lo := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		hi := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		_, err := NewRange(lo, hi).AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, []any{`["2026-01-01 00:00:00+00:00","2026-01-02 00:00:00+00:00")`}, list.Args())
	})

	t.Run("string bound with a quote is escaped for the range parser only", func(t *testing.T) {
		gen, list := bindGen()
		_, err := NewRange("a'b", `c"d`).AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, []any{`["a'b","c\"d")`}, list.Args())
	})

	t.Run("empty range binds empty", func(t *testing.T) {
		gen, list := bindGen()
		_, err := NewEmptyRange[int64]().AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, []any{"empty"}, list.Args())
	})

	t.Run("unset bounds render as exclusive infinities", func(t *testing.T) {
		gen, list := bindGen()
		_, err := Range[int64]{Lower: 3, LowerBound: RangeBoundInclusiveLeft}.AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, []any{"[3,)"}, list.Args())
	})

	t.Run("multirange binds its text and nil is an empty set", func(t *testing.T) {
		gen, list := bindGen()
		m := MultiRange[int64]{NewRange[int64](1, 2), NewRange[int64](5, 6)}
		_, err := m.AppendQuery(gen, nil)
		require.NoError(t, err)
		_, err = MultiRange[int64](nil).AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, []any{"{[1,2),[5,6)}", "{}"}, list.Args())
	})

	t.Run("unsupported bound type is an error", func(t *testing.T) {
		gen, _ := bindGen()
		_, err := NewRange(struct{}{}, struct{}{}).AppendQuery(gen, nil)
		require.Error(t, err)
	})
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./dialect/pgdialect`
Expected: FAIL. Arrays and hstore still render `'{...}'` literals; `appendRange` has the wrong signature (compile error in `range_test.go` is acceptable evidence).

- [ ] **Step 4: Implement**

In `dialect/pgdialect/array.go`, replace everything from `func (d *Dialect) arrayAppender` up to (not including) `func arrayScanner` with:

```go
func (d *Dialect) arrayAppender(typ reflect.Type) schema.AppenderFunc {
	switch typ.Kind() {
	case reflect.Pointer:
		if fn := d.arrayAppender(typ.Elem()); fn != nil {
			return schema.PtrAppender(fn)
		}
		return nil
	case reflect.Slice, reflect.Array:
		return bindArrayValue
	default:
		return nil
	}
}

// bindArrayValue binds the slice or array as-is; pgx encodes it as a
// Postgres array of the parameter's element type. A nil slice is NULL.
func bindArrayValue(gen schema.QueryGen, b []byte, v reflect.Value) []byte {
	if v.Kind() == reflect.Slice && v.IsNil() {
		return dialect.AppendNull(b)
	}
	return gen.Bind(b, v.Interface())
}
```

Then remove the imports `array.go` no longer needs (`database/sql/driver`, `math`, `strconv`, `time`); keep `database/sql`, `fmt`, `reflect`, `dialect`, `internal`, `schema`. In `append.go` delete the `slice*Type` variables and `driverValuerType`; keep `stringType`, `intType`, `int64Type`, `float64Type`, `timeType` (the scanners use them) and `appendTime` (ranges use it).

Replace `dialect/pgdialect/append.go` hstore section:

```go
var mapStringStringType = reflect.TypeOf(map[string]string(nil))

func (d *Dialect) hstoreAppender(typ reflect.Type) schema.AppenderFunc {
	kind := typ.Kind()

	switch kind {
	case reflect.Pointer:
		if fn := d.hstoreAppender(typ.Elem()); fn != nil {
			return schema.PtrAppender(fn)
		}
	case reflect.Map:
		// ok:
	default:
		return nil
	}

	if typ.Key() == stringType && typ.Elem() == stringType {
		return bindMapStringString
	}

	return func(gen schema.QueryGen, b []byte, v reflect.Value) []byte {
		err := fmt.Errorf("pgcrud: Hstore(unsupported %s)", v.Type())
		return gen.BindError(b, err)
	}
}

// bindMapStringString binds the hstore text form as a string. Postgres
// coerces a text parameter to hstore; pgx has no fixed OID for it.
func bindMapStringString(gen schema.QueryGen, b []byte, v reflect.Value) []byte {
	if v.IsNil() {
		return dialect.AppendNull(b)
	}
	m := v.Convert(mapStringStringType).Interface().(map[string]string)
	return gen.Bind(b, hstoreText(m))
}

// hstoreText renders m in hstore input syntax: "k"=>"v",... with quotes and
// backslashes escaped by a backslash.
func hstoreText(m map[string]string) string {
	var b []byte
	for key, value := range m {
		b = appendStringElem(b, key)
		b = append(b, '=', '>')
		b = appendStringElem(b, value)
		b = append(b, ',')
	}
	if len(m) > 0 {
		b = b[:len(b)-1]
	}
	return string(b)
}
```

Replace `dialect/pgdialect/elem.go` entirely:

```go
package pgdialect

import (
	"database/sql/driver"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"time"
	"unicode/utf8"
)

// appendElem writes one range bound in the text form the Postgres range
// parser reads. Strings are double-quoted with backslash escapes.
func appendElem(buf []byte, val any) ([]byte, error) {
	switch val := val.(type) {
	case time.Time:
		buf = append(buf, '"')
		buf = appendTime(buf, val)
		return append(buf, '"'), nil
	case []byte:
		return appendStringElem(buf, string(val)), nil
	case driver.Valuer:
		v, err := val.Value()
		if err != nil {
			return nil, fmt.Errorf("pgdialect: can't append elem value: %w", err)
		}
		return appendElem(buf, v)
	}

	rv := reflect.ValueOf(val)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.AppendInt(buf, rv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.AppendUint(buf, rv.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return appendFloat64Elem(buf, rv.Float()), nil
	case reflect.String:
		return appendStringElem(buf, rv.String()), nil
	}
	return nil, fmt.Errorf("pgdialect: can't append elem %T", val)
}

func appendFloat64Elem(b []byte, num float64) []byte {
	switch {
	case math.IsNaN(num):
		return append(b, "NaN"...)
	case math.IsInf(num, 1):
		return append(b, "Infinity"...)
	case math.IsInf(num, -1):
		return append(b, "-Infinity"...)
	default:
		return strconv.AppendFloat(b, num, 'f', -1, 64)
	}
}

// appendStringElem writes s double-quoted for the array, range and hstore
// text parsers: a double quote and a backslash are escaped by a backslash.
// It performs no SQL-literal escaping, because the text is bound, not inlined.
func appendStringElem(b []byte, s string) []byte {
	b = append(b, '"')
	for _, r := range s {
		switch r {
		case 0:
			// Postgres rejects NUL in text; dropping it keeps the parser happy.
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		default:
			if r < utf8.RuneSelf {
				b = append(b, byte(r))
				break
			}
			l := len(b)
			if cap(b)-l < utf8.UTFMax {
				b = append(b, make([]byte, utf8.UTFMax)...)
			}
			n := utf8.EncodeRune(b[l:l+utf8.UTFMax], r)
			b = b[:l+n]
		}
	}
	return append(b, '"')
}
```

In `dialect/pgdialect/range.go` replace `Range.AppendQuery`, `appendRange` and `MultiRange.AppendQuery`:

```go
var _ schema.QueryAppender = (*Range[any])(nil)

// AppendQuery binds the range in its text form. Postgres infers the range type
// from the column or operator; where nothing does, cast in the SQL: ?::daterange.
func (r Range[T]) AppendQuery(gen schema.QueryGen, buf []byte) ([]byte, error) {
	text, err := appendRange(nil, r)
	if err != nil {
		return nil, err
	}
	return gen.Bind(buf, string(text)), nil
}

func appendRange[T any](buf []byte, r Range[T]) (_ []byte, err error) {
	if r.IsEmpty() {
		return append(buf, "empty"...), nil
	}

	if r.LowerBound == RangeBoundUnset {
		// A missing bound is always exclusive: [,] is read as (,).
		buf = append(buf, byte(RangeBoundExclusiveLeft))
	} else {
		buf = append(buf, byte(r.LowerBound))
		buf, err = appendElem(buf, r.Lower)
		if err != nil {
			return nil, err
		}
	}
	buf = append(buf, ',')
	if r.UpperBound == RangeBoundUnset {
		buf = append(buf, byte(RangeBoundExclusiveRight))
	} else {
		buf, err = appendElem(buf, r.Upper)
		if err != nil {
			return nil, err
		}
		buf = append(buf, byte(r.UpperBound))
	}
	return buf, nil
}
```

```go
// AppendQuery binds the multirange in its text form, {} for nil.
func (m MultiRange[T]) AppendQuery(gen schema.QueryGen, buf []byte) ([]byte, error) {
	text := []byte{'{'}
	for i, r := range m {
		if i > 0 {
			text = append(text, ',')
		}
		var err error
		text, err = appendRange(text, r)
		if err != nil {
			return nil, err
		}
	}
	text = append(text, '}')
	return gen.Bind(buf, string(text)), nil
}
```

`ArrayValue.AppendQuery` and `HStoreValue.AppendQuery` keep their bodies; they call the new appenders through `a.append` and `h.append`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./dialect/pgdialect`
Expected: PASS, including the untouched parser tests.

- [ ] **Step 6: Hand off for commit**

Run `go build ./... && go vet ./...` and `detect_changes()`. Hand off: the five pgdialect source files and three test files. Suggested message: `feat(pgdialect): bind arrays natively, hstore and ranges as text`.

---

### Task 5: DB.build, executor options, result formats and the execution path

**Files:**
- Create: `query_build.go`, `result_format.go`, `result_format_test.go`
- Modify: `db.go` (options, `noCopyState`, `Exec`, `Query`, `QueryRow`, remove `format`)
- Modify: `hook.go` (`QueryEvent`, `beforeQuery`)
- Modify: `query_base.go:550-614` (`scan`, `_scan`, `exec`)
- Modify: `query_select.go` (`Rows`, `Exec`, `scanResult`, `Count`, `selectExists`), `query_insert.go:588-640`, `query_update.go:495-560`, `query_delete.go:309-370`, `query_raw.go:48-80`
- Modify: `db_test.go`
- Create: `bind_test.go` (root)

**Interfaces:**
- Consumes: `schema.NewArgList`, `QueryGen.WithArgList`, `ArgList.Args/Err`.
- Produces:
  - `const maxParams = 65535`; `var ErrTooManyParams error`
  - `func (db *DB) build(app schema.QueryAppender) (sql string, args []any, err error)`
  - `func (q *baseQuery) build(app schema.QueryAppender) (string, []any, error)`
  - `func (db *DB) failBuild(ctx context.Context, iquery Query, model Model, err error) error` — runs the query hooks with an empty statement and returns err.
  - `func (db *DB) queryArgs(args []any) []any` — `[execMode?, resultFormats, args...]`
  - `func (db *DB) execArgs(args []any) []any` — `[execMode?, args...]`
  - `func WithQueryExecMode(mode pgx.QueryExecMode) DBOption`, `func WithTextResultTypes(oids ...uint32) DBOption`
  - `func textResultFormats() pgx.QueryResultFormatsByOID`
  - `QueryEvent{DB, IQuery, Query, QueryArgs, Model, StartTime, Result, Err, Stash}`; `func (db *DB) beforeQuery(ctx, iquery Query, query string, args []any, model Model)`
  - `func (q *baseQuery) scan(ctx, iquery Query, query string, args []any, model Model, hasDest bool)`, `_scan` likewise, `func (q *baseQuery) exec(ctx, iquery Query, query string, args []any)`
  - Test helper in `bind_test.go`: `func boundArgs(c call) []any` strips leading pgx option values.

- [ ] **Step 1: Run impact analysis**

Run `impact` on `scan`, `_scan`, `exec` (baseQuery), `beforeQuery`, `QueryEvent`, `DB.Exec`, `DB.Query`, `DB.QueryRow`, `format`, `scanOrExec` (each builder), `Rows`, `scanResult`, `Count`, `selectExists`. Report; several will be HIGH since every execution flows through them.

- [ ] **Step 2: Write the failing tests**

Create `result_format_test.go` (package `pgcrud`, internal):

```go
package pgcrud

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

func TestTextResultFormats(t *testing.T) {
	formats := textResultFormats()

	t.Run("arrays are text", func(t *testing.T) {
		require.Equal(t, pgx.TextFormatCode, formats[pgtype.Int4ArrayOID])
		require.Equal(t, pgx.TextFormatCode, formats[pgtype.TextArrayOID])
		require.Equal(t, pgx.TextFormatCode, formats[pgtype.TimestamptzArrayOID])
	})

	t.Run("ranges and multiranges are text", func(t *testing.T) {
		require.Equal(t, pgx.TextFormatCode, formats[pgtype.TstzrangeOID])
		require.Equal(t, pgx.TextFormatCode, formats[pgtype.Int8rangeOID])
		require.Equal(t, pgx.TextFormatCode, formats[pgtype.Int4multirangeOID])
	})

	t.Run("scalars are not listed", func(t *testing.T) {
		_, ok := formats[pgtype.Int8OID]
		require.False(t, ok)
		_, ok = formats[pgtype.TextOID]
		require.False(t, ok)
		_, ok = formats[pgtype.JSONBOID]
		require.False(t, ok)
		_, ok = formats[pgtype.ByteaOID]
		require.False(t, ok)
	})

	t.Run("every listed OID has an array, range or multirange codec", func(t *testing.T) {
		m := pgtype.NewMap()
		for oid := range formats {
			typ, ok := m.TypeForOID(oid)
			require.True(t, ok, oid)
			switch typ.Codec.(type) {
			case *pgtype.ArrayCodec, *pgtype.RangeCodec, *pgtype.MultirangeCodec:
			default:
				t.Fatalf("oid %d has codec %T", oid, typ.Codec)
			}
		}
	})
}
```

Create `bind_test.go` (package `pgcrud_test`):

```go
package pgcrud_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/piprim/pgcrud"
)

// boundArgs returns the values of a recorded call after pgx's option values.
func boundArgs(c call) []any {
	args := c.args
	for len(args) > 0 {
		switch args[0].(type) {
		case pgx.QueryExecMode, pgx.QueryResultFormats, pgx.QueryResultFormatsByOID:
			args = args[1:]
		default:
			return args
		}
	}
	return args
}

// options returns the pgx option values that precede the bound values.
func options(c call) []any {
	return c.args[:len(c.args)-len(boundArgs(c))]
}

func TestBoundExecution(t *testing.T) {
	ctx := context.Background()

	t.Run("select sends $n SQL, the result-format map, then the values", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("id", "name"), []any{int64(1), "a"})}
		var u User
		require.NoError(t, withExec(exec).NewSelect().Model(&u).Where("id = ?", 1).Where("name = ?", "a").Scan(ctx))
		c := exec.calls[0]
		require.Equal(t, `SELECT "user"."id", "user"."name" FROM "users" AS "user" WHERE (id = $1) AND (name = $2)`, c.sql)
		require.Len(t, options(c), 1)
		require.IsType(t, pgx.QueryResultFormatsByOID{}, options(c)[0])
		require.Equal(t, []any{1, "a"}, boundArgs(c))
	})

	t.Run("insert binds model fields through Exec without result formats", func(t *testing.T) {
		exec := &fakeExecutor{tag: pgconn.NewCommandTag("INSERT 0 1")}
		_, err := withExec(exec).NewInsert().Model(&User{ID: 3, Name: "c"}).Exec(ctx)
		require.NoError(t, err)
		c := exec.calls[0]
		require.Equal(t, "Exec", c.method)
		require.Equal(t, `INSERT INTO "users" ("id", "name") VALUES ($1, $2)`, c.sql)
		require.Empty(t, options(c))
		require.Equal(t, []any{int64(3), "c"}, boundArgs(c))
	})

	t.Run("update and delete bind where values", func(t *testing.T) {
		exec := &fakeExecutor{tag: pgconn.NewCommandTag("UPDATE 1")}
		_, err := withExec(exec).NewUpdate().Model((*User)(nil)).Set("name = ?", "z").Where("id = ?", 9).Exec(ctx)
		require.NoError(t, err)
		require.Equal(t, `UPDATE "users" AS "user" SET name = $1 WHERE (id = $2)`, exec.calls[0].sql)
		require.Equal(t, []any{"z", 9}, boundArgs(exec.calls[0]))

		_, err = withExec(exec).NewDelete().Model((*User)(nil)).Where("id = ?", 9).Exec(ctx)
		require.NoError(t, err)
		require.Equal(t, []any{9}, boundArgs(exec.calls[1]))
	})

	t.Run("Count and Exists bind through QueryRow", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("count"), []any{int64(2)})}
		n, err := withExec(exec).NewSelect().Model((*User)(nil)).Where("id > ?", 5).Count(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(2), n)
		require.Equal(t, "QueryRow", exec.calls[0].method)
		require.Equal(t, []any{5}, boundArgs(exec.calls[0]))
		require.IsType(t, pgx.QueryResultFormatsByOID{}, options(exec.calls[0])[0])
	})

	t.Run("Rows binds too", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("id", "name"))}
		rows, err := withExec(exec).NewSelect().Model((*User)(nil)).Where("id = ?", 4).Rows(ctx)
		require.NoError(t, err)
		rows.Close()
		require.Equal(t, []any{4}, boundArgs(exec.calls[0]))
	})

	t.Run("WithQueryExecMode puts the mode before the result formats", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("id", "name"))}
		db := pgcrud.New(nil,
			pgcrud.WithExecutorResolver(func(context.Context) pgcrud.DBExecutor { return exec }),
			pgcrud.WithQueryExecMode(pgx.QueryExecModeCacheDescribe))
		var us []User
		require.NoError(t, db.NewSelect().Model(&us).Scan(ctx))
		opts := options(exec.calls[0])
		require.Len(t, opts, 2)
		require.Equal(t, pgx.QueryExecModeCacheDescribe, opts[0])
		require.IsType(t, pgx.QueryResultFormatsByOID{}, opts[1])

		_, err := db.NewDelete().Model((*User)(nil)).Where("id = ?", 1).Exec(ctx)
		require.NoError(t, err)
		require.Equal(t, []any{pgx.QueryExecModeCacheDescribe}, options(exec.calls[1]))
	})

	t.Run("WithTextResultTypes extends the map", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("id", "name"))}
		db := pgcrud.New(nil,
			pgcrud.WithExecutorResolver(func(context.Context) pgcrud.DBExecutor { return exec }),
			pgcrud.WithTextResultTypes(99999))
		var us []User
		require.NoError(t, db.NewSelect().Model(&us).Scan(ctx))
		formats := options(exec.calls[0])[0].(pgx.QueryResultFormatsByOID)
		require.Equal(t, pgx.TextFormatCode, formats[99999])
	})

	t.Run("the query hook sees the SQL and the bound values without options", func(t *testing.T) {
		hook := &recordingHook{}
		exec := &fakeExecutor{tag: pgconn.NewCommandTag("DELETE 1")}
		db := withExec(exec).WithQueryHook(hook)
		_, err := db.NewDelete().Model((*User)(nil)).Where("id = ?", 7).Exec(ctx)
		require.NoError(t, err)
		require.Equal(t, `DELETE FROM "users" AS "user" WHERE (id = $1)`, hook.last.Query)
		require.Equal(t, []any{7}, hook.last.QueryArgs)
	})
}

func TestTooManyParams(t *testing.T) {
	ctx := context.Background()

	type Wide struct {
		A, B, C, D, E, F, G int64
	}
	rows := func(n int) []Wide {
		out := make([]Wide, n)
		for i := range out {
			out[i] = Wide{A: int64(i)}
		}
		return out
	}

	t.Run("65535 bound values execute", func(t *testing.T) {
		exec := &fakeExecutor{tag: pgconn.NewCommandTag("INSERT 0 9362")}
		rs := rows(9362) // 9362 * 7 = 65534
		_, err := withExec(exec).NewInsert().Model(&rs).Exec(ctx)
		require.NoError(t, err)
		require.Len(t, boundArgs(exec.calls[0]), 65534)
	})

	t.Run("65536 bound values fail before the executor is called", func(t *testing.T) {
		exec := &fakeExecutor{}
		rs := rows(9363) // 9363 * 7 = 65541
		_, err := withExec(exec).NewInsert().Model(&rs).Exec(ctx)
		require.ErrorIs(t, err, pgcrud.ErrTooManyParams)
		require.Contains(t, err.Error(), "65541")
		require.Empty(t, exec.calls)
	})

	t.Run("the query hook sees ErrTooManyParams", func(t *testing.T) {
		hook := &recordingHook{}
		db := withExec(&fakeExecutor{}).WithQueryHook(hook)
		rs := rows(9363)
		_, err := db.NewInsert().Model(&rs).Exec(ctx)
		require.ErrorIs(t, err, pgcrud.ErrTooManyParams)
		require.ErrorIs(t, hook.last.Err, pgcrud.ErrTooManyParams)
	})
}
```

The exact 65535 boundary is pinned by the pair 65534 (passes) and 65541 (fails) with a seven-column row; the limit constant itself is asserted by the error text listing the count.

Update `db_test.go`:

- `TestNew` "builds SQL without a pool": expected string becomes `... WHERE (id = $1)`.
- `TestExecutorResolver` subtest "query is sent with the simple protocol as its only argument" is renamed "query is sent with the result-format map and no values" and asserts:

```go
	t.Run("query is sent with the result-format map and no values", func(t *testing.T) {
		require.Len(t, exec.calls, 1)
		require.Equal(t, "Query", exec.calls[0].method)
		require.Len(t, exec.calls[0].args, 1)
		require.IsType(t, pgx.QueryResultFormatsByOID{}, exec.calls[0].args[0])
		require.Equal(t, `SELECT "user"."id", "user"."name" FROM "users" AS "user"`, exec.calls[0].sql)
	})
```

- `TestExec` "returns the executor's command tag": replace the `QueryExecModeSimpleProtocol` assertion with `require.Empty(t, exec.calls[0].args)`.
- `TestExec` "DB.Exec formats placeholders" becomes "DB.Exec binds placeholders":

```go
	t.Run("DB.Exec binds placeholders", func(t *testing.T) {
		exec := &fakeExecutor{tag: pgconn.NewCommandTag("UPDATE 1")}
		res, err := withExec(exec).Exec(ctx, "UPDATE users SET name = ? WHERE id = ?", "x", 5)
		require.NoError(t, err)
		require.Equal(t, int64(1), res.RowsAffected())
		require.Equal(t, "UPDATE users SET name = $1 WHERE id = $2", exec.calls[0].sql)
		require.Equal(t, []any{"x", 5}, exec.calls[0].args)
	})
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test . -run 'TestTextResultFormats|TestBoundExecution|TestTooManyParams|TestNew|TestExec|TestExecutorResolver'`
Expected: compile errors (`textResultFormats`, `WithQueryExecMode`, `WithTextResultTypes`, `ErrTooManyParams` undefined).

- [ ] **Step 4: Implement the build and option layer**

Create `result_format.go`:

```go
package pgcrud

import (
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// lastBuiltinOID is Postgres's FirstNormalObjectId: every built-in type has a
// smaller OID, and pgx registers only built-in types by default.
const lastBuiltinOID = 16384

// textResultFormats returns the result-format overrides pgcrud passes on
// every Query and QueryRow. Every array, range and multirange type pgx knows
// is requested in text, because the library parses those from their text
// form; pgx would otherwise hand the scanners binary wire bytes.
func textResultFormats() pgx.QueryResultFormatsByOID {
	m := pgtype.NewMap()
	formats := make(pgx.QueryResultFormatsByOID)
	for oid := uint32(1); oid < lastBuiltinOID; oid++ {
		typ, ok := m.TypeForOID(oid)
		if !ok {
			continue
		}
		switch typ.Codec.(type) {
		case *pgtype.ArrayCodec, *pgtype.RangeCodec, *pgtype.MultirangeCodec:
			formats[oid] = pgx.TextFormatCode
		}
	}
	return formats
}
```

Create `query_build.go`:

```go
package pgcrud

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/piprim/pgcrud/internal"
	"github.com/piprim/pgcrud/schema"
)

// maxParams is the number of bind parameters Postgres accepts in one statement.
const maxParams = 65535

// ErrTooManyParams is returned when a statement binds more than 65535 values.
// Chunk the rows, or use pgx.CopyFrom for bulk loads.
var ErrTooManyParams = errors.New("pgcrud: too many bound parameters")

// build renders app into SQL with $n placeholders and returns the values bound
// to them, in order. It fails before anything is sent when a value cannot be
// encoded or the parameter limit is exceeded.
func (db *DB) build(app schema.QueryAppender) (string, []any, error) {
	list := schema.NewArgList()
	b, err := app.AppendQuery(db.gen.WithArgList(list), db.makeQueryBytes())
	if err != nil {
		return "", nil, err
	}
	if err := list.Err(); err != nil {
		return "", nil, err
	}
	args := list.Args()
	if len(args) > maxParams {
		return "", nil, fmt.Errorf("pgcrud: query binds %d parameters, Postgres allows at most %d: %w",
			len(args), maxParams, ErrTooManyParams)
	}
	return internal.String(b), args, nil
}

func (q *baseQuery) build(app schema.QueryAppender) (string, []any, error) {
	return q.db.build(app)
}

// failBuild reports a render error to the query hooks and returns it. The
// hooks see an empty Query and no args because nothing was sent.
func (db *DB) failBuild(ctx context.Context, iquery Query, model Model, err error) error {
	ctx, event := db.beforeQuery(ctx, iquery, "", nil, model)
	db.afterQuery(ctx, event, pgconn.CommandTag{}, err)
	return err
}

// queryArgs returns the arguments of an executor Query or QueryRow call: the
// pgx option values first, then the bound values.
func (db *DB) queryArgs(args []any) []any {
	out := make([]any, 0, len(args)+2)
	if db.hasExecMode {
		out = append(out, db.execMode)
	}
	out = append(out, db.resultFormats)
	return append(out, args...)
}

// execArgs returns the arguments of an executor Exec call. Exec takes no
// result formats, so only the exec mode precedes the values.
func (db *DB) execArgs(args []any) []any {
	if !db.hasExecMode {
		return args
	}
	out := make([]any, 0, len(args)+1)
	out = append(out, db.execMode)
	return append(out, args...)
}
```

In `db.go`:

```go
// noCopyState contains DB fields that must not be copied on clone().
type noCopyState struct {
	pool     *pgxpool.Pool
	resolver ExecutorResolver
	dialect  schema.Dialect

	execMode      pgx.QueryExecMode
	hasExecMode   bool
	resultFormats pgx.QueryResultFormatsByOID

	flags internal.Flag
}
```

In `New`, set `resultFormats: textResultFormats(),` inside the `noCopyState` literal.

Add the options after `WithQueryHook`:

```go
// WithQueryExecMode overrides pgx's default execution mode for every query.
// Without it pgx's connection default applies, QueryExecModeCacheStatement.
func WithQueryExecMode(mode pgx.QueryExecMode) DBOption {
	return func(db *DB) {
		db.execMode = mode
		db.hasExecMode = true
	}
}

// WithTextResultTypes asks Postgres to return the given column types in text
// format. Use it for types the application registered on the pool with a
// codec that prefers binary but that pgcrud scans with its text parsers.
func WithTextResultTypes(oids ...uint32) DBOption {
	return func(db *DB) {
		for _, oid := range oids {
			db.resultFormats[oid] = pgx.TextFormatCode
		}
	}
}
```

Replace `Exec`, `Query`, `QueryRow` and delete `format`:

```go
// Exec translates ? placeholders to $n, binds args and executes query without
// returning rows. It is always treated as a write for WithTxRequiredForWrites.
func (db *DB) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	sql, bound, err := db.build(NewRawQuery(db, query, args...))
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	ctx, event := db.beforeQuery(ctx, nil, sql, bound, nil)

	var res pgconn.CommandTag
	exec, err := db.writeExecutor(ctx)
	if err == nil {
		res, err = exec.Exec(ctx, sql, db.execArgs(bound)...)
	}

	db.afterQuery(ctx, event, res, err)
	return res, err
}

// Query translates ? placeholders to $n, binds args and executes query,
// returning pgx rows. The caller must close the rows.
//
// On error pgx may return a non-nil, already-closed pgx.Rows alongside the
// error (bun returned nil rows), so check the error before using the rows.
func (db *DB) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	sql, bound, err := db.build(NewRawQuery(db, query, args...))
	if err != nil {
		return nil, err
	}
	ctx, event := db.beforeQuery(ctx, nil, sql, bound, nil)

	var rows pgx.Rows
	exec, err := db.Executor(ctx)
	if err == nil {
		rows, err = exec.Query(ctx, sql, db.queryArgs(bound)...)
	}

	db.afterQuery(ctx, event, pgconn.CommandTag{}, err)
	return rows, err
}

// QueryRow translates ? placeholders to $n, binds args and executes query,
// returning a pgx row. Errors surface from the row's Scan; the query hook
// sees a nil error.
func (db *DB) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	sql, bound, err := db.build(NewRawQuery(db, query, args...))
	if err != nil {
		return errRow{err: db.failBuild(ctx, nil, nil, err)}
	}
	ctx, event := db.beforeQuery(ctx, nil, sql, bound, nil)

	exec, err := db.Executor(ctx)
	if err != nil {
		db.afterQuery(ctx, event, pgconn.CommandTag{}, err)
		return errRow{err: err}
	}

	row := exec.QueryRow(ctx, sql, db.queryArgs(bound)...)
	db.afterQuery(ctx, event, pgconn.CommandTag{}, nil)
	return row
}
```

In `hook.go`:

```go
// QueryEvent captures information about a query execution for hooks.
type QueryEvent struct {
	DB *DB

	IQuery Query
	// Query is the SQL sent to Postgres, with $n placeholders.
	Query string
	// QueryArgs are the values bound to the placeholders, in order. pgx option
	// values are not included.
	QueryArgs []any
	Model     Model

	StartTime time.Time
	Result    pgconn.CommandTag
	Err       error

	Stash map[any]any
}
```

```go
func (db *DB) beforeQuery(
	ctx context.Context,
	iquery Query,
	query string,
	args []any,
	model Model,
) (context.Context, *QueryEvent) {
	if len(db.queryHooks) == 0 {
		return ctx, nil
	}

	event := &QueryEvent{
		DB: db,

		Model:     model,
		IQuery:    iquery,
		Query:     query,
		QueryArgs: args,

		StartTime: time.Now(),
	}

	for _, hook := range db.queryHooks {
		ctx = hook.BeforeQuery(ctx, event)
	}

	return ctx, event
}
```

- [ ] **Step 5: Route every execution site through build**

`query_base.go`:

```go
func (q *baseQuery) scan(
	ctx context.Context,
	iquery Query,
	query string,
	args []any,
	model Model,
	hasDest bool,
) (pgconn.CommandTag, error) {
	ctx, event := q.db.beforeQuery(ctx, iquery, query, args, q.model)
	res, err := q._scan(ctx, iquery, query, args, model, hasDest)
	q.db.afterQuery(ctx, event, res, err)
	return res, err
}

func (q *baseQuery) _scan(
	ctx context.Context,
	iquery Query,
	query string,
	args []any,
	model Model,
	hasDest bool,
) (pgconn.CommandTag, error) {
	exec, err := q.resolveExecutor(ctx, iquery, query)
	if err != nil {
		return pgconn.CommandTag{}, err
	}

	rows, err := exec.Query(ctx, query, q.db.queryArgs(args)...)
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
	args []any,
) (pgconn.CommandTag, error) {
	ctx, event := q.db.beforeQuery(ctx, iquery, query, args, q.model)

	var res pgconn.CommandTag
	exec, err := q.resolveExecutor(ctx, iquery, query)
	if err == nil {
		res, err = exec.Exec(ctx, query, q.db.execArgs(args)...)
	}

	q.db.afterQuery(ctx, event, res, err)
	return res, err
}
```

In each of `SelectQuery.Rows`, `SelectQuery.Exec`, `SelectQuery.scanResult`, `InsertQuery.scanOrExec`, `UpdateQuery.scanOrExec`, `DeleteQuery.scanOrExec`, `RawQuery.scanOrExec`, replace the pair

```go
	queryBytes, err := q.AppendQuery(q.db.gen, q.db.makeQueryBytes())
	if err != nil {
		return ..., err
	}
	...
	query := internal.String(queryBytes)
```

with

```go
	query, args, err := q.build(q)
	if err != nil {
		return ..., q.db.failBuild(ctx, q, q.model, err)
	}
```

so that a render error (a marshal failure or `ErrTooManyParams`) reaches the query hooks exactly as `ErrTxRequired` does, keeping the statements that were between them (the `useScan` computation in Insert, Update and Delete must stay after `build`, because rendering can add returning fields). Then pass `args` on: `q.scan(ctx, q, query, args, model, hasDest)` and `q.exec(ctx, q, query, args)`. In `RawQuery.scanOrExec`, `query := q.db.format(q.query, q.args)` becomes `query, args, err := q.build(q)` with the error returned.

`SelectQuery.Rows`:

```go
	query, args, err := q.build(q)
	if err != nil {
		return nil, q.db.failBuild(ctx, q, q.model, err)
	}

	ctx, event := q.db.beforeQuery(ctx, q, query, args, q.model)

	var rows pgx.Rows
	exec, err := q.resolveExecutor(ctx, q, query)
	if err == nil {
		rows, err = exec.Query(ctx, query, q.db.queryArgs(args)...)
	}
```

`SelectQuery.Count`:

```go
	qq := countQuery{q}

	query, args, err := q.build(qq)
	if err != nil {
		return 0, q.db.failBuild(ctx, qq, q.model, err)
	}
	ctx, event := q.db.beforeQuery(ctx, qq, query, args, q.model)

	var num int64
	exec, err := q.resolveExecutor(ctx, qq, query)
	if err == nil {
		err = exec.QueryRow(ctx, query, q.db.queryArgs(args)...).Scan(&num)
	}
```

`SelectQuery.selectExists` is the same shape with `selectExistsQuery{q}` and `&exists`.

Remove the `internal` import from any file that no longer uses it (`go build` reports).

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test . -run 'TestTextResultFormats|TestBoundExecution|TestTooManyParams|TestNew|TestExec|TestExecutorResolver|TestScan|TestTxRequiredForWrites|TestScanAndCount|TestQueryHooks'`
Expected: PASS. `TestQuery` (snapshots) and `TestListAndTuple` still fail; Task 6 owns them.

- [ ] **Step 7: Hand off for commit**

Run `go build ./... && go vet ./...` and `detect_changes()`. Hand off: `query_build.go`, `result_format.go`, `result_format_test.go`, `bind_test.go`, `db.go`, `hook.go`, `query_base.go`, `query_select.go`, `query_insert.go`, `query_update.go`, `query_delete.go`, `query_raw.go`, `db_test.go`. Suggested message: `feat: execute every query with bound $n parameters`.

---

### Task 6: Build, Args and String on the builders; snapshots with args

**Files:**
- Modify: `query_select.go:1161`, `query_insert.go:670`, `query_update.go:594`, `query_delete.go:392`, `query_raw.go:99` (`String`), `query_values.go` (add)
- Modify: `query_test.go:1614-1631`, `snapshot_test.go` (unchanged API, documented format), `testdata/snapshots/*`
- Modify: `list_tuple_test.go`
- Modify: `bind_test.go`

**Interfaces:**
- Produces on each of `SelectQuery`, `InsertQuery`, `UpdateQuery`, `DeleteQuery`, `RawQuery`, `ValuesQuery`:
  - `func (q *T) Build() (string, []any, error)`
  - `func (q *T) Args() []any` — panics on a render error, like `String`.
  - `func (q *T) String() string` — the `$n` SQL.

- [ ] **Step 1: Run impact analysis**

Run `impact` on each `String` method. Report.

- [ ] **Step 2: Write the failing tests**

Append to `bind_test.go`:

```go
func TestBuildStringArgs(t *testing.T) {
	db := pgcrud.New(nil)

	t.Run("Build returns SQL and args that agree", func(t *testing.T) {
		q := db.NewSelect().Model((*User)(nil)).Where("id = ?", 1).Where("name = ?", "a")
		sql, args, err := q.Build()
		require.NoError(t, err)
		require.Equal(t, `SELECT "user"."id", "user"."name" FROM "users" AS "user" WHERE (id = $1) AND (name = $2)`, sql)
		require.Equal(t, []any{1, "a"}, args)
	})

	t.Run("String twice is identical and Args matches", func(t *testing.T) {
		q := db.NewUpdate().Model((*User)(nil)).Set("name = ?", "n").Where("id = ?", 2)
		require.Equal(t, q.String(), q.String())
		require.Equal(t, []any{"n", 2}, q.Args())
		require.Equal(t, []any{"n", 2}, q.Args())
	})

	t.Run("every builder has Build", func(t *testing.T) {
		u := &User{ID: 1, Name: "x"}
		for name, q := range map[string]interface{ Build() (string, []any, error) }{
			"select": db.NewSelect().Model(u).WherePK(),
			"insert": db.NewInsert().Model(u),
			"update": db.NewUpdate().Model(u).WherePK(),
			"delete": db.NewDelete().Model(u).WherePK(),
			"raw":    db.NewRaw("SELECT ?", 1),
			"values": db.NewValues(u),
		} {
			t.Run(name, func(t *testing.T) {
				_, args, err := q.Build()
				require.NoError(t, err)
				require.NotEmpty(t, args)
			})
		}
	})

	t.Run("a nested select is renumbered inside the outer statement", func(t *testing.T) {
		sub := db.NewSelect().Model((*User)(nil)).Column("id").Where("name = ?", "x")
		require.Equal(t, `SELECT "user"."id" FROM "users" AS "user" WHERE (name = $1)`, sub.String())
		outer := db.NewSelect().Model((*User)(nil)).Where("id > ?", 0).Where("id IN (?)", sub)
		sql, args, err := outer.Build()
		require.NoError(t, err)
		require.Equal(t, `SELECT "user"."id", "user"."name" FROM "users" AS "user" WHERE (id > $1) AND (id IN (SELECT "user"."id" FROM "users" AS "user" WHERE (name = $2)))`, sql)
		require.Equal(t, []any{0, "x"}, args)
	})

	t.Run("List and Tuple bind each element", func(t *testing.T) {
		sql, args, err := db.NewSelect().Model((*User)(nil)).
			Where("id IN (?)", pgcrud.List([]int64{1, 2, 3})).
			Where("(id, name) IN (?)", pgcrud.Tuple([][]any{{1, "a"}, {2, "b"}})).
			Build()
		require.NoError(t, err)
		require.Contains(t, sql, "IN ($1, $2, $3)")
		require.Contains(t, sql, "IN ((($4, $5), ($6, $7)))") // Tuple adds its own parentheses
		require.Equal(t, []any{int64(1), int64(2), int64(3), int64(1), "a", int64(2), "b"}, args)
	})

	t.Run("Values keeps its casts around bound values", func(t *testing.T) {
		sql, args, err := db.NewValues(&User{ID: 42, Name: "hello"}).Build()
		require.NoError(t, err)
		require.Equal(t, `VALUES ($1::BIGINT, $2::VARCHAR)`, sql)
		require.Equal(t, []any{int64(42), "hello"}, args)
	})
}
```

Replace the body of `TestListAndTuple` in `list_tuple_test.go` so each case renders with a real generator and checks SQL plus args. The table gains a `args []any` column; `want` becomes the `$n` form:

```go
func TestListAndTuple(t *testing.T) {
	db := New(nil)

	tests := []struct {
		name string
		q    schema.QueryAppender
		want string
		args []any
	}{
		{"List([]int)", List([]int{1, 2, 3}), "$1, $2, $3", []any{int64(1), int64(2), int64(3)}},
		{"List([]string)", List([]string{"foo", "bar"}), "$1, $2", []any{"foo", "bar"}},
		{"List([][]byte)", List([][]byte{[]byte("hello"), []byte("world")}), "$1, $2", []any{[]byte("hello"), []byte("world")}},
		{"List([][16]byte)", List([][16]byte{{0x6b, 0xa7}}), "$1", []any{append([]byte{0x6b, 0xa7}, make([]byte, 14)...)}},
		{"List([][]int) - no recursion, inner slices are JSON", List([][]int{{1, 2}, {3, 4}}), "$1, $2", []any{[]byte("[1,2]"), []byte("[3,4]")}},
		{"List([]int) empty", List([]int{}), "NULL", []any{}},
		{"Tuple([]int)", Tuple([]int{1, 2, 3}), "($1, $2, $3)", []any{int64(1), int64(2), int64(3)}},
		{"Tuple([][]int)", Tuple([][]int{{1, 2}, {3, 4}}), "(($1, $2), ($3, $4))", []any{int64(1), int64(2), int64(3), int64(4)}},
		{"Tuple(nil)", Tuple(nil), "(NULL)", []any{}},
		{"Tuple([]int) empty", Tuple([]int{}), "(NULL)", []any{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list := schema.NewArgList()
			got, err := tt.q.AppendQuery(db.QueryGen().WithArgList(list), nil)
			require.NoError(t, err)
			require.Equal(t, tt.want, string(got))
			require.Equal(t, tt.args, list.Args())
		})
	}
}
```

Keep any existing error cases of the old table (non-slice inputs) as they were, adding an empty `args` column.

Change the snapshot rendering in `query_test.go`. Replace the `timeRE` declaration and the `t.Run("pg", ...)` block with:

```go
	t.Run("pg", func(t *testing.T) {
		db := pgcrud.New(nil)
		for _, tt := range tests {
			t.Run(fmt.Sprintf("%d", tt.id), func(t *testing.T) {
				assertSnapshot(t, renderSnapshot(db, tt.query(db)))
			})
		}
	})
}

// renderSnapshot renders q as the SQL pgx would receive followed by a line
// listing the bound values. time.Time values are replaced by "[TIME]" because
// soft deletes bind time.Now().
func renderSnapshot(db *pgcrud.DB, q schema.QueryAppender) string {
	list := schema.NewArgList()
	sql, err := q.AppendQuery(db.QueryGen().WithArgList(list), nil)
	if err != nil {
		return err.Error()
	}
	if err := list.Err(); err != nil {
		return err.Error()
	}
	args := list.Args()
	for i, a := range args {
		if _, ok := a.(time.Time); ok {
			args[i] = "[TIME]"
		}
	}
	return string(sql) + "\n-- args: " + fmt.Sprintf("%#v", args)
}
```

Remove the `regexp` import from `query_test.go`.

In `snapshot_test.go` update the comment on `assertSnapshot` to: "Files hold the SQL, a newline, `-- args: <%#v of the bound values>` and one trailing newline."

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test . -run 'TestBuildStringArgs|TestListAndTuple|TestQuery'`
Expected: compile errors for `Build`/`Args` on the builders; `TestQuery` mismatches.

- [ ] **Step 4: Implement**

For each of the six builders, replace `String` and add `Build` and `Args`. `SelectQuery` shown; the others are identical with their receiver type, placed where `String` is today (`ValuesQuery` gets them after `AppendQuery`):

```go
// Build renders the query and returns the SQL with $n placeholders together
// with the values bound to them. The query must not be modified while
// rendering, so repeated calls return identical results.
func (q *SelectQuery) Build() (string, []any, error) {
	return q.db.build(q)
}

// String returns the SQL with $n placeholders. It panics on a render error.
func (q *SelectQuery) String() string {
	sql, _, err := q.Build()
	if err != nil {
		panic(err)
	}
	return sql
}

// Args returns the values bound to the placeholders of String. It panics on
// a render error.
func (q *SelectQuery) Args() []any {
	_, args, err := q.Build()
	if err != nil {
		panic(err)
	}
	return args
}
```

Regenerate the snapshots:

Run: `go test . -run TestQuery -update`

Then review the diff by eye:

Run: `git diff --stat testdata/snapshots | tail -1 && git diff testdata/snapshots | grep '^[-+][^-+]' | head -80`

Every removed line must differ from its added line only by literals turned into `$n`, and every file must end with a `-- args:` line. Cases 209 and 210 (soft delete) show `"[TIME]"` in the args. Spot-check by hand:

- `TestQuery-pg-0`: `VALUES ($1::BIGINT, $2::VARCHAR)` then `-- args: []interface {}{42, "hello"}`.
- `TestQuery-pg-20`: `INSERT INTO "models" ("id", "str") VALUES ($1, $2), ($3, $4)` then `-- args: []interface {}{42, "hello", 43, "world"}`.
- `TestQuery-pg-100`: `... IN (($1, $2), ($3, $4))`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./...`
Expected: everything PASS. This is the first fully green task since Task 2.

- [ ] **Step 6: Hand off for commit**

Run `go vet ./...` and `detect_changes()`. Hand off: the six builder files, `query_test.go`, `snapshot_test.go`, `list_tuple_test.go`, `bind_test.go`, `testdata/snapshots/*`. Suggested message: `feat: Build, Args and $n String on builders; snapshots record args`. Suggest the user commits Tasks 2 through 6 as one commit if they prefer every commit green.

---

### Task 7: raw SQL with $n passes its args through

**Files:**
- Modify: `query_raw.go:92-96` (`AppendQuery`)
- Modify: `bind_test.go`

**Interfaces:**
- Consumes: `QueryGen.BindArgs` from Task 1.

- [ ] **Step 1: Run impact analysis**

Run `impact` on `RawQuery.AppendQuery`. Report.

- [ ] **Step 2: Write the failing tests**

Append to `bind_test.go`:

```go
func TestRawPlaceholders(t *testing.T) {
	ctx := context.Background()

	t.Run("raw with ? renumbers and binds", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("id", "name"), []any{int64(1), "a"})}
		var u User
		require.NoError(t, withExec(exec).NewRaw("SELECT id, name FROM users WHERE id = ? AND name = ?", 1, "a").Scan(ctx, &u))
		require.Equal(t, "SELECT id, name FROM users WHERE id = $1 AND name = $2", exec.calls[0].sql)
		require.Equal(t, []any{1, "a"}, boundArgs(exec.calls[0]))
	})

	t.Run("raw with $n and no ? passes args through untouched", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("id", "name"), []any{int64(1), "a"})}
		var u User
		require.NoError(t, withExec(exec).NewRaw("SELECT id, name FROM users WHERE id = $1 AND name = $2", 1, "a").Scan(ctx, &u))
		require.Equal(t, "SELECT id, name FROM users WHERE id = $1 AND name = $2", exec.calls[0].sql)
		require.Equal(t, []any{1, "a"}, boundArgs(exec.calls[0]))
	})

	t.Run("raw with ? and no args leaves the ? in place", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("id", "name"))}
		var us []User
		require.NoError(t, withExec(exec).NewRaw("SELECT id, name FROM users WHERE data ? 'k'").Scan(ctx, &us))
		require.Equal(t, "SELECT id, name FROM users WHERE data ? 'k'", exec.calls[0].sql)
		require.Empty(t, boundArgs(exec.calls[0]))
	})

	t.Run("DB.Query and DB.QueryRow take the same path", func(t *testing.T) {
		exec := &fakeExecutor{rows: newFakeRows(cols("id"), []any{int64(1)})}
		db := withExec(exec)
		rows, err := db.Query(ctx, "SELECT id FROM users WHERE id = $1", 1)
		require.NoError(t, err)
		rows.Close()
		require.Equal(t, []any{1}, boundArgs(exec.calls[0]))

		var id int64
		require.NoError(t, db.QueryRow(ctx, "SELECT id FROM users WHERE id = ?", 2).Scan(&id))
		require.Equal(t, "SELECT id FROM users WHERE id = $1", exec.calls[1].sql)
		require.Equal(t, []any{2}, boundArgs(exec.calls[1]))
	})

	t.Run("raw String shows $n", func(t *testing.T) {
		q := pgcrud.New(nil).NewRaw("SELECT ? + ?", 1, 2)
		require.Equal(t, "SELECT $1 + $2", q.String())
		require.Equal(t, []any{1, 2}, q.Args())
	})
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test . -run TestRawPlaceholders`
Expected: the `$n` passthrough case fails (args dropped, `boundArgs` empty). The others pass already.

- [ ] **Step 4: Implement**

In `query_raw.go`:

```go
// AppendQuery renders the raw SQL. Question marks are translated to $n and
// their args bound. A query with no question mark at all is pgx-style SQL
// that already carries $n placeholders: it is written unchanged and its args
// are passed through in order. Mixing both styles is not supported.
func (q *RawQuery) AppendQuery(gen schema.QueryGen, b []byte) ([]byte, error) {
	b = appendComment(b, q.comment)

	if len(q.args) > 0 && strings.IndexByte(q.query, '?') == -1 {
		gen.BindArgs(q.args...)
		return append(b, q.query...), nil
	}

	return gen.AppendQuery(b, q.query, q.args...), nil
}
```

Add `"strings"` to the imports.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test . -run TestRawPlaceholders`
Expected: PASS.

- [ ] **Step 6: Hand off for commit**

Run `go build ./... && go vet ./... && go test ./...` and `detect_changes()`. Hand off: `query_raw.go`, `bind_test.go`. Suggested message: `feat(raw): pass args through for $n SQL`.

---

### Task 8: soft-delete expressions and template texts

**Files:**
- Modify: `query_delete.go:210-214`, `query_delete.go:286-296` (`softDeleteSet`)
- Modify: `query_base.go:829-831` (soft-delete compare in `appendWhere`)
- Modify: `relation_join.go:294-297` (`appendSoftDelete`)
- Modify: `query_select.go:770-774` (template column text)
- Modify: `bind_test.go`

**Interfaces:**
- Produces: `func (q *DeleteQuery) softDeleteSet(gen schema.QueryGen) string` returning `"<alias.>deleted_at = ?"`; the time is passed as the `Set` arg.

- [ ] **Step 1: Run impact analysis**

Run `impact` on `softDeleteSet`, `appendSoftDelete`, `DeleteQuery.AppendQuery`, `whereBaseQuery.appendWhere` (or the enclosing function name at `query_base.go:815`; confirm with `context({name: "appendWhere"})`). Report.

- [ ] **Step 2: Write the failing tests**

Append to `bind_test.go`:

```go
type SoftUser struct {
	pgcrud.BaseModel `bun:"table:soft_users,alias:su"`

	ID        int64     `bun:",pk"`
	Name      string
	DeletedAt time.Time `bun:",soft_delete"`
}

func TestSoftDeleteBinds(t *testing.T) {
	db := pgcrud.New(nil)

	t.Run("delete becomes an update that binds the timestamp", func(t *testing.T) {
		sql, args, err := db.NewDelete().Model(&SoftUser{}).Where("id = ?", 1).Build()
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(sql, `UPDATE "soft_users" AS "su" SET "deleted_at" = $1 WHERE`), sql)
		require.Contains(t, sql, `(id = $2)`)
		require.Contains(t, sql, `"su"."deleted_at" = $3`)
		require.Len(t, args, 3)
		_, isTime := args[0].(time.Time)
		require.True(t, isTime)
		require.Equal(t, 1, args[1])
		require.Equal(t, time.Time{}, args[2])
	})

	t.Run("select on a non-nullable soft-delete column binds the zero time", func(t *testing.T) {
		sql, args, err := db.NewSelect().Model((*SoftUser)(nil)).Build()
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(sql, `SELECT "su"."id", "su"."name", "su"."deleted_at" FROM "soft_users" AS "su" WHERE`), sql)
		require.Contains(t, sql, `"su"."deleted_at" = $1`)
		require.Equal(t, []any{time.Time{}}, args)
	})

	t.Run("WhereDeleted binds the zero time with !=", func(t *testing.T) {
		sql, _, err := db.NewSelect().Model((*SoftUser)(nil)).WhereDeleted().Build()
		require.NoError(t, err)
		require.Contains(t, sql, `"su"."deleted_at" != $1`)
	})
}
```

Add `"strings"` and `"time"` to the imports of `bind_test.go`.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test . -run TestSoftDeleteBinds`
Expected: FAIL. Today the timestamps render as literals (`= '0001-01-01 00:00:00+00:00'`), so the `$n` expectations fail and arg counts are off.

- [ ] **Step 4: Implement**

`query_delete.go`, inside `AppendQuery`:

```go
		upd.Set(q.softDeleteSet(gen), now)
```

and

```go
// softDeleteSet returns the SET expression that marks a row deleted, with a
// placeholder for the timestamp, which the caller passes as the Set argument.
func (q *DeleteQuery) softDeleteSet(gen schema.QueryGen) string {
	b := make([]byte, 0, 32)
	if gen.HasFeature(feature.UpdateMultiTable) {
		b = append(b, q.table.SQLAlias...)
		b = append(b, '.')
	}
	b = append(b, q.table.SoftDeleteField.SQLName...)
	b = append(b, " = ?"...)
	return internal.String(b)
}
```

Remove the `time` import from `query_delete.go` if `softDeleteSet` was its only user (`now := time.Now()` in `AppendQuery` still needs it, so it stays).

`query_base.go` around line 830 and `relation_join.go` around line 296: replace

```go
			b = gen.Dialect().AppendTime(b, time.Time{})
```

with

```go
			b = gen.Bind(b, time.Time{})
```

`query_select.go` around line 770, the template used when a table has more than ten columns:

```go
		if len(q.table.Fields) > 10 && gen.IsNop() {
			b = append(b, q.table.SQLAlias...)
			b = append(b, '.')
			b = append(b, '\'')
			b = fmt.Appendf(b, "%d columns", len(q.table.Fields))
			b = append(b, '\'')
		} else {
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test . -run 'TestSoftDeleteBinds|TestQuery'`
Expected: PASS. The soft-delete snapshots 209 and 210 were already regenerated in Task 6 with `"[TIME]"` for the bound `now`; after this task the zero-time comparison also becomes an arg, so re-run `go test . -run TestQuery -update`, review the diff (only soft-delete cases change: `IS NULL`/`= [TIME]` paths gain a `time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC)` arg rendered as `"[TIME]"`), and re-run without `-update`.

- [ ] **Step 6: Hand off for commit**

Run `go build ./... && go vet ./... && go test ./...` and `detect_changes()`. Hand off: `query_delete.go`, `query_base.go`, `relation_join.go`, `query_select.go`, `bind_test.go`, changed snapshots. Suggested message: `feat: bind soft-delete timestamps`.

---

### Task 9: relation joins bind parent keys

**Files:**
- Modify: `relation_join.go:68-127` (`manyQueryCompositeIn`, `manyQueryMulti`), `:129-161` (`hasManyColumns`), `:173-232` (`m2mQuery`), `:352-447` (`appendChildValues`, `appendMultiValues`, `appendAdditionalJoinOnConditions`)
- Modify: `bind_test.go`

**Interfaces:**
- Produces (unexported, `relation_join.go`):
  - `type childValues struct{ root reflect.Value; index []int; fields []*schema.Field }` implementing `schema.QueryAppender`: renders `v1, v2` or `(a1, b1), (a2, b2)` with each value bound, de-duplicated by value.
  - `type multiValues struct{ root reflect.Value; index []int; baseFields, joinFields []*schema.Field; joinTable schema.Safe }` implementing `schema.QueryAppender`: renders `((t.k = $1) AND (t.j = $2)) OR (...)`.
  - `type joinConditions []schema.QueryWithArgs` implementing `schema.QueryAppender`: renders the conditions joined with ` AND `.
  - `func childKey(v reflect.Value, fields []*schema.Field) string` — `%#v` of each key field joined with `|`, for de-duplication.

- [ ] **Step 1: Run impact analysis**

Run `impact` on `manyQueryCompositeIn`, `manyQueryMulti`, `m2mQuery`, `hasManyColumns`, `appendChildValues`, `appendMultiValues`, `appendAdditionalJoinOnConditions`. Report.

- [ ] **Step 2: Write the failing tests**

Append to `bind_test.go`:

```go
// Post, Reply, Label and PostLabel mirror Story, Comment, Tag and StoryTag from
// integration_test.go under other names, because both files are in package
// pgcrud_test.
type Post struct {
	ID       int64 `bun:",pk"`
	AuthorID int64
	Replies  []*Reply `bun:"rel:has-many,join:id=post_id"`
	Labels   []Label  `bun:"m2m:post_labels,join:Post=Label"`
}

type Reply struct {
	ID     int64 `bun:",pk"`
	PostID int64
	Body   string
}

type Label struct {
	ID   int64 `bun:",pk"`
	Name string
}

type PostLabel struct {
	PostID  int64  `bun:",pk"`
	Post    *Post  `bun:"rel:belongs-to,join:post_id=id"`
	LabelID int64  `bun:",pk"`
	Label   *Label `bun:"rel:belongs-to,join:label_id=id"`
}

// multiRowsExecutor serves a different fake row set to each successive Query.
type multiRowsExecutor struct {
	fakeExecutor
	sets []*fakeRows
}

func (e *multiRowsExecutor) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	e.calls = append(e.calls, call{"Query", sql, args})
	if len(e.sets) == 0 {
		return newFakeRows(nil), nil
	}
	rows := e.sets[0]
	e.sets = e.sets[1:]
	return rows, nil
}

func TestRelationLoadingBinds(t *testing.T) {
	ctx := context.Background()

	t.Run("has-many binds the distinct parent keys", func(t *testing.T) {
		exec := &multiRowsExecutor{sets: []*fakeRows{
			newFakeRows(cols("id", "author_id"), []any{int64(1), int64(9)}, []any{int64(2), int64(9)}, []any{int64(1), int64(9)}),
			newFakeRows(cols("id", "post_id", "body"), []any{int64(10), int64(1), "c"}),
		}}
		db := withExec(exec)
		var posts []Post
		require.NoError(t, db.NewSelect().Model(&posts).Relation("Replies").Scan(ctx))
		require.Len(t, exec.calls, 2)
		require.Contains(t, exec.calls[1].sql, `"reply"."post_id" IN ($1, $2)`)
		require.Equal(t, []any{int64(1), int64(2)}, boundArgs(exec.calls[1]))
		require.Len(t, posts[0].Replies, 1)
	})

	t.Run("has-many with a filter binds the filter after the keys", func(t *testing.T) {
		exec := &multiRowsExecutor{sets: []*fakeRows{
			newFakeRows(cols("id", "author_id"), []any{int64(1), int64(9)}),
			newFakeRows(cols("id", "post_id", "body")),
		}}
		var posts []Post
		require.NoError(t, withExec(exec).NewSelect().Model(&posts).
			Relation("Replies", func(q *pgcrud.SelectQuery) *pgcrud.SelectQuery { return q.Where("body <> ?", "spam") }).
			Scan(ctx))
		require.Contains(t, exec.calls[1].sql, `IN ($1)`)
		require.Contains(t, exec.calls[1].sql, `body <> $2`)
		require.Equal(t, []any{int64(1), "spam"}, boundArgs(exec.calls[1]))
	})

	t.Run("many-to-many binds the parent keys in the join", func(t *testing.T) {
		exec := &multiRowsExecutor{sets: []*fakeRows{
			newFakeRows(cols("id", "author_id"), []any{int64(1), int64(9)}, []any{int64(2), int64(9)}),
			newFakeRows(cols("post_id", "id", "name")),
		}}
		db := withExec(exec)
		db.RegisterModel((*PostLabel)(nil))
		var posts []Post
		require.NoError(t, db.NewSelect().Model(&posts).Relation("Labels").Scan(ctx))
		require.Contains(t, exec.calls[1].sql, `JOIN "post_labels" AS "post_label" ON ("post_label"."post_id") IN ($1, $2)`)
		require.Equal(t, []any{int64(1), int64(2)}, boundArgs(exec.calls[1]))
	})

	t.Run("has-many column expressions with args bind", func(t *testing.T) {
		exec := &multiRowsExecutor{sets: []*fakeRows{
			newFakeRows(cols("id", "author_id"), []any{int64(1), int64(9)}),
			newFakeRows(cols("id", "post_id", "body")),
		}}
		var posts []Post
		require.NoError(t, withExec(exec).NewSelect().Model(&posts).
			Relation("Replies", func(q *pgcrud.SelectQuery) *pgcrud.SelectQuery {
				return q.Column("id", "post_id").ColumnExpr("left(body, ?) AS body", 3)
			}).
			Scan(ctx))
		require.Contains(t, exec.calls[1].sql, `left(body, $1) AS body`)
		require.Contains(t, exec.calls[1].sql, `IN ($2)`)
		require.Equal(t, []any{3, int64(1)}, boundArgs(exec.calls[1]))
	})
}
```

Before running, confirm the exact expected SQL fragments against the current inline output so the assertions match how bun names aliases: run `go test . -run TestRelationLoadingBinds -v` once with the test temporarily printing `exec.calls[1].sql`; adjust alias spelling in the `Contains` assertions if they differ, keeping the `$n` expectations.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test . -run TestRelationLoadingBinds`
Expected: FAIL. Today the keys render as `?` (no collector at pre-render time), so the SQL contains `IN (?, ?)` and `boundArgs` lacks the keys.

- [ ] **Step 4: Implement**

Replace `appendChildValues`, `appendMultiValues` and `appendAdditionalJoinOnConditions` in `relation_join.go` with appender types:

```go
// childValues renders the key values of the parent rows for an IN list, each
// bound, with duplicate parents listed once.
type childValues struct {
	root   reflect.Value
	index  []int
	fields []*schema.Field
}

var _ schema.QueryAppender = childValues{}

func (c childValues) AppendQuery(gen schema.QueryGen, b []byte) ([]byte, error) {
	seen := make(map[string]struct{})
	first := true
	walk(c.root, c.index, func(v reflect.Value) {
		key := childKey(v, c.fields)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}

		if !first {
			b = append(b, ", "...)
		}
		first = false

		if len(c.fields) > 1 {
			b = append(b, '(')
		}
		for i, f := range c.fields {
			if i > 0 {
				b = append(b, ", "...)
			}
			b = f.AppendValue(gen, b, v)
		}
		if len(c.fields) > 1 {
			b = append(b, ')')
		}
	})
	return b, nil
}

// childKey identifies one parent row by its key field values, for
// de-duplication. The rendered SQL cannot serve, since every $n is distinct.
func childKey(v reflect.Value, fields []*schema.Field) string {
	var sb strings.Builder
	for _, f := range fields {
		fmt.Fprintf(&sb, "%#v|", f.Value(v).Interface())
	}
	return sb.String()
}

// multiValues is the alternative to childValues for dialects without a
// composite IN: ((t.k1 = $1) AND (t.k2 = $2)) OR (...).
type multiValues struct {
	root                   reflect.Value
	index                  []int
	baseFields, joinFields []*schema.Field
	joinTable              schema.Safe
}

var _ schema.QueryAppender = multiValues{}

func (m multiValues) AppendQuery(gen schema.QueryGen, b []byte) ([]byte, error) {
	if len(m.joinFields) != len(m.baseFields) {
		panic("not reached")
	}

	seen := make(map[string]struct{})
	first := true
	b = append(b, '(')
	walk(m.root, m.index, func(v reflect.Value) {
		key := childKey(v, m.baseFields)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}

		if !first {
			b = append(b, ") OR ("...)
		}
		first = false

		for i, f := range m.baseFields {
			if i > 0 {
				b = append(b, " AND "...)
			}
			if len(m.baseFields) > 1 {
				b = append(b, '(')
			}
			b = append(b, m.joinTable...)
			b = append(b, '.')
			b = append(b, m.joinFields[i].SQLName...)
			b = append(b, '=')
			b = f.AppendValue(gen, b, v)
			if len(m.baseFields) > 1 {
				b = append(b, ')')
			}
		}
	})
	b = append(b, ')')
	return b, nil
}

// joinConditions renders additional join-on conditions joined with AND.
type joinConditions []schema.QueryWithArgs

var _ schema.QueryAppender = joinConditions(nil)

func (c joinConditions) AppendQuery(gen schema.QueryGen, b []byte) ([]byte, error) {
	for i, cond := range c {
		if i > 0 {
			b = append(b, " AND "...)
		}
		b = gen.AppendQuery(b, cond.Query, cond.Args...)
	}
	return b, nil
}
```

Add `"fmt"` and `"strings"` to the imports of `relation_join.go`.

Rewrite the three query builders:

```go
func (j *relationJoin) manyQueryCompositeIn(where []byte, q *SelectQuery) *SelectQuery {
	if len(j.Relation.JoinPKs) > 1 {
		where = append(where, '(')
	}
	where = appendColumns(where, j.JoinModel.Table().SQLAlias, j.Relation.JoinPKs)
	if len(j.Relation.JoinPKs) > 1 {
		where = append(where, ')')
	}
	where = append(where, " IN (?)"...)

	values := childValues{
		root:   j.JoinModel.rootValue(),
		index:  j.JoinModel.parentIndex(),
		fields: j.Relation.BasePKs,
	}
	if len(j.additionalJoinOnConditions) > 0 {
		where = append(where, " AND ?"...)
		q = q.Where(internal.String(where), values, joinConditions(j.additionalJoinOnConditions))
	} else {
		q = q.Where(internal.String(where), values)
	}

	if j.Relation.PolymorphicField != nil {
		q = q.Where("? = ?", j.Relation.PolymorphicField.SQLName, j.Relation.PolymorphicValue)
	}

	j.applyTo(q)
	q = q.Apply(j.hasManyColumns)

	return q
}

func (j *relationJoin) manyQueryMulti(where []byte, q *SelectQuery) *SelectQuery {
	q = q.Where("?", multiValues{
		root:       j.JoinModel.rootValue(),
		index:      j.JoinModel.parentIndex(),
		baseFields: j.Relation.BasePKs,
		joinFields: j.Relation.JoinPKs,
		joinTable:  j.JoinModel.Table().SQLAlias,
	})

	if len(j.additionalJoinOnConditions) > 0 {
		q = q.Where("?", joinConditions(j.additionalJoinOnConditions))
	}

	if j.Relation.PolymorphicField != nil {
		q = q.Where("? = ?", j.Relation.PolymorphicField.SQLName, j.Relation.PolymorphicValue)
	}

	j.applyTo(q)
	q = q.Apply(j.hasManyColumns)

	return q
}
```

The `where []byte` parameter of `manyQueryMulti` is now unused; keep the signature so `manyQuery` is untouched, or drop the parameter in both places, your choice, but be consistent.

```go
func (j *relationJoin) hasManyColumns(q *SelectQuery) *SelectQuery {
	joinTable := j.JoinModel.Table()
	if len(j.columns) == 0 {
		b := appendColumns(nil, joinTable.SQLAlias, joinTable.Fields)
		return q.ColumnExpr(internal.String(b))
	}

	for _, col := range j.columns {
		if col.Args == nil {
			if field, ok := joinTable.FieldMap[col.Query]; ok {
				b := append([]byte(joinTable.SQLAlias), '.')
				b = append(b, field.SQLName...)
				q = q.ColumnExpr(internal.String(b))
				continue
			}
		}
		q = q.ColumnExpr("?", col)
	}

	return q
}
```

In `m2mQuery`, replace the block from `//nolint` / `var join []byte` down to `q = q.Join(internal.String(join))` with:

```go
	var join []byte
	join = append(join, "JOIN "...)
	join = gen.AppendQuery(join, string(j.Relation.M2MTable.SQLName))
	join = append(join, " AS "...)
	join = append(join, j.Relation.M2MTable.SQLAlias...)
	join = append(join, " ON ("...)
	for i, col := range j.Relation.M2MBasePKs {
		if i > 0 {
			join = append(join, ", "...)
		}
		join = append(join, j.Relation.M2MTable.SQLAlias...)
		join = append(join, '.')
		join = append(join, col.SQLName...)
	}
	join = append(join, ") IN (?)"...)

	values := childValues{root: j.BaseModel.rootValue(), index: index, fields: j.Relation.BasePKs}
	if len(j.additionalJoinOnConditions) > 0 {
		join = append(join, " AND ?"...)
		q = q.Join(internal.String(join), values, joinConditions(j.additionalJoinOnConditions))
	} else {
		q = q.Join(internal.String(join), values)
	}
```

`gen := q.db.gen` at the top of `m2mQuery` stays; it is only used for the identifier.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test . -run 'TestRelationLoadingBinds|TestQuery'`
Expected: PASS. If a snapshot involving relations changed (inline has-one joins do not go through these paths, so none should), review and regenerate as in Task 6.

- [ ] **Step 6: Hand off for commit**

Run `go build ./... && go vet ./... && go test ./...` and `detect_changes()`. Hand off: `relation_join.go`, `bind_test.go`. Suggested message: `feat(relations): bind parent keys when loading has-many and m2m`.

---

# Stage 2: deletion

### Task 10: remove the inline formatter

**Files:**
- Modify: `schema/dialect.go` (interface and `nopDialect`), `schema/querygen.go` (`FormatQuery`, `guardLineComment`), `dialect/append.go`, `dialect/pgdialect/dialect.go` (`BaseDialect` embed, `AppendUint32/64`), `dialect/pgdialect/append.go` (`appendTime` stays)
- Delete: `internal/hex.go`, `schema/appendjson_test.go`, `schema/appendstring_nul_test.go`
- Modify: `go.mod`, `go.sum` via `go mod tidy`

- [ ] **Step 1: Run impact analysis**

Run `impact` on `FormatQuery`, `BaseDialect`, `guardLineComment`, `AppendFloat32`, `AppendFloat64`, `AppendBool` (package `dialect`), `AppendError`, `NewHexEncoder`. Expected: no callers left except the tests being deleted. If any caller remains, it was missed in Stage 1: fix it to bind before deleting.

- [ ] **Step 2: Write the guard test**

Append to `schema/bind_test.go`:

```go
func TestDialectHasNoValueAppenders(t *testing.T) {
	t.Run("Dialect interface no longer carries value appenders", func(t *testing.T) {
		typ := reflect.TypeFor[Dialect]()
		for _, name := range []string{"AppendString", "AppendBytes", "AppendJSON", "AppendTime", "AppendBool", "AppendUint32", "AppendUint64"} {
			_, found := typ.MethodByName(name)
			require.False(t, found, name)
		}
	})
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./schema -run TestDialectHasNoValueAppenders`
Expected: FAIL, the methods still exist.

- [ ] **Step 4: Delete**

`schema/dialect.go`: the `Dialect` interface becomes

```go
type Dialect interface {
	Name() dialect.Name
	Features() feature.Feature

	Tables() *Tables
	OnTable(table *Table)

	IdentQuote() byte

	// AppendSequence adds the appropriate instruction for the driver to create a sequence
	// from which (autoincremented) values for the column will be generated.
	AppendSequence(b []byte, t *Table, f *Field) []byte

	// DefaultVarcharLen should be returned for dialects in which specifying VARCHAR length
	// is mandatory in queries that modify the schema (CREATE TABLE / ADD COLUMN, etc).
	// Dialects that do not have such requirement may return 0, which should be interpreted so by the caller.
	DefaultVarcharLen() int

	// DefaultSchema should returns the name of the default database schema.
	DefaultSchema() string
}
```

Delete `errStringNul`, the whole `BaseDialect` type and its methods, and remove `BaseDialect` from `nopDialect`. Drop the now-unused imports (`encoding/hex`, `errors`, `strconv`, `time`, `unicode/utf8`, `internal/parser`).

`schema/querygen.go`: delete `guardLineComment` and `FormatQuery`. `AppendQuery` keeps its short-circuit.

`dialect/append.go`: delete `AppendError`, `AppendBool`, `AppendFloat32`, `AppendFloat64`, `appendFloat`; keep `AppendNull`, `AppendName`, `AppendIdent` and helpers. Drop the `math` and `strconv` imports.

`dialect/pgdialect/dialect.go`: remove `schema.BaseDialect` from the struct, delete `AppendUint32` and `AppendUint64`; keep `uintAsInt`, `WithAppendUintAsInt`, `UintAsInt`. Drop the `strconv` import if unused.

Delete `internal/hex.go`, `schema/appendjson_test.go`, `schema/appendstring_nul_test.go`.

Run: `go mod tidy`
Expected: `github.com/tmthrgd/go-hex` disappears from `go.mod`.

Run: `go build ./... 2>&1 | head` and fix any remaining reference the compiler reports by binding it, never by re-adding an inline appender.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go vet ./... && go test ./...`
Expected: PASS, including `TestDialectHasNoValueAppenders`.

- [ ] **Step 6: Hand off for commit**

Run `detect_changes()`. Hand off: the modified and deleted files plus `go.mod`, `go.sum`. Suggested message: `refactor: delete the inline literal formatter`.

---

### Task 11: record the outcome in todo.md and the original spec

**Files:**
- Modify: `todo.md`
- Modify: `docs/superpowers/specs/2026-09-22-pgcrud-design.md` (section 8, first bullet)

- [ ] **Step 1: Update todo.md**

Replace item 1 with:

```markdown
1. ~~Real placeholders.~~ Done, see `docs/superpowers/specs/2026-09-27-bound-parameters-design.md`. Values are bound as `$1..$n`; arrays, ranges and multiranges are requested in text so the existing parsers keep working. The aliasing constraint recorded here turned out not to apply: every pgx codec copies before handing bytes to a `sql.Scanner`, and the raw-buffer path is only reached for unregistered types requested in binary, which pgcrud never does.
```

Renumber nothing; items 2 and 3 stay.

- [ ] **Step 2: Update the original spec**

In `docs/superpowers/specs/2026-09-22-pgcrud-design.md` section 8, replace the "Real placeholders" bullet with:

```markdown
- **Real placeholders.** Implemented by `2026-09-27-bound-parameters-design.md`. The constraint recorded here about pgx handing a `sql.Scanner` the raw read buffer in binary format was checked against pgx 5.9.2 and does not apply: every registered codec's `DecodeDatabaseSQLValue` returns a typed value or a fresh copy. The binary hazard is arrays, ranges and multiranges, whose binary wire form the text parsers cannot read; they are requested in text.
```

Also in section 4.3 of that document, append one sentence to the paragraph starting "Values are inlined into the SQL text": "Superseded: values are bound as `$n` parameters since `2026-09-27-bound-parameters-design.md`."

- [ ] **Step 3: Hand off for commit**

Hand off: `todo.md`, `docs/superpowers/specs/2026-09-22-pgcrud-design.md`. Suggested message: `docs: record bound parameters outcome`.

---

# Stage 3: integration tests and docs

### Task 12: integration tests against Postgres

**Files:**
- Create: `integration_bind_test.go`
- Modify: `integration_test.go` (add `CREATE EXTENSION IF NOT EXISTS hstore;` and a `kitchen` table to `schemaSQL`)

**Interfaces:**
- Consumes: `testDB(t)` and `UnitOfWork` from `integration_test.go` and `uow_test.go`; `Author`, `Story`, `Comment` fixtures.

- [ ] **Step 1: Start Postgres**

Run: `docker compose up -d --wait`
Expected: the `postgres` service is healthy. Export `PGCRUD_TEST_DSN='postgres://postgres:postgres@localhost:5442/pgcrud?sslmode=disable'` for the following runs.

- [ ] **Step 2: Extend the schema**

`schemaSQL` is several statements in one string. It keeps working because pgx runs an `Exec` with no arguments through the simple protocol, which accepts multiple statements; a prepared statement would not.

In `integration_test.go`, prepend to `schemaSQL`:

```sql
CREATE EXTENSION IF NOT EXISTS hstore;
DROP TABLE IF EXISTS kitchen;
CREATE TABLE kitchen (
	id         bigserial PRIMARY KEY,
	tags       text[],
	nums       bigint[],
	doc        jsonb,
	attrs      hstore,
	span       int8range,
	period     tstzrange,
	addr       inet,
	blob       bytea,
	packed     bytea,
	label      text,
	rank       int4,
	ratio      float8,
	ok         boolean,
	at         timestamptz,
	nullable   text
);
```

and add the model to the same file:

```go
type Kitchen struct {
	ID       int64                     `bun:",pk,autoincrement"`
	Tags     []string                  `bun:",array"`
	Nums     []int64                   `bun:",array"`
	Doc      map[string]any            `bun:",type:jsonb"`
	Attrs    map[string]string         `bun:",hstore"`
	Span     pgdialect.Range[int64]
	Period   pgdialect.Range[time.Time]
	Addr     net.IP
	Blob     []byte
	Packed   map[string]int            `bun:",msgpack"`
	Label    string
	Rank     int32
	Ratio    float64
	OK       bool                      `bun:"ok"`
	At       time.Time
	Nullable sql.NullString
}
```

Imports for `integration_test.go`: `database/sql`, `net`, `github.com/piprim/pgcrud/dialect/pgdialect`.

- [ ] **Step 3: Write the tests**

Create `integration_bind_test.go`:

```go
package pgcrud_test

import (
	"context"
	"database/sql"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/piprim/pgcrud"
	"github.com/piprim/pgcrud/dialect/pgdialect"
)

func TestIntegrationBindMapping(t *testing.T) {
	db, _ := testDB(t)
	ctx := context.Background()

	at := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	in := &Kitchen{
		Tags:     []string{"a", "b c", `q"uote`},
		Nums:     []int64{1, -2, 3},
		Doc:      map[string]any{"k": "v", "n": float64(1)},
		Attrs:    map[string]string{"x": "1", "y z": `w"v`},
		Span:     pgdialect.NewRange[int64](1, 10),
		Period:   pgdialect.NewRange(at, at.Add(time.Hour)),
		Addr:     net.ParseIP("10.1.2.3"),
		Blob:     []byte{0, 1, 255},
		Packed:   map[string]int{"p": 7},
		Label:    "it's",
		Rank:     -5,
		Ratio:    2.5,
		OK:       true,
		At:       at,
		Nullable: sql.NullString{},
	}
	_, err := db.NewInsert().Model(in).Returning("id").Exec(ctx)
	require.NoError(t, err)

	var out Kitchen
	require.NoError(t, db.NewSelect().Model(&out).Where("id = ?", in.ID).Scan(ctx))

	t.Run("text array round trips as text result", func(t *testing.T) {
		require.Equal(t, in.Tags, out.Tags)
	})
	t.Run("bigint array round trips", func(t *testing.T) {
		require.Equal(t, in.Nums, out.Nums)
	})
	t.Run("jsonb round trips", func(t *testing.T) {
		require.Equal(t, in.Doc, out.Doc)
	})
	t.Run("hstore round trips", func(t *testing.T) {
		require.Equal(t, in.Attrs, out.Attrs)
	})
	t.Run("int8range round trips", func(t *testing.T) {
		require.Equal(t, in.Span, out.Span)
	})
	t.Run("tstzrange round trips", func(t *testing.T) {
		require.True(t, in.Period.Lower.Equal(out.Period.Lower))
		require.True(t, in.Period.Upper.Equal(out.Period.Upper))
	})
	t.Run("inet round trips", func(t *testing.T) {
		require.True(t, in.Addr.Equal(out.Addr))
	})
	t.Run("bytea round trips from a binary result", func(t *testing.T) {
		require.Equal(t, in.Blob, out.Blob)
	})
	t.Run("msgpack round trips", func(t *testing.T) {
		require.Equal(t, in.Packed, out.Packed)
	})
	t.Run("scalars round trip from binary results", func(t *testing.T) {
		require.Equal(t, in.Label, out.Label)
		require.Equal(t, in.Rank, out.Rank)
		require.Equal(t, in.Ratio, out.Ratio)
		require.Equal(t, in.OK, out.OK)
		require.True(t, in.At.Equal(out.At))
	})
	t.Run("invalid NullString is NULL and scans back invalid", func(t *testing.T) {
		require.False(t, out.Nullable.Valid)
		var isNull bool
		require.NoError(t, db.NewSelect().Model((*Kitchen)(nil)).ColumnExpr("nullable IS NULL").Where("id = ?", in.ID).Scan(ctx, &isNull))
		require.True(t, isNull)
	})
	t.Run("valid NullString binds its value", func(t *testing.T) {
		_, err := db.NewUpdate().Model((*Kitchen)(nil)).Set("nullable = ?", sql.NullString{String: "set", Valid: true}).Where("id = ?", in.ID).Exec(ctx)
		require.NoError(t, err)
		var s string
		require.NoError(t, db.NewSelect().Model((*Kitchen)(nil)).Column("nullable").Where("id = ?", in.ID).Scan(ctx, &s))
		require.Equal(t, "set", s)
	})
	t.Run("array parameter in a where clause", func(t *testing.T) {
		n, err := db.NewSelect().Model((*Kitchen)(nil)).Where("tags && ?", pgdialect.Array([]string{"a"})).Count(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(1), n)
	})
	t.Run("range parameter with a cast where the type cannot be inferred", func(t *testing.T) {
		var contains bool
		require.NoError(t, db.NewSelect().ColumnExpr("?::int8range @> 5::bigint", pgdialect.NewRange[int64](1, 10)).Scan(ctx, &contains))
		require.True(t, contains)
	})
	t.Run("map scan receives typed binary values", func(t *testing.T) {
		var m map[string]any
		require.NoError(t, db.NewSelect().Model((*Kitchen)(nil)).Column("rank", "ok", "at", "tags").Where("id = ?", in.ID).Scan(ctx, &m))
		require.Equal(t, int64(-5), m["rank"])
		require.Equal(t, true, m["ok"])
		_, isTime := m["at"].(time.Time)
		require.True(t, isTime)
		_, isString := m["tags"].(string)
		require.True(t, isString, "arrays arrive as text")
	})
}

func TestIntegrationBindStatements(t *testing.T) {
	db, uow := testDB(t)
	ctx := context.Background()

	author := &Author{Name: "bind", CreatedAt: time.Now()}
	_, err := db.NewInsert().Model(author).Returning("id").Exec(ctx)
	require.NoError(t, err)
	stories := []*Story{{Title: "s1", AuthorID: author.ID}, {Title: "s2", AuthorID: author.ID}}
	_, err = db.NewInsert().Model(&stories).Returning("id").Exec(ctx)
	require.NoError(t, err)
	comments := []*Comment{{StoryID: stories[0].ID, Body: "c1"}, {StoryID: stories[1].ID, Body: "c2"}}
	_, err = db.NewInsert().Model(&comments).Exec(ctx)
	require.NoError(t, err)

	t.Run("nested select binds across both levels", func(t *testing.T) {
		sub := db.NewSelect().Model((*Story)(nil)).Column("id").Where("author_id = ?", author.ID)
		var got []Comment
		require.NoError(t, db.NewSelect().Model(&got).Where("body <> ?", "x").Where("story_id IN (?)", sub).OrderExpr("id").Scan(ctx))
		require.Len(t, got, 2)
	})

	t.Run("has-many loads through bound parent keys", func(t *testing.T) {
		var got []Story
		require.NoError(t, db.NewSelect().Model(&got).Relation("Comments").Where("story.author_id = ?", author.ID).OrderExpr("story.id").Scan(ctx))
		require.Len(t, got, 2)
		require.Len(t, got[0].Comments, 1)
		require.Equal(t, "c1", got[0].Comments[0].Body)
	})

	t.Run("values CTE with casts", func(t *testing.T) {
		rows := []Tag{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}}
		var got []Tag
		require.NoError(t, db.NewSelect().With("v", db.NewValues(&rows)).TableExpr("v").ColumnExpr("v.id, v.name").OrderExpr("v.id").Scan(ctx, &got))
		require.Equal(t, rows, got)
	})

	// Comment binds two values per row (story_id, body); its autoincrement id
	// renders as the inline keyword DEFAULT and costs no parameter.
	t.Run("bulk insert under the limit succeeds", func(t *testing.T) {
		bulk := make([]Comment, 30000)
		for i := range bulk {
			bulk[i] = Comment{StoryID: stories[0].ID, Body: "b"}
		}
		_, err := db.NewInsert().Model(&bulk).Exec(ctx) // 30000 rows * 2 values = 60000 params
		require.NoError(t, err)
	})

	t.Run("bulk insert over the limit fails with ErrTooManyParams", func(t *testing.T) {
		bulk := make([]Comment, 40000)
		for i := range bulk {
			bulk[i] = Comment{StoryID: stories[0].ID, Body: "b"}
		}
		_, err := db.NewInsert().Model(&bulk).Exec(ctx) // 80000 params
		require.ErrorIs(t, err, pgcrud.ErrTooManyParams)
	})

	t.Run("the same statement is prepared once per connection", func(t *testing.T) {
		err := uow.WithTransaction(ctx, func(ctx context.Context) error {
			q := db.NewSelect().Model((*Author)(nil)).Where("id = ?", author.ID)
			for range 3 {
				var a Author
				if err := q.Scan(ctx, &a); err != nil {
					return err
				}
			}
			sql, _, err := q.Build()
			if err != nil {
				return err
			}
			var n int64
			if err := db.NewRaw("SELECT count(*) FROM pg_prepared_statements WHERE statement = ?", sql).Scan(ctx, &n); err != nil {
				return err
			}
			require.Equal(t, int64(1), n)
			return nil
		})
		require.NoError(t, err)
	})

	t.Run("raw $n SQL runs unchanged", func(t *testing.T) {
		var name string
		require.NoError(t, db.NewRaw("SELECT name FROM authors WHERE id = $1", author.ID).Scan(ctx, &name))
		require.Equal(t, "bind", name)
	})
}
```

- [ ] **Step 4: Run the integration tests**

Run: `PGCRUD_TEST_DSN='postgres://postgres:postgres@localhost:5442/pgcrud?sslmode=disable' go test -count=1 -run 'TestIntegration' ./...`
Expected: PASS for all `TestIntegration*` including the four pre-existing ones. If `hstore round trips` fails with an encoding error, Postgres did not coerce the text parameter: check the exact error and, only if the server reports a type mismatch, add `::hstore` to the insert through a `Value("attrs", "?::hstore", ...)`; report the finding in the hand-off because it changes the spec's section 4.2 row.

Also run the full suite once without the DSN to confirm the skip path: `go test ./...`.

- [ ] **Step 5: Hand off for commit**

Run `detect_changes()`. Hand off: `integration_test.go`, `integration_bind_test.go`. Suggested message: `test: integration coverage for bound parameters`.

---

### Task 13: README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Rewrite the Notes section**

Replace the first bullet of "## Notes" and add the following bullets:

```markdown
- Queries use bun's `?` placeholders. pgcrud translates them to `$1..$n` and passes the
  values to pgx, so statements are prepared and cached per connection and parameters travel
  in binary. `q.String()` shows the SQL that is sent and `q.Args()` the values.
- A raw query that contains no `?` is passed to pgx unchanged with its args, so pgx-style
  SQL with `$1` works too. Do not mix `?` and `$n` in one query.
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
```

Keep the remaining bullets (errors, `Rows`, struct tags).

- [ ] **Step 2: Verify the module one last time**

Run: `go build ./... && go vet ./... && go test ./...` and, with the DSN exported, `go test -count=1 ./...`.
Expected: PASS.

- [ ] **Step 3: Hand off for commit**

Run `detect_changes()` for the whole branch: `detect_changes({scope: "compare", base_ref: "master"})`, and summarise the affected symbols and flows for the user. Hand off: `README.md`. Suggested message: `docs: describe bound parameters`.
