package pgdialect

import (
	"database/sql"
	"fmt"
	"reflect"
	"strconv"

	"github.com/piprim/pgcrud/dialect"
	"github.com/piprim/pgcrud/internal"
	"github.com/piprim/pgcrud/schema"
)

type ArrayValue struct {
	v reflect.Value

	append schema.AppenderFunc
	scan   schema.ScannerFunc
}

// Array accepts a slice and returns a wrapper for working with PostgreSQL
// array data type.
//
// For struct fields you can use array tag:
//
//	Emails  []string `bun:",array"`
func Array(vi any) *ArrayValue {
	v := reflect.ValueOf(vi)
	if !v.IsValid() {
		panic(fmt.Errorf("pgcrud: Array(nil)"))
	}

	return &ArrayValue{
		v: v,

		append: arrayAppender(v.Type()),
		scan:   arrayScanner(v.Type()),
	}
}

var (
	_ schema.QueryAppender = (*ArrayValue)(nil)
	_ sql.Scanner          = (*ArrayValue)(nil)
)

func (a *ArrayValue) AppendQuery(gen schema.QueryGen, b []byte) ([]byte, error) {
	if a.append == nil {
		panic(fmt.Errorf("pgcrud: Array(unsupported %s)", a.v.Type()))
	}
	return a.append(gen, b, a.v), nil
}

func (a *ArrayValue) Scan(src any) error {
	if a.scan == nil {
		return fmt.Errorf("pgcrud: Array(unsupported %s)", a.v.Type())
	}
	if a.v.Kind() != reflect.Pointer {
		return fmt.Errorf("pgcrud: Array(non-pointer %s)", a.v.Type())
	}
	return a.scan(a.v, src)
}

func (a *ArrayValue) Value() any {
	if a.v.IsValid() {
		return a.v.Interface()
	}
	return nil
}

//------------------------------------------------------------------------------

func arrayAppender(typ reflect.Type) schema.AppenderFunc {
	switch typ.Kind() {
	case reflect.Pointer:
		if fn := arrayAppender(typ.Elem()); fn != nil {
			return schema.PtrAppender(fn)
		}
		return nil
	case reflect.Slice, reflect.Array:
		return bindArrayValue
	default:
		return nil
	}
}

// bindArrayValue binds the slice or array as-is; pgx encodes it as a
// Postgres array of the parameter's element type. A nil slice is NULL.
func bindArrayValue(gen schema.QueryGen, b []byte, v reflect.Value) []byte {
	if v.Kind() == reflect.Slice && v.IsNil() {
		return dialect.AppendNull(b)
	}
	return gen.Bind(b, v.Interface())
}

//------------------------------------------------------------------------------

func arrayScanner(typ reflect.Type) schema.ScannerFunc {
	kind := typ.Kind()

	switch kind {
	case reflect.Pointer:
		if fn := arrayScanner(typ.Elem()); fn != nil {
			return schema.PtrScanner(fn)
		}
	case reflect.Slice, reflect.Array:
		// ok:
	default:
		return nil
	}

	elemType := typ.Elem()

	if kind == reflect.Slice {
		switch elemType {
		case stringType:
			return scanStringSliceValue
		case intType:
			return scanIntSliceValue
		case int64Type:
			return scanInt64SliceValue
		case float64Type:
			return scanFloat64SliceValue
		}
	}

	scanElem := schema.Scanner(elemType)
	return func(dest reflect.Value, src any) error {
		dest = reflect.Indirect(dest)
		if !dest.CanSet() {
			return fmt.Errorf("pgcrud: Scan(non-settable %s)", dest.Type())
		}

		kind := dest.Kind()

		if src == nil {
			if kind != reflect.Slice || !dest.IsNil() {
				dest.Set(reflect.Zero(dest.Type()))
			}
			return nil
		}

		if kind == reflect.Slice {
			if dest.IsNil() {
				dest.Set(reflect.MakeSlice(dest.Type(), 0, 0))
			} else if dest.Len() > 0 {
				dest.Set(dest.Slice(0, 0))
			}
		}

		if src == nil {
			return nil
		}

		b, err := toBytes(src)
		if err != nil {
			return err
		}

		p := newArrayParser(b)
		nextValue := internal.MakeSliceNextElemFunc(dest)
		for p.Next() {
			elem := p.Elem()
			elemValue := nextValue()
			if err := scanElem(elemValue, elem); err != nil {
				return fmt.Errorf("scanElem failed: %w", err)
			}
		}
		return p.Err()
	}
}

func scanStringSliceValue(dest reflect.Value, src any) error {
	dest = reflect.Indirect(dest)
	if !dest.CanSet() {
		return fmt.Errorf("pgcrud: Scan(non-settable %s)", dest.Type())
	}

	slice, err := decodeStringSlice(src)
	if err != nil {
		return err
	}

	dest.Set(reflect.ValueOf(slice))
	return nil
}

func decodeStringSlice(src any) ([]string, error) {
	if src == nil {
		return nil, nil
	}

	b, err := toBytes(src)
	if err != nil {
		return nil, err
	}

	slice := make([]string, 0)

	p := newArrayParser(b)
	for p.Next() {
		elem := p.Elem()
		slice = append(slice, string(elem))
	}
	if err := p.Err(); err != nil {
		return nil, err
	}
	return slice, nil
}

func scanIntSliceValue(dest reflect.Value, src any) error {
	dest = reflect.Indirect(dest)
	if !dest.CanSet() {
		return fmt.Errorf("pgcrud: Scan(non-settable %s)", dest.Type())
	}

	slice, err := decodeIntSlice(src)
	if err != nil {
		return err
	}

	dest.Set(reflect.ValueOf(slice))
	return nil
}

func decodeIntSlice(src any) ([]int, error) {
	if src == nil {
		return nil, nil
	}

	b, err := toBytes(src)
	if err != nil {
		return nil, err
	}

	slice := make([]int, 0)

	p := newArrayParser(b)
	for p.Next() {
		elem := p.Elem()

		if elem == nil {
			slice = append(slice, 0)
			continue
		}

		n, err := strconv.Atoi(internal.String(elem))
		if err != nil {
			return nil, err
		}

		slice = append(slice, n)
	}
	if err := p.Err(); err != nil {
		return nil, err
	}
	return slice, nil
}

func scanInt64SliceValue(dest reflect.Value, src any) error {
	dest = reflect.Indirect(dest)
	if !dest.CanSet() {
		return fmt.Errorf("pgcrud: Scan(non-settable %s)", dest.Type())
	}

	slice, err := decodeInt64Slice(src)
	if err != nil {
		return err
	}

	dest.Set(reflect.ValueOf(slice))
	return nil
}

func decodeInt64Slice(src any) ([]int64, error) {
	if src == nil {
		return nil, nil
	}

	b, err := toBytes(src)
	if err != nil {
		return nil, err
	}

	slice := make([]int64, 0)

	p := newArrayParser(b)
	for p.Next() {
		elem := p.Elem()

		if elem == nil {
			slice = append(slice, 0)
			continue
		}

		n, err := strconv.ParseInt(internal.String(elem), 10, 64)
		if err != nil {
			return nil, err
		}

		slice = append(slice, n)
	}
	if err := p.Err(); err != nil {
		return nil, err
	}
	return slice, nil
}

func scanFloat64SliceValue(dest reflect.Value, src any) error {
	dest = reflect.Indirect(dest)
	if !dest.CanSet() {
		return fmt.Errorf("pgcrud: Scan(non-settable %s)", dest.Type())
	}

	slice, err := scanFloat64Slice(src)
	if err != nil {
		return err
	}

	dest.Set(reflect.ValueOf(slice))
	return nil
}

func scanFloat64Slice(src any) ([]float64, error) {
	if src == nil {
		return nil, nil
	}

	b, err := toBytes(src)
	if err != nil {
		return nil, err
	}

	slice := make([]float64, 0)

	p := newArrayParser(b)
	for p.Next() {
		elem := p.Elem()

		if elem == nil {
			slice = append(slice, 0)
			continue
		}

		n, err := strconv.ParseFloat(internal.String(elem), 64)
		if err != nil {
			return nil, err
		}

		slice = append(slice, n)
	}
	if err := p.Err(); err != nil {
		return nil, err
	}
	return slice, nil
}

func toBytes(src any) ([]byte, error) {
	switch src := src.(type) {
	case string:
		return internal.Bytes(src), nil
	case []byte:
		return src, nil
	default:
		return nil, fmt.Errorf("pgdialect: got %T, wanted []byte or string", src)
	}
}
