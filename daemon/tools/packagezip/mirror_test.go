package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/publicmirror"
	"github.com/wdcyxxycdw/padmate/daemon/internal/publicmirror/mirrortest"
)

func TestRefusesInTheMirror(t *testing.T) {
	root := mirrortest.Root(t, true)
	out := filepath.Join(t.TempDir(), "dist")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"-version", "v1.0.0", "-root", root, "-bin", filepath.Join(root, "bin"), "-out", out}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), publicmirror.PackagingRefusal) {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("a refused run made %s", out)
	}
}
