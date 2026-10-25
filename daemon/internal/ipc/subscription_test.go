package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

func TestSubscriptionCaptureCancellationAndEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a, b := net.Pipe()
	hub := NewHub()
	d := Discovery{Protocol: Protocol, Token: strings.Repeat("a", 64)}
	released := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- ServeConn(ctx, a, d, hub, func(s *Session) jrpc2.Assigner {
			return handler.Map{"capture.start": func(context.Context, *jrpc2.Request) (any, error) {
				s.OnClose(func() { close(released) })
				return true, nil
			}}
		})
	}()
	t.Cleanup(func() { cancel(); _ = b.Close(); <-done })
	sub, err := subscribeConn(ctx, b, d, time.Second, []string{"input"}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	hub.Publish("input", map[string]string{"control": "south"})
	n, err := sub.Next(ctx)
	if err != nil || n.Topic != "input" || !strings.Contains(string(n.Data), "south") {
		t.Fatal(n, err)
	}
	// Cancellation closes the pipe even if the caller never reads again.
	cancel()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("capture lease survived client cancellation")
	}
}

func TestSubscriptionLocalOverflowIsVisible(t *testing.T) {
	s := &Subscription{events: make(chan Notification, 1), done: make(chan error, 1), overflow: make(chan struct{})}
	s.notify("input", []byte(`{}`))
	s.notify("input", []byte(`{}`))
	_, err := s.Next(context.Background())
	if !errors.Is(err, ErrEventOverflow) {
		t.Fatal(err)
	}
}

func TestSubscriptionReportsClosedDaemon(t *testing.T) {
	s := &Subscription{events: make(chan Notification, 1), done: make(chan error, 1), overflow: make(chan struct{})}
	s.done <- nil
	_, err := s.Next(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func TestSubscriptionPreservesWireOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a, b := net.Pipe()
	hub := NewHub()
	d := Discovery{Protocol: Protocol, Token: strings.Repeat("b", 64)}
	done := make(chan error, 1)
	go func() { done <- ServeConn(ctx, a, d, hub, func(*Session) jrpc2.Assigner { return handler.Map{} }) }()
	t.Cleanup(func() { cancel(); _ = b.Close(); <-done })
	sub, err := subscribeConn(ctx, b, d, time.Second, []string{"input"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	for i := 0; i < 64; i++ {
		hub.Publish("input", map[string]int{"n": i})
	}
	for i := 0; i < 64; i++ {
		n, err := sub.Next(ctx)
		var p struct {
			N int `json:"n"`
		}
		if err != nil || json.Unmarshal(n.Data, &p) != nil || n.Topic != "input" || p.N != i {
			t.Fatalf("event %d: %+v, %v", i, n, err)
		}
	}
}
