package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/watch"
)

// The profiles of the reload tests. Each is on one line, so that a
// diagnostic of the invalid one is on line 1, and their sizes differ, so
// that a save of one over another changes the file's size whatever its
// modification time.
const (
	profileZ = `{"schemaVersion": 0, "name": "reload", "layers": {"base": {` +
		`"south": {"press": {"type": "keys", "keys": "Ctrl+Z", "mode": "tap"}}, ` +
		`"east": {"press": {"type": "keys", "keys": "Space", "mode": "hold"}}}}}` + "\n"
	profileY = `{"schemaVersion": 0, "name": "reload", "layers": {"base": {` +
		`"south": {"press": {"type": "keys", "keys": "Ctrl+Y", "mode": "tap"}}, ` +
		`"east": {"press": {"type": "keys", "keys": "LShift", "mode": "hold"}}}}}` + "\n"
	profileBroken = `{"schemaVersion": 0, "name": }` + "\n"
)

// reloadLive is live mode on a fakeSource with a profile of its own, which
// the test rewrites. The watcher polls fast and counts its reads.
type reloadLive struct {
	*testLive
	src    *fakeSource
	path   string                 // the profile
	reads  atomic.Int64           // of the profile by the watcher, once each returned
	onRead atomic.Pointer[func()] // runs once, at the start of the next read
}

// startReloadLive starts live mode as newReloadLive configures it, and
// begins.
func startReloadLive(t *testing.T, profile string, args ...string) *reloadLive {
	t.Helper()
	rl, cfg := newReloadLive(t, profile, args...)
	rl.begin(t, cfg)
	return rl
}

// newReloadLive writes profile to a temporary file and returns the config
// of live mode with it, --dry-run and args, for begin.
func newReloadLive(t *testing.T, profile string, args ...string) (*reloadLive, liveConfig) {
	t.Helper()
	rl := &reloadLive{testLive: &testLive{}, src: newFakeSource(), path: filepath.Join(t.TempDir(), "profile.json")}
	rl.write(t, profile)
	cfg := testConfig(t, rl.testLive, "", nil, append([]string{"--profile", rl.path, "--dry-run"}, args...)...)
	cfg.open = func(input.Options) (eventSource, error) { return rl.src, nil }
	cfg.watch = watch.Options{
		Interval: 2 * time.Millisecond,
		// Long enough that a save, a truncate and a write, is one change.
		Debounce: 20 * time.Millisecond,
		ReadFile: func(path string) ([]byte, error) {
			if f := rl.onRead.Swap(nil); f != nil {
				(*f)()
			}
			data, err := os.ReadFile(path)
			rl.reads.Add(1)
			return data, err
		},
	}
	return rl, cfg
}

// begin starts live mode with cfg, connects a fake gamepad, device 1, and
// waits for the watcher's first read of the profile, when it watches. It
// stops when the test ends, taking the exit path.
func (rl *reloadLive) begin(t *testing.T, cfg liveConfig) {
	t.Helper()
	rl.start(t, cfg)
	rl.src.events <- input.Event{Device: 1, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: 1, Name: liveTestPad, IsGamepad: true}}
	rl.waitFor(t, rl.stdout, `device 1 "padmated live test" added to slot 0`)
	if rl.watched != nil {
		rl.waitReads(t, 1)
	}
}

// waitReads waits, at most liveWait, until the watcher has read the
// profile n times.
func (rl *reloadLive) waitReads(t *testing.T, n int64) {
	t.Helper()
	deadline := time.Now().Add(liveWait)
	for rl.reads.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("%d reads of the profile after %v, want %d\nlog:\n%s", rl.reads.Load(), liveWait, n, rl.log)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// write saves the profile as text.
func (rl *reloadLive) write(t *testing.T, text string) {
	t.Helper()
	if err := os.WriteFile(rl.path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// button sends an edge of control of device 1.
func (rl *reloadLive) button(control string, down bool) {
	kind := input.ButtonUp
	if down {
		kind = input.ButtonDown
	}
	rl.src.events <- input.Event{Device: 1, Kind: kind, Control: control}
}

// tap taps south and waits for its chord, which is the n-th tap of it.
func (rl *reloadLive) tap(t *testing.T, key string, n int) {
	t.Helper()
	rl.button("south", true)
	rl.button("south", false)
	rl.waitForCount(t, rl.stdout, "send +LCtrl +"+key+"\n", n)
	rl.waitForCount(t, rl.stdout, "send -"+key+" -LCtrl\n", n)
}

// stop stops live mode, as Ctrl+C does, and checks that it exits 0 with
// every key released.
func (rl *reloadLive) stop(t *testing.T) string {
	t.Helper()
	return rl.exited(t, rl.stopping())
}

// stopping stops live mode, as Ctrl+C does, and returns the channel of
// its exit code, without waiting for it.
func (rl *reloadLive) stopping() <-chan int {
	rl.cancel()
	codes := make(chan int, 1)
	go func() { codes <- rl.wait() }()
	return codes
}

// exited waits, at most liveWait, for the exit code of stopping, and
// checks that it is 0 with every key released.
func (rl *reloadLive) exited(t *testing.T, codes <-chan int) string {
	t.Helper()
	select {
	case code := <-codes:
		if code != 0 {
			t.Errorf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, rl.stderr)
		}
	case <-time.After(liveWait):
		t.Fatalf("live mode did not stop within %v\nstdout:\n%s\nstderr:\n%s", liveWait, rl.stdout, rl.stderr)
	}
	out := rl.stdout.String()
	checkBalanced(t, out)
	return out
}

// waitText waits, at most liveWait, until buf has want. Unlike waitFor it
// does not sync the supervisor, whose slot may be the one waiting.
func waitText(t *testing.T, buf *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(liveWait)
	for !strings.Contains(buf.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q after %v:\n%s", want, liveWait, buf)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// waitClosed waits, at most liveWait, until ch is closed.
func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(liveWait):
		t.Fatalf("no %s within %v", what, liveWait)
	}
}

// gateWriter passes each write on to w, but the first that contains text
// closes entered and waits for release first.
type gateWriter struct {
	w                 io.Writer
	text              []byte
	once              sync.Once
	entered, released chan struct{}
	release           func() // closes released; it may be called more than once
}

func newGateWriter(w io.Writer, text string) *gateWriter {
	g := &gateWriter{w: w, text: []byte(text), entered: make(chan struct{}), released: make(chan struct{})}
	g.release = sync.OnceFunc(func() { close(g.released) })
	return g
}

func (g *gateWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, g.text) {
		g.once.Do(func() {
			close(g.entered)
			<-g.released
		})
	}
	return g.w.Write(p)
}

// gen returns the generation of slot 0.
func (rl *reloadLive) gen() uint32 { return rl.sup.Status().Slots[0].Gen }

// TestLiveReload: saving the profile while east holds Space reloads it
// (PRO-03). The reload releases Space once, the release of east then does
// nothing, and south maps with the new profile.
func TestLiveReload(t *testing.T) {
	rl := startReloadLive(t, profileZ)
	rl.button("east", true)
	rl.waitFor(t, rl.stdout, "send +Space\n")
	rl.write(t, profileY)
	rl.waitFor(t, rl.log, `"msg":"profile reloaded"`)
	line := logLine(rl.log.String(), "profile reloaded")
	for _, want := range []string{`"file":` + quoteJSON(rl.path), `"gen":2,`, `"warnings":0`} {
		if !strings.Contains(line, want) {
			t.Errorf("profile reloaded: %q, want %s", line, want)
		}
	}
	if g := rl.gen(); g != 2 {
		t.Errorf("slot 0 at gen %d, want 2", g)
	}
	rl.button("east", false)
	rl.tap(t, "Y", 1)
	out := rl.stop(t)

	lines := slices.Collect(strings.Lines(out))
	marker := slices.IndexFunc(lines, func(l string) bool { return strings.HasSuffix(l, "  profile reloaded: release all\n") })
	if marker < 0 {
		t.Fatalf("no reload line:\n%s", out)
	}
	var sends []string
	for _, l := range lines[marker+1:] {
		if f := strings.Fields(l); len(f) > 2 && f[1] == "send" {
			sends = append(sends, strings.Join(f[2:], " "))
		}
	}
	// Nothing for the release of east, nor at the exit.
	if want := []string{"-Space", "+LCtrl +Y", "-Y -LCtrl"}; !slices.Equal(sends, want) {
		t.Errorf("sends after the reload line %q, want %q\n%s", sends, want, out)
	}
	if n := strings.Count(out, "send -Space\n"); n != 1 {
		t.Errorf("%d releases of Space, want 1:\n%s", n, out)
	}
	if strings.Contains(out, "LShift") {
		t.Errorf("the reload pressed east again:\n%s", out)
	}
}

// TestLiveReloadInvalid: a save with a syntax error prints its
// diagnostics and keeps the profile running; the fixed save reloads.
func TestLiveReloadInvalid(t *testing.T) {
	rl := startReloadLive(t, profileZ)
	rl.write(t, profileBroken)
	rl.waitFor(t, rl.log, `"msg":"profile not reloaded"`)
	line := logLine(rl.log.String(), "profile not reloaded")
	for _, want := range []string{`"level":"ERROR"`, `"file":` + quoteJSON(rl.path), `"errors":1,`, `"first":"1:`} {
		if !strings.Contains(line, want) {
			t.Errorf("profile not reloaded: %q, want %s", line, want)
		}
	}
	if stderr := rl.stderr.String(); !strings.Contains(stderr, "\n"+rl.path+":1:") || !strings.Contains(stderr, ": error: ") {
		t.Errorf("stderr has no error at %s:1:\n%s", rl.path, stderr)
	}
	rl.tap(t, "Z", 1)
	if g := rl.gen(); g != 1 {
		t.Errorf("slot 0 at gen %d after the invalid save, want 1", g)
	}

	rl.write(t, profileY)
	rl.waitFor(t, rl.log, `"msg":"profile reloaded"`)
	rl.tap(t, "Y", 1)
	out := rl.stop(t)
	if n := strings.Count(out, "profile reloaded: release all\n"); n != 1 {
		t.Errorf("%d reload lines, want 1:\n%s", n, out)
	}
	if line := logLine(rl.log.String(), "profile reloaded"); !strings.Contains(line, `"gen":2,`) {
		t.Errorf("profile reloaded: %q, want gen 2", line)
	}
}

// TestLiveReloadUnchanged: a save that leaves the content as it runs, a
// touch or a revert after an invalid save, reloads nothing: east keeps
// Space held until the exit.
func TestLiveReloadUnchanged(t *testing.T) {
	rl := startReloadLive(t, profileZ)
	rl.button("east", true)
	rl.waitFor(t, rl.stdout, "send +Space\n")

	// A touch: the same bytes, a new modification time. The watcher reads
	// the file once it has settled, and finds what it read before.
	n := rl.reads.Load()
	rl.write(t, profileZ)
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(rl.path, later, later); err != nil {
		t.Fatal(err)
	}
	rl.waitReads(t, n+1)

	// A revert: the running content after an invalid save.
	rl.write(t, profileBroken)
	rl.waitFor(t, rl.log, `"msg":"profile not reloaded"`)
	rl.write(t, profileZ)
	rl.waitFor(t, rl.log, `"msg":"profile unchanged"`)
	if line := logLine(rl.log.String(), "profile unchanged"); !strings.Contains(line, `"level":"DEBUG"`) {
		t.Errorf("profile unchanged: %q, want it at debug level", line)
	}
	if g := rl.gen(); g != 1 {
		t.Errorf("slot 0 at gen %d, want 1", g)
	}
	out := rl.stdout.String()
	if logLine(rl.log.String(), "profile reloaded") != "" || strings.Contains(out, "profile reloaded") || strings.Contains(out, "-Space") {
		t.Errorf("an unchanged profile was reloaded:\nstdout:\n%s\nlog:\n%s", out, rl.log)
	}
	out = rl.stop(t)
	if i, j := strings.Index(out, "exit: release all\n"), strings.Index(out, "send -Space\n"); i < 0 || j < i {
		t.Errorf("Space was released before the exit:\n%s", out)
	}
}

// TestLiveWatchFlag: --watch=false watches nothing, and --watch is only
// for live mode.
func TestLiveWatchFlag(t *testing.T) {
	rl := startReloadLive(t, profileZ, "--watch=false")
	if rl.watched != nil {
		t.Error("--watch=false started a watcher")
	}
	rl.write(t, profileY)
	// Some polls and debounces of the watcher it would have.
	time.Sleep(100 * time.Millisecond)
	rl.tap(t, "Z", 1)
	if n := rl.reads.Load(); n != 0 {
		t.Errorf("%d reads of the profile, want none", n)
	}
	if out := rl.stop(t); strings.Contains(out, "profile reloaded") || logLine(rl.log.String(), "profile reloaded") != "" {
		t.Errorf("--watch=false reloaded the profile:\n%s", out)
	}

	code, stdout, stderr := runCmd(t, "--profile", basicProfile, "--fake-input", basicTrace, "--dry-run", "--watch=false")
	if want := "padmated: --watch is only for live mode, not with --fake-input\n" + usage; code != 2 || stdout != "" || stderr != want {
		t.Errorf("--watch=false --fake-input: exit %d, stdout %q, stderr %q; want 2, empty, %q", code, stdout, stderr, want)
	}
}

// TestLiveReloadStopping: live mode stops the watcher before the exit
// line. A save the watcher reads as live mode stops is not reloaded; a
// reload under way ends first, so its release comes before the exit line.
func TestLiveReloadStopping(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		rl := startReloadLive(t, profileZ)
		rl.button("east", true)
		rl.waitFor(t, rl.stdout, "send +Space\n")
		entered, released := make(chan struct{}), make(chan struct{})
		release := sync.OnceFunc(func() { close(released) })
		t.Cleanup(release) // before the exit path of begin
		hook := func() {
			close(entered)
			<-released
		}
		rl.onRead.Store(&hook)
		rl.write(t, profileY)
		waitClosed(t, entered, "read of the save")
		codes := rl.stopping()
		waitText(t, rl.stderr, "padmated: stopping\n")
		time.Sleep(20 * time.Millisecond) // for an exit line that would not wait
		if out := rl.stdout.String(); strings.Contains(out, "exit: release all") {
			t.Errorf("the exit line came while the watcher read:\n%s", out)
		}
		release()
		out := rl.exited(t, codes)
		checkStopped(t, rl, out)
		if strings.Contains(out, "profile reloaded") || logLine(rl.log.String(), "profile reloaded") != "" {
			t.Errorf("a save read while stopping was reloaded:\nstdout:\n%s\nlog:\n%s", out, rl.log)
		}
		lines := slices.Collect(strings.Lines(out))
		if exit, space := lineIndex(lines, "  exit: release all\n"), lineIndex(lines, "    send -Space\n"); exit < 0 || space < exit {
			t.Errorf("Space released before the exit line:\n%s", out)
		}
	})
	t.Run("reload", func(t *testing.T) {
		rl, cfg := newReloadLive(t, profileZ)
		// The slot notes the reload and waits there, and so does Reload.
		gate := newGateWriter(cfg.stdout, "profile reloaded: release all")
		cfg.stdout = gate
		rl.begin(t, cfg)
		t.Cleanup(gate.release) // before the exit path of begin
		rl.button("east", true)
		rl.waitFor(t, rl.stdout, "send +Space\n")
		rl.write(t, profileY)
		waitClosed(t, gate.entered, "reload line")
		codes := rl.stopping()
		waitText(t, rl.stderr, "padmated: stopping\n")
		time.Sleep(20 * time.Millisecond) // for an exit line that would not wait
		gate.release()
		out := rl.exited(t, codes)
		checkStopped(t, rl, out)
		lines := slices.Collect(strings.Lines(out))
		marker := lineIndex(lines, "  profile reloaded: release all\n")
		space := lineIndex(lines, "    send -Space\n")
		exit := lineIndex(lines, "  exit: release all\n")
		if marker < 0 || space < marker || exit < space {
			t.Errorf("want the reload line, the release of Space, then the exit line:\n%s", out)
		}
		if line := logLine(rl.log.String(), "profile reloaded"); !strings.Contains(line, `"gen":2,`) {
			t.Errorf("profile reloaded: %q, want gen 2\nlog:\n%s", line, rl.log)
		}
	})
}

// TestLiveReloadHung: a read of the profile that hangs as live mode stops
// holds up the exit for watchStop only. The exit path releases Space, and
// the read, once it returns, reloads nothing.
func TestLiveReloadHung(t *testing.T) {
	rl, cfg := newReloadLive(t, profileZ)
	cfg.watchStop = 50 * time.Millisecond
	rl.begin(t, cfg)
	rl.button("east", true)
	rl.waitFor(t, rl.stdout, "send +Space\n")
	entered, released := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(released) })
	t.Cleanup(release) // before the exit path of begin
	hook := func() {
		close(entered)
		<-released
	}
	rl.onRead.Store(&hook)
	rl.write(t, profileY)
	waitClosed(t, entered, "read of the save")

	out := rl.exited(t, rl.stopping())
	if n := strings.Count(out, "send -Space\n"); n != 1 {
		t.Errorf("%d releases of Space, want 1:\n%s", n, out)
	}
	if want := "padmated: the profile watcher did not stop within 50ms; stopping without it\n"; !strings.Contains(rl.stderr.String(), want) {
		t.Errorf("stderr has no %q:\n%s", want, rl.stderr)
	}
	if line := logLine(rl.log.String(), "profile watcher did not stop"); !strings.Contains(line, `"level":"WARN"`) || !strings.Contains(line, `"after":"50ms"`) {
		t.Errorf("profile watcher did not stop: %q, want a warning after 50ms\nlog:\n%s", line, rl.log)
	}
	select {
	case <-rl.watched:
		t.Error("the watcher stopped while its read hung")
	default:
	}

	release()
	waitClosed(t, rl.watched, "end of the watcher")
	if n := rl.reads.Load(); n != 2 {
		t.Errorf("%d reads of the profile, want 2", n)
	}
	if strings.Contains(rl.stdout.String(), "profile reloaded") || logLine(rl.log.String(), "profile reloaded") != "" {
		t.Errorf("the hung read reloaded the profile:\nstdout:\n%s\nlog:\n%s", rl.stdout, rl.log)
	}
}

// TestLiveWatchPanic: a panic of the watcher ends the watching only. It is
// printed and logged, and padmated maps on with the profile it has.
func TestLiveWatchPanic(t *testing.T) {
	rl := startReloadLive(t, profileZ)
	hook := func() { panic("boom") }
	rl.onRead.Store(&hook)
	rl.write(t, profileY)
	waitClosed(t, rl.watched, "end of the watcher")
	if want := "padmated: the profile watcher panicked: boom; saving the profile no longer reloads it\n"; !strings.Contains(rl.stderr.String(), want) {
		t.Errorf("stderr has no %q:\n%s", want, rl.stderr)
	}
	line := logLine(rl.log.String(), "profile watcher panicked")
	for _, want := range []string{`"level":"ERROR"`, `"err":"the profile watcher panicked: boom"`, `"stack":"goroutine `} {
		if !strings.Contains(line, want) {
			t.Errorf("profile watcher panicked: %q, want %s", line, want)
		}
	}
	rl.tap(t, "Z", 1)
	if g := rl.gen(); g != 1 {
		t.Errorf("slot 0 at gen %d, want 1", g)
	}
	out := rl.stop(t)
	if strings.Contains(out, "profile reloaded") || strings.Contains(out, "LCtrl +Y") {
		t.Errorf("the save reloaded the profile after the panic:\n%s", out)
	}
}

// checkStopped checks, after live mode stopped, that its watcher has
// stopped and that out releases Space once.
func checkStopped(t *testing.T, rl *reloadLive, out string) {
	t.Helper()
	select {
	case <-rl.watched:
	default:
		t.Error("the watcher still runs after live mode stopped")
	}
	if n := strings.Count(out, "send -Space\n"); n != 1 {
		t.Errorf("%d releases of Space, want 1:\n%s", n, out)
	}
}

// lineIndex returns the index of the first of lines that ends with
// suffix, or -1.
func lineIndex(lines []string, suffix string) int {
	return slices.IndexFunc(lines, func(l string) bool { return strings.HasSuffix(l, suffix) })
}

// TestReloadProfileRacesStop: a reload after Run has ended gets
// ErrStopped, which is logged at debug level only.
func TestReloadProfileRacesStop(t *testing.T) {
	rl := startReloadLive(t, profileZ, "--watch=false")
	rl.src.end(nil) // T-input stops: Run ends by itself
	select {
	case <-rl.ran:
	case <-time.After(liveWait):
		t.Fatalf("Run did not end within %v", liveWait)
	}
	data := []byte(profileY)
	rl.reloadProfile(context.Background(), watch.Change{Data: data, Sum: sha256.Sum256(data)})
	if line := logLine(rl.log.String(), "profile reload stopped: padmated is stopping"); !strings.Contains(line, `"level":"DEBUG"`) ||
		!strings.Contains(line, `"err":"supervisor: stopped"`) {
		t.Errorf("log %q, want the reload's ErrStopped at debug level\nlog:\n%s", line, rl.log)
	}
	if code := rl.waitCode(t); code != 1 {
		t.Errorf("exit %d after the input stopped, want 1", code)
	}
}

// quoteJSON returns s as a JSON string, as the log writes it.
func quoteJSON(s string) string {
	b, _ := json.Marshal(s) // a string always encodes
	return string(b)
}
