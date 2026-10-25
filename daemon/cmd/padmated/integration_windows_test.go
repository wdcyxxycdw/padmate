package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/wdcyxxycdw/padmate/daemon/internal/injecttest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/watch"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsessiontest"
)

// profileF13 is profileZ with F13 held on east instead of Space: the save
// of TestPadToProbe.
const profileF13 = `{"schemaVersion": 0, "name": "reload", "layers": {"base": {` +
	`"south": {"press": {"type": "keys", "keys": "Ctrl+Z", "mode": "tap"}}, ` +
	`"east": {"press": {"type": "keys", "keys": "F13", "mode": "hold"}}}}}` + "\n"

// probeTest runs live mode with --inject into padmate-probe, a hook in
// another process, which records padmated's keys with LLKHF_INJECTED and
// swallows them. SDL's virtual gamepads are the process's own, so
// padmated runs in the test process, as in TestLiveInjectVirtual, and its
// nonce is the process id.
//
// Two hooks swallow every padmate-tagged event. A Backstop is installed
// first and proven live by a canary; then the probe, whose hook the
// system calls first, is proven live by a canary that it must swallow
// before padmated starts. The backstop must see only its own canaries.
// Each step waits for the probe to report its keys, by count; once
// padmated has stopped, a canary through the probe fences them. The
// cleanups, also when the test fails, stop padmated (or exit the process
// with the hooks in place when it does not stop), fence its keys, stop
// the probe and check the backstop, in that order.
type probeTest struct {
	t     *testing.T
	lib   string // the SDL library
	pid   uint32 // padmated's nonce
	back  *injecttest.Backstop
	probe *injecttest.Probe
	next  uint32         // the canary nonce last used
	hkl   windows.Handle // the foreground layout, before padmated starts
	// tl is live mode once it has started, and fenced is set once its keys
	// are fenced.
	tl     *testLive
	fenced bool
}

// newProbeTest skips t unless PADMATE_TEST_INJECT=1, and installs the
// backstop and the probe, each proven live by a canary.
func newProbeTest(t *testing.T) *probeTest {
	t.Helper()
	injecttest.Require(t)
	pt := &probeTest{t: t}
	pt.lib = inputtest.Library(t) // a failure, not a skip, with PADMATE_REQUIRE_SDL=1
	probeExe := injecttest.BuildTool(t, "../padmate-probe")
	pt.pid = uint32(os.Getpid())

	// The canaries' nonces, each used once, are far from any process id.
	pt.next = injecttest.NonceBase() | 0x80000000

	// The backstop is the only hook of the test so far. Its check runs
	// once the probe has stopped.
	pt.back = injecttest.StartBackstop(t)
	pt.back.Canary(t, pt.nonce())
	pt.probe = injecttest.StartProbe(t, probeExe, "--duration", "2m")
	pt.probe.Canary(t, pt.nonce())

	// Cleanups run last in first: this one after that of testLive.start,
	// and before those that stop the probe and check the backstop.
	t.Cleanup(func() {
		if !pt.fenced {
			pt.stopAndFence()
		}
	})
	pt.hkl = injecttest.ForegroundLayout()
	return pt
}

// nonce returns a canary nonce of its own.
func (pt *probeTest) nonce() uint32 {
	pt.next++
	return pt.next
}

// start starts live mode with cfg, which testConfig made for tl.
func (pt *probeTest) start(tl *testLive, cfg liveConfig) {
	pt.t.Helper()
	tl.start(pt.t, cfg)
	pt.tl = tl
}

// reported waits until the probe has reported n keys of padmated.
func (pt *probeTest) reported(n int, step string) {
	pt.t.Helper()
	if got := pt.probe.Wait(pt.pid, n, liveWait); len(got) < n {
		pt.t.Fatalf("%s: the probe reported %d keys of padmated after %v, want %d: %v\nlog:\n%s",
			step, len(got), liveWait, n, got, pt.tl.log)
	}
}

// stopAndFence stops live mode, when it has started, and fences its keys
// with a canary through the probe: once the probe has reported it, it
// has reported them all, and it may go. Should it not report the canary,
// a canary through the backstop fences the keys instead. When live mode
// does not stop, the process exits with both hooks in place (stopLive).
func (pt *probeTest) stopAndFence() int {
	t := pt.t
	t.Helper()
	code := stopLive(t, pt.tl)
	if got, ok := pt.probe.TryCanary(t, pt.nonce()); !ok {
		t.Errorf("the probe did not swallow the canary sent after the stop within %v (got %v, stderr %q)",
			injecttest.CanaryTimeout, got, pt.probe.Stderr())
		pt.back.Canary(t, pt.nonce())
	}
	return code
}

// finish stops live mode and fences its keys, then checks the run: exit
// code 0, no error in the log, nothing on the probe's stderr, and the
// keys the probe reported, against want (checkKeys). It logs each key,
// with its LLKHF_ALTDOWN.
func (pt *probeTest) finish(want []output.KeyEvent) {
	t := pt.t
	t.Helper()
	code := pt.stopAndFence()
	pt.fenced = true
	if code != 0 {
		t.Errorf("exit %d after Ctrl+C, want 0\nstderr:\n%s", code, pt.tl.stderr)
	}
	for line := range strings.Lines(pt.tl.log.String()) {
		if strings.Contains(line, `"level":"ERROR"`) {
			t.Errorf("padmated logged an error: %s", line)
		}
	}
	for _, line := range pt.probe.Stderr() {
		t.Errorf("probe stderr: %s", line)
	}
	got := pt.probe.WithNonce(pt.pid)
	seenKeys := make([]seenKey, len(got))
	for i, e := range got {
		t.Logf("#%d: %v altDown=%v", i, e, e.AltDown)
		seenKeys[i] = probeKey(e)
	}
	checkKeys(t, "the probe", seenKeys, want, pt.hkl, pt.pid)
}

// TestPadToProbe is ENG-10: a virtual gamepad's presses go through live
// mode with --inject and SendInput to padmate-probe (probeTest); also the
// release of a held key when the gamepad is unplugged, when the profile
// is saved and on the exit path (CLAUDE.md invariant 1, with real
// output). Unplugged, the gamepad has its buttons lifted by SDL before
// SDL reports the removal, so that release is the button's own, and the
// removal finds nothing held: the supervisor's golden test "reconnect"
// covers the release by the removal.
func TestPadToProbe(t *testing.T) {
	pt := newProbeTest(t)
	resolve := keyResolver(t)
	// F13's scan code, 0x64, is the same on every layout (ADR-0003); the
	// expected events come from the Resolver all the same, as Ctrl+Z's
	// and Space's do.
	undo, space, f13 := resolve("Ctrl+Z"), resolve("Space"), resolve("F13")
	tap := slices.Concat(undo, releases(undo))
	want := slices.Concat(
		tap, space, releases(space), // a, b: pad A
		tap, space, releases(space), // c, d: pad A2, until the save
		f13, releases(f13), // d, e: with the saved profile
	)

	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(profileZ), 0o644); err != nil {
		t.Fatal(err)
	}
	tl := &testLive{}
	cfg := testConfig(t, tl, pt.lib, nil, "--profile", path, "--inject", "--watch")
	// The watcher polls fast, as in the reload tests; step d saves by
	// rename (saveByRename).
	cfg.watch = watch.Options{Interval: 2 * time.Millisecond, Debounce: 20 * time.Millisecond}
	pt.start(tl, cfg)

	// a. Pad A taps Ctrl+Z.
	n := 0
	a := tl.attach(t)
	tl.waitFor(t, tl.log, `"msg":"slot assigned"`)
	press(t, a, "south", true)
	press(t, a, "south", false)
	n += len(tap)
	pt.reported(n, "a: tap")

	// b. East holds Space, and unplugging A releases it: SDL lifts east
	// before it reports the removal (IN-05), which then finds nothing held.
	press(t, a, "east", true)
	n += len(space)
	pt.reported(n, "b: press of east")
	detach(t, a)
	n += len(space)
	pt.reported(n, "b: unplugging A")

	// c. A2 has A's name, and so its device key: it is A reconnected.
	a2 := tl.attach(t)
	tl.waitForCount(t, tl.log, `"msg":"slot assigned"`, 2)
	if line := logLines(tl.log.String(), "slot assigned")[1]; !strings.Contains(line, `"reconnected":true`) {
		t.Errorf("slot assigned: %q, want A2 reconnected", line)
	}
	press(t, a2, "south", true)
	press(t, a2, "south", false)
	n += len(tap)
	pt.reported(n, "c: tap")

	// d. A save of the profile while east holds Space releases it
	// (PRO-03). Space released, the slot runs the saved profile: the
	// release of east does nothing (ADR-0020), and its next press holds
	// F13.
	press(t, a2, "east", true)
	n += len(space)
	pt.reported(n, "d: press of east")
	saveByRename(t, path, profileF13)
	n += len(space)
	pt.reported(n, "d: reload")
	tl.waitFor(t, tl.log, `"msg":"profile reloaded"`)
	press(t, a2, "east", false)
	press(t, a2, "east", true)
	n += len(f13)
	pt.reported(n, "d: press of east after the reload")

	// e. The exit path releases F13.
	pt.finish(want)
}

// TestPadsToProbe is IN-08 with real output: two virtual gamepads, each on
// a slot of its own, hold F13 on east through live mode with --inject and
// SendInput to padmate-probe (probeTest). The slots share the key: it goes
// down at the first press, and up at the last release only, be it a
// button's or the exit path's (CLAUDE.md invariant 1, with real output).
// Unplugged, a gamepad has its buttons lifted by SDL before SDL reports
// the removal, so unplugging it is a release too.
//
// A release that sends nothing is fenced by a tap of Ctrl+Z on south of
// the same pad: its slot handles the pad's edges in order, one output
// command at a time, so the probe reports whatever the release sent
// before the tap, and the keys the probe reports are checked in order.
func TestPadsToProbe(t *testing.T) {
	pt := newProbeTest(t)
	resolve := keyResolver(t)
	undo, f13 := resolve("Ctrl+Z"), resolve("F13")
	tap := slices.Concat(undo, releases(undo))
	want := slices.Concat(
		f13, tap, tap, releases(f13), // a, b, c: both hold, a releases, b releases
		f13, tap, tap, releases(f13), // d, e: both hold, a is unplugged, the exit
	)

	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(profileF13), 0o644); err != nil {
		t.Fatal(err)
	}
	tl := &testLive{}
	pt.start(tl, testConfig(t, tl, pt.lib, nil, "--profile", path, "--inject"))
	a := tl.attach(t)
	tl.waitFor(t, tl.log, `"msg":"slot assigned","slot":0,`)
	// Not liveTestPad+" 2": see TestLiveVirtualReconnect.
	b, err := tl.source.AttachVirtual("2nd " + liveTestPad)
	if err != nil {
		t.Fatalf("AttachVirtual: %v", err)
	}
	tl.waitFor(t, tl.log, `"msg":"slot assigned","slot":1,`)
	n := 0
	tapSouth := func(v *input.Virtual, step string) {
		t.Helper()
		press(t, v, "south", true)
		press(t, v, "south", false)
		n += len(tap)
		pt.reported(n, step)
	}

	// a. A holds east, F13 goes down; B's hold sends nothing.
	press(t, a, "east", true)
	n += len(f13)
	pt.reported(n, "a: A holds east")
	press(t, b, "east", true)
	tapSouth(b, "a: B holds east")

	// b. A lets go, and B still holds F13.
	press(t, a, "east", false)
	tapSouth(a, "b: A lets go")

	// c. B lets go: F13 goes up.
	press(t, b, "east", false)
	n += len(f13)
	pt.reported(n, "c: B lets go")

	// d. Both hold again, and A is unplugged: SDL lifts its east first, and
	// B still holds F13. Once the router has freed A's slot, Sync waits for
	// the slot to handle the edges before, and for their output, so that
	// B's tap comes after it.
	press(t, a, "east", true)
	n += len(f13)
	pt.reported(n, "d: A holds east")
	press(t, b, "east", true)
	tapSouth(b, "d: B holds east")
	detach(t, a)
	tl.waitFor(t, tl.log, `"msg":"slot freed","slot":0,"id":`+padID(a)+",")
	tl.sync()
	tapSouth(b, "d: A is unplugged")

	// e. The exit path releases F13.
	pt.finish(want)
}

// profileMenuMask holds Alt on south and LWin on east, which the
// MenuMask masks when released alone, and taps Alt+Tab on west and F13
// on north, which keep Alt from being alone: the profile of
// TestMenuMaskToProbe.
const profileMenuMask = `{"schemaVersion": 0, "name": "menu mask", "layers": {"base": {` +
	`"south": {"press": {"type": "keys", "keys": "Alt", "mode": "hold"}}, ` +
	`"east": {"press": {"type": "keys", "keys": "LWin", "mode": "hold"}}, ` +
	`"west": {"press": {"type": "keys", "keys": "Alt+Tab", "mode": "tap"}}, ` +
	`"north": {"press": {"type": "keys", "keys": "F13", "mode": "tap"}}}}}` + "\n"

// TestMenuMaskToProbe is the automatic acceptance of OUT-08 (ADR-0021):
// the release of a lone Alt or LWin of a virtual gamepad goes through
// live mode with --inject and SendInput to padmate-probe (probeTest)
// right after a press and a release of the mask key, vk 0xE8 (the probe
// sees the order, not the batches), and so does that of a lone Alt on
// the exit path; Alt+Tab, and Alt held while F13 is tapped, have no
// mask. The probe must see the mask with vk 0xE8, scan code 0 and no E0
// flag; its LLKHF_ALTDOWN is logged, not checked.
//
// The edges of one virtual gamepad reach padmated in the order they are
// set, so a hold control is pressed and released with no wait between.
// A tap is released 20 ms after its press, by the Loop's alarm, so the
// test waits for that before an edge that releases another key.
func TestMenuMaskToProbe(t *testing.T) {
	pt := newProbeTest(t)
	resolve := keyResolver(t)
	// F13's scan code is the same on every layout (ADR-0003). RAlt is left
	// out: on an AltGr layout the system may add a fake LCtrl to it.
	alt, win, altTab, f13 := resolve("Alt"), resolve("LWin"), resolve("Alt+Tab"), resolve("F13")
	mask := []output.KeyEvent{output.MenuMaskEvent(false), output.MenuMaskEvent(true)}
	masked := func(down []output.KeyEvent) []output.KeyEvent { return slices.Concat(mask, releases(down)) }
	want := slices.Concat(
		alt, masked(alt), // a: a lone Alt (the M1c acceptance)
		win, masked(win), // b: a lone LWin
		altTab, releases(altTab), // c: a tap of Alt+Tab, no mask
		alt, f13, releases(f13), releases(alt), // d: F13 tapped while Alt is held, no mask
		alt, masked(alt), // e: Alt held at the exit, masked by the exit path's release
	)

	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(profileMenuMask), 0o644); err != nil {
		t.Fatal(err)
	}
	tl := &testLive{}
	pt.start(tl, testConfig(t, tl, pt.lib, nil, "--profile", path, "--inject"))

	// a. South holds Alt alone.
	n := 0
	v := tl.attach(t)
	tl.waitFor(t, tl.log, `"msg":"slot assigned"`)
	press(t, v, "south", true)
	press(t, v, "south", false)
	n += len(alt) + len(mask) + len(alt)
	pt.reported(n, "a: lone Alt")

	// b. East holds LWin alone.
	press(t, v, "east", true)
	press(t, v, "east", false)
	n += len(win) + len(mask) + len(win)
	pt.reported(n, "b: lone LWin")

	// c. West taps Alt+Tab; the wait covers the end of the tap.
	press(t, v, "west", true)
	press(t, v, "west", false)
	n += 2 * len(altTab)
	pt.reported(n, "c: tap of Alt+Tab")

	// d. North taps F13 while south holds Alt. Without the wait for the end
	// of the tap, the release of south could reach the Loop first, and the
	// probe see -LAlt before -F13.
	press(t, v, "south", true)
	n += len(alt)
	pt.reported(n, "d: press of south")
	press(t, v, "north", true)
	n += 2 * len(f13)
	pt.reported(n, "d: tap of F13")
	press(t, v, "north", false) // the release of a tap control does nothing
	press(t, v, "south", false)
	n += len(alt)
	pt.reported(n, "d: release of south")

	// e. South holds Alt alone at the exit, whose release masks it. Without
	// the wait, the stop could come before the press.
	press(t, v, "south", true)
	n += len(alt)
	pt.reported(n, "e: press of south")
	pt.finish(want)
}

// TestComboToProbe is DMN-07 with real output: while east holds F13, a
// virtual gamepad holds back and start, its panic combo, for
// comboTestHold, through live mode with --inject and SendInput to
// padmate-probe (probeTest). OnCombo lets go of F13 from T-input, and the
// mapping pauses: a tap of south and a press of east send nothing. The
// combo held again resumes the mapping; east, held through the resume,
// does nothing when released, its next press holds F13, and the exit path
// releases it (CLAUDE.md invariant 1, with real output).
//
// A tap of Ctrl+Z on south after the resume fences what the edges before
// it sent: the slot handles the pad's edges in order, one output command
// at a time, so the probe reports whatever they sent before the tap, and
// the keys the probe reports are checked in order.
func TestComboToProbe(t *testing.T) {
	pt := newProbeTest(t)
	resolve := keyResolver(t)
	undo, f13 := resolve("Ctrl+Z"), resolve("F13")
	tap := slices.Concat(undo, releases(undo))
	want := slices.Concat(
		f13, releases(f13), // a, b: east holds F13, the combo lets go of it
		tap, f13, releases(f13), // c, d: after the resume; the exit
	)

	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(profileF13), 0o644); err != nil {
		t.Fatal(err)
	}
	tl := &testLive{}
	pt.start(tl, comboConfig(t, tl, pt.lib, "--profile", path, "--inject"))

	// a. East holds F13.
	n := 0
	v := tl.attach(t)
	tl.waitFor(t, tl.log, `"msg":"slot assigned"`)
	press(t, v, "east", true)
	n += len(f13)
	pt.reported(n, "a: press of east")

	// b. Back and start held for comboTestHold release F13 and pause the
	// mapping. The tap of south, and east released and pressed again, send
	// nothing.
	press(t, v, "back", true)
	press(t, v, "start", true)
	n += len(f13)
	pt.reported(n, "b: the combo")
	tl.waitFor(t, tl.log, `"msg":"mapping paused"`)
	press(t, v, "back", false)
	press(t, v, "start", false)
	press(t, v, "east", false)
	press(t, v, "south", true)
	press(t, v, "south", false)
	press(t, v, "east", true)

	// c. The combo held again resumes the mapping. The release of east does
	// nothing, the tap of south fences it, and the next press of east holds
	// F13; the wait for the end of the tap keeps +F13 after it.
	press(t, v, "back", true)
	press(t, v, "start", true)
	tl.waitFor(t, tl.log, `"msg":"mapping resumed"`)
	press(t, v, "back", false)
	press(t, v, "start", false)
	press(t, v, "east", false)
	press(t, v, "south", true)
	press(t, v, "south", false)
	n += len(tap)
	pt.reported(n, "c: tap after the resume")
	press(t, v, "east", true)
	n += len(f13)
	pt.reported(n, "c: press of east")

	// d. The exit path releases F13.
	pt.finish(want)
	log := tl.log.String()
	for _, msg := range []string{"mapping paused", "mapping resumed"} {
		if lines := logLines(log, msg); len(lines) != 1 || !strings.Contains(lines[0], `"by":"combo","id":`+padID(v)) {
			t.Errorf("%s lines %q, want one, by the combo of %s", msg, lines, padID(v))
		}
	}
}

// TestSessionToProbe is DMN-09 with real output and the real session
// watcher, which polls only once an hour here: while east holds F13, a
// lock of the session, sent to winsession's own window (winsessiontest),
// releases F13 within a second through live mode with --inject and
// SendInput to padmate-probe (probeTest). The mapping is suspended: a tap
// of south and a press of east send nothing. The unlock, which winsession
// confirms by querying the system, ends the suspension; east, held
// through it, does nothing when released, its next press holds F13, and
// the exit path releases it (CLAUDE.md invariant 1, with real output).
//
// A session that already suspends the mapping at the start (locked, or on
// a desktop other than the input desktop) skips the test, with the
// reasons in the log, or fails it with PADMATE_REQUIRE_SESSION=1. The
// edges while locked come from T-input and the unlock from this goroutine,
// so the test waits for the router to take them (relaySource), then fences
// what they sent with a canary through the probe, before the unlock. A tap
// of Ctrl+Z on south after the unlock fences the release of east, as in
// TestComboToProbe, and the keys the probe reports are checked in order.
func TestSessionToProbe(t *testing.T) {
	pt := newProbeTest(t)
	resolve := keyResolver(t)
	undo, f13 := resolve("Ctrl+Z"), resolve("F13")
	tap := slices.Concat(undo, releases(undo))
	want := slices.Concat(
		f13, releases(f13), // a, b: east holds F13, the lock lets go of it
		tap, f13, releases(f13), // c, d: after the unlock; the exit
	)

	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(profileF13), 0o644); err != nil {
		t.Fatal(err)
	}
	tl := &testLive{}
	cfg := testConfig(t, tl, pt.lib, nil, "--profile", path, "--inject")
	var (
		w        *winsession.Watcher
		startErr error
		rs       *relaySource
	)
	cfg.session = func(c winsession.Config) (sessionWatcher, error) {
		c.Poll = time.Hour // only the messages move it
		if w, startErr = winsession.Start(c); startErr != nil {
			return nil, startErr
		}
		return w, nil
	}
	open := cfg.open
	cfg.open = func(o input.Options) (eventSource, error) {
		src, err := open(o)
		if err != nil {
			return nil, err
		}
		rs = newRelaySource(src)
		return rs, nil
	}
	pt.start(tl, cfg)
	if startErr != nil {
		t.Fatalf("winsession.Start: %v\nstderr:\n%s", startErr, tl.stderr)
	}
	if r := w.Reasons(); r != 0 {
		if os.Getenv("PADMATE_REQUIRE_SESSION") == "1" {
			t.Fatalf("the session suspends the mapping at the start, for %v; PADMATE_REQUIRE_SESSION=1", r)
		}
		t.Logf("the session suspends the mapping at the start, for %v", r)
		t.Skip("the session suspends the mapping already; set PADMATE_REQUIRE_SESSION=1 to fail instead")
	}
	var session uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); err != nil {
		t.Fatal(err)
	}
	// send sends the session event to winsession's window, as the system
	// does, with this process's session in lParam.
	send := func(event uintptr) {
		winsessiontest.Send(w.Window(), winsession.WMWTSSessionChange, event, uintptr(session))
	}

	// a. East holds F13.
	n := 0
	v := tl.attach(t)
	tl.waitFor(t, tl.log, `"msg":"slot assigned"`)
	press(t, v, "east", true)
	n += len(f13)
	pt.reported(n, "a: press of east")

	// b. The lock releases F13 within a second and suspends the mapping.
	// The release of east, a tap of south and a press of east send
	// nothing: once the router has taken them and the output they caused
	// has run, a canary through the probe fences them.
	locked := time.Now()
	send(winsession.WTSSessionLock)
	n += len(f13)
	if got := pt.probe.Wait(pt.pid, n, time.Second); len(got) < n {
		t.Fatalf("b: the probe reported %d keys of padmated within 1s of the lock, want %d: %v\nlog:\n%s",
			len(got), n, got, tl.log)
	}
	t.Logf("b: F13 released %v after the lock", time.Since(locked))
	tl.waitFor(t, tl.log, `"msg":"mapping suspended"`)
	if st := tl.sup.Status(); st.Suspended != winsession.Lock || st.Paused {
		t.Errorf("b: Status() = %+v, want suspended for lock, not paused", st)
	}
	press(t, v, "east", false)
	press(t, v, "south", true)
	press(t, v, "south", false)
	press(t, v, "east", true)
	rs.waitButtons(t, tl, "+east", "-east", "+south", "-south", "+east")
	tl.sync()
	pt.probe.Canary(t, pt.nonce())
	if got := pt.probe.WithNonce(pt.pid); len(got) != n {
		t.Fatalf("b: the probe reported %d keys of padmated while locked, want %d: %v", len(got), n, got)
	}

	// c. The unlock, confirmed by the system's session and desktop,
	// unsuspends the mapping. The router logs that before it opens the
	// system's gate behind a barrier to every slot (openAfter), so the
	// wait syncs, which returns after those. The release of east does
	// nothing. South was released while locked, which may have been a
	// synthetic up, so its first resumed down/up establishes release.
	// The following tap of south fences it, and the next press of east
	// holds F13; the wait for the end of the tap keeps +F13 after it.
	send(winsession.WTSSessionUnlock)
	tl.waitUnsuspended(t, tl.log, `"msg":"mapping unsuspended"`)
	if st := tl.sup.Status(); !st.Mapping() {
		t.Errorf("c: Status() = %+v, want mapping", st)
	}
	press(t, v, "east", false)
	press(t, v, "south", true)
	press(t, v, "south", false)
	tl.sync()
	pt.probe.Canary(t, pt.nonce())
	if got := pt.probe.WithNonce(pt.pid); len(got) != n {
		t.Fatalf("c: uncertain south press was replayed after unlock: %v", got)
	}
	press(t, v, "south", true)
	press(t, v, "south", false)
	n += len(tap)
	pt.reported(n, "c: tap after the unlock")
	press(t, v, "east", true)
	n += len(f13)
	pt.reported(n, "c: press of east")

	// d. The exit path releases F13.
	pt.finish(want)
	log := tl.log.String()
	for _, c := range []struct{ msg, attr string }{
		{"mapping suspended", `"reasons":"lock"`},
		{"mapping unsuspended", `"was":"lock"`},
	} {
		if lines := logLines(log, c.msg); len(lines) != 1 || !strings.Contains(lines[0], c.attr) {
			t.Errorf("%s lines %q, want one, with %s", c.msg, lines, c.attr)
		}
	}
	if strings.Contains(tl.stderr.String(), "session events") {
		t.Errorf("stderr has the session events off or stopped:\n%s", tl.stderr)
	}
}

// relaySource hands on the events of a Source through a channel without a
// buffer, and records the button edges it has handed on: once it has
// recorded one, the router has taken it. It stops handing on once closed.
type relaySource struct {
	eventSource
	events chan input.Event
	stop   chan struct{}
	once   sync.Once

	mu      sync.Mutex
	buttons []string // "+east" for a press of east, "-east" for its release
}

func newRelaySource(src eventSource) *relaySource {
	r := &relaySource{eventSource: src, events: make(chan input.Event), stop: make(chan struct{})}
	go r.run()
	return r
}

func (r *relaySource) run() {
	defer close(r.events)
	for e := range r.eventSource.Events() {
		select {
		case r.events <- e:
		case <-r.stop:
			return
		}
		var sign string
		switch e.Kind {
		case input.ButtonDown:
			sign = "+"
		case input.ButtonUp:
			sign = "-"
		default:
			continue
		}
		r.mu.Lock()
		r.buttons = append(r.buttons, sign+e.Control)
		r.mu.Unlock()
	}
}

func (r *relaySource) Events() <-chan input.Event { return r.events }

func (r *relaySource) Close() {
	r.once.Do(func() { close(r.stop) })
	r.eventSource.Close()
}

// waitButtons waits, as tl.waitUntil does, until the router has taken
// exactly the button edges want.
func (r *relaySource) waitButtons(t *testing.T, tl *testLive, want ...string) {
	t.Helper()
	var got []string
	tl.waitUntil(t, fmt.Sprintf("the router to take the button edges %q", want), func() bool {
		r.mu.Lock()
		got = slices.Clone(r.buttons)
		r.mu.Unlock()
		return len(got) >= len(want)
	})
	if !slices.Equal(got, want) {
		t.Fatalf("the router took the button edges %q, want %q", got, want)
	}
}

// saveByRename replaces the file at path with text in one step, as an
// editor that saves by rename does. The watcher cannot read it half
// written then, as it may a save by os.WriteFile, truncated and not yet
// written: a profile with errors, which padmated logs at error level.
// Windows refuses to replace a file that is open without
// FILE_SHARE_DELETE, as os.ReadFile opens it for the watcher and a virus
// scanner may, so the rename is tried again for liveWait.
func saveByRename(t *testing.T, path, text string) {
	t.Helper()
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(liveWait)
	for {
		err := os.Rename(tmp, path)
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("saving %s: %v", path, err)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
