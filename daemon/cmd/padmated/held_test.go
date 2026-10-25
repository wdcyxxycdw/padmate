package main

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/emergency"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

// spaceScan is the scan code of Space, which east holds in the basic
// profile.
const spaceScan = 0x39

// The held-key log (OUT-14, ADR-0027): "PDMH", version 4, the logon
// time, the session and the logon session of the run, and the bitmaps,
// one bit per key.
const (
	heldSize = 124
	heldBits = 28 // the offset of the bitmaps
)

// heldTestID is the logon session of the held tests' padmated
// (liveConfig.heldIdentity), logged on at 2026-09-21T14:13:20Z (a FILETIME,
// 100 ns since 1601).
var heldTestID = output.HeldIdentity{Session: 1, Logon: 0x2345, LogonTime: (1_790_000_000 + 11_644_473_600) * 10_000_000}

// heldLogWith returns a held-key log of heldTestID with the scan codes,
// without E0.
func heldLogWith(scans ...uint8) []byte { return heldLogAs(heldTestID, scans...) }

// heldLogAs returns a held-key log of id with the scan codes, without E0.
func heldLogAs(id output.HeldIdentity, scans ...uint8) []byte {
	b := make([]byte, heldSize)
	copy(b, "PDMH")
	binary.LittleEndian.PutUint32(b[4:], 4)
	binary.LittleEndian.PutUint64(b[8:], uint64(id.LogonTime))
	binary.LittleEndian.PutUint32(b[16:], id.Session)
	binary.LittleEndian.PutUint64(b[20:], id.Logon)
	for _, sc := range scans {
		b[heldBits+sc/8] |= 1 << (sc % 8)
	}
	return b
}

// heldHas reports whether the held-key log at path has the scan code sc.
func heldHas(path string, sc uint8) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if len(b) != heldSize || string(b[:4]) != "PDMH" {
		return false, errors.New("not a held-key log")
	}
	return b[heldBits+sc/8]&(1<<(sc%8)) != 0, nil
}

// heldSender records the edges it injects. At the press of Space it
// records whether the held-key log already has Space. Its first fail
// calls inject nothing.
type heldSender struct {
	path string

	mu           sync.Mutex
	fail         int
	sent         []string
	spaceLogged  []bool // at each press of Space
	spaceLogErrs []error
}

var errHeldSend = errors.New("heldSender: refused")

func (s *heldSender) Send(evs []output.KeyEvent) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail > 0 {
		s.fail--
		return 0, errHeldSend
	}
	for _, e := range evs {
		name := edgeName(e)
		if name == "+Space" {
			ok, err := heldHas(s.path, spaceScan)
			s.spaceLogged = append(s.spaceLogged, ok)
			s.spaceLogErrs = append(s.spaceLogErrs, err)
		}
		s.sent = append(s.sent, name)
	}
	return len(evs), nil
}

func (s *heldSender) edges() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sent)
}

// heldLive starts live mode with --inject on a fakeSource, with the
// held-key log path, in which the last run left content (nil: no file),
// and with Space reported down.
func heldLive(t *testing.T, path string, content []byte, s *heldSender) (*testLive, *fakeSource) {
	t.Helper()
	if content != nil {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s.path = path
	return sessionLive(t, func(cfg *liveConfig) {
		cfg.heldFile = path
		cfg.keyDown = func(e output.KeyEvent) bool { return !e.ByVK && !e.Ext && e.Scan == spaceScan }
		cfg.heldIdentity = func() (output.HeldIdentity, error) { return heldTestID, nil }
	}, func() (output.Sender, error) { return s, nil }, "--inject")
}

// pressEast presses and releases east, which holds Space, and waits for
// both edges.
func (tl *testLive) pressEast(t *testing.T, src *fakeSource, s *heldSender) {
	t.Helper()
	n := len(s.edges())
	button(src, "east", true)
	tl.waitUntil(t, "+Space", func() bool { return slices.Contains(s.edges()[n:], "+Space") })
	button(src, "east", false)
	tl.waitUntil(t, "-Space", func() bool { return slices.Contains(s.edges()[n:], "-Space") })
}

// TestLiveHeldSweep: with --inject, T-output releases the keys the last
// run left in the held-key log that are down before the first edge, and
// padmated says so on stderr and in the log; later presses are in the log
// before they go out, and the exit removes it (OUT-14).
func TestLiveHeldSweep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "held.bin")
	s := &heldSender{}
	tl, src := heldLive(t, path, heldLogWith(spaceScan, 0x2C), s)
	if got := s.edges(); !slices.Equal(got, []string{"-Space"}) {
		t.Fatalf("sent %q before the first edge, want the sweep's -Space", got)
	}
	if want := "padmated: released keys the last run held: Space\n"; !strings.Contains(tl.stderr.String(), want) {
		t.Errorf("stderr has no %q:\n%s", want, tl.stderr)
	}
	if line := logLine(tl.log.String(), "held keys released"); !strings.Contains(line, `"level":"INFO"`) || !strings.Contains(line, `"keys":"Space"`) {
		t.Errorf("held keys released: %q, want info with Space", line)
	}
	if has, err := heldHas(path, spaceScan); err != nil || has {
		t.Errorf("the log has Space: %v, %v; want it released and cleared", has, err)
	}
	tl.pressEast(t, src, s)
	s.mu.Lock()
	if !slices.Equal(s.spaceLogged, []bool{true}) || s.spaceLogErrs[0] != nil {
		t.Errorf("Space in the log at its press: %v, %v; want it written before the Send", s.spaceLogged, s.spaceLogErrs)
	}
	s.mu.Unlock()
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d, want 0\nstderr:\n%s", code, tl.stderr)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the held-key log is left after the exit: %v", err)
	}
	if strings.Contains(tl.stderr.String(), "held-key log off") || logLine(tl.log.String(), "startup held-key cleanup failed") != "" {
		t.Errorf("a warning about the held-key log:\nstderr:\n%s", tl.stderr)
	}
}

// TestLiveHeldSweepFailureStarts: when the sweep cannot release the keys,
// the log is corrupt, or the log cannot be created, padmated warns and
// maps as ever.
func TestLiveHeldSweepFailureStarts(t *testing.T) {
	const cleanup = "padmated: releasing the keys the last run held: "
	for _, tc := range []struct {
		name    string
		path    func(t *testing.T) string
		content []byte
		fail    int
		stderr  string
		absent  string // not on stderr
		warn    string
		kept    bool // the log still has Space after the start
	}{
		{name: "release fails", content: heldLogWith(spaceScan), fail: 1, stderr: cleanup, warn: "startup held-key cleanup failed", kept: true},
		{
			name: "corrupt", content: []byte("{\"version\":1,\"keys\":[]}"), absent: cleanup, warn: "held-key log reset",
			stderr: "padmated: held-key log reset: output: the held-key log is corrupt: 23 bytes, want 124; keys the last run held, if any, are not released\n",
		},
		{name: "no directory", path: func(t *testing.T) string {
			file := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(file, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(file, "run", "held.bin")
		}, stderr: "padmated: held-key log off: ", warn: "held-key log off"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "run", "held.bin")
			if tc.path != nil {
				path = tc.path(t)
			}
			s := &heldSender{fail: tc.fail}
			tl, src := heldLive(t, path, tc.content, s)
			if !strings.Contains(tl.stderr.String(), tc.stderr) {
				t.Errorf("stderr has no %q:\n%s", tc.stderr, tl.stderr)
			}
			if tc.absent != "" && strings.Contains(tl.stderr.String(), tc.absent) {
				t.Errorf("stderr has %q:\n%s", tc.absent, tl.stderr)
			}
			if line := logLine(tl.log.String(), tc.warn); !strings.Contains(line, `"level":"WARN"`) {
				t.Errorf("%s: %q, want a warning", tc.warn, line)
			}
			if tc.kept {
				if has, err := heldHas(path, spaceScan); err != nil || !has {
					t.Errorf("the log has Space: %v, %v; want it kept", has, err)
				}
			}
			tl.pressEast(t, src, s)
			tl.cancel()
			if code := tl.waitCode(t); code != 0 {
				t.Errorf("exit %d, want 0\nstderr:\n%s", code, tl.stderr)
			}
		})
	}
}

// TestLiveHeldStale: a held-key log of another boot or logon session is
// reset, and none of its keys is released even when it is down now:
// padmated warns and names them, and says nothing on stderr when the log
// had no key.
func TestLiveHeldStale(t *testing.T) {
	other := heldTestID
	other.Logon++
	t.Run("keys", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "run", "held.bin")
		s := &heldSender{}
		tl, src := heldLive(t, path, heldLogAs(other, spaceScan), s)
		want := "padmated: held-key log reset: it is from another boot or logon session (session 1, logon 0x2346 at 2026-09-21T14:13:20Z); its keys are not released: Space\n"
		if !strings.Contains(tl.stderr.String(), want) {
			t.Errorf("stderr has no %q:\n%s", want, tl.stderr)
		}
		if line := logLine(tl.log.String(), "stale held-key log reset"); !strings.Contains(line, `"level":"WARN"`) || !strings.Contains(line, `"keys":"Space"`) {
			t.Errorf("stale held-key log reset: %q, want a warning with Space", line)
		}
		if got := s.edges(); len(got) > 0 {
			t.Errorf("sent %q before the first edge, want nothing", got)
		}
		if has, err := heldHas(path, spaceScan); err != nil || has {
			t.Errorf("the log has Space: %v, %v; want it reset", has, err)
		}
		tl.pressEast(t, src, s)
		tl.cancel()
		if code := tl.waitCode(t); code != 0 {
			t.Errorf("exit %d, want 0\nstderr:\n%s", code, tl.stderr)
		}
	})
	t.Run("empty", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "run", "held.bin")
		s := &heldSender{}
		tl, _ := heldLive(t, path, heldLogAs(other), s)
		if strings.Contains(tl.stderr.String(), "held-key") {
			t.Errorf("stderr mentions the held-key log:\n%s", tl.stderr)
		}
		if line := logLine(tl.log.String(), "stale held-key log reset"); !strings.Contains(line, `"level":"INFO"`) {
			t.Errorf("stale held-key log reset: %q, want info", line)
		}
		tl.cancel()
		tl.waitCode(t)
	})
}

// TestLiveHeldPathMissing: with no folder for the held-key log, --inject
// warns that the log is off, as when T-output cannot open it; the dry run
// says nothing.
func TestLiveHeldPathMissing(t *testing.T) {
	errNoFolder := errors.New("test: no folder for the held-key log")
	s := newGateSender("")
	tl, src := sessionLive(t, func(cfg *liveConfig) { cfg.heldFileErr = errNoFolder },
		func() (output.Sender, error) { return s, nil }, "--inject")
	if want := "padmated: held-key log off: test: no folder for the held-key log; keys held when padmated is killed are not released at the next start\n"; !strings.Contains(tl.stderr.String(), want) {
		t.Errorf("stderr has no %q:\n%s", want, tl.stderr)
	}
	if line := logLine(tl.log.String(), "held-key log off"); !strings.Contains(line, `"level":"WARN"`) {
		t.Errorf("held-key log off: %q, want a warning", line)
	}
	button(src, "east", true)
	tl.waitSent(t, s, "+Space")
	button(src, "east", false)
	tl.waitSent(t, s, "-Space")
	tl.cancel()
	if code := tl.waitCode(t); code != 0 {
		t.Errorf("exit %d, want 0\nstderr:\n%s", code, tl.stderr)
	}

	dry, _ := sessionLive(t, func(cfg *liveConfig) { cfg.heldFileErr = errNoFolder }, nil, "--dry-run")
	if strings.Contains(dry.stderr.String(), "held-key") {
		t.Errorf("the dry run mentions the held-key log:\n%s", dry.stderr)
	}
}

// TestHeldPathIn: the held-key log is in LOCALAPPDATA; without it,
// heldPathIn says why.
func TestHeldPathIn(t *testing.T) {
	root := filepath.Join("C:", "local")
	if got, err := heldPathIn(root); err != nil || got != filepath.Join(root, "padmate", "run", "held.bin") {
		t.Errorf("heldPathIn(%q) = %q, %v", root, got, err)
	}
	if got, err := heldPathIn(""); got != "" || err == nil || !strings.Contains(err.Error(), "LOCALAPPDATA is not set") {
		t.Errorf(`heldPathIn("") = %q, %v; want "" and why`, got, err)
	}
}

// TestLiveHeldCarriedReleased: a key the startup sweep could not release
// is released by Supervisor.ReleaseAll, which padmatectl release-all
// signals (on Windows only), and by the system's suspension, not only at
// the exit, and leaves the log.
func TestLiveHeldCarriedReleased(t *testing.T) {
	for _, tc := range []struct {
		name    string
		windows bool // the named event exists on Windows only
		release func(t *testing.T, tl *testLive)
	}{
		{"release-all", true, func(t *testing.T, tl *testLive) {
			if err := emergency.Trigger(tl.cfg.emergencyName); err != nil {
				t.Fatal(err)
			}
		}},
		{"Supervisor.ReleaseAll", false, func(t *testing.T, tl *testLive) {
			if err := tl.sup.ReleaseAll(); err != nil {
				t.Fatal(err)
			}
		}},
		{"lock", false, func(_ *testing.T, tl *testLive) { tl.session.change(winsession.SigLock, 0, winsession.Lock) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.windows && runtime.GOOS != "windows" {
				t.Skip("padmatectl release-all is Windows only")
			}
			path := filepath.Join(t.TempDir(), "run", "held.bin")
			s := &heldSender{fail: 1}
			tl, _ := heldLive(t, path, heldLogWith(spaceScan), s)
			if want := "padmated: releasing the keys the last run held: "; !strings.Contains(tl.stderr.String(), want) {
				t.Fatalf("stderr has no %q:\n%s", want, tl.stderr)
			}
			if has, err := heldHas(path, spaceScan); err != nil || !has {
				t.Fatalf("the log has Space: %v, %v; want it carried", has, err)
			}
			tc.release(t, tl)
			tl.waitUntil(t, "-Space", func() bool { return slices.Contains(s.edges(), "-Space") })
			tl.waitUntil(t, "Space off the log", func() bool {
				has, err := heldHas(path, spaceScan)
				return err == nil && !has
			})
			tl.cancel()
			if code := tl.waitCode(t); code != 0 {
				t.Errorf("exit %d, want 0\nstderr:\n%s", code, tl.stderr)
			}
			if got := s.edges(); !slices.Equal(got, []string{"-Space"}) {
				t.Errorf("sent %q, want the one -Space", got)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the held-key log is left after the exit: %v", err)
			}
		})
	}
}

// TestLiveHeldDryRun: the dry run neither sweeps nor writes the held-key
// log, whatever liveConfig.heldFile says.
func TestLiveHeldDryRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "held.bin")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	content := heldLogWith(spaceScan)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	tl, src := sessionLive(t, func(cfg *liveConfig) {
		cfg.heldFile = path
		cfg.keyDown = func(output.KeyEvent) bool { return true }
	}, nil, "--dry-run")
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "+Space")
	tl.cancel()
	tl.waitCode(t)
	if got, err := os.ReadFile(path); err != nil || !slices.Equal(got, content) {
		t.Errorf("the held-key log is %v, %v after the dry run; want it untouched", got, err)
	}
	if strings.Contains(tl.stderr.String(), "held") {
		t.Errorf("stderr mentions the held-key log:\n%s", tl.stderr)
	}
}
