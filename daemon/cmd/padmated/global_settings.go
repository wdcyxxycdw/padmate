package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// comboSource is the part of an *input.Source that a change of the
// emergency combos needs (UI-19, ADR-0051). A source without it, such as
// a test's fake, keeps the combos it picked.
type comboSource interface {
	// RepickCombos picks every gamepad's combo again on T-input, where the
	// combos are detected (ADR-0025).
	RepickCombos() error
	// VerticalJoyCons is the grip ComboFor gets.
	VerticalJoyCons() bool
}

// liveComboFor returns the ComboFor of live mode: the combo of the
// effective config that current returns at each pick, so that a committed
// config.json applies at the next RepickCombos.
func liveComboFor(current func() *profile.Config) func(input.DeviceInfo, bool) input.Combo {
	return func(info input.DeviceInfo, vertical bool) input.Combo {
		_, c := effectiveCombo(current(), info, vertical)
		return c
	}
}

// rpcCombo is the emergency combo of a connected gamepad in status.get:
// the one T-input detects, computed as ComboFor computes it.
type rpcCombo struct {
	Device  input.DeviceID `json:"device"`
	Family  string         `json:"family"`
	Buttons []string       `json:"buttons"`
	HoldMs  int64          `json:"holdMs"`
}

// combos returns the emergency combo of each device of infos.
func (r *liveRun) combos(infos []input.DeviceInfo) []rpcCombo {
	vertical := false
	if src, ok := r.src.(comboSource); ok {
		vertical = src.VerticalJoyCons()
	}
	settings := r.selectionConfig()
	out := make([]rpcCombo, 0, len(infos))
	for _, info := range infos {
		family, c := effectiveCombo(settings, info, vertical)
		out = append(out, rpcCombo{Device: info.ID, Family: family, Buttons: c.Buttons, HoldMs: c.Hold.Milliseconds()})
	}
	return out
}

// applyLiveSettings applies the global timing, the function key, the emergency
// combos, the stick calibrations, the gamepad mappings and the feedback
// switches of next, the effective config just stored, where they differ from
// prev, the one before (UI-19, ADR-0051, IN-14, UI-27, ADR-0067, ADR-0075).
// The caller holds configApplyMu; this takes profileMu after it, the only
// order the two are taken in.
func (r *liveRun) applyLiveSettings(prev, next *profile.Config) error {
	var errs []error
	if prev.Timing != next.Timing || prev.FunctionKeyOrDefault() != next.FunctionKeyOrDefault() {
		r.profileMu.Lock()
		errs = append(errs, r.reapplyProfileSettingsLocked(next))
		r.profileMu.Unlock()
	}
	if !reflect.DeepEqual(prev.Safety, next.Safety) {
		if src, ok := r.src.(comboSource); ok {
			if err := src.RepickCombos(); err != nil {
				errs = append(errs, fmt.Errorf("emergency combo not applied: %w", err))
			} else {
				r.log.Info("emergency combos applied")
			}
		}
	}
	if !reflect.DeepEqual(calibrations(prev), calibrations(next)) && r.sup != nil {
		// A slot that holds a stick gives up what the new values no longer
		// recognize and presses nothing (IN-14).
		ctx, cancel := context.WithTimeout(context.Background(), calibrationTimeout)
		if err := r.sup.RefreshCalibration(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stick calibration not applied: %w", err))
		} else {
			r.log.Info("stick calibration applied", "devices", len(calibrations(next)))
		}
		cancel()
	}
	if !slices.Equal(profile.DeviceMappings(prev.Devices), profile.DeviceMappings(next.Devices)) {
		// Custom gamepad mappings (IN-18): SDL reads them by GUID, at once.
		if err := r.applyMappings(next); err != nil {
			errs = append(errs, err)
		}
	}
	if prev.Feedback != next.Feedback && r.sup != nil {
		errs = append(errs, r.applyFeedbackSwitches(prev.Feedback, next.Feedback))
	}
	return errors.Join(errs...)
}

// calibrationTimeout bounds how long a committed config.json waits for the
// slots to apply the stick calibration or the feedback switches.
const calibrationTimeout = 10 * time.Second

// applyFeedbackSwitches makes the running controllers follow the feedback
// switches of next, which the engines, the scripts' effects and the alert
// already read (they read the effective config at each use). A slot publishes
// its layer and pause state again, which gives the lights and the player lights
// back to the controller or takes them over, and what a switch gates and that
// is still going on is stopped, as switching the profile's switch off did
// before ADR-0067: with rumble off the rumble programs of scripts, haptics and
// the alert end, with lights off a script's color is given up. Turning a
// switch on starts nothing by itself; it applies from the next event.
func (r *liveRun) applyFeedbackSwitches(prev, next profile.FeedbackSwitches) error {
	ctx, cancel := context.WithTimeout(context.Background(), calibrationTimeout)
	defer cancel()
	var err error
	if prev.Rumble && !next.Rumble || prev.LED && !next.LED {
		devices, derr := r.sup.Devices(ctx)
		if derr != nil {
			err = fmt.Errorf("feedback switches not applied to the running effects: %w", derr)
		}
		for _, d := range devices {
			if prev.Rumble && !next.Rumble {
				for _, c := range []haptic.Class{haptic.Event, haptic.Script, haptic.Alert} {
					r.effects.postStop(input.Stop{Device: d.Info.ID, Class: c})
				}
			}
			if prev.LED && !next.LED {
				r.effects.postLight(input.Light{Device: d.Info.ID, Class: haptic.Script, Release: true})
			}
		}
	}
	if ferr := r.sup.RefreshFeedback(ctx); ferr != nil {
		return errors.Join(err, fmt.Errorf("feedback switches not applied: %w", ferr))
	}
	r.log.Info("feedback switches applied", "rumble", next.Rumble, "led", next.LED, "pause", next.Pause, "lowBattery", next.LowBattery)
	return err
}

// reapplyProfileSettingsLocked compiles the running profile again with the
// global timing and the function key of settings and, when the timing or the
// function key that results differs from the running one, switches every slot
// to it with Supervisor.Reload, as a save of the profile does: keys are
// released, held function key included (invariant 1), scripts start again, the
// layer state and the session stay. A press after it looks up the new function
// key's set. A profile that sets all its timing itself keeps running untouched
// when only the timing changed. The caller holds profileMu.
func (r *liveRun) reapplyProfileSettingsLocked(settings *profile.Config) error {
	target := r.targetLocked()
	data, err := r.runningDataLocked(target)
	if err != nil {
		r.log.Warn("global settings not applied to the running profile", "err", err)
		return fmt.Errorf("global settings not applied: %w", err)
	}
	p, prog, _, err := parseProfileIn(r.cfg.opts.library, target.id, target.path, data, io.Discard, settings)
	if err != nil {
		return fmt.Errorf("global settings not applied: %w", err) // not reached: it runs
	}
	if timingMs(prog.Timing()) == timingMs(r.cfg.prog.Timing()) && prog.FunctionKey() == r.cfg.prog.FunctionKey() {
		return nil
	}
	oldProfile := r.cfg.profile
	r.cfg.profile = p
	r.scriptDef.useProfile(p)
	gen, err := r.sup.Reload(context.Background(), prog)
	if err != nil {
		r.cfg.profile = oldProfile
		r.scriptDef.useProfile(oldProfile)
		r.log.Debug("global settings not applied: padmated is stopping", "err", err)
		return err
	}
	timing := arbiterTiming(prog, nil) // the tap timing; the event gap is config.json's
	if err := r.loop.Do(func(a *output.Arbiter) error { return a.SetTiming(timing.MinPress, timing.MinGap) }); err != nil {
		r.log.Debug("profile timing update stopped", "err", err)
	}
	r.cfg.prog = prog
	r.publishProfile(&liveProfile{id: target.id, path: target.path, sum: target.sum, gen: gen})
	t := prog.Timing()
	r.log.Info("global settings applied", "profile", target.id, "gen", gen, "functionKey", prog.FunctionKey().String(),
		"holdMs", t.HoldMs, "multiTapMs", t.MultiTapMs, "tapPressMs", t.TapPressMs, "tapGapMs", t.TapGapMs)
	r.logIgnoredFunctionKeyBindings(prog, target.id)
	return nil
}

// logIgnoredFunctionKeyBindings says once, at info level, for the Program that
// was just installed, that bindings on its function key are left out
// (ADR-0075): they stay in the file, and take effect if the function key is
// changed in the settings. It is no warning: the profile is not wrong.
func (r *liveRun) logIgnoredFunctionKeyBindings(prog *engine.Program, profileID string) {
	ignored := prog.IgnoredFunctionKeyBindings()
	if len(ignored) == 0 {
		return
	}
	r.log.Info("bindings on the function key are ignored", "profile", profileID, "functionKey", prog.FunctionKey().String(), "count", len(ignored), "bindings", ignored)
}

// timingMs is what a profile's resolved timing does: its four times, not
// whether a file or a default set them.
func timingMs(t profile.Timing) [4]int {
	return [4]int{t.HoldMs, t.MultiTapMs, t.TapPressMs, t.TapGapMs}
}

// runningDataLocked returns the bytes of the profile running, target:
// those it was last installed or reloaded from, or for the profile that
// startLive started with, its source read again, which must still have the
// sum that runs. The caller holds profileMu.
func (r *liveRun) runningDataLocked(target *liveProfile) ([]byte, error) {
	if r.running != nil {
		return r.running, nil
	}
	var data []byte
	var err error
	switch {
	case target.id != "" && r.cfg.opts.library != nil:
		data, err = r.cfg.opts.library.Read(target.id)
	case target.path != "":
		data, err = os.ReadFile(target.path)
	default:
		data = []byte(disabledProfile)
	}
	if err != nil {
		return nil, err
	}
	if sha256.Sum256(data) != r.sum {
		return nil, errors.New("the running profile changed on disk and was not reloaded")
	}
	return data, nil
}
