package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/tray"
)

func TestWindowExecutable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows paths")
	}
	for _, c := range []struct{ daemon, want string }{
		{`C:\Users\u\AppData\Local\Programs\padmate\resources\bin\padmated.exe`, `C:\Users\u\AppData\Local\Programs\padmate\padmate.exe`},
		{`D:\Apps\My padmate\Resources\BIN\padmated.exe`, `D:\Apps\My padmate\padmate.exe`},
		{`D:/Apps/padmate/resources/bin/padmated.exe`, `D:\Apps\padmate\padmate.exe`},
		// Not an installation: a checkout's bin/, the development UI's, a
		// portable copy, a folder that only ends like one.
		{`D:\Projects\padmate\bin\padmated.exe`, ""},
		{`D:\Projects\padmate\.cache\ui-bin\padmated.exe`, ""},
		{`D:\padmate-preview\padmated.exe`, ""},
		{`D:\x\myresources\bin\padmated.exe`, ""},
		{`padmated.exe`, ""},
	} {
		got, err := windowExecutable(c.daemon)
		if c.want == "" {
			if !errors.Is(err, errNotInstalled) {
				t.Errorf("windowExecutable(%q) = %q, %v; want errNotInstalled", c.daemon, got, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("windowExecutable(%q) = %q, %v; want %q", c.daemon, got, err, c.want)
		}
	}
}

// installation makes the files of an installation below a temporary folder
// and returns padmated.exe and padmate.exe in it.
func installation(t *testing.T, withWindow bool) (daemon, window string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "padmate")
	daemon = filepath.Join(root, "resources", "bin", "padmated.exe")
	window = filepath.Join(root, "padmate.exe")
	if err := os.MkdirAll(filepath.Dir(daemon), 0o755); err != nil {
		t.Fatal(err)
	}
	files := []string{daemon}
	if withWindow {
		files = append(files, window)
	}
	for _, f := range files {
		if err := os.WriteFile(f, []byte("MZ"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return daemon, window
}

func TestWindowOpener(t *testing.T) {
	daemon, window := installation(t, true)
	var started []string
	start := func(exe string) error { started = append(started, exe); return nil }
	executable := func() (string, error) { return daemon, nil }

	// A development or test build starts nothing, not even to look.
	asked := false
	dev := windowOpener{dev: true, executable: func() (string, error) { asked = true; return daemon, nil }, start: start}
	if _, err := dev.open(); !errors.Is(err, errDevelopmentBuild) || asked || len(started) != 0 {
		t.Fatalf("a development build: %v, asked %v, started %q", err, asked, started)
	}

	got, err := windowOpener{executable: executable, start: start}.open()
	if err != nil || !strings.EqualFold(got, window) || len(started) != 1 || !strings.EqualFold(started[0], window) {
		t.Fatalf("open = %q, %v; started %q, want %s", got, err, started, window)
	}

	// No padmate.exe next to the resources folder: nothing is started.
	bare, _ := installation(t, false)
	started = nil
	if _, err := (windowOpener{executable: func() (string, error) { return bare, nil }, start: start}).open(); err == nil || len(started) != 0 {
		t.Errorf("an installation without its window: %v, started %q", err, started)
	}
	// A padmated outside an installation.
	if _, err := (windowOpener{executable: func() (string, error) { return `D:\Projects\padmate\bin\padmated.exe`, nil }, start: start}).open(); !errors.Is(err, errNotInstalled) || len(started) != 0 {
		t.Errorf("outside an installation: %v, started %q", err, started)
	}
	// The start fails.
	failed := errors.New("access denied")
	if _, err := (windowOpener{executable: executable, start: func(string) error { return failed }}).open(); !errors.Is(err, failed) {
		t.Errorf("a failed start: %v", err)
	}
}

// The tests run as a test build (TestMain gives padmated an instance name of
// its own), so the opener padmated uses starts no padmate.exe from them: it
// would be the real application.
func TestSystemWindowOpenerOpensNothingInATestBuild(t *testing.T) {
	o := systemWindowOpener()
	if !o.dev {
		t.Fatal("the opener of a test build is not a development one")
	}
	if _, err := o.open(); !errors.Is(err, errDevelopmentBuild) {
		t.Fatalf("open = %v, want errDevelopmentBuild", err)
	}
}

// A double click on the tray icon opens the window, once per double click,
// and changes nothing else.
func TestTrayDoubleClickOpensTheWindow(t *testing.T) {
	var opens atomic.Int32
	src, tl := newFakeSource(), &testLive{}
	cfg := testConfig(t, tl, "", nil, "--dry-run", "--watch=false")
	cfg.open = func(input.Options) (eventSource, error) { return src, nil }
	cfg.openWindow = func() (string, error) { opens.Add(1); return `C:\padmate\padmate.exe`, nil }
	tl.start(t, cfg)

	open, quit := make(chan struct{}, 1), make(chan struct{}, 1)
	closed := make(chan struct{})
	var once sync.Once
	ui := &trayUI{open: open, quit: quit,
		update: func(tray.Presentation, bool, bool) {},
		close:  func() { once.Do(func() { close(closed) }) },
		run:    func(ready, exit func(), _ func(error)) { ready(); <-closed; exit() },
	}
	finished := make(chan int, 1)
	go func() { finished <- tl.driveTray(ui) }()
	open <- struct{}{}
	tl.waitUntil(t, "the first double click", func() bool { return opens.Load() == 1 })
	open <- struct{}{}
	tl.waitUntil(t, "the second double click", func() bool { return opens.Load() == 2 })
	if tl.sup.Status().Paused {
		t.Error("opening the window paused the mapping")
	}
	quit <- struct{}{}
	select {
	case code := <-finished:
		if code != 0 {
			t.Fatalf("tray exit %d", code)
		}
	case <-time.After(liveWait):
		t.Fatal("tray exit hung")
	}
	if n := opens.Load(); n != 2 {
		t.Errorf("opened %d times, want 2", n)
	}
}
