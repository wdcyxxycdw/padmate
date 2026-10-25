//go:build windows

package logging

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotationWindowsReaderPreventsRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "padmated.log")
	var diagnostics []error
	w, err := openRotatingFile(path, 1, 3, func(err error) { diagnostics = append(diagnostics, err) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if _, err := w.Write([]byte("{\"msg\":\"one\"}\n")); err != nil {
		t.Fatal(err)
	}
	// Go's Windows file handle shares read/write, but not deletion. This
	// models a reader holding the log open while the rotation renames it.
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	if _, err := w.Write([]byte("{\"msg\":\"two\"}\n")); err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 1 || len(lines(t, readLog(t, path))) != 2 {
		t.Fatalf("failed rename lost records or error: %v", diagnostics)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("{\"msg\":\"three\"}\n")); err != nil {
		t.Fatal(err)
	}
	if len(lines(t, readLog(t, path+".1"))) != 2 || len(lines(t, readLog(t, path))) != 1 {
		t.Fatal("rotation did not recover after reader closed")
	}
}
