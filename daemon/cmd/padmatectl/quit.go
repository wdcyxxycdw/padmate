package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
)

const quitUsage = `usage: padmatectl quit [--timeout 10s] [--ipc-dir DIR] [--under DIR]

Asks the running padmated to exit as the tray's 退出 does (daemon.exit with
reason "installer"): it pauses the mapping and releases every key, lets the UI
save its last edit, then exits. Waits until the padmated process has exited,
--timeout at most (default 10s, at most 1m), so that the installer can replace
or remove its files.

--under DIR (an absolute path) limits quit to one installation: a padmated
whose executable is not inside DIR, such as a development build or a portable
copy that owns the discovery file, is left alone and not asked to exit.

Exit codes: 0 padmated has exited, none is running, or the one running is not
under --under DIR; 1 padmated is still running after --timeout, refused, does
not answer, or with --under its folder cannot be told; 2 invalid arguments.
When it fails, padmatectl release-all releases the keys of a running
--inject padmated without IPC.
`

// quitDaemonImage is the file name of the daemon's executable. A process of
// another name that holds the pid of a stale daemon.json is not padmated.
const quitDaemonImage = "padmated.exe"

// quitProcess is the padmated process quit waits for, opened before the
// request so that its exit is seen even though padmated removes daemon.json
// as it begins to stop.
type quitProcess interface {
	// Wait returns nil once the process has exited, or ctx's error.
	Wait(ctx context.Context) error
	// Name is the base name of the executable, "" when unknown.
	Name() string
	// Path is the full path of the executable, "" when unknown.
	Path() string
	Close()
}

// errProcessGone is what quitOpenProcess returns for a pid without a
// process.
var errProcessGone = errors.New("no such process")

// quitOpenProcess opens the process with pid; tests replace it.
var quitOpenProcess = openQuitProcess

// errNotRunning is the result of a quit that finds no padmated.
type errNotRunning struct{ why string }

func (e errNotRunning) Error() string {
	if e.why == "" {
		return "padmated is not running"
	}
	return "padmated is not running (" + e.why + ")"
}

// errOutside is the result of a quit --under that finds a padmated of
// another folder.
type errOutside struct {
	pid         uint32
	path, under string
}

func (e errOutside) Error() string {
	return fmt.Sprintf("padmated (pid %d) runs from %s, not under %s: left alone", e.pid, e.path, e.under)
}

func runQuit(args []string, stdout, stderr io.Writer) int {
	ctx, cancel := signal.NotifyContext(context.Background(), stopSignals...)
	defer cancel()
	return quitMain(ctx, args, stdout, stderr)
}

func quitMain(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("quit", flag.ContinueOnError)
	dir := flags.String("ipc-dir", ipcDiscoveryDir, "daemon discovery directory")
	timeout := flags.Duration("timeout", 10*time.Second, "how long to wait for padmated to exit")
	under := flags.String("under", "", "leave a padmated alone whose executable is not inside this folder")
	if code, ok := parseFlags(flags, args, quitUsage, stdout, stderr); !ok {
		return code
	}
	underGiven := false
	flags.Visit(func(f *flag.Flag) { underGiven = underGiven || f.Name == "under" })
	if flags.NArg() != 0 || *timeout <= 0 || *timeout > time.Minute || (underGiven && !filepath.IsAbs(*under)) {
		fmt.Fprint(stderr, "padmatectl: invalid quit arguments\n"+quitUsage)
		return 2
	}
	err := quitDaemon(ctx, *dir, *under, *timeout)
	var notRunning errNotRunning
	var outside errOutside
	switch {
	case err == nil:
		fmt.Fprintln(stdout, "padmatectl: padmated stopped")
		return 0
	case errors.As(err, &notRunning):
		fmt.Fprintln(stdout, "padmatectl:", notRunning)
		return 0
	case errors.As(err, &outside):
		fmt.Fprintln(stdout, "padmatectl:", outside)
		return 0
	}
	fmt.Fprintln(stderr, "padmatectl quit:", err)
	return 1
}

// quitDaemon asks the padmated that dir's daemon.json names to exit and
// waits for its process to end, timeout at most. It returns nil once the
// process has exited, an errNotRunning when there is none, and otherwise
// why padmated is still there. With under set, a padmated whose executable
// is not inside that folder is an errOutside: it is neither asked nor waited
// for, because the installer that passes its folder must not stop a
// development build or a portable copy, and either can own the discovery file.
func quitDaemon(ctx context.Context, dir, under string, timeout time.Duration) error {
	d, err := ipc.ReadDiscovery(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return errNotRunning{}
	case err != nil:
		return errNotRunning{why: err.Error()}
	}
	proc, err := quitOpenProcess(d.PID)
	switch {
	case errors.Is(err, errProcessGone):
		return errNotRunning{why: fmt.Sprintf("the process %d of the discovery file has ended", d.PID)}
	case err != nil:
		return fmt.Errorf("cannot watch padmated (pid %d): %w", d.PID, err)
	}
	defer proc.Close()
	if under != "" {
		path := proc.Path()
		if path == "" {
			return fmt.Errorf("cannot tell which folder padmated (pid %d) runs from", d.PID)
		}
		if !quitUnder(path, under) {
			return errOutside{pid: d.PID, path: path, under: under}
		}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	asked, err := askToExit(ctx, dir, d.PID)
	var refused *jrpc2.Error
	if errors.As(err, &refused) {
		return fmt.Errorf("padmated (pid %d) refused to exit: %w", d.PID, err)
	}
	detail := ""
	if err != nil && !asked {
		// Nothing answers at the pid. If the process is no padmated, the
		// discovery file is a stale one, left by a padmated that did not exit
		// by itself.
		if name := proc.Name(); name != "" && !strings.EqualFold(name, quitDaemonImage) {
			return errNotRunning{why: fmt.Sprintf("the process %d of the discovery file is %s", d.PID, name)}
		}
		// Otherwise it may be on its way out already: wait for it.
		detail = ": " + err.Error()
	}
	if err := proc.Wait(ctx); err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("padmated (pid %d) did not exit within %v%s", d.PID, timeout, detail)
		case errors.Is(err, context.Canceled):
			return errors.New("interrupted while waiting for padmated to exit")
		}
		return fmt.Errorf("waiting for padmated (pid %d): %w", d.PID, err)
	}
	return nil
}

// quitUnder reports whether the executable path is inside the folder dir. Both
// are put in long form first: Windows reports a path as it was given, short
// (8.3) or long.
func quitUnder(path, dir string) bool {
	return pathInside(quitLongPath(path), quitLongPath(dir))
}

// pathInside reports whether path lies below dir, compared without regard to
// case, as Windows does. dir itself is not inside dir.
func pathInside(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	path, dir = filepath.Clean(path), filepath.Clean(dir)
	prefix := dir
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return len(path) > len(prefix) && strings.EqualFold(path[:len(prefix)], prefix)
}

// askToExit connects to the padmated with pid and calls daemon.exit. asked
// is true once the request is sent: an error after that is a connection
// that padmated closed because it is exiting, or an error response. The
// connection is closed as soon as the reply is read, which lets padmated go
// on with its exit.
func askToExit(ctx context.Context, dir string, pid uint32) (asked bool, err error) {
	conn, d, err := ipc.Dial(ctx, dir)
	if err != nil {
		return false, err
	}
	if d.PID != pid {
		_ = conn.Close()
		return false, errors.New("the discovery file changed")
	}
	client, err := ipc.NewClient(ctx, conn, d, "padmatectl", nil)
	if err != nil {
		return false, err
	}
	defer client.Close()
	return true, client.CallResult(ctx, "daemon.exit", map[string]string{"reason": "installer"}, new(any))
}
