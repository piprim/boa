package pgdialect

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/piprim/boa/internal"
)

func TestAppendTime(t *testing.T) {
	t.Run("a finite time is written in UTC with microseconds", func(t *testing.T) {
		tm := time.Date(2026, time.April, 2, 10, 30, 0, 123456000, time.UTC)
		require.Equal(t, "2026-04-02 10:30:00.123456+00:00", string(appendTime(nil, tm)))
	})

	t.Run("the infinity sentinels are written as PostgreSQL spells them", func(t *testing.T) {
		require.Equal(t, "infinity", string(appendTime(nil, internal.PosInfinity)))
		require.Equal(t, "-infinity", string(appendTime(nil, internal.NegInfinity)))
	})
}
