package pgdialect

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHStoreAppender(t *testing.T) {
	tests := []struct {
		input      map[string]string
		expectedIn []string // maps being unsorted, several outputs are valid
	}{
		{map[string]string{}, []string{``}},
		{map[string]string{"": ""}, []string{`""=>""`}},
		{map[string]string{`\`: `\`}, []string{`"\\"=>"\\"`}},
		{map[string]string{"'": "'"}, []string{`"'"=>"'"`}},
		{map[string]string{`'"{}`: `'"{}`}, []string{`"'\"{}"=>"'\"{}"`}},
		{map[string]string{"1": "2", "3": "4"}, []string{`"1"=>"2","3"=>"4"`, `"3"=>"4","1"=>"2"`}},
		{map[string]string{"1": ""}, []string{`"1"=>""`}},
		{map[string]string{"1": "NULL"}, []string{`"1"=>"NULL"`}},
	}

	appendFunc := pgDialect.hstoreAppender(reflect.TypeFor[map[string]string]())

	for i, test := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			gen, list := bindGen()
			got := appendFunc(gen, []byte{}, reflect.ValueOf(test.input))
			require.Equal(t, "$1", string(got))
			require.Len(t, list.Args(), 1)
			require.Contains(t, test.expectedIn, list.Args()[0])
		})
	}

	t.Run("nil map is inline NULL", func(t *testing.T) {
		gen, list := bindGen()
		got := appendFunc(gen, nil, reflect.ValueOf(map[string]string(nil)))
		require.Equal(t, "NULL", string(got))
		require.Empty(t, list.Args())
	})

	t.Run("HStore wrapper binds the text", func(t *testing.T) {
		gen, list := bindGen()
		got, err := HStore(map[string]string{"k": "v"}).AppendQuery(gen, nil)
		require.NoError(t, err)
		require.Equal(t, "$1", string(got))
		require.Equal(t, []any{`"k"=>"v"`}, list.Args())
	})
}
