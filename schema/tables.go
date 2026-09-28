package schema

import (
	"fmt"
	"reflect"
	"sync"
)

// Tables is the registry of struct models. onTable, when set, runs once per
// table after its fields and relations are known; pgdialect uses it to attach
// the array and hstore codecs.
type Tables struct {
	onTable func(*Table)

	mu     sync.Mutex
	tables sync.Map // reflect.Type -> *Table

	inProgress map[reflect.Type]*Table
}

func NewTables(onTable func(*Table)) *Tables {
	return &Tables{
		onTable:    onTable,
		inProgress: make(map[reflect.Type]*Table),
	}
}

func (t *Tables) Register(models ...any) {
	for _, model := range models {
		_ = t.Get(reflect.TypeOf(model).Elem())
	}
}

func (t *Tables) Get(typ reflect.Type) *Table {
	typ = indirectType(typ)
	if typ.Kind() != reflect.Struct {
		panic(fmt.Errorf("got %s, wanted %s", typ.Kind(), reflect.Struct))
	}

	if v, ok := t.tables.Load(typ); ok {
		return v.(*Table)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if v, ok := t.tables.Load(typ); ok {
		return v.(*Table)
	}

	table := t.InProgress(typ)
	table.initRelations()

	if t.onTable != nil {
		t.onTable(table)
	}
	for _, field := range table.FieldMap {
		if field.UserSQLType == "" {
			field.UserSQLType = field.DiscoveredSQLType
		}
	}

	t.tables.Store(typ, table)
	return table
}

// InProgress returns the in-progress table for typ, initializing it
// if it has not been initialized yet. The Placeholder + init() split
// lets callers register StructMap entries with the table pointer
// before triggering recursive initialization, preventing missing
// entries in circular dependency chains.
func (t *Tables) InProgress(typ reflect.Type) *Table {
	table := t.Placeholder(typ)
	table.init(t, typ)
	return table
}

// Placeholder returns an existing in-progress table or creates a new
// uninitialized placeholder entry.
func (t *Tables) Placeholder(typ reflect.Type) *Table {
	table, ok := t.inProgress[typ]
	if !ok {
		table = new(Table)
		t.inProgress[typ] = table
	}
	return table
}

// ByModel gets the table by its Go name.
func (t *Tables) ByModel(name string) *Table {
	return t.find(func(table *Table) bool { return table.TypeName == name })
}

// ByName gets the table by its SQL name.
func (t *Tables) ByName(name string) *Table {
	return t.find(func(table *Table) bool { return table.Name == name })
}

func (t *Tables) find(match func(*Table) bool) *Table {
	var found *Table
	t.tables.Range(func(_, v any) bool {
		if table := v.(*Table); match(table) {
			found = table
			return false
		}
		return true
	})
	return found
}

// All returns all registered tables.
func (t *Tables) All() []*Table {
	var found []*Table
	t.tables.Range(func(_, v any) bool {
		found = append(found, v.(*Table))
		return true
	})
	return found
}
