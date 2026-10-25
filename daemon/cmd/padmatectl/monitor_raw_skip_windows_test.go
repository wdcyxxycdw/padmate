package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
)

// TestMonitorRawIPCSkipsADeviceThatFails: a device whose raw events cannot be
// started (unplugged between devices.list and the ask, or a joystick SDL
// cannot open) is reported on stderr and left out; the others are watched.
// When --device names it, though, there is nothing else to watch and it is an
// error.
func TestMonitorRawIPCSkipsADeviceThatFails(t *testing.T) {
	e, err := ipc.Listen(`\\.\pipe\padmatectl-raw-skip-test-`+rand.Text(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	hub := ipc.NewHub()
	var mu sync.Mutex
	var asked []string
	done := make(chan error, 1)
	go func() {
		done <- ipc.Serve(ctx, e, hub, func(*ipc.Session) jrpc2.Assigner {
			return handler.Map{
				"capture.start": func(context.Context, *jrpc2.Request) (any, error) { return map[string]bool{}, nil },
				"devices.list": func(context.Context, *jrpc2.Request) (any, error) {
					return []map[string]any{
						{"info": input.DeviceInfo{ID: 1, Name: "Gone Stick", Type: "unknown"}, "key": "k1"},
						{"info": input.DeviceInfo{ID: 2, Name: "Other Stick", Type: "unknown"}, "key": "k2"},
					}, nil
				},
				"devices.rawInfo": func(_ context.Context, req *jrpc2.Request) (any, error) {
					var p struct {
						Device string `json:"device"`
					}
					if err := req.UnmarshalParams(&p); err != nil {
						return nil, err
					}
					mu.Lock()
					asked = append(asked, p.Device)
					mu.Unlock()
					if p.Device == "1" {
						return nil, &jrpc2.Error{Code: -32004, Message: "device is not connected"}
					}
					go func() {
						time.Sleep(50 * time.Millisecond)
						hub.Publish("input.raw", map[string]any{"timeNs": 2_000_000, "device": 2, "kind": "button", "index": 3, "value": 1})
					}()
					return map[string]any{}, nil
				},
			}
		})
	}()
	t.Cleanup(func() { cancel(); <-done })

	var out, errOut bytes.Buffer
	code := monitorMain(ctx, []string{"--ipc-dir", e.Dir, "--raw", "--duration", "700ms"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("monitor --raw = %d, stderr %q; a device that fails must not end the monitor", code, &errOut)
	}
	if !strings.Contains(out.String(), "2 raw button 3 down") {
		t.Errorf("the other device is not watched:\n%s", &out)
	}
	if !strings.Contains(errOut.String(), "device 1") || !strings.Contains(errOut.String(), "device is not connected") {
		t.Errorf("stderr does not say which device was left out: %q", &errOut)
	}
	mu.Lock()
	if !slices.Equal(asked, []string{"1", "2"}) {
		t.Errorf("devices asked for %v, want [1 2]", asked)
	}
	mu.Unlock()

	// Named, it is the only thing asked for.
	out.Reset()
	errOut.Reset()
	code = monitorMain(ctx, []string{"--ipc-dir", e.Dir, "--raw", "--device", "1", "--duration", "300ms"}, &out, &errOut)
	if code != 1 || !strings.Contains(errOut.String(), "device is not connected") {
		t.Errorf("monitor --raw --device 1 = %d, stderr %q; want 1 and the reason", code, &errOut)
	}
}
