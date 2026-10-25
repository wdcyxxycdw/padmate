package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// callExit calls daemon.exit on c with params.
func callExit(c *jrpc2.Client, params any) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var got map[string]any
	err := c.CallResult(ctx, "daemon.exit", params, &got)
	return got, err
}

// stoppedCode returns a channel that gets the exit code once live mode has
// stopped.
func stoppedCode(tl *testLive) <-chan int {
	codes := make(chan int, 1)
	go func() { codes <- tl.wait() }()
	return codes
}

// TestDaemonExitInstallerWaitsForUI: daemon.exit {reason:"installer"} runs
// the tray's exit (DMN-22, PKG-04): it replies at once, lets go of every
// key and pauses the mapping, tells the UIs, waits until the UI has saved
// its last edit and answered ui.exitReady, then takes the exit path.
func TestDaemonExitInstallerWaitsForUI(t *testing.T) {
	tl, src := sessionLive(t, uiTestConfig, nil, "--dry-run", "--watch=false")
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space\n")
	ui := newExitClient(t, tl, true)
	caller := rpcTestClient(t, tl.liveRun)
	stopped := stoppedCode(tl)

	got, err := callExit(caller, map[string]string{"reason": "installer"})
	if err != nil || got["exiting"] != true || got["reason"] != "installer" {
		t.Fatalf("daemon.exit = %v, %v", got, err)
	}
	ui.eventFor(t, "installer")
	if !tl.sup.Status().Paused || tl.loop.Blocking()&output.BlockExit == 0 {
		t.Fatal("daemon.exiting before the pause and the release")
	}
	tl.waitFor(t, tl.stdout, "send -Space\n")
	select {
	case <-stopped:
		t.Fatal("exited before the UI was ready")
	case <-time.After(200 * time.Millisecond):
	}
	if !ui.exitReady(t) {
		t.Fatal("ui.exitReady says not exiting")
	}
	select {
	case code := <-stopped:
		if code != 0 {
			t.Fatalf("exit code %d\n%s", code, tl.stderr)
		}
	case <-time.After(liveWait):
		t.Fatal("daemon.exit did not stop padmated")
	}
	if !strings.Contains(tl.log.String(), `"msg":"UI ready to exit","reason":"installer","ui":1,"ready":1`) {
		t.Fatalf("log:\n%s", tl.log)
	}
	if !strings.Contains(tl.stderr.String(), "padmated: stopping: installer exit") {
		t.Fatalf("stderr:\n%s", tl.stderr)
	}
	checkBalanced(t, tl.stdout.String())
}

// TestDaemonExitStuckUI: a UI that never answers holds the installer's exit
// only until the wait is over, as for the tray's; the keys are released
// all the same.
func TestDaemonExitStuckUI(t *testing.T) {
	const wait = 300 * time.Millisecond
	tl, src := sessionLive(t, func(cfg *liveConfig) { uiTestConfig(cfg); cfg.exitReadyWait = wait }, nil, "--dry-run", "--watch=false")
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space\n")
	ui := newExitClient(t, tl, true)
	caller := rpcTestClient(t, tl.liveRun)
	stopped := stoppedCode(tl)
	start := time.Now()
	if _, err := callExit(caller, map[string]string{"reason": "installer"}); err != nil {
		t.Fatal(err)
	}
	ui.eventFor(t, "installer")
	select {
	case code := <-stopped:
		if code != 0 {
			t.Fatalf("exit code %d", code)
		}
	case <-time.After(liveWait):
		t.Fatal("daemon.exit did not stop padmated")
	}
	if d := time.Since(start); d < wait {
		t.Fatalf("exited after %v, before the wait of %v was over", d, wait)
	}
	if !strings.Contains(tl.log.String(), `"msg":"UI did not confirm the exit in time","reason":"installer"`) || !strings.Contains(tl.stdout.String(), "send -Space\n") {
		t.Fatalf("stdout:\n%s\nlog:\n%s", tl.stdout, tl.log)
	}
	checkBalanced(t, tl.stdout.String())
}

// TestDaemonExitWithoutUI: without a UI, or with clients that are not one,
// the installer's exit waits for nobody.
func TestDaemonExitWithoutUI(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra func(*testing.T, *testLive)
	}{
		{"no client", nil},
		{"not a UI", func(t *testing.T, tl *testLive) {
			c := newExitClient(t, tl, false)
			go func() { <-c.exiting }()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tl, src := sessionLive(t, uiTestConfig, nil, "--dry-run", "--watch=false")
			button(src, "east", true)
			tl.waitFor(t, tl.stdout, "send +Space\n")
			if tc.extra != nil {
				tc.extra(t, tl)
			}
			caller := rpcTestClient(t, tl.liveRun)
			stopped := stoppedCode(tl)
			start := time.Now()
			if _, err := callExit(caller, map[string]string{"reason": "installer"}); err != nil {
				t.Fatal(err)
			}
			select {
			case code := <-stopped:
				if code != 0 {
					t.Fatalf("exit code %d", code)
				}
			case <-time.After(liveWait):
				t.Fatal("daemon.exit did not stop padmated")
			}
			if d := time.Since(start); d >= exitReadyWait {
				t.Fatalf("waited %v", d)
			}
			checkBalanced(t, tl.stdout.String())
		})
	}
}

// TestDaemonExitRejectsOtherReasons: only "installer" is accepted; anything
// else changes nothing.
func TestDaemonExitRejectsOtherReasons(t *testing.T) {
	tl, src := sessionLive(t, uiTestConfig, nil, "--dry-run", "--watch=false")
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space\n")
	caller := rpcTestClient(t, tl.liveRun)
	for _, params := range []any{
		nil,
		map[string]string{},
		map[string]string{"reason": ""},
		map[string]string{"reason": "tray"},
		map[string]string{"reason": "Installer"},
		map[string]any{"reason": 5},
		map[string]any{"reason": "installer", "force": true},
	} {
		_, err := callExit(caller, params)
		if e, ok := errors.AsType[*jrpc2.Error](err); !ok || e.Code != jrpc2.InvalidParams {
			t.Fatalf("daemon.exit %v: %v, want invalid params", params, err)
		}
	}
	tl.sync()
	if tl.sup.Status().Paused || tl.loop.Blocking()&output.BlockExit != 0 || tl.end.Err() != nil {
		t.Fatal("a refused daemon.exit changed the daemon")
	}
	if strings.Contains(tl.stdout.String(), "send -Space\n") {
		t.Fatalf("a refused daemon.exit released a key:\n%s", tl.stdout)
	}
}

// TestDaemonExitSharesTheSequence: a second daemon.exit, and the tray's 退出
// while the installer's exit waits for the UI, begin nothing new: the UI is
// told once, and the tray's exit ends together with the first.
func TestDaemonExitSharesTheSequence(t *testing.T) {
	tl, src := sessionLive(t, uiTestConfig, nil, "--dry-run", "--watch=false")
	button(src, "east", true)
	tl.waitFor(t, tl.stdout, "send +Space\n")
	ui := newExitClient(t, tl, true)
	caller := rpcTestClient(t, tl.liveRun)
	if _, err := callExit(caller, map[string]string{"reason": "installer"}); err != nil {
		t.Fatal(err)
	}
	ui.eventFor(t, "installer")
	if got, err := callExit(caller, map[string]string{"reason": "installer"}); err != nil || got["exiting"] != true {
		t.Fatalf("second daemon.exit = %v, %v", got, err)
	}
	quit, finished := quitFromTray(tl)
	quit <- struct{}{}
	select {
	case <-finished:
		t.Fatal("the tray's exit did not wait for the installer's")
	case <-time.After(300 * time.Millisecond):
	}
	if !ui.exitReady(t) {
		t.Fatal("ui.exitReady says not exiting")
	}
	exited(t, finished, liveWait)
	select {
	case e := <-ui.exiting:
		t.Fatalf("daemon.exiting announced again: %s", e)
	default:
	}
	if n := strings.Count(tl.log.String(), `"msg":"exit announced"`); n != 1 {
		t.Fatalf("announced %d times\n%s", n, tl.log)
	}
	checkBalanced(t, tl.stdout.String())
}
