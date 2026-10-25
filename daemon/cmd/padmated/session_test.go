package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

// fakeSession is the session watcher that testConfig gives live mode
// (DMN-09): it keeps the Config startLive passed, and the test calls its
// hooks as T-session would.
type fakeSession struct {
	cfg      winsession.Config
	degraded chan struct{}

	mu sync.Mutex
	// onClose, when set, is called by Close, before it counts.
	onClose func()
	err     error
	// closeErr, when set, is what Close returns instead of err: Close
	// timed out.
	closeErr error
	closes   int
}

func newFakeSession(cfg winsession.Config) *fakeSession {
	return &fakeSession{cfg: cfg, degraded: make(chan struct{})}
}

// Close counts, and returns Err, as a Watcher does after its thread ended
// by itself, or closeErr when set.
func (f *fakeSession) Close() error {
	f.mu.Lock()
	onClose := f.onClose
	f.mu.Unlock()
	if onClose != nil {
		onClose()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	if f.closeErr != nil {
		return f.closeErr
	}
	return f.err
}

// setCloseErr sets what Close returns.
func (f *fakeSession) setCloseErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeErr = err
}

func (f *fakeSession) Degraded() <-chan struct{} { return f.degraded }

func (f *fakeSession) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

// setOnClose sets what Close calls first.
func (f *fakeSession) setOnClose(g func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onClose = g
}

// closeCount returns how many times Close was called.
func (f *fakeSession) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closes
}

// change reports that sig took the reasons from was to now.
func (f *fakeSession) change(sig winsession.Signal, was, now winsession.Reason) {
	f.cfg.OnChange(winsession.Change{Signal: sig, Was: was, Now: now})
}

// stop asks padmated to stop, with OnStop's cause.
func (f *fakeSession) stop(cause string) { f.cfg.OnStop(cause) }

// degrade ends the session thread with err: Degraded closes.
func (f *fakeSession) degrade(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
	close(f.degraded)
}

// isClosed reports whether Close was called, or Events closed.
func (f *fakeSource) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// sessionLive starts live mode as testConfig configures it with args,
// after change, which may change cfg, with a fakeSource, and adds the
// gamepad liveTestPad on it.
func sessionLive(t *testing.T, change func(*liveConfig), newInject func() (output.Sender, error), args ...string) (*testLive, *fakeSource) {
	t.Helper()
	tl, src := sessionStart(t, change, newInject, args...)
	tl.addPad(t, src)
	return tl, src
}

// sessionStart is sessionLive without the gamepad: startLive has returned
// when it does, and src has no device yet.
func sessionStart(t *testing.T, change func(*liveConfig), newInject func() (output.Sender, error), args ...string) (*testLive, *fakeSource) {
	t.Helper()
	src := newFakeSource()
	tl := &testLive{}
	cfg := testConfig(t, tl, "", newInject, args...)
	cfg.open = func(input.Options) (eventSource, error) { return src, nil }
	if change != nil {
		change(&cfg)
	}
	tl.start(t, cfg)
	return tl, src
}

// addPad adds the gamepad liveTestPad on src, the fakeSource of
// sessionStart, and waits for its slot.
func (tl *testLive) addPad(t *testing.T, src *fakeSource) {
	t.Helper()
	info := &input.DeviceInfo{ID: 1, Name: liveTestPad, Path: "fake1", IsGamepad: true}
	src.events <- input.Event{Device: 1, Kind: input.DeviceAdded, Info: info}
	tl.waitFor(t, tl.log, `"msg":"slot assigned"`)
}

// button sends an edge of control on the gamepad of sessionLive.
func button(src *fakeSource, control string, down bool) {
	kind := input.ButtonUp
	if down {
		kind = input.ButtonDown
	}
	src.events <- input.Event{Device: 1, Kind: kind, Control: control}
}

// TestLiveSessionLock: a lock of the session (DMN-09) suspends the
// mapping: Space, held on east, is released, and presses map to nothing
// until the unlock; east, pressed while locked and held through the
// unlock, does nothing until it is pressed again. It follows the
// supervisor's golden test session-lock. Suspend closes the gate before
// the router suspends, so the release may come before the note. Fakes
// stand in for the Source and the session watcher, so it needs no SDL.
func TestLiveSessionLock(t *testing.T) {
	tl, src := sessionLive(t, nil, nil, "--dry-run")
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space\n")
	tl.session.change(winsession.SigLock, 0, winsession.Lock)
	tl.waitFor(t, tl.stdout, "  mapping suspended (lock): release all\n")
	tl.waitFor(t, tl.stdout, "send -Space\n")
	tl.waitRow(t, "the cancel of east", decisionRow("0", "east", "cancel"))
	if st := tl.sup.Status(); st.Suspended != winsession.Lock || st.Paused || st.Mapping() {
		t.Errorf("Status() = %+v, want suspended for lock, not paused", st)
	}

	button(src, "east", false)
	button(src, "south", true)
	button(src, "south", false)
	tl.waitRow(t, "the press of south while locked", pausedRow("0", "south"))
	button(src, "east", true)
	tl.waitRow(t, "the press of east while locked", pausedRow("0", "east"))
	tl.session.change(winsession.SigUnlock, winsession.Lock, 0)
	tl.waitUnsuspended(t, tl.stdout, "  mapping unsuspended\n")
	button(src, "east", false) // held through the unlock: nothing
	ctx, cancel := context.WithTimeout(context.Background(), liveWait)
	defer cancel()
	if err := tl.sup.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if got, want := sendEdges(tl.stdout.String()), []string{"+Space", "-Space"}; !slices.Equal(got, want) {
		t.Fatalf("held release after unlock sent %q, want %q", got, want)
	}
	button(src, "east", true)
	tl.waitForCount(t, tl.stdout, "send +Space\n", 2)
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
	}

	out := tl.stdout.String()
	checkBalanced(t, out)
	if got, want := sendEdges(out), []string{"+Space", "-Space", "+Space", "-Space"}; !slices.Equal(got, want) {
		t.Errorf("edges sent %q, want %q:\n%s", got, want, out)
	}
	suspended, unsuspended := strings.Index(out, "  mapping suspended (lock): release all\n"), strings.Index(out, "  mapping unsuspended\n")
	if suspended < 0 || unsuspended < suspended || strings.Count(out, "  mapping ") != 2 {
		t.Fatalf("want one suspension, then its end:\n%s", out)
	}
	if got := sendEdges(out[suspended:unsuspended]); len(got) > 1 || len(got) == 1 && got[0] != "-Space" {
		t.Errorf("edges sent while locked %q, want at most the release of Space:\n%s", got, out)
	}
	// Resume masking can consume the release before unsuspended is printed;
	// the later physical release is then spurious. Assert output above instead
	// of requiring one diagnostic reason or cross-goroutine print order.
	log := tl.log.String()
	for _, msg := range []string{"mapping suspended", "mapping unsuspended"} {
		if n := len(logLines(log, msg)); n != 1 {
			t.Errorf("%d %s lines, want 1\nlog:\n%s", n, msg, log)
		}
	}
	if line := logLine(log, "padmated stopping"); !strings.Contains(line, `"cause":"signal"`) {
		t.Errorf("padmated stopping: %q, want cause signal", line)
	}
	if n := tl.session.closeCount(); n != 1 {
		t.Errorf("the session watcher closed %d times, want once", n)
	}
}

// TestLiveSessionAtStart: a session locked when padmated starts, which
// T-session reports before Start returns, suspends the mapping before the
// first event; the note follows the header.
func TestLiveSessionAtStart(t *testing.T) {
	tl, src := sessionLive(t, func(cfg *liveConfig) {
		start := cfg.session
		cfg.session = func(c winsession.Config) (sessionWatcher, error) {
			w, err := start(c)
			c.OnChange(winsession.Change{Signal: winsession.SigInit, Now: winsession.Lock})
			return w, err
		}
	}, nil, "--dry-run")
	tl.waitFor(t, tl.stdout, "  mapping suspended (lock): release all\n")
	if st := tl.sup.Status(); st.Suspended != winsession.Lock {
		t.Errorf("Status().Suspended = %v, want lock", st.Suspended)
	}
	button(src, "east", true)
	tl.waitRow(t, "the press of east while locked", pausedRow("0", "east"))
	button(src, "east", false)
	tl.session.change(winsession.SigUnlock, winsession.Lock, 0)
	tl.waitUnsuspended(t, tl.stdout, "  mapping unsuspended\n")
	button(src, "east", true)
	tl.sync()
	if strings.Contains(tl.stdout.String(), "send +Space\n") {
		t.Fatal("release while locked cleared the recovery quarantine")
	}
	button(src, "east", false)
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space\n")
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
	}
	out := tl.stdout.String()
	checkBalanced(t, out)
	if h, n := strings.Index(out, "  owner\n"), strings.Index(out, "  mapping suspended (lock): release all\n"); h < 0 || n < h {
		t.Errorf("the note is not after the header:\n%s", out)
	}
	if got, want := sendEdges(out), []string{"+Space", "-Space"}; !slices.Equal(got, want) {
		t.Errorf("edges sent %q, want %q:\n%s", got, want, out)
	}
}

// TestLiveSessionStops: the end of the session and WM_CLOSE stop padmated
// as Ctrl+C does: it releases every key and exits 0, with the cause in
// stderr and the log. The session watcher closes once, after the Source.
// When the session ends it closes last, after the log has "padmated
// stopped" and is closed, since its Close lets the system end the
// process; otherwise it closes before, so that what it logs on its way
// out comes before the last line and is kept (DMN-09). A session that
// ends while a signal's exit path runs, before it closes the watcher,
// closes it last too, although the log's cause stays the signal.
func TestLiveSessionStops(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cause  string // the cause of "padmated stopping"
		stop   func(*testLive, *fakeSource)
		stderr string // the end of stderr
		last   bool   // the session watcher closes after the log
	}{
		{"signal", "signal", func(tl *testLive, _ *fakeSource) { tl.cancel() }, "padmated: stopping\n", false},
		{"session-end", "session-end", func(tl *testLive, _ *fakeSource) { tl.session.stop("session-end") },
			"padmated: stopping: the session is ending\n", true},
		{"close", "close", func(tl *testLive, _ *fakeSource) { tl.session.stop("close") }, "padmated: stopping: asked to close\n", false},
		// The exit path closes the Source before it chooses the order.
		{"signal, then session-end", "signal", func(tl *testLive, src *fakeSource) {
			src.setOnClose(func() { tl.session.stop("session-end") })
			tl.cancel()
		}, "padmated: stopping\n", true},
		{"close, then session-end", "close", func(tl *testLive, src *fakeSource) {
			src.setOnClose(func() { tl.session.stop("session-end") })
			tl.session.stop("close")
		}, "padmated: stopping: asked to close\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			logClosed := false
			tl, src := sessionLive(t, func(cfg *liveConfig) {
				cfg.closeLog = func(*int) {
					mu.Lock()
					defer mu.Unlock()
					logClosed = true
				}
			}, nil, "--dry-run")
			button(src, "east", true)
			tl.waitFor(t, tl.stdout, "send +Space\n")
			var atClose struct {
				log                  string
				logClosed, srcClosed bool
			}
			tl.session.setOnClose(func() {
				mu.Lock()
				defer mu.Unlock()
				atClose.log, atClose.logClosed, atClose.srcClosed = tl.log.String(), logClosed, src.isClosed()
				tl.session.cfg.Logger.Info("session watcher closing") // as its teardown logs
			})
			tc.stop(tl, src)
			if code := tl.waitCode(t); code != 0 {
				t.Errorf("exit %d, want 0\nstderr:\n%s", code, tl.stderr)
			}
			if !strings.HasSuffix(tl.stderr.String(), tc.stderr) {
				t.Errorf("stderr does not end with %q:\n%s", tc.stderr, tl.stderr)
			}
			out := tl.stdout.String()
			checkBalanced(t, out)
			if exit, up := strings.Index(out, "  exit: release all\n"), strings.Index(out, "send -Space\n"); exit < 0 || up < exit {
				t.Errorf("Space is not released after the exit line:\n%s", out)
			}
			log := tl.log.String()
			if line := logLine(log, "padmated stopping"); !strings.Contains(line, `"cause":"`+tc.cause+`"`) {
				t.Errorf("padmated stopping: %q, want cause %s", line, tc.cause)
			}
			if n := tl.session.closeCount(); n != 1 {
				t.Errorf("the session watcher closed %d times, want once", n)
			}
			if !strings.Contains(logLine(log, "padmated stopped"), `"code":0`) {
				t.Errorf("no padmated stopped with code 0 in the log:\n%s", log)
			}
			if stopped, closing := strings.Index(log, `"msg":"padmated stopped"`), strings.Index(log, `"msg":"session watcher closing"`); closing < 0 ||
				(closing > stopped) != tc.last {
				t.Errorf("the session watcher's line at %d, padmated stopped at %d; want the watcher's after it: %v\nlog:\n%s",
					closing, stopped, tc.last, log)
			}
			mu.Lock()
			defer mu.Unlock()
			if stopped := strings.Contains(atClose.log, `"msg":"padmated stopped"`); !atClose.srcClosed ||
				atClose.logClosed != tc.last || stopped != tc.last {
				t.Errorf("at the session watcher's Close: Source closed %v, log closed %v, padmated stopped logged %v; want true, %v, %v\nlog:\n%s",
					atClose.srcClosed, atClose.logClosed, stopped, tc.last, tc.last, atClose.log)
			}
		})
	}
}

// TestLiveSessionCloseFails: a Close of the session watcher that fails is
// written to stderr and, unless the session is ending, logged at warn
// before "padmated stopped"; the exit code stays 0. When the session is
// ending the log has closed by then.
func TestLiveSessionCloseFails(t *testing.T) {
	for _, tc := range []struct {
		cause  string
		stop   func(*testLive)
		logged bool
	}{
		{"signal", func(tl *testLive) { tl.cancel() }, true},
		{"close", func(tl *testLive) { tl.session.stop("close") }, true},
		{"session-end", func(tl *testLive) { tl.session.stop("session-end") }, false},
	} {
		t.Run(tc.cause, func(t *testing.T) {
			tl, _ := sessionLive(t, nil, nil, "--dry-run")
			tl.session.setCloseErr(errors.New("winsession: the session thread did not end"))
			tc.stop(tl)
			if code := tl.waitCode(t); code != 0 {
				t.Errorf("exit %d, want 0\nstderr:\n%s", code, tl.stderr)
			}
			if stderr := tl.stderr.String(); strings.Count(stderr, "padmated: stopping the session watcher: winsession: the session thread did not end\n") != 1 {
				t.Errorf("stderr:\n%s\nwant the failed Close once", stderr)
			}
			log := tl.log.String()
			lines := logLines(log, "session watcher did not stop")
			if tc.logged != (len(lines) == 1) || len(lines) > 1 {
				t.Fatalf("session watcher did not stop lines %q, want logged %v\nlog:\n%s", lines, tc.logged, log)
			}
			if tc.logged && (!strings.Contains(lines[0], `"level":"WARN"`) ||
				strings.Index(log, `"msg":"session watcher did not stop"`) > strings.Index(log, `"msg":"padmated stopped"`)) {
				t.Errorf("want the failed Close at warn, before padmated stopped\nlog:\n%s", log)
			}
		})
	}
}

// TestLiveSessionStartFails: when the session watcher does not start,
// padmated maps without it: quietly on a system without one, with a
// warning on stderr and in the log otherwise.
func TestLiveSessionStartFails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		msg    string // of the log line
		level  string
		stderr string // "" for none
	}{
		{"unsupported", fmt.Errorf("winsession: %w", errors.ErrUnsupported), "session events unsupported", "DEBUG", ""},
		{"failed", errors.New("winsession: no window"), "session events off", "WARN",
			"padmated: session events off: winsession: no window; locking, sleep and sign-out will not release keys\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tl, src := sessionLive(t, func(cfg *liveConfig) {
				cfg.session = func(winsession.Config) (sessionWatcher, error) { return nil, tc.err }
			}, nil, "--dry-run")
			button(src, "east", true)
			tl.waitFor(t, tl.stdout, "send +Space\n")
			tl.cancel()
			if code := tl.waitCode(t); code != 0 {
				t.Errorf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
			}
			checkBalanced(t, tl.stdout.String())
			stderr := tl.stderr.String()
			if got := strings.Contains(stderr, "session events"); got != (tc.stderr != "") || !strings.Contains(stderr, tc.stderr) {
				t.Errorf("stderr:\n%s\nwant %q", stderr, tc.stderr)
			}
			log := tl.log.String()
			if lines := logLines(log, tc.msg); len(lines) != 1 || !strings.Contains(lines[0], `"level":"`+tc.level+`"`) {
				t.Errorf("%s lines %q, want one at level %s", tc.msg, lines, tc.level)
			}
		})
	}
}

// TestLiveSessionDegraded: when the session thread ends by itself,
// stderr says so, and the suspension stays: only the watcher's polling
// may end it from then on (DMN-09).
func TestLiveSessionDegraded(t *testing.T) {
	tl, src := sessionLive(t, nil, nil, "--dry-run")
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space\n")
	tl.session.change(winsession.SigLock, 0, winsession.Lock)
	tl.waitFor(t, tl.stdout, "  mapping suspended (lock): release all\n")
	tl.waitFor(t, tl.stdout, "send -Space\n")
	tl.session.degrade(errors.New("winsession: GetMessage failed"))
	tl.waitFor(t, tl.stderr, "padmated: session events stopped: winsession: GetMessage failed; locking and the secure desktop "+
		"are still polled every 2 s, sleep and sign-out will not release keys\n")
	button(src, "east", false)
	button(src, "south", true)
	tl.waitRow(t, "the press of south while locked", pausedRow("0", "south"))
	if st := tl.sup.Status(); st.Suspended != winsession.Lock {
		t.Errorf("Status().Suspended = %v, want lock", st.Suspended)
	}
	button(src, "south", false)
	tl.session.change(winsession.SigTick, winsession.Lock, 0) // the polling
	tl.waitUnsuspended(t, tl.stdout, "  mapping unsuspended\n")
	button(src, "east", true)
	tl.sync()
	button(src, "east", false)
	button(src, "east", true)
	tl.waitForCount(t, tl.stdout, "send +Space\n", 2)
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
	}
	checkBalanced(t, tl.stdout.String())
	if stderr := tl.stderr.String(); strings.Count(stderr, "session events stopped") != 1 || strings.Contains(stderr, "stopping the session watcher") {
		t.Errorf("stderr:\n%s\nwant the stop of the session thread once, and nothing of Close", stderr)
	}
	if n := tl.session.closeCount(); n != 1 {
		t.Errorf("the session watcher closed %d times, want once", n)
	}
}

// gateSender is a Sender of --inject for the tests: it keeps the edges it
// sent, fails every batch while fail is set, and blocks a batch with
// block's key until release closes.
type gateSender struct {
	block   string        // "-Space": the edge whose batch waits for release
	blocked chan struct{} // closed when that batch waits
	release chan struct{}

	mu   sync.Mutex
	fail bool
	sent []string
}

func newGateSender(block string) *gateSender {
	return &gateSender{block: block, blocked: make(chan struct{}), release: make(chan struct{})}
}

var errGate = errors.New("gateSender: refused")

func (s *gateSender) Send(evs []output.KeyEvent) (int, error) {
	for _, e := range evs {
		if s.block != "" && edgeName(e) == s.block {
			close(s.blocked)
			<-s.release
			s.block = ""
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return 0, errGate
	}
	for _, e := range evs {
		s.sent = append(s.sent, edgeName(e))
	}
	return len(evs), nil
}

func (s *gateSender) setFail(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = fail
}

func (s *gateSender) edges() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sent)
}

// waitSent waits, as waitUntil does, until s has sent edge.
func (tl *testLive) waitSent(t *testing.T, s *gateSender, edge string) {
	t.Helper()
	tl.waitUntil(t, "the edge "+edge, func() bool { return slices.Contains(s.edges(), edge) })
}

// waitUnsuspended waits, as waitFor does, until buf has want, the note or
// the log line of the end of the suspension, and then syncs: the router
// writes them before it opens the system's gate, which it does only behind
// a barrier to every slot (openAfter), and a Sync returns after those.
// Without the Sync, a press right after the note may reach the gate while
// it is still closed and be dropped.
func (tl *testLive) waitUnsuspended(t *testing.T, buf *syncBuffer, want string) {
	t.Helper()
	tl.waitFor(t, buf, want)
	ctx, cancel := context.WithTimeout(context.Background(), liveWait)
	defer cancel()
	if err := tl.sup.Sync(ctx); err != nil {
		t.Fatalf("Sync after %q: %v", want, err)
	}
	if b := tl.loop.Blocking(); b&output.BlockSystem != 0 {
		t.Fatalf("the output's gate is closed for %#x after %q and a Sync, want the system's open", b, want)
	}
}

// TestLiveSessionBarrier: the Barrier, which the sleep and the end of the
// session wait for, returns once T-output has sent the release that
// Suspend asked for; once the loop has ended it fails.
func TestLiveSessionBarrier(t *testing.T) {
	s := newGateSender("-Space")
	tl, src := sessionLive(t, nil, func() (output.Sender, error) { return s, nil }, "--inject")
	button(src, "east", true)
	tl.waitSent(t, s, "+Space")
	tl.session.change(winsession.SigSleep, 0, winsession.Sleep)
	select {
	case <-s.blocked:
	case <-time.After(liveWait):
		t.Fatalf("no release of Space within %v", liveWait)
	}
	ctx, cancel := context.WithTimeout(context.Background(), liveWait)
	defer cancel()
	barrier := make(chan error, 1)
	go func() { barrier <- tl.session.cfg.Barrier(ctx) }()
	select {
	case err := <-barrier:
		t.Fatalf("Barrier = %v while the release waits", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(s.release)
	if err := <-barrier; err != nil {
		t.Errorf("Barrier = %v, want nil", err)
	}
	if got, want := s.edges(), []string{"+Space", "-Space"}; !slices.Equal(got, want) {
		t.Errorf("sent %q when Barrier returned, want %q", got, want)
	}
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
	}
	if err := tl.session.cfg.Barrier(ctx); !errors.Is(err, output.ErrLoopClosed) {
		t.Errorf("Barrier after the exit = %v, want %v", err, output.ErrLoopClosed)
	}
}

// TestLiveSessionErrorsDemoted: while the system suspends the mapping, the
// output's errors, such as a release the lock screen refuses, which the
// loop resends, are logged at debug level only, and counted once the
// suspension ends.
func TestLiveSessionErrorsDemoted(t *testing.T) {
	s := newGateSender("")
	tl, src := sessionLive(t, nil, func() (output.Sender, error) { return s, nil }, "--inject")
	button(src, "east", true)
	tl.waitSent(t, s, "+Space")
	s.setFail(true)
	tl.session.change(winsession.SigLock, 0, winsession.Lock)
	tl.waitFor(t, tl.log, `"msg":"output error while suspended"`)
	s.setFail(false)
	tl.waitSent(t, s, "-Space") // the loop's resend
	tl.waitFor(t, tl.log, `"msg":"mapping suspended"`)
	tl.session.change(winsession.SigUnlock, winsession.Lock, 0)
	tl.waitFor(t, tl.log, `"msg":"output errors while suspended"`)
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
	}
	if got, want := s.edges(), []string{"+Space", "-Space"}; !slices.Equal(got, want) {
		t.Errorf("sent %q, want %q", got, want)
	}
	log := tl.log.String()
	if lines := logLines(log, "output error"); len(lines) != 0 {
		t.Errorf("output error lines %q, want none while suspended", lines)
	}
	debug := logLines(log, "output error while suspended")
	for _, l := range debug {
		if !strings.Contains(l, `"level":"DEBUG"`) || !strings.Contains(l, errGate.Error()) {
			t.Errorf("output error while suspended: %q, want debug level and the error", l)
		}
	}
	counts := logLines(log, "output errors while suspended")
	if len(counts) != 1 || !strings.Contains(counts[0], `"level":"INFO"`) || !strings.Contains(counts[0], `"count":`+strconv.Itoa(len(debug))) {
		t.Errorf("output errors while suspended lines %q, want one at info level with count %d", counts, len(debug))
	}
}
