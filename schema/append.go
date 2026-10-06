package schema

import (
	"reflect"

	"github.com/piprim/boa/dialect"
)

func NullZero(value any) QueryAppender {
	return nullZero{
		value: value,
	}
}

type nullZero struct {
	value any
}

func (nz nullZero) AppendQuery(gen QueryGen, b []byte) (_ []byte, err error) {
	if isZero(nz.value) {
		return dialect.AppendNull(b), nil
	}
	return gen.AppendValue(b, reflect.ValueOf(nz.value)), nil
}
