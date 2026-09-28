package pgcrud_test

import (
	"context"
	"strings"
	"testing"
	"time"

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
		require.Equal(t, int16(pgx.TextFormatCode), formats[99999])
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

	t.Run("nested raw $n after a bound value is an error, not a misnumbered statement", func(t *testing.T) {
		db := pgcrud.New(nil)
		_, _, err := db.NewSelect().Table("t").Where("a = ?", 1).
			Where("id IN (?)", db.NewRaw("SELECT id FROM u WHERE y = $1", 2)).Build()
		require.ErrorContains(t, err, "raw SQL with $n placeholders")
	})

	t.Run("nested raw $n rendered before any bound value keeps its numbering", func(t *testing.T) {
		db := pgcrud.New(nil)
		sql, args, err := db.NewSelect().TableExpr("(?) AS t", db.NewRaw("SELECT $1::int AS id", 5)).Where("t.id > ?", 0).Build()
		require.NoError(t, err)
		require.Equal(t, `SELECT * FROM (SELECT $1::int AS id) AS t WHERE (t.id > $2)`, sql)
		require.Equal(t, []any{5, 0}, args)
	})
}

func TestValuesCasts(t *testing.T) {
	db := pgcrud.New(nil)

	t.Run("map slice values are cast from their Go type, nil stays NULL", func(t *testing.T) {
		rows := []map[string]any{{
			"at": time.Time{}, "blob": []byte{1}, "id": 42, "nothing": nil, "ok": true, "ratio": 1.5, "str": "hello",
		}}
		sql, args, err := db.NewValues(&rows).Build()
		require.NoError(t, err)
		require.Equal(t, `VALUES ($1::TIMESTAMP, $2::BYTEA, $3::BIGINT, NULL, $4::BOOLEAN, $5::DOUBLE PRECISION, $6::VARCHAR)`, sql)
		require.Equal(t, []any{time.Time{}, []byte{1}, 42, true, 1.5, "hello"}, args)
	})

	t.Run("value override keeps the column cast", func(t *testing.T) {
		sql, args, err := db.NewValues(&User{ID: 1, Name: "x"}).Value("name", "upper(?)", "y").Build()
		require.NoError(t, err)
		require.Equal(t, `VALUES ($1::BIGINT, upper($2)::VARCHAR)`, sql)
		require.Equal(t, []any{int64(1), "y"}, args)
	})
}

type SoftUser struct {
	pgcrud.BaseModel `bun:"table:soft_users,alias:su"`

	ID        int64 `bun:",pk"`
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

// relDB returns a DB on exec with the m2m join table registered, which the
// Post table needs before it is built.
func relDB(exec pgcrud.DBExecutor) *pgcrud.DB {
	db := withExec(exec)
	db.RegisterModel((*PostLabel)(nil))
	return db
}

func TestRelationLoadingBinds(t *testing.T) {
	ctx := context.Background()

	t.Run("has-many binds the distinct parent keys", func(t *testing.T) {
		exec := &multiRowsExecutor{sets: []*fakeRows{
			newFakeRows(cols("id", "author_id"), []any{int64(1), int64(9)}, []any{int64(2), int64(9)}, []any{int64(1), int64(9)}),
			newFakeRows(cols("id", "post_id", "body"), []any{int64(10), int64(1), "c"}),
		}}
		db := relDB(exec)
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
		require.NoError(t, relDB(exec).NewSelect().Model(&posts).
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
		db := relDB(exec)
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
		require.NoError(t, relDB(exec).NewSelect().Model(&posts).
			Relation("Replies", func(q *pgcrud.SelectQuery) *pgcrud.SelectQuery {
				return q.Column("id", "post_id").ColumnExpr("left(body, ?) AS body", 3)
			}).
			Scan(ctx))
		require.Contains(t, exec.calls[1].sql, `left(body, $1) AS body`)
		require.Contains(t, exec.calls[1].sql, `IN ($2)`)
		require.Equal(t, []any{3, int64(1)}, boundArgs(exec.calls[1]))
	})
}
