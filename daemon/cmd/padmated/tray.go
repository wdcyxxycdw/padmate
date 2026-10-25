package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/tray"
)

// startupSetting is a login startup entry: an *autostart.Entry, or a
// test's fake (a memoryEntry, the only kind a test build uses).
type startupSetting interface {
	Enabled() (bool, error)
	// Current reports whether the entry is the command Set(true) writes.
	Current() (bool, error)
	Set(bool) error
}

// startupDevTitle is the title of the disabled "登录时启动" item of a
// development or test build.
const startupDevTitle = "登录时启动（开发版不可用）"

// developmentBuild reports whether padmated runs with an instance name or
// IPC pipe of its own: the UI's development daemon, whose linker flags set
// them, or a test, whose TestMain does.
func developmentBuild() bool { return devBuild(instanceName, ipcPipe) }

func devBuild(instance, pipe string) bool { return instance != "" || pipe != "" }

// newStartup returns the startup entry the tray and the startup RPCs set,
// from executable and create (autostart.New), or nil and why there is
// none. A development or test build gets none (errDevelopmentBuild) and
// calls neither: its entry would replace the user's real one with the
// development build (UI-19, ADR-0051, ADR-0052).
func newStartup(opts options, executable func() (string, error), create func(string, []string) (startupSetting, error)) (startupSetting, error) {
	if developmentBuild() {
		return nil, errDevelopmentBuild
	}
	exe, err := executable()
	if err != nil {
		return nil, err
	}
	args, err := startupArgs(opts)
	if err != nil {
		return nil, err
	}
	return create(exe, args)
}

// Native UI functions are injected at the desktop entry point. Tests use the
// same controller and live runtime with menu channels and a fake renderer.
type trayUI struct {
	run         func(ready, exit func(), failed func(error))
	close       func()
	update      func(tray.Presentation, bool, bool)
	notify      func(tray.Notification) error
	pause, quit <-chan struct{}
	startup     <-chan struct{}
}

func (r *liveRun) driveTray(ui *trayUI) (code int) {
	ready, done := make(chan struct{}), make(chan struct{})
	result := make(chan int, 1)
	controlDone := make(chan struct{})
	var failed atomic.Bool
	fail := func(where string, v any) {
		failed.Store(true)
		fmt.Fprintln(r.cfg.stderr, "padmated: tray "+where+":", v)
		r.endStop(stopCause("tray"))
	}
	closeUI := func() {
		defer func() {
			if v := recover(); v != nil {
				fail("close", v)
			}
		}()
		ui.close()
	}
	go func() {
		// stop already defers shutdown while a panic unwinds. Keep a UI
		// worker panic from terminating the process before that cleanup.
		code := 1
		defer func() {
			if v := recover(); v != nil {
				fail("shutdown", v)
			}
			close(done)
			// No renderer may still be using native menu items at teardown.
			<-controlDone
			closeUI()
			result <- code
		}()
		code = r.wait()
	}()
	defer func() {
		if v := recover(); v != nil {
			fail("loop", v)
		}
		r.endStop(stopCause("tray"))
		code = <-result
		if failed.Load() {
			code = 1
		}
	}()
	go func() {
		defer close(controlDone)
		defer func() {
			if v := recover(); v != nil {
				fail("control", v)
			}
			r.endStop(stopCause("tray"))
		}()
		select {
		case <-ready:
		case <-done:
			return
		case <-time.After(3 * time.Second):
			fail("initialization", "did not initialize within 3 seconds")
			return
		}
		r.serveTray(ui, done)
	}()
	// Run stays on main's locked OS thread. Every UI exit requests the
	// existing orderly path; only after it has released keys may main exit.
	ui.run(func() { close(ready) }, func() { r.endStop(stopCause("tray")) }, func(err error) { fail("initialization", err) })
	if r.end.Err() == nil {
		fail("loop", "exited before shutdown was requested")
	}
	return 0
}

func (r *liveRun) serveTray(ui *trayUI, done <-chan struct{}) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	var nextNotice time.Time
	refresh := func() {
		ctx, cancel := context.WithTimeout(r.end, 200*time.Millisecond)
		defer cancel()
		views, err := r.sup.Inspect(ctx)
		if err != nil {
			return
		}
		enabled, err := r.startup.enabled()
		if err != nil {
			r.log.Warn("cannot read startup entry", "err", err)
		}
		st := r.sup.Status()
		ui.update(tray.PresentWithPower(st, views, r.devicePowers(ctx)), st.Paused, enabled)
		r.showNotice(ui, time.Now(), &nextNotice)
	}
	refresh()
	for {
		select {
		case <-done:
			return
		case <-r.end.Done():
			return
		case <-ui.quit:
			r.exitFromTray()
			return
		case <-ui.pause:
			ctx, cancel := context.WithTimeout(r.end, time.Second)
			err := r.sup.Pause(ctx, !r.sup.Status().Paused)
			cancel()
			if err != nil {
				r.log.Warn("tray pause failed", "err", err)
			}
			refresh()
		case <-ui.startup:
			// The menu item is disabled without an entry; a click that comes
			// anyway writes nothing.
			switch err := r.startup.toggle(); {
			case err == nil:
			case r.startup.development():
				r.log.Warn("startup entry not changed in a development build")
			default:
				r.log.Error("startup entry not changed", "err", err)
			}
			refresh()
		case <-tick.C:
			refresh()
		}
	}
}

func startupArgs(opts options) ([]string, error) {
	profilePath, err := filepath.Abs(opts.profile)
	if err != nil {
		return nil, err
	}
	mode := "--dry-run"
	if opts.inject {
		mode = "--inject"
	}
	args := []string{"--profile", profilePath, mode}
	if opts.profileID != "" {
		if err := resolveDataDir(&opts); err != nil {
			return nil, err
		}
		args = []string{"--profile-id", opts.profileID, "--data-dir", opts.dataDir, mode}
	}
	if opts.autoProfile {
		args = []string{"--data-dir", opts.dataDir, mode}
	}
	if opts.library != nil && !opts.autoProfile && opts.profileID == "" {
		args = append(args, "--data-dir", opts.dataDir)
	}
	if opts.decisions {
		args = append(args, "--decisions")
	}
	if opts.fakeInput == "ipc" {
		args = append(args, "--fake-input=ipc")
	}
	if !opts.tray {
		args = append(args, "--tray=false")
	}
	if opts.device != "" {
		args = append(args, "--device", opts.device)
	}
	if opts.sdl.Preset != "" && opts.sdl.Preset != "default" {
		args = append(args, "--preset", opts.sdl.Preset)
	}
	for _, h := range opts.sdl.Extra {
		args = append(args, "--hint", h.Name+"="+h.Value)
	}
	if !opts.watch {
		args = append(args, "--watch=false")
	}
	args = append(args, "--log-level", strings.ToLower(opts.logLevel.String()))
	if opts.logFile != "" && opts.logFile != "-" {
		path, err := filepath.Abs(opts.logFile)
		if err != nil {
			return nil, err
		}
		args = append(args, "--log-file", path)
	}
	return args, nil
}
