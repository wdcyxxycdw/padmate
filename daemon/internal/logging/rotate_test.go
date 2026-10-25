package logging

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readLog(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRotationRetentionAndLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "padmated.log")
	log, closeLog, err := Open(Options{File: path, MaxBytes: 1, Backups: 3})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 7 {
		// Each record exceeds the limit by itself. It must remain whole;
		// rotation happens before the next record, never mid-line.
		log.Info(fmt.Sprintf("record %d", i), "long", strings.Repeat("x", 100))
	}
	if err := closeLog.Close(); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		file := path
		if i > 0 {
			file += fmt.Sprintf(".%d", i)
		}
		b := readLog(t, file)
		got := lines(t, b)
		if len(got) != 1 || got[0]["msg"] != fmt.Sprintf("record %d", 6-i) || b[len(b)-1] != '\n' {
			t.Fatalf("%s has incomplete or wrong records: %q", file, b)
		}
	}
	if _, err := os.Stat(path + ".4"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected fourth backup: %v", err)
	}
}

func TestRotationBoundaryAndExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "padmated.log")
	first, second := []byte("{\"msg\":\"one\"}\n"), []byte("{\"msg\":\"two\"}\n")
	if err := os.WriteFile(path, first, 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := openRotatingFile(path, int64(len(first)+len(second)), 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if _, err := w.Write(second); err != nil {
		t.Fatal(err)
	}
	if got := readLog(t, path); !bytes.Equal(got, append(bytes.Clone(first), second...)) {
		t.Fatalf("exact boundary did not append existing file: %q", got)
	}
	if _, err := os.Stat(path + ".1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rotated at exact boundary: %v", err)
	}
	if _, err := w.Write(first); err != nil {
		t.Fatal(err)
	}
	if got := readLog(t, path+".1"); !bytes.Equal(got, append(bytes.Clone(first), second...)) {
		t.Fatalf("rotation lost prior records: %q", got)
	}
	if got := readLog(t, path); !bytes.Equal(got, first) {
		t.Fatalf("new file=%q", got)
	}
}

func TestRotationFailureKeepsAppendingAndRecovers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "padmated.log")
	var diagnostics []error
	w, err := openRotatingFile(path, 1, 3, func(err error) { diagnostics = append(diagnostics, err) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	blocker := path + ".3"
	if err := os.Mkdir(blocker, 0o700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(blocker, "blocked")
	if err := os.WriteFile(child, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, record := range []string{"{\"msg\":\"one\"}\n", "{\"msg\":\"two\"}\n"} {
		if _, err := w.Write([]byte(record)); err != nil {
			t.Fatal(err)
		}
	}
	if len(diagnostics) != 1 || len(lines(t, readLog(t, path))) != 2 {
		t.Fatalf("failed rotation truncated/dropped records or hid error: %v", diagnostics)
	}
	if err := os.Remove(child); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("{\"msg\":\"three\"}\n")); err != nil {
		t.Fatal(err)
	}
	if len(lines(t, readLog(t, path+".1"))) != 2 || len(lines(t, readLog(t, path))) != 1 {
		t.Fatal("rotation did not recover after obstruction cleared")
	}
}

func TestRotationNewFileFailureRollsBack(t *testing.T) {
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
	realOpen := w.open
	w.open = func(path string, flag int, perm os.FileMode) (*os.File, error) {
		if flag&os.O_EXCL != 0 {
			return nil, os.ErrPermission
		}
		return realOpen(path, flag, perm)
	}
	if _, err := w.Write([]byte("{\"msg\":\"two\"}\n")); err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 1 || len(lines(t, readLog(t, path))) != 2 {
		t.Fatalf("new-file failure lost old/current records: %v", diagnostics)
	}
	w.open = realOpen
	if _, err := w.Write([]byte("{\"msg\":\"three\"}\n")); err != nil {
		t.Fatal(err)
	}
	if len(lines(t, readLog(t, path+".1"))) != 2 {
		t.Fatal("rollback did not preserve records")
	}
}

func TestRotationStderrAndDefaultDiagnostics(t *testing.T) {
	var stderr bytes.Buffer
	log, c, err := Open(Options{File: "-", Stderr: &stderr, MaxBytes: 1, Backups: 1})
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		log.Info("hello")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if len(lines(t, stderr.Bytes())) != 5 {
		t.Fatal("stderr rotated or lost records")
	}
	path := filepath.Join(t.TempDir(), "padmated.log")
	stderr.Reset()
	log, c, err = Open(Options{File: path, Stderr: &stderr, MaxBytes: 1, Backups: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".1", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path+".1", "blocked"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	log.Info("one")
	log.Info("two")
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "logging:") || len(lines(t, readLog(t, path))) != 2 {
		t.Fatalf("default failure diagnostic=%q", stderr.String())
	}
}

func TestRotationRollbackFailureKeepsArchive(t *testing.T) {
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
	realOpen := w.open
	w.open = func(path string, flag int, perm os.FileMode) (*os.File, error) {
		if flag&os.O_EXCL != 0 {
			// A competing filesystem change obstructs both creation of the
			// new active file and rollback of the preserved archive.
			if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return nil, err
			}
			return nil, os.ErrPermission
		}
		return realOpen(path, flag, perm)
	}
	for _, msg := range []string{"two", "three"} {
		if _, err := w.Write(fmt.Appendf(nil, "{\"msg\":\"%s\"}\n", msg)); err != nil {
			t.Fatal(err)
		}
	}
	if len(diagnostics) == 0 || len(lines(t, readLog(t, path+".1"))) != 3 {
		t.Fatalf("rollback failure lost records or error: %v", diagnostics)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	w.open = realOpen
	if _, err := w.Write([]byte("{\"msg\":\"four\"}\n")); err != nil {
		t.Fatal(err)
	}
	if len(lines(t, readLog(t, path+".1"))) != 3 || len(lines(t, readLog(t, path))) != 1 {
		t.Fatal("active file did not recover without losing the archive")
	}
}
