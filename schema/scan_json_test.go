package schema

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

type jsonPayload struct {
	A int `json:"a"`
}

type jsonRawScanner struct {
	raw string
}

func (s *jsonRawScanner) Scan(src any) error {
	b, err := toBytes(src)
	if err != nil {
		return err
	}
	s.raw = string(b)
	return nil
}

// Regression test for #1306: scanning JSON into a non-nil interface used to
// panic on dest.Addr() because the interface element is not addressable. The
// scan must fail with an error instead, so the query does not deadlock.
func TestScanJSONUnaddressableInterface(t *testing.T) {
	var value any = map[string]any{"a": float64(1)}
	dest := reflect.ValueOf(&value).Elem()

	var err error
	require.NotPanics(t, func() {
		err = scanJSONIntoInterface(dest, []byte(`{"b":2}`))
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "nonaddressable")
}

func TestScanJSONIntoInterfaceNonAddressableNil(t *testing.T) {
	var value any = map[string]any{"a": 1}
	dest := reflect.ValueOf(&value).Elem()

	require.NotPanics(t, func() {
		err := scanJSONIntoInterface(dest, nil)
		require.Error(t, err)
	})
}

// Scanning NULL zeroes an addressable dest.
func TestScanJSONAddressableNull(t *testing.T) {
	value := map[string]any{"a": float64(1)}
	dest := reflect.ValueOf(&value).Elem()

	require.NoError(t, scanJSON(dest, nil))
	require.Nil(t, value)
}

// A typed nil map inside an interface is already zero, so scanning NULL
// into it stays a no-op.
func TestScanJSONIntoInterfaceNilMapNull(t *testing.T) {
	var value any = map[string]any(nil)
	dest := reflect.ValueOf(&value).Elem()

	require.NoError(t, scanJSONIntoInterface(dest, nil))
}

// json_use_number fields scan through scanJSONUseNumber, which calls
// dest.Addr() as well.
func TestScanJSONUseNumberUnaddressable(t *testing.T) {
	var value any = map[string]any{"a": float64(1)}
	dest := reflect.ValueOf(&value).Elem().Elem()

	var err error
	require.NotPanics(t, func() {
		err = scanJSONUseNumber(dest, []byte(`{"b":2}`))
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "nonaddressable")
}

// A pointer stored in an interface is not addressable either, but PtrScanner
// scans into the pointer's element, which is. Such values scanned before the
// fix and must keep scanning.
func TestScanJSONPointerInInterface(t *testing.T) {
	payload := &jsonPayload{}
	var value any = payload
	dest := reflect.ValueOf(&value).Elem()

	require.NoError(t, scanJSONIntoInterface(dest, []byte(`{"a":7}`)))
	require.Equal(t, 7, payload.A)
}

// A pointer type implementing sql.Scanner reaches its Scan method through
// PtrScanner as well.
func TestScanJSONScannerInInterface(t *testing.T) {
	scanner := &jsonRawScanner{}
	var value any = scanner
	dest := reflect.ValueOf(&value).Elem()

	require.NoError(t, scanJSONIntoInterface(dest, []byte(`{"a":7}`)))
	require.Equal(t, `{"a":7}`, scanner.raw)
}

func TestScanJSONAddressableMap(t *testing.T) {
	var value map[string]any
	dest := reflect.ValueOf(&value).Elem()

	fn := Scanner(dest.Type())
	require.NotNil(t, fn)

	require.NoError(t, fn(dest, []byte(`{"b":2}`)))
	require.Equal(t, map[string]any{"b": float64(2)}, value)
}
