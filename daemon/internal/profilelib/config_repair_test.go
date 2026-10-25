package profilelib

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// brokenConfig is the owner's case (F17), an emergency combo of one
// button, beside settings that are fine.
const brokenConfig = `{"schemaVersion":0,"enabled":false,"rules":[{"exe":"paint.exe","profile":"paint"}],"safety":{"panic":{"standard":{"buttons":["back"],"holdMs":2000}}}}`

// backups lists the backups of config.json in the data folder.
func backups(t *testing.T, l *Library) []string {
	t.Helper()
	entries, err := os.ReadDir(l.Dir())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "config.broken-") {
			names = append(names, e.Name())
		}
	}
	return names
}

// fixedClock sets the time backups are named after for the test.
func fixedClock(t *testing.T, at time.Time) {
	t.Helper()
	old := configClock
	configClock = func() time.Time { return at }
	t.Cleanup(func() { configClock = old })
}

// TestReadRepairedConfigBacksUpThenRepairs: a config.json with errors is
// copied to config.broken-YYYYMMDD-HHMMSS.json, byte for byte, and then
// replaced by its repair, which keeps the settings that were fine; the
// next read finds nothing to repair (PRO-29).
func TestReadRepairedConfigBacksUpThenRepairs(t *testing.T) {
	l := New(t.TempDir())
	writeConfig(t, l.Dir(), brokenConfig)
	c, revision, repair, err := l.ReadRepairedConfig(true)
	if err != nil || repair == nil {
		t.Fatal(repair, err)
	}
	if !regexp.MustCompile(`^config\.broken-\d{8}-\d{6}\.json$`).MatchString(repair.Backup) || repair.Reset ||
		!slices.Equal(repair.Members, []string{"/safety/panic/standard"}) || repair.Err == nil || repair.WriteErr != nil {
		t.Fatalf("repair %+v", repair)
	}
	if got := readFile(t, filepath.Join(l.Dir(), repair.Backup)); got != brokenConfig {
		t.Fatalf("backup %q", got)
	}
	fixed := readFile(t, filepath.Join(l.Dir(), "config.json"))
	if revision != Revision([]byte(fixed)) || repair.Revision != revision {
		t.Fatalf("revision %s, repair %s, file %s", revision, repair.Revision, Revision([]byte(fixed)))
	}
	if c.Enabled || len(c.Rules) != 1 || len(c.Safety.Panic) != 0 {
		t.Fatalf("settings %+v", c)
	}
	if again, _, err := l.ReadConfig(); err != nil || again.Enabled {
		t.Fatal(again, err)
	}
	if _, rev, repair, err := l.ReadRepairedConfig(true); err != nil || repair != nil || rev != revision || len(backups(t, l)) != 1 {
		t.Fatal(rev, repair, err, backups(t, l))
	}
}

// TestReadRepairedConfigNeverOverwritesABackup: two repairs in the same
// second, and a file that already has the name, all keep their backups.
func TestReadRepairedConfigNeverOverwritesABackup(t *testing.T) {
	l := New(t.TempDir())
	fixedClock(t, time.Date(2026, 10, 15, 19, 16, 0, 0, time.Local))
	writeConfig(t, l.Dir(), "")
	if err := os.WriteFile(filepath.Join(l.Dir(), "config.broken-20261015-191600.json"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, broken := range []string{"{", `{"schemaVersion":2}`} {
		writeConfig(t, l.Dir(), broken)
		_, _, repair, err := l.ReadRepairedConfig(true)
		if err != nil || repair == nil {
			t.Fatal(repair, err)
		}
		if got := readFile(t, filepath.Join(l.Dir(), repair.Backup)); got != broken {
			t.Fatalf("backup %s = %q, want %q", repair.Backup, got, broken)
		}
		names = append(names, repair.Backup)
	}
	if !slices.Equal(names, []string{"config.broken-20261015-191600-2.json", "config.broken-20261015-191600-3.json"}) {
		t.Fatal(names)
	}
	if readFile(t, filepath.Join(l.Dir(), "config.broken-20261015-191600.json")) != "mine" {
		t.Fatal("a backup was overwritten")
	}
}

// TestReadRepairedConfigResetsWhatDoesNotParse: a file that is no JSON
// object is replaced by the defaults.
func TestReadRepairedConfigResetsWhatDoesNotParse(t *testing.T) {
	l := New(t.TempDir())
	writeConfig(t, l.Dir(), "\xff\xfe{\x00}\x00")
	c, _, repair, err := l.ReadRepairedConfig(true)
	if err != nil || repair == nil || !repair.Reset || !c.Enabled {
		t.Fatal(c, repair, err)
	}
	if got := readFile(t, filepath.Join(l.Dir(), "config.json")); got != profile.DefaultConfigText {
		t.Fatalf("config.json %q", got)
	}
}

// TestReadRepairedConfigReadOnly: without write (a replay) the repair is
// in memory only: config.json stays and no backup is written.
func TestReadRepairedConfigReadOnly(t *testing.T) {
	l := New(t.TempDir())
	good := strings.Repeat("a0", 16)
	broken := `{"schemaVersion":0,"enabled":false,"devices":{"bad":{"ignored":true},"` + good + `":{"ignored":true}}}`
	writeConfig(t, l.Dir(), broken)
	c, revision, repair, err := l.ReadRepairedConfig(false)
	if err != nil || repair == nil || repair.Backup != "" || repair.Revision != "" || !slices.Equal(repair.Members, []string{"/devices/bad"}) {
		t.Fatal(repair, err)
	}
	if c.Enabled || len(c.Devices) != 1 || revision != Revision([]byte(broken)) {
		t.Fatal(c, revision)
	}
	if readFile(t, filepath.Join(l.Dir(), "config.json")) != broken || len(backups(t, l)) != 0 {
		t.Fatal("a read-only repair wrote")
	}
}

// TestReadRepairedConfigUnreadable: a config.json that cannot be read is
// not a file with errors: nothing is repaired or written. A missing one
// is the defaults.
func TestReadRepairedConfigUnreadable(t *testing.T) {
	l := New(t.TempDir())
	if c, revision, repair, err := l.ReadRepairedConfig(true); err != nil || repair != nil || revision != "" || !c.Enabled {
		t.Fatal(c, revision, repair, err)
	}
	if err := os.MkdirAll(filepath.Join(l.Dir(), "config.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, repair, err := l.ReadRepairedConfig(true); err == nil || repair != nil || len(backups(t, l)) != 0 {
		t.Fatal(repair, err)
	}
}

// TestReadRepairedConfigWriteFailure: when config.json cannot be
// replaced, it stays as it was, its backup is removed again, and the
// settings are the repair, in memory.
func TestReadRepairedConfigWriteFailure(t *testing.T) {
	l := New(t.TempDir())
	writeConfig(t, l.Dir(), brokenConfig)
	failed := errors.New("disk full")
	l.configHook = func() error { return failed }
	c, revision, repair, err := l.ReadRepairedConfig(true)
	if err != nil || repair == nil || !errors.Is(repair.WriteErr, failed) || repair.Backup != "" || revision != Revision([]byte(brokenConfig)) || c.Enabled {
		t.Fatal(c, repair, err)
	}
	if readFile(t, filepath.Join(l.Dir(), "config.json")) != brokenConfig || len(backups(t, l)) != 0 {
		t.Fatal("a failed repair changed the data folder", backups(t, l))
	}
}

// TestReadRepairedConfigWriteFailureKnown: a repair that could not be
// written is not tried again for the same file (no backup written and
// removed on every read), the failure is marked as known, and a changed
// file is tried again. A replay is not affected.
func TestReadRepairedConfigWriteFailureKnown(t *testing.T) {
	l := New(t.TempDir())
	writeConfig(t, l.Dir(), brokenConfig)
	failed := errors.New("access denied")
	tries := 0
	l.configHook = func() error {
		tries++
		return failed
	}
	_, _, first, err := l.ReadRepairedConfig(true)
	if err != nil || first == nil || first.Known || !errors.Is(first.WriteErr, failed) || tries != 1 {
		t.Fatal(first, err, tries)
	}
	for range 3 {
		c, revision, again, err := l.ReadRepairedConfig(true)
		if err != nil || again == nil || !again.Known || !errors.Is(again.WriteErr, failed) || again.Backup != "" ||
			revision != Revision([]byte(brokenConfig)) || c.Enabled || tries != 1 || len(backups(t, l)) != 0 {
			t.Fatal(c, again, err, tries)
		}
	}
	// A replay never writes, and never marks anything known.
	if _, _, replay, err := l.ReadRepairedConfig(false); err != nil || replay == nil || replay.Known || replay.WriteErr != nil {
		t.Fatal(replay, err)
	}
	// The file changes, still with errors: it is tried again, and now works.
	changed := `{"schemaVersion":0,"enabled":false,"timing":{"holdMs":"x"}}`
	writeConfig(t, l.Dir(), changed)
	l.configHook = nil
	_, revision, repair, err := l.ReadRepairedConfig(true)
	if err != nil || repair == nil || repair.Known || repair.WriteErr != nil || repair.Backup == "" || revision != repair.Revision {
		t.Fatal(repair, err)
	}
	if got := backups(t, l); len(got) != 1 || readFile(t, filepath.Join(l.Dir(), got[0])) != changed {
		t.Fatal(got)
	}
}

// TestReadStateInvalid: a state.json with errors is ErrInvalidState, which
// padmated resets; one it cannot read is not.
func TestReadStateInvalid(t *testing.T) {
	l := New(t.TempDir())
	if err := os.WriteFile(filepath.Join(l.Dir(), "state.json"), []byte(`{"schemaVersion":0,"bogus":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ReadState(); !errors.Is(err, ErrInvalidState) {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(l.Dir(), "state.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(l.Dir(), "state.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ReadState(); err == nil || errors.Is(err, ErrInvalidState) {
		t.Fatal(err)
	}
}
