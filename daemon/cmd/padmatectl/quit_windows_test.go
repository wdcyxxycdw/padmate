//go:build windows

package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"golang.org/x/sys/windows"

	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
)

// quitDaemonUnderTest is a daemon.json and a pipe of the test's own, served
// with methods; stop ends the server as a daemon that exits does.
type quitDaemonUnderTest struct {
	dir  string
	stop context.CancelFunc
}

func startQuitDaemon(t *testing.T, methods func(stop context.CancelFunc) handler.Map) *quitDaemonUnderTest {
	t.Helper()
	e, err := ipc.Listen(`\\.\pipe\padmatectl-quit-test-`+rand.Text(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ipc.Serve(ctx, e, ipc.NewHub(), func(*ipc.Session) jrpc2.Assigner { return methods(cancel) })
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return &quitDaemonUnderTest{dir: e.Dir, stop: cancel}
}

// staleDiscovery returns a directory whose daemon.json names a pipe that
// nothing serves (it belonged to a padmated that did not exit by itself).
func staleDiscovery(t *testing.T) string {
	t.Helper()
	e, err := ipc.Listen(`\\.\pipe\padmatectl-quit-stale-`+rand.Text(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.Dir, "daemon.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return e.Dir
}

// exitRecorder is a daemon.exit that records its params.
type exitRecorder struct {
	mu     sync.Mutex
	params []string
}

func (r *exitRecorder) record(req *jrpc2.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.params = append(r.params, req.ParamString())
}

func (r *exitRecorder) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.params...)
}

func TestQuitAsksThenWaitsForTheProcess(t *testing.T) {
	proc := newFakeProcess("padmated.exe")
	opened := useFakeProcess(t, proc)
	var rec exitRecorder
	d := startQuitDaemon(t, func(context.CancelFunc) handler.Map {
		return handler.Map{"daemon.exit": func(_ context.Context, req *jrpc2.Request) (any, error) {
			rec.record(req)
			// The process ends a little after the reply, as padmated does.
			time.AfterFunc(150*time.Millisecond, func() { close(proc.exit) })
			return map[string]any{"exiting": true, "reason": "installer"}, nil
		}}
	})
	start := time.Now()
	code, stdout, stderr := runCmd(t, "quit", "--ipc-dir", d.dir)
	if code != 0 || !strings.Contains(stdout, "padmatectl: padmated stopped") || stderr != "" {
		t.Fatalf("%d %q %q", code, stdout, stderr)
	}
	if time.Since(start) < 150*time.Millisecond {
		t.Fatal("quit returned before the process had exited")
	}
	if got := rec.got(); len(got) != 1 || got[0] != `{"reason":"installer"}` {
		t.Fatalf("daemon.exit calls: %q", got)
	}
	if *opened != 1 || !proc.isClosed() {
		t.Fatalf("process opened %d times, closed %v", *opened, proc.isClosed())
	}
	disc, err := ipc.ReadDiscovery(d.dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout+stderr, disc.Token) {
		t.Fatal("authentication token was printed")
	}
}

// TestQuitTimeout: a padmated that answers but does not exit in time is a
// failure, and says so.
func TestQuitTimeout(t *testing.T) {
	proc := newFakeProcess("padmated.exe")
	useFakeProcess(t, proc)
	d := startQuitDaemon(t, func(context.CancelFunc) handler.Map {
		return handler.Map{"daemon.exit": func(context.Context, *jrpc2.Request) (any, error) {
			return map[string]any{"exiting": true}, nil
		}}
	})
	start := time.Now()
	code, stdout, stderr := runCmd(t, "quit", "--timeout", "300ms", "--ipc-dir", d.dir)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "did not exit within 300ms") || !strings.Contains(stderr, "pid") {
		t.Fatalf("%d %q %q", code, stdout, stderr)
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 3*time.Second {
		t.Fatalf("took %v for a timeout of 300ms", d)
	}
}

// TestQuitRefused: a daemon that does not know daemon.exit (an older one)
// or refuses it is a failure at once, without waiting out the timeout.
func TestQuitRefused(t *testing.T) {
	for name, methods := range map[string]handler.Map{
		"unknown method": {},
		"refused": {"daemon.exit": func(context.Context, *jrpc2.Request) (any, error) {
			return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "reason must be \"installer\""}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			proc := newFakeProcess("padmated.exe")
			useFakeProcess(t, proc)
			d := startQuitDaemon(t, func(context.CancelFunc) handler.Map { return methods })
			start := time.Now()
			code, stdout, stderr := runCmd(t, "quit", "--timeout", "10s", "--ipc-dir", d.dir)
			if code != 1 || stdout != "" || !strings.Contains(stderr, "refused to exit") {
				t.Fatalf("%d %q %q", code, stdout, stderr)
			}
			if time.Since(start) > 3*time.Second {
				t.Fatal("waited for the timeout")
			}
		})
	}
}

// TestQuitConnectionEndsWithTheDaemon: padmated closes the connection as
// it exits, perhaps before the reply is read; the process is what counts.
func TestQuitConnectionEndsWithTheDaemon(t *testing.T) {
	proc := newFakeProcess("padmated.exe")
	useFakeProcess(t, proc)
	d := startQuitDaemon(t, func(stop context.CancelFunc) handler.Map {
		return handler.Map{"daemon.exit": func(context.Context, *jrpc2.Request) (any, error) {
			close(proc.exit)
			stop() // closes every connection
			return map[string]any{"exiting": true}, nil
		}}
	})
	code, stdout, stderr := runCmd(t, "quit", "--timeout", "5s", "--ipc-dir", d.dir)
	if code != 0 || !strings.Contains(stdout, "padmated stopped") {
		t.Fatalf("%d %q %q", code, stdout, stderr)
	}
}

// TestQuitUnreachableDaemon: a padmated process that nothing answers from is
// not gone. quit fails if it stays, and succeeds if it ends meanwhile,
// as padmated does when it is already stopping and has closed its pipe.
func TestQuitUnreachableDaemon(t *testing.T) {
	t.Run("stays", func(t *testing.T) {
		useFakeProcess(t, newFakeProcess("padmated.exe"))
		code, stdout, stderr := runCmd(t, "quit", "--timeout", "300ms", "--ipc-dir", staleDiscovery(t))
		if code != 1 || stdout != "" || !strings.Contains(stderr, "did not exit within 300ms") || !strings.Contains(stderr, "pipe") {
			t.Fatalf("%d %q %q", code, stdout, stderr)
		}
	})
	t.Run("ends meanwhile", func(t *testing.T) {
		proc := newFakeProcess("padmated.exe")
		useFakeProcess(t, proc)
		time.AfterFunc(150*time.Millisecond, func() { close(proc.exit) })
		code, stdout, stderr := runCmd(t, "quit", "--timeout", "5s", "--ipc-dir", staleDiscovery(t))
		if code != 0 || !strings.Contains(stdout, "padmated stopped") {
			t.Fatalf("%d %q %q", code, stdout, stderr)
		}
	})
	t.Run("another program", func(t *testing.T) {
		useFakeProcess(t, newFakeProcess("notepad.exe"))
		code, stdout, _ := runCmd(t, "quit", "--timeout", "5s", "--ipc-dir", staleDiscovery(t))
		if code != 0 || !strings.Contains(stdout, "notepad.exe") {
			t.Fatalf("%d %q", code, stdout)
		}
	})
}

// TestQuitHelperProcess is the process that TestQuitProcess waits for: it
// sleeps for the duration in PADMATECTL_QUIT_HELPER, then exits.
func TestQuitHelperProcess(t *testing.T) {
	v := os.Getenv("PADMATECTL_QUIT_HELPER")
	if v == "" {
		t.Skip("helper of TestQuitProcess")
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		os.Exit(3)
	}
	time.Sleep(d)
	os.Exit(0)
}

func startHelper(t *testing.T, sleep time.Duration) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestQuitHelperProcess$")
	cmd.Env = append(os.Environ(), "PADMATECTL_QUIT_HELPER="+sleep.String())
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd
}

// TestQuitProcess: the Windows process handle waits for a real process to
// exit, is cut short by its context, and finds no process for a pid that
// has none.
func TestQuitProcess(t *testing.T) {
	t.Run("exits", func(t *testing.T) {
		cmd := startHelper(t, 300*time.Millisecond)
		p, err := openQuitProcess(uint32(cmd.Process.Pid))
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		if want := filepath.Base(os.Args[0]); !strings.EqualFold(p.Name(), want) {
			t.Fatalf("Name = %q, want %q", p.Name(), want)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		start := time.Now()
		if err := p.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(start); d < 100*time.Millisecond {
			t.Fatalf("Wait returned after %v, before the process had exited", d)
		}
	})
	t.Run("context ends the wait", func(t *testing.T) {
		cmd := startHelper(t, time.Minute)
		p, err := openQuitProcess(uint32(cmd.Process.Pid))
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		start := time.Now()
		if err := p.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Wait = %v, want the deadline", err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("Wait took %v after its context ended", d)
		}
	})
	t.Run("no such process", func(t *testing.T) {
		cmd := startHelper(t, time.Millisecond)
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
		if _, err := openQuitProcess(uint32(cmd.Process.Pid)); !errors.Is(err, errProcessGone) {
			t.Fatalf("openQuitProcess of an ended process = %v, want errProcessGone", err)
		}
	})
}

// TestQuitUnderAsksItsOwnFolder: a padmated of the folder given with --under
// is asked to exit, as without it.
func TestQuitUnderAsksItsOwnFolder(t *testing.T) {
	dir, exe := installTree(t)
	proc := newFakeProcessAt(exe)
	useFakeProcess(t, proc)
	var rec exitRecorder
	d := startQuitDaemon(t, func(context.CancelFunc) handler.Map {
		return handler.Map{"daemon.exit": func(_ context.Context, req *jrpc2.Request) (any, error) {
			rec.record(req)
			time.AfterFunc(100*time.Millisecond, func() { close(proc.exit) })
			return map[string]any{"exiting": true, "reason": "installer"}, nil
		}}
	})
	code, stdout, stderr := runCmd(t, "quit", "--ipc-dir", d.dir, "--under", dir)
	if code != 0 || !strings.Contains(stdout, "padmated stopped") || stderr != "" {
		t.Fatalf("%d %q %q", code, stdout, stderr)
	}
	if got := rec.got(); len(got) != 1 {
		t.Fatalf("daemon.exit calls: %q, want one", got)
	}
}

// TestQuitUnderNeverAsksAnotherFolder: a live padmated of another folder that
// owns the discovery file, which is what a development build or a portable
// copy is to the installer, is not sent daemon.exit.
func TestQuitUnderNeverAsksAnotherFolder(t *testing.T) {
	dir, _ := installTree(t)
	_, otherExe := installTree(t)
	other := newFakeProcessAt(otherExe)
	useFakeProcess(t, other)
	var rec exitRecorder
	d := startQuitDaemon(t, func(context.CancelFunc) handler.Map {
		return handler.Map{"daemon.exit": func(_ context.Context, req *jrpc2.Request) (any, error) {
			rec.record(req)
			return map[string]any{"exiting": true}, nil
		}}
	})
	code, stdout, stderr := runCmd(t, "quit", "--ipc-dir", d.dir, "--under", dir)
	if code != 0 || !strings.Contains(stdout, "left alone") || stderr != "" {
		t.Fatalf("%d %q %q", code, stdout, stderr)
	}
	if got := rec.got(); len(got) != 0 {
		t.Fatalf("daemon.exit was called on a padmated of another folder: %q", got)
	}
}

// TestQuitLongPath: Windows reports a path in the short (8.3) or the long
// form, and --under may be given in either.
func TestQuitLongPath(t *testing.T) {
	long := filepath.Join(t.TempDir(), "a rather long folder name")
	file := filepath.Join(long, "padmated.exe")
	if err := os.MkdirAll(long, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := windows.UTF16PtrFromString(long)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, windows.MAX_PATH)
	n, err := windows.GetShortPathName(in, &buf[0], uint32(len(buf)))
	short := windows.UTF16ToString(buf[:n])
	if err != nil || n == 0 || strings.EqualFold(short, long) {
		t.Skipf("no short name for %s (8.3 names are off for this volume): %q, %v", long, short, err)
	}
	if got := quitLongPath(short); !strings.EqualFold(got, long) {
		t.Errorf("quitLongPath(%q) = %q, want %q", short, got, long)
	}
	if !quitUnder(file, short) {
		t.Errorf("%s is not under its folder's short name %s", file, short)
	}
	// A path that does not exist is returned as given.
	missing := filepath.Join(long, "missing", "x.exe")
	if got := quitLongPath(missing); got != missing {
		t.Errorf("quitLongPath of a missing path = %q, want it unchanged", got)
	}
}

// TestQuitDiscoveryFileIsJSON guards the stale-file helper: the discovery
// it keeps is the one padmated wrote.
func TestQuitDiscoveryFileIsJSON(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(staleDiscovery(t), "daemon.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d ipc.Discovery
	if err := json.Unmarshal(data, &d); err != nil || d.PID == 0 || d.Pipe == "" {
		t.Fatalf("%s %v", data, err)
	}
}
