package schema

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

// tri distinguishes unset from null like presence.Of: IsUnset has a pointer
// receiver, as presence's does.
type tri struct {
	set bool
	val *string
}

func (t *tri) IsUnset() bool { return !t.set }

func TestFieldHasUnsetValue(t *testing.T) {
	type Inner struct {
		Note tri
	}
	type Model struct {
		ID    int64 `boa:",pk"`
		Name  string
		Note  tri
		PNote *tri
		Inner *Inner `boa:",embed:inner_"`
	}
	table := NewTables(nil).Get(reflect.TypeFor[*Model]())
	field := func(name string) *Field {
		f, ok := table.FieldMap[name]
		require.True(t, ok, name)
		return f
	}

	t.Run("a type without IsUnset has no checker", func(t *testing.T) {
		require.Nil(t, field("name").IsUnset)
		require.False(t, field("name").HasUnsetValue(reflect.ValueOf(&Model{}).Elem()))
	})

	t.Run("zero tri is unset", func(t *testing.T) {
		require.NotNil(t, field("note").IsUnset)
		require.True(t, field("note").HasUnsetValue(reflect.ValueOf(&Model{}).Elem()))
	})

	t.Run("set tri is not unset", func(t *testing.T) {
		m := Model{Note: tri{set: true}}
		require.False(t, field("note").HasUnsetValue(reflect.ValueOf(&m).Elem()))
	})

	t.Run("nil pointer to tri is not unset and does not panic", func(t *testing.T) {
		require.False(t, field("p_note").HasUnsetValue(reflect.ValueOf(&Model{}).Elem()))
	})

	t.Run("pointer to an unset tri is unset", func(t *testing.T) {
		m := Model{PNote: &tri{}}
		require.True(t, field("p_note").HasUnsetValue(reflect.ValueOf(&m).Elem()))
	})

	t.Run("field behind a nil embedded pointer is not unset and does not panic", func(t *testing.T) {
		require.False(t, field("inner_note").HasUnsetValue(reflect.ValueOf(&Model{}).Elem()))
	})

	t.Run("field behind a set embedded pointer is checked", func(t *testing.T) {
		m := Model{Inner: &Inner{}}
		require.True(t, field("inner_note").HasUnsetValue(reflect.ValueOf(&m).Elem()))
	})
}
