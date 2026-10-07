package schema

import (
	"database/sql/driver"
	"reflect"
)

var isZeroerType = reflect.TypeFor[isZeroer]()

type isZeroer interface {
	IsZero() bool
}

func isZero(v any) bool {
	if v == nil {
		return true
	}
	if z, ok := v.(isZeroer); ok {
		return z.IsZero()
	}
	rv := reflect.ValueOf(v)
	return zeroChecker(rv.Type())(rv)
}

// IsZeroerFunc reports whether a reflect.Value is a zero value.
type IsZeroerFunc func(reflect.Value) bool

func zeroChecker(typ reflect.Type) IsZeroerFunc {
	if typ.Implements(isZeroerType) {
		return isZeroInterface
	}

	kind := typ.Kind()

	if kind != reflect.Pointer && reflect.PointerTo(typ).Implements(isZeroerType) {
		return addrChecker(isZeroInterface)
	}

	switch kind {
	case reflect.Array:
		if typ.Elem().Kind() == reflect.Uint8 {
			return reflect.Value.IsZero
		}
		return isZeroLen
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Interface, reflect.Pointer, reflect.Slice, reflect.Map:
		return reflect.Value.IsZero
	}

	if typ.Implements(driverValuerType) {
		return isZeroDriverValue
	}

	return notZero
}

func addrChecker(fn IsZeroerFunc) IsZeroerFunc {
	return func(v reflect.Value) bool {
		if !v.CanAddr() {
			return false
		}
		return fn(v.Addr())
	}
}

func isZeroInterface(v reflect.Value) bool {
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return true
	}
	return v.Interface().(isZeroer).IsZero()
}

func isZeroDriverValue(v reflect.Value) bool {
	if v.Kind() == reflect.Pointer {
		return v.IsNil()
	}

	valuer := v.Interface().(driver.Valuer)
	value, err := valuer.Value()
	if err != nil {
		return false
	}
	return value == nil
}

func isZeroLen(v reflect.Value) bool {
	return v.Len() == 0
}

func notZero(v reflect.Value) bool {
	return false
}

var isUnsetterType = reflect.TypeFor[isUnsetter]()

// isUnsetter is implemented by values that distinguish "never set" from null
// and from a value, such as presence.Of[T].
type isUnsetter interface {
	IsUnset() bool
}

// unsetChecker returns a checker for a type that implements isUnsetter,
// directly or through its pointer, and nil for every other type.
func unsetChecker(typ reflect.Type) IsZeroerFunc {
	if typ.Implements(isUnsetterType) {
		return isUnsetInterface
	}
	if typ.Kind() != reflect.Pointer && reflect.PointerTo(typ).Implements(isUnsetterType) {
		return addrChecker(isUnsetInterface)
	}
	return nil
}

// isUnsetInterface reports IsUnset of v. A nil pointer is not unset: the
// nil-pointer rules of INSERT and UPDATE apply to it instead.
func isUnsetInterface(v reflect.Value) bool {
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return false
	}
	return v.Interface().(isUnsetter).IsUnset()
}
