package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
)

// pin is what fetch checks against: the constants of internal/sdlpin in
// the tool, a synthetic release in the tests.
type pin struct {
	ZipURL    string
	ZipName   string
	ZipSize   int64
	ZipSHA256 string // lowercase hex
	DLLName   string // at the root of the zip
	DLLSHA256 string // lowercase hex
}

// outcome says how fetch produced the DLL.
type outcome int

const (
	upToDate   outcome = iota // the DLL was already there with the pinned hash
	fromCache                 // extracted from the cached zip
	downloaded                // extracted from a freshly downloaded zip
)

func (o outcome) String() string {
	switch o {
	case upToDate:
		return "up to date"
	case fromCache:
		return "extracted from the cached zip"
	case downloaded:
		return "downloaded"
	}
	return fmt.Sprintf("outcome(%d)", int(o))
}

// fetcher puts the pinned DLL into outDir.
type fetcher struct {
	pin      pin
	outDir   string
	cacheDir string // "" disables the cache
	client   *http.Client
	log      io.Writer // progress and warnings
}

// fetch makes outDir/<DLLName> the pinned DLL. When it returns an error,
// neither outDir nor the cache has been written to.
func (f *fetcher) fetch(ctx context.Context) (outcome, error) {
	dst := filepath.Join(f.outDir, f.pin.DLLName)
	switch sum, err := fileSHA256(dst); {
	case err == nil && sum == f.pin.DLLSHA256:
		return upToDate, nil
	case err == nil:
		fmt.Fprintf(f.log, "sdlfetch: %s has sha256 %s, want %s; replacing it\n", dst, sum, f.pin.DLLSHA256)
	case !errors.Is(err, fs.ErrNotExist):
		return 0, err
	}

	data, how, err := f.zip(ctx)
	if err != nil {
		return 0, err
	}
	dll, err := extract(data, f.pin.DLLName)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", f.pin.ZipName, err)
	}
	if sum := sha256Hex(dll); sum != f.pin.DLLSHA256 {
		return 0, fmt.Errorf("%s in %s: sha256 mismatch: got %s, want %s", f.pin.DLLName, f.pin.ZipName, sum, f.pin.DLLSHA256)
	}
	if err := writeAtomic(dst, dll); err != nil {
		return 0, err
	}
	// The cache is written last, so a mismatch above leaves it untouched;
	// failing to write it only costs a download next time.
	if how == downloaded && f.cacheDir != "" {
		if err := writeAtomic(filepath.Join(f.cacheDir, f.pin.ZipName), data); err != nil {
			fmt.Fprintf(f.log, "sdlfetch: not caching the zip: %v\n", err)
		}
	}
	return how, nil
}

// zip returns the verified release zip: the cached copy when it matches
// the pin, a download otherwise.
func (f *fetcher) zip(ctx context.Context) ([]byte, outcome, error) {
	if f.cacheDir != "" {
		cached := filepath.Join(f.cacheDir, f.pin.ZipName)
		data, err := os.ReadFile(cached)
		if err == nil {
			err = f.checkZip(data)
		}
		switch {
		case err == nil:
			return data, fromCache, nil
		case !errors.Is(err, fs.ErrNotExist):
			fmt.Fprintf(f.log, "sdlfetch: ignoring the cached zip: %v\n", err)
		}
	}
	fmt.Fprintf(f.log, "sdlfetch: downloading %s\n", f.pin.ZipURL)
	data, err := f.download(ctx)
	if err != nil {
		return nil, 0, err
	}
	if err := f.checkZip(data); err != nil {
		return nil, 0, err
	}
	return data, downloaded, nil
}

// download reads the zip from ZipURL, at most one byte more than ZipSize.
func (f *fetcher) download(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.pin.ZipURL, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", f.pin.ZipURL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, f.pin.ZipSize+1))
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", f.pin.ZipURL, err)
	}
	if int64(len(data)) > f.pin.ZipSize {
		return nil, fmt.Errorf("%s: size mismatch: got more than %d bytes, want %d", f.pin.ZipName, f.pin.ZipSize, f.pin.ZipSize)
	}
	return data, nil
}

// checkZip reports whether data has the pinned size and SHA-256.
func (f *fetcher) checkZip(data []byte) error {
	if n := int64(len(data)); n != f.pin.ZipSize {
		return fmt.Errorf("%s: size mismatch: got %d bytes, want %d", f.pin.ZipName, n, f.pin.ZipSize)
	}
	if sum := sha256Hex(data); sum != f.pin.ZipSHA256 {
		return fmt.Errorf("%s: sha256 mismatch: got %s, want %s", f.pin.ZipName, sum, f.pin.ZipSHA256)
	}
	return nil
}

// extract returns the file called name at the root of the zip in data.
// The zip reader checks the file's CRC-32 as it reads.
func extract(data []byte, name string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, zf := range zr.File {
		if zf.Name != name {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("no %s in the zip", name)
}

// writeAtomic writes data to path through a temporary file in the same
// directory, so path never holds a partial file.
func writeAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// fileSHA256 returns the SHA-256 of the file at path as lowercase hex.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
