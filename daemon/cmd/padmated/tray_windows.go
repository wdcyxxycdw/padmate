package main

import (
	"fmt"
	"reflect"
	"sync"

	"fyne.io/systray"

	"github.com/wdcyxxycdw/padmate/daemon/internal/tray"
)

// systray locks the initial thread in init. This function is called only from
// the desktop main entry and runs its Win32 message loop on that thread.
func runNativeTray(r *liveRun) (code int) {
	// Establish a message queue even if systray initialization later fails.
	endMainLoop := tray.MainLoopExit()
	var pause, start *systray.MenuItem
	var details [4]*systray.MenuItem
	var previous tray.Presentation
	var oldPaused, oldStartup bool
	first := true
	var closeOnce sync.Once
	ui := &trayUI{notify: tray.Notify}
	registered := false
	var initMu sync.Mutex
	closing := false
	defer func() {
		// Only main calls Quit, after registration and controller cleanup.
		// A worker may request WM_QUIT before Run has even been entered.
		defer func() {
			if v := recover(); v != nil {
				fmt.Fprintln(r.cfg.stderr, "padmated: closing native tray:", v)
				code = 1
			}
		}()
		initMu.Lock()
		defer initMu.Unlock()
		closing = true
		if registered {
			systray.Quit()
		}
	}()
	ui.run = func(ready, exit func(), failed func(error)) {
		systray.Run(func() {
			// The library starts this callback on a worker. Early WM_QUIT
			// can end Run before it is scheduled; it must not build a menu
			// after main has deleted the notification icon.
			initMu.Lock()
			defer initMu.Unlock()
			if closing {
				return
			}
			defer func() {
				if v := recover(); v != nil {
					failed(fmt.Errorf("%v", v))
				}
			}()
			for i := range details {
				item := systray.AddMenuItem("正在读取状态…", "")
				item.Disable()
				details[i] = item
			}
			systray.AddSeparator()
			// No tooltips: the titles say it all.
			pause = systray.AddMenuItemCheckbox("暂停映射", "", false)
			start = systray.AddMenuItemCheckbox("登录时启动", "", false)
			if !r.startup.available() {
				start.Disable()
				if r.startup.development() {
					start.SetTitle(startupDevTitle)
				}
			}
			quit := systray.AddMenuItem("退出 padmate", "")
			ui.pause, ui.startup, ui.quit = pause.ClickedCh, start.ClickedCh, quit.ClickedCh
			// A double click opens the window; a single left click shows the
			// menu, once the double-click time has passed without a second
			// one (tray.Taps). The right button shows it at once.
			open := make(chan struct{}, 1)
			taps := &tray.Taps{
				Interval: tray.DoubleClickInterval(),
				Open: func() {
					// Right after the click this process may still give the
					// window it starts the foreground.
					_ = tray.AllowForeground()
					select {
					case open <- struct{}{}:
					default:
					}
				},
				Menu: func() {
					if err := tray.ShowMenu(); err != nil {
						r.log.Warn("tray menu unavailable", "err", err)
					}
				},
			}
			systray.SetOnTapped(taps.Tap)
			ui.open = open
			ready()
		}, exit)
		registered = true
	}
	ui.close = func() {
		closeOnce.Do(func() {
			// This is safe before registration as well as after init failure.
			// Daemon and controller cleanup is complete at this point.
			if err := endMainLoop(); err != nil {
				fmt.Fprintln(r.cfg.stderr, "padmated: closing tray loop:", err)
			}
		})
	}
	ui.update = func(p tray.Presentation, paused, enabled bool) {
		if first || p.Tooltip != previous.Tooltip {
			systray.SetTooltip(p.Tooltip)
		}
		if first || p.Badge != previous.Badge || p.Inactive != previous.Inactive {
			icon, err := tray.Icon(p.Badge, p.Inactive)
			if err != nil {
				r.log.Warn("tray icon unavailable", "err", err)
			} else {
				systray.SetIcon(icon)
			}
		}
		if first || !reflect.DeepEqual(p.Details, previous.Details) {
			for i, item := range details {
				if i < len(p.Details) {
					item.SetTitle(p.Details[i])
					item.Show()
				} else {
					item.Hide()
				}
			}
		}
		if first || paused != oldPaused {
			if paused {
				pause.Check()
				pause.SetTitle("恢复映射")
			} else {
				pause.Uncheck()
				pause.SetTitle("暂停映射")
			}
		}
		if first || enabled != oldStartup {
			if enabled {
				start.Check()
			} else {
				start.Uncheck()
			}
		}
		previous, oldPaused, oldStartup, first = p, paused, enabled, false
	}
	return r.driveTray(ui)
}
