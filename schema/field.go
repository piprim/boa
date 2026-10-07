package schema

import (
	"fmt"
	"reflect"

	"github.com/piprim/boa/dialect"
	"github.com/piprim/boa/internal"
	"github.com/piprim/boa/internal/tagparser"
)

type Field struct {
	Table       *Table // Contains this field
	StructField reflect.StructField
	IsPtr       bool

	Tag          tagparser.Tag
	IndirectType reflect.Type
	Index        []int

	Name    string // SQL name, .e.g. id
	SQLName Safe   // escaped SQL name, e.g. "id"
	GoName  string // struct field name, e.g. Id

	DiscoveredSQLType string
	UserSQLType       string
	SQLDefault        string

	OnDelete string
	OnUpdate string

	IsPK          bool
	NotNull       bool
	NullZero      bool
	AutoIncrement bool
	Identity      bool

	Append AppenderFunc
	Scan   ScannerFunc
	IsZero IsZeroerFunc
	// IsUnset reports whether the value was never set; nil unless the type
	// implements IsUnset() bool.
	IsUnset IsZeroerFunc
}

func (f *Field) String() string {
	return f.Name
}

func (f *Field) WithIndex(path []int) *Field {
	if len(path) == 0 {
		return f
	}
	clone := *f
	clone.Index = makeIndex(path, f.Index)
	return &clone
}

func (f *Field) Clone() *Field {
	cp := *f
	cp.Index = cp.Index[:len(f.Index):len(f.Index)]
	return &cp
}

func (f *Field) Value(strct reflect.Value) reflect.Value {
	return internal.FieldByIndexAlloc(strct, f.Index)
}

func (f *Field) HasNilValue(v reflect.Value) bool {
	if len(f.Index) == 1 {
		return v.Field(f.Index[0]).IsNil()
	}

	for _, index := range f.Index {
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return true
			}
			v = v.Elem()
		}
		v = v.Field(index)
	}
	return v.IsNil()
}

func (f *Field) HasZeroValue(v reflect.Value) bool {
	if len(f.Index) == 1 {
		return f.IsZero(v.Field(f.Index[0]))
	}

	for _, index := range f.Index {
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return true
			}
			v = v.Elem()
		}
		v = v.Field(index)
	}
	return f.IsZero(v)
}

// HasUnsetValue reports whether the field of strct is unset. It is false for
// a type without IsUnset and for a field behind a nil pointer.
func (f *Field) HasUnsetValue(strct reflect.Value) bool {
	if f.IsUnset == nil {
		return false
	}
	fv, ok := fieldByIndex(strct, f.Index)
	if !ok {
		return false
	}
	return f.IsUnset(fv)
}

func (f *Field) AppendValue(gen QueryGen, b []byte, strct reflect.Value) []byte {
	return f.appendValue(gen, b, strct, false)
}

func (f *Field) AppendValueOrDefault(gen QueryGen, b []byte, strct reflect.Value) []byte {
	return f.appendValue(gen, b, strct, true)
}

func (f *Field) appendValue(gen QueryGen, b []byte, strct reflect.Value, defaultPlaceholder bool) []byte {
	fv, ok := fieldByIndex(strct, f.Index)
	if !ok {
		return dialect.AppendNull(b)
	}

	if (f.IsPtr && fv.IsNil()) || (f.NullZero && f.IsZero(fv)) {
		if defaultPlaceholder {
			return append(b, "DEFAULT"...)
		}
		return dialect.AppendNull(b)
	}
	if f.Append == nil {
		panic(fmt.Errorf("boa: AppendValue(unsupported %s)", fv.Type()))
	}
	return f.Append(gen, b, fv)
}

func (f *Field) ScanValue(strct reflect.Value, src any) error {
	if src == nil {
		if fv, ok := fieldByIndex(strct, f.Index); ok {
			return f.ScanWithCheck(fv, src)
		}
		return nil
	}

	fv := internal.FieldByIndexAlloc(strct, f.Index)
	return f.ScanWithCheck(fv, src)
}

func (f *Field) ScanWithCheck(fv reflect.Value, src any) error {
	if f.Scan == nil {
		return fmt.Errorf("boa: Scan(unsupported %s)", f.IndirectType)
	}
	return f.Scan(fv, src)
}

func (f *Field) SkipUpdate() bool {
	return f.Tag.HasOption("skipupdate")
}
