package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIdent_On(t *testing.T) {
	t.Run("replaces the qualifier of a qualified identifier", func(t *testing.T) {
		require.Equal(t, Ident("mandat.is_exclusif"), Ident("t.is_exclusif").On("mandat"))
	})

	t.Run("qualifies a bare identifier", func(t *testing.T) {
		require.Equal(t, Ident("mandat.is_exclusif"), Ident("is_exclusif").On("mandat"))
	})

	t.Run("replaces a schema-qualified prefix as a whole", func(t *testing.T) {
		require.Equal(t, Ident("m.id"), Ident("public.mandat.id").On("m"))
	})

	t.Run("is written quoted, alias and column apart", func(t *testing.T) {
		gen := NewQueryGen(NewTables(nil), false).WithArgList(NewArgList())
		b := gen.Append(nil, Ident("t.enabled").On("mandat__ccial"))
		require.Equal(t, `"mandat__ccial"."enabled"`, string(b))
	})
}
