package boa

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/piprim/boa/schema"
)

func TestListAndTuple(t *testing.T) {
	db := New(nil)

	uuid := [16]byte{0x6b, 0xa7, 0xb8, 0x10, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}

	tests := []struct {
		name string
		q    schema.QueryAppender
		want string
		args []any
	}{
		{"List([]int)", List([]int{1, 2, 3}), "$1, $2, $3", []any{int64(1), int64(2), int64(3)}},
		{"List([]string)", List([]string{"foo", "bar"}), "$1, $2", []any{"foo", "bar"}},
		{"List([][]byte)", List([][]byte{[]byte("hello"), []byte("world")}), "$1, $2", []any{[]byte("hello"), []byte("world")}},
		{"List([][16]byte)", List([][16]byte{uuid}), "$1", []any{uuid[:]}},
		{"List([][]int) - no recursion, inner slices are JSON", List([][]int{{1, 2}, {3, 4}}), "$1, $2", []any{[]byte("[1,2]"), []byte("[3,4]")}},
		{"List([]int) empty", List([]int{}), "NULL", []any{}},
		{"Tuple([]int)", Tuple([]int{1, 2, 3}), "($1, $2, $3)", []any{int64(1), int64(2), int64(3)}},
		{"Tuple([][]int)", Tuple([][]int{{1, 2}, {3, 4}}), "(($1, $2), ($3, $4))", []any{int64(1), int64(2), int64(3), int64(4)}},
		{"Tuple([][]byte)", Tuple([][]byte{[]byte("hello"), []byte("world")}), "($1, $2)", []any{[]byte("hello"), []byte("world")}},
		{"Tuple([][16]byte)", Tuple([][16]byte{uuid}), "($1)", []any{uuid[:]}},
		{"Tuple([]int) empty", Tuple([]int{}), "(NULL)", []any{}},
		{"Tuple(nil)", Tuple(nil), "(NULL)", []any{}},
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

	t.Run("List(non-slice) is an error", func(t *testing.T) {
		_, err := List(42).AppendQuery(db.QueryGen(), nil)
		require.Error(t, err)
	})

	t.Run("Tuple(non-slice) is an error", func(t *testing.T) {
		_, err := Tuple(42).AppendQuery(db.QueryGen(), nil)
		require.Error(t, err)
	})
}
