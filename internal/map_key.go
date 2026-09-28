package internal

import "reflect"

var ifaceType = reflect.TypeFor[any]()

// MapKey wraps a slice of values as a comparable map key: an array of the
// same length, built through reflect so any length works.
type MapKey struct {
	iface any
}

func NewMapKey(is []any) MapKey {
	at := reflect.New(reflect.ArrayOf(len(is), ifaceType)).Elem()
	for i, v := range is {
		at.Index(i).Set(reflect.ValueOf(&v).Elem())
	}
	return MapKey{iface: at.Interface()}
}
