package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// fakeCapsEnv names the environment variable that sets what the fake
// gamepad of --fake-input=ipc can do: a comma-separated list of rumble,
// trigger (trigger rumble), led (a mono LED), rgb (a color LED, which is an
// LED too), player (player lights) or none. It is read when padmated starts.
const fakeCapsEnv = "PADMATE_FAKE_CAPS"

// defaultFakeCaps is what the fake gamepad can do when nothing says
// otherwise: everything an E2E needs to see an identify, and the common case
// of a pad with a color LED. It has no trigger rumble.
var defaultFakeCaps = input.Capabilities{Known: true, Rumble: true, LED: true, RGBLED: true, PlayerLED: true}

// parseFakeCaps reads a PADMATE_FAKE_CAPS list; an empty one is
// defaultFakeCaps.
func parseFakeCaps(list string) (input.Capabilities, error) {
	if strings.TrimSpace(list) == "" {
		return defaultFakeCaps, nil
	}
	caps := input.Capabilities{Known: true}
	for name := range strings.SplitSeq(list, ",") {
		switch strings.TrimSpace(name) {
		case "none":
		case "rumble":
			caps.Rumble = true
		case "trigger":
			caps.TriggerRumble = true
		case "led":
			caps.LED = true
		case "rgb":
			caps.LED, caps.RGBLED = true, true
		case "player":
			caps.PlayerLED = true
		default:
			return input.Capabilities{}, fmt.Errorf("%s: unknown capability %q (rumble, trigger, led, rgb, player or none)", fakeCapsEnv, name)
		}
	}
	return caps, nil
}

// fakeCapsFromEnv is parseFakeCaps of the environment.
func fakeCapsFromEnv() (input.Capabilities, error) { return parseFakeCaps(os.Getenv(fakeCapsEnv)) }

// rpcEffect is one effect call that the fake input received, as fake.effects
// shows it. An identify call carries the cues the fake gamepad answered and,
// for each cue, the program the real gamepad would have shown, since the fake
// has nothing to buzz or light: the buzzes (rumble), the colors of the LED
// (led) and the chase of the player lights (player).
type rpcEffect struct {
	Kind   string         `json:"kind"`
	Device input.DeviceID `json:"device"`

	// feedback
	Color    string `json:"color,omitempty"`
	Pulse    bool   `json:"pulse,omitempty"`
	Pulses   int    `json:"pulses,omitempty"`
	PulseMs  int    `json:"pulseMs,omitempty"`
	GapMs    int    `json:"gapMs,omitempty"`
	Strength uint16 `json:"strength,omitempty"`
	Paused   bool   `json:"paused,omitempty"`
	// PlayerMode is the mode the player lights show (HUD-08), absent when the
	// ambient state asks for none.
	PlayerMode int `json:"playerMode,omitempty"`

	// play: Preset is absent for an effect that only blinks (a low-battery
	// alert with rumble off) or plays the steps of a script's pad.rumble (in
	// Rumble), and Blink says that the lights flash with it. light and stop carry
	// the class too: light the color the class claims, or Release, and stop
	// whether it ended Everything the class had (the claims too) or its rumble.
	Class      string `json:"class,omitempty"`
	Preset     string `json:"preset,omitempty"`
	Blink      bool   `json:"blink,omitempty"`
	Release    bool   `json:"release,omitempty"`
	Everything bool   `json:"everything,omitempty"`

	// identify
	Cues   *haptic.Cues      `json:"cues,omitempty"`
	Rumble []rpcEffectStep   `json:"rumble,omitempty"`
	LED    []rpcEffectLED    `json:"led,omitempty"`
	Player []rpcEffectPlayer `json:"player,omitempty"`
}

// rpcEffectStep is one step of a rumble program: the strengths of the motors
// (absent for a rest) for ms milliseconds.
type rpcEffectStep struct {
	Low   uint16 `json:"low,omitempty"`
	High  uint16 `json:"high,omitempty"`
	Left  uint16 `json:"left,omitempty"`
	Right uint16 `json:"right,omitempty"`
	Ms    uint16 `json:"ms"`
}

// rpcEffectLED is one phase of an LED program: the color "#rrggbb", absent
// when the LED shows nothing of its own, for ms milliseconds.
type rpcEffectLED struct {
	Color string `json:"color,omitempty"`
	Ms    uint16 `json:"ms"`
}

// rpcEffectPlayer is one phase of a player-light program: the light of the
// player with this 0-based index shows for ms milliseconds.
type rpcEffectPlayer struct {
	Index int    `json:"index"`
	Ms    uint16 `json:"ms"`
}

// effectView describes a recorded call for fake.effects.
func effectView(c effectCall) rpcEffect {
	e := rpcEffect{Kind: c.Kind, Device: c.Device}
	switch c.Kind {
	case "feedback":
		f := c.Feedback
		e.Color, e.Pulse, e.Pulses, e.PulseMs, e.GapMs, e.Strength, e.Paused = f.Color, f.Pulse, f.Pulses, f.PulseMs, f.GapMs, f.Strength, f.Paused
		e.PlayerMode = f.PlayerMode
	case "play":
		e.Class, e.Strength, e.Blink = c.Effect.Class.String(), c.Effect.Strength, c.Effect.Blink
		if c.Effect.Preset.Valid() {
			e.Preset = c.Effect.Preset.String()
		}
		for _, s := range c.Effect.Steps {
			e.Rumble = append(e.Rumble, rpcEffectStep{Low: s.Low, High: s.High, Left: s.LeftTrig, Right: s.RightTrig, Ms: s.Ms})
		}
	case "light":
		e.Class, e.Release = c.Light.Class.String(), c.Light.Release
		if !c.Light.Release {
			e.Color = fmt.Sprintf("#%02x%02x%02x", c.Light.Color[0], c.Light.Color[1], c.Light.Color[2])
		}
	case "stop":
		e.Class, e.Everything = c.Stop.Class.String(), c.Stop.Everything
	case "identify":
		cues := c.Cues
		e.Cues = &cues
		if cues.Rumble {
			for _, s := range haptic.IdentifySteps() {
				e.Rumble = append(e.Rumble, rpcEffectStep{Low: s.Low, High: s.High, Left: s.LeftTrig, Right: s.RightTrig, Ms: s.Ms})
			}
		}
		if cues.LED {
			for _, p := range haptic.IdentifyFlashes() {
				phase := rpcEffectLED{Ms: p.Ms}
				if p.Show {
					phase.Color = fmt.Sprintf("#%02x%02x%02x", p.Value[0], p.Value[1], p.Value[2])
				}
				e.LED = append(e.LED, phase)
			}
		}
		if cues.Player {
			for _, p := range haptic.IdentifyChase() {
				e.Player = append(e.Player, rpcEffectPlayer{Index: p.Value, Ms: p.Ms})
			}
		}
	}
	return e
}

// fakeEffectsMethod adds fake.effects {clear?: bool} (IN-13, ADR-0065): the
// effect calls the fake input received, oldest first, as {effects: [...]}.
// With clear they are forgotten as they are returned. An E2E reads them to
// see what padmated asked of the controller, which the fake does not have.
func (r *liveRun) fakeEffectsMethod(methods handler.Map) {
	methods["fake.effects"] = func(_ context.Context, req *jrpc2.Request) (any, error) {
		src, err := r.fakeInput()
		if err != nil {
			return nil, err
		}
		var p struct {
			Clear bool `json:"clear"`
		}
		if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
			return nil, err
		}
		calls := src.effects.take(p.Clear)
		effects := make([]rpcEffect, len(calls))
		for i, c := range calls {
			effects[i] = effectView(c)
		}
		return map[string]any{"effects": effects}, nil
	}
}

// fakeBatteryMethod adds fake.battery {state, percent?} (IN-12, ADR-0065): the
// fake gamepad reports that power, as a real one's driver does. state is one of
// wired, charging, discharging and unknown; percent is 0 to 100 and is left out
// when the gamepad does not report a charge, which unknown always is. The
// low-battery alert, the tray's notice, the overlay's toast and the passive
// display can be seen without a battery.
func (r *liveRun) fakeBatteryMethod(methods handler.Map) {
	methods["fake.battery"] = func(ctx context.Context, req *jrpc2.Request) (any, error) {
		src, err := r.fakeInput()
		if err != nil {
			return nil, err
		}
		var p struct {
			State   string `json:"state"`
			Percent *int   `json:"percent"`
		}
		if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
			return nil, err
		}
		state, ok := input.ParsePowerState(p.State)
		if !ok {
			return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "state must be wired, charging, discharging or unknown"}
		}
		percent := -1
		if p.Percent != nil {
			percent = *p.Percent
			if percent < 0 || percent > 100 || state == input.PowerUnknown {
				return nil, &jrpc2.Error{Code: jrpc2.InvalidParams, Message: "percent must be 0 to 100, and an unknown state has none"}
			}
		}
		if err := src.power(input.Power{State: state, Percent: percent}); err != nil {
			return nil, err
		}
		if err := r.sup.Sync(ctx); err != nil {
			return nil, err
		}
		return map[string]any{"device": 1, "accepted": true}, nil
	}
}
