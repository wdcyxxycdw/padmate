package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
)

var ErrEventOverflow = errors.New("ipc: client event queue overflow; stream is incomplete")

type Notification struct {
	Topic string          `json:"topic"`
	Data  json.RawMessage `json:"data"`
}

// Subscription owns one authenticated connection. Callback delivery never
// waits for the consumer. Local overflow is fatal, so recordings cannot silently
// lose events. Server-side drops remain explicit "dropped" notifications.
type Subscription struct {
	client       *jrpc2.Client
	conn         net.Conn
	events       chan Notification
	done         chan error
	overflow     chan struct{}
	overflowOnce sync.Once
	stop         func() bool
}

func Subscribe(ctx context.Context, dir string, timeout time.Duration, topics []string, capture bool) (*Subscription, error) {
	setup, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, d, err := Dial(setup, dir)
	if err != nil {
		return nil, err
	}
	return subscribeConn(ctx, conn, d, timeout, topics, capture)
}

func subscribeConn(ctx context.Context, conn net.Conn, d Discovery, timeout time.Duration, topics []string, capture bool) (*Subscription, error) {
	s := &Subscription{conn: conn, events: make(chan Notification, eventQueueSize), done: make(chan error, 1), overflow: make(chan struct{})}
	s.stop = context.AfterFunc(ctx, func() { _ = conn.Close() })
	setup, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client, err := NewClient(setup, conn, d, "padmatectl", &jrpc2.ClientOptions{
		OnNotify: func(r *jrpc2.Request) { s.notify(r.Method(), []byte(r.ParamString())) },
		OnStop:   func(_ *jrpc2.Client, err error) { s.done <- err },
	})
	s.client = client
	if err == nil {
		err = client.CallResult(setup, "subscribe", map[string]any{"topics": topics}, new(any))
	}
	if err == nil && capture {
		err = client.CallResult(setup, "capture.start", nil, new(any))
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Subscription) notify(topic string, data json.RawMessage) {
	select {
	case s.events <- Notification{Topic: topic, Data: data}:
	default:
		s.overflowOnce.Do(func() {
			close(s.overflow)
			if s.conn != nil {
				_ = s.conn.Close()
			}
		})
	}
}

func (s *Subscription) Next(ctx context.Context) (Notification, error) {
	select {
	case <-s.overflow:
		return Notification{}, ErrEventOverflow
	default:
	}
	select {
	case <-ctx.Done():
		return Notification{}, ctx.Err()
	case <-s.overflow:
		return Notification{}, ErrEventOverflow
	case n := <-s.events:
		return n, nil
	case err := <-s.done:
		if err == nil {
			err = io.EOF
		}
		return Notification{}, err
	}
}

// CallResult allows a subscriber to query an initial snapshot on the same
// connection after subscribing, without opening a second capture owner.
func (s *Subscription) CallResult(ctx context.Context, method string, params, result any) error {
	return s.client.CallResult(ctx, method, params, result)
}

func (s *Subscription) Close() {
	if s.stop != nil {
		s.stop()
	}
	if s.conn != nil {
		_ = s.conn.Close()
	}
	if s.client != nil {
		_ = s.client.Close()
	}
}
