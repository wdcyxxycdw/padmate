package watch_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/watch"
)

const (
	ms = time.Millisecond
	// waitLimit bounds every real-time wait of the tests.
	waitLimit = 5 * time.Second
	path      = "profile.json"
)

// errShared stands for a file an editor holds open without sharing.
var errShared = errors.New("the process cannot access the file because it is being used by another process")

// fakeFS is a file system of one directory in memory, on a fake clock:
// a write sets the modification time to Now.
type fakeFS struct {
	clk     *clock.Fake
	mu      sync.Mutex
	files   map[string]fakeFile
	readErr error  // ReadFile's while set
	onRead  func() // runs once, at the start of the next ReadFile
}

type fakeFile struct {
	data string
	mod  clock.Time
}

func (f *fakeFS) write(name, data string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[name] = fakeFile{data: data, mod: f.clk.Now()}
}

func (f *fakeFS) remove(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.files, name)
}

func (f *fakeFS) failReads(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readErr = err
}

func (f *fakeFS) Stat(name string) (fs.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	file, ok := f.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	return fakeInfo{name: name, file: file}, nil
}

// duringRead makes the next ReadFile run hook first, such as a write that
// the read then sees.
func (f *fakeFS) duringRead(hook func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onRead = hook
}

func (f *fakeFS) ReadFile(name string) ([]byte, error) {
	f.mu.Lock()
	hook := f.onRead
	f.onRead = nil
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	file, ok := f.files[name]
	switch {
	case !ok:
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	case f.readErr != nil:
		return nil, &fs.PathError{Op: "open", Path: name, Err: f.readErr}
	}
	return []byte(file.data), nil
}

type fakeInfo struct {
	name string
	file fakeFile
}

func (i fakeInfo) Name() string       { return i.name }
func (i fakeInfo) Size() int64        { return int64(len(i.file.data)) }
func (i fakeInfo) Mode() fs.FileMode  { return 0o644 }
func (i fakeInfo) ModTime() time.Time { return time.Unix(1_800_000_000, int64(i.file.mod)) }
func (i fakeInfo) IsDir() bool        { return false }
func (i fakeInfo) Sys() any           { return nil }

// armClock is a fake clock that reports the deadline of each timer made:
// the Watcher makes one when it waits for its next poll.
type armClock struct {
	*clock.Fake
	armed chan clock.Time
}

func (c armClock) NewTimer(d time.Duration) clock.Timer {
	at := c.Now().Add(d)
	t := c.Fake.NewTimer(d)
	c.armed <- at
	return t
}

// logs keeps the records of a Logger.
type logs struct {
	mu      sync.Mutex
	records []slog.Record
}

func (l *logs) Enabled(context.Context, slog.Level) bool { return true }
func (l *logs) WithAttrs([]slog.Attr) slog.Handler       { return l }
func (l *logs) WithGroup(string) slog.Handler            { return l }

func (l *logs) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, r.Clone())
	return nil
}

// warns returns the warnings, as "msg: err".
func (l *logs) warns() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, r := range l.records {
		if r.Level != slog.LevelWarn {
			continue
		}
		s := r.Message
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "err" {
				s += ": " + a.Value.String()
			}
			return true
		})
		out = append(out, s)
	}
	return out
}

// call is one call of Run's function.
type call struct {
	at   clock.Time
	data string
}

// harness runs a Watcher of path on a fake clock and a fakeFS.
type harness struct {
	t      *testing.T
	clk    armClock
	fs     *fakeFS
	logs   *logs
	w      *watch.Watcher
	next   clock.Time // the deadline of the Watcher's timer
	cancel context.CancelFunc
	done   chan error
	mu     sync.Mutex
	calls  []call
}

// newHarness returns a harness whose file has the content loaded, which
// is also the Watcher's initial content, with an Interval of 100 ms and a
// Debounce of 200 ms. Run starts with run.
func newHarness(t *testing.T, loaded string) *harness {
	t.Helper()
	fake := clock.NewFake()
	h := &harness{
		t: t, clk: armClock{Fake: fake, armed: make(chan clock.Time)},
		fs:   &fakeFS{clk: fake, files: map[string]fakeFile{}},
		logs: &logs{},
	}
	h.fs.write(path, loaded)
	w, err := watch.New(h.clk, path, sha256.Sum256([]byte(loaded)), watch.Options{
		Interval: 100 * ms, Debounce: 200 * ms,
		Stat: h.fs.Stat, ReadFile: h.fs.ReadFile, Logger: slog.New(h.logs),
	})
	if err != nil {
		t.Fatal(err)
	}
	h.w = w
	return h
}

// run starts Run and waits for its first wait.
func (h *harness) run() {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel, h.done = cancel, make(chan error, 1)
	go func() {
		h.done <- h.w.Run(ctx, func(c watch.Change) {
			if c.Sum != sha256.Sum256(c.Data) {
				h.t.Errorf("Change.Sum is not the sha256 of %q", c.Data)
			}
			h.mu.Lock()
			defer h.mu.Unlock()
			h.calls = append(h.calls, call{at: h.clk.Now(), data: string(c.Data)})
		})
	}()
	h.t.Cleanup(func() {
		if err := h.stop(); err != nil {
			h.t.Errorf("Run = %v", err)
		}
	})
	h.next = h.idle()
}

// stop ends Run and returns its error.
func (h *harness) stop() error {
	h.t.Helper()
	h.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	for {
		select {
		case <-h.clk.armed: // a poll that was under way
		case err := <-h.done:
			h.done <- err // for the next stop
			return err
		case <-ctx.Done():
			h.t.Fatal("Run did not return")
		}
	}
}

// idle waits until the Watcher waits for its next poll, and returns its
// deadline.
func (h *harness) idle() clock.Time {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	select {
	case at := <-h.clk.armed:
		return at
	case err := <-h.done:
		h.t.Fatalf("Run returned %v", err)
	case <-ctx.Done():
		h.t.Fatal("the Watcher does not wait for a poll")
	}
	return 0
}

// advance moves the clock on by d, running each poll that comes due.
func (h *harness) advance(d time.Duration) {
	h.t.Helper()
	to := h.clk.Now().Add(d)
	for h.next <= to {
		h.clk.Set(h.next)
		h.next = h.idle()
	}
	h.clk.Set(to)
}

func (h *harness) gotCalls() []call {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]call(nil), h.calls...)
}

// wantCalls checks the contents of the calls so far.
func (h *harness) wantCalls(want ...string) []call {
	h.t.Helper()
	calls := h.gotCalls()
	got := make([]string, len(calls))
	for i, c := range calls {
		got[i] = c.data
	}
	if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
		h.t.Fatalf("calls %q, want %q", got, want)
	}
	return calls
}

func (h *harness) wantWarns(want ...string) {
	h.t.Helper()
	got := h.logs.warns()
	if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
		h.t.Fatalf("warnings %q, want %q", got, want)
	}
}

func TestWritesInARow(t *testing.T) {
	h := newHarness(t, "a")
	h.run()
	h.advance(10 * ms)
	for _, data := range []string{"b", "bc", "bcd"} {
		h.fs.write(path, data)
		h.advance(50 * ms)
	}
	last := h.clk.Now().Add(-50 * ms)
	h.advance(time.Second)
	calls := h.wantCalls("bcd")
	if d := calls[0].at.Sub(last); d < 200*ms || d > 400*ms {
		t.Errorf("the call came %v after the last write, want 200ms to 400ms", d)
	}
	h.wantWarns()
}

func TestTouch(t *testing.T) {
	h := newHarness(t, "a")
	h.run()
	h.advance(50 * ms)
	h.fs.write(path, "a") // a new modification time
	h.advance(time.Second)
	h.wantCalls()
}

func TestRevert(t *testing.T) {
	h := newHarness(t, "a")
	h.run()
	h.advance(10 * ms)
	h.fs.write(path, "b")
	h.advance(50 * ms)
	h.fs.write(path, "a")
	h.advance(time.Second)
	h.wantCalls()
	// After a change, the initial content is new again.
	h.fs.write(path, "b")
	h.advance(time.Second)
	h.fs.write(path, "a")
	h.advance(time.Second)
	h.wantCalls("b", "a")
}

func TestIgnore(t *testing.T) {
	h := newHarness(t, "a")
	h.w.Ignore(sha256.Sum256([]byte("own")))
	h.run()
	h.advance(10 * ms)
	h.fs.write(path, "own")
	h.advance(time.Second)
	h.wantCalls()
	h.fs.write(path, "c")
	h.advance(time.Second)
	h.wantCalls("c")
	// The ignored sum is forgotten once seen.
	h.fs.write(path, "own")
	h.advance(time.Second)
	h.wantCalls("c", "own")
}

// TestIgnoreSeen forgets an ignored sum that the file already has: a
// later save of it is a change.
func TestIgnoreSeen(t *testing.T) {
	h := newHarness(t, "a")
	h.run()
	h.w.Ignore(sha256.Sum256([]byte("a")))
	h.advance(10 * ms)
	h.fs.write(path, "a")
	h.advance(time.Second)
	h.fs.write(path, "b")
	h.advance(time.Second)
	h.fs.write(path, "a")
	h.advance(time.Second)
	h.wantCalls("b", "a")
}

func TestDelete(t *testing.T) {
	h := newHarness(t, "a")
	h.run()
	h.advance(10 * ms)
	h.fs.remove(path)
	h.advance(2 * time.Second)
	h.wantCalls()
	h.wantWarns("cannot stat the watched file: stat profile.json: file does not exist")
}

func TestDeleteRecreate(t *testing.T) {
	h := newHarness(t, "a")
	h.run()
	h.advance(10 * ms)
	h.fs.remove(path)
	h.advance(time.Second)
	h.fs.write(path, "b")
	h.advance(time.Second)
	h.wantCalls("b")
	h.wantWarns("cannot stat the watched file: stat profile.json: file does not exist")
	// A new streak warns again.
	h.fs.remove(path)
	h.advance(time.Second)
	h.fs.write(path, "b") // the content last seen
	h.advance(time.Second)
	h.wantCalls("b")
	h.wantWarns("cannot stat the watched file: stat profile.json: file does not exist",
		"cannot stat the watched file: stat profile.json: file does not exist")
}

// TestMissingAtStart waits for a file that is missing when Run starts.
func TestMissingAtStart(t *testing.T) {
	h := newHarness(t, "a")
	h.fs.remove(path)
	h.run()
	h.advance(time.Second)
	h.fs.write(path, "a")
	h.advance(time.Second)
	h.wantCalls() // the content loaded
	h.fs.write(path, "b")
	h.advance(time.Second)
	h.wantCalls("b")
	h.wantWarns("cannot stat the watched file: stat profile.json: file does not exist")
}

func TestReadError(t *testing.T) {
	h := newHarness(t, "a")
	h.run()
	h.advance(10 * ms)
	h.fs.write(path, "b")
	h.fs.failReads(errShared)
	h.advance(time.Second)
	h.wantCalls()
	h.fs.failReads(nil)
	h.advance(100 * ms) // the next poll reads
	h.wantCalls("b")
	h.wantWarns("cannot read the watched file: open profile.json: " + errShared.Error())
}

func TestChangedBeforeRun(t *testing.T) {
	h := newHarness(t, "a")
	h.clk.Advance(5 * ms)
	h.fs.write(path, "b")
	h.run()
	h.advance(time.Second)
	calls := h.wantCalls("b")
	if d := calls[0].at.Sub(clock.Time(5 * ms)); d < 200*ms {
		t.Errorf("the call came %v after Run started, want at least the debounce", d)
	}
}

// TestUnreadableAtStart reads again a file that Run could not read at its
// start.
func TestUnreadableAtStart(t *testing.T) {
	h := newHarness(t, "a")
	h.fs.failReads(errShared)
	h.run()
	h.advance(time.Second)
	h.fs.failReads(nil)
	h.advance(100 * ms)
	h.wantCalls() // the content loaded
	h.wantWarns("cannot read the watched file: open profile.json: " + errShared.Error())
}

func TestPartialWrite(t *testing.T) {
	h := newHarness(t, "{}")
	h.run()
	h.advance(10 * ms)
	h.fs.write(path, `{"na`)
	h.advance(150 * ms) // a poll sees it
	h.fs.write(path, `{"name": "x"}`)
	h.advance(time.Second)
	h.wantCalls(`{"name": "x"}`)
}

// TestSaveDuringRead: a save that begins between the stat and the read,
// which then finds it half written, is read again once it has settled.
func TestSaveDuringRead(t *testing.T) {
	h := newHarness(t, "{}")
	h.run()
	h.advance(10 * ms)
	h.fs.write(path, `{"a": 1}`)
	h.fs.duringRead(func() { h.fs.write(path, `{"a":`) })
	h.advance(300 * ms) // the poll at 300 ms reads
	h.wantCalls()
	h.fs.write(path, `{"a": 2}`) // the rest of that save
	h.advance(time.Second)
	h.wantCalls(`{"a": 2}`)
}

// TestIgnoreSuperseded: an own write that a save overwrites before the
// Watcher reads it ignores nothing later.
func TestIgnoreSuperseded(t *testing.T) {
	h := newHarness(t, "a")
	h.run()
	h.w.Ignore(sha256.Sum256([]byte("own")))
	h.advance(10 * ms)
	h.fs.write(path, "own")
	h.advance(50 * ms)
	h.fs.write(path, "user")
	h.advance(time.Second)
	h.wantCalls("user")
	h.fs.write(path, "own") // such as an undo
	h.advance(time.Second)
	h.wantCalls("user", "own")
}

func TestRunEnds(t *testing.T) {
	h := newHarness(t, "a")
	h.run()
	if err := h.stop(); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if err := h.w.Run(context.Background(), func(watch.Change) {}); err == nil {
		t.Error("a second Run did not fail")
	}
}

func TestRunNil(t *testing.T) {
	h := newHarness(t, "a")
	if err := h.w.Run(context.Background(), nil); err == nil {
		t.Error("Run with no function did not fail")
	}
}

func TestDefaults(t *testing.T) {
	fake := clock.NewFake()
	clk := armClock{Fake: fake, armed: make(chan clock.Time, 1)}
	fsys := &fakeFS{clk: fake, files: map[string]fakeFile{path: {data: "a"}}}
	w, err := watch.New(clk, path, sha256.Sum256([]byte("a")), watch.Options{Stat: fsys.Stat, ReadFile: fsys.ReadFile})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx, func(watch.Change) {}) }()
	wait, stop := context.WithTimeout(context.Background(), waitLimit)
	defer stop()
	select {
	case at := <-clk.armed:
		if at != clock.Time(watch.DefaultInterval) {
			t.Errorf("first poll at %v, want %v", at, watch.DefaultInterval)
		}
	case <-wait.Done():
		t.Fatal("the Watcher does not wait for a poll")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-wait.Done():
		t.Fatal("Run did not return")
	}
}

func TestNew(t *testing.T) {
	clk := clock.NewFake()
	for _, tc := range []struct {
		name string
		clk  clock.Clock
		path string
		o    watch.Options
	}{
		{"no clock", nil, path, watch.Options{}},
		{"no path", clk, "", watch.Options{}},
		{"negative interval", clk, path, watch.Options{Interval: -ms}},
		{"negative debounce", clk, path, watch.Options{Debounce: -ms}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := watch.New(tc.clk, tc.path, [32]byte{}, tc.o); err == nil {
				t.Error("New did not fail")
			}
		})
	}
}

// TestRealFile watches a file on disk, with the real clock and the
// default Stat and ReadFile.
func TestRealFile(t *testing.T) {
	name := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(name, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := watch.New(clock.Real(), name, sha256.Sum256([]byte("a")), watch.Options{Interval: ms, Debounce: 5 * ms})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	changes := make(chan string)
	done := make(chan error, 1)
	go func() {
		done <- w.Run(ctx, func(c watch.Change) {
			select {
			case changes <- string(c.Data):
			case <-ctx.Done():
			}
		})
	}()
	// A different size, so that a coarse modification time cannot hide it.
	if err := os.WriteFile(name, []byte("bb"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A poll between the truncation and the write may see the file empty.
	for got := ""; got != "bb"; {
		select {
		case got = <-changes:
		case <-ctx.Done():
			t.Fatal("no change to the content written")
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run = %v", err)
	}
}
