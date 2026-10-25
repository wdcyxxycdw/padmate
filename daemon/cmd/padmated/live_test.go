package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/logging"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

// The live tests need SDL: they skip without it, and fail with
// PADMATE_REQUIRE_SDL=1. They map a virtual gamepad, which --device
// selects, so that a real gamepad of the machine takes no slot.
const (
	liveTestPad = "padmated live test"
	// liveWait bounds each wait of the live tests.
	liveWait = 5 * time.Second
	// comboTestHold is the hold of the panic combo (DMN-07) in the tests
	// that hold it, instead of input.ComboHold's 2 s.
	comboTestHold = 100 * time.Millisecond
)

// syncBuffer is a bytes.Buffer that live mode's goroutines may write to
// while the test reads it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// testLive is live mode started by a test, with its output.
type testLive struct {
	*liveRun
	cancel              context.CancelFunc // stops live mode, as Ctrl+C does
	source              *input.Source      // src, with SDL
	session             *fakeSession       // the session watcher testConfig gives
	stdout, stderr, log *syncBuffer
}

// testEvents numbers the emergency events of testConfig.
var testEvents atomic.Int64

// testConfig returns the liveConfig of padmated run with --profile
// basicProfile --device liveTestPad and args, which may name another
// profile, the SDL library lib and newInject, writing to the buffers of
// tl. The log is at debug level. The session watcher is a fakeSession,
// tl.session, which reports nothing unless the test makes it. The event
// of padmatectl release-all is one of the test's own (DMN-20).
func testConfig(t *testing.T, tl *testLive, lib string, newInject func() (output.Sender, error), args ...string) liveConfig {
	t.Helper()
	args = append([]string{"--profile", basicProfile, "--device", liveTestPad}, args...)
	var stderr bytes.Buffer
	opts, _, ok := parseFlags(args, &stderr, &stderr)
	if !ok {
		t.Fatalf("parseFlags(%q): %s", args, stderr.String())
	}
	p, prog, sum, ok := loadProfile(opts.profile, &stderr)
	if !ok {
		t.Fatalf("loading %s: %s", opts.profile, stderr.String())
	}
	tl.stdout, tl.stderr, tl.log = &syncBuffer{}, &syncBuffer{}, &syncBuffer{}
	return liveConfig{
		opts: opts, profile: p, prog: prog, sum: sum, log: logging.New(tl.log, slog.LevelDebug),
		stdout: tl.stdout, stderr: tl.stderr,
		processes: func() ([]string, error) { return nil, nil },
		open: func(o input.Options) (eventSource, error) {
			o.Library = lib
			s, err := input.Open(o)
			if err != nil {
				return nil, err
			}
			tl.source = s
			return s, nil
		},
		newInject:     newInject,
		emergencyName: fmt.Sprintf(`Local\padmate.test-%d-%d`, os.Getpid(), testEvents.Add(1)),
		session: func(c winsession.Config) (sessionWatcher, error) {
			tl.session = newFakeSession(c)
			return tl.session, nil
		},
	}
}

// startTestLive starts live mode as testConfig configures it. It stops
// when the test ends, taking the exit path.
func startTestLive(t *testing.T, newInject func() (output.Sender, error), args ...string) *testLive {
	t.Helper()
	lib := inputtest.Library(t)
	tl := &testLive{}
	tl.start(t, testConfig(t, tl, lib, newInject, args...))
	return tl
}

// start starts live mode with cfg, which testConfig made for tl. It stops
// when the test ends, taking the exit path.
func (tl *testLive) start(t *testing.T, cfg liveConfig) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r, err := startLive(ctx, cfg)
	if err != nil {
		cancel()
		t.Fatalf("startLive: %v\nstderr:\n%s", err, tl.stderr)
	}
	tl.liveRun, tl.cancel = r, cancel
	t.Cleanup(func() {
		cancel()
		tl.waitCode(t)
	})
}

// attach attaches the virtual gamepad liveTestPad.
func (tl *testLive) attach(t *testing.T) *input.Virtual {
	t.Helper()
	v, err := tl.source.AttachVirtual(liveTestPad)
	if err != nil {
		t.Fatalf("AttachVirtual: %v", err)
	}
	return v
}

// waitFor waits until buf has want. A virtual gamepad's events reach
// Events asynchronously, so it syncs the supervisor between looks.
func (tl *testLive) waitFor(t *testing.T, buf *syncBuffer, want string) {
	t.Helper()
	tl.waitForCount(t, buf, want, 1)
}

// waitForCount waits, as waitFor does, until buf has want n times.
func (tl *testLive) waitForCount(t *testing.T, buf *syncBuffer, want string, n int) {
	t.Helper()
	tl.waitUntil(t, fmt.Sprintf("%d times %q", n, want), func() bool { return strings.Count(buf.String(), want) >= n })
}

// waitRow waits, as waitFor does, until a line of stdout has fields that
// match; what names the row in the failure.
func (tl *testLive) waitRow(t *testing.T, what string, match func(f []string) bool) {
	t.Helper()
	tl.waitUntil(t, "a row of "+what, func() bool {
		for l := range strings.Lines(tl.stdout.String()) {
			if match(strings.Fields(l)) {
				return true
			}
		}
		return false
	})
}

// waitUntil waits, at most liveWait, until cond holds, syncing the
// supervisor between looks; what names the wait in the failure.
func (tl *testLive) waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(liveWait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waited %v for %s\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", liveWait, what, tl.stdout, tl.stderr, tl.log)
		}
		tl.sync()
		time.Sleep(2 * time.Millisecond)
	}
}

// sync syncs the supervisor, at most a second: the events queued on Events
// have been handled, and the output they caused has run. A failed Sync
// shows as a missing row or text.
func (tl *testLive) sync() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = tl.sup.Sync(ctx)
}

// decisionRow matches the row of a decision of slot on control with edge,
// such as the fields "512.000 0 east down …" for "0", "east", "down".
func decisionRow(slot, control, edge string) func([]string) bool {
	return func(f []string) bool { return len(f) >= 4 && f[1] == slot && f[2] == control && f[3] == edge }
}

// waitCode waits, at most liveWait, for live mode to stop and returns its
// exit code.
func (tl *testLive) waitCode(t *testing.T) int {
	t.Helper()
	codes := make(chan int, 1)
	go func() { codes <- tl.wait() }()
	select {
	case code := <-codes:
		return code
	case <-time.After(liveWait):
		t.Fatalf("live mode did not stop within %v\nstderr:\n%s", liveWait, tl.stderr)
		return 0
	}
}

// padID returns v's device id as the rows and the log show it.
func padID(v *input.Virtual) string { return strconv.FormatUint(uint64(v.ID()), 10) }

// press sets a button of v.
func press(t *testing.T, v *input.Virtual, control string, down bool) {
	t.Helper()
	if err := v.SetButton(control, down); err != nil {
		t.Fatalf("SetButton(%s, %v): %v", control, down, err)
	}
}

// logLine returns the first line of log with message msg, or "" when
// there is none.
func logLine(log, msg string) string {
	if lines := logLines(log, msg); len(lines) > 0 {
		return lines[0]
	}
	return ""
}

// checkBalanced checks that the send lines of out release every key they
// press, each only while it is up, and release nothing else.
func checkBalanced(t *testing.T, out string) {
	t.Helper()
	held := map[string]bool{}
	for line := range strings.Lines(out) {
		f := strings.Fields(line)
		if len(f) < 3 || f[1] != "send" {
			continue
		}
		for _, e := range f[2:] {
			key := e[1:]
			switch {
			case e[0] == '+' && !held[key]:
				held[key] = true
			case e[0] == '-' && held[key]:
				delete(held, key)
			default:
				t.Errorf("%s while %s is held: %v, in %q", e, key, held[key], line)
			}
		}
	}
	for key := range held {
		t.Errorf("%s is still held at the end", key)
	}
}

// TestLiveVirtualDryRun maps a virtual gamepad in live dry-run mode, with
// the default 4 slots: a tap of Ctrl+Z on south, and Space held on east
// until the exit path releases it. Each row names its slot, and the idle
// slots release nothing on the exit path.
func TestLiveVirtualDryRun(t *testing.T) {
	tl := startTestLive(t, nil, "--dry-run")
	v := tl.attach(t)
	tl.waitFor(t, tl.stdout, "device "+padID(v)+` "padmated live test" added to slot 0`)
	press(t, v, "south", true)
	press(t, v, "south", false)
	// The Resolver gives each key its name on any layout.
	tl.waitFor(t, tl.stdout, "send +LCtrl +Z\n")
	tl.waitFor(t, tl.stdout, "send -Z -LCtrl\n")
	press(t, v, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space\n")
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
	}

	out := tl.stdout.String()
	wantHeader := "padmated: dry run, nothing is injected\n" +
		"profile:    ../../../examples/profiles/basic.json, \"绘画\", 7 bindings\n" +
		"gamepads:   of those whose name or device key contains \"padmated live test\", up to 4 are mapped at once, " +
		"each on its own slot; the others wait; Ctrl+C stops\n\n"
	if !strings.HasPrefix(out, wantHeader) {
		t.Errorf("stdout does not start with the header %q:\n%s", wantHeader, out)
	}
	lines := slices.Collect(strings.Lines(out))
	if got, want := strings.Fields(lines[4]), []string{"time", "slot", "input", "reason", "action", "label", "binding", "owner"}; !slices.Equal(got, want) {
		t.Errorf("line 5 is %q, want the column names", lines[4])
	}
	// The exit path cancels the press of east on slot 0, and its release
	// follows; the idle slots have nothing to release.
	exit := slices.IndexFunc(lines, func(l string) bool { return strings.HasSuffix(l, "  exit: release all\n") })
	switch {
	case exit < 0:
		t.Errorf("no exit line:\n%s", out)
	case len(lines) != exit+3:
		t.Errorf("%d lines after the exit line, want 2:\n%s", len(lines)-exit-1, out)
	default:
		if f := strings.Fields(lines[exit+1]); len(f) < 4 || f[1] != "0" || f[2] != "east" || f[3] != "cancel" {
			t.Errorf("after the exit line %q, want the cancel of east on slot 0", lines[exit+1])
		}
		if f := strings.Fields(lines[exit+2]); !slices.Equal(f[1:], []string{"send", "-Space"}) {
			t.Errorf("last line %q, want the release of Space", lines[exit+2])
		}
	}
	checkBalanced(t, out)

	stderr := tl.stderr.String()
	for _, want := range []string{"padmated: SDL ", "padmated: preset default; hints in effect:\n", "padmated: stopping\n"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr has no %q:\n%s", want, stderr)
		}
	}
	log := tl.log.String()
	for _, msg := range []string{"padmated starting", "device connected", "slot assigned", "padmated stopping"} {
		if logLine(log, msg) == "" {
			t.Errorf("log has no %q:\n%s", msg, log)
		}
	}
	if line := logLine(log, "padmated starting"); !strings.Contains(line, `"slots":4`) {
		t.Errorf("padmated starting: %q, want slots 4", line)
	}
	if line := logLine(log, "padmated stopped"); !strings.Contains(line, `"code":0`) {
		t.Errorf("padmated stopped: %q, want code 0", line)
	}
}

// profileAlt holds Alt on south: the profile of
// TestLiveVirtualDryRunMenuMask.
const profileAlt = `{"schemaVersion": 0, "name": "menu mask", "layers": {"base": {` +
	`"south": {"press": {"type": "keys", "keys": "Alt", "mode": "hold"}}}}}` + "\n"

// TestLiveVirtualDryRunMenuMask holds Alt alone on a virtual gamepad in
// live dry-run mode: the printer is below the Loop's MenuMask, so the
// release shows the mask key's press and release before it (OUT-08), as
// does the release of Alt on the exit path.
func TestLiveVirtualDryRunMenuMask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(profileAlt), 0o644); err != nil {
		t.Fatal(err)
	}
	tl := startTestLive(t, nil, "--profile", path, "--dry-run")
	v := tl.attach(t)
	tl.waitFor(t, tl.stdout, "device "+padID(v)+` "padmated live test" added to slot 0`)
	press(t, v, "south", true)
	press(t, v, "south", false)
	tl.waitFor(t, tl.stdout, "send +vk:0xE8 -vk:0xE8 -LAlt\n")
	press(t, v, "south", true)
	tl.waitForCount(t, tl.stdout, "send +LAlt\n", 2)
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
	}

	out := tl.stdout.String()
	var sends []string
	for line := range strings.Lines(out) {
		if f := strings.Fields(line); len(f) > 2 && f[1] == "send" {
			sends = append(sends, strings.Join(f[2:], " "))
		}
	}
	if want := []string{"+LAlt", "+vk:0xE8 -vk:0xE8 -LAlt", "+LAlt", "+vk:0xE8 -vk:0xE8 -LAlt"}; !slices.Equal(sends, want) {
		t.Errorf("send lines %q, want %q:\n%s", sends, want, out)
	}
	// The exit path cancels the press of south, and its masked release
	// follows.
	lines := slices.Collect(strings.Lines(out))
	exit := slices.IndexFunc(lines, func(l string) bool { return strings.HasSuffix(l, "  exit: release all\n") })
	switch {
	case exit < 0:
		t.Errorf("no exit line:\n%s", out)
	case len(lines) != exit+3:
		t.Errorf("%d lines after the exit line, want 2:\n%s", len(lines)-exit-1, out)
	default:
		if f := strings.Fields(lines[exit+1]); len(f) < 4 || f[1] != "0" || f[2] != "south" || f[3] != "cancel" {
			t.Errorf("after the exit line %q, want the cancel of south on slot 0", lines[exit+1])
		}
		if f := strings.Fields(lines[exit+2]); !slices.Equal(f[1:], []string{"send", "+vk:0xE8", "-vk:0xE8", "-LAlt"}) {
			t.Errorf("last line %q, want the masked release of LAlt", lines[exit+2])
		}
	}
	checkBalanced(t, out)
}

// TestLiveVirtualReconnect unplugs a virtual gamepad that holds east, and
// plugs in one with the same name, so the same device key: it is mapped
// again, reconnected (IN-05). Then a second gamepad waits, and holds east
// when the first leaves: it stands in, with Space pressed. The first comes
// back before the stand-in presses a button, and takes the slot back,
// provisionally until its press: Space is released, and the stand-in
// waits again. It runs with one slot, the line of IN-05; the router tests
// cover the rules with several.
func TestLiveVirtualReconnect(t *testing.T) {
	tl := &testLive{}
	cfg := testConfig(t, tl, inputtest.Library(t), nil, "--dry-run")
	cfg.slots = 1
	tl.start(t, cfg)
	v := tl.attach(t)
	tl.waitFor(t, tl.stdout, "device "+padID(v)+` "padmated live test" added to slot 0`)
	press(t, v, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space\n")
	detach(t, v)
	tl.waitFor(t, tl.stdout, "device "+padID(v)+` "padmated live test" removed from slot 0: release all`)
	tl.waitFor(t, tl.stdout, "send -Space\n")

	v = tl.attach(t)
	tl.waitFor(t, tl.stdout, "device "+padID(v)+` "padmated live test" added to slot 0`)
	press(t, v, "south", true)
	press(t, v, "south", false)
	tl.waitFor(t, tl.stdout, "send +LCtrl +Z\n")
	tl.waitFor(t, tl.stdout, "send -Z -LCtrl\n")
	assigned := logLines(tl.log.String(), "slot assigned")
	if len(assigned) != 2 || !strings.Contains(assigned[1], `"id":`+padID(v)+`,`) ||
		!strings.Contains(assigned[1], `"reconnected":true`) || !strings.Contains(assigned[1], `"handoff":false`) {
		t.Errorf("slot assigned lines %q, want the second reconnected, not a hand-off", assigned)
	}

	// Not liveTestPad+" 2": SDL names that one "padmated live test", as if
	// by the mapping of the first, whose GUID differs only in the CRC.
	w, err := tl.source.AttachVirtual("2nd " + liveTestPad)
	if err != nil {
		t.Fatalf("AttachVirtual: %v", err)
	}
	tl.waitFor(t, tl.log, `"msg":"gamepad waiting: every slot is taken","id":`+padID(w)+",")
	press(t, w, "east", true) // before the removal: T-input keeps the order
	detach(t, v)
	tl.waitFor(t, tl.stdout, "device "+padID(w)+` "2nd padmated live test" added to slot 0`)
	tl.waitForCount(t, tl.stdout, "send +Space\n", 2)

	x := tl.attach(t)
	tl.waitFor(t, tl.stdout, "device "+padID(w)+` "2nd padmated live test" removed from slot 0: release all`)
	tl.waitForCount(t, tl.stdout, "send -Space\n", 2)
	tl.waitFor(t, tl.stdout, "device "+padID(x)+` "padmated live test" added to slot 0`)
	press(t, w, "east", false) // w waits: nothing to send
	press(t, x, "south", true)
	press(t, x, "south", false)
	tl.waitForCount(t, tl.stdout, "send +LCtrl +Z\n", 2)
	tl.waitForCount(t, tl.stdout, "send -Z -LCtrl\n", 2)
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
	}
	out := tl.stdout.String()
	checkBalanced(t, out)
	if n := strings.Count(out, "send -Space\n"); n != 2 {
		t.Errorf("%d releases of Space, want 2:\n%s", n, out)
	}
	if i, j := strings.Index(out, "device "+padID(v)+` "padmated live test" removed`), strings.Index(out, "device "+padID(w)+" "); i < 0 || j < i {
		t.Errorf("the 2nd pad was added to the slot before the first was removed:\n%s", out)
	}
	log := tl.log.String()
	if line := logLine(log, "slot reclaimed"); !strings.Contains(line, `"id":`+padID(x)+`,`) || !strings.Contains(line, `"holder":`+padID(w)+`,`) {
		t.Errorf("slot reclaimed: %q, want id %s from holder %s", line, padID(x), padID(w))
	}
	assigned = logLines(log, "slot assigned")
	switch {
	case len(assigned) != 4:
		t.Errorf("slot assigned lines %q, want 4", assigned)
	case !strings.Contains(assigned[2], `"handoff":true`) || !strings.Contains(assigned[2], `"provisional":true`):
		t.Errorf("slot assigned: %q, want a provisional hand-off", assigned[2])
	case !strings.Contains(assigned[3], `"id":`+padID(x)+`,`) || !strings.Contains(assigned[3], `"reconnected":true`) ||
		!strings.Contains(assigned[3], `"handoff":false`) || !strings.Contains(assigned[3], `"provisional":true`):
		t.Errorf("slot assigned: %q, want %s reconnected, provisional, not a hand-off", assigned[3], padID(x))
	}
	if line := logLines(log, "slot confirmed"); len(line) == 0 || !strings.Contains(line[len(line)-1], `"id":`+padID(x)+`,`) {
		t.Errorf("slot confirmed lines %q, want the last of %s", line, padID(x))
	}
}

// TestLiveVirtualTakeOver, with one slot: an idle gamepad connects first
// and takes the slot, provisionally; the gamepad in hand connects after it
// and waits. Its first press takes the slot over and is mapped (IN-05), and
// the idle one's presses then map nothing. The header and the rows have
// no slots then. With padmated's 4 slots the idle one keeps a slot of its
// own, and is mapped (ROADMAP §5.2).
func TestLiveVirtualTakeOver(t *testing.T) {
	tl := &testLive{}
	cfg := testConfig(t, tl, inputtest.Library(t), nil, "--dry-run")
	cfg.slots = 1
	tl.start(t, cfg)
	// Not liveTestPad+" 2": see TestLiveVirtualReconnect.
	idle, err := tl.source.AttachVirtual("idle " + liveTestPad)
	if err != nil {
		t.Fatalf("AttachVirtual: %v", err)
	}
	tl.waitFor(t, tl.stdout, "device "+padID(idle)+` "idle padmated live test" added to slot 0`)
	v := tl.attach(t)
	tl.waitFor(t, tl.log, `"msg":"gamepad waiting: every slot is taken","id":`+padID(v)+",")
	press(t, v, "south", true)
	press(t, v, "south", false)
	tl.waitFor(t, tl.stdout, "device "+padID(v)+` "padmated live test" added to slot 0`)
	tl.waitFor(t, tl.stdout, "send +LCtrl +Z\n")
	tl.waitFor(t, tl.stdout, "send -Z -LCtrl\n")
	// The idle one waits now: its tap sends nothing. T-input keeps the
	// order, so v's east comes after it.
	press(t, idle, "south", true)
	press(t, idle, "south", false)
	press(t, v, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space\n")
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
	}
	out := tl.stdout.String()
	checkBalanced(t, out)
	if n := strings.Count(out, "send +LCtrl +Z\n"); n != 1 {
		t.Errorf("%d taps of Ctrl+Z, want 1: the idle gamepad's maps nothing\n%s", n, out)
	}
	if i, j := strings.Index(out, "device "+padID(idle)+` "idle padmated live test" removed from slot 0: release all`),
		strings.Index(out, "device "+padID(v)+` "padmated live test" added`); i < 0 || j < i {
		t.Errorf("the pad in hand was added to the slot before the idle one was removed:\n%s", out)
	}
	// One slot: the header and the columns have no slots.
	lines := slices.Collect(strings.Lines(out))
	if want := "gamepads:   of those whose name or device key contains \"padmated live test\", one is mapped at a time; " +
		"the first to press a button keeps it; Ctrl+C stops\n"; len(lines) < 5 || lines[2] != want {
		t.Errorf("line 3 is not %q:\n%s", want, out)
	} else if got, want := strings.Fields(lines[4]), []string{"time", "input", "reason", "action", "label", "binding", "owner"}; !slices.Equal(got, want) {
		t.Errorf("line 5 is %q, want the column names", lines[4])
	}
	log := tl.log.String()
	if line := logLine(log, "padmated starting"); !strings.Contains(line, `"slots":1`) {
		t.Errorf("padmated starting: %q, want slots 1", line)
	}
	if line := logLine(log, "slot taken over"); !strings.Contains(line, `"id":`+padID(v)+`,`) || !strings.Contains(line, `"holder":`+padID(idle)+`,`) {
		t.Errorf("slot taken over: %q, want id %s from holder %s", line, padID(v), padID(idle))
	}
	assigned := logLines(log, "slot assigned")
	if len(assigned) != 2 || !strings.Contains(assigned[0], `"provisional":true`) ||
		!strings.Contains(assigned[1], `"handoff":true`) || !strings.Contains(assigned[1], `"provisional":false`) {
		t.Errorf("slot assigned lines %q, want the idle one provisional, then a take-over", assigned)
	}
}

// TestLiveVirtualTwoPads maps two virtual gamepads at once, each on a slot
// of its own (IN-08), and each row names the slot of its pad. The pads
// share the keyboard: Space held by both goes down once, and up once the
// second lets go. That second release is by unplugging the pad, but SDL
// lifts east before it reports the removal (docs/PITFALLS.md "SDL 行为"), so it
// is the release of the button; the supervisor's TestSlotsSharedKey and
// TestSlotsRehomeReleases cover the release by a removal.
func TestLiveVirtualTwoPads(t *testing.T) {
	tl := startTestLive(t, nil, "--dry-run")
	a := tl.attach(t)
	tl.waitFor(t, tl.stdout, "device "+padID(a)+` "padmated live test" added to slot 0`)
	// Not liveTestPad+" 2": see TestLiveVirtualReconnect.
	b, err := tl.source.AttachVirtual("2nd " + liveTestPad)
	if err != nil {
		t.Fatalf("AttachVirtual: %v", err)
	}
	tl.waitFor(t, tl.stdout, "device "+padID(b)+` "2nd padmated live test" added to slot 1`)

	press(t, a, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space\n")
	// The second hold of Space sends nothing. The row comes before the
	// output the decision causes, and Sync waits for that.
	press(t, b, "east", true)
	tl.waitRow(t, "slot 1's press of east", decisionRow("1", "east", "down"))
	tl.sync()
	if n := strings.Count(tl.stdout.String(), "send +Space\n"); n != 1 {
		t.Fatalf("%d presses of Space once both pads hold east, want 1:\n%s", n, tl.stdout)
	}
	press(t, a, "east", false)
	tl.waitRow(t, "slot 0's release of east", decisionRow("0", "east", "up"))
	tl.sync()
	if strings.Contains(tl.stdout.String(), "send -Space\n") {
		t.Fatalf("Space released while the 2nd pad holds east:\n%s", tl.stdout)
	}
	detach(t, b)
	removed := "device " + padID(b) + ` "2nd padmated live test" removed from slot 1: release all`
	tl.waitFor(t, tl.stdout, removed)
	// A short physical press may still be waiting for MinPress when SDL
	// reports the removal. The removal must finish that release immediately.
	tl.sync()
	if out := tl.stdout.String(); strings.Count(out, "send -Space\n") != 1 {
		t.Errorf("removal did not release Space exactly once:\n%s", out)
	}

	// Pad a maps as before.
	press(t, a, "south", true)
	press(t, a, "south", false)
	tl.waitFor(t, tl.stdout, "send +LCtrl +Z\n")
	tl.waitFor(t, tl.stdout, "send -Z -LCtrl\n")
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
	}
	out := tl.stdout.String()
	checkBalanced(t, out)
	if got, want := sendEdges(out), []string{"+Space", "-Space", "+LCtrl", "+Z", "-Z", "-LCtrl"}; !slices.Equal(got, want) {
		t.Errorf("edges sent %q, want %q:\n%s", got, want, out)
	}
	// Every decision row, of a button's edge, is on its pad's slot.
	got := map[string][]string{}
	for line := range strings.Lines(out) {
		if f := strings.Fields(line); len(f) >= 4 && (f[3] == "down" || f[3] == "up") {
			got[f[1]] = append(got[f[1]], f[2]+" "+f[3])
		}
	}
	want := map[string][]string{"0": {"east down", "east up", "south down", "south up"}, "1": {"east down", "east up"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("edges by slot %q, want %q:\n%s", got, want, out)
	}
}

// TestLiveVirtualFivePads fills the 4 slots with idle virtual gamepads,
// each provisional until its first press; a fifth waits, and its press
// takes over the slot assigned longest ago, slot 0 (IN-08), and is mapped.
func TestLiveVirtualFivePads(t *testing.T) {
	tl := startTestLive(t, nil, "--dry-run")
	// Names whose first 9 bytes differ: see TestLiveVirtualReconnect. Each
	// is attached once the one before has its slot, so they get slots 0 to
	// 3 in order.
	var pads []*input.Virtual
	names := []string{liveTestPad, "2nd " + liveTestPad, "3rd " + liveTestPad, "4th " + liveTestPad, "5th " + liveTestPad}
	for i, name := range names {
		v, err := tl.source.AttachVirtual(name)
		if err != nil {
			t.Fatalf("AttachVirtual: %v", err)
		}
		pads = append(pads, v)
		if i < supervisor.MaxSlots {
			tl.waitFor(t, tl.stdout, "device "+padID(v)+" "+strconv.Quote(name)+" added to slot "+strconv.Itoa(i))
		}
	}
	fifth := pads[4]
	tl.waitFor(t, tl.log, `"msg":"gamepad waiting: every slot is taken","id":`+padID(fifth)+",")
	press(t, fifth, "south", true)
	press(t, fifth, "south", false)
	tl.waitFor(t, tl.stdout, "device "+padID(fifth)+` "5th padmated live test" added to slot 0`)
	tl.waitFor(t, tl.stdout, "send +LCtrl +Z\n")
	tl.waitFor(t, tl.stdout, "send -Z -LCtrl\n")
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
	}
	out := tl.stdout.String()
	checkBalanced(t, out)
	removed := "device " + padID(pads[0]) + ` "padmated live test" removed from slot 0: release all`
	if i, j := strings.Index(out, removed), strings.Index(out, "device "+padID(fifth)+` "5th padmated live test" added`); i < 0 || j < i {
		t.Errorf("the 5th pad was added before the first was removed from slot 0:\n%s", out)
	}
	// The others keep their slots.
	for i := 1; i < supervisor.MaxSlots; i++ {
		if note := "device " + padID(pads[i]) + " " + strconv.Quote(names[i]) + " removed"; strings.Contains(out, note) {
			t.Errorf("%q: the take-over took another slot:\n%s", note, out)
		}
	}
	if line := logLine(tl.log.String(), "slot taken over"); !strings.Contains(line, `"slot":0,`) ||
		!strings.Contains(line, `"id":`+padID(fifth)+`,`) || !strings.Contains(line, `"holder":`+padID(pads[0])+`,`) {
		t.Errorf("slot taken over: %q, want slot 0 for %s from %s", line, padID(fifth), padID(pads[0]))
	}
}

// TestLiveVirtualJoyCon maps virtual Joy-Cons under padmate's hints
// (IN-07): with COMBINE=0 each is a gamepad of its own, so --device
// selects L alone, and R's presses map nothing. It models SDL's mapping
// of a sideways Joy-Con (inputtest.JoyConLeft), not the device. L's SL
// and SR hold Ctrl and Shift, and SDL's recentering at the removal
// releases them, in button order, before the note of the removal.
func TestLiveVirtualJoyCon(t *testing.T) {
	inputtest.Unsetenv(t, input.HintCombineJoyCons, input.HintVerticalJoyCons)
	tl := startTestLive(t, nil, "--dry-run", "--device", "virtual Joy-Con (L)")
	stderr := tl.stderr.String()
	for _, want := range []string{"padmated:   SDL_JOYSTICK_HIDAPI_COMBINE_JOY_CONS=0\n",
		"padmated:   SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS unset (SDL's default)\n"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr has no %q:\n%s", want, stderr)
		}
	}
	r, err := tl.source.AttachVirtualDesc(inputtest.JoyConRight("padmate virtual Joy-Con (R)"))
	if err != nil {
		t.Fatalf("AttachVirtualDesc(R): %v", err)
	}
	tl.waitFor(t, tl.log, `"msg":"gamepad ignored: it does not match the device filter","id":`+padID(r)+",")
	l, err := tl.source.AttachVirtualDesc(inputtest.JoyConLeft("padmate virtual Joy-Con (L)"))
	if err != nil {
		t.Fatalf("AttachVirtualDesc(L): %v", err)
	}
	added := "device " + padID(l) + ` "padmate virtual Joy-Con (L)" added to slot 0`
	tl.waitFor(t, tl.stdout, added)
	press(t, l, "south", true)
	press(t, l, "south", false)
	tl.waitFor(t, tl.stdout, "send +LCtrl +Z\n")
	tl.waitFor(t, tl.stdout, "send -Z -LCtrl\n")

	// L and ZL are unbound in basic.json, and R has no slot.
	before := len(sendEdges(tl.stdout.String()))
	for _, p := range []struct {
		v       *input.Virtual
		control string
	}{{l, "paddle2"}, {l, "paddle4"}, {r, "south"}, {r, "leftshoulder"}} {
		press(t, p.v, p.control, true)
		press(t, p.v, p.control, false)
	}
	ctx, cancel := context.WithTimeout(context.Background(), liveWait)
	defer cancel()
	if err := tl.sup.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if n := len(sendEdges(tl.stdout.String())); n != before {
		t.Errorf("%d edges sent after the unbound presses, want %d:\n%s", n, before, tl.stdout)
	}
	// T-input keeps the order of the presses, so SL's edge also comes after
	// every edge they could have caused.
	press(t, l, "leftshoulder", true)
	tl.waitFor(t, tl.stdout, "send +LCtrl\n")
	if got := sendEdges(tl.stdout.String())[before:]; !slices.Equal(got, []string{"+LCtrl"}) {
		t.Errorf("edges sent after the unbound presses and SL: %q, want only +LCtrl", got)
	}
	press(t, l, "rightshoulder", true)
	tl.waitFor(t, tl.stdout, "send +LShift\n")

	detach(t, l)
	removed := "device " + padID(l) + ` "padmate virtual Joy-Con (L)" removed from slot 0: release all`
	tl.waitFor(t, tl.stdout, removed)
	tl.sync()
	beforeExit := tl.stdout.String()
	shift := strings.Index(beforeExit, "send +LShift\n")
	if shift < 0 {
		t.Fatalf("no Shift press:\n%s", beforeExit)
	}
	releases := sendEdges(beforeExit[shift+len("send +LShift\n"):])
	slices.Sort(releases)
	if !slices.Equal(releases, []string{"-LCtrl", "-LShift"}) {
		t.Fatalf("removal left modifiers held or sent extra keys: %q\n%s", releases, beforeExit)
	}
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
	}
	out := tl.stdout.String()
	checkBalanced(t, out)
	if strings.Contains(out, `"padmate virtual Joy-Con (R)" added to slot`) {
		t.Errorf("R was added to a slot:\n%s", out)
	}
	// SDL recentres both buttons before removal. MinPress can defer their
	// physical releases to removal, which releases in reverse press order.
	note := strings.Index(out, removed)
	if shift < 0 || note < shift {
		t.Fatalf("no %q before %q:\n%s", "send +LShift", removed, out)
	}
	if got := sendEdges(out[len(beforeExit):]); len(got) != 0 {
		t.Errorf("exit sent edges after removal already released everything: %q\n%s", got, out)
	}
	if !strings.Contains(out[note:], "  exit: release all\n") {
		t.Errorf("no exit line after the removal:\n%s", out)
	}
}

// comboConfig returns the liveConfig of testConfig, with the hold of the
// panic combo comboTestHold.
func comboConfig(t *testing.T, tl *testLive, lib string, args ...string) liveConfig {
	t.Helper()
	cfg := testConfig(t, tl, lib, nil, args...)
	open := cfg.open
	cfg.open = func(o input.Options) (eventSource, error) {
		o.ComboHold = comboTestHold
		return open(o)
	}
	return cfg
}

// pausedRow matches the row of a press of control on slot that the pause
// maps to nothing.
func pausedRow(slot, control string) func([]string) bool {
	return func(f []string) bool {
		return len(f) >= 5 && f[1] == slot && f[2] == control && f[3] == "down" && f[4] == "paused"
	}
}

// TestLiveVirtualCombo: a virtual gamepad holds back and start, its panic
// combo (DMN-07), for comboTestHold: the mapping pauses, and Space, held
// on east, is released. Presses map to nothing until the combo is held
// again, which resumes the mapping; east, pressed while paused and held
// through the resume, does nothing until it is pressed again. It follows
// the supervisor's golden test panic-combo. T-input's fire releases Space
// before the router pauses, so the release may come before the pause's
// note.
func TestLiveVirtualCombo(t *testing.T) {
	tl := &testLive{}
	tl.start(t, comboConfig(t, tl, inputtest.Library(t), "--dry-run"))
	v := tl.attach(t)
	tl.waitFor(t, tl.stdout, "device "+padID(v)+` "padmated live test" added to slot 0`)
	press(t, v, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space\n")
	press(t, v, "back", true)
	press(t, v, "start", true)
	tl.waitFor(t, tl.stdout, "  mapping paused: release all\n")
	tl.waitFor(t, tl.stdout, "send -Space\n")
	tl.waitRow(t, "the cancel of east", decisionRow("0", "east", "cancel"))

	press(t, v, "back", false)
	press(t, v, "start", false)
	press(t, v, "east", false)
	press(t, v, "south", true)
	press(t, v, "south", false)
	tl.waitRow(t, "the press of south while paused", pausedRow("0", "south"))
	press(t, v, "east", true)
	tl.waitRow(t, "the press of east while paused", pausedRow("0", "east"))
	press(t, v, "back", true)
	press(t, v, "start", true)
	tl.waitFor(t, tl.stdout, "  mapping resumed\n")
	press(t, v, "back", false)
	press(t, v, "start", false)
	press(t, v, "east", false) // held through the resume: nothing
	press(t, v, "east", true)
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
	paused, resumed := strings.Index(out, "  mapping paused: release all\n"), strings.Index(out, "  mapping resumed\n")
	if paused < 0 || resumed < paused || strings.Count(out, "  mapping ") != 2 {
		t.Fatalf("want one pause, then one resume:\n%s", out)
	}
	if got := sendEdges(out[paused:resumed]); len(got) > 1 || len(got) == 1 && got[0] != "-Space" {
		t.Errorf("edges sent while paused %q, want at most the release of Space:\n%s", got, out)
	}
	if !slices.ContainsFunc(slices.Collect(strings.Lines(out[resumed:])), func(l string) bool {
		f := strings.Fields(l)
		return len(f) >= 5 && f[1] == "0" && f[2] == "east" && f[3] == "up" && f[4] == "canceled"
	}) {
		t.Errorf("no canceled release of east after the resume:\n%s", out)
	}
	log := tl.log.String()
	for _, msg := range []string{"mapping paused", "mapping resumed"} {
		if lines := logLines(log, msg); len(lines) != 1 || !strings.Contains(lines[0], `"by":"combo","id":`+padID(v)) {
			t.Errorf("%s lines %q, want one, by the combo of %s", msg, lines, padID(v))
		}
	}
}

// sendEdges returns the key edges of the send lines of out, in order.
func sendEdges(out string) []string {
	var edges []string
	for line := range strings.Lines(out) {
		if f := strings.Fields(line); len(f) >= 3 && f[1] == "send" {
			edges = append(edges, f[2:]...)
		}
	}
	return edges
}

// detach detaches v.
func detach(t *testing.T, v *input.Virtual) {
	t.Helper()
	if err := v.Detach(); err != nil {
		t.Fatalf("Detach: %v", err)
	}
}

// logLines returns the lines of log with message msg.
func logLines(log, msg string) []string {
	var lines []string
	for line := range strings.Lines(log) {
		if strings.Contains(line, `"msg":`+strconv.Quote(msg)) {
			lines = append(lines, line)
		}
	}
	return lines
}

// TestLiveFlagsSmoke runs live mode from the command line, with a preset,
// a hint and a log file: stderr lists the hint, and padmate's own under
// the preset, and the log file has the run.
func TestLiveFlagsSmoke(t *testing.T) {
	inputtest.Unsetenv(t, input.HintCombineJoyCons, input.HintVerticalJoyCons)
	lib := inputtest.Library(t)
	if filepath.IsAbs(lib) {
		t.Setenv(input.EnvLibrary, lib) // not beside the test binary
	}
	logFile := filepath.Join(t.TempDir(), "logs", "padmated.log")
	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr syncBuffer
	var code int
	done := make(chan struct{})
	go func() {
		defer close(done)
		code = runContext(ctx, []string{"--profile", basicProfile, "--dry-run", "--device", liveTestPad,
			"--preset", "xinput", "--hint", "SDL_PADMATE_TEST=1", "--log-level", "debug", "--log-file", logFile}, &stdout, &stderr)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(liveWait):
			t.Errorf("padmated did not stop within %v", liveWait)
		}
	})
	deadline := time.After(liveWait)
	for !strings.Contains(stdout.String(), "  owner\n") { // the column names
		select {
		case <-done:
			t.Fatalf("exit %d before the header\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
		case <-deadline:
			t.Fatalf("no header after %v\nstdout:\n%s\nstderr:\n%s", liveWait, stdout.String(), stderr.String())
		case <-time.After(2 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(liveWait):
		t.Fatalf("padmated did not stop within %v", liveWait)
	}
	if code != 0 {
		t.Errorf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, stderr.String())
	}
	for _, want := range []string{"padmated: preset xinput; hints in effect:\n", "padmated:   SDL_PADMATE_TEST=1\n",
		"padmated:   SDL_JOYSTICK_HIDAPI_COMBINE_JOY_CONS=0\n", "padmated: stopping\n"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr has no %q:\n%s", want, stderr.String())
		}
	}
	if !strings.HasPrefix(stdout.String(), "padmated: dry run, nothing is injected\n") {
		t.Errorf("stdout does not start with the header:\n%s", stdout.String())
	}
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	if line := logLine(log, "padmated starting"); !strings.Contains(line, `"mode":"dry-run"`) || !strings.Contains(line, `"preset":"xinput"`) {
		t.Errorf("padmated starting: %q, want mode dry-run, preset xinput\nlog:\n%s", line, log)
	}
	if line := logLine(log, "padmated stopped"); !strings.Contains(line, `"code":0`) {
		t.Errorf("padmated stopped: %q, want code 0", line)
	}
	// The real session watcher (DMN-09) started, or there is none.
	if line := logLine(log, "session events off"); line != "" || strings.Contains(stderr.String(), "session events") {
		t.Errorf("session events off: %q\nstderr:\n%s", line, stderr.String())
	}
}

// TestLiveInputStops: when T-input stops by itself, live mode takes the
// exit path and exits 1.
func TestLiveInputStops(t *testing.T) {
	tl := startTestLive(t, nil, "--dry-run")
	tl.source.Close()
	if code := tl.waitCode(t); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if want := "padmated: the SDL input stopped\n"; !strings.HasSuffix(tl.stderr.String(), want) {
		t.Errorf("stderr does not end with %q:\n%s", want, tl.stderr)
	}
	if line := logLine(tl.log.String(), "padmated stopped"); !strings.Contains(line, `"code":1`) {
		t.Errorf("padmated stopped: %q, want code 1", line)
	}
}

// fakeSource is an eventSource that the test feeds, for what a test of
// padmated cannot make SDL do: Events closes with Err set, as after a
// panic on T-input.
type fakeSource struct {
	events chan input.Event
	// effects are the effect calls the source received: which DeviceID got
	// what. Nothing reaches a real controller.
	effects effectLog
	mu      sync.Mutex
	err     error
	closed  bool
	// onClose, when set, is called by Close first, on the goroutine of
	// the exit path.
	onClose func()
	// cues are the identify cues each gamepad can give (setCues); one
	// without an entry can give none. identifyErr is what Identify fails
	// with, when set.
	cues        map[input.DeviceID]haptic.Cues
	identifyErr error
	// playGate, when set before the source runs, holds every Play until it
	// is closed: a controller that takes its time.
	playGate chan struct{}
}

func newFakeSource() *fakeSource { return &fakeSource{events: make(chan input.Event, 16)} }

func (f *fakeSource) Events() <-chan input.Event { return f.events }
func (*fakeSource) Dropped() uint64              { return 0 }
func (f *fakeSource) SetFeedback(fb input.Feedback) error {
	f.effects.add(effectCall{Kind: "feedback", Device: fb.Device, Feedback: fb})
	return nil
}

func (f *fakeSource) Play(e input.Effect) error {
	if f.playGate != nil {
		<-f.playGate
	}
	f.effects.add(effectCall{Kind: "play", Device: e.Device, Effect: e})
	return nil
}

func (f *fakeSource) SetLight(l input.Light) error {
	f.effects.add(effectCall{Kind: "light", Device: l.Device, Light: l})
	return nil
}

func (f *fakeSource) Stop(st input.Stop) error {
	f.effects.add(effectCall{Kind: "stop", Device: st.Device, Stop: st})
	return nil
}

func (f *fakeSource) Identify(id input.DeviceID) (haptic.Cues, error) {
	f.mu.Lock()
	cues, err := f.cues[id], f.identifyErr
	f.mu.Unlock()
	f.effects.add(effectCall{Kind: "identify", Device: id, Cues: cues})
	return cues, err
}

// setCues sets what the gamepad id can give for Identify.
func (f *fakeSource) setCues(id input.DeviceID, cues haptic.Cues) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cues == nil {
		f.cues = map[input.DeviceID]haptic.Cues{}
	}
	f.cues[id] = cues
}

// failIdentify makes Identify fail with err.
func (f *fakeSource) failIdentify(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.identifyErr = err
}

func (*fakeSource) WriteHeader(w io.Writer, prefix, _ string) error {
	_, err := io.WriteString(w, prefix+"fake input\n")
	return err
}

func (f *fakeSource) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func (f *fakeSource) Close() {
	f.mu.Lock()
	onClose := f.onClose
	f.mu.Unlock()
	if onClose != nil {
		onClose()
	}
	f.end(nil)
}

// setOnClose sets what Close calls first.
func (f *fakeSource) setOnClose(g func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onClose = g
}

// end closes Events, with Err then err, unless it is closed already.
func (f *fakeSource) end(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed, f.err = true, err
		close(f.events)
	}
}

// TestLiveInputPanics: when T-input ends of a panic, live mode releases
// the key held, exits 1 and reports the panic, with its stack in the log.
// A fake stands in for the Source, which TestPanicOnTInput of package
// input makes panic. It needs no SDL.
func TestLiveInputPanics(t *testing.T) {
	src := newFakeSource()
	tl := &testLive{}
	cfg := testConfig(t, tl, "", nil, "--dry-run")
	cfg.open = func(input.Options) (eventSource, error) { return src, nil }
	tl.start(t, cfg)
	src.events <- input.Event{Device: 1, Kind: input.DeviceAdded, Info: &input.DeviceInfo{ID: 1, Name: liveTestPad, IsGamepad: true}}
	src.events <- input.Event{Device: 1, Kind: input.ButtonDown, Control: "east"}
	tl.waitFor(t, tl.stdout, "send +Space\n")
	src.end(&input.PanicError{Value: "boom", Stack: []byte("goroutine 7 [running]:\n")})
	if code := tl.waitCode(t); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	out := tl.stdout.String()
	if !strings.HasSuffix(out, "send -Space\n") {
		t.Errorf("stdout does not end with the release of Space:\n%s", out)
	}
	checkBalanced(t, out)
	if want := "padmated: the SDL input stopped: input: panic on T-input: boom\n"; !strings.HasSuffix(tl.stderr.String(), want) {
		t.Errorf("stderr does not end with %q:\n%s", want, tl.stderr)
	}
	log := tl.log.String()
	if line := logLine(log, "the SDL input stopped"); !strings.Contains(line, `"err":"input: panic on T-input: boom","stack":"goroutine 7 [running]:\n"`) {
		t.Errorf("the SDL input stopped: %q, want the panic and its stack", line)
	}
	if line := logLine(log, "padmated stopped"); !strings.Contains(line, `"code":1`) {
		t.Errorf("padmated stopped: %q, want code 1", line)
	}
}

// TestLiveComboBeforeLoop: live mode opens the Source with an OnCombo and
// the default hold (DMN-07). OnCombo, which T-input calls when a combo
// fires, before it sends the ComboHeld, does nothing before the output loop
// has started, and afterwards lets go of every key at once, before the
// router pauses. The ComboHeld then pauses the mapping, and the next one
// resumes it. A fake stands in for the Source, so it needs no SDL.
func TestLiveComboBeforeLoop(t *testing.T) {
	src := newFakeSource()
	tl := &testLive{}
	cfg := testConfig(t, tl, "", nil, "--dry-run")
	var onCombo func()
	cfg.open = func(o input.Options) (eventSource, error) {
		if o.OnCombo == nil || o.ComboHold != 0 {
			t.Errorf("Options with OnCombo %v, ComboHold %v; want an OnCombo and ComboHold 0", o.OnCombo != nil, o.ComboHold)
		}
		onCombo = o.OnCombo
		if onCombo != nil {
			onCombo() // before the loop exists: it does nothing
		}
		return src, nil
	}
	tl.start(t, cfg)
	if onCombo == nil {
		t.FailNow()
	}
	info := &input.DeviceInfo{ID: 1, Name: liveTestPad, Path: "fake1", IsGamepad: true}
	src.events <- input.Event{Device: 1, Kind: input.DeviceAdded, Info: info}
	src.events <- input.Event{Device: 1, Kind: input.ButtonDown, Control: "east"}
	tl.waitFor(t, tl.stdout, "send +Space\n")
	onCombo()
	tl.waitFor(t, tl.stdout, "send -Space\n")
	if strings.Contains(tl.stdout.String(), "  mapping ") {
		t.Errorf("paused before the ComboHeld:\n%s", tl.stdout)
	}
	combo := input.Event{Device: 1, Kind: input.ComboHeld}
	src.events <- combo
	tl.waitFor(t, tl.stdout, "  mapping paused: release all\n")
	tl.waitRow(t, "the cancel of east", decisionRow("0", "east", "cancel"))
	src.events <- input.Event{Device: 1, Kind: input.ButtonUp, Control: "east"}
	src.events <- input.Event{Device: 1, Kind: input.ButtonDown, Control: "south"}
	src.events <- input.Event{Device: 1, Kind: input.ButtonUp, Control: "south"}
	tl.waitRow(t, "the press of south while paused", pausedRow("0", "south"))
	onCombo() // nothing is held
	src.events <- combo
	tl.waitFor(t, tl.stdout, "  mapping resumed\n")
	src.events <- input.Event{Device: 1, Kind: input.ButtonDown, Control: "east"}
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
	if n := strings.Count(out, "  mapping "); n != 2 {
		t.Errorf("%d pause and resume rows, want 2:\n%s", n, out)
	}
	log := tl.log.String()
	for _, msg := range []string{"mapping paused", "mapping resumed"} {
		if lines := logLines(log, msg); len(lines) != 1 || !strings.Contains(lines[0], `"by":"combo","id":1`) {
			t.Errorf("%s lines %q, want one, by the combo of device 1", msg, lines)
		}
	}
}

// TestLiveSlotsExit takes each exit path of live mode, including those of
// the session watcher (DMN-09), while two slots hold keys, Space on both:
// every key held goes up once, and Space only after both slots have
// canceled their press of east (CLAUDE.md invariant 1). The order of the
// two slots' releases is otherwise free. A fake stands in for the Source,
// so it needs no SDL.
func TestLiveSlotsExit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stop   func(*testLive, *fakeSource)
		code   int
		stderr string // the end of stderr
	}{
		{"ctrl+c", func(tl *testLive, _ *fakeSource) { tl.cancel() }, 0, "padmated: stopping\n"},
		{"session end", func(tl *testLive, _ *fakeSource) { tl.session.stop("session-end") }, 0,
			"padmated: stopping: the session is ending\n"},
		{"close", func(tl *testLive, _ *fakeSource) { tl.session.stop("close") }, 0, "padmated: stopping: asked to close\n"},
		{"events closed", func(_ *testLive, src *fakeSource) { src.end(nil) }, 1, "padmated: the SDL input stopped\n"},
		{"input panics", func(_ *testLive, src *fakeSource) {
			src.end(&input.PanicError{Value: "boom", Stack: []byte("goroutine 7 [running]:\n")})
		}, 1, "padmated: the SDL input stopped: input: panic on T-input: boom\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := newFakeSource()
			tl := &testLive{}
			cfg := testConfig(t, tl, "", nil, "--dry-run")
			cfg.open = func(input.Options) (eventSource, error) { return src, nil }
			tl.start(t, cfg)
			pads := []struct {
				name     string
				controls []string // east holds Space; the shoulders LCtrl and LShift
			}{{liveTestPad, []string{"east", "leftshoulder"}}, {"2nd " + liveTestPad, []string{"east", "rightshoulder"}}}
			for i, p := range pads {
				id := input.DeviceID(i + 1)
				info := &input.DeviceInfo{ID: id, Name: p.name, Path: fmt.Sprintf("fake%d", id), IsGamepad: true}
				src.events <- input.Event{Device: id, Kind: input.DeviceAdded, Info: info}
				tl.waitFor(t, tl.stdout, fmt.Sprintf("device %d %q added to slot %d", id, p.name, i))
			}
			for i, p := range pads {
				for _, c := range p.controls {
					src.events <- input.Event{Device: input.DeviceID(i + 1), Kind: input.ButtonDown, Control: c}
				}
			}
			for _, want := range []string{"send +Space\n", "send +LCtrl\n", "send +LShift\n"} {
				tl.waitFor(t, tl.stdout, want)
			}
			tl.waitRow(t, "slot 1's press of east", decisionRow("1", "east", "down"))
			tl.sync()
			tc.stop(tl, src)
			if code := tl.waitCode(t); code != tc.code {
				t.Errorf("exit %d, want %d", code, tc.code)
			}
			if !strings.HasSuffix(tl.stderr.String(), tc.stderr) {
				t.Errorf("stderr does not end with %q:\n%s", tc.stderr, tl.stderr)
			}

			out := tl.stdout.String()
			checkBalanced(t, out)
			edges := sendEdges(out)
			if len(edges) != 6 {
				t.Errorf("edges sent %q, want 6:\n%s", edges, out)
			}
			for _, e := range []string{"+Space", "+LCtrl", "+LShift", "-Space", "-LCtrl", "-LShift"} {
				if !slices.Contains(edges, e) {
					t.Errorf("no %s sent:\n%s", e, out)
				}
			}
			lines := slices.Collect(strings.Lines(out))
			index := func(match func(f []string) bool) int {
				return slices.IndexFunc(lines, func(l string) bool { return match(strings.Fields(l)) })
			}
			sent := func(e string) func([]string) bool {
				return func(f []string) bool { return len(f) >= 3 && f[1] == "send" && slices.Contains(f[2:], e) }
			}
			up := index(sent("-Space"))
			for _, s := range []string{"0", "1"} {
				if c := index(decisionRow(s, "east", "cancel")); c < 0 || up < c {
					t.Errorf("-Space at line %d, not after slot %s's cancel of east at line %d:\n%s", up+1, s, c+1, out)
				}
			}
			exit := slices.IndexFunc(lines, func(l string) bool { return strings.HasSuffix(l, "  exit: release all\n") })
			if tc.code == 0 {
				// Ctrl+C or the session: every release follows the exit line.
				for _, e := range []string{"-Space", "-LCtrl", "-LShift"} {
					if i := index(sent(e)); exit < 0 || i < exit {
						t.Errorf("%s at line %d, not after the exit line at line %d:\n%s", e, i+1, exit+1, out)
					}
				}
			} else if exit >= 0 {
				t.Errorf("an exit line when the input stopped:\n%s", out)
			}
		})
	}
}

// TestLiveOnEvent: liveConfig.onEvent, the probe of the latency benchmark
// (ENG-11), receives every event the slot handles, in order, before the
// printer notes it, and changes nothing the printer prints: stdout is the
// same as without it, but for the times, which come from the real clock.
// The bindings are holds, which start no timer. A fake stands in for the
// Source, so it needs no SDL.
func TestLiveOnEvent(t *testing.T) {
	info := &input.DeviceInfo{ID: 1, Name: liveTestPad, Path: "fake1", IsGamepad: true}
	events := []input.Event{
		{Time: 1000, Device: 1, Kind: input.DeviceAdded, Info: info},
		{Time: 2000, Device: 1, Kind: input.ButtonDown, Control: "east"}, // Space
		{Time: 3000, Device: 1, Kind: input.AxisMotion, Control: "leftx", Value: 1200},
		{Time: 4000, Device: 1, Kind: input.ButtonDown, Control: "leftshoulder"}, // LCtrl
		{Time: 5000, Device: 1, Kind: input.ButtonUp, Control: "east"},
		{Time: 6000, Device: 1, Kind: input.ButtonUp, Control: "leftshoulder"},
		{Time: 7000, Device: 1, Kind: input.DeviceRemoved},
	}
	notes := map[input.EventKind]string{
		input.DeviceAdded:   fmt.Sprintf("%q added to slot 0\n", liveTestPad),
		input.DeviceRemoved: fmt.Sprintf("%q removed from slot 0: release all\n", liveTestPad),
	}
	type call struct {
		slot  uint8
		e     input.Event
		early bool // the printer had noted e before onEvent got it
	}
	// run feeds events to live mode, with onEvent when probe, and returns
	// stdout once it has stopped, and the calls of onEvent.
	run := func(probe bool) (string, []call) {
		src := newFakeSource()
		tl := &testLive{}
		cfg := testConfig(t, tl, "", nil, "--dry-run")
		cfg.open = func(input.Options) (eventSource, error) { return src, nil }
		var mu sync.Mutex
		var calls []call
		if probe {
			cfg.onEvent = func(slot uint8, e input.Event) {
				note, ok := notes[e.Kind]
				early := ok && strings.Contains(tl.stdout.String(), note)
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, call{slot, e, early})
			}
		}
		tl.start(t, cfg)
		for _, e := range events {
			src.events <- e
		}
		tl.waitFor(t, tl.stdout, notes[input.DeviceRemoved])
		tl.cancel()
		if code := tl.waitCode(t); code != 0 {
			t.Fatalf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
		}
		mu.Lock()
		defer mu.Unlock()
		return tl.stdout.String(), slices.Clone(calls)
	}

	plain, none := run(false)
	if len(none) != 0 {
		t.Fatalf("onEvent unset, yet called %d times", len(none))
	}
	out, calls := run(true)
	var want []call
	for _, e := range events {
		want = append(want, call{0, e, false})
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("onEvent got\n%+v\nwant\n%+v", calls, want)
	}
	if got, want := sendEdges(out), []string{"+Space", "+LCtrl", "-Space", "-LCtrl"}; !slices.Equal(got, want) {
		t.Errorf("edges sent %q, want %q:\n%s", got, want, out)
	}
	checkBalanced(t, out)
	times := regexp.MustCompile(`(?m)^ *\d+\.\d{3} `)
	if got, want := times.ReplaceAllString(out, "T "), times.ReplaceAllString(plain, "T "); got != want {
		t.Errorf("stdout with onEvent, times replaced by T:\n%s\nwithout:\n%s", got, want)
	}
}

// panicSender records the key events it is sent and panics once, on the
// batch that presses Space.
type panicSender struct {
	mu       sync.Mutex
	sent     []string
	panicked bool
}

func (s *panicSender) Send(evs []output.KeyEvent) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range evs {
		s.sent = append(s.sent, edgeName(e))
		if !s.panicked && edgeName(e) == "+Space" {
			s.panicked = true
			panic("panicSender: +Space")
		}
	}
	return len(evs), nil
}

func (s *panicSender) edges() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sent)
}

// TestLiveOutputDies: when T-output dies, here of a panic of the Sender
// of --inject, live mode exits 1 after the loop has released the key the
// panicking batch pressed, and reports the panic once.
func TestLiveOutputDies(t *testing.T) {
	s := &panicSender{}
	tl := startTestLive(t, func() (output.Sender, error) { return s, nil }, "--inject")
	v := tl.attach(t)
	tl.waitFor(t, tl.log, `"msg":"slot assigned"`)
	press(t, v, "east", true)
	if code := tl.waitCode(t); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if got, want := s.edges(), []string{"+Space", "-Space"}; !slices.Equal(got, want) {
		t.Errorf("sent %q, want %q", got, want)
	}
	stderr := tl.stderr.String()
	if n := strings.Count(stderr, "panic on the T-output loop"); n != 1 {
		t.Errorf("stderr reports the panic %d times, want once:\n%s", n, stderr)
	}
	if want := "padmated: supervisor: the output loop ended: output: panic on the T-output loop: panicSender: +Space\n"; !strings.HasSuffix(stderr, want) {
		t.Errorf("stderr does not end with %q:\n%s", want, stderr)
	}
	if out := tl.stdout.String(); !strings.HasPrefix(out, "padmated: injecting: the keys go to the foreground window\n") || strings.Count(out, "\n") != 3 {
		t.Errorf("stdout %q, want only the header of --inject", out)
	}
	log := tl.log.String()
	if logLine(log, "output loop ended") == "" {
		t.Errorf("log has no output loop ended:\n%s", log)
	}
	if line := logLine(log, "padmated stopped"); !strings.Contains(line, `"code":1`) {
		t.Errorf("padmated stopped: %q, want code 1", line)
	}
}

// TestLiveInjectSenderError: when the Sender of --inject cannot be made,
// startLive fails after closing the Source it opened.
func TestLiveInjectSenderError(t *testing.T) {
	lib := inputtest.Library(t)
	tl := &testLive{}
	errInject := errors.New("no injection here")
	cfg := testConfig(t, tl, lib, func() (output.Sender, error) { return nil, errInject }, "--inject")
	run, err := startLive(context.Background(), cfg)
	if run != nil || !errors.Is(err, errInject) {
		t.Fatalf("startLive = %v, %v; want nil, %v", run, err, errInject)
	}
	// The UI says the output failed (UI-45).
	if rep := newStartupReport(cfg.opts, err); rep.Kind != "output" || rep.Message != errInject.Error() {
		t.Errorf("startup report %+v", rep)
	}
	if want := "padmated: no injection here\n"; !strings.HasPrefix(tl.stderr.String(), "padmated: SDL ") || !strings.HasSuffix(tl.stderr.String(), want) {
		t.Errorf("stderr %q, want the SDL header, then %q", tl.stderr, want)
	}
	if tl.stdout.String() != "" {
		t.Errorf("stdout %q, want empty", tl.stdout)
	}
	if line := logLine(tl.log.String(), "padmated stopped"); !strings.Contains(line, `"code":1`) {
		t.Errorf("padmated stopped: %q, want code 1", line)
	}
	// Only one Source may be open at a time.
	src, err := input.Open(input.Options{Library: lib})
	if err != nil {
		t.Fatalf("input.Open after startLive failed: %v", err)
	}
	src.Close()
}

// receiverTestTimeout is the receiver watchdog's timeout (IN-30) in the
// tests that lose a virtual Switch Pro gamepad, instead of padmated's
// input.DefaultReceiverTimeout.
const receiverTestTimeout = 500 * time.Millisecond

// receiverConfig returns the liveConfig of testConfig with one slot and
// the receiver watchdog's timeout receiverTestTimeout. Its open checks
// that padmated turns the watchdog on with input.DefaultReceiverTimeout
// and gives T-input its log.
func receiverConfig(t *testing.T, tl *testLive, args ...string) liveConfig {
	t.Helper()
	cfg := testConfig(t, tl, inputtest.Library(t), nil, args...)
	cfg.slots = 1
	open := cfg.open
	cfg.open = func(o input.Options) (eventSource, error) {
		if o.ReceiverTimeout != input.DefaultReceiverTimeout || o.Log == nil {
			return nil, fmt.Errorf("padmated opens SDL with ReceiverTimeout %v and Log %v, want %v and its log",
				o.ReceiverTimeout, o.Log, input.DefaultReceiverTimeout)
		}
		o.ReceiverTimeout = receiverTestTimeout
		return open(o)
	}
	return cfg
}

// attachReceiver attaches a virtual Switch Pro gamepad called liveTestPad
// with its heartbeat on, and waits until the watchdog has armed.
func (tl *testLive) attachReceiver(t *testing.T) (v *input.Virtual, stop func() time.Time) {
	t.Helper()
	v, err := tl.source.AttachVirtualDesc(inputtest.SwitchPro(liveTestPad))
	if err != nil {
		t.Fatalf("AttachVirtualDesc: %v", err)
	}
	tl.waitFor(t, tl.stdout, "device "+padID(v)+` "padmated live test" added to slot 0`)
	stop = inputtest.Beats(t, v, 5*time.Millisecond)
	tl.waitFor(t, tl.log, `"msg":"receiver heartbeat armed","id":`+padID(v)+",")
	return v, stop
}

// TestLiveVirtualReceiverLoss: a virtual Switch Pro gamepad holds east
// when its heartbeat stops, as a Nova 2 Lite's does when it is switched
// off behind its receiver (IN-30). Padmated reports it removed and
// releases Space. Its next press brings it back with a new id, reconnected
// to its slot, and is mapped.
func TestLiveVirtualReceiverLoss(t *testing.T) {
	tl := &testLive{}
	tl.start(t, receiverConfig(t, tl, "--dry-run"))
	v, stop := tl.attachReceiver(t)
	press(t, v, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space\n")
	stop()
	tl.waitFor(t, tl.stdout, "device "+padID(v)+` "padmated live test" removed from slot 0: release all`)
	tl.waitFor(t, tl.stdout, "send -Space\n")
	// A goroutine of the Source writes T-input's log, after the event.
	tl.waitFor(t, tl.log, `"msg":"receiver silent: reported removed"`)
	if line := logLine(tl.log.String(), "receiver silent: reported removed"); !strings.Contains(line, `"level":"WARN"`) ||
		!strings.Contains(line, `"id":`+padID(v)+",") {
		t.Errorf("receiver silent: %q, want a warn of %s", line, padID(v))
	}

	next := strconv.FormatUint(math.MaxUint32, 10)
	press(t, v, "south", true)
	press(t, v, "south", false)
	tl.waitFor(t, tl.stdout, "device "+next+` "padmated live test" added to slot 0`)
	tl.waitFor(t, tl.stdout, "send +LCtrl +Z\n")
	tl.waitFor(t, tl.stdout, "send -Z -LCtrl\n")
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
	}
	out := tl.stdout.String()
	checkBalanced(t, out)
	log := tl.log.String()
	if line := logLine(log, "receiver back: reported as a new device"); !strings.Contains(line, `"id":`+next+",") ||
		!strings.Contains(line, `"replaces":`+padID(v)) {
		t.Errorf("receiver back: %q, want %s replacing %s", line, next, padID(v))
	}
	assigned := logLines(log, "slot assigned")
	if len(assigned) != 2 || !strings.Contains(assigned[1], `"id":`+next+",") || !strings.Contains(assigned[1], `"reconnected":true`) {
		t.Errorf("slot assigned lines %q, want the second %s reconnected", assigned, next)
	}
}

// profileLatch latches Shift on west (MAP-10): the profile of
// TestLiveVirtualReceiverLatch.
const profileLatch = `{"schemaVersion": 0, "name": "latch", "layers": {"base": {` +
	`"west": {"latch": true, "press": {"type": "keys", "keys": "Shift"}}}}}` + "\n"

// TestLiveVirtualReceiverLatch: a latched Shift is held with no control
// held. When the gamepad's heartbeat stops it is still reported removed,
// and Shift is released.
func TestLiveVirtualReceiverLatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(profileLatch), 0o644); err != nil {
		t.Fatal(err)
	}
	tl := &testLive{}
	tl.start(t, receiverConfig(t, tl, "--profile", path, "--dry-run"))
	v, stop := tl.attachReceiver(t)
	press(t, v, "west", true)
	press(t, v, "west", false)
	tl.waitFor(t, tl.stdout, "send +LShift\n")
	stop()
	tl.waitFor(t, tl.stdout, "device "+padID(v)+` "padmated live test" removed from slot 0: release all`)
	tl.waitFor(t, tl.stdout, "send -LShift\n")
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, tl.stderr)
	}
	checkBalanced(t, tl.stdout.String())
}
