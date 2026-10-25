//go:build !windows

package winsession

import "errors"

// Start returns errors.ErrUnsupported: sessions are watched on Windows
// only.
func Start(Config) (*Watcher, error) {
	return nil, errors.ErrUnsupported
}

// stop and finish are never reached: no Watcher starts here.
func (w *Watcher) stop() error { return w.wait(nil) }

func (w *Watcher) finish() { close(w.done) }
