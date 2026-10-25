package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
	"github.com/wdcyxxycdw/padmate/daemon/internal/tray"
)

// The low-battery alert (IN-12, ADR-0065): a controller that discharges down to
// lowBatteryPercent is announced once, on the controller itself, in the tray and
// on the overlay, and is announced again only after it was charged, plugged in
// or has climbed to batteryRearmPercent. The thresholds are fixed: the owner
// wants few controls, and the drivers report coarsely, so the numbers are
// chosen from the levels that SDL 3.4.16 reports:
//
//   - XInput (SDL_xinputjoystick.c, UpdateXInputJoystickBatteryInformation): a
//     wired pad is charging, an unknown or disconnected one is unknown, and the
//     rest discharge at 10 (empty), 40 (low), 70 (medium) or 100 (full).
//   - The Switch protocol (SDL_hidapi_switch.c): the driver's level is even,
//     0 (empty) to 8 (full), so the percent is 0, 25, 50, 75 or 100 (the
//     protocol's own names for the levels 2 and 4, critical and low, are not
//     in SDL); its charging bit is charging.
//   - PlayStation and other pads report finer steps.
//
// An alert at 25 or below fires on the XInput 10, on the Switch 25 (the 0 that
// follows is the same episode) and at 25 % on a pad with fine steps. The re-arm
// at 60 lies above the level a pad can rebound to after an alert (the XInput 40,
// the Switch 50), so a voltage rebound, which the alert's own rumble can cause,
// does not announce the pad again; the next level up (XInput 70, Switch 75) or a
// real recharge does, and charging or plugging in re-arms at once, whatever the
// charge.
const (
	lowBatteryPercent   = 25
	batteryRearmPercent = 60
	// maxBatteryPads bounds the controllers the tracker remembers; the oldest
	// goes first. A controller is remembered by its DeviceKey for as long as
	// padmated runs, so that a receiver's revival (a new connection ID) or a
	// profile switch does not announce it again.
	maxBatteryPads = 64
)

// batteryTracker is the single source of truth of the low-battery alert. It is
// fed with each controller's power (supervisor.Config.OnPower, on the router's
// goroutine) and answers, for the state event, which controllers are in an
// announced episode. It is keyed by the DeviceKey of the controller, as the
// text that devices.list shows, and knows nothing of profiles or slots: whether
// a controller is announced at all is a parameter of update.
type batteryTracker struct {
	mu      sync.Mutex
	pads    map[string]*batteryPad
	order   []string // by first sight
	episode uint64   // counts the alerts raised, of all controllers
}

type batteryPad struct {
	// armed: the next time the controller is low, it is an episode of its own.
	// A controller starts armed, an episode (announced or silent) disarms it,
	// and charging, plugging in or climbing to batteryRearmPercent arms it again.
	armed bool
	// active: an alert was raised for the current episode, and the controller
	// has not been re-armed since. It is what the state event calls "low".
	active bool
	// episode is the number of the alert raised for the current episode, when
	// active.
	episode uint64
}

func newBatteryTracker() *batteryTracker {
	return &batteryTracker{pads: map[string]*batteryPad{}}
}

// update takes the power p of the controller key and reports whether an alert
// is due now, with the number of the episode (unique within a run, for the
// tray's notice key). enabled is the global lowBattery switch (UI-27,
// ADR-0067), the same for every controller, with or without a slot.
//
//   - Due: the controller discharges, reports a charge of 0 to lowBatteryPercent,
//     is armed and enabled. One alert per episode.
//   - An episode that starts while the switch is off is silent and still counts:
//     the controller is disarmed, so turning the switch on, or switching to a
//     profile that has it on, does not announce it later; the next episode does.
//   - Re-armed by charging, wired, or a charge from batteryRearmPercent.
//   - unknown, and a charge that is not reported, change nothing and never
//     alert: nothing is known then.
func (t *batteryTracker) update(key string, p input.Power, enabled bool) (episode uint64, due bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	pad := t.pads[key]
	if pad == nil {
		if len(t.order) >= maxBatteryPads {
			delete(t.pads, t.order[0])
			t.order = t.order[1:]
		}
		pad = &batteryPad{armed: true}
		t.pads[key] = pad
		t.order = append(t.order, key)
	}
	switch {
	case p.State == input.PowerCharging || p.State == input.PowerWired || p.Percent >= batteryRearmPercent:
		pad.armed, pad.active, pad.episode = true, false, 0
	case p.State == input.PowerDischarging && p.Percent >= 0 && p.Percent <= lowBatteryPercent && pad.armed:
		pad.armed = false
		if !enabled {
			return 0, false
		}
		t.episode++
		pad.active, pad.episode = true, t.episode
		return t.episode, true
	}
	return 0, false
}

// low reports whether the controller key is in an announced episode.
func (t *batteryTracker) low(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	pad := t.pads[key]
	return pad != nil && pad.active
}

// episodeOf returns the number of the alert the controller key is in, 0 when it
// is in none (not low, or in a silent episode). The state event carries it, so
// that the overlay can tell an episode that began between two snapshots: a
// controller charged and low again within a snapshot's period is low at both,
// but not in the same episode.
func (t *batteryTracker) episodeOf(key string) uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if pad := t.pads[key]; pad != nil && pad.active {
		return pad.episode
	}
	return 0
}

// batteryNotice is the tray's balloon for episode of the controller key: the
// key tells the episodes apart, since the queue never forgets a key that is not
// OncePerRun.
func batteryNotice(key string, episode uint64, name string, percent int) tray.Notification {
	return tray.Notification{
		Key:   fmt.Sprintf("battery:%s:%d", key, episode),
		Title: "手柄电量低",
		Body:  fmt.Sprintf("%s 电量约 %d%%，请尽快充电。", name, percent),
	}
}

// batteryCue is the Alert-class effect on the controller itself: the pulses of
// three, and a red flash (the player lights on a controller without a color
// LED), each only when the global rumble and led switches have it on. Strength
// and the pulse times are the profile's the controller is mapped with, the
// defaults for one that holds no slot. ok is false when there is nothing to ask
// for.
func batteryCue(u supervisor.PowerUpdate, sw profile.FeedbackSwitches) (e input.Effect, ok bool) {
	fb := u.Feedback
	e = input.Effect{Device: u.Device, Class: haptic.Alert, Strength: uint16(fb.Strength), ShortMs: fb.ShortMs, GapMs: fb.GapMs, Blink: sw.LED}
	if sw.Rumble && fb.Strength > 0 {
		e.Preset = haptic.Pulse3
	}
	return e, e.Preset != 0 || e.Blink
}

// onPower is Config.OnPower: it runs on the router's goroutine, so it only
// decides, posts to the non-blocking queues, and logs.
func (r *liveRun) onPower(u supervisor.PowerUpdate) {
	key := u.Key.String()
	sw := r.feedbackSwitches()
	episode, due := r.battery.update(key, u.Power, sw.LowBattery)
	if !due {
		return
	}
	r.log.Info("controller battery low", "id", u.Device, "name", u.Name, "device", key,
		"percent", u.Power.Percent, "episode", episode, "mapped", u.Mapped)
	if cue, ok := batteryCue(u, sw); ok {
		r.effects.postEffect(cue)
	}
	// The tray shows it only when there is a tray: the queue is bounded and
	// never read otherwise.
	if r.cfg.opts.tray {
		r.notices.Add(batteryNotice(key, episode, u.Name, u.Power.Percent))
	}
}

// rpcBattery is one controller of the batteries of the engine.activity state
// event: its power as the router holds it, and whether it is in an announced
// low-battery episode ("low"), which is when the overlay tells it. Only
// controllers that have read a power are listed.
type rpcBattery struct {
	Device  input.DeviceID `json:"device"`
	Key     string         `json:"key"`
	Name    string         `json:"name"`
	State   string         `json:"state"`
	Percent *int           `json:"percent,omitempty"`
	Low     bool           `json:"low"`
	// Episode is the number of the alert a low controller is in, unique in a
	// run; it is left out while the controller is not low.
	Episode uint64 `json:"episode,omitempty"`
}

// batteries lists the controllers that have a power, with the tracker's "low".
// It is never nil, so that the event carries an empty array rather than null.
func (r *liveRun) batteries(ctx context.Context) []rpcBattery {
	out := []rpcBattery{}
	devices, err := r.sup.Devices(ctx)
	if err != nil {
		return out
	}
	for _, d := range devices {
		if d.Battery == nil {
			continue
		}
		b := rpcBattery{Device: d.Info.ID, Key: d.Key, Name: d.Info.Name, State: d.Battery.State.String(),
			Low: r.battery.low(d.Key), Episode: r.battery.episodeOf(d.Key)}
		if d.Battery.Percent >= 0 {
			pct := d.Battery.Percent
			b.Percent = &pct
		}
		out = append(out, b)
	}
	return out
}

// devicePowers returns the power each gamepad has read, by its DeviceKey, for
// the tray's menu.
func (r *liveRun) devicePowers(ctx context.Context) map[string]input.Power {
	devices, err := r.sup.Devices(ctx)
	if err != nil {
		return nil
	}
	power := make(map[string]input.Power, len(devices))
	for _, d := range devices {
		if d.Battery != nil {
			power[d.Key] = *d.Battery
		}
	}
	return power
}
