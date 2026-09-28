package schema

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArgList(t *testing.T) {
	t.Run("Bind numbers placeholders from $1 and collects values in order", func(t *testing.T) {
		list := NewArgList()
		gen := NewQueryGen(NewTables(nil), false).WithArgList(list)
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
		gen := NewQueryGen(NewTables(nil), false)
		require.Equal(t, "?", string(gen.Bind(nil, 42)))
		require.Nil(t, gen.Args())
	})

	t.Run("WithArg and WithNamedArg keep the list", func(t *testing.T) {
		list := NewArgList()
		gen := NewQueryGen(NewTables(nil), false).WithArgList(list)
		gen = gen.WithNamedArg("n", 1).WithArg(&namedArg{name: "m", value: 2})
		gen.Bind(nil, "v")
		require.Equal(t, []any{"v"}, list.Args())
	})

	t.Run("BindArgs pushes values without writing SQL", func(t *testing.T) {
		list := NewArgList()
		gen := NewQueryGen(NewTables(nil), false).WithArgList(list)
		gen.BindArgs(1, "two")
		require.Equal(t, []any{1, "two"}, list.Args())
		require.Equal(t, "$3", string(gen.Bind(nil, 3)))
	})

	t.Run("BindArgs without a list is a no-op", func(t *testing.T) {
		gen := NewQueryGen(NewTables(nil), false)
		gen.BindArgs(1)
		require.Nil(t, gen.Args())
	})

	t.Run("BindError records the first error with the position of the failing arg", func(t *testing.T) {
		list := NewArgList()
		gen := NewQueryGen(NewTables(nil), false).WithArgList(list)
		gen.Bind(nil, 1)
		b := gen.BindError(nil, errors.New("boom"))
		gen.BindError(nil, errors.New("second"))
		require.Equal(t, "?!(boom)", string(b))
		require.EqualError(t, list.Err(), "pgcrud: bind arg 2: boom")
	})

	t.Run("BindError without a list only writes the marker", func(t *testing.T) {
		gen := NewQueryGen(NewTables(nil), false)
		require.Equal(t, "?!(boom)", string(gen.BindError(nil, errors.New("boom"))))
	})
}
