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
//
// fn must return a nil interface, not a typed nil (for example a nil pgx.Tx
// stored in a non-nil DBExecutor), because only a nil interface is detected
// and reported as ErrNilExecutor.
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

// sqlSpace holds the characters SQL treats as whitespace between tokens.
const sqlSpace = " \t\n\r\v\f"

// isReadStatement reports whether a raw SQL text is a read-only statement.
// Leading whitespace, block and line comments and opening parentheses are
// skipped before the first keyword is read. Anything unrecognised, including
// WITH and an empty statement, is treated as a write so that
// WithTxRequiredForWrites fails closed.
func isReadStatement(query string) bool {
	s := skipStatementPrefix(query)
	keyword, rest := readKeyword(s)

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
//
// On error pgx may return a non-nil, already-closed pgx.Rows alongside the
// error (bun returned nil rows), so check the error before using the rows.
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
