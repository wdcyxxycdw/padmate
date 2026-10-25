package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
	"github.com/wdcyxxycdw/padmate/daemon/internal/tray"
)

// uiTestConfig lets ui.register verify any process, as the UI's bridge.
func uiTestConfig(cfg *liveConfig) {
	cfg.verifyUI = func(uint32, uint32) (string, error) { return "verified-electron-image", nil }
	cfg.ownInstance = func(uint32) (uint64, error) { return 7, nil }
	cfg.ownAlive = func(uint32, string, uint64) bool { return true }
	cfg.ownForeground = func(uint32, string) bool { return false }
}

// exitClient is an IPC client as the UI's bridge is: subscribed to
// daemon.exiting, and registered as a UI when register is set.
type exitClient struct {
	client  *jrpc2.Client
	exiting chan string
}

func newExitClient(t *testing.T, tl *testLive, register bool) *exitClient {
	t.Helper()
	c := &exitClient{exiting: make(chan string, 4)}
	c.client = rpcTestClient(t, tl.liveRun, &jrpc2.ClientOptions{OnNotify: func(req *jrpc2.Request) {
		if req.Method() == "daemon.exiting" {
			c.exiting <- req.ParamString()
		}
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.client.CallResult(ctx, "subscribe", map[string]any{"topics": []string{"daemon.exiting", "profile.changed"}}, new(any)); err != nil {
		t.Fatal(err)
	}
	if register {
		if err := c.client.CallResult(ctx, "ui.register", map[string]uint32{"pid": 42}, new(any)); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// event waits for daemon.exiting of the tray's 退出.
func (c *exitClient) event(t *testing.T) { c.eventFor(t, "tray") }

// eventFor waits for daemon.exiting with reason.
func (c *exitClient) eventFor(t *testing.T, reason string) {
	t.Helper()
	select {
	case e := <-c.exiting:
		if e != `{"reason":"`+reason+`"}` {
			t.Fatalf("daemon.exiting %s, want reason %s", e, reason)
		}
	case <-time.After(liveWait):
		t.Fatal("no daemon.exiting")
	}
}

// exitReady calls ui.exitReady and returns its exiting.
func (c *exitClient) exitReady(t *testing.T) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var got map[string]bool
	if err := c.client.CallResult(ctx, "ui.exitReady", nil, &got); err != nil {
		t.Fatal(err)
	}
	return got["exiting"]
}

// quitFromTray chooses 退出 in a tray driven by the test and returns when
// driveTray does, with its exit code.
func quitFromTray(tl *testLive) (quit chan<- struct{}, finished <-chan int) {
	q, done := make(chan struct{}, 1), make(chan int, 1)
	closed := make(chan struct{})
	var once sync.Once
	ui := &trayUI{quit: q, update: func(tray.Presentation, bool, bool) {},
		close: func() { once.Do(func() { close(closed) }) },
		run:   func(ready, exit func(), _ func(error)) { ready(); <-closed; exit() },
	}
	go func() { done <- tl.driveTray(ui) }()
	return q, done
}

func exited(t *testing.T, finished <-chan int, within time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	select {
	case code := <-finished:
		if code != 0 {
			t.Fatalf("tray exit %d", code)
		}
	case <-time.After(within):
		t.Fatalf("tray exit took more than %v", within)
	}
	return time.Since(start)
}

// TestTrayExitWaitsForUI: 退出 in the tray lets go of every key and
// pauses the mapping at once, then tells the UI (DMN-22, the owner's G9).
// padmated keeps serving while the UI saves its last edit, and exits once
// the UI answers ui.exitReady.
func TestTrayExitWaitsForUI(t *testing.T) {
	dir := t.TempDir()
	const paint = `{"schemaVersion":0,"name":"paint","layers":{"base":{"east":{"press":{"type":"keys","keys":"Space"}}}}}`
	writeLibraryProfile(t, dir, "paint", paint)
	writeLastProfile(t, dir, "paint")
	tl, src := startLibraryLive(t, `{"schemaVersion":0,"enabled":false}`, options{dataDir: dir, dryRun: true}, uiTestConfig)
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space\n")
	ui := newExitClient(t, tl, true)
	if ui.exitReady(t) {
		t.Fatal("ui.exitReady says exiting before the tray's exit")
	}
	quit, finished := quitFromTray(tl)
	quit <- struct{}{}
	ui.event(t)
	if !tl.sup.Status().Paused || tl.loop.Blocking()&output.BlockExit == 0 {
		t.Fatal("daemon.exiting before the pause and the release")
	}
	tl.waitFor(t, tl.stdout, "send -Space\n")
	// The UI saves its last edit, as when its window closes.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	edited := strings.Replace(paint, `"Space"`, `"F14"`, 1)
	var saved rpcSaved
	if err := ui.client.CallResult(ctx, "profiles.put", map[string]string{"id": "paint", "revision": profilelib.Revision([]byte(paint)), "text": edited}, &saved); err != nil || !saved.Applied {
		t.Fatal(saved, err)
	}
	// Pressed while padmated waits: nothing.
	button(src, "east", false)
	button(src, "east", true)
	if err := tl.sup.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
		t.Fatal("exited before the UI was ready")
	case <-time.After(200 * time.Millisecond):
	}
	if !ui.exitReady(t) {
		t.Fatal("ui.exitReady says not exiting")
	}
	exited(t, finished, time.Second)
	if data, err := profilelib.New(dir).Read("paint"); err != nil || string(data) != edited {
		t.Fatalf("the last edit was lost: %q %v", data, err)
	}
	if !strings.Contains(tl.log.String(), `"msg":"UI ready to exit","reason":"tray","ui":1,"ready":1`) {
		t.Fatalf("log:\n%s", tl.log)
	}
	if strings.Count(tl.stdout.String(), "send +") != 1 {
		t.Fatalf("stdout:\n%s", tl.stdout)
	}
	checkBalanced(t, tl.stdout.String())
}

// TestTrayExitStuckUI: a UI that never answers holds the exit only until
// the wait is over (2 s, here shortened); the keys are released all the
// same.
func TestTrayExitStuckUI(t *testing.T) {
	if exitReadyWait != 2*time.Second {
		t.Fatalf("exitReadyWait %v, want the owner's 2 s", exitReadyWait)
	}
	const wait = 300 * time.Millisecond
	tl, src := sessionLive(t, func(cfg *liveConfig) { uiTestConfig(cfg); cfg.exitReadyWait = wait }, nil, "--dry-run", "--watch=false")
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space\n")
	ui := newExitClient(t, tl, true)
	quit, finished := quitFromTray(tl)
	start := time.Now()
	quit <- struct{}{}
	ui.event(t)
	exited(t, finished, liveWait)
	if d := time.Since(start); d < wait {
		t.Fatalf("exited after %v, before the wait of %v was over", d, wait)
	}
	if !strings.Contains(tl.log.String(), `"msg":"UI did not confirm the exit in time"`) || !strings.Contains(tl.stdout.String(), "send -Space\n") {
		t.Fatalf("stdout:\n%s\nlog:\n%s", tl.stdout, tl.log)
	}
	checkBalanced(t, tl.stdout.String())
}

// TestTrayExitWithoutUI: without a UI, or with clients that are not one
// (padmatectl events subscribes to daemon.exiting too), or a UI that
// closes its connection instead of answering, the exit waits for nobody.
func TestTrayExitWithoutUI(t *testing.T) {
	for _, tc := range []struct {
		name string
		ui   func(*testing.T, *testLive)
	}{
		{"no client", nil},
		{"not a UI", func(t *testing.T, tl *testLive) {
			c := newExitClient(t, tl, false)
			go func() { <-c.exiting }()
		}},
		{"UI closes", func(t *testing.T, tl *testLive) {
			c := newExitClient(t, tl, true)
			go func() { <-c.exiting; _ = c.client.Close() }()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tl, src := sessionLive(t, uiTestConfig, nil, "--dry-run", "--watch=false")
			button(src, "east", true)
			tl.waitFor(t, tl.stdout, "send +Space\n")
			if tc.ui != nil {
				tc.ui(t, tl)
			}
			quit, finished := quitFromTray(tl)
			quit <- struct{}{}
			if d := exited(t, finished, liveWait); d >= exitReadyWait {
				t.Fatalf("waited %v", d)
			}
			checkBalanced(t, tl.stdout.String())
		})
	}
}

// TestExitReadyNeedsUI: only a registered UI may answer.
func TestExitReadyNeedsUI(t *testing.T) {
	tl, _ := sessionLive(t, uiTestConfig, nil, "--dry-run", "--watch=false")
	c := newExitClient(t, tl, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.client.CallResult(ctx, "ui.exitReady", nil, new(any))
	if e, ok := errors.AsType[*jrpc2.Error](err); !ok || e.Code != -32001 {
		t.Fatal(err)
	}
}
