package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/tray"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

func TestMissingControlsUseDeviceMasksAndProfileBindings(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint",
"layers":{"base":{
"south":{"press":{"type":"keys","keys":"F13"}},
"rs.up":{"press":{"type":"keys","keys":"F14"}},
"rightx+":{"press":{"type":"keys","keys":"F15"}},
"lt":{"press":{"type":"keys","keys":"F16"}}}},
"common":{"paddle1":{"press":{"type":"keys","keys":"F17"}}}}`)
	tl, src := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	src.events <- input.Event{Kind: input.DeviceRemoved, Device: 1}
	src.events <- input.Event{Kind: input.DeviceAdded, Device: 2, Info: &input.DeviceInfo{ID: 2, Name: "limited pad", IsGamepad: true, ButtonMask: 1, AxisMask: 1 << 2}}
	tl.sync()
	views, err := tl.sup.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.Device != 2 {
			continue
		}
		// The names of the mapping page (DMN-13, the owner's G11), not the
		// control IDs lt, paddle1 and rs.up.
		got := missingControls(tl.cfg.prog, v)
		want := []string{"背键 1", "LT", "RS"}
		if !slices.Equal(got, want) {
			t.Fatalf("missing=%v, want %v", got, want)
		}
		tl.checkMissingControls(context.Background())
		tl.waitFor(t, tl.log, "limited pad 没有 背键 1、LT、RS。")
		// An Xbox pad's names; a stick's directions and half axes are one.
		v.Info.Type = "xboxone"
		if got := missingControls(tl.cfg.prog, v); !slices.Equal(got, []string{"背键 1", "LT", "RS"}) {
			t.Fatalf("missing=%v", got)
		}
		v.Info.Type, v.HasAxis = "ps5", [len(v.HasAxis)]bool{}
		if got := missingControls(tl.cfg.prog, v); !slices.Equal(got, []string{"右背键", "L2", "R3"}) {
			t.Fatalf("missing=%v", got)
		}
		return
	}
	t.Fatal("device not assigned")
}

func TestConflictAndElevationNoticesReachTrayOnce(t *testing.T) {
	tl, _ := sessionLive(t, func(c *liveConfig) { c.processes = func() ([]string, error) { return []string{"steam.exe"}, nil } }, nil, "--dry-run", "--watch=false")
	tl.waitFor(t, tl.log, "Steam")
	app := focusApp("admin.exe")
	app.Elevated = true
	tl.onForeground(app)
	tl.onForeground(app)
	got := make(chan tray.Notification, 8)
	ui := &trayUI{update: func(tray.Presentation, bool, bool) {}, notify: func(n tray.Notification) error { got <- n; return nil }}
	// Use the same delivery path as the tray, with a test clock.
	now := time.Now()
	var next time.Time
	tl.showNotice(ui, now, &next)
	tl.showNotice(ui, now, &next)
	if len(got) != 1 {
		t.Fatalf("notification spacing: %d", len(got))
	}
	tl.showNotice(ui, now.Add(5*time.Second), &next)
	if len(got) != 2 {
		t.Fatalf("missing notification: %d", len(got))
	}
	names := []string{(<-got).Key, (<-got).Key}
	slices.Sort(names)
	if !slices.Equal(names, []string{"conflict:Steam", "elevated"}) {
		t.Fatalf("notices=%v", names)
	}
	if st := tl.sup.Status(); st.Paused || st.Suspended != 0 {
		t.Fatal("warning changed mapping state")
	}
}

func TestElevationAndSecureDesktopResendFailedRelease(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "elevated", true: "secure_desktop"}[secure], func(t *testing.T) {
			sender := newGateSender("")
			tl, src := sessionLive(t, nil, func() (output.Sender, error) { return sender, nil }, "--inject", "--watch=false")
			button(src, "east", true)
			tl.waitSent(t, sender, "+Space")
			sender.setFail(true)
			if secure {
				tl.session.change(winsession.SigDesktopSwitch, 0, winsession.Desktop)
			} else {
				a := focusApp("admin.exe")
				a.Elevated = true
				tl.onForeground(a)
				button(src, "east", false)
			}
			tl.waitFor(t, tl.log, "gateSender: refused")
			// No keyboard injection is used: the sender models Windows refusing
			// releases until the ordinary input desktop is available again.
			sender.setFail(false)
			if secure {
				tl.session.change(winsession.SigDesktopSwitch, winsession.Desktop, 0)
			} else {
				tl.onForeground(focusApp("paint.exe"))
			}
			tl.waitSent(t, sender, "-Space")
			tl.sync()
			if got := sender.edges(); !slices.Equal(got, []string{"+Space", "-Space"}) {
				t.Fatalf("stale output replayed: %v", got)
			}
			if !secure && strings.Contains(tl.log.String(), `"mapping suspended"`) {
				t.Fatal("elevation warning suspended the mapping")
			}
		})
	}
}

// TestNoticesAreShortAndNameNoRepositoryFile: the tray's notices are
// written-style and short (DMN-13), and send the user to no docs folder,
// which the installed app does not have.
func TestNoticesAreShortAndNameNoRepositoryFile(t *testing.T) {
	notices := []tray.Notification{elevatedNotice("admin.exe"), missingNotice("missing:x", "pad", []string{"LT", "背键 1"})}
	for _, name := range []string{"Steam", "DS4Windows", "HidHide", "Other"} {
		notices = append(notices, conflictNotice(name))
	}
	for _, n := range notices {
		text := n.Title + n.Body
		if n.Title == "" || n.Body == "" || strings.Contains(text, "docs/") || strings.Contains(text, ".md") || len([]rune(n.Body)) > 60 {
			t.Errorf("notice %q: %q / %q", n.Key, n.Title, n.Body)
		}
	}
	if n := conflictNotice("Steam"); n.Key != "conflict:Steam" || !n.OncePerRun || n.Title != "Steam 正在运行" {
		t.Fatalf("conflict notice %+v", n)
	}
}
