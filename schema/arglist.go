package schema

import (
	"fmt"
	"strconv"
)

// ArgList collects the values bound to $n placeholders while a query renders.
// One list is attached to the QueryGen for the life of one render, so nested
// appenders number their placeholders after the ones already emitted.
type ArgList struct {
	args []any
	err  error
}

// NewArgList returns an empty collector.
func NewArgList() *ArgList {
	return &ArgList{}
}

// Args returns the bound values in placeholder order. It is never nil.
func (l *ArgList) Args() []any {
	if l.args == nil {
		return []any{}
	}
	return l.args
}

// Err returns the first error a value appender reported, or nil.
func (l *ArgList) Err() error {
	return l.err
}

func (l *ArgList) add(v any) int {
	l.args = append(l.args, v)
	return len(l.args)
}

func (l *ArgList) setErr(err error) {
	if l.err == nil {
		l.err = fmt.Errorf("pgcrud: bind arg %d: %w", len(l.args)+1, err)
	}
}

// WithArgList returns a copy of gen that binds values into l.
func (gen QueryGen) WithArgList(l *ArgList) QueryGen {
	gen.bound = l
	return gen
}

// Bind appends the next $n placeholder to b and records v as its value.
// Without a list it appends a question mark, the template form used when a
// query is rendered for its name rather than for execution.
func (gen QueryGen) Bind(b []byte, v any) []byte {
	if gen.bound == nil {
		return append(b, '?')
	}
	n := gen.bound.add(v)
	b = append(b, '$')
	return strconv.AppendInt(b, int64(n), 10)
}

// BindArgs records args as bound values without writing any SQL. It is used
// for raw SQL that already carries $n placeholders.
func (gen QueryGen) BindArgs(args ...any) {
	if gen.bound == nil {
		return
	}
	for _, a := range args {
		gen.bound.add(a)
	}
}

// BindError reports that a value could not be encoded. The first error is
// kept on the list and returned by DB.build; the marker keeps the SQL readable
// in templates.
func (gen QueryGen) BindError(b []byte, err error) []byte {
	if gen.bound != nil {
		gen.bound.setErr(err)
	}
	b = append(b, "?!("...)
	b = append(b, err.Error()...)
	return append(b, ')')
}

// Args returns the values bound so far, or nil when no list is attached.
func (gen QueryGen) Args() []any {
	if gen.bound == nil {
		return nil
	}
	return gen.bound.Args()
}
