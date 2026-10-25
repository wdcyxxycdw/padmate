package logging

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

const (
	// DefaultMaxBytes rotates a file before a record would exceed 5 MiB.
	DefaultMaxBytes int64 = 5 * 1024 * 1024
	// DefaultBackups retains the three most recent files, newest in .1.
	DefaultBackups = 3
)

// rotatingFile receives one complete JSON record per Write from slog.
// A single oversized record is allowed in an empty file; the next record
// rotates it. No file is ever opened with truncation, including recovery.
type rotatingFile struct {
	mu             sync.Mutex
	path, filePath string
	f              *os.File
	size, maxBytes int64
	backups        int
	closed         bool
	closeErr       error
	lastError      string
	onError        func(error)
	open           func(string, int, os.FileMode) (*os.File, error)
}

func openRotatingFile(path string, maxBytes int64, backups int, onError func(error)) (*rotatingFile, error) {
	w := &rotatingFile{path: path, filePath: path, maxBytes: maxBytes, backups: backups, onError: onError, open: os.OpenFile}
	if err := w.reopen(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingFile) reopen() error {
	if w.f != nil {
		return nil
	}
	f, err := w.open(w.filePath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	s, err := f.Stat()
	if err != nil {
		return errors.Join(err, f.Close())
	}
	w.f, w.size = f, s.Size()
	return nil
}

func (w *rotatingFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return 0, os.ErrClosed
	}
	var rotationErr error
	if w.filePath != w.path || w.size > 0 && int64(len(p)) > w.maxBytes-w.size {
		rotationErr = w.rotate()
	}
	writeErr := w.reopen()
	n := 0
	if writeErr == nil {
		n, writeErr = w.f.Write(p)
		w.size += int64(n)
	}
	diagnostic := errors.Join(rotationErr, writeErr)
	if diagnostic != nil {
		diagnostic = fmt.Errorf("logging: file %s: %w", w.path, diagnostic)
		if diagnostic.Error() == w.lastError {
			diagnostic = nil
		} else {
			w.lastError = diagnostic.Error()
		}
	} else {
		w.lastError = ""
	}
	w.mu.Unlock()
	if diagnostic != nil && w.onError != nil {
		w.onError(diagnostic)
	}
	// Rotation failure is recoverable when appending still succeeded.
	return n, writeErr
}

func (w *rotatingFile) rotate() error {
	if w.filePath != w.path {
		// A new file and rollback both failed earlier. Keep appending to
		// the preserved .1 file until a fresh active path can be created.
		f, err := w.open(w.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("restore active log: %w", err)
		}
		var closeErr error
		if w.f != nil {
			closeErr = w.f.Close()
		}
		w.f, w.filePath, w.size = f, w.path, 0
		return closeErr
	}
	// Shift backups before touching the active file. An obstruction here
	// leaves the active handle and its contents intact.
	if err := os.Remove(fmt.Sprintf("%s.%d", w.path, w.backups)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove oldest backup: %w", err)
	}
	for i := w.backups - 1; i >= 1; i-- {
		if err := os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("move backup %d: %w", i, err)
		}
	}
	if w.f != nil {
		err := w.f.Close()
		w.f = nil
		if err != nil {
			return fmt.Errorf("close active log: %w", err)
		}
	}
	backup := w.path + ".1"
	if err := os.Rename(w.path, backup); err != nil {
		return fmt.Errorf("archive active log: %w", err)
	}
	w.filePath = backup
	f, err := w.open(w.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o644)
	if err != nil {
		rollbackErr := os.Rename(backup, w.path)
		if rollbackErr == nil {
			w.filePath = w.path
		}
		return fmt.Errorf("create active log: %w", errors.Join(err, rollbackErr))
	}
	w.f, w.filePath, w.size = f, w.path, 0
	return nil
}

func (w *rotatingFile) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		w.closed = true
		if w.f != nil {
			w.closeErr = w.f.Close()
			w.f = nil
		}
	}
	return w.closeErr
}
