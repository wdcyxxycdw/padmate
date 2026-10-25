package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

func TestSessionSubscriptionAndDisconnect(t *testing.T) {
	a, b := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	d := Discovery{Protocol: Protocol, Token: strings.Repeat("d", 64)}
	hub := NewHub()
	closed := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- ServeConn(ctx, b, d, hub, func(s *Session) jrpc2.Assigner {
			s.OnClose(func() { close(closed) })
			return handler.Map{"ping": func(context.Context, *jrpc2.Request) (any, error) { return "pong", nil }}
		})
	}()
	events := make(chan string, 4)
	client, err := NewClient(ctx, a, d, "test", &jrpc2.ClientOptions{OnNotify: func(r *jrpc2.Request) { events <- r.Method() }})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.CallResult(ctx, "subscribe", map[string]any{"topics": []string{"input"}}, new(any)); err != nil {
		t.Fatal(err)
	}
	hub.Publish("input", map[string]bool{"pressed": true})
	select {
	case topic := <-events:
		if topic != "input" {
			t.Fatal(topic)
		}
	case <-ctx.Done():
		t.Fatal("no event")
	}
	if err := client.CallResult(ctx, "subscribe", map[string]any{"topics": []string{"bad"}}, new(any)); err == nil {
		t.Fatal("unknown topic accepted")
	}
	_ = client.Close()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("disconnect cleanup missing")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("server still running")
	}
}

func TestPipelinedNotificationsAndBatchesStayOrdered(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	d := Discovery{Protocol: Protocol, Token: strings.Repeat("f", 64)}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		var count int
		done <- ServeConn(ctx, b, d, NewHub(), func(*Session) jrpc2.Assigner {
			return handler.Map{
				"add": func(context.Context, *jrpc2.Request) (any, error) { count++; return count, nil },
				"get": func(context.Context, *jrpc2.Request) (any, error) { return count, nil },
			}
		})
	}()
	ch := newLineChannel(a)
	hello := fmt.Sprintf(`{"jsonrpc":"2.0","id":0,"method":"hello","params":{"token":%q,"protocol":1,"client":"test"}}`, d.Token)
	if err := ch.Send([]byte(hello)); err != nil {
		t.Fatal(err)
	}
	if _, err := ch.Recv(); err != nil {
		t.Fatal(err)
	}
	wrote := make(chan error, 1)
	go func() {
		frames := []string{
			`{"jsonrpc":"2.0","method":"add"}`,
			`[{"jsonrpc":"2.0","method":"add"},{"jsonrpc":"2.0","method":"add"}]`,
			`{"jsonrpc":"2.0","id":1,"method":"get"}`,
			`[]`,
			`{"jsonrpc":"2.0","method":"unknown"}`,
			`{"jsonrpc":"2.0","id":2,"method":"get"}`,
		}
		for _, frame := range frames {
			if err := ch.Send([]byte(frame)); err != nil {
				wrote <- err
				return
			}
		}
		wrote <- nil
	}()
	for i := 0; i < 3; i++ {
		data, err := ch.Recv()
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Result int          `json:"result"`
			Error  *jrpc2.Error `json:"error"`
		}
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if got.Error == nil {
				t.Fatalf("empty batch accepted: %s", data)
			}
		} else if got.Result != 3 || got.Error != nil {
			t.Fatalf("out of order: %s", data)
		}
	}
	if err := <-wrote; err != nil {
		t.Fatal(err)
	}
	_ = a.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestSlowClientReceivesDroppedNotification(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	d := Discovery{Protocol: Protocol, Token: strings.Repeat("e", 64)}
	hub := NewHub()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ServeConn(ctx, b, d, hub, func(*Session) jrpc2.Assigner { return handler.Map{} }) }()
	ch := newLineChannel(a)
	hello, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "hello", "params": helloParams{Token: d.Token, Protocol: Protocol, Client: "slow"}})
	if err := ch.Send(hello); err != nil {
		t.Fatal(err)
	}
	if _, err := ch.Recv(); err != nil {
		t.Fatal(err)
	}
	if err := ch.Send([]byte(`{"jsonrpc":"2.0","id":2,"method":"subscribe","params":{"topics":["input"]}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := ch.Recv(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10000; i++ {
		hub.Publish("input", i)
	}
	found := false
	for i := 0; i < 3; i++ {
		data, err := ch.Recv()
		if err != nil {
			t.Fatal(err)
		}
		var notification struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(data, &notification); err != nil {
			t.Fatal(err)
		}
		if notification.Method == "dropped" {
			var count struct {
				N uint64 `json:"n"`
			}
			if err := json.Unmarshal(notification.Params, &count); err != nil {
				t.Fatal(err)
			}
			if count.N > 0 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("missing dropped count")
	}
	_ = a.Close()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestSlowSubscriptionDropsWithoutBlockingPublisher(t *testing.T) {
	s := newSession(NewHub(), 2)
	s.setTopics([]string{"input"})
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			s.hub.Publish("input", i)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publisher blocked")
	}
	if len(s.events) != 2 || s.dropped.Load() != 9998 {
		t.Fatalf("events=%d dropped=%d", len(s.events), s.dropped.Load())
	}
	s.close()
}

func TestSessionCloseInterruptsBlockedEventWrite(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	d := Discovery{Protocol: Protocol, Token: strings.Repeat("e", 64)}
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ServeConn(ctx, b, d, hub, func(*Session) jrpc2.Assigner { return handler.Map{} }) }()
	ch := newLineChannel(a)
	hello, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "hello", "params": helloParams{Token: d.Token, Protocol: Protocol, Client: "slow"}})
	if err := ch.Send(hello); err != nil {
		t.Fatal(err)
	}
	if _, err := ch.Recv(); err != nil {
		t.Fatal(err)
	}
	if err := ch.Send([]byte(`{"jsonrpc":"2.0","id":2,"method":"subscribe","params":{"topics":["input"]}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := ch.Recv(); err != nil {
		t.Fatal(err)
	}
	hub.Publish("input", 1)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked behind notification write")
	}
}
