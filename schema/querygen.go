package schema

import (
	"database/sql/driver"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/piprim/boa/dialect"
	"github.com/piprim/boa/internal/parser"
)

var nopQueryGen = QueryGen{tables: NewTables(nil), nop: true}

// QueryGen renders SQL fragments. tables resolves struct models used as
// named arguments; nop marks the template generator that writes ? in place
// of bound values.
type QueryGen struct {
	tables    *Tables
	uintAsInt bool
	nop       bool
	args      *namedArgList
	bound     *ArgList
}

// NewQueryGen returns a generator over tables. uintAsInt binds unsigned
// values as the signed type of the same width, see pgdialect.WithAppendUintAsInt.
func NewQueryGen(tables *Tables, uintAsInt bool) QueryGen {
	return QueryGen{tables: tables, uintAsInt: uintAsInt}
}

func NewNopQueryGen() QueryGen {
	return nopQueryGen
}

func (f QueryGen) IsNop() bool {
	return f.nop
}

func (f QueryGen) Tables() *Tables {
	return f.tables
}

func (f QueryGen) IdentQuote() byte {
	return '"'
}

// Append writes v into b as a bound parameter, or inline when v is a SQL
// fragment (QueryAppender) or nil.
func (gen QueryGen) Append(b []byte, v any) []byte {
	switch v := v.(type) {
	case nil:
		return dialect.AppendNull(b)
	case QueryAppender:
		return AppendQueryAppender(gen, b, v)
	case uint32:
		return bindUint(gen, b, uint64(v), 32)
	case uint64:
		return bindUint(gen, b, v, 64)
	case driver.Valuer:
		// A typed nil pointer satisfies the interface; it means NULL.
		if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer && rv.IsNil() {
			return dialect.AppendNull(b)
		}
		return gen.Bind(b, v)
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16,
		float32, float64, string, time.Time, []byte:
		return gen.Bind(b, v)
	default:
		vv := reflect.ValueOf(v)
		if vv.Kind() == reflect.Pointer && vv.IsNil() {
			return dialect.AppendNull(b)
		}
		appender := Appender(vv.Type())
		return appender(gen, b, vv)
	}
}

func (f QueryGen) AppendName(b []byte, name string) []byte {
	return dialect.AppendName(b, name, f.IdentQuote())
}

func (f QueryGen) AppendIdent(b []byte, ident string) []byte {
	return dialect.AppendIdent(b, ident, f.IdentQuote())
}

func (f QueryGen) AppendValue(b []byte, v reflect.Value) []byte {
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return dialect.AppendNull(b)
	}
	appender := Appender(v.Type())
	return appender(f, b, v)
}

func (f QueryGen) WithArg(arg NamedArgAppender) QueryGen {
	f.args = f.args.WithArg(arg)
	return f
}

func (f QueryGen) WithNamedArg(name string, value any) QueryGen {
	return f.WithArg(&namedArg{name: name, value: value})
}

func (f QueryGen) AppendQuery(dst []byte, query string, args ...any) []byte {
	if f.IsNop() || (args == nil && f.args == nil) || strings.IndexByte(query, '?') == -1 {
		return append(dst, query...)
	}
	return f.append(dst, parser.NewString(query), args)
}

func (f QueryGen) append(dst []byte, p *parser.Parser, args []any) []byte {
	var namedArgs NamedArgAppender
	if len(args) == 1 {
		if v, ok := args[0].(NamedArgAppender); ok {
			namedArgs = v
		} else if v, ok := newStructArgs(f, args[0]); ok {
			namedArgs = v
		}
	}

	var argIndex int
	for p.Valid() {
		b, ok := p.ReadSep('?')
		if !ok {
			dst = append(dst, b...)
			continue
		}
		if len(b) > 0 && b[len(b)-1] == '\\' {
			dst = append(dst, b[:len(b)-1]...)
			dst = append(dst, '?')
			continue
		}
		dst = append(dst, b...)

		name, numeric := p.ReadIdentifier()
		if name != "" {
			if numeric {
				idx, err := strconv.Atoi(name)
				if err != nil {
					goto restore_arg
				}

				if idx >= len(args) {
					goto restore_arg
				}

				dst = f.appendArg(dst, args[idx])
				continue
			}

			if namedArgs != nil {
				dst, ok = namedArgs.AppendNamedArg(f, dst, name)
				if ok {
					continue
				}
			}

			dst, ok = f.args.AppendNamedArg(f, dst, name)
			if ok {
				continue
			}

		restore_arg:
			dst = append(dst, '?')
			dst = append(dst, name...)
			continue
		}

		if argIndex >= len(args) {
			dst = append(dst, '?')
			continue
		}

		arg := args[argIndex]
		argIndex++

		dst = f.appendArg(dst, arg)
	}

	return dst
}

func (gen QueryGen) appendArg(b []byte, arg any) []byte {
	switch arg := arg.(type) {
	case QueryAppender:
		bb, err := arg.AppendQuery(gen, b)
		if err != nil {
			return gen.BindError(b, err)
		}
		return bb
	default:
		return gen.Append(b, arg)
	}
}

//------------------------------------------------------------------------------

type NamedArgAppender interface {
	AppendNamedArg(gen QueryGen, b []byte, name string) ([]byte, bool)
}

type namedArgList struct {
	arg  NamedArgAppender
	next *namedArgList
}

func (l *namedArgList) WithArg(arg NamedArgAppender) *namedArgList {
	return &namedArgList{
		arg:  arg,
		next: l,
	}
}

func (l *namedArgList) AppendNamedArg(gen QueryGen, b []byte, name string) ([]byte, bool) {
	for l != nil && l.arg != nil {
		if b, ok := l.arg.AppendNamedArg(gen, b, name); ok {
			return b, true
		}
		l = l.next
	}
	return b, false
}

//------------------------------------------------------------------------------

type namedArg struct {
	name  string
	value any
}

var _ NamedArgAppender = (*namedArg)(nil)

func (a *namedArg) AppendNamedArg(gen QueryGen, b []byte, name string) ([]byte, bool) {
	if a.name == name {
		return gen.appendArg(b, a.value), true
	}
	return b, false
}

//------------------------------------------------------------------------------

type structArgs struct {
	table *Table
	strct reflect.Value
}

var _ NamedArgAppender = (*structArgs)(nil)

func newStructArgs(gen QueryGen, strct any) (*structArgs, bool) {
	v := reflect.ValueOf(strct)
	if !v.IsValid() {
		return nil, false
	}

	v = reflect.Indirect(v)
	if v.Kind() != reflect.Struct {
		return nil, false
	}

	return &structArgs{
		table: gen.Tables().Get(v.Type()),
		strct: v,
	}, true
}

func (m *structArgs) AppendNamedArg(gen QueryGen, b []byte, name string) ([]byte, bool) {
	return m.table.AppendNamedArg(gen, b, name, m.strct)
}
