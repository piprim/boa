package pgdialect

import (
	"fmt"
	"reflect"
	"time"

	"github.com/piprim/pgcrud/dialect"
	"github.com/piprim/pgcrud/schema"
)

var (
	stringType  = reflect.TypeFor[string]()
	intType     = reflect.TypeFor[int]()
	int64Type   = reflect.TypeFor[int64]()
	float64Type = reflect.TypeFor[float64]()
	timeType    = reflect.TypeFor[time.Time]()
)

func appendTime(buf []byte, tm time.Time) []byte {
	return tm.UTC().AppendFormat(buf, "2006-01-02 15:04:05.999999-07:00")
}

var mapStringStringType = reflect.TypeOf(map[string]string(nil))

func hstoreAppender(typ reflect.Type) schema.AppenderFunc {
	kind := typ.Kind()

	switch kind {
	case reflect.Pointer:
		if fn := hstoreAppender(typ.Elem()); fn != nil {
			return schema.PtrAppender(fn)
		}
	case reflect.Map:
		// ok:
	default:
		return nil
	}

	if typ.Key() == stringType && typ.Elem() == stringType {
		return bindMapStringString
	}

	return func(gen schema.QueryGen, b []byte, v reflect.Value) []byte {
		err := fmt.Errorf("pgcrud: Hstore(unsupported %s)", v.Type())
		return gen.BindError(b, err)
	}
}

// bindMapStringString binds the hstore text form as a string. Postgres
// coerces a text parameter to hstore; pgx has no fixed OID for it.
func bindMapStringString(gen schema.QueryGen, b []byte, v reflect.Value) []byte {
	if v.IsNil() {
		return dialect.AppendNull(b)
	}
	m := v.Convert(mapStringStringType).Interface().(map[string]string)
	return gen.Bind(b, hstoreText(m))
}

// hstoreText renders m in hstore input syntax: "k"=>"v",... with quotes and
// backslashes escaped by a backslash.
func hstoreText(m map[string]string) string {
	var b []byte
	for key, value := range m {
		b = appendStringElem(b, key)
		b = append(b, '=', '>')
		b = appendStringElem(b, value)
		b = append(b, ',')
	}
	if len(m) > 0 {
		b = b[:len(b)-1]
	}
	return string(b)
}
