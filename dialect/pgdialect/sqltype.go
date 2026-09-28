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
	pgTypeTimestampTz = "TIMESTAMPTZ" // Timestamp with a time zone
	pgTypeInet        = "INET"        // IPv4 or IPv6 hosts and networks
	pgTypeCidr        = "CIDR"        // IPv4 or IPv6 networks
	pgTypeBytea       = "BYTEA"       // binary string
)

var (
	ipType             = reflect.TypeFor[net.IP]()
	ipNetType          = reflect.TypeFor[net.IPNet]()
	jsonRawMessageType = reflect.TypeFor[json.RawMessage]()
	nullStringType     = reflect.TypeFor[sql.NullString]()
)

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
