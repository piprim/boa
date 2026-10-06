package boa

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

func TestTextResultFormats(t *testing.T) {
	formats := textResultFormats()

	t.Run("arrays are text", func(t *testing.T) {
		require.Equal(t, int16(pgx.TextFormatCode), formats[pgtype.Int4ArrayOID])
		require.Equal(t, int16(pgx.TextFormatCode), formats[pgtype.TextArrayOID])
		require.Equal(t, int16(pgx.TextFormatCode), formats[pgtype.TimestamptzArrayOID])
	})

	t.Run("ranges and multiranges are text", func(t *testing.T) {
		require.Equal(t, int16(pgx.TextFormatCode), formats[pgtype.TstzrangeOID])
		require.Equal(t, int16(pgx.TextFormatCode), formats[pgtype.Int8rangeOID])
		require.Equal(t, int16(pgx.TextFormatCode), formats[pgtype.Int4multirangeOID])
	})

	t.Run("scalars are not listed", func(t *testing.T) {
		_, ok := formats[pgtype.Int8OID]
		require.False(t, ok)
		_, ok = formats[pgtype.TextOID]
		require.False(t, ok)
		_, ok = formats[pgtype.JSONBOID]
		require.False(t, ok)
		_, ok = formats[pgtype.ByteaOID]
		require.False(t, ok)
	})

	t.Run("every listed OID has an array, range or multirange codec", func(t *testing.T) {
		m := pgtype.NewMap()
		for oid := range formats {
			typ, ok := m.TypeForOID(oid)
			require.True(t, ok, oid)
			switch typ.Codec.(type) {
			case *pgtype.ArrayCodec, *pgtype.RangeCodec, *pgtype.MultirangeCodec:
			default:
				t.Fatalf("oid %d has codec %T", oid, typ.Codec)
			}
		}
	})
}
