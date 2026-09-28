package schema

import (
	"database/sql"
	"encoding/json"
	"net"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/piprim/pgcrud/dialect"
)

// bindTestDialect is a non-nop dialect so AppendQuery substitutes ? args.
type bindTestDialect struct {
	*nopDialect
	uintAsInt bool
}

func (bindTestDialect) Name() dialect.Name { return dialect.PG }
func (d bindTestDialect) UintAsInt() bool  { return d.uintAsInt }

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

func TestDialectHasNoValueAppenders(t *testing.T) {
	t.Run("Dialect interface no longer carries value appenders", func(t *testing.T) {
		typ := reflect.TypeFor[Dialect]()
		for _, name := range []string{"AppendString", "AppendBytes", "AppendJSON", "AppendTime", "AppendBool", "AppendUint32", "AppendUint64"} {
			_, found := typ.MethodByName(name)
			require.False(t, found, name)
		}
	})
}
