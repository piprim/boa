package boa

import (
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// lastBuiltinOID is Postgres's FirstNormalObjectId: every built-in type has a
// smaller OID, and pgx registers only built-in types by default.
const lastBuiltinOID = 16384

// textResultFormats returns the result-format overrides boa passes on
// every Query and QueryRow. Every array, range and multirange type pgx knows
// is requested in text, because the library parses those from their text
// form; pgx would otherwise hand the scanners binary wire bytes.
func textResultFormats() pgx.QueryResultFormatsByOID {
	m := pgtype.NewMap()
	formats := make(pgx.QueryResultFormatsByOID)
	for oid := uint32(1); oid < lastBuiltinOID; oid++ {
		typ, ok := m.TypeForOID(oid)
		if !ok {
			continue
		}
		switch typ.Codec.(type) {
		case *pgtype.ArrayCodec, *pgtype.RangeCodec, *pgtype.MultirangeCodec:
			formats[oid] = pgx.TextFormatCode
		}
	}
	return formats
}
