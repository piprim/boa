package boa_test

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/piprim/boa"
	"github.com/piprim/boa/dialect/pgdialect"
)

const schemaSQL = `
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
	label      text,
	rank       int4,
	ratio      float8,
	ok         boolean,
	at         timestamptz,
	nullable   text
);
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
	ID        int64 `boa:",pk,autoincrement"`
	Name      string
	Emails    []string       `boa:",array,nullzero"`
	Meta      map[string]any `boa:",type:jsonb"`
	Avatar    []byte
	Balance   float64
	Active    bool
	CreatedAt time.Time
}

type Story struct {
	ID       int64 `boa:",pk,autoincrement"`
	Title    string
	AuthorID int64
	Author   *Author    `boa:"rel:belongs-to,join:author_id=id"`
	Comments []*Comment `boa:"rel:has-many,join:id=story_id"`
	Tags     []Tag      `boa:"m2m:story_tags,join:Story=Tag"`
}

type Comment struct {
	ID      int64 `boa:",pk,autoincrement"`
	StoryID int64
	Body    string
}

type Tag struct {
	ID   int64 `boa:",pk,autoincrement"`
	Name string
}

// Kitchen holds one column of every value type the bind mapping covers.
type Kitchen struct {
	boa.BaseModel `boa:"table:kitchen"`

	ID       int64             `boa:",pk,autoincrement"`
	Tags     []string          `boa:",array"`
	Nums     []int64           `boa:",array"`
	Doc      map[string]any    `boa:",type:jsonb"`
	Attrs    map[string]string `boa:",hstore"`
	Span     pgdialect.Range[int64]
	Period   pgdialect.Range[time.Time]
	Addr     net.IP
	Blob     []byte
	Label    string
	Rank     int32
	Ratio    float64
	OK       bool `boa:"ok"`
	At       time.Time
	Nullable sql.NullString
}

type StoryTag struct {
	StoryID int64  `boa:",pk"`
	Story   *Story `boa:"rel:belongs-to,join:story_id=id"`
	TagID   int64  `boa:",pk"`
	Tag     *Tag   `boa:"rel:belongs-to,join:tag_id=id"`
}

// testDB connects to BOA_TEST_DSN, recreates the schema and returns a DB
// wired to a UnitOfWork. It skips the test when the variable is unset.
func testDB(t *testing.T) (*boa.DB, *UnitOfWork) {
	t.Helper()
	dsn := os.Getenv("BOA_TEST_DSN")
	if dsn == "" {
		t.Skip("BOA_TEST_DSN not set")
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	uow := NewUnitOfWork(pool)
	db := boa.New(pool, boa.WithExecutorResolver(uow.Executor))
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
		Relation("Comments", func(q *boa.SelectQuery) *boa.SelectQuery { return q.OrderExpr("id") }).
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

var _ boa.BeforeAppendModelHook = (*Author)(nil)

// BeforeAppendModel fills CreatedAt on insert when the caller left it zero.
func (a *Author) BeforeAppendModel(ctx context.Context, query boa.Query) error {
	if _, ok := query.(*boa.InsertQuery); ok && a.CreatedAt.IsZero() {
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
		strict := boa.New(db.Pool(),
			boa.WithExecutorResolver(uow.Executor),
			boa.WithTxRequiredForWrites())
		_, err := strict.NewInsert().Model(&Tag{Name: "escaped"}).Exec(ctx)
		require.ErrorIs(t, err, boa.ErrTxRequired)
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
