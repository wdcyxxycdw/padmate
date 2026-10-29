package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// errNotInstalled is why a padmated that is not in an installation opens no
// window: a portable copy or the bin/ folder of a checkout has no padmate.exe
// of its own.
var errNotInstalled = errors.New("padmated is not in an installation")

// windowExecutable is the window of the padmated at daemon: padmate.exe of
// its installation, two folders above resources\bin\padmated.exe (the layout
// of installerpkg and ownwindow.TrustedPaths). Case does not matter, as on
// Windows.
func windowExecutable(daemon string) (string, error) {
	bin := filepath.Dir(daemon)
	resources := filepath.Dir(bin)
	if !strings.EqualFold(filepath.Base(bin), "bin") || !strings.EqualFold(filepath.Base(resources), "resources") {
		return "", errNotInstalled
	}
	return filepath.Join(filepath.Dir(resources), "padmate.exe"), nil
}

// windowOpener opens padmate's window when the tray icon is double-clicked:
// it starts the installation's padmate.exe, which shows the window, or, when
// the window runs already, brings it to the front and quits (its
// single-instance lock, app/src/main/single-instance.ts). A development or
// test build opens nothing: its window is the development UI, and the
// installation's padmate.exe is the real application, which sends real keys
// (CLAUDE.md).
type windowOpener struct {
	dev        bool
	executable func() (string, error)
	start      func(string) error
}

// systemWindowOpener is the opener of this padmated: os.Executable and
// startWindow, and none in a development or test build.
func systemWindowOpener() windowOpener {
	return windowOpener{dev: developmentBuild(), executable: os.Executable, start: startWindow}
}

// open starts the window and returns its path.
func (o windowOpener) open() (string, error) {
	if o.dev {
		return "", errDevelopmentBuild
	}
	daemon, err := o.executable()
	if err != nil {
		return "", err
	}
	exe, err := windowExecutable(daemon)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(exe); err != nil || !info.Mode().IsRegular() {
		return exe, fmt.Errorf("no window to open: %s is not there", exe)
	}
	return exe, o.start(exe)
}

// openWindow opens the window for a double click on the tray icon and logs
// what came of it. Nothing it does holds a key.
func (r *liveRun) openWindow() {
	open := r.cfg.openWindow
	if open == nil {
		open = systemWindowOpener().open
	}
	switch exe, err := open(); {
	case err == nil:
		r.log.Info("tray opened the window", "exe", exe)
	case errors.Is(err, errDevelopmentBuild):
		r.log.Info("tray opens no window in a development build")
	default:
		r.log.Warn("tray could not open the window", "err", err)
	}
}
