package output_test

import (
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// heldNowLogonTime is the FILETIME of 2026-09-21T14:13:20Z: 100 ns since
// 1601 UTC.
const heldNowLogonTime = (1_790_000_000 + 11_644_473_600) * 10_000_000

// heldNow is the logon session of the Loops of the tests that set
// LoopOptions.Identity.
var heldNow = output.HeldIdentity{Session: 1, Logon: 0x3_0001_2345, LogonTime: heldNowLogonTime}

// identityOf is a LoopOptions.Identity that returns id.
func identityOf(id output.HeldIdentity) func() (output.HeldIdentity, error) {
	return func() (output.HeldIdentity, error) { return id, nil }
}

// writeHeldAs writes a held-key log of id with the presses of specs at
// path.
func writeHeldAs(t testing.TB, path string, id output.HeldIdentity, specs ...string) []byte {
	t.Helper()
	var events []output.KeyEvent
	for _, s := range specs {
		events = append(events, press(t, s))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	b := output.EncodeHeldAs(id, events...)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return b
}

// wantHeldIdentity checks that the header of the log at path has id.
func wantHeldIdentity(t testing.TB, path string, id output.HeldIdentity) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := output.HeldIdentityOf(b)
	if err != nil || got != id {
		t.Errorf("the held-key log's header has %v, %v; want %v", got, err, id)
	}
}

// TestLoopSweepStale: the sweep takes the keys of a log of this run's
// logon session only (LoopOptions.Identity), all three fields equal. A log
// of another session, of another logon session or of a logon session with
// another logon time, even 100 ns apart (the same LUID in another boot), is
// reset to no key, and none of its keys is released even though KeyDown
// reports them down: Swept reports it Stale, with the log's identity and
// keys. The log then has this run's header either way.
func TestLoopSweepStale(t *testing.T) {
	with := func(f func(*output.HeldIdentity)) output.HeldIdentity {
		id := heldNow
		f(&id)
		return id
	}
	allDown := func(e output.KeyEvent) bool { return !output.IsMenuMask(e) }
	for _, tc := range []struct {
		name  string
		from  output.HeldIdentity
		stale bool
	}{
		{"same", heldNow, false},
		{"logon time later", with(func(id *output.HeldIdentity) { id.LogonTime++ }), true},
		{"logon time earlier", with(func(id *output.HeldIdentity) { id.LogonTime -= 3600 * 10_000_000 }), true},
		{"session", with(func(id *output.HeldIdentity) { id.Session = 2 }), true},
		{"logon", with(func(id *output.HeldIdentity) { id.Logon++ }), true},
		{"logon high part", with(func(id *output.HeldIdentity) { id.Logon ^= 1 << 32 }), true},
		{"zero", output.HeldIdentity{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := heldPath(t)
			writeHeldAs(t, path, tc.from, "LCtrl", "Z")
			lt := startLoopWith(t, output.LoopOptions{HeldFile: path, KeyDown: allDown, Identity: identityOf(heldNow)}, nil, nil)
			res := lt.l.Swept()
			if res.Err != nil || res.Off || res.Reset || res.Stale != tc.stale {
				t.Fatalf("Swept() = %+v, want Stale %v and nothing else", res, tc.stale)
			}
			if tc.stale {
				if len(res.Released) > 0 || res.StaleFrom != tc.from || eventsTrace(res.StaleKeys) != "-Z -LCtrl" {
					t.Errorf("Swept() = %+v, want nothing released, StaleFrom %v, StaleKeys -Z -LCtrl", res, tc.from)
				}
				if got := lt.rec.Trace(); got != "" {
					t.Errorf("output: %s, want nothing", got)
				}
				wantHeld(t, path)
			} else {
				if eventsTrace(res.Released) != "-Z -LCtrl" || res.StaleKeys != nil {
					t.Errorf("Swept() = %+v, want -Z -LCtrl released and no StaleKeys", res)
				}
				wantHeld(t, path)
			}
			wantHeldIdentity(t, path, heldNow)
			lt.do(down(ownerA, "Space"))
			wantHeld(t, path, "Space")
			wantHeldIdentity(t, path, heldNow)
			lt.do(up(ownerA, "Space"))
			if err := lt.stop(); err != nil {
				t.Fatal(err)
			}
			wantNoHeld(t, path)
		})
	}
	t.Run("mask down", func(t *testing.T) {
		// The mask key, which no log has, is released as with no log.
		path := heldPath(t)
		writeHeldAs(t, path, output.HeldIdentity{}, "LAlt")
		lt := startLoopWith(t, output.LoopOptions{HeldFile: path, KeyDown: downSet(t, "LAlt", "mask").down, Identity: identityOf(heldNow)}, nil, nil)
		res := lt.l.Swept()
		if !res.Stale || res.Err != nil || eventsTrace(res.Released) != "-vk:0xE8" || eventsTrace(res.StaleKeys) != "-LAlt" {
			t.Errorf("Swept() = %+v, want Stale with LAlt, the mask released", res)
		}
		if got := lt.rec.Trace(); got != "-vk:0xE8@0" {
			t.Errorf("output: %s, want -vk:0xE8@0", got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		// A log another session left with no key: Stale, nothing to drop.
		path := heldPath(t)
		writeHeldAs(t, path, output.HeldIdentity{Session: 9})
		lt := startLoopWith(t, output.LoopOptions{HeldFile: path, KeyDown: allDown, Identity: identityOf(heldNow)}, nil, nil)
		if res := lt.l.Swept(); !res.Stale || res.Err != nil || len(res.StaleKeys) > 0 || len(res.Released) > 0 {
			t.Errorf("Swept() = %+v, want Stale with no key", res)
		}
		wantHeldIdentity(t, path, heldNow)
	})
}

// TestLoopSweepIdentityFails: when LoopOptions.Identity fails, the run
// keeps no log (Off) and sweeps nothing, and leaves the file as it was,
// for a run that can tell whose it is.
func TestLoopSweepIdentityFails(t *testing.T) {
	path := heldPath(t)
	before := writeHeldAs(t, path, heldNow, "LCtrl")
	lt := startLoopWith(t, output.LoopOptions{HeldFile: path, KeyDown: func(output.KeyEvent) bool { return true },
		Identity: func() (output.HeldIdentity, error) { return output.HeldIdentity{}, errDisk }}, nil, nil)
	res := lt.l.Swept()
	if !res.Off || !errors.Is(res.Err, errDisk) || len(res.Released) > 0 || res.Stale {
		t.Errorf("Swept() = %+v, want Off with the Identity's error", res)
	}
	lt.do(tap(ownerA, "F13", 0))
	if err := lt.stop(); err != nil {
		t.Fatal(err)
	}
	if got := lt.rec.Trace(); got != "+F13@0 -F13@0" {
		t.Errorf("output: %s, want only the tap", got)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, before) {
		t.Errorf("the held-key log is %x, %v; want it untouched", got, err)
	}
}

// TestHeldIdentityString: the messages of the sweep name the logon time
// in UTC, to the second; any logon time a log has formats, also at the
// ends of int64.
func TestHeldIdentityString(t *testing.T) {
	for _, tc := range []struct {
		id   output.HeldIdentity
		want string
	}{
		{heldNow, "session 1, logon 0x300012345 at 2026-09-21T14:13:20Z"},
		{output.HeldIdentity{LogonTime: heldNowLogonTime + 9_999_999}, "session 0, logon 0x0 at 2026-09-21T14:13:20Z"},
		{output.HeldIdentity{}, "session 0, logon 0x0 at 1601-01-01T00:00:00Z"},
		{output.HeldIdentity{LogonTime: -1}, "session 0, logon 0x0 at 1600-12-31T23:59:59Z"},
	} {
		if got := tc.id.String(); got != tc.want {
			t.Errorf("%+v.String() = %q, want %q", tc.id, got, tc.want)
		}
	}
	for _, at := range []int64{math.MinInt64, math.MaxInt64} {
		if got := (output.HeldIdentity{LogonTime: at}).String(); !strings.HasPrefix(got, "session 0, logon 0x0 at ") {
			t.Errorf("logon time %d: %q", at, got)
		}
	}
}
