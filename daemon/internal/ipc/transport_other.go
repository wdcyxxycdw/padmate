//go:build !windows

package ipc

import (
	"context"
	"errors"
	"net"
	"os"
)

func Listen(string, string) (*Endpoint, error) { return nil, errors.ErrUnsupported }
func Dial(context.Context, string) (net.Conn, Discovery, error) {
	return nil, Discovery{}, errors.ErrUnsupported
}
func writePrivate(path string, data []byte) error { return os.WriteFile(path, data, 0o600) }
func clientProcessID(net.Conn) uint32             { return 0 }
