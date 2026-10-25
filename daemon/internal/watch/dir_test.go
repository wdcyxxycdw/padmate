package watch_test

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/watch"
)

// fakeEntry is a file of a fakeFS as a directory entry.
type fakeEntry struct {
	info fakeInfo
	dir  bool
}

func (e fakeEntry) Name() string { return e.info.name }
func (e fakeEntry) IsDir() bool  { return e.dir }
func (e fakeEntry) Type() fs.FileMode {
	if e.dir {
		return fs.ModeDir
	}
	return 0
}
func (e fakeEntry) Info() (fs.FileInfo, error) { return e.info, nil }

// dirHarness runs a DirWatcher of a fakeFS's directory on a fake clock.
type dirHarness struct {
	t       *testing.T
	clk     armClock
	fs      *fakeFS
	dirs    []string // names listed as folders
	missing bool     // the folder does not exist
	listErr error
	logs    *logs
	w       *watch.DirWatcher
	next    clock.Time
	cancel  context.CancelFunc
	done    chan error
	mu      sync.Mutex
	calls   [][]string
}

func newDirHarness(t *testing.T, files ...string) *dirHarness {
	t.Helper()
	fake := clock.NewFake()
	h := &dirHarness{
		t: t, clk: armClock{Fake: fake, armed: make(chan clock.Time)},
		fs: &fakeFS{clk: fake, files: map[string]fakeFile{}}, logs: &logs{},
	}
	for _, f := range files {
		h.fs.write(f, "x")
	}
	w, err := watch.NewDir(h.clk, "macros", watch.DirOptions{
		Interval: 100 * ms, Debounce: 200 * ms, ReadDir: h.readDir, Logger: slog.New(h.logs),
		Filter: func(name string) bool { return strings.HasSuffix(name, ".ts") },
	})
	if err != nil {
		t.Fatal(err)
	}
	h.w = w
	return h
}

func (h *dirHarness) readDir(string) ([]fs.DirEntry, error) {
	h.fs.mu.Lock()
	defer h.fs.mu.Unlock()
	switch {
	case h.missing:
		return nil, &fs.PathError{Op: "open", Path: "macros", Err: fs.ErrNotExist}
	case h.listErr != nil:
		return nil, h.listErr
	}
	var out []fs.DirEntry
	for name, f := range h.fs.files {
		out = append(out, fakeEntry{info: fakeInfo{name: name, file: f}})
	}
	for _, d := range h.dirs {
		out = append(out, fakeEntry{info: fakeInfo{name: d}, dir: true})
	}
	return out, nil
}

func (h *dirHarness) set(f func()) {
	h.fs.mu.Lock()
	defer h.fs.mu.Unlock()
	f()
}

func (h *dirHarness) run() {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel, h.done = cancel, make(chan error, 1)
	go func() {
		h.done <- h.w.Run(ctx, func(c watch.DirChange) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.calls = append(h.calls, c.Names)
		})
	}()
	h.t.Cleanup(func() {
		h.cancel()
		ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
		defer cancel()
		for {
			select {
			case <-h.clk.armed:
			case err := <-h.done:
				if err != nil {
					h.t.Errorf("Run = %v", err)
				}
				return
			case <-ctx.Done():
				h.t.Error("Run did not return")
				return
			}
		}
	})
	h.next = h.idle()
}

func (h *dirHarness) idle() clock.Time {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	select {
	case at := <-h.clk.armed:
		return at
	case err := <-h.done:
		h.t.Fatalf("Run returned %v", err)
	case <-ctx.Done():
		h.t.Fatal("the DirWatcher does not wait for a poll")
	}
	return 0
}

func (h *dirHarness) advance(d time.Duration) {
	h.t.Helper()
	to := h.clk.Now().Add(d)
	for h.next <= to {
		h.clk.Set(h.next)
		h.next = h.idle()
	}
	h.clk.Set(to)
}

func (h *dirHarness) wantCalls(want ...string) {
	h.t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	got := make([]string, len(h.calls))
	for i, c := range h.calls {
		got[i] = strings.Join(c, ",")
	}
	if !slices.Equal(got, want) {
		h.t.Fatalf("calls %q, want %q", got, want)
	}
}

// Writes in a row give one change after the debounce, with every name
// that differs from the listing at the start: added, changed and removed.
// Files the filter skips, folders and a touch within the same tick change
// nothing.
func TestDirChanges(t *testing.T) {
	h := newDirHarness(t, "a.ts", "b.ts", "c.ts")
	h.run()
	h.advance(10 * ms)
	h.fs.write("a.ts", "longer")
	h.advance(50 * ms)
	h.fs.remove("b.ts")
	h.fs.write("d.ts", "new")
	h.fs.write("notes.txt", "skipped")
	h.set(func() { h.dirs = []string{"lib.ts"} })
	h.advance(50 * ms)
	h.advance(time.Second)
	h.wantCalls("a.ts,b.ts,d.ts")
	h.fs.write("c.ts", "y") // a new modification time
	h.advance(time.Second)
	h.wantCalls("a.ts,b.ts,d.ts", "c.ts")
	// A change that reverts within the debounce reports nothing.
	h.fs.write("e.ts", "x")
	h.advance(100 * ms)
	h.fs.remove("e.ts")
	h.advance(time.Second)
	h.wantCalls("a.ts,b.ts,d.ts", "c.ts")
	if w := h.logs.warns(); len(w) != 0 {
		t.Fatal(w)
	}
}

// A missing folder lists no file: one that appears reports its files, and
// one removed reports them gone. A failed listing keeps the last one and
// warns once.
func TestDirMissingAndErrors(t *testing.T) {
	h := newDirHarness(t)
	h.set(func() { h.missing = true })
	h.run()
	h.advance(time.Second)
	h.set(func() { h.missing = false })
	h.fs.write("a.ts", "x")
	h.advance(time.Second)
	h.wantCalls("a.ts")
	h.set(func() { h.listErr = errors.New("denied") })
	h.advance(time.Second)
	h.advance(time.Second)
	h.wantCalls("a.ts")
	if w := h.logs.warns(); len(w) != 1 || !strings.Contains(w[0], "denied") {
		t.Fatal(w)
	}
	h.set(func() { h.listErr = nil; h.missing = true })
	h.advance(time.Second)
	h.wantCalls("a.ts", "a.ts")
}

// The real file system: the DirWatcher sees files of the folder that
// appear, change and go, on the real clock.
func TestDirRealFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "macros")
	listed := make(chan struct{})
	var once sync.Once
	w, err := watch.NewDir(clock.Real(), dir, watch.DirOptions{Interval: 5 * ms, Debounce: 10 * ms,
		ReadDir: func(name string) ([]fs.DirEntry, error) {
			defer once.Do(func() { close(listed) })
			return os.ReadDir(name)
		}})
	if err != nil {
		t.Fatal(err)
	}
	changes := make(chan []string, 8)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx, func(c watch.DirChange) { changes <- c.Names }) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	want := func(names ...string) {
		t.Helper()
		wait, stop := context.WithTimeout(context.Background(), waitLimit)
		defer stop()
		select {
		case got := <-changes:
			if !slices.Equal(got, names) {
				t.Fatalf("change %q, want %q", got, names)
			}
		case <-wait.Done():
			t.Fatalf("no change, want %q", names)
		}
	}
	<-listed // Run's first listing: the folder is missing
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "复制 全部.ts"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	want("复制 全部.ts")
	if err := os.Remove(filepath.Join(dir, "复制 全部.ts")); err != nil {
		t.Fatal(err)
	}
	want("复制 全部.ts")
}

func TestNewDirErrors(t *testing.T) {
	for _, tc := range []struct {
		clk clock.Clock
		dir string
		o   watch.DirOptions
	}{
		{nil, "macros", watch.DirOptions{}},
		{clock.NewFake(), "", watch.DirOptions{}},
		{clock.NewFake(), "macros", watch.DirOptions{Interval: -1}},
		{clock.NewFake(), "macros", watch.DirOptions{Debounce: -1}},
	} {
		if _, err := watch.NewDir(tc.clk, tc.dir, tc.o); err == nil {
			t.Errorf("NewDir(%v, %q, %+v) did not fail", tc.clk, tc.dir, tc.o)
		}
	}
	w, err := watch.NewDir(clock.NewFake(), "macros", watch.DirOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Run(context.Background(), nil); err == nil {
		t.Error("Run(nil) did not fail")
	}
}
