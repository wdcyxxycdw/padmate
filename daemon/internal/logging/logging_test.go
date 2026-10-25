package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// lines decodes JSON lines.
func lines(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	for l := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line %q is not JSON: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

func TestNew(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelInfo)
	log.Debug("hidden")
	log.Info("slot assigned", "slot", 0, "device", input.DeviceKey{GUID: "03ab", N: 1}, "id", input.DeviceID(7))
	log.Warn("slot error", "err", os.ErrNotExist, "backoff", 100*time.Millisecond, slog.Group("window", "d", 10*time.Second))
	got := lines(t, buf.Bytes())
	if len(got) != 2 {
		t.Fatalf("%d lines, want 2 (debug filtered):\n%s", len(got), buf.String())
	}
	for _, m := range got {
		if s, ok := m["time"].(string); !ok || s == "" {
			t.Errorf("line %v has no time", m)
		}
	}
	for k, want := range map[string]any{"level": "INFO", "msg": "slot assigned", "slot": 0.0, "device": "03ab#1", "id": 7.0} {
		if got[0][k] != want {
			t.Errorf("%s = %#v, want %#v", k, got[0][k], want)
		}
	}
	if got[1]["level"] != "WARN" || got[1]["err"] != os.ErrNotExist.Error() {
		t.Errorf("second line %v, want a WARN with err %q", got[1], os.ErrNotExist)
	}
	// Durations as strings, in groups too.
	if g, _ := got[1]["window"].(map[string]any); got[1]["backoff"] != "100ms" || g["d"] != "10s" {
		t.Errorf("second line %v, want backoff 100ms and window.d 10s", got[1])
	}
}

func TestParseLevel(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug}, {"INFO", slog.LevelInfo}, {"Warn", slog.LevelWarn}, {"error", slog.LevelError},
	} {
		if got, err := ParseLevel(tc.in); err != nil || got != tc.want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
	for _, in := range []string{"", "warning", "info+1", "-4", "trace"} {
		if _, err := ParseLevel(in); err == nil || !strings.Contains(err.Error(), "debug, info, warn or error") {
			t.Errorf("ParseLevel(%q) = %v, want an error naming the levels", in, err)
		}
	}
}

func TestOpenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "padmated.log")
	for _, msg := range []string{"first", "second"} {
		log, c, err := Open(Options{Level: slog.LevelInfo, File: path})
		if err != nil {
			t.Fatal(err)
		}
		log.Debug("hidden")
		log.Info(msg)
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := lines(t, b)
	if len(got) != 2 || got[0]["msg"] != "first" || got[1]["msg"] != "second" {
		t.Errorf("the file has\n%s\nwant first, then second: Open appends", b)
	}
}

func TestOpenFileError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// The log's directory is a file.
	if _, _, err := Open(Options{File: filepath.Join(file, "padmated.log")}); err == nil {
		t.Error("Open under a file succeeded")
	}
}

// fakeStderr makes stderr a temporary file, usable or not as the check
// reports, until the test ends, and returns a func that reads what was
// written to it.
func fakeStderr(t *testing.T, usable bool) func() string {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	old, oldUsable := os.Stderr, stderrUsable
	os.Stderr, stderrUsable = f, func() bool { return usable }
	t.Cleanup(func() {
		os.Stderr, stderrUsable = old, oldUsable
		_ = f.Close()
	})
	return func() string {
		b, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
}

// configDir points os.UserConfigDir at dir, or at nothing for "".
func configDir(t *testing.T, dir string) {
	t.Helper()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", dir)
	case "darwin", "ios":
		t.Setenv("HOME", dir)
	case "plan9":
		t.Setenv("home", dir)
	default:
		t.Setenv("XDG_CONFIG_HOME", dir)
		if dir == "" {
			t.Setenv("HOME", "")
		}
	}
}

func TestOpenStderr(t *testing.T) {
	for _, tc := range []struct {
		name   string
		file   string
		usable bool
		stderr bool // else DefaultFile
	}{
		{"default, stderr usable", "", true, true},
		{"default, no stderr", "", false, false},
		{"dash, stderr usable", "-", true, true},
		{"dash, no stderr", "-", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stderr := fakeStderr(t, tc.usable)
			dir := t.TempDir()
			configDir(t, dir)
			def, err := DefaultFile()
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(dir, "padmate", "logs", "padmated.log"); runtime.GOOS != "darwin" && def != want {
				t.Fatalf("DefaultFile() = %s, want %s", def, want)
			}
			log, c, err := Open(Options{Level: slog.LevelWarn, File: tc.file})
			if err != nil {
				t.Fatal(err)
			}
			log.Info("hidden")
			log.Warn("hello")
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			file, ferr := os.ReadFile(def)
			var got string
			switch {
			case tc.stderr && ferr == nil:
				t.Errorf("Open wrote %s too", def)
			case tc.stderr:
				got = stderr()
				if _, err := os.Stderr.Stat(); err != nil {
					t.Errorf("stderr after Close: %v; the Closer must leave it open", err)
				}
			case ferr != nil:
				t.Fatalf("Open without a usable stderr wrote no %s: %v", def, ferr)
			default:
				got = string(file)
				if s := stderr(); s != "" {
					t.Errorf("Open wrote %q to stderr too", s)
				}
			}
			if m := lines(t, []byte(got)); len(m) != 1 || m[0]["msg"] != "hello" {
				t.Errorf("log %q, want the one warning", got)
			}
		})
	}
}

// TestOpenStderrWriter: Options.Stderr takes the place of os.Stderr, and
// counts as usable.
func TestOpenStderrWriter(t *testing.T) {
	for _, file := range []string{"", "-"} {
		stderr := fakeStderr(t, false)
		configDir(t, t.TempDir())
		var buf bytes.Buffer
		log, c, err := Open(Options{File: file, Stderr: &buf})
		if err != nil {
			t.Fatal(err)
		}
		log.Info("hello")
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		if m := lines(t, buf.Bytes()); len(m) != 1 || m[0]["msg"] != "hello" {
			t.Errorf("File %q: Stderr has %q, want the log", file, buf.String())
		}
		if s := stderr(); s != "" {
			t.Errorf("File %q: Open wrote %q to os.Stderr", file, s)
		}
		def, err := DefaultFile()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(def); err == nil {
			t.Errorf("File %q: Open wrote %s", file, def)
		}
	}
}

func TestDefaultFileError(t *testing.T) {
	if runtime.GOOS == "plan9" {
		t.Skip("plan9's config dir is $home/lib")
	}
	configDir(t, "")
	if f, err := DefaultFile(); err == nil {
		t.Fatalf("DefaultFile() = %s without a config dir, want an error", f)
	}
	fakeStderr(t, false)
	if _, _, err := Open(Options{}); err == nil {
		t.Error("Open without stderr or a config dir succeeded")
	}
}
