package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/emergency"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

// mouseProfile binds east to MouseLeft and south to Shift+MouseRight as holds
// (ADR-0062).
const mouseProfile = "testdata/mouse-hold.json"

// mouseLive starts live mode with --inject on a fakeSource, with the
// mouse-button profile and the Sender s, which is the test's own: no real
// event is ever injected.
func mouseLive(t *testing.T, s output.Sender, change func(*liveConfig)) (*testLive, *fakeSource) {
	t.Helper()
	return sessionLive(t, change, func() (output.Sender, error) { return s, nil }, "--inject", "--profile", mouseProfile)
}

// TestLiveMouseButtonReleasedOnEveryPath: the buttons a pad holds go up on the
// paths that end the daemon's hold, as keys do (CLAUDE.md invariant 1): the
// exit, padmatectl release-all (on Windows, on the test's own event), a
// release of every slot, the lock, and the removal of the pad.
func TestLiveMouseButtonReleasedOnEveryPath(t *testing.T) {
	for _, tc := range []struct {
		name    string
		windows bool // the named event exists on Windows only
		end     func(t *testing.T, tl *testLive, src *fakeSource)
	}{
		{"exit", false, func(t *testing.T, tl *testLive, _ *fakeSource) {
			tl.cancel()
			if code := tl.waitCode(t); code != 0 {
				t.Errorf("exit %d, want 0\nstderr:\n%s", code, tl.stderr)
			}
		}},
		{"release-all", true, func(t *testing.T, tl *testLive, _ *fakeSource) {
			if err := emergency.Trigger(tl.cfg.emergencyName); err != nil {
				t.Fatal(err)
			}
		}},
		{"Supervisor.ReleaseAll", false, func(t *testing.T, tl *testLive, _ *fakeSource) {
			if err := tl.sup.ReleaseAll(); err != nil {
				t.Fatal(err)
			}
		}},
		{"lock", false, func(_ *testing.T, tl *testLive, _ *fakeSource) {
			tl.session.change(winsession.SigLock, 0, winsession.Lock)
		}},
		{"removed", false, func(_ *testing.T, _ *testLive, src *fakeSource) {
			src.events <- input.Event{Device: 1, Kind: input.DeviceRemoved}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.windows && runtime.GOOS != "windows" {
				t.Skip("padmatectl release-all is Windows only")
			}
			s := newGateSender("")
			tl, src := mouseLive(t, s, nil)
			button(src, "east", true)
			tl.waitSent(t, s, "+MouseLeft")
			button(src, "south", true)
			tl.waitSent(t, s, "+MouseRight")
			tc.end(t, tl, src)
			for _, edge := range []string{"-MouseLeft", "-MouseRight", "-LShift"} {
				tl.waitSent(t, s, edge)
			}
			if tc.name != "exit" {
				tl.cancel()
				if code := tl.waitCode(t); code != 0 {
					t.Errorf("exit %d, want 0\nstderr:\n%s", code, tl.stderr)
				}
			}
			// Each went down once and up once, the buttons before Shift.
			want := []string{"+MouseLeft", "+LShift", "+MouseRight", "-MouseRight", "-MouseLeft", "-LShift"}
			if got := s.edges(); !slices.Equal(got, want) {
				t.Errorf("sent %q, want %q", got, want)
			}
		})
	}
}

// heldMouseLog returns a held-key log of heldTestID whose third bitmap has the
// bits of the VKs vks: the slots of VK_LBUTTON and its kin (OUT-14).
func heldMouseLog(vks ...uint8) []byte {
	b := heldLogAs(heldTestID)
	for _, vk := range vks {
		b[heldBits+64+int(vk)/8] |= 1 << (vk % 8)
	}
	return b
}

// TestLiveMouseButtonHeldSweep: the keys the last run left in the held-key log
// include its mouse buttons, in the VK slots of the log; the sweep releases
// the ones the system still has down, with a real button release, and
// padmated names them. A button the system has up is forgotten. The press of
// a button is in the log before it goes out, and the exit removes the log.
func TestLiveMouseButtonHeldSweep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "held.bin")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	// The last run held the left, the middle and the second side button.
	if err := os.WriteFile(path, heldMouseLog(0x01, 0x04, 0x06), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &mouseHeldSender{path: path}
	tl, src := mouseLive(t, s, func(cfg *liveConfig) {
		cfg.heldFile = path
		// Left and the second side button are down; the middle one went up.
		cfg.keyDown = func(e output.KeyEvent) bool { return e.Mouse == keys.MouseLeft || e.Mouse == keys.MouseX2 }
		cfg.heldIdentity = func() (output.HeldIdentity, error) { return heldTestID, nil }
	})
	if got := s.edges(); !slices.Equal(got, []string{"-MouseLeft", "-MouseX2"}) {
		t.Fatalf("sent %q before the first edge, want the sweep's -MouseLeft -MouseX2", got)
	}
	if want := "padmated: released keys the last run held: MouseLeft, MouseX2\n"; !strings.Contains(tl.stderr.String(), want) {
		t.Errorf("stderr has no %q:\n%s", want, tl.stderr)
	}
	if line := logLine(tl.log.String(), "held keys released"); !strings.Contains(line, `"level":"INFO"`) || !strings.Contains(line, `"keys":"MouseLeft,MouseX2"`) {
		t.Errorf("held keys released: %q, want info with the two buttons", line)
	}
	if b, err := os.ReadFile(path); err != nil || len(b) != heldSize || !slices.Equal(b[heldBits:], make([]byte, heldSize-heldBits)) {
		t.Errorf("the log after the sweep is %v, %v; want it cleared (the middle button too)", b, err)
	}

	button(src, "east", true)
	tl.waitUntil(t, "+MouseLeft", func() bool { return slices.Contains(s.edges(), "+MouseLeft") })
	s.mu.Lock()
	if !slices.Equal(s.loggedAtPress, []bool{true}) {
		t.Errorf("MouseLeft in the log at its press: %v; want it written before the Send", s.loggedAtPress)
	}
	s.mu.Unlock()
	button(src, "east", false)
	tl.waitUntil(t, "-MouseLeft", func() bool { return len(s.edges()) == 4 })
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d, want 0\nstderr:\n%s", code, tl.stderr)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the held-key log is left after the exit: %v", err)
	}
}

// mouseHeldSender records the edges it injects, and at the press of MouseLeft
// whether the held-key log already has the bit of VK_LBUTTON.
type mouseHeldSender struct {
	heldSender
	path          string
	loggedAtPress []bool
}

func (s *mouseHeldSender) Send(evs []output.KeyEvent) (int, error) {
	for _, e := range evs {
		if !e.Up && e.Mouse == keys.MouseLeft {
			b, err := os.ReadFile(s.path)
			s.mu.Lock()
			s.loggedAtPress = append(s.loggedAtPress, err == nil && len(b) == heldSize && b[heldBits+64]&(1<<1) != 0)
			s.mu.Unlock()
		}
	}
	return s.heldSender.Send(evs)
}
