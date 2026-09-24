package schema

import (
	"reflect"
	"testing"
)

type benchCountry struct {
	ID   int64 `bun:",pk"`
	Name string
}

type benchAuthor struct {
	ID        int64 `bun:",pk"`
	Name      string
	Email     string
	CountryID int64
	Country   *benchCountry `bun:"rel:belongs-to,join:country_id=id"`
}

type benchBook struct {
	ID       int64 `bun:",pk"`
	Title    string
	Subtitle string
	AuthorID int64
	Author   *benchAuthor `bun:"rel:belongs-to,join:author_id=id"`
}

// LookupField is called once per column per scanned row. Names that address a
// joined struct ("author__name") miss FieldMap and take the slow path, which
// walks StructMap and clones the field on every call.
func BenchmarkLookupFieldPrefixed(b *testing.B) {
	table := NewTables(newNopDialect()).Get(reflect.TypeFor[*benchBook]())

	names := []string{
		"author__id",
		"author__name",
		"author__email",
		"author__country__id",
		"author__country__name",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, name := range names {
			if f := table.LookupField(name); f == nil {
				b.Fatalf("field not found: %s", name)
			}
		}
	}
}

// Direct hits already return a shared *Field; kept as a baseline so the two
// paths can be compared side by side.
func BenchmarkLookupFieldDirect(b *testing.B) {
	table := NewTables(newNopDialect()).Get(reflect.TypeFor[*benchBook]())

	names := []string{"id", "title", "subtitle", "author_id"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, name := range names {
			if f := table.LookupField(name); f == nil {
				b.Fatalf("field not found: %s", name)
			}
		}
	}
}
