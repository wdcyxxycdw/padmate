// Package winfocus observes foreground executables without reading window
// titles. Its Windows hooks share the winsession message thread and window.
package winfocus

import (
	"log/slog"
	"strings"
	"time"
)

const Debounce = 50 * time.Millisecond

// App is a value snapshot. Window and Class are internal routing information;
// the public scripting and IPC view exposes only executable identity.
type App struct {
	Window   uintptr `json:"-"`
	Class    string  `json:"-"`
	Exe      string  `json:"exe"`
	Path     string  `json:"path"`
	PID      uint32  `json:"pid"`
	Elevated bool    `json:"elevated"`
}

func (a App) Is(exe string) bool {
	exe = strings.ReplaceAll(exe, "/", `\`)
	if strings.Contains(exe, `\`) {
		return exe != "" && strings.EqualFold(exe, strings.ReplaceAll(a.Path, "/", `\`))
	}
	return exe != "" && strings.EqualFold(exe, a.Exe)
}

func IgnoredClass(class string) bool {
	switch class {
	case "MultitaskingViewFrame", "XamlExplorerHostIslandWindow", "ForegroundStaging", "Shell_TrayWnd", "#32768":
		return true
	}
	return false
}

// Debouncer is driven by monotonic time. Sampling reads the actual current
// foreground, never the possibly stale HWND in a queued WinEvent callback.
type Debouncer struct {
	Current App
	due     time.Duration
	pending bool
}

func (d *Debouncer) Signal(now time.Duration) {
	d.pending, d.due = true, now+Debounce
}

func (d *Debouncer) Ready(now time.Duration) bool { return d.pending && now >= d.due }

func (d *Debouncer) Sample(now time.Duration, app App) (App, bool) {
	if !d.Ready(now) {
		return App{}, false
	}
	d.pending = false
	if app.Path == "" || IgnoredClass(app.Class) || app == d.Current {
		return App{}, false
	}
	d.Current = app
	return app, true
}

type Config struct {
	Logger *slog.Logger
	// OnChange runs on the session thread and must return promptly. Disk
	// work and profile switching belong on a separate worker.
	OnChange func(App)
	// Query optionally supplies foreground snapshots, primarily for tests.
	Query func() (App, error)
}

type Watcher struct {
	cfg       Config
	window    uintptr
	hooks     [2]uintptr
	debouncer Debouncer
	started   time.Time
	closed    bool
}
