package autostart

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type memoryStore struct {
	value  string
	err    error
	writes int
}

func (s *memoryStore) read() (string, error) { return s.value, s.err }
func (s *memoryStore) write(value string) error {
	s.writes++
	if s.err == nil {
		s.value = value
	}
	return s.err
}
func (s *memoryStore) remove() error { return s.write("") }

func TestAutostartExplicitEnableAndDisable(t *testing.T) {
	s := &memoryStore{}
	e := &Entry{store: s, command: `"C:\Program Files\padmate\padmated.exe" --profile "D:\My Art\profile.json" --dry-run`}
	if enabled, err := e.Enabled(); err != nil || enabled || s.value != "" {
		t.Fatal("reading startup state changed it")
	}
	if err := e.Set(true); err != nil || s.value != e.command {
		t.Fatalf("enable: %q, %v", s.value, err)
	}
	if enabled, err := e.Enabled(); err != nil || !enabled {
		t.Fatalf("enabled: %t, %v", enabled, err)
	}
	s.err = errors.New("denied")
	if err := e.Set(false); err == nil || s.value != e.command {
		t.Fatal("write failure changed startup state")
	}
	s.err = nil
	if err := e.Set(false); err != nil || s.value != "" {
		t.Fatalf("disable: %q, %v", s.value, err)
	}
}

// TestAutostartCurrent: Current tells this program's entry from another
// padmate's (another path or mode) and from none, without writing.
func TestAutostartCurrent(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "padmated.exe")
	s := &memoryStore{}
	e, err := newEntry(exe, []string{"--data-dir", t.TempDir(), "--inject"}, t.TempDir(), s)
	if err != nil {
		t.Fatal(err)
	}
	if current, err := e.Current(); err != nil || current {
		t.Fatalf("no entry: current %t, %v", current, err)
	}
	s.value = `"C:\Old\padmated.exe" --dry-run`
	if current, err := e.Current(); err != nil || current {
		t.Fatalf("another padmate's entry: current %t, %v", current, err)
	}
	if enabled, err := e.Enabled(); err != nil || !enabled {
		t.Fatalf("another padmate's entry: enabled %t, %v", enabled, err)
	}
	if err := e.Set(true); err != nil {
		t.Fatal(err)
	}
	if current, err := e.Current(); err != nil || !current {
		t.Fatalf("own entry: current %t, %v", current, err)
	}
	s.err = errors.New("denied")
	if _, err := e.Current(); err == nil {
		t.Fatal("a read failure was not reported")
	}
	if s.writes != 1 {
		t.Fatalf("%d writes, want only Set's", s.writes)
	}
}

// TestAutostartCurrentReference: a command too long for Run is current
// when Run holds its reference, which Current computes without writing
// the argument file.
func TestAutostartCurrentReference(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "startup")
	exe := filepath.Join(t.TempDir(), "padmated.exe")
	args := []string{"--profile", filepath.Join(t.TempDir(), strings.Repeat("p", 240)+".json"), "--inject"}
	s := &memoryStore{}
	e, err := newEntry(exe, args, dir, s)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Set(true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.value, referenceFlag) {
		t.Fatalf("Run value %q is not a reference", s.value)
	}
	// A fresh entry, as after a restart, has not written the reference.
	fresh, err := newEntry(exe, args, filepath.Join(t.TempDir(), "absent"), s)
	if err != nil {
		t.Fatal(err)
	}
	if current, err := fresh.Current(); err != nil || !current {
		t.Fatalf("reference entry: current %t, %v", current, err)
	}
	if _, err := os.Stat(fresh.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Current wrote the startup directory")
	}
	other, err := newEntry(exe, append(args[:len(args)-1:len(args)-1], "--dry-run"), dir, s)
	if err != nil {
		t.Fatal(err)
	}
	if current, err := other.Current(); err != nil || current {
		t.Fatalf("another mode's reference: current %t, %v", current, err)
	}
}

func TestStartupCommandQuotesWindowsArguments(t *testing.T) {
	for _, tt := range []struct{ value, want string }{
		{"simple", "simple"}, {"", `""`}, {`D:\My Art\`, `"D:\My Art\\"`},
		{`a"b`, `"a\"b"`}, {"画笔目录", "画笔目录"},
	} {
		if got := quoteArg(tt.value); got != tt.want {
			t.Errorf("quoteArg(%q)=%q want %q", tt.value, got, tt.want)
		}
	}
	if _, err := Command("padmated\x00.exe", nil); err == nil {
		t.Fatal("NUL command accepted")
	}
	if _, err := Command("padmated.exe", []string{"--profile", "bad\x00.json"}); err == nil {
		t.Fatal("NUL argument accepted")
	}
}
