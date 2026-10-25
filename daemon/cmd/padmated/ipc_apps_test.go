package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/ipc"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winfocus"
)

func TestRecentAppsOrderDedupeLimit(t *testing.T) {
	app := func(exe string) recentApp { return recentApp{Exe: exe, Path: `C:\apps\` + exe} }
	many := make([]recentApp, maxRecentApps+3)
	for i := range many {
		many[i] = app(fmt.Sprintf("app%02d.exe", i))
	}
	newest := make([]recentApp, 0, maxRecentApps)
	for i := len(many) - 1; len(newest) < maxRecentApps; i-- {
		newest = append(newest, many[i])
	}
	upper := recentApp{Exe: "A.EXE", Path: `C:\APPS\A.EXE`, Elevated: true}
	tests := []struct {
		name  string
		notes []recentApp
		want  []recentApp
	}{
		{"empty", nil, []recentApp{}},
		{"newest first", []recentApp{app("a.exe"), app("b.exe"), app("c.exe")}, []recentApp{app("c.exe"), app("b.exe"), app("a.exe")}},
		{"same path moves to the front with its latest values", []recentApp{app("a.exe"), app("b.exe"), upper}, []recentApp{upper, app("b.exe")}},
		{"repeat is one entry", []recentApp{app("a.exe"), app("a.exe"), app("a.exe")}, []recentApp{app("a.exe")}},
		{"same exe name in another folder stays distinct", []recentApp{app("a.exe"), {Exe: "a.exe", Path: `D:\tools\a.exe`}}, []recentApp{{Exe: "a.exe", Path: `D:\tools\a.exe`}, app("a.exe")}},
		{"keeps the newest twelve", many, newest},
		{"a re-seen old app returns after eviction", append(append([]recentApp{}, many...), many[0]), append([]recentApp{many[0]}, newest[:maxRecentApps-1]...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s recentApps
			for _, a := range tt.notes {
				s.note(a)
			}
			got := s.list()
			if got == nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
			if len(got) > 0 {
				got[0].Exe = "changed.exe"
				if s.list()[0].Exe == "changed.exe" {
					t.Fatal("list shares storage with the caller")
				}
			}
		})
	}
}

// appsTestRun is a liveRun with only IPC and a registered UI process: pid 42
// at uiPath.
func appsTestRun(t *testing.T) (*liveRun, string) {
	t.Helper()
	const uiPath = `C:\padmate\padmate.exe`
	r := &liveRun{events: ipc.NewHub(), log: slog.New(slog.DiscardHandler)}
	r.cfg.ownAlive = func(pid uint32, path string, created uint64) bool { return pid == 42 && path == uiPath && created == 7 }
	r.cfg.ownForeground = func(pid uint32, path string) bool { return pid == 42 && path == uiPath }
	r.ownWindows.Store(&[]ownUI{{owner: 1, pid: 42, path: uiPath, created: 7}})
	return r, uiPath
}

func TestForegroundEventAndRecentApps(t *testing.T) {
	// No Windows directory is known here, so C:\Windows\notepad.exe below is
	// an ordinary program whatever machine runs the test.
	t.Setenv("SystemRoot", "")
	t.Setenv("WINDIR", "")
	r, uiPath := appsTestRun(t)
	events := make(chan string, 16)
	client := rpcTestClient(t, r, &jrpc2.ClientOptions{OnNotify: func(req *jrpc2.Request) {
		if req.Method() == foregroundTopic {
			events <- req.ParamString()
		}
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Before any subscription the event is dropped, but the app is kept.
	r.noteForeground(winfocus.App{Window: 3, Class: "Early", Exe: "early.exe", Path: `C:\apps\early.exe`, PID: 99})
	if err := client.CallResult(ctx, "subscribe", map[string]any{"topics": []string{foregroundTopic}}, new(any)); err != nil {
		t.Fatal(err)
	}
	foreground := []struct {
		app  winfocus.App
		want string
	}{
		{winfocus.App{Window: 5, Class: "PaintMain", Exe: "paint.exe", Path: `C:\apps\paint.exe`, PID: 100, Elevated: true},
			`{"exe":"paint.exe","path":"C:\\apps\\paint.exe","elevated":true,"own":false}`},
		{winfocus.App{Window: 6, Class: "Chrome_WidgetWin_1", Exe: "padmate.exe", Path: uiPath, PID: 42},
			`{"exe":"padmate.exe","path":"C:\\padmate\\padmate.exe","elevated":false,"own":true}`},
		// Another process from the UI's image is not the registered UI.
		{winfocus.App{Window: 7, Class: "Chrome_WidgetWin_1", Exe: "padmate.exe", Path: uiPath, PID: 43},
			`{"exe":"padmate.exe","path":"C:\\padmate\\padmate.exe","elevated":false,"own":false}`},
		{winfocus.App{Window: 8, Class: "Notepad", Exe: "notepad.exe", Path: `C:\Windows\notepad.exe`, PID: 101},
			`{"exe":"notepad.exe","path":"C:\\Windows\\notepad.exe","elevated":false,"own":false}`},
	}
	for _, f := range foreground {
		r.noteForeground(f.app)
		select {
		case got := <-events:
			if got != f.want {
				t.Fatalf("event %s, want %s", got, f.want)
			}
		case <-ctx.Done():
			t.Fatal("no app.foreground event for", f.app.Exe)
		}
	}
	var recent struct {
		Apps []map[string]any `json:"apps"`
	}
	if err := client.CallResult(ctx, "apps.recent", map[string]any{}, &recent); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"exe": "notepad.exe", "path": `C:\Windows\notepad.exe`, "elevated": false},
		{"exe": "padmate.exe", "path": uiPath, "elevated": false},
		{"exe": "paint.exe", "path": `C:\apps\paint.exe`, "elevated": true},
		{"exe": "early.exe", "path": `C:\apps\early.exe`, "elevated": false},
	}
	if !reflect.DeepEqual(recent.Apps, want) {
		t.Fatalf("apps.recent %v, want %v", recent.Apps, want)
	}
}

func TestRecentAppsRPCParams(t *testing.T) {
	r, _ := appsTestRun(t)
	client := rpcTestClient(t, r)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, params := range []any{nil, map[string]any{}} {
		var got json.RawMessage
		if err := client.CallResult(ctx, "apps.recent", params, &got); err != nil {
			t.Fatal(params, err)
		}
		if string(got) != `{"apps":[]}` {
			t.Fatalf("empty recent list %s", got)
		}
	}
	for _, params := range []any{map[string]any{"limit": 3}, map[string]any{"device": ""}, []any{}, []any{1}} {
		var rpcErr *jrpc2.Error
		err := client.CallResult(ctx, "apps.recent", params, new(any))
		if !errors.As(err, &rpcErr) || rpcErr.Code != jrpc2.InvalidParams {
			t.Fatalf("params %v: %v", params, err)
		}
	}
}

func TestOnForegroundPublishesAppForeground(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{}}}`)
	tl, _ := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	events := make(chan string, 16)
	client := rpcTestClient(t, tl.liveRun, &jrpc2.ClientOptions{OnNotify: func(req *jrpc2.Request) {
		if req.Method() == foregroundTopic {
			events <- req.ParamString()
		}
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.CallResult(ctx, "subscribe", map[string]any{"topics": ipc.Topics}, new(any)); err != nil {
		t.Fatal(err)
	}
	shell := focusApp("explorer.exe")
	shell.Class = "Shell_TrayWnd"
	tl.onForeground(shell)
	tl.onForeground(focusApp("krita.exe"))
	select {
	case got := <-events:
		if want := `{"exe":"krita.exe","path":"C:\\apps\\krita.exe","elevated":false,"own":false}`; got != want {
			t.Fatalf("event %s, want %s", got, want)
		}
	case <-ctx.Done():
		t.Fatal("no app.foreground event")
	}
	var recent struct {
		Apps []recentApp `json:"apps"`
	}
	if err := client.CallResult(ctx, "apps.recent", nil, &recent); err != nil {
		t.Fatal(err)
	}
	if want := []recentApp{{Exe: "krita.exe", Path: `C:\apps\krita.exe`}}; !reflect.DeepEqual(recent.Apps, want) {
		t.Fatalf("apps.recent %v, want %v (the shell window is filtered)", recent.Apps, want)
	}
}

func TestIsSystemPath(t *testing.T) {
	const (
		searchHost = `C:\Windows\SystemApps\MicrosoftWindows.Client.CBS_cw5n1h2txyewy\SearchHost.exe`
		startMenu  = `C:\Windows\SystemApps\Microsoft.Windows.StartMenuExperienceHost_cw5n1h2txyewy\StartMenuExperienceHost.exe`
	)
	win := []string{`C:\Windows`}
	tests := []struct {
		name string
		path string
		dirs []string
		want bool
	}{
		{"search host in SystemApps", searchHost, win, true},
		{"start menu host in SystemApps", startMenu, win, true},
		{"system32", `C:\Windows\System32\notepad.exe`, win, true},
		{"directly in the directory", `C:\Windows\explorer.exe`, win, true},
		{"case-insensitive", `c:\WINDOWS\system32\Cmd.EXE`, win, true},
		{"forward slashes", `C:/Windows/System32/cmd.exe`, win, true},
		{"mixed separators and doubled ones", `C:\Windows//System32\\cmd.exe`, win, true},
		{"dot parts are resolved", `C:\Windows\.\System32\..\explorer.exe`, win, true},
		{"parent parts that climb into it", `C:\Apps\..\Windows\explorer.exe`, win, true},
		{"parent parts that climb out of it", `C:\Windows\..\Apps\paint.exe`, win, false},
		{"extended-length prefix", `\\?\C:\Windows\System32\cmd.exe`, win, true},
		{"directory given with a trailing separator", `C:\Windows\explorer.exe`, []string{`C:\Windows\`}, true},
		{"directory given in other case and slashes", `C:\Windows\explorer.exe`, []string{`c:/WINDOWS`}, true},
		{"directory itself is no file below it", `C:\Windows`, win, false},
		{"sibling with the same prefix", `C:\WindowsApps\paint.exe`, win, false},
		{"folder that only starts with the name", `C:\Windows.old\Windows\paint.exe`, win, false},
		{"same folder name on another drive", `D:\Windows\paint.exe`, win, false},
		{"folder named Windows deeper down", `C:\Apps\Windows\paint.exe`, win, false},
		{"program files", `C:\Program Files\Krita\bin\krita.exe`, win, false},
		{"padmate itself is not special here", `C:\apps\padmate.exe`, win, false},
		{"unc path", `\\server\Windows\paint.exe`, win, false},
		{"relative path", `Windows\paint.exe`, win, false},
		{"empty path", ``, win, false},
		{"no directory known", searchHost, nil, false},
		{"empty directory entry never matches everything", `C:\Apps\paint.exe`, []string{``}, false},
		{"second directory matches", `D:\Windows\System32\cmd.exe`, []string{`C:\Windows`, `D:\Windows`}, true},
		{"windows installed elsewhere", `E:\WINNT\explorer.exe`, []string{`E:\WINNT`}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSystemPath(tt.path, tt.dirs); got != tt.want {
				t.Fatalf("isSystemPath(%q, %q) = %v, want %v", tt.path, tt.dirs, got, tt.want)
			}
		})
	}
}

func TestWindowsDirsFromEnvironment(t *testing.T) {
	tests := []struct {
		name         string
		root, windir string
		want         []string
	}{
		{"both set", `C:\Windows`, `C:\WINDOWS`, []string{`C:\Windows`, `C:\WINDOWS`}},
		{"only SystemRoot", `C:\Windows`, ``, []string{`C:\Windows`}},
		{"only WINDIR", ``, `C:\Windows`, []string{`C:\Windows`}},
		{"neither", ``, ``, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SystemRoot", tt.root)
			t.Setenv("WINDIR", tt.windir)
			if got := windowsDirs(); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("windowsDirs() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSystemProcessesAreNotRemembered covers H3: Windows' own programs (the
// search and Start surfaces live in C:\Windows\SystemApps) become the
// foreground like any program, so the event still reaches the UI, flagged,
// but apps.recent never lists them.
func TestSystemProcessesAreNotRemembered(t *testing.T) {
	t.Setenv("SystemRoot", `C:\Windows`)
	t.Setenv("WINDIR", `C:\WINDOWS`)
	r, _ := appsTestRun(t)
	events := make(chan string, 16)
	client := rpcTestClient(t, r, &jrpc2.ClientOptions{OnNotify: func(req *jrpc2.Request) {
		if req.Method() == foregroundTopic {
			events <- req.ParamString()
		}
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.CallResult(ctx, "subscribe", map[string]any{"topics": []string{foregroundTopic}}, new(any)); err != nil {
		t.Fatal(err)
	}
	foreground := []struct {
		app  winfocus.App
		want string
	}{
		{winfocus.App{Window: 5, Exe: "SearchHost.exe", Path: `C:\Windows\SystemApps\MicrosoftWindows.Client.CBS_cw5n1h2txyewy\SearchHost.exe`, PID: 100},
			`{"exe":"SearchHost.exe","path":"C:\\Windows\\SystemApps\\MicrosoftWindows.Client.CBS_cw5n1h2txyewy\\SearchHost.exe","elevated":false,"own":false,"system":true}`},
		{winfocus.App{Window: 6, Exe: "StartMenuExperienceHost.exe", Path: `c:\windows\systemapps\Microsoft.Windows.StartMenuExperienceHost_cw5n1h2txyewy\StartMenuExperienceHost.exe`, PID: 101},
			`{"exe":"StartMenuExperienceHost.exe","path":"c:\\windows\\systemapps\\Microsoft.Windows.StartMenuExperienceHost_cw5n1h2txyewy\\StartMenuExperienceHost.exe","elevated":false,"own":false,"system":true}`},
		{winfocus.App{Window: 7, Exe: "krita.exe", Path: `C:\apps\krita.exe`, PID: 102},
			`{"exe":"krita.exe","path":"C:\\apps\\krita.exe","elevated":false,"own":false}`},
		{winfocus.App{Window: 8, Exe: "notepad.exe", Path: `C:/Windows/notepad.exe`, PID: 103},
			`{"exe":"notepad.exe","path":"C:/Windows/notepad.exe","elevated":false,"own":false,"system":true}`},
	}
	for _, f := range foreground {
		r.noteForeground(f.app)
		select {
		case got := <-events:
			if got != f.want {
				t.Fatalf("event %s, want %s", got, f.want)
			}
		case <-ctx.Done():
			t.Fatal("no app.foreground event for", f.app.Exe)
		}
	}
	var recent struct {
		Apps []recentApp `json:"apps"`
	}
	if err := client.CallResult(ctx, "apps.recent", nil, &recent); err != nil {
		t.Fatal(err)
	}
	if want := []recentApp{{Exe: "krita.exe", Path: `C:\apps\krita.exe`}}; !reflect.DeepEqual(recent.Apps, want) {
		t.Fatalf("apps.recent %v, want %v", recent.Apps, want)
	}
}

// TestSystemForegroundStillDrivesSelection pins what H3 leaves alone: the
// foreground of a system program is evaluated like any other.
func TestSystemForegroundStillDrivesSelection(t *testing.T) {
	t.Setenv("SystemRoot", `C:\Windows`)
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{}}}`)
	tl, _ := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	system := winfocus.App{Window: 4, PID: 200, Exe: "SearchHost.exe", Path: `C:\Windows\SystemApps\MicrosoftWindows.Client.CBS_cw5n1h2txyewy\SearchHost.exe`}
	tl.onForeground(system)
	got := tl.foreground.Load()
	if got == nil || got.Path != system.Path {
		t.Fatalf("foreground %v, want the system program", got)
	}
	if recent := tl.recent.list(); len(recent) != 0 {
		t.Fatalf("recent %v, want none", recent)
	}
}
