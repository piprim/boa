package pgdialect

import (
	"bytes"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/piprim/boa/internal"
	"github.com/piprim/boa/schema"
)

type Range[T any] struct {
	Lower, Upper           T
	LowerBound, UpperBound RangeBound
}

type MultiRange[T any] []Range[T]

type RangeBound byte

const (
	// RangeBoundUnset indicates that no bound is set.
	// This usually means the range is uninitialized or unspecified.
	RangeBoundUnset RangeBound = 0x0
	// RangeBoundEmpty is a special marker for an empty range.
	// This is NOT a valid PostgreSQL bound character, but is used internally
	// to represent a range that contains no values.
	RangeBoundEmpty RangeBound = 'E'

	RangeBoundInclusiveLeft  RangeBound = '['
	RangeBoundInclusiveRight RangeBound = ']'
	RangeBoundExclusiveLeft  RangeBound = '('
	RangeBoundExclusiveRight RangeBound = ')'
)

type RangeOption[T any] func(*Range[T])

func NewRange[T any](lower, upper T) Range[T] {
	r := Range[T]{
		Lower:      lower,
		Upper:      upper,
		LowerBound: RangeBoundInclusiveLeft,
		UpperBound: RangeBoundExclusiveRight,
	}
	return r
}

func NewEmptyRange[T any]() Range[T] {
	return Range[T]{LowerBound: RangeBoundEmpty, UpperBound: RangeBoundEmpty}
}

func (r *Range[T]) IsZero() bool {
	// NOTE: r.LowerBound represent
	return r == nil || r.LowerBound == 0
}

func (r Range[T]) IsEmpty() bool {
	return r.LowerBound == RangeBoundEmpty
}

var _ sql.Scanner = (*Range[any])(nil)

func (r *Range[T]) Scan(raw any) (err error) {
	var src []byte
	switch v := raw.(type) {
	case []byte:
		src = v
	case string:
		src = []byte(v)
	case nil:
		return nil
	default:
		return fmt.Errorf("pgdialect: Range can't scan %T", raw)
	}

	src = bytes.TrimSpace(src)
	if len(src) == 0 {
		return nil
	}

	if string(src) == "empty" {
		r.LowerBound, r.UpperBound = RangeBoundEmpty, RangeBoundEmpty
		return nil
	}

	switch src[0] {
	case byte(RangeBoundInclusiveLeft), byte(RangeBoundExclusiveLeft):
		r.LowerBound = RangeBound(src[0])
	default:
		return fmt.Errorf("unexpected lower bound: %s", string(src[:1]))
	}
	switch src[len(src)-1] {
	case byte(RangeBoundInclusiveRight), byte(RangeBoundExclusiveRight):
		r.UpperBound = RangeBound(src[len(src)-1])
	default:
		return fmt.Errorf("unexpected upper bound: %s", string(src[len(src)-1:]))
	}

	src = src[1 : len(src)-1]

	ind := bytes.IndexByte(src, ',')
	if ind == -1 {
		return fmt.Errorf("invalid range: wanted comma, got %s", string(src))
	}
	left, right := src[:ind], src[ind+1:]

	if len(left) > 0 {
		_, err := scanElem(&r.Lower, left)
		if err != nil {
			return err
		}
	} else {
		r.LowerBound = RangeBoundUnset
	}

	if len(right) > 0 {
		_, err = scanElem(&r.Upper, right)
		if err != nil {
			return err
		}
	} else {
		r.UpperBound = RangeBoundUnset
	}

	return nil
}

var _ schema.QueryAppender = (*Range[any])(nil)

// AppendQuery binds the range in its text form. Postgres infers the range type
// from the column or operator; where nothing does, cast in the SQL: ?::daterange.
func (r Range[T]) AppendQuery(gen schema.QueryGen, buf []byte) ([]byte, error) {
	text, err := appendRange(nil, r)
	if err != nil {
		return nil, err
	}
	return gen.Bind(buf, string(text)), nil
}

func appendRange[T any](buf []byte, r Range[T]) (_ []byte, err error) {
	if r.IsEmpty() {
		return append(buf, "empty"...), nil
	}

	if r.LowerBound == RangeBoundUnset {
		// A missing bound is always exclusive: [,] is read as (,).
		buf = append(buf, byte(RangeBoundExclusiveLeft))
	} else {
		buf = append(buf, byte(r.LowerBound))
		buf, err = appendElem(buf, r.Lower)
		if err != nil {
			return nil, err
		}
	}
	buf = append(buf, ',')
	if r.UpperBound == RangeBoundUnset {
		buf = append(buf, byte(RangeBoundExclusiveRight))
	} else {
		buf, err = appendElem(buf, r.Upper)
		if err != nil {
			return nil, err
		}
		buf = append(buf, byte(r.UpperBound))
	}
	return buf, nil
}

func (m *MultiRange[T]) Len() int {
	if m == nil {
		return 0
	}
	return len(([]Range[T])(*m))
}

func (m *MultiRange[T]) IsZero() bool {
	return m.Len() == 0
}

// AppendQuery binds the multirange in its text form, {} for nil.
func (m MultiRange[T]) AppendQuery(gen schema.QueryGen, buf []byte) ([]byte, error) {
	text := []byte{'{'}
	for i, r := range m {
		if i > 0 {
			text = append(text, ',')
		}
		var err error
		text, err = appendRange(text, r)
		if err != nil {
			return nil, err
		}
	}
	text = append(text, '}')
	return gen.Bind(buf, string(text)), nil
}

// scanElem parses one range bound from its text form into ptr. Quoted bounds
// are unquoted and their backslash escapes removed.
func scanElem(ptr any, src []byte) ([]byte, error) {
	// NOTE: for daterange, pg return 2024-12-01, for tzrange, pg return "2024-12-01 12:00:00"
	if len(src) >= 2 && src[0] == '"' {
		src = unquoteElem(src[1 : len(src)-1])
	}

	switch ptr := ptr.(type) {
	case *time.Time:
		tm, err := internal.ParseTime(internal.String(src))
		if err != nil {
			return nil, err
		}
		*ptr = tm

		return src, nil

	case sql.Scanner:
		if err := ptr.Scan(src); err != nil {
			return nil, err
		}
		return src, nil
	}

	rv := reflect.ValueOf(ptr)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil, fmt.Errorf("pgdialect: unsupported range type %T", ptr)
	}
	ev := rv.Elem()
	switch ev.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(internal.String(src), 10, 64)
		if err != nil {
			return nil, err
		}
		ev.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(internal.String(src), 10, 64)
		if err != nil {
			return nil, err
		}
		ev.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(internal.String(src), 64)
		if err != nil {
			return nil, err
		}
		ev.SetFloat(f)
	case reflect.String:
		ev.SetString(string(src))
	default:
		return nil, fmt.Errorf("pgdialect: unsupported range type %T", ptr)
	}
	return src, nil
}

// unquoteElem removes the backslash escapes of a double-quoted range bound.
func unquoteElem(src []byte) []byte {
	if bytes.IndexByte(src, '\\') < 0 {
		return src
	}
	out := make([]byte, 0, len(src))
	for i := 0; i < len(src); i++ {
		if src[i] == '\\' && i+1 < len(src) {
			i++
		}
		out = append(out, src[i])
	}
	return out
}
