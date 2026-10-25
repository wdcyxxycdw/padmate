package watch

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"sync/atomic"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

// DirOptions configures a DirWatcher.
type DirOptions struct {
	Interval time.Duration // between listings; 0 means DefaultInterval
	Debounce time.Duration // how long the folder must be unchanged; 0 means DefaultDebounce
	// Filter selects the names, at the top of the folder, that are watched;
	// nil watches every regular file.
	Filter  func(name string) bool
	ReadDir func(string) ([]fs.DirEntry, error) // nil means os.ReadDir
	Logger  *slog.Logger                        // nil discards
}

// DirChange is a settled change of a watched folder.
type DirChange struct {
	// Names are the files that appeared, disappeared or changed size or
	// modification time since the last change, sorted.
	Names []string
}

// DirWatcher polls the regular files at the top of a folder, those its
// filter selects, by name, size and modification time: the macros folder
// of a profile (SCR-50). Like a Watcher it debounces: once the listing has
// stayed the same for Debounce after it changed, Run's function gets the
// names that differ from the listing of the last change, or of Run's
// start. It reads no content. A missing folder lists no file; an error of
// the listing keeps the last one and is logged at warn once per streak.
// Subfolders and links are not files of it.
type DirWatcher struct {
	clk      clock.Clock
	dir      string
	interval time.Duration
	debounce time.Duration
	filter   func(string) bool
	readDir  func(string) ([]fs.DirEntry, error)
	log      *slog.Logger
	started  atomic.Bool

	// Owned by Run's goroutine.
	reported map[string]key // the listing of the last change
	current  map[string]key // at the last poll
	since    clock.Time     // when current last changed
	pending  bool
	failing  bool
}

// NewDir returns a DirWatcher of the folder dir. It fails when clk is nil,
// dir is empty or a duration is negative.
func NewDir(clk clock.Clock, dir string, o DirOptions) (*DirWatcher, error) {
	switch {
	case clk == nil:
		return nil, errors.New("watch: no clock")
	case dir == "":
		return nil, errors.New("watch: no folder")
	case o.Interval < 0 || o.Debounce < 0:
		return nil, errors.New("watch: negative interval or debounce")
	}
	w := &DirWatcher{clk: clk, dir: dir, interval: o.Interval, debounce: o.Debounce, filter: o.Filter, readDir: o.ReadDir, log: o.Logger}
	if w.interval == 0 {
		w.interval = DefaultInterval
	}
	if w.debounce == 0 {
		w.debounce = DefaultDebounce
	}
	if w.readDir == nil {
		w.readDir = os.ReadDir
	}
	if w.log == nil {
		w.log = slog.New(slog.DiscardHandler)
	}
	return w, nil
}

// Run polls the folder until ctx is done, then returns nil. The listing at
// its start is the one later changes are measured against. f gets each
// change on Run's goroutine, and the polls wait for it to return. Run may
// be called once.
func (w *DirWatcher) Run(ctx context.Context, f func(DirChange)) error {
	if f == nil {
		return errors.New("watch: Run needs a function")
	}
	if !w.started.CompareAndSwap(false, true) {
		return errors.New("watch: Run called twice")
	}
	w.reported, _ = w.list()
	if w.reported == nil {
		w.reported = map[string]key{}
	}
	w.current, w.since = w.reported, w.clk.Now()
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

// poll lists the folder, and reports the change once the listing has been
// the same for the debounce.
func (w *DirWatcher) poll(f func(DirChange)) {
	now := w.clk.Now()
	if l, ok := w.list(); ok && !sameListing(l, w.current) {
		w.current, w.since, w.pending = l, now, true
	}
	if !w.pending || now.Sub(w.since) < w.debounce {
		return
	}
	w.pending = false
	names := changedNames(w.reported, w.current)
	w.reported = w.current
	if len(names) > 0 {
		f(DirChange{Names: names})
	}
}

// list returns the watched files of the folder; ok is false when it could
// not be read. A missing folder has none.
func (w *DirWatcher) list() (map[string]key, bool) {
	entries, err := w.readDir(w.dir)
	if errors.Is(err, fs.ErrNotExist) {
		w.failing = false
		return map[string]key{}, true
	}
	if err != nil {
		if !w.failing {
			w.failing = true
			w.log.Warn("cannot list the watched folder", "dir", w.dir, "err", err)
		}
		return nil, false
	}
	w.failing = false
	files := make(map[string]key, len(entries))
	for _, e := range entries {
		if !e.Type().IsRegular() || w.filter != nil && !w.filter(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			files[e.Name()] = key{missing: true} // gone since the listing
			continue
		}
		files[e.Name()] = key{size: info.Size(), mod: info.ModTime().UnixNano()}
	}
	return files, true
}

func sameListing(a, b map[string]key) bool {
	if len(a) != len(b) {
		return false
	}
	for name, k := range a {
		if other, ok := b[name]; !ok || other != k {
			return false
		}
	}
	return true
}

// changedNames returns the names whose key differs between before and
// after, or that only one of them has, sorted.
func changedNames(before, after map[string]key) []string {
	var names []string
	for name, k := range after {
		if old, ok := before[name]; !ok || old != k {
			names = append(names, name)
		}
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
