package ipc

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

var testPipes atomic.Uint64

func testEndpoint(t *testing.T) *Endpoint {
	t.Helper()
	e, err := Listen(fmt.Sprintf(`\\.\pipe\padmate-ipc-test-%d-%d`, os.Getpid(), testPipes.Add(1)), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func TestPipeSecurityDiscoveryAndFirstInstance(t *testing.T) {
	e := testEndpoint(t)
	if second, err := Listen(e.Discovery.Pipe, t.TempDir()); err == nil {
		_ = second.Close()
		t.Fatal("second pipe instance succeeded")
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := e.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, got, err := Dial(ctx, e.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if got != e.Discovery {
		t.Fatal("discovery changed")
	}
	var server net.Conn
	select {
	case server = <-accepted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer server.Close()
	h := windows.Handle(client.(interface{ Fd() uintptr }).Fd())
	sd, err := windows.GetSecurityInfo(h, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := currentSID()
	if err != nil {
		t.Fatal(err)
	}
	// The kernel maps GENERIC_ALL to FILE_ALL_ACCESS on pipe creation.
	if got, want := sd.String(), strings.ReplaceAll(pipeSDDL(sid), "GA", "FA"); got != want {
		t.Fatalf("pipe DACL=%s, want %s", got, want)
	}
	fsd, err := windows.GetNamedSecurityInfo(filepath.Join(e.Dir, "daemon.json"), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fsd.String(), fileSDDL(sid); got != want {
		t.Fatalf("file DACL=%s, want %s", got, want)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.Dir, "daemon.json")); !os.IsNotExist(err) {
		t.Fatalf("discovery not removed: %v", err)
	}
}

func TestClientRejectsWrongPipePIDBeforeHello(t *testing.T) {
	e := testEndpoint(t)
	fake := e.Discovery
	fake.PID++
	if err := writeDiscovery(e.Dir, fake); err != nil {
		t.Fatal(err)
	}
	read := make(chan int, 1)
	go func() {
		c, err := e.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		var b [1]byte
		n, _ := c.Read(b[:])
		read <- n
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if c, _, err := Dial(ctx, e.Dir); err == nil {
		_ = c.Close()
		t.Fatal("spoofed server PID accepted")
	}
	select {
	case n := <-read:
		if n != 0 {
			t.Fatal("client sent bytes before PID verification")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
