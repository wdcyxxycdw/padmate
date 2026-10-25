package ipc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func currentSID() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

func pipeSDDL(sid string) string { return "D:P(D;;GA;;;NU)(A;;GA;;;" + sid + ")" }
func fileSDDL(sid string) string { return "D:P(A;;FA;;;" + sid + ")" }

// Listen creates a current-user-only local pipe and then publishes discovery.
// go-winio uses FILE_CREATE for the first instance and rejects remote clients.
func Listen(pipe, dir string) (*Endpoint, error) {
	sid, err := currentSID()
	if err != nil {
		return nil, err
	}
	if pipe == "" {
		pipe = os.Getenv("PADMATE_PIPE")
	}
	if pipe == "" {
		pipe = `\\.\pipe\padmate-` + sid
	}
	if !validPipe(pipe) {
		return nil, errors.New("ipc: pipe must be a local named pipe")
	}
	dir, err = discoveryDir(dir)
	if err != nil {
		return nil, err
	}
	listener, err := winio.ListenPipe(pipe, &winio.PipeConfig{SecurityDescriptor: pipeSDDL(sid), InputBufferSize: 65536, OutputBufferSize: 65536})
	if err != nil {
		return nil, err
	}
	var token [32]byte
	if _, err = rand.Read(token[:]); err != nil {
		_ = listener.Close()
		return nil, err
	}
	e := &Endpoint{Listener: listener, Dir: dir, Discovery: Discovery{Pipe: pipe, PID: uint32(os.Getpid()), Protocol: Protocol, Token: hex.EncodeToString(token[:])}}
	if err := writeDiscovery(dir, e.Discovery); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return e, nil
}

// Dial verifies the server PID before the caller can send the discovery token.
func Dial(ctx context.Context, dir string) (net.Conn, Discovery, error) {
	d, err := ReadDiscovery(dir)
	if err != nil {
		return nil, Discovery{}, err
	}
	conn, err := winio.DialPipeContext(ctx, d.Pipe)
	if err != nil {
		return nil, Discovery{}, err
	}
	fd, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		_ = conn.Close()
		return nil, Discovery{}, errors.New("ipc: pipe handle unavailable")
	}
	var pid uint32
	err = windows.GetNamedPipeServerProcessId(windows.Handle(fd.Fd()), &pid)
	if err != nil || pid != d.PID {
		_ = conn.Close()
		if err != nil {
			return nil, Discovery{}, fmt.Errorf("ipc: cannot verify server process: %w", err)
		}
		return nil, Discovery{}, errors.New("ipc: pipe server PID differs from discovery")
	}
	return conn, d, nil
}

func clientProcessID(conn net.Conn) uint32 {
	fd, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return 0
	}
	var pid uint32
	if err := windows.GetNamedPipeClientProcessId(windows.Handle(fd.Fd()), &pid); err != nil {
		return 0
	}
	return pid
}

func writePrivate(path string, data []byte) error {
	sid, err := currentSID()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(fileSDDL(sid))
	if err != nil {
		return err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_WRITE, windows.FILE_SHARE_READ, &sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(h), path)
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Close()
}
