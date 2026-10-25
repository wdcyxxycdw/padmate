package ipc

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

// subscribedPipe serves one connection over net.Pipe, whose writes return
// only once the other end has read them, and subscribes it to topics with
// a raw client that reads only when the test does.
func subscribedPipe(t *testing.T, hub *Hub, topics ...string) *lineChannel {
	t.Helper()
	a, b := net.Pipe()
	d := Discovery{Protocol: Protocol, Token: strings.Repeat("a", 64)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeConn(ctx, b, d, hub, func(*Session) jrpc2.Assigner { return handler.Map{} }) }()
	t.Cleanup(func() {
		cancel()
		_ = a.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("connection did not stop")
		}
	})
	ch := newLineChannel(a)
	hello, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "hello", "params": helloParams{Token: d.Token, Protocol: Protocol, Client: "sync"}})
	if err := ch.Send(hello); err != nil {
		t.Fatal(err)
	}
	if _, err := ch.Recv(); err != nil {
		t.Fatal(err)
	}
	sub, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "subscribe", "params": map[string]any{"topics": topics}})
	if err := ch.Send(sub); err != nil {
		t.Fatal(err)
	}
	if reply, err := ch.Recv(); err != nil || !strings.Contains(string(reply), `"result"`) {
		t.Fatalf("subscribe %v: %s %v", topics, reply, err)
	}
	return ch
}

// TestPublishSyncReturnsOnceWritten: PublishSync waits for the sessions
// that subscribe to the topic, until each has written the event to its
// connection, and not for the others (DMN-22).
func TestPublishSyncReturnsOnceWritten(t *testing.T) {
	hub := NewHub()
	reader := subscribedPipe(t, hub, "daemon.exiting")
	subscribedPipe(t, hub, "input") // never reads: not waited for
	returned := make(chan []*Session, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		returned <- hub.PublishSync(ctx, "daemon.exiting", map[string]string{"reason": "tray"})
	}()
	select {
	case n := <-returned:
		t.Fatalf("PublishSync returned %v before the client read the event", n)
	case <-time.After(50 * time.Millisecond):
	}
	data, err := reader.Recv()
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Method string `json:"method"`
		Params struct {
			Reason string `json:"reason"`
		} `json:"params"`
	}
	if err := json.Unmarshal(data, &got); err != nil || got.Method != "daemon.exiting" || got.Params.Reason != "tray" {
		t.Fatalf("notification %s: %v", data, err)
	}
	select {
	case written := <-returned:
		// The session that wrote it: the subscriber of daemon.exiting.
		if len(written) != 1 || !(*written[0].topics.Load())["daemon.exiting"] {
			t.Fatalf("PublishSync = %v, want the subscriber", written)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PublishSync did not return after the write")
	}
}

// TestPublishSyncBounded: a client that does not read holds PublishSync
// only until its context ends; without subscribers, or with a full queue,
// it returns at once.
func TestPublishSyncBounded(t *testing.T) {
	hub := NewHub()
	if n := len(hub.PublishSync(context.Background(), "daemon.exiting", nil)); n != 0 {
		t.Fatalf("no subscribers: %d", n)
	}
	subscribedPipe(t, hub, "daemon.exiting")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if n := len(hub.PublishSync(ctx, "daemon.exiting", map[string]string{"reason": "tray"})); n != 0 {
		t.Fatalf("unread event counted as written: %d", n)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("PublishSync took %v past its context", d)
	}

	full := newSession(NewHub(), 1)
	defer full.close()
	full.setTopics([]string{"daemon.exiting"})
	full.hub.Publish("daemon.exiting", 1)
	start = time.Now()
	if n := len(full.hub.PublishSync(context.Background(), "daemon.exiting", 2)); n != 0 || time.Since(start) > time.Second {
		t.Fatalf("full queue: %d after %v", n, time.Since(start))
	}
	if full.dropped.Load() != 1 {
		t.Fatalf("dropped = %d, want 1", full.dropped.Load())
	}
}

// TestPublishSyncEndsWithSession: a session that closes with the event
// still queued ends the wait, and Done tells it.
func TestPublishSyncEndsWithSession(t *testing.T) {
	s := newSession(NewHub(), 4)
	s.setTopics([]string{"daemon.exiting"})
	returned := make(chan []*Session, 1)
	go func() { returned <- s.hub.PublishSync(context.Background(), "daemon.exiting", 1) }()
	time.Sleep(20 * time.Millisecond)
	select {
	case <-s.Done():
		t.Fatal("Done before the session closed")
	default:
	}
	s.close()
	select {
	case n := <-returned:
		if len(n) != 0 {
			t.Fatalf("PublishSync = %v, want none", n)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PublishSync outlived the session")
	}
	select {
	case <-s.Done():
	default:
		t.Fatal("Done open after the session closed")
	}
}
