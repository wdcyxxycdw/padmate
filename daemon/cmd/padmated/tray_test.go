package main

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/tray"
)

func TestTrayPauseResumeAndOrderlyQuit(t *testing.T) {
	// The production live runtime sends only to the dry-run printer.
	tl := startTestLive(t, nil, "--dry-run", "--watch=false")
	v := tl.attach(t)
	press(t, v, "east", true)
	tl.waitFor(t, tl.stdout, "+Space")
	pause, quit := make(chan struct{}, 1), make(chan struct{}, 1)
	closed := make(chan struct{})
	var once sync.Once
	ui := &trayUI{pause: pause, quit: quit, update: func(tray.Presentation, bool, bool) {},
		close: func() { once.Do(func() { close(closed) }) },
		run:   func(ready, exit func(), _ func(error)) { ready(); <-closed; exit() },
	}
	finished := make(chan int, 1)
	go func() { finished <- tl.driveTray(ui) }()
	pause <- struct{}{}
	deadline := time.Now().Add(liveWait)
	for !tl.sup.Status().Paused && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !tl.sup.Status().Paused {
		t.Fatal("tray did not pause")
	}
	tl.waitFor(t, tl.stdout, "-Space")
	before := tl.stdout.String()
	pause <- struct{}{}
	for tl.sup.Status().Paused && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if tl.sup.Status().Paused {
		t.Fatal("tray did not resume")
	}
	ctx, cancel := context.WithTimeout(context.Background(), liveWait)
	defer cancel()
	if err := tl.sup.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Count(tl.stdout.String(), "+Space") != strings.Count(before, "+Space") {
		t.Fatal("resume replayed held control")
	}
	press(t, v, "east", false)
	press(t, v, "east", true)
	tl.waitForCount(t, tl.stdout, "+Space", 2)
	quit <- struct{}{}
	select {
	case code := <-finished:
		if code != 0 {
			t.Fatalf("tray exit %d", code)
		}
	case <-time.After(liveWait):
		t.Fatal("tray exit hung")
	}
	if strings.Count(tl.stdout.String(), "-Space") != 2 || !strings.Contains(tl.log.String(), "padmated stopped") {
		t.Fatalf("tray quit before release/shutdown: %s", tl.stdout.String())
	}
}

func TestStartupArgsPreserveModeAndPaths(t *testing.T) {
	opts := options{profile: "profile.json", dryRun: true, watch: false, device: "My Pad", logFile: "logs/debug.jsonl", tray: true}
	args, err := startupArgs(opts)
	if err != nil {
		t.Fatal(err)
	}
	wantProfile, _ := filepath.Abs("profile.json")
	wantLog, _ := filepath.Abs("logs/debug.jsonl")
	want := []string{"--profile", wantProfile, "--dry-run", "--device", "My Pad", "--watch=false", "--log-level", "info", "--log-file", wantLog}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("startup args=%q want %q", args, want)
	}
	opts.inject, opts.dryRun = true, false
	args, err = startupArgs(opts)
	if err != nil || args[2] != "--inject" {
		t.Fatalf("lost inject mode: %q %v", args, err)
	}
}
