package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeProcess is the padmated process of a quit test: it exits when exit
// is closed.
type fakeProcess struct {
	exit chan struct{}
	name string
	path string

	mu     sync.Mutex
	closed bool
}

func newFakeProcess(name string) *fakeProcess {
	return &fakeProcess{exit: make(chan struct{}), name: name}
}

// newFakeProcessAt is a process whose executable is the file path.
func newFakeProcessAt(path string) *fakeProcess {
	return &fakeProcess{exit: make(chan struct{}), name: filepath.Base(path), path: path}
}

func (p *fakeProcess) Path() string { return p.path }

func (p *fakeProcess) Wait(ctx context.Context) error {
	select {
	case <-p.exit:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *fakeProcess) Name() string { return p.name }

func (p *fakeProcess) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
}

func (p *fakeProcess) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// useFakeProcess makes quit open p, whatever the pid, for the rest of the
// test, and counts how often.
func useFakeProcess(t *testing.T, p *fakeProcess) *int {
	t.Helper()
	old := quitOpenProcess
	t.Cleanup(func() { quitOpenProcess = old })
	opened := new(int)
	quitOpenProcess = func(uint32) (quitProcess, error) {
		*opened++
		return p, nil
	}
	return opened
}

// useNoProcess makes quit find no process of the pid.
func useNoProcess(t *testing.T) *int {
	t.Helper()
	old := quitOpenProcess
	t.Cleanup(func() { quitOpenProcess = old })
	opened := new(int)
	quitOpenProcess = func(uint32) (quitProcess, error) {
		*opened++
		return nil, errProcessGone
	}
	return opened
}

// writeDiscovery writes a valid daemon.json for a pipe nobody listens on.
func writeDiscovery(t *testing.T, dir string) {
	t.Helper()
	const doc = `{"pipe":"\\\\.\\pipe\\padmatectl-quit-nobody","pid":4242,"protocol":1,"token":"` +
		"0000000000000000000000000000000000000000000000000000000000000000" + `"}`
	if err := os.WriteFile(filepath.Join(dir, "daemon.json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestQuitUsageAndArguments(t *testing.T) {
	code, stdout, stderr := runCmd(t, "quit", "--help")
	if code != 0 || !strings.Contains(stdout, "usage: padmatectl quit") || !strings.Contains(stdout, "--timeout") || stderr != "" {
		t.Fatalf("quit --help: %d %q %q", code, stdout, stderr)
	}
	for _, args := range [][]string{
		{"quit", "now"},
		{"quit", "--timeout", "0"},
		{"quit", "--timeout", "-1s"},
		{"quit", "--timeout", "2m"},
		{"quit", "--timeout", "soon"},
		{"quit", "--bogus"},
	} {
		code, stdout, stderr := runCmd(t, args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "usage: padmatectl quit") {
			t.Errorf("%v: %d %q %q, want 2 and the usage on stderr", args, code, stdout, stderr)
		}
	}
	// It is listed in the command overview.
	if _, stdout, _ := runCmd(t, "--help"); !strings.Contains(stdout, "quit") {
		t.Errorf("--help does not list quit:\n%s", stdout)
	}
}

// TestQuitNotRunning: no padmated to ask is a success (the installer has
// nothing to stop), and nothing is opened or called.
func TestQuitNotRunning(t *testing.T) {
	t.Run("no discovery file", func(t *testing.T) {
		opened := useNoProcess(t)
		code, stdout, stderr := runCmd(t, "quit", "--ipc-dir", t.TempDir())
		if code != 0 || !strings.Contains(stdout, "padmated is not running") || stderr != "" || *opened != 0 {
			t.Fatalf("%d %q %q opened=%d", code, stdout, stderr, *opened)
		}
	})
	t.Run("unreadable discovery file", func(t *testing.T) {
		opened := useNoProcess(t)
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "daemon.json"), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		code, stdout, _ := runCmd(t, "quit", "--ipc-dir", dir)
		if code != 0 || !strings.Contains(stdout, "padmated is not running (") || *opened != 0 {
			t.Fatalf("%d %q opened=%d", code, stdout, *opened)
		}
	})
	t.Run("stale discovery file", func(t *testing.T) {
		opened := useNoProcess(t)
		dir := t.TempDir()
		writeDiscovery(t, dir)
		code, stdout, stderr := runCmd(t, "quit", "--ipc-dir", dir)
		if code != 0 || !strings.Contains(stdout, "padmated is not running") || !strings.Contains(stdout, "4242") || stderr != "" || *opened != 1 {
			t.Fatalf("%d %q %q opened=%d", code, stdout, stderr, *opened)
		}
	})
}

// TestQuitProcessOfAnotherImage: the pid of a stale discovery file that
// another program has taken is not padmated, and nothing answers there.
func TestQuitProcessOfAnotherImage(t *testing.T) {
	other := newFakeProcess("notepad.exe")
	useFakeProcess(t, other)
	dir := t.TempDir()
	writeDiscovery(t, dir)
	start := time.Now()
	code, stdout, stderr := runCmd(t, "quit", "--timeout", "5s", "--ipc-dir", dir)
	if code != 0 || !strings.Contains(stdout, "notepad.exe") || stderr != "" {
		t.Fatalf("%d %q %q", code, stdout, stderr)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("waited for a program that is not padmated")
	}
	if !other.isClosed() {
		t.Fatal("the process handle was left open")
	}
}

// installTree is a folder with a padmated.exe in resources/bin, and the path
// of that file.
func installTree(t *testing.T) (dir, exe string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "Install dir")
	exe = filepath.Join(dir, "resources", "bin", "padmated.exe")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, exe
}

func TestQuitInvalidUnder(t *testing.T) {
	for _, args := range [][]string{
		{"quit", "--under", ""},
		{"quit", "--under", "relative"},
		{"quit", "--under"},
	} {
		code, stdout, stderr := runCmd(t, args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "usage: padmatectl quit") {
			t.Errorf("%v: %d %q %q, want 2 and the usage on stderr", args, code, stdout, stderr)
		}
	}
	if _, stdout, _ := runCmd(t, "quit", "--help"); !strings.Contains(stdout, "--under") {
		t.Errorf("the usage does not mention --under:\n%s", stdout)
	}
}

// TestQuitUnderLeavesAnotherFolderAlone: the padmated that the discovery file
// names runs from outside the folder given with --under (a development build
// or a portable copy). It is not asked to exit and not waited for, and that
// is a success: nothing of this installation runs.
func TestQuitUnderLeavesAnotherFolderAlone(t *testing.T) {
	dir, _ := installTree(t)
	_, otherExe := installTree(t)
	other := newFakeProcessAt(otherExe)
	opened := useFakeProcess(t, other)
	disc := t.TempDir()
	writeDiscovery(t, disc)
	start := time.Now()
	code, stdout, stderr := runCmd(t, "quit", "--timeout", "5s", "--ipc-dir", disc, "--under", dir)
	if code != 0 || !strings.Contains(stdout, "left alone") || !strings.Contains(stdout, "4242") || stderr != "" {
		t.Fatalf("%d %q %q", code, stdout, stderr)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("waited for a padmated of another folder")
	}
	if *opened != 1 || !other.isClosed() {
		t.Fatalf("process opened %d times, closed %v", *opened, other.isClosed())
	}
}

// TestQuitUnderUnknownPath: without the file of the process, whose folder it
// is cannot be told, and quit does not guess either way.
func TestQuitUnderUnknownPath(t *testing.T) {
	dir, _ := installTree(t)
	p := newFakeProcess("padmated.exe")
	useFakeProcess(t, p)
	disc := t.TempDir()
	writeDiscovery(t, disc)
	start := time.Now()
	code, stdout, stderr := runCmd(t, "quit", "--timeout", "5s", "--ipc-dir", disc, "--under", dir)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "4242") || !strings.Contains(stderr, "runs from") {
		t.Fatalf("%d %q %q", code, stdout, stderr)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("waited for a padmated whose folder is unknown")
	}
	if !p.isClosed() {
		t.Fatal("the process handle was left open")
	}
}

// TestQuitUnderOwnFolder: a padmated of the folder is dealt with as without
// --under (here nothing answers, so quit waits for the process to end).
func TestQuitUnderOwnFolder(t *testing.T) {
	dir, exe := installTree(t)
	own := newFakeProcessAt(exe)
	useFakeProcess(t, own)
	time.AfterFunc(150*time.Millisecond, func() { close(own.exit) })
	disc := t.TempDir()
	writeDiscovery(t, disc)
	// The folder is named with another spelling of its path.
	code, stdout, stderr := runCmd(t, "quit", "--timeout", "5s", "--ipc-dir", disc, "--under", strings.ToUpper(dir)+string(filepath.Separator))
	if code != 0 || !strings.Contains(stdout, "padmated stopped") || stderr != "" {
		t.Fatalf("%d %q %q", code, stdout, stderr)
	}
}

func TestPathInside(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "inst")
	sep := string(filepath.Separator)
	for _, c := range []struct {
		name, path, dir string
		want            bool
	}{
		{"file below", filepath.Join(dir, "resources", "bin", "padmated.exe"), dir, true},
		{"file directly in it", filepath.Join(dir, "padmate.exe"), dir, true},
		{"folder with a trailing separator", filepath.Join(dir, "padmate.exe"), dir + sep, true},
		{"case differs", strings.ToUpper(filepath.Join(dir, "bin", "A.EXE")), strings.ToLower(dir), true},
		{"dotdot is resolved", filepath.Join(dir, "bin", "..", "..", "other", "a.exe"), dir, false},
		{"sibling with the same prefix", filepath.Join(base, "inst2", "padmated.exe"), dir, false},
		{"the folder itself", dir, dir, false},
		{"parent", base, dir, false},
		{"empty path", "", dir, false},
		{"empty folder", filepath.Join(dir, "a.exe"), "", false},
	} {
		if got := pathInside(c.path, c.dir); got != c.want {
			t.Errorf("%s: pathInside(%q, %q) = %v, want %v", c.name, c.path, c.dir, got, c.want)
		}
	}
}
