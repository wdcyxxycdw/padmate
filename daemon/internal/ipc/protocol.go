package ipc

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
)

const (
	maxHelloBytes   = 8 << 10
	maxMessageBytes = 8 << 20
	helloTimeout    = 3 * time.Second
	writeTimeout    = 5 * time.Second
)

// lineChannel retains buffered bytes across hello and bounds every allocation.
// Reads have one owner; writes are serialized, including pushed events.
type lineChannel struct {
	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex
}

func newLineChannel(conn net.Conn) *lineChannel {
	return &lineChannel{conn: conn, reader: bufio.NewReaderSize(conn, 4096)}
}

func (c *lineChannel) read(limit int) ([]byte, error) {
	var data []byte
	for {
		part, err := c.reader.ReadSlice('\n')
		if len(data)+len(part) > limit+1 {
			return nil, errors.New("ipc: message exceeds size limit")
		}
		data = append(data, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return data[:len(data)-1], nil
	}
}

func (c *lineChannel) Recv() ([]byte, error) { return c.read(maxMessageBytes) }

func (c *lineChannel) Send(data []byte) error {
	if len(data) > maxMessageBytes {
		return errors.New("ipc: message exceeds size limit")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	frame := make([]byte, len(data)+1)
	copy(frame, data)
	frame[len(data)] = '\n'
	for len(frame) > 0 {
		n, err := c.conn.Write(frame)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}

func (c *lineChannel) Close() error { return c.conn.Close() }

// jrpc2 dispatches received frames in separate goroutines. Its OnNotify calls
// are mutually exclusive, but not ordered. Consume notifications on the one
// channel reader before that dispatch so event streams retain wire order.
// Callbacks must only enqueue bounded data, never call back into the client.
type notificationChannel struct {
	channel.Channel
	notify func(*jrpc2.Request)
}

func (c *notificationChannel) Recv() ([]byte, error) {
	for {
		data, err := c.Channel.Recv()
		if err != nil {
			return nil, err
		}
		requests, parseErr := jrpc2.ParseRequests(data)
		allNotify := parseErr == nil && len(requests) > 0
		for _, r := range requests {
			if r.Error != nil || r.ID != "" || r.Method == "" {
				allNotify = false
				break
			}
		}
		if !allNotify {
			return data, nil
		}
		for _, r := range requests {
			c.notify(r.ToRequest())
		}
	}
}

type helloParams struct {
	Token    string `json:"token"`
	Protocol int    `json:"protocol"`
	Client   string `json:"client"`
}

type helloResult struct {
	Protocol int `json:"protocol"`
}

func checkHello(data []byte, d Discovery) (json.RawMessage, error) {
	requests, err := jrpc2.ParseRequests(data)
	if err != nil || len(requests) != 1 {
		return nil, errors.New("ipc: first request must be hello")
	}
	req := requests[0]
	if req.Error != nil || req.Batch || req.ID == "" || req.Method != "hello" {
		return nil, errors.New("ipc: first request must be hello with an id")
	}
	id := json.RawMessage(req.ID)
	var p helloParams
	if err := req.ToRequest().UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
		return id, errors.New("ipc: invalid hello parameters")
	}
	if p.Protocol != Protocol || p.Protocol != d.Protocol {
		return id, errors.New("ipc: unsupported protocol")
	}
	if len(d.Token) != 64 || subtle.ConstantTimeCompare([]byte(p.Token), []byte(d.Token)) != 1 {
		return id, errors.New("ipc: authentication failed")
	}
	if p.Client == "" || len(p.Client) > 128 {
		return id, errors.New("ipc: invalid client name")
	}
	return id, nil
}

// Authenticate validates and replies to hello before any RPC dispatcher starts.
// The caller owns closing conn on failure; the returned channel owns it on success.
func Authenticate(conn net.Conn, d Discovery) (channel.Channel, error) {
	ch := newLineChannel(conn)
	if err := conn.SetReadDeadline(time.Now().Add(helloTimeout)); err != nil {
		return nil, err
	}
	data, err := ch.read(maxHelloBytes)
	if err != nil {
		return nil, err
	}
	id, authErr := checkHello(data, d)
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	response := struct {
		Version string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  *helloResult    `json:"result,omitempty"`
		Error   *jrpc2.Error    `json:"error,omitempty"`
	}{Version: "2.0", ID: id}
	if authErr != nil {
		response.Error = &jrpc2.Error{Code: -32001, Message: authErr.Error()}
	} else {
		response.Result = &helloResult{Protocol: Protocol}
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	if err := ch.Send(encoded); err != nil {
		return nil, err
	}
	if authErr != nil {
		return nil, authErr
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return nil, err
	}
	return ch, nil
}

// NewClient completes hello before returning a usable authenticated client.
// Callers using pipes must use Dial first to check the server PID.
func NewClient(ctx context.Context, conn net.Conn, d Discovery, name string, opts *jrpc2.ClientOptions) (*jrpc2.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, helloTimeout)
	defer cancel()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	var ch channel.Channel = newLineChannel(conn)
	if opts != nil && opts.OnNotify != nil {
		copyOpts := *opts
		ch = &notificationChannel{Channel: ch, notify: opts.OnNotify}
		copyOpts.OnNotify = nil
		opts = &copyOpts
	}
	client := jrpc2.NewClient(ch, opts)
	var result helloResult
	if err := client.CallResult(ctx, "hello", helloParams{Token: d.Token, Protocol: d.Protocol, Client: name}, &result); err != nil {
		_ = conn.Close()
		_ = client.Close()
		return nil, fmt.Errorf("ipc: hello failed: %w", err)
	}
	if result.Protocol != Protocol {
		_ = client.Close()
		return nil, errors.New("ipc: unsupported server protocol")
	}
	return client, nil
}
