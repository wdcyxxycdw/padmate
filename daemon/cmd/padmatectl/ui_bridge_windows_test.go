//go:build windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
)

// TestUIBridgeDeliversEventSentRightBeforeClose: what the daemon wrote
// before it closed the connection, daemon.exiting above all (DMN-22), still
// reaches the UI: the bridge prints it before it exits. Each round races
// the event with the close, behind a burst of log events that a slow UI
// reads one by one.
func TestUIBridgeDeliversEventSentRightBeforeClose(t *testing.T) {
	for round := range 20 {
		e, err := ipc.Listen(`\\.\pipe\padmatectl-bridge-exit-`+rand.Text(), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		hub := ipc.NewHub()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		served := make(chan error, 1)
		go func() {
			served <- ipc.Serve(ctx, e, hub, func(*ipc.Session) jrpc2.Assigner {
				return handler.Map{"ui.register": func(context.Context, *jrpc2.Request) (any, error) { return true, nil }}
			})
		}()
		stdinR, stdinW := io.Pipe()
		stdoutR, stdoutW := io.Pipe()
		lines := make(chan string, 64)
		var slow atomic.Bool
		go func() {
			defer close(lines)
			scanner := bufio.NewScanner(stdoutR)
			for scanner.Scan() {
				lines <- scanner.Text()
				if slow.Load() {
					time.Sleep(time.Millisecond)
				}
			}
		}()
		var stderr bytes.Buffer
		code := make(chan int, 1)
		go func() {
			code <- uiBridgeMain(context.Background(), []string{"--ipc-dir", e.Dir, "--ui-pid", "4242", "--timeout", "3s"}, stdinR, stdoutW, &stderr)
			_ = stdoutW.Close()
		}()
		next := func(what string) string {
			t.Helper()
			select {
			case line, ok := <-lines:
				if !ok {
					t.Fatalf("round %d: bridge output ended before %s; stderr %s", round, what, &stderr)
				}
				return line
			case <-time.After(5 * time.Second):
				t.Fatalf("round %d: no %s", round, what)
			}
			return ""
		}
		if line := next("ready"); !strings.Contains(line, `"ready":true`) {
			t.Fatalf("round %d: %s", round, line)
		}
		if _, err := io.WriteString(stdinW, `{"id":1,"method":"subscribe","params":{"topics":["log","daemon.exiting"]}}`+"\n"); err != nil {
			t.Fatal(err)
		}
		if line := next("subscription"); !strings.Contains(line, `"result"`) {
			t.Fatalf("round %d: %s", round, line)
		}
		slow.Store(true)
		for i := range 30 {
			hub.Publish("log", map[string]int{"seq": i})
		}
		if n := len(hub.PublishSync(ctx, "daemon.exiting", map[string]string{"reason": "tray"})); n != 1 {
			t.Fatalf("round %d: event written to %d connections", round, n)
		}
		cancel() // the daemon closes every connection at once
		var rest []string
		for line := range lines {
			rest = append(rest, line)
		}
		if got := strings.Join(rest, "\n"); !strings.Contains(got, `{"topic":"daemon.exiting","data":{"reason":"tray"}}`) {
			t.Fatalf("round %d: bridge exited without the event; stdout %q, stderr %s", round, got, &stderr)
		}
		if c := <-code; c != 1 {
			t.Fatalf("round %d: bridge exit %d, want 1 (the daemon went away)", round, c)
		}
		_ = stdinW.Close()
		if err := <-served; err != nil {
			t.Fatal(err)
		}
	}
}
