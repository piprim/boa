package boa

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsReadStatement(t *testing.T) {
	tests := []struct {
		name  string
		query string
		read  bool
	}{
		{name: "plain select", query: "SELECT 1", read: true},
		{name: "lowercase select with leading space and newline", query: "  select\n1", read: true},
		{name: "block comment before select", query: "/* c */ SELECT 1", read: true},
		{name: "line comment before select", query: "-- c\nSELECT 1", read: true},
		{name: "block and line comment before select", query: "/* a */ -- b\n SELECT 1", read: true},
		{name: "parenthesized union of selects", query: "(SELECT 1) UNION (SELECT 2)", read: true},
		{name: "show", query: "SHOW search_path", read: true},
		{name: "values", query: "VALUES (1)", read: true},
		{name: "explain select", query: "EXPLAIN SELECT 1", read: true},
		{name: "explain with non-analyze option", query: "EXPLAIN (VERBOSE) SELECT 1", read: true},
		{name: "explain analyze update", query: "EXPLAIN ANALYZE UPDATE t SET a = 1", read: false},
		{name: "explain analyze option list delete", query: "EXPLAIN (ANALYZE, VERBOSE) DELETE FROM t", read: false},
		{name: "explain block comment before analyze update", query: "EXPLAIN /* c */ ANALYZE UPDATE t SET a = 1", read: false},
		{name: "explain line comment before analyze delete", query: "EXPLAIN -- c\nANALYZE DELETE FROM t", read: false},
		{name: "lowercase explain analyze select executes", query: "explain analyze select 1", read: false},
		{name: "with cte wrapping insert", query: "WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x", read: false},
		{name: "insert", query: "INSERT INTO t VALUES (1)", read: false},
		{name: "empty statement", query: "", read: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.read, isReadStatement(tt.query), "query: %q", tt.query)
		})
	}
}
