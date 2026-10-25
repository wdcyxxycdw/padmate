//go:build windows

package main

import (
	"context"
	"crypto/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
	"github.com/wdcyxxycdw/padmate/daemon/internal/tray"
)

// exitListener is an IPC client subscribed to daemon.exiting, as the UI's
// bridge is: events has each one's params, and stopped closes when the
// daemon closes the connection.
type exitListener struct {
	client  *jrpc2.Client
	events  chan string
	stopped chan struct{}
}

func listenForExit(t *testing.T, dir string) *exitListener {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, d, err := ipc.Dial(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	l := &exitListener{events: make(chan string, 4), stopped: make(chan struct{})}
	var once sync.Once
	l.client, err = ipc.NewClient(ctx, conn, d, "exit-test", &jrpc2.ClientOptions{
		OnNotify: func(req *jrpc2.Request) {
			if req.Method() == "daemon.exiting" {
				l.events <- req.ParamString()
			}
		},
		OnStop: func(*jrpc2.Client, error) { once.Do(func() { close(l.stopped) }) },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.client.Close() })
	if err := l.client.CallResult(ctx, "subscribe", map[string]any{"topics": []string{"daemon.exiting"}}, new(any)); err != nil {
		t.Fatal(err)
	}
	return l
}

// received waits for the daemon to close the connection, then for the
// notifications that came before, and returns their params.
func (l *exitListener) received(t *testing.T) []string {
	t.Helper()
	select {
	case <-l.stopped:
	case <-time.After(liveWait):
		t.Fatal("the daemon kept the connection open after it stopped")
	}
	_ = l.client.Close() // waits for the notifications being delivered
	var got []string
	for {
		select {
		case p := <-l.events:
			got = append(got, p)
		default:
			return got
		}
	}
}

// TestTrayQuitTellsUIBeforeClosing: choosing 退出 in the tray writes
// daemon.exiting {"reason":"tray"} to the UI's connection before the
// daemon closes it (DMN-22), so the UI quits too; the keys are released
// as on every exit.
func TestTrayQuitTellsUIBeforeClosing(t *testing.T) {
	e, err := ipc.Listen(`\\.\pipe\padmate-tray-exit-`+rand.Text(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	tl, src := sessionLive(t, func(cfg *liveConfig) { cfg.endpoint = e }, nil, "--dry-run", "--watch=false")
	src.events <- input.Event{Device: 1, Kind: input.ButtonDown, Control: "east"}
	tl.waitFor(t, tl.stdout, "send +Space\n")
	ui := listenForExit(t, e.Dir)
	quit, closed := make(chan struct{}, 1), make(chan struct{})
	var once sync.Once
	finished := make(chan int, 1)
	go func() {
		finished <- tl.driveTray(&trayUI{quit: quit, update: func(tray.Presentation, bool, bool) {},
			close: func() { once.Do(func() { close(closed) }) },
			run:   func(ready, exit func(), _ func(error)) { ready(); <-closed; exit() },
		})
	}()
	quit <- struct{}{}
	select {
	case code := <-finished:
		if code != 0 {
			t.Fatalf("tray exit %d\n%s", code, tl.stderr)
		}
	case <-time.After(liveWait):
		t.Fatal("tray exit hung")
	}
	if got := ui.received(t); len(got) != 1 || got[0] != `{"reason":"tray"}` {
		t.Fatalf("daemon.exiting = %q, want one with reason tray", got)
	}
	if !strings.Contains(tl.stdout.String(), "send -Space\n") || !strings.Contains(tl.log.String(), `"msg":"exit announced"`) {
		t.Fatalf("stdout:\n%s\nlog:\n%s", tl.stdout, tl.log)
	}
	checkBalanced(t, tl.stdout.String())
}

// TestStopWithoutTrayTellsUINothing: a stop the user did not choose in
// the tray (here Ctrl+C) sends no daemon.exiting: the UI shows the lost
// connection, as after a crash.
func TestStopWithoutTrayTellsUINothing(t *testing.T) {
	e, err := ipc.Listen(`\\.\pipe\padmate-signal-exit-`+rand.Text(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	tl, _ := sessionLive(t, func(cfg *liveConfig) { cfg.endpoint = e }, nil, "--dry-run", "--watch=false")
	ui := listenForExit(t, e.Dir)
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Fatal(code)
	}
	if got := ui.received(t); len(got) != 0 {
		t.Fatalf("daemon.exiting %q after Ctrl+C", got)
	}
}
