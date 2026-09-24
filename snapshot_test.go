package pgcrud_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var updateSnapshots = flag.Bool("update", false, "rewrite golden files under testdata/snapshots")

// assertSnapshot compares got with testdata/snapshots/<TestName with / as ->.
// Files hold the value followed by one newline, the format bun's suite used.
func assertSnapshot(t *testing.T, got string) {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "-")
	path := filepath.Join("testdata", "snapshots", name)

	if *updateSnapshots {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got+"\n"), 0o644))
		return
	}

	want, err := os.ReadFile(path)
	require.NoError(t, err, "missing snapshot %s (run with -update to create)", path)
	require.Equal(t, strings.TrimSuffix(string(want), "\n"), got)
}
