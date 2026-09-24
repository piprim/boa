package pgdialect

import (
	"database/sql"
	"encoding/json"
	"net"
	"reflect"

	"github.com/piprim/pgcrud/dialect/sqltype"
	"github.com/piprim/pgcrud/schema"
)

const (
	// Date / Time
	pgTypeTimestamp       = "TIMESTAMP"                // Timestamp
	pgTypeTimestampWithTz = "TIMESTAMP WITH TIME ZONE" // Timestamp with a time zone
	pgTypeTimestampTz     = "TIMESTAMPTZ"              // Timestamp with a time zone (alias)
	pgTypeDate            = "DATE"                     // Date
	pgTypeTime            = "TIME"                     // Time without a time zone
	pgTypeTimeTz          = "TIME WITH TIME ZONE"      // Time with a time zone
	pgTypeInterval        = "INTERVAL"                 // Time interval

	// Network Addresses
	pgTypeInet    = "INET"    // IPv4 or IPv6 hosts and networks
	pgTypeCidr    = "CIDR"    // IPv4 or IPv6 networks
	pgTypeMacaddr = "MACADDR" // MAC addresses

	// Serial Types
	pgTypeSmallSerial = "SMALLSERIAL" // 2 byte autoincrementing integer
	pgTypeSerial      = "SERIAL"      // 4 byte autoincrementing integer
	pgTypeBigSerial   = "BIGSERIAL"   // 8 byte autoincrementing integer

	// Character Types
	pgTypeChar             = "CHAR"              // fixed length string (blank padded)
	pgTypeCharacter        = "CHARACTER"         // alias for CHAR
	pgTypeText             = "TEXT"              // variable length string without limit
	pgTypeVarchar          = "VARCHAR"           // variable length string with optional limit
	pgTypeCharacterVarying = "CHARACTER VARYING" // alias for VARCHAR

	// Binary Data Types
	pgTypeBytea = "BYTEA" // binary string
)

var (
	ipType             = reflect.TypeFor[net.IP]()
	ipNetType          = reflect.TypeFor[net.IPNet]()
	jsonRawMessageType = reflect.TypeFor[json.RawMessage]()
	nullStringType     = reflect.TypeFor[sql.NullString]()
)

func (d *Dialect) DefaultVarcharLen() int {
	return 0
}

func (d *Dialect) DefaultSchema() string {
	return "public"
}

func fieldSQLType(field *schema.Field) string {
	if field.UserSQLType != "" {
		return field.UserSQLType
	}

	if v, ok := field.Tag.Option("composite"); ok {
		return v
	}
	if field.Tag.HasOption("hstore") {
		return sqltype.HSTORE
	}

	if field.Tag.HasOption("array") {
		switch field.IndirectType.Kind() {
		case reflect.Slice, reflect.Array:
			sqlType := sqlType(field.IndirectType.Elem())
			return sqlType + "[]"
		}
	}

	if field.DiscoveredSQLType == sqltype.Blob {
		return pgTypeBytea
	}

	return sqlType(field.IndirectType)
}

func sqlType(typ reflect.Type) string {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	switch typ {
	case nullStringType: // typ.Kind() == reflect.Struct, test for exact match
		return sqltype.VarChar
	case ipType:
		return pgTypeInet
	case ipNetType:
		return pgTypeCidr
	case jsonRawMessageType:
		return sqltype.JSONB
	}

	sqlType := schema.DiscoverSQLType(typ)
	switch sqlType {
	case sqltype.Timestamp:
		sqlType = pgTypeTimestampTz
	}

	switch typ.Kind() {
	case reflect.Map, reflect.Struct: // except typ == nullStringType, see above
		if sqlType == sqltype.VarChar {
			return sqltype.JSONB
		}
		return sqlType
	case reflect.Array, reflect.Slice:
		if typ.Elem().Kind() == reflect.Uint8 {
			return pgTypeBytea
		}
		return sqltype.JSONB
	}

	return sqlType
}
