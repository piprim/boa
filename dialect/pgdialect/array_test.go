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
	return schema.NewQueryGen(New().Tables(), false).WithArgList(list), list
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
