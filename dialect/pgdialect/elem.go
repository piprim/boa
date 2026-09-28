package pgdialect

import (
	"database/sql/driver"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"time"
	"unicode/utf8"
)

// appendElem writes one range bound in the text form the Postgres range
// parser reads. Strings are double-quoted with backslash escapes.
func appendElem(buf []byte, val any) ([]byte, error) {
	switch val := val.(type) {
	case time.Time:
		buf = append(buf, '"')
		buf = appendTime(buf, val)
		return append(buf, '"'), nil
	case []byte:
		return appendStringElem(buf, string(val)), nil
	case driver.Valuer:
		v, err := val.Value()
		if err != nil {
			return nil, fmt.Errorf("pgdialect: can't append elem value: %w", err)
		}
		return appendElem(buf, v)
	}

	rv := reflect.ValueOf(val)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.AppendInt(buf, rv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.AppendUint(buf, rv.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return appendFloat64Elem(buf, rv.Float()), nil
	case reflect.String:
		return appendStringElem(buf, rv.String()), nil
	}
	return nil, fmt.Errorf("pgdialect: can't append elem %T", val)
}

func appendFloat64Elem(b []byte, num float64) []byte {
	switch {
	case math.IsNaN(num):
		return append(b, "NaN"...)
	case math.IsInf(num, 1):
		return append(b, "Infinity"...)
	case math.IsInf(num, -1):
		return append(b, "-Infinity"...)
	default:
		return strconv.AppendFloat(b, num, 'f', -1, 64)
	}
}

// appendStringElem writes s double-quoted for the array, range and hstore
// text parsers: a double quote and a backslash are escaped by a backslash.
// It performs no SQL-literal escaping, because the text is bound, not inlined.
func appendStringElem(b []byte, s string) []byte {
	b = append(b, '"')
	for _, r := range s {
		switch r {
		case 0:
			// Postgres rejects NUL in text; dropping it keeps the parser happy.
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		default:
			if r < utf8.RuneSelf {
				b = append(b, byte(r))
				break
			}
			l := len(b)
			if cap(b)-l < utf8.UTFMax {
				b = append(b, make([]byte, utf8.UTFMax)...)
			}
			n := utf8.EncodeRune(b[l:l+utf8.UTFMax], r)
			b = b[:l+n]
		}
	}
	return append(b, '"')
}
