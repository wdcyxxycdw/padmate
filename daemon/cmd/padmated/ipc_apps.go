package main

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/winfocus"
)

// maxRecentApps bounds the apps.recent list of the window capture helper
// (PRO-16).
const maxRecentApps = 12

// foregroundTopic carries every accepted foreground executable to the UI.
const foregroundTopic = "app.foreground"

// recentApp is the IPC view of a foreground executable: identity only, never
// a window title, handle, class or PID.
type recentApp struct {
	Exe      string `json:"exe"`
	Path     string `json:"path"`
	Elevated bool   `json:"elevated"`
}

// foregroundEvent is the app.foreground payload. Own marks a registered
// padmate UI, which a capture helper skips instead of offering it as a rule.
// System marks a program of Windows itself (see isSystemPath), which the UI
// does not list among the recent apps; it is left out when false.
type foregroundEvent struct {
	recentApp
	Own    bool `json:"own"`
	System bool `json:"system,omitempty"`
}

// recentApps remembers distinct non-padmate foreground executables, newest
// first. It lives in memory only; a restart forgets it.
type recentApps struct {
	mu   sync.Mutex
	apps []recentApp
}

// note moves app to the front, replacing an older entry with the same path
// (compared case-insensitively, like Windows paths), and keeps at most
// maxRecentApps entries.
func (s *recentApps) note(app recentApp) {
	key := strings.ToLower(app.Path)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apps = slices.DeleteFunc(s.apps, func(a recentApp) bool { return strings.ToLower(a.Path) == key })
	s.apps = slices.Insert(s.apps, 0, app)
	if len(s.apps) > maxRecentApps {
		clear(s.apps[maxRecentApps:])
		s.apps = s.apps[:maxRecentApps]
	}
}

func (s *recentApps) list() []recentApp {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recentApp{}, s.apps...)
}

// windowsDirs returns the Windows directory as %SystemRoot% and %WINDIR%
// name it (usually the same folder; both are kept in case they differ). The
// environment is read on every call, which is cheap and lets tests set it.
// Without either variable no path counts as a system path.
func windowsDirs() []string {
	var dirs []string
	for _, name := range []string{"SystemRoot", "WINDIR"} {
		if dir := os.Getenv(name); dir != "" {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// isSystemPath reports whether path is a file below one of dirs, the Windows
// directory: the shell's own programs such as SearchHost.exe and
// StartMenuExperienceHost.exe (C:\Windows\SystemApps\...), which become the
// foreground when the user opens search or the Start menu. Paths compare
// case-insensitively, with either kind of separator, and with "." and ".."
// parts resolved, so C:\Apps\..\Windows\x.exe is a system path and
// C:\Windows\..\Apps\x.exe is not. It never touches the file system.
func isSystemPath(path string, dirs []string) bool {
	parts := windowsPathParts(path)
	for _, dir := range dirs {
		base := windowsPathParts(dir)
		if len(base) > 0 && len(parts) > len(base) && slices.Equal(parts[:len(base)], base) {
			return true
		}
	}
	return false
}

// windowsPathParts splits a Windows path into lower-case parts, resolving
// "." and ".." and dropping empty ones and the \\?\ extended-length prefix.
func windowsPathParts(path string) []string {
	path = strings.ReplaceAll(path, "/", `\`)
	path = strings.TrimPrefix(path, `\\?\`)
	var parts []string
	for part := range strings.SplitSeq(strings.ToLower(path), `\`) {
		switch part {
		case "", ".":
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		default:
			parts = append(parts, part)
		}
	}
	return parts
}

// noteForeground publishes a foreground executable the daemon accepted, after
// the shell-window filter, and remembers it for apps.recent unless it is a
// padmate UI or a program of Windows itself (H3: they are no program to bind
// a profile to). The event is published either way, flagged; the foreground
// still drives profile selection. It runs on the session thread's foreground
// callback, so it stays short: isOwnUI queries a process only when its PID
// registered as a UI, and Publish never waits for a client.
func (r *liveRun) noteForeground(app winfocus.App) {
	view := recentApp{Exe: app.Exe, Path: app.Path, Elevated: app.Elevated}
	own := r.isOwnUI(&app)
	system := isSystemPath(app.Path, windowsDirs())
	if !own && !system {
		r.recent.note(view)
	}
	r.events.Publish(foregroundTopic, foregroundEvent{recentApp: view, Own: own, System: system})
}

func (r *liveRun) appMethods(methods handler.Map) {
	methods["apps.recent"] = func(_ context.Context, req *jrpc2.Request) (any, error) {
		if err := req.UnmarshalParams(jrpc2.StrictFields(new(struct{}))); err != nil {
			return nil, err
		}
		return map[string][]recentApp{"apps": r.recent.list()}, nil
	}
}
