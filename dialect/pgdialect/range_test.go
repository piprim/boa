package pgdialect

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRangeAppend(t *testing.T) {
	t.Run("int64 range binds its text", func(t *testing.T) {
		gen, list := bindGen()
		got, err := NewRange[int64](1, 5).AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, "$1", string(got))
		require.Equal(t, []any{"[1,5)"}, list.Args())
	})

	t.Run("int range binds through its kind", func(t *testing.T) {
		gen, list := bindGen()
		_, err := NewRange[int](1, 5).AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, []any{"[1,5)"}, list.Args())
	})

	t.Run("time range quotes bounds", func(t *testing.T) {
		gen, list := bindGen()
		lo := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		hi := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		_, err := NewRange(lo, hi).AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, []any{`["2026-01-01 00:00:00+00:00","2026-01-02 00:00:00+00:00")`}, list.Args())
	})

	t.Run("string bound with a quote is escaped for the range parser only", func(t *testing.T) {
		gen, list := bindGen()
		_, err := NewRange("a'b", `c"d`).AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, []any{`["a'b","c\"d")`}, list.Args())
	})

	t.Run("empty range binds empty", func(t *testing.T) {
		gen, list := bindGen()
		_, err := NewEmptyRange[int64]().AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, []any{"empty"}, list.Args())
	})

	t.Run("unset bounds render as exclusive infinities", func(t *testing.T) {
		gen, list := bindGen()
		_, err := Range[int64]{Lower: 3, LowerBound: RangeBoundInclusiveLeft}.AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, []any{"[3,)"}, list.Args())
	})

	t.Run("multirange binds its text and nil is an empty set", func(t *testing.T) {
		gen, list := bindGen()
		m := MultiRange[int64]{NewRange[int64](1, 2), NewRange[int64](5, 6)}
		_, err := m.AppendQuery(gen, nil)
		require.NoError(t, err)
		_, err = MultiRange[int64](nil).AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, []any{"{[1,2),[5,6)}", "{}"}, list.Args())
	})

	t.Run("unsupported bound type is an error", func(t *testing.T) {
		gen, _ := bindGen()
		_, err := NewRange(struct{}{}, struct{}{}).AppendQuery(gen, nil)
		require.Error(t, err)
	})
}

func TestRangeScan(t *testing.T) {
	t.Run("int64 range scans from text", func(t *testing.T) {
		var r Range[int64]
		require.NoError(t, r.Scan("[1,5)"))
		require.Equal(t, NewRange[int64](1, 5), r)
	})

	t.Run("float64 range scans from text", func(t *testing.T) {
		var r Range[float64]
		require.NoError(t, r.Scan("(1.5,2.5]"))
		require.Equal(t, Range[float64]{Lower: 1.5, Upper: 2.5, LowerBound: RangeBoundExclusiveLeft, UpperBound: RangeBoundInclusiveRight}, r)
	})

	t.Run("string range unquotes and unescapes bounds", func(t *testing.T) {
		var r Range[string]
		require.NoError(t, r.Scan(`["a\"b","c\\d")`))
		require.Equal(t, `a"b`, r.Lower)
		require.Equal(t, `c\d`, r.Upper)
	})

	t.Run("time range scans from text", func(t *testing.T) {
		var r Range[time.Time]
		require.NoError(t, r.Scan(`["2026-01-01 00:00:00+00","2026-01-02 00:00:00+00")`))
		require.True(t, r.Lower.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	})

	t.Run("unsupported bound type is an error, not a panic", func(t *testing.T) {
		var r Range[struct{}]
		require.Error(t, r.Scan("[1,2)"))
	})
}
