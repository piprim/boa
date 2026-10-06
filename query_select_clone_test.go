package boa

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/piprim/boa/schema"
)

// Regression test for #1388: Clone must copy execution state, not only the
// public builder slices.
func TestSelectQueryCloneCopiesExecutionState(t *testing.T) {
	sentinelErr := errors.New("sentinel")
	pk := &schema.Field{Name: "id"}

	q := &SelectQuery{}
	q.err = sentinelErr
	q.flags = q.flags.Set(deletedFlag)
	q.whereFields = []*schema.Field{pk}
	q.with = []WithQuery{
		{name: "mat", materialized: true},
		{name: "not_mat", notMaterialized: true},
	}

	clone := q.Clone()

	t.Run("err is copied", func(t *testing.T) {
		require.True(t, clone.err == sentinelErr, "err must be copied")
	})

	t.Run("flags are copied", func(t *testing.T) {
		require.True(t, clone.flags.Has(deletedFlag), "flags must be copied")
	})

	t.Run("whereFields are copied", func(t *testing.T) {
		require.Equal(t, []*schema.Field{pk}, clone.whereFields)
	})

	t.Run("materialized CTE flag is copied", func(t *testing.T) {
		require.True(t, clone.with[0].materialized, "materialized must be copied")
	})

	t.Run("not materialized CTE flag is copied", func(t *testing.T) {
		require.True(t, clone.with[1].notMaterialized, "notMaterialized must be copied")
	})

	t.Run("whereFields slice is not aliased", func(t *testing.T) {
		// The whereFields slice must be a copy, not an alias.
		clone.whereFields[0] = &schema.Field{Name: "other"}
		require.True(t, q.whereFields[0] == pk, "whereFields must not alias the original")
	})
}

func TestSelectQueryCloneNilWhereFields(t *testing.T) {
	t.Run("nil whereFields stay nil", func(t *testing.T) {
		q := &SelectQuery{}
		require.Nil(t, q.Clone().whereFields)
	})

	t.Run("cloning a nil query returns nil", func(t *testing.T) {
		var nilQuery *SelectQuery
		require.Nil(t, nilQuery.Clone())
	})
}
