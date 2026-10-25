package output_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

var errDisk = errors.New("test: disk")

// press returns the press of the key spec names, as the Arbiter sends it.
func press(t testing.TB, spec string) output.KeyEvent {
	t.Helper()
	e, err := output.KeyEventFor(keys.MustParse(spec).Key(), false)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// heldPath returns a held-key log path in a directory of the test's own.
func heldPath(t testing.TB) string {
	return filepath.Join(t.TempDir(), "run", "held.bin")
}

// writeHeld writes a held-key log with the presses of specs at path.
func writeHeld(t testing.TB, path string, specs ...string) {
	t.Helper()
	var events []output.KeyEvent
	for _, s := range specs {
		events = append(events, press(t, s))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, output.EncodeHeld(events...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// heldNames returns the keys of the held-key log at path by KeyID;
// exists is false when there is no file.
func heldNames(path string) (names []string, exists bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	ids, err := output.DecodeHeld(b)
	for _, id := range ids {
		names = append(names, id.String())
	}
	return names, true, err
}

// readHeld is heldNames that fails the test on an error.
func readHeld(t testing.TB, path string) (names []string, exists bool) {
	t.Helper()
	names, exists, err := heldNames(path)
	if err != nil {
		t.Fatalf("reading the held-key log: %v", err)
	}
	return names, exists
}

// wantHeld checks that the log at path has the keys specs, named by
// their KeyID.
func wantHeld(t testing.TB, path string, specs ...string) {
	t.Helper()
	got, ok := readHeld(t, path)
	if !ok {
		t.Fatalf("no held-key log, want one with %q", specs)
	}
	want := []string{}
	for _, s := range specs {
		want = append(want, press(t, s).ID().String())
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("held-key log has %q, want %q", got, want)
	}
}

// wantNoHeld checks that there is no held-key log at path.
func wantNoHeld(t testing.TB, path string) {
	t.Helper()
	if got, ok := readHeld(t, path); ok {
		t.Errorf("held-key log with %q left, want none", got)
	}
}

// startLoopWith is startLoopOn with the Sender that sender makes of the
// SyncRecorder, and prepare run on the Recorder before the Loop starts.
func startLoopWith(t *testing.T, opts output.LoopOptions, prepare func(*simtest.Recorder), sender func(*simtest.SyncRecorder) output.Sender) *loopTest {
	t.Helper()
	fake := clock.NewFake()
	lt := &loopTest{t: t, clk: fake, rec: simtest.NewSyncRecorder(fake)}
	if prepare != nil {
		lt.rec.With(prepare)
	}
	var s output.Sender = lt.rec
	if sender != nil {
		s = sender(lt.rec)
	}
	lt.l = output.StartLoop(fake, s, opts)
	t.Cleanup(func() {
		_ = lt.stop()
		lt.rec.With(func(r *simtest.Recorder) { simtest.AssertNoneHeld(t, nil, r) })
	})
	return lt
}

// coverSender checks at every Send that the held-key log has every key
// the batch presses and every key the Recorder has down (OUT-14: the log
// is written before the Send).
type coverSender struct {
	t    *testing.T
	path string
	rec  *simtest.SyncRecorder
	sent atomic.Int64
}

func (c *coverSender) Send(events []output.KeyEvent) (int, error) {
	c.sent.Add(1)
	got, _, err := heldNames(c.path)
	if err != nil {
		c.t.Errorf("Send(%s): reading the held-key log: %v", eventsTrace(events), err)
	}
	var need []output.KeyEvent
	c.rec.With(func(r *simtest.Recorder) { need = r.Down() })
	for _, e := range events {
		if !e.Up {
			need = append(need, e)
		}
	}
	for _, e := range need {
		if !output.IsMenuMask(e) && !slices.Contains(got, e.ID().String()) {
			c.t.Errorf("Send(%s): the held-key log has %q, not %s", eventsTrace(events), got, e.ID())
		}
	}
	return c.rec.Send(events)
}

// TestLoopHeldLogCoversPresses: the Loop writes the held-key log before
// each Send, so a key that went down is in it at once: Ctrl down at 0 and
// Shift at 20 ms, with no command after, are both in it (the audit's
// case, which the 50 ms throttle of main missed). Once they are up, the
// log has no key, and Stop removes it.
func TestLoopHeldLogCoversPresses(t *testing.T) {
	path := heldPath(t)
	var cs *coverSender
	lt := startLoopWith(t, output.LoopOptions{HeldFile: path, KeyDown: func(output.KeyEvent) bool { return false }}, nil,
		func(r *simtest.SyncRecorder) output.Sender {
			cs = &coverSender{t: t, path: path, rec: r}
			return cs
		})
	if res := lt.l.Swept(); res.Err != nil || res.Off || len(res.Released) > 0 {
		t.Fatalf("Swept() = %+v, want nothing done", res)
	}
	wantHeld(t, path)
	lt.do(down(ownerA, "LCtrl"))
	lt.clk.Advance(20 * ms)
	lt.do(down(ownerA, "LShift"))
	wantHeld(t, path, "LCtrl", "LShift")
	lt.do(tap(ownerB, "Ctrl+Z", 0))
	wantHeld(t, path, "LCtrl", "LShift")
	lt.do(up(ownerA, "LShift"))
	wantHeld(t, path, "LCtrl")
	lt.do(up(ownerA, "LCtrl"))
	wantHeld(t, path)
	if err := lt.stop(); err != nil {
		t.Fatal(err)
	}
	wantNoHeld(t, path)
	if got := lt.rec.Trace(); got != "+LCtrl@0 +LShift@20 +Z@20 -Z@20 -LShift@20 -LCtrl@20" {
		t.Errorf("output: %s", got)
	}
	if n := cs.sent.Load(); n != 5 {
		t.Errorf("%d Send calls, want 5", n)
	}
}

// keyDownSet is a LoopOptions.KeyDown that reports the keys in it down;
// the test may change it while the Loop runs.
type keyDownSet struct {
	keys atomic.Pointer[[]output.KeyID]
}

func downSet(t testing.TB, specs ...string) *keyDownSet {
	k := &keyDownSet{}
	k.set(t, specs...)
	return k
}

// set sets the keys reported down; "mask" is the menu mask key.
func (k *keyDownSet) set(t testing.TB, specs ...string) {
	ids := []output.KeyID{}
	for _, s := range specs {
		if s == "mask" {
			ids = append(ids, output.MenuMaskEvent(false).ID())
		} else {
			ids = append(ids, press(t, s).ID())
		}
	}
	k.keys.Store(&ids)
}

func (k *keyDownSet) down(e output.KeyEvent) bool { return slices.Contains(*k.keys.Load(), e.ID()) }

// TestLoopSweep: before its first command the Loop releases the keys the
// last run left in the held-key log that KeyDown reports down, through
// its MenuMask, and the menu mask key when that is down; failures leave
// the keys in the log, and the Loop runs on.
func TestLoopSweep(t *testing.T) {
	allDown := func(e output.KeyEvent) bool { return !output.IsMenuMask(e) }
	for _, tc := range []struct {
		name     string
		file     []byte // nil: no log
		keyDown  func(output.KeyEvent) bool
		prepare  func(*simtest.Recorder)
		out      string // before the first command, a tap of F13
		released string
		err      error // wrapped by Swept().Err
		reset    bool  // Swept().Reset
		logAfter []string
	}{
		{name: "no log", keyDown: allDown, released: ""},
		{
			name: "intersect", file: output.EncodeHeld(press(t, "LCtrl"), press(t, "Z")),
			keyDown: downSet(t, "LCtrl").down, out: "-LCtrl@0", released: "-LCtrl",
		},
		{name: "mask alone", keyDown: downSet(t, "mask").down, out: "-vk:0xE8@0", released: "-vk:0xE8"},
		{
			name: "mask and Alt", file: output.EncodeHeld(press(t, "LAlt")), keyDown: downSet(t, "LAlt", "mask").down,
			out: "-vk:0xE8@0 +vk:0xE8@0 -vk:0xE8@0 -LAlt@0", released: "-LAlt -vk:0xE8",
		},
		{name: "too short", file: []byte("PDMH"), keyDown: allDown, err: output.ErrHeldCorrupt, reset: true},
		{name: "bad magic", file: append([]byte("JSON"), make([]byte, output.HeldSize-4)...), keyDown: allDown, err: output.ErrHeldCorrupt, reset: true},
		{
			name: "bad version", file: func() []byte {
				b := output.EncodeHeld(press(t, "LCtrl"))
				b[4] = 1
				return b
			}(), keyDown: allDown, err: output.ErrHeldCorrupt, reset: true,
		},
		{
			// Version 3 had the boot time for the logon time.
			name: "version 3", file: func() []byte {
				b := output.EncodeHeld(press(t, "LCtrl"))
				b[4] = 3
				return b
			}(), keyDown: allDown, err: output.ErrHeldCorrupt, reset: true,
		},
		{
			// Bits no key has, VK 0 and the mask key's: the sweep neither
			// releases nor keeps them, and Stop removes the log.
			name: "keyless bits", file: output.WithHeldVK(output.EncodeHeld(press(t, "LCtrl")), 0, output.MenuMaskVK),
			keyDown: func(output.KeyEvent) bool { return true }, out: "-vk:0xE8@0 -LCtrl@0", released: "-LCtrl -vk:0xE8",
		},
		{
			name: "keyless bits alone", file: output.WithHeldVK(output.EncodeHeld(), 0, output.MenuMaskVK),
			keyDown: allDown, released: "",
		},
		{
			name: "send fails", file: output.EncodeHeld(press(t, "LCtrl")), keyDown: allDown,
			prepare: func(r *simtest.Recorder) { r.FailNext(0, errBlocked) },
			err:     errBlocked, logAfter: []string{"LCtrl"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := heldPath(t)
			if tc.file != nil {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, tc.file, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			lt := startLoopWith(t, output.LoopOptions{HeldFile: path, KeyDown: tc.keyDown}, tc.prepare, nil)
			res := lt.l.Swept()
			switch {
			case tc.err == nil && res.Err != nil:
				t.Errorf("Swept().Err = %v, want nil", res.Err)
			case tc.err != nil && !errors.Is(res.Err, tc.err):
				t.Errorf("Swept().Err = %v, want one wrapping %v", res.Err, tc.err)
			}
			if res.Off {
				t.Error("Swept().Off, want the log on")
			}
			if res.Reset != tc.reset {
				t.Errorf("Swept().Reset = %v, want %v", res.Reset, tc.reset)
			}
			if got := eventsTrace(res.Released); got != tc.released {
				t.Errorf("Swept().Released = %q, want %q", got, tc.released)
			}
			wantHeld(t, path, tc.logAfter...)
			lt.do(tap(ownerA, "F13", 0))
			want := strings.TrimSpace(tc.out + " +F13@0 -F13@0")
			if got := lt.rec.Trace(); got != want {
				t.Errorf("output:\n got %s\nwant %s", got, want)
			}
			wantHeld(t, path, tc.logAfter...)
			if err := lt.stop(); err != nil {
				t.Fatal(err)
			}
			if len(tc.logAfter) == 0 {
				wantNoHeld(t, path)
			}
		})
	}
}

// TestLoopSweepCarried: a key the sweep could not release stays in the
// log while other keys go down and up, until a release of it goes out;
// otherwise Stop releases it once when KeyDown reports it down, and keeps
// it in the log when not.
func TestLoopSweepCarried(t *testing.T) {
	start := func(t *testing.T, kd *keyDownSet) (*loopTest, string) {
		t.Helper()
		path := heldPath(t)
		writeHeld(t, path, "LCtrl")
		lt := startLoopWith(t, output.LoopOptions{HeldFile: path, KeyDown: kd.down},
			func(r *simtest.Recorder) { r.FailNext(0, errBlocked) }, nil)
		if res := lt.l.Swept(); !errors.Is(res.Err, errBlocked) || len(res.Released) > 0 || !strings.Contains(res.Err.Error(), "LCtrl") {
			t.Fatalf("Swept() = %+v, want the release of LCtrl failed", res)
		}
		wantHeld(t, path, "LCtrl")
		lt.do(down(ownerA, "Z"))
		wantHeld(t, path, "LCtrl", "Z")
		lt.do(up(ownerA, "Z"))
		wantHeld(t, path, "LCtrl")
		return lt, path
	}
	t.Run("released by a command", func(t *testing.T) {
		lt, path := start(t, downSet(t, "LCtrl"))
		lt.do(down(ownerA, "Ctrl"))
		wantHeld(t, path, "LCtrl")
		lt.do(up(ownerA, "Ctrl"))
		wantHeld(t, path)
		if err := lt.stop(); err != nil {
			t.Fatal(err)
		}
		wantNoHeld(t, path)
		if got, want := lt.rec.Trace(), "+Z@0 -Z@0 +LCtrl@0 -LCtrl@0"; got != want {
			t.Errorf("output: %s, want %s", got, want)
		}
	})
	t.Run("released by Stop", func(t *testing.T) {
		lt, path := start(t, downSet(t, "LCtrl"))
		if err := lt.stop(); err != nil {
			t.Fatal(err)
		}
		wantNoHeld(t, path)
		if got, want := lt.rec.Trace(), "+Z@0 -Z@0 -LCtrl@0"; got != want {
			t.Errorf("output: %s, want %s", got, want)
		}
	})
	t.Run("up at Stop", func(t *testing.T) {
		kd := downSet(t, "LCtrl")
		lt, path := start(t, kd)
		kd.set(t)
		if err := lt.stop(); err != nil {
			t.Fatal(err)
		}
		wantHeld(t, path, "LCtrl")
		if got, want := lt.rec.Trace(), "+Z@0 -Z@0"; got != want {
			t.Errorf("output: %s, want %s", got, want)
		}
	})
	t.Run("Stop fails", func(t *testing.T) {
		opts, errs := errorsTo(t)
		path := heldPath(t)
		writeHeld(t, path, "LCtrl")
		opts.HeldFile, opts.KeyDown = path, downSet(t, "LCtrl").down
		lt := startLoopWith(t, opts, func(r *simtest.Recorder) {
			r.FailNext(0, errBlocked)
			r.FailNext(0, errBlocked)
		}, nil)
		lt.l.Swept()
		if err := lt.stop(); err != nil {
			t.Fatal(err)
		}
		wantHeld(t, path, "LCtrl")
		if len(errs) != 1 {
			t.Fatalf("%d errors, want the carried release's", len(errs))
		}
		wantErr(t, <-errs, errBlocked, "LCtrl", "last run")
	})
}

// TestLoopSweepCarriedGate: a closing BlockFor, of the pause, of the
// system's suspension or of padmatectl release-all, releases the keys the
// sweep could not that KeyDown reports down, as Stop does, after the
// Arbiter's ReleaseAll, and takes them off the log. A release that fails
// goes to Errors and keeps the key carried for the next one; a key
// KeyDown reports up stays in the log.
func TestLoopSweepCarriedGate(t *testing.T) {
	start := func(t *testing.T, kd *keyDownSet) (*loopTest, string, <-chan error) {
		t.Helper()
		opts, errs := errorsTo(t)
		path := heldPath(t)
		writeHeld(t, path, "LCtrl")
		opts.HeldFile, opts.KeyDown = path, kd.down
		lt := startLoopWith(t, opts, func(r *simtest.Recorder) { r.FailNext(0, errBlocked) }, nil)
		if res := lt.l.Swept(); !errors.Is(res.Err, errBlocked) || len(res.Released) > 0 {
			t.Fatalf("Swept() = %+v, want the release of LCtrl failed", res)
		}
		wantHeld(t, path, "LCtrl")
		return lt, path, errs
	}
	for _, b := range []struct {
		name string
		b    output.Blocker
	}{{"pause", output.BlockUser}, {"suspension", output.BlockSystem}, {"release-all", output.BlockEmergency}} {
		t.Run(b.name, func(t *testing.T) {
			lt, path, errs := start(t, downSet(t, "LCtrl"))
			lt.do(down(ownerA, "Z"))
			lt.l.BlockFor(b.b, true)
			lt.sync()
			if got, want := lt.rec.Trace(), "+Z@0 -Z@0 -LCtrl@0"; got != want {
				t.Errorf("output: %s, want %s", got, want)
			}
			wantHeld(t, path)
			// Released once: the next close has nothing carried.
			lt.l.BlockFor(b.b, false)
			lt.l.BlockFor(b.b, true)
			lt.sync()
			lt.l.BlockFor(b.b, false)
			if err := lt.stop(); err != nil {
				t.Fatal(err)
			}
			if got, want := lt.rec.Trace(), "+Z@0 -Z@0 -LCtrl@0"; got != want {
				t.Errorf("output after Stop: %s, want %s", got, want)
			}
			wantNoHeld(t, path)
			if len(errs) > 0 {
				t.Errorf("Errors got %v", <-errs)
			}
		})
	}
	t.Run("fails", func(t *testing.T) {
		lt, path, errs := start(t, downSet(t, "LCtrl"))
		lt.rec.With(func(r *simtest.Recorder) { r.FailNext(0, errBlocked) })
		lt.l.Block(true)
		lt.sync()
		if len(errs) != 1 {
			t.Fatalf("%d errors, want the carried release's", len(errs))
		}
		wantErr(t, <-errs, errBlocked, "LCtrl", "last run")
		wantHeld(t, path, "LCtrl")
		lt.l.Block(true) // a close on a closed owner releases again
		lt.sync()
		if got, want := lt.rec.Trace(), "-LCtrl@0"; got != want {
			t.Errorf("output: %s, want %s", got, want)
		}
		wantHeld(t, path)
		lt.l.Block(false)
	})
	t.Run("up", func(t *testing.T) {
		kd := downSet(t, "LCtrl")
		lt, path, errs := start(t, kd)
		kd.set(t)
		lt.l.Block(true)
		lt.sync()
		lt.l.Block(false)
		if got := lt.rec.Trace(); got != "" {
			t.Errorf("output: %s, want nothing", got)
		}
		wantHeld(t, path, "LCtrl")
		if len(errs) > 0 {
			t.Errorf("Errors got %v", <-errs)
		}
	})
}

// TestLoopHeldLogStopAndPanic: the held-key log after Stop and after a
// panic keeps exactly the keys that may be held, and its errors go to
// Errors on the Stop path and to PanicError.HeldErr after a panic.
func TestLoopHeldLogStopAndPanic(t *testing.T) {
	noKeys := func(output.KeyEvent) bool { return false }
	t.Run("Stop fails", func(t *testing.T) {
		path := heldPath(t)
		lt := startLoop(t, output.LoopOptions{HeldFile: path, KeyDown: noKeys})
		lt.do(down(ownerA, "Space"))
		lt.rec.With(func(r *simtest.Recorder) {
			for range 2 + 3 {
				r.FailNext(0, errBlocked)
			}
		})
		wantErr(t, lt.stop(), errBlocked, "Space")
		wantHeld(t, path, "Space")
		releaseDown(t, lt.rec)
	})
	t.Run("Sender panics", func(t *testing.T) {
		path := heldPath(t)
		fake := clock.NewFake()
		rec := simtest.NewSyncRecorder(fake)
		l := output.StartLoop(fake, &panicSender{rec: rec, panics: []int{1}}, output.LoopOptions{HeldFile: path, KeyDown: noKeys})
		if err := l.Do(down(ownerA, "Space")); err != nil {
			t.Fatal(err)
		}
		checkPanic(t, l, l.Do(down(ownerA, "Z")), "boom 1", "-Z -Space", nil)
		var pe *output.PanicError
		if !errors.As(l.Err(), &pe) || pe.HeldErr != nil {
			t.Errorf("HeldErr = %v, want nil", pe.HeldErr)
		}
		wantNoHeld(t, path)
		releaseDown(t, rec)
	})
	t.Run("release fails", func(t *testing.T) {
		path := heldPath(t)
		fake := clock.NewFake()
		rec := simtest.NewSyncRecorder(fake)
		l := output.StartLoop(fake, rec, output.LoopOptions{HeldFile: path, KeyDown: noKeys})
		if err := l.Do(down(ownerA, "Space")); err != nil {
			t.Fatal(err)
		}
		rec.With(func(r *simtest.Recorder) { r.FailNext(0, errBlocked) })
		err := l.Do(func(*output.Arbiter) error { panic("boom") })
		checkPanic(t, l, err, "boom", "", errBlocked, "Space")
		wantHeld(t, path, "Space")
		releaseDown(t, rec)
	})
	// The release after a panic also releases the keys the sweep could not
	// that KeyDown reports down, after the ledger's, as Stop does, and
	// takes those that went out off the log (invariant 1: padmated exits
	// after a panic on T-output).
	t.Run("carried", func(t *testing.T) {
		start := func(t *testing.T, keyDown func(output.KeyEvent) bool, fails int) (*loopTest, string) {
			t.Helper()
			path := heldPath(t)
			writeHeld(t, path, "LCtrl")
			lt := startLoopWith(t, output.LoopOptions{HeldFile: path, KeyDown: keyDown}, func(r *simtest.Recorder) {
				for range fails {
					r.FailNext(0, errBlocked)
				}
			}, nil)
			if res := lt.l.Swept(); !errors.Is(res.Err, errBlocked) || len(res.Released) > 0 {
				t.Fatalf("Swept() = %+v, want the release of LCtrl failed", res)
			}
			wantHeld(t, path, "LCtrl")
			return lt, path
		}
		boom := func(*output.Arbiter) error { panic("boom") }
		t.Run("released", func(t *testing.T) {
			lt, path := start(t, downSet(t, "LCtrl").down, 1)
			lt.do(down(ownerA, "Space"))
			checkPanic(t, lt.l, lt.l.Do(boom), "boom", "-Space -LCtrl", nil)
			wantNoHeld(t, path)
			if got, want := lt.rec.Trace(), "+Space@0 -Space@0 -LCtrl@0"; got != want {
				t.Errorf("output: %s, want %s", got, want)
			}
		})
		t.Run("fails", func(t *testing.T) {
			lt, path := start(t, downSet(t, "LCtrl").down, 2)
			checkPanic(t, lt.l, lt.l.Do(boom), "boom", "", errBlocked, "LCtrl", "last run", "may be held")
			wantHeld(t, path, "LCtrl")
		})
		t.Run("up", func(t *testing.T) {
			kd := downSet(t, "LCtrl")
			lt, path := start(t, kd.down, 1)
			kd.set(t)
			checkPanic(t, lt.l, lt.l.Do(boom), "boom", "", nil)
			wantHeld(t, path, "LCtrl")
			if got := lt.rec.Trace(); got != "" {
				t.Errorf("output: %s, want nothing", got)
			}
		})
		t.Run("KeyDown panics", func(t *testing.T) {
			var after atomic.Bool
			lt, path := start(t, func(e output.KeyEvent) bool {
				if after.Load() {
					panic("boom in KeyDown")
				}
				return !output.IsMenuMask(e)
			}, 1)
			after.Store(true)
			checkPanic(t, lt.l, lt.l.Do(boom), "boom", "", nil, "boom in KeyDown", "LCtrl", "may be held")
			wantHeld(t, path, "LCtrl")
		})
		t.Run("Sender panics", func(t *testing.T) {
			path := heldPath(t)
			writeHeld(t, path, "LCtrl")
			fake := clock.NewFake()
			rec := simtest.NewSyncRecorder(fake)
			rec.With(func(r *simtest.Recorder) { r.FailNext(0, errBlocked) })
			// Calls: 0 the sweep, 1 +Space, 2 +Z, which panics and so counts
			// as down, 3 -Z -Space, 4 -LCtrl.
			ps := &panicSender{rec: rec, panics: []int{2, 4}}
			l := output.StartLoop(fake, ps, output.LoopOptions{HeldFile: path, KeyDown: downSet(t, "LCtrl").down})
			l.Swept()
			if err := l.Do(down(ownerA, "Space")); err != nil {
				t.Fatal(err)
			}
			checkPanic(t, l, l.Do(down(ownerA, "Z")), "boom 2", "-Z -Space", nil, "boom 4", "LCtrl", "last run")
			wantHeld(t, path, "LCtrl")
			releaseDown(t, rec)
		})
	})
	t.Run("errors", func(t *testing.T) {
		t.Run("Stop", func(t *testing.T) {
			hf := output.FaultHeld(t)
			opts, errs := errorsTo(t)
			path := heldPath(t)
			opts.HeldFile, opts.KeyDown = path, noKeys
			lt := startLoop(t, opts)
			lt.l.Swept()
			lt.do(down(ownerA, "Space"))
			hf.FailWrites(errDisk) // the log's write after the release
			hf.FailRemove(errDisk)
			if err := lt.stop(); err != nil {
				t.Fatalf("Stop = %v, want nil: the release went out", err)
			}
			var got []error
			for len(errs) > 0 {
				got = append(got, <-errs)
			}
			if len(got) != 2 || !errors.Is(got[0], errDisk) || !errors.Is(got[1], errDisk) || !strings.Contains(got[1].Error(), "remove") {
				t.Errorf("Errors got %v, want the failed write and the failed removal", got)
			}
			wantHeld(t, path)
		})
		t.Run("panic", func(t *testing.T) {
			hf := output.FaultHeld(t)
			opts, errs := errorsTo(t)
			path := heldPath(t)
			opts.HeldFile, opts.KeyDown = path, noKeys
			fake := clock.NewFake()
			rec := simtest.NewSyncRecorder(fake)
			l := output.StartLoop(fake, rec, opts)
			if err := l.Do(down(ownerA, "Space")); err != nil {
				t.Fatal(err)
			}
			hf.FailClose(errDisk)
			rec.With(func(r *simtest.Recorder) { r.FailNext(0, errBlocked) })
			err := l.Do(func(*output.Arbiter) error { panic("boom") })
			checkPanic(t, l, err, "boom", "", errBlocked, "Space")
			var pe *output.PanicError
			errors.As(l.Err(), &pe)
			if !errors.Is(pe.HeldErr, errDisk) || errors.Is(pe.ReleaseErr, errDisk) {
				t.Errorf("HeldErr = %v, ReleaseErr = %v; want the close's error in HeldErr only", pe.HeldErr, pe.ReleaseErr)
			}
			if !strings.Contains(pe.Error(), "held-key log") || !strings.Contains(pe.Error(), pe.HeldErr.Error()) {
				t.Errorf("error %q leaves out HeldErr", pe)
			}
			if len(errs) > 0 {
				t.Errorf("Errors got %v, want nothing after a panic", <-errs)
			}
			wantHeld(t, path, "Space")
			releaseDown(t, rec)
		})
	})
	t.Run("Errors panics after finish", func(t *testing.T) {
		hf := output.FaultHeld(t)
		path := heldPath(t)
		opts := output.LoopOptions{HeldFile: path, KeyDown: noKeys, Errors: func(err error) {
			if err != nil {
				panic("boom in Errors")
			}
		}}
		lt := startLoop(t, opts)
		lt.do(down(ownerA, "Space"))
		lt.rec.With(func(r *simtest.Recorder) { failRelease(r, 0) })
		err := lt.stop()
		var pe *output.PanicError
		if !errors.As(err, &pe) || pe.Value != "boom in Errors" {
			t.Fatalf("Stop = %v, want the panic of Errors", err)
		}
		if pe.HeldErr != nil {
			t.Errorf("HeldErr = %v, want nil: the log was finished before", pe.HeldErr)
		}
		if n := hf.Closes(); n != 1 {
			t.Errorf("the log was closed %d times, want 1", n)
		}
		wantNoHeld(t, path)
		if got := lt.rec.Trace(); got != "+Space@0 -Space@100" {
			t.Errorf("output: %s, want +Space@0 -Space@100", got)
		}
	})
	t.Run("close panics", func(t *testing.T) {
		hf := output.FaultHeld(t)
		path := heldPath(t)
		lt := startLoop(t, output.LoopOptions{HeldFile: path, KeyDown: noKeys})
		lt.l.Swept()
		hf.PanicClose()
		var pe *output.PanicError
		if err := lt.stop(); !errors.As(err, &pe) {
			t.Fatalf("Stop = %v, want the panic of Close", err)
		}
		if pe.HeldErr != nil {
			t.Errorf("HeldErr = %v, want nil: the second finish does nothing", pe.HeldErr)
		}
		if n := hf.Closes(); n != 1 {
			t.Errorf("the log was closed %d times, want 1", n)
		}
	})
}

// TestHeldLogWriteFails: a write of the log that fails is reported and
// the keys still go out; the next write that succeeds puts the whole log
// right.
func TestHeldLogWriteFails(t *testing.T) {
	hf := output.FaultHeld(t)
	opts, errs := errorsTo(t)
	path := heldPath(t)
	opts.HeldFile, opts.KeyDown = path, func(output.KeyEvent) bool { return false }
	lt := startLoop(t, opts)
	lt.l.Swept()
	hf.FailWrites(errDisk, errDisk) // the mark and the settle of the press
	lt.do(down(ownerA, "Space"))
	if got := lt.rec.Trace(); got != "+Space@0" {
		t.Errorf("output: %s, want +Space@0", got)
	}
	for range 2 {
		wantErr(t, <-errs, errDisk, "held-key log")
	}
	wantHeld(t, path)
	lt.do(down(ownerA, "Z"))
	wantHeld(t, path, "Space", "Z")
	lt.do(up(ownerA, "Z"))
	lt.do(up(ownerA, "Space"))
	wantHeld(t, path)
	if len(errs) > 0 {
		t.Errorf("Errors got %v too", <-errs)
	}
}

// TestHeldLogNoAllocs: the writes of the held-key log before and after a
// Send do not allocate.
func TestHeldLogNoAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector may allocate; the other CI jobs run this")
	}
	h, err := output.OpenHeldLog(heldPath(t))
	if err != nil {
		t.Fatal(err)
	}
	f13 := press(t, "F13")
	downs, ups := []output.KeyEvent{f13}, []output.KeyEvent{{Key: f13.Key, Scan: f13.Scan, Up: true}}
	fail := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	n := testing.AllocsPerRun(100, func() {
		fail(h.Mark(downs))
		fail(h.Settle(downs, downs))
		fail(h.Mark(ups))
		fail(h.Settle(nil, ups))
	})
	if n != 0 {
		t.Errorf("%v allocations per run, want 0", n)
	}
	if err := h.Finish(nil); err != nil {
		t.Fatal(err)
	}
}

// TestHeldReleasesNames: the sweep names the keys of the log as their
// sided KeySpec, the other keys before the modifiers.
func TestHeldReleasesNames(t *testing.T) {
	ups, err := output.HeldReleases(output.EncodeHeld(press(t, "Ctrl"), press(t, "Z"), press(t, "RAlt"),
		press(t, "Pause"), press(t, "Num1"), output.KeyEvent{Scan: 0x59}))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := eventsTrace(ups), "-Z -sc:0x59 -Pause -Num1 -LCtrl -RAlt"; got != want {
		t.Errorf("releases %q, want %q", got, want)
	}
	for _, e := range ups {
		if !e.Up {
			t.Errorf("%v is not a release", e)
		}
	}
}

// TestLoopSweepPanics: a panic in the sweep, in KeyDown or in the Sender,
// ends the Loop, and Swept reports it. The release after the panic tries
// the keys of the last run that were not reported up or released once
// more (PanicError.Released, ReleaseErr), and the log keeps those it did
// not release.
func TestLoopSweepPanics(t *testing.T) {
	for _, tc := range []struct {
		name       string
		keyDown    func(output.KeyEvent) bool
		panics     []int
		log        []string
		released   string
		releaseErr []string
		trace      string
	}{
		{"KeyDown", func(e output.KeyEvent) bool {
			if keyName(e) == "Z" {
				panic("boom in KeyDown")
			}
			return false
		}, nil, []string{"LCtrl", "Z"}, "", []string{"boom in KeyDown", "LCtrl", "Z", "last run"}, ""},
		{"Sender", func(e output.KeyEvent) bool { return !output.IsMenuMask(e) }, []int{0}, nil, "-Z -LCtrl", nil, "-Z@0 -LCtrl@0"},
		{"Sender twice", func(e output.KeyEvent) bool { return !output.IsMenuMask(e) }, []int{0, 1}, []string{"LCtrl", "Z"}, "", []string{"boom 1", "LCtrl", "Z", "last run"}, ""},
		{"Sender after", func(e output.KeyEvent) bool { return keyName(e) == "LCtrl" }, []int{0, 1}, []string{"LCtrl"}, "", []string{"boom 1", "LCtrl"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := heldPath(t)
			writeHeld(t, path, "LCtrl", "Z")
			fake := clock.NewFake()
			rec := simtest.NewSyncRecorder(fake)
			l := output.StartLoop(fake, &panicSender{rec: rec, panics: tc.panics}, output.LoopOptions{HeldFile: path, KeyDown: tc.keyDown})
			res := l.Swept()
			var pe *output.PanicError
			if !errors.Is(res.Err, output.ErrLoopClosed) || !errors.As(res.Err, &pe) || len(res.Released) > 0 {
				t.Errorf("Swept() = %+v, want ErrLoopClosed and the *PanicError", res)
			}
			waitDone(t, l)
			if err := l.Do(noop); !errors.Is(err, output.ErrLoopClosed) {
				t.Errorf("Do after the panic = %v, want ErrLoopClosed", err)
			}
			if pe != nil {
				if got := eventsTrace(pe.Released); got != tc.released {
					t.Errorf("Released = %q, want %q", got, tc.released)
				}
				checkReleaseErr(t, pe, nil, tc.releaseErr...)
			}
			if got, want := rec.Trace(), tc.trace; got != want {
				t.Errorf("output: %q, want %q", got, want)
			}
			if tc.log == nil {
				wantNoHeld(t, path) // no key left: finish removed it
			} else {
				wantHeld(t, path, tc.log...)
			}
		})
	}
}

// A wheel notch leaves nothing down, so the held-key log never has a wheel
// key, though the Loop sends the press and the release of it like any key's:
// the modifier of Ctrl+WheelUp is the only key the log keeps (ADR-0062).
func TestLoopHeldLogSkipsWheelKeys(t *testing.T) {
	path := heldPath(t)
	lt := startLoopWith(t, output.LoopOptions{HeldFile: path, KeyDown: func(output.KeyEvent) bool { return false }}, nil, nil)
	if res := lt.l.Swept(); res.Err != nil || res.Off || len(res.Released) > 0 {
		t.Fatalf("Swept() = %+v, want nothing done", res)
	}
	lt.do(down(ownerA, "Ctrl+WheelUp"))
	wantHeld(t, path, "LCtrl")
	lt.do(down(ownerB, "WheelDown"))
	lt.do(tap(ownerB, "WheelLeft", 0))
	wantHeld(t, path, "LCtrl")
	lt.do(up(ownerB, "WheelDown"))
	lt.do(up(ownerA, "Ctrl+WheelUp"))
	wantHeld(t, path)
	if err := lt.stop(); err != nil {
		t.Fatal(err)
	}
	wantNoHeld(t, path)
	if got := lt.rec.Trace(); got != "+LCtrl@0 +WheelUp@0 +WheelDown@0 +WheelLeft@0 -WheelLeft@0 -WheelDown@0 -WheelUp@0 -LCtrl@0" {
		t.Errorf("output: %s", got)
	}
}

// A wheel key has no bit in the log, so a log cannot name one: encoding a
// wheel press sets nothing, and the sweep's releases have none.
func TestHeldLogNeverHasWheelKeys(t *testing.T) {
	for _, name := range []string{"WheelUp", "WheelDown", "WheelLeft", "WheelRight"} {
		b := output.EncodeHeld(press(t, name), press(t, "LCtrl"))
		ids, err := output.DecodeHeld(b)
		if err != nil || len(ids) != 1 || ids[0] != press(t, "LCtrl").ID() {
			t.Errorf("a log with %s and LCtrl has %v, %v; want only LCtrl", name, ids, err)
		}
		ups, err := output.HeldReleases(b)
		if err != nil || len(ups) != 1 || ups[0].Mouse != keys.MouseNone {
			t.Errorf("the sweep of a log with %s releases %v, %v; want only LCtrl", name, ups, err)
		}
	}
}
