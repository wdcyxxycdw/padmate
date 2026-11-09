package cspfile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"time"
)

// Fingerprint identifies the state of a file: size, modification time and
// the SHA-256 of its bytes.
type Fingerprint struct {
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	SHA256  string    `json:"sha256"`
}

// FingerprintOf reads path and returns its fingerprint. path must be a
// regular file.
func FingerprintOf(path string) (Fingerprint, error) {
	f, err := os.Open(path)
	if err != nil {
		return Fingerprint{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Fingerprint{}, err
	}
	if !fi.Mode().IsRegular() {
		return Fingerprint{}, fmt.Errorf("cspfile: %s is not a regular file", path)
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return Fingerprint{}, err
	}
	return Fingerprint{Size: n, ModTime: fi.ModTime().UTC(), SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// Same reports whether a and b describe the same file state.
func Same(a, b Fingerprint) bool {
	return a.Size == b.Size && a.ModTime.Equal(b.ModTime) && a.SHA256 == b.SHA256
}
