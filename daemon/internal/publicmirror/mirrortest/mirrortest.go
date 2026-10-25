// Package mirrortest makes repository roots for the tests that depend on
// whether the tree is the public code mirror.
package mirrortest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/publicmirror"
)

// Root makes a temporary directory that looks like a repository root
// (Taskfile.yml next to daemon/go.mod) and, when mirror is true, has the
// marker of the public code mirror. It returns the root; the daemon folder,
// where the tasks run, is Root/daemon.
func Root(t testing.TB, mirror bool) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range []string{"Taskfile.yml", "daemon/go.mod"} {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if mirror {
		if err := os.WriteFile(filepath.Join(root, publicmirror.Marker), []byte(publicmirror.MarkerText), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
