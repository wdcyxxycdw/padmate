package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/publicmirror"
)

// fakeRoot makes a repository root (Taskfile.yml next to daemon/go.mod) and
// returns its daemon folder, where the task runs the tool.
func fakeRoot(t *testing.T, withMarker bool) string {
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
	if withMarker {
		if err := os.WriteFile(filepath.Join(root, publicmirror.Marker), []byte(publicmirror.MarkerText), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(root, "daemon")
}

// In the public code mirror there is no ROADMAP.md, and the task skips the
// check; in the development repository a missing ROADMAP.md is an error, and
// so is a file that was named with -file.
func TestRunWithoutRoadmap(t *testing.T) {
	t.Run("mirror: default file is skipped", func(t *testing.T) {
		t.Chdir(fakeRoot(t, true))
		var stdout, stderr bytes.Buffer
		if code := run(nil, &stdout, &stderr); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr.String())
		}
		if out := stdout.String(); !strings.Contains(out, "skipped") || !strings.Contains(out, publicmirror.Marker) {
			t.Errorf("stdout = %q", out)
		}
	})

	t.Run("mirror: a named file is still required", func(t *testing.T) {
		t.Chdir(fakeRoot(t, true))
		var stdout, stderr bytes.Buffer
		if code := run([]string{"-file", "../ROADMAP.md"}, &stdout, &stderr); code != 2 {
			t.Fatalf("exit %d, want 2: %s", code, stdout.String())
		}
	})

	t.Run("development repository: a missing file is an error", func(t *testing.T) {
		t.Chdir(fakeRoot(t, false))
		var stdout, stderr bytes.Buffer
		if code := run(nil, &stdout, &stderr); code != 2 {
			t.Fatalf("exit %d, want 2: %s", code, stdout.String())
		}
		if !strings.Contains(stderr.String(), "ROADMAP.md") {
			t.Errorf("stderr = %q", stderr.String())
		}
	})
}
