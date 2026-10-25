package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// New returns a logger that writes JSON lines to w at level and above.
// Durations are written as strings such as "100ms", not nanoseconds.
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level, ReplaceAttr: durationString}))
}

// durationString writes a duration attribute as time.Duration.String.
func durationString(_ []string, a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindDuration {
		a.Value = slog.StringValue(a.Value.Duration().String())
	}
	return a
}

// Options configure Open.
type Options struct {
	Level slog.Level
	// File is the log file. "" means stderr when stderr is usable (a
	// console, or redirected) and otherwise DefaultFile(); "-" means
	// stderr always.
	File string
	// Stderr is the stderr of File; nil means os.Stderr. Only os.Stderr
	// can be unusable.
	Stderr io.Writer
	// MaxBytes and Backups configure file rotation; zero uses 5 MiB and
	// three backups. A record larger than MaxBytes remains whole.
	MaxBytes int64
	Backups  int
	// Errors receives file write and rotation failures; nil reports them
	// to Stderr. Repeated identical failures are suppressed until recovery.
	// The callback must support concurrent calls and must not log here.
	Errors func(error)
}

// stderrUsable reports whether stderr leads somewhere: a GUI-subsystem
// build started without a redirect has none. A variable for the tests.
var stderrUsable = func() bool {
	_, err := os.Stderr.Stat()
	return err == nil
}

// DefaultFile returns padmated's log file, padmate/logs/padmated.log in
// os.UserConfigDir: %APPDATA%\padmate\logs\padmated.log on Windows.
func DefaultFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("logging: no default log file: %w", err)
	}
	return filepath.Join(dir, "padmate", "logs", "padmated.log"), nil
}

// Open opens the log of o. A file is created with its directories and
// appended to; the Closer closes it, and does nothing for stderr.
// Files rotate at complete JSON record boundaries; stderr never rotates.
func Open(o Options) (*slog.Logger, io.Closer, error) {
	path, stderr := o.File, o.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	if path == "-" || path == "" && (stderr != io.Writer(os.Stderr) || stderrUsable()) {
		return New(stderr, o.Level), nopCloser{}, nil
	}
	if path == "" {
		p, err := DefaultFile()
		if err != nil {
			return nil, nil, err
		}
		path = p
	}
	if o.MaxBytes < 0 || o.Backups < 0 {
		return nil, nil, fmt.Errorf("logging: rotation size and backups must not be negative")
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	if o.Backups == 0 {
		o.Backups = DefaultBackups
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, fmt.Errorf("logging: %w", err)
	}
	onError := o.Errors
	if onError == nil {
		onError = func(err error) { fmt.Fprintln(stderr, "padmated:", err) }
	}
	f, err := openRotatingFile(path, o.MaxBytes, o.Backups, onError)
	if err != nil {
		return nil, nil, fmt.Errorf("logging: %w", err)
	}
	return New(f, o.Level), f, nil
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// ParseLevel parses debug, info, warn or error, in any case.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("logging: unknown level %q; want debug, info, warn or error", s)
}
