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

// TestMonitorRawIPC (IN-17): "monitor --raw" through the daemon pauses the
// mapping (capture.start), asks for the raw events of the device it names
// (devices.rawInfo) and prints the input.raw events of that device only,
// next to the usual input events.
func TestMonitorRawIPC(t *testing.T) {
	e, err := ipc.Listen(`\\.\pipe\padmatectl-raw-test-`+rand.Text(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	hub := ipc.NewHub()
	var mu sync.Mutex
	var calls []string
	done := make(chan error, 1)
	go func() {
		done <- ipc.Serve(ctx, e, hub, func(*ipc.Session) jrpc2.Assigner {
			note := func(s string) {
				mu.Lock()
				calls = append(calls, s)
				mu.Unlock()
			}
			return handler.Map{
				"capture.start": func(context.Context, *jrpc2.Request) (any, error) {
					note("capture.start")
					return map[string]bool{}, nil
				},
				"devices.list": func(context.Context, *jrpc2.Request) (any, error) {
					note("devices.list")
					return []map[string]any{
						{"info": input.DeviceInfo{ID: 1, Name: "Raw Stick", Type: "unknown"}, "key": "k1"},
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
					note("devices.rawInfo " + p.Device)
					go func() {
						time.Sleep(50 * time.Millisecond)
						hub.Publish("input.raw", map[string]any{"timeNs": 1_000_000, "device": 2, "kind": "button", "index": 1, "value": 1})
						hub.Publish("input.raw", map[string]any{"timeNs": 2_000_000, "device": 1, "kind": "button", "index": 3, "value": 1})
						hub.Publish("input.raw", map[string]any{"timeNs": 3_000_000, "device": 1, "kind": "axis", "index": 0, "value": -32768})
					}()
					return map[string]any{}, nil
				},
			}
		})
	}()
	t.Cleanup(func() { cancel(); <-done })

	var out, errOut bytes.Buffer
	code := monitorMain(ctx, []string{"--ipc-dir", e.Dir, "--raw", "--device", "1", "--duration", "700ms"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("monitor --raw = %d, stderr %q", code, &errOut)
	}
	text := out.String()
	for _, want := range []string{`+device 1 unknown "Raw Stick"`, "1 raw button 3 down", "1 raw axis 0 -32768"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "2 raw button") {
		t.Errorf("the raw event of the other device is printed:\n%s", text)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"capture.start", "devices.list", "devices.rawInfo 1"}; !slices.Equal(calls, want) {
		t.Errorf("daemon calls %v, want %v: the mapping is paused first, and only the named device is asked for", calls, want)
	}
}
