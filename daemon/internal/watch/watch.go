// Package watch polls a file for changes of its content: padmated's
// reload of the profile when it is saved (PRO-03). A DirWatcher polls the
// files of a folder, the profile's macros (SCR-50), by name, size and
// modification time.
//
// A Watcher stats the file every Interval. The file's key is its size and
// modification time, or "missing" when the stat fails. A key that differs
// from the last poll's marks the file changed; once the key has stayed the
// same for Debounce, measured on the clock across polls, the Watcher reads
// the file and takes its sha256; a key that differs right after the read,
// a save that began after the stat, discards what was read and starts the
// debounce again. Content with the sum last seen changes nothing: a touch,
// or a save that reverts. Other content goes to Run's function. So several
// writes in a row, such as an editor's truncate and write, or a save by
// rename, give one change with the last content, some Debounce to
// Debounce+2*Interval after the last write.
//
// A missing file is no change: nothing happens until it is back. A read
// that fails, for a file an editor holds open without sharing, leaves the
// change pending, and the read is tried again at each poll. Errors of
// stat and read are logged at warn once per streak: the first one, then
// none until a read succeeds.
//
// Polling costs a stat per Interval and needs no thread of its own or
// Win32 binding; it sees saves by rename, and is deterministic with a
// fake clock and file system. A file system with a coarse modification
// time (FAT) can hide a save of the same size within one tick of it.
//
// Ignore is for the daemon's own writes of the file (ADR-0002): when the
// next read of the settled file finds content with an ignored sum, it is
// taken as seen without a change. Each such read forgets every sum
// ignored, so an own write that a save overwrote within the debounce
// ignores nothing later. Nothing calls it until IPC-04 (M9).
//
// The package reads time only from its clock.Clock and is cross-platform.
package watch

import (
	"context"
	"crypto/sha256"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

// Defaults of Options.
const (
	DefaultInterval = 100 * time.Millisecond
	DefaultDebounce = 200 * time.Millisecond
)

// Options configures a Watcher.
type Options struct {
	Interval time.Duration                     // between stats; 0 means DefaultInterval
	Debounce time.Duration                     // how long the file must be unchanged; 0 means DefaultDebounce
	Stat     func(string) (fs.FileInfo, error) // nil means os.Stat
	ReadFile func(string) ([]byte, error)      // nil means os.ReadFile
	Logger   *slog.Logger                      // nil discards
}

// Change is new content of the watched file.
type Change struct {
	Data []byte
	Sum  [32]byte // sha256 of Data
}

// Watcher polls one file. Run polls; Ignore may be called from any
// goroutine.
type Watcher struct {
	clk      clock.Clock
	path     string
	interval time.Duration
	debounce time.Duration
	stat     func(string) (fs.FileInfo, error)
	read     func(string) ([]byte, error)
	log      *slog.Logger
	started  atomic.Bool

	// Owned by Run's goroutine.
	last    [32]byte   // the sum last seen: initial, then the last read
	key     key        // at the last poll
	since   clock.Time // when key last changed
	pending bool       // the file changed and was not read since
	failing bool       // an error was logged, and no read succeeded since

	mu     sync.Mutex
	ignore map[[32]byte]bool
}

// key is what a poll compares: the file's size and modification time.
type key struct {
	missing bool
	size    int64
	mod     int64 // ns since 1970
}

// New returns a Watcher of the file at path, whose content had the sum
// initial when it was loaded. It fails when clk is nil, path is empty or
// a duration is negative.
func New(clk clock.Clock, path string, initial [32]byte, o Options) (*Watcher, error) {
	switch {
	case clk == nil:
		return nil, errors.New("watch: no clock")
	case path == "":
		return nil, errors.New("watch: no path")
	case o.Interval < 0 || o.Debounce < 0:
		return nil, errors.New("watch: negative interval or debounce")
	}
	w := &Watcher{
		clk: clk, path: path, interval: o.Interval, debounce: o.Debounce,
		stat: o.Stat, read: o.ReadFile, log: o.Logger,
		last: initial, ignore: map[[32]byte]bool{},
	}
	if w.interval == 0 {
		w.interval = DefaultInterval
	}
	if w.debounce == 0 {
		w.debounce = DefaultDebounce
	}
	if w.stat == nil {
		w.stat = os.Stat
	}
	if w.read == nil {
		w.read = os.ReadFile
	}
	if w.log == nil {
		w.log = slog.New(slog.DiscardHandler)
	}
	return w, nil
}

// Run polls the file until ctx is done, then returns nil. It first stats
// and reads the file once: content that is not the initial one, a change
// between the load and Run, counts as a change at that time. f gets each
// change on Run's goroutine, and the polls wait for it to return. Run may
// be called once.
func (w *Watcher) Run(ctx context.Context, f func(Change)) error {
	if f == nil {
		return errors.New("watch: Run needs a function")
	}
	if !w.started.CompareAndSwap(false, true) {
		return errors.New("watch: Run called twice")
	}
	w.start()
	for {
		t := w.clk.NewTimer(w.interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C():
			w.poll(f)
		}
	}
}

// Ignore makes content with sum, the daemon's own write, no change when
// the next read of the settled file finds it; that read forgets sum, and
// so does one that finds other content. Call it before the write.
func (w *Watcher) Ignore(sum [32]byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ignore[sum] = true
}

// start takes the key of the file at Run's start, and marks a change when
// the content is not the initial one or cannot be read.
func (w *Watcher) start() {
	w.key, w.since = w.statKey(), w.clk.Now()
	if w.key.missing {
		return
	}
	data, err := w.read(w.path)
	if err != nil {
		w.warn("cannot read the watched file", err)
		w.pending = true
		return
	}
	w.failing = false
	w.pending = sha256.Sum256(data) != w.last
}

// poll stats the file, and reads it once it has been unchanged for the
// debounce since it changed.
func (w *Watcher) poll(f func(Change)) {
	now := w.clk.Now()
	if k := w.statKey(); k != w.key {
		w.key, w.since, w.pending = k, now, true
	}
	if !w.pending || w.key.missing || now.Sub(w.since) < w.debounce {
		return
	}
	data, err := w.read(w.path)
	if err != nil {
		w.warn("cannot read the watched file", err)
		return // still pending
	}
	// A save that began after the stat may have left data half written.
	if k := w.statKey(); k != w.key {
		w.key, w.since = k, now
		return // still pending
	}
	w.failing, w.pending = false, false
	sum := sha256.Sum256(data)
	ignored := w.forget(sum)
	if sum == w.last {
		return
	}
	w.last = sum
	if ignored {
		w.log.Debug("watched file: own write", "file", w.path)
		return
	}
	f(Change{Data: data, Sum: sum})
}

// statKey returns the key of the file, logging a failed stat.
func (w *Watcher) statKey() key {
	fi, err := w.stat(w.path)
	if err != nil {
		w.warn("cannot stat the watched file", err)
		return key{missing: true}
	}
	return key{size: fi.Size(), mod: fi.ModTime().UnixNano()}
}

// forget reports whether sum, just read, was ignored, and forgets every
// sum ignored: an own write that this read did not find was overwritten.
func (w *Watcher) forget(sum [32]byte) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	ok := w.ignore[sum]
	clear(w.ignore)
	return ok
}

// warn logs the first error of a streak.
func (w *Watcher) warn(msg string, err error) {
	if w.failing {
		return
	}
	w.failing = true
	w.log.Warn(msg, "file", w.path, "err", err)
}
