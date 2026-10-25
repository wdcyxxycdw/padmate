package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/instance"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
)

// startupReportKeys are the fields every report has (UI-45, ADR-0056).
var startupReportKeys = []string{"cause", "col", "dataDir", "errors", "file", "kind", "line", "message", "pointer", "version"}

// runReport runs padmated with args and --startup-report, first, as the UI
// passes it, and returns the exit code, stderr and the report. The report
// starts out stale, so that a run that writes none fails the test.
func runReport(t *testing.T, args ...string) (code int, stderr string, rep startupReport) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "startup-error.json")
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, errOut bytes.Buffer
	code = run(append([]string{"--startup-report", path}, args...), &stdout, &errOut)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("exit %d, no report: %v\nstderr:\n%s", code, err, &errOut)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("exit %d, report %q: %v\nstderr:\n%s", code, data, err, &errOut)
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, startupReportKeys) {
		t.Errorf("report fields %v, want %v", keys, startupReportKeys)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rep); err != nil {
		t.Fatal(err)
	}
	if rep.Version != 1 || rep.Errors == nil {
		t.Errorf("report %+v: want version 1 and an errors list", rep)
	}
	return code, errOut.String(), rep
}

// TestStartupReportConfig: a config.json with errors no longer stops the
// start (PRO-29, the owner's G1 after F17): padmated repairs it. One it
// cannot read still does, and the report names the file.
func TestStartupReportConfig(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	if err := os.Mkdir(config, 0o700); err != nil {
		t.Fatal(err)
	}
	code, stderr, rep := runReport(t, "--data-dir", dir, "--dry-run", "--tray=false")
	if code != exitStartup || !strings.Contains(stderr, "padmated: config.json: ") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if rep.Kind != "config" || rep.Cause != "other" || rep.File != config || rep.Line != 0 || len(rep.Errors) != 0 || rep.Message == "" || strings.Contains(rep.Message, config) || rep.DataDir != dir {
		t.Fatalf("report %+v", rep)
	}
}

// TestStartupReportFiles: the profile and the log each name themselves; a
// data folder that is a file is the library's problem. A state.json with
// errors no longer stops the start (PRO-29, TestStartResetsBrokenState).
func TestStartupReportFiles(t *testing.T) {
	dir := t.TempDir()
	notDir := filepath.Join(dir, "file")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	badProfile := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badProfile, []byte("{\"schemaVersion\":0,\n\"layers\":{\"base\":{\"nope\":{}}}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		args        []string
		kind, cause string
		file        string
		line        bool // the report has a line and column
	}{
		{"library", []string{"--data-dir", notDir, "--dry-run"}, "library", "other", notDir, false},
		{"profile errors", []string{"--profile", badProfile, "--dry-run"}, "profile", "invalid", badProfile, true},
		{"profile missing", []string{"--profile", filepath.Join(dir, "missing.json"), "--dry-run"}, "profile", "missing", filepath.Join(dir, "missing.json"), false},
		{"library profile missing", []string{"--data-dir", filepath.Join(dir, "library"), "--profile-id", "gone", "--dry-run"}, "profile", "missing",
			filepath.Join(dir, "library", "profiles", "gone", "profile.json"), false},
		{"log", []string{"--profile", basicProfile, "--dry-run", "--log-file", logDir}, "log", "", logDir, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stderr, rep := runReport(t, tc.args...)
			if code != exitStartup || stderr == "" {
				t.Fatalf("exit %d, stderr %q", code, stderr)
			}
			if rep.Kind != tc.kind || tc.cause != "" && rep.Cause != tc.cause || rep.File != tc.file || rep.Message == "" {
				t.Fatalf("report %+v, want kind %s, cause %s, file %s", rep, tc.kind, tc.cause, tc.file)
			}
			if tc.line != (rep.Line > 0 && rep.Col > 0 && len(rep.Errors) > 0) {
				t.Fatalf("report %+v: line and errors %v", rep, tc.line)
			}
			if strings.Contains(rep.Message, rep.File) {
				t.Errorf("message %q repeats the file", rep.Message)
			}
		})
	}
}

// TestStartupReportUsage: a usage error exits 2, as before, and still
// reports what is wrong when the report's flag came first.
func TestStartupReportUsage(t *testing.T) {
	code, _, rep := runReport(t, "--profile", basicProfile, "--dry-run", "--inject")
	if code != 2 || rep.Kind != "usage" || !strings.Contains(rep.Message, "--dry-run and --inject cannot be combined") {
		t.Fatalf("exit %d, report %+v", code, rep)
	}
	code, _, rep = runReport(t, "--bogus")
	if code != 2 || rep.Kind != "usage" || rep.Message != "flag provided but not defined: -bogus" {
		t.Fatalf("exit %d, report %+v", code, rep)
	}
	code, _, rep = runReport(t, "--profile", basicProfile, "--fake-input", "--inject")
	if code != 2 || rep.Kind != "usage" || rep.Message != "--fake-input needs --dry-run" {
		t.Fatalf("exit %d, report %+v", code, rep)
	}
}

// TestStartupReportCauses: the instance's problems, SDL, the data folder,
// a panic, a usage error found after the flags (exit 2), the report's
// folder, which is created, and an unwritable report, which changes
// nothing but stderr.
func TestStartupReportCauses(t *testing.T) {
	refused := forwardFailure(&jrpc2.Error{Code: -32011, Message: "output mode differs; restart the daemon to change --dry-run/--inject"})
	if rep := newStartupReport(options{}, refused); rep.Kind != "instance" || rep.Cause != "refused" ||
		rep.Message != "output mode differs; restart the daemon to change --dry-run/--inject" {
		t.Fatalf("refused: %+v", rep)
	}
	if rep := newStartupReport(options{}, forwardFailure(errors.New("running instance IPC unavailable: context deadline exceeded"))); rep.Kind != "instance" || rep.Cause != "unavailable" {
		t.Fatalf("unavailable: %+v", rep)
	}
	if rep := newStartupReport(options{dataDir: "relative"}, errors.New("boom")); rep.Kind != "internal" || rep.Cause != "other" || rep.Message != "boom" || !filepath.IsAbs(rep.DataDir) {
		t.Fatalf("internal: %+v", rep)
	}
	noSDL := fmt.Errorf("%w: no SDL3.dll next to padmated.exe", input.ErrNoLibrary)
	if rep := newStartupReport(options{}, &startupError{kind: "sdl", err: noSDL}); rep.Kind != "sdl" || rep.Cause != "missing" {
		t.Fatalf("SDL: %+v", rep)
	}
	dir := filepath.Join(t.TempDir(), "data")
	denied := &startupError{kind: "library", file: dir, prefix: "data directory", err: &fs.PathError{Op: "open", Path: dir, Err: fs.ErrPermission}}
	if rep := newStartupReport(options{dataDir: dir}, denied); rep.Kind != "library" || rep.Cause != "denied" || rep.File != dir || rep.Message != fs.ErrPermission.Error() {
		t.Fatalf("denied: %+v", rep)
	}
	if rep := newStartupReport(options{}, startupPanic("boom")); rep.Kind != "internal" || rep.Message != "panic: boom" {
		t.Fatalf("panic: %+v", rep)
	}
	var stderr bytes.Buffer
	report := filepath.Join(t.TempDir(), "missing", "dir", "report.json")
	usage := &startupError{kind: "usage", err: errors.New("fake IPC input requires dry-run")}
	if code := startupFailed(options{startupReport: report}, &stderr, usage); code != 2 || stderr.Len() > 0 {
		t.Fatalf("usage: exit %d, stderr %q", code, &stderr)
	}
	if data, err := os.ReadFile(report); err != nil || !strings.Contains(string(data), `"kind":"usage"`) {
		t.Fatalf("report %s in a new folder: %v", data, err)
	}
	notDir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := options{startupReport: filepath.Join(notDir, "report.json")}
	if code := startupFailed(opts, &stderr, errors.New("boom")); code != exitStartup || !strings.Contains(stderr.String(), "padmated: writing the startup report:") {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
}

// TestStartupReportLive: live mode's other failures, through live as the
// UI starts it: a panic while starting (here acquiring the instance),
// which still exits exitStartup, and the IPC endpoint. The report is a
// file, so that the GUI build, which has no stderr, writes it too.
func TestStartupReportLive(t *testing.T) {
	report := filepath.Join(t.TempDir(), "startup-error.json")
	opts := options{profile: basicProfile, dryRun: true, startupReport: report}
	read := func() startupReport {
		t.Helper()
		var rep startupReport
		data, err := os.ReadFile(report)
		if err == nil {
			err = json.Unmarshal(data, &rep)
		}
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	acquire := acquireInstance
	t.Cleanup(func() { acquireInstance = acquire })
	acquireInstance = func(string) (*instance.Lease, error) { panic("acquire boom") }
	if code := live(context.Background(), opts, io.Discard, failingWriter{}); code != exitStartup {
		t.Fatalf("panic: exit %d", code)
	}
	if rep := read(); rep.Kind != "internal" || rep.Message != "panic: acquire boom" {
		t.Fatalf("panic: %+v", rep)
	}
	acquireInstance = acquire
	listen := listenIPC
	t.Cleanup(func() { listenIPC = listen })
	listenIPC = func(string, string) (*ipc.Endpoint, error) {
		return nil, &fs.PathError{Op: "create", Path: `\\.\pipe\taken`, Err: fs.ErrPermission}
	}
	var stderr bytes.Buffer
	if code := live(context.Background(), opts, io.Discard, &stderr); code != exitStartup || !strings.Contains(stderr.String(), "padmated: IPC: ") {
		t.Fatalf("IPC: exit %d, stderr %q", code, &stderr)
	}
	if rep := read(); rep.Kind != "ipc" || rep.Cause != "denied" {
		t.Fatalf("IPC: %+v", rep)
	}
}

// failingWriter is a stderr that does not exist, as in the GUI build.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("no stderr") }

// TestStartupReportRemovedFirst: padmated removes a report left by an
// earlier start as soon as it has read its flags, before it starts, so
// that a start that hangs, or is killed, leaves none (UI-45). The UI
// passes the flag first, before the packaged UI's --inject or the
// development UI's --dry-run and --data-dir.
func TestStartupReportRemovedFirst(t *testing.T) {
	report := filepath.Join(t.TempDir(), "startup-error.json")
	if err := os.WriteFile(report, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--startup-report", report, "-version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, &stderr)
	}
	if _, err := os.Stat(report); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stale report kept: %v", err)
	}
	data := t.TempDir()
	for _, args := range [][]string{
		{"--startup-report", report, "--inject", "--data-dir", data},
		{"--startup-report", report, "--dry-run", "--data-dir", data},
	} {
		opts, code, ok := parseFlags(args, &stdout, &stderr)
		if !ok || code != 0 || opts.startupReport != report || opts.dataDir != data {
			t.Fatalf("%q: %d %v %s", args, code, ok, &stderr)
		}
	}
}
