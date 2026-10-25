//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"fyne.io/systray"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/tray"
)

// These review reproductions use the production runtime with a fake event
// source and session watcher. They never start SDL, inject or write HKCU.
func startReviewTrayLive(t *testing.T) *testLive {
	t.Helper()
	tl := &testLive{}
	cfg := testConfig(t, tl, "", nil, "--dry-run", "--watch=false")
	src := newFakeSource()
	cfg.open = func(input.Options) (eventSource, error) { return src, nil }
	tl.start(t, cfg)
	return tl
}

func TestReviewTrayInitTimeoutFailureCode(t *testing.T) {
	tl := startReviewTrayLive(t)
	closed := make(chan struct{})
	var once sync.Once
	ui := &trayUI{
		run:    func(_, _ func(), _ func(error)) { <-closed },
		close:  func() { once.Do(func() { close(closed) }) },
		update: func(tray.Presentation, bool, bool) {},
	}
	if code := tl.driveTray(ui); code != 1 {
		t.Fatalf("native initialization timeout returned %d, want failure; stderr: %s", code, tl.stderr.String())
	}
}

func TestReviewTrayControlPanicFailureCode(t *testing.T) {
	tl := startReviewTrayLive(t)
	closed := make(chan struct{})
	var once sync.Once
	ui := &trayUI{
		run:    func(ready, _ func(), _ func(error)) { ready(); <-closed },
		close:  func() { once.Do(func() { close(closed) }) },
		update: func(tray.Presentation, bool, bool) { panic("renderer failed") },
	}
	if code := tl.driveTray(ui); code != 1 {
		t.Fatalf("renderer panic returned %d, want failure; stderr: %s", code, tl.stderr.String())
	}
}

func TestReviewTrayUnexpectedLoopReturnFailureCode(t *testing.T) {
	tl := startReviewTrayLive(t)
	ui := &trayUI{
		// A native GetMessage failure returns without its onExit callback.
		run:    func(ready, _ func(), _ func(error)) { ready() },
		close:  func() {},
		update: func(tray.Presentation, bool, bool) {},
	}
	if code := tl.driveTray(ui); code != 1 {
		t.Fatalf("unexpected native-loop return gave %d, want failure; stderr: %s", code, tl.stderr.String())
	}
}

func TestReviewTrayQuitBeforeRegistration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReviewTrayQuitBeforeRegistrationChild$")
	cmd.Env = append(os.Environ(), "PADMATE_TRAY_REVIEW_CHILD=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("shutdown before native Register crashed: %v\n%s", err, out)
	}
}

func TestReviewTrayQuitBeforeRegistrationChild(t *testing.T) {
	if os.Getenv("PADMATE_TRAY_REVIEW_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	endMainLoop := tray.MainLoopExit()
	tl := startReviewTrayLive(t)
	tl.cancel()
	closed := make(chan struct{})
	initialized := make(chan struct{})
	ui := &trayUI{
		// Force shutdown to post WM_QUIT before native registration. The
		// production close path must not call systray.Quit on this worker.
		run: func(ready, exit func(), _ func(error)) {
			<-closed
			systray.Run(func() { ready(); close(initialized) }, exit)
			<-initialized
		},
		close: func() {
			if err := endMainLoop(); err != nil {
				panic(err)
			}
			close(closed)
		},
		update: func(tray.Presentation, bool, bool) {},
	}
	if code := tl.driveTray(ui); code != 0 {
		t.Fatalf("early orderly shutdown returned %d: %s", code, tl.stderr.String())
	}
	// Registration and controller teardown have both finished on this thread.
	systray.Quit()
}

func TestTrayFailureCallbacksStillCleanUp(t *testing.T) {
	for _, kind := range []string{"initialize", "loop", "close"} {
		t.Run(kind, func(t *testing.T) {
			tl := startReviewTrayLive(t)
			closed := make(chan struct{})
			ui := &trayUI{
				run: func(ready, _ func(), fail func(error)) {
					if kind == "loop" {
						panic("native loop")
					}
					if kind == "initialize" {
						fail(errors.New("initialize"))
					} else {
						ready()
						tl.cancel()
					}
					<-closed
				},
				close: func() {
					close(closed)
					if kind == "close" {
						panic("close")
					}
				},
				update: func(tray.Presentation, bool, bool) {},
			}
			if code := tl.driveTray(ui); code != 1 {
				t.Fatalf("%s failure returned %d", kind, code)
			}
			if !strings.Contains(tl.log.String(), "padmated stopped") {
				t.Fatalf("failed tray returned before cleanup: %s", tl.log.String())
			}
		})
	}
}
