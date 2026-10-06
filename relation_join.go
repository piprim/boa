package boa

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/piprim/boa/internal"
	"github.com/piprim/boa/schema"
)

type relationJoin struct {
	Parent    *relationJoin
	BaseModel TableModel
	JoinModel TableModel
	Relation  *schema.Relation

	additionalJoinOnConditions []schema.QueryWithArgs

	apply   func(*SelectQuery) *SelectQuery
	columns []schema.QueryWithArgs
}

func (j *relationJoin) applyTo(q *SelectQuery) {
	if j.apply == nil {
		return
	}

	var table *schema.Table
	var columns []schema.QueryWithArgs

	// Save state.
	table, q.table = q.table, j.JoinModel.Table()
	columns, q.columns = q.columns, nil

	q = j.apply(q)

	// Restore state.
	q.table = table
	j.columns, q.columns = q.columns, columns
}

func (j *relationJoin) selectMany(ctx context.Context, q *SelectQuery) error {
	q = j.manyQuery(q)
	if q == nil {
		return nil
	}
	return q.Scan(ctx)
}

func (j *relationJoin) manyQuery(q *SelectQuery) *SelectQuery {
	hasManyModel := newHasManyModel(j)
	if hasManyModel == nil {
		return nil
	}

	q = q.Model(hasManyModel)

	var where []byte

	return j.manyQueryCompositeIn(where, q)
}

func (j *relationJoin) manyQueryCompositeIn(where []byte, q *SelectQuery) *SelectQuery {
	if len(j.Relation.JoinPKs) > 1 {
		where = append(where, '(')
	}
	where = appendColumns(where, j.JoinModel.Table().SQLAlias, j.Relation.JoinPKs)
	if len(j.Relation.JoinPKs) > 1 {
		where = append(where, ')')
	}
	where = append(where, " IN (?)"...)

	values := childValues{
		root:   j.JoinModel.rootValue(),
		index:  j.JoinModel.parentIndex(),
		fields: j.Relation.BasePKs,
	}
	if len(j.additionalJoinOnConditions) > 0 {
		where = append(where, " AND ?"...)
		q = q.Where(internal.String(where), values, joinConditions(j.additionalJoinOnConditions))
	} else {
		q = q.Where(internal.String(where), values)
	}

	if j.Relation.PolymorphicField != nil {
		q = q.Where("? = ?", j.Relation.PolymorphicField.SQLName, j.Relation.PolymorphicValue)
	}

	j.applyTo(q)
	q = q.Apply(j.hasManyColumns)

	return q
}

func (j *relationJoin) hasManyColumns(q *SelectQuery) *SelectQuery {
	joinTable := j.JoinModel.Table()
	if len(j.columns) == 0 {
		b := appendColumns(nil, joinTable.SQLAlias, joinTable.Fields)
		return q.ColumnExpr(internal.String(b))
	}

	for _, col := range j.columns {
		if col.Args == nil {
			if field, ok := joinTable.FieldMap[col.Query]; ok {
				b := append([]byte(joinTable.SQLAlias), '.')
				b = append(b, field.SQLName...)
				q = q.ColumnExpr(internal.String(b))
				continue
			}
		}
		q = q.ColumnExpr("?", col)
	}

	return q
}

func (j *relationJoin) selectM2M(ctx context.Context, q *SelectQuery) error {
	q = j.m2mQuery(q)
	if q == nil {
		return nil
	}
	return q.Scan(ctx)
}

func (j *relationJoin) m2mQuery(q *SelectQuery) *SelectQuery {
	gen := q.db.gen

	m2mModel := newM2MModel(j)
	if m2mModel == nil {
		return nil
	}
	q = q.Model(m2mModel)

	index := j.JoinModel.parentIndex()

	if j.Relation.M2MTable != nil {
		// We only need base pks to park joined models to the base model.
		fields := j.Relation.M2MBasePKs

		b := make([]byte, 0, len(fields))
		b = appendColumns(b, j.Relation.M2MTable.SQLAlias, fields)

		q = q.ColumnExpr(internal.String(b))
	}

	var join []byte
	join = append(join, "JOIN "...)
	join = gen.AppendQuery(join, string(j.Relation.M2MTable.SQLName))
	join = append(join, " AS "...)
	join = append(join, j.Relation.M2MTable.SQLAlias...)
	join = append(join, " ON ("...)
	for i, col := range j.Relation.M2MBasePKs {
		if i > 0 {
			join = append(join, ", "...)
		}
		join = append(join, j.Relation.M2MTable.SQLAlias...)
		join = append(join, '.')
		join = append(join, col.SQLName...)
	}
	join = append(join, ") IN (?)"...)

	values := childValues{root: j.BaseModel.rootValue(), index: index, fields: j.Relation.BasePKs}
	if len(j.additionalJoinOnConditions) > 0 {
		join = append(join, " AND ?"...)
		q = q.Join(internal.String(join), values, joinConditions(j.additionalJoinOnConditions))
	} else {
		q = q.Join(internal.String(join), values)
	}

	joinTable := j.JoinModel.Table()
	for i, m2mJoinField := range j.Relation.M2MJoinPKs {
		joinField := j.Relation.JoinPKs[i]
		q = q.Where("?.? = ?.?",
			joinTable.SQLAlias, joinField.SQLName,
			j.Relation.M2MTable.SQLAlias, m2mJoinField.SQLName)
	}

	j.applyTo(q)
	q = q.Apply(j.hasManyColumns)

	return q
}

func (j *relationJoin) hasParent() bool {
	if j.Parent != nil {
		switch j.Parent.Relation.Type {
		case schema.HasOneRelation, schema.BelongsToRelation:
			return true
		}
	}
	return false
}

func (j *relationJoin) appendAlias(gen schema.QueryGen, b []byte) []byte {
	quote := gen.IdentQuote()

	b = append(b, quote)
	b = appendAlias(b, j)
	b = append(b, quote)
	return b
}

func (j *relationJoin) appendAliasColumn(gen schema.QueryGen, b []byte, column string) []byte {
	quote := gen.IdentQuote()

	b = append(b, quote)
	b = appendAlias(b, j)
	b = append(b, "__"...)
	b = append(b, column...)
	b = append(b, quote)
	return b
}

func (j *relationJoin) appendBaseAlias(gen schema.QueryGen, b []byte) []byte {
	quote := gen.IdentQuote()

	if j.hasParent() {
		b = append(b, quote)
		b = appendAlias(b, j.Parent)
		b = append(b, quote)
		return b
	}
	return append(b, j.BaseModel.Table().SQLAlias...)
}

func (j *relationJoin) appendSoftDelete(
	gen schema.QueryGen, b []byte, flags internal.Flag,
) []byte {
	b = append(b, '.')

	field := j.JoinModel.Table().SoftDeleteField
	b = append(b, field.SQLName...)

	if field.IsPtr || field.NullZero {
		if flags.Has(deletedFlag) {
			b = append(b, " IS NOT NULL"...)
		} else {
			b = append(b, " IS NULL"...)
		}
	} else {
		if flags.Has(deletedFlag) {
			b = append(b, " != "...)
		} else {
			b = append(b, " = "...)
		}
		b = gen.Bind(b, time.Time{})
	}

	return b
}

func appendAlias(b []byte, j *relationJoin) []byte {
	if j.hasParent() {
		b = appendAlias(b, j.Parent)
		b = append(b, "__"...)
	}
	b = append(b, j.Relation.Field.Name...)
	return b
}

func (j *relationJoin) appendHasOneJoin(
	gen schema.QueryGen, b []byte, q *SelectQuery,
) (_ []byte, err error) {
	isSoftDelete := j.JoinModel.Table().SoftDeleteField != nil && !q.flags.Has(allWithDeletedFlag)

	b = append(b, "LEFT JOIN "...)
	b = gen.AppendQuery(b, string(j.JoinModel.Table().SQLNameForSelects))
	b = append(b, " AS "...)
	b = j.appendAlias(gen, b)

	b = append(b, " ON "...)

	b = append(b, '(')
	for i, baseField := range j.Relation.BasePKs {
		if i > 0 {
			b = append(b, " AND "...)
		}
		b = j.appendAlias(gen, b)
		b = append(b, '.')
		b = append(b, j.Relation.JoinPKs[i].SQLName...)
		b = append(b, " = "...)
		b = j.appendBaseAlias(gen, b)
		b = append(b, '.')
		b = append(b, baseField.SQLName...)
	}
	b = append(b, ')')

	if isSoftDelete {
		b = append(b, " AND "...)
		b = j.appendAlias(gen, b)
		b = j.appendSoftDelete(gen, b, q.flags)
	}

	if len(j.additionalJoinOnConditions) > 0 {
		b = append(b, " AND "...)
		b, err = joinConditions(j.additionalJoinOnConditions).AppendQuery(gen, b)
		if err != nil {
			return nil, err
		}
	}

	return b, nil
}

// childValues renders the key values of the parent rows for an IN list, each
// bound, with duplicate parents listed once.
type childValues struct {
	root   reflect.Value
	index  []int
	fields []*schema.Field
}

var _ schema.QueryAppender = childValues{}

func (c childValues) AppendQuery(gen schema.QueryGen, b []byte) ([]byte, error) {
	seen := make(map[string]struct{})
	first := true
	walk(c.root, c.index, func(v reflect.Value) {
		key := childKey(v, c.fields)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}

		if !first {
			b = append(b, ", "...)
		}
		first = false

		if len(c.fields) > 1 {
			b = append(b, '(')
		}
		for i, f := range c.fields {
			if i > 0 {
				b = append(b, ", "...)
			}
			b = f.AppendValue(gen, b, v)
		}
		if len(c.fields) > 1 {
			b = append(b, ')')
		}
	})
	return b, nil
}

// childKey identifies one parent row by its key field values, for
// de-duplication. The rendered SQL cannot serve, since every $n is distinct.
func childKey(v reflect.Value, fields []*schema.Field) string {
	var sb strings.Builder
	for _, f := range fields {
		fmt.Fprintf(&sb, "%#v|", f.Value(v).Interface())
	}
	return sb.String()
}

// joinConditions renders additional join-on conditions joined with AND.
type joinConditions []schema.QueryWithArgs

var _ schema.QueryAppender = joinConditions(nil)

func (c joinConditions) AppendQuery(gen schema.QueryGen, b []byte) ([]byte, error) {
	for i, cond := range c {
		if i > 0 {
			b = append(b, " AND "...)
		}
		b = gen.AppendQuery(b, cond.Query, cond.Args...)
	}
	return b, nil
}
