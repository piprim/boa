package schema

import (
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/piprim/boa/dialect"
	"github.com/piprim/boa/dialect/sqltype"
)

type (
	AppenderFunc   func(gen QueryGen, b []byte, v reflect.Value) []byte
	CustomAppender func(typ reflect.Type) AppenderFunc
)

var appenders = []AppenderFunc{
	reflect.Bool:          AppendBoolValue,
	reflect.Int:           AppendIntValue,
	reflect.Int8:          AppendIntValue,
	reflect.Int16:         AppendIntValue,
	reflect.Int32:         AppendIntValue,
	reflect.Int64:         AppendIntValue,
	reflect.Uint:          AppendUintValue,
	reflect.Uint8:         AppendUintValue,
	reflect.Uint16:        AppendUintValue,
	reflect.Uint32:        appendUint32Value,
	reflect.Uint64:        appendUint64Value,
	reflect.Uintptr:       nil,
	reflect.Float32:       AppendFloat32Value,
	reflect.Float64:       AppendFloat64Value,
	reflect.Complex64:     nil,
	reflect.Complex128:    nil,
	reflect.Array:         AppendJSONValue,
	reflect.Chan:          nil,
	reflect.Func:          nil,
	reflect.Interface:     nil,
	reflect.Map:           AppendJSONValue,
	reflect.Pointer:       nil,
	reflect.Slice:         AppendJSONValue,
	reflect.String:        AppendStringValue,
	reflect.Struct:        AppendJSONValue,
	reflect.UnsafePointer: nil,
}

var appenderCache sync.Map // reflect.Type -> AppenderFunc

func FieldAppender(field *Field) AppenderFunc {
	fieldType := field.StructField.Type

	switch strings.ToUpper(field.UserSQLType) {
	case sqltype.JSON, sqltype.JSONB:
		if fieldType.Implements(driverValuerType) {
			return appendDriverValue
		}

		if fieldType.Kind() != reflect.Pointer {
			if reflect.PointerTo(fieldType).Implements(driverValuerType) {
				return addrAppender(appendDriverValue)
			}
		}

		return AppendJSONValue
	}

	return Appender(fieldType)
}

func Appender(typ reflect.Type) AppenderFunc {
	if v, ok := appenderCache.Load(typ); ok {
		return v.(AppenderFunc)
	}

	fn := appender(typ)

	if v, ok := appenderCache.LoadOrStore(typ, fn); ok {
		return v.(AppenderFunc)
	}
	return fn
}

func appender(typ reflect.Type) AppenderFunc {
	switch typ {
	case bytesType:
		return appendBytesValue
	case timeType:
		return appendTimeValue
	case timePtrType:
		return PtrAppender(appendTimeValue)
	case ipNetType:
		return appendIPNetValue
	case ipType, netipPrefixType, netipAddrType:
		return appendStringer
	case jsonRawMessageType:
		return appendJSONRawMessageValue
	}

	kind := typ.Kind()

	if typ.Implements(queryAppenderType) {
		if kind == reflect.Pointer {
			return nilAwareAppender(appendQueryAppenderValue)
		}
		return appendQueryAppenderValue
	}
	if typ.Implements(driverValuerType) {
		if kind == reflect.Pointer {
			return nilAwareAppender(appendDriverValue)
		}
		return appendDriverValue
	}

	if kind != reflect.Pointer {
		ptr := reflect.PointerTo(typ)
		if ptr.Implements(queryAppenderType) {
			return addrAppender(appendQueryAppenderValue)
		}
		if ptr.Implements(driverValuerType) {
			return addrAppender(appendDriverValue)
		}
	}

	switch kind {
	case reflect.Interface:
		return ifaceAppenderFunc
	case reflect.Pointer:
		if typ.Implements(jsonMarshalerType) {
			return nilAwareAppender(AppendJSONValue)
		}
		if fn := Appender(typ.Elem()); fn != nil {
			return PtrAppender(fn)
		}
	case reflect.Slice:
		if typ.Elem().Kind() == reflect.Uint8 {
			return appendBytesValue
		}
	case reflect.Array:
		if typ.Elem().Kind() == reflect.Uint8 {
			return appendArrayBytesValue
		}
	}

	return appenders[typ.Kind()]
}

func ifaceAppenderFunc(gen QueryGen, b []byte, v reflect.Value) []byte {
	if v.IsNil() {
		return dialect.AppendNull(b)
	}
	elem := v.Elem()
	appender := Appender(elem.Type())
	return appender(gen, b, elem)
}

func nilAwareAppender(fn AppenderFunc) AppenderFunc {
	return func(gen QueryGen, b []byte, v reflect.Value) []byte {
		if v.IsNil() {
			return dialect.AppendNull(b)
		}
		return fn(gen, b, v)
	}
}

func PtrAppender(fn AppenderFunc) AppenderFunc {
	return func(gen QueryGen, b []byte, v reflect.Value) []byte {
		if v.IsNil() {
			return dialect.AppendNull(b)
		}
		return fn(gen, b, v.Elem())
	}
}

// bindUint binds n, wrapped to the signed type of the same width when the
// dialect asks for it.
func bindUint(gen QueryGen, b []byte, n uint64, bits int) []byte {
	if gen.uintAsInt {
		if bits == 32 {
			return gen.Bind(b, int32(uint32(n)))
		}
		return gen.Bind(b, int64(n))
	}
	if bits == 32 {
		return gen.Bind(b, uint32(n))
	}
	return gen.Bind(b, n)
}

func AppendBoolValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, v.Bool())
}

func AppendIntValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, v.Int())
}

func AppendUintValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, v.Uint())
}

func appendUint32Value(gen QueryGen, b []byte, v reflect.Value) []byte {
	return bindUint(gen, b, v.Uint(), 32)
}

func appendUint64Value(gen QueryGen, b []byte, v reflect.Value) []byte {
	return bindUint(gen, b, v.Uint(), 64)
}

func AppendFloat32Value(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, float32(v.Float()))
}

func AppendFloat64Value(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, v.Float())
}

func appendBytesValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, v.Bytes())
}

func appendArrayBytesValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	tmp := make([]byte, v.Len())
	reflect.Copy(reflect.ValueOf(tmp), v)
	return gen.Bind(b, tmp)
}

func AppendStringValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, v.String())
}

func AppendJSONValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	bb, err := json.Marshal(v.Interface())
	if err != nil {
		return gen.BindError(b, err)
	}

	if len(bb) > 0 && bb[len(bb)-1] == '\n' {
		bb = bb[:len(bb)-1]
	}

	return gen.Bind(b, bb)
}

func appendTimeValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, v.Interface().(time.Time))
}

func appendIPNetValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	ipnet := v.Interface().(net.IPNet)
	return gen.Bind(b, ipnet.String())
}

func appendStringer(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, v.Interface().(fmt.Stringer).String())
}

func appendJSONRawMessageValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	bytes := v.Bytes()
	if bytes == nil {
		return dialect.AppendNull(b)
	}
	return gen.Bind(b, []byte(bytes))
}

func appendQueryAppenderValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	return AppendQueryAppender(gen, b, v.Interface().(QueryAppender))
}

// appendDriverValue binds the valuer itself; pgx calls Value() when encoding.
func appendDriverValue(gen QueryGen, b []byte, v reflect.Value) []byte {
	return gen.Bind(b, v.Interface())
}

func addrAppender(fn AppenderFunc) AppenderFunc {
	return func(gen QueryGen, b []byte, v reflect.Value) []byte {
		if !v.CanAddr() {
			err := fmt.Errorf("boa: Append(nonaddressable %T)", v.Interface())
			return gen.BindError(b, err)
		}
		return fn(gen, b, v.Addr())
	}
}

func AppendQueryAppender(gen QueryGen, b []byte, app QueryAppender) []byte {
	bb, err := app.AppendQuery(gen, b)
	if err != nil {
		return gen.BindError(b, err)
	}
	return bb
}
