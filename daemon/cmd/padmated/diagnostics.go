package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/diagnostics"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/padlabel"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
	"github.com/wdcyxxycdw/padmate/daemon/internal/tray"
)

// rpcConflict is a program status.get reports as possibly holding the
// controller (IN-15). Message and Document, a file of the repository, are
// for developers reading the raw status ("padmatectl rpc status.get"); the
// UI words its notices from Process alone, as the installed app has no
// docs folder.
//
// Kind is "conflict" for a program that is running, and "steamInput" (IN-24,
// ADR-0071) for the Steam Input settings that may govern the connected
// gamepads while Steam runs: Classes lists them (diagnostics.SteamClass,
// "xbox", "playstation", "switch", "steamHardware"). padmate cannot see that
// Steam took a gamepad, so the entry says which settings may.
type rpcConflict struct {
	Kind     string   `json:"kind"`
	Process  string   `json:"process"`
	Message  string   `json:"message"`
	Document string   `json:"document"`
	Classes  []string `json:"classes,omitempty"`
}

// troubleshootingDoc is the document that conflicts point developers to.
const troubleshootingDoc = "docs/troubleshooting-input.md"

func (r *liveRun) notice(n tray.Notification) {
	if r.notices.Add(n) {
		r.log.Warn("compatibility notice", "kind", n.Key, "message", n.Body)
	}
}

func (r *liveRun) startDiagnostics() {
	ctx, cancel := context.WithCancel(r.end)
	r.diagnosticsStop = cancel
	r.diagnosticsDone = make(chan struct{})
	go func() {
		defer close(r.diagnosticsDone)
		defer func() {
			if v := recover(); v != nil {
				r.log.Error("diagnostics stopped", "panic", v)
			}
		}()
		scan := r.cfg.processes
		if scan == nil {
			scan = diagnostics.Processes
		}
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		var nextProcess time.Time
		// running is the programs of the last scan that can hold the
		// controller; scanned says there was one.
		var running []string
		scanned := false
		for ctx.Err() == nil {
			now := time.Now()
			if !now.Before(nextProcess) {
				names, err := scan()
				if ctx.Err() != nil {
					return
				}
				if err != nil {
					r.log.Debug("process diagnostics unavailable", "err", err)
				} else {
					running, scanned = diagnostics.ConflictingProcesses(names), true
					for _, name := range running {
						r.notice(conflictNotice(name))
					}
				}
				nextProcess = now.Add(5 * time.Second)
			}
			if scanned {
				// Every second, not with the scan: the gamepads come and go
				// between two scans.
				conflicts := r.conflictList(ctx, running)
				r.conflicts.Store(&conflicts)
			}
			r.checkMissingControls(ctx)
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
}

func (r *liveRun) stopDiagnostics() {
	if r.diagnosticsStop == nil {
		return
	}
	r.diagnosticsStop()
	select {
	case <-r.diagnosticsDone:
	case <-time.After(watchStopWait):
		r.log.Debug("diagnostics did not stop before shutdown")
	}
}

// conflictList is what status.get reports as possibly holding the controller
// (IN-15) while the programs running are running, as ConflictingProcesses
// names them: one entry for each, and, while Steam runs, one for the Steam
// Input settings that may govern the connected gamepads (IN-24).
func (r *liveRun) conflictList(ctx context.Context, running []string) []rpcConflict {
	conflicts := []rpcConflict{}
	for _, name := range running {
		conflicts = append(conflicts, rpcConflict{Kind: "conflict", Process: name, Message: name + " may intercept or duplicate controller mappings", Document: troubleshootingDoc})
	}
	if slices.Contains(running, "Steam") {
		if c, ok := r.steamInputConflict(ctx); ok {
			conflicts = append(conflicts, c)
		}
	}
	return conflicts
}

// steamInputConflict is the conflict of the Steam Input settings that may
// govern the gamepads padmate maps or could map (IN-24, ADR-0071), from their
// type, vendor, product and driver. A gamepad that padmate ignores is not one:
// Steam taking it over costs padmate nothing. It reports false when no class
// applies, or when the gamepads cannot be listed now.
func (r *liveRun) steamInputConflict(ctx context.Context) (rpcConflict, bool) {
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	devices, err := r.sup.Devices(ctx)
	if err != nil {
		return rpcConflict{}, false
	}
	var pads []diagnostics.SteamPad
	for _, d := range devices {
		if !d.Info.IsGamepad || d.Status == "ignored" || d.Status == "unmapped" {
			continue
		}
		pads = append(pads, diagnostics.SteamPad{Type: d.Info.Type, Vendor: d.Info.Vendor, Product: d.Info.Product, Driver: d.Info.Driver, Virtual: d.Info.Virtual})
	}
	classes := diagnostics.SteamClasses(pads)
	if len(classes) == 0 {
		return rpcConflict{}, false
	}
	return rpcConflict{Kind: "steamInput", Process: "Steam", Classes: classes, Document: troubleshootingDoc,
		Message: "Steam Input may take over the controllers of these kinds while Steam runs: " + strings.Join(classes, ", ")}, true
}

func (r *liveRun) checkMissingControls(ctx context.Context) {
	r.profileMu.Lock()
	prog, target := r.cfg.prog, r.targetLocked()
	r.profileMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	views, err := r.sup.Inspect(ctx)
	if err != nil || ctx.Err() != nil || r.profileTarget.Load() != target {
		return
	}
	for _, v := range views {
		if v.Device == 0 || v.Gen != target.gen || !v.Info.ControlsKnown() {
			continue
		}
		missing := missingControls(prog, v)
		if len(missing) == 0 {
			continue
		}
		r.notice(missingNotice(fmt.Sprintf("missing:%s:%x:%s", target.id, target.sum, v.Key), v.Info.Name, missing))
	}
}

// The tray's notices (DMN-13) are short and written-style, and name no
// file of the repository: the installed app has no docs folder.

// conflictNotice is the notice of name, a running program that may hold
// the controller (IN-15), as diagnostics.ConflictingProcesses names it.
func conflictNotice(name string) tray.Notification {
	advice := name + " 可能拦截手柄输入。如有异常，请先退出该程序。"
	switch name {
	case "Steam":
		advice = "Steam 输入可能抢占手柄或重复发送按键。如有异常，请完全退出 Steam。"
	case "DS4Windows":
		advice = "DS4Windows 可能另外创建一个虚拟手柄。如出现重复的手柄，请退出该程序。"
	case "HidHide":
		advice = "HidHide 可能对 padmate 隐藏手柄。请将 padmated.exe 加入其允许列表。"
	}
	return tray.Notification{Key: "conflict:" + name, OncePerRun: true, Title: name + " 正在运行", Body: advice}
}

// missingNotice is the notice of a gamepad, named pad, that lacks the
// controls missing, which the profile binds, named as missingControls
// names them; key tells it from the others.
func missingNotice(key, pad string, missing []string) tray.Notification {
	return tray.Notification{Key: key, Title: "手柄缺少配置中的按键", Body: pad + " 没有 " + strings.Join(missing, "、") + "。请改用其他按键。"}
}

// elevatedNotice is the notice of exe, a foreground program that runs as
// administrator, which Windows may not let padmate's keys reach (UIPI).
func elevatedNotice(exe string) tray.Notification {
	return tray.Notification{Key: "elevated", OncePerRun: true, Title: "前台程序以管理员身份运行",
		Body: exe + " 可能无法接收 padmate 发送的快捷键。请勿以管理员身份运行该程序。"}
}

// missingControls names the controls that prog binds and the gamepad of
// v lacks, as the mapping page names them for its family (padlabel, the
// owner's G11): LT, 背键 1, not lt, paddle1. A stick's directions and
// half axes are the stick, a trigger's half axes the trigger; each name
// comes once, in the order of the controls.
func missingControls(prog *engine.Program, v slot.View) []string {
	var missing []control.Control
	if prog == nil {
		return nil
	}
	for _, b := range prog.Bindings() {
		c := b.Binding.Control
		has := v.HasControl[c]
		if c >= control.LeftXPositive && c <= control.RightTriggerNegative {
			name := c.String()
			if a, ok := control.ParseAxis(name[:len(name)-1]); ok {
				has = v.HasAxis[a]
			}
		}
		if !has {
			missing = append(missing, c)
		}
	}
	slices.Sort(missing)
	pad := padlabel.Pad{Type: v.Info.Type, ButtonMask: v.Info.ButtonMask, Printed: map[string]string{
		"south": v.Info.ButtonLabels.South, "east": v.Info.ButtonLabels.East,
		"west": v.Info.ButtonLabels.West, "north": v.Info.ButtonLabels.North,
	}}
	var names []string
	for _, c := range missing {
		if name := padlabel.Name(c.String(), pad); !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

func (r *liveRun) showNotice(ui *trayUI, now time.Time, next *time.Time) {
	if ui.notify == nil || now.Before(*next) {
		return
	}
	if n, ok := r.notices.Take(); ok {
		if err := ui.notify(n); err != nil {
			r.log.Warn("tray notification unavailable", "kind", n.Key, "err", err)
		}
		*next = now.Add(5 * time.Second)
	}
}
