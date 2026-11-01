package profilelib

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// openFor keeps name open for reading, as a reader of the daemon would, until
// hold has passed.
func openFor(t *testing.T, name string, hold time.Duration) <-chan struct{} {
	t.Helper()
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() {
		time.Sleep(hold)
		_ = f.Close()
		close(closed)
	}()
	return closed
}

// TestAtomicReplaceWaitsForAReader: a file that a reader has open is replaced
// once the reader lets go, within the waits of replace; Windows alone would
// refuse the rename (a sharing violation) while it is open.
func TestAtomicReplaceWaitsForAReader(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "profile.json")
	if err := os.WriteFile(name, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	closed := openFor(t, name, 150*time.Millisecond)
	if err := atomicReplace(root, "profile.json", []byte("new")); err != nil {
		t.Fatalf("replace while a reader had the file: %v", err)
	}
	<-closed
	got, err := os.ReadFile(name)
	if err != nil || string(got) != "new" {
		t.Fatalf("file = %q, %v; want new", got, err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".padmate-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left: %v", leftovers)
	}
}

// TestAtomicReplaceGivesUp: a reader that keeps the file longer than the
// waits makes the replacement fail as before, and the old file stays whole.
func TestAtomicReplaceGivesUp(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "profile.json")
	if err := os.WriteFile(name, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	closed := openFor(t, name, 2*time.Second)
	start := time.Now()
	err = atomicReplace(root, "profile.json", []byte("new"))
	took := time.Since(start)
	if err == nil || !fileInUse(err) {
		t.Fatalf("replace = %v; want a file-in-use error", err)
	}
	if took > 1500*time.Millisecond {
		t.Fatalf("gave up after %v; want about 0.6 s", took)
	}
	<-closed
	if got, _ := os.ReadFile(name); string(got) != "old" {
		t.Fatalf("file = %q; want old", got)
	}
}
