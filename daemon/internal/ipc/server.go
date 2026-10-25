package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
)

const eventQueueSize = 128
const maxClients = 16
const maxBatchRequests = 64

// Topics are the subscription topics. daemon.exiting is published, with
// PublishSync, right before the daemon stops at the user's request (DMN-22,
// ADR-0056). hud carries the scripts' toasts (SCR-22); a client subscribing
// to it is an overlay, so scripts see host.has("hud") true. input.raw carries
// the raw joystick events of the controllers that devices.rawInfo asked for
// (IN-17, ADR-0072); the mapping wizard subscribes to it while it is open.
var Topics = []string{"input", "engine.activity", "log", "script.status", "profile.changed", "output.preview", "app.foreground", "daemon.exiting", "hud", "input.raw"}

type event struct {
	topic string
	data  json.RawMessage
	// sent, for PublishSync only, receives whether the event was written to
	// the connection; it has room for that one value.
	sent chan bool
}

// done reports the outcome of writing e to PublishSync, if it waits.
func (e event) done(written bool) {
	if e.sent != nil {
		e.sent <- written
	}
}

type topicSet map[string]bool

// Hub publishes immutable event data without waiting for a client or disk I/O.
// Session list and subscriptions are copy-on-write, off the mapping path.
type Hub struct {
	mu      sync.Mutex
	clients atomic.Pointer[[]*Session]
}

func NewHub() *Hub { h := new(Hub); h.clients.Store(&[]*Session{}); return h }

func (h *Hub) add(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	clients := append(slices.Clone(*h.clients.Load()), s)
	h.clients.Store(&clients)
}

func (h *Hub) remove(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	clients := slices.DeleteFunc(slices.Clone(*h.clients.Load()), func(c *Session) bool { return c == s })
	h.clients.Store(&clients)
}

// Listening lets mapping callbacks avoid building events when no client wants
// them. Subscriptions can change immediately after this advisory snapshot.
func (h *Hub) Listening(topic string) bool {
	if h == nil {
		return false
	}
	for _, s := range *h.clients.Load() {
		if (*s.topics.Load())[topic] {
			return true
		}
	}
	return false
}

// Publish encodes data only when at least one client subscribes to the topic.
// It must receive a stable snapshot, never a concurrently mutated map or slice.
func (h *Hub) Publish(topic string, data any) { h.publish(topic, data, false, nil) }

// PublishTo publishes data as Publish does, to the subscribers of topic that
// accept returns true for. accept runs for each of them, on the caller's
// goroutine, so it must be quick and must not call the Hub. The raw joystick
// events go only to the connections that asked for the device (input.raw).
func (h *Hub) PublishTo(topic string, data any, accept func(*Session) bool) {
	h.publish(topic, data, false, accept)
}

// PublishSync publishes data as Publish does, then waits until every
// session that subscribes to topic has written it to its connection, has
// dropped it (its queue was full) or has closed, or until ctx is done. It
// returns the sessions that wrote it, which a tray exit waits for (DMN-22).
// A write that returned has put the line in the connection's buffer: the
// client still reads it after the daemon closes its end. Never call it on
// the mapping path.
func (h *Hub) PublishSync(ctx context.Context, topic string, data any) (written []*Session) {
	for _, w := range h.publish(topic, data, true, nil) {
		select {
		case ok := <-w.sent:
			if ok {
				written = append(written, w.session)
			}
		case <-w.session.done:
		case <-ctx.Done():
			return written
		}
	}
	return written
}

// pending is an event PublishSync waits for.
type pending struct {
	session *Session
	sent    chan bool
}

func (h *Hub) publish(topic string, data any, sync bool, accept func(*Session) bool) (waits []pending) {
	if h == nil {
		return nil
	}
	clients := h.clients.Load()
	var encoded json.RawMessage
	for _, s := range *clients {
		if !(*s.topics.Load())[topic] || accept != nil && !accept(s) {
			continue
		}
		if encoded == nil {
			var err error
			encoded, err = json.Marshal(data)
			if err != nil || len(encoded) > maxMessageBytes/2 {
				return nil
			}
		}
		e := event{topic: topic, data: encoded}
		if sync {
			e.sent = make(chan bool, 1)
		}
		select {
		case s.events <- e:
			if sync {
				waits = append(waits, pending{session: s, sent: e.sent})
			}
		default:
			s.dropped.Add(1)
		}
	}
	return waits
}

// Session owns a connection's subscriptions and resources (such as capture).
type Session struct {
	// PeerPID is the OS-verified local pipe client; zero for other transports.
	PeerPID  uint32
	hub      *Hub
	topics   atomic.Pointer[topicSet]
	events   chan event
	dropped  atomic.Uint64
	mu       sync.Mutex
	cleanups []func()
	closed   bool
	done     chan struct{}
}

func newSession(h *Hub, capacity int) *Session {
	s := &Session{hub: h, events: make(chan event, capacity), done: make(chan struct{})}
	s.setTopics(nil)
	h.add(s)
	return s
}

func (s *Session) setTopics(topics []string) {
	set := make(topicSet, len(topics))
	for _, t := range topics {
		set[t] = true
	}
	s.topics.Store(&set)
}

// Done is closed once the session has closed: its connection is gone.
func (s *Session) Done() <-chan struct{} { return s.done }

// OnClose registers cleanup after all request handlers have returned.
func (s *Session) OnClose(f func()) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		f()
		return
	}
	s.cleanups = append(s.cleanups, f)
	s.mu.Unlock()
}

func (s *Session) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	cleanups := s.cleanups
	s.cleanups = nil
	close(s.done)
	s.mu.Unlock()
	s.hub.remove(s)
	for _, f := range cleanups {
		f()
	}
}

func (s *Session) subscribe(_ context.Context, r *jrpc2.Request) (any, error) {
	var p struct {
		Topics []string `json:"topics"`
	}
	if err := r.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return nil, err
	}
	if len(p.Topics) > len(Topics) {
		return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "too many topics"}
	}
	for _, topic := range p.Topics {
		if !slices.Contains(Topics, topic) {
			return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "unknown subscription topic"}
		}
	}
	s.setTopics(p.Topics)
	return map[string]any{"topics": p.Topics}, nil
}

func (s *Session) sendEvents(ctx context.Context, server *jrpc2.Server) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case e := <-s.events:
			if n := s.dropped.Swap(0); n > 0 {
				if err := server.Notify(ctx, "dropped", map[string]uint64{"n": n}); err != nil {
					e.done(false)
					return
				}
			}
			err := server.Notify(ctx, e.topic, e.data)
			e.done(err == nil)
			if err != nil {
				return
			}
		}
	}
}

// serialChannel bounds the library's otherwise unbounded request queue to one
// frame (plus one buffered read). Batches are bounded; notifications complete
// in handlers, while requests complete when the frame's response is sent.
type serialChannel struct {
	channel.Channel
	next          chan struct{}
	done          chan struct{}
	once          sync.Once
	notifications atomic.Int64
}

func newSerialChannel(ch channel.Channel) *serialChannel {
	c := &serialChannel{Channel: ch, next: make(chan struct{}, 1), done: make(chan struct{})}
	c.release()
	return c
}

func (c *serialChannel) release() {
	select {
	case c.next <- struct{}{}:
	default:
	}
}
func (c *serialChannel) notificationDone() {
	if c.notifications.Load() > 0 && c.notifications.Add(-1) == 0 {
		c.release()
	}
}

func (c *serialChannel) Recv() ([]byte, error) {
	data, err := c.Channel.Recv()
	if err != nil {
		return nil, err
	}
	select {
	case <-c.done:
		return nil, io.EOF
	case <-c.next:
	}
	requests, err := jrpc2.ParseRequests(data)
	if len(requests) > maxBatchRequests {
		return nil, errors.New("ipc: request batch exceeds limit")
	}
	allNotify := err == nil && len(requests) > 0
	for _, req := range requests {
		if req.Error != nil || req.ID != "" || req.Method == "" {
			allNotify = false
			break
		}
	}
	if allNotify {
		c.notifications.Store(int64(len(requests)))
	} else {
		c.notifications.Store(0)
	}
	return data, nil
}

func (c *serialChannel) Send(data []byte) error {
	err := c.Channel.Send(data)
	var header struct {
		Method string `json:"method"`
	}
	// Arrays are batch responses. An object without method is a response too.
	if json.Unmarshal(data, &header) != nil || header.Method == "" {
		c.release()
	}
	return err
}

func (c *serialChannel) Close() error { c.once.Do(func() { close(c.done) }); return c.Channel.Close() }

type sessionAssigner struct {
	session *Session
	channel *serialChannel
	methods jrpc2.Assigner
}

func (a sessionAssigner) Assign(ctx context.Context, method string) jrpc2.Handler {
	h := a.methods.Assign(ctx, method)
	if method == "subscribe" {
		h = a.session.subscribe
	}
	if h == nil {
		h = func(context.Context, *jrpc2.Request) (any, error) {
			return nil, &jrpc2.Error{Code: jrpc2.MethodNotFound, Message: "unknown method"}
		}
	}
	return func(ctx context.Context, r *jrpc2.Request) (any, error) {
		if r.IsNotification() {
			defer a.channel.notificationDone()
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return h(ctx, r)
	}
}

// ServeConn authenticates a connection, runs RPC and guarantees disconnect
// cleanup. Close the connection before stopping jrpc2: Notify holds its mutex
// while writing, so a slow reader must not hold shutdown behind that mutex.
func ServeConn(ctx context.Context, conn net.Conn, d Discovery, hub *Hub, methods func(*Session) jrpc2.Assigner) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	defer conn.Close()
	ch, err := Authenticate(conn, d)
	if err != nil {
		return err
	}
	serial := newSerialChannel(ch)
	s := newSession(hub, eventQueueSize)
	s.PeerPID = clientProcessID(conn)
	defer s.close()
	server := jrpc2.NewServer(sessionAssigner{session: s, channel: serial, methods: methods(s)}, &jrpc2.ServerOptions{AllowPush: true, DisableBuiltin: true, Concurrency: 1, NewContext: func() context.Context { return ctx }}).Start(serial)
	senderDone := make(chan struct{})
	go func() { defer close(senderDone); defer conn.Close(); s.sendEvents(ctx, server) }()
	err = server.Wait()
	canceled := ctx.Err() != nil
	_ = conn.Close()
	cancel()
	<-senderDone
	if canceled || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// Serve accepts a bounded number of clients, including incomplete hellos.
// Endpoint is closed on cancellation; callers also close it on startup error.
func Serve(ctx context.Context, e *Endpoint, hub *Hub, methods func(*Session) jrpc2.Assigner) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopClose := context.AfterFunc(ctx, func() { _ = e.Close() })
	defer stopClose()
	defer e.Close()
	var workers sync.WaitGroup
	defer workers.Wait()
	slots := make(chan struct{}, maxClients)
	for {
		conn, err := e.Accept()
		if err != nil {
			canceled := ctx.Err() != nil
			cancel()
			if canceled || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		select {
		case slots <- struct{}{}:
		default:
			_ = conn.Close()
			continue
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer func() { <-slots }()
			_ = ServeConn(ctx, conn, e.Discovery, hub, methods)
		}()
	}
}
