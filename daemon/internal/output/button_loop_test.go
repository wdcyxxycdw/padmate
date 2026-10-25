package output_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// A mouse button held by a binding must go up on every path that ends the
// binding's hold (CLAUDE.md invariant 1). These tests run the exit paths of
// the T-output Loop with a button held, next to the keyboard ones of
// loop_test.go: Stop (the daemon's exit), a panic in a command or in the
// Sender, the gate that pause, the system's suspension and the emergency
// release close, and ReleaseOwner/ReleaseOwners (a profile switch, a reload,
// a device that went away). The recorders of the tests fail the test when a
// button is left down.

// holdButtons presses the buttons the exit-path tests release: a plain
// button, a button under Ctrl, and a side button by a second owner that
// shares the plain one.
func holdButtons(lt *loopTest) {
	lt.do(down(ownerA, "MouseLeft"))
	lt.do(down(ownerB, "Ctrl+MouseRight"))
	lt.do(down(ownerS, "XButton1"))
	lt.do(down(ownerB, "MouseLeft"))
}

const buttonsHeld = "+MouseLeft@0 +LCtrl@0 +MouseRight@0 +MouseX1@0"

func TestLoopStopReleasesButtons(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	holdButtons(lt)
	if err := lt.stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Non-modifiers first, then modifiers, each in reverse press order.
	if got, want := lt.rec.Trace(), buttonsHeld+" -MouseX1@0 -MouseRight@0 -MouseLeft@0 -LCtrl@0"; got != want {
		t.Errorf("output:\n got %s\nwant %s", got, want)
	}
	lt.rec.With(func(r *simtest.Recorder) {
		if d := r.Down(); len(d) > 0 {
			t.Errorf("keys down after Stop: %v", d)
		}
	})
}

// The gate: Block(true) (the pause), BlockFor(BlockSystem) (lock, sleep, the
// secure desktop) and BlockFor(BlockEmergency) (the offline release-all) each
// release every held button on the loop and then press none.
func TestLoopBlockReleasesButtons(t *testing.T) {
	for _, tc := range []struct {
		name  string
		block func(l *output.Loop, on bool)
		who   output.Blocker
	}{
		{"pause", func(l *output.Loop, on bool) { l.Block(on) }, output.BlockUser},
		{"system", func(l *output.Loop, on bool) { l.BlockFor(output.BlockSystem, on) }, output.BlockSystem},
		{"emergency", func(l *output.Loop, on bool) { l.BlockFor(output.BlockEmergency, on) }, output.BlockEmergency},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lt := startLoop(t, output.LoopOptions{})
			holdButtons(lt)
			tc.block(lt.l, true)
			blocking(t, lt.l, tc.who)
			lt.do(func(a *output.Arbiter) error {
				if held := a.Held(); len(held) > 0 {
					t.Errorf("%d keys held after the gate closed: %v", len(held), held)
				}
				return errors.Join(blocked(true)(a), a.Down(ownerA, keys.MustParse("MouseMiddle")), a.Tap(ownerB, keys.MustParse("MouseLeft"), 0))
			})
			if got, want := lt.rec.Trace(), buttonsHeld+" -MouseX1@0 -MouseRight@0 -MouseLeft@0 -LCtrl@0"; got != want {
				t.Errorf("output:\n got %s\nwant %s", got, want)
			}
			lt.do(statBlocked(2))
			lt.rec.With(func(r *simtest.Recorder) {
				if d := r.Down(); len(d) > 0 {
					t.Errorf("keys down while the gate is closed: %v", d)
				}
			})
			tc.block(lt.l, false)
			blocking(t, lt.l, 0)
			lt.do(blocked(false))
			// After the gate opens, a press works again and still ends.
			lt.do(down(ownerA, "MouseMiddle"))
			lt.do(up(ownerA, "MouseMiddle"))
		})
	}
}

// A panic in a command, a Goexit or a panic in the Sender ends the Loop; the
// ledger releases every button that went down, from its own record, in reverse
// press order, also one whose press may or may not have gone out.
func TestLoopPanicReleasesButtons(t *testing.T) {
	t.Run("command", func(t *testing.T) {
		for _, end := range []struct {
			name  string
			value string
			f     func()
		}{
			{"panic", "boom", func() { panic("boom") }},
			{"Goexit", "runtime.Goexit", runtime.Goexit},
		} {
			t.Run(end.name, func(t *testing.T) {
				fake := clock.NewFake()
				rec := simtest.NewSyncRecorder(fake)
				l := output.StartLoop(fake, &panicSender{rec: rec}, output.LoopOptions{})
				err := l.Do(func(a *output.Arbiter) error {
					if err := a.Down(ownerA, keys.MustParse("Shift+MouseLeft")); err != nil {
						return err
					}
					if err := a.Down(ownerB, keys.MustParse("MouseX2")); err != nil {
						return err
					}
					end.f()
					return errors.New("not reached")
				})
				checkPanic(t, l, err, end.value, "-MouseX2 -MouseLeft -LShift", nil)
				if got, want := rec.Trace(), "+LShift@0 +MouseLeft@0 +MouseX2@0 -MouseX2@0 -MouseLeft@0 -LShift@0"; got != want {
					t.Errorf("output:\n got %s\nwant %s", got, want)
				}
				releaseDown(t, rec)
			})
		}
	})

	for _, tc := range []struct {
		name     string
		after    bool
		out      string
		released string
	}{
		// The ledger cannot tell whether the call that panicked got through,
		// so it releases the button it carried either way.
		{"send panics before sending", false, "+MouseRight@0 -MouseLeft@0 -MouseRight@0", "-MouseLeft -MouseRight"},
		{"send panics after sending", true, "+MouseRight@0 +MouseLeft@0 -MouseLeft@0 -MouseRight@0", "-MouseLeft -MouseRight"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := clock.NewFake()
			rec := simtest.NewSyncRecorder(fake)
			l := output.StartLoop(fake, &panicSender{rec: rec, panics: []int{1}, after: tc.after}, output.LoopOptions{})
			if err := l.Do(down(ownerA, "MouseRight")); err != nil {
				t.Fatal(err)
			}
			checkPanic(t, l, l.Do(down(ownerB, "MouseLeft")), "boom 1", tc.released, nil)
			if got := rec.Trace(); got != tc.out {
				t.Errorf("output:\n got %s\nwant %s", got, tc.out)
			}
			releaseDown(t, rec)
		})
	}
}

// RemoteArbiter.ReleaseOwner is what a profile switch, a reload and the
// removal of a device end up calling: it lets go of that owner's buttons and
// leaves another owner's alone.
func TestRemoteReleaseOwnerButtons(t *testing.T) {
	lt := startLoop(t, output.LoopOptions{})
	holdButtons(lt)
	r := lt.l.Remote()
	if err := r.ReleaseOwner(ownerB); err != nil {
		t.Fatal(err)
	}
	// B held Ctrl+MouseRight and shared MouseLeft with A.
	if got, want := lt.rec.Trace(), buttonsHeld+" -MouseRight@0 -LCtrl@0"; got != want {
		t.Errorf("after B's release:\n got %s\nwant %s", got, want)
	}
	if err := r.ReleaseOwners(func(o output.Owner) bool { return o.Slot == 0 }); err != nil {
		t.Fatal(err)
	}
	if got, want := lt.rec.Trace(), buttonsHeld+" -MouseRight@0 -LCtrl@0 -MouseX1@0 -MouseLeft@0"; got != want {
		t.Errorf("after slot 0's release:\n got %s\nwant %s", got, want)
	}
	lt.rec.With(func(r *simtest.Recorder) {
		if d := r.Down(); len(d) > 0 {
			t.Errorf("keys down: %v", d)
		}
	})
}

// The held-key log of OUT-14 keeps the buttons in the VK slots of its third
// bitmap, VK_LBUTTON 0x01, VK_RBUTTON 0x02, VK_MBUTTON 0x04, VK_XBUTTON1 0x05
// and VK_XBUTTON2 0x06, with the format and version unchanged. The wheel keys
// are not in it.
func TestHeldLogButtonBits(t *testing.T) {
	b := output.EncodeHeld(press(t, "MouseLeft"), press(t, "RButton"), press(t, "MouseMiddle"), press(t, "XButton1"), press(t, "MouseX2"))
	if len(b) != output.HeldSize || string(b[:4]) != "PDMH" || b[4] != 4 {
		t.Errorf("log header %q version %d size %d, want the unchanged format (PDMH, version 4, %d bytes)", b[:4], b[4], len(b), output.HeldSize)
	}
	// The bitmap of the VKs is the last 32 bytes: the buttons are bits 1, 2, 4,
	// 5 and 6 of its first byte, and nothing else is set anywhere.
	vks := b[len(b)-32:]
	if vks[0] != 0b0111_0110 {
		t.Errorf("first byte of the VK bitmap = %#08b, want bits 1, 2, 4, 5 and 6", vks[0])
	}
	for i, v := range vks[1:] {
		if v != 0 {
			t.Errorf("VK bitmap byte %d = %#x, want 0", i+1, v)
		}
	}
	for i, v := range b[len(b)-96 : len(b)-32] {
		if v != 0 {
			t.Errorf("scan code bitmap byte %d = %#x, want 0", i, v)
		}
	}

	ids, err := output.DecodeHeld(b)
	if err != nil {
		t.Fatal(err)
	}
	want := []output.KeyID{{Mouse: keys.MouseLeft}, {Mouse: keys.MouseRight}, {Mouse: keys.MouseMiddle}, {Mouse: keys.MouseX1}, {Mouse: keys.MouseX2}}
	if len(ids) != len(want) {
		t.Fatalf("DecodeHeld = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("DecodeHeld[%d] = %v, want %v", i, ids[i], want[i])
		}
	}

	// The sweep's releases: real mouse button releases (not keyboard events
	// with those VKs), after the other keys and before the modifiers, named.
	ups, err := output.HeldReleases(output.EncodeHeld(press(t, "Ctrl"), press(t, "Z"), press(t, "MouseX2"), press(t, "MouseLeft"), press(t, "Pause")))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := eventsTrace(ups), "-Z -MouseLeft -MouseX2 -Pause -LCtrl"; got != want {
		t.Errorf("releases %q, want %q", got, want)
	}
	for _, e := range ups {
		if !e.Up || e.ByVK && e.Key.Kind() == keys.KindInvalid {
			t.Errorf("%+v is not a proper release", e)
		}
	}
	if m := ups[1].Mouse; m != keys.MouseLeft || ups[1].ByVK || ups[1].VK != 0 {
		t.Errorf("the sweep's release of MouseLeft is %+v, want a mouse event", ups[1])
	}
}

// A wheel key still has no bit, next to the buttons.
func TestHeldLogButtonsNotWheels(t *testing.T) {
	b := output.EncodeHeld(press(t, "WheelUp"), press(t, "MouseLeft"), press(t, "WheelDown"))
	ids, err := output.DecodeHeld(b)
	if err != nil || len(ids) != 1 || ids[0] != press(t, "MouseLeft").ID() {
		t.Errorf("a log with two wheel keys and MouseLeft has %v, %v; want only MouseLeft", ids, err)
	}
}

// The Loop writes the buttons into the held-key log before each Send, as it
// does the keys: a button that went down is in the log at once, and gone from
// it once released; Stop removes the log.
func TestLoopHeldLogRecordsButtons(t *testing.T) {
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
	lt.do(down(ownerA, "MouseLeft"))
	wantHeld(t, path, "MouseLeft")
	lt.do(down(ownerB, "Ctrl+MouseRight"))
	wantHeld(t, path, "MouseLeft", "LCtrl", "MouseRight")
	lt.do(tap(ownerS, "XButton2", 0))
	wantHeld(t, path, "MouseLeft", "LCtrl", "MouseRight")
	lt.do(up(ownerB, "Ctrl+MouseRight"))
	wantHeld(t, path, "MouseLeft")
	lt.do(up(ownerA, "MouseLeft"))
	wantHeld(t, path)
	lt.do(down(ownerA, "MouseX1"))
	wantHeld(t, path, "MouseX1")
	if err := lt.stop(); err != nil {
		t.Fatal(err)
	}
	wantNoHeld(t, path)
	if got := lt.rec.Trace(); !strings.HasSuffix(got, "+MouseX1@0 -MouseX1@0") {
		t.Errorf("output: %s", got)
	}
}

// The startup sweep releases a button the last run left down when the system
// still has it down (GetAsyncKeyState by VK_LBUTTON and its kin), through the
// same MenuMask and Sender as any key, and forgets one the system has up.
func TestLoopSweepButtons(t *testing.T) {
	for _, tc := range []struct {
		name     string
		down     []string // the keys KeyDown reports down
		out      string
		released string
		logAfter []string
		fail     bool
	}{
		{name: "left down", down: []string{"MouseLeft"}, out: "-MouseLeft@0", released: "-MouseLeft"},
		{name: "all down", down: []string{"MouseLeft", "MouseRight", "MouseMiddle", "MouseX1", "MouseX2", "LCtrl"},
			out: "-MouseLeft@0 -MouseRight@0 -MouseMiddle@0 -MouseX1@0 -MouseX2@0 -LCtrl@0", released: "-MouseLeft -MouseRight -MouseMiddle -MouseX1 -MouseX2 -LCtrl"},
		{name: "only right still down", down: []string{"MouseRight"}, out: "-MouseRight@0", released: "-MouseRight"},
		{name: "none down"},
		{name: "send fails", down: []string{"MouseLeft", "MouseRight"}, fail: true,
			logAfter: []string{"MouseLeft", "MouseRight"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := heldPath(t)
			writeHeld(t, path, "MouseLeft", "MouseRight", "MouseMiddle", "MouseX1", "MouseX2", "LCtrl")
			down := downSet(t, tc.down...)
			prepare := func(r *simtest.Recorder) {}
			if tc.fail {
				prepare = func(r *simtest.Recorder) { r.FailNext(0, errBlocked) }
			}
			// Only the keys named in "down" are reported down, whatever the log has.
			lt := startLoopWith(t, output.LoopOptions{HeldFile: path, KeyDown: down.down}, prepare, nil)
			res := lt.l.Swept()
			if tc.fail != (res.Err != nil) || tc.fail && !errors.Is(res.Err, errBlocked) {
				t.Errorf("Swept().Err = %v, want an error: %v", res.Err, tc.fail)
			}
			if got := eventsTrace(res.Released); got != tc.released {
				t.Errorf("Swept().Released = %q, want %q", got, tc.released)
			}
			if got := lt.rec.Trace(); got != tc.out {
				t.Errorf("output:\n got %s\nwant %s", got, tc.out)
			}
			for _, e := range res.Released {
				if e.Mouse == keys.MouseNone && !e.Key.IsModifier() {
					t.Errorf("%+v released is no mouse button", e)
				}
			}
			// The buttons the sweep could not release stay in the log until a
			// release goes out; the others are gone from it.
			wantHeld(t, path, tc.logAfter...)
			if err := lt.stop(); err != nil {
				t.Fatal(err)
			}
			// Stop sends the release the sweep could not, KeyDown still having
			// them down, and removes the log.
			if tc.fail && !strings.HasSuffix(lt.rec.Trace(), "-MouseLeft@0 -MouseRight@0") {
				t.Errorf("Stop did not release the carried buttons: %s", lt.rec.Trace())
			}
			wantNoHeld(t, path)
		})
	}
}

// A log written by an older padmated or edited by hand cannot make the sweep
// release a button it must not: the corrupt and foreign logs are ignored as
// ever, and the button bits are plain bits of the log.
func TestHeldLogButtonsInForeignSession(t *testing.T) {
	path := heldPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	other := output.HeldIdentity{Session: 7, Logon: 0x1234, LogonTime: 99}
	if err := os.WriteFile(path, output.EncodeHeldAs(other, press(t, "MouseLeft")), 0o600); err != nil {
		t.Fatal(err)
	}
	// Every key is down but the mask key.
	lt := startLoopWith(t, output.LoopOptions{HeldFile: path, KeyDown: func(e output.KeyEvent) bool { return !output.IsMenuMask(e) }}, nil, nil)
	res := lt.l.Swept()
	if !res.Stale || len(res.Released) > 0 || len(res.StaleKeys) != 1 || res.StaleKeys[0].Mouse != keys.MouseLeft {
		t.Errorf("Swept() = %+v, want the other session's MouseLeft reported and not released", res)
	}
	if got := lt.rec.Trace(); got != "" {
		t.Errorf("output %q, want nothing sent for another session's button", got)
	}
}
