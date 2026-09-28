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

	t.Run("map-slice values CTE feeds INSERT ... SELECT", func(t *testing.T) {
		src := db.NewValues(&[]map[string]any{{"name": "from-map-1"}, {"name": "from-map-2"}})
		_, err := db.NewInsert().With("src", src).TableExpr("tags (name)").TableExpr("src").Exec(ctx)
		require.NoError(t, err)
		n, err := db.NewSelect().Model((*Tag)(nil)).Where("name LIKE ?", "from-map-%").Count(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(2), n)
	})

	t.Run("map-slice values CTE drives UPDATE ... FROM", func(t *testing.T) {
		src := db.NewValues(&[]map[string]any{{"id": stories[0].ID, "title": "renamed"}})
		_, err := db.NewUpdate().With("src", src).Table("stories", "src").
			Set("title = src.title").Where("stories.id = src.id").Exec(ctx)
		require.NoError(t, err)
		var title string
		require.NoError(t, db.NewSelect().Model((*Story)(nil)).Column("title").Where("id = ?", stories[0].ID).Scan(ctx, &title))
		require.Equal(t, "renamed", title)
	})

	t.Run("raw $n SQL runs unchanged", func(t *testing.T) {
		var name string
		require.NoError(t, db.NewRaw("SELECT name FROM authors WHERE id = $1", author.ID).Scan(ctx, &name))
		require.Equal(t, "bind", name)
	})
}
