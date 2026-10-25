package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
	"github.com/wdcyxxycdw/padmate/daemon/internal/tray"
)

// memoryStartup is a startup entry in memory: value is Run's padmate
// value, own the command this padmated writes. It records each Set.
type memoryStartup struct {
	mu    sync.Mutex
	value string
	own   string
	err   error
	sets  []bool
}

func (s *memoryStartup) Enabled() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value != "", s.err
}

func (s *memoryStartup) Current() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value != "" && s.value == s.own, s.err
}

func (s *memoryStartup) Set(v bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sets = append(s.sets, v)
	if s.err != nil {
		return s.err
	}
	s.value = ""
	if v {
		s.value = s.own
	}
	return nil
}

func (s *memoryStartup) calls() []bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sets)
}

// inMemory makes memoryStartup a memoryEntry, which a test build uses.
func (*memoryStartup) inMemory() {}

// unmarkedStartup stands for a real entry (an *autostart.Entry) in a test
// build: it is not a memoryEntry, but writes only memory.
type unmarkedStartup struct{ m *memoryStartup }

func (s unmarkedStartup) Enabled() (bool, error) { return s.m.Enabled() }
func (s unmarkedStartup) Current() (bool, error) { return s.m.Current() }
func (s unmarkedStartup) Set(v bool) error       { return s.m.Set(v) }

// startupTestRun is a liveRun with only IPC and the startup control that
// create gives opts.
func startupTestRun(opts options, create func(options) (startupSetting, error)) *liveRun {
	log := slog.New(slog.DiscardHandler)
	return &liveRun{events: ipc.NewHub(), log: log, startup: newStartupControl(opts, create, log)}
}

func callStartup(t *testing.T, client *jrpc2.Client, method string, params any) (startupState, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), liveWait)
	defer cancel()
	var st startupState
	err := client.CallResult(ctx, method, params, &st)
	return st, err
}

// TestStartupRPC: startup.get and startup.set read and write padmated's
// own entry, in its own mode; enabling replaces another padmate's entry.
// Neither takes an executable or arguments from the client.
func TestStartupRPC(t *testing.T) {
	entry := &memoryStartup{value: `"C:\Old\padmated.exe" --dry-run`, own: `"C:\padmate\padmated.exe" --inject`}
	r := startupTestRun(options{inject: true}, func(options) (startupSetting, error) { return entry, nil })
	client := rpcTestClient(t, r)
	check := func(got startupState, err error, want startupState) {
		t.Helper()
		if err != nil || got != want {
			t.Fatalf("startup %+v, %v; want %+v", got, err, want)
		}
	}
	st, err := callStartup(t, client, "startup.get", nil)
	check(st, err, startupState{Available: true, Enabled: true, Mode: "inject"})
	st, err = callStartup(t, client, "startup.set", map[string]bool{"enabled": true})
	check(st, err, startupState{Available: true, Enabled: true, Current: true, Mode: "inject"})
	st, err = callStartup(t, client, "startup.get", map[string]any{})
	check(st, err, startupState{Available: true, Enabled: true, Current: true, Mode: "inject"})
	st, err = callStartup(t, client, "startup.set", map[string]bool{"enabled": false})
	check(st, err, startupState{Available: true, Mode: "inject"})

	for _, tc := range []struct {
		method string
		params any
	}{
		{"startup.set", map[string]any{}},
		{"startup.set", map[string]any{"enabled": "yes"}},
		{"startup.set", map[string]any{"enabled": true, "exe": `C:\evil.exe`}},
		{"startup.set", map[string]any{"enabled": true, "args": []string{"--dry-run"}}},
		{"startup.get", map[string]any{"mode": "dry-run"}},
	} {
		if _, err := callStartup(t, client, tc.method, tc.params); jrpc2.ErrorCode(err) != jrpc2.InvalidParams {
			t.Errorf("%s %v: %v, want invalid params", tc.method, tc.params, err)
		}
	}
	if sets := entry.calls(); !slices.Equal(sets, []bool{true, false}) {
		t.Fatalf("writes %v", sets)
	}

	// A failed write is the system's error, not "unavailable".
	entry.mu.Lock()
	entry.err = errors.New("access denied")
	entry.mu.Unlock()
	if _, err := callStartup(t, client, "startup.set", map[string]bool{"enabled": true}); err == nil || jrpc2.ErrorCode(err) == rpcCodeStartupUnavailable {
		t.Fatalf("failed write: %v", err)
	}
}

// TestStartupRPCDryRunMode: a dry-run padmated's entry starts a dry-run
// padmated, and says so.
func TestStartupRPCDryRunMode(t *testing.T) {
	entry := &memoryStartup{own: `"C:\padmate\padmated.exe" --dry-run`}
	r := startupTestRun(options{dryRun: true}, func(options) (startupSetting, error) { return entry, nil })
	st, err := callStartup(t, rpcTestClient(t, r), "startup.set", map[string]bool{"enabled": true})
	if err != nil || st != (startupState{Available: true, Enabled: true, Current: true, Mode: "dry-run"}) {
		t.Fatalf("%+v, %v", st, err)
	}
}

// TestDevelopmentBuildWritesNoStartupEntry: a development or test build
// (an instance name or IPC pipe of its own) never creates or writes the
// user's startup entry, whatever the tray or a client asks (UI-19,
// ADR-0051, ADR-0052).
func TestDevelopmentBuildWritesNoStartupEntry(t *testing.T) {
	for _, tc := range []struct {
		instance, pipe string
		dev            bool
	}{
		{"", "", false},
		{`Local\padmate.instance-dev`, "", true},
		{"", `\\.\pipe\padmate-dev`, true},
		{`Local\padmate.instance-dev`, `\\.\pipe\padmate-dev`, true},
	} {
		if got := devBuild(tc.instance, tc.pipe); got != tc.dev {
			t.Errorf("devBuild(%q, %q) = %v", tc.instance, tc.pipe, got)
		}
	}
	// TestMain gives the tests an instance name and pipe of their own.
	if !developmentBuild() {
		t.Fatal("the test build is not a development build")
	}
	created := false
	startup, err := newStartup(options{profile: basicProfile, dryRun: true}, func() (string, error) {
		t.Error("executable asked in a development build")
		return `C:\padmate\padmated.exe`, nil
	}, func(string, []string) (startupSetting, error) {
		created = true
		return &memoryStartup{}, nil
	})
	if startup != nil || !errors.Is(err, errDevelopmentBuild) || created {
		t.Fatalf("development build got a startup entry: %v, %v, created %v", startup, err, created)
	}

	// The RPCs say why, and refuse to write.
	client := rpcTestClient(t, startupTestRun(options{inject: true}, nil))
	st, err := callStartup(t, client, "startup.get", nil)
	if err != nil || st != (startupState{Mode: "inject", Reason: "development"}) {
		t.Fatalf("startup.get %+v, %v", st, err)
	}
	_, err = callStartup(t, client, "startup.set", map[string]bool{"enabled": true})
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpcCodeStartupUnavailable || !strings.Contains(rpcErr.Message, "development") {
		t.Fatalf("startup.set in a development build: %v", err)
	}
	if err := json.Unmarshal(rpcErr.Data, &st); err != nil || st.Available || st.Reason != "development" {
		t.Fatalf("error data %s: %v", rpcErr.Data, err)
	}

	// A live run's tray has no entry: a click writes nothing.
	src, tl := newFakeSource(), &testLive{}
	cfg := testConfig(t, tl, "", nil, "--dry-run", "--watch=false")
	cfg.open = func(input.Options) (eventSource, error) { return src, nil }
	tl.start(t, cfg)
	if tl.startup.available() || !tl.startup.development() {
		t.Fatal("a test build's live run has a startup entry")
	}
	runTrayClick(t, tl, func() bool {
		return strings.Contains(tl.log.String(), "startup entry not changed in a development build")
	})
}

// TestDevelopmentBuildRefusesRealStartupEntry: the second guard. A
// development or test build handed an entry that is not a test's
// in-memory one (as a real *autostart.Entry is not) treats it as none:
// neither a client nor the tray writes it, nor does a control made
// without newStartupControl.
func TestDevelopmentBuildRefusesRealStartupEntry(t *testing.T) {
	if !developmentBuild() {
		t.Fatal("the test build is not a development build")
	}
	mem := &memoryStartup{value: `"C:\Old\padmated.exe" --inject`, own: `"C:\padmate\padmated.exe" --inject`}
	realEntry := func(options) (startupSetting, error) { return unmarkedStartup{mem}, nil }

	client := rpcTestClient(t, startupTestRun(options{inject: true}, realEntry))
	st, err := callStartup(t, client, "startup.get", nil)
	if err != nil || st != (startupState{Mode: "inject", Reason: "development"}) {
		t.Fatalf("startup.get %+v, %v", st, err)
	}
	for _, enabled := range []bool{true, false} {
		_, err = callStartup(t, client, "startup.set", map[string]bool{"enabled": enabled})
		if jrpc2.ErrorCode(err) != rpcCodeStartupUnavailable {
			t.Fatalf("startup.set %v: %v", enabled, err)
		}
	}

	direct := &startupControl{entry: unmarkedStartup{mem}}
	if _, err := direct.set(true); !errors.Is(err, errStartupUnavailable) {
		t.Fatalf("set: %v", err)
	}
	if err := direct.toggle(); !errors.Is(err, errStartupUnavailable) {
		t.Fatalf("toggle: %v", err)
	}
	if on, err := direct.enabled(); on || err != nil || direct.available() {
		t.Fatalf("enabled %v, %v; available %v", on, err, direct.available())
	}

	src, tl := newFakeSource(), &testLive{}
	cfg := testConfig(t, tl, "", nil, "--dry-run", "--watch=false")
	cfg.open = func(input.Options) (eventSource, error) { return src, nil }
	cfg.startup = realEntry
	tl.start(t, cfg)
	if tl.startup.available() || !tl.startup.development() {
		t.Fatal("a test build's live run took a real startup entry")
	}
	if !strings.Contains(tl.log.String(), "startup entry refused in a development build") {
		t.Fatal("the refusal is not logged")
	}
	runTrayClick(t, tl, func() bool {
		return strings.Contains(tl.log.String(), "startup entry not changed in a development build")
	})
	if sets := mem.calls(); len(sets) != 0 {
		t.Fatalf("a test build wrote the entry: %v", sets)
	}
}

// TestTrayAndRPCShareStartupEntry: the tray's "登录时启动" and the
// startup RPCs set one entry.
func TestTrayAndRPCShareStartupEntry(t *testing.T) {
	entry := &memoryStartup{own: `"C:\padmate\padmated.exe" --dry-run`}
	src, tl := newFakeSource(), &testLive{}
	cfg := testConfig(t, tl, "", nil, "--dry-run", "--watch=false")
	cfg.open = func(input.Options) (eventSource, error) { return src, nil }
	cfg.startup = func(options) (startupSetting, error) { return entry, nil }
	tl.start(t, cfg)
	client := rpcTestClient(t, tl.liveRun)
	runTrayClick(t, tl, func() bool { return len(entry.calls()) > 0 })
	if sets := entry.calls(); !slices.Equal(sets, []bool{true}) {
		t.Fatalf("tray click wrote %v", sets)
	}
	st, err := callStartup(t, client, "startup.get", nil)
	if err != nil || !st.Enabled || !st.Current || st.Mode != "dry-run" {
		t.Fatalf("after the tray click: %+v, %v", st, err)
	}
	if _, err := callStartup(t, client, "startup.set", map[string]bool{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	if on, _ := tl.startup.enabled(); on {
		t.Fatal("the tray still sees the entry the RPC removed")
	}
}

// runTrayClick drives tl's tray, clicks "登录时启动" once, waits for
// done and quits.
func runTrayClick(t *testing.T, tl *testLive, done func() bool) {
	t.Helper()
	click, quit := make(chan struct{}, 1), make(chan struct{}, 1)
	closed := make(chan struct{})
	var once sync.Once
	ui := &trayUI{startup: click, quit: quit,
		update: func(tray.Presentation, bool, bool) {},
		close:  func() { once.Do(func() { close(closed) }) },
		run:    func(ready, exit func(), _ func(error)) { ready(); <-closed; exit() },
	}
	finished := make(chan int, 1)
	go func() { finished <- tl.driveTray(ui) }()
	click <- struct{}{}
	tl.waitUntil(t, "the tray's click", done)
	quit <- struct{}{}
	select {
	case code := <-finished:
		if code != 0 {
			t.Fatalf("tray exit %d", code)
		}
	case <-time.After(liveWait):
		t.Fatal("tray exit hung")
	}
}

// TestStartupArgsOfUIStartedDaemon: the packaged UI starts padmated with
// --inject and its data directory (ADR-0052); enabling start at sign-in
// from it registers the same.
func TestStartupArgsOfUIStartedDaemon(t *testing.T) {
	dir := t.TempDir()
	var stderr strings.Builder
	opts, _, ok := parseFlags([]string{"--inject", "--data-dir", dir}, &stderr, &stderr)
	if !ok {
		t.Fatal(stderr.String())
	}
	// As live does before startLive.
	if err := resolveLibrary(&opts); err != nil {
		t.Fatal(err)
	}
	args, err := startupArgs(opts)
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(dir)
	if !slices.Equal(args[:3], []string{"--data-dir", abs, "--inject"}) || slices.Contains(args, "--dry-run") || slices.Contains(args, "--profile") {
		t.Fatalf("startup args %q", args)
	}
}
