package ipc

import (
	"bufio"
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

func TestHelloRequiredBeforeRPC(t *testing.T) {
	for _, request := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"status.get"}`,
		`{"jsonrpc":"2.0","id":1,"method":"hello","params":{"protocol":1,"client":"test"}}`,
		`{"jsonrpc":"2.0","id":1,"method":"hello","params":{"protocol":1,"token":"wrong","client":"test"}}`,
		`{"jsonrpc":"2.0","id":1,"method":"hello","params":{"protocol":2,"token":"` + strings.Repeat("a", 64) + `","client":"test"}}`,
		`{"jsonrpc":"2.0","id":1,"method":"hello","params":{"protocol":2,"token":"wrong","client":"test"}}`,
		`[{"jsonrpc":"2.0","id":1,"method":"hello"}]`,
		`{"jsonrpc":"2.0","method":"hello"}`,
	} {
		t.Run(request, func(t *testing.T) {
			a, b := net.Pipe()
			defer a.Close()
			result := make(chan error, 1)
			go func() {
				_, err := Authenticate(b, Discovery{Token: strings.Repeat("a", 64), Protocol: Protocol})
				result <- err
				_ = b.Close()
			}()
			_ = a.SetDeadline(time.Now().Add(time.Second))
			if _, err := a.Write([]byte(request + "\n")); err != nil {
				t.Fatal(err)
			}
			line, err := bufio.NewReader(a).ReadBytes('\n')
			if err != nil {
				t.Fatal(err)
			}
			var response struct {
				Error *jrpc2.Error `json:"error"`
			}
			if err := json.Unmarshal(line, &response); err != nil || response.Error == nil {
				t.Fatalf("bad handshake response: %s, %v", line, err)
			}
			if err := <-result; err == nil {
				t.Fatal("invalid hello accepted")
			}
		})
	}
}

func TestAuthenticatedClient(t *testing.T) {
	a, b := net.Pipe()
	d := Discovery{Protocol: Protocol, Token: strings.Repeat("b", 64)}
	done := make(chan error, 1)
	go func() {
		defer b.Close()
		ch, err := Authenticate(b, d)
		if err != nil {
			done <- err
			return
		}
		s := jrpc2.NewServer(handler.Map{"status.get": func(context.Context, *jrpc2.Request) (any, error) { return map[string]bool{"dryRun": true}, nil }}, &jrpc2.ServerOptions{Concurrency: 1, DisableBuiltin: true}).Start(ch)
		done <- s.Wait()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := NewClient(ctx, a, d, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		DryRun bool `json:"dryRun"`
	}
	if err := client.CallResult(ctx, "status.get", nil, &status); err != nil || !status.DryRun {
		t.Fatalf("status: %+v %v", status, err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("RPC did not close")
	}
}

func TestHelloPreservesPipelinedFrame(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	d := Discovery{Protocol: Protocol, Token: strings.Repeat("c", 64)}
	hello := `{"jsonrpc":"2.0","id":1,"method":"hello","params":{"protocol":1,"client":"pipeline","token":"` + d.Token + `"}}`
	next := `{"jsonrpc":"2.0","id":2,"method":"status.get"}`
	done := make(chan error, 1)
	go func() {
		ch, err := Authenticate(b, d)
		if err != nil {
			done <- err
			return
		}
		data, err := ch.Recv()
		if err == nil && string(data) != next {
			err = fmt.Errorf("lost buffered request: %q", data)
		}
		done <- err
	}()
	_ = a.SetDeadline(time.Now().Add(time.Second))
	if _, err := a.Write([]byte(hello + "\n" + next + "\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(a).ReadBytes('\n'); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func FuzzHello(f *testing.F) {
	d := Discovery{Protocol: Protocol, Token: strings.Repeat("a", 64)}
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"method":"hello","params":{"token":"` + d.Token + `","protocol":1,"client":"fuzz"}}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"method":"hello"}`))
	f.Add([]byte(`[]`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxHelloBytes {
			return
		}
		id, err := checkHello(data, d)
		if err == nil && !json.Valid(id) {
			t.Fatal("accepted hello with invalid id")
		}
	})
}

func TestClientCancellationInterruptsBlockedHelloWrite(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		client, err := NewClient(ctx, a, Discovery{Protocol: Protocol, Token: strings.Repeat("a", 64)}, "cancel", nil)
		if client != nil {
			_ = client.Close()
		}
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled hello accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked hello ignored cancellation")
	}
}

func TestHelloAndMessageSizeBounds(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	ch := newLineChannel(b)
	go func() { _, _ = a.Write([]byte(strings.Repeat("x", maxHelloBytes+1) + "\n")) }()
	if _, err := ch.read(maxHelloBytes); err == nil {
		t.Fatal("oversized hello accepted")
	}
}
