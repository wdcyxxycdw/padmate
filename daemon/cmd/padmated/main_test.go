package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/logging"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/version"
)

// Rewrite testdata/*.golden from the current output with
//
//	go test ./cmd/padmated -update
//
// and review the diff.
var update = flag.Bool("update", false, "rewrite testdata/*.golden from the current output")

const (
	// basicProfile and basicTrace are the M1a acceptance example.
	basicProfile = "../../../examples/profiles/basic.json"
	basicTrace   = "../../../examples/traces/basic.jsonl"
)

func runCmd(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// transcript runs padmated with args as text and with --decisions and
// returns both runs as a golden file shows them.
func transcript(t *testing.T, args ...string) string {
	t.Helper()
	var b strings.Builder
	for _, args := range [][]string{args, append(slices.Clone(args), "--decisions")} {
		code, stdout, stderr := runCmd(t, args...)
		fmt.Fprintf(&b, "$ padmated %s\n%s", strings.Join(args, " "), stdout)
		if stderr != "" {
			fmt.Fprintf(&b, "--- stderr\n%s", stderr)
		}
		fmt.Fprintf(&b, "exit %d\n\n", code)
	}
	return b.String()
}

// checkGolden compares got with the golden file at path, or rewrites it
// with -update.
func checkGolden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v; run go test ./cmd/padmated -update", err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s (rerun with -update and review the diff):\n--- got\n%s--- want\n%s", path, got, want)
	}
}

// TestGolden runs padmated over each testdata/<case>.jsonl, with the
// profile testdata/<case>.json or else examples/profiles/basic.json, as
// text and with --decisions, and compares the transcript with
// testdata/<case>.golden.
func TestGolden(t *testing.T) {
	traces, err := filepath.Glob("testdata/*.jsonl")
	if err != nil || len(traces) == 0 {
		t.Fatalf("no cases in testdata: %v", err)
	}
	for _, trace := range traces {
		name := strings.TrimSuffix(filepath.Base(trace), ".jsonl")
		t.Run(name, func(t *testing.T) {
			prof := "testdata/" + name + ".json"
			if _, err := os.Stat(prof); err != nil {
				prof = basicProfile
			}
			got := transcript(t, "--profile", prof, "--fake-input", "testdata/"+name+".jsonl", "--dry-run")
			checkGolden(t, "testdata/"+name+".golden", got)
		})
	}
}

// TestGoldenFeedbackSwitchesOfConfig: a replay obeys the feedback switches of
// the config.json in --data-dir (UI-27, ADR-0067), as live mode does. The
// profile of feedback-pause.json still holds the retired pause switch, set to
// false, and its golden shows that it is ignored: the pause is shown.
// Here config.json turns the pause indication off, which the replay then
// prints without the pulse and the pause color.
func TestGoldenFeedbackSwitchesOfConfig(t *testing.T) {
	got := transcript(t, "--profile", "testdata/feedback-pause.json", "--data-dir", "testdata/config-feedback-pause-off",
		"--fake-input", "testdata/feedback-pause.jsonl", "--dry-run")
	checkGolden(t, "testdata/feedback-pause-global-off.golden", got)
	if strings.Contains(got, "paused pulse") || strings.Contains(got, "#FFAA00") {
		t.Errorf("the pause is shown with config.json feedback.pause false:\n%s", got)
	}
}

// TestAcceptance is the M1a acceptance command: the example trace with
// the example profile prints the expected actions and ends with the
// release of the pan still held.
func TestAcceptance(t *testing.T) {
	args := []string{"--profile", basicProfile, "--fake-input", basicTrace, "--dry-run"}
	code, stdout, stderr := runCmd(t, args...)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q; want 0, empty", code, stderr)
	}
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if !strings.Contains(lines[0], "nothing is injected") {
		t.Errorf("header %q does not say that nothing is injected", lines[0])
	}
	for _, want := range [][]string{
		{"north down", "dispatch  tap B"},
		{"south down", "dispatch  tap Ctrl+Z"},
		{"lt down (15730)", "dispatch  hold Alt"},
		{"lt up (2310)", "release   hold Alt"},
		{"dpad.left down", "unbound"},
		{"  send +LCtrl +Z"},
		{"  send -Z -LCtrl"},
	} {
		found := false
		for _, part := range want {
			if !strings.Contains(stdout, part) {
				found = true
				break
			}
		}
		if found {
			t.Errorf("output has no sequence %q", want)
		}
	}
	tail := lines[len(lines)-3:]
	if !strings.HasSuffix(tail[0], "exit: release all") || !strings.Contains(tail[1], "east") || !strings.Contains(tail[1], "cancel    hold Space") ||
		!strings.HasSuffix(tail[2], "  send -Space") {
		t.Errorf("output ends with\n%s\nwant the exit, the cancel of the pan and -Space", strings.Join(tail, "\n"))
	}
	checkGolden(t, "testdata/acceptance.golden", transcript(t, args...))
}

// TestReplayCombo: a replay pauses with the panic combo (DMN-07) as live
// mode does. In testdata/panic.jsonl, back and start held together for 2 s
// pause the mapping at 2600 ms, which releases the keys held; the presses
// map to nothing until the resume at 6100, and the pan pressed before it
// does nothing through it; a hold of 1.9 s does nothing; and the last hold
// fires after the last event. The header counts the events of the trace,
// not the ComboHeld events among them.
func TestReplayCombo(t *testing.T) {
	code, stdout, stderr := runCmd(t, "--profile", basicProfile, "--fake-input", "testdata/panic.jsonl", "--dry-run")
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q; want 0, empty", code, stderr)
	}
	if want := "fake input: testdata/panic.jsonl, 28 events of device 1 \"panic pad\"\n"; !strings.Contains(stdout, want) {
		t.Errorf("stdout has no %q:\n%s", want, stdout)
	}
	var marks, sends, paused, function []string
	for line := range strings.Lines(stdout) {
		switch f := strings.Fields(line); {
		case len(f) >= 3 && f[1] == "mapping":
			marks = append(marks, f[0]+" "+strings.Join(f[1:], " "))
		case len(f) >= 3 && f[1] == "send":
			sends = append(sends, f[0]+" "+strings.Join(f[2:], " "))
		case len(f) >= 4 && f[3] == "paused":
			paused = append(paused, f[0]+" "+f[1]+" "+f[2])
		case len(f) >= 4 && f[3] == "function":
			function = append(function, f[0]+" "+f[1]+" "+f[2])
		}
	}
	for _, c := range []struct {
		what      string
		got, want []string
	}{
		{"marks", marks, []string{"2600.000 mapping paused: release all", "6100.000 mapping resumed", "14600.000 mapping paused: release all"}},
		{"sends", sends, []string{
			// The menu mask goes right before the release of the lone Alt.
			"100.000 +Space", "2000.000 +LAlt", "2600.000 -Space +vk:0xE8 -vk:0xE8 -LAlt",
			"7500.000 +Space", "7800.000 -Space", "8000.000 +LCtrl +Z", "8020.000 -Z -LCtrl",
			"12000.000 +Space", "14600.000 -Space",
		}},
		// back is an ordinary, unbound control here; its press is "paused" as
		// any press is.
		{"presses while paused", paused, []string{"3500.000 south down", "4000.000 back down", "5000.000 east down"}},
		// start is the function key (MAP-27, ADR-0075; the default): every
		// edge of it, paused or not, is a decision with that reason and
		// nothing else.
		{"function key edges", function, []string{
			"600.000 start down", "3000.000 start up", "4100.000 start down", "6500.000 start up",
			"9100.000 start down", "11000.000 start up", "12600.000 start down",
		}},
	} {
		if !slices.Equal(c.got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q\nstdout:\n%s", c.what, c.got, c.want, stdout)
		}
	}
}

func TestUsage(t *testing.T) {
	const (
		choose = "choose --dry-run to print the keys or --inject to press them\n"
		noMode = "padmated: live mode needs --dry-run or --inject: " + choose
		noDry  = "padmated: --fake-input needs --dry-run\n"
	)
	replay := []string{"--profile", basicProfile, "--fake-input", basicTrace, "--dry-run"}
	liveOnlyErr := func(flag string) string {
		return "padmated: --" + flag + " is only for live mode, not with --fake-input\n"
	}
	for _, tc := range []struct {
		name       string
		args       []string
		code       int
		stdout     string // prefix
		stderrHead string // prefix; the usage follows
	}{
		{"help", []string{"-h"}, 0, "usage: padmated", ""},
		{"help double dash", []string{"--help"}, 0, "usage: padmated", ""},
		{"no flags", nil, 2, "", "padmated: --profile is required\n"},
		{"no profile", []string{"--fake-input", basicTrace, "--dry-run"}, 2, "", "padmated: --profile is required\n"},
		{"live without a mode", []string{"--profile", basicProfile}, 2, "", noMode},
		{"live dry run false", []string{"--profile", basicProfile, "--dry-run=false"}, 2, "", noMode},
		{"live both modes", []string{"--profile", basicProfile, "--dry-run", "--inject"}, 2, "",
			"padmated: --dry-run and --inject cannot be combined: " + choose},
		{"inject with decisions", []string{"--profile", basicProfile, "--inject", "--decisions"}, 2, "",
			"padmated: --decisions needs --dry-run\n"},
		{"replay without dry run", []string{"--profile", basicProfile, "--fake-input", basicTrace}, 2, "", noDry},
		{"replay dry run false", []string{"--profile", basicProfile, "--fake-input", basicTrace, "--dry-run=false"}, 2, "", noDry},
		{"replay with inject", append(slices.Clone(replay), "--inject"), 2, "", liveOnlyErr("inject")},
		{"replay with device", append(slices.Clone(replay), "--device", "xbox"), 2, "", liveOnlyErr("device")},
		{"replay with preset", append(slices.Clone(replay), "--preset", "xinput"), 2, "", liveOnlyErr("preset")},
		{"replay with hint", append(slices.Clone(replay), "--hint", "A=1"), 2, "", liveOnlyErr("hint")},
		{"replay with watch", append(slices.Clone(replay), "--watch"), 2, "", liveOnlyErr("watch")},
		{"replay with tray", append(slices.Clone(replay), "--tray=false"), 2, "", liveOnlyErr("tray")},
		{"bad preset", []string{"--profile", basicProfile, "--dry-run", "--preset", "nope"}, 2, "",
			"padmated: unknown preset \"nope\"; want one of default, xinput, rawinput, hidapi-off, wgi, gameinput\n"},
		{"bad hint", []string{"--profile", basicProfile, "--dry-run", "--hint", "NOVALUE"}, 2, "",
			"invalid value \"NOVALUE\" for flag -hint: hint \"NOVALUE\": want NAME=VALUE\n"},
		{"bad log level", []string{"--profile", basicProfile, "--dry-run", "--log-level", "loud"}, 2, "",
			"invalid value \"loud\" for flag -log-level: logging: unknown level \"loud\"; want debug, info, warn or error\n"},
		{"argument", append(slices.Clone(replay), "extra"), 2, "", "padmated: unexpected argument \"extra\"\n"},
		{"live argument", []string{"--profile", basicProfile, "--dry-run", "extra"}, 2, "", "padmated: unexpected argument \"extra\"\n"},
		{"unknown flag", []string{"--bogus"}, 2, "", "flag provided but not defined: -bogus\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runCmd(t, tc.args...)
			if code != tc.code || !strings.HasPrefix(stdout, tc.stdout) || tc.stdout == "" && stdout != "" {
				t.Errorf("exit %d, stdout %q; want %d, %q...", code, stdout, tc.code, tc.stdout)
			}
			wantErr := ""
			if tc.code == 2 {
				wantErr = tc.stderrHead + usage
			}
			if stderr != wantErr {
				t.Errorf("stderr = %q, want %q", stderr, wantErr)
			}
		})
	}
}

// TestParseFlags checks the options of valid flags.
func TestParseFlags(t *testing.T) {
	def, _ := input.PresetHints("default")
	xinput, _ := input.PresetHints("xinput")
	for _, tc := range []struct {
		name string
		args []string
		want options
	}{
		{"live dry run", []string{"--profile", "p.json", "--dry-run"},
			options{profile: "p.json", dryRun: true, watch: true, tray: true, sdl: input.SDLFlags{Preset: "default"}, hints: def}},
		{"live without tray", []string{"--profile", "p.json", "--dry-run", "--tray=false"},
			options{profile: "p.json", dryRun: true, watch: true, sdl: input.SDLFlags{Preset: "default"}, hints: def}},
		{"live without watching", []string{"--profile", "p.json", "--dry-run", "--watch=false"},
			options{profile: "p.json", dryRun: true, tray: true, sdl: input.SDLFlags{Preset: "default"}, hints: def}},
		{"live decisions", []string{"--profile", "p.json", "--dry-run", "--decisions", "--log-level", "warn"},
			options{profile: "p.json", dryRun: true, decisions: true, watch: true, tray: true, sdl: input.SDLFlags{Preset: "default"}, hints: def, logLevel: slog.LevelWarn}},
		{"live inject", []string{
			"--profile", "p.json", "--inject", "--device", "Xbox", "--preset", "xinput", "--hint", "A=1",
			"--log-level", "DEBUG", "--log-file", "-",
		}, options{
			profile: "p.json", inject: true, watch: true, tray: true, device: "Xbox",
			sdl:   input.SDLFlags{Preset: "xinput", Extra: input.HintList{{Name: "A", Value: "1"}}},
			hints: append(slices.Clone(xinput), input.Hint{Name: "A", Value: "1"}), logLevel: slog.LevelDebug, logFile: "-",
		}},
		{"replay", []string{"--profile", "p.json", "--fake-input", "t.jsonl", "--dry-run", "--decisions", "--log-level", "error", "--log-file", "f.log"},
			options{profile: "p.json", fakeInput: "t.jsonl", dryRun: true, decisions: true, watch: true, tray: true, sdl: input.SDLFlags{Preset: "default"}, logLevel: slog.LevelError, logFile: "f.log"}},
		{"version", []string{"-version", "--inject"}, options{inject: true, version: true, watch: true, tray: true, sdl: input.SDLFlags{Preset: "default"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			opts, code, ok := parseFlags(tc.args, &stdout, &stderr)
			if !ok || code != 0 || stdout.Len() > 0 || stderr.Len() > 0 {
				t.Fatalf("parseFlags: ok %v, code %d, stdout %q, stderr %q", ok, code, stdout.String(), stderr.String())
			}
			// Forwarding preserves spelling separately from the parsed settings.
			if !slices.Equal(opts.forwardArgs, tc.args) {
				t.Fatal("startup arguments not retained")
			}
			opts.forwardArgs, opts.explicitFlags = nil, nil
			if !reflect.DeepEqual(opts, tc.want) {
				t.Errorf("options = %+v, want %+v", opts, tc.want)
			}
		})
	}
}

// TestReplayLog: the replay opens the log too, and logs only at debug
// level.
func TestReplayLog(t *testing.T) {
	for _, tc := range []struct {
		level string
		want  []string
	}{
		{"info", nil},
		{"debug", []string{`"msg":"padmated starting"`, `"mode":"replay"`, `"msg":"padmated stopped"`, `"code":0`}},
	} {
		t.Run(tc.level, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "logs", "padmated.log")
			code, _, stderr := runCmd(t, "--profile", basicProfile, "--fake-input", basicTrace, "--dry-run",
				"--log-level", tc.level, "--log-file", path)
			if code != 0 || stderr != "" {
				t.Fatalf("exit %d, stderr %q; want 0, empty", code, stderr)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == nil && len(data) > 0 {
				t.Errorf("log at %s = %q, want empty", tc.level, data)
			}
			for _, want := range tc.want {
				if !strings.Contains(string(data), want) {
					t.Errorf("log has no %s:\n%s", want, data)
				}
			}
		})
	}
}

// TestReplayLogStderr: with --log-file - the log goes to the stderr that
// padmated was given, not to the process's.
func TestReplayLogStderr(t *testing.T) {
	code, _, stderr := runCmd(t, "--profile", basicProfile, "--fake-input", basicTrace, "--dry-run", "--log-level", "debug", "--log-file", "-")
	if code != 0 || !strings.Contains(stderr, `"msg":"padmated starting"`) || !strings.Contains(stderr, `"msg":"padmated stopped"`) {
		t.Errorf("exit %d, stderr %q; want 0, the log", code, stderr)
	}
}

// TestLogError: a log that does not open stops padmated before anything
// runs, in both modes: a replay exits 1, live mode exitStartup (UI-45).
func TestLogError(t *testing.T) {
	dir := t.TempDir() // a directory is not a file to append to
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"--profile", basicProfile, "--fake-input", basicTrace, "--dry-run", "--log-file", dir}, 1},
		{[]string{"--profile", basicProfile, "--dry-run", "--log-file", dir}, exitStartup},
	} {
		code, stdout, stderr := runCmd(t, tc.args...)
		if code != tc.code || stdout != "" || !strings.HasPrefix(stderr, "padmated: logging: ") {
			t.Errorf("%q: exit %d, stdout %q, stderr %q; want %d, empty, the log's error", tc.args, code, stdout, stderr, tc.code)
		}
	}
}

// TestLiveOpenError: when SDL does not load, live mode exits exitStartup
// with the error, after logging it, and the startup report says so
// (UI-45). It needs no SDL.
func TestLiveOpenError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(input.EnvLibrary, filepath.Join(dir, "missing", "SDL3.dll"))
	logFile := filepath.Join(dir, "padmated.log")
	reportFile := filepath.Join(dir, "startup-error.json")
	var stdout, stderr bytes.Buffer
	code := runContext(context.Background(), []string{"--startup-report", reportFile, "--profile", basicProfile, "--dry-run", "--log-file", logFile}, &stdout, &stderr)
	if code != exitStartup || stdout.Len() > 0 || !strings.HasPrefix(stderr.String(), "padmated: "+input.ErrNoLibrary.Error()) {
		t.Errorf("exit %d, stdout %q, stderr %q; want %d, empty, %q...", code, stdout.String(), stderr.String(), exitStartup, input.ErrNoLibrary)
	}
	report, err := os.ReadFile(reportFile)
	if err != nil || !strings.Contains(string(report), `"kind":"sdl","cause":"missing"`) || !strings.Contains(string(report), input.ErrNoLibrary.Error()) {
		t.Errorf("report %s: %v", report, err)
	}
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"msg":"padmated starting"`, `"mode":"dry-run"`, `"preset":"default"`, `"slots":4`, `"msg":"SDL did not start"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("log has no %s:\n%s", want, data)
		}
	}
}

// TestStartLiveOpenError: startLive returns Open's error and needs no
// cleanup.
func TestStartLiveOpenError(t *testing.T) {
	opts, _, ok := parseFlags([]string{"--profile", basicProfile, "--dry-run"}, io.Discard, io.Discard)
	if !ok {
		t.Fatal("parseFlags failed")
	}
	p, prog, _, _ := loadProfile(basicProfile, io.Discard)
	errOpen := errors.New("no SDL here")
	var stdout, stderr, logs bytes.Buffer
	run, err := startLive(context.Background(), liveConfig{
		opts: opts, profile: p, prog: prog, log: logging.New(&logs, slog.LevelInfo), stdout: &stdout, stderr: &stderr,
		open: func(input.Options) (eventSource, error) { return nil, errOpen },
	})
	if run != nil || !errors.Is(err, errOpen) {
		t.Errorf("startLive = %v, %v; want nil, %v", run, err, errOpen)
	}
	if stdout.Len() > 0 || stderr.String() != "padmated: no SDL here\n" {
		t.Errorf("stdout %q, stderr %q; want empty, the error", stdout.String(), stderr.String())
	}
	if !strings.Contains(logs.String(), `"msg":"SDL did not start","err":"no SDL here"`) {
		t.Errorf("log has no error:\n%s", logs.String())
	}
}

// countSender counts the calls of newInject; its Sender must never be
// used.
type countSender struct {
	calls int
	err   error
}

func (c *countSender) newInject() (output.Sender, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return c, nil
}

func (*countSender) Send([]output.KeyEvent) (int, error) { panic("countSender: Send") }

// TestChooseSender: the dry run's Sender is the printer, and only
// --inject constructs the inject Sender (CLAUDE.md invariant 6).
func TestChooseSender(t *testing.T) {
	pr := &printer{}
	c := &countSender{}
	if s, err := chooseSender(false, pr, c.newInject); s != output.Sender(pr) || err != nil || c.calls != 0 {
		t.Errorf("dry run: %v, %v after %d calls; want the printer, nil, no call", s, err, c.calls)
	}
	if s, err := chooseSender(true, pr, c.newInject); s != output.Sender(c) || err != nil || c.calls != 1 {
		t.Errorf("inject: %v, %v after %d calls; want the inject Sender, nil, 1 call", s, err, c.calls)
	}
	c = &countSender{err: errors.New("no injection")}
	if s, err := chooseSender(true, pr, c.newInject); s != nil || !errors.Is(err, c.err) {
		t.Errorf("inject failing: %v, %v; want nil, %v", s, err, c.err)
	}
}

func TestVersion(t *testing.T) {
	for _, arg := range []string{"-version", "--version"} {
		code, stdout, stderr := runCmd(t, arg)
		if want := "padmated " + version.String() + "\n"; code != 0 || stdout != want || stderr != "" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want 0, %q, empty", arg, code, stdout, stderr, want)
		}
	}
}

// TestErrors checks the errors before the replay starts: each exits 1
// with a message on stderr and prints nothing on stdout.
func TestErrors(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		path := filepath.ToSlash(filepath.Join(dir, name))
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	badProfile := write("bad.json", `{"schemaVersion": 0, "name": "t", "layers": {"base": {"east": {"press": {"type": "keys", "keys": "Spcae"}}}}}`)
	badTrace := write("bad.jsonl", "{\"timeNs\":0,\"kind\":\"added\",\"device\":1}\n{\"timeNs\":1,\"kind\":\"press\",\"device\":1}\n")
	empty := write("empty.jsonl", "# nothing\n\n")
	// An event 292 years after the first does not fit the engine's clock.
	farTrace := write("far.jsonl", "{\"timeNs\":0,\"kind\":\"added\",\"device\":1}\n"+
		"{\"timeNs\":1000000,\"kind\":\"down\",\"device\":1,\"control\":\"east\"}\n"+
		"{\"timeNs\":9223372036854775808,\"kind\":\"up\",\"device\":1,\"control\":\"east\"}\n")
	// SDL does not reuse device ids.
	backTrace := write("back.jsonl", "{\"timeNs\":0,\"kind\":\"added\",\"device\":1}\n"+
		"{\"timeNs\":1,\"kind\":\"removed\",\"device\":1}\n"+
		"{\"timeNs\":2,\"kind\":\"added\",\"device\":1}\n")
	missing := filepath.ToSlash(filepath.Join(dir, "missing.json"))
	for _, tc := range []struct {
		name, profile, trace, want string
	}{
		{"profile missing", missing, basicTrace, "padmated: " + missing + ": "},
		{"profile is a directory", dir, basicTrace, "padmated: " + dir + ": is a directory\n"},
		{"profile errors", badProfile, basicTrace, badProfile + ":1:"},
		{"trace missing", basicProfile, missing, "padmated: " + missing + ": "},
		{"trace is a directory", basicProfile, dir, "padmated: " + dir + ": is a directory\n"},
		{"trace line", basicProfile, badTrace, "padmated: " + badTrace + ": line 2: unknown kind \"press\";"},
		{"trace empty", basicProfile, empty, "padmated: " + empty + ": no events\n"},
		{"trace time out of range", basicProfile, farTrace,
			"padmated: " + farTrace + ": line 3: timeNs: want an integer from 0 to 9223372036854775807, got 9223372036854775808\n"},
		{"trace device id reused", basicProfile, backTrace, "padmated: " + backTrace + ": line 3: device 1 was removed on line 2; "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runCmd(t, "--profile", tc.profile, "--fake-input", tc.trace, "--dry-run")
			if code != 1 || stdout != "" || !strings.HasPrefix(stderr, tc.want) {
				t.Errorf("exit %d, stdout %q, stderr %q; want 1, empty, %q...", code, stdout, stderr, tc.want)
			}
		})
	}
}

// TestInterrupted: a stop signal still takes the exit path, which releases
// what is held, and exits 1.
func TestInterrupted(t *testing.T) {
	if !slices.Contains(stopSignals, os.Interrupt) || !slices.Contains(stopSignals, os.Signal(syscall.SIGTERM)) {
		t.Errorf("stopSignals = %v, want Interrupt and SIGTERM", stopSignals)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	code := runContext(ctx, []string{"--profile", basicProfile, "--fake-input", basicTrace, "--dry-run"}, &stdout, &stderr)
	if code != 1 || stderr.String() != "padmated: interrupted\n" {
		t.Errorf("exit %d, stderr %q; want 1, interrupted", code, stderr.String())
	}
	if out := stdout.String(); !strings.HasSuffix(out, "exit: release all\n"+
		" 11718.645  east                cancel    hold Space   \"抓手\"  /layers/base/east\n"+
		" 11718.645    send -Space\n") {
		t.Errorf("stdout does not end with the release of the pan:\n%s", out)
	}
}

// TestReplayError: when the replay fails, padmated still takes the exit
// path and exits 1. ReadEvents rejects times beyond the clock, so the
// events are built here.
func TestReplayError(t *testing.T) {
	var discard bytes.Buffer
	p, prog, _, ok := loadProfile(basicProfile, &discard)
	if !ok {
		t.Fatalf("loading %s: %s", basicProfile, discard.String())
	}
	evs := []input.Event{
		{Time: 0, Device: 1, Kind: input.DeviceAdded},
		{Time: 1_000_000, Device: 1, Kind: input.ButtonDown, Control: "east"},
		{Time: math.MaxUint64, Device: 1, Kind: input.ButtonUp, Control: "east"},
	}
	var stdout, stderr bytes.Buffer
	opts := options{profile: basicProfile, fakeInput: "built.jsonl", dryRun: true}
	code := replayDryRun(context.Background(), opts, p, prog, device{id: 1}, evs, &stdout, &stderr)
	if want := "padmated: slot: event 2: time 18446744073709551615ns is out of range\n"; code != 1 || stderr.String() != want {
		t.Errorf("exit %d, stderr %q; want 1, %q", code, stderr.String(), want)
	}
	if out := stdout.String(); !strings.HasSuffix(out, "  0.000  exit: release all\n") {
		t.Errorf("stdout does not end with the exit path:\n%s", out)
	}
}

// failWriter fails every write.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWriteError(t *testing.T) {
	var stderr bytes.Buffer
	code := run([]string{"--profile", basicProfile, "--fake-input", basicTrace, "--dry-run"}, failWriter{}, &stderr)
	if want := "padmated: writing the output: disk full\n"; code != 1 || stderr.String() != want {
		t.Errorf("exit %d, stderr %q; want 1, %q", code, stderr.String(), want)
	}
}
