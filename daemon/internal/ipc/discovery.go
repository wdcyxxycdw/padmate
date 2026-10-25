// Package ipc implements the local authenticated padmate control protocol.
package ipc

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const Protocol = 1
const maxDiscoveryBytes = 8192

// Discovery is the small, private daemon.json document. Never log its token.
type Discovery struct {
	Pipe     string `json:"pipe"`
	PID      uint32 `json:"pid"`
	Protocol int    `json:"protocol"`
	Token    string `json:"token"`
}

func validPipe(name string) bool {
	const prefix = `\\.\pipe\`
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(name, prefix)
	return suffix != "" && len(name) <= 240 && !strings.ContainsAny(suffix, "\\/\x00\r\n")
}

func (d Discovery) validate() error {
	if !validPipe(d.Pipe) || d.PID == 0 || d.Protocol != Protocol {
		return errors.New("ipc: invalid discovery endpoint or protocol")
	}
	token, err := hex.DecodeString(d.Token)
	if err != nil || len(token) != 32 {
		return errors.New("ipc: invalid discovery token")
	}
	return nil
}

func encodeDiscovery(d Discovery) ([]byte, error) {
	if err := d.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(d)
}

func decodeDiscovery(data []byte) (Discovery, error) {
	var d Discovery
	if len(data) > maxDiscoveryBytes {
		return d, errors.New("ipc: discovery file exceeds limit")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return Discovery{}, fmt.Errorf("ipc: invalid discovery JSON: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return Discovery{}, errors.New("ipc: trailing discovery data")
	}
	return d, d.validate()
}

// DefaultDir is independent of profile data and identifies this user's daemon.
func DefaultDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "padmate", "run"), nil
}

func discoveryDir(dir string) (string, error) {
	if dir == "" {
		return DefaultDir()
	}
	return filepath.Abs(dir)
}

// ReadDiscovery reads at most 8 KiB. Stale discovery files are allowed; Dial
// verifies that the live pipe server owns the advertised PID before hello.
func ReadDiscovery(dir string) (Discovery, error) {
	dir, err := discoveryDir(dir)
	if err != nil {
		return Discovery{}, err
	}
	f, err := os.Open(filepath.Join(dir, "daemon.json"))
	if err != nil {
		return Discovery{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxDiscoveryBytes+1))
	if err != nil {
		return Discovery{}, err
	}
	return decodeDiscovery(data)
}

func writeDiscovery(dir string, d Discovery) error {
	data, err := encodeDiscovery(d)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file := filepath.Join(dir, "daemon-"+rand.Text()+".tmp")
	defer func() { _ = os.Remove(file) }()
	if err := writePrivate(file, append(data, '\n')); err != nil {
		return err
	}
	return os.Rename(file, filepath.Join(dir, "daemon.json"))
}

// Endpoint owns the first pipe instance and its discovery file.
type Endpoint struct {
	net.Listener
	Discovery Discovery
	Dir       string
	once      sync.Once
	err       error
}

// Close removes only our discovery before releasing the first-instance lock.
func (e *Endpoint) Close() error {
	e.once.Do(func() {
		if d, err := ReadDiscovery(e.Dir); err == nil && d == e.Discovery {
			e.err = os.Remove(filepath.Join(e.Dir, "daemon.json"))
		}
		e.err = errors.Join(e.err, e.Listener.Close())
	})
	return e.err
}
