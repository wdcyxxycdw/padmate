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
// emergency combos needs (ADR-0017). A source without it, such as
// a test's fake, keeps the combos it picked.
type comboSource interface {
	// RepickCombos picks every gamepad's combo again on T-input, where the
	// combos are detected (ADR-0006).
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

// applyLiveSettings applies the global timing, the settings of each
// controller (function key, emergency combo, feedback switches), the stick
// calibrations and the gamepad mappings of next, the effective config just
// stored, where they differ from prev, the one before (ADR-0017, ADR-0015,
// ADR-0012). A controller's settings are compared one controller at a time, and
// a slot is touched only if its own controller's setting changed: another
// controller's keys are not let go, and its combo keeps counting.
// The caller holds configApplyMu; this takes profileMu after it, the only
// order the two are taken in.
func (r *liveRun) applyLiveSettings(prev, next *profile.Config) error {
	var errs []error
	if prev.Timing != next.Timing {
		r.profileMu.Lock()
		errs = append(errs, r.reapplyProfileSettingsLocked(next))
		r.profileMu.Unlock()
	}
	if !reflect.DeepEqual(functionKeySettings(prev), functionKeySettings(next)) && r.sup != nil {
		errs = append(errs, r.applyFunctionKeys())
	}
	if !reflect.DeepEqual(comboSettings(prev), comboSettings(next)) {
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
		// recognize and presses nothing.
		ctx, cancel := context.WithTimeout(context.Background(), calibrationTimeout)
		if err := r.sup.RefreshCalibration(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stick calibration not applied: %w", err))
		} else {
			r.log.Info("stick calibration applied", "devices", len(calibrations(next)))
		}
		cancel()
	}
	if !slices.Equal(profile.DeviceMappings(prev.Devices), profile.DeviceMappings(next.Devices)) {
		// Custom gamepad mappings: SDL reads them by GUID, at once.
		if err := r.applyMappings(next); err != nil {
			errs = append(errs, err)
		}
	}
	if !reflect.DeepEqual(feedbackSettings(prev), feedbackSettings(next)) && r.sup != nil {
		errs = append(errs, r.applyFeedbackSwitches(prev, next))
	}
	if prev.JoyCon2 != next.JoyCon2 {
		errs = append(errs, r.applyJoyCon2(next.JoyCon2))
	}
	return errors.Join(errs...)
}

// functionKeySettings is what decides the function keys of c: the function key
// each device sets, and the retired top-level one, which a controller with the
// standard layout that sets none still follows. Two configs that are equal in it
// give every controller the same function key.
func functionKeySettings(c *profile.Config) map[string]string {
	out := map[string]string{"": c.FunctionKeyOrDefault().String()}
	for key, d := range c.Devices {
		if d.FunctionKey != "" {
			out[key] = d.FunctionKey
		}
	}
	return out
}

// comboSettings is what decides the emergency combos of c: the retired
// safety.panic of the families and the combo each device sets.
func comboSettings(c *profile.Config) map[string]profile.PanicCombo {
	out := map[string]profile.PanicCombo{}
	for family, combo := range c.Safety.Panic {
		out["family:"+family] = combo
	}
	for key, d := range c.Devices {
		if d.Panic != nil {
			out["device:"+key] = *d.Panic
		}
	}
	return out
}

// feedbackSettings is what decides the feedback switches of c: the retired
// top-level switches and the members each device sets.
type feedbackInputs struct {
	Global  profile.FeedbackSwitches
	Devices map[string]profile.FeedbackOverrides
}

func feedbackSettings(c *profile.Config) feedbackInputs {
	out := feedbackInputs{Global: c.Feedback, Devices: map[string]profile.FeedbackOverrides{}}
	for key, d := range c.Devices {
		if f := d.Feedback; f != nil && (f.Rumble != nil || f.Led != nil || f.Pause != nil || f.LowBattery != nil) {
			out.Devices[key] = *f
		}
	}
	return out
}

// applyJoyCon2 makes the Joy-Con 2 backend follow joycon2.autoConnect of the
// config just stored. Controllers that are connected stay so; the scan follows
// the switch. A source without the backend has nothing to apply.
func (r *liveRun) applyJoyCon2(next profile.JoyCon2Settings) error {
	if !r.joyCon2Available() {
		return nil
	}
	if err := r.joyCon2Src().JoyCon2SetAutoConnect(next.AutoConnect); err != nil {
		return fmt.Errorf("joy-con 2 auto-connect not applied: %w", err)
	}
	r.log.Info("joy-con 2 auto-connect applied", "autoConnect", next.AutoConnect)
	r.joyCon2Kick()
	return nil
}

// calibrationTimeout bounds how long a committed config.json waits for the
// slots to apply the stick calibration or the feedback switches.
const calibrationTimeout = 10 * time.Second

// applyFunctionKeys has the slots follow the function keys of the effective
// config (ADR-0017): each asks for the key of its own controller and switches
// only if that is not the one it runs. The switch lets go of that slot's keys
// first (slot.Slot.RefreshFunctionKey), the held function key and the keys of
// controls pressed under it included (invariant 1), and the presses after it
// look up the new key's set; the other slots keep their keys, and the profile,
// its generation and its scripts stay as they are.
func (r *liveRun) applyFunctionKeys() error {
	ctx, cancel := context.WithTimeout(context.Background(), calibrationTimeout)
	defer cancel()
	if err := r.sup.RefreshFunctionKeys(ctx); err != nil {
		return fmt.Errorf("function keys not applied: %w", err)
	}
	r.log.Info("function keys applied")
	return nil
}

// onFunctionKey is Config.OnFunctionKey: a slot switched to the function key
// of its controller. It runs on the slot's goroutine, so it only logs. A slot
// that lost its controller (key "") goes back to the function key of a
// controller with no settings, which nobody uses until the next one comes, and
// says nothing.
func (r *liveRun) onFunctionKey(slot uint8, key string, prog *engine.Program) {
	if key == "" {
		return
	}
	r.log.Info("function key of the controller applied", "slot", slot, "device", key, "functionKey", prog.FunctionKey().String())
	r.logIgnoredBindings(prog, r.profileTarget.Load().id)
}

// applyFeedbackSwitches makes the running controllers follow the feedback
// switches of next, the effective config just stored, which the engines, the
// scripts' effects and the alert already read (they read the effective config at
// each use, each for its own controller). For a controller whose switches
// changed, a slot publishes its layer and pause state again, which gives the
// lights and the player lights back to the controller or takes them over, and
// what a switch gates and that is still going on is stopped, as switching the
// profile's switch off did before ADR-0015: with rumble off the rumble programs
// of scripts, haptics and the alert end, with lights off a script's color is
// given up. Turning a switch on starts nothing by itself; it applies from the
// next event. A controller whose switches did not change is not touched, and
// nothing is asked if no connected controller's did.
func (r *liveRun) applyFeedbackSwitches(prev, next *profile.Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), calibrationTimeout)
	defer cancel()
	devices, err := r.sup.Devices(ctx)
	if err != nil {
		return fmt.Errorf("feedback switches not applied: %w", err)
	}
	var changed []string
	for _, d := range devices {
		if !d.Info.IsGamepad || d.Key == "" {
			continue
		}
		was, _ := prev.PadFeedback(d.Key)
		now, _ := next.PadFeedback(d.Key)
		if was == now {
			continue
		}
		changed = append(changed, d.Key)
		if was.Rumble && !now.Rumble {
			for _, c := range []haptic.Class{haptic.Event, haptic.Script, haptic.Alert} {
				r.effects.postStop(input.Stop{Device: d.Info.ID, Class: c})
			}
		}
		if was.LED && !now.LED {
			r.effects.postLight(input.Light{Device: d.Info.ID, Class: haptic.Script, Release: true})
		}
	}
	if len(changed) == 0 {
		return nil
	}
	if ferr := r.sup.RefreshFeedback(ctx); ferr != nil {
		return fmt.Errorf("feedback switches not applied: %w", ferr)
	}
	r.log.Info("feedback switches applied", "devices", changed)
	return nil
}

// reapplyProfileSettingsLocked compiles the running profile again with the
// global timing of settings and, when the timing that results differs from the
// running one, switches every slot to it with Supervisor.Reload, as a save of
// the profile does: keys are released (invariant 1), scripts start again, the
// layer state and the session stay. A profile that sets all its timing itself
// keeps running untouched when only the timing changed. The function key is no
// part of it: it is a setting of each controller (ADR-0017), which the slots
// follow on their own (applyFunctionKeys). The caller holds profileMu.
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
	if timingMs(prog.Timing()) == timingMs(r.cfg.prog.Timing()) {
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
	r.log.Info("global settings applied", "profile", target.id, "gen", gen,
		"holdMs", t.HoldMs, "multiTapMs", t.MultiTapMs, "tapPressMs", t.TapPressMs, "tapGapMs", t.TapGapMs)
	r.logIgnoredFunctionKeyBindings(prog, target.id)
	return nil
}

// logIgnoredFunctionKeyBindings says once, at info level, for the Program that
// was just installed, that bindings on the function key a controller with the
// standard layout and no settings of its own has are left out (ADR-0012): they
// stay in the file, and take effect if the function key is changed in the
// settings. The controllers that have another function key say so when their
// slot switches (onFunctionKey). It is no warning: the profile is not wrong.
func (r *liveRun) logIgnoredFunctionKeyBindings(prog *engine.Program, profileID string) {
	if variant, err := prog.ForFunctionKey(r.padFunctionKey("", input.DeviceInfo{})); err == nil {
		prog = variant
	}
	r.logIgnoredBindings(prog, profileID)
}

// logIgnoredBindings is logIgnoredFunctionKeyBindings for prog as it is.
func (r *liveRun) logIgnoredBindings(prog *engine.Program, profileID string) {
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
