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

// In the public code mirror stage reads nothing and makes nothing: it refuses
// with the reason. The root it is given decides, so a test run in a temporary
// folder is not affected by the folder it runs in.
func TestStageRefusesInTheMirror(t *testing.T) {
	root := mirrortest.Root(t, true)
	out := filepath.Join(t.TempDir(), "stage")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"stage", "-version", "v1.0.0", "-root", root, "-bin", filepath.Join(root, "bin"), "-out", out}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), publicmirror.PackagingRefusal) {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("a refused stage made %s", out)
	}
}

func TestStageOutsideTheMirrorGoesOn(t *testing.T) {
	root := mirrortest.Root(t, false)
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"stage", "-version", "v1.0.0", "-root", root, "-bin", filepath.Join(root, "bin"), "-out", filepath.Join(t.TempDir(), "stage")}, &stdout, &stderr)
	if code != 1 || strings.Contains(stderr.String(), publicmirror.PackagingRefusal) {
		t.Fatalf("exit %d, stderr %q: want the error of the missing binaries", code, stderr.String())
	}
}
