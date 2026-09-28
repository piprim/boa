package pgcrud_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/piprim/pgcrud"
	"github.com/piprim/pgcrud/schema"
)

func TestQuery(t *testing.T) {
	type Model struct {
		ID  int64 `bun:",pk,autoincrement"`
		Str string
	}

	type User struct {
		ID   int64 `bun:",pk,autoincrement"`
		Name string
	}

	type Story struct {
		ID     int64 `bun:",pk,autoincrement"`
		Name   string
		UserID int64
		User   *User `bun:"rel:belongs-to"`
	}

	type SoftDelete1 struct {
		pgcrud.BaseModel `bun:"soft_deletes,alias:soft_delete"`

		ID        int64     `bun:",pk,autoincrement"`
		DeletedAt time.Time `bun:",soft_delete,nullzero"`
	}

	type SoftDelete2 struct {
		pgcrud.BaseModel `bun:"soft_deletes,alias:soft_delete"`

		ID        int64     `bun:",pk,autoincrement"`
		DeletedAt time.Time `bun:",soft_delete"`
	}

	type test struct {
		id    int
		query func(db *pgcrud.DB) schema.QueryAppender
	}

	// The case ids skip the 34 cases that used builders pgcrud does not provide
	// (CreateTable, DropTable, TruncateTable, CreateIndex, DropIndex, AddColumn,
	// DropColumn, Merge, ResetModel) or branched on the dialect name, so their
	// ids and snapshot files are intentionally absent.
	tests := []test{
		{
			id: 0,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewValues(&Model{42, "hello"})
			},
		},
		{
			id: 1,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				models := []Model{
					{42, "hello"},
					{43, "world"},
				}
				return db.NewValues(&models)
			},
		},
		{
			id: 2,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model((*Model)(nil)).ModelTableExpr("?TableName AS ?TableAlias")
			},
		},
		{
			id: 3,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model((*Model)(nil)).ColumnExpr("?PKs")
			},
		},
		{
			id: 4,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model((*Model)(nil)).ColumnExpr("?TablePKs")
			},
		},
		{
			id: 5,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model((*Model)(nil)).ColumnExpr("?Columns")
			},
		},
		{
			id: 6,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model((*Model)(nil)).ColumnExpr("?TableColumns")
			},
		},
		{
			id: 7,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect()
			},
		},
		{
			id: 8,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Table("table")
			},
		},
		{
			id: 9,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().TableExpr("table")
			},
		},
		{
			id: 10,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(Model)).WherePK()
			},
		},
		{
			id: 11,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(Model)).WherePK()
			},
		},
		{
			id: 12,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(Model)).WhereOr("id = 42")
			},
		},
		{
			id: 13,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Distinct().Model(new(Model))
			},
		},
		{
			id: 14,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().DistinctOn("foo").Model(new(Model))
			},
		},
		{
			id: 15,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				query := db.NewSelect().Model(new(Model))
				return db.NewSelect().With("foo", query).Table("foo")
			},
		},
		{
			id: 16,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				q1 := db.NewSelect().Model(new(Model)).Where("1")
				q2 := db.NewSelect().Model(new(Model))
				return q1.Union(q2)
			},
		},
		{
			id: 17,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				models := []Model{
					{42, "hello"},
					{43, "world"},
				}
				return db.NewSelect().
					With("_data", db.NewValues(&models).WithOrder()).
					Model(&models).
					Where("model.id = _data.id").
					OrderExpr("_data._order")
			},
		},
		{
			id: 18,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				models := []Model{
					{42, "hello"},
					{43, "world"},
				}
				return db.NewSelect().
					Model(&models).
					TableExpr("(?) AS (?Columns)", db.NewValues(&models).WithOrder()).
					Where("model.id = _data.id").
					OrderExpr("_data._order")
			},
		},
		{
			id: 19,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				model := &Model{ID: 42, Str: "hello"}
				return db.NewInsert().Model(model)
			},
		},
		{
			id: 20,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				models := []Model{
					{42, "hello"},
					{43, "world"},
				}
				return db.NewInsert().Model(&models)
			},
		},
		{
			id: 21,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				models := []*Model{
					{42, "hello"},
					{43, "world"},
				}
				return db.NewInsert().Model(&models)
			},
		},
		{
			id: 22,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				models := []*Model{
					{42, "hello"},
					{43, "world"},
				}
				return db.NewInsert().Model(&models).On("CONFLICT DO NOTHING")
			},
		},
		{
			id: 23,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				models := []*Model{
					{42, "hello"},
					{43, "world"},
				}
				return db.NewInsert().
					Model(&models).
					On("CONFLICT DO UPDATE").
					Set("model.str = EXCLUDED.str").
					Where("model.str IS NULL")
			},
		},
		{
			id: 24,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.
					NewInsert().
					Model(&map[string]any{
						"id":  42,
						"str": "hello",
					}).
					Table("models")
			},
		},
		{
			id: 25,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				src := db.NewValues(&[]map[string]any{
					{"id": 42, "str": "hello"},
					{"id": 43, "str": "world"},
				})
				return db.NewInsert().With("src", src).TableExpr("dest").TableExpr("src")
			},
		},
		{
			id: 26,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewUpdate().Model(new(Model)).WherePK()
			},
		},
		{
			id: 27,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				model := &Model{ID: 42, Str: "hello"}
				return db.NewUpdate().Model(model).WherePK()
			},
		},
		{
			id: 28,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				models := []Model{
					{42, "hello"},
					{43, "world"},
				}
				return db.NewUpdate().
					With("_data", db.NewValues(&models)).
					Model(&models).
					Table("_data").
					Set("model.str = _data.str").
					Where("model.id = _data.id")
			},
		},
		{
			id: 29,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewUpdate().
					Model(&map[string]any{"str": "hello"}).
					Table("models").
					Where("id = 42")
			},
		},
		{
			id: 30,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				src := db.NewValues(&[]map[string]any{
					{"id": 42, "str": "hello"},
					{"id": 43, "str": "world"},
				})
				return db.NewUpdate().
					With("src", src).
					Table("dest", "src").
					Set("dest.str = src.str").
					Where("dest.id = src.id")
			},
		},
		{
			id: 31,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewDelete().Model(new(Model)).WherePK()
			},
		},
		{
			id: 35,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(Model)).
					Where("1").
					WhereOr("2").
					WhereGroup(" OR ", func(q *pgcrud.SelectQuery) *pgcrud.SelectQuery {
						return q.
							WhereOr("3").
							WhereOr("4").
							WhereGroup(" OR NOT ", func(q *pgcrud.SelectQuery) *pgcrud.SelectQuery {
								return q.
									WhereOr("5").
									WhereOr("6")
							})
					})
			},
		},
		{
			id: 36,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(Model)).ExcludeColumn("id")
			},
		},
		{
			id: 40,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Where("id IN ?", pgcrud.Tuple([]int{1, 2, 3}))
			},
		},
		{
			id: 41,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Where("(id1, id2) IN ?", pgcrud.Tuple([][]int{{1, 2}, {3, 4}}))
			},
		},
		{
			id: 46,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(Story)).Relation("User")
			},
		},
		{
			id: 47,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().
					Model(new(Story)).
					Relation("User", func(q *pgcrud.SelectQuery) *pgcrud.SelectQuery {
						q = q.ExcludeColumn("*")
						return q
					})
			},
		},
		{
			id: 48,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().
					Model(new(Story)).
					Relation("User", func(q *pgcrud.SelectQuery) *pgcrud.SelectQuery {
						q = q.ExcludeColumn("id")
						return q
					})
			},
		},
		{
			id: 49,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(Model)).WherePK().For("UPDATE")
			},
		},
		{
			id: 50,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				models := []Model{
					{42, "hello"},
					{43, "world"},
				}
				return db.NewUpdate().
					Model(&models).
					Table("_data").
					Where("model.id = _data.id")
			},
		},
		{
			id: 51,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				// "nullzero" marshals zero values as DEFAULT or NULL (if DEFAULT placeholder is not supported)
				// DB drivers which support DEFAULT placeholder resolve it to NULL for columns that do not have a DEFAULT value.
				type Model struct {
					Int      int64     `bun:",nullzero"`
					Uint     uint64    `bun:",nullzero"`
					Str      string    `bun:",nullzero"`
					Time     time.Time `bun:",nullzero"`
					Bool     bool      `bun:",nullzero"`
					EmptyStr string    `bun:",nullzero"` // same as Str
				}
				return db.NewInsert().Model(new(Model))
			},
		},
		{
			id: 52,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				// "nullzero,default" is equivalent to "default", marshalling zero values to DEFAULT
				type Model struct {
					Int      int64     `bun:",nullzero,default:42"`
					Uint     uint64    `bun:",nullzero,default:42"`
					Str      string    `bun:",nullzero,default:'hello'"`
					Time     time.Time `bun:",nullzero,default:now()"`
					Bool     bool      `bun:",nullzero,default:true"`
					EmptyStr string    `bun:",nullzero,default:''"`
				}
				return db.NewInsert().Model(new(Model))
			},
		},
		{
			id: 53,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type mystr string
				type Model struct {
					Array []mystr `bun:",array"`
				}
				return db.NewInsert().Model(&Model{
					Array: []mystr{"foo", "bar"},
				})
			},
		},
		{
			id: 54,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewInsert().Ignore().Model(new(Model))
			},
		},
		{
			id: 56,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				models := []*Model{
					{42, "hello"},
					{43, "world"},
				}
				return db.NewInsert().
					Model(&models).
					On("DUPLICATE KEY UPDATE").
					Set("str = upper(str)")
			},
		},
		{
			id: 58,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					Raw json.RawMessage
				}
				return db.NewInsert().Model(new(Model))
			},
		},
		{
			id: 59,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					Raw *json.RawMessage
				}
				return db.NewInsert().Model(new(Model))
			},
		},
		{
			id: 60,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					Bytes []byte
				}
				return db.NewInsert().Model(&Model{Bytes: make([]byte, 10)})
			},
		},
		{
			id: 61,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					Time pgcrud.NullTime
				}
				models := make([]Model, 2)
				models[1].Time = pgcrud.NullTime{Time: time.Unix(0, 0)}
				return db.NewValues(&models)
			},
		},
		{
			id: 62,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewInsert().Model(new(Model)).Value("foo", "?", "bar")
			},
		},
		{
			id: 63,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewUpdate().Model(new(Model)).Value("foo", "?", "bar").WherePK()
			},
		},
		{
			id: 64,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewDelete().Model(new(SoftDelete1)).WherePK()
			},
		},
		{
			id: 65,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewDelete().Model(new(SoftDelete1)).WherePK().ForceDelete()
			},
		},
		{
			id: 66,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(SoftDelete1))
			},
		},
		{
			id: 67,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(SoftDelete1)).WhereDeleted()
			},
		},
		{
			id: 68,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(SoftDelete1)).WhereAllWithDeleted()
			},
		},
		{
			id: 69,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					ID   int64 `bun:",pk,autoincrement"`
					Str1 string
					Str2 string `bun:",skipupdate"`
				}
				models := []Model{
					{42, "hello", "skip"},
					{43, "world", "skip"},
				}
				return db.NewUpdate().
					Model(&models).
					Bulk()
			},
		},
		{
			id: 72,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().
					WhereGroup("", func(q *pgcrud.SelectQuery) *pgcrud.SelectQuery {
						return q.Where("a = 1").Where("b = 1")
					}).
					WhereGroup(" OR ", func(q *pgcrud.SelectQuery) *pgcrud.SelectQuery {
						return q.Where("a = 2").Where("b = 2")
					})
			},
		},
		{
			id: 73,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				params := struct {
					A     int
					B     float32
					Alias pgcrud.Ident
				}{
					A:     1,
					B:     2.34,
					Alias: pgcrud.Ident("sum"),
				}
				return db.NewSelect().Where("?a + ?b AS ?alias", params)
			},
		},
		{
			id: 74,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					ID int `bun:",pk"`
				}
				return db.NewInsert().Model(new(Model))
			},
		},
		{
			id: 75,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				user := &User{Name: "Hello"}
				return db.NewUpdate().Model(user).Set("name = ?name").Where("id = ?id")
			},
		},
		{
			id: 76,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				user := &User{ID: 42}
				return db.NewDelete().Model(user).Where("id = ?id")
			},
		},
		{
			id: 77,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				user := &User{Name: "Hello"}
				return db.NewInsert().Model(user).On("CONFLICT DO UPDATE").Set("name = ?name")
			},
		},
		{
			id: 78,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().WhereGroup(" AND ", func(q *pgcrud.SelectQuery) *pgcrud.SelectQuery {
					return q.WhereOr("one").WhereOr("two")
				})
			},
		},
		{
			id: 79,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					ID   int64 `bun:",pk,autoincrement"`
					Str1 string
					Str2 string
				}

				models := []Model{
					{42, "hello", "world"},
					{43, "foo", "bar"},
				}
				return db.NewUpdate().
					Model(&models).
					Column("str2").
					Bulk()
			},
		},
		{
			id: 80,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				models := []Model{
					{42, "hello"},
					{43, "foo"},
				}
				return db.NewInsert().
					Model(&models).
					Value("str", "?", "custom")
			},
		},
		{
			id: 81,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				models := []Model{
					{42, "hello"},
					{43, "foo"},
				}
				return db.NewUpdate().
					Model(&models).
					Value("str", "?", "custom").
					Bulk()
			},
		},
		{
			id: 82,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				model := &Model{42, "hello"}
				return db.NewInsert().
					Model(model).
					On("CONFLICT (id) DO UPDATE")
			},
		},
		{
			id: 84,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					Time time.Time `bun:",notnull"`
				}
				return db.NewInsert().Model(new(Model))
			},
		},
		{
			id: 85,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					ID int64
				}
				return db.NewInsert().Model(new(Model))
			},
		},
		{
			id: 86,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewInsert().Model(new(SoftDelete1)).On("CONFLICT DO NOTHING")
			},
		},
		{
			id: 87,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewUpdate().Model(&Model{ID: 42}).OmitZero().WherePK()
			},
		},
		{
			id: 88,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					ID   int64 `bun:",pk,autoincrement"`
					Time time.Time
				}
				return db.NewInsert().Model(&Model{ID: 123, Time: time.Unix(0, 0)})
			},
		},
		{
			id: 89,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewInsert().ColumnExpr("id, name").Table("dest").Table("src")
			},
		},
		{
			id: 90,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewDelete().Model(new(SoftDelete2)).WherePK()
			},
		},
		{
			id: 91,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewDelete().Model(new(SoftDelete2)).WherePK().ForceDelete()
			},
		},
		{
			id: 92,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(SoftDelete2))
			},
		},
		{
			id: 93,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(SoftDelete2)).WhereDeleted()
			},
		},
		{
			id: 94,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(SoftDelete2)).WhereAllWithDeleted()
			},
		},
		{
			id: 95,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewInsert().Model(new(SoftDelete2)).On("CONFLICT DO NOTHING")
			},
		},
		{
			id: 96,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewInsert().Model(&Model{}).Returning("")
			},
		},
		{
			id: 99,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				models := []Model{
					{ID: 1},
					{ID: 2},
				}
				return db.NewSelect().Model(&models).WherePK()
			},
		},
		{
			id: 100,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				models := []Model{
					{ID: 1, Str: "hello"},
					{ID: 2, Str: "world"},
				}
				return db.NewSelect().Model(&models).WherePK("id", "str")
			},
		},
		{
			id: 104,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					ID   int64     `bun:",pk,autoincrement"`
					Int  int64     `bun:",nullzero"`
					Uint uint64    `bun:",nullzero"`
					Str  string    `bun:",nullzero"`
					Time time.Time `bun:",nullzero"`
				}
				return db.NewUpdate().
					Model(new(Model)).
					Set("int = ?int").
					Set("uint = ?uint").
					Set("str = ?str").
					Set("time = ?time").
					WherePK()
			},
		},
		{
			id: 105,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type ID string
				type Model struct {
					ID ID
				}
				return db.NewInsert().Model(&Model{ID: ID("embed")})
			},
		},
		{
			id: 106,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					Raw *json.RawMessage
				}
				return db.NewInsert().Model(new(Model))
			},
		},
		{
			id: 107,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				models := []Model{
					{42, "hello"},
					{43, "foo"},
				}
				return db.NewInsert().
					Model(&models).
					Value("extra", "?", "custom")
			},
		},
		{
			id: 108,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Item struct {
					Foo string
					Bar string
				}
				type Model struct {
					ID    int64
					Slice []Item `bun:",nullzero"`
				}
				return db.NewInsert().Model(&Model{ID: 123, Slice: make([]Item, 0)})
			},
		},
		{
			id: 109,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					Time *time.Time
				}
				return db.NewInsert().Model(new(Model))
			},
		},
		{
			id: 110,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					Time *time.Time
				}
				tm := time.Unix(0, 0)
				return db.NewInsert().Model(&Model{Time: &tm})
			},
		},
		{
			id: 111,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				values := [][]byte{
					[]byte("foo"),
					[]byte("bar"),
				}
				return db.NewSelect().Where("x IN ?", pgcrud.Tuple(values))
			},
		},
		{
			id: 112,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().
					QueryBuilder().
					WhereGroup("", func(q pgcrud.QueryBuilder) pgcrud.QueryBuilder {
						return q.Where("a = 1").Where("b = 1")
					}).
					WhereGroup(" OR ", func(q pgcrud.QueryBuilder) pgcrud.QueryBuilder {
						return q.Where("a = 2").Where("b = 2")
					})
			},
		},
		{
			id: 113,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(Model)).QueryBuilder().Where("id = 42")
			},
		},
		{
			id: 114,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(Model)).QueryBuilder().WhereOr("id = 42")
			},
		},
		{
			id: 115,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(SoftDelete1)).QueryBuilder().WherePK()
			},
		},
		{
			id: 116,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(SoftDelete1)).QueryBuilder().WhereDeleted()
			},
		},
		{
			id: 117,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(SoftDelete1)).QueryBuilder().WhereAllWithDeleted()
			},
		},
		{
			id: 118,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewUpdate().Model(new(SoftDelete1)).QueryBuilder().WherePK().
					WhereGroup("", func(q pgcrud.QueryBuilder) pgcrud.QueryBuilder {
						return q.Where("a = 1").Where("b = 1")
					}).
					WhereGroup(" OR ", func(q pgcrud.QueryBuilder) pgcrud.QueryBuilder {
						return q.Where("a = 2").Where("b = 2")
					})
			},
		},
		{
			id: 119,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewUpdate().Model(new(Model)).QueryBuilder().Where("id = 42")
			},
		},
		{
			id: 120,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewUpdate().Model(new(Model)).QueryBuilder().WhereOr("id = 42")
			},
		},
		{
			id: 121,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewUpdate().Model(new(SoftDelete1)).QueryBuilder().WherePK()
			},
		},
		{
			id: 122,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewUpdate().Model(new(SoftDelete1)).QueryBuilder().WherePK().WhereDeleted()
			},
		},
		{
			id: 123,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewUpdate().
					Model(new(SoftDelete1)).
					QueryBuilder().
					WherePK().
					WhereAllWithDeleted()
			},
		},
		{
			id: 124,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewDelete().
					Model(new(SoftDelete1)).
					QueryBuilder().
					WherePK().
					WhereGroup("", func(q pgcrud.QueryBuilder) pgcrud.QueryBuilder {
						return q.Where("a = 1").Where("b = 1")
					}).
					WhereGroup(" OR ", func(q pgcrud.QueryBuilder) pgcrud.QueryBuilder {
						return q.Where("a = 2").Where("b = 2")
					})
			},
		},
		{
			id: 125,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewDelete().Model(new(Model)).QueryBuilder().Where("id = 42")
			},
		},
		{
			id: 126,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewDelete().Model(new(Model)).QueryBuilder().WhereOr("id = 42")
			},
		},
		{
			id: 127,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewDelete().Model(new(SoftDelete1)).QueryBuilder().WherePK()
			},
		},
		{
			id: 128,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewDelete().Model(new(SoftDelete1)).QueryBuilder().WherePK().WhereDeleted()
			},
		},
		{
			id: 129,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewDelete().
					Model(new(SoftDelete1)).
					QueryBuilder().
					WherePK().
					WhereAllWithDeleted()
			},
		},
		{
			id: 130,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					ID        int64 `bun:",pk"`
					UpdatedAt time.Time
				}
				return db.NewUpdate().
					Model(&Model{}).
					OmitZero().
					WherePK().
					Value("updated_at", "NOW()").
					Returning("*")
			},
		},
		{
			id: 146,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().
					Model((*Model)(nil)).
					Order("id DESC").
					Limit(20)
			},
		},
		{
			id: 147,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().
					Model((*Model)(nil)).
					Order("id DESC").
					Offset(20).
					Limit(20)
			},
		},
		{
			id: 148,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().
					Model((*Model)(nil)).
					Order("id DESC").
					Offset(20)
			},
		},
		{
			id: 152,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					ID           int64 `bun:",pk,autoincrement"`
					SoftDeleteID int64
					SoftDelete   *SoftDelete1 `bun:"rel:belongs-to"`
				}
				return db.NewSelect().Model(new(Model)).Relation("SoftDelete")
			},
		},
		{
			id: 153,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					ID           int64 `bun:",pk,autoincrement"`
					SoftDeleteID int64
					SoftDelete   *SoftDelete2 `bun:"rel:belongs-to"`
				}
				return db.NewSelect().Model(new(Model)).Relation("SoftDelete")
			},
		},
		{
			id: 158,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewUpdate().TableExpr("xxx").Set("foo = ?", pgcrud.NullZero("")).Where("1")
			},
		},
		{
			id: 159,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewUpdate().Model(new(Model)).OmitZero().WherePK()
			},
		},
		{
			id: 160,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewUpdate().Model(&Model{Str: ""}).OmitZero().WherePK()
			},
		},
		{
			id: 161,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewUpdate().Model(&Model{Str: ""}).WherePK()
			},
		},
		{
			id: 162,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewUpdate().Model(&Model{42, ""}).OmitZero()
			},
		},
		{
			id: 163,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewUpdate().
					Model((*Story)(nil)).
					Set("name = ?", "new-name").
					Join("JOIN user ON user.id = story.user_id").
					Where("user.id = ?", 1)
			},
		},
		{
			id: 165,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewUpdate().Model(&struct {
					pgcrud.BaseModel `bun:"table:accounts"`
					ID               int  `bun:"id,pk,autoincrement"`
					IsActive         bool `bun:"is_active,notnull,default:true"`
				}{
					ID:       1,
					IsActive: false,
				}).Column("is_active").WherePK()
			},
		},
		{
			id: 166,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				// "default" marshals zero values as DEFAULT or the specified default value
				type Model struct {
					Int      int64     `bun:",default:42"`
					Uint     uint64    `bun:",default:42"`
					Str      string    `bun:",default:'hello'"`
					Time     time.Time `bun:",default:now()"`
					Bool     bool      `bun:",default:true"`
					EmptyStr string    `bun:",default:''"`
				}
				return db.NewInsert().Model(new(Model))
			},
		},
		{
			id: 167,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				// specified option names
				type Model struct {
					pgcrud.BaseModel `bun:"table:table"`
					IsDefault        bool `bun:"column:default"`
				}
				return db.NewInsert().Model(new(Model))
			},
		},
		{
			id: 172,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewDelete().Model(&Model{}).WherePK().Returning("*")
			},
		},
		{
			id: 175,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewDelete().
					Model(&Model{}).
					WherePK().
					Comment("test")
			},
		},
		{
			id: 178,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewInsert().
					Model(&Model{}).
					Comment("test").
					Value("column_name", "value")
			},
		},
		{
			id: 180,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewRaw("SELECT 1").Comment("test")
			},
		},
		{
			id: 181,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().
					Model(&Model{}).
					Comment("test")
			},
		},
		{
			id: 185,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewUpdate().
					Model(&Model{}).
					Comment("test").
					Set("name = ?", "new-name").
					Where("id = ?", 1)
			},
		},
		{
			id: 186,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewValues(&[]Model{{1, "hello"}}).
					Comment("test")
			},
		},
		{
			id: 188,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					Name string
				}
				model := &Model{
					Name: "name1",
				}
				return db.NewUpdate().
					Model(model).
					Column("name").
					Set("updated_at = ?", time.Now()).
					Where("id = ?", 1)
			},
		},
		{
			id: 189,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					Name string
				}
				model := &Model{
					Name: "name1",
				}
				return db.NewUpdate().
					Model(model).
					Column("name").
					Value("name", "name2").
					Set("updated_at = ?", time.Now()).
					Where("id = ?", 1)
			},
		},
		{
			id: 190,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					Name string
				}
				model := &Model{
					Name: "name1",
				}
				return db.NewUpdate().
					Model(model).
					ExcludeColumn().
					Set("updated_at = ?", time.Now()).
					Where("id = ?", 1)
			},
		},
		{
			id: 191,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					Name string
				}
				type Values struct {
					Foo *string
					Bar *int64
				}
				model := &Model{
					Name: "name1",
				}
				values := &Values{
					Foo: new(string),
				}
				return db.NewInsert().
					Model(model).
					On("CONFLICT DO UPDATE").
					SetValues(db.NewValues(values).OmitZero())
			},
		},
		{
			id: 192,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				type Model struct {
					Name string
				}
				type Values struct {
					Foo *string
					Bar *int64
				}
				model := &Model{
					Name: "name1",
				}
				values := map[string]any{
					"foo": "string",
					"bar": nil,
				}
				return db.NewInsert().
					Model(model).
					On("CONFLICT DO UPDATE").
					SetValues(db.NewValues(&values))
			},
		},
		{
			id: 194,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().
					OrderBy("foo", pgcrud.OrderAsc).
					OrderBy("foo.bar", pgcrud.OrderDesc).
					OrderBy("xxx", pgcrud.Order("bad"))
			},
		},
		{
			id: 195,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Where("id IN (?)", pgcrud.List([]int{1, 2, 3}))
			},
		},
		{
			id: 196,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Where("id IN ?", pgcrud.Tuple([]int{1, 2, 3}))
			},
		},
		{
			id: 197,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Where("(id1, id2) IN (?)", pgcrud.List([][]int{{1, 2}, {3, 4}}))
			},
		},
		{
			id: 198,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Where("id IN ?", pgcrud.Tuple([][]int{{1, 2}, {3, 4}}))
			},
		},
		{
			id: 199,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Where("name IN (?)", pgcrud.List([]string{"foo", "bar"}))
			},
		},
		{
			id: 200,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Where("id IN (?)", pgcrud.List([]int{}))
			},
		},
		{
			id: 201,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Where("id IN ?", pgcrud.Tuple([]int{}))
			},
		},
		{
			id: 202,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Where("id IN ?", pgcrud.Tuple(nil))
			},
		},
		{
			id: 203,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Where("id IN ?", pgcrud.Tuple([][]byte{
					[]byte("hello"),
					[]byte("world"),
				}))
			},
		},
		{
			id: 204,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Where("id IN ?", pgcrud.Tuple([][16]byte{
					{0x6b, 0xa7, 0xb8, 0x10, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8},
					{0x6b, 0xa7, 0xb8, 0x11, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8},
				}))
			},
		},
		{
			id: 205,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(SoftDelete1)).
					Where("id = 1").
					WhereOr("id = 2")
			},
		},
		{
			id: 206,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(SoftDelete1)).
					WhereOr("id = 1").
					WhereOr("id = 2")
			},
		},
		{
			id: 207,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(SoftDelete1)).
					Where("id = 1").
					WhereOr("id = 2").
					WhereDeleted()
			},
		},
		{
			id: 208,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewSelect().Model(new(SoftDelete1)).
					Where("id = 1").
					Where("id = 2")
			},
		},
		{
			id: 209,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewDelete().Model(new(SoftDelete1)).
					Where("id = 1").
					WhereOr("id = 2")
			},
		},
		{
			id: 210,
			query: func(db *pgcrud.DB) schema.QueryAppender {
				return db.NewUpdate().Model(new(SoftDelete1)).
					Set("id = id").
					Where("id = 1").
					WhereOr("id = 2")
			},
		},
	}

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
