package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
)

func TestRPCPipeAuthenticationAndResult(t *testing.T) {
	e, err := ipc.Listen(`\\.\pipe\padmatectl-rpc-test-`+rand.Text(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	done := make(chan error, 1)
	go func() {
		done <- ipc.Serve(ctx, e, ipc.NewHub(), func(*ipc.Session) jrpc2.Assigner {
			return handler.Map{"status.get": func(_ context.Context, r *jrpc2.Request) (any, error) {
				var p map[string]string
				if err := r.UnmarshalParams(&p); err != nil {
					return nil, err
				}
				return map[string]any{"dryRun": true, "device": p["device"]}, nil
			}}
		})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	var stdout, stderr bytes.Buffer
	code := rpcMain(ctx, "status", []string{"--ipc-dir", e.Dir, "--device=2", "--json"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), `"dryRun":true`) || !strings.Contains(stdout.String(), `"device":"2"`) {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, &stdout, &stderr)
	}
	if strings.Contains(stdout.String()+stderr.String(), e.Discovery.Token) {
		t.Fatal("authentication token was printed")
	}
}

func TestCaptureCLIStreamsAndReleasesOnExit(t *testing.T) {
	e, err := ipc.Listen(`\\.\pipe\padmatectl-stream-test-`+rand.Text(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	hub := ipc.NewHub()
	released := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- ipc.Serve(ctx, e, hub, func(s *ipc.Session) jrpc2.Assigner {
			return handler.Map{"capture.start": func(context.Context, *jrpc2.Request) (any, error) {
				s.OnClose(func() { close(released) })
				hub.Publish("input", map[string]string{"kind": "down", "control": "south"})
				return true, nil
			}}
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	var out, errOut bytes.Buffer
	code := eventsMain(ctx, "capture", []string{"--ipc-dir", e.Dir, "--count=1"}, &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), `"topic":"input"`) || !strings.Contains(out.String(), `"south"`) {
		t.Fatal(code, &out, &errOut)
	}
	select {
	case <-released:
	case <-ctx.Done():
		t.Fatal("CLI exit left capture active")
	}
}
